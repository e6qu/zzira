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
var wikiLabelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,254}$`)

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

func (s *Service) CreateWikiFooterComment(ctx context.Context, ws, actor string, comment models.WikiFooterComment) (*models.WikiFooterComment, error) {
	if err := validateWikiComment(comment); err != nil {
		return nil, err
	}
	if comment.PageID == "" && comment.ParentCommentID == "" {
		return nil, fmt.Errorf("%w: pageId or parentCommentId is required", store.ErrWikiValidation)
	}
	if comment.PageID != "" && comment.ParentCommentID != "" {
		return nil, fmt.Errorf("%w: choose either pageId or parentCommentId", store.ErrWikiValidation)
	}
	return s.Store.CreateWikiFooterComment(ctx, ws, actor, comment)
}

func (s *Service) UpdateWikiFooterComment(ctx context.Context, ws, actor string, comment models.WikiFooterComment) (*models.WikiFooterComment, error) {
	if comment.ID == "" {
		return nil, fmt.Errorf("%w: comment id is required", store.ErrWikiValidation)
	}
	if comment.Version.Number < 2 {
		return nil, fmt.Errorf("%w: the next comment version is required", store.ErrWikiValidation)
	}
	if err := validateWikiComment(comment); err != nil {
		return nil, err
	}
	return s.Store.UpdateWikiFooterComment(ctx, ws, actor, comment)
}

func validateWikiComment(comment models.WikiFooterComment) error {
	if comment.Body.Representation != "storage" {
		return fmt.Errorf("%w: only the storage comment representation is currently supported", store.ErrWikiValidation)
	}
	if strings.TrimSpace(comment.Body.Value) == "" {
		return fmt.Errorf("%w: comment body is required", store.ErrWikiValidation)
	}
	if len(comment.Body.Value) > 1<<20 {
		return fmt.Errorf("%w: comment body must be at most 1 MiB", store.ErrWikiValidation)
	}
	if _, err := wikimarkup.Render(comment.Body.Value); err != nil {
		return fmt.Errorf("%w: %v", store.ErrWikiValidation, err)
	}
	if len(comment.Version.Message) > 2000 {
		return fmt.Errorf("%w: version message must be at most 2000 bytes", store.ErrWikiValidation)
	}
	return nil
}

func normalizeWikiLabels(labels []models.WikiLabel) ([]models.WikiLabel, error) {
	result := make([]models.WikiLabel, 0, len(labels))
	seen := map[string]bool{}
	for _, label := range labels {
		label.Name = strings.ToLower(strings.TrimSpace(label.Name))
		if label.Name == "" {
			continue
		}
		label.Prefix = strings.ToLower(strings.TrimSpace(label.Prefix))
		if label.Prefix == "" {
			label.Prefix = "global"
		}
		if label.Prefix == "system" || (label.Prefix != "global" && label.Prefix != "my" && label.Prefix != "team") {
			return nil, fmt.Errorf("%w: label prefix must be global, my, or team", store.ErrWikiValidation)
		}
		if !wikiLabelPattern.MatchString(label.Name) {
			return nil, fmt.Errorf("%w: label names must contain 1–255 lowercase letters, numbers, dots, underscores, or hyphens", store.ErrWikiValidation)
		}
		key := label.Prefix + "\x00" + label.Name
		if !seen[key] {
			seen[key] = true
			result = append(result, label)
		}
	}
	if len(result) == 0 || len(result) > 100 {
		return nil, fmt.Errorf("%w: provide between 1 and 100 labels", store.ErrWikiValidation)
	}
	return result, nil
}

func (s *Service) AddWikiPageLabels(ctx context.Context, ws, actor, pageID string, labels []models.WikiLabel) ([]models.WikiLabel, error) {
	labels, err := normalizeWikiLabels(labels)
	if err != nil {
		return nil, err
	}
	return s.Store.AddWikiPageLabels(ctx, ws, actor, pageID, labels)
}

func (s *Service) RemoveWikiPageLabel(ctx context.Context, ws, actor, pageID, prefix, name string) error {
	labels, err := normalizeWikiLabels([]models.WikiLabel{{Prefix: prefix, Name: name}})
	if err != nil {
		return err
	}
	return s.Store.RemoveWikiPageLabel(ctx, ws, actor, pageID, labels[0])
}

func (s *Service) AddWikiSpaceLabels(ctx context.Context, ws, actor, spaceID string, labels []models.WikiLabel) ([]models.WikiLabel, error) {
	labels, err := normalizeWikiLabels(labels)
	if err != nil {
		return nil, err
	}
	return s.Store.AddWikiSpaceLabels(ctx, ws, actor, spaceID, labels)
}

func (s *Service) RemoveWikiSpaceLabel(ctx context.Context, ws, actor, spaceID, prefix, name string) error {
	labels, err := normalizeWikiLabels([]models.WikiLabel{{Prefix: prefix, Name: name}})
	if err != nil {
		return err
	}
	return s.Store.RemoveWikiSpaceLabel(ctx, ws, actor, spaceID, labels[0])
}
