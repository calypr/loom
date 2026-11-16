package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestRunConstructionCapabilityBuildersOverlap proves that independent builders
// execute concurrently: both builders must reach their start barrier before
// either is allowed to finish. A sequential implementation would deadlock at
// the second start barrier.
func TestRunConstructionCapabilityBuildersOverlap(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	builders := []func(context.Context) error{
		func(context.Context) error {
			started <- "first"
			<-release
			return nil
		},
		func(context.Context) error {
			started <- "second"
			<-release
			return nil
		},
	}

	done := make(chan []error, 1)
	go func() {
		done <- runConstructionCapabilityBuilders(context.Background(), 4, builders)
	}()

	for range builders {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("builders did not overlap; the second builder never started while the first was blocked")
		}
	}
	close(release)

	for _, err := range <-done {
		if err != nil {
			t.Fatalf("unexpected builder error: %v", err)
		}
	}
}

func TestRunConstructionCapabilityBuildersReturnsErrorsInDeclarationOrder(t *testing.T) {
	first := errors.New("first")
	second := errors.New("second")
	builders := []func(context.Context) error{
		func(context.Context) error { return second },
		func(context.Context) error { return nil },
		func(context.Context) error { return first },
	}

	errs := runConstructionCapabilityBuilders(context.Background(), 4, builders)
	if len(errs) != len(builders) {
		t.Fatalf("got %d errors, want %d", len(errs), len(builders))
	}
	if errs[0] != second {
		t.Fatalf("errs[0] = %v, want the builder-declared error %v", errs[0], second)
	}
	if errs[1] != nil {
		t.Fatalf("errs[1] = %v, want nil", errs[1])
	}
	if errs[2] != first {
		t.Fatalf("errs[2] = %v, want %v", errs[2], first)
	}
}
