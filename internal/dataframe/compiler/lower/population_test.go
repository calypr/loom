package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestPopulationRootSourceStartsFromMembersWithoutProvenance(t *testing.T) {
	rendered := renderPopulationRecipe(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 2,
			ResourceType: "Specimen",
		},
	})
	for _, want := range []string{
		"FOR population_member IN @@population_members_collection",
		"population_member.selectionId == @population_selection_id",
		"population_member.project == @population_project",
		"population_member.generation == @dataset_generation",
		"population_member.resourceType == @population_resource_type",
		"FOR population_source IN @@population_source_collection",
		"population_source.id == population_member.id",
		"COLLECT __loom_physical_population_root_key = population_source._key",
		"LET root = DOCUMENT(@@root_collection, __loom_physical_population_root_key)",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("ordinary population query is missing %q:\n%s", want, rendered.Query)
		}
	}
	if got := rendered.BindVars["@population_members_collection"]; got != "loom_explorer_selection_members" {
		t.Fatalf("population member collection bind = %#v", got)
	}
	if got := rendered.BindVars["population_project"]; got != "project/a" {
		t.Fatalf("population project bind = %#v", got)
	}
	if strings.Contains(rendered.Query, "FOR root IN @@root_collection") || strings.Contains(rendered.Query, "SORTED_UNIQUE") || strings.Contains(rendered.Query, "__loom_population_members") {
		t.Fatalf("ordinary population query materialized provenance:\n%s", rendered.Query)
	}
}

func TestPopulationSemijoinDirectRootHasNoHiddenProvenanceColumn(t *testing.T) {
	rendered, output := compilePopulationRecipe(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 2,
			ResourceType: "Specimen",
		},
	})
	for _, column := range output.OutputSchema {
		if column.Name == "__loom_population_members" || column.Name == "__loom_population_members_value" {
			t.Fatalf("ordinary output schema contains population provenance: %#v", output.OutputSchema)
		}
	}
	if strings.Contains(rendered.Query, "__loom_population_members") {
		t.Fatalf("ordinary population query contains hidden provenance:\n%s", rendered.Query)
	}
}

func TestPopulationConstructionRetainsRootStorageKeysAcrossGroupPivotAndCohort(t *testing.T) {
	population := func() *recipe.PopulationConstraint {
		return &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-construction", MembershipDigest: "sha256:population-construction", MemberCount: 2,
			ResourceType: "Patient",
		}
	}
	assertContributorColumn := func(t *testing.T, compiled CompiledRecipeOutput) {
		t.Helper()
		var found *CompiledOutputColumn
		for index := range compiled.OutputSchema {
			if compiled.OutputSchema[index].Name == rootContributorSetColumn {
				found = &compiled.OutputSchema[index]
				break
			}
		}
		if found == nil || !found.Internal || found.Kind != "string" || found.Cardinality != "many" || found.RootContributorResourceType != "Patient" {
			t.Fatalf("population construction final schema lacks typed Patient root contributor keys: %#v", compiled.OutputSchema)
		}
		public := publicCompiledSchema(compiled.OutputSchema)
		for _, column := range public {
			if column.Name == rootContributorSetColumn || column.Name == "_key" {
				t.Fatalf("compiler-owned root keys leaked into the public schema: %#v", public)
			}
		}
	}
	assertPublicNames := func(t *testing.T, compiled CompiledRecipeOutput, want []string) {
		t.Helper()
		public := publicCompiledSchema(compiled.OutputSchema)
		got := make([]string, len(public))
		for index, column := range public {
			got[index] = column.Name
		}
		if !equalStringSlices(got, want) {
			t.Fatalf("population construction public schema = %#v, want %#v", got, want)
		}
	}
	t.Run("group output", func(t *testing.T) {
		output := recipe.Output{
			Name: "PopulationGrouped", RootResourceType: "Patient", RowGrain: "patient",
			Fields: []recipe.Field{
				{Name: "id", ColumnID: "id", Expr: recipe.Expression{Select: "root.id"}},
				{Name: "active", ColumnID: "active", Expr: recipe.Expression{Select: "root.active"}},
			},
			Population: population(),
			Construction: &recipe.Construction{
				Version: 1, SourceColumns: []recipe.StageColumn{{ID: "id", Name: "id"}, {ID: "active", Name: "active"}},
				Steps: []recipe.ConstructionStep{{
					ID: "group_active", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
						ConstructionID: "group_active", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
						Keys:       []recipe.ConstructionGroupKey{{InputColumnID: "active", OutputColumnID: "active_group"}},
						Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "records"}},
					}},
					Outputs: []recipe.StageColumn{{ID: "active_group", Name: "active"}, {ID: "records", Name: "records", Type: "integer"}},
				}},
			},
		}
		_, compiled := compilePopulationRecipe(t, output)
		if compiled.Plan.StageSequence == nil || len(compiled.Plan.StageSequence.Stages) != 1 {
			t.Fatalf("population GROUP did not use the ordinary typed construction path: %#v", compiled.Plan.StageSequence)
		}
		group := compiled.Plan.StageSequence.Stages[0]
		if group.Group == nil || group.Group.RootContributorInputColumn != "_key" || group.Group.RootContributorOutputColumn != rootContributorSetColumn {
			t.Fatalf("population GROUP did not reduce the exact source storage keys: %#v", group.Group)
		}
		assertContributorColumn(t, compiled)
		assertPublicNames(t, compiled, []string{"active", "records"})
	})
	t.Run("pivot output", func(t *testing.T) {
		output := constructionTestOutput()
		output.Population = population()
		_, compiled := compilePopulationRecipe(t, output)
		if compiled.Plan.StageSequence == nil || len(compiled.Plan.StageSequence.Stages) == 0 {
			t.Fatalf("population Pivot did not use the ordinary typed construction path: %#v", compiled.Plan.StageSequence)
		}
		pivot := compiled.Plan.StageSequence.Stages[0]
		if pivot.GroupedPivot == nil || pivot.GroupedPivot.RootContributorInputColumn != "_key" || pivot.GroupedPivot.RootContributorOutputColumn != rootContributorSetColumn {
			t.Fatalf("population Pivot did not retain the exact source storage keys: %#v", pivot.GroupedPivot)
		}
		assertContributorColumn(t, compiled)
		last := output.Construction.Steps[len(output.Construction.Steps)-1]
		want := make([]string, len(last.Outputs))
		for index, column := range last.Outputs {
			want[index] = column.Name
		}
		assertPublicNames(t, compiled, want)
	})
	t.Run("cohort output", func(t *testing.T) {
		output := recipe.Output{
			Name: "PopulationCohort", RootResourceType: "Patient", RowGrain: "groups",
			Fields: []recipe.Field{
				{Name: "id", ColumnID: "id", Expr: recipe.Expression{Select: "root.id"}},
				{Name: "active", ColumnID: "active", Expr: recipe.Expression{Select: "root.active"}},
			},
			Population: population(),
			Construction: &recipe.Construction{
				Version: 1, SourceColumns: []recipe.StageColumn{{ID: "id", Name: "id"}, {ID: "active", Name: "active"}},
				Steps: []recipe.ConstructionStep{{
					ID: "group_active", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
						ConstructionID: "group_active", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
						Keys:       []recipe.ConstructionGroupKey{{InputColumnID: "active", OutputColumnID: "active_group"}},
						Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "records"}},
					}},
					Outputs: []recipe.StageColumn{{ID: "active_group", Name: "active"}, {ID: "records", Name: "records", Type: "integer"}},
				}},
			},
			GroupRows: &recipe.GroupRows{
				RevisionID: "grouprev_population_construction", AfterStepID: "group_active", UnassignedMemberPolicy: "EXCLUDE",
				RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "id", Policy: recipe.ConstructionRowValueAll}},
			},
		}
		_, compiled := compilePopulationRecipe(t, output)
		if compiled.Plan.StageSequence == nil || len(compiled.Plan.StageSequence.Stages) != 2 {
			t.Fatalf("population cohort did not use the ordinary typed construction path: %#v", compiled.Plan.StageSequence)
		}
		cohort := compiled.Plan.StageSequence.Stages[1]
		if cohort.CohortGroup == nil || cohort.CohortGroup.ContributorInputColumn != rootContributorSetColumn || cohort.CohortGroup.RootContributorOutputColumn != rootContributorSetColumn {
			t.Fatalf("population cohort did not consume and retain the exact prior root keys: %#v", cohort.CohortGroup)
		}
		assertContributorColumn(t, compiled)
		assertPublicNames(t, compiled, []string{"group_id", "group_label", "group_ordinal", "members", "id"})
	})
}

func TestPopulationRootSourceReversesRouteAndRemainsScoped(t *testing.T) {
	rendered := renderPopulationRecipe(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-documents", MembershipDigest: "sha256:members", MemberCount: 2,
			ResourceType: "DocumentReference",
			Route:        []recipe.PopulationRouteStep{{ResourceType: "DocumentReference", Relationship: "subject_Specimen"}},
		},
	})
	for _, want := range []string{
		"FOR population_source IN @@population_source_collection",
		"FOR population_root_edge_0 IN @@population_root_route_0_edge_collection",
		"population_root_edge_0._from == population_source._id",
		"population_root_edge_0.label == @population_root_route_0_label",
		"population_root_node_0.resourceType == @population_root_route_0_target_type",
		"population_member.selectionId == @population_selection_id",
		"population_member.project == @population_project",
		"population_member.generation == @dataset_generation",
		"population_member.resourceType == @population_resource_type",
		"population_source.id == population_member.id",
		"COLLECT __loom_physical_population_root_key = population_root_node_0._key",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("ordinary reversed population query is missing %q:\n%s", want, rendered.Query)
		}
	}
	if got := strings.Count(rendered.Query, "FOR population_member IN @@population_members_collection"); got != 1 {
		t.Fatalf("membership-driven scan count = %d, want 1:\n%s", got, rendered.Query)
	}
	if strings.Contains(rendered.Query, "FOR root IN @@root_collection") || strings.Contains(rendered.Query, "SORTED_UNIQUE") || strings.Contains(rendered.Query, "__loom_population_members") {
		t.Fatalf("ordinary reversed population query materialized provenance:\n%s", rendered.Query)
	}
}

func TestPopulationSelfRouteReversesThePinnedStorageDirection(t *testing.T) {
	cases := []struct {
		name                string
		storageDirection    string
		wantPhysicalRoute   string
		wantSourceEndpoint  string
		wantTargetTypeField string
	}{
		{
			name:             "inbound selected route reverses outbound",
			storageDirection: "INBOUND", wantPhysicalRoute: "OUTBOUND",
			wantSourceEndpoint:  "population_root_edge_0._from == population_source._id",
			wantTargetTypeField: "population_root_edge_0.to_type == @population_root_route_0_target_type",
		},
		{
			name:             "outbound selected route reverses inbound",
			storageDirection: "OUTBOUND", wantPhysicalRoute: "INBOUND",
			wantSourceEndpoint:  "population_root_edge_0._to == population_source._id",
			wantTargetTypeField: "population_root_edge_0.from_type == @population_root_route_0_target_type",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			rendered := renderPopulationRecipe(t, recipe.Output{
				Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
				Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
				Population: &recipe.PopulationConstraint{
					SelectionRevisionID: "selection-self", MembershipDigest: "sha256:members", MemberCount: 1,
					ResourceType: "Specimen",
					Route: []recipe.PopulationRouteStep{{
						ResourceType: "Specimen", Relationship: "parent", StorageDirection: test.storageDirection,
					}},
				},
			})
			for _, want := range []string{test.wantSourceEndpoint, test.wantTargetTypeField} {
				if !strings.Contains(rendered.Query, want) {
					t.Fatalf("population query is missing reverse-route evidence %q:\n%s", want, rendered.Query)
				}
			}
			if strings.Contains(rendered.Query, "population_root_edge_0._from == population_source._id") != (test.wantPhysicalRoute == "OUTBOUND") {
				t.Fatalf("population query did not preserve the pinned reverse direction %s:\n%s", test.wantPhysicalRoute, rendered.Query)
			}
		})
	}

	defaultRoute, err := BuildPhysicalTraversal(TraversalLoweringRequest{
		FromType: "Specimen", EdgeLabel: "parent", ToType: "Specimen",
		SourceVariable: "root", TargetVariable: "node", EdgeVariable: "edge",
		BindPrefix: "self_default", Policy: ir.DefaultPhysicalOptimizationPolicy(),
	})
	if err != nil {
		t.Fatalf("default self-type route: %v", err)
	}
	if defaultRoute.Traversal.Direction != ir.PhysicalInbound {
		t.Fatalf("global self-type route default changed to %q", defaultRoute.Traversal.Direction)
	}
}

func renderPopulationRecipe(t *testing.T, output recipe.Output) aql.RenderedPhysicalPlan {
	t.Helper()
	rendered, _ := compilePopulationRecipe(t, output)
	return rendered
}

func compilePopulationRecipe(t *testing.T, output recipe.Output) (aql.RenderedPhysicalPlan, CompiledRecipeOutput) {
	t.Helper()
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "population-test",
		TranslationVersion:  "population-test",
		Outputs:             []recipe.Output{output},
	}
	plan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{
		Project: "project-a", SelectionProject: "project/a", DatasetGeneration: "generation-a", SelectionMembersCollection: "loom_explorer_selection_members",
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Outputs) != 1 {
		t.Fatalf("compiled outputs = %d", len(compiled.Outputs))
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Outputs[0].Plan)
	if err != nil {
		t.Fatal(err)
	}
	return rendered, compiled.Outputs[0]
}
