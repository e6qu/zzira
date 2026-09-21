package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

// SCIM provisioning writes through the same directory operations an
// administrator uses, so a person an identity provider creates is the same
// person the site already knows, with the same audit trail. What SCIM adds is
// the identity provider's own id for each person and group, and the name parts
// a display name cannot hold.

// ErrSCIMNotFound is a resource the directory does not have.
var ErrSCIMNotFound = errors.New("scim resource not found")

// SCIMUser is one provisioned person.
type SCIMUser struct {
	User       *models.User
	ExternalID string
	GivenName  string
	FamilyName string
	Created    time.Time
	Updated    time.Time
	Groups     []SCIMGroupRef
}

// SCIMGroupRef names a group a person belongs to.
type SCIMGroupRef struct {
	ID   string
	Name string
}

// SCIMGroup is one provisioned group with its members.
type SCIMGroup struct {
	Group      *models.Group
	ExternalID string
	Created    time.Time
	Updated    time.Time
	Members    []SCIMGroupMember
}

// SCIMGroupMember is one member of a provisioned group.
type SCIMGroupMember struct {
	UserID, DisplayName string
}

// SCIMDirectory is the directory an identity provider provisions into: the
// organization's directory, which becomes SCIM-managed the first time one
// writes to it.
func (s *Store) SCIMDirectory(ctx context.Context, workspaceID, directoryID string) (string, error) {
	var id string
	err := s.Pool.QueryRow(ctx, `SELECT d.id::text FROM directories d JOIN sites si ON si.organization_id=d.organization_id
		WHERE si.workspace_id=$1 AND d.id::text=$2 AND d.active`, workspaceID, directoryID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrSCIMNotFound
	}
	return id, err
}

// MarkDirectorySCIMManaged records that an identity provider provisions this
// directory, which is what makes the organization SCIM-managed.
func (s *Store) MarkDirectorySCIMManaged(ctx context.Context, directoryID string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE directories SET directory_type='scim' WHERE id::text=$1 AND directory_type<>'scim'`, directoryID)
	return err
}

// DirectorySCIMManaged reports whether an identity provider provisions any of
// the organization's directories.
func (s *Store) DirectorySCIMManaged(ctx context.Context, workspaceID string) (bool, error) {
	var managed bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM directories d JOIN sites si ON si.organization_id=d.organization_id
		WHERE si.workspace_id=$1 AND d.directory_type='scim')`, workspaceID).Scan(&managed)
	return managed, err
}

// A SCIM user is active when their directory membership is: deactivating one
// is how an identity provider takes access away, and it is the directory
// suspension an administrator would apply by hand. An account deactivated for
// its own reasons is not active either.
const scimUserColumns = `u.id,u.email,u.display_name,(u.active AND du.active),COALESCE(su.external_id,''),COALESCE(su.given_name,''),COALESCE(su.family_name,''),
	COALESCE(su.created_at,du.added_at),COALESCE(su.updated_at,du.added_at)`

func scanSCIMUser(row pgx.Row) (SCIMUser, error) {
	entry := SCIMUser{User: &models.User{}}
	err := row.Scan(&entry.User.ID, &entry.User.Email, &entry.User.DisplayName, &entry.User.Active,
		&entry.ExternalID, &entry.GivenName, &entry.FamilyName, &entry.Created, &entry.Updated)
	return entry, err
}

// SCIMUsers lists the directory's people, newest id last, optionally narrowed
// to one the identity provider names.
func (s *Store) SCIMUsers(ctx context.Context, directoryID, userName, externalID string) ([]SCIMUser, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+scimUserColumns+`
		FROM directory_users du
		JOIN users u ON u.id=du.user_id
		LEFT JOIN scim_users su ON su.directory_id=du.directory_id AND su.user_id=du.user_id
		WHERE du.directory_id=$1::uuid
		  AND ($2='' OR lower(u.email)=lower($2))
		  AND ($3='' OR COALESCE(su.external_id,'')=$3)
		ORDER BY u.email,u.id`, directoryID, userName, externalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]SCIMUser, 0)
	for rows.Next() {
		entry, err := scanSCIMUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range users {
		groups, err := s.scimUserGroups(ctx, directoryID, users[index].User.ID)
		if err != nil {
			return nil, err
		}
		users[index].Groups = groups
	}
	return users, nil
}

// SCIMUserByID reads one provisioned person of a directory.
func (s *Store) SCIMUserByID(ctx context.Context, directoryID, userID string) (SCIMUser, error) {
	entry, err := scanSCIMUser(s.Pool.QueryRow(ctx, `SELECT `+scimUserColumns+`
		FROM directory_users du
		JOIN users u ON u.id=du.user_id
		LEFT JOIN scim_users su ON su.directory_id=du.directory_id AND su.user_id=du.user_id
		WHERE du.directory_id=$1::uuid AND du.user_id=$2`, directoryID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return SCIMUser{}, ErrSCIMNotFound
	}
	if err != nil {
		return SCIMUser{}, err
	}
	entry.Groups, err = s.scimUserGroups(ctx, directoryID, userID)
	return entry, err
}

func (s *Store) scimUserGroups(ctx context.Context, directoryID, userID string) ([]SCIMGroupRef, error) {
	rows, err := s.Pool.Query(ctx, `SELECT g.id::text,g.name FROM group_members gm JOIN groups g ON g.id=gm.group_id
		WHERE gm.user_id=$1 AND g.directory_id=$2::uuid ORDER BY lower(g.name),g.id`, userID, directoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]SCIMGroupRef, 0)
	for rows.Next() {
		var ref SCIMGroupRef
		if err := rows.Scan(&ref.ID, &ref.Name); err != nil {
			return nil, err
		}
		groups = append(groups, ref)
	}
	return groups, rows.Err()
}

// SaveSCIMUserIdentity records what the identity provider calls a person.
func (s *Store) SaveSCIMUserIdentity(ctx context.Context, directoryID, userID, externalID, givenName, familyName string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO scim_users(directory_id,user_id,external_id,given_name,family_name)
		VALUES($1::uuid,$2,$3,$4,$5)
		ON CONFLICT(directory_id,user_id) DO UPDATE SET external_id=EXCLUDED.external_id,given_name=EXCLUDED.given_name,family_name=EXCLUDED.family_name,updated_at=now()`,
		directoryID, userID, strings.TrimSpace(externalID), strings.TrimSpace(givenName), strings.TrimSpace(familyName))
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: another person already carries that externalId", ErrAdminConflict)
	}
	return err
}

const scimGroupColumns = `g.id::text,g.name,g.description,COALESCE(sg.external_id,''),COALESCE(sg.created_at,g.created_at),COALESCE(sg.updated_at,g.updated_at)`

func scanSCIMGroup(row pgx.Row) (SCIMGroup, error) {
	entry := SCIMGroup{Group: &models.Group{}}
	err := row.Scan(&entry.Group.ID, &entry.Group.Name, &entry.Group.Description, &entry.ExternalID, &entry.Created, &entry.Updated)
	return entry, err
}

// SCIMGroups lists the directory's groups, optionally narrowed to one the
// identity provider names.
func (s *Store) SCIMGroups(ctx context.Context, directoryID, displayName, externalID string) ([]SCIMGroup, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+scimGroupColumns+`
		FROM groups g LEFT JOIN scim_groups sg ON sg.group_id=g.id
		WHERE g.directory_id=$1::uuid
		  AND ($2='' OR lower(g.name)=lower($2))
		  AND ($3='' OR COALESCE(sg.external_id,'')=$3)
		ORDER BY lower(g.name),g.id`, directoryID, displayName, externalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]SCIMGroup, 0)
	for rows.Next() {
		entry, err := scanSCIMGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range groups {
		members, err := s.scimGroupMembers(ctx, groups[index].Group.ID)
		if err != nil {
			return nil, err
		}
		groups[index].Members = members
	}
	return groups, nil
}

// SCIMGroupByID reads one provisioned group with its members.
func (s *Store) SCIMGroupByID(ctx context.Context, directoryID, groupID string) (SCIMGroup, error) {
	entry, err := scanSCIMGroup(s.Pool.QueryRow(ctx, `SELECT `+scimGroupColumns+`
		FROM groups g LEFT JOIN scim_groups sg ON sg.group_id=g.id
		WHERE g.directory_id=$1::uuid AND g.id::text=$2`, directoryID, groupID))
	if errors.Is(err, pgx.ErrNoRows) {
		return SCIMGroup{}, ErrSCIMNotFound
	}
	if err != nil {
		return SCIMGroup{}, err
	}
	entry.Members, err = s.scimGroupMembers(ctx, groupID)
	return entry, err
}

func (s *Store) scimGroupMembers(ctx context.Context, groupID string) ([]SCIMGroupMember, error) {
	rows, err := s.Pool.Query(ctx, `SELECT u.id,u.display_name FROM group_members gm JOIN users u ON u.id=gm.user_id
		WHERE gm.group_id=$1::uuid ORDER BY u.display_name,u.id`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := make([]SCIMGroupMember, 0)
	for rows.Next() {
		var member SCIMGroupMember
		if err := rows.Scan(&member.UserID, &member.DisplayName); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, rows.Err()
}

// SaveSCIMGroupIdentity records what the identity provider calls a group.
func (s *Store) SaveSCIMGroupIdentity(ctx context.Context, groupID, externalID string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO scim_groups(group_id,external_id) VALUES($1::uuid,$2)
		ON CONFLICT(group_id) DO UPDATE SET external_id=EXCLUDED.external_id,updated_at=now()`, groupID, strings.TrimSpace(externalID))
	return err
}

// RenameDirectoryGroup changes a provisioned group's name, which is what a
// SCIM replace of displayName means.
func (s *Store) RenameDirectoryGroup(ctx context.Context, workspaceID, actorID, directoryID, groupID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 255 {
		return fmt.Errorf("%w: a group name is 1 to 255 characters", ErrAdminValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var organizationID string
	if err := tx.QueryRow(ctx, `SELECT d.organization_id::text FROM directories d JOIN sites si ON si.organization_id=d.organization_id
		WHERE d.id::text=$1 AND si.workspace_id=$2 AND d.active`, directoryID, workspaceID).Scan(&organizationID); err != nil {
		return ErrSCIMNotFound
	}
	command, err := tx.Exec(ctx, `UPDATE groups SET name=$3,updated_at=now() WHERE id::text=$1 AND directory_id=$2::uuid`, groupID, directoryID, name)
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: a group of that name already exists", ErrAdminConflict)
	}
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrSCIMNotFound
	}
	if _, err := tx.Exec(ctx, `INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		VALUES($1::uuid,$2,'group.renamed','group',$3,$4)`, organizationID, actorID, groupID, `{"name":"`+strings.ReplaceAll(name, `"`, `\"`)+`"}`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
