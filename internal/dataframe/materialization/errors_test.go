package materialization

import (
	"errors"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func TestBackendCallErrorPreservesTypedAndWrapsUnknown(t *testing.T) {
	typed := dataframeerrors.NewError(dataframeerrors.CodeInvalidCursor, "")
	if got := backendCallError(typed); got != typed {
		t.Fatalf("typed error was replaced: %v", got)
	}

	cause := errors.New("driver details")
	got := backendCallError(cause)
	user, ok := dataframeerrors.AsUserError(got)
	if !ok || user.Code() != string(dataframeerrors.CodeBackendUnavailable) || !user.Retryable() {
		t.Fatalf("backend error = %#v", got)
	}
	if !errors.Is(got, cause) {
		t.Fatal("backend cause was not preserved")
	}
}
