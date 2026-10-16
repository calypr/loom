package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestDiscoverConstructionCategoriesClassifiesScanFailures(t *testing.T) {
	refusal := &compiler.CategoryScanRefusal{Code: compiler.CategoryScanStageUnknown, Column: "private-column"}
	tests := []struct {
		name      string
		scanErr   error
		wantClass ErrorClass
		wantCode  string
		wantCause error
		wantRaw   bool
	}{
		{
			name:      "wrapped compiler refusal",
			scanErr:   fmt.Errorf("compile category query: %w", refusal),
			wantClass: ClassUnprocessable,
			wantCode:  string(compiler.CategoryScanStageUnknown),
			wantCause: refusal,
		},
		{
			name:      "wrapped deadline",
			scanErr:   fmt.Errorf("execute category query: %w", context.DeadlineExceeded),
			wantClass: ClassUnavailable,
			wantCode:  "CATEGORY_SCAN_TIMEOUT",
			wantCause: context.DeadlineExceeded,
		},
		{
			name:      "wrapped feature error",
			scanErr:   wrappedCategoryUserError(t, dataframeerrors.CodeTablePivotCellCardinality, "private query details"),
			wantClass: ClassUnprocessable,
			wantCode:  string(dataframeerrors.CodeTablePivotCellCardinality),
			wantCause: categoryScanTestCause,
		},
		{
			name:      "wrapped backend resource limit",
			scanErr:   wrappedCategoryUserError(t, dataframeerrors.CodeQueryMemoryLimitExceeded, "private query details"),
			wantClass: ClassUnavailable,
			wantCode:  string(dataframeerrors.CodeQueryMemoryLimitExceeded),
			wantCause: categoryScanTestCause,
		},
		{
			name:      "wrapped receipt conflict",
			scanErr:   wrappedCategoryUserError(t, dataframeerrors.CodeRecipeContractViolation, "private receipt details"),
			wantClass: ClassConflict,
			wantCode:  string(dataframeerrors.CodeRecipeContractViolation),
			wantCause: categoryScanTestCause,
		},
		{
			name:      "wrapped forbidden error",
			scanErr:   wrappedCategoryUserError(t, dataframeerrors.CodeForbidden, "private authorization details"),
			wantClass: ClassForbidden,
			wantCode:  string(dataframeerrors.CodeForbidden),
			wantCause: categoryScanTestCause,
		},
		{
			name:      "wrapped cancellation",
			scanErr:   fmt.Errorf("execute category query: %w", context.Canceled),
			wantCause: context.Canceled,
			wantRaw:   true,
		},
		{
			name:      "preclassified lifecycle error",
			scanErr:   &Error{Class: ClassNotFound, Stage: "receipt", Code: "COMPILE_RECEIPT_NOT_FOUND", Message: "receipt unavailable", Cause: categoryScanTestCause},
			wantClass: ClassNotFound,
			wantCode:  "COMPILE_RECEIPT_NOT_FOUND",
			wantCause: categoryScanTestCause,
		},
		{
			name:      "untyped execution failure",
			scanErr:   fmt.Errorf("execute category query with secret-bind-value: %w", categoryScanTestCause),
			wantClass: ClassInternal,
			wantCode:  "INTERNAL_ERROR",
			wantCause: categoryScanTestCause,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, request := constructionCategoryDiscoveryErrorFixture(t, test.scanErr)
			_, err := service.DiscoverConstructionCategories(context.Background(), request)
			if err == nil {
				t.Fatal("DiscoverConstructionCategories() error = nil")
			}
			if test.wantCause != nil && !errors.Is(err, test.wantCause) {
				t.Fatalf("error %v does not preserve cause %v", err, test.wantCause)
			}
			if test.wantRaw {
				var lifecycleErr *Error
				if errors.As(err, &lifecycleErr) {
					t.Fatalf("error = %#v, want cancellation to remain transport-classifiable", lifecycleErr)
				}
				return
			}
			var lifecycleErr *Error
			if !errors.As(err, &lifecycleErr) {
				t.Fatalf("error = %T %v, want lifecycle.Error", err, err)
			}
			if lifecycleErr.Class != test.wantClass || lifecycleErr.Code != test.wantCode {
				t.Fatalf("lifecycle error = %#v, want class %q code %q", lifecycleErr, test.wantClass, test.wantCode)
			}
			if strings.Contains(lifecycleErr.Message, "private") || strings.Contains(lifecycleErr.Message, "secret-bind-value") {
				t.Fatalf("public lifecycle message contains scan internals: %q", lifecycleErr.Message)
			}
			if test.wantClass == ClassInternal && lifecycleErr.Message != "category discovery failed unexpectedly" {
				t.Fatalf("internal error message = %q, want safe generic message", lifecycleErr.Message)
			}
		})
	}
}

var categoryScanTestCause = errors.New("private query bind sentinel")

func wrappedCategoryUserError(t *testing.T, code dataframeerrors.ErrorCode, detail string) error {
	t.Helper()
	return fmt.Errorf("scan callback: %w", dataframeerrors.Wrap(categoryScanTestCause, code, detail))
}

func constructionCategoryDiscoveryErrorFixture(t *testing.T, scanErr error) (*Service, ConstructionCategoryDiscoveryRequest) {
	t.Helper()
	service, store, snapshot := constructionProposalService(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	for index := range workspace.Documents[0].Columns {
		workspace.Documents[0].Columns[index].LogicalType = "string"
	}
	category := workspace.Documents[0].Columns[0]
	category.Column = "status"
	category.Label = "Status"
	category.Source.Field.Path = "status"
	workspace.Documents[0].Columns = append(workspace.Documents[0].Columns, category)
	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftConfig, store.created.DraftDigest = encoded, digest
	upgraded, err := authoringv2.UpgradeDocumentToConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	request := ConstructionCategoryDiscoveryRequest{
		Project: store.created.Project, ExplorerID: store.created.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", StageID: recipe.ConstructionSourceProjectionID,
		CategoryColumnID: upgraded.Columns[1].ColumnID, ValueColumnID: upgraded.Columns[0].ColumnID,
	}
	service.config.ScanCategories = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
		return dataframeexecution.CategoryScanResult{}, fmt.Errorf("category scan adapter: %w", scanErr)
	}
	return service, request
}
