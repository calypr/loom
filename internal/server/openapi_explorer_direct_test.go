package server

import (
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
)

func TestLegacyErrorMessagePreservesStructuredLifecycleMessage(t *testing.T) {
	var response loomapi.LegacyErrorResponse
	if err := response.Error.FromLegacyErrorBody(loomapi.LegacyErrorBody{Code: "COMPILATION_FAILED", Message: "catalog field is unavailable"}); err != nil {
		t.Fatal(err)
	}
	if got := legacyErrorMessage(response); got != "catalog field is unavailable" {
		t.Fatalf("legacy error message = %q", got)
	}
}
