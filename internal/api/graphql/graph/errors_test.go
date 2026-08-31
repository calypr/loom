package graphqlapi

import (
	"errors"
	"reflect"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func TestPresentErrorUsesStableExtensions(t *testing.T) {
	err := dataframeerrors.NewError(
		dataframeerrors.CodeUnknownField,
		"secret internal selector detail",
		dataframeerrors.WithFieldPath("rootFields", "2", "fieldRef"),
		dataframeerrors.WithDetails(map[string]any{"fieldRef": "Patient.missing", "aql": "FOR p IN Patient RETURN p"}),
	)
	graphqlErr := presentError(err, "request-123")
	if graphqlErr == nil {
		t.Fatal("presentError returned nil")
	}
	if graphqlErr.Message != "the selected field is not recognized" {
		t.Fatalf("message = %q", graphqlErr.Message)
	}
	if graphqlErr.Extensions["code"] != string(dataframeerrors.CodeUnknownField) {
		t.Fatalf("extensions = %#v", graphqlErr.Extensions)
	}
	if graphqlErr.Extensions["requestId"] != "request-123" {
		t.Fatalf("request ID missing: %#v", graphqlErr.Extensions)
	}
	if _, ok := graphqlErr.Extensions["aql"]; ok {
		t.Fatalf("AQL extension leaked: %#v", graphqlErr.Extensions)
	}
	if !errors.Is(graphqlErr, err) {
		t.Fatal("GraphQL error did not preserve original cause")
	}
}

func TestPresentErrorRedactsUnknownErrors(t *testing.T) {
	graphqlErr := presentError(errors.New("AQL FOR p IN Patient bind=@token"), "request-456")
	if graphqlErr.Message != "internal server error" {
		t.Fatalf("unknown message = %q", graphqlErr.Message)
	}
	if graphqlErr.Extensions["code"] != string(dataframeerrors.CodeInternalError) {
		t.Fatalf("unknown extensions = %#v", graphqlErr.Extensions)
	}
	if graphqlErr.Extensions["details"] != nil {
		t.Fatalf("unknown details leaked: %#v", graphqlErr.Extensions)
	}
}

func TestPresentRecipeResolutionErrorKeepsActionableMessage(t *testing.T) {
	err := dataframeerrors.NewError(
		dataframeerrors.CodeRecipeResolutionFailed,
		`outputs[0] DocumentReference: field "project_id" is not in the active FHIR schema`,
	)
	graphqlErr := presentError(err, "request-789")
	if graphqlErr.Message != err.Error() {
		t.Fatalf("recipe resolution message = %q", graphqlErr.Message)
	}
	if graphqlErr.Extensions["code"] != string(dataframeerrors.CodeRecipeResolutionFailed) {
		t.Fatalf("recipe resolution extensions = %#v", graphqlErr.Extensions)
	}
}

func TestExtensionsForErrorOmitsEmptyRequestID(t *testing.T) {
	extensions := extensionsForError(dataframeerrors.NewError(dataframeerrors.CodeInvalidCursor, ""), "")
	if _, ok := extensions["requestId"]; ok {
		t.Fatalf("empty request ID should be omitted: %#v", extensions)
	}
	if extensions["fieldPath"] == nil {
		t.Fatal("fieldPath extension should remain present for stable shape")
	}
}

func TestPresentGraphQLErrorAddsDefaultsToValidationError(t *testing.T) {
	original := &gqlerror.Error{
		Message:   `Cannot query field "missing" on type "Patient".`,
		Path:      ast.Path{ast.PathName("patient")},
		Locations: []gqlerror.Location{{Line: 4, Column: 9}},
	}

	presented := presentGraphQLError(original, "request-validation")
	if presented == nil {
		t.Fatal("presentGraphQLError returned nil")
	}
	if presented.Message != original.Message {
		t.Fatalf("message = %q, want %q", presented.Message, original.Message)
	}
	if !reflect.DeepEqual(presented.Path, original.Path) {
		t.Fatalf("path = %#v, want %#v", presented.Path, original.Path)
	}
	if !reflect.DeepEqual(presented.Locations, original.Locations) {
		t.Fatalf("locations = %#v, want %#v", presented.Locations, original.Locations)
	}
	if presented.Extensions["code"] != "GRAPHQL_VALIDATION_FAILED" {
		t.Fatalf("code = %#v", presented.Extensions["code"])
	}
	if presented.Extensions["retryable"] != false {
		t.Fatalf("retryable = %#v", presented.Extensions["retryable"])
	}
	if presented.Extensions["requestId"] != "request-validation" {
		t.Fatalf("request ID = %#v", presented.Extensions["requestId"])
	}
}

func TestPresentGraphQLErrorPreservesValidationExtensions(t *testing.T) {
	original := &gqlerror.Error{
		Message: `Variable "$selector" got invalid value.`,
		Extensions: map[string]any{
			"code":      "CUSTOM_VALIDATION",
			"retryable": true,
			"hint":      "use a valid selector",
		},
	}

	presented := presentGraphQLError(original, "request-validation-custom")
	if presented == nil {
		t.Fatal("presentGraphQLError returned nil")
	}
	if presented.Extensions["code"] != "CUSTOM_VALIDATION" {
		t.Fatalf("code = %#v", presented.Extensions["code"])
	}
	if presented.Extensions["retryable"] != true {
		t.Fatalf("retryable = %#v", presented.Extensions["retryable"])
	}
	if presented.Extensions["hint"] != "use a valid selector" {
		t.Fatalf("hint = %#v", presented.Extensions["hint"])
	}
	if presented.Extensions["requestId"] != "request-validation-custom" {
		t.Fatalf("request ID = %#v", presented.Extensions["requestId"])
	}
}

func TestPresentGraphQLErrorDoesNotMutateValidationExtensions(t *testing.T) {
	original := &gqlerror.Error{
		Message: `Variable "$selector" got invalid value.`,
		Extensions: map[string]any{
			"code": "CUSTOM_VALIDATION",
		},
	}

	presented := presentGraphQLError(original, "request-validation-custom")
	if presented == nil {
		t.Fatal("presentGraphQLError returned nil")
	}
	if _, ok := original.Extensions["requestId"]; ok {
		t.Fatalf("presentGraphQLError mutated the original extensions: %#v", original.Extensions)
	}
	if presented.Extensions["requestId"] != "request-validation-custom" {
		t.Fatalf("presented request ID = %#v", presented.Extensions["requestId"])
	}
}

func TestPresentGraphQLErrorPresentsWrappedSemanticCause(t *testing.T) {
	semantic := dataframeerrors.NewError(
		dataframeerrors.CodeUnknownField,
		"secret internal selector detail",
		dataframeerrors.WithFieldPath("input", "field"),
	)
	original := &gqlerror.Error{
		Err:       semantic,
		Message:   "internal selector failure",
		Path:      ast.Path{ast.PathName("patient")},
		Locations: []gqlerror.Location{{Line: 8, Column: 12}},
	}

	presented := presentGraphQLError(original, "request-semantic")
	if presented == nil {
		t.Fatal("presentGraphQLError returned nil")
	}
	if presented.Message != "the selected field is not recognized" {
		t.Fatalf("message = %q", presented.Message)
	}
	if presented.Extensions["code"] != string(dataframeerrors.CodeUnknownField) {
		t.Fatalf("code = %#v", presented.Extensions["code"])
	}
	if !errors.Is(presented, semantic) {
		t.Fatal("GraphQL error did not preserve semantic cause")
	}
	if !reflect.DeepEqual(presented.Path, original.Path) {
		t.Fatalf("path = %#v, want %#v", presented.Path, original.Path)
	}
	if !reflect.DeepEqual(presented.Locations, original.Locations) {
		t.Fatalf("locations = %#v, want %#v", presented.Locations, original.Locations)
	}
}
