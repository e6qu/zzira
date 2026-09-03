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
	if err != nil {
		return d.markFailed(ctx, webhook.ID, seq, fmt.Errorf("load action: %w", err))
	}
	event, ok := EventFor(action)
	if !ok {
		return d.mark(ctx, webhook.ID, seq, true, "")
	}
	if len(webhook.Events) > 0 && !containsString(webhook.Events, event) {
		return d.mark(ctx, webhook.ID, seq, true, "")
	}
	if webhook.JQL != "" && action.EntityType == models.EntityIssue {
		if d.Checker == nil || d.Checker.Search == nil {
			return d.markFailed(ctx, webhook.ID, seq, errors.New("JQL checker is not configured"))
		}
		match, err := d.Checker.Search(ctx, workspaceID, fmt.Sprintf(`(%s) AND key = %s`, webhook.JQL, strconv.Quote(actionKey(action))))
		if err != nil {
			return d.markFailed(ctx, webhook.ID, seq, fmt.Errorf("evaluate JQL: %w", err))
		}
		if !match {
			return d.mark(ctx, webhook.ID, seq, true, "")
		}
	}
	payload, err := json.Marshal(map[string]any{
		"webhookEvent": event,
		"timestamp":    action.CreatedAt,
		"action":       action,
	})
	if err != nil {
		return d.markFailed(ctx, webhook.ID, seq, fmt.Errorf("encode payload: %w", err))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook.URL, bytes.NewReader(payload))
	if err != nil {
		return d.markFailed(ctx, webhook.ID, seq, fmt.Errorf("create request: %w", err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ZZIRA-Event", event)
	if d.Client == nil {
		return d.markFailed(ctx, webhook.ID, seq, errors.New("webhook HTTP client is not configured"))
	}
	resp, err := d.Client.Do(req)
	if err != nil {
		return d.markFailed(ctx, webhook.ID, seq, fmt.Errorf("send request: %w", err))
	}
	_, readErr := io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	closeErr := resp.Body.Close()
	if readErr != nil || closeErr != nil {
		return d.markFailed(ctx, webhook.ID, seq, errors.Join(readErr, closeErr))
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return d.mark(ctx, webhook.ID, seq, true, "")
	}
	return d.markFailed(ctx, webhook.ID, seq, fmt.Errorf("http %d", resp.StatusCode))
}

func (d *Dispatcher) markFailed(ctx context.Context, webhookID string, seq int64, cause error) error {
	if err := d.mark(ctx, webhookID, seq, false, cause.Error()); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func (d *Dispatcher) mark(ctx context.Context, webhookID string, seq int64, delivered bool, lastErr string) error {
	if err := d.Store.MarkWebhookDelivery(ctx, webhookID, seq, delivered, lastErr); err != nil {
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
