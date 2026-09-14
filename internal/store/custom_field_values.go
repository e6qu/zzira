package store

import (
	"context"

	"github.com/e6qu/zzira/internal/models"
)

// CustomFieldValueCatalog holds what custom field values name: options, people
// and groups, so issue responses can describe them as Jira does.
type CustomFieldValueCatalog struct {
	Options map[string]models.CustomFieldOption
	Users   map[string]*models.User
	Groups  map[string]SiteGroup
}

// LoadCustomFieldValueCatalog loads the options, users and groups named by ids.
func (s *Store) LoadCustomFieldValueCatalog(ctx context.Context, workspaceID string, optionIDs, userIDs, groupIDs []string) (CustomFieldValueCatalog, error) {
	catalog := CustomFieldValueCatalog{Options: map[string]models.CustomFieldOption{}, Users: map[string]*models.User{}, Groups: map[string]SiteGroup{}}
	if len(optionIDs) > 0 {
		rows, err := s.Pool.Query(ctx, `SELECT o.id::text,o.context_id::text,o.value,o.disabled,o.position,COALESCE(o.parent_id::text,'')
			FROM custom_field_options o JOIN custom_field_contexts c ON c.id=o.context_id
			WHERE c.workspace_id=$1 AND o.id::text = ANY($2)`, workspaceID, optionIDs)
		if err != nil {
			return catalog, err
		}
		for rows.Next() {
			option, err := scanCustomFieldOption(rows)
			if err != nil {
				rows.Close()
				return catalog, err
			}
			catalog.Options[option.ID] = option
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return catalog, err
		}
	}
	if len(userIDs) > 0 {
		wanted := map[string]bool{}
		for _, id := range userIDs {
			wanted[id] = true
		}
		users, err := s.SiteUsers(ctx, workspaceID)
		if err != nil {
			return catalog, err
		}
		for _, user := range users {
			if wanted[user.ID] {
				catalog.Users[user.ID] = user
			}
		}
	}
	if len(groupIDs) > 0 {
		groups, err := s.SiteGroups(ctx, workspaceID)
		if err != nil {
			return catalog, err
		}
		for _, group := range groups {
			catalog.Groups[group.ID] = group
		}
	}
	return catalog, nil
}
