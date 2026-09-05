package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

type dashboardSlice struct {
	Name, Color  string
	Count        int
	Percent      float64
	Dash, Offset string
}
type dashboardTile struct {
	Gadget  models.DashboardGadget
	Results store.GadgetResults
	Slices  []dashboardSlice
	Error   string
}
type customDashboardsData struct {
	Dashboards             []*models.Dashboard
	Dashboard              *models.Dashboard
	Details                store.DashboardDetails
	Members                []*models.User
	Filters                []*models.Filter
	Catalog                []models.GadgetDefinition
	Columns                [][]dashboardTile
	ColumnOptions          []int
	Editing, Adding, Owner bool
	Gadget                 *models.DashboardGadget
	Config                 models.GadgetConfig
	Error, Query, Filter   string
}

func dashboardWebError(err error) (int, string) {
	switch {
	case errors.Is(err, store.ErrDashboardValidation):
		return 400, err.Error()
	case errors.Is(err, store.ErrDashboardPermission):
		return 403, err.Error()
	case errors.Is(err, pgx.ErrNoRows):
		return 404, "Dashboard or gadget does not exist."
	default:
		log.Print("dashboard operation: ", strconv.Quote(err.Error()))
		return 500, "Could not complete the dashboard operation."
	}
}
func dashboardForm(r *http.Request) store.DashboardDetails {
	permissions := func(key string) []models.DashboardShare {
		out := []models.DashboardShare{}
		for _, v := range r.PostForm[key] {
			if v == "loggedin" {
				out = append(out, models.DashboardShare{Type: "loggedin"})
			} else if v != "" {
				out = append(out, models.DashboardShare{Type: "user", User: &models.DashboardShareUser{AccountID: v}})
			}
		}
		return out
	}
	return store.DashboardDetails{Name: r.PostFormValue("name"), Description: r.PostFormValue("description"), SharePermissions: permissions("viewers"), EditPermissions: permissions("editors")}
}
func (h *Handler) CustomDashboards(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	data := customDashboardsData{Query: r.URL.Query().Get("q"), Filter: r.URL.Query().Get("filter")}
	status := 200
	if r.Method == http.MethodPost {
		if !parseForm(w, r) {
			return
		}
		data.Details = dashboardForm(r)
		d, err := h.Store.SaveDashboard(r.Context(), ws, user.ID, "", data.Details)
		if err == nil {
			redirectLocal(w, r, "/dashboards/"+d.ID+"?add=1")
			return
		}
		status, data.Error = dashboardWebError(err)
	}
	all, err := h.Store.Dashboards(r.Context(), ws, user.ID)
	if err != nil {
		http.Error(w, "Could not load dashboards.", 500)
		return
	}
	for _, d := range all {
		if data.Filter == "my" && d.OwnerID != user.ID || data.Filter == "favourite" && !d.Favourite || !strings.Contains(strings.ToLower(d.Name), strings.ToLower(data.Query)) {
			continue
		}
		data.Dashboards = append(data.Dashboards, d)
	}
	data.Members, err = h.Store.MembersByWorkspace(r.Context(), ws)
	if err != nil {
		http.Error(w, "Could not load members.", 500)
		return
	}
	h.writeWorkspacePageStatus(w, r, "page_custom_dashboards", user, ws, data, "dashboards", "", status)
}
func (h *Handler) CustomDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	d, err := h.Store.Dashboard(r.Context(), ws, user.ID, id)
	if err != nil {
		status, msg := dashboardWebError(err)
		http.Error(w, msg, status)
		return
	}
	data := customDashboardsData{Dashboard: d, Details: store.DashboardDetails{Name: d.Name, Description: d.Description, SharePermissions: d.SharePermissions, EditPermissions: d.EditPermissions}, Owner: d.OwnerID == user.ID, Editing: r.URL.Query().Get("edit") == "1", Adding: r.URL.Query().Get("add") == "1", Catalog: models.GadgetCatalog()}
	status := 200
	if r.Method == http.MethodPost {
		if !parseForm(w, r) {
			return
		}
		var opErr error
		action := r.PostFormValue("action")
		gid, _ := strconv.ParseInt(r.PostFormValue("gadget"), 10, 64)
		switch action {
		case "save":
			data.Details = dashboardForm(r)
			data.Editing = true
			_, opErr = h.Store.SaveDashboard(r.Context(), ws, user.ID, id, data.Details)
		case "copy":
			copy, err := h.Store.CopyDashboard(r.Context(), ws, user.ID, id, store.DashboardDetails{Name: r.PostFormValue("name"), Description: d.Description})
			opErr = err
			if err == nil {
				redirectLocal(w, r, "/dashboards/"+copy.ID)
				return
			}
		case "delete":
			opErr = h.Store.DeleteDashboard(r.Context(), ws, user.ID, id)
			if opErr == nil {
				redirectLocal(w, r, "/dashboards")
				return
			}
		case "favourite":
			opErr = h.Store.SetDashboardFavourite(r.Context(), ws, user.ID, id, r.PostFormValue("favourite") == "true")
		case "presentation":
			refresh, e := strconv.Atoi(r.PostFormValue("refresh"))
			if e != nil {
				opErr = store.ErrDashboardValidation
			} else {
				opErr = h.Store.DashboardPresentation(r.Context(), ws, user.ID, id, r.PostFormValue("layout"), refresh)
			}
			data.Editing = true
		case "add":
			data.Adding = true
			g, err := h.Store.SaveDashboardGadget(r.Context(), ws, user.ID, id, 0, store.GadgetUpdate{ModuleKey: r.PostFormValue("moduleKey")})
			opErr = err
			if err == nil {
				redirectLocal(w, r, fmt.Sprintf("/dashboards/%s?gadget=%d", id, g.ID))
				return
			}
		case "remove":
			if gid <= 0 {
				opErr = store.ErrDashboardValidation
			} else {
				opErr = h.Store.DeleteDashboardGadget(r.Context(), ws, user.ID, id, gid)
			}
		case "position":
			row, e1 := strconv.Atoi(r.PostFormValue("row"))
			col, e2 := strconv.Atoi(r.PostFormValue("column"))
			title, color := r.PostFormValue("title"), r.PostFormValue("color")
			if gid <= 0 || e1 != nil || e2 != nil {
				opErr = store.ErrDashboardValidation
			} else {
				_, opErr = h.Store.SaveDashboardGadget(r.Context(), ws, user.ID, id, gid, store.GadgetUpdate{Title: &title, Color: &color, Position: &models.GadgetPosition{Column: col, Row: row}})
			}
		case "configure":
			limit, e := strconv.Atoi(r.PostFormValue("limit"))
			if e != nil || gid <= 0 {
				opErr = store.ErrDashboardValidation
			} else {
				c := models.GadgetConfig{JQL: r.PostFormValue("jql"), FilterID: r.PostFormValue("filterId"), GroupBy: r.PostFormValue("groupBy"), Limit: limit}
				raw, _ := json.Marshal(c)
				_, opErr = h.Store.SetDashboardProperty(r.Context(), ws, user.ID, id, gid, "zzira.config", raw)
				data.Config = c
			}
		default:
			opErr = store.ErrDashboardValidation
		}
		if opErr == nil {
			redirectLocal(w, r, "/dashboards/"+id)
			return
		}
		status, data.Error = dashboardWebError(opErr)
	}
	gadgets, err := h.Store.DashboardGadgets(r.Context(), ws, user.ID, id)
	if err != nil {
		status, msg := dashboardWebError(err)
		http.Error(w, msg, status)
		return
	}
	data.Columns = make([][]dashboardTile, d.Columns())
	for i := 0; i < d.Columns(); i++ {
		data.ColumnOptions = append(data.ColumnOptions, i)
	}
	editID := r.URL.Query().Get("gadget")
	if r.Method == http.MethodPost && r.PostFormValue("gadget") != "" {
		editID = r.PostFormValue("gadget")
	}
	colors := []string{"#1769e0", "#6658d3", "#168568", "#c26914", "#be4565", "#577081"}
	for _, g := range gadgets {
		tile := dashboardTile{Gadget: g}
		tile.Results, err = h.Store.DashboardGadgetResults(r.Context(), ws, user.ID, id, g)
		if err != nil {
			tile.Error = "This gadget could not load its query. Check its configuration and saved filter."
		} else {
			offset := 0.0
			for i, c := range tile.Results.Counts {
				percent := float64(c.Count) * 100 / float64(tile.Results.Total)
				tile.Slices = append(tile.Slices, dashboardSlice{Name: c.Name, Count: c.Count, Color: colors[i%len(colors)], Percent: percent, Dash: fmt.Sprintf("%.4f %.4f", percent, 100-percent), Offset: fmt.Sprintf("%.4f", -offset)})
				offset += percent
			}
		}
		data.Columns[g.Position.Column] = append(data.Columns[g.Position.Column], tile)
		if strconv.FormatInt(g.ID, 10) == editID && d.Writable {
			copy := g
			data.Gadget = &copy
			if r.Method != http.MethodPost || r.PostFormValue("action") != "configure" {
				data.Config = tile.Results.Config
			}
			_ = store.NormalizeGadgetConfig(&data.Config)
		}
	}
	if strings.HasSuffix(r.URL.Path, "/content") {
		writePageStatus(w, "custom_dashboard_grid", data, status)
		return
	}
	data.Members, err = h.Store.MembersByWorkspace(r.Context(), ws)
	if err != nil {
		http.Error(w, "Could not load members.", 500)
		return
	}
	data.Filters, err = h.Store.ListFilters(r.Context(), ws, user.ID)
	if err != nil {
		http.Error(w, "Could not load filters.", 500)
		return
	}
	h.writeWorkspacePageStatus(w, r, "page_custom_dashboard", user, ws, data, "dashboards", "", status)
}
func (d customDashboardsData) ShareSelected(kind, id string) bool {
	list := d.Details.SharePermissions
	if kind == "edit" {
		list = d.Details.EditPermissions
	}
	for _, p := range list {
		if id == "loggedin" && p.Type == "loggedin" || p.User != nil && p.User.AccountID == id {
			return true
		}
	}
	return false
}
