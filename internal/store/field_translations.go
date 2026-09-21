package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

// A site speaks more than one language when its people do. An administrator
// names a field once per language, and each person reads the name in theirs;
// the field's own name stays what the site calls it, which is what the REST
// field resource reports as `name` beside the caller's `translatedName`.

// FieldTranslation is one language's name for a field.
type FieldTranslation struct {
	Locale      string
	Name        string
	Description string
}

var (
	ErrFieldTranslation = errors.New("invalid field translation")
	localePattern       = regexp.MustCompile(`^[a-z]{2,3}(-[a-z0-9]{2,8})?$`)
)

// NormalizeLocale reads a language tag the way a browser or Jira sends one:
// "es-419", "pt_BR", "EN-GB". An unusable tag answers "".
func NormalizeLocale(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(value, "_", "-")))
	if !localePattern.MatchString(value) {
		return ""
	}
	return value
}

// FieldTranslations lists a field's translations, ordered by locale.
func (s *Store) FieldTranslations(ctx context.Context, workspaceID, fieldID string) ([]FieldTranslation, error) {
	rows, err := s.Pool.Query(ctx, `SELECT locale,name,description FROM custom_field_translations
		WHERE field_id=$1 AND (workspace_id IS NULL OR workspace_id=$2) ORDER BY locale`, fieldID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	translations := make([]FieldTranslation, 0)
	for rows.Next() {
		var translation FieldTranslation
		if err := rows.Scan(&translation.Locale, &translation.Name, &translation.Description); err != nil {
			return nil, err
		}
		translations = append(translations, translation)
	}
	return translations, rows.Err()
}

// SaveFieldTranslation records what a field is called in one language.
func (s *Store) SaveFieldTranslation(ctx context.Context, workspaceID, actorID, fieldID string, translation FieldTranslation) error {
	locale := NormalizeLocale(translation.Locale)
	if locale == "" {
		return fmt.Errorf("%w: choose a language tag such as es or pt-br", ErrFieldTranslation)
	}
	name := strings.TrimSpace(translation.Name)
	if name == "" || len([]rune(name)) > 255 {
		return fmt.Errorf("%w: a translated name is 1 to 255 characters", ErrFieldTranslation)
	}
	description := strings.TrimSpace(translation.Description)
	if len([]rune(description)) > 1000 {
		return fmt.Errorf("%w: a translated description is at most 1000 characters", ErrFieldTranslation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var known bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM custom_fields WHERE id=$1 AND (workspace_id IS NULL OR workspace_id=$2))`, fieldID, workspaceID).Scan(&known); err != nil {
		return err
	}
	if !known {
		return fmt.Errorf("%w: that field does not exist", ErrFieldTranslation)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO custom_field_translations(field_id,workspace_id,locale,name,description)
		VALUES($1,$2,$3,$4,$5)
		ON CONFLICT(field_id,locale) DO UPDATE SET name=EXCLUDED.name,description=EXCLUDED.description,updated_at=now()`,
		fieldID, workspaceID, locale, name, description); err != nil {
		return err
	}
	if err := appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "custom_field_translation", fieldID+":"+locale, models.OpUpsert,
		map[string]any{"fieldId": fieldID, "locale": locale, "name": name, "description": description}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteFieldTranslation removes one language's name for a field.
func (s *Store) DeleteFieldTranslation(ctx context.Context, workspaceID, actorID, fieldID, locale string) error {
	locale = NormalizeLocale(locale)
	if locale == "" {
		return fmt.Errorf("%w: choose a language tag such as es or pt-br", ErrFieldTranslation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `DELETE FROM custom_field_translations WHERE field_id=$1 AND locale=$2 AND (workspace_id IS NULL OR workspace_id=$3)`, fieldID, locale, workspaceID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return fmt.Errorf("%w: that translation is already gone", ErrFieldTranslation)
	}
	if err := appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "custom_field_translation", fieldID+":"+locale, models.OpDelete,
		map[string]any{"fieldId": fieldID, "locale": locale}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// FieldNamesInLocale answers what each field is called in one language, for
// the fields that have a translation there. A locale with a region falls back
// to the language alone, so a Brazilian reader sees the Portuguese name when
// nobody wrote a Brazilian one.
func (s *Store) FieldNamesInLocale(ctx context.Context, workspaceID, locale string) (map[string]FieldTranslation, error) {
	locale = NormalizeLocale(locale)
	if locale == "" {
		return map[string]FieldTranslation{}, nil
	}
	language, _, _ := strings.Cut(locale, "-")
	rows, err := s.Pool.Query(ctx, `SELECT field_id,locale,name,description FROM custom_field_translations
		WHERE (workspace_id IS NULL OR workspace_id=$1) AND locale IN ($2,$3)`, workspaceID, locale, language)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := map[string]FieldTranslation{}
	for rows.Next() {
		var fieldID string
		var translation FieldTranslation
		if err := rows.Scan(&fieldID, &translation.Locale, &translation.Name, &translation.Description); err != nil {
			return nil, err
		}
		// The exact locale wins over the language it belongs to.
		if held, seen := names[fieldID]; seen && held.Locale == locale {
			continue
		}
		names[fieldID] = translation
	}
	return names, rows.Err()
}

// TranslateFields renames fields into a reader's language, in place. Fields
// with no translation keep the name the site gave them.
func (s *Store) TranslateFields(ctx context.Context, workspaceID, locale string, fields []*models.CustomField) error {
	if locale == "" || len(fields) == 0 {
		return nil
	}
	names, err := s.FieldNamesInLocale(ctx, workspaceID, locale)
	if err != nil || len(names) == 0 {
		return err
	}
	for _, field := range fields {
		if translation, ok := names[field.ID]; ok {
			field.Name = translation.Name
			if translation.Description != "" {
				field.Description = translation.Description
			}
		}
	}
	return nil
}

// LocaleForUser is the language a person reads in, or "" when they never
// chose one.
func (s *Store) LocaleForUser(ctx context.Context, workspaceID, accountID string) string {
	value, err := s.UserPreference(ctx, workspaceID, accountID, UserPreferenceLocaleKey)
	if err != nil {
		return ""
	}
	return NormalizeLocale(value)
}
