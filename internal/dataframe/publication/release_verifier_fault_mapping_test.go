package publication

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/dataset"
)

type erroringExecutionCatalog struct {
	err error
}

func (c *erroringExecutionCatalog) FindExecutionBySelector(_ context.Context, _, _ string, _ DataframeSelector) (BundleExecution, BundleOutputRecord, error) {
	return BundleExecution{}, BundleOutputRecord{}, c.err
}

func TestPublicationVerificationStoreClassifiesFaultVsMissingRecord(t *testing.T) {
	selector := dataset.DataframeSelector{Recipe: "core", TranslationVersion: "v1", Output: "Patient"}

	_, err := (publicationVerificationStore{executions: &erroringExecutionCatalog{err: ErrBundleNotFound}}).
		VerifyPublication(context.Background(), "project-a", "commit-a", selector)
	if !errors.Is(err, dataset.ErrPublicationRecordMissing) {
		t.Fatalf("not-found must map to ErrPublicationRecordMissing, got %v", err)
	}
	if !errors.Is(err, ErrBundleNotFound) {
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
