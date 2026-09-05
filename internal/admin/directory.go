package admin

import (
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

type directorySort struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

type groupSearchRequest struct {
	Cursor         string          `json:"cursor"`
	Limit          int             `json:"limit"`
	SortBy         []directorySort `json:"sortBy"`
	AccountIDs     []string        `json:"accountIds"`
	DirectoryIDs   []string        `json:"directoryIds"`
	RoleIDs        []string        `json:"roleIds"`
	ResourceOwners []string        `json:"resourceOwners"`
	ResourceIDs    []string        `json:"resourceIds"`
	SearchTerm     string          `json:"searchTerm"`
	GroupIDs       []string        `json:"groupIds"`
	GroupNames     []string        `json:"groupNames"`
	Expand         []string        `json:"expand"`
}

type userSearchRequest struct {
	Cursor           string          `json:"cursor"`
	Limit            int             `json:"limit"`
	AccountIDs       []string        `json:"accountIds"`
	DirectoryIDs     []string        `json:"directoryIds"`
	ResourceIDs      []string        `json:"resourceIds"`
	GroupIDs         []string        `json:"groupIds"`
	MFAEnabled       *bool           `json:"mfaEnabled"`
	ClaimStatus      string          `json:"claimStatus"`
	Status           []string        `json:"status"`
	AccountStatus    []string        `json:"accountStatus"`
	MembershipStatus []string        `json:"membershipStatus"`
	RoleIDs          []string        `json:"roleIds"`
	EmailDomains     []string        `json:"emailDomains"`
	SearchTerm       string          `json:"searchTerm"`
	Emails           []string        `json:"emails"`
	Expand           []string        `json:"expand"`
	SortBy           []directorySort `json:"sortBy"`
}

type groupSearchResult struct {
	group         *models.Group
	resourceCount int
}

func includes(items []string, candidate string) bool {
	if len(items) == 0 {
		return true
	}
	for _, item := range items {
		if item == candidate {
			return true
		}
	}
	return false
}

func validatesList(items []string, minimum, maximum int) bool {
	if len(items) == 0 {
		return minimum == 0
	}
	if len(items) < minimum || len(items) > maximum {
		return false
	}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if strings.TrimSpace(item) == "" || seen[item] {
			return false
		}
		seen[item] = true
	}
	return true
}

func searchPage(cursor string, limit, defaultLimit int) (int, int, error) {
	if limit == 0 {
		limit = defaultLimit
	}
	values := url.Values{"limit": []string{strconv.Itoa(limit)}}
	if cursor != "" {
		values.Set("cursor", cursor)
	}
	return parsePage(values)
}

func (h *Handler) directoryScope(w http.ResponseWriter, r *http.Request, workspaceID string) (roleContext, []string, bool) {
	context, ok := h.loadRoleContext(w, r, workspaceID)
	if !ok {
		return roleContext{}, nil, false
	}
	directoryID := r.PathValue("directoryId")
	if directoryID != "-" {
		if !context.DirectoryIDs[directoryID] {
			failure(w, http.StatusNotFound, "Directory was not found.")
			return roleContext{}, nil, false
		}
		return context, []string{directoryID}, true
	}
	ids := make([]string, 0, len(context.DirectoryIDs))
	for id := range context.DirectoryIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return context, ids, true
}

func groupResourceCount(ctx roleContext, bindings []*models.RoleBinding) int {
	resources := map[string]bool{}
	for _, binding := range bindings {
		resourceID, _, ok := roleResource(ctx, binding)
		if !ok {
			continue
		}
		resources[resourceID] = true
	}
	return len(resources)
}

func (h *Handler) groupModel(ctx roleContext, group *models.Group, resourceCount int, includeUsers, includeResources bool) map[string]any {
	self := strings.TrimRight(h.BaseURL, "/") + "/admin/v2/orgs/" + ctx.Organization.ID + "/directories/" + group.DirectoryID + "/groups/" + group.ID
	model := map[string]any{
		"id": group.ID, "name": group.Name, "description": group.Description,
		"directoryId": group.DirectoryID, "externalSynced": false, "managedBy": "admins",
		"managementAccess": map[string]bool{"deletable": true, "modifiable": true, "readable": true},
		"links":            map[string]string{"self": self},
	}
	if includeUsers || includeResources {
		counts := map[string]int{}
		if includeUsers {
			counts["users"] = group.MemberCount
		}
		if includeResources {
			counts["resources"] = resourceCount
		}
		model["counts"] = counts
	}
	return model
}

func (h *Handler) GroupDetails(w http.ResponseWriter, r *http.Request) {
	actorID, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := rejectUnknownQuery(r.URL.Query()); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	context, directoryIDs, ok := h.directoryScope(w, r, workspaceID)
	if !ok {
		return
	}
	var group *models.Group
	for _, directoryID := range directoryIDs {
		candidate, err := h.Store.DirectoryGroup(r.Context(), directoryID, r.PathValue("groupId"))
		if err == nil {
			group = candidate
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			failure(w, http.StatusInternalServerError, "Group lookup failed.")
			return
		}
	}
	if group == nil {
		failure(w, http.StatusNotFound, "Group was not found.")
		return
	}
	if r.Method == http.MethodDelete {
		if err := h.Store.DeleteDirectoryGroup(r.Context(), workspaceID, actorID, group.DirectoryID, group.ID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				failure(w, http.StatusNotFound, "Group was not found.")
			} else {
				failure(w, http.StatusInternalServerError, "Group deletion failed.")
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	bindings, err := h.Store.RoleBindingsForPrincipal(r.Context(), workspaceID, "group", group.ID)
	if err != nil {
		failure(w, http.StatusInternalServerError, "Group role lookup failed.")
		return
	}
	resourceCount := groupResourceCount(context, bindings)
	writeJSON(w, http.StatusOK, map[string]any{"data": h.groupModel(context, group, resourceCount, true, true)})
}

func (h *Handler) GroupCount(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	context, directoryIDs, ok := h.directoryScope(w, r, workspaceID)
	if !ok {
		return
	}
	if err := rejectUnknownQuery(r.URL.Query(), "directoryIds", "accountIds", "groupIds", "resourceOwners", "resourceIds", "searchTerm", "roleIds"); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	input := groupSearchRequest{
		DirectoryIDs: queryItems(r, "directoryIds"), AccountIDs: queryItems(r, "accountIds"),
		GroupIDs: queryItems(r, "groupIds"), ResourceOwners: queryItems(r, "resourceOwners"),
		ResourceIDs: queryItems(r, "resourceIds"), SearchTerm: r.URL.Query().Get("searchTerm"), RoleIDs: queryItems(r, "roleIds"),
	}
	if !validatesList(input.DirectoryIDs, 0, 10) || !validatesList(input.AccountIDs, 0, 10) ||
		!validatesList(input.GroupIDs, 0, 10) || !validatesList(input.ResourceOwners, 0, 10) ||
		!validatesList(input.ResourceIDs, 0, 20) || !validatesList(input.RoleIDs, 0, 10) {
		failure(w, http.StatusBadRequest, "Group count filters exceed their supported limits or contain duplicates.")
		return
	}
	selected, valid := selectDirectories(context, directoryIDs, input.DirectoryIDs)
	if !valid {
		failure(w, http.StatusBadRequest, "directoryIds contains a directory outside this organization.")
		return
	}
	results, err := h.matchingGroups(r, workspaceID, context, selected, input)
	if err != nil {
		failure(w, http.StatusInternalServerError, "Group count failed.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": len(results)})
}

func queryItems(r *http.Request, name string) []string {
	items := make([]string, 0)
	for _, value := range r.URL.Query()[name] {
		for _, item := range strings.Split(value, ",") {
			items = append(items, strings.TrimSpace(item))
		}
	}
	return items
}

func (h *Handler) GroupStats(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := rejectUnknownQuery(r.URL.Query()); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	_, directoryIDs, ok := h.directoryScope(w, r, workspaceID)
	if !ok {
		return
	}
	count := 0
	for _, directoryID := range directoryIDs {
		groups, err := h.Store.GroupsByDirectory(r.Context(), directoryID)
		if err != nil {
			failure(w, http.StatusInternalServerError, "Group statistics failed.")
			return
		}
		count += len(groups)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"types":  []map[string]any{{"type": "TEAM", "count": 0}, {"type": "GROUP", "count": count}, {"type": "USERBASE_GROUP", "count": 0}},
		"totals": map[string]int{"all": count, "synced": 0, "managed": 0},
	})
}

func (h *Handler) SearchGroups(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := rejectUnknownQuery(r.URL.Query()); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	context, directoryIDs, ok := h.directoryScope(w, r, workspaceID)
	if !ok {
		return
	}
	var input groupSearchRequest
	if r.ContentLength != 0 {
		if err := decodeJSONBody(r, &input); err != nil {
			failure(w, http.StatusBadRequest, "Group search body is invalid.")
			return
		}
	}
	if input.SearchTerm != "" && len(input.GroupNames) > 0 || len(input.GroupIDs) > 0 && len(input.GroupNames) > 0 {
		failure(w, http.StatusBadRequest, "groupNames cannot be combined with searchTerm or groupIds.")
		return
	}
	if !validatesList(input.AccountIDs, 0, 10) || !validatesList(input.DirectoryIDs, 0, 10) ||
		!validatesList(input.RoleIDs, 0, 10) || !validatesList(input.ResourceOwners, 0, 10) ||
		!validatesList(input.ResourceIDs, 0, 20) || !validatesList(input.GroupIDs, 0, 10) ||
		!validatesList(input.GroupNames, 0, 100) || !validatesList(input.Expand, 0, 2) || len(input.SortBy) > 1 {
		failure(w, http.StatusBadRequest, "Group search fields exceed their supported limits or contain duplicates.")
		return
	}
	for _, field := range input.Expand {
		if field != "counts.resources" && field != "counts.users" {
			failure(w, http.StatusBadRequest, "expand contains an unsupported field.")
			return
		}
	}
	if len(input.SortBy) == 1 && (input.SortBy[0].Field != "name" || input.SortBy[0].Direction != "asc" && input.SortBy[0].Direction != "desc") {
		failure(w, http.StatusBadRequest, "sortBy supports name in asc or desc direction.")
		return
	}
	selectedDirectories, valid := selectDirectories(context, directoryIDs, input.DirectoryIDs)
	if !valid {
		failure(w, http.StatusBadRequest, "directoryIds contains a directory outside this organization.")
		return
	}
	results, err := h.matchingGroups(r, workspaceID, context, selectedDirectories, input)
	if err != nil {
		failure(w, http.StatusInternalServerError, "Group search failed.")
		return
	}
	sort.Slice(results, func(i, j int) bool {
		left, right := strings.ToLower(results[i].group.Name), strings.ToLower(results[j].group.Name)
		if len(input.SortBy) == 1 && input.SortBy[0].Direction == "desc" {
			return left > right
		}
		return left < right
	})
	offset, limit, err := searchPage(input.Cursor, input.Limit, 20)
	if err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	page, next := pageSlice(results, offset, limit)
	data := make([]map[string]any, 0, len(page))
	for _, item := range page {
		data = append(data, h.groupModel(context, item.group, item.resourceCount,
			includesExact(input.Expand, "counts.users"), includesExact(input.Expand, "counts.resources")))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "links": map[string]string{"self": cursorFor(offset), "next": next}})
}

func selectDirectories(context roleContext, available, requested []string) (map[string]bool, bool) {
	selected := map[string]bool{}
	for _, id := range available {
		if includes(requested, id) {
			selected[id] = true
		}
	}
	for _, id := range requested {
		if !context.DirectoryIDs[id] {
			return nil, false
		}
	}
	return selected, true
}

func (h *Handler) matchingGroups(r *http.Request, workspaceID string, context roleContext, directories map[string]bool, input groupSearchRequest) ([]groupSearchResult, error) {
	results := make([]groupSearchResult, 0)
	for directoryID := range directories {
		groups, err := h.Store.GroupsByDirectory(r.Context(), directoryID)
		if err != nil {
			return nil, err
		}
		for _, group := range groups {
			if !includes(input.GroupIDs, group.ID) || input.SearchTerm != "" && !strings.Contains(strings.ToLower(group.Name), strings.ToLower(input.SearchTerm)) {
				continue
			}
			if len(input.GroupNames) > 0 {
				matched := false
				for _, name := range input.GroupNames {
					matched = matched || strings.EqualFold(name, group.Name)
				}
				if !matched {
					continue
				}
			}
			if len(input.AccountIDs) > 0 {
				members, err := h.Store.GroupMemberIDs(r.Context(), group.ID)
				if err != nil {
					return nil, err
				}
				matched := false
				for _, member := range members {
					matched = matched || includes(input.AccountIDs, member)
				}
				if !matched {
					continue
				}
			}
			bindings, err := h.Store.RoleBindingsForPrincipal(r.Context(), workspaceID, "group", group.ID)
			if err != nil {
				return nil, err
			}
			resourceCount := groupResourceCount(context, bindings)
			if !bindingsMatch(context, bindings, input.ResourceIDs, input.ResourceOwners, input.RoleIDs) {
				continue
			}
			results = append(results, groupSearchResult{group: group, resourceCount: resourceCount})
		}
	}
	return results, nil
}

func bindingsMatch(context roleContext, bindings []*models.RoleBinding, resourceIDs, resourceOwners, roleIDs []string) bool {
	if len(resourceIDs) == 0 && len(resourceOwners) == 0 && len(roleIDs) == 0 {
		return true
	}
	for _, binding := range bindings {
		resourceID, owner, found := roleResource(context, binding)
		if found && includes(resourceIDs, resourceID) && includes(resourceOwners, owner) && includes(roleIDs, apiRole(binding.RoleKey)) {
			return true
		}
	}
	return false
}

func userState(user *models.User) (status, accountStatus, membershipStatus string) {
	if user.Active {
		return "active", "active", "active"
	}
	return "deactivated", "inactive", "suspended"
}

func (h *Handler) SearchUsers(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := rejectUnknownQuery(r.URL.Query()); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	context, directoryIDs, ok := h.directoryScope(w, r, workspaceID)
	if !ok {
		return
	}
	var input userSearchRequest
	if r.ContentLength != 0 {
		if err := decodeJSONBody(r, &input); err != nil {
			failure(w, http.StatusBadRequest, "User search body is invalid.")
			return
		}
	}
	if input.SearchTerm != "" && len(input.Emails) > 0 {
		failure(w, http.StatusBadRequest, "emails cannot be combined with searchTerm.")
		return
	}
	if !validUserSearch(input) {
		failure(w, http.StatusBadRequest, "User search fields exceed their supported limits, contain duplicates, or use unsupported values.")
		return
	}
	selectedDirectories, valid := selectDirectories(context, directoryIDs, input.DirectoryIDs)
	if !valid {
		failure(w, http.StatusBadRequest, "directoryIds contains a directory outside this organization.")
		return
	}
	groupsByUser := map[string][]map[string]any{}
	for directoryID := range selectedDirectories {
		groups, err := h.Store.GroupsByDirectory(r.Context(), directoryID)
		if err != nil {
			failure(w, http.StatusInternalServerError, "User group search failed.")
			return
		}
		for _, group := range groups {
			members, err := h.Store.GroupMemberIDs(r.Context(), group.ID)
			if err != nil {
				failure(w, http.StatusInternalServerError, "User group search failed.")
				return
			}
			for _, accountID := range members {
				groupsByUser[accountID] = append(groupsByUser[accountID], map[string]any{"id": group.ID, "name": group.Name, "description": group.Description})
			}
		}
	}
	usersByID := map[string]*models.User{}
	for directoryID := range selectedDirectories {
		users, err := h.Store.DirectoryUsers(r.Context(), directoryID)
		if err != nil {
			failure(w, http.StatusInternalServerError, "User search failed.")
			return
		}
		for _, user := range users {
			usersByID[user.ID] = user
		}
	}
	users := make([]*models.User, 0, len(usersByID))
	modelsByID := map[string]map[string]any{}
	for _, user := range usersByID {
		status, accountStatus, membershipStatus := userState(user)
		if !includes(input.AccountIDs, user.ID) || !includes(input.Status, status) || !includes(input.AccountStatus, accountStatus) || !includes(input.MembershipStatus, membershipStatus) {
			continue
		}
		if input.MFAEnabled != nil && *input.MFAEnabled || input.ClaimStatus == "unmanaged" {
			continue
		}
		if input.SearchTerm != "" && !strings.Contains(strings.ToLower(user.DisplayName+" "+user.Email), strings.ToLower(input.SearchTerm)) || !matchesEmail(user.Email, input.Emails, input.EmailDomains) {
			continue
		}
		if len(input.GroupIDs) > 0 {
			matched := false
			for _, group := range groupsByUser[user.ID] {
				matched = matched || includes(input.GroupIDs, group["id"].(string))
			}
			if !matched {
				continue
			}
		}
		bindings, err := h.Store.RoleBindingsForPrincipal(r.Context(), workspaceID, "user", user.ID)
		if err != nil {
			failure(w, http.StatusInternalServerError, "User role search failed.")
			return
		}
		resources, platformRoles := map[string]bool{}, []string{}
		productAccess := map[string]map[string]any{}
		for _, binding := range bindings {
			resourceID, owner, found := roleResource(context, binding)
			if !found {
				continue
			}
			resources[resourceID] = true
			if owner != "platform" {
				productAccess[resourceID] = map[string]any{"key": owner, "id": resourceID}
			}
			role := apiRole(binding.RoleKey)
			if role == "atlassian/org-admin" || role == "atlassian/site-admin" || role == "atlassian/user-access-admin" || role == "atlassian/ai-access" {
				platformRoles = append(platformRoles, role)
			}
		}
		if !bindingsMatch(context, bindings, input.ResourceIDs, nil, input.RoleIDs) {
			continue
		}
		model := multiDirectoryUser(user)
		if includesExact(input.Expand, "counts.resources") {
			model["counts"] = map[string]int{"resources": len(resources)}
		}
		if includesExact(input.Expand, "platformRoles") {
			sort.Strings(platformRoles)
			model["platformRoles"] = uniqueStrings(platformRoles)
		}
		if includesExact(input.Expand, "groups") {
			model["groups"] = groupsByUser[user.ID]
		}
		if includesExact(input.Expand, "productAccess") {
			ids := make([]string, 0, len(productAccess))
			for id := range productAccess {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			items := make([]map[string]any, 0, len(ids))
			for _, id := range ids {
				items = append(items, productAccess[id])
			}
			model["productAccess"] = items
		}
		users = append(users, user)
		modelsByID[user.ID] = model
	}
	desc := len(input.SortBy) == 1 && input.SortBy[0].Direction == "desc"
	sort.Slice(users, func(i, j int) bool {
		left, right := strings.ToLower(users[i].DisplayName), strings.ToLower(users[j].DisplayName)
		if desc {
			return left > right
		}
		return left < right
	})
	offset, limit, err := searchPage(input.Cursor, input.Limit, 50)
	if err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	page, next := pageSlice(users, offset, limit)
	data := make([]map[string]any, 0, len(page))
	for _, user := range page {
		data = append(data, modelsByID[user.ID])
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "links": map[string]string{"self": cursorFor(offset), "next": next}})
}

func includesExact(items []string, candidate string) bool {
	for _, item := range items {
		if item == candidate {
			return true
		}
	}
	return false
}

func uniqueStrings(items []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}

func matchesEmail(email string, emails, domains []string) bool {
	if len(emails) > 0 {
		matched := false
		for _, candidate := range emails {
			matched = matched || strings.EqualFold(candidate, email)
		}
		if !matched {
			return false
		}
	}
	if len(domains) > 0 {
		parts := strings.SplitN(email, "@", 2)
		if len(parts) != 2 {
			return false
		}
		matched := false
		for _, domain := range domains {
			matched = matched || strings.EqualFold(strings.TrimPrefix(domain, "@"), parts[1])
		}
		return matched
	}
	return true
}

func validUserSearch(input userSearchRequest) bool {
	lists := []struct {
		items []string
		max   int
	}{
		{input.AccountIDs, 10}, {input.DirectoryIDs, 10}, {input.ResourceIDs, 20}, {input.GroupIDs, 10},
		{input.Status, 4}, {input.AccountStatus, 3}, {input.MembershipStatus, 3}, {input.RoleIDs, 10},
		{input.EmailDomains, 10}, {input.Emails, 100}, {input.Expand, 4},
	}
	for _, list := range lists {
		if !validatesList(list.items, 0, list.max) {
			return false
		}
	}
	if input.ClaimStatus != "" && input.ClaimStatus != "managed" && input.ClaimStatus != "unmanaged" || len(input.SortBy) > 1 {
		return false
	}
	if len(input.SortBy) == 1 && (input.SortBy[0].Field != "nick_name" || input.SortBy[0].Direction != "asc" && input.SortBy[0].Direction != "desc") {
		return false
	}
	for _, value := range input.Status {
		if !includesExact([]string{"active", "suspended", "not_invited", "deactivated", "for_deletion"}, value) {
			return false
		}
	}
	for _, value := range input.AccountStatus {
		if !includesExact([]string{"active", "inactive", "closed"}, value) {
			return false
		}
	}
	for _, value := range input.MembershipStatus {
		if !includesExact([]string{"active", "suspended", "no_membership"}, value) {
			return false
		}
	}
	allowedRoles := []string{
		"atlassian/user", "atlassian/admin", "atlassian/guest", "atlassian/customer",
		"atlassian/user-access-admin", "atlassian/contributor", "atlassian/basic",
		"atlassian/stakeholder", "atlassian/org-admin", "atlassian/site-admin", "atlassian/ai-access",
	}
	for _, value := range input.RoleIDs {
		if !includesExact(allowedRoles, value) {
			return false
		}
	}
	for _, value := range input.Expand {
		if !includesExact([]string{"platformRoles", "counts.resources", "productAccess", "groups"}, value) {
			return false
		}
	}
	return true
}

func (h *Handler) UserStats(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := rejectUnknownQuery(r.URL.Query()); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	context, directoryIDs, ok := h.directoryScope(w, r, workspaceID)
	if !ok {
		return
	}
	usersByID := map[string]*models.User{}
	for _, directoryID := range directoryIDs {
		users, err := h.Store.DirectoryUsers(r.Context(), directoryID)
		if err != nil {
			failure(w, http.StatusInternalServerError, "User statistics failed.")
			return
		}
		for _, user := range users {
			usersByID[user.ID] = user
		}
	}
	roleCounts := map[string]int{}
	statusCounts := map[string]int{"active": 0, "inactive": 0, "closed": 0}
	for _, user := range usersByID {
		_, accountStatus, _ := userState(user)
		statusCounts[accountStatus]++
		bindings, err := h.Store.RoleBindingsForPrincipal(r.Context(), workspaceID, "user", user.ID)
		if err != nil {
			failure(w, http.StatusInternalServerError, "User role statistics failed.")
			return
		}
		roles := map[string]bool{}
		for _, binding := range bindings {
			if _, _, found := roleResource(context, binding); found {
				roles[apiRole(binding.RoleKey)] = true
			}
		}
		for role := range roles {
			roleCounts[role]++
		}
	}
	roleNames := make([]string, 0, len(roleCounts))
	for role := range roleCounts {
		roleNames = append(roleNames, role)
	}
	sort.Strings(roleNames)
	roles := make([]map[string]any, 0, len(roleNames))
	for _, role := range roleNames {
		roles = append(roles, map[string]any{"roleId": role, "count": roleCounts[role]})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"roles":         roles,
		"accountStatus": []map[string]any{{"status": "active", "count": statusCounts["active"]}, {"status": "inactive", "count": statusCounts["inactive"]}, {"status": "closed", "count": 0}},
	})
}
