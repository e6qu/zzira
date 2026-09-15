package confluence

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestTaskMacros pins tasks as Confluence keeps them: written in page and blog
// post bodies in storage or the document format, following every edit of the
// body, ticked in the body when completed, and added to a page as a new
// version of it.
func TestTaskMacros(t *testing.T) {
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
	if err := store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	ws, actor, member := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Tasks test')`, ws)
	for _, user := range []string{actor, member} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test',$1)`, user, user+"@example.test")
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, user, store.HashToken(user))
	}
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin'),($1,$3,'member')`, ws, actor, member)
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_blog_post_versions WHERE blog_post_id IN (SELECT b.id FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_blog_posts WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM notifications WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(sql, ws)
		}
		for _, user := range []string{actor, member} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, user)
			exec(`DELETE FROM users WHERE id=$1`, user)
		}
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	v2 := func(user, method, path string, body any, want int) map[string]any {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, "/wiki/api/v2"+path, strings.NewReader(string(raw)))
		request.SetBasicAuth(user+"@example.test", user)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		out := map[string]any{}
		if strings.HasPrefix(strings.TrimSpace(response.Body.String()), "{") {
			if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
		}
		return out
	}
	idOf := func(body map[string]any) string {
		t.Helper()
		id, _ := body["id"].(string)
		if id == "" {
			t.Fatalf("no id in %v", body)
		}
		return id
	}
	results := func(body map[string]any) []map[string]any {
		t.Helper()
		list, _ := body["results"].([]any)
		out := make([]map[string]any, 0, len(list))
		for _, item := range list {
			object, _ := item.(map[string]any)
			out = append(out, object)
		}
		return out
	}
	storage := func(value string) map[string]any { return map[string]any{"representation": "storage", "value": value} }
	space := idOf(v2(actor, "POST", "/spaces", map[string]any{"key": "TSK", "name": "Tasks"}, 201))

	// A page's task lists are its tasks: the first person mentioned is the
	// assignee, the first date is when it is due, and a task written complete
	// is completed by its writer.
	taskList := `<ac:task-list><ac:task><ac:task-id>1</ac:task-id><ac:task-status>incomplete</ac:task-status><ac:task-body>Ship <ac:link><ri:user ri:account-id="` + member + `" /></ac:link> <time datetime="2030-01-02" /></ac:task-body></ac:task><ac:task><ac:task-id>2</ac:task-id><ac:task-status>complete</ac:task-status><ac:task-body>Draft notes</ac:task-body></ac:task></ac:task-list>`
	page := idOf(v2(actor, "POST", "/pages", map[string]any{"spaceId": space, "title": "Launch", "status": "current", "body": storage("<p>Plan</p>" + taskList)}, 200))
	tasks := results(v2(actor, "GET", "/tasks?body-format=storage&page-id="+page, nil, 200))
	if len(tasks) != 2 || tasks[0]["localId"] != "1" || tasks[0]["assignedTo"] != member || tasks[0]["dueAt"] != "2030-01-02T00:00:00.000Z" || tasks[0]["status"] != "incomplete" || tasks[0]["pageId"] != page ||
		tasks[1]["status"] != "complete" || tasks[1]["completedBy"] != actor {
		t.Fatalf("tasks from the page body: %v", tasks)
	}
	first, second := tasks[0]["id"].(string), tasks[1]["id"].(string)
	if adfTask := v2(actor, "GET", "/tasks/"+first+"?body-format=atlas_doc_format", nil, 200); !strings.Contains(mustJSON(t, adfTask["body"]), `\"type\":\"mention\"`) {
		t.Fatalf("task body in the document format: %v", adfTask["body"])
	}
	v2(actor, "GET", "/tasks/"+first+"?body-format=view", nil, 400)

	// Completing a task ticks it in the page without making a new version.
	if done := v2(member, "PUT", "/tasks/"+first, map[string]string{"status": "complete"}, 200); done["status"] != "complete" || done["completedBy"] != member {
		t.Fatalf("completed task: %v", done)
	}
	ticked := v2(actor, "GET", "/pages/"+page+"?body-format=storage", nil, 200)
	if body := mustJSON(t, ticked["body"]); !strings.Contains(body, `<ac:task-id>1</ac:task-id><ac:task-status>complete`) || mustJSON(t, ticked["version"]) == "" || ticked["version"].(map[string]any)["number"] != float64(1) {
		t.Fatalf("ticked page: %v", ticked)
	}

	// An edit that drops a task removes it, and one that reopens a task
	// clears who completed it.
	v2(actor, "PUT", "/pages/"+page, map[string]any{"id": page, "spaceId": space, "title": "Launch", "status": "current", "version": map[string]any{"number": 2},
		"body": storage(`<ac:task-list><ac:task><ac:task-id>2</ac:task-id><ac:task-status>incomplete</ac:task-status><ac:task-body>Draft notes</ac:task-body></ac:task></ac:task-list>`)}, 200)
	if tasks := results(v2(actor, "GET", "/tasks?page-id="+page, nil, 200)); len(tasks) != 1 || tasks[0]["id"] != second || tasks[0]["status"] != "incomplete" || tasks[0]["completedBy"] != nil {
		t.Fatalf("tasks after the edit: %v", tasks)
	}
	v2(actor, "GET", "/tasks/"+first, nil, 404)

	// Tasks written in the document format are tasks too.
	adfBody := `{"type":"doc","version":1,"content":[{"type":"taskList","attrs":{"localId":"list"},"content":[{"type":"taskItem","attrs":{"localId":"a1","state":"TODO"},"content":[{"type":"text","text":"From the document format"}]}]}]}`
	adfPage := idOf(v2(actor, "POST", "/pages", map[string]any{"spaceId": space, "title": "Written in ADF", "status": "current", "body": map[string]any{"representation": "atlas_doc_format", "value": adfBody}}, 200))
	if tasks := results(v2(actor, "GET", "/tasks?body-format=storage&page-id="+adfPage, nil, 200)); len(tasks) != 1 || tasks[0]["localId"] != "a1" || !strings.Contains(mustJSON(t, tasks[0]["body"]), "From the document format") {
		t.Fatalf("tasks from the document format: %v", tasks)
	}

	// Blog posts hold tasks, listed by blog post and ticked in the post.
	post := idOf(v2(actor, "POST", "/blogposts", map[string]any{"spaceId": space, "title": "Weekly", "status": "current", "body": storage(`<p>This week</p><ac:task-list><ac:task><ac:task-id>1</ac:task-id><ac:task-status>incomplete</ac:task-status><ac:task-body>Review</ac:task-body></ac:task></ac:task-list>`)}, 200))
	blogTasks := results(v2(actor, "GET", "/tasks?blogpost-id="+post, nil, 200))
	if len(blogTasks) != 1 || blogTasks[0]["blogPostId"] != post || blogTasks[0]["pageId"] != nil {
		t.Fatalf("blog post tasks: %v", blogTasks)
	}
	v2(actor, "PUT", "/tasks/"+blogTasks[0]["id"].(string), map[string]string{"status": "complete"}, 200)
	if body := mustJSON(t, v2(actor, "GET", "/blogposts/"+post+"?body-format=storage", nil, 200)["body"]); !strings.Contains(body, `ac:task-status>complete`) {
		t.Fatalf("ticked blog post: %s", body)
	}
	if both := results(v2(actor, "GET", "/tasks?page-id="+page+"&blogpost-id="+post, nil, 200)); len(both) != 2 {
		t.Fatalf("tasks for a page and a blog post: %v", both)
	}

	// Adding a task to a page writes it into the body as a new version, and
	// the assignee hears about it as a mention.
	added, err := h.Commands.CreateWikiTask(ctx, ws, actor, models.WikiTask{PageID: page, AssignedTo: member, DueAt: "2030-02-03T10:00:00Z", Body: models.WikiBody{Representation: "storage", Value: "Announce"}})
	if err != nil {
		t.Fatal(err)
	}
	if added.LocalID != "3" || added.AssignedTo != member || added.DueAt != "2030-02-03T00:00:00.000Z" {
		t.Fatalf("added task: %+v", added)
	}
	withTask := v2(actor, "GET", "/pages/"+page+"?body-format=storage", nil, 200)
	if withTask["version"].(map[string]any)["number"] != float64(3) || !strings.Contains(mustJSON(t, withTask["body"]), `<ac:task-id>3</ac:task-id>`) {
		t.Fatalf("page after adding a task: %v", withTask)
	}
	notifications, err := st.NotificationsByUser(ctx, ws, member, 20)
	if err != nil {
		t.Fatal(err)
	}
	mentions := 0
	for _, n := range notifications {
		if n.Kind == "mentioned" && n.EntityID == page {
			mentions++
		}
	}
	if mentions != 2 {
		t.Fatalf("assignee mentions: %d in %+v", mentions, notifications)
	}
}
