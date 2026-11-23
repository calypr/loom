package workspace

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

type projectionBranchCase struct {
	ID            string `json:"id"`
	Branch        string `json:"branch"`
	Accept        bool   `json:"accept"`
	ErrorContains string `json:"error_contains"`
}

type projectionBranchResults struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Cases         []projectionBranchCase `json:"cases"`
}

func projectionFacts(columnID, owner string) ConstructionSourceProjection {
	return ConstructionSourceProjection{ColumnID: columnID, OwnerStepID: owner, OccurrenceID: RootOccurrenceID, FieldPath: "Observation.value", FHIRType: "string", LogicalType: "integer", Label: "Owned source"}
}

func projectionPublicColumns() []Column {
	return []Column{
		{ColumnID: "group_id", Column: "group_id", Label: "Group", LogicalType: "integer", OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: SourceProjectID}},
		{ColumnID: "category_id", Column: "category_id", Label: "Category", LogicalType: "integer", OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: SourceProjectID}},
		{ColumnID: "value_id", Column: "value_id", Label: "Value", LogicalType: "integer", OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: SourceProjectID}},
	}
}

func projectionColumns(ids ...string) []StageColumn {
	out := make([]StageColumn, 0, len(ids))
	for _, id := range ids {
		typ := "integer"
		if id == "category_id" {
			typ = "string"
		}
		out = append(out, StageColumn{ID: id, Name: id, Label: "Col " + id, Type: typ})
	}
	return out
}

func projectionFilterStep(id, inputStepID, columnID string, outputs []StageColumn, payload bool) ConstructionStep {
	step := ConstructionStep{ID: id, Outputs: outputs}
	if inputStepID == "SOURCE" {
		step.Inputs = []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}}
	} else if inputStepID != "" {
		step.Inputs = []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: inputStepID}}
	}
	step.Operation.Kind = ConstructionOperationFilter
	if payload {
		step.Operation.Filter = &ConstructionFilter{ColumnID: columnID, Operator: ConstructionFilterExists}
	}
	return step
}

func projectionDerivedStep(id, inputStepID, operandColumn string, outputs []StageColumn) ConstructionStep {
	one := int64(1)
	return ConstructionStep{ID: id, Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: inputStepID}}, Operation: ConstructionOperation{Kind: ConstructionOperationDerive, Derive: &ConstructionDerive{
		ConstructionID: id, OutputColumnID: "derived_id", Operation: ConstructionDerivedAdd,
		Left:               ConstructionOperand{Kind: ConstructionColumnOperand, ColumnID: operandColumn},
		Right:              ConstructionOperand{Kind: ConstructionLiteralOperand, Literal: &ConstructionLiteral{Kind: ConstructionNumericInteger, Integer: &one}},
		MissingInputPolicy: ConstructionMissingInputPropagateNull,
	}}, Outputs: outputs}
}

func projectionPivotStep(id, inputStepID string, outputs []StageColumn) ConstructionStep {
	return ConstructionStep{ID: id, Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: inputStepID}}, Operation: ConstructionOperation{Kind: ConstructionOperationPivot, Pivot: &ConstructionPivot{
		ConstructionID: id, GroupKeyIDs: []string{"group_id"}, CategoryColumnID: "category_id", ValueColumnID: "value_id",
		Categories:             []ConstructionPivotCategory{{Key: stringScalar("A"), OutputColumnID: "pivot_a"}},
		DuplicatePolicy:        ConstructionPivotDuplicateSum,
		MissingCellPolicy:      ConstructionPivotMissingNull,
		UnlistedCategoryPolicy: ConstructionPivotUnlistedError,
	}}, Outputs: outputs}
}

func projectionPivotStepWithValue(id, inputStepID, valueColumnID string, outputs []StageColumn) ConstructionStep {
	step := projectionPivotStep(id, inputStepID, outputs)
	step.Operation.Pivot.ValueColumnID = valueColumnID
	return step
}

func projectionCaseConstruction(branch string) (*Construction, []Column, error) {

	rootExtras := []string{}
	addRoot := func(ids ...string) { rootExtras = append(rootExtras, ids...) }
	finish := func(extraSteps ...ConstructionStep) (*Construction, []Column, error) {
		root := projectionFilterStep("root_filter", "SOURCE", "group_id", append(projectionColumns("group_id", "category_id", "value_id"), projectionColumns(rootExtras...)...), true)
		construction := &Construction{Version: ConstructionVersion, Steps: append([]ConstructionStep{root}, extraSteps...)}
		return construction, projectionPublicColumns(), nil
	}
	switch branch {
	case "pivot-accept":
		addRoot("proj_owned")
		return finish(projectionPivotStepWithValue("owner", "root_filter", "proj_owned", projectionColumns("group_id", "pivot_a")))
	case "pivot-not-consumed":
		return finish(func() ConstructionStep {
			step := projectionPivotStep("owner", "root_filter", projectionColumns("group_id", "pivot_a"))
			return step
		}())
	case "group-accept":
		addRoot("proj_owned")
		return finish(ConstructionStep{ID: "owner", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "root_filter"}}, Operation: ConstructionOperation{Kind: ConstructionOperationGroup, Group: &ConstructionGroup{
			ConstructionID: "owner", MissingKeyPolicy: ConstructionGroupMissingKeyGroup,
			Keys:       []ConstructionGroupKey{{InputColumnID: "proj_owned", OutputColumnID: "owned_key"}},
			Aggregates: []ConstructionGroupAggregate{{Operation: ConstructionGroupCountRows, OutputColumnID: "count_rows"}},
		}}, Outputs: projectionColumns("owned_key", "count_rows")})
	case "filter-accept":
		addRoot("proj_focused")
		return finish(projectionFilterStep("owner", "root_filter", "proj_focused", projectionColumns("group_id", "category_id", "value_id", "proj_focused"), true))
	case "filter-wrong-column":
		return finish(projectionFilterStep("owner", "root_filter", "value_id", projectionColumns("group_id", "category_id", "value_id"), true))
	case "filter-nil-payload":
		return finish(projectionFilterStep("owner", "root_filter", "", projectionColumns("group_id", "category_id", "value_id"), false))
	case "derive-accept":
		addRoot("proj_operand")
		return finish(projectionDerivedStep("owner", "root_filter", "proj_operand", append(projectionColumns("group_id", "category_id", "value_id", "proj_operand"), projectionColumns("derived_id")...)))
	case "derive-not-operand":
		return finish(projectionDerivedStep("owner", "root_filter", "value_id", append(projectionColumns("group_id", "category_id", "value_id"), projectionColumns("derived_id")...)))
	case "unpivot-accept":
		addRoot("pivot_a")
		return finish(ConstructionStep{ID: "owner", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "root_filter"}}, Operation: ConstructionOperation{Kind: ConstructionOperationUnpivot, Unpivot: &ConstructionUnpivot{
			ConstructionID:    "owner",
			Inputs:            []ConstructionUnpivotInput{{ColumnID: "pivot_a", Key: stringScalar("A")}},
			KeyOutputColumnID: "measure_key", ValueOutputColumnID: "measure_value", NullRowPolicy: ConstructionUnpivotPreserve,
		}}, Outputs: projectionColumns("group_id", "category_id", "value_id", "measure_key", "measure_value")})
	case "unpivot-non-melt":
		return finish(ConstructionStep{ID: "owner", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "root_filter"}}, Operation: ConstructionOperation{Kind: ConstructionOperationUnpivot, Unpivot: &ConstructionUnpivot{
			ConstructionID:    "owner",
			Inputs:            []ConstructionUnpivotInput{{ColumnID: "group_id", Key: stringScalar("A")}},
			KeyOutputColumnID: "measure_key", ValueOutputColumnID: "measure_value", NullRowPolicy: ConstructionUnpivotPreserve,
		}}, Outputs: projectionColumns("group_id", "category_id", "value_id", "measure_key", "measure_value")})
	case "unpivot-nil-payload":
		return finish(ConstructionStep{ID: "owner", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "root_filter"}}, Operation: ConstructionOperation{Kind: ConstructionOperationUnpivot}, Outputs: projectionColumns("group_id", "category_id", "value_id")})
	case "owner-missing":
		return finish(projectionPivotStep("owner", "root_filter", projectionColumns("group_id", "pivot_a")))
	case "owner-stepid-empty":
		return finish(projectionPivotStep("owner", "root_filter", projectionColumns("group_id", "pivot_a")))
	case "default-combine-owner":
		return finish(ConstructionStep{ID: "owner", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "root_filter"}}, Operation: ConstructionOperation{Kind: ConstructionOperationCombine}, Outputs: projectionColumns("group_id", "category_id", "value_id")})
	case "carry-cross-noncarrier":
		addRoot("proj_pass")
		gate := ConstructionStep{ID: "carry_gate", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "root_filter"}}, Operation: ConstructionOperation{Kind: ConstructionOperationGroup, Group: &ConstructionGroup{
			ConstructionID: "carry_gate", MissingKeyPolicy: ConstructionGroupMissingKeyGroup,
			Keys:       []ConstructionGroupKey{{InputColumnID: "group_id", OutputColumnID: "group_rows"}},
			Aggregates: []ConstructionGroupAggregate{{Operation: ConstructionGroupCountRows, OutputColumnID: "count_rows"}},
		}}, Outputs: projectionColumns("group_rows", "count_rows")}
		owner := projectionPivotStepWithValue("owner", "carry_gate", "proj_pass", projectionColumns("group_rows", "pivot_a"))
		owner.Operation.Pivot.GroupKeyIDs = []string{"group_rows"}
		return finish(gate, owner)
	case "carry-missing-typed-output":
		addRoot("proj_pass")
		carrier := projectionFilterStep("carrier", "root_filter", "group_id", projectionColumns("group_id", "category_id", "value_id"), true)
		owner := projectionPivotStepWithValue("owner", "carrier", "proj_pass", projectionColumns("group_id", "pivot_a"))
		return finish(carrier, owner)
	default:
		return nil, nil, fmt.Errorf("unknown projection branch %q", branch)
	}
}

func projectionCaseProjection(branch string) ConstructionSourceProjection {
	owner := "owner"
	if branch == "owner-missing" {
		owner = "ghost_owner"
	}
	column := map[string]string{
		"pivot-accept": "proj_owned", "pivot-not-consumed": "proj_outside",
		"group-accept":  "proj_owned",
		"filter-accept": "proj_focused", "filter-wrong-column": "proj_focused", "filter-nil-payload": "proj_focused",
		"derive-accept": "proj_operand", "derive-not-operand": "proj_operand",
		"unpivot-accept": "pivot_a", "unpivot-non-melt": "proj_outside", "unpivot-nil-payload": "pivot_a",
		"owner-missing": "proj_owned", "owner-stepid-empty": "proj_owned", "default-combine-owner": "proj_pass",
		"carry-cross-noncarrier": "proj_pass", "carry-missing-typed-output": "proj_pass",
	}[branch]
	if branch == "owner-stepid-empty" {
		return ConstructionSourceProjection{ColumnID: column, OccurrenceID: RootOccurrenceID, FieldPath: "Observation.value", FHIRType: "string", LogicalType: "integer", Label: "Owned source"}
	}
	return projectionFacts(column, owner)
}

var projectionDirectEntryBranches = map[string]bool{
	"filter-nil-payload":    true,
	"unpivot-nil-payload":   true,
	"default-combine-owner": true,
	"owner-stepid-empty":    true,
}

func runProjectionCase(branch string) (bool, string, error) {
	construction, columns, err := projectionCaseConstruction(branch)
	if err != nil {
		return false, "", err
	}
	projection := projectionCaseProjection(branch)
	construction.SourceProjections = []ConstructionSourceProjection{projection}
	runErr := construction.Validate(columns)
	if runErr == nil {
		return true, "", nil
	}
	message := runErr.Error()
	if projectionDirectEntryBranches[branch] {
		if directErr := validatePivotSourceProjection(construction, projection); directErr != nil {
			message = directErr.Error()
		}
	}
	return false, message, nil
}

func TestProjectionOwnerBranchReplay(t *testing.T) {
	data, err := os.ReadFile("testdata/projection_owner_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var input projectionBranchResults
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		t.Fatal("projection case data has trailing JSON")
	}
	if input.SchemaVersion != 1 || len(input.Cases) != 16 {
		t.Fatalf("projection cases schema/count = %d/%d, want 1/16", input.SchemaVersion, len(input.Cases))
	}
	seen := map[string]bool{}
	for _, row := range input.Cases {
		if row.ID == "" || row.Branch == "" || seen[row.Branch] {
			t.Fatalf("duplicate or empty projection case identity: %#v", row)
		}
		seen[row.Branch] = true
		t.Run(row.ID, func(t *testing.T) {
			accepted, message, err := runProjectionCase(row.Branch)
			if err != nil {
				t.Fatal(err)
			}
			if accepted != row.Accept {
				t.Fatalf("branch %s accepted=%v want %v; error=%q", row.Branch, accepted, row.Accept, message)
			}
			if row.Accept {
				if message != "" || row.ErrorContains != "" {
					t.Fatalf("accepted branch %s returned error %q or has error expectation %q", row.Branch, message, row.ErrorContains)
				}
				return
			}
			if row.ErrorContains == "" || !strings.Contains(message, row.ErrorContains) {
				t.Fatalf("rejected branch %s error %q lacks frozen text %q", row.Branch, message, row.ErrorContains)
			}
		})
	}
}
