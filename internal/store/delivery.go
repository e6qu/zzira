package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
)

func normalizedProperties(properties json.RawMessage) json.RawMessage {
	if len(properties) == 0 {
		return json.RawMessage(`{}`)
	}
	return properties
}

// UpsertSoftwareBuilds applies updateSequenceNumber ordering across a bulk submission.
func (s *Store) UpsertSoftwareBuilds(ctx context.Context, workspaceID string, builds []models.SoftwareBuild) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, build := range builds {
		if build.PipelineID == "" || !json.Valid(build.Payload) {
			return fmt.Errorf("build key, updateSequenceNumber, and payload are required")
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO software_builds(workspace_id,pipeline_id,build_number,update_sequence_number,issue_keys,display_name,url,state,last_updated,properties,payload)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT (workspace_id,pipeline_id,build_number) DO UPDATE SET
			  update_sequence_number=EXCLUDED.update_sequence_number,issue_keys=EXCLUDED.issue_keys,
			  display_name=EXCLUDED.display_name,url=EXCLUDED.url,state=EXCLUDED.state,last_updated=EXCLUDED.last_updated,
			  properties=EXCLUDED.properties,payload=EXCLUDED.payload,updated_at=now()
			WHERE EXCLUDED.update_sequence_number > software_builds.update_sequence_number`,
			workspaceID, build.PipelineID, build.BuildNumber, build.UpdateSequenceNumber, build.IssueKeys,
			build.DisplayName, build.URL, build.State, build.LastUpdated, normalizedProperties(build.Properties), build.Payload)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) SoftwareBuild(ctx context.Context, workspaceID, pipelineID string, buildNumber int64) (json.RawMessage, error) {
	var payload json.RawMessage
	err := s.Pool.QueryRow(ctx, `SELECT payload FROM software_builds WHERE workspace_id=$1 AND pipeline_id=$2 AND build_number=$3`, workspaceID, pipelineID, buildNumber).Scan(&payload)
	return payload, err
}

func (s *Store) DeleteSoftwareBuild(ctx context.Context, workspaceID, pipelineID string, buildNumber int64, sequence *int64) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM software_builds WHERE workspace_id=$1 AND pipeline_id=$2 AND build_number=$3 AND ($4::bigint IS NULL OR update_sequence_number <= $4)`, workspaceID, pipelineID, buildNumber, sequence)
	return err
}

func (s *Store) DeleteSoftwareBuildsByProperties(ctx context.Context, workspaceID string, properties json.RawMessage, sequence *int64) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM software_builds WHERE workspace_id=$1 AND properties @> $2::jsonb AND ($3::bigint IS NULL OR update_sequence_number <= $3)`, workspaceID, properties, sequence)
	return err
}

// UpsertSoftwareDeployments applies updateSequenceNumber ordering across a bulk submission.
func (s *Store) UpsertSoftwareDeployments(ctx context.Context, workspaceID string, deployments []models.SoftwareDeployment) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, deployment := range deployments {
		if deployment.PipelineID == "" || deployment.EnvironmentID == "" || !json.Valid(deployment.Payload) {
			return fmt.Errorf("deployment key, updateSequenceNumber, and payload are required")
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO software_deployments(workspace_id,pipeline_id,environment_id,deployment_sequence_number,update_sequence_number,issue_keys,display_name,url,state,environment_name,environment_type,last_updated,properties,payload)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			ON CONFLICT (workspace_id,pipeline_id,environment_id,deployment_sequence_number) DO UPDATE SET
			  update_sequence_number=EXCLUDED.update_sequence_number,issue_keys=EXCLUDED.issue_keys,
			  display_name=EXCLUDED.display_name,url=EXCLUDED.url,state=EXCLUDED.state,
			  environment_name=EXCLUDED.environment_name,environment_type=EXCLUDED.environment_type,
			  last_updated=EXCLUDED.last_updated,properties=EXCLUDED.properties,payload=EXCLUDED.payload,updated_at=now()
			WHERE EXCLUDED.update_sequence_number > software_deployments.update_sequence_number`,
			workspaceID, deployment.PipelineID, deployment.EnvironmentID, deployment.DeploymentSequenceNumber,
			deployment.UpdateSequenceNumber, deployment.IssueKeys, deployment.DisplayName, deployment.URL, deployment.State,
			deployment.EnvironmentName, deployment.EnvironmentType, deployment.LastUpdated,
			normalizedProperties(deployment.Properties), deployment.Payload)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) SoftwareDeployment(ctx context.Context, workspaceID, pipelineID, environmentID string, sequenceNumber int64) (*models.SoftwareDeployment, error) {
	deployment := &models.SoftwareDeployment{}
	err := s.Pool.QueryRow(ctx, `
		SELECT pipeline_id,environment_id,deployment_sequence_number,update_sequence_number,issue_keys,
		       display_name,url,state,environment_name,environment_type,last_updated,properties,payload
		FROM software_deployments
		WHERE workspace_id=$1 AND pipeline_id=$2 AND environment_id=$3 AND deployment_sequence_number=$4`,
		workspaceID, pipelineID, environmentID, sequenceNumber).Scan(
		&deployment.PipelineID, &deployment.EnvironmentID, &deployment.DeploymentSequenceNumber,
		&deployment.UpdateSequenceNumber, &deployment.IssueKeys, &deployment.DisplayName, &deployment.URL,
		&deployment.State, &deployment.EnvironmentName, &deployment.EnvironmentType, &deployment.LastUpdated,
		&deployment.Properties, &deployment.Payload)
	return deployment, err
}

func (s *Store) DeleteSoftwareDeployment(ctx context.Context, workspaceID, pipelineID, environmentID string, deploymentSequenceNumber int64, sequence *int64) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM software_deployments WHERE workspace_id=$1 AND pipeline_id=$2 AND environment_id=$3 AND deployment_sequence_number=$4 AND ($5::bigint IS NULL OR update_sequence_number <= $5)`, workspaceID, pipelineID, environmentID, deploymentSequenceNumber, sequence)
	return err
}

func (s *Store) DeleteSoftwareDeploymentsByProperties(ctx context.Context, workspaceID string, properties json.RawMessage, sequence *int64) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM software_deployments WHERE workspace_id=$1 AND properties @> $2::jsonb AND ($3::bigint IS NULL OR update_sequence_number <= $3)`, workspaceID, properties, sequence)
	return err
}

func (s *Store) DeliveryItemsForIssues(ctx context.Context, workspaceID string, issueKeys []string) ([]models.DeliveryItem, error) {
	if len(issueKeys) == 0 {
		return []models.DeliveryItem{}, nil
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT kind,pipeline_id,display_name,url,state,environment_name,environment_type,
		       to_char(last_updated AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM (
		  SELECT 'build' AS kind,pipeline_id,display_name,url,state,'' AS environment_name,'' AS environment_type,last_updated,updated_at
		  FROM software_builds WHERE workspace_id=$1 AND issue_keys && $2::text[]
		  UNION ALL
		  SELECT 'deployment',pipeline_id,display_name,url,state,environment_name,environment_type,last_updated,updated_at
		  FROM software_deployments WHERE workspace_id=$1 AND issue_keys && $2::text[]
		) evidence
		ORDER BY last_updated DESC,updated_at DESC,pipeline_id,display_name`, workspaceID, issueKeys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]models.DeliveryItem, 0)
	for rows.Next() {
		var item models.DeliveryItem
		if err := rows.Scan(&item.Kind, &item.PipelineID, &item.DisplayName, &item.URL, &item.State, &item.EnvironmentName, &item.EnvironmentType, &item.LastUpdated); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
