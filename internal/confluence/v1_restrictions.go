package confluence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

type v1RestrictionInput struct {
	Operation    string `json:"operation"`
	Restrictions struct {
		User  json.RawMessage                 `json:"user"`
		Group []models.WikiRestrictionSubject `json:"group"`
	} `json:"restrictions"`
}

func decodeV1Restrictions(w http.ResponseWriter, r *http.Request) ([]models.WikiPageRestriction, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		failure(w, 400, "Invalid restriction request.")
		return nil, false
	}
	var input []v1RestrictionInput
	if err := json.Unmarshal(raw, &input); err != nil {
		var wrapper struct {
			Results []v1RestrictionInput `json:"results"`
		}
		if wrapperErr := json.Unmarshal(raw, &wrapper); wrapperErr != nil || wrapper.Results == nil {
			failure(w, 400, "Expected a restriction array or results object.")
			return nil, false
		}
		input = wrapper.Results
	}
	result := make([]models.WikiPageRestriction, 0, len(input))
	for _, item := range input {
		entry := models.WikiPageRestriction{Operation: item.Operation, Users: []models.WikiRestrictionSubject{}, Groups: []models.WikiRestrictionSubject{}}
		for _, group := range item.Restrictions.Group {
			group.Type = "group"
			entry.Groups = append(entry.Groups, group)
		}
		if len(item.Restrictions.User) > 0 && string(item.Restrictions.User) != "null" {
			var users []models.WikiRestrictionSubject
			if err := json.Unmarshal(item.Restrictions.User, &users); err != nil {
				var wrapper struct {
					Results []models.WikiRestrictionSubject `json:"results"`
				}
				if wrapperErr := json.Unmarshal(item.Restrictions.User, &wrapper); wrapperErr != nil {
					failure(w, 400, "Invalid user restrictions.")
					return nil, false
				}
				users = wrapper.Results
			}
			for _, user := range users {
				user.Type = "user"
				entry.Users = append(entry.Users, user)
			}
		}
		result = append(result, entry)
	}
	return result, true
}

func (h *V1Handler) restrictionBean(pageID string, restriction models.WikiPageRestriction) map[string]any {
	users := make([]any, 0, len(restriction.Users))
	for _, user := range restriction.Users {
		users = append(users, map[string]any{"type": "known", "accountType": "atlassian", "accountId": user.AccountID, "displayName": user.DisplayName, "publicName": user.DisplayName, "_links": map[string]string{"self": h.BaseURL + "/wiki/rest/api/user?accountId=" + user.AccountID}})
	}
	groups := make([]any, 0, len(restriction.Groups))
	for _, group := range restriction.Groups {
		groups = append(groups, map[string]any{"type": "group", "id": group.ID, "name": group.Name, "_links": map[string]string{"self": h.BaseURL + "/wiki/rest/api/group/by-id?id=" + group.ID}})
	}
	list := func(results []any) map[string]any {
		return map[string]any{"results": results, "start": 0, "limit": len(results), "size": len(results), "_links": map[string]string{}}
	}
	self := "/wiki/rest/api/content/" + pageID + "/restriction/byOperation/" + restriction.Operation
	return map[string]any{
		"operation":    restriction.Operation,
		"restrictions": map[string]any{"user": list(users), "group": list(groups), "_expandable": map[string]string{}},
		"_expandable":  map[string]string{"content": "/wiki/rest/api/content/" + pageID},
		"_links":       map[string]string{"base": h.BaseURL + "/wiki", "context": "/wiki", "self": strings.TrimPrefix(self, "/wiki")},
	}
}

func restrictionByOperation(restrictions []models.WikiPageRestriction, operation string) (models.WikiPageRestriction, bool) {
	for _, restriction := range restrictions {
		if restriction.Operation == operation {
			return restriction, true
		}
	}
	return models.WikiPageRestriction{}, false
}

func (h *V1Handler) restrictionArray(w http.ResponseWriter, r *http.Request, pageID string, restrictions []models.WikiPageRestriction) {
	start, limit := 0, 100
	if raw := r.URL.Query().Get("start"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			failure(w, 400, "start must be zero or greater.")
			return
		}
		start = value
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > 1000 {
			failure(w, 400, "limit must be between 0 and 1000.")
			return
		}
		limit = value
	}
	start = min(start, len(restrictions))
	end := min(start+limit, len(restrictions))
	beans := make([]any, 0, end-start)
	for _, restriction := range restrictions[start:end] {
		beans = append(beans, h.restrictionBean(pageID, restriction))
	}
	hashInput, _ := json.Marshal(restrictions)
	digest := sha256.Sum256(hashInput)
	respond(w, 200, map[string]any{"results": beans, "start": start, "limit": limit, "size": len(beans), "restrictionsHash": hex.EncodeToString(digest[:]), "_links": map[string]string{"base": h.BaseURL + "/wiki", "context": "/wiki", "self": strings.TrimPrefix(r.URL.Path, "/wiki")}})
}

func (h *V1Handler) contentRestrictions(w http.ResponseWriter, r *http.Request, ws, actor, pageID string) {
	allowed := []string{"expand"}
	if r.Method == "GET" {
		allowed = append(allowed, "start", "limit")
	}
	if !supportedQuery(w, r, allowed...) {
		return
	}
	switch r.Method {
	case "GET":
		restrictions, err := h.Store.WikiPageRestrictions(r.Context(), ws, actor, pageID)
		if err != nil {
			writeError(w, err)
			return
		}
		h.restrictionArray(w, r, pageID, restrictions)
	case "PUT", "POST":
		input, ok := decodeV1Restrictions(w, r)
		if !ok {
			return
		}
		mode := "replace"
		if r.Method == "POST" {
			mode = "add"
		}
		restrictions, err := h.Commands.SetWikiPageRestrictions(r.Context(), ws, actor, pageID, mode, input)
		if err != nil {
			writeError(w, err)
			return
		}
		h.restrictionArray(w, r, pageID, restrictions)
	case "DELETE":
		restrictions, err := h.Commands.SetWikiPageRestrictions(r.Context(), ws, actor, pageID, "clear", nil)
		if err != nil {
			writeError(w, err)
			return
		}
		h.restrictionArray(w, r, pageID, restrictions)
	default:
		failure(w, 405, "Method not allowed.")
	}
}

func (h *V1Handler) contentRestrictionsByOperation(w http.ResponseWriter, r *http.Request, ws, actor, pageID string) {
	if !supportedQuery(w, r, "expand") {
		return
	}
	restrictions, err := h.Store.WikiPageRestrictions(r.Context(), ws, actor, pageID)
	if err != nil {
		writeError(w, err)
		return
	}
	result := map[string]any{"_links": map[string]string{"base": h.BaseURL + "/wiki", "context": "/wiki", "self": strings.TrimPrefix(r.URL.Path, "/wiki")}}
	for _, restriction := range restrictions {
		result[restriction.Operation] = h.restrictionBean(pageID, restriction)
	}
	respond(w, 200, result)
}

func (h *V1Handler) contentRestrictionOperation(w http.ResponseWriter, r *http.Request, ws, actor, pageID, operation string) {
	if !supportedQuery(w, r, "expand", "start", "limit") {
		return
	}
	if !slices.Contains([]string{"read", "update"}, operation) {
		failure(w, 400, "operationKey must be read or update.")
		return
	}
	restrictions, err := h.Store.WikiPageRestrictions(r.Context(), ws, actor, pageID)
	if err != nil {
		writeError(w, err)
		return
	}
	restriction, _ := restrictionByOperation(restrictions, operation)
	respond(w, 200, h.restrictionBean(pageID, restriction))
}

func (h *V1Handler) contentRestrictionGroup(w http.ResponseWriter, r *http.Request, ws, actor, pageID, operation, groupID string) {
	if !supportedQuery(w, r) {
		return
	}
	subject := models.WikiRestrictionSubject{Type: "group", ID: groupID}
	switch r.Method {
	case "GET":
		exists, err := h.Store.WikiPageRestrictionSubject(r.Context(), ws, actor, pageID, operation, subject)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, exists)
		return
	case "PUT", "DELETE":
		_, err := h.Commands.SetWikiPageRestrictionSubject(r.Context(), ws, actor, pageID, operation, subject, r.Method == "PUT")
		if err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(200)
		return
	default:
		failure(w, 405, "Method not allowed.")
		return
	}
}

func (h *V1Handler) restrictionUser(r *http.Request, ws string) (models.WikiRestrictionSubject, error) {
	query := r.URL.Query()
	id := strings.TrimSpace(query.Get("accountId"))
	if id == "" {
		id = strings.TrimSpace(query.Get("key"))
	}
	if id != "" {
		return models.WikiRestrictionSubject{Type: "user", ID: id, AccountID: id}, nil
	}
	username := strings.TrimSpace(query.Get("username"))
	if username == "" {
		return models.WikiRestrictionSubject{}, store.ErrWikiValidation
	}
	members, err := h.Store.MembersByWorkspace(r.Context(), ws)
	if err != nil {
		return models.WikiRestrictionSubject{}, err
	}
	for _, member := range members {
		if strings.EqualFold(member.Email, username) || strings.EqualFold(member.DisplayName, username) {
			return models.WikiRestrictionSubject{Type: "user", ID: member.ID, AccountID: member.ID}, nil
		}
	}
	return models.WikiRestrictionSubject{}, pgx.ErrNoRows
}

func (h *V1Handler) contentRestrictionUser(w http.ResponseWriter, r *http.Request, ws, actor, pageID, operation string) {
	if !supportedQuery(w, r, "key", "username", "accountId") {
		return
	}
	subject, err := h.restrictionUser(r, ws)
	if err != nil {
		if err == store.ErrWikiValidation {
			failure(w, 400, "accountId, key, or username is required.")
		} else {
			writeError(w, err)
		}
		return
	}
	switch r.Method {
	case "GET":
		exists, err := h.Store.WikiPageRestrictionSubject(r.Context(), ws, actor, pageID, operation, subject)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, exists)
		return
	case "PUT", "DELETE":
		_, err := h.Commands.SetWikiPageRestrictionSubject(r.Context(), ws, actor, pageID, operation, subject, r.Method == "PUT")
		if err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(200)
		return
	default:
		failure(w, 405, "Method not allowed.")
		return
	}
}
