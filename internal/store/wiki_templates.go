package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Confluence has two kinds of template. A content template is written through
// the API. A blueprint template comes from a blueprint, so it cannot be created
// or updated through the API — but it can be modified for the site or for one
// space, and deleting that modification reverts to what the blueprint provides.

var ErrWikiTemplateValidation = errors.New("invalid content template")

// WikiTemplate is a template as Confluence reports it.
type WikiTemplate struct {
	ID                  string
	Name                string
	Description         string
	TemplateType        string
	Body                string
	EditorVersion       string
	Labels              []string
	SpaceKey            string
	SpaceID             string
	BlueprintModuleKey  string
	BlueprintPluginKey  string
	ReferencingBlueprnt string
}

// blueprintTemplates are the templates the site's blueprints provide. They come
// from the blueprints rather than from anyone's data, which is why the API
// refuses to create or update one.
var blueprintTemplates = []WikiTemplate{
	{
		ID: "blueprint:meeting-notes", Name: "Meeting notes", TemplateType: "page",
		Description:        "Record the attendees, discussion and actions from a meeting.",
		BlueprintPluginKey: "com.atlassian.confluence.plugins.confluence-business-blueprints",
		BlueprintModuleKey: "meeting-notes-blueprint",
		Body:               "<h2>Date</h2><p /><h2>Attendees</h2><ul><li /></ul><h2>Discussion</h2><p /><h2>Action items</h2><ul><li /></ul>",
	},
	{
		ID: "blueprint:decision", Name: "Decision", TemplateType: "page",
		Description:        "Capture a decision, the options considered and the outcome.",
		BlueprintPluginKey: "com.atlassian.confluence.plugins.confluence-business-blueprints",
		BlueprintModuleKey: "decision-blueprint",
		Body:               "<h2>Background</h2><p /><h2>Options considered</h2><p /><h2>Outcome</h2><p /><h2>Action items</h2><ul><li /></ul>",
	},
	{
		ID: "blueprint:product-requirements", Name: "Product requirements", TemplateType: "page",
		Description:        "Describe what is being built and why.",
		BlueprintPluginKey: "com.atlassian.confluence.plugins.confluence-software-blueprints",
		BlueprintModuleKey: "product-requirements-blueprint",
		Body:               "<h2>Objective</h2><p /><h2>Success metrics</h2><p /><h2>Assumptions</h2><p /><h2>Requirements</h2><p /><h2>Out of scope</h2><p />",
	},
	{
		ID: "blueprint:how-to-article", Name: "How-to article", TemplateType: "page",
		Description:        "Explain how to complete a task, step by step.",
		BlueprintPluginKey: "com.atlassian.confluence.plugins.confluence-knowledge-base",
		BlueprintModuleKey: "how-to-article-blueprint",
		Body:               "<h2>Before you begin</h2><p /><h2>Steps</h2><ol><li /></ol><h2>Related articles</h2><p />",
	},
	{
		ID: "blueprint:troubleshooting-article", Name: "Troubleshooting article", TemplateType: "page",
		Description:        "Describe a problem, its cause and how to resolve it.",
		BlueprintPluginKey: "com.atlassian.confluence.plugins.confluence-knowledge-base",
		BlueprintModuleKey: "troubleshooting-article-blueprint",
		Body:               "<h2>Problem</h2><p /><h2>Diagnosis</h2><p /><h2>Solution</h2><p /><h2>Related articles</h2><p />",
	},
	{
		ID: "blueprint:retrospective", Name: "Retrospective", TemplateType: "page",
		Description:        "Look back on a piece of work and agree what to change.",
		BlueprintPluginKey: "com.atlassian.confluence.plugins.confluence-business-blueprints",
		BlueprintModuleKey: "retrospective-blueprint",
		Body:               "<h2>What went well</h2><ul><li /></ul><h2>What could be improved</h2><ul><li /></ul><h2>Actions</h2><ul><li /></ul>",
	},
}

func blueprintTemplateByModule(moduleKey string) (WikiTemplate, bool) {
	for _, template := range blueprintTemplates {
		if template.BlueprintModuleKey == moduleKey {
			return template, true
		}
	}
	return WikiTemplate{}, false
}

const wikiTemplateSelect = `SELECT t.id::text,t.name,t.description,t.template_type,t.body,t.editor_version,
	t.labels,COALESCE(s.key,''),COALESCE(t.space_id::text,''),t.blueprint_module_key
	FROM wiki_content_templates t LEFT JOIN wiki_spaces s ON s.id=t.space_id`

func scanWikiTemplate(row pgx.Row) (WikiTemplate, error) {
	var template WikiTemplate
	err := row.Scan(&template.ID, &template.Name, &template.Description, &template.TemplateType,
		&template.Body, &template.EditorVersion, &template.Labels, &template.SpaceKey,
		&template.SpaceID, &template.BlueprintModuleKey)
	if template.BlueprintModuleKey != "" {
		if blueprint, ok := blueprintTemplateByModule(template.BlueprintModuleKey); ok {
			template.BlueprintPluginKey = blueprint.BlueprintPluginKey
			template.ReferencingBlueprnt = blueprint.ID
		}
	}
	return template, err
}

// SaveWikiTemplate creates or updates a content template. A blueprint's own
// template cannot be created or updated here, which is Confluence's rule: the
// blueprint owns it.
func (s *Store) SaveWikiTemplate(ctx context.Context, ws, actor, templateID, name, description, templateType, body, spaceKey string, labels []string) (WikiTemplate, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 255 {
		return WikiTemplate{}, fmt.Errorf("%w: a template name of 1 to 255 characters is required", ErrWikiTemplateValidation)
	}
	if templateType == "" {
		templateType = "page"
	}
	if templateType != "page" && templateType != "blogpost" {
		return WikiTemplate{}, fmt.Errorf("%w: a template is for a page or a blogpost", ErrWikiTemplateValidation)
	}
	if labels == nil {
		labels = []string{}
	}
	spaceID, err := s.templateSpaceID(ctx, ws, actor, spaceKey)
	if err != nil {
		return WikiTemplate{}, err
	}
	if err = s.requireTemplateAdmin(ctx, ws, actor, spaceKey); err != nil {
		return WikiTemplate{}, err
	}
	if templateID != "" {
		existing, existingErr := s.WikiTemplate(ctx, ws, actor, templateID)
		if existingErr != nil {
			return WikiTemplate{}, existingErr
		}
		if existing.BlueprintModuleKey != "" {
			return WikiTemplate{}, fmt.Errorf("%w: a blueprint's template belongs to the blueprint", ErrWikiTemplateValidation)
		}
	}
	// The write and the read back are one transaction. A data-modifying CTE's
	// rows are not in the outer query's snapshot, so writing and selecting in
	// one statement would report the template as missing right after making it.
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return WikiTemplate{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	saved := templateID
	if templateID == "" {
		if err = tx.QueryRow(ctx, `INSERT INTO wiki_content_templates(workspace_id,space_id,name,description,template_type,body,labels)
			VALUES($1,$2::bigint,$3,$4,$5,$6,$7) RETURNING id::text`,
			ws, spaceID, name, description, templateType, body, labels).Scan(&saved); err != nil {
			return WikiTemplate{}, err
		}
	} else if _, err = tx.Exec(ctx, `UPDATE wiki_content_templates
		SET name=$3,description=$4,template_type=$5,body=$6,labels=$7,updated_at=now()
		WHERE workspace_id=$1 AND id::text=$2`,
		ws, templateID, name, description, templateType, body, labels); err != nil {
		return WikiTemplate{}, err
	}
	template, err := scanWikiTemplate(tx.QueryRow(ctx, wikiTemplateSelect+` WHERE t.workspace_id=$1 AND t.id::text=$2`, ws, saved))
	if err != nil {
		return WikiTemplate{}, err
	}
	return template, tx.Commit(ctx)
}

func (s *Store) templateSpaceID(ctx context.Context, ws, actor, spaceKey string) (any, error) {
	if strings.TrimSpace(spaceKey) == "" {
		return nil, nil
	}
	space, err := s.WikiSpaceByKey(ctx, ws, actor, spaceKey)
	if err != nil {
		return nil, err
	}
	return space.ID, nil
}

// requireTemplateAdmin accepts a space administrator for a space's templates
// and a workspace administrator for the site's.
func (s *Store) requireTemplateAdmin(ctx context.Context, ws, actor, spaceKey string) error {
	if strings.TrimSpace(spaceKey) == "" {
		return s.requireSiteAdmin(ctx, ws, actor)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = lockSpaceForAdmin(ctx, tx, ws, actor, spaceKey); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// WikiTemplate reads one template. A blueprint's id names the blueprint rather
// than a row, so it is answered from what the blueprint provides unless the
// site or a space has modified it.
func (s *Store) WikiTemplate(ctx context.Context, ws, actor, templateID string) (WikiTemplate, error) {
	if err := s.requireMember(ctx, ws, actor); err != nil {
		return WikiTemplate{}, err
	}
	if strings.HasPrefix(templateID, "blueprint:") {
		for _, template := range blueprintTemplates {
			if template.ID == templateID {
				return s.blueprintWithModifications(ctx, ws, template, "")
			}
		}
		return WikiTemplate{}, pgx.ErrNoRows
	}
	return scanWikiTemplate(s.Pool.QueryRow(ctx, wikiTemplateSelect+` WHERE t.workspace_id=$1 AND t.id::text=$2`, ws, templateID))
}

// blueprintWithModifications applies the site's modification, then the space's,
// because a space that has changed a blueprint overrides the site's change to
// the same blueprint.
func (s *Store) blueprintWithModifications(ctx context.Context, ws string, template WikiTemplate, spaceKey string) (WikiTemplate, error) {
	rows, err := s.Pool.Query(ctx, wikiTemplateSelect+`
		WHERE t.workspace_id=$1 AND t.blueprint_module_key=$2
		AND (t.space_id IS NULL OR COALESCE(s.key,'')=$3)
		ORDER BY t.space_id NULLS FIRST`, ws, template.BlueprintModuleKey, spaceKey)
	if err != nil {
		return WikiTemplate{}, err
	}
	defer rows.Close()
	for rows.Next() {
		modified, scanErr := scanWikiTemplate(rows)
		if scanErr != nil {
			return WikiTemplate{}, scanErr
		}
		template.ID = modified.ID
		template.Name, template.Description = modified.Name, modified.Description
		template.Body, template.Labels = modified.Body, modified.Labels
		template.SpaceKey, template.SpaceID = modified.SpaceKey, modified.SpaceID
	}
	return template, rows.Err()
}

// WikiTemplates lists the content templates for the site, or for one space.
// Confluence's space listing carries the global templates too, because a space
// inherits them.
func (s *Store) WikiTemplates(ctx context.Context, ws, actor, spaceKey string) ([]WikiTemplate, error) {
	if err := s.requireMember(ctx, ws, actor); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiTemplateSelect+`
		WHERE t.workspace_id=$1 AND t.blueprint_module_key=''
		AND (t.space_id IS NULL OR ($2<>'' AND COALESCE(s.key,'')=$2))
		ORDER BY t.id`, ws, spaceKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	templates := []WikiTemplate{}
	for rows.Next() {
		template, scanErr := scanWikiTemplate(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		templates = append(templates, template)
	}
	return templates, rows.Err()
}

// WikiBlueprintTemplates lists what the blueprints provide, with the site's and
// the space's modifications applied. A space inherits every global blueprint,
// which is why the space listing is the same set.
func (s *Store) WikiBlueprintTemplates(ctx context.Context, ws, actor, spaceKey string) ([]WikiTemplate, error) {
	if err := s.requireMember(ctx, ws, actor); err != nil {
		return nil, err
	}
	if strings.TrimSpace(spaceKey) != "" {
		if _, err := s.WikiSpaceByKey(ctx, ws, actor, spaceKey); err != nil {
			return nil, err
		}
	}
	templates := make([]WikiTemplate, 0, len(blueprintTemplates))
	for _, template := range blueprintTemplates {
		resolved, err := s.blueprintWithModifications(ctx, ws, template, spaceKey)
		if err != nil {
			return nil, err
		}
		templates = append(templates, resolved)
	}
	sort.Slice(templates, func(i, j int) bool { return templates[i].Name < templates[j].Name })
	return templates, nil
}

// DeleteWikiTemplate removes a content template. Removing a blueprint's
// modification reverts to what the blueprint provides rather than taking the
// template away, which is what Confluence documents for this operation.
func (s *Store) DeleteWikiTemplate(ctx context.Context, ws, actor, templateID string) error {
	template, err := s.WikiTemplate(ctx, ws, actor, templateID)
	if err != nil {
		return err
	}
	if strings.HasPrefix(templateID, "blueprint:") {
		// The id names the blueprint itself, so there is no modification to
		// remove and the template already is what the blueprint provides.
		return fmt.Errorf("%w: this blueprint has not been modified", ErrWikiTemplateValidation)
	}
	if err = s.requireTemplateAdmin(ctx, ws, actor, template.SpaceKey); err != nil {
		return err
	}
	tag, err := s.Pool.Exec(ctx, `DELETE FROM wiki_content_templates WHERE workspace_id=$1 AND id::text=$2`, ws, templateID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// PublishWikiBlueprintDraft publishes a draft page created from a blueprint.
func (s *Store) PublishWikiBlueprintDraft(ctx context.Context, ws, actor, draftID, title string, version int, spaceKey string, parentID string) (string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	draft, err := lockWritablePage(ctx, tx, ws, actor, draftID, "draft")
	if err != nil {
		return "", err
	}
	if title == "" {
		title = draft.Title
	}
	if version != 0 && version != draft.Version+1 {
		return "", fmt.Errorf("%w: the next version is %d", ErrWikiConflict, draft.Version+1)
	}
	next := draft.Version + 1
	var parent any
	if strings.TrimSpace(parentID) != "" {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_pages p JOIN wiki_pages d ON d.id::text=$2
			WHERE p.id::text=$1 AND p.space_id=d.space_id AND p.status='current')`, parentID, draftID).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return "", fmt.Errorf("%w: the parent must be a current page in the draft's space", ErrWikiValidation)
		}
		parent = parentID
	}
	if strings.TrimSpace(spaceKey) != "" {
		var matches bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id
			WHERE p.id::text=$1 AND s.key=$2)`, draftID, spaceKey).Scan(&matches); err != nil {
			return "", err
		}
		if !matches {
			return "", fmt.Errorf("%w: the draft is not in that space", ErrWikiValidation)
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_pages SET status='current',title=$2,version=$3,published=TRUE,
		parent_id=COALESCE($4::bigint,parent_id) WHERE id::text=$1`, draftID, title, next, parent); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_page_versions(page_id,version,title,body,status,author_id,message)
		VALUES($1::bigint,$2,$3,$4,'current',$5,'Published from a blueprint')`,
		draftID, next, title, draft.Body, actor); err != nil {
		return "", err
	}
	return draftID, tx.Commit(ctx)
}
