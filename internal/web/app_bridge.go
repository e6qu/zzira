package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	appRuntime "github.com/e6qu/zzira/internal/apps"
)

type appBridgeRequest struct {
	URL         string `json:"url"`
	Type        string `json:"type"`
	Data        string `json:"data"`
	ContentType string `json:"contentType"`
}

type appBridgeResponse struct {
	Status      int    `json:"status"`
	ContentType string `json:"contentType"`
	Body        string `json:"body"`
}

// AppModuleRequest performs the product API request an app's frame asks for
// with AP.request: as the person using the frame, through the site's own
// routes, and only for operations the app's scopes allow.
func (h *Handler) AppModuleRequest(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	module, err := h.Store.ActiveAppModule(r.Context(), workspaceID, r.PathValue("module"))
	if err != nil || module.RemoteURL == "" {
		http.NotFound(w, r)
		return
	}
	if module.Location == "jira.admin" || module.Location == "jira.project.settings" {
		isAdmin, adminErr := h.Store.IsAdmin(r.Context(), workspaceID, user.ID)
		if adminErr != nil || !isAdmin {
			http.NotFound(w, r)
			return
		}
	}
	installation, err := h.Store.AppInstallation(r.Context(), workspaceID, module.AppKey)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var request appBridgeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil {
		http.Error(w, "The app request is not valid.", http.StatusBadRequest)
		return
	}
	method := strings.ToUpper(strings.TrimSpace(request.Type))
	if method == "" {
		method = http.MethodGet
	}
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		http.Error(w, "The app request method is not supported.", http.StatusBadRequest)
		return
	}
	target, err := url.Parse(strings.TrimSpace(request.URL))
	if err != nil || target.Scheme != "" || target.Host != "" || target.User != nil || !strings.HasPrefix(target.Path, "/") || strings.HasPrefix(target.Path, "//") || strings.ContainsAny(request.URL, "\\\r\n") {
		http.Error(w, "An app can only request this site's APIs by path.", http.StatusBadRequest)
		return
	}
	if err := appRuntime.BridgeRequestAllowed(installation, method, target.Path); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if h.Routes == nil {
		http.Error(w, "App requests are not available.", http.StatusServiceUnavailable)
		return
	}
	var body io.Reader
	if method != http.MethodGet && method != http.MethodDelete {
		body = strings.NewReader(request.Data)
	}
	// The request is served in-process by Routes and never sent over a network;
	// its path was checked against the app's scopes above.
	inner, err := http.NewRequestWithContext(r.Context(), method, target.RequestURI(), body) // #nosec G704 -- in-process request to a scope-checked local API path, served by Routes.ServeHTTP without any network client.
	if err != nil {
		http.Error(w, "The app request is not valid.", http.StatusBadRequest)
		return
	}
	inner.Host, inner.RemoteAddr = r.Host, r.RemoteAddr
	inner.Header.Set("Cookie", r.Header.Get("Cookie"))
	inner.Header.Set("Accept", "application/json")
	inner.Header.Set("X-Atlassian-Token", "no-check")
	if request.ContentType != "" {
		inner.Header.Set("Content-Type", request.ContentType)
	} else if body != nil {
		inner.Header.Set("Content-Type", "application/json")
	}
	response := &inProcessResponse{header: http.Header{}}
	h.Routes.ServeHTTP(response, inner)
	if response.status == 0 {
		response.status = http.StatusOK
	}
	if response.body.Len() > 4<<20 {
		http.Error(w, "The API answer is too large for an app request.", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(appBridgeResponse{Status: response.status, ContentType: response.header.Get("Content-Type"), Body: response.body.String()})
}
