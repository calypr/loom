package graphqlapi

import (
	"errors"
	"testing"

	"github.com/calypr/loom/internal/dataframe"
)

func TestPresentErrorUsesStableExtensions(t *testing.T) {
	err := dataframe.NewError(
		dataframe.CodeUnknownField,
		"secret internal selector detail",
		dataframe.WithFieldPath("rootFields", "2", "fieldRef"),
		dataframe.WithDetails(map[string]any{"fieldRef": "Patient.missing", "aql": "FOR p IN Patient RETURN p"}),
	)
	graphqlErr := PresentError(err, "request-123")
	if graphqlErr == nil {
		t.Fatal("PresentError returned nil")
	}
	if graphqlErr.Message != "the selected field is not recognized" {
		t.Fatalf("message = %q", graphqlErr.Message)
	}
	if graphqlErr.Extensions["code"] != string(dataframe.CodeUnknownField) {
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
	if graphqlErr.Extensions["code"] != string(dataframe.CodeInternalError) {
		t.Fatalf("unknown extensions = %#v", graphqlErr.Extensions)
	}
	if graphqlErr.Extensions["details"] != nil {
		t.Fatalf("unknown details leaked: %#v", graphqlErr.Extensions)
	}
}

func TestExtensionsForErrorOmitsEmptyRequestID(t *testing.T) {
	extensions := ExtensionsForError(dataframe.NewError(dataframe.CodeInvalidCursor, ""), "")
	if _, ok := extensions["requestId"]; ok {
		t.Fatalf("empty request ID should be omitted: %#v", extensions)
	}
	if extensions["fieldPath"] == nil {
		t.Fatal("fieldPath extension should remain present for stable shape")
	}
}
