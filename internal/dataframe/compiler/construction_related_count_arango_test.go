package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestRelatedEligibilityCountRowsReverseMatchesForwardOnArango(t *testing.T) {
	if testing.Short() {
		t.Skip("Arango query equivalence requires the integration database")
	}
	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Patient"}, {Name: "Observation"}, {Name: "Specimen"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}

	project, generation := "loom_related_count_"+uuid.NewString(), "generation-related-count"
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, collection := range []string{"Patient", "Observation", "Specimen", "fhir_edge"} {
			query := fmt.Sprintf("FOR document IN %s FILTER document.project == @project REMOVE document IN %s", collection, collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"project": project}); err != nil {
				t.Errorf("remove related-count fixtures from %s: %v", collection, err)
			}
		}
	}()

	document := func(collection, id, resourceType, authPath string) json.RawMessage {
		t.Helper()
		encoded, err := json.Marshal(map[string]any{
			"_key": project + "_" + id, "id": id, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": resourceType, "auth_resource_path": authPath,
			"payload": map[string]any{"id": id, "resourceType": resourceType},
		})
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	collections := map[string][]json.RawMessage{
		"Patient": {
			document("Patient", "p1", "Patient", "/allowed"),
			document("Patient", "p2", "Patient", "/denied"),
			document("Patient", "p3", "Patient", "/allowed"),
		},
		"Observation": {
			document("Observation", "o1", "Observation", "/allowed"),
			document("Observation", "o2", "Observation", "/allowed"),
			document("Observation", "o3", "Observation", "/denied"),
			document("Observation", "o4", "Observation", "/allowed"),
		},
		"Specimen": {
			document("Specimen", "s1", "Specimen", "/allowed"),
			document("Specimen", "s2", "Specimen", "/denied"),
		},
	}
	for collection, documents := range collections {
		if err := client.InsertBatchRaw(ctx, collection, documents, false, "document"); err != nil {
			t.Fatalf("insert related-count documents into %s: %v", collection, err)
		}
	}
	edges := []map[string]any{
		relatedCountEdge(project, generation, "e1", relatedCountEndpoint(project, "Observation", "o1"), relatedCountEndpoint(project, "Patient", "p1"), "subject_Patient", "Observation", "Patient", "/allowed"),
		relatedCountEdge(project, generation, "e2", relatedCountEndpoint(project, "Observation", "o2"), relatedCountEndpoint(project, "Patient", "p1"), "subject_Patient", "Observation", "Patient", "/allowed"),
		relatedCountEdge(project, generation, "e3", relatedCountEndpoint(project, "Observation", "o3"), relatedCountEndpoint(project, "Patient", "p2"), "subject_Patient", "Observation", "Patient", "/denied"),
		relatedCountEdge(project, generation, "e4", relatedCountEndpoint(project, "Observation", "o4"), relatedCountEndpoint(project, "Patient", "p3"), "subject_Patient", "Observation", "Patient", "/allowed"),
		relatedCountEdge(project, generation, "e5", relatedCountEndpoint(project, "Observation", "o1"), relatedCountEndpoint(project, "Specimen", "s1"), "specimen_Specimen", "Observation", "Specimen", "/allowed"),
		relatedCountEdge(project, generation, "e6", relatedCountEndpoint(project, "Observation", "o2"), relatedCountEndpoint(project, "Specimen", "s1"), "specimen_Specimen", "Observation", "Specimen", "/allowed"),
		relatedCountEdge(project, generation, "e7", relatedCountEndpoint(project, "Observation", "o3"), relatedCountEndpoint(project, "Specimen", "s2"), "specimen_Specimen", "Observation", "Specimen", "/denied"),
	}
	edgeDocs := make([]json.RawMessage, 0, len(edges))
	for _, edge := range edges {
		encoded, err := json.Marshal(edge)
		if err != nil {
			t.Fatal(err)
		}
		edgeDocs = append(edgeDocs, encoded)
	}
	if err := client.InsertBatchRaw(ctx, "fhir_edge", edgeDocs, false, "document"); err != nil {
		t.Fatalf("insert related-count edges: %v", err)
	}

	plan := compileRelatedCountPhysicalPlan(t, project, generation)
	compare := func(name string, scoped ir.PhysicalPlan, want float64) {
		t.Helper()
		fast, err := aql.RenderPhysicalPlan(scoped)
		if err != nil {
			t.Fatalf("render fast %s plan: %v", name, err)
		}
		forward, err := aql.RenderPhysicalPlanWithTerminalProjection(scoped, "rows")
		if err != nil {
			t.Fatalf("render forward %s plan: %v", name, err)
		}
		gotFast := executeRelatedCountValue(t, ctx, client, fast.Query, fast.BindVars)
		gotForward := executeRelatedCountValue(t, ctx, client, forward.Query, forward.BindVars)
		if gotFast != gotForward || gotFast != want {
			t.Fatalf("%s count: reverse=%v forward=%v want=%v", name, gotFast, gotForward, want)
		}
	}
	compare("unrestricted", plan, 2)

	restricted := ir.ClonePhysicalPlan(plan)
	restricted.BindVars["auth_resource_paths_unrestricted"] = false
	restricted.BindVars["auth_resource_paths"] = []string{"/allowed"}
	compare("restricted", restricted, 1)

	empty := ir.ClonePhysicalPlan(plan)
	empty.BindVars["project"] = project + "_empty"
	compare("empty", empty, 0)
}

func relatedCountEndpoint(project, resourceType, id string) string {
	return resourceType + "/" + project + "_" + id
}

func relatedCountEdge(project, generation, key, from, to, label, fromType, toType, authPath string) map[string]any {
	return map[string]any{
		"_key": project + "_" + key, "_from": from, "_to": to, "project": project, "project_id": project,
		"dataset_generation": generation, "auth_resource_path": authPath,
		"label": label, "from_type": fromType, "to_type": toType,
	}
}

func compileRelatedCountPhysicalPlan(t *testing.T, project, generation string) ir.PhysicalPlan {
	t.Helper()
	output := recipe.Output{
		Name: "related_count_oracle", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id"}},
			Steps: []recipe.ConstructionStep{
				{
					ID: "eligible_patients", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedEligibilityOp, RelatedEligibility: &recipe.ConstructionRelatedEligibility{
						AnchorColumnID: "_key", ChoiceID: "patient-observation-specimen-choice", TargetNodeID: "specimen-node", TargetResourceType: "Specimen",
						Route: []recipe.ConstructionRelatedRouteStep{
							{EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node", FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL"},
							{EdgeID: "observation-specimen", FromNodeID: "observation-node", ToNodeID: "specimen-node", FromResourceType: "Observation", ToResourceType: "Specimen", Relationship: "specimen_Specimen", StorageDirection: "OUTBOUND", MatchMode: "OPTIONAL"},
						},
						ContributorPolicy: "ALL_MATCHES", MatchKind: recipe.RelatedEligibilityExists,
					}},
					Outputs: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id"}},
				},
				{
					ID: "count_eligible", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "eligible_patients"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
						ConstructionID: "count_eligible", Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
					}},
					Outputs: []recipe.StageColumn{{ID: "rows_id", Name: "rows"}},
				},
			},
		},
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
	bindings := recipe.RuntimeBindings{Project: project, DatasetGeneration: generation, AuthScopeMode: authscope.ReadScopeUnrestricted}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, project, generation)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return compiled.Outputs[0].Plan
}

func executeRelatedCountValue(t *testing.T, ctx context.Context, client *store.Client, query string, binds map[string]any) float64 {
	t.Helper()
	var value float64
	count := 0
	if err := client.QueryRows(ctx, query, 10, binds, func(row map[string]any) error {
		count++
		got, ok := row["rows"].(float64)
		if !ok {
			return fmt.Errorf("COUNT_ROWS result has type %T: %#v", row["rows"], row)
		}
		value = got
		return nil
	}); err != nil {
		t.Fatalf("execute related COUNT_ROWS query: %v\n%s", err, query)
	}
	if count != 1 {
		t.Fatalf("COUNT_ROWS emitted %d rows, want exactly one: %s", count, query)
	}
	return value
}
