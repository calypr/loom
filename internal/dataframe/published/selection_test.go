package published

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
)

type selectionCatalog struct {
	publication.BundleCatalog
	execution publication.BundleExecution
}

func (c selectionCatalog) GetExecution(context.Context, string) (publication.BundleExecution, error) {
	return c.execution, nil
}

func TestExactExecutionMaterializationRejectsMissingAddressability(t *testing.T) {
	reader := &Reader{Catalog: selectionCatalog{execution: publication.BundleExecution{
		ID: "execution-a", BundleIdentity: publication.BundleIdentity{Project: "project", DatasetGeneration: "generation", ReceiptID: "receipt", SchemaDigest: "schema"}, State: publication.BundlePublished,
		Outputs: []publication.BundleOutputRecord{{Name: "files", PhysicalTable: "files_table", State: publication.BundlePublished, VerifiedAt: timePtr(time.Now())}},
	}}}
	if _, err := reader.ExactExecutionMaterialization(context.Background(), "execution-a", "files"); !errors.Is(err, publication.ErrSelectionSourceNotAddressable) {
		t.Fatalf("error = %v, want source addressability failure", err)
	}
}

func TestSourceResourceRefRequiresStringIdentity(t *testing.T) {
	materialization := Materialization{Project: "project", DatasetGeneration: "generation", SourceRow: &publication.SourceRowMetadata{ResourceType: "DocumentReference", IDColumn: "id"}}
	if _, _, err := SourceResourceRef(materialization, map[string]any{"id": 42}); !errors.Is(err, publication.ErrSelectionSourceNotAddressable) {
		t.Fatalf("numeric source id error = %v", err)
	}
	resourceType, id, err := SourceResourceRef(materialization, map[string]any{"id": "files001"})
	if err != nil || resourceType != "DocumentReference" || id != "files001" {
		t.Fatalf("source ref = %q/%q, err=%v", resourceType, id, err)
	}
}

func timePtr(value time.Time) *time.Time { return &value }
