package server

// The 14-branch vendor classifier moved verbatim into the ArangoDB adapter
// (internal/store/arango/errors.go, pinned by the golden table in
// internal/store/arango/errors_test.go). These tests keep the composition
// side of the boundary: adapter-classified errors must route exactly as the
// classifier's output did, unknown errors pass through, and the neutral
// execution budget is wired into the ArangoDB adapter mechanism by
// configuredAQLQueryRows.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func classifyThroughAdapter(t *testing.T, driverErr error) error {
	t.Helper()
	classified, ok := arangostore.TranslateQueryError(fmt.Errorf("arango query: %w", driverErr))
	if !ok {
		t.Fatalf("adapter did not classify %v", driverErr)
	}
	return classified
}

func TestPreserveConfiguredQueryErrorRoutesAdapterClassifiedAssertion(t *testing.T) {
	driverErr := shared.ArangoError{
		HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert,
		ErrorMessage: "AQL: CONSTRUCTION_EXPANSION_EMPTY: related expansion construction related_1 has no related records for row row-17 (while executing)",
	}
	err := preserveConfiguredQueryError(classifyThroughAdapter(t, driverErr))
	var routeErr *explorer.AuthoringError
	if !errors.As(err, &routeErr) || routeErr.Status != http.StatusUnprocessableEntity ||
		routeErr.Diagnostic.Code != string(dataframeerrors.CodeConstructionExpansionEmpty) {
		t.Fatalf("preview route error = %#v, want non-retryable construction expansion diagnostic with HTTP 422", routeErr)
	}
	if !errors.Is(err, driverErr) {
		t.Fatal("adapter-classified error lost the driver cause")
	}
}

func TestPreserveConfiguredQueryErrorRoutesGroupedValueMultiplicity(t *testing.T) {
	cause := shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert, ErrorMessage: "AQL: CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES (while executing)"}
	err := preserveConfiguredQueryError(classifyThroughAdapter(t, cause))
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeConstructionRowValueMultipleValues) || userErr.Retryable() {
		t.Fatalf("expected actionable grouped-value error, got %v", err)
	}
	var routeErr *explorer.AuthoringError
	if !errors.As(err, &routeErr) || routeErr.Status != 422 {
		t.Fatalf("expected 422 grouped-value error, got %v", routeErr)
	}
	if !errors.Is(err, cause) {
		t.Fatal("driver cause lost")
	}
}

func TestPreserveConfiguredQueryErrorRoutesPivotCellCardinality(t *testing.T) {
	driverErr := shared.ArangoError{
		HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert,
		ErrorMessage: "AQL: TABLE_PIVOT_CELL_CARDINALITY (while executing)",
	}
	err := preserveConfiguredQueryError(classifyThroughAdapter(t, driverErr))
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeTablePivotCellCardinality) || userErr.Retryable() {
		t.Fatalf("classified error=%#v, want non-retryable %s", err, dataframeerrors.CodeTablePivotCellCardinality)
	}
}

func TestPreserveConfiguredQueryErrorPreservesUnlistedPivotCategory(t *testing.T) {
	driverErr := shared.ArangoError{
		HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert,
		ErrorMessage: "AQL: TABLE_PIVOT_UNLISTED_CATEGORY (while executing)",
	}
	err := preserveConfiguredQueryError(classifyThroughAdapter(t, driverErr))
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != "TABLE_PIVOT_UNLISTED_CATEGORY" || userErr.Retryable() {
		t.Fatalf("classified error=%#v, want non-retryable TABLE_PIVOT_UNLISTED_CATEGORY", err)
	}
	if !errors.Is(err, driverErr) {
		t.Fatal("classified error did not preserve the Arango cause")
	}
}

func TestPreserveConfiguredQueryErrorRoutesTemporalAssertions(t *testing.T) {
	for _, code := range []dataframeerrors.ErrorCode{
		dataframeerrors.CodeTemporalAnchorInvalid,
		dataframeerrors.CodeTemporalPrecisionUnsupported,
		dataframeerrors.CodeTemporalTieAmbiguous,
		dataframeerrors.CodeUnitIdentityUnknown,
		dataframeerrors.CodeUnitDimensionIncompatible,
		dataframeerrors.CodeExplicitGroupUnassignedMember,
		dataframeerrors.CodeRelationshipCardinalityViolation,
	} {
		t.Run(string(code), func(t *testing.T) {
			driverErr := shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert, ErrorMessage: "AQL: " + string(code) + " (while executing)"}
			userErr, ok := dataframeerrors.AsUserError(preserveConfiguredQueryError(classifyThroughAdapter(t, driverErr)))
			if !ok || userErr.Code() != string(code) || userErr.Retryable() {
				t.Fatalf("classified error=%#v", userErr)
			}
		})
	}
}

func TestPreserveConfiguredQueryErrorPreservesResourceLimitClassifications(t *testing.T) {
	memory := shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrResourceLimit, ErrorMessage: "AQL: query would use more memory than allowed"}
	userErr, ok := dataframeerrors.AsUserError(preserveConfiguredQueryError(classifyThroughAdapter(t, memory)))
	if !ok || userErr.Code() != string(dataframeerrors.CodeQueryMemoryLimitExceeded) {
		t.Fatalf("memory-limit classification lost through composition: %#v", userErr)
	}
	if got := userErr.Details()["backend"]; got != "arangodb" {
		t.Fatalf("backend = %v, want arangodb", got)
	}
	if got := userErr.Details()["resource"]; got != "query_memory" {
		t.Fatalf("resource = %v, want query_memory", got)
	}
	outOfMemory := shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrOutOfMemory, ErrorMessage: "out of memory"}
	userErr, ok = dataframeerrors.AsUserError(preserveConfiguredQueryError(classifyThroughAdapter(t, outOfMemory)))
	if !ok || userErr.Code() != string(dataframeerrors.CodeQueryBackendOutOfMemory) {
		t.Fatalf("out-of-memory classification lost: %#v", userErr)
	}
	otherResource := shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrResourceLimit, ErrorMessage: "resource limit exceeded"}
	userErr, ok = dataframeerrors.AsUserError(preserveConfiguredQueryError(classifyThroughAdapter(t, otherResource)))
	if !ok || userErr.Code() != string(dataframeerrors.CodeQueryResourceLimitExceeded) {
		t.Fatalf("resource-limit classification lost: %#v", userErr)
	}
}

func TestPreserveConfiguredQueryErrorLeavesOtherFailuresUntouched(t *testing.T) {
	want := errors.New("visitor failed")
	if got := preserveConfiguredQueryError(want); !errors.Is(got, want) || got != want {
		t.Fatalf("preserveConfiguredQueryError() = %v, want original error", got)
	}
	// An unrecognized ASSERT must stay exactly as the adapter returned it.
	unrecognized := shared.ArangoError{
		HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert,
		ErrorMessage: "AQL: row expansion construction expand_tags has no items for row row-17 (while executing)",
	}
	wrapped := fmt.Errorf("arango query: %w", unrecognized)
	if got := preserveConfiguredQueryError(wrapped); got != wrapped {
		t.Fatalf("preserveConfiguredQueryError() = %v, want original unrecognized assertion", got)
	}
}

func TestPreserveConfiguredQueryErrorMapsAuthorizationCodes(t *testing.T) {
	unauthenticated := dataframeerrors.NewError(dataframeerrors.CodeUnauthenticated, "")
	var routeErr *explorer.AuthoringError
	if err := preserveConfiguredQueryError(unauthenticated); !errors.As(err, &routeErr) || routeErr.Status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated routing = %#v, want 401 AuthoringError", err)
	}
	forbidden := dataframeerrors.NewError(dataframeerrors.CodeForbidden, "")
	if err := preserveConfiguredQueryError(forbidden); !errors.As(err, &routeErr) || routeErr.Status != http.StatusForbidden {
		t.Fatalf("forbidden routing = %#v, want 403 AuthoringError", err)
	}
}

func TestConfiguredAQLQueryRowsWiresExecutionBudgetIntoArangoAdapter(t *testing.T) {
	var captured context.Context
	adapter := configuredAQLQueryRows(nil, "test_phase", func(ctx context.Context, _ compiler.CompiledCommand, _ int, _ dataframeexecution.RowVisitor) error {
		captured = ctx
		return nil
	})
	command := testAQLCommand(t, "RETURN 1", nil)

	ctx := dataframeexecution.WithQueryMemoryBudget(context.Background(), 900000)
	if err := adapter(ctx, command, 10, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if limit, ok := arangostore.QueryMemoryLimitFromContext(captured); !ok || limit != 900000 {
		t.Fatalf("adapter memory limit = %d/%t, want the execution budget 900000", limit, ok)
	}

	if err := adapter(context.Background(), command, 10, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if limit, ok := arangostore.QueryMemoryLimitFromContext(captured); ok {
		t.Fatalf("adapter memory limit attached without an execution budget: %d", limit)
	}
}

// Golden diagnostic rendering: the exact server-side cause strings the
// composition logger produced before the move, now supplied by the adapter.
func TestDiagnosticErrorCauseGoldenArangoRenderings(t *testing.T) {
	t.Run("value_with_message", func(t *testing.T) {
		err := fmt.Errorf("arango query: %w", shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrResourceLimit, ErrorMessage: "boom"})
		if got := diagnosticErrorCause(err); got != "ArangoDB error number 32 (HTTP 500): boom" {
			t.Fatalf("cause = %q", got)
		}
	})
	t.Run("pointer_without_message", func(t *testing.T) {
		err := fmt.Errorf("prepare index: %w", &shared.ArangoError{HasError: true, Code: 400, ErrorNum: 1023})
		if got := diagnosticErrorCause(err); got != "ArangoDB error number 1023 (HTTP 400)" {
			t.Fatalf("cause = %q", got)
		}
	})
	t.Run("typed_cause_wins_over_vendor_rendering", func(t *testing.T) {
		classified := classifyThroughAdapter(t, shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrResourceLimit, ErrorMessage: "AQL: query would use more memory than allowed"})
		if got := diagnosticErrorCause(classified); got != dataframeerrors.PublicMessage(classified) {
			t.Fatalf("classified cause = %q, want the public message", got)
		}
	})
}
