package published

import (
	"context"
	"strings"
	"testing"
	"time"

	bundlepublication "github.com/calypr/loom/internal/dataframe/publication"
	publication "github.com/calypr/loom/internal/dataset"
)

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

func (c *federationCatalog) ListExecutions(_ context.Context, state bundlepublication.BundleState, before time.Time) ([]bundlepublication.BundleExecution, error) {
	result := make([]bundlepublication.BundleExecution, 0, len(c.executions))
	for _, execution := range c.executions {
		if execution.State == state && execution.UpdatedAt.Before(before) {
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
		Catalog:                &federationCatalog{executions: executions, pointers: pointers},
		ActiveManifestResolver: noActiveFederationResolver{},
	}

	sources, err := reader.CurrentFederatedSources(context.Background(), []string{"project-a"}, "ResearchSubject")
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
	if _, err := reader.CurrentFederatedSources(context.Background(), []string{"project-a"}, "patients"); err == nil {
		t.Fatal("CurrentFederatedSources accepted a non-FHIR dataset name")
	}
}
