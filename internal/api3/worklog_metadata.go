package api3

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func worklogTimestamp(value string) string {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC().Format("2006-01-02T15:04:05.000-0700")
	}
	return value
}

func worklogInput(w http.ResponseWriter, raw, visibility json.RawMessage) (*time.Time, bool) {
	// Silently ignoring a requested restriction would expose private work notes.
	if len(visibility) > 0 && string(visibility) != "null" {
		jiraFieldError(w, 400, map[string]string{"visibility": "Worklog group and role restrictions are not supported."})
		return nil, false
	}
	if len(raw) == 0 {
		return nil, true
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil && value != "" {
		for _, layout := range []string{"2006-01-02T15:04:05.000-0700", "2006-01-02T15:04:05-0700", time.RFC3339Nano} {
			if parsed, err := time.Parse(layout, value); err == nil {
				return &parsed, true
			}
		}
	}
	jiraFieldError(w, 400, map[string]string{"started": "Enter a date and time with a timezone, such as 2026-10-01T09:00:00.000+0000."})
	return nil, false
}

func parseWorklogPage(w http.ResponseWriter, r *http.Request) (store.WorklogPageOptions, bool) {
	result := store.WorklogPageOptions{MaxResults: 5000}
	for _, item := range []struct {
		name   string
		target *int
	}{{"startAt", &result.StartAt}, {"maxResults", &result.MaxResults}} {
		if values, ok := r.URL.Query()[item.name]; ok {
			value, err := strconv.Atoi(values[0])
			if err != nil || value < 0 {
				jiraFieldError(w, 400, map[string]string{item.name: "A non-negative integer is required."})
				return result, false
			}
			*item.target = value
		}
	}
	if result.MaxResults > 5000 {
		result.MaxResults = 5000
	}
	for _, item := range []struct {
		name   string
		target **time.Time
	}{{"startedAfter", &result.StartedAfter}, {"startedBefore", &result.StartedBefore}} {
		if values, ok := r.URL.Query()[item.name]; ok {
			value, err := strconv.ParseInt(values[0], 10, 64)
			if err != nil || value < 0 {
				jiraFieldError(w, 400, map[string]string{item.name: "A non-negative timestamp in milliseconds is required."})
				return result, false
			}
			// PostgreSQL dates cannot represent an arbitrary int64 epoch millisecond.
			if value > 253402300799999 {
				jiraFieldError(w, 400, map[string]string{item.name: "The timestamp is outside the supported date range."})
				return result, false
			}
			parsed := time.UnixMilli(value)
			*item.target = &parsed
		}
	}
	return result, true
}

func (h *Handler) expandedWorklogs(r *http.Request, worklogs []*models.Worklog) ([]map[string]any, error) {
	expandProperties := false
	for _, expansion := range strings.Split(r.URL.Query().Get("expand"), ",") {
		if strings.TrimSpace(expansion) == "properties" {
			expandProperties = true
		}
	}
	properties := map[string][]store.WorklogPropertyValue{}
	if expandProperties {
		ids := make([]string, 0, len(worklogs))
		for _, worklog := range worklogs {
			ids = append(ids, worklog.ID)
		}
		var err error
		properties, err = h.Store.WorklogPropertiesByIDs(r.Context(), ids)
		if err != nil {
			return nil, err
		}
	}
	beans := make([]map[string]any, 0, len(worklogs))
	for _, worklog := range worklogs {
		bean := h.worklogBean(worklog)
		if expandProperties {
			values := properties[worklog.ID]
			if values == nil {
				values = []store.WorklogPropertyValue{}
			}
			bean["properties"] = values
		}
		beans = append(beans, bean)
	}
	return beans, nil
}
