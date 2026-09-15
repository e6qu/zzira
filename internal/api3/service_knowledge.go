package api3

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/wikimarkup"
)

func (h *Handler) serviceAssetsWorkspaces(w http.ResponseWriter, r *http.Request, workspaceID string) {
	ids, err := h.Store.ServiceAssetsWorkspaceIDs(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load Assets workspaces.")
		return
	}
	values := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		values = append(values, map[string]any{"workspaceId": id})
	}
	h.writeServicePage(w, r, values)
}

func serviceArticleExcerpt(body string) string {
	text, err := wikimarkup.Text(body)
	if err != nil {
		return ""
	}
	runes := []rune(text)
	if len(runes) > 240 {
		return string(runes[:237]) + "..."
	}
	return text
}

func highlightServiceArticle(value, query string) string {
	if query == "" {
		return value
	}
	matcher, err := regexp.Compile("(?i)" + regexp.QuoteMeta(query))
	if err != nil {
		return value
	}
	return matcher.ReplaceAllStringFunc(value, func(match string) string { return "@@@hl@@@" + match + "@@@endhl@@@" })
}

func (h *Handler) serviceKnowledgeArticleBean(article models.ServiceKnowledgeArticle, query string, highlight bool) map[string]any {
	title, excerpt := article.Title, serviceArticleExcerpt(article.Body)
	if highlight {
		title, excerpt = highlightServiceArticle(title, query), highlightServiceArticle(excerpt, query)
	}
	return map[string]any{
		"title": title, "excerpt": excerpt,
		"source":  map[string]any{"type": "confluence", "pageId": article.PageID, "spaceKey": article.SpaceKey},
		"content": map[string]any{"iframeSrc": h.BaseURL + "/rest/servicedeskapi/knowledgebase/article/view/" + article.PageID},
	}
}

func (h *Handler) serviceKnowledgeArticles(w http.ResponseWriter, r *http.Request, workspaceID, actorID, serviceDeskID string) {
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	if query == "" || utf8.RuneCountInString(query) > 255 {
		jiraError(w, http.StatusBadRequest, "query is required and accepts at most 255 characters.")
		return
	}
	allowAll, err := h.Store.IsAdmin(r.Context(), workspaceID, actorID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not authorize knowledge-base access.")
		return
	}
	if serviceDeskID != "" && !allowAll {
		allowed, err := h.Store.CanCreateServiceRequest(r.Context(), workspaceID, serviceDeskID, actorID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not authorize service desk access.")
			return
		}
		if !allowed {
			jiraError(w, http.StatusForbidden, "You do not have access to this service desk.")
			return
		}
	}
	articles, err := h.Store.ServiceKnowledgeArticles(r.Context(), workspaceID, actorID, serviceDeskID, query, allowAll)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not search knowledge-base articles.")
		return
	}
	values := make([]map[string]any, 0, len(articles))
	highlight := r.URL.Query().Get("highlight") == "true"
	for _, article := range articles {
		values = append(values, h.serviceKnowledgeArticleBean(article, query, highlight))
	}
	h.writeServiceCursorPage(w, r, values)
}

// writeServiceCursorPage pages a list the way Jira's knowledge base search
// does: an opaque cursor marks where a page starts, prev=true asks for the
// page before it, and the deprecated start still works without a cursor.
func (h *Handler) writeServiceCursorPage(w http.ResponseWriter, r *http.Request, values []map[string]any) {
	limit := 50
	if value, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && value > 0 && value <= 100 {
		limit = value
	}
	start := 0
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		offset, parseErr := strconv.Atoi(strings.TrimPrefix(string(decoded), "offset:"))
		if err != nil || parseErr != nil || !strings.HasPrefix(string(decoded), "offset:") || offset < 0 {
			jiraError(w, http.StatusBadRequest, "The cursor is invalid.")
			return
		}
		start = offset
		if r.URL.Query().Get("prev") == "true" {
			start = max(0, offset-limit)
		}
	} else if value, err := strconv.Atoi(r.URL.Query().Get("start")); err == nil && value >= 0 {
		start = value
	}
	start = min(start, len(values))
	end := min(start+limit, len(values))
	page := func(offset int, previous bool) string {
		query := r.URL.Query()
		query.Del("start")
		query.Del("prev")
		query.Set("cursor", base64.RawURLEncoding.EncodeToString([]byte("offset:"+strconv.Itoa(offset))))
		if previous {
			query.Set("prev", "true")
		}
		return h.BaseURL + r.URL.Path + "?" + query.Encode()
	}
	self := h.BaseURL + r.URL.Path
	if r.URL.RawQuery != "" {
		self += "?" + r.URL.RawQuery
	}
	links := map[string]string{"base": h.BaseURL + "/rest/servicedeskapi", "context": "", "self": self}
	if end < len(values) {
		links["next"] = page(end, false)
	}
	if start > 0 {
		links["prev"] = page(start, true)
	}
	writeJSON(w, http.StatusOK, map[string]any{"start": start, "limit": limit, "size": end - start, "isLastPage": end == len(values), "values": values[start:end], "_expands": []any{}, "_links": links})
}

func (h *Handler) serviceKnowledgeArticle(w http.ResponseWriter, r *http.Request, workspaceID, actorID, pageID string) {
	allowAll, err := h.Store.IsAdmin(r.Context(), workspaceID, actorID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not authorize knowledge-base access.")
		return
	}
	article, err := h.Store.ServiceKnowledgeArticle(r.Context(), workspaceID, actorID, pageID, allowAll)
	if err != nil {
		jiraError(w, http.StatusNotFound, "Knowledge-base article was not found.")
		return
	}
	body, err := wikimarkup.Render(article.Body)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Knowledge-base article is invalid.")
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func (h *Handler) serviceRequestTypeGroups(w http.ResponseWriter, r *http.Request, workspaceID, actorID, serviceDeskID string) {
	if _, err := h.Store.ServiceDesk(r.Context(), workspaceID, serviceDeskID); err != nil {
		jiraError(w, http.StatusNotFound, "Service desk was not found.")
		return
	}
	agent, err := h.Store.IsServiceAgent(r.Context(), workspaceID, serviceDeskID, actorID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not authorize request-type access.")
		return
	}
	if !agent {
		allowed, err := h.Store.CanCreateServiceRequest(r.Context(), workspaceID, serviceDeskID, actorID)
		if err != nil || !allowed {
			jiraError(w, http.StatusForbidden, "You do not have access to this service desk.")
			return
		}
	}
	groups, err := h.Store.ServiceRequestTypeGroups(r.Context(), workspaceID, serviceDeskID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load request-type groups.")
		return
	}
	values := make([]map[string]any, 0, len(groups))
	for _, group := range groups {
		values = append(values, map[string]any{"id": group.ID, "name": group.Name})
	}
	h.writeServicePage(w, r, values)
}

func (h *Handler) serviceRequestTypePermissions(w http.ResponseWriter, r *http.Request, workspaceID, actorID, serviceDeskID string) {
	var input struct {
		AccountID      string        `json:"accountId"`
		Permissions    []string      `json:"permissions"`
		RequestTypeIDs []json.Number `json:"requestTypeIds"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.UseNumber()
	if err := decoder.Decode(&input); err != nil || len(input.Permissions) == 0 || len(input.RequestTypeIDs) > 50 {
		jiraError(w, http.StatusBadRequest, "permissions are required and at most 50 request type IDs may be checked.")
		return
	}
	for _, permission := range input.Permissions {
		if permission != "canCreateRequest" && permission != "canAdminister" {
			jiraError(w, http.StatusBadRequest, "Permission is invalid.")
			return
		}
	}
	targetID := input.AccountID
	if targetID == "" {
		targetID = actorID
	} else if targetID != actorID {
		admin, err := h.Store.IsAdmin(r.Context(), workspaceID, actorID)
		if err != nil || !admin {
			jiraError(w, http.StatusUnauthorized, "Administrator access is required to check another account.")
			return
		}
	}
	member, err := h.Store.IsMember(r.Context(), workspaceID, targetID)
	if err != nil || !member {
		jiraError(w, http.StatusBadRequest, "accountId is invalid.")
		return
	}
	targetAdmin, err := h.Store.IsAdmin(r.Context(), workspaceID, targetID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not check request-type permissions.")
		return
	}
	canCreate, err := h.Store.CanCreateServiceRequest(r.Context(), workspaceID, serviceDeskID, targetID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not check request-type permissions.")
		return
	}
	response := map[string]any{}
	for _, permission := range input.Permissions {
		ids := make([]json.Number, 0)
		for _, id := range input.RequestTypeIDs {
			if _, err := h.Store.ServiceRequestType(r.Context(), workspaceID, serviceDeskID, id.String()); err != nil {
				continue
			}
			if (permission == "canCreateRequest" && canCreate) || (permission == "canAdminister" && targetAdmin) {
				ids = append(ids, id)
			}
		}
		response[permission] = ids
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) serviceRequestTypePropertyKeys(w http.ResponseWriter, r *http.Request, workspaceID, actorID, serviceDeskID, requestTypeID string) {
	if _, err := h.Store.ServiceRequestType(r.Context(), workspaceID, serviceDeskID, requestTypeID); err != nil {
		jiraError(w, http.StatusNotFound, "Request type was not found.")
		return
	}
	if allowed, err := h.Store.CanCreateServiceRequest(r.Context(), workspaceID, serviceDeskID, actorID); err != nil || !allowed {
		jiraError(w, http.StatusForbidden, "You do not have access to this request type.")
		return
	}
	keys, err := h.Store.ServiceRequestTypePropertyKeys(r.Context(), workspaceID, serviceDeskID, requestTypeID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load request-type properties.")
		return
	}
	values := make([]map[string]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, map[string]string{"key": key, "self": h.BaseURL + "/rest/servicedeskapi/servicedesk/" + serviceDeskID + "/requesttype/" + requestTypeID + "/property/" + key})
	}
	writeJSON(w, http.StatusOK, map[string]any{"entityPropertyKeyBeans": values})
}

func (h *Handler) serviceRequestTypeProperty(w http.ResponseWriter, r *http.Request, workspaceID, actorID, serviceDeskID, requestTypeID, key string) {
	if _, err := h.Store.ServiceRequestType(r.Context(), workspaceID, serviceDeskID, requestTypeID); err != nil {
		jiraError(w, http.StatusNotFound, "Request type was not found.")
		return
	}
	if r.Method == http.MethodGet {
		if allowed, err := h.Store.CanCreateServiceRequest(r.Context(), workspaceID, serviceDeskID, actorID); err != nil || !allowed {
			jiraError(w, http.StatusForbidden, "You do not have access to this request type.")
			return
		}
		value, err := h.Store.ServiceRequestTypeProperty(r.Context(), workspaceID, serviceDeskID, requestTypeID, key)
		if err != nil {
			jiraError(w, http.StatusNotFound, "Request type property was not found.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": json.RawMessage(value)})
		return
	}
	if !h.serviceDeskAdminAccess(r, workspaceID, serviceDeskID, actorID) {
		jiraError(w, http.StatusForbidden, "Service desk administrator access is required.")
		return
	}
	if agent, err := h.Store.IsServiceAgent(r.Context(), workspaceID, serviceDeskID, actorID); err != nil || !agent {
		jiraError(w, http.StatusForbidden, "A Jira Service Management agent license is required.")
		return
	}
	if r.Method == http.MethodDelete {
		if err := h.Commands.DeleteServiceRequestTypeProperty(r.Context(), actorID, workspaceID, serviceDeskID, requestTypeID, key); err != nil {
			jiraError(w, http.StatusNotFound, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if key == "" || len(key) > 255 {
		jiraError(w, http.StatusBadRequest, "Property key accepts 1 to 255 bytes.")
		return
	}
	var value json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32769)).Decode(&value); err != nil || len(value) == 0 || !json.Valid(value) {
		jiraError(w, http.StatusBadRequest, "A valid JSON property value of at most 32768 bytes is required.")
		return
	}
	created, err := h.Commands.SetServiceRequestTypeProperty(r.Context(), actorID, workspaceID, serviceDeskID, requestTypeID, key, value)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not set request-type property.")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{})
}
