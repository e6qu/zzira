package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// An organization's administrators define its data classification levels. A
// level starts as a draft, is published to be used, and is archived when it
// should no longer be chosen; content that already carries an archived level
// keeps it. Levels are ordered by rank, and the same levels serve Jira projects
// and Confluence content on every site of the organization.

var ErrClassificationValidation = errors.New("invalid classification level")

// dataQuerier is what both the pool and a transaction offer.
type dataQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

const dataClassificationSelect = `SELECT id,status,rank,name,description,guideline,color FROM data_classification_levels`

func scanDataClassificationLevel(row pgx.Row) (models.DataClassificationLevel, error) {
	var level models.DataClassificationLevel
	err := row.Scan(&level.ID, &level.Status, &level.Rank, &level.Name, &level.Description, &level.Guideline, &level.Color)
	return level, err
}

// workspaceOrganization is the organization a site belongs to. An
// organization that has never had levels gets the starting four.
func workspaceOrganization(ctx context.Context, q dataQuerier, ws string) (string, error) {
	var organizationID string
	if err := q.QueryRow(ctx, `SELECT organization_id::text FROM sites WHERE workspace_id=$1`, ws).Scan(&organizationID); err != nil {
		return "", err
	}
	var seeded bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM data_classification_levels WHERE organization_id=$1)`, organizationID).Scan(&seeded); err != nil || seeded {
		return organizationID, err
	}
	for _, level := range models.DefaultDataClassificationLevels {
		if _, err := q.Exec(ctx, `INSERT INTO data_classification_levels(organization_id,id,name,description,guideline,color,status,rank)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`, organizationID, level.ID, level.Name, level.Description, level.Guideline, level.Color, level.Status, level.Rank); err != nil {
			return "", err
		}
	}
	return organizationID, nil
}

// DataClassificationLevels lists a site's classification levels in rank order.
func (s *Store) DataClassificationLevels(ctx context.Context, ws string) ([]models.DataClassificationLevel, error) {
	organizationID, err := workspaceOrganization(ctx, s.Pool, ws)
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, dataClassificationSelect+` WHERE organization_id=$1 ORDER BY rank,id`, organizationID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (models.DataClassificationLevel, error) {
		return scanDataClassificationLevel(row)
	})
}

// DataClassificationLevel reads one of a site's levels.
func (s *Store) DataClassificationLevel(ctx context.Context, ws, id string) (models.DataClassificationLevel, error) {
	organizationID, err := workspaceOrganization(ctx, s.Pool, ws)
	if err != nil {
		return models.DataClassificationLevel{}, err
	}
	return scanDataClassificationLevel(s.Pool.QueryRow(ctx, dataClassificationSelect+` WHERE organization_id=$1 AND id=$2`, organizationID, id))
}

// PublishedDataClassificationLevel checks a level can be given to something:
// only a published level can.
func (s *Store) PublishedDataClassificationLevel(ctx context.Context, ws, id string) error {
	level, err := s.DataClassificationLevel(ctx, ws, id)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && level.Status != "PUBLISHED" {
		return fmt.Errorf("%w: choose a published classification level", ErrWikiValidation)
	}
	return err
}

// DataClassificationLevelInput is what an administrator writes about a level.
type DataClassificationLevelInput struct {
	Name, Description, Guideline, Color string
}

func validDataClassificationLevel(input DataClassificationLevelInput) error {
	name := strings.TrimSpace(input.Name)
	switch {
	case name == "" || len(name) > 255:
		return fmt.Errorf("%w: a level needs a name of 1 to 255 characters", ErrClassificationValidation)
	case len(input.Description) > 1000:
		return fmt.Errorf("%w: a level's description is at most 1,000 characters", ErrClassificationValidation)
	case len(input.Guideline) > 4000:
		return fmt.Errorf("%w: a level's guideline is at most 4,000 characters", ErrClassificationValidation)
	case !slices.Contains(models.DataClassificationColors, input.Color):
		return fmt.Errorf("%w: choose one of the level colors", ErrClassificationValidation)
	}
	return nil
}

// dataClassificationAdmin opens a change to a site's levels for one of its
// administrators and locks the organization's levels.
func (s *Store) dataClassificationAdmin(ctx context.Context, ws, actor string) (pgx.Tx, string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, "", err
	}
	if err := projectAdmin(ctx, tx, ws, actor); err != nil {
		_ = tx.Rollback(ctx)
		return nil, "", err
	}
	organizationID, err := workspaceOrganization(ctx, tx, ws)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, "", err
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM data_classification_levels WHERE organization_id=$1 FOR UPDATE`, organizationID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, "", err
	}
	return tx, organizationID, nil
}

func finishDataClassificationChange(ctx context.Context, tx pgx.Tx, organizationID, id string, err error) (models.DataClassificationLevel, error) {
	defer func() { _ = tx.Rollback(ctx) }()
	if isUniqueViolation(err) {
		return models.DataClassificationLevel{}, fmt.Errorf("%w: another level already has that name", ErrClassificationValidation)
	}
	if err != nil {
		return models.DataClassificationLevel{}, err
	}
	level, err := scanDataClassificationLevel(tx.QueryRow(ctx, dataClassificationSelect+` WHERE organization_id=$1 AND id=$2`, organizationID, id))
	if err != nil {
		return models.DataClassificationLevel{}, err
	}
	return level, tx.Commit(ctx)
}

// CreateDataClassificationLevel adds a draft level after the existing ones.
func (s *Store) CreateDataClassificationLevel(ctx context.Context, ws, actor string, input DataClassificationLevelInput) (models.DataClassificationLevel, error) {
	if err := validDataClassificationLevel(input); err != nil {
		return models.DataClassificationLevel{}, err
	}
	tx, organizationID, err := s.dataClassificationAdmin(ctx, ws, actor)
	if err != nil {
		return models.DataClassificationLevel{}, err
	}
	id := strings.TrimPrefix(NewID("dcl"), "dcl_")
	_, err = tx.Exec(ctx, `INSERT INTO data_classification_levels(organization_id,id,name,description,guideline,color,status,rank)
		VALUES($1,$2,$3,$4,$5,$6,'DRAFT',(SELECT COALESCE(max(rank)+1,0) FROM data_classification_levels WHERE organization_id=$1))`,
		organizationID, id, strings.TrimSpace(input.Name), input.Description, input.Guideline, input.Color)
	return finishDataClassificationChange(ctx, tx, organizationID, id, err)
}

// UpdateDataClassificationLevel rewrites a level's name, description,
// guideline and color. An archived level is kept as it was.
func (s *Store) UpdateDataClassificationLevel(ctx context.Context, ws, actor, id string, input DataClassificationLevelInput) (models.DataClassificationLevel, error) {
	if err := validDataClassificationLevel(input); err != nil {
		return models.DataClassificationLevel{}, err
	}
	tx, organizationID, err := s.dataClassificationAdmin(ctx, ws, actor)
	if err != nil {
		return models.DataClassificationLevel{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE data_classification_levels SET name=$3,description=$4,guideline=$5,color=$6,updated_at=now()
		WHERE organization_id=$1 AND id=$2 AND status<>'ARCHIVED'`, organizationID, id, strings.TrimSpace(input.Name), input.Description, input.Guideline, input.Color)
	if err == nil && tag.RowsAffected() == 0 {
		err = fmt.Errorf("%w: only draft and published levels can be edited", ErrClassificationValidation)
	}
	return finishDataClassificationChange(ctx, tx, organizationID, id, err)
}

// SetDataClassificationLevelStatus publishes, archives or restores a level.
// A draft is published; a published level is archived; an archived level is
// published again. A level that has been published does not go back to draft.
func (s *Store) SetDataClassificationLevelStatus(ctx context.Context, ws, actor, id, status string) (models.DataClassificationLevel, error) {
	allowedFrom := map[string][]string{"PUBLISHED": {"DRAFT", "ARCHIVED"}, "ARCHIVED": {"PUBLISHED"}}[status]
	if allowedFrom == nil {
		return models.DataClassificationLevel{}, fmt.Errorf("%w: a level is published or archived", ErrClassificationValidation)
	}
	tx, organizationID, err := s.dataClassificationAdmin(ctx, ws, actor)
	if err != nil {
		return models.DataClassificationLevel{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE data_classification_levels SET status=$3,updated_at=now()
		WHERE organization_id=$1 AND id=$2 AND status=ANY($4::text[])`, organizationID, id, status, allowedFrom)
	if err == nil && tag.RowsAffected() == 0 {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM data_classification_levels WHERE organization_id=$1 AND id=$2)`, organizationID, id).Scan(&exists); err == nil {
			err = pgx.ErrNoRows
			if exists {
				err = fmt.Errorf("%w: the level cannot move to %s from where it is", ErrClassificationValidation, strings.ToLower(status))
			}
		}
	}
	return finishDataClassificationChange(ctx, tx, organizationID, id, err)
}

// MoveDataClassificationLevel moves a level one place up or down the order.
func (s *Store) MoveDataClassificationLevel(ctx context.Context, ws, actor, id string, up bool) error {
	tx, organizationID, err := s.dataClassificationAdmin(ctx, ws, actor)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT id FROM data_classification_levels WHERE organization_id=$1 ORDER BY rank,id`, organizationID)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	at := slices.Index(ids, id)
	if at < 0 {
		return pgx.ErrNoRows
	}
	other := at + 1
	if up {
		other = at - 1
	}
	if other >= 0 && other < len(ids) {
		ids[at], ids[other] = ids[other], ids[at]
	}
	for rank, levelID := range ids {
		if _, err := tx.Exec(ctx, `UPDATE data_classification_levels SET rank=$3 WHERE organization_id=$1 AND id=$2`, organizationID, levelID, rank); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
