package models

import "testing"

// TestVersionMarkerNamesOneVersionOfOneWorkItem pins the marker a form
// carries to say which version of a work item it was composed from. It has to
// change whenever the work item is written, differ between work items, and be
// absent where the write sequence is unknown, because a form that cannot say
// which version it saw must not claim one: the browser replica builds its
// dialog without the sequence, and its queued edits apply to the work item as
// it stands.
func TestVersionMarkerNamesOneVersionOfOneWorkItem(t *testing.T) {
	first := Issue{ID: "iss_one", UpdatedSeq: 41}
	again := Issue{ID: "iss_one", UpdatedSeq: 41}
	written := Issue{ID: "iss_one", UpdatedSeq: 42}
	other := Issue{ID: "iss_two", UpdatedSeq: 41}
	if first.VersionMarker() == "" {
		t.Fatal("a work item with a write sequence named no version")
	}
	if first.VersionMarker() != again.VersionMarker() {
		t.Error("the same version of the same work item named two versions")
	}
	if first.VersionMarker() == written.VersionMarker() {
		t.Error("a written work item kept its version")
	}
	if first.VersionMarker() == other.VersionMarker() {
		t.Error("two work items at the same sequence named one version")
	}
	// The marker stands in for the sequence rather than publishing it.
	if marker := first.VersionMarker(); marker == "41" || len(marker) != 16 {
		t.Errorf("the marker is %q, which does not stand in for the sequence", marker)
	}
	if (Issue{ID: "iss_one"}).VersionMarker() != "" {
		t.Error("a work item whose write sequence is unknown claimed a version")
	}
}
