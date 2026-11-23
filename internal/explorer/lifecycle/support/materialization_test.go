package support_test

import (
	"errors"
	"strings"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
)

func TestClassifyMaterializationErrorPreservesTemporalResolutionFailures(t *testing.T) {
	for _, code := range []dataframeerrors.ErrorCode{
		dataframeerrors.CodeConstructionExpansionEmpty,
		dataframeerrors.CodeTablePivotUnlistedCategory,
		dataframeerrors.CodeTemporalAnchorInvalid,
		dataframeerrors.CodeTemporalPrecisionUnsupported,
		dataframeerrors.CodeTemporalTieAmbiguous,
	} {
		t.Run(string(code), func(t *testing.T) {
			err := support.ClassifyMaterializationError("materialize", dataframeerrors.NewError(code, "private"))
			var lifecycleErr *ports.Error
			if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ports.ClassUnprocessable || lifecycleErr.Code != string(code) {
				t.Fatalf("classified error = %#v, want unprocessable %s", lifecycleErr, code)
			}
			if !strings.Contains(lifecycleErr.Message, "active revision was retained") {
				t.Fatalf("message = %q", lifecycleErr.Message)
			}
			if code == dataframeerrors.CodeConstructionExpansionEmpty &&
				(!strings.Contains(lifecycleErr.Message, "Drop the original row") ||
					!strings.Contains(lifecycleErr.Message, "Keep the row with a missing item") ||
					!strings.Contains(lifecycleErr.Message, "Leave that current row out") ||
					!strings.Contains(lifecycleErr.Message, "Keep that current row once, with no related record ID") ||
					!strings.Contains(lifecycleErr.Message, "Choose the empty-list or no-match policy")) {
				t.Fatalf("empty expansion message = %q, want list and related-record policy guidance", lifecycleErr.Message)
			}
			if code == dataframeerrors.CodeTablePivotUnlistedCategory && !strings.Contains(lifecycleErr.Message, "select all discovered categories or filter rows before Pivot") {
				t.Fatalf("unlisted pivot category message = %q, want category-selection guidance", lifecycleErr.Message)
			}
		})
	}
}

func TestMaterializationResourceErrorDistinguishesDatabaseOutOfMemory(t *testing.T) {
	cause := dataframeerrors.NewError(
		dataframeerrors.CodeQueryBackendOutOfMemory,
		"",
		dataframeerrors.WithDetails(map[string]any{"backend": "arangodb"}),
	)

	err := support.ClassifyMaterializationError("repository_publish", cause)
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("materializationMemoryError() = %v, want lifecycle error", err)
	}
	if lifecycleErr.Class != ports.ClassUnavailable || lifecycleErr.Code != "QUERY_BACKEND_OUT_OF_MEMORY" {
		t.Fatalf("error = %#v, want unavailable QUERY_BACKEND_OUT_OF_MEMORY", lifecycleErr)
	}
	if !strings.Contains(lifecycleErr.Message, "ran out of memory") || strings.Contains(lifecycleErr.Message, "query-memory-limit") {
		t.Fatalf("message = %q, want database OOM guidance without query-limit advice", lifecycleErr.Message)
	}
}

func TestMaterializationResourceErrorReportsOtherResourceLimitWithoutMemoryAdvice(t *testing.T) {
	cause := dataframeerrors.NewError(dataframeerrors.CodeQueryResourceLimitExceeded, "")

	err := support.ClassifyMaterializationError("materialize", cause)
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != "QUERY_RESOURCE_LIMIT_EXCEEDED" {
		t.Fatalf("support.ClassifyMaterializationError() = %#v, want QUERY_RESOURCE_LIMIT_EXCEEDED", lifecycleErr)
	}
	if !strings.Contains(lifecycleErr.Message, "configured resource limit") || strings.Contains(lifecycleErr.Message, "query-memory-limit") {
		t.Fatalf("message = %q, want generic resource-limit guidance", lifecycleErr.Message)
	}
}
