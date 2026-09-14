package api3

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// projectExpandOptions is the expand attribute Jira sets on project beans.
const projectExpandOptions = "description,lead,issueTypes,url,projectKeys,permissions,insight"

// jiraDateTime is the timestamp shape Jira uses in project beans.
const jiraDateTime = "2006-01-02T15:04:05.000-0700"

// projectView says how much of a project a response carries.
type projectView struct {
	expands    map[string]struct{}
	properties map[string]struct{}
	// full is GET /project/{projectIdOrKey}: description, issue types, lead,
	// components, versions and roles by default.
	full bool
	// insightMillis reports lastIssueUpdateTime in epoch milliseconds, as the
	// recent-projects operation does.
	insightMillis bool
	categories    map[string]*models.ProjectCategory
}

func (h *Handler) newProjectView(r *http.Request, workspaceID string, full bool) (projectView, error) {
	categories, err := h.projectCategoryMap(r.Context(), workspaceID)
	if err != nil {
		return projectView{}, err
	}
	return projectView{expands: commaQuerySet(r, "expand"), properties: commaQuerySet(r, "properties"), full: full, categories: categories}, nil
}

func (v projectView) has(expand string) bool {
	return querySetContains(v.expands, expand, "*")
}

// projectRepresentation is the project bean the caller reads, with the
// expansions and properties the request asked for.
func (h *Handler) projectRepresentation(r *http.Request, workspaceID, userID string, p *models.Project, view projectView) (map[string]any, error) {
	ctx := r.Context()
	bean := h.projectBean(p)
	bean["expand"] = projectExpandOptions
	if !view.full && !view.has("description") {
		delete(bean, "description")
	}
	if !view.full && !view.has("url") {
		delete(bean, "url")
	}
	if category := view.categories[p.CategoryID]; category != nil {
		bean["projectCategory"] = h.categoryBean(category)
	}
	if (view.full || view.has("lead")) && p.LeadAccountID != "" {
		lead, err := h.Store.UserByID(ctx, p.LeadAccountID)
		if err != nil {
			return nil, err
		}
		bean["lead"] = h.userBeanFor(ctx, lead)
	}
	var issueTypes []models.IssueType
	if view.full || view.has("issueTypes") || view.has("issueTypeHierarchy") {
		types, err := h.Store.ProjectIssueTypes(ctx, workspaceID, p.ID, nil)
		if err != nil {
			return nil, err
		}
		issueTypes = types
	}
	if view.full || view.has("issueTypes") {
		beans := make([]map[string]any, 0, len(issueTypes))
		for _, issueType := range issueTypes {
			beans = append(beans, h.issueTypeBean(issueType))
		}
		bean["issueTypes"] = beans
	}
	if view.full && view.has("issueTypeHierarchy") {
		bean["issueTypeHierarchy"] = issueTypeHierarchyBean(issueTypes)
	}
	if view.has("projectKeys") {
		bean["projectKeys"] = []string{p.Key}
	}
	if view.has("permissions") {
		canEdit, err := h.hasProjectPermission(ctx, workspaceID, userID, p.ID, "", "ADMINISTER_PROJECTS")
		if err != nil {
			return nil, err
		}
		bean["permissions"] = map[string]any{"canEdit": canEdit}
	}
	if view.has("insight") {
		count, updated, err := h.Store.ProjectInsight(ctx, workspaceID, p.ID)
		if err != nil {
			return nil, err
		}
		insight := map[string]any{"totalIssueCount": count}
		if updated > 0 {
			if view.insightMillis {
				insight["lastIssueUpdateTime"] = updated
			} else {
				insight["lastIssueUpdateTime"] = time.UnixMilli(updated).UTC().Format(jiraDateTime)
			}
		}
		bean["insight"] = insight
	}
	if view.full {
		components, err := h.Store.Components(ctx, workspaceID, p.ID, "", "name")
		if err != nil {
			return nil, err
		}
		componentBeans := make([]map[string]any, 0, len(components))
		for _, component := range components {
			componentBeans = append(componentBeans, h.componentBean(r, component, false))
		}
		bean["components"] = componentBeans
		versions, err := h.Store.ProjectVersions(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		versionBeans := make([]map[string]any, 0, len(versions))
		for _, version := range versions {
			versionBeans = append(versionBeans, h.versionBean(version))
		}
		bean["versions"] = versionBeans
		roles, err := h.Store.ProjectRoles(ctx, workspaceID)
		if err != nil {
			return nil, err
		}
		roleURLs := make(map[string]string, len(roles))
		for _, role := range roles {
			roleURLs[role.Name] = h.BaseURL + "/rest/api/3/project/" + url.PathEscape(p.Key) + "/role/" + strconv.FormatInt(role.ID, 10)
		}
		bean["roles"] = roleURLs
		bean["isPrivate"] = false
	}
	if view.full || len(view.properties) > 0 {
		selected := map[string]any{}
		if len(view.properties) > 0 {
			properties, err := h.Store.ProjectProperties(ctx, workspaceID, p.ID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
			for _, property := range properties {
				if querySetContains(view.properties, property.Key) {
					selected[property.Key] = property.Value
				}
			}
		}
		bean["properties"] = selected
	}
	if err := h.projectLifecycleFields(r, p, bean); err != nil {
		return nil, err
	}
	return bean, nil
}

// projectLifecycleFields adds the archive and trash details Jira reports for
// projects outside the live state.
func (h *Handler) projectLifecycleFields(r *http.Request, p *models.Project, bean map[string]any) error {
	actor := func() (map[string]any, error) {
		if p.LifecycleActor == "" {
			return nil, nil
		}
		user, err := h.Store.UserByID(r.Context(), p.LifecycleActor)
		if err != nil {
			return nil, err
		}
		return h.userBeanFor(r.Context(), user), nil
	}
	switch p.LifecycleState {
	case store.ProjectLifecycleArchived:
		bean["archived"] = true
		if at, err := time.Parse(time.RFC3339, p.ArchivedAt); err == nil {
			bean["archivedDate"] = at.UTC().Format(jiraDateTime)
		}
		by, err := actor()
		if err != nil {
			return err
		}
		if by != nil {
			bean["archivedBy"] = by
		}
	case store.ProjectLifecycleTrashed:
		bean["deleted"] = true
		if at, err := time.Parse(time.RFC3339, p.TrashedAt); err == nil {
			bean["deletedDate"] = at.UTC().Format(jiraDateTime)
			bean["retentionTillDate"] = at.UTC().Add(60 * 24 * time.Hour).Format(jiraDateTime)
		}
		by, err := actor()
		if err != nil {
			return err
		}
		if by != nil {
			bean["deletedBy"] = by
		}
	}
	return nil
}

// issueTypeHierarchyBean groups a project's issue types by hierarchy level.
func issueTypeHierarchyBean(issueTypes []models.IssueType) map[string]any {
	byLevel := map[int][]models.IssueType{}
	for _, issueType := range issueTypes {
		byLevel[issueType.HierarchyLevel] = append(byLevel[issueType.HierarchyLevel], issueType)
	}
	levels := make([]int, 0, len(byLevel))
	for level := range byLevel {
		levels = append(levels, level)
	}
	sort.Ints(levels)
	beans := make([]map[string]any, 0, len(levels))
	for _, level := range levels {
		types := byLevel[level]
		name := types[0].Name
		switch level {
		case -1:
			name = "Subtask"
		case 0:
			name = "Base"
		case 1:
			name = "Epic"
		}
		ids := make([]int64, 0, len(types))
		for _, issueType := range types {
			ids = append(ids, issueType.JiraID)
		}
		beans = append(beans, map[string]any{"level": level, "hierarchyLevelNumber": level, "name": name, "issueTypeIds": ids})
	}
	return map[string]any{"levels": beans}
}

// projectPropertyQuery matches Jira's propertyQuery, [key].path=value, against
// a project's stored properties.
type projectPropertyQuery struct {
	key, path string
	want      any
}

func parseProjectPropertyQuery(raw string) (*projectPropertyQuery, error) {
	if raw == "" {
		return nil, nil
	}
	if !strings.HasPrefix(raw, "[") {
		return nil, fmt.Errorf("propertyQuery must start with the property key in square brackets")
	}
	end := strings.Index(raw, "]")
	if end < 2 {
		return nil, fmt.Errorf("propertyQuery must name a property key")
	}
	query := &projectPropertyQuery{key: raw[1:end]}
	rest := raw[end+1:]
	left, value, ok := strings.Cut(rest, "=")
	if !ok {
		return nil, fmt.Errorf("propertyQuery must compare a value with =")
	}
	if left != "" && !strings.HasPrefix(left, ".") {
		return nil, fmt.Errorf("propertyQuery paths are separated by dots")
	}
	query.path = strings.TrimPrefix(left, ".")
	if err := json.Unmarshal([]byte(value), &query.want); err != nil {
		query.want = value
	}
	return query, nil
}

func (q *projectPropertyQuery) matches(properties []models.ProjectProperty) bool {
	for _, property := range properties {
		if property.Key != q.key {
			continue
		}
		var current any
		if json.Unmarshal(property.Value, &current) != nil {
			return false
		}
		if q.path != "" {
			for _, segment := range strings.Split(q.path, ".") {
				object, ok := current.(map[string]any)
				if !ok {
					return false
				}
				current = object[segment]
			}
		}
		if values, ok := current.([]any); ok {
			for _, value := range values {
				if fmt.Sprint(value) == fmt.Sprint(q.want) {
					return true
				}
			}
			return false
		}
		return current != nil && fmt.Sprint(current) == fmt.Sprint(q.want)
	}
	return false
}
