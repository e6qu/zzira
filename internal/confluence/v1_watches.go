package confluence

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

func v1WatchPage(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	start, limit := 0, 200
	if raw := r.URL.Query().Get("start"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			failure(w, 400, "start must be zero or greater.")
			return 0, 0, false
		}
		start = value
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > 1000 {
			failure(w, 400, "limit must be between 0 and 1000.")
			return 0, 0, false
		}
		limit = value
	}
	return start, limit, true
}

func pageUsers(users []*models.User, start, limit int) ([]*models.User, int) {
	start = min(start, len(users))
	end := min(start+limit, len(users))
	return users[start:end], start
}

func (h *V1Handler) v1WatchUserBean(user *models.User) map[string]any {
	picture := user.PictureURL
	if picture == "" {
		picture = user.AvatarURL
	}
	return map[string]any{
		"type": "known", "username": user.Username, "userKey": user.ID,
		"accountId": user.ID, "displayName": user.DisplayName, "timeZone": user.TimeZone,
		"profilePicture": map[string]any{"path": picture, "width": 48, "height": 48, "isDefault": picture == ""},
		"operations":     []any{}, "externalCollaborator": false, "isGuest": false,
		"isExternalCollaborator": false, "accountType": "atlassian", "email": user.Email,
		"publicName": user.DisplayName, "personalSpace": nil,
	}
}

func (h *V1Handler) v1ContentWatches(w http.ResponseWriter, r *http.Request, ws, actor, pageID, notificationType string) {
	if !supportedQuery(w, r, "start", "limit") {
		return
	}
	start, limit, ok := v1WatchPage(w, r)
	if !ok {
		return
	}
	page, err := h.Store.WikiPage(r.Context(), ws, actor, pageID)
	if err != nil {
		writeError(w, err)
		return
	}
	if page.Status != "current" {
		failure(w, 404, "Content does not exist or you do not have permission to view it.")
		return
	}
	targetType, targetID, watchType := "content", page.ID, "page"
	if notificationType == "created" {
		space, spaceErr := h.Store.WikiSpace(r.Context(), ws, actor, page.SpaceID)
		if spaceErr != nil {
			writeError(w, spaceErr)
			return
		}
		targetType, targetID, watchType = "space", space.Key, "space"
	}
	users, err := h.Store.WikiWatchers(r.Context(), ws, actor, targetType, targetID)
	if err != nil {
		writeError(w, err)
		return
	}
	users, start = pageUsers(users, start, limit)
	contentID, parseErr := strconv.ParseInt(page.ID, 10, 64)
	if parseErr != nil {
		failure(w, 500, "Content ID is invalid.")
		return
	}
	results := make([]any, 0, len(users))
	for _, user := range users {
		results = append(results, map[string]any{"type": watchType, "watcher": h.v1WatchUserBean(user), "contentId": contentID})
	}
	respond(w, 200, map[string]any{"results": results, "start": start, "limit": limit, "size": len(results), "_links": map[string]string{"base": h.BaseURL + "/wiki"}})
}

func (h *V1Handler) v1SpaceWatchers(w http.ResponseWriter, r *http.Request, ws, actor, spaceKey string) {
	if !supportedQuery(w, r, "start", "limit") {
		return
	}
	start, limit, ok := v1WatchPage(w, r)
	if !ok {
		return
	}
	space, err := h.Store.WikiSpaceByKey(r.Context(), ws, actor, spaceKey)
	if err != nil {
		writeError(w, err)
		return
	}
	users, err := h.Store.WikiWatchers(r.Context(), ws, actor, "space", space.Key)
	if err != nil {
		writeError(w, err)
		return
	}
	users, start = pageUsers(users, start, limit)
	results := make([]any, 0, len(users))
	for _, user := range users {
		results = append(results, map[string]any{"type": "space", "watcher": h.v1WatchUserBean(user), "spaceKey": space.Key})
	}
	respond(w, 200, map[string]any{"results": results, "start": start, "limit": limit, "size": len(results), "_links": map[string]string{"base": h.BaseURL + "/wiki"}})
}

func (h *V1Handler) v1WatchTargetUser(w http.ResponseWriter, r *http.Request, ws, actor string) (*models.User, bool) {
	selected := ""
	for _, name := range []string{"accountId", "key", "username"} {
		if value := strings.TrimSpace(r.URL.Query().Get(name)); value != "" {
			if selected != "" {
				failure(w, 400, "Specify only one user selector.")
				return nil, false
			}
			selected = value
		}
	}
	if selected == "" {
		selected = actor
	}
	user, err := h.Store.ResolveWikiWatchUser(r.Context(), ws, selected)
	if err != nil {
		writeError(w, err)
		return nil, false
	}
	if user.ID != actor {
		admin, adminErr := h.Store.IsAdmin(r.Context(), ws, actor)
		if adminErr != nil {
			writeError(w, adminErr)
			return nil, false
		}
		if !admin {
			failure(w, 403, "Only an administrator can manage watches for another user.")
			return nil, false
		}
	}
	return user, true
}

func (h *V1Handler) v1UserWatch(w http.ResponseWriter, r *http.Request, ws, actor, targetType, targetID string) {
	if targetType != "content" && targetType != "label" && targetType != "space" {
		failure(w, 404, "Watch target is not supported.")
		return
	}
	if r.Method != "GET" && r.Method != "POST" && r.Method != "DELETE" {
		failure(w, 405, "Method not allowed.")
		return
	}
	if !supportedQuery(w, r, "accountId", "key", "username") {
		return
	}
	needsNoCheck := r.Method == "DELETE" && targetType == "content" || r.Method == "POST" && (targetType == "label" || targetType == "space")
	if needsNoCheck && !strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Atlassian-Token")), "no-check") {
		failure(w, 403, "X-Atlassian-Token: no-check is required.")
		return
	}
	user, ok := h.v1WatchTargetUser(w, r, ws, actor)
	if !ok {
		return
	}
	if r.Method == "GET" {
		watching, err := h.Store.WikiWatchStatus(r.Context(), ws, actor, user.ID, targetType, targetID)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, map[string]bool{"watching": watching})
		return
	}
	if err := h.Commands.SetWikiWatch(r.Context(), ws, actor, user.ID, targetType, targetID, r.Method == "POST"); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}
