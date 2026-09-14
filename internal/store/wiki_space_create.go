package store

import (
	"context"
	"fmt"
	"slices"

	"github.com/e6qu/zzira/internal/models"
)

// Creating a space through the v2 API decides who can use it: the default
// roles, a set of role assignments, or the access another space has. It can
// also start the space from a space template, which sets what kind of space it
// is and writes its homepage.

// CreateWikiSpaceRequest is what a space is created from.
type CreateWikiSpaceRequest struct {
	Key, Alias, Name, Description string
	Private                       bool
	RoleAssignments               []models.WikiSpaceRoleAssignment
	// CopyFrom is the id of a space whose access the new space takes.
	CopyFrom    string
	TemplateKey string
}

type wikiSpaceTemplate struct {
	Type, HomepageTitle, HomepageBody string
}

// wikiSpaceTemplates are the space templates a space can start from.
var wikiSpaceTemplates = map[string]wikiSpaceTemplate{
	"com.atlassian.confluence.plugins.confluence-space-blueprints:documentation-space-blueprint": {
		Type: "global", HomepageTitle: "Documentation",
		HomepageBody: `<h2>Documentation</h2><p>Gather the guides, references and how-to articles for this space here.</p>`,
	},
	"com.atlassian.confluence.plugins.confluence-space-blueprints:team-space-blueprint": {
		Type: "collaboration", HomepageTitle: "Team home",
		HomepageBody: `<h2>Who we are</h2><p>Introduce the team, what it works on and how to reach it.</p>`,
	},
	"com.atlassian.confluence.plugins.confluence-knowledge-base:knowledge-base-space-blueprint": {
		Type: "knowledge_base", HomepageTitle: "Knowledge base",
		HomepageBody: `<h2>Knowledge base</h2><p>Answers to common questions and the steps that solve common problems.</p>`,
	},
	"com.atlassian.confluence.plugins.confluence-software-blueprints:software-project-space-blueprint": {
		Type: "collaboration", HomepageTitle: "Project overview",
		HomepageBody: `<h2>Project overview</h2><p>The goals, requirements, decisions and releases of this project.</p>`,
	},
}

// CreateWikiSpaceWithAccess creates a space with the access and template the
// request names. With role assignments the space has exactly those roles, one
// of which must administer the space; a space whose only assignment is its
// creator as administrator is private.
func (s *Store) CreateWikiSpaceWithAccess(ctx context.Context, ws, actor string, req CreateWikiSpaceRequest) (*models.WikiSpace, error) {
	spaceType := "global"
	var template wikiSpaceTemplate
	if req.TemplateKey != "" {
		found, ok := wikiSpaceTemplates[req.TemplateKey]
		if !ok {
			return nil, fmt.Errorf("%w: there is no space template with that key", ErrWikiValidation)
		}
		template, spaceType = found, found.Type
	}
	choices := 0
	for _, chosen := range []bool{req.CopyFrom != "", len(req.RoleAssignments) > 0, req.Private} {
		if chosen {
			choices++
		}
	}
	if choices > 1 {
		return nil, fmt.Errorf("%w: copy another space's access, assign roles, or create a private space — only one of them", ErrWikiValidation)
	}
	administers := map[string]bool{}
	for _, assignment := range req.RoleAssignments {
		if _, known := administers[assignment.RoleID]; known {
			continue
		}
		role, err := s.WikiSpaceRole(ctx, ws, actor, assignment.RoleID)
		if err != nil {
			return nil, fmt.Errorf("%w: assigned space role does not exist", ErrWikiValidation)
		}
		administers[assignment.RoleID] = slices.Contains(role.SpacePermissions, "administer/space")
	}
	if len(req.RoleAssignments) > 0 && !slices.ContainsFunc(req.RoleAssignments, func(a models.WikiSpaceRoleAssignment) bool { return administers[a.RoleID] }) {
		return nil, fmt.Errorf("%w: at least one role assignment must administer the space", ErrWikiValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectAdmin(ctx, tx, ws, actor); err != nil {
		return nil, err
	}
	private := req.Private
	assignments := req.RoleAssignments
	type grant struct{ subjectType, subjectID, permission string }
	var grants []grant
	if req.CopyFrom != "" {
		if err := wikiSpaceAdmin(ctx, tx, ws, actor, req.CopyFrom); err != nil {
			return nil, err
		}
		if err := tx.QueryRow(ctx, `SELECT private FROM wiki_spaces WHERE workspace_id=$1 AND id::text=$2`, ws, req.CopyFrom).Scan(&private); err != nil {
			return nil, err
		}
		rows, err := tx.Query(ctx, `SELECT role_id,principal_type,principal_id FROM wiki_space_role_assignments WHERE space_id::text=$1 ORDER BY role_id,principal_type,principal_id`, req.CopyFrom)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var assignment models.WikiSpaceRoleAssignment
			if err := rows.Scan(&assignment.RoleID, &assignment.PrincipalType, &assignment.PrincipalID); err != nil {
				rows.Close()
				return nil, err
			}
			assignments = append(assignments, assignment)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		rows, err = tx.Query(ctx, `SELECT subject_type,subject_id,permission FROM wiki_space_permission_grants WHERE space_id::text=$1 ORDER BY id`, req.CopyFrom)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var g grant
			if err := rows.Scan(&g.subjectType, &g.subjectID, &g.permission); err != nil {
				rows.Close()
				return nil, err
			}
			grants = append(grants, g)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	for _, assignment := range req.RoleAssignments {
		if err := s.validateWikiSpaceRoleAssignment(ctx, tx, ws, assignment); err != nil {
			return nil, err
		}
	}
	if len(req.RoleAssignments) == 1 && req.RoleAssignments[0].PrincipalType == "USER" && req.RoleAssignments[0].PrincipalID == actor && administers[req.RoleAssignments[0].RoleID] {
		private = true
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO wiki_spaces(workspace_id,key,name,description,author_id,private,space_type,alias)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text`, ws, req.Key, req.Name, req.Description, actor, private, spaceType, req.Alias).Scan(&id)
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: a space with this key already exists", ErrWikiValidation)
	}
	if err != nil {
		return nil, err
	}
	for _, assignment := range assignments {
		if _, err := tx.Exec(ctx, `INSERT INTO wiki_space_role_assignments(space_id,role_id,principal_type,principal_id) VALUES($1::bigint,$2,$3,$4) ON CONFLICT DO NOTHING`, id, assignment.RoleID, assignment.PrincipalType, assignment.PrincipalID); err != nil {
			return nil, err
		}
	}
	for _, g := range grants {
		if _, err := tx.Exec(ctx, `INSERT INTO wiki_space_permission_grants(space_id,subject_type,subject_id,permission) VALUES($1::bigint,$2,$3,$4) ON CONFLICT DO NOTHING`, id, g.subjectType, g.subjectID, g.permission); err != nil {
			return nil, err
		}
	}
	if template.HomepageTitle != "" {
		var pageID string
		if err := tx.QueryRow(ctx, `INSERT INTO wiki_pages(space_id,title,status,body,author_id,owner_id,published)
			VALUES($1::bigint,$2,'current',$3,$4,$4,true) RETURNING id::text`, id, template.HomepageTitle, template.HomepageBody, actor).Scan(&pageID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO wiki_page_versions(page_id,version,title,body,status,author_id,message,minor_edit) VALUES($1::bigint,1,$2,$3,'current',$4,'',false)`, pageID, template.HomepageTitle, template.HomepageBody, actor); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE wiki_spaces SET homepage_id=$2::bigint WHERE id::text=$1`, id, pageID); err != nil {
			return nil, err
		}
		page, err := scanWikiPage(tx.QueryRow(ctx, wikiPageSelect+` WHERE p.id::text=$1`, pageID))
		if err != nil {
			return nil, err
		}
		if err := wikiAction(ctx, tx, ws, actor, "wiki_page", page.ID, id, page); err != nil {
			return nil, err
		}
	}
	space, err := scanWikiSpace(tx.QueryRow(ctx, wikiSpaceSelect+` WHERE s.id::text=$1`, id))
	if err != nil {
		return nil, err
	}
	if err := wikiAction(ctx, tx, ws, actor, "wiki_space", id, id, space); err != nil {
		return nil, err
	}
	if len(assignments) > 0 {
		if err := wikiRoleAction(ctx, tx, ws, actor, "wiki_space_role_assignments", id, models.OpUpsert, map[string]any{"space": space, "assignments": assignments}); err != nil {
			return nil, err
		}
	}
	return space, tx.Commit(ctx)
}
