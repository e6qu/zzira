package commands

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
)

type ServiceTemporaryAttachmentRunner struct {
	Service *Service
	Logf    func(string, ...any)
}

func (r *ServiceTemporaryAttachmentRunner) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.DrainOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				logger := r.Logf
				if logger == nil {
					logger = log.Printf
				}
				logger("service temporary attachment runner: %v", err)
			}
		}
	}
}

func (r *ServiceTemporaryAttachmentRunner) DrainOnce(ctx context.Context) error {
	if r.Service == nil || r.Service.Store == nil || r.Service.Blobs == nil {
		return fmt.Errorf("service temporary attachment runner is not configured")
	}
	for {
		refs, err := r.Service.Store.TakeExpiredServiceTemporaryAttachments(ctx, 100)
		if err != nil {
			return err
		}
		var cleanupErr error
		for _, ref := range refs {
			if err := r.Service.Blobs.Delete(ctx, ref); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete temporary attachment blob %q: %w", ref, err))
			}
		}
		if cleanupErr != nil {
			return cleanupErr
		}
		if len(refs) < 100 {
			return nil
		}
	}
}
