package workflow

import (
	"slices"
	"testing"
)

// TestGlobalAndInitialTransitions covers Jira's transition types: a global
// transition runs from every status of its workflow, including its own
// destination; the initial transition never runs from a status and decides
// where work items start.
func TestGlobalAndInitialTransitions(t *testing.T) {
	wf := Workflow{ID: "wf", Name: "Types", Transitions: []Transition{
		{ID: "1", Name: "Create", Type: TransitionInitial, To: "st_inprogress"},
		{ID: "11", Name: "Start", From: []string{"st_todo"}, To: "st_inprogress"},
		{ID: "21", Name: "Close", Type: TransitionGlobal, To: "st_done"},
	}}
	names := func(transitions []Transition) []string {
		out := []string{}
		for _, transition := range transitions {
			out = append(out, transition.Name)
		}
		return out
	}
	if got := names(wf.Available("st_todo")); !slices.Equal(got, []string{"Start", "Close"}) {
		t.Fatalf("from To Do = %v", got)
	}
	if got := names(wf.Available("st_done")); !slices.Equal(got, []string{"Close"}) {
		t.Fatalf("a global transition runs from its own destination: %v", got)
	}
	if got := wf.Available("st_elsewhere"); len(got) != 0 {
		t.Fatalf("a status outside the workflow has transitions: %v", names(got))
	}
	if _, ok := wf.Validate("1", "st_todo"); ok {
		t.Fatal("the initial transition ran from a status")
	}
	if _, ok := wf.Validate("21", "st_inprogress"); !ok {
		t.Fatal("the global transition did not run from In Progress")
	}
	if got := wf.InitialStatus(); got != "st_inprogress" {
		t.Fatalf("initial status = %q", got)
	}
	if got := wf.StatusIDs(); !slices.Equal(got, []string{"st_inprogress", "st_todo", "st_done"}) {
		t.Fatalf("statuses = %v", got)
	}

	// A workflow stored before it had an initial transition starts in To Do
	// when it uses it, and otherwise in its first status.
	legacy := Workflow{Transitions: []Transition{{ID: "11", Name: "Review", From: []string{"st_review"}, To: "st_done"}}}
	if got := legacy.InitialStatus(); got != "st_review" {
		t.Fatalf("legacy initial status = %q", got)
	}
	legacy.Transitions = append(legacy.Transitions, Transition{ID: "21", Name: "Reopen", From: []string{"st_done"}, To: "st_todo"})
	if got := legacy.InitialStatus(); got != "st_todo" {
		t.Fatalf("legacy initial status with To Do = %q", got)
	}
	if got := Default().InitialStatus(); got != "st_todo" || Default().Initial() == nil || Default().Initial().ID != "1" {
		t.Fatalf("default workflow initial = %v %q", Default().Initial(), got)
	}
	if next := NextTransitionID(Default().Transitions); next != "41" {
		t.Fatalf("next id after the Create transition = %s", next)
	}
}
