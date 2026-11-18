package explorerapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"

	"github.com/calypr/loom/internal/explorer"
)

func TestConfiguredQueryErrorMappingPreservesTypedSemantics(t *testing.T) {
	canceled := fmt.Errorf("query canceled: %w", context.Canceled)
	var canceledAuthoring *explorer.AuthoringError
	if got := PreserveConfiguredQueryError(canceled); !errors.As(got, &canceledAuthoring) || canceledAuthoring.Status != 499 || canceledAuthoring.Diagnostic.Code != "CLIENT_CANCELED" {
		t.Fatalf("canceled query error = %#v, want redacted 499 CLIENT_CANCELED", got)
	}

	deadline := fmt.Errorf("query deadline: %w", context.DeadlineExceeded)
	if got := PreserveConfiguredQueryError(deadline); !errors.Is(got, context.DeadlineExceeded) || got != deadline {
		t.Fatalf("deadline query error = %#v, want original lifecycle-classifiable deadline", got)
	}

	userErr := dataframeerrors.NewError(dataframeerrors.CodeConstructionExpansionEmpty, "")
	var userAuthoring *explorer.AuthoringError
	if got := PreserveConfiguredQueryError(userErr); !errors.As(got, &userAuthoring) || userAuthoring.Status != http.StatusUnprocessableEntity || userAuthoring.Diagnostic.Code != string(dataframeerrors.CodeConstructionExpansionEmpty) {
		t.Fatalf("typed query error = %#v, want mapped 422 user error", got)
	}

	unknown := errors.New("private backend details")
	if got := PreserveConfiguredQueryError(unknown); got != unknown {
		t.Fatalf("untyped query error = %#v, want original lifecycle-classifiable error", got)
	}
}

func TestConfiguredQueryErrorMappingKeepsAuthStatusAndSafeMessage(t *testing.T) {
	tests := []struct {
		code   dataframeerrors.ErrorCode
		status int
	}{
		{code: dataframeerrors.CodeUnauthenticated, status: http.StatusUnauthorized},
		{code: dataframeerrors.CodeForbidden, status: http.StatusForbidden},
		{code: dataframeerrors.CodeUnauthorizedProject, status: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(string(test.code), func(t *testing.T) {
			cause := dataframeerrors.NewError(test.code, "private authorization detail")
			mapped := PreserveConfiguredQueryError(cause)
			var authoringErr *explorer.AuthoringError
			if !errors.As(mapped, &authoringErr) || authoringErr.Status != test.status || authoringErr.Diagnostic.Code != string(test.code) {
				t.Fatalf("mapped auth error = %#v, want status %d and code %s", mapped, test.status, test.code)
			}
			if authoringErr.Diagnostic.Message != dataframeerrors.PublicMessage(cause) || errors.Unwrap(authoringErr) != cause {
				t.Fatalf("mapped auth message/cause = %q/%v", authoringErr.Diagnostic.Message, errors.Unwrap(authoringErr))
			}
			if strings.Contains(authoringErr.Error(), "private authorization detail") {
				t.Fatalf("mapped auth error exposes private message: %v", authoringErr)
			}
		})
	}
}
