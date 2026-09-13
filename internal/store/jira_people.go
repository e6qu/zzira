package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// People as the Jira API sees them within one site: everyone who belongs to it,
// their groups, the properties apps store against them, their preferences and
// the issue table columns they chose.

var (
	ErrPeopleValidation = errors.New("invalid people request")
	ErrPeopleNotFound   = errors.New("person or group not found")
)

const siteUserColumns = `u.id, u.email, u.display_name, COALESCE(u.time_zone,''), u.active`

// SiteUsers lists everyone who belongs to the site, including inactive people
// and app accounts, which is what Jira's list of all users returns.
func (s *Store) SiteUsers(ctx context.Context, workspaceID string) ([]*models.User, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT `+siteUserColumns+`
		FROM memberships m JOIN users u ON u.id=m.user_id
		WHERE m.workspace_id=$1 ORDER BY u.display_name, u.id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSiteUsers(rows)
}

func scanSiteUsers(rows pgx.Rows) ([]*models.User, error) {
	out := []*models.User{}
	for rows.Next() {
		u := &models.User{}
		if err := rows.Scan(&u.ID, &u.Email, &u.DisplayName, &u.TimeZone, &u.Active); err != nil {
			return nil, err
		}
		u.AccountType = accountTypeOf(u.ID)
		out = append(out, u)
	}
	return out, rows.Err()
}

// accountTypeOf reports what kind of account an id is. App principals are
// created with an app_principal_ id.
func accountTypeOf(id string) string {
	if strings.HasPrefix(id, "app_") {
		return "app"
	}
	return "atlassian"
}

// SiteUser returns one person in the site, active or not.
func (s *Store) SiteUser(ctx context.Context, workspaceID, accountID string) (*models.User, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT `+siteUserColumns+`
		FROM memberships m JOIN users u ON u.id=m.user_id
		WHERE m.workspace_id=$1 AND u.id=$2`, workspaceID, strings.TrimSpace(accountID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users, err := scanSiteUsers(rows)
	if err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return nil, ErrPeopleNotFound
	}
	return users[0], nil
}

// SiteUsersByIDs returns the named people in the order given, skipping ids that
// are not people in the site.
func (s *Store) SiteUsersByIDs(ctx context.Context, workspaceID string, accountIDs []string) ([]*models.User, error) {
	if len(accountIDs) == 0 {
		return []*models.User{}, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT `+siteUserColumns+`
		FROM memberships m JOIN users u ON u.id=m.user_id
		WHERE m.workspace_id=$1 AND u.id = ANY($2)`, workspaceID, accountIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found, err := scanSiteUsers(rows)
	if err != nil {
		return nil, err
	}
	byID := map[string]*models.User{}
	for _, u := range found {
		byID[u.ID] = u
	}
	out := []*models.User{}
	seen := map[string]bool{}
	for _, id := range accountIDs {
		if u, ok := byID[id]; ok && !seen[id] {
			out = append(out, u)
			seen[id] = true
		}
	}
	return out, nil
}

// RemoveSiteUser takes a person out of the site's user base. Their Atlassian
// account remains; only their access to this site goes, as Jira documents.
func (s *Store) RemoveSiteUser(ctx context.Context, workspaceID, actorID, accountID string) error {
	if accountID == actorID {
		return fmt.Errorf("%w: you cannot remove yourself", ErrPeopleValidation)
	}
	if _, err := s.SiteUser(ctx, workspaceID, accountID); err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, statement := range []string{
		`DELETE FROM memberships WHERE workspace_id=$1 AND user_id=$2`,
		`DELETE FROM jira_user_properties WHERE workspace_id=$1 AND user_id=$2`,
		`DELETE FROM jira_user_preferences WHERE workspace_id=$1 AND user_id=$2`,
		`DELETE FROM jira_user_columns WHERE workspace_id=$1 AND user_id=$2`,
	} {
		if _, err = tx.Exec(ctx, statement, workspaceID, accountID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ---- Groups ----

// SiteGroup is a group as Jira reports it.
type SiteGroup struct {
	ID   string
	Name string
}

// SiteGroups lists the groups of the directory the site's people belong to.
func (s *Store) SiteGroups(ctx context.Context, workspaceID string) ([]SiteGroup, error) {
	rows, err := s.Pool.Query(ctx, `SELECT g.id::text, g.name FROM groups g
		JOIN directories d ON d.id=g.directory_id
		JOIN sites si ON si.organization_id=d.organization_id
		WHERE si.workspace_id=$1 AND d.active ORDER BY g.name, g.id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SiteGroup{}
	for rows.Next() {
		var g SiteGroup
		if err = rows.Scan(&g.ID, &g.Name); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SiteGroupByIDOrName finds a group by its id or, failing that, its exact name.
// Jira accepts either, and prefers the id when both are sent.
func (s *Store) SiteGroupByIDOrName(ctx context.Context, workspaceID, groupID, groupName string) (SiteGroup, error) {
	if strings.TrimSpace(groupID) == "" && strings.TrimSpace(groupName) == "" {
		return SiteGroup{}, fmt.Errorf("%w: groupId or groupname is required", ErrPeopleValidation)
	}
	groups, err := s.SiteGroups(ctx, workspaceID)
	if err != nil {
		return SiteGroup{}, err
	}
	for _, g := range groups {
		if groupID != "" && g.ID == strings.TrimSpace(groupID) {
			return g, nil
		}
	}
	if groupID == "" {
		for _, g := range groups {
			if g.Name == groupName {
				return g, nil
			}
		}
	}
	return SiteGroup{}, ErrPeopleNotFound
}

// SiteGroupMembers lists a group's people in the site, active only unless asked.
func (s *Store) SiteGroupMembers(ctx context.Context, workspaceID, groupID string, includeInactive bool) ([]*models.User, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT `+siteUserColumns+`
		FROM group_members gm JOIN users u ON u.id=gm.user_id
		JOIN memberships m ON m.user_id=u.id AND m.workspace_id=$1
		WHERE gm.group_id::text=$2 AND ($3 OR u.active)
		ORDER BY u.display_name, u.id`, workspaceID, groupID, includeInactive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSiteUsers(rows)
}

// UserGroups lists the groups a person belongs to.
func (s *Store) UserGroups(ctx context.Context, workspaceID, accountID string) ([]SiteGroup, error) {
	if _, err := s.SiteUser(ctx, workspaceID, accountID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT g.id::text, g.name FROM groups g
		JOIN group_members gm ON gm.group_id=g.id
		JOIN directories d ON d.id=g.directory_id
		JOIN sites si ON si.organization_id=d.organization_id
		WHERE si.workspace_id=$1 AND gm.user_id=$2 ORDER BY g.name, g.id`, workspaceID, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SiteGroup{}
	for rows.Next() {
		var g SiteGroup
		if err = rows.Scan(&g.ID, &g.Name); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ---- User properties ----

var userPropertyKey = regexp.MustCompile(`^.{1,255}$`)

func (s *Store) UserPropertyKeys(ctx context.Context, workspaceID, accountID string) ([]string, error) {
	if _, err := s.SiteUser(ctx, workspaceID, accountID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT key FROM jira_user_properties WHERE workspace_id=$1 AND user_id=$2 ORDER BY key`, workspaceID, accountID)
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

func (s *Store) UserProperty(ctx context.Context, workspaceID, accountID, key string) (json.RawMessage, error) {
	if _, err := s.SiteUser(ctx, workspaceID, accountID); err != nil {
		return nil, err
	}
	var value json.RawMessage
	err := s.Pool.QueryRow(ctx, `SELECT value FROM jira_user_properties WHERE workspace_id=$1 AND user_id=$2 AND key=$3`, workspaceID, accountID, key).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPeopleNotFound
	}
	return value, err
}

// SetUserProperty stores a value against a person and reports whether the key
// is new, since Jira answers 201 for a new property and 200 for a replaced one.
func (s *Store) SetUserProperty(ctx context.Context, workspaceID, accountID, key string, value json.RawMessage) (bool, error) {
	if !userPropertyKey.MatchString(key) {
		return false, fmt.Errorf("%w: a property key of 1 to 255 characters is required", ErrPeopleValidation)
	}
	trimmed := strings.TrimSpace(string(value))
	if trimmed == "" || !json.Valid(value) {
		return false, fmt.Errorf("%w: the property value must be valid, non-empty JSON", ErrPeopleValidation)
	}
	if len(trimmed) > 32768 {
		return false, fmt.Errorf("%w: the property value must be at most 32768 characters", ErrPeopleValidation)
	}
	if _, err := s.SiteUser(ctx, workspaceID, accountID); err != nil {
		return false, err
	}
	var created bool
	err := s.Pool.QueryRow(ctx, `INSERT INTO jira_user_properties(workspace_id,user_id,key,value) VALUES($1,$2,$3,$4::jsonb)
		ON CONFLICT (workspace_id,user_id,key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()
		RETURNING (xmax = 0)`, workspaceID, accountID, key, value).Scan(&created)
	return created, err
}

func (s *Store) DeleteUserProperty(ctx context.Context, workspaceID, accountID, key string) error {
	if _, err := s.SiteUser(ctx, workspaceID, accountID); err != nil {
		return err
	}
	tag, err := s.Pool.Exec(ctx, `DELETE FROM jira_user_properties WHERE workspace_id=$1 AND user_id=$2 AND key=$3`, workspaceID, accountID, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrPeopleNotFound
	}
	return nil
}

// UserPropertyValues returns every property value of the given key across the
// site's people, for structured user queries that match on a property.
func (s *Store) UserPropertyValues(ctx context.Context, workspaceID, key string) (map[string]json.RawMessage, error) {
	rows, err := s.Pool.Query(ctx, `SELECT user_id, value FROM jira_user_properties WHERE workspace_id=$1 AND key=$2`, workspaceID, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var id string
		var value json.RawMessage
		if err = rows.Scan(&id, &value); err != nil {
			return nil, err
		}
		out[id] = value
	}
	return out, rows.Err()
}

// ---- Preferences ----

// UserPreferenceLocaleKey is where a person's locale is kept.
const UserPreferenceLocaleKey = "jira.user.locale"

func validPreferenceKey(key string) error {
	if strings.TrimSpace(key) == "" || len(key) > 255 {
		return fmt.Errorf("%w: a preference key is required", ErrPeopleNotFound)
	}
	return nil
}

func (s *Store) UserPreference(ctx context.Context, workspaceID, accountID, key string) (string, error) {
	if err := validPreferenceKey(key); err != nil {
		return "", err
	}
	var value string
	err := s.Pool.QueryRow(ctx, `SELECT value FROM jira_user_preferences WHERE workspace_id=$1 AND user_id=$2 AND key=$3`, workspaceID, accountID, key).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrPeopleNotFound
	}
	return value, err
}

func (s *Store) SetUserPreference(ctx context.Context, workspaceID, accountID, key, value string) error {
	if err := validPreferenceKey(key); err != nil {
		return err
	}
	if len(value) > 255 {
		return fmt.Errorf("%w: a preference value of at most 255 characters is required", ErrPeopleNotFound)
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO jira_user_preferences(workspace_id,user_id,key,value) VALUES($1,$2,$3,$4)
		ON CONFLICT (workspace_id,user_id,key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`, workspaceID, accountID, key, value)
	return err
}

func (s *Store) DeleteUserPreference(ctx context.Context, workspaceID, accountID, key string) error {
	if err := validPreferenceKey(key); err != nil {
		return err
	}
	tag, err := s.Pool.Exec(ctx, `DELETE FROM jira_user_preferences WHERE workspace_id=$1 AND user_id=$2 AND key=$3`, workspaceID, accountID, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrPeopleNotFound
	}
	return nil
}

// ---- Issue table columns ----

var customFieldColumn = regexp.MustCompile(`^customfield_\d+$`)

// systemColumns are the issue table columns Jira offers besides custom fields.
var systemColumns = map[string]bool{
	"issuetype": true, "issuekey": true, "summary": true, "assignee": true, "reporter": true,
	"priority": true, "status": true, "resolution": true, "created": true, "updated": true,
	"duedate": true, "labels": true, "components": true, "fixVersions": true, "versions": true,
	"resolutiondate": true, "creator": true, "project": true, "parent": true, "environment": true,
	"description": true, "security": true, "watches": true, "votes": true, "lastViewed": true,
	"timeestimate": true, "timeoriginalestimate": true, "timespent": true, "aggregatetimespent": true,
	"aggregatetimeestimate": true, "aggregatetimeoriginalestimate": true, "aggregateprogress": true,
	"progress": true, "workratio": true, "subtasks": true, "statuscategorychangedate": true,
}

// ValidIssueTableColumn reports whether an id names a column the issue table offers.
func ValidIssueTableColumn(id string) bool {
	return systemColumns[id] || customFieldColumn.MatchString(id)
}

// UserColumns returns the columns a person chose, or the site's default columns
// when they have chosen none.
func (s *Store) UserColumns(ctx context.Context, workspaceID, accountID string) ([]string, error) {
	if _, err := s.SiteUser(ctx, workspaceID, accountID); err != nil {
		return nil, err
	}
	var columns []string
	err := s.Pool.QueryRow(ctx, `SELECT columns FROM jira_user_columns WHERE workspace_id=$1 AND user_id=$2`, workspaceID, accountID).Scan(&columns)
	if err == nil {
		return columns, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	cfg, err := s.JiraSiteConfiguration(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), cfg.NavigatorColumns...), nil
}

// SetUserColumns stores a person's columns. Sending none removes every column.
func (s *Store) SetUserColumns(ctx context.Context, workspaceID, accountID string, columns []string) error {
	if _, err := s.SiteUser(ctx, workspaceID, accountID); err != nil {
		return err
	}
	seen := map[string]bool{}
	clean := []string{}
	for _, column := range columns {
		column = strings.TrimSpace(column)
		if !ValidIssueTableColumn(column) {
			return fmt.Errorf("%w: %q is not an issue table column", ErrPeopleValidation, column)
		}
		if !seen[column] {
			seen[column] = true
			clean = append(clean, column)
		}
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO jira_user_columns(workspace_id,user_id,columns) VALUES($1,$2,$3)
		ON CONFLICT (workspace_id,user_id) DO UPDATE SET columns=EXCLUDED.columns, updated_at=now()`, workspaceID, accountID, clean)
	return err
}

// ResetUserColumns returns a person to the site's default columns.
func (s *Store) ResetUserColumns(ctx context.Context, workspaceID, accountID string) error {
	if _, err := s.SiteUser(ctx, workspaceID, accountID); err != nil {
		return err
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM jira_user_columns WHERE workspace_id=$1 AND user_id=$2`, workspaceID, accountID)
	return err
}
