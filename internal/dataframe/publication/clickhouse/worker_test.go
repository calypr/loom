package clickhouse

import (
	"context"
	"errors"
	"testing"
	"time"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
)

func workerIdentity() publication.BundleIdentity {
	return publication.BundleIdentity{Name: "documents", TranslationVersion: "v1", Project: "p1", DatasetGeneration: "g1", RecipeDigest: "digest"}
}

func publishFixture(ctx context.Context, execution publication.BundleExecution, target publication.Target, names ...string) error {
	streams := make([]publication.OutputStream, 0, len(names))
	for _, name := range names {
		name := name
		streams = append(streams, publication.OutputStream{
			Name: name, Columns: []publication.LogicalColumn{{Name: "id", Kind: "string"}},
			Stream: func(_ context.Context, visit func(map[string]any) error) error {
				return visit(map[string]any{"id": name + "-1"})
			},
		})
	}
	_, err := publication.Publish(ctx, target, publication.PublicationIdentity{
		Name: execution.Name, TranslationVersion: execution.TranslationVersion, Project: execution.Project,
		DatasetGeneration: execution.DatasetGeneration, RecipeDigest: execution.RecipeDigest,
	}, streams, publication.Limits{})
	return err
}

func TestWorkerDuplicateRequestsAreIdempotent(t *testing.T) {
	catalog := newBundleCatalogFixture()
	store, _ := NewBundleStore(newBundleClickHouseFixture(), catalog)
	worker, _ := NewWorker(store, func(context.Context, publication.BundleExecution, publication.Target) error { return nil }, WorkerConfig{})
	first, err := worker.Enqueue(context.Background(), workerIdentity())
	if err != nil {
		t.Fatal(err)
	}
	second, err := worker.Enqueue(context.Background(), workerIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || len(catalog.executions) != 1 || first.State != publication.BundleQueued {
		t.Fatalf("enqueue was not idempotent: first=%#v second=%#v", first, second)
	}
}

func TestWorkerIdentityIncludesRestrictedEmptyScope(t *testing.T) {
	unrestricted := workerIdentity()
	unrestricted.AuthScopeMode = "unrestricted"
	restricted := unrestricted
	restricted.AuthScopeMode = "restricted"
	if unrestricted.Key() == restricted.Key() {
		t.Fatal("restricted-empty and unrestricted commands shared an idempotency key")
	}
	catalog := newBundleCatalogFixture()
	store, _ := NewBundleStore(newBundleClickHouseFixture(), catalog)
	var observed string
	worker, _ := NewWorker(store, func(_ context.Context, execution publication.BundleExecution, _ publication.Target) error {
		observed = execution.AuthScopeMode
		return dataframeerrors.NewError(dataframeerrors.CodeInvalidData, "")
	}, WorkerConfig{MaxAttempts: 1})
	if _, err := worker.Enqueue(context.Background(), restricted); err != nil {
		t.Fatal(err)
	}
	_ = worker.RunOnce(context.Background())
	if observed != "restricted" {
		t.Fatalf("recovered auth scope mode = %q, want restricted", observed)
	}
}

func TestWorkerRetriesTypedRetryableFailure(t *testing.T) {
	catalog := newBundleCatalogFixture()
	client := newBundleClickHouseFixture()
	store, _ := NewBundleStore(client, catalog)
	calls := 0
	worker, _ := NewWorker(store, func(ctx context.Context, execution publication.BundleExecution, target publication.Target) error {
		calls++
		if calls == 1 {
			return publication.WithPhase(dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true)), "RESOLVE", "")
		}
		return publishFixture(ctx, execution, target, "one", "two")
	}, WorkerConfig{MaxAttempts: 3, RetryDelay: time.Nanosecond})
	execution, _ := worker.Enqueue(context.Background(), workerIdentity())
	if err := worker.RunOnce(context.Background()); err == nil {
		t.Fatal("first retryable failure was not reported")
	}
	retrying := catalog.executions[execution.ID]
	if retrying.State != publication.BundleQueued || retrying.Attempt != 1 || retrying.FailurePhase != "RESOLVE" {
		t.Fatalf("retry state = %#v", retrying)
	}
	past := time.Now().Add(-time.Second)
	retrying.NextAttemptAt = &past
	catalog.executions[execution.ID] = retrying
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	published := catalog.executions[execution.ID]
	if published.State != publication.BundlePublished || published.Attempt != 2 || !allOutputsQueryable(published.Outputs) {
		t.Fatalf("recovered execution = %#v", published)
	}
	for _, output := range published.Outputs {
		want := publication.DataframeSelector{Recipe: "documents", TranslationVersion: "v1", Output: output.Name}
		if output.Selector != want {
			t.Fatalf("output selector = %#v, want %#v", output.Selector, want)
		}
	}
}

func TestWorkerDoesNotRetryPermanentValidationFailure(t *testing.T) {
	catalog := newBundleCatalogFixture()
	store, _ := NewBundleStore(newBundleClickHouseFixture(), catalog)
	calls := 0
	worker, _ := NewWorker(store, func(context.Context, publication.BundleExecution, publication.Target) error {
		calls++
		return publication.WithPhase(dataframeerrors.NewError(dataframeerrors.CodeInvalidData, ""), "VALIDATE_RECIPE", "")
	}, WorkerConfig{MaxAttempts: 3})
	execution, _ := worker.Enqueue(context.Background(), workerIdentity())
	_ = worker.RunOnce(context.Background())
	_ = worker.RunOnce(context.Background())
	failed := catalog.executions[execution.ID]
	if calls != 1 || failed.State != publication.BundleFailed || failed.FailureRetryable || failed.Attempt != 1 {
		t.Fatalf("permanent failure retried: calls=%d execution=%#v", calls, failed)
	}
}

func TestWorkerRecoversExpiredLeaseAndCleansStaging(t *testing.T) {
	catalog := newBundleCatalogFixture()
	client := newBundleClickHouseFixture()
	store, _ := NewBundleStore(client, catalog)
	worker, _ := NewWorker(store, func(ctx context.Context, execution publication.BundleExecution, target publication.Target) error {
		return publishFixture(ctx, execution, target, "one")
	}, WorkerConfig{MaxAttempts: 3, RetryDelay: time.Nanosecond})
	identity := workerIdentity()
	past := time.Now().Add(-time.Minute)
	execution := publication.BundleExecution{ID: "interrupted", Key: identity.Key(), BundleIdentity: identity, State: publication.BundleRunning, Attempt: 1, MaxAttempts: 3, CreatedAt: past, UpdatedAt: past, LeaseExpiresAt: &past, Outputs: []publication.BundleOutputRecord{{Name: "one", PhysicalTable: "staging_interrupted", State: publication.BundleRunning}}}
	catalog.executions[execution.ID] = execution
	client.tables["staging_interrupted"] = []map[string]any{{"id": "partial"}}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered := catalog.executions[execution.ID]
	past = time.Now().Add(-time.Second)
	recovered.NextAttemptAt = &past
	catalog.executions[execution.ID] = recovered
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, exists := client.tables["staging_interrupted"]; exists {
		t.Fatal("interrupted staging table was not cleaned")
	}
	if got := catalog.executions[execution.ID]; got.State != publication.BundlePublished || got.Attempt != 2 {
		t.Fatalf("recovered execution = %#v", got)
	}
}

func TestWorkerRecoversExpiredValidatingLease(t *testing.T) {
	catalog := newBundleCatalogFixture()
	client := newBundleClickHouseFixture()
	store, _ := NewBundleStore(client, catalog)
	worker, _ := NewWorker(store, func(ctx context.Context, execution publication.BundleExecution, target publication.Target) error {
		return publishFixture(ctx, execution, target, "one")
	}, WorkerConfig{MaxAttempts: 3, RetryDelay: time.Nanosecond})
	identity := workerIdentity()
	past := time.Now().Add(-time.Minute)
	execution := publication.BundleExecution{ID: "validating", Key: identity.Key(), BundleIdentity: identity, State: publication.BundleValidating, Attempt: 1, MaxAttempts: 3, CreatedAt: past, UpdatedAt: past, LeaseExpiresAt: &past, Outputs: []publication.BundleOutputRecord{{Name: "one", PhysicalTable: "staging_validating", State: publication.BundleValidating}}}
	catalog.executions[execution.ID] = execution
	client.tables["staging_validating"] = []map[string]any{{"id": "partial"}}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered := catalog.executions[execution.ID]
	if recovered.State != publication.BundleQueued || len(recovered.Outputs) != 0 {
		t.Fatalf("validating recovery = %#v", recovered)
	}
}

func TestWorkerRetainsFailedCleanupUntilDropSucceeds(t *testing.T) {
	catalog := newBundleCatalogFixture()
	client := newBundleClickHouseFixture()
	client.dropErr = errors.New("ClickHouse unavailable")
	store, _ := NewBundleStore(client, catalog)
	worker, _ := NewWorker(store, func(ctx context.Context, execution publication.BundleExecution, target publication.Target) error {
		return publishFixture(ctx, execution, target, "one")
	}, WorkerConfig{MaxAttempts: 3, RetryDelay: time.Nanosecond})
	identity := workerIdentity()
	past := time.Now().Add(-time.Minute)
	execution := publication.BundleExecution{ID: "cleanup", Key: identity.Key(), BundleIdentity: identity, State: publication.BundleRunning, Attempt: 1, MaxAttempts: 3, CreatedAt: past, UpdatedAt: past, LeaseExpiresAt: &past, Outputs: []publication.BundleOutputRecord{{Name: "one", PhysicalTable: "staging_cleanup", State: publication.BundleRunning}}}
	catalog.executions[execution.ID] = execution
	client.tables["staging_cleanup"] = []map[string]any{{"id": "partial"}}
	_ = worker.RunOnce(context.Background())
	retained := catalog.executions[execution.ID]
	if retained.State != publication.BundleQueued || len(retained.Outputs) != 1 {
		t.Fatalf("failed cleanup was forgotten: %#v", retained)
	}
	client.dropErr = nil
	due := time.Now().Add(-time.Second)
	retained.NextAttemptAt = &due
	catalog.executions[execution.ID] = retained
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, exists := client.tables["staging_cleanup"]; exists {
		t.Fatal("retained staging table was not cleaned on retry")
	}
	if got := catalog.executions[execution.ID]; got.State != publication.BundlePublished {
		t.Fatalf("execution did not recover: %#v", got)
	}
}

func TestWorkerConfirmsCommitAfterLostResponse(t *testing.T) {
	catalog := &leaseBundleCatalog{bundleCatalogFixture: newBundleCatalogFixture(), acquire: true, renewResult: true, publishCommitThenErr: errors.New("response lost")}
	client := newBundleClickHouseFixture()
	store, _ := NewBundleStore(client, catalog)
	worker, _ := NewWorker(store, func(ctx context.Context, execution publication.BundleExecution, target publication.Target) error {
		return publishFixture(ctx, execution, target, "one")
	}, WorkerConfig{MaxAttempts: 1})
	execution, err := worker.Enqueue(context.Background(), workerIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	published := catalog.executions[execution.ID]
	if published.State != publication.BundlePublished || !allOutputsQueryable(published.Outputs) {
		t.Fatalf("lost response was not confirmed: %#v", published)
	}
	if _, exists := client.tables[published.Outputs[0].PhysicalTable]; !exists {
		t.Fatal("confirmed published table was deleted")
	}
	pointer, err := catalog.GetPointer(context.Background(), workerIdentity().PointerName())
	if err != nil || pointer.ExecutionID != execution.ID {
		t.Fatalf("published pointer = %#v, %v", pointer, err)
	}
}

func TestVerificationAndPointerFailuresNeverPublish(t *testing.T) {
	for _, test := range []struct {
		name       string
		verifyErr  error
		publishErr error
	}{
		{name: "verification", verifyErr: errors.New("verification failed")},
		{name: "pointer", publishErr: publication.ErrBundlePointerConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog := &leaseBundleCatalog{bundleCatalogFixture: newBundleCatalogFixture(), acquire: true, renewResult: true, publishErr: test.publishErr}
			client := newBundleClickHouseFixture()
			client.verifyErr = test.verifyErr
			store, _ := NewBundleStore(client, catalog)
			worker, _ := NewWorker(store, func(ctx context.Context, execution publication.BundleExecution, target publication.Target) error {
				return publishFixture(ctx, execution, target, "one", "two")
			}, WorkerConfig{MaxAttempts: 1})
			execution, _ := worker.Enqueue(context.Background(), workerIdentity())
			if err := worker.RunOnce(context.Background()); err == nil {
				t.Fatal("failure was not returned")
			}
			failed := catalog.executions[execution.ID]
			if failed.State == publication.BundlePublished || failed.PublishedAt != nil {
				t.Fatalf("failed execution reported published: %#v", failed)
			}
			if _, err := catalog.GetPointer(context.Background(), workerIdentity().PointerName()); !errors.Is(err, publication.ErrBundleNotFound) {
				t.Fatalf("visibility pointer changed: %v", err)
			}
		})
	}
}
