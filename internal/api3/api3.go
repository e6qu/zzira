// Package api3 is the REST edge for the Atlassian Jira Cloud REST API v3
// contract. It only translates wire DTOs ⇄ internal models and calls the
// command core. Wire shapes are locked by golden tests (golden_test.go).
package api3

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

type Handler struct {
	Store         *store.Store
	Commands      *commands.Service
	Blobs         attachments.Store
	BaseURL       string
	WorkspaceSlug string
	// StaticDir holds the server's static assets, such as system avatar icons.
	StaticDir string
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/rest/servicedeskapi/") {
		h.serviceDeskRoute(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/jira/forms/cloud/") {
		h.issueFormsRoute(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/rest/devinfo/0.10/") || strings.HasPrefix(r.URL.Path, "/jira/devinfo/0.1/cloud/") {
		h.developmentRoute(w, r)
		return
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/rest/webhooks/1.0/"):
		h.adminWebhookRoute(w, r)
		return
	case strings.HasPrefix(r.URL.Path, "/rest/atlassian-connect/1/migration/"):
		h.connectMigrationRoute(w, r)
		return
	case r.URL.Path == "/rest/atlassian-connect/1/service-registry":
		h.serviceRegistry(w, r)
		return
	case strings.HasPrefix(r.URL.Path, "/rest/atlassian-connect/1/addons/"):
		h.connectAddonProperties(w, r)
		return
	case r.URL.Path == "/rest/forge/1/app/properties" || strings.HasPrefix(r.URL.Path, "/rest/forge/1/app/properties/"):
		h.forgeAppProperties(w, r)
		return
	case r.URL.Path == "/rest/internal/api/latest/worklog/bulk":
		h.internalWorklogBulk(w, r)
		return
	}
	if module, ok := providerModuleFor(r.URL.Path); ok {
		h.softwareProviderRoute(w, r, module)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/rest/builds/0.1/") || strings.HasPrefix(r.URL.Path, "/jira/builds/0.1/cloud/") {
		h.softwareDeliveryRoute(w, r, "builds")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/rest/deployments/0.1/") || strings.HasPrefix(r.URL.Path, "/jira/deployments/0.1/cloud/") {
		h.softwareDeliveryRoute(w, r, "deployments")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/rest/api/3")
	if h.peopleRoute(w, r, path) {
		return
	}
	switch {
	case path == "/announcementBanner":
		h.siteAnnouncementBanner(w, r)
	case path == "/application-properties" || path == "/application-properties/advanced-settings" || strings.HasPrefix(path, "/application-properties/"):
		h.siteApplicationProperties(w, r, path)
	case path == "/configuration":
		h.globalJiraConfiguration(w, r)
	case path == "/configuration/timetracking" || path == "/configuration/timetracking/list" || path == "/configuration/timetracking/options":
		h.siteTimeTracking(w, r, path)
	case path == "/settings/columns":
		h.issueNavigatorColumns(w, r)
	case isNotificationSchemePath(path):
		h.notificationSchemeRoute(w, r, path)
	case isIssueSecurityPath(path):
		h.securitySchemeRoute(w, r, path)
	case isScreenPath(path):
		h.screenRoute(w, r, path)
	case isScreenSchemePath(path):
		h.screenSchemeRoute(w, r, path)
	case isFieldConfigurationPath(path):
		h.fieldConfigurationRoute(w, r, path)
	case isPermissionSchemePath(path):
		h.permissionSchemeRoute(w, r, path)
	case isProjectRolePath(path):
		h.projectRoleRoute(w, r, path)
	case isProjectLifecyclePath(path, r.Method):
		h.projectLifecycleRoute(w, r, path)
	case isProjectGovernancePath(path):
		h.projectGovernanceRoute(w, r, path)
	case strings.HasPrefix(path, "/bulk/"):
		h.bulkIssueRoute(w, r, strings.TrimPrefix(path, "/bulk/"))
	case path == "/dashboard":
		h.dashboardRoute(w, r, nil)
	case strings.HasPrefix(path, "/dashboard/"):
		h.dashboardRoute(w, r, strings.Split(strings.TrimPrefix(path, "/dashboard/"), "/"))
	case path == "/project-template" || strings.HasPrefix(path, "/project-template/"):
		h.projectTemplateRoute(w, r, path)
	case strings.HasPrefix(path, "/app/field/"):
		h.appFieldRoute(w, r, path)
	case path == "/plans/plan" || strings.HasPrefix(path, "/plans/plan/"):
		h.plansRoute(w, r, path)
	case path == "/serverInfo" && r.Method == http.MethodGet:
		h.serverInfo(w, r)
	case path == "/myself" && r.Method == http.MethodGet:
		h.myself(w, r)
	case path == "/issue" && r.Method == http.MethodPost:
		h.createIssue(w, r)
	case path == "/field" && r.Method == http.MethodGet:
		h.listFields(w, r)
	case path == "/field" && r.Method == http.MethodPost:
		h.createField(w, r)
	case path == "/worklog/updated" || path == "/worklog/deleted" || path == "/worklog/list":
		h.worklogFeedRoute(w, r, path)
	case strings.HasPrefix(path, "/customFieldOption/"):
		h.customFieldOptionResource(w, r, strings.TrimPrefix(path, "/customFieldOption/"))
	case path == "/field/association":
		h.fieldAssociationRoute(w, r)
	case path == "/config/fieldschemes" || strings.HasPrefix(path, "/config/fieldschemes/"):
		h.fieldSchemeRoute(w, r, path)
	case path == "/field/search":
		h.fieldSearchRoute(w, r, false)
	case path == "/field/search/trashed":
		h.fieldSearchRoute(w, r, true)
	case path == "/projects/fields":
		h.projectsFieldsRoute(w, r)
	// `/contexts` is a different operation from `/context`, so it is matched
	// before the context router, which would otherwise swallow it.
	case strings.HasPrefix(path, "/field/") && strings.HasSuffix(path, "/contexts"):
		h.fieldContextsForField(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/field/"), "/contexts"))
	case strings.HasPrefix(path, "/field/") && strings.HasSuffix(path, "/association/project"):
		h.fieldProjectAssociations(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/field/"), "/association/project"))
	case strings.HasPrefix(path, "/field/") && strings.HasSuffix(path, "/trash") && r.Method == http.MethodPost:
		h.fieldTrashRoute(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/field/"), "/trash"), true)
	case strings.HasPrefix(path, "/field/") && strings.HasSuffix(path, "/restore") && r.Method == http.MethodPost:
		h.fieldTrashRoute(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/field/"), "/restore"), false)
	case strings.HasPrefix(path, "/field/") && strings.Contains(path, "/context"):
		rest := strings.TrimPrefix(path, "/field/")
		field, remainder, _ := strings.Cut(rest, "/context")
		h.fieldContextRoute(w, r, field, remainder)
	// An app's select list has its own option surface, separate from the
	// context options an administrator manages.
	case strings.HasPrefix(path, "/field/") && strings.Contains(path, "/option"):
		rest := strings.TrimPrefix(path, "/field/")
		fieldKey, remainder, _ := strings.Cut(rest, "/option")
		parts := []string{}
		if trimmed := strings.Trim(remainder, "/"); trimmed != "" {
			parts = strings.Split(trimmed, "/")
		}
		h.appFieldOptionRoute(w, r, fieldKey, parts)
	case strings.HasPrefix(path, "/field/") && strings.HasSuffix(path, "/screens"):
		h.screensForField(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/field/"), "/screens"))
	case strings.HasPrefix(path, "/field/"):
		h.fieldRoute(w, r, strings.Split(strings.TrimPrefix(path, "/field/"), "/"))
	case path == "/issueLinkType":
		h.issueLinkTypeRoute(w, r, "")
	case strings.HasPrefix(path, "/issueLinkType/"):
		h.issueLinkTypeRoute(w, r, strings.TrimPrefix(path, "/issueLinkType/"))
	case path == "/issueLink" && r.Method == http.MethodPost:
		h.issueLinkRoute(w, r, "")
	case strings.HasPrefix(path, "/issueLink/") && (r.Method == http.MethodGet || r.Method == http.MethodDelete):
		h.issueLinkRoute(w, r, strings.TrimPrefix(path, "/issueLink/"))
	case path == "/comment/list":
		h.commentsByIDs(w, r)
	case strings.HasPrefix(path, "/comment/"):
		parts := strings.SplitN(strings.TrimPrefix(path, "/comment/"), "/", 3)
		switch {
		case len(parts) == 2 && parts[1] == "properties":
			h.commentPropertyRoute(w, r, parts[0], "")
		case len(parts) == 3 && parts[1] == "properties" && parts[2] != "":
			h.commentPropertyRoute(w, r, parts[0], parts[2])
		default:
			jiraError(w, http.StatusNotFound, fmt.Sprintf("No resource found for path %s", r.URL.Path))
		}
	case path == "/issuetype" || strings.HasPrefix(path, "/issuetype/") ||
		path == "/priority" || strings.HasPrefix(path, "/priority/") ||
		path == "/resolution" || strings.HasPrefix(path, "/resolution/") ||
		path == "/issuetypescheme" || strings.HasPrefix(path, "/issuetypescheme/") ||
		path == "/priorityscheme" || strings.HasPrefix(path, "/priorityscheme/"):
		if !h.issueMetadataRoute(w, r, path) {
			jiraError(w, http.StatusNotFound, "No resource found for path /rest/api/3"+path)
		}
	case path == "/label" && r.Method == http.MethodGet:
		h.labelsEndpoint(w, r)
	case path == "/status" && r.Method == http.MethodGet:
		h.statusesEndpoint(w, r)
	case strings.HasPrefix(path, "/status/") && r.Method == http.MethodGet:
		h.statusDetailEndpoint(w, r, strings.TrimPrefix(path, "/status/"))
	case path == "/statuscategory" && r.Method == http.MethodGet:
		h.statusCategoryEndpoint(w, r)
	case strings.HasPrefix(path, "/statuscategory/") && r.Method == http.MethodGet:
		h.statusCategoryDetailEndpoint(w, r, strings.TrimPrefix(path, "/statuscategory/"))
	case path == "/statuses" && (r.Method == http.MethodGet || r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete):
		h.bulkStatusesEndpoint(w, r)
	case path == "/statuses/byNames" && r.Method == http.MethodGet:
		h.statusesByNameEndpoint(w, r)
	case path == "/statuses/search" && r.Method == http.MethodGet:
		h.searchStatusesEndpoint(w, r)
	case strings.HasPrefix(path, "/statuses/") && r.Method == http.MethodGet:
		h.statusUsageEndpoint(w, r, strings.Split(strings.TrimPrefix(path, "/statuses/"), "/"))
	case path == "/mypermissions" && r.Method == http.MethodGet:
		h.myPermissions(w, r)
	case path == "/permissions" && r.Method == http.MethodGet:
		h.allPermissions(w, r)
	case path == "/permissions/check" && r.Method == http.MethodPost:
		h.bulkPermissions(w, r)
	case path == "/permissions/project" && r.Method == http.MethodPost:
		h.permittedProjects(w, r)
	case path == "/workflow/search" && r.Method == http.MethodGet:
		h.workflowRoute(w, r)
	case path == "/workflow" && r.Method == http.MethodPost:
		h.workflowRoute(w, r)
	case path == "/workflows/defaultEditor" && r.Method == http.MethodGet:
		h.workflowDefaultEditor(w, r)
	case path == "/workflows/search" && r.Method == http.MethodGet:
		h.workflowSearch(w, r)
	case path == "/workflows/capabilities" && r.Method == http.MethodGet:
		h.workflowCapabilities(w, r)
	case path == "/workflows/create/validation" && r.Method == http.MethodPost:
		h.workflowCreateValidation(w, r)
	case path == "/workflows/update/validation" && r.Method == http.MethodPost:
		h.workflowUpdateValidation(w, r)
	case path == "/workflows/create" && r.Method == http.MethodPost:
		h.workflowCreate(w, r)
	case path == "/workflows/update" && r.Method == http.MethodPost:
		h.workflowUpdate(w, r)
	case path == "/workflows/preview" && r.Method == http.MethodPost:
		h.workflowPreview(w, r)
	case path == "/workflowscheme" || path == "/workflowscheme/project" || strings.HasPrefix(path, "/workflowscheme/"):
		h.workflowSchemeRoute(w, r, path)
	case strings.HasPrefix(path, "/task/"):
		h.taskRoute(w, r, path)
	case path == "/workflow/history" || path == "/workflow/history/list":
		h.workflowHistoryRoute(w, r, path)
	case path == "/workflow/rule/config" || path == "/workflow/rule/config/delete":
		h.workflowRuleConfigRoute(w, r, path)
	case path == "/workflows" && r.Method == http.MethodPost:
		h.readWorkflows(w, r)
	case strings.HasPrefix(path, "/workflow/project/"):
		h.workflowRoute(w, r)
	case strings.HasPrefix(path, "/workflow/"):
		h.workflowUsageRoute(w, r, path)
	case path == "/role" || strings.HasPrefix(path, "/role/"):
		h.globalProjectRoleRoute(w, r, path)
	case path == "/webhook" || strings.HasPrefix(path, "/webhook/"):
		h.dynamicWebhookRoute(w, r, path)
	case path == "/classification-levels" && r.Method == http.MethodGet:
		h.classificationLevelsEndpoint(w, r)
	case path == "/data-policy" && r.Method == http.MethodGet:
		h.workspaceDataPolicy(w, r)
	case path == "/data-policy/project" && r.Method == http.MethodGet:
		h.projectDataPolicies(w, r)
	case path == "/instance/license" && r.Method == http.MethodGet:
		h.instanceLicense(w, r)
	case path == "/license/approximateLicenseCount" && r.Method == http.MethodGet:
		h.approximateLicenseCount(w, r, "")
	case strings.HasPrefix(path, "/license/approximateLicenseCount/product/") && r.Method == http.MethodGet:
		h.approximateLicenseCount(w, r, strings.TrimPrefix(path, "/license/approximateLicenseCount/product/"))
	case path == "/auditing/record" && r.Method == http.MethodGet:
		h.auditRecords(w, r)
	case path == "/uiModifications" || strings.HasPrefix(path, "/uiModifications/"):
		h.uiModificationsRoute(w, r, path)
	case path == "/filter/defaultShareScope":
		h.filterDefaultShareScope(w, r)
	case path == "/filter/favourite" && r.Method == http.MethodGet:
		h.filterCollection(w, r, "favourite")
	case path == "/filter/my" && r.Method == http.MethodGet:
		h.filterCollection(w, r, "my")
	case path == "/filter/search" && r.Method == http.MethodGet:
		h.searchFilters(w, r)
	case path == "/filter" && r.Method == http.MethodPost:
		h.createFilter(w, r)
	case path == "/component" && r.Method == http.MethodGet:
		h.componentCollection(w, r)
	case path == "/component" && r.Method == http.MethodPost:
		h.createComponent(w, r)
	case strings.HasPrefix(path, "/component/"):
		h.componentResource(w, r, strings.Split(strings.TrimPrefix(path, "/component/"), "/"))
	case path == "/version":
		h.versionRoute(w, r, nil)
	case strings.HasPrefix(path, "/version/"):
		h.versionRoute(w, r, strings.Split(strings.TrimPrefix(path, "/version/"), "/"))
	case strings.HasPrefix(path, "/project/") && (strings.HasSuffix(path, "/statuses") || strings.HasSuffix(path, "/hierarchy") || strings.Contains(path, "/classification-")):
		h.projectPlatformRoute(w, r, path)
	case strings.HasPrefix(path, "/project/") && (strings.HasSuffix(path, "/version") || strings.HasSuffix(path, "/versions")):
		parts := strings.Split(strings.TrimPrefix(path, "/project/"), "/")
		if len(parts) != 2 {
			jiraError(w, 404, "No resource found")
			return
		}
		h.projectVersions(w, r, parts[0], parts[1] == "version")
	case strings.HasPrefix(path, "/project/") && (strings.HasSuffix(path, "/component") || strings.HasSuffix(path, "/components")):
		parts := strings.Split(strings.TrimPrefix(path, "/project/"), "/")
		if len(parts) != 2 || r.Method != http.MethodGet {
			jiraError(w, 404, "No resource found")
			return
		}
		h.projectComponents(w, r, parts[0], parts[1] == "component")
	case path == "/project" && r.Method == http.MethodGet:
		h.listProjects(w, r)
	case path == "/project" && r.Method == http.MethodPost:
		h.createProject(w, r)
	case path == "/project/search" && r.Method == http.MethodGet:
		h.searchProjects(w, r)
	case strings.HasPrefix(path, "/project/") && r.Method == http.MethodGet:
		h.getProject(w, r, strings.TrimPrefix(path, "/project/"))
	case strings.HasPrefix(path, "/project/") && r.Method == http.MethodPut:
		h.updateProject(w, r, strings.TrimPrefix(path, "/project/"))
	case path == "/user/permission/search" && r.Method == http.MethodGet:
		h.usersWithPermissions(w, r)
	case path == "/search" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		h.search(w, r)
	case path == "/search/jql" && (r.Method == http.MethodPost || r.Method == http.MethodGet):
		h.searchJQL(w, r)
	case path == "/search/approximate-count" && r.Method == http.MethodPost:
		h.searchCount(w, r)
	case path == "/jql/autocompletedata" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		h.jqlAutoCompleteData(w, r)
	case path == "/jql/autocompletedata/suggestions" && r.Method == http.MethodGet:
		h.jqlSuggestions(w, r)
	case path == "/jql/parse" && r.Method == http.MethodPost:
		h.jqlParse(w, r)
	case path == "/expression/analyse" && r.Method == http.MethodPost:
		h.analyseExpressions(w, r)
	case path == "/expression/eval" && r.Method == http.MethodPost:
		h.evaluateExpression(w, r, false)
	case path == "/expression/evaluate" && r.Method == http.MethodPost:
		h.evaluateExpression(w, r, true)
	case path == "/jql/match" && r.Method == http.MethodPost:
		h.jqlMatch(w, r)
	case path == "/jql/pdcleaner" && r.Method == http.MethodPost:
		h.jqlPersonalDataMigration(w, r)
	case path == "/jql/sanitize" && r.Method == http.MethodPost:
		h.jqlSanitize(w, r)
	case path == "/jql/function/computation" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		h.jqlFunctionPrecomputations(w, r)
	case path == "/jql/function/computation/search" && r.Method == http.MethodPost:
		h.jqlFunctionPrecomputationsByID(w, r)
	case path == "/filter" && r.Method == http.MethodGet:
		h.listFilters(w, r)
	case strings.HasPrefix(path, "/filter/"):
		h.filterCRUD(w, r, strings.TrimPrefix(path, "/filter/"))
	case path == "/bootstrap" && r.Method == http.MethodGet:
		h.bootstrap(w, r)
	case path == "/attachment/meta" && r.Method == http.MethodGet:
		h.attachmentSettings(w, r)
	case strings.HasPrefix(path, "/attachment/content/"):
		h.attachmentContent(w, r, strings.TrimPrefix(path, "/attachment/content/"))
	case strings.HasPrefix(path, "/attachment/thumbnail/"):
		h.attachmentThumbnail(w, r, strings.TrimPrefix(path, "/attachment/thumbnail/"))
	case strings.HasPrefix(path, "/attachment/"):
		parts := strings.Split(strings.TrimPrefix(path, "/attachment/"), "/")
		if len(parts) == 3 && parts[1] == "expand" && r.Method == http.MethodGet {
			h.attachmentArchive(w, r, parts[0], parts[2])
		} else if len(parts) == 1 {
			h.attachmentMeta(w, r, parts[0])
		} else {
			jiraError(w, http.StatusNotFound, "No resource found")
		}
	case path == "/issue/watching":
		h.bulkIsWatching(w, r)
	case path == "/issue/bulkfetch":
		h.bulkFetchIssues(w, r)
	case path == "/issue/bulk":
		h.createIssues(w, r)
	case path == "/issue/picker":
		h.issuePicker(w, r)
	case path == "/issue/archive":
		h.issueArchivalRoute(w, r, false)
	case path == "/issue/unarchive":
		h.issueArchivalRoute(w, r, true)
	case path == "/issue/limit/report":
		h.issueLimitReport(w, r, false)
	case path == "/issue/limit/adf/report":
		h.issueLimitReport(w, r, true)
	case path == "/issue/properties" || strings.HasPrefix(path, "/issue/properties/"):
		h.issuePropertiesBulkRoute(w, r, path)
	case path == "/issues/archive/export":
		h.exportArchivedIssues(w, r)
	case path == "/changelog/bulkfetch":
		h.bulkChangelogs(w, r)
	case path == "/events":
		h.issueEvents(w, r)
	case path == "/redact":
		h.redactRoute(w, r, "")
	case strings.HasPrefix(path, "/redact/status/"):
		h.redactRoute(w, r, strings.TrimPrefix(path, "/redact/status/"))
	case path == "/forge/panel/action/bulk/async":
		h.issuePanelPins(w, r)
	case strings.HasPrefix(path, "/issue/"):
		parts := strings.Split(strings.TrimPrefix(path, "/issue/"), "/")
		h.issueRoute(w, r, parts)
	default:
		jiraError(w, http.StatusNotFound, fmt.Sprintf("No resource found for path %s", r.URL.Path))
	}
}

func (h *Handler) issueRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	idOrKey := parts[0]
	switch {
	case len(parts) == 1 && idOrKey == "createmeta" && r.Method == http.MethodGet:
		h.createMeta(w, r)
		return
	case len(parts) == 3 && idOrKey == "createmeta" && parts[2] == "issuetypes" && r.Method == http.MethodGet:
		h.createMetaIssueTypes(w, r, parts[1])
		return
	case len(parts) == 4 && idOrKey == "createmeta" && parts[2] == "issuetypes" && r.Method == http.MethodGet:
		h.createMetaFields(w, r, parts[1], parts[3])
		return
	case len(parts) == 2 && parts[1] == "properties":
		h.issueProperties(w, r, idOrKey, nil)
	case len(parts) >= 3 && parts[1] == "properties":
		propertyKey := strings.Join(parts[2:], "/")
		h.issueProperties(w, r, idOrKey, &propertyKey)
	case len(parts) == 1:
		switch r.Method {
		case http.MethodGet:
			h.getIssue(w, r, idOrKey)
		case http.MethodPut:
			h.putIssue(w, r, idOrKey)
		case http.MethodDelete:
			h.deleteIssue(w, r, idOrKey)
		default:
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case len(parts) == 2 && parts[1] == "comment":
		h.issueCommentRoute(w, r, idOrKey, "")
	case len(parts) == 3 && parts[1] == "comment":
		h.issueCommentRoute(w, r, idOrKey, parts[2])
	case len(parts) == 2 && parts[1] == "remotelink":
		h.remoteLinkRoute(w, r, idOrKey, "")
	case len(parts) == 3 && parts[1] == "remotelink":
		h.remoteLinkRoute(w, r, idOrKey, parts[2])
	case len(parts) == 2 && parts[1] == "transitions" && r.Method == http.MethodGet:
		h.listTransitions(w, r, idOrKey)
	case len(parts) == 2 && parts[1] == "transitions" && r.Method == http.MethodPost:
		h.performTransition(w, r, idOrKey)
	case len(parts) >= 2 && parts[1] == "worklog":
		h.issueWorklogRoute(w, r, idOrKey, parts[2:])
	case len(parts) == 2 && parts[1] == "assignee" && r.Method == http.MethodPut:
		h.putAssignee(w, r, idOrKey)
	case len(parts) == 2 && parts[1] == "attachments" && r.Method == http.MethodPost:
		h.uploadAttachments(w, r, idOrKey)
	case len(parts) == 2 && parts[1] == "watchers":
		h.issueWatchers(w, r, idOrKey)
	case len(parts) == 2 && parts[1] == "votes":
		h.issueVotes(w, r, idOrKey)
	case len(parts) == 2 && parts[1] == "changelog" && r.Method == http.MethodGet:
		h.issueChangelog(w, r, idOrKey)
	case len(parts) == 3 && parts[1] == "changelog" && parts[2] == "list" && r.Method == http.MethodPost:
		h.changelogByIDs(w, r, idOrKey)
	case len(parts) == 2 && parts[1] == "notify":
		h.notifyIssue(w, r, idOrKey)
	case len(parts) == 2 && parts[1] == "editmeta" && r.Method == http.MethodGet:
		h.editMeta(w, r, idOrKey)
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

// ---- auth helpers ----

type jerr struct {
	status      int
	message     string
	fieldErrors map[string]string
}

func (h *Handler) authWorkspace(r *http.Request) (wsID, userID string, j *jerr) {
	userID, err := authn.Identify(r.Context(), h.Store, r)
	if err != nil {
		return "", "", &jerr{http.StatusUnauthorized, "You are not authenticated. Authentication required to perform this operation.", nil}
	}
	if h.WorkspaceSlug == "" {
		return "", "", &jerr{http.StatusInternalServerError, "workspace is not configured", nil}
	}
	wsID, err = h.Store.WorkspaceBySlug(r.Context(), h.WorkspaceSlug)
	if err != nil {
		return "", "", &jerr{http.StatusInternalServerError, "no workspace configured", nil}
	}
	ok, err := authz.CanSeeWorkspace(r.Context(), h.Store, wsID, userID)
	if err != nil || !ok {
		return "", "", &jerr{http.StatusForbidden, "You do not have permission to perform this operation.", nil}
	}
	return wsID, userID, nil
}

// authWorkspaceAdmin is the explicit gate for workspace control-plane
// operations. Regular members may use project data; changing shared
// configuration requires the workspace administrator role.
func (h *Handler) authWorkspaceAdmin(r *http.Request) (wsID, userID string, j *jerr) {
	wsID, userID, j = h.authWorkspace(r)
	if j != nil {
		return "", "", j
	}
	admin, err := authz.IsWorkspaceAdmin(r.Context(), h.Store, wsID, userID)
	if err != nil {
		return "", "", &jerr{http.StatusInternalServerError, "internal error", nil}
	}
	if !admin {
		return "", "", &jerr{http.StatusForbidden, "You do not have permission to perform this operation.", nil}
	}
	return wsID, userID, nil
}

func (h *Handler) resolveIssue(r *http.Request, wsID, idOrKey string) (*models.Issue, *jerr) {
	issue, err := h.Store.IssueByIDOrKey(r.Context(), wsID, idOrKey)
	if err != nil {
		return nil, &jerr{http.StatusNotFound, "Issue does not exist or you do not have permission to see it.", nil}
	}
	userID, err := authn.Identify(r.Context(), h.Store, r)
	if err != nil {
		return nil, &jerr{http.StatusUnauthorized, "You are not authenticated. Authentication required to perform this operation.", nil}
	}
	visible, err := authz.CanSeeIssue(r.Context(), h.Store, wsID, issue.ProjectID, userID, issue.ID, issue.SecurityLevelID)
	if err != nil {
		return nil, &jerr{http.StatusInternalServerError, "internal error", nil}
	}
	if !visible {
		return nil, &jerr{http.StatusNotFound, "Issue does not exist or you do not have permission to see it.", nil}
	}
	return issue, nil
}

func (h *Handler) statusBean(s models.Status) map[string]any {
	return map[string]any{
		"self":           h.BaseURL + "/rest/api/3/status/" + statusWireID(s),
		"id":             statusWireID(s),
		"name":           s.Name,
		"description":    s.Description,
		"statusCategory": statusCategoryBean(s.Category),
	}
}

func writeJerr(w http.ResponseWriter, e *jerr) {
	if e.fieldErrors != nil {
		jiraFieldError(w, e.status, e.fieldErrors)
		return
	}
	jiraError(w, e.status, e.message)
}

// ---- serverInfo / myself ----

func (h *Handler) myself(w http.ResponseWriter, r *http.Request) {
	userID, err := authn.Identify(r.Context(), h.Store, r)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="zzira"`)
		jiraError(w, http.StatusUnauthorized, "You are not authenticated. Authentication required to perform this operation.")
		return
	}
	u, err := h.Store.UserByID(r.Context(), userID)
	if err != nil {
		jiraError(w, http.StatusUnauthorized, "You are not authenticated. Authentication required to perform this operation.")
		return
	}
	bean := h.fullUserBean(u, true)
	bean["expand"] = "groups,applicationRoles"
	bean["locale"] = acceptLanguageLocale(r)
	if workspaceID, _, e := h.authWorkspace(r); e == nil {
		if locale, err := h.Store.UserPreference(r.Context(), workspaceID, userID, store.UserPreferenceLocaleKey); err == nil {
			bean["locale"] = locale
		}
		h.userExpansions(r, workspaceID, u, bean)
	}
	writeJSON(w, http.StatusOK, bean)
}

func (h *Handler) userBean(u *models.User) map[string]any {
	accountType := "atlassian"
	if strings.HasPrefix(u.ID, "app_") {
		accountType = "app"
	}
	return map[string]any{
		"accountId":    u.ID,
		"emailAddress": u.Email,
		"displayName":  u.DisplayName,
		"active":       u.Active,
		"timeZone":     u.TimeZone,
		"accountType":  accountType,
		"avatarUrls": map[string]string{
			"48x48": h.BaseURL + "/static/img/avatar-default.svg",
			"24x24": h.BaseURL + "/static/img/avatar-default.svg",
		},
	}
}

// ---- issue create/get/update/delete ----

type createIssueRequest struct {
	Update json.RawMessage `json:"update"`
	Fields struct {
		Project *struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		} `json:"project"`
		Summary     string          `json:"summary"`
		Description json.RawMessage `json:"description"`
		IssueType   *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"issuetype"`
		Parent *struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		} `json:"parent"`
		Priority *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"priority"`
		Assignee *struct {
			AccountID string `json:"accountId"`
		} `json:"assignee"`
		Security *struct {
			ID string `json:"id"`
		} `json:"security"`
		Labels *[]string `json:"labels"`
	} `json:"fields"`
}

func (h *Handler) createIssue(w http.ResponseWriter, r *http.Request) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		if e.status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", `Basic realm="zzira"`)
		}
		writeJerr(w, e)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"fields": "Invalid request payload."})
		return
	}
	var req createIssueRequest
	if err := json.Unmarshal(body, &req); err != nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"fields": "Invalid request payload."})
		return
	}
	if fieldErrors := unsupportedCreateFields(body); len(fieldErrors) > 0 {
		jiraFieldError(w, http.StatusBadRequest, fieldErrors)
		return
	}
	if len(req.Update) > 0 && string(req.Update) != "null" && string(req.Update) != "{}" {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"update": "The update operations object is not supported when creating an issue."})
		return
	}
	if req.Fields.Project == nil || (req.Fields.Project.Key == "" && req.Fields.Project.ID == "") {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"project": "Project key or id is required."})
		return
	}
	if req.Fields.Summary == "" {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"summary": "You must specify a summary of the issue."})
		return
	}
	if req.Fields.IssueType == nil || (req.Fields.IssueType.ID == "" && req.Fields.IssueType.Name == "") {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"issuetype": "Issue type is required."})
		return
	}
	issueTypeID := req.Fields.IssueType.ID
	if issueTypeID == "" {
		issueTypeID = req.Fields.IssueType.Name
	}
	priorityID := ""
	if req.Fields.Priority != nil {
		priorityID = req.Fields.Priority.ID
		if priorityID == "" {
			priorityID = req.Fields.Priority.Name
		}
	}
	assigneeID := ""
	var rawRequest struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal(body, &rawRequest); err != nil {
		jiraError(w, 400, "Invalid issue fields.")
		return
	}
	_, assigneeProvided := rawRequest.Fields["assignee"]
	if req.Fields.Assignee != nil {
		assigneeID = req.Fields.Assignee.AccountID
	}
	labels := []string{}
	if req.Fields.Labels != nil {
		labels = *req.Fields.Labels
	}
	projectIDOrKey := req.Fields.Project.Key
	if projectIDOrKey == "" {
		projectIDOrKey = req.Fields.Project.ID
	}
	customFields, err := h.resolveCustomFieldAliases(r.Context(), wsID, customFieldsFromBody(body))
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	issue, _, err := h.Commands.CreateIssue(r.Context(), commands.CreateIssueInput{
		ActorID:        userID,
		WorkspaceID:    wsID,
		ProjectIDOrKey: projectIDOrKey,
		Summary:        req.Fields.Summary,
		DescriptionADF: req.Fields.Description,
		IssueTypeID:    issueTypeID,
		ParentIDOrKey: func() string {
			if req.Fields.Parent == nil {
				return ""
			}
			if req.Fields.Parent.Key != "" {
				return req.Fields.Parent.Key
			}
			return req.Fields.Parent.ID
		}(),
		PriorityID:                priorityID,
		AssigneeID:                assigneeID,
		UseProjectDefaultAssignee: !assigneeProvided || assigneeID == "-1",
		SecurityLevelID: func() string {
			if req.Fields.Security == nil {
				return ""
			}
			return req.Fields.Security.ID
		}(),
		Labels: labels,
		Fields: customFields,
	})
	if err != nil {
		jiraFieldError(w, http.StatusBadRequest, createIssueFieldError(err))
		return
	}
	var extras struct {
		Properties []entityPropertyInput `json:"properties"`
		Transition *struct {
			ID string `json:"id"`
		} `json:"transition"`
	}
	_ = json.Unmarshal(body, &extras)
	for _, property := range extras.Properties {
		if _, err = h.Store.SetIssueProperty(r.Context(), userID, issue.ID, property.Key, property.Value); err != nil {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"properties": "The property " + property.Key + " is invalid."})
			return
		}
	}
	created := map[string]any{
		"id":   jiraIssueID(issue),
		"key":  issue.Key,
		"self": h.BaseURL + "/rest/api/3/issue/" + jiraIssueID(issue),
	}
	if extras.Transition != nil && extras.Transition.ID != "" {
		result := map[string]any{"status": http.StatusNoContent, "errorCollection": map[string]any{"errorMessages": []string{}, "errors": map[string]string{}}}
		if _, _, transitionErr := h.Commands.TransitionIssueWithUpdateFromAPI(r.Context(), userID, wsID, issue.ID, extras.Transition.ID, store.IssueUpdate{}); transitionErr != nil {
			result = map[string]any{"status": http.StatusBadRequest, "errorCollection": map[string]any{"errorMessages": []string{transitionErr.Error()}, "errors": map[string]string{}}}
		}
		created["transition"] = result
	}
	writeJSON(w, http.StatusCreated, created)
}

func unsupportedCreateFields(body []byte) map[string]string {
	var raw struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	if json.Unmarshal(body, &raw) != nil {
		return nil
	}
	supported := map[string]struct{}{
		"project": {}, "summary": {}, "description": {}, "issuetype": {}, "priority": {},
		"assignee": {}, "security": {}, "labels": {}, "fixVersions": {}, "versions": {}, "components": {}, "parent": {},
	}
	for field := range raw.Fields {
		if _, ok := supported[field]; ok || customFieldIDPattern.MatchString(field) || appCustomFieldKeyPattern.MatchString(field) {
			continue
		}
		return map[string]string{field: "Field is not available on the create screen."}
	}
	return nil
}

func createIssueFieldError(err error) map[string]string {
	message := err.Error()
	for _, field := range []string{"summary", "project", "priority", "assignee", "security", "labels", "description", "components", "parent"} {
		if strings.Contains(message, field) {
			return map[string]string{field: message}
		}
	}
	if strings.Contains(message, "issue type") {
		return map[string]string{"issuetype": message}
	}
	if match := customFieldInMessagePattern.FindString(message); match != "" {
		return map[string]string{match: message}
	}
	return map[string]string{"fields": message}
}

type putIssueRequest struct {
	Update map[string][]map[string]json.RawMessage `json:"update"`
	Fields struct {
		Summary     *string         `json:"summary"`
		Description json.RawMessage `json:"description"`
		Priority    *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"priority"`
		Assignee *json.RawMessage `json:"assignee"`
		Security *struct {
			ID string `json:"id"`
		} `json:"security"`
		Labels *[]string `json:"labels"`
	} `json:"fields"`
}

func (h *Handler) putIssue(w http.ResponseWriter, r *http.Request, idOrKey string) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	current, e := h.resolveIssue(r, wsID, idOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	for _, flag := range []string{"overrideScreenSecurity", "overrideEditableFlag"} {
		if strings.EqualFold(r.URL.Query().Get(flag), "true") {
			if admin, adminErr := h.Store.IsAdmin(r.Context(), wsID, userID); adminErr != nil || !admin {
				jiraError(w, http.StatusForbidden, "Only administrators can use "+flag+".")
				return
			}
		}
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"fields": "Invalid request payload."})
		return
	}
	var issueProperties struct {
		Properties []entityPropertyInput `json:"properties"`
	}
	_ = json.Unmarshal(body, &issueProperties)
	for _, property := range issueProperties.Properties {
		if strings.TrimSpace(property.Key) == "" || !validPropertyValue(property.Value) {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"properties": "Each property needs a key and a valid JSON value."})
			return
		}
	}
	var req putIssueRequest
	if err := json.Unmarshal(body, &req); err != nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"fields": "Invalid request payload."})
		return
	}
	up := store.IssueUpdate{Summary: req.Fields.Summary}
	if len(req.Fields.Description) > 0 {
		up.Description = req.Fields.Description
	}
	if req.Fields.Priority != nil {
		// Jira accepts either wire form. Reading only the id silently cleared
		// the priority when a client sent a name.
		p := req.Fields.Priority.ID
		if p == "" {
			p = req.Fields.Priority.Name
		}
		up.PriorityID = &p
	}
	if req.Fields.Assignee != nil {
		var a *struct {
			AccountID string `json:"accountId"`
		}
		if string(*req.Fields.Assignee) == "null" {
			empty := ""
			up.AssigneeID = &empty // explicit null = unassign
		} else if err := json.Unmarshal(*req.Fields.Assignee, &a); err != nil || a == nil {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"assignee": "Invalid assignee payload."})
			return
		} else {
			up.AssigneeID = &a.AccountID
		}
	}
	fields, err := h.resolveCustomFieldAliases(r.Context(), wsID, customFieldsFromBody(body))
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	var rawFields struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	_ = json.Unmarshal(body, &rawFields)
	var parentIDOrKey *string
	if rawParent, provided := rawFields.Fields["parent"]; provided {
		value := ""
		if string(rawParent) != "null" {
			var parent struct {
				ID  string `json:"id"`
				Key string `json:"key"`
			}
			if err := json.Unmarshal(rawParent, &parent); err != nil || (parent.ID == "" && parent.Key == "") {
				jiraFieldError(w, http.StatusBadRequest, map[string]string{"parent": "Parent requires an issue id or key."})
				return
			}
			value = parent.Key
			if value == "" {
				value = parent.ID
			}
		}
		parentIDOrKey = &value
	}
	var securityID *string
	if req.Fields.Security != nil {
		sid := req.Fields.Security.ID
		securityID = &sid
	}
	if _, _, err := h.Commands.UpdateIssue(r.Context(), commands.UpdateIssueInput{
		ActorID: userID, WorkspaceID: wsID, IssueIDOrKey: idOrKey,
		Summary: up.Summary, Description: up.Description,
		PriorityID: up.PriorityID, AssigneeID: up.AssigneeID,
		ParentIDOrKey:   parentIDOrKey,
		SecurityLevelID: securityID, Labels: req.Fields.Labels, Fields: fields, VersionOperations: req.Update,
	}); err != nil {
		field := "fields"
		if strings.Contains(err.Error(), "parent") || strings.Contains(err.Error(), "sub-task") {
			field = "parent"
		}
		jiraFieldError(w, http.StatusBadRequest, map[string]string{field: err.Error()})
		return
	}
	for _, property := range issueProperties.Properties {
		if _, err := h.Store.SetIssueProperty(r.Context(), userID, current.ID, property.Key, property.Value); err != nil {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"properties": "The property " + property.Key + " is invalid."})
			return
		}
	}
	if strings.EqualFold(r.URL.Query().Get("returnIssue"), "true") {
		h.getIssue(w, r, current.ID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) deleteIssue(w http.ResponseWriter, r *http.Request, idOrKey string) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	issue, e := h.resolveIssue(r, wsID, idOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	children, err := h.Store.ChildIssues(r.Context(), wsID, issue.ID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	subtasks := children[:0]
	for _, child := range children {
		if child.IssueType.Subtask {
			subtasks = append(subtasks, child)
		}
	}
	if len(subtasks) > 0 && !strings.EqualFold(r.URL.Query().Get("deleteSubtasks"), "true") {
		jiraError(w, http.StatusBadRequest, "The issue has subtasks. To delete it, set deleteSubtasks to true.")
		return
	}
	for _, subtask := range subtasks {
		if _, err = h.Commands.DeleteIssue(r.Context(), userID, wsID, subtask.ID, "deleted with its parent via API"); err != nil {
			issueCommandError(w, err)
			return
		}
	}
	if _, err = h.Commands.DeleteIssue(r.Context(), userID, wsID, issue.ID, "deleted via API"); err != nil {
		issueCommandError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) getIssue(w http.ResponseWriter, r *http.Request, idOrKey string) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		if e.status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", `Basic realm="zzira"`)
		}
		writeJerr(w, e)
		return
	}
	issue, e := h.resolveIssue(r, wsID, idOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	q := r.URL.Query()
	options := searchOptions{Fields: splitSearchValues(q["fields"]), Expand: []string{q.Get("expand")}, Properties: splitSearchValues(q["properties"]), IssueDetails: true}
	for name, target := range map[string]*bool{"fieldsByKeys": &options.FieldsByKeys, "failFast": &options.FailFast} {
		if raw := q.Get(name); raw != "" {
			value, err := strconv.ParseBool(raw)
			if err != nil {
				jiraError(w, http.StatusBadRequest, name+" must be true or false.")
				return
			}
			*target = value
		}
	}
	if e := validateSearchOptions(&options); e != nil {
		writeJerr(w, e)
		return
	}
	customFields, err := h.Store.CustomFieldsForWorkspace(r.Context(), wsID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	definitions := searchFieldDefinitions(customFields)
	beans, err := h.searchIssueBeans(r.Context(), wsID, userID, []*models.Issue{issue}, options, len(options.Fields) == 0, definitions)
	if err != nil || len(beans) != 1 {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	bean := beans[0]
	if fields, ok := bean["fields"].(map[string]any); ok && issue.SecurityLevelID != "" {
		if _, wanted := fields["security"]; wanted || len(options.Fields) == 0 {
			if name := h.Store.SecurityLevelName(r.Context(), issue.ProjectID, issue.SecurityLevelID); name != "" {
				fields["security"] = map[string]any{"id": issue.SecurityLevelID, "name": name}
			}
		}
	}
	requested := normalizeSearchFields(options.Fields, definitions, options.FieldsByKeys)
	names, schemas := searchFieldMetadata(requested, len(options.Fields) == 0, options.FieldsByKeys, definitions)
	if hasSearchExpand(options, "names") {
		bean["names"] = names
	}
	if hasSearchExpand(options, "schema") {
		bean["schema"] = schemas
	}
	if _, ok := bean["expand"]; !ok {
		bean["expand"] = "renderedFields,names,schema,operations,editmeta,changelog,versionedRepresentations"
	}
	if strings.EqualFold(q.Get("updateHistory"), "true") {
		if err = h.Store.RecordIssueView(r.Context(), wsID, userID, issue.ID); err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	writeJSON(w, http.StatusOK, bean)
}

// issueBean renders the Jira IssueBean (V3 subset). Field keys match the
// published contract; golden tests lock them.
// IssueBean is exported for the Agile edge, which must render identical beans.
func (h *Handler) IssueBean(i *models.Issue) map[string]any { return h.issueBean(i) }

func (h *Handler) issueBean(i *models.Issue) map[string]any {
	fields := map[string]any{
		"summary":     i.Summary,
		"description": i.Description,
		"labels":      i.Labels,
		"fixVersions": []any{},
		"versions":    []any{},
		"created":     issueCreated(i),
		"updated":     i.UpdatedAt,
		"project": map[string]any{
			"id":   i.ProjectID,
			"key":  projectKeyOf(i),
			"name": projectKeyOf(i),
		},
		"status": map[string]any{
			"name":           i.Status.Name,
			"id":             statusWireID(i.Status),
			"self":           h.BaseURL + "/rest/api/3/status/" + statusWireID(i.Status),
			"statusCategory": statusCategoryBean(i.Status.Category),
		},
		"issuetype": h.issueTypeBean(i.IssueType),
	}
	if i.Parent != nil {
		parentID := strconv.FormatInt(i.Parent.JiraID, 10)
		fields["parent"] = map[string]any{
			"id": parentID, "key": i.Parent.Key,
			"self":   h.BaseURL + "/rest/api/3/issue/" + parentID,
			"fields": map[string]any{"summary": i.Parent.Summary},
		}
	}
	if i.Priority != nil {
		fields["priority"] = h.priorityBean(*i.Priority)
	}
	// Jira always carries both fields: null and absent until the issue is resolved.
	fields["resolution"] = nil
	fields["resolutiondate"] = nil
	if i.Resolution != nil {
		fields["resolution"] = h.resolutionBean(*i.Resolution)
		if i.ResolvedAt != "" {
			fields["resolutiondate"] = i.ResolvedAt
		}
	}
	if i.Assignee != nil {
		fields["assignee"] = h.userBean(i.Assignee)
	}
	if i.Reporter != nil {
		fields["reporter"] = h.userBean(i.Reporter)
	}
	for k, v := range i.Fields {
		fields[k] = v
	}
	for _, field := range []string{"fixVersions", "versions"} {
		values := []map[string]any{}
		for _, version := range i.VersionRefs(field) {
			version.ProjectID = i.ProjectID
			values = append(values, h.versionBean(&version))
		}
		fields[field] = values
	}
	return map[string]any{
		"expand": "renderedFields,names,schema,operations,editmeta,changelog,versionedRepresentations",
		"id":     jiraIssueID(i),
		"self":   h.BaseURL + "/rest/api/3/issue/" + jiraIssueID(i),
		"key":    i.Key,
		"fields": fields,
	}
}

// statusWireID is the id clients know a status by.
func statusWireID(status models.Status) string {
	if status.JiraID != 0 {
		return strconv.FormatInt(status.JiraID, 10)
	}
	return status.ID
}

// issueCreated is when an issue was created, falling back to its update time
// for issues built without one, such as those replayed on a client.
func issueCreated(issue *models.Issue) string {
	if issue.CreatedAt != "" {
		return issue.CreatedAt
	}
	return issue.UpdatedAt
}

func jiraIssueID(issue *models.Issue) string {
	return strconv.FormatInt(issue.JiraID, 10)
}

func statusCategoryBean(category string) map[string]any {
	name := category
	id := 2
	color := "blue-gray"
	switch category {
	case "new":
		name = "To Do"
	case "indeterminate":
		name = "In Progress"
		id = 4
		color = "yellow"
	case "done":
		name = "Done"
		id = 3
		color = "green"
	}
	return map[string]any{"id": id, "key": category, "name": name, "colorName": color}
}

func projectKeyOf(i *models.Issue) string {
	if idx := strings.IndexByte(i.Key, '-'); idx > 0 {
		return i.Key[:idx]
	}
	return i.Key
}

// ---- comments ----

type addCommentRequest struct {
	Body json.RawMessage `json:"body"`
}

// ---- transitions ----

func (h *Handler) listTransitions(w http.ResponseWriter, r *http.Request, idOrKey string) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	issue, e := h.resolveIssue(r, wsID, idOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	beans, err := h.issueTransitionBeans(r.Context(), wsID, userID, issue)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	q := r.URL.Query()
	withFields := strings.Contains(q.Get("expand"), "transitions.fields")
	filtered := []map[string]any{}
	for _, bean := range beans {
		if id := q.Get("transitionId"); id != "" && bean["id"] != id {
			continue
		}
		if !withFields {
			delete(bean, "fields")
		}
		filtered = append(filtered, bean)
	}
	if strings.EqualFold(q.Get("sortByOpsBarAndStatus"), "true") {
		sort.SliceStable(filtered, func(i, j int) bool {
			left, _ := filtered[i]["to"].(map[string]any)
			right, _ := filtered[j]["to"].(map[string]any)
			return statusCategoryOrder(left) < statusCategoryOrder(right)
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"expand":      "transitions",
		"transitions": filtered,
	})
}

func (h *Handler) issueTransitionBeans(ctx context.Context, workspaceID, userID string, issue *models.Issue) ([]map[string]any, error) {
	wf, err := h.Store.WorkflowForProjectAndIssueType(ctx, issue.ProjectID, issue.IssueType.ID)
	if err != nil {
		return nil, err
	}
	evaluation := workflow.ContextForIssue(userID, issue)
	evaluation.IsAPI = true
	evaluation.StatusHistory, err = h.Store.IssueStatusHistory(ctx, workspaceID, issue.ID)
	if err != nil {
		return nil, err
	}
	evaluation.Transitions, err = h.Store.IssueTransitionHistory(ctx, workspaceID, issue.ID)
	if err != nil {
		return nil, err
	}
	evaluation.ParentStatus, evaluation.ChildStatuses, err = h.Store.IssueHierarchyStatuses(ctx, workspaceID, issue.ID)
	if err != nil {
		return nil, err
	}
	beans := []map[string]any{}
	for _, transition := range wf.AvailableFor(issue.Status.ID, evaluation) {
		status, err := h.Store.StatusByIDForProject(ctx, transition.To, issue.ProjectID)
		if err != nil {
			return nil, err
		}
		fields := map[string]any{}
		required := transition.RequiredFields()
		for _, field := range transition.ScreenFields() {
			fields[field] = transitionFieldMetadata(field, required[field])
		}
		beans = append(beans, map[string]any{
			"id": transition.ID, "name": transition.Name, "to": h.statusBean(status),
			"hasScreen": transition.Screen != nil, "isGlobal": false, "isInitial": false,
			"isConditional": transition.Conditions != nil, "isAvailable": true, "fields": fields,
		})
	}
	return beans, nil
}

func (h *Handler) performTransition(w http.ResponseWriter, r *http.Request, idOrKey string) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	var req struct {
		Transition struct {
			ID string `json:"id"`
		} `json:"transition"`
		Fields     map[string]json.RawMessage `json:"fields"`
		Properties []entityPropertyInput      `json:"properties"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Transition.ID == "" {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"transition": "Transition id is required."})
		return
	}
	resolvedFields, err := h.resolveCustomFieldAliases(r.Context(), wsID, req.Fields)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	update, fieldErrors := transitionIssueUpdate(resolvedFields)
	if len(fieldErrors) > 0 {
		jiraFieldError(w, http.StatusBadRequest, fieldErrors)
		return
	}
	transitioned, _, err := h.Commands.TransitionIssueWithUpdateFromAPI(r.Context(), userID, wsID, idOrKey, req.Transition.ID, update)
	if err != nil {
		issueCommandError(w, err)
		return
	}
	for _, property := range req.Properties {
		if _, err = h.Store.SetIssueProperty(r.Context(), userID, transitioned.ID, property.Key, property.Value); err != nil {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"properties": "The property " + property.Key + " is invalid."})
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// statusCategoryOrder ranks a status by category: to do, in progress, done.
func statusCategoryOrder(status map[string]any) int {
	category, _ := status["statusCategory"].(map[string]any)
	switch category["key"] {
	case "new":
		return 0
	case "indeterminate":
		return 1
	case "done":
		return 2
	}
	return 3
}

func transitionFieldMetadata(field string, required bool) map[string]any {
	name := field
	schema := map[string]any{"type": "string"}
	switch field {
	case "summary":
		name = "Summary"
	case "description":
		name, schema = "Description", map[string]any{"type": "any", "system": "description"}
	case "labels":
		name, schema = "Labels", map[string]any{"type": "array", "items": "string", "system": "labels"}
	case "assignee":
		name, schema = "Assignee", map[string]any{"type": "user", "system": "assignee"}
	case "priority":
		name, schema = "Priority", map[string]any{"type": "priority", "system": "priority"}
	}
	return map[string]any{"required": required, "name": name, "schema": schema, "operations": []string{"set"}}
}

func transitionIssueUpdate(fields map[string]json.RawMessage) (store.IssueUpdate, map[string]string) {
	update := store.IssueUpdate{}
	errors := make(map[string]string)
	for field, raw := range fields {
		switch field {
		case "summary":
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				errors[field] = "Summary must be a string."
			} else {
				update.Summary = &value
			}
		case "description":
			update.Description = raw
		case "labels":
			var value []string
			if err := json.Unmarshal(raw, &value); err != nil {
				errors[field] = "Labels must be an array of strings."
			} else {
				update.Labels = &value
			}
		case "assignee", "priority":
			value := ""
			if string(raw) != "null" {
				var object map[string]any
				if err := json.Unmarshal(raw, &object); err != nil {
					errors[field] = "Field value must be an object or null."
					continue
				}
				key := "id"
				if field == "assignee" {
					key = "accountId"
				}
				value, _ = object[key].(string)
				if value == "" {
					errors[field] = "Field value requires " + key + "."
					continue
				}
			}
			if field == "assignee" {
				update.AssigneeID = &value
			} else {
				update.PriorityID = &value
			}
		default:
			if strings.HasPrefix(field, "customfield_") {
				if update.Fields == nil {
					update.Fields = make(map[string]json.RawMessage)
				}
				update.Fields[field] = raw
			} else {
				errors[field] = "Field is not supported during a transition."
			}
		}
	}
	return update, errors
}

// ---- changelog (derived view of the log) ----

// changelogMetadataID turns a stored priority, resolution or issue type id into
// its numeric id. An id the site no longer has keeps its stored value, since
// the history of a deleted item still has to say what it was.
func (h *Handler) changelogMetadataID(ctx context.Context, workspaceID, field, id string) string {
	if id == "" {
		return id
	}
	switch field {
	case "priority":
		if p, err := h.Store.PriorityInWorkspace(ctx, workspaceID, id); err == nil {
			return jiraIDString(p.JiraID)
		}
	case "resolution":
		if r, err := h.Store.ResolutionInWorkspace(ctx, workspaceID, id); err == nil {
			return jiraIDString(r.JiraID)
		}
	case "issuetype":
		if t, err := h.Store.IssueTypeInWorkspace(ctx, workspaceID, id); err == nil {
			return jiraIDString(t.JiraID)
		}
	}
	return id
}

func (h *Handler) issueChangelogBeans(ctx context.Context, workspaceID, issueID string, newestFirst bool) ([]map[string]any, error) {
	entries, err := h.Store.IssueChangelog(ctx, workspaceID, issueID)
	if err != nil {
		return nil, err
	}
	values := make([]map[string]any, 0, len(entries))
	for index := range entries {
		entry := entries[index]
		if newestFirst {
			entry = entries[len(entries)-1-index]
		}
		items := make([]map[string]any, 0, len(entry.Items))
		for _, item := range entry.Items {
			from, to := item.From, item.To
			// Jira reports a priority, resolution or issue type change by the
			// numeric ids clients know, so the stored ids are translated here.
			switch item.Field {
			case "priority", "resolution", "issuetype":
				from, to = h.changelogMetadataID(ctx, workspaceID, item.Field, from), h.changelogMetadataID(ctx, workspaceID, item.Field, to)
			}
			items = append(items, map[string]any{
				"field": item.Field, "fieldId": item.Field, "fieldtype": item.FieldType,
				"from": from, "fromString": item.FromString,
				"to": to, "toString": item.ToString,
			})
		}
		values = append(values, map[string]any{
			"id": fmt.Sprintf("%d", entry.Seq), "author": h.userBean(entry.Author),
			"created": entry.Created, "items": items,
		})
	}
	return values, nil
}

func (h *Handler) issueChangelogPage(ctx context.Context, workspaceID, issueID string, newestFirst bool) (map[string]any, error) {
	histories, err := h.issueChangelogBeans(ctx, workspaceID, issueID, newestFirst)
	if err != nil {
		return nil, err
	}
	return map[string]any{"startAt": 0, "maxResults": 1000, "total": len(histories), "histories": histories}, nil
}

// ---- editmeta ----

func (h *Handler) editMeta(w http.ResponseWriter, r *http.Request, idOrKey string) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	issue, e := h.resolveIssue(r, wsID, idOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	metadata, err := h.issueEditMetadata(r.Context(), wsID, userID, issue)
	if err != nil {
		versionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, metadata)
}

func (h *Handler) issueEditMetadata(ctx context.Context, workspaceID, userID string, issue *models.Issue) (map[string]any, error) {
	metadata, err := h.Store.IssueCreateMetadata(ctx, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	// The edit form follows the project's edit screen, which falls back to the
	// screen scheme's default screen when no edit screen is mapped.
	editFields, err := h.Store.ResolveScreenFields(ctx, workspaceID, issue.ProjectID, issue.IssueType.ID, "edit")
	if err != nil {
		return nil, err
	}
	fields := map[string]any{}
	for _, project := range metadata.Projects {
		if project.Project.ID != issue.ProjectID {
			continue
		}
		if len(editFields) > 0 {
			project.ScreenFields = map[string][]string{issue.IssueType.ID: editFields}
		}
		behaviour, behaviourErr := h.Store.ResolveFieldBehaviour(ctx, workspaceID, issue.ProjectID, issue.IssueType.ID)
		if behaviourErr != nil {
			return nil, behaviourErr
		}
		project.FieldBehaviour = map[string]map[string]models.FieldBehaviour{issue.IssueType.ID: behaviour}
		for _, source := range project.FieldsForIssueType(issue.IssueType.ID) {
			if source.ID == "project" || source.ID == "issuetype" {
				continue
			}
			field := source
			if field.ID == "parent" {
				field.Required = issue.IssueType.Subtask
			}
			bean := h.legacyCreateFieldBean(field)
			if field.Type == "array" || field.Type == "versions" {
				bean["operations"] = []string{"set", "add", "remove"}
			}
			fields[field.ID] = bean
		}
		break
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("edit metadata project is unavailable")
	}
	return map[string]any{"fields": fields}, nil
}

// ---- shared helpers ----

func jiraError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"errorMessages": []string{message}})
}

func jiraFieldError(w http.ResponseWriter, status int, errors map[string]string) {
	writeJSON(w, status, map[string]any{
		"errorMessages": []string{},
		"errors":        errors,
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("api3: encode response: %v", err)
	}
}
