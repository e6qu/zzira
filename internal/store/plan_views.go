package store

import (
	"context"
	"errors"
	"fmt"
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
	rows, err := s.Pool.Query(ctx, `SELECT id,name,group_by,query FROM plan_views
		WHERE workspace_id=$1 AND plan_id=$2 ORDER BY lower(name),id`, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	views := []PlanView{}
	for rows.Next() {
		var view PlanView
		if err := rows.Scan(&view.ID, &view.Name, &view.GroupBy, &view.Query); err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, rows.Err()
}

// PlanViewByID is one saved view of a plan.
func (s *Store) PlanViewByID(ctx context.Context, workspaceID string, planID, viewID int64) (*PlanView, error) {
	view := &PlanView{}
	err := s.Pool.QueryRow(ctx, `SELECT id,name,group_by,query FROM plan_views
		WHERE workspace_id=$1 AND plan_id=$2 AND id=$3`, workspaceID, planID, viewID).Scan(&view.ID, &view.Name, &view.GroupBy, &view.Query)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("that view is not in this plan")
	}
	return view, err
}

// SavePlanView keeps a view under its name, replacing one of the same name so
// that saving twice does not leave two.
func (s *Store) SavePlanView(ctx context.Context, workspaceID, actorID string, planID int64, view PlanView) (*PlanView, error) {
	view.Name = strings.TrimSpace(view.Name)
	view.Query = strings.TrimSpace(view.Query)
	if view.Name == "" || len([]rune(view.Name)) > 120 {
		return nil, fmt.Errorf("a view needs a name of at most 120 characters")
	}
	if len([]rune(view.Query)) > 200 {
		return nil, fmt.Errorf("a view's filter is at most 200 characters")
	}
	if !ValidPlanGrouping(view.GroupBy) {
		return nil, fmt.Errorf("%q is not a way to group a plan", view.GroupBy)
	}
	err := s.Pool.QueryRow(ctx, `INSERT INTO plan_views(workspace_id,plan_id,name,group_by,query,created_by)
		VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT(workspace_id,plan_id,lower(name)) DO UPDATE SET group_by=EXCLUDED.group_by,query=EXCLUDED.query
		RETURNING id`, workspaceID, planID, view.Name, view.GroupBy, view.Query, actorID).Scan(&view.ID)
	return &view, err
}

// DeletePlanView removes a saved view.
func (s *Store) DeletePlanView(ctx context.Context, workspaceID string, planID, viewID int64) error {
	result, err := s.Pool.Exec(ctx, `DELETE FROM plan_views WHERE workspace_id=$1 AND plan_id=$2 AND id=$3`, workspaceID, planID, viewID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("that view is not in this plan")
	}
	return nil
}
