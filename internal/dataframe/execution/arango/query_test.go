package arango

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func TestClassifyDataframeQueryErrorPreservesArangoMemoryLimit(t *testing.T) {
	driverErr := shared.ArangoError{
		HasError:     true,
		Code:         500,
		ErrorNum:     shared.ErrResourceLimit,
		ErrorMessage: "AQL: query would use more memory than allowed",
	}

	err := ClassifyDataframeQueryError(driverErr)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("ClassifyDataframeQueryError() = %v, want dataframe user error", err)
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

	err := ClassifyDataframeQueryError(driverErr)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeQueryBackendOutOfMemory) {
		t.Fatalf("ClassifyDataframeQueryError() = %#v, want %s", userErr, dataframeerrors.CodeQueryBackendOutOfMemory)
	}
}

func TestClassifyDataframeQueryErrorPreservesOtherArangoResourceLimit(t *testing.T) {
	driverErr := shared.ArangoError{
		HasError:     true,
		Code:         500,
		ErrorNum:     shared.ErrResourceLimit,
		ErrorMessage: "resource limit exceeded",
	}

	err := ClassifyDataframeQueryError(driverErr)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeQueryResourceLimitExceeded) {
		t.Fatalf("ClassifyDataframeQueryError() = %#v, want %s", userErr, dataframeerrors.CodeQueryResourceLimitExceeded)
	}
}

func TestClassifyDataframeQueryErrorLeavesOtherFailuresUntouched(t *testing.T) {
	want := errors.New("visitor failed")
	if got := ClassifyDataframeQueryError(want); !errors.Is(got, want) || got != want {
		t.Fatalf("ClassifyDataframeQueryError() = %v, want original error", got)
	}
}

func TestClassifyDataframeQueryErrorPreservesRelationshipCardinalityViolation(t *testing.T) {
	driverErr := shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert, ErrorMessage: "AQL: RELATIONSHIP_CARDINALITY_VIOLATION (while executing)"}
	userErr, ok := dataframeerrors.AsUserError(ClassifyDataframeQueryError(driverErr))
	if !ok || userErr.Code() != string(dataframeerrors.CodeRelationshipCardinalityViolation) || userErr.Retryable() {
		t.Fatalf("classified error=%#v", userErr)
	}
}

func TestClassifyDataframeQueryErrorPreservesUnlistedPivotCategory(t *testing.T) {
	driverErr := shared.ArangoError{
		HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert,
		ErrorMessage: "AQL: TABLE_PIVOT_UNLISTED_CATEGORY (while executing)",
	}
	err := ClassifyDataframeQueryError(driverErr)
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
	err := ClassifyDataframeQueryError(driverErr)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeTablePivotCellCardinality) || userErr.Retryable() {
		t.Fatalf("classified error=%#v, want non-retryable %s", userErr, dataframeerrors.CodeTablePivotCellCardinality)
	}
	if !errors.Is(err, driverErr) {
		t.Fatal("classified error did not preserve the Arango cause")
	}
}

func TestClassifyDataframeQueryErrorPreservesUnassignedExplicitGroup(t *testing.T) {
	driverErr := shared.ArangoError{
		HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert,
		ErrorMessage: "AQL: EXPLICIT_GROUP_UNASSIGNED_MEMBER (while executing)",
	}
	err := ClassifyDataframeQueryError(driverErr)
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
	if got := ClassifyDataframeQueryError(driverErr); got != driverErr {
		t.Fatalf("ClassifyDataframeQueryError() = %v, want original unrecognized assertion", got)
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
			userErr, ok := dataframeerrors.AsUserError(ClassifyDataframeQueryError(driverErr))
			if !ok || userErr.Code() != string(code) || userErr.Retryable() {
				t.Fatalf("classified error=%#v", userErr)
			}
		})
	}
}

func TestSnapshotPreviewQueryHasRuntimeBudget(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want time.Duration
	}{
		{"ordinary", context.Background(), ExplorerPreviewTimeout},
		{"pivot", WithPivotPreviewQueryBudget(context.Background()), PivotCategoryScanTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := WithPreviewQueryTimeout(func(ctx context.Context, _ string, _ int, _ map[string]any, _ func(map[string]any) error) error {
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Fatal("snapshot query has no deadline")
				}
				remaining := time.Until(deadline)
				if remaining > tc.want || remaining < tc.want-time.Second {
					t.Fatalf("query budget = %s, want %s", remaining, tc.want)
				}
				return nil
			})
			if err := query(tc.ctx, "RETURN 1", 1, nil, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
