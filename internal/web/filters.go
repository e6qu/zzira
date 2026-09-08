package web

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

type filterColumnChoice struct {
	Value    string
	Label    string
	Selected bool
}

type filterPermissionRow struct {
	ID     int64
	Label  string
	Access string
}

type savedFilterRow struct {
	Filter          *models.Filter
	OpenURL         string
	Owner           bool
	CanEdit         bool
	CanTransfer     bool
	Columns         []filterColumnChoice
	ViewPermissions []filterPermissionRow
	EditPermissions []filterPermissionRow
}

type savedFiltersPageData struct {
	Rows         []savedFilterRow
	Members      []*models.User
	Groups       []*models.Group
	Projects     []*models.Project
	DefaultScope string
	Notice       string
	Error        string
	CanAdmin     bool
}

var savedFilterColumns = []filterColumnChoice{
	{Value: "key", Label: "Key"},
	{Value: "summary", Label: "Summary"},
	{Value: "issuetype", Label: "Work type"},
	{Value: "status", Label: "Status"},
	{Value: "priority", Label: "Priority"},
	{Value: "assignee", Label: "Assignee"},
	{Value: "reporter", Label: "Reporter"},
	{Value: "created", Label: "Created"},
	{Value: "updated", Label: "Updated"},
}

func (h *Handler) SavedFilters(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	workspaceID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	admin, err := h.Store.IsAdmin(r.Context(), workspaceID, user.ID)
	if err != nil {
		log.Printf("saved filters admin lookup: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	filters, err := h.Store.Filters(r.Context(), workspaceID, user.ID, store.FilterSearch{Override: admin, OrderBy: "favourite"})
	if err != nil {
		log.Printf("saved filters list: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	members, err := h.Store.MembersByWorkspace(r.Context(), workspaceID)
	if err != nil {
		log.Printf("saved filters members: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	groups, err := h.Store.GroupsForWorkspace(r.Context(), workspaceID)
	if err != nil {
		log.Printf("saved filters groups: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	projects, err := h.Store.ProjectsByWorkspace(r.Context(), workspaceID)
	if err != nil {
		log.Printf("saved filters projects: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	scope, err := h.Store.FilterDefaultShareScope(r.Context(), workspaceID, user.ID)
	if err != nil {
		log.Printf("saved filters default share scope: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	projectKey := ""
	if len(projects) > 0 {
		projectKey = projects[0].Key
	}
	data := savedFiltersPageData{
		Rows:    savedFilterRows(filters, user.ID, admin, projectKey),
		Members: members, Groups: groups, Projects: projects, DefaultScope: scope,
		Notice: strings.TrimSpace(r.URL.Query().Get("notice")),
		Error:  strings.TrimSpace(r.URL.Query().Get("error")), CanAdmin: admin,
	}
	h.writeWorkspacePage(w, r, "page_saved_filters", user, workspaceID, data, "filters", projectKey)
}

// savedFilterRows keeps the page model independent of template logic and makes every
// permission scope readable without exposing implementation identifiers.
func savedFilterRows(filters []*models.Filter, userID string, admin bool, projectKey string) []savedFilterRow {
	rows := make([]savedFilterRow, 0, len(filters))
	for _, filter := range filters {
		selected := map[string]bool{}
		for _, column := range filter.Columns {
			selected[column] = true
		}
		columns := make([]filterColumnChoice, len(savedFilterColumns))
		for index, column := range savedFilterColumns {
			columns[index] = column
			columns[index].Selected = selected[column.Value]
		}
		row := savedFilterRow{
			Filter: filter, Owner: filter.OwnerID == userID, CanEdit: filter.Writable,
			CanTransfer: filter.OwnerID == userID || admin, Columns: columns,
		}
		if projectKey != "" {
			row.OpenURL = "/issues/" + url.PathEscape(projectKey) + "?mode=advanced&jql=" + url.QueryEscape(filter.JQL) + "&filter=" + url.QueryEscape(filter.ID)
		}
		for _, permission := range filter.SharePermissions {
			value := filterPermissionRow{ID: permission.ID, Label: filterPermissionLabel(permission), Access: "View"}
			if permission.Rights == 2 {
				value.Access = "Edit"
				row.EditPermissions = append(row.EditPermissions, value)
			} else {
				row.ViewPermissions = append(row.ViewPermissions, value)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func filterPermissionLabel(permission models.FilterSharePermission) string {
	switch permission.Type {
	case "global":
		return "Anyone"
	case "authenticated":
		return "Everyone signed in"
	case "user":
		return permission.AccountName
	case "group":
		return "Group · " + permission.GroupName
	case "project":
		return "Project · " + permission.ProjectName
	case "projectRole":
		return "Project · " + permission.ProjectName + " · " + permission.ProjectRole
	default:
		return permission.Type
	}
}

func (h *Handler) SavedFilterDefaultScope(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	workspaceID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := h.Store.SetFilterDefaultShareScope(r.Context(), workspaceID, user.ID, r.FormValue("scope")); err != nil {
		redirectFilters(w, r, "", "Choose Private or Everyone signed in.")
		return
	}
	redirectFilters(w, r, "Default sharing updated.", "")
}

func (h *Handler) UpdateSavedFilter(w http.ResponseWriter, r *http.Request, id string) {
	if !parseForm(w, r) {
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	workspaceID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var err error
	notice := "Filter updated."
	switch r.FormValue("action") {
	case "update":
		_, err = h.Store.UpdateManagedFilter(r.Context(), workspaceID, user.ID, id, store.FilterDetails{
			Name: r.FormValue("name"), JQL: r.FormValue("jql"), Description: r.FormValue("description"),
		})
	case "favorite":
		err = h.Store.SetFilterFavourite(r.Context(), workspaceID, user.ID, id, r.FormValue("favorite") == "true")
		if r.FormValue("favorite") == "true" {
			notice = "Filter added to favorites."
		} else {
			notice = "Filter removed from favorites."
		}
	case "columns":
		columns := r.Form["column"]
		if len(columns) == 0 {
			err = h.Store.ResetFilterColumns(r.Context(), workspaceID, user.ID, id)
			notice = "Filter columns reset."
		} else {
			err = h.Store.SetFilterColumns(r.Context(), workspaceID, user.ID, id, columns)
			notice = "Filter columns updated."
		}
	case "share":
		rights := 1
		if r.FormValue("access") == "edit" {
			rights = 2
		}
		_, err = h.Store.AddFilterPermission(r.Context(), workspaceID, user.ID, id, store.FilterPermissionInput{
			Type: r.FormValue("shareType"), Rights: rights,
			AccountID: r.FormValue("accountId"), GroupID: r.FormValue("groupId"),
			ProjectID: r.FormValue("projectId"), ProjectRoleID: r.FormValue("projectRoleId"),
		})
		notice = "Filter access added."
	case "remove-share":
		permissionID, parseErr := strconv.ParseInt(r.FormValue("permissionId"), 10, 64)
		if parseErr != nil {
			err = store.ErrFilterValidation
		} else {
			err = h.Store.DeleteFilterPermission(r.Context(), workspaceID, user.ID, id, permissionID)
		}
		notice = "Filter access removed."
	case "owner":
		admin, adminErr := h.Store.IsAdmin(r.Context(), workspaceID, user.ID)
		if adminErr != nil {
			err = adminErr
		} else {
			err = h.Store.ChangeFilterOwner(r.Context(), workspaceID, user.ID, id, r.FormValue("accountId"), admin)
		}
		notice = "Filter owner changed."
	case "delete":
		err = h.Store.DeleteFilter(r.Context(), workspaceID, user.ID, id)
		notice = "Filter deleted."
	default:
		err = store.ErrFilterValidation
	}
	if err != nil {
		redirectFilters(w, r, "", savedFilterError(err))
		return
	}
	redirectFilters(w, r, notice, "")
}

func savedFilterError(err error) string {
	switch {
	case errors.Is(err, store.ErrFilterValidation):
		return strings.TrimPrefix(err.Error(), store.ErrFilterValidation.Error()+": ")
	case errors.Is(err, store.ErrFilterPermission):
		return "You do not have permission to change this filter."
	case errors.Is(err, pgx.ErrNoRows):
		return "The filter or selected access target no longer exists."
	default:
		return "The filter could not be updated."
	}
}

func redirectFilters(w http.ResponseWriter, r *http.Request, notice, message string) {
	query := url.Values{}
	if notice != "" {
		query.Set("notice", notice)
	}
	if message != "" {
		query.Set("error", message)
	}
	target := "/filters"
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
