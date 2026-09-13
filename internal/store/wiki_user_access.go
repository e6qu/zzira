package store

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Confluence lets anyone who can use the site ask which addresses do not yet
// have access, and invite them. Access here is membership of the workspace: an
// active account that belongs to it.

var ErrWikiUserAccessValidation = errors.New("invalid user access request")

// splitWikiEmails separates valid addresses from invalid ones, keeping the
// order they were given and answering a duplicate once.
func splitWikiEmails(emails []string) (valid, invalid []string, err error) {
	if len(emails) < 1 || len(emails) > 100 {
		return nil, nil, fmt.Errorf("%w: between 1 and 100 emails are required", ErrWikiUserAccessValidation)
	}
	seen := map[string]bool{}
	valid, invalid = []string{}, []string{}
	for _, raw := range emails {
		email := strings.TrimSpace(raw)
		key := strings.ToLower(email)
		if seen[key] {
			continue
		}
		seen[key] = true
		address, parseErr := mail.ParseAddress(email)
		if email == "" || parseErr != nil || !strings.EqualFold(address.Address, email) {
			invalid = append(invalid, raw)
			continue
		}
		valid = append(valid, email)
	}
	return valid, invalid, nil
}

// WikiEmailsWithoutAccess reports which valid addresses have no access to the
// site, and which addresses were not valid at all.
func (s *Store) WikiEmailsWithoutAccess(ctx context.Context, ws, actor string, emails []string) ([]string, []string, error) {
	if err := s.requireMember(ctx, ws, actor); err != nil {
		return nil, nil, err
	}
	valid, invalid, err := splitWikiEmails(emails)
	if err != nil {
		return nil, nil, err
	}
	without := []string{}
	for _, email := range valid {
		var has bool
		if err = s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users u JOIN memberships m ON m.user_id=u.id
			WHERE m.workspace_id=$1 AND lower(u.email)=lower($2) AND u.active)`, ws, email).Scan(&has); err != nil {
			return nil, nil, err
		}
		if !has {
			without = append(without, email)
		}
	}
	return without, invalid, nil
}

// InviteWikiUsersByEmail invites every valid address that has no access yet.
// Invalid addresses are ignored and addresses that already have access are left
// alone, as Confluence documents. An account that is already in the directory
// but not in the site is given access rather than invited a second time.
func (s *Store) InviteWikiUsersByEmail(ctx context.Context, ws, actor string, emails []string, passwordHash string) error {
	without, _, err := s.WikiEmailsWithoutAccess(ctx, ws, actor, emails)
	if err != nil {
		return err
	}
	if len(without) == 0 {
		return nil
	}
	directoryID, err := s.inviteDirectory(ctx, ws)
	if err != nil {
		return err
	}
	for _, email := range without {
		name := strings.TrimSpace(strings.SplitN(email, "@", 2)[0])
		_, inviteErr := s.InviteDirectoryUserWithAccess(ctx, ws, actor, directoryID, email, name, passwordHash, InviteOptions{})
		switch {
		case inviteErr == nil:
		case errors.Is(inviteErr, ErrAdminConflict):
			// Already in the directory, or a suspended account. A suspended
			// account stays suspended; an active one is given site access.
			if _, err = s.Pool.Exec(ctx, `INSERT INTO memberships(workspace_id,user_id,role)
				SELECT $1, u.id, 'member' FROM users u WHERE lower(u.email)=lower($2) AND u.active
				ON CONFLICT DO NOTHING`, ws, email); err != nil {
				return err
			}
		case errors.Is(inviteErr, ErrAdminValidation):
			// Confluence ignores an address it cannot invite.
		default:
			return inviteErr
		}
	}
	return nil
}

// inviteDirectory is the directory an invitation lands in: the organization's
// first active directory, the same one the organization invite uses.
func (s *Store) inviteDirectory(ctx context.Context, ws string) (string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT d.id::text FROM sites si JOIN directories d ON d.organization_id=si.organization_id
		WHERE si.workspace_id=$1 AND d.active`, ws)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", pgx.ErrNoRows
	}
	sort.Strings(ids)
	return ids[0], nil
}
