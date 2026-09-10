package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

var (
	ErrIssueSecurityValidation   = errors.New("issue security scheme request is invalid")
	ErrIssueSecurityNotFound     = errors.New("issue security scheme, level, member, or project does not exist")
	ErrIssueSecurityConflict     = errors.New("issue security scheme is in use")
	ErrIssueSecurityTaskConflict = errors.New("an issue security migration is already running")
)

type SecurityLevelMemberInput struct {
	Type      string
	Parameter string
}

type SecurityLevelInput struct {
	Name        string
	Description string
	Default     bool
	Members     []SecurityLevelMemberInput
}

type SecurityLevelFilter struct {
	IDs         []string
	SchemeIDs   []string
	OnlyDefault bool
}

type SecurityMemberFilter struct {
	IDs       []string
	SchemeIDs []string
	LevelIDs  []string
}

type IssueSecuritySchemeMapping struct {
	SchemeID  string
	ProjectID string
}

type IssueSecurityLevelMapping struct {
	OldLevelID   string `json:"oldLevelId"`
	NewLevelID   string `json:"newLevelId"`
	OldUnsecured bool   `json:"oldUnsecured,omitempty"`
}

type assignIssueSecuritySchemeTaskPayload struct {
	ProjectID      string                      `json:"projectId"`
	TargetSchemeID string                      `json:"schemeId"`
	Mappings       []IssueSecurityLevelMapping `json:"oldToNewSecurityLevelMappings,omitempty"`
}

type removeIssueSecurityLevelTaskPayload struct {
	SchemeID    string `json:"schemeId"`
	LevelID     string `json:"levelId"`
	Replacement string `json:"replaceWith"`
}

const issueSecuritySchemeColumns = `scheme.id,COALESCE(scheme.workspace_id,''),scheme.name,scheme.description,
	COALESCE(scheme.default_level_id,''),scheme.levels`

func scanIssueSecurityScheme(row pgx.Row) (*models.SecurityScheme, error) {
	scheme := &models.SecurityScheme{}
	var levels []byte
	if err := row.Scan(&scheme.ID, &scheme.WorkspaceID, &scheme.Name, &scheme.Description, &scheme.DefaultLevelID, &levels); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrIssueSecurityNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal(levels, &scheme.Levels); err != nil {
		return nil, err
	}
	for index := range scheme.Levels {
		scheme.Levels[index].IsDefault = scheme.Levels[index].ID == scheme.DefaultLevelID
	}
	return scheme, nil
}

func scanSecurityLevelMember(row pgx.Row) (models.SecurityLevelMember, error) {
	member := models.SecurityLevelMember{}
	err := row.Scan(&member.ID, &member.SchemeID, &member.LevelID, &member.HolderType,
		&member.HolderParameter, &member.HolderValue, &member.Managed)
	return member, err
}

func loadSecurityLevelMembersTx(ctx context.Context, tx pgx.Tx, workspaceID, schemeID string) ([]models.SecurityLevelMember, error) {
	rows, err := tx.Query(ctx, `SELECT id,scheme_id,level_id,holder_type,COALESCE(holder_parameter,''),COALESCE(holder_value,''),managed
		FROM issue_security_level_members WHERE workspace_id=$1 AND scheme_id=$2 ORDER BY level_id,id`, workspaceID, schemeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []models.SecurityLevelMember{}
	for rows.Next() {
		member, scanErr := scanSecurityLevelMember(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		members = append(members, member)
	}
	return members, rows.Err()
}

func attachSecurityMembers(scheme *models.SecurityScheme, members []models.SecurityLevelMember) {
	for index := range scheme.Levels {
		scheme.Levels[index].Grants = []models.SecurityLevelMember{}
	}
	for _, member := range members {
		for index := range scheme.Levels {
			if scheme.Levels[index].ID == member.LevelID {
				scheme.Levels[index].Grants = append(scheme.Levels[index].Grants, member)
				break
			}
		}
	}
}

func issueSecuritySchemeTx(ctx context.Context, tx pgx.Tx, workspaceID, schemeID string, expand bool) (*models.SecurityScheme, error) {
	scheme, err := scanIssueSecurityScheme(tx.QueryRow(ctx, `SELECT `+issueSecuritySchemeColumns+` FROM security_schemes scheme WHERE scheme.workspace_id=$1 AND scheme.id=$2`, workspaceID, schemeID))
	if err != nil || !expand {
		return scheme, err
	}
	members, err := loadSecurityLevelMembersTx(ctx, tx, workspaceID, schemeID)
	if err == nil {
		attachSecurityMembers(scheme, members)
	}
	return scheme, err
}

func (s *Store) IssueSecurityScheme(ctx context.Context, workspaceID, schemeID string, expand bool) (*models.SecurityScheme, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return issueSecuritySchemeTx(ctx, tx, workspaceID, schemeID, expand)
}

func (s *Store) IssueSecuritySchemes(ctx context.Context, workspaceID string, expand bool) ([]*models.SecurityScheme, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+issueSecuritySchemeColumns+` FROM security_schemes scheme WHERE scheme.workspace_id=$1 ORDER BY lower(scheme.name),scheme.id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	schemes := []*models.SecurityScheme{}
	for rows.Next() {
		scheme, scanErr := scanIssueSecurityScheme(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		schemes = append(schemes, scheme)
	}
	if err = rows.Err(); err != nil || !expand {
		return schemes, err
	}
	for _, scheme := range schemes {
		members, memberErr := s.IssueSecurityLevelMembers(ctx, workspaceID, SecurityMemberFilter{SchemeIDs: []string{scheme.ID}})
		if memberErr != nil {
			return nil, memberErr
		}
		attachSecurityMembers(scheme, members)
	}
	return schemes, nil
}

func containsSecurityFilter(values []string, candidate string) bool {
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func (s *Store) IssueSecurityLevels(ctx context.Context, workspaceID string, filter SecurityLevelFilter) ([]models.SecurityLevel, error) {
	schemes, err := s.IssueSecuritySchemes(ctx, workspaceID, false)
	if err != nil {
		return nil, err
	}
	levels := []models.SecurityLevel{}
	for _, scheme := range schemes {
		if !containsSecurityFilter(filter.SchemeIDs, scheme.ID) {
			continue
		}
		for _, level := range scheme.Levels {
			if !containsSecurityFilter(filter.IDs, level.ID) || filter.OnlyDefault && !level.IsDefault {
				continue
			}
			levels = append(levels, level)
		}
	}
	sort.Slice(levels, func(i, j int) bool { return levels[i].ID < levels[j].ID })
	return levels, nil
}

func (s *Store) IssueSecurityLevel(ctx context.Context, workspaceID, levelID string) (*models.SecurityLevel, string, error) {
	schemes, err := s.IssueSecuritySchemes(ctx, workspaceID, true)
	if err != nil {
		return nil, "", err
	}
	for _, scheme := range schemes {
		for index := range scheme.Levels {
			if scheme.Levels[index].ID == levelID {
				return &scheme.Levels[index], scheme.ID, nil
			}
		}
	}
	return nil, "", ErrIssueSecurityNotFound
}

func (s *Store) IssueSecurityLevelMembers(ctx context.Context, workspaceID string, filter SecurityMemberFilter) ([]models.SecurityLevelMember, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,scheme_id,level_id,holder_type,COALESCE(holder_parameter,''),COALESCE(holder_value,''),managed
		FROM issue_security_level_members WHERE workspace_id=$1 ORDER BY id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []models.SecurityLevelMember{}
	for rows.Next() {
		member, scanErr := scanSecurityLevelMember(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		if !containsSecurityFilter(filter.IDs, fmt.Sprint(member.ID)) || !containsSecurityFilter(filter.SchemeIDs, member.SchemeID) || !containsSecurityFilter(filter.LevelIDs, member.LevelID) {
			continue
		}
		members = append(members, member)
	}
	return members, rows.Err()
}

func validateIssueSecurityName(name string, max int, kind string) error {
	if name == "" || name != strings.TrimSpace(name) || len(name) > max {
		return fmt.Errorf("%w: %s name must contain 1 to %d characters and cannot begin or end with whitespace", ErrIssueSecurityValidation, kind, max)
	}
	return nil
}

func validateSecurityMember(ctx context.Context, tx pgx.Tx, workspaceID string, member SecurityLevelMemberInput) (PermissionGrantInput, error) {
	allowed := map[string]bool{"applicationRole": true, "assignee": true, "group": true, "groupCustomField": true, "projectLead": true, "projectRole": true, "reporter": true, "user": true, "userCustomField": true}
	member.Type, member.Parameter = strings.TrimSpace(member.Type), strings.TrimSpace(member.Parameter)
	if !allowed[member.Type] {
		return PermissionGrantInput{}, fmt.Errorf("%w: issue security member type is invalid", ErrIssueSecurityValidation)
	}
	input, err := validatePermissionGrant(ctx, tx, workspaceID, PermissionGrantInput{Permission: "BROWSE_PROJECTS", HolderType: member.Type, HolderParameter: member.Parameter})
	if err != nil {
		return input, fmt.Errorf("%w: %v", ErrIssueSecurityValidation, err)
	}
	return input, nil
}

func insertSecurityMember(ctx context.Context, tx pgx.Tx, workspaceID, schemeID, levelID string, input SecurityLevelMemberInput) (models.SecurityLevelMember, error) {
	validated, err := validateSecurityMember(ctx, tx, workspaceID, input)
	if err != nil {
		return models.SecurityLevelMember{}, err
	}
	member, err := scanSecurityLevelMember(tx.QueryRow(ctx, `INSERT INTO issue_security_level_members(workspace_id,scheme_id,level_id,holder_type,holder_parameter,holder_value)
		VALUES($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,'')) RETURNING id,scheme_id,level_id,holder_type,COALESCE(holder_parameter,''),COALESCE(holder_value,''),managed`,
		workspaceID, schemeID, levelID, validated.HolderType, validated.HolderParameter, validated.HolderValue))
	if isUniqueViolation(err) {
		return member, fmt.Errorf("%w: issue security member already exists", ErrIssueSecurityConflict)
	}
	return member, err
}

func normalizeSecurityLevels(ctx context.Context, tx pgx.Tx, workspaceID string, inputs []SecurityLevelInput) ([]models.SecurityLevel, []struct {
	levelID string
	input   SecurityLevelMemberInput
}, string, error) {
	levels := make([]models.SecurityLevel, 0, len(inputs))
	members := []struct {
		levelID string
		input   SecurityLevelMemberInput
	}{}
	defaultID := ""
	seen := map[string]bool{}
	for _, input := range inputs {
		if err := validateIssueSecurityName(input.Name, 255, "level"); err != nil {
			return nil, nil, "", err
		}
		if len(input.Description) > 4000 {
			return nil, nil, "", fmt.Errorf("%w: level description accepts at most 4000 characters", ErrIssueSecurityValidation)
		}
		key := strings.ToLower(input.Name)
		if seen[key] {
			return nil, nil, "", fmt.Errorf("%w: level names must be unique", ErrIssueSecurityConflict)
		}
		seen[key] = true
		var levelID string
		if err := tx.QueryRow(ctx, `SELECT nextval('jira_issue_security_level_id')::text`).Scan(&levelID); err != nil {
			return nil, nil, "", err
		}
		levels = append(levels, models.SecurityLevel{ID: levelID, Name: input.Name, Description: input.Description, IsDefault: input.Default})
		if input.Default {
			if defaultID != "" {
				return nil, nil, "", fmt.Errorf("%w: only one default level is allowed", ErrIssueSecurityValidation)
			}
			defaultID = levelID
		}
		for _, member := range input.Members {
			if _, err := validateSecurityMember(ctx, tx, workspaceID, member); err != nil {
				return nil, nil, "", err
			}
			members = append(members, struct {
				levelID string
				input   SecurityLevelMemberInput
			}{levelID, member})
		}
	}
	return levels, members, defaultID, nil
}

func (s *Store) CreateIssueSecurityScheme(ctx context.Context, workspaceID, actorID, name, description string, inputs []SecurityLevelInput) (*models.SecurityScheme, error) {
	if err := validateIssueSecurityName(name, 60, "scheme"); err != nil {
		return nil, err
	}
	if len(description) > 255 {
		return nil, fmt.Errorf("%w: scheme description accepts at most 255 characters", ErrIssueSecurityValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	levels, members, defaultID, err := normalizeSecurityLevels(ctx, tx, workspaceID, inputs)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(levels)
	if err != nil {
		return nil, err
	}
	scheme, err := scanIssueSecurityScheme(tx.QueryRow(ctx, `INSERT INTO security_schemes(id,workspace_id,name,description,default_level_id,levels)
		VALUES(nextval('jira_issue_security_scheme_id')::text,$1,$2,$3,NULLIF($4,''),$5)
		RETURNING id,workspace_id,name,description,COALESCE(default_level_id,''),levels`, workspaceID, name, description, defaultID, encoded))
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: a scheme with this name already exists", ErrIssueSecurityConflict)
	}
	if err != nil {
		return nil, err
	}
	for _, member := range members {
		if _, err = insertSecurityMember(ctx, tx, workspaceID, scheme.ID, member.levelID, member.input); err != nil {
			return nil, err
		}
	}
	scheme, err = issueSecuritySchemeTx(ctx, tx, workspaceID, scheme.ID, true)
	if err == nil {
		err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "issue_security_scheme", scheme.ID, models.OpUpsert, scheme)
	}
	if err != nil {
		return nil, err
	}
	return scheme, tx.Commit(ctx)
}

func (s *Store) UpdateIssueSecurityScheme(ctx context.Context, workspaceID, actorID, schemeID string, name, description *string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	scheme, err := issueSecuritySchemeTx(ctx, tx, workspaceID, schemeID, true)
	if err != nil {
		return err
	}
	if name != nil {
		if err = validateIssueSecurityName(*name, 60, "scheme"); err != nil {
			return err
		}
		scheme.Name = *name
	}
	if description != nil {
		if len(*description) > 255 {
			return fmt.Errorf("%w: scheme description accepts at most 255 characters", ErrIssueSecurityValidation)
		}
		scheme.Description = *description
	}
	_, err = tx.Exec(ctx, `UPDATE security_schemes SET name=$3,description=$4,updated_at=now() WHERE workspace_id=$1 AND id=$2`, workspaceID, schemeID, scheme.Name, scheme.Description)
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: a scheme with this name already exists", ErrIssueSecurityConflict)
	}
	if err == nil {
		err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "issue_security_scheme", schemeID, models.OpUpsert, scheme)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func securityLevelIndex(levels []models.SecurityLevel, levelID string) int {
	for index := range levels {
		if levels[index].ID == levelID {
			return index
		}
	}
	return -1
}

func writeSecurityLevels(ctx context.Context, tx pgx.Tx, workspaceID, schemeID string, levels []models.SecurityLevel, defaultID string) error {
	encoded, err := json.Marshal(levels)
	if err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE security_schemes SET levels=$3,default_level_id=NULLIF($4,''),updated_at=now() WHERE workspace_id=$1 AND id=$2`, workspaceID, schemeID, encoded, defaultID)
	if err == nil && command.RowsAffected() != 1 {
		err = ErrIssueSecurityNotFound
	}
	return err
}

func (s *Store) AddIssueSecurityLevels(ctx context.Context, workspaceID, actorID, schemeID string, inputs []SecurityLevelInput) error {
	if len(inputs) == 0 {
		return fmt.Errorf("%w: at least one level is required", ErrIssueSecurityValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	scheme, err := issueSecuritySchemeTx(ctx, tx, workspaceID, schemeID, true)
	if err != nil {
		return err
	}
	newLevels, members, newDefault, err := normalizeSecurityLevels(ctx, tx, workspaceID, inputs)
	if err != nil {
		return err
	}
	names := map[string]bool{}
	for _, level := range scheme.Levels {
		names[strings.ToLower(level.Name)] = true
	}
	for _, level := range newLevels {
		if names[strings.ToLower(level.Name)] {
			return fmt.Errorf("%w: level names must be unique", ErrIssueSecurityConflict)
		}
	}
	if newDefault != "" {
		scheme.DefaultLevelID = newDefault
	}
	scheme.Levels = append(scheme.Levels, newLevels...)
	if err = writeSecurityLevels(ctx, tx, workspaceID, schemeID, scheme.Levels, scheme.DefaultLevelID); err != nil {
		return err
	}
	for _, member := range members {
		if _, err = insertSecurityMember(ctx, tx, workspaceID, schemeID, member.levelID, member.input); err != nil {
			return err
		}
	}
	scheme, err = issueSecuritySchemeTx(ctx, tx, workspaceID, schemeID, true)
	if err == nil {
		err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "issue_security_scheme", schemeID, models.OpUpsert, scheme)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) UpdateIssueSecurityLevel(ctx context.Context, workspaceID, actorID, schemeID, levelID string, name, description *string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	scheme, err := issueSecuritySchemeTx(ctx, tx, workspaceID, schemeID, true)
	if err != nil {
		return err
	}
	index := securityLevelIndex(scheme.Levels, levelID)
	if index < 0 {
		return ErrIssueSecurityNotFound
	}
	if name != nil {
		if err = validateIssueSecurityName(*name, 60, "level"); err != nil {
			return err
		}
		for i, level := range scheme.Levels {
			if i != index && strings.EqualFold(level.Name, *name) {
				return fmt.Errorf("%w: level names must be unique", ErrIssueSecurityConflict)
			}
		}
		scheme.Levels[index].Name = *name
	}
	if description != nil {
		if len(*description) > 255 {
			return fmt.Errorf("%w: level description accepts at most 255 characters", ErrIssueSecurityValidation)
		}
		scheme.Levels[index].Description = *description
	}
	if err = writeSecurityLevels(ctx, tx, workspaceID, schemeID, scheme.Levels, scheme.DefaultLevelID); err == nil {
		err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "issue_security_level", levelID, models.OpUpsert, scheme.Levels[index])
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SetIssueSecurityDefaults(ctx context.Context, workspaceID, actorID string, defaults map[string]string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	for schemeID, levelID := range defaults {
		scheme, loadErr := issueSecuritySchemeTx(ctx, tx, workspaceID, schemeID, false)
		if loadErr != nil {
			return loadErr
		}
		if levelID != "" && securityLevelIndex(scheme.Levels, levelID) < 0 {
			return ErrIssueSecurityNotFound
		}
		if _, err = tx.Exec(ctx, `UPDATE security_schemes SET default_level_id=NULLIF($3,''),updated_at=now() WHERE workspace_id=$1 AND id=$2`, workspaceID, schemeID, levelID); err != nil {
			return err
		}
		if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "issue_security_default", schemeID, models.OpUpsert, map[string]any{"issueSecuritySchemeId": schemeID, "defaultLevelId": levelID}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) AddIssueSecurityMembers(ctx context.Context, workspaceID, actorID, schemeID, levelID string, inputs []SecurityLevelMemberInput) error {
	if len(inputs) == 0 {
		return fmt.Errorf("%w: at least one member is required", ErrIssueSecurityValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	scheme, err := issueSecuritySchemeTx(ctx, tx, workspaceID, schemeID, false)
	if err != nil {
		return err
	}
	if securityLevelIndex(scheme.Levels, levelID) < 0 {
		return ErrIssueSecurityNotFound
	}
	for _, input := range inputs {
		member, insertErr := insertSecurityMember(ctx, tx, workspaceID, schemeID, levelID, input)
		if insertErr != nil {
			return insertErr
		}
		if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "issue_security_member", fmt.Sprint(member.ID), models.OpUpsert, member); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteIssueSecurityMember(ctx context.Context, workspaceID, actorID, schemeID, levelID string, memberID int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	member, err := scanSecurityLevelMember(tx.QueryRow(ctx, `DELETE FROM issue_security_level_members WHERE workspace_id=$1 AND scheme_id=$2 AND level_id=$3 AND id=$4 AND NOT managed
		RETURNING id,scheme_id,level_id,holder_type,COALESCE(holder_parameter,''),COALESCE(holder_value,''),managed`, workspaceID, schemeID, levelID, memberID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrIssueSecurityNotFound
	}
	if err == nil {
		err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "issue_security_member", fmt.Sprint(memberID), models.OpDelete, member)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteIssueSecurityScheme(ctx context.Context, workspaceID, actorID, schemeID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	scheme, err := issueSecuritySchemeTx(ctx, tx, workspaceID, schemeID, true)
	if err != nil {
		return err
	}
	var projects int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM projects WHERE workspace_id=$1 AND security_scheme_id=$2`, workspaceID, schemeID).Scan(&projects); err != nil {
		return err
	}
	if projects > 0 {
		return fmt.Errorf("%w: reassign projects before deleting this scheme", ErrIssueSecurityConflict)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM security_schemes WHERE workspace_id=$1 AND id=$2`, workspaceID, schemeID); err == nil {
		err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "issue_security_scheme", schemeID, models.OpDelete, scheme)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) IssueSecuritySchemeMappings(ctx context.Context, workspaceID string) ([]IssueSecuritySchemeMapping, error) {
	rows, err := s.Pool.Query(ctx, `SELECT security_scheme_id,id FROM projects WHERE workspace_id=$1 AND security_scheme_id IS NOT NULL ORDER BY security_scheme_id,id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	mappings := []IssueSecuritySchemeMapping{}
	for rows.Next() {
		var mapping IssueSecuritySchemeMapping
		if err = rows.Scan(&mapping.SchemeID, &mapping.ProjectID); err != nil {
			return nil, err
		}
		mappings = append(mappings, mapping)
	}
	return mappings, rows.Err()
}

func (s *Store) AssignedIssueSecurityScheme(ctx context.Context, workspaceID, projectIDOrKey string, expand bool) (*models.SecurityScheme, *models.Project, error) {
	project, err := s.ProjectByIDOrKey(ctx, workspaceID, projectIDOrKey)
	if err != nil {
		return nil, nil, ErrIssueSecurityNotFound
	}
	if project.SecuritySchemeID == "" {
		return nil, project, nil
	}
	scheme, err := s.IssueSecurityScheme(ctx, workspaceID, project.SecuritySchemeID, expand)
	return scheme, project, err
}

func (s *Store) CanUseIssueSecurityLevel(ctx context.Context, workspaceID, projectID, issueID, userID, levelID string) (bool, error) {
	var allowed bool
	err := s.Pool.QueryRow(ctx, `SELECT jira_issue_security_visible($1,$2,NULLIF($3,''),$4,NULLIF($5,''))`, workspaceID, projectID, issueID, userID, levelID).Scan(&allowed)
	return allowed, err
}

func (s *Store) EnqueueAssignIssueSecurityScheme(ctx context.Context, workspaceID, actorID, projectID, targetSchemeID string, mappings []IssueSecurityLevelMapping) (APITask, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return APITask{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return APITask{}, err
	}
	var currentScheme string
	if err = tx.QueryRow(ctx, `SELECT COALESCE(security_scheme_id,'') FROM projects WHERE workspace_id=$1 AND id=$2 AND lifecycle_state='ACTIVE' FOR UPDATE`, workspaceID, projectID).Scan(&currentScheme); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return APITask{}, ErrIssueSecurityNotFound
		}
		return APITask{}, err
	}
	var target *models.SecurityScheme
	if targetSchemeID != "" {
		target, err = issueSecuritySchemeTx(ctx, tx, workspaceID, targetSchemeID, false)
		if err != nil {
			return APITask{}, err
		}
	}
	targetLevels := map[string]bool{}
	if target != nil {
		for _, level := range target.Levels {
			targetLevels[level.ID] = true
		}
	}
	provided := map[string]string{}
	providedUnsecured := false
	for _, mapping := range mappings {
		if mapping.OldLevelID == "" && !mapping.OldUnsecured {
			return APITask{}, fmt.Errorf("%w: old level ID is required", ErrIssueSecurityValidation)
		}
		if mapping.OldUnsecured {
			if providedUnsecured {
				return APITask{}, fmt.Errorf("%w: unsecured issues can be mapped once", ErrIssueSecurityValidation)
			}
			providedUnsecured = true
		}
		if _, duplicate := provided[mapping.OldLevelID]; duplicate {
			return APITask{}, fmt.Errorf("%w: each old level can be mapped once", ErrIssueSecurityValidation)
		}
		if mapping.NewLevelID != "" && !targetLevels[mapping.NewLevelID] {
			return APITask{}, fmt.Errorf("%w: replacement level does not belong to the target scheme", ErrIssueSecurityValidation)
		}
		if !mapping.OldUnsecured {
			provided[mapping.OldLevelID] = mapping.NewLevelID
		}
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT security_level_id FROM issues WHERE project_id=$1 AND security_level_id IS NOT NULL`, projectID)
	if err != nil {
		return APITask{}, err
	}
	for rows.Next() {
		var levelID string
		if err = rows.Scan(&levelID); err != nil {
			rows.Close()
			return APITask{}, err
		}
		if _, ok := provided[levelID]; !ok && (currentScheme != targetSchemeID || !targetLevels[levelID]) {
			rows.Close()
			return APITask{}, fmt.Errorf("%w: map every security level currently used by this project", ErrIssueSecurityValidation)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return APITask{}, err
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM api_tasks WHERE workspace_id=$1 AND kind=$2 AND status IN ('ENQUEUED','RUNNING') AND payload->>'projectId'=$3)`, workspaceID, apiTaskAssignSecurityScheme, projectID).Scan(&active); err != nil {
		return APITask{}, err
	}
	if active {
		return APITask{}, ErrIssueSecurityTaskConflict
	}
	task, err := queuedAPITask(workspaceID, actorID, "Associate issue security scheme with project", apiTaskAssignSecurityScheme, assignIssueSecuritySchemeTaskPayload{ProjectID: projectID, TargetSchemeID: targetSchemeID, Mappings: mappings})
	if err != nil {
		return APITask{}, err
	}
	if err = insertAPITask(ctx, tx, task); err != nil {
		return APITask{}, err
	}
	return task, tx.Commit(ctx)
}

func (s *Store) EnqueueRemoveIssueSecurityLevel(ctx context.Context, workspaceID, actorID, schemeID, levelID, replacement string) (APITask, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return APITask{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return APITask{}, err
	}
	scheme, err := issueSecuritySchemeTx(ctx, tx, workspaceID, schemeID, false)
	if err != nil {
		return APITask{}, err
	}
	if securityLevelIndex(scheme.Levels, levelID) < 0 {
		return APITask{}, ErrIssueSecurityNotFound
	}
	if replacement == levelID {
		return APITask{}, fmt.Errorf("%w: replacement must be a different level", ErrIssueSecurityValidation)
	}
	if replacement != "" && securityLevelIndex(scheme.Levels, replacement) < 0 {
		return APITask{}, ErrIssueSecurityNotFound
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM api_tasks WHERE workspace_id=$1 AND kind=$2 AND status IN ('ENQUEUED','RUNNING') AND payload->>'schemeId'=$3 AND payload->>'levelId'=$4)`, workspaceID, apiTaskRemoveSecurityLevel, schemeID, levelID).Scan(&active); err != nil {
		return APITask{}, err
	}
	if active {
		return APITask{}, ErrIssueSecurityTaskConflict
	}
	task, err := queuedAPITask(workspaceID, actorID, "Remove issue security level", apiTaskRemoveSecurityLevel, removeIssueSecurityLevelTaskPayload{SchemeID: schemeID, LevelID: levelID, Replacement: replacement})
	if err != nil {
		return APITask{}, err
	}
	if err = insertAPITask(ctx, tx, task); err != nil {
		return APITask{}, err
	}
	return task, tx.Commit(ctx)
}

func (s *Store) executeAssignIssueSecuritySchemeTask(ctx context.Context, task APITask) error {
	var payload assignIssueSecuritySchemeTaskPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("decode issue security assignment: %w", err)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var current string
	if err = tx.QueryRow(ctx, `SELECT COALESCE(security_scheme_id,'') FROM projects WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, task.WorkspaceID, payload.ProjectID).Scan(&current); err != nil {
		return err
	}
	if payload.TargetSchemeID != "" {
		if _, err = issueSecuritySchemeTx(ctx, tx, task.WorkspaceID, payload.TargetSchemeID, false); err != nil {
			return err
		}
	}
	for _, mapping := range payload.Mappings {
		if _, err = tx.Exec(ctx, `UPDATE issues SET security_level_id=NULLIF($3,''),updated_at=now()
			WHERE project_id=$1 AND (security_level_id=$2 OR ($4 AND security_level_id IS NULL))`, payload.ProjectID, mapping.OldLevelID, mapping.NewLevelID, mapping.OldUnsecured); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE projects SET security_scheme_id=NULLIF($3,'') WHERE workspace_id=$1 AND id=$2`, task.WorkspaceID, payload.ProjectID, payload.TargetSchemeID); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, task.WorkspaceID, task.SubmittedBy, "project_issue_security_scheme", payload.ProjectID, models.OpUpsert, map[string]any{"projectId": payload.ProjectID, "oldSchemeId": current, "issueSecuritySchemeId": payload.TargetSchemeID, "mappings": payload.Mappings}); err != nil {
		return err
	}
	if err = completeAPITask(ctx, tx, task.WorkspaceID, task.ID, "Issue security scheme associated.", map[string]any{"projectId": payload.ProjectID, "issueSecuritySchemeId": payload.TargetSchemeID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) executeRemoveIssueSecurityLevelTask(ctx context.Context, task APITask) error {
	var payload removeIssueSecurityLevelTaskPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("decode issue security level removal: %w", err)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	scheme, err := issueSecuritySchemeTx(ctx, tx, task.WorkspaceID, payload.SchemeID, true)
	if err != nil {
		return err
	}
	index := securityLevelIndex(scheme.Levels, payload.LevelID)
	if index < 0 {
		return ErrIssueSecurityNotFound
	}
	removed := scheme.Levels[index]
	if payload.Replacement != "" && securityLevelIndex(scheme.Levels, payload.Replacement) < 0 {
		return ErrIssueSecurityNotFound
	}
	if _, err = tx.Exec(ctx, `UPDATE issues issue SET security_level_id=NULLIF($4,''),updated_at=now() FROM projects project
		WHERE issue.project_id=project.id AND project.workspace_id=$1 AND project.security_scheme_id=$2 AND issue.security_level_id=$3`, task.WorkspaceID, payload.SchemeID, payload.LevelID, payload.Replacement); err != nil {
		return err
	}
	scheme.Levels = append(scheme.Levels[:index], scheme.Levels[index+1:]...)
	if scheme.DefaultLevelID == payload.LevelID {
		scheme.DefaultLevelID = payload.Replacement
	}
	if err = writeSecurityLevels(ctx, tx, task.WorkspaceID, payload.SchemeID, scheme.Levels, scheme.DefaultLevelID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM issue_security_level_members WHERE workspace_id=$1 AND scheme_id=$2 AND level_id=$3`, task.WorkspaceID, payload.SchemeID, payload.LevelID); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, task.WorkspaceID, task.SubmittedBy, "issue_security_level", payload.LevelID, models.OpDelete, map[string]any{"level": removed, "replaceWith": payload.Replacement}); err != nil {
		return err
	}
	if err = completeAPITask(ctx, tx, task.WorkspaceID, task.ID, "Issue security level removed.", map[string]any{"schemeId": payload.SchemeID, "levelId": payload.LevelID, "replaceWith": payload.Replacement}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
