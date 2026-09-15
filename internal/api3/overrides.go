package api3

import (
	"context"
	"net/http"
	"strconv"

	"github.com/e6qu/zzira/internal/apps"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/commands"
)

// requestOverrides reads Jira's overrideScreenSecurity and
// overrideEditableFlag parameters, which only Connect and Forge apps with
// Administer Jira may use, and returns a context carrying the ones requested.
// allowed names the parameters the operation takes; others are ignored.
func (h *Handler) requestOverrides(r *http.Request, workspaceID, userID string, allowed ...string) (context.Context, *jerr) {
	var overrides commands.Overrides
	for _, name := range allowed {
		raw := r.URL.Query().Get(name)
		if raw == "" {
			continue
		}
		requested, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, &jerr{http.StatusBadRequest, name + " must be true or false.", nil}
		}
		switch name {
		case "overrideScreenSecurity":
			overrides.ScreenSecurity = requested
		case "overrideEditableFlag":
			overrides.EditableFlag = requested
		}
	}
	if !overrides.ScreenSecurity && !overrides.EditableFlag {
		return r.Context(), nil
	}
	_, isApp := apps.InstallationFromContext(r.Context())
	admin, err := authz.IsWorkspaceAdmin(r.Context(), h.Store, workspaceID, userID)
	if err != nil {
		return nil, &jerr{http.StatusInternalServerError, "internal error", nil}
	}
	if !isApp || !admin {
		return nil, &jerr{http.StatusForbidden, "Only Connect and Forge apps with the Administer Jira global permission can override screen security or the editable flag.", nil}
	}
	return commands.WithOverrides(r.Context(), overrides), nil
}
