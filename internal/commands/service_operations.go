package commands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

func (s *Service) UpdateServiceOperationsSettings(ctx context.Context, actorID, workspaceID, deskID string, threshold, reviewDays int, cabIDs []string) error {
	if threshold < 1 || threshold > 16 || reviewDays < 1 || reviewDays > 90 {
		return fmt.Errorf("CAB threshold must be 1–16 and review due days must be 1–90")
	}
	return s.Store.UpdateServiceOperationsSettings(ctx, workspaceID, actorID, deskID, threshold, reviewDays, cabIDs)
}

func (s *Service) CreateServiceOnCallShift(ctx context.Context, actorID, workspaceID, deskID, userID, label string, startsAt, endsAt time.Time) error {
	label = strings.TrimSpace(label)
	if label == "" || len(label) > 255 || !endsAt.After(startsAt) {
		return fmt.Errorf("on-call label and a valid start/end window are required")
	}
	return s.Store.CreateServiceOnCallShift(ctx, workspaceID, actorID, deskID, userID, label, startsAt, endsAt)
}

func (s *Service) DeleteServiceOnCallShift(ctx context.Context, actorID, workspaceID, deskID, shiftID string) error {
	return s.Store.DeleteServiceOnCallShift(ctx, workspaceID, actorID, deskID, shiftID)
}

func (s *Service) UpdateServiceOperationsProfile(ctx context.Context, actorID, workspaceID, issueIDOrKey string, profile models.ServiceOperationsProfile) (*models.ServiceOperationsProfile, error) {
	canManage, err := s.Store.CanManageServiceRequest(ctx, workspaceID, actorID, issueIDOrKey)
	if err != nil || !canManage {
		return nil, fmt.Errorf("service agent access is required")
	}
	request, err := s.Store.ServiceRequest(ctx, workspaceID, actorID, issueIDOrKey, true)
	if err != nil {
		return nil, fmt.Errorf("request does not exist")
	}
	current, err := s.Store.ServiceOperationsProfile(ctx, workspaceID, request.Issue.ID)
	if err != nil {
		return nil, fmt.Errorf("operations profile does not exist")
	}
	profile.RequestIssueID, profile.Kind = current.RequestIssueID, current.Kind
	if profile.Impact < 1 || profile.Impact > 4 || profile.Likelihood < 1 || profile.Likelihood > 4 {
		return nil, fmt.Errorf("impact and likelihood must be between 1 and 4")
	}
	if profile.ChangeType != "standard" && profile.ChangeType != "normal" && profile.ChangeType != "emergency" {
		return nil, fmt.Errorf("choose a valid change type")
	}
	if profile.PlannedStart != nil && profile.PlannedEnd != nil && !profile.PlannedEnd.After(*profile.PlannedStart) {
		return nil, fmt.Errorf("planned end must be after planned start")
	}
	if profile.ReviewStatus != "not_required" && profile.ReviewStatus != "pending" && profile.ReviewStatus != "in_progress" && profile.ReviewStatus != "completed" {
		return nil, fmt.Errorf("choose a valid review status")
	}
	if len(profile.RollbackPlan) > 10000 || len(profile.ReviewSummary) > 10000 {
		return nil, fmt.Errorf("operations notes accept at most 10000 characters")
	}
	if profile.OnCallUserID != "" {
		members, err := s.Store.MembersByWorkspace(ctx, workspaceID)
		if err != nil {
			return nil, err
		}
		found := false
		for _, member := range members {
			if member.ID == profile.OnCallUserID {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("on-call owner must be an active workspace member")
		}
	}
	settings, err := s.Store.ServiceOperationsSettings(ctx, workspaceID, request.ServiceDesk.ID)
	if err != nil {
		return nil, err
	}
	if profile.Kind == "change" && profile.Impact*profile.Likelihood >= settings.CABRiskThreshold && len(settings.CABMembers) == 0 {
		return nil, fmt.Errorf("configure at least one CAB member before saving a change at this risk")
	}
	if err := s.Store.UpdateServiceOperationsProfile(ctx, workspaceID, actorID, request.Issue.ID, profile); err != nil {
		return nil, err
	}
	if profile.Kind == "change" && profile.Impact*profile.Likelihood >= settings.CABRiskThreshold {
		ids := make([]string, 0, len(settings.CABMembers))
		for _, member := range settings.CABMembers {
			ids = append(ids, member.ID)
		}
		if _, err := s.createServiceApproval(ctx, actorID, workspaceID, request.Issue.ID, "Change advisory board", ids, "cab-risk-threshold"); err != nil {
			return nil, err
		}
	}
	return s.Store.ServiceOperationsProfile(ctx, workspaceID, request.Issue.ID)
}
