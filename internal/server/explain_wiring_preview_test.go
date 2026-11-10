package server

// Offline adapter-boundary tests for the Arango preview evidence and
// assessment translation (arch-backend-02). Shared execution consumes only
// the neutral shapes from internal/dataframe/execution; these tests pin the
// vendor decoding that used to live inside the engine so the relocation
// preserves the exact decision inputs.

import (
	"reflect"
	"testing"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func explainBool(value bool) *bool { return &value }

func TestPreviewCollectionScanEvidenceFromExplainSinglePlanRule(t *testing.T) {
	node := arangostore.ExplainNode{Type: "IndexNode", Collection: "Observation",
		IndexCoversProjections: explainBool(true), IndexCoversOutProjections: explainBool(true),
		Indexes: arangostore.ExplainIndexes{{Name: "loom_pivot_preview_a", Collection: "Observation"}}}
	want := dataframeexecution.PreviewCollectionScanEvidence{IndexNodes: []dataframeexecution.PreviewIndexNodeEvidence{
		{IndexNames: []string{"loom_pivot_preview_a"}, Coverage: dataframeexecution.PreviewIndexCoverageCovering},
	}}
	if got := PreviewCollectionScanEvidenceFromExplain(arangostore.ExplainResult{Plan: &arangostore.ExplainPlan{Nodes: []arangostore.ExplainNode{node}}}, "Observation"); !reflect.DeepEqual(got, want) {
		t.Fatalf("lone Plan evidence = %+v, want %+v", got, want)
	}
	if got := PreviewCollectionScanEvidenceFromExplain(arangostore.ExplainResult{Plans: []arangostore.ExplainPlan{{Nodes: []arangostore.ExplainNode{node}}}}, "Observation"); !reflect.DeepEqual(got, want) {
		t.Fatalf("lone alternative Plans evidence = %+v, want %+v", got, want)
	}
	// Ambiguous shapes certify nothing, matching the pre-change single-plan rule.
	for _, result := range []arangostore.ExplainResult{
		{},
		{Plan: &arangostore.ExplainPlan{Nodes: []arangostore.ExplainNode{node}}, Plans: []arangostore.ExplainPlan{{Nodes: []arangostore.ExplainNode{node}}}},
		{Plans: []arangostore.ExplainPlan{{Nodes: []arangostore.ExplainNode{node}}, {Nodes: []arangostore.ExplainNode{node}}}},
	} {
		if got := PreviewCollectionScanEvidenceFromExplain(result, "Observation"); !reflect.DeepEqual(got, dataframeexecution.PreviewCollectionScanEvidence{}) {
			t.Fatalf("ambiguous plan shape produced evidence: %+v from %+v", got, result)
		}
	}
}

func TestPreviewCollectionScanEvidenceFromExplainFiltersToQueriedCollection(t *testing.T) {
	result := arangostore.ExplainResult{Plan: &arangostore.ExplainPlan{Nodes: []arangostore.ExplainNode{
		{Type: "EnumerateCollectionNode", Collection: "Patient"},
		{Type: "IndexNode", Collection: "Patient", IndexCoversProjections: explainBool(false), IndexCoversOutProjections: explainBool(false)},
		{Type: "IndexNode", Collection: "Observation", IndexCoversProjections: explainBool(false), IndexCoversOutProjections: explainBool(false)},
	}}}
	got := PreviewCollectionScanEvidenceFromExplain(result, "Observation")
	if got.FullCollectionEnumeration {
		t.Fatal("an enumeration on another collection blocked the queried collection")
	}
	if len(got.IndexNodes) != 1 || got.IndexNodes[0].Coverage != dataframeexecution.PreviewIndexCoverageNotCovering {
		t.Fatalf("index nodes from another collection leaked in: %+v", got.IndexNodes)
	}
	blocked := PreviewCollectionScanEvidenceFromExplain(arangostore.ExplainResult{Plan: &arangostore.ExplainPlan{Nodes: []arangostore.ExplainNode{
		{Type: "IndexNode", Collection: "Observation", IndexCoversProjections: explainBool(true), IndexCoversOutProjections: explainBool(true)},
		{Type: "EnumerateCollectionNode", Collection: "Observation"},
	}}}, "Observation")
	if !blocked.FullCollectionEnumeration {
		t.Fatal("an enumeration on the queried collection must block every optimization")
	}
	// Node kinds without scan meaning are dropped; node order is preserved.
	ordered := PreviewCollectionScanEvidenceFromExplain(arangostore.ExplainResult{Plan: &arangostore.ExplainPlan{Nodes: []arangostore.ExplainNode{
		{Type: "FilterNode", Collection: "Observation"},
		{Type: "IndexNode", Collection: "Observation"},
		{Type: "CalculationNode", Collection: "Observation"},
		{Type: "IndexNode", Collection: "Observation"},
	}}}, "Observation")
	if len(ordered.IndexNodes) != 2 {
		t.Fatalf("scan-meaningless nodes leaked or order changed: %+v", ordered.IndexNodes)
	}
}

func TestPreviewCollectionScanEvidenceFromExplainCoverageTriState(t *testing.T) {
	cases := []struct {
		name string
		node arangostore.ExplainNode
		want dataframeexecution.PreviewIndexCoverage
	}{
		{"both true", arangostore.ExplainNode{IndexCoversProjections: explainBool(true), IndexCoversOutProjections: explainBool(true)}, dataframeexecution.PreviewIndexCoverageCovering},
		{"both false", arangostore.ExplainNode{IndexCoversProjections: explainBool(false), IndexCoversOutProjections: explainBool(false)}, dataframeexecution.PreviewIndexCoverageNotCovering},
		{"mixed true false", arangostore.ExplainNode{IndexCoversProjections: explainBool(true), IndexCoversOutProjections: explainBool(false)}, dataframeexecution.PreviewIndexCoverageUnknown},
		{"mixed false true", arangostore.ExplainNode{IndexCoversProjections: explainBool(false), IndexCoversOutProjections: explainBool(true)}, dataframeexecution.PreviewIndexCoverageUnknown},
		{"both missing", arangostore.ExplainNode{}, dataframeexecution.PreviewIndexCoverageUnknown},
		{"one missing", arangostore.ExplainNode{IndexCoversProjections: explainBool(false)}, dataframeexecution.PreviewIndexCoverageUnknown},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			node := test.node
			node.Type = "IndexNode"
			node.Collection = "Observation"
			got := PreviewCollectionScanEvidenceFromExplain(arangostore.ExplainResult{Plan: &arangostore.ExplainPlan{Nodes: []arangostore.ExplainNode{node}}}, "Observation")
			if len(got.IndexNodes) != 1 || got.IndexNodes[0].Coverage != test.want {
				t.Fatalf("coverage = %+v, want %v", got.IndexNodes, test.want)
			}
		})
	}
}

func TestPreviewCollectionScanEvidenceFromExplainFiltersIndexNamesToCollection(t *testing.T) {
	result := arangostore.ExplainResult{Plan: &arangostore.ExplainPlan{Nodes: []arangostore.ExplainNode{{
		Type: "IndexNode", Collection: "Observation",
		IndexCoversProjections: explainBool(true), IndexCoversOutProjections: explainBool(true),
		Indexes: arangostore.ExplainIndexes{
			{Name: "loom_pivot_preview_named", Collection: "Observation"},
			{Name: "loom_pivot_preview_unowned"},
			{Name: "loom_pivot_preview_foreign", Collection: "Patient"},
		},
	}}}}
	got := PreviewCollectionScanEvidenceFromExplain(result, "Observation")
	if len(got.IndexNodes) != 1 {
		t.Fatalf("index nodes = %+v", got.IndexNodes)
	}
	if want := []string{"loom_pivot_preview_named", "loom_pivot_preview_unowned"}; !reflect.DeepEqual(got.IndexNodes[0].IndexNames, want) {
		t.Fatalf("index names = %v, want %v", got.IndexNodes[0].IndexNames, want)
	}
}

func TestArangoAssessmentCopiesStoreAssessmentFieldForField(t *testing.T) {
	input := arangostore.ExplainAssessment{
		Plans: []arangostore.ExplainPlanEstimate{
			{Plan: 0, EstimatedCost: 12.5, EstimatedNrItems: 3},
			{Plan: 1, EstimatedCost: 100.25, EstimatedNrItems: 10},
		},
		FullCollectionScans: []arangostore.ExplainCollectionScan{{Plan: 1, NodeID: 4, Collection: "Patient"}},
		Indexes: []arangostore.ExplainIndexSummary{{
			Collection: "Patient", ID: "Patient/idx1", Name: "idx1", Type: "skiplist",
			Fields: []string{"id"}, Uses: []arangostore.ExplainIndexLocation{{Plan: 0, NodeID: 2}},
		}},
		Warnings:              []arangostore.ExplainWarning{{Code: 1004, Message: "full collection scan"}},
		AppliedOptimizerRules: []string{"move-calculations-up"},
	}
	got := arangoAssessment(input)
	want := dataframeexecution.ExplainAssessment{
		Plans: []dataframeexecution.ExplainPlanEstimate{
			{Plan: 0, EstimatedCost: 12.5, EstimatedNrItems: 3},
			{Plan: 1, EstimatedCost: 100.25, EstimatedNrItems: 10},
		},
		FullCollectionScans: []dataframeexecution.ExplainCollectionScan{{Plan: 1, NodeID: 4, Collection: "Patient"}},
		Indexes: []dataframeexecution.ExplainIndexSummary{{
			Collection: "Patient", ID: "Patient/idx1", Name: "idx1", Type: "skiplist",
			Fields: []string{"id"}, Uses: []dataframeexecution.ExplainIndexLocation{{Plan: 0, NodeID: 2}},
		}},
		Warnings:              []dataframeexecution.ExplainWarning{{Code: 1004, Message: "full collection scan"}},
		AppliedOptimizerRules: []string{"move-calculations-up"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("assessment translation drift: got %+v want %+v", got, want)
	}
	// The copy must be ownership-safe: mutating the store assessment must not
	// disturb the transport DTO.
	input.Plans[0].EstimatedCost = 999
	input.Indexes[0].Fields[0] = "mutated"
	input.Indexes[0].Uses[0].NodeID = 77
	if got.Plans[0].EstimatedCost != 12.5 || got.Indexes[0].Fields[0] != "id" || got.Indexes[0].Uses[0].NodeID != 2 {
		t.Fatalf("assessment shared mutable state with the store: %+v", got)
	}
	// Empty-list semantics are pinned to the pre-change copy verbatim: the
	// four make-built slices are always non-nil (JSON arrays), while the
	// append-built rules and fields keep nil for empty (JSON null).
	empty := arangoAssessment(arangostore.ExplainAssessment{})
	if empty.Plans == nil || empty.FullCollectionScans == nil || empty.Indexes == nil || empty.Warnings == nil {
		t.Fatalf("empty assessment lost the non-nil list shape: %+v", empty)
	}
	if empty.AppliedOptimizerRules != nil {
		t.Fatalf("empty rules must stay nil, matching the removed conversion: %v", empty.AppliedOptimizerRules)
	}
}

func TestPreviewCollectionIndexesFromStoreConvertsFieldForField(t *testing.T) {
	if got := previewCollectionIndexesFromStore(nil); got != nil {
		t.Fatalf("nil listing converted to %+v, want nil", got)
	}
	indexes := []arangostore.PreviewCoveringIndex{{
		Collection: "Observation", Name: "loom_pivot_preview_a",
		Fields: []string{"project", "dataset_generation"}, StoredValues: []string{"payload.status"},
	}}
	got := previewCollectionIndexesFromStore(indexes)
	want := []dataframeexecution.PreviewCollectionIndex{{
		Collection: "Observation", Name: "loom_pivot_preview_a",
		Fields: []string{"project", "dataset_generation"}, StoredValues: []string{"payload.status"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("listing conversion = %+v, want %+v", got, want)
	}
}
