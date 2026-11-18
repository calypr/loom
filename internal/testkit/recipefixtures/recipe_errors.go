package recipefixtures

import (
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func AssertTypedProjectRequired(t *testing.T, err error, site string) dataframeerrors.UserError {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: whitespace-only project accepted; want typed PROJECT_REQUIRED", site)
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("%s: error escaped as raw (INTERNAL fault would be synthesized): %v", site, err)
	}
	if userErr.Code() != string(dataframeerrors.CodeProjectRequired) {
		t.Fatalf("%s: code=%s want %s (err=%v)", site, userErr.Code(), dataframeerrors.CodeProjectRequired, err)
	}
	if userErr.Retryable() {
		t.Fatalf("%s: PROJECT_REQUIRED must stay non-retryable", site)
	}
	if message := dataframeerrors.PublicMessage(err); message != dataframeerrors.PublicMessage(dataframeerrors.NewError(dataframeerrors.CodeProjectRequired, "")) {
		t.Fatalf("%s: message=%q want default publish text", site, message)
	}
	return userErr
}
