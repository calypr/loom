package server

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataset"
)

type erroringExecutionCatalog struct {
	err error
}

func (c *erroringExecutionCatalog) FindExecutionBySelector(_ context.Context, _, _ string, _ publication.DataframeSelector) (publication.BundleExecution, publication.BundleOutputRecord, error) {
	return publication.BundleExecution{}, publication.BundleOutputRecord{}, c.err
}

func TestPublicationVerificationStoreClassifiesFaultVsMissingRecord(t *testing.T) {
	selector := dataset.DataframeSelector{Recipe: "core", TranslationVersion: "v1", Output: "Patient"}

	_, err := (publicationVerificationStore{executions: &erroringExecutionCatalog{err: publication.ErrBundleNotFound}}).
		VerifyPublication(context.Background(), "project-a", "commit-a", selector)
	if !errors.Is(err, dataset.ErrPublicationRecordMissing) {
		t.Fatalf("not-found must map to ErrPublicationRecordMissing, got %v", err)
	}
	if !errors.Is(err, publication.ErrBundleNotFound) {
		t.Fatalf("not-found mapping must preserve the underlying sentinel: %v", err)
	}

	storeFault := errors.New("arangodb: cursor timeout")
	_, err = (publicationVerificationStore{executions: &erroringExecutionCatalog{err: storeFault}}).
		VerifyPublication(context.Background(), "project-a", "commit-a", selector)
	if !errors.Is(err, storeFault) {
		t.Fatalf("store fault must pass through errors.Is-visible: %v", err)
	}
	if errors.Is(err, dataset.ErrPublicationRecordMissing) {
		t.Fatalf("store fault must not masquerade as a missing record: %v", err)
	}
}
