package web

import (
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func TestAppModuleThumbnailPath(t *testing.T) {
	module := models.AppModule{ID: "42", Body: `{"description":"Release health","thumbnailUrl":"dashboard.svg"}`}
	if got := appModuleThumbnailURL(module); got != "dashboard.svg" {
		t.Fatalf("thumbnail URL = %q", got)
	}
	if got := appModuleThumbnailPath(module); got != "/app-modules/42/thumbnail" {
		t.Fatalf("thumbnail path = %q", got)
	}
	module.Body = "host-rendered content"
	if got := appModuleThumbnailPath(module); got != "" {
		t.Fatalf("plain module thumbnail path = %q", got)
	}
}

func TestAppModuleIconPath(t *testing.T) {
	module := models.AppModule{ID: "84", Body: `{"iconUrl":"/project.svg"}`}
	if got := appModuleIconURL(module); got != "/project.svg" {
		t.Fatalf("icon URL = %q", got)
	}
	if got := appModuleIconPath(module); got != "/app-modules/84/icon" {
		t.Fatalf("icon path = %q", got)
	}
}
