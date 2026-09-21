package authn

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/store"
)

// TestChangePassword covers a person replacing their own password: they prove
// they know the current one, the new one meets the site's rule, and the
// session they changed it from is the only one that survives.
func TestChangePassword(t *testing.T) {
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

	const password, replacement = "demo1234", "a-longer-secret"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	userID := store.NewID("usr")
	email := userID + "@example.invalid"
	if _, err := st.CreateUser(ctx, userID, email, hash, "Password Person"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	})
	signedIn := func(token string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "https://zzira.example/", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		return r
	}

	first, err := Login(ctx, st, email, password)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Login(ctx, st, email, password)
	if err != nil {
		t.Fatal(err)
	}
	here, elsewhere := first.Session, second.Session

	// What the rules refuse leaves the password as it was.
	var refused ErrPasswordRefused
	if err := ChangePassword(ctx, st, userID, "not-the-password", replacement, here); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("change with the wrong current password: %v, want ErrUnauthorized", err)
	}
	if err := ChangePassword(ctx, st, userID, password, "short", here); !errors.As(err, &refused) {
		t.Fatalf("change to a password under the minimum: %v, want ErrPasswordRefused", err)
	}
	if err := ChangePassword(ctx, st, userID, password, password, here); !errors.As(err, &refused) {
		t.Fatalf("change to the password already in use: %v, want ErrPasswordRefused", err)
	}
	// bcrypt reads 72 bytes and no more, so a password it would silently
	// truncate is refused rather than accepted as what was typed.
	long := make([]byte, store.MaximumPasswordLength+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := ChangePassword(ctx, st, userID, password, string(long), here); !errors.As(err, &refused) {
		t.Fatalf("change to a password past where bcrypt reads: %v, want ErrPasswordRefused", err)
	}
	if _, err := Login(ctx, st, email, password); err != nil {
		t.Fatalf("the refused changes took the old password away: %v", err)
	}

	if err := ChangePassword(ctx, st, userID, password, replacement, here); err != nil {
		t.Fatal(err)
	}
	if _, err := Login(ctx, st, email, password); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("the old password still signs in: %v", err)
	}
	if _, err := Login(ctx, st, email, replacement); err != nil {
		t.Fatalf("the new password does not sign in: %v", err)
	}

	// The session it was changed from carries on; the one left open on
	// another device does not.
	if got, err := Identify(ctx, st, signedIn(here)); err != nil || got != userID {
		t.Fatalf("the session the password was changed from: %q, %v", got, err)
	}
	if got, err := Identify(ctx, st, signedIn(elsewhere)); err == nil {
		t.Fatalf("a session opened with the old password resolved %q, want ended", got)
	}
}

// TestPasswordLink covers the sign-in link an account with no password anyone
// knows is reached by: it works once, it expires, and spending it ends every
// session that account had.
func TestPasswordLink(t *testing.T) {
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

	// An invitation provisions an account whose password is a value nobody
	// typed and nobody can type.
	unusable, err := UnusablePasswordHash()
	if err != nil {
		t.Fatal(err)
	}
	userID := store.NewID("usr")
	email := userID + "@example.invalid"
	if _, err := st.CreateUser(ctx, userID, email, unusable, "Invited Person"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM password_setup_links WHERE user_id=$1`, userID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	})

	token, expires, err := NewPasswordLink(ctx, st, userID)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(expires) < 23*time.Hour {
		t.Fatalf("a sign-in link expires at %s, which is too soon to reach anyone", expires)
	}
	// Issuing another link retires the first, so a link sent twice cannot be
	// spent twice.
	replaced, _, err := NewPasswordLink(ctx, st, userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := PasswordLinkPolicy(ctx, st, token); !errors.Is(err, store.ErrPasswordLinkInvalid) {
		t.Fatalf("the replaced link still opens the page: %v", err)
	}
	if err := SetPasswordWithLink(ctx, st, "not-a-link", "a-good-password"); !errors.Is(err, store.ErrPasswordLinkInvalid) {
		t.Fatalf("an invented link: %v, want ErrPasswordLinkInvalid", err)
	}
	var refused ErrPasswordRefused
	if err := SetPasswordWithLink(ctx, st, replaced, "short"); !errors.As(err, &refused) {
		t.Fatalf("a password under the minimum: %v, want ErrPasswordRefused", err)
	}

	if err := SetPasswordWithLink(ctx, st, replaced, "a-good-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := Login(ctx, st, email, "a-good-password"); err != nil {
		t.Fatalf("the password the link set does not sign in: %v", err)
	}
	// The link is spent.
	if err := SetPasswordWithLink(ctx, st, replaced, "another-password"); !errors.Is(err, store.ErrPasswordLinkInvalid) {
		t.Fatalf("a spent link set another password: %v", err)
	}

	// An expired link is no link at all.
	stale, _, err := NewPasswordLink(ctx, st, userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE password_setup_links SET expires_at=now()-interval '1 minute' WHERE used_at IS NULL AND user_id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	if err := SetPasswordWithLink(ctx, st, stale, "yet-another-password"); !errors.Is(err, store.ErrPasswordLinkInvalid) {
		t.Fatalf("an expired link set a password: %v", err)
	}
}
