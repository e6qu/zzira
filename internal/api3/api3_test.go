package api3

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func goldenHandler() *Handler {
	return &Handler{BaseURL: "http://localhost:8080"}
}

func TestIssueBeanGolden(t *testing.T) {
	h := goldenHandler()
	issue := &models.Issue{
		ID:          "iss_abc123",
		WorkspaceID: "ws_default",
		ProjectID:   "prj_default",
		Key:         "ZZ-1",
		Summary:     "Walking skeleton comes online",
		Description: json.RawMessage(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"body"}]}]}`),
		Status:      models.Status{ID: "st_todo", Name: "To Do", Category: "new"},
		IssueType:   models.IssueType{ID: "it_task", Name: "Task"},
		Priority:    &models.Priority{ID: "pr_medium", Name: "Medium"},
		Assignee:    &models.User{ID: "usr_1", DisplayName: "Demo User", AccountType: "atlassian"},
		Reporter:    &models.User{ID: "usr_1", DisplayName: "Demo User", AccountType: "atlassian"},
		UpdatedAt:   "2026-08-28T09:00:00Z",
	}
	bean := h.issueBean(issue)
	got, err := json.MarshalIndent(bean, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	goldenPath := "testdata/issue_bean.golden.json"
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		_ = os.WriteFile(goldenPath, got, 0o644)
		t.Skipf("golden written; re-run to verify (%s)", goldenPath)
	}
	if !jsonEqual(want, got) {
		t.Fatalf("IssueBean drifted from Jira contract golden.\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}

func jsonEqual(a, b []byte) bool {
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		return false
	}
	ea, _ := json.Marshal(va)
	eb, _ := json.Marshal(vb)
	return string(ea) == string(eb)
}

func TestCreateIssueRequestValidation(t *testing.T) {
	// missing summary → Jira-shaped field error key
	body := `{"fields":{"project":{"key":"ZZ"}}}`
	var req createIssueRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	if req.Fields.Project == nil || req.Fields.Project.Key != "ZZ" {
		t.Fatal("project not decoded")
	}
	if req.Fields.Summary != "" {
		t.Fatal("summary should be empty")
	}
}

func TestQuerySearchPageRejectsInvalidExplicitValues(t *testing.T) {
	tests := []string{
		"/rest/api/3/search?startAt=-1",
		"/rest/api/3/search?startAt=invalid",
		"/rest/api/3/search?maxResults=-1",
		"/rest/api/3/search?maxResults=101",
		"/rest/api/3/search?maxResults=invalid",
	}
	for _, target := range tests {
		t.Run(target, func(t *testing.T) {
			if _, _, err := querySearchPage(httptest.NewRequest("GET", target, nil)); err == nil {
				t.Fatal("expected explicit invalid pagination value to be rejected")
			}
		})
	}
}

func TestQuerySearchPageDefaultsOnlyWhenOmitted(t *testing.T) {
	startAt, maxResults, err := querySearchPage(httptest.NewRequest("GET", "/rest/api/3/search", nil))
	if err != nil || startAt != 0 || maxResults != defaultSearchPageSize {
		t.Fatalf("omitted page values = (%d, %d, %v)", startAt, maxResults, err)
	}
	startAt, maxResults, err = querySearchPage(httptest.NewRequest("GET", "/rest/api/3/search?startAt=2&maxResults=0", nil))
	if err != nil || startAt != 2 || maxResults != 0 {
		t.Fatalf("explicit page values = (%d, %d, %v)", startAt, maxResults, err)
	}
}

func TestADFToPlainText(t *testing.T) {
	raw := json.RawMessage(`{"type":"doc","version":1,"content":[
		{"type":"paragraph","content":[{"type":"text","text":"hello "},{"type":"text","text":"world"}]},
		{"type":"paragraph","content":[{"type":"text","text":"second"}]}
	]}`)
	if got := adfToPlainText(raw); got != "hello world\nsecond" {
		t.Fatalf("adfToPlainText = %q", got)
	}
}

func TestProjectKeyOf(t *testing.T) {
	if got := projectKeyOf(&models.Issue{Key: "ZZ-12"}); got != "ZZ" {
		t.Fatalf("projectKeyOf = %q", got)
	}
}

func TestMutationRoutesRequireTheirDeclaredMethod(t *testing.T) {
	h := goldenHandler()
	for _, tt := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/rest/api/3/issueLink/lnk_1"},
		{http.MethodPost, "/rest/api/3/project/ZZ"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tt.method, tt.path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s status=%d, want 404 without dispatch", tt.method, tt.path, w.Code)
		}
	}
}
