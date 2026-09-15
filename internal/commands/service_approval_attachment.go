package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func (s *Service) CreateServiceApproval(ctx context.Context, actorID, workspaceID, issueIDOrKey, name string, approverIDs []string) (*models.ServiceApproval, error) {
	return s.createServiceApproval(ctx, actorID, workspaceID, issueIDOrKey, name, approverIDs, "")
}

func (s *Service) createServiceApproval(ctx context.Context, actorID, workspaceID, issueIDOrKey, name string, approverIDs []string, automationKey string) (*models.ServiceApproval, error) {
	canManage, err := s.Store.CanManageServiceRequest(ctx, workspaceID, actorID, issueIDOrKey)
	if err != nil {
		return nil, err
	}
	if !canManage {
		return nil, fmt.Errorf("only a service agent may request approval")
	}
	request, err := s.Store.ServiceRequest(ctx, workspaceID, actorID, issueIDOrKey, true)
	if err != nil {
		return nil, fmt.Errorf("request does not exist")
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 255 {
		return nil, fmt.Errorf("approval name is required and accepts at most 255 characters")
	}
	if len(approverIDs) == 0 {
		return nil, fmt.Errorf("at least one approver is required")
	}
	approval, created, err := s.Store.CreateServiceApproval(ctx, workspaceID, request.Issue.ID, actorID, name, approverIDs, automationKey)
	if err != nil {
		return nil, err
	}
	if created {
		if err := s.notifyServiceRequestUsers(ctx, actorID, workspaceID, request, approverIDs, "service_approval", "requested your approval on "+request.Issue.Key, false, models.CustomerNotificationApproval); err != nil {
			return nil, err
		}
	}
	return approval, nil
}

func (s *Service) AnswerServiceApproval(ctx context.Context, actorID, workspaceID, issueIDOrKey, approvalID, decision string) (*models.ServiceApproval, error) {
	decision = strings.ToLower(strings.TrimSpace(decision))
	if decision == "approve" {
		decision = "approved"
	}
	if decision == "decline" {
		decision = "declined"
	}
	if decision != "approved" && decision != "declined" {
		return nil, fmt.Errorf("decision must be approve or decline")
	}
	canManage, err := s.Store.CanManageServiceRequest(ctx, workspaceID, actorID, issueIDOrKey)
	if err != nil {
		return nil, err
	}
	request, err := s.Store.ServiceRequest(ctx, workspaceID, actorID, issueIDOrKey, canManage)
	if err != nil {
		return nil, fmt.Errorf("request does not exist")
	}
	approval, err := s.Store.AnswerServiceApproval(ctx, request.Issue.ID, approvalID, actorID, decision)
	if err != nil {
		return nil, err
	}
	if err := s.notifyServiceRequestSubscribers(ctx, actorID, workspaceID, request, "service_approval", decision+" approval on "+request.Issue.Key, false); err != nil {
		return nil, err
	}
	// A workflow status's approval runs the transition its configuration names
	// for the decision, while the request still waits in that status.
	transitionID := ""
	switch approval.FinalDecision {
	case "approved":
		transitionID = approval.TransitionApproved
	case "declined":
		transitionID = approval.TransitionRejected
	}
	if transitionID != "" {
		current, err := s.Store.IssueByIDOrKey(ctx, workspaceID, request.Issue.ID)
		if err != nil {
			return nil, err
		}
		if current.Status.ID == approval.StatusID {
			if _, err := s.TransitionServiceRequest(ctx, actorID, workspaceID, request.Issue.ID, transitionID); err != nil {
				return nil, fmt.Errorf("the approval was recorded, but its %s transition failed: %w", approval.FinalDecision, err)
			}
		}
	}
	return approval, nil
}

// ErrServiceDeskNotFound reports a service desk that does not exist.
var ErrServiceDeskNotFound = errors.New("service desk does not exist")

// ErrServiceAttachmentPermission reports a caller who may not add attachments
// in a service desk.
var ErrServiceAttachmentPermission = errors.New("permission to add attachments in this service desk is required")

func (s *Service) CreateServiceTemporaryAttachment(ctx context.Context, actorID, workspaceID, serviceDeskID, filename, mimeType string, r io.Reader) (*models.ServiceTemporaryAttachment, error) {
	if s.Blobs == nil {
		return nil, fmt.Errorf("attachment storage not configured")
	}
	desk, err := s.Store.ServiceDesk(ctx, workspaceID, serviceDeskID)
	if err != nil {
		return nil, ErrServiceDeskNotFound
	}
	allowed, err := s.Store.CanCreateServiceRequest(ctx, workspaceID, serviceDeskID, actorID)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrServiceAttachmentPermission
	}
	if !desk.AttachmentsEnabled {
		return nil, fmt.Errorf("%w: attachments are turned off for this service desk", ErrServiceAttachmentPermission)
	}
	configuration, err := s.jiraSiteConfiguration(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if !configuration.AttachmentsEnabled {
		return nil, fmt.Errorf("%w: attachments are disabled for this site", ErrServiceAttachmentPermission)
	}
	limit := configuration.AttachmentUploadLimit
	if limit <= 0 {
		limit = 32 << 20
	}
	filename, err = normalizedAttachmentFilename(filename)
	if err != nil {
		return nil, err
	}
	mimeType, err = normalizedAttachmentMIMEType(mimeType)
	if err != nil {
		return nil, err
	}
	value := &models.ServiceTemporaryAttachment{ID: store.NewID("temp"), WorkspaceID: workspaceID, ServiceDeskID: serviceDeskID, AuthorID: actorID, Filename: filename, MimeType: mimeType, BlobRef: store.NewID("blob")}
	value.Size, err = s.Blobs.Put(ctx, value.BlobRef, io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if value.Size > limit {
		if cleanupErr := s.Blobs.Delete(ctx, value.BlobRef); cleanupErr != nil {
			return nil, errors.Join(ErrAttachmentTooLarge, cleanupErr)
		}
		return nil, ErrAttachmentTooLarge
	}
	if err := s.Store.CreateServiceTemporaryAttachment(ctx, *value); err != nil {
		cleanupErr := s.Blobs.Delete(ctx, value.BlobRef)
		if cleanupErr != nil {
			return nil, fmt.Errorf("%v; remove temporary blob: %w", err, cleanupErr)
		}
		return nil, err
	}
	return value, nil
}

func (s *Service) CreateServiceAttachmentComment(ctx context.Context, actorID, workspaceID, issueIDOrKey string, temporaryIDs []string, body string, public bool) ([]models.ServiceRequestAttachment, *models.ServiceRequestComment, error) {
	canManage, err := s.Store.CanManageServiceRequest(ctx, workspaceID, actorID, issueIDOrKey)
	if err != nil {
		return nil, nil, err
	}
	request, err := s.Store.ServiceRequest(ctx, workspaceID, actorID, issueIDOrKey, canManage)
	if err != nil {
		return nil, nil, fmt.Errorf("request does not exist")
	}
	if !canManage && !public {
		return nil, nil, fmt.Errorf("customers may only add public attachments")
	}
	if len(temporaryIDs) == 0 {
		return nil, nil, fmt.Errorf("at least one temporary attachment is required")
	}
	if strings.TrimSpace(body) == "" {
		body = "Added attachments."
	}
	comment, err := s.addServiceRequestComment(ctx, actorID, workspaceID, request.Issue.ID, json.RawMessage(nil), body, public, false)
	if err != nil {
		return nil, nil, err
	}
	values, err := s.Store.FinalizeServiceAttachments(ctx, actorID, workspaceID, request.Issue.ID, comment.Comment.ID, temporaryIDs, public)
	if err != nil {
		_, cleanupErr := s.DeleteComment(ctx, actorID, workspaceID, comment.Comment.ID)
		if cleanupErr != nil {
			return nil, nil, fmt.Errorf("%v; remove attachment comment: %w", err, cleanupErr)
		}
		return nil, nil, err
	}
	comment.Attachments = make([]models.Attachment, 0, len(values))
	for _, value := range values {
		comment.Attachments = append(comment.Attachments, value.Attachment)
	}
	if err := s.afterServiceRequestComment(ctx, actorID, workspaceID, request, public, canManage); err != nil {
		return nil, nil, err
	}
	return values, comment, nil
}

// startStatusApproval opens the approval a workflow status configures when a
// service request enters it. Its approvers are the users in the configured
// user picker field, less the excluded assignee or reporter.
func (s *Service) startStatusApproval(ctx context.Context, actorID, workspaceID string, issue *models.Issue) error {
	deskID, err := s.Store.ServiceRequestDeskID(ctx, workspaceID, issue.ID)
	if err != nil || deskID == "" {
		return err
	}
	wf, err := s.Store.WorkflowForProjectAndIssueType(ctx, issue.ProjectID, issue.IssueType.ID)
	if err != nil {
		return err
	}
	configuration := wf.StatusApproval(issue.Status.ID)
	if configuration == nil {
		return nil
	}
	excluded := map[string]bool{}
	for _, role := range configuration.Exclude {
		if role == "assignee" && issue.Assignee != nil {
			excluded[issue.Assignee.ID] = true
		}
		if role == "reporter" && issue.Reporter != nil {
			excluded[issue.Reporter.ID] = true
		}
	}
	approverIDs := []string{}
	for _, id := range userPickerAccountIDs(issue.Fields[configuration.FieldID]) {
		if !excluded[id] {
			approverIDs = append(approverIDs, id)
		}
	}
	if len(approverIDs) == 0 {
		return nil
	}
	conditionValue, err := strconv.Atoi(configuration.ConditionValue)
	if err != nil {
		return err
	}
	rule := models.ServiceApproval{StatusID: issue.Status.ID, ConditionType: configuration.ConditionType, ConditionValue: conditionValue, TransitionApproved: configuration.TransitionApproved, TransitionRejected: configuration.TransitionRejected}
	// Each entry into the status opens its own approval.
	key := fmt.Sprintf("workflow-status:%s:%d", issue.Status.ID, issue.UpdatedSeq)
	_, created, err := s.Store.CreateStatusServiceApproval(ctx, workspaceID, issue.ID, actorID, issue.Status.Name, approverIDs, key, rule)
	if err != nil || !created {
		return err
	}
	request, err := s.Store.ServiceRequest(ctx, workspaceID, actorID, issue.ID, true)
	if err != nil {
		return err
	}
	return s.notifyServiceRequestUsers(ctx, actorID, workspaceID, request, approverIDs, "service_approval", "requested your approval on "+issue.Key, false, models.CustomerNotificationApproval)
}

// userPickerAccountIDs reads the account IDs a user or multi-user picker
// field stores.
func userPickerAccountIDs(raw json.RawMessage) []string {
	var single string
	if json.Unmarshal(raw, &single) == nil && single != "" {
		return []string{single}
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return many
	}
	return nil
}
