package web

import (
	"net/http"
	"sort"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

type projectDirectoryCard struct {
	Project      *models.Project
	IssueCount   int
	WorkflowName string
	Boards       []*models.Board
	PrimaryBoard *models.Board
}

type projectsPageData struct {
	Projects []projectDirectoryCard
}

type projectOverviewData struct {
	Project      *models.Project
	Issues       []*models.Issue
	Boards       []*models.Board
	PrimaryBoard *models.Board
	Workflow     workflow.Workflow
}

type peoplePageData struct {
	People []*models.User
}

type profilePageData struct {
	Profile  *models.User
	Self     bool
	Assigned []*models.Issue
	Reported []*models.Issue
}

type workflowDirectoryCard struct {
	Workflow workflow.Workflow
	Projects []*models.Project
}

type workflowsPageData struct {
	Workflows []workflowDirectoryCard
	CanCreate bool
}

type workflowTransitionView struct {
	ID   string
	Name string
	To   models.Status
}

type workflowLaneView struct {
	Status      models.Status
	Transitions []workflowTransitionView
}

type workflowEditorData struct {
	Workflow  workflow.Workflow
	Lanes     []workflowLaneView
	Statuses  []models.Status
	Projects  []*models.Project
	Assigned  []*models.Project
	CanEdit   bool
	CanAssign bool
}

func (h *Handler) ProjectsPage(w http.ResponseWriter, r *http.Request) {
	user, wsID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	projects, err := h.Store.ProjectsByWorkspace(r.Context(), wsID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	workflows, err := h.Store.ListWorkflows(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	workflowNames := make(map[string]string, len(workflows))
	for _, item := range workflows {
		workflowNames[item.ID] = item.Name
	}
	boards, err := h.Store.BoardsByWorkspace(r.Context(), wsID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	data := projectsPageData{Projects: make([]projectDirectoryCard, 0, len(projects))}
	for _, project := range projects {
		issueCount, err := h.Store.IssueCountByProject(r.Context(), wsID, project.ID, user.ID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		workflowName := workflowNames[project.WorkflowID]
		if workflowName == "" {
			workflowName = workflow.Default().Name
		}
		card := projectDirectoryCard{Project: project, IssueCount: issueCount, WorkflowName: workflowName}
		for _, board := range boards {
			if board.ProjectID == project.ID {
				card.Boards = append(card.Boards, board)
				if card.PrimaryBoard == nil {
					card.PrimaryBoard = board
				}
			}
		}
		data.Projects = append(data.Projects, card)
	}
	h.writeWorkspacePage(w, r, "page_projects", user, wsID, data, "projects", "")
}

func (h *Handler) ProjectOverview(w http.ResponseWriter, r *http.Request, idOrKey string) {
	user, wsID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	project, err := h.Store.ProjectByIDOrKey(r.Context(), wsID, idOrKey)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	issues, err := h.Store.IssuesByProject(r.Context(), wsID, project.ID, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	allBoards, err := h.Store.BoardsByWorkspace(r.Context(), wsID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	boards := make([]*models.Board, 0)
	for _, board := range allBoards {
		if board.ProjectID == project.ID {
			boards = append(boards, board)
		}
	}
	wf, err := h.Store.WorkflowForProject(r.Context(), project.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var primaryBoard *models.Board
	if len(boards) > 0 {
		primaryBoard = boards[0]
	}
	if len(issues) > 10 {
		issues = issues[:10]
	}
	h.writeWorkspacePage(w, r, "page_project_overview", user, wsID, projectOverviewData{
		Project: project, Issues: issues, Boards: boards, PrimaryBoard: primaryBoard, Workflow: wf,
	}, "project-overview", project.ID)
}

func (h *Handler) PeoplePage(w http.ResponseWriter, r *http.Request) {
	user, wsID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	people, err := h.Store.MembersByWorkspace(r.Context(), wsID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_people", user, wsID, peoplePageData{People: people}, "people", "")
}

func (h *Handler) SelfProfile(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/people/"+user.ID, http.StatusSeeOther)
}

func (h *Handler) ProfilePage(w http.ResponseWriter, r *http.Request, accountID string) {
	user, wsID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	profile, err := h.Store.MemberByID(r.Context(), wsID, accountID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	assigned, err := h.Store.IssuesAssignedToUser(r.Context(), wsID, profile.ID, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	reported, err := h.Store.IssuesReportedByUser(r.Context(), wsID, profile.ID, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_profile", user, wsID, profilePageData{
		Profile: profile, Self: profile.ID == user.ID, Assigned: assigned, Reported: reported,
	}, "people", "")
}

func (h *Handler) WorkflowsPage(w http.ResponseWriter, r *http.Request) {
	user, wsID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	items, err := h.Store.ListWorkflows(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	projects, err := h.Store.ProjectsByWorkspace(r.Context(), wsID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	cards := make([]workflowDirectoryCard, 0, len(items))
	for _, item := range items {
		card := workflowDirectoryCard{Workflow: item}
		for _, project := range projects {
			if project.WorkflowID == item.ID || (project.WorkflowID == "" && item.ID == workflow.Default().ID) {
				card.Projects = append(card.Projects, project)
			}
		}
		cards = append(cards, card)
	}
	admin, _ := h.Store.IsAdmin(r.Context(), wsID, user.ID)
	h.writeWorkspacePage(w, r, "page_workflows", user, wsID, workflowsPageData{Workflows: cards, CanCreate: admin}, "workflows", "")
}

func (h *Handler) WorkflowPage(w http.ResponseWriter, r *http.Request, id string) {
	user, wsID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	wf, err := h.Store.WorkflowByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	statuses, err := h.Store.AllStatuses(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	sort.SliceStable(statuses, func(i, j int) bool {
		return workflowStatusOrder(statuses[i].Category) < workflowStatusOrder(statuses[j].Category)
	})
	projects, err := h.Store.ProjectsByWorkspace(r.Context(), wsID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	statusByID := make(map[string]models.Status, len(statuses))
	for _, status := range statuses {
		statusByID[status.ID] = status
	}
	lanes := make([]workflowLaneView, 0, len(statuses))
	for _, status := range statuses {
		lane := workflowLaneView{Status: status}
		for _, transition := range wf.Transitions {
			if containsValue(transition.From, status.ID) {
				lane.Transitions = append(lane.Transitions, workflowTransitionView{ID: transition.ID, Name: transition.Name, To: statusByID[transition.To]})
			}
		}
		lanes = append(lanes, lane)
	}
	assigned := make([]*models.Project, 0)
	for _, project := range projects {
		if project.WorkflowID == wf.ID || (project.WorkflowID == "" && wf.ID == workflow.Default().ID) {
			assigned = append(assigned, project)
		}
	}
	admin, _ := h.Store.IsAdmin(r.Context(), wsID, user.ID)
	h.writeWorkspacePage(w, r, "page_workflow", user, wsID, workflowEditorData{
		Workflow: wf, Lanes: lanes, Statuses: statuses, Projects: projects, Assigned: assigned,
		CanEdit: admin && wf.ID != workflow.Default().ID, CanAssign: admin,
	}, "workflows", "")
}

func (h *Handler) CreateWorkflow(w http.ResponseWriter, r *http.Request) {
	_, _, ok := h.requireAdminPage(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" || len(name) > 255 {
		http.Error(w, "workflow name is required (max 255 characters)", http.StatusBadRequest)
		return
	}
	wf := workflow.Default()
	wf.ID = store.NewID("workflow")
	wf.Name = name
	if err := h.Store.CreateWorkflow(r.Context(), wf); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/settings/workflows/"+wf.ID, http.StatusSeeOther)
}

func (h *Handler) AddWorkflowTransition(w http.ResponseWriter, r *http.Request, workflowID string) {
	_, _, ok := h.requireAdminPage(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	if workflowID == workflow.Default().ID {
		http.Error(w, "the built-in workflow is read-only; create a copy to edit it", http.StatusBadRequest)
		return
	}
	wf, err := h.Store.WorkflowByID(r.Context(), workflowID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	wf.Transitions = append(wf.Transitions, workflow.Transition{
		ID: store.NewID("transition"), Name: strings.TrimSpace(r.PostFormValue("name")),
		From: []string{r.PostFormValue("from")}, To: r.PostFormValue("to"),
	})
	if err := h.Store.CreateWorkflow(r.Context(), wf); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/settings/workflows/"+workflowID, http.StatusSeeOther)
}

func (h *Handler) DeleteWorkflowTransition(w http.ResponseWriter, r *http.Request, workflowID, transitionID string) {
	_, _, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if workflowID == workflow.Default().ID {
		http.Error(w, "the built-in workflow is read-only", http.StatusBadRequest)
		return
	}
	wf, err := h.Store.WorkflowByID(r.Context(), workflowID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	transitions := wf.Transitions[:0]
	for _, transition := range wf.Transitions {
		if transition.ID != transitionID {
			transitions = append(transitions, transition)
		}
	}
	if len(transitions) == len(wf.Transitions) || len(transitions) == 0 {
		http.Error(w, "workflow transition not found or workflow would become empty", http.StatusBadRequest)
		return
	}
	wf.Transitions = transitions
	if err := h.Store.CreateWorkflow(r.Context(), wf); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/settings/workflows/"+workflowID, http.StatusSeeOther)
}

func (h *Handler) AssignProjectWorkflow(w http.ResponseWriter, r *http.Request, workflowID string) {
	_, wsID, ok := h.requireAdminPage(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	if _, err := h.Store.WorkflowByID(r.Context(), workflowID); err != nil {
		http.NotFound(w, r)
		return
	}
	project, err := h.Store.ProjectByIDOrKey(r.Context(), wsID, r.PostFormValue("project"))
	if err != nil {
		http.Error(w, "project not found", http.StatusBadRequest)
		return
	}
	if err := h.Store.AssignWorkflowToProject(r.Context(), project.ID, workflowID); err != nil {
		http.Error(w, "could not assign workflow", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings/workflows/"+workflowID, http.StatusSeeOther)
}

func (h *Handler) pageContext(w http.ResponseWriter, r *http.Request) (*models.User, string, bool) {
	user := h.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return nil, "", false
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return nil, "", false
	}
	return user, wsID, true
}

func (h *Handler) requireAdminPage(w http.ResponseWriter, r *http.Request) (*models.User, string, bool) {
	user, wsID, ok := h.pageContext(w, r)
	if !ok {
		return nil, "", false
	}
	admin, err := h.Store.IsAdmin(r.Context(), wsID, user.ID)
	if err != nil || !admin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return nil, "", false
	}
	return user, wsID, true
}

func containsValue(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func workflowStatusOrder(category string) int {
	switch category {
	case "new":
		return 0
	case "indeterminate":
		return 1
	case "done":
		return 2
	default:
		return 3
	}
}
