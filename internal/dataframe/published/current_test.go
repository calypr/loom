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
	execution  publication.BundleExecution
	output     publication.BundleOutputRecord
	executions map[string]publication.BundleExecution
}

func (c exactCurrentCatalog) FindExecutionBySelector(context.Context, string, string, publication.DataframeSelector) (publication.BundleExecution, publication.BundleOutputRecord, error) {
	return c.execution, c.output, nil
}

func (c exactCurrentCatalog) GetPointer(context.Context, string) (publication.BundlePointer, error) {
	return publication.BundlePointer{ExecutionID: c.execution.ID}, nil
}

func (c exactCurrentCatalog) GetExecution(_ context.Context, id string) (publication.BundleExecution, error) {
	return c.executions[id], nil
}

type activeManifestFixture struct{ manifest dataset.Manifest }

func (f activeManifestFixture) ResolveActiveManifest(context.Context, string) (dataset.Manifest, error) {
	return f.manifest, nil
}

type activeReleaseFixture struct{ release dataset.ActiveRelease }

func (f activeReleaseFixture) ReadActiveRelease(context.Context, string) (dataset.ActiveRelease, error) {
	return f.release, nil
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

func TestCurrentProjectDatasetUsesActiveReleaseExecutionWhenBundlePointerMoves(t *testing.T) {
	selector := DataframeSelector{Recipe: "recipe", TranslationVersion: "v1", Output: "Patient"}
	verified := time.Now().UTC()
	output := func(table string) publication.BundleOutputRecord {
		return publication.BundleOutputRecord{
			Name: "Patient", Selector: selector, PhysicalTable: table,
			State: publication.BundlePublished, VerifiedAt: &verified,
		}
	}
	execution := func(id, table string) publication.BundleExecution {
		return publication.BundleExecution{
			ID: id, BundleIdentity: publication.BundleIdentity{
				Name: "recipe", TranslationVersion: "v1", Project: "P1", DatasetGeneration: "generation",
			},
			State: publication.BundlePublished, Outputs: []publication.BundleOutputRecord{output(table)},
		}
	}
	active := execution("execution-a", "patient_a")
	candidate := execution("execution-b", "patient_b")
	manifest := dataset.Manifest{
		Dataset: dataset.Ref{Project: "P1", Generation: "generation"}, State: dataset.StateStaged,
		SchemaIdentity: dataset.SchemaSnapshot{SchemaSHA256: "0000000000000000000000000000000000000000000000000000000000000000", GeneratedResourceTypes: []string{"Patient"}},
	}
	reader := Reader{
		Catalog: exactCurrentCatalog{
			execution: candidate, output: candidate.Outputs[0],
			executions: map[string]publication.BundleExecution{active.ID: active, candidate.ID: candidate},
		},
		ActiveManifestResolver: activeManifestFixture{manifest: manifest},
		ActiveReleaseResolver: activeReleaseFixture{release: dataset.ActiveRelease{Release: dataset.ProjectRelease{
			Project: "P1", Generation: "generation",
			Publications: []dataset.ReleasePublication{{Selector: selector, ExecutionID: active.ID, Generation: "generation"}},
		}}},
	}

	value, err := reader.CurrentProjectDataset(context.Background(), "P1", selector)
	if err != nil {
		t.Fatal(err)
	}
	if value.Revision != active.ID || value.PhysicalTable != "patient_a" {
		t.Fatalf("materialization = %#v, want active release execution %q", value, active.ID)
	}
}
