package models

import "encoding/json"

import (
	"slices"
	"time"
)

// ServiceDesk is the Jira Service Management portal attached to a service project.
type ServiceDesk struct {
	ID             string
	WorkspaceID    string
	ProjectID      string
	ProjectKey     string
	ProjectName    string
	ProjectTypeKey string
	PortalName     string
	// PortalDescription is the portal's introduction text and PortalLogoURL
	// its logo, both shown on the portal and the help center.
	PortalDescription, PortalLogoURL string
	// AnnouncementsEnabled lets the desk's agents add an announcement to the
	// portal: AnnouncementTitle and AnnouncementMessage.
	AnnouncementsEnabled                   bool
	AnnouncementTitle, AnnouncementMessage string
	CustomerAccessOpen                     bool
	AttachmentsEnabled                     bool
	FeedbackEnabled                        bool
	// DisabledCustomerNotifications are the customer notifications the desk
	// does not send.
	DisabledCustomerNotifications []string
}

// CustomerNotificationEnabled reports whether the desk sends one of Jira's
// customer notifications.
func (d ServiceDesk) CustomerNotificationEnabled(key string) bool {
	return !slices.Contains(d.DisabledCustomerNotifications, key)
}

// Jira's customer notifications, which a service desk turns on or off. They
// reach customers; agents keep their own updates.
const (
	CustomerNotificationInvited        = "customer_invited"
	CustomerNotificationRequestCreated = "request_created"
	CustomerNotificationPublicComment  = "public_comment_added"
	CustomerNotificationStatusChanged  = "status_changed"
	CustomerNotificationParticipant    = "participant_added"
	CustomerNotificationApproval       = "approval_required"
)

// ServiceHelpCenter is the help center's branding and home page announcement.
// Empty values keep ZZIRA's defaults.
type ServiceHelpCenter struct {
	Name, HomeTitle, LogoURL, BannerURL              string
	BannerColour, BannerTextColour                   string
	NavigationBackgroundColour, NavigationTextColour string
	AnnouncementTitle, AnnouncementMessage           string
}

// ServiceCustomerNotification is one customer notification as Jira names it.
type ServiceCustomerNotification struct {
	Key, Name, Description string
}

// ServiceCustomerNotifications lists the customer notifications in Jira's order.
func ServiceCustomerNotifications() []ServiceCustomerNotification {
	return []ServiceCustomerNotification{
		{CustomerNotificationInvited, "Customer invited", "Emails people invited to the help center."},
		{CustomerNotificationRequestCreated, "Request created", "Confirms to the reporter that their request was received."},
		{CustomerNotificationPublicComment, "Public comment added", "Tells the customers involved about comments they can see."},
		{CustomerNotificationStatusChanged, "Customer-visible status changed", "Tells the customers involved when the request moves to another status."},
		{CustomerNotificationParticipant, "Participant added", "Tells people they were added to a request."},
		{CustomerNotificationApproval, "Approval required", "Tells approvers that a request needs their decision."},
	}
}

type ServiceOrganization struct {
	ID, WorkspaceID, Name, UUID string
	CreatedAt                   time.Time
}

type ServiceRequestTypeGroup struct {
	ID, ServiceDeskID, Name string
	Position                int
}

type ServiceRequestTypeField struct {
	ID, RequestTypeID, Name, Type, Description, HelpText string
	Required, Custom                                     bool
	Position                                             int
	// Hidden is true for a field hidden from the portal; a hidden field is
	// filled with PresetValue, a Jira field value, when a request is raised.
	Hidden      bool
	PresetValue json.RawMessage
	// ConditionFieldID and ConditionOptionIDs show the field only when that
	// select or multi-select field of the form has one of those options.
	ConditionFieldID   string
	ConditionOptionIDs []string
	// AssetSchemaID narrows an Assets object field to one schema of the
	// service project. An empty schema offers the desk's whole inventory.
	AssetSchemaID string
	// AssetsMultiple is the cardinality of the field's applicable custom field
	// context: an Assets object field holds several objects when it is set.
	AssetsMultiple bool
}

// ShownFor reports whether the field is shown for the option ids chosen for
// each field of its form.
func (f ServiceRequestTypeField) ShownFor(chosen map[string][]string) bool {
	if f.ConditionFieldID == "" {
		return true
	}
	for _, id := range chosen[f.ConditionFieldID] {
		if f.HasConditionOption(id) {
			return true
		}
	}
	return false
}

// HasConditionOption reports whether an option shows the field.
func (f ServiceRequestTypeField) HasConditionOption(id string) bool {
	for _, wanted := range f.ConditionOptionIDs {
		if wanted == id {
			return true
		}
	}
	return false
}

// ConditionOptions lists the options that show the field, separated by spaces.
func (f ServiceRequestTypeField) ConditionOptions() string {
	out := ""
	for index, id := range f.ConditionOptionIDs {
		if index > 0 {
			out += " "
		}
		out += id
	}
	return out
}

type ServiceKnowledgeArticle struct {
	PageID, SpaceID, SpaceKey, Title, Excerpt, Body string
}

// ServiceRequestType describes one customer-facing form backed by a Jira issue type.
type ServiceRequestType struct {
	ID            string
	ServiceDeskID string
	Name          string
	Description   string
	HelpText      string
	IssueTypeID   string
	GroupIDs      []string
}

// ServiceRequest attaches customer-facing service metadata to a regular Jira
// issue. The issue remains the canonical workflow and field record.
type ServiceRequest struct {
	Issue       *Issue
	ServiceDesk ServiceDesk
	RequestType ServiceRequestType
	Customer    *User
	Channel     string
	CreatedAt   time.Time
	SLAs        []ServiceSLA
}

// ServiceRequestComment records which Jira comments may cross the customer
// portal boundary. Comments without a row are agent-internal by default.
type ServiceRequestComment struct {
	Comment     Comment
	Public      bool
	Attachments []Attachment
}

type ServiceApprover struct {
	User      *User
	Decision  string
	DecidedAt *time.Time
}

type ServiceApproval struct {
	ID, RequestIssueID, Name, FinalDecision string
	CreatedAt                               time.Time
	CompletedAt                             *time.Time
	Approvers                               []ServiceApprover
	// A workflow status's approval keeps its status, condition and the
	// transitions that run once it is decided.
	StatusID, ConditionType                string
	ConditionValue                         int
	TransitionApproved, TransitionRejected string
	// ApproverGroups names, for approvers who came from group picker fields,
	// the groups they came from.
	ApproverGroups map[string][]string
}

type ServiceTemporaryAttachment struct {
	ID, WorkspaceID, ServiceDeskID, AuthorID string
	Filename, MimeType, BlobRef              string
	Size                                     int64
	CreatedAt, ExpiresAt                     time.Time
}

type ServiceRequestAttachment struct {
	Attachment Attachment
	CommentID  string
	Public     bool
}

type ServiceRequestFeedback struct {
	RequestIssueID, ReporterID, Type, Comment string
	Rating                                    int
	CreatedAt, UpdatedAt                      time.Time
}

// ServiceQueue is an ordered agent work view for one service desk.
type ServiceQueue struct {
	ID, ServiceDeskID, Name, JQL, Kind string
	Fields                             []string
	Position                           int
	IssueCount                         int
}

type ServiceCalendar struct {
	ID, ServiceDeskID, Name, TimeZone string
	Weekdays                          []int16
	StartMinute, EndMinute            int16
	Holidays                          map[string]string
}

type ServiceSLAMetric struct {
	ID, ServiceDeskID, CalendarID, Name, Kind, PauseJQL string
	GoalMillis                                          int64
	Position                                            int
	// StartConditions and StopConditions are the Jira SLA conditions that
	// start and stop the metric's clock.
	StartConditions, StopConditions []string
}

// StartsOn reports whether a condition starts the metric's clock.
func (m ServiceSLAMetric) StartsOn(condition string) bool {
	return slices.Contains(m.StartConditions, condition)
}

// StopsOn reports whether a condition stops the metric's clock.
func (m ServiceSLAMetric) StopsOn(condition string) bool {
	return slices.Contains(m.StopConditions, condition)
}

// Jira's SLA conditions: the events that start or stop an SLA's clock.
const (
	SLAConditionIssueCreated           = "issue_created"
	SLAConditionAssigneeFromUnassigned = "assignee_from_unassigned"
	SLAConditionAssigneeToUnassigned   = "assignee_to_unassigned"
	SLAConditionAssigneeChanged        = "assignee_changed"
	SLAConditionCommentByCustomer      = "comment_by_customer"
	SLAConditionCommentForCustomers    = "comment_for_customers"
	SLAConditionDueDateSet             = "duedate_set"
	SLAConditionDueDateCleared         = "duedate_cleared"
	SLAConditionDueDateChanged         = "duedate_changed"
	SLAConditionResolutionSet          = "resolution_set"
	SLAConditionResolutionCleared      = "resolution_cleared"
	slaConditionEnteredStatusPrefix    = "entered_status:"
)

// ServiceSLACondition is one condition as Jira names it.
type ServiceSLACondition struct {
	Key, Name string
}

// ServiceSLAConditions lists the conditions that do not depend on a
// project's statuses, in Jira's order.
func ServiceSLAConditions() []ServiceSLACondition {
	return []ServiceSLACondition{
		{SLAConditionAssigneeFromUnassigned, "Assignee: From Unassigned"},
		{SLAConditionAssigneeToUnassigned, "Assignee: To Unassigned"},
		{SLAConditionAssigneeChanged, "Assignee: Changed"},
		{SLAConditionCommentByCustomer, "Comment: By Customer"},
		{SLAConditionCommentForCustomers, "Comment: For Customers"},
		{SLAConditionDueDateCleared, "Due Date: Cleared"},
		{SLAConditionDueDateSet, "Due Date: Set"},
		{SLAConditionDueDateChanged, "Due Date: Changed"},
		{SLAConditionIssueCreated, "Issue Created"},
		{SLAConditionResolutionCleared, "Resolution: Cleared"},
		{SLAConditionResolutionSet, "Resolution: Set"},
	}
}

// ServiceSLAEnteredStatus is the condition met when a work item enters a status.
func ServiceSLAEnteredStatus(statusID string) ServiceSLACondition {
	return ServiceSLACondition{Key: slaConditionEnteredStatusPrefix + statusID}
}

// ServiceSLAGoal is one of an SLA's goals: the requests it applies to, the
// time it allows and the calendar that time is measured in.
// ServiceSLAGoal is one of an SLA's goals: the requests it applies to, the
// time it allows and, when it names one, the calendar that time is measured
// in. An empty CalendarID measures the goal in the metric's calendar.
type ServiceSLAGoal struct {
	ID, MetricID, Name, JQL, CalendarID string
	GoalMillis                          int64
	Position                            int
}

type ServiceSLACycle struct {
	ID, GoalID, GoalName, CalendarID           string
	GoalLabel, ElapsedLabel, RemainingLabel    string
	StartTime, BreachTime                      time.Time
	StopTime                                   *time.Time
	GoalMillis, ElapsedMillis, RemainingMillis int64
	Breached, Paused, WithinCalendarHours      bool
}

type ServiceSLAPause struct {
	ID, Reason string
	StartTime  time.Time
	StopTime   *time.Time
}

type ServiceSLA struct {
	ServiceSLAMetric
	CompletedCycles []ServiceSLACycle
	OngoingCycle    *ServiceSLACycle
}

type ServiceReportDay struct {
	Day   string
	Count int
}

type ServiceReportSegment struct {
	ID, Name string
	Count    int
}

type ServiceReportFilter struct {
	RequestTypeID, Channel, Status string
}

type ServiceReport struct {
	WindowDays                                    int
	TotalRequests, OpenRequests, ResolvedRequests int
	BreachedRequests, SatisfactionResponses       int
	AverageSatisfaction                           float64
	Daily                                         []ServiceReportDay
	RequestTypes, Channels                        []ServiceReportSegment
	// Priorities and Organizations break the same requests down by priority
	// and by the organizations their customers belong to on the desk. A
	// customer in several organizations counts in each.
	Priorities, Organizations []ServiceReportSegment
}

type ServiceOperationsSettings struct {
	ServiceDeskID, WorkspaceID string
	CABRiskThreshold           int
	ReviewDueDays              int
	CABMembers                 []*User
	OnCallShifts               []ServiceOnCallShift
	EscalationSteps            []ServiceEscalationStep
}

type ServiceOnCallShift struct {
	ID, ServiceDeskID, UserID, UserName, Label string
	StartsAt, EndsAt                           time.Time
}

type ServiceEscalationStep struct {
	ID, ServiceDeskID, TargetUserID, TargetUserName string
	Position, DelayMinutes                          int
	TriggeredAt                                     *time.Time
}

type ServiceOperationsProfile struct {
	RequestIssueID, Kind, ChangeType, RollbackPlan, OnCallUserID string
	Impact, Likelihood, RiskScore                                int
	PlannedStart, PlannedEnd, ReviewDueAt                        *time.Time
	OnCallUser                                                   *User
	ReviewRequired, MajorIncident                                bool
	MajorIncidentGeneration                                      int
	MajorIncidentDeclaredAt                                      *time.Time
	ReviewStatus, ReviewSummary                                  string
	UpdatedAt                                                    time.Time
}

type ServiceIncidentUpdate struct {
	ID, RequestIssueID, AuthorID, AuthorName, Audience, Message string
	CreatedAt                                                   time.Time
}

type ServiceChangeWindow struct {
	IssueID, IssueKey, Summary, StatusName, StatusCategory, RiskLevel string
	PlannedStart, PlannedEnd                                          time.Time
	RiskScore, ConflictCount                                          int
}

type ServiceDependencyNode struct {
	IssueID, IssueKey, Summary, Kind string
	Status                           Status
}

type ServiceDependencyEdge struct {
	ID, Relationship string
	From, To         ServiceDependencyNode
}

func (p ServiceOperationsProfile) RiskLevel() string {
	switch {
	case p.RiskScore >= 13:
		return "Critical"
	case p.RiskScore >= 9:
		return "High"
	case p.RiskScore >= 4:
		return "Medium"
	default:
		return "Low"
	}
}

// ServiceIncidentRoleDefinition is a role in a major incident's response team.
type ServiceIncidentRoleDefinition struct{ Key, Name string }

// ServiceIncidentRoleDefinitions are the major incident roles, in the order
// people see them.
var ServiceIncidentRoleDefinitions = []ServiceIncidentRoleDefinition{
	{"commander", "Incident commander"}, {"communications", "Communications lead"}, {"technical", "Technical lead"},
}

// ServiceIncidentRole is who holds an incident role; UserID is empty while
// nobody does.
type ServiceIncidentRole struct {
	Role, Name, UserID, UserName string
	AssignedAt                   *time.Time
}

// ServiceIncidentStakeholder follows a major incident's stakeholder updates:
// a site member or an email address.
type ServiceIncidentStakeholder struct {
	ID, UserID, Name, Email string
	AddedAt                 time.Time
}
