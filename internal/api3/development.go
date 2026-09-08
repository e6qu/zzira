package api3

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

var developmentIssueKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]*-[1-9][0-9]*$`)
var developmentEntityID = regexp.MustCompile(`^[A-Za-z0-9~._-]+$`)

type developmentBulkRequest struct {
	Repositories       []json.RawMessage `json:"repositories"`
	PreventTransitions bool              `json:"preventTransitions"`
	Properties         map[string]string `json:"properties"`
}

func (h *Handler) developmentRoute(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	path := r.URL.Path
	if strings.HasPrefix(path, "/jira/devinfo/0.1/cloud/") {
		parts := strings.Split(strings.TrimPrefix(path, "/jira/devinfo/0.1/cloud/"), "/")
		if len(parts) < 2 || !h.validDevelopmentCloudID(r, workspaceID, parts[0]) {
			jiraError(w, http.StatusNotFound, "Cloud site was not found.")
			return
		}
		path = "/" + strings.Join(parts[1:], "/")
	} else {
		path = strings.TrimPrefix(path, "/rest/devinfo/0.10")
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == "bulk" && r.Method == http.MethodPost:
		h.developmentBulk(w, r, workspaceID, actorID)
	case len(parts) == 1 && parts[0] == "existsByProperties" && r.Method == http.MethodGet:
		properties, sequence, err := developmentPropertiesFromQuery(r)
		if err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		exists, err := h.Store.DevelopmentRepositoriesExistByProperties(r.Context(), workspaceID, properties, sequence)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not inspect development information.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"hasDataMatchingProperties": exists})
	case len(parts) == 1 && parts[0] == "bulkByProperties" && r.Method == http.MethodDelete:
		properties, _, err := developmentPropertiesFromQuery(r)
		if err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := h.Store.DeleteDevelopmentRepositoriesByProperties(r.Context(), workspaceID, properties); err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not delete development information.")
			return
		}
		w.WriteHeader(http.StatusAccepted)
	case len(parts) == 2 && parts[0] == "repository" && r.Method == http.MethodGet:
		payload, err := h.Store.DevelopmentRepository(r.Context(), workspaceID, parts[1])
		if err != nil {
			jiraError(w, http.StatusNotFound, "Repository was not found.")
			return
		}
		writeRawJSON(w, payload)
	case len(parts) == 2 && parts[0] == "repository" && r.Method == http.MethodDelete:
		sequence, err := developmentUpdateSequence(r)
		if err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := h.Store.DeleteDevelopmentRepository(r.Context(), workspaceID, parts[1], sequence); err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not delete repository.")
			return
		}
		w.WriteHeader(http.StatusAccepted)
	case len(parts) == 4 && parts[0] == "repository" && r.Method == http.MethodDelete:
		entityType := normalizeDevelopmentEntityType(parts[2])
		if entityType == "" {
			jiraError(w, http.StatusBadRequest, "Wrong entity type specified.")
			return
		}
		sequence, err := developmentUpdateSequence(r)
		if err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := h.Store.DeleteDevelopmentEntity(r.Context(), workspaceID, parts[1], entityType, parts[3], sequence); err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not delete development entity.")
			return
		}
		w.WriteHeader(http.StatusAccepted)
	default:
		jiraError(w, http.StatusNotFound, "Development information resource does not exist.")
	}
}

func (h *Handler) validDevelopmentCloudID(r *http.Request, workspaceID, cloudID string) bool {
	var configured string
	return h.Store.Pool.QueryRow(r.Context(), `SELECT cloud_id::text FROM workspaces WHERE id=$1`, workspaceID).Scan(&configured) == nil && configured == cloudID
}

func (h *Handler) developmentBulk(w http.ResponseWriter, r *http.Request, workspaceID, actorID string) {
	var request developmentBulkRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 5<<20))
	if err := decoder.Decode(&request); err != nil || len(request.Repositories) == 0 || len(request.Repositories) > 100 {
		jiraError(w, http.StatusBadRequest, "At least one repository is required.")
		return
	}
	properties, err := validateDevelopmentProperties(request.Properties)
	if err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	repositories := make([]models.DevelopmentRepository, 0, len(request.Repositories))
	allIssueKeys := make(map[string]bool)
	accepted := make(map[string]map[string][]string)
	seenRepositories := make(map[string]bool)
	entityCount := 0
	for _, raw := range request.Repositories {
		repository, err := parseDevelopmentRepository(raw)
		if err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		if seenRepositories[repository.ID] {
			jiraError(w, http.StatusBadRequest, "Repository IDs must be unique within a request.")
			return
		}
		seenRepositories[repository.ID] = true
		repository.Properties = properties
		entityIDs := map[string][]string{"commits": {}, "branches": {}, "pullRequests": {}}
		for _, entity := range repository.Entities {
			plural := map[string]string{"commit": "commits", "branch": "branches", "pullrequest": "pullRequests"}[entity.Type]
			entityIDs[plural] = append(entityIDs[plural], entity.ID)
		}
		accepted[repository.ID] = entityIDs
		entityCount += len(repository.Entities)
		if entityCount > 1000 {
			jiraError(w, http.StatusBadRequest, "A development information request accepts at most 1000 entities.")
			return
		}
		for index := range repository.Entities {
			entity := &repository.Entities[index]
			entity.IssueKeys = h.resolveDevelopmentIssueKeys(r, workspaceID, entity.IssueKeys)
		}
		for _, entity := range repository.Entities {
			for _, issueKey := range entity.IssueKeys {
				allIssueKeys[issueKey] = true
			}
		}
		repositories = append(repositories, repository)
	}
	events, err := h.Store.UpsertDevelopmentRepositories(r.Context(), workspaceID, repositories)
	if err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !request.PreventTransitions {
		for _, event := range events {
			h.runDevelopmentTriggers(r, workspaceID, actorID, event)
		}
	}
	unknown := make([]string, 0)
	for issueKey := range allIssueKeys {
		if _, err := h.Store.IssueByIDOrKey(r.Context(), workspaceID, issueKey); err == pgx.ErrNoRows {
			unknown = append(unknown, issueKey)
		}
	}
	sort.Strings(unknown)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"acceptedDevinfoEntities": accepted,
		"failedDevinfoEntities":   map[string]any{},
		"unknownIssueKeys":        unknown,
		"unknownAssociations":     []any{},
	})
}

func parseDevelopmentRepository(raw json.RawMessage) (models.DevelopmentRepository, error) {
	var envelope struct {
		ID               string            `json:"id"`
		UpdateSequenceID int64             `json:"updateSequenceId"`
		Name             string            `json:"name"`
		URL              string            `json:"url"`
		Commits          []json.RawMessage `json:"commits"`
		Branches         []json.RawMessage `json:"branches"`
		PullRequests     []json.RawMessage `json:"pullRequests"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || !validDevelopmentID(envelope.ID) || strings.TrimSpace(envelope.Name) == "" || !validDevelopmentURL(envelope.URL) || envelope.UpdateSequenceID <= 0 || len(envelope.Name) > 255 || len(envelope.URL) > 2000 {
		return models.DevelopmentRepository{}, &developmentInputError{"Repository id, name, URL, and positive updateSequenceId are required."}
	}
	repository := models.DevelopmentRepository{
		ID: strings.TrimSpace(envelope.ID), UpdateSequenceID: envelope.UpdateSequenceID,
		Name: strings.TrimSpace(envelope.Name), URL: strings.TrimSpace(envelope.URL),
		Payload: bytes.Clone(raw),
	}
	groups := []struct {
		entityType string
		values     []json.RawMessage
	}{{"commit", envelope.Commits}, {"branch", envelope.Branches}, {"pullrequest", envelope.PullRequests}}
	for _, group := range groups {
		if len(group.values) > 400 {
			return models.DevelopmentRepository{}, &developmentInputError{"Each repository accepts at most 400 entities of one type."}
		}
		seenEntities := make(map[string]bool)
		for _, entityRaw := range group.values {
			entity, err := parseDevelopmentEntity(repository.ID, group.entityType, entityRaw)
			if err != nil {
				return models.DevelopmentRepository{}, err
			}
			if seenEntities[entity.ID] {
				return models.DevelopmentRepository{}, &developmentInputError{"Development entity IDs must be unique within their repository and type."}
			}
			seenEntities[entity.ID] = true
			repository.Entities = append(repository.Entities, entity)
		}
	}
	return repository, nil
}

func parseDevelopmentEntity(repositoryID, entityType string, raw json.RawMessage) (models.DevelopmentEntity, error) {
	var value struct {
		ID               string   `json:"id"`
		DisplayID        string   `json:"displayId"`
		Name             string   `json:"name"`
		Title            string   `json:"title"`
		Message          string   `json:"message"`
		URL              string   `json:"url"`
		Status           string   `json:"status"`
		UpdateSequenceID int64    `json:"updateSequenceId"`
		IssueKeys        []string `json:"issueKeys"`
		Associations     []struct {
			AssociationType string   `json:"associationType"`
			Values          []string `json:"values"`
		} `json:"associations"`
		AuthorTimestamp string `json:"authorTimestamp"`
		LastUpdate      string `json:"lastUpdate"`
	}
	if err := json.Unmarshal(raw, &value); err != nil || !validDevelopmentID(value.ID) || !validDevelopmentURL(value.URL) || value.UpdateSequenceID <= 0 || len(value.ID) > 1024 || len(value.URL) > 2000 {
		return models.DevelopmentEntity{}, &developmentInputError{"Development entities require a valid id, URL, and positive updateSequenceId."}
	}
	keys := append([]string(nil), value.IssueKeys...)
	for _, association := range value.Associations {
		if association.AssociationType == "issueIdOrKeys" {
			keys = append(keys, association.Values...)
		}
	}
	keys = normalizeDevelopmentIssueKeys(keys)
	name := strings.TrimSpace(value.Name)
	if name == "" {
		name = strings.TrimSpace(value.Title)
	}
	if name == "" {
		name = strings.TrimSpace(value.Message)
	}
	if name == "" {
		name = strings.TrimSpace(value.DisplayID)
	}
	if name == "" {
		name = strings.TrimSpace(value.ID)
	}
	var occurredAt *time.Time
	for _, timestamp := range []string{value.LastUpdate, value.AuthorTimestamp} {
		if parsed, err := time.Parse(time.RFC3339, timestamp); err == nil {
			parsed = parsed.UTC()
			occurredAt = &parsed
			break
		}
	}
	return models.DevelopmentEntity{
		RepositoryID: repositoryID, Type: entityType, ID: strings.TrimSpace(value.ID),
		UpdateSequenceID: value.UpdateSequenceID, IssueKeys: keys, Name: name,
		URL: strings.TrimSpace(value.URL), Status: strings.TrimSpace(value.Status), OccurredAt: occurredAt,
		Payload: bytes.Clone(raw),
	}, nil
}

func validDevelopmentID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 1024 && developmentEntityID.MatchString(value)
}

func validDevelopmentURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != ""
}

type developmentInputError struct{ message string }

func (e *developmentInputError) Error() string { return e.message }

func normalizeDevelopmentIssueKeys(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool)
	for _, value := range values {
		ref := strings.TrimSpace(value)
		if ref == "" {
			continue
		}
		if developmentIssueKey.MatchString(strings.ToUpper(ref)) {
			ref = strings.ToUpper(ref)
		}
		if !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	return out
}

func (h *Handler) resolveDevelopmentIssueKeys(r *http.Request, workspaceID string, refs []string) []string {
	keys := make([]string, 0, len(refs))
	seen := make(map[string]bool)
	for _, ref := range refs {
		issue, err := h.Store.IssueByIDOrKey(r.Context(), workspaceID, ref)
		key := strings.ToUpper(ref)
		if err == nil {
			key = issue.Key
		} else if !developmentIssueKey.MatchString(key) {
			continue
		}
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	return keys
}

func normalizeDevelopmentEntityType(value string) string {
	switch strings.ToLower(value) {
	case "commit", "commits":
		return "commit"
	case "branch", "branches":
		return "branch"
	case "pullrequest", "pullrequests", "pull_request", "pull-request", "pull-requests":
		return "pullrequest"
	default:
		return ""
	}
}

func validateDevelopmentProperties(values map[string]string) (json.RawMessage, error) {
	if len(values) > 5 {
		return nil, &developmentInputError{"At most five development information properties are allowed."}
	}
	for key, value := range values {
		if key == "" || strings.HasPrefix(key, "_") || strings.Contains(key, ":") || len(key) > 255 || len(value) > 255 {
			return nil, &developmentInputError{"Development information properties contain an invalid key or value."}
		}
	}
	encoded, err := json.Marshal(values)
	return encoded, err
}

func developmentUpdateSequence(r *http.Request) (*int64, error) {
	value := r.URL.Query().Get("_updateSequenceId")
	if value == "" {
		return nil, nil
	}
	sequence, err := strconv.ParseInt(value, 10, 64)
	if err != nil || sequence < 0 {
		return nil, &developmentInputError{"_updateSequenceId must be a non-negative integer."}
	}
	return &sequence, nil
}

func developmentPropertiesFromQuery(r *http.Request) (json.RawMessage, *int64, error) {
	sequence, err := developmentUpdateSequence(r)
	if err != nil {
		return nil, nil, err
	}
	values := make(map[string]string)
	for key, candidates := range r.URL.Query() {
		if key == "_updateSequenceId" {
			continue
		}
		if len(candidates) != 1 {
			return nil, nil, &developmentInputError{"Each development information property must have exactly one value."}
		}
		values[key] = candidates[0]
	}
	if len(values) == 0 {
		return nil, nil, &developmentInputError{"At least one development information property is required."}
	}
	properties, err := validateDevelopmentProperties(values)
	return properties, sequence, err
}

func (h *Handler) runDevelopmentTriggers(r *http.Request, workspaceID, actorID string, event models.DevelopmentTriggerEvent) {
	if event.Type != "branch" || h.Commands == nil {
		return
	}
	seen := make(map[string]bool)
	for _, issueKey := range event.IssueKeys {
		if seen[issueKey] {
			continue
		}
		seen[issueKey] = true
		issue, err := h.Store.IssueByIDOrKey(r.Context(), workspaceID, issueKey)
		if err != nil {
			log.Printf("devinfo: resolve linked issue failed: %T", err)
			continue
		}
		wf, err := h.Store.WorkflowForProjectAndIssueType(r.Context(), issue.ProjectID, issue.IssueType.ID)
		if err != nil {
			log.Printf("devinfo: resolve workflow failed: %T", err)
			continue
		}
		for _, transition := range wf.Available(issue.Status.ID) {
			if transition.HasDevelopmentTrigger(workflow.DevelopmentBranchCreated) {
				if _, _, err := h.Commands.TransitionIssueWithUpdateFromAPI(r.Context(), actorID, workspaceID, issue.Key, transition.ID, store.IssueUpdate{}); err != nil {
					log.Printf("devinfo: branch-created transition failed: %T", err)
				}
				break
			}
		}
	}
}
