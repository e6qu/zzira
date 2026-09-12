package confluence

import (
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/store"
)

// Relations are named one-way links between two entities: the favourites a
// person saved, the pages a client named siblings of one another. Reading one
// direction says nothing about the other, so a relation from A to B is not a
// relation from B to A, and the two listings answer different questions.

// relationEntityBean renders one end of a relation as the entity it is.
func (h *V1Handler) relationEntityBean(entity store.WikiRelationEntity) map[string]any {
	switch entity.Type {
	case "user":
		return h.wikiUserBean(entity.User, false)
	case "space":
		return h.spaceBean(entity.Space, "plain", false)
	default:
		page := entity.Content
		bean := map[string]any{
			"id": page.ID, "type": "page", "status": entity.Status, "title": page.Title,
			"space":  map[string]string{"id": page.SpaceID},
			"_links": map[string]string{"webui": "/spaces/" + page.SpaceID + "/pages/" + page.ID, "self": h.BaseURL + "/wiki/rest/api/content/" + page.ID},
		}
		if entity.Status == "historical" {
			bean["version"] = map[string]any{"number": entity.Version}
		}
		return bean
	}
}

// relationBean renders the relation. Confluence sends the ends and the
// authorship only when they were asked for, and otherwise names them as
// expandable so a reader knows what more there is to fetch.
func (h *V1Handler) relationBean(relation store.WikiRelation, expand map[string]bool) map[string]any {
	self := h.BaseURL + "/wiki/rest/api/relation/" + relation.Name +
		"/from/" + relation.Source.Type + "/" + relation.Source.Key +
		"/to/" + relation.Target.Type + "/" + relation.Target.Key
	bean := map[string]any{
		"name":   relation.Name,
		"_links": map[string]string{"self": self, "base": h.BaseURL + "/wiki"},
	}
	expandable := map[string]string{}
	if expand["relationData"] {
		bean["relationData"] = map[string]any{
			"createdBy":   h.wikiUserBean(relation.CreatedBy, false),
			"createdDate": relation.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		}
	} else {
		expandable["relationData"] = ""
	}
	if expand["source"] {
		bean["source"] = h.relationEntityBean(relation.Source)
	} else {
		expandable["source"] = ""
	}
	if expand["target"] {
		bean["target"] = h.relationEntityBean(relation.Target)
	} else {
		expandable["target"] = ""
	}
	bean["_expandable"] = expandable
	return bean
}

// relationExpansions reads the expand parameter. relationData, source and
// target are the three parts of a relation there are to ask for.
func relationExpansions(w http.ResponseWriter, r *http.Request) (map[string]bool, bool) {
	expand := map[string]bool{}
	for _, raw := range r.URL.Query()["expand"] {
		for _, value := range strings.Split(raw, ",") {
			value = strings.TrimSpace(value)
			switch value {
			case "":
			case "relationData", "source", "target":
				expand[value] = true
			default:
				failure(w, 400, "A relation expands relationData, source or target.")
				return nil, false
			}
		}
	}
	return expand, true
}

// relationEnds reads the status and version qualifiers. Only content has a
// status and a version, and a version names one historical revision of it.
func relationEnds(w http.ResponseWriter, r *http.Request, source, target store.WikiRelationEntity) (store.WikiRelationEntity, store.WikiRelationEntity, bool) {
	query := r.URL.Query()
	source.Status, target.Status = query.Get("sourceStatus"), query.Get("targetStatus")
	for _, end := range []struct {
		param  string
		status string
		into   *int
	}{
		{"sourceVersion", source.Status, &source.Version},
		{"targetVersion", target.Status, &target.Version},
	} {
		raw := query.Get(end.param)
		if raw == "" {
			continue
		}
		version, err := strconvAtoiBounded(raw, 1, 1<<20)
		if err != nil {
			failure(w, 400, end.param+" must be a version of 1 or more.")
			return source, target, false
		}
		*end.into = version
	}
	return source, target, true
}

// relationPage renders a page of relations. Confluence returns 25 of them
// unless asked for another number.
func (h *V1Handler) relationPage(w http.ResponseWriter, r *http.Request, relations []store.WikiRelation, expand map[string]bool) {
	start, limit := 0, 25
	if raw := r.URL.Query().Get("start"); raw != "" {
		parsed, err := strconvAtoiBounded(raw, 0, 1<<20)
		if err != nil {
			failure(w, 400, "start must be zero or greater.")
			return
		}
		start = parsed
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconvAtoiBounded(raw, 0, 200)
		if err != nil {
			failure(w, 400, "limit must be between 0 and 200.")
			return
		}
		limit = parsed
	}
	results := []any{}
	for i := start; i < len(relations) && len(results) < limit; i++ {
		results = append(results, h.relationBean(relations[i], expand))
	}
	respond(w, 200, map[string]any{
		"results": results, "start": start, "limit": limit, "size": len(results),
		"_links": map[string]string{"base": h.BaseURL + "/wiki"},
	})
}

// v1RelationTargets lists what a source is related to.
func (h *V1Handler) v1RelationTargets(w http.ResponseWriter, r *http.Request, ws, actor, name, sourceType, sourceKey, targetType string) {
	if !supportedQuery(w, r, "sourceStatus", "targetStatus", "sourceVersion", "targetVersion", "expand", "start", "limit") {
		return
	}
	expand, ok := relationExpansions(w, r)
	if !ok {
		return
	}
	source, target, ok := relationEnds(w, r,
		store.WikiRelationEntity{Type: sourceType, Key: sourceKey}, store.WikiRelationEntity{Type: targetType})
	if !ok {
		return
	}
	relations, err := h.Store.WikiRelationsFrom(r.Context(), ws, actor, name, source, target)
	if err != nil {
		writeError(w, err)
		return
	}
	h.relationPage(w, r, relations, expand)
}

// v1RelationSources lists what is related to a target.
func (h *V1Handler) v1RelationSources(w http.ResponseWriter, r *http.Request, ws, actor, name, targetType, targetKey, sourceType string) {
	if !supportedQuery(w, r, "sourceStatus", "targetStatus", "sourceVersion", "targetVersion", "expand", "start", "limit") {
		return
	}
	expand, ok := relationExpansions(w, r)
	if !ok {
		return
	}
	source, target, ok := relationEnds(w, r,
		store.WikiRelationEntity{Type: sourceType}, store.WikiRelationEntity{Type: targetType, Key: targetKey})
	if !ok {
		return
	}
	relations, err := h.Store.WikiRelationsTo(r.Context(), ws, actor, name, target, source)
	if err != nil {
		writeError(w, err)
		return
	}
	h.relationPage(w, r, relations, expand)
}

// v1Relation reads, creates and removes one relation.
func (h *V1Handler) v1Relation(w http.ResponseWriter, r *http.Request, ws, actor, name, sourceType, sourceKey, targetType, targetKey string) {
	allowed := []string{"sourceStatus", "targetStatus", "sourceVersion", "targetVersion"}
	if r.Method == http.MethodGet {
		allowed = append(allowed, "expand")
	}
	if !supportedQuery(w, r, allowed...) {
		return
	}
	expand, ok := relationExpansions(w, r)
	if !ok {
		return
	}
	source, target, ok := relationEnds(w, r,
		store.WikiRelationEntity{Type: sourceType, Key: sourceKey},
		store.WikiRelationEntity{Type: targetType, Key: targetKey})
	if !ok {
		return
	}
	relation := store.WikiRelation{Name: name, Source: source, Target: target}
	switch r.Method {
	case http.MethodGet:
		found, err := h.Store.WikiRelationBetween(r.Context(), ws, actor, relation)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, h.relationBean(found, expand))
	case http.MethodPut:
		saved, err := h.Store.SaveWikiRelation(r.Context(), ws, actor, relation)
		if err != nil {
			writeError(w, err)
			return
		}
		// Creating a relation takes no expand parameter, so the reply names
		// the relation and leaves its parts to be fetched.
		respond(w, 200, h.relationBean(saved, nil))
	case http.MethodDelete:
		if err := h.Store.DeleteWikiRelation(r.Context(), ws, actor, relation); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		failure(w, 405, "Relations support GET, PUT and DELETE.")
	}
}
