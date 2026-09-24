package commands

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type CreateServiceRequestInput struct {
	ActorID, WorkspaceID, ServiceDeskID, RequestTypeID string
	CustomerID, Channel, Summary, Description          string
	DescriptionADF                                     json.RawMessage
	ParticipantIDs                                     []string
	// OrganizationIDs are the organizations the customer shares the request
	// with as they raise it, as the portal's "Share with" choice does.
	OrganizationIDs []string
	Fields          map[string]json.RawMessage
}

// CreateServiceRequest creates the canonical Jira issue first and compensates
// with a logged deletion if the customer-facing link cannot be persisted.
func (s *Service) CreateServiceRequest(ctx context.Context, in CreateServiceRequestInput) (*models.ServiceRequest, error) {
	requestType, err := s.Store.ServiceRequestType(ctx, in.WorkspaceID, in.ServiceDeskID, in.RequestTypeID)
	if err != nil {
		return nil, fmt.Errorf("request type does not exist: %w", err)
	}
	desk, err := s.Store.ServiceDesk(ctx, in.WorkspaceID, in.ServiceDeskID)
	if err != nil {
		return nil, fmt.Errorf("service desk does not exist: %w", err)
	}
	if in.CustomerID == "" {
		in.CustomerID = in.ActorID
	}
	if in.CustomerID != in.ActorID {
		canRaiseOnBehalf, accessErr := s.Store.IsServiceAgent(ctx, in.WorkspaceID, in.ServiceDeskID, in.ActorID)
		if accessErr != nil {
			return nil, accessErr
		}
		if !canRaiseOnBehalf {
			return nil, fmt.Errorf("only a service agent may raise a request for another customer")
		}
	}
	if err := s.Store.EnrollServiceCustomer(ctx, in.WorkspaceID, in.CustomerID); err != nil {
		return nil, err
	}
	labelSet := map[string]bool{}
	for _, group := range requestType.GroupIDs {
		switch strings.ToLower(group) {
		case "incidents":
			labelSet["incident"] = true
		case "problems":
			labelSet["problem"] = true
		case "changes":
			labelSet["change"] = true
		}
	}
	name := strings.ToLower(requestType.Name)
	words := strings.FieldsFunc(name, func(value rune) bool { return !unicode.IsLetter(value) && !unicode.IsDigit(value) })
	for _, kind := range []string{"incident", "problem", "change"} {
		if slices.Contains(words, kind) {
			labelSet[kind] = true
		}
	}
	labels := make([]string, 0, len(labelSet))
	operationKind := ""
	for _, kind := range []string{"incident", "problem", "change"} {
		if labelSet[kind] {
			labels = append(labels, kind)
			if operationKind == "" {
				operationKind = kind
			}
		}
	}
	// An Assets object field scoped to a schema offers only that schema's
	// objects, so a request may name only those.
	formFields, err := s.Store.ServiceRequestTypeFields(ctx, in.WorkspaceID, in.ServiceDeskID, in.RequestTypeID)
	if err != nil {
		return nil, err
	}
	assetSchemas := map[string]string{}
	for _, field := range formFields {
		if field.AssetSchemaID != "" {
			assetSchemas[field.ID] = field.AssetSchemaID
		}
	}
	issue, _, err := s.CreateIssue(ctx, CreateIssueInput{
		ActorID: in.ActorID, ReporterID: in.CustomerID, WorkspaceID: in.WorkspaceID, ProjectIDOrKey: desk.ProjectID,
		Summary: in.Summary, Description: in.Description, DescriptionADF: in.DescriptionADF,
		IssueTypeID: requestType.IssueTypeID, Labels: labels, Fields: in.Fields, AssetSchemas: assetSchemas,
		customerRequest: true,
	})
	if err != nil {
		return nil, err
	}
	channel := strings.TrimSpace(in.Channel)
	if channel == "" {
		channel = "portal"
	}
	if err := s.Store.CreateServiceRequest(ctx, in.WorkspaceID, issue.ID, in.ServiceDeskID, in.RequestTypeID, in.CustomerID, channel); err != nil {
		_, cleanupErr := s.deleteIssueUnchecked(ctx, in.ActorID, in.WorkspaceID, issue.ID, "service request creation failed")
		return nil, errors.Join(err, cleanupErr)
	}
	if operationKind != "" {
		if err := s.Store.CreateServiceOperationsProfile(ctx, in.WorkspaceID, in.ActorID, issue.ID, in.ServiceDeskID, operationKind); err != nil {
			_, cleanupErr := s.deleteIssueUnchecked(ctx, in.ActorID, in.WorkspaceID, issue.ID, "service operations profile creation failed")
			return nil, errors.Join(err, cleanupErr)
		}
	}
	if err := s.Store.ApplyServiceSLAGoals(ctx, in.WorkspaceID, in.ActorID, in.ServiceDeskID, issue.ID); err != nil {
		_, cleanupErr := s.deleteIssueUnchecked(ctx, in.ActorID, in.WorkspaceID, issue.ID, "service SLA goal selection failed")
		return nil, errors.Join(err, cleanupErr)
	}
	if err := s.Store.ReconcileServiceSLAPauses(ctx, in.WorkspaceID, in.ActorID, issue.ID, time.Now().UTC()); err != nil {
		_, cleanupErr := s.deleteIssueUnchecked(ctx, in.ActorID, in.WorkspaceID, issue.ID, "service SLA pause selection failed")
		return nil, errors.Join(err, cleanupErr)
	}
	if len(in.ParticipantIDs) > 0 {
		if err := s.Store.UpdateServiceRequestParticipants(ctx, in.WorkspaceID, issue.ID, in.ParticipantIDs, false); err != nil {
			_, cleanupErr := s.deleteIssueUnchecked(ctx, in.ActorID, in.WorkspaceID, issue.ID, "service request participant creation failed")
			return nil, errors.Join(err, cleanupErr)
		}
	}
	if len(in.OrganizationIDs) > 0 {
		if err := s.Store.ShareServiceRequest(ctx, in.WorkspaceID, issue.ID, in.OrganizationIDs); err != nil {
			_, cleanupErr := s.deleteIssueUnchecked(ctx, in.ActorID, in.WorkspaceID, issue.ID, "service request sharing failed")
			return nil, errors.Join(err, cleanupErr)
		}
	}
	if err := s.notifyServiceRequestCreated(ctx, in.WorkspaceID, in.ServiceDeskID, in.CustomerID, issue.ID); err != nil {
		return nil, err
	}
	// A request created straight into an approval status opens its approval.
	current, err := s.Store.IssueByIDOrKey(ctx, in.WorkspaceID, issue.ID)
	if err != nil {
		return nil, err
	}
	if err := s.startStatusApproval(ctx, in.ActorID, in.WorkspaceID, current); err != nil {
		return nil, err
	}
	canManage, err := s.Store.CanManageServiceRequest(ctx, in.WorkspaceID, in.ActorID, issue.ID)
	if err != nil {
		return nil, err
	}
	return s.Store.ServiceRequest(ctx, in.WorkspaceID, in.ActorID, issue.ID, canManage)
}

func (s *Service) UpdateServiceRequestParticipants(ctx context.Context, actorID, workspaceID, issueIDOrKey string, userIDs []string, remove bool) ([]*models.User, error) {
	canManage, err := s.Store.CanManageServiceRequest(ctx, workspaceID, actorID, issueIDOrKey)
	if err != nil {
		return nil, err
	}
	request, err := s.Store.ServiceRequest(ctx, workspaceID, actorID, issueIDOrKey, canManage)
	if err != nil {
		return nil, fmt.Errorf("request does not exist")
	}
	if !canManage && request.Customer.ID != actorID {
		return nil, fmt.Errorf("only the reporter or an agent may manage participants")
	}
	if len(userIDs) == 0 {
		return nil, fmt.Errorf("at least one participant is required")
	}
	if err := s.Store.UpdateServiceRequestParticipants(ctx, workspaceID, request.Issue.ID, userIDs, remove); err != nil {
		return nil, err
	}
	if !remove {
		if err := s.notifyServiceRequestUsers(ctx, actorID, workspaceID, request, userIDs, "service_participant", "added you as a participant on "+request.Issue.Key, false, models.CustomerNotificationParticipant); err != nil {
			return nil, err
		}
	}
	return s.Store.ServiceRequestParticipants(ctx, request.Issue.ID)
}

// ShareServiceRequest says which organizations a request is shared with, which
// only the person who raised it or an agent of its desk decides.
func (s *Service) ShareServiceRequest(ctx context.Context, actorID, workspaceID, issueIDOrKey string, organizationIDs []string) ([]models.ServiceOrganization, error) {
	canManage, err := s.Store.CanManageServiceRequest(ctx, workspaceID, actorID, issueIDOrKey)
	if err != nil {
		return nil, err
	}
	request, err := s.Store.ServiceRequest(ctx, workspaceID, actorID, issueIDOrKey, canManage)
	if err != nil {
		return nil, fmt.Errorf("request does not exist")
	}
	if !canManage && request.Customer.ID != actorID {
		return nil, fmt.Errorf("only the reporter or an agent may share a request")
	}
	if err := s.Store.ShareServiceRequest(ctx, workspaceID, request.Issue.ID, organizationIDs); err != nil {
		return nil, err
	}
	return s.Store.ServiceRequestOrganizations(ctx, request.Issue.ID)
}

func (s *Service) CreateServiceCustomer(ctx context.Context, actorID, workspaceID, email, displayName string) (*models.User, error) {
	admin, err := s.Store.IsAdmin(ctx, workspaceID, actorID)
	if err != nil {
		return nil, err
	}
	if !admin {
		return nil, fmt.Errorf("only a site administrator may create customers")
	}
	email, displayName = strings.TrimSpace(strings.ToLower(email)), strings.TrimSpace(displayName)
	if email == "" || !strings.Contains(email, "@") || displayName == "" || len(displayName) > 255 {
		return nil, fmt.Errorf("a valid email and display name are required")
	}
	return s.Store.CreateServiceCustomer(ctx, workspaceID, email, displayName)
}

func (s *Service) AddServiceRequestComment(ctx context.Context, actorID, workspaceID, issueIDOrKey string, body json.RawMessage, plainText string, public bool) (*models.ServiceRequestComment, error) {
	return s.addServiceRequestComment(ctx, actorID, workspaceID, issueIDOrKey, body, plainText, public, true)
}

func (s *Service) addServiceRequestComment(ctx context.Context, actorID, workspaceID, issueIDOrKey string, body json.RawMessage, plainText string, public, emitSideEffects bool) (*models.ServiceRequestComment, error) {
	canManage, err := s.Store.CanManageServiceRequest(ctx, workspaceID, actorID, issueIDOrKey)
	if err != nil {
		return nil, err
	}
	request, err := s.Store.ServiceRequest(ctx, workspaceID, actorID, issueIDOrKey, canManage)
	if err != nil {
		return nil, fmt.Errorf("request does not exist")
	}
	if !canManage && !public {
		return nil, fmt.Errorf("customers may only add public comments")
	}
	comment, _, err := s.AddComment(ctx, AddCommentInput{ActorID: actorID, WorkspaceID: workspaceID, IssueIDOrKey: request.Issue.ID, Body: body, PlainText: plainText})
	if err != nil {
		return nil, err
	}
	if err := s.Store.MarkServiceRequestComment(ctx, request.Issue.ID, comment.ID, public); err != nil {
		_, cleanupErr := s.DeleteComment(ctx, actorID, workspaceID, comment.ID)
		return nil, errors.Join(err, cleanupErr)
	}
	if emitSideEffects {
		if err := s.afterServiceRequestComment(ctx, actorID, workspaceID, request, public, canManage); err != nil {
			return nil, err
		}
	}
	return &models.ServiceRequestComment{Comment: *comment, Public: public}, nil
}

func (s *Service) afterServiceRequestComment(ctx context.Context, actorID, workspaceID string, request *models.ServiceRequest, public, canManage bool) error {
	// A public agent comment is a comment for customers; a customer's own
	// comment is a comment by customer.
	if public {
		event := models.SLAConditionCommentByCustomer
		if canManage {
			event = models.SLAConditionCommentForCustomers
		}
		if err := s.applyServiceSLAEvents(ctx, actorID, workspaceID, request.ServiceDesk.ID, request.Issue.ID, []string{event}, store.ActionTimeOr(ctx, time.Now().UTC())); err != nil {
			return err
		}
	}
	return s.notifyServiceRequestSubscribers(ctx, actorID, workspaceID, request, "service_comment", "commented on "+request.Issue.Key, !public, models.CustomerNotificationPublicComment)
}

func (s *Service) TransitionServiceRequest(ctx context.Context, actorID, workspaceID, issueIDOrKey, transitionID string) (*models.ServiceRequest, error) {
	canManage, err := s.Store.CanManageServiceRequest(ctx, workspaceID, actorID, issueIDOrKey)
	if err != nil {
		return nil, err
	}
	request, err := s.Store.ServiceRequest(ctx, workspaceID, actorID, issueIDOrKey, canManage)
	if err != nil {
		return nil, fmt.Errorf("request does not exist")
	}
	if canManage {
		_, _, err = s.TransitionIssue(ctx, actorID, workspaceID, request.Issue.ID, transitionID)
	} else {
		_, _, err = s.transitionForCustomer(ctx, actorID, workspaceID, request.Issue.ID, transitionID)
	}
	if err != nil {
		return nil, err
	}
	request, err = s.Store.ServiceRequest(ctx, workspaceID, actorID, request.Issue.ID, canManage)
	if err != nil {
		return nil, err
	}
	if err := s.notifyServiceRequestSubscribers(ctx, actorID, workspaceID, request, "service_status", "moved "+request.Issue.Key+" to "+request.Issue.Status.Name, false, models.CustomerNotificationStatusChanged); err != nil {
		return nil, err
	}
	return request, nil
}

// syncServiceSLAsAfterIssueChange applies the SLA condition events a change to
// a service request's work item produced.
func (s *Service) syncServiceSLAsAfterIssueChange(ctx context.Context, actorID, workspaceID string, before, after *models.Issue, at time.Time) error {
	serviceDeskID, err := s.Store.ServiceRequestDeskID(ctx, workspaceID, after.ID)
	if err != nil || serviceDeskID == "" {
		return err
	}
	return s.applyServiceSLAEvents(ctx, actorID, workspaceID, serviceDeskID, after.ID, serviceSLAEvents(before, after), at)
}

// applyServiceSLAEvents stops and starts the request's SLA clocks whose
// conditions the events meet, reselects goals and settles pauses.
func (s *Service) applyServiceSLAEvents(ctx context.Context, actorID, workspaceID, serviceDeskID, issueID string, events []string, at time.Time) error {
	if len(events) > 0 {
		if _, err := s.Store.ApplyServiceSLAEvents(ctx, workspaceID, serviceDeskID, issueID, events, at); err != nil {
			return err
		}
		if err := s.Store.ApplyServiceSLAGoals(ctx, workspaceID, actorID, serviceDeskID, issueID); err != nil {
			return err
		}
	}
	return s.Store.ReconcileServiceSLAPauses(ctx, workspaceID, actorID, issueID, at)
}

// serviceSLAEvents lists the SLA condition events a change to a work item
// meets: entering a status, and changes to its assignee, due date and
// resolution.
func serviceSLAEvents(before, after *models.Issue) []string {
	assignee := func(issue *models.Issue) string {
		if issue.Assignee == nil {
			return ""
		}
		return issue.Assignee.ID
	}
	resolution := func(issue *models.Issue) string {
		if issue.Resolution == nil {
			return ""
		}
		return issue.Resolution.ID
	}
	events := serviceSLAFieldEvents("status", before.Status.ID, after.Status.ID)
	events = append(events, serviceSLAFieldEvents("assignee", assignee(before), assignee(after))...)
	events = append(events, serviceSLAFieldEvents("duedate", before.DueDate, after.DueDate)...)
	return append(events, serviceSLAFieldEvents("resolution", resolution(before), resolution(after))...)
}

// serviceSLAFieldEvents lists the SLA condition events one field's change
// meets, from the value it had to the value it has.
// serviceSLAStatusPredicate reads a pause condition that asks only about
// status, so it can be replayed over a request's history. Jira pauses an SLA
// while a request waits in a status such as "Waiting for customer"; a
// condition about anything else is only known for the request as it stands.
func serviceSLAStatusPredicate(query string) (func(status string) bool, bool) {
	parsed, err := jql.Parse(query)
	if err != nil || parsed.OrderBy != nil || len(parsed.Orders) > 0 {
		return nil, false
	}
	return serviceSLAStatusNode(parsed.Root)
}

func serviceSLAStatusNode(node jql.Node) (func(status string) bool, bool) {
	switch value := node.(type) {
	case jql.And:
		terms, ok := serviceSLAStatusTerms(value.Terms)
		if !ok {
			return nil, false
		}
		return func(status string) bool {
			for _, term := range terms {
				if !term(status) {
					return false
				}
			}
			return true
		}, true
	case jql.Or:
		terms, ok := serviceSLAStatusTerms(value.Terms)
		if !ok {
			return nil, false
		}
		return func(status string) bool {
			for _, term := range terms {
				if term(status) {
					return true
				}
			}
			return false
		}, true
	case jql.Not:
		inner, ok := serviceSLAStatusNode(value.Inner)
		if !ok {
			return nil, false
		}
		return func(status string) bool { return !inner(status) }, true
	case jql.Clause:
		if !strings.EqualFold(value.Field, "status") || len(value.Values) == 0 {
			return nil, false
		}
		names := make([]string, 0, len(value.Values))
		for _, raw := range value.Values {
			name := strings.Trim(strings.TrimSpace(raw), "\"'")
			if name == "" {
				return nil, false
			}
			names = append(names, name)
		}
		listed := func(status string) bool {
			return slices.ContainsFunc(names, func(name string) bool { return strings.EqualFold(name, status) })
		}
		switch strings.ToLower(strings.Join(strings.Fields(value.Op), " ")) {
		case "=", "in":
			return listed, true
		case "!=", "not in", "notin":
			return func(status string) bool { return !listed(status) }, true
		}
	}
	return nil, false
}

func serviceSLAStatusTerms(nodes []jql.Node) ([]func(string) bool, bool) {
	terms := make([]func(string) bool, 0, len(nodes))
	for _, node := range nodes {
		term, ok := serviceSLAStatusNode(node)
		if !ok {
			return nil, false
		}
		terms = append(terms, term)
	}
	return terms, true
}

// serviceSLAPauseIntervals replays a status pause condition over a request's
// history: the clock pauses when the request enters a status the condition
// names and runs again when it leaves. An interval left open is one the
// request is still waiting in.
func serviceSLAPauseIntervals(paused func(string) bool, changes []serviceSLAChange, reason string) []store.ServiceSLACyclePause {
	intervals := []store.ServiceSLACyclePause{}
	for _, change := range changes {
		open := len(intervals) > 0 && intervals[len(intervals)-1].Stop == nil
		switch holds := paused(change.Status); {
		case holds && !open:
			intervals = append(intervals, store.ServiceSLACyclePause{Start: change.At, Reason: reason})
		case !holds && open:
			at := change.At
			intervals[len(intervals)-1].Stop = &at
		}
	}
	return intervals
}

// replayServiceSLAPauses rebuilds a request's pauses for one metric, so a
// recalculated SLA keeps the time the request spent waiting rather than
// counting it against the goal. A condition that cannot be replayed is left to
// the live reconciliation, which knows only the request as it stands.
func (s *Service) replayServiceSLAPauses(ctx context.Context, workspaceID, serviceDeskID string, metric models.ServiceSLAMetric, requestID string, changes []serviceSLAChange) error {
	if strings.TrimSpace(metric.PauseJQL) == "" {
		return s.Store.ReplaceServiceSLAPauses(ctx, workspaceID, serviceDeskID, metric.ID, requestID, nil)
	}
	paused, ok := serviceSLAStatusPredicate(metric.PauseJQL)
	if !ok {
		return nil
	}
	intervals := serviceSLAPauseIntervals(paused, changes, "Matched pause condition for "+metric.Name)
	return s.Store.ReplaceServiceSLAPauses(ctx, workspaceID, serviceDeskID, metric.ID, requestID, intervals)
}

func serviceSLAFieldEvents(field, from, to string) []string {
	if from == to {
		return []string{}
	}
	switch field {
	case "status":
		return []string{models.ServiceSLAEnteredStatus(to).Key}
	case "assignee":
		switch {
		case from == "":
			return []string{models.SLAConditionAssigneeFromUnassigned, models.SLAConditionAssigneeChanged}
		case to == "":
			return []string{models.SLAConditionAssigneeToUnassigned, models.SLAConditionAssigneeChanged}
		}
		return []string{models.SLAConditionAssigneeChanged}
	case "duedate":
		switch {
		case from == "":
			return []string{models.SLAConditionDueDateSet, models.SLAConditionDueDateChanged}
		case to == "":
			return []string{models.SLAConditionDueDateCleared, models.SLAConditionDueDateChanged}
		}
		return []string{models.SLAConditionDueDateChanged}
	case "resolution":
		if to == "" {
			return []string{models.SLAConditionResolutionCleared}
		}
		return []string{models.SLAConditionResolutionSet}
	}
	return []string{}
}

// serviceSLAChange is one moment in a request's history and the SLA condition
// events it met.
type serviceSLAChange struct {
	At     time.Time
	Events []string
	// Status is the request's status once the change has been applied, so a
	// pause condition about status can be replayed over the history.
	Status string
}

// serviceSLASpans replays a request's history against an SLA's conditions the
// way live events apply: a stop condition ends the running cycle, then a start
// condition begins a new one when none is running.
func serviceSLASpans(start, stop []string, changes []serviceSLAChange) []store.ServiceSLACycleSpan {
	spans := []store.ServiceSLACycleSpan{}
	meets := func(conditions, events []string) bool {
		for _, event := range events {
			if slices.Contains(conditions, event) {
				return true
			}
		}
		return false
	}
	for _, change := range changes {
		running := len(spans) > 0 && spans[len(spans)-1].Stop == nil
		if running && meets(stop, change.Events) {
			at := change.At
			spans[len(spans)-1].Stop = &at
			running = false
		}
		if !running && meets(start, change.Events) {
			spans = append(spans, store.ServiceSLACycleSpan{Start: change.At})
		}
	}
	return spans
}

// serviceRequestSLAHistory lists a request's moments that SLA conditions can
// meet, oldest first: its creation, its field changes and its public comments.
// A comment counts as for customers when its author manages the request now.
func (s *Service) serviceRequestSLAHistory(ctx context.Context, workspaceID, issueID string) ([]serviceSLAChange, error) {
	var created time.Time
	var status string
	if err := s.Store.Pool.QueryRow(ctx, `
		SELECT sr.created_at,COALESCE(st.name,'')
		FROM service_requests sr JOIN issues i ON i.id=sr.issue_id
		LEFT JOIN statuses st ON st.id=i.status_id
		WHERE sr.workspace_id=$1 AND sr.issue_id=$2`, workspaceID, issueID).Scan(&created, &status); err != nil {
		return nil, err
	}
	entries, err := s.Store.IssueChangelog(ctx, workspaceID, issueID)
	if err != nil {
		return nil, err
	}
	// The status a request was created in is the one the first status change
	// moved away from; without such a change it is the status it is in now.
	for _, entry := range entries {
		if index := slices.IndexFunc(entry.Items, func(item models.ChangeItem) bool { return item.Field == "status" }); index >= 0 {
			status = entry.Items[index].From
			break
		}
	}
	changes := []serviceSLAChange{{At: created, Events: []string{models.SLAConditionIssueCreated}, Status: status}}
	for _, entry := range entries {
		at, parseErr := time.Parse(time.RFC3339, entry.Created)
		if parseErr != nil {
			return nil, parseErr
		}
		change := serviceSLAChange{At: at}
		for _, item := range entry.Items {
			change.Events = append(change.Events, serviceSLAFieldEvents(item.Field, item.From, item.To)...)
			if item.Field == "status" && item.From != item.To {
				// The changelog names a status by its id and carries its
				// name beside it; a pause condition speaks of the status by
				// its name, as the live evaluation of the same condition
				// does.
				change.Status = cmp.Or(item.ToString, item.To)
			}
		}
		if len(change.Events) > 0 {
			changes = append(changes, change)
		}
	}
	comments, err := s.Store.ServiceRequestPublicComments(ctx, issueID)
	if err != nil {
		return nil, err
	}
	agents := map[string]bool{}
	for _, comment := range comments {
		agent, checked := agents[comment.AuthorID]
		if !checked {
			if agent, err = s.Store.CanManageServiceRequest(ctx, workspaceID, comment.AuthorID, issueID); err != nil {
				return nil, err
			}
			agents[comment.AuthorID] = agent
		}
		event := models.SLAConditionCommentByCustomer
		if agent {
			event = models.SLAConditionCommentForCustomers
		}
		changes = append(changes, serviceSLAChange{At: comment.At, Events: []string{event}})
	}
	slices.SortStableFunc(changes, func(a, b serviceSLAChange) int { return a.At.Compare(b.At) })
	// Changes that are not status changes, such as comments, happen in the
	// status the request was left in.
	for index := range changes {
		if changes[index].Status == "" && index > 0 {
			changes[index].Status = changes[index-1].Status
		}
	}
	return changes, nil
}

// recalculateServiceSLA rebuilds one SLA's cycles for every open request of
// the desk from the requests' history, as Jira recalculates SLAs after their
// configuration changes. Completed requests keep their cycles.
func (s *Service) recalculateServiceSLA(ctx context.Context, actorID, workspaceID, serviceDeskID, metricID string) error {
	metrics, err := s.Store.ServiceSLAMetrics(ctx, workspaceID, serviceDeskID)
	if err != nil {
		return err
	}
	index := slices.IndexFunc(metrics, func(metric models.ServiceSLAMetric) bool { return metric.ID == metricID })
	if index < 0 {
		return fmt.Errorf("SLA metric does not exist")
	}
	requestIDs, err := s.Store.OpenServiceRequestIDs(ctx, workspaceID, serviceDeskID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, requestID := range requestIDs {
		history, err := s.serviceRequestSLAHistory(ctx, workspaceID, requestID)
		if err != nil {
			return err
		}
		spans := serviceSLASpans(metrics[index].StartConditions, metrics[index].StopConditions, history)
		if err := s.Store.ReplaceServiceSLACycles(ctx, workspaceID, serviceDeskID, metricID, requestID, spans); err != nil {
			return err
		}
		if err := s.Store.ApplyServiceSLAGoals(ctx, workspaceID, actorID, serviceDeskID, requestID); err != nil {
			return err
		}
		if err := s.replayServiceSLAPauses(ctx, workspaceID, serviceDeskID, metrics[index], requestID, history); err != nil {
			return err
		}
		if err := s.Store.ReconcileServiceSLAPauses(ctx, workspaceID, actorID, requestID, now); err != nil {
			return err
		}
	}
	return nil
}

// UpdateServiceSLAConditions sets the Jira conditions that start and stop an
// SLA metric's clock; each side needs at least one condition.
func (s *Service) UpdateServiceSLAConditions(ctx context.Context, actorID, workspaceID, serviceDeskID, metricID string, start, stop []string) error {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return err
	}
	start, stop, err := s.serviceSLAConditions(ctx, workspaceID, serviceDeskID, start, stop)
	if err != nil {
		return err
	}
	if err := s.Store.UpdateServiceSLAConditions(ctx, workspaceID, actorID, serviceDeskID, metricID, start, stop); err != nil {
		return err
	}
	return s.recalculateServiceSLA(ctx, actorID, workspaceID, serviceDeskID, metricID)
}

// serviceSLAConditions checks start and stop conditions against Jira's
// catalog and the desk project's statuses, drops repeats and requires at
// least one of each.
func (s *Service) serviceSLAConditions(ctx context.Context, workspaceID, serviceDeskID string, start, stop []string) ([]string, []string, error) {
	desk, err := s.Store.ServiceDesk(ctx, workspaceID, serviceDeskID)
	if err != nil {
		return nil, nil, fmt.Errorf("service desk does not exist: %w", err)
	}
	statuses, err := s.Store.StatusesForProject(ctx, workspaceID, desk.ProjectID, true)
	if err != nil {
		return nil, nil, err
	}
	known := map[string]bool{}
	for _, condition := range models.ServiceSLAConditions() {
		known[condition.Key] = true
	}
	for _, status := range statuses {
		known[models.ServiceSLAEnteredStatus(status.ID).Key] = true
	}
	normalize := func(side string, conditions []string) ([]string, error) {
		kept := []string{}
		for _, condition := range conditions {
			if condition = strings.TrimSpace(condition); !known[condition] {
				return nil, fmt.Errorf("%q is not an SLA condition for this service project", condition)
			}
			if !slices.Contains(kept, condition) {
				kept = append(kept, condition)
			}
		}
		if len(kept) == 0 {
			return nil, fmt.Errorf("an SLA needs at least one %s condition", side)
		}
		return kept, nil
	}
	if start, err = normalize("start", start); err != nil {
		return nil, nil, err
	}
	if stop, err = normalize("stop", stop); err != nil {
		return nil, nil, err
	}
	return start, stop, nil
}

// CreateServiceSLAMetric adds a custom SLA to a service desk with its goal and
// the conditions that start and stop its clock.
func (s *Service) CreateServiceSLAMetric(ctx context.Context, actorID, workspaceID, serviceDeskID, name string, goalMillis int64, start, stop []string) (models.ServiceSLAMetric, error) {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return models.ServiceSLAMetric{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 255 {
		return models.ServiceSLAMetric{}, fmt.Errorf("an SLA name of 1 to 255 characters is required")
	}
	start, stop, err := s.serviceSLAConditions(ctx, workspaceID, serviceDeskID, start, stop)
	if err != nil {
		return models.ServiceSLAMetric{}, err
	}
	metric, err := s.Store.CreateServiceSLAMetric(ctx, workspaceID, actorID, serviceDeskID, name, goalMillis, start, stop)
	if err != nil {
		return metric, err
	}
	return metric, s.recalculateServiceSLA(ctx, actorID, workspaceID, serviceDeskID, metric.ID)
}

// DeleteServiceSLAMetric removes a custom SLA from a service desk.
func (s *Service) DeleteServiceSLAMetric(ctx context.Context, actorID, workspaceID, serviceDeskID, metricID string) error {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return err
	}
	return s.Store.DeleteServiceSLAMetric(ctx, workspaceID, actorID, serviceDeskID, metricID)
}

func (s *Service) SetServiceDeskAgent(ctx context.Context, actorID, workspaceID, serviceDeskID, userID string, enabled bool) error {
	admin, err := s.Store.IsAdmin(ctx, workspaceID, actorID)
	if err != nil {
		return err
	}
	if !admin {
		return fmt.Errorf("only an administrator may manage service desk agents")
	}
	return s.Store.SetServiceDeskAgent(ctx, workspaceID, actorID, serviceDeskID, userID, enabled)
}

func (s *Service) UpdateServiceSLAMetric(ctx context.Context, actorID, workspaceID, serviceDeskID, metricID, pauseJQL string, goalMillis int64) error {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return err
	}
	pauseJQL = strings.TrimSpace(pauseJQL)
	if len(pauseJQL) > 2000 {
		return fmt.Errorf("SLA pause JQL accepts at most 2000 characters")
	}
	if pauseJQL != "" {
		lower := strings.ToLower(pauseJQL)
		for _, function := range []string{"breached(", "completed(", "everbreached(", "paused(", "remaining(", "running(", "withincalendarhours("} {
			if strings.Contains(lower, function) {
				return fmt.Errorf("SLA pause JQL cannot depend on SLA functions")
			}
		}
		parsed, err := jql.Parse(pauseJQL)
		if err != nil {
			return err
		}
		if parsed.OrderBy != nil || len(parsed.Orders) > 0 {
			return fmt.Errorf("SLA pause JQL cannot contain ORDER BY")
		}
		if err := s.Store.ExpandAppJQL(ctx, workspaceID, parsed); err != nil {
			return err
		}
		resolver, err := s.Store.JQLResolver(ctx, workspaceID)
		if err != nil {
			return err
		}
		if compiled := jql.Compile(parsed, actorID, resolver); compiled.Err != nil {
			return compiled.Err
		}
	}
	if err := s.Store.UpdateServiceSLAMetric(ctx, workspaceID, actorID, serviceDeskID, metricID, pauseJQL, goalMillis); err != nil {
		return err
	}
	requestIDs, err := s.Store.OpenServiceSLAMetricRequestIDs(ctx, workspaceID, serviceDeskID, metricID)
	if err != nil {
		return err
	}
	metrics, err := s.Store.ServiceSLAMetrics(ctx, workspaceID, serviceDeskID)
	if err != nil {
		return err
	}
	index := slices.IndexFunc(metrics, func(metric models.ServiceSLAMetric) bool { return metric.ID == metricID })
	now := time.Now().UTC()
	for _, requestID := range requestIDs {
		if index >= 0 {
			history, err := s.serviceRequestSLAHistory(ctx, workspaceID, requestID)
			if err != nil {
				return err
			}
			if err := s.replayServiceSLAPauses(ctx, workspaceID, serviceDeskID, metrics[index], requestID, history); err != nil {
				return err
			}
		}
		if err := s.Store.ReconcileServiceSLAPauses(ctx, workspaceID, actorID, requestID, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) validateServiceSLAGoal(ctx context.Context, workspaceID, name, query string, goalMillis int64) (string, string, error) {
	name, query = strings.TrimSpace(name), strings.TrimSpace(query)
	if name == "" || len(name) > 255 {
		return "", "", fmt.Errorf("SLA goal name is required and accepts at most 255 characters")
	}
	if query == "" || len(query) > 2000 {
		return "", "", fmt.Errorf("conditional SLA goal JQL is required and accepts at most 2000 characters")
	}
	if goalMillis < time.Minute.Milliseconds() || goalMillis > (365*24*time.Hour).Milliseconds() {
		return "", "", fmt.Errorf("SLA goal must be between one minute and 365 days")
	}
	parsed, err := jql.Parse(query)
	if err != nil {
		return "", "", err
	}
	if parsed.OrderBy != nil {
		return "", "", fmt.Errorf("conditional SLA goal JQL cannot contain ORDER BY")
	}
	if err := s.Store.ExpandAppJQL(ctx, workspaceID, parsed); err != nil {
		return "", "", err
	}
	resolver, err := s.Store.JQLResolver(ctx, workspaceID)
	if err != nil {
		return "", "", err
	}
	if compiled := jql.Compile(parsed, "validation", resolver); compiled.Err != nil {
		return "", "", compiled.Err
	}
	return name, query, nil
}

func (s *Service) CreateServiceSLAGoal(ctx context.Context, actorID, workspaceID, serviceDeskID, metricID, name, query, calendarID string, goalMillis int64) (*models.ServiceSLAGoal, error) {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return nil, err
	}
	name, query, err := s.validateServiceSLAGoal(ctx, workspaceID, name, query, goalMillis)
	if err != nil {
		return nil, err
	}
	return s.Store.CreateServiceSLAGoal(ctx, workspaceID, actorID, serviceDeskID, metricID, name, query, strings.TrimSpace(calendarID), goalMillis)
}

func (s *Service) UpdateServiceSLAGoal(ctx context.Context, actorID, workspaceID, serviceDeskID, metricID, goalID, name, query, calendarID string, goalMillis int64) error {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return err
	}
	name, query, err := s.validateServiceSLAGoal(ctx, workspaceID, name, query, goalMillis)
	if err != nil {
		return err
	}
	return s.Store.UpdateServiceSLAGoal(ctx, workspaceID, actorID, serviceDeskID, metricID, goalID, name, query, strings.TrimSpace(calendarID), goalMillis)
}

func (s *Service) DeleteServiceSLAGoal(ctx context.Context, actorID, workspaceID, serviceDeskID, metricID, goalID string) error {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return err
	}
	return s.Store.DeleteServiceSLAGoal(ctx, workspaceID, actorID, serviceDeskID, metricID, goalID)
}

// MoveServiceSLAGoal moves a conditional SLA goal up or down in the order its
// metric evaluates goals.
func (s *Service) MoveServiceSLAGoal(ctx context.Context, actorID, workspaceID, serviceDeskID, metricID, goalID, direction string) error {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return err
	}
	return s.Store.MoveServiceSLAGoal(ctx, workspaceID, actorID, serviceDeskID, metricID, goalID, direction)
}

func (s *Service) UpdateServiceCalendar(ctx context.Context, actorID, workspaceID, serviceDeskID, name, timeZone string, weekdays []int16, startMinute, endMinute int16) error {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return err
	}
	return s.Store.UpdateServiceCalendar(ctx, workspaceID, actorID, serviceDeskID, name, timeZone, weekdays, startMinute, endMinute)
}

// CreateServiceCalendar adds working hours the desk's SLA goals can name.
func (s *Service) CreateServiceCalendar(ctx context.Context, actorID, workspaceID, serviceDeskID, name, timeZone string, weekdays []int16, startMinute, endMinute int16) (*models.ServiceCalendar, error) {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return nil, err
	}
	return s.Store.CreateServiceCalendar(ctx, workspaceID, actorID, serviceDeskID, name, timeZone, weekdays, startMinute, endMinute)
}

// DeleteServiceCalendar removes working hours no SLA goal is measured in.
func (s *Service) DeleteServiceCalendar(ctx context.Context, actorID, workspaceID, serviceDeskID, calendarID string) error {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return err
	}
	return s.Store.DeleteServiceCalendar(ctx, workspaceID, actorID, serviceDeskID, strings.TrimSpace(calendarID))
}

func (s *Service) UpsertServiceCalendarHoliday(ctx context.Context, actorID, workspaceID, serviceDeskID, calendarID, day, name string) error {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return err
	}
	holiday, err := time.Parse(time.DateOnly, strings.TrimSpace(day))
	if err != nil {
		return fmt.Errorf("holiday date must use YYYY-MM-DD")
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 255 {
		return fmt.Errorf("holiday name is required and accepts at most 255 characters")
	}
	return s.Store.UpsertServiceCalendarHoliday(ctx, workspaceID, actorID, serviceDeskID, strings.TrimSpace(calendarID), holiday, name)
}

func (s *Service) DeleteServiceCalendarHoliday(ctx context.Context, actorID, workspaceID, serviceDeskID, calendarID, day string) error {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return err
	}
	holiday, err := time.Parse(time.DateOnly, strings.TrimSpace(day))
	if err != nil {
		return fmt.Errorf("holiday date must use YYYY-MM-DD")
	}
	return s.Store.DeleteServiceCalendarHoliday(ctx, workspaceID, actorID, serviceDeskID, strings.TrimSpace(calendarID), holiday)
}

func (s *Service) validateServiceQueue(ctx context.Context, workspaceID, name, query string) (string, string, error) {
	name, query = strings.TrimSpace(name), strings.TrimSpace(query)
	if name == "" || len(name) > 255 {
		return "", "", fmt.Errorf("queue name is required and accepts at most 255 characters")
	}
	if query == "" || len(query) > 2000 {
		return "", "", fmt.Errorf("queue JQL is required and accepts at most 2000 characters")
	}
	parsed, err := jql.Parse(query)
	if err != nil {
		return "", "", err
	}
	if err := s.Store.ExpandAppJQL(ctx, workspaceID, parsed); err != nil {
		return "", "", err
	}
	resolver, err := s.Store.JQLResolver(ctx, workspaceID)
	if err != nil {
		return "", "", err
	}
	if compiled := jql.Compile(parsed, "validation", resolver); compiled.Err != nil {
		return "", "", compiled.Err
	}
	return name, query, nil
}

func (s *Service) CreateServiceQueue(ctx context.Context, actorID, workspaceID, serviceDeskID, name, query string) (*models.ServiceQueue, error) {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return nil, err
	}
	name, query, err := s.validateServiceQueue(ctx, workspaceID, name, query)
	if err != nil {
		return nil, err
	}
	return s.Store.CreateServiceQueue(ctx, workspaceID, actorID, serviceDeskID, name, query)
}

func (s *Service) UpdateServiceQueue(ctx context.Context, actorID, workspaceID, serviceDeskID, queueID, name, query string) error {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return err
	}
	name, query, err := s.validateServiceQueue(ctx, workspaceID, name, query)
	if err != nil {
		return err
	}
	return s.Store.UpdateServiceQueue(ctx, workspaceID, actorID, serviceDeskID, queueID, name, query)
}

func (s *Service) DeleteServiceQueue(ctx context.Context, actorID, workspaceID, serviceDeskID, queueID string) error {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return err
	}
	return s.Store.DeleteServiceQueue(ctx, workspaceID, actorID, serviceDeskID, queueID)
}

func (s *Service) SetServiceRequestTypeFields(ctx context.Context, actorID, workspaceID, serviceDeskID, requestTypeID string, fields []models.ServiceRequestTypeField) error {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return err
	}
	if len(fields) == 0 || len(fields) > 50 {
		return fmt.Errorf("request type forms require 1 to 50 fields")
	}
	desk, err := s.Store.ServiceDesk(ctx, workspaceID, serviceDeskID)
	if err != nil {
		return fmt.Errorf("service desk does not exist")
	}
	customFields, err := s.Store.CustomFieldsForProject(ctx, desk.ProjectID)
	if err != nil {
		return err
	}
	allowed := map[string]bool{"summary": true, "description": true}
	for _, field := range customFields {
		allowed[field.ID] = true
	}
	seen, hasSummary := map[string]bool{}, false
	for index := range fields {
		field := &fields[index]
		field.ID, field.HelpText = strings.TrimSpace(field.ID), strings.TrimSpace(field.HelpText)
		if !allowed[field.ID] || seen[field.ID] {
			return fmt.Errorf("field %q is duplicated or unavailable for this service project", field.ID)
		}
		if len(field.HelpText) > 1000 {
			return fmt.Errorf("field help text accepts at most 1000 characters")
		}
		seen[field.ID] = true
		if field.ID == "summary" {
			hasSummary, field.Required = true, true
			if field.Hidden {
				return fmt.Errorf("summary cannot be hidden from the portal")
			}
		}
		// A hidden field is filled with its preset value, which it needs when
		// it is required, because a customer cannot answer it.
		preset := len(field.PresetValue) > 0 && string(field.PresetValue) != "null"
		if preset && (!json.Valid(field.PresetValue) || len(field.PresetValue) > 64<<10) {
			return fmt.Errorf("the preset value of %s must be JSON of at most 64 KiB", field.ID)
		}
		if field.Hidden && field.Required && !preset {
			return fmt.Errorf("hidden required field %s needs a preset value", field.ID)
		}
	}
	if !hasSummary {
		return fmt.Errorf("summary is required on every request type form")
	}
	// An Assets object field may offer one schema of the service project
	// rather than its whole inventory.
	deskSchemas, err := s.Store.ServiceDeskAssetSchemas(ctx, workspaceID, serviceDeskID)
	if err != nil {
		return err
	}
	fieldTypes := map[string]string{}
	for _, field := range customFields {
		fieldTypes[field.ID] = field.Type
	}
	for index := range fields {
		field := &fields[index]
		field.AssetSchemaID = strings.TrimSpace(field.AssetSchemaID)
		if field.AssetSchemaID == "" {
			continue
		}
		if fieldTypes[field.ID] != models.CustomFieldAsset {
			return fmt.Errorf("only an Assets object field can offer a schema, and %s is not one", field.ID)
		}
		if !slices.ContainsFunc(deskSchemas, func(schema models.ServiceAssetSchema) bool { return schema.ID == field.AssetSchemaID }) {
			return fmt.Errorf("%s offers no Assets schema of this service project", field.ID)
		}
	}
	// A field shown only for some answers waits on a visible select or
	// multi-select field of the same form that is not conditional itself.
	types := map[string]string{}
	for _, field := range customFields {
		types[field.ID] = field.Type
	}
	byID := map[string]models.ServiceRequestTypeField{}
	for _, field := range fields {
		byID[field.ID] = field
	}
	for index := range fields {
		field := &fields[index]
		field.ConditionFieldID = strings.TrimSpace(field.ConditionFieldID)
		if field.ConditionFieldID == "" {
			field.ConditionOptionIDs = nil
			continue
		}
		source, onForm := byID[field.ConditionFieldID]
		switch {
		case field.ID == "summary" || field.Hidden:
			return fmt.Errorf("%s cannot be shown only for some answers", field.ID)
		case !onForm || source.Hidden || strings.TrimSpace(source.ConditionFieldID) != "" || source.ID == field.ID:
			return fmt.Errorf("%s must wait on another visible, unconditional field of the form", field.ID)
		case types[source.ID] != models.CustomFieldSelect && types[source.ID] != models.CustomFieldMultiSelect:
			return fmt.Errorf("%s can only wait on a select or multi-select field", field.ID)
		case len(field.ConditionOptionIDs) == 0 || len(field.ConditionOptionIDs) > 20:
			return fmt.Errorf("choose between 1 and 20 options that show %s", field.ID)
		}
		options, err := s.Store.ServiceRequestFieldOptions(ctx, workspaceID, serviceDeskID, source.ID)
		if err != nil {
			return err
		}
		offered := map[string]bool{}
		for _, option := range options {
			offered[option.ID] = true
		}
		seen, ids := map[string]bool{}, []string{}
		for _, id := range field.ConditionOptionIDs {
			if !offered[id] {
				return fmt.Errorf("%s offers no option %q", source.ID, id)
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		field.ConditionOptionIDs = ids
	}
	return s.Store.SetServiceRequestTypeFields(ctx, workspaceID, actorID, serviceDeskID, requestTypeID, fields)
}
