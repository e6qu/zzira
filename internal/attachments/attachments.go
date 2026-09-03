// Package attachments abstracts blob storage. V3 ships a filesystem
// implementation behind the interface; an S3/MinIO implementation slots in
// without touching callers.
package attachments

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var ErrNotFound = errors.New("attachment blob not found")
var ErrInvalidKey = errors.New("attachment blob key is invalid")

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

func (s *FS) path(key string) (string, error) {
	if len(key) < 2 {
		return "", ErrInvalidKey
	}
	for _, r := range key {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return "", ErrInvalidKey
		}
	}
	return filepath.Join(key[:2], key), nil
}

func (s *FS) Put(ctx context.Context, key string, r io.Reader) (n int64, retErr error) {
	p, err := s.path(key)
	if err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return 0, err
	}
	defer func() { retErr = errors.Join(retErr, root.Close()) }()
	if err := root.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return 0, err
	}
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return 0, err
	}
	tmp := filepath.Join(filepath.Dir(p), ".upload-"+hex.EncodeToString(suffix[:]))
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	defer func() {
		if rmErr := root.Remove(tmp); rmErr != nil && !os.IsNotExist(rmErr) {
			fmt.Fprintf(os.Stderr, "attachments: remove temp %s: %v\n", tmp, rmErr)
		}
	}()
	n, err = io.Copy(f, r)
	if err != nil {
		if closeErr := f.Close(); closeErr != nil {
			return 0, fmt.Errorf("copy: %v; close: %w", err, closeErr)
		}
		return 0, err
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	if err := root.Rename(tmp, p); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *FS) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	p, err := s.path(key)
	if err != nil {
		return nil, 0, err
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return nil, 0, err
	}
	f, err := root.Open(p)
	if err != nil {
		closeErr := root.Close()
		if os.IsNotExist(err) {
			return nil, 0, errors.Join(ErrNotFound, closeErr)
		}
		return nil, 0, errors.Join(err, closeErr)
	}
	info, err := f.Stat()
	if err != nil {
		if closeErr := f.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
		return nil, 0, errors.Join(err, root.Close())
	}
	if err := root.Close(); err != nil {
		return nil, 0, errors.Join(err, f.Close())
	}
	return f, info.Size(), nil
}

func (s *FS) Delete(ctx context.Context, key string) (retErr error) {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, root.Close()) }()
	err = root.Remove(p)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
