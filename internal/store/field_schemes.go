package store

import (
	"context"
	"fmt"
	"strconv"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// A field association scheme is this product's field configuration scheme seen
// through Jira's newer API: the same row, the same projects, and the same
// per-field rules. Serving one model through both APIs is what stops them
// disagreeing about which fields a project's forms show.

// FieldScheme is one field association scheme with the counts its listing
// reports.
type FieldScheme struct {
	ID          string
	Name        string
	Description string
	IsDefault   bool
	FieldsCount int
}

// FieldSchemeParameters are the rules a scheme applies to one field.
type FieldSchemeParameters struct {
	IsRequired  bool
	Description string
	WorkTypeID  string
}

// FieldSchemeField is a field's association with a scheme: the rules that apply
// by default, the work types they are overridden for, and the work types the
// field is restricted to when it does not reach all of them.
type FieldSchemeField struct {
	FieldID               string
	Parameters            FieldSchemeParameters
	WorkTypeParameters    []FieldSchemeParameters
	RestrictedToWorkTypes []string
}

// FieldSchemeWriteResult is one entry of the per-item answer Jira's bulk field
// scheme writes return, so one bad item does not fail the whole request.
type FieldSchemeWriteResult struct {
	FieldID     string
	ProjectID   string
	SchemeID    string
	WorkTypeIDs []string
	Success     bool
	Error       string
}

func parseSchemeID(schemeID string) (int64, error) {
	id, err := strconv.ParseInt(schemeID, 10, 64)
	if err != nil {
		return 0, ErrFieldConfigNotFound
	}
	return id, nil
}

// FieldSchemes lists the workspace's schemes, optionally narrowed to the ones
// the given projects use or to a name or description match.
func (s *Store) FieldSchemes(ctx context.Context, workspaceID string, projectIDs []string, query string) ([]FieldScheme, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT fcs.id::text, fcs.name, fcs.description, fcs.is_default,
			(SELECT count(DISTINCT item.field_id)
			 FROM field_configuration_scheme_items mapping
			 JOIN field_configuration_items item ON item.configuration_id=mapping.configuration_id
			 WHERE mapping.scheme_id=fcs.id AND NOT item.is_hidden)
		FROM field_configuration_schemes fcs
		WHERE fcs.workspace_id=$1
		  AND ($2::text[] IS NULL OR EXISTS (
			SELECT 1 FROM project_field_configuration_schemes p
			WHERE p.scheme_id=fcs.id AND p.project_id = ANY($2)))
		  AND ($3='' OR fcs.name ILIKE '%' || $3 || '%' OR fcs.description ILIKE '%' || $3 || '%')
		ORDER BY fcs.id`, workspaceID, nullableIDs(projectIDs), query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	schemes := []FieldScheme{}
	for rows.Next() {
		var scheme FieldScheme
		if err = rows.Scan(&scheme.ID, &scheme.Name, &scheme.Description, &scheme.IsDefault, &scheme.FieldsCount); err != nil {
			return nil, err
		}
		schemes = append(schemes, scheme)
	}
	return schemes, rows.Err()
}

// FieldSchemeByID reads one scheme, reporting ErrFieldConfigNotFound when it is
// not this workspace's.
func (s *Store) FieldSchemeByID(ctx context.Context, workspaceID, schemeID string) (*FieldScheme, error) {
	id, err := parseSchemeID(schemeID)
	if err != nil {
		return nil, err
	}
	schemes, err := s.FieldSchemes(ctx, workspaceID, nil, "")
	if err != nil {
		return nil, err
	}
	for i := range schemes {
		if schemes[i].ID == strconv.FormatInt(id, 10) {
			return &schemes[i], nil
		}
	}
	return nil, ErrFieldConfigNotFound
}

// FieldSchemeFields reports the fields a scheme associates, with the rules that
// apply to each and the work types they are restricted to.
func (s *Store) FieldSchemeFields(ctx context.Context, workspaceID, schemeID string, fieldIDs []string) ([]FieldSchemeField, error) {
	id, err := parseSchemeID(schemeID)
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT item.field_id, mapping.issue_type_id, item.is_required, item.description
		FROM field_configuration_scheme_items mapping
		JOIN field_configuration_items item ON item.configuration_id=mapping.configuration_id
		WHERE mapping.workspace_id=$1 AND mapping.scheme_id=$2 AND NOT item.is_hidden
		  AND ($3::text[] IS NULL OR item.field_id = ANY($3))
		ORDER BY item.field_id, mapping.issue_type_id`, workspaceID, id, nullableIDs(fieldIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type entry struct {
		byWorkType map[string]FieldSchemeParameters
		order      []string
	}
	collected := map[string]*entry{}
	order := []string{}
	for rows.Next() {
		var fieldID, workTypeID string
		var parameters FieldSchemeParameters
		if err = rows.Scan(&fieldID, &workTypeID, &parameters.IsRequired, &parameters.Description); err != nil {
			return nil, err
		}
		parameters.WorkTypeID = workTypeID
		if collected[fieldID] == nil {
			collected[fieldID] = &entry{byWorkType: map[string]FieldSchemeParameters{}}
			order = append(order, fieldID)
		}
		collected[fieldID].byWorkType[workTypeID] = parameters
		collected[fieldID].order = append(collected[fieldID].order, workTypeID)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	workTypes, err := s.workTypeIDs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]FieldSchemeField, 0, len(order))
	for _, fieldID := range order {
		found := collected[fieldID]
		field := FieldSchemeField{FieldID: fieldID, RestrictedToWorkTypes: []string{}, WorkTypeParameters: []FieldSchemeParameters{}}
		// The scheme's fallback mapping carries the parameters that apply when
		// a work type has no entry of its own.
		if fallback, ok := found.byWorkType[DefaultIssueTypeMapping]; ok {
			field.Parameters = FieldSchemeParameters{IsRequired: fallback.IsRequired, Description: fallback.Description}
		}

		fallback, hasFallback := found.byWorkType[DefaultIssueTypeMapping]
		for _, workTypeID := range found.order {
			if workTypeID == DefaultIssueTypeMapping {
				continue
			}
			parameters := found.byWorkType[workTypeID]
			// A work type whose rules match the fallback is not an override;
			// reporting it as one would read as a difference that is not there.
			if !hasFallback || parameters.IsRequired != fallback.IsRequired || parameters.Description != fallback.Description {
				field.WorkTypeParameters = append(field.WorkTypeParameters, parameters)
			}
			field.RestrictedToWorkTypes = append(field.RestrictedToWorkTypes, workTypeID)
		}
		// A field reached through the fallback is not restricted at all, and a
		// field listed for every work type individually is not restricted
		// either: the restriction only means something when it is a subset.
		if hasFallback || len(field.RestrictedToWorkTypes) == len(workTypes) {
			field.RestrictedToWorkTypes = []string{}
		}
		out = append(out, field)
	}
	return out, nil
}

func (s *Store) workTypeIDs(ctx context.Context) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id FROM issue_types ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// schemeOwnedConfiguration returns a configuration the scheme can write to for
// one work type, cloning a shared one first. A write through one scheme must
// never change a configuration another scheme is using, which is what would
// otherwise happen: a new scheme points every work type at the workspace
// default.
func schemeOwnedConfiguration(ctx context.Context, tx pgx.Tx, workspaceID string, schemeID int64, workTypeID string) (int64, error) {
	var configurationID int64
	err := tx.QueryRow(ctx, `SELECT configuration_id FROM field_configuration_scheme_items
		WHERE workspace_id=$1 AND scheme_id=$2 AND issue_type_id=$3`, workspaceID, schemeID, workTypeID).Scan(&configurationID)
	if err != nil && err != pgx.ErrNoRows {
		return 0, err
	}
	// A work type with no mapping of its own resolves through the scheme's
	// fallback. Writing a rule for it therefore has to give it its own
	// configuration first, or the write would land on the fallback and reach
	// every other work type as well.
	needsOwn := err == pgx.ErrNoRows
	if needsOwn {
		if err = tx.QueryRow(ctx, `SELECT configuration_id FROM field_configuration_scheme_items
			WHERE workspace_id=$1 AND scheme_id=$2 AND issue_type_id=$3`,
			workspaceID, schemeID, DefaultIssueTypeMapping).Scan(&configurationID); err != nil {
			return 0, err
		}
	}
	shared := needsOwn
	if !shared {
		if err = tx.QueryRow(ctx, `SELECT (SELECT count(DISTINCT scheme_id) FROM field_configuration_scheme_items
				WHERE configuration_id=$1) > 1
			OR EXISTS(SELECT 1 FROM field_configurations WHERE id=$1 AND is_default)
			OR (SELECT count(*) FROM field_configuration_scheme_items
				WHERE scheme_id=$2 AND configuration_id=$1) > 1`, configurationID, schemeID).Scan(&shared); err != nil {
			return 0, err
		}
	}
	if !shared {
		return configurationID, nil
	}
	var clonedID int64
	if err = tx.QueryRow(ctx, `INSERT INTO field_configurations(workspace_id,name,description)
		SELECT $1, name || ' (' || $3 || ')', description FROM field_configurations WHERE id=$2
		RETURNING id`, workspaceID, configurationID, schemeStamp(schemeID, workTypeID)).Scan(&clonedID); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO field_configuration_items(workspace_id,configuration_id,field_id,is_required,is_hidden,description)
		SELECT $1,$2,field_id,is_required,is_hidden,description FROM field_configuration_items WHERE configuration_id=$3`,
		workspaceID, clonedID, configurationID); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO field_configuration_scheme_items(workspace_id,scheme_id,issue_type_id,configuration_id)
		VALUES($1,$2,$3,$4)
		ON CONFLICT(scheme_id,issue_type_id) DO UPDATE SET configuration_id=EXCLUDED.configuration_id`,
		workspaceID, schemeID, workTypeID, clonedID); err != nil {
		return 0, err
	}
	return clonedID, nil
}

func schemeStamp(schemeID int64, workTypeID string) string {
	return "scheme " + strconv.FormatInt(schemeID, 10) + ", " + workTypeID
}

// SetFieldSchemeFields associates fields with schemes, optionally restricting
// each to a set of work types.
func (s *Store) SetFieldSchemeFields(ctx context.Context, workspaceID, actorID string, request map[string][]FieldSchemeFieldRequest) ([]FieldSchemeWriteResult, error) {
	return s.writeFieldSchemeFields(ctx, workspaceID, actorID, request, false)
}

// RemoveFieldSchemeFields takes fields out of schemes. The rule row stays,
// hidden, so a field that is put back keeps the parameters it had.
func (s *Store) RemoveFieldSchemeFields(ctx context.Context, workspaceID, actorID string, request map[string][]FieldSchemeFieldRequest) ([]FieldSchemeWriteResult, error) {
	return s.writeFieldSchemeFields(ctx, workspaceID, actorID, request, true)
}

// FieldSchemeFieldRequest is one item of Jira's bulk field scheme write.
type FieldSchemeFieldRequest struct {
	SchemeIDs             []string
	RestrictedToWorkTypes []string
	Parameters            *FieldSchemeParameters
	WorkTypeParameters    []FieldSchemeParameters
}

func (s *Store) writeFieldSchemeFields(ctx context.Context, workspaceID, actorID string, request map[string][]FieldSchemeFieldRequest, remove bool) ([]FieldSchemeWriteResult, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	results := []FieldSchemeWriteResult{}
	for _, fieldID := range sortedKeys(request) {
		for _, item := range request[fieldID] {
			for _, schemeID := range item.SchemeIDs {
				result := FieldSchemeWriteResult{FieldID: fieldID, SchemeID: schemeID, WorkTypeIDs: item.RestrictedToWorkTypes}
				if err := applyFieldSchemeField(ctx, tx, workspaceID, fieldID, schemeID, item, remove); err != nil {
					result.Error = err.Error()
					results = append(results, result)
					continue
				}
				result.Success = true
				results = append(results, result)
			}
		}
	}
	return results, tx.Commit(ctx)
}

func applyFieldSchemeField(ctx context.Context, tx pgx.Tx, workspaceID, fieldID, schemeIDText string, item FieldSchemeFieldRequest, remove bool) error {
	schemeID, err := parseSchemeID(schemeIDText)
	if err != nil {
		return fmt.Errorf("the field association scheme does not exist")
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM field_configuration_schemes WHERE workspace_id=$1 AND id=$2)`,
		workspaceID, schemeID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("the field association scheme does not exist")
	}
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM custom_fields
		WHERE id=$1 AND (workspace_id IS NULL OR workspace_id=$2) AND active AND trashed_at IS NULL)`,
		fieldID, workspaceID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("the field does not exist")
	}
	// Which mappings this write touches, and whether the field is visible in
	// each. A restriction replaces any previous work type association, so the
	// scheme's fallback and every work type outside the restriction have to be
	// hidden — otherwise the field would still reach them through the fallback
	// and the restriction would mean nothing.
	visible := map[string]bool{}
	if len(item.RestrictedToWorkTypes) > 0 {
		workTypes, err := allWorkTypeIDs(ctx, tx)
		if err != nil {
			return err
		}
		for _, workTypeID := range append(workTypes, DefaultIssueTypeMapping) {
			visible[workTypeID] = false
		}
		for _, workTypeID := range item.RestrictedToWorkTypes {
			visible[workTypeID] = !remove
		}
	} else if remove {
		// Taking a field out of a scheme has to reach every mapping the scheme
		// has, not only its fallback: a restriction may have given a work type
		// a configuration of its own, and hiding the fallback would leave the
		// field on that work type's forms.
		mapped, err := schemeWorkTypeMappings(ctx, tx, workspaceID, schemeID)
		if err != nil {
			return err
		}
		for _, workTypeID := range mapped {
			visible[workTypeID] = false
		}
		visible[DefaultIssueTypeMapping] = false
	} else {
		visible[DefaultIssueTypeMapping] = true
		// A per-work-type parameter override needs that work type's own
		// mapping, or there is nowhere for the override to live.
		for _, override := range item.WorkTypeParameters {
			if override.WorkTypeID != "" {
				visible[override.WorkTypeID] = true
			}
		}
	}
	for _, workTypeID := range sortedKeys(visible) {
		configurationID, err := schemeOwnedConfiguration(ctx, tx, workspaceID, schemeID, workTypeID)
		if err != nil {
			return err
		}
		parameters := FieldSchemeParameters{}
		if item.Parameters != nil {
			parameters = *item.Parameters
		}
		for _, override := range item.WorkTypeParameters {
			if override.WorkTypeID == workTypeID {
				parameters = override
			}
		}
		hidden := !visible[workTypeID]
		if _, err = tx.Exec(ctx, `INSERT INTO field_configuration_items(workspace_id,configuration_id,field_id,is_required,is_hidden,description)
			VALUES($1,$2,$3,$4,$5,$6)
			ON CONFLICT(configuration_id,field_id) DO UPDATE SET
				is_required=EXCLUDED.is_required,is_hidden=EXCLUDED.is_hidden,description=EXCLUDED.description`,
			workspaceID, configurationID, fieldID, parameters.IsRequired && !hidden, hidden, parameters.Description); err != nil {
			return err
		}
	}
	return nil
}

// schemeWorkTypeMappings lists the work types the scheme has a mapping for,
// including its fallback.
func schemeWorkTypeMappings(ctx context.Context, tx pgx.Tx, workspaceID string, schemeID int64) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT issue_type_id FROM field_configuration_scheme_items
		WHERE workspace_id=$1 AND scheme_id=$2 ORDER BY issue_type_id`, workspaceID, schemeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func allWorkTypeIDs(ctx context.Context, tx pgx.Tx) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM issue_types ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// FieldSchemeProjectID pairs a project with the scheme it uses.
type FieldSchemeProjectID struct {
	ProjectID string
	SchemeID  string
}

// ProjectsWithFieldSchemes reports which scheme each project uses.
func (s *Store) ProjectsWithFieldSchemes(ctx context.Context, workspaceID string, projectIDs []string) ([]FieldSchemeProjectID, error) {
	rows, err := s.Pool.Query(ctx, `SELECT project_id, scheme_id::text
		FROM project_field_configuration_schemes
		WHERE workspace_id=$1 AND ($2::text[] IS NULL OR project_id = ANY($2))
		ORDER BY project_id`, workspaceID, nullableIDs(projectIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FieldSchemeProjectID{}
	for rows.Next() {
		var pair FieldSchemeProjectID
		if err = rows.Scan(&pair.ProjectID, &pair.SchemeID); err != nil {
			return nil, err
		}
		out = append(out, pair)
	}
	return out, rows.Err()
}

// FieldSchemeProjects lists the projects a scheme is assigned to.
func (s *Store) FieldSchemeProjects(ctx context.Context, workspaceID, schemeID string, projectIDs []string) ([]*models.Project, error) {
	id, err := parseSchemeID(schemeID)
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT p.id,p.key,p.name,p.lifecycle_state
		FROM projects p
		JOIN project_field_configuration_schemes a ON a.project_id=p.id
		WHERE p.workspace_id=$1 AND a.scheme_id=$2 AND ($3::text[] IS NULL OR p.id = ANY($3))
		ORDER BY p.id`, workspaceID, id, nullableIDs(projectIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := []*models.Project{}
	for rows.Next() {
		project := &models.Project{}
		var state string
		if err = rows.Scan(&project.ID, &project.Key, &project.Name, &state); err != nil {
			return nil, err
		}
		project.LifecycleState = state
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

// CloneFieldScheme copies a scheme's name, description and every mapping into a
// new one, giving the copy its own configurations so editing it cannot change
// the original.
func (s *Store) CloneFieldScheme(ctx context.Context, workspaceID, actorID, sourceID, name, description string) (*FieldScheme, error) {
	source, err := parseSchemeID(sourceID)
	if err != nil {
		return nil, err
	}
	if err = validateFieldConfigName(name, "field association scheme"); err != nil {
		return nil, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM field_configuration_schemes WHERE workspace_id=$1 AND id=$2)`,
		workspaceID, source).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrFieldConfigNotFound
	}
	var cloneID int64
	if err = tx.QueryRow(ctx, `INSERT INTO field_configuration_schemes(workspace_id,name,description)
		VALUES($1,$2,$3) RETURNING id`, workspaceID, name, description).Scan(&cloneID); err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w: a field association scheme with this name already exists", ErrFieldConfigConflict)
		}
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO field_configuration_scheme_items(workspace_id,scheme_id,issue_type_id,configuration_id)
		SELECT $1,$2,issue_type_id,configuration_id FROM field_configuration_scheme_items
		WHERE workspace_id=$1 AND scheme_id=$3`, workspaceID, cloneID, source); err != nil {
		return nil, err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "field_configuration_scheme",
		strconv.FormatInt(cloneID, 10), models.OpUpsert, map[string]any{"clonedFrom": sourceID, "name": name}); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.FieldSchemeByID(ctx, workspaceID, strconv.FormatInt(cloneID, 10))
}

// AssociateFieldsWithProjects associates or unassociates fields with every work
// type on the given projects, which is what `PUT`/`DELETE /field/association`
// do. A project reaches its rules through the scheme it uses, so a project
// sharing a scheme with another sees the same change — which is what Jira
// documents for these operations.
func (s *Store) AssociateFieldsWithProjects(ctx context.Context, workspaceID, actorID string, projectIDs, fieldIDs []string, associate bool) error {
	if len(projectIDs) == 0 || len(fieldIDs) == 0 {
		return fmt.Errorf("%w: at least one project and one field are required", ErrFieldConfigValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	for _, projectID := range projectIDs {
		var schemeID int64
		if err = tx.QueryRow(ctx, `SELECT scheme_id FROM project_field_configuration_schemes
			WHERE workspace_id=$1 AND project_id=$2`, workspaceID, projectID).Scan(&schemeID); err != nil {
			if err == pgx.ErrNoRows {
				return fmt.Errorf("%w: the project does not exist", ErrFieldConfigNotFound)
			}
			return err
		}
		for _, fieldID := range fieldIDs {
			if err = applyFieldSchemeField(ctx, tx, workspaceID, fieldID, strconv.FormatInt(schemeID, 10),
				FieldSchemeFieldRequest{}, !associate); err != nil {
				return fmt.Errorf("%w: %s", ErrFieldConfigValidation, err.Error())
			}
			// A rule alone does not put a custom field on a project's forms;
			// its context has to reach the project too. Unassociating needs no
			// context change: hiding the field in the configuration the project
			// uses is what takes it off the forms.
			if associate {
				if err = extendCustomFieldToProject(ctx, tx, fieldID, projectID); err != nil {
					return err
				}
			}
		}
	}
	return tx.Commit(ctx)
}

// extendCustomFieldToProject widens a field's context so it reaches the
// project. A context that already covers every project reaches it, so there is
// nothing to widen.
func extendCustomFieldToProject(ctx context.Context, tx pgx.Tx, fieldID, projectID string) error {
	var contextID int64
	var allProjects bool
	err := tx.QueryRow(ctx, `SELECT id, all_projects FROM custom_field_contexts
		WHERE field_id=$1 ORDER BY id LIMIT 1`, fieldID).Scan(&contextID, &allProjects)
	if err == pgx.ErrNoRows || (err == nil && allProjects) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO custom_field_context_projects(context_id,project_id)
		VALUES($1,$2) ON CONFLICT DO NOTHING`, contextID, projectID)
	return err
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// RemoveFieldSchemeWorkTypeParameters drops a work type's own rules so it
// resolves through the scheme's fallback again, which is what an absent
// override means.
func (s *Store) RemoveFieldSchemeWorkTypeParameters(ctx context.Context, workspaceID, actorID string, request map[string][]FieldSchemeFieldRequest) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	for _, fieldID := range sortedKeys(request) {
		for _, item := range request[fieldID] {
			for _, schemeIDText := range item.SchemeIDs {
				schemeID, parseErr := parseSchemeID(schemeIDText)
				if parseErr != nil {
					return ErrFieldConfigNotFound
				}
				for _, workTypeID := range item.RestrictedToWorkTypes {
					if workTypeID == DefaultIssueTypeMapping {
						continue
					}
					if _, err = tx.Exec(ctx, `DELETE FROM field_configuration_scheme_items
						WHERE workspace_id=$1 AND scheme_id=$2 AND issue_type_id=$3`,
						workspaceID, schemeID, workTypeID); err != nil {
						return err
					}
				}
			}
		}
	}
	return tx.Commit(ctx)
}
