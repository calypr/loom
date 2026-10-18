package recipe

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConstructionGroupMissingKeyPolicyDefaultsAndPersists(t *testing.T) {
	var legacy ConstructionGroup
	if err := json.Unmarshal([]byte(`{"constructionId":"group","keys":[{"inputColumnId":"input","outputColumnId":"output"}]}`), &legacy); err != nil {
		t.Fatalf("decode legacy group without missingKeyPolicy: %v", err)
	}
	if legacy.MissingKeyPolicy != ConstructionGroupMissingKeyGroup {
		t.Fatalf("legacy missingKeyPolicy = %q, want GROUP", legacy.MissingKeyPolicy)
	}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy group: %v", err)
	}
	if !strings.Contains(string(encoded), `"missingKeyPolicy":"GROUP"`) {
		t.Fatalf("legacy group did not persist explicit GROUP policy: %s", encoded)
	}

	input := map[string]StageColumn{"input": {ID: "input"}}
	output := map[string]StageColumn{"output": {ID: "output"}}
	if err := validateConstructionGroup(legacy, input, output, "steps[0]", map[string]bool{}, false); err != nil {
		t.Fatalf("validate legacy group: %v", err)
	}
	legacy.MissingKeyPolicy = "SILENT_DEFAULT"
	if err := validateConstructionGroup(legacy, input, output, "steps[0]", map[string]bool{}, false); err == nil || !strings.Contains(err.Error(), "missingKeyPolicy") {
		t.Fatalf("unsupported missingKeyPolicy error = %v", err)
	}
}

func TestConstructionAllowsEmptySourceOnlyForCompilerBoundGroupFieldSource(t *testing.T) {
	binding := &ConstructionGroupFieldSource{
		Kind: "BINDING", ValueKind: "CODING_IDENTITY", SourceStageID: ConstructionSourceProjectionID,
		SourceShapeID: "shape-1", SourceRole: "CURRENT_ITEM", OccurrenceID: "observation",
		OwnerResourceType: "Observation", ResourceType: "Coding", CanonicalPath: "valueCodeableConcept.coding[]",
	}
	construction := Construction{Version: 1, Steps: []ConstructionStep{{
		ID: "group", Inputs: []ConstructionInputRef{{Kind: ConstructionSourceProjectionInput}},
		Operation: ConstructionOperation{Kind: ConstructionGroupOp, Group: &ConstructionGroup{
			ConstructionID: "coding-group", MissingKeyPolicy: ConstructionGroupMissingKeyGroup,
			Keys: []ConstructionGroupKey{{FieldSource: binding, OutputColumnID: "coding_label"}},
		}},
		Outputs: []StageColumn{{ID: "coding_label", Name: "coding_label", Type: "string"}},
	}}}
	if err := construction.Validate(nil); err != nil {
		t.Fatalf("validate empty-source compiler-bound Group: %v", err)
	}

	unbound := construction
	unbound.Steps = append([]ConstructionStep(nil), construction.Steps...)
	unbound.Steps[0].Operation.Group = &ConstructionGroup{
		ConstructionID: "unbound-group", MissingKeyPolicy: ConstructionGroupMissingKeyGroup,
		Keys: []ConstructionGroupKey{{InputColumnID: "missing", OutputColumnID: "coding_label"}},
	}
	if err := unbound.Validate(nil); err == nil || !strings.Contains(err.Error(), "source projection must contain at least one column") {
		t.Fatalf("unbound empty-source Group error = %v", err)
	}
}

func TestConstructionRowValueFieldSourceIsGroupOnlyAndMutuallyExclusive(t *testing.T) {
	base := ConstructionRowValue{
		OutputColumnID: "values", Policy: ConstructionRowValueAll,
		FieldSource: &ConstructionGroupFieldSource{
			Kind: "BINDING", ValueKind: "SCALAR", SourceStageID: ConstructionSourceProjectionID,
			SourceShapeID: "shape-1", SourceRole: "ROOT", OccurrenceID: "base",
			OwnerResourceType: "Observation", ResourceType: "Observation", CandidateID: "note",
			CanonicalPath: "status",
		},
	}
	group := Construction{Version: 1, Steps: []ConstructionStep{{
		ID: "group", Inputs: []ConstructionInputRef{{Kind: ConstructionSourceProjectionInput}},
		Operation: ConstructionOperation{Kind: ConstructionGroupOp, Group: &ConstructionGroup{
			ConstructionID: "group-id", MissingKeyPolicy: ConstructionGroupMissingKeyGroup,
		}},
		Outputs:   []StageColumn{{ID: "values", Name: "values", Type: "array"}},
		RowValues: []ConstructionRowValue{base},
	}}}
	if err := group.Validate(nil); err != nil {
		t.Fatalf("validate bound Group row value with empty source schema: %v", err)
	}

	conflict := group
	conflict.Steps = append([]ConstructionStep(nil), group.Steps...)
	conflict.Steps[0].RowValues = []ConstructionRowValue{{
		InputColumnID: "status", FieldSource: base.FieldSource, OutputColumnID: "values", Policy: ConstructionRowValueAll,
	}}
	if err := conflict.Validate(nil); err == nil || !strings.Contains(err.Error(), "choose exactly one source representation") {
		t.Fatalf("conflicting row value source error = %v", err)
	}
}
