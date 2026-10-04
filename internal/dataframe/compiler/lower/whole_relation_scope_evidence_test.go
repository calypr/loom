package lower

import (
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestCompiledWholeRelationEvidenceCoversCompleteTypedSources(t *testing.T) {
	outputs := []struct {
		name   string
		output recipe.Output
	}{
		{name: "construction Group", output: wholeRelationGroupOutput()},
		{name: "table Pivot", output: wholeRelationTablePivotOutput()},
		{name: "CodedGroup", output: codedGroupOutput("Observation", "component[].code.coding[]", recipe.ConstructionGroupMissingKeyGroup)},
		{name: "CohortGroup", output: wholeRelationCohortOutput()},
		{name: "GROUP_ROWS source", output: recipe.Output{
			Name: "explicit_groups", RootResourceType: "Patient", RowGrain: "groups",
			GroupRows: &recipe.GroupRows{RevisionID: "grouprev_1", UnassignedMemberPolicy: "EXCLUDE"},
		}},
	}

	for _, test := range outputs {
		t.Run(test.name, func(t *testing.T) {
			output := compileWholeRelationTestOutput(t, test.output, recipe.RuntimeBindings{
				Project: "project", SelectionProject: "project", DatasetGeneration: "generation",
				AuthScopeMode: authscope.ReadScopeUnrestricted,
			})
			if output.ScopeEvidence == nil || output.ScopeEvidenceIdentity == nil {
				t.Fatal("compiled grouped output has no compiler-issued whole-relation scope evidence")
			}
			identity := *output.ScopeEvidenceIdentity
			if identity.OutputID != output.Name || identity.Project == "" || identity.DatasetGeneration == "" || identity.SchemaDigest == "" {
				t.Fatalf("evidence identity = %#v, missing exact compiled output identity", identity)
			}
			if !output.ScopeEvidence.Matches(output.Plan, identity) || output.ScopeEvidence.Digest() == "" {
				t.Fatal("whole-relation evidence does not match its exact finalized plan and schema identity")
			}
			if output.ScopeEvidence.Matches(output.Plan, withChangedWholeRelationIdentity(identity, func(changed *ir.WholeRelationScopeIdentity) {
				changed.OutputID += "-other"
			})) {
				t.Fatal("whole-relation evidence matched a different output ID")
			}
			if output.ScopeEvidence.Matches(output.Plan, withChangedWholeRelationIdentity(identity, func(changed *ir.WholeRelationScopeIdentity) {
				changed.SchemaDigest += "-other"
			})) {
				t.Fatal("whole-relation evidence matched a different finalized schema")
			}
			mutated := ir.ClonePhysicalPlan(output.Plan)
			mutated.BindVars["dataset_generation"] = "other-generation"
			if output.ScopeEvidence.Matches(mutated, identity) {
				t.Fatal("whole-relation evidence matched a different physical plan generation")
			}
		})
	}

	ordinary := compileDerivedTestOutput(t, recipe.Output{
		Name: "ordinary_rows", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
	})
	if ordinary.ScopeEvidence == nil || ordinary.ScopeEvidenceIdentity == nil || !ordinary.ScopeEvidence.Matches(ordinary.Plan, *ordinary.ScopeEvidenceIdentity) {
		t.Fatal("complete ordinary row output lacks evidence for its exact authorized AQL source relation")
	}
}

func TestWholeRelationEvidenceRejectsUnscopedOrNarrowedGroupedPlans(t *testing.T) {
	compiled := compileWholeRelationTestOutput(t, wholeRelationGroupOutput(), recipe.RuntimeBindings{
		Project: "project", SelectionProject: "project", DatasetGeneration: "generation", AuthScopeMode: authscope.ReadScopeUnrestricted,
	})
	if compiled.ScopeEvidence == nil || compiled.ScopeEvidenceIdentity == nil {
		t.Fatal("compiled Group has no whole-relation scope evidence")
	}
	identity := *compiled.ScopeEvidenceIdentity

	missingProject := ir.ClonePhysicalPlan(compiled.Plan)
	rootVariable := ""
	for _, operation := range missingProject.Operations {
		if operation.RootScan != nil {
			rootVariable = operation.RootScan.Variable
			break
		}
	}
	if rootVariable == "" || !removePhysicalScopeFilter(&missingProject.Operations, rootVariable, "project") {
		t.Fatal("test setup could not remove the exact root project guard")
	}
	if _, err := ir.NewWholeRelationScopeEvidence(missingProject, identity); err == nil {
		t.Fatal("whole-relation evidence accepted a grouped source without its project guard")
	}

	wrongScope := ir.ClonePhysicalPlan(compiled.Plan)
	wrongScope.BindVars["auth_resource_paths_unrestricted"] = false
	if _, err := ir.NewWholeRelationScopeEvidence(wrongScope, identity); err == nil {
		t.Fatal("whole-relation evidence accepted a plan with a different authorization mode")
	}

	filteredOutput := compileDerivedTestOutput(t, wholeRelationFilteredGroupOutput())
	if filteredOutput.ScopeEvidence == nil || filteredOutput.ScopeEvidenceIdentity == nil || !filteredOutput.ScopeEvidence.Matches(filteredOutput.Plan, *filteredOutput.ScopeEvidenceIdentity) {
		t.Fatal("a complete post-group filter plan should retain evidence for its full scoped source relation")
	}

	cohort := compileWholeRelationTestOutput(t, wholeRelationCohortOutput(), recipe.RuntimeBindings{
		Project: "project", SelectionProject: "project", DatasetGeneration: "generation", AuthScopeMode: authscope.ReadScopeUnrestricted,
	})
	cohortPlan := ir.ClonePhysicalPlan(cohort.Plan)
	var cohortRows *ir.PhysicalGroupRows
	for index := range cohortPlan.StageSequence.Stages {
		if cohortPlan.StageSequence.Stages[index].CohortGroup != nil {
			cohortRows = &cohortPlan.StageSequence.Stages[index].CohortGroup.Rows
			break
		}
	}
	if cohortRows == nil {
		t.Fatal("test setup could not find typed CohortGroup source")
	}
	cohortPlan.BindVars[cohortRows.AuthResourcePathsBindKey] = []string{"/wrong/path"}
	if _, err := ir.NewWholeRelationScopeEvidence(cohortPlan, *cohort.ScopeEvidenceIdentity); err == nil {
		t.Fatal("whole-relation evidence accepted a CohortGroup custom read outside the exact authorization path set")
	}

	coded := compileWholeRelationTestOutput(t, codedGroupOutput("Observation", "component[].code.coding[]", recipe.ConstructionGroupMissingKeyGroup), recipe.RuntimeBindings{
		Project: "project", SelectionProject: "project", DatasetGeneration: "generation", AuthScopeMode: authscope.ReadScopeUnrestricted,
	})
	codedPlan := ir.ClonePhysicalPlan(coded.Plan)
	codedPlan.BindVars[codedPlan.StageSequence.Stages[0].CodedGroup.RootCollectionBindKey] = "Procedure"
	if _, err := ir.NewWholeRelationScopeEvidence(codedPlan, *coded.ScopeEvidenceIdentity); err == nil {
		t.Fatal("whole-relation evidence accepted a CodedGroup custom lookup substituted to another source collection")
	}
}

func withChangedWholeRelationIdentity(identity ir.WholeRelationScopeIdentity, change func(*ir.WholeRelationScopeIdentity)) ir.WholeRelationScopeIdentity {
	change(&identity)
	return identity
}

func compileWholeRelationTestOutput(t *testing.T, output recipe.Output, bindings recipe.RuntimeBindings) CompiledRecipeOutput {
	t.Helper()
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "whole-relation-evidence", TranslationVersion: "test", Outputs: []recipe.Output{output}}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return compiled.Outputs[0]
}

func wholeRelationGroupOutput() recipe.Output {
	return recipe.Output{
		Name: "grouped", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "gender", ColumnID: "gender_id", Expr: recipe.Expression{Select: "root.gender"}}},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{{ID: "gender_id", Name: "gender", Type: "string"}},
			Steps: []recipe.ConstructionStep{{
				ID: "group_gender", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "group_gender",
					Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "gender_id", OutputColumnID: "group_gender_id"}},
					Aggregates:     []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "row_count_id"}},
				}},
				Outputs: []recipe.StageColumn{{ID: "group_gender_id", Name: "gender", Type: "string"}, {ID: "row_count_id", Name: "rows", Type: "integer"}},
			}},
		},
	}
}

func wholeRelationTablePivotOutput() recipe.Output {
	zero := int64(0)
	return recipe.Output{
		Name: "pivoted", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{
			{Name: "gender", Expr: recipe.Expression{Select: "gender"}},
			{Name: "category", Expr: recipe.Expression{Select: "multipleBirthInteger"}},
			{Name: "value", Expr: recipe.Expression{Select: "multipleBirthInteger"}},
		},
		TableReshape: &recipe.TableReshape{Kind: recipe.TableReshapeGroupedPivot, GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "pivot", GroupKeys: []string{"gender"}, CategoryColumn: "category", ValueColumn: "value",
			Categories:      []recipe.GroupedPivotCategory{{Key: recipe.TableScalar{Kind: recipe.TableScalarInteger, Integer: &zero}, Output: "zero", Label: "Zero"}},
			DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence,
		}},
	}
}

func wholeRelationCohortOutput() recipe.Output {
	return recipe.Output{
		Name: "cohort_with_related_count", RootResourceType: "Patient", RowGrain: "groups",
		Fields: []recipe.Field{{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id"}},
			Steps: []recipe.ConstructionStep{{
				ID: "related_count", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: recipe.ConstructionCohortGroupStageID}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: &recipe.ConstructionRelatedSource{
					AnchorColumnID: "__loom_row_id", ChoiceID: "patient_observation", SourceOccurrenceID: "observation_node",
					Source:            recipe.ConstructionRelatedFieldSource{CandidateID: "observation_id", NodeID: "observation_node", ResourceType: "Observation", Path: "Observation.id", Cardinality: "required_one", LogicalType: "string"},
					Route:             []recipe.ConstructionRelatedRouteStep{{EdgeID: "patient_observation", FromNodeID: "patient_node", ToNodeID: "observation_node", FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL"}},
					ContributorPolicy: "ALL_MATCHES", Form: "COUNT", OutputColumnID: "observation_count",
				}},
				Outputs: []recipe.StageColumn{{ID: "group_id", Name: "group_id"}, {ID: "group_label", Name: "group_label"}, {ID: "group_ordinal", Name: "group_ordinal"}, {ID: "members", Name: "members"}, {ID: "observation_count", Name: "observation_count"}},
			}},
		},
		GroupRows: &recipe.GroupRows{RevisionID: "grouprev_1", UnassignedMemberPolicy: "EXCLUDE"},
	}
}

func TestWholeRelationEvidenceUsesExactRestrictedScope(t *testing.T) {
	output := wholeRelationGroupOutput()
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "scope evidence", TranslationVersion: "test", Outputs: []recipe.Output{output}}
	bindings := recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation", AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/a", "/b"}}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	evidence, identity := compiled.Outputs[0].ScopeEvidence, compiled.Outputs[0].ScopeEvidenceIdentity
	if evidence == nil || identity == nil || identity.AuthScopeMode != "restricted" || len(identity.AuthResourcePaths) != 2 {
		t.Fatalf("restricted whole-relation evidence = %#v identity = %#v", evidence, identity)
	}
	if !evidence.Matches(compiled.Outputs[0].Plan, *identity) {
		t.Fatal("restricted evidence does not match its exact path set")
	}
}

func wholeRelationFilteredGroupOutput() recipe.Output {
	return recipe.Output{
		Name: "filtered_group", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "gender", ColumnID: "gender_id", Expr: recipe.Expression{Select: "root.gender"}}},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{{ID: "gender_id", Name: "gender", Type: "string"}},
			Steps: []recipe.ConstructionStep{
				{
					ID: "group_gender", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
						ConstructionID: "group_gender",
						Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "gender_id", OutputColumnID: "group_gender_id"}},
						Aggregates:     []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "row_count_id"}},
					}},
					Outputs: []recipe.StageColumn{{ID: "group_gender_id", Name: "gender", Type: "string"}, {ID: "row_count_id", Name: "rows", Type: "integer"}},
				},
				{
					ID: "keep_nonempty_groups", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "group_gender"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "row_count_id", Operator: recipe.FilterExists,
					}},
					Outputs: []recipe.StageColumn{{ID: "group_gender_id", Name: "gender", Type: "string"}, {ID: "row_count_id", Name: "rows", Type: "integer"}},
				},
			},
		},
	}
}
