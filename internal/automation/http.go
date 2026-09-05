package automation

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/authz"
)

type Handler struct {
	Service       *Service
	WorkspaceSlug string
}

func (h *Handler) TenantInfo(w http.ResponseWriter, r *http.Request) {
	workspaceID, err := h.Service.Store.WorkspaceBySlug(r.Context(), h.WorkspaceSlug)
	if err != nil {
		automationError(w, http.StatusInternalServerError, "automation.workspace.not_found", "Workspace is not configured", "")
		return
	}
	cloudID, err := h.Service.WorkspaceCloudID(r.Context(), workspaceID)
	if err != nil {
		automationError(w, http.StatusInternalServerError, "automation.cloud_id.unavailable", "Cloud ID is unavailable", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"cloudId": cloudID})
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.authorizeAdmin(w, r)
	if !ok {
		return
	}
	prefix := "/gateway/api/automation/public/jira/"
	path := strings.TrimPrefix(r.URL.Path, prefix)
	parts := strings.Split(path, "/")
	if len(parts) < 4 || parts[1] != "rest" || (parts[2] != "v1" && parts[2] != "latest") || parts[3] != "rule" {
		automationError(w, http.StatusNotFound, "automation.resource.not_found", "No resource found", "")
		return
	}
	cloudID, err := h.Service.WorkspaceCloudID(r.Context(), workspaceID)
	if err != nil || parts[0] != cloudID {
		automationError(w, http.StatusNotFound, "automation.cloud_id.not_found", "Cloud site was not found", "cloudid")
		return
	}
	tail := parts[4:]
	switch {
	case len(tail) == 1 && tail[0] == "summary" && r.Method == http.MethodGet:
		h.listSummaries(w, r, workspaceID, summaryFilterFromQuery(r.URL.Query()))
	case len(tail) == 1 && tail[0] == "summary" && r.Method == http.MethodPost:
		h.searchSummaries(w, r, workspaceID)
	case len(tail) == 0 && r.Method == http.MethodPost:
		h.createRule(w, r, workspaceID)
	case len(tail) == 1 && r.Method == http.MethodGet:
		h.getRule(w, r, workspaceID, tail[0])
	case len(tail) == 1 && r.Method == http.MethodPut:
		h.updateRule(w, r, workspaceID, tail[0])
	case len(tail) == 1 && r.Method == http.MethodDelete:
		h.deleteRule(w, r, workspaceID, tail[0])
	case len(tail) == 2 && tail[1] == "state" && r.Method == http.MethodPut:
		h.setState(w, r, workspaceID, tail[0])
	case len(tail) == 2 && tail[1] == "rule-scope" && r.Method == http.MethodPut:
		h.setScope(w, r, workspaceID, tail[0])
	default:
		automationError(w, http.StatusNotFound, "automation.resource.not_found", "No resource found", "")
	}
}

func (h *Handler) authorizeAdmin(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	userID, err := authn.Identify(r.Context(), h.Service.Store, r)
	if err != nil {
		automationError(w, http.StatusUnauthorized, "automation.authentication.required", "Authentication is required", "")
		return "", "", false
	}
	workspaceID, err := h.Service.Store.WorkspaceBySlug(r.Context(), h.WorkspaceSlug)
	if err != nil {
		automationError(w, http.StatusInternalServerError, "automation.workspace.not_found", "Workspace is not configured", "")
		return "", "", false
	}
	admin, err := authz.IsWorkspaceAdmin(r.Context(), h.Service.Store, workspaceID, userID)
	if err != nil || !admin {
		automationError(w, http.StatusForbidden, "automation.permission.denied", "You do not have permission to manage automation rules", "")
		return "", "", false
	}
	return workspaceID, userID, true
}

func (h *Handler) listSummaries(w http.ResponseWriter, r *http.Request, workspaceID string, filter SummaryFilter) {
	page, err := h.Service.Rules(r.Context(), workspaceID, filter)
	if err != nil {
		automationError(w, http.StatusBadRequest, "automation.pagination.invalid", err.Error(), "cursor")
		return
	}
	data := make([]map[string]any, 0, len(page.Rules))
	for _, rule := range page.Rules {
		data = append(data, summary(rule))
	}
	query := func(cursor string) string {
		if cursor == "" {
			return ""
		}
		return "?cursor=" + url.QueryEscape(cursor) + "&limit=" + strconv.Itoa(page.Limit)
	}
	self := "?limit=" + strconv.Itoa(page.Limit)
	if filter.Cursor != "" {
		self = query(filter.Cursor)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"links": map[string]string{"self": self, "next": query(page.NextCursor), "prev": query(page.PrevCursor)},
		"data":  data,
	})
}

func (h *Handler) searchSummaries(w http.ResponseWriter, r *http.Request, workspaceID string) {
	var body struct {
		Trigger string `json:"trigger"`
		State   string `json:"state"`
		Scope   string `json:"scope"`
		Author  string `json:"author"`
		Limit   *int   `json:"limit"`
		Cursor  string `json:"cursor"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Trigger == "" && body.State == "" && body.Scope == "" && body.Author == "" && body.Limit == nil {
		automationError(w, http.StatusBadRequest, "automation.rule.filter.required", "At least one rule filter is required", "")
		return
	}
	if body.State != "" && !validState(body.State) {
		automationError(w, http.StatusBadRequest, "automation.rule.state.invalid", "state must be ENABLED or DISABLED", "state")
		return
	}
	limit := 50
	if body.Limit != nil {
		limit = *body.Limit
		if limit < 1 || limit > maxRulePage {
			automationError(w, http.StatusBadRequest, "automation.pagination.invalid", "limit must be between 1 and 100", "limit")
			return
		}
	}
	filter := SummaryFilter{Triggers: []string{body.Trigger}, States: []string{body.State}, ScopeARI: body.Scope, AuthorID: body.Author, Limit: limit, Cursor: body.Cursor}
	if body.Trigger == "" {
		filter.Triggers = nil
	}
	if body.State == "" {
		filter.States = nil
	}
	h.listSummaries(w, r, workspaceID, filter)
}

func summary(rule *Rule) map[string]any {
	var actorAccountID any
	var payload struct {
		Actor struct {
			Actor string `json:"actor"`
			Type  string `json:"type"`
		} `json:"actor"`
	}
	if json.Unmarshal(rule.Payload, &payload) == nil && payload.Actor.Type == "ACCOUNT_ID" {
		actorAccountID = payload.Actor.Actor
	}
	return map[string]any{
		"actorAccountId": actorAccountID, "ruleScopeARIs": rule.RuleScopeARIs, "authorAccountId": rule.AuthorID,
		"created": float64(rule.CreatedAt.UnixNano()) / 1e9, "updated": float64(rule.UpdatedAt.UnixNano()) / 1e9,
		"description": rule.Description, "uuid": rule.UUID, "labels": rule.Labels, "name": rule.Name, "state": rule.State,
	}
}

func summaryFilterFromQuery(query url.Values) SummaryFilter {
	limit, _ := strconv.Atoi(query.Get("limit"))
	if !query.Has("limit") {
		limit = 0
	} else if limit <= 0 {
		limit = -1
	}
	return SummaryFilter{Limit: limit, Cursor: query.Get("cursor")}
}

func (h *Handler) createRule(w http.ResponseWriter, r *http.Request, workspaceID string) {
	_, userID, ok := h.authorizeAdmin(w, r)
	if !ok {
		return
	}
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	uuid, err := h.Service.CreateRule(r.Context(), workspaceID, userID, body)
	if err != nil {
		automationError(w, http.StatusBadRequest, "automation.rule.invalid", publicDBError(err), "rule")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"ruleUuid": uuid})
}

func (h *Handler) getRule(w http.ResponseWriter, r *http.Request, workspaceID, uuid string) {
	rule, err := h.Service.Rule(r.Context(), workspaceID, uuid)
	if err != nil {
		ruleError(w, err)
		return
	}
	payload := ruleReadPayload(rule)
	connections := connectionReadPayload(rule)
	if r.URL.Query().Get("redactSensitiveFields") == "true" {
		payload = redactJSON(payload)
		connections = redactJSON(connections)
	}
	var ruleValue, connectionValue any
	_ = json.Unmarshal(payload, &ruleValue)
	_ = json.Unmarshal(connections, &connectionValue)
	writeJSON(w, http.StatusOK, map[string]any{"rule": ruleValue, "connections": connectionValue})
}

func (h *Handler) updateRule(w http.ResponseWriter, r *http.Request, workspaceID, uuid string) {
	_, userID, ok := h.authorizeAdmin(w, r)
	if !ok {
		return
	}
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	if err := h.Service.UpdateRule(r.Context(), workspaceID, userID, uuid, body); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ruleError(w, err)
			return
		}
		automationError(w, http.StatusBadRequest, "automation.rule.invalid", publicDBError(err), "rule")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ruleUuid": uuid})
}

func (h *Handler) deleteRule(w http.ResponseWriter, r *http.Request, workspaceID, uuid string) {
	if err := h.Service.DeleteRule(r.Context(), workspaceID, uuid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ruleError(w, err)
			return
		}
		automationError(w, http.StatusBadRequest, "automation.rule.must_be_disabled", err.Error(), "state")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ruleUuid": uuid})
}

func (h *Handler) setState(w http.ResponseWriter, r *http.Request, workspaceID, uuid string) {
	var body struct {
		Value string `json:"value"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := h.Service.SetState(r.Context(), workspaceID, uuid, strings.ToUpper(body.Value)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ruleError(w, err)
			return
		}
		automationError(w, http.StatusBadRequest, "automation.rule.state.invalid", err.Error(), "value")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ruleUuid": uuid})
}

func (h *Handler) setScope(w http.ResponseWriter, r *http.Request, workspaceID, uuid string) {
	var body struct {
		RuleScopeARIs []string `json:"ruleScopeARIs"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.RuleScopeARIs == nil {
		automationError(w, http.StatusBadRequest, "automation.rule.scope.invalid", "ruleScopeARIs is required", "ruleScopeARIs")
		return
	}
	if err := h.Service.SetScope(r.Context(), workspaceID, uuid, body.RuleScopeARIs); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ruleError(w, err)
			return
		}
		automationError(w, http.StatusBadRequest, "automation.rule.scope.invalid", err.Error(), "ruleScopeARIs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ruleUuid": uuid})
}

func readBody(w http.ResponseWriter, r *http.Request) (json.RawMessage, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, (2<<20)+1))
	if err != nil || len(body) > 2<<20 {
		automationError(w, http.StatusBadRequest, "automation.request.invalid", "Request body is too large or unreadable", "")
		return nil, false
	}
	if len(body) == 0 || !json.Valid(body) {
		automationError(w, http.StatusBadRequest, "automation.request.invalid", "Request body must be valid JSON", "")
		return nil, false
	}
	return body, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	body, ok := readBody(w, r)
	if !ok {
		return false
	}
	if err := json.Unmarshal(body, value); err != nil {
		automationError(w, http.StatusBadRequest, "automation.request.invalid", "Request body does not match the expected shape", "")
		return false
	}
	return true
}

func ruleError(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		automationError(w, http.StatusNotFound, "automation.rule.not_found", "Automation rule was not found", "ruleUuid")
		return
	}
	automationError(w, http.StatusInternalServerError, "automation.internal", "Internal error", "")
}

func automationError(w http.ResponseWriter, status int, code, title, field string) {
	item := map[string]any{"id": newErrorID(), "status": status, "code": code, "title": title}
	if field != "" {
		item["field"] = field
	}
	writeJSON(w, status, map[string]any{"errors": []any{item}})
}

func newErrorID() string {
	id, err := NewUUIDv7()
	if err != nil {
		return "00000000-0000-7000-8000-000000000000"
	}
	return id
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func publicDBError(err error) string {
	message := err.Error()
	if strings.Contains(message, "automation_rules_workspace_id_name_key") {
		return "A rule with this name already exists"
	}
	if strings.HasPrefix(message, "create rule:") || strings.HasPrefix(message, "update rule:") {
		return "The automation rule could not be saved"
	}
	return message
}

func ruleReadPayload(rule *Rule) json.RawMessage {
	var value map[string]any
	if json.Unmarshal(rule.Payload, &value) != nil {
		return rule.Payload
	}
	value["created"] = float64(rule.CreatedAt.UnixNano()) / 1e9
	value["updated"] = float64(rule.UpdatedAt.UnixNano()) / 1e9
	out, err := json.Marshal(value)
	if err != nil {
		return rule.Payload
	}
	return out
}

func connectionReadPayload(rule *Rule) json.RawMessage {
	var values []map[string]any
	if json.Unmarshal(rule.Connections, &values) != nil {
		return rule.Connections
	}
	for _, value := range values {
		value["createdAt"] = float64(rule.CreatedAt.UnixNano()) / 1e9
		value["updatedAt"] = float64(rule.UpdatedAt.UnixNano()) / 1e9
		value["container"] = map[string]string{"id": rule.UUID, "containerType": "RULE"}
	}
	out, err := json.Marshal(values)
	if err != nil {
		return rule.Connections
	}
	return out
}

func redactJSON(raw json.RawMessage) json.RawMessage {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return raw
	}
	redactValue(value)
	out, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return out
}

func redactValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "targetconfigjson") {
				typed[key] = "REDACTED"
				continue
			}
			redactValue(child)
		}
	case []any:
		for _, child := range typed {
			redactValue(child)
		}
	}
}
