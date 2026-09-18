package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

// Base and subtask levels are fixed, as in Jira: an administrator names and
// adds levels above Epic only.
const (
	SubtaskHierarchyLevel = -1
	BaseHierarchyLevel    = 0
	EpicHierarchyLevel    = 1
)

// HierarchyLevel is one level of the site's work type hierarchy, with the work
// types that sit on it.
type HierarchyLevel struct {
	Level     int
	Name      string
	WorkTypes []models.IssueType
}

// HierarchyLevels lists the site's levels from the top down, each with its
// work types.
func (s *Store) HierarchyLevels(ctx context.Context, workspaceID string) ([]HierarchyLevel, error) {
	rows, err := s.Pool.Query(ctx, `SELECT level,name FROM issue_type_hierarchy_levels
		WHERE workspace_id=$1 ORDER BY level DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	levels := []HierarchyLevel{}
	for rows.Next() {
		var level HierarchyLevel
		if err = rows.Scan(&level.Level, &level.Name); err != nil {
			rows.Close()
			return nil, err
		}
		levels = append(levels, level)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	workTypes, err := s.IssueTypesForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	for index := range levels {
		levels[index].WorkTypes = []models.IssueType{}
		for _, workType := range workTypes {
			if workType.HierarchyLevel == levels[index].Level {
				levels[index].WorkTypes = append(levels[index].WorkTypes, workType)
			}
		}
	}
	return levels, nil
}

// HierarchyLevelName is a level's name, or the level number when the site has
// no row for it.
func (s *Store) HierarchyLevelName(ctx context.Context, workspaceID string, level int) (string, error) {
	var name string
	err := s.Pool.QueryRow(ctx, `SELECT name FROM issue_type_hierarchy_levels
		WHERE workspace_id=$1 AND level=$2`, workspaceID, level).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Sprintf("Level %d", level), nil
	}
	return name, err
}

// ErrHierarchyValidation refuses a hierarchy change Jira would refuse.
var ErrHierarchyValidation = errors.New("invalid work type hierarchy change")

// AddHierarchyLevel adds a named level above the site's top level.
func (s *Store) AddHierarchyLevel(ctx context.Context, workspaceID, actorID, name string) (HierarchyLevel, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 255 {
		return HierarchyLevel{}, fmt.Errorf("%w: a level name of 1 to 255 characters is required", ErrHierarchyValidation)
	}
	if err := s.requireSiteAdmin(ctx, workspaceID, actorID); err != nil {
		return HierarchyLevel{}, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return HierarchyLevel{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if taken, err := hierarchyNameTaken(ctx, tx, workspaceID, name, nil); err != nil {
		return HierarchyLevel{}, err
	} else if taken {
		return HierarchyLevel{}, fmt.Errorf("%w: a level with this name already exists", ErrHierarchyValidation)
	}
	level := HierarchyLevel{Name: name, WorkTypes: []models.IssueType{}}
	if err = tx.QueryRow(ctx, `INSERT INTO issue_type_hierarchy_levels(workspace_id,level,name)
		SELECT $1, COALESCE(max(level),0)+1, $2 FROM issue_type_hierarchy_levels WHERE workspace_id=$1
		RETURNING level`, workspaceID, name).Scan(&level.Level); err != nil {
		return HierarchyLevel{}, err
	}
	if err = appendHierarchyAction(ctx, tx, workspaceID, actorID, "hierarchy_level_added", level.Level, name); err != nil {
		return HierarchyLevel{}, err
	}
	return level, tx.Commit(ctx)
}

// RenameHierarchyLevel renames a level. Jira fixes the base and subtask level
// names, so only Epic and the levels above it can be renamed.
func (s *Store) RenameHierarchyLevel(ctx context.Context, workspaceID, actorID string, level int, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 255 {
		return fmt.Errorf("%w: a level name of 1 to 255 characters is required", ErrHierarchyValidation)
	}
	if level < EpicHierarchyLevel {
		return fmt.Errorf("%w: the base and subtask levels cannot be renamed", ErrHierarchyValidation)
	}
	if err := s.requireSiteAdmin(ctx, workspaceID, actorID); err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if taken, err := hierarchyNameTaken(ctx, tx, workspaceID, name, &level); err != nil {
		return err
	} else if taken {
		return fmt.Errorf("%w: a level with this name already exists", ErrHierarchyValidation)
	}
	tag, err := tx.Exec(ctx, `UPDATE issue_type_hierarchy_levels SET name=$3
		WHERE workspace_id=$1 AND level=$2`, workspaceID, level, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if err = appendHierarchyAction(ctx, tx, workspaceID, actorID, "hierarchy_level_renamed", level, name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteHierarchyLevel removes the site's top level, which must be above Epic
// and hold no work types.
func (s *Store) DeleteHierarchyLevel(ctx context.Context, workspaceID, actorID string, level int) error {
	if level <= EpicHierarchyLevel {
		return fmt.Errorf("%w: the Epic, base and subtask levels cannot be removed", ErrHierarchyValidation)
	}
	if err := s.requireSiteAdmin(ctx, workspaceID, actorID); err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var top int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(level),0) FROM issue_type_hierarchy_levels WHERE workspace_id=$1`, workspaceID).Scan(&top); err != nil {
		return err
	}
	if level != top {
		return fmt.Errorf("%w: only the top level can be removed", ErrHierarchyValidation)
	}
	var occupied bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_types
		WHERE COALESCE(workspace_id,$1)=$1 AND hierarchy_level=$2)`, workspaceID, level).Scan(&occupied); err != nil {
		return err
	}
	if occupied {
		return fmt.Errorf("%w: move its work types to another level first", ErrHierarchyValidation)
	}
	tag, err := tx.Exec(ctx, `DELETE FROM issue_type_hierarchy_levels WHERE workspace_id=$1 AND level=$2`, workspaceID, level)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if err = appendHierarchyAction(ctx, tx, workspaceID, actorID, "hierarchy_level_removed", level, ""); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetWorkTypeHierarchyLevel moves a work type to another level of the site's
// hierarchy. Subtask types stay at the subtask level, which no other type may
// use, and a type only moves while nothing depends on its old place.
func (s *Store) SetWorkTypeHierarchyLevel(ctx context.Context, workspaceID, actorID, idOrName string, level int) (models.IssueType, error) {
	if err := s.requireSiteAdmin(ctx, workspaceID, actorID); err != nil {
		return models.IssueType{}, err
	}
	workType, err := s.IssueTypeByIDOrName(ctx, workspaceID, idOrName)
	if err != nil {
		return models.IssueType{}, err
	}
	if workType.Subtask || level == SubtaskHierarchyLevel {
		return models.IssueType{}, fmt.Errorf("%w: a subtask work type stays at the subtask level", ErrHierarchyValidation)
	}
	if workType.HierarchyLevel == level {
		return *workType, nil
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return models.IssueType{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var known bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_type_hierarchy_levels
		WHERE workspace_id=$1 AND level=$2)`, workspaceID, level).Scan(&known); err != nil {
		return models.IssueType{}, err
	}
	if !known {
		return models.IssueType{}, fmt.Errorf("%w: the site has no level %d", ErrHierarchyValidation, level)
	}
	// Moving a type would orphan the parents and children its work items have
	// at the level it leaves.
	var related bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM issues i WHERE i.workspace_id=$1 AND i.issuetype_id=$2
			AND (i.parent_id IS NOT NULL OR EXISTS(SELECT 1 FROM issues c WHERE c.parent_id=i.id)))`,
		workspaceID, workType.ID).Scan(&related); err != nil {
		return models.IssueType{}, err
	}
	if related {
		return models.IssueType{}, fmt.Errorf("%w: work items of this type already have a parent or children", ErrHierarchyValidation)
	}
	// A work type the site owns moves in place; a shared default moves for
	// this site only, through its override.
	if workType.WorkspaceID == "" {
		_, err = tx.Exec(ctx, `INSERT INTO issue_metadata_overrides(workspace_id,entity_type,entity_id,hierarchy_level)
			VALUES($1,'issuetype',$2,$3)
			ON CONFLICT (workspace_id,entity_type,entity_id) DO UPDATE SET hierarchy_level=EXCLUDED.hierarchy_level`,
			workspaceID, workType.ID, level)
	} else {
		_, err = tx.Exec(ctx, `UPDATE issue_types SET hierarchy_level=$3 WHERE id=$1 AND workspace_id=$2`,
			workType.ID, workspaceID, level)
	}
	if err != nil {
		return models.IssueType{}, err
	}
	if err = appendHierarchyAction(ctx, tx, workspaceID, actorID, "work_type_level_changed", level, workType.Name); err != nil {
		return models.IssueType{}, err
	}
	moved, err := scanEffectiveIssueType(tx.QueryRow(ctx, effectiveIssueTypeSelect+` AND t.id=$2`, workspaceID, workType.ID))
	if err != nil {
		return models.IssueType{}, err
	}
	return moved, tx.Commit(ctx)
}

// hierarchyNameTaken reports whether another level already has the name.
func hierarchyNameTaken(ctx context.Context, tx pgx.Tx, workspaceID, name string, exceptLevel *int) (bool, error) {
	except := -2 // no level
	if exceptLevel != nil {
		except = *exceptLevel
	}
	var taken bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_type_hierarchy_levels
		WHERE workspace_id=$1 AND lower(name)=lower($2) AND level<>$3)`, workspaceID, name, except).Scan(&taken)
	return taken, err
}

// appendHierarchyAction records a hierarchy change in the action log.
func appendHierarchyAction(ctx context.Context, tx pgx.Tx, workspaceID, actorID, kind string, level int, name string) error {
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"kind": kind, "level": level, "name": name})
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: "jira_configuration",
		EntityID: fmt.Sprintf("hierarchy:%d", level), Op: models.OpUpsert,
		SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	})
}

// ParentOptions lists the work items in a project that a work type at level
// may take as a parent: those one level above it, most recently updated first.
func (s *Store) ParentOptions(ctx context.Context, workspaceID, projectID string, level int) ([]models.CreateFieldOption, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT i.jira_id::text, i.key, i.summary
		FROM issues i JOIN issue_types t ON t.id=i.issuetype_id
		LEFT JOIN issue_metadata_overrides o ON o.workspace_id=$2 AND o.entity_type='issuetype' AND o.entity_id=t.id
		WHERE i.project_id=$1 AND COALESCE(o.hierarchy_level,t.hierarchy_level)=$3
		ORDER BY i.updated_seq DESC, i.key LIMIT 200`, projectID, workspaceID, level+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	options := []models.CreateFieldOption{}
	for rows.Next() {
		option := models.CreateFieldOption{HierarchyLevel: level + 1}
		if err := rows.Scan(&option.ID, &option.Key, &option.Name); err != nil {
			return nil, err
		}
		option.Name = option.Key + " — " + option.Name
		options = append(options, option)
	}
	return options, rows.Err()
}
