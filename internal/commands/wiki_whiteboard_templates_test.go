package commands

import "testing"

func TestWhiteboardStartersFitVisibleCanvas(t *testing.T) {
	for key := range wikiWhiteboardTemplates {
		canvas := wikiWhiteboardCanvas(key)
		if len(canvas.Objects) < 4 {
			t.Fatalf("%s has no usable starter", key)
		}
		ids := map[string]bool{}
		for _, object := range canvas.Objects {
			if ids[object.ID] || object.ID == "" {
				t.Fatalf("%s has duplicate object IDs", key)
			}
			ids[object.ID] = true
			if object.X < 0 || object.Y < 0 || object.X+object.Width > 1400 || object.Y+object.Height > 800 || object.Width < 80 || object.Height < 60 || object.Title == "" {
				t.Fatalf("%s has an inaccessible starter: %+v", key, object)
			}
		}
		for _, connector := range canvas.Connectors {
			if !ids[connector.FromObjectID] || !ids[connector.ToObjectID] || connector.FromObjectID == connector.ToObjectID {
				t.Fatalf("%s has a dangling connector", key)
			}
		}
	}
}
