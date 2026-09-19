package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/catalog"
	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	publication "github.com/calypr/loom/internal/dataset"
)

func TestBackfillSemanticInventoryResumesPartialPageWithoutDuplicates(t *testing.T) {
	backend := newBackfillFake()
	backend.rows["Observation"] = []catalogarango.RetainedSemanticInventoryRow{
		backfillSourceRow(t, "a", "scope-a", backfillObservation("shared", "v1", false)),
		backfillSourceRow(t, "b", "scope-b", backfillObservation("shared", "v1", false)),
		backfillSourceRow(t, "c", "scope-a", backfillObservation("third", "v1", true)),
	}
	original := cloneBackfillRows(backend.rows["Observation"])
	backend.failWriteAt = 4 // First event for source c persists, its next event is interrupted.
	options := SemanticInventoryBackfillOptions{Project: "project", DatasetGeneration: "generation", PageSize: 2, BatchSize: 1}
	manifest := backfillManifest(t, publication.StateStaged)

	if _, err := BackfillSemanticInventory(context.Background(), backend, manifest, options); err == nil {
		t.Fatal("interrupted backfill error = nil, want persistence failure")
	}
	if got, want := backend.build.State, catalog.SemanticInventoryFailed; got != want {
		t.Fatalf("failed build state = %q, want %q", got, want)
	}
	if got, want := backend.build.ScannedResources, int64(2); got != want {
		t.Fatalf("failure reset durable scanned count to %d, want %d", got, want)
	}
	if got, want := backend.build.SourceCheckpoint, (catalog.SemanticInventoryCheckpoint{Collection: "Observation", Key: "b"}); got != want {
		t.Fatalf("failure checkpoint = %+v, want %+v", got, want)
	}
	partialCount := len(backend.contributions)
	if partialCount != 3 {
		t.Fatalf("partially persisted contribution keys = %d, want three deterministic rows", partialCount)
	}

	backend.failWriteAt = 0
	report, err := BackfillSemanticInventory(context.Background(), backend, manifest, options)
	if err != nil {
		t.Fatalf("resume backfill: %v", err)
	}
	if report.State != catalog.SemanticInventoryComplete || report.ScannedThisRun != 1 || report.ScannedTotal != 3 {
		t.Fatalf("resume report = %+v, want complete with one newly scanned and three total rows", report)
	}
	if got := len(backend.contributions); got != 4 {
		t.Fatalf("contribution keys after replay = %d, want four unique source events", got)
	}
	if got := backend.build.ScannedResources; got != 3 {
		t.Fatalf("durable scanned count after resume = %d, want 3", got)
	}
	if !reflect.DeepEqual(backend.rows["Observation"], original) {
		t.Fatal("backfill mutated retained source documents")
	}
	assertBackfillContributionScopes(t, backend.contributions)

	writesBeforeNoOp := backend.writeCalls
	second, err := BackfillSemanticInventory(context.Background(), backend, manifest, options)
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if !second.NoOp || second.State != catalog.SemanticInventoryComplete || backend.writeCalls != writesBeforeNoOp {
		t.Fatalf("second run = %+v, writes %d -> %d, want completed no-op", second, writesBeforeNoOp, backend.writeCalls)
	}
}

func TestBackfillSemanticInventoryRejectsConcurrentClaim(t *testing.T) {
	backend := newBackfillFake()
	backend.claimedByOther = true
	_, err := BackfillSemanticInventory(context.Background(), backend, backfillManifest(t, publication.StateStaged), SemanticInventoryBackfillOptions{
		Project: "project", DatasetGeneration: "generation",
	})
	if !errors.Is(err, catalogarango.ErrSemanticInventoryClaimed) {
		t.Fatalf("concurrent claim error = %v, want ErrSemanticInventoryClaimed", err)
	}
	if backend.readCalls != 0 || len(backend.contributions) != 0 {
		t.Fatalf("scanner did work after claim rejection: reads=%d contributions=%d", backend.readCalls, len(backend.contributions))
	}
}

func TestBackfillSemanticInventoryMalformedPayloadFailsAndPreservesSource(t *testing.T) {
	backend := newBackfillFake()
	backend.rows["Observation"] = []catalogarango.RetainedSemanticInventoryRow{{Key: "bad", AuthResourcePath: strptr("scope-a"), Payload: json.RawMessage(`[]`)}}
	original := cloneBackfillRows(backend.rows["Observation"])
	_, err := BackfillSemanticInventory(context.Background(), backend, backfillManifest(t, publication.StateReady), SemanticInventoryBackfillOptions{
		Project: "project", DatasetGeneration: "generation",
	})
	if err == nil {
		t.Fatal("malformed payload backfill error = nil")
	}
	if backend.build.State != catalog.SemanticInventoryFailed {
		t.Fatalf("malformed payload build state = %q, want failed", backend.build.State)
	}
	if backend.build.State == catalog.SemanticInventoryComplete {
		t.Fatal("malformed source was marked complete")
	}
	if !reflect.DeepEqual(backend.rows["Observation"], original) {
		t.Fatal("malformed payload failure mutated retained source")
	}
}

func TestBackfillSemanticInventoryRequiresMatchingImmutableManifest(t *testing.T) {
	for _, state := range []publication.State{publication.StateLoading, publication.StateFailed} {
		backend := newBackfillFake()
		_, err := BackfillSemanticInventory(context.Background(), backend, backfillManifest(t, state), SemanticInventoryBackfillOptions{
			Project: "project", DatasetGeneration: "generation",
		})
		if err == nil {
			t.Fatalf("manifest state %s accepted", state)
		}
		if backend.prepared || backend.readCalls != 0 {
			t.Fatalf("manifest state %s caused storage/source work", state)
		}
	}
	backend := newBackfillFake()
	manifest := backfillManifest(t, publication.StateStaged)
	manifest.Dataset.Generation = "other"
	if _, err := BackfillSemanticInventory(context.Background(), backend, manifest, SemanticInventoryBackfillOptions{
		Project: "project", DatasetGeneration: "generation",
	}); err == nil {
		t.Fatal("mismatched manifest identity accepted")
	}
}

func TestBackfillSemanticInventoryMarksMissingUnprofiledRootUnproven(t *testing.T) {
	backend := newBackfillFake()
	backend.rows["Observation"] = []catalogarango.RetainedSemanticInventoryRow{backfillSourceRow(t, "a", "", backfillObservation("one", "v1", false))}
	backend.missingCollections["Patient"] = true
	manifest := backfillManifestWithRoots(t, publication.StateStaged, []string{"Observation", "Patient"})
	report, err := BackfillSemanticInventory(context.Background(), backend, manifest, SemanticInventoryBackfillOptions{
		Project: "project", DatasetGeneration: "generation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.State != catalog.SemanticInventoryComplete || report.SourceAvailability != catalog.SemanticInventorySourceAvailabilityUnproven {
		t.Fatalf("missing-root report = %+v, want completed scan with explicit unproven availability", report)
	}
	if len(report.MissingCollections) != 1 || report.MissingCollections[0] != "Patient" || backend.build.SourceAvailability != catalog.SemanticInventorySourceAvailabilityUnproven {
		t.Fatalf("missing-root evidence report=%v durable=%q", report.MissingCollections, backend.build.SourceAvailability)
	}
}

func TestBackfillSemanticInventoryCancellationReleasesClaimForImmediateResume(t *testing.T) {
	backend := newBackfillFake()
	backend.rows["Observation"] = []catalogarango.RetainedSemanticInventoryRow{
		backfillSourceRow(t, "a", "", backfillObservation("one", "v1", false)),
		backfillSourceRow(t, "b", "", backfillObservation("two", "v1", false)),
	}
	manifest := backfillManifest(t, publication.StateStaged)
	ctx, cancel := context.WithCancel(context.Background())
	_, err := BackfillSemanticInventory(ctx, backend, manifest, SemanticInventoryBackfillOptions{
		Project: "project", DatasetGeneration: "generation", PageSize: 1,
		Progress: func(SemanticInventoryBackfillProgress) { cancel() },
	})
	cancel()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled backfill error = %v, want context canceled", err)
	}
	if backend.build.State != catalog.SemanticInventoryFailed || backend.build.LeaseToken != "" {
		t.Fatalf("cancelled run left build/claim as %+v", backend.build)
	}
	resumed, err := BackfillSemanticInventory(context.Background(), backend, manifest, SemanticInventoryBackfillOptions{
		Project: "project", DatasetGeneration: "generation", PageSize: 1,
	})
	if err != nil || resumed.State != catalog.SemanticInventoryComplete || resumed.ScannedTotal != 2 {
		t.Fatalf("immediate post-cancel resume = %+v err=%v", resumed, err)
	}
}

func assertBackfillContributionScopes(t *testing.T, contributions map[string]catalog.SemanticInventoryContribution) {
	t.Helper()
	scopes := map[string]map[string]bool{}
	for _, contribution := range contributions {
		if contribution.SourceKind != catalog.SemanticInventorySourceRetained {
			t.Fatalf("contribution source kind = %q, want retained vertex", contribution.SourceKind)
		}
		if scopes[contribution.SourceID] == nil {
			scopes[contribution.SourceID] = map[string]bool{}
		}
		scopes[contribution.SourceID][contribution.AuthResourcePath] = true
	}
	want := map[string]string{"retained:Observation/a": "scope-a", "retained:Observation/b": "scope-b", "retained:Observation/c": "scope-a"}
	for sourceID, authPath := range want {
		if len(scopes[sourceID]) != 1 || !scopes[sourceID][authPath] {
			t.Fatalf("source %s authorization paths = %v, want only %q", sourceID, scopes[sourceID], authPath)
		}
	}
}

func backfillObservation(code, version string, withComponent bool) map[string]any {
	payload := map[string]any{
		"resourceType":  "Observation",
		"code":          map[string]any{"coding": []any{map[string]any{"system": "urn:test", "version": version, "code": code}}},
		"valueQuantity": map[string]any{"value": 1.0, "unit": "mg"},
	}
	if withComponent {
		payload["component"] = []any{map[string]any{
			"code":          map[string]any{"coding": []any{map[string]any{"system": "urn:test", "version": version, "code": code + "-component"}}},
			"valueQuantity": map[string]any{"value": 2.0, "unit": "mg"},
		}}
	}
	return payload
}

func backfillSourceRow(t *testing.T, key, authPath string, payload map[string]any) catalogarango.RetainedSemanticInventoryRow {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return catalogarango.RetainedSemanticInventoryRow{Key: key, AuthResourcePath: strptr(authPath), Payload: encoded}
}

func backfillManifest(t *testing.T, state publication.State) publication.Manifest {
	return backfillManifestWithRoots(t, state, []string{"Observation"})
}

func backfillManifestWithRoots(t *testing.T, state publication.State, resourceTypes []string) publication.Manifest {
	t.Helper()
	ref, err := publication.NewRef("project", "generation")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := publication.NewSchemaSnapshot("schema", "4.0.1", strings.Repeat("a", 64), resourceTypes)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := publication.NewManifest(ref, schema)
	if err != nil {
		t.Fatal(err)
	}
	manifest.State = state
	return manifest
}

func cloneBackfillRows(rows []catalogarango.RetainedSemanticInventoryRow) []catalogarango.RetainedSemanticInventoryRow {
	cloned := make([]catalogarango.RetainedSemanticInventoryRow, len(rows))
	for index, row := range rows {
		cloned[index] = row
		cloned[index].Payload = append(json.RawMessage(nil), row.Payload...)
		if row.AuthResourcePath != nil {
			cloned[index].AuthResourcePath = strptr(*row.AuthResourcePath)
		}
	}
	return cloned
}

func strptr(value string) *string { return &value }

type backfillFake struct {
	rows               map[string][]catalogarango.RetainedSemanticInventoryRow
	contributions      map[string]catalog.SemanticInventoryContribution
	build              catalog.SemanticInventoryBuild
	claimedByOther     bool
	prepared           bool
	readCalls          int
	writeCalls         int
	failWriteAt        int
	missingCollections map[string]bool
}

func newBackfillFake() *backfillFake {
	return &backfillFake{rows: map[string][]catalogarango.RetainedSemanticInventoryRow{}, contributions: map[string]catalog.SemanticInventoryContribution{}, missingCollections: map[string]bool{}}
}

func (f *backfillFake) PrepareSemanticInventoryBackfill(context.Context) error {
	f.prepared = true
	return nil
}

func (f *backfillFake) ReadRetainedSemanticInventoryPage(ctx context.Context, project, generation, collection, afterKey string, limit int) (catalogarango.RetainedSemanticInventoryPage, error) {
	if err := ctx.Err(); err != nil {
		return catalogarango.RetainedSemanticInventoryPage{}, err
	}
	f.readCalls++
	if project != "project" || generation != "generation" {
		return catalogarango.RetainedSemanticInventoryPage{}, fmt.Errorf("unexpected source scope %s/%s", project, generation)
	}
	rows := append([]catalogarango.RetainedSemanticInventoryRow(nil), f.rows[collection]...)
	if f.missingCollections[collection] {
		return catalogarango.RetainedSemanticInventoryPage{Rows: []catalogarango.RetainedSemanticInventoryRow{}}, nil
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	page := catalogarango.RetainedSemanticInventoryPage{Rows: make([]catalogarango.RetainedSemanticInventoryRow, 0, limit), SourceExists: true}
	for _, row := range rows {
		if row.Key <= afterKey {
			continue
		}
		if len(page.Rows) == limit {
			page.HasMore = true
			break
		}
		page.Rows = append(page.Rows, row)
	}
	return page, nil
}

func (f *backfillFake) ClaimSemanticInventoryBackfill(_ context.Context, build catalog.SemanticInventoryBuild, token string, lease time.Duration) (catalog.SemanticInventoryBuild, bool, error) {
	if f.claimedByOther {
		return catalog.SemanticInventoryBuild{}, false, catalogarango.ErrSemanticInventoryClaimed
	}
	if f.build.State == catalog.SemanticInventoryComplete {
		return f.build, false, nil
	}
	if f.build.LeaseToken != "" && f.build.LeaseToken != token {
		return catalog.SemanticInventoryBuild{}, false, catalogarango.ErrSemanticInventoryClaimed
	}
	if f.build.Key == "" {
		f.build = build
	}
	if f.build.SourceKind != build.SourceKind {
		f.build.SourceCheckpoint = catalog.SemanticInventoryCheckpoint{}
		f.build.ScannedResources = 0
	}
	f.build.SourceKind = build.SourceKind
	f.build.State = catalog.SemanticInventoryRunning
	f.build.LeaseToken = token
	f.build.LeaseExpiresAt = time.Now().Add(lease).UnixMilli()
	return f.build, true, nil
}

func (f *backfillFake) AdvanceSemanticInventoryBackfill(_ context.Context, build catalog.SemanticInventoryBuild, token string, expected, next catalog.SemanticInventoryCheckpoint, scanned int64, lease time.Duration) (catalog.SemanticInventoryBuild, error) {
	if f.build.LeaseToken != token || f.build.SourceCheckpoint != expected {
		return catalog.SemanticInventoryBuild{}, catalogarango.ErrSemanticInventoryClaimLost
	}
	f.build.SourceCheckpoint = next
	f.build.ScannedResources = scanned
	f.build.SourceAvailability = build.SourceAvailability
	f.build.LeaseExpiresAt = time.Now().Add(lease).UnixMilli()
	return f.build, nil
}

func (f *backfillFake) CompleteSemanticInventoryBackfill(_ context.Context, build catalog.SemanticInventoryBuild, token string, expected catalog.SemanticInventoryCheckpoint, scanned int64) (catalog.SemanticInventoryBuild, error) {
	if f.build.LeaseToken != token || f.build.SourceCheckpoint != expected {
		return catalog.SemanticInventoryBuild{}, catalogarango.ErrSemanticInventoryClaimLost
	}
	f.build.State = catalog.SemanticInventoryComplete
	f.build.ScannedResources = scanned
	f.build.SourceAvailability = build.SourceAvailability
	f.build.EntryIndexVersion = catalog.SemanticInventoryEntryIndexVersion
	f.build.LeaseToken = ""
	f.build.LeaseExpiresAt = 0
	return f.build, nil
}

func (f *backfillFake) FailSemanticInventoryBackfill(ctx context.Context, _ catalog.SemanticInventoryBuild, token, diagnostic string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.build.LeaseToken != token {
		return catalogarango.ErrSemanticInventoryClaimLost
	}
	f.build.State = catalog.SemanticInventoryFailed
	f.build.LeaseToken = ""
	f.build.LeaseExpiresAt = 0
	f.build.Diagnostics = []string{diagnostic}
	return nil
}

func (f *backfillFake) WriteSemanticInventoryContributions(_ context.Context, contributions []catalog.SemanticInventoryContribution, _ int) error {
	f.writeCalls++
	if f.failWriteAt != 0 && f.writeCalls == f.failWriteAt {
		return errors.New("injected interrupted write")
	}
	for _, contribution := range contributions {
		f.contributions[contribution.Key] = contribution
	}
	return nil
}

func (f *backfillFake) EnsureSemanticInventoryEntries(_ context.Context, build catalog.SemanticInventoryBuild) (catalog.SemanticInventoryBuild, error) {
	build.EntryIndexVersion = catalog.SemanticInventoryEntryIndexVersion
	f.build = build
	return build, nil
}
