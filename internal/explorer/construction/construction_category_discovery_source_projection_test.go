package construction

import (
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestPruneConstructionCategorySourceProjectionsKeepsRetainedOwnerSteps(t *testing.T) {
	projection := func(columnID, ownerStepID string) authoringv2.ConstructionSourceProjection {
		return authoringv2.ConstructionSourceProjection{
			ColumnID: columnID, OwnerStepID: ownerStepID, OccurrenceID: authoringv2.RootOccurrenceID,
			FieldPath: columnID, FHIRType: "string", LogicalType: "string", Label: columnID,
		}
	}
	steps := []authoringv2.ConstructionStep{
		{
			ID: "filter-step",
			Operation: authoringv2.ConstructionOperation{
				Kind:   authoringv2.ConstructionOperationFilter,
				Filter: &authoringv2.ConstructionFilter{ColumnID: "filter_projection", Operator: authoringv2.ConstructionFilterExists},
			},
			Outputs: []authoringv2.StageColumn{{ID: "filter_projection", Name: "filter_projection", Label: "filter_projection", Type: "string"}},
		},
		{
			ID: "derive-step",
			Operation: authoringv2.ConstructionOperation{
				Kind: authoringv2.ConstructionOperationDerive,
				Derive: &authoringv2.ConstructionDerive{
					ConstructionID:     "derive-step",
					OutputColumnID:     "derived",
					Operation:          authoringv2.ConstructionDerivedAdd,
					Left:               authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionColumnOperand, ColumnID: "derive_projection"},
					Right:              authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionLiteralOperand, Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericInteger, Integer: constructionCategoryDiscoveryInt(1)}},
					MissingInputPolicy: authoringv2.ConstructionMissingInputPropagateNull,
				},
			},
			Outputs: []authoringv2.StageColumn{
				{ID: "derive_projection", Name: "derive_projection", Label: "derive_projection", Type: "string"},
				{ID: "derived", Name: "derived", Label: "derived", Type: "integer"},
			},
		},
		{
			ID: "unpivot-step",
			Operation: authoringv2.ConstructionOperation{
				Kind: authoringv2.ConstructionOperationUnpivot,
				Unpivot: &authoringv2.ConstructionUnpivot{
					ConstructionID: "unpivot-step",
					Inputs:         []authoringv2.ConstructionUnpivotInput{{ColumnID: "unpivot_projection"}},
				},
			},
			Outputs: []authoringv2.StageColumn{{ID: "unpivot_projection", Name: "unpivot_projection", Label: "unpivot_projection", Type: "string"}},
		},
		{
			ID: "pivot-step",
			Operation: authoringv2.ConstructionOperation{
				Kind: authoringv2.ConstructionOperationPivot,
				Pivot: &authoringv2.ConstructionPivot{
					ConstructionID: "pivot-step", GroupKeyIDs: []string{"pivot_projection"},
				},
			},
			Outputs: []authoringv2.StageColumn{{ID: "pivot_projection", Name: "pivot_projection", Label: "pivot_projection", Type: "string"}},
		},
	}
	projections := []authoringv2.ConstructionSourceProjection{
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
