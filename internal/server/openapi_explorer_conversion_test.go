package server

import (
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/dataframe/unit"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestV2ReceiptResponsePreservesResultUnitsAndOmitsAbsentUnit(t *testing.T) {
	const system = "http://unitsofmeasure.org"
	aggregateUnit := &unit.UnitIdentity{System: system, Code: "kg"}
	derivedUnit := &unit.UnitIdentity{System: system, Code: "kg/m2"}
	receipt := &explorer.CompilationReceipt{
		ID: "receipt-a",
		EmittedColumns: []explorer.EmittedColumn{
			{OutputID: "patients", PublicColumn: "mean_weight", Label: "Mean weight", LogicalType: "decimal", ResultUnit: aggregateUnit},
			{OutputID: "patients", PublicColumn: "bmi", Label: "BMI", LogicalType: "decimal", ResultUnit: derivedUnit},
			{OutputID: "patients", PublicColumn: "patient_id", Label: "Patient ID", LogicalType: "string"},
		},
	}
	workspace := authoringv2.Workspace{Documents: []authoringv2.Document{{Output: authoringv2.Output{ID: "patients", Title: "Patients"}}}}

	response := v2ReceiptResponse(receipt, workspace)
	if len(response.Outputs) != 1 || len(response.Outputs[0].Columns) != 3 {
		t.Fatalf("compile response columns = %#v", response.Outputs)
	}
	if got := response.Outputs[0].Columns[0].ResultUnit; got == nil || got.System != aggregateUnit.System || got.Code != aggregateUnit.Code {
		t.Fatalf("aggregate result unit = %#v, want %#v", got, aggregateUnit)
	}
	if got := response.Outputs[0].Columns[1].ResultUnit; got == nil || got.System != derivedUnit.System || got.Code != derivedUnit.Code {
		t.Fatalf("derived result unit = %#v, want %#v", got, derivedUnit)
	}

	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Outputs []struct {
			Columns []map[string]json.RawMessage `json:"columns"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("decode compile response %s: %v", raw, err)
	}
	assertResultUnit := func(raw json.RawMessage, code string) {
		t.Helper()
		var got map[string]string
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("decode resultUnit %s: %v", raw, err)
		}
		if len(got) != 2 || got["system"] != "http://unitsofmeasure.org" || got["code"] != code {
			t.Fatalf("resultUnit JSON = %#v, want {system: UCUM, code: %q}", got, code)
		}
	}
	assertResultUnit(wire.Outputs[0].Columns[0]["resultUnit"], "kg")
	assertResultUnit(wire.Outputs[0].Columns[1]["resultUnit"], "kg/m2")
	if _, exists := wire.Outputs[0].Columns[2]["resultUnit"]; exists {
		t.Fatalf("unitless compile column contains resultUnit: %s", wire.Outputs[0].Columns[2]["resultUnit"])
	}
}
