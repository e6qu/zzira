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
}

const dashboardAccess = `(d.owner_id=$2 OR d.share_permissions @> '[{"type":"loggedin"}]'::jsonb OR d.edit_permissions @> '[{"type":"loggedin"}]'::jsonb OR EXISTS(SELECT 1 FROM jsonb_array_elements(d.share_permissions || d.edit_permissions) perm WHERE perm->'user'->>'accountId'=$2))`
const dashboardWritable = `(d.owner_id=$2 OR d.edit_permissions @> '[{"type":"loggedin"}]'::jsonb OR EXISTS(SELECT 1 FROM jsonb_array_elements(d.edit_permissions) perm WHERE perm->'user'->>'accountId'=$2))`
const dashboardSelect = `SELECT d.id,d.workspace_id,d.owner_id,u.display_name,d.name,d.description,d.share_permissions,d.edit_permissions,d.layout,d.refresh_ms,EXISTS(SELECT 1 FROM dashboard_favourites f WHERE f.dashboard_id=d.id AND f.user_id=$2),(SELECT count(*) FROM dashboard_favourites f WHERE f.dashboard_id=d.id),` + dashboardWritable + ` FROM dashboards d JOIN users u ON u.id=d.owner_id WHERE d.workspace_id=$1 AND NOT d.deleted AND EXISTS(SELECT 1 FROM memberships WHERE workspace_id=$1 AND user_id=$2) AND ` + dashboardAccess

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
func lockDashboard(ctx context.Context, tx pgx.Tx, ws, user, id string, ownerOnly bool) (*models.Dashboard, error) {
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
				if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.workspace_id=$1 AND m.user_id=$2 AND u.active)`, ws, perm.User.AccountID).Scan(&exists); err != nil {
					return err
				}
				if !exists {
					return fmt.Errorf("%w: select a member of this workspace", ErrDashboardValidation)
				}
			default:
				return fmt.Errorf("%w: only user and authenticated sharing are supported", ErrDashboardValidation)
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
