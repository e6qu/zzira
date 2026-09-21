package web

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// Setting a plan up was REST-only: the browser could show a plan and change
// the work in it, but a plan had to be created, given its sources, its
// exclusions and its access somewhere else. These handlers are that setup.

type planSourceChoice struct {
	// Value is the "Type:id" a form submits, which is how a source is named
	// in one control.
	Value string
	Label string
	Group string
	// Chosen reports that the plan already reads this source.
	Chosen bool
}

type planPermissionRow struct {
	Index  int
	Type   string
	Holder string
	Name   string
}

type planSettingsData struct {
	Estimations []string
	DateFields  []struct{ Value, Label string }
	Plan        store.Plan
	Lead        string
	Sources     []planSourceChoice
	IssueTypes  []models.IssueType
	Statuses    []models.Status
	Permissions []planPermissionRow
	Members     []*models.User
	Groups      []*models.Group
	Excluded    map[int64]bool
	Notice      string
	Error       string
}

// planSourceChoices lists every board, project and filter a plan can read,
// marking the ones it already reads.
func (h *Handler) planSourceChoices(r *http.Request, workspaceID, userID string, plan store.Plan) ([]planSourceChoice, error) {
	chosen := map[string]bool{}
	for _, source := range plan.IssueSources {
		chosen[source.Type+":"+strconv.FormatInt(source.Value, 10)] = true
	}
	choices := make([]planSourceChoice, 0)
	add := func(group, kind, id, label string) {
		value := kind + ":" + id
		choices = append(choices, planSourceChoice{Value: value, Label: label, Group: group, Chosen: chosen[value]})
	}
	projects, err := h.Store.ProjectsByWorkspace(r.Context(), workspaceID)
	if err != nil {
		return nil, err
	}
	for _, project := range projects {
		add("Projects", "Project", project.ID, project.Name+" ("+project.Key+")")
	}
	boards, err := h.Store.BoardsByWorkspace(r.Context(), workspaceID)
	if err != nil {
		return nil, err
	}
	for _, board := range boards {
		add("Boards", "Board", strconv.FormatInt(board.JiraID, 10), board.Name)
	}
	filters, err := h.Store.Filters(r.Context(), workspaceID, userID, store.FilterSearch{OrderBy: "name"})
	if err != nil {
		return nil, err
	}
	for _, filter := range filters {
		add("Filters", "Filter", strconv.FormatInt(filter.JiraID, 10), filter.Name)
	}
	return choices, nil
}

// planSources reads the sources a form submitted, in the order it listed them.
func planSources(values []string) ([]store.PlanIssueSource, error) {
	sources := make([]store.PlanIssueSource, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		kind, id, found := strings.Cut(value, ":")
		if !found || seen[value] {
			continue
		}
		switch kind {
		case "Board", "Project", "Filter":
		default:
			return nil, errors.New("a plan reads boards, projects and filters")
		}
		number, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return nil, errors.New("a plan reads boards, projects and filters")
		}
		seen[value] = true
		sources = append(sources, store.PlanIssueSource{Type: kind, Value: number})
	}
	return sources, nil
}

// planEstimations and planDateFields are the scheduling choices a plan makes:
// what its estimates are counted in, and where it reads each of its dates.
var planEstimations = []string{"StoryPoints", "Days", "Hours"}

var planDateFields = []struct{ Value, Label string }{
	{"TargetStartDate", "Target start"},
	{"TargetEndDate", "Target end"},
	{"DueDate", "Due date"},
}

// planScheduling reads the scheduling a form chose, leaving what it did not
// name as it was.
func planScheduling(r *http.Request, plan *store.Plan) error {
	estimation := r.PostFormValue("estimation")
	if estimation == "" {
		estimation = plan.Scheduling.Estimation
	}
	if estimation == "" {
		estimation = "StoryPoints"
	}
	if !slices.Contains(planEstimations, estimation) {
		return errors.New("estimates are counted in story points, days or hours")
	}
	plan.Scheduling.Estimation = estimation
	for name, field := range map[string]*store.PlanDateField{"startField": &plan.Scheduling.StartDate, "endField": &plan.Scheduling.EndDate} {
		value := r.PostFormValue(name)
		if value == "" {
			continue
		}
		known := false
		for _, choice := range planDateFields {
			known = known || choice.Value == value
		}
		if !known {
			return errors.New("a plan reads its dates from Target start, Target end or the due date")
		}
		field.Type, field.DateCustomFieldID = value, nil
	}
	return nil
}

// CreatePlan makes a plan from the plans directory. Creating one is site
// administration, as it is over REST (/rest/api/3/plans/plan); who may then
// configure and read it is the plan's own lead and permissions.
func (h *Handler) CreatePlan(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	sources, err := planSources(r.Form["source"])
	if err != nil {
		redirectLocal(w, r, "/plans?error="+url.QueryEscape(err.Error()))
		return
	}
	if len(sources) == 0 {
		redirectLocal(w, r, "/plans?error="+url.QueryEscape("Choose at least one board, project or filter for the plan to read."))
		return
	}
	plan := store.Plan{Name: r.PostFormValue("name"), LeadAccountID: user.ID, IssueSources: sources}
	if err := planScheduling(r, &plan); err != nil {
		redirectLocal(w, r, "/plans?error="+url.QueryEscape(err.Error()))
		return
	}
	id, err := h.Store.CreatePlan(r.Context(), workspaceID, user.ID, plan)
	if err != nil {
		if message := planErrorMessage(err); message != "" {
			redirectLocal(w, r, "/plans?error="+url.QueryEscape(message))
			return
		}
		http.Error(w, "Could not create the plan.", http.StatusInternalServerError)
		return
	}
	redirectLocal(w, r, "/plans/"+strconv.FormatInt(id, 10))
}

// PlanSettingsPage shows what a plan reads, what it leaves out and who may
// see it. Everyone who can view the plan can read it; only an editor's page
// carries the controls.
func (h *Handler) PlanSettingsPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, plan, edit, _, _, ok := h.planContext(w, r)
	if !ok {
		return
	}
	if !edit {
		http.Error(w, "You cannot edit this plan.", http.StatusForbidden)
		return
	}
	data := planSettingsData{
		Plan: plan, Excluded: map[int64]bool{}, Estimations: planEstimations, DateFields: planDateFields,
		Notice: r.URL.Query().Get("notice"), Error: r.URL.Query().Get("error"),
	}
	var err error
	if data.Sources, err = h.planSourceChoices(r, workspaceID, user.ID, plan); err != nil {
		http.Error(w, "Could not load what a plan can read.", http.StatusInternalServerError)
		return
	}
	if data.IssueTypes, err = h.Store.IssueTypes(r.Context(), workspaceID); err != nil {
		http.Error(w, "Could not load the work types.", http.StatusInternalServerError)
		return
	}
	if data.Statuses, err = h.Store.StatusesForWorkspace(r.Context(), workspaceID); err != nil {
		http.Error(w, "Could not load the statuses.", http.StatusInternalServerError)
		return
	}
	if data.Members, err = h.Store.MembersByWorkspace(r.Context(), workspaceID); err != nil {
		http.Error(w, "Could not load the people.", http.StatusInternalServerError)
		return
	}
	if data.Groups, err = h.Store.GroupsByWorkspace(r.Context(), workspaceID); err != nil {
		http.Error(w, "Could not load the groups.", http.StatusInternalServerError)
		return
	}
	for _, id := range plan.ExclusionRules.IssueTypeIDs {
		data.Excluded[id] = true
	}
	for _, id := range plan.ExclusionRules.WorkStatusIDs {
		data.Excluded[id] = true
	}
	names := map[string]string{}
	for _, member := range data.Members {
		names[member.ID] = member.DisplayName
	}
	for _, group := range data.Groups {
		names[group.ID] = group.Name
	}
	if lead, err := h.Store.UserByID(r.Context(), plan.LeadAccountID); err == nil && lead != nil {
		data.Lead = lead.DisplayName
	}
	for index, permission := range plan.Permissions {
		name := names[permission.Holder]
		if name == "" {
			name = permission.Holder
		}
		data.Permissions = append(data.Permissions, planPermissionRow{Index: index, Type: permission.Type, Holder: permission.HolderType, Name: name})
	}
	h.writeWorkspacePage(w, r, "page_plan_settings", user, workspaceID, data, "plans", "")
}

// PlanSettingsSave applies one section of the settings page. Each section
// says what it changed, or why it could not.
func (h *Handler) PlanSettingsSave(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, plan, edit, _, _, ok := h.planContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	if !edit {
		http.Error(w, "You cannot edit this plan.", http.StatusForbidden)
		return
	}
	notice := ""
	switch r.PostFormValue("action") {
	case "details":
		plan.Name = r.PostFormValue("name")
		if lead := r.PostFormValue("lead"); lead != "" {
			plan.LeadAccountID = lead
		}
		notice = "Plan details saved."
	case "scheduling":
		if err := planScheduling(r, &plan); err != nil {
			planSettingsBack(w, r, plan, "error", err.Error())
			return
		}
		notice = "Scheduling saved."
	case "sources":
		sources, err := planSources(r.Form["source"])
		if err != nil {
			planSettingsBack(w, r, plan, "error", err.Error())
			return
		}
		if len(sources) == 0 {
			planSettingsBack(w, r, plan, "error", "Choose at least one board, project or filter for the plan to read.")
			return
		}
		plan.IssueSources = sources
		notice = "Issue sources saved."
	case "exclusions":
		days, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("completedDays")))
		if err != nil || days < 0 || days > 3650 {
			planSettingsBack(w, r, plan, "error", "Completed work is shown for a whole number of days, up to 3650.")
			return
		}
		types, typeErr := planExcludedIDs(r.Form["issueType"])
		statuses, statusErr := planExcludedIDs(r.Form["status"])
		if typeErr != nil || statusErr != nil {
			planSettingsBack(w, r, plan, "error", "Choose what to leave out from the lists.")
			return
		}
		plan.ExclusionRules.NumberOfDaysToShowCompletedIssues = days
		plan.ExclusionRules.IssueTypeIDs = types
		plan.ExclusionRules.WorkStatusIDs = statuses
		notice = "Exclusion rules saved."
	case "grant":
		holder := strings.TrimSpace(r.PostFormValue("holder"))
		holderType, access := r.PostFormValue("holderType"), r.PostFormValue("access")
		if holder == "" || (holderType != "AccountId" && holderType != "Group") || (access != "View" && access != "Edit") {
			planSettingsBack(w, r, plan, "error", "Choose a person or a group, and what they may do.")
			return
		}
		for _, permission := range plan.Permissions {
			if permission.HolderType == holderType && permission.Holder == holder && permission.Type == access {
				planSettingsBack(w, r, plan, "error", "They already have that access.")
				return
			}
		}
		plan.Permissions = append(plan.Permissions, store.PlanPermission{Type: access, HolderType: holderType, Holder: holder})
		notice = "Plan access granted."
	case "revoke":
		index, err := strconv.Atoi(r.PostFormValue("index"))
		if err != nil || index < 0 || index >= len(plan.Permissions) {
			planSettingsBack(w, r, plan, "error", "That access is already gone.")
			return
		}
		plan.Permissions = append(plan.Permissions[:index], plan.Permissions[index+1:]...)
		notice = "Plan access removed."
	default:
		planSettingsBack(w, r, plan, "error", "That is not something this page changes.")
		return
	}
	if err := h.Store.UpdatePlan(r.Context(), workspaceID, plan); err != nil {
		if message := planErrorMessage(err); message != "" {
			planSettingsBack(w, r, plan, "error", message)
			return
		}
		http.Error(w, "Could not save the plan.", http.StatusInternalServerError)
		return
	}
	planSettingsBack(w, r, plan, "notice", notice)
}

func planExcludedIDs(values []string) ([]int64, error) {
	ids := make([]int64, 0, len(values))
	for _, value := range values {
		number, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return nil, err
		}
		ids = append(ids, number)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func planSettingsBack(w http.ResponseWriter, r *http.Request, plan store.Plan, key, message string) {
	redirectLocal(w, r, "/plans/"+strconv.FormatInt(plan.ID, 10)+"/settings?"+key+"="+url.QueryEscape(message))
}
