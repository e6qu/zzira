package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
)

var (
	// ErrRemoteLinkValidation is a remote issue link request Jira rejects.
	ErrRemoteLinkValidation = errors.New("invalid remote issue link")
	// ErrRemoteLinkNotFound is a remote issue link the issue does not have.
	ErrRemoteLinkNotFound = errors.New("remote issue link not found")
)

// RemoteIssueLink is an item in another system that an issue links to.
type RemoteIssueLink struct {
	ID           int64
	IssueID      string
	GlobalID     *string
	Application  json.RawMessage
	Relationship *string
	Object       json.RawMessage
}

// RemoteIssueLinkInput is the content of a remote issue link as a client sends it.
type RemoteIssueLinkInput struct {
	GlobalID     *string
	Application  json.RawMessage
	Relationship *string
	Object       json.RawMessage
}

const remoteLinkColumns = `id, issue_id, global_id, application, relationship, object`

func scanRemoteLink(row pgx.Row) (RemoteIssueLink, error) {
	var link RemoteIssueLink
	var application, object []byte
	err := row.Scan(&link.ID, &link.IssueID, &link.GlobalID, &application, &link.Relationship, &object)
	link.Application, link.Object = application, object
	return link, err
}

// validateRemoteLink applies Jira's rules: an object with a title and a URL.
func validateRemoteLink(in RemoteIssueLinkInput) error {
	if len(in.Object) == 0 || string(in.Object) == "null" {
		return fmt.Errorf("%w: object is required", ErrRemoteLinkValidation)
	}
	var object struct {
		URL   *string `json:"url"`
		Title *string `json:"title"`
	}
	if err := json.Unmarshal(in.Object, &object); err != nil {
		return fmt.Errorf("%w: object must be a JSON object", ErrRemoteLinkValidation)
	}
	if object.URL == nil || strings.TrimSpace(*object.URL) == "" {
		return fmt.Errorf("%w: object.url is required", ErrRemoteLinkValidation)
	}
	if parsed, err := url.Parse(strings.TrimSpace(*object.URL)); err != nil || parsed.Scheme == "" {
		return fmt.Errorf("%w: object.url must be an absolute URL", ErrRemoteLinkValidation)
	}
	if object.Title == nil || strings.TrimSpace(*object.Title) == "" {
		return fmt.Errorf("%w: object.title is required", ErrRemoteLinkValidation)
	}
	if in.GlobalID != nil && (*in.GlobalID == "" || len([]rune(*in.GlobalID)) > 255) {
		return fmt.Errorf("%w: globalId must be 1 to 255 characters", ErrRemoteLinkValidation)
	}
	if len(in.Application) > 0 && string(in.Application) != "null" {
		var application map[string]any
		if err := json.Unmarshal(in.Application, &application); err != nil {
			return fmt.Errorf("%w: application must be a JSON object", ErrRemoteLinkValidation)
		}
	}
	return nil
}

func nullableJSON(raw json.RawMessage) []byte {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}

// RemoteIssueLinks lists an issue's remote links in the order they were made.
func (s *Store) RemoteIssueLinks(ctx context.Context, workspaceID, issueID string) ([]RemoteIssueLink, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+remoteLinkColumns+` FROM remote_issue_links WHERE workspace_id=$1 AND issue_id=$2 ORDER BY id`, workspaceID, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RemoteIssueLink{}
	for rows.Next() {
		link, scanErr := scanRemoteLink(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, link)
	}
	return out, rows.Err()
}

// RemoteIssueLink finds one of an issue's remote links by id.
func (s *Store) RemoteIssueLink(ctx context.Context, workspaceID, issueID string, id int64) (RemoteIssueLink, error) {
	link, err := scanRemoteLink(s.Pool.QueryRow(ctx, `SELECT `+remoteLinkColumns+` FROM remote_issue_links WHERE workspace_id=$1 AND issue_id=$2 AND id=$3`, workspaceID, issueID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return link, ErrRemoteLinkNotFound
	}
	return link, err
}

// RemoteIssueLinkByGlobalID finds one of an issue's remote links by its global id.
func (s *Store) RemoteIssueLinkByGlobalID(ctx context.Context, workspaceID, issueID, globalID string) (RemoteIssueLink, error) {
	link, err := scanRemoteLink(s.Pool.QueryRow(ctx, `SELECT `+remoteLinkColumns+` FROM remote_issue_links WHERE workspace_id=$1 AND issue_id=$2 AND global_id=$3`, workspaceID, issueID, globalID))
	if errors.Is(err, pgx.ErrNoRows) {
		return link, ErrRemoteLinkNotFound
	}
	return link, err
}

// RemoteIssueLinkCount counts an issue's remote links, for Jira's per-issue limit.
func (s *Store) RemoteIssueLinkCount(ctx context.Context, issueID string) (int, error) {
	var count int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM remote_issue_links WHERE issue_id=$1`, issueID).Scan(&count)
	return count, err
}

// SaveRemoteIssueLink creates a remote link, or replaces the one with the same
// global id. Fields the request leaves out become empty, as Jira documents.
func (s *Store) SaveRemoteIssueLink(ctx context.Context, workspaceID, issueID string, in RemoteIssueLinkInput) (RemoteIssueLink, bool, error) {
	if err := validateRemoteLink(in); err != nil {
		return RemoteIssueLink{}, false, err
	}
	var link RemoteIssueLink
	var created bool
	var application, object []byte
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO remote_issue_links(workspace_id,issue_id,global_id,application,relationship,object)
		VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT (issue_id, global_id) WHERE global_id IS NOT NULL
		DO UPDATE SET application=EXCLUDED.application, relationship=EXCLUDED.relationship, object=EXCLUDED.object, updated_at=now()
		RETURNING `+remoteLinkColumns+`, (xmax = 0)`,
		workspaceID, issueID, in.GlobalID, nullableJSON(in.Application), in.Relationship, []byte(in.Object)).
		Scan(&link.ID, &link.IssueID, &link.GlobalID, &application, &link.Relationship, &object, &created)
	link.Application, link.Object = application, object
	return link, created, err
}

// UpdateRemoteIssueLink replaces a remote link's content.
func (s *Store) UpdateRemoteIssueLink(ctx context.Context, workspaceID, issueID string, id int64, in RemoteIssueLinkInput) error {
	if err := validateRemoteLink(in); err != nil {
		return err
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE remote_issue_links SET global_id=$4,application=$5,relationship=$6,object=$7,updated_at=now() WHERE workspace_id=$1 AND issue_id=$2 AND id=$3`,
		workspaceID, issueID, id, in.GlobalID, nullableJSON(in.Application), in.Relationship, []byte(in.Object))
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: another remote link on this issue has that globalId", ErrRemoteLinkValidation)
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRemoteLinkNotFound
	}
	return nil
}

// DeleteRemoteIssueLink removes one of an issue's remote links by id.
func (s *Store) DeleteRemoteIssueLink(ctx context.Context, workspaceID, issueID string, id int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM remote_issue_links WHERE workspace_id=$1 AND issue_id=$2 AND id=$3`, workspaceID, issueID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRemoteLinkNotFound
	}
	return nil
}

// DeleteRemoteIssueLinkByGlobalID removes one of an issue's remote links by global id.
func (s *Store) DeleteRemoteIssueLinkByGlobalID(ctx context.Context, workspaceID, issueID, globalID string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM remote_issue_links WHERE workspace_id=$1 AND issue_id=$2 AND global_id=$3`, workspaceID, issueID, globalID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRemoteLinkNotFound
	}
	return nil
}

// RemoteIssueLinkExists reports whether a remote link with this id exists on
// any of the site's issues.
func (s *Store) RemoteIssueLinkExists(ctx context.Context, workspaceID string, id int64) bool {
	var exists bool
	_ = s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM remote_issue_links WHERE workspace_id=$1 AND id=$2)`, workspaceID, id).Scan(&exists)
	return exists
}
