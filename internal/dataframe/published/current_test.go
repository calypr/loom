package published

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataset"
)

type exactCurrentCatalog struct {
	publication.BundleCatalog
	execution       publication.BundleExecution
	output          publication.BundleOutputRecord
	executions      map[string]publication.BundleExecution
	executionErrors map[string]error
}

func (c exactCurrentCatalog) FindExecutionBySelector(context.Context, string, string, publication.DataframeSelector) (publication.BundleExecution, publication.BundleOutputRecord, error) {
	return c.execution, c.output, nil
}

func (c exactCurrentCatalog) GetPointer(context.Context, string) (publication.BundlePointer, error) {
	return publication.BundlePointer{ExecutionID: c.execution.ID}, nil
}

func (c exactCurrentCatalog) GetExecution(_ context.Context, id string) (publication.BundleExecution, error) {
	if err := c.executionErrors[id]; err != nil {
		return publication.BundleExecution{}, err
	}
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

func TestCurrentProjectDatasetsSkipsBrokenOptionalReleaseBinding(t *testing.T) {
	selector := DataframeSelector{Recipe: "healthy", TranslationVersion: "v1", Output: "out-a"}
	verified := time.Now().UTC()
	output := publication.BundleOutputRecord{Name: selector.Output, Selector: selector, PhysicalTable: "healthy_table", State: publication.BundlePublished, VerifiedAt: &verified}
	healthy := publication.BundleExecution{ID: "healthy", BundleIdentity: publication.BundleIdentity{Project: "P1", DatasetGeneration: "generation"}, State: publication.BundlePublished, Outputs: []publication.BundleOutputRecord{output}}
	catalog := exactCurrentCatalog{
		executions:      map[string]publication.BundleExecution{healthy.ID: healthy},
		executionErrors: map[string]error{"optional": publication.ErrBundleNotFound},
	}
	reader := Reader{
		Catalog: catalog,
		ActiveReleaseResolver: activeReleaseFixture{release: dataset.ActiveRelease{Release: dataset.ProjectRelease{
			Project: "P1", Generation: "generation",
			Publications: []dataset.ReleasePublication{
				{Selector: selector, ExecutionID: healthy.ID, Generation: "generation", Required: true},
				{Selector: DataframeSelector{Recipe: "optional", TranslationVersion: "v1", Output: "out-b"}, ExecutionID: "optional", Generation: "generation"},
			},
		}}},
	}

	values, err := reader.CurrentProjectDatasets(context.Background(), "P1")
	if err != nil || len(values) != 1 || values[0].ID != healthy.ID+":"+selector.Output {
		t.Fatalf("list with broken optional binding = %#v, %v; want healthy output only", values, err)
	}
	if _, err := reader.CurrentProjectDataset(context.Background(), "P1", DataframeSelector{Recipe: "optional", TranslationVersion: "v1", Output: "out-b"}); !errors.Is(err, publication.ErrBundleNotFound) {
		t.Fatalf("single read for broken optional binding = %v, want its missing-execution error", err)
	}
}

func TestCurrentProjectDatasetsFailsClosedForBrokenRequiredOrTransientBinding(t *testing.T) {
	selector := DataframeSelector{Recipe: "required", TranslationVersion: "v1", Output: "out-a"}
	transient := errors.New("catalog unavailable")
	for _, tc := range []struct {
		name     string
		err      error
		required bool
	}{
		{name: "required missing", err: publication.ErrBundleNotFound, required: true},
		{name: "optional transient", err: transient},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := Reader{
				Catalog: exactCurrentCatalog{executionErrors: map[string]error{"bad": tc.err}},
				ActiveReleaseResolver: activeReleaseFixture{release: dataset.ActiveRelease{Release: dataset.ProjectRelease{
					Project: "P1", Generation: "generation",
					Publications: []dataset.ReleasePublication{{Selector: selector, ExecutionID: "bad", Generation: "generation", Required: tc.required}},
				}}},
			}
			values, err := reader.CurrentProjectDatasets(context.Background(), "P1")
			if err == nil || len(values) != 0 || !errors.Is(err, tc.err) {
				t.Fatalf("list = %#v, %v; want fail-closed error %v", values, err, tc.err)
			}
		})
	}
}
