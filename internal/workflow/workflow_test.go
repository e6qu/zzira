package workflow

import "testing"

func TestAvailableFromTodo(t *testing.T) {
	ids := map[string]bool{}
	for _, tr := range Default().Available("st_todo") {
		ids[tr.ID] = true
	}
	if !ids["21"] || !ids["31"] || len(ids) != 2 {
		t.Fatalf("transitions from To Do must be {21,31}: %v", ids)
	}
}

func TestAvailableFromInProgress(t *testing.T) {
	ids := map[string]bool{}
	for _, tr := range Default().Available("st_inprogress") {
		ids[tr.ID] = true
	}
	if !ids["11"] || !ids["31"] {
		t.Fatalf("In Progress must allow To Do (11) and Done (31): %v", ids)
	}
}

func TestValidate(t *testing.T) {
	if _, ok := Default().Validate("31", "st_inprogress"); !ok {
		t.Fatal("31 must be valid from In Progress")
	}
	if _, ok := Default().Validate("31", "st_done"); ok {
		t.Fatal("31 must be invalid from Done")
	}
	if _, ok := Default().Validate("99", "st_todo"); ok {
		t.Fatal("unknown transition must be invalid")
	}
}

func TestCustomWorkflowOverridesDefault(t *testing.T) {
	wf := Workflow{ID: "x", Name: "X", Transitions: []Transition{
		{ID: "101", Name: "Confirm", From: []string{"st_todo"}, To: "st_inprogress"},
	}}
	ids := map[string]bool{}
	for _, tr := range wf.Available("st_todo") {
		ids[tr.ID] = true
	}
	if len(ids) != 1 || !ids["101"] {
		t.Fatalf("custom workflow transitions = %v", ids)
	}
	if _, ok := wf.Validate("21", "st_todo"); ok {
		t.Fatal("default transitions must not leak into custom workflow")
	}
}
