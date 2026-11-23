package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestConstructionCodedPivotCanonicalizesTableScalarLogicalTypes(t *testing.T) {
	tests := []struct {
		name          string
		logicalType   string
		valuePath     string
		wantKind      string
		wantValueType string
	}{
		{name: "date_time", logicalType: "date_time", valuePath: "effectiveDateTime", wantKind: "date_time", wantValueType: "STRING"},
		{name: "number", logicalType: "number", valuePath: "valueQuantity.value", wantKind: "decimal", wantValueType: "DECIMAL"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			compiled := compileDerivedTestOutput(t, codedPivotScalarOutput(test.logicalType, test.valuePath))
			sequence := compiled.Plan.StageSequence
			if sequence == nil || len(sequence.Stages) != 1 || sequence.Stages[0].Kind != ir.PhysicalStagePivotOp {
				t.Fatalf("stage sequence = %#v, want one typed Pivot stage", sequence)
			}
			pivot := sequence.Stages[0].GroupedPivot
			if pivot == nil || pivot.CodedCorrelation == nil {
				t.Fatalf("coded Pivot IR = %#v, want a typed coded correlation", pivot)
			}
			if pivot.ValueType != test.wantValueType {
				t.Fatalf("coded Pivot value type = %q, want %q", pivot.ValueType, test.wantValueType)
			}
			if pivot.CodedCorrelation.LogicalType != test.wantKind {
				t.Fatalf("coded Pivot correlation logical type = %q, want %q", pivot.CodedCorrelation.LogicalType, test.wantKind)
			}
			column, ok := outputSchemaColumn(compiled.OutputSchema, "systolic")
			if !ok {
				t.Fatal("compiled output schema has no systolic column")
			}
			if column.Kind != test.wantKind {
				t.Fatalf("compiled output kind = %q, want %q", column.Kind, test.wantKind)
			}
		})
	}
}

func codedPivotScalarOutput(logicalType, valuePath string) recipe.Output {
	output := codedPivotOutput()
	output.Name = "construction_coded_pivot_" + logicalType
	pivot := output.Construction.Steps[0].Operation.CodedPivot
	pivot.DuplicatePolicy = recipe.PivotDuplicateError
	pivot.Source.LogicalType = logicalType
	pivot.Source.ValuePath = valuePath
	pivot.Source.SourcePath = "Observation." + valuePath
	if logicalType == "date_time" {
		pivot.Source.OwningScope = ""
		pivot.Source.KeyPath = "code.coding[]"
		pivot.Source.ChoiceArms = nil
	}
	for index := range output.Construction.Steps[0].Outputs {
		output.Construction.Steps[0].Outputs[index].Type = logicalType
	}
	return output
}

func TestReconcileConstructionCodedPivotSchemaTypeHintsCaseInsensitive(t *testing.T) {
	decimalColumn := func(cardinality expression.Cardinality) []CompiledOutputColumn {
		return []CompiledOutputColumn{{Name: "value_column", Kind: "decimal", Cardinality: string(cardinality)}}
	}
	for _, declaredType := range []string{"", "INFER", "infer"} {
		declarations := []recipe.StageColumn{{ID: "value_column", Name: "value_column", Type: declaredType}}
		if _, err := reconcileConstructionCodedPivotSchema(declarations, decimalColumn(expression.RequiredOne), nil); err != nil {
			t.Errorf("coded pivot output type %q rejected: %v", declaredType, err)
		}
	}
	declarations := []recipe.StageColumn{{ID: "value_column", Name: "value_column", Type: "ARRAY"}}
	if _, err := reconcileConstructionCodedPivotSchema(declarations, decimalColumn(expression.Many), nil); err != nil {
		t.Errorf("coded pivot ARRAY output with many cardinality rejected: %v", err)
	}
	if _, err := reconcileConstructionCodedPivotSchema(declarations, decimalColumn(expression.RequiredOne), nil); err == nil ||
		!strings.Contains(err.Error(), "does not match source cardinality") {
		t.Errorf("coded pivot ARRAY output with one cardinality error = %v, want cardinality mismatch", err)
	}
}
