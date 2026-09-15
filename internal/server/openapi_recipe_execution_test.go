package server

import (
	"errors"
	"net/http"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
)

func TestRecipeExecutionHTTPStatePreservesExplorerReadyContract(t *testing.T) {
	if got := recipeExecutionHTTPState(publication.BundlePublished); got != "READY" {
		t.Fatalf("published HTTP state = %q, want READY", got)
	}
	if got := recipeExecutionHTTPState(publication.BundleRunning); got != "RUNNING" {
		t.Fatalf("running HTTP state = %q, want RUNNING", got)
	}
}

func TestRecipeExecutionAuthorizationStatusDistinguishesDenialFromOutage(t *testing.T) {
	if got := recipeExecutionAuthorizationStatus(authscope.ErrForbidden); got != http.StatusForbidden {
		t.Fatalf("forbidden status = %d, want %d", got, http.StatusForbidden)
	}
	if got := recipeExecutionAuthorizationStatus(authscope.ErrAuthorizationBackendUnavailable); got != http.StatusServiceUnavailable {
		t.Fatalf("backend outage status = %d, want %d", got, http.StatusServiceUnavailable)
	}
	if got := recipeExecutionAuthorizationStatus(errors.New("unexpected scope failure")); got != http.StatusServiceUnavailable {
		t.Fatalf("unknown scope status = %d, want %d", got, http.StatusServiceUnavailable)
	}
}
