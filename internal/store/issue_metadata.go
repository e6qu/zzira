package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// Issue types, priorities and resolutions as a site sees them.
//
// Jira's defaults are stored once and shared. A site's rename, reorder or
// deletion of a default is an override kept for that site alone, and what a
// site creates belongs to that site. Every read here merges the two, so a site
// sees exactly its own set and never another site's changes.

var (
	ErrIssueMetadataValidation = errors.New("invalid issue metadata")
	ErrIssueMetadataConflict   = errors.New("issue metadata conflict")
	ErrIssueMetadataNotFound   = errors.New("issue metadata not found")
)

// ---- Issue types ----

const effectiveIssueTypeSelect = `SELECT t.id, t.jira_id, COALESCE(t.workspace_id,''),
	COALESCE(o.name,t.name), t.icon, t.subtask, COALESCE(o.description,t.description),
	COALESCE(o.hierarchy_level,t.hierarchy_level), COALESCE(o.avatar_id,t.avatar_id,0)
	FROM issue_types t
	LEFT JOIN issue_metadata_overrides o ON o.workspace_id=$1 AND o.entity_type='issuetype' AND o.entity_id=t.id
	WHERE (t.workspace_id IS NULL OR t.workspace_id=$1) AND NOT COALESCE(o.deleted,FALSE)`

func scanEffectiveIssueType(row pgx.Row) (models.IssueType, error) {
	var t models.IssueType
	err := row.Scan(&t.ID, &t.JiraID, &t.WorkspaceID, &t.Name, &t.Icon, &t.Subtask, &t.Description, &t.HierarchyLevel, &t.AvatarID)
	return t, err
}

// IssueTypesForWorkspace lists the issue types a site has, parents before
// children and then in the order they were made.
func (s *Store) IssueTypesForWorkspace(ctx context.Context, workspaceID string) ([]models.IssueType, error) {
	rows, err := s.Pool.Query(ctx, effectiveIssueTypeSelect+` ORDER BY t.hierarchy_level DESC, t.jira_id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.IssueType{}
	for rows.Next() {
		t, err := scanEffectiveIssueType(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// IssueTypeInWorkspace resolves the ways a client names an issue type: its
// numeric id, its name, or — for requests the product itself makes — its
// internal id.
func (s *Store) IssueTypeInWorkspace(ctx context.Context, workspaceID, idOrName string) (models.IssueType, error) {
	idOrName = strings.TrimSpace(idOrName)
	t, err := scanEffectiveIssueType(s.Pool.QueryRow(ctx, effectiveIssueTypeSelect+`
		AND (t.id=$2 OR t.jira_id::text=$2 OR lower(COALESCE(o.name,t.name))=lower($2))
		ORDER BY (t.jira_id::text=$2) DESC, (t.id=$2) DESC LIMIT 1`, workspaceID, idOrName))
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrIssueMetadataNotFound
	}
	return t, err
}

func validMetadataName(name string, max int) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > max {
		return "", fmt.Errorf("%w: a name of 1 to %d characters is required", ErrIssueMetadataValidation, max)
	}
	return name, nil
}

// issueMetadataNameTaken reports whether another effective entry of the same
// kind already carries the name. Names are unique within a site, compared
// without regard to case.
func issueMetadataNameTaken(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, table, entityType, workspaceID, name, exceptID string) (bool, error) {
	var taken bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+` e
		LEFT JOIN issue_metadata_overrides o ON o.workspace_id=$1 AND o.entity_type=$2 AND o.entity_id=e.id
		WHERE (e.workspace_id IS NULL OR e.workspace_id=$1) AND NOT COALESCE(o.deleted,FALSE)
		AND lower(COALESCE(o.name,e.name))=lower($3) AND e.id<>$4)`, workspaceID, entityType, name, exceptID).Scan(&taken)
	return taken, err
}

// CreateIssueType adds an issue type to the site. A standard type sits at
// hierarchy level 0 and a subtask type at -1; the site's default issue type
// scheme gains it, as Jira adds every new type to that scheme.
func (s *Store) CreateIssueType(ctx context.Context, workspaceID, name, description, kind string, hierarchyLevel *int) (models.IssueType, error) {
	name, err := validMetadataName(name, 60)
	if err != nil {
		return models.IssueType{}, err
	}
	level := 0
	switch kind {
	case "", "standard":
		if hierarchyLevel != nil {
			if *hierarchyLevel < 0 || *hierarchyLevel > 0 {
				return models.IssueType{}, fmt.Errorf("%w: a standard issue type is created at hierarchy level 0", ErrIssueMetadataValidation)
			}
		}
	case "subtask":
		level = -1
		if hierarchyLevel != nil && *hierarchyLevel != -1 {
			return models.IssueType{}, fmt.Errorf("%w: a subtask issue type is at hierarchy level -1", ErrIssueMetadataValidation)
		}
	default:
		return models.IssueType{}, fmt.Errorf("%w: type is standard or subtask", ErrIssueMetadataValidation)
	}
	if len(description) > 4000 {
		return models.IssueType{}, fmt.Errorf("%w: a description of at most 4000 characters is allowed", ErrIssueMetadataValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return models.IssueType{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if taken, err := issueMetadataNameTaken(ctx, tx, "issue_types", "issuetype", workspaceID, name, ""); err != nil {
		return models.IssueType{}, err
	} else if taken {
		return models.IssueType{}, fmt.Errorf("%w: an issue type with this name already exists", ErrIssueMetadataConflict)
	}
	id := NewID("it")
	icon := "task"
	if level == -1 {
		icon = "subtask"
	}
	if _, err = tx.Exec(ctx, `INSERT INTO issue_types(id,workspace_id,name,icon,subtask,description,hierarchy_level)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, id, workspaceID, name, icon, level == -1, description, level); err != nil {
		return models.IssueType{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO issue_type_scheme_items(scheme_id,issue_type_id,position)
		SELECT sc.id, $2, COALESCE((SELECT max(position)+1 FROM issue_type_scheme_items WHERE scheme_id=sc.id),0)
		FROM issue_type_schemes sc WHERE sc.workspace_id=$1 AND sc.is_default`, workspaceID, id); err != nil {
		return models.IssueType{}, err
	}
	created, err := scanEffectiveIssueType(tx.QueryRow(ctx, effectiveIssueTypeSelect+` AND t.id=$2`, workspaceID, id))
	if err != nil {
		return models.IssueType{}, err
	}
	return created, tx.Commit(ctx)
}

// UpdateIssueType renames or re-describes an issue type. A shared default is
// changed for this site only.
func (s *Store) UpdateIssueType(ctx context.Context, workspaceID, idOrName string, name, description *string, avatarID *int64) (models.IssueType, error) {
	if name == nil && description == nil && avatarID == nil {
		return models.IssueType{}, fmt.Errorf("%w: at least one of name, description or avatarId is required", ErrIssueMetadataValidation)
	}
	current, err := s.IssueTypeInWorkspace(ctx, workspaceID, idOrName)
	if err != nil {
		return current, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return current, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if name != nil {
		trimmed, err := validMetadataName(*name, 60)
		if err != nil {
			return current, err
		}
		if taken, err := issueMetadataNameTaken(ctx, tx, "issue_types", "issuetype", workspaceID, trimmed, current.ID); err != nil {
			return current, err
		} else if taken {
			return current, fmt.Errorf("%w: an issue type with this name already exists", ErrIssueMetadataConflict)
		}
		name = &trimmed
	}
	if description != nil && len(*description) > 4000 {
		return current, fmt.Errorf("%w: a description of at most 4000 characters is allowed", ErrIssueMetadataValidation)
	}
	if current.WorkspaceID == "" {
		_, err = tx.Exec(ctx, `INSERT INTO issue_metadata_overrides(workspace_id,entity_type,entity_id,name,description,avatar_id)
			VALUES($1,'issuetype',$2,$3,$4,$5)
			ON CONFLICT (workspace_id,entity_type,entity_id) DO UPDATE SET
			  name=COALESCE(EXCLUDED.name,issue_metadata_overrides.name),
			  description=COALESCE(EXCLUDED.description,issue_metadata_overrides.description),
			  avatar_id=COALESCE(EXCLUDED.avatar_id,issue_metadata_overrides.avatar_id)`,
			workspaceID, current.ID, name, description, avatarID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE issue_types SET name=COALESCE($3,name), description=COALESCE($4,description), avatar_id=COALESCE($5,avatar_id)
			WHERE id=$2 AND workspace_id=$1`, workspaceID, current.ID, name, description, avatarID)
	}
	if err != nil {
		return current, err
	}
	updated, err := scanEffectiveIssueType(tx.QueryRow(ctx, effectiveIssueTypeSelect+` AND t.id=$2`, workspaceID, current.ID))
	if err != nil {
		return current, err
	}
	return updated, tx.Commit(ctx)
}

// AlternativeIssueTypes lists what an issue type's issues could move to when it
// is deleted: the site's other issue types of the same kind, since an issue
// cannot become a subtask, or stop being one, by losing its type.
func (s *Store) AlternativeIssueTypes(ctx context.Context, workspaceID, idOrName string) ([]models.IssueType, error) {
	current, err := s.IssueTypeInWorkspace(ctx, workspaceID, idOrName)
	if err != nil {
		return nil, err
	}
	all, err := s.IssueTypesForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	out := []models.IssueType{}
	for _, t := range all {
		if t.ID != current.ID && t.Subtask == current.Subtask {
			out = append(out, t)
		}
	}
	return out, nil
}

// DeleteIssueType removes an issue type from the site. Issues of that type move
// to the alternative, and the type leaves every scheme and mapping that named
// it, so nothing is left pointing at a type the site no longer has.
func (s *Store) DeleteIssueType(ctx context.Context, workspaceID, idOrName, alternative string) error {
	current, err := s.IssueTypeInWorkspace(ctx, workspaceID, idOrName)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var inUse int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM issues WHERE workspace_id=$1 AND issuetype_id=$2`, workspaceID, current.ID).Scan(&inUse); err != nil {
		return err
	}
	var target models.IssueType
	if strings.TrimSpace(alternative) != "" {
		target, err = s.IssueTypeInWorkspace(ctx, workspaceID, alternative)
		if err != nil {
			return fmt.Errorf("%w: the alternative issue type does not exist", ErrIssueMetadataNotFound)
		}
		if target.ID == current.ID {
			return fmt.Errorf("%w: an issue type cannot be its own alternative", ErrIssueMetadataConflict)
		}
		if target.Subtask != current.Subtask {
			return fmt.Errorf("%w: the alternative issue type must be the same kind of issue type", ErrIssueMetadataValidation)
		}
	} else if inUse > 0 {
		return fmt.Errorf("%w: the issue type is in use, so an alternative issue type is required", ErrIssueMetadataNotFound)
	}
	if inUse > 0 {
		if _, err = tx.Exec(ctx, `UPDATE issues SET issuetype_id=$3, updated_at=now() WHERE workspace_id=$1 AND issuetype_id=$2`, workspaceID, current.ID, target.ID); err != nil {
			return err
		}
	}
	for _, statement := range []string{
		`DELETE FROM issue_type_scheme_items WHERE issue_type_id=$2 AND scheme_id IN (SELECT id FROM issue_type_schemes WHERE workspace_id=$1)`,
		`UPDATE issue_type_schemes SET default_issue_type_id=NULL WHERE workspace_id=$1 AND default_issue_type_id=$2`,
		`DELETE FROM issue_type_screen_scheme_items WHERE workspace_id=$1 AND issue_type_id=$2`,
		`DELETE FROM field_configuration_scheme_items WHERE workspace_id=$1 AND issue_type_id=$2`,
		`DELETE FROM custom_field_context_issue_types WHERE issue_type_id=$2 AND context_id IN (SELECT id FROM custom_field_contexts WHERE workspace_id=$1)`,
		`UPDATE workflow_schemes SET issue_type_mappings = issue_type_mappings - $2 WHERE workspace_id=$1 AND issue_type_mappings ? $2`,
		`DELETE FROM issue_type_properties WHERE workspace_id=$1 AND issue_type_id=$2`,
	} {
		if _, err = tx.Exec(ctx, statement, workspaceID, current.ID); err != nil {
			return err
		}
	}
	if current.WorkspaceID == "" {
		_, err = tx.Exec(ctx, `INSERT INTO issue_metadata_overrides(workspace_id,entity_type,entity_id,deleted)
			VALUES($1,'issuetype',$2,TRUE) ON CONFLICT (workspace_id,entity_type,entity_id) DO UPDATE SET deleted=TRUE`, workspaceID, current.ID)
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM issue_types WHERE id=$2 AND workspace_id=$1`, workspaceID, current.ID)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---- Issue type properties ----

var entityPropertyKey = regexp.MustCompile(`^.{1,255}$`)

// IssueTypePropertyKeys lists the keys stored against an issue type.
func (s *Store) IssueTypePropertyKeys(ctx context.Context, workspaceID, issueTypeIDOrName string) (models.IssueType, []string, error) {
	t, err := s.IssueTypeInWorkspace(ctx, workspaceID, issueTypeIDOrName)
	if err != nil {
		return t, nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT key FROM issue_type_properties WHERE workspace_id=$1 AND issue_type_id=$2 ORDER BY key`, workspaceID, t.ID)
	if err != nil {
		return t, nil, err
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			return t, nil, err
		}
		keys = append(keys, key)
	}
	return t, keys, rows.Err()
}

func (s *Store) IssueTypeProperty(ctx context.Context, workspaceID, issueTypeIDOrName, key string) (json.RawMessage, error) {
	t, err := s.IssueTypeInWorkspace(ctx, workspaceID, issueTypeIDOrName)
	if err != nil {
		return nil, err
	}
	var value json.RawMessage
	err = s.Pool.QueryRow(ctx, `SELECT value FROM issue_type_properties WHERE workspace_id=$1 AND issue_type_id=$2 AND key=$3`, workspaceID, t.ID, key).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrIssueMetadataNotFound
	}
	return value, err
}

// SetIssueTypeProperty stores a value against an issue type and reports whether
// the key is new, because Jira answers 201 for a new property and 200 for a
// replaced one. The value is a non-empty JSON value of at most 32768 characters.
func (s *Store) SetIssueTypeProperty(ctx context.Context, workspaceID, issueTypeIDOrName, key string, value json.RawMessage) (bool, error) {
	if !entityPropertyKey.MatchString(key) {
		return false, fmt.Errorf("%w: a property key of 1 to 255 characters is required", ErrIssueMetadataValidation)
	}
	trimmed := strings.TrimSpace(string(value))
	if trimmed == "" || !json.Valid(value) {
		return false, fmt.Errorf("%w: the property value must be valid, non-empty JSON", ErrIssueMetadataValidation)
	}
	if len(trimmed) > 32768 {
		return false, fmt.Errorf("%w: the property value must be at most 32768 characters", ErrIssueMetadataValidation)
	}
	t, err := s.IssueTypeInWorkspace(ctx, workspaceID, issueTypeIDOrName)
	if err != nil {
		return false, err
	}
	var created bool
	err = s.Pool.QueryRow(ctx, `INSERT INTO issue_type_properties(workspace_id,issue_type_id,key,value) VALUES($1,$2,$3,$4::jsonb)
		ON CONFLICT (workspace_id,issue_type_id,key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()
		RETURNING (xmax = 0)`, workspaceID, t.ID, key, value).Scan(&created)
	return created, err
}

func (s *Store) DeleteIssueTypeProperty(ctx context.Context, workspaceID, issueTypeIDOrName, key string) error {
	t, err := s.IssueTypeInWorkspace(ctx, workspaceID, issueTypeIDOrName)
	if err != nil {
		return err
	}
	tag, err := s.Pool.Exec(ctx, `DELETE FROM issue_type_properties WHERE workspace_id=$1 AND issue_type_id=$2 AND key=$3`, workspaceID, t.ID, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrIssueMetadataNotFound
	}
	return nil
}

// ---- Priorities ----

const effectivePrioritySelect = `SELECT p.id, p.jira_id, COALESCE(p.workspace_id,''),
	COALESCE(o.name,p.name), COALESCE(o.description,p.description), COALESCE(o.status_color,p.status_color),
	COALESCE(o.icon_url,p.icon_url), COALESCE(o.avatar_id,p.avatar_id,0), COALESCE(o.position,p.position),
	COALESCE(d.default_priority_id=p.id, FALSE)
	FROM priorities p
	LEFT JOIN issue_metadata_overrides o ON o.workspace_id=$1 AND o.entity_type='priority' AND o.entity_id=p.id
	LEFT JOIN workspace_issue_defaults d ON d.workspace_id=$1
	WHERE (p.workspace_id IS NULL OR p.workspace_id=$1) AND NOT COALESCE(o.deleted,FALSE)`

func scanEffectivePriority(row pgx.Row) (models.Priority, error) {
	var p models.Priority
	err := row.Scan(&p.ID, &p.JiraID, &p.WorkspaceID, &p.Name, &p.Description, &p.StatusColor, &p.IconURL, &p.AvatarID, &p.Position, &p.IsDefault)
	return p, err
}

// PrioritiesForWorkspace lists a site's priorities in the order the site set.
func (s *Store) PrioritiesForWorkspace(ctx context.Context, workspaceID string) ([]models.Priority, error) {
	rows, err := s.Pool.Query(ctx, effectivePrioritySelect+` ORDER BY COALESCE(o.position,p.position), p.jira_id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Priority{}
	for rows.Next() {
		p, err := scanEffectivePriority(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) PriorityInWorkspace(ctx context.Context, workspaceID, idOrName string) (models.Priority, error) {
	idOrName = strings.TrimSpace(idOrName)
	p, err := scanEffectivePriority(s.Pool.QueryRow(ctx, effectivePrioritySelect+`
		AND (p.id=$2 OR p.jira_id::text=$2 OR lower(COALESCE(o.name,p.name))=lower($2))
		ORDER BY (p.jira_id::text=$2) DESC, (p.id=$2) DESC LIMIT 1`, workspaceID, idOrName))
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrIssueMetadataNotFound
	}
	return p, err
}

var hexColor = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// PriorityInput is what a create or an update may set.
type PriorityInput struct {
	Name        *string
	Description *string
	StatusColor *string
	IconURL     *string
	AvatarID    *int64
}

func validPriorityInput(in PriorityInput, creating bool) (PriorityInput, error) {
	if creating && (in.Name == nil || in.StatusColor == nil) {
		return in, fmt.Errorf("%w: name and statusColor are required", ErrIssueMetadataValidation)
	}
	if !creating && in.Name == nil && in.Description == nil && in.StatusColor == nil && in.IconURL == nil && in.AvatarID == nil {
		return in, fmt.Errorf("%w: at least one request body parameter must be defined", ErrIssueMetadataValidation)
	}
	if in.Name != nil {
		name, err := validMetadataName(*in.Name, 60)
		if err != nil {
			return in, err
		}
		in.Name = &name
	}
	if in.StatusColor != nil && !hexColor.MatchString(*in.StatusColor) {
		return in, fmt.Errorf("%w: statusColor is a 3-digit or 6-digit hexadecimal colour such as #FFF or #06f", ErrIssueMetadataValidation)
	}
	// An empty iconUrl on an edit leaves the icon alone; a priority always has
	// one.
	if !creating && in.IconURL != nil && strings.TrimSpace(*in.IconURL) == "" {
		in.IconURL = nil
	}
	if in.IconURL != nil && in.AvatarID != nil {
		return in, fmt.Errorf("%w: either iconUrl or avatarId may be given, not both", ErrIssueMetadataValidation)
	}
	if in.Description != nil && len(*in.Description) > 255 {
		return in, fmt.Errorf("%w: a description of at most 255 characters is allowed", ErrIssueMetadataValidation)
	}
	return in, nil
}

// PriorityIconNames are Jira's built-in priority icons, most important first.
var PriorityIconNames = []string{"highest", "high", "medium", "low", "lowest", "blocker", "critical", "major", "minor", "trivial"}

// DefaultPriorityIconURL is the icon a priority created without one takes, so
// every priority a client reads has an icon to show.
const DefaultPriorityIconURL = "/images/icons/priorities/medium.svg"

// CreatePriority adds a priority to the site, last in order, and to the site's
// default priority scheme.
func (s *Store) CreatePriority(ctx context.Context, workspaceID string, in PriorityInput) (models.Priority, error) {
	in, err := validPriorityInput(in, true)
	if err != nil {
		return models.Priority{}, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return models.Priority{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if taken, err := issueMetadataNameTaken(ctx, tx, "priorities", "priority", workspaceID, *in.Name, ""); err != nil {
		return models.Priority{}, err
	} else if taken {
		return models.Priority{}, fmt.Errorf("%w: a priority with this name already exists", ErrIssueMetadataValidation)
	}
	id := NewID("pr")
	description, icon := "", ""
	if in.Description != nil {
		description = *in.Description
	}
	if in.IconURL != nil {
		icon = *in.IconURL
	}
	if strings.TrimSpace(icon) == "" {
		icon = DefaultPriorityIconURL
	}
	if _, err = tx.Exec(ctx, `INSERT INTO priorities(id,workspace_id,name,description,status_color,icon_url,avatar_id,position)
		VALUES($1,$2,$3,$4,$5,$6,$7,(SELECT COALESCE(max(COALESCE(o.position,p.position)),0)+1 FROM priorities p
		  LEFT JOIN issue_metadata_overrides o ON o.workspace_id=$2 AND o.entity_type='priority' AND o.entity_id=p.id
		  WHERE p.workspace_id IS NULL OR p.workspace_id=$2))`,
		id, workspaceID, *in.Name, description, *in.StatusColor, icon, in.AvatarID); err != nil {
		return models.Priority{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO priority_scheme_items(scheme_id,priority_id,position)
		SELECT sc.id, $2, COALESCE((SELECT max(position)+1 FROM priority_scheme_items WHERE scheme_id=sc.id),0)
		FROM priority_schemes sc WHERE sc.workspace_id=$1 AND sc.is_default`, workspaceID, id); err != nil {
		return models.Priority{}, err
	}
	created, err := scanEffectivePriority(tx.QueryRow(ctx, effectivePrioritySelect+` AND p.id=$2`, workspaceID, id))
	if err != nil {
		return models.Priority{}, err
	}
	return created, tx.Commit(ctx)
}

// UpdatePriority changes a priority; a shared default changes for this site only.
func (s *Store) UpdatePriority(ctx context.Context, workspaceID, idOrName string, in PriorityInput) error {
	in, err := validPriorityInput(in, false)
	if err != nil {
		return err
	}
	current, err := s.PriorityInWorkspace(ctx, workspaceID, idOrName)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if in.Name != nil {
		if taken, err := issueMetadataNameTaken(ctx, tx, "priorities", "priority", workspaceID, *in.Name, current.ID); err != nil {
			return err
		} else if taken {
			return fmt.Errorf("%w: a priority with this name already exists", ErrIssueMetadataValidation)
		}
	}
	if current.WorkspaceID == "" {
		_, err = tx.Exec(ctx, `INSERT INTO issue_metadata_overrides(workspace_id,entity_type,entity_id,name,description,status_color,icon_url,avatar_id)
			VALUES($1,'priority',$2,$3,$4,$5,$6,$7)
			ON CONFLICT (workspace_id,entity_type,entity_id) DO UPDATE SET
			  name=COALESCE(EXCLUDED.name,issue_metadata_overrides.name),
			  description=COALESCE(EXCLUDED.description,issue_metadata_overrides.description),
			  status_color=COALESCE(EXCLUDED.status_color,issue_metadata_overrides.status_color),
			  icon_url=COALESCE(EXCLUDED.icon_url,issue_metadata_overrides.icon_url),
			  avatar_id=COALESCE(EXCLUDED.avatar_id,issue_metadata_overrides.avatar_id)`,
			workspaceID, current.ID, in.Name, in.Description, in.StatusColor, in.IconURL, in.AvatarID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE priorities SET name=COALESCE($3,name), description=COALESCE($4,description),
			status_color=COALESCE($5,status_color), icon_url=COALESCE($6,icon_url), avatar_id=COALESCE($7,avatar_id)
			WHERE id=$2 AND workspace_id=$1`, workspaceID, current.ID, in.Name, in.Description, in.StatusColor, in.IconURL, in.AvatarID)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetDefaultPriority makes a priority the one an issue gets when none is chosen.
func (s *Store) SetDefaultPriority(ctx context.Context, workspaceID, idOrName string) error {
	p, err := s.PriorityInWorkspace(ctx, workspaceID, idOrName)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO workspace_issue_defaults(workspace_id,default_priority_id) VALUES($1,$2)
		ON CONFLICT (workspace_id) DO UPDATE SET default_priority_id=EXCLUDED.default_priority_id`, workspaceID, p.ID)
	return err
}

// DefaultPriority is the priority an issue gets when none is chosen.
func (s *Store) DefaultPriority(ctx context.Context, workspaceID string) (models.Priority, error) {
	var id string
	if err := s.Pool.QueryRow(ctx, `SELECT COALESCE(default_priority_id,'') FROM workspace_issue_defaults WHERE workspace_id=$1`, workspaceID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Priority{}, ErrIssueMetadataNotFound
		}
		return models.Priority{}, err
	}
	if id == "" {
		return models.Priority{}, ErrIssueMetadataNotFound
	}
	return s.PriorityInWorkspace(ctx, workspaceID, id)
}

// reorderMetadata moves entries of one kind to a new place in the site's order.
// Either after names the entry they follow, or position is First or Last; the
// moved entries keep the order they were given in.
func (s *Store) reorderMetadata(ctx context.Context, workspaceID, entityType string, ordered []string, ids []string, after, position string) error {
	if len(ids) == 0 {
		return fmt.Errorf("%w: ids are required", ErrIssueMetadataValidation)
	}
	if (after == "") == (position == "") {
		return fmt.Errorf("%w: either after or position is required", ErrIssueMetadataValidation)
	}
	index := map[string]int{}
	for i, id := range ordered {
		index[id] = i
	}
	moving := map[string]bool{}
	for _, id := range ids {
		if _, ok := index[id]; !ok {
			return fmt.Errorf("%w: %s is not one of the site's entries", ErrIssueMetadataNotFound, id)
		}
		if moving[id] {
			return fmt.Errorf("%w: ids must be unique", ErrIssueMetadataValidation)
		}
		moving[id] = true
	}
	rest := []string{}
	for _, id := range ordered {
		if !moving[id] {
			rest = append(rest, id)
		}
	}
	var result []string
	switch {
	case after != "":
		if moving[after] {
			return fmt.Errorf("%w: after must not be one of the moved ids", ErrIssueMetadataValidation)
		}
		if _, ok := index[after]; !ok {
			return fmt.Errorf("%w: after is not one of the site's entries", ErrIssueMetadataNotFound)
		}
		for _, id := range rest {
			result = append(result, id)
			if id == after {
				result = append(result, ids...)
			}
		}
	case strings.EqualFold(position, "First"):
		result = append(append(result, ids...), rest...)
	case strings.EqualFold(position, "Last"):
		result = append(append(result, rest...), ids...)
	default:
		return fmt.Errorf("%w: position is First or Last", ErrIssueMetadataValidation)
	}
	table := map[string]string{"priority": "priorities", "resolution": "resolutions"}[entityType]
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for i, id := range result {
		var owned bool
		if err = tx.QueryRow(ctx, `SELECT workspace_id IS NOT NULL FROM `+table+` WHERE id=$1`, id).Scan(&owned); err != nil {
			return err
		}
		if owned {
			_, err = tx.Exec(ctx, `UPDATE `+table+` SET position=$3 WHERE id=$2 AND workspace_id=$1`, workspaceID, id, i+1)
		} else {
			_, err = tx.Exec(ctx, `INSERT INTO issue_metadata_overrides(workspace_id,entity_type,entity_id,position) VALUES($1,$2,$3,$4)
				ON CONFLICT (workspace_id,entity_type,entity_id) DO UPDATE SET position=EXCLUDED.position`, workspaceID, entityType, id, i+1)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// MovePriorities reorders priorities, which is the order people choose from.
func (s *Store) MovePriorities(ctx context.Context, workspaceID string, ids []string, after, position string) error {
	all, err := s.PrioritiesForWorkspace(ctx, workspaceID)
	if err != nil {
		return err
	}
	ordered, internal, err := resolveMetadataIDs(len(all), func(i int) (string, int64, string) { return all[i].ID, all[i].JiraID, all[i].Name }, ids)
	if err != nil {
		return err
	}
	afterID := ""
	if after != "" {
		_, afterIDs, err := resolveMetadataIDs(len(all), func(i int) (string, int64, string) { return all[i].ID, all[i].JiraID, all[i].Name }, []string{after})
		if err != nil {
			return err
		}
		afterID = afterIDs[0]
	}
	return s.reorderMetadata(ctx, workspaceID, "priority", ordered, internal, afterID, position)
}

// resolveMetadataIDs maps the numeric ids a client sends to internal ids, and
// returns the site's current internal order alongside them.
func resolveMetadataIDs(n int, at func(int) (string, int64, string), wire []string) ([]string, []string, error) {
	ordered := make([]string, n)
	byWire := map[string]string{}
	for i := 0; i < n; i++ {
		id, jira, _ := at(i)
		ordered[i] = id
		byWire[strconv.FormatInt(jira, 10)] = id
		byWire[id] = id
	}
	internal := make([]string, 0, len(wire))
	for _, raw := range wire {
		id, ok := byWire[strings.TrimSpace(raw)]
		if !ok {
			return nil, nil, fmt.Errorf("%w: %s does not exist", ErrIssueMetadataNotFound, raw)
		}
		internal = append(internal, id)
	}
	return ordered, internal, nil
}

// PriorityUsage counts the issues that carry a priority in the site.
func (s *Store) PriorityUsage(ctx context.Context, workspaceID, priorityID string) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM issues WHERE workspace_id=$1 AND priority_id=$2`, workspaceID, priorityID).Scan(&n)
	return n, err
}

// deletePriorityNow removes a priority from the site. Its issues take the
// site's default priority, which is what Jira gives an issue whose priority is
// deleted; the default itself cannot be deleted, because nothing would be left
// to replace it with.
func (s *Store) deletePriorityNow(ctx context.Context, workspaceID, priorityID string) (int, error) {
	current, err := s.PriorityInWorkspace(ctx, workspaceID, priorityID)
	if err != nil {
		return 0, err
	}
	if current.IsDefault {
		return 0, fmt.Errorf("%w: the default priority cannot be deleted", ErrIssueMetadataValidation)
	}
	fallback, err := s.DefaultPriority(ctx, workspaceID)
	if err != nil {
		return 0, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE issues SET priority_id=$3, updated_at=now() WHERE workspace_id=$1 AND priority_id=$2`, workspaceID, current.ID, fallback.ID)
	if err != nil {
		return 0, err
	}
	for _, statement := range []string{
		`DELETE FROM priority_scheme_items WHERE priority_id=$2 AND scheme_id IN (SELECT id FROM priority_schemes WHERE workspace_id=$1)`,
		`UPDATE priority_schemes SET default_priority_id=(SELECT default_priority_id FROM workspace_issue_defaults WHERE workspace_id=$1) WHERE workspace_id=$1 AND default_priority_id=$2`,
	} {
		if _, err = tx.Exec(ctx, statement, workspaceID, current.ID); err != nil {
			return 0, err
		}
	}
	if current.WorkspaceID == "" {
		_, err = tx.Exec(ctx, `INSERT INTO issue_metadata_overrides(workspace_id,entity_type,entity_id,deleted)
			VALUES($1,'priority',$2,TRUE) ON CONFLICT (workspace_id,entity_type,entity_id) DO UPDATE SET deleted=TRUE`, workspaceID, current.ID)
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM priorities WHERE id=$2 AND workspace_id=$1`, workspaceID, current.ID)
	}
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

// ---- Resolutions ----

const effectiveResolutionSelect = `SELECT r.id, r.jira_id, COALESCE(r.workspace_id,''),
	COALESCE(o.name,r.name), COALESCE(o.description,r.description), COALESCE(o.position,r.position),
	COALESCE(d.default_resolution_id=r.id, FALSE)
	FROM resolutions r
	LEFT JOIN issue_metadata_overrides o ON o.workspace_id=$1 AND o.entity_type='resolution' AND o.entity_id=r.id
	LEFT JOIN workspace_issue_defaults d ON d.workspace_id=$1
	WHERE (r.workspace_id IS NULL OR r.workspace_id=$1) AND NOT COALESCE(o.deleted,FALSE)`

func scanEffectiveResolution(row pgx.Row) (models.Resolution, error) {
	var r models.Resolution
	err := row.Scan(&r.ID, &r.JiraID, &r.WorkspaceID, &r.Name, &r.Description, &r.Position, &r.IsDefault)
	return r, err
}

func (s *Store) ResolutionsForWorkspace(ctx context.Context, workspaceID string) ([]models.Resolution, error) {
	rows, err := s.Pool.Query(ctx, effectiveResolutionSelect+` ORDER BY COALESCE(o.position,r.position), r.jira_id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Resolution{}
	for rows.Next() {
		r, err := scanEffectiveResolution(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ResolutionInWorkspace(ctx context.Context, workspaceID, idOrName string) (models.Resolution, error) {
	idOrName = strings.TrimSpace(idOrName)
	r, err := scanEffectiveResolution(s.Pool.QueryRow(ctx, effectiveResolutionSelect+`
		AND (r.id=$2 OR r.jira_id::text=$2 OR lower(COALESCE(o.name,r.name))=lower($2))
		ORDER BY (r.jira_id::text=$2) DESC, (r.id=$2) DESC LIMIT 1`, workspaceID, idOrName))
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrIssueMetadataNotFound
	}
	return r, err
}

func validResolutionInput(name, description string) (string, error) {
	name, err := validMetadataName(name, 60)
	if err != nil {
		return "", err
	}
	if len(description) > 255 {
		return "", fmt.Errorf("%w: a description of at most 255 characters is allowed", ErrIssueMetadataValidation)
	}
	return name, nil
}

func (s *Store) CreateResolution(ctx context.Context, workspaceID, name, description string) (models.Resolution, error) {
	name, err := validResolutionInput(name, description)
	if err != nil {
		return models.Resolution{}, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return models.Resolution{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if taken, err := issueMetadataNameTaken(ctx, tx, "resolutions", "resolution", workspaceID, name, ""); err != nil {
		return models.Resolution{}, err
	} else if taken {
		return models.Resolution{}, fmt.Errorf("%w: a resolution with this name already exists", ErrIssueMetadataValidation)
	}
	id := NewID("res")
	if _, err = tx.Exec(ctx, `INSERT INTO resolutions(id,workspace_id,name,description,position)
		VALUES($1,$2,$3,$4,(SELECT COALESCE(max(COALESCE(o.position,r.position)),0)+1 FROM resolutions r
		  LEFT JOIN issue_metadata_overrides o ON o.workspace_id=$2 AND o.entity_type='resolution' AND o.entity_id=r.id
		  WHERE r.workspace_id IS NULL OR r.workspace_id=$2))`, id, workspaceID, name, description); err != nil {
		return models.Resolution{}, err
	}
	created, err := scanEffectiveResolution(tx.QueryRow(ctx, effectiveResolutionSelect+` AND r.id=$2`, workspaceID, id))
	if err != nil {
		return models.Resolution{}, err
	}
	return created, tx.Commit(ctx)
}

// UpdateResolution renames a resolution; a shared default changes for this site only.
func (s *Store) UpdateResolution(ctx context.Context, workspaceID, idOrName, name, description string, descriptionSet bool) error {
	name, err := validResolutionInput(name, description)
	if err != nil {
		return err
	}
	current, err := s.ResolutionInWorkspace(ctx, workspaceID, idOrName)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if taken, err := issueMetadataNameTaken(ctx, tx, "resolutions", "resolution", workspaceID, name, current.ID); err != nil {
		return err
	} else if taken {
		return fmt.Errorf("%w: a resolution with this name already exists", ErrIssueMetadataValidation)
	}
	var desc *string
	if descriptionSet {
		desc = &description
	}
	if current.WorkspaceID == "" {
		_, err = tx.Exec(ctx, `INSERT INTO issue_metadata_overrides(workspace_id,entity_type,entity_id,name,description)
			VALUES($1,'resolution',$2,$3,$4)
			ON CONFLICT (workspace_id,entity_type,entity_id) DO UPDATE SET
			  name=EXCLUDED.name, description=COALESCE(EXCLUDED.description,issue_metadata_overrides.description)`,
			workspaceID, current.ID, name, desc)
	} else {
		_, err = tx.Exec(ctx, `UPDATE resolutions SET name=$3, description=COALESCE($4,description) WHERE id=$2 AND workspace_id=$1`,
			workspaceID, current.ID, name, desc)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SetDefaultResolution(ctx context.Context, workspaceID, idOrName string) error {
	r, err := s.ResolutionInWorkspace(ctx, workspaceID, idOrName)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO workspace_issue_defaults(workspace_id,default_resolution_id) VALUES($1,$2)
		ON CONFLICT (workspace_id) DO UPDATE SET default_resolution_id=EXCLUDED.default_resolution_id`, workspaceID, r.ID)
	return err
}

func (s *Store) MoveResolutions(ctx context.Context, workspaceID string, ids []string, after, position string) error {
	all, err := s.ResolutionsForWorkspace(ctx, workspaceID)
	if err != nil {
		return err
	}
	at := func(i int) (string, int64, string) { return all[i].ID, all[i].JiraID, all[i].Name }
	ordered, internal, err := resolveMetadataIDs(len(all), at, ids)
	if err != nil {
		return err
	}
	afterID := ""
	if after != "" {
		_, afterIDs, err := resolveMetadataIDs(len(all), at, []string{after})
		if err != nil {
			return err
		}
		afterID = afterIDs[0]
	}
	return s.reorderMetadata(ctx, workspaceID, "resolution", ordered, internal, afterID, position)
}

// deleteResolutionNow removes a resolution, moving its issues to the
// replacement so a resolved issue never becomes unresolved by a deletion.
func (s *Store) deleteResolutionNow(ctx context.Context, workspaceID, resolutionID, replacementID string) (int, error) {
	current, err := s.ResolutionInWorkspace(ctx, workspaceID, resolutionID)
	if err != nil {
		return 0, err
	}
	replacement, err := s.ResolutionInWorkspace(ctx, workspaceID, replacementID)
	if err != nil {
		return 0, err
	}
	if replacement.ID == current.ID {
		return 0, fmt.Errorf("%w: a resolution cannot be replaced with itself", ErrIssueMetadataValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE issues SET resolution_id=$3, updated_at=now() WHERE workspace_id=$1 AND resolution_id=$2`, workspaceID, current.ID, replacement.ID)
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `UPDATE workspace_issue_defaults SET default_resolution_id=$3 WHERE workspace_id=$1 AND default_resolution_id=$2`, workspaceID, current.ID, replacement.ID); err != nil {
		return 0, err
	}
	if current.WorkspaceID == "" {
		_, err = tx.Exec(ctx, `INSERT INTO issue_metadata_overrides(workspace_id,entity_type,entity_id,deleted)
			VALUES($1,'resolution',$2,TRUE) ON CONFLICT (workspace_id,entity_type,entity_id) DO UPDATE SET deleted=TRUE`, workspaceID, current.ID)
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM resolutions WHERE id=$2 AND workspace_id=$1`, workspaceID, current.ID)
	}
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

// DefaultResolution is the resolution an issue gets when it reaches a done
// status without one being chosen.
func (s *Store) DefaultResolution(ctx context.Context, workspaceID string) (models.Resolution, error) {
	var id string
	if err := s.Pool.QueryRow(ctx, `SELECT COALESCE(default_resolution_id,'') FROM workspace_issue_defaults WHERE workspace_id=$1`, workspaceID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Resolution{}, ErrIssueMetadataNotFound
		}
		return models.Resolution{}, err
	}
	if id == "" {
		return models.Resolution{}, ErrIssueMetadataNotFound
	}
	return s.ResolutionInWorkspace(ctx, workspaceID, id)
}
