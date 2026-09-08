package web

import (
	"encoding/json"
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
	glances := []models.AppModule{{AppKey: "app.one", Key: "first", Type: "jira:issueGlance"}, {AppKey: "app.one", Key: "second", Type: "jira:issueGlance"}}
	selected := selectIssueContextModules(glances)
	if len(selected) != 1 || selected[0].Key != "first" {
		t.Fatalf("selected glances = %+v", selected)
	}
	modules := append(glances, models.AppModule{AppKey: "app.one", Key: "modern", Type: "jira:issueContext"})
	selected = selectIssueContextModules(modules)
	if len(selected) != 1 || selected[0].Key != "modern" {
		t.Fatalf("selected modern contexts = %+v", selected)
	}
	modules = append(modules, models.AppModule{AppKey: "app.one", Key: "second-modern", Type: "jira:issueContext"}, models.AppModule{AppKey: "app.two", Key: "other-app", Type: "jira:issueContext"})
	selected = selectIssueContextModules(modules)
	if len(selected) != 2 || selected[0].Key != "modern" || selected[1].Key != "other-app" {
		t.Fatalf("did not select the first eligible context from each app: %+v", selected)
	}
}

func TestIssueContextStatus(t *testing.T) {
	module := models.AppModule{ID: "status-module", AppKey: "example.connect", Key: "delivery-context"}
	if got := issueContextStatusPropertyKey(module); got != "com.atlassian.jira.issue:example.connect:delivery-context:status" {
		t.Fatalf("status property key = %q", got)
	}
	tests := []struct {
		name, raw, kind, label, className, iconPath string
	}{
		{"badge", `{"type":"badge","value":{"label":"7"}}`, "badge", "7", "", ""},
		{"badge cap", `{"type":"badge","value":{"label":"123"}}`, "badge", "99+", "", ""},
		{"lozenge", `{"type":"lozenge","value":{"label":"At risk","type":"moved"}}`, "lozenge", "At risk", "lozenge-moved", ""},
		{"icon", `{"type":"icon","value":{"label":"/status.svg"}}`, "icon", "", "", "/status.svg"},
		{"Forge icon alias", `{"type":"icon","value":{"url":"icons/status.svg"}}`, "icon", "", "", "icons/status.svg"},
		{"zero badge", `{"type":"badge","value":{"label":"0"}}`, "", "", "", ""},
		{"invalid badge", `{"type":"badge","value":{"label":"many"}}`, "", "", "", ""},
		{"invalid appearance", `{"type":"lozenge","value":{"label":"Risk","type":"warning"}}`, "", "", "", ""},
		{"remote icon", `{"type":"icon","value":{"label":"https://evil.example/status.svg"}}`, "", "", "", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status := parseIssueContextStatus(json.RawMessage(test.raw))
			if status.kind != test.kind || status.label != test.label || status.appearance != test.className || status.iconPath != test.iconPath {
				t.Fatalf("status = %+v", status)
			}
		})
	}
	decorateIssueContextStatus(&module, json.RawMessage(`{"type":"icon","value":{"label":"/status.svg"}}`), "ZZ-1")
	if module.ContextStatusType != "icon" || module.ContextStatusIconURL != "/app-modules/status-module/status-icon?issueKey=ZZ-1" {
		t.Fatalf("decorated status = %+v", module)
	}
}
