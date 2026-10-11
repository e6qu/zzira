package models

import "testing"

func TestSubtaskCreationKeepsMandatoryParentOutsideOptionalDetails(t *testing.T) {
	for _, screen := range [][]string{nil, {}, {"parent", "labels"}} {
		meta := CreateProjectMeta{
			IssueTypes: []IssueType{{ID: "child", Subtask: true}, {ID: "task"}},
			Fields: []CreateFieldMeta{
				{ID: "project", Section: "context"}, {ID: "issuetype", Section: "context"},
				{ID: "summary", Section: "primary"}, {ID: "parent", Section: "details"},
				{ID: "labels", Section: "details"},
			},
			ScreenFields: map[string][]string{"child": screen, "task": screen},
			FieldBehaviour: map[string]map[string]FieldBehaviour{
				"child": {"parent": {IsHidden: true}},
			},
		}
		parents := 0
		for _, field := range meta.FieldsForIssueType("child") {
			if field.ID == "parent" {
				parents++
				if !field.Required || field.Section != "primary" {
					t.Fatalf("mandatory parent is optional or collapsed: %+v", field)
				}
			}
		}
		if parents != 1 {
			t.Fatalf("parent count for screen %v: %d", screen, parents)
		}
		meta.ForEdit = true
		for _, field := range meta.FieldsForIssueType("child") {
			if field.ID == "parent" {
				t.Fatal("hidden parent became editable")
			}
		}
		meta.ForEdit = false
		for _, field := range meta.FieldsForIssueType("task") {
			if field.ID == "parent" && (field.Required || field.Section != "details") {
				t.Fatal("standard work inherited subtask requirements")
			}
		}
	}
}
