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
	if report.CoverageEmissionSourceRows != 1 || report.CoverageCertificationSourceRows != 3 {
		t.Fatalf("resume coverage source rows emitted=%d certified=%d, want 1 emitted and 3 terminally re-enumerated", report.CoverageEmissionSourceRows, report.CoverageCertificationSourceRows)
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
	coverageBeginsBeforeNoOp := backend.coverageBeginCalls
	second, err := BackfillSemanticInventory(context.Background(), backend, manifest, options)
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if !second.NoOp || second.State != catalog.SemanticInventoryComplete || backend.writeCalls != writesBeforeNoOp || backend.coverageBeginCalls != coverageBeginsBeforeNoOp+1 {
		t.Fatalf("second run = %+v, writes %d -> %d coverage begins %d -> %d, want completed no-op with a fresh BUILDING marker", second, writesBeforeNoOp, backend.writeCalls, coverageBeginsBeforeNoOp, backend.coverageBeginCalls)
	}
	if second.CoverageEmissionSourceRows != 0 || second.CoverageCertificationSourceRows != 3 {
		t.Fatalf("no-op coverage source rows emitted=%d certified=%d, want zero emissions and three certificate reads", second.CoverageEmissionSourceRows, second.CoverageCertificationSourceRows)
	}
	if backend.coverageResourceStates["Observation"] != "COMPLETE" || backend.coverageFinalizeCalls != 2 {
		t.Fatalf("no-op coverage reconciliation states=%v finalizers=%d, want exact source certificate", backend.coverageResourceStates, backend.coverageFinalizeCalls)
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

func TestBackfillSemanticCoverageWriteFailurePrecedesCheckpointAndCompletion(t *testing.T) {
	backend := newBackfillFake()
	backend.rows["Observation"] = []catalogarango.RetainedSemanticInventoryRow{
		backfillSourceRow(t, "key-a", "scope-a", backfillObservation("one", "v1", false)),
	}
	backend.failCoverageWriteAt = 1
	manifest := backfillManifest(t, publication.StateStaged)
	_, err := BackfillSemanticInventory(context.Background(), backend, manifest, SemanticInventoryBackfillOptions{
		Project: "project", DatasetGeneration: "generation", PageSize: 1, BatchSize: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "injected coverage write failure") {
		t.Fatalf("coverage persistence error = %v, want injected failure", err)
	}
	if backend.advanceCalls != 0 || backend.completeCalls != 0 {
		t.Fatalf("coverage failure advanced checkpoint %d times or completed inventory %d times", backend.advanceCalls, backend.completeCalls)
	}
	if backend.build.SourceCheckpoint != (catalog.SemanticInventoryCheckpoint{}) || backend.build.ScannedResources != 0 {
		t.Fatalf("failed coverage write advanced durable source state: %+v", backend.build)
	}
	if backend.coverageState != catalogarango.SemanticCoverageBuildFailed || backend.coverageFinalizeCalls != 0 || backend.coverageResourceStates["Observation"] != "FAILED" {
		t.Fatalf("coverage state=%q finalizers=%d resource states=%v, want failed resource before finalization", backend.coverageState, backend.coverageFinalizeCalls, backend.coverageResourceStates)
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
	if backend.coverageState != catalogarango.SemanticCoverageBuildPartial {
		t.Fatalf("coverage state=%q, want PARTIAL while inventory remains complete", backend.coverageState)
	}
}

func TestBackfillSemanticCoverageCertifiesPresentSpecimenAndMarksMissingTask(t *testing.T) {
	backend := newBackfillFake()
	backend.rows["Specimen"] = []catalogarango.RetainedSemanticInventoryRow{
		backfillSourceRow(t, "specimen-key", "scope-specimen", map[string]any{"resourceType": "Specimen", "id": "specimen-1"}),
	}
	backend.missingCollections["Task"] = true
	manifest := backfillManifestWithRoots(t, publication.StateStaged, []string{"Specimen", "Task"})

	report, err := BackfillSemanticInventory(context.Background(), backend, manifest, SemanticInventoryBackfillOptions{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	if backend.coverageResourceStates["Specimen"] != "COMPLETE" || backend.coverageResourceStates["Task"] != "MISSING" {
		t.Fatalf("retained resource certificates = %v, want Specimen COMPLETE and Task MISSING", backend.coverageResourceStates)
	}
	if backend.coverageState != catalogarango.SemanticCoverageBuildPartial || backend.coverageFinalizedResources == nil || !reflect.DeepEqual(backend.coverageFinalizedResources, []string{"Specimen", "Task"}) {
		t.Fatalf("global coverage state=%q finalized=%v, want PARTIAL over exact manifest roots", backend.coverageState, backend.coverageFinalizedResources)
	}
	if report.SourceAvailability != catalog.SemanticInventorySourceAvailabilityUnproven || len(report.MissingCollections) != 1 || report.MissingCollections[0] != "Task" {
		t.Fatalf("missing retained root report = %+v", report)
	}
	if report.CoverageEmissionSourceRows != 1 || report.CoverageCertificationSourceRows != 1 {
		t.Fatalf("retained coverage source rows emitted=%d certified=%d, want only Specimen counted", report.CoverageEmissionSourceRows, report.CoverageCertificationSourceRows)
	}
}

func TestBackfillSemanticCoverageMissingSourceSentinelFailsClosedOnNoOp(t *testing.T) {
	backend := newBackfillFake()
	backend.rows["Observation"] = []catalogarango.RetainedSemanticInventoryRow{
		backfillSourceRow(t, "orphaned", "scope-a", backfillObservation("one", "v1", false)),
	}
	backend.build = catalog.NewSemanticInventoryBuild("project", "generation", "")
	backend.build.State = catalog.SemanticInventoryComplete
	backend.build.SourceKind = catalog.SemanticInventorySourceRetained
	backend.build.ScannedResources = 1
	backend.build.EntryIndexVersion = catalog.SemanticInventoryEntryIndexVersion

	_, err := BackfillSemanticInventory(context.Background(), backend, backfillManifest(t, publication.StateReady), SemanticInventoryBackfillOptions{Project: "project", DatasetGeneration: "generation"})
	if err == nil || !strings.Contains(err.Error(), "missing retained semantic coverage source sentinel") {
		t.Fatalf("missing source sentinel error = %v, want fail-closed reconciliation", err)
	}
	if backend.coverageResourceStates["Observation"] != "FAILED" || backend.coverageState != catalogarango.SemanticCoverageBuildFailed || backend.coverageFinalizeCalls != 0 {
		t.Fatalf("missing sentinel states: resources=%v global=%q finalizers=%d", backend.coverageResourceStates, backend.coverageState, backend.coverageFinalizeCalls)
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
	rows                       map[string][]catalogarango.RetainedSemanticInventoryRow
	contributions              map[string]catalog.SemanticInventoryContribution
	coverageContributions      map[string]catalog.SemanticCoverageSourceContribution
	coverageResourceStates     map[string]string
	coverageFinalizedResources []string
	build                      catalog.SemanticInventoryBuild
	coverageIdentity           catalogarango.SemanticCoverageBuildIdentity
	coverageState              catalogarango.SemanticCoverageBuildState
	claimedByOther             bool
	prepared                   bool
	readCalls                  int
	writeCalls                 int
	failWriteAt                int
	coverageBeginCalls         int
	coverageWriteCalls         int
	failCoverageWriteAt        int
	coverageFinalizeCalls      int
	coverageReadRows           int64
	advanceCalls               int
	completeCalls              int
	missingCollections         map[string]bool
}

func newBackfillFake() *backfillFake {
	return &backfillFake{
		rows:                   map[string][]catalogarango.RetainedSemanticInventoryRow{},
		contributions:          map[string]catalog.SemanticInventoryContribution{},
		coverageContributions:  map[string]catalog.SemanticCoverageSourceContribution{},
		coverageResourceStates: map[string]string{}, missingCollections: map[string]bool{},
	}
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
	f.advanceCalls++
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
	f.completeCalls++
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

func (f *backfillFake) BeginSemanticCoverageBuild(_ context.Context, identity catalogarango.SemanticCoverageBuildIdentity) error {
	f.coverageBeginCalls++
	f.coverageIdentity = identity
	f.coverageState = catalogarango.SemanticCoverageBuildBuilding
	f.coverageResourceStates = map[string]string{}
	f.coverageFinalizedResources = nil
	return nil
}

func (f *backfillFake) BeginSemanticCoverageResource(_ context.Context, identity catalogarango.SemanticCoverageBuildIdentity, resourceType string) error {
	if identity != f.coverageIdentity || f.coverageState != catalogarango.SemanticCoverageBuildBuilding {
		return errors.New("semantic coverage resource began outside its build")
	}
	f.coverageResourceStates[resourceType] = "BUILDING"
	return nil
}

func (f *backfillFake) MarkSemanticCoverageResourceMissing(_ context.Context, identity catalogarango.SemanticCoverageBuildIdentity, resourceType string) error {
	if identity != f.coverageIdentity {
		return errors.New("semantic coverage resource identity mismatch")
	}
	f.coverageResourceStates[resourceType] = "MISSING"
	return nil
}

func (f *backfillFake) CompleteSemanticCoverageResource(_ context.Context, identity catalogarango.SemanticCoverageBuildIdentity, expectation catalogarango.SemanticCoverageResourceExpectation, _ int) error {
	if identity != f.coverageIdentity || !expectation.SourceExists || !expectation.Terminal {
		return errors.New("semantic coverage source expectation is incomplete")
	}
	f.coverageResourceStates[expectation.ResourceType] = "COMPLETE"
	return nil
}

func (f *backfillFake) CompleteRetainedSemanticCoverageResource(_ context.Context, identity catalogarango.SemanticCoverageBuildIdentity, resourceType string, _, _ int) (int64, error) {
	if identity != f.coverageIdentity {
		return 0, errors.New("semantic coverage resource identity mismatch")
	}
	if f.missingCollections[resourceType] {
		f.coverageResourceStates[resourceType] = "MISSING"
		return 0, nil
	}
	rows := append([]catalogarango.RetainedSemanticInventoryRow(nil), f.rows[resourceType]...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	for _, row := range rows {
		sourceID := "retained:" + resourceType + "/" + row.Key
		contribution, ok := f.coverageContributions[sourceID]
		if !ok || contribution.Identity.AuthResourcePath != dereference(row.AuthResourcePath) {
			f.coverageResourceStates[resourceType] = "FAILED"
			return 0, fmt.Errorf("missing retained semantic coverage source sentinel %s", sourceID)
		}
	}
	for sourceID := range f.coverageContributions {
		if strings.HasPrefix(sourceID, "retained:"+resourceType+"/") {
			found := false
			for _, row := range rows {
				if sourceID == "retained:"+resourceType+"/"+row.Key {
					found = true
					break
				}
			}
			if !found {
				f.coverageResourceStates[resourceType] = "FAILED"
				return 0, fmt.Errorf("unexpected retained semantic coverage source sentinel %s", sourceID)
			}
		}
	}
	f.coverageResourceStates[resourceType] = "COMPLETE"
	return int64(len(rows)), nil
}

func (f *backfillFake) WriteSemanticCoverageContributions(_ context.Context, buildID string, contributions []catalog.SemanticCoverageSourceContribution, _ int) (catalogarango.SemanticCoverageWriteStats, error) {
	f.coverageWriteCalls++
	if f.failCoverageWriteAt != 0 && f.coverageWriteCalls == f.failCoverageWriteAt {
		return catalogarango.SemanticCoverageWriteStats{}, errors.New("injected coverage write failure")
	}
	var stats catalogarango.SemanticCoverageWriteStats
	for _, contribution := range contributions {
		if contribution.BuildID != buildID {
			return stats, errors.New("coverage contribution build mismatch")
		}
		if f.coverageResourceStates[contribution.Identity.ResourceType] != "BUILDING" {
			return stats, fmt.Errorf("semantic coverage source %s was written before resource certificate begin", contribution.Identity.SourceID)
		}
		f.coverageContributions[contribution.Identity.SourceID] = contribution
		stats.SourceSetsReplaced++
		stats.Rows += int64(1 + len(contribution.Paths))
	}
	return stats, nil
}

func (f *backfillFake) FinalizeSemanticCoverageBuild(_ context.Context, identity catalogarango.SemanticCoverageBuildIdentity, expectedResourceTypes []string) error {
	f.coverageFinalizeCalls++
	f.coverageIdentity = identity
	f.coverageFinalizedResources = append([]string(nil), expectedResourceTypes...)
	for _, resourceType := range expectedResourceTypes {
		state, ok := f.coverageResourceStates[resourceType]
		if !ok || state != "COMPLETE" {
			f.coverageState = catalogarango.SemanticCoverageBuildPartial
			return nil
		}
	}
	f.coverageState = catalogarango.SemanticCoverageBuildComplete
	return nil
}

func (f *backfillFake) FailSemanticCoverageResource(_ context.Context, identity catalogarango.SemanticCoverageBuildIdentity, resourceType, _ string) error {
	if identity != f.coverageIdentity {
		return errors.New("semantic coverage resource identity mismatch")
	}
	f.coverageResourceStates[resourceType] = "FAILED"
	return nil
}

func (f *backfillFake) FailSemanticCoverageBuild(_ context.Context, _ catalogarango.SemanticCoverageBuildIdentity, _ string) error {
	f.coverageState = catalogarango.SemanticCoverageBuildFailed
	return nil
}

func (f *backfillFake) EnsureSemanticInventoryEntries(_ context.Context, build catalog.SemanticInventoryBuild) (catalog.SemanticInventoryBuild, error) {
	build.EntryIndexVersion = catalog.SemanticInventoryEntryIndexVersion
	f.build = build
	return build, nil
}

func dereference(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
