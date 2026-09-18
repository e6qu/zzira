package api3

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestAnonymousAccess reads every operation Jira lets anonymous callers use,
// against a project granting Browse Projects to anyone and a private one.
func TestAnonymousAccess(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err = store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspaceID, adminID := store.NewID("ws"), store.NewID("usr")
	suffix := time.Now().UnixNano() % 1000000
	publicKey, privateKey := fmt.Sprintf("PUB%06d", suffix), fmt.Sprintf("PRV%06d", suffix)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Anonymous access')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Anonymous admin')`, adminID, adminID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, adminID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, adminID, store.HashToken(adminID))
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM dashboards WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM filters WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, adminID)
	})

	service := &commands.Service{Store: st, Blobs: blobs}
	// AnonymousAccess is the instance's own setting: an installation that must
	// be reached only after signing in leaves it off, and this test is about
	// what Jira's anonymous user may see when the installation allows one.
	h := &Handler{Store: st, Commands: service, Blobs: blobs, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test", AnonymousAccess: true}
	serve := func(authenticated bool, method, path, contentType, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if authenticated {
			request.SetBasicAuth(adminID+"@example.test", adminID)
		}
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		request.Header.Set("X-Atlassian-Token", "no-check")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		return response
	}
	admin := func(method, path, body string, want int) map[string]any {
		t.Helper()
		response := serve(true, method, path, "application/json", body)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		result := map[string]any{}
		_ = json.Unmarshal(response.Body.Bytes(), &result)
		return result
	}
	text := func(value any) string {
		switch typed := value.(type) {
		case string:
			return typed
		case float64:
			return strconv.FormatInt(int64(typed), 10)
		}
		return fmt.Sprint(value)
	}

	type fixture struct {
		key, projectID, issueID, issueKey, commentID, worklogID, remoteLinkID, componentID, versionID, filterID, dashboardID, attachmentID, linkID, marker string
	}
	build := func(key, marker string) fixture {
		f := fixture{key: key, marker: marker}
		project := admin(http.MethodPost, "/rest/api/3/project", `{"key":"`+key+`","name":"`+marker+` project","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated)
		f.projectID = text(project["id"])
		issue := admin(http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+key+`"},"summary":"`+marker+` summary","issuetype":{"name":"Task"}}}`, http.StatusCreated)
		f.issueID, f.issueKey = text(issue["id"]), text(issue["key"])
		other := admin(http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+key+`"},"summary":"`+marker+` blocker","issuetype":{"name":"Task"}}}`, http.StatusCreated)
		f.commentID = text(admin(http.MethodPost, "/rest/api/3/issue/"+f.issueKey+"/comment", `{"body":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"`+marker+` comment"}]}]}}`, http.StatusCreated)["id"])
		admin(http.MethodPut, "/rest/api/3/comment/"+f.commentID+"/properties/note", `{"text":"`+marker+` comment property"}`, http.StatusCreated)
		f.worklogID = text(admin(http.MethodPost, "/rest/api/3/issue/"+f.issueKey+"/worklog", `{"timeSpentSeconds":60,"comment":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"`+marker+` worklog"}]}]}}`, http.StatusCreated)["id"])
		admin(http.MethodPut, "/rest/api/3/issue/"+f.issueKey+"/worklog/"+f.worklogID+"/properties/note", `{"text":"`+marker+` worklog property"}`, http.StatusCreated)
		f.remoteLinkID = text(admin(http.MethodPost, "/rest/api/3/issue/"+f.issueKey+"/remotelink", `{"object":{"url":"https://example.test/`+marker+`","title":"`+marker+` remote link"}}`, http.StatusCreated)["id"])
		admin(http.MethodPut, "/rest/api/3/issue/"+f.issueKey+"/properties/note", `{"text":"`+marker+` issue property"}`, http.StatusCreated)
		admin(http.MethodPut, "/rest/api/3/project/"+key+"/properties/note", `{"text":"`+marker+` project property"}`, http.StatusCreated)
		f.componentID = text(admin(http.MethodPost, "/rest/api/3/component", `{"name":"`+marker+` component","project":"`+key+`"}`, http.StatusCreated)["id"])
		f.versionID = text(admin(http.MethodPost, "/rest/api/3/version", `{"name":"`+marker+` version","projectId":`+f.projectID+`}`, http.StatusCreated)["id"])
		admin(http.MethodPost, "/rest/api/3/issueLink", `{"type":{"name":"Blocks"},"inwardIssue":{"key":"`+text(other["key"])+`"},"outwardIssue":{"key":"`+f.issueKey+`"}}`, http.StatusCreated)
		links := admin(http.MethodGet, "/rest/api/3/issue/"+f.issueKey+"?fields=issuelinks", "", http.StatusOK)
		for _, link := range links["fields"].(map[string]any)["issuelinks"].([]any) {
			f.linkID = text(link.(map[string]any)["id"])
		}
		var upload bytes.Buffer
		writer := multipart.NewWriter(&upload)
		part, _ := writer.CreateFormFile("file", marker+".txt")
		_, _ = part.Write([]byte(marker + " attachment"))
		_ = writer.Close()
		uploaded := serve(true, http.MethodPost, "/rest/api/3/issue/"+f.issueKey+"/attachments", writer.FormDataContentType(), upload.String())
		var files []map[string]any
		if uploaded.Code != http.StatusOK || json.Unmarshal(uploaded.Body.Bytes(), &files) != nil || len(files) != 1 {
			t.Fatalf("attachment upload: %d %s", uploaded.Code, uploaded.Body.String())
		}
		f.attachmentID = text(files[0]["id"])
		return f
	}
	public := build(publicKey, fmt.Sprintf("Openly%06d", suffix))
	private := build(privateKey, fmt.Sprintf("Secretly%06d", suffix))
	scheme := admin(http.MethodPost, "/rest/api/3/permissionscheme", `{"name":"Public `+publicKey+`","permissions":[{"permission":"BROWSE_PROJECTS","holder":{"type":"anyone"}}]}`, http.StatusCreated)
	admin(http.MethodPut, "/rest/api/3/project/"+publicKey+"/permissionscheme", `{"id":`+text(scheme["id"])+`}`, http.StatusOK)
	dashboard := admin(http.MethodPost, "/rest/api/3/dashboard", `{"name":"`+private.marker+` dashboard","sharePermissions":[],"editPermissions":[]}`, http.StatusOK)
	private.dashboardID = text(dashboard["id"])
	filter := admin(http.MethodPost, "/rest/api/3/filter", `{"name":"`+private.marker+` filter","jql":"project = `+privateKey+`"}`, http.StatusOK)
	private.filterID = text(filter["id"])
	// Jira Cloud no longer shares filters or dashboards publicly, so both
	// fixtures read the private owner's objects.
	public.dashboardID, public.filterID = private.dashboardID, private.filterID

	parameter := regexp.MustCompile(`\{[^}]+\}`)
	resolve := func(row anonymousOperationRow, f fixture) string {
		return parameter.ReplaceAllStringFunc(row.Path, func(name string) string {
			switch name {
			case "{issueIdOrKey}":
				return f.issueKey
			case "{projectIdOrKey}", "{projectKeyOrId}":
				return f.key
			case "{commentId}":
				return f.commentID
			case "{worklogId}":
				return f.worklogID
			case "{linkId}":
				if strings.Contains(row.Path, "remotelink") {
					return f.remoteLinkID
				}
				return f.linkID
			case "{propertyKey}":
				return "note"
			case "{dashboardId}":
				return f.dashboardID
			case "{issueTypeId}":
				return "10001"
			case "{type}":
				return "project"
			case "{entityId}":
				return f.projectID
			case "{idOrName}":
				return "1"
			case "{projectTypeKey}":
				return "software"
			case "{itemId}", "{permissionId}":
				return "1"
			case "{id}":
				switch {
				case strings.HasPrefix(row.Path, "/rest/api/3/attachment"):
					return f.attachmentID
				case strings.HasPrefix(row.Path, "/rest/api/3/component"):
					return f.componentID
				case strings.HasPrefix(row.Path, "/rest/api/3/version"):
					return f.versionID
				case strings.HasPrefix(row.Path, "/rest/api/3/filter"):
					return f.filterID
				case strings.HasPrefix(row.Path, "/rest/api/3/dashboard"):
					return f.dashboardID
				case strings.Contains(row.Path, "/comment/"):
					return f.commentID
				case strings.Contains(row.Path, "/worklog/"):
					return f.worklogID
				}
				return "1"
			}
			return "1"
		})
	}
	bodies := map[string]string{
		"/rest/api/3/comment/list":                        `{"ids":[` + public.commentID + `,` + private.commentID + `]}`,
		"/rest/api/3/issue/bulkfetch":                     `{"issueIdsOrKeys":["` + public.issueKey + `","` + private.issueKey + `"],"fields":["summary","comment","worklog"]}`,
		"/rest/api/3/expression/eval":                     `{"expression":"issue.summary","context":{"issue":{"key":"` + private.issueKey + `"}}}`,
		"/rest/api/3/expression/evaluate":                 `{"expression":"issue.summary","context":{"issue":{"key":"` + private.issueKey + `"}}}`,
		"/rest/api/3/jql/parse":                           `{"queries":["project = ` + privateKey + `"]}`,
		"/rest/api/3/permissions/check":                   `{"projectPermissions":[{"permissions":["BROWSE_PROJECTS"],"projects":[` + public.projectID + `,` + private.projectID + `]}]}`,
		"/rest/api/3/permissions/project":                 `{"permissions":["BROWSE_PROJECTS"]}`,
		"/rest/api/3/search":                              `{"jql":"order by created","fields":["*all"]}`,
		"/rest/api/3/search/jql":                          `{"jql":"order by created","fields":["*all"]}`,
		"/rest/api/3/search/approximate-count":            `{"jql":"project = ` + privateKey + `"}`,
		"/rest/api/3/issue/{issueIdOrKey}/changelog/list": `{"changelogIds":[1]}`,
	}
	queries := map[string]string{
		"/rest/api/3/search":                             "?jql=order%20by%20created&fields=*all",
		"/rest/api/3/search/jql":                         "?jql=order%20by%20created&fields=*all",
		"/rest/api/3/issue/picker":                       "?query=ly&currentJQL=order%20by%20created",
		"/rest/api/3/jql/autocompletedata/suggestions":   "?fieldName=project",
		"/rest/api/3/user/assignable/multiProjectSearch": "?projectKeys=" + publicKey + "," + privateKey,
		"/rest/api/3/user/permission/search":             "?permissions=BROWSE_PROJECTS&projectKey=" + privateKey,
		"/rest/api/3/user/viewissue/search":              "?issueKey=" + private.issueKey,
		"/rest/api/3/issuetype/project":                  "?projectId=" + private.projectID,
		"/rest/api/3/mypermissions":                      "?projectKey=" + privateKey + "&permissions=BROWSE_PROJECTS",
		"/rest/api/3/issue/createmeta":                   "?expand=projects.issuetypes.fields",
		"/rest/api/3/project/search":                     "?expand=description,lead,issueTypes,url,projectKeys,permissions,insight",
		"/rest/api/3/filter/search":                      "?expand=jql,owner,sharePermissions",
		"/rest/api/3/dashboard/search":                   "?expand=description,owner,sharePermissions",
		"/rest/api/3/groupuserpicker":                    "?query=a",
		"/rest/api/3/user/picker":                        "?query=a",
		"/rest/api/3/user/search":                        "?query=a",
		"/rest/api/3/groups/picker":                      "?query=a",
	}
	anonymous := func(method, path, body string, want int) string {
		t.Helper()
		response := serve(false, method, path, "application/json", body)
		if response.Code != want {
			t.Fatalf("anonymous %s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response.Body.String()
	}
	if body := anonymous(http.MethodGet, "/rest/api/3/issue/"+public.issueKey, "", http.StatusOK); !strings.Contains(body, public.marker+" summary") {
		t.Fatalf("public issue: %s", body)
	}

	// The instance's own setting closes it to callers without credentials.
	// Jira's public API is unchanged -- the same request that the anonymous
	// user may make above is refused here, because this installation admits no
	// anonymous user at all. The attachment download goes with it: an
	// installation reachable only after signing in must not serve its files to
	// a caller who has not.
	t.Run("an installation with anonymous access off refuses every caller without credentials", func(t *testing.T) {
		h.AnonymousAccess = false
		t.Cleanup(func() { h.AnonymousAccess = true })
		for _, probe := range []struct{ method, path string }{
			{http.MethodGet, "/rest/api/3/issue/" + public.issueKey},
			{http.MethodGet, "/rest/api/3/search?jql=" + url.QueryEscape("order by created")},
			{http.MethodGet, "/secure/attachment/" + public.attachmentID + "/file.txt"},
			{http.MethodGet, "/secure/thumbnail/" + public.attachmentID + "/file.txt"},
		} {
			response := serve(false, probe.method, probe.path, "application/json", "")
			if response.Code != http.StatusUnauthorized {
				t.Errorf("anonymous %s %s: got %d want 401: %.200s",
					probe.method, probe.path, response.Code, response.Body.String())
			}
		}
		// A caller who signs in still reaches what it always did.
		if response := serve(true, http.MethodGet, "/rest/api/3/issue/"+public.issueKey, "application/json", ""); response.Code != http.StatusOK {
			t.Errorf("authenticated read with anonymous access off: got %d want 200: %.200s", response.Code, response.Body.String())
		}
	})
	anonymous(http.MethodGet, "/rest/api/3/issue/"+private.issueKey, "", http.StatusNotFound)
	if body := anonymous(http.MethodPost, "/rest/api/3/search/jql", `{"jql":"project in (`+publicKey+`,`+privateKey+`) order by key","fields":["summary"]}`, http.StatusOK); !strings.Contains(body, public.issueKey) || strings.Contains(body, private.issueKey) {
		t.Fatalf("anonymous search: %s", body)
	}
	if body := anonymous(http.MethodGet, "/rest/api/3/project", "", http.StatusOK); !strings.Contains(body, publicKey) || strings.Contains(body, privateKey) {
		t.Fatalf("anonymous projects: %s", body)
	}
	if body := anonymous(http.MethodGet, "/rest/api/3/component", "", http.StatusOK); !strings.Contains(body, public.marker+" component") || strings.Contains(body, `"emailAddress"`) {
		t.Fatalf("anonymous components: %s", body)
	}
	if body := anonymous(http.MethodGet, "/rest/api/3/mypermissions?projectKey="+publicKey+"&permissions=BROWSE_PROJECTS,CREATE_ISSUES", "", http.StatusOK); !strings.Contains(body, `"BROWSE_PROJECTS":{"description":"View a project and its work items.","havePermission":true`) {
		t.Fatalf("anonymous permissions: %s", body)
	}
	// Writes, operations Jira does not open to anonymous callers and failed
	// credentials all stay unauthorized.
	anonymous(http.MethodPost, "/rest/api/3/issue/"+public.issueKey+"/comment", `{"body":{"type":"doc","version":1,"content":[]}}`, http.StatusUnauthorized)
	anonymous(http.MethodGet, "/rest/api/3/myself", "", http.StatusUnauthorized)
	anonymous(http.MethodGet, "/rest/api/3/permissionscheme", "", http.StatusUnauthorized)
	badCredentials := httptest.NewRequest(http.MethodGet, "/rest/api/3/issue/"+public.issueKey, nil)
	badCredentials.SetBasicAuth(adminID+"@example.test", "wrong")
	rejected := httptest.NewRecorder()
	h.ServeHTTP(rejected, badCredentials)
	if rejected.Code != http.StatusUnauthorized {
		t.Fatalf("failed credentials on an anonymous operation: %d %s", rejected.Code, rejected.Body.String())
	}

	privateMarkers := []string{private.marker, privateKey, adminID + "@example.test"}
	for _, row := range anonymousOperationTable {
		for _, f := range []fixture{public, private} {
			path := resolve(row, f) + queries[row.Path]
			response := serve(false, row.Method, path, "application/json", bodies[row.Path])
			if response.Code == http.StatusUnauthorized || response.Code >= 500 {
				t.Errorf("anonymous %s %s: got %d: %s", row.Method, path, response.Code, response.Body.String())
				continue
			}
			if response.Code >= 400 || row.Path == "/rest/api/3/jql/parse" {
				// Errors and parsed queries echo what the caller sent.
				continue
			}
			for _, marker := range privateMarkers {
				if strings.Contains(response.Body.String(), marker) {
					t.Errorf("anonymous %s %s (%d) exposes %q: %s", row.Method, path, response.Code, marker, response.Body.String())
				}
			}
			t.Logf("anonymous %s %s -> %d %.200s", row.Method, path, response.Code, response.Body.String())
		}
	}
}
