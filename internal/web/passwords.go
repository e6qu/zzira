package web

import (
	"errors"
	"net/http"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/store"
)

// A sign-in link is how someone whose account has no password anyone knows --
// an invitation, or a forgotten password -- sets one. The link is the only
// credential involved, so the page it opens says what it is for and nothing
// about whose account it is.

type passwordLinkPageData struct {
	// Token is the link this page was opened with, carried into the form so
	// the browser does not have to keep it in the URL after the answer.
	Token string
	// PasswordMinimum is the rule the person's authentication policy
	// applies, shown before they type a password.
	PasswordMinimum int
	Error           string
	// Invalid is a link that has been used, has expired, or was never
	// issued. The page then offers no form: there is nothing to fill in.
	Invalid bool
}

// SetPasswordForm opens a sign-in link.
func (h *Handler) SetPasswordForm(w http.ResponseWriter, r *http.Request) {
	noStoreAuthResponse(w)
	token := r.URL.Query().Get("token")
	h.writePasswordLinkPage(w, r, token, "", http.StatusOK)
}

// SetPasswordSubmit spends a sign-in link on the password just typed.
func (h *Handler) SetPasswordSubmit(w http.ResponseWriter, r *http.Request) {
	noStoreAuthResponse(w)
	if !parseForm(w, r) {
		return
	}
	token, password := r.PostFormValue("token"), r.PostFormValue("newPassword")
	if password != r.PostFormValue("confirmPassword") {
		h.writePasswordLinkPage(w, r, token, "The password and its confirmation are different.", http.StatusBadRequest)
		return
	}
	err := authn.SetPasswordWithLink(r.Context(), h.Store, token, password)
	var refused authn.ErrPasswordRefused
	switch {
	case err == nil:
		http.Redirect(w, r, "/login?saved=password", http.StatusSeeOther)
	case errors.As(err, &refused):
		h.writePasswordLinkPage(w, r, token, refused.Reason, http.StatusBadRequest)
	default:
		h.writePasswordLinkPage(w, r, token, "", http.StatusBadRequest)
	}
}

// writePasswordLinkPage reads what the link is still good for and renders the
// page for it: a form, or what became of the link.
func (h *Handler) writePasswordLinkPage(w http.ResponseWriter, r *http.Request, token, message string, status int) {
	data := passwordLinkPageData{Token: token, PasswordMinimum: store.MinimumPasswordLength, Error: message}
	_, policy, err := authn.PasswordLinkPolicy(r.Context(), h.Store, token)
	switch {
	case err == nil:
		data.PasswordMinimum = policy.PasswordMinimum()
	case errors.Is(err, store.ErrPasswordLinkInvalid):
		data.Invalid, data.Error = true, "That sign-in link has been used or has expired. Ask an administrator for another."
		status = http.StatusNotFound
	case errors.Is(err, authn.ErrSSORequired):
		data.Invalid, data.Error = true, "Your organization signs this account in through its identity provider, so it has no password to set."
		status = http.StatusForbidden
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writePageStatus(w, "page_password_link", data, status)
}
