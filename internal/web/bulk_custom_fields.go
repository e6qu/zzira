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
// can render. An empty answer is a field the editor leaves alone, which the
// REST bulk edit still takes.
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
	case "option-with-child":
		// A cascading select's choices are its parents, each followed by its
		// children, exactly as the single-item editor lists them.
		return "cascadingSelect"
	case "user":
		return "user"
	case "users":
		return "multiUser"
	case "group":
		return "group"
	case "groups":
		return "multiGroup"
	case "team":
		return "team"
	case "projectpicker":
		return "project"
	case "version":
		return "version"
	case "versions":
		return "multiVersion"
	case "array":
		return "labels"
	default:
		return ""
	}
}

// bulkCustomFieldChooses reports whether the kind is picked from a list the
// editor offers rather than typed into a box.
func bulkCustomFieldChooses(kind string) bool {
	switch kind {
	case "singleSelect", "multiSelect", "cascadingSelect", "user", "multiUser", "group", "multiGroup",
		"team", "project", "version", "multiVersion":
		return true
	}
	return false
}

// MultiValued reports whether the field takes several values at once, which
// is how the page decides between a select and a multiple select.
func (field bulkCustomField) MultiValued() bool {
	switch field.Kind {
	case "multiSelect", "multiUser", "multiGroup", "multiVersion":
		return true
	}
	return false
}

// Chooses reports whether this field is picked from the options beside it.
func (field bulkCustomField) Chooses() bool { return bulkCustomFieldChooses(field.Kind) }

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
	cascading := false
	for id, field := range shared {
		if seen[id] != len(project.IssueTypes) {
			continue
		}
		cascading = cascading || field.Kind == "cascadingSelect"
		fields = append(fields, field)
	}
	if cascading {
		// A cascading select is chosen as one value -- an option, or an
		// option and one of its children -- so its list is the parents each
		// followed by their children, as the work item's own editor reads it.
		catalog, err := h.Store.CustomFieldOptionCatalog(ctx, workspaceID, projectID, project.IssueTypes[0].ID)
		if err != nil {
			return nil, err
		}
		for index, field := range fields {
			if field.Kind != "cascadingSelect" {
				continue
			}
			cascade := []models.CreateFieldOption{}
			for _, parent := range field.Options {
				cascade = append(cascade, parent)
				children := catalog[field.ID].Children[parent.ID]
				names := make([]string, 0, len(children))
				for name := range children {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					cascade = append(cascade, models.CreateFieldOption{ID: parent.ID + ":" + children[name], Name: parent.Name + " › " + name})
				}
			}
			fields[index].Options = cascade
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
	case "cascadingSelect":
		// The option list names a child as "parent:child", as the work item
		// editor's own cascading select does.
		if single == "" {
			return encode(nil)
		}
		parent, child, _ := strings.Cut(single, ":")
		cascade := map[string]any{"id": strings.TrimSpace(parent)}
		if child = strings.TrimSpace(child); child != "" {
			cascade["child"] = map[string]string{"id": child}
		}
		return encode(cascade)
	case "user":
		if single == "" {
			return encode(nil)
		}
		return encode(map[string]string{"accountId": single})
	case "multiUser":
		people := make([]map[string]string, 0, len(values))
		for _, value := range values {
			people = append(people, map[string]string{"accountId": value})
		}
		return encode(people)
	case "team", "project", "version":
		// Each of these names one thing by its id, and an empty box clears
		// the field as it does everywhere else in this editor.
		if single == "" {
			return encode(nil)
		}
		return encode(map[string]string{"id": single})
	case "multiVersion":
		versions := make([]map[string]string, 0, len(values))
		for _, value := range values {
			versions = append(versions, map[string]string{"id": value})
		}
		return encode(versions)
	case "group":
		if single == "" {
			return encode(nil)
		}
		return encode(map[string]string{"groupId": single})
	case "multiGroup":
		groups := make([]map[string]string, 0, len(values))
		for _, value := range values {
			groups = append(groups, map[string]string{"groupId": value})
		}
		return encode(groups)
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
