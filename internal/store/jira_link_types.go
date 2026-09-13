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

var (
	// ErrLinkTypeValidation is an issue link type request Jira rejects as invalid.
	ErrLinkTypeValidation = errors.New("invalid issue link type")
	// ErrLinkTypeNotFound is an issue link type the site does not have.
	ErrLinkTypeNotFound = errors.New("issue link type not found")
	// ErrLinkTypeNameInUse is a name another link type on the site already has.
	ErrLinkTypeNameInUse = errors.New("issue link type name is in use")
)

const linkTypeColumns = `id, workspace_id, jira_id, name, inward, outward`

func scanLinkType(row pgx.Row) (*models.LinkType, error) {
	lt := &models.LinkType{}
	err := row.Scan(&lt.ID, &lt.WorkspaceID, &lt.JiraID, &lt.Name, &lt.Inward, &lt.Outward)
	return lt, err
}

// LinkTypes lists a site's issue link types by name.
func (s *Store) LinkTypes(ctx context.Context, workspaceID string) ([]*models.LinkType, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+linkTypeColumns+` FROM issue_link_types WHERE workspace_id=$1 ORDER BY lower(name), jira_id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*models.LinkType{}
	for rows.Next() {
		lt, scanErr := scanLinkType(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, lt)
	}
	return out, rows.Err()
}

// LinkTypeByRef finds a site's link type by the id clients see or the one stored.
func (s *Store) LinkTypeByRef(ctx context.Context, workspaceID, ref string) (*models.LinkType, error) {
	lt, err := scanLinkType(s.Pool.QueryRow(ctx, `SELECT `+linkTypeColumns+` FROM issue_link_types WHERE workspace_id=$1 AND (jira_id::text=$2 OR id=$2)`, workspaceID, strings.TrimSpace(ref)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrLinkTypeNotFound
	}
	return lt, err
}

// LinkTypeIDByName resolves a site's link type by name, ignoring case.
func (s *Store) LinkTypeIDByName(ctx context.Context, workspaceID, name string) (string, error) {
	var id string
	err := s.Pool.QueryRow(ctx, `SELECT id FROM issue_link_types WHERE workspace_id=$1 AND lower(name)=lower($2)`, workspaceID, strings.TrimSpace(name)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrLinkTypeNotFound
	}
	return id, err
}

func validLinkTypeText(label, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%w: the %s is required", ErrLinkTypeValidation, label)
	}
	if len([]rune(value)) > 255 {
		return fmt.Errorf("%w: the %s must be 255 characters or fewer", ErrLinkTypeValidation, label)
	}
	return nil
}

// CreateLinkType adds a link type to a site. Name, inward and outward
// descriptions are all required, and the name must be unused on the site.
func (s *Store) CreateLinkType(ctx context.Context, workspaceID, name, inward, outward string) (*models.LinkType, error) {
	for _, field := range []struct{ label, value string }{{"name", name}, {"inward description", inward}, {"outward description", outward}} {
		if err := validLinkTypeText(field.label, field.value); err != nil {
			return nil, err
		}
	}
	lt, err := scanLinkType(s.Pool.QueryRow(ctx, `INSERT INTO issue_link_types(id,workspace_id,name,inward,outward) VALUES($1,$2,$3,$4,$5) RETURNING `+linkTypeColumns,
		NewID("lt"), workspaceID, strings.TrimSpace(name), strings.TrimSpace(inward), strings.TrimSpace(outward)))
	if isUniqueViolation(err) {
		return nil, ErrLinkTypeNameInUse
	}
	return lt, err
}

// UpdateLinkType changes whichever of a link type's name and descriptions are given.
func (s *Store) UpdateLinkType(ctx context.Context, workspaceID, ref string, name, inward, outward *string) (*models.LinkType, error) {
	current, err := s.LinkTypeByRef(ctx, workspaceID, ref)
	if err != nil {
		return nil, err
	}
	next := *current
	for _, field := range []struct {
		label  string
		value  *string
		target *string
	}{{"name", name, &next.Name}, {"inward description", inward, &next.Inward}, {"outward description", outward, &next.Outward}} {
		if field.value == nil {
			continue
		}
		if err = validLinkTypeText(field.label, *field.value); err != nil {
			return nil, err
		}
		*field.target = strings.TrimSpace(*field.value)
	}
	lt, err := scanLinkType(s.Pool.QueryRow(ctx, `UPDATE issue_link_types SET name=$3,inward=$4,outward=$5 WHERE workspace_id=$1 AND id=$2 RETURNING `+linkTypeColumns,
		workspaceID, current.ID, next.Name, next.Inward, next.Outward))
	if isUniqueViolation(err) {
		return nil, ErrLinkTypeNameInUse
	}
	return lt, err
}

// DeleteLinkType removes a link type and, as Jira does, every link of that type.
// Each removed link is recorded so synced clients drop it too.
func (s *Store) DeleteLinkType(ctx context.Context, actorID, workspaceID, ref string) error {
	current, err := s.LinkTypeByRef(ctx, workspaceID, ref)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT id,inward_id,outward_id FROM issue_links WHERE workspace_id=$1 AND link_type_id=$2 ORDER BY created_at,id FOR UPDATE`, workspaceID, current.ID)
	if err != nil {
		return err
	}
	removed := []models.IssueLinkDeletePayload{}
	for rows.Next() {
		var payload models.IssueLinkDeletePayload
		if err = rows.Scan(&payload.LinkID, &payload.InwardID, &payload.OutwardID); err != nil {
			rows.Close()
			return err
		}
		removed = append(removed, payload)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, payload := range removed {
		seq, seqErr := nextSeq(ctx, tx, workspaceID)
		if seqErr != nil {
			return seqErr
		}
		raw, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return marshalErr
		}
		if err = appendAction(ctx, tx, &models.Action{
			WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityIssueLink, EntityID: payload.LinkID,
			Op: models.OpDelete, SchemaV: models.SchemaVersion, Payload: raw, ActorID: actorID,
		}); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM issue_links WHERE workspace_id=$1 AND link_type_id=$2`, workspaceID, current.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM issue_link_types WHERE workspace_id=$1 AND id=$2`, workspaceID, current.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
