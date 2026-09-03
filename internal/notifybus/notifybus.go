// Package notifybus fans Postgres NOTIFY pokes out to in-process SSE
// subscribers. Each replica LISTENs directly on the fixed channel
// "zzira_actions" (payload "<workspaceID>|<seq>"), so any replica can serve
// any client and the bus has no external dependency.
package notifybus

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Channel is the fixed NOTIFY channel; store mutations publish on it.
const Channel = "zzira_actions"

type Bus struct {
	mu   sync.Mutex
	subs map[string]map[chan int64]struct{}
}

func New() *Bus {
	return &Bus{subs: map[string]map[chan int64]struct{}{}}
}

// Subscribe registers a listener for a workspace's head pokes.
func (b *Bus) Subscribe(workspaceID string) (chan int64, func()) {
	ch := make(chan int64, 8)
	var cancelOnce sync.Once
	b.mu.Lock()
	if b.subs[workspaceID] == nil {
		b.subs[workspaceID] = map[chan int64]struct{}{}
	}
	b.subs[workspaceID][ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		cancelOnce.Do(func() {
			b.mu.Lock()
			delete(b.subs[workspaceID], ch)
			if len(b.subs[workspaceID]) == 0 {
				delete(b.subs, workspaceID)
			}
			close(ch)
			b.mu.Unlock()
		})
	}
}

func (b *Bus) publish(workspaceID string, head int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs[workspaceID] {
		select {
		case ch <- head:
		default: // slow subscriber: drop the poke; the client pulls by checkpoint anyway
		}
	}
}

// Listen consumes notifications until ctx is done. Listen failures are
// returned; the caller owns the (loud) restart policy.
func (b *Bus) Listen(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		return err
	}
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		parts := strings.SplitN(n.Payload, "|", 2)
		if len(parts) != 2 {
			continue
		}
		seq, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			continue
		}
		b.publish(parts[0], seq)
	}
}
