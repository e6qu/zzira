package api3

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

type filterPermissionRequest struct {
	Type          string `json:"type"`
	Rights        int    `json:"rights"`
	AccountID     string `json:"accountId"`
	GroupID       string `json:"groupId"`
	GroupName     string `json:"groupname"`
	ProjectID     string `json:"projectId"`
	ProjectRoleID string `json:"projectRoleId"`
}

type filterRequest struct {
	Name             string                     `json:"name"`
	JQL              string                     `json:"jql"`
	Description      string                     `json:"description"`
	Favourite        *bool                      `json:"favourite"`
	SharePermissions *[]filterPermissionRequest `json:"sharePermissions"`
	EditPermissions  *[]filterPermissionRequest `json:"editPermissions"`
}

func filterPermissionInput(request filterPermissionRequest, rights int) store.FilterPermissionInput {
	if request.Rights != 0 {
		rights = request.Rights
	}
	return store.FilterPermissionInput{
		Type: request.Type, Rights: rights, AccountID: request.AccountID,
		GroupID: request.GroupID, GroupName: request.GroupName,
		ProjectID: request.ProjectID, ProjectRoleID: request.ProjectRoleID,
	}
}

func filterDetails(request filterRequest) store.FilterDetails {
	details := store.FilterDetails{Name: request.Name, JQL: request.JQL, Description: request.Description}
	if request.Favourite != nil {
		details.Favourite = *request.Favourite
	}
	details.SharesProvided = request.SharePermissions != nil || request.EditPermissions != nil
	if request.SharePermissions != nil {
		for _, permission := range *request.SharePermissions {
			details.SharePermissions = append(details.SharePermissions, filterPermissionInput(permission, 1))
		}
	}
	if request.EditPermissions != nil {
		for _, permission := range *request.EditPermissions {
			details.SharePermissions = append(details.SharePermissions, filterPermissionInput(permission, 2))
		}
	}
	return details
}

func (h *Handler) filterPermissionBean(permission models.FilterSharePermission) map[string]any {
	typeName := permission.Type
	if typeName == "authenticated" {
		typeName = "loggedin"
	}
	bean := map[string]any{"id": permission.ID, "type": typeName}
	switch permission.Type {
	case "user":
		bean["user"] = map[string]any{
			"accountId": permission.AccountID, "displayName": permission.AccountName,
			"active": true, "accountType": "atlassian",
		}
	case "group":
		bean["group"] = map[string]any{
			"groupId": permission.GroupID, "name": permission.GroupName,
			"self": h.BaseURL + "/rest/api/3/group?groupId=" + url.QueryEscape(permission.GroupID),
		}
	case "project", "projectRole":
		bean["project"] = map[string]any{
			"id": permission.ProjectID, "key": permission.ProjectKey,
			"name": permission.ProjectName,
			"self": h.BaseURL + "/rest/api/3/project/" + url.PathEscape(permission.ProjectID),
		}
		if permission.Type == "projectRole" {
			roleID, _ := strconv.ParseInt(permission.ProjectRoleID, 10, 64)
			bean["role"] = map[string]any{
				"id": roleID, "name": permission.ProjectRole,
				"self": h.BaseURL + "/rest/api/3/role/" + url.PathEscape(permission.ProjectRoleID),
			}
			bean["type"] = "project"
		}
	}
	return bean
}

func (h *Handler) filterBean(filter *models.Filter) map[string]any {
	view, edit := []map[string]any{}, []map[string]any{}
	for _, permission := range filter.SharePermissions {
		if permission.Rights == 2 {
			edit = append(edit, h.filterPermissionBean(permission))
		} else {
			view = append(view, h.filterPermissionBean(permission))
		}
	}
	owner := map[string]any{}
	if filter.OwnerID != "" {
		owner = map[string]any{
			"accountId": filter.OwnerID, "displayName": filter.OwnerName,
			"active": true, "accountType": "atlassian",
		}
	}
	self := h.BaseURL + "/rest/api/3/filter/" + url.PathEscape(filter.ID)
	subscriptions := make([]map[string]any, 0, len(filter.Subscriptions))
	for _, subscription := range filter.Subscriptions {
		item := map[string]any{
			"id": subscription.ID, "cronExpression": subscription.CronExpression,
			"enabled": subscription.Enabled, "nextRunAt": subscription.NextRunAt,
			"recipients": subscription.Recipients,
		}
		if subscription.LastRunAt != "" {
			item["lastRunAt"] = subscription.LastRunAt
		}
		if subscription.LastResultCount != nil {
			item["lastResultCount"] = *subscription.LastResultCount
		}
		if subscription.LastError != "" {
			item["lastError"] = subscription.LastError
		}
		subscriptions = append(subscriptions, item)
	}
	bean := map[string]any{
		"id": filter.ID, "name": filter.Name, "self": self,
		"jql": filter.JQL, "description": filter.Description, "owner": owner,
		"favourite": filter.Favourite, "favouritedCount": filter.FavouritedCount,
		"sharePermissions": view, "editPermissions": edit,
		"viewUrl":   h.BaseURL + "/issues/?filter=" + url.QueryEscape(filter.ID),
		"searchUrl": h.BaseURL + "/rest/api/3/search?jql=" + url.QueryEscape(filter.JQL),
		"subscriptions": map[string]any{
			"size": len(subscriptions), "items": subscriptions, "start-index": 0,
			"end-index": len(subscriptions), "max-results": len(subscriptions),
		},
	}
	if filter.ApproximateLastUsed != "" {
		bean["approximateLastUsed"] = filter.ApproximateLastUsed
	} else {
		bean["approximateLastUsed"] = nil
	}
	return bean
}

func decodeFilterRequest(w http.ResponseWriter, r *http.Request) (filterRequest, bool) {
	request := filterRequest{}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		jiraError(w, http.StatusBadRequest, "The filter request is invalid.")
		return request, false
	}
	if strings.TrimSpace(request.Name) == "" {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"name": "A filter name is required."})
		return request, false
	}
	return request, true
}

func writeFilterMutationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrFilterValidation):
		jiraError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), store.ErrFilterValidation.Error()+": "))
	case errors.Is(err, store.ErrFilterPermission):
		jiraError(w, http.StatusForbidden, "You do not have permission to change this filter.")
	case errors.Is(err, pgx.ErrNoRows):
		jiraError(w, http.StatusNotFound, "Filter does not exist.")
	default:
		jiraError(w, http.StatusInternalServerError, "Could not save the filter.")
	}
}

func (h *Handler) createFilter(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	request, ok := decodeFilterRequest(w, r)
	if !ok {
		return
	}
	if _, queryErr := h.compileJQL(r.Context(), workspaceID, request.JQL, userID); queryErr != nil {
		writeJerr(w, queryErr)
		return
	}
	filter, err := h.Store.CreateManagedFilter(r.Context(), store.NewID("flt"), workspaceID, userID, filterDetails(request))
	if err != nil {
		writeFilterMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.filterBean(filter))
}

func (h *Handler) putFilter(w http.ResponseWriter, r *http.Request, id string) {
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	request, ok := decodeFilterRequest(w, r)
	if !ok {
		return
	}
	if _, queryErr := h.compileJQL(r.Context(), workspaceID, request.JQL, userID); queryErr != nil {
		writeJerr(w, queryErr)
		return
	}
	filter, err := h.Store.UpdateManagedFilter(r.Context(), workspaceID, userID, id, filterDetails(request))
	if err != nil {
		writeFilterMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.filterBean(filter))
}

func (h *Handler) getFilter(w http.ResponseWriter, r *http.Request, id string) {
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	filter, err := h.Store.FilterByID(r.Context(), workspaceID, userID, id)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "Filter does not exist or you do not have permission to view it.")
		return
	}
	writeJSON(w, http.StatusOK, h.filterBean(filter))
}

func (h *Handler) deleteFilter(w http.ResponseWriter, r *http.Request, id string) {
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if err := h.Store.DeleteFilter(r.Context(), workspaceID, userID, id); err != nil {
		jiraError(w, http.StatusBadRequest, "Filter does not exist or you do not have permission to delete it.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listFilters(w http.ResponseWriter, r *http.Request) {
	h.filterCollection(w, r, "visible")
}

func (h *Handler) filterCollection(w http.ResponseWriter, r *http.Request, collection string) {
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	filters, err := h.Store.Filters(r.Context(), workspaceID, userID, store.FilterSearch{OrderBy: "name"})
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load filters.")
		return
	}
	includeFavourites := r.URL.Query().Get("includeFavourites") == "true"
	beans := []map[string]any{}
	for _, filter := range filters {
		include := collection == "visible" ||
			collection == "favourite" && filter.Favourite ||
			collection == "my" && (filter.OwnerID == userID || includeFavourites && filter.Favourite)
		if include {
			beans = append(beans, h.filterBean(filter))
		}
	}
	writeJSON(w, http.StatusOK, beans)
}

func (h *Handler) searchFilters(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	query := r.URL.Query()
	owner, accountID := query.Get("owner"), query.Get("accountId")
	if owner != "" && accountID != "" {
		jiraError(w, http.StatusBadRequest, "owner and accountId cannot be used together.")
		return
	}
	if owner != "" {
		accountID = owner
	}
	orderMap := map[string]string{
		"": "name", "name": "name", "-name": "-name", "id": "id", "-id": "-id",
		"owner": "owner", "-owner": "-owner", "favourite_count": "favouriteCount",
		"-favourite_count": "-favouriteCount", "is_favourite": "favourite", "-is_favourite": "-favourite",
	}
	order, valid := orderMap[query.Get("orderBy")]
	if !valid {
		jiraError(w, http.StatusBadRequest, "orderBy is invalid.")
		return
	}
	ids := map[string]bool{}
	for _, raw := range query["id"] {
		for _, id := range strings.Split(raw, ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids[id] = true
			}
		}
	}
	if len(ids) > 200 {
		jiraError(w, http.StatusBadRequest, "No more than 200 filter IDs can be searched.")
		return
	}
	override := query.Get("overrideSharePermissions") == "true"
	if override {
		admin, err := h.Store.IsAdmin(r.Context(), workspaceID, userID)
		if err != nil || !admin {
			jiraError(w, http.StatusForbidden, "Administer Jira permission is required to override shares.")
			return
		}
	}
	filters, err := h.Store.Filters(r.Context(), workspaceID, userID, store.FilterSearch{
		Name: query.Get("filterName"), OwnerID: accountID, GroupID: query.Get("groupId"),
		GroupName: query.Get("groupname"), ProjectID: query.Get("projectId"), IDs: ids,
		Substring: query.Get("isSubstringMatch") == "true", Override: override, OrderBy: order,
	})
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not search filters.")
		return
	}
	start, limit, pageErr := metadataPage(r)
	if pageErr != nil {
		writeJerr(w, pageErr)
		return
	}
	total := len(filters)
	if start > total {
		start = total
	}
	end := min(total, start+limit)
	values := make([]map[string]any, 0, end-start)
	for _, filter := range filters[start:end] {
		values = append(values, h.filterBean(filter))
	}
	self := h.BaseURL + r.URL.Path
	if r.URL.RawQuery != "" {
		self += "?" + r.URL.RawQuery
	}
	page := map[string]any{
		"self": self, "startAt": start, "maxResults": limit,
		"total": total, "isLast": end == total, "values": values,
	}
	if end < total {
		next := *r.URL
		nextQuery := next.Query()
		nextQuery.Set("startAt", strconv.Itoa(end))
		next.RawQuery = nextQuery.Encode()
		page["nextPage"] = h.BaseURL + next.String()
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) filterDefaultShareScope(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	switch r.Method {
	case http.MethodGet:
		scope, err := h.Store.FilterDefaultShareScope(r.Context(), workspaceID, userID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load the default share scope.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"scope": scope})
	case http.MethodPut:
		var request struct {
			Scope string `json:"scope"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
			jiraError(w, http.StatusBadRequest, "The default share scope is invalid.")
			return
		}
		if err := h.Store.SetFilterDefaultShareScope(r.Context(), workspaceID, userID, request.Scope); err != nil {
			writeFilterMutationError(w, err)
			return
		}
		scope, _ := h.Store.FilterDefaultShareScope(r.Context(), workspaceID, userID)
		writeJSON(w, http.StatusOK, map[string]string{"scope": scope})
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func (h *Handler) filterCRUD(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	id := parts[0]
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		h.getFilter(w, r, id)
	case len(parts) == 1 && r.Method == http.MethodPut:
		h.putFilter(w, r, id)
	case len(parts) == 1 && r.Method == http.MethodDelete:
		h.deleteFilter(w, r, id)
	case len(parts) == 2 && parts[1] == "favourite" && (r.Method == http.MethodPut || r.Method == http.MethodPost):
		h.setFilterFavourite(w, r, id, true)
	case len(parts) == 2 && parts[1] == "favourite" && r.Method == http.MethodDelete:
		h.setFilterFavourite(w, r, id, false)
	case len(parts) == 2 && parts[1] == "columns":
		h.filterColumns(w, r, id)
	case len(parts) == 2 && parts[1] == "owner" && r.Method == http.MethodPut:
		h.changeFilterOwner(w, r, id)
	case len(parts) == 2 && parts[1] == "permission":
		h.filterPermissions(w, r, id, 0)
	case len(parts) == 3 && parts[1] == "permission":
		permissionID, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			jiraError(w, http.StatusNotFound, "Share permission does not exist.")
			return
		}
		h.filterPermissions(w, r, id, permissionID)
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func (h *Handler) setFilterFavourite(w http.ResponseWriter, r *http.Request, id string, favourite bool) {
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if err := h.Store.SetFilterFavourite(r.Context(), workspaceID, userID, id, favourite); err != nil {
		jiraError(w, http.StatusBadRequest, "Filter does not exist or you do not have permission to view it.")
		return
	}
	filter, err := h.Store.FilterByID(r.Context(), workspaceID, userID, id)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "Filter does not exist.")
		return
	}
	writeJSON(w, http.StatusOK, h.filterBean(filter))
}

func (h *Handler) filterColumns(w http.ResponseWriter, r *http.Request, id string) {
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	switch r.Method {
	case http.MethodGet:
		columns, err := h.Store.FilterColumns(r.Context(), workspaceID, userID, id)
		if err != nil {
			jiraError(w, http.StatusNotFound, "Column configuration was not found.")
			return
		}
		values := make([]map[string]string, 0, len(columns))
		for _, column := range columns {
			values = append(values, map[string]string{"label": filterColumnLabel(column), "value": column})
		}
		writeJSON(w, http.StatusOK, values)
	case http.MethodPut:
		var request struct {
			Columns []string `json:"columns"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&request); err != nil {
			jiraError(w, http.StatusBadRequest, "Column configuration is invalid.")
			return
		}
		if err := h.Store.SetFilterColumns(r.Context(), workspaceID, userID, id, request.Columns); err != nil {
			writeFilterMutationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{})
	case http.MethodDelete:
		if err := h.Store.ResetFilterColumns(r.Context(), workspaceID, userID, id); err != nil {
			writeFilterMutationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func filterColumnLabel(column string) string {
	labels := map[string]string{
		"key": "Key", "summary": "Summary", "issuetype": "Issue Type",
		"status": "Status", "priority": "Priority", "assignee": "Assignee",
		"reporter": "Reporter", "created": "Created", "updated": "Updated",
	}
	if label := labels[column]; label != "" {
		return label
	}
	return column
}

func (h *Handler) changeFilterOwner(w http.ResponseWriter, r *http.Request, id string) {
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		AccountID string `json:"accountId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil || request.AccountID == "" {
		jiraError(w, http.StatusBadRequest, "accountId is required.")
		return
	}
	admin, err := h.Store.IsAdmin(r.Context(), workspaceID, userID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not verify permissions.")
		return
	}
	if err = h.Store.ChangeFilterOwner(r.Context(), workspaceID, userID, id, request.AccountID, admin); err != nil {
		writeFilterMutationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) filterPermissions(w http.ResponseWriter, r *http.Request, id string, permissionID int64) {
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	filter, err := h.Store.FilterByID(r.Context(), workspaceID, userID, id)
	if err != nil {
		jiraError(w, http.StatusNotFound, "Filter does not exist.")
		return
	}
	if permissionID != 0 {
		var permission *models.FilterSharePermission
		for index := range filter.SharePermissions {
			if filter.SharePermissions[index].ID == permissionID {
				permission = &filter.SharePermissions[index]
				break
			}
		}
		if permission == nil {
			jiraError(w, http.StatusNotFound, "Share permission does not exist.")
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, h.filterPermissionBean(*permission))
		case http.MethodDelete:
			if err := h.Store.DeleteFilterPermission(r.Context(), workspaceID, userID, id, permissionID); err != nil {
				jiraError(w, http.StatusNotFound, "Filter or share permission does not exist.")
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			jiraError(w, http.StatusNotFound, "No resource found")
		}
		return
	}
	switch r.Method {
	case http.MethodGet:
		values := make([]map[string]any, 0, len(filter.SharePermissions))
		for _, permission := range filter.SharePermissions {
			values = append(values, h.filterPermissionBean(permission))
		}
		writeJSON(w, http.StatusOK, values)
	case http.MethodPost:
		request := filterPermissionRequest{}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&request); err != nil {
			jiraError(w, http.StatusBadRequest, "Share permission is invalid.")
			return
		}
		permission, err := h.Store.AddFilterPermission(r.Context(), workspaceID, userID, id, filterPermissionInput(request, 1))
		if err != nil {
			writeFilterMutationError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, h.filterPermissionBean(*permission))
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}
