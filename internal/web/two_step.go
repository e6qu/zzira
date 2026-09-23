package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/qr"
	"github.com/e6qu/zzira/internal/store"
)

// Two-step verification asks for a code from the account's authenticator app
// after its password. The half-finished sign-in is held in a cookie of its
// own, which carries no access: it names a challenge the database keeps, for
// ten minutes and five wrong codes.

const (
	signInChallengeCookie       = "zzira_sign_in"
	signInChallengeSecureCookie = "__Host-zzira_sign_in"
)

func signInChallengeCookieName() string {
	if authn.SecureCookies() {
		return signInChallengeSecureCookie
	}
	return signInChallengeCookie
}

type twoStepVerifyData struct {
	Error string
	// Keys says the account has a security key, so the page offers it beside
	// the code.
	Keys bool
}

// twoStepSecret opens the sealed secret an account's authenticator app holds.
func (h *Handler) twoStepSecret(ctx context.Context, userID string) (string, error) {
	enrolment, err := h.Store.TwoStep(ctx, userID)
	if err != nil {
		return "", err
	}
	if !enrolment.Confirmed {
		return "", store.ErrTwoStepCode
	}
	plain, err := h.ProviderSecrets.Open(enrolment.Secret, twoStepSecretContext(userID))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// twoStepSecretContext binds a sealed secret to the account it belongs to, so
// one account's secret cannot be moved onto another.
func twoStepSecretContext(userID string) string { return "two-step/" + userID }

// startSignInChallenge holds a sign-in that has passed its password and sends
// the browser to the step that finishes it.
func (h *Handler) startSignInChallenge(w http.ResponseWriter, r *http.Request, challenge string) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure is deployment-configured consistently with the session cookie.
		Name:     signInChallengeCookieName(),
		Value:    challenge,
		Path:     "/",
		HttpOnly: true,
		Secure:   authn.SecureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(store.SignInChallengeTTL.Seconds()),
	})
	http.Redirect(w, r, "/login/verify", http.StatusSeeOther)
}

func clearSignInChallenge(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure is deployment-configured consistently with the session cookie.
		Name: signInChallengeCookieName(), Value: "", Path: "/", HttpOnly: true,
		Secure: authn.SecureCookies(), SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

// VerifySignInForm asks for the code a waiting sign-in owes.
func (h *Handler) VerifySignInForm(w http.ResponseWriter, r *http.Request) {
	noStoreAuthResponse(w)
	challenge, ok := h.waitingSignIn(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !h.waitingSignInVerifies(r, challenge) {
		// The sign-in is held by a policy that requires a second step this
		// account has not set up: the enrolment is the step.
		http.Redirect(w, r, "/login/enrol", http.StatusSeeOther)
		return
	}
	writePage(w, "page_two_step_verify", twoStepVerifyData{Keys: h.waitingSignInKeys(r, challenge)})
}

// waitingSignInVerifies says whether the account this sign-in belongs to has
// something to answer with: an authenticator app, or a security key.
func (h *Handler) waitingSignInVerifies(r *http.Request, challenge string) bool {
	userID, err := h.Store.SignInChallengeUser(r.Context(), authn.SessionHash(challenge))
	if err != nil {
		return false
	}
	if enrolment, err := h.Store.TwoStep(r.Context(), userID); err == nil && enrolment.Confirmed {
		return true
	}
	keys, err := h.Store.WebAuthnCredentials(r.Context(), userID)
	return err == nil && len(keys) > 0
}

// waitingSignInKeys says whether the account this sign-in belongs to has a
// security key, which is what the page offers beside the code.
func (h *Handler) waitingSignInKeys(r *http.Request, challenge string) bool {
	userID, err := h.Store.SignInChallengeUser(r.Context(), authn.SessionHash(challenge))
	if err != nil {
		return false
	}
	keys, err := h.Store.WebAuthnCredentials(r.Context(), userID)
	return err == nil && len(keys) > 0
}

// VerifySignInSubmit answers a waiting sign-in with a code.
func (h *Handler) VerifySignInSubmit(w http.ResponseWriter, r *http.Request) {
	noStoreAuthResponse(w)
	if !parseForm(w, r) {
		return
	}
	challenge, ok := h.waitingSignIn(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !h.waitingSignInVerifies(r, challenge) {
		http.Redirect(w, r, "/login/enrol", http.StatusSeeOther)
		return
	}
	signIn, err := authn.CompleteSignIn(r.Context(), h.Store, challenge, r.PostFormValue("code"), h.twoStepSecret)
	if errors.Is(err, store.ErrTwoStepCode) {
		writePageStatus(w, "page_two_step_verify", twoStepVerifyData{
			Error: "That code is not right. Use the code your authenticator app shows now, or one of your recovery codes.",
			Keys:  h.waitingSignInKeys(r, challenge),
		}, http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	clearSignInChallenge(w)
	authn.SetSessionCookieFor(w, signIn.Session, signIn.TTL)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// waitingSignIn is the challenge this browser is part way through, if it
// still stands.
func (h *Handler) waitingSignIn(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(signInChallengeCookieName())
	if err != nil || cookie.Value == "" {
		return "", false
	}
	if _, err := h.Store.SignInChallengeUser(r.Context(), authn.SessionHash(cookie.Value)); err != nil {
		return "", false
	}
	return cookie.Value, true
}

// UpdateTwoStep is how a person turns two-step verification on and off for
// their own account: starting an enrolment shows the secret once, confirming
// it proves the authenticator app works and hands over the recovery codes,
// and turning it off asks for the password again.
func (h *Handler) UpdateTwoStep(w http.ResponseWriter, r *http.Request) {
	user, _, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	if h.ProviderSecrets == nil {
		http.Error(w, "this site cannot keep a two-step secret: set ZZIRA_IDENTITY_ENCRYPTION_KEY", http.StatusServiceUnavailable)
		return
	}
	switch r.PostFormValue("action") {
	case "start":
		h.startTwoStep(w, r, user)
	case "confirm":
		h.confirmTwoStep(w, r, user)
	case "disable":
		h.disableTwoStep(w, r, user)
	default:
		http.Error(w, "action must be start, confirm, or disable", http.StatusBadRequest)
	}
}

func (h *Handler) startTwoStep(w http.ResponseWriter, r *http.Request, user *models.User) {
	secret, err := authn.NewTOTPSecret()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	sealed, err := h.ProviderSecrets.Seal([]byte(secret), twoStepSecretContext(user.ID))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := h.Store.StartTwoStep(r.Context(), user.ID, sealed); err != nil {
		if errors.Is(err, store.ErrTwoStep) {
			h.profilePageWith(w, r, user, func(data *profilePageData) {
				data.TwoStepError = "This account already verifies in two steps. Turn it off before setting it up again."
			}, http.StatusBadRequest)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	issuer := h.WorkspaceSlug
	if issuer == "" {
		issuer = "ZZIRA"
	}
	uri, path, extent, err := twoStepSetup(secret, issuer, user.Email)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.profilePageWith(w, r, user, func(data *profilePageData) {
		data.TwoStepPending = true
		data.TwoStepSecret = secret
		data.TwoStepURI = uri
		data.TwoStepQR, data.TwoStepQRExtent = path, extent
	}, http.StatusOK)
}

// twoStepSetup is the setup link an authenticator app reads, as text to type
// and as a QR symbol to scan.
func twoStepSetup(secret, issuer, account string) (uri, path string, extent int, err error) {
	uri = authn.TOTPURI(secret, issuer, account)
	symbol, err := qr.Encode(uri)
	if err != nil {
		return "", "", 0, err
	}
	return uri, symbol.SVGPath(), symbol.Extent(), nil
}

func (h *Handler) confirmTwoStep(w http.ResponseWriter, r *http.Request, user *models.User) {
	enrolment, err := h.Store.TwoStep(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(enrolment.Secret) == 0 || enrolment.Confirmed {
		h.profilePageWith(w, r, user, func(data *profilePageData) {
			data.TwoStepError = "There is no enrolment waiting to be confirmed. Start one."
		}, http.StatusBadRequest)
		return
	}
	plain, err := h.ProviderSecrets.Open(enrolment.Secret, twoStepSecretContext(user.ID))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !authn.TOTPMatches(string(plain), r.PostFormValue("code"), time.Now()) {
		h.profilePageWith(w, r, user, func(data *profilePageData) {
			data.TwoStepError = "That code is not right. Use the one your authenticator app shows now."
		}, http.StatusBadRequest)
		return
	}
	codes, hashes, err := authn.NewRecoveryCodes()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := h.Store.ConfirmTwoStep(r.Context(), user.ID, hashes); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.profilePageWith(w, r, user, func(data *profilePageData) {
		data.TwoStepConfirmed, data.TwoStepPending = true, false
		data.TwoStepCodesLeft = len(codes)
		data.TwoStepRecoveryCodes = codes
	}, http.StatusOK)
}

func (h *Handler) disableTwoStep(w http.ResponseWriter, r *http.Request, user *models.User) {
	// The password is asked for again, so a session someone walked away from
	// cannot quietly take the second step off the account.
	hash, err := h.Store.UserPasswordHash(r.Context(), user.ID)
	if err != nil || !authn.CheckPassword(hash, r.PostFormValue("currentPassword")) {
		h.profilePageWith(w, r, user, func(data *profilePageData) {
			data.TwoStepError = "That is not your current password."
		}, http.StatusBadRequest)
		return
	}
	if err := h.Store.DisableTwoStep(r.Context(), user.ID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/people/"+url.PathEscape(user.ID)+"?saved="+url.QueryEscape("Two-step verification turned off"), http.StatusSeeOther)
}

// EnrolTwoStepForm is where a sign-in lands when the account's authentication
// policy requires a second step it does not have yet: the password was
// accepted, and the way on is to set up an authenticator app.
func (h *Handler) EnrolTwoStepForm(w http.ResponseWriter, r *http.Request) {
	noStoreAuthResponse(w)
	challenge, ok := h.waitingSignIn(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	userID, err := h.Store.SignInChallengeUser(r.Context(), authn.SessionHash(challenge))
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if h.ProviderSecrets == nil {
		http.Error(w, "this site cannot keep a two-step secret: set ZZIRA_IDENTITY_ENCRYPTION_KEY", http.StatusServiceUnavailable)
		return
	}
	person, err := h.Store.UserByID(r.Context(), userID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	secret, err := h.pendingTwoStepSecret(r, userID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if secret == "" {
		if secret, err = authn.NewTOTPSecret(); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		sealed, sealErr := h.ProviderSecrets.Seal([]byte(secret), twoStepSecretContext(userID))
		if sealErr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if err := h.Store.StartTwoStep(r.Context(), userID, sealed); err != nil {
			if errors.Is(err, store.ErrTwoStep) {
				// It was confirmed in the meantime, somewhere else: a code is
				// what this sign-in owes now.
				http.Redirect(w, r, "/login/verify", http.StatusSeeOther)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	issuer := h.WorkspaceSlug
	if issuer == "" {
		issuer = "ZZIRA"
	}
	uri, path, extent, err := twoStepSetup(secret, issuer, person.Email)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writePage(w, "page_two_step_enrol", twoStepEnrolData{Secret: secret, URI: uri, QR: path, QRExtent: extent})
}

// pendingTwoStepSecret is the key an enrolment already offered this person,
// so reloading the page does not hand them a second one after they have added
// the first to their app.
func (h *Handler) pendingTwoStepSecret(r *http.Request, userID string) (string, error) {
	enrolment, err := h.Store.TwoStep(r.Context(), userID)
	if err != nil || len(enrolment.Secret) == 0 || enrolment.Confirmed {
		return "", err
	}
	plain, err := h.ProviderSecrets.Open(enrolment.Secret, twoStepSecretContext(userID))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

type twoStepEnrolData struct {
	Secret string
	URI    string
	// QR is the setup link as a QR symbol, and QRExtent how wide it is with
	// its quiet zone, both as page_two_step_enrol draws them.
	QR       string
	QRExtent int
	Error    string
	// RecoveryCodes are shown once, on the answer that finishes both the
	// enrolment and the sign-in.
	RecoveryCodes []string
}

// EnrolTwoStepSubmit finishes an enrolment a policy asked for, and with it the
// sign-in that was waiting on it.
func (h *Handler) EnrolTwoStepSubmit(w http.ResponseWriter, r *http.Request) {
	noStoreAuthResponse(w)
	if !parseForm(w, r) {
		return
	}
	challenge, ok := h.waitingSignIn(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	userID, err := h.Store.SignInChallengeUser(r.Context(), authn.SessionHash(challenge))
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	enrolment, err := h.Store.TwoStep(r.Context(), userID)
	if err != nil || len(enrolment.Secret) == 0 {
		http.Redirect(w, r, "/login/enrol", http.StatusSeeOther)
		return
	}
	plain, err := h.ProviderSecrets.Open(enrolment.Secret, twoStepSecretContext(userID))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	issuer := h.WorkspaceSlug
	if issuer == "" {
		issuer = "ZZIRA"
	}
	person, err := h.Store.UserByID(r.Context(), userID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !authn.TOTPMatches(string(plain), r.PostFormValue("code"), time.Now()) {
		if err := h.Store.FailSignInChallenge(r.Context(), authn.SessionHash(challenge)); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		uri, path, extent, err := twoStepSetup(string(plain), issuer, person.Email)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writePageStatus(w, "page_two_step_enrol", twoStepEnrolData{
			Secret: string(plain), URI: uri, QR: path, QRExtent: extent,
			Error: "That code is not right. Use the one your authenticator app shows now.",
		}, http.StatusUnauthorized)
		return
	}
	codes, hashes, err := authn.NewRecoveryCodes()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := h.Store.ConfirmTwoStep(r.Context(), userID, hashes); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	signIn, err := authn.CompleteEnrolledSignIn(r.Context(), h.Store, challenge)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	clearSignInChallenge(w)
	authn.SetSessionCookieFor(w, signIn.Session, signIn.TTL)
	// The codes are shown on the answer that signs them in, because they
	// exist once and a redirect would lose them.
	writePage(w, "page_two_step_enrol", twoStepEnrolData{RecoveryCodes: codes})
}
