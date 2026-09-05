package commands

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/wikimarkup"
)

var spaceKeyPattern = regexp.MustCompile(`^[A-Za-z0-9]{1,255}$`)

func (s *Service) CreateWikiSpace(ctx context.Context, ws, actor, key, name, description string, private bool) (*models.WikiSpace, error) {
	name = strings.TrimSpace(name)
	if !spaceKeyPattern.MatchString(key) {
		return nil, fmt.Errorf("%w: space key must contain 1–255 letters or numbers", store.ErrWikiValidation)
	}
	if name == "" || utf8.RuneCountInString(name) > 255 {
		return nil, fmt.Errorf("%w: space name is required (max 255 characters)", store.ErrWikiValidation)
	}
	if len(description) > 1<<20 {
		return nil, fmt.Errorf("%w: space description must be at most 1 MiB", store.ErrWikiValidation)
	}
	return s.Store.CreateWikiSpace(ctx, ws, actor, key, name, description, private)
}

func (s *Service) SaveWikiPage(ctx context.Context, ws, actor string, p models.WikiPage) (*models.WikiPage, error) {
	p.Title = strings.TrimSpace(p.Title)
	if p.Status == "" && p.ID == "" {
		p.Status = "current"
	}
	if p.Status != "current" && p.Status != "draft" && p.Status != "trashed" {
		return nil, fmt.Errorf("%w: choose current or draft page status", store.ErrWikiValidation)
	}
	if p.Title == "" || utf8.RuneCountInString(p.Title) > 255 {
		return nil, fmt.Errorf("%w: page title is required (max 255 characters)", store.ErrWikiValidation)
	}
	if p.SpaceID == "" {
		return nil, fmt.Errorf("%w: choose a space", store.ErrWikiValidation)
	}
	if p.Body.Representation != "storage" {
		return nil, fmt.Errorf("%w: only the storage body representation is currently supported", store.ErrWikiValidation)
	}
	if _, err := wikimarkup.Render(p.Body.Value); err != nil {
		return nil, fmt.Errorf("%w: %v", store.ErrWikiValidation, err)
	}
	if len(p.Version.Message) > 2000 {
		return nil, fmt.Errorf("%w: version message must be at most 2000 bytes", store.ErrWikiValidation)
	}
	return s.Store.SaveWikiPage(ctx, ws, actor, p)
}
