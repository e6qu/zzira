package authn

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/store"
)

// TestTwoStepSignIn covers an account that verifies in two steps: the
// password earns a challenge rather than a session, the code answers it, a
// recovery code stands in for the app once, and guessing is cut off.
func TestTwoStepSignIn(t *testing.T) {
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

	const password = "demo1234"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	userID := store.NewID("usr")
	email := userID + "@example.invalid"
	if _, err := st.CreateUser(ctx, userID, email, hash, "Two Step Person"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM sign_in_challenges WHERE user_id=$1`, userID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM user_recovery_codes WHERE user_id=$1`, userID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM user_two_step WHERE user_id=$1`, userID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	})

	// Until the account verifies in two steps, the password is the whole
	// answer.
	signIn, err := Login(ctx, st, email, password)
	if err != nil || signIn.Session == "" || signIn.Challenge != "" {
		t.Fatalf("sign-in with one step: %#v, %v", signIn, err)
	}

	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	// The secret arrives sealed; this test keeps it as it is, because what
	// seals it belongs to the site, not to this package.
	if err := st.StartTwoStep(ctx, userID, []byte(secret)); err != nil {
		t.Fatal(err)
	}
	open := func(_ context.Context, id string) (string, error) {
		if id != userID {
			t.Fatalf("a code was checked against %s, not the account signing in", id)
		}
		enrolment, err := st.TwoStep(ctx, id)
		if err != nil {
			return "", err
		}
		return string(enrolment.Secret), nil
	}

	// An enrolment that was never confirmed changes nothing about signing in.
	signIn, err = Login(ctx, st, email, password)
	if err != nil || signIn.Session == "" {
		t.Fatalf("sign-in with an unconfirmed enrolment: %#v, %v", signIn, err)
	}
	codes, hashes, err := NewRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ConfirmTwoStep(ctx, userID, hashes); err != nil {
		t.Fatal(err)
	}
	// Starting another enrolment over a confirmed one is refused: it is how a
	// session someone walked away from would take the account.
	if err := st.StartTwoStep(ctx, userID, []byte(secret)); !errors.Is(err, store.ErrTwoStep) {
		t.Fatalf("starting again over a confirmed enrolment: %v, want ErrTwoStep", err)
	}

	// Now the password earns a challenge and nothing else.
	signIn, err = Login(ctx, st, email, password)
	if err != nil || signIn.Session != "" || signIn.Challenge == "" {
		t.Fatalf("sign-in with two steps: %#v, %v", signIn, err)
	}
	code, err := TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	answered, err := CompleteSignIn(ctx, st, signIn.Challenge, code, open)
	if err != nil || answered.Session == "" {
		t.Fatalf("answering with the app's code: %#v, %v", answered, err)
	}
	// The challenge is spent with the answer.
	if _, err := CompleteSignIn(ctx, st, signIn.Challenge, code, open); !errors.Is(err, store.ErrTwoStepCode) {
		t.Fatalf("an answered challenge was answered again: %v", err)
	}

	// A recovery code stands in for the app, once, whatever case it is typed
	// in.
	signIn, err = Login(ctx, st, email, password)
	if err != nil {
		t.Fatal(err)
	}
	if answered, err = CompleteSignIn(ctx, st, signIn.Challenge, strings.ToUpper(codes[0]), open); err != nil || answered.Session == "" {
		t.Fatalf("answering with a recovery code: %#v, %v", answered, err)
	}
	signIn, err = Login(ctx, st, email, password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CompleteSignIn(ctx, st, signIn.Challenge, codes[0], open); !errors.Is(err, store.ErrTwoStepCode) {
		t.Fatalf("a spent recovery code was accepted again: %v", err)
	}
	enrolment, err := st.TwoStep(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if enrolment.RecoveryCodesLeft != RecoveryCodeCount-1 {
		t.Fatalf("recovery codes left: %d, want %d", enrolment.RecoveryCodesLeft, RecoveryCodeCount-1)
	}

	// Six digits are guessable if the guessing is free, so a challenge is
	// thrown away after a few wrong codes.
	signIn, err = Login(ctx, st, email, password)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= store.SignInChallengeAttempts; attempt++ {
		if _, err := CompleteSignIn(ctx, st, signIn.Challenge, "000000", open); !errors.Is(err, store.ErrTwoStepCode) {
			t.Fatalf("wrong code %d: %v", attempt, err)
		}
	}
	if _, err := CompleteSignIn(ctx, st, signIn.Challenge, code, open); !errors.Is(err, store.ErrTwoStepCode) {
		t.Fatalf("the challenge survived %d wrong codes: %v", store.SignInChallengeAttempts, err)
	}

	// Turning it off puts the account back on one step.
	if err := st.DisableTwoStep(ctx, userID); err != nil {
		t.Fatal(err)
	}
	signIn, err = Login(ctx, st, email, password)
	if err != nil || signIn.Session == "" {
		t.Fatalf("sign-in after turning two steps off: %#v, %v", signIn, err)
	}
	if enrolment, err = st.TwoStep(ctx, userID); err != nil || enrolment.Confirmed || len(enrolment.Secret) != 0 {
		t.Fatalf("enrolment after turning it off: %#v, %v", enrolment, err)
	}
}
