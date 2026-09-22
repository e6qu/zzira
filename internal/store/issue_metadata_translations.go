package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

// A site speaks more than one language when its people do. An administrator
// names a work type, a priority, a resolution or a status once per language,
// and each person reads it in theirs; the site's own name stays what the site
// calls it, which is what the REST resources report beside the caller's.

// MetadataTranslation is one language's name for one piece of work item
// metadata.
type MetadataTranslation struct {
	EntityType  string
	EntityID    string
	Locale      string
	Name        string
	Description string
}

// ErrMetadataTranslation is a translation a site cannot record.
var ErrMetadataTranslation = errors.New("invalid translation")

// MetadataTranslationKinds are the things an administrator can translate.
var MetadataTranslationKinds = []string{"issuetype", "priority", "resolution", "status"}

// IssueMetadataTranslations lists every translation of one kind of metadata on
// this site, keyed by the id being translated and ordered by locale, so a
// settings page reads them all at once rather than once per row.
func (s *Store) IssueMetadataTranslations(ctx context.Context, workspaceID, entityType string) (map[string][]MetadataTranslation, error) {
	if !slices.Contains(MetadataTranslationKinds, entityType) {
		return nil, fmt.Errorf("%w: %q is not something a site translates", ErrMetadataTranslation, entityType)
	}
	rows, err := s.Pool.Query(ctx, `SELECT entity_id,locale,name,description FROM issue_metadata_translations
		WHERE workspace_id=$1 AND entity_type=$2 ORDER BY entity_id,locale`, workspaceID, entityType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	translations := map[string][]MetadataTranslation{}
	for rows.Next() {
		translation := MetadataTranslation{EntityType: entityType}
		if err := rows.Scan(&translation.EntityID, &translation.Locale, &translation.Name, &translation.Description); err != nil {
			return nil, err
		}
		translations[translation.EntityID] = append(translations[translation.EntityID], translation)
	}
	return translations, rows.Err()
}

// SaveIssueMetadataTranslation records what one piece of metadata is called in
// one language.
func (s *Store) SaveIssueMetadataTranslation(ctx context.Context, workspaceID, actorID string, translation MetadataTranslation) error {
	if !slices.Contains(MetadataTranslationKinds, translation.EntityType) {
		return fmt.Errorf("%w: %q is not something a site translates", ErrMetadataTranslation, translation.EntityType)
	}
	if strings.TrimSpace(translation.EntityID) == "" {
		return fmt.Errorf("%w: choose what to translate", ErrMetadataTranslation)
	}
	locale := NormalizeLocale(translation.Locale)
	if locale == "" {
		return fmt.Errorf("%w: choose a language tag such as es or pt-br", ErrMetadataTranslation)
	}
	name := strings.TrimSpace(translation.Name)
	if name == "" || len([]rune(name)) > 255 {
		return fmt.Errorf("%w: a translated name is 1 to 255 characters", ErrMetadataTranslation)
	}
	description := strings.TrimSpace(translation.Description)
	if len([]rune(description)) > 1000 {
		return fmt.Errorf("%w: a translated description is at most 1000 characters", ErrMetadataTranslation)
	}
	known, err := s.issueMetadataExists(ctx, workspaceID, translation.EntityType, translation.EntityID)
	if err != nil {
		return err
	}
	if !known {
		return fmt.Errorf("%w: that %s does not exist", ErrMetadataTranslation, translation.EntityType)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO issue_metadata_translations(workspace_id,entity_type,entity_id,locale,name,description)
		VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT(workspace_id,entity_type,entity_id,locale) DO UPDATE SET name=EXCLUDED.name,description=EXCLUDED.description,updated_at=now()`,
		workspaceID, translation.EntityType, translation.EntityID, locale, name, description); err != nil {
		return err
	}
	if err := appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "issue_metadata_translation",
		translation.EntityType+":"+translation.EntityID+":"+locale, models.OpUpsert,
		map[string]any{"entityType": translation.EntityType, "entityId": translation.EntityID, "locale": locale, "name": name, "description": description}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteIssueMetadataTranslation removes one language's name.
func (s *Store) DeleteIssueMetadataTranslation(ctx context.Context, workspaceID, actorID, entityType, entityID, locale string) error {
	locale = NormalizeLocale(locale)
	if locale == "" {
		return fmt.Errorf("%w: choose a language tag such as es or pt-br", ErrMetadataTranslation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `DELETE FROM issue_metadata_translations
		WHERE workspace_id=$1 AND entity_type=$2 AND entity_id=$3 AND locale=$4`, workspaceID, entityType, entityID, locale)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return fmt.Errorf("%w: that translation is already gone", ErrMetadataTranslation)
	}
	if err := appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "issue_metadata_translation",
		entityType+":"+entityID+":"+locale, models.OpDelete,
		map[string]any{"entityType": entityType, "entityId": entityID, "locale": locale}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// IssueMetadataNamesInLocale is what this site's metadata is called in one
// language, keyed by kind and id -- "priority:3". A caller with no language,
// or one the site has not been translated into, reads the site's own names.
func (s *Store) IssueMetadataNamesInLocale(ctx context.Context, workspaceID, locale string) (map[string]MetadataTranslation, error) {
	locale = NormalizeLocale(locale)
	if locale == "" {
		return nil, nil
	}
	// A site translated into "pt-br" also answers a reader who asked for
	// "pt", and the other way round, as Jira falls back to the language
	// without its region.
	base, _, _ := strings.Cut(locale, "-")
	rows, err := s.Pool.Query(ctx, `SELECT entity_type,entity_id,locale,name,description FROM issue_metadata_translations
		WHERE workspace_id=$1 AND (locale=$2 OR locale=$3 OR locale LIKE $3 || '-%')
		ORDER BY (locale=$2) DESC,locale`, workspaceID, locale, base)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := map[string]MetadataTranslation{}
	for rows.Next() {
		var translation MetadataTranslation
		if err := rows.Scan(&translation.EntityType, &translation.EntityID, &translation.Locale, &translation.Name, &translation.Description); err != nil {
			return nil, err
		}
		key := translation.EntityType + ":" + translation.EntityID
		if _, taken := names[key]; !taken {
			names[key] = translation
		}
	}
	return names, rows.Err()
}

// issueMetadataExists reports whether the site has the thing being translated,
// so a translation cannot be written for something that is not there.
func (s *Store) issueMetadataExists(ctx context.Context, workspaceID, entityType, entityID string) (bool, error) {
	var query string
	switch entityType {
	case "issuetype":
		query = `SELECT EXISTS(SELECT 1 FROM issue_types WHERE id=$2 AND (workspace_id IS NULL OR workspace_id=$1))`
	case "priority":
		query = `SELECT EXISTS(SELECT 1 FROM priorities WHERE id=$2 AND (workspace_id IS NULL OR workspace_id=$1))`
	case "resolution":
		query = `SELECT EXISTS(SELECT 1 FROM resolutions WHERE id=$2 AND (workspace_id IS NULL OR workspace_id=$1))`
	case "status":
		query = `SELECT EXISTS(SELECT 1 FROM statuses WHERE id=$2 AND (workspace_id IS NULL OR workspace_id=$1))`
	default:
		return false, fmt.Errorf("%w: %q is not something a site translates", ErrMetadataTranslation, entityType)
	}
	var known bool
	err := s.Pool.QueryRow(ctx, query, workspaceID, entityID).Scan(&known)
	return known, err
}

// TranslatedLocales are the languages this site has been translated into: the
// ones an administrator has named a work type, a priority, a resolution, a
// status or a field in. A person can read the site in one of these, and in no
// others, because there is nothing else to read.
func (s *Store) TranslatedLocales(ctx context.Context, workspaceID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT locale FROM issue_metadata_translations WHERE workspace_id=$1
		UNION
		SELECT DISTINCT locale FROM custom_field_translations WHERE workspace_id=$1
		ORDER BY 1`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	locales := []string{}
	for rows.Next() {
		var locale string
		if err := rows.Scan(&locale); err != nil {
			return nil, err
		}
		locales = append(locales, locale)
	}
	return locales, rows.Err()
}
