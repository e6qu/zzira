package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

func (s *Service) requireAnyServiceAgent(ctx context.Context, workspaceID, actorID string) error {
	agent, err := s.Store.IsAnyServiceAgent(ctx, workspaceID, actorID)
	if err != nil {
		return err
	}
	if !agent {
		return fmt.Errorf("service agent access is required")
	}
	return nil
}

func (s *Service) requireServiceDeskAgent(ctx context.Context, workspaceID, serviceDeskID, actorID string) error {
	agent, err := s.Store.IsServiceAgent(ctx, workspaceID, serviceDeskID, actorID)
	if err != nil {
		return err
	}
	if !agent {
		return fmt.Errorf("service agent access is required")
	}
	return nil
}

func (s *Service) requireServiceAdmin(ctx context.Context, workspaceID, actorID string) error {
	admin, err := s.Store.IsAdmin(ctx, workspaceID, actorID)
	if err != nil {
		return err
	}
	if !admin {
		return fmt.Errorf("site administrator access is required")
	}
	return nil
}

func (s *Service) CreateServiceOrganization(ctx context.Context, actorID, workspaceID, name string) (*models.ServiceOrganization, error) {
	if err := s.requireAnyServiceAgent(ctx, workspaceID, actorID); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 255 {
		return nil, fmt.Errorf("organization name is required and accepts at most 255 characters")
	}
	return s.Store.CreateServiceOrganization(ctx, workspaceID, name)
}

func (s *Service) DeleteServiceOrganization(ctx context.Context, actorID, workspaceID, organizationID string) error {
	if err := s.requireAnyServiceAgent(ctx, workspaceID, actorID); err != nil {
		return err
	}
	return s.Store.DeleteServiceOrganization(ctx, workspaceID, organizationID)
}

func (s *Service) SetServiceOrganizationUsers(ctx context.Context, actorID, workspaceID, organizationID string, userIDs []string, add bool) error {
	if err := s.requireAnyServiceAgent(ctx, workspaceID, actorID); err != nil {
		return err
	}
	if len(userIDs) == 0 {
		return fmt.Errorf("at least one customer account ID is required")
	}
	return s.Store.SetServiceOrganizationUsers(ctx, workspaceID, organizationID, userIDs, add)
}

func (s *Service) SetServiceOrganizationProperty(ctx context.Context, actorID, workspaceID, organizationID, key string, value json.RawMessage) error {
	if err := s.requireAnyServiceAgent(ctx, workspaceID, actorID); err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 255 || len(value) == 0 || len(value) > 32768 || !json.Valid(value) {
		return fmt.Errorf("property key and a valid JSON value of at most 32768 bytes are required")
	}
	return s.Store.SetServiceOrganizationProperty(ctx, workspaceID, organizationID, key, value)
}

func (s *Service) DeleteServiceOrganizationProperty(ctx context.Context, actorID, workspaceID, organizationID, key string) error {
	if err := s.requireAnyServiceAgent(ctx, workspaceID, actorID); err != nil {
		return err
	}
	return s.Store.DeleteServiceOrganizationProperty(ctx, workspaceID, organizationID, key)
}

func (s *Service) SetServiceDeskOrganization(ctx context.Context, actorID, workspaceID, serviceDeskID, organizationID string, add bool) error {
	if err := s.requireServiceDeskAgent(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return err
	}
	return s.Store.SetServiceDeskOrganization(ctx, workspaceID, serviceDeskID, organizationID, add)
}

func (s *Service) SetServiceDeskCustomers(ctx context.Context, actorID, workspaceID, serviceDeskID string, userIDs []string, add bool) error {
	if err := s.requireServiceAdmin(ctx, workspaceID, actorID); err != nil {
		return err
	}
	if len(userIDs) == 0 {
		return fmt.Errorf("at least one customer account ID is required")
	}
	return s.Store.SetServiceDeskCustomers(ctx, workspaceID, serviceDeskID, userIDs, add)
}

func (s *Service) InviteServiceDeskCustomer(ctx context.Context, actorID, workspaceID, serviceDeskID, email, displayName string) (*models.User, error) {
	if err := s.requireServiceAdmin(ctx, workspaceID, actorID); err != nil {
		return nil, err
	}
	if _, err := s.Store.ServiceDesk(ctx, workspaceID, serviceDeskID); err != nil {
		return nil, fmt.Errorf("service desk does not exist")
	}
	email, displayName = strings.TrimSpace(strings.ToLower(email)), strings.TrimSpace(displayName)
	if email == "" || !strings.Contains(email, "@") || displayName == "" || len(displayName) > 255 {
		return nil, fmt.Errorf("a valid email and display name are required")
	}
	customer, err := s.Store.CreateServiceCustomer(ctx, workspaceID, email, displayName)
	if err != nil {
		return nil, err
	}
	if err := s.Store.SetServiceDeskCustomers(ctx, workspaceID, serviceDeskID, []string{customer.ID}, true); err != nil {
		return nil, err
	}
	return customer, nil
}

func (s *Service) RevokePortalOnlyServiceCustomer(ctx context.Context, actorID, workspaceID, userID string) error {
	if err := s.requireServiceAdmin(ctx, workspaceID, actorID); err != nil {
		return err
	}
	return s.Store.RevokePortalOnlyServiceCustomer(ctx, workspaceID, userID)
}

func (s *Service) SetServiceDeskCustomerAccess(ctx context.Context, actorID, workspaceID, serviceDeskID string, open bool) error {
	if err := s.requireServiceAdmin(ctx, workspaceID, actorID); err != nil {
		return err
	}
	return s.Store.SetServiceDeskCustomerAccess(ctx, workspaceID, serviceDeskID, open)
}

func (s *Service) SetServiceDeskKnowledgeSpace(ctx context.Context, actorID, workspaceID, serviceDeskID, spaceID string, link bool) error {
	if err := s.requireServiceAdmin(ctx, workspaceID, actorID); err != nil {
		return err
	}
	return s.Store.SetServiceDeskKnowledgeSpace(ctx, workspaceID, actorID, serviceDeskID, spaceID, link)
}

func (s *Service) SetServiceRequestTypeProperty(ctx context.Context, actorID, workspaceID, serviceDeskID, requestTypeID, key string, value json.RawMessage) (bool, error) {
	if err := s.requireServiceAdmin(ctx, workspaceID, actorID); err != nil {
		return false, err
	}
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 255 || len(value) == 0 || len(value) > 32768 || !json.Valid(value) {
		return false, fmt.Errorf("property key and a valid JSON value of at most 32768 bytes are required")
	}
	return s.Store.SetServiceRequestTypeProperty(ctx, workspaceID, serviceDeskID, requestTypeID, key, value)
}

func (s *Service) DeleteServiceRequestTypeProperty(ctx context.Context, actorID, workspaceID, serviceDeskID, requestTypeID, key string) error {
	if err := s.requireServiceAdmin(ctx, workspaceID, actorID); err != nil {
		return err
	}
	return s.Store.DeleteServiceRequestTypeProperty(ctx, workspaceID, serviceDeskID, requestTypeID, key)
}
