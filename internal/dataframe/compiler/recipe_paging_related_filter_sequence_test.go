package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestCompileRecipeOutputPageOrdersMixedRelatedFilterSequences(t *testing.T) {
	rootID := "observation-page-root"
	patientID := "patient-page-related"
	practitionerID := "practitioner-page-related"

	t.Run("root filter before shared related producer", func(t *testing.T) {
		output := rootAndRelatedPageFilterOutput(false)
		columns := output.Construction.SourceColumns
		output.Construction.Steps = []recipe.ConstructionStep{
			pageFilterStep("keep_root", "", "root-id", recipe.FilterEquals, rootID, columns),
			pageFilterStep("keep_patient", "keep_root", "patient-id", recipe.FilterEquals, patientID, columns),
			pageFilterStep("require_patient", "keep_patient", "patient-id", recipe.FilterExists, "", columns),
		}
		page := compileRelatedConstructionPage(t, "root-before-shared-related", output)
		seed, rootQuery := splitRootPageCandidateSeed(t, page.RootKeysQuery)
		assertPageQueryOrder(t, seed,
			"child_set_1_node.payload.id == @construction_filter_value_1",
			"FOR child_set_1_edge IN",
			"RETURN root._key",
		)
		assertPageQueryOrder(t, rootQuery,
			"FILTER root._key > @loom_root_page_after_key",
			"FILTER __loom_construction_input_1 == @construction_filter_value",
			"LET child_set_1 = UNIQUE",
			"FILTER child_set_1_reduced.__loom_reduced_0 == @construction_filter_value_1",
			"FILTER child_set_1_reduced.__loom_reduced_0 != null",
			"SORT root._key ASC",
			"LIMIT @limit",
		)
		if count := strings.Count(page.RootKeysQuery, "LET child_set_1 = UNIQUE"); count != 1 {
			t.Fatalf("shared related producer count = %d, want 1:\n%s", count, page.RootKeysQuery)
		}
	})

	t.Run("related producer before root expression", func(t *testing.T) {
		output := rootAndRelatedPageFilterOutput(false)
		columns := output.Construction.SourceColumns
		output.Construction.Steps = []recipe.ConstructionStep{
			pageFilterStep("keep_patient", "", "patient-id", recipe.FilterEquals, patientID, columns),
			pageFilterStep("keep_root", "keep_patient", "root-id", recipe.FilterEquals, rootID, columns),
		}
		page := compileRelatedConstructionPage(t, "related-before-root-expression", output)
		seed, rootQuery := splitRootPageCandidateSeed(t, page.RootKeysQuery)
		assertPageQueryOrder(t, seed,
			"child_set_1_node.payload.id == @construction_filter_value",
			"FOR child_set_1_edge IN",
			"RETURN root._key",
		)
		assertPageQueryOrder(t, rootQuery,
			"FILTER root._key > @loom_root_page_after_key",
			"LET child_set_1 = UNIQUE",
			"FILTER child_set_1_reduced.__loom_reduced_0 == @construction_filter_value",
			"FILTER __loom_construction_input_2 == @construction_filter_value_1",
			"SORT root._key ASC",
			"LIMIT @limit",
		)
	})

	t.Run("two independent related producers", func(t *testing.T) {
		output := rootAndRelatedPageFilterOutput(true)
		columns := output.Construction.SourceColumns
		output.Construction.Steps = []recipe.ConstructionStep{
			pageFilterStep("keep_patient", "", "patient-id", recipe.FilterEquals, patientID, columns),
			pageFilterStep("keep_practitioner", "keep_patient", "practitioner-id", recipe.FilterEquals, practitionerID, columns),
		}
		page := compileRelatedConstructionPage(t, "independent-related-producers", output)
		seed, rootQuery := splitRootPageCandidateSeed(t, page.RootKeysQuery)
		assertPageQueryOrder(t, seed,
			"child_set_1_node.payload.id == @construction_filter_value",
			"FOR child_set_1_edge IN",
			"RETURN root._key",
		)
		assertPageQueryOrder(t, rootQuery,
			"FILTER root._key > @loom_root_page_after_key",
			"LET child_set_1 = UNIQUE",
			"FILTER child_set_1_reduced.__loom_reduced_0 == @construction_filter_value",
			"LET child_set_2 = UNIQUE",
			"FILTER child_set_2_reduced.__loom_reduced_0 == @construction_filter_value_1",
			"SORT root._key ASC",
			"LIMIT @limit",
		)
	})
}

func splitRootPageCandidateSeed(t *testing.T, query string) (string, string) {
	t.Helper()
	marker := "LET root = DOCUMENT(@@root_collection, __loom_physical_root_page_candidate_key)"
	index := strings.Index(query, marker)
	if index < 0 {
		t.Fatalf("root-key query is missing candidate-key restoration:\n%s", query)
	}
	return query[:index], query[index:]
}

func rootAndRelatedPageFilterOutput(includePractitioner bool) recipe.Output {
	rootColumn := recipe.StageColumn{ID: "root-id", Name: "root_id", Label: "Root ID", Type: "string"}
	patientColumn := recipe.StageColumn{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"}
	columns := []recipe.StageColumn{rootColumn, patientColumn}
	output := recipe.Output{
		Name: "observations", RootResourceType: "Observation", RootOccurrenceID: "base", RowGrain: "observation",
		RootColumnNaming: recipe.RootColumnNamingExact, TraversalColumnNaming: recipe.TraversalColumnNamingExact,
		Fields: []recipe.Field{{Name: "root_id", ColumnID: "root-id", Expr: recipe.Expression{Select: "root.id"}}},
		Traversals: []recipe.Traversal{{
			Name: "subject_Patient", OccurrenceID: "patient-occurrence", Alias: "patient-occurrence", ToResourceType: "Patient",
			MatchMode: recipe.MatchOptional,
			Fields: []recipe.Field{{
				Name: "patient_id", ColumnID: "patient-id", Label: "Patient ID", FieldRef: "id",
				Expr: recipe.Expression{Select: "patient-occurrence.id"}, ValueMode: recipe.ValueModeAuto,
			}},
		}},
		Construction: &recipe.Construction{Version: 1, SourceColumns: columns},
	}
	if includePractitioner {
		practitionerColumn := recipe.StageColumn{ID: "practitioner-id", Name: "practitioner_id", Label: "Practitioner ID", Type: "string"}
		output.Construction.SourceColumns = append(output.Construction.SourceColumns, practitionerColumn)
		output.Traversals = append(output.Traversals, recipe.Traversal{
			Name: "performer_Practitioner", OccurrenceID: "practitioner-occurrence", Alias: "practitioner-occurrence", ToResourceType: "Practitioner",
			MatchMode: recipe.MatchOptional,
			Fields: []recipe.Field{{
				Name: "practitioner_id", ColumnID: "practitioner-id", Label: "Practitioner ID", FieldRef: "id",
				Expr: recipe.Expression{Select: "practitioner-occurrence.id"}, ValueMode: recipe.ValueModeAuto,
			}},
		})
	}
	return output
}

func pageFilterStep(id, inputStepID, columnID string, operator recipe.FilterOperator, value string, outputs []recipe.StageColumn) recipe.ConstructionStep {
	input := recipe.ConstructionInputRef{Kind: recipe.ConstructionSourceProjectionInput}
	if inputStepID != "" {
		input = recipe.ConstructionInputRef{Kind: recipe.ConstructionStepOutputInput, StepID: inputStepID}
	}
	filter := &recipe.ConstructionFilter{ColumnID: columnID, Operator: operator}
	if operator != recipe.FilterExists && operator != recipe.FilterMissing {
		filter.Values = []recipe.FilterValue{{Kind: recipe.FilterString, String: &value}}
	}
	return recipe.ConstructionStep{
		ID: id, Inputs: []recipe.ConstructionInputRef{input},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: filter},
		Outputs:   append([]recipe.StageColumn(nil), outputs...),
	}
}

func compileRelatedConstructionPage(t *testing.T, name string, output recipe.Output) CompiledOutputPage {
	t.Helper()
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: name, TranslationVersion: name,
		Outputs: []recipe.Output{output},
	}
	bindings := recipe.RuntimeBindings{Project: "project-a", SelectionProject: "project/a", DatasetGeneration: "generation-a"}
	semanticPlan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(semanticPlan, "scope-a", bindings.DatasetGeneration)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	page, err := CompileRecipeOutputPageWithPolicy(compiled.Outputs[0], bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func assertPageQueryOrder(t *testing.T, query string, markers ...string) {
	t.Helper()
	previous := -1
	for _, marker := range markers {
		index := strings.Index(query, marker)
		if index <= previous {
			t.Fatalf("query marker %q is missing or out of order after offset %d:\n%s", marker, previous, query)
		}
		previous = index
	}
}
