package explorerapi

import (
	"errors"
	"net/http"
	"testing"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	executionarango "github.com/calypr/loom/internal/dataframe/execution/arango"
	"github.com/calypr/loom/internal/explorer"
)

func TestClassifyDataframeQueryErrorPreservesEmptyConstructionExpansion(t *testing.T) {
	driverErr := shared.ArangoError{
		HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert,
		ErrorMessage: "AQL: CONSTRUCTION_EXPANSION_EMPTY: related expansion construction related_1 has no related records for row row-17 (while executing)",
	}
	err := executionarango.ClassifyDataframeQueryError(driverErr)
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

func TestClassifyGroupedColumnMultiplicity(t *testing.T) {
	cause := shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert, ErrorMessage: "AQL: CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES (while executing)"}
	err := executionarango.ClassifyDataframeQueryError(cause)
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
