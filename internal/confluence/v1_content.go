package confluence

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// Confluence's v1 content bean is small until asked for more: each part a
// caller does not expand is listed under `_expandable`, and each part it names
// in `expand` is filled in. The same bean answers a copy and a descendant read.

type v1Expansions map[string]bool

// parseV1Expand reads `expand`, which may repeat and may list several parts
// separated by commas. Confluence caps how many a request may ask for.
func parseV1Expand(w http.ResponseWriter, r *http.Request, maximum int) (v1Expansions, bool) {
	expand := v1Expansions{}
	for _, raw := range r.URL.Query()["expand"] {
		for _, value := range strings.Split(raw, ",") {
			if value = strings.TrimSpace(value); value != "" {
				expand[value] = true
			}
		}
	}
	if maximum > 0 && len(expand) > maximum {
		failure(w, 400, fmt.Sprintf("At most %d expansions can be requested.", maximum))
		return nil, false
	}
	return expand, true
}

// wants reports whether a part, or anything beneath it, was asked for.
func (e v1Expansions) wants(part string) bool {
	for key := range e {
		if key == part || strings.HasPrefix(key, part+".") {
			return true
		}
	}
	return false
}

func (h *V1Handler) v1ContentLinks(spaceID, contentType, id string) map[string]string {
	webui := "/spaces/" + spaceID + "/pages/" + id
	if contentType != "page" {
		webui = "/spaces/" + spaceID + "/" + contentType + "s/" + id
	}
	return map[string]string{"webui": webui, "self": h.BaseURL + "/wiki/rest/api/content/" + id, "base": h.BaseURL + "/wiki"}
}

func v1UserReference(accountID string) map[string]any {
	return map[string]any{"type": "known", "accountId": accountID, "accountType": "atlassian"}
}

func v1VersionBean(version models.WikiVersion, contentID, base string) map[string]any {
	return map[string]any{
		"number": version.Number, "message": version.Message, "minorEdit": version.MinorEdit,
		"when": version.CreatedAt, "by": v1UserReference(version.AuthorID),
		"_links": map[string]string{"self": base + "/wiki/rest/api/content/" + contentID + "/version/" + fmt.Sprint(version.Number)},
	}
}

func contentArrayBean(values []any, self string) map[string]any {
	return map[string]any{"results": values, "start": 0, "limit": len(values), "size": len(values), "_links": map[string]string{"self": self}}
}

// v1PageBean renders a page as v1 content with the requested expansions.
func (h *V1Handler) v1PageBean(ctx context.Context, ws, actor string, page *models.WikiPage, expand v1Expansions) (map[string]any, error) {
	self := h.BaseURL + "/wiki/rest/api/content/" + page.ID
	bean := map[string]any{
		"id": page.ID, "type": "page", "status": page.Status, "title": page.Title,
		"_links": h.v1ContentLinks(page.SpaceID, "page", page.ID),
	}
	expandable := map[string]string{}
	part := func(name string, build func() (any, error)) error {
		if !expand.wants(name) {
			expandable[name] = ""
			return nil
		}
		value, err := build()
		if err != nil {
			return err
		}
		bean[name] = value
		return nil
	}
	spaceBean := func() (any, error) {
		space, err := h.Store.WikiSpace(ctx, ws, actor, page.SpaceID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"id": space.ID, "key": space.Key, "name": space.Name, "type": space.Type, "status": space.Status,
			"_links": map[string]string{"webui": "/spaces/" + space.Key, "self": h.BaseURL + "/wiki/rest/api/space/" + space.Key}}, nil
	}
	steps := []struct {
		name  string
		build func() (any, error)
	}{
		{"space", spaceBean},
		{"container", spaceBean},
		{"version", func() (any, error) { return v1VersionBean(page.Version, page.ID, h.BaseURL), nil }},
		{"history", func() (any, error) {
			history := map[string]any{
				"latest": page.Status == "current", "createdBy": v1UserReference(page.AuthorID), "createdDate": page.CreatedAt,
				"lastUpdated": v1VersionBean(page.Version, page.ID, h.BaseURL),
				"_links":      map[string]string{"self": self + "/history"},
			}
			if page.OwnerID != "" {
				history["ownedBy"] = v1UserReference(page.OwnerID)
			}
			return history, nil
		}},
		{"body", func() (any, error) {
			body := map[string]any{}
			bodyExpandable := map[string]string{}
			for _, representation := range []string{"storage", "view", "export_view", "styled_view", "editor", "atlas_doc_format", "anonymous_export_view"} {
				if !expand["body."+representation] {
					bodyExpandable[representation] = ""
					continue
				}
				target := representation
				if target == "anonymous_export_view" {
					target = "export_view"
				}
				value := page.Body.Value
				if target != "storage" {
					converted, err := store.ConvertWikiBody(page.Body.Value, "storage", target)
					if err != nil {
						return nil, err
					}
					value = converted
				}
				body[representation] = map[string]any{"value": value, "representation": representation, "_expandable": map[string]string{"content": self}}
			}
			body["_expandable"] = bodyExpandable
			return body, nil
		}},
		{"ancestors", func() (any, error) {
			relations, err := h.Store.WikiTreeAncestors(ctx, ws, actor, page.ID, "page")
			if err != nil {
				return nil, err
			}
			values := []any{}
			for _, relation := range relations {
				values = append(values, h.v1TreeNodeBean(relation))
			}
			return values, nil
		}},
		{"metadata", func() (any, error) {
			metadata := map[string]any{}
			metadataExpandable := map[string]string{"currentuser": "", "frontend": "", "editorHtml": ""}
			if expand["metadata.labels"] {
				labels, err := h.Store.WikiPageLabels(ctx, ws, actor, page.ID)
				if err != nil {
					return nil, err
				}
				values := make([]any, 0, len(labels))
				for _, label := range labels {
					values = append(values, v1LabelBean(label))
				}
				metadata["labels"] = contentArrayBean(values, self+"/label")
			} else {
				metadataExpandable["labels"] = ""
			}
			if expand["metadata.properties"] {
				properties, err := h.Store.WikiPageProperties(ctx, ws, actor, page.ID, "")
				if err != nil {
					return nil, err
				}
				values := map[string]any{}
				for _, property := range properties {
					values[property.Key] = map[string]any{"key": property.Key, "value": property.Value, "version": property.Version}
				}
				metadata["properties"] = values
			} else {
				metadataExpandable["properties"] = ""
			}
			metadata["_expandable"] = metadataExpandable
			return metadata, nil
		}},
		{"operations", func() (any, error) {
			canUpdate, err := h.Store.CanUpdateWikiPage(ctx, ws, actor, page.ID)
			if err != nil {
				return nil, err
			}
			canDelete, err := h.Store.CanDeleteWikiPage(ctx, ws, actor, page.ID)
			if err != nil {
				return nil, err
			}
			return pageOperationsFor(canUpdate, canDelete), nil
		}},
		{"restrictions", func() (any, error) {
			restrictions, err := h.Store.WikiPageRestrictions(ctx, ws, actor, page.ID)
			if err != nil {
				return nil, err
			}
			values := map[string]any{}
			for _, restriction := range restrictions {
				values[restriction.Operation] = h.restrictionBean(page.ID, restriction)
			}
			for _, operation := range []string{"read", "update"} {
				if _, ok := values[operation]; !ok {
					values[operation] = h.restrictionBean(page.ID, models.WikiPageRestriction{Operation: operation})
				}
			}
			return values, nil
		}},
		{"childTypes", func() (any, error) {
			children, err := h.v1Descendants(ctx, ws, actor, page, 1)
			if err != nil {
				return nil, err
			}
			types := map[string]any{}
			for _, kind := range v1ChildKinds {
				types[kind] = map[string]any{"value": len(children[kind]) > 0, "_links": map[string]string{"self": self + "/child/" + kind}}
			}
			return types, nil
		}},
		{"children", func() (any, error) { return h.v1ChildrenBean(ctx, ws, actor, page, expand, "children", 1) }},
		{"descendants", func() (any, error) { return h.v1ChildrenBean(ctx, ws, actor, page, expand, "descendants", 100) }},
	}
	for _, step := range steps {
		if err := part(step.name, step.build); err != nil {
			return nil, err
		}
	}
	bean["_expandable"] = expandable
	return bean, nil
}

var v1ChildKinds = []string{"attachment", "comment", "page", "whiteboard", "database", "embed", "folder"}

// v1ChildrenBean fills a children or descendants expansion with the kinds the
// caller named, such as `children.page`, and lists the rest as expandable.
func (h *V1Handler) v1ChildrenBean(ctx context.Context, ws, actor string, page *models.WikiPage, expand v1Expansions, name string, depth int) (map[string]any, error) {
	found, err := h.v1Descendants(ctx, ws, actor, page, depth)
	if err != nil {
		return nil, err
	}
	self := h.BaseURL + "/wiki/rest/api/content/" + page.ID + "/" + strings.TrimSuffix(name, "ren")
	if name == "descendants" {
		self = h.BaseURL + "/wiki/rest/api/content/" + page.ID + "/descendant"
	}
	bean := map[string]any{"_links": map[string]string{"self": self, "base": h.BaseURL + "/wiki"}}
	expandable := map[string]string{}
	for _, kind := range v1ChildKinds {
		if expand[name+"."+kind] {
			bean[kind] = contentArrayBean(found[kind], self+"/"+kind)
		} else {
			expandable[kind] = ""
		}
	}
	bean["_expandable"] = expandable
	return bean, nil
}

// v1TreeNodeBean is the short bean for a page or content in the tree.
func (h *V1Handler) v1TreeNodeBean(relation store.WikiTreeRelation) map[string]any {
	return map[string]any{
		"id": relation.ID(), "type": relation.Type(), "status": relation.Status(), "title": relation.Title(),
		"_links": h.v1ContentLinks(relation.SpaceID(), relation.Type(), relation.ID()),
	}
}

func (h *V1Handler) v1CommentBean(page *models.WikiPage, comment *models.WikiFooterComment) map[string]any {
	location := "footer"
	if comment.CommentType == "inline" {
		location = "inline"
	}
	extensions := map[string]any{"location": location}
	if location == "inline" {
		extensions["inlineProperties"] = map[string]any{"originalSelection": comment.InlineSelection, "markerRef": comment.InlineMarkerRef}
		extensions["resolution"] = map[string]any{"status": comment.ResolutionStatus}
	}
	return map[string]any{
		"id": comment.ID, "type": "comment", "status": "current", "title": "Re: " + page.Title,
		"container":  map[string]any{"id": page.ID, "type": "page", "title": page.Title},
		"extensions": extensions,
		"_links": map[string]string{
			"webui": "/spaces/" + page.SpaceID + "/pages/" + page.ID + "#comment-" + comment.ID,
			"self":  h.BaseURL + "/wiki/rest/api/content/" + comment.ID, "base": h.BaseURL + "/wiki",
		},
	}
}

// v1Descendants gathers everything beneath a page to a depth, by kind. A
// page's comments and attachments are one level beneath it, a reply is one
// level beneath the comment it answers, and a child page's own comments and
// attachments are one level beneath that page.
func (h *V1Handler) v1Descendants(ctx context.Context, ws, actor string, root *models.WikiPage, depth int) (map[string][]any, error) {
	found := map[string][]any{}
	for _, kind := range v1ChildKinds {
		found[kind] = []any{}
	}
	type placedPage struct {
		page  *models.WikiPage
		depth int
	}
	pages := []placedPage{{root, 0}}
	if depth > 1 || depth == 1 {
		relations, err := h.Store.WikiTreeDescendants(ctx, ws, actor, root.ID, "page", depth)
		if err != nil {
			return nil, err
		}
		for _, relation := range relations {
			found[relation.Type()] = append(found[relation.Type()], h.v1TreeNodeBean(relation))
			if relation.Page != nil && relation.Page.Status == "current" && relation.Depth < depth {
				pages = append(pages, placedPage{relation.Page, relation.Depth})
			}
		}
	}
	for _, placed := range pages {
		// The thread reads include replies, which the page's comment lists
		// leave to each comment's children.
		footer, err := h.Store.WikiFooterCommentThread(ctx, ws, actor, placed.page.ID)
		if err != nil {
			return nil, err
		}
		inline, err := h.Store.WikiInlineCommentThread(ctx, ws, actor, placed.page.ID)
		if err != nil {
			return nil, err
		}
		comments := append(append([]*models.WikiFooterComment{}, footer...), inline...)
		parents := map[string]string{}
		for _, comment := range comments {
			parents[comment.ID] = comment.ParentCommentID
		}
		for _, comment := range comments {
			level := placed.depth + 1
			for parent := comment.ParentCommentID; parent != ""; parent = parents[parent] {
				level++
			}
			if level <= depth {
				found["comment"] = append(found["comment"], h.v1CommentBean(placed.page, comment))
			}
		}
		if placed.depth+1 <= depth {
			attachments, err := h.Store.WikiAttachments(ctx, ws, actor, placed.page.ID, "", "", "current")
			if err != nil {
				return nil, err
			}
			for _, attachment := range attachments {
				found["attachment"] = append(found["attachment"], h.v1AttachmentBean(attachment))
			}
		}
	}
	return found, nil
}
