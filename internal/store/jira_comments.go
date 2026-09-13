package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

var (
	// ErrCommentNotFound is a comment the site does not have.
	ErrCommentNotFound = errors.New("comment not found")
	// ErrCommentValidation is a comment request Jira rejects as invalid.
	ErrCommentValidation = errors.New("invalid comment")
	// ErrCommentPropertyNotFound is a comment property that is not set.
	ErrCommentPropertyNotFound = errors.New("comment property not found")
)

// CommentVisibility restricts a comment to a group (by group id) or a project
// role (by role id). The zero value leaves the comment unrestricted.
type CommentVisibility struct {
	Type  string
	Value string
}

// CommentByRef finds a site's comment by the id clients see or the one stored.
func (s *Store) CommentByRef(ctx context.Context, workspaceID, ref string) (*models.Comment, error) {
	c, err := scanComment(s.Pool.QueryRow(ctx, commentJoin+`WHERE c.workspace_id=$1 AND (c.jira_id::text=$2 OR c.id=$2)`, workspaceID, strings.TrimSpace(ref)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCommentNotFound
	}
	return c, err
}

// CommentsByJiraIDs lists a site's comments with the given ids, by id.
func (s *Store) CommentsByJiraIDs(ctx context.Context, workspaceID string, ids []int64) ([]*models.Comment, error) {
	rows, err := s.Pool.Query(ctx, commentJoin+`WHERE c.workspace_id=$1 AND c.jira_id = ANY($2) ORDER BY c.jira_id`, workspaceID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*models.Comment{}
	for rows.Next() {
		c, scanErr := scanComment(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CommentCount counts an issue's comments, for Jira's per-issue limit.
func (s *Store) CommentCount(ctx context.Context, issueID string) (int, error) {
	var count int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM comments WHERE issue_id=$1`, issueID).Scan(&count)
	return count, err
}

// CommentVisibleTo reports whether a person may read a comment: an unrestricted
// comment is visible to anyone who can see the issue; a restricted one only to
// members of its group or project role.
func (s *Store) CommentVisibleTo(ctx context.Context, workspaceID, projectID, userID string, c *models.Comment) (bool, error) {
	switch c.VisibilityType {
	case "":
		return true, nil
	case "group":
		var member bool
		err := s.Pool.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM group_members gm JOIN groups g ON g.id=gm.group_id
			JOIN directories d ON d.id=g.directory_id JOIN sites si ON si.organization_id=d.organization_id
			WHERE si.workspace_id=$1 AND d.active AND g.id::text=$2 AND gm.user_id=$3)`, workspaceID, c.VisibilityValue, userID).Scan(&member)
		return member, err
	case "role":
		roleID, err := strconv.ParseInt(c.VisibilityValue, 10, 64)
		if err != nil {
			return false, nil
		}
		return s.UserInProjectRole(ctx, workspaceID, projectID, userID, roleID)
	}
	return false, nil
}

// ResolveCommentVisibility turns a client's visibility — a group by id or name,
// or a project role by id or name — into the restriction stored on a comment.
func (s *Store) ResolveCommentVisibility(ctx context.Context, workspaceID, kind, identifier, value string) (CommentVisibility, string, error) {
	identifier, value = strings.TrimSpace(identifier), strings.TrimSpace(value)
	switch kind {
	case "group":
		var id, name string
		err := s.Pool.QueryRow(ctx, `SELECT g.id::text, g.name FROM groups g
			JOIN directories d ON d.id=g.directory_id JOIN sites si ON si.organization_id=d.organization_id
			WHERE si.workspace_id=$1 AND d.active AND ((NULLIF($2,'') IS NOT NULL AND g.id::text=$2) OR (NULLIF($2,'') IS NULL AND lower(g.name)=lower($3)))
			LIMIT 1`, workspaceID, identifier, value).Scan(&id, &name)
		if err != nil {
			return CommentVisibility{}, "", fmt.Errorf("%w: the group does not exist", ErrCommentValidation)
		}
		return CommentVisibility{Type: "group", Value: id}, name, nil
	case "role":
		var id int64
		var name string
		err := s.Pool.QueryRow(ctx, `SELECT id, name FROM project_roles WHERE workspace_id=$1
			AND ((NULLIF($2,'') IS NOT NULL AND id::text=$2) OR (NULLIF($2,'') IS NULL AND lower(name)=lower($3)))
			LIMIT 1`, workspaceID, identifier, value).Scan(&id, &name)
		if err != nil {
			return CommentVisibility{}, "", fmt.Errorf("%w: the project role does not exist", ErrCommentValidation)
		}
		return CommentVisibility{Type: "role", Value: strconv.FormatInt(id, 10)}, name, nil
	}
	return CommentVisibility{}, "", fmt.Errorf("%w: visibility type must be group or role", ErrCommentValidation)
}

// CommentVisibilityName is the group or role name a restricted comment shows.
func (s *Store) CommentVisibilityName(ctx context.Context, workspaceID string, c *models.Comment) string {
	var name string
	switch c.VisibilityType {
	case "group":
		_ = s.Pool.QueryRow(ctx, `SELECT name FROM groups WHERE id::text=$1`, c.VisibilityValue).Scan(&name)
	case "role":
		_ = s.Pool.QueryRow(ctx, `SELECT name FROM project_roles WHERE workspace_id=$1 AND id::text=$2`, workspaceID, c.VisibilityValue).Scan(&name)
	}
	return name
}

// UpdateComment replaces a comment's body and records who edited it. With
// setVisibility the restriction is replaced too; a zero visibility removes it.
func (s *Store) UpdateComment(ctx context.Context, actorID, workspaceID, commentID string, body json.RawMessage, setVisibility bool, visibility CommentVisibility) (*models.Comment, *models.Action, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	tag, err := tx.Exec(ctx, `UPDATE comments SET
		body=COALESCE($3::jsonb, body), updated_at=now(), update_author_id=$4, updated_seq=$5,
		visibility_type=CASE WHEN $6 THEN NULLIF($7,'') ELSE visibility_type END,
		visibility_value=CASE WHEN $6 THEN NULLIF($8,'') ELSE visibility_value END
		WHERE workspace_id=$1 AND id=$2`, workspaceID, commentID, nullableJSON(body), actorID, seq, setVisibility, visibility.Type, visibility.Value)
	if err != nil {
		return nil, nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, nil, ErrCommentNotFound
	}
	comment, err := scanComment(tx.QueryRow(ctx, commentJoin+`WHERE c.id=$1`, commentID))
	if err != nil {
		return nil, nil, err
	}
	payload, err := json.Marshal(models.CommentUpsertPayload{Comment: *comment})
	if err != nil {
		return nil, nil, err
	}
	action := &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityComment, EntityID: commentID,
		Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	}
	if err = appendAction(ctx, tx, action); err != nil {
		return nil, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return comment, action, nil
}

// CommentPropertyKeys lists the keys set on a comment.
func (s *Store) CommentPropertyKeys(ctx context.Context, commentID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT key FROM jira_comment_properties WHERE comment_id=$1 ORDER BY key`, commentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

// CommentProperties returns every property set on a comment.
func (s *Store) CommentProperties(ctx context.Context, commentID string) (map[string]json.RawMessage, error) {
	rows, err := s.Pool.Query(ctx, `SELECT key, value FROM jira_comment_properties WHERE comment_id=$1 ORDER BY key`, commentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var key string
		var value []byte
		if err = rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

// CommentProperty reads one comment property.
func (s *Store) CommentProperty(ctx context.Context, commentID, key string) (json.RawMessage, error) {
	var value []byte
	err := s.Pool.QueryRow(ctx, `SELECT value FROM jira_comment_properties WHERE comment_id=$1 AND key=$2`, commentID, key).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCommentPropertyNotFound
	}
	return value, err
}

// SetCommentProperty stores a property value, reporting whether it is new. The
// value must be valid, non-empty JSON of at most 32768 characters.
func (s *Store) SetCommentProperty(ctx context.Context, commentID, key string, value json.RawMessage) (bool, error) {
	key = strings.TrimSpace(key)
	if key == "" || len([]rune(key)) > 255 {
		return false, fmt.Errorf("%w: the property key must be 1 to 255 characters", ErrCommentValidation)
	}
	trimmed := strings.TrimSpace(string(value))
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return false, fmt.Errorf("%w: the property value must be valid, non-empty JSON", ErrCommentValidation)
	}
	if len([]rune(trimmed)) > 32768 {
		return false, fmt.Errorf("%w: the property value must be 32768 characters or fewer", ErrCommentValidation)
	}
	var created bool
	err := s.Pool.QueryRow(ctx, `INSERT INTO jira_comment_properties(comment_id,key,value) VALUES($1,$2,$3)
		ON CONFLICT (comment_id,key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()
		RETURNING (xmax = 0)`, commentID, key, []byte(trimmed)).Scan(&created)
	return created, err
}

// DeleteCommentProperty removes a comment property.
func (s *Store) DeleteCommentProperty(ctx context.Context, commentID, key string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM jira_comment_properties WHERE comment_id=$1 AND key=$2`, commentID, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrCommentPropertyNotFound
	}
	return nil
}
