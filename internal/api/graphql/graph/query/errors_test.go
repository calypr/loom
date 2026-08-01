package queryapi

import (
	"errors"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func TestQueryBackendPreservesAuthorizationClassification(t *testing.T) {
	err := queryBackend(errors.Join(authscope.ErrForbidden, errors.New("private scope detail")))
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeForbidden) || userErr.Retryable() {
		t.Fatalf("error = %#v, want non-retryable FORBIDDEN", err)
	}
}

func TestQueryBackendWrapsUnknownCauseAsRetryable(t *testing.T) {
	cause := errors.New("arango driver detail")
	err := queryBackend(cause)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) || !userErr.Retryable() {
		t.Fatalf("error = %#v, want retryable BACKEND_UNAVAILABLE", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("backend cause was not preserved")
	}
}
