package api3

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/store"
)

// Archiving, redaction, bulk issue properties and issue panel pins.

// issueArchivalRoute serves PUT /issue/archive, POST /issue/archive (by JQL)
// and PUT /issue/unarchive.
func (h *Handler) issueArchivalRoute(w http.ResponseWriter, r *http.Request, restore bool) {
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if r.Method == http.MethodPost && !restore {
		h.archiveIssuesByJQL(w, r, workspaceID, actorID)
		return
	}
	if r.Method != http.MethodPut {
		methodNotAllowed(w)
		return
	}
	var request struct {
		IssueIDsOrKeys []string `json:"issueIdsOrKeys"`
	}
	if !decodeMetadataRequest(w, r, &request) {
		return
	}
	if len(request.IssueIDsOrKeys) == 0 {
		jiraError(w, http.StatusBadRequest, "At least one issue ID or key is required.")
		return
	}
	refs, truncated := request.IssueIDsOrKeys, false
	if len(refs) > 1000 {
		refs, truncated = refs[:1000], true
	}
	outcome := store.NewIssueArchivalOutcome()
	if admin, err := h.Store.IsAdmin(r.Context(), workspaceID, actorID); err != nil || !admin {
		outcome.RejectIssueArchival("userDoesNotHavePermission", refs)
	} else if err = h.Store.ChangeIssueArchival(r.Context(), workspaceID, actorID, refs, !restore, &outcome); err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	status := http.StatusOK
	switch {
	case outcome.Updated == 0:
		status = http.StatusBadRequest
	case truncated:
		status = http.StatusPreconditionFailed
	}
	writeJSON(w, status, map[string]any{"numberOfIssuesUpdated": outcome.Updated, "errors": outcome.Errors})
}

func (h *Handler) archiveIssuesByJQL(w http.ResponseWriter, r *http.Request, workspaceID, actorID string) {
	if !h.requireJiraAdmin(w, r, workspaceID, actorID) {
		return
	}
	var request struct {
		JQL string `json:"jql"`
	}
	if !decodeMetadataRequest(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.JQL) == "" {
		jiraError(w, http.StatusBadRequest, "A JQL query is required.")
		return
	}
	compiled, e := h.compileJQL(r.Context(), workspaceID, request.JQL, actorID)
	if e != nil {
		writeJerr(w, e)
		return
	}
	issues, _, err := h.Store.Search(r.Context(), workspaceID, actorID, compiled, 100000, 0)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "The JQL query could not be run.")
		return
	}
	ids := make([]string, 0, len(issues))
	for _, issue := range issues {
		ids = append(ids, issue.ID)
	}
	task, err := h.Store.EnqueueIssueArchive(r.Context(), workspaceID, actorID, ids)
	if errors.Is(err, store.ErrIssueArchiveRunning) {
		jiraError(w, http.StatusPreconditionFailed, "A request to archive issues is already running.")
		return
	}
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusAccepted, h.BaseURL+"/rest/api/3/task/"+task.ID)
}

var archiveDatePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// exportArchivedIssues serves PUT /issues/archive/export.
func (h *Handler) exportArchivedIssues(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w)
		return
	}
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if !h.requireJiraAdmin(w, r, workspaceID, actorID) {
		return
	}
	var request struct {
		ArchivedBy        []string `json:"archivedBy"`
		ArchivedDateRange *struct {
			DateAfter  string `json:"dateAfter"`
			DateBefore string `json:"dateBefore"`
		} `json:"archivedDateRange"`
		IssueTypes []string `json:"issueTypes"`
		Projects   []string `json:"projects"`
		Reporters  []string `json:"reporters"`
	}
	if !decodeMetadataRequest(w, r, &request) {
		return
	}
	filter := store.ArchivedIssuesFilter{ArchivedBy: request.ArchivedBy, Projects: request.Projects, Reporters: request.Reporters}
	if request.ArchivedDateRange != nil {
		for _, date := range []string{request.ArchivedDateRange.DateAfter, request.ArchivedDateRange.DateBefore} {
			if _, err := time.Parse("2006-01-02", date); !archiveDatePattern.MatchString(date) || err != nil {
				jiraError(w, http.StatusBadRequest, "archivedDateRange needs dateAfter and dateBefore in YYYY-MM-DD format.")
				return
			}
		}
		filter.DateAfter, filter.DateBefore = request.ArchivedDateRange.DateAfter, request.ArchivedDateRange.DateBefore
	}
	ids := h.issueTypeIDsFor(r, workspaceID)
	for _, wireID := range request.IssueTypes {
		internal := ids.toInternal(wireID)
		if internal == wireID && numericID(wireID) {
			if _, err := h.Store.IssueTypeInWorkspace(r.Context(), workspaceID, wireID); err != nil {
				jiraError(w, http.StatusBadRequest, "The issue type "+wireID+" does not exist.")
				return
			}
		}
		filter.IssueTypes = append(filter.IssueTypes, internal)
	}
	task, err := h.Store.EnqueueArchivedIssuesExport(r.Context(), workspaceID, actorID, filter)
	if errors.Is(err, store.ErrArchivedIssueExportRunning) {
		jiraError(w, http.StatusPreconditionFailed, "A request to export archived issues is already running.")
		return
	}
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	payload, _ := json.Marshal(request)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"taskId": task.ID, "status": task.Status, "progress": task.Progress, "payload": string(payload),
		"submittedTime": task.SubmittedAt.UTC().Format(time.RFC3339), "fileUrl": "",
	})
}

// ArchivedIssuesExportFile serves an archived issue export's CSV to the person
// who requested it.
func (h *Handler) ArchivedIssuesExportFile(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	taskID := strings.TrimSuffix(r.PathValue("file"), ".csv")
	content, err := h.Store.ArchivedIssueExport(r.Context(), workspaceID, taskID, actorID)
	if err != nil {
		jiraError(w, http.StatusNotFound, "The export was not found.")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="archived-issues.csv"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(content))
}

var redactionExternalIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// redactRoute serves POST /redact and GET /redact/status/{jobId}.
func (h *Handler) redactRoute(w http.ResponseWriter, r *http.Request, jobID string) {
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if jobID != "" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		task, err := h.Store.RedactionJob(r.Context(), workspaceID, jobID)
		if err != nil {
			jiraError(w, http.StatusNotFound, "The redaction job was not found.")
			return
		}
		body := map[string]any{}
		switch task.Status {
		case "ENQUEUED":
			body["jobStatus"] = "PENDING"
		case "RUNNING":
			body["jobStatus"] = "IN_PROGRESS"
		default:
			body["jobStatus"] = "COMPLETED"
			var result struct {
				Results []store.RedactionResult `json:"results"`
			}
			_ = json.Unmarshal(task.Result, &result)
			if result.Results == nil {
				result.Results = []store.RedactionResult{}
			}
			body["bulkRedactionResponse"] = map[string]any{"results": result.Results}
		}
		writeJSON(w, http.StatusOK, body)
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !h.requireJiraAdmin(w, r, workspaceID, actorID) {
		return
	}
	var request struct {
		Redactions []struct {
			ContentItem *struct {
				EntityID   string `json:"entityId"`
				EntityType string `json:"entityType"`
				ID         string `json:"id"`
			} `json:"contentItem"`
			ExternalID        string `json:"externalId"`
			Reason            string `json:"reason"`
			RedactionPosition *struct {
				ADFPointer   string `json:"adfPointer"`
				ExpectedText string `json:"expectedText"`
				From         *int   `json:"from"`
				To           *int   `json:"to"`
			} `json:"redactionPosition"`
		} `json:"redactions"`
	}
	if !decodeMetadataRequest(w, r, &request) {
		return
	}
	if len(request.Redactions) == 0 {
		jiraError(w, http.StatusBadRequest, "At least one redaction is required.")
		return
	}
	requests := make([]store.RedactionRequest, 0, len(request.Redactions))
	seen := map[string]bool{}
	for index, item := range request.Redactions {
		prefix := "redactions[" + strconv.Itoa(index) + "]"
		switch {
		case !redactionExternalIDPattern.MatchString(item.ExternalID) || seen[item.ExternalID]:
			jiraFieldError(w, http.StatusBadRequest, map[string]string{prefix + ".externalId": "The external ID must be a unique UUID."})
			return
		case strings.TrimSpace(item.Reason) == "":
			jiraFieldError(w, http.StatusBadRequest, map[string]string{prefix + ".reason": "A reason is required."})
			return
		case item.ContentItem == nil || item.ContentItem.EntityID == "" || item.ContentItem.ID == "" ||
			(item.ContentItem.EntityType != "issuefieldvalue" && item.ContentItem.EntityType != "issue-comment" && item.ContentItem.EntityType != "issue-worklog"):
			jiraFieldError(w, http.StatusBadRequest, map[string]string{prefix + ".contentItem": "contentItem needs an entityType, entityId and issue id."})
			return
		case item.RedactionPosition == nil || item.RedactionPosition.ExpectedText == "" || item.RedactionPosition.From == nil || item.RedactionPosition.To == nil ||
			*item.RedactionPosition.From < 0 || *item.RedactionPosition.To <= *item.RedactionPosition.From:
			jiraFieldError(w, http.StatusBadRequest, map[string]string{prefix + ".redactionPosition": "redactionPosition needs expectedText and a from before to."})
			return
		}
		seen[item.ExternalID] = true
		requests = append(requests, store.RedactionRequest{
			ExternalID: item.ExternalID, Reason: item.Reason,
			EntityType: item.ContentItem.EntityType, EntityID: item.ContentItem.EntityID, IssueRef: item.ContentItem.ID,
			ADFPointer: item.RedactionPosition.ADFPointer, ExpectedText: item.RedactionPosition.ExpectedText,
			From: *item.RedactionPosition.From, To: *item.RedactionPosition.To,
		})
	}
	task, err := h.Store.EnqueueRedaction(r.Context(), workspaceID, actorID, requests)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusAccepted, task.ID)
}

func validPropertyValue(value json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(value))
	return trimmed != "" && trimmed != "null" && json.Valid([]byte(trimmed)) && len([]rune(trimmed)) <= 32768
}

// issuePropertiesBulkRoute serves the four bulk issue property operations.
func (h *Handler) issuePropertiesBulkRoute(w http.ResponseWriter, r *http.Request, path string) {
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	mapIssueIDs := func(jiraIDs []int64) ([]string, bool) {
		mapped, err := h.Store.IssueIDsByJiraIDs(r.Context(), workspaceID, jiraIDs)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return nil, false
		}
		ids := []string{}
		for _, jiraID := range jiraIDs {
			if id, ok := mapped[jiraID]; ok {
				ids = append(ids, id)
			}
		}
		return ids, true
	}
	var request store.IssuePropertyBulkRequest
	switch {
	case path == "/issue/properties" && r.Method == http.MethodPost:
		var body struct {
			EntitiesIDs []int64                    `json:"entitiesIds"`
			Properties  map[string]json.RawMessage `json:"properties"`
		}
		if !decodeMetadataRequest(w, r, &body) {
			return
		}
		if len(body.EntitiesIDs) == 0 || len(body.EntitiesIDs) > 10000 || len(body.Properties) == 0 || len(body.Properties) > 10 {
			jiraError(w, http.StatusBadRequest, "Up to 10 properties can be set on up to 10000 issues.")
			return
		}
		for key, value := range body.Properties {
			if strings.TrimSpace(key) == "" || len([]rune(key)) > 255 || !validPropertyValue(value) {
				jiraError(w, http.StatusBadRequest, "The property "+key+" has an invalid key or value.")
				return
			}
		}
		ids, ok := mapIssueIDs(body.EntitiesIDs)
		if !ok {
			return
		}
		request = store.IssuePropertyBulkRequest{Mode: "set", IssueIDs: ids, Properties: body.Properties}
	case path == "/issue/properties/multi" && r.Method == http.MethodPost:
		var body struct {
			Issues []struct {
				IssueID    int64                      `json:"issueID"`
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"issues"`
		}
		if !decodeMetadataRequest(w, r, &body) {
			return
		}
		if len(body.Issues) == 0 || len(body.Issues) > 100 {
			jiraError(w, http.StatusBadRequest, "Properties can be set on up to 100 issues.")
			return
		}
		jiraIDs := []int64{}
		for _, item := range body.Issues {
			if len(item.Properties) == 0 || len(item.Properties) > 10 {
				jiraError(w, http.StatusBadRequest, "Each issue can have up to 10 properties set.")
				return
			}
			for key, value := range item.Properties {
				if strings.TrimSpace(key) == "" || len([]rune(key)) > 255 || !validPropertyValue(value) {
					jiraError(w, http.StatusBadRequest, "The property "+key+" has an invalid key or value.")
					return
				}
			}
			jiraIDs = append(jiraIDs, item.IssueID)
		}
		mapped, err := h.Store.IssueIDsByJiraIDs(r.Context(), workspaceID, jiraIDs)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		request = store.IssuePropertyBulkRequest{Mode: "multi"}
		for _, item := range body.Issues {
			if id, ok := mapped[item.IssueID]; ok {
				request.PerIssue = append(request.PerIssue, store.IssuePropertyBulkItem{IssueID: id, Properties: item.Properties})
			}
		}
	case strings.HasPrefix(path, "/issue/properties/") && (r.Method == http.MethodPut || r.Method == http.MethodDelete):
		key := strings.TrimPrefix(path, "/issue/properties/")
		if strings.TrimSpace(key) == "" || len([]rune(key)) > 255 {
			jiraError(w, http.StatusBadRequest, "The property key is invalid.")
			return
		}
		var body struct {
			Expression string          `json:"expression"`
			Value      json.RawMessage `json:"value"`
			Filter     *struct {
				CurrentValue json.RawMessage `json:"currentValue"`
				EntityIDs    []int64         `json:"entityIds"`
				HasProperty  *bool           `json:"hasProperty"`
			} `json:"filter"`
			CurrentValue json.RawMessage `json:"currentValue"`
			EntityIDs    []int64         `json:"entityIds"`
		}
		if !decodeMetadataRequest(w, r, &body) {
			return
		}
		request = store.IssuePropertyBulkRequest{Mode: "delete-filtered", Key: key, CurrentValue: body.CurrentValue}
		entityIDs := body.EntityIDs
		if r.Method == http.MethodPut {
			if strings.TrimSpace(body.Expression) != "" {
				jiraError(w, http.StatusBadRequest, "Setting a property from a Jira expression isn't supported; send the value instead.")
				return
			}
			if !validPropertyValue(body.Value) {
				jiraError(w, http.StatusBadRequest, "The property value must be valid, non-empty JSON of at most 32768 characters.")
				return
			}
			request = store.IssuePropertyBulkRequest{Mode: "set-filtered", Key: key, Value: body.Value}
			entityIDs = nil
			if body.Filter != nil {
				request.CurrentValue, request.HasProperty, entityIDs = body.Filter.CurrentValue, body.Filter.HasProperty, body.Filter.EntityIDs
			}
		}
		if entityIDs != nil {
			ids, ok := mapIssueIDs(entityIDs)
			if !ok {
				return
			}
			request.IssueIDs = ids
		}
	default:
		methodNotAllowed(w)
		return
	}
	task, err := h.Store.EnqueueIssuePropertiesTask(r.Context(), workspaceID, actorID, request)
	if errors.Is(err, store.ErrIssuePropertyTaskConflict) {
		jiraError(w, http.StatusConflict, "Another bulk update on the same issues is already in progress.")
		return
	}
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("Location", h.BaseURL+"/rest/api/3/task/"+task.ID)
	writeJSON(w, http.StatusSeeOther, h.apiTaskBean(task))
}

// issuePanelPins serves POST /forge/panel/action/bulk/async.
func (h *Handler) issuePanelPins(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if !h.requireJiraAdmin(w, r, workspaceID, actorID) {
		return
	}
	var request struct {
		ModuleID    string                      `json:"moduleId"`
		ProjectList []store.IssuePanelPinAction `json:"projectList"`
	}
	if !decodeMetadataRequest(w, r, &request) {
		return
	}
	if request.ModuleID == "" || len(request.ProjectList) == 0 {
		jiraError(w, http.StatusBadRequest, "moduleId and projectList are required.")
		return
	}
	for _, action := range request.ProjectList {
		if (action.Action != "PIN" && action.Action != "UNPIN") || strings.TrimSpace(action.ProjectIDOrKey) == "" {
			jiraError(w, http.StatusBadRequest, "Each project needs a projectIdOrKey and an action of PIN or UNPIN.")
			return
		}
	}
	moduleID, err := h.Store.IssuePanelModule(r.Context(), workspaceID, request.ModuleID)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "The moduleId doesn't name an installed issue panel.")
		return
	}
	task, err := h.Store.EnqueueIssuePanelPins(r.Context(), workspaceID, actorID, moduleID, request.ProjectList)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "The task could not be submitted.")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"taskId": task.ID})
}
