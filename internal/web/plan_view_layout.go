package web

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/store"
)

type planColumnChoice struct {
	ID, Name string
	Selected bool
}

func configurePlanLayout(r *http.Request, data *planPageData) error {
	names := map[string]string{"start": "Start", "end": "End", "team": "Team", "sprint": "Sprint", "estimate": "Estimate", "dependencies": "Dependencies"}
	for _, id := range store.PlanOptionalColumns {
		data.ColumnChoices = append(data.ColumnChoices, planColumnChoice{ID: id, Name: names[id]})
	}
	for _, column := range data.CustomColumns {
		data.ColumnChoices = append(data.ColumnChoices, planColumnChoice{ID: "customfield_" + column.ID, Name: column.Name})
	}
	columns := r.URL.Query()["column"]
	provided := r.URL.Query().Get("columnsProvided") == "1"
	data.Zoom = r.URL.Query().Get("zoom")
	if data.View != nil {
		columns = data.View.Columns
		provided = columns != nil
		data.Zoom = data.View.Zoom
	}
	if data.Zoom == "" {
		data.Zoom = "standard"
	}
	if !store.ValidPlanZoom(data.Zoom) {
		return fmt.Errorf("Choose compact, standard or detailed zoom.")
	}
	known := map[string]bool{}
	for _, choice := range data.ColumnChoices {
		known[choice.ID] = true
	}
	selected := map[string]bool{}
	if !provided {
		for id := range known {
			selected[id] = true
		}
	} else {
		for _, id := range columns {
			if !known[id] {
				if data.View != nil && strings.HasPrefix(id, "customfield_") {
					continue
				}
				return fmt.Errorf("Choose columns offered by this plan.")
			}
			if selected[id] {
				return fmt.Errorf("Choose each column once.")
			}
			selected[id] = true
		}
	}
	data.VisibleColumns = selected
	data.WorkColumnCount = 2
	if data.Edit {
		data.WorkColumnCount++
	}
	for i := range data.ColumnChoices {
		data.ColumnChoices[i].Selected = selected[data.ColumnChoices[i].ID]
		if data.ColumnChoices[i].Selected {
			data.WorkColumnCount++
		}
	}
	scalePlanTimeline(&data.Timeline, data.Zoom)
	return nil
}

func scalePlanTimeline(data *timelineData, zoom string) {
	factor := 1.0
	if zoom == "compact" {
		factor = .5
	}
	if zoom == "detailed" {
		factor = 2
	}
	data.Width *= factor
	data.TodayX *= factor
	for i := range data.Months {
		data.Months[i].X *= factor
		data.Months[i].Width *= factor
	}
	for i := range data.Rows {
		if bar := data.Rows[i].Bar; bar != nil {
			bar.X *= factor
			bar.Width *= factor
		}
	}
}
