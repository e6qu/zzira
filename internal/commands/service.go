package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

type CreateServiceRequestInput struct {
	ActorID, WorkspaceID, ServiceDeskID, RequestTypeID string
	CustomerID, Channel, Summary, Description          string
	DescriptionADF                                     json.RawMessage
	ParticipantIDs                                     []string
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
	labels := []string{}
	for _, group := range requestType.GroupIDs {
		if strings.EqualFold(group, "incidents") {
			labels = append(labels, "incident")
		}
	}
	if strings.Contains(strings.ToLower(requestType.Name), "incident") {
		labels = append(labels, "incident")
	}
	issue, _, err := s.CreateIssue(ctx, CreateIssueInput{
		ActorID: in.ActorID, ReporterID: in.CustomerID, WorkspaceID: in.WorkspaceID, ProjectIDOrKey: desk.ProjectID,
		Summary: in.Summary, Description: in.Description, DescriptionADF: in.DescriptionADF,
		IssueTypeID: requestType.IssueTypeID, Labels: labels,
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
	agent, err := s.Store.IsAnyServiceAgent(ctx, workspaceID, actorID)
	if err != nil {
		return nil, err
	}
	if !agent {
		return nil, fmt.Errorf("only a service agent may create customers")
	}
	email, displayName = strings.TrimSpace(strings.ToLower(email)), strings.TrimSpace(displayName)
	if email == "" || !strings.Contains(email, "@") || displayName == "" || len(displayName) > 255 {
		return nil, fmt.Errorf("a valid email and display name are required")
	}
	return s.Store.CreateServiceCustomer(ctx, workspaceID, email, displayName)
}

func (s *Service) AddServiceRequestComment(ctx context.Context, actorID, workspaceID, issueIDOrKey string, body json.RawMessage, plainText string, public bool) (*models.ServiceRequestComment, error) {
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
	if public && canManage {
		if err := s.Store.CompleteServiceSLA(ctx, workspaceID, request.Issue.ID, "first_response", time.Now().UTC()); err != nil {
			return nil, err
		}
	}
	return &models.ServiceRequestComment{Comment: *comment, Public: public}, nil
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
	updated, _, err := s.TransitionIssue(ctx, actorID, workspaceID, request.Issue.ID, transitionID)
	if err != nil {
		return nil, err
	}
	if updated.Status.Category == "done" {
		if err := s.Store.CompleteServiceSLA(ctx, workspaceID, request.Issue.ID, "resolution", time.Now().UTC()); err != nil {
			return nil, err
		}
	} else if err := s.Store.EnsureResolutionSLA(ctx, workspaceID, request.Issue.ID, time.Now().UTC()); err != nil {
		return nil, err
	}
	return s.Store.ServiceRequest(ctx, workspaceID, actorID, request.Issue.ID, canManage)
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

func (s *Service) UpdateServiceSLAMetric(ctx context.Context, actorID, workspaceID, serviceDeskID, metricID string, goalMillis int64) error {
	admin, err := s.Store.IsAdmin(ctx, workspaceID, actorID)
	if err != nil {
		return err
	}
	if !admin {
		return fmt.Errorf("only an administrator may configure service SLAs")
	}
	return s.Store.UpdateServiceSLAMetric(ctx, workspaceID, actorID, serviceDeskID, metricID, goalMillis)
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
