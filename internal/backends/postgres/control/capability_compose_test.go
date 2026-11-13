// Runtime composition regression (defect PG-PG-CAPABILITY-EVIDENCE-
// COMPOSITION-PANIC): the value the server hands the Explorer capability
// resolver (Runtime.Catalog) must complete all three
// catalog.ReadCapabilityEvidence reads without a nil dereference, and must
// be the same assembled Store behind every source-series catalog port.
package control

import (
	"errors"
	"reflect"
	"testing"

	catalogstore "github.com/calypr/loom/internal/backends/postgres/internal/catalog"
	"github.com/calypr/loom/internal/catalog"
)

// TestRuntimeCatalogSatisfiesCapabilityEvidenceReader is the regression the
// benchmark session asked for: on a runtime opened with the source series
// assigned (the block that composes the catalog ports),
// ReadCapabilityEvidence(runtime.Catalog, ...) never panics and every leg
// answers with a typed posture. The source-series block is the only place
// these ports are assembled, so the two-series harness (source +
// dataset_release) exercises the full composition law.
func TestRuntimeCatalogSatisfiesCapabilityEvidenceReader(t *testing.T) {
	s := newCTLService(t, true)
	runtime := s.runtime

	// One assembled value behind all four catalog ports.
	full := reflect.ValueOf(runtime.Catalog).Pointer()
	for name, port := range map[string]any{
		"Inventory":     runtime.Inventory,
		"FieldProfiles": runtime.FieldProfiles,
		"Relationships": runtime.Relationships,
	} {
		if port == nil || reflect.ValueOf(port).IsNil() {
			t.Fatalf("runtime.%s is nil with the source series assigned", name)
		}
		if reflect.ValueOf(port).Pointer() != full {
			t.Fatalf("runtime.%s is not the same Store as runtime.Catalog", name)
		}
	}

	unrestricted := catalog.ExplicitAuthResourcePathsUnrestricted(false)
	opts := catalog.CapabilityEvidenceOptions{
		Project: crProject, DatasetGeneration: crGenA,
		AuthResourcePaths:             []string{"/fhir/clinical"},
		AuthResourcePathsUnrestricted: unrestricted,
	}

	// Fresh migrated schema: every leg must answer typed, never panic.
	evidence, err := func() (evidence catalog.CapabilityEvidence, err error) {
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("ReadCapabilityEvidence(runtime.Catalog) panicked on the migrated schema: %v", rec)
			}
		}()
		return catalog.ReadCapabilityEvidence(s.ctx, runtime.Catalog, opts)
	}()
	if err == nil {
		t.Fatal("a migrated-but-unloaded generation must not report a complete aggregate")
	}
	// Inventory: no published build yet -> typed unavailable sentinel.
	if !errors.Is(err, catalogstore.ErrResourceInventoryBuildUnavailable) {
		t.Fatalf("inventory leg must refuse with the typed build-unavailable error, got %v", err)
	}
	if evidence.ResourceInventory.Available || evidence.ResourceInventory.Status != catalog.EvidenceUnavailable {
		t.Fatalf("inventory leg posture = available=%v status=%v, want typed unavailable",
			evidence.ResourceInventory.Available, evidence.ResourceInventory.Status)
	}
	// Relationships: the scan runs and legitimately answers empty.
	if !evidence.Relationships.Available || len(evidence.Relationships.Values) != 0 {
		t.Fatalf("relationship leg on an empty schema = available=%v values=%d, want available/empty",
			evidence.Relationships.Available, len(evidence.Relationships.Values))
	}
	// Field enrichment: never persisted -> typed unavailable (this is the
	// leg that panicked before the composition fix).
	if evidence.FieldEnrichment.Available || evidence.FieldEnrichment.Status != catalog.EvidenceUnavailable {
		t.Fatalf("enrichment leg posture = available=%v status=%v, want typed unavailable",
			evidence.FieldEnrichment.Available, evidence.FieldEnrichment.Status)
	}

	// Write profiles through the runtime's own port: the enrichment leg
	// must flip to available on the SAME Catalog value -- proof that the
	// capability reader carries the profile repository, not just refuses.
	s.writeProfiles(t)
	evidence, err = func() (evidence catalog.CapabilityEvidence, err error) {
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("ReadCapabilityEvidence(runtime.Catalog) panicked after seeding: %v", rec)
			}
		}()
		return catalog.ReadCapabilityEvidence(s.ctx, runtime.Catalog, opts)
	}()
	if err == nil || !errors.Is(err, catalogstore.ErrResourceInventoryBuildUnavailable) {
		t.Fatalf("only the inventory leg may still refuse after seeding, got %v", err)
	}
	if !evidence.FieldEnrichment.Available || len(evidence.FieldEnrichment.Values) == 0 {
		t.Fatalf("enrichment leg must answer through the composed store: available=%v values=%d",
			evidence.FieldEnrichment.Available, len(evidence.FieldEnrichment.Values))
	}
}

// TestRuntimeWithoutSourceSeriesKeepsNilPorts pins the other half of the
// posture law: a genuinely unassigned source series leaves the catalog
// ports nil for the server's typed configuration refusal -- the runtime
// must not fabricate a half-wired store, and consumers must never receive
// a value that panics.
func TestRuntimeWithoutSourceSeriesKeepsNilPorts(t *testing.T) {
	s := newCTLService(t, false)
	runtime := s.runtime
	if runtime.Catalog != nil {
		t.Fatal("Catalog must stay nil without the source series")
	}
	if runtime.Inventory != nil || runtime.FieldProfiles != nil || runtime.Relationships != nil {
		t.Fatal("narrow catalog ports must stay nil without the source series")
	}
}
