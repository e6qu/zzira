package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Avatars for projects, issue types and priorities, and the system avatars
// every site offers. Custom avatars belong to the site and to the item they were
// loaded for; system avatars are a fixed catalogue shared by all.

// Avatar is one avatar as the avatar APIs describe it.
type Avatar struct {
	ID         int64
	OwnerType  string
	OwnerID    string
	IsSystem   bool
	IsSelected bool
	MediaType  string
	FileName   string
}

// systemAvatarIcons is the system catalogue, by owner type. Each entry is the
// avatar's id and the icon that renders it.
var systemAvatarIcons = map[string][]struct {
	ID   int64
	Icon string
}{
	"user":      {{10122, "avatar-default.svg"}},
	"priority":  {{10200, "priorities/highest.svg"}, {10201, "priorities/high.svg"}, {10202, "priorities/medium.svg"}, {10203, "priorities/low.svg"}, {10204, "priorities/lowest.svg"}},
	"issuetype": {{10300, "issuetype-epic.svg"}, {10301, "issuetype-story.svg"}, {10302, "issuetype-task.svg"}, {10303, "issuetype-subtask.svg"}, {10304, "issuetype-bug.svg"}},
	"project":   {{10400, "avatar-default.svg"}},
}

// SystemAvatars lists the system avatars for an owner type.
func SystemAvatars(ownerType string) ([]Avatar, error) {
	icons, ok := systemAvatarIcons[ownerType]
	if !ok {
		return nil, fmt.Errorf("%w: the avatar type is invalid", ErrPeopleNotFound)
	}
	out := make([]Avatar, 0, len(icons))
	for _, icon := range icons {
		out = append(out, Avatar{ID: icon.ID, OwnerType: ownerType, IsSystem: true, MediaType: "image/svg+xml", FileName: icon.Icon})
	}
	return out, nil
}

// SystemAvatarIcon returns the static icon a system avatar is drawn from.
func SystemAvatarIcon(ownerType string, id int64) (string, bool) {
	for _, icon := range systemAvatarIcons[ownerType] {
		if icon.ID == id {
			return icon.Icon, true
		}
	}
	return "", false
}

// DefaultSystemAvatar is the avatar an owner type shows when nothing is selected.
func DefaultSystemAvatar(ownerType string) (int64, string) {
	icons := systemAvatarIcons[ownerType]
	if len(icons) == 0 {
		return 0, ""
	}
	return icons[0].ID, icons[0].Icon
}

// resolveAvatarOwner finds the item an avatar belongs to in the site and returns
// its internal id and the avatar it currently shows.
func (s *Store) resolveAvatarOwner(ctx context.Context, workspaceID, ownerType, ownerID string) (string, int64, error) {
	switch ownerType {
	case "issuetype":
		t, err := s.IssueTypeInWorkspace(ctx, workspaceID, ownerID)
		if err != nil {
			return "", 0, fmt.Errorf("%w: the issue type was not found", ErrPeopleNotFound)
		}
		return t.ID, t.AvatarID, nil
	case "priority":
		p, err := s.PriorityInWorkspace(ctx, workspaceID, ownerID)
		if err != nil {
			return "", 0, fmt.Errorf("%w: the priority was not found", ErrPeopleNotFound)
		}
		return p.ID, p.AvatarID, nil
	case "project":
		var id string
		var avatar *int64
		err := s.Pool.QueryRow(ctx, `SELECT id, avatar_id FROM projects WHERE workspace_id=$1 AND (id=$2 OR upper(key)=upper($2)) AND lifecycle_state='ACTIVE'`,
			workspaceID, ownerID).Scan(&id, &avatar)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, fmt.Errorf("%w: the project was not found", ErrPeopleNotFound)
		}
		if err != nil {
			return "", 0, err
		}
		if avatar == nil {
			return id, 0, nil
		}
		return id, *avatar, nil
	default:
		return "", 0, fmt.Errorf("%w: the avatar type is invalid", ErrPeopleNotFound)
	}
}

// OwnerAvatars lists the system and custom avatars for an item, marking the one
// it shows.
func (s *Store) OwnerAvatars(ctx context.Context, workspaceID, ownerType, ownerID string) ([]Avatar, []Avatar, error) {
	internalID, selected, err := s.resolveAvatarOwner(ctx, workspaceID, ownerType, ownerID)
	if err != nil {
		return nil, nil, err
	}
	system, err := SystemAvatars(ownerType)
	if err != nil {
		return nil, nil, err
	}
	if selected == 0 {
		selected, _ = DefaultSystemAvatar(ownerType)
	}
	for i := range system {
		system[i].OwnerID = internalID
		system[i].IsSelected = system[i].ID == selected
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, media_type FROM universal_avatars WHERE workspace_id=$1 AND owner_type=$2 AND owner_id=$3 ORDER BY id`,
		workspaceID, ownerType, internalID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	custom := []Avatar{}
	for rows.Next() {
		a := Avatar{OwnerType: ownerType, OwnerID: internalID}
		if err = rows.Scan(&a.ID, &a.MediaType); err != nil {
			return nil, nil, err
		}
		a.IsSelected = a.ID == selected
		custom = append(custom, a)
	}
	return system, custom, rows.Err()
}

// StoreAvatar loads a custom avatar for an item. It is stored, not selected;
// selecting it is a separate change, as in Jira.
func (s *Store) StoreAvatar(ctx context.Context, workspaceID, ownerType, ownerID, mediaType string, data []byte) (Avatar, error) {
	if mediaType == "image/jpg" {
		mediaType = "image/jpeg"
	}
	switch mediaType {
	case "image/png", "image/jpeg", "image/gif":
	default:
		return Avatar{}, fmt.Errorf("%w: the image type is unsupported; use JPEG, GIF or PNG", ErrPeopleValidation)
	}
	if len(data) == 0 || len(data) > 1<<20 {
		return Avatar{}, fmt.Errorf("%w: an image of at most 1 MiB is required", ErrPeopleValidation)
	}
	internalID, _, err := s.resolveAvatarOwner(ctx, workspaceID, ownerType, ownerID)
	if err != nil {
		return Avatar{}, err
	}
	a := Avatar{OwnerType: ownerType, OwnerID: internalID, MediaType: mediaType}
	err = s.Pool.QueryRow(ctx, `INSERT INTO universal_avatars(workspace_id,owner_type,owner_id,media_type,data) VALUES($1,$2,$3,$4,$5) RETURNING id`,
		workspaceID, ownerType, internalID, mediaType, data).Scan(&a.ID)
	return a, err
}

// SelectAvatar makes an avatar the one an item shows. It must be a system avatar
// of the item's type or a custom avatar loaded for that item.
func (s *Store) SelectAvatar(ctx context.Context, workspaceID, ownerType, ownerID string, avatarID int64) error {
	internalID, _, err := s.resolveAvatarOwner(ctx, workspaceID, ownerType, ownerID)
	if err != nil {
		return err
	}
	if _, system := SystemAvatarIcon(ownerType, avatarID); !system {
		var exists bool
		if err = s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM universal_avatars WHERE workspace_id=$1 AND owner_type=$2 AND owner_id=$3 AND id=$4)`,
			workspaceID, ownerType, internalID, avatarID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: the avatar was not found", ErrPeopleNotFound)
		}
	}
	switch ownerType {
	case "project":
		_, err = s.Pool.Exec(ctx, `UPDATE projects SET avatar_id=$3 WHERE workspace_id=$1 AND id=$2`, workspaceID, internalID, avatarID)
	case "issuetype":
		_, err = s.UpdateIssueType(ctx, workspaceID, internalID, nil, nil, &avatarID)
	case "priority":
		err = s.UpdatePriority(ctx, workspaceID, internalID, PriorityInput{AvatarID: &avatarID})
	}
	return err
}

// DeleteAvatar removes a custom avatar. System avatars cannot be deleted. An
// item showing the deleted avatar falls back to its default.
func (s *Store) DeleteAvatar(ctx context.Context, workspaceID, ownerType, ownerID string, avatarID int64) error {
	internalID, selected, err := s.resolveAvatarOwner(ctx, workspaceID, ownerType, ownerID)
	if err != nil {
		return err
	}
	if _, system := SystemAvatarIcon(ownerType, avatarID); system {
		return fmt.Errorf("%w: system avatars cannot be deleted", ErrPeopleForbidden)
	}
	tag, err := s.Pool.Exec(ctx, `DELETE FROM universal_avatars WHERE workspace_id=$1 AND owner_type=$2 AND owner_id=$3 AND id=$4`,
		workspaceID, ownerType, internalID, avatarID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: the avatar was not found", ErrPeopleNotFound)
	}
	if selected == avatarID {
		defaultID, _ := DefaultSystemAvatar(ownerType)
		return s.SelectAvatar(ctx, workspaceID, ownerType, internalID, defaultID)
	}
	return nil
}

// AvatarImage returns a custom avatar's image. System avatars are served from
// their static icons by the caller.
func (s *Store) AvatarImage(ctx context.Context, workspaceID, ownerType string, avatarID int64) (string, []byte, error) {
	var mediaType string
	var data []byte
	err := s.Pool.QueryRow(ctx, `SELECT media_type, data FROM universal_avatars WHERE workspace_id=$1 AND owner_type=$2 AND id=$3`,
		workspaceID, ownerType, avatarID).Scan(&mediaType, &data)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, fmt.Errorf("%w: the avatar was not found", ErrPeopleNotFound)
	}
	return mediaType, data, err
}

// OwnerSelectedAvatar returns the avatar an item shows.
func (s *Store) OwnerSelectedAvatar(ctx context.Context, workspaceID, ownerType, ownerID string) (int64, error) {
	_, selected, err := s.resolveAvatarOwner(ctx, workspaceID, ownerType, ownerID)
	if err != nil {
		return 0, err
	}
	if selected == 0 {
		selected, _ = DefaultSystemAvatar(ownerType)
	}
	return selected, nil
}

// ErrPeopleForbidden is a request the caller may not make, such as deleting a
// system avatar.
var ErrPeopleForbidden = errors.New("forbidden")
