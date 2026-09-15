package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// WallboardSlideshow is the site's wallboard slide show.
type WallboardSlideshow struct {
	DashboardIDs    []string
	IntervalSeconds int
	RandomOrder     bool
}

// DashboardWallboardSlideshow reads the site's slide show, with Jira's
// defaults when nobody has configured it.
func (s *Store) DashboardWallboardSlideshow(ctx context.Context, workspaceID string) (WallboardSlideshow, error) {
	show := WallboardSlideshow{DashboardIDs: []string{}, IntervalSeconds: 30}
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT dashboard_ids,interval_seconds,random_order FROM dashboard_wallboard_slideshows WHERE workspace_id=$1`, workspaceID).Scan(&raw, &show.IntervalSeconds, &show.RandomOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return show, nil
	}
	if err != nil {
		return show, err
	}
	if err := json.Unmarshal(raw, &show.DashboardIDs); err != nil {
		return show, err
	}
	return show, nil
}

// SaveDashboardWallboardSlideshow configures the site's slide show from
// dashboards the person may view.
func (s *Store) SaveDashboardWallboardSlideshow(ctx context.Context, workspaceID, actorID string, show WallboardSlideshow) error {
	if show.IntervalSeconds < 5 || show.IntervalSeconds > 3600 {
		return fmt.Errorf("%w: the slide show interval must be between 5 and 3600 seconds", ErrDashboardValidation)
	}
	if len(show.DashboardIDs) == 0 || len(show.DashboardIDs) > 50 {
		return fmt.Errorf("%w: choose between 1 and 50 dashboards for the slide show", ErrDashboardValidation)
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(show.DashboardIDs))
	for _, id := range show.DashboardIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, err := s.Dashboard(ctx, workspaceID, actorID, id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: choose dashboards you can view", ErrDashboardValidation)
			}
			return err
		}
		ids = append(ids, id)
	}
	encoded, _ := json.Marshal(ids)
	_, err := s.Pool.Exec(ctx, `INSERT INTO dashboard_wallboard_slideshows(workspace_id,dashboard_ids,interval_seconds,random_order,updated_by,updated_at)
		VALUES($1,$2,$3,$4,$5,now())
		ON CONFLICT (workspace_id) DO UPDATE SET dashboard_ids=EXCLUDED.dashboard_ids,interval_seconds=EXCLUDED.interval_seconds,random_order=EXCLUDED.random_order,updated_by=EXCLUDED.updated_by,updated_at=now()`,
		workspaceID, encoded, show.IntervalSeconds, show.RandomOrder, actorID)
	return err
}
