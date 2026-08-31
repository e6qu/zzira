// Package webhooks delivers action-log events to registered webhooks. The
// delivery ledger (webhook_deliveries) plus FOR UPDATE SKIP LOCKED claims make
// delivery exactly-once-per-event across replicas; failures are retried by
// remaining 'delivering' rows being re-claimed after a timeout.
package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
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
			if json.Unmarshal(a.Payload, &p) == nil && len(p.Diff) > 0 {
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
	if err := d.Store.ClaimNewWebhookSeqs(ctx, head); err != nil {
		fmt.Printf("webhooks: enroll: %v\n", err)
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		webhook, seqs, ok, err := d.Store.ClaimPendingWebhookBatch(ctx, 100)
		if err != nil {
			fmt.Printf("webhooks: claim: %v\n", err)
			return
		}
		if !ok {
			return
		}
		for _, seq := range seqs {
			d.deliver(ctx, workspaceID, webhook, seq)
		}
	}
}

func (d *Dispatcher) deliver(ctx context.Context, workspaceID string, webhook *models.Webhook, seq int64) {
	action, err := d.Store.ActionBySeq(ctx, workspaceID, seq)
	if err != nil {
		_ = d.Store.MarkWebhookDelivery(ctx, webhook.ID, seq, false, "action not found")
		return
	}
	event, ok := EventFor(action)
	if !ok {
		_ = d.Store.MarkWebhookDelivery(ctx, webhook.ID, seq, true, "")
		return
	}
	if len(webhook.Events) > 0 && !containsString(webhook.Events, event) {
		_ = d.Store.MarkWebhookDelivery(ctx, webhook.ID, seq, true, "")
		return
	}
	if webhook.JQL != "" && action.EntityType == models.EntityIssue {
		if d.Checker == nil || d.Checker.Search == nil {
			_ = d.Store.MarkWebhookDelivery(ctx, webhook.ID, seq, false, "JQL checker is not configured")
			return
		}
		match, err := d.Checker.Search(ctx, workspaceID, fmt.Sprintf(`(%s) AND key = "%s"`, webhook.JQL, actionKey(action)))
		if err != nil {
			_ = d.Store.MarkWebhookDelivery(ctx, webhook.ID, seq, false, err.Error())
			return
		}
		if !match {
			_ = d.Store.MarkWebhookDelivery(ctx, webhook.ID, seq, true, "")
			return
		}
	}
	payload, err := json.Marshal(map[string]any{
		"webhookEvent": event,
		"timestamp":    action.CreatedAt,
		"action":       action,
	})
	if err != nil {
		_ = d.Store.MarkWebhookDelivery(ctx, webhook.ID, seq, false, err.Error())
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook.URL, bytes.NewReader(payload))
	if err != nil {
		_ = d.Store.MarkWebhookDelivery(ctx, webhook.ID, seq, false, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ZZIRA-Event", event)
	if d.Client == nil {
		_ = d.Store.MarkWebhookDelivery(ctx, webhook.ID, seq, false, "webhook HTTP client is not configured")
		return
	}
	resp, err := d.Client.Do(req)
	if err != nil {
		_ = d.Store.MarkWebhookDelivery(ctx, webhook.ID, seq, false, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		_ = d.Store.MarkWebhookDelivery(ctx, webhook.ID, seq, true, "")
		return
	}
	_ = d.Store.MarkWebhookDelivery(ctx, webhook.ID, seq, false, fmt.Sprintf("http %d", resp.StatusCode))
}

func actionKey(a *models.Action) string {
	if a.EntityType == models.EntityIssue {
		return a.EntityID
	}
	return ""
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
