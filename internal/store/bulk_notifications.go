package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type bulkNotificationKey struct{}

// BulkNotificationCollector gathers the email lines issue events would send
// during a bulk operation, so each recipient gets one bulk change email
// instead of one message per work item.
type BulkNotificationCollector struct {
	mu    sync.Mutex
	lines map[string][]string
}

// WithBulkNotifications routes issue event emails raised under ctx into the
// collector.
func WithBulkNotifications(ctx context.Context, collector *BulkNotificationCollector) context.Context {
	return context.WithValue(ctx, bulkNotificationKey{}, collector)
}

func bulkNotifications(ctx context.Context) *BulkNotificationCollector {
	collector, _ := ctx.Value(bulkNotificationKey{}).(*BulkNotificationCollector)
	return collector
}

func (c *BulkNotificationCollector) add(recipient, line string) {
	recipient = strings.TrimSpace(recipient)
	if recipient == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lines == nil {
		c.lines = map[string][]string{}
	}
	key := strings.ToLower(recipient)
	for _, existing := range c.lines[key] {
		if existing == line {
			return
		}
	}
	c.lines[key] = append(c.lines[key], line)
}

// Recipients lists who the collected changes would notify.
func (c *BulkNotificationCollector) Recipients() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	recipients := make([]string, 0, len(c.lines))
	for recipient := range c.lines {
		recipients = append(recipients, recipient)
	}
	sort.Strings(recipients)
	return recipients
}

// WriteBulkNotificationEmails sends each recipient one bulk change email
// listing the work items the task changed for them. A retried task sends at
// most one email per recipient.
func (s *Store) WriteBulkNotificationEmails(ctx context.Context, workspaceID, taskID, operation string, collector *BulkNotificationCollector) error {
	collector.mu.Lock()
	lines := make(map[string][]string, len(collector.lines))
	for recipient, entries := range collector.lines {
		lines[recipient] = append([]string(nil), entries...)
	}
	collector.mu.Unlock()
	for recipient, entries := range lines {
		subject := fmt.Sprintf("Bulk %s: %d work items", operation, len(entries))
		body := "The following work items were changed in one bulk " + operation + ":\n\n" + strings.Join(entries, "\n")
		dedupe := fmt.Sprintf("bulk-notification:%s:%s:%s", workspaceID, taskID, recipient)
		if _, err := s.Pool.Exec(ctx, `INSERT INTO email_outbox(workspace_id,recipient,subject,body,dedupe_key) VALUES($1,$2,$3,$4,$5) ON CONFLICT(dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING`, workspaceID, recipient, subject, body, dedupe); err != nil {
			return err
		}
	}
	return nil
}
