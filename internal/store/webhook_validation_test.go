package store

import (
	"context"
	"strings"
	"testing"
)

func TestCreateWebhookRejectsUnsafeURLBeforeDatabaseAccess(t *testing.T) {
	storeWithoutPool := &Store{}
	for _, rawURL := range []string{
		"",
		"/relative",
		"ftp://example.com/hook",
		"https://user:secret@example.com/hook",
		"https://example.com/hook\nforged",
	} {
		t.Run(strings.ReplaceAll(rawURL, "/", "_"), func(t *testing.T) {
			if _, err := storeWithoutPool.CreateWebhook(context.Background(), "ws", rawURL, nil, ""); err == nil {
				t.Fatalf("CreateWebhook(%q) succeeded", rawURL)
			}
		})
	}
}
