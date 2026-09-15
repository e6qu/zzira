package web

import (
	"encoding/json"
	"errors"
	"fmt"
	appRuntime "github.com/e6qu/zzira/internal/apps"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

type dashboardSlice struct {
	Name, Color string
	Count       int
	// Weight sizes a heat map value from 1 to 5 by its count.
	Weight       int
	Percent      float64
	Dash, Offset string
}
type dashboardTile struct {
	Gadget    models.DashboardGadget
	AppModule *models.AppModule
	// Configurable and Refreshable are what a Connect dashboard item offers.
	Configurable, Refreshable bool
	// DashboardID and Writable are the dashboard the gadget is on and whether
	// the viewer may edit it.
	DashboardID string
	Writable    bool
	Results     store.GadgetResults
	Slices      []dashboardSlice
	Report      *gadgetReport
	Error       string
}
type customDashboardsData struct {
	Dashboards                        []*models.Dashboard
	Dashboard                         *models.Dashboard
	Details                           store.DashboardDetails
	Members                           []*models.User
	Groups                            []store.SiteGroup
	Projects                          []*models.Project
	Roles                             []*models.ProjectRole
	Filters                           []*models.Filter
	Boards                            []*models.Board
	Subscriptions                     []models.DashboardSubscription
	CurrentUserID                     string
	ReportWindows                     []int
	Catalog                           []models.GadgetDefinition
	Groupings, TimeSinceFields        []models.GadgetGrouping
	Columns                           [][]dashboardTile
	ColumnOptions                     []int
	Editing, Adding, Owner, AppGadget bool
	// Slideshow is the site's wallboard slide show, which people who can edit
	// a dashboard configure from it.
	Slideshow            store.WallboardSlideshow
	SlideshowOpen        bool
	Gadget               *models.DashboardGadget
	Config               models.GadgetConfig
	Error, Query, Filter string
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

// loadDashboardShareChoices lists the groups, browsable projects and project
// roles a dashboard can be shared with.
func (h *Handler) loadDashboardShareChoices(w http.ResponseWriter, r *http.Request, ws, userID string, data *customDashboardsData) bool {
	var err error
	if data.Groups, err = h.Store.SiteGroups(r.Context(), ws); err == nil {
		if data.Projects, err = h.Store.ProjectsWithPermissions(r.Context(), ws, userID, []string{"BROWSE_PROJECTS"}); err == nil {
			data.Roles, err = h.Store.ProjectRoles(r.Context(), ws)
		}
	}
	if err != nil {
		http.Error(w, "Could not load sharing choices.", 500)
		return false
	}
	return true
}

func dashboardForm(r *http.Request) store.DashboardDetails {
	permissions := func(key string) []models.DashboardShare {
		out := []models.DashboardShare{}
		for _, v := range r.PostForm[key] {
			kind, rest, _ := strings.Cut(v, ":")
			switch {
			case v == "loggedin":
				out = append(out, models.DashboardShare{Type: "loggedin"})
			case kind == "group" && rest != "":
				out = append(out, models.DashboardShare{Type: "group", Group: &models.DashboardShareGroup{GroupID: rest}})
			case kind == "project" && rest != "":
				out = append(out, models.DashboardShare{Type: "project", Project: &models.DashboardShareProject{ID: rest}})
			case kind == "projectRole" && strings.Contains(rest, ":"):
				projectID, roleID, _ := strings.Cut(rest, ":")
				out = append(out, models.DashboardShare{Type: "projectRole", Project: &models.DashboardShareProject{ID: projectID}, Role: &models.DashboardShareRole{ID: models.FlexibleID(roleID)}})
			case v != "":
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
	if r.Method != http.MethodPost {
		// A new dashboard starts with the owner's default sharing, as a new
		// filter does.
		if scope, err := h.Store.FilterDefaultShareScope(r.Context(), ws, user.ID); err == nil && scope == "AUTHENTICATED" {
			data.Details.SharePermissions = []models.DashboardShare{{Type: "loggedin"}}
		}
	}
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
	if !h.loadDashboardShareChoices(w, r, ws, user.ID, &data) {
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
	data := customDashboardsData{Dashboard: d, Details: store.DashboardDetails{Name: d.Name, Description: d.Description, SharePermissions: d.SharePermissions, EditPermissions: d.EditPermissions}, Owner: d.OwnerID == user.ID, Editing: r.URL.Query().Get("edit") == "1", Adding: r.URL.Query().Get("add") == "1", Catalog: models.GadgetCatalog(), Groupings: models.GadgetGroupings, TimeSinceFields: models.TimeSinceFields}
	appGadgets, err := h.Store.AppModulesByLocation(r.Context(), ws, "jira.dashboard")
	if err != nil {
		http.Error(w, "Could not load app gadgets.", 500)
		return
	}
	for _, module := range appGadgets {
		if _, _, conditions := appRuntime.DashboardItemOptions(module.Body); !appRuntime.ConnectConditionsMet(conditions, h.appConditionFacts(r, ws, user.ID)) {
			continue
		}
		description := module.AppName + " app gadget"
		var metadata struct {
			Description string `json:"description"`
		}
		if json.Unmarshal([]byte(module.Body), &metadata) == nil && metadata.Description != "" {
			description = metadata.Description
		}
		data.Catalog = append(data.Catalog, models.GadgetDefinition{ModuleKey: "app:" + module.ID, Title: module.Title, Description: description, Thumbnail: appModuleThumbnailPath(module)})
	}
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
		case "subscribe":
			_, opErr = h.Store.SaveDashboardSubscription(r.Context(), ws, user.ID, id, r.PostFormValue("schedule"), r.PostForm["recipient"])
		case "unsubscribe":
			subscriptionID, parseErr := strconv.ParseInt(r.PostFormValue("subscriptionId"), 10, 64)
			if parseErr != nil {
				opErr = store.ErrDashboardValidation
			} else {
				opErr = h.Store.DeleteDashboardSubscription(r.Context(), ws, user.ID, id, subscriptionID)
			}
		case "slideshow":
			interval, e := strconv.Atoi(r.PostFormValue("interval"))
			data.Slideshow = store.WallboardSlideshow{DashboardIDs: r.PostForm["slideshowDashboard"], IntervalSeconds: interval, RandomOrder: r.PostFormValue("randomOrder") == "true"}
			data.SlideshowOpen = true
			switch {
			case !d.Writable:
				opErr = store.ErrDashboardPermission
			case e != nil:
				opErr = fmt.Errorf("%w: the slide show interval must be a number of seconds", store.ErrDashboardValidation)
			default:
				opErr = h.Store.SaveDashboardWallboardSlideshow(r.Context(), ws, user.ID, data.Slideshow)
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
			c, e := gadgetConfigForm(r)
			if e != nil || gid <= 0 {
				opErr = store.ErrDashboardValidation
				break
			}
			data.Config = c
			check := c
			if opErr = store.NormalizeGadgetConfig(&check); opErr != nil {
				break
			}
			raw, _ := json.Marshal(c)
			_, opErr = h.Store.SetDashboardProperty(r.Context(), ws, user.ID, id, gid, "zzira.config", raw)
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
	tiles := h.dashboardTiles(r, ws, user.ID, id, gadgets, d.Writable)
	for index, g := range gadgets {
		tile := tiles[index]
		data.Columns[g.Position.Column] = append(data.Columns[g.Position.Column], tile)
		if strconv.FormatInt(g.ID, 10) == editID && d.Writable {
			copy := g
			data.Gadget = &copy
			data.AppGadget = strings.HasPrefix(g.ModuleKey, "app:")
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
	if !h.loadDashboardShareChoices(w, r, ws, user.ID, &data) {
		return
	}
	data.Filters, err = h.Store.ListFilters(r.Context(), ws, user.ID)
	if err != nil {
		http.Error(w, "Could not load filters.", 500)
		return
	}
	// Report gadgets choose among the scrum boards of projects the viewer can
	// browse.
	boards, err := h.Store.BoardsByWorkspace(r.Context(), ws)
	if err != nil {
		http.Error(w, "Could not load boards.", 500)
		return
	}
	browsable := map[string]bool{}
	for _, project := range data.Projects {
		browsable[project.ID] = true
	}
	for _, board := range boards {
		if board.Type == "scrum" && browsable[board.ProjectID] {
			data.Boards = append(data.Boards, board)
		}
	}
	data.ReportWindows = analysisWindows
	if !data.SlideshowOpen {
		if data.Slideshow, err = h.Store.DashboardWallboardSlideshow(r.Context(), ws); err != nil {
			http.Error(w, "Could not load the wallboard slide show.", 500)
			return
		}
	}
	if d.Writable {
		if data.Dashboards, err = h.Store.Dashboards(r.Context(), ws, user.ID); err != nil {
			http.Error(w, "Could not load dashboards.", 500)
			return
		}
	}
	data.CurrentUserID = user.ID
	if data.Subscriptions, err = h.Store.DashboardSubscriptions(r.Context(), ws, user.ID, id); err != nil {
		http.Error(w, "Could not load dashboard emails.", 500)
		return
	}
	h.writeWorkspacePageStatus(w, r, "page_custom_dashboard", user, ws, data, "dashboards", "", status)
}

// InSlideshow reports whether a dashboard is chosen for the slide show.
func (d customDashboardsData) InSlideshow(id string) bool {
	for _, chosen := range d.Slideshow.DashboardIDs {
		if chosen == id {
			return true
		}
	}
	return false
}

func (d customDashboardsData) ShareSelected(kind, id string) bool {
	list := d.Details.SharePermissions
	if kind == "edit" {
		list = d.Details.EditPermissions
	}
	for _, p := range list {
		switch {
		case id == "loggedin" && p.Type == "loggedin", p.User != nil && p.User.AccountID == id:
			return true
		case p.Type == "group" && p.Group != nil && id == "group:"+p.Group.GroupID:
			return true
		case p.Type == "project" && p.Project != nil && id == "project:"+p.Project.ID:
			return true
		case p.Type == "projectRole" && p.Project != nil && p.Role != nil && id == "projectRole:"+p.Project.ID+":"+string(p.Role.ID):
			return true
		}
	}
	return false
}

// gadgetConfigForm reads a gadget's settings from its configuration form: a
// query gadget's JQL, filter, grouping and limit, or a report gadget's project
// or board, window and running totals.
func gadgetConfigForm(r *http.Request) (models.GadgetConfig, error) {
	c := models.GadgetConfig{
		JQL: r.PostFormValue("jql"), FilterID: r.PostFormValue("filterId"), GroupBy: r.PostFormValue("groupBy"), YGroupBy: r.PostFormValue("yGroupBy"),
		ProjectKey: strings.TrimSpace(r.PostFormValue("projectKey")), BoardID: strings.TrimSpace(r.PostFormValue("boardId")),
		Cumulative: r.PostFormValue("cumulative") == "true", DateField: r.PostFormValue("dateField"),
	}
	for field, target := range map[string]*int{"limit": &c.Limit, "days": &c.Days} {
		if value := r.PostFormValue(field); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return c, err
			}
			*target = parsed
		}
	}
	return c, nil
}

// gadgetReport is a report gadget drawn for the person viewing the dashboard.
type gadgetReport struct {
	Project         *models.Project
	Board           *models.Board
	Days            int
	CreatedResolved *createdResolvedView
	Resolution      *resolutionTimeView
	Velocity        *velocityReportView
	Sprint          *models.Sprint
	Burndown        *sprintReportView
	RecentlyCreated *recentlyCreatedView
	AverageAge      *averageAgeView
	TimeSince       *timeSinceView
	DaysRemaining   *daysRemainingView
	SprintHealth    *models.SprintHealth
}

// gadgetReport draws a report gadget from its configured project or board,
// counting only work the viewer can browse. A project that turned Reports
// off, or a board that is not a scrum board, draws nothing.
func (h *Handler) gadgetReport(r *http.Request, ws, userID, moduleKey string, config models.GadgetConfig) (*gadgetReport, string) {
	ctx := r.Context()
	look := h.siteLook(r, ws)
	report := &gadgetReport{Days: config.Days}
	reportsOn := func(projectID string) bool {
		enabled, err := h.Store.ProjectFeatureEnabled(ctx, projectID, "jsw.classic.reports")
		return err == nil && enabled
	}
	const failed = "This report could not be calculated."
	switch moduleKey {
	case "com.zzira:created-vs-resolved", "com.zzira:resolution-time", "com.zzira:recently-created", "com.zzira:average-age", "com.zzira:time-since":
		if config.ProjectKey == "" {
			return nil, "Configure this gadget to choose a project."
		}
		project, err := h.Store.ProjectByIDOrKey(ctx, ws, config.ProjectKey)
		if err != nil || !reportsOn(project.ID) {
			return nil, "This project's reports are not available."
		}
		report.Project = project
		now := time.Now()
		switch moduleKey {
		case "com.zzira:created-vs-resolved":
			data, err := h.Store.CreatedVsResolved(ctx, ws, userID, project.ID, config.Days, now)
			if err != nil {
				return nil, failed
			}
			report.CreatedResolved = newCreatedResolvedView(data, config.Cumulative, look.DateDay)
		case "com.zzira:resolution-time":
			data, err := h.Store.ResolutionTime(ctx, ws, userID, project.ID, config.Days, now)
			if err != nil {
				return nil, failed
			}
			report.Resolution = newResolutionTimeView(data, look.DateDay)
		case "com.zzira:recently-created":
			data, err := h.Store.RecentlyCreated(ctx, ws, userID, project.ID, config.Days, now)
			if err != nil {
				return nil, failed
			}
			report.RecentlyCreated = newRecentlyCreatedView(data, look.DateDay)
		case "com.zzira:average-age":
			data, err := h.Store.AverageAge(ctx, ws, userID, project.ID, config.Days, now)
			if err != nil {
				return nil, failed
			}
			report.AverageAge = newAverageAgeView(data, look.DateDay)
		default:
			data, err := h.Store.TimeSince(ctx, ws, userID, project.ID, config.DateField, config.Days, now)
			if err != nil {
				return nil, failed
			}
			report.TimeSince = newTimeSinceView(data, look.DateDay)
		}
	default:
		if config.BoardID == "" {
			return nil, "Configure this gadget to choose a scrum board."
		}
		board, err := h.Store.BoardByIDInWorkspace(ctx, ws, config.BoardID)
		if err != nil || board.Type != "scrum" || !reportsOn(board.ProjectID) {
			return nil, "This board's reports are not available."
		}
		report.Board = board
		if moduleKey == "com.zzira:velocity" {
			data, err := h.Store.VelocityReport(ctx, ws, userID, board)
			if err != nil {
				return nil, failed
			}
			report.Velocity = newVelocityReportView(data)
			break
		}
		sprints, err := h.Store.SprintsByBoard(ctx, board.ID)
		if err != nil {
			return nil, failed
		}
		for _, sprint := range sprints {
			if sprint.State == "active" {
				report.Sprint = sprint
				break
			}
		}
		if report.Sprint == nil {
			break
		}
		now := time.Now()
		if moduleKey == "com.zzira:days-remaining" {
			report.DaysRemaining = newDaysRemainingView(report.Sprint, now, look.DateDay)
			break
		}
		data, err := h.Store.SprintReport(ctx, ws, userID, board, report.Sprint, now)
		if err != nil {
			return nil, failed
		}
		if moduleKey == "com.zzira:sprint-health" {
			health := models.NewSprintHealth(data, now)
			report.SprintHealth = &health
		} else {
			report.Burndown = newSprintReportView(data, siteDateLayouts{day: look.DateDay, complete: look.DateComplete})
		}
	}
	return report, ""
}

// appConditionFacts are what Connect conditions may ask about the person
// viewing a dashboard.
func (h *Handler) appConditionFacts(r *http.Request, workspaceID, userID string) appRuntime.ConnectConditionFacts {
	admin, err := h.Store.IsAdmin(r.Context(), workspaceID, userID)
	return appRuntime.ConnectConditionFacts{LoggedIn: true, SiteAdmin: err == nil && admin}
}

// dashboardTiles loads what each gadget shows for someone: an app gadget's
// module, or a native gadget's results, chart slices or report.
func (h *Handler) dashboardTiles(r *http.Request, ws, userID, id string, gadgets []models.DashboardGadget, writable bool) []dashboardTile {
	colors := []string{"#1769e0", "#6658d3", "#168568", "#c26914", "#be4565", "#577081"}
	tiles := make([]dashboardTile, 0, len(gadgets))
	var err error
	for _, g := range gadgets {
		tile := dashboardTile{Gadget: g, DashboardID: id, Writable: writable}
		if strings.HasPrefix(g.ModuleKey, "app:") {
			tile.AppModule, err = h.Store.ActiveDashboardAppModule(r.Context(), ws, g.ModuleKey)
			if err != nil {
				tile.Error = "This app gadget is unavailable. Ask an administrator to resume or reinstall the app."
			} else if configurable, refreshable, conditions := appRuntime.DashboardItemOptions(tile.AppModule.Body); !appRuntime.ConnectConditionsMet(conditions, h.appConditionFacts(r, ws, userID)) {
				tile.AppModule, tile.Error = nil, "This app gadget is not available to you."
			} else {
				tile.Configurable, tile.Refreshable = configurable, refreshable
			}
		} else {
			tile.Results, err = h.Store.DashboardGadgetResults(r.Context(), ws, userID, id, g)
			if err != nil {
				tile.Error = "This gadget could not load its query. Check its configuration and saved filter."
			} else if g.ReportGadget() {
				tile.Report, tile.Error = h.gadgetReport(r, ws, userID, g.ModuleKey, tile.Results.Config)
			} else {
				offset, largest := 0.0, 1
				for _, c := range tile.Results.Counts {
					largest = max(largest, c.Count)
				}
				for i, c := range tile.Results.Counts {
					percent := float64(c.Count) * 100 / float64(tile.Results.Total)
					tile.Slices = append(tile.Slices, dashboardSlice{Name: c.Name, Count: c.Count, Weight: 1 + 4*c.Count/largest, Color: colors[i%len(colors)], Percent: percent, Dash: fmt.Sprintf("%.4f %.4f", percent, 100-percent), Offset: fmt.Sprintf("%.4f", -offset)})
					offset += percent
				}
			}
		}
		tiles = append(tiles, tile)
	}
	return tiles
}
