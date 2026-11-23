package compilation

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestCompileMapsAuthoredConstructionDerivesIntoRecipeContract(t *testing.T) {
	integer := int64(0)
	decimal := float64(2.75)
	document := exactRecodeDocument(nil)
	document.Columns[0].ColumnID = "status_id"
	document.Columns[1].ColumnID = "untouched_id"
	source := []ws.StageColumn{
		{ID: "status_id", Name: "status", Label: "Status", Type: "string"},
		{ID: "untouched_id", Name: "untouched", Label: "Untouched", Type: "string"},
	}
	scaled := append(append([]ws.StageColumn(nil), source...), ws.StageColumn{ID: "scaled_status_id", Name: "scaled_status", Label: "Scaled status", Type: "decimal"})
	final := append(append([]ws.StageColumn(nil), scaled...), ws.StageColumn{ID: "present_id", Name: "present", Label: "Present", Type: "decimal"})
	document.Construction = &ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{

			{
				ID:     "calc_scaled_status",
				Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
				Operation: ws.ConstructionOperation{
					Kind: ws.ConstructionOperationDerive,
					Derive: &ws.ConstructionDerive{

						ConstructionID: "calc_scaled_status",
						OutputColumnID: "scaled_status_id",
						Operation:      ws.ConstructionDerivedMultiply,

						Left: ws.ConstructionOperand{Kind: ws.ConstructionColumnOperand, ColumnID: "status_id"},
						Right: ws.ConstructionOperand{
							Kind:    ws.ConstructionLiteralOperand,
							Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericDecimal, Decimal: &decimal},
						},
						MissingInputPolicy: ws.ConstructionMissingInputPropagateNull,
					},
				},
				Outputs: scaled,
			},

			{
				ID:     "calc_present",
				Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "calc_scaled_status"}},
				Operation: ws.ConstructionOperation{
					Kind: ws.ConstructionOperationDerive,
					Derive: &ws.ConstructionDerive{

						ConstructionID: "calc_present",
						OutputColumnID: "present_id",
						Operation:      ws.ConstructionDerivedDivide,

						Left: ws.ConstructionOperand{
							Kind:    ws.ConstructionLiteralOperand,
							Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger, Integer: &integer},
						},
						Right:                ws.ConstructionOperand{Kind: ws.ConstructionColumnOperand, ColumnID: "scaled_status_id"},
						MissingInputPolicy:   ws.ConstructionMissingInputError,
						DivisionByZeroPolicy: ws.ConstructionDivisionByZeroNull,
					},
				},
				Outputs: final,
			},
		},
	}
	compiled, err := Compile(context.Background(), "project", "explorer", document, fixtureSnapshotForProject("project"))
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Bundle.Outputs) != 1 || compiled.Bundle.Outputs[0].Construction == nil {
		t.Fatalf("compiled outputs=%#v", compiled.Bundle.Outputs)
	}
	steps := compiled.Bundle.Outputs[0].Construction.Steps
	want := []recipe.ConstructionDerive{

		{
			ConstructionID: "calc_scaled_status",
			OutputColumnID: "scaled_status_id",
			Operation:      recipe.DerivedMultiply,
			Left:           recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "status_id"},
			Right: recipe.ConstructionOperand{
				Kind:    recipe.DerivedLiteralOperand,
				Literal: &recipe.DerivedLiteral{Kind: recipe.NumericDecimal, Decimal: &decimal},
			},
			MissingInputPolicy: recipe.MissingInputPropagateNull,
		},

		{
			ConstructionID: "calc_present",
			OutputColumnID: "present_id",
			Operation:      recipe.DerivedDivide,
			Left: recipe.ConstructionOperand{
				Kind:    recipe.DerivedLiteralOperand,
				Literal: &recipe.DerivedLiteral{Kind: recipe.NumericInteger, Integer: &integer},
			},
			Right:                recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "scaled_status_id"},
			MissingInputPolicy:   recipe.MissingInputError,
			DivisionByZeroPolicy: recipe.DivisionByZeroNull,
		},
	}
	if len(steps) != len(want) {
		t.Fatalf("derive step count=%d", len(steps))
	}
	for i, expected := range want {
		if steps[i].ID != expected.ConstructionID || steps[i].Operation.Derive == nil || !reflect.DeepEqual(*steps[i].Operation.Derive, expected) {
			t.Fatalf("recipe derive %d=%#v want %#v", i, steps[i], expected)
		}
	}
	outputs := steps[1].Outputs
	if outputs[2].Name != "scaled_status" || outputs[2].Label != "Scaled status" || outputs[3].Name != "present" || outputs[3].Label != "Present" {
		t.Fatalf("derived public names/labels changed: %#v", outputs)
	}
	if len(compiled.Bundle.Outputs[0].DerivedColumns) != 0 || compiled.Bundle.Outputs[0].ArchiveRetiredReshape != nil {
		t.Fatal("current compiler emitted retired post-source fields")
	}
}

func TestCompileRejectsNonNumericConstructionLiteral(t *testing.T) {
	document := exactRecodeDocument(nil)
	for i := range document.Columns {
		document.Columns[i].ColumnID = document.Columns[i].Column
	}
	document.Construction = &ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			{
				ID:     "calc_invalid",
				Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
				Operation: ws.ConstructionOperation{
					Kind: ws.ConstructionOperationDerive,
					Derive: &ws.ConstructionDerive{
						ConstructionID: "calc_invalid",
						OutputColumnID: "invalid",
						Operation:      ws.ConstructionDerivedAdd,
						Left: ws.ConstructionOperand{
							Kind:    ws.ConstructionLiteralOperand,
							Literal: &ws.ConstructionLiteral{Kind: "STRING"},
						},
						Right: ws.ConstructionOperand{
							Kind:    ws.ConstructionLiteralOperand,
							Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger, Integer: int64Pointer(1)},
						},
						MissingInputPolicy: ws.ConstructionMissingInputError,
					},
				},
				Outputs: []ws.StageColumn{
					{ID: "status", Name: "status", Label: "Status", Type: "string"},
					{ID: "untouched", Name: "untouched", Label: "Untouched", Type: "string"},
					{ID: "invalid", Name: "invalid", Label: "Invalid", Type: "decimal"},
				},
			},
		},
	}
	_, err := Compile(context.Background(), "project", "explorer", document, fixtureSnapshotForProject("project"))
	var compileError *Error
	if !errors.As(err, &compileError) || compileError.Code != "INVALID_DOCUMENT" || !strings.Contains(compileError.Message, "unsupported literal kind") {
		t.Fatalf("compile error=%v, want rejection of nonnumeric typed construction literal", err)
	}
}
func TestCompileRejectsOpaqueRetiredShapeBeforeTranslation(t *testing.T) {
	document := exactRecodeDocument(nil)
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["tableShape"] = json.RawMessage(`{"derived":[{"constructionId":"old","output":{"column":"old_value","label":"Old"},"operation":"ADD","left":{"kind":"COLUMN","column":"status"},"right":{"kind":"LITERAL","literal":{"kind":"INTEGER","integer":1}},"missingInputPolicy":"ERROR"}]}`)
	raw, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	_, err = Compile(context.Background(), "project", "explorer", document, fixtureSnapshotForProject("project"))
	var compileError *Error
	if !errors.As(err, &compileError) || compileError.Code != "INVALID_DOCUMENT" || !strings.Contains(compileError.Message, "stored-data migration") {
		t.Fatalf("retired shape compile error=%v", err)
	}
}
func int64Pointer(value int64) *int64 { return &value }
