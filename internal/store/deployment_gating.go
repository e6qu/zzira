package store

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
)

// ServiceDeploymentGate is a service desk's deployment gating: the provider it
// connects ("*" for any) and the environment types it gates.
type ServiceDeploymentGate struct {
	ServiceDeskID    string
	ProviderKey      string
	EnvironmentTypes []string
}

// Gates reports whether the gate covers an environment type.
func (g *ServiceDeploymentGate) Gates(environmentType string) bool {
	return g != nil && slices.Contains(g.EnvironmentTypes, environmentType)
}

// ServiceDeploymentGate returns a service desk's deployment gating, or nil
// when the desk gates no deployments.
func (s *Store) ServiceDeploymentGate(ctx context.Context, workspaceID, serviceDeskID string) (*ServiceDeploymentGate, error) {
	gate := &ServiceDeploymentGate{}
	err := s.Pool.QueryRow(ctx, `
		SELECT g.service_desk_id,g.provider_key,g.environment_types FROM service_deployment_gates g
		JOIN service_desks sd ON sd.id=g.service_desk_id WHERE sd.workspace_id=$1 AND g.service_desk_id=$2`, workspaceID, serviceDeskID).
		Scan(&gate.ServiceDeskID, &gate.ProviderKey, &gate.EnvironmentTypes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return gate, err
}

// SetServiceDeploymentGate saves a service desk's deployment gating; no
// environment types turns it off.
func (s *Store) SetServiceDeploymentGate(ctx context.Context, workspaceID, serviceDeskID, providerKey string, environmentTypes []string) error {
	if len(environmentTypes) == 0 {
		_, err := s.Pool.Exec(ctx, `DELETE FROM service_deployment_gates g USING service_desks sd WHERE sd.id=g.service_desk_id AND sd.workspace_id=$1 AND g.service_desk_id=$2`, workspaceID, serviceDeskID)
		return err
	}
	tag, err := s.Pool.Exec(ctx, `
		INSERT INTO service_deployment_gates(service_desk_id,provider_key,environment_types)
		SELECT sd.id,$3,$4 FROM service_desks sd WHERE sd.workspace_id=$1 AND sd.id=$2
		ON CONFLICT(service_desk_id) DO UPDATE SET provider_key=EXCLUDED.provider_key,environment_types=EXCLUDED.environment_types,updated_at=now()`,
		workspaceID, serviceDeskID, providerKey, environmentTypes)
	if err == nil && tag.RowsAffected() == 0 {
		return fmt.Errorf("service desk does not exist")
	}
	return err
}

// DeploymentGates lists the service desks gating a provider's deployments to
// an environment type, oldest desk first.
func (s *Store) DeploymentGates(ctx context.Context, workspaceID, providerKey, environmentType string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT g.service_desk_id FROM service_deployment_gates g JOIN service_desks sd ON sd.id=g.service_desk_id
		WHERE sd.workspace_id=$1 AND (g.provider_key='*' OR g.provider_key=$2) AND $3=ANY(g.environment_types)
		ORDER BY sd.id::bigint`, workspaceID, providerKey, environmentType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	desks := make([]string, 0)
	for rows.Next() {
		var desk string
		if err := rows.Scan(&desk); err != nil {
			return nil, err
		}
		desks = append(desks, desk)
	}
	return desks, rows.Err()
}

// RecordDeploymentGating keeps the change request gating a deployment; an
// empty issue records that gating failed.
func (s *Store) RecordDeploymentGating(ctx context.Context, workspaceID, pipelineID, environmentID string, sequence int64, issueID string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO software_deployment_gatings(workspace_id,pipeline_id,environment_id,deployment_sequence_number,issue_id)
		VALUES($1,$2,$3,$4,NULLIF($5,'')) ON CONFLICT DO NOTHING`, workspaceID, pipelineID, environmentID, sequence, issueID)
	return err
}

// DeploymentGating returns the change request gating a deployment and whether
// the deployment is gated at all.
func (s *Store) DeploymentGating(ctx context.Context, workspaceID, pipelineID, environmentID string, sequence int64) (string, bool, error) {
	var issueID *string
	err := s.Pool.QueryRow(ctx, `
		SELECT issue_id FROM software_deployment_gatings
		WHERE workspace_id=$1 AND pipeline_id=$2 AND environment_id=$3 AND deployment_sequence_number=$4`, workspaceID, pipelineID, environmentID, sequence).Scan(&issueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil || issueID == nil {
		return "", err == nil, err
	}
	return *issueID, true, nil
}
