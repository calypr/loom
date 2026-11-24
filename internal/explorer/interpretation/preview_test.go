package interpretation

import (
	"testing"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer/viewer"
)

func TestCompareInterpretationCandidateRowsCountsTransitionsAndPreservesPublicValues(t *testing.T) {
	base := PreviewRows{ReceiptID: "base", Rows: map[string]map[string]any{
		"row-1": {"feature": "old"},
		"row-2": {"feature": nil},
		"row-3": {"feature": "known"},
		"row-4": {"feature": "same"},
	}}
	candidate := PreviewRows{ReceiptID: "candidate", Rows: map[string]map[string]any{
		"row-1": {"feature": "new"},
		"row-2": {"feature": "resolved"},
		"row-3": {"feature": nil},
		"row-4": {"feature": "same"},
	}}
	emissions := []viewer.EmittedColumn{{EmissionID: "em-feature", OutputID: "patients", PublicColumn: "feature", AuthoredColumns: []string{"meaning"}}}
	result := CompareCandidateRows(base, candidate, emissions, emissions, PreviewInterpretationCandidateRequest{OutputID: "patients", Column: "meaning", RevisionID: "revision-b"}, 10)
	if result.BaseReceiptID != "base" || result.CandidateReceiptID != "candidate" || result.Completeness != CandidatePreviewComplete {
		t.Fatalf("result identity/completeness = %#v", result)
	}
	if result.Counts != (CandidatePreviewCounts{Compared: 4, Changed: 3, Resolved: 1, Unresolved: 1}) {
		t.Fatalf("counts = %#v", result.Counts)
	}
	states := map[string]CandidatePreviewState{}
	for _, sample := range result.Samples {
		states[sample.RowID] = sample.State
	}
	if states["row-1"] != CandidatePreviewChanged || states["row-2"] != CandidatePreviewResolved || states["row-3"] != CandidatePreviewUnresolved || states["row-4"] != CandidatePreviewUnchanged {
		t.Fatalf("states = %#v", states)
	}
	if result.Samples[0].Before["feature"] != "old" || result.Samples[0].After["feature"] != "new" {
		t.Fatalf("public values were not retained: %#v", result.Samples[0])
	}
}

func TestPreviewSummaryCompletenessRequiresNaturalExhaustion(t *testing.T) {
	if !PreviewSummaryComplete(dataframeexecution.PreviewSummary{Complete: true}) {
		t.Fatal("complete summary was rejected")
	}
	if PreviewSummaryComplete(dataframeexecution.PreviewSummary{Complete: false, Truncated: true}) {
		t.Fatal("truncated summary was treated as complete")
	}
}
