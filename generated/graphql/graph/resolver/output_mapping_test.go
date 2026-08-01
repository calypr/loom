package resolver

import (
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func TestGraphqlRowsReturnsEncodingError(t *testing.T) {
	_, err := graphqlRows([]map[string]any{{"bad": func() {}}})
	if err == nil {
		t.Fatal("expected encoding error")
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeOutputEncodingFailed) {
		t.Fatalf("error = %v", err)
	}
}
