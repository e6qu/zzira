package apps

import (
	"regexp"
	"strings"
)

// Confluence marks each REST operation with the Connect scope an app must hold
// to call it. The gateway looks the operation up and requires the matching
// granted scope; operations Confluence keeps from apps altogether are refused.

type confluenceScopeRow struct {
	Method, Path, Pattern, Scope string
}

type compiledConfluenceScope struct {
	method  string
	pattern *regexp.Regexp
	scope   string
}

var confluenceOperationScopes = func() []compiledConfluenceScope {
	compiled := make([]compiledConfluenceScope, 0, len(confluenceScopeTable))
	for _, row := range confluenceScopeTable {
		compiled = append(compiled, compiledConfluenceScope{method: row.Method, pattern: regexp.MustCompile(row.Pattern), scope: row.Scope})
	}
	return compiled
}()

// appScopeInaccessible marks an operation no app may call.
const appScopeInaccessible = "!inaccessible"

// confluenceConnectScope is the Connect scope of the operation a request
// names, if it is a pinned Confluence operation.
func confluenceConnectScope(method, path string) (string, bool) {
	if method == "HEAD" {
		method = "GET"
	}
	for _, operation := range confluenceOperationScopes {
		if operation.method == method && operation.pattern.MatchString(path) {
			return operation.scope, true
		}
	}
	return "", false
}

// confluenceGrantedScope is the scope an installation must hold for a
// Connect scope: reading, writing, deleting and administering content, and
// reading people's email addresses.
func confluenceGrantedScope(connectScope string) string {
	switch strings.ToUpper(connectScope) {
	case "NONE":
		return ""
	case "READ":
		return "read:confluence-content"
	case "WRITE":
		return "write:confluence-content"
	case "DELETE":
		return "delete:confluence-content"
	case "SPACE_ADMIN", "ADMIN", "PROJECT_ADMIN":
		return "admin:confluence"
	case "ACCESS_EMAIL_ADDRESSES":
		return "access:email-addresses"
	case "INACCESSIBLE":
		return appScopeInaccessible
	default:
		return "admin:confluence"
	}
}

// confluenceScopeImplies lists what each Confluence scope also grants: an
// administrator can delete, deleting includes writing, and writing includes
// reading, as Connect's scope levels do.
var confluenceScopeImplies = map[string][]string{
	"admin:confluence":          {"delete:confluence-content", "write:confluence-content", "read:confluence-content"},
	"delete:confluence-content": {"write:confluence-content", "read:confluence-content"},
	"write:confluence-content":  {"read:confluence-content"},
}
