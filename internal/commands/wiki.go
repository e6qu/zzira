package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/wikimarkup"
)

var spaceKeyPattern = regexp.MustCompile(`^[A-Za-z0-9]{1,255}$`)
var wikiLabelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,254}$`)
var wikiWhiteboardTemplates = stringSet("2x2-prioritization", "4ls-retro", "annual-calendar", "brainwriting", "concept-map", "crazy-8s", "daily-sync", "disruptive-brainstorm", "dot-voting", "elevator-pitch", "flow-chart", "gap-analysis", "ice-breakers", "incident-postmortem", "journey-mapping-kit", "kanban-board", "lean-coffee", "network-of-teams", "org-chart", "pi-planning", "prioritization", "prioritization-experiment", "product-roadmap", "product-vision-board", "rice", "sailboat-retro", "service-blueprint", "simple-retrospective", "sprint-planning", "sticky-note-pack", "swimlanes", "team-formation-guide", "timeline", "timeline-workflow", "user-story-map", "workflow", "vision-board", "venn-diagram", "storyboard", "action-plan", "root-cause-analysis", "executive-summary", "stakeholder-mapping", "annual-calendar-2025-2026", "health-monitor", "okr-planning", "swot-analysis", "poker-planning", "fishbone-diagram", "risk-assessment", "bounded-context", "hopes-and-fears", "swimlane-vertical")
var wikiWhiteboardLocales = stringSet("de-DE", "cs-CZ", "ko-KR", "fr-FR", "it-IT", "ja-JP", "nl-NL", "nb-NO", "da-DK", "sv-SE", "fi-FI", "ru-RU", "pl-PL", "tr-TR", "hu-HU", "en-GB", "en-US", "pt-BR", "zh-CN", "zh-TW", "es-ES")

func stringSet(values ...string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

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

func (s *Service) SetWikiWatch(ctx context.Context, ws, actor, userID, targetType, targetID string, watching bool) error {
	if targetType == "label" {
		targetID = strings.ToLower(strings.TrimSpace(targetID))
		if !wikiLabelPattern.MatchString(targetID) {
			return fmt.Errorf("%w: label name is invalid", store.ErrWikiValidation)
		}
	}
	return s.Store.SetWikiWatch(ctx, ws, actor, userID, targetType, targetID, watching)
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

func (s *Service) SaveWikiBlogPost(ctx context.Context, ws, actor string, post models.WikiBlogPost) (*models.WikiBlogPost, error) {
	post.Title = strings.TrimSpace(post.Title)
	if post.Status == "" && post.ID == "" {
		post.Status = "current"
	}
	if post.Status != "current" && post.Status != "draft" && post.Status != "trashed" {
		return nil, fmt.Errorf("%w: choose current or draft blog post status", store.ErrWikiValidation)
	}
	if (post.Status == "current" && post.Title == "") || utf8.RuneCountInString(post.Title) > 255 {
		return nil, fmt.Errorf("%w: blog post title is required for published posts (max 255 characters)", store.ErrWikiValidation)
	}
	if post.SpaceID == "" {
		return nil, fmt.Errorf("%w: choose a space", store.ErrWikiValidation)
	}
	if post.Body.Representation != "storage" {
		return nil, fmt.Errorf("%w: only the storage body representation is currently supported", store.ErrWikiValidation)
	}
	if _, err := wikimarkup.Render(post.Body.Value); err != nil {
		return nil, fmt.Errorf("%w: %v", store.ErrWikiValidation, err)
	}
	if len(post.Version.Message) > 2000 {
		return nil, fmt.Errorf("%w: version message must be at most 2000 bytes", store.ErrWikiValidation)
	}
	if post.CreatedAt != "" {
		if _, err := time.Parse(time.RFC3339, post.CreatedAt); err != nil {
			return nil, fmt.Errorf("%w: createdAt must be an RFC 3339 timestamp", store.ErrWikiValidation)
		}
	}
	return s.Store.SaveWikiBlogPost(ctx, ws, actor, post)
}

func (s *Service) PurgeWikiBlogPost(ctx context.Context, ws, actor, id string) error {
	return s.Store.PurgeWikiBlogPost(ctx, ws, actor, id)
}

func (s *Service) CreateWikiContent(ctx context.Context, ws, actor string, content models.WikiContent) (*models.WikiContent, error) {
	content.Title = strings.TrimSpace(content.Title)
	if content.Type != "folder" && content.Type != "database" && content.Type != "embed" && content.Type != "whiteboard" {
		return nil, fmt.Errorf("%w: choose a supported hierarchical content type", store.ErrWikiValidation)
	}
	if content.SpaceID == "" {
		return nil, fmt.Errorf("%w: choose a space", store.ErrWikiValidation)
	}
	if content.Title == "" || utf8.RuneCountInString(content.Title) > 255 {
		return nil, fmt.Errorf("%w: content title is required (max 255 characters)", store.ErrWikiValidation)
	}
	if content.Type != "embed" && content.EmbedURL != "" {
		return nil, fmt.Errorf("%w: only Smart Links accept an embed URL", store.ErrWikiValidation)
	}
	if content.Type != "database" && content.Type != "whiteboard" && content.Private {
		return nil, fmt.Errorf("%w: only databases and whiteboards accept private content state", store.ErrWikiValidation)
	}
	if content.Type != "whiteboard" && (content.TemplateKey != "" || content.Locale != "") {
		return nil, fmt.Errorf("%w: only whiteboards accept template and locale", store.ErrWikiValidation)
	}
	if content.Type == "whiteboard" {
		if content.TemplateKey != "" && !wikiWhiteboardTemplates[content.TemplateKey] {
			return nil, fmt.Errorf("%w: choose a supported whiteboard template", store.ErrWikiValidation)
		}
		if content.Locale != "" && (content.TemplateKey == "" || !wikiWhiteboardLocales[content.Locale]) {
			return nil, fmt.Errorf("%w: choose a supported whiteboard template locale", store.ErrWikiValidation)
		}
	}
	if content.Type == "embed" && content.EmbedURL != "" {
		content.EmbedURL = strings.TrimSpace(content.EmbedURL)
		parsed, err := url.Parse(content.EmbedURL)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || len(content.EmbedURL) > 8192 {
			return nil, fmt.Errorf("%w: Smart Link URL must be an absolute HTTP or HTTPS URL without credentials", store.ErrWikiValidation)
		}
	}
	content.Status = "current"
	return s.Store.CreateWikiContent(ctx, ws, actor, content)
}

func (s *Service) DeleteWikiContent(ctx context.Context, ws, actor, id, contentType string) error {
	return s.Store.DeleteWikiContent(ctx, ws, actor, id, contentType)
}

func (s *Service) SetWikiContentClassification(ctx context.Context, ws, actor, id, contentType, levelID string) (*models.WikiContent, error) {
	switch levelID {
	case "", "public", "internal", "confidential", "restricted":
	default:
		return nil, fmt.Errorf("%w: choose a supported classification level", store.ErrWikiValidation)
	}
	return s.Store.SetWikiContentClassification(ctx, ws, actor, id, contentType, levelID)
}

func (s *Service) CreateWikiFooterComment(ctx context.Context, ws, actor string, comment models.WikiFooterComment) (*models.WikiFooterComment, error) {
	if err := validateWikiComment(comment); err != nil {
		return nil, err
	}
	targets := 0
	for _, id := range []string{comment.PageID, comment.AttachmentID, comment.ParentCommentID} {
		if id != "" {
			targets++
		}
	}
	if targets != 1 {
		return nil, fmt.Errorf("%w: choose exactly one of pageId, attachmentId, or parentCommentId", store.ErrWikiValidation)
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

func (s *Service) CreateWikiInlineComment(ctx context.Context, ws, actor string, comment models.WikiFooterComment) (*models.WikiFooterComment, error) {
	if err := validateWikiComment(comment); err != nil {
		return nil, err
	}
	if (comment.PageID == "") == (comment.ParentCommentID == "") {
		return nil, fmt.Errorf("%w: choose exactly one of pageId or parentCommentId", store.ErrWikiValidation)
	}
	if comment.ParentCommentID == "" {
		if strings.TrimSpace(comment.InlineSelection) == "" || len(comment.InlineSelection) > 10000 || comment.InlineMatchCount < 1 || comment.InlineMatchIndex < 0 || comment.InlineMatchIndex >= comment.InlineMatchCount {
			return nil, fmt.Errorf("%w: valid inline text selection metadata is required", store.ErrWikiValidation)
		}
	}
	return s.Store.CreateWikiInlineComment(ctx, ws, actor, comment)
}

func (s *Service) UpdateWikiInlineComment(ctx context.Context, ws, actor string, comment models.WikiFooterComment, resolved *bool) (*models.WikiFooterComment, error) {
	if comment.ID == "" || comment.Version.Number < 2 {
		return nil, fmt.Errorf("%w: comment id and next version are required", store.ErrWikiValidation)
	}
	if err := validateWikiComment(comment); err != nil {
		return nil, err
	}
	return s.Store.UpdateWikiInlineComment(ctx, ws, actor, comment, resolved)
}

func (s *Service) CreateWikiTask(ctx context.Context, ws, actor string, task models.WikiTask) (*models.WikiTask, error) {
	task.AssignedTo = strings.TrimSpace(task.AssignedTo)
	if task.PageID == "" {
		return nil, fmt.Errorf("%w: task page is required", store.ErrWikiValidation)
	}
	if task.Body.Representation != "storage" {
		return nil, fmt.Errorf("%w: only the storage task representation is currently supported", store.ErrWikiValidation)
	}
	if len(task.Body.Value) > 1<<20 {
		return nil, fmt.Errorf("%w: task body must be at most 1 MiB", store.ErrWikiValidation)
	}
	if task.Body.Value != "" {
		if _, err := wikimarkup.Render(task.Body.Value); err != nil {
			return nil, fmt.Errorf("%w: %v", store.ErrWikiValidation, err)
		}
	}
	if task.DueAt != "" {
		due, err := time.Parse(time.RFC3339, task.DueAt)
		if err != nil {
			return nil, fmt.Errorf("%w: task due date must be RFC 3339", store.ErrWikiValidation)
		}
		task.DueAt = due.UTC().Format(time.RFC3339)
	}
	task.Status = "incomplete"
	return s.Store.CreateWikiTask(ctx, ws, actor, task)
}

func (s *Service) UpdateWikiTask(ctx context.Context, ws, actor, id, status string) (*models.WikiTask, error) {
	if id == "" || (status != "complete" && status != "incomplete") {
		return nil, fmt.Errorf("%w: task id and complete or incomplete status are required", store.ErrWikiValidation)
	}
	return s.Store.UpdateWikiTask(ctx, ws, actor, id, status)
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

func (s *Service) AddWikiBlogPostLabels(ctx context.Context, ws, actor, blogPostID string, labels []models.WikiLabel) ([]models.WikiLabel, error) {
	labels, err := normalizeWikiLabels(labels)
	if err != nil {
		return nil, err
	}
	return s.Store.AddWikiBlogPostLabels(ctx, ws, actor, blogPostID, labels)
}

func (s *Service) RemoveWikiBlogPostLabel(ctx context.Context, ws, actor, blogPostID, prefix, name string) error {
	labels, err := normalizeWikiLabels([]models.WikiLabel{{Prefix: prefix, Name: name}})
	if err != nil {
		return err
	}
	return s.Store.RemoveWikiBlogPostLabel(ctx, ws, actor, blogPostID, labels[0])
}

func validateWikiProperty(key string, value json.RawMessage) error {
	if key == "" || utf8.RuneCountInString(key) > 255 {
		return fmt.Errorf("%w: property key must contain between 1 and 255 characters", store.ErrWikiValidation)
	}
	if len(value) == 0 || !json.Valid(value) {
		return fmt.Errorf("%w: property value must be valid JSON", store.ErrWikiValidation)
	}
	return nil
}

func (s *Service) CreateWikiAttachmentProperty(ctx context.Context, ws, actor, attachmentID, key string, value json.RawMessage) (*models.WikiAttachmentProperty, error) {
	if err := validateWikiProperty(key, value); err != nil {
		return nil, err
	}
	return s.Store.CreateWikiAttachmentProperty(ctx, ws, actor, attachmentID, key, value)
}

func (s *Service) UpdateWikiAttachmentProperty(ctx context.Context, ws, actor, attachmentID, propertyID, key string, value json.RawMessage, version int, message string) (*models.WikiAttachmentProperty, error) {
	if err := validateWikiProperty(key, value); err != nil {
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

func (s *Service) CreateWikiContentProperty(ctx context.Context, ws, actor, contentID, contentType, key string, value json.RawMessage) (*models.WikiContentProperty, error) {
	if err := validateWikiProperty(key, value); err != nil {
		return nil, err
	}
	return s.Store.CreateWikiContentProperty(ctx, ws, actor, contentID, contentType, key, value)
}

func (s *Service) UpdateWikiContentProperty(ctx context.Context, ws, actor, contentID, contentType, propertyID, key string, value json.RawMessage, version int, message string) (*models.WikiContentProperty, error) {
	if err := validateWikiProperty(key, value); err != nil {
		return nil, err
	}
	if version < 2 || len(message) > 2000 {
		return nil, fmt.Errorf("%w: the next property version and a message of at most 2000 bytes are required", store.ErrWikiValidation)
	}
	return s.Store.UpdateWikiContentProperty(ctx, ws, actor, contentID, contentType, propertyID, key, value, version, message)
}

func (s *Service) DeleteWikiContentProperty(ctx context.Context, ws, actor, contentID, contentType, propertyID string) error {
	return s.Store.DeleteWikiContentProperty(ctx, ws, actor, contentID, contentType, propertyID)
}

func (s *Service) CreateWikiBlogPostProperty(ctx context.Context, ws, actor, blogPostID, key string, value json.RawMessage) (*models.WikiContentProperty, error) {
	if err := validateWikiProperty(key, value); err != nil {
		return nil, err
	}
	return s.Store.CreateWikiBlogPostProperty(ctx, ws, actor, blogPostID, key, value)
}

func (s *Service) UpdateWikiBlogPostProperty(ctx context.Context, ws, actor, blogPostID, propertyID, key string, value json.RawMessage, version int, message string) (*models.WikiContentProperty, error) {
	if err := validateWikiProperty(key, value); err != nil {
		return nil, err
	}
	if version < 2 || len(message) > 2000 {
		return nil, fmt.Errorf("%w: the next property version and a message of at most 2000 bytes are required", store.ErrWikiValidation)
	}
	return s.Store.UpdateWikiBlogPostProperty(ctx, ws, actor, blogPostID, propertyID, key, value, version, message)
}

func (s *Service) DeleteWikiBlogPostProperty(ctx context.Context, ws, actor, blogPostID, propertyID string) error {
	return s.Store.DeleteWikiBlogPostProperty(ctx, ws, actor, blogPostID, propertyID)
}

func (s *Service) SetWikiBlogPostClassification(ctx context.Context, ws, actor, id, levelID string) (*models.WikiBlogPost, error) {
	if levelID != "" && levelID != "public" && levelID != "internal" && levelID != "confidential" && levelID != "restricted" {
		return nil, fmt.Errorf("%w: choose a supported classification level", store.ErrWikiValidation)
	}
	return s.Store.SetWikiBlogPostClassification(ctx, ws, actor, id, levelID)
}

func (s *Service) RedactWikiBlogPost(ctx context.Context, ws, actor, id, createdAt string, version int, cleanHistory bool, title, body []models.WikiRedactionPointer) (*models.WikiBlogPost, []models.WikiRedactionResult, []models.WikiRedactionResult, error) {
	if id == "" || createdAt == "" || version < 0 {
		return nil, nil, nil, fmt.Errorf("%w: blog post, createdAt and a nonnegative version are required", store.ErrWikiValidation)
	}
	if _, err := time.Parse(time.RFC3339, createdAt); err != nil {
		return nil, nil, nil, fmt.Errorf("%w: createdAt must be an RFC 3339 timestamp", store.ErrWikiValidation)
	}
	return s.Store.RedactWikiBlogPost(ctx, ws, actor, id, createdAt, version, cleanHistory, title, body)
}

func (s *Service) SetWikiPageRestrictions(ctx context.Context, ws, actor, pageID, mode string, restrictions []models.WikiPageRestriction) ([]models.WikiPageRestriction, error) {
	return s.Store.SetWikiPageRestrictions(ctx, ws, actor, pageID, mode, restrictions)
}

func (s *Service) SetWikiPageRestrictionSubject(ctx context.Context, ws, actor, pageID, operation string, subject models.WikiRestrictionSubject, add bool) ([]models.WikiPageRestriction, error) {
	return s.Store.SetWikiPageRestrictionSubject(ctx, ws, actor, pageID, operation, subject, add)
}

func (s *Service) SaveWikiAttachment(ctx context.Context, ws, actor, pageID, attachmentID, filename, mediaType, comment, message string, minor bool, r io.Reader) (*models.WikiAttachment, error) {
	return s.saveWikiAttachment(ctx, filename, mediaType, comment, message, r, func(filename, mediaType string, size int64, blobRef string) (*models.WikiAttachment, error) {
		return s.Store.SaveWikiAttachment(ctx, ws, actor, pageID, attachmentID, filename, mediaType, comment, message, minor, size, blobRef)
	})
}

func (s *Service) SaveWikiBlogAttachment(ctx context.Context, ws, actor, blogPostID, attachmentID, filename, mediaType, comment, message string, minor bool, r io.Reader) (*models.WikiAttachment, error) {
	return s.saveWikiAttachment(ctx, filename, mediaType, comment, message, r, func(filename, mediaType string, size int64, blobRef string) (*models.WikiAttachment, error) {
		return s.Store.SaveWikiBlogAttachment(ctx, ws, actor, blogPostID, attachmentID, filename, mediaType, comment, message, minor, size, blobRef)
	})
}

func (s *Service) saveWikiAttachment(ctx context.Context, filename, mediaType, comment, message string, r io.Reader, persist func(string, string, int64, string) (*models.WikiAttachment, error)) (*models.WikiAttachment, error) {
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
	a, err := persist(filename, mediaType, size, blobRef)
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
