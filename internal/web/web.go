package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/render"
	"github.com/e6qu/zzira/internal/store"
)

type Handler struct {
	Store         *store.Store
	Commands      *commands.Service
	OIDC          *OIDC
	WorkspaceSlug string
}

type pageData struct {
	User   *models.User
	Data   any
	Active string
}

type createDialogData struct {
	Project   *models.Project
	IssueType *models.IssueType
	Error     string
}

type projectIssuesData struct {
	Project      *models.Project
	Issues       []*models.Issue
	Selected     *models.Issue
	Statuses     []models.Status
	Members      []*models.User
	Filters      []*models.Filter
	ActiveFilter string
	Chips        []navigatorChip
	SaveJQL      string
	Mode         string
	JQL          string
	Text         string
	Status       string
	Assignee     string
	Sort         string
	Direction    string
	Total        int
	ResultStart  int
	ResultEnd    int
	Page         int
	PageCount    int
	PreviousURL  string
	NextURL      string
	BasicURL     string
	AdvancedURL  string
	SortURLs     map[string]string
	JQLError     string
}

type navigatorChip struct {
	Label string
	URL   string
}

const navigatorPageSize = 50

var navigatorSortFields = map[string]struct{}{
	"key": {}, "summary": {}, "status": {}, "priority": {}, "assignee": {}, "updated": {},
}

type navigatorParams struct {
	Mode      string
	JQL       string
	Text      string
	Status    string
	Assignee  string
	Sort      string
	Direction string
	Page      int
	SortSet   bool
}

func parseNavigatorParams(values url.Values) navigatorParams {
	mode := values.Get("mode")
	if mode != "basic" && mode != "advanced" {
		if values.Get("jql") != "" {
			mode = "advanced"
		} else {
			mode = "basic"
		}
	}
	sortField := strings.ToLower(values.Get("sort"))
	if _, ok := navigatorSortFields[sortField]; !ok {
		sortField = "updated"
	}
	direction := strings.ToLower(values.Get("direction"))
	if direction != "asc" && direction != "desc" {
		direction = "desc"
	}
	page := 1
	if requested, err := strconv.Atoi(values.Get("page")); err == nil && requested > 1 {
		// Bound offsets supplied by an untrusted URL while still allowing far
		// more pages than a human-operated navigator can realistically reach.
		page = min(requested, 1_000_000)
	}
	params := navigatorParams{
		Mode: mode, JQL: strings.TrimSpace(values.Get("jql")), Text: strings.TrimSpace(values.Get("text")),
		Status: values.Get("status"), Assignee: values.Get("assignee"),
		Sort: sortField, Direction: direction, Page: page, SortSet: values.Has("sort") || values.Has("direction"),
	}
	if mode == "advanced" && !params.SortSet {
		if parsed, err := jql.Parse(params.JQL); err == nil && parsed.OrderBy != nil {
			if _, ok := navigatorSortFields[parsed.OrderBy.Field]; ok {
				params.Sort = parsed.OrderBy.Field
				if parsed.OrderBy.Desc {
					params.Direction = "desc"
				} else {
					params.Direction = "asc"
				}
			}
		}
	}
	return params
}

func compileNavigatorSearch(projectKey, userID string, p navigatorParams) (jql.Compiled, error) {
	var query *jql.Query
	if p.Mode == "advanced" {
		parsed, err := jql.Parse(p.JQL)
		if err != nil {
			return jql.Compiled{}, err
		}
		query = parsed
	} else {
		terms := []jql.Node{}
		if p.Text != "" {
			terms = append(terms, jql.Text{Value: p.Text})
		}
		if p.Status != "" {
			terms = append(terms, jql.Clause{Field: "status", Op: "=", Values: []string{p.Status}})
		}
		switch p.Assignee {
		case "":
		case "currentUser()":
			terms = append(terms, jql.Clause{Field: "assignee", Op: "=", Values: []string{"currentUser()"}})
		case "unassigned":
			terms = append(terms, jql.Clause{Field: "assignee", Op: "empty"})
		default:
			terms = append(terms, jql.Clause{Field: "assignee", Op: "=", Values: []string{p.Assignee}})
		}
		var root jql.Node = jql.Text{Value: ""}
		if len(terms) == 1 {
			root = terms[0]
		} else if len(terms) > 1 {
			root = jql.And{Terms: terms}
		}
		query = &jql.Query{Root: root}
	}

	// A project navigator is a project-scoped view even when advanced JQL
	// names a different project (or none). Enforce that boundary structurally
	// instead of trusting user-entered query text.
	query.Root = jql.And{Terms: []jql.Node{
		jql.Clause{Field: "project", Op: "=", Values: []string{projectKey}},
		query.Root,
	}}
	if p.Mode == "basic" || p.SortSet {
		query.OrderBy = &jql.Order{Field: p.Sort, Desc: p.Direction == "desc"}
	}
	compiled := jql.CompileAt(query, userID, jql.DefaultResolver(), 2)
	if compiled.Err != nil {
		return jql.Compiled{}, compiled.Err
	}
	return compiled, nil
}

func basicAsJQL(p navigatorParams) string {
	parts := make([]string, 0, 4)
	if p.Text != "" {
		parts = append(parts, strconv.Quote(p.Text))
	}
	if p.Status != "" {
		parts = append(parts, "status = "+strconv.Quote(p.Status))
	}
	switch p.Assignee {
	case "currentUser()":
		parts = append(parts, "assignee = currentUser()")
	case "unassigned":
		parts = append(parts, "assignee IS EMPTY")
	case "":
	default:
		parts = append(parts, "assignee = "+strconv.Quote(p.Assignee))
	}
	query := strings.Join(parts, " AND ")
	if query == "" {
		query = "ORDER BY " + p.Sort + " " + strings.ToUpper(p.Direction)
	} else {
		query += " ORDER BY " + p.Sort + " " + strings.ToUpper(p.Direction)
	}
	return query
}

func navigatorURL(projectKey string, p navigatorParams, page int) string {
	values := url.Values{}
	values.Set("mode", p.Mode)
	if p.Mode == "advanced" {
		if p.JQL != "" {
			values.Set("jql", p.JQL)
		}
	} else {
		if p.Text != "" {
			values.Set("text", p.Text)
		}
		if p.Status != "" {
			values.Set("status", p.Status)
		}
		if p.Assignee != "" {
			values.Set("assignee", p.Assignee)
		}
	}
	values.Set("sort", p.Sort)
	values.Set("direction", p.Direction)
	if page > 1 {
		values.Set("page", strconv.Itoa(page))
	}
	return "/issues/" + url.PathEscape(projectKey) + "?" + values.Encode()
}

func navigatorChips(projectKey string, p navigatorParams, members []*models.User) []navigatorChip {
	if p.Mode != "basic" {
		return nil
	}
	chips := make([]navigatorChip, 0, 3)
	if p.Text != "" {
		without := p
		without.Text = ""
		chips = append(chips, navigatorChip{Label: "Text: " + p.Text, URL: navigatorURL(projectKey, without, 1)})
	}
	if p.Status != "" {
		without := p
		without.Status = ""
		chips = append(chips, navigatorChip{Label: "Status: " + p.Status, URL: navigatorURL(projectKey, without, 1)})
	}
	if p.Assignee != "" {
		name := p.Assignee
		switch p.Assignee {
		case "currentUser()":
			name = "Current user"
		case "unassigned":
			name = "Unassigned"
		default:
			for _, member := range members {
				if member.ID == p.Assignee {
					name = member.DisplayName
					break
				}
			}
		}
		without := p
		without.Assignee = ""
		chips = append(chips, navigatorChip{Label: "Assignee: " + name, URL: navigatorURL(projectKey, without, 1)})
	}
	return chips
}

// writePage renders a full page; template failures are loud 500s.
func writePage(w http.ResponseWriter, name string, data any) {
	if err := render.Page(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// writeFragment renders an HTMX fragment; template failures are loud 500s.
func writeFragment(w http.ResponseWriter, name string, data any) {
	if err := render.Fragment(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// parseForm reads the request form or answers 400 on malformed bodies.
func parseForm(w http.ResponseWriter, r *http.Request) bool {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form body", http.StatusBadRequest)
		return false
	}
	return true
}

func (h *Handler) currentUser(r *http.Request) *models.User {
	userID, err := authn.Identify(r.Context(), h.Store, r)
	if err != nil {
		return nil
	}
	u, err := h.Store.UserByID(r.Context(), userID)
	if err != nil {
		return nil
	}
	return u
}

func (h *Handler) memberWorkspace(r *http.Request, user *models.User) (string, bool) {
	if h.WorkspaceSlug == "" {
		return "", false
	}
	wsID, err := h.Store.WorkspaceBySlug(r.Context(), h.WorkspaceSlug)
	if err != nil {
		return "", false
	}
	ok, err := authz.CanSeeWorkspace(r.Context(), h.Store, wsID, user.ID)
	if err != nil || !ok {
		return "", false
	}
	return wsID, true
}

func (h *Handler) requireUser(w http.ResponseWriter, r *http.Request) string {
	userID, err := authn.Identify(r.Context(), h.Store, r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return ""
	}
	return userID
}

// buildIssueView assembles everything the issue_view fragment needs.
func (h *Handler) issueForUser(r *http.Request, user *models.User, wsID, idOrKey string) (*models.Issue, error) {
	issue, err := h.Store.IssueByIDOrKey(r.Context(), wsID, idOrKey)
	if err != nil {
		return nil, err
	}
	visible, err := authz.CanSeeIssue(r.Context(), h.Store, wsID, issue.ProjectID, user.ID, issue.SecurityLevelID)
	if err != nil || !visible {
		return nil, fmt.Errorf("issue %q not found", idOrKey)
	}
	return issue, nil
}

func (h *Handler) buildIssueView(r *http.Request, user *models.User, wsID, idOrKey string) (*models.IssueView, error) {
	issue, err := h.issueForUser(r, user, wsID, idOrKey)
	if err != nil {
		return nil, err
	}
	comments, err := h.Store.CommentsByIssue(r.Context(), issue.ID)
	if err != nil {
		return nil, err
	}
	history, err := h.Store.IssueChangelog(r.Context(), wsID, issue.ID)
	if err != nil {
		return nil, err
	}
	attachments, err := h.Store.AttachmentsByIssue(r.Context(), issue.ID)
	if err != nil {
		return nil, err
	}
	worklogs, err := h.Store.WorklogsByIssue(r.Context(), issue.ID)
	if err != nil {
		return nil, err
	}
	wf, err := h.Store.WorkflowForProject(r.Context(), issue.ProjectID)
	if err != nil {
		return nil, err
	}
	var transitions []models.WorkflowTransition
	for _, t := range wf.Available(issue.Status.ID) {
		transitions = append(transitions, models.WorkflowTransition{ID: t.ID, Name: t.Name})
	}
	editView, err := h.buildEditDialogView(r.Context(), wsID, issue)
	if err != nil {
		return nil, err
	}
	priorities, err := h.Store.Priorities(r.Context())
	if err != nil {
		return nil, err
	}
	watcherIDs, err := h.Store.WatchersByIssue(r.Context(), issue.ID)
	if err != nil {
		return nil, err
	}
	watchers := make([]models.User, 0, len(watcherIDs))
	isWatching := false
	for _, watcherID := range watcherIDs {
		if watcherID == user.ID {
			isWatching = true
		}
		watcher, err := h.Store.MemberByID(r.Context(), wsID, watcherID)
		if err == nil {
			watchers = append(watchers, *watcher)
		}
	}
	links, err := h.Store.LinksByIssue(r.Context(), issue.ID)
	if err != nil {
		return nil, err
	}
	linkViews := make([]models.IssueLinkView, 0, len(links))
	for _, link := range links {
		otherID, relationship := link.OutwardID, link.Inward
		if link.OutwardID == issue.ID {
			otherID, relationship = link.InwardID, link.Outward
		}
		other, err := h.issueForUser(r, user, wsID, otherID)
		if err != nil {
			continue
		}
		linkViews = append(linkViews, models.IssueLinkView{
			ID: link.ID, Relationship: relationship, IssueKey: other.Key,
			Summary: other.Summary, Status: other.Status,
		})
	}
	linkTypes, err := h.Store.LinkTypes(r.Context())
	if err != nil {
		return nil, err
	}
	activity := make([]models.IssueActivityItem, 0, len(comments)+len(worklogs)+len(history))
	for _, comment := range comments {
		authorName := comment.AuthorName
		if authorName == "" {
			authorName = "Unknown"
		}
		activity = append(activity, models.IssueActivityItem{
			Kind: "comment", ID: comment.ID, AuthorID: comment.AuthorID, AuthorName: authorName,
			Created: comment.Created, Body: comment.Body,
		})
	}
	for _, worklog := range worklogs {
		authorName := worklog.AuthorName
		if authorName == "" {
			authorName = "Unknown"
		}
		activity = append(activity, models.IssueActivityItem{
			Kind: "worklog", ID: worklog.ID, AuthorID: worklog.AuthorID, AuthorName: authorName,
			Created: worklog.Created, Body: worklog.Comment, TimeSpentSeconds: worklog.TimeSpentSeconds,
			CanDelete: worklog.AuthorID == user.ID,
		})
	}
	for _, entry := range history {
		authorName := "Unknown"
		if entry.Author != nil && entry.Author.DisplayName != "" {
			authorName = entry.Author.DisplayName
		}
		activity = append(activity, models.IssueActivityItem{
			Kind: "history", ID: strconv.FormatInt(entry.Seq, 10), AuthorID: entry.AuthorID,
			AuthorName: authorName, Created: entry.Created, Items: entry.Items,
		})
	}
	sort.SliceStable(activity, func(i, j int) bool { return activity[i].Created > activity[j].Created })
	priorityValues := make([]models.Priority, 0, len(priorities))
	for _, priority := range priorities {
		priorityValues = append(priorityValues, *priority)
	}
	linkTypeValues := make([]models.LinkType, 0, len(linkTypes))
	for _, linkType := range linkTypes {
		linkTypeValues = append(linkTypeValues, *linkType)
	}
	return &models.IssueView{
		Issue:             *issue,
		ProjectKey:        projectKeyOf(issue.Key),
		CanEdit:           true,
		CanTriage:         true,
		CurrentUserID:     user.ID,
		Comments:          derefComments(comments),
		Transitions:       transitions,
		History:           history,
		Attachments:       derefAttachments(attachments),
		Worklogs:          derefWorklogs(worklogs),
		Activity:          activity,
		Members:           editView.Members,
		Priorities:        priorityValues,
		SecurityLevels:    editView.SecurityLevels,
		SecurityLevelName: h.Store.SecurityLevelName(r.Context(), issue.ProjectID, issue.SecurityLevelID),
		CustomFields:      editView.CustomFields,
		Watchers:          watchers,
		IsWatching:        isWatching,
		Links:             linkViews,
		LinkTypes:         linkTypeValues,
	}, nil
}

// buildEditDialogView produces the complete edit-command schema for the
// online endpoint. The local replica renders the same fragment from its own
// persisted issue state while offline.
func (h *Handler) buildEditDialogView(ctx context.Context, wsID string, issue *models.Issue) (*models.EditDialogView, error) {
	members, err := h.Store.MembersByWorkspace(ctx, wsID)
	if err != nil {
		return nil, err
	}
	scheme, err := h.Store.SecuritySchemeForProject(ctx, issue.ProjectID)
	if err != nil {
		return nil, err
	}
	customFields, err := h.Store.CustomFieldsForProject(ctx, issue.ProjectID)
	if err != nil {
		return nil, err
	}
	view := &models.EditDialogView{Issue: *issue}
	for _, m := range members {
		view.Members = append(view.Members, *m)
	}
	if scheme != nil {
		for _, lvl := range scheme.Levels {
			view.SecurityLevels = append(view.SecurityLevels, models.WorkflowTransition{ID: lvl.ID, Name: lvl.Name})
		}
	}
	for _, cf := range customFields {
		value := ""
		if raw, ok := issue.Fields[cf.ID]; ok {
			var text string
			if json.Unmarshal(raw, &text) == nil {
				value = text
			} else {
				value = string(raw)
			}
		}
		view.CustomFields = append(view.CustomFields, models.CustomFieldView{ID: cf.ID, Name: cf.Name, Value: value})
	}
	return view, nil
}

func derefComments(in []*models.Comment) []models.Comment {
	out := make([]models.Comment, 0, len(in))
	for _, c := range in {
		out = append(out, *c)
	}
	return out
}

func derefAttachments(in []*models.Attachment) []models.Attachment {
	out := make([]models.Attachment, 0, len(in))
	for _, a := range in {
		out = append(out, *a)
	}
	return out
}

func derefWorklogs(in []*models.Worklog) []models.Worklog {
	out := make([]models.Worklog, 0, len(in))
	for _, w := range in {
		out = append(out, *w)
	}
	return out
}

func isHX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

// serveIssue renders the fragment (HTMX) or the full page.
func (h *Handler) serveIssue(w http.ResponseWriter, r *http.Request, user *models.User, wsID, idOrKey string) {
	view, err := h.buildIssueView(r, user, wsID, idOrKey)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if isHX(r) {
		if err := render.Fragment(w, "issue_view", view); err != nil {
			log.Printf("render issue_view: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	if err := render.Page(w, "page_issue", pageData{User: user, Data: view, Active: "issues"}); err != nil {
		log.Printf("render page_issue: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// ---- auth ----

func (h *Handler) LoginForm(w http.ResponseWriter, r *http.Request) {
	if h.currentUser(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if h.OIDC != nil {
		http.Redirect(w, r, "/auth/shauth", http.StatusSeeOther)
		return
	}
	writePage(w, "page_login", map[string]string{})
}

func (h *Handler) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	token, err := authn.Login(r.Context(), h.Store, r.PostFormValue("email"), r.PostFormValue("password"))
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		writePage(w, "page_login", map[string]string{"Error": "Incorrect email or password."})
		return
	}
	authn.SetSessionCookie(w, token)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	var idToken string
	if c, err := r.Cookie(sessionCookieName()); err == nil {
		if h.OIDC != nil && h.OIDC.endSessionEndpoint != "" {
			idToken, err = h.Store.OIDCSessionToken(r.Context(), authn.SessionHash(c.Value))
			if err != nil {
				log.Printf("OIDC session token: %v", err)
			}
		}
		if err := h.Store.DeleteSession(r.Context(), authn.SessionHash(c.Value)); err != nil {
			log.Printf("delete session: %v", err)
		}
	}
	authn.ClearSessionCookie(w)
	if idToken != "" {
		logoutURL, err := url.Parse(h.OIDC.endSessionEndpoint)
		if err != nil {
			log.Printf("OIDC end-session URL: %v", err)
		} else {
			query := logoutURL.Query()
			query.Set("id_token_hint", idToken)
			query.Set("post_logout_redirect_uri", h.OIDC.postLogoutRedirectURL)
			logoutURL.RawQuery = query.Encode()
			http.Redirect(w, r, logoutURL.String(), http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/signed-out", http.StatusSeeOther)
}

// signedOutData tells the template which sign-in control to offer: when
// Shauth SSO is configured, /login only ever redirects straight through to
// it (LoginForm), so the signed-out page's own control must say exactly
// "Sign in with Shauth" and link there directly -- a generic "Log in" link
// gives an anonymous caller (and Shauth's own SSO validator, which asserts
// on that exact accessible name) no visible way back into the app.
type signedOutData struct{ OIDCEnabled bool }

func (h *Handler) SignedOut(w http.ResponseWriter, r *http.Request) {
	authn.ClearSessionCookie(w)
	writePage(w, "page_signed_out", signedOutData{OIDCEnabled: h.OIDC != nil})
}

// OIDCLogoutComplete is the registered post-logout redirect bridge Shauth
// sends the browser to after RP-initiated logout finishes. It is a
// completion step, not the user-facing destination: it finalizes the local
// session and hands off to the real signed-out page, matching the bridge/
// destination split every other app in this deployment uses (a validator
// checking for one committed page at this bridge, rather than a redirect
// through it, would be checking the wrong thing).
func (h *Handler) OIDCLogoutComplete(w http.ResponseWriter, r *http.Request) {
	authn.ClearSessionCookie(w)
	http.Redirect(w, r, "/signed-out", http.StatusSeeOther)
}

func sessionCookieName() string { return "zzira_session" }

// ---- pages ----

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	stats, err := h.Store.DashboardData(r.Context(), wsID, user.ID)
	if err != nil {
		log.Printf("dashboard: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writePage(w, "page_home", pageData{User: user, Data: stats, Active: "dashboard"})
}

func (h *Handler) CreateDialog(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	project, err := h.Store.DefaultProjectInWorkspace(r.Context(), wsID)
	if err != nil {
		http.Error(w, "no project", http.StatusInternalServerError)
		return
	}
	issueType, err := h.Store.FirstIssueType(r.Context())
	if err != nil {
		http.Error(w, "no issue type", http.StatusInternalServerError)
		return
	}
	writeFragment(w, "create_dialog", createDialogData{Project: project, IssueType: issueType})
}

func (h *Handler) CreateIssue(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	issue, _, err := h.Commands.CreateIssue(r.Context(), commands.CreateIssueInput{
		ActorID:     user.ID,
		WorkspaceID: wsID,
		ProjectKey:  r.PostFormValue("project"),
		Summary:     strings.TrimSpace(r.PostFormValue("summary")),
		Description: r.PostFormValue("description"),
		IssueTypeID: r.PostFormValue("issuetype"),
	})
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		project, perr := h.Store.DefaultProjectInWorkspace(r.Context(), wsID)
		issueType, terr := h.Store.FirstIssueType(r.Context())
		if perr == nil && terr == nil {
			writeFragment(w, "create_dialog", createDialogData{Project: project, IssueType: issueType, Error: err.Error()})
		}
		return
	}
	w.Header().Set("HX-Redirect", "/browse/"+issue.Key)
	w.WriteHeader(http.StatusNoContent)
}

// ProjectIssues serves /issues/{projectKey} — the issue navigator with JQL.
func (h *Handler) ProjectIssues(w http.ResponseWriter, r *http.Request, key string) {
	user := h.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	project, err := h.Store.ProjectByKey(r.Context(), wsID, key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	statuses, err := h.Store.AllStatuses(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	members, err := h.Store.MembersByWorkspace(r.Context(), wsID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	filters, err := h.Store.ListFilters(r.Context(), wsID, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	values := r.URL.Query()
	activeFilter := values.Get("filter")
	filterError := ""
	if activeFilter != "" {
		var selected *models.Filter
		for _, filter := range filters {
			if filter.ID == activeFilter {
				selected = filter
				break
			}
		}
		if selected == nil {
			filterError = "Saved filter not found."
		} else {
			values.Set("mode", "advanced")
			values.Set("jql", selected.JQL)
		}
	}
	params := parseNavigatorParams(values)
	data := projectIssuesData{
		Project: project, Statuses: statuses, Members: members, Filters: filters, ActiveFilter: activeFilter,
		Mode: params.Mode, JQL: params.JQL, Text: params.Text, Status: params.Status, Assignee: params.Assignee,
		Sort: params.Sort, Direction: params.Direction, Page: params.Page, SortURLs: map[string]string{},
	}
	data.Chips = navigatorChips(project.Key, params, members)
	if params.Mode == "basic" {
		data.SaveJQL = basicAsJQL(params)
	} else {
		data.SaveJQL = params.JQL
	}
	compiled, err := compileNavigatorSearch(project.Key, user.ID, params)
	if filterError != "" {
		data.JQLError = filterError
	} else if err != nil {
		data.JQLError = err.Error()
	} else {
		offset := (params.Page - 1) * navigatorPageSize
		issues, total, searchErr := h.Store.Search(r.Context(), wsID, user.ID, compiled, navigatorPageSize, offset)
		if searchErr != nil {
			data.JQLError = searchErr.Error()
		} else {
			data.PageCount = max(1, (total+navigatorPageSize-1)/navigatorPageSize)
			if total > 0 && params.Page > data.PageCount {
				params.Page = data.PageCount
				data.Page = params.Page
				offset = (params.Page - 1) * navigatorPageSize
				issues, total, searchErr = h.Store.Search(r.Context(), wsID, user.ID, compiled, navigatorPageSize, offset)
			}
			if searchErr != nil {
				data.JQLError = searchErr.Error()
			} else {
				data.Issues, data.Total = issues, total
				if len(issues) > 0 {
					data.Selected = issues[0]
					data.ResultStart = offset + 1
					data.ResultEnd = offset + len(issues)
				}
			}
		}
	}
	if params.Page > 1 {
		data.PreviousURL = navigatorURL(project.Key, params, params.Page-1)
	}
	if data.JQLError == "" && params.Page < data.PageCount {
		data.NextURL = navigatorURL(project.Key, params, params.Page+1)
	}
	data.BasicURL = "/issues/" + url.PathEscape(project.Key) + "?mode=basic"
	advanced := params
	advanced.Mode = "advanced"
	if params.Mode == "basic" {
		advanced.JQL = basicAsJQL(params)
	}
	data.AdvancedURL = navigatorURL(project.Key, advanced, 1)
	for field := range navigatorSortFields {
		sortParams := params
		sortParams.Page = 1
		sortParams.SortSet = true
		if params.Sort == field {
			if params.Direction == "asc" {
				sortParams.Direction = "desc"
			} else {
				sortParams.Direction = "asc"
			}
		} else {
			sortParams.Direction = "asc"
		}
		sortParams.Sort = field
		if sortParams.Mode == "advanced" {
			orderedJQL, orderErr := jql.SetOrder(sortParams.JQL, field, sortParams.Direction == "desc")
			if orderErr == nil {
				sortParams.JQL = orderedJQL
			}
		}
		data.SortURLs[field] = navigatorURL(project.Key, sortParams, 1)
	}
	writePage(w, "page_project", pageData{User: user, Data: data, Active: "issues"})
}

// SaveNavigatorFilter persists the current, already-valid search and stars it
// for the creator so it appears in the navigator's saved-filter control.
func (h *Handler) SaveNavigatorFilter(w http.ResponseWriter, r *http.Request, projectKey string) {
	if !parseForm(w, r) {
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	project, err := h.Store.ProjectByKey(r.Context(), wsID, projectKey)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	filterJQL := strings.TrimSpace(r.PostFormValue("jql"))
	if name == "" {
		http.Error(w, "filter name is required", http.StatusBadRequest)
		return
	}
	if _, err := compileNavigatorSearch(project.Key, user.ID, navigatorParams{Mode: "advanced", JQL: filterJQL, Sort: "updated", Direction: "desc"}); err != nil {
		http.Error(w, "invalid JQL: "+err.Error(), http.StatusBadRequest)
		return
	}
	filter, err := h.Store.CreateFavouriteFilter(r.Context(), store.NewID("flt"), wsID, name, filterJQL, "", user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/issues/"+url.PathEscape(project.Key)+"?mode=advanced&filter="+url.QueryEscape(filter.ID), http.StatusSeeOther)
}

// IssuePreview renders the permission-checked contextual panel used by the
// issue navigator. The canonical browse link remains available without HTMX.
func (h *Handler) IssuePreview(w http.ResponseWriter, r *http.Request, issueKey string) {
	user := h.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	issue, err := h.issueForUser(r, user, wsID, issueKey)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	writeFragment(w, "issue_preview", issue)
}

// BrowseIssue serves /browse/{key} — the Jira-style issue URL.
func (h *Handler) BrowseIssue(w http.ResponseWriter, r *http.Request, key string) {
	user := h.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	h.serveIssue(w, r, user, wsID, key)
}

// ---- issue mutations from the web edge ----

func (h *Handler) TransitionIssue(w http.ResponseWriter, r *http.Request, key string) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !parseForm(w, r) {
		return
	}
	if _, _, err := h.Commands.TransitionIssue(r.Context(), user.ID, wsID, key, r.PostFormValue("transition")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.serveIssue(w, r, user, wsID, key)
}

func (h *Handler) AddComment(w http.ResponseWriter, r *http.Request, key string) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !parseForm(w, r) {
		return
	}
	in := commands.AddCommentInput{
		ActorID: user.ID, WorkspaceID: wsID, IssueIDOrKey: key, PlainText: r.PostFormValue("body"),
	}
	// Rich editor path: body is an ADF JSON document.
	if raw := strings.TrimSpace(r.PostFormValue("adf")); raw != "" {
		in.PlainText = ""
		in.Body = adf.Normalize(json.RawMessage(raw))
		if !json.Valid(in.Body) {
			http.Error(w, "invalid ADF", http.StatusBadRequest)
			return
		}
	}
	if _, _, err := h.Commands.AddComment(r.Context(), in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.serveIssue(w, r, user, wsID, key)
}

// UploadAttachment accepts a multipart upload from the web edge.
func (h *Handler) UploadAttachment(w http.ResponseWriter, r *http.Request, key string) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)        // bounded: 32MB max upload
	if err := r.ParseMultipartForm(32 << 20); err != nil { // #nosec G120 -- body capped by MaxBytesReader above
		http.Error(w, "multipart form required", http.StatusBadRequest)
		return
	}
	defer cleanupMultipart(r)
	for _, files := range r.MultipartForm.File {
		for _, fh := range files {
			f, err := fh.Open()
			if err != nil {
				http.Error(w, "could not read upload", http.StatusBadRequest)
				return
			}
			_, _, err = h.Commands.AddAttachment(r.Context(), user.ID, wsID, key, fh.Filename, fh.Header.Get("Content-Type"), f)
			if closeErr := f.Close(); closeErr != nil {
				log.Printf("attachment file close: %v", closeErr)
			}
			if err != nil {
				http.Error(w, "upload failed", http.StatusBadRequest)
				return
			}
		}
	}
	if r.MultipartForm == nil || len(r.MultipartForm.File) == 0 {
		http.Error(w, "no file uploaded", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/browse/"+key, http.StatusSeeOther)
}

// AddWorklog records time from the web edge.
func (h *Handler) AddWorklog(w http.ResponseWriter, r *http.Request, key string) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !parseForm(w, r) {
		return
	}
	seconds, err := strconv.Atoi(r.PostFormValue("seconds"))
	if err != nil || seconds <= 0 {
		http.Error(w, "seconds must be a positive number", http.StatusBadRequest)
		return
	}
	if _, _, err := h.Commands.AddWorklog(r.Context(), user.ID, wsID, key, adf.ParagraphDoc(r.PostFormValue("comment")), seconds); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.serveIssue(w, r, user, wsID, key)
}

func (h *Handler) DeleteWorklog(w http.ResponseWriter, r *http.Request, key, worklogID string) {
	user, wsID, ok := h.issueMutationContext(w, r, key)
	if !ok {
		return
	}
	issue, _ := h.issueForUser(r, user, wsID, key)
	worklog, err := h.Store.WorklogByID(r.Context(), wsID, worklogID)
	if err != nil || worklog.IssueID != issue.ID {
		http.NotFound(w, r)
		return
	}
	if _, err := h.Commands.DeleteWorklog(r.Context(), user.ID, wsID, worklogID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.serveIssue(w, r, user, wsID, key)
}

func (h *Handler) DeleteAttachment(w http.ResponseWriter, r *http.Request, key, attachmentID string) {
	user, wsID, ok := h.issueMutationContext(w, r, key)
	if !ok {
		return
	}
	issue, _ := h.issueForUser(r, user, wsID, key)
	attachment, err := h.Store.AttachmentByID(r.Context(), wsID, attachmentID)
	if err != nil || attachment.IssueID != issue.ID {
		http.NotFound(w, r)
		return
	}
	if _, err := h.Commands.DeleteAttachment(r.Context(), user.ID, wsID, attachmentID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.serveIssue(w, r, user, wsID, key)
}

func (h *Handler) UpdateIssueField(w http.ResponseWriter, r *http.Request, key string) {
	user, wsID, ok := h.issueMutationContext(w, r, key)
	if !ok || !parseForm(w, r) {
		return
	}
	field, value := r.PostFormValue("field"), r.PostFormValue("value")
	in := commands.UpdateIssueInput{ActorID: user.ID, WorkspaceID: wsID, IssueIDOrKey: key}
	switch field {
	case "summary":
		value = strings.TrimSpace(value)
		in.Summary = &value
	case "priority":
		in.PriorityID = &value
	case "assignee":
		in.AssigneeID = &value
	case "security":
		in.SecurityLevelID = &value
	case "labels":
		labels := strings.Split(value, ",")
		if strings.TrimSpace(value) == "" {
			labels = []string{}
		}
		in.Labels = &labels
	default:
		const prefix = "custom:"
		if !strings.HasPrefix(field, prefix) || len(field) == len(prefix) {
			http.Error(w, "unknown issue field", http.StatusBadRequest)
			return
		}
		in.Fields = map[string]json.RawMessage{strings.TrimPrefix(field, prefix): json.RawMessage(strconv.Quote(value))}
	}
	if _, _, err := h.Commands.UpdateIssue(r.Context(), in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.serveIssue(w, r, user, wsID, key)
}

func (h *Handler) SetWatching(w http.ResponseWriter, r *http.Request, key string) {
	user, wsID, ok := h.issueMutationContext(w, r, key)
	if !ok || !parseForm(w, r) {
		return
	}
	if _, err := h.Commands.SetWatching(r.Context(), user.ID, wsID, key, r.PostFormValue("watching") == "true"); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.serveIssue(w, r, user, wsID, key)
}

func (h *Handler) LinkIssue(w http.ResponseWriter, r *http.Request, key string) {
	user, wsID, ok := h.issueMutationContext(w, r, key)
	if !ok || !parseForm(w, r) {
		return
	}
	otherKey := strings.TrimSpace(r.PostFormValue("issue"))
	if otherKey == "" {
		http.Error(w, "linked issue key is required", http.StatusBadRequest)
		return
	}
	if _, _, err := h.Commands.LinkIssue(r.Context(), user.ID, wsID, key, r.PostFormValue("type"), otherKey); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.serveIssue(w, r, user, wsID, key)
}

func (h *Handler) DeleteIssueLink(w http.ResponseWriter, r *http.Request, key, linkID string) {
	user, wsID, ok := h.issueMutationContext(w, r, key)
	if !ok {
		return
	}
	if _, err := h.Commands.DeleteIssueLink(r.Context(), user.ID, wsID, key, linkID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.serveIssue(w, r, user, wsID, key)
}

func (h *Handler) issueMutationContext(w http.ResponseWriter, r *http.Request, key string) (*models.User, string, bool) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return nil, "", false
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return nil, "", false
	}
	if _, err := h.issueForUser(r, user, wsID, key); err != nil {
		http.NotFound(w, r)
		return nil, "", false
	}
	return user, wsID, true
}

func (h *Handler) EditDialog(w http.ResponseWriter, r *http.Request, key string) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	issue, err := h.issueForUser(r, user, wsID, key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	view, err := h.buildEditDialogView(r.Context(), wsID, issue)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeFragment(w, "edit_dialog", *view)
}

func (h *Handler) EditIssue(w http.ResponseWriter, r *http.Request, key string) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !parseForm(w, r) {
		return
	}
	summary := strings.TrimSpace(r.PostFormValue("summary"))
	description := adf.ParagraphDoc(r.PostFormValue("description"))
	assignee := r.PostFormValue("assignee")
	in := commands.UpdateIssueInput{
		ActorID: user.ID, WorkspaceID: wsID, IssueIDOrKey: key,
		Summary: &summary, Description: description, AssigneeID: &assignee,
	}
	if sec, ok := r.PostForm["security"]; ok {
		in.SecurityLevelID = &sec[0] // "" = public
	}
	customFields, cfErr := h.Store.CustomFields(r.Context())
	if cfErr != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	for _, cf := range customFields {
		if v, ok := r.PostForm[cf.ID]; ok {
			if in.Fields == nil {
				in.Fields = map[string]json.RawMessage{}
			}
			in.Fields[cf.ID] = json.RawMessage(strconv.Quote(v[0]))
		}
	}
	if _, _, err := h.Commands.UpdateIssue(r.Context(), in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.serveIssue(w, r, user, wsID, key)
}

func (h *Handler) DeleteIssue(w http.ResponseWriter, r *http.Request, key string) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	issue, err := h.issueForUser(r, user, wsID, key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := h.Commands.DeleteIssue(r.Context(), user.ID, wsID, issue.ID, "deleted via UI"); err != nil {
		http.Error(w, "delete failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/issues/"+projectKeyOf(issue.Key))
	w.WriteHeader(http.StatusNoContent)
}

func projectKeyOf(key string) string {
	if idx := strings.IndexByte(key, '-'); idx > 0 {
		return key[:idx]
	}
	return key
}

// cleanupMultipart removes temp files from a parsed multipart form; failures
// are logged (cleanup must not fail the response) but never silent.
func cleanupMultipart(r *http.Request) {
	if r.MultipartForm == nil {
		return
	}
	if err := r.MultipartForm.RemoveAll(); err != nil {
		log.Printf("multipart cleanup: %v", err)
	}
}
