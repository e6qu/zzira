package store

import (
	"context"
	"errors"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

// A cross-project release groups the versions that ship together across the
// projects a plan reads. The plan document has carried them since the REST
// resource was written; these read them back with the projects, dates and
// progress that make them worth having.

// PlanReleaseMember is one project's version inside a cross-project release.
type PlanReleaseMember struct {
	Version     *models.Version
	ProjectID   string
	ProjectKey  string
	ProjectName string
	Progress    models.VersionProgress
}

// PlanReleaseView is a cross-project release as a reader sees it.
type PlanReleaseView struct {
	Name    string
	Members []PlanReleaseMember
	// Start is the earliest start date of its members and Release the latest
	// release date: when the release as a whole runs.
	Start   string
	Release string
	// DatesDiffer reports members that do not share a release date, which is
	// what makes a cross-project release worth looking at.
	DatesDiffer bool
	Progress    models.VersionProgress
	// Missing names the member versions that are gone, so a release that
	// points at a deleted version says so rather than quietly shrinking.
	Missing []int64
}

// PlanProjects are the projects a plan reads directly: its project sources,
// and the project each of its board sources belongs to. A filter source is
// not one project, so its work can reach projects this does not name.
func (s *Store) PlanProjects(ctx context.Context, workspaceID string, plan Plan) ([]*models.Project, error) {
	ids := make([]string, 0, len(plan.IssueSources))
	seen := map[string]bool{}
	for _, source := range plan.IssueSources {
		var id string
		switch source.Type {
		case "Project":
			id = strconv.FormatInt(source.Value, 10)
		case "Board":
			err := s.Pool.QueryRow(ctx, `SELECT b.project_id FROM boards b JOIN projects p ON p.id=b.project_id WHERE p.workspace_id=$1 AND b.jira_id=$2`, workspaceID, source.Value).Scan(&id)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
		default:
			continue
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	projects := make([]*models.Project, 0, len(ids))
	for _, id := range ids {
		project, err := s.ProjectByIDOrKey(ctx, workspaceID, id)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	sort.SliceStable(projects, func(i, j int) bool { return projects[i].Key < projects[j].Key })
	return projects, nil
}

// PlanReleaseViews reads a plan's cross-project releases with their member
// versions, each version's project and the progress of the work the reader
// can see.
func (s *Store) PlanReleaseViews(ctx context.Context, workspaceID, userID string, plan Plan) ([]PlanReleaseView, error) {
	views := make([]PlanReleaseView, 0, len(plan.CrossProjectReleases))
	for _, release := range plan.CrossProjectReleases {
		view := PlanReleaseView{Name: release.Name, Members: make([]PlanReleaseMember, 0, len(release.ReleaseIDs)), Missing: []int64{}}
		releaseDates := map[string]bool{}
		for _, id := range release.ReleaseIDs {
			version, err := s.Version(ctx, workspaceID, strconv.FormatInt(id, 10))
			if errors.Is(err, pgx.ErrNoRows) {
				view.Missing = append(view.Missing, id)
				continue
			}
			if err != nil {
				return nil, err
			}
			project, err := s.ProjectByIDOrKey(ctx, workspaceID, version.ProjectID)
			if errors.Is(err, pgx.ErrNoRows) {
				view.Missing = append(view.Missing, id)
				continue
			}
			if err != nil {
				return nil, err
			}
			issues, err := s.VersionIssues(ctx, workspaceID, userID, version.ProjectID, version.ID, "fixVersions")
			if err != nil {
				return nil, err
			}
			member := PlanReleaseMember{
				Version: version, ProjectID: project.ID, ProjectKey: project.Key, ProjectName: project.Name,
				Progress: VersionProgress(issues),
			}
			view.Members = append(view.Members, member)
			view.Progress.ToDo += member.Progress.ToDo
			view.Progress.InProgress += member.Progress.InProgress
			view.Progress.Done += member.Progress.Done
			view.Progress.Unmapped += member.Progress.Unmapped
			if version.StartDate != "" && (view.Start == "" || version.StartDate < view.Start) {
				view.Start = version.StartDate
			}
			if version.ReleaseDate != "" {
				releaseDates[version.ReleaseDate] = true
				if view.Release == "" || version.ReleaseDate > view.Release {
					view.Release = version.ReleaseDate
				}
			}
		}
		view.DatesDiffer = len(releaseDates) > 1
		sort.SliceStable(view.Members, func(i, j int) bool { return view.Members[i].ProjectKey < view.Members[j].ProjectKey })
		views = append(views, view)
	}
	sort.SliceStable(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	return views, nil
}
