package automation

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"sort"

	"github.com/e6qu/zzira/internal/models"
)

// Assignment methods are how Jira Automation picks who to assign work to when
// the rule does not name one person.
const (
	AssignRoundRobin = "round-robin"
	AssignBalanced   = "balanced"
	AssignRandom     = "random"
)

// assignmentMethods are the methods the editor offers, by the name Jira gives
// each one.
var assignmentMethods = map[string]bool{AssignRoundRobin: true, AssignBalanced: true, AssignRandom: true}

// pickAssignee chooses who a rule assigns work to. Candidates are the people
// the project's assignee picker offers, so a rule cannot assign work to
// someone a person could not.
func (r *Runner) pickAssignee(ctx context.Context, run *claimedRun, issue *models.Issue, method string) (string, error) {
	candidates, err := r.Service.Store.AssignableUsersForProject(ctx, run.WorkspaceID, issue.ProjectID, issue.ID)
	if err != nil {
		return "", err
	}
	if len(candidates) == 0 {
		return "", errors.New("nobody in the project may be assigned work")
	}
	// A stable order keeps a rotation repeatable and a balance tie settled the
	// same way every run.
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })

	switch method {
	case AssignRandom:
		index, err := rand.Int(rand.Reader, big.NewInt(int64(len(candidates))))
		if err != nil {
			return "", err
		}
		return candidates[index.Int64()].ID, nil
	case AssignBalanced:
		counts, err := r.Service.Store.OpenWorkByAssignee(ctx, issue.ProjectID)
		if err != nil {
			return "", err
		}
		chosen := candidates[0]
		for _, candidate := range candidates[1:] {
			if counts[candidate.ID] < counts[chosen.ID] {
				chosen = candidate
			}
		}
		return chosen.ID, nil
	case AssignRoundRobin:
		seen, err := r.Service.Store.LastAssignedAtByAssignee(ctx, issue.ProjectID)
		if err != nil {
			return "", err
		}
		// Whoever has waited longest goes next, and someone never assigned
		// work in the project waits longest of all.
		chosen := candidates[0]
		for _, candidate := range candidates[1:] {
			current, hasCurrent := seen[chosen.ID]
			next, hasNext := seen[candidate.ID]
			switch {
			case !hasNext && hasCurrent:
				chosen = candidate
			case hasNext && hasCurrent && next < current:
				chosen = candidate
			}
		}
		return chosen.ID, nil
	}
	return "", fmt.Errorf("assign action cannot pick by %q", method)
}
