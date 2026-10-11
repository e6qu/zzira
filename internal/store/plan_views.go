package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// PlanView is a saved way of reading a plan: how the work is grouped, and
// what it is narrowed to.
type PlanView struct {
	ID      int64
	Name    string
	GroupBy string
	Query   string
	// RollUp is whether the work under a parent is added up onto it.
	RollUp        bool
	CustomFilters map[string]string
}

// PlanGroupings are the fields a plan's work can be grouped by, in the order
// the page offers them. The empty grouping is the plan's own order.
var PlanGroupings = []string{"team", "sprint", "project", "status", "assignee"}

// ValidPlanGrouping reports whether a grouping is one the plan offers.
func ValidPlanGrouping(value string) bool {
	if value == "" {
		return true
	}
	for _, grouping := range PlanGroupings {
		if grouping == value {
			return true
		}
	}
	return false
}

// PlanViews are a plan's saved views, by name.
func (s *Store) PlanViews(ctx context.Context, workspaceID string, planID int64) ([]PlanView, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,name,group_by,query,rollup,custom_filters FROM plan_views
		WHERE workspace_id=$1 AND plan_id=$2 ORDER BY lower(name),id`, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	views := []PlanView{}
	for rows.Next() {
		var view PlanView
		var filters []byte
		if err := rows.Scan(&view.ID, &view.Name, &view.GroupBy, &view.Query, &view.RollUp, &filters); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(filters, &view.CustomFilters); err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, rows.Err()
}

// PlanViewByID is one saved view of a plan.
func (s *Store) PlanViewByID(ctx context.Context, workspaceID string, planID, viewID int64) (*PlanView, error) {
	view := &PlanView{}
	var filters []byte
	err := s.Pool.QueryRow(ctx, `SELECT id,name,group_by,query,rollup,custom_filters FROM plan_views
		WHERE workspace_id=$1 AND plan_id=$2 AND id=$3`, workspaceID, planID, viewID).Scan(&view.ID, &view.Name, &view.GroupBy, &view.Query, &view.RollUp, &filters)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("that view is not in this plan")
	}
	if err == nil {
		err = json.Unmarshal(filters, &view.CustomFilters)
	}
	return view, err
}

// SavePlanView keeps a view under its name, replacing one of the same name so
// that saving twice does not leave two.
func (s *Store) SavePlanView(ctx context.Context, workspaceID, actorID string, planID int64, view PlanView) (*PlanView, error) {
	view.Name = strings.TrimSpace(view.Name)
	view.Query = strings.TrimSpace(view.Query)
	if view.Name == "" || len([]rune(view.Name)) > 120 {
		return nil, planInvalid("A view needs a name of at most 120 characters.")
	}
	if len([]rune(view.Query)) > 200 {
		return nil, planInvalid("A view's filter is at most 200 characters.")
	}
	if !ValidPlanGrouping(view.GroupBy) {
		return nil, planInvalid("%q is not a way to group a plan.", view.GroupBy)
	}
	if view.CustomFilters == nil {
		view.CustomFilters = map[string]string{}
	}
	if len(view.CustomFilters) > 100 {
		return nil, planInvalid("A view supports at most 100 custom filters.")
	}
	for id, value := range view.CustomFilters {
		number, err := strconv.ParseInt(id, 10, 64)
		if err != nil || number <= 0 || len(value) > 1024 || !json.Valid([]byte(value)) {
			return nil, planInvalid("Choose custom filter values from the plan.")
		}
		var text string
		if value != "null" && json.Unmarshal([]byte(value), &text) != nil {
			return nil, planInvalid("Choose custom filter values from the plan.")
		}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = activePlanTx(ctx, tx, workspaceID, planID); err != nil {
		return nil, err
	}
	if err = planEditorTx(ctx, tx, workspaceID, actorID, planID); err != nil {
		return nil, err
	}
	for id := range view.CustomFilters {
		var enabled bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plans p CROSS JOIN jsonb_array_elements(p.custom_fields) field WHERE p.workspace_id=$1 AND p.id=$2 AND field->>'customFieldId'=$3 AND field->>'filter'='true')`, workspaceID, planID, id).Scan(&enabled); err != nil {
			return nil, err
		}
		if !enabled {
			return nil, planInvalid("The custom filter is no longer enabled in this plan.")
		}
	}
	filters, err := json.Marshal(view.CustomFilters)
	if err != nil {
		return nil, err
	}
	if err = tx.QueryRow(ctx, `INSERT INTO plan_views(workspace_id,plan_id,name,group_by,query,rollup,created_by,custom_filters)
	 VALUES($1,$2,$3,$4,$5,$6,$7,$8)
	 ON CONFLICT(workspace_id,plan_id,lower(name)) DO UPDATE SET group_by=EXCLUDED.group_by,query=EXCLUDED.query,rollup=EXCLUDED.rollup,custom_filters=EXCLUDED.custom_filters
	 RETURNING id,name`, workspaceID, planID, view.Name, view.GroupBy, view.Query, view.RollUp, actorID, filters).Scan(&view.ID, &view.Name); err != nil {
		return nil, err
	}
	if err = siteConfigurationAudit(ctx, tx, workspaceID, actorID, "plan.view_saved", "plan:"+strconv.FormatInt(planID, 10)+":view:"+strconv.FormatInt(view.ID, 10), view); err != nil {
		return nil, err
	}
	return &view, tx.Commit(ctx)
}

// DeletePlanView removes a saved view with the same edit check as saving one.
func (s *Store) DeletePlanView(ctx context.Context, workspaceID, actorID string, planID, viewID int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = activePlanTx(ctx, tx, workspaceID, planID); err != nil {
		return err
	}
	if err = planEditorTx(ctx, tx, workspaceID, actorID, planID); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `DELETE FROM plan_views WHERE workspace_id=$1 AND plan_id=$2 AND id=$3`, workspaceID, planID, viewID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("that view is not in this plan")
	}
	if err = siteConfigurationAudit(ctx, tx, workspaceID, actorID, "plan.view_deleted", "plan:"+strconv.FormatInt(planID, 10)+":view:"+strconv.FormatInt(viewID, 10), map[string]any{"planId": planID, "viewId": viewID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
