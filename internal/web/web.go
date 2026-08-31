package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
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
	User *models.User
	Data any
}

type createDialogData struct {
	Project   *models.Project
	IssueType *models.IssueType
	Error     string
}

type projectIssuesData struct {
	Project  *models.Project
	Issues   []*models.Issue
	JQL      string
	Total    int
	JQLError string
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
	return &models.IssueView{
		Issue:       *issue,
		ProjectKey:  projectKeyOf(issue.Key),
		CanEdit:     true,
		Comments:    derefComments(comments),
		Transitions: transitions,
		History:     history,
		Attachments: derefAttachments(attachments),
		Worklogs:    derefWorklogs(worklogs),
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
	if err := render.Page(w, "page_issue", pageData{User: user, Data: view}); err != nil {
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

func (h *Handler) SignedOut(w http.ResponseWriter, r *http.Request) {
	authn.ClearSessionCookie(w)
	writePage(w, "page_signed_out", map[string]string{})
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
	writePage(w, "page_home", pageData{User: user, Data: stats})
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
	data := projectIssuesData{Project: project, JQL: r.URL.Query().Get("jql")}
	if data.JQL != "" {
		q, err := jql.Parse(data.JQL)
		if err != nil {
			data.JQLError = err.Error()
		} else {
			compiled := jql.CompileAt(q, user.ID, jql.DefaultResolver(), 2)
			issues, total, err := h.Store.Search(r.Context(), wsID, user.ID, compiled, 200, 0)
			if err != nil {
				data.JQLError = err.Error()
			} else {
				data.Issues, data.Total = issues, total
			}
		}
	} else {
		issues, err := h.Store.IssuesByProject(r.Context(), wsID, project.ID, user.ID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		data.Issues = issues
		data.Total = len(issues)
	}
	writePage(w, "page_project", pageData{User: user, Data: data})
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
	if _, err := h.Store.DeleteIssue(r.Context(), user.ID, wsID, issue.ID, "deleted via UI"); err != nil {
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
