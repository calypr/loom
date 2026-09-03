package published

import (
	"context"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataset"
)

type exactCurrentCatalog struct {
	publication.BundleCatalog
	execution publication.BundleExecution
	output    publication.BundleOutputRecord
}

func (c exactCurrentCatalog) FindExecutionBySelector(context.Context, string, string, publication.DataframeSelector) (publication.BundleExecution, publication.BundleOutputRecord, error) {
	return c.execution, c.output, nil
}

func (c exactCurrentCatalog) GetPointer(context.Context, string) (publication.BundlePointer, error) {
	return publication.BundlePointer{ExecutionID: c.execution.ID}, nil
}

type activeManifestFixture struct{ manifest dataset.Manifest }

func (f activeManifestFixture) ResolveActiveManifest(context.Context, string) (dataset.Manifest, error) {
	return f.manifest, nil
}

func TestCurrentProjectDatasetUsesExactCatalogLookup(t *testing.T) {
	selector := DataframeSelector{Recipe: "recipe", TranslationVersion: "v1", Output: "Patient"}
	verified := time.Now().UTC()
	output := publication.BundleOutputRecord{
		Name: "Patient", Selector: selector, PhysicalTable: "patient_table",
		State: publication.BundlePublished, VerifiedAt: &verified,
	}
	execution := publication.BundleExecution{
		ID: "execution", BundleIdentity: publication.BundleIdentity{
			Name: "recipe", TranslationVersion: "v1", Project: "P1", DatasetGeneration: "generation",
		},
		State: publication.BundlePublished, Outputs: []publication.BundleOutputRecord{output},
	}
	manifest := dataset.Manifest{
		Dataset: dataset.Ref{Project: "P1", Generation: "generation"}, State: dataset.StateStaged,
		SchemaIdentity: dataset.SchemaSnapshot{SchemaSHA256: "0000000000000000000000000000000000000000000000000000000000000000", GeneratedResourceTypes: []string{"Patient"}},
	}
	reader := Reader{
		Catalog:                exactCurrentCatalog{execution: execution, output: output},
		ActiveManifestResolver: activeManifestFixture{manifest: manifest},
	}

	value, err := reader.CurrentProjectDataset(context.Background(), "P1", selector)
	if err != nil {
		t.Fatal(err)
	}
	if value.ID != "execution:Patient" || value.PhysicalTable != "patient_table" {
		t.Fatalf("materialization = %#v", value)
	}
}
