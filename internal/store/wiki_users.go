package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Confluence looks users up by account id or email, reports the groups they
// are in, and keeps arbitrary app data against them. A workspace member may
// see who someone is; only an administrator may see their email address,
// which is the distinction Confluence's separate email endpoints exist for.

var ErrWikiUserValidation = errors.New("invalid user request")

// WikiUser is Confluence's view of a person.
type WikiUser struct {
	AccountID   string
	AccountType string
	Email       string
	PublicName  string
	DisplayName string
	Active      bool
}

const wikiUserSelect = `SELECT u.id,u.email,u.display_name,COALESCE(NULLIF(u.nickname,''),u.display_name),u.active
	FROM users u JOIN memberships m ON m.user_id=u.id`

func scanWikiUser(row pgx.Row) (WikiUser, error) {
	user := WikiUser{AccountType: "atlassian"}
	err := row.Scan(&user.AccountID, &user.Email, &user.DisplayName, &user.PublicName, &user.Active)
	return user, err
}

func (s *Store) requireMember(ctx context.Context, ws, actor string) error {
	member, err := s.IsMember(ctx, ws, actor)
	if err != nil {
		return err
	}
	if !member {
		return ErrProjectPermission
	}
	return nil
}

// WikiUserByAccountID reads one person in the workspace.
func (s *Store) WikiUserByAccountID(ctx context.Context, ws, actor, accountID string) (WikiUser, error) {
	if err := s.requireMember(ctx, ws, actor); err != nil {
		return WikiUser{}, err
	}
	return scanWikiUser(s.Pool.QueryRow(ctx, wikiUserSelect+` WHERE m.workspace_id=$1 AND u.id=$2`, ws, accountID))
}

// WikiUsersByAccountIDs reads several at once, skipping ids that are not this
// workspace's people rather than failing the whole request.
func (s *Store) WikiUsersByAccountIDs(ctx context.Context, ws, actor string, accountIDs []string) ([]WikiUser, error) {
	if err := s.requireMember(ctx, ws, actor); err != nil {
		return nil, err
	}
	if len(accountIDs) == 0 {
		return nil, fmt.Errorf("%w: at least one accountId is required", ErrWikiUserValidation)
	}
	rows, err := s.Pool.Query(ctx, wikiUserSelect+` WHERE m.workspace_id=$1 AND u.id = ANY($2) ORDER BY u.id`, ws, accountIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []WikiUser{}
	for rows.Next() {
		user, scanErr := scanWikiUser(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

// WikiUserEmails answers Confluence's email endpoints, which an ordinary
// member may not use: an email address is not part of knowing who someone is.
func (s *Store) WikiUserEmails(ctx context.Context, ws, actor string, accountIDs []string) ([]WikiUser, error) {
	admin, err := s.IsAdmin(ctx, ws, actor)
	if err != nil {
		return nil, err
	}
	if !admin {
		return nil, ErrProjectPermission
	}
	return s.WikiUsersByAccountIDs(ctx, ws, actor, accountIDs)
}

// SearchWikiUsers finds people by name or email. Confluence takes a CQL string;
// the part of it this answers is a text match on the person.
func (s *Store) SearchWikiUsers(ctx context.Context, ws, actor, query string) ([]WikiUser, error) {
	if err := s.requireMember(ctx, ws, actor); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiUserSelect+`
		WHERE m.workspace_id=$1 AND ($2='' OR u.display_name ILIKE '%' || $2 || '%'
			OR u.nickname ILIKE '%' || $2 || '%' OR u.email ILIKE '%' || $2 || '%')
		ORDER BY u.display_name, u.id`, ws, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []WikiUser{}
	for rows.Next() {
		user, scanErr := scanWikiUser(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

// WikiGroup is a group as Confluence reports it.
type WikiGroup struct {
	ID   string
	Name string
}

// WikiUserGroups reports the groups a person belongs to.
func (s *Store) WikiUserGroups(ctx context.Context, ws, actor, accountID string) ([]WikiGroup, error) {
	if _, err := s.WikiUserByAccountID(ctx, ws, actor, accountID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT g.id::text,g.name FROM groups g
		JOIN group_members gm ON gm.group_id=g.id WHERE gm.user_id=$1 ORDER BY g.name, g.id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []WikiGroup{}
	for rows.Next() {
		var group WikiGroup
		if err = rows.Scan(&group.ID, &group.Name); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

// WikiUserProperty is arbitrary data an app keeps against a person.
type WikiUserProperty struct {
	Key     string
	Value   json.RawMessage
	Version int
}

func (s *Store) WikiUserProperties(ctx context.Context, ws, actor, accountID string) ([]WikiUserProperty, error) {
	if _, err := s.WikiUserByAccountID(ctx, ws, actor, accountID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT key,value,version FROM wiki_user_properties
		WHERE user_id=$1 ORDER BY key`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	properties := []WikiUserProperty{}
	for rows.Next() {
		var property WikiUserProperty
		if err = rows.Scan(&property.Key, &property.Value, &property.Version); err != nil {
			return nil, err
		}
		properties = append(properties, property)
	}
	return properties, rows.Err()
}

func (s *Store) WikiUserProperty(ctx context.Context, ws, actor, accountID, key string) (WikiUserProperty, error) {
	if _, err := s.WikiUserByAccountID(ctx, ws, actor, accountID); err != nil {
		return WikiUserProperty{}, err
	}
	var property WikiUserProperty
	err := s.Pool.QueryRow(ctx, `SELECT key,value,version FROM wiki_user_properties
		WHERE user_id=$1 AND key=$2`, accountID, key).Scan(&property.Key, &property.Value, &property.Version)
	return property, err
}

// SaveWikiUserProperty creates or replaces a property. A person's own data is
// theirs to write; anyone else's needs administration, because a property is
// read back as if the person had set it.
func (s *Store) SaveWikiUserProperty(ctx context.Context, ws, actor, accountID, key string, value json.RawMessage, create bool) (WikiUserProperty, error) {
	if key == "" || len(key) > 255 {
		return WikiUserProperty{}, fmt.Errorf("%w: a key of 1 to 255 characters is required", ErrWikiUserValidation)
	}
	if len(value) == 0 || !json.Valid(value) {
		return WikiUserProperty{}, fmt.Errorf("%w: the value must be JSON", ErrWikiUserValidation)
	}
	if err := s.requireUserPropertyWriter(ctx, ws, actor, accountID); err != nil {
		return WikiUserProperty{}, err
	}
	if _, err := s.WikiUserByAccountID(ctx, ws, actor, accountID); err != nil {
		return WikiUserProperty{}, err
	}
	if create {
		var existing bool
		if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_user_properties
			WHERE user_id=$1 AND key=$2)`, accountID, key).Scan(&existing); err != nil {
			return WikiUserProperty{}, err
		}
		if existing {
			return WikiUserProperty{}, ErrWikiPropertyConflict
		}
	}
	var property WikiUserProperty
	err := s.Pool.QueryRow(ctx, `INSERT INTO wiki_user_properties(user_id,key,value)
		VALUES($1,$2,$3) ON CONFLICT (user_id,key) DO UPDATE
		SET value=EXCLUDED.value, version=wiki_user_properties.version+1, updated_at=now()
		RETURNING key,value,version`, accountID, key, []byte(value)).
		Scan(&property.Key, &property.Value, &property.Version)
	return property, err
}

func (s *Store) DeleteWikiUserProperty(ctx context.Context, ws, actor, accountID, key string) error {
	if err := s.requireUserPropertyWriter(ctx, ws, actor, accountID); err != nil {
		return err
	}
	if _, err := s.WikiUserByAccountID(ctx, ws, actor, accountID); err != nil {
		return err
	}
	tag, err := s.Pool.Exec(ctx, `DELETE FROM wiki_user_properties WHERE user_id=$1 AND key=$2`, accountID, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) requireUserPropertyWriter(ctx context.Context, ws, actor, accountID string) error {
	if actor == accountID {
		return s.requireMember(ctx, ws, actor)
	}
	admin, err := s.IsAdmin(ctx, ws, actor)
	if err != nil {
		return err
	}
	if !admin {
		return ErrProjectPermission
	}
	return nil
}

// AnonymousWikiUser is the reader who is not signed in. Confluence reports one
// so a client can ask what an unauthenticated visitor would see.
func AnonymousWikiUser() WikiUser {
	return WikiUser{AccountType: "anonymous", DisplayName: "Anonymous", PublicName: "Anonymous", Active: true}
}

// splitAccountIDs reads Confluence's repeated-or-comma-separated accountId.
func SplitAccountIDs(values []string) []string {
	out := []string{}
	for _, raw := range values {
		for _, part := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				out = append(out, trimmed)
			}
		}
	}
	return out
}

// Confluence's groups are the directory groups the organization already has,
// read and written through the wiki surface. A group belongs to a directory
// rather than to a workspace, so creating one puts it in the directory this
// workspace's people come from.

// WikiGroupPage is a page of groups with the total a caller asked for.
type WikiGroupPage struct {
	Groups []WikiGroup
	Total  int
}

// WikiGroups lists the groups, optionally narrowed to a name match.
func (s *Store) WikiGroups(ctx context.Context, ws, actor, query string) ([]WikiGroup, error) {
	if err := s.requireMember(ctx, ws, actor); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT g.id::text,g.name FROM groups g
		WHERE ($1='' OR g.name ILIKE '%' || $1 || '%') ORDER BY g.name, g.id`, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []WikiGroup{}
	for rows.Next() {
		var group WikiGroup
		if err = rows.Scan(&group.ID, &group.Name); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func (s *Store) WikiGroupByID(ctx context.Context, ws, actor, groupID string) (WikiGroup, error) {
	if err := s.requireMember(ctx, ws, actor); err != nil {
		return WikiGroup{}, err
	}
	var group WikiGroup
	err := s.Pool.QueryRow(ctx, `SELECT id::text,name FROM groups WHERE id::text=$1`, groupID).
		Scan(&group.ID, &group.Name)
	return group, err
}

// CreateWikiGroup adds a group to the directory this workspace's people are in.
func (s *Store) CreateWikiGroup(ctx context.Context, ws, actor, name string) (WikiGroup, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 255 {
		return WikiGroup{}, fmt.Errorf("%w: a group name of 1 to 255 characters is required", ErrWikiUserValidation)
	}
	if err := s.requireSiteAdmin(ctx, ws, actor); err != nil {
		return WikiGroup{}, err
	}
	directoryID, err := s.workspaceDirectory(ctx, ws)
	if err != nil {
		return WikiGroup{}, err
	}
	var group WikiGroup
	err = s.Pool.QueryRow(ctx, `INSERT INTO groups(directory_id,name) VALUES($1::uuid,$2)
		RETURNING id::text,name`, directoryID, name).Scan(&group.ID, &group.Name)
	if isUniqueViolation(err) {
		return WikiGroup{}, fmt.Errorf("%w: a group with this name already exists", ErrWikiUserValidation)
	}
	return group, err
}

// workspaceDirectory finds the directory this workspace's people belong to,
// creating the organization's default one when there is none yet.
func (s *Store) workspaceDirectory(ctx context.Context, ws string) (string, error) {
	var directoryID string
	err := s.Pool.QueryRow(ctx, `SELECT d.id::text FROM directories d
		WHERE d.active ORDER BY d.created_at, d.id LIMIT 1`).Scan(&directoryID)
	if err == nil {
		return directoryID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var organizationID string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM organizations ORDER BY created_at, id LIMIT 1`).Scan(&organizationID); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		if err = tx.QueryRow(ctx, `INSERT INTO organizations(name) SELECT name FROM workspaces WHERE id=$1
			RETURNING id::text`, ws).Scan(&organizationID); err != nil {
			return "", err
		}
	}
	if err = tx.QueryRow(ctx, `INSERT INTO directories(organization_id,name) VALUES($1::uuid,'Default directory')
		RETURNING id::text`, organizationID).Scan(&directoryID); err != nil {
		return "", err
	}
	return directoryID, tx.Commit(ctx)
}

// DeleteWikiGroup removes a group and, with it, its memberships.
func (s *Store) DeleteWikiGroup(ctx context.Context, ws, actor, groupID string) error {
	if err := s.requireSiteAdmin(ctx, ws, actor); err != nil {
		return err
	}
	tag, err := s.Pool.Exec(ctx, `DELETE FROM groups WHERE id::text=$1`, groupID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// WikiGroupMembers lists the people in a group.
func (s *Store) WikiGroupMembers(ctx context.Context, ws, actor, groupID string) ([]WikiUser, error) {
	if _, err := s.WikiGroupByID(ctx, ws, actor, groupID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiUserSelect+`
		JOIN group_members gm ON gm.user_id=u.id
		WHERE m.workspace_id=$1 AND gm.group_id::text=$2 ORDER BY u.display_name, u.id`, ws, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []WikiUser{}
	for rows.Next() {
		user, scanErr := scanWikiUser(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

// SetWikiGroupMembership adds or removes one person.
func (s *Store) SetWikiGroupMembership(ctx context.Context, ws, actor, groupID, accountID string, member bool) error {
	if err := s.requireSiteAdmin(ctx, ws, actor); err != nil {
		return err
	}
	if _, err := s.WikiGroupByID(ctx, ws, actor, groupID); err != nil {
		return err
	}
	if _, err := s.WikiUserByAccountID(ctx, ws, actor, accountID); err != nil {
		return err
	}
	if member {
		_, err := s.Pool.Exec(ctx, `INSERT INTO group_members(group_id,user_id) VALUES($1::uuid,$2)
			ON CONFLICT DO NOTHING`, groupID, accountID)
		return err
	}
	tag, err := s.Pool.Exec(ctx, `DELETE FROM group_members WHERE group_id::text=$1 AND user_id=$2`, groupID, accountID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) requireSiteAdmin(ctx context.Context, ws, actor string) error {
	admin, err := s.IsAdmin(ctx, ws, actor)
	if err != nil {
		return err
	}
	if !admin {
		return ErrProjectPermission
	}
	return nil
}
