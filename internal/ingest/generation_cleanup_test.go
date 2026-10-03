package ingest

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestGenerationCleanupBoundsStalledStoreAndClientAfterCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	originalErr := errors.New("load canceled")

	stalled := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	started := time.Now()
	err := generationCleanupError(parent, 20*time.Millisecond, originalErr, "mark generation failed", stalled)
	err = generationCleanupError(parent, 20*time.Millisecond, err, "close generation backend", stalled)

	if !errors.Is(err, originalErr) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cleanup error = %v, want original cancellation and cleanup deadline", err)
	}
	if elapsed := time.Since(started); elapsed < 35*time.Millisecond || elapsed > 200*time.Millisecond {
		t.Fatalf("cleanup elapsed = %s, want two independent bounded cleanup windows", elapsed)
	}
}
