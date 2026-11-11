package server

// Offline adapter-boundary tests for the Arango preview evidence and
// assessment translation (arch-backend-02). Shared execution consumes only
// the neutral shapes from internal/dataframe/execution; these tests pin the
// vendor decoding that used to live inside the engine so the relocation
// preserves the exact decision inputs.

import (
	"reflect"
	"testing"

	arangofactory "github.com/calypr/loom/internal/backends/arango"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
)

func explainBool(value bool) *bool { return &value }

func TestPreviewCollectionScanEvidenceFromExplainSinglePlanRule(t *testing.T) {
	node := arangofactory.ExplainNode{Type: "IndexNode", Collection: "Observation",
		IndexCoversProjections: explainBool(true), IndexCoversOutProjections: explainBool(true),
		Indexes: arangofactory.ExplainIndexes{{Name: "loom_pivot_preview_a", Collection: "Observation"}}}
	want := dataframeexecution.PreviewCollectionScanEvidence{IndexNodes: []dataframeexecution.PreviewIndexNodeEvidence{
		{IndexNames: []string{"loom_pivot_preview_a"}, Coverage: dataframeexecution.PreviewIndexCoverageCovering},
	}}
	if got := PreviewCollectionScanEvidenceFromExplain(arangofactory.ExplainResult{Plan: &arangofactory.ExplainPlan{Nodes: []arangofactory.ExplainNode{node}}}, "Observation"); !reflect.DeepEqual(got, want) {
		t.Fatalf("lone Plan evidence = %+v, want %+v", got, want)
	}
	if got := PreviewCollectionScanEvidenceFromExplain(arangofactory.ExplainResult{Plans: []arangofactory.ExplainPlan{{Nodes: []arangofactory.ExplainNode{node}}}}, "Observation"); !reflect.DeepEqual(got, want) {
		t.Fatalf("lone alternative Plans evidence = %+v, want %+v", got, want)
	}
	// Ambiguous shapes certify nothing, matching the pre-change single-plan rule.
	for _, result := range []arangofactory.ExplainResult{
		{},
		{Plan: &arangofactory.ExplainPlan{Nodes: []arangofactory.ExplainNode{node}}, Plans: []arangofactory.ExplainPlan{{Nodes: []arangofactory.ExplainNode{node}}}},
		{Plans: []arangofactory.ExplainPlan{{Nodes: []arangofactory.ExplainNode{node}}, {Nodes: []arangofactory.ExplainNode{node}}}},
	} {
		if got := PreviewCollectionScanEvidenceFromExplain(result, "Observation"); !reflect.DeepEqual(got, dataframeexecution.PreviewCollectionScanEvidence{}) {
			t.Fatalf("ambiguous plan shape produced evidence: %+v from %+v", got, result)
		}
	}
}

func TestPreviewCollectionScanEvidenceFromExplainFiltersToQueriedCollection(t *testing.T) {
	result := arangofactory.ExplainResult{Plan: &arangofactory.ExplainPlan{Nodes: []arangofactory.ExplainNode{
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
	blocked := PreviewCollectionScanEvidenceFromExplain(arangofactory.ExplainResult{Plan: &arangofactory.ExplainPlan{Nodes: []arangofactory.ExplainNode{
		{Type: "IndexNode", Collection: "Observation", IndexCoversProjections: explainBool(true), IndexCoversOutProjections: explainBool(true)},
		{Type: "EnumerateCollectionNode", Collection: "Observation"},
	}}}, "Observation")
	if !blocked.FullCollectionEnumeration {
		t.Fatal("an enumeration on the queried collection must block every optimization")
	}
	// Node kinds without scan meaning are dropped; node order is preserved.
	ordered := PreviewCollectionScanEvidenceFromExplain(arangofactory.ExplainResult{Plan: &arangofactory.ExplainPlan{Nodes: []arangofactory.ExplainNode{
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
		node arangofactory.ExplainNode
		want dataframeexecution.PreviewIndexCoverage
	}{
		{"both true", arangofactory.ExplainNode{IndexCoversProjections: explainBool(true), IndexCoversOutProjections: explainBool(true)}, dataframeexecution.PreviewIndexCoverageCovering},
		{"both false", arangofactory.ExplainNode{IndexCoversProjections: explainBool(false), IndexCoversOutProjections: explainBool(false)}, dataframeexecution.PreviewIndexCoverageNotCovering},
		{"mixed true false", arangofactory.ExplainNode{IndexCoversProjections: explainBool(true), IndexCoversOutProjections: explainBool(false)}, dataframeexecution.PreviewIndexCoverageUnknown},
		{"mixed false true", arangofactory.ExplainNode{IndexCoversProjections: explainBool(false), IndexCoversOutProjections: explainBool(true)}, dataframeexecution.PreviewIndexCoverageUnknown},
		{"both missing", arangofactory.ExplainNode{}, dataframeexecution.PreviewIndexCoverageUnknown},
		{"one missing", arangofactory.ExplainNode{IndexCoversProjections: explainBool(false)}, dataframeexecution.PreviewIndexCoverageUnknown},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			node := test.node
			node.Type = "IndexNode"
			node.Collection = "Observation"
			got := PreviewCollectionScanEvidenceFromExplain(arangofactory.ExplainResult{Plan: &arangofactory.ExplainPlan{Nodes: []arangofactory.ExplainNode{node}}}, "Observation")
			if len(got.IndexNodes) != 1 || got.IndexNodes[0].Coverage != test.want {
				t.Fatalf("coverage = %+v, want %v", got.IndexNodes, test.want)
			}
		})
	}
}

func TestPreviewCollectionScanEvidenceFromExplainFiltersIndexNamesToCollection(t *testing.T) {
	result := arangofactory.ExplainResult{Plan: &arangofactory.ExplainPlan{Nodes: []arangofactory.ExplainNode{{
		Type: "IndexNode", Collection: "Observation",
		IndexCoversProjections: explainBool(true), IndexCoversOutProjections: explainBool(true),
		Indexes: arangofactory.ExplainIndexes{
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
	input := arangofactory.ExplainAssessment{
		Plans: []arangofactory.ExplainPlanEstimate{
			{Plan: 0, EstimatedCost: 12.5, EstimatedNrItems: 3},
			{Plan: 1, EstimatedCost: 100.25, EstimatedNrItems: 10},
		},
		FullCollectionScans: []arangofactory.ExplainCollectionScan{{Plan: 1, NodeID: 4, Collection: "Patient"}},
		Indexes: []arangofactory.ExplainIndexSummary{{
			Collection: "Patient", ID: "Patient/idx1", Name: "idx1", Type: "skiplist",
			Fields: []string{"id"}, Uses: []arangofactory.ExplainIndexLocation{{Plan: 0, NodeID: 2}},
		}},
		Warnings:              []arangofactory.ExplainWarning{{Code: 1004, Message: "full collection scan"}},
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
	empty := arangoAssessment(arangofactory.ExplainAssessment{})
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
	indexes := []arangofactory.PreviewCoveringIndex{{
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

// TestPreviewCollectionScanEvidenceFromExplainUnknownNodeKindNeverImpliesEnumeration
// pins the decoder half of the tri-state guarantee (verdict F-1): the
// full-collection-enumeration fact has exactly one source — the adapter's
// EnumerateCollectionNode decode. Every other node kind, named or foreign,
// must leave evidence silent so UNKNOWN can never degrade into a blocking
// enumeration (or out of it). Assertion strength matters here: an
// enumeration-bit check per node kind is the minimum that dies when a
// catch-all default starts setting the bit.
func TestPreviewCollectionScanEvidenceFromExplainUnknownNodeKindNeverImpliesEnumeration(t *testing.T) {
	for _, nodeType := range []string{
		"FilterNode", "CalculationNode", "SortNode", "LimitNode",
		"MaterializedDocumentSourceNode", "RemoteExecutionNode",
		"EnumerationNode", "IndexNodeAlias", "FetchNode", "WindowNode",
		"unknown-kind", "",
	} {
		got := PreviewCollectionScanEvidenceFromExplain(arangofactory.ExplainResult{Plan: &arangofactory.ExplainPlan{
			Nodes: []arangofactory.ExplainNode{{Type: nodeType, Collection: "Observation"}},
		}}, "Observation")
		if got.FullCollectionEnumeration {
			t.Fatalf("node kind %q implicitly certified a full collection enumeration: %+v", nodeType, got)
		}
		if len(got.IndexNodes) != 0 {
			t.Fatalf("node kind %q produced index-node evidence: %+v", nodeType, got.IndexNodes)
		}
	}
	// Exhaustiveness posture: the two recognized kinds keep their exclusive
	// meanings, and unknown kinds riding alongside stay inert — the
	// enumeration bit moves only with EnumerateCollectionNode.
	mixed := PreviewCollectionScanEvidenceFromExplain(arangofactory.ExplainResult{Plan: &arangofactory.ExplainPlan{
		Nodes: []arangofactory.ExplainNode{
			{Type: "FilterNode", Collection: "Observation"},
			{Type: "ForeignScanNode", Collection: "Observation"},
			{Type: "IndexNode", Collection: "Observation", IndexCoversProjections: explainBool(true), IndexCoversOutProjections: explainBool(true)},
		},
	}}, "Observation")
	if mixed.FullCollectionEnumeration || len(mixed.IndexNodes) != 1 || mixed.IndexNodes[0].Coverage != dataframeexecution.PreviewIndexCoverageCovering {
		t.Fatalf("unknown kinds rode into the evidence alongside a covering index node: %+v", mixed)
	}
	enumerated := PreviewCollectionScanEvidenceFromExplain(arangofactory.ExplainResult{Plan: &arangofactory.ExplainPlan{
		Nodes: []arangofactory.ExplainNode{
			{Type: "WindowNode", Collection: "Observation"},
			{Type: "EnumerateCollectionNode", Collection: "Observation"},
		},
	}}, "Observation")
	if !enumerated.FullCollectionEnumeration {
		t.Fatalf("the only certified enumeration source stopped setting the fact: %+v", enumerated)
	}
}
