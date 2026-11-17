package aql

import (
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func TestSourceGroupChildrenPreserveReferencesAndMetadata(t *testing.T) {
	expressions := []ir.PhysicalExpression{
		{Kind: ir.PhysicalExtractExpression, Extract: &ir.PhysicalExtract{Source: ir.PhysicalValue{Variable: "selector_source"}, Prepared: &ir.PhysicalPreparedReference{SetVariable: "prepared_extract"}}},
		{Kind: ir.PhysicalPivotExpression, Pivot: &ir.PhysicalPivotMap{Source: ir.PhysicalValue{Variable: "pivot_source"}, PreparedKey: &ir.PhysicalPreparedReference{SetVariable: "prepared_key"}, PreparedValue: &ir.PhysicalPreparedReference{SetVariable: "prepared_value"}}},
		{Kind: ir.PhysicalObjectLookupExpression, ObjectLookup: &ir.PhysicalObjectLookup{ObjectVariable: "lookup_object"}},
		{Kind: ir.PhysicalObjectKeysExpression, ObjectKeys: &ir.PhysicalObjectKeys{ObjectVariable: "keys_object"}},
		{Kind: ir.PhysicalSubplanExpression, Subplan: &ir.PhysicalSubplan{Captures: []string{"captured"}, Operations: []ir.PhysicalOperation{{Kind: ir.PhysicalSetOp, Set: &ir.PhysicalSet{SourceSetVariable: "subset_source", Prepared: &ir.PhysicalPreparedSet{SourceSetVariable: "prepared_source"}}}}, Return: ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Value: &ir.PhysicalValue{Variable: "return_source"}}}},
	}
	operation := ir.PhysicalOperation{Kind: ir.PhysicalExpressionLetOp, ExpressionLet: &ir.PhysicalExpressionLet{Expression: ir.PhysicalExpression{Kind: ir.PhysicalCallExpression, Call: &ir.PhysicalCall{Args: expressions}}}}
	got, want := map[string]struct{}{}, map[string]struct{}{}
	addSourceGroupOperationReferences(got, operation)
	for _, name := range []string{"selector_source", "prepared_extract", "pivot_source", "prepared_key", "prepared_value", "lookup_object", "keys_object", "captured", "subset_source", "prepared_source", "return_source"} {
		want[name] = struct{}{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("references=%v, want exact dependencies %v", got, want)
	}
}
