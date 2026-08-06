package server

import (
	"context"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataset"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type releaseQueryFixture struct {
	row   map[string]any
	binds map[string]interface{}
}

func (f *releaseQueryFixture) QueryRows(_ context.Context, _ string, _ int, binds map[string]interface{}, visit arangostore.RowVisitor) error {
	f.binds = binds
	if f.row != nil {
		return visit(f.row)
	}
	return nil
}

func TestPublicationVerificationUsesExactSelectorAndQueryableEvidence(t *testing.T) {
	verifiedAt := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	query := &releaseQueryFixture{row: map[string]any{
		"executionId": "execution-a", "generation": "commit-a", "executionState": "READY", "outputState": "READY",
		"verifiedAt": verifiedAt.Format(time.RFC3339), "physicalTable": "patient_a",
	}}
	selector := dataset.DataframeSelector{Recipe: "core", TranslationVersion: "v1", Output: "Patient"}
	result, err := (publicationVerificationStore{query: query}).VerifyPublication(context.Background(), "project-a", "commit-a", selector)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "PUBLISHED" || !result.Queryable || result.ExecutionID != "execution-a" || result.Selector != selector {
		t.Fatalf("verification = %#v", result)
	}
	if query.binds["recipe"] != "core" || query.binds["translation_version"] != "v1" || query.binds["output"] != "Patient" {
		t.Fatalf("selector binds = %#v", query.binds)
	}
}
