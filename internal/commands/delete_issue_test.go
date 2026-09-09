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

type failOnceDeleteStore struct {
	attachments.Store
	failed bool
}

func (s *failOnceDeleteStore) Delete(ctx context.Context, key string) error {
	if !s.failed {
		s.failed = true
		return errors.New("temporary attachment store failure")
	}
	return s.Store.Delete(ctx, key)
}

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

func TestDeleteIssueRetriesAttachmentBlobCleanup(t *testing.T) {
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
	if err := store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	fs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	blobs := &failOnceDeleteStore{Store: fs}
	svc := &Service{Store: st, Blobs: blobs}
	issue, _, err := svc.CreateIssue(ctx, CreateIssueInput{
		ActorID: "usr_test", WorkspaceID: "ws_default", ProjectIDOrKey: "ZZ",
		Summary: "retry attachment cleanup", IssueTypeID: "it_task",
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
	action, err := svc.DeleteIssue(ctx, "usr_test", "ws_default", issue.ID, "test retry")
	if err != nil || action == nil {
		t.Fatalf("delete issue action=%v err=%v", action, err)
	}
	reader, _, err := fs.Get(ctx, blobRef)
	if err != nil {
		t.Fatalf("blob should remain until retry: %v", err)
	}
	if closeErr := reader.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	pending, err := st.AttachmentBlobDeletionPending(ctx, blobRef)
	if err != nil || !pending {
		t.Fatalf("cleanup pending=%v err=%v", pending, err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE attachment_blob_deletions SET available_at=now(),leased_until=NULL WHERE blob_ref=$1`, blobRef); err != nil {
		t.Fatal(err)
	}
	if err := (&AttachmentBlobDeletionRunner{Service: svc}).DrainOnce(ctx); err != nil {
		t.Fatal(err)
	}
	reader, _, err = fs.Get(ctx, blobRef)
	if reader != nil {
		_ = reader.Close()
	}
	if !errors.Is(err, attachments.ErrNotFound) {
		t.Fatalf("blob still exists after retry: %v", err)
	}
	pending, err = st.AttachmentBlobDeletionPending(ctx, blobRef)
	if err != nil || pending {
		t.Fatalf("cleanup pending=%v err=%v", pending, err)
	}
}

func TestDeleteAttachmentRetriesBlobCleanup(t *testing.T) {
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
	if err := store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	fs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	blobs := &failOnceDeleteStore{Store: fs}
	svc := &Service{Store: st, Blobs: blobs}
	issue, _, err := svc.CreateIssue(ctx, CreateIssueInput{
		ActorID: "usr_test", WorkspaceID: "ws_default", ProjectIDOrKey: "ZZ",
		Summary: "retry direct attachment cleanup", IssueTypeID: "it_task",
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
	action, err := svc.DeleteAttachment(ctx, "usr_test", "ws_default", att.ID)
	if err != nil || action == nil {
		t.Fatalf("delete attachment action=%v err=%v", action, err)
	}
	reader, _, err := fs.Get(ctx, blobRef)
	if err != nil {
		t.Fatalf("blob should remain until retry: %v", err)
	}
	if closeErr := reader.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	pending, err := st.AttachmentBlobDeletionPending(ctx, blobRef)
	if err != nil || !pending {
		t.Fatalf("cleanup pending=%v err=%v", pending, err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE attachment_blob_deletions SET available_at=now(),leased_until=NULL WHERE blob_ref=$1`, blobRef); err != nil {
		t.Fatal(err)
	}
	if err := (&AttachmentBlobDeletionRunner{Service: svc}).DrainOnce(ctx); err != nil {
		t.Fatal(err)
	}
	reader, _, err = fs.Get(ctx, blobRef)
	if reader != nil {
		_ = reader.Close()
	}
	if !errors.Is(err, attachments.ErrNotFound) {
		t.Fatalf("blob still exists after retry: %v", err)
	}
	pending, err = st.AttachmentBlobDeletionPending(ctx, blobRef)
	if err != nil || pending {
		t.Fatalf("cleanup pending=%v err=%v", pending, err)
	}
}
