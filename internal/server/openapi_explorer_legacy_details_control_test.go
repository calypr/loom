package server

// Durable control (F-B28, round 43): docs/loom-reliability-contract.md states
// retryability is explicit and clients must not infer it from an HTTP status or
// message. The legacy Explorer error encoder must therefore carry
// lifecycle.Error.Details (e.g. retryable:false for a permanent release
// activation miss) into the marshalled body instead of dropping it.

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func marshalLegacyBody(t *testing.T, err error) (map[string]any, int) {
	t.Helper()
	status, legacy := explorerLegacyResponse(err)
	// Route wrappers embed this value verbatim into the status-specific JSON
	// response, so marshalling it reproduces the client-visible envelope.
	raw, merr := json.Marshal(legacy)
	if merr != nil {
		t.Fatalf("marshal legacy envelope: %v", merr)
	}
	var envelope map[string]any
	if uerr := json.Unmarshal(raw, &envelope); uerr != nil {
		t.Fatalf("unmarshal legacy envelope: %v", uerr)
	}
	body, ok := envelope["error"].(map[string]any)
	if !ok {
		t.Fatalf("legacy error envelope is not a structured object (client cannot read explicit retryability): %s", raw)
	}
	return body, status
}

func TestLegacyFailureCarriesExplicitRetryabilityForActivationMiss(t *testing.T) {
	// Same value repository_publish.go:84-88 builds for a typed activation miss.
	miss := &lifecycle.Error{Class: lifecycle.ClassConflict, Stage: "repository_publish", Code: "RELEASE_ACTIVATION_FAILED", Message: "activate published ExplorerConfigV2: requirements unmet", Details: map[string]any{"retryable": false}, Cause: errors.New("release requirements unmet")}
	body, status := marshalLegacyBody(t, lifecycleLegacyFailure(miss))
	if status != http.StatusConflict {
		t.Fatalf("activation miss status = %d, want 409", status)
	}
	if body["code"] != "RELEASE_ACTIVATION_FAILED" {
		t.Fatalf("legacy body lost the lifecycle code: %v", body)
	}
	if retryBlock, ok := body["details"].(map[string]any); !ok || retryBlock["retryable"] != false {
		t.Fatalf("F-B28 RED: explicit retryable=false from lifecycle.Error.Details never reached the client body: %v", body)
	}
}

func TestLegacyFailureKeepsUnrelatedErrorsUnwidened(t *testing.T) {
	// Plain (non-lifecycle) errors keep the existing structured-INTERNAL_ERROR
	// envelope shape: no invented retryability keys.
	body, status := marshalLegacyBody(t, lifecycleLegacyFailure(errors.New("store exploded")))
	if status != http.StatusInternalServerError {
		t.Fatalf("plain error status = %d, want 500", status)
	}
	if body["code"] != "INTERNAL_ERROR" {
		t.Fatalf("plain error code = %v, want INTERNAL_ERROR", body["code"])
	}
	if _, present := body["retryable"]; present {
		t.Fatalf("encoder invented retryability for an error that never declared one: %v", body)
	}
	if _, present := body["details"]; present {
		t.Fatalf("encoder invented a details block for an error that never declared one: %v", body)
	}
	var typed loomapi.LegacyErrorBody
	if err := json.Unmarshal([]byte(`{"code":"X","retryable":true}`), &typed); err != nil || typed.Code != "X" || typed.AdditionalProperties["retryable"] != true {
		t.Fatalf("generated AdditionalProperties contract drifted: %#v err=%v", typed, err)
	}
}
