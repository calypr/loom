package arango

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

// This test is opt-in because it needs a real Arango instance. It exercises
// the production AQL pin state document rather than a query-string fake.
func TestExecutionReadPinArbitrationAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Bootstrap(ctx, BootstrapSpec()); err != nil {
		t.Fatal(err)
	}
	registry, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	executionID := "pin-test-" + uuid.NewString()
	readerOwner, cleanupOwner := "reader-"+uuid.NewString(), "cleanup-"+uuid.NewString()
	defer func() {
		_ = registry.ReleaseExecutionReadPin(context.Background(), executionID, readerOwner)
		_ = registry.ReleaseExecutionCleanup(context.Background(), executionID, cleanupOwner)
	}()

	if ok, err := registry.AcquireExecutionReadPin(ctx, executionID, readerOwner, time.Now().UTC().Add(time.Minute)); err != nil || !ok {
		t.Fatalf("acquire reader pin: ok=%v err=%v", ok, err)
	}
	if ok, err := registry.ClaimExecutionCleanup(ctx, executionID, cleanupOwner); err != nil || ok {
		t.Fatalf("cleanup crossed active reader: ok=%v err=%v", ok, err)
	}
	if ok, err := registry.RenewExecutionReadPin(ctx, executionID, readerOwner, time.Now().UTC().Add(time.Minute)); err != nil || !ok {
		t.Fatalf("renew reader pin: ok=%v err=%v", ok, err)
	}
	if err := registry.ReleaseExecutionReadPin(ctx, executionID, readerOwner); err != nil {
		t.Fatal(err)
	}
	if ok, err := registry.ClaimExecutionCleanup(ctx, executionID, cleanupOwner); err != nil || !ok {
		t.Fatalf("claim cleanup after reader release: ok=%v err=%v", ok, err)
	}
	if ok, err := registry.AcquireExecutionReadPin(ctx, executionID, readerOwner, time.Now().UTC().Add(time.Minute)); err != nil || ok {
		t.Fatalf("reader crossed active cleanup: ok=%v err=%v", ok, err)
	}
	if ok, err := registry.RenewExecutionCleanup(ctx, executionID, cleanupOwner, time.Now().UTC().Add(time.Minute)); err != nil || !ok {
		t.Fatalf("renew cleanup lease: ok=%v err=%v", ok, err)
	}
	if err := registry.ReleaseExecutionCleanup(ctx, executionID, cleanupOwner); err != nil {
		t.Fatal(err)
	}

	expiredOwner := "expired-" + uuid.NewString()
	if ok, err := registry.AcquireExecutionReadPin(ctx, executionID, expiredOwner, time.Now().UTC().Add(-time.Second)); err != nil || !ok {
		t.Fatalf("acquire short-lived pin: ok=%v err=%v", ok, err)
	}
	if ok, err := registry.RenewExecutionReadPin(ctx, executionID, expiredOwner, time.Now().UTC().Add(time.Minute)); err != nil || ok {
		t.Fatalf("renew expired pin: ok=%v err=%v", ok, err)
	}
	_ = registry.ReleaseExecutionReadPin(ctx, executionID, expiredOwner)

	// Both contenders start from an unclaimed state. The shared state document
	// must make the outcomes mutually exclusive, even when requests overlap.
	for race := 0; race < 50; race++ {
		tracedExecution := "pin-race-" + uuid.NewString()
		tracedReader, tracedCleanup := "reader-"+uuid.NewString(), "cleanup-"+uuid.NewString()
		var wg sync.WaitGroup
		var readerOK, cleanupOK bool
		var readerErr, cleanupErr error
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			readerOK, readerErr = registry.AcquireExecutionReadPin(ctx, tracedExecution, tracedReader, time.Now().UTC().Add(time.Minute))
		}()
		go func() {
			defer wg.Done()
			<-start
			cleanupOK, cleanupErr = registry.ClaimExecutionCleanup(ctx, tracedExecution, tracedCleanup)
		}()
		close(start)
		wg.Wait()
		if readerErr != nil && !shared.IsConflict(readerErr) && !shared.IsPreconditionFailed(readerErr) {
			t.Fatal(readerErr)
		}
		if cleanupErr != nil && !shared.IsConflict(cleanupErr) && !shared.IsPreconditionFailed(cleanupErr) {
			t.Fatal(cleanupErr)
		}
		if readerOK == cleanupOK {
			t.Fatalf("race %d did not produce exactly one winner: reader=%v/%v cleanup=%v/%v", race, readerOK, readerErr, cleanupOK, cleanupErr)
		}
		_ = registry.ReleaseExecutionReadPin(ctx, tracedExecution, tracedReader)
		_ = registry.ReleaseExecutionCleanup(ctx, tracedExecution, tracedCleanup)
	}
}
