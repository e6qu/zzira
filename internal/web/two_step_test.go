package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/secretbox"
	"github.com/e6qu/zzira/internal/store"
)

type verificationFixture struct {
	st                *store.Store
	h                 *Handler
	userID, challenge string
}

func newVerificationFixture(t *testing.T, app, confirmed, key bool) verificationFixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	id, challenge := store.NewID("usr"), store.NewID("challenge")
	if _, err := st.Pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Verification person')`, id, id+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id); err != nil {
			t.Error(err)
		}
	})
	box, err := secretbox.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, ProviderSecrets: box, WorkspaceSlug: "verification-test"}
	if app {
		sealed, err := box.Seal([]byte("JBSWY3DPEHPK3PXP"), twoStepSecretContext(id))
		if err != nil {
			t.Fatal(err)
		}
		if err := st.StartTwoStep(ctx, id, sealed); err != nil {
			t.Fatal(err)
		}
		if confirmed {
			if err := st.ConfirmTwoStep(ctx, id, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	if key {
		if err := st.SaveWebAuthnCredential(ctx, id, store.WebAuthnCredential{CredentialID: []byte(id), PublicKey: []byte("fixture"), Label: "Test key"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.CreateSignInChallenge(ctx, authn.SessionHash(challenge), id); err != nil {
		t.Fatal(err)
	}
	return verificationFixture{st: st, h: h, userID: id, challenge: challenge}
}

func (f verificationFixture) request(t *testing.T, method, path string, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(url.Values{"code": {"invalid"}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: signInChallengeCookieName(), Value: f.challenge})
	w := httptest.NewRecorder()
	handler(w, r)
	if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("verification response can be cached")
	}
	return w
}

func TestVerificationOffersOnlyEnrolledMethods(t *testing.T) {
	for _, tc := range []struct {
		name                                    string
		app, confirmed, key, wantCodes, wantKey bool
	}{
		{name: "key only", key: true, wantKey: true},
		{name: "pending app with key", app: true, key: true, wantKey: true},
		{name: "confirmed app", app: true, confirmed: true, wantCodes: true},
		{name: "both methods", app: true, confirmed: true, key: true, wantCodes: true, wantKey: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newVerificationFixture(t, tc.app, tc.confirmed, tc.key)
			w := f.request(t, http.MethodGet, "/login/verify", f.h.VerifySignInForm)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d", w.Code)
			}
			body := w.Body.String()
			if strings.Contains(body, `id="two-step-code"`) != tc.wantCodes {
				t.Error("incorrect code form visibility")
			}
			if strings.Contains(body, "data-passkey-signin") != tc.wantKey {
				t.Error("incorrect security-key visibility")
			}
			if !tc.wantCodes && !strings.Contains(body, ">Use your security key</h1>") {
				t.Error("missing key-only heading")
			}
		})
	}
}

func TestKeyOnlyCodeSubmissionKeepsChallengeAvailable(t *testing.T) {
	f := newVerificationFixture(t, false, false, true)
	w := f.request(t, http.MethodPost, "/login/verify", f.h.VerifySignInSubmit)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Use your security key to finish signing in") {
		t.Fatalf("unexpected response: %d", w.Code)
	}
	if userID, err := f.st.SignInChallengeUser(context.Background(), authn.SessionHash(f.challenge)); err != nil || userID != f.userID {
		t.Fatalf("challenge lost: %v", err)
	}
}

func TestVerificationAttemptLimitRestartsSignIn(t *testing.T) {
	for _, enrol := range []bool{false, true} {
		name := "verify"
		if enrol {
			name = "enrol"
		}
		t.Run(name, func(t *testing.T) {
			f := newVerificationFixture(t, true, !enrol, false)
			handler := f.h.VerifySignInSubmit
			if enrol {
				handler = f.h.EnrolTwoStepSubmit
			}
			for attempt := 1; attempt <= store.SignInChallengeAttempts; attempt++ {
				w := f.request(t, http.MethodPost, "/login/"+name, handler)
				if attempt < store.SignInChallengeAttempts {
					if w.Code != http.StatusUnauthorized {
						t.Fatalf("attempt %d: status %d", attempt, w.Code)
					}
				} else {
					if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login?notice=verification-ended" {
						t.Fatalf("final attempt: status %d, location %s", w.Code, w.Header().Get("Location"))
					}
					if len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].MaxAge >= 0 {
						t.Error("expired challenge cookie was not cleared")
					}
				}
			}
		})
	}
}

func TestExpiredVerificationRestartsSignIn(t *testing.T) {
	f := newVerificationFixture(t, true, true, false)
	if _, err := f.st.Pool.Exec(context.Background(), `UPDATE sign_in_challenges SET expires_at=now()-interval '1 minute' WHERE user_id=$1`, f.userID); err != nil {
		t.Fatal(err)
	}
	for _, handler := range []http.HandlerFunc{f.h.VerifySignInForm, f.h.VerifySignInSubmit, f.h.EnrolTwoStepForm, f.h.EnrolTwoStepSubmit} {
		w := f.request(t, http.MethodPost, "/login/verify", handler)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login?notice=verification-ended" {
			t.Fatalf("unexpected expiry response: %d", w.Code)
		}
	}
}
