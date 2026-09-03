package commands

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/store"
)

func TestDeleteIssueCleansAttachmentBlob(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	blobStore, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{Store: st, Blobs: blobStore}
	issue, _, err := svc.CreateIssue(ctx, CreateIssueInput{
		ActorID: "usr_test", WorkspaceID: "ws_default", ProjectIDOrKey: "ZZ",
		Summary: "attachment cleanup", IssueTypeID: "it_task",
	})
	if err != nil {
		t.Fatal(err)
	}
	att, _, err := svc.AddAttachment(ctx, "usr_test", "ws_default", issue.ID, "proof.txt", "text/plain", strings.NewReader("proof"))
	if err != nil {
		t.Fatal(err)
	}
	blobRef, _, _, err := st.AttachmentBlobRef(ctx, "ws_default", att.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DeleteIssue(ctx, "usr_test", "ws_default", issue.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := blobStore.Get(ctx, blobRef); !errors.Is(err, attachments.ErrNotFound) {
		t.Fatalf("blob still exists after issue delete: %v", err)
	}
}
