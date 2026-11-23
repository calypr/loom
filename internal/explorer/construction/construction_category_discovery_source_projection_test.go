package construction

import (
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func TestPruneConstructionCategorySourceProjectionsKeepsRetainedOwnerSteps(t *testing.T) {
	projection := func(columnID, ownerStepID string) ws.ConstructionSourceProjection {
		return ws.ConstructionSourceProjection{
			ColumnID: columnID, OwnerStepID: ownerStepID, OccurrenceID: ws.RootOccurrenceID,
			FieldPath: columnID, FHIRType: "string", LogicalType: "string", Label: columnID,
		}
	}
	steps := []ws.ConstructionStep{
		{
			ID: "filter-step",
			Operation: ws.ConstructionOperation{
				Kind:   ws.ConstructionOperationFilter,
				Filter: &ws.ConstructionFilter{ColumnID: "filter_projection", Operator: ws.ConstructionFilterExists},
			},
			Outputs: []ws.StageColumn{{ID: "filter_projection", Name: "filter_projection", Label: "filter_projection", Type: "string"}},
		},
		{
			ID: "derive-step",
			Operation: ws.ConstructionOperation{
				Kind: ws.ConstructionOperationDerive,
				Derive: &ws.ConstructionDerive{
					ConstructionID:     "derive-step",
					OutputColumnID:     "derived",
					Operation:          ws.ConstructionDerivedAdd,
					Left:               ws.ConstructionOperand{Kind: ws.ConstructionColumnOperand, ColumnID: "derive_projection"},
					Right:              ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger, Integer: constructionCategoryDiscoveryInt(1)}},
					MissingInputPolicy: ws.ConstructionMissingInputPropagateNull,
				},
			},
			Outputs: []ws.StageColumn{
				{ID: "derive_projection", Name: "derive_projection", Label: "derive_projection", Type: "string"},
				{ID: "derived", Name: "derived", Label: "derived", Type: "integer"},
			},
		},
		{
			ID: "unpivot-step",
			Operation: ws.ConstructionOperation{
				Kind: ws.ConstructionOperationUnpivot,
				Unpivot: &ws.ConstructionUnpivot{
					ConstructionID: "unpivot-step",
					Inputs:         []ws.ConstructionUnpivotInput{{ColumnID: "unpivot_projection"}},
				},
			},
			Outputs: []ws.StageColumn{{ID: "unpivot_projection", Name: "unpivot_projection", Label: "unpivot_projection", Type: "string"}},
		},
		{
			ID: "pivot-step",
			Operation: ws.ConstructionOperation{
				Kind: ws.ConstructionOperationPivot,
				Pivot: &ws.ConstructionPivot{
					ConstructionID: "pivot-step", GroupKeyIDs: []string{"pivot_projection"},
				},
			},
			Outputs: []ws.StageColumn{{ID: "pivot_projection", Name: "pivot_projection", Label: "pivot_projection", Type: "string"}},
		},
	}
	projections := []ws.ConstructionSourceProjection{
		projection("filter_projection", "filter-step"),
		projection("derive_projection", "derive-step"),
		projection("unpivot_projection", "unpivot-step"),
		projection("pivot_projection", "pivot-step"),
		projection("legacy_projection", ""),
		projection("truncated_projection", "removed-step"),
	}

	pruned := pruneConstructionCategorySourceProjections(projections, steps)
	got := make(map[string]bool, len(pruned))
	for _, item := range pruned {
		got[item.ColumnID] = true
	}
	for _, columnID := range []string{"filter_projection", "derive_projection", "unpivot_projection", "pivot_projection", "legacy_projection"} {
		if !got[columnID] {
			t.Fatalf("pruned projections = %#v, want retained owner column %q", pruned, columnID)
		}
	}
	if got["truncated_projection"] {
		t.Fatalf("pruned projections = %#v, want the removed step's projection dropped", pruned)
	}
	if len(pruned) != 5 {
		t.Fatalf("pruned projections = %#v, want exactly 5 retained projections", pruned)
	}
}

func constructionCategoryDiscoveryInt(value int64) *int64 { return &value }
