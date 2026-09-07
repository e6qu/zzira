package store

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

func validWikiRestrictionOperation(operation string) bool {
	return operation == "read" || operation == "update"
}

func (s *Store) CanUpdateWikiPage(ctx context.Context, ws, actor, pageID string) (bool, error) {
	var allowed bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id
		WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageWritable+` AND p.id::text=$3
	)`, ws, actor, pageID).Scan(&allowed)
	return allowed, err
}

func (s *Store) CanRestrictWikiPage(ctx context.Context, ws, actor, pageID string) (bool, error) {
	var allowed bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id
		WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageWritable+` AND p.id::text=$3
	)`, ws, actor, pageID).Scan(&allowed)
	return allowed, err
}

func (s *Store) WikiRestrictionGroups(ctx context.Context, ws string) ([]models.WikiRestrictionSubject, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT g.id::text,g.name
		FROM sites si JOIN directories d ON d.organization_id=si.organization_id AND d.active
		JOIN groups g ON g.directory_id=d.id
		WHERE si.workspace_id=$1 ORDER BY g.name,g.id::text`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []models.WikiRestrictionSubject{}
	for rows.Next() {
		var subject models.WikiRestrictionSubject
		subject.Type = "group"
		if err := rows.Scan(&subject.ID, &subject.Name); err != nil {
			return nil, err
		}
		groups = append(groups, subject)
	}
	return groups, rows.Err()
}

func (s *Store) WikiPageRestrictions(ctx context.Context, ws, actor, pageID string) ([]models.WikiPageRestriction, error) {
	if _, err := s.WikiPage(ctx, ws, actor, pageID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT wr.operation,wr.subject_type,wr.subject_id,COALESCE(u.display_name,''),COALESCE(g.name,'')
		FROM wiki_page_restrictions wr
		LEFT JOIN users u ON wr.subject_type='user' AND u.id=wr.subject_id
		LEFT JOIN groups g ON wr.subject_type='group' AND g.id::text=wr.subject_id
		WHERE wr.page_id::text=$1 ORDER BY wr.operation,wr.subject_type,COALESCE(u.display_name,g.name),wr.subject_id`, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byOperation := map[string]*models.WikiPageRestriction{
		"read":   {Operation: "read", Users: []models.WikiRestrictionSubject{}, Groups: []models.WikiRestrictionSubject{}},
		"update": {Operation: "update", Users: []models.WikiRestrictionSubject{}, Groups: []models.WikiRestrictionSubject{}},
	}
	for rows.Next() {
		var operation, subjectType, id, displayName, name string
		if err := rows.Scan(&operation, &subjectType, &id, &displayName, &name); err != nil {
			return nil, err
		}
		subject := models.WikiRestrictionSubject{Type: subjectType, ID: id, AccountID: id, Name: name, DisplayName: displayName}
		if subjectType == "user" {
			byOperation[operation].Users = append(byOperation[operation].Users, subject)
		} else {
			byOperation[operation].Groups = append(byOperation[operation].Groups, subject)
		}
	}
	return []models.WikiPageRestriction{*byOperation["read"], *byOperation["update"]}, rows.Err()
}

func (s *Store) wikiRestrictionManager(ctx context.Context, tx pgx.Tx, ws, actor, pageID string) (string, error) {
	var spaceID string
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT p.space_id::text,`+wikiPageWritable+` FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id
		WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND p.id::text=$3 FOR UPDATE OF p`, ws, actor, pageID).Scan(&spaceID, &allowed)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", ErrProjectPermission
	}
	return spaceID, nil
}

func (s *Store) validateWikiRestriction(ctx context.Context, tx pgx.Tx, ws string, subject models.WikiRestrictionSubject) error {
	switch subject.Type {
	case "user":
		id := subject.AccountID
		if id == "" {
			id = subject.ID
		}
		var ok bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id AND u.active WHERE m.workspace_id=$1 AND m.user_id=$2 AND EXISTS (
			SELECT 1 FROM sites si JOIN directories d ON d.organization_id=si.organization_id AND d.active JOIN directory_users du ON du.directory_id=d.id AND du.active AND du.user_id=m.user_id WHERE si.workspace_id=m.workspace_id))`, ws, id).Scan(&ok)
		if err != nil {
			return err
		}
		if !ok {
			return pgx.ErrNoRows
		}
	case "group":
		var ok bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sites si JOIN directories d ON d.organization_id=si.organization_id AND d.active JOIN groups g ON g.directory_id=d.id WHERE si.workspace_id=$1 AND (($2<>'' AND g.id::text=$2) OR ($2='' AND lower(g.name)=lower($3))))`, ws, subject.ID, subject.Name).Scan(&ok)
		if err != nil {
			return err
		}
		if !ok {
			return pgx.ErrNoRows
		}
	default:
		return ErrWikiValidation
	}
	return nil
}

func normalizeWikiRestriction(subject models.WikiRestrictionSubject) models.WikiRestrictionSubject {
	if subject.Type == "user" && subject.AccountID != "" {
		subject.ID = subject.AccountID
	}
	return subject
}

// SetWikiPageRestrictions replaces or adds operation grants. An operation with
// no subjects is unrestricted, matching Confluence's content restriction model.
func (s *Store) SetWikiPageRestrictions(ctx context.Context, ws, actor, pageID, mode string, input []models.WikiPageRestriction) ([]models.WikiPageRestriction, error) {
	if mode != "replace" && mode != "add" && mode != "clear" {
		return nil, ErrWikiValidation
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	spaceID, err := s.wikiRestrictionManager(ctx, tx, ws, actor, pageID)
	if err != nil {
		return nil, err
	}
	if mode == "clear" {
		_, err = tx.Exec(ctx, `DELETE FROM wiki_page_restrictions WHERE page_id::text=$1`, pageID)
	} else {
		for _, restriction := range input {
			if !validWikiRestrictionOperation(restriction.Operation) {
				return nil, ErrWikiValidation
			}
			for _, raw := range restriction.Users {
				subject := normalizeWikiRestriction(raw)
				if err := s.validateWikiRestriction(ctx, tx, ws, subject); err != nil {
					return nil, err
				}
			}
			for _, raw := range restriction.Groups {
				subject := normalizeWikiRestriction(raw)
				if err := s.validateWikiRestriction(ctx, tx, ws, subject); err != nil {
					return nil, err
				}
			}
		}
		if mode == "replace" {
			if _, err = tx.Exec(ctx, `DELETE FROM wiki_page_restrictions WHERE page_id::text=$1`, pageID); err != nil {
				return nil, err
			}
		}
		for _, restriction := range input {
			subjectLists := [][]models.WikiRestrictionSubject{restriction.Users, restriction.Groups}
			for _, subjects := range subjectLists {
				for _, raw := range subjects {
					subject := normalizeWikiRestriction(raw)
					id := subject.ID
					if subject.Type == "group" && id == "" {
						if err := tx.QueryRow(ctx, `SELECT g.id::text FROM sites si JOIN directories d ON d.organization_id=si.organization_id JOIN groups g ON g.directory_id=d.id WHERE si.workspace_id=$1 AND lower(g.name)=lower($2) ORDER BY g.id LIMIT 1`, ws, subject.Name).Scan(&id); err != nil {
							return nil, err
						}
					}
					_, err = tx.Exec(ctx, `INSERT INTO wiki_page_restrictions(page_id,operation,subject_type,subject_id,author_id) VALUES($1::bigint,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, pageID, restriction.Operation, subject.Type, id, actor)
					if err != nil {
						return nil, err
					}
				}
			}
		}
	}
	if err != nil {
		return nil, err
	}
	current, err := wikiPageRestrictionsTx(ctx, tx, pageID)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{"wikiSpaceId": spaceID, "wiki_restriction": map[string]any{"pageId": pageID, "restrictions": current}})
	if err != nil {
		return nil, err
	}
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return nil, err
	}
	if err := appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: "wiki_restriction", EntityID: pageID, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return current, nil
}

func wikiPageRestrictionsTx(ctx context.Context, tx pgx.Tx, pageID string) ([]models.WikiPageRestriction, error) {
	rows, err := tx.Query(ctx, `SELECT wr.operation,wr.subject_type,wr.subject_id,COALESCE(u.display_name,''),COALESCE(g.name,'') FROM wiki_page_restrictions wr LEFT JOIN users u ON wr.subject_type='user' AND u.id=wr.subject_id LEFT JOIN groups g ON wr.subject_type='group' AND g.id::text=wr.subject_id WHERE wr.page_id::text=$1 ORDER BY wr.operation,wr.subject_type,wr.subject_id`, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	by := map[string]*models.WikiPageRestriction{"read": {Operation: "read", Users: []models.WikiRestrictionSubject{}, Groups: []models.WikiRestrictionSubject{}}, "update": {Operation: "update", Users: []models.WikiRestrictionSubject{}, Groups: []models.WikiRestrictionSubject{}}}
	for rows.Next() {
		var op, typ, id, display, name string
		if err := rows.Scan(&op, &typ, &id, &display, &name); err != nil {
			return nil, err
		}
		subject := models.WikiRestrictionSubject{Type: typ, ID: id, AccountID: id, Name: name, DisplayName: display}
		if typ == "user" {
			by[op].Users = append(by[op].Users, subject)
		} else {
			by[op].Groups = append(by[op].Groups, subject)
		}
	}
	return []models.WikiPageRestriction{*by["read"], *by["update"]}, rows.Err()
}

func (s *Store) SetWikiPageRestrictionSubject(ctx context.Context, ws, actor, pageID, operation string, subject models.WikiRestrictionSubject, add bool) ([]models.WikiPageRestriction, error) {
	if !validWikiRestrictionOperation(operation) {
		return nil, ErrWikiValidation
	}
	subject = normalizeWikiRestriction(subject)
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	spaceID, err := s.wikiRestrictionManager(ctx, tx, ws, actor, pageID)
	if err != nil {
		return nil, err
	}
	if err := s.validateWikiRestriction(ctx, tx, ws, subject); err != nil {
		return nil, err
	}
	id := subject.ID
	if subject.Type == "group" && id == "" {
		err = tx.QueryRow(ctx, `SELECT g.id::text FROM sites si JOIN directories d ON d.organization_id=si.organization_id JOIN groups g ON g.directory_id=d.id WHERE si.workspace_id=$1 AND lower(g.name)=lower($2) ORDER BY g.id LIMIT 1`, ws, subject.Name).Scan(&id)
		if err != nil {
			return nil, err
		}
	}
	if add {
		_, err = tx.Exec(ctx, `INSERT INTO wiki_page_restrictions(page_id,operation,subject_type,subject_id,author_id) VALUES($1::bigint,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, pageID, operation, subject.Type, id, actor)
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM wiki_page_restrictions WHERE page_id::text=$1 AND operation=$2 AND subject_type=$3 AND subject_id=$4`, pageID, operation, subject.Type, id)
	}
	if err != nil {
		return nil, err
	}
	current, err := wikiPageRestrictionsTx(ctx, tx, pageID)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{"wikiSpaceId": spaceID, "wiki_restriction": map[string]any{"pageId": pageID, "restrictions": current}})
	if err != nil {
		return nil, err
	}
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return nil, err
	}
	if err = appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: "wiki_restriction", EntityID: pageID, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor}); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return current, nil
}

func (s *Store) WikiPageRestrictionSubject(ctx context.Context, ws, actor, pageID, operation string, subject models.WikiRestrictionSubject) (bool, error) {
	if !validWikiRestrictionOperation(operation) {
		return false, ErrWikiValidation
	}
	if _, err := s.WikiPage(ctx, ws, actor, pageID); err != nil {
		return false, err
	}
	subject = normalizeWikiRestriction(subject)
	id := subject.ID
	switch subject.Type {
	case "user":
		if _, err := s.MemberByID(ctx, ws, id); err != nil {
			return false, err
		}
	case "group":
		query := `SELECT g.id::text FROM sites si JOIN directories d ON d.organization_id=si.organization_id AND d.active JOIN groups g ON g.directory_id=d.id WHERE si.workspace_id=$1 AND (($2<>'' AND g.id::text=$2) OR ($2='' AND lower(g.name)=lower($3))) ORDER BY g.id LIMIT 1`
		if err := s.Pool.QueryRow(ctx, query, ws, id, subject.Name).Scan(&id); err != nil {
			return false, err
		}
	default:
		return false, ErrWikiValidation
	}
	if strings.TrimSpace(id) == "" {
		return false, ErrWikiValidation
	}
	var exists bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_page_restrictions WHERE page_id::text=$1 AND operation=$2 AND subject_type=$3 AND subject_id=$4)`, pageID, operation, subject.Type, id).Scan(&exists)
	return exists, err
}
