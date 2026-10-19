package arango

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

// This opt-in test exercises Arango's conditional document writes. It uses
// unique keys in the dedicated private manifest collection and never truncates
// or drops that collection.
func TestCatalogLeaseArbitrationAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := arangostore.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		_ = client.Close(closeCtx)
	}()
	if err := client.Bootstrap(ctx, BootstrapSpec()); err != nil {
		t.Fatal(err)
	}
	catalog, err := NewCatalog(client)
	if err != nil {
		t.Fatal(err)
	}

	artifactID := "catalog-arango-test-" + uuid.NewString()
	writer := "writer-" + uuid.NewString()
	cleanupOne, cleanupTwo := "cleanup-"+uuid.NewString(), "cleanup-"+uuid.NewString()
	cleanupFinal := "cleanup-final-" + uuid.NewString()
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanupCancel()
		if _, ok, err := catalog.ClaimCleanup(cleanupCtx, artifactID, cleanupFinal, time.Now().UTC().Add(time.Hour), time.Now().UTC().Add(2*time.Hour)); err == nil && ok {
			_ = catalog.Delete(cleanupCtx, artifactID, cleanupFinal)
		}
	}()

	now := time.Now().UTC()
	manifest := chartifact.Manifest{
		ArtifactID: artifactID,
		Identity: chartifact.Identity{
			ExecutionID: "execution-" + uuid.NewString(), OutputID: "output-1", StageID: "stage-1",
			Project: "catalog-test", DatasetGeneration: "generation-1", RecipeDigest: "recipe-digest",
			PlanDigest: "plan-digest", AuthScopeMode: authscope.ReadScopeUnrestricted,
		},
		Columns:       []chartifact.Column{{ID: "column-1", Name: "value", LogicalType: "string", ClickHouseType: "String"}},
		PhysicalTable: "loom_private_catalogtest",
		State:         chartifact.StateCreating,
		LeaseOwner:    writer,
		LeaseUntil:    now.Add(time.Minute),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := catalog.Create(ctx, manifest); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Create(ctx, manifest); err == nil {
		t.Fatal("duplicate ArtifactID replaced the existing manifest")
	}

	if err := catalog.Update(ctx, artifactID, writer, chartifact.Progress{
		State: chartifact.StateWriting, RowCount: 4, ByteCount: 128,
		LeaseUntil: now.Add(2 * time.Minute), UpdatedAt: now,
	}); err != nil {
		t.Fatalf("renew active manifest progress: %v", err)
	}
	if owned, err := catalog.Renew(ctx, artifactID, writer, now.Add(3*time.Minute)); err != nil || !owned {
		t.Fatalf("active owner renewal = (%t, %v)", owned, err)
	}
	if _, ok, err := catalog.ClaimCleanup(ctx, artifactID, cleanupOne, now, now.Add(time.Minute)); err != nil || ok {
		t.Fatalf("cleanup claimed a live lease: (%t, %v)", ok, err)
	}
	if err := catalog.Update(ctx, artifactID, writer, chartifact.Progress{
		State: chartifact.StateCleanupPending, RowCount: 4, ByteCount: 128,
		LeaseUntil: now.Add(2 * time.Minute), UpdatedAt: now,
	}); err != nil {
		t.Fatalf("mark stopped artifact cleanup-pending: %v", err)
	}
	if err := catalog.Update(ctx, artifactID, writer, chartifact.Progress{
		State: chartifact.StateWriting, RowCount: 5, ByteCount: 160,
		LeaseUntil: now.Add(4 * time.Minute), UpdatedAt: now,
	}); !errors.Is(err, chartifact.ErrLeaseLost) {
		t.Fatalf("cleanup-pending artifact returned to active writing: %v", err)
	}
	if err := catalog.Update(ctx, artifactID, writer, chartifact.Progress{
		State: chartifact.StateCleanupPending, RowCount: 4, ByteCount: 128,
		LeaseUntil: now.Add(-time.Second), UpdatedAt: now,
	}); err != nil {
		t.Fatalf("same-state cleanup retry did not make artifact reclaimable: %v", err)
	}

	// Concurrent claimers race on the same expired manifest. The AQL update
	// predicate must permit one owner at most; the loser may observe no row or
	// an Arango write conflict, but must never also own the manifest.
	type claimResult struct {
		manifest chartifact.Manifest
		ok       bool
		err      error
	}
	start := make(chan struct{})
	results := make(chan claimResult, 2)
	var wait sync.WaitGroup
	for _, owner := range []string{cleanupOne, cleanupTwo} {
		owner := owner
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			claimed, ok, err := catalog.ClaimCleanup(ctx, artifactID, owner, time.Now().UTC(), time.Now().UTC().Add(time.Minute))
			results <- claimResult{manifest: claimed, ok: ok, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	var winner string
	wins := 0
	for result := range results {
		if result.err != nil {
			if shared.IsArangoErrorWithErrorNum(result.err, shared.ErrArangoConflict) {
				// A stale revision is a valid losing outcome for the conditional write.
				continue
			}
			t.Fatalf("cleanup claim failed outside expected revision conflict: %v", result.err)
		}
		if result.ok {
			wins++
			winner = result.manifest.LeaseOwner
		}
	}
	if wins != 1 || (winner != cleanupOne && winner != cleanupTwo) {
		t.Fatalf("concurrent cleanup claims won %d times with owner %q", wins, winner)
	}
	if err := catalog.Delete(ctx, artifactID, writer); !errors.Is(err, chartifact.ErrLeaseLost) {
		t.Fatalf("former writer deleted cleanup-owned manifest: %v", err)
	}
	if err := catalog.ReleaseCleanup(ctx, artifactID, writer, now); !errors.Is(err, chartifact.ErrLeaseLost) {
		t.Fatalf("former writer released cleanup lease: %v", err)
	}
	if err := catalog.Delete(ctx, artifactID, winner); err != nil {
		t.Fatalf("cleanup owner could not delete manifest: %v", err)
	}
	if renewed, err := catalog.Renew(ctx, artifactID, writer, now.Add(time.Minute)); err != nil || renewed {
		t.Fatalf("deleted/expired owner renewed: (%t, %v)", renewed, err)
	}
}

func TestCatalogRenewalAndExpiredCleanupClaimHaveOneWinnerAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client, err := arangostore.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		_ = client.Close(closeCtx)
	}()
	if err := client.Bootstrap(ctx, BootstrapSpec()); err != nil {
		t.Fatal(err)
	}
	catalog, err := NewCatalog(client)
	if err != nil {
		t.Fatal(err)
	}

	for race := 0; race < 20; race++ {
		artifactID := "catalog-renew-claim-race-" + uuid.NewString()
		writer := "writer-" + uuid.NewString()
		cleanupOwner := "cleanup-" + uuid.NewString()
		now := time.Now().UTC()
		manifest := sampleManifest()
		manifest.ArtifactID = artifactID
		manifest.Identity.ExecutionID = "execution-" + uuid.NewString()
		manifest.LeaseOwner = writer
		manifest.LeaseUntil = now.Add(time.Minute)
		manifest.CreatedAt, manifest.UpdatedAt = now, now
		if err := catalog.Create(ctx, manifest); err != nil {
			t.Fatalf("race %d create: %v", race, err)
		}
		defer func(id string) {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cleanupCancel()
			owner := "cleanup-final-" + uuid.NewString()
			if _, ok, err := catalog.ClaimCleanup(cleanupCtx, id, owner, time.Now().UTC().Add(time.Hour), time.Now().UTC().Add(2*time.Hour)); err == nil && ok {
				_ = catalog.Delete(cleanupCtx, id, owner)
			}
		}(artifactID)

		type result struct {
			renewed bool
			claimed chartifact.Manifest
			claimOK bool
			err     error
		}
		start := make(chan struct{})
		results := make(chan result, 2)
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			renewed, err := catalog.Renew(ctx, artifactID, writer, now.Add(5*time.Minute))
			results <- result{renewed: renewed, err: err}
		}()
		go func() {
			defer wait.Done()
			<-start
			claimed, ok, err := catalog.ClaimCleanup(ctx, artifactID, cleanupOwner, now.Add(2*time.Minute), now.Add(6*time.Minute))
			results <- result{claimed: claimed, claimOK: ok, err: err}
		}()
		close(start)
		wait.Wait()
		close(results)

		var renewed, claimed bool
		var cleanupManifest chartifact.Manifest
		for outcome := range results {
			if outcome.err != nil {
				if shared.IsArangoErrorWithErrorNum(outcome.err, shared.ErrArangoConflict) {
					continue
				}
				t.Fatalf("race %d returned unexpected AQL error: %v", race, outcome.err)
			}
			if outcome.renewed {
				renewed = true
			}
			if outcome.claimOK {
				claimed = true
				cleanupManifest = outcome.claimed
			}
		}
		if renewed == claimed {
			t.Fatalf("race %d should have exactly one winner, renewed=%t claimed=%t", race, renewed, claimed)
		}
		if renewed {
			if err := catalog.Update(ctx, artifactID, writer, chartifact.Progress{
				State: chartifact.StateCleanupPending, RowCount: manifest.RowCount, ByteCount: manifest.ByteCount,
				LeaseUntil: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("race %d mark renewed artifact for cleanup: %v", race, err)
			}
			if err := catalog.Delete(ctx, artifactID, writer); err != nil {
				t.Fatalf("race %d renewed owner lost cleanup identity: %v", race, err)
			}
		} else {
			if cleanupManifest.LeaseOwner != cleanupOwner || cleanupManifest.State != chartifact.StateCleanupPending {
				t.Fatalf("race %d cleanup claim returned wrong owner/state: %#v", race, cleanupManifest)
			}
			if err := catalog.Delete(ctx, artifactID, cleanupOwner); err != nil {
				t.Fatalf("race %d claimed owner lost cleanup identity: %v", race, err)
			}
		}

		// A cleanup-owner removal racing a reclaim must either remove the row
		// before transfer or lose the revision/owner check after transfer.
		deleteRaceNow := time.Now().UTC()
		manifest.State = chartifact.StateCleanupPending
		manifest.LeaseOwner = writer
		manifest.LeaseUntil = deleteRaceNow.Add(time.Minute)
		manifest.UpdatedAt = deleteRaceNow
		if err := catalog.Create(ctx, manifest); err != nil {
			t.Fatalf("race %d create removal race: %v", race, err)
		}
		deleteStart := make(chan struct{})
		deleteResults := make(chan error, 1)
		claimResults := make(chan result, 1)
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-deleteStart
			deleteResults <- catalog.Delete(ctx, artifactID, writer)
		}()
		go func() {
			defer wait.Done()
			<-deleteStart
			claimed, ok, err := catalog.ClaimCleanup(ctx, artifactID, cleanupOwner, deleteRaceNow.Add(2*time.Minute), deleteRaceNow.Add(3*time.Minute))
			claimResults <- result{claimed: claimed, claimOK: ok, err: err}
		}()
		close(deleteStart)
		wait.Wait()
		deleteErr := <-deleteResults
		claimOutcome := <-claimResults
		if claimOutcome.err != nil && !shared.IsArangoErrorWithErrorNum(claimOutcome.err, shared.ErrArangoConflict) {
			t.Fatalf("race %d cleanup reclaim returned unexpected error: %v", race, claimOutcome.err)
		}
		if deleteErr != nil && !errors.Is(deleteErr, chartifact.ErrLeaseLost) && !shared.IsArangoErrorWithErrorNum(deleteErr, shared.ErrArangoConflict) {
			t.Fatalf("race %d cleanup removal returned unexpected error: %v", race, deleteErr)
		}
		deleteWon := deleteErr == nil
		if deleteWon == claimOutcome.claimOK {
			t.Fatalf("race %d removal and reclaim must have exactly one winner: delete=%v claim=%t", race, deleteErr, claimOutcome.claimOK)
		}
		if claimOutcome.claimOK {
			if claimOutcome.claimed.LeaseOwner != cleanupOwner || claimOutcome.claimed.State != chartifact.StateCleanupPending {
				t.Fatalf("race %d reclaim returned wrong owner/state: %#v", race, claimOutcome.claimed)
			}
			if err := catalog.Delete(ctx, artifactID, cleanupOwner); err != nil {
				t.Fatalf("race %d reclaimed owner could not delete manifest: %v", race, err)
			}
		}
	}
}

func TestCatalogLeaseTimestampsOrderWholeAndFractionalSecondsAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := arangostore.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		_ = client.Close(closeCtx)
	}()
	if err := client.Bootstrap(ctx, BootstrapSpec()); err != nil {
		t.Fatal(err)
	}
	catalog, err := NewCatalog(client)
	if err != nil {
		t.Fatal(err)
	}

	whole := time.Now().UTC().Truncate(time.Second).Add(-time.Second)
	fractional := whole.Add(500 * time.Millisecond)
	wholeID, fractionalID := "catalog-whole-"+uuid.NewString(), "catalog-fractional-"+uuid.NewString()
	owner := "writer-" + uuid.NewString()
	for _, item := range []struct {
		id  string
		end time.Time
	}{{wholeID, whole}, {fractionalID, fractional}} {
		manifest := sampleManifest()
		manifest.ArtifactID = item.id
		manifest.Identity.ExecutionID = "execution-" + uuid.NewString()
		manifest.LeaseOwner = owner
		manifest.LeaseUntil = item.end
		manifest.CreatedAt, manifest.UpdatedAt = whole, whole
		if err := catalog.Create(ctx, manifest); err != nil {
			t.Fatalf("create timestamp fixture %q: %v", item.id, err)
		}
		defer func(id string) {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cleanupCancel()
			cleanupOwner := "cleanup-final-" + uuid.NewString()
			if _, ok, err := catalog.ClaimCleanup(cleanupCtx, id, cleanupOwner, time.Now().UTC().Add(time.Hour), time.Now().UTC().Add(2*time.Hour)); err == nil && ok {
				_ = catalog.Delete(cleanupCtx, id, cleanupOwner)
			}
		}(item.id)
	}

	beforeFractional, err := catalog.ListExpired(ctx, whole.Add(250*time.Millisecond), maxExpiredPageSize)
	if err != nil {
		t.Fatal(err)
	}
	if !containsArtifact(beforeFractional, wholeID) || containsArtifact(beforeFractional, fractionalID) {
		t.Fatalf("fractional lease crossed exact-second cutoff: whole=%t fractional=%t", containsArtifact(beforeFractional, wholeID), containsArtifact(beforeFractional, fractionalID))
	}
	bothExpired, err := catalog.ListExpired(ctx, fractional.Add(250*time.Millisecond), maxExpiredPageSize)
	if err != nil {
		t.Fatal(err)
	}
	wholeIndex, fractionalIndex := artifactIndex(bothExpired, wholeID), artifactIndex(bothExpired, fractionalID)
	if wholeIndex < 0 || fractionalIndex < 0 || wholeIndex >= fractionalIndex {
		t.Fatalf("expired lease ordering is not chronological: whole index=%d fractional index=%d", wholeIndex, fractionalIndex)
	}
}

func containsArtifact(manifests []chartifact.Manifest, artifactID string) bool {
	return artifactIndex(manifests, artifactID) >= 0
}

func artifactIndex(manifests []chartifact.Manifest, artifactID string) int {
	for index, manifest := range manifests {
		if manifest.ArtifactID == artifactID {
			return index
		}
	}
	return -1
}
