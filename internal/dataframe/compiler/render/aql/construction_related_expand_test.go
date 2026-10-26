package aql

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func TestRelatedExpandEmptyErrorUsesStableConstructionCode(t *testing.T) {
	renderer := &physicalPlanRenderer{
		bindVars:       map[string]any{},
		reservedVars:   map[string]struct{}{},
		internalPrefix: "test_",
	}
	stage := ir.PhysicalConstructionStage{
		InputRowVariable:  "input_row",
		OutputRowVariable: "output_row",
		OutputProjections: []ir.PhysicalProjection{{
			Name: "related_id", Value: ir.PhysicalValue{Variable: "related_item", Path: []string{"_id"}},
		}},
		RelatedExpand: &ir.PhysicalStageRelatedExpand{
			ParentIdentityColumn:   "_key",
			ConstructionIDBindKey:  "construction_id",
			EmptyPolicy:            ir.PhysicalUnnestError,
			RelatedRecordsVariable: "related_records",
			IndexVariable:          "related_index",
			ItemVariable:           "related_item",
			IdentityVariable:       "related_identity",
			RelatedRecords: ir.PhysicalSubplan{
				Return: ir.PhysicalExpression{
					Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
					NullBehavior: ir.PhysicalPreserveNull,
					Value:        &ir.PhysicalValue{Variable: "target", Path: []string{"_id"}},
				},
			},
		},
	}

	lines, err := renderer.renderConstructionRelatedExpandStage(stage, "")
	if err != nil {
		t.Fatal(err)
	}
	query := strings.Join(lines, "\n")
	for _, want := range []string{
		string(dataframeerrors.CodeConstructionExpansionEmpty) + ": related expansion construction",
		"has no related records for row",
		"FILTER ASSERT(LENGTH(related_records) > 0,",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("related expansion query missing %q:\n%s", want, query)
		}
	}
}
