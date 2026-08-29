package graphqlapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func TestErrorChainIncludesWrappedBackendCause(t *testing.T) {
	err := dataframeerrors.Wrap(errors.New("clickhouse connection refused"), dataframeerrors.CodeBackendUnavailable, "")

	chain := errorChain(err)
	if !strings.Contains(chain, "the dataframe backend is temporarily unavailable") {
		t.Fatalf("error chain omitted public error: %q", chain)
	}
	if !strings.Contains(chain, "clickhouse connection refused") {
		t.Fatalf("error chain omitted backend cause: %q", chain)
	}
}

func TestGraphQLBackendFailureUsesServiceUnavailable(t *testing.T) {
	status := serveCapturedGraphQL(t, `{"errors":[{"extensions":{"code":"BACKEND_UNAVAILABLE","retryable":true}}],"data":null}`)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", status, http.StatusServiceUnavailable)
	}
}

func TestGraphQLValidationFailureUsesBadRequest(t *testing.T) {
	status := serveCapturedGraphQL(t, `{"errors":[{"extensions":{"code":"GRAPHQL_VALIDATION_FAILED","retryable":false}}]}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
}

func TestGraphQLPartialDataKeepsSuccessfulTransportStatus(t *testing.T) {
	status := serveCapturedGraphQL(t, `{"errors":[{"extensions":{"code":"DATASET_NOT_FOUND","retryable":false}}],"data":{"optionalDataset":null,"healthyField":"value"}}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}
}

func serveCapturedGraphQL(t *testing.T, body string) int {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	})
	recorder := httptest.NewRecorder()
	serveGraphQLResponse(recorder, httptest.NewRequest(http.MethodPost, "/graphql/graph", nil), next)
	return recorder.Code
}
