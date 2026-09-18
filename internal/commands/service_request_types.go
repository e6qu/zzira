package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

// serviceRequestTypeText checks the name, description and help text Jira
// accepts for a request type: a name on one line, and text fields of at most
// 255 characters.
func serviceRequestTypeText(name, description, helpText string) (string, string, string, error) {
	name, description, helpText = strings.TrimSpace(name), strings.TrimSpace(description), strings.TrimSpace(helpText)
	if name == "" || len(name) > 255 || strings.ContainsAny(name, "\r\n") {
		return "", "", "", fmt.Errorf("the request type name must be 1 to 255 characters on one line")
	}
	if len(description) > 255 {
		return "", "", "", fmt.Errorf("the request type description accepts at most 255 characters")
	}
	if len(helpText) > 255 {
		return "", "", "", fmt.Errorf("the request type help text accepts at most 255 characters")
	}
	return name, description, helpText, nil
}

// CreateServiceRequestType adds a request type to a service desk, based on one
// of the desk project's work types. Jira's REST create leaves the groups
// empty, which keeps the request type off the customer portal until an
// administrator puts it in one; the agent workspace passes its groups here.
func (s *Service) CreateServiceRequestType(ctx context.Context, actorID, workspaceID, serviceDeskID, name, description, helpText, issueTypeID string, groupIDs []string) (*models.ServiceRequestType, error) {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return nil, err
	}
	name, description, helpText, err := serviceRequestTypeText(name, description, helpText)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(issueTypeID) == "" {
		return nil, fmt.Errorf("a request type is based on a work type")
	}
	return s.Store.CreateServiceRequestType(ctx, workspaceID, actorID, serviceDeskID, name, description, helpText, strings.TrimSpace(issueTypeID), groupIDs)
}

// UpdateServiceRequestType changes the name, description and help text a
// request type shows on the portal. Jira's service desk administrators edit
// these in the project's request type settings; there is no REST operation.
func (s *Service) UpdateServiceRequestType(ctx context.Context, actorID, workspaceID, serviceDeskID, requestTypeID, name, description, helpText string) error {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return err
	}
	name, description, helpText, err := serviceRequestTypeText(name, description, helpText)
	if err != nil {
		return err
	}
	return s.Store.UpdateServiceRequestType(ctx, workspaceID, actorID, serviceDeskID, requestTypeID, name, description, helpText)
}

// DeleteServiceRequestType removes a request type. The requests raised with it
// remain, without a request type.
func (s *Service) DeleteServiceRequestType(ctx context.Context, actorID, workspaceID, serviceDeskID, requestTypeID string) error {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return err
	}
	return s.Store.DeleteServiceRequestType(ctx, workspaceID, actorID, serviceDeskID, requestTypeID)
}

// SetServiceRequestTypeGroups puts a request type in the portal groups the
// administrator chose. A request type in no group is not visible on the
// customer portal.
func (s *Service) SetServiceRequestTypeGroups(ctx context.Context, actorID, workspaceID, serviceDeskID, requestTypeID string, groupIDs []string) error {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return err
	}
	return s.Store.SetServiceRequestTypeGroups(ctx, workspaceID, actorID, serviceDeskID, requestTypeID, groupIDs)
}

// MoveServiceRequestType moves a request type one place up or down inside a
// portal group, which is the order the portal lists them in.
func (s *Service) MoveServiceRequestType(ctx context.Context, actorID, workspaceID, serviceDeskID, groupID, requestTypeID, direction string) error {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return err
	}
	if direction != "up" && direction != "down" {
		return fmt.Errorf("a request type moves up or down")
	}
	return s.Store.MoveServiceRequestType(ctx, workspaceID, actorID, serviceDeskID, groupID, requestTypeID, direction)
}

// serviceRequestTypeGroupName checks the name of a portal group.
func serviceRequestTypeGroupName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 255 || strings.ContainsAny(name, "\r\n") {
		return "", fmt.Errorf("the group name must be 1 to 255 characters on one line")
	}
	return name, nil
}

// CreateServiceRequestTypeGroup adds a customer request type group, the
// heading the portal groups request types under.
func (s *Service) CreateServiceRequestTypeGroup(ctx context.Context, actorID, workspaceID, serviceDeskID, name string) (*models.ServiceRequestTypeGroup, error) {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return nil, err
	}
	name, err := serviceRequestTypeGroupName(name)
	if err != nil {
		return nil, err
	}
	return s.Store.CreateServiceRequestTypeGroup(ctx, workspaceID, actorID, serviceDeskID, name)
}

// RenameServiceRequestTypeGroup changes a group's heading on the portal.
func (s *Service) RenameServiceRequestTypeGroup(ctx context.Context, actorID, workspaceID, serviceDeskID, groupID, name string) error {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return err
	}
	name, err := serviceRequestTypeGroupName(name)
	if err != nil {
		return err
	}
	return s.Store.RenameServiceRequestTypeGroup(ctx, workspaceID, actorID, serviceDeskID, groupID, name)
}

// DeleteServiceRequestTypeGroup removes a group from the portal. The group
// must be empty, because deleting it would otherwise take its request types
// off the portal with it.
func (s *Service) DeleteServiceRequestTypeGroup(ctx context.Context, actorID, workspaceID, serviceDeskID, groupID string) error {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return err
	}
	return s.Store.DeleteServiceRequestTypeGroup(ctx, workspaceID, actorID, serviceDeskID, groupID)
}

// MoveServiceRequestTypeGroup moves a group one place up or down, the
// arbitrary order Jira Service Management shows the groups in on the portal.
func (s *Service) MoveServiceRequestTypeGroup(ctx context.Context, actorID, workspaceID, serviceDeskID, groupID, direction string) error {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return err
	}
	if direction != "up" && direction != "down" {
		return fmt.Errorf("a group moves up or down")
	}
	return s.Store.MoveServiceRequestTypeGroup(ctx, workspaceID, actorID, serviceDeskID, groupID, direction)
}
