package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// An authentication policy says how the people it covers sign in. A person
// belongs to one policy; anyone in none gets the organization's default, and
// a site with no default policy enforces nothing -- which is every site until
// an administrator writes one.

// AuthenticationPolicy is the settings that apply to one person.
type AuthenticationPolicy struct {
	PolicyID string
	Name     string
	AuthenticationConfig
	// Enforced reports that a policy applies at all: a disabled policy, or
	// none, leaves sign-in as it was.
	Enforced bool
}

// SessionDuration is how long this policy's sessions last, or the fallback
// when no policy applies.
func (policy AuthenticationPolicy) SessionDuration(fallback time.Duration) time.Duration {
	if !policy.Enforced || policy.SessionDurationMinutes <= 0 {
		return fallback
	}
	return time.Duration(policy.SessionDurationMinutes) * time.Minute
}

// AuthenticationPolicyForUser is the policy that covers a person: the one
// they are named on, else the one covering a group they are in, else the
// organization's default. Being named on a policy wins over being in a group
// under one, because naming somebody is the more particular statement -- and
// a person in two covered groups gets the policy whose group they joined
// first, so the answer does not change between two reads.
func (s *Store) AuthenticationPolicyForUser(ctx context.Context, userID string) (AuthenticationPolicy, error) {
	policy, err := s.authenticationPolicyRow(ctx, `
		SELECT p.id::text,p.name,p.status,p.rule FROM authentication_policy_members m
		JOIN organization_policies p ON p.id=m.policy_id AND p.policy_type='authentication-policy'
		WHERE m.user_id=$1`, userID)
	if err == nil || !errors.Is(err, pgx.ErrNoRows) {
		return policy, err
	}
	policy, err = s.authenticationPolicyRow(ctx, `
		SELECT p.id::text,p.name,p.status,p.rule FROM authentication_policy_groups apg
		JOIN organization_policies p ON p.id=apg.policy_id AND p.policy_type='authentication-policy'
		JOIN group_members gm ON gm.group_id=apg.group_id
		WHERE gm.user_id=$1
		ORDER BY gm.added_at, apg.group_id LIMIT 1`, userID)
	if err == nil || !errors.Is(err, pgx.ErrNoRows) {
		return policy, err
	}
	policy, err = s.authenticationPolicyRow(ctx, `
		SELECT p.id::text,p.name,p.status,p.rule FROM organization_policies p
		JOIN organizations o ON o.id=p.organization_id
		JOIN sites si ON si.organization_id=o.id
		JOIN memberships m ON m.workspace_id=si.workspace_id AND m.user_id=$1
		WHERE p.policy_type='authentication-policy' AND p.rule->'config'->>'default'='true'
		ORDER BY p.created_at LIMIT 1`, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthenticationPolicy{}, nil
	}
	return policy, err
}

func (s *Store) authenticationPolicyRow(ctx context.Context, query string, args ...any) (AuthenticationPolicy, error) {
	var policy AuthenticationPolicy
	var status string
	var rule []byte
	if err := s.Pool.QueryRow(ctx, query, args...).Scan(&policy.PolicyID, &policy.Name, &status, &rule); err != nil {
		return AuthenticationPolicy{}, err
	}
	var decoded struct {
		Config AuthenticationConfig `json:"config"`
	}
	if err := json.Unmarshal(rule, &decoded); err != nil {
		return AuthenticationPolicy{}, err
	}
	policy.AuthenticationConfig = decoded.Config
	policy.Enforced = status == "enabled"
	return policy, nil
}

// AuthenticationPolicyMembers lists the people one policy covers by name.
func (s *Store) AuthenticationPolicyMembers(ctx context.Context, organizationID, policyID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT m.user_id FROM authentication_policy_members m
		JOIN organization_policies p ON p.id=m.policy_id
		WHERE p.id::text=$1 AND p.organization_id=$2::uuid ORDER BY m.user_id`, policyID, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		members = append(members, id)
	}
	return members, rows.Err()
}

// SetAuthenticationPolicyMember puts a person under one policy or takes them
// out of it. A person belongs to one policy, so joining a second leaves the
// first.
func (s *Store) SetAuthenticationPolicyMember(ctx context.Context, workspaceID, actorID, policyID, userID string, member bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	organizationID, err := policyOrganizationForWorkspace(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	var policyType string
	if err := tx.QueryRow(ctx, `SELECT policy_type FROM organization_policies WHERE id::text=$1 AND organization_id=$2::uuid`, policyID, organizationID).Scan(&policyType); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: that policy does not exist", ErrAdminNotFound)
		}
		return err
	}
	if policyType != "authentication-policy" {
		return fmt.Errorf("%w: only an authentication policy has members", ErrAdminValidation)
	}
	var known bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships WHERE workspace_id=$1 AND user_id=$2)`, workspaceID, userID).Scan(&known); err != nil {
		return err
	}
	if !known {
		return fmt.Errorf("%w: that person is not a member of this site", ErrAdminNotFound)
	}
	action := "policy.member-removed"
	if member {
		action = "policy.member-added"
		if _, err := tx.Exec(ctx, `INSERT INTO authentication_policy_members(policy_id,user_id) VALUES($1::uuid,$2)
			ON CONFLICT(user_id) DO UPDATE SET policy_id=EXCLUDED.policy_id,added_at=now()`, policyID, userID); err != nil {
			return err
		}
	} else {
		command, err := tx.Exec(ctx, `DELETE FROM authentication_policy_members WHERE policy_id=$1::uuid AND user_id=$2`, policyID, userID)
		if err != nil {
			return err
		}
		if command.RowsAffected() == 0 {
			return fmt.Errorf("%w: that person is not covered by this policy", ErrAdminNotFound)
		}
	}
	if err := addPolicyAudit(ctx, tx, organizationID, actorID, action, policyID, map[string]any{"accountId": userID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AuthenticationConfigFromRule reads an authentication policy's settings out
// of the rule a policy carries, so an administrator can change one setting
// without restating the rest.
func AuthenticationConfigFromRule(rule map[string]any) AuthenticationConfig {
	var config AuthenticationConfig
	encoded, err := json.Marshal(rule["config"])
	if err != nil {
		return config
	}
	if err := json.Unmarshal(encoded, &config); err != nil {
		return AuthenticationConfig{}
	}
	return config
}

// PasswordRefused says why a new password does not meet the rules the policy
// applies, or is empty when it does. A site with no policy still asks for the
// shortest password anyone may set, and no site accepts one past where bcrypt
// stops reading.
func (policy AuthenticationPolicy) PasswordRefused(password string) string {
	minimum := MinimumPasswordLength
	if policy.Enforced && policy.PasswordMinimumLength > minimum {
		minimum = policy.PasswordMinimumLength
	}
	if len(password) < minimum {
		return fmt.Sprintf("A password is at least %d characters.", minimum)
	}
	if len(password) > MaximumPasswordLength {
		return fmt.Sprintf("A password is at most %d characters.", MaximumPasswordLength)
	}
	return ""
}

// PasswordMinimum is the shortest password the people this policy covers may
// set, which a page shows before they type one.
func (policy AuthenticationPolicy) PasswordMinimum() int {
	if policy.Enforced && policy.PasswordMinimumLength > MinimumPasswordLength {
		return policy.PasswordMinimumLength
	}
	return MinimumPasswordLength
}

// AuthenticationPolicyGroups lists the groups one policy covers.
func (s *Store) AuthenticationPolicyGroups(ctx context.Context, organizationID, policyID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT g.group_id::text FROM authentication_policy_groups g
		JOIN organization_policies p ON p.id=g.policy_id
		WHERE p.id::text=$1 AND p.organization_id=$2::uuid ORDER BY g.group_id`, policyID, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		groups = append(groups, id)
	}
	return groups, rows.Err()
}

// SetAuthenticationPolicyGroup puts a group under one policy or takes it out.
// A group belongs to one policy, as a person does: putting it under a second
// takes it out of the first.
func (s *Store) SetAuthenticationPolicyGroup(ctx context.Context, workspaceID, actorID, policyID, groupID string, covered bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	organizationID, err := policyOrganizationForWorkspace(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	var policyType string
	if err := tx.QueryRow(ctx, `SELECT policy_type FROM organization_policies WHERE id::text=$1 AND organization_id=$2::uuid`, policyID, organizationID).Scan(&policyType); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: that policy does not exist", ErrAdminNotFound)
		}
		return err
	}
	if policyType != "authentication-policy" {
		return fmt.Errorf("%w: only an authentication policy has members", ErrAdminValidation)
	}
	var known bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM groups g JOIN directories d ON d.id=g.directory_id
		JOIN sites si ON si.organization_id=d.organization_id
		WHERE si.workspace_id=$1 AND g.id::text=$2)`, workspaceID, groupID).Scan(&known); err != nil {
		return err
	}
	if !known {
		return fmt.Errorf("%w: that group is not a group of this site", ErrAdminNotFound)
	}
	action := "policy.group-removed"
	if covered {
		action = "policy.group-added"
		if _, err := tx.Exec(ctx, `INSERT INTO authentication_policy_groups(policy_id,group_id) VALUES($1::uuid,$2::uuid)
			ON CONFLICT(group_id) DO UPDATE SET policy_id=EXCLUDED.policy_id,added_at=now()`, policyID, groupID); err != nil {
			return err
		}
	} else {
		command, err := tx.Exec(ctx, `DELETE FROM authentication_policy_groups WHERE policy_id=$1::uuid AND group_id=$2::uuid`, policyID, groupID)
		if err != nil {
			return err
		}
		if command.RowsAffected() == 0 {
			return fmt.Errorf("%w: that group is not covered by this policy", ErrAdminNotFound)
		}
	}
	if err := addPolicyAudit(ctx, tx, organizationID, actorID, action, policyID, map[string]any{"groupId": groupID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
