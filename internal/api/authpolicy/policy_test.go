package authpolicy

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func TestClassifyAuthorizationSentinelsByOperation(t *testing.T) {
	for _, test := range []struct {
		operation Operation
		wantCode  dataframeerrors.ErrorCode
	}{
		{OperationQueryRead, dataframeerrors.CodeForbidden},
		{OperationDataframeRead, dataframeerrors.CodeForbidden},
		{OperationRecipeControl, dataframeerrors.CodeUnauthorizedProject},
		{OperationRecipeAuthorization, dataframeerrors.CodeUnauthorizedProject},
		{OperationConcealedLookup, dataframeerrors.CodeRecipeExecutionNotFound},
	} {
		t.Run(string(test.operation), func(t *testing.T) {
			err := Classify(errors.Join(authscope.ErrForbidden, errors.New("private detail")), test.operation)
			userErr, ok := dataframeerrors.AsUserError(err)
			if !ok || userErr.Code() != string(test.wantCode) || userErr.Retryable() {
				t.Fatalf("classified = %#v, want %s non-retryable", err, test.wantCode)
			}
		})
	}
}

func TestClassifyAuthorizationBackendOutageIsRetryableInEveryContext(t *testing.T) {
	for _, operation := range []Operation{OperationQueryRead, OperationDataframeRead, OperationRecipeControl, OperationRecipeAuthorization, OperationConcealedLookup} {
		err := Classify(authscope.ErrAuthorizationBackendUnavailable, operation)
		userErr, ok := dataframeerrors.AsUserError(err)
		if !ok || userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) || !userErr.Retryable() {
			t.Fatalf("%s classified = %#v, want retryable BACKEND_UNAVAILABLE", operation, err)
		}
	}
	if err := Classify(context.Canceled, OperationQueryRead); dataframeerrors.Normalize(err).Code() != string(dataframeerrors.CodeClientCanceled) {
		t.Fatalf("canceled classification = %#v, want CLIENT_CANCELED", err)
	}
}
