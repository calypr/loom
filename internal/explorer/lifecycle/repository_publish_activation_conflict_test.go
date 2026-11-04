package lifecycle

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
)

// The authoring publish path types a lost release compare-and-swap as a
// PUBLICATION_ACTIVATION_CONFLICT (publish.go, pinned in service_test). The
// repository publish path used to fall through to retryable 503s whose public
// message echoed the wrapped transport text. These tests pin the mirrored
// contract on the repository path.

func TestRepositoryPublishActivationConflictMirrorsAuthoringContract(t *testing.T) {
	wrapped := fmt.Errorf("activate release: arango preconditions emptied: %w", dataset.ErrReleaseActivationConflict)
	lifecycleErr, store, err := repositoryPublishIntoActivation(t, wrapped)
	if lifecycleErr.Class != ClassConflict || lifecycleErr.Code != "PUBLICATION_ACTIVATION_CONFLICT" {
		t.Fatalf("activation conflict classified %q/%q, want conflict/PUBLICATION_ACTIVATION_CONFLICT", lifecycleErr.Class, lifecycleErr.Code)
	}
	if _, retryable := lifecycleErr.Details["retryable"]; retryable {
		t.Fatalf("activation conflict advertises a retry hint: %#v", lifecycleErr.Details)
	}
	if strings.Contains(lifecycleErr.Message, "activate release") || strings.Contains(lifecycleErr.Message, "arango") {
		t.Fatalf("activation conflict republishes wrapped transport text: %q", lifecycleErr.Message)
	}
	if !strings.Contains(err.Error(), "PUBLICATION_ACTIVATION_CONFLICT") {
		t.Fatalf("wire error lost the stable code: %v", err)
	}
	if !errors.Is(err, dataset.ErrReleaseActivationConflict) {
		t.Fatalf("wrapped cause no longer carries the conflict sentinel: %v", err)
	}
	if store.repositoryRevision == nil || store.repositoryRevision.Status != explorer.RevisionFailed ||
		len(store.repositoryRevision.Diagnostics) != 1 ||
		store.repositoryRevision.Diagnostics[0].Code != "PUBLICATION_ACTIVATION_CONFLICT" ||
		store.repositoryRevision.Diagnostics[0].Retryable {
		t.Fatalf("failed revision = %#v, want non-retryable PUBLICATION_ACTIVATION_CONFLICT diagnostic", store.repositoryRevision)
	}
}

func TestRepositoryPublishActivationConflictUnwrappedSentinelClassifiesTheSame(t *testing.T) {
	lifecycleErr, _, err := repositoryPublishIntoActivation(t, dataset.ErrReleaseActivationConflict)
	if lifecycleErr.Class != ClassConflict || lifecycleErr.Code != "PUBLICATION_ACTIVATION_CONFLICT" {
		t.Fatalf("unwrapped sentinel classified %q/%q, want conflict/PUBLICATION_ACTIVATION_CONFLICT", lifecycleErr.Class, lifecycleErr.Code)
	}
	if !strings.Contains(err.Error(), "another publication won activation") {
		t.Fatalf("activation conflict lost its stable guidance text: %v", err)
	}
}

// Control: an infrastructure activation fault (no conflict sentinel) must
// stay the retryable unavailable path with the retryable diagnostic, so the
// new branch does not swallow real faults into a conflict.
func TestRepositoryPublishActivationInfrastructureFaultStaysRetryableUnavailable(t *testing.T) {
	lifecycleErr, store, _ := repositoryPublishIntoActivation(t, errors.New("arango: connection reset by peer"))
	if lifecycleErr.Class != ClassUnavailable || lifecycleErr.Code != "RELEASE_ACTIVATION_FAILED" {
		t.Fatalf("infrastructure fault classified %q/%q, want unavailable/RELEASE_ACTIVATION_FAILED", lifecycleErr.Class, lifecycleErr.Code)
	}
	if store.repositoryRevision == nil || len(store.repositoryRevision.Diagnostics) != 1 || !store.repositoryRevision.Diagnostics[0].Retryable {
		t.Fatalf("infrastructure fault lost the retryable diagnostic: %#v", store.repositoryRevision)
	}
}
