package commands

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

var wikiDatabaseKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func (s *Service) AddWikiDatabaseColumn(ctx context.Context, ws, actor, databaseID string, column models.WikiDatabaseColumn) (*models.WikiDatabaseColumn, error) {
	column.Key, column.Name, column.Type = strings.TrimSpace(column.Key), strings.TrimSpace(column.Name), strings.TrimSpace(column.Type)
	if !wikiDatabaseKeyPattern.MatchString(column.Key) || column.Name == "" || utf8.RuneCountInString(column.Name) > 255 {
		return nil, fmt.Errorf("%w: column key and name are invalid", store.ErrWikiValidation)
	}
	switch column.Type {
	case "text", "number", "date", "checkbox":
		if len(column.Options) != 0 {
			return nil, fmt.Errorf("%w: only select columns accept options", store.ErrWikiValidation)
		}
	case "select":
		seen := map[string]bool{}
		clean := []string{}
		for _, option := range column.Options {
			option = strings.TrimSpace(option)
			if option == "" || utf8.RuneCountInString(option) > 100 || seen[option] {
				return nil, fmt.Errorf("%w: select options must be unique non-empty values", store.ErrWikiValidation)
			}
			seen[option] = true
			clean = append(clean, option)
		}
		if len(clean) == 0 || len(clean) > 50 {
			return nil, fmt.Errorf("%w: select columns require 1 to 50 options", store.ErrWikiValidation)
		}
		column.Options = clean
	default:
		return nil, fmt.Errorf("%w: choose a supported column type", store.ErrWikiValidation)
	}
	return s.Store.AddWikiDatabaseColumn(ctx, ws, actor, databaseID, column)
}

func (s *Service) DeleteWikiDatabaseColumn(ctx context.Context, ws, actor, databaseID, columnID string) error {
	return s.Store.DeleteWikiDatabaseColumn(ctx, ws, actor, databaseID, columnID)
}

func (s *Service) SaveWikiDatabaseRow(ctx context.Context, ws, actor, databaseID, rowID string, values map[string]string) (*models.WikiDatabaseRow, error) {
	data, err := s.Store.WikiDatabaseData(ctx, ws, actor, databaseID)
	if err != nil {
		return nil, err
	}
	columns := map[string]models.WikiDatabaseColumn{}
	for _, column := range data.Columns {
		columns[column.Key] = column
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("%w: add a column before creating records", store.ErrWikiValidation)
	}
	if len(values) > len(columns) {
		return nil, fmt.Errorf("%w: row contains unknown columns", store.ErrWikiValidation)
	}
	clean := map[string]string{}
	for key, raw := range values {
		column, ok := columns[key]
		if !ok {
			return nil, fmt.Errorf("%w: row contains unknown column %s", store.ErrWikiValidation, key)
		}
		value := strings.TrimSpace(raw)
		if utf8.RuneCountInString(value) > 10000 {
			return nil, fmt.Errorf("%w: cell value is too long", store.ErrWikiValidation)
		}
		if value != "" {
			switch column.Type {
			case "number":
				number, parseErr := strconv.ParseFloat(value, 64)
				if parseErr != nil || math.IsInf(number, 0) || math.IsNaN(number) {
					return nil, fmt.Errorf("%w: %s must be a number", store.ErrWikiValidation, column.Name)
				}
			case "date":
				if _, parseErr := time.Parse("2006-01-02", value); parseErr != nil {
					return nil, fmt.Errorf("%w: %s must be a date", store.ErrWikiValidation, column.Name)
				}
			case "checkbox":
				if value != "true" && value != "false" {
					return nil, fmt.Errorf("%w: %s must be checked or unchecked", store.ErrWikiValidation, column.Name)
				}
			case "select":
				valid := false
				for _, option := range column.Options {
					valid = valid || option == value
				}
				if !valid {
					return nil, fmt.Errorf("%w: %s has an unknown option", store.ErrWikiValidation, column.Name)
				}
			}
		}
		clean[key] = value
	}
	return s.Store.SaveWikiDatabaseRow(ctx, ws, actor, databaseID, rowID, clean)
}

func (s *Service) DeleteWikiDatabaseRow(ctx context.Context, ws, actor, databaseID, rowID string) error {
	return s.Store.DeleteWikiDatabaseRow(ctx, ws, actor, databaseID, rowID)
}

func (s *Service) SaveWikiDatabaseView(ctx context.Context, ws, actor, databaseID string, view models.WikiDatabaseView) (*models.WikiDatabaseView, error) {
	view.Name, view.FilterValue = strings.TrimSpace(view.Name), strings.TrimSpace(view.FilterValue)
	if view.Name == "" || utf8.RuneCountInString(view.Name) > 255 || (view.SortDirection != "asc" && view.SortDirection != "desc") {
		return nil, fmt.Errorf("%w: saved view name or direction is invalid", store.ErrWikiValidation)
	}
	data, err := s.Store.WikiDatabaseData(ctx, ws, actor, databaseID)
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{"": true}
	for _, column := range data.Columns {
		keys[column.Key] = true
	}
	if !keys[view.SortKey] || !keys[view.FilterKey] || (view.FilterKey == "" && view.FilterValue != "") || utf8.RuneCountInString(view.FilterValue) > 10000 {
		return nil, fmt.Errorf("%w: saved view references an unknown column", store.ErrWikiValidation)
	}
	return s.Store.SaveWikiDatabaseView(ctx, ws, actor, databaseID, view)
}

func (s *Service) DeleteWikiDatabaseView(ctx context.Context, ws, actor, databaseID, viewID string) error {
	return s.Store.DeleteWikiDatabaseView(ctx, ws, actor, databaseID, viewID)
}
