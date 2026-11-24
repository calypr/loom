package receipt

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/testkit"
	"github.com/calypr/loom/internal/testkit/explorerfixtures"
	recipetest "github.com/calypr/loom/internal/testkit/recipetest"

	"github.com/calypr/loom/internal/testkit/constructionfixtures"

	executionarango "github.com/calypr/loom/internal/dataframe/execution/arango"

	"github.com/calypr/loom/internal/authscope"
	httpctx "github.com/calypr/loom/internal/foundation/httpctx"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
)

func TestCompileExplorerReceiptLogsSafeStructuredSummary(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	requestID := "http-request-123"
	ctx := httpctx.ContextWithRequestID(context.Background(), requestID)
	request, engine, service := diagnosticCompileFixture(t, "synthetic-compile-role")
	receipt, err := Compile(ctx, request, nil, engine, service, logger, nil)
	if err != nil {
		t.Fatalf("compile receipt: %v", err)
	}
	if receipt.RequestID != "synthetic-compile-role" {
		t.Fatalf("receipt request role = %q, want synthetic compile role", receipt.RequestID)
	}
	records := testkit.DiagnosticRecords(t, &output)
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
	receipt, err := Compile(context.Background(), request, nil, engine, service, nil, nil)
	if err != nil {
		t.Fatalf("compile receipt fixture: %v", err)
	}
	ctx := httpctx.ContextWithRequestID(context.Background(), "request-category")
	requestScan := dataframeexecution.CategoryScanRequest{Output: "patients", Column: "c_patient", MaxValues: 10}
	_, err = NewRuntime(RuntimeConfig{Engine: engine, Logger: logger}).ScanCategories(ctx, receipt, recipeBindingsForDiagnostic(receipt), requestScan)
	if err != nil {
		t.Fatalf("category scan: %v", err)
	}
	records := testkit.DiagnosticRecords(t, &output)
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
	receipt, err := Compile(context.Background(), request, nil, receiptEngine, service, nil, nil)
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
		Registry: recipetest.Registry{}, QueryRows: queryRows, PreviewQueryRows: queryRows,
	})
	if err != nil {
		t.Fatalf("create category scan engine: %v", err)
	}
	requestScan := dataframeexecution.CategoryScanRequest{Output: "patients", Column: "c_patient", MaxValues: 10}
	if _, err := NewRuntime(RuntimeConfig{Engine: scanEngine, Logger: logger}).ScanCategories(context.Background(), receipt, recipeBindingsForDiagnostic(receipt), requestScan); err != nil {
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
	if _, err := NewRuntime(RuntimeConfig{Engine: scanEngine, Logger: logger}).ScanCategories(parentCtx, receipt, recipeBindingsForDiagnostic(receipt), requestScan); err != nil {
		t.Fatalf("category scan with earlier caller deadline: %v", err)
	}
	if !queryDeadline.Equal(parentDeadline) {
		t.Fatalf("category query deadline = %s, want earlier caller deadline %s", queryDeadline, parentDeadline)
	}

	backendFailure := errors.New("synthetic category backend failure")
	failingEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: recipetest.Registry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return backendFailure
		},
	})
	if err != nil {
		t.Fatalf("create failing category scan engine: %v", err)
	}
	_, err = NewRuntime(RuntimeConfig{Engine: failingEngine, Logger: logger}).ScanCategories(context.Background(), receipt, recipeBindingsForDiagnostic(receipt), requestScan)
	if !errors.Is(err, backendFailure) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("non-timeout category error = %v, want original backend cause without timeout classification", err)
	}
}

func TestConfiguredCategoryScannerReturnsTransportNeutralReceiptConflict(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	request, engine, service := diagnosticCompileFixture(t, "compile-role")
	receipt, err := Compile(context.Background(), request, nil, engine, service, nil, nil)
	if err != nil {
		t.Fatalf("compile receipt fixture: %v", err)
	}
	receipt.ResolvedSchemaDigest = "private-mismatch-value"
	ctx := httpctx.ContextWithRequestID(context.Background(), "request-category-conflict")
	requestScan := dataframeexecution.CategoryScanRequest{Output: "patients", StageID: "stage-base", ColumnID: "category-id", ValueColumnID: "value-id", MaxValues: 10}
	_, err = NewRuntime(RuntimeConfig{Engine: engine, Logger: logger}).ScanCategories(ctx, receipt, recipeBindingsForDiagnostic(receipt), requestScan)
	var authoringErr *explorer.AuthoringError
	if !errors.As(err, &authoringErr) || authoringErr.Status != http.StatusConflict || authoringErr.Diagnostic.Code != "RECEIPT_RECOMPILE_REQUIRED" {
		t.Fatalf("category conflict cause = %#v, want mapped redacted 409 authoring error", err)
	}
	if authoringErr.Diagnostic.Details["receiptId"] != receipt.ID {
		t.Fatalf("category conflict details = %#v, want receipt ID", authoringErr.Diagnostic.Details)
	}
	records := testkit.DiagnosticRecords(t, &output)
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

func diagnosticCompileFixture(t *testing.T, compileRole string) (ports.CompileReceiptRequest, *dataframeexecution.Engine, *explorer.Service) {
	t.Helper()
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: recipetest.Registry{},
		QueryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			return visit(map[string]any{"present": true, "value": "patient-1"})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := explorerfixtures.NewService(explorerfixtures.NewStore())
	if err != nil {
		t.Fatal(err)
	}
	return ports.CompileReceiptRequest{
		Project: "project-a", ExplorerID: "custom", Workspace: workspace, SnapshotToken: snapshot.Token, RequestID: compileRole,
		Authorized: ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}},
	}, engine, service
}

func recipeBindingsForDiagnostic(receipt *receiptmodel.CompilationReceipt) recipe.RuntimeBindings {
	return recipe.RuntimeBindings{
		Project: "project-a", SelectionProject: "project-a", DatasetGeneration: receipt.SourceGeneration,
		AuthScopeMode: authscope.ReadScopeUnrestricted, OutputNames: []string{"patients"},
	}
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
