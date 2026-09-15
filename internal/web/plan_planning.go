package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type planTeamOption struct {
	ID   int64
	Name string
}

type planSourceOption struct {
	ID   int64
	Name string
}

type planPageData struct {
	Plan      store.Plan
	Edit      bool
	Work      store.PlanWork
	Timeline  timelineData
	Planning  store.PlanPlanning
	Scenarios []store.PlanScenario
	Scenario  store.PlanScenario
	Colors    []string
	Teams     []planTeamOption
	Sources   []planSourceOption
	Blocking  map[string][]store.PlanDependency
	BlockedBy map[string][]store.PlanDependency
	Notice    string
	Error     string
}

// planContext loads a plan the user may view, with whether they may edit it
// and the scenario the request names, the default when it names none.
func (h *Handler) planContext(w http.ResponseWriter, r *http.Request) (*models.User, string, store.Plan, bool, []store.PlanScenario, store.PlanScenario, bool) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return nil, "", store.Plan{}, false, nil, store.PlanScenario{}, false
	}
	planID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return nil, "", store.Plan{}, false, nil, store.PlanScenario{}, false
	}
	plan, err := h.Store.Plan(r.Context(), workspaceID, planID)
	if err != nil {
		http.NotFound(w, r)
		return nil, "", store.Plan{}, false, nil, store.PlanScenario{}, false
	}
	view, edit, err := h.Store.PlanAccess(r.Context(), workspaceID, user.ID, plan)
	if err != nil {
		http.Error(w, "Could not load the plan.", http.StatusInternalServerError)
		return nil, "", store.Plan{}, false, nil, store.PlanScenario{}, false
	}
	if !view || (plan.Status != "" && plan.Status != "Active") {
		http.NotFound(w, r)
		return nil, "", store.Plan{}, false, nil, store.PlanScenario{}, false
	}
	scenarios, err := h.Store.PlanScenarios(r.Context(), workspaceID, plan.ID)
	if err != nil || len(scenarios) == 0 {
		http.Error(w, "Could not load the plan's scenarios.", http.StatusInternalServerError)
		return nil, "", store.Plan{}, false, nil, store.PlanScenario{}, false
	}
	scenario := scenarios[0]
	wanted := r.URL.Query().Get("scenario")
	if r.Method == http.MethodPost {
		wanted = r.PostFormValue("scenario")
	}
	if wanted != "" {
		found := false
		for _, candidate := range scenarios {
			if strconv.FormatInt(candidate.ID, 10) == wanted {
				scenario, found = candidate, true
			}
		}
		if !found {
			http.NotFound(w, r)
			return nil, "", store.Plan{}, false, nil, store.PlanScenario{}, false
		}
	}
	return user, workspaceID, plan, edit, scenarios, scenario, true
}

// planBack returns to the plan in a scenario with a notice or an error.
func planBack(w http.ResponseWriter, r *http.Request, plan store.Plan, scenarioID int64, page, key, message string) {
	query := url.Values{"scenario": {strconv.FormatInt(scenarioID, 10)}}
	if message != "" {
		query.Set(key, message)
	}
	redirectLocal(w, r, fmt.Sprintf("/plans/%d%s?%s", plan.ID, page, query.Encode()))
}

func planErrorMessage(err error) string {
	switch {
	case errors.Is(err, store.ErrPlanValidation):
		return strings.TrimPrefix(err.Error(), store.ErrPlanValidation.Error()+": ")
	case errors.Is(err, store.ErrPlanNotFound):
		return "That is no longer part of the plan."
	case errors.Is(err, store.ErrPlanNotActive):
		return "The plan is no longer active."
	}
	return ""
}

// PlanPage shows a plan as one of its scenarios plans it: the work across
// months with its teams, sprints and estimates, the dependencies between the
// work, and each team's capacity.
func (h *Handler) PlanPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, plan, edit, scenarios, scenario, ok := h.planContext(w, r)
	if !ok {
		return
	}
	now := time.Now()
	planning, err := h.Store.PlanPlanning(r.Context(), workspaceID, user.ID, plan, scenario.ID, now)
	if err != nil {
		http.Error(w, "Could not load the work in the plan.", http.StatusInternalServerError)
		return
	}
	data := planPageData{Plan: plan, Edit: edit, Work: planning.Work, Planning: planning, Scenarios: scenarios, Scenario: scenario, Colors: store.PlanScenarioColors,
		Timeline: newTimelineData(nil, models.ProjectTimeline{Epics: planning.Work.Items}, now, h.siteLook(r, workspaceID).DateDay),
		Blocking: map[string][]store.PlanDependency{}, BlockedBy: map[string][]store.PlanDependency{},
		Notice: r.URL.Query().Get("notice"), Error: r.URL.Query().Get("error")}
	for _, capacity := range planning.Capacity {
		data.Teams = append(data.Teams, planTeamOption{ID: capacity.Team.ID, Name: capacity.Name})
	}
	names, err := h.Store.PlanSourceNames(r.Context(), workspaceID, plan)
	if err != nil {
		http.Error(w, "Could not load the plan's sources.", http.StatusInternalServerError)
		return
	}
	for _, source := range plan.IssueSources {
		data.Sources = append(data.Sources, planSourceOption{ID: source.ID, Name: names[source.ID]})
	}
	for _, dependency := range planning.Dependencies {
		data.Blocking[dependency.Blocker.Issue.ID] = append(data.Blocking[dependency.Blocker.Issue.ID], dependency)
		data.BlockedBy[dependency.Blocked.Issue.ID] = append(data.BlockedBy[dependency.Blocked.Issue.ID], dependency)
	}
	h.writeWorkspacePage(w, r, "page_plan", user, workspaceID, data, "plans", "")
}

// planCurrentValue is the value a work item has in Jira for a field a plan
// changes, in the form a scenario keeps.
func planCurrentValue(item *store.PlanItem, timeline *models.TimelineItem, field string) json.RawMessage {
	var value any
	switch field {
	case "summary":
		value = item.Issue.Summary
	case "startDate":
		if timeline.StartDate != "" {
			value = timeline.StartDate
		}
	case "endDate":
		if timeline.DueDate != "" {
			value = timeline.DueDate
		}
	case "team":
		if item.TeamID != 0 {
			value = item.TeamID
		}
	case "sprint":
		if item.SprintID != "" {
			value = item.SprintID
		}
	case "estimate":
		if item.Estimate != nil {
			value = *item.Estimate
		}
	}
	raw, _ := json.Marshal(value)
	return raw
}

// PlanWorkChange records the changes to a work item made in a scenario.
func (h *Handler) PlanWorkChange(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	user, workspaceID, plan, edit, _, scenario, ok := h.planContext(w, r)
	if !ok {
		return
	}
	if !edit {
		http.Error(w, "You cannot edit this plan.", http.StatusForbidden)
		return
	}
	// A change is compared with the work item's values in Jira, so the plan
	// built from the default values is read without the scenario's changes.
	base, err := h.Store.PlanPlanning(r.Context(), workspaceID, user.ID, plan, 0, time.Now())
	if err != nil {
		http.Error(w, "Could not load the work in the plan.", http.StatusInternalServerError)
		return
	}
	issueID := r.PostFormValue("issue")
	item, found := base.Items[issueID]
	timeline := base.Timeline(issueID)
	if !found || timeline == nil {
		planBack(w, r, plan, scenario.ID, "", "error", "Choose work that is in the plan.")
		return
	}
	for _, field := range store.PlanChangeFields {
		if _, submitted := r.PostForm[field]; !submitted {
			continue
		}
		value, err := store.NormalizePlanChangeValue(field, r.PostFormValue(field))
		if err == nil && field == "team" && string(value) != "null" {
			err = planTeamInPlan(base, value)
		}
		if err == nil && field == "sprint" && string(value) != "null" {
			err = planSprintInPlan(base, value)
		}
		if err == nil {
			err = h.Store.SetPlanChange(r.Context(), workspaceID, user.ID, plan.ID, scenario.ID, issueID, field, value, planCurrentValue(item, timeline, field))
		}
		if err != nil {
			if message := planErrorMessage(err); message != "" {
				planBack(w, r, plan, scenario.ID, "", "error", message)
				return
			}
			http.Error(w, "Could not save the change in the plan.", http.StatusInternalServerError)
			return
		}
	}
	planBack(w, r, plan, scenario.ID, "", "notice", fmt.Sprintf("%s changed in %s.", item.Issue.Key, scenario.Name))
}

func planTeamInPlan(planning store.PlanPlanning, value json.RawMessage) error {
	var id int64
	if json.Unmarshal(value, &id) == nil {
		for _, team := range planning.Work.Teams {
			if team.ID == id {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: Choose a team of the plan.", store.ErrPlanValidation)
}

func planSprintInPlan(planning store.PlanPlanning, value json.RawMessage) error {
	var id string
	if json.Unmarshal(value, &id) == nil {
		for _, sprint := range planning.Sprints {
			if sprint.ID == id {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: Choose an active or future sprint of a team in the plan.", store.ErrPlanValidation)
}

// PlanScenarioChange creates, renames or deletes a scenario.
func (h *Handler) PlanScenarioChange(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	user, workspaceID, plan, edit, scenarios, scenario, ok := h.planContext(w, r)
	if !ok {
		return
	}
	if !edit {
		http.Error(w, "You cannot edit this plan.", http.StatusForbidden)
		return
	}
	var err error
	target, notice := scenario.ID, ""
	switch r.PostFormValue("action") {
	case "create":
		copyFrom, _ := strconv.ParseInt(r.PostFormValue("copyFrom"), 10, 64)
		target, err = h.Store.CreatePlanScenario(r.Context(), workspaceID, user.ID, plan.ID, r.PostFormValue("name"), r.PostFormValue("color"), copyFrom)
		notice = "Scenario created."
		if err != nil {
			target = scenario.ID
		}
	case "update":
		err = h.Store.UpdatePlanScenario(r.Context(), workspaceID, plan.ID, scenario.ID, r.PostFormValue("name"), r.PostFormValue("color"))
		notice = "Scenario saved."
	case "delete":
		err = h.Store.DeletePlanScenario(r.Context(), workspaceID, plan.ID, scenario.ID)
		target, notice = scenarios[0].ID, scenario.Name+" deleted."
	default:
		http.Error(w, "Choose what to do with the scenario.", http.StatusBadRequest)
		return
	}
	if err != nil {
		if message := planErrorMessage(err); message != "" {
			planBack(w, r, plan, scenario.ID, "", "error", message)
			return
		}
		http.Error(w, "Could not change the scenario.", http.StatusInternalServerError)
		return
	}
	planBack(w, r, plan, target, "", "notice", notice)
}

// PlanCapacityChange sets the capacity of one iteration of a team in a
// scenario; an empty capacity goes back to the team's.
func (h *Handler) PlanCapacityChange(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	user, workspaceID, plan, edit, _, scenario, ok := h.planContext(w, r)
	if !ok {
		return
	}
	if !edit {
		http.Error(w, "You cannot edit this plan.", http.StatusForbidden)
		return
	}
	teamID, err := strconv.ParseInt(r.PostFormValue("team"), 10, 64)
	if err != nil {
		planBack(w, r, plan, scenario.ID, "", "error", "Choose a team of the plan.")
		return
	}
	var capacity *float64
	if text := strings.TrimSpace(r.PostFormValue("capacity")); text != "" {
		value, parseErr := strconv.ParseFloat(text, 64)
		if parseErr != nil {
			planBack(w, r, plan, scenario.ID, "", "error", "The capacity must be a number.")
			return
		}
		capacity = &value
	}
	if err := h.Store.SetPlanIterationCapacity(r.Context(), workspaceID, user.ID, plan.ID, scenario.ID, teamID, r.PostFormValue("iteration"), capacity); err != nil {
		if message := planErrorMessage(err); message != "" {
			planBack(w, r, plan, scenario.ID, "", "error", message)
			return
		}
		http.Error(w, "Could not set the capacity.", http.StatusInternalServerError)
		return
	}
	planBack(w, r, plan, scenario.ID, "", "notice", "Capacity set in "+scenario.Name+".")
}

// PlanSettingsChange sets whether dependent work may share an iteration.
func (h *Handler) PlanSettingsChange(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	_, workspaceID, plan, edit, _, scenario, ok := h.planContext(w, r)
	if !ok {
		return
	}
	if !edit {
		http.Error(w, "You cannot edit this plan.", http.StatusForbidden)
		return
	}
	plan.Scheduling.Dependencies = "Sequential"
	if r.PostFormValue("concurrent") == "true" {
		plan.Scheduling.Dependencies = "Concurrent"
	}
	if err := h.Store.UpdatePlan(r.Context(), workspaceID, plan); err != nil {
		if message := planErrorMessage(err); message != "" {
			planBack(w, r, plan, scenario.ID, "", "error", message)
			return
		}
		http.Error(w, "Could not save the plan setting.", http.StatusInternalServerError)
		return
	}
	planBack(w, r, plan, scenario.ID, "", "notice", "Plan setting saved.")
}

type planReviewChange struct {
	Ref, Field, Label, From, To, By, Note string
}

type planReviewRow struct {
	IssueID, Key, Summary string
	Changes               []planReviewChange
}

type planReviewData struct {
	Plan     store.Plan
	Scenario store.PlanScenario
	Rows     []planReviewRow
	Notice   string
	Problems []string
}

var planFieldLabels = map[string]string{"summary": "Summary", "startDate": "Start date", "endDate": "End date", "team": "Team", "sprint": "Sprint", "estimate": "Estimate"}

// PlanReview lists a scenario's unsaved changes and saves the chosen ones to
// Jira or discards them.
func (h *Handler) PlanReview(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && !parseForm(w, r) {
		return
	}
	user, workspaceID, plan, edit, _, scenario, ok := h.planContext(w, r)
	if !ok {
		return
	}
	if !edit {
		http.Error(w, "You cannot edit this plan.", http.StatusForbidden)
		return
	}
	now := time.Now()
	base, err := h.Store.PlanPlanning(r.Context(), workspaceID, user.ID, plan, 0, now)
	if err != nil {
		http.Error(w, "Could not load the work in the plan.", http.StatusInternalServerError)
		return
	}
	changes, err := h.Store.PlanChanges(r.Context(), workspaceID, plan.ID, scenario.ID)
	if err != nil {
		http.Error(w, "Could not load the plan's changes.", http.StatusInternalServerError)
		return
	}
	data := planReviewData{Plan: plan, Scenario: scenario, Notice: r.URL.Query().Get("notice")}
	if r.Method == http.MethodPost {
		selected := map[string]bool{}
		for _, ref := range r.PostForm["change"] {
			selected[ref] = true
		}
		byIssue := map[string][]store.PlanChange{}
		order := []string{}
		for _, change := range changes {
			if !selected[change.IssueID+"."+change.Field] {
				continue
			}
			if _, seen := byIssue[change.IssueID]; !seen {
				order = append(order, change.IssueID)
			}
			byIssue[change.IssueID] = append(byIssue[change.IssueID], change)
		}
		if len(order) == 0 {
			data.Problems = append(data.Problems, "Choose the changes to save or discard.")
		}
		done := 0
		switch r.PostFormValue("action") {
		case "discard":
			for _, issueID := range order {
				for _, change := range byIssue[issueID] {
					if err := h.Store.DiscardPlanChange(r.Context(), workspaceID, plan.ID, scenario.ID, issueID, change.Field); err != nil && !errors.Is(err, store.ErrPlanNotFound) {
						http.Error(w, "Could not discard the changes.", http.StatusInternalServerError)
						return
					}
					done++
				}
			}
			if done > 0 {
				planBack(w, r, plan, scenario.ID, "/review", "notice", fmt.Sprintf("%d changes discarded.", done))
				return
			}
		case "save":
			notify := r.PostFormValue("notify") == "true"
			for _, issueID := range order {
				saved, problem := h.savePlanChanges(r, user.ID, workspaceID, plan, scenario, base, issueID, byIssue[issueID], notify)
				done += saved
				if problem != "" {
					data.Problems = append(data.Problems, problem)
				}
			}
			if len(data.Problems) == 0 {
				planBack(w, r, plan, scenario.ID, "/review", "notice", fmt.Sprintf("%d changes saved to Jira.", done))
				return
			}
			if done > 0 {
				data.Notice = fmt.Sprintf("%d changes saved to Jira.", done)
			}
			if changes, err = h.Store.PlanChanges(r.Context(), workspaceID, plan.ID, scenario.ID); err != nil {
				http.Error(w, "Could not load the plan's changes.", http.StatusInternalServerError)
				return
			}
			if base, err = h.Store.PlanPlanning(r.Context(), workspaceID, user.ID, plan, 0, now); err != nil {
				http.Error(w, "Could not load the work in the plan.", http.StatusInternalServerError)
				return
			}
		default:
			http.Error(w, "Choose whether to save or discard the changes.", http.StatusBadRequest)
			return
		}
	}
	teamNames := map[int64]string{}
	teamAtlassian := map[int64]string{}
	for _, capacity := range base.Capacity {
		teamNames[capacity.Team.ID] = capacity.Name
		teamAtlassian[capacity.Team.ID] = capacity.Team.AtlassianTeamID
	}
	sprintNames := map[string]string{}
	for _, sprint := range base.Sprints {
		sprintNames[sprint.ID] = sprint.Name
	}
	show := func(field string, raw json.RawMessage) string {
		if len(raw) == 0 || string(raw) == "null" {
			return "None"
		}
		switch field {
		case "team":
			var id int64
			if json.Unmarshal(raw, &id) == nil && teamNames[id] != "" {
				return teamNames[id]
			}
		case "sprint":
			var id string
			if json.Unmarshal(raw, &id) == nil && sprintNames[id] != "" {
				return sprintNames[id]
			}
		case "estimate":
			var number float64
			if json.Unmarshal(raw, &number) == nil {
				return strconv.FormatFloat(number, 'f', -1, 64) + " " + base.Unit
			}
		}
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return text
		}
		return string(raw)
	}
	rows := map[string]*planReviewRow{}
	for _, change := range changes {
		row := rows[change.IssueID]
		if row == nil {
			row = &planReviewRow{IssueID: change.IssueID, Key: change.IssueKey, Summary: change.Summary}
			rows[change.IssueID] = row
		}
		from := "None"
		if item, ok := base.Items[change.IssueID]; ok {
			if timeline := base.Timeline(change.IssueID); timeline != nil {
				from = show(change.Field, planCurrentValue(item, timeline, change.Field))
			}
		}
		reviewed := planReviewChange{Ref: change.IssueID + "." + change.Field, Field: change.Field, Label: planFieldLabels[change.Field], From: from, To: show(change.Field, change.Value), By: change.ChangedByName}
		if change.Field == "team" {
			var id int64
			if json.Unmarshal(change.Value, &id) == nil && teamAtlassian[id] == "" {
				reviewed.Note = "A plan-only team exists only in this plan, so this change cannot be saved to Jira."
			}
		}
		row.Changes = append(row.Changes, reviewed)
	}
	// Rows keep the order of the changes, which follow the work's rank.
	seen := map[string]bool{}
	for _, change := range changes {
		if !seen[change.IssueID] {
			seen[change.IssueID] = true
			data.Rows = append(data.Rows, *rows[change.IssueID])
		}
	}
	h.writeWorkspacePage(w, r, "page_plan_review", user, workspaceID, data, "plans", "")
}

// savePlanChanges saves one work item's chosen changes to Jira as the user,
// and forgets each change once it is saved. It answers how many it saved and
// what stopped the rest.
func (h *Handler) savePlanChanges(r *http.Request, userID, workspaceID string, plan store.Plan, scenario store.PlanScenario, base store.PlanPlanning, issueID string, changes []store.PlanChange, notify bool) (int, string) {
	item, ok := base.Items[issueID]
	if !ok {
		return 0, "Some changed work is no longer in the plan."
	}
	ctx := r.Context()
	key := item.Issue.Key
	startID, endID, err := h.Store.PlanDateFieldIDs(ctx, workspaceID, plan)
	if err != nil {
		return 0, key + ": the plan's date fields could not be loaded."
	}
	in := commands.UpdateIssueInput{ActorID: userID, WorkspaceID: workspaceID, IssueIDOrKey: issueID, Fields: map[string]json.RawMessage{}, SuppressNotifications: !notify}
	fieldChanges := []store.PlanChange{}
	var sprintChange *store.PlanChange
	setDate := func(fieldID string, raw json.RawMessage) string {
		switch fieldID {
		case "":
			return "the plan has no field for this date"
		case "duedate":
			var day string
			_ = json.Unmarshal(raw, &day)
			in.DueDate = &day
		default:
			in.Fields[fieldID] = raw
		}
		return ""
	}
	for index := range changes {
		change := changes[index]
		problem := ""
		switch change.Field {
		case "summary":
			var summary string
			_ = json.Unmarshal(change.Value, &summary)
			in.Summary = &summary
		case "startDate":
			problem = setDate(startID, change.Value)
		case "endDate":
			problem = setDate(endID, change.Value)
		case "team":
			teamFieldID, err := h.Store.PlanTeamFieldID(ctx, workspaceID)
			if err != nil || teamFieldID == "" {
				problem = "the site has no Team field"
				break
			}
			if string(change.Value) == "null" {
				in.Fields[teamFieldID] = json.RawMessage("null")
				break
			}
			var id int64
			_ = json.Unmarshal(change.Value, &id)
			atlassianID := ""
			for _, team := range base.Work.Teams {
				if team.ID == id {
					atlassianID = team.AtlassianTeamID
				}
			}
			if atlassianID == "" {
				problem = "a plan-only team exists only in this plan"
				break
			}
			in.Fields[teamFieldID], _ = json.Marshal(atlassianID)
		case "estimate":
			if base.Unit == "story points" {
				fieldID, err := h.Store.PlanStoryPointFieldID(ctx, workspaceID)
				if err != nil || fieldID == "" {
					problem = "the site has no Story point estimate field"
					break
				}
				in.Fields[fieldID] = change.Value
				break
			}
			seconds := store.ClearEstimate
			var number *float64
			if json.Unmarshal(change.Value, &number) == nil && number != nil {
				hours := *number
				if base.Unit == "days" {
					hours *= h.Store.PlanHoursPerDay(ctx, workspaceID)
				}
				seconds = int64(hours*3600 + 0.5)
			}
			in.OriginalEstimate = &seconds
		case "sprint":
			sprintChange = &changes[index]
			continue
		}
		if problem != "" {
			return 0, fmt.Sprintf("%s: %s, so its %s change was not saved.", key, problem, strings.ToLower(planFieldLabels[change.Field]))
		}
		fieldChanges = append(fieldChanges, change)
	}
	saved := 0
	if len(fieldChanges) > 0 {
		if len(in.Fields) == 0 {
			in.Fields = nil
		}
		if _, _, err := h.Commands.UpdateIssue(ctx, in); err != nil {
			message := err.Error()
			if errors.Is(err, commands.ErrIssueNotEditable) {
				message = "it cannot be edited in its current status"
			}
			return 0, fmt.Sprintf("%s was not saved: %s", key, message)
		}
		for _, change := range fieldChanges {
			if err := h.Store.DiscardPlanChange(ctx, workspaceID, plan.ID, scenario.ID, issueID, change.Field); err == nil {
				saved++
			}
		}
	}
	if sprintChange != nil {
		var sprintID string
		_ = json.Unmarshal(sprintChange.Value, &sprintID)
		if sprintID == "" {
			err = h.Store.RemoveIssueFromPlanning(ctx, userID, workspaceID, issueID)
		} else if rank, rankErr := h.Store.NextSprintRank(ctx, sprintID); rankErr != nil {
			err = rankErr
		} else {
			_, err = h.Store.AddIssueToSprint(ctx, userID, workspaceID, sprintID, issueID, rank)
		}
		if err != nil {
			return saved, fmt.Sprintf("%s: its sprint change was not saved: %s", key, err.Error())
		}
		if err := h.Store.DiscardPlanChange(ctx, workspaceID, plan.ID, scenario.ID, issueID, "sprint"); err == nil {
			saved++
		}
	}
	return saved, ""
}

// PlanTeamSettings changes how a team plans in the plan: its planning style,
// the issue source whose board gives its sprints, its capacity and its
// sprint length.
func (h *Handler) PlanTeamSettings(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	_, workspaceID, plan, edit, _, scenario, ok := h.planContext(w, r)
	if !ok {
		return
	}
	if !edit {
		http.Error(w, "You cannot edit this plan.", http.StatusForbidden)
		return
	}
	teamID, _ := strconv.ParseInt(r.PostFormValue("team"), 10, 64)
	teams, _, err := h.Store.PlanTeams(r.Context(), workspaceID, plan.ID, 0, 100)
	if err != nil {
		http.Error(w, "Could not load the plan's teams.", http.StatusInternalServerError)
		return
	}
	var team *store.PlanTeam
	for index := range teams {
		if teams[index].ID == teamID {
			team = &teams[index]
		}
	}
	if team == nil {
		planBack(w, r, plan, scenario.ID, "", "error", "Choose a team of the plan.")
		return
	}
	team.PlanningStyle = r.PostFormValue("planningStyle")
	team.IssueSourceID, team.Capacity, team.SprintLength = nil, nil, nil
	if value, err := strconv.ParseInt(r.PostFormValue("issueSource"), 10, 64); err == nil && value > 0 {
		team.IssueSourceID = &value
	}
	if text := strings.TrimSpace(r.PostFormValue("capacity")); text != "" {
		value, err := strconv.ParseFloat(text, 64)
		if err != nil {
			planBack(w, r, plan, scenario.ID, "", "error", "The capacity must be a number.")
			return
		}
		team.Capacity = &value
	}
	if text := strings.TrimSpace(r.PostFormValue("sprintLength")); text != "" {
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			planBack(w, r, plan, scenario.ID, "", "error", "The sprint length must be a whole number of weeks.")
			return
		}
		team.SprintLength = &value
	}
	if _, err := h.Store.SavePlanTeam(r.Context(), workspaceID, plan.ID, *team, false); err != nil {
		if message := planErrorMessage(err); message != "" {
			planBack(w, r, plan, scenario.ID, "", "error", message)
			return
		}
		http.Error(w, "Could not save the team.", http.StatusInternalServerError)
		return
	}
	planBack(w, r, plan, scenario.ID, "", "notice", "Team settings saved.")
}
