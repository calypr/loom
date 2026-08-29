package server

import (
	"context"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataset"
)

type exactExecutionFixture struct {
	project, generation string
	selector            dataset.DataframeSelector
	execution           publication.BundleExecution
	output              publication.BundleOutputRecord
}

func (f *exactExecutionFixture) FindExecutionBySelector(_ context.Context, project, generation string, selector publication.DataframeSelector) (publication.BundleExecution, publication.BundleOutputRecord, error) {
	f.project, f.generation, f.selector = project, generation, selector
	return f.execution, f.output, nil
}

func TestPublicationVerificationUsesExactSelectorAndQueryableEvidence(t *testing.T) {
	verifiedAt := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	selector := dataset.DataframeSelector{Recipe: "core", TranslationVersion: "v1", Output: "Patient"}
	exact := &exactExecutionFixture{
		execution: publication.BundleExecution{ID: "execution-a", BundleIdentity: publication.BundleIdentity{DatasetGeneration: "commit-a"}, State: publication.BundleReady},
		output:    publication.BundleOutputRecord{Selector: selector, Name: "Patient", State: publication.BundleReady, VerifiedAt: &verifiedAt, PhysicalTable: "patient_a"},
	}
	result, err := (publicationVerificationStore{executions: exact}).VerifyPublication(context.Background(), "project-a", "commit-a", selector)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "PUBLISHED" || !result.Queryable || result.ExecutionID != "execution-a" || result.Selector != selector {
		t.Fatalf("verification = %#v", result)
	}
	if exact.project != "project-a" || exact.generation != "commit-a" || exact.selector != selector {
		t.Fatalf("selector lookup = %q %q %#v", exact.project, exact.generation, exact.selector)
	}
}
