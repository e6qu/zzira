package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// The navigator's bulk editor offers the custom fields every work type in the
// project shares, because the selection it applies to is whatever is ticked:
// a field only some types carry would fail for the rest.

// bulkCustomField is one custom field the bulk editor can set, in the shape
// the page renders and the submission reads back.
type bulkCustomField struct {
	ID   string
	Name string
	// Kind is what the page renders and how the value is encoded: text,
	// number, date, dateTime, url, singleSelect, multiSelect or labels.
	Kind    string
	Options []models.CreateFieldOption
}

// InputName is the form field this custom field's value arrives in.
func (field bulkCustomField) InputName() string { return "valueCustom_" + field.ID }

// bulkCustomFieldKind maps a field's create-metadata type to what the editor
// can render. An empty answer is a field the editor leaves alone -- a
// cascading select, a user or group picker, rich text -- which the REST bulk
// edit still takes.
func bulkCustomFieldKind(field models.CreateFieldMeta) string {
	switch field.Type {
	case "string":
		return "text"
	case "number":
		return "number"
	case "date":
		return "date"
	case "datetime":
		return "dateTime"
	case "url":
		return "url"
	case "option":
		return "singleSelect"
	case "options":
		return "multiSelect"
	case "array":
		return "labels"
	default:
		return ""
	}
}

// bulkCustomFields are the custom fields shared by every work type of one
// project, ordered by name so the panel reads the same way twice.
func (h *Handler) bulkCustomFields(ctx context.Context, workspaceID, userID, projectID string) ([]bulkCustomField, error) {
	metadata, err := h.Store.IssueCreateMetadata(ctx, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	var project *models.CreateProjectMeta
	for index := range metadata.Projects {
		if metadata.Projects[index].Project.ID == projectID {
			project = &metadata.Projects[index]
			break
		}
	}
	if project == nil || len(project.IssueTypes) == 0 {
		return nil, nil
	}
	shared := map[string]bulkCustomField{}
	seen := map[string]int{}
	for _, issueType := range project.IssueTypes {
		for _, field := range project.FieldsForIssueType(issueType.ID) {
			if !strings.HasPrefix(field.ID, "customfield_") {
				continue
			}
			kind := bulkCustomFieldKind(field)
			if kind == "" {
				continue
			}
			seen[field.ID]++
			shared[field.ID] = bulkCustomField{ID: field.ID, Name: field.Name, Kind: kind, Options: field.Options}
		}
	}
	fields := make([]bulkCustomField, 0, len(shared))
	for id, field := range shared {
		if seen[id] == len(project.IssueTypes) {
			fields = append(fields, field)
		}
	}
	sort.Slice(fields, func(i, j int) bool {
		if strings.EqualFold(fields[i].Name, fields[j].Name) {
			return fields[i].ID < fields[j].ID
		}
		return strings.ToLower(fields[i].Name) < strings.ToLower(fields[j].Name)
	})
	return fields, nil
}

// BulkIssueFields answers with the custom fields the bulk editor can set,
// which the editor asks for when it is opened. It is not part of the
// navigator page: reading them means reading the whole site's create
// metadata, and a page of work items should not pay for an editor nobody
// opened.
func (h *Handler) BulkIssueFields(w http.ResponseWriter, r *http.Request, projectKey string) {
	user, workspaceID, ok := h.requireBulkChange(w, r)
	if !ok {
		return
	}
	project, err := h.Store.ProjectByKey(r.Context(), workspaceID, projectKey)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	fields, err := h.bulkCustomFields(r.Context(), workspaceID, user.ID, project.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeFragment(w, "bulk_custom_fields", struct{ Fields []bulkCustomField }{Fields: fields})
}

// bulkCustomFieldOperation reads one custom field's value out of the form, in
// the shapes the update command takes: an option by id, a list of options by
// id, a list of strings, a number, or text. An empty value clears the field,
// which is what the single-item editor does with an emptied box.
func bulkCustomFieldOperation(r *http.Request, field bulkCustomField) (store.BulkIssueEditOperation, error) {
	operation := store.BulkIssueEditOperation{FieldID: field.ID, Action: "SET"}
	encode := func(value any) (store.BulkIssueEditOperation, error) {
		encoded, err := json.Marshal(value)
		if err != nil {
			return operation, err
		}
		operation.Value = encoded
		return operation, nil
	}
	values := []string{}
	for _, raw := range r.Form[field.InputName()] {
		for _, part := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				values = append(values, trimmed)
			}
		}
	}
	single := strings.TrimSpace(r.FormValue(field.InputName()))
	switch field.Kind {
	case "multiSelect":
		options := make([]map[string]string, 0, len(values))
		for _, value := range values {
			options = append(options, map[string]string{"id": value})
		}
		return encode(options)
	case "labels":
		return encode(values)
	case "dateTime":
		if single == "" {
			return encode("")
		}
		// A browser's datetime-local box has no zone. The site reads it as
		// UTC, which is what the work item's own datetime fields are stored
		// and shown in.
		moment, err := time.Parse("2006-01-02T15:04", single)
		if err != nil {
			if moment, err = time.Parse(time.RFC3339, single); err != nil {
				return operation, fmt.Errorf("%s is a date and time", field.Name)
			}
		}
		return encode(moment.UTC().Format(time.RFC3339))
	case "number":
		if single == "" {
			operation.Value = json.RawMessage("null")
			return operation, nil
		}
		number, err := strconv.ParseFloat(single, 64)
		if err != nil {
			return operation, fmt.Errorf("%s is a number", field.Name)
		}
		return encode(number)
	default:
		return encode(single)
	}
}
