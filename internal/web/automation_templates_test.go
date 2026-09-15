package web

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAutomationTemplateFormTypesParameters(t *testing.T) {
	form := url.Values{"param_days": {" 5 "}, "param_label": {"old"}, "param_unrelated": {"x"}}
	r := httptest.NewRequest("POST", "/settings/automation/templates", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		t.Fatal(err)
	}
	parameters, problem := automationTemplateForm(r, "scheduled-label-stale-work")
	if problem != "" || len(parameters) != 2 || parameters["days"].Type != "NUMBER" || parameters["days"].Value != 5.0 || parameters["label"].Value != "old" {
		t.Fatalf("parameters = %+v, %q", parameters, problem)
	}
	bad := httptest.NewRequest("POST", "/settings/automation/templates", strings.NewReader(url.Values{"param_days": {"soon"}}.Encode()))
	bad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := bad.ParseForm(); err != nil {
		t.Fatal(err)
	}
	if _, problem := automationTemplateForm(bad, "scheduled-label-stale-work"); problem != "Days without updates must be a number." {
		t.Fatalf("problem = %q", problem)
	}
}
