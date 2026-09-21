package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

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

// ForgotPasswordCooldown is how long one account waits between the sign-in
// links this page sends, so the form cannot be used to fill a mailbox.
const ForgotPasswordCooldown = 5 * time.Minute

type forgotPasswordPageData struct {
	// Sent is the same answer whatever address was typed: the page does not
	// say who has an account here.
	Sent bool
	// Unavailable is a site that cannot send email, where a sign-in link
	// comes from an administrator instead.
	Unavailable bool
}

// ForgotPasswordForm asks for the address to send a sign-in link to.
func (h *Handler) ForgotPasswordForm(w http.ResponseWriter, r *http.Request) {
	noStoreAuthResponse(w)
	writePage(w, "page_password_forgot", forgotPasswordPageData{Unavailable: !h.canEmailSignInLinks()})
}

// ForgotPasswordSubmit sends a sign-in link to an address that has an
// account, and says the same thing either way.
func (h *Handler) ForgotPasswordSubmit(w http.ResponseWriter, r *http.Request) {
	noStoreAuthResponse(w)
	if !parseForm(w, r) {
		return
	}
	if !h.canEmailSignInLinks() {
		writePageStatus(w, "page_password_forgot", forgotPasswordPageData{Unavailable: true}, http.StatusServiceUnavailable)
		return
	}
	if err := h.emailSignInLink(r, strings.TrimSpace(r.PostFormValue("email"))); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writePage(w, "page_password_forgot", forgotPasswordPageData{Sent: true})
}

// canEmailSignInLinks says whether this site can carry a link to anyone: it
// needs somewhere to send from and its own address to write into the link.
func (h *Handler) canEmailSignInLinks() bool {
	return h.InvitationNotificationsConfigured && h.IdentityExternalURL != ""
}

// emailSignInLink sends one, if that address has an account that signs in
// with a password at all. Whatever it finds, the page says the same.
func (h *Handler) emailSignInLink(r *http.Request, email string) error {
	if email == "" {
		return nil
	}
	userID, _, _, err := h.Store.UserByEmail(r.Context(), email)
	if err != nil {
		return nil
	}
	policy, err := h.Store.AuthenticationPolicyForUser(r.Context(), userID)
	if err != nil {
		return err
	}
	if policy.Enforced && policy.EnforceSSO {
		// There is no password on this account to set.
		return nil
	}
	recent, err := h.Store.RecentPasswordLink(r.Context(), userID, ForgotPasswordCooldown)
	if err != nil || recent {
		return err
	}
	token, expires, err := authn.NewPasswordLink(r.Context(), h.Store, userID)
	if err != nil {
		if errors.Is(err, store.ErrPasswordInactive) {
			return nil
		}
		return err
	}
	link := h.IdentityExternalURL + "/password/set?token=" + url.QueryEscape(token)
	body := "Set a new password for your account:\n\n" + link +
		"\n\nThe link works once and expires on " + expires.UTC().Format("2006-01-02 15:04 UTC") +
		".\n\nIf you did not ask for it, nothing has changed and you can ignore this message."
	// The message belongs to the site this server serves, which is the one
	// workspace it has: a person asking for a link is not signed in to name
	// one.
	workspaceID, _, err := h.Store.DefaultWorkspace(r.Context())
	if err != nil {
		return err
	}
	return h.Store.QueueEmail(r.Context(), workspaceID, email, "Set your password", body)
}
