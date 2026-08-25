package catalog

import (
	"reflect"
	"testing"
)

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

func TestEvidenceStatusesDistinguishEmptyFailureAndTruncation(t *testing.T) {
	if got := newEvidenceStatus(true, true, false, 0); got != EvidenceEmpty {
		t.Fatalf("empty status = %q", got)
	}
	if got := newEvidenceStatus(false, false, false, 0); got != EvidenceUnavailable {
		t.Fatalf("failure status = %q", got)
	}
	if got := newEvidenceStatus(true, false, true, 1); got != EvidenceTruncated {
		t.Fatalf("truncated status = %q", got)
	}
}
