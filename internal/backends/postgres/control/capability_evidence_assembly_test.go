package control

import (
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

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
