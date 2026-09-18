package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrServiceDeskNotFound reports a service desk this site does not have.
var ErrServiceDeskNotFound = errors.New("service desk does not exist")

// ErrServiceRequestTypeGroupNotFound reports a request type group the desk
// does not have.
var ErrServiceRequestTypeGroupNotFound = errors.New("request type group does not exist")

// ErrServiceRequestTypeGroupInUse reports a group that still shows request
// types on the portal.
var ErrServiceRequestTypeGroupInUse = errors.New("request type group still holds request types")

// ErrServiceRequestTypeWorkTypeNotFound reports the work type a request type
// would be raised as, which Jira answers 404 for.
var ErrServiceRequestTypeWorkTypeNotFound = errors.New("work type does not exist")

func serviceRequestTypeWriteError(err error) error {
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) {
		switch {
		case pgerr.Code == "23505":
			return fmt.Errorf("a request type with this name already exists")
		case pgerr.Code == "23503" && strings.Contains(pgerr.ConstraintName, "issue_type"):
			return ErrServiceRequestTypeWorkTypeNotFound
		}
	}
	return err
}

func serviceRequestTypeGroupWriteError(err error) error {
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "23505" {
		return fmt.Errorf("a request type group with this name already exists")
	}
	return err
}

// writeServiceAudit records a service desk change in the site's audit log.
func writeServiceAudit(ctx context.Context, tx pgx.Tx, workspaceID, actorID, action, targetType, targetID string, detail map[string]any) error {
	body, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		SELECT organization_id,$2,$3,$4,$5,$6::jsonb FROM sites WHERE workspace_id=$1`,
		workspaceID, actorID, action, targetType, targetID, body)
	return err
}

// serviceRequestTypeGroupID makes a readable id for a new group, as the seeded
// groups have. Jira's group ids are opaque to clients.
func serviceRequestTypeGroupID(name string) string {
	slug := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			return unicode.ToLower(r)
		default:
			return '-'
		}
	}, name)
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	slug = strings.Trim(slug, "-")
	if len(slug) > 64 {
		slug = strings.Trim(slug[:64], "-")
	}
	if slug == "" {
		slug = "group"
	}
	return slug
}

// serviceDeskExists reports whether the site has the service desk, so a change
// to a desk that is not there fails the same way everywhere.
func serviceDeskExists(ctx context.Context, tx pgx.Tx, workspaceID, serviceDeskID string) error {
	var found bool
	err := tx.QueryRow(ctx, `SELECT TRUE FROM service_desks WHERE workspace_id=$1 AND id=$2`, workspaceID, serviceDeskID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrServiceDeskNotFound
	}
	return err
}

// ServicePortalGroups lists the portal's request type groups in order, each
// with the request types it shows in the order the desk arranged them. A
// request type in no group is not on the portal at all, which is how Jira
// Service Management hides a request type created without a group. A search
// term narrows the request types by name and description.
func (s *Store) ServicePortalGroups(ctx context.Context, workspaceID, serviceDeskID, search string) ([]models.ServicePortalGroup, error) {
	groups, err := s.ServiceRequestTypeGroups(ctx, workspaceID, serviceDeskID)
	if err != nil {
		return nil, err
	}
	search = strings.TrimSpace(search)
	rows, err := s.Pool.Query(ctx, `
		SELECT m.group_id,rt.id,rt.service_desk_id,rt.name,rt.description,rt.help_text,rt.issue_type_id
		FROM service_request_type_group_members m
		JOIN service_request_types rt ON rt.id=m.request_type_id
		JOIN service_desks sd ON sd.id=m.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2
		  AND ($3='' OR rt.name ILIKE '%'||$3||'%' OR rt.description ILIKE '%'||$3||'%')
		ORDER BY m.position,rt.id::bigint`, workspaceID, serviceDeskID, search)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := map[string][]models.ServiceRequestType{}
	for rows.Next() {
		var groupID string
		requestType := models.ServiceRequestType{}
		if err := rows.Scan(&groupID, &requestType.ID, &requestType.ServiceDeskID, &requestType.Name,
			&requestType.Description, &requestType.HelpText, &requestType.IssueTypeID); err != nil {
			return nil, err
		}
		members[groupID] = append(members[groupID], requestType)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	portal := make([]models.ServicePortalGroup, 0, len(groups))
	for _, group := range groups {
		portal = append(portal, models.ServicePortalGroup{Group: group, RequestTypes: members[group.ID]})
	}
	return portal, nil
}

// ServiceUngroupedRequestTypes lists the desk's request types that are in no
// group, which the portal does not show.
func (s *Store) ServiceUngroupedRequestTypes(ctx context.Context, workspaceID, serviceDeskID string) ([]models.ServiceRequestType, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT rt.id,rt.service_desk_id,rt.name,rt.description,rt.help_text,rt.issue_type_id
		FROM service_request_types rt JOIN service_desks sd ON sd.id=rt.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2
		  AND NOT EXISTS (SELECT 1 FROM service_request_type_group_members m WHERE m.request_type_id=rt.id)
		ORDER BY rt.id::bigint`, workspaceID, serviceDeskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]models.ServiceRequestType, 0)
	for rows.Next() {
		requestType := models.ServiceRequestType{GroupIDs: []string{}}
		if err := rows.Scan(&requestType.ID, &requestType.ServiceDeskID, &requestType.Name,
			&requestType.Description, &requestType.HelpText, &requestType.IssueTypeID); err != nil {
			return nil, err
		}
		values = append(values, requestType)
	}
	return values, rows.Err()
}

// CreateServiceRequestTypeGroup adds a group the portal shows after the groups
// already there.
func (s *Store) CreateServiceRequestTypeGroup(ctx context.Context, workspaceID, actorID, serviceDeskID, name string) (*models.ServiceRequestTypeGroup, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := serviceDeskExists(ctx, tx, workspaceID, serviceDeskID); err != nil {
		return nil, err
	}
	base := serviceRequestTypeGroupID(name)
	id := base
	for attempt := 2; ; attempt++ {
		var taken bool
		err := tx.QueryRow(ctx, `SELECT TRUE FROM service_request_type_groups WHERE service_desk_id=$1 AND id=$2`, serviceDeskID, id).Scan(&taken)
		if errors.Is(err, pgx.ErrNoRows) {
			break
		}
		if err != nil {
			return nil, err
		}
		id = fmt.Sprintf("%s-%d", base, attempt)
	}
	group := &models.ServiceRequestTypeGroup{ID: id, ServiceDeskID: serviceDeskID, Name: name}
	err = tx.QueryRow(ctx, `
		INSERT INTO service_request_type_groups(service_desk_id,id,name,position)
		VALUES($1,$2,$3,COALESCE((SELECT max(position)+1 FROM service_request_type_groups WHERE service_desk_id=$1),0))
		RETURNING position`, serviceDeskID, id, name).Scan(&group.Position)
	if err != nil {
		return nil, serviceRequestTypeGroupWriteError(err)
	}
	if err := writeServiceAudit(ctx, tx, workspaceID, actorID, "service.request_type_group.created", "service_request_type_group", id,
		map[string]any{"serviceDeskId": serviceDeskID, "name": name}); err != nil {
		return nil, err
	}
	return group, tx.Commit(ctx)
}

// RenameServiceRequestTypeGroup changes the heading the portal shows above a
// group.
func (s *Store) RenameServiceRequestTypeGroup(ctx context.Context, workspaceID, actorID, serviceDeskID, groupID, name string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `
		UPDATE service_request_type_groups g SET name=$4 FROM service_desks sd
		WHERE sd.id=g.service_desk_id AND sd.workspace_id=$1 AND sd.id=$2 AND g.id=$3`, workspaceID, serviceDeskID, groupID, name)
	if err != nil {
		return serviceRequestTypeGroupWriteError(err)
	}
	if result.RowsAffected() == 0 {
		return ErrServiceRequestTypeGroupNotFound
	}
	if err := writeServiceAudit(ctx, tx, workspaceID, actorID, "service.request_type_group.updated", "service_request_type_group", groupID,
		map[string]any{"serviceDeskId": serviceDeskID, "name": name}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteServiceRequestTypeGroup removes an empty group from the portal. A
// group that still holds request types would take them off the portal with it,
// so it is refused.
func (s *Store) DeleteServiceRequestTypeGroup(ctx context.Context, workspaceID, actorID, serviceDeskID, groupID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var members int
	err = tx.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM service_request_type_group_members m WHERE m.service_desk_id=g.service_desk_id AND m.group_id=g.id)
		FROM service_request_type_groups g JOIN service_desks sd ON sd.id=g.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2 AND g.id=$3 FOR UPDATE OF g`, workspaceID, serviceDeskID, groupID).Scan(&members)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrServiceRequestTypeGroupNotFound
	}
	if err != nil {
		return err
	}
	if members > 0 {
		return ErrServiceRequestTypeGroupInUse
	}
	if _, err := tx.Exec(ctx, `DELETE FROM service_request_type_groups WHERE service_desk_id=$1 AND id=$2`, serviceDeskID, groupID); err != nil {
		return err
	}
	if err := serviceRequestTypeGroupPositions(ctx, tx, serviceDeskID); err != nil {
		return err
	}
	if err := writeServiceAudit(ctx, tx, workspaceID, actorID, "service.request_type_group.deleted", "service_request_type_group", groupID,
		map[string]any{"serviceDeskId": serviceDeskID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// MoveServiceRequestTypeGroup swaps a group with the one before or after it,
// which is how a desk administrator arranges the portal's groups.
func (s *Store) MoveServiceRequestTypeGroup(ctx context.Context, workspaceID, actorID, serviceDeskID, groupID, direction string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := serviceDeskExists(ctx, tx, workspaceID, serviceDeskID); err != nil {
		return err
	}
	if err := serviceRequestTypeGroupPositions(ctx, tx, serviceDeskID); err != nil {
		return err
	}
	var position int
	err = tx.QueryRow(ctx, `SELECT position FROM service_request_type_groups WHERE service_desk_id=$1 AND id=$2 FOR UPDATE`, serviceDeskID, groupID).Scan(&position)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrServiceRequestTypeGroupNotFound
	}
	if err != nil {
		return err
	}
	neighbour := position - 1
	if direction == "down" {
		neighbour = position + 1
	}
	var neighbourID string
	err = tx.QueryRow(ctx, `SELECT id FROM service_request_type_groups WHERE service_desk_id=$1 AND position=$2 FOR UPDATE`, serviceDeskID, neighbour).Scan(&neighbourID)
	if errors.Is(err, pgx.ErrNoRows) {
		// The group is already first or last; the portal order does not change.
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE service_request_type_groups SET position=$3 WHERE service_desk_id=$1 AND id=$2`, serviceDeskID, neighbourID, position); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE service_request_type_groups SET position=$3 WHERE service_desk_id=$1 AND id=$2`, serviceDeskID, groupID, neighbour); err != nil {
		return err
	}
	if err := writeServiceAudit(ctx, tx, workspaceID, actorID, "service.request_type_group.moved", "service_request_type_group", groupID,
		map[string]any{"serviceDeskId": serviceDeskID, "position": neighbour}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// serviceRequestTypeGroupPositions closes the gaps a deletion leaves, so
// moving by one position always finds its neighbour.
func serviceRequestTypeGroupPositions(ctx context.Context, tx pgx.Tx, serviceDeskID string) error {
	_, err := tx.Exec(ctx, `
		UPDATE service_request_type_groups g SET position=ordered.rank
		FROM (SELECT id,row_number() OVER (ORDER BY position,id)-1 AS rank
		      FROM service_request_type_groups WHERE service_desk_id=$1) ordered
		WHERE g.service_desk_id=$1 AND g.id=ordered.id AND g.position<>ordered.rank`, serviceDeskID)
	return err
}

// serviceRequestTypeGroupIDs reads the groups a request type belongs to, in
// the order the portal shows those groups.
func serviceRequestTypeGroupIDs(ctx context.Context, tx pgx.Tx, requestTypeID string) ([]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT m.group_id FROM service_request_type_group_members m
		JOIN service_request_type_groups g ON g.service_desk_id=m.service_desk_id AND g.id=m.group_id
		WHERE m.request_type_id=$1 ORDER BY g.position,g.id`, requestTypeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// setServiceRequestTypeGroups puts a request type in exactly the groups given,
// adding it at the end of each group it joins and leaving the order of the
// groups it already belonged to alone.
func setServiceRequestTypeGroups(ctx context.Context, tx pgx.Tx, serviceDeskID, requestTypeID string, groupIDs []string) error {
	wanted := make([]string, 0, len(groupIDs))
	seen := map[string]bool{}
	for _, id := range groupIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		wanted = append(wanted, id)
	}
	for _, id := range wanted {
		var found bool
		err := tx.QueryRow(ctx, `SELECT TRUE FROM service_request_type_groups WHERE service_desk_id=$1 AND id=$2`, serviceDeskID, id).Scan(&found)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrServiceRequestTypeGroupNotFound, id)
		}
		if err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM service_request_type_group_members
		WHERE request_type_id=$1 AND NOT (group_id=ANY($2))`, requestTypeID, wanted); err != nil {
		return err
	}
	for _, id := range wanted {
		if _, err := tx.Exec(ctx, `
			INSERT INTO service_request_type_group_members(service_desk_id,group_id,request_type_id,position)
			VALUES($1,$2,$3,COALESCE((SELECT max(position)+1 FROM service_request_type_group_members WHERE service_desk_id=$1 AND group_id=$2),0))
			ON CONFLICT DO NOTHING`, serviceDeskID, id, requestTypeID); err != nil {
			return err
		}
	}
	return nil
}

// SetServiceRequestTypeGroups puts a request type in the portal groups given,
// and takes it out of the others. A request type in no group is not shown on
// the portal.
func (s *Store) SetServiceRequestTypeGroups(ctx context.Context, workspaceID, actorID, serviceDeskID, requestTypeID string, groupIDs []string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var found bool
	err = tx.QueryRow(ctx, `
		SELECT TRUE FROM service_request_types rt JOIN service_desks sd ON sd.id=rt.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2 AND rt.id=$3 FOR UPDATE OF rt`, workspaceID, serviceDeskID, requestTypeID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrServiceRequestTypeNotFound
	}
	if err != nil {
		return err
	}
	if err := setServiceRequestTypeGroups(ctx, tx, serviceDeskID, requestTypeID, groupIDs); err != nil {
		return err
	}
	if err := writeServiceAudit(ctx, tx, workspaceID, actorID, "service.request_type.grouped", "service_request_type", requestTypeID,
		map[string]any{"serviceDeskId": serviceDeskID, "groupIds": groupIDs}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// MoveServiceRequestType swaps a request type with the one before or after it
// inside one portal group.
func (s *Store) MoveServiceRequestType(ctx context.Context, workspaceID, actorID, serviceDeskID, groupID, requestTypeID, direction string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := serviceDeskExists(ctx, tx, workspaceID, serviceDeskID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE service_request_type_group_members m SET position=ordered.rank
		FROM (SELECT request_type_id,row_number() OVER (ORDER BY position,request_type_id::bigint)-1 AS rank
		      FROM service_request_type_group_members WHERE service_desk_id=$1 AND group_id=$2) ordered
		WHERE m.service_desk_id=$1 AND m.group_id=$2 AND m.request_type_id=ordered.request_type_id AND m.position<>ordered.rank`,
		serviceDeskID, groupID); err != nil {
		return err
	}
	var position int
	err = tx.QueryRow(ctx, `
		SELECT position FROM service_request_type_group_members
		WHERE service_desk_id=$1 AND group_id=$2 AND request_type_id=$3 FOR UPDATE`, serviceDeskID, groupID, requestTypeID).Scan(&position)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrServiceRequestTypeNotFound
	}
	if err != nil {
		return err
	}
	neighbour := position - 1
	if direction == "down" {
		neighbour = position + 1
	}
	var neighbourID string
	err = tx.QueryRow(ctx, `
		SELECT request_type_id FROM service_request_type_group_members
		WHERE service_desk_id=$1 AND group_id=$2 AND position=$3 FOR UPDATE`, serviceDeskID, groupID, neighbour).Scan(&neighbourID)
	if errors.Is(err, pgx.ErrNoRows) {
		// Already first or last in its group; the portal order does not change.
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE service_request_type_group_members SET position=$4
		WHERE service_desk_id=$1 AND group_id=$2 AND request_type_id=$3`, serviceDeskID, groupID, neighbourID, position); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE service_request_type_group_members SET position=$4
		WHERE service_desk_id=$1 AND group_id=$2 AND request_type_id=$3`, serviceDeskID, groupID, requestTypeID, neighbour); err != nil {
		return err
	}
	if err := writeServiceAudit(ctx, tx, workspaceID, actorID, "service.request_type.moved", "service_request_type", requestTypeID,
		map[string]any{"serviceDeskId": serviceDeskID, "groupId": groupID, "position": neighbour}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
