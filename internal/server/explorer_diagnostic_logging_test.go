package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/testkit/constructionfixtures"

	executionarango "github.com/calypr/loom/internal/dataframe/execution/arango"

	shared "github.com/arangodb/go-driver/v2/arangodb/shared"

	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func TestConfiguredAQLQueryRowsLogsSafeRequestCorrelatedRecords(t *testing.T) {
	for _, phase := range []string{"query_rows", "preview_query_rows"} {
		t.Run(phase, func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&output, nil))
			ctx := httpapi.ContextWithRequestID(context.Background(), "request-123")
			query := "FOR d IN patients FILTER d.id == @secret RETURN d"
			bindVars := map[string]any{"secret": "private-bind-value"}
			failure := errors.New("arangodb unavailable: connection refused; query=RETURN @secret auth_token=private-auth-value")
			gotErr := executionarango.ConfiguredAQLQueryRows(logger, phase, preserveConfiguredQueryError, func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
				return failure
			})(ctx, query, 16, bindVars, func(map[string]any) error { return nil })
			if gotErr != failure {
				t.Fatalf("callback error = %v, want original error", gotErr)
			}

			records := diagnosticRecords(t, &output)
			if len(records) != 2 {
				t.Fatalf("record count = %d, want start and completion", len(records))
			}
			start, record := records[0], records[1]
			if start["request_id"] != "request-123" || start["phase"] != phase+"_start" || start["query_hash"] != record["query_hash"] {
				t.Fatalf("start record fields = %#v", start)
			}
			if start["query_bytes"] != float64(len(query)) || start["bind_vars"] != float64(len(bindVars)) || start["cursor_batch_size"] != float64(16) {
				t.Fatalf("start counts = %#v", start)
			}
			if _, found := start["success"]; found {
				t.Fatalf("pending start record has terminal success field: %#v", start)
			}
			if record["request_id"] != "request-123" || record["phase"] != phase || record["success"] != false {
				t.Fatalf("record fields = %#v", record)
			}
			if _, ok := record["duration_ms"].(float64); !ok {
				t.Fatalf("duration_ms = %#v, want number", record["duration_ms"])
			}
			digest := sha256.Sum256([]byte(query))
			if record["query_hash"] != hex.EncodeToString(digest[:]) {
				t.Fatalf("query_hash = %#v, want full SHA-256", record["query_hash"])
			}
			if record["error_type"] != "*errors.errorString" {
				t.Fatalf("error_type = %#v", record["error_type"])
			}
			if record["cause"] != "arangodb unavailable: connection refused; query=[redacted]" {
				t.Fatalf("bounded cause = %#v", record["cause"])
			}
			if len(record["cause"].(string)) > 256 {
				t.Fatalf("AQL cause exceeds %d bytes: %d", 256, len(record["cause"].(string)))
			}
			for _, forbidden := range []string{query, "private-bind-value", "private-auth-value", failure.Error()} {
				if strings.Contains(output.String(), forbidden) {
					t.Fatalf("log contains sensitive input %q: %s", forbidden, output.String())
				}
			}
		})
	}
}

func TestConfiguredQueryErrorMappingPreservesTypedSemantics(t *testing.T) {
	canceled := fmt.Errorf("query canceled: %w", context.Canceled)
	var canceledAuthoring *explorer.AuthoringError
	if got := preserveConfiguredQueryError(canceled); !errors.As(got, &canceledAuthoring) || canceledAuthoring.Status != 499 || canceledAuthoring.Diagnostic.Code != "CLIENT_CANCELED" {
		t.Fatalf("canceled query error = %#v, want redacted 499 CLIENT_CANCELED", got)
	}

	deadline := fmt.Errorf("query deadline: %w", context.DeadlineExceeded)
	if got := preserveConfiguredQueryError(deadline); !errors.Is(got, context.DeadlineExceeded) || got != deadline {
		t.Fatalf("deadline query error = %#v, want original lifecycle-classifiable deadline", got)
	}

	userErr := dataframeerrors.NewError(dataframeerrors.CodeConstructionExpansionEmpty, "")
	var userAuthoring *explorer.AuthoringError
	if got := preserveConfiguredQueryError(userErr); !errors.As(got, &userAuthoring) || userAuthoring.Status != http.StatusUnprocessableEntity || userAuthoring.Diagnostic.Code != string(dataframeerrors.CodeConstructionExpansionEmpty) {
		t.Fatalf("typed query error = %#v, want mapped 422 user error", got)
	}

	unknown := errors.New("private backend details")
	if got := preserveConfiguredQueryError(unknown); got != unknown {
		t.Fatalf("untyped query error = %#v, want original lifecycle-classifiable error", got)
	}
}

func TestConfiguredQueryErrorMappingKeepsAuthStatusAndSafeMessage(t *testing.T) {
	tests := []struct {
		code   dataframeerrors.ErrorCode
		status int
	}{
		{code: dataframeerrors.CodeUnauthenticated, status: http.StatusUnauthorized},
		{code: dataframeerrors.CodeForbidden, status: http.StatusForbidden},
		{code: dataframeerrors.CodeUnauthorizedProject, status: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(string(test.code), func(t *testing.T) {
			cause := dataframeerrors.NewError(test.code, "private authorization detail")
			mapped := preserveConfiguredQueryError(cause)
			var authoringErr *explorer.AuthoringError
			if !errors.As(mapped, &authoringErr) || authoringErr.Status != test.status || authoringErr.Diagnostic.Code != string(test.code) {
				t.Fatalf("mapped auth error = %#v, want status %d and code %s", mapped, test.status, test.code)
			}
			if authoringErr.Diagnostic.Message != dataframeerrors.PublicMessage(cause) || errors.Unwrap(authoringErr) != cause {
				t.Fatalf("mapped auth message/cause = %q/%v", authoringErr.Diagnostic.Message, errors.Unwrap(authoringErr))
			}
			if strings.Contains(authoringErr.Error(), "private authorization detail") {
				t.Fatalf("mapped auth error exposes private message: %v", authoringErr)
			}
		})
	}
}

func TestLoggedPreviewIndexPreparationUsesSafeFields(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	ctx := httpapi.ContextWithRequestID(context.Background(), "request-index")
	failure := fmt.Errorf("prepare index: %w", shared.ArangoError{
		HasError: true, Code: http.StatusConflict, ErrorNum: 12345, ErrorMessage: "AQL: persistent index field is unsupported; access_token=private-auth-value",
	})
	spec := compiler.PreviewCoveringIndexSpec{Collection: "patients", Name: "idx_patient_category"}
	gotErr := executionarango.LoggedPreviewIndexPreparation(logger, func(context.Context, compiler.PreviewCoveringIndexSpec) error { return failure })(ctx, spec)
	if gotErr != failure {
		t.Fatalf("preparation error = %v, want original error", gotErr)
	}
	records := diagnosticRecords(t, &output)
	if len(records) != 1 || records[0]["request_id"] != "request-index" || records[0]["phase"] != "index_prepare" || records[0]["collection"] != spec.Collection || records[0]["index"] != spec.Name || records[0]["success"] != false {
		t.Fatalf("index log records = %#v", records)
	}
	if records[0]["cause"] != "ArangoDB error number 12345 (HTTP 409): AQL: persistent index field is unsupported; access_token=[redacted]" {
		t.Fatalf("index cause = %#v", records[0]["cause"])
	}
	if len(records[0]["cause"].(string)) > 256 {
		t.Fatalf("index cause exceeds %d bytes: %d", 256, len(records[0]["cause"].(string)))
	}
	if strings.Contains(output.String(), failure.Error()) {
		t.Fatalf("index logs contain raw backend error: %s", output.String())
	}
}

func TestCompileExplorerReceiptLogsSafeStructuredSummary(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	requestID := "http-request-123"
	ctx := httpapi.ContextWithRequestID(context.Background(), requestID)
	request, engine, service := diagnosticCompileFixture(t, "synthetic-compile-role")
	receipt, err := compileExplorerReceipt(ctx, request, nil, engine, service, logger, nil)
	if err != nil {
		t.Fatalf("compile receipt: %v", err)
	}
	if receipt.RequestID != "synthetic-compile-role" {
		t.Fatalf("receipt request role = %q, want synthetic compile role", receipt.RequestID)
	}
	records := diagnosticRecords(t, &output)
	if len(records) != 1 {
		t.Fatalf("record count = %d, want one compile summary: %s", len(records), output.String())
	}
	record := records[0]
	if record["request_id"] != requestID || record["compile_role"] != receipt.RequestID || record["phase"] != "compile_receipt" || record["success"] != true {
		t.Fatalf("compile summary fields = %#v", record)
	}
	if record["project"] != request.Project || record["explorer_id"] != request.ExplorerID || record["receipt_id"] != receipt.ID {
		t.Fatalf("compile summary identity fields = %#v", record)
	}
	if _, ok := record["duration_ms"].(float64); !ok {
		t.Fatalf("duration_ms = %#v, want number", record["duration_ms"])
	}
	for _, field := range []string{"prepare_ms", "compile_workspace_ms", "compile_resolved_bundle_ms", "contract_build_ms", "validate_persist_ms", "receipt_bytes", "output_count", "column_count"} {
		if _, ok := record[field].(float64); !ok {
			t.Fatalf("compile summary %s = %#v, want number", field, record[field])
		}
	}
	if !containsJSONValue(record["output_ids"], "patients") || !containsJSONValue(record["output_column_ids"], "c_patient") {
		t.Fatalf("compile summary lacks output/column identifiers: %#v", record)
	}
	if strings.Contains(output.String(), request.SnapshotToken) {
		t.Fatalf("compile summary leaked snapshot token: %s", output.String())
	}
}

func TestConfiguredCategoryScannerSeparatesResolutionAndScanDiagnostics(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	request, engine, service := diagnosticCompileFixture(t, "compile-role")
	receipt, err := compileExplorerReceipt(context.Background(), request, nil, engine, service, nil, nil)
	if err != nil {
		t.Fatalf("compile receipt fixture: %v", err)
	}
	ctx := httpapi.ContextWithRequestID(context.Background(), "request-category")
	requestScan := dataframeexecution.CategoryScanRequest{Output: "patients", Column: "c_patient", MaxValues: 10}
	_, err = configuredCategoryScanner(logger, engine)(ctx, receipt, recipeBindingsForDiagnostic(receipt), requestScan)
	if err != nil {
		t.Fatalf("category scan: %v", err)
	}
	records := diagnosticRecords(t, &output)
	if len(records) != 2 {
		t.Fatalf("category records = %#v", records)
	}
	wantPhases := []string{"category_scan_resolution", "category_scan"}
	for index, record := range records {
		if record["request_id"] != "request-category" || record["phase"] != wantPhases[index] || record["success"] != true || record["output_id"] != "patients" {
			t.Fatalf("category phase %d record = %#v", index, record)
		}
	}
}

func TestConfiguredCategoryScannerUsesPivotTimeoutAndPreservesBackendErrors(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	request, receiptEngine, service := diagnosticCompileFixture(t, "pivot-timeout")
	receipt, err := compileExplorerReceipt(context.Background(), request, nil, receiptEngine, service, nil, nil)
	if err != nil {
		t.Fatalf("compile receipt fixture: %v", err)
	}
	if executionarango.PivotCategoryScanTimeout != 30*time.Second {
		t.Fatalf("Pivot category timeout = %s, want 30s", executionarango.PivotCategoryScanTimeout)
	}
	if executionarango.ExplorerPreviewTimeout != 10*time.Second {
		t.Fatalf("ordinary proposal/direct preview timeout = %s, want unchanged 10s", executionarango.ExplorerPreviewTimeout)
	}
	if got := executionarango.PreviewQueryMaxRuntime(context.Background()); got != executionarango.ExplorerPreviewTimeout {
		t.Fatalf("ordinary preview query max runtime = %s, want %s", got, executionarango.ExplorerPreviewTimeout)
	}
	if got := executionarango.PreviewQueryMaxRuntime(executionarango.WithPivotPreviewQueryBudget(context.Background())); got != executionarango.PivotCategoryScanTimeout {
		t.Fatalf("Pivot preview query max runtime = %s, want %s", got, executionarango.PivotCategoryScanTimeout)
	}

	var queryDeadline time.Time
	var queryRuntime time.Duration
	queryRows := func(ctx context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		var ok bool
		queryDeadline, ok = ctx.Deadline()
		if !ok {
			t.Fatal("Pivot category query has no scanner deadline")
		}
		queryRuntime = executionarango.PreviewQueryMaxRuntime(ctx)
		return visit(map[string]any{"present": true, "value": "patient-1"})
	}
	scanEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: compilerTestRegistry{}, QueryRows: queryRows, PreviewQueryRows: queryRows,
	})
	if err != nil {
		t.Fatalf("create category scan engine: %v", err)
	}
	requestScan := dataframeexecution.CategoryScanRequest{Output: "patients", Column: "c_patient", MaxValues: 10}
	if _, err := configuredCategoryScanner(logger, scanEngine)(context.Background(), receipt, recipeBindingsForDiagnostic(receipt), requestScan); err != nil {
		t.Fatalf("category scan: %v", err)
	}
	remaining := time.Until(queryDeadline)
	if remaining > executionarango.PivotCategoryScanTimeout || remaining < executionarango.PivotCategoryScanTimeout-time.Second {
		t.Fatalf("category query deadline remaining = %s, want approximately %s", remaining, executionarango.PivotCategoryScanTimeout)
	}
	if queryRuntime != executionarango.PivotCategoryScanTimeout {
		t.Fatalf("category Arango max runtime = %s, want %s", queryRuntime, executionarango.PivotCategoryScanTimeout)
	}

	parentDeadline := time.Now().Add(2 * time.Second)
	parentCtx, cancel := context.WithDeadline(context.Background(), parentDeadline)
	defer cancel()
	if _, err := configuredCategoryScanner(logger, scanEngine)(parentCtx, receipt, recipeBindingsForDiagnostic(receipt), requestScan); err != nil {
		t.Fatalf("category scan with earlier caller deadline: %v", err)
	}
	if !queryDeadline.Equal(parentDeadline) {
		t.Fatalf("category query deadline = %s, want earlier caller deadline %s", queryDeadline, parentDeadline)
	}

	backendFailure := errors.New("synthetic category backend failure")
	failingEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: compilerTestRegistry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return backendFailure
		},
	})
	if err != nil {
		t.Fatalf("create failing category scan engine: %v", err)
	}
	_, err = configuredCategoryScanner(logger, failingEngine)(context.Background(), receipt, recipeBindingsForDiagnostic(receipt), requestScan)
	if !errors.Is(err, backendFailure) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("non-timeout category error = %v, want original backend cause without timeout classification", err)
	}
}

func TestConfiguredCategoryScannerReturnsTransportNeutralReceiptConflict(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	request, engine, service := diagnosticCompileFixture(t, "compile-role")
	receipt, err := compileExplorerReceipt(context.Background(), request, nil, engine, service, nil, nil)
	if err != nil {
		t.Fatalf("compile receipt fixture: %v", err)
	}
	receipt.ResolvedSchemaDigest = "private-mismatch-value"
	ctx := httpapi.ContextWithRequestID(context.Background(), "request-category-conflict")
	requestScan := dataframeexecution.CategoryScanRequest{Output: "patients", StageID: "stage-base", ColumnID: "category-id", ValueColumnID: "value-id", MaxValues: 10}
	_, err = configuredCategoryScanner(logger, engine)(ctx, receipt, recipeBindingsForDiagnostic(receipt), requestScan)
	var authoringErr *explorer.AuthoringError
	if !errors.As(err, &authoringErr) || authoringErr.Status != http.StatusConflict || authoringErr.Diagnostic.Code != "RECEIPT_RECOMPILE_REQUIRED" {
		t.Fatalf("category conflict cause = %#v, want mapped redacted 409 authoring error", err)
	}
	if authoringErr.Diagnostic.Details["receiptId"] != receipt.ID {
		t.Fatalf("category conflict details = %#v, want receipt ID", authoringErr.Diagnostic.Details)
	}
	records := diagnosticRecords(t, &output)
	if len(records) != 1 || records[0]["request_id"] != "request-category-conflict" || records[0]["phase"] != "category_scan_resolution" || records[0]["error_code"] != "RECEIPT_RECOMPILE_REQUIRED" {
		t.Fatalf("category conflict records = %#v", records)
	}
	if records[0]["cause"] != authoringErr.Diagnostic.Message {
		t.Fatalf("category resolution cause = %#v, want the safe conflict message", records[0]["cause"])
	}
	if len(records[0]["cause"].(string)) > 256 {
		t.Fatalf("category cause exceeds %d bytes: %d", 256, len(records[0]["cause"].(string)))
	}
	if strings.Contains(output.String(), "private-mismatch-value") || strings.Contains(output.String(), "receipt_id") {
		t.Fatalf("category conflict logs contain private mismatch details: %s", output.String())
	}
}

func diagnosticCompileFixture(t *testing.T, compileRole string) (lifecycle.CompileReceiptRequest, *dataframeexecution.Engine, *explorer.Service) {
	t.Helper()
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace, err := authoringv2.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: compilerTestRegistry{},
		QueryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			return visit(map[string]any{"present": true, "value": "patient-1"})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(newTestExplorerStore())
	if err != nil {
		t.Fatal(err)
	}
	return lifecycle.CompileReceiptRequest{
		Project: "project-a", ExplorerID: "custom", Workspace: workspace, SnapshotToken: snapshot.Token, RequestID: compileRole,
		Authorized: lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}},
	}, engine, service
}

func recipeBindingsForDiagnostic(receipt *explorer.CompilationReceipt) recipe.RuntimeBindings {
	return recipe.RuntimeBindings{
		Project: "project-a", SelectionProject: "project-a", DatasetGeneration: receipt.SourceGeneration,
		AuthScopeMode: authscope.ReadScopeUnrestricted, OutputNames: []string{"patients"},
	}
}

func diagnosticRecords(t *testing.T, output *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(output.Bytes()))
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode slog record: %v", err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return records
}

func containsJSONValue(value any, expected string) bool {
	values, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range values {
		if item == expected {
			return true
		}
	}
	return false
}
