package store

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
)

// IPAllowlistBlocks reports whether an organization IP allowlist keeps an
// address from a product: an enabled allowlist policy covers the product on
// the workspace's site and none of the covering policies lists the address.
func (s *Store) IPAllowlistBlocks(ctx context.Context, workspaceID, productKey, address string) (bool, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT p.rule FROM organization_policies p
		JOIN sites si ON si.organization_id=p.organization_id
		JOIN organization_policy_resources pr ON pr.policy_id=p.id AND pr.resource_id='ari:cloud:'||$2||'::site/'||si.id::text
		WHERE si.workspace_id=$1 AND p.policy_type='ip-allowlist' AND p.status='enabled'`, workspaceID, productKey)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	ip := net.ParseIP(address)
	covered := false
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return false, err
		}
		covered = true
		var rule struct {
			In []string `json:"in"`
		}
		if err := json.Unmarshal(raw, &rule); err != nil {
			return false, err
		}
		for _, value := range rule.In {
			if allowlisted := net.ParseIP(value); allowlisted != nil && ip != nil && allowlisted.Equal(ip) {
				return false, nil
			}
			if _, network, err := net.ParseCIDR(value); err == nil && ip != nil && network.Contains(ip) {
				return false, nil
			}
		}
	}
	return covered, rows.Err()
}

// allowlistProduct is the product a request path belongs to for IP
// allowlists; administration, sign-in and static assets belong to none.
func allowlistProduct(path string) string {
	switch {
	case strings.HasPrefix(path, "/wiki"):
		return "confluence"
	case path == "/service" || strings.HasPrefix(path, "/service/") || strings.HasPrefix(path, "/rest/servicedeskapi"):
		return "jira-service-management"
	case strings.HasPrefix(path, "/browse/") || strings.HasPrefix(path, "/rest/api/") || strings.HasPrefix(path, "/rest/agile/") ||
		strings.HasPrefix(path, "/projects") || strings.HasPrefix(path, "/issues") || strings.HasPrefix(path, "/boards"):
		return "jira-software"
	}
	return ""
}

// IPAllowlistHandler refuses product requests from addresses an enabled
// organization IP allowlist does not list. Administration and sign-in stay
// reachable, so administrators can correct a policy.
func (s *Store) IPAllowlistHandler(workspaceSlug string, next http.Handler) http.Handler {
	var workspaces sync.Map
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if product := allowlistProduct(r.URL.Path); product != "" {
			workspaceID, cached := workspaces.Load(workspaceSlug)
			if !cached {
				if id, err := s.WorkspaceBySlug(r.Context(), workspaceSlug); err == nil {
					workspaceID = id
					workspaces.Store(workspaceSlug, id)
				}
			}
			if id, ok := workspaceID.(string); ok {
				address := r.RemoteAddr
				if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
					address = host
				}
				if blocked, err := s.IPAllowlistBlocks(r.Context(), id, product, address); err == nil && blocked {
					http.Error(w, "Your IP address is not on your organization's IP allowlist.", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
