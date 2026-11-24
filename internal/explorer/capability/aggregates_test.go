package capability

import "testing"

func TestDeriveAggregateOperationCapabilitiesUsesTypeAndRowContext(t *testing.T) {
	byOperation := func(choices []AggregateOperationCapability, want AggregateOperation) AggregateOperationCapability {
		t.Helper()
		for _, choice := range choices {
			if choice.Operation == want {
				return choice
			}
		}
		t.Fatalf("operation %s was not returned: %#v", want, choices)
		return AggregateOperationCapability{}
	}

	numeric := DeriveAggregateOperationCapabilities(AggregateInput{LogicalType: "decimal", Cardinality: "MANY", HasField: true}, AggregateRowsRecords)
	for _, operation := range []AggregateOperation{AggregateSum, AggregateMean} {
		choice := byOperation(numeric, operation)
		if !choice.Supported || choice.RowContext != AggregateRowsRecords || choice.ResultLogicalType != "decimal" || choice.ResultCardinality != "OPTIONAL_ONE" ||
			choice.MissingValueSemantics != "null inputs are ignored; no non-null inputs returns null" ||
			choice.ContributorSemantics != "every non-null numeric input contributes once, grouped by its source resource" {
			t.Fatalf("%s numeric contract = %#v", operation, choice)
		}
	}
	if !byOperation(numeric, AggregateCount).Supported || !byOperation(numeric, AggregateExists).Supported {
		t.Fatal("existing count/existence operations must remain supported")
	}

	textChoices := DeriveAggregateOperationCapabilities(AggregateInput{LogicalType: "string", Cardinality: "OPTIONAL_ONE", HasField: true}, AggregateRowsRecords)
	for _, operation := range []AggregateOperation{AggregateSum, AggregateMean} {
		choice := byOperation(textChoices, operation)
		if choice.Supported || choice.ReasonCode != "NUMERIC_INPUT_REQUIRED" || choice.Reason == "" {
			t.Fatalf("%s non-numeric decision = %#v", operation, choice)
		}
	}

	groupChoices := DeriveAggregateOperationCapabilities(AggregateInput{LogicalType: "decimal", Cardinality: "MANY", HasField: true}, AggregateRowsGroups)
	for _, operation := range []AggregateOperation{AggregateSum, AggregateMean} {
		choice := byOperation(groupChoices, operation)
		if choice.Supported || choice.RowContext != AggregateRowsGroups || choice.ReasonCode != "ROW_CONTEXT_UNSUPPORTED" {
			t.Fatalf("%s groups decision = %#v", operation, choice)
		}
	}
	expanded := byOperation(DeriveAggregateOperationCapabilities(AggregateInput{LogicalType: "decimal", Cardinality: "MANY", HasField: true}, AggregateRowsExpanded), AggregateSum)
	if expanded.Supported || expanded.ReasonCode != "EXPANDED_AGGREGATE_SCOPE_UNDEFINED" || expanded.RowContext != AggregateRowsExpanded {
		t.Fatalf("expanded-row scope decision = %#v", expanded)
	}

	unknownCardinality := byOperation(DeriveAggregateOperationCapabilities(AggregateInput{LogicalType: "integer", HasField: true}, AggregateRowsRecords), AggregateSum)
	if unknownCardinality.Supported || unknownCardinality.ReasonCode != "INPUT_CARDINALITY_UNRESOLVED" {
		t.Fatalf("unresolved cardinality decision = %#v", unknownCardinality)
	}
	noField := byOperation(DeriveAggregateOperationCapabilities(AggregateInput{LogicalType: "decimal", Cardinality: "ONE"}, AggregateRowsRecords), AggregateSum)
	if noField.Supported || noField.ReasonCode != "FIELD_INPUT_REQUIRED" {
		t.Fatalf("missing selector decision = %#v", noField)
	}
}
