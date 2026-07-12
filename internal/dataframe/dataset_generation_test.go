package dataframe

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

func TestDatasetGenerationCompilesRootTraversalAndRequiredMatch(t *testing.T) {
	compiled, err := CompileRequest(Builder{
		Project:           "P1",
		DatasetGeneration: " generation-a ",
		RootResourceType:  "Patient",
		Traversals: []TraversalStep{{
			Label:          "subject_Patient",
			ToResourceType: "Condition",
			Alias:          "diagnosis",
			MatchMode:      TraversalMatchRequired,
		}},
	}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := compiled.DatasetGeneration; got != "generation-a" {
		t.Fatalf("compiled DatasetGeneration = %q, want normalized exact generation", got)
	}
	if got := compiled.BindVars[datasetGenerationBindKey]; got != "generation-a" {
		t.Fatalf("compiled generation bind = %#v, want generation-a", got)
	}
	for _, want := range []string{
		"root.dataset_generation == @dataset_generation",
		"__edge.dataset_generation == @dataset_generation",
		"__node.dataset_generation == @dataset_generation",
		"__match_edge_0_0.dataset_generation == @dataset_generation",
		"__match_0_0.dataset_generation == @dataset_generation",
	} {
		if !strings.Contains(compiled.Query, want) {
			t.Fatalf("compiled query is missing generation predicate %q:\n%s", want, compiled.Query)
		}
	}

	semantic, err := BuildSemanticPlan(Builder{Project: "P1", DatasetGeneration: " generation-a ", RootResourceType: "Patient"})
	if err != nil {
		t.Fatal(err)
	}
	if got := semantic.DatasetGeneration; got != "generation-a" {
		t.Fatalf("semantic DatasetGeneration = %q, want normalized exact generation", got)
	}
}

func TestDatasetGenerationAbsentCompilesLegacyNullNamespace(t *testing.T) {
	compiled, err := CompileRequest(Builder{
		Project:          "P1",
		RootResourceType: "Specimen",
		Traversals: []TraversalStep{{
			Label:          "subject_Specimen",
			ToResourceType: "DocumentReference",
			Alias:          "file",
		}},
	}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got, present := compiled.BindVars[datasetGenerationBindKey]; !present || got != nil {
		t.Fatalf("legacy compiled generation bind = %#v (present=%t), want explicit nil", got, present)
	}
	for _, want := range []string{
		"root.dataset_generation == @dataset_generation",
		"edge_1.dataset_generation == @dataset_generation",
		"node_1.dataset_generation == @dataset_generation",
	} {
		if !strings.Contains(compiled.Query, want) {
			t.Fatalf("legacy query is missing null-generation predicate %q:\n%s", want, compiled.Query)
		}
	}
}

func TestDatasetGenerationPhysicalPlanAndRendererFilterEveryGraphDocument(t *testing.T) {
	semantic := SemanticPlan{
		Version:           1,
		Project:           "P1",
		DatasetGeneration: "generation-a",
		Root: SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []SemanticNode{{
				Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
			}},
		},
	}
	plan, err := BuildGenericPhysicalPlan(semantic)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.BindVars[datasetGenerationBindKey]; got != "generation-a" {
		t.Fatalf("physical generation bind = %#v, want generation-a", got)
	}
	for _, variable := range []string{"root", "edge_1", "node_1"} {
		if !hasDatasetGenerationFilter(plan, variable) {
			t.Fatalf("physical plan is missing exact generation filter for %q: %#v", variable, plan.Operations)
		}
	}
	if err := ValidateGenericPhysicalPlanScope(plan); err != nil {
		t.Fatalf("generation-safe physical plan failed scope validation: %v", err)
	}

	rendered, err := RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if got := rendered.BindVars[datasetGenerationBindKey]; got != "generation-a" {
		t.Fatalf("rendered generation bind = %#v, want generation-a", got)
	}
	for _, want := range []string{
		"FILTER root.dataset_generation == @dataset_generation",
		"FILTER edge_1.dataset_generation == @dataset_generation",
		"FILTER node_1.dataset_generation == @dataset_generation",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("rendered physical query missing %q:\n%s", want, rendered.Query)
		}
	}

	legacy := semantic
	legacy.DatasetGeneration = ""
	legacyPlan, err := BuildGenericPhysicalPlan(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if got, present := legacyPlan.BindVars[datasetGenerationBindKey]; !present || got != nil {
		t.Fatalf("legacy physical generation bind = %#v (present=%t), want explicit nil", got, present)
	}
}

func TestDatasetGenerationPhysicalScopeRejectsMissingTargetPredicate(t *testing.T) {
	plan, err := BuildGenericPhysicalPlan(SemanticPlan{
		Version: 1,
		Project: "P1",
		Root: SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []SemanticNode{{Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, operation := range plan.Operations {
		if operation.Kind == PhysicalFilterOp && isDatasetGenerationScopePredicate(operation.Filter.Predicate, "node_1") {
			plan.Operations = append(plan.Operations[:index], plan.Operations[index+1:]...)
			break
		}
	}
	if err := ValidateGenericPhysicalPlanScope(plan); err == nil || !strings.Contains(err.Error(), "node_1.dataset_generation") {
		t.Fatalf("missing target generation predicate error = %v", err)
	}
}

func TestServicePropagatesDatasetGenerationToEveryCatalogRead(t *testing.T) {
	fieldOptions := make([]catalog.PopulatedFieldOptions, 0)
	referenceOptions := make([]catalog.PopulatedReferenceOptions, 0)
	service := NewService(ServiceConfig{
		DiscoverFields: func(_ context.Context, options catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			fieldOptions = append(fieldOptions, options)
			return []catalog.PopulatedField{}, nil
		},
		DiscoverReferences: func(_ context.Context, options catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
			referenceOptions = append(referenceOptions, options)
			return []catalog.PopulatedReference{{FromType: "Patient", Label: "subject_Patient", ToType: "Condition"}}, nil
		},
		ExecuteRows: func(_ context.Context, _ ExecuteQueryOptions, query string, bindVars map[string]any, _ func(map[string]any) error) error {
			if got := bindVars[datasetGenerationBindKey]; got != "generation-a" {
				t.Fatalf("execution generation bind = %#v, want generation-a", got)
			}
			if !strings.Contains(query, "root.dataset_generation == @dataset_generation") {
				t.Fatalf("execution query lost root generation predicate:\n%s", query)
			}
			return nil
		},
	})
	_, err := service.Run(context.Background(), RunRequest{Builder: Builder{
		Project:           "P1",
		DatasetGeneration: " generation-a ",
		RootResourceType:  "Patient",
		Traversals: []TraversalStep{{
			Label:          "subject_Patient",
			ToResourceType: "Condition",
			Alias:          "diagnosis",
		}},
	}, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(fieldOptions) == 0 || len(referenceOptions) == 0 {
		t.Fatalf("catalog calls fields=%d references=%d, want both", len(fieldOptions), len(referenceOptions))
	}
	for index, options := range fieldOptions {
		if got := catalog.DatasetGenerationBindValue(options.DatasetGeneration); got != "generation-a" {
			t.Fatalf("field catalog call %d generation bind = %#v, want generation-a", index, got)
		}
	}
	for index, options := range referenceOptions {
		if got := catalog.DatasetGenerationBindValue(options.DatasetGeneration); got != "generation-a" {
			t.Fatalf("reference catalog call %d generation bind = %#v, want generation-a", index, got)
		}
	}
}

func hasDatasetGenerationFilter(plan PhysicalPlan, variable string) bool {
	for _, operation := range plan.Operations {
		if operation.Kind == PhysicalFilterOp && isDatasetGenerationScopePredicate(operation.Filter.Predicate, variable) {
			return true
		}
	}
	return false
}
