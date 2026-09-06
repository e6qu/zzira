package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

// UpsertDevelopmentRepositories applies Jira's updateSequenceId ordering to
// repositories and every nested entity. Stale deliveries are successful
// no-ops, which makes integration retries safe.
func (s *Store) UpsertDevelopmentRepositories(ctx context.Context, workspaceID string, repositories []models.DevelopmentRepository) ([]models.DevelopmentTriggerEvent, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	accepted := make([]models.DevelopmentTriggerEvent, 0)
	for _, repository := range repositories {
		if repository.ID == "" || repository.UpdateSequenceID <= 0 || !json.Valid(repository.Payload) {
			return nil, fmt.Errorf("repository id, positive updateSequenceId, and payload are required")
		}
		properties := repository.Properties
		if len(properties) == 0 {
			properties = json.RawMessage(`{}`)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO development_repositories(workspace_id,repository_id,update_sequence_id,name,url,properties,payload)
			VALUES($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (workspace_id,repository_id) DO UPDATE SET
			  update_sequence_id=EXCLUDED.update_sequence_id,name=EXCLUDED.name,url=EXCLUDED.url,
			  properties=EXCLUDED.properties,payload=EXCLUDED.payload,updated_at=now()
			WHERE EXCLUDED.update_sequence_id > development_repositories.update_sequence_id`,
			workspaceID, repository.ID, repository.UpdateSequenceID, repository.Name, repository.URL, properties, repository.Payload)
		if err != nil {
			return nil, err
		}
		for _, entity := range repository.Entities {
			if entity.ID == "" || entity.UpdateSequenceID <= 0 || !validDevelopmentEntityType(entity.Type) || !json.Valid(entity.Payload) {
				return nil, fmt.Errorf("development entities require an id, positive updateSequenceId, and supported type")
			}
			isNew := false
			if entity.Type == "branch" {
				lockKey := workspaceID + "|" + repository.ID + "|" + entity.Type + "|" + entity.ID
				if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockKey); err != nil {
					return nil, err
				}
				var currentSequence int64
				err := tx.QueryRow(ctx, `SELECT update_sequence_id FROM development_entities WHERE workspace_id=$1 AND repository_id=$2 AND entity_type=$3 AND entity_id=$4 FOR UPDATE`, workspaceID, repository.ID, entity.Type, entity.ID).Scan(&currentSequence)
				if err == pgx.ErrNoRows {
					isNew = true
				} else if err != nil {
					return nil, err
				}
			}
			tag, err := tx.Exec(ctx, `
				INSERT INTO development_entities(workspace_id,repository_id,entity_type,entity_id,update_sequence_id,issue_keys,name,url,status,occurred_at,payload)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
				ON CONFLICT (workspace_id,repository_id,entity_type,entity_id) DO UPDATE SET
				  update_sequence_id=EXCLUDED.update_sequence_id,issue_keys=EXCLUDED.issue_keys,name=EXCLUDED.name,
				  url=EXCLUDED.url,status=EXCLUDED.status,occurred_at=EXCLUDED.occurred_at,payload=EXCLUDED.payload,updated_at=now()
				WHERE EXCLUDED.update_sequence_id > development_entities.update_sequence_id`,
				workspaceID, repository.ID, entity.Type, entity.ID, entity.UpdateSequenceID, entity.IssueKeys,
				entity.Name, entity.URL, entity.Status, entity.OccurredAt, entity.Payload)
			if err != nil {
				return nil, err
			}
			if tag.RowsAffected() == 1 && entity.Type == "branch" && isNew {
				accepted = append(accepted, models.DevelopmentTriggerEvent{Type: entity.Type, IssueKeys: entity.IssueKeys})
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return accepted, nil
}

func validDevelopmentEntityType(entityType string) bool {
	return entityType == "commit" || entityType == "branch" || entityType == "pullrequest"
}

func (s *Store) DevelopmentRepository(ctx context.Context, workspaceID, repositoryID string) (json.RawMessage, error) {
	var payload json.RawMessage
	if err := s.Pool.QueryRow(ctx, `SELECT payload FROM development_repositories WHERE workspace_id=$1 AND repository_id=$2`, workspaceID, repositoryID).Scan(&payload); err != nil {
		return nil, err
	}
	var repository map[string]any
	if err := json.Unmarshal(payload, &repository); err != nil {
		return nil, err
	}
	groups := map[string][]json.RawMessage{"commits": {}, "branches": {}, "pullRequests": {}}
	rows, err := s.Pool.Query(ctx, `SELECT entity_type,payload FROM development_entities WHERE workspace_id=$1 AND repository_id=$2 ORDER BY occurred_at DESC NULLS LAST,updated_at DESC,entity_id LIMIT 400`, workspaceID, repositoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var entityType string
		var entity json.RawMessage
		if err := rows.Scan(&entityType, &entity); err != nil {
			return nil, err
		}
		key := map[string]string{"commit": "commits", "branch": "branches", "pullrequest": "pullRequests"}[entityType]
		groups[key] = append(groups[key], entity)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for key, values := range groups {
		repository[key] = values
	}
	return json.Marshal(repository)
}

func (s *Store) DeleteDevelopmentRepository(ctx context.Context, workspaceID, repositoryID string, updateSequenceID *int64) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM development_repositories WHERE workspace_id=$1 AND repository_id=$2 AND ($3::bigint IS NULL OR update_sequence_id < $3)`, workspaceID, repositoryID, updateSequenceID)
	return err
}

func (s *Store) DeleteDevelopmentEntity(ctx context.Context, workspaceID, repositoryID, entityType, entityID string, updateSequenceID *int64) error {
	if !validDevelopmentEntityType(entityType) {
		return fmt.Errorf("wrong entity type specified")
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM development_entities WHERE workspace_id=$1 AND repository_id=$2 AND entity_type=$3 AND entity_id=$4 AND ($5::bigint IS NULL OR update_sequence_id < $5)`, workspaceID, repositoryID, entityType, entityID, updateSequenceID)
	return err
}

func (s *Store) DevelopmentRepositoriesExistByProperties(ctx context.Context, workspaceID string, properties json.RawMessage, updateSequenceID *int64) (bool, error) {
	var exists bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM development_repositories WHERE workspace_id=$1 AND properties @> $2::jsonb AND ($3::bigint IS NULL OR update_sequence_id <= $3))`, workspaceID, properties, updateSequenceID).Scan(&exists)
	return exists, err
}

func (s *Store) DeleteDevelopmentRepositoriesByProperties(ctx context.Context, workspaceID string, properties json.RawMessage) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM development_repositories WHERE workspace_id=$1 AND properties @> $2::jsonb`, workspaceID, properties)
	return err
}

func (s *Store) DevelopmentItemsForIssue(ctx context.Context, workspaceID, issueKey string) ([]models.DevelopmentItem, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT e.repository_id,r.name,e.entity_type,e.entity_id,e.name,e.url,e.status,
		       COALESCE(to_char(e.occurred_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),'')
		FROM development_entities e
		JOIN development_repositories r ON r.workspace_id=e.workspace_id AND r.repository_id=e.repository_id
		WHERE e.workspace_id=$1 AND upper($2)=ANY(e.issue_keys)
		ORDER BY e.occurred_at DESC NULLS LAST,e.updated_at DESC,e.entity_id`, workspaceID, issueKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]models.DevelopmentItem, 0)
	for rows.Next() {
		var item models.DevelopmentItem
		if err := rows.Scan(&item.RepositoryID, &item.RepositoryName, &item.Type, &item.ID, &item.Name, &item.URL, &item.Status, &item.Occurred); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
