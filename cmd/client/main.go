//go:build js && wasm

// cmd/client is the sync worker: it owns the browser-local SQLite replica
// (OPFS via the SAH pool VFS), polls the /sync endpoint, replays the ordered
// action log, queues offline commands in the outbox, and renders HTML locally
// with the same render package the server uses — the isomorphic guarantee.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"syscall/js"
	"time"

	"github.com/e6qu/zzira/internal/build"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/render"
	"github.com/e6qu/zzira/internal/syncclient"
	"github.com/e6qu/zzira/internal/workflow"
)

var (
	db          js.Value
	currentView string // issue id the page is currently showing
	window      = js.Global()
	messageFunc js.Func
)

func main() {
	// Keep the callback alive for the worker lifetime. Register it through the
	// worker's event target rather than assigning the onmessage property: this
	// is the stable dispatch API when the worker bootstrap and Go runtime share
	// the same global scope.
	messageFunc = js.FuncOf(onMessage)
	window.Call("addEventListener", "message", messageFunc)

	safeInstall("sqlite init", initDB)
	if db.IsUndefined() {
		return // initDB already surfaced the error; a dead DB cannot serve
	}

	post(map[string]any{"type": "ready", "renderer": build.Renderer})

	// Yield to the browser event loop before doing any network work. An issue
	// document can be opened and edited while this worker starts; synchronous
	// bootstrap here would otherwise hold its command messages behind an
	// offline HTTP request. The regular maintenance cycle owns bootstrap and
	// sync after the command loop is live.
	ticker := time.NewTicker(3 * time.Second)
	for range ticker.C {
		if draining, _ := drainOutbox(); draining {
			syncOnce()
			continue
		}
		bootstrapIfEmpty()
		syncOnce()
	}
}

// runProtected wraps a sync-cycle function so a panic surfaces as a banner
// error instead of silently killing the worker.
func runProtected(what string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			post(map[string]any{"type": "error", "message": what + " crashed: " + fmt.Sprint(r)})
		}
	}()
	fn()
}

// bootstrapIfEmpty installs a server snapshot when the replica is fresh,
// then the normal sync loop replays only the tail.
func bootstrapIfEmpty() {
	if checkpoint() > 0 {
		return
	}
	rows := selectObjects(`SELECT COUNT(*) AS n FROM issues`, nil)
	if len(rows) > 0 && int64Of(rows[0], "n") > 0 {
		return
	}
	resp, err := http.Get("/bootstrap")
	if err != nil {
		return // offline fresh client: stays empty until online
	}
	body, err := readAndClose(resp)
	if err != nil || resp.StatusCode != http.StatusOK {
		return
	}
	var snap struct {
		Seq         int64                            `json:"seq"`
		Issues      []models.IssueUpsertPayload      `json:"issues"`
		Comments    []models.CommentUpsertPayload    `json:"comments"`
		Attachments []models.AttachmentUpsertPayload `json:"attachments"`
		Worklogs    []models.WorklogUpsertPayload    `json:"worklogs"`
	}
	if err := json.Unmarshal(body, &snap); err != nil {
		post(map[string]any{"type": "error", "message": "bad bootstrap payload"})
		return
	}
	exec(`BEGIN`, nil)
	committed := false
	defer func() {
		if !committed {
			exec(`ROLLBACK`, nil)
		}
	}()
	for _, p := range snap.Issues {
		raw, err := json.Marshal(p)
		if err != nil {
			post(map[string]any{"type": "error", "message": "encode bootstrap issue: " + err.Error()})
			return
		}
		if _, err := apply(models.Action{
			EntityType: models.EntityIssue, Op: models.OpUpsert,
			SchemaV: models.SchemaVersion, Payload: raw,
		}); err != nil {
			return
		}
	}
	for _, p := range snap.Comments {
		raw, err := json.Marshal(p)
		if err != nil {
			post(map[string]any{"type": "error", "message": "encode bootstrap comment: " + err.Error()})
			return
		}
		if _, err := apply(models.Action{
			EntityType: models.EntityComment, Op: models.OpUpsert,
			SchemaV: models.SchemaVersion, Payload: raw,
		}); err != nil {
			return
		}
	}
	for _, p := range snap.Attachments {
		raw, err := json.Marshal(p)
		if err != nil {
			post(map[string]any{"type": "error", "message": "encode bootstrap attachment: " + err.Error()})
			return
		}
		if _, err := apply(models.Action{
			EntityType: models.EntityAttachment, Op: models.OpUpsert,
			SchemaV: models.SchemaVersion, Payload: raw,
		}); err != nil {
			return
		}
	}
	for _, p := range snap.Worklogs {
		raw, err := json.Marshal(p)
		if err != nil {
			post(map[string]any{"type": "error", "message": "encode bootstrap worklog: " + err.Error()})
			return
		}
		if _, err := apply(models.Action{
			EntityType: models.EntityWorklog, Op: models.OpUpsert,
			SchemaV: models.SchemaVersion, Payload: raw,
		}); err != nil {
			return
		}
	}
	setKV("checkpoint", fmt.Sprintf("%d", snap.Seq))
	setKV("server_head", fmt.Sprintf("%d", snap.Seq))
	exec(`COMMIT`, nil)
	committed = true
	post(map[string]any{"type": "bootstrapped", "seq": snap.Seq, "issues": len(snap.Issues)})
}

// ---- SQLite (sqlite3-wasm OO1 API driven from Go) ----

const schemaSQL = `
CREATE TABLE IF NOT EXISTS actions (
  seq INTEGER PRIMARY KEY,
  entity_type TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  op TEXT NOT NULL,
  schema_v INTEGER NOT NULL,
  payload TEXT NOT NULL,
  actor_id TEXT,
  created_at TEXT
);
CREATE TABLE IF NOT EXISTS issues (
  id TEXT PRIMARY KEY,
  project_id TEXT, key TEXT, summary TEXT, description TEXT, fields TEXT, labels TEXT NOT NULL DEFAULT '[]',
  status_id TEXT, status_name TEXT, status_category TEXT,
  issuetype_name TEXT, priority_name TEXT,
  assignee_id TEXT, assignee_name TEXT,
  reporter_id TEXT, reporter_name TEXT,
  updated_seq INTEGER, updated_at TEXT
);
CREATE TABLE IF NOT EXISTS comments (
  id TEXT PRIMARY KEY,
  issue_id TEXT NOT NULL,
  author_id TEXT, author_name TEXT,
  body TEXT NOT NULL,
  created TEXT,
  local INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_comments_issue ON comments (issue_id, created);
CREATE TABLE IF NOT EXISTS attachments (
  id TEXT PRIMARY KEY,
  issue_id TEXT NOT NULL,
  filename TEXT, mime_type TEXT, size INTEGER,
  author_name TEXT, created TEXT
);
CREATE INDEX IF NOT EXISTS idx_attachments_issue ON attachments (issue_id);
CREATE TABLE IF NOT EXISTS worklogs (
  id TEXT PRIMARY KEY,
  issue_id TEXT NOT NULL,
  author_name TEXT, comment TEXT,
  time_spent_seconds INTEGER, created TEXT
);
CREATE INDEX IF NOT EXISTS idx_worklogs_issue ON worklogs (issue_id);
CREATE TABLE IF NOT EXISTS watchers (
  issue_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  PRIMARY KEY (issue_id, user_id)
);
CREATE TABLE IF NOT EXISTS notifications (
  id TEXT PRIMARY KEY,
  target_user TEXT, actor_name TEXT,
  kind TEXT, entity_type TEXT, entity_id TEXT,
  message TEXT, created TEXT
);
CREATE TABLE IF NOT EXISTS boards (
  id TEXT PRIMARY KEY, project_id TEXT, name TEXT, type TEXT
);
CREATE TABLE IF NOT EXISTS sprints (
  id TEXT PRIMARY KEY, board_id TEXT, name TEXT, state TEXT, goal TEXT
);
CREATE TABLE IF NOT EXISTS sprint_issues (
  sprint_id TEXT NOT NULL, issue_id TEXT NOT NULL, rank TEXT,
  PRIMARY KEY (sprint_id, issue_id)
);
CREATE TABLE IF NOT EXISTS issue_links (
  id TEXT PRIMARY KEY, type_name TEXT,
  inward TEXT, outward TEXT, inward_id TEXT, outward_id TEXT
);
CREATE TABLE IF NOT EXISTS outbox (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  method TEXT NOT NULL,
  path TEXT NOT NULL,
  body TEXT NOT NULL,
  content_type TEXT NOT NULL DEFAULT 'application/x-www-form-urlencoded'
);
CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v TEXT);
`

func initDB() error {
	sah := window.Get("sqlite3").Get("oo1").Get("OpfsSAHDb")
	if sah.IsUndefined() {
		return fmt.Errorf("OpfsSAHDb unavailable")
	}
	replica, err := replicaID()
	if err != nil {
		return err
	}
	db = sah.New(js.ValueOf("/zzira-" + replica + ".db"))
	exec(schemaSQL, nil)
	ensureColumn("issues", "labels", "TEXT NOT NULL DEFAULT '[]'")
	ensureColumn("issue_links", "inward", "TEXT")
	ensureColumn("issue_links", "outward", "TEXT")
	return nil
}

func ensureColumn(table, column, definition string) {
	for _, row := range selectObjects("PRAGMA table_info("+table+")", nil) {
		if str(row, "name") == column {
			return
		}
	}
	exec("ALTER TABLE "+table+" ADD COLUMN "+column+" "+definition, nil)
}

// replicaID is supplied by the page from sessionStorage. It remains stable
// across a tab reload (so offline data remains available) but intentionally
// differs across tabs, avoiding concurrent OPFS handles on one SQLite file.
func replicaID() (string, error) {
	query := window.Get("location").Get("search").String()
	values, err := url.ParseQuery(strings.TrimPrefix(query, "?"))
	if err != nil {
		return "", fmt.Errorf("replica query: %w", err)
	}
	id := values.Get("replica")
	if len(id) != 36 {
		return "", fmt.Errorf("replica id is missing or invalid")
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'f' || c >= '0' && c <= '9' || c == '-') {
			return "", fmt.Errorf("replica id is missing or invalid")
		}
	}
	return id, nil
}

// safeInstall runs fn, recovering any JS-triggered panic (a trapped wasm or
// thrown JS exception crashes the worker unless recovered here).
func safeInstall(what string, fn func() error) {
	defer func() {
		if r := recover(); r != nil {
			post(map[string]any{"type": "error", "message": what + " crashed: " + fmt.Sprint(r)})
		}
	}()
	if err := fn(); err != nil {
		post(map[string]any{"type": "error", "message": what + " failed: " + err.Error()})
	}
}

func exec(sql string, bind []any) js.Value {
	if bind == nil {
		return db.Call("exec", sql)
	}
	return db.Call("exec", map[string]any{"sql": sql, "bind": bind})
}

func selectObjects(sql string, bind []any) []map[string]js.Value {
	var res js.Value
	if bind == nil {
		res = db.Call("selectObjects", sql)
	} else {
		res = db.Call("selectObjects", sql, js.ValueOf(bind))
	}
	out := make([]map[string]js.Value, 0, res.Length())
	for i := 0; i < res.Length(); i++ {
		row := res.Index(i)
		m := make(map[string]js.Value)
		for _, k := range jsKeys(row) {
			m[k] = row.Get(k)
		}
		out = append(out, m)
	}
	return out
}

func str(v map[string]js.Value, key string) string {
	val, ok := v[key]
	if !ok || val.IsNull() || val.IsUndefined() {
		return ""
	}
	return val.String()
}

func int64Of(v map[string]js.Value, key string) int64 {
	val, ok := v[key]
	if !ok || val.IsNull() || val.IsUndefined() {
		return 0
	}
	return int64(val.Int())
}

func jsKeys(v js.Value) []string {
	keys := window.Get("Object").Call("keys", v)
	out := make([]string, 0, keys.Length())
	for i := 0; i < keys.Length(); i++ {
		out = append(out, keys.Index(i).String())
	}
	return out
}

func checkpoint() int64 {
	rows := selectObjects(`SELECT v FROM meta WHERE k='checkpoint'`, nil)
	if len(rows) == 0 {
		return 0
	}
	var n int64
	fmt.Sscanf(str(rows[0], "v"), "%d", &n)
	return n
}

func setKV(k, v string) {
	exec(`INSERT INTO meta (k,v) VALUES ($1,$2) ON CONFLICT(k) DO UPDATE SET v=$2`, []any{k, v})
}

// ---- sync ----

func syncOnce() {
	cp := checkpoint()
	url := fmt.Sprintf("/sync?since=%d&limit=500", cp)
	resp, err := http.Get(url)
	if err != nil {
		post(map[string]any{"type": "offline"})
		return
	}
	body, err := readAndClose(resp)
	if err != nil {
		post(map[string]any{"type": "offline"})
		return
	}
	if resp.StatusCode == http.StatusNotModified {
		post(map[string]any{"type": "synced", "seq": cp})
		return
	}
	if resp.StatusCode != http.StatusOK {
		post(map[string]any{"type": "error", "message": fmt.Sprintf("sync http %d", resp.StatusCode)})
		return
	}
	var delta models.SyncResponse
	if err := json.Unmarshal(body, &delta); err != nil {
		post(map[string]any{"type": "error", "message": "bad sync payload"})
		return
	}
	if delta.From != cp || delta.To < cp || delta.To > delta.Head {
		post(map[string]any{"type": "error", "message": "invalid sync checkpoint range"})
		return
	}

	changedIssues := make(map[string]bool)
	exec(`BEGIN`, nil)
	committed := false
	defer func() {
		if !committed {
			exec(`ROLLBACK`, nil)
		}
	}()
	lastSeq := cp
	for _, a := range delta.Actions {
		if a.Seq <= lastSeq || a.Seq > delta.To {
			post(map[string]any{"type": "error", "message": "invalid sync action order"})
			return
		}
		ids, err := apply(a)
		if err != nil {
			post(map[string]any{"type": "error", "message": "apply failed: " + err.Error()})
			return
		}
		for _, id := range ids {
			changedIssues[id] = true
		}
		lastSeq = a.Seq
	}
	setKV("checkpoint", fmt.Sprintf("%d", delta.To))
	setKV("server_head", fmt.Sprintf("%d", delta.Head))
	exec(`COMMIT`, nil)
	committed = true
	post(map[string]any{"type": "synced", "seq": delta.To})
	if currentView != "" && changedIssues[currentView] {
		pushCurrentView()
	}
}

func readAndClose(resp *http.Response) ([]byte, error) {
	body, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	return body, errors.Join(readErr, closeErr)
}

// apply materializes one action; returns issue ids whose view may have changed.
func apply(a models.Action) ([]string, error) {
	exec(`INSERT OR IGNORE INTO actions (seq, entity_type, entity_id, op, schema_v, payload, actor_id, created_at)
	      VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		[]any{a.Seq, a.EntityType, a.EntityID, a.Op, a.SchemaV, string(a.Payload), a.ActorID, a.CreatedAt})

	switch a.EntityType {
	case models.EntityIssue:
		return applyIssue(a)
	case models.EntityComment:
		return applyComment(a)
	case models.EntityAttachment:
		return applyAttachment(a)
	case models.EntityWorklog:
		return applyWorklog(a)
	case models.EntityBoard:
		return applyBoard(a)
	case models.EntitySprint:
		return applySprint(a)
	case models.EntitySprintIssue:
		return applySprintIssue(a)
	case models.EntityWatcher:
		return applyWatcher(a)
	case models.EntityNotification:
		return applyNotification(a)
	case models.EntityTombstone:
		return applyTombstone(a)
	case models.EntityIssueLink:
		return applyIssueLink(a)
	}
	return nil, nil // unknown entity: skip-and-ack
}

func applyIssueLink(a models.Action) ([]string, error) {
	switch a.Op {
	case models.OpUpsert:
		var p models.IssueLinkPayload
		if err := json.Unmarshal(a.Payload, &p); err != nil {
			return nil, fmt.Errorf("decode link payload: %w", err)
		}
		exec(`INSERT OR REPLACE INTO issue_links (id, type_name, inward, outward, inward_id, outward_id) VALUES ($1,$2,$3,$4,$5,$6)`,
			[]any{p.Link.ID, p.Link.TypeName, p.Link.Inward, p.Link.Outward, p.Link.InwardID, p.Link.OutwardID})
		return []string{p.Link.InwardID, p.Link.OutwardID}, nil
	case models.OpDelete:
		var p models.IssueLinkDeletePayload
		if err := json.Unmarshal(a.Payload, &p); err != nil {
			return nil, fmt.Errorf("decode link delete payload: %w", err)
		}
		exec(`DELETE FROM issue_links WHERE id=$1`, []any{a.EntityID})
		return []string{p.InwardID, p.OutwardID}, nil
	}
	return nil, nil
}

// applyTombstone drops the issue (and children) from the replica. Only
// excluded users receive these actions (server-side filter).
func applyTombstone(a models.Action) ([]string, error) {
	var p models.TombstonePayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		return nil, fmt.Errorf("decode tombstone payload: %w", err)
	}
	if p.IssueID == "" {
		return nil, nil
	}
	exec(`DELETE FROM issues WHERE id=$1`, []any{p.IssueID})
	exec(`DELETE FROM comments WHERE issue_id=$1`, []any{p.IssueID})
	exec(`DELETE FROM attachments WHERE issue_id=$1`, []any{p.IssueID})
	exec(`DELETE FROM worklogs WHERE issue_id=$1`, []any{p.IssueID})
	exec(`DELETE FROM sprint_issues WHERE issue_id=$1`, []any{p.IssueID})
	exec(`DELETE FROM watchers WHERE issue_id=$1`, []any{p.IssueID})
	exec(`DELETE FROM issue_links WHERE inward_id=$1 OR outward_id=$1`, []any{p.IssueID})
	return []string{p.IssueID}, nil
}

func applyBoard(a models.Action) ([]string, error) {
	if a.Op != models.OpUpsert {
		exec(`DELETE FROM boards WHERE id=$1`, []any{a.EntityID})
		return nil, nil
	}
	var p models.BoardUpsertPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		return nil, fmt.Errorf("decode board payload: %w", err)
	}
	exec(`INSERT OR REPLACE INTO boards (id, project_id, name, type) VALUES ($1,$2,$3,$4)`,
		[]any{p.Board.ID, p.Board.ProjectID, p.Board.Name, p.Board.Type})
	return nil, nil
}

func applySprint(a models.Action) ([]string, error) {
	if a.Op != models.OpUpsert {
		exec(`DELETE FROM sprints WHERE id=$1`, []any{a.EntityID})
		return nil, nil
	}
	var p models.SprintUpsertPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		return nil, fmt.Errorf("decode sprint payload: %w", err)
	}
	exec(`INSERT OR REPLACE INTO sprints (id, board_id, name, state, goal) VALUES ($1,$2,$3,$4,$5)`,
		[]any{p.Sprint.ID, p.Sprint.BoardID, p.Sprint.Name, p.Sprint.State, p.Sprint.Goal})
	return nil, nil
}

func applySprintIssue(a models.Action) ([]string, error) {
	var p models.SprintIssuePayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		return nil, fmt.Errorf("decode sprint-issue payload: %w", err)
	}
	switch a.Op {
	case models.OpUpsert:
		exec(`INSERT OR REPLACE INTO sprint_issues (sprint_id, issue_id, rank) VALUES ($1,$2,$3)`,
			[]any{p.SprintID, p.IssueID, p.Rank})
	case models.OpDelete:
		exec(`DELETE FROM sprint_issues WHERE sprint_id=$1 AND issue_id=$2`, []any{p.SprintID, p.IssueID})
	}
	return nil, nil
}

func applyWatcher(a models.Action) ([]string, error) {
	var p models.WatcherPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		return nil, fmt.Errorf("decode watcher payload: %w", err)
	}
	switch a.Op {
	case models.OpUpsert:
		exec(`INSERT OR REPLACE INTO watchers (issue_id, user_id) VALUES ($1,$2)`, []any{p.IssueID, p.AccountID})
	case models.OpDelete:
		exec(`DELETE FROM watchers WHERE issue_id=$1 AND user_id=$2`, []any{p.IssueID, p.AccountID})
	}
	return nil, nil
}

func applyNotification(a models.Action) ([]string, error) {
	if a.Op != models.OpUpsert {
		exec(`DELETE FROM notifications WHERE id=$1`, []any{a.EntityID})
		return nil, nil
	}
	var p models.NotificationPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		return nil, fmt.Errorf("decode notification payload: %w", err)
	}
	n := p.Notification
	exec(`INSERT OR REPLACE INTO notifications (id, target_user, actor_name, kind, entity_type, entity_id, message, created)
	      VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		[]any{n.ID, n.TargetUser, n.ActorName, n.Kind, n.EntityType, n.EntityID, n.Message, n.Created})
	return nil, nil
}

func applyAttachment(a models.Action) ([]string, error) {
	switch a.Op {
	case models.OpUpsert:
		var p models.AttachmentUpsertPayload
		if err := json.Unmarshal(a.Payload, &p); err != nil {
			return nil, fmt.Errorf("decode attachment payload: %w", err)
		}
		att := p.Attachment
		if att.ID == "" {
			return nil, nil
		}
		exec(`INSERT OR REPLACE INTO attachments (id, issue_id, filename, mime_type, size, author_name, created)
		      VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			[]any{att.ID, att.IssueID, att.Filename, att.MimeType, att.Size, att.AuthorName, att.Created})
		return []string{att.IssueID}, nil
	case models.OpDelete:
		var p models.AttachmentDeletePayload
		if err := json.Unmarshal(a.Payload, &p); err != nil {
			return nil, fmt.Errorf("decode attachment delete payload: %w", err)
		}
		if p.AttachmentID == "" || p.IssueID == "" {
			return nil, fmt.Errorf("attachment delete payload is incomplete")
		}
		exec(`DELETE FROM attachments WHERE id=$1`, []any{p.AttachmentID})
		return []string{p.IssueID}, nil
	}
	return nil, nil
}

func applyWorklog(a models.Action) ([]string, error) {
	switch a.Op {
	case models.OpUpsert:
		var p models.WorklogUpsertPayload
		if err := json.Unmarshal(a.Payload, &p); err != nil {
			return nil, fmt.Errorf("decode worklog payload: %w", err)
		}
		w := p.Worklog
		if w.ID == "" {
			return nil, nil
		}
		comment := ""
		if len(w.Comment) > 0 {
			comment = string(w.Comment)
		}
		exec(`INSERT OR REPLACE INTO worklogs (id, issue_id, author_name, comment, time_spent_seconds, created)
		      VALUES ($1,$2,$3,$4,$5,$6)`,
			[]any{w.ID, w.IssueID, w.AuthorName, comment, w.TimeSpentSeconds, w.Created})
		return []string{w.IssueID}, nil
	case models.OpDelete:
		var p models.WorklogDeletePayload
		if err := json.Unmarshal(a.Payload, &p); err != nil {
			return nil, fmt.Errorf("decode worklog delete payload: %w", err)
		}
		if p.WorklogID == "" || p.IssueID == "" {
			return nil, fmt.Errorf("worklog delete payload is incomplete")
		}
		exec(`DELETE FROM worklogs WHERE id=$1`, []any{p.WorklogID})
		return []string{p.IssueID}, nil
	}
	return nil, nil
}

func applyIssue(a models.Action) ([]string, error) {
	switch a.Op {
	case models.OpDelete:
		exec(`DELETE FROM issues WHERE id=$1`, []any{a.EntityID})
		exec(`DELETE FROM comments WHERE issue_id=$1`, []any{a.EntityID})
		exec(`DELETE FROM attachments WHERE issue_id=$1`, []any{a.EntityID})
		exec(`DELETE FROM worklogs WHERE issue_id=$1`, []any{a.EntityID})
		exec(`DELETE FROM sprint_issues WHERE issue_id=$1`, []any{a.EntityID})
		exec(`DELETE FROM watchers WHERE issue_id=$1`, []any{a.EntityID})
		exec(`DELETE FROM issue_links WHERE inward_id=$1 OR outward_id=$1`, []any{a.EntityID})
		return []string{a.EntityID}, nil
	case models.OpUpsert:
		var p models.IssueUpdatePayload // covers create snapshots too (diff empty)
		if err := json.Unmarshal(a.Payload, &p); err != nil {
			return nil, fmt.Errorf("decode issue payload: %w", err)
		}
		i := p.Issue
		if i.ID == "" {
			return nil, nil
		}
		desc := ""
		if len(i.Description) > 0 {
			desc = string(i.Description)
		}
		fieldsJSON := "{}"
		if len(i.Fields) > 0 {
			b, err := json.Marshal(i.Fields)
			if err != nil {
				return nil, fmt.Errorf("encode issue fields: %w", err)
			}
			fieldsJSON = string(b)
		}
		labelsJSON, err := json.Marshal(i.Labels)
		if err != nil {
			return nil, fmt.Errorf("encode issue labels: %w", err)
		}
		exec(`INSERT OR REPLACE INTO issues
		      (id, project_id, key, summary, description, fields, labels,
		       status_id, status_name, status_category,
		       issuetype_name, priority_name,
		       assignee_id, assignee_name, reporter_id, reporter_name,
		       updated_seq, updated_at)
		      VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
			[]any{i.ID, i.ProjectID, i.Key, i.Summary, desc, fieldsJSON, string(labelsJSON),
				i.Status.ID, i.Status.Name, i.Status.Category,
				i.IssueType.Name, priorityName(i),
				userID(i.Assignee), userName(i.Assignee),
				userID(i.Reporter), userName(i.Reporter),
				i.UpdatedSeq, i.UpdatedAt})
		return []string{i.ID}, nil
	}
	return nil, nil
}

func applyComment(a models.Action) ([]string, error) {
	switch a.Op {
	case models.OpUpsert:
		var p models.CommentUpsertPayload
		if err := json.Unmarshal(a.Payload, &p); err != nil {
			return nil, fmt.Errorf("decode comment payload: %w", err)
		}
		c := p.Comment
		if c.ID == "" {
			return nil, nil
		}
		exec(`INSERT OR REPLACE INTO comments (id, issue_id, author_id, author_name, body, created, local)
		      VALUES ($1,$2,$3,$4,$5,$6,0)`,
			[]any{c.ID, c.IssueID, c.AuthorID, c.AuthorName, string(c.Body), c.Created})
		return []string{c.IssueID}, nil
	case models.OpDelete:
		var p models.CommentDeletePayload
		if err := json.Unmarshal(a.Payload, &p); err != nil {
			return nil, fmt.Errorf("decode comment delete payload: %w", err)
		}
		if p.CommentID == "" || p.IssueID == "" {
			return nil, fmt.Errorf("comment delete payload is incomplete")
		}
		exec(`DELETE FROM comments WHERE id=$1`, []any{p.CommentID})
		return []string{p.IssueID}, nil
	}
	return nil, nil
}

func priorityName(i models.Issue) string {
	if i.Priority != nil {
		return i.Priority.Name
	}
	return ""
}

func userID(u *models.User) string {
	if u != nil {
		return u.ID
	}
	return ""
}

func userName(u *models.User) string {
	if u != nil {
		return u.DisplayName
	}
	return ""
}

// ---- outbox: offline commands, drained in order ----

func enqueue(method, path, body string) {
	exec(`INSERT INTO outbox (method, path, body) VALUES ($1,$2,$3)`, []any{method, path, body})
}

func outboxSize() int64 {
	rows := selectObjects(`SELECT COUNT(*) AS n FROM outbox`, nil)
	if len(rows) == 0 {
		return 0
	}
	return int64Of(rows[0], "n")
}

// drainOutbox replays queued commands in order. Returns false on first failure
// (still offline) — remaining entries stay queued for the next attempt.
func drainOutbox() (bool, int64) {
	n := outboxSize()
	if n == 0 {
		return false, 0
	}
	rows := selectObjects(`SELECT id, method, path, body, content_type FROM outbox ORDER BY id LIMIT 1`, nil)
	if len(rows) == 0 {
		return false, 0
	}
	id := int64Of(rows[0], "id")
	method := str(rows[0], "method")
	path := str(rows[0], "path")
	body := str(rows[0], "body")
	ct := str(rows[0], "content_type")

	req, err := http.NewRequest(method, path, strings.NewReader(body))
	if err != nil {
		return false, n
	}
	req.Header.Set("Content-Type", ct)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		post(map[string]any{"type": "offline"})
		return false, n
	}
	if err := resp.Body.Close(); err != nil {
		post(map[string]any{"type": "error", "message": "close outbox response: " + err.Error()})
	}
	switch syncclient.DispositionForStatus(resp.StatusCode) {
	case syncclient.OutboxAccepted:
		exec(`DELETE FROM outbox WHERE id=$1`, []any{id})
		return true, n - 1
	case syncclient.OutboxRejected:
		exec(`DELETE FROM outbox WHERE id=$1`, []any{id})
		post(map[string]any{"type": "error", "message": fmt.Sprintf("outbox cmd rejected (%d)", resp.StatusCode)})
		return true, n - 1
	default:
		post(map[string]any{"type": "error", "message": fmt.Sprintf("outbox cmd will retry (%d)", resp.StatusCode)})
		return false, n
	}
}

// ---- local rendering (isomorphic path) ----

func onMessage(_ js.Value, args []js.Value) any {
	if len(args) == 0 {
		return nil
	}
	// Worker message listeners receive a MessageEvent; protocol commands live
	// in its data field. Reading the event itself makes every command look like
	// the browser event type "message" and silently drops it.
	msg := args[0].Get("data")
	if msg.IsUndefined() || msg.IsNull() {
		return nil
	}
	switch msg.Get("type").String() {
	case "seed-view":
		issue := msg.Get("issue")
		id := issue.Get("id").String()
		if id == "" {
			return nil
		}
		description := issue.Get("description").String()
		doc, err := json.Marshal(adfFor(description))
		if err != nil {
			return nil
		}
		// The SSR view is authoritative at navigation time. Store it only when
		// absent; action-log sync will subsequently replace it with its full,
		// versioned representation.
		exec(`INSERT OR IGNORE INTO issues
		      (id, key, summary, description, fields, status_id, status_name, status_category,
		       issuetype_name, updated_seq, updated_at)
		      VALUES ($1,$2,$3,$4,'{}','st_todo','To Do','new','Task',0,'')`,
			[]any{id, issue.Get("key").String(), issue.Get("summary").String(), string(doc)})
	case "view":
		currentView = msg.Get("issueId").String()
		pushCurrentView()
	case "sync-now":
		for {
			drained, remaining := drainOutbox()
			if !drained || remaining == 0 {
				break
			}
		}
		syncOnce()
	case "edit-dialog":
		issueID := ""
		if data := msg.Get("data"); !data.IsUndefined() {
			issueID = data.Get("issueId").String()
		} else {
			issueID = msg.Get("issueId").String()
		}
		post(map[string]any{"type": "info", "message": "edit-dialog received [" + issueID + "]"})
		if html, ok := renderEditDialog(issueID); ok {
			post(map[string]any{"type": "info", "message": "edit-dialog rendered ok"})
			post(map[string]any{"type": "dialog-html", "html": html})
		} else {
			post(map[string]any{"type": "info", "message": "edit-dialog: issue not in replica"})
		}
	case "enqueue":
		enqueue(msg.Get("method").String(), msg.Get("path").String(), msg.Get("body").String())
		if currentView != "" {
			applyOptimistic(msg.Get("kind").String(), msg.Get("body").String())
			pushCurrentView()
		}
		post(map[string]any{"type": "queued", "size": outboxSize()})
	}
	return nil
}

// applyOptimistic materializes a queued command locally so the UI updates
// instantly while offline. Server-wins: authoritative rows replace local ones.
func applyOptimistic(kind, body string) {
	vals, err := url.ParseQuery(body)
	if err != nil {
		return
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	switch kind {
	case "comment":
		id := "local_" + fmt.Sprintf("%d", time.Now().UnixNano())
		doc := []byte(vals.Get("adf"))
		if !json.Valid(doc) {
			doc, err = json.Marshal(adfFor(vals.Get("body")))
			if err != nil {
				post(map[string]any{"type": "error", "message": "encode offline comment: " + err.Error()})
				return
			}
		}
		exec(`INSERT INTO comments (id, issue_id, author_id, author_name, body, created, local)
		      VALUES ($1,$2,$3,$4,$5,$6,1)`,
			[]any{id, currentView, "", "You (offline)", string(doc), now})
	case "transition":
		if t := workflow.Default().Find(vals.Get("transition")); t != nil {
			exec(`UPDATE issues SET status_id=$2, status_name=$3, status_category=$4 WHERE id=$1`,
				[]any{currentView, t.To, statusName(t.To), statusCategory(t.To)})
		}
	case "edit":
		if s := vals.Get("summary"); s != "" {
			exec(`UPDATE issues SET summary=$2 WHERE id=$1`, []any{currentView, s})
		}
		if d := vals.Get("description"); d != "" {
			doc, err := json.Marshal(adfFor(d))
			if err != nil {
				post(map[string]any{"type": "error", "message": "encode offline description: " + err.Error()})
				return
			}
			exec(`UPDATE issues SET description=$2 WHERE id=$1`, []any{currentView, string(doc)})
		}
	}
}

func statusName(id string) string {
	switch id {
	case "st_todo":
		return "To Do"
	case "st_inprogress":
		return "In Progress"
	case "st_done":
		return "Done"
	}
	return id
}

func statusCategory(id string) string {
	switch id {
	case "st_todo":
		return "new"
	case "st_inprogress":
		return "indeterminate"
	case "st_done":
		return "done"
	}
	return "new"
}

func adfFor(text string) map[string]any {
	if text == "" {
		return map[string]any{"type": "doc", "version": 1, "content": []any{map[string]any{"type": "paragraph"}}}
	}
	return map[string]any{
		"type": "doc", "version": 1,
		"content": []any{map[string]any{
			"type":    "paragraph",
			"content": []any{map[string]any{"type": "text", "text": text}},
		}},
	}
}

// renderEditDialog builds the edit dialog from the local replica so offline
// users can edit summary/description. Members and scheme data are unknown
// offline and render as such.
func renderEditDialog(issueID string) (string, bool) {
	rows := selectObjects(`SELECT * FROM issues WHERE id=$1`, []any{issueID})
	if len(rows) == 0 {
		return "", false
	}
	r := rows[0]
	issue := models.Issue{
		ID:          str(r, "id"),
		Key:         str(r, "key"),
		Summary:     str(r, "summary"),
		Description: json.RawMessage(orEmptyJSON(str(r, "description"))),
		Status:      models.Status{ID: str(r, "status_id"), Name: str(r, "status_name")},
	}
	view := models.EditDialogView{Issue: issue}
	var buf bytes.Buffer
	if err := render.Fragment(&buf, "edit_dialog", view); err != nil {
		post(map[string]any{"type": "error", "message": "render dialog: " + err.Error()})
		return "", false
	}
	return buf.String(), true
}

func pushCurrentView() {
	rows := selectObjects(`SELECT * FROM issues WHERE id=$1`, []any{currentView})
	if len(rows) == 0 {
		return
	}
	r := rows[0]
	issue := models.Issue{
		ID:          str(r, "id"),
		ProjectID:   str(r, "project_id"),
		Key:         str(r, "key"),
		Summary:     str(r, "summary"),
		Description: json.RawMessage(orEmptyJSON(str(r, "description"))),
		Status:      models.Status{ID: str(r, "status_id"), Name: str(r, "status_name"), Category: str(r, "status_category")},
		IssueType:   models.IssueType{Name: str(r, "issuetype_name")},
		UpdatedSeq:  int64Of(r, "updated_seq"),
		UpdatedAt:   str(r, "updated_at"),
	}
	if err := json.Unmarshal([]byte(orEmptyJSONArray(str(r, "labels"))), &issue.Labels); err != nil {
		issue.Labels = []string{}
	}
	if p := str(r, "priority_name"); p != "" {
		issue.Priority = &models.Priority{Name: p}
	}
	if id := str(r, "assignee_id"); id != "" {
		issue.Assignee = &models.User{ID: id, DisplayName: str(r, "assignee_name"), AccountType: "atlassian"}
	}
	if id := str(r, "reporter_id"); id != "" {
		issue.Reporter = &models.User{ID: id, DisplayName: str(r, "reporter_name"), AccountType: "atlassian"}
	}

	comments := []models.Comment{}
	for _, c := range selectObjects(`SELECT * FROM comments WHERE issue_id=$1 ORDER BY created, id`, []any{currentView}) {
		comments = append(comments, models.Comment{
			ID:         str(c, "id"),
			IssueID:    currentView,
			AuthorID:   str(c, "author_id"),
			AuthorName: str(c, "author_name"),
			Body:       json.RawMessage(orEmptyJSON(str(c, "body"))),
			Created:    str(c, "created"),
		})
	}

	attachmentsOut := []models.Attachment{}
	for _, r := range selectObjects(`SELECT * FROM attachments WHERE issue_id=$1 ORDER BY created, id`, []any{currentView}) {
		attachmentsOut = append(attachmentsOut, models.Attachment{
			ID: str(r, "id"), IssueID: currentView, Filename: str(r, "filename"),
			MimeType: str(r, "mime_type"), Size: int64Of(r, "size"),
			AuthorName: str(r, "author_name"), Created: str(r, "created"),
		})
	}

	worklogs := []models.Worklog{}
	for _, r := range selectObjects(`SELECT * FROM worklogs WHERE issue_id=$1 ORDER BY created, id`, []any{currentView}) {
		comment := str(r, "comment")
		wl := models.Worklog{
			ID: str(r, "id"), IssueID: currentView,
			AuthorName: str(r, "author_name"),
			Created:    str(r, "created"),
		}
		if comment != "" {
			wl.Comment = json.RawMessage(comment)
		}
		wl.TimeSpentSeconds = int(int64Of(r, "time_spent_seconds"))
		worklogs = append(worklogs, wl)
	}

	history := []models.ChangelogEntry{}
	for _, a := range selectObjects(`
			SELECT seq, payload, actor_id, created_at FROM actions
			WHERE entity_type='issue' AND entity_id=$1 AND op='upsert' AND schema_v>=2
			  AND json_extract(payload,'$.diff') IS NOT NULL
			ORDER BY seq`, []any{currentView}) {
		var p models.IssueUpdatePayload
		if err := json.Unmarshal([]byte(str(a, "payload")), &p); err != nil {
			continue
		}
		author := ""
		history = append(history, models.ChangelogEntry{
			Seq:      int64Of(a, "seq"),
			AuthorID: str(a, "actor_id"),
			Author:   &models.User{DisplayName: author},
			Created:  str(a, "created_at"),
			Items:    models.SortedDiffItems(p.Diff),
		})
	}

	var transitions []models.WorkflowTransition
	for _, t := range workflow.Default().Available(issue.Status.ID) {
		transitions = append(transitions, models.WorkflowTransition{ID: t.ID, Name: t.Name})
	}
	activity := make([]models.IssueActivityItem, 0, len(comments)+len(worklogs)+len(history))
	for _, comment := range comments {
		author := comment.AuthorName
		if author == "" {
			author = "Unknown"
		}
		activity = append(activity, models.IssueActivityItem{Kind: "comment", ID: comment.ID, AuthorID: comment.AuthorID, AuthorName: author, Created: comment.Created, Body: comment.Body})
	}
	for _, worklog := range worklogs {
		author := worklog.AuthorName
		if author == "" {
			author = "Unknown"
		}
		activity = append(activity, models.IssueActivityItem{Kind: "worklog", ID: worklog.ID, AuthorName: author, Created: worklog.Created, Body: worklog.Comment, TimeSpentSeconds: worklog.TimeSpentSeconds})
	}
	for _, entry := range history {
		activity = append(activity, models.IssueActivityItem{Kind: "history", ID: fmt.Sprintf("%d", entry.Seq), AuthorID: entry.AuthorID, AuthorName: "Unknown", Created: entry.Created, Items: entry.Items})
	}
	sort.SliceStable(activity, func(i, j int) bool { return activity[i].Created > activity[j].Created })
	linkViews := []models.IssueLinkView{}
	for _, link := range selectObjects(`
		SELECT l.id, CASE WHEN l.outward_id=$1 THEN l.outward ELSE l.inward END AS relationship,
		       i.key, i.summary, i.status_id, i.status_name, i.status_category
		FROM issue_links l
		JOIN issues i ON i.id=CASE WHEN l.outward_id=$1 THEN l.inward_id ELSE l.outward_id END
		WHERE l.inward_id=$1 OR l.outward_id=$1 ORDER BY l.id`, []any{currentView}) {
		linkViews = append(linkViews, models.IssueLinkView{
			ID: str(link, "id"), Relationship: str(link, "relationship"), IssueKey: str(link, "key"), Summary: str(link, "summary"),
			Status: models.Status{ID: str(link, "status_id"), Name: str(link, "status_name"), Category: str(link, "status_category")},
		})
	}

	view := models.IssueView{
		Issue:       issue,
		ProjectKey:  projectKeyOf(issue.Key),
		CanEdit:     true,
		Comments:    comments,
		Transitions: transitions,
		History:     history,
		Attachments: attachmentsOut,
		Worklogs:    worklogs,
		Activity:    activity,
		Links:       linkViews,
	}
	var buf bytes.Buffer
	if err := render.Fragment(&buf, "issue_view", view); err != nil {
		post(map[string]any{"type": "error", "message": "render: " + err.Error()})
		return
	}
	post(map[string]any{"type": "html", "issueId": issue.ID, "html": buf.String()})
}

func orEmptyJSON(s string) string {
	if s == "" {
		return "{}"
	}
	return s
}

func orEmptyJSONArray(s string) string {
	if s == "" {
		return "[]"
	}
	return s
}

func projectKeyOf(key string) string {
	if idx := strings.IndexByte(key, '-'); idx > 0 {
		return key[:idx]
	}
	return key
}

func post(msg map[string]any) {
	window.Call("postMessage", js.ValueOf(msg))
}
