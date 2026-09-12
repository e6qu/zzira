package confluence

import (
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/store"
)

func (h *Handler) templateBean(template store.WikiTemplate) map[string]any {
	labels := make([]any, 0, len(template.Labels))
	for _, label := range template.Labels {
		labels = append(labels, map[string]any{"prefix": "global", "name": label, "label": label})
	}
	bean := map[string]any{
		"templateId": template.ID, "name": template.Name, "description": template.Description,
		"templateType": template.TemplateType, "editorVersion": template.EditorVersion,
		"labels": labels,
		"body": map[string]any{
			"storage": map[string]any{"value": template.Body, "representation": "storage"},
		},
		"_links": map[string]string{"base": h.BaseURL + "/wiki"},
	}
	if template.BlueprintModuleKey != "" {
		bean["originalTemplate"] = map[string]any{
			"pluginKey": template.BlueprintPluginKey, "moduleKey": template.BlueprintModuleKey,
		}
		bean["referencingBlueprint"] = template.ReferencingBlueprnt
	}
	if template.SpaceKey != "" {
		bean["space"] = map[string]any{"id": template.SpaceID, "key": template.SpaceKey}
	}
	return bean
}

type templateWrite struct {
	TemplateID   string `json:"templateId"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	TemplateType string `json:"templateType"`
	Body         struct {
		Storage struct {
			Value          string `json:"value"`
			Representation string `json:"representation"`
		} `json:"storage"`
	} `json:"body"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Space struct {
		Key string `json:"key"`
	} `json:"space"`
}

func (input templateWrite) labelNames() []string {
	names := make([]string, 0, len(input.Labels))
	for _, label := range input.Labels {
		if trimmed := strings.TrimSpace(label.Name); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	return names
}

// v1SaveTemplate serves both the create and the update; Confluence tells them
// apart by whether the body names a template.
func (h *V1Handler) v1SaveTemplate(w http.ResponseWriter, r *http.Request, ws, actor string, update bool) {
	if !supportedQuery(w, r) {
		return
	}
	var input templateWrite
	if !decode(w, r, &input) {
		return
	}
	templateID := ""
	if update {
		templateID = strings.TrimSpace(input.TemplateID)
		if templateID == "" {
			failure(w, 400, "templateId is required.")
			return
		}
	}
	template, err := h.Store.SaveWikiTemplate(r.Context(), ws, actor, templateID, input.Name,
		input.Description, input.TemplateType, input.Body.Storage.Value, input.Space.Key, input.labelNames())
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.templateBean(template))
}

func (h *V1Handler) v1Template(w http.ResponseWriter, r *http.Request, ws, actor, templateID string) {
	if !supportedQuery(w, r, "expand") {
		return
	}
	switch r.Method {
	case http.MethodGet:
		template, err := h.Store.WikiTemplate(r.Context(), ws, actor, templateID)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, h.templateBean(template))
	case http.MethodDelete:
		if err := h.Store.DeleteWikiTemplate(r.Context(), ws, actor, templateID); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		failure(w, 405, "Method not allowed.")
	}
}

func (h *V1Handler) v1PageTemplates(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "spaceKey", "start", "limit", "expand") {
		return
	}
	templates, err := h.Store.WikiTemplates(r.Context(), ws, actor, r.URL.Query().Get("spaceKey"))
	if err != nil {
		writeError(w, err)
		return
	}
	h.wikiPage(w, r, h.templateBeans(templates), false)
}

func (h *V1Handler) v1BlueprintTemplates(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "spaceKey", "start", "limit", "expand") {
		return
	}
	templates, err := h.Store.WikiBlueprintTemplates(r.Context(), ws, actor, r.URL.Query().Get("spaceKey"))
	if err != nil {
		writeError(w, err)
		return
	}
	h.wikiPage(w, r, h.templateBeans(templates), false)
}

func (h *Handler) templateBeans(templates []store.WikiTemplate) []any {
	values := make([]any, 0, len(templates))
	for _, template := range templates {
		values = append(values, h.templateBean(template))
	}
	return values
}

// v1PublishBlueprintDraft publishes a draft page created from a blueprint. The
// shared and legacy drafts behave the same way, which is what Confluence says
// of them, so one handler serves both.
func (h *V1Handler) v1PublishBlueprintDraft(w http.ResponseWriter, r *http.Request, ws, actor, draftID string) {
	if !supportedQuery(w, r, "status", "expand") {
		return
	}
	var input struct {
		Version struct {
			Number int `json:"number"`
		} `json:"version"`
		Title string `json:"title"`
		Space struct {
			Key string `json:"key"`
		} `json:"space"`
		Ancestors []struct {
			ID string `json:"id"`
		} `json:"ancestors"`
	}
	if !decode(w, r, &input) {
		return
	}
	parentID := ""
	if len(input.Ancestors) > 0 {
		parentID = input.Ancestors[len(input.Ancestors)-1].ID
	}
	pageID, err := h.Store.PublishWikiBlueprintDraft(r.Context(), ws, actor, draftID,
		strings.TrimSpace(input.Title), input.Version.Number, input.Space.Key, parentID)
	if err != nil {
		writeError(w, err)
		return
	}
	page, err := h.Store.WikiPage(r.Context(), ws, actor, pageID)
	if err != nil {
		writeError(w, err)
		return
	}
	space, err := h.Store.WikiSpace(r.Context(), ws, actor, page.SpaceID)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, map[string]any{
		"id": page.ID, "type": "page", "status": page.Status, "title": page.Title,
		"space":   h.spaceBean(space, "plain", false),
		"version": map[string]any{"number": page.Version.Number, "minorEdit": false},
		"body":    map[string]any{"storage": map[string]any{"value": page.Body.Value, "representation": "storage"}},
		"_links":  map[string]string{"base": h.BaseURL + "/wiki", "webui": "/spaces/" + page.SpaceID + "/pages/" + page.ID},
	})
}
