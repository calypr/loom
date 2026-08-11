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
		{ID: "recipe-a", BundleIdentity: bundlepublication.BundleIdentity{Name: "recipe-a", Project: "p"}, State: bundlepublication.BundleReady, UpdatedAt: now, Outputs: []bundlepublication.BundleOutputRecord{{Name: "DocumentReference", PhysicalTable: "a"}}},
		{ID: "recipe-b", BundleIdentity: bundlepublication.BundleIdentity{Name: "recipe-b", Project: "p"}, State: bundlepublication.BundleReady, UpdatedAt: now, Outputs: []bundlepublication.BundleOutputRecord{{Name: "DocumentReference", PhysicalTable: "b"}}},
	}
	pointers := map[string]bundlepublication.BundlePointer{}
	for _, execution := range executions {
		pointers[execution.PointerName()] = bundlepublication.BundlePointer{ExecutionID: execution.ID}
	}
	reader := Reader{Catalog: &federationCatalog{executions: executions, pointers: pointers}, LegacyTranslationVersion: "v1"}
	sources, err := reader.CurrentFederatedSources(context.Background(), []string{"p"}, DataframeSelector{Recipe: "recipe-a", TranslationVersion: "v1", Output: "DocumentReference"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].PhysicalTable != "a" {
		t.Fatalf("sources = %#v", sources)
	}
}

func TestLegacyReadyExecutionUsesLegacyPointerAndDefaultVersion(t *testing.T) {
	now := time.Now().UTC()
	execution := bundlepublication.BundleExecution{ID: "legacy", BundleIdentity: bundlepublication.BundleIdentity{Name: "documents", Project: "p", DatasetGeneration: "g"}, State: bundlepublication.BundleReady, UpdatedAt: now, Outputs: []bundlepublication.BundleOutputRecord{{Name: "DocumentReference", PhysicalTable: "legacy_table", State: bundlepublication.BundleReady}}}
	catalog := &versionedFederationCatalog{federationCatalog: &federationCatalog{executions: []bundlepublication.BundleExecution{execution}, pointers: map[string]bundlepublication.BundlePointer{execution.PointerName(): {ExecutionID: execution.ID}}}, selectors: map[string]DataframeSelector{}}
	reader := Reader{Catalog: catalog, LegacyTranslationVersion: "v1"}
	sources, err := reader.CurrentFederatedSources(context.Background(), []string{"p"}, DataframeSelector{Recipe: "documents", TranslationVersion: "v1", Output: "DocumentReference"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].PhysicalTable != "legacy_table" {
		t.Fatalf("legacy sources = %#v", sources)
	}
	projects, err := reader.PublishedProjects(context.Background())
	if err != nil || len(projects) != 1 || projects[0] != "p" {
		t.Fatalf("legacy projects = %#v, %v", projects, err)
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

func TestAggregateGroupOrderClauseGroupsBeforeOrdering(t *testing.T) {
	clause := aggregateGroupOrderClause([]string{"project_id", "status"})
	if clause != " GROUP BY `project_id`, `status` ORDER BY `project_id`, `status`" {
		t.Fatalf("aggregate group/order clause = %q", clause)
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

func TestFederationFallsBackToUnversionedPublicationsWithoutActiveManifest(t *testing.T) {
	now := time.Now().UTC()
	executions := []bundlepublication.BundleExecution{
		{
			ID: "unversioned", BundleIdentity: bundlepublication.BundleIdentity{
				Name: "research-subject", Project: "project-a",
			}, State: bundlepublication.BundleReady, UpdatedAt: now,
			Outputs: []bundlepublication.BundleOutputRecord{
				{Name: "ResearchSubject", PhysicalTable: "loom_research_subject"},
				{Name: "patients", PhysicalTable: "loom_patients"},
			},
		},
		{
			ID: "versioned", BundleIdentity: bundlepublication.BundleIdentity{
				Name: "research-subject", Project: "project-a", DatasetGeneration: "generation-a",
			}, State: bundlepublication.BundleReady, UpdatedAt: now,
			Outputs: []bundlepublication.BundleOutputRecord{{Name: "ResearchSubject", PhysicalTable: "loom_research_subject_v1"}},
		},
	}
	pointers := map[string]bundlepublication.BundlePointer{}
	for _, execution := range executions {
		pointers[execution.PointerName()] = bundlepublication.BundlePointer{
			Name: execution.PointerName(), ExecutionID: execution.ID,
		}
	}
	reader := &Reader{
		Catalog:                  &federationCatalog{executions: executions, pointers: pointers},
		ActiveManifestResolver:   noActiveFederationResolver{},
		LegacyTranslationVersion: "legacy",
	}

	sources, err := reader.CurrentFederatedSources(context.Background(), []string{"project-a"}, DataframeSelector{Recipe: "research-subject", TranslationVersion: "legacy", Output: "ResearchSubject"})
	if err != nil {
		t.Fatalf("CurrentFederatedSources() error = %v", err)
	}
	if len(sources) != 1 || sources[0].ID != "unversioned:ResearchSubject" {
		t.Fatalf("sources = %#v, want only unversioned publication", sources)
	}

	resourceTypes, err := reader.CurrentFederatedResourceTypes(context.Background(), []string{"project-a"})
	if err != nil {
		t.Fatalf("CurrentFederatedResourceTypes() error = %v", err)
	}
	if len(resourceTypes) != 1 || resourceTypes[0] != "ResearchSubject" {
		t.Fatalf("resource types = %#v, want unversioned resource types", resourceTypes)
	}
	patients, err := reader.CurrentFederatedSources(context.Background(), []string{"project-a"}, DataframeSelector{Recipe: "research-subject", TranslationVersion: "legacy", Output: "patients"})
	if err != nil || len(patients) != 1 || patients[0].ID != "unversioned:patients" {
		t.Fatalf("exact non-FHIR output sources = %#v, %v", patients, err)
	}
}
