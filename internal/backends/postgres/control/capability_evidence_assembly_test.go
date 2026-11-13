// Runtime capability-evidence assembly witness.
//
// Runtime.Catalog is documented as the full catalog store: the value behind
// the narrow evidence ports AND the aggregate capability-evidence reader the
// Explorer capability resolver consumes through
// catalog.ReadCapabilityEvidence, which calls DiscoverResourceInventory,
// DiscoverRelationshipObservations, and DiscoverFieldEnrichment on ONE
// reader. Assembling it from a single-boundary store (inventory only) leaves
// the other two methods without a repository, and the resolver dereferences
// them on the first Builder capability read. These witnesses drive all three
// methods through the assembled runtime, so an assembly that drops a
// boundary fails here by name. The pg-fixture runner executes both cases
// by name under a real PostgreSQL (scripts/run-postgres-integration.mjs,
// capability-evidence-integration phase), where the zero-skip acceptance
// gate makes a skipped witness a failed run rather than a green lane.
package control

import (
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

// seedInventory builds the resource inventory through the runtime's own
// rebuild port, the production path. A migrated-but-empty generation has no
// build marker, and the inventory read rightly refuses to guess one; the
// witnesses below need a real build so that "available" means the boundary
// was answered, not merely that it did not panic.
func (s *ctlService) seedInventory(t *testing.T) {
	t.Helper()
	if s.runtime.Inventory == nil {
		t.Fatal("runtime assembled without the inventory rebuild port")
	}
	if _, err := s.runtime.Inventory.RebuildResourceInventory(s.ctx, catalog.ResourceInventoryRebuildOptions{
		Project: crProject, DatasetGeneration: crGenA,
		ResourceTypes: capabilityEvidenceTypes,
	}); err != nil {
		s.fail("rebuild resource inventory", err)
	}
}

// The evidence types under witness: the same explicit request the Builder
// capability lane makes for its document types. An empty request would ask
// for every concrete FHIR type, which the seeded build deliberately does not
// cover.
var capabilityEvidenceTypes = []string{"Patient", "Observation"}

func capabilityEvidenceOptions() catalog.CapabilityEvidenceOptions {
	unrestricted := true
	return catalog.CapabilityEvidenceOptions{
		Project:                       crProject,
		DatasetGeneration:             crGenA,
		AuthResourcePathsUnrestricted: &unrestricted,
		ResourceTypes:                 capabilityEvidenceTypes,
	}
}

// TestRuntimeCatalogServesAllThreeEvidenceSources reads the aggregate the way
// the capability resolver does. The field profiles arrive through the
// runtime's own write port (production path), so the enrichment read has
// evidence behind it; the inventory and relationship reads run against
// migrated-but-empty evidence, which is still a verdict, never a panic.
func TestRuntimeCatalogServesAllThreeEvidenceSources(t *testing.T) {
	s := newCTLService(t, true)
	s.writeProfiles(t)
	s.seedInventory(t)
	if s.runtime.Catalog == nil {
		t.Fatal("runtime assembled without the catalog capability-evidence reader")
	}

	opts := capabilityEvidenceOptions()
	evidence, err := catalog.ReadCapabilityEvidence(s.ctx, s.runtime.Catalog, opts)
	if err != nil {
		s.fail("read capability evidence", err)
	}
	if !evidence.ResourceInventory.Available || !evidence.Relationships.Available || !evidence.FieldEnrichment.Available {
		t.Errorf("aggregate evidence reported an unavailable source: inventory=%v relationships=%v enrichment=%v, want all available",
			evidence.ResourceInventory.Available, evidence.Relationships.Available, evidence.FieldEnrichment.Available)
	}

	// Each boundary individually, so a regression names the boundary that
	// lost its repository instead of only the aggregate.
	if _, err := s.runtime.Catalog.DiscoverResourceInventory(s.ctx, opts); err != nil {
		s.fail("discover resource inventory", err)
	}
	if _, err := s.runtime.Catalog.DiscoverRelationshipObservations(s.ctx, opts); err != nil {
		s.fail("discover relationship observations", err)
	}
	enrichment, err := s.runtime.Catalog.DiscoverFieldEnrichment(s.ctx, opts)
	if err != nil {
		s.fail("discover field enrichment", err)
	}
	if len(enrichment.Values) == 0 {
		t.Errorf("field enrichment served no observations after the runtime's own write path stored profiles for %s/%s, want the written evidence", crProject, crGenA)
	}
}

// TestRuntimeCatalogServesScopedEvidenceUnderAuthPaths is the restricted
// counterpart: a scoped read must be answered by all three boundaries too,
// because the resolver's restricted path passes authorized paths, not an
// unrestricted flag, and an early return there would hide a missing boundary
// from the unrestricted witness above only if it were the whole story.
func TestRuntimeCatalogServesScopedEvidenceUnderAuthPaths(t *testing.T) {
	s := newCTLService(t, true)
	s.writeProfiles(t)
	s.seedInventory(t)

	unrestricted := false
	opts := catalog.CapabilityEvidenceOptions{
		Project:                       crProject,
		DatasetGeneration:             crGenA,
		AuthResourcePathsUnrestricted: &unrestricted,
		AuthResourcePaths:             []string{"/fhir/clinical"},
		ResourceTypes:                 capabilityEvidenceTypes,
	}
	evidence, err := catalog.ReadCapabilityEvidence(s.ctx, s.runtime.Catalog, opts)
	if err != nil {
		s.fail("read scoped capability evidence", err)
	}
	if !evidence.Relationships.Available || !evidence.FieldEnrichment.Available {
		t.Errorf("scoped evidence reported an unavailable source: relationships=%v enrichment=%v, want both available",
			evidence.Relationships.Available, evidence.FieldEnrichment.Available)
	}
	if len(evidence.FieldEnrichment.Values) == 0 {
		t.Errorf("scoped field enrichment served no observations for /fhir/clinical, want the profiles written there")
	}
}
