package api3

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

type jqlFunctionPrecomputationBean struct {
	Arguments    []string `json:"arguments"`
	Created      string   `json:"created"`
	Error        *string  `json:"error,omitempty"`
	Field        string   `json:"field"`
	FunctionKey  string   `json:"functionKey"`
	FunctionName string   `json:"functionName"`
	ID           string   `json:"id"`
	Operator     string   `json:"operator"`
	Updated      string   `json:"updated"`
	Used         string   `json:"used"`
	Value        *string  `json:"value,omitempty"`
}

func jqlFunctionPrecomputationWire(value models.JQLFunctionPrecomputation) jqlFunctionPrecomputationBean {
	arguments := value.Arguments
	if arguments == nil {
		arguments = []string{}
	}
	return jqlFunctionPrecomputationBean{
		Arguments: arguments, Created: value.CreatedAt.Format("2006-01-02T15:04:05.000-0700"),
		Error: value.Error, Field: value.Field, FunctionKey: value.FunctionKey,
		FunctionName: value.FunctionName, ID: value.ID, Operator: value.Operator,
		Updated: value.UpdatedAt.Format("2006-01-02T15:04:05.000-0700"),
		Used:    value.UsedAt.Format("2006-01-02T15:04:05.000-0700"), Value: value.Value,
	}
}

func jqlFunctionPrecomputationPage(r *http.Request) (int, int, *jerr) {
	startAt, maxResults := 0, 100
	for name, target := range map[string]*int{"startAt": &startAt, "maxResults": &maxResults} {
		raw := r.URL.Query().Get(name)
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || name == "maxResults" && value > 1000 {
			return 0, 0, &jerr{status: http.StatusBadRequest, message: name + " must be between 0 and 1000."}
		}
		*target = value
	}
	return startAt, maxResults, nil
}

func jqlFunctionKeys(r *http.Request) []string {
	values := []string{}
	seen := map[string]bool{}
	for _, raw := range r.URL.Query()["functionKey"] {
		for _, value := range strings.Split(raw, ",") {
			value = strings.TrimSpace(value)
			if value != "" && !seen[value] {
				seen[value] = true
				values = append(values, value)
			}
		}
	}
	return values
}

func (h *Handler) jqlFunctionApp(r *http.Request) (string, string, *jerr) {
	workspaceID, principalID, authErr := h.authWorkspace(r)
	if authErr != nil {
		return "", "", authErr
	}
	installationID, _, err := h.Store.ActiveAppInstallationForPrincipal(r.Context(), workspaceID, principalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", &jerr{status: http.StatusForbidden, message: "This resource is only available to an installed app."}
	}
	if err != nil {
		return "", "", &jerr{status: http.StatusInternalServerError, message: "Could not resolve the app installation."}
	}
	return workspaceID, installationID, nil
}

func (h *Handler) jqlFunctionPrecomputations(w http.ResponseWriter, r *http.Request) {
	_, installationID, authErr := h.jqlFunctionApp(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if r.Method == http.MethodPost {
		h.updateJQLFunctionPrecomputations(w, r, installationID)
		return
	}
	startAt, maxResults, pageErr := jqlFunctionPrecomputationPage(r)
	if pageErr != nil {
		writeJerr(w, pageErr)
		return
	}
	functionKeys := jqlFunctionKeys(r)
	values, total, err := h.Store.JQLFunctionPrecomputations(r.Context(), installationID, functionKeys, startAt, maxResults, r.URL.Query().Get("orderBy"))
	if err != nil {
		jiraError(w, http.StatusBadRequest, "The precomputation query is invalid.")
		return
	}
	if len(functionKeys) > 0 && total == 0 {
		jiraError(w, http.StatusNotFound, "The JQL function was not found.")
		return
	}
	wires := make([]jqlFunctionPrecomputationBean, 0, len(values))
	for _, value := range values {
		wires = append(wires, jqlFunctionPrecomputationWire(value))
	}
	requestURL := *r.URL
	self := strings.TrimRight(h.BaseURL, "/") + requestURL.RequestURI()
	response := map[string]any{"isLast": int64(startAt+len(values)) >= total, "maxResults": maxResults, "self": self, "startAt": startAt, "total": total, "values": wires}
	if len(values) > 0 && int64(startAt+len(values)) < total {
		query := requestURL.Query()
		query.Set("startAt", strconv.Itoa(startAt+len(values)))
		requestURL.RawQuery = query.Encode()
		response["nextPage"] = strings.TrimRight(h.BaseURL, "/") + requestURL.RequestURI()
	}
	writeJSON(w, http.StatusOK, response)
}

type jqlFunctionPrecomputationUpdateWire struct {
	ID    string  `json:"id"`
	Value *string `json:"value"`
	Error *string `json:"error"`
}

func (h *Handler) updateJQLFunctionPrecomputations(w http.ResponseWriter, r *http.Request, installationID string) {
	var request struct {
		Values []jqlFunctionPrecomputationUpdateWire `json:"values"`
	}
	if !decodeJQLBody(w, r, &request) {
		return
	}
	if len(request.Values) == 0 || len(request.Values) > 1000 {
		jiraError(w, http.StatusBadRequest, "values must contain between 1 and 1000 precomputation updates.")
		return
	}
	updates := make([]models.JQLFunctionPrecomputationUpdate, 0, len(request.Values))
	seen := map[string]bool{}
	for _, value := range request.Values {
		value.ID = strings.TrimSpace(value.ID)
		if value.ID == "" || seen[value.ID] || (value.Value == nil) == (value.Error == nil) {
			jiraError(w, http.StatusBadRequest, "Each update needs a unique id and exactly one of value or error.")
			return
		}
		seen[value.ID] = true
		updates = append(updates, models.JQLFunctionPrecomputationUpdate{ID: value.ID, Value: value.Value, Error: value.Error})
	}
	rawSkip := r.URL.Query().Get("skipNotFoundPrecomputations")
	if rawSkip == "" {
		rawSkip = "false"
	}
	skip, err := strconv.ParseBool(rawSkip)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "skipNotFoundPrecomputations must be true or false.")
		return
	}
	missing, err := h.Store.UpdateJQLFunctionPrecomputations(r.Context(), installationID, updates, skip)
	if errors.Is(err, store.ErrJQLFunctionPrecomputationNotFound) {
		jiraError(w, http.StatusNotFound, "One or more precomputations were not found.")
		return
	}
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not update JQL function precomputations.")
		return
	}
	if skip {
		writeJSON(w, http.StatusOK, map[string]any{"notFoundPrecomputationIDs": missing})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) jqlFunctionPrecomputationsByID(w http.ResponseWriter, r *http.Request) {
	_, installationID, authErr := h.jqlFunctionApp(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		IDs []string `json:"precomputationIDs"`
	}
	if !decodeJQLBody(w, r, &request) {
		return
	}
	if len(request.IDs) > 1000 {
		jiraError(w, http.StatusBadRequest, "No more than 1000 precomputation IDs may be supplied.")
		return
	}
	ids := make([]string, 0, len(request.IDs))
	seen := map[string]bool{}
	for _, id := range request.IDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			jiraError(w, http.StatusBadRequest, "precomputationIDs must contain unique, non-empty IDs.")
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	values, missing, err := h.Store.JQLFunctionPrecomputationsByID(r.Context(), installationID, ids, r.URL.Query().Get("orderBy"))
	if err != nil {
		jiraError(w, http.StatusBadRequest, "The precomputation query is invalid.")
		return
	}
	wires := make([]jqlFunctionPrecomputationBean, 0, len(values))
	for _, value := range values {
		wires = append(wires, jqlFunctionPrecomputationWire(value))
	}
	writeJSON(w, http.StatusOK, map[string]any{"notFoundPrecomputationIDs": missing, "precomputations": wires})
}
