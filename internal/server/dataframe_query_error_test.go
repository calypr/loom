package server

import (
	"errors"
	"net/http"
	"testing"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/explorer"
)

func TestClassifyDataframeQueryErrorPreservesArangoMemoryLimit(t *testing.T) {
	driverErr := shared.ArangoError{
		HasError:     true,
		Code:         500,
		ErrorNum:     shared.ErrResourceLimit,
		ErrorMessage: "AQL: query would use more memory than allowed",
	}

	err := classifyDataframeQueryError(driverErr)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("classifyDataframeQueryError() = %v, want dataframe user error", err)
	}
	if userErr.Code() != string(dataframeerrors.CodeQueryMemoryLimitExceeded) {
		t.Fatalf("code = %q, want %q", userErr.Code(), dataframeerrors.CodeQueryMemoryLimitExceeded)
	}
	if got := userErr.Details()["backend"]; got != "arangodb" {
		t.Fatalf("backend = %v, want arangodb", got)
	}
	if !errors.Is(err, driverErr) {
		t.Fatal("classified error did not preserve the driver cause")
	}
}

func TestClassifyDataframeQueryErrorPreservesArangoOutOfMemory(t *testing.T) {
	driverErr := shared.ArangoError{
		HasError:     true,
		Code:         500,
		ErrorNum:     shared.ErrOutOfMemory,
		ErrorMessage: "out of memory",
	}

	err := classifyDataframeQueryError(driverErr)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeQueryBackendOutOfMemory) {
		t.Fatalf("classifyDataframeQueryError() = %#v, want %s", userErr, dataframeerrors.CodeQueryBackendOutOfMemory)
	}
}

func TestClassifyDataframeQueryErrorPreservesOtherArangoResourceLimit(t *testing.T) {
	driverErr := shared.ArangoError{
		HasError:     true,
		Code:         500,
		ErrorNum:     shared.ErrResourceLimit,
		ErrorMessage: "resource limit exceeded",
	}

	err := classifyDataframeQueryError(driverErr)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeQueryResourceLimitExceeded) {
		t.Fatalf("classifyDataframeQueryError() = %#v, want %s", userErr, dataframeerrors.CodeQueryResourceLimitExceeded)
	}
}

func TestClassifyDataframeQueryErrorLeavesOtherFailuresUntouched(t *testing.T) {
	want := errors.New("visitor failed")
	if got := classifyDataframeQueryError(want); !errors.Is(got, want) || got != want {
		t.Fatalf("classifyDataframeQueryError() = %v, want original error", got)
	}
}

func TestClassifyDataframeQueryErrorPreservesRelationshipCardinalityViolation(t *testing.T) {
	driverErr := shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert, ErrorMessage: "AQL: RELATIONSHIP_CARDINALITY_VIOLATION (while executing)"}
	userErr, ok := dataframeerrors.AsUserError(classifyDataframeQueryError(driverErr))
	if !ok || userErr.Code() != string(dataframeerrors.CodeRelationshipCardinalityViolation) || userErr.Retryable() {
		t.Fatalf("classified error=%#v", userErr)
	}
}

func TestClassifyDataframeQueryErrorPreservesUnlistedPivotCategory(t *testing.T) {
	driverErr := shared.ArangoError{
		HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert,
		ErrorMessage: "AQL: TABLE_PIVOT_UNLISTED_CATEGORY (while executing)",
	}
	err := classifyDataframeQueryError(driverErr)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != "TABLE_PIVOT_UNLISTED_CATEGORY" || userErr.Retryable() {
		t.Fatalf("classified error=%#v, want non-retryable TABLE_PIVOT_UNLISTED_CATEGORY", userErr)
	}
	if !errors.Is(err, driverErr) {
		t.Fatal("classified error did not preserve the Arango cause")
	}
}

func TestClassifyDataframeQueryErrorPreservesPivotCellCardinality(t *testing.T) {
	driverErr := shared.ArangoError{
		HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert,
		ErrorMessage: "AQL: TABLE_PIVOT_CELL_CARDINALITY (while executing)",
	}
	err := classifyDataframeQueryError(driverErr)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeTablePivotCellCardinality) || userErr.Retryable() {
		t.Fatalf("classified error=%#v, want non-retryable %s", userErr, dataframeerrors.CodeTablePivotCellCardinality)
	}
	if !errors.Is(err, driverErr) {
		t.Fatal("classified error did not preserve the Arango cause")
	}
}

func TestClassifyDataframeQueryErrorPreservesEmptyConstructionExpansion(t *testing.T) {
	driverErr := shared.ArangoError{
		HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert,
		ErrorMessage: "AQL: CONSTRUCTION_EXPANSION_EMPTY: related expansion construction related_1 has no related records for row row-17 (while executing)",
	}
	err := classifyDataframeQueryError(driverErr)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeConstructionExpansionEmpty) || userErr.Retryable() {
		t.Fatalf("classified error=%#v, want non-retryable %s", userErr, dataframeerrors.CodeConstructionExpansionEmpty)
	}
	if !errors.Is(err, driverErr) {
		t.Fatal("classified error did not preserve the Arango cause")
	}
	var routeErr *explorer.AuthoringError
	if !errors.As(previewRouteError(err), &routeErr) || routeErr.Status != http.StatusUnprocessableEntity ||
		routeErr.Diagnostic.Code != string(dataframeerrors.CodeConstructionExpansionEmpty) {
		t.Fatalf("preview route error = %#v, want non-retryable construction expansion diagnostic with HTTP 422", routeErr)
	}
}

func TestClassifyDataframeQueryErrorPreservesUnassignedExplicitGroup(t *testing.T) {
	driverErr := shared.ArangoError{
		HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert,
		ErrorMessage: "AQL: EXPLICIT_GROUP_UNASSIGNED_MEMBER (while executing)",
	}
	err := classifyDataframeQueryError(driverErr)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeExplicitGroupUnassignedMember) || userErr.Retryable() {
		t.Fatalf("classified error=%#v, want non-retryable %s", userErr, dataframeerrors.CodeExplicitGroupUnassignedMember)
	}
	if !errors.Is(err, driverErr) {
		t.Fatal("classified error did not preserve the Arango cause")
	}
}

func TestClassifyDataframeQueryErrorDoesNotInferEmptyExpansionFromMessage(t *testing.T) {
	driverErr := shared.ArangoError{
		HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert,
		ErrorMessage: "AQL: row expansion construction expand_tags has no items for row row-17 (while executing)",
	}
	if got := classifyDataframeQueryError(driverErr); got != driverErr {
		t.Fatalf("classifyDataframeQueryError() = %v, want original unrecognized assertion", got)
	}
}

func TestClassifyDataframeQueryErrorPreservesTemporalAssertions(t *testing.T) {
	tests := []dataframeerrors.ErrorCode{
		dataframeerrors.CodeTemporalAnchorInvalid,
		dataframeerrors.CodeTemporalPrecisionUnsupported,
		dataframeerrors.CodeTemporalTieAmbiguous,
	}
	for _, code := range tests {
		t.Run(string(code), func(t *testing.T) {
			driverErr := shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert, ErrorMessage: "AQL: " + string(code) + " (while executing)"}
			userErr, ok := dataframeerrors.AsUserError(classifyDataframeQueryError(driverErr))
			if !ok || userErr.Code() != string(code) || userErr.Retryable() {
				t.Fatalf("classified error=%#v", userErr)
			}
		})
	}
}

func TestClassifyGroupedColumnMultiplicity(t *testing.T) {
	cause := shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert, ErrorMessage: "AQL: CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES (while executing)"}
	err := classifyDataframeQueryError(cause)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeConstructionRowValueMultipleValues) || userErr.Retryable() {
		t.Fatalf("expected actionable grouped-value error, got %v", err)
	}
	var routeErr *explorer.AuthoringError
	if !errors.As(previewRouteError(err), &routeErr) || routeErr.Status != 422 {
		t.Fatalf("expected 422 grouped-value error, got %v", routeErr)
	}
	if !errors.Is(err, cause) {
		t.Fatal("driver cause lost")
	}
}
