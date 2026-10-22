package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestConstructionInputNamesReportsTheMissingColumn(t *testing.T) {
	_, err := constructionInputNames(map[string]string{"present": "name"}, []string{"present", "missing"})
	var missing *constructionInputMissingError
	if !errors.As(err, &missing) {
		t.Fatalf("error = %v, want constructionInputMissingError", err)
	}
	if missing.ColumnID != "missing" {
		t.Fatalf("column ID = %q, want missing", missing.ColumnID)
	}
}

func TestReceiptContractFailureIsDescriptive(t *testing.T) {
	const columnID = "source_child_v1_missing"
	err := receiptContractFailure(fmt.Errorf("reconcile output metadata: %w", &constructionInputMissingError{ColumnID: columnID, StepID: "pivot_1", OutputName: "missing_value"}))
	status, response := authoringErrorForOpenAPI(context.Background(), "proposeExplorerConstruction", err)
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", status, http.StatusInternalServerError)
	}
	if response.Error.Code != "CONSTRUCTION_RECEIPT_INPUT_MISSING" {
		t.Fatalf("code = %q, want CONSTRUCTION_RECEIPT_INPUT_MISSING", response.Error.Code)
	}
	if !strings.Contains(response.Error.Message, columnID) || !strings.Contains(response.Error.Message, "pivot_1") || !strings.Contains(response.Error.Message, "missing_value") {
		t.Fatalf("message = %q, want step, output, and input column", response.Error.Message)
	}
	if response.Error.Diagnostic == nil || response.Error.Diagnostic.Details == nil {
		t.Fatalf("diagnostic details missing: %#v", response.Error)
	}
	details := *response.Error.Diagnostic.Details
	if details["inputColumnId"] != columnID || details["stepId"] != "pivot_1" || details["outputName"] != "missing_value" {
		t.Fatalf("details = %#v, want step/output/inputColumnId", details)
	}
	if response.Error.Diagnostic.Stage != "reconcile_receipt" {
		t.Fatalf("stage = %q, want reconcile_receipt", response.Error.Diagnostic.Stage)
	}
}

func TestReceiptContractFailureDoesNotLeakPrivateCause(t *testing.T) {
	private := errors.New("private compiler memory address")
	err := receiptContractFailure(fmt.Errorf("wrapped: %w", private))
	_, response := authoringErrorForOpenAPI(context.Background(), "proposeExplorerConstruction", err)
	wire, marshalErr := json.Marshal(response)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(wire), private.Error()) {
		t.Fatalf("response leaked private cause: %s", wire)
	}
	if response.Error.Code != "CONSTRUCTION_RECEIPT_CONTRACT_INVALID" {
		t.Fatalf("code = %q, want CONSTRUCTION_RECEIPT_CONTRACT_INVALID", response.Error.Code)
	}
	if response.Error.Diagnostic == nil || response.Error.Diagnostic.Details == nil {
		t.Fatalf("diagnostic details missing: %#v", response.Error)
	}
	if got := (*response.Error.Diagnostic.Details)["causeType"]; got == nil {
		t.Fatalf("cause type missing from diagnostic details: %#v", response.Error.Diagnostic.Details)
	}
}

func TestAuthoringErrorForOpenAPIUnknownErrorNamesTheOperation(t *testing.T) {
	private := errors.New("private socket detail")
	status, response := authoringErrorForOpenAPI(context.Background(), "proposeExplorerConstruction", private)
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", status, http.StatusInternalServerError)
	}
	if response.Error.Code != "INTERNAL_ERROR" {
		t.Fatalf("code = %q, want INTERNAL_ERROR", response.Error.Code)
	}
	if !strings.Contains(response.Error.Message, "proposeExplorerConstruction") {
		t.Fatalf("message = %q, want operation name", response.Error.Message)
	}
	if response.Error.Diagnostic == nil || response.Error.Diagnostic.Details == nil {
		t.Fatalf("diagnostic details missing: %#v", response.Error)
	}
	details := *response.Error.Diagnostic.Details
	if details["operation"] != "proposeExplorerConstruction" || details["causeType"] == nil {
		t.Fatalf("details = %#v, want operation and causeType", details)
	}
	wire, marshalErr := json.Marshal(response)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(wire), private.Error()) {
		t.Fatalf("response leaked private cause: %s", wire)
	}
}
