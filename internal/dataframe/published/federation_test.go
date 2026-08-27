package published

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	bundlepublication "github.com/calypr/loom/internal/dataframe/publication"
	publication "github.com/calypr/loom/internal/dataset"
)

func TestReconcileFederatedDatasetAllowsSparseProjectColumns(t *testing.T) {
	selector := DataframeSelector{Recipe: "documents", TranslationVersion: "v1", Output: "DocumentReference"}
	dataset, err := ReconcileFederatedDataset(selector, []string{"a", "b"}, []Materialization{
		{ID: "a", Project: "a", PhysicalTable: "table_a", Columns: []Column{{Name: "id", ClickHouse: "String"}, {Name: "only_a", ClickHouse: "String"}}},
		{ID: "b", Project: "b", PhysicalTable: "table_b", Columns: []Column{{Name: "id", ClickHouse: "String"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	column, ok := findColumn(dataset.Columns, "only_a")
	if !ok || column.ClickHouse != "Nullable(String)" {
		t.Fatalf("sparse column = %#v", column)
	}
	if dataset.Availability != FederationAvailable {
		t.Fatalf("availability = %s", dataset.Availability)
	}
}

func TestReconcileFederatedDatasetReportsCardinalityCollision(t *testing.T) {
	selector := DataframeSelector{Recipe: "documents", TranslationVersion: "v1", Output: "DocumentReference"}
	_, err := ReconcileFederatedDataset(selector, []string{"a", "b"}, []Materialization{
		{ID: "a", Project: "a", PhysicalTable: "table_a", Columns: []Column{{Name: "code", ClickHouse: "String"}}},
		{ID: "b", Project: "b", PhysicalTable: "table_b", Columns: []Column{{Name: "code", ClickHouse: "Array(String)"}}},
	})
	var userErr dataframeerrors.UserError
	if !errors.As(err, &userErr) || userErr.Code() != string(dataframeerrors.CodeFederationIncompatible) {
		t.Fatalf("error = %v", err)
	}
	details := userErr.Details()
	if details["column"] != "code" {
		t.Fatalf("details = %#v", details)
	}
}

func TestReconcileExcludesMalformedSourceAndDegrades(t *testing.T) {
	selector := DataframeSelector{Recipe: "documents", TranslationVersion: "v1", Output: "DocumentReference"}
	dataset, err := ReconcileFederatedDataset(selector, []string{"a", "b"}, []Materialization{
		{ID: "a", Project: "a", PhysicalTable: "table_a", Columns: []Column{{Name: "id", ClickHouse: "String"}}},
		{ID: "b", Project: "b", PhysicalTable: "", Columns: []Column{{Name: "id", ClickHouse: "String"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if dataset.Availability != FederationDegraded || len(dataset.Sources) != 1 {
		t.Fatalf("dataset = %#v", dataset)
	}
	if dataset.ProjectStatuses[1].State != ProjectExcluded {
		t.Fatalf("statuses = %#v", dataset.ProjectStatuses)
	}
}

func TestExactSelectorDoesNotMixRecipesSharingOutput(t *testing.T) {
	now := time.Now().UTC()
	executions := []bundlepublication.BundleExecution{
		{ID: "recipe-a", BundleIdentity: bundlepublication.BundleIdentity{Name: "recipe-a", TranslationVersion: "v1", Project: "p"}, State: bundlepublication.BundleReady, UpdatedAt: now, Outputs: []bundlepublication.BundleOutputRecord{{Name: "DocumentReference", PhysicalTable: "a"}}},
		{ID: "recipe-b", BundleIdentity: bundlepublication.BundleIdentity{Name: "recipe-b", TranslationVersion: "v1", Project: "p"}, State: bundlepublication.BundleReady, UpdatedAt: now, Outputs: []bundlepublication.BundleOutputRecord{{Name: "DocumentReference", PhysicalTable: "b"}}},
	}
	pointers := map[string]bundlepublication.BundlePointer{}
	for _, execution := range executions {
		pointers[execution.PointerName()] = bundlepublication.BundlePointer{ExecutionID: execution.ID}
	}
	reader := Reader{Catalog: &federationCatalog{executions: executions, pointers: pointers}}
	sources, err := reader.CurrentFederatedSources(context.Background(), []string{"p"}, DataframeSelector{Recipe: "recipe-a", TranslationVersion: "v1", Output: "DocumentReference"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].PhysicalTable != "a" {
		t.Fatalf("sources = %#v", sources)
	}
}

func TestRecipeVersionsRemainIndependentlyQueryable(t *testing.T) {
	now := time.Now().UTC()
	executions := []bundlepublication.BundleExecution{
		{ID: "v1", BundleIdentity: bundlepublication.BundleIdentity{Name: "documents", Project: "p", DatasetGeneration: "g1"}, State: bundlepublication.BundleReady, UpdatedAt: now, Outputs: []bundlepublication.BundleOutputRecord{{Name: "DocumentReference", PhysicalTable: "v1_table"}}},
		{ID: "v2", BundleIdentity: bundlepublication.BundleIdentity{Name: "documents", Project: "p", DatasetGeneration: "g2"}, State: bundlepublication.BundleReady, UpdatedAt: now, Outputs: []bundlepublication.BundleOutputRecord{{Name: "DocumentReference", PhysicalTable: "v2_table"}}},
	}
	pointers := map[string]bundlepublication.BundlePointer{}
	for _, execution := range executions {
		pointers[execution.PointerName()] = bundlepublication.BundlePointer{ExecutionID: execution.ID}
	}
	catalog := &versionedFederationCatalog{federationCatalog: &federationCatalog{executions: executions, pointers: pointers}, selectors: map[string]DataframeSelector{
		"v1": {Recipe: "documents", TranslationVersion: "v1"}, "v2": {Recipe: "documents", TranslationVersion: "v2"},
	}}
	reader := Reader{Catalog: catalog}
	for _, version := range []string{"v1", "v2"} {
		sources, err := reader.CurrentFederatedSources(context.Background(), []string{"p"}, DataframeSelector{Recipe: "documents", TranslationVersion: version, Output: "DocumentReference"})
		if err != nil || len(sources) != 1 || sources[0].PhysicalTable != version+"_table" {
			t.Fatalf("version %s sources = %#v, %v", version, sources, err)
		}
	}
}

func TestVersionedExecutionWithoutRequestedOutputIsSkipped(t *testing.T) {
	now := time.Now().UTC()
	execution := bundlepublication.BundleExecution{
		ID: "research-subject", BundleIdentity: bundlepublication.BundleIdentity{
			Name: "calypr-meta-default", TranslationVersion: "v2", Project: "p", DatasetGeneration: "g",
		},
		State: bundlepublication.BundlePublished, UpdatedAt: now,
		Outputs: []bundlepublication.BundleOutputRecord{{Name: "ResearchSubject", PhysicalTable: "research_subject"}},
	}
	catalog := &missingOutputSelectorCatalog{federationCatalog: &federationCatalog{
		executions: []bundlepublication.BundleExecution{execution},
		pointers:   map[string]bundlepublication.BundlePointer{execution.PointerName(): {ExecutionID: execution.ID}},
	}}
	reader := Reader{Catalog: catalog}

	sources, err := reader.CurrentFederatedSources(context.Background(), []string{"p"}, DataframeSelector{
		Recipe: "calypr-meta-default", TranslationVersion: "v2", Output: "Patient",
	})
	if err != nil {
		t.Fatalf("CurrentFederatedSources() error = %v", err)
	}
	if len(sources) != 0 {
		t.Fatalf("sources = %#v, want no Patient publication", sources)
	}
	if catalog.selectorCalls != 0 {
		t.Fatalf("selector resolver called %d times for absent output", catalog.selectorCalls)
	}
}

func TestFederatedUnionSynthesizesProjectIDForLegacySource(t *testing.T) {
	dataset := FederatedDataset{
		Columns: []Column{{Name: "id", ClickHouse: "String"}, {Name: projectIDColumn, ClickHouse: "String"}},
		Sources: []Materialization{{ID: "execution:Patient", Project: "HTAN_INT-BForePC", PhysicalTable: "loom_patient", Columns: []Column{{Name: "id", ClickHouse: "String"}}}},
	}
	query, args, err := federatedNormalizedUnion(dataset, []string{projectIDColumn}, map[string]SourceAccess{"HTAN_INT-BForePC": {Unrestricted: true}})
	if err != nil {
		t.Fatalf("federatedNormalizedUnion() error = %v", err)
	}
	if !strings.Contains(query, "CAST(? AS String) AS `project_id`") {
		t.Fatalf("query does not synthesize project_id: %s", query)
	}
	if len(args) != 2 || args[0] != "HTAN_INT-BForePC" || args[1] != "execution:Patient" {
		t.Fatalf("args = %#v, want project then source id", args)
	}
}

func TestFederatedColumnsRejectsUnknownTableColumnAsInvalidRequest(t *testing.T) {
	_, _, err := federatedColumns(FederatedDataset{
		Columns: []Column{{Name: "patient_id", ClickHouse: "String"}},
	}, []string{"stale_column"}, nil)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("error = %v, want typed user error", err)
	}
	if userErr.Code() != string(dataframeerrors.CodeInvalidRequest) {
		t.Fatalf("error code = %q, want INVALID_REQUEST", userErr.Code())
	}
	if userErr.Retryable() {
		t.Fatal("unknown table column must not be retryable")
	}
}

type federationCatalog struct {
	bundlepublication.BundleCatalog
	executions []bundlepublication.BundleExecution
	pointers   map[string]bundlepublication.BundlePointer
}

type versionedFederationCatalog struct {
	*federationCatalog
	selectors map[string]DataframeSelector
}

type missingOutputSelectorCatalog struct {
	*federationCatalog
	selectorCalls int
}

type staleExecutionSelectorCatalog struct {
	*federationCatalog
	selectors map[string]DataframeSelector
	staleID   string
}

func (c *staleExecutionSelectorCatalog) DataframeSelectorForExecution(_ context.Context, executionID, output string) (DataframeSelector, error) {
	if executionID == c.staleID {
		return DataframeSelector{}, bundlepublication.ErrBundleNotFound
	}
	selector := c.selectors[executionID]
	selector.Output = output
	return selector, nil
}

func (c *missingOutputSelectorCatalog) DataframeSelectorForExecution(context.Context, string, string) (DataframeSelector, error) {
	c.selectorCalls++
	return DataframeSelector{}, bundlepublication.ErrBundleNotFound
}

type releaseExecutionFixture map[string]string

func (f releaseExecutionFixture) ActiveReleaseExecutionIDs(context.Context, []string, DataframeSelector) (map[string]string, error) {
	result := make(map[string]string, len(f))
	for project, executionID := range f {
		result[project] = executionID
	}
	return result, nil
}

func (f releaseExecutionFixture) ActiveReleaseSelectors(context.Context, []string) ([]DataframeSelector, map[string]bool, error) {
	controlled := make(map[string]bool, len(f))
	for project := range f {
		controlled[project] = true
	}
	return nil, controlled, nil
}

func (c *versionedFederationCatalog) DataframeSelectorForExecution(_ context.Context, executionID, output string) (DataframeSelector, error) {
	selector := c.selectors[executionID]
	selector.Output = output
	return selector, nil
}

func TestActiveReleaseExecutionOverridesMutablePublicationPointer(t *testing.T) {
	now := time.Now().UTC()
	selector := DataframeSelector{Recipe: "documents", TranslationVersion: "v1", Output: "DocumentReference"}
	selected := bundlepublication.BundleExecution{ID: "release-selected", BundleIdentity: bundlepublication.BundleIdentity{Name: selector.Recipe, TranslationVersion: selector.TranslationVersion, Project: "p", DatasetGeneration: "old"}, State: bundlepublication.BundlePublished, UpdatedAt: now, Outputs: []bundlepublication.BundleOutputRecord{{Name: selector.Output, PhysicalTable: "stable", State: bundlepublication.BundlePublished}}}
	newer := bundlepublication.BundleExecution{ID: "pointer-newer", BundleIdentity: bundlepublication.BundleIdentity{Name: selector.Recipe, TranslationVersion: selector.TranslationVersion, Project: "p", DatasetGeneration: "new"}, State: bundlepublication.BundlePublished, UpdatedAt: now.Add(time.Second), Outputs: []bundlepublication.BundleOutputRecord{{Name: selector.Output, PhysicalTable: "replacement", State: bundlepublication.BundlePublished}}}
	catalog := &federationCatalog{executions: []bundlepublication.BundleExecution{selected, newer}, pointers: map[string]bundlepublication.BundlePointer{
		selected.PointerName(): {ExecutionID: newer.ID}, newer.PointerName(): {ExecutionID: newer.ID},
	}}
	reader := Reader{Catalog: catalog, ActiveManifestResolver: noActiveFederationResolver{}, ReleaseExecutionResolver: releaseExecutionFixture{"p": selected.ID}}
	sources, err := reader.CurrentFederatedSources(context.Background(), []string{"p"}, selector)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].ID != selected.ID+":"+selector.Output || sources[0].PhysicalTable != "stable" {
		t.Fatalf("release-selected sources = %#v", sources)
	}
}

func TestStaleReleaseExecutionDoesNotPoisonValidFederatedSource(t *testing.T) {
	now := time.Now().UTC()
	selector := DataframeSelector{Recipe: "explorer_project_default", TranslationVersion: "repository-commit", Output: "Patient"}
	valid := bundlepublication.BundleExecution{ID: "valid", BundleIdentity: bundlepublication.BundleIdentity{Name: selector.Recipe, TranslationVersion: selector.TranslationVersion, Project: "valid-project", DatasetGeneration: "g1"}, State: bundlepublication.BundlePublished, UpdatedAt: now, Outputs: []bundlepublication.BundleOutputRecord{{Name: selector.Output, PhysicalTable: "patient_valid", State: bundlepublication.BundlePublished}}}
	stale := bundlepublication.BundleExecution{ID: "stale", BundleIdentity: bundlepublication.BundleIdentity{Name: selector.Recipe, TranslationVersion: selector.TranslationVersion, Project: "stale-project", DatasetGeneration: "g2"}, State: bundlepublication.BundlePublished, UpdatedAt: now, Outputs: []bundlepublication.BundleOutputRecord{{Name: selector.Output, PhysicalTable: "patient_stale", State: bundlepublication.BundlePublished}}}
	catalog := &staleExecutionSelectorCatalog{federationCatalog: &federationCatalog{executions: []bundlepublication.BundleExecution{valid, stale}}, selectors: map[string]DataframeSelector{valid.ID: selector}, staleID: stale.ID}
	reader := Reader{Catalog: catalog, ReleaseExecutionResolver: releaseExecutionFixture{"valid-project": valid.ID, "stale-project": stale.ID}}

	sources, err := reader.CurrentFederatedSources(context.Background(), []string{"valid-project", "stale-project"}, selector)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Project != "valid-project" || sources[0].PhysicalTable != "patient_valid" {
		t.Fatalf("sources = %#v", sources)
	}
}

func (c *federationCatalog) ListExecutions(_ context.Context, state bundlepublication.BundleState, before time.Time) ([]bundlepublication.BundleExecution, error) {
	result := make([]bundlepublication.BundleExecution, 0, len(c.executions))
	for _, execution := range c.executions {
		if execution.State.Canonical() == state.Canonical() && execution.UpdatedAt.Before(before) {
			result = append(result, execution)
		}
	}
	return result, nil
}

func (c *federationCatalog) GetPointer(_ context.Context, name string) (bundlepublication.BundlePointer, error) {
	return c.pointers[name], nil
}

type noActiveFederationResolver struct{}

func (noActiveFederationResolver) ResolveActiveManifest(context.Context, string) (publication.Manifest, error) {
	return publication.Manifest{}, publication.ErrNoActiveGeneration
}
