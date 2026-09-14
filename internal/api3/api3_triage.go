package api3

import (
	"bytes"
	"encoding/json"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/models"
	"io"
	"net/http"
)

// issueVotes implements Jira's self-service issue vote resource.
func (h *Handler) issueVotes(w http.ResponseWriter, r *http.Request, idOrKey string) {
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
	switch r.Method {
	case http.MethodGet:
		voterIDs, err := h.Store.VotersByIssue(r.Context(), issue.ID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		// Voter details need View voters and watchers; the count does not.
		seeVoters, err := h.hasProjectPermission(r.Context(), wsID, userID, issue.ProjectID, issue.ID, "VIEW_VOTERS_AND_WATCHERS")
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		voters := make([]map[string]any, 0, len(voterIDs))
		hasVoted := false
		for _, voterID := range voterIDs {
			if voterID == userID {
				hasVoted = true
			}
			if !seeVoters {
				continue
			}
			if user, err := h.Store.MemberByID(r.Context(), wsID, voterID); err == nil {
				voters = append(voters, h.userBeanFor(r.Context(), user))
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"self": h.BaseURL + "/rest/api/3/issue/" + issue.Key + "/votes", "votes": len(voterIDs),
			"hasVoted": hasVoted, "voters": voters,
		})
	case http.MethodPost:
		if _, err := h.Commands.SetVoting(r.Context(), userID, wsID, issue.ID, true); err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if _, err := h.Commands.SetVoting(r.Context(), userID, wsID, issue.ID, false); err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// issueWatchers serves GET, POST and DELETE /issue/{key}/watchers. Watchers are
// listed to people with View voters and watchers; changing someone else's
// subscription needs Manage watchers.
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
	configuration, err := h.Store.JiraSiteConfiguration(r.Context(), wsID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !configuration.WatchingEnabled {
		jiraError(w, http.StatusNotFound, "Watching is disabled.")
		return
	}
	hasPermission := func(key string) bool {
		allowed, permErr := h.Store.HasProjectPermission(r.Context(), wsID, userID, issue.ProjectID, issue.ID, key)
		return permErr == nil && allowed
	}
	changeFor := func(accountID string) (*models.User, bool) {
		if issue.ArchivedAt != "" {
			jiraError(w, http.StatusBadRequest, "The issue is archived and can't be changed.")
			return nil, false
		}
		if accountID != userID && !hasPermission("MANAGE_WATCHERS") {
			jiraError(w, http.StatusForbidden, "You do not have the permission to manage the watcher list.")
			return nil, false
		}
		target, userErr := h.Store.SiteUser(r.Context(), wsID, accountID)
		if userErr != nil {
			jiraError(w, http.StatusNotFound, "The user does not exist.")
			return nil, false
		}
		return target, true
	}
	switch r.Method {
	case http.MethodGet:
		watcherIDs, err := h.Store.WatchersByIssue(r.Context(), issue.ID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		watchers := []map[string]any{}
		if hasPermission("VIEW_VOTERS_AND_WATCHERS") {
			for _, watcherID := range watcherIDs {
				if watcher, userErr := h.Store.SiteUser(r.Context(), wsID, watcherID); userErr == nil {
					watchers = append(watchers, h.fullUserBean(watcher, watcherID == userID))
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"self":       h.BaseURL + "/rest/api/3/issue/" + issue.Key + "/watchers",
			"isWatching": containsString(watcherIDs, userID), "watchCount": len(watcherIDs), "watchers": watchers,
		})
	case http.MethodPost:
		accountID := userID
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<10))
		if err != nil {
			jiraError(w, http.StatusBadRequest, "The request body must be an account ID string.")
			return
		}
		if len(bytes.TrimSpace(body)) > 0 {
			if json.Unmarshal(body, &accountID) != nil || accountID == "" {
				jiraError(w, http.StatusBadRequest, "The request body must be an account ID string.")
				return
			}
		}
		target, ok := changeFor(accountID)
		if !ok {
			return
		}
		if accountID == userID {
			_, err = h.Commands.SetWatching(r.Context(), userID, wsID, issue.ID, true)
		} else {
			if visible, visErr := authz.CanSeeIssue(r.Context(), h.Store, wsID, issue.ProjectID, target.ID, issue.ID, issue.SecurityLevelID); visErr != nil || !visible {
				jiraError(w, http.StatusNotFound, "The user can't see the issue.")
				return
			}
			_, err = h.Store.AddWatcher(r.Context(), userID, wsID, issue.ID, accountID)
		}
		if err != nil {
			issueCommandError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		q := r.URL.Query()
		accountID := q.Get("accountId")
		if accountID == "" {
			if q.Get("username") != "" {
				jiraError(w, http.StatusBadRequest, "Usernames aren't supported; use accountId.")
			} else {
				jiraError(w, http.StatusBadRequest, "accountId is required.")
			}
			return
		}
		if _, ok := changeFor(accountID); !ok {
			return
		}
		if accountID == userID {
			_, err = h.Commands.SetWatching(r.Context(), userID, wsID, issue.ID, false)
		} else {
			_, err = h.Store.RemoveWatcher(r.Context(), userID, wsID, issue.ID, accountID)
		}
		if err != nil {
			issueCommandError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w)
	}
}
