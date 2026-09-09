package commands

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
)

type AttachmentBlobDeletionRunner struct {
	Service      *Service
	Logf         func(string, ...any)
	PollInterval time.Duration
}

func (r *AttachmentBlobDeletionRunner) Run(ctx context.Context) {
	interval := r.PollInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for {
		if err := r.DrainOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger := r.Logf
			if logger == nil {
				logger = log.Printf
			}
			logger("attachment blob deletion runner: %v", err)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
	}
}

func (r *AttachmentBlobDeletionRunner) DrainOnce(ctx context.Context) error {
	if r.Service == nil || r.Service.Store == nil || r.Service.Blobs == nil {
		return errors.New("attachment blob deletion runner is not configured")
	}
	for range 100 {
		deletion, err := r.Service.Store.ClaimAttachmentBlobDeletion(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := r.Service.Blobs.Delete(ctx, deletion.BlobRef); err != nil {
			if deferErr := r.Service.Store.DeferAttachmentBlobDeletion(ctx, deletion.BlobRef, err.Error(), deletion.Attempts); deferErr != nil {
				return errors.Join(err, deferErr)
			}
			return fmt.Errorf("delete attachment blob %q: %w", deletion.BlobRef, err)
		}
		if err := r.Service.Store.CompleteAttachmentBlobDeletion(ctx, deletion.BlobRef); err != nil {
			return err
		}
	}
	return nil
}
