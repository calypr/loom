package compilation

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestAuthoredStandaloneStructuredValueAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	const resource, path = "MedicationAdministration", "occurenceTiming.repeat.boundsRange"
	project := "structured_authoring_" + uuid.NewString()
	want := map[string]any{
		"low":  map[string]any{"value": float64(0), "unit": "day"},
		"high": map[string]any{"value": float64(7), "unit": "day"},
	}
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: resource}}}); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{
		"_key": project, "id": project, "project": project, "project_id": project, "dataset_generation": "generation-a", "resourceType": resource,
		"payload": map[string]any{"resourceType": resource, "id": project, "occurenceTiming": map[string]any{"repeat": map[string]any{"boundsRange": want}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.InsertBatchRaw(ctx, resource, []json.RawMessage{raw}, false, "document"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.QueryRows(ctx, "REMOVE @key IN @@collection", 1, map[string]any{"key": project, "@collection": resource}, func(map[string]any) error { return nil }); err != nil {
			t.Errorf("remove fixture: %v", err)
		}
	}()
	snapshot := fixtureSnapshotForProject(project)
	snapshot.Nodes = []capability.Node{{ID: "medication", ResourceType: resource, RowRootEligible: true}}
	snapshot.Edges = nil
	snapshot.Candidates = []capability.Candidate{{ID: "timing-range", NodeID: "medication", ResourceType: resource, FieldPath: path, LogicalType: "object", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}}}
	document := authoringv2.Document{
		Kind: authoringv2.Kind, Rows: authoringv2.RecordsRowDefinition(), Output: authoringv2.Output{ID: "medications", Title: "Medications"}, RootResourceType: resource,
		Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: resource},
		Columns: []authoringv2.Column{{Column: "timing_range", Label: "Timing range", LogicalType: "object", OccurrenceID: authoringv2.RootOccurrenceID,
			Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: path, ProjectionMode: "VALUE"}},
		}},
	}
	compiled, err := Compile(ctx, project, "explorer", document, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.OutputContract.Columns) != 1 || compiled.OutputContract.Columns[0].LogicalType != "object" {
		t.Fatalf("structured value lost its object contract: %#v", compiled.OutputContract)
	}
	plan, err := semantic.BuildRecipePlan(compiled.Bundle, recipe.RuntimeBindings{Project: project, DatasetGeneration: "generation-a"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	physical, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(physical.Outputs[0].Plan)
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := client.QueryRows(ctx, rendered.Query, 10, rendered.BindVars, func(row map[string]any) error { rows = append(rows, row); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !reflect.DeepEqual(rows[0]["timing_range"], want) {
		t.Fatalf("whole timing range changed: %#v, want %#v\n%s\n%#v", rows, want, rendered.Query, rendered.BindVars)
	}
}
