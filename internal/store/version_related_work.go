package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

var (
	ErrRelatedWorkValidation = errors.New("related work request is invalid")
	ErrRelatedWorkNotFound   = errors.New("related work does not exist")
)

func validateRelatedWork(work *models.VersionRelatedWork) error {
	work.Category = strings.TrimSpace(work.Category)
	work.Title = strings.TrimSpace(work.Title)
	work.URL = strings.TrimSpace(work.URL)
	if work.Category == "" || len(work.Category) > 255 {
		return fmt.Errorf("%w: category must contain 1 to 255 characters", ErrRelatedWorkValidation)
	}
	if len(work.Title) > 255 {
		return fmt.Errorf("%w: title accepts at most 255 characters", ErrRelatedWorkValidation)
	}
	if work.URL != "" {
		parsed, err := url.Parse(work.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("%w: url must be an absolute http or https address", ErrRelatedWorkValidation)
		}
	}
	return nil
}

func (s *Store) VersionRelatedWork(ctx context.Context, workspaceID, versionID string) ([]models.VersionRelatedWork, error) {
	if _, err := s.Version(ctx, workspaceID, versionID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT id,version_id,category,title,url FROM version_related_work
		WHERE version_id=$1 ORDER BY id`, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []models.VersionRelatedWork{}
	for rows.Next() {
		var work models.VersionRelatedWork
		if err = rows.Scan(&work.ID, &work.VersionID, &work.Category, &work.Title, &work.URL); err != nil {
			return nil, err
		}
		items = append(items, work)
	}
	return items, rows.Err()
}

func (s *Store) CreateVersionRelatedWork(ctx context.Context, workspaceID, actorID, versionID string, work models.VersionRelatedWork) (models.VersionRelatedWork, error) {
	if err := validateRelatedWork(&work); err != nil {
		return work, err
	}
	version, err := s.Version(ctx, workspaceID, versionID)
	if err != nil {
		return work, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return work, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = tx.QueryRow(ctx, `INSERT INTO version_related_work(version_id,category,title,url)
		VALUES($1,$2,$3,$4) RETURNING id,version_id,category,title,url`,
		version.ID, work.Category, work.Title, work.URL).
		Scan(&work.ID, &work.VersionID, &work.Category, &work.Title, &work.URL); err != nil {
		return work, err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "version_related_work", work.ID, models.OpUpsert, work); err != nil {
		return work, err
	}
	return work, tx.Commit(ctx)
}

func (s *Store) UpdateVersionRelatedWork(ctx context.Context, workspaceID, actorID, versionID string, work models.VersionRelatedWork) (models.VersionRelatedWork, error) {
	if work.ID == "" {
		return work, fmt.Errorf("%w: relatedWorkId is required", ErrRelatedWorkValidation)
	}
	if err := validateRelatedWork(&work); err != nil {
		return work, err
	}
	version, err := s.Version(ctx, workspaceID, versionID)
	if err != nil {
		return work, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return work, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	err = tx.QueryRow(ctx, `UPDATE version_related_work SET category=$3,title=$4,url=$5
		WHERE version_id=$1 AND id=$2 RETURNING id,version_id,category,title,url`,
		version.ID, work.ID, work.Category, work.Title, work.URL).
		Scan(&work.ID, &work.VersionID, &work.Category, &work.Title, &work.URL)
	if errors.Is(err, pgx.ErrNoRows) {
		return work, ErrRelatedWorkNotFound
	}
	if err != nil {
		return work, err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "version_related_work", work.ID, models.OpUpsert, work); err != nil {
		return work, err
	}
	return work, tx.Commit(ctx)
}

func (s *Store) DeleteVersionRelatedWork(ctx context.Context, workspaceID, actorID, versionID, relatedWorkID string) error {
	version, err := s.Version(ctx, workspaceID, versionID)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `DELETE FROM version_related_work WHERE version_id=$1 AND id=$2`, version.ID, relatedWorkID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrRelatedWorkNotFound
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "version_related_work", relatedWorkID, models.OpDelete,
		map[string]any{"versionId": version.ID, "relatedWorkId": relatedWorkID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// MoveVersion applies Jira's reorder request: after another version, or a
// relative First, Earlier, Later, or Last position within the project.
func (s *Store) MoveVersion(ctx context.Context, workspaceID, actorID, versionID, after, position string) error {
	version, err := s.Version(ctx, workspaceID, versionID)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT id FROM project_versions WHERE project_id=$1 ORDER BY position,id`, version.ProjectID)
	if err != nil {
		return err
	}
	order := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		order = append(order, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	current := -1
	for index, id := range order {
		if id == version.ID {
			current = index
		}
	}
	if current < 0 {
		return ErrRelatedWorkNotFound
	}
	remaining := make([]string, 0, len(order))
	for _, id := range order {
		if id != version.ID {
			remaining = append(remaining, id)
		}
	}
	target := 0
	switch {
	case after != "":
		found := false
		for index, id := range remaining {
			if id == after {
				target, found = index+1, true
			}
		}
		if !found {
			return fmt.Errorf("%w: the version to move after is not in this project", ErrRelatedWorkValidation)
		}
	case strings.EqualFold(position, "First"):
		target = 0
	case strings.EqualFold(position, "Last"):
		target = len(remaining)
	case strings.EqualFold(position, "Earlier"):
		target = max(0, current-1)
	case strings.EqualFold(position, "Later"):
		target = min(len(remaining), current+1)
	default:
		return fmt.Errorf("%w: move requires after or position First, Earlier, Later, or Last", ErrRelatedWorkValidation)
	}
	moved := append(remaining[:target:target], append([]string{version.ID}, remaining[target:]...)...)
	for index, id := range moved {
		if _, err = tx.Exec(ctx, `UPDATE project_versions SET position=$2 WHERE id=$1`, id, index); err != nil {
			return err
		}
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_version", version.ID, models.OpUpsert,
		map[string]any{"versionId": version.ID, "position": target}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
