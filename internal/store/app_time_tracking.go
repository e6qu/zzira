package store

import "context"

// JiraTimeTrackingProviderKey is Jira's own time tracking provider.
const JiraTimeTrackingProviderKey = "Jira"

// InstalledTimeTrackingProvider is a time tracking provider an active app
// installation declares. Key is the app key and module key joined by two
// underscores.
type InstalledTimeTrackingProvider struct {
	Key          string
	Name         string
	AppKey       string
	AdminPageKey string
}

// AppTimeTrackingProviders lists the time tracking providers the site's active
// apps declare, ordered by key.
func (s *Store) AppTimeTrackingProviders(ctx context.Context, workspaceID string) ([]InstalledTimeTrackingProvider, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT i.app_key||'__'||p.module_key,p.name,i.app_key,p.admin_page_key
		FROM app_time_tracking_providers p JOIN app_installations i ON i.id=p.installation_id
		WHERE i.workspace_id=$1 AND i.status='active'
		ORDER BY 1`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	providers := []InstalledTimeTrackingProvider{}
	for rows.Next() {
		var provider InstalledTimeTrackingProvider
		if err := rows.Scan(&provider.Key, &provider.Name, &provider.AppKey, &provider.AdminPageKey); err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}
	return providers, rows.Err()
}

// TimeTrackingProviders lists Jira's provider and every installed app's.
func (s *Store) TimeTrackingProviders(ctx context.Context, workspaceID string) ([]InstalledTimeTrackingProvider, error) {
	providers, err := s.AppTimeTrackingProviders(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return append([]InstalledTimeTrackingProvider{{Key: JiraTimeTrackingProviderKey, Name: "JIRA provided time tracking"}}, providers...), nil
}
