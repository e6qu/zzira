package authz

import (
	"context"
	"errors"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

// IssueWithVisibleParent keeps a relationship from revealing an inaccessible
// issue. It copies the view instead of changing the issue used by commands.
func IssueWithVisibleParent(ctx context.Context, st *store.Store, reader string, issue *models.Issue) (*models.Issue, error) {
	shaped, err := IssuesWithVisibleParents(ctx, st, reader, []*models.Issue{issue})
	if err != nil {
		return nil, err
	}
	return shaped[0], nil
}

// IssuesWithVisibleParents checks each distinct parent once per list response.
func IssuesWithVisibleParents(ctx context.Context, st *store.Store, reader string, issues []*models.Issue) ([]*models.Issue, error) {
	type reference struct{ workspaceID, parentID string }
	readable := map[reference]bool{}
	shaped := make([]*models.Issue, 0, len(issues))
	for _, issue := range issues {
		if issue.Parent == nil {
			shaped = append(shaped, issue)
			continue
		}
		ref := reference{issue.WorkspaceID, issue.Parent.ID}
		visible, known := readable[ref]
		if !known {
			parent, err := st.IssueByIDOrKey(ctx, issue.WorkspaceID, issue.Parent.ID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
			if err == nil {
				visible, err = CanSeeIssue(ctx, st, issue.WorkspaceID, parent.ProjectID, reader, parent.ID, parent.SecurityLevelID)
				if err != nil {
					return nil, err
				}
			}
			readable[ref] = visible
		}
		if visible {
			shaped = append(shaped, issue)
		} else {
			copy := *issue
			copy.Parent = nil
			shaped = append(shaped, &copy)
		}
	}
	return shaped, nil
}
