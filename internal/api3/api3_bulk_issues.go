package api3

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func (h *Handler) bulkIssueRoute(w http.ResponseWriter, r *http.Request, path string) {
	switch {
	case path == "issues/delete" && r.Method == http.MethodPost:
		h.submitBulkDelete(w, r)
	case path == "issues/move" && r.Method == http.MethodPost:
		h.submitBulkMove(w, r)
	case path == "issues/fields" && r.Method == http.MethodGet:
		h.bulkEditableFields(w, r)
	case path == "issues/fields" && r.Method == http.MethodPost:
		h.submitBulkEdit(w, r)
	case (path == "issues/watch" || path == "issues/unwatch") && r.Method == http.MethodPost:
		h.submitBulkWatch(w, r, path == "issues/watch")
	case strings.HasPrefix(path, "queue/") && r.Method == http.MethodGet:
		h.bulkOperationProgress(w, r, strings.TrimPrefix(path, "queue/"))
	default:
		jiraError(w, http.StatusNotFound, "The bulk operation does not exist.")
	}
}

type bulkMoveTargetRequest struct {
	InferClassificationDefaults bool              `json:"inferClassificationDefaults"`
	InferFieldDefaults          bool              `json:"inferFieldDefaults"`
	InferStatusDefaults         bool              `json:"inferStatusDefaults"`
	InferSubtaskTypeDefault     bool              `json:"inferSubtaskTypeDefault"`
	IssueIDsOrKeys              []string          `json:"issueIdsOrKeys"`
	TargetClassification        []json.RawMessage `json:"targetClassification"`
	TargetMandatoryFields       []json.RawMessage `json:"targetMandatoryFields"`
	TargetStatus                []struct {
		Statuses map[string][]string `json:"statuses"`
	} `json:"targetStatus"`
}

func (h *Handler) submitBulkMove(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		SendBulkNotification bool                             `json:"sendBulkNotification"`
		TargetToSources      map[string]bulkMoveTargetRequest `json:"targetToSourcesMapping"`
	}
	if !decodeBulkOperationBody(w, r, &request) {
		return
	}
	if len(request.TargetToSources) == 0 {
		bulkOperationError(w, http.StatusBadRequest, "targetToSourcesMapping is required")
		return
	}
	seen := map[string]bool{}
	items := make([]store.BulkIssueMoveTaskItem, 0)
	for target, mapping := range request.TargetToSources {
		parts := strings.Split(target, ",")
		if len(parts) < 2 || len(parts) > 3 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			bulkOperationError(w, http.StatusBadRequest, "target mapping keys must use project,issueType[,parent]")
			return
		}
		project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, strings.TrimSpace(parts[0]))
		if err != nil {
			bulkOperationError(w, http.StatusBadRequest, "A destination project is invalid or inaccessible")
			return
		}
		issueType, err := h.Store.IssueTypeByIDOrName(r.Context(), strings.TrimSpace(parts[1]))
		if err != nil {
			bulkOperationError(w, http.StatusBadRequest, "A destination issue type is invalid")
			return
		}
		parentID := ""
		if len(parts) == 3 && strings.TrimSpace(parts[2]) != "" {
			parent, parentErr := h.resolveIssue(r, workspaceID, strings.TrimSpace(parts[2]))
			if parentErr != nil || parent.ProjectID != project.ID || parent.IssueType.Subtask {
				bulkOperationError(w, http.StatusBadRequest, "A destination parent is invalid or inaccessible")
				return
			}
			parentID = parent.ID
		}
		if issueType.Subtask != (parentID != "") {
			bulkOperationError(w, http.StatusBadRequest, "A destination parent is required only for sub-task issue types")
			return
		}
		if len(mapping.TargetClassification) > 0 || len(mapping.TargetMandatoryFields) > 0 {
			bulkOperationError(w, http.StatusBadRequest, "classification and mandatory-field move mappings are not supported yet")
			return
		}
		statusMappings := map[string]string{}
		for _, statusGroup := range mapping.TargetStatus {
			for destination, sources := range statusGroup.Statuses {
				if _, err := h.Store.StatusByIDForProject(r.Context(), destination, project.ID); err != nil {
					bulkOperationError(w, http.StatusBadRequest, "A destination status is invalid for the target project")
					return
				}
				for _, source := range sources {
					if source == "" || statusMappings[source] != "" {
						bulkOperationError(w, http.StatusBadRequest, "Source statuses must have one destination mapping")
						return
					}
					statusMappings[source] = destination
				}
			}
		}
		if len(mapping.IssueIDsOrKeys) == 0 {
			bulkOperationError(w, http.StatusBadRequest, "Each target mapping must contain issueIdsOrKeys")
			return
		}
		for _, idOrKey := range mapping.IssueIDsOrKeys {
			issue, issueErr := h.resolveIssue(r, workspaceID, strings.TrimSpace(idOrKey))
			if issueErr != nil || seen[issue.ID] {
				bulkOperationError(w, http.StatusBadRequest, "Some issueIdsOrKeys are invalid, inaccessible, or repeated")
				return
			}
			seen[issue.ID] = true
			items = append(items, store.BulkIssueMoveTaskItem{
				BulkIssueTaskItem: store.BulkIssueTaskItem{ID: issue.ID, JiraID: issue.JiraID},
				ProjectID:         project.ID, IssueTypeID: issueType.ID, ParentID: parentID,
				InferStatusDefaults: mapping.InferStatusDefaults, StatusMappings: statusMappings,
			})
			if len(items) > 1000 {
				bulkOperationError(w, http.StatusBadRequest, "No more than 1,000 issues can be moved")
				return
			}
		}
	}
	task, err := h.Store.EnqueueBulkMoveTask(r.Context(), workspaceID, actorID, items, request.SendBulkNotification)
	if errors.Is(err, store.ErrBulkTaskLimit) {
		bulkOperationError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not submit the bulk operation.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"taskId": task.ID})
}

func (h *Handler) submitBulkDelete(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		SelectedIssueIDsOrKeys []string `json:"selectedIssueIdsOrKeys"`
		SendBulkNotification   bool     `json:"sendBulkNotification"`
	}
	if !decodeBulkOperationBody(w, r, &request) {
		return
	}
	if len(request.SelectedIssueIDsOrKeys) < 1 || len(request.SelectedIssueIDsOrKeys) > 1000 {
		bulkOperationError(w, http.StatusBadRequest, "selectedIssueIdsOrKeys must contain between 1 and 1000 issues")
		return
	}
	seen := make(map[string]bool, len(request.SelectedIssueIDsOrKeys))
	issues := make([]store.BulkIssueTaskItem, 0, len(request.SelectedIssueIDsOrKeys))
	for _, idOrKey := range request.SelectedIssueIDsOrKeys {
		idOrKey = strings.TrimSpace(idOrKey)
		normalized := strings.ToLower(idOrKey)
		if idOrKey == "" || seen[normalized] {
			bulkOperationError(w, http.StatusBadRequest, "selectedIssueIdsOrKeys must contain unique non-empty issue IDs or keys")
			return
		}
		seen[normalized] = true
		issue, issueErr := h.resolveIssue(r, workspaceID, idOrKey)
		if issueErr != nil {
			bulkOperationError(w, http.StatusBadRequest, "Some of the issues in selectedIssueIdsOrKeys are invalid or inaccessible")
			return
		}
		issues = append(issues, store.BulkIssueTaskItem{ID: issue.ID, JiraID: issue.JiraID})
	}
	task, err := h.Store.EnqueueBulkDeleteTask(r.Context(), workspaceID, actorID, issues, request.SendBulkNotification)
	if errors.Is(err, store.ErrBulkTaskLimit) {
		bulkOperationError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not submit the bulk operation.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"taskId": task.ID})
}

type bulkEditableField struct {
	ID                      string           `json:"id"`
	Name                    string           `json:"name"`
	Type                    string           `json:"type"`
	Description             string           `json:"description,omitempty"`
	IsRequired              bool             `json:"isRequired"`
	FieldOptions            []map[string]any `json:"fieldOptions,omitempty"`
	MultiSelectFieldOptions []string         `json:"multiSelectFieldOptions,omitempty"`
	SearchURL               string           `json:"searchUrl,omitempty"`
	UnavailableMessage      string           `json:"unavailableMessage,omitempty"`
}

func (h *Handler) bulkEditableFields(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	values := splitBulkIssueQuery(r.URL.Query()["issueIdsOrKeys"])
	issues, selectionErr := h.resolveBulkIssues(r, workspaceID, values)
	if selectionErr != nil {
		bulkOperationError(w, http.StatusBadRequest, selectionErr.Error())
		return
	}
	metadata, err := h.Store.IssueCreateMetadata(r.Context(), workspaceID, userID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not read bulk editable fields.")
		return
	}
	fields := commonBulkEditableFields(metadata, issues, h.BaseURL)
	search := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("searchText")))
	if search != "" {
		filtered := fields[:0]
		for _, field := range fields {
			if strings.Contains(strings.ToLower(field.ID), search) || strings.Contains(strings.ToLower(field.Name), search) {
				filtered = append(filtered, field)
			}
		}
		fields = filtered
	}
	if len(fields) == 0 {
		bulkOperationError(w, http.StatusNotFound, "No editable fields were found for the selected issues")
		return
	}
	start, cursorErr := bulkFieldPageStart(r, len(fields))
	if cursorErr != nil {
		bulkOperationError(w, http.StatusBadRequest, cursorErr.Error())
		return
	}
	end := min(start+50, len(fields))
	response := map[string]any{"fields": fields[start:end]}
	if start > 0 {
		response["endingBefore"] = encodeBulkFieldCursor(start)
	}
	if end < len(fields) {
		response["startingAfter"] = encodeBulkFieldCursor(end)
	}
	writeJSON(w, http.StatusOK, response)
}

func splitBulkIssueQuery(values []string) []string {
	var out []string
	for _, value := range values {
		out = append(out, strings.Split(value, ",")...)
	}
	return out
}

func (h *Handler) resolveBulkIssues(r *http.Request, workspaceID string, values []string) ([]*models.Issue, error) {
	if len(values) < 1 || len(values) > 1000 {
		return nil, errors.New("issueIdsOrKeys must contain between 1 and 1000 issues")
	}
	seen := make(map[string]bool, len(values))
	issues := make([]*models.Issue, 0, len(values))
	for _, idOrKey := range values {
		idOrKey = strings.TrimSpace(idOrKey)
		normalized := strings.ToLower(idOrKey)
		if idOrKey == "" || seen[normalized] {
			return nil, errors.New("issueIdsOrKeys must contain unique non-empty issue IDs or keys")
		}
		seen[normalized] = true
		issue, issueErr := h.resolveIssue(r, workspaceID, idOrKey)
		if issueErr != nil {
			return nil, errors.New("some of the issues in issueIdsOrKeys are invalid or inaccessible")
		}
		issues = append(issues, issue)
	}
	return issues, nil
}

func commonBulkEditableFields(metadata *models.IssueCreateMetadata, issues []*models.Issue, baseURL string) []bulkEditableField {
	projects := make(map[string]bool)
	for _, issue := range issues {
		projects[issue.ProjectID] = true
	}
	projectKeys := make([]string, 0, len(projects))
	for _, project := range metadata.Projects {
		if projects[project.Project.ID] {
			projectKeys = append(projectKeys, project.Project.Key)
		}
	}
	sort.Strings(projectKeys)
	type candidate struct {
		field   bulkEditableField
		seen    int
		options map[string]map[string]any
	}
	candidates := map[string]*candidate{}
	for _, project := range metadata.Projects {
		if !projects[project.Project.ID] {
			continue
		}
		for _, source := range project.Fields {
			fieldType := bulkEditableFieldType(source)
			if fieldType == "" {
				continue
			}
			entry := candidates[source.ID]
			if entry == nil {
				entry = &candidate{field: bulkEditableField{
					ID: source.ID, Name: source.Name, Type: fieldType,
					Description: source.Description, IsRequired: source.Required,
				}, options: map[string]map[string]any{}}
				if fieldType == "assignee" {
					entry.field.SearchURL = baseURL + "/rest/api/3/user/assignable/multiProjectSearch?projectKeys=" + url.QueryEscape(strings.Join(projectKeys, ",")) + "&query="
				}
				if fieldType == "components" || fieldType == "labels" || fieldType == "versions" {
					entry.field.MultiSelectFieldOptions = []string{"ADD", "REMOVE", "REPLACE", "REMOVE_ALL"}
				}
				candidates[source.ID] = entry
			}
			entry.seen++
			for _, option := range source.Options {
				entry.options[option.ID+"\x00"+option.Name] = bulkFieldOption(fieldType, option)
			}
		}
	}
	fields := make([]bulkEditableField, 0, len(candidates))
	for _, entry := range candidates {
		if entry.seen != len(projects) {
			continue
		}
		keys := make([]string, 0, len(entry.options))
		for key := range entry.options {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			entry.field.FieldOptions = append(entry.field.FieldOptions, entry.options[key])
		}
		if len(entry.field.FieldOptions) == 0 && (entry.field.Type == "components" || entry.field.Type == "versions") {
			entry.field.UnavailableMessage = "The selected projects do not have values available for this field."
		}
		fields = append(fields, entry.field)
	}
	sort.Slice(fields, func(i, j int) bool {
		left, right := strings.ToLower(fields[i].Name), strings.ToLower(fields[j].Name)
		if left == right {
			return fields[i].ID < fields[j].ID
		}
		return left < right
	})
	return fields
}

func bulkEditableFieldType(field models.CreateFieldMeta) string {
	switch field.Type {
	case "user":
		return "assignee"
	case "priority":
		return "priority"
	case "components":
		return "components"
	case "versions":
		return "versions"
	case "array":
		return "labels"
	case "doc":
		return "richText"
	case "number":
		return "number"
	case "datetime":
		return "dateTime"
	case "securitylevel":
		return "singleSelect"
	case "string":
		return "singleLineText"
	default:
		return ""
	}
}

func bulkFieldOption(fieldType string, option models.CreateFieldOption) map[string]any {
	switch fieldType {
	case "assignee":
		return map[string]any{"accountId": option.ID, "displayName": option.Name}
	case "priority":
		return map[string]any{"id": option.ID, "priority": option.Name}
	default:
		return map[string]any{"id": option.ID, "name": option.Name}
	}
}

func encodeBulkFieldCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte("bulk-fields:" + strconv.Itoa(offset)))
}

func decodeBulkFieldCursor(cursor string) (int, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || !strings.HasPrefix(string(raw), "bulk-fields:") {
		return 0, errors.New("invalid bulk field cursor")
	}
	offset, err := strconv.Atoi(strings.TrimPrefix(string(raw), "bulk-fields:"))
	if err != nil || offset < 0 {
		return 0, errors.New("invalid bulk field cursor")
	}
	return offset, nil
}

func bulkFieldPageStart(r *http.Request, total int) (int, error) {
	startingAfter, endingBefore := r.URL.Query().Get("startingAfter"), r.URL.Query().Get("endingBefore")
	if startingAfter != "" && endingBefore != "" {
		return 0, errors.New("startingAfter and endingBefore cannot be used together")
	}
	if startingAfter != "" {
		offset, err := decodeBulkFieldCursor(startingAfter)
		if err != nil || offset > total {
			return 0, errors.New("invalid startingAfter cursor")
		}
		return offset, nil
	}
	if endingBefore != "" {
		offset, err := decodeBulkFieldCursor(endingBefore)
		if err != nil || offset > total {
			return 0, errors.New("invalid endingBefore cursor")
		}
		return max(0, offset-50), nil
	}
	return 0, nil
}

func bulkOperationError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"errors": []map[string]string{{"message": message}}})
}

func decodeBulkOperationBody(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		bulkOperationError(w, http.StatusBadRequest, "Invalid bulk operation request: "+err.Error())
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		bulkOperationError(w, http.StatusBadRequest, "Invalid bulk operation request: expected one JSON object")
		return false
	}
	return true
}

func (h *Handler) submitBulkWatch(w http.ResponseWriter, r *http.Request, watch bool) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		SelectedIssueIDsOrKeys []string `json:"selectedIssueIdsOrKeys"`
	}
	if !decodeBulkOperationBody(w, r, &request) {
		return
	}
	if len(request.SelectedIssueIDsOrKeys) < 1 || len(request.SelectedIssueIDsOrKeys) > 1000 {
		bulkOperationError(w, http.StatusBadRequest, "selectedIssueIdsOrKeys must contain between 1 and 1000 issues")
		return
	}
	seen := make(map[string]bool, len(request.SelectedIssueIDsOrKeys))
	issues := make([]store.BulkIssueTaskItem, 0, len(request.SelectedIssueIDsOrKeys))
	for _, idOrKey := range request.SelectedIssueIDsOrKeys {
		idOrKey = strings.TrimSpace(idOrKey)
		if idOrKey == "" || seen[strings.ToLower(idOrKey)] {
			bulkOperationError(w, http.StatusBadRequest, "selectedIssueIdsOrKeys must contain unique non-empty issue IDs or keys")
			return
		}
		seen[strings.ToLower(idOrKey)] = true
		issue, issueErr := h.resolveIssue(r, workspaceID, idOrKey)
		if issueErr != nil {
			bulkOperationError(w, http.StatusBadRequest, "Some of the issues in selectedIssueIdsOrKeys are invalid or inaccessible")
			return
		}
		issues = append(issues, store.BulkIssueTaskItem{ID: issue.ID, JiraID: issue.JiraID})
	}
	task, err := h.Store.EnqueueBulkWatchTask(r.Context(), workspaceID, actorID, issues, watch)
	if errors.Is(err, store.ErrBulkTaskLimit) {
		bulkOperationError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not submit the bulk operation.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"taskId": task.ID})
}

func (h *Handler) bulkOperationProgress(w http.ResponseWriter, r *http.Request, taskID string) {
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if taskID == "" || strings.Contains(taskID, "/") {
		bulkOperationError(w, http.StatusBadRequest, "The task associated with this taskId is not a bulk operation task")
		return
	}
	task, err := h.Store.APITaskByID(r.Context(), workspaceID, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		bulkOperationError(w, http.StatusBadRequest, "The task associated with this taskId is not a bulk operation task")
		return
	}
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not read the bulk operation.")
		return
	}
	if !task.IsBulkIssueOperation() {
		bulkOperationError(w, http.StatusBadRequest, "The task associated with this taskId is not a bulk operation task")
		return
	}
	bean := map[string]any{
		"taskId": task.ID, "status": task.Status, "progressPercent": task.Progress,
		"submittedBy": map[string]string{"accountId": task.SubmittedBy},
		"created":     task.SubmittedAt.UnixMilli(), "updated": task.LastUpdateAt.UnixMilli(),
	}
	if task.StartedAt != nil {
		bean["started"] = task.StartedAt.UnixMilli()
	}
	if len(task.Result) > 0 && string(task.Result) != "null" {
		var result map[string]any
		if json.Unmarshal(task.Result, &result) == nil {
			for key, value := range result {
				bean[key] = value
			}
		}
	}
	writeJSON(w, http.StatusOK, bean)
}
