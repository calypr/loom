package dataframe

import (
	"errors"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dfpublished "github.com/calypr/loom/internal/dataframe/published"
)

func TestPersistedFailureRedactsLegacyText(t *testing.T) {
	message, code, retryable := PersistedFailure("clickhouse table=secret", "", false)
	if message == nil || *message != "internal server error" || code == nil || *code != string(dataframeerrors.CodeInternalError) || retryable == nil || *retryable {
		t.Fatalf("failure = %v/%v/%v", message, code, retryable)
	}
}

func TestProjectMetadataMapping(t *testing.T) {
	value := Model(dfpublished.Materialization{
		ID: "execution:DocumentReference", Name: "DocumentReference", Project: "P1", DatasetGeneration: "generation", State: dfpublished.StateReady,
		Selector: dfpublished.DataframeSelector{Recipe: "documents", TranslationVersion: "v2", Output: "DocumentReference"},
	})
	if value.Selector == nil || value.Selector.TranslationVersion != "v2" {
		t.Fatalf("selector metadata = %#v", value)
	}
	if value.ProjectID != "P1" || value.DatasetGeneration != "generation" {
		t.Fatalf("project metadata = %#v", value)
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
