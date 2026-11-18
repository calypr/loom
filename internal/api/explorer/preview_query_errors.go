package explorerapi

import (
	"net/http"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"

	executionarango "github.com/calypr/loom/internal/dataframe/execution/arango"

	"context"
	"errors"

	"github.com/calypr/loom/internal/explorer"
)

func PreserveConfiguredQueryError(err error) error {
	err = executionarango.ClassifyDataframeQueryError(err)
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return previewRouteError(err)
	}
	if userErr, ok := dataframeerrors.AsUserError(err); ok {
		switch userErr.Code() {
		case string(dataframeerrors.CodeUnauthenticated):
			return &explorer.AuthoringError{Status: http.StatusUnauthorized, Diagnostic: explorer.AuthoringDiagnostic{
				Severity: "ERROR", Stage: "preview", Code: userErr.Code(), Message: dataframeerrors.PublicMessage(err),
			}, Cause: err}
		case string(dataframeerrors.CodeForbidden), string(dataframeerrors.CodeUnauthorizedProject):
			return &explorer.AuthoringError{Status: http.StatusForbidden, Diagnostic: explorer.AuthoringDiagnostic{
				Severity: "ERROR", Stage: "preview", Code: userErr.Code(), Message: dataframeerrors.PublicMessage(err),
			}, Cause: err}
		}
		return previewRouteError(err)
	}
	return err
}
