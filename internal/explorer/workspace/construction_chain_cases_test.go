package workspace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
)

type constructionChainCase struct {
	ID               string `json:"id"`
	FirstKind        string `json:"firstKind"`
	SecondInput      string `json:"secondInput"`
	SecondColumn     string `json:"secondColumn"`
	Rename           *bool  `json:"rename"`
	Reorder          *bool  `json:"reorder"`
	ExpectedAccepted bool   `json:"expectedAccepted"`
}

func constructionForChainCase(value constructionChainCase) (Construction, []Column, error) {
	if value.ID == "" || value.Rename == nil || value.Reorder == nil {
		return Construction{}, nil, fmt.Errorf("missing case fields")
	}
	if value.FirstKind != "FILTER" && value.FirstKind != "DERIVE" {
		return Construction{}, nil, fmt.Errorf("unknown first operation")
	}
	if value.SecondColumn != "x" && value.SecondColumn != "d" && value.SecondColumn != "foreign" {
		return Construction{}, nil, fmt.Errorf("unknown column")
	}
	names := map[string]string{"x": "x", "d": "d", "foreign": "foreign"}
	firstID, secondID := "first_stage", "second_stage"
	if *value.Rename {
		names = map[string]string{"x": "root_value", "d": "computed_value", "foreign": "missing_value"}
		firstID, secondID = "stage_alpha", "stage_beta"
	}
	root := StageColumn{ID: names["x"], Name: names["x"], Label: "Root value", Type: "integer"}
	outputs := []StageColumn{root}
	first := ConstructionStep{ID: firstID, Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}}}
	if value.FirstKind == "DERIVE" {
		one := int64(1)
		outputs = append(outputs, StageColumn{ID: names["d"], Name: names["d"], Label: "Derived value", Type: "integer"})
		first.Operation = ConstructionOperation{Kind: ConstructionOperationDerive, Derive: &ConstructionDerive{ConstructionID: firstID, OutputColumnID: names["d"], Operation: ConstructionDerivedAdd, Left: ConstructionOperand{Kind: ConstructionColumnOperand, ColumnID: names["x"]}, Right: ConstructionOperand{Kind: ConstructionLiteralOperand, Literal: &ConstructionLiteral{Kind: ConstructionNumericInteger, Integer: &one}}, MissingInputPolicy: ConstructionMissingInputPropagateNull}}
	} else {
		first.Operation = ConstructionOperation{Kind: ConstructionOperationFilter, Filter: &ConstructionFilter{ColumnID: names["x"], Operator: ConstructionFilterExists}}
	}
	first.Outputs = append([]StageColumn(nil), outputs...)
	input := ConstructionInputRef{Kind: ConstructionInputStepOutput, StepID: firstID}
	switch value.SecondInput {
	case "PREVIOUS":
	case "SOURCE":
		input = ConstructionInputRef{Kind: ConstructionInputSourceProjection}
	case "SELF":
		input.StepID = secondID
	case "FOREIGN":
		input.StepID = "another_stage"
	case "REVISION":
		input = ConstructionInputRef{Kind: ConstructionInputTableRevision, TableID: "another_table", RevisionID: "revision_1", OutputID: "another_output"}
	default:
		return Construction{}, nil, fmt.Errorf("unknown second input")
	}
	if *value.Reorder {
		for left, right := 0, len(outputs)-1; left < right; left, right = left+1, right-1 {
			outputs[left], outputs[right] = outputs[right], outputs[left]
		}
	}
	second := ConstructionStep{ID: secondID, Inputs: []ConstructionInputRef{input}, Operation: ConstructionOperation{Kind: ConstructionOperationFilter, Filter: &ConstructionFilter{ColumnID: names[value.SecondColumn], Operator: ConstructionFilterExists}}, Outputs: outputs}
	columns := []Column{{ColumnID: names["x"], Column: names["x"], Label: "Root value", LogicalType: "integer", OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: SourceProjectID}}}
	return Construction{Version: ConstructionVersion, Steps: []ConstructionStep{first, second}}, columns, nil
}

func TestConstructionModelReplay(t *testing.T) {
	const scheduleFile = "testdata/construction_chain_cases.json"
	data, err := os.ReadFile(scheduleFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 4<<20 {
		t.Fatalf("schedule file %s exceeds 4 MiB", scheduleFile)
	}
	var input struct {
		SchemaVersion int                     `json:"schemaVersion"`
		Cases         []constructionChainCase `json:"cases"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		t.Fatal("trailing input")
	}
	if input.SchemaVersion != 1 || len(input.Cases) == 0 || len(input.Cases) > 200 {
		t.Fatal("invalid frozen schedule set")
	}
	seen := map[string]bool{}
	for _, row := range input.Cases {
		if seen[row.ID] {
			t.Fatal("duplicate schedule case")
		}
		seen[row.ID] = true
		construction, columns, err := constructionForChainCase(row)
		if err != nil {
			t.Fatal(err)
		}
		validationErr := construction.Validate(columns)
		if got := validationErr == nil; got != row.ExpectedAccepted {
			t.Errorf("schedule %s: accepted=%t, want %t (validation error: %v)", row.ID, got, row.ExpectedAccepted, validationErr)
		}
	}
}
