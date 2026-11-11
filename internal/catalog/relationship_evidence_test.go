package catalog

import (
	"sort"
	"testing"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

// traversalStub answers the generated-traversal registry with fixed
// found/error results so a single branch of the shared projection can be
// exercised with literals. Production always binds
// fhirschema.ResolveCompilerTraversal; this stub is test-only.
func traversalStub(found map[string]bool, errs map[string]error) func(fromType, edgeLabel, toType string) (fhirschema.CompilerTraversal, bool, error) {
	return func(fromType, edgeLabel, toType string) (fhirschema.CompilerTraversal, bool, error) {
		key := fromType + "|" + edgeLabel + "|" + toType
		if !found[key] {
			return fhirschema.CompilerTraversal{}, false, nil
		}
		return fhirschema.CompilerTraversal{FromType: fromType, EdgeLabel: edgeLabel, ToType: toType}, true, errs[key]
	}
}

// TestRelationshipEvidenceProjection pins the shared storage-to-Builder
// relationship policy with literal expectations on every branch: the
// inverse-first projection, the direct-only fallback, inverse-over-direct
// precedence, the unsupported-traversal diagnostic, the non-positive count
// diagnostic, and the merge-key/ordering/digest bytes both adapters depend
// on. The digest and ordering fixtures were captured from the pre-
// consolidation adapters (b604a307), so passing here means the shared
// implementation serves both backends byte-for-byte.
func TestRelationshipEvidenceProjection(t *testing.T) {
	t.Run("inverse projection is the preferred Builder view", func(t *testing.T) {
		observation, ok, diagnostic := ProjectRelationshipObservation("proj", "g1", "clinic/a", "Condition", "subject_Patient", "Patient", 3)
		if !ok {
			t.Fatalf("registry-known triple rejected: %+v", diagnostic)
		}
		want := RelationshipObservation{
			Project: "proj", DatasetGeneration: "g1", AuthResourcePath: "clinic/a",
			FromType: "Patient", Label: "subject_Patient", ToType: "Condition", EdgeCount: 3,
			StorageFromType: "Condition", StorageToType: "Patient", StorageDirection: "OUTBOUND",
			BuilderFromType: "Patient", BuilderToType: "Condition", BuilderDirection: "INBOUND",
		}
		if observation != want {
			t.Fatalf("observation = %+v, want %+v", observation, want)
		}
		wantDiagnostic := EvidenceDiagnostic{StorageFromType: "Condition", Label: "subject_Patient", StorageToType: "Patient", Count: 3}
		if diagnostic != wantDiagnostic {
			t.Fatalf("carried diagnostic = %+v, want %+v", diagnostic, wantDiagnostic)
		}
	})

	t.Run("direct-only traversal is a valid fallback projection", func(t *testing.T) {
		// No generated registry entry is direct-only today (verified by
		// census at b604a307), so this branch is exercised with a stub that
		// resolves only the direct orientation.
		found := map[string]bool{"Condition|subject_Patient|Patient": true}
		observation, ok, diagnostic := projectRelationshipObservation(traversalStub(found, nil), "proj", "g1", "clinic/a", "Condition", "subject_Patient", "Patient", 2)
		if !ok {
			t.Fatalf("direct-only triple rejected: %+v", diagnostic)
		}
		want := RelationshipObservation{
			Project: "proj", DatasetGeneration: "g1", AuthResourcePath: "clinic/a",
			FromType: "Condition", Label: "subject_Patient", ToType: "Patient", EdgeCount: 2,
			StorageFromType: "Condition", StorageToType: "Patient", StorageDirection: "OUTBOUND",
			BuilderFromType: "Condition", BuilderToType: "Patient", BuilderDirection: "OUTBOUND",
		}
		if observation != want {
			t.Fatalf("observation = %+v, want %+v", observation, want)
		}
	})

	t.Run("inverse wins when both orientations resolve", func(t *testing.T) {
		found := map[string]bool{
			"Condition|subject_Patient|Patient": true,
			"Patient|subject_Patient|Condition": true,
		}
		observation, ok, _ := projectRelationshipObservation(traversalStub(found, nil), "proj", "g1", "clinic/a", "Condition", "subject_Patient", "Patient", 1)
		if !ok {
			t.Fatal("both orientations resolve, projection must succeed")
		}
		if observation.BuilderDirection != "INBOUND" || observation.BuilderFromType != "Patient" || observation.BuilderToType != "Condition" {
			t.Fatalf("inverse-first selection broken: %+v", observation)
		}
	})

	t.Run("unsupported traversal is diagnosed with exact bytes", func(t *testing.T) {
		_, ok, diagnostic := ProjectRelationshipObservation("proj", "g1", "clinic/a", "Patient", "not_a_generated_label", "Practitioner", 7)
		if ok {
			t.Fatal("unsupported traversal accepted")
		}
		want := EvidenceDiagnostic{
			Code:            "UNSUPPORTED_GENERATED_TRAVERSAL",
			Message:         "relationship endpoints and label are not a supported generated traversal",
			StorageFromType: "Patient", Label: "not_a_generated_label", StorageToType: "Practitioner", Count: 7,
		}
		if diagnostic != want {
			t.Fatalf("diagnostic = %+v, want %+v", diagnostic, want)
		}
		// A resolved-but-erroring direct traversal falls through to the
		// same diagnostic (the original guards are `found && err == nil`).
		found := map[string]bool{"Condition|subject_Patient|Patient": true}
		errs := map[string]error{"Condition|subject_Patient|Patient": errStub}
		_, ok, diagnostic = projectRelationshipObservation(traversalStub(found, errs), "proj", "g1", "clinic/a", "Condition", "subject_Patient", "Patient", 4)
		if ok || diagnostic.Code != "UNSUPPORTED_GENERATED_TRAVERSAL" {
			t.Fatalf("erroring resolver: ok=%v diagnostic=%+v", ok, diagnostic)
		}
	})

	t.Run("non-positive count is diagnosed with exact bytes", func(t *testing.T) {
		for _, count := range []int64{0, -3} {
			observation, ok, diagnostic := ProjectRelationshipObservation("proj", "g1", "clinic/a", "Condition", "subject_Patient", "Patient", count)
			if ok {
				t.Fatalf("count %d accepted", count)
			}
			if observation != (RelationshipObservation{}) {
				t.Fatalf("rejected count %d returned an observation: %+v", count, observation)
			}
			want := EvidenceDiagnostic{
				Code:            "INVALID_RELATIONSHIP_COUNT",
				Message:         "relationship evidence count must be positive",
				StorageFromType: "Condition", Label: "subject_Patient", StorageToType: "Patient", Count: count,
			}
			if diagnostic != want {
				t.Fatalf("count %d diagnostic = %+v, want %+v", count, diagnostic, want)
			}
		}
	})

	t.Run("merge ordering and digest bytes match the pre-consolidation adapters", func(t *testing.T) {
		// Inputs captured from the b604a307 probes that drove both adapters'
		// merge loops. The repeated Condition/subject_Patient/Patient pair
		// (5 + 2) must merge to one INBOUND row of 7.
		type aggregate struct {
			project, generation, auth, storageFrom, label, storageTo string
			count                                                    int64
		}
		aggregates := []aggregate{
			{"p2", "g1", "clinic/b", "Condition", "subject_Patient", "Patient", 3},
			{"p1", "g1", "clinic/a", "BodyStructure", "patient", "Patient", 1},
			{"p1", "g1", "clinic/a", "Condition", "subject_Patient", "Patient", 5},
			{"p1", "g1", "clinic/a", "Condition", "subject_Patient", "Patient", 2},
			{"p1", "g2", "clinic/a", "Condition", "subject_Patient", "Patient", 3},
		}
		counts := make(map[RelationshipEvidenceKey]int64)
		for _, a := range aggregates {
			observation, ok, diagnostic := ProjectRelationshipObservation(a.project, a.generation, a.auth, a.storageFrom, a.label, a.storageTo, a.count)
			if !ok {
				t.Fatalf("aggregate %+v rejected: %+v", a, diagnostic)
			}
			counts[RelationshipEvidenceKeyFor(observation)] += observation.EdgeCount
		}
		values := make([]RelationshipObservation, 0, len(counts))
		for key, count := range counts {
			values = append(values, RelationshipObservationFromKey(key, count))
		}
		if len(values) != 4 {
			t.Fatalf("merged %d groups, want 4: %+v", len(values), values)
		}
		// Ordering is total over this fixture: sort.Slice's order must equal
		// the hand-sorted golden, verified via the strict comparator.
		sort.Slice(values, func(i, j int) bool { return RelationshipEvidenceLess(values[i], values[j]) })
		for i := 1; i < len(values); i++ {
			if !RelationshipEvidenceLess(values[i-1], values[i]) {
				t.Fatalf("order not strict at %d: %+v before %+v", i, values[i-1], values[i])
			}
		}
		wantOrder := []RelationshipObservation{
			{Project: "p1", DatasetGeneration: "g1", AuthResourcePath: "clinic/a", FromType: "Patient", Label: "patient", ToType: "BodyStructure", EdgeCount: 1, StorageFromType: "BodyStructure", StorageToType: "Patient", StorageDirection: "OUTBOUND", BuilderFromType: "Patient", BuilderToType: "BodyStructure", BuilderDirection: "INBOUND"},
			{Project: "p1", DatasetGeneration: "g1", AuthResourcePath: "clinic/a", FromType: "Patient", Label: "subject_Patient", ToType: "Condition", EdgeCount: 7, StorageFromType: "Condition", StorageToType: "Patient", StorageDirection: "OUTBOUND", BuilderFromType: "Patient", BuilderToType: "Condition", BuilderDirection: "INBOUND"},
			{Project: "p1", DatasetGeneration: "g2", AuthResourcePath: "clinic/a", FromType: "Patient", Label: "subject_Patient", ToType: "Condition", EdgeCount: 3, StorageFromType: "Condition", StorageToType: "Patient", StorageDirection: "OUTBOUND", BuilderFromType: "Patient", BuilderToType: "Condition", BuilderDirection: "INBOUND"},
			{Project: "p2", DatasetGeneration: "g1", AuthResourcePath: "clinic/b", FromType: "Patient", Label: "subject_Patient", ToType: "Condition", EdgeCount: 3, StorageFromType: "Condition", StorageToType: "Patient", StorageDirection: "OUTBOUND", BuilderFromType: "Patient", BuilderToType: "Condition", BuilderDirection: "INBOUND"},
		}
		for i := range wantOrder {
			if values[i] != wantOrder[i] {
				t.Fatalf("order[%d] = %+v, want %+v", i, values[i], wantOrder[i])
			}
		}
		digest, err := RelationshipObservationDigest(values)
		if err != nil {
			t.Fatalf("digest: %v", err)
		}
		// Golden captured from the b604a307 adapters over this exact fixture.
		if want := "sha256:f2391bc882ce18f46fc6877d8eeb40ffd84773597a9a3308ce2a10872783982e"; digest != want {
			t.Fatalf("digest = %q, want %q", digest, want)
		}
	})

	t.Run("comparator tie-breaks only on the count", func(t *testing.T) {
		// After a merge no two rows share all eight identity fields, so the
		// count tie-break is defensive parity with the pre-consolidation
		// comparators; pin it directly.
		base := RelationshipObservation{Project: "p", DatasetGeneration: "g", AuthResourcePath: "a", FromType: "Patient", Label: "subject_Patient", ToType: "Condition", StorageFromType: "Condition", StorageToType: "Patient", StorageDirection: "OUTBOUND", BuilderFromType: "Patient", BuilderToType: "Condition", BuilderDirection: "INBOUND"}
		low, high := base, base
		low.EdgeCount, high.EdgeCount = 5, 9
		if !RelationshipEvidenceLess(low, high) || RelationshipEvidenceLess(high, low) {
			t.Fatalf("count tie-break wrong: low<high=%v high<low=%v", RelationshipEvidenceLess(low, high), RelationshipEvidenceLess(high, low))
		}
		if RelationshipEvidenceLess(low, low) {
			t.Fatal("comparator is not strict on equal rows")
		}
		// First differing identity field decides, not the count.
		other := base
		other.Project = "o"
		other.EdgeCount = 1
		low.EdgeCount = 9
		if !RelationshipEvidenceLess(other, low) {
			t.Fatal("identity field must outrank the count")
		}
	})

	t.Run("evidence status ladder matches the pre-consolidation copies", func(t *testing.T) {
		for _, tc := range []struct {
			available, complete, truncated bool
			count                          int
			want                           EvidenceStatus
		}{
			{false, true, false, 5, EvidenceUnavailable},
			{false, false, true, 0, EvidenceUnavailable},
			{true, true, true, 3, EvidenceTruncated},
			{true, false, false, 3, EvidenceIncomplete},
			{true, true, false, 0, EvidenceEmpty},
			{true, true, false, 2, EvidenceAvailable},
		} {
			if got := EvidenceStatusFor(tc.available, tc.complete, tc.truncated, tc.count); got != tc.want {
				t.Fatalf("EvidenceStatusFor(%v,%v,%v,%d) = %v, want %v", tc.available, tc.complete, tc.truncated, tc.count, got, tc.want)
			}
		}
	})
}

var errStub = &stubError{}

type stubError struct{}

func (*stubError) Error() string { return "stub traversal error" }
