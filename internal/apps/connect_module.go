package apps

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ConnectModuleURL resolves a descriptor module URL beneath baseUrl and adds
// the standard host context plus a short-lived product-to-app JWT.
func ConnectModuleURL(baseURL, moduleURL, hostURL, workspaceID string, secret []byte, channel string, contextValues url.Values, now time.Time) (string, error) {
	for key, values := range contextValues {
		if len(values) > 0 {
			moduleURL = strings.ReplaceAll(moduleURL, "{"+key+"}", url.QueryEscape(values[0]))
		}
	}
	module, err := url.Parse(moduleURL)
	if err != nil || !validAppCallbackPath(moduleURL) {
		return "", fmt.Errorf("invalid remote module URL")
	}
	actual, err := appRelativeURL(baseURL, moduleURL)
	if err != nil {
		return "", err
	}
	query := module.Query()
	for key, values := range contextValues {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	query.Set("xdm_e", strings.TrimRight(hostURL, "/"))
	query.Set("xdm_c", channel)
	query.Set("cp", "")
	query.Set("lic", "active")
	query.Set("cv", "1.0")
	actual.RawQuery = query.Encode()
	canonical := &http.Request{Method: http.MethodGet, URL: &url.URL{Path: module.Path, RawQuery: actual.RawQuery}}
	token, err := SignConnectJWT(secret, workspaceID, canonical, nil, now, 3*time.Minute)
	if err != nil {
		return "", err
	}
	query.Set("jwt", token)
	actual.RawQuery = query.Encode()
	return actual.String(), nil
}

func appRelativeURL(baseURL, relativeURL string) (*url.URL, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" {
		return nil, fmt.Errorf("invalid app base URL")
	}
	relative, err := url.Parse(relativeURL)
	if err != nil || !validAppCallbackPath(relativeURL) {
		return nil, fmt.Errorf("invalid app relative URL")
	}
	actual := *base
	actual.Path = strings.TrimRight(base.Path, "/") + "/" + strings.TrimLeft(relative.Path, "/")
	actual.RawPath = ""
	actual.RawQuery = relative.RawQuery
	actual.Fragment = ""
	return &actual, nil
}
