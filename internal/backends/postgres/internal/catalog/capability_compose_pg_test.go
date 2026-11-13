// Composition witnesses for the full catalog Store (defect
// PG-PG-CAPABILITY-EVIDENCE-COMPOSITION-PANIC). The Explorer capability
// resolver consumes all three evidence reads from ONE value
// (catalog.ReadCapabilityEvidence); these tests pin that a Store assembled
// with NewFullStore answers all three legs from their own repositories with
// exactly one boundary read per leg, and that a Store missing a leg refuses
// with the typed ErrStoreLegNotAssembled — never a nil dereference.
// Counting wrappers delegate to the real repositories unchanged; they never
// fake results.
package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/catalog"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/jackc/pgx/v5"
)

const (
	ccProject   = "cap-compose-p"
	ccGen       = "cap-compose-g"
	ccAuthPath  = "/programs/loom/projects/cap-compose-p/bound"
	ccOtherPath = "/programs/loom/projects/cap-compose-p/foreign"
)

// ccInventoryWrapper counts every call on the inventory boundary; all reads
// delegate to the real repository unchanged.
type ccInventoryWrapper struct {
	real *storepostgres.InventoryRepository

	scanSourceCalls   int
	insertCountsCalls int
	publishBuildCalls int
	readBuildCalls    int
	readCountSums     int
}

func (w *ccInventoryWrapper) ScanSourceCounts(ctx context.Context, project, datasetGeneration, resourceType string) ([]storepostgres.InventorySourceGroup, error) {
	w.scanSourceCalls++
	return w.real.ScanSourceCounts(ctx, project, datasetGeneration, resourceType)
}

func (w *ccInventoryWrapper) InsertCounts(ctx context.Context, rows []catalog.ResourceInventoryCount, batchSize int) error {
	w.insertCountsCalls++
	return w.real.InsertCounts(ctx, rows, batchSize)
}

func (w *ccInventoryWrapper) PublishBuild(ctx context.Context, build catalog.ResourceInventoryBuild) error {
	w.publishBuildCalls++
	return w.real.PublishBuild(ctx, build)
}

func (w *ccInventoryWrapper) ReadBuild(ctx context.Context, project, datasetGeneration string) (catalog.ResourceInventoryBuild, bool, error) {
	w.readBuildCalls++
	return w.real.ReadBuild(ctx, project, datasetGeneration)
}

func (w *ccInventoryWrapper) ReadCountSums(ctx context.Context, scope storepostgres.InventoryCountScope) (map[string]int64, error) {
	w.readCountSums++
	return w.real.ReadCountSums(ctx, scope)
}

// ccProfileWrapper counts every call on the field-profile boundary.
type ccProfileWrapper struct {
	real *storepostgres.FieldProfileRepository

	upsertCalls       int
	readRowsCalls     int
	readFilteredCalls int
	readCoverageCalls int
	listDistinctCalls int
}

func (w *ccProfileWrapper) UpsertFieldProfileBatch(ctx context.Context, scope storepostgres.FieldProfileScope, docs []catalog.FieldCatalogDocument, coverage storepostgres.FieldProfileCoverage, batchSize int) error {
	w.upsertCalls++
	return w.real.UpsertFieldProfileBatch(ctx, scope, docs, coverage, batchSize)
}

func (w *ccProfileWrapper) ReadFieldProfileRows(ctx context.Context, scope storepostgres.FieldProfileScope) ([]catalog.FieldCatalogDocument, error) {
	w.readRowsCalls++
	return w.real.ReadFieldProfileRows(ctx, scope)
}

func (w *ccProfileWrapper) ReadFieldProfileRowsFiltered(ctx context.Context, filter storepostgres.FieldProfileFilter) ([]catalog.FieldCatalogDocument, error) {
	w.readFilteredCalls++
	return w.real.ReadFieldProfileRowsFiltered(ctx, filter)
}

func (w *ccProfileWrapper) ReadProfileCoverage(ctx context.Context, filter storepostgres.FieldProfileFilter) ([]storepostgres.FieldProfileCoverage, error) {
	w.readCoverageCalls++
	return w.real.ReadProfileCoverage(ctx, filter)
}

func (w *ccProfileWrapper) ListDistinctAuthResourcePaths(ctx context.Context, project string) ([]string, error) {
	w.listDistinctCalls++
	return w.real.ListDistinctAuthResourcePaths(ctx, project)
}

// ccRelationshipWrapper counts every call on the relationship boundary.
type ccRelationshipWrapper struct {
	real *RelationshipEvidenceRepository

	scanCalls int
}

func (w *ccRelationshipWrapper) ScanRelationshipOccurrences(ctx context.Context, scope RelationshipScope) ([]RelationshipOccurrenceGroup, error) {
	w.scanCalls++
	return w.real.ScanRelationshipOccurrences(ctx, scope)
}

func (w *ccRelationshipWrapper) reset() { w.scanCalls = 0 }

func (w *ccInventoryWrapper) reset() {
	w.scanSourceCalls, w.insertCountsCalls, w.publishBuildCalls, w.readBuildCalls, w.readCountSums = 0, 0, 0, 0, 0
}

func (w *ccProfileWrapper) reset() {
	w.upsertCalls, w.readRowsCalls, w.readFilteredCalls, w.readCoverageCalls, w.listDistinctCalls = 0, 0, 0, 0, 0
}

// ccRepos builds one recording wrapper per boundary over the schema's real
// repositories.
func ccRepos(t *testing.T, s *fieldProfileService) (*ccInventoryWrapper, *ccProfileWrapper, *ccRelationshipWrapper) {
	t.Helper()
	inventory, err := storepostgres.NewInventoryRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("new inventory repository", err)
	}
	profiles, err := storepostgres.NewFieldProfileRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("new field profile repository", err)
	}
	relationships, err := NewRelationshipEvidenceRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("new relationship evidence repository", err)
	}
	return &ccInventoryWrapper{real: inventory}, &ccProfileWrapper{real: profiles}, &ccRelationshipWrapper{real: relationships}
}

// ccSeedGeneration stages one generation through the production write
// entries: source rows, a published inventory build, field profiles with a
// coverage marker, and one concrete-typed relationship occurrence. Seeding
// runs on the un-wrapped repositories so the measured counters stay clean.
func ccSeedGeneration(t *testing.T, s *fieldProfileService) {
	t.Helper()
	invRepo, err := storepostgres.NewInventoryRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("seed: inventory repository", err)
	}
	profRepo, err := storepostgres.NewFieldProfileRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("seed: field profile repository", err)
	}
	for _, row := range []struct{ rtype, id, authPath string }{
		{"Patient", "p1", ccAuthPath},
		{"Observation", "o1", ccAuthPath},
		{"Observation", "o2", ccOtherPath},
	} {
		s.exec(t, "INSERT INTO "+pgx.Identifier{s.schema, "fhir_resource"}.Sanitize()+
			" (project, dataset_generation, resource_type, logical_id, auth_resource_path)"+
			" VALUES ($1, $2, $3, $4, $5)",
			ccProject, ccGen, row.rtype, row.id, row.authPath)
	}
	invStore, err := New(invRepo)
	if err != nil {
		s.fail("seed: inventory store", err)
	}
	// The published build must cover every concrete type, or the default
	// (all-types) discovery read is a typed unavailable build.
	if _, err := invStore.RebuildResourceInventory(s.ctx, catalog.ResourceInventoryRebuildOptions{
		Project: ccProject, DatasetGeneration: ccGen, ResourceTypes: fhirschema.ResourceTypes(), BatchSize: 2,
	}); err != nil {
		s.fail("seed: rebuild inventory", err)
	}
	profStore, err := NewFieldProfileStore(profRepo)
	if err != nil {
		s.fail("seed: profile store", err)
	}
	if err := profStore.WriteFieldProfile(s.ctx, fdScope(ccProject, ccGen, ccAuthPath, "Patient"),
		[]catalog.FieldCatalogDocument{fdDoc(ccProject, ccGen, ccAuthPath, "Patient", "gender", 1, "female")}, 100); err != nil {
		s.fail("seed: write field profile", err)
	}
	// The occurrence mirrors a generated traversal (Condition subject ->
	// Patient): ProjectRelationshipObservation drops labels that are not
	// supported generated traversals, and the leg must carry a value.
	s.exec(t, "INSERT INTO "+pgx.Identifier{s.schema, "fhir_edge_occurrence"}.Sanitize()+
		" (project, dataset_generation, auth_resource_path, label,"+
		" source_resource_type, source_logical_id, target_resource_type, target_logical_id,"+
		" source_path, source_ordinal)"+
		" VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)",
		ccProject, ccGen, ccAuthPath, "subject_Patient", "Condition", "cond-cap1", "Patient", "p1", "subject", int64(0))
}

func ccEvidenceOptions() catalog.CapabilityEvidenceOptions {
	return catalog.CapabilityEvidenceOptions{
		Project: ccProject, DatasetGeneration: ccGen,
		AuthResourcePaths:             []string{ccAuthPath},
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
	}
}

// TestFullStoreCapabilityEvidenceReadsEachLegOnce is the exactly-one-query
// witness for the composed capability reader: one ReadCapabilityEvidence
// over the full store drives exactly one boundary read per evidence leg,
// and each leg's result equals the same read served by the untouched real
// repositories (delegation, never fabrication).
func TestFullStoreCapabilityEvidenceReadsEachLegOnce(t *testing.T) {
	s := newFieldProfileService(t)
	ccSeedGeneration(t, s)
	inventory, profiles, relationships := ccRepos(t, s)
	full, err := NewFullStore(inventory, profiles, relationships)
	if err != nil {
		s.fail("new full store", err)
	}
	// Truth baseline: the same reads from an unwrapped full store.
	plainInv, err := storepostgres.NewInventoryRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("baseline inventory repository", err)
	}
	plainProf, err := storepostgres.NewFieldProfileRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("baseline profile repository", err)
	}
	plainRel, err := NewRelationshipEvidenceRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("baseline relationship repository", err)
	}
	plain, err := NewFullStore(plainInv, plainProf, plainRel)
	if err != nil {
		s.fail("new baseline full store", err)
	}
	opts := ccEvidenceOptions()
	want, err := catalog.ReadCapabilityEvidence(s.ctx, plain, opts)
	if err != nil {
		s.fail("baseline capability evidence", err)
	}

	inventory.reset()
	profiles.reset()
	relationships.reset()
	got, err := catalog.ReadCapabilityEvidence(s.ctx, full, opts)
	if err != nil {
		s.fail("capability evidence over full store", err)
	}
	if !got.ResourceInventory.Available || !got.Relationships.Available || !got.FieldEnrichment.Available {
		t.Fatalf("a seeded generation must answer every leg: inventory=%v relationships=%v enrichment=%v",
			got.ResourceInventory.Available, got.Relationships.Available, got.FieldEnrichment.Available)
	}
	if !reflect.DeepEqual(got.ResourceInventory, want.ResourceInventory) ||
		!reflect.DeepEqual(got.Relationships, want.Relationships) ||
		!reflect.DeepEqual(got.FieldEnrichment, want.FieldEnrichment) {
		t.Fatalf("full-store evidence diverges from the real-repository baseline:\n got %+v\nwant %+v", got, want)
	}
	if len(got.Relationships.Values) == 0 || len(got.FieldEnrichment.Values) == 0 {
		t.Fatalf("seeded legs must carry values: rel=%d enr=%d",
			len(got.Relationships.Values), len(got.FieldEnrichment.Values))
	}
	// The published build carries a row per concrete type, so a
	// non-empty Values list proves nothing on its own (adversarial
	// finding M3): pin the seeded restricted-scope counts to the rows
	// actually stored. Deleting the fhir_resource seeding must fail here,
	// not merely shrink a vacuous length check.
	// Per-type totals within the restricted scope (the observation shape
	// carries no auth path on this lane): Observation=1 witnesses that the
	// foreign-path row o2 is excluded by the scope, and both pins fail if
	// the fhir_resource seeding disappears.
	counts := map[string]int64{}
	for _, v := range got.ResourceInventory.Values {
		counts[v.ResourceType] += v.DocumentCount
	}
	if counts["Patient"] != 1 || counts["Observation"] != 1 {
		t.Fatalf("inventory must reflect the seeded rows: Patient=%d Observation=%d, want 1/1 (o2 lives on the foreign path)",
			counts["Patient"], counts["Observation"])
	}
	// Exactly one read per leg, and no cross-leg boundary traffic.
	if inventory.readBuildCalls != 1 || inventory.readCountSums != 1 {
		t.Fatalf("inventory leg boundary calls: readBuild=%d readCountSums=%d, want 1/1", inventory.readBuildCalls, inventory.readCountSums)
	}
	if inventory.scanSourceCalls != 0 || inventory.insertCountsCalls != 0 || inventory.publishBuildCalls != 0 {
		t.Fatalf("inventory reads must not touch write calls: scan=%d insert=%d publish=%d",
			inventory.scanSourceCalls, inventory.insertCountsCalls, inventory.publishBuildCalls)
	}
	if relationships.scanCalls != 1 {
		t.Fatalf("relationship leg boundary calls: scan=%d, want exactly 1", relationships.scanCalls)
	}
	// One filtered read carries the rows; the coverage-marker read only runs
	// on the zero-rows branch (known-empty vs never-persisted), so a
	// seeded scope must issue exactly one filtered read and zero coverage
	// reads.
	if profiles.readFilteredCalls != 1 || profiles.readCoverageCalls != 0 {
		t.Fatalf("enrichment leg boundary calls: filtered=%d coverage=%d, want 1/0",
			profiles.readFilteredCalls, profiles.readCoverageCalls)
	}
	if profiles.upsertCalls != 0 || profiles.readRowsCalls != 0 || profiles.listDistinctCalls != 0 {
		t.Fatalf("enrichment reads must not touch other profile calls: upsert=%d rows=%d distinct=%d",
			profiles.upsertCalls, profiles.readRowsCalls, profiles.listDistinctCalls)
	}
}

// TestNarrowStoreWrongLegRefusesTypedNeverPanics is the negative control
// for the never-nil law (PG-PG-CAPABILITY-EVIDENCE-COMPOSITION-PANIC):
// every method whose repository is absent refuses with typed
// ErrStoreLegNotAssembled, zero boundary queries, and no panic. Deleting a
// requireLeg guard must make this test fail on a nil dereference.
func TestNarrowStoreWrongLegRefusesTypedNeverPanics(t *testing.T) {
	s := newFieldProfileService(t)
	inventory, profiles, relationships := ccRepos(t, s)

	inventoryOnly, err := New(inventory.real)
	if err != nil {
		s.fail("inventory-only store", err)
	}
	profileOnly, err := NewFieldProfileStore(profiles.real)
	if err != nil {
		s.fail("profile-only store", err)
	}
	relationshipOnly, err := NewRelationshipEvidenceStore(relationships.real)
	if err != nil {
		s.fail("relationship-only store", err)
	}

	type probe struct {
		name string
		call func() error
	}
	unrestricted := catalog.ExplicitAuthResourcePathsUnrestricted(false)

	probes := []probe{
		{"inventory-only: relationships", func() error {
			res, err := inventoryOnly.DiscoverRelationshipObservations(s.ctx, catalog.RelationshipObservationOptions{
				Project: ccProject, DatasetGeneration: ccGen, AuthResourcePaths: []string{ccAuthPath}, AuthResourcePathsUnrestricted: unrestricted})
			if err == nil {
				return errors.New("expected typed refusal")
			}
			if res.Available || res.Status != catalog.EvidenceUnavailable || len(res.Values) != 0 {
				return errors.New("relationship refusal must be typed unavailable with no values")
			}
			if !errors.Is(err, ErrStoreLegNotAssembled) {
				return errors.New("refusal must wrap ErrStoreLegNotAssembled, got " + err.Error())
			}
			return nil
		}},
		{"inventory-only: enrichment", func() error {
			res, err := inventoryOnly.DiscoverFieldEnrichment(s.ctx, catalog.FieldEnrichmentOptions{
				Project: ccProject, DatasetGeneration: ccGen, AuthResourcePaths: []string{ccAuthPath}, AuthResourcePathsUnrestricted: unrestricted})
			if err == nil {
				return errors.New("expected typed refusal")
			}
			if res.Available || res.Status != catalog.EvidenceUnavailable || len(res.Values) != 0 {
				return errors.New("enrichment refusal must be typed unavailable with no values")
			}
			if !errors.Is(err, ErrStoreLegNotAssembled) {
				return errors.New("refusal must wrap ErrStoreLegNotAssembled, got " + err.Error())
			}
			return nil
		}},
		{"inventory-only: profiles family", func() error {
			if _, err := inventoryOnly.DiscoverFields(s.ctx, catalog.PopulatedFieldOptions{Project: ccProject, DatasetGeneration: ccGen, AuthResourcePaths: []string{ccAuthPath}, AuthResourcePathsUnrestricted: unrestricted}); !errors.Is(err, ErrStoreLegNotAssembled) {
				return errors.New("DiscoverFields: " + errText(err))
			}
			if _, err := inventoryOnly.DiscoverExistingAuthResourcePaths(s.ctx, catalog.AuthResourcePathOptions{Project: ccProject, AllGenerations: true}); !errors.Is(err, ErrStoreLegNotAssembled) {
				return errors.New("DiscoverExistingAuthResourcePaths: " + errText(err))
			}
			if err := inventoryOnly.WriteFieldProfile(s.ctx, fdScope(ccProject, ccGen, ccAuthPath, "Patient"), nil, 100); !errors.Is(err, ErrStoreLegNotAssembled) {
				return errors.New("WriteFieldProfile: " + errText(err))
			}
			if _, err := inventoryOnly.ReadFieldProfile(s.ctx, fdScope(ccProject, ccGen, ccAuthPath, "Patient")); !errors.Is(err, ErrStoreLegNotAssembled) {
				return errors.New("ReadFieldProfile: " + errText(err))
			}
			return nil
		}},
		{"profile-only: inventory", func() error {
			res, err := profileOnly.DiscoverResourceInventory(s.ctx, catalog.ResourceInventoryOptions{
				Project: ccProject, DatasetGeneration: ccGen, AuthResourcePaths: []string{ccAuthPath}, AuthResourcePathsUnrestricted: unrestricted})
			if err == nil {
				return errors.New("expected typed refusal")
			}
			if res.Available || res.Status != catalog.EvidenceUnavailable || len(res.Values) != 0 {
				return errors.New("inventory refusal must be typed unavailable with no values")
			}
			if !errors.Is(err, ErrStoreLegNotAssembled) {
				return errors.New("refusal must wrap ErrStoreLegNotAssembled, got " + err.Error())
			}
			return nil
		}},
		{"profile-only: relationships", func() error {
			res, err := profileOnly.DiscoverRelationshipObservations(s.ctx, catalog.RelationshipObservationOptions{
				Project: ccProject, DatasetGeneration: ccGen, AuthResourcePaths: []string{ccAuthPath}, AuthResourcePathsUnrestricted: unrestricted})
			if err == nil {
				return errors.New("expected typed refusal")
			}
			if res.Available || res.Status != catalog.EvidenceUnavailable || len(res.Values) != 0 {
				return errors.New("relationship refusal must be typed unavailable with no values")
			}
			if !errors.Is(err, ErrStoreLegNotAssembled) {
				return errors.New("refusal must wrap ErrStoreLegNotAssembled, got " + err.Error())
			}
			return nil
		}},
		{"profile-only: rebuild", func() error {
			_, err := profileOnly.RebuildResourceInventory(s.ctx, catalog.ResourceInventoryRebuildOptions{
				Project: ccProject, DatasetGeneration: ccGen, ResourceTypes: []string{"Patient"}, BatchSize: 2})
			if !errors.Is(err, ErrStoreLegNotAssembled) {
				return errors.New("RebuildResourceInventory: " + errText(err))
			}
			return nil
		}},
		{"relationship-only: inventory+profiles", func() error {
			if _, err := relationshipOnly.DiscoverResourceInventory(s.ctx, catalog.ResourceInventoryOptions{
				Project: ccProject, DatasetGeneration: ccGen, AuthResourcePaths: []string{ccAuthPath}, AuthResourcePathsUnrestricted: unrestricted}); err == nil || !errors.Is(err, ErrStoreLegNotAssembled) {
				return errors.New("DiscoverResourceInventory: " + errText(err))
			}
			if _, err := relationshipOnly.DiscoverFieldEnrichment(s.ctx, catalog.FieldEnrichmentOptions{
				Project: ccProject, DatasetGeneration: ccGen, AuthResourcePaths: []string{ccAuthPath}, AuthResourcePathsUnrestricted: unrestricted}); err == nil || !errors.Is(err, ErrStoreLegNotAssembled) {
				return errors.New("DiscoverFieldEnrichment: " + errText(err))
			}
			if _, err := relationshipOnly.DiscoverFields(s.ctx, catalog.PopulatedFieldOptions{Project: ccProject, DatasetGeneration: ccGen, AuthResourcePaths: []string{ccAuthPath}, AuthResourcePathsUnrestricted: unrestricted}); !errors.Is(err, ErrStoreLegNotAssembled) {
				return errors.New("DiscoverFields: " + errText(err))
			}
			return nil
		}},
		{"profile-only: relationships alias", func() error {
			if _, err := profileOnly.ReadRelationshipObservations(s.ctx, catalog.RelationshipObservationOptions{Project: ccProject, DatasetGeneration: ccGen}); err == nil || !errors.Is(err, ErrStoreLegNotAssembled) {
				return errors.New("profileOnly.ReadRelationshipObservations: " + errText(err))
			}
			return nil
		}},
		{"relationship-only: full profile+inventory surface", func() error {
			if _, err := relationshipOnly.RebuildResourceInventory(s.ctx, catalog.ResourceInventoryRebuildOptions{
				Project: ccProject, DatasetGeneration: ccGen, ResourceTypes: []string{"Patient"}, BatchSize: 2}); !errors.Is(err, ErrStoreLegNotAssembled) {
				return errors.New("relationshipOnly.RebuildResourceInventory: " + errText(err))
			}
			if _, err := relationshipOnly.ReadResourceInventory(s.ctx, catalog.ResourceInventoryOptions{Project: ccProject, DatasetGeneration: ccGen}); err == nil || !errors.Is(err, ErrStoreLegNotAssembled) {
				return errors.New("relationshipOnly.ReadResourceInventory: " + errText(err))
			}
			if _, err := relationshipOnly.DiscoverExistingAuthResourcePaths(s.ctx, catalog.AuthResourcePathOptions{Project: ccProject, AllGenerations: true}); !errors.Is(err, ErrStoreLegNotAssembled) {
				return errors.New("relationshipOnly.DiscoverExistingAuthResourcePaths: " + errText(err))
			}
			if err := relationshipOnly.WriteFieldProfile(s.ctx, fdScope(ccProject, ccGen, ccAuthPath, "Patient"), nil, 100); !errors.Is(err, ErrStoreLegNotAssembled) {
				return errors.New("relationshipOnly.WriteFieldProfile: " + errText(err))
			}
			if _, err := relationshipOnly.ReadFieldProfile(s.ctx, fdScope(ccProject, ccGen, ccAuthPath, "Patient")); !errors.Is(err, ErrStoreLegNotAssembled) {
				return errors.New("relationshipOnly.ReadFieldProfile: " + errText(err))
			}
			return nil
		}},
		{"aliases inherit the guard", func() error {
			// ReadRelationshipObservations delegates to
			// DiscoverRelationshipObservations; ReadResourceInventory to
			// DiscoverResourceInventory. They must refuse too, not panic.
			if _, err := inventoryOnly.ReadRelationshipObservations(s.ctx, catalog.RelationshipObservationOptions{Project: ccProject, DatasetGeneration: ccGen}); err == nil || !errors.Is(err, ErrStoreLegNotAssembled) {
				return errors.New("ReadRelationshipObservations: " + errText(err))
			}
			if _, err := profileOnly.ReadResourceInventory(s.ctx, catalog.ResourceInventoryOptions{Project: ccProject, DatasetGeneration: ccGen}); err == nil || !errors.Is(err, ErrStoreLegNotAssembled) {
				return errors.New("ReadResourceInventory: " + errText(err))
			}
			return nil
		}},
	}

	for _, p := range probes {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("%s: panic instead of typed refusal: %v", p.name, rec)
				}
			}()
			if err := p.call(); err != nil {
				t.Fatalf("%s: %v", p.name, err)
			}
		}()
	}

	// Zero boundary queries across every wrong-leg probe: the guard refuses
	// before any SQL, so counters must still be at their seeded-zero state.
	inventory.reset()
	profiles.reset()
	relationships.reset()
	for _, p := range probes {
		if err := p.call(); err != nil {
			t.Fatalf("%s (replay): %v", p.name, err)
		}
	}
	if inventory.scanSourceCalls+inventory.insertCountsCalls+inventory.publishBuildCalls+inventory.readBuildCalls+inventory.readCountSums != 0 {
		t.Fatalf("wrong-leg probes issued inventory queries: %+v", *inventory)
	}
	if profiles.upsertCalls+profiles.readRowsCalls+profiles.readFilteredCalls+profiles.readCoverageCalls+profiles.listDistinctCalls != 0 {
		t.Fatalf("wrong-leg probes issued profile queries: %+v", *profiles)
	}
	if relationships.scanCalls != 0 {
		t.Fatalf("wrong-leg probes issued relationship queries: %d", relationships.scanCalls)
	}
}

func errText(err error) string {
	if err == nil {
		return "expected typed refusal, got nil"
	}
	return err.Error()
}

// TestNewFullStoreRequiresEveryRepository pins the constructor refusal: a
// full store is only full when all three boundaries are present; a genuinely
// unassigned series must stay a nil port at the runtime level.
func TestNewFullStoreRequiresEveryRepository(t *testing.T) {
	s := newFieldProfileService(t)
	inventory, profiles, relationships := ccRepos(t, s)
	if _, err := NewFullStore(nil, profiles.real, relationships.real); err == nil {
		t.Fatal("missing inventory must be refused")
	}
	if _, err := NewFullStore(inventory.real, nil, relationships.real); err == nil {
		t.Fatal("missing profiles must be refused")
	}
	if _, err := NewFullStore(inventory.real, profiles.real, nil); err == nil {
		t.Fatal("missing relationships must be refused")
	}
	full, err := NewFullStore(inventory.real, profiles.real, relationships.real)
	if err != nil {
		s.fail("full store with all repositories", err)
	}
	if full == nil {
		t.Fatal("full store is nil")
	}
	// A constructed full store satisfies the reader contract statically.
	var _ catalog.CapabilityEvidenceReader = full
}
