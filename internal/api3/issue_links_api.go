package api3

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// Issue link types, issue links and remote issue links.
//
// Jira names a link's sides by what each issue's view shows: for Blocks, the
// inward issue reads "blocks" the outward issue, which reads "is blocked by".
// Stored links keep the source issue as outward_id, so a request's inwardIssue
// is the stored outward side.

func (h *Handler) issueLinkTypeBean(lt *models.LinkType) map[string]any {
	id := strconv.FormatInt(lt.JiraID, 10)
	return map[string]any{"id": id, "name": lt.Name, "inward": lt.Inward, "outward": lt.Outward, "self": h.BaseURL + "/rest/api/3/issueLinkType/" + id}
}

// issueLinkingEnabled answers 404 when the site has issue linking switched off.
func (h *Handler) issueLinkingEnabled(w http.ResponseWriter, r *http.Request, workspaceID string) bool {
	configuration, err := h.Store.JiraSiteConfiguration(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return false
	}
	if !configuration.IssueLinkingEnabled {
		jiraError(w, http.StatusNotFound, "Issue linking is disabled.")
		return false
	}
	return true
}

func numericID(ref string) bool {
	_, err := strconv.ParseInt(strings.TrimSpace(ref), 10, 64)
	return err == nil
}

// issueLinkTypeRoute serves /issueLinkType and /issueLinkType/{id}.
func (h *Handler) issueLinkTypeRoute(w http.ResponseWriter, r *http.Request, ref string) {
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if !h.issueLinkingEnabled(w, r, workspaceID) {
		return
	}
	requireAdmin := func() bool {
		admin, err := h.Store.IsAdmin(r.Context(), workspaceID, actorID)
		if err != nil || !admin {
			jiraError(w, http.StatusNotFound, "You do not have the permission to manage issue link types.")
			return false
		}
		return true
	}
	var request struct {
		Name    *string `json:"name"`
		Inward  *string `json:"inward"`
		Outward *string `json:"outward"`
	}
	if ref == "" {
		switch r.Method {
		case http.MethodGet:
			types, err := h.Store.LinkTypes(r.Context(), workspaceID)
			if err != nil {
				jiraError(w, http.StatusInternalServerError, "internal error")
				return
			}
			values := make([]map[string]any, 0, len(types))
			for _, lt := range types {
				values = append(values, h.issueLinkTypeBean(lt))
			}
			writeJSON(w, http.StatusOK, map[string]any{"issueLinkTypes": values})
		case http.MethodPost:
			if !requireAdmin() || !decodeMetadataRequest(w, r, &request) {
				return
			}
			lt, err := h.Store.CreateLinkType(r.Context(), workspaceID, derefString(request.Name), derefString(request.Inward), derefString(request.Outward))
			switch {
			case errors.Is(err, store.ErrLinkTypeNameInUse):
				jiraError(w, http.StatusNotFound, "The issue link type name is in use.")
			case errors.Is(err, store.ErrLinkTypeValidation):
				jiraError(w, http.StatusBadRequest, trimErrorPrefix(err))
			case err != nil:
				jiraError(w, http.StatusInternalServerError, "internal error")
			default:
				writeJSON(w, http.StatusCreated, h.issueLinkTypeBean(lt))
			}
		default:
			methodNotAllowed(w)
		}
		return
	}
	if !numericID(ref) {
		jiraError(w, http.StatusBadRequest, "The issue link type ID is invalid.")
		return
	}
	switch r.Method {
	case http.MethodGet:
		lt, err := h.Store.LinkTypeByRef(r.Context(), workspaceID, ref)
		if err != nil {
			jiraError(w, http.StatusNotFound, "No issue link type with ID '"+ref+"' found.")
			return
		}
		writeJSON(w, http.StatusOK, h.issueLinkTypeBean(lt))
	case http.MethodPut:
		if !requireAdmin() || !decodeMetadataRequest(w, r, &request) {
			return
		}
		lt, err := h.Store.UpdateLinkType(r.Context(), workspaceID, ref, request.Name, request.Inward, request.Outward)
		switch {
		case errors.Is(err, store.ErrLinkTypeNotFound):
			jiraError(w, http.StatusNotFound, "No issue link type with ID '"+ref+"' found.")
		case errors.Is(err, store.ErrLinkTypeNameInUse):
			jiraError(w, http.StatusBadRequest, "The issue link type name is in use.")
		case errors.Is(err, store.ErrLinkTypeValidation):
			jiraError(w, http.StatusBadRequest, trimErrorPrefix(err))
		case err != nil:
			jiraError(w, http.StatusInternalServerError, "internal error")
		default:
			writeJSON(w, http.StatusOK, h.issueLinkTypeBean(lt))
		}
	case http.MethodDelete:
		if !requireAdmin() {
			return
		}
		err := h.Store.DeleteLinkType(r.Context(), actorID, workspaceID, ref)
		switch {
		case errors.Is(err, store.ErrLinkTypeNotFound):
			jiraError(w, http.StatusNotFound, "No issue link type with ID '"+ref+"' found.")
		case err != nil:
			jiraError(w, http.StatusInternalServerError, "internal error")
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	default:
		methodNotAllowed(w)
	}
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// linkedIssueBean is the short form of an issue on the other side of a link.
func (h *Handler) linkedIssueBean(issue *models.Issue) map[string]any {
	id := jiraIssueID(issue)
	fields := map[string]any{
		"summary":   issue.Summary,
		"status":    h.statusBean(models.Status{ID: issue.Status.ID, Name: issue.Status.Name, Category: issue.Status.Category}),
		"issuetype": h.issueTypeBean(issue.IssueType),
	}
	if issue.Priority != nil {
		fields["priority"] = h.priorityBean(*issue.Priority)
	}
	return map[string]any{"id": id, "key": issue.Key, "self": h.BaseURL + "/rest/api/3/issue/" + id, "fields": fields}
}

// issueLinkBeanFor is a link as it appears in an issue's issuelinks field: the
// issue on the other side, named by the side it is on.
func (h *Handler) issueLinkBeanFor(link *models.IssueLink, current, other *models.Issue) map[string]any {
	id := strconv.FormatInt(link.JiraID, 10)
	bean := map[string]any{
		"id":   id,
		"self": h.BaseURL + "/rest/api/3/issueLink/" + id,
		"type": h.issueLinkTypeBean(&models.LinkType{JiraID: link.TypeJiraID, Name: link.TypeName, Inward: link.Inward, Outward: link.Outward}),
	}
	if link.OutwardID == current.ID {
		bean["outwardIssue"] = h.linkedIssueBean(other)
	} else {
		bean["inwardIssue"] = h.linkedIssueBean(other)
	}
	return bean
}

// issueLinkRoute serves POST /issueLink and GET or DELETE /issueLink/{id}.
func (h *Handler) issueLinkRoute(w http.ResponseWriter, r *http.Request, ref string) {
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if ref == "" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		h.createIssueLink(w, r, workspaceID, actorID)
		return
	}
	if !numericID(ref) {
		jiraError(w, http.StatusBadRequest, "The issue link ID is invalid.")
		return
	}
	if !h.issueLinkingEnabled(w, r, workspaceID) {
		return
	}
	link, err := h.Store.IssueLinkByID(r.Context(), workspaceID, ref)
	if err != nil {
		jiraError(w, http.StatusNotFound, "No issue link with id '"+ref+"' exists.")
		return
	}
	source, sourceErr := h.resolveIssue(r, workspaceID, link.OutwardID)
	target, targetErr := h.resolveIssue(r, workspaceID, link.InwardID)
	if sourceErr != nil || targetErr != nil {
		jiraError(w, http.StatusNotFound, "No issue link with id '"+ref+"' exists.")
		return
	}
	switch r.Method {
	case http.MethodGet:
		id := strconv.FormatInt(link.JiraID, 10)
		writeJSON(w, http.StatusOK, map[string]any{
			"id": id, "self": h.BaseURL + "/rest/api/3/issueLink/" + id,
			"type":         h.issueLinkTypeBean(&models.LinkType{JiraID: link.TypeJiraID, Name: link.TypeName, Inward: link.Inward, Outward: link.Outward}),
			"inwardIssue":  h.linkedIssueBean(source),
			"outwardIssue": h.linkedIssueBean(target),
		})
	case http.MethodDelete:
		canLink := false
		for _, issue := range []*models.Issue{source, target} {
			if allowed, permErr := h.Store.HasProjectPermission(r.Context(), workspaceID, actorID, issue.ProjectID, issue.ID, "LINK_ISSUES"); permErr == nil && allowed {
				canLink = true
			}
		}
		if !canLink {
			jiraError(w, http.StatusNotFound, "You do not have the permission to delete links in these issues.")
			return
		}
		if _, err = h.Commands.DeleteIssueLink(r.Context(), actorID, workspaceID, source.ID, link.ID); err != nil {
			issueCommandError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w)
	}
}

// issueCommandError maps a failed issue command to Jira's answer.
func issueCommandError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, commands.ErrIssueArchived):
		jiraError(w, http.StatusBadRequest, "The issue is archived and can't be changed.")
	case errors.Is(err, commands.ErrCommentPermission):
		jiraError(w, http.StatusBadRequest, "You do not have the permission to change this comment.")
	case errors.Is(err, store.ErrCommentNotFound):
		jiraError(w, http.StatusNotFound, "The comment does not exist.")
	case strings.Contains(err.Error(), "not found"):
		jiraError(w, http.StatusNotFound, "Issue does not exist or you do not have permission to see it.")
	default:
		jiraError(w, http.StatusBadRequest, err.Error())
	}
}

type linkedIssueRef struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

func (ref linkedIssueRef) value() string {
	if strings.TrimSpace(ref.Key) != "" {
		return ref.Key
	}
	return ref.ID
}

func (h *Handler) createIssueLink(w http.ResponseWriter, r *http.Request, workspaceID, actorID string) {
	var request struct {
		Type struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"type"`
		InwardIssue  linkedIssueRef   `json:"inwardIssue"`
		OutwardIssue linkedIssueRef   `json:"outwardIssue"`
		Comment      *json.RawMessage `json:"comment"`
	}
	if !decodeMetadataRequest(w, r, &request) {
		return
	}
	if request.InwardIssue.value() == "" || request.OutwardIssue.value() == "" || (request.Type.ID == "" && request.Type.Name == "") {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"issueLinkType": "type, inwardIssue and outwardIssue are required."})
		return
	}
	if !h.issueLinkingEnabled(w, r, workspaceID) {
		return
	}
	var linkType *models.LinkType
	var err error
	if request.Type.ID != "" {
		linkType, err = h.Store.LinkTypeByRef(r.Context(), workspaceID, request.Type.ID)
	} else {
		var typeID string
		if typeID, err = h.Store.LinkTypeIDByName(r.Context(), workspaceID, request.Type.Name); err == nil {
			linkType, err = h.Store.LinkTypeByRef(r.Context(), workspaceID, typeID)
		}
	}
	if err != nil {
		jiraError(w, http.StatusNotFound, "No issue link type with name '"+request.Type.Name+request.Type.ID+"' found.")
		return
	}
	source, e := h.resolveIssue(r, workspaceID, request.InwardIssue.value())
	if e != nil {
		writeJerr(w, e)
		return
	}
	target, e := h.resolveIssue(r, workspaceID, request.OutwardIssue.value())
	if e != nil {
		writeJerr(w, e)
		return
	}
	if allowed, permErr := h.Store.HasProjectPermission(r.Context(), workspaceID, actorID, source.ProjectID, source.ID, "LINK_ISSUES"); permErr != nil || !allowed {
		jiraError(w, http.StatusNotFound, "You do not have the permission to link issues in the project.")
		return
	}
	for _, issue := range []*models.Issue{source, target} {
		links, countErr := h.Store.LinksByIssue(r.Context(), issue.ID)
		if countErr != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if len(links) >= store.IssueEntityLimits["issuelinks"] {
			jiraError(w, http.StatusRequestEntityTooLarge, "The per-issue limit for issue links has been breached.")
			return
		}
	}
	var comment *commentInput
	if request.Comment != nil && string(*request.Comment) != "null" {
		parsed, commentErr := h.parseCommentInput(r, workspaceID, *request.Comment, true)
		if commentErr != nil {
			jiraError(w, http.StatusBadRequest, commentErr.Error())
			return
		}
		comment = &parsed
	}
	// The link's source is the inward issue; the comment goes on the outward one.
	if _, _, err = h.Commands.LinkIssue(r.Context(), actorID, workspaceID, source.ID, linkType.ID, target.ID); err != nil {
		issueCommandError(w, err)
		return
	}
	if comment != nil {
		if _, e := h.createCommentOn(r, workspaceID, actorID, target, *comment); e != nil {
			writeJerr(w, e)
			return
		}
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) remoteLinkBean(issue *models.Issue, link store.RemoteIssueLink) map[string]any {
	id := strconv.FormatInt(link.ID, 10)
	bean := map[string]any{"id": link.ID, "self": h.BaseURL + "/rest/api/3/issue/" + jiraIssueID(issue) + "/remotelink/" + id, "object": link.Object}
	if link.GlobalID != nil {
		bean["globalId"] = *link.GlobalID
	}
	if len(link.Application) > 0 {
		bean["application"] = link.Application
	} else {
		bean["application"] = map[string]any{}
	}
	if link.Relationship != nil {
		bean["relationship"] = *link.Relationship
	}
	return bean
}

type remoteLinkRequest struct {
	GlobalID     *string         `json:"globalId"`
	Application  json.RawMessage `json:"application"`
	Relationship *string         `json:"relationship"`
	Object       json.RawMessage `json:"object"`
}

func (request remoteLinkRequest) input() store.RemoteIssueLinkInput {
	return store.RemoteIssueLinkInput{GlobalID: request.GlobalID, Application: request.Application, Relationship: request.Relationship, Object: request.Object}
}

// remoteLinkRoute serves /issue/{key}/remotelink and /issue/{key}/remotelink/{id}.
func (h *Handler) remoteLinkRoute(w http.ResponseWriter, r *http.Request, idOrKey, linkRef string) {
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	issue, e := h.resolveIssue(r, workspaceID, idOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if !h.issueLinkingEnabled(w, r, workspaceID) {
		return
	}
	canChange := func() bool {
		for _, permission := range []string{"LINK_ISSUES", "EDIT_ISSUES"} {
			if allowed, err := h.Store.HasProjectPermission(r.Context(), workspaceID, actorID, issue.ProjectID, issue.ID, permission); err != nil || !allowed {
				jiraError(w, http.StatusForbidden, "You do not have the permission to link issues.")
				return false
			}
		}
		if issue.ArchivedAt != "" {
			jiraError(w, http.StatusBadRequest, "The issue is archived and can't be changed.")
			return false
		}
		return true
	}
	remoteError := func(err error) {
		switch {
		case errors.Is(err, store.ErrRemoteLinkValidation):
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"object": trimErrorPrefix(err)})
		case errors.Is(err, store.ErrRemoteLinkNotFound):
			jiraError(w, http.StatusNotFound, "The remote issue link was not found.")
		default:
			jiraError(w, http.StatusInternalServerError, "internal error")
		}
	}
	if linkRef == "" {
		switch r.Method {
		case http.MethodGet:
			if globalID := r.URL.Query().Get("globalId"); r.URL.Query().Has("globalId") {
				link, err := h.Store.RemoteIssueLinkByGlobalID(r.Context(), workspaceID, issue.ID, globalID)
				if err != nil {
					remoteError(err)
					return
				}
				writeJSON(w, http.StatusOK, h.remoteLinkBean(issue, link))
				return
			}
			links, err := h.Store.RemoteIssueLinks(r.Context(), workspaceID, issue.ID)
			if err != nil {
				remoteError(err)
				return
			}
			values := make([]map[string]any, 0, len(links))
			for _, link := range links {
				values = append(values, h.remoteLinkBean(issue, link))
			}
			writeJSON(w, http.StatusOK, values)
		case http.MethodPost:
			var request remoteLinkRequest
			if !canChange() || !decodeMetadataRequest(w, r, &request) {
				return
			}
			if request.GlobalID == nil {
				if count, err := h.Store.RemoteIssueLinkCount(r.Context(), issue.ID); err != nil || count >= store.IssueEntityLimits["remoteIssueLinks"] {
					jiraError(w, http.StatusRequestEntityTooLarge, "The per-issue limit for remote links has been breached.")
					return
				}
			}
			link, created, err := h.Store.SaveRemoteIssueLink(r.Context(), workspaceID, issue.ID, request.input())
			if err != nil {
				remoteError(err)
				return
			}
			status := http.StatusOK
			if created {
				status = http.StatusCreated
			}
			writeJSON(w, status, map[string]any{"id": link.ID, "self": h.BaseURL + "/rest/api/3/issue/" + jiraIssueID(issue) + "/remotelink/" + strconv.FormatInt(link.ID, 10)})
		case http.MethodDelete:
			if !r.URL.Query().Has("globalId") || r.URL.Query().Get("globalId") == "" {
				jiraError(w, http.StatusBadRequest, "The globalId parameter is required.")
				return
			}
			if !canChange() {
				return
			}
			if err := h.Store.DeleteRemoteIssueLinkByGlobalID(r.Context(), workspaceID, issue.ID, r.URL.Query().Get("globalId")); err != nil {
				remoteError(err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			methodNotAllowed(w)
		}
		return
	}
	id, err := strconv.ParseInt(linkRef, 10, 64)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "The remote issue link ID is invalid.")
		return
	}
	if _, err = h.Store.RemoteIssueLink(r.Context(), workspaceID, issue.ID, id); errors.Is(err, store.ErrRemoteLinkNotFound) {
		if h.Store.RemoteIssueLinkExists(r.Context(), workspaceID, id) {
			jiraError(w, http.StatusBadRequest, "The remote issue link does not belong to the issue.")
			return
		}
		remoteError(err)
		return
	} else if err != nil {
		remoteError(err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		link, _ := h.Store.RemoteIssueLink(r.Context(), workspaceID, issue.ID, id)
		writeJSON(w, http.StatusOK, h.remoteLinkBean(issue, link))
	case http.MethodPut:
		var request remoteLinkRequest
		if !canChange() || !decodeMetadataRequest(w, r, &request) {
			return
		}
		if err = h.Store.UpdateRemoteIssueLink(r.Context(), workspaceID, issue.ID, id, request.input()); err != nil {
			remoteError(err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if !canChange() {
			return
		}
		if err = h.Store.DeleteRemoteIssueLink(r.Context(), workspaceID, issue.ID, id); err != nil {
			remoteError(err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w)
	}
}
