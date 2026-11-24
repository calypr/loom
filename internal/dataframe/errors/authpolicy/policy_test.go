package authpolicy

import (
	"context"
	"errors"
	"testing"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
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

func TestClassifyExplorerAuthorizationPreservesLegacyStatusPrecedence(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		code      dataframeerrors.ErrorCode
		retryable bool
	}{
		{"denial wins joined outage", errors.Join(authscope.ErrForbidden, authscope.ErrAuthorizationBackendUnavailable), dataframeerrors.CodeForbidden, false},
		{"unauthenticated remains forbidden", authscope.ErrUnauthenticated, dataframeerrors.CodeForbidden, false},
		{"backend sentinel", authscope.ErrAuthorizationBackendUnavailable, dataframeerrors.CodeBackendUnavailable, true},
		{"deadline", context.DeadlineExceeded, dataframeerrors.CodeBackendUnavailable, true},
		{"cancelled is not a storage outage", context.Canceled, dataframeerrors.CodeForbidden, false},
		{"pretyped backend code without sentinel remains denial", dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "pretyped"), dataframeerrors.CodeForbidden, false},
		{"pretyped error retaining backend sentinel is outage", dataframeerrors.Wrap(authscope.ErrAuthorizationBackendUnavailable, dataframeerrors.CodeForbidden, "typed"), dataframeerrors.CodeBackendUnavailable, true},
		{"Arango HTTP 401 is permission", shared.ArangoError{Code: shared.ErrHttpUnauthorized}, dataframeerrors.CodeForbidden, false},
		{"Arango HTTP 403 is permission", shared.ArangoError{Code: shared.ErrHttpForbidden}, dataframeerrors.CodeForbidden, false},
		{"Arango 11 is permission", shared.ArangoError{ErrorNum: shared.ErrForbidden}, dataframeerrors.CodeForbidden, false},
		{"Arango pointer HTTP 401 is permission", &shared.ArangoError{Code: shared.ErrHttpUnauthorized}, dataframeerrors.CodeForbidden, false},
		{"Arango pointer HTTP 403 is permission", &shared.ArangoError{Code: shared.ErrHttpForbidden}, dataframeerrors.CodeForbidden, false},
		{"Arango pointer 11 is permission", &shared.ArangoError{ErrorNum: shared.ErrForbidden}, dataframeerrors.CodeForbidden, false},
		{"Arango query 400 is outage", shared.ArangoError{Code: 400, ErrorNum: 1552}, dataframeerrors.CodeBackendUnavailable, true},
		{"Arango pointer query fault is outage", &shared.ArangoError{Code: 400, ErrorNum: 1552}, dataframeerrors.CodeBackendUnavailable, true},
		{"unknown failure stays denial", errors.New("unknown authorizer error"), dataframeerrors.CodeForbidden, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			classified := Classify(test.err, OperationExplorerAuthorization)
			userErr, ok := dataframeerrors.AsUserError(classified)
			if !ok || userErr.Code() != string(test.code) || userErr.Retryable() != test.retryable {
				t.Fatalf("classified=%#v, want code=%s retryable=%v", classified, test.code, test.retryable)
			}
			if !errors.Is(classified, test.err) {
				t.Fatalf("cause chain lost original error: %v", classified)
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
