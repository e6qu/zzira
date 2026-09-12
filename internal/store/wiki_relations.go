package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

// A relation is a named, one-way link between two entities. Confluence supports
// 'favourite' by default and lets a client name any other relation it needs, so
// the name is not an allowlist. What is checked is that both ends exist, that
// the caller may see them, and that a 'favourite' has the shape a favourite has.

var ErrWikiRelationValidation = errors.New("invalid relation")

// CurrentUserKey is how a relation names the caller rather than an account.
const CurrentUserKey = "current"

// WikiRelationEntity is one end of a relation. Only content carries a status
// and a version; for a user or a space those stay at the neutral values.
type WikiRelationEntity struct {
	Type    string
	Key     string
	Status  string
	Version int

	// Resolved is the entity itself once it has been looked up, so a caller
	// rendering the relation does not have to fetch it a second time.
	User    WikiUser
	Space   *models.WikiSpace
	Content *models.WikiPage
}

// WikiRelation is one link, together with both of its ends.
type WikiRelation struct {
	Name      string
	Source    WikiRelationEntity
	Target    WikiRelationEntity
	CreatedBy WikiUser
	CreatedAt time.Time
}

// resolveRelationEntity checks the end of a relation exists and that the caller
// may see it. A relation to something that is not there would read back as a
// link to nothing, which is why this runs before every read and write.
func (s *Store) resolveRelationEntity(ctx context.Context, ws, actor string, entity WikiRelationEntity) (WikiRelationEntity, error) {
	if entity.Status == "" {
		entity.Status = "current"
	}
	switch entity.Type {
	case "user", "space":
		if entity.Status != "current" || entity.Version != 0 {
			return entity, fmt.Errorf("%w: a status and a version belong to content, not to a %s", ErrWikiRelationValidation, entity.Type)
		}
	case "content":
	default:
		return entity, fmt.Errorf("%w: an entity is a user, a space or content", ErrWikiRelationValidation)
	}
	switch entity.Type {
	case "user":
		key := entity.Key
		if key == CurrentUserKey {
			key = actor
		}
		user, err := s.WikiUserByAccountID(ctx, ws, actor, key)
		if err != nil {
			return entity, err
		}
		entity.Key, entity.User = user.AccountID, user
		return entity, nil
	case "space":
		space, err := s.WikiSpaceByKey(ctx, ws, actor, entity.Key)
		if err != nil {
			return entity, err
		}
		entity.Key, entity.Space = space.Key, space
		return entity, nil
	default:
		switch entity.Status {
		case "current", "draft", "archived", "trashed":
			if entity.Version != 0 {
				return entity, fmt.Errorf("%w: a version names one historical revision, so it belongs with the historical status", ErrWikiRelationValidation)
			}
			page, err := s.WikiPage(ctx, ws, actor, entity.Key)
			if err != nil {
				return entity, err
			}
			if page.Status != entity.Status {
				return entity, fmt.Errorf("%w: content %s is %s, not %s", ErrWikiRelationValidation, entity.Key, page.Status, entity.Status)
			}
			entity.Key, entity.Content = page.ID, page
			return entity, nil
		case "historical":
			if entity.Version < 1 {
				return entity, fmt.Errorf("%w: historical content is named by a version of 1 or more", ErrWikiRelationValidation)
			}
			page, err := s.WikiPageAtVersion(ctx, ws, actor, entity.Key, entity.Version)
			if err != nil {
				return entity, err
			}
			entity.Key, entity.Content = page.ID, page
			return entity, nil
		default:
			return entity, fmt.Errorf("%w: content is current, draft, archived, trashed or historical", ErrWikiRelationValidation)
		}
	}
}

// validRelationShape checks the name and, for the relations Confluence defines
// itself, that the two ends are the ones that relation is made of. A favourite
// is something a person saves for later, so it runs from a user to a space or
// to content and never the other way about.
func validRelationShape(name, sourceType, targetType string) error {
	if strings.TrimSpace(name) == "" || len(name) > 255 {
		return fmt.Errorf("%w: a relation name of 1 to 255 characters is required", ErrWikiRelationValidation)
	}
	if name == "favourite" {
		if sourceType != "user" {
			return fmt.Errorf("%w: the source of a favourite is the user who saved it", ErrWikiRelationValidation)
		}
		if targetType != "space" && targetType != "content" {
			return fmt.Errorf("%w: a favourite is of a space or of content", ErrWikiRelationValidation)
		}
	}
	return nil
}

// SaveWikiRelation creates the relation. Naming one that already exists is the
// same relation rather than an error: the request asks for the link to be
// there, and afterwards it is.
func (s *Store) SaveWikiRelation(ctx context.Context, ws, actor string, relation WikiRelation) (WikiRelation, error) {
	relation, err := s.prepareRelation(ctx, ws, actor, relation)
	if err != nil {
		return WikiRelation{}, err
	}
	if err = s.requireRelationAuthorship(ctx, ws, actor, relation); err != nil {
		return WikiRelation{}, err
	}
	var createdBy string
	if err = s.Pool.QueryRow(ctx, `INSERT INTO wiki_relations
		(workspace_id,name,source_type,source_key,source_status,source_version,target_type,target_key,target_status,target_version,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (workspace_id,name,source_type,source_key,source_status,source_version,target_type,target_key,target_status,target_version)
		DO UPDATE SET name=EXCLUDED.name
		RETURNING created_by, created_at`,
		ws, relation.Name, relation.Source.Type, relation.Source.Key, relation.Source.Status, relation.Source.Version,
		relation.Target.Type, relation.Target.Key, relation.Target.Status, relation.Target.Version, actor).
		Scan(&createdBy, &relation.CreatedAt); err != nil {
		return WikiRelation{}, err
	}
	if relation.CreatedBy, err = s.WikiUserByAccountID(ctx, ws, actor, createdBy); err != nil {
		return WikiRelation{}, err
	}
	return relation, nil
}

// WikiRelationBetween reports whether one particular relation exists.
func (s *Store) WikiRelationBetween(ctx context.Context, ws, actor string, relation WikiRelation) (WikiRelation, error) {
	relation, err := s.prepareRelation(ctx, ws, actor, relation)
	if err != nil {
		return WikiRelation{}, err
	}
	var createdBy string
	if err = s.Pool.QueryRow(ctx, `SELECT created_by, created_at FROM wiki_relations
		WHERE workspace_id=$1 AND name=$2 AND source_type=$3 AND source_key=$4 AND source_status=$5 AND source_version=$6
		AND target_type=$7 AND target_key=$8 AND target_status=$9 AND target_version=$10`,
		ws, relation.Name, relation.Source.Type, relation.Source.Key, relation.Source.Status, relation.Source.Version,
		relation.Target.Type, relation.Target.Key, relation.Target.Status, relation.Target.Version).
		Scan(&createdBy, &relation.CreatedAt); err != nil {
		return WikiRelation{}, err
	}
	if relation.CreatedBy, err = s.WikiUserByAccountID(ctx, ws, actor, createdBy); err != nil {
		return WikiRelation{}, err
	}
	return relation, nil
}

// DeleteWikiRelation removes it. A relation that was never there is already in
// the state the request asks for, so only an entity that does not exist is an
// error; deleting twice is not.
func (s *Store) DeleteWikiRelation(ctx context.Context, ws, actor string, relation WikiRelation) error {
	relation, err := s.prepareRelation(ctx, ws, actor, relation)
	if err != nil {
		return err
	}
	if err = s.requireRelationAuthorship(ctx, ws, actor, relation); err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `DELETE FROM wiki_relations
		WHERE workspace_id=$1 AND name=$2 AND source_type=$3 AND source_key=$4 AND source_status=$5 AND source_version=$6
		AND target_type=$7 AND target_key=$8 AND target_status=$9 AND target_version=$10`,
		ws, relation.Name, relation.Source.Type, relation.Source.Key, relation.Source.Status, relation.Source.Version,
		relation.Target.Type, relation.Target.Key, relation.Target.Status, relation.Target.Version)
	return err
}

func (s *Store) prepareRelation(ctx context.Context, ws, actor string, relation WikiRelation) (WikiRelation, error) {
	if err := validRelationShape(relation.Name, relation.Source.Type, relation.Target.Type); err != nil {
		return WikiRelation{}, err
	}
	source, err := s.resolveRelationEntity(ctx, ws, actor, relation.Source)
	if err != nil {
		return WikiRelation{}, err
	}
	target, err := s.resolveRelationEntity(ctx, ws, actor, relation.Target)
	if err != nil {
		return WikiRelation{}, err
	}
	relation.Source, relation.Target = source, target
	return relation, nil
}

// requireRelationAuthorship keeps one person from speaking for another. A
// relation whose source is a user is that user's own statement — their
// favourites are theirs — so making or unmaking one on someone else's behalf is
// an administrative act.
func (s *Store) requireRelationAuthorship(ctx context.Context, ws, actor string, relation WikiRelation) error {
	if relation.Source.Type != "user" || relation.Source.Key == actor {
		return nil
	}
	return s.requireSiteAdmin(ctx, ws, actor)
}

// WikiRelationsFrom lists what a source is related to. Relations are one way,
// so this and WikiRelationsTo answer different questions rather than the same
// question from either end.
func (s *Store) WikiRelationsFrom(ctx context.Context, ws, actor, name string, source WikiRelationEntity, target WikiRelationEntity) ([]WikiRelation, error) {
	if err := validRelationListing(name, source.Type, target.Type); err != nil {
		return nil, err
	}
	resolved, err := s.resolveRelationEntity(ctx, ws, actor, source)
	if err != nil {
		return nil, err
	}
	return s.relations(ctx, ws, actor, name, resolved, target, true)
}

// WikiRelationsTo lists what is related to a target.
func (s *Store) WikiRelationsTo(ctx context.Context, ws, actor, name string, target WikiRelationEntity, source WikiRelationEntity) ([]WikiRelation, error) {
	if err := validRelationListing(name, source.Type, target.Type); err != nil {
		return nil, err
	}
	resolved, err := s.resolveRelationEntity(ctx, ws, actor, target)
	if err != nil {
		return nil, err
	}
	return s.relations(ctx, ws, actor, name, resolved, source, false)
}

// validRelationListing applies the one rule the listings add: Confluence lists
// the relations a client named itself, and directs a reader after somebody's
// favourites to the endpoints that serve them.
func validRelationListing(name, sourceType, targetType string) error {
	if err := validRelationShape(name, sourceType, targetType); err != nil {
		return err
	}
	switch name {
	case "favourite", "like":
		return fmt.Errorf("%w: %s relations are read through their own endpoints, not by listing relations", ErrWikiRelationValidation, name)
	}
	return nil
}

// relations lists in one direction. known is the end that was resolved to a
// particular entity; other is the far end, of which only the type is given.
func (s *Store) relations(ctx context.Context, ws, actor, name string, known, other WikiRelationEntity, knownIsSource bool) ([]WikiRelation, error) {
	if other.Status == "" {
		other.Status = "current"
	}
	knownSide, otherSide := "source", "target"
	if !knownIsSource {
		knownSide, otherSide = "target", "source"
	}
	rows, err := s.Pool.Query(ctx, `SELECT name,
		source_type,source_key,source_status,source_version,
		target_type,target_key,target_status,target_version,created_by,created_at
		FROM wiki_relations WHERE workspace_id=$1 AND name=$2
		AND `+knownSide+`_type=$3 AND `+knownSide+`_key=$4 AND `+knownSide+`_status=$5 AND `+knownSide+`_version=$6
		AND `+otherSide+`_type=$7 AND `+otherSide+`_status=$8 AND `+otherSide+`_version=$9
		ORDER BY id`,
		ws, name, known.Type, known.Key, known.Status, known.Version, other.Type, other.Status, other.Version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	relations := []WikiRelation{}
	for rows.Next() {
		var relation WikiRelation
		var createdBy string
		if err = rows.Scan(&relation.Name,
			&relation.Source.Type, &relation.Source.Key, &relation.Source.Status, &relation.Source.Version,
			&relation.Target.Type, &relation.Target.Key, &relation.Target.Status, &relation.Target.Version,
			&createdBy, &relation.CreatedAt); err != nil {
			return nil, err
		}
		relation.CreatedBy = WikiUser{AccountID: createdBy}
		relations = append(relations, relation)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	// The far end was never resolved, and the listing renders both ends, so
	// fill each one in now. An entity the reader may not see drops the
	// relation from the listing rather than hiding only half of it.
	out := make([]WikiRelation, 0, len(relations))
	for _, relation := range relations {
		known, other := &relation.Source, &relation.Target
		if !knownIsSource {
			known, other = &relation.Target, &relation.Source
		}
		resolvedOther, err := s.resolveRelationEntity(ctx, ws, actor, *other)
		if err != nil {
			continue
		}
		resolvedKnown, err := s.resolveRelationEntity(ctx, ws, actor, *known)
		if err != nil {
			continue
		}
		*other, *known = resolvedOther, resolvedKnown
		if relation.CreatedBy, err = s.WikiUserByAccountID(ctx, ws, actor, relation.CreatedBy.AccountID); err != nil {
			continue
		}
		out = append(out, relation)
	}
	return out, nil
}
