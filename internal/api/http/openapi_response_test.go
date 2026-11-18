package httpapi

import (
	"reflect"
	"testing"
)

func TestServiceErrorResponsePreservesOrderedFieldPath(t *testing.T) {
	want := []string{"input", "outputs", "0", "columns", "2"}
	response := ServiceErrorResponse(ErrorResponse{Error: HTTPErrorBody{
		Code: "INVALID_REQUEST", Message: "the request is invalid", FieldPath: want,
	}})
	if response.Error.FieldPath == nil || !reflect.DeepEqual(*response.Error.FieldPath, want) {
		t.Fatalf("generated service error field path = %#v, want %#v", response.Error.FieldPath, want)
	}
	want[0] = "changed"
	if (*response.Error.FieldPath)[0] != "input" {
		t.Fatalf("generated service error retained caller slice: %#v", *response.Error.FieldPath)
	}
}
