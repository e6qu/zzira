package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type planCustomFieldChoice struct {
	ID, Name, Kind                          string
	Selected, Filter, Filterable, Available bool
}
type planCustomFieldCell struct {
	Display   string
	Tokens    []string
	Available bool
}
type planCustomFieldFilter struct {
	ID, Name, Value string
	Options         []models.CreateFieldOption
}

func planCustomFieldSupported(kind string) bool {
	switch kind {
	case models.CustomFieldText, models.CustomFieldNumber, models.CustomFieldDate, models.CustomFieldDatetime, models.CustomFieldURL, models.CustomFieldSelect, models.CustomFieldMultiSelect, models.CustomFieldLabels:
		return true
	}
	return false
}
func planCustomFieldFilterable(kind string) bool {
	return kind == models.CustomFieldSelect || kind == models.CustomFieldMultiSelect || kind == models.CustomFieldLabels
}
func planCustomFieldChoices(fields []*models.CustomField, plan store.Plan, r *http.Request) []planCustomFieldChoice {
	selected, filters := map[string]bool{}, map[string]bool{}
	for _, field := range plan.CustomFields {
		id := strconv.FormatInt(field.CustomFieldID, 10)
		selected[id], filters[id] = true, field.Filter
	}
	if r != nil && r.PostFormValue("action") == "custom-fields" {
		selected, filters = map[string]bool{}, map[string]bool{}
		for _, id := range r.PostForm["customField"] {
			selected[id] = true
		}
		for _, id := range r.PostForm["customFieldFilter"] {
			filters[id] = true
		}
	}
	choices := []planCustomFieldChoice{}
	seen := map[string]bool{}
	for _, field := range fields {
		if !planCustomFieldSupported(field.Type) {
			continue
		}
		id := strings.TrimPrefix(field.ID, "customfield_")
		seen[id] = true
		choices = append(choices, planCustomFieldChoice{ID: id, Name: field.Name, Kind: field.Type, Selected: selected[id], Filter: filters[id], Filterable: planCustomFieldFilterable(field.Type), Available: true})
	}
	for id := range selected {
		if !seen[id] {
			choices = append(choices, planCustomFieldChoice{ID: id, Name: "Unavailable custom field (" + id + ")", Selected: true, Filter: filters[id]})
		}
	}
	sort.Slice(choices, func(i, j int) bool {
		return choices[i].Name < choices[j].Name || choices[i].Name == choices[j].Name && choices[i].ID < choices[j].ID
	})
	return choices
}

func planCustomFieldsFromForm(r *http.Request) ([]store.PlanCustomField, error) {
	ids, err := planExcludedIDs(r.PostForm["customField"])
	if err != nil {
		return nil, fmt.Errorf("Choose custom fields from the list.")
	}
	filters, err := planExcludedIDs(r.PostForm["customFieldFilter"])
	if err != nil {
		return nil, fmt.Errorf("Choose filter fields from the list.")
	}
	chosen := map[int64]bool{}
	for _, id := range ids {
		chosen[id] = true
	}
	enabled := map[int64]bool{}
	for _, id := range filters {
		if !chosen[id] {
			return nil, fmt.Errorf("Add a custom field to the plan before enabling its filter.")
		}
		enabled[id] = true
	}
	out := make([]store.PlanCustomField, 0, len(ids))
	for _, id := range ids {
		out = append(out, store.PlanCustomField{CustomFieldID: id, Filter: enabled[id]})
	}
	return out, nil
}

// planCustomFieldCellFor uses the governing context's option names. Hidden or
// unassociated values are never displayed or offered as filter choices.
func planCustomFieldCellFor(field planCustomFieldChoice, raw json.RawMessage, options []models.CreateFieldOption) planCustomFieldCell {
	cell := planCustomFieldCell{Available: true}
	if len(raw) == 0 || string(raw) == "null" {
		cell.Display = "None"
		cell.Tokens = []string{"null"}
		return cell
	}
	var text string
	var values []string
	switch {
	case json.Unmarshal(raw, &text) == nil:
		values = []string{text}
	case json.Unmarshal(raw, &values) == nil:
	case field.Kind == models.CustomFieldNumber:
		var number json.Number
		if json.Unmarshal(raw, &number) == nil {
			cell.Display = number.String()
		} else {
			cell.Display = "Unavailable value"
		}
		return cell
	default:
		cell.Display = "Unavailable value"
		return cell
	}
	names := map[string]string{}
	for _, option := range options {
		names[option.ID] = option.Name
	}
	labels := []string{}
	for _, value := range values {
		if value == "" {
			continue
		}
		label := value
		if field.Kind == models.CustomFieldSelect || field.Kind == models.CustomFieldMultiSelect {
			label = names[value]
			if label == "" {
				label = "Unavailable option"
			}
		}
		labels = append(labels, label)
		if (field.Kind == models.CustomFieldSelect || field.Kind == models.CustomFieldMultiSelect) && names[value] == "" {
			continue
		}
		token, _ := json.Marshal(value)
		cell.Tokens = append(cell.Tokens, string(token))
	}
	cell.Display = strings.Join(labels, ", ")
	if cell.Display == "" {
		cell.Display = "None"
		cell.Tokens = []string{"null"}
	}
	return cell
}

func (h *Handler) loadPlanCustomFields(r *http.Request, workspaceID string, data *planPageData) error {
	if len(data.Plan.CustomFields) == 0 {
		return nil
	}
	catalog, err := h.Store.CustomFieldsForWorkspace(r.Context(), workspaceID)
	if err != nil {
		return err
	}
	choices := planCustomFieldChoices(catalog, data.Plan, nil)
	for _, choice := range choices {
		if choice.Selected {
			data.CustomColumns = append(data.CustomColumns, choice)
		}
	}
	contexts, err := h.Store.CustomFieldContextsByProject(r.Context(), workspaceID)
	if err != nil {
		return err
	}
	behaviour, err := h.Store.ResolveFieldBehaviourByProject(r.Context(), workspaceID)
	if err != nil {
		return err
	}
	data.CustomCells = map[string]map[string]planCustomFieldCell{}
	options := map[string]map[string]string{}
	for _, row := range data.Timeline.Rows {
		issue := row.Item.Issue
		cells := map[string]planCustomFieldCell{}
		for _, column := range data.CustomColumns {
			fieldID := "customfield_" + column.ID
			context, associated := contexts[issue.ProjectID][issue.IssueType.ID][fieldID]
			cell := planCustomFieldCell{Display: "Not available"}
			if column.Available && associated && !behaviour[issue.ProjectID][issue.IssueType.ID][fieldID].IsHidden {
				cell = planCustomFieldCellFor(column, issue.Fields[fieldID], context.Options)
			}
			cells[column.ID] = cell
			if column.Filter && column.Filterable && cell.Available {
				if options[column.ID] == nil {
					options[column.ID] = map[string]string{}
				}
				for _, token := range cell.Tokens {
					label := cell.Display
					if token != "null" {
						var value string
						_ = json.Unmarshal([]byte(token), &value)
						if column.Kind == models.CustomFieldLabels {
							label = value
						} else {
							label = "Unavailable option"
							for _, option := range context.Options {
								if option.ID == value {
									label = option.Name
									break
								}
							}
						}
					}
					options[column.ID][token] = label
				}
			}
		}
		data.CustomCells[issue.ID] = cells
	}
	for _, column := range data.CustomColumns {
		if !column.Filter || !column.Filterable {
			continue
		}
		filter := planCustomFieldFilter{ID: column.ID, Name: column.Name, Value: r.URL.Query().Get("cf_" + column.ID)}
		for value, name := range options[column.ID] {
			filter.Options = append(filter.Options, models.CreateFieldOption{ID: value, Name: name})
		}
		sort.Slice(filter.Options, func(i, j int) bool {
			return filter.Options[i].Name < filter.Options[j].Name || filter.Options[i].Name == filter.Options[j].Name && filter.Options[i].ID < filter.Options[j].ID
		})
		if filter.Value != "" {
			if _, ok := options[column.ID][filter.Value]; !ok {
				return fmt.Errorf("%w: Choose a value offered by the %s filter.", store.ErrPlanValidation, column.Name)
			}
		}
		data.CustomFilters = append(data.CustomFilters, filter)
	}
	return nil
}

func planCustomFieldRows(data *planPageData) []timelineRow {
	rows := []timelineRow{}
	for _, row := range data.Timeline.Rows {
		matches := true
		for _, filter := range data.CustomFilters {
			if filter.Value == "" {
				continue
			}
			found := false
			for _, token := range data.CustomCells[row.Item.Issue.ID][filter.ID].Tokens {
				if token == filter.Value {
					found = true
					break
				}
			}
			matches = matches && found
		}
		if matches {
			rows = append(rows, row)
		}
	}
	return rows
}
