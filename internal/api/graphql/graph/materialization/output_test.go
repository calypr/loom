package materializationapi

import (
	"errors"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func TestPersistedFailureRedactsLegacyText(t *testing.T) {
	message, code, retryable := PersistedFailure("clickhouse table=secret", "", false)
	if message == nil || *message != "internal server error" || code == nil || *code != string(dataframeerrors.CodeInternalError) || retryable == nil || *retryable {
		t.Fatalf("failure = %v/%v/%v", message, code, retryable)
	}
}

func TestAggregateRowsResultReturnsEncodingError(t *testing.T) {
	_, err := AggregateRowsResult([]map[string]any{{"bad": func() {}}})
	if err == nil {
		t.Fatal("expected encoding error")
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeOutputEncodingFailed) {
		t.Fatalf("error = %v", err)
	}
	if errors.Is(err, dataframeerrors.ErrBackendUnavailable) {
		t.Fatal("encoding failure was classified as backend failure")
	}
}
