package api3

import (
	"net/http"
	"strconv"

	"github.com/e6qu/zzira/internal/store"
)

// holderExpander adds the user, group, project role and field details a
// permission holder or notification recipient names when a request expands
// them, as Jira's scheme reads do.
type holderExpander struct {
	h           *Handler
	r           *http.Request
	workspaceID string
	expands     map[string]struct{}
	groups      map[string]store.SiteGroup
}

func (h *Handler) newHolderExpander(r *http.Request, workspaceID string) *holderExpander {
	return &holderExpander{h: h, r: r, workspaceID: workspaceID, expands: commaQuerySet(r, "expand")}
}

func (x *holderExpander) wants(name string) bool {
	return x != nil && querySetContains(x.expands, name, "all")
}

func (x *holderExpander) group(idOrName string) (store.SiteGroup, bool) {
	if x.groups == nil {
		x.groups = map[string]store.SiteGroup{}
		groups, err := x.h.Store.SiteGroups(x.r.Context(), x.workspaceID)
		if err != nil {
			return store.SiteGroup{}, false
		}
		for _, group := range groups {
			x.groups[group.ID] = group
			x.groups["name:"+group.Name] = group
		}
	}
	if group, ok := x.groups[idOrName]; ok {
		return group, true
	}
	group, ok := x.groups["name:"+idOrName]
	return group, ok
}

// expand adds to bean the details of what a holder of the given kind names.
// kind is user, group, projectRole or field; reference is the account, group,
// role or field the holder refers to.
func (x *holderExpander) expand(bean map[string]any, kind, reference string) {
	if kind == "" {
		return
	}
	bean["expand"] = kind
	if !x.wants(kind) || reference == "" {
		return
	}
	ctx := x.r.Context()
	switch kind {
	case "user":
		if user, err := x.h.Store.SiteUser(ctx, x.workspaceID, reference); err == nil {
			bean["user"] = x.h.userBeanFor(ctx, user)
		}
	case "group":
		if group, ok := x.group(reference); ok {
			bean["group"] = x.h.groupNameBean(group)
		}
	case "projectRole":
		if id, err := strconv.ParseInt(reference, 10, 64); err == nil {
			if role, roleErr := x.h.Store.ProjectRole(ctx, x.workspaceID, id); roleErr == nil {
				bean["projectRole"] = map[string]any{
					"id": role.ID, "name": role.Name, "description": role.Description,
					"self": x.h.BaseURL + "/rest/api/3/role/" + strconv.FormatInt(role.ID, 10),
				}
			}
		}
	case "field":
		if field, err := x.h.Store.CustomFieldByID(ctx, x.workspaceID, reference); err == nil {
			bean["field"] = x.h.customFieldBean(field)
		}
	}
}

// permissionHolderKind names the expansion a permission or security holder
// type offers.
func permissionHolderKind(holderType string) string {
	switch holderType {
	case "user", "group", "projectRole":
		return holderType
	case "userCustomField", "groupCustomField":
		return "field"
	}
	return ""
}

// notificationRecipientKind names the expansion a notification type offers.
func notificationRecipientKind(notificationType string) string {
	switch notificationType {
	case "User":
		return "user"
	case "Group":
		return "group"
	case "ProjectRole":
		return "projectRole"
	case "UserCustomField", "GroupCustomField":
		return "field"
	}
	return ""
}
