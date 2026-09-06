package commands

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func (s *Service) SetServiceRequestSubscription(ctx context.Context, actorID, workspaceID, issueIDOrKey string, subscribed bool) error {
	canManage, err := s.Store.CanManageServiceRequest(ctx, workspaceID, actorID, issueIDOrKey)
	if err != nil {
		return err
	}
	request, err := s.Store.ServiceRequest(ctx, workspaceID, actorID, issueIDOrKey, canManage)
	if err != nil {
		return fmt.Errorf("request does not exist")
	}
	return s.Store.SetServiceRequestSubscription(ctx, request.Issue.ID, actorID, subscribed)
}

func (s *Service) notifyServiceRequestUsers(ctx context.Context, actorID, workspaceID string, request *models.ServiceRequest, userIDs []string, kind, message string, agentsOnly bool) error {
	actor, err := s.Store.UserByID(ctx, actorID)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, userID := range userIDs {
		if userID == actorID || seen[userID] {
			continue
		}
		seen[userID] = true
		canManage, err := s.Store.CanManageServiceRequest(ctx, workspaceID, userID, request.Issue.ID)
		if err != nil {
			return err
		}
		if agentsOnly && !canManage {
			continue
		}
		if _, err := s.Store.ServiceRequest(ctx, workspaceID, userID, request.Issue.ID, canManage); err != nil {
			continue
		}
		if _, err := s.Store.CreateNotification(ctx, workspaceID, &models.Notification{
			ID: store.NewID("ntf"), TargetUser: userID, ActorID: actorID, ActorName: actor.DisplayName,
			Kind: kind, EntityType: models.EntityServiceRequest, EntityID: request.Issue.Key, Message: message,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) notifyServiceRequestSubscribers(ctx context.Context, actorID, workspaceID string, request *models.ServiceRequest, kind, message string, agentsOnly bool) error {
	userIDs, err := s.Store.ServiceRequestSubscriberIDs(ctx, request.Issue.ID)
	if err != nil {
		return err
	}
	return s.notifyServiceRequestUsers(ctx, actorID, workspaceID, request, userIDs, kind, message, agentsOnly)
}

func (s *Service) PutServiceRequestFeedback(ctx context.Context, actorID, workspaceID, issueIDOrKey, feedbackType string, rating int, comment string) (*models.ServiceRequestFeedback, error) {
	canManage, err := s.Store.CanManageServiceRequest(ctx, workspaceID, actorID, issueIDOrKey)
	if err != nil {
		return nil, err
	}
	request, err := s.Store.ServiceRequest(ctx, workspaceID, actorID, issueIDOrKey, canManage)
	if err != nil {
		return nil, fmt.Errorf("request does not exist")
	}
	if request.Customer.ID != actorID {
		return nil, fmt.Errorf("only the reporter may leave feedback")
	}
	feedbackType = strings.ToLower(strings.TrimSpace(feedbackType))
	if feedbackType != "csat" {
		return nil, fmt.Errorf("feedback type must be csat")
	}
	if rating < 1 || rating > 5 {
		return nil, fmt.Errorf("rating must be between 1 and 5")
	}
	comment = strings.TrimSpace(comment)
	if len(comment) > 4000 {
		return nil, fmt.Errorf("feedback comment accepts at most 4000 characters")
	}
	feedback, err := s.Store.PutServiceRequestFeedback(ctx, workspaceID, request.Issue.ID, actorID, feedbackType, rating, comment)
	if err != nil {
		return nil, err
	}
	if err := s.notifyServiceRequestSubscribers(ctx, actorID, workspaceID, request, "service_feedback", "rated "+request.Issue.Key+" "+strconv.Itoa(rating)+" out of 5", true); err != nil {
		return nil, err
	}
	return feedback, nil
}

func (s *Service) DeleteServiceRequestFeedback(ctx context.Context, actorID, workspaceID, issueIDOrKey string) error {
	canManage, err := s.Store.CanManageServiceRequest(ctx, workspaceID, actorID, issueIDOrKey)
	if err != nil {
		return err
	}
	request, err := s.Store.ServiceRequest(ctx, workspaceID, actorID, issueIDOrKey, canManage)
	if err != nil {
		return fmt.Errorf("request does not exist")
	}
	if request.Customer.ID != actorID {
		return fmt.Errorf("only the reporter may delete feedback")
	}
	return s.Store.DeleteServiceRequestFeedback(ctx, request.Issue.ID, actorID)
}
