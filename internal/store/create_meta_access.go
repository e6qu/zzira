package store

import (
	"context"

	"github.com/e6qu/zzira/internal/models"
)

// IssueCreateMetadataForCreation shares the create permission gate across browser and API.
func (s *Store) IssueCreateMetadataForCreation(ctx context.Context, workspaceID, userID string) (*models.IssueCreateMetadata, error) {
	meta, err := s.IssueCreateMetadata(ctx, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	creatable, err := s.ProjectsWithPermissions(ctx, workspaceID, userID, []string{"CREATE_ISSUES"})
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, project := range creatable {
		allowed[project.ID] = true
	}
	projects := meta.Projects[:0]
	for _, project := range meta.Projects {
		if !allowed[project.Project.ID] {
			continue
		}
		for i := range project.Fields {
			if project.Fields[i].ID != "project" {
				continue
			}
			options := []models.CreateFieldOption{}
			for _, option := range project.Fields[i].Options {
				if allowed[option.ID] {
					options = append(options, option)
				}
			}
			project.Fields[i].Options = options
		}
		projects = append(projects, project)
	}
	meta.Projects = projects
	return meta, nil
}
