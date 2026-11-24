package semantic

import (
	"reflect"
	"testing"
)

func TestSourceSetAccumulatorProducesStableSortedPartitions(t *testing.T) {
	first := NewSourceSetAccumulator("Observation", true)
	for _, source := range [][2]string{{"obs-b", "Patient/b"}, {"obs-a", "Patient/a"}, {"obs-c", "Patient/a"}} {
		if err := first.AddSource(source[0], source[1]); err != nil {
			t.Fatal(err)
		}
	}
	got, err := first.Snapshot(true)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateResourceExpectation(got); err != nil {
		t.Fatalf("snapshot is not a valid expectation: %v", err)
	}
	if len(got.Partitions) != 2 || got.Partitions[0].AuthResourcePath != "Patient/a" || got.Partitions[1].AuthResourcePath != "Patient/b" {
		t.Fatalf("partitions are not sorted: %#v", got.Partitions)
	}

	second := NewSourceSetAccumulator("Observation", true)
	for _, source := range [][2]string{{"obs-c", "Patient/a"}, {"obs-a", "Patient/a"}, {"obs-b", "Patient/b"}} {
		if err := second.AddSource(source[0], source[1]); err != nil {
			t.Fatal(err)
		}
	}
	want, err := second.Snapshot(true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) || !SameResourceSourceSet(got, want) {
		t.Fatalf("source order changed the snapshot: got %#v, want %#v", got, want)
	}
}

func TestSourceSetAccumulatorRejectsMissingSources(t *testing.T) {
	accumulator := NewSourceSetAccumulator("Observation", false)
	if _, err := accumulator.Snapshot(true); err != nil {
		t.Fatalf("missing collection snapshot: %v", err)
	}
	if err := accumulator.AddSource("obs-a", "Patient/a"); err != ErrCoverageResourceExpectation {
		t.Fatalf("adding a source to a missing collection returned %v", err)
	}
}

func TestFinalizeBuildPreservesResourceStateAndCounts(t *testing.T) {
	input := []ResourceFinalizationInput{{
		ResourceType: "Observation", Found: true, State: SemanticCoverageBuildComplete,
		SourceEnumerationComplete: true, SourceSetDigest: "digest",
		ExpectedSourceCount: 3, MaterializedSourceCount: 3,
		SourcePartitionCount: 2, MaterializedPartitionCount: 2, StoredPartitionCount: 2, SummaryCount: 8,
	}}
	got := CoverageFinalizeBuild(input)
	want := BuildFinalization{State: SemanticCoverageBuildComplete, ExpectedSources: 3, MaterializedSources: 3, SummaryCount: 8}
	if got != want {
		t.Fatalf("finalization = %#v, want %#v", got, want)
	}
}

func TestFinalizeBuildMarksIncompleteResourcesPartial(t *testing.T) {
	got := CoverageFinalizeBuild([]ResourceFinalizationInput{
		{ResourceType: "ZObservation", Found: true, State: SemanticCoverageBuildFailed, Reason: "SOURCE_READ_FAILED"},
		{ResourceType: "Patient", Found: false},
	})
	want := BuildFinalization{State: SemanticCoverageBuildPartial, Reason: "RESOURCE_CERTIFICATES_INCOMPLETE"}
	if got != want {
		t.Fatalf("finalization = %#v, want %#v", got, want)
	}
}

func TestFinalizeBuildKeepsCountsWhenPartitionsAreIncomplete(t *testing.T) {
	resource := ResourceFinalizationInput{
		ResourceType: "Observation", Found: true, State: SemanticCoverageBuildComplete,
		SourceEnumerationComplete: true, SourceSetDigest: "digest",
		ExpectedSourceCount: 4, MaterializedSourceCount: 4,
		SourcePartitionCount: 2, MaterializedPartitionCount: 2, StoredPartitionCount: 1, SummaryCount: 6,
		IncompletePartitionReason: "SOURCE_PARTITION_INCOMPLETE",
	}
	got := CoverageFinalizeBuild([]ResourceFinalizationInput{resource})
	want := BuildFinalization{
		State: SemanticCoverageBuildPartial, Reason: "RESOURCE_PARTITION_SET_MISMATCH",
		ExpectedSources: 4, MaterializedSources: 4, SummaryCount: 6,
	}
	if got != want {
		t.Fatalf("finalization = %#v, want %#v", got, want)
	}
}

func TestFinalizeBuildChoosesReasonByResourceType(t *testing.T) {
	observation := ResourceFinalizationInput{
		ResourceType: "Observation", Found: true, State: SemanticCoverageBuildComplete,
		SourceEnumerationComplete: true, SourceSetDigest: "digest",
		ExpectedSourceCount: 1, MaterializedSourceCount: 1,
		SourcePartitionCount: 1, MaterializedPartitionCount: 1, StoredPartitionCount: 1,
		IncompletePartitionReason: "OBSERVATION_PARTIAL",
	}
	patient := observation
	patient.ResourceType = "Patient"
	patient.IncompletePartitionReason = "PATIENT_PARTIAL"
	for _, resources := range [][]ResourceFinalizationInput{{observation, patient}, {patient, observation}} {
		got := CoverageFinalizeBuild(resources)
		if got.State != SemanticCoverageBuildPartial || got.Reason != "OBSERVATION_PARTIAL" {
			t.Fatalf("finalization = %#v, want stable Observation reason", got)
		}
	}
}
