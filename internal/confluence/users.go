package confluence

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/store"
)

func (h *Handler) wikiUserBean(user store.WikiUser, withEmail bool) map[string]any {
	bean := map[string]any{
		"type": "known", "accountId": user.AccountID, "accountType": user.AccountType,
		"publicName": user.PublicName, "displayName": user.DisplayName,
		"isExternalCollaborator": false,
		"profilePicture": map[string]any{
			"path": "/wiki/aa-avatar/" + user.AccountID, "width": 48, "height": 48, "isDefault": true,
		},
		"_links": map[string]string{"base": h.BaseURL + "/wiki", "self": h.BaseURL + "/wiki/rest/api/user?accountId=" + user.AccountID},
	}
	if user.AccountType == "anonymous" {
		// Anonymous has no account, so it has no address to link to either.
		bean["type"] = "anonymous"
		delete(bean, "accountId")
		bean["_links"] = map[string]string{"base": h.BaseURL + "/wiki"}
		bean["profilePicture"] = map[string]any{
			"path": "/wiki/aa-avatar/anonymous", "width": 48, "height": 48, "isDefault": true,
		}
	}
	if withEmail {
		bean["email"] = user.Email
	}
	return bean
}

// accountIDParam reads the accountId Confluence's user reads are addressed by.
func accountIDParam(w http.ResponseWriter, r *http.Request, plural bool) ([]string, bool) {
	ids := store.SplitAccountIDs(r.URL.Query()["accountId"])
	if len(ids) == 0 {
		failure(w, 400, "accountId is required.")
		return nil, false
	}
	if !plural && len(ids) > 1 {
		failure(w, 400, "Only one accountId is supported here.")
		return nil, false
	}
	return ids, true
}

func (h *V1Handler) v1CurrentUser(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "expand") {
		return
	}
	user, err := h.Store.WikiUserByAccountID(r.Context(), ws, actor, actor)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.wikiUserBean(user, false))
}

func (h *V1Handler) v1AnonymousUser(w http.ResponseWriter, r *http.Request) {
	if !supportedQuery(w, r, "expand") {
		return
	}
	respond(w, 200, h.wikiUserBean(store.AnonymousWikiUser(), false))
}

func (h *V1Handler) v1User(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "accountId", "expand") {
		return
	}
	ids, ok := accountIDParam(w, r, false)
	if !ok {
		return
	}
	user, err := h.Store.WikiUserByAccountID(r.Context(), ws, actor, ids[0])
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.wikiUserBean(user, false))
}

// v1BulkUsers and v1BulkUserEmails differ in one thing: whether the answer
// carries the email address, which an ordinary member may not see.
func (h *V1Handler) v1BulkUsers(w http.ResponseWriter, r *http.Request, ws, actor string, emails bool) {
	allowed := []string{"accountId", "expand", "limit", "start", "cursor"}
	if !supportedQuery(w, r, allowed...) {
		return
	}
	ids, ok := accountIDParam(w, r, true)
	if !ok {
		return
	}
	var users []store.WikiUser
	var err error
	if emails {
		users, err = h.Store.WikiUserEmails(r.Context(), ws, actor, ids)
	} else {
		users, err = h.Store.WikiUsersByAccountIDs(r.Context(), ws, actor, ids)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	results := make([]any, 0, len(users))
	for _, user := range users {
		if emails {
			results = append(results, map[string]any{"accountId": user.AccountID, "email": user.Email})
			continue
		}
		results = append(results, h.wikiUserBean(user, false))
	}
	respond(w, 200, map[string]any{
		"results": results, "start": 0, "limit": len(results), "size": len(results),
		"_links": map[string]string{"base": h.BaseURL + "/wiki"},
	})
}

// v1UserEmail answers one person's email, which is administration.
func (h *V1Handler) v1UserEmail(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "accountId") {
		return
	}
	ids, ok := accountIDParam(w, r, false)
	if !ok {
		return
	}
	users, err := h.Store.WikiUserEmails(r.Context(), ws, actor, ids)
	if err != nil {
		writeError(w, err)
		return
	}
	if len(users) == 0 {
		failure(w, 404, "The user does not exist.")
		return
	}
	respond(w, 200, map[string]any{"accountId": users[0].AccountID, "email": users[0].Email})
}

func (h *V1Handler) v1UserGroups(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "accountId", "start", "limit") {
		return
	}
	ids, ok := accountIDParam(w, r, false)
	if !ok {
		return
	}
	groups, err := h.Store.WikiUserGroups(r.Context(), ws, actor, ids[0])
	if err != nil {
		writeError(w, err)
		return
	}
	results := make([]any, 0, len(groups))
	for _, group := range groups {
		results = append(results, map[string]any{
			"type": "group", "id": group.ID, "name": group.Name,
			"_links": map[string]string{"self": h.BaseURL + "/wiki/rest/api/group/by-id?id=" + group.ID},
		})
	}
	respond(w, 200, map[string]any{
		"results": results, "start": 0, "limit": len(results), "size": len(results),
		"_links": map[string]string{"base": h.BaseURL + "/wiki"},
	})
}

// v1SearchUsers answers Confluence's user search. It takes a CQL string, and
// the part of it this serves is the text a person is matched on; a query
// naming a field this does not filter on is refused rather than silently
// matching everyone.
func (h *V1Handler) v1SearchUsers(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "cql", "start", "limit", "expand", "sitePermissionTypeFilter") {
		return
	}
	cql := strings.TrimSpace(r.URL.Query().Get("cql"))
	if cql == "" {
		failure(w, 400, "cql is required.")
		return
	}
	term, ok := userSearchTerm(cql)
	if !ok {
		failure(w, 400, "Only user.fullname ~ \"…\" and user ~ \"…\" queries are supported.")
		return
	}
	users, err := h.Store.SearchWikiUsers(r.Context(), ws, actor, term)
	if err != nil {
		writeError(w, err)
		return
	}
	results := make([]any, 0, len(users))
	for _, user := range users {
		results = append(results, map[string]any{
			"user": h.wikiUserBean(user, false), "title": user.DisplayName,
			"entityType": "user", "score": 0,
		})
	}
	respond(w, 200, map[string]any{
		"results": results, "start": 0, "limit": len(results), "size": len(results),
		"totalSize": len(results), "cqlQuery": cql,
		"_links": map[string]string{"base": h.BaseURL + "/wiki"},
	})
}

func userSearchTerm(cql string) (string, bool) {
	lower := strings.ToLower(cql)
	for _, prefix := range []string{"user.fullname~", "user.fullname ~", "user~", "user ~"} {
		if strings.HasPrefix(lower, prefix) {
			return strings.Trim(strings.TrimSpace(cql[len(prefix):]), `"'`), true
		}
	}
	return "", false
}

func (h *V1Handler) v1UserProperties(w http.ResponseWriter, r *http.Request, ws, actor, accountID string) {
	if !supportedQuery(w, r, "start", "limit", "expand") {
		return
	}
	properties, err := h.Store.WikiUserProperties(r.Context(), ws, actor, accountID)
	if err != nil {
		writeError(w, err)
		return
	}
	results := make([]any, 0, len(properties))
	for _, property := range properties {
		results = append(results, h.userPropertyBean(accountID, property))
	}
	respond(w, 200, map[string]any{
		"results": results, "start": 0, "limit": len(results), "size": len(results),
		"_links": map[string]string{"base": h.BaseURL + "/wiki"},
	})
}

func (h *Handler) userPropertyBean(accountID string, property store.WikiUserProperty) map[string]any {
	return map[string]any{
		"key": property.Key, "value": property.Value,
		"version": map[string]any{"number": property.Version, "minorEdit": false},
		"_links":  map[string]string{"self": h.BaseURL + "/wiki/rest/api/user/" + accountID + "/property/" + property.Key},
	}
}

func (h *V1Handler) v1UserProperty(w http.ResponseWriter, r *http.Request, ws, actor, accountID, key string) {
	if !supportedQuery(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		property, err := h.Store.WikiUserProperty(r.Context(), ws, actor, accountID, key)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, h.userPropertyBean(accountID, property))
	case http.MethodPost, http.MethodPut:
		var input struct {
			Key   string          `json:"key"`
			Value json.RawMessage `json:"value"`
		}
		if !decode(w, r, &input) {
			return
		}
		if input.Key != "" && input.Key != key {
			failure(w, 400, "The key in the body must match the one in the path.")
			return
		}
		property, err := h.Store.SaveWikiUserProperty(r.Context(), ws, actor, accountID, key, input.Value, r.Method == http.MethodPost)
		if err != nil {
			writeError(w, err)
			return
		}
		status := 200
		if r.Method == http.MethodPost {
			status = 201
		}
		respond(w, status, h.userPropertyBean(accountID, property))
	case http.MethodDelete:
		if err := h.Store.DeleteWikiUserProperty(r.Context(), ws, actor, accountID, key); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		failure(w, 405, "Method not allowed.")
	}
}

// bulkUsersV2 is the v2 surface's bulk read, which takes the ids in the body
// rather than the query string.
func (h *Handler) bulkUsersV2(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "cursor", "limit") {
		return
	}
	var input struct {
		AccountIDs []string `json:"accountIds"`
	}
	if !decode(w, r, &input) {
		return
	}
	if len(input.AccountIDs) == 0 || len(input.AccountIDs) > 500 {
		failure(w, 400, "Between 1 and 500 account ids are required.")
		return
	}
	users, err := h.Store.WikiUsersByAccountIDs(r.Context(), ws, actor, input.AccountIDs)
	if err != nil {
		writeError(w, err)
		return
	}
	results := make([]any, 0, len(users))
	for _, user := range users {
		results = append(results, h.wikiUserBean(user, false))
	}
	respond(w, 200, map[string]any{"results": results, "_links": map[string]string{"base": h.BaseURL + "/wiki"}})
}
