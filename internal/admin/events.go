package admin

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

var organizationEventActions = map[string]string{
	"group.created":        "Group created",
	"group.deleted":        "Group deleted",
	"group.member.added":   "Group member added",
	"group.member.removed": "Group member removed",
	"role.assigned":        "Role assigned",
	"role.revoked":         "Role revoked",
	"user.invited":         "User invited",
	"user.profile.updated": "User profile updated",
	"user.removed":         "User removed",
	"user.restored":        "User restored",
	"user.suspended":       "User suspended",
}

type eventLocationFilter struct {
	City        string `json:"city"`
	CountryName string `json:"countryName"`
}

func eventPage(values url.Values, defaultLimit int) (int, int, error) {
	limit := defaultLimit
	if raw := values.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			return 0, 0, errors.New("limit must be between 1 and 500")
		}
		limit = parsed
	}
	offset := 0
	if cursor := values.Get("cursor"); cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || !strings.HasPrefix(string(decoded), "offset:") {
			return 0, 0, errors.New("cursor is invalid or expired")
		}
		parsed, err := strconv.Atoi(strings.TrimPrefix(string(decoded), "offset:"))
		if err != nil || parsed < 0 {
			return 0, 0, errors.New("cursor is invalid or expired")
		}
		offset = parsed
	}
	return offset, limit, nil
}

func eventTime(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	milliseconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, errors.New("event times must be UNIX epoch milliseconds")
	}
	value := time.UnixMilli(milliseconds).UTC()
	return &value, nil
}

func eventLocations(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var locations []eventLocationFilter
	if err := json.Unmarshal([]byte(raw), &locations); err != nil || len(locations) == 0 || len(locations) > 100 {
		return nil, errors.New("location must be a non-empty JSON array with at most 100 entries")
	}
	patterns := make([]string, 0, len(locations)*2)
	for _, location := range locations {
		if location.City == "" && location.CountryName == "" {
			return nil, errors.New("each location requires city or countryName")
		}
		if location.City != "" {
			patterns = append(patterns, "%"+location.City+"%")
		}
		if location.CountryName != "" {
			patterns = append(patterns, "%"+location.CountryName+"%")
		}
	}
	return patterns, nil
}

func eventList(values url.Values, stream bool) (store.OrganizationAuditFilter, error) {
	defaultLimit := 30
	if stream {
		defaultLimit = 200
	}
	offset, limit, err := eventPage(values, defaultLimit)
	if err != nil {
		return store.OrganizationAuditFilter{}, err
	}
	from, err := eventTime(values.Get("from"))
	if err != nil {
		return store.OrganizationAuditFilter{}, err
	}
	to, err := eventTime(values.Get("to"))
	if err != nil {
		return store.OrganizationAuditFilter{}, err
	}
	if from != nil && to != nil && from.After(*to) {
		return store.OrganizationAuditFilter{}, errors.New("from must not be later than to")
	}
	actors := splitQueryList(values["actor"])
	ips := splitQueryList(values["ip"])
	products := splitQueryList(values["product"])
	if len(actors) > 100 || len(ips) > 100 || len(products) > 5 {
		return store.OrganizationAuditFilter{}, errors.New("event filters contain too many values")
	}
	for _, ip := range ips {
		if net.ParseIP(ip) == nil {
			return store.OrganizationAuditFilter{}, fmt.Errorf("invalid IP address %q", ip)
		}
	}
	allowedProducts := map[string]bool{"bitbucket": true, "confluence": true, "guard_detect": true, "jira": true, "loom": true}
	for _, product := range products {
		if !allowedProducts[product] {
			return store.OrganizationAuditFilter{}, fmt.Errorf("unsupported product %q", product)
		}
	}
	locations, err := eventLocations(values.Get("location"))
	if err != nil {
		return store.OrganizationAuditFilter{}, err
	}
	order := values.Get("sortOrder")
	if stream && order != "" && order != "asc" && order != "desc" {
		return store.OrganizationAuditFilter{}, errors.New("sortOrder must be asc or desc")
	}
	return store.OrganizationAuditFilter{
		Query: values.Get("q"), Action: values.Get("action"), Actors: actors,
		IPs: ips, Products: products, Locations: locations, From: from, To: to,
		Offset: offset, Limit: limit, Ascending: stream && order != "desc",
	}, nil
}

func splitQueryList(values []string) []string {
	items := make([]string, 0)
	seen := map[string]bool{}
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			item = strings.TrimSpace(item)
			if item != "" && !seen[item] {
				seen[item] = true
				items = append(items, item)
			}
		}
	}
	return items
}

func (h *Handler) eventModel(organization *models.Organization, event *models.OrganizationAuditEvent, polling bool) map[string]any {
	self := strings.TrimRight(h.BaseURL, "/") + "/admin/v1/orgs/" + url.PathEscape(organization.ID) + "/events/" + strconv.FormatInt(event.ID, 10)
	actorID := event.ActorID
	if actorID == "" {
		actorID = "system"
	}
	actor := map[string]any{"id": actorID}
	if event.ActorName != "" {
		actor["name"] = event.ActorName
	}
	if event.ActorEmail != "" {
		actor["email"] = event.ActorEmail
	}
	attributes := map[string]any{
		"time": event.CreatedAt, "action": event.Action, "actor": actor,
		"context":   []map[string]any{{"id": event.TargetID, "type": event.TargetType, "attributes": event.Detail}},
		"container": []map[string]any{{"id": organization.ID, "type": "organization", "attributes": map[string]any{"name": organization.Name}}},
	}
	if polling {
		attributes["processedAt"] = event.CreatedAt
	}
	if ip, ok := event.Detail["ip"].(string); ok && ip != "" {
		attributes["location"] = map[string]any{"ip": ip, "city": event.Detail["city"], "countryName": event.Detail["countryName"]}
	}
	return map[string]any{"id": strconv.FormatInt(event.ID, 10), "type": "events", "attributes": attributes, "links": map[string]string{"self": self}}
}

func (h *Handler) eventPageLink(r *http.Request, offset int) string {
	query := r.URL.Query()
	query.Set("cursor", cursorFor(offset))
	return strings.TrimRight(h.BaseURL, "/") + r.URL.Path + "?" + query.Encode()
}

func (h *Handler) Events(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	stream := strings.HasSuffix(r.URL.Path, "/events-stream")
	allowed := []string{"cursor", "from", "to", "limit"}
	if stream {
		allowed = append(allowed, "sortOrder")
	} else {
		allowed = append(allowed, "q", "action", "actor", "ip", "product", "location")
	}
	if err := rejectUnknownQuery(r.URL.Query(), allowed...); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	organization, ok := h.organizationForRequest(w, r, workspaceID)
	if !ok {
		return
	}
	filter, err := eventList(r.URL.Query(), stream)
	if err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	events, hasMore, err := h.Store.QueryOrganizationAuditEvents(r.Context(), organization.ID, filter)
	if err != nil {
		failure(w, http.StatusInternalServerError, "Audit event query failed.")
		return
	}
	data := make([]map[string]any, 0, len(events))
	for _, event := range events {
		data = append(data, h.eventModel(organization, event, stream))
	}
	meta := map[string]any{"page_size": len(data)}
	links := map[string]string{"self": strings.TrimRight(h.BaseURL, "/") + r.URL.RequestURI()}
	if filter.Offset > 0 {
		previous := filter.Offset - filter.Limit
		if previous < 0 {
			previous = 0
		}
		links["prev"] = h.eventPageLink(r, previous)
	}
	if hasMore || stream {
		nextOffset := filter.Offset + len(data)
		next := cursorFor(nextOffset)
		meta["next"] = next
		links["next"] = h.eventPageLink(r, nextOffset)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "meta": meta, "links": links})
}

func (h *Handler) EventDetails(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := rejectUnknownQuery(r.URL.Query()); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	organization, ok := h.organizationForRequest(w, r, workspaceID)
	if !ok {
		return
	}
	eventID, err := strconv.ParseInt(r.PathValue("eventId"), 10, 64)
	if err != nil || eventID < 1 {
		failure(w, http.StatusNotFound, "Audit event was not found.")
		return
	}
	event, err := h.Store.OrganizationAuditEventByID(r.Context(), organization.ID, eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		failure(w, http.StatusNotFound, "Audit event was not found.")
		return
	}
	if err != nil {
		failure(w, http.StatusInternalServerError, "Audit event lookup failed.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": h.eventModel(organization, event, false)})
}

func (h *Handler) EventActions(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := rejectUnknownQuery(r.URL.Query()); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok := h.organizationForRequest(w, r, workspaceID); !ok {
		return
	}
	ids := make([]string, 0, len(organizationEventActions))
	for id := range organizationEventActions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	data := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		data = append(data, map[string]any{
			"id": id, "type": "event-actions",
			"attributes": map[string]string{"displayName": organizationEventActions[id], "groupDisplayName": "User management"},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}
