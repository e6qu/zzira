package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
		admin, adminErr := s.Store.IsAdmin(ctx, in.WorkspaceID, in.ActorID)
		if adminErr != nil {
			return nil, adminErr
		}
		if !admin {
			return nil, fmt.Errorf("only an administrator may raise a request for another customer")
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
	admin, err := s.Store.IsAdmin(ctx, in.WorkspaceID, in.ActorID)
	if err != nil {
		return nil, err
	}
	return s.Store.ServiceRequest(ctx, in.WorkspaceID, in.ActorID, issue.ID, admin)
}

func (s *Service) UpdateServiceRequestParticipants(ctx context.Context, actorID, workspaceID, issueIDOrKey string, userIDs []string, remove bool) ([]*models.User, error) {
	admin, err := s.Store.IsAdmin(ctx, workspaceID, actorID)
	if err != nil {
		return nil, err
	}
	request, err := s.Store.ServiceRequest(ctx, workspaceID, actorID, issueIDOrKey, admin)
	if err != nil {
		return nil, fmt.Errorf("request does not exist")
	}
	if !admin && request.Customer.ID != actorID {
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
		return nil, fmt.Errorf("only an administrator may create customers")
	}
	email, displayName = strings.TrimSpace(strings.ToLower(email)), strings.TrimSpace(displayName)
	if email == "" || !strings.Contains(email, "@") || displayName == "" || len(displayName) > 255 {
		return nil, fmt.Errorf("a valid email and display name are required")
	}
	return s.Store.CreateServiceCustomer(ctx, workspaceID, email, displayName)
}

func (s *Service) AddServiceRequestComment(ctx context.Context, actorID, workspaceID, issueIDOrKey string, body json.RawMessage, plainText string, public bool) (*models.ServiceRequestComment, error) {
	admin, err := s.Store.IsAdmin(ctx, workspaceID, actorID)
	if err != nil {
		return nil, err
	}
	request, err := s.Store.ServiceRequest(ctx, workspaceID, actorID, issueIDOrKey, admin)
	if err != nil {
		return nil, fmt.Errorf("request does not exist")
	}
	if !admin && !public {
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
	return &models.ServiceRequestComment{Comment: *comment, Public: public}, nil
}

func (s *Service) TransitionServiceRequest(ctx context.Context, actorID, workspaceID, issueIDOrKey, transitionID string) (*models.ServiceRequest, error) {
	admin, err := s.Store.IsAdmin(ctx, workspaceID, actorID)
	if err != nil {
		return nil, err
	}
	request, err := s.Store.ServiceRequest(ctx, workspaceID, actorID, issueIDOrKey, admin)
	if err != nil {
		return nil, fmt.Errorf("request does not exist")
	}
	if _, _, err := s.TransitionIssue(ctx, actorID, workspaceID, request.Issue.ID, transitionID); err != nil {
		return nil, err
	}
	return s.Store.ServiceRequest(ctx, workspaceID, actorID, request.Issue.ID, admin)
}
