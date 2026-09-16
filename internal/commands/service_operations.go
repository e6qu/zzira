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

func (s *Service) CreateServiceEscalationStep(ctx context.Context, actorID, workspaceID, deskID, targetUserID string, delayMinutes int) error {
	if strings.TrimSpace(targetUserID) == "" || delayMinutes < 1 || delayMinutes > 10080 {
		return fmt.Errorf("escalation target and a delay between 1 and 10080 minutes are required")
	}
	return s.Store.CreateServiceEscalationStep(ctx, workspaceID, actorID, deskID, targetUserID, delayMinutes)
}

func (s *Service) DeleteServiceEscalationStep(ctx context.Context, actorID, workspaceID, deskID, stepID string) error {
	if strings.TrimSpace(stepID) == "" {
		return fmt.Errorf("escalation step is required")
	}
	return s.Store.DeleteServiceEscalationStep(ctx, workspaceID, actorID, deskID, stepID)
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
	if profile.Kind != "incident" {
		profile.MajorIncident = false
	}
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

func (s *Service) CreateServiceIncidentUpdate(ctx context.Context, actorID, workspaceID, issueIDOrKey, audience, message string) (*models.ServiceIncidentUpdate, error) {
	audience = strings.TrimSpace(audience)
	message = strings.TrimSpace(message)
	if audience != "public" && audience != "internal" && audience != "stakeholders" {
		return nil, fmt.Errorf("incident update audience must be public, internal or stakeholders")
	}
	if message == "" || len(message) > 10000 {
		return nil, fmt.Errorf("incident update must contain 1 to 10000 characters")
	}
	canManage, err := s.Store.CanManageServiceRequest(ctx, workspaceID, actorID, issueIDOrKey)
	if err != nil || !canManage {
		return nil, fmt.Errorf("service agent access is required")
	}
	request, err := s.Store.ServiceRequest(ctx, workspaceID, actorID, issueIDOrKey, true)
	if err != nil {
		return nil, fmt.Errorf("request does not exist")
	}
	profile, err := s.Store.ServiceOperationsProfile(ctx, workspaceID, request.Issue.ID)
	if err != nil || profile.Kind != "incident" || !profile.MajorIncident {
		return nil, fmt.Errorf("declare this request as a major incident before publishing updates")
	}
	update, err := s.Store.CreateServiceIncidentUpdate(ctx, workspaceID, actorID, request.Issue.ID, audience, message)
	if err != nil {
		return nil, err
	}
	if audience == "stakeholders" {
		// Stakeholder updates stay with the response team on the request and
		// reach stakeholders by email.
		if err := s.notifyServiceRequestSubscribers(ctx, actorID, workspaceID, request, "service_incident_update", "published a stakeholder update on "+request.Issue.Key, true); err != nil {
			return nil, err
		}
		if err := s.emailServiceIncidentStakeholders(ctx, actorID, workspaceID, request, message); err != nil {
			return nil, err
		}
		return update, nil
	}
	if err := s.notifyServiceRequestSubscribers(ctx, actorID, workspaceID, request, "service_incident_update", "published a "+audience+" incident update on "+request.Issue.Key, audience == "internal"); err != nil {
		return nil, err
	}
	return update, nil
}

// emailServiceIncidentStakeholders sends a stakeholder update to every
// stakeholder address once.
func (s *Service) emailServiceIncidentStakeholders(ctx context.Context, actorID, workspaceID string, request *models.ServiceRequest, message string) error {
	actor, err := s.Store.UserByID(ctx, actorID)
	if err != nil {
		return err
	}
	emails, err := s.Store.ServiceIncidentStakeholderEmails(ctx, request.Issue.ID)
	if err != nil {
		return err
	}
	subject := "[" + request.Issue.Key + "] Stakeholder update: " + request.Issue.Summary
	body := "Stakeholder update on " + request.Issue.Key + " — " + request.Issue.Summary + "\n\n" + message + "\n\nFrom " + actor.DisplayName
	for _, email := range emails {
		if err := s.Store.QueueEmail(ctx, workspaceID, email, subject, body); err != nil {
			return err
		}
	}
	return nil
}

// agentServiceRequest finds a request its agent is changing.
func (s *Service) agentServiceRequest(ctx context.Context, actorID, workspaceID, issueIDOrKey string) (*models.ServiceRequest, error) {
	canManage, err := s.Store.CanManageServiceRequest(ctx, workspaceID, actorID, issueIDOrKey)
	if err != nil || !canManage {
		return nil, fmt.Errorf("service agent access is required")
	}
	request, err := s.Store.ServiceRequest(ctx, workspaceID, actorID, issueIDOrKey, true)
	if err != nil {
		return nil, fmt.Errorf("request does not exist")
	}
	return request, nil
}

// SetServiceIncidentRole gives a major incident role to an agent, or clears
// it, and tells the new holder.
func (s *Service) SetServiceIncidentRole(ctx context.Context, actorID, workspaceID, issueIDOrKey, role, userID string) error {
	request, err := s.agentServiceRequest(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return err
	}
	userID = strings.TrimSpace(userID)
	if err := s.Store.SetServiceIncidentRole(ctx, workspaceID, actorID, request.Issue.ID, role, userID); err != nil {
		return err
	}
	if userID == "" {
		return nil
	}
	for _, definition := range models.ServiceIncidentRoleDefinitions {
		if definition.Key == role {
			return s.notifyServiceRequestUsers(ctx, actorID, workspaceID, request, []string{userID}, "service_incident_role", "made you "+definition.Name+" on "+request.Issue.Key, true)
		}
	}
	return nil
}

// AddServiceIncidentStakeholder adds a site member or an email address as a
// stakeholder of a major incident.
func (s *Service) AddServiceIncidentStakeholder(ctx context.Context, actorID, workspaceID, issueIDOrKey, userID, email string) error {
	request, err := s.agentServiceRequest(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return err
	}
	return s.Store.AddServiceIncidentStakeholder(ctx, workspaceID, actorID, request.Issue.ID, strings.TrimSpace(userID), email)
}

// RemoveServiceIncidentStakeholder removes a stakeholder from a request.
func (s *Service) RemoveServiceIncidentStakeholder(ctx context.Context, actorID, workspaceID, issueIDOrKey, stakeholderID string) error {
	request, err := s.agentServiceRequest(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return err
	}
	return s.Store.RemoveServiceIncidentStakeholder(ctx, workspaceID, actorID, request.Issue.ID, stakeholderID)
}
