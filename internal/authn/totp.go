package authn

import (
	"crypto/hmac"
	"crypto/rand"
	// #nosec G505 -- RFC 6238 defines TOTP over HMAC-SHA1, and every
	// authenticator app implements that and nothing else. The hash is used
	// as a message authentication code over a counter, not for collision
	// resistance.
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Two-step verification is the RFC 6238 time-based code every authenticator
// app produces: a shared secret, a thirty-second step, and six digits.

const (
	// totpStepSeconds is the interval each code covers, and totpStep the
	// same as a duration.
	totpStepSeconds = 30
	totpStep        = totpStepSeconds * time.Second
	// totpDigits is the length of a code.
	totpDigits = 6
	// totpSkew is how many steps either side of now are accepted, which
	// covers a phone whose clock is a little out and a code typed as it
	// turns over.
	totpSkew = 1
)

var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret mints a secret for one person to hold in their authenticator
// app, as the base32 an app expects.
func NewTOTPSecret() (string, error) {
	secret := make([]byte, 20)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	return totpEncoding.EncodeToString(secret), nil
}

// TOTPURI is what an authenticator app reads: the secret, whose account it is
// for, and which site issued it.
func TOTPURI(secret, issuer, account string) string {
	label := url.PathEscape(issuer + ":" + account)
	query := url.Values{
		"secret": {secret}, "issuer": {issuer},
		"algorithm": {"SHA1"}, "digits": {fmt.Sprint(totpDigits)}, "period": {fmt.Sprint(totpStepSeconds)},
	}
	return "otpauth://totp/" + label + "?" + query.Encode()
}

// TOTPCode is the code for one secret at one moment, which is what an
// authenticator app shows.
func TOTPCode(secret string, at time.Time) (string, error) {
	key, err := totpEncoding.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", err
	}
	seconds := at.Unix()
	if seconds < 0 {
		// The counter runs forward from 1970; nothing before it is a step.
		return "", fmt.Errorf("a code at %s is before the counter starts", at.UTC().Format(time.RFC3339))
	}
	counter := make([]byte, 8)
	binary.BigEndian.PutUint64(counter, uint64(seconds)/totpStepSeconds)
	mac := hmac.New(sha1.New, key)
	mac.Write(counter)
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	divisor := uint32(1)
	for i := 0; i < totpDigits; i++ {
		divisor *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, value%divisor), nil
}

// TOTPMatches says whether a typed code is one this secret produces around
// now. It compares in constant time, so a code is not told apart by how long
// the answer took.
func TOTPMatches(secret, typed string, now time.Time) bool {
	typed = strings.TrimSpace(typed)
	if len(typed) != totpDigits {
		return false
	}
	for step := -totpSkew; step <= totpSkew; step++ {
		code, err := TOTPCode(secret, now.Add(time.Duration(step)*totpStep))
		if err != nil {
			return false
		}
		if hmac.Equal([]byte(code), []byte(typed)) {
			return true
		}
	}
	return false
}
