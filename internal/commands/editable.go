package commands

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/workflow"
)

// ErrIssueNotEditable refuses changing a work item whose status sets
// jira.issue.editable to false, such as a closed status.
var ErrIssueNotEditable = errors.New("the work item is not editable in its current status")

// Overrides are Jira's screen security overrides, which Connect and Forge
// apps with Administer Jira may request.
type Overrides struct {
	// ScreenSecurity lets a write set fields the field configuration hides.
	ScreenSecurity bool
	// EditableFlag lets a write change a work item that is not editable.
	EditableFlag bool
}

type overridesContextKey struct{}

// WithOverrides carries authorized overrides to the commands a request runs.
func WithOverrides(ctx context.Context, overrides Overrides) context.Context {
	return context.WithValue(ctx, overridesContextKey{}, overrides)
}

// OverridesFromContext returns the overrides a request was authorized for.
func OverridesFromContext(ctx context.Context) Overrides {
	overrides, _ := ctx.Value(overridesContextKey{}).(Overrides)
	return overrides
}

// IssueEditable reports whether the work item's status lets it be edited.
func (s *Service) IssueEditable(ctx context.Context, issue *models.Issue) (bool, error) {
	wf, err := s.Store.WorkflowForProjectAndIssueType(ctx, issue.ProjectID, issue.IssueType.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		wf, err = workflow.Default(), nil
	}
	if err != nil {
		return false, err
	}
	return wf.StatusEditable(issue.Status.ID), nil
}

// requireEditable refuses a change to a work item that is not editable unless
// the request overrides the editable flag.
func (s *Service) requireEditable(ctx context.Context, issue *models.Issue) error {
	if OverridesFromContext(ctx).EditableFlag {
		return nil
	}
	editable, err := s.IssueEditable(ctx, issue)
	if err != nil {
		return err
	}
	if !editable {
		return ErrIssueNotEditable
	}
	return nil
}
