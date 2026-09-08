package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

func (s *Store) ServiceRequestTypeGroups(ctx context.Context, workspaceID, serviceDeskID string) ([]models.ServiceRequestTypeGroup, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT g.id,g.service_desk_id,g.name,g.position FROM service_request_type_groups g
		JOIN service_desks sd ON sd.id=g.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2 ORDER BY g.position,g.id`, workspaceID, serviceDeskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]models.ServiceRequestTypeGroup, 0)
	for rows.Next() {
		var group models.ServiceRequestTypeGroup
		if err := rows.Scan(&group.ID, &group.ServiceDeskID, &group.Name, &group.Position); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func (s *Store) ServiceRequestTypePropertyKeys(ctx context.Context, workspaceID, serviceDeskID, requestTypeID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT jsonb_object_keys(rt.properties) FROM service_request_types rt JOIN service_desks sd ON sd.id=rt.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2 AND rt.id=$3 ORDER BY 1`, workspaceID, serviceDeskID, requestTypeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := make([]string, 0)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (s *Store) ServiceRequestTypeProperty(ctx context.Context, workspaceID, serviceDeskID, requestTypeID, key string) (json.RawMessage, error) {
	var value json.RawMessage
	err := s.Pool.QueryRow(ctx, `
		SELECT rt.properties->$4 FROM service_request_types rt JOIN service_desks sd ON sd.id=rt.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2 AND rt.id=$3 AND rt.properties ? $4`, workspaceID, serviceDeskID, requestTypeID, key).Scan(&value)
	return value, err
}

func (s *Store) SetServiceRequestTypeProperty(ctx context.Context, workspaceID, serviceDeskID, requestTypeID, key string, value json.RawMessage) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var existed bool
	if err := tx.QueryRow(ctx, `
		SELECT rt.properties ? $4 FROM service_request_types rt JOIN service_desks sd ON sd.id=rt.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2 AND rt.id=$3 FOR UPDATE`, workspaceID, serviceDeskID, requestTypeID, key).Scan(&existed); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE service_request_types SET properties=jsonb_set(properties,ARRAY[$2],$3::jsonb,true) WHERE id=$1`, requestTypeID, key, value); err != nil {
		return false, err
	}
	return !existed, tx.Commit(ctx)
}

func (s *Store) DeleteServiceRequestTypeProperty(ctx context.Context, workspaceID, serviceDeskID, requestTypeID, key string) error {
	result, err := s.Pool.Exec(ctx, `
		UPDATE service_request_types rt SET properties=rt.properties-$4
		FROM service_desks sd WHERE sd.id=rt.service_desk_id AND sd.workspace_id=$1 AND sd.id=$2 AND rt.id=$3 AND rt.properties ? $4`, workspaceID, serviceDeskID, requestTypeID, key)
	if err == nil && result.RowsAffected() == 0 {
		return fmt.Errorf("request type property does not exist")
	}
	return err
}

func (s *Store) CanCreateServiceRequest(ctx context.Context, workspaceID, serviceDeskID, userID string) (bool, error) {
	agent, err := s.IsServiceAgent(ctx, workspaceID, serviceDeskID, userID)
	if err != nil || agent {
		return agent, err
	}
	member, err := s.IsMember(ctx, workspaceID, userID)
	if err != nil || !member {
		return false, err
	}
	var allowed bool
	err = s.Pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM service_desks sd JOIN users u ON u.id=$3 AND u.active
		WHERE sd.workspace_id=$1 AND sd.id=$2 AND (
			sd.customer_access_open
			OR EXISTS(SELECT 1 FROM service_desk_customers dc JOIN service_customers sc ON sc.workspace_id=sd.workspace_id AND sc.user_id=dc.user_id AND sc.active WHERE dc.service_desk_id=sd.id AND dc.user_id=$3 AND dc.active)
			OR EXISTS(SELECT 1 FROM service_desk_organizations dso JOIN service_organization_users sou ON sou.organization_id=dso.organization_id JOIN service_customers sc ON sc.workspace_id=sd.workspace_id AND sc.user_id=sou.user_id AND sc.active WHERE dso.service_desk_id=sd.id AND sou.user_id=$3)
		))`, workspaceID, serviceDeskID, userID).Scan(&allowed)
	return allowed, err
}

func scanServiceKnowledgeArticle(row interface{ Scan(...any) error }) (*models.ServiceKnowledgeArticle, error) {
	article := &models.ServiceKnowledgeArticle{}
	err := row.Scan(&article.PageID, &article.SpaceID, &article.SpaceKey, &article.Title, &article.Body)
	return article, err
}

const serviceKnowledgeArticleSelect = `SELECT DISTINCT p.id::text,s.id::text,s.key,p.title,p.body FROM service_desk_knowledge_bases kb JOIN service_desks sd ON sd.id=kb.service_desk_id JOIN wiki_spaces s ON s.id=kb.space_id JOIN wiki_pages p ON p.space_id=s.id `

func (s *Store) ServiceKnowledgeArticles(ctx context.Context, workspaceID, viewerID, serviceDeskID, query string, allowAll bool) ([]models.ServiceKnowledgeArticle, error) {
	query = strings.TrimSpace(query)
	rows, err := s.Pool.Query(ctx, serviceKnowledgeArticleSelect+`
		WHERE sd.workspace_id=$1 AND ($3='' OR sd.id=$3) AND p.published AND p.status='current'
		  AND ($5 OR EXISTS(SELECT 1 FROM service_desk_agents a WHERE a.service_desk_id=sd.id AND a.user_id=$2) OR sd.customer_access_open
		    OR EXISTS(SELECT 1 FROM service_desk_customers dc JOIN service_customers sc ON sc.workspace_id=sd.workspace_id AND sc.user_id=dc.user_id AND sc.active WHERE dc.service_desk_id=sd.id AND dc.user_id=$2 AND dc.active)
		    OR EXISTS(SELECT 1 FROM service_desk_organizations dso JOIN service_organization_users sou ON sou.organization_id=dso.organization_id JOIN service_customers sc ON sc.workspace_id=sd.workspace_id AND sc.user_id=sou.user_id AND sc.active WHERE dso.service_desk_id=sd.id AND sou.user_id=$2))
		  AND ($4='' OR p.title ILIKE '%'||$4||'%' OR p.body ILIKE '%'||$4||'%')
		ORDER BY p.title,p.id::text`, workspaceID, viewerID, serviceDeskID, query, allowAll)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	articles := make([]models.ServiceKnowledgeArticle, 0)
	for rows.Next() {
		article, err := scanServiceKnowledgeArticle(rows)
		if err != nil {
			return nil, err
		}
		articles = append(articles, *article)
	}
	return articles, rows.Err()
}

func (s *Store) ServiceKnowledgeArticle(ctx context.Context, workspaceID, viewerID, pageID string, allowAll bool) (*models.ServiceKnowledgeArticle, error) {
	return scanServiceKnowledgeArticle(s.Pool.QueryRow(ctx, serviceKnowledgeArticleSelect+`
		WHERE sd.workspace_id=$1 AND p.id::text=$3 AND p.published AND p.status='current'
		  AND ($4 OR EXISTS(SELECT 1 FROM service_desk_agents a WHERE a.service_desk_id=sd.id AND a.user_id=$2) OR sd.customer_access_open
		    OR EXISTS(SELECT 1 FROM service_desk_customers dc JOIN service_customers sc ON sc.workspace_id=sd.workspace_id AND sc.user_id=dc.user_id AND sc.active WHERE dc.service_desk_id=sd.id AND dc.user_id=$2 AND dc.active)
		    OR EXISTS(SELECT 1 FROM service_desk_organizations dso JOIN service_organization_users sou ON sou.organization_id=dso.organization_id JOIN service_customers sc ON sc.workspace_id=sd.workspace_id AND sc.user_id=sou.user_id AND sc.active WHERE dso.service_desk_id=sd.id AND sou.user_id=$2))
		LIMIT 1`, workspaceID, viewerID, pageID, allowAll))
}

func (s *Store) ServiceDeskKnowledgeSpaces(ctx context.Context, workspaceID, actorID, serviceDeskID string) ([]*models.WikiSpace, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT kb.space_id::text FROM service_desk_knowledge_bases kb JOIN service_desks sd ON sd.id=kb.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2 ORDER BY kb.linked_at,kb.space_id`, workspaceID, serviceDeskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	spaces := make([]*models.WikiSpace, 0, len(ids))
	for _, id := range ids {
		space, err := s.WikiSpace(ctx, workspaceID, actorID, id)
		if err != nil {
			return nil, err
		}
		spaces = append(spaces, space)
	}
	return spaces, rows.Err()
}

func (s *Store) SetServiceDeskKnowledgeSpace(ctx context.Context, workspaceID, actorID, serviceDeskID, spaceID string, link bool) error {
	if _, err := s.WikiSpace(ctx, workspaceID, actorID, spaceID); err != nil {
		return fmt.Errorf("knowledge space does not exist or is not visible")
	}
	if link {
		result, err := s.Pool.Exec(ctx, `
			INSERT INTO service_desk_knowledge_bases(service_desk_id,space_id)
			SELECT sd.id,$3::bigint FROM service_desks sd WHERE sd.workspace_id=$1 AND sd.id=$2 ON CONFLICT DO NOTHING`, workspaceID, serviceDeskID, spaceID)
		if err == nil && result.RowsAffected() == 0 {
			if _, deskErr := s.ServiceDesk(ctx, workspaceID, serviceDeskID); deskErr != nil {
				return fmt.Errorf("service desk does not exist")
			}
		}
		return err
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM service_desk_knowledge_bases kb USING service_desks sd WHERE kb.service_desk_id=sd.id AND sd.workspace_id=$1 AND sd.id=$2 AND kb.space_id=$3::bigint`, workspaceID, serviceDeskID, spaceID)
	return err
}

func (s *Store) ServiceAssetsWorkspaceIDs(ctx context.Context, workspaceID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id::text FROM service_assets_workspaces WHERE workspace_id=$1 ORDER BY id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
