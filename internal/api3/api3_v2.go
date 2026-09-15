package api3

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

// ---- projects ----

func (h *Handler) projectBean(p *models.Project) map[string]any {
	return map[string]any{
		"id":             p.ID,
		"key":            p.Key,
		"name":           p.Name,
		"description":    p.Description,
		"url":            p.URL,
		"assigneeType":   p.AssigneeType,
		"self":           h.BaseURL + "/rest/api/3/project/" + p.Key,
		"projectTypeKey": p.ProjectTypeKey,
		"simplified":     true,
		"style":          "classic",
		"avatarUrls": map[string]string{
			"48x48": h.BaseURL + "/static/img/avatar-default.svg",
		},
	}
}

func (h *Handler) listProjects(w http.ResponseWriter, r *http.Request) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	projects, err := h.Store.ProjectsWithPermissions(r.Context(), wsID, userID, []string{"BROWSE_PROJECTS"})
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if raw := r.URL.Query().Get("recent"); raw != "" {
		// recent returns the caller's most recently viewed projects, up to 20.
		limit, parseErr := strconv.Atoi(raw)
		if parseErr != nil || limit < 0 {
			jiraError(w, http.StatusBadRequest, "recent must be a non-negative integer.")
			return
		}
		recent, recentErr := h.Store.RecentProjects(r.Context(), wsID, userID)
		if recentErr != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		browsable := map[string]bool{}
		for _, p := range projects {
			browsable[p.ID] = true
		}
		projects = projects[:0]
		for _, p := range recent {
			if browsable[p.ID] && len(projects) < min(limit, 20) {
				projects = append(projects, p)
			}
		}
	}
	view, err := h.newProjectView(r, wsID, false)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := make([]map[string]any, 0, len(projects))
	for _, p := range projects {
		bean, beanErr := h.projectRepresentation(r, wsID, userID, p, view)
		if beanErr != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		out = append(out, bean)
	}
	writeJSON(w, http.StatusOK, out)
}

// searchProjects pages the projects the caller may act on, as Jira's project
// search does: by action and status, with filters, property queries,
// ordering and expansions.
func (h *Handler) searchProjects(w http.ResponseWriter, r *http.Request) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	start, limit, e := metadataPage(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	permissions := map[string][]string{
		"view": {"BROWSE_PROJECTS", "ADMINISTER_PROJECTS"}, "browse": {"BROWSE_PROJECTS"},
		"edit": {"ADMINISTER_PROJECTS"}, "create": {"CREATE_ISSUES"},
	}
	action := strings.ToLower(r.URL.Query().Get("action"))
	if action == "" {
		action = "view"
	}
	anyOf, known := permissions[action]
	if !known {
		jiraError(w, http.StatusBadRequest, "action must be view, browse, edit or create.")
		return
	}
	states := commaQuerySet(r, "status")
	if len(states) == 0 {
		states = map[string]struct{}{"live": {}}
	}
	lifecycle := map[string]string{"live": store.ProjectLifecycleActive, "archived": store.ProjectLifecycleArchived, "deleted": store.ProjectLifecycleTrashed}
	candidates := []*models.Project{}
	for state := range states {
		stored, ok := lifecycle[state]
		if !ok {
			jiraError(w, http.StatusBadRequest, "status must be live, archived or deleted.")
			return
		}
		projects, err := h.Store.ProjectsByLifecycle(r.Context(), wsID, stored)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		candidates = append(candidates, projects...)
	}
	propertyQuery, err := parseProjectPropertyQuery(r.URL.Query().Get("propertyQuery"))
	if err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	projects, e := filterProjects(r, candidates)
	if e != nil {
		writeJerr(w, e)
		return
	}
	permitted := projects[:0]
	for _, p := range projects {
		allowed := false
		for _, permission := range anyOf {
			granted, permissionErr := h.hasProjectPermission(r.Context(), wsID, userID, p.ID, "", permission)
			if permissionErr != nil {
				jiraError(w, http.StatusInternalServerError, "internal error")
				return
			}
			if granted {
				allowed = true
				break
			}
		}
		if allowed && propertyQuery != nil {
			properties, propertyErr := h.Store.ProjectProperties(r.Context(), wsID, p.ID)
			allowed = propertyErr == nil && propertyQuery.matches(properties)
		}
		if allowed {
			permitted = append(permitted, p)
		}
	}
	projects = permitted
	view, err := h.newProjectView(r, wsID, false)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err = h.orderProjects(r, wsID, projects, view.categories); err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	total := len(projects)
	end := min(start, total) + min(limit, total-min(start, total))
	values := make([]map[string]any, 0, end-min(start, total))
	for _, p := range projects[min(start, total):end] {
		bean, beanErr := h.projectRepresentation(r, wsID, userID, p, view)
		if beanErr != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		values = append(values, bean)
	}
	page := map[string]any{
		"self":       h.projectPageURL(r, start, limit),
		"isLast":     end >= total,
		"maxResults": limit,
		"startAt":    start,
		"total":      total,
		"values":     values,
	}
	if end < total && limit > 0 {
		page["nextPage"] = h.projectPageURL(r, end, limit)
	}
	writeJSON(w, http.StatusOK, page)
}

// orderProjects applies the orderings that need more than a project's key and
// name: category, lead, issue counts and lifecycle dates.
func (h *Handler) orderProjects(r *http.Request, workspaceID string, projects []*models.Project, categories map[string]*models.ProjectCategory) error {
	order := r.URL.Query().Get("orderBy")
	field := strings.TrimLeft(order, "+-")
	descending := strings.HasPrefix(order, "-")
	var sortKey func(*models.Project) (string, int64)
	switch field {
	case "category":
		sortKey = func(p *models.Project) (string, int64) {
			if category := categories[p.CategoryID]; category != nil {
				return strings.ToLower(category.Name), 0
			}
			return "", 0
		}
	case "owner":
		leads := map[string]string{}
		for _, p := range projects {
			if _, seen := leads[p.LeadAccountID]; seen || p.LeadAccountID == "" {
				continue
			}
			if lead, err := h.Store.UserByID(r.Context(), p.LeadAccountID); err == nil {
				leads[p.LeadAccountID] = strings.ToLower(lead.DisplayName)
			}
		}
		sortKey = func(p *models.Project) (string, int64) { return leads[p.LeadAccountID], 0 }
	case "issueCount", "lastIssueUpdatedDate":
		counts, updated := map[string]int64{}, map[string]int64{}
		for _, p := range projects {
			count, last, err := h.Store.ProjectInsight(r.Context(), workspaceID, p.ID)
			if err != nil {
				return err
			}
			counts[p.ID], updated[p.ID] = int64(count), last
		}
		sortKey = func(p *models.Project) (string, int64) {
			if field == "issueCount" {
				return "", counts[p.ID]
			}
			return "", updated[p.ID]
		}
	case "archivedDate":
		sortKey = func(p *models.Project) (string, int64) { return p.ArchivedAt, 0 }
	case "deletedDate":
		sortKey = func(p *models.Project) (string, int64) { return p.TrashedAt, 0 }
	default:
		return nil
	}
	sort.SliceStable(projects, func(i, j int) bool {
		leftText, leftNumber := sortKey(projects[i])
		rightText, rightNumber := sortKey(projects[j])
		if leftText == rightText && leftNumber == rightNumber {
			return projects[i].Key < projects[j].Key
		}
		less := leftText < rightText || leftText == rightText && leftNumber < rightNumber
		if descending {
			return !less
		}
		return less
	})
	return nil
}

func (h *Handler) getProject(w http.ResponseWriter, r *http.Request, keyOrID string) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	project, err := h.Store.ProjectByIDOrKey(r.Context(), wsID, keyOrID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			jiraError(w, http.StatusNotFound, "No project could be found with key or id "+keyOrID+".")
			return
		}
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	allowed, err := h.Store.HasProjectPermission(r.Context(), wsID, userID, project.ID, "", "BROWSE_PROJECTS")
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !allowed {
		jiraError(w, http.StatusNotFound, "No project could be found with key or id "+keyOrID+".")
		return
	}
	if userID != "" {
		if err = h.Store.RecordProjectView(r.Context(), wsID, userID, project.ID); err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	h.writeProject(w, r, userID, project)
}

// ---- createmeta ----

func (h *Handler) createMeta(w http.ResponseWriter, r *http.Request) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	meta, err := h.creatableMetadata(r, wsID, userID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	projectFilter := commaQuerySet(r, "projectIds", "projectKeys")
	typeFilter := commaQuerySet(r, "issuetypeIds", "issuetypeNames")
	includeFields := strings.Contains(r.URL.Query().Get("expand"), "fields")
	projectValues := make([]map[string]any, 0, len(meta.Projects))
	for _, project := range meta.Projects {
		if len(projectFilter) > 0 && !querySetContains(projectFilter, project.Project.ID, project.Project.Key) {
			continue
		}
		issueTypes := make([]map[string]any, 0, len(project.IssueTypes))
		for _, issueType := range project.IssueTypes {
			if len(typeFilter) > 0 && !querySetContains(typeFilter, jiraIDString(issueType.JiraID), issueType.ID, issueType.Name) {
				continue
			}
			bean := h.createMetaIssueTypeBean(issueType)
			if includeFields {
				typeFields := project.FieldsForIssueType(issueType.ID)
				fields := make(map[string]any, len(typeFields))
				for _, source := range typeFields {
					field := source
					if field.ID == "parent" {
						field.Required = issueType.Subtask
					}
					fields[field.ID] = h.legacyCreateFieldBean(field)
				}
				bean["fields"] = fields
			}
			issueTypes = append(issueTypes, bean)
		}
		projectBean := h.projectBean(&project.Project)
		projectBean["issuetypes"] = issueTypes
		projectValues = append(projectValues, map[string]any{
			"id": projectBean["id"], "key": projectBean["key"], "name": projectBean["name"],
			"self": projectBean["self"], "avatarUrls": projectBean["avatarUrls"], "issuetypes": issueTypes,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"expand":   "projects",
		"projects": projectValues,
	})
}

func (h *Handler) createMetaIssueTypes(w http.ResponseWriter, r *http.Request, projectIDOrKey string) {
	project, e := h.createMetaProject(r, projectIDOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	start, limit, e := metadataPage(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	safeStart := min(start, len(project.IssueTypes))
	end := min(safeStart+limit, len(project.IssueTypes))
	values := make([]map[string]any, 0, end-safeStart)
	for _, issueType := range project.IssueTypes[safeStart:end] {
		values = append(values, h.createMetaIssueTypeBean(issueType))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"startAt": start, "maxResults": limit, "total": len(project.IssueTypes), "issueTypes": values,
	})
}

func (h *Handler) createMetaFields(w http.ResponseWriter, r *http.Request, projectIDOrKey, issueTypeID string) {
	project, e := h.createMetaProject(r, projectIDOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	var selectedType *models.IssueType
	for _, issueType := range project.IssueTypes {
		if createMetaTypeMatches(issueType, issueTypeID) {
			selected := issueType
			selectedType = &selected
			break
		}
	}
	if selectedType == nil {
		jiraError(w, http.StatusBadRequest, "The issue type is not available in this project.")
		return
	}
	start, limit, e := metadataPage(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	typeFields := project.FieldsForIssueType(selectedType.ID)
	safeStart := min(start, len(typeFields))
	end := min(safeStart+limit, len(typeFields))
	values := make([]map[string]any, 0, end-safeStart)
	for _, source := range typeFields[safeStart:end] {
		field := source
		if field.ID == "parent" {
			field.Required = selectedType.Subtask
		}
		values = append(values, h.createFieldBean(field))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"startAt": start, "maxResults": limit, "total": len(typeFields), "fields": values,
	})
}

func (h *Handler) createMetaProject(r *http.Request, projectIDOrKey string) (*models.CreateProjectMeta, *jerr) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		return nil, e
	}
	meta, err := h.creatableMetadata(r, wsID, userID)
	if err != nil {
		return nil, &jerr{http.StatusInternalServerError, "internal error", nil}
	}
	for i := range meta.Projects {
		project := &meta.Projects[i]
		if project.Project.ID == projectIDOrKey || strings.EqualFold(project.Project.Key, projectIDOrKey) {
			return project, nil
		}
	}
	return nil, &jerr{http.StatusBadRequest, "You cannot create issues in this project.", nil}
}

// creatableMetadata is the create metadata for the projects where the caller
// holds Create issues; the project field offers only those projects.
func (h *Handler) creatableMetadata(r *http.Request, workspaceID, userID string) (*models.IssueCreateMetadata, error) {
	meta, err := h.Store.IssueCreateMetadata(r.Context(), workspaceID, userID)
	if err != nil {
		return nil, err
	}
	creatable, err := h.Store.ProjectsWithPermissions(r.Context(), workspaceID, userID, []string{"CREATE_ISSUES"})
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, project := range creatable {
		allowed[project.ID] = true
	}
	projects := meta.Projects[:0]
	for _, project := range meta.Projects {
		if !allowed[project.Project.ID] {
			continue
		}
		for i := range project.Fields {
			if project.Fields[i].ID != "project" {
				continue
			}
			options := []models.CreateFieldOption{}
			for _, option := range project.Fields[i].Options {
				if allowed[option.ID] {
					options = append(options, option)
				}
			}
			project.Fields[i].Options = options
		}
		projects = append(projects, project)
	}
	meta.Projects = projects
	return meta, nil
}

// createMetaIssueTypeBean is the issue type bean create metadata carries: the
// same numeric id and details every other issue type response uses.
func (h *Handler) createMetaIssueTypeBean(issueType models.IssueType) map[string]any {
	return h.issueTypeBean(issueType)
}

func (h *Handler) createFieldBean(field models.CreateFieldMeta) map[string]any {
	bean := map[string]any{
		"fieldId": field.ID, "key": field.ID, "name": field.Name, "required": field.Required,
		"schema": createFieldSchema(field), "allowedValues": createAllowedValues(field.Options),
	}
	if field.Default != "" {
		bean["hasDefaultValue"] = true
		bean["defaultValue"] = json.RawMessage(field.Default)
	}
	return bean
}

func (h *Handler) legacyCreateFieldBean(field models.CreateFieldMeta) map[string]any {
	bean := h.createFieldBean(field)
	delete(bean, "fieldId")
	if _, present := bean["hasDefaultValue"]; !present {
		bean["hasDefaultValue"] = false
	}
	bean["operations"] = []string{"set"}
	if field.ID == "timetracking" {
		bean["operations"] = []string{"set", "edit"}
	}
	return bean
}

func createFieldSchema(field models.CreateFieldMeta) map[string]any {
	schema := map[string]any{"type": field.Type}
	if field.ID == "description" {
		schema["system"] = field.ID
	} else if field.Custom {
		schema["custom"] = field.TypeKey
		customID := strings.TrimPrefix(field.ID, "customfield_")
		if numericID, err := strconv.Atoi(customID); err == nil {
			schema["customId"] = numericID
		} else {
			schema["customId"] = customID
		}
	} else {
		schema["system"] = field.ID
	}
	if field.Type == "versions" {
		schema["type"] = "array"
		schema["items"] = "version"
	}
	if field.Type == "components" {
		schema["type"] = "array"
		schema["items"] = "component"
	}
	if field.Type == "array" {
		schema["items"] = "string"
	}
	switch field.Type {
	case "options":
		schema["type"], schema["items"] = "array", "option"
	case "users":
		schema["type"], schema["items"] = "array", "user"
	case "groups":
		schema["type"], schema["items"] = "array", "group"
	case "url":
		schema["type"] = "string"
	case "projectpicker":
		schema["type"] = "project"
	}
	return schema
}

func createAllowedValues(options []models.CreateFieldOption) []map[string]any {
	values := make([]map[string]any, 0, len(options))
	for _, option := range options {
		value := map[string]any{"name": option.Name}
		if option.ID != "" {
			value["id"] = option.ID
		}
		if option.Key != "" {
			value["key"] = option.Key
		}
		values = append(values, value)
	}
	return values
}

func metadataPage(r *http.Request) (int, int, *jerr) {
	start, limit := 0, 50
	for name, target := range map[string]*int{"startAt": &start, "maxResults": &limit} {
		if raw := r.URL.Query().Get(name); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 0 || (name == "maxResults" && value > 100) {
				return 0, 0, &jerr{http.StatusBadRequest, name + " must be between 0 and 100.", nil}
			}
			*target = value
		}
	}
	return start, limit, nil
}

func commaQuerySet(r *http.Request, names ...string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, name := range names {
		for _, raw := range r.URL.Query()[name] {
			for _, value := range strings.Split(raw, ",") {
				if value = strings.TrimSpace(value); value != "" {
					out[strings.ToLower(value)] = struct{}{}
				}
			}
		}
	}
	return out
}

func querySetContains(values map[string]struct{}, candidates ...string) bool {
	for _, candidate := range candidates {
		if _, ok := values[strings.ToLower(candidate)]; ok {
			return true
		}
	}
	return false
}

// ---- search ----

func (h *Handler) compileJQL(ctx context.Context, workspaceID, raw, currentUser string) (jql.Compiled, *jerr) {
	if raw == "" {
		raw = "ORDER BY updated DESC"
	}
	q, err := jql.Parse(raw)
	if err != nil {
		return jql.Compiled{}, &jerr{http.StatusBadRequest, "Error in the JQL Query: " + err.Error(), nil}
	}
	if err := h.Store.ExpandAppJQL(ctx, workspaceID, q); err != nil {
		return jql.Compiled{}, &jerr{http.StatusBadRequest, "Error in the JQL Query: " + err.Error(), nil}
	}
	resolver, err := h.Store.JQLResolver(ctx, workspaceID)
	if err != nil {
		return jql.Compiled{}, &jerr{http.StatusInternalServerError, "internal error", nil}
	}
	// offset 2: store.Search reserves $1 for the workspace predicate
	c := jql.CompileAt(q, currentUser, resolver, 2)
	if c.Err != nil {
		return jql.Compiled{}, &jerr{http.StatusBadRequest, "Error in the JQL Query: " + c.Err.Error(), nil}
	}
	return c, nil
}

// compileJQLValidated compiles a query under Jira's validation modes.
// Malformed JQL is always refused. strict (and its legacy synonym true)
// reports every clause error; warn (and false) reports them as warnings while
// the failing clauses match nothing; none reports nothing.
func (h *Handler) compileJQLValidated(ctx context.Context, workspaceID, raw, currentUser, mode string) (compiled jql.Compiled, warnings, errors []string, failure *jerr) {
	if raw == "" {
		raw = "ORDER BY updated DESC"
	}
	q, err := jql.Parse(raw)
	if err != nil {
		return jql.Compiled{}, nil, nil, &jerr{http.StatusBadRequest, "Error in the JQL Query: " + err.Error(), nil}
	}
	if err = h.Store.ExpandAppJQL(ctx, workspaceID, q); err != nil {
		return jql.Compiled{}, nil, nil, &jerr{http.StatusBadRequest, "Error in the JQL Query: " + err.Error(), nil}
	}
	resolver, resolverErr := h.Store.JQLResolver(ctx, workspaceID)
	if resolverErr != nil {
		return jql.Compiled{}, nil, nil, &jerr{http.StatusInternalServerError, "internal error", nil}
	}
	compiled = jql.CompileLenientAt(q, currentUser, resolver, 2)
	if compiled.Err != nil {
		return jql.Compiled{}, nil, nil, &jerr{http.StatusBadRequest, "Error in the JQL Query: " + compiled.Err.Error(), nil}
	}
	messages := make([]string, 0, len(compiled.Warnings))
	for _, warning := range compiled.Warnings {
		messages = append(messages, "Error in the JQL Query: "+warning)
	}
	switch mode {
	case "none":
		return compiled, nil, nil, nil
	case "warn", "false":
		return compiled, messages, nil, nil
	default:
		return compiled, nil, messages, nil
	}
}

const defaultSearchPageSize = 50
const maximumSearchPageSize = 100

func validateSearchPage(startAt, maxResults int) *jerr {
	if startAt < 0 {
		return &jerr{http.StatusBadRequest, "startAt must be a non-negative integer.", nil}
	}
	if maxResults < 0 || maxResults > maximumSearchPageSize {
		return &jerr{http.StatusBadRequest, "maxResults must be between 0 and 100.", nil}
	}
	return nil
}

func querySearchPage(r *http.Request) (int, int, *jerr) {
	startAt := 0
	maxResults := defaultSearchPageSize
	if raw := r.URL.Query().Get("startAt"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return 0, 0, &jerr{http.StatusBadRequest, "startAt must be a non-negative integer.", nil}
		}
		startAt = value
	}
	if raw := r.URL.Query().Get("maxResults"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return 0, 0, &jerr{http.StatusBadRequest, "maxResults must be between 0 and 100.", nil}
		}
		maxResults = value
	}
	if err := validateSearchPage(startAt, maxResults); err != nil {
		return 0, 0, err
	}
	return startAt, maxResults, nil
}

func (h *Handler) runSearch(w http.ResponseWriter, r *http.Request, jqlText string, startAt, maxResults int, options searchOptions) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if e := validateSearchPage(startAt, maxResults); e != nil {
		writeJerr(w, e)
		return
	}
	if e := validateSearchOptions(&options); e != nil {
		writeJerr(w, e)
		return
	}
	c, warnings, validationErrors, e := h.compileJQLValidated(r.Context(), wsID, jqlText, userID, options.Validate)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if len(validationErrors) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"errorMessages": validationErrors, "errors": map[string]string{}})
		return
	}
	issues, total, err := h.Store.Search(r.Context(), wsID, userID, c, maxResults, startAt)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "Error in the JQL Query: "+err.Error())
		return
	}
	customFields, err := h.Store.CustomFieldsForWorkspace(r.Context(), wsID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load search field metadata.")
		return
	}
	definitions := searchFieldDefinitions(customFields)
	beans, err := h.searchIssueBeans(r.Context(), wsID, userID, issues, options, true, definitions)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load issue properties.")
		return
	}
	response := map[string]any{
		"expand":     strings.Join(options.Expand, ","),
		"startAt":    startAt,
		"maxResults": maxResults,
		"total":      total,
		"issues":     beans,
	}
	if len(warnings) > 0 {
		response["warningMessages"] = warnings
	}
	requested := normalizeSearchFields(options.Fields, definitions, options.FieldsByKeys)
	names, schemas := searchFieldMetadata(requested, true, options.FieldsByKeys, definitions)
	if hasSearchExpand(options, "names") {
		response["names"] = names
	}
	if hasSearchExpand(options, "schema") {
		response["schema"] = schemas
	}
	writeJSON(w, http.StatusOK, response)
}

type searchRequest struct {
	JQL          string   `json:"jql"`
	StartAt      *int     `json:"startAt"`
	MaxResults   *int     `json:"maxResults"`
	Fields       []string `json:"fields"`
	Expand       []string `json:"expand"`
	Properties   []string `json:"properties"`
	FieldsByKeys bool     `json:"fieldsByKeys"`
	FailFast     bool     `json:"failFast"`
	Validate     string   `json:"validateQuery"`
}

type enhancedSearchRequest struct {
	JQL                     string   `json:"jql"`
	MaxResults              *int     `json:"maxResults"`
	Fields                  []string `json:"fields"`
	NextPageToken           string   `json:"nextPageToken"`
	Expand                  string   `json:"expand"`
	Properties              []string `json:"properties"`
	FieldsByKeys            bool     `json:"fieldsByKeys"`
	FailFast                bool     `json:"failFast"`
	ReconcileIssues         []int64  `json:"reconcileIssues"`
	IncludeArchivedProjects bool     `json:"includeArchivedProjects"`
}

type enhancedSearchCursor struct {
	Version    int    `json:"v"`
	SnapshotID string `json:"s"`
	Position   int64  `json:"p"`
	QueryHash  string `json:"q"`
	Workspace  string `json:"w"`
	User       string `json:"u"`
	ExpiresAt  int64  `json:"e"`
}

func enhancedSearchQueryHash(query string, reconcileIssues []int64) string {
	ids := append([]int64{}, reconcileIssues...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	material, _ := json.Marshal(struct {
		JQL       string  `json:"jql"`
		Reconcile []int64 `json:"reconcileIssues"`
	}{JQL: strings.TrimSpace(query), Reconcile: ids})
	sum := sha256.Sum256(material)
	return base64.RawURLEncoding.EncodeToString(sum[:16])
}

func encodeEnhancedSearchCursor(cursor enhancedSearchCursor) string {
	payload, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeEnhancedSearchCursor(token, query, workspaceID, userID string, reconcileIssues []int64, now time.Time) (enhancedSearchCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return enhancedSearchCursor{}, err
	}
	var cursor enhancedSearchCursor
	if err = json.Unmarshal(raw, &cursor); err != nil {
		return enhancedSearchCursor{}, err
	}
	if cursor.Version != 2 || cursor.SnapshotID == "" || cursor.Position < 0 || cursor.ExpiresAt < now.Unix() ||
		cursor.QueryHash != enhancedSearchQueryHash(query, reconcileIssues) || cursor.Workspace != workspaceID || cursor.User != userID {
		return enhancedSearchCursor{}, errors.New("cursor does not match this search")
	}
	return cursor, nil
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	jqlText := r.URL.Query().Get("jql")
	startAt, maxResults, e := querySearchPage(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	options := searchOptions{
		Fields: r.URL.Query()["fields"], Expand: r.URL.Query()["expand"], Properties: r.URL.Query()["properties"],
		Validate: r.URL.Query().Get("validateQuery"),
	}
	for name, target := range map[string]*bool{"fieldsByKeys": &options.FieldsByKeys, "failFast": &options.FailFast} {
		if raw := r.URL.Query().Get(name); raw != "" {
			value, err := strconv.ParseBool(raw)
			if err != nil {
				jiraError(w, http.StatusBadRequest, name+" must be true or false.")
				return
			}
			*target = value
		}
	}
	if r.Method == http.MethodPost {
		var request searchRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"jql": "Invalid request payload."})
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			jiraError(w, http.StatusBadRequest, "Expected one JSON object.")
			return
		}
		jqlText = request.JQL
		startAt = 0
		maxResults = defaultSearchPageSize
		if request.StartAt != nil {
			startAt = *request.StartAt
		}
		if request.MaxResults != nil {
			maxResults = *request.MaxResults
		}
		options = searchOptions{Fields: request.Fields, Expand: request.Expand, Properties: request.Properties, FieldsByKeys: request.FieldsByKeys, FailFast: request.FailFast, Validate: request.Validate}
	}
	h.runSearch(w, r, jqlText, startAt, maxResults, options)
}

func (h *Handler) searchJQL(w http.ResponseWriter, r *http.Request) {
	var req enhancedSearchRequest
	if r.Method == http.MethodGet {
		q := r.URL.Query()
		for key := range q {
			switch key {
			case "jql", "fields", "maxResults", "nextPageToken", "expand", "properties", "fieldsByKeys", "failFast", "reconcileIssues", "includeArchivedProjects":
			default:
				jiraError(w, 400, "Unsupported enhanced search parameter: "+key)
				return
			}
		}
		req.JQL = q.Get("jql")
		req.NextPageToken = q.Get("nextPageToken")
		req.Fields = q["fields"]
		req.Properties = q["properties"]
		req.Expand = q.Get("expand")
		if q.Has("maxResults") {
			n, err := strconv.Atoi(q.Get("maxResults"))
			if err != nil {
				jiraError(w, 400, "maxResults must be an integer.")
				return
			}
			req.MaxResults = &n
		}
		for name, target := range map[string]*bool{"fieldsByKeys": &req.FieldsByKeys, "failFast": &req.FailFast, "includeArchivedProjects": &req.IncludeArchivedProjects} {
			if raw := q.Get(name); raw != "" {
				value, err := strconv.ParseBool(raw)
				if err != nil {
					jiraError(w, http.StatusBadRequest, name+" must be true or false.")
					return
				}
				*target = value
			}
		}
		for _, raw := range splitSearchValues(q["reconcileIssues"]) {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				jiraError(w, http.StatusBadRequest, "reconcileIssues must contain numeric issue IDs.")
				return
			}
			req.ReconcileIssues = append(req.ReconcileIssues, value)
		}
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			jiraError(w, 400, "Invalid enhanced search request: "+err.Error())
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			jiraError(w, 400, "Expected one JSON object.")
			return
		}
	}
	if len(req.ReconcileIssues) > 50 {
		jiraError(w, http.StatusBadRequest, "A maximum of 50 issue IDs can be reconciled.")
		return
	}
	if req.IncludeArchivedProjects {
		jiraError(w, http.StatusBadRequest, "Archived project search is not available until project archiving is configured.")
		return
	}
	options := searchOptions{Fields: req.Fields, Expand: []string{req.Expand}, Properties: req.Properties, FieldsByKeys: req.FieldsByKeys, FailFast: req.FailFast}
	if e := validateSearchOptions(&options); e != nil {
		writeJerr(w, e)
		return
	}
	maxResults := defaultSearchPageSize
	if req.MaxResults != nil {
		maxResults = *req.MaxResults
	}
	if maxResults < 1 || maxResults > 5000 {
		jiraError(w, 400, "maxResults must be between 1 and 5000.")
		return
	}
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	now := time.Now()
	cursor := enhancedSearchCursor{
		Version: 2, QueryHash: enhancedSearchQueryHash(req.JQL, req.ReconcileIssues),
		Workspace: wsID, User: userID, ExpiresAt: now.Add(7 * 24 * time.Hour).Unix(),
	}
	if req.NextPageToken != "" {
		var err error
		cursor, err = decodeEnhancedSearchCursor(req.NextPageToken, req.JQL, wsID, userID, req.ReconcileIssues, now)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "Invalid nextPageToken.")
			return
		}
	}
	parsed, err := jql.Parse(req.JQL)
	if err != nil {
		jiraError(w, 400, "Error in the JQL Query: "+err.Error())
		return
	}
	if root, ok := parsed.Root.(jql.Text); ok && root.Value == "" {
		jiraError(w, 400, "Enhanced search requires a bounded JQL query.")
		return
	}
	c, e := h.compileJQL(r.Context(), wsID, req.JQL, userID)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if cursor.SnapshotID == "" {
		cursor.SnapshotID, err = h.Store.CreateSearchSnapshot(r.Context(), wsID, userID, cursor.QueryHash, c, time.Unix(cursor.ExpiresAt, 0))
		if err != nil {
			log.Printf("api3: create search snapshot: %v", err)
			jiraError(w, http.StatusInternalServerError, "Could not create search snapshot.")
			return
		}
	}
	page, err := h.Store.SearchSnapshotPage(r.Context(), cursor.SnapshotID, wsID, userID, cursor.QueryHash, cursor.Position, maxResults)
	if err != nil {
		if errors.Is(err, store.ErrSearchSnapshot) {
			jiraError(w, http.StatusBadRequest, "Invalid nextPageToken.")
		} else {
			jiraError(w, http.StatusInternalServerError, "Could not read search snapshot.")
		}
		return
	}
	issues, hasMore := page.Issues, page.HasMore
	customFields, err := h.Store.CustomFieldsForWorkspace(r.Context(), wsID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load search field metadata.")
		return
	}
	definitions := searchFieldDefinitions(customFields)
	beans, err := h.searchIssueBeans(r.Context(), wsID, userID, issues, options, false, definitions)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load issue properties.")
		return
	}
	resp := map[string]any{"issues": beans, "isLast": !hasMore}
	requested := normalizeSearchFields(options.Fields, definitions, options.FieldsByKeys)
	names, schemas := searchFieldMetadata(requested, false, options.FieldsByKeys, definitions)
	if hasSearchExpand(options, "names") {
		resp["names"] = names
	}
	if hasSearchExpand(options, "schema") {
		resp["schema"] = schemas
	}
	if hasMore {
		cursor.Position = page.NextPosition
		resp["nextPageToken"] = encodeEnhancedSearchCursor(cursor)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) searchCount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		JQL string `json:"jql"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"jql": "Invalid request payload."})
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		jiraError(w, http.StatusBadRequest, "Expected one JSON object.")
		return
	}
	parsed, err := jql.Parse(req.JQL)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "Error in the JQL Query: "+err.Error())
		return
	}
	if root, ok := parsed.Root.(jql.Text); ok && root.Value == "" {
		jiraError(w, http.StatusBadRequest, "Approximate count requires a bounded JQL query.")
		return
	}
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	c, e := h.compileJQL(r.Context(), wsID, req.JQL, userID)
	if e != nil {
		writeJerr(w, e)
		return
	}
	_, total, err := h.Store.Search(r.Context(), wsID, userID, c, 1, 0)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "Error in the JQL Query: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": total})
}

// ---- bootstrap (custom delta-sync endpoint) ----

// BootstrapHandler serves GET /bootstrap — registered on the top-level mux
// (it lives outside the /rest/api/3 subtree by design).
func (h *Handler) BootstrapHandler(w http.ResponseWriter, r *http.Request) {
	h.bootstrap(w, r)
}

// bootstrap returns sync-payload-shaped data (same shapes as action payloads)
// so the client's apply path materializes it unchanged.
func (h *Handler) bootstrap(w http.ResponseWriter, r *http.Request) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	snap, err := h.Store.BootstrapSnapshot(r.Context(), wsID, userID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "bootstrap failed")
		return
	}
	issues := make([]models.IssueUpsertPayload, 0, len(snap.Issues))
	for i := range snap.Issues {
		issues = append(issues, models.IssueUpsertPayload{Issue: snap.Issues[i]})
	}
	comments := make([]models.CommentUpsertPayload, 0, len(snap.Comments))
	for i := range snap.Comments {
		comments = append(comments, models.CommentUpsertPayload{Comment: snap.Comments[i]})
	}
	attachments := make([]models.AttachmentUpsertPayload, 0, len(snap.Attachments))
	for i := range snap.Attachments {
		attachments = append(attachments, models.AttachmentUpsertPayload{Attachment: snap.Attachments[i]})
	}
	worklogs := make([]models.WorklogUpsertPayload, 0, len(snap.Worklogs))
	for i := range snap.Worklogs {
		worklogs = append(worklogs, models.WorklogUpsertPayload{Worklog: snap.Worklogs[i]})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"seq":         snap.Seq,
		"issues":      issues,
		"comments":    comments,
		"attachments": attachments,
		"worklogs":    worklogs,
	})
}

// createMetaTypeMatches reports whether a request names this issue type, by the
// numeric id clients use or, for requests the product itself makes, its name.
func createMetaTypeMatches(issueType models.IssueType, requested string) bool {
	requested = strings.TrimSpace(requested)
	return requested == jiraIDString(issueType.JiraID) || requested == issueType.ID || strings.EqualFold(requested, issueType.Name)
}
