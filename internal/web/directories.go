package web

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/authn"
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
	Projects  []projectDirectoryCard
	CanCreate bool
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
	Profile    *models.User
	Self       bool
	Assigned   []*models.Issue
	Reported   []*models.Issue
	Identities []profileIdentityView
	Saved      string
}

type profileIdentityView struct {
	ProviderKey string
	DisplayName string
	Issuer      string
	Subject     string
	Email       string
	CreatedAt   string
	Connected   bool
	CanUnlink   bool
}

type workflowDirectoryCard struct {
	Workflow workflow.Workflow
	Projects []*models.Project
}

type workflowsPageData struct {
	Workflows []workflowDirectoryCard
	Projects  []*models.Project
	CanCreate bool
}

type workflowTransitionView struct {
	ID           string
	Name         string
	To           models.Status
	ScreenFields []string
	RuleSummary  []string
}

type workflowNodeView struct {
	Status      models.Status
	X           int
	Y           int
	Transitions []workflowTransitionView
}

type workflowEdgeView struct {
	FromID string
	ToID   string
}

type workflowEditorData struct {
	Workflow  workflow.Workflow
	Nodes     []workflowNodeView
	Edges     []workflowEdgeView
	MapWidth  int
	MapHeight int
	Statuses  []models.Status
	Projects  []*models.Project
	Assigned  []*models.Project
	CanEdit   bool
	CanAssign bool
}

type statusDirectoryData struct {
	Items        []store.StatusUsage
	Projects     []*models.Project
	ProjectNames map[string]string
	CanEdit      bool
	Saved        string
	Blocked      string
}

type workflowSchemeCard struct {
	Scheme              workflow.Scheme
	DefaultWorkflowName string
	Projects            []*models.Project
}

type workflowSchemesData struct {
	Schemes   []workflowSchemeCard
	Workflows []workflow.Workflow
	CanCreate bool
}

type workflowSchemeMappingView struct {
	IssueType  models.IssueType
	WorkflowID string
}

type workflowSchemeEditorData struct {
	Scheme              workflow.Scheme
	DefaultWorkflowName string
	Workflows           []workflow.Workflow
	Mappings            []workflowSchemeMappingView
	Projects            []*models.Project
	Assigned            []*models.Project
	PreviewProject      *models.Project
	Impact              []workflowSchemeImpactView
	CanEdit             bool
	Saved               string
	Error               string
}

type workflowSchemeImpactView struct {
	IssueTypeID    string
	Status         models.Status
	IssueCount     int
	TargetStatuses []models.Status
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
	workflows, err := h.Store.ListWorkflows(r.Context(), wsID)
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
	data.CanCreate, err = h.Store.IsAdmin(r.Context(), wsID, user.ID)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
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
	data := profilePageData{Profile: profile, Self: profile.ID == user.ID, Assigned: assigned, Reported: reported, Saved: r.URL.Query().Get("saved")}
	if data.Self {
		identities, err := h.Store.OIDCIdentitiesByUser(r.Context(), user.ID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		byIssuer := make(map[string]store.OIDCIdentity, len(identities))
		for _, identity := range identities {
			byIssuer[identity.Issuer] = identity
		}
		for _, provider := range h.loginProviders() {
			identity, connected := byIssuer[provider.Issuer]
			view := profileIdentityView{
				ProviderKey: provider.Key, DisplayName: provider.DisplayName, Issuer: provider.Issuer,
				Subject: identity.Subject, Email: identity.Email, Connected: connected, CanUnlink: connected && len(identities) > 1,
			}
			if connected {
				view.CreatedAt = identity.CreatedAt.UTC().Format("2006-01-02 15:04 UTC")
			}
			data.Identities = append(data.Identities, view)
			delete(byIssuer, provider.Issuer)
		}
		for _, identity := range identities {
			if _, unknown := byIssuer[identity.Issuer]; !unknown {
				continue
			}
			data.Identities = append(data.Identities, profileIdentityView{
				DisplayName: "External provider", Issuer: identity.Issuer, Subject: identity.Subject, Email: identity.Email,
				CreatedAt: identity.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"), Connected: true,
			})
		}
	}
	h.writeWorkspacePage(w, r, "page_profile", user, wsID, data, "people", "")
}

func (h *Handler) UnlinkIdentityProvider(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	provider, _ := h.identityProvider(r)
	if provider == nil {
		http.NotFound(w, r)
		return
	}
	currentIssuer := ""
	if cookie, err := r.Cookie(sessionCookieName()); err == nil {
		_, currentIssuer, _ = h.Store.IdentityProviderSession(r.Context(), authn.SessionHash(cookie.Value))
	}
	if err := h.Store.UnlinkOIDCIdentity(r.Context(), user.ID, provider.issuer); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrAdminConflict) {
			status = http.StatusConflict
		} else if errors.Is(err, store.ErrAdminNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	if currentIssuer == provider.issuer {
		authn.ClearSessionCookie(w)
		http.Redirect(w, r, "/signed-out", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/people/"+url.PathEscape(user.ID)+"?saved="+url.QueryEscape(provider.displayName+" disconnected"), http.StatusSeeOther)
}

func (h *Handler) WorkflowsPage(w http.ResponseWriter, r *http.Request) {
	user, wsID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	items, err := h.Store.ListWorkflows(r.Context(), wsID)
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
	h.writeWorkspacePage(w, r, "page_workflows", user, wsID, workflowsPageData{Workflows: cards, Projects: projects, CanCreate: admin}, "workflows", "")
}

func (h *Handler) StatusesPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	items, err := h.Store.StatusDirectory(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	projects, err := h.Store.ProjectsByWorkspace(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	projectNames := make(map[string]string, len(projects))
	for _, project := range projects {
		projectNames[project.ID] = project.Name
	}
	admin, _ := h.Store.IsAdmin(r.Context(), workspaceID, user.ID)
	h.writeWorkspacePage(w, r, "page_statuses", user, workspaceID, statusDirectoryData{
		Items: items, Projects: projects, ProjectNames: projectNames, CanEdit: admin, Saved: r.URL.Query().Get("saved"), Blocked: r.URL.Query().Get("blocked"),
	}, "statuses", "")
}

func (h *Handler) WorkflowSchemesPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	schemes, err := h.Store.ListWorkflowSchemes(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	workflows, err := h.Store.ListGlobalWorkflows(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	workflowNames := make(map[string]string, len(workflows))
	for _, item := range workflows {
		workflowNames[item.ID] = item.Name
	}
	data := workflowSchemesData{Workflows: workflows}
	data.CanCreate, _ = h.Store.IsAdmin(r.Context(), workspaceID, user.ID)
	for _, scheme := range schemes {
		projects, err := h.Store.ProjectsForWorkflowScheme(r.Context(), workspaceID, scheme.ID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		data.Schemes = append(data.Schemes, workflowSchemeCard{Scheme: scheme, DefaultWorkflowName: workflowNames[scheme.DefaultWorkflowID], Projects: projects})
	}
	h.writeWorkspacePage(w, r, "page_workflow_schemes", user, workspaceID, data, "workflow-schemes", "")
}

func (h *Handler) WorkflowSchemePage(w http.ResponseWriter, r *http.Request, schemeID string) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	scheme, err := h.Store.WorkflowSchemeByID(r.Context(), workspaceID, schemeID, true)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	workflows, err := h.Store.ListGlobalWorkflows(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	issueTypes, err := h.Store.IssueTypes(r.Context())
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	projects, err := h.Store.ProjectsByWorkspace(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	assigned, err := h.Store.ProjectsForWorkflowScheme(r.Context(), workspaceID, schemeID)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	data := workflowSchemeEditorData{Scheme: scheme, Workflows: workflows, Projects: projects, Assigned: assigned, Saved: r.URL.Query().Get("saved"), Error: r.URL.Query().Get("error")}
	data.CanEdit, _ = h.Store.IsAdmin(r.Context(), workspaceID, user.ID)
	for _, item := range workflows {
		if item.ID == scheme.DefaultWorkflowID {
			data.DefaultWorkflowName = item.Name
		}
	}
	for _, issueType := range issueTypes {
		data.Mappings = append(data.Mappings, workflowSchemeMappingView{IssueType: issueType, WorkflowID: scheme.IssueTypeMappings[issueType.ID]})
	}
	if projectID := r.URL.Query().Get("project"); projectID != "" {
		project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, projectID)
		if err != nil {
			data.Error = "Project not found."
		} else {
			data.PreviewProject = project
			impacts, impactErr := h.Store.WorkflowSchemeImpact(r.Context(), workspaceID, project.ID, schemeID, false)
			err = impactErr
			if err != nil {
				http.Error(w, "internal error", 500)
				return
			}
			statuses, err := h.Store.StatusesForProject(r.Context(), workspaceID, project.ID, true)
			if err != nil {
				http.Error(w, "internal error", 500)
				return
			}
			for _, impact := range impacts {
				allowed := make(map[string]bool)
				for _, transition := range impact.TargetWorkflow.Transitions {
					allowed[transition.To] = true
					for _, from := range transition.From {
						allowed[from] = true
					}
				}
				view := workflowSchemeImpactView{IssueTypeID: impact.IssueTypeID, Status: impact.Status, IssueCount: impact.IssueCount}
				for _, status := range statuses {
					if allowed[status.ID] {
						view.TargetStatuses = append(view.TargetStatuses, status)
					}
				}
				data.Impact = append(data.Impact, view)
			}
		}
	}
	h.writeWorkspacePage(w, r, "page_workflow_scheme", user, workspaceID, data, "workflow-schemes", "")
}

func (h *Handler) CreateWorkflowScheme(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	scheme, err := h.Store.CreateWorkflowScheme(r.Context(), workspaceID, user.ID, workflow.Scheme{Name: r.PostFormValue("name"), Description: r.PostFormValue("description"), DefaultWorkflowID: r.PostFormValue("default_workflow")})
	if err != nil {
		statusAdminError(w, err)
		return
	}
	http.Redirect(w, r, "/settings/workflow-schemes/"+url.PathEscape(scheme.ID), http.StatusSeeOther)
}

func (h *Handler) SaveWorkflowSchemeDraft(w http.ResponseWriter, r *http.Request, schemeID string) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	scheme, err := h.Store.WorkflowSchemeByID(r.Context(), workspaceID, schemeID, true)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	scheme.Name, scheme.Description, scheme.DefaultWorkflowID = r.PostFormValue("name"), r.PostFormValue("description"), r.PostFormValue("default_workflow")
	scheme.IssueTypeMappings = make(map[string]string)
	issueTypes, err := h.Store.IssueTypes(r.Context())
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	for _, issueType := range issueTypes {
		if workflowID := r.PostFormValue("issue_type_" + issueType.ID); workflowID != "" {
			scheme.IssueTypeMappings[issueType.ID] = workflowID
		}
	}
	if err := h.Store.SaveWorkflowSchemeDraft(r.Context(), workspaceID, user.ID, scheme); err != nil {
		statusAdminError(w, err)
		return
	}
	http.Redirect(w, r, "/settings/workflow-schemes/"+url.PathEscape(schemeID)+"?saved="+url.QueryEscape("Draft saved"), http.StatusSeeOther)
}

func (h *Handler) FinishWorkflowSchemeDraft(w http.ResponseWriter, r *http.Request, schemeID string) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	action := r.PostFormValue("action")
	var err error
	if action == "publish" {
		err = h.Store.PublishWorkflowSchemeDraft(r.Context(), workspaceID, user.ID, schemeID)
	} else if action == "discard" {
		err = h.Store.DiscardWorkflowSchemeDraft(r.Context(), workspaceID, user.ID, schemeID)
	} else {
		http.Error(w, "action must be publish or discard", 400)
		return
	}
	if err != nil {
		if errors.Is(err, store.ErrAdminConflict) {
			http.Redirect(w, r, "/settings/workflow-schemes/"+url.PathEscape(schemeID)+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		statusAdminError(w, err)
		return
	}
	http.Redirect(w, r, "/settings/workflow-schemes/"+url.PathEscape(schemeID)+"?saved="+url.QueryEscape("Scheme "+map[string]string{"publish": "published", "discard": "draft discarded"}[action]), http.StatusSeeOther)
}

func (h *Handler) AssignWorkflowScheme(w http.ResponseWriter, r *http.Request, schemeID string) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	projectID := r.PostFormValue("project")
	project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, projectID)
	if err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}
	impacts, err := h.Store.WorkflowSchemeImpact(r.Context(), workspaceID, project.ID, schemeID, false)
	if err != nil {
		statusAdminError(w, err)
		return
	}
	mappings := make([]store.WorkflowStatusMapping, 0, len(impacts))
	for _, impact := range impacts {
		mappings = append(mappings, store.WorkflowStatusMapping{
			IssueTypeID: impact.IssueTypeID, OldStatusID: impact.Status.ID,
			NewStatusID: r.PostFormValue("mapping_" + impact.IssueTypeID + "_" + impact.Status.ID),
		})
	}
	if err := h.Store.SwitchWorkflowScheme(r.Context(), workspaceID, user.ID, project.ID, schemeID, mappings); err != nil {
		if errors.Is(err, store.ErrAdminConflict) {
			http.Redirect(w, r, "/settings/workflow-schemes/"+url.PathEscape(schemeID)+"?project="+url.QueryEscape(projectID)+"&error="+url.QueryEscape("Assignment blocked until incompatible statuses are migrated."), http.StatusSeeOther)
			return
		}
		statusAdminError(w, err)
		return
	}
	http.Redirect(w, r, "/settings/workflow-schemes/"+url.PathEscape(schemeID)+"?saved="+url.QueryEscape("Project assigned"), http.StatusSeeOther)
}

func statusForm(r *http.Request) models.Status {
	return models.Status{
		ID: r.PostFormValue("id"), Name: r.PostFormValue("name"),
		Description: r.PostFormValue("description"), Category: r.PostFormValue("category"), ProjectID: r.PostFormValue("project"),
	}
}

func statusAdminError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	if errors.Is(err, store.ErrAdminValidation) {
		code = http.StatusBadRequest
	} else if errors.Is(err, store.ErrAdminConflict) {
		code = http.StatusConflict
	} else if errors.Is(err, store.ErrAdminNotFound) {
		code = http.StatusNotFound
	}
	http.Error(w, err.Error(), code)
}

func (h *Handler) CreateStatus(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	status, err := h.Store.CreateStatus(r.Context(), workspaceID, user.ID, statusForm(r))
	if err != nil {
		statusAdminError(w, err)
		return
	}
	http.Redirect(w, r, "/settings/statuses?saved="+url.QueryEscape(status.Name+" created"), http.StatusSeeOther)
}

func (h *Handler) UpdateStatus(w http.ResponseWriter, r *http.Request, statusID string) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	status := statusForm(r)
	status.ID = statusID
	if err := h.Store.UpdateStatus(r.Context(), workspaceID, user.ID, status); err != nil {
		statusAdminError(w, err)
		return
	}
	http.Redirect(w, r, "/settings/statuses?saved="+url.QueryEscape(status.Name+" updated"), http.StatusSeeOther)
}

func (h *Handler) DeleteStatus(w http.ResponseWriter, r *http.Request, statusID string) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if err := h.Store.DeleteStatus(r.Context(), workspaceID, user.ID, statusID); err != nil {
		if errors.Is(err, store.ErrAdminConflict) {
			http.Redirect(w, r, "/settings/statuses?blocked="+url.QueryEscape(statusID), http.StatusSeeOther)
			return
		}
		statusAdminError(w, err)
		return
	}
	http.Redirect(w, r, "/settings/statuses?saved="+url.QueryEscape("Status deleted"), http.StatusSeeOther)
}

func (h *Handler) WorkflowPage(w http.ResponseWriter, r *http.Request, id string) {
	user, wsID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	wf, err := h.Store.WorkflowDraftByID(r.Context(), wsID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var statuses []models.Status
	if wf.ProjectID == "" {
		statuses, err = h.Store.StatusesForWorkspace(r.Context(), wsID)
	} else {
		statuses, err = h.Store.StatusesForProject(r.Context(), wsID, wf.ProjectID, true)
	}
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
	if wf.ProjectID != "" {
		projectScoped := projects[:0]
		for _, project := range projects {
			if project.ID == wf.ProjectID {
				projectScoped = append(projectScoped, project)
			}
		}
		projects = projectScoped
	}
	nodes, edges, mapWidth, mapHeight := workflowDesignerMap(wf, statuses)
	assigned := make([]*models.Project, 0)
	for _, project := range projects {
		if project.WorkflowID == wf.ID || (project.WorkflowID == "" && wf.ID == workflow.Default().ID) {
			assigned = append(assigned, project)
		}
	}
	admin, _ := h.Store.IsAdmin(r.Context(), wsID, user.ID)
	h.writeWorkspacePage(w, r, "page_workflow", user, wsID, workflowEditorData{
		Workflow: wf, Nodes: nodes, Edges: edges, MapWidth: mapWidth, MapHeight: mapHeight, Statuses: statuses, Projects: projects, Assigned: assigned,
		CanEdit: admin && wf.ID != workflow.Default().ID, CanAssign: admin,
	}, "workflows", "")
}

func (h *Handler) SaveWorkflowLayout(w http.ResponseWriter, r *http.Request, workflowID string) {
	_, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	if workflowID == workflow.Default().ID {
		http.Error(w, "the built-in workflow is read-only", http.StatusBadRequest)
		return
	}
	x, xErr := strconv.ParseFloat(r.PostFormValue("x"), 64)
	y, yErr := strconv.ParseFloat(r.PostFormValue("y"), 64)
	if xErr != nil || yErr != nil || math.IsNaN(x) || math.IsInf(x, 0) || math.IsNaN(y) || math.IsInf(y, 0) || x < 0 || x > 10000 || y < 0 || y > 10000 {
		http.Error(w, "workflow coordinates must be between 0 and 10000", http.StatusBadRequest)
		return
	}
	wf, err := h.Store.WorkflowDraftByID(r.Context(), workspaceID, workflowID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	statusID := r.PostFormValue("status")
	referenced := workflowStatusIDs(wf)
	if !referenced[statusID] {
		http.Error(w, "status is not part of this workflow", http.StatusBadRequest)
		return
	}
	updated := false
	for index := range wf.Statuses {
		if wf.Statuses[index].StatusReference == statusID {
			wf.Statuses[index].Layout = &workflow.Layout{X: x, Y: y}
			updated = true
			break
		}
	}
	if !updated {
		wf.Statuses = append(wf.Statuses, workflow.StatusLayout{StatusReference: statusID, Layout: &workflow.Layout{X: x, Y: y}, Properties: map[string]string{}})
	}
	if err := h.Store.SaveWorkflowDraft(r.Context(), workspaceID, wf); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) CreateWorkflow(w http.ResponseWriter, r *http.Request) {
	_, wsID, ok := h.requireAdminPage(w, r)
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
	if projectID := r.PostFormValue("project"); projectID != "" {
		project, err := h.Store.ProjectByIDOrKey(r.Context(), wsID, projectID)
		if err != nil {
			http.Error(w, "workflow project does not exist", http.StatusBadRequest)
			return
		}
		wf.ProjectID = project.ID
	}
	if err := h.Store.CreateWorkflow(r.Context(), wsID, wf); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if wf.ProjectID != "" {
		if err := h.Store.AssignWorkflowToProject(r.Context(), wsID, wf.ProjectID, wf.ID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	http.Redirect(w, r, "/settings/workflows/"+wf.ID, http.StatusSeeOther)
}

func (h *Handler) AddWorkflowTransition(w http.ResponseWriter, r *http.Request, workflowID string) {
	_, wsID, ok := h.requireAdminPage(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	if workflowID == workflow.Default().ID {
		http.Error(w, "the built-in workflow is read-only; create a copy to edit it", http.StatusBadRequest)
		return
	}
	wf, err := h.Store.WorkflowDraftByID(r.Context(), wsID, workflowID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	transition := workflow.Transition{
		ID: store.NewID("transition"), Name: strings.TrimSpace(r.PostFormValue("name")),
		From: []string{r.PostFormValue("from")}, To: r.PostFormValue("to"),
	}
	if fields := r.PostForm["screen_field"]; len(fields) > 0 {
		transition.Screen = &workflow.Rule{ID: store.NewID("rule"), RuleKey: workflow.RuleTransitionScreen, Parameters: map[string]string{"fields": strings.Join(fields, ",")}}
	}
	conditions := make([]workflow.Rule, 0, 2)
	if restriction := r.PostFormValue("restriction"); restriction == "block-users" || restriction == "block-all" {
		mode := "users"
		if restriction == "block-all" {
			mode = "usersAndAPI"
		}
		conditions = append(conditions, workflow.Rule{
			ID: store.NewID("rule"), RuleKey: workflow.RuleRestrictFromAllUsers, Parameters: map[string]string{"restrictMode": mode},
		})
	} else if restriction != "" {
		conditions = append(conditions, workflow.Rule{
			ID: store.NewID("rule"), RuleKey: workflow.RuleRestrictIssueTransition, Parameters: map[string]string{"accountIds": restriction},
		})
	}
	if field := strings.TrimSpace(r.PostFormValue("condition_field")); field != "" {
		values, _ := json.Marshal([]string{r.PostFormValue("condition_value")})
		conditions = append(conditions, workflow.Rule{
			ID: store.NewID("rule"), RuleKey: workflow.RuleCheckFieldValue, Parameters: map[string]string{
				"fieldId": field, "fieldValue": string(values), "comparator": r.PostFormValue("condition_comparator"), "comparisonType": r.PostFormValue("condition_type"),
			},
		})
	}
	if statusID := r.PostFormValue("previous_status_condition"); statusID != "" {
		conditions = append(conditions, workflow.Rule{
			ID: store.NewID("rule"), RuleKey: workflow.RulePreviousStatusCondition, Parameters: map[string]string{
				"previousStatusIds": statusID, "mostRecentStatusOnly": formBool(r, "previous_status_recent"),
				"includeCurrentStatus": formBool(r, "previous_status_current"), "not": formBool(r, "previous_status_not"), "ignoreLoopTransitions": "true",
			},
		})
	}
	if fromStatusID := r.PostFormValue("separation_from"); fromStatusID != "" {
		conditions = append(conditions, workflow.Rule{
			ID: store.NewID("rule"), RuleKey: workflow.RuleSeparationOfDuties, Parameters: map[string]string{
				"fromStatusId": fromStatusID, "toStatusId": r.PostFormValue("separation_to"),
			},
		})
	}
	if len(conditions) > 0 {
		transition.Conditions = &workflow.ConditionGroup{Operation: "ALL", Conditions: conditions}
	}
	if fields := r.PostForm["required_field"]; len(fields) > 0 {
		transition.Validators = append(transition.Validators, workflow.Rule{
			ID: store.NewID("rule"), RuleKey: workflow.RuleValidateFieldValue,
			Parameters: map[string]string{"ruleType": "fieldRequired", "fieldsRequired": strings.Join(fields, ","), "errorMessage": "Complete the required transition fields."},
		})
	}
	if statusID := r.PostFormValue("previous_status_validator"); statusID != "" {
		transition.Validators = append(transition.Validators, workflow.Rule{
			ID: store.NewID("rule"), RuleKey: workflow.RulePreviousStatusValidator, Parameters: map[string]string{
				"previousStatusIds": statusID, "mostRecentStatusOnly": formBool(r, "previous_validator_recent"),
			},
		})
	}
	if effect := r.PostFormValue("assignee_effect"); effect != "" {
		transition.Actions = append(transition.Actions, workflow.Rule{
			ID: store.NewID("rule"), RuleKey: workflow.RuleChangeAssignee, Parameters: map[string]string{"type": effect},
		})
	}
	if field := r.PostFormValue("update_field"); field != "" {
		transition.Actions = append(transition.Actions, workflow.Rule{
			ID: store.NewID("rule"), RuleKey: workflow.RuleUpdateField, Parameters: map[string]string{
				"field": field, "value": r.PostFormValue("update_value"), "mode": r.PostFormValue("update_mode"),
			},
		})
	}
	if source := r.PostFormValue("copy_source"); source != "" {
		transition.Actions = append(transition.Actions, workflow.Rule{
			ID: store.NewID("rule"), RuleKey: workflow.RuleCopyFieldValue, Parameters: map[string]string{
				"sourceFieldKey": source, "targetFieldKey": r.PostFormValue("copy_target"), "issueSource": "SAME",
			},
		})
	}
	wf.Transitions = append(wf.Transitions, transition)
	if err := h.Store.SaveWorkflowDraft(r.Context(), wsID, wf); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/settings/workflows/"+workflowID, http.StatusSeeOther)
}

func formBool(r *http.Request, name string) string {
	if r.PostFormValue(name) != "" {
		return "true"
	}
	return "false"
}

func (h *Handler) DeleteWorkflowTransition(w http.ResponseWriter, r *http.Request, workflowID, transitionID string) {
	_, wsID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if workflowID == workflow.Default().ID {
		http.Error(w, "the built-in workflow is read-only", http.StatusBadRequest)
		return
	}
	wf, err := h.Store.WorkflowDraftByID(r.Context(), wsID, workflowID)
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
	if err := h.Store.SaveWorkflowDraft(r.Context(), wsID, wf); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/settings/workflows/"+workflowID, http.StatusSeeOther)
}

func (h *Handler) FinishWorkflowDraft(w http.ResponseWriter, r *http.Request, workflowID string) {
	user, wsID, ok := h.requireAdminPage(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	if workflowID == workflow.Default().ID {
		http.Error(w, "the built-in workflow is read-only", http.StatusBadRequest)
		return
	}
	action := r.PostFormValue("action")
	var err error
	switch action {
	case "publish":
		err = h.Store.PublishWorkflowDraft(r.Context(), wsID, user.ID, workflowID)
	case "discard":
		err = h.Store.DiscardWorkflowDraft(r.Context(), wsID, user.ID, workflowID)
	default:
		http.Error(w, "action must be publish or discard", http.StatusBadRequest)
		return
	}
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrAdminConflict) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	http.Redirect(w, r, "/settings/workflows/"+workflowID+"?saved="+url.QueryEscape("Workflow "+map[string]string{"publish": "published", "discard": "draft discarded"}[action]), http.StatusSeeOther)
}

func (h *Handler) AssignProjectWorkflow(w http.ResponseWriter, r *http.Request, workflowID string) {
	_, wsID, ok := h.requireAdminPage(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	if _, err := h.Store.WorkflowByID(r.Context(), wsID, workflowID); err != nil {
		http.NotFound(w, r)
		return
	}
	project, err := h.Store.ProjectByIDOrKey(r.Context(), wsID, r.PostFormValue("project"))
	if err != nil {
		http.Error(w, "project not found", http.StatusBadRequest)
		return
	}
	if err := h.Store.AssignWorkflowToProject(r.Context(), wsID, project.ID, workflowID); err != nil {
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

func workflowStatusIDs(wf workflow.Workflow) map[string]bool {
	ids := make(map[string]bool)
	for _, status := range wf.Statuses {
		ids[status.StatusReference] = true
	}
	for _, transition := range wf.Transitions {
		ids[transition.To] = true
		for _, from := range transition.From {
			ids[from] = true
		}
	}
	return ids
}

func workflowDesignerMap(wf workflow.Workflow, statuses []models.Status) ([]workflowNodeView, []workflowEdgeView, int, int) {
	referenced := workflowStatusIDs(wf)
	layouts := make(map[string]*workflow.Layout, len(wf.Statuses))
	for _, status := range wf.Statuses {
		layouts[status.StatusReference] = status.Layout
	}
	categoryX := map[string]int{"new": 36, "indeterminate": 326, "done": 616}
	categoryRows := map[string]int{}
	statusByID := make(map[string]models.Status, len(statuses))
	for _, status := range statuses {
		statusByID[status.ID] = status
	}
	nodes := make([]workflowNodeView, 0, len(referenced))
	maxX, maxY := 0, 0
	for _, status := range statuses {
		if !referenced[status.ID] {
			continue
		}
		x, y := categoryX[status.Category], 68+categoryRows[status.Category]*148
		categoryRows[status.Category]++
		if layout := layouts[status.ID]; layout != nil {
			x, y = int(math.Round(layout.X)), int(math.Round(layout.Y))
		}
		transitions := make([]workflowTransitionView, 0)
		for _, transition := range wf.Transitions {
			if containsValue(transition.From, status.ID) {
				rules := make([]string, 0, 3)
				if transition.Conditions != nil {
					rules = append(rules, "condition")
				}
				if len(transition.Validators) > 0 {
					rules = append(rules, "validator")
				}
				if len(transition.Actions) > 0 {
					rules = append(rules, "post-function")
				}
				transitions = append(transitions, workflowTransitionView{ID: transition.ID, Name: transition.Name, To: statusByID[transition.To], ScreenFields: transition.ScreenFields(), RuleSummary: rules})
			}
		}
		nodes = append(nodes, workflowNodeView{Status: status, X: x, Y: y, Transitions: transitions})
		maxX, maxY = max(maxX, x), max(maxY, y)
	}
	edges := make([]workflowEdgeView, 0, len(wf.Transitions))
	for _, transition := range wf.Transitions {
		for _, from := range transition.From {
			edges = append(edges, workflowEdgeView{FromID: from, ToID: transition.To})
		}
	}
	return nodes, edges, max(880, maxX+270), max(480, maxY+150)
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
