package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/workflow"
	"github.com/jackc/pgx/v5"
)

type serviceTransitionView struct{ ID, Name, To string }

type servicePageData struct {
	Desks                 []models.ServiceDesk
	Desk                  *models.ServiceDesk
	RequestTypes          []models.ServiceRequestType
	RequestType           *models.ServiceRequestType
	Requests              []*models.ServiceRequest
	Request               *models.ServiceRequest
	Queues                []models.ServiceQueue
	Queue                 *models.ServiceQueue
	Comments              []models.ServiceRequestComment
	Attachments           []models.ServiceRequestAttachment
	Approvals             []models.ServiceApproval
	Feedback              *models.ServiceRequestFeedback
	Participants          []*models.User
	Members               []*models.User
	Agents                map[string]bool
	Calendar              *models.ServiceCalendar
	SLAMetrics            []models.ServiceSLAMetric
	SLAs                  []models.ServiceSLA
	Customers             []*models.User
	Organizations         []models.ServiceOrganization
	DeskOrganizations     map[string]bool
	OrganizationUsers     map[string][]*models.User
	Transitions           []serviceTransitionView
	CanAdmin              bool
	CanAgent              bool
	CanManageParticipants bool
	CurrentUserID         string
	Subscribed            bool
	CanLeaveFeedback      bool
	Error                 string
	Summary               string
	Description           string
	Query                 string
}

func (h *Handler) ServiceAgent(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	agent, err := h.Store.IsAnyServiceAgent(r.Context(), workspaceID, user.ID)
	if err != nil || !agent {
		http.Error(w, "Service agent access is required.", http.StatusForbidden)
		return
	}
	desks, err := h.Store.ServiceDesksForAgent(r.Context(), workspaceID, user.ID)
	if err != nil {
		http.Error(w, "Could not load service desks.", http.StatusInternalServerError)
		return
	}
	admin, err := h.Store.IsAdmin(r.Context(), workspaceID, user.ID)
	if err != nil {
		http.Error(w, "Could not authorize service administration.", http.StatusInternalServerError)
		return
	}
	data := servicePageData{Desks: desks, CanAdmin: admin, CanAgent: true}
	deskID := r.PathValue("desk")
	if deskID == "" && len(desks) > 0 {
		deskID = desks[0].ID
	}
	if deskID != "" {
		allowed, err := h.Store.IsServiceAgent(r.Context(), workspaceID, deskID, user.ID)
		if err != nil {
			http.Error(w, "Could not authorize service desk access.", http.StatusInternalServerError)
			return
		}
		if !allowed {
			http.Error(w, "Service agent access is required.", http.StatusForbidden)
			return
		}
		desk, err := h.Store.ServiceDesk(r.Context(), workspaceID, deskID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		data.Desk = desk
		if admin {
			data.Members, err = h.Store.MembersByWorkspace(r.Context(), workspaceID)
			if err != nil {
				http.Error(w, "Could not load workspace members.", http.StatusInternalServerError)
				return
			}
			agents, err := h.Store.ServiceDeskAgents(r.Context(), workspaceID, deskID)
			if err != nil {
				http.Error(w, "Could not load service desk agents.", http.StatusInternalServerError)
				return
			}
			data.Agents = make(map[string]bool, len(agents))
			for _, assigned := range agents {
				data.Agents[assigned.ID] = true
			}
			data.Calendar, err = h.Store.ServiceCalendar(r.Context(), workspaceID, deskID)
			if err != nil {
				http.Error(w, "Could not load the service calendar.", http.StatusInternalServerError)
				return
			}
			data.SLAMetrics, err = h.Store.ServiceSLAMetrics(r.Context(), workspaceID, deskID)
			if err != nil {
				http.Error(w, "Could not load SLA goals.", http.StatusInternalServerError)
				return
			}
		}
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
		data.Customers, err = h.Store.ServiceDeskCustomers(r.Context(), workspaceID, deskID, "")
		if err != nil {
			http.Error(w, "Could not load service desk customers.", http.StatusInternalServerError)
			return
		}
		data.Organizations, err = h.Store.ServiceOrganizations(r.Context(), workspaceID, user.ID, "", true)
		if err != nil {
			http.Error(w, "Could not load customer organizations.", http.StatusInternalServerError)
			return
		}
		linkedOrganizations, err := h.Store.ServiceDeskOrganizations(r.Context(), workspaceID, deskID)
		if err != nil {
			http.Error(w, "Could not load service desk organizations.", http.StatusInternalServerError)
			return
		}
		data.DeskOrganizations = make(map[string]bool, len(linkedOrganizations))
		for _, organization := range linkedOrganizations {
			data.DeskOrganizations[organization.ID] = true
		}
		data.OrganizationUsers = make(map[string][]*models.User, len(data.Organizations))
		for _, organization := range data.Organizations {
			customers, err := h.Store.ServiceOrganizationUsers(r.Context(), workspaceID, organization.ID)
			if err != nil {
				http.Error(w, "Could not load organization customers.", http.StatusInternalServerError)
				return
			}
			data.OrganizationUsers[organization.ID] = customers
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
	agent, err := h.Store.IsServiceAgent(r.Context(), workspaceID, r.PathValue("desk"), user.ID)
	if err != nil || !agent {
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

func (h *Handler) ServiceAgentSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	if err := h.Commands.SetServiceDeskAgent(r.Context(), user.ID, workspaceID, r.PathValue("desk"), r.PostFormValue("accountId"), r.PostFormValue("enabled") == "true"); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, "/service/agent/"+r.PathValue("desk")+"#agents")
}

func (h *Handler) ServiceCustomerSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	deskID := r.PathValue("desk")
	switch r.PostFormValue("action") {
	case "access":
		if err := h.Commands.SetServiceDeskCustomerAccess(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("open") == "true"); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	case "invite":
		if _, err := h.Commands.InviteServiceDeskCustomer(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("email"), r.PostFormValue("displayName")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	case "add", "remove":
		customer, err := h.Store.ServiceCustomer(r.Context(), workspaceID, strings.TrimSpace(r.PostFormValue("customer")))
		if err != nil {
			http.Error(w, "Active customer was not found.", http.StatusBadRequest)
			return
		}
		if err := h.Commands.SetServiceDeskCustomers(r.Context(), user.ID, workspaceID, deskID, []string{customer.ID}, r.PostFormValue("action") == "add"); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "Customer action is invalid.", http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, "/service/agent/"+deskID+"#customers")
}

func (h *Handler) ServiceOrganizationSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	deskID, organizationID := r.PathValue("desk"), r.PostFormValue("organizationId")
	var err error
	switch r.PostFormValue("action") {
	case "create":
		_, err = h.Commands.CreateServiceOrganization(r.Context(), user.ID, workspaceID, r.PostFormValue("name"))
	case "link", "unlink":
		err = h.Commands.SetServiceDeskOrganization(r.Context(), user.ID, workspaceID, deskID, organizationID, r.PostFormValue("action") == "link")
	case "add-member":
		var customer *models.User
		customer, err = h.Store.ServiceCustomer(r.Context(), workspaceID, strings.TrimSpace(r.PostFormValue("customer")))
		if err == nil {
			err = h.Commands.SetServiceOrganizationUsers(r.Context(), user.ID, workspaceID, organizationID, []string{customer.ID}, true)
		}
	case "remove-member":
		err = h.Commands.SetServiceOrganizationUsers(r.Context(), user.ID, workspaceID, organizationID, []string{r.PostFormValue("accountId")}, false)
	case "delete":
		err = h.Commands.DeleteServiceOrganization(r.Context(), user.ID, workspaceID, organizationID)
	default:
		err = errors.New("organization action is invalid")
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, "/service/agent/"+deskID+"#organizations")
}

func parseServiceClock(value string) (int16, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, strconv.ErrSyntax
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, err
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil || hour < 0 || hour > 24 || minute < 0 || minute > 59 || (hour == 24 && minute != 0) {
		return 0, strconv.ErrSyntax
	}
	return int16(hour*60 + minute), nil
}

func (h *Handler) ServiceCalendarSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	startMinute, startErr := parseServiceClock(r.PostFormValue("start"))
	endMinute, endErr := parseServiceClock(r.PostFormValue("end"))
	if startErr != nil || endErr != nil {
		http.Error(w, "Working hours must use HH:MM.", http.StatusBadRequest)
		return
	}
	weekdays := make([]int16, 0, len(r.PostForm["weekday"]))
	for _, value := range r.PostForm["weekday"] {
		weekday, err := strconv.Atoi(value)
		if err != nil {
			http.Error(w, "Working days are invalid.", http.StatusBadRequest)
			return
		}
		weekdays = append(weekdays, int16(weekday))
	}
	if err := h.Commands.UpdateServiceCalendar(r.Context(), user.ID, workspaceID, r.PathValue("desk"), r.PostFormValue("name"), r.PostFormValue("timeZone"), weekdays, startMinute, endMinute); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, "/service/agent/"+r.PathValue("desk")+"#sla-settings")
}

func (h *Handler) ServiceSLASettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	goalMinutes, err := strconv.ParseInt(r.PostFormValue("goalMinutes"), 10, 64)
	if err != nil || goalMinutes < 1 {
		http.Error(w, "SLA goal must be a positive number of minutes.", http.StatusBadRequest)
		return
	}
	if err := h.Commands.UpdateServiceSLAMetric(r.Context(), user.ID, workspaceID, r.PathValue("desk"), r.PathValue("metric"), goalMinutes*time.Minute.Milliseconds()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, "/service/agent/"+r.PathValue("desk")+"#sla-settings")
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
	organizations, err := h.Store.ServiceOrganizations(r.Context(), workspaceID, user.ID, "", false)
	if err != nil {
		http.Error(w, "Could not load your customer organizations.", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_service_home", user, workspaceID, servicePageData{Desks: desks, Requests: requests, Organizations: organizations}, "service", "")
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
	canManage, err := h.Store.CanManageServiceRequest(r.Context(), workspaceID, userID, issueIDOrKey)
	if err != nil {
		return nil, false, err
	}
	request, err := h.Store.ServiceRequest(r.Context(), workspaceID, userID, issueIDOrKey, canManage)
	return request, canManage, err
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
	request, canManage, err := h.serviceRequestForPage(r, workspaceID, user.ID, r.PathValue("key"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	comments, err := h.Store.ServiceRequestComments(r.Context(), request.Issue.ID, canManage)
	if err != nil {
		http.Error(w, "Could not load request comments.", http.StatusInternalServerError)
		return
	}
	for i := range comments {
		values, loadErr := h.Store.ServiceCommentAttachments(r.Context(), request.Issue.ID, comments[i].Comment.ID, canManage)
		if loadErr != nil {
			http.Error(w, "Could not load request attachments.", http.StatusInternalServerError)
			return
		}
		for _, value := range values {
			comments[i].Attachments = append(comments[i].Attachments, value.Attachment)
		}
	}
	transitions, err := h.servicePageTransitions(r, workspaceID, user.ID, request)
	if err != nil {
		http.Error(w, "Could not load request transitions.", http.StatusInternalServerError)
		return
	}
	participants, err := h.Store.ServiceRequestParticipants(r.Context(), request.Issue.ID)
	if err != nil {
		http.Error(w, "Could not load request participants.", http.StatusInternalServerError)
		return
	}
	slas, err := h.Store.ServiceSLAs(r.Context(), workspaceID, request.Issue.ID, time.Now().UTC())
	if err != nil {
		http.Error(w, "Could not load request SLAs.", http.StatusInternalServerError)
		return
	}
	attachments, err := h.Store.ServiceRequestAttachments(r.Context(), request.Issue.ID, canManage)
	if err != nil {
		http.Error(w, "Could not load request attachments.", http.StatusInternalServerError)
		return
	}
	approvals, err := h.Store.ServiceApprovals(r.Context(), request.Issue.ID)
	if err != nil {
		http.Error(w, "Could not load request approvals.", http.StatusInternalServerError)
		return
	}
	subscribed, err := h.Store.ServiceRequestSubscription(r.Context(), request.Issue.ID, user.ID)
	if err != nil {
		http.Error(w, "Could not load notification subscription.", http.StatusInternalServerError)
		return
	}
	feedback, err := h.Store.ServiceRequestFeedback(r.Context(), request.Issue.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Could not load request feedback.", http.StatusInternalServerError)
		return
	}
	members := []*models.User{}
	if canManage {
		members, err = h.Store.MembersByWorkspace(r.Context(), workspaceID)
		if err != nil {
			http.Error(w, "Could not load eligible approvers.", http.StatusInternalServerError)
			return
		}
	}
	h.writeWorkspacePage(w, r, "page_service_request", user, workspaceID, servicePageData{Request: request, Comments: comments, Attachments: attachments, Approvals: approvals, Feedback: feedback, Participants: participants, Members: members, SLAs: slas, Transitions: transitions, CanAgent: canManage, CanManageParticipants: canManage || request.Customer.ID == user.ID, CurrentUserID: user.ID, Subscribed: subscribed, CanLeaveFeedback: request.Customer.ID == user.ID && request.Issue.Status.Category == "done"}, "service", request.Issue.ProjectID)
}

func (h *Handler) ServiceRequestParticipant(w http.ResponseWriter, r *http.Request) {
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
	customer, err := h.Store.ServiceCustomer(r.Context(), workspaceID, strings.TrimSpace(r.PostFormValue("participant")))
	if err != nil {
		http.Error(w, "Participant is not an active service customer.", http.StatusBadRequest)
		return
	}
	if _, err := h.Commands.UpdateServiceRequestParticipants(r.Context(), user.ID, workspaceID, request.Issue.ID, []string{customer.ID}, r.PostFormValue("action") == "remove"); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, "/service/requests/"+request.Issue.Key+"#participants")
}

func (h *Handler) ServiceRequestComment(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "Could not read the comment form.", http.StatusBadRequest)
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	request, canManage, err := h.serviceRequestForPage(r, workspaceID, user.ID, r.PathValue("key"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	public := true
	if canManage {
		public = r.PostFormValue("public") == "true"
	}
	files := r.MultipartForm.File["file"]
	if len(files) > 1 {
		http.Error(w, "Attach one file at a time.", http.StatusBadRequest)
		return
	}
	if len(files) == 1 {
		file, openErr := files[0].Open()
		if openErr != nil {
			http.Error(w, "Could not read the attachment.", http.StatusBadRequest)
			return
		}
		temporary, createErr := h.Commands.CreateServiceTemporaryAttachment(r.Context(), user.ID, workspaceID, request.ServiceDesk.ID, files[0].Filename, files[0].Header.Get("Content-Type"), file)
		closeErr := file.Close()
		if createErr != nil {
			http.Error(w, createErr.Error(), http.StatusBadRequest)
			return
		}
		if closeErr != nil {
			http.Error(w, "Could not close the attachment.", http.StatusInternalServerError)
			return
		}
		if _, _, err := h.Commands.CreateServiceAttachmentComment(r.Context(), user.ID, workspaceID, request.Issue.ID, []string{temporary.ID}, r.PostFormValue("body"), public); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	} else if _, err := h.Commands.AddServiceRequestComment(r.Context(), user.ID, workspaceID, request.Issue.ID, json.RawMessage(nil), r.PostFormValue("body"), public); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, "/service/requests/"+request.Issue.Key+"#conversation")
}

func (h *Handler) ServiceRequestApproval(w http.ResponseWriter, r *http.Request) {
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
	if _, err := h.Commands.CreateServiceApproval(r.Context(), user.ID, workspaceID, request.Issue.ID, r.PostFormValue("name"), []string{r.PostFormValue("approver")}); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, "/service/requests/"+request.Issue.Key+"#approvals")
}

func (h *Handler) ServiceRequestApprovalDecision(w http.ResponseWriter, r *http.Request) {
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
	if _, err := h.Commands.AnswerServiceApproval(r.Context(), user.ID, workspaceID, request.Issue.ID, r.PathValue("approval"), r.PostFormValue("decision")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, "/service/requests/"+request.Issue.Key+"#approvals")
}

func (h *Handler) ServiceRequestNotification(w http.ResponseWriter, r *http.Request) {
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
	if err := h.Commands.SetServiceRequestSubscription(r.Context(), user.ID, workspaceID, request.Issue.ID, r.PostFormValue("subscribed") == "true"); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, "/service/requests/"+request.Issue.Key+"#notifications")
}

func (h *Handler) ServiceRequestFeedback(w http.ResponseWriter, r *http.Request) {
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
	if r.PostFormValue("action") == "delete" {
		err = h.Commands.DeleteServiceRequestFeedback(r.Context(), user.ID, workspaceID, request.Issue.ID)
	} else {
		var rating int
		rating, err = strconv.Atoi(r.PostFormValue("rating"))
		if err == nil {
			_, err = h.Commands.PutServiceRequestFeedback(r.Context(), user.ID, workspaceID, request.Issue.ID, "csat", rating, r.PostFormValue("comment"))
		}
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, "/service/requests/"+request.Issue.Key+"#feedback")
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
