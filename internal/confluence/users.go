package confluence

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

// strconvAtoiBounded parses a bounded integer query value.
func strconvAtoiBounded(raw string, minimum, maximum int) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, strconv.ErrRange
	}
	return value, nil
}

func (h *Handler) wikiUserBean(user store.WikiUser, withEmail bool) map[string]any {
	bean := map[string]any{
		"type": "known", "accountId": user.AccountID, "accountType": user.AccountType,
		"publicName": user.PublicName, "displayName": user.DisplayName,
		"isExternalCollaborator": user.ExternalCollaborator, "externalCollaborator": user.ExternalCollaborator,
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
	// Licensed users by default; guests only, or everyone, when asked.
	filter := r.URL.Query().Get("sitePermissionTypeFilter")
	if filter == "" {
		filter = "none"
	}
	if filter != "none" && filter != "externalCollaborator" && filter != "all" {
		failure(w, 400, "sitePermissionTypeFilter must be all, externalCollaborator or none.")
		return
	}
	start, limit := 0, 25
	var err error
	if raw := r.URL.Query().Get("start"); raw != "" {
		if start, err = strconvAtoiBounded(raw, 0, 1<<20); err != nil {
			failure(w, 400, "start must be zero or greater.")
			return
		}
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if limit, err = strconvAtoiBounded(raw, 0, 1000); err != nil {
			failure(w, 400, "limit must be between 0 and 1000.")
			return
		}
	}
	users, err := h.Store.SearchWikiUsers(r.Context(), ws, actor, term)
	if err != nil {
		writeError(w, err)
		return
	}
	matching := make([]store.WikiUser, 0, len(users))
	for _, user := range users {
		if filter == "all" || (filter == "externalCollaborator") == user.ExternalCollaborator {
			matching = append(matching, user)
		}
	}
	total := len(matching)
	matching = matching[min(start, total):min(start+limit, total)]
	beans, ok := h.expandedUserBeans(w, r, ws, actor, matching)
	if !ok {
		return
	}
	results := make([]any, 0, len(beans))
	for i, bean := range beans {
		results = append(results, map[string]any{
			"user": bean, "title": matching[i].DisplayName,
			"entityType": "user", "score": 0,
		})
	}
	respond(w, 200, map[string]any{
		"results": results, "start": start, "limit": limit, "size": len(results),
		"totalSize": total, "cqlQuery": cql,
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
	if !supportedQuery(w, r) {
		return
	}
	var input struct {
		AccountIDs []string `json:"accountIds"`
	}
	if !decode(w, r, &input) {
		return
	}
	if len(input.AccountIDs) == 0 || len(input.AccountIDs) > 250 {
		failure(w, 400, "Between 1 and 250 account ids are required.")
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

func (h *Handler) wikiGroupBean(group store.WikiGroup) map[string]any {
	return map[string]any{
		"type": "group", "id": group.ID, "name": group.Name,
		"_links": map[string]string{"self": h.BaseURL + "/wiki/rest/api/group/by-id?id=" + group.ID},
	}
}

// wikiPage renders Confluence's v1 paged collection, with the total only when
// the caller asked for it — counting is work a caller should opt into.
func (h *Handler) wikiPage(w http.ResponseWriter, r *http.Request, values []any, withTotal bool) {
	start, limit := 0, 200
	if raw := r.URL.Query().Get("start"); raw != "" {
		parsed, err := strconvAtoiBounded(raw, 0, 1<<20)
		if err != nil {
			failure(w, 400, "start must be zero or greater.")
			return
		}
		start = parsed
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconvAtoiBounded(raw, 1, 200)
		if err != nil {
			failure(w, 400, "limit must be between 1 and 200.")
			return
		}
		limit = parsed
	}
	page := []any{}
	for i := start; i < len(values) && len(page) < limit; i++ {
		page = append(page, values[i])
	}
	bean := map[string]any{
		"results": page, "start": start, "limit": limit, "size": len(page),
		"_links": map[string]string{"base": h.BaseURL + "/wiki"},
	}
	if withTotal {
		bean["totalSize"] = len(values)
	}
	respond(w, 200, bean)
}

func (h *V1Handler) v1Groups(w http.ResponseWriter, r *http.Request, ws, actor string) {
	switch r.Method {
	case http.MethodGet:
		if !supportedQuery(w, r, "start", "limit", "accessType") {
			return
		}
		accessType := r.URL.Query().Get("accessType")
		if accessType != "" && accessType != "user" && accessType != "admin" && accessType != "site-admin" {
			failure(w, 400, "accessType must be user, admin or site-admin.")
			return
		}
		groups, err := h.Store.WikiGroupsByAccess(r.Context(), ws, actor, accessType)
		if err != nil {
			writeError(w, err)
			return
		}
		values := make([]any, 0, len(groups))
		for _, group := range groups {
			values = append(values, h.wikiGroupBean(group))
		}
		h.wikiPage(w, r, values, false)
	case http.MethodPost:
		if !supportedQuery(w, r) {
			return
		}
		var input struct {
			Name string `json:"name"`
		}
		if !decode(w, r, &input) {
			return
		}
		group, err := h.Store.CreateWikiGroup(r.Context(), ws, actor, input.Name)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 201, h.wikiGroupBean(group))
	default:
		failure(w, 405, "Method not allowed.")
	}
}

func (h *V1Handler) v1GroupByID(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "id") {
		return
	}
	groupID := strings.TrimSpace(r.URL.Query().Get("id"))
	if groupID == "" {
		failure(w, 400, "id is required.")
		return
	}
	switch r.Method {
	case http.MethodGet:
		group, err := h.Store.WikiGroupByID(r.Context(), ws, actor, groupID)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, h.wikiGroupBean(group))
	case http.MethodDelete:
		if err := h.Store.DeleteWikiGroup(r.Context(), ws, actor, groupID); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		failure(w, 405, "Method not allowed.")
	}
}

func (h *V1Handler) v1GroupPicker(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "query", "start", "limit", "shouldReturnTotalSize") {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	if query == "" {
		failure(w, 400, "query is required.")
		return
	}
	groups, err := h.Store.WikiGroups(r.Context(), ws, actor, query)
	if err != nil {
		writeError(w, err)
		return
	}
	values := make([]any, 0, len(groups))
	for _, group := range groups {
		values = append(values, h.wikiGroupBean(group))
	}
	h.wikiPage(w, r, values, r.URL.Query().Get("shouldReturnTotalSize") == "true")
}

func (h *V1Handler) v1GroupMembers(w http.ResponseWriter, r *http.Request, ws, actor, groupID string) {
	if !supportedQuery(w, r, "start", "limit", "shouldReturnTotalSize", "expand") {
		return
	}
	users, err := h.Store.WikiGroupMembers(r.Context(), ws, actor, groupID)
	if err != nil {
		writeError(w, err)
		return
	}
	beans, ok := h.expandedUserBeans(w, r, ws, actor, users)
	if !ok {
		return
	}
	values := make([]any, 0, len(beans))
	for _, bean := range beans {
		values = append(values, bean)
	}
	h.wikiPage(w, r, values, r.URL.Query().Get("shouldReturnTotalSize") == "true")
}

func (h *V1Handler) v1GroupMembership(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "groupId", "accountId") {
		return
	}
	groupID := strings.TrimSpace(r.URL.Query().Get("groupId"))
	if groupID == "" {
		failure(w, 400, "groupId is required.")
		return
	}
	switch r.Method {
	case http.MethodPost:
		var input struct {
			AccountID string `json:"accountId"`
		}
		if !decode(w, r, &input) {
			return
		}
		accountID := strings.TrimSpace(input.AccountID)
		if accountID == "" {
			failure(w, 400, "accountId is required.")
			return
		}
		if err := h.Store.SetWikiGroupMembership(r.Context(), ws, actor, groupID, accountID, true); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(201)
	case http.MethodDelete:
		accountID := strings.TrimSpace(r.URL.Query().Get("accountId"))
		if accountID == "" {
			failure(w, 400, "accountId is required.")
			return
		}
		if err := h.Store.SetWikiGroupMembership(r.Context(), ws, actor, groupID, accountID, false); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		failure(w, 405, "Method not allowed.")
	}
}

// userExpansions are the properties of a user Confluence expands on request.
var userExpansions = map[string]bool{"operations": true, "personalSpace": true, "isExternalCollaborator": true}

// expandedUserBeans renders users with the expansions the request names:
// operations are the site permissions the person holds, and personalSpace is
// the space keyed for them when the caller can see it. Unexpanded properties
// are listed as expandable.
func (h *V1Handler) expandedUserBeans(w http.ResponseWriter, r *http.Request, ws, actor string, users []store.WikiUser) ([]map[string]any, bool) {
	expand := map[string]bool{}
	for _, raw := range r.URL.Query()["expand"] {
		for _, name := range strings.Split(raw, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if !userExpansions[name] {
				failure(w, 400, "expand accepts operations, personalSpace and isExternalCollaborator.")
				return nil, false
			}
			expand[name] = true
		}
	}
	beans := make([]map[string]any, 0, len(users))
	for _, user := range users {
		bean := h.wikiUserBean(user, false)
		expandable := map[string]string{}
		if expand["operations"] {
			admin, err := h.Store.IsAdmin(r.Context(), ws, user.AccountID)
			if err != nil {
				writeError(w, err)
				return nil, false
			}
			operations := []any{operation("use", "application")}
			if admin {
				operations = append(operations, operation("create", "space"), operation("administer", "application"))
			}
			bean["operations"] = operations
		} else {
			expandable["operations"] = ""
		}
		if expand["personalSpace"] {
			bean["personalSpace"] = nil
			space, err := h.Store.WikiSpaceByKey(r.Context(), ws, actor, store.PersonalSpaceKey(user.AccountID))
			if err == nil {
				bean["personalSpace"] = h.spaceBean(space, "plain", false)
			} else if !errors.Is(err, pgx.ErrNoRows) {
				writeError(w, err)
				return nil, false
			}
		} else {
			expandable["personalSpace"] = ""
		}
		if len(expandable) > 0 {
			bean["_expandable"] = expandable
		}
		beans = append(beans, bean)
	}
	return beans, true
}
