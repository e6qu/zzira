package apps

import (
	"bytes"
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

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/secretbox"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

var storageKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$`)
var requestIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{7,127}$`)

type Handler struct {
	Store         *store.Store
	Secrets       *secretbox.Box
	WorkspaceSlug string
	Now           func() time.Time
}

func SignRequest(secret []byte, timestamp int64, requestID, method, escapedPath string, body []byte) string {
	digest := sha256.Sum256(body)
	canonical := strconv.FormatInt(timestamp, 10) + "\n" + requestID + "\n" + strings.ToUpper(method) + "\n" + escapedPath + "\n" + hex.EncodeToString(digest[:])
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

func (h *Handler) workspaceID(r *http.Request) (string, error) {
	if h.Store == nil || h.WorkspaceSlug == "" {
		return "", fmt.Errorf("app runtime is not configured")
	}
	return h.Store.WorkspaceBySlug(r.Context(), h.WorkspaceSlug)
}

func (h *Handler) authenticate(r *http.Request) (*models.AppInstallation, []byte, int, error) {
	if h.Secrets == nil {
		return nil, nil, http.StatusServiceUnavailable, fmt.Errorf("app credential encryption is not configured")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return nil, nil, http.StatusRequestEntityTooLarge, fmt.Errorf("app request body is too large")
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	workspaceID, err := h.workspaceID(r)
	if err != nil {
		return nil, nil, http.StatusServiceUnavailable, err
	}
	installation, err := h.Store.AppInstallation(r.Context(), workspaceID, r.PathValue("appKey"))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, http.StatusUnauthorized, fmt.Errorf("unknown app")
	}
	if err != nil {
		return nil, nil, http.StatusInternalServerError, err
	}
	if installation.Status == "uninstalled" {
		return nil, nil, http.StatusUnauthorized, fmt.Errorf("app is uninstalled")
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
	secret, err := h.Secrets.Open(installation.SecretCiphertext, workspaceID+"/"+installation.Key)
	if err != nil {
		return nil, nil, http.StatusUnauthorized, fmt.Errorf("app credentials are unavailable")
	}
	expected, _ := hex.DecodeString(SignRequest(secret, timestamp, requestID, r.Method, r.URL.EscapedPath(), body))
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
