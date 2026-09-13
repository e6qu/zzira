package confluence

import (
	"net/http"

	"github.com/e6qu/zzira/internal/authn"
)

func (h *Handler) checkAccessByEmail(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r) {
		return
	}
	var input struct {
		Emails []string `json:"emails"`
	}
	if !decode(w, r, &input) {
		return
	}
	without, invalid, err := h.Store.WikiEmailsWithoutAccess(r.Context(), ws, actor, input.Emails)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, map[string]any{"emailsWithoutAccess": without, "invalidEmails": invalid})
}

// inviteByEmail invites the addresses that have no access. Confluence documents
// it as asynchronous; here the invitations are complete before the answer.
func (h *Handler) inviteByEmail(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r) {
		return
	}
	var input struct {
		Emails []string `json:"emails"`
	}
	if !decode(w, r, &input) {
		return
	}
	passwordHash, err := authn.UnusablePasswordHash()
	if err != nil {
		failure(w, 500, "Could not prepare the invitation.")
		return
	}
	if err = h.Store.InviteWikiUsersByEmail(r.Context(), ws, actor, input.Emails, passwordHash); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(200)
}
