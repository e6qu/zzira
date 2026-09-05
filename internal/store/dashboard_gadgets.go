package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

type dashboardQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func dashboardGadgets(ctx context.Context, q dashboardQuerier, id string) ([]models.DashboardGadget, error) {
	rows, err := q.Query(ctx, `SELECT id,module_key,title,color,col,row FROM dashboard_gadgets WHERE dashboard_id=$1 ORDER BY col,row,id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.DashboardGadget{}
	for rows.Next() {
		var g models.DashboardGadget
		if err = rows.Scan(&g.ID, &g.ModuleKey, &g.Title, &g.Color, &g.Position.Column, &g.Position.Row); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func (s *Store) DashboardGadgets(ctx context.Context, ws, user, id string) ([]models.DashboardGadget, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = scanDashboard(tx.QueryRow(ctx, dashboardSelect+` AND d.id=$3 FOR SHARE OF d`, ws, user, id)); err != nil {
		return nil, err
	}
	return dashboardGadgets(ctx, tx, id)
}

type GadgetUpdate struct {
	ModuleKey        string                 `json:"moduleKey"`
	URI              string                 `json:"uri"`
	IgnoreValidation bool                   `json:"ignoreUriAndModuleKeyValidation"`
	Title            *string                `json:"title"`
	Color            *string                `json:"color"`
	Position         *models.GadgetPosition `json:"position"`
}

func (s *Store) SaveDashboardGadget(ctx context.Context, ws, user, id string, gid int64, in GadgetUpdate) (*models.DashboardGadget, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	d, err := lockDashboard(ctx, tx, ws, user, id, false)
	if err != nil {
		return nil, err
	}
	all, err := dashboardGadgets(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	g := models.DashboardGadget{ModuleKey: in.ModuleKey, Color: "blue"}
	found := gid == 0
	for _, old := range all {
		if old.ID == gid {
			g = old
			found = true
		}
	}
	if !found {
		return nil, pgx.ErrNoRows
	}
	if in.URI != "" || in.IgnoreValidation {
		return nil, fmt.Errorf("%w: remote gadgets are not supported", ErrDashboardValidation)
	}
	if gid == 0 {
		if len(all) >= 20 {
			return nil, fmt.Errorf("%w: dashboard limit is 20 gadgets", ErrDashboardValidation)
		}
		known := false
		for _, def := range models.GadgetCatalog() {
			if def.ModuleKey == g.ModuleKey {
				known = true
				g.Title = def.Title
			}
		}
		if !known {
			return nil, fmt.Errorf("%w: unsupported gadget moduleKey", ErrDashboardValidation)
		}
	} else if in.ModuleKey != "" {
		return nil, ErrDashboardValidation
	}
	if in.Title != nil {
		g.Title = strings.TrimSpace(*in.Title)
	}
	if in.Color != nil {
		g.Color = *in.Color
	}
	if in.Position != nil {
		g.Position = *in.Position
	}
	if g.Title == "" || utf8.RuneCountInString(g.Title) > 255 {
		return nil, ErrDashboardValidation
	}
	switch g.Color {
	case "blue", "red", "yellow", "green", "cyan", "purple", "gray", "white":
	default:
		return nil, ErrDashboardValidation
	}
	if g.Position.Column < 0 || g.Position.Column >= d.Columns() || g.Position.Row < 0 {
		return nil, fmt.Errorf("%w: gadget position is outside the dashboard layout", ErrDashboardValidation)
	}
	// Remove the moving gadget, compact every column, then insert at the requested row.
	columns := make([][]models.DashboardGadget, d.Columns())
	for _, old := range all {
		if old.ID != gid {
			columns[old.Position.Column] = append(columns[old.Position.Column], old)
		}
	}
	col := columns[g.Position.Column]
	if g.Position.Row > len(col) {
		g.Position.Row = len(col)
	}
	if gid == 0 {
		err = tx.QueryRow(ctx, `INSERT INTO dashboard_gadgets(dashboard_id,module_key,title,color,col,row) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, id, g.ModuleKey, g.Title, g.Color, g.Position.Column, g.Position.Row).Scan(&g.ID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE dashboard_gadgets SET title=$2,color=$3 WHERE id=$1`, gid, g.Title, g.Color)
	}
	if err != nil {
		return nil, err
	}
	col = append(col, models.DashboardGadget{})
	copy(col[g.Position.Row+1:], col[g.Position.Row:])
	col[g.Position.Row] = g
	columns[g.Position.Column] = col
	for c, items := range columns {
		for row, item := range items {
			if _, err = tx.Exec(ctx, `UPDATE dashboard_gadgets SET col=$2,row=$3 WHERE id=$1`, item.ID, c, row); err != nil {
				return nil, err
			}
		}
	}
	if err = dashboardAction(ctx, tx, ws, user, id, models.OpUpsert); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &g, nil
}
func (s *Store) DeleteDashboardGadget(ctx context.Context, ws, user, id string, gid int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = lockDashboard(ctx, tx, ws, user, id, false); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM dashboard_gadgets WHERE dashboard_id=$1 AND id=$2`, id, gid)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if _, err = tx.Exec(ctx, `WITH ordered AS (SELECT id,row_number() OVER(PARTITION BY col ORDER BY row,id)-1 AS r FROM dashboard_gadgets WHERE dashboard_id=$1) UPDATE dashboard_gadgets g SET row=o.r FROM ordered o WHERE g.id=o.id`, id); err != nil {
		return err
	}
	if err = dashboardAction(ctx, tx, ws, user, id, models.OpUpsert); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func NormalizeGadgetConfig(c *models.GadgetConfig) error {
	if c.GroupBy == "" {
		c.GroupBy = "status"
	}
	if c.Limit == 0 {
		c.Limit = 10
	}
	if len(c.JQL) > 16000 || (c.JQL != "" && c.FilterID != "") || c.Limit < 1 || c.Limit > 50 {
		return fmt.Errorf("%w: choose JQL or a saved filter and a result limit from 1 to 50", ErrDashboardValidation)
	}
	switch c.GroupBy {
	case "status", "priority", "issuetype", "assignee":
	default:
		return fmt.Errorf("%w: unsupported grouping", ErrDashboardValidation)
	}
	if _, err := jql.Parse(c.JQL); strings.TrimSpace(c.JQL) != "" && err != nil {
		return fmt.Errorf("%w: %s", ErrDashboardValidation, err)
	}
	return nil
}
func (s *Store) DashboardProperties(ctx context.Context, ws, user, id string, gid int64) (map[string]json.RawMessage, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = scanDashboard(tx.QueryRow(ctx, dashboardSelect+` AND d.id=$3 FOR SHARE OF d`, ws, user, id)); err != nil {
		return nil, err
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM dashboard_gadgets WHERE dashboard_id=$1 AND id=$2)`, id, gid).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, pgx.ErrNoRows
	}
	rows, err := tx.Query(ctx, `SELECT key,value FROM dashboard_gadget_properties WHERE gadget_id=$1 ORDER BY key`, gid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var k string
		var v []byte
		if err = rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = json.RawMessage(v)
	}
	return out, rows.Err()
}

// A nil value deletes a property. The bool reports whether a PUT created a key.
func (s *Store) SetDashboardProperty(ctx context.Context, ws, user, id string, gid int64, key string, value json.RawMessage) (bool, error) {
	if key == "" || utf8.RuneCountInString(key) > 255 || (value != nil && (len(value) > 32768 || !json.Valid(value))) {
		return false, ErrDashboardValidation
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = lockDashboard(ctx, tx, ws, user, id, false); err != nil {
		return false, err
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM dashboard_gadgets WHERE dashboard_id=$1 AND id=$2)`, id, gid).Scan(&exists); err != nil {
		return false, err
	}
	if !exists {
		return false, pgx.ErrNoRows
	}
	if value != nil && key == "zzira.config" {
		var c models.GadgetConfig
		dec := json.NewDecoder(strings.NewReader(string(value)))
		dec.DisallowUnknownFields()
		if err = dec.Decode(&c); err != nil || string(value) == "null" {
			return false, ErrDashboardValidation
		}
		if err = NormalizeGadgetConfig(&c); err != nil {
			return false, err
		}
		if c.FilterID != "" {
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM filters WHERE workspace_id=$1 AND id=$2)`, ws, c.FilterID).Scan(&exists); err != nil {
				return false, err
			}
			if !exists {
				return false, fmt.Errorf("%w: saved filter does not exist", ErrDashboardValidation)
			}
		}
		value, err = json.Marshal(c)
		if err != nil {
			return false, err
		}
	}
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM dashboard_gadget_properties WHERE gadget_id=$1 AND key=$2)`, gid, key).Scan(&exists); err != nil {
		return false, err
	}
	if value == nil {
		if !exists {
			return false, pgx.ErrNoRows
		}
		_, err = tx.Exec(ctx, `DELETE FROM dashboard_gadget_properties WHERE gadget_id=$1 AND key=$2`, gid, key)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO dashboard_gadget_properties(gadget_id,key,value) VALUES($1,$2,$3) ON CONFLICT(gadget_id,key) DO UPDATE SET value=EXCLUDED.value`, gid, key, []byte(value))
	}
	if err != nil {
		return false, err
	}
	if err = dashboardAction(ctx, tx, ws, user, id, models.OpUpsert); err != nil {
		return false, err
	}
	return !exists, tx.Commit(ctx)
}
