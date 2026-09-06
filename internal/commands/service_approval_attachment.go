package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func (s *Service) CreateServiceApproval(ctx context.Context, actorID, workspaceID, issueIDOrKey, name string, approverIDs []string) (*models.ServiceApproval, error) {
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
	return s.Store.CreateServiceApproval(ctx, workspaceID, request.Issue.ID, actorID, name, approverIDs)
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
	return s.Store.AnswerServiceApproval(ctx, request.Issue.ID, approvalID, actorID, decision)
}

func (s *Service) CreateServiceTemporaryAttachment(ctx context.Context, actorID, workspaceID, serviceDeskID, filename, mimeType string, r io.Reader) (*models.ServiceTemporaryAttachment, error) {
	if s.Blobs == nil {
		return nil, fmt.Errorf("attachment storage not configured")
	}
	if _, err := s.Store.ServiceDesk(ctx, workspaceID, serviceDeskID); err != nil {
		return nil, fmt.Errorf("service desk does not exist")
	}
	agent, err := s.Store.IsServiceAgent(ctx, workspaceID, serviceDeskID, actorID)
	if err != nil {
		return nil, err
	}
	if !agent {
		if _, err := s.Store.ServiceCustomer(ctx, workspaceID, actorID); err != nil {
			return nil, fmt.Errorf("permission to add attachments in this service desk is required")
		}
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
	value.Size, err = s.Blobs.Put(ctx, value.BlobRef, r)
	if err != nil {
		return nil, err
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
	comment, err := s.AddServiceRequestComment(ctx, actorID, workspaceID, request.Issue.ID, json.RawMessage(nil), body, public)
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
	return values, comment, nil
}
