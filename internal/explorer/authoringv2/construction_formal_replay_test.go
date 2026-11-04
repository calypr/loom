package authoringv2

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
)

type formalChainCase struct {
	ID           string `json:"id"`
	FirstKind    string `json:"firstKind"`
	SecondInput  string `json:"secondInput"`
	SecondColumn string `json:"secondColumn"`
	Rename       *bool  `json:"rename"`
	Reorder      *bool  `json:"reorder"`
}

type formalChainResult struct {
	ID       string `json:"id"`
	Accepted bool   `json:"accepted"`
	Error    string `json:"error"`
}

func formalChainConstruction(value formalChainCase) (Construction, []Column, error) {
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
	inputPath, outputPath := os.Getenv("LOOM_FORMAL_CHAIN_INPUT"), os.Getenv("LOOM_FORMAL_CHAIN_OUTPUT")
	if inputPath == "" && outputPath == "" {
		no := false
		for _, row := range []struct {
			first, input, column string
			want                 bool
		}{{"FILTER", "PREVIOUS", "x", true}, {"FILTER", "SELF", "x", false}, {"DERIVE", "PREVIOUS", "d", true}} {
			construction, columns, err := formalChainConstruction(formalChainCase{ID: "smoke", FirstKind: row.first, SecondInput: row.input, SecondColumn: row.column, Rename: &no, Reorder: &no})
			if err != nil {
				t.Fatal(err)
			}
			if got := construction.Validate(columns) == nil; got != row.want {
				t.Fatalf("%+v accepted %v", row, got)
			}
		}
		return
	}
	if inputPath == "" || outputPath == "" {
		t.Fatal("both formal input and output paths are required")
	}
	file, err := os.Open(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > 4<<20 {
		t.Fatal("formal input exceeds size limit or cannot be inspected")
	}
	var input struct {
		SchemaVersion int               `json:"schemaVersion"`
		Cases         []formalChainCase `json:"cases"`
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
	if input.SchemaVersion != 1 || len(input.Cases) == 0 || len(input.Cases) > 1024 {
		t.Fatal("invalid formal case inventory")
	}
	seen := map[string]bool{}
	results := make([]formalChainResult, 0, len(input.Cases))
	for _, row := range input.Cases {
		if seen[row.ID] {
			t.Fatal("duplicate formal case")
		}
		seen[row.ID] = true
		construction, columns, err := formalChainConstruction(row)
		if err != nil {
			t.Fatal(err)
		}
		err = construction.Validate(columns)
		result := formalChainResult{ID: row.ID, Accepted: err == nil}
		if err != nil {
			result.Error = err.Error()
		}
		results = append(results, result)
	}
	data, err := json.Marshal(struct {
		SchemaVersion int                 `json:"schemaVersion"`
		Results       []formalChainResult `json:"results"`
	}{1, results})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, data, 0600); err != nil {
		t.Fatal(err)
	}
}
