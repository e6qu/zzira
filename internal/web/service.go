package web

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/workflow"
)

type serviceTransitionView struct{ ID, Name, To string }

type servicePageData struct {
	Desks        []models.ServiceDesk
	Desk         *models.ServiceDesk
	RequestTypes []models.ServiceRequestType
	RequestType  *models.ServiceRequestType
	Requests     []*models.ServiceRequest
	Request      *models.ServiceRequest
	Queues       []models.ServiceQueue
	Queue        *models.ServiceQueue
	Comments     []models.ServiceRequestComment
	Transitions  []serviceTransitionView
	CanAdmin     bool
	Error        string
	Summary      string
	Description  string
	Query        string
}

func (h *Handler) ServiceAgent(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	admin, err := h.Store.IsAdmin(r.Context(), workspaceID, user.ID)
	if err != nil || !admin {
		http.Error(w, "Service agent access is required.", http.StatusForbidden)
		return
	}
	desks, err := h.Store.ServiceDesks(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "Could not load service desks.", http.StatusInternalServerError)
		return
	}
	data := servicePageData{Desks: desks, CanAdmin: true}
	deskID := r.PathValue("desk")
	if deskID == "" && len(desks) > 0 {
		deskID = desks[0].ID
	}
	if deskID != "" {
		desk, err := h.Store.ServiceDesk(r.Context(), workspaceID, deskID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		data.Desk = desk
		data.Queues, err = h.Store.ServiceQueues(r.Context(), workspaceID, deskID)
		if err != nil {
			http.Error(w, "Could not load queues.", http.StatusInternalServerError)
			return
		}
		queueID := r.URL.Query().Get("queue")
		if queueID == "" && len(data.Queues) > 0 {
			queueID = data.Queues[0].ID
		}
		if queueID != "" {
			data.Queue, data.Requests, err = h.Store.ServiceQueueRequests(r.Context(), workspaceID, user.ID, deskID, queueID)
			if err != nil {
				http.NotFound(w, r)
				return
			}
		}
	}
	preferredProject := ""
	if data.Desk != nil {
		preferredProject = data.Desk.ProjectID
	}
	h.writeWorkspacePage(w, r, "page_service_agent", user, workspaceID, data, "service-agent", preferredProject)
}

func (h *Handler) ServiceAgentAssign(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	admin, err := h.Store.IsAdmin(r.Context(), workspaceID, user.ID)
	if err != nil || !admin {
		http.Error(w, "Service agent access is required.", http.StatusForbidden)
		return
	}
	request, err := h.Store.ServiceRequest(r.Context(), workspaceID, user.ID, r.PathValue("key"), true)
	if err != nil || request.ServiceDesk.ID != r.PathValue("desk") {
		http.NotFound(w, r)
		return
	}
	assignee := user.ID
	if r.FormValue("assignment") == "unassigned" {
		assignee = ""
	}
	if _, _, err := h.Commands.UpdateIssue(r.Context(), commands.UpdateIssueInput{ActorID: user.ID, WorkspaceID: workspaceID, IssueIDOrKey: request.Issue.ID, AssigneeID: &assignee}); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, "/service/agent/"+request.ServiceDesk.ID+"?queue="+r.FormValue("queue"))
}

func (h *Handler) ServiceHome(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	desks, err := h.Store.ServiceDesks(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "Could not load service portals.", http.StatusInternalServerError)
		return
	}
	requests, err := h.Store.ServiceRequests(r.Context(), workspaceID, user.ID, "", "", false)
	if err != nil {
		http.Error(w, "Could not load customer requests.", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_service_home", user, workspaceID, servicePageData{Desks: desks, Requests: requests}, "service", "")
}

func (h *Handler) ServicePortal(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	desk, err := h.Store.ServiceDesk(r.Context(), workspaceID, r.PathValue("desk"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	requestTypes, err := h.Store.ServiceRequestTypes(r.Context(), workspaceID, desk.ID, query)
	if err != nil {
		http.Error(w, "Could not load request types.", http.StatusInternalServerError)
		return
	}
	requests, err := h.Store.ServiceRequests(r.Context(), workspaceID, user.ID, desk.ID, "", false)
	if err != nil {
		http.Error(w, "Could not load requests.", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_service_portal", user, workspaceID, servicePageData{Desk: desk, RequestTypes: requestTypes, Requests: requests, Query: query}, "service", desk.ProjectID)
}

func (h *Handler) ServiceRequestForm(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	desk, err := h.Store.ServiceDesk(r.Context(), workspaceID, r.PathValue("desk"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	requestType, err := h.Store.ServiceRequestType(r.Context(), workspaceID, desk.ID, r.PathValue("requestType"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data := servicePageData{Desk: desk, RequestType: requestType}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		if !parseForm(w, r) {
			return
		}
		data.Summary, data.Description = strings.TrimSpace(r.PostFormValue("summary")), r.PostFormValue("description")
		request, err := h.Commands.CreateServiceRequest(r.Context(), commands.CreateServiceRequestInput{ActorID: user.ID, WorkspaceID: workspaceID, ServiceDeskID: desk.ID, RequestTypeID: requestType.ID, Channel: "portal", Summary: data.Summary, Description: data.Description})
		if err == nil {
			redirectLocal(w, r, "/service/requests/"+request.Issue.Key)
			return
		}
		data.Error, status = err.Error(), http.StatusBadRequest
	}
	h.writeWorkspacePageStatus(w, r, "page_service_request_form", user, workspaceID, data, "service", desk.ProjectID, status)
}

func (h *Handler) serviceRequestForPage(r *http.Request, workspaceID, userID, issueIDOrKey string) (*models.ServiceRequest, bool, error) {
	admin, err := h.Store.IsAdmin(r.Context(), workspaceID, userID)
	if err != nil {
		return nil, false, err
	}
	request, err := h.Store.ServiceRequest(r.Context(), workspaceID, userID, issueIDOrKey, admin)
	return request, admin, err
}

func (h *Handler) servicePageTransitions(r *http.Request, workspaceID, actorID string, request *models.ServiceRequest) ([]serviceTransitionView, error) {
	wf, err := h.Store.WorkflowForProjectAndIssueType(r.Context(), request.Issue.ProjectID, request.Issue.IssueType.ID)
	if err != nil {
		return nil, err
	}
	evaluation := workflow.ContextForIssue(actorID, request.Issue)
	evaluation.StatusHistory, err = h.Store.IssueStatusHistory(r.Context(), workspaceID, request.Issue.ID)
	if err != nil {
		return nil, err
	}
	evaluation.Transitions, err = h.Store.IssueTransitionHistory(r.Context(), workspaceID, request.Issue.ID)
	if err != nil {
		return nil, err
	}
	evaluation.ParentStatus, evaluation.ChildStatuses, err = h.Store.IssueHierarchyStatuses(r.Context(), workspaceID, request.Issue.ID)
	if err != nil {
		return nil, err
	}
	evaluation.FormsAttached, evaluation.FormsSubmitted, err = h.Store.IssueFormState(r.Context(), workspaceID, request.Issue.ID)
	if err != nil {
		return nil, err
	}
	views := make([]serviceTransitionView, 0)
	for _, transition := range wf.AvailableFor(request.Issue.Status.ID, evaluation) {
		status, err := h.Store.StatusByIDForProject(r.Context(), transition.To, request.Issue.ProjectID)
		if err != nil {
			return nil, err
		}
		views = append(views, serviceTransitionView{ID: transition.ID, Name: transition.Name, To: status.Name})
	}
	return views, nil
}

func (h *Handler) ServiceRequestPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	request, admin, err := h.serviceRequestForPage(r, workspaceID, user.ID, r.PathValue("key"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	comments, err := h.Store.ServiceRequestComments(r.Context(), request.Issue.ID, admin)
	if err != nil {
		http.Error(w, "Could not load request comments.", http.StatusInternalServerError)
		return
	}
	transitions, err := h.servicePageTransitions(r, workspaceID, user.ID, request)
	if err != nil {
		http.Error(w, "Could not load request transitions.", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_service_request", user, workspaceID, servicePageData{Request: request, Comments: comments, Transitions: transitions, CanAdmin: admin}, "service", request.Issue.ProjectID)
}

func (h *Handler) ServiceRequestComment(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	request, admin, err := h.serviceRequestForPage(r, workspaceID, user.ID, r.PathValue("key"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	public := true
	if admin {
		public = r.PostFormValue("public") == "true"
	}
	if _, err := h.Commands.AddServiceRequestComment(r.Context(), user.ID, workspaceID, request.Issue.ID, json.RawMessage(nil), r.PostFormValue("body"), public); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, "/service/requests/"+request.Issue.Key+"#conversation")
}

func (h *Handler) ServiceRequestTransition(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	request, _, err := h.serviceRequestForPage(r, workspaceID, user.ID, r.PathValue("key"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := h.Commands.TransitionServiceRequest(r.Context(), user.ID, workspaceID, request.Issue.ID, r.PostFormValue("transition")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, "/service/requests/"+request.Issue.Key)
}
