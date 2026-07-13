package httpapi

import (
	"errors"
	"net/http"
	"testing"

	"github.com/calypr/loom/internal/dataframe"
)

func TestMapDataframeErrorUsesSharedCodeAndStatus(t *testing.T) {
	mapped := MapDataframeError(dataframe.NewError(
		dataframe.CodeUnauthorizedProject,
		"do not expose this message",
		dataframe.WithDetails(map[string]any{"project": "secret", "collection": "Patient"}),
	), "request-789")
	if mapped.Status != http.StatusForbidden {
		t.Fatalf("status = %d", mapped.Status)
	}
	if mapped.Body.Error.Code != string(dataframe.CodeUnauthorizedProject) {
		t.Fatalf("code = %q", mapped.Body.Error.Code)
	}
	if mapped.Body.Error.Message != "the requested project is not available" {
		t.Fatalf("message = %q", mapped.Body.Error.Message)
	}
	if mapped.Body.Error.RequestID != "request-789" {
		t.Fatalf("request ID = %q", mapped.Body.Error.RequestID)
	}
	if _, ok := mapped.Body.Error.Details["collection"]; ok {
		t.Fatalf("collection leaked: %#v", mapped.Body.Error.Details)
	}
}

func TestMapDataframeErrorMapsOperationalConditions(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "backend", err: dataframe.ErrBackendUnavailable, status: http.StatusServiceUnavailable, code: string(dataframe.CodeBackendUnavailable)},
		{name: "generation", err: dataframe.NewError(dataframe.CodeDatasetGenerationChanged, ""), status: http.StatusConflict, code: string(dataframe.CodeDatasetGenerationChanged)},
		{name: "internal", err: errors.New("driver AQL details"), status: http.StatusInternalServerError, code: string(dataframe.CodeInternalError)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			mapped := MapDataframeError(test.err, "request")
			if mapped.Status != test.status || mapped.Body.Error.Code != test.code {
				t.Fatalf("mapped = %#v", mapped)
			}
		})
	}
}

func TestMappedErrorPreservesCauseForLogging(t *testing.T) {
	cause := errors.New("backend")
	mapped := MapDataframeError(dataframe.Wrap(cause, dataframe.CodeBackendUnavailable, "private"), "request")
	if !errors.Is(mapped, cause) {
		t.Fatal("mapped error did not preserve cause")
	}
}
