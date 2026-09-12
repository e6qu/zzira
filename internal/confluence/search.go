package confluence

import (
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/store"
)

// CQL search reads across everything a reader may see. Two endpoints serve it:
// /search returns search results, which describe a match — its title, an
// excerpt, where it sits — and /content/search returns the content itself.

// searchScope is what both endpoints read from the request beyond the query.
type searchScope struct {
	request store.WikiSearchRequest
	excerpt string
	cursor  string
}

// readSearchScope reads the query string both endpoints share.
func (h *V1Handler) readSearchScope(w http.ResponseWriter, r *http.Request, entityTypes []string, defaultLimit int) (searchScope, bool) {
	scope := searchScope{request: store.WikiSearchRequest{
		EntityTypes: entityTypes, Limit: defaultLimit, Now: time.Now().UTC(),
	}, excerpt: "highlight"}
	query := r.URL.Query()

	scope.request.CQL = strings.TrimSpace(query.Get("cql"))
	if scope.request.CQL == "" {
		failure(w, 400, "cql is required.")
		return scope, false
	}
	if raw := query.Get("cqlcontext"); raw != "" {
		var context struct {
			SpaceKey        string   `json:"spaceKey"`
			ContentID       string   `json:"contentId"`
			ContentStatuses []string `json:"contentStatuses"`
		}
		if err := json.Unmarshal([]byte(raw), &context); err != nil {
			failure(w, 400, "cqlcontext must be an object with spaceKey, contentId and contentStatuses.")
			return scope, false
		}
		scope.request.SpaceKey = context.SpaceKey
		scope.request.ContentID = context.ContentID
		scope.request.ContentStatuses = context.ContentStatuses
		if context.ContentID != "" && context.SpaceKey == "" {
			failure(w, 400, "A contentId in cqlcontext names content in the space given by spaceKey.")
			return scope, false
		}
	}
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconvAtoiBounded(raw, 0, 200)
		if err != nil {
			failure(w, 400, "limit must be between 0 and 200.")
			return scope, false
		}
		scope.request.Limit = parsed
	}
	if raw := query.Get("start"); raw != "" {
		parsed, err := strconvAtoiBounded(raw, 0, 1<<20)
		if err != nil {
			failure(w, 400, "start must be zero or greater.")
			return scope, false
		}
		scope.request.Start = parsed
	}
	// A cursor points at a set of results. It carries the position it points
	// at, so it stays meaningful across a restart and cannot be made to read
	// anything a fresh request could not.
	if raw := query.Get("cursor"); raw != "" {
		start, err := decodeSearchCursor(raw)
		if err != nil {
			failure(w, 400, "cursor is not one this search issued.")
			return scope, false
		}
		scope.request.Start = start
		scope.cursor = raw
	}
	for _, flag := range []struct {
		name string
		into *bool
	}{
		{"includeArchivedSpaces", &scope.request.IncludeArchivedSpaces},
		{"excludeCurrentSpaces", &scope.request.ExcludeCurrentSpaces},
	} {
		raw := query.Get(flag.name)
		if raw == "" {
			continue
		}
		value, err := strconv.ParseBool(raw)
		if err != nil {
			failure(w, 400, flag.name+" must be true or false.")
			return scope, false
		}
		*flag.into = value
	}
	if raw := query.Get("excerpt"); raw != "" {
		switch raw {
		case "highlight", "indexed", "none", "highlight_unescaped", "indexed_unescaped":
			scope.excerpt = raw
		default:
			failure(w, 400, "excerpt is highlight, indexed, none, highlight_unescaped or indexed_unescaped.")
			return scope, false
		}
	}
	return scope, true
}

// searchCursor encodes where a page of results starts. Confluence hands a
// client an opaque string rather than a number, so this one is opaque too.
func encodeSearchCursor(start int) string {
	return "zc" + strconv.FormatInt(int64(start)*7+13, 36)
}

func decodeSearchCursor(cursor string) (int, error) {
	if !strings.HasPrefix(cursor, "zc") {
		return 0, strconv.ErrSyntax
	}
	value, err := strconv.ParseInt(cursor[2:], 36, 64)
	if err != nil || value < 13 || (value-13)%7 != 0 {
		return 0, strconv.ErrSyntax
	}
	return int((value - 13) / 7), nil
}

// searchLinks builds the base, self, next and previous links. Confluence hands
// back the URLs to walk with rather than asking a client to build them.
func (h *V1Handler) searchLinks(r *http.Request, scope searchScope, returned, total int) map[string]string {
	self := r.URL.Path
	if r.URL.RawQuery != "" {
		self += "?" + r.URL.RawQuery
	}
	links := map[string]string{
		"base": h.BaseURL + "/wiki", "context": "/wiki", "self": h.BaseURL + self,
	}
	withCursor := func(start int) string {
		values := url.Values{}
		for name, held := range r.URL.Query() {
			if name == "cursor" || name == "start" {
				continue
			}
			values[name] = held
		}
		values.Set("cursor", encodeSearchCursor(start))
		// The walking links are relative to the base, which is how Confluence
		// hands them back: a client joins base and next to get the URL.
		return strings.TrimPrefix(r.URL.Path, "/wiki") + "?" + values.Encode()
	}
	if scope.request.Start+returned < total {
		links["next"] = withCursor(scope.request.Start + returned)
	}
	if scope.request.Start > 0 {
		previous := scope.request.Start - scope.request.Limit
		if previous < 0 {
			previous = 0
		}
		links["prev"] = withCursor(previous)
	}
	return links
}

// excerptOf renders the passage shown under a result. Confluence marks the
// words that matched so a reader can see why the result is there.
func excerptOf(result store.WikiSearchResult, strategy, cqlQuery string) string {
	if strategy == "none" {
		return ""
	}
	text := strings.Join(strings.Fields(adf.StripTags(result.Body)), " ")
	if len(text) > 500 {
		text = text[:500]
	}
	escaped := strategy == "highlight" || strategy == "indexed"
	if escaped {
		text = html.EscapeString(text)
	}
	if strategy != "highlight" && strategy != "highlight_unescaped" {
		return text
	}
	for _, term := range highlightTerms(cqlQuery) {
		if escaped {
			term = html.EscapeString(term)
		}
		text = highlight(text, term)
	}
	return text
}

// highlightTerms pulls the words a reader searched for out of the query, so the
// excerpt can point at them.
func highlightTerms(cqlQuery string) []string {
	terms := []string{}
	for _, part := range strings.Split(cqlQuery, "~") {
		trimmed := strings.TrimSpace(part)
		if !strings.HasPrefix(trimmed, `"`) && !strings.HasPrefix(trimmed, "'") {
			continue
		}
		quote := trimmed[0]
		if end := strings.IndexByte(trimmed[1:], quote); end >= 0 {
			if term := strings.TrimSpace(trimmed[1 : end+1]); term != "" {
				terms = append(terms, term)
			}
		}
	}
	return terms
}

// highlight wraps every case-insensitive occurrence of the term in the markers
// Confluence uses, without disturbing the text around them.
func highlight(text, term string) string {
	if term == "" {
		return text
	}
	lowerText, lowerTerm := strings.ToLower(text), strings.ToLower(term)
	var out strings.Builder
	for {
		index := strings.Index(lowerText, lowerTerm)
		if index < 0 {
			out.WriteString(text)
			return out.String()
		}
		out.WriteString(text[:index])
		out.WriteString("@@@hl@@@" + text[index:index+len(term)] + "@@@endhl@@@")
		text, lowerText = text[index+len(term):], lowerText[index+len(term):]
	}
}

var searchIconClasses = map[string]string{
	"page": "aui-iconfont-page-default", "blogpost": "aui-iconfont-blogroll",
	"comment": "aui-iconfont-comment", "attachment": "aui-iconfont-file-generic",
	"space": "aui-iconfont-space-default",
}

func (h *V1Handler) searchResultURL(result store.WikiSearchResult) string {
	if result.EntityType == "space" {
		return "/spaces/" + result.SpaceID
	}
	return "/spaces/" + result.SpaceID + "/pages/" + result.ID
}

// searchContentBean renders a match as the content it is, which is what the
// content search returns.
func (h *V1Handler) searchContentBean(result store.WikiSearchResult) map[string]any {
	contentType := result.EntityType
	if contentType == "blogpost" {
		contentType = "blogpost"
	}
	return map[string]any{
		"id": result.ID, "type": contentType, "status": result.Status, "title": result.Title,
		"space": map[string]string{"id": result.SpaceID, "key": result.SpaceKey},
		"_links": map[string]string{
			"webui": h.searchResultURL(result),
			"self":  h.BaseURL + "/wiki/rest/api/content/" + result.ID,
			"base":  h.BaseURL + "/wiki",
		},
		"_expandable": map[string]string{"body": "", "version": "", "history": ""},
	}
}

// searchResultBean renders a match as a search result, which describes where
// the match sits as well as what it is.
func (h *V1Handler) searchResultBean(result store.WikiSearchResult, scope searchScope) map[string]any {
	bean := map[string]any{
		"title":      result.Title,
		"excerpt":    excerptOf(result, scope.excerpt, scope.request.CQL),
		"url":        h.searchResultURL(result),
		"entityType": entityTypeOf(result.EntityType),
		"iconCssClass": func() string {
			if class, ok := searchIconClasses[result.EntityType]; ok {
				return class
			}
			return "aui-iconfont-page-default"
		}(),
		"lastModified":         result.LastModified.UTC().Format("2006-01-02T15:04:05.000Z"),
		"friendlyLastModified": friendlyDate(result.LastModified, scope.request.Now),
		"score":                0,
		"resultGlobalContainer": map[string]string{
			"title": result.SpaceKey, "displayUrl": "/spaces/" + result.SpaceID,
		},
		"breadcrumbs": []any{},
	}
	if result.EntityType == "space" {
		bean["space"] = map[string]any{"id": result.SpaceID, "key": result.SpaceKey, "name": result.Title,
			"_links": map[string]string{"webui": "/spaces/" + result.SpaceID}}
	} else {
		bean["content"] = h.searchContentBean(result)
	}
	if result.ContainerTitle != "" && result.EntityType != "space" {
		bean["resultParentContainer"] = map[string]string{
			"title": result.ContainerTitle, "displayUrl": "/spaces/" + result.SpaceID,
		}
	}
	return bean
}

// entityTypeOf reports what a result is. Confluence calls everything that lives
// in a space "content" and names the rest by what it is.
func entityTypeOf(entityType string) string {
	if entityType == "space" {
		return "space"
	}
	return "content"
}

// friendlyDate is the phrasing Confluence shows beside a result.
func friendlyDate(moment, now time.Time) string {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	days := int(now.UTC().Truncate(24*time.Hour).Sub(moment.UTC().Truncate(24*time.Hour)).Hours() / 24)
	switch {
	case days <= 0:
		return "today"
	case days == 1:
		return "yesterday"
	case days < 7:
		return moment.UTC().Format("Mon")
	default:
		return moment.UTC().Format("Jan 02, 2006")
	}
}

// v1Search searches everything a reader may see and describes each match.
func (h *V1Handler) v1Search(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "cql", "cqlcontext", "cursor", "next", "prev", "limit", "start",
		"includeArchivedSpaces", "excludeCurrentSpaces", "excerpt", "sitePermissionTypeFilter", "expand", "_") {
		return
	}
	scope, ok := h.readSearchScope(w, r, store.AllWikiSearchEntityTypes, 25)
	if !ok {
		return
	}
	began := time.Now()
	results, total, err := h.Store.SearchWiki(r.Context(), ws, actor, scope.request)
	if err != nil {
		writeError(w, err)
		return
	}
	beans := make([]any, 0, len(results))
	archived := 0
	for _, result := range results {
		beans = append(beans, h.searchResultBean(result, scope))
	}
	respond(w, 200, map[string]any{
		"results": beans, "start": scope.request.Start, "limit": scope.request.Limit,
		"size": len(beans), "totalSize": total, "cqlQuery": scope.request.CQL,
		"searchDuration":      int(time.Since(began).Milliseconds()),
		"archivedResultCount": archived,
		"_links":              h.searchLinks(r, scope, len(beans), total),
	})
}

// v1ContentSearch searches content and returns the content itself. A space is
// not content, so it is never a result here however the query is written.
func (h *V1Handler) v1ContentSearch(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "cql", "cqlcontext", "cursor", "limit", "expand") {
		return
	}
	scope, ok := h.readSearchScope(w, r, store.WikiSearchContentTypes, 25)
	if !ok {
		return
	}
	results, total, err := h.Store.SearchWiki(r.Context(), ws, actor, scope.request)
	if err != nil {
		writeError(w, err)
		return
	}
	beans := make([]any, 0, len(results))
	for _, result := range results {
		beans = append(beans, h.searchContentBean(result))
	}
	respond(w, 200, map[string]any{
		"results": beans, "start": scope.request.Start, "limit": scope.request.Limit,
		"size": len(beans), "totalSize": total,
		"_links": h.searchLinks(r, scope, len(beans), total),
	})
}
