package compiler

import (
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func TestPreviewLivenessPreservesCaptureBoundaryAndMetadata(t *testing.T) {
	expression := ir.PhysicalExpression{Kind: ir.PhysicalCallExpression, Call: &ir.PhysicalCall{Args: []ir.PhysicalExpression{
		{Kind: ir.PhysicalSubplanExpression, Subplan: &ir.PhysicalSubplan{
			Captures: []string{"outer"},
			Return:   ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Value: &ir.PhysicalValue{Variable: "local"}},
		}},
		{Kind: ir.PhysicalExtractExpression, Extract: &ir.PhysicalExtract{
			Source:    ir.PhysicalValue{Variable: "source"},
			Fallbacks: []ir.PhysicalSelectorFallback{{Source: ir.PhysicalValue{Variable: "fallback"}}},
			Prepared:  &ir.PhysicalPreparedReference{SetVariable: "prepared"},
		}},
		{Kind: ir.PhysicalObjectLookupExpression, ObjectLookup: &ir.PhysicalObjectLookup{ObjectVariable: "object"}},
	}}}
	uses := map[string]bool{}
	if !collectPreviewExpressionUses(expression, uses) {
		t.Fatal("supported nested expression was not analyzable")
	}
	want := map[string]bool{"outer": true, "source": true, "fallback": true, "prepared": true, "object": true}
	if !reflect.DeepEqual(uses, want) {
		t.Fatalf("outer dependencies=%v, want %v", uses, want)
	}
}

func TestPreviewLivenessDeclinesUnknownNestedExpression(t *testing.T) {
	expression := ir.PhysicalExpression{Kind: ir.PhysicalCallExpression, Call: &ir.PhysicalCall{Args: []ir.PhysicalExpression{{Kind: "FUTURE_EXPRESSION"}}}}
	if collectPreviewExpressionUses(expression, map[string]bool{}) {
		t.Fatal("unknown nested expression was treated as safe for pruning")
	}
}
