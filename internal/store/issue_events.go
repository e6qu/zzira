package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

var (
	ErrIssueEventValidation = errors.New("issue event request is invalid")
	ErrIssueEventNotFound   = errors.New("issue event does not exist")
	ErrIssueEventConflict   = errors.New("issue event is in use")
)

// customIssueEventStart is the first id of an administrator-added event;
// Jira's built-in events keep their fixed ids below it.
const customIssueEventStart = 10000

// IsCustomIssueEvent reports whether an event id names an administrator-added
// event rather than a built-in one.
func IsCustomIssueEvent(id int64) bool { return id >= customIssueEventStart }

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// IssueEvents lists Jira's built-in events followed by the site's custom
// events, in id order.
func (s *Store) IssueEvents(ctx context.Context, workspaceID string) ([]NotificationEventDefinition, error) {
	events := NotificationEvents()
	rows, err := s.Pool.Query(ctx, `SELECT id,name,description FROM issue_events WHERE workspace_id=$1 ORDER BY id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var event NotificationEventDefinition
		if err = rows.Scan(&event.ID, &event.Name, &event.Description); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// IssueEvent finds one built-in or custom event of the site.
func (s *Store) IssueEvent(ctx context.Context, workspaceID string, id int64) (NotificationEventDefinition, bool, error) {
	return issueEvent(ctx, s.Pool, workspaceID, id)
}

func issueEvent(ctx context.Context, q rowQuerier, workspaceID string, id int64) (NotificationEventDefinition, bool, error) {
	if !IsCustomIssueEvent(id) {
		event, ok := NotificationEvent(id)
		return event, ok, nil
	}
	event := NotificationEventDefinition{ID: id}
	err := q.QueryRow(ctx, `SELECT name,description FROM issue_events WHERE workspace_id=$1 AND id=$2`, workspaceID, id).Scan(&event.Name, &event.Description)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotificationEventDefinition{}, false, nil
	}
	return event, err == nil, err
}

func validateIssueEvent(name, description string) (string, string, error) {
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if name == "" || len(name) > 255 {
		return name, description, fmt.Errorf("%w: an event name must contain 1 to 255 characters", ErrIssueEventValidation)
	}
	if len(description) > 1000 {
		return name, description, fmt.Errorf("%w: an event description accepts at most 1000 characters", ErrIssueEventValidation)
	}
	for _, event := range notificationEvents {
		if strings.EqualFold(event.Name, name) {
			return name, description, fmt.Errorf("%w: an event named %q already exists", ErrIssueEventConflict, name)
		}
	}
	return name, description, nil
}

// CreateIssueEvent adds a custom event administrators can map in notification
// schemes and fire from workflow transitions.
func (s *Store) CreateIssueEvent(ctx context.Context, workspaceID, actorID, name, description string) (NotificationEventDefinition, error) {
	name, description, err := validateIssueEvent(name, description)
	if err != nil {
		return NotificationEventDefinition{}, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return NotificationEventDefinition{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	event := NotificationEventDefinition{Name: name, Description: description}
	err = tx.QueryRow(ctx, `INSERT INTO issue_events(workspace_id,name,description) VALUES($1,$2,$3)
		ON CONFLICT (workspace_id, lower(name)) DO NOTHING RETURNING id`, workspaceID, name, description).Scan(&event.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotificationEventDefinition{}, fmt.Errorf("%w: an event named %q already exists", ErrIssueEventConflict, name)
	}
	if err != nil {
		return NotificationEventDefinition{}, err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "issue_event", fmt.Sprint(event.ID), models.OpUpsert, event); err != nil {
		return NotificationEventDefinition{}, err
	}
	return event, tx.Commit(ctx)
}

// UpdateIssueEvent renames or redescribes a custom event.
func (s *Store) UpdateIssueEvent(ctx context.Context, workspaceID, actorID string, id int64, name, description string) error {
	if !IsCustomIssueEvent(id) {
		return fmt.Errorf("%w: built-in events cannot be changed", ErrIssueEventValidation)
	}
	name, description, err := validateIssueEvent(name, description)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var clash bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_events WHERE workspace_id=$1 AND lower(name)=lower($2) AND id<>$3)`, workspaceID, name, id).Scan(&clash); err != nil {
		return err
	}
	if clash {
		return fmt.Errorf("%w: an event named %q already exists", ErrIssueEventConflict, name)
	}
	command, err := tx.Exec(ctx, `UPDATE issue_events SET name=$3,description=$4 WHERE workspace_id=$1 AND id=$2`, workspaceID, id, name, description)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrIssueEventNotFound
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "issue_event", fmt.Sprint(id), models.OpUpsert, NotificationEventDefinition{ID: id, Name: name, Description: description}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteIssueEvent removes a custom event no notification scheme maps and no
// workflow transition, published or draft, fires.
func (s *Store) DeleteIssueEvent(ctx context.Context, workspaceID, actorID string, id int64) error {
	if !IsCustomIssueEvent(id) {
		return fmt.Errorf("%w: built-in events cannot be deleted", ErrIssueEventValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var used bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM notification_scheme_entries WHERE workspace_id=$1 AND event_id=$2)
		OR EXISTS(SELECT 1 FROM workflows w,
		  jsonb_array_elements(COALESCE(w.def->'transitions','[]'::jsonb) || COALESCE(w.draft_def->'transitions','[]'::jsonb)) t
		  WHERE w.workspace_id=$1 AND t->>'customIssueEventId'=$2::text)`, workspaceID, id).Scan(&used); err != nil {
		return err
	}
	if used {
		return fmt.Errorf("%w: a notification scheme or workflow transition uses the event", ErrIssueEventConflict)
	}
	command, err := tx.Exec(ctx, `DELETE FROM issue_events WHERE workspace_id=$1 AND id=$2`, workspaceID, id)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrIssueEventNotFound
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "issue_event", fmt.Sprint(id), models.OpDelete, map[string]any{"id": id}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
