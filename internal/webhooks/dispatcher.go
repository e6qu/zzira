// Package webhooks delivers action-log events to registered webhooks. The
// delivery ledger (webhook_deliveries) plus FOR UPDATE SKIP LOCKED claims make
// each delivery attempt exclusive across replicas. Failed deliveries persist a
// retry schedule, so retries remain durable across process restarts.
package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

type Dispatcher struct {
	Store   *store.Store
	Checker *JQLChecker
	Client  *http.Client
}

// JQLChecker evaluates a webhook's optional JQL filter against an issue.
type JQLChecker struct {
	Search func(ctx context.Context, wsID, jqlText string) (bool, error)
}

// EventFor maps an action to a Jira webhook event name; ok=false when the
// action has no webhook event.
func EventFor(a *models.Action) (string, bool) {
	switch a.EntityType {
	case models.EntityIssue:
		switch a.Op {
		case models.OpUpsert:
			var p models.IssueUpdatePayload
			if json.Unmarshal(a.Payload, &p) == nil && (len(p.Diff) > 0 || len(p.TriggeredWebhookIDs) > 0) {
				return "jira:issue_updated", true
			}
			return "jira:issue_created", true
		case models.OpDelete:
			return "jira:issue_deleted", true
		}
	case models.EntityComment:
		if a.Op == models.OpUpsert {
			return "comment_created", true
		}
		return "comment_deleted", true
	case models.EntityAttachment:
		if a.Op == models.OpUpsert {
			return "attachment_created", true
		}
	case models.EntityIssueProperty:
		if a.Op == models.OpUpsert {
			return "issue_property_set", true
		}
		return "issue_property_deleted", true
	}
	return "", false
}

// Run claims and delivers until ctx is done. Errors are logged with context
// and surfaced as failed delivery state, never silent.
func (d *Dispatcher) Run(ctx context.Context, workspaceID string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.drainOnce(ctx, workspaceID)
		}
	}
}

func (d *Dispatcher) drainOnce(ctx context.Context, workspaceID string) {
	head, err := d.Store.Head(ctx, workspaceID)
	if err != nil {
		fmt.Printf("webhooks: head: %v\n", err)
		return
	}
	if err := d.Store.ClaimNewWebhookSeqs(ctx, workspaceID, head); err != nil {
		fmt.Printf("webhooks: enroll: %v\n", err)
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		webhook, seqs, ok, err := d.Store.ClaimPendingWebhookBatch(ctx, workspaceID, 100)
		if err != nil {
			fmt.Printf("webhooks: claim: %v\n", err)
			return
		}
		if !ok {
			return
		}
		for _, seq := range seqs {
			if err := d.deliver(ctx, workspaceID, webhook, seq); err != nil {
				fmt.Printf("webhooks: deliver webhook=%s seq=%d: %v\n", webhook.ID, seq, err)
			}
		}
	}
}

func (d *Dispatcher) deliver(ctx context.Context, workspaceID string, webhook *models.Webhook, seq int64) error {
	action, err := d.Store.ActionBySeq(ctx, workspaceID, seq)
	if errors.Is(err, pgx.ErrNoRows) {
		// Workspace sequences are monotonic watermarks; maintenance and
		// permission-shaped actions can leave a sequence without a public
		// action row. The gap has no event to deliver and is terminal.
		return d.mark(ctx, webhook.ID, seq, true, "", "")
	}
	if err != nil {
		return d.markFailed(ctx, webhook.ID, seq, fmt.Errorf("load action: %w", err), "")
	}
	event, ok := EventFor(action)
	if !ok {
		return d.mark(ctx, webhook.ID, seq, true, "", "")
	}
	forced := actionTriggersWebhook(action, webhook.ID)
	if !forced && len(webhook.Events) > 0 && !containsString(webhook.Events, event) {
		return d.mark(ctx, webhook.ID, seq, true, "", "")
	}
	if !forced && webhook.JQL != "" && actionKey(action) != "" {
		if d.Checker == nil || d.Checker.Search == nil {
			return d.markFailed(ctx, webhook.ID, seq, errors.New("JQL checker is not configured"), "")
		}
		match, err := d.Checker.Search(ctx, workspaceID, fmt.Sprintf(`(%s) AND key = %s`, webhook.JQL, strconv.Quote(actionKey(action))))
		if err != nil {
			return d.markFailed(ctx, webhook.ID, seq, fmt.Errorf("evaluate JQL: %w", err), "")
		}
		if !match {
			return d.mark(ctx, webhook.ID, seq, true, "", "")
		}
	}
	if !forced && event == "jira:issue_updated" && len(webhook.FieldIDs) > 0 && !changesField(action, webhook.FieldIDs) {
		return d.mark(ctx, webhook.ID, seq, true, "", "")
	}
	if action.EntityType == models.EntityIssueProperty && len(webhook.PropertyKeys) > 0 && !containsString(webhook.PropertyKeys, propertyKey(action)) {
		return d.mark(ctx, webhook.ID, seq, true, "", "")
	}
	payload, err := json.Marshal(map[string]any{
		"webhookEvent": event,
		"timestamp":    action.CreatedAt,
		"action":       action,
	})
	if err != nil {
		return d.markFailed(ctx, webhook.ID, seq, fmt.Errorf("encode payload: %w", err), "")
	}
	body := payload
	if webhook.ExcludeBody {
		body = nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook.URL, bytes.NewReader(body))
	if err != nil {
		return d.markFailed(ctx, webhook.ID, seq, fmt.Errorf("create request: %w", err), string(body))
	}
	if !webhook.ExcludeBody {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-ZZIRA-Event", event)
	req.Header.Set("X-Atlassian-Webhook-Identifier", fmt.Sprintf("%d-%d", webhook.JiraID, seq))
	if d.Client == nil {
		return d.markFailed(ctx, webhook.ID, seq, errors.New("webhook HTTP client is not configured"), string(body))
	}
	resp, err := d.Client.Do(req)
	if err != nil {
		return d.markFailed(ctx, webhook.ID, seq, fmt.Errorf("send request: %w", err), string(body))
	}
	_, readErr := io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	closeErr := resp.Body.Close()
	if readErr != nil || closeErr != nil {
		return d.markFailed(ctx, webhook.ID, seq, errors.Join(readErr, closeErr), string(body))
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return d.mark(ctx, webhook.ID, seq, true, "", "")
	}
	return d.markFailed(ctx, webhook.ID, seq, fmt.Errorf("http %d", resp.StatusCode), string(body))
}

func actionTriggersWebhook(action *models.Action, webhookID string) bool {
	if action.EntityType != models.EntityIssue || action.Op != models.OpUpsert {
		return false
	}
	var payload models.IssueUpdatePayload
	return json.Unmarshal(action.Payload, &payload) == nil && containsString(payload.TriggeredWebhookIDs, webhookID)
}

// changesField reports whether an issue update changed one of the fields a
// webhook filters on.
func changesField(action *models.Action, fieldIDs []string) bool {
	var payload models.IssueUpdatePayload
	if json.Unmarshal(action.Payload, &payload) != nil {
		return false
	}
	for key, item := range payload.Diff {
		if containsString(fieldIDs, key) || containsString(fieldIDs, item.Field) {
			return true
		}
	}
	return false
}

func (d *Dispatcher) markFailed(ctx context.Context, webhookID string, seq int64, cause error, body string) error {
	if err := d.mark(ctx, webhookID, seq, false, cause.Error(), body); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func (d *Dispatcher) mark(ctx context.Context, webhookID string, seq int64, delivered bool, lastErr, body string) error {
	if err := d.Store.MarkWebhookDelivery(ctx, webhookID, seq, delivered, lastErr, body); err != nil {
		return fmt.Errorf("record delivery state: %w", err)
	}
	return nil
}

func actionKey(a *models.Action) string {
	if a.EntityType == models.EntityIssue {
		var p models.IssueUpdatePayload
		if err := json.Unmarshal(a.Payload, &p); err == nil && p.Issue.Key != "" {
			return p.Issue.Key
		}
		return a.EntityID
	}
	if a.EntityType == models.EntityIssueProperty {
		var p models.IssuePropertyPayload
		if err := json.Unmarshal(a.Payload, &p); err == nil {
			return p.IssueKey
		}
	}
	return ""
}

// propertyKey is the issue property an issue property action changed.
func propertyKey(a *models.Action) string {
	var p models.IssuePropertyPayload
	if json.Unmarshal(a.Payload, &p) != nil {
		return ""
	}
	return p.Key
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
