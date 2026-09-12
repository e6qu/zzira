package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Moving a site from direct space permission grants to roles is done in three
// steps, and Confluence exposes each. First the site is scanned for the
// distinct sets of permissions people actually hold — a "combination". Then an
// administrator decides, once per combination and per kind of principal, which
// role it should become or whether access goes away. Both decisions are applied
// in the background, because a site can have a great many spaces.

const (
	apiTaskWikiPermissionCombinations = "wiki-permission-combinations"
	apiTaskWikiPermissionAssignRoles  = "wiki-permission-assign-roles"
	apiTaskWikiPermissionRemoveAccess = "wiki-permission-remove-access"
)

// WikiPermissionCombination is one distinct set of permissions, and how widely
// it is held.
type WikiPermissionCombination struct {
	ID             string
	Permissions    []string
	SpaceCount     int
	PrincipalCount int
	PrincipalTypes []string
	GeneratedAt    string
}

// WikiSpaceSelection narrows which spaces a transition step touches.
type WikiSpaceSelection struct {
	SpaceType      string
	SelectedSpaces []string
}

// WikiPrincipalTypeAssignment says what one kind of principal holding a
// combination should become.
type WikiPrincipalTypeAssignment struct {
	PrincipalType string
	RemoveAccess  bool
	RoleID        string
}

// WikiRoleAssignmentRequest is one combination's decision.
type WikiRoleAssignmentRequest struct {
	CombinationID            string
	PrincipalTypeAssignments []WikiPrincipalTypeAssignment
}

type wikiPermissionAssignPayload struct {
	Assignments []WikiRoleAssignmentRequest `json:"assignments"`
	Selection   WikiSpaceSelection          `json:"selection"`
}

type wikiPermissionRemovePayload struct {
	CombinationIDs []string           `json:"combinationIds"`
	Selection      WikiSpaceSelection `json:"selection"`
}

// combinationID names a permission set by its contents, so the same set found
// again keeps the same id and an administrator's decision is not invalidated by
// a rescan.
func combinationID(permissions []string) string {
	sorted := append([]string(nil), permissions...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return "cmb_" + hex.EncodeToString(sum[:8])
}

// validateSpaceSelection reports whether a selection is one this product can
// honor. Confluence's personal spaces do not exist here — a space belongs to
// the workspace, not to a person — so a selection naming them is refused rather
// than quietly matching something else.
func validateSpaceSelection(selection WikiSpaceSelection) error {
	switch selection.SpaceType {
	case "", "ALL":
		return nil
	case "SPECIFIC", "ALL_EXCEPT_SPECIFIC":
		if len(selection.SelectedSpaces) == 0 {
			return fmt.Errorf("%w: %s needs at least one space", ErrWikiPermissionValidation, selection.SpaceType)
		}
		return nil
	case "PERSONAL", "ALL_EXCEPT_PERSONAL":
		return nil
	default:
		return fmt.Errorf("%w: spaceType must be ALL, ALL_EXCEPT_PERSONAL, ALL_EXCEPT_SPECIFIC, PERSONAL or SPECIFIC", ErrWikiPermissionValidation)
	}
}

// spaceSelectionPredicate turns a selection into SQL over the alias `sel`.
// The selected spaces may be named by id or by key, as Confluence allows.
func spaceSelectionPredicate(selection WikiSpaceSelection) (string, []string) {
	switch selection.SpaceType {
	case "SPECIFIC":
		return `(sel.id::text = ANY($2) OR sel.key = ANY($2))`, selection.SelectedSpaces
	case "ALL_EXCEPT_SPECIFIC":
		return `NOT (sel.id::text = ANY($2) OR sel.key = ANY($2))`, selection.SelectedSpaces
	case "PERSONAL":
		// Every branch mentions $2 so the query always takes the same
		// arguments, whether or not the selection names spaces.
		return `sel.space_type='personal' AND ($2::text[] IS NULL OR TRUE)`, nil
	case "ALL_EXCEPT_PERSONAL":
		return `sel.space_type<>'personal' AND ($2::text[] IS NULL OR TRUE)`, nil
	default:
		return `($2::text[] IS NULL OR TRUE)`, nil
	}
}

// EnqueueWikiPermissionCombinations queues the scan that finds the distinct
// permission sets on the site.
func (s *Store) EnqueueWikiPermissionCombinations(ctx context.Context, ws, actor string) (APITask, error) {
	if err := s.requireSiteAdmin(ctx, ws, actor); err != nil {
		return APITask{}, err
	}
	task, err := queuedAPITask(ws, actor, "Find space permission combinations", apiTaskWikiPermissionCombinations, map[string]any{})
	if err != nil {
		return APITask{}, err
	}
	return task, s.enqueueAPITask(ctx, task)
}

func (s *Store) executeWikiPermissionCombinations(ctx context.Context, task APITask) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// One row per space and subject, carrying the set of permissions that
	// subject holds in that space.
	rows, err := tx.Query(ctx, `
		SELECT g.subject_type, g.subject_id, array_agg(DISTINCT g.permission ORDER BY g.permission)
		FROM wiki_space_permission_grants g
		JOIN wiki_spaces s ON s.id=g.space_id
		WHERE s.workspace_id=$1
		GROUP BY g.space_id, g.subject_type, g.subject_id`, task.WorkspaceID)
	if err != nil {
		return err
	}
	type aggregate struct {
		permissions    []string
		principals     map[string]bool
		principalTypes map[string]bool
	}
	found := map[string]*aggregate{}
	for rows.Next() {
		var subjectType, subjectID string
		var permissions []string
		if err = rows.Scan(&subjectType, &subjectID, &permissions); err != nil {
			rows.Close()
			return err
		}
		id := combinationID(permissions)
		entry := found[id]
		if entry == nil {
			entry = &aggregate{permissions: permissions, principals: map[string]bool{}, principalTypes: map[string]bool{}}
			found[id] = entry
		}
		// One person holding the same set in three spaces is one principal,
		// not three; the space count is what says how widely it is held.
		entry.principals[subjectType+"\x00"+subjectID] = true
		entry.principalTypes[strings.ToUpper(subjectType)] = true
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	// A combination's space count is how many spaces it appears in, counted
	// once per space rather than once per subject.
	spaceCounts, err := combinationSpaceCounts(ctx, tx, task.WorkspaceID)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM wiki_space_permission_combinations WHERE workspace_id=$1`, task.WorkspaceID); err != nil {
		return err
	}
	for id, entry := range found {
		types := make([]string, 0, len(entry.principalTypes))
		for principalType := range entry.principalTypes {
			types = append(types, principalType)
		}
		sort.Strings(types)
		if _, err = tx.Exec(ctx, `INSERT INTO wiki_space_permission_combinations
			(id,workspace_id,permissions,space_count,principal_count,principal_types)
			VALUES($1,$2,$3,$4,$5,$6)`,
			id, task.WorkspaceID, entry.permissions, spaceCounts[id], len(entry.principals), types); err != nil {
			return err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	return s.CompleteAPITask(ctx, task, "Found the space permission combinations.",
		map[string]any{"combinations": len(found)})
}

func combinationSpaceCounts(ctx context.Context, tx pgx.Tx, ws string) (map[string]int, error) {
	rows, err := tx.Query(ctx, `
		SELECT g.space_id::text, array_agg(DISTINCT g.permission ORDER BY g.permission)
		FROM wiki_space_permission_grants g
		JOIN wiki_spaces s ON s.id=g.space_id
		WHERE s.workspace_id=$1
		GROUP BY g.space_id, g.subject_type, g.subject_id`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	spaces := map[string]map[string]bool{}
	for rows.Next() {
		var spaceID string
		var permissions []string
		if err = rows.Scan(&spaceID, &permissions); err != nil {
			return nil, err
		}
		id := combinationID(permissions)
		if spaces[id] == nil {
			spaces[id] = map[string]bool{}
		}
		spaces[id][spaceID] = true
	}
	counts := map[string]int{}
	for id, set := range spaces {
		counts[id] = len(set)
	}
	return counts, rows.Err()
}

// WikiPermissionCombinations lists the combinations found by the last scan,
// leaving out any whose permissions already match a space role — those have
// somewhere to go already.
func (s *Store) WikiPermissionCombinations(ctx context.Context, ws, actor string) ([]WikiPermissionCombination, string, error) {
	if err := s.requireSiteAdmin(ctx, ws, actor); err != nil {
		return nil, "", err
	}
	roleSets, err := s.roleermissionSets(ctx, ws)
	if err != nil {
		return nil, "", err
	}
	rows, err := s.Pool.Query(ctx, `SELECT id,permissions,space_count,principal_count,principal_types,
		to_char(generated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM wiki_space_permission_combinations WHERE workspace_id=$1 ORDER BY id`, ws)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []WikiPermissionCombination{}
	generatedAt := ""
	for rows.Next() {
		var combination WikiPermissionCombination
		if err = rows.Scan(&combination.ID, &combination.Permissions, &combination.SpaceCount,
			&combination.PrincipalCount, &combination.PrincipalTypes, &combination.GeneratedAt); err != nil {
			return nil, "", err
		}
		generatedAt = combination.GeneratedAt
		if roleSets[combinationID(combination.Permissions)] {
			continue
		}
		out = append(out, combination)
	}
	return out, generatedAt, rows.Err()
}

// roleermissionSets indexes the space roles by the set of permissions they
// carry, so a combination that already has a role is not offered again.
func (s *Store) roleermissionSets(ctx context.Context, ws string) (map[string]bool, error) {
	sets := map[string]bool{}
	for _, role := range systemWikiSpaceRoles {
		sets[combinationID(role.SpacePermissions)] = true
	}
	rows, err := s.Pool.Query(ctx, `SELECT space_permissions FROM wiki_space_roles WHERE workspace_id=$1`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var permissions []string
		if err = rows.Scan(&permissions); err != nil {
			return nil, err
		}
		sets[combinationID(permissions)] = true
	}
	return sets, rows.Err()
}

// EnqueueWikiPermissionRoleAssignments queues the decision an administrator has
// made for one or more combinations.
func (s *Store) EnqueueWikiPermissionRoleAssignments(ctx context.Context, ws, actor string, assignments []WikiRoleAssignmentRequest, selection WikiSpaceSelection) (APITask, error) {
	if err := s.requireSiteAdmin(ctx, ws, actor); err != nil {
		return APITask{}, err
	}
	if len(assignments) == 0 {
		return APITask{}, fmt.Errorf("%w: at least one assignment is required", ErrWikiPermissionValidation)
	}
	if err := validateSpaceSelection(selection); err != nil {
		return APITask{}, err
	}
	for _, assignment := range assignments {
		if assignment.CombinationID == "" {
			return APITask{}, fmt.Errorf("%w: every assignment needs a permission combination id", ErrWikiPermissionValidation)
		}
		for _, principal := range assignment.PrincipalTypeAssignments {
			if !principal.RemoveAccess && principal.RoleID == "" {
				return APITask{}, fmt.Errorf("%w: a role id is required unless access is being removed", ErrWikiPermissionValidation)
			}
		}
	}
	task, err := queuedAPITask(ws, actor, "Assign space roles", apiTaskWikiPermissionAssignRoles,
		wikiPermissionAssignPayload{Assignments: assignments, Selection: selection})
	if err != nil {
		return APITask{}, err
	}
	return task, s.enqueueAPITask(ctx, task)
}

// EnqueueWikiPermissionAccessRemovals queues the removal of every grant in the
// named combinations.
func (s *Store) EnqueueWikiPermissionAccessRemovals(ctx context.Context, ws, actor string, combinationIDs []string, selection WikiSpaceSelection) (APITask, error) {
	if err := s.requireSiteAdmin(ctx, ws, actor); err != nil {
		return APITask{}, err
	}
	if len(combinationIDs) == 0 {
		return APITask{}, fmt.Errorf("%w: at least one permission combination id is required", ErrWikiPermissionValidation)
	}
	if err := validateSpaceSelection(selection); err != nil {
		return APITask{}, err
	}
	task, err := queuedAPITask(ws, actor, "Remove space access", apiTaskWikiPermissionRemoveAccess,
		wikiPermissionRemovePayload{CombinationIDs: combinationIDs, Selection: selection})
	if err != nil {
		return APITask{}, err
	}
	return task, s.enqueueAPITask(ctx, task)
}

// matchingGrantHolders finds the (space, subject) pairs whose grant set is one
// of the named combinations, within the selected spaces.
func (s *Store) matchingGrantHolders(ctx context.Context, ws string, combinationIDs map[string]bool, selection WikiSpaceSelection) ([]grantHolder, error) {
	predicate, args := spaceSelectionPredicate(selection)
	var selected any
	if len(args) > 0 {
		selected = args
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT g.space_id::text, g.subject_type, g.subject_id,
		       array_agg(DISTINCT g.permission ORDER BY g.permission)
		FROM wiki_space_permission_grants g
		JOIN wiki_spaces sel ON sel.id=g.space_id
		WHERE sel.workspace_id=$1 AND `+predicate+`
		GROUP BY g.space_id, g.subject_type, g.subject_id`, ws, selected)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	holders := []grantHolder{}
	for rows.Next() {
		var holder grantHolder
		if err = rows.Scan(&holder.SpaceID, &holder.SubjectType, &holder.SubjectID, &holder.Permissions); err != nil {
			return nil, err
		}
		if combinationIDs[combinationID(holder.Permissions)] {
			holders = append(holders, holder)
		}
	}
	return holders, rows.Err()
}

type grantHolder struct {
	SpaceID     string
	SubjectType string
	SubjectID   string
	Permissions []string
}

func (s *Store) executeWikiPermissionAssignRoles(ctx context.Context, task APITask) error {
	var payload wikiPermissionAssignPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("decode role assignment: %w", err)
	}
	decisions := map[string]map[string]WikiPrincipalTypeAssignment{}
	wanted := map[string]bool{}
	for _, assignment := range payload.Assignments {
		wanted[assignment.CombinationID] = true
		byType := map[string]WikiPrincipalTypeAssignment{}
		for _, principal := range assignment.PrincipalTypeAssignments {
			byType[strings.ToUpper(principal.PrincipalType)] = principal
		}
		decisions[assignment.CombinationID] = byType
	}
	holders, err := s.matchingGrantHolders(ctx, task.WorkspaceID, wanted, payload.Selection)
	if err != nil {
		return err
	}
	assigned, removed := 0, 0
	for _, holder := range holders {
		decision, ok := decisions[combinationID(holder.Permissions)][strings.ToUpper(holder.SubjectType)]
		if !ok {
			// A principal type the administrator said nothing about keeps what
			// it has; silently changing it would be a decision nobody made.
			continue
		}
		if !decision.RemoveAccess {
			if _, err = s.Pool.Exec(ctx, `INSERT INTO wiki_space_role_assignments(space_id,role_id,principal_type,principal_id)
				VALUES($1::bigint,$2,$3,$4) ON CONFLICT DO NOTHING`,
				holder.SpaceID, decision.RoleID, strings.ToUpper(holder.SubjectType), holder.SubjectID); err != nil {
				return err
			}
			assigned++
		} else {
			removed++
		}
		// Either way the direct grants go: the whole point is to leave the
		// site governed by roles rather than by both at once.
		if _, err = s.Pool.Exec(ctx, `DELETE FROM wiki_space_permission_grants
			WHERE space_id::text=$1 AND subject_type=$2 AND subject_id=$3`,
			holder.SpaceID, holder.SubjectType, holder.SubjectID); err != nil {
			return err
		}
	}
	return s.CompleteAPITask(ctx, task, "Applied the space role assignments.",
		map[string]any{"assigned": assigned, "accessRemoved": removed})
}

func (s *Store) executeWikiPermissionRemoveAccess(ctx context.Context, task APITask) error {
	var payload wikiPermissionRemovePayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("decode access removal: %w", err)
	}
	wanted := map[string]bool{}
	for _, id := range payload.CombinationIDs {
		wanted[id] = true
	}
	holders, err := s.matchingGrantHolders(ctx, task.WorkspaceID, wanted, payload.Selection)
	if err != nil {
		return err
	}
	removed := 0
	for _, holder := range holders {
		if _, err = s.Pool.Exec(ctx, `DELETE FROM wiki_space_permission_grants
			WHERE space_id::text=$1 AND subject_type=$2 AND subject_id=$3`,
			holder.SpaceID, holder.SubjectType, holder.SubjectID); err != nil {
			return err
		}
		removed++
	}
	return s.CompleteAPITask(ctx, task, "Removed the space access.", map[string]any{"accessRemoved": removed})
}

// WikiPermissionTransitionTask reads one transition task back.
func (s *Store) WikiPermissionTransitionTask(ctx context.Context, ws, actor, taskID string) (APITask, error) {
	if err := s.requireSiteAdmin(ctx, ws, actor); err != nil {
		return APITask{}, err
	}
	task, err := s.APITaskByID(ctx, ws, taskID)
	if err != nil {
		return APITask{}, err
	}
	switch task.Kind {
	case apiTaskWikiPermissionCombinations, apiTaskWikiPermissionAssignRoles, apiTaskWikiPermissionRemoveAccess:
		return task, nil
	default:
		return APITask{}, pgx.ErrNoRows
	}
}
