package store

import (
	"context"
	"strings"
)

// Application roles are how Jira names access to its products: Jira Software
// and Jira Service Management. A person has a role when the product grants them
// access, directly or through a group.

// ApplicationRole is one Jira product's access as Jira reports it.
type ApplicationRole struct {
	Key       string
	Name      string
	UserCount int
	Groups    []SiteGroup
}

// jiraApplicationRoles maps the site's products to the role keys Jira uses.
// Confluence is not a Jira application, so it has no role here.
var jiraApplicationRoles = []struct{ productKey, key, name string }{
	{"jira-software", "jira-software", "Jira Software"},
	{"jira-service-management", "jira-servicedesk", "Jira Service Management"},
}

const productAccessRoles = `('atlassian/user','atlassian/admin','atlassian/guest','atlassian/customer',
	'atlassian/contributor','atlassian/basic','atlassian/stakeholder','atlassian/product-user','atlassian/product-admin')`

// ApplicationRoles lists the Jira application roles the site's enabled products
// define, with how many people each grants access to and the groups that grant it.
func (s *Store) ApplicationRoles(ctx context.Context, workspaceID string) ([]ApplicationRole, error) {
	roles := []ApplicationRole{}
	for _, def := range jiraApplicationRoles {
		var productID string
		err := s.Pool.QueryRow(ctx, `SELECT p.id::text FROM products p JOIN sites si ON si.id=p.site_id
			WHERE si.workspace_id=$1 AND p.product_key=$2 AND p.enabled`, workspaceID, def.productKey).Scan(&productID)
		if err != nil {
			continue
		}
		role := ApplicationRole{Key: def.key, Name: def.name, Groups: []SiteGroup{}}
		if err = s.Pool.QueryRow(ctx, `SELECT count(DISTINCT u.id) FROM users u
			JOIN memberships m ON m.user_id=u.id AND m.workspace_id=$1
			WHERE u.active AND EXISTS (
			  SELECT 1 FROM role_bindings rb WHERE rb.scope_type='product' AND rb.scope_id=$2
			    AND rb.role_key IN `+productAccessRoles+`
			    AND (rb.principal_type='user' AND rb.principal_id=u.id
			      OR rb.principal_type='group' AND EXISTS (SELECT 1 FROM group_members gm WHERE gm.group_id::text=rb.principal_id AND gm.user_id=u.id)))`,
			workspaceID, productID).Scan(&role.UserCount); err != nil {
			return nil, err
		}
		rows, err := s.Pool.Query(ctx, `SELECT DISTINCT g.id::text, g.name FROM role_bindings rb
			JOIN groups g ON g.id::text=rb.principal_id
			WHERE rb.scope_type='product' AND rb.scope_id=$1 AND rb.principal_type='group' AND rb.role_key IN `+productAccessRoles+`
			ORDER BY g.name`, productID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var g SiteGroup
			if err = rows.Scan(&g.ID, &g.Name); err != nil {
				rows.Close()
				return nil, err
			}
			role.Groups = append(role.Groups, g)
		}
		rows.Close()
		roles = append(roles, role)
	}
	return roles, nil
}

// ApplicationRole returns one role by key.
func (s *Store) ApplicationRole(ctx context.Context, workspaceID, key string) (ApplicationRole, error) {
	roles, err := s.ApplicationRoles(ctx, workspaceID)
	if err != nil {
		return ApplicationRole{}, err
	}
	for _, role := range roles {
		if strings.EqualFold(role.Key, strings.TrimSpace(key)) {
			return role, nil
		}
	}
	return ApplicationRole{}, ErrPeopleNotFound
}

// UserApplicationRoles lists the roles a person holds.
func (s *Store) UserApplicationRoles(ctx context.Context, workspaceID, accountID string) ([]ApplicationRole, error) {
	out := []ApplicationRole{}
	for _, def := range jiraApplicationRoles {
		var has bool
		err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM products p JOIN sites si ON si.id=p.site_id
			JOIN role_bindings rb ON rb.scope_type='product' AND rb.scope_id=p.id::text AND rb.role_key IN `+productAccessRoles+`
			WHERE si.workspace_id=$1 AND p.product_key=$2 AND p.enabled
			  AND (rb.principal_type='user' AND rb.principal_id=$3
			    OR rb.principal_type='group' AND EXISTS (SELECT 1 FROM group_members gm WHERE gm.group_id::text=rb.principal_id AND gm.user_id=$3)))`,
			workspaceID, def.productKey, accountID).Scan(&has)
		if err != nil {
			return nil, err
		}
		if has {
			out = append(out, ApplicationRole{Key: def.key, Name: def.name})
		}
	}
	return out, nil
}
