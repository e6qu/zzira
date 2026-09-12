package confluence

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/store"
)

func versionNumberFrom(w http.ResponseWriter, raw string) (int, bool) {
	number, err := strconv.Atoi(raw)
	if err != nil || number < 1 {
		failure(w, 400, "The version number must be a positive integer.")
		return 0, false
	}
	return number, true
}

// v1RestoreVersion makes a historical version the latest.
func (h *V1Handler) v1RestoreVersion(w http.ResponseWriter, r *http.Request, ws, actor, pageID string) {
	if !supportedQuery(w, r, "expand") {
		return
	}
	var input struct {
		OperationKey string `json:"operationKey"`
		Params       struct {
			VersionNumber int    `json:"versionNumber"`
			Message       string `json:"message"`
			RestoreTitle  bool   `json:"restoreTitle"`
		} `json:"params"`
	}
	if !decode(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.OperationKey) != "restore" {
		failure(w, 400, "operationKey must be restore.")
		return
	}
	number, err := h.Store.RestoreWikiPageVersion(r.Context(), ws, actor, pageID,
		input.Params.VersionNumber, input.Params.Message, input.Params.RestoreTitle)
	if err != nil {
		writeError(w, err)
		return
	}
	page, err := h.Store.WikiPage(r.Context(), ws, actor, pageID)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, map[string]any{
		"number": number, "minorEdit": false, "message": input.Params.Message,
		"by":     map[string]any{"type": "known", "accountId": actor, "accountType": "atlassian"},
		"when":   page.Version.CreatedAt,
		"_links": map[string]string{"base": h.BaseURL + "/wiki"},
	})
}

func (h *V1Handler) v1DeleteVersion(w http.ResponseWriter, r *http.Request, ws, actor, pageID, versionRaw string) {
	if !supportedQuery(w, r) {
		return
	}
	number, ok := versionNumberFrom(w, versionRaw)
	if !ok {
		return
	}
	if err := h.Store.DeleteWikiPageVersion(r.Context(), ws, actor, pageID, number); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}

// v1HistoricalMacro reads a macro as it was in one version.
func (h *V1Handler) v1HistoricalMacro(w http.ResponseWriter, r *http.Request, ws, actor, pageID, versionRaw, macroID string) {
	if !supportedQuery(w, r) {
		return
	}
	number, ok := versionNumberFrom(w, versionRaw)
	if !ok {
		return
	}
	macro, err := h.Store.WikiMacroFromVersion(r.Context(), ws, actor, pageID, number, macroID)
	if err != nil {
		writeError(w, err)
		return
	}
	parameters := map[string]any{}
	for name, value := range macro.Parameters {
		parameters[name] = value
	}
	respond(w, 200, map[string]any{
		"name": macro.Name, "body": macro.Body, "parameters": parameters,
		"_links": map[string]string{"base": h.BaseURL + "/wiki"},
	})
}

// v1ConvertHistoricalMacro renders that macro into another format.
func (h *V1Handler) v1ConvertHistoricalMacro(w http.ResponseWriter, r *http.Request, ws, actor, pageID, versionRaw, macroID, to string, async bool) {
	if !supportedQuery(w, r, "expand", "spaceKeyContext", "embeddedContentRender", "allowCache") {
		return
	}
	number, ok := versionNumberFrom(w, versionRaw)
	if !ok {
		return
	}
	macro, err := h.Store.WikiMacroFromVersion(r.Context(), ws, actor, pageID, number, macroID)
	if err != nil {
		writeError(w, err)
		return
	}
	if async {
		id, startErr := h.Store.StartWikiBodyConversion(r.Context(), ws, actor, macro.Body, "storage", to)
		if startErr != nil {
			writeError(w, startErr)
			return
		}
		respond(w, 200, map[string]any{"asyncId": id})
		return
	}
	converted, err := store.ConvertWikiBody(macro.Body, "storage", to)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, map[string]any{
		"value": converted, "representation": to, "embeddedContent": []any{},
		"_links": map[string]string{"base": h.BaseURL + "/wiki"},
	})
}

func (h *Handler) conversionBean(conversion store.WikiBodyConversion) map[string]any {
	bean := map[string]any{
		"value": conversion.Value, "representation": conversion.Representation,
		"status": conversion.Status, "renderTaskId": conversion.ID,
		"embeddedContent": []any{},
		"_links":          map[string]string{"base": h.BaseURL + "/wiki"},
	}
	if conversion.Error != "" {
		bean["error"] = conversion.Error
	}
	return bean
}

// v1ConvertBodyAsync converts a body and answers the id to fetch it with.
func (h *V1Handler) v1ConvertBodyAsync(w http.ResponseWriter, r *http.Request, ws, actor, to string) {
	if !supportedQuery(w, r, "spaceKeyContext", "contentIdContext", "allowCache", "embeddedContentRender", "expand") {
		return
	}
	var input struct {
		Value          string `json:"value"`
		Representation string `json:"representation"`
	}
	if !decode(w, r, &input) {
		return
	}
	id, err := h.Store.StartWikiBodyConversion(r.Context(), ws, actor, input.Value, input.Representation, to)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, map[string]any{"asyncId": id})
}

func (h *V1Handler) v1ConversionResult(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r) {
		return
	}
	conversion, err := h.Store.WikiBodyConversionResult(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.conversionBean(conversion))
}

// v1BulkConversions converts many bodies at once, and reads many results back.
func (h *V1Handler) v1BulkConversions(w http.ResponseWriter, r *http.Request, ws, actor string) {
	switch r.Method {
	case http.MethodPost:
		if !supportedQuery(w, r) {
			return
		}
		var input struct {
			ConversionInputs []struct {
				To   string `json:"to"`
				Body struct {
					Value          string `json:"value"`
					Representation string `json:"representation"`
				} `json:"body"`
			} `json:"conversionInputs"`
		}
		if !decode(w, r, &input) {
			return
		}
		if len(input.ConversionInputs) == 0 || len(input.ConversionInputs) > 100 {
			failure(w, 400, "Between 1 and 100 conversions are required.")
			return
		}
		results := make([]any, 0, len(input.ConversionInputs))
		for _, conversion := range input.ConversionInputs {
			id, err := h.Store.StartWikiBodyConversion(r.Context(), ws, actor,
				conversion.Body.Value, conversion.Body.Representation, conversion.To)
			if err != nil {
				writeError(w, err)
				return
			}
			results = append(results, map[string]any{"asyncId": id})
		}
		respond(w, 200, results)
	case http.MethodGet:
		if !supportedQuery(w, r, "ids") {
			return
		}
		ids := splitQueryValues(r, "ids")
		if len(ids) == 0 {
			failure(w, 400, "ids is required.")
			return
		}
		results := make([]any, 0, len(ids))
		for _, id := range ids {
			conversion, err := h.Store.WikiBodyConversionResult(r.Context(), ws, actor, id)
			if err != nil {
				// A result that has expired or never existed is reported as
				// such rather than failing the whole read, because a bulk read
				// is asking about several independent conversions.
				results = append(results, map[string]any{
					"renderTaskId": id, "status": "FAILED", "error": "No conversion with this id is available.",
				})
				continue
			}
			results = append(results, h.conversionBean(conversion))
		}
		respond(w, 200, results)
	default:
		failure(w, 405, "Method not allowed.")
	}
}
