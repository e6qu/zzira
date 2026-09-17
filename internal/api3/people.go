package api3

import (
	"bytes"
	"encoding/json"
	"errors"
	"html"
	"io"
	"io/fs"
	"net/http"
	"net/mail"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/apps"
	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// People and identity: users, their search, groups, properties, preferences,
// columns, application roles and avatars, as the Jira API exposes them.

func peopleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrPeopleValidation):
		jiraError(w, http.StatusBadRequest, trimErrorPrefix(err))
	case errors.Is(err, store.ErrPeopleForbidden):
		jiraError(w, http.StatusForbidden, trimErrorPrefix(err))
	case errors.Is(err, store.ErrPeopleNotFound), errors.Is(err, store.ErrIssueMetadataNotFound):
		jiraError(w, http.StatusNotFound, trimErrorPrefix(err))
	case errors.Is(err, store.ErrWikiUserValidation):
		jiraError(w, http.StatusBadRequest, trimErrorPrefix(err))
	case errors.Is(err, store.ErrProjectPermission):
		jiraError(w, http.StatusForbidden, "You do not have permission to perform this operation.")
	default:
		jiraError(w, http.StatusInternalServerError, "internal error")
	}
}

// peopleRoute dispatches the people family and reports whether it handled the path.
func (h *Handler) peopleRoute(w http.ResponseWriter, r *http.Request, path string) bool {
	switch {
	case path == "/user" && r.Method == http.MethodGet:
		h.getUser(w, r)
	case path == "/user" && r.Method == http.MethodPost:
		h.createUser(w, r)
	case path == "/user" && r.Method == http.MethodDelete:
		h.removeUser(w, r)
	case path == "/user/bulk":
		h.bulkUsers(w, r)
	case path == "/user/bulk/migration":
		h.bulkUsersMigration(w, r)
	case path == "/user/columns":
		h.userColumns(w, r)
	case path == "/user/email":
		h.userEmail(w, r, false)
	case path == "/user/email/bulk":
		h.userEmail(w, r, true)
	case path == "/user/groups":
		h.userGroups(w, r)
	case path == "/users" || path == "/users/search":
		h.allUsers(w, r)
	case path == "/user/search":
		h.searchUsers(w, r)
	case path == "/user/assignable/search":
		h.assignableUsers(w, r)
	case path == "/user/assignable/multiProjectSearch":
		h.assignableUsersMultiProject(w, r)
	case path == "/user/viewissue/search":
		h.browseUsers(w, r)
	case path == "/user/picker":
		h.userPicker(w, r)
	case path == "/user/search/query":
		h.usersByQuery(w, r, false)
	case path == "/user/search/query/key":
		h.usersByQuery(w, r, true)
	case path == "/user/properties":
		h.userPropertyKeys(w, r)
	case strings.HasPrefix(path, "/user/properties/"):
		h.userProperty(w, r, strings.TrimPrefix(path, "/user/properties/"))
	case path == "/group":
		h.groupResource(w, r)
	case path == "/group/bulk":
		h.bulkGroups(w, r)
	case path == "/group/member":
		h.groupMembers(w, r)
	case path == "/group/user":
		h.groupUser(w, r)
	case path == "/groups/picker":
		h.groupPicker(w, r)
	case path == "/groupuserpicker":
		h.groupUserPicker(w, r)
	case path == "/mypreferences":
		h.myPreference(w, r)
	case path == "/mypreferences/locale":
		h.myLocale(w, r)
	case path == "/applicationrole":
		h.applicationRoles(w, r, "")
	case strings.HasPrefix(path, "/applicationrole/"):
		h.applicationRoles(w, r, strings.TrimPrefix(path, "/applicationrole/"))
	case strings.HasPrefix(path, "/avatar/") && strings.HasSuffix(path, "/system"):
		h.systemAvatars(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/avatar/"), "/system"))
	case strings.HasPrefix(path, "/universal_avatar/"):
		h.universalAvatarRoute(w, r, strings.Split(strings.TrimPrefix(path, "/universal_avatar/"), "/"))
	case strings.HasPrefix(path, "/project/") && (strings.HasSuffix(path, "/avatar") || strings.HasSuffix(path, "/avatars") || strings.HasSuffix(path, "/avatar2") || strings.Contains(path, "/avatar/")):
		h.projectAvatarRoute(w, r, strings.Split(strings.TrimPrefix(path, "/project/"), "/"))
	default:
		return false
	}
	return true
}

// ---- user beans ----

func (h *Handler) avatarURLs(url string) map[string]string {
	return map[string]string{"48x48": url, "32x32": url, "24x24": url, "16x16": url}
}

// fullUserBean is a user with the fields Jira sends for a user record. Email is
// shown only to the person themself or an administrator.
func (h *Handler) fullUserBean(u *models.User, showEmail bool) map[string]any {
	bean := h.userBean(u)
	bean["self"] = h.BaseURL + "/rest/api/3/user?accountId=" + u.ID
	bean["avatarUrls"] = h.avatarURLs(h.BaseURL + "/static/img/avatar-default.svg")
	if !showEmail {
		delete(bean, "emailAddress")
	}
	if u.TimeZone == "" {
		delete(bean, "timeZone")
	}
	return bean
}

func (h *Handler) userExpansions(r *http.Request, workspaceID string, u *models.User, bean map[string]any) {
	expand := r.URL.Query().Get("expand")
	if strings.Contains(expand, "groups") {
		groups, _ := h.Store.UserGroups(r.Context(), workspaceID, u.ID)
		items := make([]map[string]any, 0, len(groups))
		for _, g := range groups {
			items = append(items, h.groupNameBean(g))
		}
		bean["groups"] = map[string]any{"size": len(items), "items": items}
	}
	if strings.Contains(expand, "applicationRoles") {
		roles, _ := h.Store.UserApplicationRoles(r.Context(), workspaceID, u.ID)
		items := make([]map[string]any, 0, len(roles))
		for _, role := range roles {
			items = append(items, map[string]any{"key": role.Key, "name": role.Name})
		}
		bean["applicationRoles"] = map[string]any{"size": len(items), "items": items}
	}
}

// canSeeEmail reports whether the caller may see a person's email: their own,
// or anyone's for an administrator.
func (h *Handler) canSeeEmail(r *http.Request, workspaceID, actorID, subjectID string) bool {
	if actorID == subjectID {
		return true
	}
	admin, err := h.Store.IsAdmin(r.Context(), workspaceID, actorID)
	return err == nil && admin
}

// browsesUsers reports whether the caller holds Browse users and groups.
func (h *Handler) browsesUsers(r *http.Request, workspaceID, actorID string) bool {
	if allowed, err := h.Store.HasGlobalPermission(r.Context(), workspaceID, actorID, "USER_PICKER"); err == nil && allowed {
		return true
	}
	admin, err := h.Store.IsAdmin(r.Context(), workspaceID, actorID)
	return err == nil && admin
}

// requireBrowseUsers checks the Browse users and groups global permission.
func (h *Handler) requireBrowseUsers(w http.ResponseWriter, r *http.Request, workspaceID, actorID string) bool {
	allowed, err := h.Store.HasGlobalPermission(r.Context(), workspaceID, actorID, "USER_PICKER")
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return false
	}
	if !allowed {
		if admin, adminErr := h.Store.IsAdmin(r.Context(), workspaceID, actorID); adminErr == nil && admin {
			return true
		}
		jiraError(w, http.StatusForbidden, "You do not have the Browse users and groups global permission.")
		return false
	}
	return true
}

func pageParams(w http.ResponseWriter, r *http.Request, defaultMax int) (int, int, bool) {
	startAt, maxResults := 0, defaultMax
	if raw := r.URL.Query().Get("startAt"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			jiraError(w, http.StatusBadRequest, "startAt must be zero or greater.")
			return 0, 0, false
		}
		startAt = v
	}
	for _, name := range []string{"maxResults", "maxResult"} {
		if raw := r.URL.Query().Get(name); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil || v < 0 {
				jiraError(w, http.StatusBadRequest, name+" must be zero or greater.")
				return 0, 0, false
			}
			if v > 1000 {
				v = 1000
			}
			maxResults = v
		}
	}
	return startAt, maxResults, true
}

// ---- users ----

func (h *Handler) getUser(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	accountID := strings.TrimSpace(r.URL.Query().Get("accountId"))
	if accountID == "" {
		jiraError(w, http.StatusBadRequest, "The accountId parameter is required.")
		return
	}
	if accountID != actorID && !h.requireBrowseUsers(w, r, workspaceID, actorID) {
		return
	}
	u, err := h.Store.SiteUser(r.Context(), workspaceID, accountID)
	if err != nil {
		jiraError(w, http.StatusNotFound, "The user does not exist.")
		return
	}
	bean := h.fullUserBean(u, h.canSeeEmail(r, workspaceID, actorID, u.ID))
	h.userExpansions(r, workspaceID, u, bean)
	writeJSON(w, http.StatusOK, bean)
}

// createUser gives an address access to the site. An existing person who
// already has Jira access is answered 200 with their record, a new one 201.
func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, e := h.authWorkspaceAdmin(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	var request struct {
		EmailAddress    string   `json:"emailAddress"`
		DisplayName     string   `json:"displayName"`
		Products        []string `json:"products"`
		ApplicationKeys []string `json:"applicationKeys"`
		Name            string   `json:"name"`
		Key             string   `json:"key"`
		Password        string   `json:"password"`
		Self            string   `json:"self"`
	}
	if !decodeMetadataRequest(w, r, &request) {
		return
	}
	address, err := mail.ParseAddress(strings.TrimSpace(request.EmailAddress))
	if err != nil || request.Products == nil {
		jiraError(w, http.StatusBadRequest, "emailAddress and products are required.")
		return
	}
	for _, product := range request.Products {
		switch product {
		case "jira-software", "jira-servicedesk", "jira-product-discovery":
		default:
			jiraError(w, http.StatusBadRequest, "products contains an unknown Jira product.")
			return
		}
	}
	existingID, _, _, lookupErr := h.Store.UserByEmail(r.Context(), address.Address)
	if lookupErr == nil {
		if u, err := h.Store.SiteUser(r.Context(), workspaceID, existingID); err == nil {
			writeJSON(w, http.StatusOK, h.fullUserBean(u, true))
			return
		}
		if len(request.Products) == 0 {
			jiraError(w, http.StatusBadRequest, "The user exists but has no access to Jira, and no products were requested.")
			return
		}
	}
	passwordHash, err := authn.UnusablePasswordHash()
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err = h.Store.InviteWikiUsersByEmail(r.Context(), workspaceID, actorID, []string{address.Address}, passwordHash); err != nil {
		peopleError(w, err)
		return
	}
	id, _, _, err := h.Store.UserByEmail(r.Context(), address.Address)
	if err != nil {
		peopleError(w, err)
		return
	}
	u, err := h.Store.SiteUser(r.Context(), workspaceID, id)
	if err != nil {
		peopleError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, h.fullUserBean(u, true))
}

func (h *Handler) removeUser(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, e := h.authWorkspaceAdmin(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	accountID := strings.TrimSpace(r.URL.Query().Get("accountId"))
	if accountID == "" {
		jiraError(w, http.StatusBadRequest, "The accountId parameter is required.")
		return
	}
	if err := h.Store.RemoveSiteUser(r.Context(), workspaceID, actorID, accountID); err != nil {
		peopleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) bulkUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	startAt, maxResults, ok := pageParams(w, r, 10)
	if !ok {
		return
	}
	ids := securityQueryValues(r, "accountId")
	if len(ids) == 0 {
		jiraError(w, http.StatusBadRequest, "accountId is required.")
		return
	}
	users, err := h.Store.SiteUsersByIDs(r.Context(), workspaceID, ids)
	if err != nil {
		peopleError(w, err)
		return
	}
	values := make([]map[string]any, 0, len(users))
	for _, u := range users {
		values = append(values, h.fullUserBean(u, h.canSeeEmail(r, workspaceID, actorID, u.ID)))
	}
	page := pageSlice(values, startAt, maxResults)
	writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(values), startAt, maxResults))
}

// bulkUsersMigration maps legacy usernames and user keys to account ids. Cloud
// people have no username or key, so nothing matches, and Jira answers with an
// empty list rather than an error.
func (h *Handler) bulkUsersMigration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	keys, usernames := securityQueryValues(r, "key"), securityQueryValues(r, "username")
	if len(keys) == 0 && len(usernames) == 0 {
		jiraError(w, http.StatusBadRequest, "key or username is required.")
		return
	}
	startAt, maxResults, ok := pageParams(w, r, 10)
	if !ok {
		return
	}
	// Each handle the caller asked about names its own entry, so a request by
	// key answers with the key it asked with and one by username with that.
	out := []map[string]string{}
	for _, asked := range []struct {
		field   string
		handles []string
	}{{"key", keys}, {"username", usernames}} {
		for _, handle := range asked.handles {
			u, err := h.Store.SiteUserByHandle(r.Context(), workspaceID, handle)
			// A handle naming nobody is left out, as Jira leaves it out; any
			// other failure is this site's, and saying nothing about it would
			// read as though the person did not exist.
			if errors.Is(err, store.ErrPeopleNotFound) {
				continue
			}
			if err != nil {
				jiraError(w, http.StatusInternalServerError, "Could not look up the people named.")
				return
			}
			out = append(out, map[string]string{"accountId": u.ID, asked.field: handle})
		}
	}
	writeJSON(w, http.StatusOK, pageSlice(out, startAt, maxResults))
}

func (h *Handler) userColumns(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	target := strings.TrimSpace(r.URL.Query().Get("accountId"))
	if target == "" {
		target = actorID
	}
	if target != actorID {
		if admin, err := h.Store.IsAdmin(r.Context(), workspaceID, actorID); err != nil || !admin {
			jiraError(w, http.StatusForbidden, "Only administrators can manage another person's columns.")
			return
		}
	}
	switch r.Method {
	case http.MethodGet:
		columns, err := h.Store.UserColumns(r.Context(), workspaceID, target)
		if err != nil {
			peopleError(w, err)
			return
		}
		out := make([]map[string]string, 0, len(columns))
		for _, c := range columns {
			out = append(out, map[string]string{"label": issueTableColumnLabel(c), "value": c})
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodPut:
		// Columns arrive as form data, as Jira documents: columns=summary&columns=status.
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		if err := r.ParseForm(); err != nil {
			jiraError(w, http.StatusBadRequest, "Columns must be sent as form data.")
			return
		}
		columns := r.PostForm["columns"]
		if len(columns) == 1 && strings.Contains(columns[0], ",") {
			columns = strings.Split(columns[0], ",")
		}
		if err := h.Store.SetUserColumns(r.Context(), workspaceID, target, columns); err != nil {
			if errors.Is(err, store.ErrPeopleValidation) {
				jiraError(w, http.StatusInternalServerError, trimErrorPrefix(err))
				return
			}
			peopleError(w, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		if err := h.Store.ResetUserColumns(r.Context(), workspaceID, target); err != nil {
			peopleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w)
	}
}

func issueTableColumnLabel(id string) string {
	labels := map[string]string{
		"issuekey": "Key", "issuetype": "Issue Type", "summary": "Summary", "assignee": "Assignee",
		"reporter": "Reporter", "priority": "Priority", "status": "Status", "resolution": "Resolution",
		"created": "Created", "updated": "Updated", "duedate": "Due", "labels": "Labels",
		"components": "Components", "fixVersions": "Fix versions", "versions": "Affects versions",
		"resolutiondate": "Resolved", "creator": "Creator", "project": "Project", "parent": "Parent",
	}
	if label, ok := labels[id]; ok {
		return label
	}
	return id
}

// userEmail returns email addresses to apps. Jira offers it only to approved
// apps; a person calling it is refused as an unapproved caller.
func (h *Handler) userEmail(w http.ResponseWriter, r *http.Request, bulk bool) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if _, isApp := apps.InstallationFromContext(r.Context()); !isApp {
		jiraError(w, http.StatusBadRequest, "The calling app is not approved to use this API.")
		return
	}
	ids := securityQueryValues(r, "accountId")
	if len(ids) == 0 {
		jiraError(w, http.StatusBadRequest, "accountId is required.")
		return
	}
	if !bulk {
		u, err := h.Store.SiteUser(r.Context(), workspaceID, ids[0])
		if err != nil {
			jiraError(w, http.StatusNotFound, "A user with the given accountId does not exist.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"accountId": u.ID, "email": u.Email})
		return
	}
	users, err := h.Store.SiteUsersByIDs(r.Context(), workspaceID, ids)
	if err != nil {
		peopleError(w, err)
		return
	}
	out := make([]map[string]string, 0, len(users))
	for _, u := range users {
		out = append(out, map[string]string{"accountId": u.ID, "email": u.Email})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) groupNameBean(g store.SiteGroup) map[string]any {
	return map[string]any{"groupId": g.ID, "name": g.Name, "self": h.BaseURL + "/rest/api/3/group?groupId=" + g.ID}
}

func (h *Handler) userGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	accountID := strings.TrimSpace(r.URL.Query().Get("accountId"))
	if accountID == "" {
		jiraError(w, http.StatusBadRequest, "The accountId parameter is required.")
		return
	}
	if accountID != actorID && !h.requireBrowseUsers(w, r, workspaceID, actorID) {
		return
	}
	groups, err := h.Store.UserGroups(r.Context(), workspaceID, accountID)
	if err != nil {
		peopleError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		out = append(out, h.groupNameBean(g))
	}
	writeJSON(w, http.StatusOK, out)
}

// allUsers lists everyone in the site, active, inactive and app accounts alike.
func (h *Handler) allUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if !h.requireBrowseUsers(w, r, workspaceID, actorID) {
		return
	}
	startAt, maxResults, ok := pageParams(w, r, 50)
	if !ok {
		return
	}
	users, err := h.Store.SiteUsers(r.Context(), workspaceID)
	if err != nil {
		peopleError(w, err)
		return
	}
	page := pageSlice(users, startAt, maxResults)
	out := make([]map[string]any, 0, len(page))
	for _, u := range page {
		out = append(out, h.fullUserBean(u, h.canSeeEmail(r, workspaceID, actorID, u.ID)))
	}
	writeJSON(w, http.StatusOK, out)
}

// userMatches reports whether a person matches a search string: a
// case-insensitive match on display name, or an exact match on email.
func userMatches(u *models.User, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(u.DisplayName), query) || strings.EqualFold(u.Email, query) || strings.HasPrefix(strings.ToLower(u.Email), query)
}

// searchUsers finds active people by query, account id or property. Jira needs
// at least one of the three.
func (h *Handler) searchUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	query, accountID, property := r.URL.Query().Get("query"), r.URL.Query().Get("accountId"), r.URL.Query().Get("property")
	if query == "" && accountID == "" && property == "" {
		jiraError(w, http.StatusBadRequest, "One of query, accountId or property is required.")
		return
	}
	if query != "" && accountID != "" {
		jiraError(w, http.StatusBadRequest, "query and accountId cannot both be given.")
		return
	}
	startAt, maxResults, ok := pageParams(w, r, 50)
	if !ok {
		return
	}
	// Anonymous calls and calls without Browse users and groups find nobody.
	if !h.browsesUsers(r, workspaceID, actorID) {
		writeJSON(w, http.StatusOK, []map[string]any{})
		return
	}
	users, err := h.Store.SiteUsers(r.Context(), workspaceID)
	if err != nil {
		peopleError(w, err)
		return
	}
	propertyMatch := map[string]bool{}
	if property != "" {
		matched, err := h.usersMatchingProperty(r, workspaceID, property)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "The property expression is not valid.")
			return
		}
		propertyMatch = matched
	}
	matches := []*models.User{}
	for _, u := range users {
		if !u.Active || u.AccountType == "app" {
			continue
		}
		if accountID != "" && u.ID != accountID {
			continue
		}
		if query != "" && !userMatches(u, query) {
			continue
		}
		if property != "" && !propertyMatch[u.ID] {
			continue
		}
		matches = append(matches, u)
	}
	page := pageSlice(matches, startAt, maxResults)
	out := make([]map[string]any, 0, len(page))
	for _, u := range page {
		out = append(out, h.fullUserBean(u, h.canSeeEmail(r, workspaceID, actorID, u.ID)))
	}
	writeJSON(w, http.StatusOK, out)
}

// usersMatchingProperty evaluates a property expression — key.path=value — against
// the site's user properties.
func (h *Handler) usersMatchingProperty(r *http.Request, workspaceID, expression string) (map[string]bool, error) {
	left, value, ok := strings.Cut(expression, "=")
	if !ok {
		return nil, store.ErrPeopleValidation
	}
	key, path, _ := strings.Cut(strings.TrimSpace(left), ".")
	return h.usersWithPropertyValue(r, workspaceID, key, path, strings.Trim(strings.TrimSpace(value), `"`))
}

func (h *Handler) usersWithPropertyValue(r *http.Request, workspaceID, key, path, want string) (map[string]bool, error) {
	values, err := h.Store.UserPropertyValues(r.Context(), workspaceID, key)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for id, raw := range values {
		var decoded any
		if json.Unmarshal(raw, &decoded) != nil {
			continue
		}
		current := decoded
		if path != "" {
			for _, part := range strings.Split(path, ".") {
				object, isObject := current.(map[string]any)
				if !isObject {
					current = nil
					break
				}
				current = object[part]
			}
		}
		switch v := current.(type) {
		case string:
			out[id] = v == want
		case float64:
			out[id] = strconv.FormatFloat(v, 'f', -1, 64) == want
		case bool:
			out[id] = strconv.FormatBool(v) == want
		}
	}
	return out, nil
}

// usersWithProjectPermission filters active people to those holding a permission
// in a project, or on one issue when issueID is given.
func (h *Handler) usersWithProjectPermission(r *http.Request, workspaceID, projectID, issueID, permission string, users []*models.User) []*models.User {
	out := []*models.User{}
	for _, u := range users {
		if !u.Active || u.AccountType == "app" {
			continue
		}
		if allowed, err := h.Store.HasProjectPermission(r.Context(), workspaceID, u.ID, projectID, issueID, permission); err == nil && allowed {
			out = append(out, u)
		}
	}
	return out
}

func (h *Handler) assignableUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	q := r.URL.Query()
	projectRef, issueKey, issueID := q.Get("project"), q.Get("issueKey"), q.Get("issueId")
	if projectRef == "" && issueKey == "" && issueID == "" {
		jiraError(w, http.StatusBadRequest, "One of issueKey, issueId or project is required.")
		return
	}
	startAt, maxResults, ok := pageParams(w, r, 50)
	if !ok {
		return
	}
	projectID, scopedIssue := "", ""
	if issueKey != "" || issueID != "" {
		ref := issueKey
		if ref == "" {
			ref = issueID
		}
		issue, err := h.Store.IssueByIDOrKey(r.Context(), workspaceID, ref)
		if err != nil {
			jiraError(w, http.StatusNotFound, "The issue was not found.")
			return
		}
		projectID, scopedIssue = issue.ProjectID, issue.ID
	} else {
		project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, projectRef)
		if err != nil {
			jiraError(w, http.StatusNotFound, "The project was not found.")
			return
		}
		projectID = project.ID
	}
	users, err := h.Store.SiteUsers(r.Context(), workspaceID)
	if err != nil {
		peopleError(w, err)
		return
	}
	query, accountID := q.Get("query"), q.Get("accountId")
	candidates := []*models.User{}
	for _, u := range users {
		if (accountID == "" || u.ID == accountID) && userMatches(u, query) {
			candidates = append(candidates, u)
		}
	}
	matches := h.usersWithProjectPermission(r, workspaceID, projectID, scopedIssue, "ASSIGNABLE_USER", candidates)
	page := pageSlice(matches, startAt, maxResults)
	out := make([]map[string]any, 0, len(page))
	for _, u := range page {
		out = append(out, h.fullUserBean(u, h.canSeeEmail(r, workspaceID, actorID, u.ID)))
	}
	writeJSON(w, http.StatusOK, out)
}

// assignableUsersMultiProject lists people assignable in every one of the named
// projects.
func (h *Handler) assignableUsersMultiProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	keys := securityQueryValues(r, "projectKeys")
	if len(keys) == 0 {
		jiraError(w, http.StatusBadRequest, "projectKeys is required.")
		return
	}
	startAt, maxResults, ok := pageParams(w, r, 50)
	if !ok {
		return
	}
	users, err := h.Store.SiteUsers(r.Context(), workspaceID)
	if err != nil {
		peopleError(w, err)
		return
	}
	query, accountID := r.URL.Query().Get("query"), r.URL.Query().Get("accountId")
	candidates := []*models.User{}
	for _, u := range users {
		if (accountID == "" || u.ID == accountID) && userMatches(u, query) {
			candidates = append(candidates, u)
		}
	}
	for _, key := range keys {
		project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, key)
		if err != nil {
			jiraError(w, http.StatusNotFound, "One or more of the projects was not found.")
			return
		}
		if allowed, browseErr := h.canBrowseProject(r, workspaceID, actorID, project.ID); browseErr != nil || !allowed {
			jiraError(w, http.StatusNotFound, "One or more of the projects was not found.")
			return
		}
		candidates = h.usersWithProjectPermission(r, workspaceID, project.ID, "", "ASSIGNABLE_USER", candidates)
	}
	page := pageSlice(candidates, startAt, maxResults)
	out := make([]map[string]any, 0, len(page))
	for _, u := range page {
		out = append(out, h.fullUserBean(u, h.canSeeEmail(r, workspaceID, actorID, u.ID)))
	}
	writeJSON(w, http.StatusOK, out)
}

// browseUsers lists people who can browse an issue or any issue in a project.
func (h *Handler) browseUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	q := r.URL.Query()
	issueKey, projectKey := q.Get("issueKey"), q.Get("projectKey")
	if issueKey == "" && projectKey == "" {
		jiraError(w, http.StatusBadRequest, "issueKey or projectKey is required.")
		return
	}
	startAt, maxResults, ok := pageParams(w, r, 50)
	if !ok {
		return
	}
	// Anonymous calls and calls without Browse users and groups find nobody.
	if !h.browsesUsers(r, workspaceID, actorID) {
		writeJSON(w, http.StatusOK, []map[string]any{})
		return
	}
	projectID, issueID, issueSecurityLevel := "", "", ""
	if issueKey != "" {
		issue, err := h.Store.IssueByIDOrKey(r.Context(), workspaceID, issueKey)
		if err != nil {
			jiraError(w, http.StatusNotFound, "The issue was not found.")
			return
		}
		projectID, issueID, issueSecurityLevel = issue.ProjectID, issue.ID, issue.SecurityLevelID
	} else {
		project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, projectKey)
		if err != nil {
			jiraError(w, http.StatusNotFound, "The project was not found.")
			return
		}
		projectID = project.ID
	}
	users, err := h.Store.SiteUsers(r.Context(), workspaceID)
	if err != nil {
		peopleError(w, err)
		return
	}
	query, accountID := q.Get("query"), q.Get("accountId")
	candidates := []*models.User{}
	for _, u := range users {
		if (accountID == "" || u.ID == accountID) && userMatches(u, query) {
			candidates = append(candidates, u)
		}
	}
	matches := h.usersWithProjectPermission(r, workspaceID, projectID, issueID, "BROWSE_PROJECTS", candidates)
	if issueID != "" {
		visible := []*models.User{}
		for _, u := range matches {
			if seen, err := authz.CanSeeIssue(r.Context(), h.Store, workspaceID, projectID, u.ID, issueID, issueSecurityLevel); err == nil && seen {
				visible = append(visible, u)
			}
		}
		matches = visible
	}
	page := pageSlice(matches, startAt, maxResults)
	out := make([]map[string]any, 0, len(page))
	for _, u := range page {
		out = append(out, h.fullUserBean(u, h.canSeeEmail(r, workspaceID, actorID, u.ID)))
	}
	writeJSON(w, http.StatusOK, out)
}

// highlight wraps the matched query term in strong tags, as the pickers do.
func pickerHTML(text, query string) string {
	escaped := html.EscapeString(text)
	query = strings.TrimSpace(query)
	if query == "" {
		return escaped
	}
	lower, needle := strings.ToLower(escaped), strings.ToLower(html.EscapeString(query))
	index := strings.Index(lower, needle)
	if index < 0 {
		return escaped
	}
	return escaped[:index] + "<strong>" + escaped[index:index+len(needle)] + "</strong>" + escaped[index+len(needle):]
}

func (h *Handler) userPicker(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	query := r.URL.Query().Get("query")
	if strings.TrimSpace(query) == "" {
		jiraError(w, http.StatusBadRequest, "query is required.")
		return
	}
	if len(r.URL.Query()["exclude"]) > 0 && len(r.URL.Query()["excludeAccountIds"]) > 0 {
		jiraError(w, http.StatusBadRequest, "exclude and excludeAccountIds cannot both be given.")
		return
	}
	_, maxResults, ok := pageParams(w, r, 50)
	if !ok {
		return
	}
	exclude := stringQuerySet(append(securityQueryValues(r, "excludeAccountIds"), securityQueryValues(r, "exclude")...))
	users, err := h.Store.SiteUsers(r.Context(), workspaceID)
	if err != nil {
		peopleError(w, err)
		return
	}
	excludeApps := strings.EqualFold(r.URL.Query().Get("excludeConnectUsers"), "true")
	// Without Browse users and groups only an exact name matches.
	browses := h.browsesUsers(r, workspaceID, actorID)
	matches := []map[string]any{}
	for _, u := range users {
		if !u.Active || exclude[u.ID] || !userMatches(u, query) || (excludeApps && u.AccountType == "app") {
			continue
		}
		if !browses && !strings.EqualFold(strings.TrimSpace(query), u.DisplayName) {
			continue
		}
		matches = append(matches, map[string]any{
			"accountId": u.ID, "accountType": u.AccountType, "displayName": u.DisplayName,
			"html": pickerHTML(u.DisplayName, query), "avatarUrl": h.BaseURL + "/static/img/avatar-default.svg",
		})
	}
	total := len(matches)
	if len(matches) > maxResults {
		matches = matches[:maxResults]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"users": matches, "total": total,
		"header": "Showing " + strconv.Itoa(len(matches)) + " of " + strconv.Itoa(total) + " matching users",
	})
}

// usersByQuery evaluates Jira's structured user query.
func (h *Handler) usersByQuery(w http.ResponseWriter, r *http.Request, keysOnly bool) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if !h.requireBrowseUsers(w, r, workspaceID, actorID) {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	if query == "" {
		jiraError(w, http.StatusBadRequest, "query is required.")
		return
	}
	startAt, maxResults, ok := pageParams(w, r, 100)
	if !ok {
		return
	}
	matched, err := h.evaluateUserQuery(r, workspaceID, query)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "The query is invalid: "+trimErrorPrefix(err))
		return
	}
	users, err := h.Store.SiteUsersByIDs(r.Context(), workspaceID, matched)
	if err != nil {
		peopleError(w, err)
		return
	}
	sort.SliceStable(users, func(i, j int) bool {
		return strings.ToLower(users[i].DisplayName) < strings.ToLower(users[j].DisplayName)
	})
	values := make([]map[string]any, 0, len(users))
	for _, u := range users {
		if keysOnly {
			values = append(values, map[string]any{"accountId": u.ID, "key": u.ID})
		} else {
			values = append(values, h.fullUserBean(u, h.canSeeEmail(r, workspaceID, actorID, u.ID)))
		}
	}
	page := pageSlice(values, startAt, maxResults)
	writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(values), startAt, maxResults))
}

// ---- user properties ----

// propertySubject resolves whose properties a request reads: the caller's own
// unless an accountId is given, which only an administrator may use for others.
func (h *Handler) propertySubject(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return "", "", false
	}
	accountID := strings.TrimSpace(r.URL.Query().Get("accountId"))
	if accountID == "" {
		jiraError(w, http.StatusBadRequest, "accountId is required.")
		return "", "", false
	}
	if accountID != actorID {
		admin, err := h.Store.IsAdmin(r.Context(), workspaceID, actorID)
		if err != nil || !admin {
			jiraError(w, http.StatusForbidden, "You can only access your own user properties.")
			return "", "", false
		}
	}
	return workspaceID, accountID, true
}

func (h *Handler) userPropertyKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, accountID, ok := h.propertySubject(w, r)
	if !ok {
		return
	}
	keys, err := h.Store.UserPropertyKeys(r.Context(), workspaceID, accountID)
	if err != nil {
		peopleError(w, err)
		return
	}
	out := make([]map[string]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, map[string]string{"key": key, "self": h.BaseURL + "/rest/api/3/user/properties/" + key + "?accountId=" + accountID})
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": out})
}

func (h *Handler) userProperty(w http.ResponseWriter, r *http.Request, key string) {
	if strings.TrimSpace(key) == "" {
		jiraError(w, http.StatusMethodNotAllowed, "The property key is not specified.")
		return
	}
	workspaceID, accountID, ok := h.propertySubject(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		value, err := h.Store.UserProperty(r.Context(), workspaceID, accountID, key)
		if err != nil {
			peopleError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": value})
	case http.MethodPut:
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			jiraError(w, http.StatusBadRequest, "A property value is required.")
			return
		}
		created, err := h.Store.SetUserProperty(r.Context(), workspaceID, accountID, key, body)
		if err != nil {
			peopleError(w, err)
			return
		}
		if created {
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		if err := h.Store.DeleteUserProperty(r.Context(), workspaceID, accountID, key); err != nil {
			peopleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w)
	}
}

// ---- groups ----

func (h *Handler) groupRef(r *http.Request) (string, string) {
	q := r.URL.Query()
	name := q.Get("groupname")
	if name == "" {
		name = q.Get("groupName")
	}
	return strings.TrimSpace(q.Get("groupId")), name
}

func (h *Handler) groupResource(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		workspaceID, actorID, e := h.authWorkspace(r)
		if e != nil {
			writeJerr(w, e)
			return
		}
		if !h.requireBrowseUsers(w, r, workspaceID, actorID) {
			return
		}
		id, name := h.groupRef(r)
		g, err := h.Store.SiteGroupByIDOrName(r.Context(), workspaceID, id, name)
		if err != nil {
			peopleError(w, err)
			return
		}
		members, err := h.Store.SiteGroupMembers(r.Context(), workspaceID, g.ID, false)
		if err != nil {
			peopleError(w, err)
			return
		}
		bean := h.groupNameBean(g)
		items := []map[string]any{}
		if strings.Contains(r.URL.Query().Get("expand"), "users") {
			for _, u := range members {
				items = append(items, h.fullUserBean(u, h.canSeeEmail(r, workspaceID, actorID, u.ID)))
			}
		}
		bean["users"] = map[string]any{"size": len(members), "items": items, "max-results": 50, "start-index": 0, "end-index": len(items)}
		bean["expand"] = "users"
		writeJSON(w, http.StatusOK, bean)
	case http.MethodPost:
		workspaceID, actorID, e := h.authWorkspaceAdmin(r)
		if e != nil {
			writeJerr(w, e)
			return
		}
		var request struct {
			Name string `json:"name"`
		}
		if !decodeMetadataRequest(w, r, &request) {
			return
		}
		created, err := h.Store.CreateWikiGroup(r.Context(), workspaceID, actorID, request.Name)
		if err != nil {
			jiraError(w, http.StatusBadRequest, trimErrorPrefix(err))
			return
		}
		bean := h.groupNameBean(store.SiteGroup(created))
		bean["users"] = map[string]any{"size": 0, "items": []any{}, "max-results": 50, "start-index": 0, "end-index": 0}
		writeJSON(w, http.StatusCreated, bean)
	case http.MethodDelete:
		workspaceID, actorID, e := h.authWorkspaceAdmin(r)
		if e != nil {
			writeJerr(w, e)
			return
		}
		id, name := h.groupRef(r)
		g, err := h.Store.SiteGroupByIDOrName(r.Context(), workspaceID, id, name)
		if err != nil {
			peopleError(w, err)
			return
		}
		swapID, swapName := strings.TrimSpace(r.URL.Query().Get("swapGroupId")), r.URL.Query().Get("swapGroup")
		swap := store.SiteGroup{}
		if swapID != "" || swapName != "" {
			if swap, err = h.Store.SiteGroupByIDOrName(r.Context(), workspaceID, swapID, swapName); err != nil {
				peopleError(w, err)
				return
			}
			if swap.ID == g.ID {
				jiraError(w, http.StatusBadRequest, "A group cannot be swapped for itself.")
				return
			}
		}
		if err := h.Store.DeleteSiteGroup(r.Context(), workspaceID, actorID, g.ID, swap.ID); err != nil {
			peopleError(w, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) bulkGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if !h.requireBrowseUsers(w, r, workspaceID, actorID) {
		return
	}
	startAt, maxResults, ok := pageParams(w, r, 50)
	if !ok {
		return
	}
	groups, err := h.Store.SiteGroups(r.Context(), workspaceID)
	if err != nil {
		peopleError(w, err)
		return
	}
	ids, names := stringQuerySet(securityQueryValues(r, "groupId")), stringQuerySet(securityQueryValues(r, "groupName"))
	values := []map[string]any{}
	for _, g := range groups {
		if (len(ids) > 0 || len(names) > 0) && !ids[g.ID] && !names[g.Name] {
			continue
		}
		values = append(values, map[string]any{"groupId": g.ID, "name": g.Name})
	}
	page := pageSlice(values, startAt, maxResults)
	writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(values), startAt, maxResults))
}

func (h *Handler) groupMembers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if !h.requireBrowseUsers(w, r, workspaceID, actorID) {
		return
	}
	startAt, maxResults, ok := pageParams(w, r, 50)
	if !ok {
		return
	}
	id, name := h.groupRef(r)
	g, err := h.Store.SiteGroupByIDOrName(r.Context(), workspaceID, id, name)
	if err != nil {
		peopleError(w, err)
		return
	}
	members, err := h.Store.SiteGroupMembers(r.Context(), workspaceID, g.ID, strings.EqualFold(r.URL.Query().Get("includeInactiveUsers"), "true"))
	if err != nil {
		peopleError(w, err)
		return
	}
	values := make([]map[string]any, 0, len(members))
	for _, u := range members {
		values = append(values, h.fullUserBean(u, h.canSeeEmail(r, workspaceID, actorID, u.ID)))
	}
	page := pageSlice(values, startAt, maxResults)
	writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(values), startAt, maxResults))
}

func (h *Handler) groupUser(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, e := h.authWorkspaceAdmin(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	id, name := h.groupRef(r)
	if id == "" && name == "" {
		jiraError(w, http.StatusBadRequest, "groupname is required.")
		return
	}
	g, err := h.Store.SiteGroupByIDOrName(r.Context(), workspaceID, id, name)
	if err != nil {
		peopleError(w, err)
		return
	}
	switch r.Method {
	case http.MethodPost:
		var request struct {
			AccountID string `json:"accountId"`
			Name      string `json:"name"`
		}
		if !decodeMetadataRequest(w, r, &request) {
			return
		}
		if strings.TrimSpace(request.AccountID) == "" {
			jiraError(w, http.StatusBadRequest, "accountId is required.")
			return
		}
		if _, err := h.Store.SiteUser(r.Context(), workspaceID, request.AccountID); err != nil {
			peopleError(w, err)
			return
		}
		if err := h.Store.SetWikiGroupMembership(r.Context(), workspaceID, actorID, g.ID, request.AccountID, true); err != nil {
			peopleError(w, err)
			return
		}
		bean := h.groupNameBean(g)
		members, _ := h.Store.SiteGroupMembers(r.Context(), workspaceID, g.ID, false)
		bean["users"] = map[string]any{"size": len(members), "items": []any{}, "max-results": 50, "start-index": 0, "end-index": 0}
		writeJSON(w, http.StatusCreated, bean)
	case http.MethodDelete:
		accountID := strings.TrimSpace(r.URL.Query().Get("accountId"))
		if accountID == "" {
			jiraError(w, http.StatusBadRequest, "accountId is required.")
			return
		}
		if _, err := h.Store.SiteUser(r.Context(), workspaceID, accountID); err != nil {
			peopleError(w, err)
			return
		}
		if err := h.Store.SetWikiGroupMembership(r.Context(), workspaceID, actorID, g.ID, accountID, false); err != nil {
			peopleError(w, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) groupPickerMatches(r *http.Request, workspaceID, query string, caseInsensitive bool, exclude map[string]bool, accountID string) ([]store.SiteGroup, error) {
	groups, err := h.Store.SiteGroups(r.Context(), workspaceID)
	if err != nil {
		return nil, err
	}
	member := map[string]bool{}
	if accountID != "" {
		mine, err := h.Store.UserGroups(r.Context(), workspaceID, accountID)
		if err != nil {
			return nil, err
		}
		for _, g := range mine {
			member[g.ID] = true
		}
	}
	out := []store.SiteGroup{}
	for _, g := range groups {
		if exclude[g.ID] || exclude[g.Name] {
			continue
		}
		if accountID != "" && !member[g.ID] {
			continue
		}
		name, needle := g.Name, query
		if caseInsensitive {
			name, needle = strings.ToLower(name), strings.ToLower(needle)
		}
		if needle != "" && !strings.Contains(name, needle) {
			continue
		}
		out = append(out, g)
	}
	return out, nil
}

func (h *Handler) groupPicker(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	q := r.URL.Query()
	maxResults := 20
	if raw := q.Get("maxResults"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
			maxResults = v
		}
	}
	exclude := stringQuerySet(append(securityQueryValues(r, "exclude"), securityQueryValues(r, "excludeId")...))
	matches, err := h.groupPickerMatches(r, workspaceID, q.Get("query"), strings.EqualFold(q.Get("caseInsensitive"), "true"), exclude, q.Get("accountId"))
	if err != nil {
		peopleError(w, err)
		return
	}
	total := len(matches)
	if len(matches) > maxResults {
		matches = matches[:maxResults]
	}
	items := make([]map[string]any, 0, len(matches))
	for _, g := range matches {
		items = append(items, map[string]any{"groupId": g.ID, "name": g.Name, "html": pickerHTML(g.Name, q.Get("query")), "labels": []any{}})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"groups": items, "total": total,
		"header": "Showing " + strconv.Itoa(len(items)) + " of " + strconv.Itoa(total) + " matching groups",
	})
}

func (h *Handler) groupUserPicker(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if !h.requireBrowseUsers(w, r, workspaceID, actorID) {
		return
	}
	q := r.URL.Query()
	query := q.Get("query")
	if strings.TrimSpace(query) == "" {
		jiraError(w, http.StatusBadRequest, "query is required.")
		return
	}
	maxResults := 50
	if raw := q.Get("maxResults"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
			maxResults = v
		}
	}
	users, err := h.Store.SiteUsers(r.Context(), workspaceID)
	if err != nil {
		peopleError(w, err)
		return
	}
	excludeApps := strings.EqualFold(q.Get("excludeConnectAddons"), "true")
	userItems := []map[string]any{}
	for _, u := range users {
		if !u.Active || !userMatches(u, query) || (excludeApps && u.AccountType == "app") {
			continue
		}
		userItems = append(userItems, map[string]any{"accountId": u.ID, "accountType": u.AccountType, "displayName": u.DisplayName,
			"html": pickerHTML(u.DisplayName, query), "avatarUrl": h.BaseURL + "/static/img/avatar-default.svg"})
	}
	userTotal := len(userItems)
	if len(userItems) > maxResults {
		userItems = userItems[:maxResults]
	}
	// Group names match case-sensitively unless asked otherwise, as Jira documents.
	groups, err := h.groupPickerMatches(r, workspaceID, query, strings.EqualFold(q.Get("caseInsensitive"), "true"), nil, "")
	if err != nil {
		peopleError(w, err)
		return
	}
	groupTotal := len(groups)
	if len(groups) > maxResults {
		groups = groups[:maxResults]
	}
	groupItems := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		groupItems = append(groupItems, map[string]any{"groupId": g.ID, "name": g.Name, "html": pickerHTML(g.Name, query), "labels": []any{}})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"users":  map[string]any{"users": userItems, "total": userTotal, "header": "Showing " + strconv.Itoa(len(userItems)) + " of " + strconv.Itoa(userTotal) + " matching users"},
		"groups": map[string]any{"groups": groupItems, "total": groupTotal, "header": "Showing " + strconv.Itoa(len(groupItems)) + " of " + strconv.Itoa(groupTotal) + " matching groups"},
	})
}

// ---- preferences ----

func (h *Handler) myPreference(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	key := r.URL.Query().Get("key")
	if strings.TrimSpace(key) == "" {
		jiraError(w, http.StatusNotFound, "The key is not provided.")
		return
	}
	switch r.Method {
	case http.MethodGet:
		value, err := h.Store.UserPreference(r.Context(), workspaceID, actorID, key)
		if err != nil {
			peopleError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	case http.MethodPut:
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<12))
		if err != nil {
			jiraError(w, http.StatusNotFound, "The value is not provided.")
			return
		}
		value := strings.TrimSpace(string(body))
		// The value is plain text; a JSON string is accepted too and unwrapped.
		var quoted string
		if json.Unmarshal(body, &quoted) == nil {
			value = quoted
		}
		if value == "" {
			jiraError(w, http.StatusNotFound, "The value is not provided.")
			return
		}
		if err = h.Store.SetUserPreference(r.Context(), workspaceID, actorID, key, value); err != nil {
			peopleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := h.Store.DeleteUserPreference(r.Context(), workspaceID, actorID, key); err != nil {
			peopleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w)
	}
}

// supportedLocales are the locales a person may choose.
var supportedLocales = map[string]bool{
	"en_US": true, "en_GB": true, "de_DE": true, "fr_FR": true, "es_ES": true, "ja_JP": true,
	"pt_BR": true, "it_IT": true, "nl_NL": true, "ko_KR": true, "zh_CN": true, "zh_TW": true,
	"ru_RU": true, "pl_PL": true, "sv_SE": true, "da_DK": true, "fi_FI": true, "nb_NO": true,
	"cs_CZ": true, "hu_HU": true, "tr_TR": true,
}

func (h *Handler) myLocale(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		workspaceID, actorID, e := h.authWorkspace(r)
		if e != nil {
			writeJerr(w, e)
			return
		}
		if value, err := h.Store.UserPreference(r.Context(), workspaceID, actorID, store.UserPreferenceLocaleKey); err == nil {
			writeJSON(w, http.StatusOK, map[string]string{"locale": value})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"locale": acceptLanguageLocale(r)})
	case http.MethodPut:
		workspaceID, actorID, e := h.authWorkspace(r)
		if e != nil {
			writeJerr(w, e)
			return
		}
		var request struct {
			Locale string `json:"locale"`
		}
		if !decodeMetadataRequest(w, r, &request) {
			return
		}
		if !supportedLocales[request.Locale] {
			jiraError(w, http.StatusBadRequest, "The locale is not supported.")
			return
		}
		if err := h.Store.SetUserPreference(r.Context(), workspaceID, actorID, store.UserPreferenceLocaleKey, request.Locale); err != nil {
			peopleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w)
	}
}

// acceptLanguageLocale picks the locale the browser asked for when a person has
// none set, falling back to the site default.
func acceptLanguageLocale(r *http.Request) string {
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		locale := strings.ReplaceAll(tag, "-", "_")
		if supportedLocales[locale] {
			return locale
		}
		for candidate := range supportedLocales {
			if strings.HasPrefix(candidate, locale+"_") {
				return candidate
			}
		}
	}
	return "en_US"
}

// ---- application roles ----

func (h *Handler) applicationRoleBean(role store.ApplicationRole) map[string]any {
	groups, details := []string{}, []map[string]any{}
	for _, g := range role.Groups {
		groups = append(groups, g.Name)
		details = append(details, h.groupNameBean(g))
	}
	return map[string]any{
		"key": role.Key, "name": role.Name, "groups": groups, "groupDetails": details,
		"defaultGroups": groups, "defaultGroupsDetails": details, "selectedByDefault": false,
		"defined": true, "hasUnlimitedSeats": true, "numberOfSeats": -1, "remainingSeats": -1,
		"userCount": role.UserCount, "userCountDescription": strconv.Itoa(role.UserCount) + " users", "platform": false,
	}
}

func (h *Handler) applicationRoles(w http.ResponseWriter, r *http.Request, key string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, _, e := h.authWorkspaceAdmin(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if key != "" {
		role, err := h.Store.ApplicationRole(r.Context(), workspaceID, key)
		if err != nil {
			jiraError(w, http.StatusNotFound, "The role is not found.")
			return
		}
		writeJSON(w, http.StatusOK, h.applicationRoleBean(role))
		return
	}
	roles, err := h.Store.ApplicationRoles(r.Context(), workspaceID)
	if err != nil {
		peopleError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(roles))
	for _, role := range roles {
		out = append(out, h.applicationRoleBean(role))
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- avatars ----

func (h *Handler) avatarBean(a store.Avatar, ownerWire string) map[string]any {
	id := strconv.FormatInt(a.ID, 10)
	view := h.BaseURL + "/rest/api/3/universal_avatar/view/type/" + a.OwnerType + "/avatar/" + id
	bean := map[string]any{
		"id": id, "isSystemAvatar": a.IsSystem, "isSelected": a.IsSelected, "isDeletable": !a.IsSystem,
		"urls": map[string]string{"16x16": view + "?size=xsmall", "24x24": view + "?size=small", "32x32": view + "?size=medium", "48x48": view},
	}
	if !a.IsSystem && ownerWire != "" {
		bean["owner"] = ownerWire
	}
	if a.FileName != "" {
		bean["fileName"] = filepath.Base(a.FileName)
	}
	return bean
}

// ownerWireID is the id clients use for an avatar's owner.
func (h *Handler) ownerWireID(r *http.Request, workspaceID, ownerType, ownerID string) string {
	switch ownerType {
	case "issuetype":
		if t, err := h.Store.IssueTypeInWorkspace(r.Context(), workspaceID, ownerID); err == nil {
			return jiraIDString(t.JiraID)
		}
	case "priority":
		if p, err := h.Store.PriorityInWorkspace(r.Context(), workspaceID, ownerID); err == nil {
			return jiraIDString(p.JiraID)
		}
	}
	return ownerID
}

func (h *Handler) systemAvatars(w http.ResponseWriter, r *http.Request, ownerType string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if _, _, e := h.authWorkspace(r); e != nil {
		writeJerr(w, e)
		return
	}
	avatars, err := store.SystemAvatars(ownerType)
	if err != nil {
		jiraError(w, http.StatusNotFound, "The avatar type is invalid.")
		return
	}
	out := make([]map[string]any, 0, len(avatars))
	for _, a := range avatars {
		out = append(out, h.avatarBean(a, ""))
	}
	writeJSON(w, http.StatusOK, map[string]any{"system": out})
}

func (h *Handler) universalAvatarRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	switch {
	// type/{type}/owner/{id}
	case len(parts) == 4 && parts[0] == "type" && parts[2] == "owner":
		h.ownerAvatars(w, r, parts[1], parts[3])
	// type/{type}/owner/{id}/avatar/{avatarId}
	case len(parts) == 6 && parts[0] == "type" && parts[2] == "owner" && parts[4] == "avatar" && r.Method == http.MethodDelete:
		workspaceID, _, e := h.authWorkspaceAdmin(r)
		if e != nil {
			writeJerr(w, e)
			return
		}
		id, err := strconv.ParseInt(parts[5], 10, 64)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "The avatar id is invalid.")
			return
		}
		if err := h.Store.DeleteAvatar(r.Context(), workspaceID, parts[1], parts[3], id); err != nil {
			peopleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	// view/type/{type}
	case len(parts) == 3 && parts[0] == "view" && parts[1] == "type":
		h.avatarImage(w, r, parts[2], "", "")
	// view/type/{type}/avatar/{id}
	case len(parts) == 5 && parts[0] == "view" && parts[1] == "type" && parts[3] == "avatar":
		h.avatarImage(w, r, parts[2], parts[4], "")
	// view/type/{type}/owner/{id}
	case len(parts) == 5 && parts[0] == "view" && parts[1] == "type" && parts[3] == "owner":
		h.avatarImage(w, r, parts[2], "", parts[4])
	default:
		jiraError(w, http.StatusNotFound, "No resource found for path /rest/api/3/universal_avatar/"+strings.Join(parts, "/"))
	}
}

func validUniversalAvatarType(ownerType string) bool {
	return ownerType == "project" || ownerType == "issuetype" || ownerType == "priority" || ownerType == "SD_REQTYPE"
}

func (h *Handler) ownerAvatars(w http.ResponseWriter, r *http.Request, ownerType, ownerID string) {
	if !validUniversalAvatarType(ownerType) {
		jiraError(w, http.StatusNotFound, "The avatar type is invalid.")
		return
	}
	switch r.Method {
	case http.MethodGet:
		workspaceID, _, e := h.authWorkspace(r)
		if e != nil {
			writeJerr(w, e)
			return
		}
		system, custom, err := h.Store.OwnerAvatars(r.Context(), workspaceID, ownerType, ownerID)
		if err != nil {
			peopleError(w, err)
			return
		}
		wire := h.ownerWireID(r, workspaceID, ownerType, ownerID)
		systemOut, customOut := []map[string]any{}, []map[string]any{}
		for _, a := range system {
			systemOut = append(systemOut, h.avatarBean(a, wire))
		}
		for _, a := range custom {
			customOut = append(customOut, h.avatarBean(a, wire))
		}
		writeJSON(w, http.StatusOK, map[string]any{"system": systemOut, "custom": customOut})
	case http.MethodPost:
		h.loadAvatar(w, r, ownerType, ownerID, false)
	default:
		methodNotAllowed(w)
	}
}

// loadAvatar stores an uploaded avatar for an owner. Jira requires the XSRF
// header and a JPEG, GIF or PNG body.
func (h *Handler) loadAvatar(w http.ResponseWriter, r *http.Request, ownerType, ownerID string, projectAdmin bool) {
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if projectAdmin {
		if allowed, err := h.Store.HasProjectPermission(r.Context(), workspaceID, actorID, ownerID, "", "ADMINISTER_PROJECTS"); err != nil || !allowed {
			if admin, adminErr := h.Store.IsAdmin(r.Context(), workspaceID, actorID); adminErr != nil || !admin {
				jiraError(w, http.StatusForbidden, "You do not have permission to administer the project.")
				return
			}
		}
	} else if admin, err := h.Store.IsAdmin(r.Context(), workspaceID, actorID); err != nil || !admin {
		jiraError(w, http.StatusForbidden, "You do not have the necessary permissions.")
		return
	}
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Atlassian-Token")), "no-check") {
		jiraError(w, http.StatusForbidden, "X-Atlassian-Token: no-check is required.")
		return
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil || len(body) == 0 {
		jiraError(w, http.StatusBadRequest, "An image is required.")
		return
	}
	avatar, err := h.Store.StoreAvatar(r.Context(), workspaceID, ownerType, ownerID, mediaType, body)
	if err != nil {
		peopleError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, h.avatarBean(avatar, h.ownerWireID(r, workspaceID, ownerType, ownerID)))
}

// avatarImage serves an avatar image: a system avatar from its icon, a custom
// one from the store, or the owner's selected avatar.
func (h *Handler) avatarImage(w http.ResponseWriter, r *http.Request, ownerType, avatarRef, ownerID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if !validUniversalAvatarType(ownerType) {
		jiraError(w, http.StatusNotFound, "The avatar type is invalid.")
		return
	}
	if size := r.URL.Query().Get("size"); size != "" {
		switch size {
		case "xsmall", "small", "medium", "large", "xlarge":
		default:
			jiraError(w, http.StatusNotFound, "No avatar matches the requested size.")
			return
		}
	}
	workspaceID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	var avatarID int64
	switch {
	case avatarRef != "":
		id, err := strconv.ParseInt(avatarRef, 10, 64)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "The avatar id is invalid.")
			return
		}
		avatarID = id
	case ownerID != "":
		id, err := h.Store.OwnerSelectedAvatar(r.Context(), workspaceID, ownerType, ownerID)
		if err != nil {
			peopleError(w, err)
			return
		}
		avatarID = id
	default:
		avatarID, _ = store.DefaultSystemAvatar(ownerType)
	}
	if icon, system := store.SystemAvatarIcon(ownerType, avatarID); system {
		// System icons come from a fixed catalogue; fs.ValidPath keeps the
		// read inside the static directory all the same.
		name := path.Join("img", icon)
		data, err := fs.ReadFile(os.DirFS(h.staticRoot()), name)
		if !fs.ValidPath(name) || err != nil {
			jiraError(w, http.StatusNotFound, "The avatar image was not found.")
			return
		}
		serveAvatarImage(w, r, "image/svg+xml", icon, data)
		return
	}
	mediaType, data, err := h.Store.AvatarImage(r.Context(), workspaceID, ownerType, avatarID)
	if err != nil {
		peopleError(w, err)
		return
	}
	serveAvatarImage(w, r, mediaType, "avatar-"+strconv.FormatInt(avatarID, 10), data)
}

// serveAvatarImage answers with image bytes of a type the store accepted, never
// sniffed into anything else.
func serveAvatarImage(w http.ResponseWriter, r *http.Request, mediaType, name string, data []byte) {
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

// staticRoot is where the server's static assets live: the directory the
// server was started with, or the repository's when a test builds a handler.
func (h *Handler) staticRoot() string {
	if h.StaticDir != "" {
		return h.StaticDir
	}
	for _, candidate := range []string{"web/static", "../../web/static"} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return "web/static"
}

func (h *Handler) projectAvatarRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) < 2 {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	project := parts[0]
	switch {
	case len(parts) == 2 && parts[1] == "avatars" && r.Method == http.MethodGet:
		h.ownerAvatars(w, r, "project", project)
	case len(parts) == 2 && parts[1] == "avatar2" && r.Method == http.MethodPost:
		h.loadAvatar(w, r, "project", project, true)
	case len(parts) == 2 && parts[1] == "avatar" && r.Method == http.MethodPut:
		workspaceID, actorID, e := h.authWorkspace(r)
		if e != nil {
			writeJerr(w, e)
			return
		}
		if !h.canAdministerProject(r, workspaceID, actorID, project) {
			jiraError(w, http.StatusForbidden, "You do not have permission to administer the project.")
			return
		}
		var request struct {
			ID             string            `json:"id"`
			FileName       string            `json:"fileName"`
			IsDeletable    bool              `json:"isDeletable"`
			IsSelected     bool              `json:"isSelected"`
			IsSystemAvatar bool              `json:"isSystemAvatar"`
			Owner          string            `json:"owner"`
			URLs           map[string]string `json:"urls"`
		}
		if !decodeMetadataRequest(w, r, &request) {
			return
		}
		id, err := strconv.ParseInt(strings.TrimSpace(request.ID), 10, 64)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "The avatar id is required.")
			return
		}
		if err := h.Store.SelectAvatar(r.Context(), workspaceID, "project", project, id); err != nil {
			peopleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case len(parts) == 3 && parts[1] == "avatar" && r.Method == http.MethodDelete:
		workspaceID, actorID, e := h.authWorkspace(r)
		if e != nil {
			writeJerr(w, e)
			return
		}
		if !h.canAdministerProject(r, workspaceID, actorID, project) {
			jiraError(w, http.StatusForbidden, "You do not have permission to administer the project.")
			return
		}
		id, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			jiraError(w, http.StatusNotFound, "The avatar was not found.")
			return
		}
		if err := h.Store.DeleteAvatar(r.Context(), workspaceID, "project", project, id); err != nil {
			peopleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusNotFound, "No resource found for path /rest/api/3/project/"+strings.Join(parts, "/"))
	}
}

func (h *Handler) canAdministerProject(r *http.Request, workspaceID, actorID, project string) bool {
	if allowed, err := h.Store.HasProjectPermission(r.Context(), workspaceID, actorID, project, "", "ADMINISTER_PROJECTS"); err == nil && allowed {
		return true
	}
	admin, err := h.Store.IsAdmin(r.Context(), workspaceID, actorID)
	return err == nil && admin
}
