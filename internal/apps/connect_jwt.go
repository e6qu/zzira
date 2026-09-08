package apps

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type connectClaims struct {
	Issuer    string `json:"iss"`
	Subject   string `json:"sub,omitempty"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
	QueryHash string `json:"qsh"`
}

func connectToken(r *http.Request) string {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) == 2 && strings.EqualFold(parts[0], "JWT") {
		return parts[1]
	}
	return ""
}

func connectIssuer(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(token) > 16<<10 {
		return "", errors.New("Connect JWT is malformed")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("Connect JWT claims are malformed")
	}
	var claims connectClaims
	if json.Unmarshal(payload, &claims) != nil || !appKeyPattern.MatchString(claims.Issuer) {
		return "", errors.New("Connect JWT issuer is invalid")
	}
	return claims.Issuer, nil
}

func verifyConnectJWT(token string, secret []byte, request *http.Request, body []byte, now time.Time) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errors.New("Connect JWT is malformed")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return errors.New("Connect JWT header is malformed")
	}
	var header struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}
	if json.Unmarshal(headerBytes, &header) != nil || header.Algorithm != "HS256" {
		return errors.New("Connect JWT must use HS256")
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return errors.New("Connect JWT signature is malformed")
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return errors.New("Connect JWT signature is invalid")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return errors.New("Connect JWT claims are malformed")
	}
	var claims connectClaims
	if json.Unmarshal(payload, &claims) != nil || claims.Issuer == "" || claims.IssuedAt == 0 || claims.ExpiresAt == 0 || claims.QueryHash == "" {
		return errors.New("Connect JWT claims are incomplete")
	}
	const leeway = 3 * time.Minute
	if claims.ExpiresAt <= claims.IssuedAt || now.After(time.Unix(claims.ExpiresAt, 0).Add(leeway)) || time.Unix(claims.IssuedAt, 0).After(now.Add(leeway)) {
		return errors.New("Connect JWT is outside its validity window")
	}
	want, err := connectQueryHash(request, body)
	if err != nil {
		return err
	}
	if !hmac.Equal([]byte(strings.ToLower(claims.QueryHash)), []byte(want)) {
		return errors.New("Connect JWT query hash is invalid")
	}
	return nil
}

func connectQueryHash(request *http.Request, body []byte) (string, error) {
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return "", errors.New("request query is malformed")
	}
	values.Del("jwt")
	if request.Method == http.MethodPost && strings.HasPrefix(strings.ToLower(request.Header.Get("Content-Type")), "application/x-www-form-urlencoded") {
		form, err := url.ParseQuery(string(body))
		if err != nil {
			return "", errors.New("request form is malformed")
		}
		for name, entries := range form {
			values[name] = append(values[name], entries...)
		}
	}
	canonical := strings.ToUpper(request.Method) + "&" + connectCanonicalURI(request.URL.EscapedPath()) + "&" + connectCanonicalQuery(values)
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:]), nil
}

func connectCanonicalURI(path string) string {
	if path == "" {
		return "/"
	}
	for _, prefix := range []string{"/jira", "/wiki"} {
		if path == prefix {
			path = "/"
			break
		}
		if strings.HasPrefix(path, prefix+"/") {
			path = strings.TrimPrefix(path, prefix)
			break
		}
	}
	if len(path) > 1 {
		path = strings.TrimSuffix(path, "/")
	}
	return strings.ReplaceAll(path, "&", "%26")
}

func connectCanonicalQuery(values url.Values) string {
	names := make([]string, 0, len(values))
	encoded := make(map[string][]string, len(values))
	for name, entries := range values {
		encodedName := connectPercentEncode(name)
		names = append(names, encodedName)
		for _, entry := range entries {
			encoded[encodedName] = append(encoded[encodedName], connectPercentEncode(entry))
		}
		sort.Strings(encoded[encodedName])
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+strings.Join(encoded[name], "%2C"))
	}
	return strings.Join(parts, "&")
}

func connectPercentEncode(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

// SignConnectJWT creates an app-to-product token for compatibility tests and
// remote module URLs. The caller supplies the app key as issuer.
func SignConnectJWT(secret []byte, issuer string, request *http.Request, body []byte, issuedAt time.Time, lifetime time.Duration) (string, error) {
	queryHash, err := connectQueryHash(request, body)
	if err != nil {
		return "", err
	}
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	claims, _ := json.Marshal(connectClaims{Issuer: issuer, IssuedAt: issuedAt.Unix(), ExpiresAt: issuedAt.Add(lifetime).Unix(), QueryHash: queryHash})
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(unsigned))
	return fmt.Sprintf("%s.%s", unsigned, base64.RawURLEncoding.EncodeToString(mac.Sum(nil))), nil
}
