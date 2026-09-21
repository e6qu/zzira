package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

// WebRequestActionType is Jira Automation's send web request action. The
// method is named in the action, as a link action names its link type.
const WebRequestActionType = "jira.issue.outgoing-webhook"

// webRequestMethods are the methods the action sends, as Jira offers them.
var webRequestMethods = map[string]bool{http.MethodGet: true, http.MethodPost: true, http.MethodPut: true, http.MethodDelete: true}

// webRequestClient carries the site's outbound automation requests. Jira waits
// a bounded time for a response and gives up, rather than holding a rule open.
var webRequestClient = &http.Client{Timeout: 20 * time.Second}

// maxWebResponse is how much of a response the rule keeps for {{webResponse}}.
const maxWebResponse = 64 << 10

// webResponse is the answer a web request action received, which later actions
// read as {{webResponse}}.
type webResponse struct {
	Status int
	Body   string
}

// webRequestHostCheck is the guard a web request passes before it is sent.
// Tests replace it to reach a local server, which the guard refuses by design.
var webRequestHostCheck = checkWebRequestHost

// checkWebRequestHost refuses an address that would reach the site's own
// network. Jira sends these from its cloud, where a private address is not
// reachable at all; refusing here keeps a rule from being used to read what
// only the server can see.
func checkWebRequestHost(target *url.URL) error {
	host := target.Hostname()
	if host == "" {
		return errors.New("the web request URL has no host")
	}
	addresses, err := net.LookupIP(host)
	if err != nil || len(addresses) == 0 {
		return fmt.Errorf("the web request host %q could not be resolved", host)
	}
	for _, address := range addresses {
		if address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsUnspecified() {
			return fmt.Errorf("the web request host %q is on a private network", host)
		}
	}
	return nil
}

// webRequestHeaderName is what a header may be called: the token grammar HTTP
// itself allows, so a rule cannot fold a second header or a body into one.
var webRequestHeaderName = regexp.MustCompile(`^[A-Za-z0-9!#$%&'*+.^_` + "`" + `|~-]+$`)

// webRequestExtras are what a rule sends besides the address: a body of its
// own and the headers it needs to be read with.
type webRequestExtras struct {
	Body    string
	Headers map[string]string
}

// sendWebRequest performs a rule's web request and keeps its answer for
// {{webResponse}}. A work item, when the rule has one, is sent as the body in
// Jira's format, which is what Jira sends when the rule writes none of its
// own.
func (r *Runner) sendWebRequest(ctx context.Context, run *claimedRun, issue *models.Issue, method, rawURL string, extras webRequestExtras) (bool, error) {
	method = strings.ToUpper(strings.TrimSpace(method))
	if !webRequestMethods[method] {
		return false, fmt.Errorf("a web request is sent with GET, POST, PUT or DELETE, not %q", method)
	}
	target, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") {
		return false, errors.New("a web request needs an http or https URL")
	}
	if err := webRequestHostCheck(target); err != nil {
		return false, err
	}
	var body io.Reader
	if written := strings.TrimSpace(extras.Body); written != "" {
		// A rule that writes its own body sends it whatever the method, as
		// Jira does: the shape of the call belongs to whoever is receiving it.
		body = strings.NewReader(written)
	} else if method == http.MethodPost || method == http.MethodPut {
		payload := []byte(`{}`)
		if issue != nil {
			encoded, err := json.Marshal(map[string]any{
				"issue": map[string]any{"id": issue.ID, "key": issue.Key, "fields": map[string]any{
					"summary": issue.Summary, "status": map[string]any{"name": issue.Status.Name},
					"issuetype": map[string]any{"name": issue.IssueType.Name}, "labels": issue.Labels,
				}},
			})
			if err != nil {
				return false, err
			}
			payload = encoded
		}
		body = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return false, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "zzira-automation")
	for name, value := range extras.Headers {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !webRequestHeaderName.MatchString(name) {
			return false, fmt.Errorf("%q is not a header name", name)
		}
		if strings.EqualFold(name, "Host") || strings.EqualFold(name, "Content-Length") {
			// The connection's own headers are the site's to set.
			return false, fmt.Errorf("a rule does not set %s", name)
		}
		request.Header.Set(name, strings.TrimSpace(value))
	}
	response, err := webRequestClient.Do(request)
	if err != nil {
		return false, fmt.Errorf("the web request did not complete: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	answer, err := io.ReadAll(io.LimitReader(response.Body, maxWebResponse))
	if err != nil {
		return false, fmt.Errorf("the web request answer could not be read: %w", err)
	}
	run.WebResponse = &webResponse{Status: response.StatusCode, Body: strings.TrimSpace(string(answer))}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return false, fmt.Errorf("the web request answered %s", strconv.Itoa(response.StatusCode))
	}
	return true, nil
}
