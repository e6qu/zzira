package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrVersionValidation = errors.New("invalid version")

type VersionUpdate struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	StartDate   *string `json:"startDate"`
	ReleaseDate *string `json:"releaseDate"`
	Released    *bool   `json:"released"`
	Archived    *bool   `json:"archived"`
}

const versionSelect = `SELECT v.id,v.project_id,v.name,v.description,COALESCE(v.start_date::text,''),COALESCE(v.release_date::text,''),v.released,v.archived,v.position FROM project_versions v `

func scanVersion(row pgx.Row) (*models.Version, error) {
	v := &models.Version{}
	err := row.Scan(&v.ID, &v.ProjectID, &v.Name, &v.Description, &v.StartDate, &v.ReleaseDate, &v.Released, &v.Archived, &v.Position)
	return v, err
}
func (s *Store) Version(ctx context.Context, ws, id string) (*models.Version, error) {
	return scanVersion(s.Pool.QueryRow(ctx, versionSelect+` JOIN projects p ON p.id=v.project_id WHERE p.workspace_id=$1 AND v.id=$2`, ws, id))
}
func (s *Store) ProjectVersions(ctx context.Context, projectID string) ([]*models.Version, error) {
	rows, err := s.Pool.Query(ctx, versionSelect+` WHERE v.project_id=$1 ORDER BY v.position,v.id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []*models.Version{}
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
func validateVersion(v *models.Version) error {
	v.Name = strings.TrimSpace(v.Name)
	if v.Name == "" || utf8.RuneCountInString(v.Name) > 255 {
		return fmt.Errorf("%w: name must contain 1–255 characters", ErrVersionValidation)
	}
	if len(v.Description) > 16384 {
		return fmt.Errorf("%w: description must be at most 16384 bytes", ErrVersionValidation)
	}
	for _, date := range []string{v.StartDate, v.ReleaseDate} {
		if date != "" {
			if _, err := time.Parse("2006-01-02", date); err != nil {
				return fmt.Errorf("%w: dates must use YYYY-MM-DD", ErrVersionValidation)
			}
		}
	}
	return nil
}
func versionDBError(err error) error {
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "23505" {
		return fmt.Errorf("%w: a version with this name already exists in the project", ErrVersionValidation)
	}
	return err
}

// Project locks serialize version lifecycle changes with issue membership writes.
func lockVersionProject(ctx context.Context, tx pgx.Tx, ws, project string) error {
	var id string
	return tx.QueryRow(ctx, `SELECT id FROM projects WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, ws, project).Scan(&id)
}
func versionAction(ctx context.Context, tx pgx.Tx, ws, actor string, v *models.Version, op string) error {
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"version": v, "projectId": v.ProjectID})
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: "version", EntityID: v.ID, Op: op, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor})
}
func (s *Store) SaveVersion(ctx context.Context, ws, actor, project, id string, up VersionUpdate) (*models.Version, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, ws, actor); err != nil {
		return nil, err
	}
	if err = lockVersionProject(ctx, tx, ws, project); err != nil {
		return nil, err
	}
	v := &models.Version{ProjectID: project}
	if id != "" {
		v, err = scanVersion(tx.QueryRow(ctx, versionSelect+` WHERE v.id=$1 AND v.project_id=$2`, id, project))
		if err != nil {
			return nil, err
		}
	}
	if up.Name != nil {
		v.Name = *up.Name
	}
	if up.Description != nil {
		v.Description = *up.Description
	}
	if up.StartDate != nil {
		v.StartDate = *up.StartDate
	}
	if up.ReleaseDate != nil {
		v.ReleaseDate = *up.ReleaseDate
	}
	if up.Released != nil {
		v.Released = *up.Released
	}
	if up.Archived != nil {
		v.Archived = *up.Archived
	}
	if err = validateVersion(v); err != nil {
		return nil, err
	}
	if id == "" {
		err = tx.QueryRow(ctx, `INSERT INTO project_versions(project_id,name,description,start_date,release_date,released,archived,position) VALUES($1,$2,$3,NULLIF($4,'')::date,NULLIF($5,'')::date,$6,$7,COALESCE((SELECT max(position)+1 FROM project_versions WHERE project_id=$1),0)) RETURNING id,position`, project, v.Name, v.Description, v.StartDate, v.ReleaseDate, v.Released, v.Archived).Scan(&v.ID, &v.Position)
	} else {
		_, err = tx.Exec(ctx, `UPDATE project_versions SET name=$2,description=$3,start_date=NULLIF($4,'')::date,release_date=NULLIF($5,'')::date,released=$6,archived=$7 WHERE id=$1`, id, v.Name, v.Description, v.StartDate, v.ReleaseDate, v.Released, v.Archived)
	}
	if err != nil {
		return nil, versionDBError(err)
	}
	if id != "" {
		if err = refreshVersionIssues(ctx, tx, ws, actor, v, nil, false); err != nil {
			return nil, err
		}
	}
	if err = versionAction(ctx, tx, ws, actor, v, models.OpUpsert); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return v, nil
}

// normalizeVersionFields validates references under the project lock and stores
// canonical snapshots, so issue APIs and replicas see names and release state.
func normalizeVersionFields(ctx context.Context, tx pgx.Tx, project string, fields map[string]json.RawMessage) error {
	for _, key := range []string{"fixVersions", "versions"} {
		raw, ok := fields[key]
		if !ok {
			continue
		}
		var refs []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if len(raw) > 64<<10 || json.Unmarshal(raw, &refs) != nil || refs == nil || len(refs) > 100 {
			return fmt.Errorf("%w: %s must be an array of at most 100 version references", ErrVersionValidation, key)
		}
		versions := []*models.Version{}
		seen := map[string]bool{}
		for _, ref := range refs {
			if ref.ID == "" && ref.Name == "" {
				return fmt.Errorf("%w: %s requires a version id or name", ErrVersionValidation, key)
			}
			v, err := scanVersion(tx.QueryRow(ctx, versionSelect+` WHERE v.project_id=$1 AND (($2<>'' AND v.id=$2) OR ($2='' AND v.name=$3))`, project, ref.ID, ref.Name))
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: %s version does not belong to this project", ErrVersionValidation, key)
			}
			if err != nil {
				return err
			}
			if !seen[v.ID] {
				versions = append(versions, v)
				seen[v.ID] = true
			}
		}
		encoded, err := json.Marshal(versions)
		if err != nil {
			return err
		}
		fields[key] = encoded
	}
	return nil
}

// refreshVersionIssues changes references and emits full issue snapshots in the
// same transaction as version edits/deletion. Hidden issues never enter a web response.
func refreshVersionIssues(ctx context.Context, tx pgx.Tx, ws, actor string, v *models.Version, replacements map[string]*models.Version, remove bool) error {
	match, _ := json.Marshal([]map[string]string{{"id": v.ID}})
	rows, err := tx.Query(ctx, `SELECT id FROM issues WHERE project_id=$1 AND (fields->'fixVersions' @> $2::jsonb OR fields->'versions' @> $2::jsonb) ORDER BY id FOR UPDATE`, v.ProjectID, match)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		issue, err := scanIssue(tx.QueryRow(ctx, issueJoin+` WHERE i.id=$1`, id))
		if err != nil {
			return err
		}
		diff := map[string]models.ChangeItem{}
		for _, field := range []string{"fixVersions", "versions"} {
			var refs []*models.Version
			if raw := issue.Fields[field]; raw != nil {
				if err = json.Unmarshal(raw, &refs); err != nil {
					return err
				}
			}
			updated := []*models.Version{}
			seen := map[string]bool{}
			changed := false
			for _, ref := range refs {
				if ref.ID == v.ID {
					changed = true
					if remove {
						ref = replacements[field]
					} else {
						ref = v
					}
				}
				if ref != nil && !seen[ref.ID] {
					updated = append(updated, ref)
					seen[ref.ID] = true
				}
			}
			if changed {
				raw, err := json.Marshal(updated)
				if err != nil {
					return err
				}
				diff[field] = versionChange(field, issue.Fields[field], raw)
				issue.Fields[field] = raw
			}
		}
		seq, err := nextSeq(ctx, tx, ws)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(issue.Fields)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE issues SET fields=$2,updated_seq=$3,updated_at=now() WHERE id=$1`, id, raw, seq); err != nil {
			return err
		}
		issue, err = scanIssue(tx.QueryRow(ctx, issueJoin+` WHERE i.id=$1`, id))
		if err != nil {
			return err
		}
		payload, err := json.Marshal(models.IssueUpdatePayload{Issue: *issue, Diff: diff})
		if err != nil {
			return err
		}
		if err = appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: models.EntityIssue, EntityID: id, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor}); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) DeleteVersion(ctx context.Context, ws, actor, id, fixTo, affectedTo string) error {
	v, err := s.Version(ctx, ws, id)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, ws, actor); err != nil {
		return err
	}
	if err = lockVersionProject(ctx, tx, ws, v.ProjectID); err != nil {
		return err
	}
	v, err = scanVersion(tx.QueryRow(ctx, versionSelect+` WHERE v.id=$1`, id))
	if err != nil {
		return err
	}
	replacements := map[string]*models.Version{}
	for field, target := range map[string]string{"fixVersions": fixTo, "versions": affectedTo} {
		if target == "" {
			continue
		}
		if target == id {
			return fmt.Errorf("%w: replacement must be a different version", ErrVersionValidation)
		}
		replacement, e := scanVersion(tx.QueryRow(ctx, versionSelect+` WHERE v.id=$1 AND v.project_id=$2`, target, v.ProjectID))
		if errors.Is(e, pgx.ErrNoRows) {
			return fmt.Errorf("%w: replacement must belong to the same project", ErrVersionValidation)
		}
		if e != nil {
			return e
		}
		replacements[field] = replacement
	}
	if err = refreshVersionIssues(ctx, tx, ws, actor, v, replacements, true); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM project_versions WHERE id=$1`, id); err != nil {
		return err
	}
	if err = versionAction(ctx, tx, ws, actor, v, models.OpDelete); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) VersionIssues(ctx context.Context, ws, user, project, id, field string) ([]*models.Issue, error) {
	if field != "fixVersions" && field != "versions" {
		return nil, ErrVersionValidation
	}
	match, _ := json.Marshal([]map[string]string{{"id": id}})
	rows, err := s.Pool.Query(ctx, issueJoin+` WHERE i.workspace_id=$1 AND i.project_id=$2 AND i.fields->$3 @> $4::jsonb AND `+VisibleIssuePredicate("i", "$5")+` ORDER BY i.key`, ws, project, field, match, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*models.Issue{}
	for rows.Next() {
		i, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}
func VersionProgress(issues []*models.Issue) models.VersionProgress {
	var p models.VersionProgress
	for _, i := range issues {
		switch i.Status.Category {
		case "new":
			p.ToDo++
		case "indeterminate":
			p.InProgress++
		case "done":
			p.Done++
		default:
			p.Unmapped++
		}
	}
	return p
}

// VersionOperations applies Jira set/add/remove operations against the locked
// current issue, preserving concurrent changes to other version memberships.
func applyVersionOperations(ctx context.Context, tx pgx.Tx, project string, current map[string]json.RawMessage, fields map[string]json.RawMessage, operations map[string][]map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	for field, ops := range operations {
		if field != "fixVersions" && field != "versions" {
			return nil, fmt.Errorf("%w: unsupported update operations for %s", ErrVersionValidation, field)
		}
		if _, ok := fields[field]; ok {
			return nil, fmt.Errorf("%w: %s cannot appear in both fields and update", ErrVersionValidation, field)
		}
		raw := current[field]
		if raw == nil {
			raw = json.RawMessage(`[]`)
		}
		for _, op := range ops {
			if len(op) != 1 {
				return nil, fmt.Errorf("%w: expected one set, add or remove operation", ErrVersionValidation)
			}
			for kind, value := range op {
				normalized := map[string]json.RawMessage{field: value}
				if kind == "add" || kind == "remove" {
					normalized[field] = append(append([]byte{'['}, value...), ']')
				}
				if kind != "add" && kind != "remove" && kind != "set" {
					return nil, fmt.Errorf("%w: unknown version update operation", ErrVersionValidation)
				}
				if err := normalizeVersionFields(ctx, tx, project, normalized); err != nil {
					return nil, err
				}
				if kind == "set" {
					raw = normalized[field]
					continue
				}
				var existing, change []*models.Version
				if err := json.Unmarshal(raw, &existing); err != nil {
					return nil, err
				}
				if err := json.Unmarshal(normalized[field], &change); err != nil {
					return nil, err
				}
				if len(change) != 1 {
					return nil, ErrVersionValidation
				}
				result := []*models.Version{}
				found := false
				for _, v := range existing {
					if v.ID == change[0].ID {
						found = true
						if kind == "remove" {
							continue
						}
					}
					result = append(result, v)
				}
				if kind == "add" && !found {
					result = append(result, change[0])
				}
				var err error
				raw, err = json.Marshal(result)
				if err != nil {
					return nil, err
				}
			}
		}
		fields[field] = raw
	}
	return fields, nil
}

func versionChange(field string, before, after json.RawMessage) models.ChangeItem {
	labels := func(raw json.RawMessage) (string, string) {
		var versions []models.Version
		if len(raw) == 0 || json.Unmarshal(raw, &versions) != nil {
			return "", ""
		}
		ids, names := []string{}, []string{}
		for _, version := range versions {
			ids = append(ids, version.ID)
			names = append(names, version.Name)
		}
		return strings.Join(ids, ", "), strings.Join(names, ", ")
	}
	from, fromName := labels(before)
	to, toName := labels(after)
	return diffItem(field, from, fromName, to, toName)
}
