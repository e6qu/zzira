package apps

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/secretbox"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

var storageKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$`)
var requestIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{7,127}$`)
var dynamicWebhookKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9-]{1,100}$`)

type appInstallationContextKey struct{}

type Handler struct {
	Store         *store.Store
	Secrets       *secretbox.Box
	WorkspaceSlug string
	Now           func() time.Time
}

func SignRequest(secret []byte, timestamp int64, requestID, method, requestTarget string, body []byte) string {
	digest := sha256.Sum256(body)
	canonical := strconv.FormatInt(timestamp, 10) + "\n" + requestID + "\n" + strings.ToUpper(method) + "\n" + requestTarget + "\n" + hex.EncodeToString(digest[:])
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

func signedRequestTarget(r *http.Request) string {
	target := r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	return target
}

func (h *Handler) workspaceID(r *http.Request) (string, error) {
	if h.Store == nil || h.WorkspaceSlug == "" {
		return "", fmt.Errorf("app runtime is not configured")
	}
	return h.Store.WorkspaceBySlug(r.Context(), h.WorkspaceSlug)
}

func (h *Handler) authenticate(r *http.Request) (*models.AppInstallation, []byte, int, error) {
	return h.authenticateKey(r, r.PathValue("appKey"))
}

func (h *Handler) authenticateKey(r *http.Request, appKey string) (*models.AppInstallation, []byte, int, error) {
	if h.Secrets == nil {
		return nil, nil, http.StatusServiceUnavailable, fmt.Errorf("app credential encryption is not configured")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (34<<20)+1))
	if err != nil || len(body) > 34<<20 {
		return nil, nil, http.StatusRequestEntityTooLarge, fmt.Errorf("app request body is too large")
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	workspaceID, err := h.workspaceID(r)
	if err != nil {
		return nil, nil, http.StatusServiceUnavailable, err
	}
	installation, err := h.Store.AppInstallation(r.Context(), workspaceID, appKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, http.StatusUnauthorized, fmt.Errorf("unknown app")
	}
	if err != nil {
		return nil, nil, http.StatusInternalServerError, err
	}
	if installation.Status == "uninstalled" {
		return nil, nil, http.StatusUnauthorized, fmt.Errorf("app is uninstalled")
	}
	secret, err := h.Secrets.Open(installation.SecretCiphertext, workspaceID+"/"+installation.Key)
	if err != nil {
		return nil, nil, http.StatusUnauthorized, fmt.Errorf("app credentials are unavailable")
	}
	if token := connectToken(r); token != "" {
		issuer, err := connectIssuer(token)
		if err != nil || issuer != installation.Key {
			return nil, nil, http.StatusUnauthorized, fmt.Errorf("Connect JWT issuer is invalid")
		}
		now := time.Now().UTC()
		if h.Now != nil {
			now = h.Now().UTC()
		}
		if err := verifyConnectJWT(token, secret, r, body, now); err != nil {
			return nil, nil, http.StatusUnauthorized, err
		}
		return installation, body, 0, nil
	}
	timestamp, err := strconv.ParseInt(r.Header.Get("X-Zzira-App-Timestamp"), 10, 64)
	requestID := r.Header.Get("X-Zzira-App-Request-Id")
	provided, err2 := hex.DecodeString(r.Header.Get("X-Zzira-App-Signature"))
	if err != nil || err2 != nil || !requestIDPattern.MatchString(requestID) {
		return nil, nil, http.StatusUnauthorized, fmt.Errorf("signed app headers are invalid")
	}
	now := time.Now().UTC()
	if h.Now != nil {
		now = h.Now().UTC()
	}
	requestTime := time.Unix(timestamp, 0)
	if requestTime.Before(now.Add(-5*time.Minute)) || requestTime.After(now.Add(5*time.Minute)) {
		return nil, nil, http.StatusUnauthorized, fmt.Errorf("app request timestamp is outside the five-minute window")
	}
	expected, _ := hex.DecodeString(SignRequest(secret, timestamp, requestID, r.Method, signedRequestTarget(r), body))
	if !hmac.Equal(provided, expected) {
		return nil, nil, http.StatusUnauthorized, fmt.Errorf("app signature is invalid")
	}
	claimed, err := h.Store.ClaimAppSignedRequest(r.Context(), installation.ID, requestID)
	if err != nil {
		return nil, nil, http.StatusInternalServerError, err
	}
	if !claimed {
		return nil, nil, http.StatusConflict, fmt.Errorf("app request was already processed")
	}
	return installation, body, 0, nil
}

func appAPIScope(r *http.Request) (string, bool) {
	if r.URL.Path == "/rest/atlassian-connect/1/app/module/dynamic" || r.URL.Path == "/wiki/rest/atlassian-connect/1/app/module/dynamic" {
		return "", true
	}
	// Atlassian Connect classifies both reads and recalculation writes for this
	// app-owned resource under its READ scope.
	if r.URL.Path == "/rest/api/3/jql/function/computation" || r.URL.Path == "/rest/api/3/jql/function/computation/search" {
		return "read:jira-work", true
	}
	product := ""
	switch {
	case strings.HasPrefix(r.URL.Path, "/rest/api/"), strings.HasPrefix(r.URL.Path, "/rest/agile/"), strings.HasPrefix(r.URL.Path, "/rest/servicedeskapi/"):
		product = "jira-work"
	case strings.HasPrefix(r.URL.Path, "/wiki/api/"), strings.HasPrefix(r.URL.Path, "/wiki/rest/api/"):
		product = "confluence-content"
	default:
		return "", false
	}
	verb := "write"
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		verb = "read"
	}
	return verb + ":" + product, true
}

// APIPrincipal authenticates a workspace app for the Jira, Agile and
// Confluence REST surfaces, then exposes its stable non-human account through
// the same authorization path used by people and API tokens.
func (h *Handler) APIPrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appKey := strings.TrimSpace(r.Header.Get("X-Zzira-App-Key"))
		if appKey == "" {
			if token := connectToken(r); token != "" {
				issuer, err := connectIssuer(token)
				if err != nil {
					appFailure(w, http.StatusUnauthorized, err)
					return
				}
				appKey = issuer
			}
		}
		if appKey == "" {
			next.ServeHTTP(w, r)
			return
		}
		scope, supported := appAPIScope(r)
		if !supported {
			appFailure(w, http.StatusForbidden, fmt.Errorf("signed app authentication is only available on product APIs"))
			return
		}
		installation, _, status, err := h.authenticateKey(r, appKey)
		if err != nil {
			appFailure(w, status, err)
			return
		}
		if installation.Status != "active" {
			appFailure(w, http.StatusForbidden, fmt.Errorf("app is suspended"))
			return
		}
		if scope != "" && !store.AppHasScope(installation, scope) {
			appFailure(w, http.StatusForbidden, fmt.Errorf("%s scope is required", scope))
			return
		}
		ctx := context.WithValue(authn.WithPrincipal(r.Context(), installation.PrincipalID), appInstallationContextKey{}, installation)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *Handler) DynamicModules(w http.ResponseWriter, r *http.Request) {
	installation, ok := r.Context().Value(appInstallationContextKey{}).(*models.AppInstallation)
	if !ok || installation == nil || connectToken(r) == "" {
		appFailure(w, http.StatusUnauthorized, fmt.Errorf("Connect JWT authentication is required"))
		return
	}
	if installation.Status != "active" {
		appFailure(w, http.StatusForbidden, fmt.Errorf("app is suspended"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		modules, err := h.Store.DynamicAppModules(r.Context(), installation.ID)
		if err != nil {
			appFailure(w, http.StatusInternalServerError, err)
			return
		}
		grouped := map[string][]json.RawMessage{}
		for _, module := range modules {
			grouped[module.Type] = append(grouped[module.Type], module.Descriptor)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(grouped)
	case http.MethodPost:
		body, err := io.ReadAll(io.LimitReader(r.Body, (256<<10)+1))
		if err != nil || len(body) > 256<<10 {
			appFailure(w, http.StatusBadRequest, fmt.Errorf("dynamic module request must contain at most 256 KiB"))
			return
		}
		modules, err := parseDynamicModules(body, installation)
		if err == nil {
			err = h.Store.RegisterDynamicAppModules(r.Context(), installation, modules)
		}
		if err != nil {
			appFailure(w, http.StatusBadRequest, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		keys := r.URL.Query()["moduleKey"]
		for _, key := range keys {
			if !moduleKeyPattern.MatchString(key) {
				appFailure(w, http.StatusBadRequest, fmt.Errorf("dynamic module key is invalid"))
				return
			}
		}
		if err := h.Store.DeleteDynamicAppModules(r.Context(), installation.ID, keys); err != nil {
			appFailure(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func parseDynamicModules(body []byte, installation *models.AppInstallation) ([]models.AppDynamicModule, error) {
	var groups map[string]json.RawMessage
	if err := json.Unmarshal(body, &groups); err != nil || len(groups) == 0 {
		return nil, fmt.Errorf("dynamic modules must be a non-empty JSON object")
	}
	modules := []models.AppDynamicModule{}
	keys := map[string]bool{}
	for moduleType, raw := range groups {
		if moduleType != "webPanels" && moduleType != "webhooks" && moduleType != "jiraIssueFields" && moduleType != "webItems" {
			return nil, fmt.Errorf("dynamic module type %q is not supported yet", moduleType)
		}
		if (moduleType == "webPanels" || moduleType == "webhooks") && !store.AppHasScope(installation, "read:jira-work") {
			return nil, fmt.Errorf("dynamic %s require READ scope", moduleType)
		}
		var entries []json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil || len(entries) == 0 {
			return nil, fmt.Errorf("dynamic %s must be a non-empty array", moduleType)
		}
		for _, entry := range entries {
			if moduleType == "webItems" {
				var input connectWebItemWire
				if err := json.Unmarshal(entry, &input); err != nil {
					return nil, fmt.Errorf("invalid dynamic web item: %w", err)
				}
				translated, err := translateConnectWebItem(input)
				if err != nil {
					return nil, err
				}
				if !moduleKeyPattern.MatchString(translated.Key) || keys[translated.Key] || !validAppCallbackPath(translated.URL) || translated.Title == "" || len(translated.Title) > 255 {
					return nil, fmt.Errorf("dynamic web item needs a unique key, name, relative URL, and supported navigation location")
				}
				keys[translated.Key] = true
				modules = append(modules, models.AppDynamicModule{Type: moduleType, Key: translated.Key, Descriptor: entry, Module: models.AppModule{Key: translated.Key, Type: translated.Type, Location: translated.Location, Title: translated.Title, RemoteURL: translated.URL, Dynamic: true}})
				continue
			}
			if moduleType == "jiraIssueFields" {
				var input connectIssueFieldWire
				if err := json.Unmarshal(entry, &input); err != nil {
					return nil, fmt.Errorf("invalid dynamic Jira issue field: %w", err)
				}
				field, err := translateConnectIssueField(input, true)
				if err != nil {
					return nil, err
				}
				if keys[field.Key] {
					return nil, fmt.Errorf("dynamic module keys must be unique")
				}
				keys[field.Key] = true
				modules = append(modules, models.AppDynamicModule{Type: moduleType, Key: field.Key, Descriptor: entry, IssueField: field})
				continue
			}
			if moduleType == "webhooks" {
				var input connectWebhookWire
				if err := json.Unmarshal(entry, &input); err != nil {
					return nil, fmt.Errorf("invalid dynamic webhook: %w", err)
				}
				input.Key, input.Event, input.URL, input.Filter = strings.TrimSpace(input.Key), strings.TrimSpace(input.Event), strings.TrimSpace(input.URL), strings.TrimSpace(input.Filter)
				if !dynamicWebhookKeyPattern.MatchString(input.Key) || keys[input.Key] || !allowedAppWebhookEvents[input.Event] || !validAppCallbackPath(input.URL) || len(input.Filter) > 10000 || input.ExcludeBody || len(input.PropertyKeys) > 0 || len(input.Conditions) > 0 {
					return nil, fmt.Errorf("dynamic webhook needs a unique key, supported event, relative URL, optional valid JQL, and supported body settings")
				}
				if input.Filter != "" {
					if _, err := jql.Parse(input.Filter); err != nil {
						return nil, fmt.Errorf("dynamic webhook %q has invalid JQL: %w", input.Key, err)
					}
				}
				keys[input.Key] = true
				modules = append(modules, models.AppDynamicModule{Type: moduleType, Key: input.Key, Descriptor: entry, Webhook: models.AppWebhook{Key: input.Key, Path: input.URL, Events: []string{input.Event}, JQL: input.Filter, Dynamic: true}})
				continue
			}
			var input connectRemoteModuleWire
			if err := json.Unmarshal(entry, &input); err != nil {
				return nil, fmt.Errorf("invalid dynamic web panel: %w", err)
			}
			input.Key, input.URL, input.Location, input.Name.Value = strings.TrimSpace(input.Key), strings.TrimSpace(input.URL), strings.TrimSpace(input.Location), strings.TrimSpace(input.Name.Value)
			if !moduleKeyPattern.MatchString(input.Key) || keys[input.Key] || !validAppCallbackPath(input.URL) || !connectIssuePanelLocation(input.Location) || input.Name.Value == "" || len(input.Name.Value) > 255 {
				return nil, fmt.Errorf("dynamic web panel needs a unique key, name, relative URL, and supported issue-view location")
			}
			keys[input.Key] = true
			modules = append(modules, models.AppDynamicModule{Type: moduleType, Key: input.Key, Descriptor: entry, Module: models.AppModule{Key: input.Key, Type: "jira:issuePanel", Location: "jira.issue.view", Title: input.Name.Value, RemoteURL: input.URL, Dynamic: true}})
		}
	}
	return modules, nil
}

func appFailure(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func (h *Handler) Lifecycle(w http.ResponseWriter, r *http.Request) {
	installation, body, status, err := h.authenticate(r)
	if err != nil {
		appFailure(w, status, err)
		return
	}
	workspaceID, _ := h.workspaceID(r)
	event := r.PathValue("event")
	switch event {
	case "enabled":
		err = h.Store.UpdateAppState(r.Context(), workspaceID, "", installation.Key, "active", false)
	case "disabled":
		err = h.Store.UpdateAppState(r.Context(), workspaceID, "", installation.Key, "suspended", false)
	case "uninstalled":
		err = h.Store.UpdateAppState(r.Context(), workspaceID, "", installation.Key, "uninstalled", false)
	case "upgraded":
		var descriptor models.AppDescriptor
		descriptor, err = ParseDescriptor(body)
		if err == nil && descriptor.Key != installation.Key {
			err = fmt.Errorf("upgraded descriptor key must match the installation")
		}
		if err == nil {
			for _, scope := range descriptor.Scopes {
				if !store.AppHasScope(installation, scope) {
					err = fmt.Errorf("scope %s requires administrator consent", scope)
					break
				}
			}
		}
		if err == nil {
			err = h.Store.UpgradeApp(r.Context(), workspaceID, installation.Key, descriptor, body)
		}
	default:
		appFailure(w, http.StatusNotFound, fmt.Errorf("unknown app lifecycle event"))
		return
	}
	if err != nil {
		appFailure(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Storage(w http.ResponseWriter, r *http.Request) {
	installation, body, status, err := h.authenticate(r)
	if err != nil {
		appFailure(w, status, err)
		return
	}
	if installation.Status != "active" {
		appFailure(w, http.StatusForbidden, fmt.Errorf("app is suspended"))
		return
	}
	key := r.PathValue("key")
	if !storageKeyPattern.MatchString(key) {
		appFailure(w, http.StatusBadRequest, fmt.Errorf("app storage key is invalid"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !store.AppHasScope(installation, "read:app-storage") {
			appFailure(w, http.StatusForbidden, fmt.Errorf("read:app-storage scope is required"))
			return
		}
		value, err := h.Store.AppStorage(r.Context(), installation.ID, key)
		if errors.Is(err, pgx.ErrNoRows) {
			appFailure(w, http.StatusNotFound, fmt.Errorf("app storage value was not found"))
			return
		}
		if err != nil {
			appFailure(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(value)
	case http.MethodPut:
		if !store.AppHasScope(installation, "write:app-storage") {
			appFailure(w, http.StatusForbidden, fmt.Errorf("write:app-storage scope is required"))
			return
		}
		if !json.Valid(body) || len(body) == 0 {
			appFailure(w, http.StatusBadRequest, fmt.Errorf("app storage value must be valid JSON"))
			return
		}
		value, err := h.Store.PutAppStorage(r.Context(), installation.ID, key, json.RawMessage(body))
		if err != nil {
			appFailure(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(value)
	case http.MethodDelete:
		if !store.AppHasScope(installation, "write:app-storage") {
			appFailure(w, http.StatusForbidden, fmt.Errorf("write:app-storage scope is required"))
			return
		}
		if err := h.Store.DeleteAppStorage(r.Context(), installation.ID, key); errors.Is(err, pgx.ErrNoRows) {
			appFailure(w, http.StatusNotFound, fmt.Errorf("app storage value was not found"))
			return
		} else if err != nil {
			appFailure(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		appFailure(w, http.StatusMethodNotAllowed, fmt.Errorf("method is not allowed"))
	}
}
