package store

import (
	"context"
	"time"
)

// TakeProviderRequest counts one request by a caller to a DevOps provider API
// in the current one-minute window and reports whether it is within the
// limit, how many requests remain, and when the window resets.
func (s *Store) TakeProviderRequest(ctx context.Context, workspaceID, principalID, module string, limit int, now time.Time) (int, time.Time, bool, error) {
	var count int
	var windowStart time.Time
	err := s.Pool.QueryRow(ctx, `INSERT INTO provider_rate_windows(workspace_id,principal_id,module,window_start,count)
		VALUES($1,$2,$3,date_trunc('minute',$4::timestamptz),1)
		ON CONFLICT (workspace_id,principal_id,module) DO UPDATE SET
		  count=CASE WHEN provider_rate_windows.window_start=date_trunc('minute',$4::timestamptz) THEN provider_rate_windows.count+1 ELSE 1 END,
		  window_start=date_trunc('minute',$4::timestamptz)
		RETURNING count,window_start`, workspaceID, principalID, module, now).Scan(&count, &windowStart)
	if err != nil {
		return 0, time.Time{}, false, err
	}
	remaining := limit - count
	if remaining < 0 {
		remaining = 0
	}
	return remaining, windowStart.Add(time.Minute), count <= limit, nil
}
