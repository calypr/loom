package published

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
)

type pinTestCatalog struct {
	publication.BundleCatalog
	mu          sync.Mutex
	acquired    []string
	released    []string
	failedID    string
	loseRenewal bool
}

func (c *pinTestCatalog) AcquireExecutionReadPin(_ context.Context, executionID, _ string, _ time.Time) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if executionID == c.failedID {
		return false, nil
	}
	c.acquired = append(c.acquired, executionID)
	return true, nil
}

func (c *pinTestCatalog) RenewExecutionReadPin(context.Context, string, string, time.Time) (bool, error) {
	return !c.loseRenewal, nil
}

func (c *pinTestCatalog) ReleaseExecutionReadPin(_ context.Context, executionID, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.released = append(c.released, executionID)
	return nil
}

func (*pinTestCatalog) ClaimExecutionCleanup(context.Context, string, string) (bool, error) {
	return false, nil
}

func (*pinTestCatalog) RenewExecutionCleanup(context.Context, string, string, time.Time) (bool, error) {
	return false, nil
}

func (*pinTestCatalog) ReleaseExecutionCleanup(context.Context, string, string) error { return nil }

func TestWithExecutionReadPinsPinsEveryExactRevisionAndReleases(t *testing.T) {
	catalog := &pinTestCatalog{}
	reader := &Reader{Catalog: catalog}
	called := false
	err := reader.WithExecutionReadPins(context.Background(), []string{"execution-b", "execution-a", "execution-b"}, func(context.Context) error {
		called = true
		catalog.mu.Lock()
		defer catalog.mu.Unlock()
		if !reflect.DeepEqual(catalog.acquired, []string{"execution-a", "execution-b"}) {
			t.Fatalf("acquired pins inside visit = %#v", catalog.acquired)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithExecutionReadPins() error = %v", err)
	}
	if !called {
		t.Fatal("pin visitor was not called")
	}
	if !reflect.DeepEqual(catalog.released, []string{"execution-a", "execution-b"}) {
		t.Fatalf("released pins = %#v", catalog.released)
	}
}

func TestWithExecutionReadPinsReleasesPartialAcquisitionOnFailure(t *testing.T) {
	catalog := &pinTestCatalog{failedID: "execution-b"}
	reader := &Reader{Catalog: catalog}
	called := false
	err := reader.WithExecutionReadPins(context.Background(), []string{"execution-a", "execution-b"}, func(context.Context) error {
		called = true
		return nil
	})
	if !errors.Is(err, publication.ErrExecutionReadPinLost) {
		t.Fatalf("WithExecutionReadPins() error = %v, want lost-pin error", err)
	}
	if called {
		t.Fatal("visitor ran after an input revision could not be pinned")
	}
	if !reflect.DeepEqual(catalog.released, []string{"execution-a"}) {
		t.Fatalf("released partial pins = %#v", catalog.released)
	}
}

func TestWithExecutionReadPinsCancelsVisitorOnRenewalLoss(t *testing.T) {
	catalog := &pinTestCatalog{loseRenewal: true}
	reader := &Reader{Catalog: catalog}
	err := reader.withExecutionReadPins(context.Background(), []string{"execution-a"}, 10*time.Millisecond, time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, publication.ErrExecutionReadPinLost) {
		t.Fatalf("WithExecutionReadPins() error = %v, want lost-pin error", err)
	}
	if !reflect.DeepEqual(catalog.released, []string{"execution-a"}) {
		t.Fatalf("released after renewal loss = %#v", catalog.released)
	}
}
