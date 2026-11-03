package spec

import (
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

// The explorer row-source boundary (explorer/lifecycle source_row.go) calls
// these validators without a prior schema gate, so their rejections are
// directly user-facing and must arrive typed and non-retryable.

func assertTypedUserFault(t *testing.T, err error, wantMessage string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected rejection %q, got success", wantMessage)
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("user-input rejection escaped untyped (route would serve INTERNAL_ERROR 500): %v", err)
	}
	if userErr.Code() != string(dataframeerrors.CodeInvalidRequest) || userErr.Retryable() {
		t.Fatalf("rejection = code %s retryable %v (%v)", userErr.Code(), userErr.Retryable(), err)
	}
	if userErr.Error() != wantMessage {
		t.Fatalf("message drifted: got %q, want %q", userErr.Error(), wantMessage)
	}
}

func TestRowGrainValidateRejectsEmptyAsUserFault(t *testing.T) {
	assertTypedUserFault(t, RowGrain("").Validate(), "row grain is required")
}

func TestValidateRootGrainRejectsUnrepresentedRootAsUserFault(t *testing.T) {
	assertTypedUserFault(t, ValidateRootGrain("NotAThing", RowGrainResource), `root resource type "NotAThing" is not represented by the active generated FHIR schema`)
}
