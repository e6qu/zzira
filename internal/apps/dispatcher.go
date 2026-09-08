package apps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/secretbox"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/webhooks"
	"github.com/jackc/pgx/v5"
)

const (
	maxAppWebhookActions = 500
	maxAppSchedules      = 100
	maxAppDeliveries     = 100
)

// OutboundRunner turns descriptor-declared callbacks into durable, signed
// HTTP requests. Webhook cursors, schedules, and retry state live in the
// database so another replica can resume after a process stops.
type OutboundRunner struct {
	Store   *store.Store
	Secrets *secretbox.Box
	Client  *http.Client
	Search  func(context.Context, string, string) (bool, error)
	Now     func() time.Time
}

func (r *OutboundRunner) Run(ctx context.Context, workspaceID string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.DrainOnce(ctx, workspaceID); err != nil {
				fmt.Printf("apps: outbound runtime: %v\n", err)
			}
		}
	}
}

// DrainOnce performs a bounded pass over webhook actions, due schedules, and
// pending deliveries. It is exported so operational checks can drain without
// starting a background goroutine.
func (r *OutboundRunner) DrainOnce(ctx context.Context, workspaceID string) error {
	if r.Store == nil || r.Store.Pool == nil {
		return errors.New("app outbound store is not configured")
	}
	if r.Secrets == nil {
		return errors.New("app credential encryption is not configured")
	}
	if r.Client == nil {
		return errors.New("app outbound HTTP client is not configured")
	}
	if err := r.enrollWebhooks(ctx, workspaceID); err != nil {
		return fmt.Errorf("enroll webhooks: %w", err)
	}
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	for count := 0; count < maxAppSchedules; count++ {
		enqueued, err := r.Store.EnqueueDueAppSchedule(ctx, workspaceID, now)
		if err != nil {
			return fmt.Errorf("enqueue schedule: %w", err)
		}
		if !enqueued {
			break
		}
	}
	for count := 0; count < maxAppDeliveries; count++ {
		delivery, err := r.Store.ClaimAppOutboundDelivery(ctx, workspaceID, now)
		if errors.Is(err, pgx.ErrNoRows) {
			break
		}
		if err != nil {
			return fmt.Errorf("claim delivery: %w", err)
		}
		status, deliveryErr := r.deliver(ctx, workspaceID, delivery, now)
		if err := r.Store.CompleteAppOutboundDelivery(ctx, delivery.ID, status, deliveryErr, now); err != nil {
			return fmt.Errorf("complete delivery %s: %w", delivery.ID, err)
		}
	}
	return nil
}

func (r *OutboundRunner) enrollWebhooks(ctx context.Context, workspaceID string) error {
	head, err := r.Store.Head(ctx, workspaceID)
	if err != nil {
		return err
	}
	hooks, err := r.Store.ActiveAppWebhooks(ctx, workspaceID)
	if err != nil {
		return err
	}
	processed := 0
	for _, hook := range hooks {
		for hook.LastSeq < head && processed < maxAppWebhookActions {
			seq := hook.LastSeq + 1
			action, err := r.Store.ActionBySeq(ctx, workspaceID, seq)
			if errors.Is(err, pgx.ErrNoRows) {
				advanced, advanceErr := r.Store.AdvanceAppWebhook(ctx, hook, seq, "", nil, false)
				if advanceErr != nil {
					return advanceErr
				}
				if !advanced {
					break
				}
				hook.LastSeq = seq
				processed++
				continue
			}
			if err != nil {
				return err
			}
			event, ok := webhooks.EventFor(action)
			deliver := ok && stringIn(hook.Events, event)
			if deliver && hook.JQL != "" && action.EntityType == models.EntityIssue {
				if r.Search == nil {
					return errors.New("app webhook JQL search is not configured")
				}
				matches, err := r.Search(ctx, workspaceID, fmt.Sprintf("(%s) AND key = %s", hook.JQL, strconv.Quote(appActionKey(action))))
				if err != nil {
					return fmt.Errorf("evaluate webhook %s JQL: %w", hook.Key, err)
				}
				deliver = matches
			}
			var payload json.RawMessage
			if deliver {
				payload, err = json.Marshal(map[string]any{
					"webhookEvent": event,
					"timestamp":    action.CreatedAt,
					"moduleKey":    hook.Key,
					"action":       action,
				})
				if err != nil {
					return err
				}
			}
			advanced, err := r.Store.AdvanceAppWebhook(ctx, hook, seq, event, payload, deliver)
			if err != nil {
				return err
			}
			if !advanced {
				break
			}
			hook.LastSeq = seq
			processed++
		}
		if processed >= maxAppWebhookActions {
			break
		}
	}
	return nil
}

func (r *OutboundRunner) deliver(ctx context.Context, workspaceID string, delivery *models.AppOutboundDelivery, now time.Time) (int, error) {
	secret, err := r.Secrets.Open(delivery.SecretCiphertext, workspaceID+"/"+delivery.AppKey)
	if err != nil {
		return 0, fmt.Errorf("open app credentials: %w", err)
	}
	base, err := url.Parse(delivery.BaseURL)
	if err != nil {
		return 0, fmt.Errorf("parse app base URL: %w", err)
	}
	reference, err := url.Parse(delivery.Path)
	if err != nil {
		return 0, fmt.Errorf("parse callback path: %w", err)
	}
	target := base.ResolveReference(reference)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(delivery.Payload))
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	timestamp := now.Unix()
	requestTarget := request.URL.EscapedPath()
	if request.URL.RawQuery != "" {
		requestTarget += "?" + request.URL.RawQuery
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Zzira-App-Key", delivery.AppKey)
	request.Header.Set("X-Zzira-App-Timestamp", strconv.FormatInt(timestamp, 10))
	request.Header.Set("X-Zzira-App-Request-Id", delivery.ID)
	request.Header.Set("X-Zzira-App-Signature", SignRequest(secret, timestamp, delivery.ID, request.Method, requestTarget, delivery.Payload))
	request.Header.Set("X-Zzira-App-Event", delivery.Event)
	response, err := r.Client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("send request: %w", err)
	}
	_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return response.StatusCode, errors.Join(readErr, closeErr)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return response.StatusCode, fmt.Errorf("http %d", response.StatusCode)
	}
	return response.StatusCode, nil
}

func stringIn(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func appActionKey(action *models.Action) string {
	if action.EntityType != models.EntityIssue {
		return ""
	}
	var payload models.IssueUpdatePayload
	if json.Unmarshal(action.Payload, &payload) == nil && payload.Issue.Key != "" {
		return payload.Issue.Key
	}
	return action.EntityID
}
