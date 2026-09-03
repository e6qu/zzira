package api3

import (
	"encoding/json"
	"net/http"
)

// issueWatchers implements Jira's issue watcher resource. This deployment
// currently grants users control over their own subscription only.
func (h *Handler) issueWatchers(w http.ResponseWriter, r *http.Request, idOrKey string) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	issue, e := h.resolveIssue(r, wsID, idOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	watcherIDs, err := h.Store.WatchersByIssue(r.Context(), issue.ID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}

	switch r.Method {
	case http.MethodGet:
		watchers := make([]map[string]any, 0, len(watcherIDs))
		isWatching := false
		for _, watcherID := range watcherIDs {
			if watcherID == userID {
				isWatching = true
			}
			user, err := h.Store.MemberByID(r.Context(), wsID, watcherID)
			if err == nil {
				watchers = append(watchers, h.userBean(user))
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"self":       h.BaseURL + "/rest/api/3/issue/" + issue.Key + "/watchers",
			"isWatching": isWatching, "watchCount": len(watcherIDs), "watchers": watchers,
		})
	case http.MethodPost:
		accountID := userID
		if r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&accountID); err != nil || accountID == "" {
				jiraError(w, http.StatusBadRequest, "The request body must be an account ID string.")
				return
			}
		}
		if accountID != userID {
			jiraError(w, http.StatusForbidden, "You may only change your own watcher subscription.")
			return
		}
		if _, err := h.Commands.SetWatching(r.Context(), userID, wsID, issue.ID, true); err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		accountID := r.URL.Query().Get("accountId")
		if accountID == "" {
			accountID = userID
		}
		if accountID != userID {
			jiraError(w, http.StatusForbidden, "You may only change your own watcher subscription.")
			return
		}
		if _, err := h.Commands.SetWatching(r.Context(), userID, wsID, issue.ID, false); err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
