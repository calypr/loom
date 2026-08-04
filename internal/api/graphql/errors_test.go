package graphqlerrors

import (
	"errors"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func TestPresentErrorUsesStableExtensions(t *testing.T) {
	err := dataframeerrors.NewError(
		dataframeerrors.CodeUnknownField,
		"secret internal selector detail",
		dataframeerrors.WithFieldPath("rootFields", "2", "fieldRef"),
		dataframeerrors.WithDetails(map[string]any{"fieldRef": "Patient.missing", "aql": "FOR p IN Patient RETURN p"}),
	)
	graphqlErr := PresentError(err, "request-123")
	if graphqlErr == nil {
		t.Fatal("PresentError returned nil")
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
	graphqlErr := PresentError(errors.New("AQL FOR p IN Patient bind=@token"), "request-456")
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
	graphqlErr := PresentError(err, "request-789")
	if graphqlErr.Message != err.Error() {
		t.Fatalf("recipe resolution message = %q", graphqlErr.Message)
	}
	if graphqlErr.Extensions["code"] != string(dataframeerrors.CodeRecipeResolutionFailed) {
		t.Fatalf("recipe resolution extensions = %#v", graphqlErr.Extensions)
	}
}

func TestExtensionsForErrorOmitsEmptyRequestID(t *testing.T) {
	extensions := ExtensionsForError(dataframeerrors.NewError(dataframeerrors.CodeInvalidCursor, ""), "")
	if _, ok := extensions["requestId"]; ok {
		t.Fatalf("empty request ID should be omitted: %#v", extensions)
	}
	if extensions["fieldPath"] == nil {
		t.Fatal("fieldPath extension should remain present for stable shape")
	}
}
