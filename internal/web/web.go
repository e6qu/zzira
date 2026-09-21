package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/automation"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/render"
	"github.com/e6qu/zzira/internal/secretbox"
	"github.com/e6qu/zzira/internal/store"
)

type Handler struct {
	Store                             *store.Store
	Commands                          *commands.Service
	Automation                        *automation.Service
	OIDC                              *OIDC
	IdentityProviders                 *ProviderRegistry
	ProviderSecrets                   *secretbox.Box
	IdentityExternalURL               string
	WorkspaceSlug                     string
	BaseURL                           string
	InvitationNotificationsConfigured bool
	DomainTXTLookup                   func(context.Context, string) ([]string, error)
	// Routes are the application's routes, which scheduled report emails and
	// app requests are served through in-process.
	Routes http.Handler
}

type pageData struct {
	User         *models.User
	Data         any
	Active       string
	Navigation   *workspaceNavigation
	Announcement *models.AnnouncementBanner
	Site         *render.SiteLook
	// WikiLook is the Confluence look and feel wiki pages apply, if any.
	WikiLook *wikiLookView
	// ServiceLook is the help center branding service pages apply, if any.
	ServiceLook *serviceLookView
}

// SiteLook is the look and feel the page shows.
func (p pageData) SiteLook() render.SiteLook {
	if p.Site == nil {
		return render.DefaultSiteLook
	}
	return *p.Site
}

// siteLookFor is the look and feel a site's application properties set,
// keeping ZZIRA's own for what they leave unset.
func siteLookFor(properties map[string]string) render.SiteLook {
	look := render.DefaultSiteLook
	if title := strings.TrimSpace(properties["jira.title"]); title != "" {
		look.Title = title
	}
	look.LogoURL, look.FaviconURL = properties["jira.lf.logo.url"], properties["jira.lf.favicon.url"]
	look.NavigationBackground, look.NavigationHighlight = properties["jira.lf.navigation.bgcolour"], properties["jira.lf.navigation.highlightcolour"]
	look.ShowTitle = properties["jira.lf.logo.show.application.title"] == "true"
	look.FaviconHiResURL = properties["jira.lf.favicon.hires.url"]
	if colour := strings.TrimSpace(properties["jira.lf.hero.button.base.bg.colour"]); lookAndFeelColour.MatchString(colour) && whiteTextContrast(colour) >= 4.5 {
		look.HeroButtonBackground = colour
	}
	look.DateComplete, look.DateDay = models.SiteDateLayouts(properties)
	return look
}

// siteLook loads the workspace's look and feel, falling back to ZZIRA's own.
func (h *Handler) siteLook(r *http.Request, workspaceID string) render.SiteLook {
	configuration, err := h.Store.JiraSiteConfiguration(r.Context(), workspaceID)
	if err != nil {
		return render.DefaultSiteLook
	}
	return siteLookFor(configuration.ApplicationProperties)
}

type createDialogData struct {
	Metadata                 *models.IssueCreateMetadata
	Selected                 models.CreateProjectMeta
	SelectedIssueTypeSubtask bool
	Values                   map[string]string
	// DetailTabs are the screen's tabs when it has more than one; the form
	// then shows the detail fields as tabs rather than one list.
	DetailTabs []models.FieldTabView
	Error      string
	CreatedKey string
}

type projectIssuesData struct {
	Project         *models.Project
	Projects        []*models.Project
	IssueTypes      []models.IssueType
	BulkTransitions []models.WorkflowTransition
	// BulkPriorities are the priorities a bulk edit can set, which are the
	// project's own. BulkComponents and BulkVersions are the same for the
	// two list fields a project owns.
	BulkPriorities []models.Priority
	BulkComponents []*models.ProjectComponent
	BulkVersions   []*models.Version
	BoardID        string
	Issues         []*models.Issue
	Selected       *models.Issue
	Statuses       []models.Status
	Members        []*models.User
	Filters        []*models.Filter
	ActiveFilter   string
	Chips          []navigatorChip
	SaveJQL        string
	Mode           string
	JQL            string
	Text           string
	Status         string
	Assignee       string
	Sort           string
	Direction      string
	Total          int
	ResultStart    int
	ResultEnd      int
	Page           int
	PageCount      int
	PreviousURL    string
	NextURL        string
	BasicURL       string
	AdvancedURL    string
	SortURLs       map[string]string
	JQLError       string
	CanBulk        bool
}

type bulkIssueTaskData struct {
	Project      *models.Project
	Task         store.APITask
	Processed    int
	Failed       int
	Inaccessible int
	Total        int
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

func compileNavigatorSearch(ctx context.Context, st *store.Store, workspaceID, projectKey, userID string, p navigatorParams) (jql.Compiled, error) {
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
		query.Orders = nil
	}
	if st != nil {
		if err := st.ExpandAppJQL(ctx, workspaceID, query); err != nil {
			return jql.Compiled{}, err
		}
	}
	resolver := jql.DefaultResolver()
	if st != nil {
		var err error
		if resolver, err = st.JQLResolver(ctx, workspaceID); err != nil {
			return jql.Compiled{}, err
		}
	}
	compiled := jql.CompileAt(query, userID, resolver, 2)
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
	writePageStatus(w, name, data, http.StatusOK)
}

func writePageStatus(w http.ResponseWriter, name string, data any, status int) {
	var output bytes.Buffer
	if err := render.Page(&output, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = output.WriteTo(w)
}

// writeFragment renders an HTMX fragment; template failures are loud 500s.
func writeFragment(w http.ResponseWriter, name string, data any) {
	var output bytes.Buffer
	if err := render.Fragment(&output, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = output.WriteTo(w)
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
	// A report drawn in-process for a scheduled email is drawn as its
	// recipient; no request from outside can carry this context value.
	userID, drawn := r.Context().Value(reportRecipientKey{}).(string)
	if !drawn {
		identified, err := authn.Identify(r.Context(), h.Store, r)
		if err != nil {
			return nil
		}
		userID = identified
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

// issueForUser loads a work item the person can see.
func (h *Handler) issueForUser(r *http.Request, user *models.User, wsID, idOrKey string) (*models.Issue, error) {
	issue, err := h.Store.IssueByIDOrKey(r.Context(), wsID, idOrKey)
	if err != nil {
		return nil, err
	}
	visible, err := authz.CanSeeIssue(r.Context(), h.Store, wsID, issue.ProjectID, user.ID, issue.ID, issue.SecurityLevelID)
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
	project, err := h.Store.ProjectByIDOrKey(r.Context(), wsID, issue.ProjectID)
	if err != nil {
		return nil, err
	}
	boards, err := h.Store.BoardsByWorkspace(r.Context(), wsID)
	if err != nil {
		return nil, err
	}
	boardID := ""
	for _, board := range boards {
		if board.ProjectID == issue.ProjectID {
			boardID = board.ID
			break
		}
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
	wf, err := h.Store.WorkflowForProjectAndIssueType(r.Context(), issue.ProjectID, issue.IssueType.ID)
	if err != nil {
		return nil, err
	}
	var transitions []models.WorkflowTransition
	evaluation, err := h.Store.IssueWorkflowEvaluation(r.Context(), wsID, user.ID, issue)
	if err != nil {
		return nil, err
	}
	editView, err := h.buildEditDialogView(r.Context(), wsID, user.ID, issue)
	if err != nil {
		return nil, err
	}
	// The edit dialog has already resolved every custom field's type, value and
	// options for this work item, so a transition screen names the same ones
	// rather than loading them again.
	customByID := map[string]models.CustomFieldView{}
	for _, field := range editView.CustomFields {
		customByID[field.ID] = field
	}
	// A transition is offered only to someone who may move the work, as the
	// REST resource offers them: a control the server would refuse is not a
	// control.
	canTransition, err := h.Store.HasProjectPermission(r.Context(), wsID, user.ID, issue.ProjectID, issue.ID, "TRANSITION_ISSUES")
	if err != nil {
		return nil, err
	}
	if canTransition {
		for _, t := range wf.AvailableFor(issue.Status.ID, evaluation) {
			message, _, _ := t.ScreenReminder()
			screenFields := t.ScreenFields()
			asked := []models.CustomFieldView{}
			for _, id := range screenFields {
				if field, ok := customByID[id]; ok {
					asked = append(asked, field)
				}
			}
			transitions = append(transitions, models.WorkflowTransition{ID: t.ID, Name: t.Name, ScreenFields: screenFields, ScreenCustomFields: asked, ScreenMessage: message})
		}
	}
	// Resolution is offered to people who may resolve work, as Jira ties the
	// field to the Resolve issues permission.
	resolutions, err := h.Store.ResolutionsForWorkspace(r.Context(), wsID)
	if err != nil {
		return nil, err
	}
	resolutionValues := resolutions
	canResolve, err := h.Store.HasProjectPermission(r.Context(), wsID, user.ID, issue.ProjectID, issue.ID, "RESOLVE_ISSUES")
	if err != nil {
		return nil, err
	}
	// The priority field offers what the project's priority scheme holds.
	priorities, err := h.Store.PrioritiesForProject(r.Context(), wsID, issue.ProjectID)
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
	voterIDs, err := h.Store.VotersByIssue(r.Context(), issue.ID)
	if err != nil {
		return nil, err
	}
	voters := make([]models.User, 0, len(voterIDs))
	hasVoted := false
	for _, voterID := range voterIDs {
		if voterID == user.ID {
			hasVoted = true
		}
		voter, err := h.Store.MemberByID(r.Context(), wsID, voterID)
		if err == nil {
			voters = append(voters, *voter)
		}
	}
	links, err := h.Store.LinksByIssue(r.Context(), issue.ID)
	if err != nil {
		return nil, err
	}
	children, err := h.Store.ChildIssues(r.Context(), wsID, issue.ID)
	if err != nil {
		return nil, err
	}
	forms, err := h.Store.IssueForms(r.Context(), wsID, issue.ID)
	if err != nil {
		return nil, err
	}
	// A software project's Code and Deployments features decide whether its
	// development and delivery evidence shows.
	codeEnabled, err := h.Store.ProjectFeatureEnabled(r.Context(), issue.ProjectID, "jsw.classic.code")
	if err != nil {
		return nil, err
	}
	deploymentsEnabled, err := h.Store.ProjectFeatureEnabled(r.Context(), issue.ProjectID, "jsw.classic.deployments")
	if err != nil {
		return nil, err
	}
	var development []models.DevelopmentItem
	if codeEnabled {
		if development, err = h.Store.DevelopmentItemsForIssue(r.Context(), wsID, issue.Key); err != nil {
			return nil, err
		}
	}
	var delivery []models.DeliveryItem
	if deploymentsEnabled {
		if delivery, err = h.Store.DeliveryItemsForIssues(r.Context(), wsID, []string{issue.Key}); err != nil {
			return nil, err
		}
	}
	// The work item's parent comes from one level above its own work type, in
	// its own project.
	parentOptions, err := h.Store.ParentOptions(r.Context(), wsID, issue.ProjectID, issue.IssueType.HierarchyLevel)
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
	linkTypes, err := h.Store.LinkTypes(r.Context(), wsID)
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
	linkTypeValues := make([]models.LinkType, 0, len(linkTypes))
	for _, linkType := range linkTypes {
		linkTypeValues = append(linkTypeValues, *linkType)
	}
	appPanels, err := h.Store.AppModulesByLocation(r.Context(), wsID, "jira.issue.view")
	if err != nil {
		return nil, err
	}
	appContexts, err := h.Store.AppModulesByLocation(r.Context(), wsID, "jira.issue.context")
	if err != nil {
		return nil, err
	}
	issueFacts := h.appConditionFactsFor(r.Context(), wsID, user, nil, issue)
	appPanels = appModulesShown(appPanels, issueFacts)
	appContexts = selectIssueContextModules(appModulesShown(appContexts, issueFacts))
	issueProperties := map[string]json.RawMessage{}
	if len(appContexts) > 0 {
		issueProperties, err = h.Store.IssueProperties(r.Context(), issue.ID)
		if err != nil {
			return nil, err
		}
	}
	for index := range appContexts {
		decorateIssueContext(&appContexts[index])
		decorateIssueContextStatus(&appContexts[index], issueProperties[issueContextStatusPropertyKey(appContexts[index])], issue.Key)
	}
	appActivityTabs, err := h.Store.AppModulesByLocation(r.Context(), wsID, "jira.issue.activity")
	if err != nil {
		return nil, err
	}
	appActivityTabs = appModulesShown(appActivityTabs, issueFacts)
	appIssueContent, err := h.Store.AppIssueContentForIssue(r.Context(), wsID, issue.ID)
	if err != nil {
		return nil, err
	}
	configuration, err := h.Store.JiraSiteConfiguration(r.Context(), wsID)
	if err != nil {
		return nil, err
	}
	editable, err := h.Commands.IssueEditable(r.Context(), issue)
	if err != nil {
		return nil, err
	}
	return &models.IssueView{
		Issue:               *issue,
		ProjectKey:          project.Key,
		ProjectName:         project.Name,
		BoardID:             boardID,
		CanEdit:             true,
		Editable:            editable,
		CanTriage:           true,
		AttachmentsEnabled:  configuration.AttachmentsEnabled,
		IssueLinkingEnabled: configuration.IssueLinkingEnabled,
		TimeTrackingEnabled: configuration.TimeTrackingEnabled,
		TimeTracking:        models.NewTimeTrackingView(*issue, configuration.TimeTracking),
		VotingEnabled:       configuration.VotingEnabled,
		WatchingEnabled:     configuration.WatchingEnabled,
		CurrentUserID:       user.ID,
		Comments:            derefComments(comments),
		Transitions:         transitions,
		History:             history,
		Attachments:         derefAttachments(attachments),
		Worklogs:            derefWorklogs(worklogs),
		Activity:            activity,
		Members:             editView.Members,
		Priorities:          priorities,
		Resolutions:         resolutionValues,
		CanResolve:          canResolve,
		SecurityLevels:      editView.SecurityLevels,
		SecurityLevelName:   h.Store.SecurityLevelName(r.Context(), issue.ProjectID, issue.SecurityLevelID),
		CustomFields:        editView.CustomFields,
		Watchers:            watchers,
		IsWatching:          isWatching,
		Voters:              voters,
		HasVoted:            hasVoted,
		Links:               linkViews,
		LinkTypes:           linkTypeValues,
		Children:            derefIssues(children),
		ParentOptions:       parentOptions,
		Forms:               derefForms(forms),
		Development:         development,
		Delivery:            delivery,
		CodeDisabled:        !codeEnabled,
		DeploymentsDisabled: !deploymentsEnabled,
		AppPanels:           appPanels,
		AppActivityTabs:     appActivityTabs,
		AppContexts:         appContexts,
		AppIssueContent:     appIssueContent,
	}, nil
}

func selectIssueContextModules(modules []models.AppModule) []models.AppModule {
	modernApps := map[string]bool{}
	for _, module := range modules {
		if module.Type == "jira:issueContext" {
			modernApps[module.AppKey] = true
		}
	}
	selectedApps := map[string]bool{}
	selected := make([]models.AppModule, 0, len(modernApps))
	for _, module := range modules {
		eligible := module.Type == "jira:issueContext" || (module.Type == "jira:issueGlance" && !modernApps[module.AppKey])
		if eligible && !selectedApps[module.AppKey] {
			selected = append(selected, module)
			selectedApps[module.AppKey] = true
		}
	}
	return selected
}

func (h *Handler) SetIssueAppContent(w http.ResponseWriter, r *http.Request, key, moduleID string) {
	user, wsID, ok := h.issueMutationContext(w, r, key)
	if !ok || !parseForm(w, r) {
		return
	}
	issue, err := h.issueForUser(r, user, wsID, key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	added := r.PostFormValue("action") != "remove"
	if err := h.Store.SetAppIssueContent(r.Context(), wsID, issue.ID, user.ID, moduleID, added); err != nil {
		http.Error(w, "issue content module is unavailable", http.StatusBadRequest)
		return
	}
	h.serveIssue(w, r, user, wsID, key)
}

func derefForms(in []*models.IssueForm) []models.IssueForm {
	out := make([]models.IssueForm, 0, len(in))
	for _, form := range in {
		out = append(out, *form)
	}
	return out
}

func derefIssues(in []*models.Issue) []models.Issue {
	out := make([]models.Issue, 0, len(in))
	for _, issue := range in {
		out = append(out, *issue)
	}
	return out
}

// buildEditDialogView produces the complete edit-command schema for the
// online endpoint. The local replica renders the same fragment from its own
// persisted issue state while offline.
// buildEditDialogView is the edit form's data. reader is who is reading it,
// so the fields carry the names their language gives them.
func (h *Handler) buildEditDialogView(ctx context.Context, wsID, reader string, issue *models.Issue) (*models.EditDialogView, error) {
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
	if err := h.Store.TranslateFields(ctx, wsID, h.Store.LocaleForUser(ctx, wsID, reader), customFields); err != nil {
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
	choices, err := h.customFieldChoices(ctx, wsID, issue, customFields, members)
	if err != nil {
		return nil, err
	}
	for _, cf := range customFields {
		value := ""
		if raw, ok := issue.Fields[cf.ID]; ok && string(raw) != "null" {
			var text string
			var list []string
			var cascade struct {
				Parent string `json:"parent"`
				Child  string `json:"child"`
			}
			switch {
			case json.Unmarshal(raw, &text) == nil:
				value = text
			case json.Unmarshal(raw, &list) == nil:
				value = strings.Join(list, ",")
			case json.Unmarshal(raw, &cascade) == nil && cascade.Parent != "":
				value = cascade.Parent
				if cascade.Child != "" {
					value += ":" + cascade.Child
				}
			default:
				value = string(raw)
			}
		}
		fieldView := models.CustomFieldView{ID: cf.ID, Name: cf.Name, Type: cf.Type, Description: cf.Description, Value: value, Display: value, Options: choices[cf.ID]}
		if len(fieldView.Options) > 0 && value != "" {
			names := map[string]string{}
			for _, option := range fieldView.Options {
				names[option.ID] = option.Name
			}
			display := []string{}
			for _, id := range strings.Split(value, ",") {
				if name := names[id]; name != "" {
					display = append(display, name)
				} else {
					display = append(display, id)
				}
			}
			fieldView.Display = strings.Join(display, ", ")
		}
		view.CustomFields = append(view.CustomFields, fieldView)
	}
	return view, nil
}

// customFieldChoices lists what each picker custom field on a work item offers:
// the options of its governing context (a cascading select's options followed
// by each option with its children), site members, groups, projects, or the
// work item's project versions.
func (h *Handler) customFieldChoices(ctx context.Context, wsID string, issue *models.Issue, fields []*models.CustomField, members []*models.User) (map[string][]models.CreateFieldOption, error) {
	choices := map[string][]models.CreateFieldOption{}
	var contexts map[string]models.CustomFieldContextInfo
	var catalog map[string]store.OptionCatalog
	var groups, projects, versions []models.CreateFieldOption
	for _, field := range fields {
		switch field.Type {
		case models.CustomFieldSelect, models.CustomFieldMultiSelect, models.CustomFieldCascadingSelect:
			if contexts == nil {
				byProject, err := h.Store.CustomFieldContextsByProject(ctx, wsID)
				if err != nil {
					return nil, err
				}
				contexts = byProject[issue.ProjectID][issue.IssueType.ID]
				if contexts == nil {
					contexts = map[string]models.CustomFieldContextInfo{}
				}
			}
			options := contexts[field.ID].Options
			if field.Type != models.CustomFieldCascadingSelect {
				choices[field.ID] = options
				continue
			}
			if catalog == nil {
				loaded, err := h.Store.CustomFieldOptionCatalog(ctx, wsID, issue.ProjectID, issue.IssueType.ID)
				if err != nil {
					return nil, err
				}
				catalog = loaded
			}
			cascade := []models.CreateFieldOption{}
			for _, parent := range options {
				cascade = append(cascade, parent)
				children := catalog[field.ID].Children[parent.ID]
				names := make([]string, 0, len(children))
				for name := range children {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					cascade = append(cascade, models.CreateFieldOption{ID: parent.ID + ":" + children[name], Name: parent.Name + " › " + name})
				}
			}
			choices[field.ID] = cascade
		case models.CustomFieldUser, models.CustomFieldMultiUser:
			for _, member := range members {
				choices[field.ID] = append(choices[field.ID], models.CreateFieldOption{ID: member.ID, Name: member.DisplayName})
			}
		case models.CustomFieldTeam:
			teams, err := h.Store.AtlassianTeams(ctx, wsID)
			if err != nil {
				return nil, err
			}
			for _, team := range teams {
				choices[field.ID] = append(choices[field.ID], models.CreateFieldOption{ID: team.ID, Name: team.Name})
			}
		case models.CustomFieldGroup, models.CustomFieldMultiGroup:
			if groups == nil {
				siteGroups, err := h.Store.SiteGroups(ctx, wsID)
				if err != nil {
					return nil, err
				}
				groups = []models.CreateFieldOption{}
				for _, group := range siteGroups {
					groups = append(groups, models.CreateFieldOption{ID: group.ID, Name: group.Name})
				}
			}
			choices[field.ID] = groups
		case models.CustomFieldProject:
			if projects == nil {
				all, err := h.Store.ProjectsByWorkspace(ctx, wsID)
				if err != nil {
					return nil, err
				}
				projects = []models.CreateFieldOption{}
				for _, project := range all {
					projects = append(projects, models.CreateFieldOption{ID: project.ID, Name: project.Name + " (" + project.Key + ")"})
				}
			}
			choices[field.ID] = projects
		case models.CustomFieldVersion, models.CustomFieldMultiVersion:
			if versions == nil {
				all, err := h.Store.ProjectVersions(ctx, issue.ProjectID)
				if err != nil {
					return nil, err
				}
				versions = []models.CreateFieldOption{}
				for _, version := range all {
					if !version.Archived {
						versions = append(versions, models.CreateFieldOption{ID: version.ID, Name: version.Name})
					}
				}
			}
			choices[field.ID] = versions
		}
	}
	return choices, nil
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
	// Opening a work item is what puts it in your history, which the issue
	// picker offers and lastViewed and issueHistory() search. The REST read
	// asks for this with updateHistory=true; a person reading the page is
	// saying the same thing by being here.
	if err := h.Store.RecordIssueView(r.Context(), wsID, user.ID, view.Issue.ID); err != nil {
		log.Printf("record issue view %s: %s", strconv.Quote(view.Issue.Key), strconv.Quote(err.Error()))
	}
	if isHX(r) {
		writeFragment(w, "issue_view", view)
		return
	}
	h.writeWorkspacePage(w, r, "page_issue", user, wsID, view, "issues", view.Issue.ProjectID)
}

// ---- auth ----

func (h *Handler) LoginForm(w http.ResponseWriter, r *http.Request) {
	noStoreAuthResponse(w)
	if h.currentUser(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	providers := h.loginProviders()
	if len(providers) == 1 && providers[0].Key == "shauth" {
		http.Redirect(w, r, "/auth/shauth", http.StatusSeeOther)
		return
	}
	notice := ""
	if r.URL.Query().Get("saved") == "password" {
		notice = "Your password is set. Sign in with it."
	}
	writePage(w, "page_login", loginPageData{Notice: notice, Providers: providers, Password: !authn.LocalCredentialsRefused(r.Context())})
}

func (h *Handler) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	noStoreAuthResponse(w)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	signIn, err := authn.Login(r.Context(), h.Store, r.PostFormValue("email"), r.PostFormValue("password"))
	if errors.Is(err, authn.ErrSSORequired) {
		// An authentication policy admits this person only through the
		// identity provider, which is something they can act on: the page
		// says so rather than reading as a wrong password.
		writePageStatus(w, "page_login", loginPageData{
			Error:     "Your organization signs this account in through its identity provider.",
			Providers: h.loginProviders(), Password: !authn.LocalCredentialsRefused(r.Context()),
		}, http.StatusForbidden)
		return
	}
	if err != nil {
		writePageStatus(w, "page_login", loginPageData{Error: "Incorrect email or password.", Providers: h.loginProviders(), Password: !authn.LocalCredentialsRefused(r.Context())}, http.StatusUnauthorized)
		return
	}
	if signIn.Challenge != "" {
		// The password is half the answer: this account verifies in two
		// steps, and the rest is a code.
		h.startSignInChallenge(w, r, signIn.Challenge)
		return
	}
	authn.SetSessionCookieFor(w, signIn.Session, signIn.TTL)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	noStoreAuthResponse(w)
	var idToken string
	var sessionProvider *OIDC
	if c, err := r.Cookie(sessionCookieName()); err == nil {
		if h.IdentityProviders != nil || h.OIDC != nil {
			var issuer string
			idToken, issuer, err = h.Store.IdentityProviderSession(r.Context(), authn.SessionHash(c.Value))
			if err != nil {
				log.Printf("OIDC session token: %v", err)
			}
			if h.IdentityProviders != nil {
				sessionProvider = h.IdentityProviders.ProviderByIssuer(issuer)
			} else if h.OIDC != nil && (issuer == "" || h.OIDC.issuer == "" || h.OIDC.issuer == issuer) {
				sessionProvider = h.OIDC
			}
		}
		if err := h.Store.DeleteSession(r.Context(), authn.SessionHash(c.Value)); err != nil {
			log.Printf("delete session: %v", err)
		}
	}
	authn.ClearSessionCookie(w)
	if idToken != "" && sessionProvider != nil && sessionProvider.endSessionEndpoint != "" {
		logoutURL, err := url.Parse(sessionProvider.endSessionEndpoint)
		if err != nil {
			log.Printf("OIDC end-session URL: %v", err)
		} else {
			query := logoutURL.Query()
			query.Set("id_token_hint", idToken)
			query.Set("post_logout_redirect_uri", sessionProvider.postLogoutRedirectURL)
			logoutURL.RawQuery = query.Encode()
			target := logoutURL.String()
			if err := validOIDCEndpointURL(target); err != nil {
				log.Printf("OIDC end-session redirect: %v", err)
				http.Redirect(w, r, "/signed-out", http.StatusSeeOther)
				return
			}
			http.Redirect(w, r, target, http.StatusSeeOther) // #nosec G710 -- final URL is HTTPS/loopback validated above; cross-origin IdP logout is intentional.
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
type loginPageData struct {
	Error string
	// Notice is what just happened somewhere else that the person needs to
	// read here, such as a password they have set and can now sign in with.
	// It is chosen from a fixed set, never taken from the URL: the page is
	// public, and text carried in a link is text an attacker writes.
	Notice    string
	Providers []LoginProvider
	// Password says whether this installation still accepts the credentials
	// it issued itself. With ZZIRA_LOCAL_CREDENTIALS=off it does not, so the
	// page offers no password box: every answer to it would be a 401.
	Password bool
}

type signedOutData struct {
	Providers []LoginProvider
	// Password has the same meaning as on loginPageData: without it the
	// "Use password" link leads to a form that cannot sign anyone in.
	Password bool
}

func (h *Handler) loginProviders() []LoginProvider {
	if h.IdentityProviders != nil {
		return h.IdentityProviders.LoginProviders()
	}
	if h.OIDC != nil {
		return []LoginProvider{{Key: "shauth", DisplayName: "Shauth", Kind: "OpenID Connect", Issuer: h.OIDC.issuer}}
	}
	return nil
}

func (h *Handler) SignedOut(w http.ResponseWriter, r *http.Request) {
	noStoreAuthResponse(w)
	authn.ClearSessionCookie(w)
	writePage(w, "page_signed_out", signedOutData{Providers: h.loginProviders(), Password: !authn.LocalCredentialsRefused(r.Context())})
}

// OIDCLogoutComplete is the registered post-logout redirect bridge Shauth
// sends the browser to after RP-initiated logout finishes. It hands off to
// Shauth's own /oauth/logout/complete rather than finishing the flow here:
// zzira's own session cookie is already cleared by Logout before the browser
// ever left for Shauth, and Shauth still needs its turn at this same-site hop
// to clear its own first-party session cookie, which zzira's origin cannot
// touch. The target ignores every query parameter on the incoming request
// (there is a real one, next, but it is Shauth's own /oauth/logout/complete
// that reads it, never this bridge) so a crafted next or redirect_uri here
// cannot steer the browser anywhere but back to Shauth.
func (h *Handler) OIDCLogoutComplete(w http.ResponseWriter, r *http.Request) {
	noStoreAuthResponse(w)
	authn.ClearSessionCookie(w)
	provider, providerKey := h.identityProvider(r)
	if providerKey == "shauth" {
		if origin := provider.FormActionOrigin(); origin != "" {
			target := origin + "/oauth/logout/complete"
			if validOIDCEndpointURL(target) == nil {
				w.Header().Set("Location", target)
				w.WriteHeader(http.StatusSeeOther)
				return
			}
		}
	}
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
	h.writeWorkspacePage(w, r, "page_home", user, wsID, stats, "dashboard", "")
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
	values := firstFormValues(r)
	data, err := h.buildCreateDialogData(r.Context(), wsID, user.ID, values)
	if err != nil {
		http.Error(w, "create metadata unavailable", http.StatusInternalServerError)
		return
	}
	writeFragment(w, "create_dialog", data)
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
	values := firstFormValues(r)
	data, err := h.buildCreateDialogData(r.Context(), wsID, user.ID, values)
	if err != nil {
		http.Error(w, "create metadata unavailable", http.StatusInternalServerError)
		return
	}
	customFields, err := createFieldsFromForm(data.Selected.Fields, values)
	if err != nil {
		data.Error = err.Error()
		writeFragment(w, "create_dialog", data)
		return
	}
	// The time tracking field holds the original estimate as a duration.
	var originalEstimate *int64
	if estimate := strings.TrimSpace(values["timetracking"]); estimate != "" {
		configuration, configErr := h.Store.JiraSiteConfiguration(r.Context(), wsID)
		if configErr != nil {
			http.Error(w, "Could not load time tracking settings.", http.StatusInternalServerError)
			return
		}
		seconds, parseErr := models.ParseJiraDuration(estimate, configuration.TimeTracking)
		if parseErr != nil {
			data.Error = "Time tracking: " + parseErr.Error()
			writeFragment(w, "create_dialog", data)
			return
		}
		originalEstimate = &seconds
	}
	issue, _, err := h.Commands.CreateIssue(r.Context(), commands.CreateIssueInput{
		OriginalEstimate: originalEstimate,
		ActorID:          user.ID,
		WorkspaceID:      wsID,
		ProjectIDOrKey:   values["project"],
		Summary:          values["summary"],
		Description:      values["description"],
		IssueTypeID:      values["issuetype"],
		ParentIDOrKey:    values["parent"],
		PriorityID:       values["priority"],
		AssigneeID:       values["assignee"],
		SecurityLevelID:  values["security"],
		Labels:           strings.Split(values["labels"], ","),
		DueDate:          values["duedate"],
		Fields:           customFields,
	})
	if err != nil {
		data.Error = err.Error()
		writeFragment(w, "create_dialog", data)
		return
	}
	if values["createAnother"] == "true" {
		nextValues := map[string]string{"project": data.Selected.Project.Key, "issuetype": values["issuetype"], "createAnother": "true"}
		data, err = h.buildCreateDialogData(r.Context(), wsID, user.ID, nextValues)
		if err != nil {
			http.Error(w, "create metadata unavailable", http.StatusInternalServerError)
			return
		}
		data.CreatedKey = issue.Key
		writeFragment(w, "create_dialog", data)
		return
	}
	w.Header().Set("HX-Redirect", "/browse/"+issue.Key)
	w.WriteHeader(http.StatusNoContent)
}

func firstFormValues(r *http.Request) map[string]string {
	_ = r.ParseForm()
	values := make(map[string]string, len(r.Form))
	for key, entries := range r.Form {
		if len(entries) > 0 {
			values[key] = entries[0]
			// Fields with several values, such as versions and multi-select
			// custom fields, post one entry per chosen value.
			if key == "fixVersions" || key == "versions" || (strings.HasPrefix(key, "customfield_") && len(entries) > 1) {
				values[key] = strings.Join(entries, ",")
			}
		}
	}
	return values
}

func (h *Handler) buildCreateDialogData(ctx context.Context, workspaceID, userID string, requested map[string]string) (createDialogData, error) {
	meta, err := h.Store.IssueCreateMetadata(ctx, workspaceID, userID)
	if err != nil {
		return createDialogData{}, err
	}
	if len(meta.Projects) == 0 {
		return createDialogData{}, fmt.Errorf("no project is available")
	}
	selected := meta.Projects[0]
	for _, project := range meta.Projects {
		if project.Project.ID == requested["project"] || strings.EqualFold(project.Project.Key, requested["project"]) {
			selected = project
			break
		}
	}
	values := map[string]string{"project": selected.Project.Key}
	for _, key := range []string{"summary", "description", "createAnother"} {
		values[key] = requested[key]
	}
	for _, field := range selected.Fields {
		if field.Section == "details" {
			values[field.ID] = requested[field.ID]
		}
	}
	if requestedType := requested["issuetype"]; requestedType != "" {
		for _, issueType := range selected.IssueTypes {
			if issueType.ID == requestedType {
				values["issuetype"] = issueType.ID
				break
			}
		}
	}
	if values["issuetype"] == "" && len(selected.IssueTypes) > 0 {
		values["issuetype"] = selected.IssueTypes[0].ID
	}
	if values["issuetype"] == "" {
		return createDialogData{}, fmt.Errorf("no issue type is available")
	}
	selectedSubtask := false
	for _, issueType := range selected.IssueTypes {
		if issueType.ID == values["issuetype"] {
			selectedSubtask = issueType.Subtask
			break
		}
	}
	// The project's create screen decides which fields this work type shows.
	selected.Fields = selected.FieldsForIssueType(values["issuetype"])
	// A custom field context can supply a default, which pre-fills the form
	// unless the request already carries a value for that field.
	for _, field := range selected.Fields {
		if field.Default == "" || values[field.ID] != "" {
			continue
		}
		var text string
		if err := json.Unmarshal([]byte(field.Default), &text); err == nil {
			values[field.ID] = text
			continue
		}
		values[field.ID] = strings.Trim(field.Default, `"`)
	}
	// A work item takes the default priority of the project's priority scheme
	// unless the form says otherwise.
	if values["priority"] == "" {
		values["priority"] = selected.DefaultPriorityID
	}
	// Parent offers the work items one level above the chosen work type, and
	// is left out when that level holds none.
	parents := selected.ParentOptionsForIssueType(values["issuetype"])
	visibleFields := make([]models.CreateFieldMeta, 0, len(selected.Fields))
	for _, field := range selected.Fields {
		if field.ID != "parent" {
			visibleFields = append(visibleFields, field)
			continue
		}
		if len(parents) == 0 && !selectedSubtask {
			continue
		}
		field.Options = parents
		visibleFields = append(visibleFields, field)
	}
	selected.Fields = visibleFields
	return createDialogData{
		Metadata: meta, Selected: selected, SelectedIssueTypeSubtask: selectedSubtask, Values: values,
		DetailTabs: selected.DetailTabsForIssueType(values["issuetype"], selected.Fields),
	}, nil
}

func createFieldsFromForm(fields []models.CreateFieldMeta, values map[string]string) (map[string]json.RawMessage, error) {
	custom := map[string]json.RawMessage{}
	for _, field := range fields {
		if (field.Type == "versions" || field.Type == "components") && values[field.ID] != "" {
			refs := []map[string]string{}
			for _, id := range strings.Split(values[field.ID], ",") {
				refs = append(refs, map[string]string{"id": id})
			}
			raw, err := json.Marshal(refs)
			if err != nil {
				return nil, err
			}
			custom[field.ID] = raw
			continue
		}
		if !field.Custom || values[field.ID] == "" {
			continue
		}
		encoded, err := encodeWebCustomField(field.Type, values[field.ID])
		if err != nil {
			return nil, fmt.Errorf("%s %w", field.Name, err)
		}
		custom[field.ID] = encoded
	}
	if len(custom) == 0 {
		return nil, nil
	}
	return custom, nil
}

func encodeWebCustomField(fieldType, value string) (json.RawMessage, error) {
	switch fieldType {
	case models.CustomFieldNumber:
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return nil, fmt.Errorf("must be a number")
		}
		encoded, err := json.Marshal(json.Number(value))
		if err != nil {
			return nil, fmt.Errorf("must be a finite number")
		}
		return encoded, nil
	case "string", models.CustomFieldText, models.CustomFieldDatetime,
		// A select field posts the chosen option's ID, which the command path
		// checks against the options the governing context offers.
		"option", models.CustomFieldSelect:
		encoded, err := json.Marshal(value)
		return encoded, err
	case models.CustomFieldDate, models.CustomFieldURL, "user", models.CustomFieldUser, "group", models.CustomFieldGroup, models.CustomFieldProject, "projectpicker", models.CustomFieldVersion, models.CustomFieldAsset:
		return json.Marshal(value)
	case models.CustomFieldCascadingSelect, "option-with-child":
		parent, child, _ := strings.Cut(value, ":")
		cascade := map[string]string{"parent": strings.TrimSpace(parent)}
		if child = strings.TrimSpace(child); child != "" {
			cascade["child"] = child
		}
		return json.Marshal(cascade)
	case "array", models.CustomFieldLabels:
		labels := append([]string{}, strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' })...)
		return json.Marshal(labels)
	case "users", models.CustomFieldMultiUser, "groups", models.CustomFieldMultiGroup, "options", models.CustomFieldMultiSelect, models.CustomFieldMultiVersion:
		ids := []string{}
		for _, id := range strings.Split(value, ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
		return json.Marshal(ids)
	default:
		return nil, fmt.Errorf("has unsupported type %q", fieldType)
	}
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
	boards, err := h.Store.BoardsByWorkspace(r.Context(), wsID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	statuses, err := h.Store.StatusesForProject(r.Context(), wsID, project.ID, true)
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
			if filter.ID == activeFilter || strconv.FormatInt(filter.JiraID, 10) == activeFilter {
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
	data.CanBulk, err = h.Store.HasGlobalPermission(r.Context(), wsID, user.ID, "BULK_CHANGE")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if data.CanBulk {
		data.Projects, err = h.Store.ProjectsByWorkspace(r.Context(), wsID)
		if err == nil {
			data.IssueTypes, err = h.Store.IssueTypes(r.Context(), wsID)
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	for _, board := range boards {
		if board.ProjectID == project.ID {
			data.BoardID = board.ID
			break
		}
	}
	data.Chips = navigatorChips(project.Key, params, members)
	if params.Mode == "basic" {
		data.SaveJQL = basicAsJQL(params)
	} else {
		data.SaveJQL = params.JQL
	}
	compiled, err := compileNavigatorSearch(r.Context(), h.Store, wsID, project.Key, user.ID, params)
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
	if data.CanBulk && len(data.Issues) > 0 {
		data.BulkTransitions, err = h.commonBulkTransitions(r.Context(), wsID, user.ID, data.Issues)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if data.BulkPriorities, err = h.Store.PrioritiesForProject(r.Context(), wsID, project.ID); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if data.BulkComponents, err = h.Store.Components(r.Context(), wsID, project.ID, "", "name"); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if data.BulkVersions, err = h.Store.ProjectVersions(r.Context(), project.ID); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	h.writeWorkspacePage(w, r, "page_project", user, wsID, data, "issues", project.ID)
}

func (h *Handler) commonBulkTransitions(ctx context.Context, workspaceID, userID string, issues []*models.Issue) ([]models.WorkflowTransition, error) {
	common := map[string]models.WorkflowTransition{}
	for index, issue := range issues {
		wf, err := h.Store.WorkflowForProjectAndIssueType(ctx, issue.ProjectID, issue.IssueType.ID)
		if err != nil {
			return nil, err
		}
		evaluation, err := h.Store.IssueWorkflowEvaluation(ctx, workspaceID, userID, issue)
		if err != nil {
			return nil, err
		}
		available := map[string]models.WorkflowTransition{}
		for _, transition := range wf.AvailableFor(issue.Status.ID, evaluation) {
			if len(transition.ScreenFields()) == 0 {
				available[transition.ID] = models.WorkflowTransition{ID: transition.ID, Name: transition.Name}
			}
		}
		if index == 0 {
			common = available
		} else {
			for id := range common {
				if _, ok := available[id]; !ok {
					delete(common, id)
				}
			}
		}
	}
	result := make([]models.WorkflowTransition, 0, len(common))
	for _, transition := range common {
		result = append(result, transition)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name || result[i].Name == result[j].Name && result[i].ID < result[j].ID
	})
	return result, nil
}

// SubmitBulkIssueDelete starts the same durable bulk-delete task used by Jira's
// REST endpoint from the issue navigator.
func (h *Handler) SubmitBulkIssueDelete(w http.ResponseWriter, r *http.Request, projectKey string) {
	if !parseForm(w, r) {
		return
	}
	user, workspaceID, ok := h.requireBulkChange(w, r)
	if !ok {
		return
	}
	project, err := h.Store.ProjectByKey(r.Context(), workspaceID, projectKey)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	issues, ok := h.bulkSelection(w, r, user, workspaceID, project.ID)
	if !ok {
		return
	}
	task, err := h.Store.EnqueueBulkDeleteTask(r.Context(), workspaceID, user.ID, bulkTaskItems(issues), r.FormValue("sendNotification") == "true")
	if errors.Is(err, store.ErrBulkTaskLimit) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/issues/"+url.PathEscape(project.Key)+"/bulk/"+url.PathEscape(task.WireID()), http.StatusSeeOther)
}

func (h *Handler) SubmitBulkIssueMove(w http.ResponseWriter, r *http.Request, projectKey string) {
	if !parseForm(w, r) {
		return
	}
	user, workspaceID, ok := h.requireBulkChange(w, r)
	if !ok {
		return
	}
	sourceProject, err := h.Store.ProjectByKey(r.Context(), workspaceID, projectKey)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	destination, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, r.FormValue("project"))
	if err != nil {
		http.Error(w, "invalid destination project", http.StatusBadRequest)
		return
	}
	issueType, err := h.Store.IssueTypeByIDOrName(r.Context(), workspaceID, r.FormValue("issueType"))
	if err != nil {
		http.Error(w, "invalid destination work type", http.StatusBadRequest)
		return
	}
	parentID := ""
	if parentValue := strings.TrimSpace(r.FormValue("parent")); parentValue != "" {
		parent, parentErr := h.issueForUser(r, user, workspaceID, parentValue)
		if parentErr != nil || parent.ProjectID != destination.ID || parent.IssueType.Subtask {
			http.Error(w, "invalid destination parent", http.StatusBadRequest)
			return
		}
		parentID = parent.ID
	}
	if issueType.Subtask != (parentID != "") {
		http.Error(w, "a destination parent is required only for sub-task work types", http.StatusBadRequest)
		return
	}
	selection, ok := h.bulkSelection(w, r, user, workspaceID, sourceProject.ID)
	if !ok {
		return
	}
	items := make([]store.BulkIssueMoveTaskItem, 0, len(selection))
	for _, issue := range selection {
		items = append(items, store.BulkIssueMoveTaskItem{
			BulkIssueTaskItem: store.BulkIssueTaskItem{ID: issue.ID, JiraID: issue.JiraID},
			ProjectID:         destination.ID, IssueTypeID: issueType.ID, ParentID: parentID, InferStatusDefaults: true,
		})
	}
	task, err := h.Store.EnqueueBulkMoveTask(r.Context(), workspaceID, user.ID, items, r.FormValue("sendNotification") == "true")
	if errors.Is(err, store.ErrBulkTaskLimit) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/issues/"+url.PathEscape(sourceProject.Key)+"/bulk/"+url.PathEscape(task.WireID()), http.StatusSeeOther)
}

func (h *Handler) SubmitBulkIssueTransition(w http.ResponseWriter, r *http.Request, projectKey string) {
	if !parseForm(w, r) {
		return
	}
	user, workspaceID, ok := h.requireBulkChange(w, r)
	if !ok {
		return
	}
	project, err := h.Store.ProjectByKey(r.Context(), workspaceID, projectKey)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	transitionID := strings.TrimSpace(r.FormValue("transition"))
	if transitionID == "" {
		http.Error(w, "choose a transition", http.StatusBadRequest)
		return
	}
	issues, ok := h.bulkSelection(w, r, user, workspaceID, project.ID)
	if !ok {
		return
	}
	common, err := h.commonBulkTransitions(r.Context(), workspaceID, user.ID, issues)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	valid := false
	for _, transition := range common {
		if transition.ID == transitionID {
			valid = true
			break
		}
	}
	if !valid {
		http.Error(w, "transition is not common to the selected work items", http.StatusBadRequest)
		return
	}
	items := make([]store.BulkIssueTransitionTaskItem, 0, len(issues))
	for _, issue := range issues {
		items = append(items, store.BulkIssueTransitionTaskItem{BulkIssueTaskItem: store.BulkIssueTaskItem{ID: issue.ID, JiraID: issue.JiraID}, TransitionID: transitionID})
	}
	task, err := h.Store.EnqueueBulkTransitionTask(r.Context(), workspaceID, user.ID, items, r.FormValue("sendNotification") == "true")
	if errors.Is(err, store.ErrBulkTaskLimit) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/issues/"+url.PathEscape(project.Key)+"/bulk/"+url.PathEscape(task.WireID()), http.StatusSeeOther)
}

// bulkSelection resolves the work items a navigator form selected, holding
// them to Jira's bounds and to the reader's own access. Every bulk submission
// starts here, so one selection cannot behave differently from another. The
// caller has already reported anything else its own form is missing, so the
// bound reported here is the selection's alone.
func (h *Handler) bulkSelection(w http.ResponseWriter, r *http.Request, user *models.User, workspaceID, projectID string) ([]*models.Issue, bool) {
	selected := r.Form["issue"]
	if len(selected) < 1 || len(selected) > 1000 {
		http.Error(w, "select between 1 and 1,000 work items", http.StatusBadRequest)
		return nil, false
	}
	seen := make(map[string]bool, len(selected))
	issues := make([]*models.Issue, 0, len(selected))
	for _, idOrKey := range selected {
		issue, lookupErr := h.issueForUser(r, user, workspaceID, idOrKey)
		if lookupErr != nil || issue.ProjectID != projectID || seen[issue.ID] {
			http.Error(w, "the selection contains an invalid or inaccessible work item", http.StatusBadRequest)
			return nil, false
		}
		seen[issue.ID] = true
		issues = append(issues, issue)
	}
	return issues, true
}

// bulkTaskItems names a selection the way a durable bulk task records it.
func bulkTaskItems(issues []*models.Issue) []store.BulkIssueTaskItem {
	items := make([]store.BulkIssueTaskItem, 0, len(issues))
	for _, issue := range issues {
		items = append(items, store.BulkIssueTaskItem{ID: issue.ID, JiraID: issue.JiraID})
	}
	return items
}

// SubmitBulkIssueEdit sets one field across the selection, through the same
// durable task Jira's bulk edit endpoint queues. The navigator offers the
// fields a reader can set on every work item without a screen: the assignee,
// the priority, the due date and labels.
func (h *Handler) SubmitBulkIssueEdit(w http.ResponseWriter, r *http.Request, projectKey string) {
	if !parseForm(w, r) {
		return
	}
	user, workspaceID, ok := h.requireBulkChange(w, r)
	if !ok {
		return
	}
	project, err := h.Store.ProjectByKey(r.Context(), workspaceID, projectKey)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	issues, ok := h.bulkSelection(w, r, user, workspaceID, project.ID)
	if !ok {
		return
	}
	operations, err := bulkEditOperations(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	task, err := h.Store.EnqueueBulkEditTask(r.Context(), workspaceID, user.ID, store.BulkIssueEditTaskPayload{
		Issues: bulkTaskItems(issues), Operations: operations,
		SendBulkNotification: r.FormValue("sendNotification") == "true",
	})
	if errors.Is(err, store.ErrBulkTaskLimit) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/issues/"+url.PathEscape(project.Key)+"/bulk/"+url.PathEscape(task.WireID()), http.StatusSeeOther)
}

// bulkEditOperations reads every field the navigator's editor was asked to
// change, in the shapes the bulk edit command reads: a user or a priority by
// id, labels, components and versions as lists. A field is changed when its
// box is ticked, so the editor sets several at once the way Jira's does, and
// an empty box clears the field -- except a priority, which Jira has no unset
// value for.
func bulkEditOperations(r *http.Request) ([]store.BulkIssueEditOperation, error) {
	encode := func(v any) json.RawMessage {
		encoded, _ := json.Marshal(v)
		return encoded
	}
	list := func(name string) []string {
		values := []string{}
		for _, value := range r.Form[name] {
			for _, part := range strings.Split(value, ",") {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					values = append(values, trimmed)
				}
			}
		}
		return values
	}
	operations := []store.BulkIssueEditOperation{}
	for _, field := range r.Form["field"] {
		value := strings.TrimSpace(r.FormValue(map[string]string{
			"assignee": "valueAssignee", "priority": "valuePriority",
			"duedate": "valueDueDate", "summary": "valueSummary",
		}[field]))
		switch field {
		case "assignee":
			// The command sets the assignee from an account id, and an empty
			// one unassigns, as the single-item edit does.
			operations = append(operations, store.BulkIssueEditOperation{FieldID: "assignee", Action: "SET", Value: encode(value)})
		case "priority":
			if value == "" {
				return nil, fmt.Errorf("choose a priority")
			}
			operations = append(operations, store.BulkIssueEditOperation{FieldID: "priority", Action: "SET", Value: encode(value)})
		case "duedate":
			if value != "" {
				if _, err := time.Parse("2006-01-02", value); err != nil {
					return nil, fmt.Errorf("a due date is a date, as YYYY-MM-DD")
				}
			}
			operations = append(operations, store.BulkIssueEditOperation{FieldID: "duedate", Action: "SET", Value: encode(value)})
		case "labels":
			labels := list("valueLabels")
			if len(labels) == 0 {
				return nil, fmt.Errorf("name at least one label")
			}
			action := "ADD"
			if r.FormValue("labelAction") == "remove" {
				action = "REMOVE"
			}
			operations = append(operations, store.BulkIssueEditOperation{FieldID: "labels", Action: action, Value: encode(labels)})
		case "components":
			operations = append(operations, store.BulkIssueEditOperation{
				FieldID: "components", Action: bulkListAction(r.FormValue("componentAction")), Value: encode(list("valueComponents")),
			})
		case "fixVersions":
			operations = append(operations, store.BulkIssueEditOperation{
				FieldID: "fixVersions", Action: bulkListAction(r.FormValue("versionAction")), Value: encode(list("valueFixVersions")),
			})
		default:
			return nil, fmt.Errorf("choose a field to change")
		}
	}
	if len(operations) == 0 {
		return nil, fmt.Errorf("choose a field to change")
	}
	return operations, nil
}

// bulkListAction reads what to do with a list field: replace what is there,
// add to it, or take values out of it. An empty list with SET clears it.
func bulkListAction(action string) string {
	switch action {
	case "add":
		return "ADD"
	case "remove":
		return "REMOVE"
	}
	return "SET"
}

// SubmitBulkIssueWatch starts watching, or stops watching, every work item in
// the selection as the reader themselves.
func (h *Handler) SubmitBulkIssueWatch(w http.ResponseWriter, r *http.Request, projectKey string, watch bool) {
	if !parseForm(w, r) {
		return
	}
	// Watching is not a change to the work item, so it asks for no more than
	// being able to see it -- as the single-item watch does.
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	project, err := h.Store.ProjectByKey(r.Context(), workspaceID, projectKey)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	issues, ok := h.bulkSelection(w, r, user, workspaceID, project.ID)
	if !ok {
		return
	}
	task, err := h.Store.EnqueueBulkWatchTask(r.Context(), workspaceID, user.ID, bulkTaskItems(issues), watch)
	if errors.Is(err, store.ErrBulkTaskLimit) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/issues/"+url.PathEscape(project.Key)+"/bulk/"+url.PathEscape(task.WireID()), http.StatusSeeOther)
}

func (h *Handler) BulkIssueTask(w http.ResponseWriter, r *http.Request, projectKey, taskID string) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	project, err := h.Store.ProjectByKey(r.Context(), workspaceID, projectKey)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	task, err := h.Store.APITaskByID(r.Context(), workspaceID, taskID)
	if err != nil || !task.IsBulkIssueOperation() {
		http.NotFound(w, r)
		return
	}
	// A bulk task's progress is its submitter's, and administrators'.
	if task.SubmittedBy != user.ID {
		admin, adminErr := h.Store.IsAdmin(r.Context(), workspaceID, user.ID)
		if adminErr != nil || !admin {
			http.NotFound(w, r)
			return
		}
	}
	data := bulkIssueTaskData{Project: project, Task: task}
	if len(task.Result) > 0 && string(task.Result) != "null" {
		var result struct {
			Processed    []int64             `json:"processedAccessibleIssues"`
			Failed       map[string][]string `json:"failedAccessibleIssues"`
			Inaccessible int                 `json:"invalidOrInaccessibleIssueCount"`
			Total        int                 `json:"totalIssueCount"`
		}
		if err := json.Unmarshal(task.Result, &result); err == nil {
			data.Processed = len(result.Processed)
			data.Failed = len(result.Failed)
			data.Inaccessible = result.Inaccessible
			data.Total = result.Total
		}
	}
	h.writeWorkspacePage(w, r, "page_bulk_issue_task", user, workspaceID, data, "issues", project.ID)
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
	if _, err := compileNavigatorSearch(r.Context(), h.Store, wsID, project.Key, user.ID, navigatorParams{Mode: "advanced", JQL: filterJQL, Sort: "updated", Direction: "desc"}); err != nil {
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
	update := store.IssueUpdate{}
	for name, values := range r.PostForm {
		if !strings.HasPrefix(name, "field_") || len(values) == 0 {
			continue
		}
		field, value := strings.TrimPrefix(name, "field_"), values[0]
		switch field {
		case "summary":
			update.Summary = &value
		case "description":
			update.Description = adf.ParagraphDoc(value)
		case "labels":
			labels := strings.Split(value, ",")
			if strings.TrimSpace(value) == "" {
				labels = []string{}
			}
			update.Labels = &labels
		case "assignee":
			update.AssigneeID = &value
		case "priority":
			update.PriorityID = &value
		case "resolution":
			update.ResolutionID = &value
		default:
			if strings.HasPrefix(field, "customfield_") {
				if update.Fields == nil {
					update.Fields = make(map[string]json.RawMessage)
				}
				encoded, _ := json.Marshal(value)
				update.Fields[field] = encoded
			}
		}
	}
	if _, _, err := h.Commands.TransitionIssueWithUpdate(r.Context(), user.ID, wsID, key, r.PostFormValue("transition"), update); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
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
		http.Error(w, err.Error(), commandErrorStatus(err))
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
	configuration, err := h.Store.JiraSiteConfiguration(r.Context(), wsID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Each of up to 60 files may reach the site's attachment limit.
	r.Body = http.MaxBytesReader(w, r.Body, min(60*configuration.AttachmentUploadLimit+(1<<20), 2<<30))
	if err := r.ParseMultipartForm(32 << 20); err != nil { // #nosec G120 -- body capped by MaxBytesReader above
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "the attachment exceeds the maximum attachment size", http.StatusRequestEntityTooLarge)
			return
		}
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
			if errors.Is(err, commands.ErrAttachmentTooLarge) {
				http.Error(w, "the attachment exceeds the maximum attachment size", http.StatusRequestEntityTooLarge)
				return
			}
			if errors.Is(err, commands.ErrAttachmentCreatePermission) {
				http.Error(w, "you do not have permission to attach files to this work item", http.StatusForbidden)
				return
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
	configuration, err := h.Store.JiraSiteConfiguration(r.Context(), wsID)
	if err != nil {
		http.Error(w, "Could not load time tracking settings.", http.StatusInternalServerError)
		return
	}
	spent, err := models.ParseJiraDuration(r.PostFormValue("timeSpent"), configuration.TimeTracking)
	if err != nil || spent <= 0 {
		http.Error(w, "Enter the time spent, such as 1h 30m.", http.StatusBadRequest)
		return
	}
	// The remaining estimate moves by the time logged unless the person sets
	// it or leaves it as it is.
	estimate := store.WorklogEstimate{Mode: r.PostFormValue("adjustEstimate"), Notify: true}
	if estimate.Mode == "new" {
		if estimate.NewSeconds, err = models.ParseJiraDuration(r.PostFormValue("newEstimate"), configuration.TimeTracking); err != nil {
			http.Error(w, "Enter the new remaining estimate, such as 2h.", http.StatusBadRequest)
			return
		}
	}
	if _, _, err := h.Commands.AddWorklogWithEstimate(r.Context(), user.ID, wsID, key, adf.ParagraphDoc(r.PostFormValue("comment")), int(spent), estimate); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	h.serveIssue(w, r, user, wsID, key)
}

// EditTimeTracking sets a work item's original and remaining estimates from
// the issue page; an empty field removes that estimate.
func (h *Handler) EditTimeTracking(w http.ResponseWriter, r *http.Request, key string) {
	user, wsID, ok := h.issueMutationContext(w, r, key)
	if !ok || !parseForm(w, r) {
		return
	}
	configuration, err := h.Store.JiraSiteConfiguration(r.Context(), wsID)
	if err != nil {
		http.Error(w, "Could not load time tracking settings.", http.StatusInternalServerError)
		return
	}
	estimates := map[string]*int64{}
	for _, name := range []string{"originalEstimate", "remainingEstimate"} {
		value := strings.TrimSpace(r.PostFormValue(name))
		seconds := store.ClearEstimate
		if value != "" {
			if seconds, err = models.ParseJiraDuration(value, configuration.TimeTracking); err != nil {
				http.Error(w, err.Error(), commandErrorStatus(err))
				return
			}
		}
		estimates[name] = &seconds
	}
	if _, _, err := h.Commands.UpdateIssue(r.Context(), commands.UpdateIssueInput{ActorID: user.ID, WorkspaceID: wsID, IssueIDOrKey: key,
		OriginalEstimate: estimates["originalEstimate"], RemainingEstimate: estimates["remainingEstimate"]}); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
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
		http.Error(w, err.Error(), commandErrorStatus(err))
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
		http.Error(w, err.Error(), commandErrorStatus(err))
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
	case "parent":
		in.ParentIDOrKey = &value
	case "resolution":
		in.ResolutionID = &value
	case "security":
		in.SecurityLevelID = &value
	case "labels":
		labels := strings.Split(value, ",")
		if strings.TrimSpace(value) == "" {
			labels = []string{}
		}
		in.Labels = &labels
	case "duedate":
		in.DueDate = &value
	default:
		const prefix = "custom:"
		if !strings.HasPrefix(field, prefix) || len(field) == len(prefix) {
			http.Error(w, "unknown issue field", http.StatusBadRequest)
			return
		}
		fieldID := strings.TrimPrefix(field, prefix)
		encoded := json.RawMessage(strconv.Quote(value))
		if definition, err := h.Store.CustomFieldByID(r.Context(), wsID, fieldID); err == nil {
			// A multiple choice posts one value per chosen entry; a blank value
			// clears the field.
			joined := strings.Join(r.PostForm["value"], ",")
			if strings.TrimSpace(joined) == "" {
				encoded = json.RawMessage("null")
			} else if encoded, err = encodeWebCustomField(definition.Type, joined); err != nil {
				http.Error(w, err.Error(), commandErrorStatus(err))
				return
			}
		}
		in.Fields = map[string]json.RawMessage{fieldID: encoded}
	}
	if _, _, err := h.Commands.UpdateIssue(r.Context(), in); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
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
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	h.serveIssue(w, r, user, wsID, key)
}

func (h *Handler) SetVoting(w http.ResponseWriter, r *http.Request, key string) {
	user, wsID, ok := h.issueMutationContext(w, r, key)
	if !ok || !parseForm(w, r) {
		return
	}
	if _, err := h.Commands.SetVoting(r.Context(), user.ID, wsID, key, r.PostFormValue("voting") == "true"); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
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
		http.Error(w, err.Error(), commandErrorStatus(err))
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
		http.Error(w, err.Error(), commandErrorStatus(err))
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
	view, err := h.buildEditDialogView(r.Context(), wsID, user.ID, issue)
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
	issue, err := h.issueForUser(r, user, wsID, key)
	if err != nil {
		http.NotFound(w, r)
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
	customFields, cfErr := h.Store.CustomFieldsForProject(r.Context(), issue.ProjectID)
	if cfErr != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	for _, cf := range customFields {
		if v, ok := r.PostForm[cf.ID]; ok {
			if v[0] == "" {
				if _, existed := issue.Fields[cf.ID]; !existed {
					continue
				}
				if in.Fields == nil {
					in.Fields = map[string]json.RawMessage{}
				}
				in.Fields[cf.ID] = json.RawMessage("null")
				continue
			}
			if in.Fields == nil {
				in.Fields = map[string]json.RawMessage{}
			}
			encoded, err := encodeWebCustomField(cf.Type, strings.Join(v, ","))
			if err != nil {
				http.Error(w, cf.Name+" "+err.Error(), http.StatusBadRequest)
				return
			}
			in.Fields[cf.ID] = encoded
		}
	}
	if _, _, err := h.Commands.UpdateIssue(r.Context(), in); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
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
		if errors.Is(err, commands.ErrPermission) {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
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

// commandErrorStatus is the status for a refused command: 403 when a project
// permission is missing, 400 otherwise.
func commandErrorStatus(err error) int {
	if errors.Is(err, commands.ErrPermission) {
		return http.StatusForbidden
	}
	return http.StatusBadRequest
}

// requireBulkChange admits people holding Jira's Bulk change global
// permission, which every bulk operation needs.
func (h *Handler) requireBulkChange(w http.ResponseWriter, r *http.Request) (*models.User, string, bool) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return nil, "", false
	}
	allowed, err := h.Store.HasGlobalPermission(r.Context(), workspaceID, user.ID, "BULK_CHANGE")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, "", false
	}
	if !allowed {
		http.Error(w, "You do not have the Bulk change permission.", http.StatusForbidden)
		return nil, "", false
	}
	return user, workspaceID, true
}
