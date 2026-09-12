package confluence

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/store"
)

func (h *Handler) auditRecordBean(record store.WikiAuditRecord) map[string]any {
	author := map[string]any{
		"type": "user", "displayName": record.AuthorName, "accountId": record.AuthorID,
		"accountType": "atlassian", "operations": []any{},
	}
	bean := map[string]any{
		"author": author, "remoteAddress": record.RemoteAddress,
		"creationDate": record.CreationDate.UnixMilli(),
		"summary":      record.Summary, "description": record.Description, "category": record.Category,
		"sysAdmin": record.SysAdmin, "superAdmin": record.SuperAdmin,
		"changedValues": json.RawMessage(record.ChangedValues), "associatedObjects": json.RawMessage(record.AssociatedObjects),
	}
	if len(record.AffectedObject) > 0 && string(record.AffectedObject) != "null" {
		bean["affectedObject"] = json.RawMessage(record.AffectedObject)
	}
	return bean
}

// auditDate reads Confluence's millisecond timestamps.
func auditDate(w http.ResponseWriter, r *http.Request, name string) (time.Time, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return time.Time{}, true
	}
	millis, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || millis < 0 {
		failure(w, 400, name+" must be a millisecond timestamp.")
		return time.Time{}, false
	}
	return time.UnixMilli(millis).UTC(), true
}

func (h *V1Handler) v1AuditLog(w http.ResponseWriter, r *http.Request, ws, actor string) {
	switch r.Method {
	case http.MethodGet:
		if !supportedQuery(w, r, "startDate", "endDate", "searchString", "start", "limit") {
			return
		}
		start, ok := auditDate(w, r, "startDate")
		if !ok {
			return
		}
		end, ok := auditDate(w, r, "endDate")
		if !ok {
			return
		}
		records, err := h.Store.WikiAuditRecords(r.Context(), ws, actor, store.WikiAuditQuery{
			Start: start, End: end, Search: strings.TrimSpace(r.URL.Query().Get("searchString")),
		})
		if err != nil {
			writeError(w, err)
			return
		}
		h.wikiPage(w, r, h.auditBeans(records), false)
	case http.MethodPost:
		if !supportedQuery(w, r) {
			return
		}
		var input struct {
			Author *struct {
				AccountID   string `json:"accountId"`
				DisplayName string `json:"displayName"`
			} `json:"author"`
			RemoteAddress     string          `json:"remoteAddress"`
			CreationDate      int64           `json:"creationDate"`
			Summary           string          `json:"summary"`
			Description       string          `json:"description"`
			Category          string          `json:"category"`
			SysAdmin          bool            `json:"sysAdmin"`
			SuperAdmin        bool            `json:"superAdmin"`
			AffectedObject    json.RawMessage `json:"affectedObject"`
			ChangedValues     json.RawMessage `json:"changedValues"`
			AssociatedObjects json.RawMessage `json:"associatedObjects"`
		}
		if !decode(w, r, &input) {
			return
		}
		record := store.WikiAuditRecord{
			RemoteAddress: input.RemoteAddress, Summary: input.Summary, Description: input.Description,
			Category: input.Category, SysAdmin: input.SysAdmin, SuperAdmin: input.SuperAdmin,
			AffectedObject: input.AffectedObject, ChangedValues: input.ChangedValues,
			AssociatedObjects: input.AssociatedObjects,
		}
		if input.Author != nil {
			record.AuthorID, record.AuthorName = input.Author.AccountID, input.Author.DisplayName
		}
		if input.CreationDate > 0 {
			record.CreationDate = time.UnixMilli(input.CreationDate).UTC()
		}
		saved, err := h.Store.RecordWikiAudit(r.Context(), ws, actor, record)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, h.auditRecordBean(saved))
	default:
		failure(w, 405, "Method not allowed.")
	}
}

func (h *Handler) auditBeans(records []store.WikiAuditRecord) []any {
	values := make([]any, 0, len(records))
	for _, record := range records {
		values = append(values, h.auditRecordBean(record))
	}
	return values
}

func (h *V1Handler) v1AuditSince(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "number", "units", "searchString", "start", "limit") {
		return
	}
	number := 3
	if raw := strings.TrimSpace(r.URL.Query().Get("number")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			failure(w, 400, "number must be a positive integer.")
			return
		}
		number = parsed
	}
	units := strings.TrimSpace(r.URL.Query().Get("units"))
	records, err := h.Store.WikiAuditRecordsSince(r.Context(), ws, actor, number, units,
		strings.TrimSpace(r.URL.Query().Get("searchString")))
	if err != nil {
		writeError(w, err)
		return
	}
	h.wikiPage(w, r, h.auditBeans(records), false)
}

// v1AuditExport writes the log as a file. Confluence offers CSV and ZIP; the
// ZIP is the same CSV compressed, so the rows a caller gets are the same either
// way.
func (h *V1Handler) v1AuditExport(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "startDate", "endDate", "searchString", "format") {
		return
	}
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "csv"
	}
	if format != "csv" && format != "zip" {
		failure(w, 400, "format is csv or zip.")
		return
	}
	start, ok := auditDate(w, r, "startDate")
	if !ok {
		return
	}
	end, ok := auditDate(w, r, "endDate")
	if !ok {
		return
	}
	records, err := h.Store.WikiAuditRecords(r.Context(), ws, actor, store.WikiAuditQuery{
		Start: start, End: end, Search: strings.TrimSpace(r.URL.Query().Get("searchString")),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	rows := [][]string{{"Date", "Author", "Remote address", "Category", "Summary", "Description"}}
	for _, record := range records {
		rows = append(rows, []string{
			record.CreationDate.UTC().Format(time.RFC3339), record.AuthorName, record.RemoteAddress,
			record.Category, record.Summary, record.Description,
		})
	}
	if format == "zip" {
		h.writeAuditZip(w, rows)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="audit.csv"`)
	writer := csv.NewWriter(w)
	_ = writer.WriteAll(rows)
	writer.Flush()
}

func (h *V1Handler) v1AuditRetention(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		retention, err := h.Store.WikiAuditRetentionPeriod(r.Context(), ws, actor)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, map[string]any{"number": retention.Number, "units": retention.Units})
	case http.MethodPut:
		var input struct {
			Number int    `json:"number"`
			Units  string `json:"units"`
		}
		if !decode(w, r, &input) {
			return
		}
		retention, err := h.Store.SetWikiAuditRetentionPeriod(r.Context(), ws, actor,
			store.WikiAuditRetention{Number: input.Number, Units: input.Units})
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, map[string]any{"number": retention.Number, "units": retention.Units})
	default:
		failure(w, 405, "Method not allowed.")
	}
}

// writeAuditZip compresses the same rows the CSV export writes.
func (h *V1Handler) writeAuditZip(w http.ResponseWriter, rows [][]string) {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := writer.WriteAll(rows); err != nil {
		failure(w, 500, "Could not write the audit export.")
		return
	}
	writer.Flush()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="audit.zip"`)
	archive := zip.NewWriter(w)
	entry, err := archive.Create("audit.csv")
	if err != nil {
		return
	}
	if _, err = entry.Write(buffer.Bytes()); err != nil {
		return
	}
	_ = archive.Close()
}
