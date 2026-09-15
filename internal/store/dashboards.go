package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

var ErrDashboardValidation = errors.New("invalid dashboard")
var ErrDashboardPermission = errors.New("you do not have permission to edit this dashboard")

type DashboardDetails struct {
	Name             string                  `json:"name"`
	Description      string                  `json:"description"`
	SharePermissions []models.DashboardShare `json:"sharePermissions"`
	EditPermissions  []models.DashboardShare `json:"editPermissions"`
	// NewOwnerID transfers ownership when set, which Jira's bulk edit does.
	// Only the current owner may hand a dashboard on.
	NewOwnerID string `json:"-"`
}

// dashboardShareMatch decides whether the user in $2 is among the people a
// share list names: everyone signed in, a user, a group's members, the people
// who can browse a shared project, or the members of a shared project role.
func dashboardShareMatch(list string) string {
	return `EXISTS(SELECT 1 FROM jsonb_array_elements(` + list + `) perm WHERE perm->>'type'='loggedin'
		OR perm->'user'->>'accountId'=$2
		OR (perm->>'type'='group' AND EXISTS(SELECT 1 FROM group_members gm JOIN groups g ON g.id=gm.group_id JOIN directories dr ON dr.id=g.directory_id WHERE g.id::text=perm->'group'->>'groupId' AND gm.user_id=$2 AND dr.active))
		OR (perm->>'type'='project' AND jira_has_project_permission(d.workspace_id, perm->'project'->>'id', $2::text, NULL, 'BROWSE_PROJECTS'))
		OR (perm->>'type'='projectRole' AND EXISTS(SELECT 1 FROM role_bindings rb WHERE rb.scope_type='project' AND rb.scope_id=perm->'project'->>'id' AND rb.role_key=perm->'role'->>'id'
			AND ((rb.principal_type='user' AND rb.principal_id=$2) OR (rb.principal_type='group' AND EXISTS(SELECT 1 FROM group_members gm JOIN groups g ON g.id=gm.group_id JOIN directories dr ON dr.id=g.directory_id WHERE gm.group_id::text=rb.principal_id AND gm.user_id=$2 AND dr.active))))))`
}

var dashboardAccess = `(d.owner_id=$2 OR ` + dashboardShareMatch("d.share_permissions || d.edit_permissions") + `)`
var dashboardWritable = `(d.owner_id=$2 OR ` + dashboardShareMatch("d.edit_permissions") + `)`
var dashboardSelect = `SELECT d.id,d.workspace_id,d.owner_id,u.display_name,d.name,d.description,d.share_permissions,d.edit_permissions,d.layout,d.refresh_ms,EXISTS(SELECT 1 FROM dashboard_favourites f WHERE f.dashboard_id=d.id AND f.user_id=$2),(SELECT count(*) FROM dashboard_favourites f WHERE f.dashboard_id=d.id),` + dashboardWritable + ` FROM dashboards d JOIN users u ON u.id=d.owner_id WHERE d.workspace_id=$1 AND NOT d.deleted AND EXISTS(SELECT 1 FROM memberships m WHERE m.workspace_id=$1 AND m.user_id=$2 AND EXISTS (SELECT 1 FROM sites si JOIN directories dr ON dr.organization_id=si.organization_id JOIN directory_users du ON du.directory_id=dr.id AND du.user_id=m.user_id WHERE si.workspace_id=m.workspace_id AND dr.active AND du.active)) AND ` + dashboardAccess

func scanDashboard(row pgx.Row) (*models.Dashboard, error) {
	d := &models.Dashboard{}
	var view, edit []byte
	err := row.Scan(&d.ID, &d.WorkspaceID, &d.OwnerID, &d.OwnerName, &d.Name, &d.Description, &view, &edit, &d.Layout, &d.RefreshMS, &d.Favourite, &d.Popularity, &d.Writable)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(view, &d.SharePermissions); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(edit, &d.EditPermissions); err != nil {
		return nil, err
	}
	return d, nil
}
func (s *Store) Dashboard(ctx context.Context, ws, user, id string) (*models.Dashboard, error) {
	return scanDashboard(s.Pool.QueryRow(ctx, dashboardSelect+` AND d.id=$3`, ws, user, id))
}
func (s *Store) Dashboards(ctx context.Context, ws, user string) ([]*models.Dashboard, error) {
	rows, err := s.Pool.Query(ctx, dashboardSelect+` ORDER BY lower(d.name),d.id`, ws, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*models.Dashboard{}
	for rows.Next() {
		d, err := scanDashboard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

type dashboardAdministrationKey struct{}

// WithDashboardAdministration marks a change made with Jira's
// extendAdminPermissions: a site administrator acting on any dashboard.
func WithDashboardAdministration(ctx context.Context) context.Context {
	return context.WithValue(ctx, dashboardAdministrationKey{}, true)
}

func lockDashboard(ctx context.Context, tx pgx.Tx, ws, user, id string, ownerOnly bool) (*models.Dashboard, error) {
	if extended, _ := ctx.Value(dashboardAdministrationKey{}).(bool); extended {
		if err := projectAdmin(ctx, tx, ws, user); err != nil {
			return nil, ErrDashboardPermission
		}
		// Administration reaches dashboards the administrator was not shared.
		unshared := strings.Replace(dashboardSelect, " AND "+dashboardAccess, "", 1)
		return scanDashboard(tx.QueryRow(ctx, unshared+` AND d.id=$3 FOR UPDATE OF d`, ws, user, id))
	}
	d, err := scanDashboard(tx.QueryRow(ctx, dashboardSelect+` AND d.id=$3 FOR UPDATE OF d`, ws, user, id))
	if err != nil {
		return nil, err
	}
	if !d.Writable || (ownerOnly && d.OwnerID != user) {
		return nil, ErrDashboardPermission
	}
	return d, nil
}
func validateDashboardDetails(ctx context.Context, tx pgx.Tx, ws string, in *DashboardDetails) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 255 || len(in.Description) > 16384 {
		return fmt.Errorf("%w: name must contain 1–255 characters and description must be at most 16384 bytes", ErrDashboardValidation)
	}
	for _, list := range []*[]models.DashboardShare{&in.SharePermissions, &in.EditPermissions} {
		if len(*list) > 100 {
			return fmt.Errorf("%w: at most 100 share permissions are supported", ErrDashboardValidation)
		}
		if *list == nil {
			*list = []models.DashboardShare{}
		}
		for i := range *list {
			perm := &(*list)[i]
			perm.ID = int64(i + 1)
			switch perm.Type {
			case "authenticated", "loggedin":
				perm.Type = "loggedin"
				if perm.User != nil {
					return ErrDashboardValidation
				}
			case "user":
				if perm.User == nil || perm.User.AccountID == "" {
					return ErrDashboardValidation
				}
				var exists bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.workspace_id=$1 AND m.user_id=$2 AND u.active AND EXISTS (SELECT 1 FROM sites si JOIN directories d ON d.organization_id=si.organization_id JOIN directory_users du ON du.directory_id=d.id AND du.user_id=m.user_id WHERE si.workspace_id=m.workspace_id AND d.active AND du.active))`, ws, perm.User.AccountID).Scan(&exists); err != nil {
					return err
				}
				if !exists {
					return fmt.Errorf("%w: select a member of this workspace", ErrDashboardValidation)
				}
			case "group":
				if perm.Group == nil || perm.User != nil || perm.Project != nil || perm.Role != nil {
					return fmt.Errorf("%w: a group share names one group", ErrDashboardValidation)
				}
				if err := tx.QueryRow(ctx, `SELECT g.id::text,g.name FROM groups g JOIN directories dr ON dr.id=g.directory_id JOIN sites si ON si.organization_id=dr.organization_id
					WHERE si.workspace_id=$1 AND dr.active AND (g.id::text=$2 OR ($2='' AND g.name=$3)) ORDER BY g.name LIMIT 1`, ws, perm.Group.GroupID, perm.Group.Name).Scan(&perm.Group.GroupID, &perm.Group.Name); err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						return fmt.Errorf("%w: select a group of this site", ErrDashboardValidation)
					}
					return err
				}
			case "project", "projectRole":
				if perm.Project == nil || strings.TrimSpace(perm.Project.ID) == "" || perm.User != nil || perm.Group != nil {
					return fmt.Errorf("%w: a project share names one project", ErrDashboardValidation)
				}
				if err := tx.QueryRow(ctx, `SELECT id,key,name FROM projects WHERE workspace_id=$1 AND lifecycle_state='ACTIVE' AND (id=$2 OR upper(key)=upper($2))`, ws, strings.TrimSpace(perm.Project.ID)).Scan(&perm.Project.ID, &perm.Project.Key, &perm.Project.Name); err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						return fmt.Errorf("%w: select an active project", ErrDashboardValidation)
					}
					return err
				}
				switch {
				case perm.Role != nil:
					perm.Type = "projectRole"
					if err := tx.QueryRow(ctx, `SELECT name FROM project_roles WHERE workspace_id=$1 AND id::text=$2`, ws, string(perm.Role.ID)).Scan(&perm.Role.Name); err != nil {
						if errors.Is(err, pgx.ErrNoRows) {
							return fmt.Errorf("%w: select a project role", ErrDashboardValidation)
						}
						return err
					}
				case perm.Type == "projectRole":
					return fmt.Errorf("%w: a project role share names a role", ErrDashboardValidation)
				}
			default:
				// Jira Cloud no longer shares dashboards publicly.
				return fmt.Errorf("%w: dashboards are shared with users, groups, projects, project roles or everyone signed in", ErrDashboardValidation)
			}
		}
	}
	return nil
}

// Dashboard actions intentionally carry only invalidation metadata, not private
// configuration or rendered results. Catalog materialization is a future slice.
func dashboardAction(ctx context.Context, tx pgx.Tx, ws, user, id, op string) error {
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{"dashboardId": id})
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: "dashboard", EntityID: id, Op: op, SchemaV: models.SchemaVersion, Payload: body, ActorID: user})
}
func (s *Store) SaveDashboard(ctx context.Context, ws, user, id string, in DashboardDetails) (*models.Dashboard, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if id != "" {
		if _, err = lockDashboard(ctx, tx, ws, user, id, true); err != nil {
			return nil, err
		}
	} else {
		var role string
		if err = tx.QueryRow(ctx, `SELECT role FROM memberships WHERE workspace_id=$1 AND user_id=$2 FOR SHARE`, ws, user).Scan(&role); err != nil {
			return nil, err
		}
	}
	if err = validateDashboardDetails(ctx, tx, ws, &in); err != nil {
		return nil, err
	}
	view, err := json.Marshal(in.SharePermissions)
	if err != nil {
		return nil, err
	}
	edit, err := json.Marshal(in.EditPermissions)
	if err != nil {
		return nil, err
	}
	if id == "" {
		if err = tx.QueryRow(ctx, `INSERT INTO dashboards(workspace_id,owner_id,name,description,share_permissions,edit_permissions) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, ws, user, in.Name, in.Description, view, edit).Scan(&id); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO dashboard_favourites(dashboard_id,user_id) VALUES($1,$2)`, id, user); err != nil {
			return nil, err
		}
	} else {
		if _, err = tx.Exec(ctx, `UPDATE dashboards SET name=$2,description=$3,share_permissions=$4,edit_permissions=$5 WHERE id=$1`, id, in.Name, in.Description, view, edit); err != nil {
			return nil, err
		}
		if in.NewOwnerID != "" {
			var owner string
			if err = tx.QueryRow(ctx, `SELECT owner_id FROM dashboards WHERE id=$1`, id).Scan(&owner); err != nil {
				return nil, err
			}
			if owner != user {
				return nil, fmt.Errorf("%w: only the owner can hand a dashboard on", ErrDashboardPermission)
			}
			var active bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships
				WHERE workspace_id=$1 AND user_id=$2)`, ws, in.NewOwnerID).Scan(&active); err != nil {
				return nil, err
			}
			if !active {
				return nil, fmt.Errorf("%w: the new owner is not a member of this workspace", ErrDashboardValidation)
			}
			if _, err = tx.Exec(ctx, `UPDATE dashboards SET owner_id=$2 WHERE id=$1`, id, in.NewOwnerID); err != nil {
				return nil, err
			}
		}
	}
	if err = dashboardAction(ctx, tx, ws, user, id, models.OpUpsert); err != nil {
		return nil, err
	}
	d, err := scanDashboard(tx.QueryRow(ctx, dashboardSelect+` AND d.id=$3`, ws, user, id))
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return d, nil
}
func (s *Store) DeleteDashboard(ctx context.Context, ws, user, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = lockDashboard(ctx, tx, ws, user, id, true); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM dashboard_gadgets WHERE dashboard_id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM dashboard_favourites WHERE dashboard_id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboards SET deleted=true,name='Deleted dashboard',description='' WHERE id=$1`, id); err != nil {
		return err
	}
	if err = dashboardAction(ctx, tx, ws, user, id, models.OpDelete); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) SetDashboardFavourite(ctx context.Context, ws, user, id string, favourite bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = scanDashboard(tx.QueryRow(ctx, dashboardSelect+` AND d.id=$3 FOR SHARE OF d`, ws, user, id)); err != nil {
		return err
	}
	if favourite {
		_, err = tx.Exec(ctx, `INSERT INTO dashboard_favourites(dashboard_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, user)
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM dashboard_favourites WHERE dashboard_id=$1 AND user_id=$2`, id, user)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) DashboardPresentation(ctx context.Context, ws, user, id, layout string, refresh int) error {
	if layout != "A" && layout != "AA" && layout != "AB" && layout != "BA" && layout != "AAA" {
		return ErrDashboardValidation
	}
	if refresh != 0 && refresh != 60000 && refresh != 300000 && refresh != 900000 {
		return ErrDashboardValidation
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = lockDashboard(ctx, tx, ws, user, id, false); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboards SET layout=$2,refresh_ms=$3 WHERE id=$1`, id, layout, refresh); err != nil {
		return err
	}
	// Collapsing columns appends displaced gadgets in stable reading order.
	columns := models.Dashboard{Layout: layout}.Columns()
	if _, err = tx.Exec(ctx, `WITH ordered AS (SELECT id,LEAST(col,$2-1) AS c,row_number() OVER(PARTITION BY LEAST(col,$2-1) ORDER BY col,row,id)-1 AS r FROM dashboard_gadgets WHERE dashboard_id=$1) UPDATE dashboard_gadgets g SET col=o.c,row=o.r FROM ordered o WHERE g.id=o.id`, id, columns); err != nil {
		return err
	}
	if err = dashboardAction(ctx, tx, ws, user, id, models.OpUpsert); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) CopyDashboard(ctx context.Context, ws, user, sourceID string, in DashboardDetails) (*models.Dashboard, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	source, err := scanDashboard(tx.QueryRow(ctx, dashboardSelect+` AND d.id=$3 FOR SHARE OF d`, ws, user, sourceID))
	if err != nil {
		return nil, err
	}
	if err = validateDashboardDetails(ctx, tx, ws, &in); err != nil {
		return nil, err
	}
	view, err := json.Marshal(in.SharePermissions)
	if err != nil {
		return nil, err
	}
	edit, err := json.Marshal(in.EditPermissions)
	if err != nil {
		return nil, err
	}
	var id string
	if err = tx.QueryRow(ctx, `INSERT INTO dashboards(workspace_id,owner_id,name,description,share_permissions,edit_permissions,layout,refresh_ms) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, ws, user, in.Name, in.Description, view, edit, source.Layout, source.RefreshMS).Scan(&id); err != nil {
		return nil, err
	}
	gadgets, err := dashboardGadgets(ctx, tx, sourceID)
	if err != nil {
		return nil, err
	}
	for _, g := range gadgets {
		var gid int64
		if err = tx.QueryRow(ctx, `INSERT INTO dashboard_gadgets(dashboard_id,module_key,title,color,col,row) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, id, g.ModuleKey, g.Title, g.Color, g.Position.Column, g.Position.Row).Scan(&gid); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO dashboard_gadget_properties(gadget_id,key,value) SELECT $1,key,value FROM dashboard_gadget_properties WHERE gadget_id=$2`, gid, g.ID); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO dashboard_favourites(dashboard_id,user_id) VALUES($1,$2)`, id, user); err != nil {
		return nil, err
	}
	if err = dashboardAction(ctx, tx, ws, user, id, models.OpUpsert); err != nil {
		return nil, err
	}
	d, err := scanDashboard(tx.QueryRow(ctx, dashboardSelect+` AND d.id=$3`, ws, user, id))
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return d, nil
}
