package store

import (
	"errors"
	"testing"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
)

func TestReportGadgetsNormalizeTheirWindow(t *testing.T) {
	for _, key := range []string{"com.zzira:created-vs-resolved", "com.zzira:resolution-time", "com.zzira:velocity", "com.zzira:sprint-burndown", "com.zzira:recently-created", "com.zzira:average-age", "com.zzira:time-since", "com.zzira:days-remaining", "com.zzira:sprint-health"} {
		if !models.ReportGadget(key) {
			t.Fatalf("%s is not a report gadget", key)
		}
		found := false
		for _, definition := range models.GadgetCatalog() {
			found = found || definition.ModuleKey == key
		}
		if !found {
			t.Fatalf("%s is not in the catalog", key)
		}
	}
	if models.ReportGadget("com.zzira:pie-chart") {
		t.Fatal("pie chart reported as a report gadget")
	}
	for key, project := range map[string]bool{"com.zzira:recently-created": true, "com.zzira:average-age": true, "com.zzira:time-since": true, "com.zzira:created-vs-resolved": true, "com.zzira:velocity": false, "com.zzira:days-remaining": false, "com.zzira:sprint-health": false} {
		if models.ProjectReportGadget(key) != project {
			t.Fatalf("%s project report = %v", key, !project)
		}
	}
	dated := models.GadgetConfig{}
	if err := NormalizeGadgetConfig(&dated); err != nil || dated.DateField != "created" {
		t.Fatalf("default date field = %+v, %v", dated, err)
	}
	for _, field := range models.TimeSinceFields {
		if _, ok := timeSinceColumns[field.Key]; !ok {
			t.Fatalf("%s has no column", field.Key)
		}
	}
	if err := NormalizeGadgetConfig(&models.GadgetConfig{DateField: "duedate"}); !errors.Is(err, ErrDashboardValidation) {
		t.Fatalf("unknown date field error = %v", err)
	}
	config := models.GadgetConfig{ProjectKey: "ZZ"}
	if err := NormalizeGadgetConfig(&config); err != nil || config.Days != 30 {
		t.Fatalf("default window = %+v, %v", config, err)
	}
	bad := models.GadgetConfig{BoardID: "brd_default", Days: 14}
	if err := NormalizeGadgetConfig(&bad); !errors.Is(err, ErrDashboardValidation) {
		t.Fatalf("14 day window error = %v", err)
	}
}

func TestChartGadgetsGroupAndScopeWork(t *testing.T) {
	config := models.GadgetConfig{}
	if err := NormalizeGadgetConfig(&config); err != nil || config.GroupBy != "status" || config.YGroupBy != "assignee" {
		t.Fatalf("default groupings = %+v, %v", config, err)
	}
	for _, grouping := range models.GadgetGroupings {
		if _, ok := gadgetGroupSQL[grouping.Key]; !ok {
			t.Fatalf("%s has no SQL grouping", grouping.Key)
		}
		valid := models.GadgetConfig{GroupBy: grouping.Key, YGroupBy: grouping.Key}
		if err := NormalizeGadgetConfig(&valid); err != nil {
			t.Fatalf("%s grouping refused: %v", grouping.Key, err)
		}
	}
	for _, invalid := range []models.GadgetConfig{{GroupBy: "fixVersion"}, {YGroupBy: "i.id"}} {
		if err := NormalizeGadgetConfig(&invalid); !errors.Is(err, ErrDashboardValidation) {
			t.Fatalf("%+v error = %v", invalid, err)
		}
	}
	if name := (models.GadgetConfig{GroupBy: "issuetype", YGroupBy: "labels"}); name.GroupLabel() != "Work type" || name.YGroupLabel() != "Labels" {
		t.Fatalf("grouping names = %q, %q", name.GroupLabel(), name.YGroupLabel())
	}
	catalog := map[string]bool{}
	for _, definition := range models.GadgetCatalog() {
		catalog[definition.ModuleKey] = true
	}
	for _, key := range []string{"com.zzira:filter-results", "com.zzira:assigned-to-me", "com.zzira:watched-issues", "com.zzira:voted-issues", "com.zzira:in-progress"} {
		if !catalog[key] || !models.ListGadget(key) {
			t.Fatalf("%s is not a catalogued list gadget", key)
		}
		if scope := models.GadgetScopeJQL(key); scope != "" {
			if _, err := jql.Parse(scope); err != nil {
				t.Fatalf("%s scope %q: %v", key, scope, err)
			}
		}
	}
	for _, key := range []string{"com.zzira:two-dimensional-statistics", "com.zzira:heat-map", "com.zzira:pie-chart", "com.zzira:issue-statistics"} {
		if !catalog[key] || models.ListGadget(key) || models.ReportGadget(key) || !models.ChartGadget(key) {
			t.Fatalf("%s is not a catalogued chart gadget", key)
		}
	}
	// Streams and reports neither list nor count work by a grouping.
	bubbles := models.GadgetConfig{}
	if err := NormalizeGadgetConfig(&bubbles); err != nil || bubbles.BubbleAxis != "participants" {
		t.Fatalf("default bubble axis = %+v, %v", bubbles, err)
	}
	if err := NormalizeGadgetConfig(&models.GadgetConfig{BubbleAxis: "age"}); !errors.Is(err, ErrDashboardValidation) {
		t.Fatalf("unknown bubble axis error = %v", err)
	}
	for _, key := range []string{"com.zzira:activity-stream", "com.zzira:calendar", "com.zzira:road-map", "com.zzira:filter-results", "com.zzira:velocity", "com.zzira:bubble-chart"} {
		if !catalog[key] || models.ChartGadget(key) {
			t.Fatalf("%s is counted as a chart gadget", key)
		}
	}
	if !models.ProjectReportGadget("com.zzira:road-map") || models.ListGadget("com.zzira:activity-stream") || models.ReportGadget("com.zzira:calendar") {
		t.Fatal("stream and road map gadgets are misclassified")
	}
}
