package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/e6qu/zzira/internal/store"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/wikimarkup"
	"github.com/e6qu/zzira/internal/workflow"
	"github.com/jackc/pgx/v5"
)

type serviceTransitionView struct{ ID, Name, To string }

// serviceSLAGoalOrder is where a conditional SLA goal stands in its order.
type serviceSLAGoalOrder struct{ First, Last bool }

type serviceRequestFieldChoice struct {
	models.ServiceRequestTypeField
	Enabled bool
}

type serviceRequestTypeFormView struct {
	RequestType models.ServiceRequestType
	Fields      []serviceRequestFieldChoice
	// ChoiceFields are the select and multi-select fields a field can be shown
	// for, with their options.
	ChoiceFields []serviceRequestChoiceField
	// AssetSchemas are the service project's Assets schemas, one of which an
	// Assets object field can offer.
	AssetSchemas []models.ServiceAssetSchema
}

// serviceRequestChoiceField is a select or multi-select field and its options.
type serviceRequestChoiceField struct {
	ID, Name string
	Options  []store.ServiceRequestFieldOption
}

type serviceRequestFieldValueView struct {
	Name, Value string
}

type serviceCalendarHolidayView struct {
	Day, Name string
}

type serviceReportDayView struct {
	Day          string
	Count, Width int
}

type servicePageData struct {
	// BulkStatuses are the statuses selected queue requests can move to,
	// DeskAgents the people requests can be assigned to, and BulkNotice and
	// BulkProblem what the last bulk action did.
	BulkStatuses     []string
	DeskAgents       []*models.User
	BulkNotice       string
	BulkProblem      string
	ReportActions    reportActions
	ReportCompare    bool
	ReportComparison map[string]string
	Desks            []models.ServiceDesk
	Desk             *models.ServiceDesk
	RequestTypes     []models.ServiceRequestType
	RequestType      *models.ServiceRequestType
	Requests         []*models.ServiceRequest
	Request          *models.ServiceRequest
	Queues           []models.ServiceQueue
	Queue            *models.ServiceQueue
	Comments         []models.ServiceRequestComment
	Attachments      []models.ServiceRequestAttachment
	Links            []models.IssueLinkView
	LinkTypes        []models.LinkType
	Approvals        []models.ServiceApproval
	Feedback         *models.ServiceRequestFeedback
	Participants     []*models.User
	Members          []*models.User
	Agents           map[string]bool
	Calendar         *models.ServiceCalendar
	Calendars        []models.ServiceCalendar
	CalendarHolidays []serviceCalendarHolidayView
	Report           *models.ServiceReport
	ReportDays       []serviceReportDayView
	ReportFilter     models.ServiceReportFilter
	ReportChannels   []string
	SLAMetrics       []models.ServiceSLAMetric
	// SLAConditions are the conditions an SLA's clock can start and stop on,
	// including entering each of the desk project's statuses.
	SLAConditions []models.ServiceSLACondition
	// CustomerNotifications are Jira's customer notifications with whether
	// the desk sends each.
	CustomerNotifications []serviceCustomerNotificationView
	SLAGoals              map[string][]models.ServiceSLAGoal
	// SLAGoalOrder says which conditional goals start and end their metric's order.
	SLAGoalOrder        map[string]serviceSLAGoalOrder
	SLAs                []models.ServiceSLA
	Customers           []*models.User
	Organizations       []models.ServiceOrganization
	DeskOrganizations   map[string]bool
	OrganizationUsers   map[string][]*models.User
	KnowledgeArticles   []models.ServiceKnowledgeArticle
	KnowledgeSpaces     []*models.WikiSpace
	KnowledgeSpaceLinks map[string]bool
	RequestTypeForms    []serviceRequestTypeFormView
	RequestTypeFields   []models.ServiceRequestTypeField
	// RequestTypeGroups are the portal's groups in the order the portal shows
	// them, each with its request types in order; UngroupedRequestTypes are
	// the request types in no group, which the portal does not show;
	// RequestTypeAdmin is what the settings form edits for each request type;
	// WorkTypes are the work types a new request type can be raised as;
	// PortalGroups is what the portal itself lists.
	RequestTypeGroups     []serviceRequestTypeGroupView
	UngroupedRequestTypes []models.ServiceRequestType
	RequestTypeAdmin      []serviceRequestTypeAdminView
	WorkTypes             []models.IssueType
	PortalGroups          []models.ServicePortalGroup
	// RequestTypeCount is how many request types the portal shows, so an empty
	// portal says so once rather than under every group.
	RequestTypeCount    int
	RequestFieldValues  []serviceRequestFieldValueView
	OperationsSettings  *models.ServiceOperationsSettings
	OperationsProfile   *models.ServiceOperationsProfile
	ChangeWindows       []models.ServiceChangeWindow
	ChangeConflicts     []models.ServiceChangeWindow
	DependencyEdges     []models.ServiceDependencyEdge
	DependencyNodeCount int
	IncidentUpdates     []models.ServiceIncidentUpdate
	EscalationSteps     []models.ServiceEscalationStep
	// IncidentRoles and IncidentStakeholders are a major incident's response
	// team and the people who follow its stakeholder updates.
	IncidentRoles        []models.ServiceIncidentRole
	IncidentStakeholders []models.ServiceIncidentStakeholder
	AssetInventory       *models.ServiceAssetInventory
	RequestAssets        []models.ServiceRequestAsset
	FieldValues          map[string]string
	// FieldOptions are the options each select field on a portal form offers,
	// and FieldChoices the answers a refused form keeps.
	FieldOptions map[string][]store.ServiceRequestFieldOption
	FieldChoices map[string][]string
	Transitions  []serviceTransitionView
	CanAdmin     bool
	CanSiteAdmin bool
	// HelpCenter is the help center's branding and announcement.
	HelpCenter          models.ServiceHelpCenter
	DeploymentGate      *store.ServiceDeploymentGate
	DeploymentProviders []*models.AppInstallation
	CanAgent            bool
	// CanAnnounce shows the portal announcement form to the desk's agents and
	// administrators while the portal lets agents add announcements.
	CanAnnounce           bool
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
	staff, err := h.Store.IsAnyServiceDeskStaff(r.Context(), workspaceID, user.ID)
	if err != nil || !staff {
		http.Error(w, "Service agent access is required.", http.StatusForbidden)
		return
	}
	desks, err := h.Store.ServiceDesksForAgent(r.Context(), workspaceID, user.ID)
	if err != nil {
		http.Error(w, "Could not load service desks.", http.StatusInternalServerError)
		return
	}
	siteAdmin, err := h.Store.IsAdmin(r.Context(), workspaceID, user.ID)
	if err != nil {
		http.Error(w, "Could not authorize service administration.", http.StatusInternalServerError)
		return
	}
	data := servicePageData{Desks: desks, CanSiteAdmin: siteAdmin, CanAgent: true}
	deskID := r.PathValue("desk")
	if deskID == "" && len(desks) > 0 {
		deskID = desks[0].ID
	}
	if deskID != "" {
		agent, err := h.Store.IsServiceAgent(r.Context(), workspaceID, deskID, user.ID)
		if err != nil {
			http.Error(w, "Could not authorize service desk access.", http.StatusInternalServerError)
			return
		}
		deskAdmin, err := h.Store.IsServiceDeskAdmin(r.Context(), workspaceID, deskID, user.ID)
		if err != nil {
			http.Error(w, "Could not authorize service desk access.", http.StatusInternalServerError)
			return
		}
		if !agent && !deskAdmin {
			http.Error(w, "Service agent access is required.", http.StatusForbidden)
			return
		}
		data.CanAdmin = deskAdmin
		desk, err := h.Store.ServiceDesk(r.Context(), workspaceID, deskID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		data.Desk = desk
		data.CanAnnounce = desk.AnnouncementsEnabled
		now := time.Now().UTC()
		data.ChangeWindows, err = h.Store.ServiceChangeCalendar(r.Context(), workspaceID, user.ID, deskID, now.AddDate(0, 0, -7), now.AddDate(0, 0, 90))
		if err != nil {
			http.Error(w, "Could not load the change calendar.", http.StatusInternalServerError)
			return
		}
		dependencyLinks, err := h.Store.ServiceDependencyLinks(r.Context(), workspaceID, user.ID, deskID)
		if err != nil {
			http.Error(w, "Could not load the dependency map.", http.StatusInternalServerError)
			return
		}
		dependencyNodes := map[string]bool{}
		for _, link := range dependencyLinks {
			from, fromErr := h.issueForUser(r, user, workspaceID, link.OutwardID)
			to, toErr := h.issueForUser(r, user, workspaceID, link.InwardID)
			if fromErr != nil || toErr != nil {
				continue
			}
			fromNode := serviceDependencyNode(from)
			toNode := serviceDependencyNode(to)
			data.DependencyEdges = append(data.DependencyEdges, models.ServiceDependencyEdge{ID: link.ID, Relationship: link.Outward, From: fromNode, To: toNode})
			dependencyNodes[from.ID], dependencyNodes[to.ID] = true, true
		}
		data.DependencyNodeCount = len(dependencyNodes)
		if deskAdmin {
			data.DeploymentGate, err = h.Store.ServiceDeploymentGate(r.Context(), workspaceID, deskID)
			if err != nil {
				http.Error(w, "Could not load deployment gating.", http.StatusInternalServerError)
				return
			}
			data.DeploymentProviders, err = h.Store.AppInstallations(r.Context(), workspaceID)
			if err != nil {
				http.Error(w, "Could not load deployment providers.", http.StatusInternalServerError)
				return
			}
			data.Members, err = h.Store.MembersByWorkspace(r.Context(), workspaceID)
			if err != nil {
				http.Error(w, "Could not load workspace members.", http.StatusInternalServerError)
				return
			}
			data.OperationsSettings, err = h.Store.ServiceOperationsSettings(r.Context(), workspaceID, deskID)
			if err != nil {
				http.Error(w, "Could not load operations settings.", http.StatusInternalServerError)
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
			data.Calendars, err = h.Store.ServiceCalendars(r.Context(), workspaceID, deskID)
			if err != nil {
				http.Error(w, "Could not load the service calendars.", http.StatusInternalServerError)
				return
			}
			for day, name := range data.Calendar.Holidays {
				data.CalendarHolidays = append(data.CalendarHolidays, serviceCalendarHolidayView{Day: day, Name: name})
			}
			sort.Slice(data.CalendarHolidays, func(i, j int) bool { return data.CalendarHolidays[i].Day < data.CalendarHolidays[j].Day })
			data.SLAMetrics, err = h.Store.ServiceSLAMetrics(r.Context(), workspaceID, deskID)
			if err != nil {
				http.Error(w, "Could not load SLA goals.", http.StatusInternalServerError)
				return
			}
			slaDesk, err := h.Store.ServiceDesk(r.Context(), workspaceID, deskID)
			if err != nil {
				http.Error(w, "Could not load SLA conditions.", http.StatusInternalServerError)
				return
			}
			slaStatuses, err := h.Store.StatusesForProject(r.Context(), workspaceID, slaDesk.ProjectID, true)
			if err != nil {
				http.Error(w, "Could not load SLA conditions.", http.StatusInternalServerError)
				return
			}
			for _, notification := range models.ServiceCustomerNotifications() {
				data.CustomerNotifications = append(data.CustomerNotifications, serviceCustomerNotificationView{ServiceCustomerNotification: notification, Enabled: slaDesk.CustomerNotificationEnabled(notification.Key)})
			}
			data.SLAConditions = models.ServiceSLAConditions()
			for _, status := range slaStatuses {
				condition := models.ServiceSLAEnteredStatus(status.ID)
				condition.Name = "Entered Status: " + status.Name
				data.SLAConditions = append(data.SLAConditions, condition)
			}
			data.SLAGoals = make(map[string][]models.ServiceSLAGoal, len(data.SLAMetrics))
			data.SLAGoalOrder = map[string]serviceSLAGoalOrder{}
			for _, metric := range data.SLAMetrics {
				goals, err := h.Store.ServiceSLAGoals(r.Context(), workspaceID, deskID, metric.ID)
				if err != nil {
					http.Error(w, "Could not load conditional SLA goals.", http.StatusInternalServerError)
					return
				}
				data.SLAGoals[metric.ID] = goals
				conditional := []string{}
				for _, goal := range goals {
					if goal.JQL != "" {
						conditional = append(conditional, goal.ID)
					}
				}
				for index, id := range conditional {
					data.SLAGoalOrder[id] = serviceSLAGoalOrder{First: index == 0, Last: index == len(conditional)-1}
				}
			}
			data.KnowledgeSpaces, err = h.Store.WikiSpaces(r.Context(), workspaceID, user.ID)
			if err != nil {
				http.Error(w, "Could not load knowledge spaces.", http.StatusInternalServerError)
				return
			}
			linkedSpaces, err := h.Store.ServiceDeskKnowledgeSpaces(r.Context(), workspaceID, user.ID, deskID)
			if err != nil {
				http.Error(w, "Could not load linked knowledge spaces.", http.StatusInternalServerError)
				return
			}
			data.KnowledgeSpaceLinks = make(map[string]bool, len(linkedSpaces))
			for _, space := range linkedSpaces {
				data.KnowledgeSpaceLinks[space.ID] = true
			}
			data.RequestTypes, err = h.Store.ServiceRequestTypes(r.Context(), workspaceID, deskID, "")
			if err != nil {
				http.Error(w, "Could not load request types.", http.StatusInternalServerError)
				return
			}
			if err := h.serviceRequestTypeAdmin(r, workspaceID, desk, &data); err != nil {
				http.Error(w, "Could not load request type groups.", http.StatusInternalServerError)
				return
			}
			customFields, err := h.Store.CustomFieldsForProject(r.Context(), desk.ProjectID)
			if err != nil {
				http.Error(w, "Could not load service form fields.", http.StatusInternalServerError)
				return
			}
			choiceFields := []serviceRequestChoiceField{}
			for _, customField := range customFields {
				if customField.Type != models.CustomFieldSelect && customField.Type != models.CustomFieldMultiSelect {
					continue
				}
				options, err := h.Store.ServiceRequestFieldOptions(r.Context(), workspaceID, deskID, customField.ID)
				if err != nil {
					http.Error(w, "Could not load service form options.", http.StatusInternalServerError)
					return
				}
				choiceFields = append(choiceFields, serviceRequestChoiceField{ID: customField.ID, Name: customField.Name, Options: options})
			}
			assetSchemas, err := h.Store.ServiceDeskAssetSchemas(r.Context(), workspaceID, deskID)
			if err != nil {
				http.Error(w, "Could not load Assets schemas.", http.StatusInternalServerError)
				return
			}
			for _, requestType := range data.RequestTypes {
				configured, err := h.Store.ServiceRequestTypeFields(r.Context(), workspaceID, deskID, requestType.ID)
				if err != nil {
					http.Error(w, "Could not load request type fields.", http.StatusInternalServerError)
					return
				}
				byID := make(map[string]models.ServiceRequestTypeField, len(configured))
				for _, field := range configured {
					byID[field.ID] = field
				}
				choices := []serviceRequestFieldChoice{
					{ServiceRequestTypeField: models.ServiceRequestTypeField{ID: "summary", RequestTypeID: requestType.ID, Name: "Summary", Type: models.CustomFieldText, Required: true, HelpText: requestType.HelpText}, Enabled: true},
					{ServiceRequestTypeField: models.ServiceRequestTypeField{ID: "description", RequestTypeID: requestType.ID, Name: "Description", Type: models.CustomFieldText, Description: "Describe the request."}, Enabled: byID["description"].ID != ""},
				}
				if field := byID["summary"]; field.ID != "" {
					choices[0].ServiceRequestTypeField = field
				}
				if field := byID["description"]; field.ID != "" {
					choices[1].ServiceRequestTypeField = field
				}
				for _, customField := range customFields {
					field, enabled := byID[customField.ID]
					if !enabled {
						field = models.ServiceRequestTypeField{ID: customField.ID, RequestTypeID: requestType.ID, Name: customField.Name, Type: customField.Type, Description: customField.Description, Custom: true}
					}
					choices = append(choices, serviceRequestFieldChoice{ServiceRequestTypeField: field, Enabled: enabled})
				}
				data.RequestTypeForms = append(data.RequestTypeForms, serviceRequestTypeFormView{RequestType: requestType, Fields: choices, ChoiceFields: choiceFields, AssetSchemas: assetSchemas})
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
			statuses := map[string]bool{}
			for _, request := range data.Requests {
				transitions, err := h.servicePageTransitions(r, workspaceID, user.ID, request)
				if err != nil {
					http.Error(w, "Could not load request transitions.", http.StatusInternalServerError)
					return
				}
				for _, transition := range transitions {
					statuses[transition.To] = true
				}
			}
			for status := range statuses {
				data.BulkStatuses = append(data.BulkStatuses, status)
			}
			sort.Strings(data.BulkStatuses)
			if data.DeskAgents, err = h.Store.ServiceDeskAgents(r.Context(), workspaceID, deskID); err != nil {
				http.Error(w, "Could not load service desk agents.", http.StatusInternalServerError)
				return
			}
			data.BulkNotice, data.BulkProblem = r.URL.Query().Get("bulk"), r.URL.Query().Get("bulkError")
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

func serviceDependencyNode(issue *models.Issue) models.ServiceDependencyNode {
	kind := issue.IssueType.Name
	for _, label := range issue.Labels {
		if label == "incident" || label == "problem" || label == "change" {
			kind = label
			break
		}
	}
	return models.ServiceDependencyNode{IssueID: issue.ID, IssueKey: issue.Key, Summary: issue.Summary, Kind: kind, Status: issue.Status}
}

func (h *Handler) ServiceReports(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	deskID := r.PathValue("desk")
	allowed, err := h.Store.IsServiceAgent(r.Context(), workspaceID, deskID, user.ID)
	if err != nil || !allowed {
		http.Error(w, "Service agent access is required.", http.StatusForbidden)
		return
	}
	desk, err := h.Store.ServiceDesk(r.Context(), workspaceID, deskID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	days := 30
	if requested, parseErr := strconv.Atoi(r.URL.Query().Get("days")); parseErr == nil && (requested == 7 || requested == 30 || requested == 90) {
		days = requested
	}
	filter := models.ServiceReportFilter{
		RequestTypeID: strings.TrimSpace(r.URL.Query().Get("requestType")),
		Channel:       strings.TrimSpace(r.URL.Query().Get("channel")),
		Status:        strings.TrimSpace(r.URL.Query().Get("status")),
	}
	if filter.Status != "" && filter.Status != "open" && filter.Status != "resolved" {
		http.Error(w, "Report status filter is invalid.", http.StatusBadRequest)
		return
	}
	requestTypes, err := h.Store.ServiceRequestTypes(r.Context(), workspaceID, deskID, "")
	if err != nil {
		http.Error(w, "Could not load report request types.", http.StatusInternalServerError)
		return
	}
	channels, err := h.Store.ServiceRequestChannels(r.Context(), workspaceID, deskID)
	if err != nil {
		http.Error(w, "Could not load report channels.", http.StatusInternalServerError)
		return
	}
	if filter.RequestTypeID != "" {
		valid := false
		for _, requestType := range requestTypes {
			valid = valid || requestType.ID == filter.RequestTypeID
		}
		if !valid {
			http.Error(w, "Report request type filter is invalid.", http.StatusBadRequest)
			return
		}
	}
	if filter.Channel != "" && !slices.Contains(channels, filter.Channel) {
		http.Error(w, "Report channel filter is invalid.", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	report, err := h.Store.ServiceReportFiltered(r.Context(), workspaceID, deskID, filter, days, now)
	if err != nil {
		http.Error(w, "Could not load service reports.", http.StatusInternalServerError)
		return
	}
	// A comparison counts the requests created in the window before, with the
	// same filters; open and resolved are their statuses now.
	compare := wantsComparison(r)
	var previous *models.ServiceReport
	var comparison map[string]string
	if compare {
		if previous, err = h.Store.ServiceReportFiltered(r.Context(), workspaceID, deskID, filter, days, previousPeriod(now, days)); err != nil {
			http.Error(w, "Could not load service reports.", http.StatusInternalServerError)
			return
		}
		comparison = map[string]string{
			"total":        compareCount(report.TotalRequests, previous.TotalRequests, days),
			"open":         compareCount(report.OpenRequests, previous.OpenRequests, days),
			"resolved":     compareCount(report.ResolvedRequests, previous.ResolvedRequests, days),
			"breached":     compareCount(report.BreachedRequests, previous.BreachedRequests, days),
			"satisfaction": compareRate(report.AverageSatisfaction, previous.AverageSatisfaction, previous.SatisfactionResponses, days, ""),
		}
	}
	maximum := 1
	for _, day := range report.Daily {
		maximum = max(maximum, day.Count)
	}
	views := make([]serviceReportDayView, 0, len(report.Daily))
	for _, day := range report.Daily {
		views = append(views, serviceReportDayView{Day: day.Day, Count: day.Count, Width: day.Count * 100 / maximum})
	}
	if wantsCSV(r) {
		header, rows := []string{"Date", "Requests created"}, serviceCSVRows(report)
		if compare {
			header, rows = withPeriods(header, serviceCSVRows(previous), rows)
		}
		writeReportCSV(w, desk.ProjectKey+" service requests", header, rows)
		return
	}
	actions, actionsErr := h.reportActions(r, workspaceID, user)
	if actionsErr != nil {
		http.Error(w, "Could not load report emails.", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_service_reports", user, workspaceID, servicePageData{Desk: desk, RequestTypes: requestTypes, Report: report, ReportDays: views, ReportFilter: filter, ReportChannels: channels, CanAgent: true, ReportActions: actions, ReportCompare: compare, ReportComparison: comparison}, "service", desk.ProjectID)
}

func serviceCSVRows(report *models.ServiceReport) [][]string {
	rows := make([][]string, 0, len(report.Daily))
	for _, day := range report.Daily {
		rows = append(rows, []string{day.Day, strconv.Itoa(day.Count)})
	}
	return rows
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
		http.Error(w, err.Error(), commandErrorStatus(err))
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
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/agent/"+r.PathValue("desk")+"#agents")
}

func (h *Handler) ServiceOperationsSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	deskID := r.PathValue("desk")
	threshold, thresholdErr := strconv.Atoi(r.PostFormValue("cabRiskThreshold"))
	reviewDays, reviewErr := strconv.Atoi(r.PostFormValue("reviewDueDays"))
	if thresholdErr != nil || reviewErr != nil {
		http.Error(w, "Operations settings are invalid.", http.StatusBadRequest)
		return
	}
	if err := h.Commands.UpdateServiceOperationsSettings(r.Context(), user.ID, workspaceID, deskID, threshold, reviewDays, r.PostForm["cabMember"]); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/agent/"+deskID+"#operations-settings")
}

func parseServiceDateTime(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse("2006-01-02T15:04", value)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func (h *Handler) ServiceOnCallSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	deskID := r.PathValue("desk")
	var err error
	if r.PostFormValue("action") == "delete" {
		err = h.Commands.DeleteServiceOnCallShift(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("shiftId"))
	} else {
		start, startErr := parseServiceDateTime(r.PostFormValue("startsAt"))
		end, endErr := parseServiceDateTime(r.PostFormValue("endsAt"))
		if startErr != nil || endErr != nil || start == nil || end == nil {
			http.Error(w, "On-call window is invalid.", http.StatusBadRequest)
			return
		}
		err = h.Commands.CreateServiceOnCallShift(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("accountId"), r.PostFormValue("label"), *start, *end)
	}
	if err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/agent/"+deskID+"#operations-settings")
}

func (h *Handler) ServiceEscalationSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	deskID := r.PathValue("desk")
	var err error
	if r.PostFormValue("action") == "delete" {
		err = h.Commands.DeleteServiceEscalationStep(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("stepId"))
	} else {
		delayMinutes, parseErr := strconv.Atoi(r.PostFormValue("delayMinutes"))
		if parseErr != nil {
			http.Error(w, "Escalation delay is invalid.", http.StatusBadRequest)
			return
		}
		err = h.Commands.CreateServiceEscalationStep(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("accountId"), delayMinutes)
	}
	if err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/agent/"+deskID+"#escalation-policy")
}

type serviceCustomerNotificationView struct {
	models.ServiceCustomerNotification
	Enabled bool
}

// ServicePortalSettings saves a portal's name, introduction text and logo.
func (h *Handler) ServicePortalSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	deskID := r.PathValue("desk")
	if err := h.Commands.UpdateServiceDeskPortal(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("name"), r.PostFormValue("description"), r.PostFormValue("logoUrl")); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	if err := h.Commands.SetServiceDeskAnnouncementsEnabled(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("announcementsEnabled") == "true"); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/agent/"+deskID+"#portal-settings")
}

// ServicePortalAnnouncement saves or clears the portal's announcement.
func (h *Handler) ServicePortalAnnouncement(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	deskID := r.PathValue("desk")
	if err := h.Commands.UpdateServiceDeskAnnouncement(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("title"), r.PostFormValue("message")); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/agent/"+deskID+"#portal-announcement")
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
			http.Error(w, err.Error(), commandErrorStatus(err))
			return
		}
	case "notification":
		if err := h.Commands.SetServiceDeskCustomerNotification(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("notification"), r.PostFormValue("enabled") == "true"); err != nil {
			http.Error(w, err.Error(), commandErrorStatus(err))
			return
		}
	case "feedback":
		if err := h.Commands.SetServiceDeskFeedbackEnabled(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("enabled") == "true"); err != nil {
			http.Error(w, err.Error(), commandErrorStatus(err))
			return
		}
	case "attachments":
		if err := h.Commands.SetServiceDeskAttachmentsEnabled(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("enabled") == "true"); err != nil {
			http.Error(w, err.Error(), commandErrorStatus(err))
			return
		}
	case "invite":
		if _, err := h.Commands.InviteServiceDeskCustomer(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("email"), r.PostFormValue("displayName")); err != nil {
			http.Error(w, err.Error(), commandErrorStatus(err))
			return
		}
	case "add", "remove":
		customer, err := h.Store.ServiceCustomer(r.Context(), workspaceID, strings.TrimSpace(r.PostFormValue("customer")))
		if err != nil {
			http.Error(w, "Active customer was not found.", http.StatusBadRequest)
			return
		}
		if err := h.Commands.SetServiceDeskCustomers(r.Context(), user.ID, workspaceID, deskID, []string{customer.ID}, r.PostFormValue("action") == "add"); err != nil {
			http.Error(w, err.Error(), commandErrorStatus(err))
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
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/agent/"+deskID+"#organizations")
}

// ServiceDeploymentGateSettings saves the desk's deployment gating.
func (h *Handler) ServiceDeploymentGateSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	deskID := r.PathValue("desk")
	if err := h.Commands.SetServiceDeploymentGate(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("provider"), r.PostForm["environmentType"]); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/agent/"+deskID+"#deployment-gating")
}

func (h *Handler) ServiceKnowledgeSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	deskID := r.PathValue("desk")
	if err := h.Commands.SetServiceDeskKnowledgeSpace(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("spaceId"), r.PostFormValue("linked") == "true"); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/agent/"+deskID+"#knowledge-base")
}

func (h *Handler) ServiceQueueSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	admin, err := h.Store.IsServiceDeskAdmin(r.Context(), workspaceID, r.PathValue("desk"), user.ID)
	if err != nil {
		http.Error(w, "Could not authorize service administration.", http.StatusInternalServerError)
		return
	}
	if !admin {
		http.Error(w, "Service desk administrator access is required.", http.StatusForbidden)
		return
	}
	if !parseForm(w, r) {
		return
	}
	deskID, queueID := r.PathValue("desk"), r.PostFormValue("queueId")
	switch r.PostFormValue("action") {
	case "create":
		queue, err := h.Commands.CreateServiceQueue(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("name"), r.PostFormValue("jql"))
		if err != nil {
			http.Error(w, err.Error(), commandErrorStatus(err))
			return
		}
		redirectLocal(w, r, "/service/agent/"+deskID+"?queue="+queue.ID+"#queue-settings")
	case "update":
		if err := h.Commands.UpdateServiceQueue(r.Context(), user.ID, workspaceID, deskID, queueID, r.PostFormValue("name"), r.PostFormValue("jql")); err != nil {
			http.Error(w, err.Error(), commandErrorStatus(err))
			return
		}
		redirectLocal(w, r, "/service/agent/"+deskID+"?queue="+queueID+"#queue-settings")
	case "delete":
		if err := h.Commands.DeleteServiceQueue(r.Context(), user.ID, workspaceID, deskID, queueID); err != nil {
			http.Error(w, err.Error(), commandErrorStatus(err))
			return
		}
		redirectLocal(w, r, "/service/agent/"+deskID+"#queue-settings")
	default:
		http.Error(w, "Choose a queue action.", http.StatusBadRequest)
	}
}

func (h *Handler) ServiceRequestTypeFieldSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	admin, err := h.Store.IsServiceDeskAdmin(r.Context(), workspaceID, r.PathValue("desk"), user.ID)
	if err != nil {
		http.Error(w, "Could not authorize service administration.", http.StatusInternalServerError)
		return
	}
	if !admin {
		http.Error(w, "Service desk administrator access is required.", http.StatusForbidden)
		return
	}
	if !parseForm(w, r) {
		return
	}
	required := make(map[string]bool, len(r.PostForm["requiredFieldId"]))
	for _, fieldID := range r.PostForm["requiredFieldId"] {
		required[fieldID] = true
	}
	hidden := make(map[string]bool, len(r.PostForm["hiddenFieldId"]))
	for _, fieldID := range r.PostForm["hiddenFieldId"] {
		hidden[fieldID] = true
	}
	fields := make([]models.ServiceRequestTypeField, 0, len(r.PostForm["fieldId"]))
	for _, fieldID := range r.PostForm["fieldId"] {
		field := models.ServiceRequestTypeField{ID: fieldID, Required: required[fieldID], HelpText: r.PostFormValue("help_" + fieldID), Hidden: hidden[fieldID]}
		// The form lists every choice field's options; only those of the chosen
		// field show this one.
		if condition := strings.TrimSpace(r.PostFormValue("condition_" + fieldID)); condition != "" {
			field.ConditionFieldID = condition
			options, err := h.Store.ServiceRequestFieldOptions(r.Context(), workspaceID, r.PathValue("desk"), condition)
			if err != nil {
				http.Error(w, "Could not load service form options.", http.StatusInternalServerError)
				return
			}
			offered := map[string]bool{}
			for _, option := range options {
				offered[option.ID] = true
			}
			for _, id := range r.PostForm["condition_options_"+fieldID] {
				if offered[id] {
					field.ConditionOptionIDs = append(field.ConditionOptionIDs, id)
				}
			}
		}
		field.AssetSchemaID = strings.TrimSpace(r.PostFormValue("asset_schema_" + fieldID))
		// A hidden field's preset is typed as the value a customer would give:
		// JSON when it parses, otherwise text.
		if preset := strings.TrimSpace(r.PostFormValue("preset_" + fieldID)); preset != "" {
			if json.Valid([]byte(preset)) {
				field.PresetValue = json.RawMessage(preset)
			} else if encoded, err := json.Marshal(preset); err == nil {
				field.PresetValue = encoded
			}
		}
		fields = append(fields, field)
	}
	deskID, requestTypeID := r.PathValue("desk"), r.PathValue("requestType")
	if err := h.Commands.SetServiceRequestTypeFields(r.Context(), user.ID, workspaceID, deskID, requestTypeID, fields); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/agent/"+deskID+"#request-forms")
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

// ServiceCalendars adds or removes the working hours a desk's SLA goals can be
// measured in, beside the default calendar the desk started with.
func (h *Handler) ServiceCalendars(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	deskID := r.PathValue("desk")
	var err error
	if r.PostFormValue("action") == "delete" {
		err = h.Commands.DeleteServiceCalendar(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("calendarId"))
	} else {
		startMinute, startErr := parseServiceClock(r.PostFormValue("start"))
		endMinute, endErr := parseServiceClock(r.PostFormValue("end"))
		if startErr != nil || endErr != nil {
			http.Error(w, "Working hours must use HH:MM.", http.StatusBadRequest)
			return
		}
		_, err = h.Commands.CreateServiceCalendar(r.Context(), user.ID, workspaceID, deskID,
			r.PostFormValue("name"), r.PostFormValue("timeZone"), serviceWeekdays(r), startMinute, endMinute)
	}
	if err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/agent/"+deskID+"#calendar")
}

// serviceWeekdays reads the ISO working days a calendar form submitted.
func serviceWeekdays(r *http.Request) []int16 {
	weekdays := make([]int16, 0, len(r.PostForm["weekday"]))
	for _, value := range r.PostForm["weekday"] {
		if len(value) == 1 && value[0] >= '1' && value[0] <= '7' {
			weekdays = append(weekdays, int16(value[0]-'0'))
		}
	}
	return weekdays
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
		var weekday int16
		switch value {
		case "1":
			weekday = 1
		case "2":
			weekday = 2
		case "3":
			weekday = 3
		case "4":
			weekday = 4
		case "5":
			weekday = 5
		case "6":
			weekday = 6
		case "7":
			weekday = 7
		default:
			http.Error(w, "Working days are invalid.", http.StatusBadRequest)
			return
		}
		weekdays = append(weekdays, weekday)
	}
	if err := h.Commands.UpdateServiceCalendar(r.Context(), user.ID, workspaceID, r.PathValue("desk"), r.PostFormValue("name"), r.PostFormValue("timeZone"), weekdays, startMinute, endMinute); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
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
	if err := h.Commands.UpdateServiceSLAMetric(r.Context(), user.ID, workspaceID, r.PathValue("desk"), r.PathValue("metric"), r.PostFormValue("pauseJql"), goalMinutes*time.Minute.Milliseconds()); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	if r.PostFormValue("conditions") != "" {
		if err := h.Commands.UpdateServiceSLAConditions(r.Context(), user.ID, workspaceID, r.PathValue("desk"), r.PathValue("metric"), r.PostForm["startCondition"], r.PostForm["stopCondition"]); err != nil {
			http.Error(w, err.Error(), commandErrorStatus(err))
			return
		}
	}
	redirectLocal(w, r, "/service/agent/"+r.PathValue("desk")+"#sla-settings")
}

// ServiceSLACreate adds a custom SLA to a service desk.
func (h *Handler) ServiceSLACreate(w http.ResponseWriter, r *http.Request) {
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
	if _, err := h.Commands.CreateServiceSLAMetric(r.Context(), user.ID, workspaceID, r.PathValue("desk"), r.PostFormValue("name"), goalMinutes*time.Minute.Milliseconds(), r.PostForm["startCondition"], r.PostForm["stopCondition"]); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/agent/"+r.PathValue("desk")+"#sla-settings")
}

// ServiceSLADelete removes a custom SLA from a service desk.
func (h *Handler) ServiceSLADelete(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if err := h.Commands.DeleteServiceSLAMetric(r.Context(), user.ID, workspaceID, r.PathValue("desk"), r.PathValue("metric")); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/agent/"+r.PathValue("desk")+"#sla-settings")
}

func (h *Handler) ServiceSLAGoalSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	admin, err := h.Store.IsServiceDeskAdmin(r.Context(), workspaceID, r.PathValue("desk"), user.ID)
	if err != nil {
		http.Error(w, "Could not authorize service administration.", http.StatusInternalServerError)
		return
	}
	if !admin {
		http.Error(w, "Service desk administrator access is required.", http.StatusForbidden)
		return
	}
	if !parseForm(w, r) {
		return
	}
	deskID, metricID, goalID := r.PathValue("desk"), r.PathValue("metric"), r.PostFormValue("goalId")
	action := r.PostFormValue("action")
	err = nil
	switch action {
	case "delete":
		err = h.Commands.DeleteServiceSLAGoal(r.Context(), user.ID, workspaceID, deskID, metricID, goalID)
	case "move":
		err = h.Commands.MoveServiceSLAGoal(r.Context(), user.ID, workspaceID, deskID, metricID, goalID, r.PostFormValue("direction"))
	default:
		goalMinutes, parseErr := strconv.ParseInt(r.PostFormValue("goalMinutes"), 10, 64)
		if parseErr != nil || goalMinutes < 1 {
			http.Error(w, "SLA goal must be a positive number of minutes.", http.StatusBadRequest)
			return
		}
		if action == "update" {
			err = h.Commands.UpdateServiceSLAGoal(r.Context(), user.ID, workspaceID, deskID, metricID, goalID, r.PostFormValue("name"), r.PostFormValue("jql"), r.PostFormValue("calendarId"), goalMinutes*time.Minute.Milliseconds())
		} else {
			_, err = h.Commands.CreateServiceSLAGoal(r.Context(), user.ID, workspaceID, deskID, metricID, r.PostFormValue("name"), r.PostFormValue("jql"), r.PostFormValue("calendarId"), goalMinutes*time.Minute.Milliseconds())
		}
	}
	if err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/agent/"+deskID+"#sla-settings")
}

func (h *Handler) ServiceCalendarHolidaySettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	deskID, day := r.PathValue("desk"), r.PostFormValue("day")
	// An empty calendar means the desk's default one.
	calendarID := r.PostFormValue("calendarId")
	var err error
	if r.PostFormValue("action") == "delete" {
		err = h.Commands.DeleteServiceCalendarHoliday(r.Context(), user.ID, workspaceID, deskID, calendarID, day)
	} else {
		err = h.Commands.UpsertServiceCalendarHoliday(r.Context(), user.ID, workspaceID, deskID, calendarID, day, r.PostFormValue("name"))
	}
	if err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/agent/"+deskID+"#sla-settings")
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
	center, err := h.Store.ServiceHelpCenter(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "Could not load the help center.", http.StatusInternalServerError)
		return
	}
	siteAdmin, err := h.Store.IsAdmin(r.Context(), workspaceID, user.ID)
	if err != nil {
		http.Error(w, "Could not authorize help center administration.", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_service_home", user, workspaceID, servicePageData{Desks: desks, Requests: requests, Organizations: organizations, HelpCenter: center, CanSiteAdmin: siteAdmin}, "service", "")
}

// ServiceHelpCenterSettings saves the help center's branding and announcement.
func (h *Handler) ServiceHelpCenterSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	center := models.ServiceHelpCenter{Name: r.PostFormValue("name"), HomeTitle: r.PostFormValue("homeTitle"), LogoURL: r.PostFormValue("logoUrl"), BannerURL: r.PostFormValue("bannerUrl"),
		BannerColour: r.PostFormValue("bannerColour"), BannerTextColour: r.PostFormValue("bannerTextColour"),
		NavigationBackgroundColour: r.PostFormValue("navigationBackgroundColour"), NavigationTextColour: r.PostFormValue("navigationTextColour"),
		AnnouncementTitle: r.PostFormValue("announcementTitle"), AnnouncementMessage: r.PostFormValue("announcementMessage")}
	if err := h.Commands.UpdateServiceHelpCenter(r.Context(), user.ID, workspaceID, center); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service#help-center-settings")
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
	// The portal lists request types under their groups, in the order the
	// desk's administrators arranged. A request type in no group is not shown
	// on the portal at all, as Jira Service Management hides it.
	portalGroups, err := h.Store.ServicePortalGroups(r.Context(), workspaceID, desk.ID, query)
	if err != nil {
		http.Error(w, "Could not load request types.", http.StatusInternalServerError)
		return
	}
	requests, err := h.Store.ServiceRequests(r.Context(), workspaceID, user.ID, desk.ID, "", false)
	if err != nil {
		http.Error(w, "Could not load requests.", http.StatusInternalServerError)
		return
	}
	shown := 0
	for _, group := range portalGroups {
		shown += len(group.RequestTypes)
	}
	data := servicePageData{Desk: desk, PortalGroups: portalGroups, Requests: requests, Query: query, RequestTypeCount: shown}
	if query != "" {
		admin, err := h.Store.IsAdmin(r.Context(), workspaceID, user.ID)
		if err != nil {
			http.Error(w, "Could not authorize knowledge access.", http.StatusInternalServerError)
			return
		}
		data.KnowledgeArticles, err = h.Store.ServiceKnowledgeArticles(r.Context(), workspaceID, user.ID, desk.ID, query, admin)
		if err != nil {
			http.Error(w, "Could not search knowledge articles.", http.StatusInternalServerError)
			return
		}
		for index := range data.KnowledgeArticles {
			data.KnowledgeArticles[index].Excerpt = serviceKnowledgeExcerpt(data.KnowledgeArticles[index].Body)
		}
	}
	h.writeWorkspacePage(w, r, "page_service_portal", user, workspaceID, data, "service", desk.ProjectID)
}

func serviceKnowledgeExcerpt(storage string) string {
	value, err := wikimarkup.Text(storage)
	if err != nil {
		return ""
	}
	runes := []rune(value)
	if len(runes) > 180 {
		return string(runes[:177]) + "..."
	}
	return value
}

func (h *Handler) ServiceKnowledgePage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	admin, err := h.Store.IsAdmin(r.Context(), workspaceID, user.ID)
	if err != nil {
		http.Error(w, "Could not authorize knowledge access.", http.StatusInternalServerError)
		return
	}
	article, err := h.Store.ServiceKnowledgeArticle(r.Context(), workspaceID, user.ID, r.PathValue("page"), admin)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.writeWorkspacePage(w, r, "page_service_knowledge", user, workspaceID, servicePageData{KnowledgeArticles: []models.ServiceKnowledgeArticle{*article}}, "service", "")
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
	fields, err := h.Store.ServiceRequestTypeFields(r.Context(), workspaceID, desk.ID, requestType.ID)
	if err != nil {
		http.Error(w, "Could not load request fields.", http.StatusInternalServerError)
		return
	}
	data := servicePageData{Desk: desk, RequestType: requestType, RequestTypeFields: fields, FieldValues: map[string]string{},
		FieldOptions: map[string][]store.ServiceRequestFieldOption{}, FieldChoices: map[string][]string{}}
	for _, field := range fields {
		switch field.Type {
		case models.CustomFieldSelect, models.CustomFieldMultiSelect, models.CustomFieldCascadingSelect:
			options, err := h.Store.ServiceRequestFieldOptions(r.Context(), workspaceID, desk.ID, field.ID)
			if err != nil {
				http.Error(w, "Could not load request field options.", http.StatusInternalServerError)
				return
			}
			data.FieldOptions[field.ID] = options
		default:
			if store.IsServicePortalPicker(field.Type) {
				choices, err := h.Store.ServicePortalPickerChoices(r.Context(), workspaceID, desk.ID, user.ID, field.Type, field.AssetSchemaID)
				if err != nil {
					http.Error(w, "Could not load request field options.", http.StatusInternalServerError)
					return
				}
				data.FieldOptions[field.ID] = choices
			}
		}
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		if !parseForm(w, r) {
			return
		}
		// A user picker names a member by email, so the portal never lists the
		// site's people.
		memberByEmail := func(email string) (string, error) {
			id, _, _, err := h.Store.UserByEmail(r.Context(), email)
			if err != nil {
				id, _, _, err = h.Store.UserByEmail(r.Context(), strings.ToLower(email))
			}
			if err == nil {
				if _, memberErr := h.Store.MemberByID(r.Context(), workspaceID, id); memberErr == nil {
					return id, nil
				}
			}
			return "", fmt.Errorf("No active member of this site uses %s.", email)
		}
		customFields := map[string]json.RawMessage{}
		var descriptionADF json.RawMessage
		chosen := map[string][]string{}
		for _, field := range fields {
			if field.Type == models.CustomFieldSelect || field.Type == models.CustomFieldMultiSelect {
				chosen[field.ID] = r.PostForm["field_"+field.ID]
			}
		}
		for _, field := range fields {
			// A hidden field takes its preset value, as REST request creation does.
			if field.Hidden {
				if len(field.PresetValue) == 0 || string(field.PresetValue) == "null" {
					continue
				}
				if field.ID == "description" {
					var text string
					if json.Unmarshal(field.PresetValue, &text) == nil {
						data.Description = text
					} else {
						descriptionADF = field.PresetValue
					}
				} else if field.Custom {
					customFields[field.ID] = field.PresetValue
				}
				continue
			}
			// A field the other answers keep hidden is neither required nor sent.
			if !field.ShownFor(chosen) {
				continue
			}
			submitted := r.PostForm["field_"+field.ID]
			child := r.PostFormValue("field_" + field.ID + "_child")
			data.FieldValues[field.ID] = r.PostFormValue("field_" + field.ID)
			data.FieldChoices[field.ID] = submitted
			if child != "" {
				data.FieldChoices[field.ID+"_child"] = []string{child}
			}
			// The first problem is reported, but every answer is still kept so a
			// refused form comes back as the customer filled it in.
			problem := ""
			switch field.ID {
			case "summary", "description":
				value := r.PostFormValue("field_" + field.ID)
				switch {
				case field.Required && strings.TrimSpace(value) == "":
					problem = field.Name + " is required."
				case field.ID == "summary":
					data.Summary = strings.TrimSpace(value)
				default:
					data.Description = value
				}
			default:
				encoded, present, err := encodeServicePortalField(field, submitted, child, data.FieldOptions[field.ID], memberByEmail)
				switch {
				case err != nil:
					problem = err.Error()
				case !present && field.Required:
					problem = field.Name + " is required."
				case present:
					customFields[field.ID] = encoded
				}
			}
			if data.Error == "" {
				data.Error = problem
			}
		}
		if data.Error != "" {
			status = http.StatusBadRequest
			h.writeWorkspacePageStatus(w, r, "page_service_request_form", user, workspaceID, data, "service", desk.ProjectID, status)
			return
		}
		request, err := h.Commands.CreateServiceRequest(r.Context(), commands.CreateServiceRequestInput{ActorID: user.ID, WorkspaceID: workspaceID, ServiceDeskID: desk.ID, RequestTypeID: requestType.ID, Channel: "portal", Summary: data.Summary, Description: data.Description, DescriptionADF: descriptionADF, Fields: customFields})
		if err == nil {
			redirectLocal(w, r, "/service/requests/"+request.Issue.Key)
			return
		}
		data.Error, status = err.Error(), http.StatusBadRequest
	}
	h.writeWorkspacePageStatus(w, r, "page_service_request_form", user, workspaceID, data, "service", desk.ProjectID, status)
}

// Chosen reports whether a refused portal form chose a value for a field.
func (d servicePageData) Chosen(fieldID, value string) bool {
	for _, chosen := range d.FieldChoices[fieldID] {
		if chosen == value {
			return true
		}
	}
	return false
}

// encodeServicePortalField reads a portal answer as the Jira value its field
// stores: option ids for select fields, where a cascading select's child must
// belong to its parent, members found by email for user pickers, and lists for
// multi-value fields. present is false when the customer left it blank.
func encodeServicePortalField(field models.ServiceRequestTypeField, submitted []string, child string, options []store.ServiceRequestFieldOption, memberByEmail func(string) (string, error)) (json.RawMessage, bool, error) {
	values := []string{}
	for _, value := range submitted {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return nil, false, nil
	}
	option := func(id string) (store.ServiceRequestFieldOption, bool) {
		for _, candidate := range options {
			if candidate.ID == id {
				return candidate, true
			}
		}
		return store.ServiceRequestFieldOption{}, false
	}
	split := func(separators string) []string {
		return strings.FieldsFunc(strings.Join(values, " "), func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(separators, r) })
	}
	var encoded any
	switch field.Type {
	case models.CustomFieldNumber:
		number, err := strconv.ParseFloat(values[0], 64)
		if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
			return nil, true, fmt.Errorf("%s must be a finite number.", field.Name)
		}
		encoded = number
	case models.CustomFieldDate:
		if _, err := time.Parse("2006-01-02", values[0]); err != nil {
			return nil, true, fmt.Errorf("%s must be a date.", field.Name)
		}
		encoded = values[0]
	case models.CustomFieldSelect, models.CustomFieldGroup, models.CustomFieldProject, models.CustomFieldVersion, models.CustomFieldTeam, models.CustomFieldAsset:
		if field.Type == models.CustomFieldAsset && field.AssetsMultiple {
			ids, seen := []string{}, map[string]bool{}
			for _, value := range values {
				if _, ok := option(value); !ok {
					return nil, true, fmt.Errorf("Choose from the options for %s.", field.Name)
				}
				if !seen[value] {
					seen[value] = true
					ids = append(ids, value)
				}
			}
			encoded = ids
			break
		}
		if _, ok := option(values[0]); !ok {
			return nil, true, fmt.Errorf("Choose one of the options for %s.", field.Name)
		}
		encoded = values[0]
	case models.CustomFieldMultiSelect, models.CustomFieldMultiGroup, models.CustomFieldMultiVersion:
		ids, seen := []string{}, map[string]bool{}
		for _, value := range values {
			if _, ok := option(value); !ok {
				return nil, true, fmt.Errorf("Choose from the options for %s.", field.Name)
			}
			if !seen[value] {
				seen[value] = true
				ids = append(ids, value)
			}
		}
		encoded = ids
	case models.CustomFieldCascadingSelect:
		parent, ok := option(values[0])
		if !ok {
			return nil, true, fmt.Errorf("Choose one of the options for %s.", field.Name)
		}
		value := map[string]string{"parent": parent.ID}
		if child = strings.TrimSpace(child); child != "" {
			belongs := false
			for _, candidate := range parent.Children {
				belongs = belongs || candidate.ID == child
			}
			if !belongs {
				return nil, true, fmt.Errorf("Choose a %s detail that belongs to %s.", field.Name, parent.Value)
			}
			value["child"] = child
		}
		encoded = value
	case models.CustomFieldLabels:
		encoded = split(",")
	case models.CustomFieldUser, models.CustomFieldMultiUser:
		emails := split(",;")
		if field.Type == models.CustomFieldUser && len(emails) > 1 {
			return nil, true, fmt.Errorf("Enter one email address for %s.", field.Name)
		}
		ids, seen := []string{}, map[string]bool{}
		for _, email := range emails {
			id, err := memberByEmail(email)
			if err != nil {
				return nil, true, err
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		if field.Type == models.CustomFieldUser {
			encoded = ids[0]
		} else {
			encoded = ids
		}
	default:
		encoded = values[0]
	}
	raw, err := json.Marshal(encoded)
	return raw, true, err
}

// serviceFieldIDs lists the ids a stored field value names.
func serviceFieldIDs(value any) []string {
	ids := []string{}
	switch typed := value.(type) {
	case string:
		ids = append(ids, typed)
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok {
				ids = append(ids, text)
			}
		}
	case map[string]any:
		for _, key := range []string{"parent", "child"} {
			if text, ok := typed[key].(string); ok && text != "" {
				ids = append(ids, text)
			}
		}
	}
	return ids
}

// serviceFieldDisplay writes a stored field value for people: options and
// members by name, and lists joined.
func serviceFieldDisplay(fieldType string, value any, catalog store.CustomFieldValueCatalog, pickerNames map[string]string) string {
	name := func(id string) string {
		if option, ok := catalog.Options[id]; ok {
			return option.Value
		}
		if user, ok := catalog.Users[id]; ok {
			return user.DisplayName
		}
		if group, ok := catalog.Groups[id]; ok {
			return group.Name
		}
		if picked, ok := pickerNames[id]; ok {
			return picked
		}
		return id
	}
	names := func() []string {
		out := []string{}
		for _, id := range serviceFieldIDs(value) {
			if fieldType == models.CustomFieldLabels {
				out = append(out, id)
			} else {
				out = append(out, name(id))
			}
		}
		return out
	}
	switch fieldType {
	case models.CustomFieldSelect, models.CustomFieldUser, models.CustomFieldMultiSelect, models.CustomFieldMultiUser, models.CustomFieldLabels,
		models.CustomFieldGroup, models.CustomFieldMultiGroup, models.CustomFieldProject, models.CustomFieldVersion, models.CustomFieldMultiVersion, models.CustomFieldTeam, models.CustomFieldAsset:
		return strings.Join(names(), ", ")
	case models.CustomFieldCascadingSelect:
		return strings.Join(names(), " - ")
	}
	return fmt.Sprint(value)
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
	evaluation.Approvals, err = h.Store.IssueApprovalDecisions(r.Context(), request.Issue.ID)
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
	operations, operationsErr := h.Store.ServiceOperationsProfile(r.Context(), workspaceID, request.Issue.ID)
	if operationsErr != nil && !errors.Is(operationsErr, pgx.ErrNoRows) {
		http.Error(w, "Could not load operations controls.", http.StatusInternalServerError)
		return
	}
	changeConflicts := []models.ServiceChangeWindow{}
	if canManage && operations != nil && operations.Kind == "change" {
		changeConflicts, err = h.Store.ServiceChangeConflicts(r.Context(), workspaceID, user.ID, request.Issue.ID)
		if err != nil {
			http.Error(w, "Could not load change conflicts.", http.StatusInternalServerError)
			return
		}
	}
	incidentUpdates := []models.ServiceIncidentUpdate{}
	escalationSteps := []models.ServiceEscalationStep{}
	incidentRoles := []models.ServiceIncidentRole{}
	incidentStakeholders := []models.ServiceIncidentStakeholder{}
	incidentAgents := []*models.User{}
	if operations != nil && operations.Kind == "incident" {
		incidentUpdates, err = h.Store.ServiceIncidentUpdates(r.Context(), workspaceID, user.ID, request.Issue.ID)
		if err != nil {
			http.Error(w, "Could not load incident updates.", http.StatusInternalServerError)
			return
		}
		if canManage {
			escalationSteps, err = h.Store.ServiceIncidentEscalationStatus(r.Context(), workspaceID, user.ID, request.Issue.ID)
			if err != nil {
				http.Error(w, "Could not load incident escalation status.", http.StatusInternalServerError)
				return
			}
			if incidentRoles, err = h.Store.ServiceIncidentRoles(r.Context(), workspaceID, user.ID, request.Issue.ID); err == nil {
				if incidentStakeholders, err = h.Store.ServiceIncidentStakeholders(r.Context(), workspaceID, user.ID, request.Issue.ID); err == nil {
					// Incident roles go to anyone who can manage the request: its
					// desk's agents and site administrators.
					incidentAgents, err = h.incidentRoleCandidates(r, workspaceID, request.Issue.ID)
				}
			}
			if err != nil {
				http.Error(w, "Could not load the incident team.", http.StatusInternalServerError)
				return
			}
		}
	}
	members := []*models.User{}
	if canManage {
		members, err = h.Store.MembersByWorkspace(r.Context(), workspaceID)
		if err != nil {
			http.Error(w, "Could not load eligible approvers.", http.StatusInternalServerError)
			return
		}
	}
	linkViews := []models.IssueLinkView{}
	linkTypes := []models.LinkType{}
	if canManage {
		links, err := h.Store.LinksByIssue(r.Context(), request.Issue.ID)
		if err != nil {
			http.Error(w, "Could not load related operations work.", http.StatusInternalServerError)
			return
		}
		for _, link := range links {
			otherID, relationship := link.OutwardID, link.Inward
			if link.OutwardID == request.Issue.ID {
				otherID, relationship = link.InwardID, link.Outward
			}
			other, err := h.issueForUser(r, user, workspaceID, otherID)
			if err != nil {
				continue
			}
			linkViews = append(linkViews, models.IssueLinkView{ID: link.ID, Relationship: relationship, IssueKey: other.Key, Summary: other.Summary, Status: other.Status})
		}
		values, err := h.Store.LinkTypes(r.Context(), workspaceID)
		if err != nil {
			http.Error(w, "Could not load operations relationship types.", http.StatusInternalServerError)
			return
		}
		for _, value := range values {
			linkTypes = append(linkTypes, *value)
		}
	}
	configuredFields, err := h.Store.ServiceRequestTypeFields(r.Context(), workspaceID, request.ServiceDesk.ID, request.RequestType.ID)
	if err != nil {
		http.Error(w, "Could not load request fields.", http.StatusInternalServerError)
		return
	}
	decodedFields := map[string]any{}
	optionIDs, userIDs, groupIDs := []string{}, []string{}, []string{}
	pickerNames := map[string]string{}
	for _, field := range configuredFields {
		if !field.Custom {
			continue
		}
		raw, ok := request.Issue.Fields[field.ID]
		if !ok || string(raw) == "null" {
			continue
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			http.Error(w, "Could not render request fields.", http.StatusInternalServerError)
			return
		}
		decodedFields[field.ID] = value
		switch field.Type {
		case models.CustomFieldSelect, models.CustomFieldMultiSelect, models.CustomFieldCascadingSelect:
			optionIDs = append(optionIDs, serviceFieldIDs(value)...)
		case models.CustomFieldUser, models.CustomFieldMultiUser:
			userIDs = append(userIDs, serviceFieldIDs(value)...)
		case models.CustomFieldGroup, models.CustomFieldMultiGroup:
			groupIDs = append(groupIDs, serviceFieldIDs(value)...)
		case models.CustomFieldProject, models.CustomFieldVersion, models.CustomFieldMultiVersion, models.CustomFieldTeam, models.CustomFieldAsset:
			choices, choicesErr := h.Store.ServicePortalPickerChoices(r.Context(), workspaceID, request.ServiceDesk.ID, user.ID, field.Type, field.AssetSchemaID)
			if choicesErr != nil {
				http.Error(w, "Could not render request fields.", http.StatusInternalServerError)
				return
			}
			for _, choice := range choices {
				pickerNames[choice.ID] = choice.Value
			}
		}
	}
	fieldCatalog, err := h.Store.LoadCustomFieldValueCatalog(r.Context(), workspaceID, optionIDs, userIDs, groupIDs)
	if err != nil {
		http.Error(w, "Could not render request fields.", http.StatusInternalServerError)
		return
	}
	requestFields := make([]serviceRequestFieldValueView, 0)
	for _, field := range configuredFields {
		if value, ok := decodedFields[field.ID]; ok {
			requestFields = append(requestFields, serviceRequestFieldValueView{Name: field.Name, Value: serviceFieldDisplay(field.Type, value, fieldCatalog, pickerNames)})
		}
	}
	var assetInventory *models.ServiceAssetInventory
	requestAssets := []models.ServiceRequestAsset{}
	if canManage {
		assetInventory, err = h.Store.ServiceAssetInventory(r.Context(), workspaceID, user.ID, request.ServiceDesk.ID)
		if err != nil {
			http.Error(w, "Could not load service assets.", http.StatusInternalServerError)
			return
		}
		requestAssets, err = h.Store.ServiceRequestAssetImpact(r.Context(), workspaceID, user.ID, request.Issue.ID)
		if err != nil {
			http.Error(w, "Could not calculate asset impact.", http.StatusInternalServerError)
			return
		}
	}
	desk, err := h.Store.ServiceDesk(r.Context(), workspaceID, request.ServiceDesk.ID)
	if err != nil {
		http.Error(w, "Could not load the service desk.", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_service_request", user, workspaceID, servicePageData{Desk: desk, Request: request, RequestFieldValues: requestFields, OperationsProfile: operations, ChangeConflicts: changeConflicts, IncidentUpdates: incidentUpdates, EscalationSteps: escalationSteps, IncidentRoles: incidentRoles, IncidentStakeholders: incidentStakeholders, DeskAgents: incidentAgents, AssetInventory: assetInventory, RequestAssets: requestAssets, Comments: comments, Attachments: attachments, Links: linkViews, LinkTypes: linkTypes, Approvals: approvals, Feedback: feedback, Participants: participants, Members: members, SLAs: slas, Transitions: transitions, CanAgent: canManage, CanManageParticipants: canManage || request.Customer.ID == user.ID, CurrentUserID: user.ID, Subscribed: subscribed, CanLeaveFeedback: desk.FeedbackEnabled && request.Customer.ID == user.ID && request.Issue.Resolution != nil}, "service", request.Issue.ProjectID)
}

func (h *Handler) ServiceRequestLink(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	request, canManage, err := h.serviceRequestForPage(r, workspaceID, user.ID, r.PathValue("key"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !canManage {
		http.Error(w, "Service agent access is required.", http.StatusForbidden)
		return
	}
	if _, _, err := h.Commands.LinkIssue(r.Context(), user.ID, workspaceID, request.Issue.ID, r.PostFormValue("type"), strings.TrimSpace(r.PostFormValue("issue"))); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/requests/"+request.Issue.Key+"#operations-links")
}

func (h *Handler) ServiceRequestLinkDelete(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	request, canManage, err := h.serviceRequestForPage(r, workspaceID, user.ID, r.PathValue("key"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !canManage {
		http.Error(w, "Service agent access is required.", http.StatusForbidden)
		return
	}
	if _, err := h.Commands.DeleteIssueLink(r.Context(), user.ID, workspaceID, request.Issue.ID, r.PathValue("link")); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/requests/"+request.Issue.Key+"#operations-links")
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
		http.Error(w, err.Error(), commandErrorStatus(err))
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
	if err := r.ParseMultipartForm(32 << 20); err != nil { // #nosec G120 -- body capped by MaxBytesReader above
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
			http.Error(w, err.Error(), commandErrorStatus(err))
			return
		}
	} else if _, err := h.Commands.AddServiceRequestComment(r.Context(), user.ID, workspaceID, request.Issue.ID, json.RawMessage(nil), r.PostFormValue("body"), public); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
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
		http.Error(w, err.Error(), commandErrorStatus(err))
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
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/requests/"+request.Issue.Key+"#approvals")
}

func (h *Handler) ServiceRequestOperations(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	impact, impactErr := strconv.Atoi(r.PostFormValue("impact"))
	likelihood, likelihoodErr := strconv.Atoi(r.PostFormValue("likelihood"))
	plannedStart, startErr := parseServiceDateTime(r.PostFormValue("plannedStart"))
	plannedEnd, endErr := parseServiceDateTime(r.PostFormValue("plannedEnd"))
	reviewDue, dueErr := parseServiceDateTime(r.PostFormValue("reviewDueAt"))
	if impactErr != nil || likelihoodErr != nil || startErr != nil || endErr != nil || dueErr != nil {
		http.Error(w, "Operations assessment is invalid.", http.StatusBadRequest)
		return
	}
	reviewRequired := r.PostFormValue("reviewRequired") == "true"
	reviewStatus := r.PostFormValue("reviewStatus")
	if !reviewRequired {
		reviewStatus = "not_required"
		reviewDue = nil
	} else if reviewStatus == "not_required" {
		reviewStatus = "pending"
	}
	_, err := h.Commands.UpdateServiceOperationsProfile(r.Context(), user.ID, workspaceID, r.PathValue("key"), models.ServiceOperationsProfile{Impact: impact, Likelihood: likelihood, ChangeType: r.PostFormValue("changeType"), PlannedStart: plannedStart, PlannedEnd: plannedEnd, RollbackPlan: r.PostFormValue("rollbackPlan"), OnCallUserID: r.PostFormValue("onCallUser"), MajorIncident: r.PostFormValue("majorIncident") == "true", ReviewRequired: reviewRequired, ReviewDueAt: reviewDue, ReviewStatus: reviewStatus, ReviewSummary: r.PostFormValue("reviewSummary")})
	if err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/requests/"+r.PathValue("key")+"#operations-control")
}

func (h *Handler) ServiceIncidentUpdate(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	if _, err := h.Commands.CreateServiceIncidentUpdate(r.Context(), user.ID, workspaceID, r.PathValue("key"), r.PostFormValue("audience"), r.PostFormValue("message")); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/requests/"+r.PathValue("key")+"#incident-updates")
}

// incidentRoleCandidates lists the members who may hold a request's major
// incident roles.
func (h *Handler) incidentRoleCandidates(r *http.Request, workspaceID, issueID string) ([]*models.User, error) {
	members, err := h.Store.MembersByWorkspace(r.Context(), workspaceID)
	if err != nil {
		return nil, err
	}
	candidates := []*models.User{}
	for _, member := range members {
		canManage, err := h.Store.CanManageServiceRequest(r.Context(), workspaceID, member.ID, issueID)
		if err != nil {
			return nil, err
		}
		if canManage {
			candidates = append(candidates, member)
		}
	}
	return candidates, nil
}

// ServiceIncidentRole gives a major incident role to an agent or clears it.
func (h *Handler) ServiceIncidentRole(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	if err := h.Commands.SetServiceIncidentRole(r.Context(), user.ID, workspaceID, r.PathValue("key"), r.PostFormValue("role"), r.PostFormValue("user_id")); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/requests/"+r.PathValue("key")+"#incident-team")
}

// ServiceIncidentStakeholder adds or removes a major incident's stakeholder.
func (h *Handler) ServiceIncidentStakeholder(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	key := r.PathValue("key")
	var err error
	if r.PostFormValue("action") == "remove" {
		err = h.Commands.RemoveServiceIncidentStakeholder(r.Context(), user.ID, workspaceID, key, r.PostFormValue("stakeholder_id"))
	} else {
		err = h.Commands.AddServiceIncidentStakeholder(r.Context(), user.ID, workspaceID, key, r.PostFormValue("user_id"), r.PostFormValue("email"))
	}
	if err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/requests/"+key+"#incident-team")
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
		http.Error(w, err.Error(), commandErrorStatus(err))
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
		http.Error(w, err.Error(), commandErrorStatus(err))
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
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	redirectLocal(w, r, "/service/requests/"+request.Issue.Key)
}

// ServiceQueueBulk applies one action to the requests an agent selects in a
// queue, as Jira Service Management's queue action bar does: assigning them
// to an agent or leaving them unassigned, moving each to a status through a
// transition its workflow offers, or commenting on them as an internal note or
// a reply to the customer. Requests it cannot change are named with the
// reason, and the rest are changed.
func (h *Handler) ServiceQueueBulk(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	deskID := r.PathValue("desk")
	agent, err := h.Store.IsServiceAgent(r.Context(), workspaceID, deskID, user.ID)
	if err != nil || !agent {
		http.Error(w, "Service agent access is required.", http.StatusForbidden)
		return
	}
	back := func(notice, problem string) {
		query := url.Values{"queue": {r.PostFormValue("queue")}}
		if notice != "" {
			query.Set("bulk", notice)
		}
		if problem != "" {
			if len(problem) > 1500 {
				problem = problem[:1500] + "…"
			}
			query.Set("bulkError", problem)
		}
		redirectLocal(w, r, "/service/agent/"+url.PathEscape(deskID)+"?"+query.Encode())
	}
	keys := r.PostForm["request"]
	if len(keys) == 0 {
		back("", "Select the requests to change.")
		return
	}
	if len(keys) > 100 {
		back("", "Change at most 100 requests at a time.")
		return
	}
	action := r.PostFormValue("action")
	assignee := r.PostFormValue("assignee")
	status := strings.TrimSpace(r.PostFormValue("status"))
	body := strings.TrimSpace(r.PostFormValue("body"))
	switch action {
	case "assign":
		if assignee != "" {
			agents, err := h.Store.ServiceDeskAgents(r.Context(), workspaceID, deskID)
			if err != nil {
				http.Error(w, "Could not load service desk agents.", http.StatusInternalServerError)
				return
			}
			found := false
			for _, candidate := range agents {
				found = found || candidate.ID == assignee
			}
			if !found {
				back("", "Assign requests to an agent of this service desk.")
				return
			}
		}
	case "transition":
		if status == "" {
			back("", "Choose the status to move the requests to.")
			return
		}
	case "comment":
		if body == "" {
			back("", "Write the comment to add to the requests.")
			return
		}
	default:
		back("", "Choose an action for the selected requests.")
		return
	}
	changed := 0
	problems := []string{}
	for _, key := range keys {
		request, err := h.Store.ServiceRequest(r.Context(), workspaceID, user.ID, key, true)
		if err != nil || request.ServiceDesk.ID != deskID {
			problems = append(problems, key+" is not a request of this service desk.")
			continue
		}
		switch action {
		case "assign":
			value := assignee
			_, _, err = h.Commands.UpdateIssue(r.Context(), commands.UpdateIssueInput{ActorID: user.ID, WorkspaceID: workspaceID, IssueIDOrKey: request.Issue.ID, AssigneeID: &value})
		case "transition":
			if request.Issue.Status.Name == status {
				problems = append(problems, request.Issue.Key+" is already "+status+".")
				continue
			}
			transitions, transitionErr := h.servicePageTransitions(r, workspaceID, user.ID, request)
			if transitionErr != nil {
				http.Error(w, "Could not load request transitions.", http.StatusInternalServerError)
				return
			}
			transitionID := ""
			for _, transition := range transitions {
				if transition.To == status && transitionID == "" {
					transitionID = transition.ID
				}
			}
			if transitionID == "" {
				problems = append(problems, request.Issue.Key+" cannot move from "+request.Issue.Status.Name+" to "+status+".")
				continue
			}
			_, err = h.Commands.TransitionServiceRequest(r.Context(), user.ID, workspaceID, request.Issue.ID, transitionID)
		case "comment":
			_, err = h.Commands.AddServiceRequestComment(r.Context(), user.ID, workspaceID, request.Issue.ID, json.RawMessage(nil), body, r.PostFormValue("visibility") == "public")
		}
		if err != nil {
			problems = append(problems, request.Issue.Key+": "+err.Error())
			continue
		}
		changed++
	}
	notice := ""
	if changed > 0 {
		notice = fmt.Sprintf("%d of %d requests changed.", changed, len(keys))
	}
	back(notice, strings.Join(problems, " "))
}
