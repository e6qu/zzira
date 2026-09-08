package web

import (
	"net/http"
	"net/url"
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

func appModuleContextValues(r *http.Request) url.Values {
	values := url.Values{}
	if issueKey := strings.TrimSpace(r.URL.Query().Get("issueKey")); issueKey != "" {
		values.Set("issue.key", issueKey)
	} else if issueKey := strings.TrimSpace(r.URL.Query().Get("issue.key")); issueKey != "" {
		values.Set("issue.key", issueKey)
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
	return values
}
