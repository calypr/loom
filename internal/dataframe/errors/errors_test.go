package dataframeerrors

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestErrorCodesAreUniqueAndStable(t *testing.T) {
	want := []ErrorCode{
		CodeProjectRequired, CodeRootResourceTypeRequired, CodeUnauthorizedProject,
		CodeUnknownField, CodeFieldNotPopulated, CodeInvalidTraversal, CodeUnsafeTraversalRoute,
		CodeInvalidFilter, CodeUnboundedPivot, CodeInvalidPivotColumn, CodeInvalidSlice,
		CodePlanTooExpensive, CodeInvalidCursor, CodeStaleCursor, CodeDatasetGenerationChanged,
		CodeUnsupportedExportFormat, CodeClientCanceled, CodeBackendUnavailable, CodeDatasetNotFound,
		CodeSchemaConflict, CodeInternalError, CodeInvalidResourceType, CodeInvalidLimit,
		CodeNoActiveGeneration, CodeResourceDecodeFailed, CodeReferenceNotResolved, CodeQueryDepthExceeded,
		CodeInvalidRequest, CodeInvalidData, CodeUnauthenticated, CodeForbidden, CodeRecipeNotFound,
		CodeRecipeExecutionNotFound, CodeExportLimitExceeded, CodeIngestPreflightFailed,
		CodeGenerationLoadIncomplete, CodeGenerationActivationUnknown, CodeInvalidGenerationFile,
		CodeDuplicateGenerationFile, CodePublicationInProgress, CodePublicationConflict,
		CodePublicationLeaseLost, CodeOutputEncodingFailed,
	}
	if !reflect.DeepEqual(AllErrorCodes, want) {
		t.Fatalf("error registry = %#v, want %#v", AllErrorCodes, want)
	}
	seen := make(map[ErrorCode]struct{}, len(AllErrorCodes))
	for _, code := range AllErrorCodes {
		if code == "" {
			t.Fatal("error registry contains an empty code")
		}
		if _, exists := seen[code]; exists {
			t.Fatalf("duplicate error code %q", code)
		}
		seen[code] = struct{}{}
	}
}

func TestErrorWrapPreservesCauseAndCopiesPublicFields(t *testing.T) {
	cause := errors.New("driver details: collection=Patient bind=@secret")
	details := map[string]any{
		"fieldRef":   "Patient.id",
		"collection": "Patient",
		"aql":        "FOR p IN Patient RETURN p",
		"nested":     map[string]any{"safe": true, "bind": "secret"},
	}
	err := Wrap(cause, CodeUnknownField, "internal selector text", WithFieldPath("rootFields", "0", "fieldRef"), WithDetails(details))
	if !errors.Is(err, cause) {
		t.Fatal("wrapped cause was not preserved")
	}
	if err.Code() != string(CodeUnknownField) {
		t.Fatalf("code = %q", err.Code())
	}
	if !reflect.DeepEqual(err.FieldPath(), []string{"rootFields", "0", "fieldRef"}) {
		t.Fatalf("field path = %#v", err.FieldPath())
	}
	public := err.Details()
	if public["fieldRef"] != "Patient.id" {
		t.Fatalf("safe detail missing: %#v", public)
	}
	if _, ok := public["collection"]; ok {
		t.Fatalf("collection leaked: %#v", public)
	}
	if _, ok := public["aql"]; ok {
		t.Fatalf("AQL leaked: %#v", public)
	}
	nested, ok := public["nested"].(map[string]any)
	if !ok || nested["safe"] != true {
		t.Fatalf("safe nested detail missing: %#v", public)
	}
	if _, ok := nested["bind"]; ok {
		t.Fatalf("bind value leaked: %#v", public)
	}

	path := err.FieldPath()
	path[0] = "changed"
	if err.FieldPath()[0] != "rootFields" {
		t.Fatal("field path was not copied")
	}
	details["new"] = "not copied"
	if _, ok := err.Details()["new"]; ok {
		t.Fatal("details map was not copied")
	}
}

func TestNormalizeUsesTypedAndContextConditionsOnly(t *testing.T) {
	typed := NewError(CodeInvalidFilter, "unsafe internal message")
	if got := Normalize(typed); got.Code() != string(CodeInvalidFilter) {
		t.Fatalf("typed code = %q", got.Code())
	}
	if got := Normalize(context.Canceled); got.Code() != string(CodeClientCanceled) {
		t.Fatalf("canceled code = %q", got.Code())
	}
	if got := Normalize(context.DeadlineExceeded); got.Code() != string(CodeBackendUnavailable) || !got.Retryable() {
		t.Fatalf("deadline mapping = code %q retryable=%v", got.Code(), got.Retryable())
	}
	unknown := errors.New("unknown error mentioning invalid cursor")
	got := Normalize(unknown)
	if got.Code() != string(CodeInternalError) {
		t.Fatalf("unknown code = %q", got.Code())
	}
	if PublicMessage(unknown) != "internal server error" {
		t.Fatalf("unknown message was exposed: %q", PublicMessage(unknown))
	}
}

func TestPublicMessageDoesNotExposeTypedMessage(t *testing.T) {
	err := NewError(CodeUnknownField, "field Patient.secret is not available")
	if got := PublicMessage(err); got != "the selected field is not recognized" {
		t.Fatalf("public message = %q", got)
	}
}

type unsafeUserError struct{}

func (unsafeUserError) Error() string       { return "private" }
func (unsafeUserError) Code() string        { return string(CodeUnknownField) }
func (unsafeUserError) FieldPath() []string { return []string{"field"} }
func (unsafeUserError) Details() map[string]any {
	return map[string]any{"aql": "private", "safe": true}
}
func (unsafeUserError) Retryable() bool { return false }

func TestNormalizeRedactsAdapterOwnedUserErrors(t *testing.T) {
	got := Normalize(unsafeUserError{})
	if got.Code() != string(CodeUnknownField) {
		t.Fatalf("code = %q", got.Code())
	}
	if _, leaked := got.Details()["aql"]; leaked {
		t.Fatalf("unsafe adapter detail leaked: %#v", got.Details())
	}
	if got.Details()["safe"] != true {
		t.Fatalf("safe adapter detail missing: %#v", got.Details())
	}
}

func TestErrorClassification(t *testing.T) {
	if !IsUserCorrectable(CodeInvalidFilter) || IsUserCorrectable(CodeInternalError) {
		t.Fatal("unexpected user-correctable classification")
	}
	if !IsRetryableCode(CodeBackendUnavailable) || IsRetryableCode(CodeClientCanceled) {
		t.Fatal("unexpected retryable classification")
	}
	if !IsOperatorFailure(CodeInternalError) || IsOperatorFailure(CodeInvalidCursor) {
		t.Fatal("unexpected operator classification")
	}
}
