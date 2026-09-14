package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrServiceRegistryValidation = errors.New("invalid service")

type ServiceRegistryTier struct {
	ID, Name, NameKey, Description string
	Level                          int
}

type ServiceRegistryService struct {
	ID, Name, Description, Revision, OrganizationID string
	Tier                                            ServiceRegistryTier
	UpdatedAt                                       time.Time
}

const serviceRegistrySelect = `SELECT s.id::text,s.name,s.description,s.revision::text,COALESCE(si.organization_id::text,''),t.id::text,t.name,t.name_key,t.description,t.level,s.updated_at
	FROM service_registry_services s JOIN service_registry_tiers t ON t.level=s.tier_level LEFT JOIN sites si ON si.workspace_id=s.workspace_id`

func (s *Store) ServiceRegistryTiers(ctx context.Context) ([]ServiceRegistryTier, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id::text,name,name_key,description,level FROM service_registry_tiers ORDER BY level`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tiers := []ServiceRegistryTier{}
	for rows.Next() {
		var tier ServiceRegistryTier
		if err := rows.Scan(&tier.ID, &tier.Name, &tier.NameKey, &tier.Description, &tier.Level); err != nil {
			return nil, err
		}
		tiers = append(tiers, tier)
	}
	return tiers, rows.Err()
}

// ServiceRegistryServices lists the site's services, or only the given ids.
func (s *Store) ServiceRegistryServices(ctx context.Context, workspaceID string, ids []string) ([]ServiceRegistryService, error) {
	query := serviceRegistrySelect + ` WHERE s.workspace_id=$1`
	args := []any{workspaceID}
	if ids != nil {
		query += ` AND s.id::text = ANY($2)`
		args = append(args, ids)
	}
	rows, err := s.Pool.Query(ctx, query+` ORDER BY lower(s.name),s.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	services := []ServiceRegistryService{}
	for rows.Next() {
		var service ServiceRegistryService
		if err := rows.Scan(&service.ID, &service.Name, &service.Description, &service.Revision, &service.OrganizationID, &service.Tier.ID, &service.Tier.Name, &service.Tier.NameKey, &service.Tier.Description, &service.Tier.Level, &service.UpdatedAt); err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	return services, rows.Err()
}

func validService(name, description string, tier int) (string, string, error) {
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if name == "" || len([]rune(name)) > 255 || len([]rune(description)) > 2000 || tier < 1 || tier > 4 {
		return "", "", fmt.Errorf("%w: a service needs a name of 1 to 255 characters, a description of at most 2000 and a tier from 1 to 4", ErrServiceRegistryValidation)
	}
	return name, description, nil
}

func (s *Store) CreateServiceRegistryService(ctx context.Context, workspaceID, actorID, name, description string, tier int) (string, error) {
	name, description, err := validService(name, description, tier)
	if err != nil {
		return "", err
	}
	var id string
	err = s.Pool.QueryRow(ctx, `INSERT INTO service_registry_services(workspace_id,name,description,tier_level,created_by) VALUES($1,$2,$3,$4,$5) RETURNING id::text`, workspaceID, name, description, tier, actorID).Scan(&id)
	if err != nil && strings.Contains(err.Error(), "service_registry_services_name") {
		return "", fmt.Errorf("%w: a service with this name already exists", ErrServiceRegistryValidation)
	}
	return id, err
}

// UpdateServiceRegistryService changes a service and advances its revision.
func (s *Store) UpdateServiceRegistryService(ctx context.Context, workspaceID, id, name, description string, tier int) error {
	name, description, err := validService(name, description, tier)
	if err != nil {
		return err
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE service_registry_services SET name=$3,description=$4,tier_level=$5,revision=revision+1,updated_at=now() WHERE workspace_id=$1 AND id::text=$2`, workspaceID, id, name, description, tier)
	if err != nil && strings.Contains(err.Error(), "service_registry_services_name") {
		return fmt.Errorf("%w: a service with this name already exists", ErrServiceRegistryValidation)
	}
	if err == nil && tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: the service does not exist", ErrServiceRegistryValidation)
	}
	return err
}

func (s *Store) DeleteServiceRegistryService(ctx context.Context, workspaceID, id string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM service_registry_services WHERE workspace_id=$1 AND id::text=$2`, workspaceID, id)
	return err
}
