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

func TestDecorateIssueContext(t *testing.T) {
	module := models.AppModule{ID: "99", Body: `{"iconUrl":"context.svg","label":"3 linked deployments"}`}
	decorateIssueContext(&module)
	if module.IconURL != "/app-modules/99/icon" || module.ContextLabel != "3 linked deployments" {
		t.Fatalf("decorated module = %+v", module)
	}
}

func TestSelectIssueContextModules(t *testing.T) {
	glances := []models.AppModule{{Key: "first", Type: "jira:issueGlance"}, {Key: "second", Type: "jira:issueGlance"}}
	selected := selectIssueContextModules(glances)
	if len(selected) != 1 || selected[0].Key != "first" {
		t.Fatalf("selected glances = %+v", selected)
	}
	modules := append(glances, models.AppModule{Key: "modern", Type: "jira:issueContext"})
	selected = selectIssueContextModules(modules)
	if len(selected) != 1 || selected[0].Key != "modern" {
		t.Fatalf("selected modern contexts = %+v", selected)
	}
}
