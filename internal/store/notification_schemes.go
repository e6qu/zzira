package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

var (
	ErrNotificationSchemeValidation = errors.New("notification scheme request is invalid")
	ErrNotificationSchemeNotFound   = errors.New("notification scheme does not exist")
	ErrNotificationSchemeConflict   = errors.New("notification scheme is in use")
)

type NotificationEventDefinition struct {
	ID          int64
	Name        string
	Description string
}

var notificationEvents = []NotificationEventDefinition{
	{1, "Issue created", "A work item was created."},
	{2, "Issue updated", "A work item's details changed."},
	{3, "Issue assigned", "A work item was assigned to a new user."},
	{4, "Issue resolved", "A work item was resolved."},
	{5, "Issue closed", "A work item was closed."},
	{6, "Issue commented", "A comment was added to a work item."},
	{7, "Issue comment edited", "A work item comment was edited."},
	{8, "Issue reopened", "A work item was reopened."},
	{9, "Issue deleted", "A work item was deleted."},
	{10, "Issue moved", "A work item moved to another project."},
	{11, "Work logged on issue", "Work was logged on a work item."},
	{12, "Work started on issue", "Work started on a work item."},
	{13, "Work stopped on issue", "Work stopped on a work item."},
	{14, "Issue worklog updated", "A work log was updated."},
	{15, "Issue worklog deleted", "A work log was deleted."},
	{16, "Generic event", "A workflow fired the generic event."},
	{17, "Issue comment deleted", "A work item comment was deleted."},
}

func NotificationEvents() []NotificationEventDefinition {
	return append([]NotificationEventDefinition{}, notificationEvents...)
}

func NotificationEvent(id int64) (NotificationEventDefinition, bool) {
	for _, event := range notificationEvents {
		if event.ID == id {
			return event, true
		}
	}
	return NotificationEventDefinition{}, false
}

type NotificationEntryInput struct {
	EventID          int64
	NotificationType string
	Parameter        string
}

const notificationSchemeColumns = `ns.id,ns.workspace_id,ns.name,ns.description,ns.is_default,
	(SELECT count(*)::int FROM project_notification_schemes pns WHERE pns.workspace_id=ns.workspace_id AND pns.scheme_id=ns.id)`

func scanNotificationScheme(row pgx.Row) (*models.NotificationScheme, error) {
	scheme := &models.NotificationScheme{}
	if err := row.Scan(&scheme.ID, &scheme.WorkspaceID, &scheme.Name, &scheme.Description, &scheme.Default, &scheme.ProjectCount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotificationSchemeNotFound
		}
		return nil, err
	}
	return scheme, nil
}

func scanNotificationEntry(row pgx.Row) (models.NotificationSchemeEntry, error) {
	entry := models.NotificationSchemeEntry{}
	err := row.Scan(&entry.ID, &entry.EventID, &entry.NotificationType, &entry.Parameter, &entry.Recipient)
	return entry, err
}

func notificationSchemeEntriesTx(ctx context.Context, tx pgx.Tx, workspaceID string, schemeID int64) ([]models.NotificationSchemeEvent, error) {
	rows, err := tx.Query(ctx, `SELECT id,event_id,notification_type,COALESCE(parameter,''),COALESCE(recipient,'') FROM notification_scheme_entries WHERE workspace_id=$1 AND scheme_id=$2 ORDER BY event_id,id`, workspaceID, schemeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []models.NotificationSchemeEvent{}
	for rows.Next() {
		entry, scanErr := scanNotificationEntry(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		if len(events) == 0 || events[len(events)-1].EventID != entry.EventID {
			events = append(events, models.NotificationSchemeEvent{EventID: entry.EventID, Notifications: []models.NotificationSchemeEntry{}})
		}
		events[len(events)-1].Notifications = append(events[len(events)-1].Notifications, entry)
	}
	return events, rows.Err()
}

func (s *Store) NotificationSchemes(ctx context.Context, workspaceID string, expand bool) ([]*models.NotificationScheme, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+notificationSchemeColumns+` FROM notification_schemes ns WHERE workspace_id=$1 ORDER BY is_default DESC,lower(name),id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	schemes := []*models.NotificationScheme{}
	for rows.Next() {
		scheme, scanErr := scanNotificationScheme(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		schemes = append(schemes, scheme)
	}
	if err = rows.Err(); err != nil || !expand {
		return schemes, err
	}
	for _, scheme := range schemes {
		scheme.Events, err = s.NotificationSchemeEvents(ctx, workspaceID, scheme.ID)
		if err != nil {
			return nil, err
		}
	}
	return schemes, nil
}

func (s *Store) NotificationScheme(ctx context.Context, workspaceID string, schemeID int64, expand bool) (*models.NotificationScheme, error) {
	scheme, err := scanNotificationScheme(s.Pool.QueryRow(ctx, `SELECT `+notificationSchemeColumns+` FROM notification_schemes ns WHERE workspace_id=$1 AND id=$2`, workspaceID, schemeID))
	if err == nil && expand {
		scheme.Events, err = s.NotificationSchemeEvents(ctx, workspaceID, schemeID)
	}
	return scheme, err
}

func (s *Store) NotificationSchemeEvents(ctx context.Context, workspaceID string, schemeID int64) ([]models.NotificationSchemeEvent, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM notification_schemes WHERE workspace_id=$1 AND id=$2)`, workspaceID, schemeID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotificationSchemeNotFound
	}
	return notificationSchemeEntriesTx(ctx, tx, workspaceID, schemeID)
}

func validateNotificationSchemeName(name string) error {
	if name == "" || name != strings.TrimSpace(name) || len(name) > 255 {
		return fmt.Errorf("%w: name must contain 1 to 255 characters and cannot begin or end with whitespace", ErrNotificationSchemeValidation)
	}
	return nil
}

func validateNotificationEntry(ctx context.Context, tx pgx.Tx, workspaceID string, input NotificationEntryInput) (NotificationEntryInput, string, error) {
	input.NotificationType = strings.TrimSpace(input.NotificationType)
	input.Parameter = strings.TrimSpace(input.Parameter)
	if _, ok := NotificationEvent(input.EventID); !ok {
		return input, "", fmt.Errorf("%w: event type with ID %d was not found", ErrNotificationSchemeValidation, input.EventID)
	}
	recipient := input.Parameter
	switch input.NotificationType {
	case "CurrentAssignee", "Reporter", "CurrentUser", "ProjectLead", "ComponentLead", "AllWatchers":
		if input.Parameter != "" {
			return input, "", fmt.Errorf("%w: %s does not accept a parameter", ErrNotificationSchemeValidation, input.NotificationType)
		}
	case "User":
		var active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.workspace_id=$1 AND m.user_id=$2 AND u.active)`, workspaceID, input.Parameter).Scan(&active); err != nil || !active {
			return input, "", fmt.Errorf("%w: user does not exist", ErrNotificationSchemeValidation)
		}
	case "Group":
		var id, name string
		err := tx.QueryRow(ctx, `SELECT g.id::text,g.name FROM groups g JOIN directories d ON d.id=g.directory_id JOIN sites si ON si.organization_id=d.organization_id WHERE si.workspace_id=$1 AND d.active AND (g.id::text=$2 OR lower(g.name)=lower($2)) LIMIT 1`, workspaceID, input.Parameter).Scan(&id, &name)
		if err != nil {
			return input, "", fmt.Errorf("%w: group does not exist", ErrNotificationSchemeValidation)
		}
		input.Parameter, recipient = name, id
	case "ProjectRole":
		roleID, err := strconv.ParseInt(input.Parameter, 10, 64)
		var exists bool
		if err != nil || roleID <= 0 {
			return input, "", fmt.Errorf("%w: project role is invalid", ErrNotificationSchemeValidation)
		}
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_roles WHERE workspace_id=$1 AND id=$2)`, workspaceID, roleID).Scan(&exists); err != nil || !exists {
			return input, "", fmt.Errorf("%w: project role does not exist", ErrNotificationSchemeValidation)
		}
		recipient = strconv.FormatInt(roleID, 10)
	case "EmailAddress":
		address, err := mail.ParseAddress(input.Parameter)
		if err != nil || !strings.EqualFold(address.Address, input.Parameter) {
			return input, "", fmt.Errorf("%w: email address is invalid", ErrNotificationSchemeValidation)
		}
		input.Parameter, recipient = address.Address, strings.ToLower(address.Address)
	case "UserCustomField", "GroupCustomField":
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM custom_fields WHERE id=$1 AND (workspace_id IS NULL OR workspace_id=$2) AND active)`, input.Parameter, workspaceID).Scan(&exists); err != nil || !exists {
			return input, "", fmt.Errorf("%w: custom field does not exist", ErrNotificationSchemeValidation)
		}
	default:
		return input, "", fmt.Errorf("%w: notification type is invalid", ErrNotificationSchemeValidation)
	}
	return input, recipient, nil
}

func insertNotificationEntry(ctx context.Context, tx pgx.Tx, workspaceID string, schemeID int64, input NotificationEntryInput) (models.NotificationSchemeEntry, error) {
	input, recipient, err := validateNotificationEntry(ctx, tx, workspaceID, input)
	if err != nil {
		return models.NotificationSchemeEntry{}, err
	}
	entry, err := scanNotificationEntry(tx.QueryRow(ctx, `INSERT INTO notification_scheme_entries(workspace_id,scheme_id,event_id,notification_type,parameter,recipient) VALUES($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,'')) RETURNING id,event_id,notification_type,COALESCE(parameter,''),COALESCE(recipient,'')`, workspaceID, schemeID, input.EventID, input.NotificationType, input.Parameter, recipient))
	if isUniqueViolation(err) {
		return entry, fmt.Errorf("%w: notification already exists", ErrNotificationSchemeConflict)
	}
	return entry, err
}

func (s *Store) CreateNotificationScheme(ctx context.Context, workspaceID, actorID, name, description string, entries []NotificationEntryInput) (*models.NotificationScheme, error) {
	if err := validateNotificationSchemeName(name); err != nil {
		return nil, err
	}
	if len(description) > 4000 {
		return nil, fmt.Errorf("%w: description accepts at most 4000 characters", ErrNotificationSchemeValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	scheme, err := scanNotificationScheme(tx.QueryRow(ctx, `INSERT INTO notification_schemes(workspace_id,name,description) VALUES($1,$2,$3) RETURNING id,workspace_id,name,description,is_default,0`, workspaceID, name, description))
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: a notification scheme with this name already exists", ErrNotificationSchemeConflict)
	}
	if err != nil {
		return nil, err
	}
	for _, input := range entries {
		if _, err = insertNotificationEntry(ctx, tx, workspaceID, scheme.ID, input); err != nil {
			return nil, err
		}
	}
	scheme.Events, err = notificationSchemeEntriesTx(ctx, tx, workspaceID, scheme.ID)
	if err == nil {
		err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "notification_scheme", strconv.FormatInt(scheme.ID, 10), models.OpUpsert, scheme)
	}
	if err != nil {
		return nil, err
	}
	return scheme, tx.Commit(ctx)
}

func (s *Store) UpdateNotificationScheme(ctx context.Context, workspaceID, actorID string, schemeID int64, name, description *string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	current, err := scanNotificationScheme(tx.QueryRow(ctx, `SELECT `+notificationSchemeColumns+` FROM notification_schemes ns WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, workspaceID, schemeID))
	if err != nil {
		return err
	}
	if name != nil {
		if err = validateNotificationSchemeName(*name); err != nil {
			return err
		}
		current.Name = *name
	}
	if description != nil {
		if len(*description) > 4000 {
			return fmt.Errorf("%w: description accepts at most 4000 characters", ErrNotificationSchemeValidation)
		}
		current.Description = *description
	}
	_, err = tx.Exec(ctx, `UPDATE notification_schemes SET name=$3,description=$4,updated_at=now() WHERE workspace_id=$1 AND id=$2`, workspaceID, schemeID, current.Name, current.Description)
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: a notification scheme with this name already exists", ErrNotificationSchemeConflict)
	}
	if err == nil {
		current.Events, err = notificationSchemeEntriesTx(ctx, tx, workspaceID, schemeID)
	}
	if err == nil {
		err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "notification_scheme", strconv.FormatInt(schemeID, 10), models.OpUpsert, current)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) AddNotificationEntries(ctx context.Context, workspaceID, actorID string, schemeID int64, inputs []NotificationEntryInput) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM notification_schemes WHERE workspace_id=$1 AND id=$2)`, workspaceID, schemeID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotificationSchemeNotFound
	}
	for _, input := range inputs {
		entry, insertErr := insertNotificationEntry(ctx, tx, workspaceID, schemeID, input)
		if insertErr != nil {
			return insertErr
		}
		if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "event_notification", strconv.FormatInt(entry.ID, 10), models.OpUpsert, entry); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteNotificationEntry(ctx context.Context, workspaceID, actorID string, schemeID, entryID int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	entry, err := scanNotificationEntry(tx.QueryRow(ctx, `DELETE FROM notification_scheme_entries WHERE workspace_id=$1 AND scheme_id=$2 AND id=$3 RETURNING id,event_id,notification_type,COALESCE(parameter,''),COALESCE(recipient,'')`, workspaceID, schemeID, entryID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotificationSchemeNotFound
	}
	if err == nil {
		err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "event_notification", strconv.FormatInt(entry.ID, 10), models.OpDelete, entry)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteNotificationScheme(ctx context.Context, workspaceID, actorID string, schemeID int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	scheme, err := scanNotificationScheme(tx.QueryRow(ctx, `SELECT `+notificationSchemeColumns+` FROM notification_schemes ns WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, workspaceID, schemeID))
	if err != nil {
		return err
	}
	if scheme.Default || scheme.ProjectCount > 0 {
		return fmt.Errorf("%w: the default or an assigned notification scheme cannot be deleted", ErrNotificationSchemeConflict)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM notification_schemes WHERE workspace_id=$1 AND id=$2`, workspaceID, schemeID); err == nil {
		err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "notification_scheme", strconv.FormatInt(schemeID, 10), models.OpDelete, scheme)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) AssignedNotificationScheme(ctx context.Context, workspaceID, projectIDOrKey string, expand bool) (*models.NotificationScheme, *models.Project, error) {
	project, err := s.ProjectByIDOrKey(ctx, workspaceID, projectIDOrKey)
	if err != nil {
		return nil, nil, ErrNotificationSchemeNotFound
	}
	scheme, err := scanNotificationScheme(s.Pool.QueryRow(ctx, `SELECT `+notificationSchemeColumns+` FROM notification_schemes ns JOIN project_notification_schemes pns ON pns.workspace_id=ns.workspace_id AND pns.scheme_id=ns.id WHERE pns.project_id=$1 AND ns.workspace_id=$2`, project.ID, workspaceID))
	if err == nil && expand {
		scheme.Events, err = s.NotificationSchemeEvents(ctx, workspaceID, scheme.ID)
	}
	return scheme, project, err
}

func (s *Store) AssignNotificationScheme(ctx context.Context, workspaceID, actorID, projectIDOrKey string, schemeID int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	var projectID string
	if err = tx.QueryRow(ctx, `SELECT id FROM projects WHERE workspace_id=$1 AND lifecycle_state='ACTIVE' AND (id=$2 OR upper(key)=upper($2)) FOR UPDATE`, workspaceID, projectIDOrKey).Scan(&projectID); err != nil {
		return ErrNotificationSchemeNotFound
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM notification_schemes WHERE workspace_id=$1 AND id=$2)`, workspaceID, schemeID).Scan(&exists); err != nil || !exists {
		if err == nil {
			err = ErrNotificationSchemeNotFound
		}
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO project_notification_schemes(project_id,workspace_id,scheme_id) VALUES($1,$2,$3) ON CONFLICT(project_id) DO UPDATE SET workspace_id=EXCLUDED.workspace_id,scheme_id=EXCLUDED.scheme_id,assigned_at=now()`, projectID, workspaceID, schemeID)
	if err == nil {
		err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_notification_scheme", projectID, models.OpUpsert, map[string]any{"projectId": projectID, "notificationSchemeId": schemeID})
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type NotificationSchemeMapping struct {
	SchemeID  int64
	ProjectID string
}

func (s *Store) NotificationSchemeMappings(ctx context.Context, workspaceID string) ([]NotificationSchemeMapping, error) {
	rows, err := s.Pool.Query(ctx, `SELECT scheme_id,project_id FROM project_notification_schemes WHERE workspace_id=$1 ORDER BY scheme_id,project_id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []NotificationSchemeMapping{}
	for rows.Next() {
		var mapping NotificationSchemeMapping
		if err = rows.Scan(&mapping.SchemeID, &mapping.ProjectID); err != nil {
			return nil, err
		}
		result = append(result, mapping)
	}
	return result, rows.Err()
}

func stringValues(value any) []string {
	result := []string{}
	var walk func(any)
	walk = func(v any) {
		switch typed := v.(type) {
		case string:
			if typed != "" {
				result = append(result, typed)
			}
		case []any:
			for _, item := range typed {
				walk(item)
			}
		case map[string]any:
			for _, key := range []string{"accountId", "id", "name", "value"} {
				if item, ok := typed[key]; ok {
					walk(item)
					break
				}
			}
		}
	}
	walk(value)
	return result
}

// DeliverIssueNotification resolves a scheme against the final issue state and
// commits private inbox actions and durable email deliveries exactly once for
// the source action and event.
func (s *Store) DeliverIssueNotification(ctx context.Context, workspaceID, actorID, issueID string, actionSeq, eventID int64, kind, message string) error {
	event, ok := NotificationEvent(eventID)
	if !ok || actionSeq <= 0 {
		return ErrNotificationSchemeValidation
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `INSERT INTO notification_event_deliveries(workspace_id,action_seq,event_id,issue_id) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, workspaceID, actionSeq, eventID, issueID)
	if err != nil || command.RowsAffected() == 0 {
		return err
	}
	var projectID, issueKey, summary, assigneeID, reporterID, projectLeadID, securityLevelID string
	var fields, securityLevels []byte
	err = tx.QueryRow(ctx, `SELECT i.project_id,i.key,i.summary,COALESCE(i.assignee_id,''),COALESCE(i.reporter_id,''),COALESCE(p.lead_account_id,''),i.fields,COALESCE(i.security_level_id,''),COALESCE(ss.levels,'[]'::jsonb) FROM issues i JOIN projects p ON p.id=i.project_id LEFT JOIN security_schemes ss ON ss.id=p.security_scheme_id WHERE i.workspace_id=$1 AND i.id=$2`, workspaceID, issueID).Scan(&projectID, &issueKey, &summary, &assigneeID, &reporterID, &projectLeadID, &fields, &securityLevelID, &securityLevels)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT nse.notification_type,COALESCE(nse.recipient,''),COALESCE(nse.parameter,'') FROM notification_scheme_entries nse JOIN project_notification_schemes pns ON pns.workspace_id=nse.workspace_id AND pns.scheme_id=nse.scheme_id WHERE pns.project_id=$1 AND nse.workspace_id=$2 AND nse.event_id=$3 ORDER BY nse.id`, projectID, workspaceID, eventID)
	if err != nil {
		return err
	}
	type recipientRule struct{ kind, recipient, parameter string }
	rules := []recipientRule{}
	for rows.Next() {
		var rule recipientRule
		if err = rows.Scan(&rule.kind, &rule.recipient, &rule.parameter); err != nil {
			rows.Close()
			return err
		}
		rules = append(rules, rule)
	}
	rows.Close()
	// The value records an explicit CurrentUser match. Implicit roles follow
	// ZZIRA's default preference of suppressing notifications for your own
	// changes; administrators can add CurrentUser when a scheme needs them.
	users := map[string]bool{}
	addUser := func(id string, currentUser bool) {
		if id == "" {
			return
		}
		users[id] = users[id] || currentUser
	}
	externalEmails := map[string]bool{}
	var issueFields map[string]any
	_ = json.Unmarshal(fields, &issueFields)
	var levels []models.SecurityLevel
	_ = json.Unmarshal(securityLevels, &levels)
	addGroup := func(group string) error {
		groupRows, queryErr := tx.Query(ctx, `SELECT gm.user_id FROM group_members gm JOIN groups g ON g.id=gm.group_id JOIN directories d ON d.id=g.directory_id JOIN sites si ON si.organization_id=d.organization_id WHERE si.workspace_id=$1 AND d.active AND (g.id::text=$2 OR lower(g.name)=lower($2))`, workspaceID, group)
		if queryErr != nil {
			return queryErr
		}
		defer groupRows.Close()
		for groupRows.Next() {
			var id string
			if queryErr = groupRows.Scan(&id); queryErr != nil {
				return queryErr
			}
			addUser(id, false)
		}
		return groupRows.Err()
	}
	for _, rule := range rules {
		switch rule.kind {
		case "CurrentAssignee":
			addUser(assigneeID, false)
		case "Reporter":
			addUser(reporterID, false)
		case "CurrentUser":
			addUser(actorID, true)
		case "ProjectLead":
			addUser(projectLeadID, false)
		case "User":
			addUser(rule.recipient, true)
		case "AllWatchers":
			watchRows, queryErr := tx.Query(ctx, `SELECT user_id FROM watchers WHERE issue_id=$1`, issueID)
			if queryErr != nil {
				return queryErr
			}
			for watchRows.Next() {
				var id string
				if queryErr = watchRows.Scan(&id); queryErr != nil {
					watchRows.Close()
					return queryErr
				}
				addUser(id, false)
			}
			watchRows.Close()
		case "Group":
			if err = addGroup(rule.recipient); err != nil {
				return err
			}
		case "ProjectRole":
			roleRows, queryErr := tx.Query(ctx, `SELECT DISTINCT CASE WHEN rb.principal_type='user' THEN rb.principal_id ELSE gm.user_id END FROM role_bindings rb LEFT JOIN group_members gm ON rb.principal_type='group' AND gm.group_id::text=rb.principal_id WHERE rb.scope_type='project' AND rb.scope_id=$1 AND rb.role_key=$2`, projectID, rule.recipient)
			if queryErr != nil {
				return queryErr
			}
			for roleRows.Next() {
				var id *string
				if queryErr = roleRows.Scan(&id); queryErr != nil {
					roleRows.Close()
					return queryErr
				}
				if id != nil {
					addUser(*id, false)
				}
			}
			roleRows.Close()
		case "ComponentLead":
			componentRows, queryErr := tx.Query(ctx, `SELECT DISTINCT c.lead_account_id FROM project_components c WHERE c.project_id=$1 AND c.lead_account_id IS NOT NULL AND EXISTS(SELECT 1 FROM jsonb_array_elements(CASE WHEN $2::jsonb->'components' IS NOT NULL AND jsonb_typeof($2::jsonb->'components')='array' THEN $2::jsonb->'components' ELSE '[]'::jsonb END) item WHERE item->>'id'=c.id)`, projectID, fields)
			if queryErr != nil {
				return queryErr
			}
			for componentRows.Next() {
				var id string
				if queryErr = componentRows.Scan(&id); queryErr != nil {
					componentRows.Close()
					return queryErr
				}
				addUser(id, false)
			}
			componentRows.Close()
		case "UserCustomField":
			for _, id := range stringValues(issueFields[rule.recipient]) {
				addUser(id, false)
			}
		case "GroupCustomField":
			for _, group := range stringValues(issueFields[rule.recipient]) {
				if err = addGroup(group); err != nil {
					return err
				}
			}
		case "EmailAddress":
			externalEmails[rule.recipient] = rule.recipient != ""
		}
	}
	delete(users, "")
	var actorName string
	_ = tx.QueryRow(ctx, `SELECT display_name FROM users WHERE id=$1`, actorID).Scan(&actorName)
	userIDs := make([]string, 0, len(users))
	for id := range users {
		userIDs = append(userIDs, id)
	}
	sort.Strings(userIDs)
	subject := fmt.Sprintf("[%s] %s: %s", issueKey, event.Name, summary)
	body := message + "\n\n" + issueKey + " — " + summary + "\n/browse/" + issueKey
	for _, userID := range userIDs {
		if userID == actorID && !users[userID] {
			continue
		}
		allowed, _, permissionErr := hasProjectPermissionTx(ctx, tx, workspaceID, userID, projectID, issueID, "BROWSE_PROJECTS")
		if permissionErr != nil {
			return permissionErr
		}
		if !allowed {
			continue
		}
		if securityLevelID != "" {
			admin, adminErr := globalPermissionForUser(ctx, tx, workspaceID, userID, "ADMINISTER")
			if adminErr != nil {
				return adminErr
			}
			if !admin {
				levelFound, member := false, false
				for _, level := range levels {
					if level.ID != securityLevelID {
						continue
					}
					levelFound = true
					for _, accountID := range level.Members {
						if accountID == userID {
							member = true
							break
						}
					}
					break
				}
				if levelFound && !member {
					continue
				}
			}
		}
		var email string
		if err = tx.QueryRow(ctx, `SELECT u.email FROM users u JOIN memberships m ON m.user_id=u.id WHERE m.workspace_id=$1 AND u.id=$2 AND u.active`, workspaceID, userID).Scan(&email); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return err
		}
		notification := &models.Notification{ID: NewID("ntf"), WorkspaceID: workspaceID, TargetUser: userID, ActorID: actorID, ActorName: actorName, Kind: kind, EntityType: models.EntityIssue, EntityID: issueID, Message: message}
		if err = tx.QueryRow(ctx, `INSERT INTO notifications(id,workspace_id,user_id,actor_id,kind,entity_type,entity_id,message) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')`, notification.ID, workspaceID, userID, actorID, kind, models.EntityIssue, issueID, message).Scan(&notification.Created); err != nil {
			return err
		}
		seq, seqErr := nextSeq(ctx, tx, workspaceID)
		if seqErr != nil {
			return seqErr
		}
		payload, marshalErr := json.Marshal(models.NotificationPayload{Notification: *notification})
		if marshalErr != nil {
			return marshalErr
		}
		if err = appendAction(ctx, tx, &models.Action{WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityNotification, EntityID: notification.ID, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID}); err != nil {
			return err
		}
		dedupe := fmt.Sprintf("issue-notification:%s:%d:%d:%s", workspaceID, actionSeq, eventID, userID)
		if _, err = tx.Exec(ctx, `INSERT INTO email_outbox(workspace_id,recipient,subject,body,dedupe_key) VALUES($1,$2,$3,$4,$5) ON CONFLICT(dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING`, workspaceID, email, subject, body, dedupe); err != nil {
			return err
		}
		delete(externalEmails, strings.ToLower(email))
	}
	for email := range externalEmails {
		dedupe := fmt.Sprintf("issue-notification:%s:%d:%d:email:%s", workspaceID, actionSeq, eventID, email)
		if _, err = tx.Exec(ctx, `INSERT INTO email_outbox(workspace_id,recipient,subject,body,dedupe_key) VALUES($1,$2,$3,$4,$5) ON CONFLICT(dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING`, workspaceID, email, subject, body, dedupe); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
