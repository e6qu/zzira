package commands

import (
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
)

type CreateServiceRequestInput struct {
	ActorID, WorkspaceID, ServiceDeskID, RequestTypeID string
	CustomerID, Channel, Summary, Description          string
	DescriptionADF                                     json.RawMessage
	ParticipantIDs                                     []string
	Fields                                             map[string]json.RawMessage
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
	issue, _, err := s.CreateIssue(ctx, CreateIssueInput{
		ActorID: in.ActorID, ReporterID: in.CustomerID, WorkspaceID: in.WorkspaceID, ProjectIDOrKey: desk.ProjectID,
		Summary: in.Summary, Description: in.Description, DescriptionADF: in.DescriptionADF,
		IssueTypeID: requestType.IssueTypeID, Labels: labels, Fields: in.Fields,
	})
	if err != nil {
		return nil, err
	}
	channel := strings.TrimSpace(in.Channel)
	if channel == "" {
		channel = "portal"
	}
	if err := s.Store.CreateServiceRequest(ctx, in.WorkspaceID, issue.ID, in.ServiceDeskID, in.RequestTypeID, in.CustomerID, channel); err != nil {
		_, cleanupErr := s.DeleteIssue(ctx, in.ActorID, in.WorkspaceID, issue.ID, "service request creation failed")
		return nil, errors.Join(err, cleanupErr)
	}
	if operationKind != "" {
		if err := s.Store.CreateServiceOperationsProfile(ctx, in.WorkspaceID, in.ActorID, issue.ID, in.ServiceDeskID, operationKind); err != nil {
			_, cleanupErr := s.DeleteIssue(ctx, in.ActorID, in.WorkspaceID, issue.ID, "service operations profile creation failed")
			return nil, errors.Join(err, cleanupErr)
		}
	}
	if err := s.Store.ApplyServiceSLAGoals(ctx, in.WorkspaceID, in.ActorID, in.ServiceDeskID, issue.ID); err != nil {
		_, cleanupErr := s.DeleteIssue(ctx, in.ActorID, in.WorkspaceID, issue.ID, "service SLA goal selection failed")
		return nil, errors.Join(err, cleanupErr)
	}
	if err := s.Store.ReconcileServiceSLAPauses(ctx, in.WorkspaceID, in.ActorID, issue.ID, time.Now().UTC()); err != nil {
		_, cleanupErr := s.DeleteIssue(ctx, in.ActorID, in.WorkspaceID, issue.ID, "service SLA pause selection failed")
		return nil, errors.Join(err, cleanupErr)
	}
	if len(in.ParticipantIDs) > 0 {
		if err := s.Store.UpdateServiceRequestParticipants(ctx, in.WorkspaceID, issue.ID, in.ParticipantIDs, false); err != nil {
			_, cleanupErr := s.DeleteIssue(ctx, in.ActorID, in.WorkspaceID, issue.ID, "service request participant creation failed")
			return nil, errors.Join(err, cleanupErr)
		}
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
	return s.Store.ServiceRequestParticipants(ctx, request.Issue.ID)
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
	if public && canManage {
		if err := s.Store.CompleteServiceSLA(ctx, workspaceID, request.Issue.ID, "first_response", time.Now().UTC()); err != nil {
			return err
		}
	}
	return s.notifyServiceRequestSubscribers(ctx, actorID, workspaceID, request, "service_comment", "commented on "+request.Issue.Key, !public)
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
	_, _, err = s.TransitionIssue(ctx, actorID, workspaceID, request.Issue.ID, transitionID)
	if err != nil {
		return nil, err
	}
	request, err = s.Store.ServiceRequest(ctx, workspaceID, actorID, request.Issue.ID, canManage)
	if err != nil {
		return nil, err
	}
	if err := s.notifyServiceRequestSubscribers(ctx, actorID, workspaceID, request, "service_status", "moved "+request.Issue.Key+" to "+request.Issue.Status.Name, false); err != nil {
		return nil, err
	}
	return request, nil
}

func (s *Service) syncServiceSLAsAfterIssueChange(ctx context.Context, actorID, workspaceID string, issue *models.Issue, at time.Time) error {
	serviceDeskID, err := s.Store.ServiceRequestDeskID(ctx, workspaceID, issue.ID)
	if err != nil || serviceDeskID == "" {
		return err
	}
	if issue.Status.Category == "done" {
		if err := s.Store.CompleteServiceSLA(ctx, workspaceID, issue.ID, "resolution", at); err != nil {
			return err
		}
	} else {
		if err := s.Store.EnsureResolutionSLA(ctx, workspaceID, issue.ID, at); err != nil {
			return err
		}
		if err := s.Store.ApplyServiceSLAGoals(ctx, workspaceID, actorID, serviceDeskID, issue.ID); err != nil {
			return err
		}
	}
	return s.Store.ReconcileServiceSLAPauses(ctx, workspaceID, actorID, issue.ID, at)
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
	admin, err := s.Store.IsAdmin(ctx, workspaceID, actorID)
	if err != nil {
		return err
	}
	if !admin {
		return fmt.Errorf("only an administrator may configure service SLAs")
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
		fields, err := s.Store.CustomFieldsForWorkspace(ctx, workspaceID)
		if err != nil {
			return err
		}
		if compiled := jql.Compile(parsed, actorID, jql.WithCustomFields(jql.DefaultResolver(), fields)); compiled.Err != nil {
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
	now := time.Now().UTC()
	for _, requestID := range requestIDs {
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
	resolver := jql.DefaultResolver()
	fields, err := s.Store.CustomFieldsForWorkspace(ctx, workspaceID)
	if err != nil {
		return "", "", err
	}
	if compiled := jql.Compile(parsed, "validation", jql.WithCustomFields(resolver, fields)); compiled.Err != nil {
		return "", "", compiled.Err
	}
	return name, query, nil
}

func (s *Service) CreateServiceSLAGoal(ctx context.Context, actorID, workspaceID, serviceDeskID, metricID, name, query string, goalMillis int64) (*models.ServiceSLAGoal, error) {
	if err := s.requireServiceAdmin(ctx, workspaceID, actorID); err != nil {
		return nil, err
	}
	name, query, err := s.validateServiceSLAGoal(ctx, workspaceID, name, query, goalMillis)
	if err != nil {
		return nil, err
	}
	return s.Store.CreateServiceSLAGoal(ctx, workspaceID, actorID, serviceDeskID, metricID, name, query, goalMillis)
}

func (s *Service) UpdateServiceSLAGoal(ctx context.Context, actorID, workspaceID, serviceDeskID, metricID, goalID, name, query string, goalMillis int64) error {
	if err := s.requireServiceAdmin(ctx, workspaceID, actorID); err != nil {
		return err
	}
	name, query, err := s.validateServiceSLAGoal(ctx, workspaceID, name, query, goalMillis)
	if err != nil {
		return err
	}
	return s.Store.UpdateServiceSLAGoal(ctx, workspaceID, actorID, serviceDeskID, metricID, goalID, name, query, goalMillis)
}

func (s *Service) DeleteServiceSLAGoal(ctx context.Context, actorID, workspaceID, serviceDeskID, metricID, goalID string) error {
	if err := s.requireServiceAdmin(ctx, workspaceID, actorID); err != nil {
		return err
	}
	return s.Store.DeleteServiceSLAGoal(ctx, workspaceID, actorID, serviceDeskID, metricID, goalID)
}

func (s *Service) UpdateServiceCalendar(ctx context.Context, actorID, workspaceID, serviceDeskID, name, timeZone string, weekdays []int16, startMinute, endMinute int16) error {
	admin, err := s.Store.IsAdmin(ctx, workspaceID, actorID)
	if err != nil {
		return err
	}
	if !admin {
		return fmt.Errorf("only an administrator may configure service calendars")
	}
	return s.Store.UpdateServiceCalendar(ctx, workspaceID, actorID, serviceDeskID, name, timeZone, weekdays, startMinute, endMinute)
}

func (s *Service) UpsertServiceCalendarHoliday(ctx context.Context, actorID, workspaceID, serviceDeskID, day, name string) error {
	if err := s.requireServiceAdmin(ctx, workspaceID, actorID); err != nil {
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
	return s.Store.UpsertServiceCalendarHoliday(ctx, workspaceID, actorID, serviceDeskID, holiday, name)
}

func (s *Service) DeleteServiceCalendarHoliday(ctx context.Context, actorID, workspaceID, serviceDeskID, day string) error {
	if err := s.requireServiceAdmin(ctx, workspaceID, actorID); err != nil {
		return err
	}
	holiday, err := time.Parse(time.DateOnly, strings.TrimSpace(day))
	if err != nil {
		return fmt.Errorf("holiday date must use YYYY-MM-DD")
	}
	return s.Store.DeleteServiceCalendarHoliday(ctx, workspaceID, actorID, serviceDeskID, holiday)
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
	resolver := jql.DefaultResolver()
	fields, err := s.Store.CustomFieldsForWorkspace(ctx, workspaceID)
	if err != nil {
		return "", "", err
	}
	if compiled := jql.Compile(parsed, "validation", jql.WithCustomFields(resolver, fields)); compiled.Err != nil {
		return "", "", compiled.Err
	}
	return name, query, nil
}

func (s *Service) CreateServiceQueue(ctx context.Context, actorID, workspaceID, serviceDeskID, name, query string) (*models.ServiceQueue, error) {
	if err := s.requireServiceAdmin(ctx, workspaceID, actorID); err != nil {
		return nil, err
	}
	name, query, err := s.validateServiceQueue(ctx, workspaceID, name, query)
	if err != nil {
		return nil, err
	}
	return s.Store.CreateServiceQueue(ctx, workspaceID, actorID, serviceDeskID, name, query)
}

func (s *Service) UpdateServiceQueue(ctx context.Context, actorID, workspaceID, serviceDeskID, queueID, name, query string) error {
	if err := s.requireServiceAdmin(ctx, workspaceID, actorID); err != nil {
		return err
	}
	name, query, err := s.validateServiceQueue(ctx, workspaceID, name, query)
	if err != nil {
		return err
	}
	return s.Store.UpdateServiceQueue(ctx, workspaceID, actorID, serviceDeskID, queueID, name, query)
}

func (s *Service) DeleteServiceQueue(ctx context.Context, actorID, workspaceID, serviceDeskID, queueID string) error {
	if err := s.requireServiceAdmin(ctx, workspaceID, actorID); err != nil {
		return err
	}
	return s.Store.DeleteServiceQueue(ctx, workspaceID, actorID, serviceDeskID, queueID)
}

func (s *Service) SetServiceRequestTypeFields(ctx context.Context, actorID, workspaceID, serviceDeskID, requestTypeID string, fields []models.ServiceRequestTypeField) error {
	if err := s.requireServiceAdmin(ctx, workspaceID, actorID); err != nil {
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
		}
	}
	if !hasSummary {
		return fmt.Errorf("summary is required on every request type form")
	}
	return s.Store.SetServiceRequestTypeFields(ctx, workspaceID, actorID, serviceDeskID, requestTypeID, fields)
}
