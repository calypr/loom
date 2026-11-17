package authoringv2

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

type formalProjectionCase struct {
	ID     string `json:"id"`
	Branch string `json:"branch"`
}

type formalProjectionResult struct {
	ID     string `json:"id"`
	Branch string `json:"branch"`
	Accept bool   `json:"accept"`
	Error  string `json:"error"`
}

var projectionBranches = []string{
	"pivot-accept", "pivot-not-consumed",
	"group-accept",
	"filter-accept", "filter-wrong-column", "filter-nil-payload",
	"derive-accept", "derive-not-operand",
	"unpivot-accept", "unpivot-non-melt", "unpivot-nil-payload",
	"owner-missing", "owner-stepid-empty", "default-combine-owner",
	"carry-cross-noncarrier", "carry-missing-typed-output",
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
		out = append(out, StageColumn{ID: id, Name: id, Label: "Col " + id, Type: "integer"})
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

func projectionBranchExpectation(branch string) (accepted bool, contains string) {
	switch branch {
	case "pivot-accept", "group-accept", "filter-accept", "derive-accept", "unpivot-accept":
		return true, ""
	case "pivot-not-consumed", "default-combine-owner":
		return false, "source projection must be consumed by its owner PIVOT step"
	case "filter-wrong-column", "filter-nil-payload":
		return false, "is not the column filtered by its owner FILTER step"
	case "derive-not-operand":
		return false, "is not a COLUMN operand of its owner DERIVE step"
	case "unpivot-non-melt":
		return false, "is not a melt input of its owner UNPIVOT step"
	case "unpivot-nil-payload":
		return false, "has no unpivot payload"
	case "owner-missing":
		return false, "does not exist"
	case "owner-stepid-empty":
		return false, "ownerStepId must be an exact step id"
	case "carry-cross-noncarrier":
		return false, "cannot cross GROUP before its owner PIVOT"
	case "carry-missing-typed-output":
		return false, "does not carry source projection"
	default:
		return false, "unknown projection branch"
	}
}

var projectionDirectEntryBranches = map[string]bool{
	"filter-nil-payload":    true,
	"unpivot-nil-payload":   true,
	"default-combine-owner": true,
	"owner-stepid-empty":    true,
}

func runProjectionCase(branch string) (formalProjectionResult, error) {
	construction, columns, err := projectionCaseConstruction(branch)
	if err != nil {
		return formalProjectionResult{}, err
	}
	projection := projectionCaseProjection(branch)
	construction.SourceProjections = []ConstructionSourceProjection{projection}
	result := formalProjectionResult{ID: "projection-" + branch, Branch: branch}
	runErr := construction.Validate(columns)
	if runErr == nil {
		result.Accept = true
		return result, nil
	}
	result.Error = runErr.Error()
	if projectionDirectEntryBranches[branch] {
		if directErr := validatePivotSourceProjection(construction, projection); directErr != nil {
			result.Error = directErr.Error()
		}
	}
	return result, nil
}

func TestProjectionOwnerBranchReplay(t *testing.T) {
	inputPath, outputPath := os.Getenv("LOOM_FORMAL_PROJECTION_INPUT"), os.Getenv("LOOM_FORMAL_PROJECTION_OUTPUT")
	if inputPath == "" && outputPath == "" {
		for _, branch := range projectionBranches {
			result, err := runProjectionCase(branch)
			if err != nil {
				t.Fatal(err)
			}
			accepted, contains := projectionBranchExpectation(branch)
			if result.Accept != accepted {
				t.Fatalf("branch %s accepted=%v want %v err=%v", branch, result.Accept, accepted, result.Error)
			}
			if !accepted && !strings.Contains(result.Error, contains) {
				t.Fatalf("branch %s error %q lacks %q", branch, result.Error, contains)
			}
			if accepted && result.Error != "" {
				t.Fatalf("branch %s unexpectedly rejected: %v", branch, result.Error)
			}
		}
		return
	}
	if inputPath == "" || outputPath == "" {
		t.Fatal("both formal projection input and output paths are required")
	}
	file, err := os.Open(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > 4<<20 {
		t.Fatal("formal projection input exceeds size limit or cannot be inspected")
	}
	var input struct {
		SchemaVersion int                    `json:"schemaVersion"`
		Cases         []formalProjectionCase `json:"cases"`
	}
	decoder := json.NewDecoder(io.LimitReader(file, 4<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		t.Fatal("trailing input")
	}
	known := map[string]bool{}
	for _, branch := range projectionBranches {
		known[branch] = true
	}
	if input.SchemaVersion != 1 || len(input.Cases) == 0 || len(input.Cases) > 1024 {
		t.Fatal("invalid formal projection case inventory")
	}
	seen, seenBranch := map[string]bool{}, map[string]bool{}
	results := make([]formalProjectionResult, 0, len(input.Cases))
	for _, row := range input.Cases {
		if seen[row.ID] || !known[row.Branch] || seenBranch[row.Branch] {
			t.Fatal("duplicate or unknown formal projection case")
		}
		seen[row.ID], seenBranch[row.Branch] = true, true
		result, err := runProjectionCase(row.Branch)
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, result)
	}
	data, err := json.Marshal(struct {
		SchemaVersion int                      `json:"schemaVersion"`
		Results       []formalProjectionResult `json:"results"`
	}{1, results})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, data, 0600); err != nil {
		t.Fatal(err)
	}
}
