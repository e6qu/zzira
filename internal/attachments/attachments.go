// Package attachments abstracts blob storage. V3 ships a filesystem
// implementation behind the interface; an S3/MinIO implementation slots in
// without touching callers.
package attachments

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var ErrNotFound = errors.New("attachment blob not found")

type Store interface {
	Put(ctx context.Context, key string, r io.Reader) (int64, error)
	Get(ctx context.Context, key string) (io.ReadCloser, int64, error)
	Delete(ctx context.Context, key string) error
}

// FS stores blobs under a root directory, sharded by the first two key chars.
type FS struct {
	Root string
}

func NewFS(root string) (*FS, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, err
	}
	return &FS{Root: root}, nil
}

func (s *FS) path(key string) string {
	if len(key) < 2 {
		key = "00" + key
	}
	return filepath.Join(s.Root, key[:2], key)
}

func (s *FS) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	p := s.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return 0, err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".upload-*")
	if err != nil {
		return 0, err
	}
	tmp := f.Name()
	defer func() {
		if rmErr := os.Remove(tmp); rmErr != nil && !os.IsNotExist(rmErr) {
			fmt.Fprintf(os.Stderr, "attachments: remove temp %s: %v\n", tmp, rmErr)
		}
	}()
	n, err := io.Copy(f, r)
	if err != nil {
		if closeErr := f.Close(); closeErr != nil {
			return 0, fmt.Errorf("copy: %v; close: %w", err, closeErr)
		}
		return 0, err
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, p); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *FS) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	f, err := os.Open(s.path(key))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		if closeErr := f.Close(); closeErr != nil {
			return nil, 0, fmt.Errorf("stat: %v; close: %w", err, closeErr)
		}
		return nil, 0, err
	}
	return f, info.Size(), nil
}

func (s *FS) Delete(ctx context.Context, key string) error {
	err := os.Remove(s.path(key))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
