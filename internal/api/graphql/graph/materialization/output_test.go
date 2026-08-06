package materializationapi

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

func TestFederationMetadataMapping(t *testing.T) {
	value := Model(dfpublished.Materialization{
		ID: "federated", Name: "DocumentReference", Selector: dfpublished.DataframeSelector{Recipe: "documents", TranslationVersion: "v2", Output: "DocumentReference"},
		ActiveContractVersion: "v1", Availability: dfpublished.FederationDegraded, ExpectedProjects: 2,
		IncludedProjects: 1,
		ProjectStatuses:  []dfpublished.ProjectStatus{{ProjectID: "allowed", State: dfpublished.ProjectCurrent}, {ProjectID: "missing", State: dfpublished.ProjectMissing}},
	})
	if value.Selector == nil || value.Selector.TranslationVersion != "v2" || value.ActiveContractVersion == nil || *value.ActiveContractVersion != "v1" {
		t.Fatalf("selector metadata = %#v", value)
	}
	if value.Availability == nil || *value.Availability != "DEGRADED" || value.Completeness == nil || *value.Completeness != .5 {
		t.Fatalf("availability metadata = %#v", value)
	}
	if len(value.ProjectStatuses) != 2 {
		t.Fatalf("statuses = %#v", value.ProjectStatuses)
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
