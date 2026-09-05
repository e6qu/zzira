package web

import (
	"net/http"
	"net/url"
	"strings"
)

// redirectLocal only accepts absolute paths within this application. Validate
// the decoded path too so encoded authority separators cannot bypass the check.
func redirectLocal(w http.ResponseWriter, r *http.Request, target string) {
	u, err := url.Parse(target)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || u.Opaque != "" ||
		!strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") ||
		strings.ContainsAny(u.Path, "\\\r\n") || strings.ContainsAny(target, "\\\r\n") {
		http.Error(w, "Invalid redirect destination.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, u.String(), http.StatusSeeOther) // #nosec G710 -- parsed destination must be a local absolute path without an authority, scheme, backslash or newline.
}
