package compiler

import (
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestCompilePopulationMappingDirectKeepsMemberRowWitnessesInternal(t *testing.T) {
	compiled := compilePopulationMappingRecipe(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 3,
			ResourceType: "Specimen",
		},
	})
	if got := strings.Count(testAQLText(t, compiled.Command), "FOR population_member IN @@population_members_collection"); got != 1 {
		t.Fatalf("mapping member scan count = %d, want 1:\n%s", got, testAQLText(t, compiled.Command))
	}
	for _, want := range []string{
		"COLLECT __loom_physical_population_root_key = population_source._key INTO __loom_physical_population_member_ids = population_member.id",
		"LET __loom_population_members_value = SORTED_UNIQUE(__loom_physical_population_member_ids)",
		"FOR __loom_physical_population_mapping_member IN (__loom_population_members_value == null ? [] : __loom_population_members_value)",
		"@__loom_physical_population_mapping_member_name",
		"@__loom_physical_population_mapping_identity_name",
		"[@project, root._key]",
	} {
		if !strings.Contains(testAQLText(t, compiled.Command), want) {
			t.Fatalf("mapping query is missing %q:\n%s", want, testAQLText(t, compiled.Command))
		}
	}
	if strings.Contains(testAQLText(t, compiled.Command), "RETURN { [@__loom_projection") {
		t.Fatalf("mapping query used a public projection return:\n%s", testAQLText(t, compiled.Command))
	}
	if !containsBindValue(testAQLBinds(t, compiled.Command), ir.PhysicalPopulationMappingMemberField) {
		t.Fatalf("member witness bind is missing from %#v", testAQLBinds(t, compiled.Command))
	}
	if !containsBindValue(testAQLBinds(t, compiled.Command), ir.PhysicalPopulationMappingIdentityPartsField) {
		t.Fatalf("identity-parts witness bind is missing from %#v", testAQLBinds(t, compiled.Command))
	}
	if compiled.IdentityPartsColumn != ir.PhysicalPopulationMappingIdentityPartsField || compiled.ExplicitIdentityColumn != "" {
		t.Fatalf("default mapping identity columns = (%q, %q)", compiled.IdentityPartsColumn, compiled.ExplicitIdentityColumn)
	}
	if compiled.RowIdentity == nil || len(compiled.RowIdentity.Fields) != 2 || compiled.RowIdentity.Fields[0] != "project" || compiled.RowIdentity.Fields[1] != "_key" {
		t.Fatalf("default mapping row identity = %#v", compiled.RowIdentity)
	}
}

func TestCompilePopulationMappingExplicitIdentityUsesCompiledExpression(t *testing.T) {
	compiled := compilePopulationMappingRecipe(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields:   []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Identity: &recipe.Identity{Name: "row", Expr: recipe.Expression{Select: "root.id"}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 3,
			ResourceType: "Specimen",
		},
	})
	if !strings.Contains(testAQLText(t, compiled.Command), "@__loom_physical_population_mapping_identity_name]: root.payload.id") || strings.Contains(testAQLText(t, compiled.Command), "@__loom_physical_population_mapping_identity_name]: [root.payload.id]") {
		t.Fatalf("mapping query did not preserve the compiled explicit identity expression:\n%s", testAQLText(t, compiled.Command))
	}
	if containsBindValue(testAQLBinds(t, compiled.Command), ir.PhysicalPopulationMappingIdentityPartsField) {
		t.Fatalf("explicit mapping unexpectedly rendered default identity parts: %#v", testAQLBinds(t, compiled.Command))
	}
	if !containsBindValue(testAQLBinds(t, compiled.Command), ir.PhysicalPopulationMappingExplicitIdentityField) {
		t.Fatalf("explicit identity bind is missing: %#v", testAQLBinds(t, compiled.Command))
	}
	if compiled.IdentityPartsColumn != "" || compiled.ExplicitIdentityColumn != ir.PhysicalPopulationMappingExplicitIdentityField {
		t.Fatalf("explicit mapping identity columns = (%q, %q)", compiled.IdentityPartsColumn, compiled.ExplicitIdentityColumn)
	}
}

func TestCompilePopulationMappingDefaultIdentityIsStableAcrossInnerOuterExpansion(t *testing.T) {
	queries := make(map[string]CompiledPopulationMappingQuery, 2)
	for _, mode := range []string{"INNER", "OUTER"} {
		output := compilePopulationMappingOutput(t, recipe.Output{
			Name: "Specimens", RootResourceType: "Specimen", RowGrain: "expanded",
			Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
			Expand: &recipe.Expansion{From: recipe.Expression{Select: "root.extension[]"}, As: "item"},
			Population: &recipe.PopulationConstraint{
				SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 2,
				ResourceType: "Specimen",
			},
		})
		if mode == "OUTER" {
			for index := range output.Plan.Operations {
				if output.Plan.Operations[index].Kind == ir.PhysicalUnnestOp && output.Plan.Operations[index].Unnest != nil {
					output.Plan.Operations[index].Unnest.EmptyPolicy = ir.PhysicalUnnestPreserveParent
				}
			}
		}
		compiled, err := CompilePopulationMappingOutput(ArangoTarget(), output, recipe.RuntimeBindings{}, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(testAQLText(t, compiled.Command), "[@project, root._key]") {
			t.Fatalf("%s mapping query does not carry canonical project/key identity parts:\n%s", mode, testAQLText(t, compiled.Command))
		}
		if compiled.IdentityPartsColumn != ir.PhysicalPopulationMappingIdentityPartsField || compiled.ExplicitIdentityColumn != "" {
			t.Fatalf("%s mapping identity columns = (%q, %q)", mode, compiled.IdentityPartsColumn, compiled.ExplicitIdentityColumn)
		}
		queries[mode] = compiled
	}
	if !reflect.DeepEqual(queries["INNER"].RowIdentity, queries["OUTER"].RowIdentity) {
		t.Fatalf("inner/outer mapping identities differ: %#v != %#v", queries["INNER"].RowIdentity, queries["OUTER"].RowIdentity)
	}
}

func TestCompilePopulationMappingRejectsMissingIdentityField(t *testing.T) {
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 3,
			ResourceType: "Specimen",
		},
	})
	output.RowIdentity.Fields = []string{"project", "missing_identity_field"}
	if _, err := CompilePopulationMappingOutput(ArangoTarget(), output, recipe.RuntimeBindings{}, ir.DefaultPhysicalOptimizationPolicy()); err == nil || !strings.Contains(err.Error(), "missing_identity_field") {
		t.Fatalf("missing identity field error = %v", err)
	}
}

func TestPopulationMappingPhysicalPlanCollectsMatchedIDsOnceAndUsesFinalTerminal(t *testing.T) {
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 3,
			ResourceType: "Specimen",
		},
	})
	physical, err := mappingPhysicalPlan(output, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	var terminal *ir.PhysicalPopulationMappingReturn
	var rootSource *ir.PhysicalPopulationRootSource
	for index := range physical.Operations {
		operation := &physical.Operations[index]
		if operation.Kind == ir.PhysicalRootScanOp && operation.RootScan != nil {
			rootSource = operation.RootScan.Population
		}
		if operation.Kind == ir.PhysicalPopulationMappingReturnOp {
			terminal = operation.PopulationMappingReturn
		}
	}
	if rootSource == nil || terminal == nil {
		t.Fatalf("mapping plan missing population root source or witness terminal: %#v", physical.Operations)
	}
	if rootSource.MemberScan.Variable != "population_member" || rootSource.MemberScan.CollectionBindKey != "population_members_collection" {
		t.Fatalf("mapping root source has wrong member scan: %#v", rootSource.MemberScan)
	}
	if rootSource.CollectMembersVariable != ir.PopulationMappingMembersVariable {
		t.Fatalf("mapping root source does not collect member witnesses: %#v", rootSource)
	}
	if rootSource.MemberID.Variable != "population_member" || len(rootSource.MemberID.Path) != 1 || rootSource.MemberID.Path[0] != "id" {
		t.Fatalf("mapping root source does not collect selected IDs: %#v", rootSource.MemberID)
	}
	if terminal.Members.Value == nil || terminal.Members.Value.Variable != ir.PopulationMappingMembersVariable {
		t.Fatalf("witness terminal does not reuse matched-member value: %#v", terminal.Members)
	}
	if len(terminal.IdentityParts) != 2 || terminal.IdentityParts[0].Name != "project" || terminal.IdentityParts[1].Name != "_key" {
		t.Fatalf("witness terminal identity parts are not canonical: %#v", terminal.IdentityParts)
	}
	if terminal.IdentityParts[0].Expression.Value == nil || terminal.IdentityParts[0].Expression.Value.BindKey != "project" {
		t.Fatalf("witness terminal project identity is not compiler-bound: %#v", terminal.IdentityParts[0])
	}
	if terminal.IdentityParts[1].Expression.Value == nil || terminal.IdentityParts[1].Expression.Value.Variable != "root" || len(terminal.IdentityParts[1].Expression.Value.Path) != 1 || terminal.IdentityParts[1].Expression.Value.Path[0] != "_key" {
		t.Fatalf("witness terminal key identity is not final root key: %#v", terminal.IdentityParts[1])
	}
}

func TestCompilePopulationMappingReversedRouteRunsOnceBeforeWitnessTerminal(t *testing.T) {
	compiled := compilePopulationMappingRecipe(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-documents", MembershipDigest: "sha256:members", MemberCount: 3,
			ResourceType: "DocumentReference",
			Route:        []recipe.PopulationRouteStep{{ResourceType: "DocumentReference", Relationship: "subject_Specimen"}},
		},
	})
	if got := strings.Count(testAQLText(t, compiled.Command), "FOR population_root_edge_0 IN @@population_root_route_0_edge_collection"); got != 1 {
		t.Fatalf("mapping route traversal count = %d, want 1:\n%s", got, testAQLText(t, compiled.Command))
	}
	if strings.Index(testAQLText(t, compiled.Command), "population_source.id == population_member.id") > strings.Index(testAQLText(t, compiled.Command), "FOR __loom_physical_population_mapping_member") {
		t.Fatalf("mapping witness terminal preceded member matching:\n%s", testAQLText(t, compiled.Command))
	}
}

func TestCompilePopulationMappingRequiredNavigationRemainsBeforeWitnesses(t *testing.T) {
	compiled := compilePopulationMappingRecipe(t, recipe.Output{
		Name: "Patients", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Traversals: []recipe.Traversal{{
			Name: "subject_Patient", ToResourceType: "Condition", MatchMode: recipe.MatchRequired,
			Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "id"}}},
		}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 3,
			ResourceType: "Patient",
		},
	})
	if !strings.Contains(testAQLText(t, compiled.Command), "required_0_node_0") {
		t.Fatalf("mapping query lost required downstream elimination:\n%s", testAQLText(t, compiled.Command))
	}
	if strings.Index(testAQLText(t, compiled.Command), "required_0_node_0") > strings.Index(testAQLText(t, compiled.Command), "FOR __loom_physical_population_mapping_member") {
		t.Fatalf("required match was evaluated after witnesses:\n%s", testAQLText(t, compiled.Command))
	}
}

func TestCompilePopulationMappingPreservesInnerAndOuterFinalRowSemantics(t *testing.T) {
	for _, mode := range []string{"INNER", "OUTER"} {
		t.Run(mode, func(t *testing.T) {
			output := compilePopulationMappingOutput(t, recipe.Output{
				Name: "Specimens", RootResourceType: "Specimen", RowGrain: "expanded",
				Fields:   []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
				Expand:   &recipe.Expansion{From: recipe.Expression{Select: "root.extension[]"}, As: "item"},
				Identity: &recipe.Identity{Name: "row", Expr: recipe.Expression{Select: "root.id"}},
				Population: &recipe.PopulationConstraint{
					SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 2,
					ResourceType: "Specimen",
				},
			})
			if mode == "OUTER" {
				found := false
				for index := range output.Plan.Operations {
					if output.Plan.Operations[index].Kind != ir.PhysicalUnnestOp || output.Plan.Operations[index].Unnest == nil {
						continue
					}
					output.Plan.Operations[index].Unnest.EmptyPolicy = ir.PhysicalUnnestPreserveParent
					found = true
				}
				if !found {
					t.Fatal("compiled expansion has no UNNEST operation")
				}
			}
			compiled, err := CompilePopulationMappingOutput(ArangoTarget(), output, recipe.RuntimeBindings{Project: "fixture_program-project-a", SelectionProject: "fixture_program/project-a", DatasetGeneration: "generation-a", SelectionMembersCollection: "loom_explorer_selection_members"}, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			if mode == "INNER" && !strings.Contains(testAQLText(t, compiled.Command), "LET item =") {
				t.Fatalf("inner mapping query lost expansion:\n%s", testAQLText(t, compiled.Command))
			}
			if mode == "OUTER" && !strings.Contains(testAQLText(t, compiled.Command), "LET item =") {
				t.Fatalf("outer mapping query lost expansion:\n%s", testAQLText(t, compiled.Command))
			}
			if mode == "INNER" && !strings.Contains(testAQLText(t, compiled.Command), "LENGTH(__loom_physical_unnest_source_0) == 0 ? [] : RANGE") {
				t.Fatalf("inner mapping query did not preserve inner expansion:\n%s", testAQLText(t, compiled.Command))
			}
			if mode == "OUTER" && !strings.Contains(testAQLText(t, compiled.Command), "LENGTH(__loom_physical_unnest_source_0) == 0 ? [null]") {
				t.Fatalf("outer mapping query did not preserve outer expansion:\n%s", testAQLText(t, compiled.Command))
			}
			if strings.Index(testAQLText(t, compiled.Command), "FOR item IN") > strings.Index(testAQLText(t, compiled.Command), "FOR __loom_physical_population_mapping_member") {
				t.Fatalf("%s witness terminal ran before final expansion:\n%s", mode, testAQLText(t, compiled.Command))
			}
		})
	}
}

func containsBindValue(bindVars map[string]any, want string) bool {
	for _, value := range bindVars {
		if value == want {
			return true
		}
	}
	return false
}

func TestCompilePopulationMappingConstructionUsesFinalObjectIdentityAndRootMembers(t *testing.T) {
	filterValue := "Cohort"
	compiled := compilePopulationMappingRecipe(t, recipe.Output{
		Name: "NamedCohort", RootResourceType: "Observation", RowGrain: "groups",
		Fields: []recipe.Field{{Name: "specimen_id", ColumnID: "specimen_id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 2,
			ResourceType: "Observation",
		},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "specimen_id", Name: "specimen_id", Type: "string"}},
			Steps: []recipe.ConstructionStep{{
				ID: "filter_group_rows", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: recipe.ConstructionCohortGroupStageID}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
					ColumnID: "group_label", Operator: recipe.FilterEquals,
					Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &filterValue}},
				}},
				Outputs: []recipe.StageColumn{
					{ID: "group_id", Name: "group_id", Type: "string"},
					{ID: "group_label", Name: "group_label", Type: "string"},
					{ID: "group_ordinal", Name: "group_ordinal", Type: "integer"},
					{ID: "members", Name: "members", Type: "array"},
					{ID: "specimen_id", Name: "specimen_id", Type: "array"},
				},
			}},
		},
		GroupRows: &recipe.GroupRows{
			RevisionID: "grouprev_cohort_row_lineage", UnassignedMemberPolicy: "EXCLUDE",
			RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "specimen_id", Policy: recipe.ConstructionRowValueAll}},
		},
	})
	wants := []string{
		"INTO __loom_physical_population_member_ids = population_member.id",
		"LET __loom_population_members_value = SORTED_UNIQUE(__loom_physical_population_member_ids)",
		"FOR __loom_physical_construction_population_mapping_source_row IN __loom_construction_source_projection",
		"FILTER __loom_physical_construction_population_mapping_source_row[@__loom_physical_construction_population_mapping_source_root_key] IN __loom_physical_construction_population_mapping_root_keys",
		"FOR __loom_physical_construction_population_mapping_source_member IN (__loom_physical_construction_population_mapping_source_row[@__loom_physical_construction_population_mapping_source_members] == null ? [] : __loom_physical_construction_population_mapping_source_row[@__loom_physical_construction_population_mapping_source_members])",
		"COLLECT __loom_physical_construction_population_mapping_selected_member = __loom_physical_construction_population_mapping_source_member, __loom_physical_construction_population_mapping_selected_identity = __loom_construction_final_row[@__loom_physical_construction_population_mapping_final_row_identity]",
		"@__loom_physical_construction_population_mapping_identity_name]: [__loom_physical_construction_population_mapping_selected_identity]",
	}
	for _, want := range wants {
		if !strings.Contains(testAQLText(t, compiled.Command), want) {
			t.Fatalf("construction population mapping query is missing %q:\n%s", want, testAQLText(t, compiled.Command))
		}
	}
	if compiled.RowIdentity == nil || len(compiled.RowIdentity.Fields) != 1 || compiled.RowIdentity.Fields[0] != "__loom_row_id" ||
		compiled.IdentityPartsColumn != ir.PhysicalPopulationMappingIdentityPartsField || compiled.ExplicitIdentityColumn != "" {
		t.Fatalf("construction population mapping identity columns = (%#v, %q, %q)", compiled.RowIdentity, compiled.IdentityPartsColumn, compiled.ExplicitIdentityColumn)
	}
	if !containsBindValue(testAQLBinds(t, compiled.Command), ir.PhysicalPopulationMappingMembersColumn) {
		t.Fatal("construction population mapping source IDs are not projected through the private source mapping column")
	}
}

func TestCompilePopulationMappingOrdinaryGroupRelatedUsesTypedFinalIdentity(t *testing.T) {
	compiledOutput := compilePopulationMappingOutput(t, populationMappingGroupRelatedOutput())
	if compiledOutput.RowIdentity == nil || !reflect.DeepEqual(compiledOutput.RowIdentity.Fields, []string{"project", "_key"}) {
		t.Fatalf("ordinary construction's public row identity changed: %#v", compiledOutput.RowIdentity)
	}
	sequence := compiledOutput.Plan.StageSequence
	if sequence == nil || sequence.FinalStageID != "summary" || sequence.FinalRowIdentity != "__loom_row_id" {
		t.Fatalf("GROUP→RELATED_SOURCE final identity = %#v", sequence)
	}

	mapping, err := CompilePopulationMappingOutput(ArangoTarget(), compiledOutput, recipe.RuntimeBindings{
		Project: "fixture_project", SelectionProject: "fixture/project", DatasetGeneration: "generation-a",
		SelectionMembersCollection: "loom_explorer_selection_members",
	}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile GROUP→RELATED_SOURCE population mapping: %v", err)
	}
	if mapping.RowIdentity == nil || mapping.RowIdentity.Grain != compiledOutput.RowIdentity.Grain || !reflect.DeepEqual(mapping.RowIdentity.Fields, []string{"__loom_row_id"}) {
		t.Fatalf("mapping identity = %#v, want compiler-proven final identity", mapping.RowIdentity)
	}
	if mapping.IdentityPartsColumn != ir.PhysicalPopulationMappingIdentityPartsField || mapping.ExplicitIdentityColumn != "" {
		t.Fatalf("mapping identity columns = (%q, %q)", mapping.IdentityPartsColumn, mapping.ExplicitIdentityColumn)
	}
	if !strings.Contains(testAQLText(t, mapping.Command), "COLLECT __loom_physical_construction_population_mapping_selected_member") ||
		!strings.Contains(testAQLText(t, mapping.Command), "__loom_construction_final_row[@__loom_physical_construction_population_mapping_final_row_identity]") {
		t.Fatalf("mapping query did not bind selected members to the exact final row identity:\n%s", testAQLText(t, mapping.Command))
	}
}

func TestCompilePopulationMappingConstructionRejectsUnrelatedIdentityFallback(t *testing.T) {
	compiledOutput := compilePopulationMappingOutput(t, populationMappingGroupRelatedOutput())
	compiledOutput.RowIdentity = compiledOutput.RowIdentity.Clone()
	compiledOutput.RowIdentity.Fields = []string{"project", "unrelated_key"}
	if _, err := CompilePopulationMappingOutput(ArangoTarget(), compiledOutput, recipe.RuntimeBindings{
		Project: "fixture_project", SelectionProject: "fixture/project", DatasetGeneration: "generation-a",
		SelectionMembersCollection: "loom_explorer_selection_members",
	}, ir.DefaultPhysicalOptimizationPolicy()); err == nil || !strings.Contains(err.Error(), "exact final row identity") {
		t.Fatalf("unrelated identity fallback was accepted: %v", err)
	}
}

func TestCompilePopulationMappingConstructionRejectsUnprovenFinalIdentitySchema(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ir.PhysicalStageColumn)
	}{
		{name: "missing", mutate: nil},
		{name: "noninternal", mutate: func(column *ir.PhysicalStageColumn) { column.Internal = false }},
		{name: "identity_flag_false", mutate: func(column *ir.PhysicalStageColumn) { column.Identity = false }},
		{name: "optional", mutate: func(column *ir.PhysicalStageColumn) { column.Cardinality = "optional_one" }},
		{name: "many", mutate: func(column *ir.PhysicalStageColumn) { column.Cardinality = "many" }},
		{name: "nonstr", mutate: func(column *ir.PhysicalStageColumn) { column.Kind = "integer" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			compiledOutput := compilePopulationMappingOutput(t, populationMappingGroupRelatedOutput())
			columns := compiledOutput.Plan.StageSequence.FinalColumns
			identityIndex := -1
			for index := range columns {
				if columns[index].Name == compiledOutput.Plan.StageSequence.FinalRowIdentity {
					identityIndex = index
					break
				}
			}
			if test.name != "missing" && identityIndex < 0 {
				t.Fatalf("fixture final identity %q is missing", compiledOutput.Plan.StageSequence.FinalRowIdentity)
			}
			if test.name == "missing" {
				filtered := make([]ir.PhysicalStageColumn, 0, len(columns)-1)
				for _, column := range columns {
					if column.Name != compiledOutput.Plan.StageSequence.FinalRowIdentity {
						filtered = append(filtered, column)
					}
				}
				compiledOutput.Plan.StageSequence.FinalColumns = filtered
			} else {
				test.mutate(&compiledOutput.Plan.StageSequence.FinalColumns[identityIndex])
			}
			if _, err := CompilePopulationMappingOutput(ArangoTarget(), compiledOutput, populationMappingTestBindings(), ir.DefaultPhysicalOptimizationPolicy()); err == nil {
				t.Fatalf("population mapping accepted unproven final identity schema: %#v", compiledOutput.Plan.StageSequence.FinalColumns)
			}
		})
	}
}

func TestCompilePopulationMappingConstructionRejectsWrongDefaultIdentityFieldCounts(t *testing.T) {
	tests := []struct {
		name   string
		fields []string
	}{
		{name: "shorter", fields: []string{"project"}},
		{name: "longer", fields: []string{"project", "_key", "extra"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			compiledOutput := compilePopulationMappingOutput(t, populationMappingGroupRelatedOutput())
			compiledOutput.RowIdentity = compiledOutput.RowIdentity.Clone()
			compiledOutput.RowIdentity.Fields = append([]string(nil), test.fields...)
			if _, err := CompilePopulationMappingOutput(ArangoTarget(), compiledOutput, populationMappingTestBindings(), ir.DefaultPhysicalOptimizationPolicy()); err == nil || !strings.Contains(err.Error(), "exact final row identity") {
				t.Fatalf("identity fields %v were accepted or returned the wrong error: %v", test.fields, err)
			}
		})
	}
}

func populationMappingGroupRelatedOutput() recipe.Output {
	output := groupedRelatedSummaryOutput("COUNT", "")
	output.Population = &recipe.PopulationConstraint{
		SelectionRevisionID: "selection-group-related", MembershipDigest: "sha256:group-related-members", MemberCount: 2,
		ResourceType: "Patient",
	}
	return output
}

func populationMappingTestBindings() recipe.RuntimeBindings {
	return recipe.RuntimeBindings{
		Project: "fixture_project", SelectionProject: "fixture/project", DatasetGeneration: "generation-a",
		SelectionMembersCollection: "loom_explorer_selection_members",
	}
}

func compilePopulationMappingRecipe(t *testing.T, output recipe.Output) CompiledPopulationMappingQuery {
	t.Helper()
	compiled := compilePopulationMappingOutput(t, output)
	mapping, err := CompilePopulationMappingOutput(ArangoTarget(), compiled, recipe.RuntimeBindings{Project: "fixture_program-project-a", SelectionProject: "fixture_program/project-a", DatasetGeneration: "generation-a", SelectionMembersCollection: "loom_explorer_selection_members"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return mapping
}

func compilePopulationMappingOutput(t *testing.T, output recipe.Output) lower.CompiledRecipeOutput {
	t.Helper()
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "population-mapping-test",
		TranslationVersion:  "population-mapping-test",
		Outputs:             []recipe.Output{output},
	}
	bindings := recipe.RuntimeBindings{Project: "fixture_program-project-a", SelectionProject: "fixture_program/project-a", DatasetGeneration: "generation-a", SelectionMembersCollection: "loom_explorer_selection_members"}
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
	return compiled.Outputs[0]
}

// ---------------------------------------------------------------------------
// pg-sql-22: the PostgreSQL dispatch arm.
// ---------------------------------------------------------------------------

func populationMappingPostgreSQLTarget(t *testing.T) Target {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding("loom")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	target, err := PostgreSQLTarget(binding)
	if err != nil {
		t.Fatalf("PostgreSQLTarget: %v", err)
	}
	return target
}

func populationMappingPostgreSQLSQL(t *testing.T, compiled CompiledPopulationMappingQuery) (string, []command.PostgreSQLParameter) {
	t.Helper()
	if compiled.Command.Backend() != command.CompiledBackendPostgreSQL {
		t.Fatalf("mapping command backend = %q, want postgresql", string(compiled.Command.Backend()))
	}
	sql, args, err := compiled.Command.PostgreSQLParameters()
	if err != nil {
		t.Fatalf("PostgreSQLParameters: %v", err)
	}
	if sql == "" {
		t.Fatal("mapping statement SQL is empty")
	}
	return sql, args
}

func TestCompilePopulationMappingPostgreSQLDefaultIdentityWitnesses(t *testing.T) {
	output := recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 3,
			ResourceType: "Specimen",
		},
	}
	compiledOutput := compilePopulationMappingOutput(t, output)
	bindings := recipe.RuntimeBindings{Project: "fixture_program-project-a", SelectionProject: "fixture_program/project-a", DatasetGeneration: "generation-a", SelectionMembersCollection: "loom_explorer_selection_members"}
	postgresQuery, err := CompilePopulationMappingOutput(populationMappingPostgreSQLTarget(t), compiledOutput, bindings, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("postgresql population mapping compile: %v", err)
	}
	sql, args := populationMappingPostgreSQLSQL(t, postgresQuery)
	for _, want := range []string{
		`CROSS JOIN LATERAL jsonb_array_elements_text(`,
		`jsonb_typeof(`,
		`'[]'::jsonb`,
		`jsonb_build_array(`,
		`ARRAY_AGG(`,
		`COLLATE "C"`,
		`"loom"."selection_members"`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("mapping SQL is missing %q:\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "@") {
		t.Fatalf("mapping SQL carries AQL bind syntax:\n%s", sql)
	}
	if len(args) == 0 {
		t.Fatal("mapping statement is not parameterized")
	}
	// Every value stays a $n placeholder: no quoted plan string leaks in.
	if strings.Contains(sql, "fixture_program") {
		t.Fatalf("mapping SQL inlines bind values:\n%s", sql)
	}
	// The AQL control proves the dialect-neutral proof metadata is shared:
	// same output, both targets, identical metadata contract.
	aqlQuery, err := CompilePopulationMappingOutput(ArangoTarget(), compiledOutput, bindings, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("aql population mapping control: %v", err)
	}
	if postgresQuery.MemberColumn != aqlQuery.MemberColumn ||
		postgresQuery.IdentityPartsColumn != aqlQuery.IdentityPartsColumn ||
		postgresQuery.ExplicitIdentityColumn != aqlQuery.ExplicitIdentityColumn ||
		!reflect.DeepEqual(postgresQuery.RowIdentity, aqlQuery.RowIdentity) ||
		!reflect.DeepEqual(postgresQuery.Diagnostics, aqlQuery.Diagnostics) {
		t.Fatalf("postgresql proof metadata drifted from the AQL control:\n pg: %#v\n aql: %#v", postgresQuery, aqlQuery)
	}
	if postgresQuery.IdentityPartsColumn != ir.PhysicalPopulationMappingIdentityPartsField || postgresQuery.ExplicitIdentityColumn != "" {
		t.Fatalf("default mapping identity columns = (%q, %q)", postgresQuery.IdentityPartsColumn, postgresQuery.ExplicitIdentityColumn)
	}
	// Determinism: one plan and bindings render byte-identical SQL and
	// value-identical ordered arguments.
	second, err := CompilePopulationMappingOutput(populationMappingPostgreSQLTarget(t), compiledOutput, bindings, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("second postgresql compile: %v", err)
	}
	sql2, args2 := populationMappingPostgreSQLSQL(t, second)
	if sql2 != sql || !reflect.DeepEqual(args2, args) {
		t.Fatal("population mapping rendering is not deterministic")
	}
}

func TestCompilePopulationMappingPostgreSQLExplicitIdentity(t *testing.T) {
	compiled := compilePopulationMappingRecipePostgreSQL(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields:   []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Identity: &recipe.Identity{Name: "row", Expr: recipe.Expression{Select: "root.id"}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 3,
			ResourceType: "Specimen",
		},
	})
	sql, _ := populationMappingPostgreSQLSQL(t, compiled)
	if compiled.IdentityPartsColumn != "" || compiled.ExplicitIdentityColumn != ir.PhysicalPopulationMappingExplicitIdentityField {
		t.Fatalf("explicit mapping identity columns = (%q, %q)", compiled.IdentityPartsColumn, compiled.ExplicitIdentityColumn)
	}
	// The compiled explicit identity expression is preserved through the
	// registered selector reading, not re-spelled as a parts array.
	if !strings.Contains(sql, "#>") {
		t.Fatalf("explicit identity did not render through the registered payload path:\n%s", sql)
	}
}

// populationMappingSummaryOutput is a pure registered composition (the
// summary GROUP) under a population constraint; the keyed variant drives
// the contributor pool's GROUP BY/join path. RELATED_* stages stay outside
// the registered composition surface (their own slice), so they are not
// used here.
func populationMappingSummaryOutput(keyed bool) recipe.Output {
	output := constructionSummaryOutput()
	if keyed {
		group := output.Construction.Steps[0].Operation.Group
		group.Keys = []recipe.ConstructionGroupKey{{InputColumnID: "status_id", OutputColumnID: "status_key_id"}}
		output.Construction.Steps[0].Outputs = append([]recipe.StageColumn{{ID: "status_key_id", Name: "status_key"}}, output.Construction.Steps[0].Outputs...)
	}
	output.Population = &recipe.PopulationConstraint{
		SelectionRevisionID: "selection-summary", MembershipDigest: "sha256:summary-members", MemberCount: 2,
		ResourceType: "Observation",
	}
	return output
}

func TestCompilePopulationMappingPostgreSQLConstructionTerminal(t *testing.T) {
	for _, keyed := range []bool{false, true} {
		name := "grand-total"
		if keyed {
			name = "keyed"
		}
		t.Run(name, func(t *testing.T) {
			compiledOutput := compilePopulationMappingOutput(t, populationMappingSummaryOutput(keyed))
			postgresQuery, err := CompilePopulationMappingOutput(populationMappingPostgreSQLTarget(t), compiledOutput, populationMappingTestBindings(), ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatalf("postgresql construction mapping compile: %v", err)
			}
			sql, _ := populationMappingPostgreSQLSQL(t, postgresQuery)
			// The contributor set is a renderer-owned pool relation joined
			// to the group aggregate, and the terminal expands it into the
			// source rows on the materialized "_key" text, then the member
			// array, then de-duplicates on (identity, member). The plan-
			// internal contributor column name never reaches SQL text.
			for _, want := range []string{
				"WITH ",
				`"loom"."selection_members"`,
				"rcpool AS (",
				"ARRAY_AGG(DISTINCT",
				"INNER JOIN __loom_src AS ps ON",
				"CROSS JOIN LATERAL jsonb_array_elements_text(",
				`GROUP BY s."`,
				`COLLATE "C" ASC, mn0.member COLLATE "C" ASC`,
			} {
				if !strings.Contains(sql, want) {
					t.Fatalf("construction mapping SQL is missing %q:\n%s", want, sql)
				}
			}
			if strings.Contains(sql, "@") {
				t.Fatalf("construction mapping SQL carries AQL bind syntax:\n%s", sql)
			}
			if postgresQuery.IdentityPartsColumn != ir.PhysicalPopulationMappingIdentityPartsField {
				t.Fatalf("construction mapping identity column = %q", postgresQuery.IdentityPartsColumn)
			}
			aqlQuery, err := CompilePopulationMappingOutput(ArangoTarget(), compiledOutput, populationMappingTestBindings(), ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatalf("aql construction control: %v", err)
			}
			if !reflect.DeepEqual(postgresQuery.RowIdentity, aqlQuery.RowIdentity) || !reflect.DeepEqual(postgresQuery.Diagnostics, aqlQuery.Diagnostics) {
				t.Fatalf("construction proof metadata drifted from the AQL control")
			}
		})
	}
}

func TestCompilePopulationMappingPostgreSQLRefusesMissingRowIdentity(t *testing.T) {
	compiledOutput := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 3,
			ResourceType: "Specimen",
		},
	})
	compiledOutput.RowIdentity = nil
	if _, err := CompilePopulationMappingOutput(populationMappingPostgreSQLTarget(t), compiledOutput, populationMappingTestBindings(), ir.DefaultPhysicalOptimizationPolicy()); err == nil || !strings.Contains(err.Error(), "requires a row identity") {
		t.Fatalf("missing row identity error = %v", err)
	}
}

func compilePopulationMappingRecipePostgreSQL(t *testing.T, output recipe.Output) CompiledPopulationMappingQuery {
	t.Helper()
	compiled := compilePopulationMappingOutput(t, output)
	mapping, err := CompilePopulationMappingOutput(populationMappingPostgreSQLTarget(t), compiled, recipe.RuntimeBindings{Project: "fixture_program-project-a", SelectionProject: "fixture_program/project-a", DatasetGeneration: "generation-a", SelectionMembersCollection: "loom_explorer_selection_members"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return mapping
}
