package commands

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func (s *Service) SaveWikiWhiteboardObject(ctx context.Context, ws, actor, whiteboardID string, object models.WikiWhiteboardObject) (*models.WikiWhiteboardObject, error) {
	object.Title, object.Body = strings.TrimSpace(object.Title), strings.TrimSpace(object.Body)
	if !stringSet("sticky", "text", "shape")[object.Type] || !stringSet("yellow", "blue", "green", "pink", "gray")[object.Color] {
		return nil, fmt.Errorf("%w: choose a supported object type and color", store.ErrWikiValidation)
	}
	if utf8.RuneCountInString(object.Title) > 255 || utf8.RuneCountInString(object.Body) > 10000 || (object.Title == "" && object.Body == "") {
		return nil, fmt.Errorf("%w: an object needs bounded title or body text", store.ErrWikiValidation)
	}
	if object.X < 0 || object.X > 5000 || object.Y < 0 || object.Y > 5000 || object.Width < 80 || object.Width > 1200 || object.Height < 60 || object.Height > 1200 {
		return nil, fmt.Errorf("%w: object position or size is outside the canvas", store.ErrWikiValidation)
	}
	return s.Store.SaveWikiWhiteboardObject(ctx, ws, actor, whiteboardID, object)
}

func (s *Service) DeleteWikiWhiteboardObject(ctx context.Context, ws, actor, whiteboardID, objectID string) error {
	return s.Store.DeleteWikiWhiteboardObject(ctx, ws, actor, whiteboardID, objectID)
}

func (s *Service) SaveWikiWhiteboardConnector(ctx context.Context, ws, actor, whiteboardID string, connector models.WikiWhiteboardConnector) (*models.WikiWhiteboardConnector, error) {
	connector.Label = strings.TrimSpace(connector.Label)
	if connector.FromObjectID == "" || connector.ToObjectID == "" || connector.FromObjectID == connector.ToObjectID || !stringSet("solid", "dashed")[connector.Style] || utf8.RuneCountInString(connector.Label) > 255 {
		return nil, fmt.Errorf("%w: connector endpoints, label or style are invalid", store.ErrWikiValidation)
	}
	return s.Store.SaveWikiWhiteboardConnector(ctx, ws, actor, whiteboardID, connector)
}

func (s *Service) DeleteWikiWhiteboardConnector(ctx context.Context, ws, actor, whiteboardID, connectorID string) error {
	return s.Store.DeleteWikiWhiteboardConnector(ctx, ws, actor, whiteboardID, connectorID)
}
