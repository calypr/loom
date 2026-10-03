package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/unit"
	"github.com/calypr/loom/internal/explorer"
)

func TestPreviewResponseEncoderProducesAtomicContract(t *testing.T) {
	receipt := &explorer.CompilationReceipt{ID: "same-id"}
	aggregateUnit := &unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "kg"}
	derivedUnit := &unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "kg/m2"}
	columns := []explorer.EmittedColumn{
		{EmissionID: "mean-weight", OutputID: "same-id", AuthoredColumns: []string{"weight"}, PublicColumn: "mean_weight", Label: "Mean weight", LogicalType: "decimal", Shape: "scalar", Nullable: true, ResultUnit: aggregateUnit},
		{EmissionID: "bmi", OutputID: "same-id", AuthoredColumns: []string{"weight", "height"}, PublicColumn: "bmi", Label: "BMI", LogicalType: "decimal", ResultUnit: derivedUnit},
		{EmissionID: "patient-id", OutputID: "same-id", AuthoredColumns: []string{"patient_id"}, PublicColumn: "patient_id", Label: "Patient ID", LogicalType: "string"},
	}
	encoder, err := newPreviewResponseEncoder(receipt, "same-id", columns, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if err := encoder.Visit(map[string]any{
		"public": "value", "__loom_source_resource_id": "secret-id",
		"__loom_preview_source": map[string]any{"kind": "SINGLE", "resourceType": "Observation", "id": "fhir-id"},
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := encoder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		ReceiptID  string                   `json:"receiptId"`
		OutputID   string                   `json:"outputId"`
		Rows       []map[string]any         `json:"rows"`
		RowSources []json.RawMessage        `json:"rowSources"`
		RowCount   int                      `json:"rowCount"`
		Columns    []explorer.EmittedColumn `json:"columns"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode response %s: %v", raw, err)
	}
	if decoded.ReceiptID != receipt.ID || decoded.OutputID != "same-id" || decoded.RowCount != 1 || len(decoded.Rows) != 1 || len(decoded.RowSources) != len(decoded.Rows) || len(decoded.Columns) != 3 {
		t.Fatalf("response = %#v", decoded)
	}
	if _, exists := decoded.Rows[0]["__loom_source_resource_id"]; exists {
		t.Fatalf("private FHIR source ID leaked into row JSON: %#v", decoded.Rows[0])
	}
	if _, exists := decoded.Rows[0]["__loom_preview_source"]; exists {
		t.Fatalf("row sidecar metadata leaked into row JSON: %#v", decoded.Rows[0])
	}
	var source map[string]string
	if err := json.Unmarshal(decoded.RowSources[0], &source); err != nil {
		t.Fatalf("decode row source %s: %v", decoded.RowSources[0], err)
	}
	if len(source) != 3 || source["kind"] != "SINGLE" || source["resourceType"] != "Observation" || source["id"] != "fhir-id" {
		t.Fatalf("row source = %#v, want one Observation/fhir-id source", source)
	}
	if len(decoded.Columns[0].AuthoredColumns) != 1 || decoded.Columns[0].AuthoredColumns[0] != "weight" {
		t.Fatalf("authored columns = %#v", decoded.Columns[0].AuthoredColumns)
	}
	if decoded.Columns[0].Shape != "scalar" || !decoded.Columns[0].Nullable {
		t.Fatalf("preview shape/nullability = %q/%t, want scalar/true", decoded.Columns[0].Shape, decoded.Columns[0].Nullable)
	}
	if got := decoded.Columns[0].ResultUnit; got == nil || got.System != aggregateUnit.System || got.Code != aggregateUnit.Code {
		t.Fatalf("aggregate result unit = %#v, want %#v", got, aggregateUnit)
	}
	if got := decoded.Columns[1].ResultUnit; got == nil || got.System != derivedUnit.System || got.Code != derivedUnit.Code {
		t.Fatalf("derived result unit = %#v, want %#v", got, derivedUnit)
	}
	var wire struct {
		Columns []map[string]json.RawMessage `json:"columns"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("decode preview column JSON: %v", err)
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
	assertResultUnit(wire.Columns[0]["resultUnit"], "kg")
	assertResultUnit(wire.Columns[1]["resultUnit"], "kg/m2")
	if _, exists := wire.Columns[2]["resultUnit"]; exists {
		t.Fatalf("unitless preview column contains resultUnit: %s", wire.Columns[2]["resultUnit"])
	}
}

func TestPreviewResponseEncoderEmitsAlignedEmptyRowSources(t *testing.T) {
	encoder, err := newPreviewResponseEncoder(&explorer.CompilationReceipt{ID: "receipt"}, "output", nil, 4096)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := encoder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Rows       []map[string]any  `json:"rows"`
		RowSources []json.RawMessage `json:"rowSources"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode empty response %s: %v", raw, err)
	}
	if decoded.Rows == nil || decoded.RowSources == nil || len(decoded.Rows) != 0 || len(decoded.RowSources) != len(decoded.Rows) {
		t.Fatalf("empty rows and sidecars = %#v / %#v, want empty arrays", decoded.Rows, decoded.RowSources)
	}
}

func TestPreviewResponseEncoderRejectsOverflowWithoutResult(t *testing.T) {
	encoder, err := newPreviewResponseEncoder(&explorer.CompilationReceipt{ID: "receipt"}, "output", nil, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := encoder.Visit(map[string]any{"value": strings.Repeat("x", 2048)}); !errors.Is(err, ErrPreviewResponseTooLarge) {
		t.Fatalf("Visit error = %v, want %v", err, ErrPreviewResponseTooLarge)
	}
}

func TestPreviewResponseEncoderBoundsSourceSidecarDuringVisit(t *testing.T) {
	encoder, err := newPreviewResponseEncoder(&explorer.CompilationReceipt{ID: "receipt"}, "output", nil, 1024)
	if err != nil {
		t.Fatal(err)
	}
	err = encoder.Visit(map[string]any{
		"value": "small row",
		"__loom_preview_source": map[string]any{
			"kind": "SINGLE", "resourceType": "Observation", "id": strings.Repeat("x", 2048),
		},
	})
	if !errors.Is(err, ErrPreviewResponseTooLarge) {
		t.Fatalf("Visit error = %v, want %v for oversized source sidecar", err, ErrPreviewResponseTooLarge)
	}
}

func TestPreviewErrorPreservesStableClassifications(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"timeout", context.DeadlineExceeded, http.StatusGatewayTimeout, "PREVIEW_TIMEOUT"},
		{"canceled", context.Canceled, 499, "CLIENT_CANCELED"},
		{"oversized", &previewResponseTooLargeError{Limit: 32}, http.StatusRequestEntityTooLarge, "RESPONSE_TOO_LARGE"},
		{"plan", dataframeerrors.NewError(dataframeerrors.CodePlanTooExpensive, "private"), http.StatusTooManyRequests, "PLAN_TOO_EXPENSIVE"},
		{"relationship-cardinality", dataframeerrors.NewError(dataframeerrors.CodeRelationshipCardinalityViolation, "private"), http.StatusUnprocessableEntity, "RELATIONSHIP_CARDINALITY_VIOLATION"},
		{"construction-expansion-empty", dataframeerrors.NewError(dataframeerrors.CodeConstructionExpansionEmpty, ""), http.StatusUnprocessableEntity, "CONSTRUCTION_EXPANSION_EMPTY"},
		{"explicit-group-unassigned", dataframeerrors.NewError(dataframeerrors.CodeExplicitGroupUnassignedMember, ""), http.StatusUnprocessableEntity, "EXPLICIT_GROUP_UNASSIGNED_MEMBER"},
		{"pivot-cell-cardinality", dataframeerrors.NewError(dataframeerrors.CodeTablePivotCellCardinality, ""), http.StatusUnprocessableEntity, "TABLE_PIVOT_CELL_CARDINALITY"},
		{"pivot-unlisted-category", dataframeerrors.NewError(dataframeerrors.ErrorCode("TABLE_PIVOT_UNLISTED_CATEGORY"), ""), http.StatusUnprocessableEntity, "TABLE_PIVOT_UNLISTED_CATEGORY"},
		{"temporal-anchor", dataframeerrors.NewError(dataframeerrors.CodeTemporalAnchorInvalid, "private"), http.StatusUnprocessableEntity, "TEMPORAL_ANCHOR_INVALID"},
		{"temporal-precision", dataframeerrors.NewError(dataframeerrors.CodeTemporalPrecisionUnsupported, "private"), http.StatusUnprocessableEntity, "TEMPORAL_PRECISION_UNSUPPORTED"},
		{"temporal-tie", dataframeerrors.NewError(dataframeerrors.CodeTemporalTieAmbiguous, "private"), http.StatusUnprocessableEntity, "TEMPORAL_TIE_AMBIGUOUS"},
		{"backend", dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "private", dataframeerrors.WithRetryable(true)), http.StatusServiceUnavailable, "BACKEND_UNAVAILABLE"},
		{"memory-limit", dataframeerrors.NewError(dataframeerrors.CodeQueryMemoryLimitExceeded, "private"), http.StatusServiceUnavailable, "QUERY_MEMORY_LIMIT_EXCEEDED"},
		{"resource-limit", dataframeerrors.NewError(dataframeerrors.CodeQueryResourceLimitExceeded, "private"), http.StatusServiceUnavailable, "QUERY_RESOURCE_LIMIT_EXCEEDED"},
		{"out-of-memory", dataframeerrors.NewError(dataframeerrors.CodeQueryBackendOutOfMemory, "private"), http.StatusServiceUnavailable, "QUERY_BACKEND_OUT_OF_MEMORY"},
		{"receipt", &receiptPreviewResolutionError{ReceiptID: "receipt-1", Err: contractMismatch("output_execution", "patients", "private-expected", "private-actual")}, http.StatusConflict, "RECEIPT_RECOMPILE_REQUIRED"},
		{"unknown", errors.New("private"), http.StatusInternalServerError, "PREVIEW_FAILED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got *explorer.AuthoringError
			if !errors.As(previewRouteError(test.err), &got) || got.Status != test.status || got.Diagnostic.Code != test.code {
				t.Fatalf("error=%#v, want status=%d code=%s", got, test.status, test.code)
			}
		})
	}
}

func TestConstructionExpansionEmptyPreviewErrorIsActionable(t *testing.T) {
	var got *explorer.AuthoringError
	if !errors.As(previewRouteError(dataframeerrors.NewError(dataframeerrors.CodeConstructionExpansionEmpty, "")), &got) {
		t.Fatal("previewRouteError() did not return an authoring error")
	}
	want := "At least one row has an empty list or no matching related records. Choose 'Drop the original row' or 'Keep the row with a missing item' for lists, or 'Leave that current row out' or 'Keep that current row once, with no related record ID' for related records."
	if got.Status != http.StatusUnprocessableEntity || got.Diagnostic.Code != string(dataframeerrors.CodeConstructionExpansionEmpty) || got.Diagnostic.Message != want {
		t.Fatalf("diagnostic = %#v, want 422 %s with actionable message", got.Diagnostic, dataframeerrors.CodeConstructionExpansionEmpty)
	}
}

func TestUnlistedPivotCategoryPreviewErrorIsActionable(t *testing.T) {
	var got *explorer.AuthoringError
	err := dataframeerrors.NewError(dataframeerrors.ErrorCode("TABLE_PIVOT_UNLISTED_CATEGORY"), "private")
	if !errors.As(previewRouteError(err), &got) {
		t.Fatal("previewRouteError() did not return an authoring error")
	}
	want := "Pivot found an unlisted category; select all discovered categories or filter rows before Pivot."
	if got.Status != http.StatusUnprocessableEntity || got.Diagnostic.Code != "TABLE_PIVOT_UNLISTED_CATEGORY" || got.Diagnostic.Message != want {
		t.Fatalf("diagnostic = %#v, want 422 TABLE_PIVOT_UNLISTED_CATEGORY with actionable message %q", got.Diagnostic, want)
	}
}

func TestClassifyReceiptPreviewResolutionErrorOnlyMarksContractFailuresForRecompile(t *testing.T) {
	mismatch := classifyReceiptPreviewResolutionError("receipt-1", contractMismatch("output_execution", "patients", "expected", "actual"))
	var resolution *receiptPreviewResolutionError
	if !errors.As(mismatch, &resolution) || resolution.ReceiptID != "receipt-1" {
		t.Fatalf("mismatch classification = %#v", mismatch)
	}
	var authoring *explorer.AuthoringError
	if !errors.As(previewRouteError(mismatch), &authoring) || authoring.Status != http.StatusConflict {
		t.Fatalf("mismatch route error = %#v", authoring)
	}
	if got := authoring.Diagnostic.Details["receiptId"]; got != "receipt-1" {
		t.Fatalf("receiptId detail = %#v", got)
	}
	if got := authoring.Diagnostic.Details["outputId"]; got != "patients" {
		t.Fatalf("outputId detail = %#v", got)
	}

	compileFailure := classifyReceiptPreviewResolutionError("receipt-1", errors.New("compiler failed"))
	if errors.As(compileFailure, &resolution) {
		t.Fatalf("ordinary compiler failure was classified as a receipt mismatch: %v", compileFailure)
	}
	if !errors.As(previewRouteError(compileFailure), &authoring) || authoring.Status != http.StatusInternalServerError || authoring.Diagnostic.Code != "PREVIEW_FAILED" {
		t.Fatalf("compiler route error = %#v", authoring)
	}
}
