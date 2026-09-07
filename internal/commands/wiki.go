package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

func (s *Service) AddWikiAttachmentLabels(ctx context.Context, ws, actor, attachmentID string, labels []models.WikiLabel) ([]models.WikiLabel, error) {
	labels, err := normalizeWikiLabels(labels)
	if err != nil {
		return nil, err
	}
	return s.Store.AddWikiAttachmentLabels(ctx, ws, actor, attachmentID, labels)
}

func (s *Service) RemoveWikiAttachmentLabel(ctx context.Context, ws, actor, attachmentID, prefix, name string) error {
	labels, err := normalizeWikiLabels([]models.WikiLabel{{Prefix: prefix, Name: name}})
	if err != nil {
		return err
	}
	return s.Store.RemoveWikiAttachmentLabel(ctx, ws, actor, attachmentID, labels[0])
}

func validateWikiAttachmentProperty(key string, value json.RawMessage) error {
	if key == "" || utf8.RuneCountInString(key) > 255 {
		return fmt.Errorf("%w: property key must contain between 1 and 255 characters", store.ErrWikiValidation)
	}
	if len(value) == 0 || !json.Valid(value) {
		return fmt.Errorf("%w: property value must be valid JSON", store.ErrWikiValidation)
	}
	return nil
}

func (s *Service) CreateWikiAttachmentProperty(ctx context.Context, ws, actor, attachmentID, key string, value json.RawMessage) (*models.WikiAttachmentProperty, error) {
	if err := validateWikiAttachmentProperty(key, value); err != nil {
		return nil, err
	}
	return s.Store.CreateWikiAttachmentProperty(ctx, ws, actor, attachmentID, key, value)
}

func (s *Service) UpdateWikiAttachmentProperty(ctx context.Context, ws, actor, attachmentID, propertyID, key string, value json.RawMessage, version int, message string) (*models.WikiAttachmentProperty, error) {
	if err := validateWikiAttachmentProperty(key, value); err != nil {
		return nil, err
	}
	if version < 2 || len(message) > 2000 {
		return nil, fmt.Errorf("%w: the next property version and a message of at most 2000 bytes are required", store.ErrWikiValidation)
	}
	return s.Store.UpdateWikiAttachmentProperty(ctx, ws, actor, attachmentID, propertyID, key, value, version, message)
}

func (s *Service) DeleteWikiAttachmentProperty(ctx context.Context, ws, actor, attachmentID, propertyID string) error {
	return s.Store.DeleteWikiAttachmentProperty(ctx, ws, actor, attachmentID, propertyID)
}

func (s *Service) SetWikiPageRestrictions(ctx context.Context, ws, actor, pageID, mode string, restrictions []models.WikiPageRestriction) ([]models.WikiPageRestriction, error) {
	return s.Store.SetWikiPageRestrictions(ctx, ws, actor, pageID, mode, restrictions)
}

func (s *Service) SetWikiPageRestrictionSubject(ctx context.Context, ws, actor, pageID, operation string, subject models.WikiRestrictionSubject, add bool) ([]models.WikiPageRestriction, error) {
	return s.Store.SetWikiPageRestrictionSubject(ctx, ws, actor, pageID, operation, subject, add)
}

func (s *Service) SaveWikiAttachment(ctx context.Context, ws, actor, pageID, attachmentID, filename, mediaType, comment, message string, minor bool, r io.Reader) (*models.WikiAttachment, error) {
	if s.Blobs == nil {
		return nil, fmt.Errorf("attachment storage not configured")
	}
	var err error
	filename, err = normalizedAttachmentFilename(filename)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", store.ErrWikiValidation, err)
	}
	mediaType, err = normalizedAttachmentMIMEType(mediaType)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", store.ErrWikiValidation, err)
	}
	if len(comment) > 2000 || len(message) > 2000 {
		return nil, fmt.Errorf("%w: attachment comments must be at most 2000 bytes", store.ErrWikiValidation)
	}
	blobRef := store.NewID("blob")
	size, err := s.Blobs.Put(ctx, blobRef, io.LimitReader(r, 100<<20+1))
	if err != nil {
		return nil, err
	}
	if size > 100<<20 {
		_ = s.Blobs.Delete(ctx, blobRef)
		return nil, fmt.Errorf("%w: attachments must be at most 100 MiB", store.ErrWikiValidation)
	}
	a, err := s.Store.SaveWikiAttachment(ctx, ws, actor, pageID, attachmentID, filename, mediaType, comment, message, minor, size, blobRef)
	if err != nil {
		return nil, errors.Join(err, s.Blobs.Delete(ctx, blobRef))
	}
	return a, nil
}

func (s *Service) DeleteWikiAttachment(ctx context.Context, ws, actor, id string) error {
	refs, err := s.Store.DeleteWikiAttachment(ctx, ws, actor, id)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if s.Blobs != nil {
			if cleanupErr := s.Blobs.Delete(ctx, ref); cleanupErr != nil {
				err = errors.Join(err, cleanupErr)
			}
		}
	}
	return err
}
