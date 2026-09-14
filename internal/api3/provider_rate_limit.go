package api3

import (
	"net/http"
	"strconv"
	"time"
)

// defaultProviderRateLimit is how many requests one caller may make to one
// DevOps provider API in a minute.
const defaultProviderRateLimit = 1000

// providerRateLimited counts a request against the caller's one-minute window
// for a DevOps provider API, reporting the window in Jira Software's rate
// limit headers. When the window is spent it answers 429 with Retry-After and
// reports true.
func (h *Handler) providerRateLimited(w http.ResponseWriter, r *http.Request, workspaceID, actorID, module string, providerShape bool) bool {
	limit := h.ProviderRateLimit
	if limit <= 0 {
		limit = defaultProviderRateLimit
	}
	now := time.Now().UTC()
	remaining, reset, allowed, err := h.Store.TakeProviderRequest(r.Context(), workspaceID, actorID, module, limit, now)
	if err != nil {
		// Counting is a safeguard; a failure to count does not refuse data.
		return false
	}
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	w.Header().Set("X-RateLimit-Reset", reset.Format(time.RFC3339))
	if allowed {
		return false
	}
	retryAfter := int(reset.Sub(now).Seconds()) + 1
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	if providerShape {
		providerError(w, http.StatusTooManyRequests, "API rate limit has been exceeded.")
	} else {
		jiraError(w, http.StatusTooManyRequests, "API rate limit has been exceeded.")
	}
	return true
}
