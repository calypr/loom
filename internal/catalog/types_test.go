package catalog

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestResourceInventoryBuildIdentityKeepsLegacyDigest(t *testing.T) {
	counts := []ResourceInventoryCount{
		{Project: "project", DatasetGeneration: "generation", AuthResourcePath: "/scope/b", ResourceType: "Patient", DocumentCount: 23},
		{Project: "project", DatasetGeneration: "generation", ResourceType: "Observation", DocumentCount: 17},
	}
	build, _, err := NewResourceInventoryBuild("project", "generation", []string{"Patient", "Observation"}, counts)
	if err != nil {
		t.Fatal(err)
	}
	coverageDigest, err := ResourceInventoryCoverageDigest([]string{"Patient", "Observation"})
	if err != nil {
		t.Fatal(err)
	}
	type legacyCountIdentity struct {
		Project           string `json:"project"`
		DatasetGeneration string `json:"dataset_generation"`
		AuthResourcePath  string `json:"auth_resource_path,omitempty"`
		ResourceType      string `json:"resource_type"`
		DocumentCount     int64  `json:"document_count"`
	}
	legacy, err := canonicalDigest(struct {
		SchemaVersion  string                `json:"schema_version"`
		Project        string                `json:"project"`
		Generation     string                `json:"dataset_generation"`
		CoverageDigest string                `json:"coverage_digest"`
		Counts         []legacyCountIdentity `json:"counts"`
	}{
		SchemaVersion: "resource-inventory-build/v1", Project: "project", Generation: "generation", CoverageDigest: coverageDigest,
		Counts: []legacyCountIdentity{
			{Project: "project", DatasetGeneration: "generation", ResourceType: "Observation", DocumentCount: 17},
			{Project: "project", DatasetGeneration: "generation", AuthResourcePath: "/scope/b", ResourceType: "Patient", DocumentCount: 23},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if build.BuildID != legacy {
		t.Fatalf("resource inventory build identity changed: got %s, want %s", build.BuildID, legacy)
	}

	actual, err := json.Marshal([]ResourceInventoryObservation{
		{Project: "project", DatasetGeneration: "generation", ResourceType: "Observation", DocumentCount: 17},
		{Project: "project", DatasetGeneration: "generation", AuthResourcePath: "/scope/b", ResourceType: "Patient", DocumentCount: 23},
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyBytes, err := json.Marshal([]legacyCountIdentity{
		{Project: "project", DatasetGeneration: "generation", ResourceType: "Observation", DocumentCount: 17},
		{Project: "project", DatasetGeneration: "generation", AuthResourcePath: "/scope/b", ResourceType: "Patient", DocumentCount: 23},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(legacyBytes) {
		t.Fatalf("resource inventory count JSON changed: got %s, want %s", actual, legacyBytes)
	}
}

func TestCapabilityEvidenceDigestsAreOrderIndependentAndScoped(t *testing.T) {
	records := []ResourceInventoryObservation{
		{Project: "p", DatasetGeneration: "g", AuthResourcePath: "b", ResourceType: "Patient", DocumentCount: 2},
		{Project: "p", DatasetGeneration: "g", AuthResourcePath: "a", ResourceType: "Condition", DocumentCount: 1},
	}
	first, err := ResourceInventoryDigest(records)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ResourceInventoryDigest([]ResourceInventoryObservation{records[1], records[0]})
	if err != nil || first != second {
		t.Fatalf("digest depends on record order: %q != %q (err=%v)", first, second, err)
	}
	other, err := ResourceInventoryDigest([]ResourceInventoryObservation{{Project: "p", DatasetGeneration: "g", AuthResourcePath: "other", ResourceType: "Patient", DocumentCount: 2}})
	if err != nil || first == other {
		t.Fatalf("digest does not distinguish authorization scope: %q == %q (err=%v)", first, other, err)
	}
}

func TestFieldEnrichmentDigestIncludesExactCanonicalRecord(t *testing.T) {
	base := FieldEnrichmentObservation{Project: "p", DatasetGeneration: "g", AuthResourcePath: "scope", ResourceType: "Patient", Path: "id", Kind: "scalar", DocCount: 2, DistinctValues: []string{"a", "b"}}
	one, err := FieldEnrichmentDigest([]FieldEnrichmentObservation{base})
	if err != nil {
		t.Fatal(err)
	}
	base.DistinctValues = []string{"a", "c"}
	two, err := FieldEnrichmentDigest([]FieldEnrichmentObservation{base})
	if err != nil || one == two {
		t.Fatalf("digest omitted exact enrichment values: %q == %q (err=%v)", one, two, err)
	}
	if got := cloneFieldEnrichment([]FieldEnrichmentObservation{base}); !reflect.DeepEqual(got[0].DistinctValues, []string{"a", "c"}) {
		t.Fatalf("field enrichment clone lost values: %#v", got)
	}
}

func TestFieldEnrichmentDigestIncludesArrayWidth(t *testing.T) {
	base := FieldEnrichmentObservation{Project: "p", DatasetGeneration: "g", ResourceType: "Patient", Path: "name[]", Kind: "array", MaxItems: 2}
	one, err := FieldEnrichmentDigest([]FieldEnrichmentObservation{base})
	if err != nil {
		t.Fatal(err)
	}
	base.MaxItems = 3
	two, err := FieldEnrichmentDigest([]FieldEnrichmentObservation{base})
	if err != nil || one == two {
		t.Fatalf("digest omitted exact array width: %q == %q (err=%v)", one, two, err)
	}
}
