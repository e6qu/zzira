package authn

import (
	"strings"
	"testing"
	"time"
)

// TestTOTPMatchesTheReferenceVector checks this implementation against RFC
// 6238's own SHA-1 vector, so an authenticator app and this server agree
// about what a code is.
func TestTOTPMatchesTheReferenceVector(t *testing.T) {
	// The RFC's seed is the ASCII "12345678901234567890".
	secret := totpEncoding.EncodeToString([]byte("12345678901234567890"))
	for _, tt := range []struct {
		seconds int64
		code    string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
	} {
		got, err := TOTPCode(secret, time.Unix(tt.seconds, 0))
		if err != nil {
			t.Fatal(err)
		}
		if got != tt.code {
			t.Errorf("code at %d = %s, want %s", tt.seconds, got, tt.code)
		}
	}
}

func TestTOTPMatchesAcceptsTheNeighbouringSteps(t *testing.T) {
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, offset := range []time.Duration{-totpStep, 0, totpStep} {
		code, err := TOTPCode(secret, now.Add(offset))
		if err != nil {
			t.Fatal(err)
		}
		if !TOTPMatches(secret, code, now) {
			t.Errorf("a code %s away was refused", offset)
		}
	}
	far, err := TOTPCode(secret, now.Add(5*totpStep))
	if err != nil {
		t.Fatal(err)
	}
	if TOTPMatches(secret, far, now) {
		t.Error("a code from two and a half minutes away was accepted")
	}
	for _, typed := range []string{"", "12345", "1234567", "abcdef"} {
		if TOTPMatches(secret, typed, now) {
			t.Errorf("%q was accepted as a code", typed)
		}
	}
}

// The URI is what the authenticator app reads, so it carries the secret, whose
// account it is, and the parameters this server uses.
func TestTOTPURI(t *testing.T) {
	uri := TOTPURI("ABCDEF", "ZZIRA", "person@example.invalid")
	for _, want := range []string{
		"otpauth://totp/ZZIRA:person@example.invalid?",
		"secret=ABCDEF", "issuer=ZZIRA", "algorithm=SHA1", "digits=6", "period=30",
	} {
		if !strings.Contains(uri, want) {
			t.Errorf("%q is missing from %s", want, uri)
		}
	}
}
