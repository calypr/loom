package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestCompileRecipeOutputPageSelectsRootsBeforeExpansion(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Fields: []semantic.SemanticField{{
			Name: "id", FieldRef: "Patient.id",
			Expr: semantic.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "id"})}, Projection: spec.ProjectionScalar,
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	selector, err := spec.ParseSelector("extension[].url")
	if err != nil {
		t.Fatal(err)
	}
	unnest := ir.PhysicalOperation{Kind: ir.PhysicalUnnestOp, Unnest: &ir.PhysicalUnnest{
		Owner:          ir.PhysicalUnnestOwner{ResourceType: "Patient", RootVariable: "root", OwnerVariable: "root"},
		OutputVariable: "item", HasItemVariable: "has_item", EmptyPolicy: ir.PhysicalUnnestExclude,
		Expression: ir.PhysicalExpression{
			Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull,
			Extract: &ir.PhysicalExtract{Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}, ResourceType: "Patient", Selector: selector, ExecutionMode: ir.PhysicalSelectorConditionalArray},
		},
	}}
	plan.Operations = append(plan.Operations, ir.PhysicalOperation{})
	copy(plan.Operations[6:], plan.Operations[5:])
	plan.Operations[5] = unnest
	page, err := CompileRecipeOutputPageWithPolicy(lower.CompiledRecipeOutput{
		Name: "patients", RootResourceType: "Patient", Plan: plan,
	}, recipe.RuntimeBindings{Project: "p"}, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if got := page.RootKeysBindVars[RootPageSizeBind]; got != 25 {
		t.Fatalf("root page size bind = %#v, want 25", got)
	}
	if strings.Contains(page.RootKeysQuery, "unnest") || strings.Contains(page.RootKeysQuery, "extension") {
		t.Fatalf("root-key discovery crossed the expansion boundary:\n%s", page.RootKeysQuery)
	}
	keyFilter := strings.Index(page.RowsQuery, "root._key IN @"+RootPageKeysBind)
	unnestIndex := strings.Index(page.RowsQuery, "LET item =")
	if keyFilter < 0 || unnestIndex < 0 || keyFilter > unnestIndex {
		t.Fatalf("selected-root filter was not rendered before UNNEST:\n%s", page.RowsQuery)
	}
	if !strings.Contains(page.RootKeysQuery, "root._key > @"+RootPageAfterKeyBind) {
		t.Fatalf("root-key discovery is not a keyset query:\n%s", page.RootKeysQuery)
	}
}

func TestCompileRecipeOutputPageKeepsRelatedStageOutOfRootKeyDiscovery(t *testing.T) {
	sourceFields := []recipe.Field{
		{Name: "patient_id", ColumnID: "patient-id", Expr: recipe.Expression{Select: "root.id"}},
		{Name: "patient_active", ColumnID: "patient-active", Expr: recipe.Expression{Select: "root.active"}},
		{Name: "patient_gender", ColumnID: "patient-gender", Expr: recipe.Expression{Select: "root.gender"}},
		{Name: "patient_birth_date", ColumnID: "patient-birth-date", Expr: recipe.Expression{Select: "root.birthDate"}},
		{Name: "patient_deceased", ColumnID: "patient-deceased", Expr: recipe.Expression{Select: "root.deceasedBoolean"}},
		{Name: "patient_multiple_birth", ColumnID: "patient-multiple-birth", Expr: recipe.Expression{Select: "root.multipleBirthBoolean"}},
		{Name: "patient_family", ColumnID: "patient-family", Expr: recipe.Expression{Select: "root.name[].family"}, ValueMode: recipe.ValueModeFirst},
		{Name: "patient_telecom", ColumnID: "patient-telecom", Expr: recipe.Expression{Select: "root.telecom[].value"}, ValueMode: recipe.ValueModeFirst},
	}
	sourceColumns := make([]recipe.StageColumn, 0, len(sourceFields))
	for _, field := range sourceFields {
		sourceColumns = append(sourceColumns, recipe.StageColumn{ID: field.ColumnID, Name: field.Name, Label: field.Label})
	}
	outputColumns := append(append([]recipe.StageColumn(nil), sourceColumns...), recipe.StageColumn{ID: "observation-status", Name: "observation_status", Label: "Observation statuses"})
	output := recipe.Output{
		Name: "patients", RootResourceType: "Patient", RowGrain: "patient", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: sourceFields,
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: sourceColumns,
			Steps: []recipe.ConstructionStep{{
				ID: "related_observation_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: &recipe.ConstructionRelatedSource{
					AnchorColumnID: "_key", ChoiceID: "choice-observation-status", SourceOccurrenceID: "observation-node",
					Source:            recipe.ConstructionRelatedFieldSource{CandidateID: "observation-status", NodeID: "observation-node", ResourceType: "Observation", Path: "Observation.status", Cardinality: "optional_one", LogicalType: "string"},
					Route:             []recipe.ConstructionRelatedRouteStep{{EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node", FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL"}},
					ContributorPolicy: "ALL_MATCHES", Form: "ALL", OutputColumnID: "observation-status",
				}},
				Outputs: outputColumns,
			}},
		},
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "related-source-paging", TranslationVersion: "test", Outputs: []recipe.Output{output}}
	bindings := recipe.RuntimeBindings{Project: "project-a", SelectionProject: "project/a", DatasetGeneration: "generation-a"}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope-a", bindings.DatasetGeneration)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	page, err := CompileRecipeOutputPageWithPolicy(compiled.Outputs[0], bindings, 2, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile paged related-source output: %v", err)
	}
	if strings.Contains(page.RootKeysQuery, "observation_status") || strings.Contains(page.RootKeysQuery, "@@related_") {
		t.Fatalf("root-key discovery evaluated the related-source stage:\n%s", page.RootKeysQuery)
	}
	if !strings.Contains(page.RootKeysQuery, "root._key > @"+RootPageAfterKeyBind) || page.RootKeysBindVars[RootPageSizeBind] != 2 {
		t.Fatalf("root-key discovery lost its keyset window: query=%s binds=%#v", page.RootKeysQuery, page.RootKeysBindVars)
	}
	if !strings.Contains(page.RowsQuery, "root._key IN @"+RootPageKeysBind) || !strings.Contains(page.RowsQuery, "observation_status") || !strings.Contains(page.RowsQuery, "@@related_0_hop_1_edge_collection") {
		t.Fatalf("selected-root page did not retain the related-source stage:\n%s", page.RowsQuery)
	}
}

func TestCompileRecipeOutputPageRetainsSinglePopulationComputation(t *testing.T) {
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "population-page-test",
		TranslationVersion:  "population-page-test",
		Outputs: []recipe.Output{{
			Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
			Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
			Population: &recipe.PopulationConstraint{
				SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 2,
				ResourceType: "Specimen",
			},
		}},
	}
	bindings := recipe.RuntimeBindings{Project: "project-a", SelectionProject: "project/a", DatasetGeneration: "generation-a", SelectionMembersCollection: "loom_explorer_selection_members"}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope-a", "generation-a")
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
	for name, query := range map[string]string{"root keys": page.RootKeysQuery, "rows": page.RowsQuery} {
		if got := strings.Count(query, "FOR population_member IN @@population_members_collection"); got != 1 {
			t.Fatalf("%s population member scan count = %d, want 1:\n%s", name, got, query)
		}
		if !strings.Contains(query, "FOR population_source IN @@population_source_collection") || !strings.Contains(query, "COLLECT __loom_physical_population_root_key = population_source._key") {
			t.Fatalf("%s query lost the membership-driven population root source:\n%s", name, query)
		}
		if strings.Contains(query, "__loom_population_members_value") || strings.Contains(query, "__loom_population_members") {
			t.Fatalf("%s ordinary page query materialized population provenance:\n%s", name, query)
		}
	}
	filterIndex := strings.Index(page.RowsQuery, "root._key IN @"+RootPageKeysBind)
	populationIndex := strings.Index(page.RowsQuery, "FOR population_member IN @@population_members_collection")
	if populationIndex < 0 || filterIndex < 0 || populationIndex > filterIndex {
		t.Fatalf("population eligibility was not evaluated before selected-root paging:\n%s", page.RowsQuery)
	}
}

func TestCompileRecipeOutputPageFiltersConstructionRowsBeforeRootWindow(t *testing.T) {
	id := "b7cad184-db67-5542-a975-10fffa3e89e7"
	output := constructionEqualsPageOutput(id)
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "construction-filter-root-page",
		TranslationVersion:  "construction-filter-root-page",
		Outputs:             []recipe.Output{output},
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
	page, err := CompileRecipeOutputPageWithPolicy(compiled.Outputs[0], bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	letSuffix := " = root.payload.id"
	letValue := strings.Index(page.RootKeysQuery, letSuffix)
	if letValue < 0 {
		t.Fatalf("root-key query lost the source projection for the filtered ID:\n%s", page.RootKeysQuery)
	}
	letStart := strings.LastIndex(page.RootKeysQuery[:letValue], "\n") + 1
	letVariable := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(page.RootKeysQuery[letStart:letValue]), "LET "))
	filterText := "FILTER " + letVariable + " == @construction_filter_value"
	filter := strings.Index(page.RootKeysQuery, filterText)
	window := strings.Index(page.RootKeysQuery, "SORT root._key ASC")
	limit := strings.Index(page.RootKeysQuery, "LIMIT @limit")
	if letVariable == "" || filter < 0 || window < 0 || limit < 0 || letValue > filter || filter > window || window > limit {
		t.Fatalf("construction EQUALS filter must compare the source ID value and run before the keyset page window: let=%q filter=%d sort=%d limit=%d\n%s", letVariable, filter, window, limit, page.RootKeysQuery)
	}
	for name, bindVars := range map[string]map[string]any{"root keys": page.RootKeysBindVars, "selected rows": page.RowsBindVars} {
		if got := bindVars["construction_filter_value"]; got != id {
			t.Fatalf("%s construction filter bind = %#v, want %q", name, got, id)
		}
	}
	if !strings.Contains(page.RowsQuery, "@construction_filter_value") {
		t.Fatalf("selected-root execution lost the construction filter:\n%s", page.RowsQuery)
	}
}

func constructionEqualsPageOutput(id string) recipe.Output {
	columns := []recipe.StageColumn{{ID: "specimen-id", Name: "specimen_id", Label: "Specimen ID"}}
	return recipe.Output{
		Name: "specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "specimen_id", ColumnID: "specimen-id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: columns,
			Steps: []recipe.ConstructionStep{{
				ID: "keep_matching_specimens", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
					ColumnID: "specimen-id", Operator: recipe.FilterEquals,
					Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &id}},
				}},
				Outputs: columns,
			}},
		},
	}
}
