package recipe

import (
	"strings"
	"testing"
)

func TestConstructionCodedPivotLogicalTypeGate(t *testing.T) {
	construction := func(logicalType string) Construction {
		return Construction{Version: 1, Steps: []ConstructionStep{{
			ID: "pivot", Inputs: []ConstructionInputRef{{Kind: ConstructionSourceProjectionInput}},
			Operation: ConstructionOperation{Kind: ConstructionCodedPivotOp, CodedPivot: &ConstructionCodedPivot{
				ConstructionID: "pivot",
				Source: ConstructionCodedPivotSource{
					BindingID: "binding", ResourceType: "Observation", SourcePath: "Observation.value",
					KeyPath: "code.coding[]", ValuePath: "valueString", LogicalType: logicalType,
					RuleVersion: "1", SchemaVersion: 1, CandidateID: "candidate", NodeID: "node", FieldPath: "Observation.value",
				},
				Categories:      []ConstructionCodedPivotCategory{{System: "urn:example", Code: "value", OutputColumnID: "value_column"}},
				DuplicatePolicy: PivotDuplicateError, MissingCellPolicy: PivotMissingCellNull,
			}},
			Outputs: []StageColumn{{ID: "value_column", Name: "value_column", Label: "Value", Type: "string", Nullable: true}},
		}}}
	}

	for _, logicalType := range []string{"string", "date_time", "date-time", "datetime", "number", "decimal", "integer", "boolean", "code", "uuid", "date"} {
		if err := construction(logicalType).Validate(nil); err != nil {
			t.Errorf("coded pivot source logical type %q rejected: %v", logicalType, err)
		}
	}
	for _, logicalType := range []string{"money", "object", "quantity"} {
		if err := construction(logicalType).Validate(nil); err == nil || !strings.Contains(err.Error(), "not a supported table scalar") {
			t.Errorf("coded pivot source logical type %q error = %v, want unsupported table scalar", logicalType, err)
		}
	}
}

func TestConstructionCodedPivotSumRequiresNumericSourceLogicalType(t *testing.T) {
	withPolicy := func(logicalType, outputType string, policy PivotDuplicatePolicy) Construction {
		construction := Construction{Version: 1, Steps: []ConstructionStep{{
			ID: "pivot", Inputs: []ConstructionInputRef{{Kind: ConstructionSourceProjectionInput}},
			Operation: ConstructionOperation{Kind: ConstructionCodedPivotOp, CodedPivot: &ConstructionCodedPivot{
				ConstructionID: "pivot",
				Source: ConstructionCodedPivotSource{
					BindingID: "binding", ResourceType: "Observation", SourcePath: "Observation.value",
					KeyPath: "code.coding[]", ValuePath: "valueString", LogicalType: logicalType,
					RuleVersion: "1", SchemaVersion: 1, CandidateID: "candidate", NodeID: "node", FieldPath: "Observation.value",
				},
				Categories:      []ConstructionCodedPivotCategory{{System: "urn:example", Code: "value", OutputColumnID: "value_column"}},
				DuplicatePolicy: policy, MissingCellPolicy: PivotMissingCellNull,
			}},
			Outputs: []StageColumn{{ID: "value_column", Name: "value_column", Label: "Value", Type: outputType, Nullable: true}},
		}}}
		return construction
	}

	err := withPolicy("string", "string", PivotDuplicateSum).Validate(nil)
	if err == nil || !strings.Contains(err.Error(), "SUM requires an INTEGER or DECIMAL source logicalType") {
		t.Fatalf("coded pivot SUM with string source error = %v, want numeric source rejection", err)
	}
	for _, logicalType := range []string{"integer", "decimal", "number"} {
		if err := withPolicy(logicalType, "decimal", PivotDuplicateSum).Validate(nil); err != nil {
			t.Errorf("coded pivot SUM with %s source rejected: %v", logicalType, err)
		}
	}
}
