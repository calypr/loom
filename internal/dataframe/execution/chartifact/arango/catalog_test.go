package arango

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	arangostore "github.com/calypr/loom/internal/store/arango"
	clickhousestore "github.com/calypr/loom/internal/store/clickhouse"
)

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
