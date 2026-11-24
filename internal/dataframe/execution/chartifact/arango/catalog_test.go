package arango

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	arangostore "github.com/calypr/loom/internal/store/arango"
	clickhousestore "github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
)

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

var _ chartifact.ClickHouse = (*clickhousestore.Client)(nil)

type queryCall struct {
	query string
	limit int
	vars  map[string]any
}

type scriptedQueryer struct {
	calls []queryCall
	rows  []map[string]any
	err   error
}

func (q *scriptedQueryer) QueryRows(ctx context.Context, query string, batchSize int, vars map[string]interface{}, visit arangostore.RowVisitor) error {
	q.calls = append(q.calls, queryCall{query: query, limit: batchSize, vars: vars})
	if err := ctx.Err(); err != nil {
		return err
	}
	if q.err != nil {
		return q.err
	}
	for _, row := range q.rows {
		if err := visit(row); err != nil {
			return err
		}
	}
	return nil
}

func TestBootstrapAndCreateKeepPrivateManifestInsertOnly(t *testing.T) {
	if _, err := NewCatalog(nil); err == nil {
		t.Fatal("NewCatalog accepted a nil Arango client")
	}
	bootstrap := BootstrapSpec()
	if len(bootstrap.Collections) != 1 {
		t.Fatalf("bootstrap collections = %#v", bootstrap.Collections)
	}
	collection := bootstrap.Collections[0]
	if collection.Name != PrivateArtifactsCollection || collection.Truncate || collection.Edge || len(collection.Indexes) != 1 || strings.Join(collection.Indexes[0], ",") != "leaseUntil" {
		t.Fatalf("private artifact bootstrap spec = %#v", collection)
	}

	queryer := &scriptedQueryer{rows: []map[string]any{{"created": true}}}
	catalog, err := NewCatalog(queryer)
	if err != nil {
		t.Fatal(err)
	}
	manifest := sampleManifest()
	if err := catalog.Create(context.Background(), manifest); err != nil {
		t.Fatal(err)
	}
	call := queryer.calls[0]
	if !strings.Contains(call.query, "FILTER existing == null\nINSERT @document") || strings.Contains(call.query, "UPDATE") || strings.Contains(call.query, "UPSERT") {
		t.Fatalf("Create is not insert-only: %s", call.query)
	}
	if call.vars["@collection"] != PrivateArtifactsCollection || call.vars["key"] != manifest.ArtifactID {
		t.Fatalf("Create binding mismatch: %#v", call.vars)
	}
	document, ok := call.vars["document"].(map[string]any)
	if !ok || document["_key"] != manifest.ArtifactID || document["artifactId"] != manifest.ArtifactID || document["physicalTable"] != manifest.PhysicalTable {
		t.Fatalf("Create document lost identity: %#v", call.vars["document"])
	}
	if _, hasVisibilityPointer := document["publishedPointer"]; hasVisibilityPointer {
		t.Fatalf("private manifest contains a publication pointer: %#v", document)
	}

	queryer.rows = nil
	if err := catalog.Create(context.Background(), manifest); err == nil {
		t.Fatal("Create reported success when the ID was already present")
	}
}

func TestManifestLeaseTimestampEncodingOrdersWholeAndFractionalSeconds(t *testing.T) {
	whole := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	fractional := whole.Add(500 * time.Millisecond)
	wholeEncoded, fractionalEncoded := formatManifestTime(whole), formatManifestTime(fractional)
	if !(wholeEncoded < fractionalEncoded) {
		t.Fatalf("fixed-width timestamps sort out of time order: %q >= %q", wholeEncoded, fractionalEncoded)
	}
	if wholeEncoded != "2026-10-04T12:00:00.000000000Z" || fractionalEncoded != "2026-10-04T12:00:00.500000000Z" {
		t.Fatalf("manifest timestamp format changed: whole=%q fractional=%q", wholeEncoded, fractionalEncoded)
	}
	parsed, err := time.Parse(time.RFC3339Nano, fractionalEncoded)
	if err != nil || !parsed.Equal(fractional) {
		t.Fatalf("fixed-width timestamp does not round-trip: %v %v", parsed, err)
	}
	manifest := sampleManifest()
	manifest.LeaseUntil, manifest.CreatedAt, manifest.UpdatedAt = whole, fractional, whole
	document, err := manifestDocument(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if document["leaseUntil"] != wholeEncoded || document["createdAt"] != fractionalEncoded {
		t.Fatalf("manifest timestamps are not stored in fixed-width form: %#v", document)
	}
}

func TestLeaseAndCleanupWritesRecheckOwnerAndExpiryAtomically(t *testing.T) {
	catalog, queryer := newCatalog(t)
	now := time.Now().UTC()

	if err := catalog.Update(context.Background(), "artifact-1", "writer-1", chartifact.Progress{
		State: chartifact.StateWriting, RowCount: 2, ByteCount: 18, LeaseUntil: now.Add(time.Minute), UpdatedAt: now,
	}); !errors.Is(err, chartifact.ErrLeaseLost) {
		t.Fatalf("missing-row Update = %v, want ErrLeaseLost", err)
	}
	call := lastCall(t, queryer)
	requireQuery(t, call.query, "existing.leaseOwner == @owner", "existing.leaseUntil >= @now", "existing.state != @cleanupPendingState OR @state == @cleanupPendingState", "MAX([existing.leaseUntil, @leaseUntil])", "OPTIONS { ignoreRevs: false }")
	for _, immutable := range []string{"identity:", "columns:", "physicalTable:", "artifactId:"} {
		if strings.Contains(call.query, immutable) {
			t.Errorf("Update mutates immutable manifest field %q: %s", immutable, call.query)
		}
	}
	if call.vars["cleanupPending"] != false || call.vars["cleanupPendingState"] != chartifact.StateCleanupPending {
		t.Fatalf("active update cleanup bindings = %#v", call.vars)
	}
	requireFixedManifestTime(t, call.vars["now"])
	requireFixedManifestTime(t, call.vars["leaseUntil"])
	if call.vars["leaseUntil"] != formatManifestTime(now.Add(time.Minute)) {
		t.Fatalf("Update lease binding is not fixed-width: %#v", call.vars["leaseUntil"])
	}

	renewed, err := catalog.Renew(context.Background(), "artifact-1", "writer-1", now.Add(2*time.Minute))
	if err != nil || renewed {
		t.Fatalf("missing-row Renew = (%t, %v)", renewed, err)
	}
	call = lastCall(t, queryer)
	requireQuery(t, call.query, "existing.leaseOwner == @owner", "existing.state IN @renewableStates", "existing.leaseUntil >= @now", "MAX([existing.leaseUntil, @leaseUntil])", "OPTIONS { ignoreRevs: false }")
	requireFixedManifestTime(t, call.vars["now"])
	if call.vars["leaseUntil"] != formatManifestTime(now.Add(2*time.Minute)) {
		t.Fatalf("renewal lease is not fixed-width UTC: %#v", call.vars["leaseUntil"])
	}

	candidate := sampleManifest()
	candidate.State = chartifact.StateCleanupPending
	candidate.LeaseOwner = "cleanup-1"
	candidate.LeaseUntil = now.Add(time.Minute)
	document, err := manifestDocument(candidate)
	if err != nil {
		t.Fatal(err)
	}
	queryer.rows = []map[string]any{{"manifest": document}}
	claimed, ok, err := catalog.ClaimCleanup(context.Background(), candidate.ArtifactID, "cleanup-1", now, now.Add(time.Minute))
	if err != nil || !ok {
		t.Fatalf("ClaimCleanup = (%#v, %t, %v)", claimed, ok, err)
	}
	if !reflect.DeepEqual(claimed.Identity, candidate.Identity) || claimed.ArtifactID != candidate.ArtifactID || claimed.LeaseOwner != "cleanup-1" || claimed.State != chartifact.StateCleanupPending {
		t.Fatalf("ClaimCleanup changed identity or failed to acquire lease: %#v", claimed)
	}
	call = lastCall(t, queryer)
	requireQuery(t, call.query, "existing.leaseUntil <= @expiredBefore", "leaseOwner: @owner", "state: @cleanupPending", "OPTIONS { ignoreRevs: false }")
	if strings.Contains(call.query, "UPSERT") {
		t.Fatalf("ClaimCleanup can create a manifest: %s", call.query)
	}

	queryer.rows = nil
	_, ok, err = catalog.ClaimCleanup(context.Background(), candidate.ArtifactID, "cleanup-2", now, now.Add(time.Minute))
	if err != nil || ok {
		t.Fatalf("stale ClaimCleanup = (%t, %v), want no claim", ok, err)
	}

	if err := catalog.ReleaseCleanup(context.Background(), candidate.ArtifactID, "cleanup-1", now); !errors.Is(err, chartifact.ErrLeaseLost) {
		t.Fatalf("missing-owner ReleaseCleanup = %v, want ErrLeaseLost", err)
	}
	call = lastCall(t, queryer)
	requireQuery(t, call.query, "existing.leaseOwner == @owner", "existing.state == @cleanupPending", "OPTIONS { ignoreRevs: false }")

	if err := catalog.Delete(context.Background(), candidate.ArtifactID, "cleanup-1"); !errors.Is(err, chartifact.ErrLeaseLost) {
		t.Fatalf("missing-owner Delete = %v, want ErrLeaseLost", err)
	}
	call = lastCall(t, queryer)
	requireQuery(t, call.query, "existing.leaseOwner == @owner", "existing.state == @cleanupPending", "REMOVE existing", "OPTIONS { ignoreRevs: false }")
}

func TestUpdateCleanupPendingIsTerminalButSameStateRetryCanShortenLease(t *testing.T) {
	catalog, queryer := newCatalog(t)
	now := time.Now().UTC()
	queryer.rows = []map[string]any{{"updated": true}}
	if err := catalog.Update(context.Background(), "artifact-1", "writer-1", chartifact.Progress{
		State: chartifact.StateCleanupPending, RowCount: 0, ByteCount: 0, LeaseUntil: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("same-state cleanup retry = %v", err)
	}
	call := lastCall(t, queryer)
	if call.vars["cleanupPending"] != true || call.vars["cleanupPendingState"] != chartifact.StateCleanupPending || call.vars["state"] != chartifact.StateCleanupPending || !strings.Contains(call.query, "@cleanupPending ? @leaseUntil : MAX") || !strings.Contains(call.query, "existing.state != @cleanupPendingState OR @state == @cleanupPendingState") {
		t.Fatalf("cleanup retry does not allow immediate reclaim or preserve terminal state: query=%s vars=%#v", call.query, call.vars)
	}

	queryer.rows = nil
	if err := catalog.Update(context.Background(), "artifact-1", "writer-1", chartifact.Progress{
		State: chartifact.StateWriting, RowCount: 1, ByteCount: 1, LeaseUntil: now.Add(time.Minute), UpdatedAt: now,
	}); !errors.Is(err, chartifact.ErrLeaseLost) {
		t.Fatalf("active-state transition from cleanup-pending = %v, want ErrLeaseLost", err)
	}
	call = lastCall(t, queryer)
	if call.vars["cleanupPending"] != false || call.vars["cleanupPendingState"] != chartifact.StateCleanupPending || call.vars["state"] != chartifact.StateWriting || !strings.Contains(call.query, "existing.state != @cleanupPendingState OR @state == @cleanupPendingState") {
		t.Fatalf("active-state update does not reject terminal cleanup state: query=%s vars=%#v", call.query, call.vars)
	}
}

func TestListExpiredIsBoundedStableAndDecodesManifest(t *testing.T) {
	catalog, queryer := newCatalog(t)
	manifest := sampleManifest()
	document, err := manifestDocument(manifest)
	if err != nil {
		t.Fatal(err)
	}
	queryer.rows = []map[string]any{document}
	got, err := catalog.ListExpired(context.Background(), time.Now().UTC(), maxExpiredPageSize+7)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ArtifactID != manifest.ArtifactID || got[0].PhysicalTable != manifest.PhysicalTable || got[0].Identity.ExecutionID != manifest.Identity.ExecutionID {
		t.Fatalf("decoded expired manifests = %#v", got)
	}
	call := lastCall(t, queryer)
	requireQuery(t, call.query, "FILTER manifest.leaseUntil <= @expiredBefore", "SORT manifest.leaseUntil ASC, manifest._key ASC", "LIMIT @limit")
	if call.vars["limit"] != maxExpiredPageSize {
		t.Fatalf("expired page limit = %#v, want %d", call.vars["limit"], maxExpiredPageSize)
	}
	if _, ok := call.vars["expiredBefore"].(string); !ok {
		t.Fatalf("expiry cutoff binding is not a fixed-width UTC string: %#v", call.vars["expiredBefore"])
	}

	queryer.calls = nil
	queryer.rows = nil
	got, err = catalog.ListExpired(context.Background(), time.Now().UTC(), 0)
	if err != nil || len(got) != 0 || len(queryer.calls) != 0 {
		t.Fatalf("zero-limit ListExpired = (%#v, %v), queries=%d", got, err, len(queryer.calls))
	}
}

func TestCatalogPropagatesCancellationBeforeIssuingAQL(t *testing.T) {
	catalog, queryer := newCatalog(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := catalog.Create(ctx, sampleManifest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Create cancellation = %v, want context.Canceled", err)
	}
	if len(queryer.calls) != 0 {
		t.Fatalf("canceled request issued %d AQL queries", len(queryer.calls))
	}
}

func newCatalog(t *testing.T) (*Catalog, *scriptedQueryer) {
	t.Helper()
	queryer := &scriptedQueryer{}
	catalog, err := NewCatalog(queryer)
	if err != nil {
		t.Fatal(err)
	}
	return catalog, queryer
}

func lastCall(t *testing.T, queryer *scriptedQueryer) queryCall {
	t.Helper()
	if len(queryer.calls) == 0 {
		t.Fatal("no AQL call recorded")
	}
	return queryer.calls[len(queryer.calls)-1]
}

func requireFixedManifestTime(t *testing.T, value any) {
	t.Helper()
	encoded, ok := value.(string)
	if !ok {
		t.Fatalf("timestamp binding is not a string: %#v", value)
	}
	parsed, err := time.Parse(time.RFC3339Nano, encoded)
	if err != nil || formatManifestTime(parsed) != encoded {
		t.Fatalf("timestamp binding is not fixed-width UTC: %q (%v)", encoded, err)
	}
}

func requireQuery(t *testing.T, query string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(query, part) {
			t.Errorf("AQL does not contain %q:\n%s", part, query)
		}
	}
}

func sampleManifest() chartifact.Manifest {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	return chartifact.Manifest{
		ArtifactID:    "artifact-1",
		Identity:      chartifact.Identity{ExecutionID: "execution-1", OutputID: "output-1", StageID: "stage-1", Project: "project-1", DatasetGeneration: "generation-1", RecipeDigest: "recipe-digest", PlanDigest: "plan-digest", AuthScopeMode: authscope.ReadScopeUnrestricted},
		Columns:       []chartifact.Column{{ID: "column-1", Name: "value", LogicalType: "string", ClickHouseType: "String"}},
		PhysicalTable: "loom_private_artifact1",
		State:         chartifact.StateCreating,
		LeaseOwner:    "writer-1",
		LeaseUntil:    now.Add(time.Minute),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func TestDecodeManifestPreservesEncodeAndDecodeErrorLabels(t *testing.T) {
	if _, err := decodeManifest(map[string]any{"unsupported": make(chan int)}); err == nil || !strings.Contains(err.Error(), "encode stored private artifact manifest") {
		t.Fatalf("encode error = %v, want encode label", err)
	}
	if _, err := decodeManifest(map[string]any{"artifactId": 42}); err == nil || !strings.Contains(err.Error(), "decode stored private artifact manifest") {
		t.Fatalf("decode error = %v, want decode label", err)
	}
}
