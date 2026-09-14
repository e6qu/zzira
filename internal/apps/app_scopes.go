package apps

import (
	"regexp"
	"strings"
)

// Jira, Jira Software, Jira Service Management and Confluence mark each REST
// operation with the Connect scope an app must hold to call it. The gateway
// looks the operation up and requires the matching granted scope; operations
// the product keeps from apps altogether are refused.

type appScopeRow struct {
	Method, Path, Product, Pattern, Scope string
}

type compiledAppScope struct {
	method, product, scope string
	pattern                *regexp.Regexp
}

var appOperationScopes = func() []compiledAppScope {
	compiled := make([]compiledAppScope, 0, len(appScopeTable))
	for _, row := range appScopeTable {
		compiled = append(compiled, compiledAppScope{method: row.Method, product: row.Product, scope: row.Scope, pattern: regexp.MustCompile(row.Pattern)})
	}
	return compiled
}()

// appScopeInaccessible marks an operation no app may call.
const appScopeInaccessible = "!inaccessible"

// appOperationScope is the product and Connect scope of the pinned operation a
// request names.
func appOperationScope(method, path string) (string, string, bool) {
	if method == "HEAD" {
		method = "GET"
	}
	for _, operation := range appOperationScopes {
		if operation.method == method && operation.pattern.MatchString(path) {
			return operation.product, operation.scope, true
		}
	}
	return "", "", false
}

// grantedScopeFor is the scope an installation must hold for a product's
// Connect scope.
func grantedScopeFor(product, connectScope string) string {
	scope := strings.ToUpper(connectScope)
	switch scope {
	case "NONE":
		return ""
	case "INACCESSIBLE":
		return appScopeInaccessible
	case "ACCESS_EMAIL_ADDRESSES":
		return "access:email-addresses"
	}
	if product == "confluence" {
		switch scope {
		case "READ":
			return "read:confluence-content"
		case "WRITE":
			return "write:confluence-content"
		case "DELETE":
			return "delete:confluence-content"
		default:
			return "admin:confluence"
		}
	}
	switch scope {
	case "READ":
		return "read:jira-work"
	case "WRITE":
		return "write:jira-work"
	case "DELETE":
		return "delete:jira-work"
	case "PROJECT_ADMIN":
		return "admin:jira-project"
	case "ACT_AS_USER":
		return "act-as-user:jira"
	default:
		return "admin:jira"
	}
}

// appScopeImplies lists what each scope also grants, as Connect's levels
// nest: administering includes deleting, deleting includes writing, and
// writing includes reading. A site-wide Jira administrator also administers
// projects.
var appScopeImplies = map[string][]string{
	"admin:confluence":          {"delete:confluence-content", "write:confluence-content", "read:confluence-content"},
	"delete:confluence-content": {"write:confluence-content", "read:confluence-content"},
	"write:confluence-content":  {"read:confluence-content"},
	"admin:jira":                {"admin:jira-project", "delete:jira-work", "write:jira-work", "read:jira-work"},
	"admin:jira-project":        {"delete:jira-work", "write:jira-work", "read:jira-work"},
	"delete:jira-work":          {"write:jira-work", "read:jira-work"},
	"write:jira-work":           {"read:jira-work"},
}
