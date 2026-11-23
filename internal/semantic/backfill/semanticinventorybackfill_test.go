package backfill

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
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/semantic"
	semanticarango "github.com/calypr/loom/internal/semantic/arango"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/testkit/arangotest"
	"github.com/google/uuid"
)

func TestBackfillSemanticInventoryResumesPartialPageWithoutDuplicates(t *testing.T) {
	backend := newBackfillFake()
	backend.rows["Observation"] = []semanticarango.RetainedSemanticInventoryRow{
		backfillSourceRow(t, "a", "scope-a", backfillObservation("shared", "v1", false)),
		backfillSourceRow(t, "b", "scope-b", backfillObservation("shared", "v1", false)),
		backfillSourceRow(t, "c", "scope-a", backfillObservation("third", "v1", true)),
	}
	original := cloneBackfillRows(backend.rows["Observation"])
	backend.failWriteAt = 4
	options := SemanticInventoryBackfillOptions{Project: "project", DatasetGeneration: "generation", PageSize: 2, BatchSize: 1}
	manifest := backfillManifest(t, publication.StateStaged)

	if _, err := BackfillSemanticInventory(context.Background(), backend, manifest, options); err == nil {
		t.Fatal("interrupted backfill error = nil, want persistence failure")
	}
	if got, want := backend.build.State, semantic.Failed; got != want {
		t.Fatalf("failed build state = %q, want %q", got, want)
	}
	if got, want := backend.build.ScannedResources, int64(2); got != want {
		t.Fatalf("failure reset durable scanned count to %d, want %d", got, want)
	}
	if got, want := backend.build.SourceCheckpoint, (semantic.Checkpoint{Collection: "Observation", Key: "b"}); got != want {
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
	if report.State != semantic.Complete || report.ScannedThisRun != 1 || report.ScannedTotal != 3 {
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
	readsBeforeNoOp := backend.readCalls
	second, err := BackfillSemanticInventory(context.Background(), backend, manifest, options)
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if !second.NoOp || second.State != semantic.Complete || backend.writeCalls != writesBeforeNoOp || backend.readCalls != readsBeforeNoOp {
		t.Fatalf("second run = %+v, writes %d -> %d source page reads %d -> %d, want completed no-op without source audit", second, writesBeforeNoOp, backend.writeCalls, readsBeforeNoOp, backend.readCalls)
	}
}

func TestBackfillSemanticInventoryRejectsConcurrentClaim(t *testing.T) {
	backend := newBackfillFake()
	backend.claimedByOther = true
	_, err := BackfillSemanticInventory(context.Background(), backend, backfillManifest(t, publication.StateStaged), SemanticInventoryBackfillOptions{
		Project: "project", DatasetGeneration: "generation",
	})
	if !errors.Is(err, semanticarango.ErrSemanticInventoryClaimed) {
		t.Fatalf("concurrent claim error = %v, want ErrSemanticInventoryClaimed", err)
	}
	if backend.readCalls != 0 || len(backend.contributions) != 0 {
		t.Fatalf("scanner did work after claim rejection: reads=%d contributions=%d", backend.readCalls, len(backend.contributions))
	}
}

func TestBackfillSemanticInventoryMalformedPayloadFailsAndPreservesSource(t *testing.T) {
	backend := newBackfillFake()
	backend.rows["Observation"] = []semanticarango.RetainedSemanticInventoryRow{{Key: "bad", AuthResourcePath: strptr("scope-a"), Payload: json.RawMessage(`[]`)}}
	original := cloneBackfillRows(backend.rows["Observation"])
	_, err := BackfillSemanticInventory(context.Background(), backend, backfillManifest(t, publication.StateReady), SemanticInventoryBackfillOptions{
		Project: "project", DatasetGeneration: "generation",
	})
	if err == nil {
		t.Fatal("malformed payload backfill error = nil")
	}
	if backend.build.State != semantic.Failed {
		t.Fatalf("malformed payload build state = %q, want failed", backend.build.State)
	}
	if backend.build.State == semantic.Complete {
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
	backend.rows["Observation"] = []semanticarango.RetainedSemanticInventoryRow{backfillSourceRow(t, "a", "", backfillObservation("one", "v1", false))}
	backend.missingCollections["Patient"] = true
	manifest := backfillManifestWithRoots(t, publication.StateStaged, []string{"Observation", "Patient"})
	report, err := BackfillSemanticInventory(context.Background(), backend, manifest, SemanticInventoryBackfillOptions{
		Project: "project", DatasetGeneration: "generation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.State != semantic.Complete || report.SourceAvailability != semantic.SourceAvailabilityUnproven {
		t.Fatalf("missing-root report = %+v, want completed scan with explicit unproven availability", report)
	}
	if len(report.MissingCollections) != 1 || report.MissingCollections[0] != "Patient" || backend.build.SourceAvailability != semantic.SourceAvailabilityUnproven {
		t.Fatalf("missing-root evidence report=%v durable=%q", report.MissingCollections, backend.build.SourceAvailability)
	}
}

func TestBackfillSemanticInventoryCancellationReleasesClaimForImmediateResume(t *testing.T) {
	backend := newBackfillFake()
	backend.rows["Observation"] = []semanticarango.RetainedSemanticInventoryRow{
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
	if backend.build.State != semantic.Failed || backend.build.LeaseToken != "" {
		t.Fatalf("cancelled run left build/claim as %+v", backend.build)
	}
	resumed, err := BackfillSemanticInventory(context.Background(), backend, manifest, SemanticInventoryBackfillOptions{
		Project: "project", DatasetGeneration: "generation", PageSize: 1,
	})
	if err != nil || resumed.State != semantic.Complete || resumed.ScannedTotal != 2 {
		t.Fatalf("immediate post-cancel resume = %+v err=%v", resumed, err)
	}
}

func assertBackfillContributionScopes(t *testing.T, contributions map[string]semantic.Contribution) {
	t.Helper()
	scopes := map[string]map[string]bool{}
	for _, contribution := range contributions {
		if contribution.SourceKind != semantic.SourceRetained {
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

func backfillSourceRow(t *testing.T, key, authPath string, payload map[string]any) semanticarango.RetainedSemanticInventoryRow {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return semanticarango.RetainedSemanticInventoryRow{Key: key, AuthResourcePath: strptr(authPath), Payload: encoded}
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

func cloneBackfillRows(rows []semanticarango.RetainedSemanticInventoryRow) []semanticarango.RetainedSemanticInventoryRow {
	cloned := make([]semanticarango.RetainedSemanticInventoryRow, len(rows))
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
	rows               map[string][]semanticarango.RetainedSemanticInventoryRow
	contributions      map[string]semantic.Contribution
	build              semantic.Build
	claimedByOther     bool
	prepared           bool
	readCalls          int
	writeCalls         int
	failWriteAt        int
	advanceCalls       int
	completeCalls      int
	missingCollections map[string]bool
}

func newBackfillFake() *backfillFake {
	return &backfillFake{
		rows:               map[string][]semanticarango.RetainedSemanticInventoryRow{},
		contributions:      map[string]semantic.Contribution{},
		missingCollections: map[string]bool{},
	}
}

func (f *backfillFake) PrepareSemanticInventoryBackfill(context.Context) error {
	f.prepared = true
	return nil
}

func (f *backfillFake) ReadRetainedSemanticInventoryPage(ctx context.Context, project, generation, collection, afterKey string, limit int) (semanticarango.RetainedSemanticInventoryPage, error) {
	if err := ctx.Err(); err != nil {
		return semanticarango.RetainedSemanticInventoryPage{}, err
	}
	f.readCalls++
	if project != "project" || generation != "generation" {
		return semanticarango.RetainedSemanticInventoryPage{}, fmt.Errorf("unexpected source scope %s/%s", project, generation)
	}
	rows := append([]semanticarango.RetainedSemanticInventoryRow(nil), f.rows[collection]...)
	if f.missingCollections[collection] {
		return semanticarango.RetainedSemanticInventoryPage{Rows: []semanticarango.RetainedSemanticInventoryRow{}}, nil
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	page := semanticarango.RetainedSemanticInventoryPage{Rows: make([]semanticarango.RetainedSemanticInventoryRow, 0, limit), SourceExists: true}
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

func (f *backfillFake) ClaimSemanticInventoryBackfill(_ context.Context, build semantic.Build, token string, lease time.Duration) (semantic.Build, bool, error) {
	if f.claimedByOther {
		return semantic.Build{}, false, semanticarango.ErrSemanticInventoryClaimed
	}
	if f.build.State == semantic.Complete {
		return f.build, false, nil
	}
	if f.build.LeaseToken != "" && f.build.LeaseToken != token {
		return semantic.Build{}, false, semanticarango.ErrSemanticInventoryClaimed
	}
	if f.build.Key == "" {
		f.build = build
	}
	if f.build.SourceKind != build.SourceKind {
		f.build.SourceCheckpoint = semantic.Checkpoint{}
		f.build.ScannedResources = 0
	}
	f.build.SourceKind = build.SourceKind
	f.build.State = semantic.Running
	f.build.LeaseToken = token
	f.build.LeaseExpiresAt = time.Now().Add(lease).UnixMilli()
	return f.build, true, nil
}

func (f *backfillFake) AdvanceSemanticInventoryBackfill(_ context.Context, build semantic.Build, token string, expected, next semantic.Checkpoint, scanned int64, lease time.Duration) (semantic.Build, error) {
	f.advanceCalls++
	if f.build.LeaseToken != token || f.build.SourceCheckpoint != expected {
		return semantic.Build{}, semanticarango.ErrSemanticInventoryClaimLost
	}
	f.build.SourceCheckpoint = next
	f.build.ScannedResources = scanned
	f.build.SourceAvailability = build.SourceAvailability
	f.build.LeaseExpiresAt = time.Now().Add(lease).UnixMilli()
	return f.build, nil
}

func (f *backfillFake) CompleteSemanticInventoryBackfill(_ context.Context, build semantic.Build, token string, expected semantic.Checkpoint, scanned int64) (semantic.Build, error) {
	f.completeCalls++
	if f.build.LeaseToken != token || f.build.SourceCheckpoint != expected {
		return semantic.Build{}, semanticarango.ErrSemanticInventoryClaimLost
	}
	f.build.State = semantic.Complete
	f.build.ScannedResources = scanned
	f.build.SourceAvailability = build.SourceAvailability
	f.build.EntryIndexVersion = semantic.EntryIndexVersion
	f.build.LeaseToken = ""
	f.build.LeaseExpiresAt = 0
	return f.build, nil
}

func (f *backfillFake) FailSemanticInventoryBackfill(ctx context.Context, _ semantic.Build, token, diagnostic string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.build.LeaseToken != token {
		return semanticarango.ErrSemanticInventoryClaimLost
	}
	f.build.State = semantic.Failed
	f.build.LeaseToken = ""
	f.build.LeaseExpiresAt = 0
	f.build.Diagnostics = []string{diagnostic}
	return nil
}

func (f *backfillFake) WriteSemanticInventoryContributions(_ context.Context, contributions []semantic.Contribution, _ int) error {
	f.writeCalls++
	if f.failWriteAt != 0 && f.writeCalls == f.failWriteAt {
		return errors.New("injected interrupted write")
	}
	for _, contribution := range contributions {
		f.contributions[contribution.Key] = contribution
	}
	return nil
}

func (f *backfillFake) EnsureSemanticInventoryEntries(_ context.Context, build semantic.Build) (semantic.Build, error) {
	build.EntryIndexVersion = semantic.EntryIndexVersion
	f.build = build
	return build, nil
}

func TestRetainedSemanticInventoryBackfillArangoIntegration(t *testing.T) {
	owned := arangotest.Owned(t, "loom_test_backfill_")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	client := owned.Client
	project := "c01-backfill-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	generation := strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := client.Bootstrap(ctx, arangostore.BootstrapSpec{Collections: []arangostore.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}
	store, err := semanticarango.New(client)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareSemanticInventoryBackfill(ctx); err != nil {
		t.Fatal(err)
	}
	payloadA := backfillIntegrationPayload("scope-a", 1000)
	payloadB := backfillIntegrationPayload("scope-b", 2)
	keys := []string{"c01_" + strings.ReplaceAll(uuid.NewString(), "-", ""), "c01_" + strings.ReplaceAll(uuid.NewString(), "-", "")}
	fixtureDocs := make([]map[string]any, 2)
	for index, fixture := range []struct {
		payload map[string]any
		scope   string
	}{{payloadA, "scope-a"}, {payloadB, "scope-b"}} {
		doc := map[string]any{
			"_key": keys[index], "project": project, "dataset_generation": generation,
			"auth_resource_path": fixture.scope, "payload": fixture.payload,
		}
		encoded, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		fixtureDocs[index] = doc
		if err := client.InsertBatchRaw(ctx, "Observation", []json.RawMessage{encoded}, false, ""); err != nil {
			t.Fatalf("insert isolated retained source %s: %v", fixture.scope, err)
		}
	}
	before := readBackfillIntegrationSources(t, ctx, store, project, generation)
	manifest := integrationBackfillManifest(t, project, generation)
	build := semantic.NewBuild(project, generation, "")
	build.SourceKind = semantic.SourceRetained
	if _, claimed, err := store.ClaimSemanticInventoryBackfill(ctx, build, "integration-owner", time.Minute); err != nil || !claimed {
		t.Fatalf("initial explicit claim claimed=%v err=%v", claimed, err)
	}
	if _, claimed, err := store.ClaimSemanticInventoryBackfill(ctx, build, "integration-contender", time.Minute); err != semanticarango.ErrSemanticInventoryClaimed || claimed {
		t.Fatalf("concurrent explicit claim claimed=%v err=%v, want claim rejection", claimed, err)
	}
	if err := store.FailSemanticInventoryBackfill(ctx, build, "integration-owner", "release test claim"); err != nil {
		t.Fatal(err)
	}
	options := SemanticInventoryBackfillOptions{Project: project, DatasetGeneration: generation, PageSize: 10, BatchSize: 64}
	var progressCount int
	options.Progress = func(SemanticInventoryBackfillProgress) { progressCount++ }
	report, err := BackfillSemanticInventory(ctx, store, manifest, options)
	if err != nil {
		t.Fatal(err)
	}
	if report.State != semantic.Complete || report.SourceAvailability != semantic.SourceAvailabilityVerified || report.ScannedTotal != 2 || report.Contributions != 1002 || progressCount == 0 {
		t.Fatalf("retained backfill report = %+v progress events=%d, want two source rows and 1002 contributions", report, progressCount)
	}
	assertRetainedInventoryPageCodes(t, ctx, store, project, generation, []string{"scope-a"}, 1000, "scope-a", 1)
	assertRetainedInventoryPageCodes(t, ctx, store, project, generation, []string{"scope-b"}, 2, "scope-b", 1)
	assertRetainedInventoryPageCodes(t, ctx, store, project, generation, []string{"scope-a", "scope-b"}, 1001, "scope-a", 2)
	pageAfterReplay, err := collectRetainedInventoryPageCodes(ctx, store, project, generation, []string{"scope-a", "scope-b"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := BackfillSemanticInventory(ctx, store, manifest, options)
	if err != nil || !second.NoOp {
		t.Fatalf("second retained backfill = %+v err=%v, want complete no-op", second, err)
	}
	pageAfterSecondRun, err := collectRetainedInventoryPageCodes(ctx, store, project, generation, []string{"scope-a", "scope-b"})
	if err != nil || !equalStringSets(pageAfterReplay, pageAfterSecondRun) {
		t.Fatalf("second-run inventory changed: before=%d after=%d err=%v", len(pageAfterReplay), len(pageAfterSecondRun), err)
	}
	after := readBackfillIntegrationSources(t, ctx, store, project, generation)
	if len(before) != len(fixtureDocs) || len(after) != len(before) {
		t.Fatalf("retained source row counts before=%d after=%d fixture=%d", len(before), len(after), len(fixtureDocs))
	}
	for key, source := range before {
		if after[key].Key != source.Key || after[key].AuthResourcePath == nil || source.AuthResourcePath == nil || *after[key].AuthResourcePath != *source.AuthResourcePath || string(after[key].Payload) != string(source.Payload) {
			t.Fatalf("retained source %s changed during backfill", key)
		}
	}
}

func backfillIntegrationPayload(scope string, distinct int) map[string]any {
	codes := []string{}
	if scope == "scope-a" {
		for index := 0; index < distinct; index++ {
			codes = append(codes, fmt.Sprintf("concept-%04d", index))
		}
	} else {
		codes = []string{"concept-0000", "scope-b-only"}
	}
	components := make([]any, 0, len(codes))
	for _, code := range codes {
		components = append(components, map[string]any{
			"code":          map[string]any{"coding": []any{map[string]any{"system": "urn:c01-backfill", "version": "v1", "code": code}}},
			"valueQuantity": map[string]any{"value": 1.0, "unit": "mg"},
		})
	}
	return map[string]any{"resourceType": "Observation", "id": scope, "component": components}
}

func readBackfillIntegrationSources(t *testing.T, ctx context.Context, store *semanticarango.Store, project, generation string) map[string]semanticarango.RetainedSemanticInventoryRow {
	t.Helper()
	page, err := store.ReadRetainedSemanticInventoryPage(ctx, project, generation, "Observation", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	rows := make(map[string]semanticarango.RetainedSemanticInventoryRow, len(page.Rows))
	for _, row := range page.Rows {
		rows[row.Key] = row
	}
	return rows
}

func integrationBackfillManifest(t *testing.T, project, generation string) publication.Manifest {
	t.Helper()
	ref, err := publication.NewRef(project, generation)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := publication.NewSchemaSnapshot("c01-backfill", "4.0.1", strings.Repeat("b", 64), []string{"Observation"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := publication.NewManifest(ref, schema)
	if err != nil {
		t.Fatal(err)
	}
	manifest.State = publication.StateStaged
	return manifest
}

func assertRetainedInventoryPageCodes(t *testing.T, ctx context.Context, store *semanticarango.Store, project, generation string, paths []string, wantCount int, prefix string, sharedPopulation int64) {
	t.Helper()
	codes, err := collectRetainedInventoryPageCodes(ctx, store, project, generation, paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != wantCount {
		t.Fatalf("inventory code count for %v = %d, want %d", paths, len(codes), wantCount)
	}
	if len(paths) == 1 && paths[0] == "scope-a" {
		for index := 0; index < 1000; index++ {
			code := fmt.Sprintf("concept-%04d", index)
			if _, ok := codes[code]; !ok {
				t.Fatalf("scope-a page is missing expected code %q", code)
			}
		}
	}
	shared := codes["concept-0000"]
	if shared.Population != sharedPopulation {
		t.Fatalf("shared concept population for %v = %d, want %d", paths, shared.Population, sharedPopulation)
	}
	if prefix == "scope-b" {
		if _, ok := codes["scope-b-only"]; !ok {
			t.Fatal("scope-b inventory is missing its distinct concept")
		}
		if _, ok := codes["concept-0001"]; ok {
			t.Fatal("scope-b inventory leaked scope-a-only concept concept-0001")
		}
	}
}

func collectRetainedInventoryPageCodes(ctx context.Context, store *semanticarango.Store, project, generation string, paths []string) (map[string]semantic.Observation, error) {
	options := semantic.PageOptions{
		Project: project, DatasetGeneration: generation, AuthResourcePaths: paths,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
		Limit:                         semantic.PageLimit,
	}
	codes := map[string]semantic.Observation{}
	for {
		page, err := store.PageSemanticInventory(ctx, options)
		if err != nil {
			return nil, err
		}
		if page.State != semantic.Complete {
			return nil, fmt.Errorf("inventory state for %v is %q, want complete", paths, page.State)
		}
		if len(page.Entries) > semantic.PageLimit {
			return nil, fmt.Errorf("inventory page size %d exceeds %d", len(page.Entries), semantic.PageLimit)
		}
		for _, entry := range page.Entries {
			code := entry.Observation.Key.Code
			if _, duplicate := codes[code]; duplicate {
				return nil, fmt.Errorf("inventory page repeated code %q", code)
			}
			codes[code] = entry.Observation
		}
		if page.NextCursor == "" {
			return codes, nil
		}
		options.Cursor = page.NextCursor
	}
}

func equalStringSets(left, right map[string]semantic.Observation) bool {
	if len(left) != len(right) {
		return false
	}
	for code := range left {
		if _, ok := right[code]; !ok {
			return false
		}
	}
	return true
}
