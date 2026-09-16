package automation

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ErrWebhookUnknown reports a webhook address that names no rule, and
// ErrWebhookForbidden a request that did not present the rule's secret.
var (
	ErrWebhookUnknown   = errors.New("no rule is called at that address")
	ErrWebhookForbidden = errors.New("the webhook secret is missing or wrong")
	ErrWebhookDisabled  = errors.New("the rule is disabled")
)

// webhookBody is what an incoming webhook request may name. Jira's trigger
// runs for the work items the body names, and the whole body reaches the
// rule's actions as {{webhookData}}.
type webhookBody struct {
	Issues []string `json:"issues"`
}

// TriggerWebhook runs the rule an incoming webhook address names. It queues a
// run for each work item the request names, or one run with no work item when
// it names none, and reports how many it queued.
func (s *Service) TriggerWebhook(ctx context.Context, token, secret string, body json.RawMessage) (int, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, ErrWebhookUnknown
	}
	var (
		uuid, workspaceID, state, ruleSecret string
	)
	err := s.Store.Pool.QueryRow(ctx, `
		SELECT uuid::text,workspace_id,state,COALESCE(webhook_secret,'')
		FROM automation_rules WHERE webhook_token=$1`, token).Scan(&uuid, &workspaceID, &state, &ruleSecret)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrWebhookUnknown
	}
	if err != nil {
		return 0, err
	}
	// The address alone is a secret, and the rule's secret is checked in
	// constant time so a wrong one cannot be found by timing.
	if ruleSecret != "" && subtle.ConstantTimeCompare([]byte(ruleSecret), []byte(strings.TrimSpace(secret))) != 1 {
		return 0, ErrWebhookForbidden
	}
	if state != "ENABLED" {
		return 0, ErrWebhookDisabled
	}
	if len(body) == 0 || strings.TrimSpace(string(body)) == "" {
		body = json.RawMessage(`{}`)
	}
	if !json.Valid(body) {
		return 0, fmt.Errorf("the webhook body must be JSON")
	}
	var named webhookBody
	_ = json.Unmarshal(body, &named)

	issueIDs := []string{}
	for _, key := range named.Issues {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		issue, err := s.Store.IssueByIDOrKey(ctx, workspaceID, key)
		if err != nil {
			// A work item the site does not hold is skipped, as Jira skips
			// what its webhook names and cannot find.
			continue
		}
		issueIDs = append(issueIDs, issue.ID)
		if len(issueIDs) == 100 {
			break
		}
	}

	queued := 0
	queue := func(issueID string) error {
		id, err := NewUUIDv7()
		if err != nil {
			return err
		}
		tag, err := s.Store.Pool.Exec(ctx, `
			INSERT INTO automation_runs(id,rule_uuid,scheduled_for,state,issue_id,webhook_data)
			VALUES($1,$2,clock_timestamp(),'PENDING',NULLIF($3,''),$4)
			ON CONFLICT (rule_uuid,scheduled_for) WHERE trigger_seq IS NULL DO NOTHING`, id, uuid, issueID, body)
		if err != nil {
			return err
		}
		queued += int(tag.RowsAffected())
		return nil
	}
	if len(issueIDs) == 0 {
		if err := queue(""); err != nil {
			return 0, err
		}
		return queued, nil
	}
	for _, issueID := range issueIDs {
		if err := queue(issueID); err != nil {
			return queued, err
		}
	}
	return queued, nil
}

// IncomingWebhook serves POST /pro/hooks/{token}, the address Jira Automation
// gives an incoming webhook rule. It is deliberately unauthenticated: the
// address and the rule's secret are the credentials.
func (h *Handler) IncomingWebhook(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodPost {
		automationError(w, http.StatusMethodNotAllowed, "automation.method.not_allowed", "method not allowed", "")
		return
	}
	// Jira's incoming webhook accepts a request with no body at all, so an
	// empty one is not an error here; the rule then simply has no data.
	body, err := io.ReadAll(io.LimitReader(r.Body, (2<<20)+1))
	if err != nil || len(body) > 2<<20 {
		automationError(w, http.StatusBadRequest, "automation.webhook.body", "The webhook body is too large or unreadable", "")
		return
	}
	secret := r.Header.Get("X-Automation-Webhook-Token")
	queued, err := h.Service.TriggerWebhook(r.Context(), token, secret, body)
	switch {
	case errors.Is(err, ErrWebhookUnknown):
		automationError(w, http.StatusNotFound, "automation.webhook.not_found", "No rule is called at that address", "")
		return
	case errors.Is(err, ErrWebhookForbidden):
		automationError(w, http.StatusForbidden, "automation.webhook.forbidden", "The webhook secret is missing or wrong", "")
		return
	case errors.Is(err, ErrWebhookDisabled):
		automationError(w, http.StatusConflict, "automation.webhook.disabled", "The rule is disabled", "")
		return
	case err != nil:
		automationError(w, http.StatusBadRequest, "automation.webhook.rejected", err.Error(), "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "queued": queued})
}
