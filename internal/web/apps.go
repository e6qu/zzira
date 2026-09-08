package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	appRuntime "github.com/e6qu/zzira/internal/apps"
	"github.com/e6qu/zzira/internal/models"
)

type appModulePageData struct {
	Module   *models.AppModule
	FrameURL string
}

type projectAppModulePageData struct {
	Project  *models.Project
	Module   *models.AppModule
	FrameURL string
}

func (h *Handler) AppModulePage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	module, err := h.Store.ActiveAppModule(r.Context(), workspaceID, r.PathValue("module"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.writeWorkspacePage(w, r, "page_app_module", user, workspaceID, appModulePageData{Module: module, FrameURL: remoteModuleFramePath(module, appModuleContextValues(r))}, "app-module:"+module.ID, "")
}

func (h *Handler) ProjectAppModulePage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, r.PathValue("key"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	module, err := h.Store.ActiveAppModule(r.Context(), workspaceID, r.PathValue("module"))
	if err != nil || module.Location != "jira.project.page" {
		http.NotFound(w, r)
		return
	}
	contextValues := url.Values{"project.key": {project.Key}, "project.id": {project.ID}}
	h.writeWorkspacePage(w, r, "page_project_app_module", user, workspaceID, projectAppModulePageData{
		Project: project, Module: module, FrameURL: remoteModuleFramePath(module, contextValues),
	}, "project-app-module:"+module.ID, project.ID)
}

func (h *Handler) ProjectAdminAppModulePage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, r.PathValue("key"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	module, err := h.Store.ActiveAppModule(r.Context(), workspaceID, r.PathValue("module"))
	if err != nil || module.Location != "jira.project.settings" {
		http.NotFound(w, r)
		return
	}
	contextValues := url.Values{"project.key": {project.Key}, "project.id": {project.ID}}
	h.writeWorkspacePage(w, r, "page_project_admin_app_module", user, workspaceID, projectAppModulePageData{
		Project: project, Module: module, FrameURL: remoteModuleFramePath(module, contextValues),
	}, "project-admin-app-module:"+module.ID, project.ID)
}

func remoteModuleFramePath(module *models.AppModule, values url.Values) string {
	if module == nil || module.RemoteURL == "" {
		return ""
	}
	path := "/app-modules/" + module.ID + "/frame"
	if len(values) > 0 {
		path += "?" + values.Encode()
	}
	return path
}

func (h *Handler) AppModuleFrame(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	module, err := h.Store.ActiveAppModule(r.Context(), workspaceID, r.PathValue("module"))
	if err != nil || module.RemoteURL == "" {
		http.NotFound(w, r)
		return
	}
	if h.ProviderSecrets == nil {
		http.Error(w, "App credential encryption is unavailable", http.StatusServiceUnavailable)
		return
	}
	secret, err := h.ProviderSecrets.Open(module.SecretCiphertext, workspaceID+"/"+module.AppKey)
	if err != nil {
		http.Error(w, "App credentials are unavailable", http.StatusServiceUnavailable)
		return
	}
	contextValues := appModuleContextValues(r)
	target, err := appRuntime.ConnectModuleURL(module.BaseURL, module.RemoteURL, h.BaseURL, workspaceID, secret, "zzira-"+module.ID, contextValues, time.Now().UTC())
	if err != nil {
		http.Error(w, "Remote app module is invalid", http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// AppModuleThumbnail resolves a descriptor-owned thumbnail through the same
// authenticated, signed gateway as remote module frames. The route fixes the
// source to validated installation metadata instead of accepting a caller URL.
func (h *Handler) AppModuleThumbnail(w http.ResponseWriter, r *http.Request) {
	h.appModuleAsset(w, r, "thumbnail")
}

// AppModuleIcon delivers the installed project-page icon through the signed
// asset gateway.
func (h *Handler) AppModuleIcon(w http.ResponseWriter, r *http.Request) {
	h.appModuleAsset(w, r, "icon")
}

// AppModuleStatusIcon resolves the issue-specific icon path stored in Jira's
// standard issue-context status property. Callers select the issue, never the
// remote asset URL.
func (h *Handler) AppModuleStatusIcon(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	module, err := h.Store.ActiveAppModule(r.Context(), workspaceID, r.PathValue("module"))
	if err != nil || (module.Type != "jira:issueContext" && module.Type != "jira:issueGlance") {
		http.NotFound(w, r)
		return
	}
	issue, err := h.issueForUser(r, user, workspaceID, strings.TrimSpace(r.URL.Query().Get("issueKey")))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	raw, err := h.Store.IssueProperty(r.Context(), issue.ID, issueContextStatusPropertyKey(*module))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	status := parseIssueContextStatus(raw)
	if status.kind != "icon" || status.iconPath == "" {
		http.NotFound(w, r)
		return
	}
	h.redirectAppModuleAsset(w, r, workspaceID, module, status.iconPath, "status-icon")
}

func (h *Handler) appModuleAsset(w http.ResponseWriter, r *http.Request, kind string) {
	_, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	module, err := h.Store.ActiveAppModule(r.Context(), workspaceID, r.PathValue("module"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	assetURL := ""
	switch kind {
	case "thumbnail":
		if module.Type == "jira:report" || module.Type == "jira:dashboardGadget" {
			assetURL = appModuleThumbnailURL(*module)
		}
	case "icon":
		if module.Type == "jira:projectPage" || module.Type == "jira:issueContext" || module.Type == "jira:issueGlance" {
			assetURL = appModuleIconURL(*module)
		}
	}
	if assetURL == "" {
		http.NotFound(w, r)
		return
	}
	h.redirectAppModuleAsset(w, r, workspaceID, module, assetURL, kind)
}

func (h *Handler) redirectAppModuleAsset(w http.ResponseWriter, r *http.Request, workspaceID string, module *models.AppModule, assetURL, kind string) {
	if !strings.HasPrefix(assetURL, "/") {
		assetURL = "/" + assetURL
	}
	if h.ProviderSecrets == nil {
		http.Error(w, "App credential encryption is unavailable", http.StatusServiceUnavailable)
		return
	}
	secret, err := h.ProviderSecrets.Open(module.SecretCiphertext, workspaceID+"/"+module.AppKey)
	if err != nil {
		http.Error(w, "App credentials are unavailable", http.StatusServiceUnavailable)
		return
	}
	target, err := appRuntime.ConnectModuleURL(module.BaseURL, assetURL, h.BaseURL, workspaceID, secret, "zzira-"+module.ID+"-"+kind, nil, time.Now().UTC())
	if err != nil {
		http.Error(w, "Remote app asset is invalid", http.StatusBadGateway)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target, http.StatusFound)
}

func appModuleIconURL(module models.AppModule) string {
	var metadata struct {
		IconURL string `json:"iconUrl"`
	}
	if json.Unmarshal([]byte(module.Body), &metadata) != nil {
		return ""
	}
	return strings.TrimSpace(metadata.IconURL)
}

func appModuleIconPath(module models.AppModule) string {
	if appModuleIconURL(module) == "" {
		return ""
	}
	return "/app-modules/" + module.ID + "/icon"
}

func decorateIssueContext(module *models.AppModule) {
	if module == nil {
		return
	}
	var metadata struct {
		IconURL string `json:"iconUrl"`
		Label   string `json:"label"`
	}
	if json.Unmarshal([]byte(module.Body), &metadata) != nil {
		return
	}
	module.ContextLabel = strings.TrimSpace(metadata.Label)
	if strings.TrimSpace(metadata.IconURL) != "" {
		module.IconURL = "/app-modules/" + module.ID + "/icon"
	}
}

type issueContextStatus struct {
	kind, label, appearance, iconPath, accessibleLabel string
}

func issueContextStatusPropertyKey(module models.AppModule) string {
	return "com.atlassian.jira.issue:" + module.AppKey + ":" + module.Key + ":status"
}

func parseIssueContextStatus(raw json.RawMessage) issueContextStatus {
	var value struct {
		Type  string `json:"type"`
		Value struct {
			Label string `json:"label"`
			URL   string `json:"url"`
			Type  string `json:"type"`
		} `json:"value"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return issueContextStatus{}
	}
	switch value.Type {
	case "badge":
		count, err := strconv.ParseUint(value.Value.Label, 10, 64)
		if err != nil || count == 0 {
			return issueContextStatus{}
		}
		label := strconv.FormatUint(count, 10)
		if count > 99 {
			label = "99+"
		}
		return issueContextStatus{kind: "badge", label: label, accessibleLabel: strconv.FormatUint(count, 10)}
	case "lozenge":
		label := strings.TrimSpace(value.Value.Label)
		appearances := map[string]string{
			"default": "lozenge-default", "inprogress": "lozenge-current", "moved": "lozenge-moved",
			"new": "lozenge-new", "removed": "lozenge-danger", "success": "lozenge-success",
		}
		class, ok := appearances[value.Value.Type]
		if label == "" || !ok {
			return issueContextStatus{}
		}
		return issueContextStatus{kind: "lozenge", label: label, appearance: class}
	case "icon":
		path := strings.TrimSpace(value.Value.Label)
		if path == "" {
			path = strings.TrimSpace(value.Value.URL)
		}
		parsed, err := url.Parse(path)
		if err != nil || path == "" || parsed.IsAbs() || parsed.Host != "" || strings.HasPrefix(path, "//") {
			return issueContextStatus{}
		}
		return issueContextStatus{kind: "icon", iconPath: path}
	default:
		return issueContextStatus{}
	}
}

func decorateIssueContextStatus(module *models.AppModule, raw json.RawMessage, issueKey string) {
	if module == nil {
		return
	}
	status := parseIssueContextStatus(raw)
	module.ContextStatusType = status.kind
	module.ContextStatusLabel = status.label
	module.ContextStatusClass = status.appearance
	module.ContextStatusAccessibleLabel = status.accessibleLabel
	if status.kind == "icon" {
		module.ContextStatusIconURL = "/app-modules/" + module.ID + "/status-icon?issueKey=" + url.QueryEscape(issueKey)
	}
}

func appModuleThumbnailURL(module models.AppModule) string {
	var metadata struct {
		ThumbnailURL string `json:"thumbnailUrl"`
	}
	if json.Unmarshal([]byte(module.Body), &metadata) != nil {
		return ""
	}
	return strings.TrimSpace(metadata.ThumbnailURL)
}

func appModuleThumbnailPath(module models.AppModule) string {
	if appModuleThumbnailURL(module) == "" {
		return ""
	}
	return "/app-modules/" + module.ID + "/thumbnail"
}

func appModuleContextValues(r *http.Request) url.Values {
	values := url.Values{}
	if issueKey := strings.TrimSpace(r.URL.Query().Get("issueKey")); issueKey != "" {
		values.Set("issue.key", issueKey)
	} else if issueKey := strings.TrimSpace(r.URL.Query().Get("issue.key")); issueKey != "" {
		values.Set("issue.key", issueKey)
	}
	if issueID := strings.TrimSpace(r.URL.Query().Get("issueId")); issueID != "" {
		values.Set("issue.id", issueID)
	} else if issueID := strings.TrimSpace(r.URL.Query().Get("issue.id")); issueID != "" {
		values.Set("issue.id", issueID)
	}
	if pageID := strings.TrimSpace(r.URL.Query().Get("pageId")); pageID != "" {
		values.Set("content.id", pageID)
	} else if pageID := strings.TrimSpace(r.URL.Query().Get("content.id")); pageID != "" {
		values.Set("content.id", pageID)
	}
	if projectKey := strings.TrimSpace(r.URL.Query().Get("projectKey")); projectKey != "" {
		values.Set("project.key", projectKey)
	} else if projectKey := strings.TrimSpace(r.URL.Query().Get("project.key")); projectKey != "" {
		values.Set("project.key", projectKey)
	}
	if projectID := strings.TrimSpace(r.URL.Query().Get("projectId")); projectID != "" {
		values.Set("project.id", projectID)
	} else if projectID := strings.TrimSpace(r.URL.Query().Get("project.id")); projectID != "" {
		values.Set("project.id", projectID)
	}
	for queryKey, contextKey := range map[string]string{"dashboardId": "dashboard.id", "dashboardItemId": "dashboardItem.id", "dashboardItemKey": "dashboardItem.key", "dashboardItemViewType": "dashboardItem.viewType"} {
		if value := strings.TrimSpace(r.URL.Query().Get(queryKey)); value != "" {
			values.Set(contextKey, value)
		} else if value := strings.TrimSpace(r.URL.Query().Get(contextKey)); value != "" {
			values.Set(contextKey, value)
		}
	}
	return values
}
