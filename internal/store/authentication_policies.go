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

// AuthenticationPolicyForUser is the policy that covers a person: the one they
// are a member of, or the organization's default.
func (s *Store) AuthenticationPolicyForUser(ctx context.Context, userID string) (AuthenticationPolicy, error) {
	policy, err := s.authenticationPolicyRow(ctx, `
		SELECT p.id::text,p.name,p.status,p.rule FROM authentication_policy_members m
		JOIN organization_policies p ON p.id=m.policy_id AND p.policy_type='authentication-policy'
		WHERE m.user_id=$1`, userID)
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
