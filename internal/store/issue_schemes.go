package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// Issue type schemes decide which issue types a project offers, and priority
// schemes decide which priorities. Every site has a default scheme of each
// kind; a project without its own scheme uses the default.

// IssueTypeScheme is one issue type scheme with its types in order.
type IssueTypeScheme struct {
	ID                 string
	Name               string
	Description        string
	DefaultIssueTypeID string
	IsDefault          bool
	IssueTypeIDs       []string
	ProjectIDs         []string
}

// PriorityScheme is one priority scheme with its priorities in order.
type PriorityScheme struct {
	ID                string
	Name              string
	Description       string
	DefaultPriorityID string
	IsDefault         bool
	PriorityIDs       []string
	ProjectIDs        []string
}

// ---- Issue type schemes ----

func (s *Store) IssueTypeSchemes(ctx context.Context, workspaceID string) ([]IssueTypeScheme, error) {
	rows, err := s.Pool.Query(ctx, `SELECT sc.id::text, sc.name, sc.description, COALESCE(sc.default_issue_type_id,''), sc.is_default,
		COALESCE((SELECT array_agg(i.issue_type_id ORDER BY i.position) FROM issue_type_scheme_items i
		  JOIN issue_types t ON t.id=i.issue_type_id
		  LEFT JOIN issue_metadata_overrides o ON o.workspace_id=sc.workspace_id AND o.entity_type='issuetype' AND o.entity_id=t.id
		  WHERE i.scheme_id=sc.id AND NOT COALESCE(o.deleted,FALSE)), ARRAY[]::text[]),
		COALESCE((SELECT array_agg(p.project_id ORDER BY p.project_id) FROM project_issue_type_schemes p WHERE p.scheme_id=sc.id), ARRAY[]::text[])
		FROM issue_type_schemes sc WHERE sc.workspace_id=$1 ORDER BY sc.id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IssueTypeScheme{}
	for rows.Next() {
		var sc IssueTypeScheme
		if err = rows.Scan(&sc.ID, &sc.Name, &sc.Description, &sc.DefaultIssueTypeID, &sc.IsDefault, &sc.IssueTypeIDs, &sc.ProjectIDs); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func (s *Store) issueTypeScheme(ctx context.Context, workspaceID, id string) (IssueTypeScheme, error) {
	all, err := s.IssueTypeSchemes(ctx, workspaceID)
	if err != nil {
		return IssueTypeScheme{}, err
	}
	for _, sc := range all {
		if sc.ID == strings.TrimSpace(id) {
			return sc, nil
		}
	}
	return IssueTypeScheme{}, ErrIssueMetadataNotFound
}

// resolveIssueTypeIDs turns the ids a client sends into internal ids, refusing
// any the site does not have and any given twice.
func (s *Store) resolveIssueTypeIDs(ctx context.Context, workspaceID string, wire []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(wire))
	for _, raw := range wire {
		t, err := s.IssueTypeInWorkspace(ctx, workspaceID, raw)
		if err != nil {
			return nil, fmt.Errorf("%w: issue type %s does not exist", ErrIssueMetadataNotFound, raw)
		}
		if seen[t.ID] {
			return nil, fmt.Errorf("%w: issue type ids must be unique", ErrIssueMetadataValidation)
		}
		seen[t.ID] = true
		out = append(out, t.ID)
	}
	return out, nil
}

func (s *Store) CreateIssueTypeScheme(ctx context.Context, workspaceID, name, description, defaultIssueType string, issueTypes []string) (string, error) {
	name, err := validMetadataName(name, 255)
	if err != nil {
		return "", err
	}
	if len(issueTypes) == 0 {
		return "", fmt.Errorf("%w: issueTypeIds must name at least one issue type", ErrIssueMetadataValidation)
	}
	ids, err := s.resolveIssueTypeIDs(ctx, workspaceID, issueTypes)
	if err != nil {
		return "", err
	}
	defaultID := ""
	if strings.TrimSpace(defaultIssueType) != "" {
		t, err := s.IssueTypeInWorkspace(ctx, workspaceID, defaultIssueType)
		if err != nil {
			return "", fmt.Errorf("%w: the default issue type does not exist", ErrIssueMetadataValidation)
		}
		defaultID = t.ID
		if !containsString(ids, defaultID) {
			return "", fmt.Errorf("%w: the default issue type must be one of the scheme's issue types", ErrIssueMetadataValidation)
		}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var taken bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_type_schemes WHERE workspace_id=$1 AND lower(name)=lower($2))`, workspaceID, name).Scan(&taken); err != nil {
		return "", err
	}
	if taken {
		return "", fmt.Errorf("%w: the scheme name is used by another scheme", ErrIssueMetadataConflict)
	}
	var id string
	if err = tx.QueryRow(ctx, `INSERT INTO issue_type_schemes(workspace_id,name,description,default_issue_type_id) VALUES($1,$2,$3,NULLIF($4,'')) RETURNING id::text`,
		workspaceID, name, description, defaultID).Scan(&id); err != nil {
		return "", err
	}
	for i, typeID := range ids {
		if _, err = tx.Exec(ctx, `INSERT INTO issue_type_scheme_items(scheme_id,issue_type_id,position) VALUES($1::bigint,$2,$3)`, id, typeID, i); err != nil {
			return "", err
		}
	}
	return id, tx.Commit(ctx)
}

func (s *Store) UpdateIssueTypeScheme(ctx context.Context, workspaceID, schemeID string, name, description, defaultIssueType *string) error {
	scheme, err := s.issueTypeScheme(ctx, workspaceID, schemeID)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if name != nil {
		trimmed, err := validMetadataName(*name, 255)
		if err != nil {
			return err
		}
		var taken bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_type_schemes WHERE workspace_id=$1 AND lower(name)=lower($2) AND id::text<>$3)`, workspaceID, trimmed, scheme.ID).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return fmt.Errorf("%w: the scheme name is used by another scheme", ErrIssueMetadataConflict)
		}
		if _, err = tx.Exec(ctx, `UPDATE issue_type_schemes SET name=$3 WHERE workspace_id=$1 AND id::text=$2`, workspaceID, scheme.ID, trimmed); err != nil {
			return err
		}
	}
	if description != nil {
		if _, err = tx.Exec(ctx, `UPDATE issue_type_schemes SET description=$3 WHERE workspace_id=$1 AND id::text=$2`, workspaceID, scheme.ID, *description); err != nil {
			return err
		}
	}
	if defaultIssueType != nil {
		defaultID := ""
		if strings.TrimSpace(*defaultIssueType) != "" {
			t, err := s.IssueTypeInWorkspace(ctx, workspaceID, *defaultIssueType)
			if err != nil || !containsString(scheme.IssueTypeIDs, t.ID) {
				return fmt.Errorf("%w: the default issue type must be one of the scheme's issue types", ErrIssueMetadataValidation)
			}
			defaultID = t.ID
		}
		if _, err = tx.Exec(ctx, `UPDATE issue_type_schemes SET default_issue_type_id=NULLIF($3,'') WHERE workspace_id=$1 AND id::text=$2`, workspaceID, scheme.ID, defaultID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// DeleteIssueTypeScheme removes a scheme that no project uses. The default
// scheme is what unassigned projects fall back to, so it cannot be deleted.
func (s *Store) DeleteIssueTypeScheme(ctx context.Context, workspaceID, schemeID string) error {
	scheme, err := s.issueTypeScheme(ctx, workspaceID, schemeID)
	if err != nil {
		return err
	}
	if scheme.IsDefault {
		return fmt.Errorf("%w: the default issue type scheme cannot be deleted", ErrIssueMetadataValidation)
	}
	if len(scheme.ProjectIDs) > 0 {
		return fmt.Errorf("%w: the issue type scheme is associated with projects", ErrIssueMetadataValidation)
	}
	_, err = s.Pool.Exec(ctx, `DELETE FROM issue_type_schemes WHERE workspace_id=$1 AND id::text=$2`, workspaceID, scheme.ID)
	return err
}

// ProjectIssueTypeScheme is the scheme a project uses: its own, or the default.
func (s *Store) ProjectIssueTypeScheme(ctx context.Context, workspaceID, projectID string) (IssueTypeScheme, error) {
	all, err := s.IssueTypeSchemes(ctx, workspaceID)
	if err != nil {
		return IssueTypeScheme{}, err
	}
	for _, sc := range all {
		if containsString(sc.ProjectIDs, projectID) {
			return sc, nil
		}
	}
	for _, sc := range all {
		if sc.IsDefault {
			return sc, nil
		}
	}
	return IssueTypeScheme{}, ErrIssueMetadataNotFound
}

// AssignIssueTypeScheme sets a project's scheme. Every issue the project already
// has must keep a type the new scheme offers, or the assignment is refused.
func (s *Store) AssignIssueTypeScheme(ctx context.Context, workspaceID, schemeID, projectID string) error {
	scheme, err := s.issueTypeScheme(ctx, workspaceID, schemeID)
	if err != nil {
		return err
	}
	var exists bool
	if err = s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE workspace_id=$1 AND id=$2)`, workspaceID, projectID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: the project does not exist", ErrIssueMetadataNotFound)
	}
	var stranded int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM issues WHERE workspace_id=$1 AND project_id=$2 AND NOT (issuetype_id = ANY($3))`, workspaceID, projectID, scheme.IssueTypeIDs).Scan(&stranded); err != nil {
		return err
	}
	if stranded > 0 {
		return fmt.Errorf("%w: %d issues in the project use issue types the scheme does not include", ErrIssueMetadataValidation, stranded)
	}
	if scheme.IsDefault {
		_, err = s.Pool.Exec(ctx, `DELETE FROM project_issue_type_schemes WHERE workspace_id=$1 AND project_id=$2`, workspaceID, projectID)
		return err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO project_issue_type_schemes(project_id,workspace_id,scheme_id) VALUES($1,$2,$3::bigint)
		ON CONFLICT (project_id) DO UPDATE SET scheme_id=EXCLUDED.scheme_id`, projectID, workspaceID, scheme.ID)
	return err
}

// AddIssueTypesToScheme appends issue types. If any is already in the scheme,
// none is added.
func (s *Store) AddIssueTypesToScheme(ctx context.Context, workspaceID, schemeID string, issueTypes []string) error {
	scheme, err := s.issueTypeScheme(ctx, workspaceID, schemeID)
	if err != nil {
		return err
	}
	if len(issueTypes) == 0 {
		return fmt.Errorf("%w: issueTypeIds must name at least one issue type", ErrIssueMetadataValidation)
	}
	ids, err := s.resolveIssueTypeIDs(ctx, workspaceID, issueTypes)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if containsString(scheme.IssueTypeIDs, id) {
			return fmt.Errorf("%w: an issue type is already in the scheme", ErrIssueMetadataValidation)
		}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, id := range ids {
		if _, err = tx.Exec(ctx, `INSERT INTO issue_type_scheme_items(scheme_id,issue_type_id,position)
			VALUES($1::bigint,$2,(SELECT COALESCE(max(position),-1)+1 FROM issue_type_scheme_items WHERE scheme_id=$1::bigint))`, scheme.ID, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// MoveIssueTypesInScheme reorders issue types within a scheme.
func (s *Store) MoveIssueTypesInScheme(ctx context.Context, workspaceID, schemeID string, issueTypes []string, after, position string) error {
	scheme, err := s.issueTypeScheme(ctx, workspaceID, schemeID)
	if err != nil {
		return err
	}
	ids, err := s.resolveIssueTypeIDs(ctx, workspaceID, issueTypes)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !containsString(scheme.IssueTypeIDs, id) {
			return fmt.Errorf("%w: every issue type must belong to the scheme", ErrIssueMetadataValidation)
		}
	}
	afterID := ""
	if strings.TrimSpace(after) != "" {
		t, err := s.IssueTypeInWorkspace(ctx, workspaceID, after)
		if err != nil || !containsString(scheme.IssueTypeIDs, t.ID) {
			return fmt.Errorf("%w: after must be an issue type in the scheme", ErrIssueMetadataValidation)
		}
		afterID = t.ID
	}
	order, err := reorderList(scheme.IssueTypeIDs, ids, afterID, position)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for i, id := range order {
		if _, err = tx.Exec(ctx, `UPDATE issue_type_scheme_items SET position=$3 WHERE scheme_id=$1::bigint AND issue_type_id=$2`, scheme.ID, id, i); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// RemoveIssueTypeFromScheme takes an issue type out of a scheme. Jira refuses
// to remove a type issues still use, any type of the default scheme, and the
// last standard type, because a project must always offer one.
func (s *Store) RemoveIssueTypeFromScheme(ctx context.Context, workspaceID, schemeID, issueType string) error {
	scheme, err := s.issueTypeScheme(ctx, workspaceID, schemeID)
	if err != nil {
		return err
	}
	t, err := s.IssueTypeInWorkspace(ctx, workspaceID, issueType)
	if err != nil || !containsString(scheme.IssueTypeIDs, t.ID) {
		return fmt.Errorf("%w: the issue type is not in the issue type scheme", ErrIssueMetadataNotFound)
	}
	if scheme.IsDefault {
		return fmt.Errorf("%w: issue types cannot be removed from the default issue type scheme", ErrIssueMetadataValidation)
	}
	var used int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM issues WHERE workspace_id=$1 AND issuetype_id=$2 AND project_id = ANY($3)`, workspaceID, t.ID, scheme.ProjectIDs).Scan(&used); err != nil {
		return err
	}
	if used > 0 {
		return fmt.Errorf("%w: the issue type is used by issues", ErrIssueMetadataValidation)
	}
	if !t.Subtask {
		standard := 0
		all, err := s.IssueTypesForWorkspace(ctx, workspaceID)
		if err != nil {
			return err
		}
		for _, other := range all {
			if !other.Subtask && containsString(scheme.IssueTypeIDs, other.ID) {
				standard++
			}
		}
		if standard <= 1 {
			return fmt.Errorf("%w: the last standard issue type cannot be removed from a scheme", ErrIssueMetadataValidation)
		}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `DELETE FROM issue_type_scheme_items WHERE scheme_id=$1::bigint AND issue_type_id=$2`, scheme.ID, t.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE issue_type_schemes SET default_issue_type_id=NULL WHERE id::text=$1 AND default_issue_type_id=$2`, scheme.ID, t.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ProjectIssueTypes lists the issue types a project offers, from its scheme,
// optionally at one hierarchy level.
func (s *Store) ProjectIssueTypes(ctx context.Context, workspaceID, projectID string, level *int) ([]models.IssueType, error) {
	scheme, err := s.ProjectIssueTypeScheme(ctx, workspaceID, projectID)
	if err != nil {
		return nil, err
	}
	all, err := s.IssueTypesForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	byID := map[string]models.IssueType{}
	for _, t := range all {
		byID[t.ID] = t
	}
	out := []models.IssueType{}
	for _, id := range scheme.IssueTypeIDs {
		t, ok := byID[id]
		if !ok || (level != nil && t.HierarchyLevel != *level) {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// ---- Priority schemes ----

func (s *Store) PrioritySchemes(ctx context.Context, workspaceID string) ([]PriorityScheme, error) {
	rows, err := s.Pool.Query(ctx, `SELECT sc.id::text, sc.name, sc.description, sc.default_priority_id, sc.is_default,
		COALESCE((SELECT array_agg(i.priority_id ORDER BY i.position) FROM priority_scheme_items i
		  JOIN priorities p ON p.id=i.priority_id
		  LEFT JOIN issue_metadata_overrides o ON o.workspace_id=sc.workspace_id AND o.entity_type='priority' AND o.entity_id=p.id
		  WHERE i.scheme_id=sc.id AND NOT COALESCE(o.deleted,FALSE)), ARRAY[]::text[]),
		COALESCE((SELECT array_agg(pp.project_id ORDER BY pp.project_id) FROM project_priority_schemes pp WHERE pp.scheme_id=sc.id), ARRAY[]::text[])
		FROM priority_schemes sc WHERE sc.workspace_id=$1 ORDER BY sc.id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PriorityScheme{}
	for rows.Next() {
		var sc PriorityScheme
		if err = rows.Scan(&sc.ID, &sc.Name, &sc.Description, &sc.DefaultPriorityID, &sc.IsDefault, &sc.PriorityIDs, &sc.ProjectIDs); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func (s *Store) PrioritySchemeByID(ctx context.Context, workspaceID, id string) (PriorityScheme, error) {
	all, err := s.PrioritySchemes(ctx, workspaceID)
	if err != nil {
		return PriorityScheme{}, err
	}
	for _, sc := range all {
		if sc.ID == strings.TrimSpace(id) {
			return sc, nil
		}
	}
	return PriorityScheme{}, ErrIssueMetadataNotFound
}

func (s *Store) resolvePriorityIDs(ctx context.Context, workspaceID string, wire []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(wire))
	for _, raw := range wire {
		p, err := s.PriorityInWorkspace(ctx, workspaceID, raw)
		if err != nil {
			return nil, fmt.Errorf("%w: priority %s does not exist", ErrIssueMetadataValidation, raw)
		}
		if seen[p.ID] {
			return nil, fmt.Errorf("%w: priority ids must be unique", ErrIssueMetadataValidation)
		}
		seen[p.ID] = true
		out = append(out, p.ID)
	}
	return out, nil
}

// PrioritySchemeMapping moves the issues of one priority to another when a
// scheme change would leave them with a priority their project no longer offers.
type PrioritySchemeMapping struct {
	From string
	To   string
}

// PrioritySchemeInput is what a create or update may set.
type PrioritySchemeInput struct {
	Name            *string
	Description     *string
	DefaultPriority *string
	PriorityIDs     *[]string
	ProjectIDs      *[]string
	Mappings        []PrioritySchemeMapping
	// AddRemove carries the add and remove lists an update sends instead of
	// complete lists.
	AddRemove *PrioritySchemeAddRemove
}

// PrioritySchemeAddRemove names priorities and projects to add to, or remove
// from, a scheme's current lists.
type PrioritySchemeAddRemove struct {
	AddPriorities    []string
	RemovePriorities []string
	AddProjects      []string
	RemoveProjects   []string
}

// ProjectPriorityScheme is the scheme a project uses: its own, or the default.
func (s *Store) ProjectPriorityScheme(ctx context.Context, workspaceID, projectID string) (PriorityScheme, error) {
	all, err := s.PrioritySchemes(ctx, workspaceID)
	if err != nil {
		return PriorityScheme{}, err
	}
	for _, sc := range all {
		if containsString(sc.ProjectIDs, projectID) {
			return sc, nil
		}
	}
	for _, sc := range all {
		if sc.IsDefault {
			return sc, nil
		}
	}
	return PriorityScheme{}, ErrIssueMetadataNotFound
}

// PrioritiesNeedingMapping reports which priorities issues in the given projects
// use that the given priority list lacks. A change leaving those issues with a
// priority their project does not offer needs a mapping for each.
func (s *Store) PrioritiesNeedingMapping(ctx context.Context, workspaceID string, projectIDs, priorityIDs []string) ([]string, error) {
	if len(projectIDs) == 0 {
		return []string{}, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT priority_id FROM issues WHERE workspace_id=$1 AND project_id = ANY($2)
		AND priority_id IS NOT NULL AND NOT (priority_id = ANY($3)) ORDER BY priority_id`, workspaceID, projectIDs, priorityIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Store) validatePrioritySchemeChange(ctx context.Context, workspaceID string, priorityIDs, projectIDs []string, mappings []PrioritySchemeMapping) (map[string]string, error) {
	needing, err := s.PrioritiesNeedingMapping(ctx, workspaceID, projectIDs, priorityIDs)
	if err != nil {
		return nil, err
	}
	resolved := map[string]string{}
	for _, m := range mappings {
		from, err := s.PriorityInWorkspace(ctx, workspaceID, m.From)
		if err != nil {
			return nil, fmt.Errorf("%w: a mapping names a priority that does not exist", ErrIssueMetadataValidation)
		}
		to, err := s.PriorityInWorkspace(ctx, workspaceID, m.To)
		if err != nil || !containsString(priorityIDs, to.ID) {
			return nil, fmt.Errorf("%w: a mapping must move issues to a priority in the scheme", ErrIssueMetadataValidation)
		}
		resolved[from.ID] = to.ID
	}
	for _, id := range needing {
		if _, ok := resolved[id]; !ok {
			return nil, fmt.Errorf("%w: the priorities with IDs [%s] are used by issues in these projects and need to be mapped", ErrIssueMetadataValidation, id)
		}
	}
	return resolved, nil
}

func (s *Store) resolveProjectIDs(ctx context.Context, workspaceID string, wire []string) ([]string, error) {
	out := []string{}
	for _, raw := range wire {
		var id string
		err := s.Pool.QueryRow(ctx, `SELECT id FROM projects WHERE workspace_id=$1 AND (id=$2 OR jira_id::text=$2)`, workspaceID, strings.TrimSpace(raw)).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: project %s does not exist", ErrIssueMetadataValidation, raw)
		}
		if err != nil {
			return nil, err
		}
		if !containsString(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// CreatePriorityScheme creates a scheme and assigns it to any projects named.
func (s *Store) CreatePriorityScheme(ctx context.Context, workspaceID string, in PrioritySchemeInput) (string, error) {
	if in.Name == nil || in.DefaultPriority == nil || in.PriorityIDs == nil {
		return "", fmt.Errorf("%w: name, defaultPriorityId and priorityIds are required", ErrIssueMetadataValidation)
	}
	name, err := validMetadataName(*in.Name, 255)
	if err != nil {
		return "", err
	}
	priorityIDs, err := s.resolvePriorityIDs(ctx, workspaceID, *in.PriorityIDs)
	if err != nil {
		return "", err
	}
	if len(priorityIDs) == 0 {
		return "", fmt.Errorf("%w: priorityIds must name at least one priority", ErrIssueMetadataValidation)
	}
	def, err := s.PriorityInWorkspace(ctx, workspaceID, *in.DefaultPriority)
	if err != nil || !containsString(priorityIDs, def.ID) {
		return "", fmt.Errorf("%w: the default priority must be one of the scheme's priorities", ErrIssueMetadataValidation)
	}
	projectIDs := []string{}
	if in.ProjectIDs != nil {
		if projectIDs, err = s.resolveProjectIDs(ctx, workspaceID, *in.ProjectIDs); err != nil {
			return "", err
		}
	}
	mappings, err := s.validatePrioritySchemeChange(ctx, workspaceID, priorityIDs, projectIDs, in.Mappings)
	if err != nil {
		return "", err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var taken bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM priority_schemes WHERE workspace_id=$1 AND lower(name)=lower($2))`, workspaceID, name).Scan(&taken); err != nil {
		return "", err
	}
	if taken {
		return "", fmt.Errorf("%w: a priority scheme with this name already exists", ErrIssueMetadataValidation)
	}
	description := ""
	if in.Description != nil {
		description = *in.Description
	}
	var id string
	if err = tx.QueryRow(ctx, `INSERT INTO priority_schemes(workspace_id,name,description,default_priority_id) VALUES($1,$2,$3,$4) RETURNING id::text`,
		workspaceID, name, description, def.ID).Scan(&id); err != nil {
		return "", err
	}
	if err = writePrioritySchemeContents(ctx, tx, workspaceID, id, priorityIDs, projectIDs, mappings); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func writePrioritySchemeContents(ctx context.Context, tx pgx.Tx, workspaceID, schemeID string, priorityIDs, projectIDs []string, mappings map[string]string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM priority_scheme_items WHERE scheme_id=$1::bigint`, schemeID); err != nil {
		return err
	}
	for i, id := range priorityIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO priority_scheme_items(scheme_id,priority_id,position) VALUES($1::bigint,$2,$3)`, schemeID, id, i); err != nil {
			return err
		}
	}
	for _, projectID := range projectIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO project_priority_schemes(project_id,workspace_id,scheme_id) VALUES($1,$2,$3::bigint)
			ON CONFLICT (project_id) DO UPDATE SET scheme_id=EXCLUDED.scheme_id`, projectID, workspaceID, schemeID); err != nil {
			return err
		}
		for from, to := range mappings {
			if _, err := tx.Exec(ctx, `UPDATE issues SET priority_id=$4, updated_at=now() WHERE workspace_id=$1 AND project_id=$2 AND priority_id=$3`, workspaceID, projectID, from, to); err != nil {
				return err
			}
		}
	}
	return nil
}

// UpdatePriorityScheme changes a scheme's details, priorities and projects.
// Projects given are the scheme's complete list; a project dropped from it goes
// back to the default scheme.
func (s *Store) UpdatePriorityScheme(ctx context.Context, workspaceID, schemeID string, in PrioritySchemeInput) (PriorityScheme, error) {
	scheme, err := s.PrioritySchemeByID(ctx, workspaceID, schemeID)
	if err != nil {
		return scheme, err
	}
	if in.AddRemove != nil {
		priorities := append([]string{}, scheme.PriorityIDs...)
		if len(in.AddRemove.RemovePriorities) > 0 {
			removed, err := s.resolvePriorityIDs(ctx, workspaceID, in.AddRemove.RemovePriorities)
			if err != nil {
				return scheme, err
			}
			kept := []string{}
			for _, id := range priorities {
				if !containsString(removed, id) {
					kept = append(kept, id)
				}
			}
			priorities = kept
		}
		if len(in.AddRemove.AddPriorities) > 0 {
			added, err := s.resolvePriorityIDs(ctx, workspaceID, in.AddRemove.AddPriorities)
			if err != nil {
				return scheme, err
			}
			for _, id := range added {
				if !containsString(priorities, id) {
					priorities = append(priorities, id)
				}
			}
		}
		in.PriorityIDs = &priorities
		if len(in.AddRemove.AddProjects) > 0 || len(in.AddRemove.RemoveProjects) > 0 {
			projects := append([]string{}, scheme.ProjectIDs...)
			removed, err := s.resolveProjectIDs(ctx, workspaceID, in.AddRemove.RemoveProjects)
			if err != nil {
				return scheme, err
			}
			kept := []string{}
			for _, id := range projects {
				if !containsString(removed, id) {
					kept = append(kept, id)
				}
			}
			added, err := s.resolveProjectIDs(ctx, workspaceID, in.AddRemove.AddProjects)
			if err != nil {
				return scheme, err
			}
			for _, id := range added {
				if !containsString(kept, id) {
					kept = append(kept, id)
				}
			}
			in.ProjectIDs = &kept
		}
	}
	priorityIDs := scheme.PriorityIDs
	if in.PriorityIDs != nil {
		if priorityIDs, err = s.resolvePriorityIDs(ctx, workspaceID, *in.PriorityIDs); err != nil {
			return scheme, err
		}
		if len(priorityIDs) == 0 {
			return scheme, fmt.Errorf("%w: a priority scheme needs at least one priority", ErrIssueMetadataValidation)
		}
	}
	projectIDs := scheme.ProjectIDs
	if in.ProjectIDs != nil {
		if scheme.IsDefault {
			return scheme, fmt.Errorf("%w: projects use the default priority scheme by not having another", ErrIssueMetadataValidation)
		}
		if projectIDs, err = s.resolveProjectIDs(ctx, workspaceID, *in.ProjectIDs); err != nil {
			return scheme, err
		}
	}
	defaultID := scheme.DefaultPriorityID
	if in.DefaultPriority != nil {
		p, err := s.PriorityInWorkspace(ctx, workspaceID, *in.DefaultPriority)
		if err != nil {
			return scheme, fmt.Errorf("%w: the default priority does not exist", ErrIssueMetadataValidation)
		}
		defaultID = p.ID
	}
	if !containsString(priorityIDs, defaultID) {
		return scheme, fmt.Errorf("%w: the default priority must be one of the scheme's priorities", ErrIssueMetadataValidation)
	}
	affected := projectIDs
	if scheme.IsDefault {
		affected, err = s.projectsWithoutPriorityScheme(ctx, workspaceID)
		if err != nil {
			return scheme, err
		}
	}
	mappings, err := s.validatePrioritySchemeChange(ctx, workspaceID, priorityIDs, affected, in.Mappings)
	if err != nil {
		return scheme, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return scheme, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if in.Name != nil {
		name, err := validMetadataName(*in.Name, 255)
		if err != nil {
			return scheme, err
		}
		var taken bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM priority_schemes WHERE workspace_id=$1 AND lower(name)=lower($2) AND id::text<>$3)`, workspaceID, name, scheme.ID).Scan(&taken); err != nil {
			return scheme, err
		}
		if taken {
			return scheme, fmt.Errorf("%w: a priority scheme with this name already exists", ErrIssueMetadataValidation)
		}
		if _, err = tx.Exec(ctx, `UPDATE priority_schemes SET name=$3 WHERE workspace_id=$1 AND id::text=$2`, workspaceID, scheme.ID, name); err != nil {
			return scheme, err
		}
	}
	if in.Description != nil {
		if _, err = tx.Exec(ctx, `UPDATE priority_schemes SET description=$3 WHERE workspace_id=$1 AND id::text=$2`, workspaceID, scheme.ID, *in.Description); err != nil {
			return scheme, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE priority_schemes SET default_priority_id=$3 WHERE workspace_id=$1 AND id::text=$2`, workspaceID, scheme.ID, defaultID); err != nil {
		return scheme, err
	}
	if in.ProjectIDs != nil {
		if _, err = tx.Exec(ctx, `DELETE FROM project_priority_schemes WHERE workspace_id=$1 AND scheme_id::text=$2 AND NOT (project_id = ANY($3))`, workspaceID, scheme.ID, projectIDs); err != nil {
			return scheme, err
		}
	}
	writeProjects := projectIDs
	if scheme.IsDefault {
		writeProjects = []string{}
		for _, projectID := range affected {
			for from, to := range mappings {
				if _, err = tx.Exec(ctx, `UPDATE issues SET priority_id=$4, updated_at=now() WHERE workspace_id=$1 AND project_id=$2 AND priority_id=$3`, workspaceID, projectID, from, to); err != nil {
					return scheme, err
				}
			}
		}
	}
	if err = writePrioritySchemeContents(ctx, tx, workspaceID, scheme.ID, priorityIDs, writeProjects, mappings); err != nil {
		return scheme, err
	}
	if err = tx.Commit(ctx); err != nil {
		return scheme, err
	}
	return s.PrioritySchemeByID(ctx, workspaceID, scheme.ID)
}

func (s *Store) projectsWithoutPriorityScheme(ctx context.Context, workspaceID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT p.id FROM projects p WHERE p.workspace_id=$1
		AND NOT EXISTS (SELECT 1 FROM project_priority_schemes pp WHERE pp.project_id=p.id) ORDER BY p.id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// DeletePriorityScheme removes a scheme without projects. The default scheme
// is what unassigned projects use, so it stays.
func (s *Store) DeletePriorityScheme(ctx context.Context, workspaceID, schemeID string) error {
	scheme, err := s.PrioritySchemeByID(ctx, workspaceID, schemeID)
	if err != nil {
		return err
	}
	if scheme.IsDefault {
		return fmt.Errorf("%w: the default priority scheme cannot be deleted", ErrIssueMetadataValidation)
	}
	if len(scheme.ProjectIDs) > 0 {
		return fmt.Errorf("%w: projects must be removed from the priority scheme before it is deleted", ErrIssueMetadataValidation)
	}
	_, err = s.Pool.Exec(ctx, `DELETE FROM priority_schemes WHERE workspace_id=$1 AND id::text=$2`, workspaceID, scheme.ID)
	return err
}

// ---- shared ----

// reorderList moves ids to after one entry, or to the First or Last place.
func reorderList(current, ids []string, after, position string) ([]string, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: ids are required", ErrIssueMetadataValidation)
	}
	if (after == "") == (strings.TrimSpace(position) == "") {
		return nil, fmt.Errorf("%w: either after or position is required", ErrIssueMetadataValidation)
	}
	moving := map[string]bool{}
	for _, id := range ids {
		moving[id] = true
	}
	if after != "" && moving[after] {
		return nil, fmt.Errorf("%w: after must not be one of the moved issue types", ErrIssueMetadataValidation)
	}
	rest := []string{}
	for _, id := range current {
		if !moving[id] {
			rest = append(rest, id)
		}
	}
	out := []string{}
	switch {
	case after != "":
		for _, id := range rest {
			out = append(out, id)
			if id == after {
				out = append(out, ids...)
			}
		}
	case strings.EqualFold(position, "First"):
		out = append(append(out, ids...), rest...)
	case strings.EqualFold(position, "Last"):
		out = append(append(out, rest...), ids...)
	default:
		return nil, fmt.Errorf("%w: position is First or Last", ErrIssueMetadataValidation)
	}
	return out, nil
}
