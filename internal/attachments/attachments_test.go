package attachments

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestFSRoundTripAndDelete(t *testing.T) {
	fs, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const key = "blob_0123456789abcdef"
	if n, err := fs.Put(context.Background(), key, strings.NewReader("hello")); err != nil || n != 5 {
		t.Fatalf("Put() n=%d err=%v", n, err)
	}
	r, size, err := fs.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := io.ReadAll(r)
	closeErr := r.Close()
	if readErr != nil || closeErr != nil || size != 5 || string(got) != "hello" {
		t.Fatalf("Get() body=%q size=%d readErr=%v closeErr=%v", got, size, readErr, closeErr)
	}
	if err := fs.Delete(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fs.Get(context.Background(), key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() after delete err=%v, want ErrNotFound", err)
	}
}

func TestFSRejectsUnsafeKeys(t *testing.T) {
	fs, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", "x", "../outside", "ab/cd", "ab\\cd", "ab.cd", "ab\x00cd"} {
		t.Run(key, func(t *testing.T) {
			if _, err := fs.Put(context.Background(), key, strings.NewReader("x")); !errors.Is(err, ErrInvalidKey) {
				t.Fatalf("Put(%q) err=%v, want ErrInvalidKey", key, err)
			}
		})
	}
}

func TestFSHonorsCanceledContext(t *testing.T) {
	fs, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fs.Put(ctx, "blob_cancelled", strings.NewReader("x")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Put() err=%v, want context.Canceled", err)
	}
}
