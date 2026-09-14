package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	neturl "net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

// ---- Jira app properties ----

// ErrJiraAppPropertyValidation reports a property key or value Jira refuses.
var ErrJiraAppPropertyValidation = errors.New("invalid app property")

// ValidJiraAppProperty applies Jira's limits: a key of at most 127 characters
// and a non-empty JSON value of at most 32,768 characters.
func ValidJiraAppProperty(key string, value json.RawMessage, checkValue bool) error {
	if key == "" || len([]rune(key)) > 127 {
		return fmt.Errorf("%w: the property key must be between 1 and 127 characters", ErrJiraAppPropertyValidation)
	}
	if checkValue {
		if len(value) == 0 || !json.Valid(value) {
			return fmt.Errorf("%w: the value must be valid, non-empty JSON", ErrJiraAppPropertyValidation)
		}
		if len([]rune(string(value))) > 32768 {
			return fmt.Errorf("%w: the value must be at most 32768 characters", ErrJiraAppPropertyValidation)
		}
	}
	return nil
}

func (s *Store) JiraAppPropertyKeys(ctx context.Context, installationID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT key FROM jira_app_properties WHERE installation_id=$1 ORDER BY key`, installationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (s *Store) JiraAppProperty(ctx context.Context, installationID, key string) (json.RawMessage, error) {
	var value json.RawMessage
	err := s.Pool.QueryRow(ctx, `SELECT value FROM jira_app_properties WHERE installation_id=$1 AND key=$2`, installationID, key).Scan(&value)
	return value, err
}

// PutJiraAppProperty creates or replaces a property and reports whether it
// was created.
func (s *Store) PutJiraAppProperty(ctx context.Context, installationID, key string, value json.RawMessage) (bool, error) {
	if err := ValidJiraAppProperty(key, value, true); err != nil {
		return false, err
	}
	var created bool
	err := s.Pool.QueryRow(ctx, `INSERT INTO jira_app_properties(installation_id,key,value) VALUES($1,$2,$3::jsonb)
		ON CONFLICT (installation_id,key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()
		RETURNING (xmax = 0)`, installationID, key, []byte(value)).Scan(&created)
	return created, err
}

// DeleteJiraAppProperty removes a property and reports whether it existed.
func (s *Store) DeleteJiraAppProperty(ctx context.Context, installationID, key string) (bool, error) {
	result, err := s.Pool.Exec(ctx, `DELETE FROM jira_app_properties WHERE installation_id=$1 AND key=$2`, installationID, key)
	return result.RowsAffected() > 0, err
}

// ---- UI modifications ----

// ErrUIModificationValidation reports a UI modification Jira refuses.
var ErrUIModificationValidation = errors.New("invalid UI modification")

// UIModificationContext is where a UI modification applies.
type UIModificationContext struct {
	ID            string
	ProjectID     *string
	IssueTypeID   *string
	PortalID      *string
	RequestTypeID *string
	ViewType      *string
	IsAvailable   bool
}

// UIModification is an app's UI modification.
type UIModification struct {
	ID          string
	Name        string
	Description string
	Data        *string
	Contexts    []UIModificationContext
}

const (
	uiModificationsPerApp     = 3000
	uiModificationContexts    = 1000
	uiModificationsPerContext = 100
)

func (s *Store) UIModifications(ctx context.Context, workspaceID, installationID string, offset, limit int) ([]UIModification, int, error) {
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM ui_modifications WHERE workspace_id=$1 AND installation_id=$2`, workspaceID, installationID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT id::text,name,description,data FROM ui_modifications
		WHERE workspace_id=$1 AND installation_id=$2 ORDER BY created_at,id OFFSET $3 LIMIT $4`, workspaceID, installationID, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	modifications := []UIModification{}
	for rows.Next() {
		var modification UIModification
		if err := rows.Scan(&modification.ID, &modification.Name, &modification.Description, &modification.Data); err != nil {
			rows.Close()
			return nil, 0, err
		}
		modifications = append(modifications, modification)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	for index := range modifications {
		contexts, err := s.uiModificationContexts(ctx, workspaceID, modifications[index].ID)
		if err != nil {
			return nil, 0, err
		}
		modifications[index].Contexts = contexts
	}
	return modifications, total, nil
}

func (s *Store) uiModificationContexts(ctx context.Context, workspaceID, modificationID string) ([]UIModificationContext, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT c.id::text,c.project_id,c.issue_type_id,c.portal_id,c.request_type_id,c.view_type,
		       (c.project_id IS NULL OR EXISTS(SELECT 1 FROM projects p WHERE p.id=c.project_id AND p.workspace_id=$2 AND p.lifecycle_state='ACTIVE'))
		   AND (c.issue_type_id IS NULL OR EXISTS(SELECT 1 FROM issue_types t WHERE t.jira_id::text=c.issue_type_id AND (t.workspace_id IS NULL OR t.workspace_id=$2)))
		FROM ui_modification_contexts c WHERE c.ui_modification_id=$1::uuid ORDER BY c.position`, modificationID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	contexts := []UIModificationContext{}
	for rows.Next() {
		var context UIModificationContext
		if err := rows.Scan(&context.ID, &context.ProjectID, &context.IssueTypeID, &context.PortalID, &context.RequestTypeID, &context.ViewType, &context.IsAvailable); err != nil {
			return nil, err
		}
		contexts = append(contexts, context)
	}
	return contexts, rows.Err()
}

func contextKey(context UIModificationContext) string {
	value := func(pointer *string) string {
		if pointer == nil {
			return "\x00"
		}
		return *pointer
	}
	return strings.Join([]string{value(context.ProjectID), value(context.IssueTypeID), value(context.PortalID), value(context.RequestTypeID), value(context.ViewType)}, "\x1f")
}

func (s *Store) writeUIModificationContexts(ctx context.Context, tx pgx.Tx, workspaceID, modificationID string, contexts []UIModificationContext) error {
	if len(contexts) > uiModificationContexts {
		return fmt.Errorf("%w: a UI modification can define at most %d contexts", ErrUIModificationValidation, uiModificationContexts)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM ui_modification_contexts WHERE ui_modification_id=$1::uuid`, modificationID); err != nil {
		return err
	}
	for index, context := range contexts {
		var shared int
		if err := tx.QueryRow(ctx, `
			SELECT count(DISTINCT c.ui_modification_id) FROM ui_modification_contexts c JOIN ui_modifications m ON m.id=c.ui_modification_id
			WHERE m.workspace_id=$1 AND c.project_id IS NOT DISTINCT FROM $2 AND c.issue_type_id IS NOT DISTINCT FROM $3
			  AND c.portal_id IS NOT DISTINCT FROM $4 AND c.request_type_id IS NOT DISTINCT FROM $5 AND c.view_type IS NOT DISTINCT FROM $6`,
			workspaceID, context.ProjectID, context.IssueTypeID, context.PortalID, context.RequestTypeID, context.ViewType).Scan(&shared); err != nil {
			return err
		}
		if shared >= uiModificationsPerContext {
			return fmt.Errorf("%w: a context can be assigned to at most %d UI modifications", ErrUIModificationValidation, uiModificationsPerContext)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ui_modification_contexts(ui_modification_id,position,project_id,issue_type_id,portal_id,request_type_id,view_type)
			VALUES($1::uuid,$2,$3,$4,$5,$6,$7)`, modificationID, index, context.ProjectID, context.IssueTypeID, context.PortalID, context.RequestTypeID, context.ViewType); err != nil {
			return err
		}
	}
	return nil
}

func validateUIModificationContexts(contexts []UIModificationContext) error {
	seen := map[string]bool{}
	for _, context := range contexts {
		key := contextKey(context)
		if seen[key] {
			return fmt.Errorf("%w: contexts must be unique", ErrUIModificationValidation)
		}
		seen[key] = true
	}
	return nil
}

func (s *Store) CreateUIModification(ctx context.Context, workspaceID, installationID string, modification UIModification) (string, error) {
	if err := validateUIModificationContexts(modification.Contexts); err != nil {
		return "", err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM ui_modifications WHERE installation_id=$1`, installationID).Scan(&count); err != nil {
		return "", err
	}
	if count >= uiModificationsPerApp {
		return "", fmt.Errorf("%w: an app can define at most %d UI modifications", ErrUIModificationValidation, uiModificationsPerApp)
	}
	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO ui_modifications(workspace_id,installation_id,name,description,data) VALUES($1,$2,$3,$4,$5) RETURNING id::text`,
		workspaceID, installationID, modification.Name, modification.Description, modification.Data).Scan(&id); err != nil {
		return "", err
	}
	if err := s.writeUIModificationContexts(ctx, tx, workspaceID, id, modification.Contexts); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

// UIModificationUpdate changes the fields that are present.
type UIModificationUpdate struct {
	Name        *string
	Description *string
	Data        *string
	SetData     bool
	Contexts    *[]UIModificationContext
}

func (s *Store) UpdateUIModification(ctx context.Context, workspaceID, installationID, id string, update UIModificationUpdate) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `UPDATE ui_modifications SET
		name=COALESCE($4,name), description=COALESCE($5,description),
		data=CASE WHEN $7 THEN $6 ELSE data END, updated_at=now()
		WHERE id::text=$1 AND workspace_id=$2 AND installation_id=$3`,
		id, workspaceID, installationID, update.Name, update.Description, update.Data, update.SetData)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if update.Contexts != nil {
		if err := validateUIModificationContexts(*update.Contexts); err != nil {
			return err
		}
		if err := s.writeUIModificationContexts(ctx, tx, workspaceID, id, *update.Contexts); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteUIModification(ctx context.Context, workspaceID, installationID, id string) error {
	result, err := s.Pool.Exec(ctx, `DELETE FROM ui_modifications WHERE id::text=$1 AND workspace_id=$2 AND installation_id=$3`, id, workspaceID, installationID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ---- webhooks ----

// ErrWebhookValidation reports a webhook registration Jira refuses.
var ErrWebhookValidation = errors.New("invalid webhook")

// dynamicWebhookLifetime is how long a webhook registered through the REST API
// stays active without a refresh.
const dynamicWebhookLifetime = 30 * 24 * time.Hour

const webhookColumns = `w.id,w.jira_id,w.url,w.events,w.jql,w.active,w.start_seq,COALESCE(w.installation_id,''),w.expires_at,
	COALESCE(w.field_ids_filter,'{}'),COALESCE(w.issue_property_keys_filter,'{}'),w.name,w.exclude_body,w.updated_at,COALESCE(w.updated_by,''),COALESCE(u.display_name,'')`

func scanWebhook(row pgx.Row) (*models.Webhook, error) {
	webhook := &models.Webhook{}
	err := row.Scan(&webhook.ID, &webhook.JiraID, &webhook.URL, &webhook.Events, &webhook.JQL, &webhook.Active, &webhook.StartSeq,
		&webhook.InstallationID, &webhook.ExpiresAt, &webhook.FieldIDs, &webhook.PropertyKeys, &webhook.Name, &webhook.ExcludeBody,
		&webhook.UpdatedAt, &webhook.UpdatedBy, &webhook.UpdatedByName)
	return webhook, err
}

func (s *Store) queryWebhooks(ctx context.Context, where string, args ...any) ([]*models.Webhook, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+webhookColumns+` FROM webhooks w LEFT JOIN users u ON u.id=w.updated_by WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	webhooks := []*models.Webhook{}
	for rows.Next() {
		webhook, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		webhooks = append(webhooks, webhook)
	}
	return webhooks, rows.Err()
}

func validWebhookURL(raw string) error {
	parsed, err := neturl.ParseRequestURI(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return fmt.Errorf("%w: the webhook URL must be an absolute HTTP(S) URL without user information", ErrWebhookValidation)
	}
	return nil
}

// DynamicWebhookSpec is one webhook an app registers.
type DynamicWebhookSpec struct {
	Events       []string
	JQL          string
	FieldIDs     []string
	PropertyKeys []string
}

// RegisterAppWebhook registers one of an app's webhooks. An app registers all
// its webhooks against a single URL.
func (s *Store) RegisterAppWebhook(ctx context.Context, workspaceID, installationID, url string, spec DynamicWebhookSpec) (int64, error) {
	if err := validWebhookURL(url); err != nil {
		return 0, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "app-webhooks:"+installationID); err != nil {
		return 0, err
	}
	var otherURL string
	err = tx.QueryRow(ctx, `SELECT url FROM webhooks WHERE installation_id=$1 AND url<>$2 LIMIT 1`, installationID, url).Scan(&otherURL)
	if err == nil {
		return 0, fmt.Errorf("%w: Only a single URL per app is allowed to be registered.", ErrWebhookValidation)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM webhooks WHERE installation_id=$1`, installationID).Scan(&count); err != nil {
		return 0, err
	}
	if count >= 100 {
		return 0, fmt.Errorf("%w: An app can register at most 100 webhooks.", ErrWebhookValidation)
	}
	var head int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(seq),0) FROM actions WHERE workspace_id=$1`, workspaceID).Scan(&head); err != nil {
		return 0, err
	}
	var jiraID int64
	err = tx.QueryRow(ctx, `INSERT INTO webhooks(id,workspace_id,url,events,jql,start_seq,installation_id,expires_at,field_ids_filter,issue_property_keys_filter)
		VALUES($1,$2,$3,$4,$5,$6,$7,now()+$8::interval,$9,$10) RETURNING jira_id`,
		NewID("wh"), workspaceID, url, spec.Events, spec.JQL, head, installationID, fmt.Sprintf("%d seconds", int(dynamicWebhookLifetime.Seconds())),
		nullableStrings(spec.FieldIDs), nullableStrings(spec.PropertyKeys)).Scan(&jiraID)
	if err != nil {
		return 0, err
	}
	return jiraID, tx.Commit(ctx)
}

func nullableStrings(values []string) any {
	if values == nil {
		return nil
	}
	return values
}

func (s *Store) AppWebhooks(ctx context.Context, workspaceID, installationID string, offset, limit int) ([]*models.Webhook, int, error) {
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM webhooks WHERE workspace_id=$1 AND installation_id=$2`, workspaceID, installationID).Scan(&total); err != nil {
		return nil, 0, err
	}
	webhooks, err := s.queryWebhooks(ctx, `w.workspace_id=$1 AND w.installation_id=$2 ORDER BY w.jira_id OFFSET $3 LIMIT $4`, workspaceID, installationID, offset, limit)
	return webhooks, total, err
}

// DeleteAppWebhooks removes the listed webhooks the app registered; others are
// ignored.
func (s *Store) DeleteAppWebhooks(ctx context.Context, workspaceID, installationID string, ids []int64) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM webhooks WHERE workspace_id=$1 AND installation_id=$2 AND jira_id=ANY($3)`, workspaceID, installationID, ids)
	return err
}

// RefreshAppWebhooks extends the life of the listed webhooks the app
// registered and returns their new expiration.
func (s *Store) RefreshAppWebhooks(ctx context.Context, workspaceID, installationID string, ids []int64) (time.Time, error) {
	expiration := time.Now().UTC().Add(dynamicWebhookLifetime).Truncate(time.Millisecond)
	_, err := s.Pool.Exec(ctx, `UPDATE webhooks SET expires_at=$4, updated_at=now() WHERE workspace_id=$1 AND installation_id=$2 AND jira_id=ANY($3)`,
		workspaceID, installationID, ids, expiration)
	return expiration, err
}

// FailedWebhook is a delivery abandoned after its retries.
type FailedWebhook struct {
	ID          string
	URL         string
	Body        string
	FailureTime time.Time
}

// FailedAppWebhooks lists an app's abandoned deliveries from the last 72
// hours that failed after the given time, oldest first. Deliveries sharing the
// last failure time stay on one page.
func (s *Store) FailedAppWebhooks(ctx context.Context, workspaceID, installationID string, after time.Time, limit int) ([]FailedWebhook, error) {
	rows, err := s.Pool.Query(ctx, `
		WITH page AS (
			SELECT d.failed_at FROM webhook_deliveries d JOIN webhooks w ON w.id=d.webhook_id
			WHERE w.workspace_id=$1 AND w.installation_id=$2 AND d.state='abandoned'
			  AND d.failed_at>$3 AND d.failed_at>now()-interval '72 hours'
			ORDER BY d.failed_at LIMIT $4
		)
		SELECT w.jira_id::text||'-'||d.seq::text, w.url, COALESCE(d.body,''), d.failed_at
		FROM webhook_deliveries d JOIN webhooks w ON w.id=d.webhook_id
		WHERE w.workspace_id=$1 AND w.installation_id=$2 AND d.state='abandoned'
		  AND d.failed_at>$3 AND d.failed_at>now()-interval '72 hours'
		  AND d.failed_at<=(SELECT COALESCE(max(failed_at),'-infinity') FROM page)
		ORDER BY d.failed_at, d.seq`, workspaceID, installationID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	failed := []FailedWebhook{}
	for rows.Next() {
		var webhook FailedWebhook
		if err := rows.Scan(&webhook.ID, &webhook.URL, &webhook.Body, &webhook.FailureTime); err != nil {
			return nil, err
		}
		failed = append(failed, webhook)
	}
	return failed, rows.Err()
}

// AdminWebhookInput is an administrator's webhook.
type AdminWebhookInput struct {
	Name        string
	URL         string
	Events      []string
	JQL         string
	ExcludeBody bool
	Enabled     bool
}

func (s *Store) AdminWebhooks(ctx context.Context, workspaceID string) ([]*models.Webhook, error) {
	return s.queryWebhooks(ctx, `w.workspace_id=$1 AND w.installation_id IS NULL ORDER BY w.jira_id`, workspaceID)
}

func (s *Store) AdminWebhook(ctx context.Context, workspaceID, jiraID string) (*models.Webhook, error) {
	webhooks, err := s.queryWebhooks(ctx, `w.workspace_id=$1 AND w.installation_id IS NULL AND w.jira_id::text=$2`, workspaceID, jiraID)
	if err != nil {
		return nil, err
	}
	if len(webhooks) == 0 {
		return nil, pgx.ErrNoRows
	}
	return webhooks[0], nil
}

func (s *Store) CreateAdminWebhook(ctx context.Context, workspaceID, actorID string, input AdminWebhookInput) (*models.Webhook, error) {
	if err := validWebhookURL(input.URL); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Name) == "" {
		return nil, fmt.Errorf("%w: the webhook name is required", ErrWebhookValidation)
	}
	head, err := s.Head(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	var jiraID int64
	if err := s.Pool.QueryRow(ctx, `INSERT INTO webhooks(id,workspace_id,url,events,jql,start_seq,name,exclude_body,active,updated_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING jira_id`,
		NewID("wh"), workspaceID, input.URL, input.Events, input.JQL, head, strings.TrimSpace(input.Name), input.ExcludeBody, input.Enabled, actorID).Scan(&jiraID); err != nil {
		return nil, err
	}
	return s.AdminWebhook(ctx, workspaceID, fmt.Sprint(jiraID))
}

func (s *Store) UpdateAdminWebhook(ctx context.Context, workspaceID, actorID, jiraID string, input AdminWebhookInput) (*models.Webhook, error) {
	if err := validWebhookURL(input.URL); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Name) == "" {
		return nil, fmt.Errorf("%w: the webhook name is required", ErrWebhookValidation)
	}
	result, err := s.Pool.Exec(ctx, `UPDATE webhooks SET name=$3,url=$4,events=$5,jql=$6,exclude_body=$7,active=$8,updated_by=$9,updated_at=now()
		WHERE workspace_id=$1 AND installation_id IS NULL AND jira_id::text=$2`,
		workspaceID, jiraID, strings.TrimSpace(input.Name), input.URL, input.Events, input.JQL, input.ExcludeBody, input.Enabled, actorID)
	if err != nil {
		return nil, err
	}
	if result.RowsAffected() == 0 {
		return nil, pgx.ErrNoRows
	}
	return s.AdminWebhook(ctx, workspaceID, jiraID)
}

func (s *Store) DeleteAdminWebhook(ctx context.Context, workspaceID, jiraID string) error {
	result, err := s.Pool.Exec(ctx, `DELETE FROM webhooks WHERE workspace_id=$1 AND installation_id IS NULL AND jira_id::text=$2`, workspaceID, jiraID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ---- project data classification ----

func (s *Store) ProjectDefaultClassification(ctx context.Context, workspaceID, projectID string) (string, error) {
	var level *string
	err := s.Pool.QueryRow(ctx, `SELECT default_classification_level FROM projects WHERE id=$1 AND workspace_id=$2`, projectID, workspaceID).Scan(&level)
	if err != nil || level == nil {
		return "", err
	}
	return *level, nil
}

// SetProjectDefaultClassification sets or, with an empty level, removes a
// project's default classification level.
func (s *Store) SetProjectDefaultClassification(ctx context.Context, workspaceID, projectID, levelID string) error {
	var value any
	if levelID != "" {
		value = levelID
	}
	result, err := s.Pool.Exec(ctx, `UPDATE projects SET default_classification_level=$3 WHERE id=$1 AND workspace_id=$2`, projectID, workspaceID, value)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ---- audit records ----

// JiraAuditRecord is one site audit event with its author.
type JiraAuditRecord struct {
	models.OrganizationAuditEvent
	CreatedAt time.Time
}

// JiraAuditRecords reads the site organization's audit events, newest first,
// matching every filter word in any field and created within the bounds.
func (s *Store) JiraAuditRecords(ctx context.Context, workspaceID string, words []string, from, to *time.Time, offset, limit int) ([]JiraAuditRecord, int, error) {
	where := `e.organization_id=(SELECT organization_id FROM sites WHERE workspace_id=$1)`
	args := []any{workspaceID}
	for _, word := range words {
		args = append(args, "%"+word+"%")
		placeholder := fmt.Sprintf("$%d", len(args))
		where += ` AND (e.action ILIKE ` + placeholder + ` OR e.target_type ILIKE ` + placeholder + ` OR e.target_id ILIKE ` + placeholder +
			` OR e.detail::text ILIKE ` + placeholder + ` OR COALESCE(u.display_name,'') ILIKE ` + placeholder + ` OR COALESCE(e.actor_id,'') ILIKE ` + placeholder + `)`
	}
	if from != nil {
		args = append(args, *from)
		where += fmt.Sprintf(` AND e.created_at>=$%d`, len(args))
	}
	if to != nil {
		args = append(args, *to)
		where += fmt.Sprintf(` AND e.created_at<=$%d`, len(args))
	}
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM organization_audit_events e LEFT JOIN users u ON u.id=e.actor_id WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, offset, limit)
	rows, err := s.Pool.Query(ctx, `SELECT e.id,e.organization_id::text,COALESCE(e.actor_id,''),COALESCE(u.display_name,''),e.action,e.target_type,e.target_id,e.detail,e.created_at
		FROM organization_audit_events e LEFT JOIN users u ON u.id=e.actor_id WHERE `+where+
		fmt.Sprintf(` ORDER BY e.created_at DESC,e.id DESC OFFSET $%d LIMIT $%d`, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	records := []JiraAuditRecord{}
	for rows.Next() {
		var record JiraAuditRecord
		var detail []byte
		if err := rows.Scan(&record.ID, &record.OrganizationID, &record.ActorID, &record.ActorName, &record.Action, &record.TargetType, &record.TargetID, &detail, &record.CreatedAt); err != nil {
			return nil, 0, err
		}
		if len(detail) > 0 {
			_ = json.Unmarshal(detail, &record.Detail)
		}
		records = append(records, record)
	}
	return records, total, rows.Err()
}

// ---- licensing ----

// SiteProductKeys lists the site's enabled products.
func (s *Store) SiteProductKeys(ctx context.Context, workspaceID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT p.product_key FROM products p JOIN sites si ON si.id=p.site_id WHERE si.workspace_id=$1 AND p.enabled ORDER BY p.product_key`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

// ActiveMemberCount counts the site's active members.
func (s *Store) ActiveMemberCount(ctx context.Context, workspaceID string) (int, error) {
	var count int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM memberships m WHERE m.workspace_id=$1
		AND NOT EXISTS (SELECT 1 FROM directory_users du WHERE du.user_id=m.user_id AND du.deactivated_at IS NOT NULL)`, workspaceID).Scan(&count)
	return count, err
}

// ---- worklog keys ----

// WorklogKey pairs a worklog's issue and worklog ids.
type WorklogKey struct {
	IssueID   int64 `json:"issueId"`
	WorklogID int64 `json:"worklogId"`
}

// ExistingWorklogKeys returns the pairs that name an existing worklog on that
// issue, in request order.
func (s *Store) ExistingWorklogKeys(ctx context.Context, workspaceID string, keys []WorklogKey) ([]WorklogKey, error) {
	issueIDs := make([]int64, 0, len(keys))
	worklogIDs := make([]int64, 0, len(keys))
	for _, key := range keys {
		issueIDs = append(issueIDs, key.IssueID)
		worklogIDs = append(worklogIDs, key.WorklogID)
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT pair.issue_id, pair.worklog_id FROM unnest($2::bigint[],$3::bigint[]) WITH ORDINALITY AS pair(issue_id,worklog_id,position)
		WHERE EXISTS (SELECT 1 FROM worklogs w JOIN issues i ON i.id=w.issue_id
		              WHERE w.workspace_id=$1 AND i.jira_id=pair.issue_id AND w.jira_id=pair.worklog_id)
		ORDER BY pair.position`, workspaceID, issueIDs, worklogIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := []WorklogKey{}
	for rows.Next() {
		var key WorklogKey
		if err := rows.Scan(&key.IssueID, &key.WorklogID); err != nil {
			return nil, err
		}
		found = append(found, key)
	}
	return found, rows.Err()
}
