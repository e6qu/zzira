package web

import (
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// sharedFilterRow and sharedDashboardRow are one shared item on the page, with
// who owns it and who it reaches.
type sharedFilterRow struct {
	Filter *models.Filter
	Shares []string
}

type sharedDashboardRow struct {
	Dashboard *models.Dashboard
	Shares    []string
}

// sharedItemsPageData is the shared items settings page.
type sharedItemsPageData struct {
	Filters    []sharedFilterRow
	Dashboards []sharedDashboardRow
	Members    []models.User
	Notice     string
	Error      string
}

// filterShareLabels says who a filter reaches, in the words the share uses.
func filterShareLabels(shares []models.FilterSharePermission) []string {
	labels := []string{}
	for _, share := range shares {
		switch {
		case share.Type == "global" || share.Type == "loggedin":
			labels = append(labels, "Everyone signed in")
		case share.GroupName != "":
			labels = append(labels, "Group "+share.GroupName)
		case share.ProjectName != "":
			if share.ProjectRole != "" {
				labels = append(labels, share.ProjectName+" · "+share.ProjectRole)
				continue
			}
			labels = append(labels, "Project "+share.ProjectName)
		case share.AccountName != "":
			labels = append(labels, share.AccountName)
		default:
			labels = append(labels, share.Type)
		}
	}
	return labels
}

// orElse is the first of two labels that says something.
func orElse(preferred, fallback string) string {
	if strings.TrimSpace(preferred) != "" {
		return preferred
	}
	return fallback
}

// dashboardShareLabels says who a dashboard reaches.
func dashboardShareLabels(shares []models.DashboardShare) []string {
	labels := []string{}
	for _, share := range shares {
		switch {
		case share.Type == "global" || share.Type == "loggedin" || share.Type == "authenticated":
			labels = append(labels, "Everyone signed in")
		case share.Group != nil:
			labels = append(labels, "Group "+orElse(share.Group.Name, share.Group.GroupID))
		case share.Project != nil:
			project := "Project " + orElse(share.Project.Name, share.Project.Key)
			if share.Role != nil {
				labels = append(labels, project+" · "+orElse(share.Role.Name, string(share.Role.ID)))
				continue
			}
			labels = append(labels, project)
		case share.User != nil:
			labels = append(labels, share.User.AccountID)
		default:
			labels = append(labels, share.Type)
		}
	}
	return labels
}

// SharedItemsPage lists the filters and dashboards people share on this site,
// so an administrator can hand one on or remove it.
func (h *Handler) SharedItemsPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	data := sharedItemsPageData{Notice: r.URL.Query().Get("notice"), Error: r.URL.Query().Get("error")}
	filters, err := h.Store.Filters(r.Context(), workspaceID, user.ID, store.FilterSearch{Override: true, OrderBy: "name"})
	if err != nil {
		http.Error(w, "Could not load filters.", http.StatusInternalServerError)
		return
	}
	for _, filter := range filters {
		// A private filter is nobody else's business; this page is the shared
		// ones, as Jira's shared items are.
		if len(filter.SharePermissions) == 0 {
			continue
		}
		data.Filters = append(data.Filters, sharedFilterRow{Filter: filter, Shares: filterShareLabels(filter.SharePermissions)})
	}
	dashboards, err := h.Store.AdministeredDashboards(r.Context(), workspaceID, user.ID)
	if err != nil {
		http.Error(w, "Could not load dashboards.", http.StatusInternalServerError)
		return
	}
	for _, dashboard := range dashboards {
		if len(dashboard.SharePermissions) == 0 && len(dashboard.EditPermissions) == 0 {
			continue
		}
		shares := append(dashboardShareLabels(dashboard.SharePermissions), dashboardShareLabels(dashboard.EditPermissions)...)
		data.Dashboards = append(data.Dashboards, sharedDashboardRow{Dashboard: dashboard, Shares: shares})
	}
	members, err := h.Store.MembersByWorkspace(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "Could not load people.", http.StatusInternalServerError)
		return
	}
	for _, member := range members {
		data.Members = append(data.Members, *member)
	}
	h.writeWorkspacePage(w, r, "page_shared_items", user, workspaceID, data, "shared-items", "")
}

// SharedItemsMutation hands a shared filter or dashboard to another person, or
// removes it. Administration reaches items the administrator does not own,
// which is the point of the page.
func (h *Handler) SharedItemsMutation(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	value := func(name string) string { return strings.TrimSpace(r.PostFormValue(name)) }
	notice, err := "", error(nil)
	switch r.PostFormValue("action") {
	case "filter-owner":
		if err = h.Store.ChangeFilterOwner(r.Context(), workspaceID, user.ID, value("filter"), value("accountId"), true); err == nil {
			notice = "Filter owner changed."
		}
	case "filter-delete":
		if err = h.Store.DeleteFilter(r.Context(), workspaceID, user.ID, value("filter"), true); err == nil {
			notice = "Filter deleted; the people it was shared with no longer see it."
		}
	case "dashboard-owner":
		ctx := store.WithDashboardAdministration(r.Context())
		id := value("dashboard")
		var current *models.Dashboard
		if current, err = h.Store.Dashboard(ctx, workspaceID, user.ID, id); err == nil {
			_, err = h.Store.SaveDashboard(ctx, workspaceID, user.ID, id, store.DashboardDetails{
				Name: current.Name, Description: current.Description,
				SharePermissions: current.SharePermissions, EditPermissions: current.EditPermissions,
				NewOwnerID: value("accountId"),
			})
		}
		if err == nil {
			notice = "Dashboard owner changed."
		}
	case "dashboard-delete":
		if err = h.Store.DeleteDashboard(store.WithDashboardAdministration(r.Context()), workspaceID, user.ID, value("dashboard")); err == nil {
			notice = "Dashboard deleted; the people it was shared with no longer see it."
		}
	default:
		http.Error(w, "Unknown shared item action.", http.StatusBadRequest)
		return
	}
	h.redirectMetadata(w, r, "/settings/shared-items", notice, err)
}
