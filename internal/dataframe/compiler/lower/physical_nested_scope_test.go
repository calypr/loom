package lower

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestValidateGenericPhysicalPlanScopeCoversLoweredNestedPlans(t *testing.T) {
	t.Run("graph path extension scope", func(t *testing.T) {
		plan, err := BuildGraphPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []semantic.SemanticNode{{
				Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient",
				MatchMode: "REQUIRED",
			}},
		}}, semantic.ExecutionContext{Project: "project"}, 100, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("BuildGraphPhysicalPlan() error = %v", err)
		}
		var pathExtend *ir.PhysicalPathExtend
		for index := range plan.Operations {
			if plan.Operations[index].PathExtend != nil {
				pathExtend = plan.Operations[index].PathExtend
				break
			}
		}
		if pathExtend == nil {
			t.Fatal("compiled graph plan has no typed path extension")
		}
		if err := ir.ValidateGenericPhysicalPlanScope(plan); err != nil {
			t.Fatalf("compiled graph path scope: %v", err)
		}
		mutated := ir.ClonePhysicalPlan(plan)
		for index := range mutated.Operations {
			if extend := mutated.Operations[index].PathExtend; extend != nil {
				target := extend.Traversal.TargetVariable
				requireScopeMutation(t, setPhysicalScopeFilterBind(&extend.Scope, target, "project", "dataset_generation"))
				break
			}
		}
		if err := ir.ValidateGenericPhysicalPlanScope(mutated); err == nil {
			t.Fatal("scope validator accepted an embedded path traversal without the exact project guard")
		}
	})

	t.Run("row expansion owner route scope", func(t *testing.T) {
		compiled := compileExpansionRecipeOutput(t, recipe.Output{
			Name: "expanded_specimens", RootResourceType: "Patient", RootOccurrenceID: "patient-root", RowGrain: "expanded",
			TraversalColumnNaming: recipe.TraversalColumnNamingAlias,
			Fields:                []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
			Expand: &recipe.Expansion{
				OwnerOccurrenceID: "specimen-owner", From: recipe.Expression{Select: "specimen.identifier[]"},
				As: "item", Ordinality: "position", EmptyPolicy: recipe.ExpansionError,
			},
			Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
			Traversals: []recipe.Traversal{{
				Name: "subject_Patient", OccurrenceID: "specimen-owner", Alias: "specimen", ToResourceType: "Specimen",
			}},
		})
		if err := ir.ValidateGenericPhysicalPlanScope(compiled.Plan); err != nil {
			t.Fatalf("compiled row expansion owner route scope: %v", err)
		}
		mutated := ir.ClonePhysicalPlan(compiled.Plan)
		unnest := findRecipeUnnest(t, mutated)
		if len(unnest.Owner.Route) != 1 {
			t.Fatalf("compiled owner route steps = %d, want 1", len(unnest.Owner.Route))
		}
		requireScopeMutation(t, removePhysicalScopeFilter(&unnest.Owner.Route[0].Scope, unnest.Owner.Route[0].Traversal.TargetVariable, "project"))
		if err := ir.ValidateGenericPhysicalPlanScope(mutated); err == nil {
			t.Fatal("scope validator accepted an owner-route traversal without its project guard")
		}
	})

	t.Run("correlated optional child", func(t *testing.T) {
		plan, err := BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []semantic.SemanticNode{{
				Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
				Fields: []semantic.SemanticField{{
					Name: "specimen_id", FieldRef: "Specimen.id",
					Expr:       semantic.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "id"})},
					Projection: spec.ProjectionFirst,
				}},
			}},
		}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("BuildGenericPhysicalPlanWithPolicy() error = %v", err)
		}
		set := findPhysicalSetForScopeTest(t, plan)
		targetVariable := firstTraversalTargetForScopeTest(t, plan.Operations[set].Set.Subplan.Operations)
		if err := ir.ValidateGenericPhysicalPlanScope(plan); err != nil {
			t.Fatalf("compiled correlated plan scope: %v", err)
		}

		cases := []struct {
			name   string
			mutate func(*testing.T, *ir.PhysicalSubplan)
		}{
			{"missing project guard", func(t *testing.T, subplan *ir.PhysicalSubplan) {
				requireScopeMutation(t, removePhysicalScopeFilter(&subplan.Operations, targetVariable, "project"))
			}},
			{"misbound generation guard", func(t *testing.T, subplan *ir.PhysicalSubplan) {
				requireScopeMutation(t, setPhysicalScopeFilterBind(&subplan.Operations, targetVariable, "dataset_generation", "project"))
			}},
			{"missing auth path input", func(t *testing.T, subplan *ir.PhysicalSubplan) {
				requireScopeMutation(t, removePhysicalScopeAuthPath(&subplan.Operations, targetVariable))
			}},
			{"misbound auth equality", func(t *testing.T, subplan *ir.PhysicalSubplan) {
				requireScopeMutation(t, setPhysicalScopeAuthEqualityBind(&subplan.Operations, targetVariable, "project"))
			}},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				mutated := ir.ClonePhysicalPlan(plan)
				test.mutate(t, &mutated.Operations[set].Set.Subplan)
				if err := ir.ValidateGenericPhysicalPlanScope(mutated); err == nil {
					t.Fatal("scope validator accepted an unscoped nested resource")
				}
			})
		}
	})

	t.Run("population source", func(t *testing.T) {
		_, compiled := compilePopulationRecipe(t, recipe.Output{
			Name: "population_scope", RootResourceType: "Specimen", RowGrain: "specimen",
			Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
			Population: &recipe.PopulationConstraint{
				SelectionRevisionID: "selection", MembershipDigest: "sha256:population", MemberCount: 2,
				ResourceType: "Specimen",
			},
		})
		if err := ir.ValidateGenericPhysicalPlanScope(compiled.Plan); err != nil {
			t.Fatalf("compiled population plan scope: %v", err)
		}
		root := findPopulationRootForScopeTest(t, compiled.Plan)
		if len(root.Population.ResourceOperations) == 0 {
			t.Fatal("compiled population root omitted its nested resource operations")
		}
		mutated := ir.ClonePhysicalPlan(compiled.Plan)
		root = findPopulationRootForScopeTest(t, mutated)
		requireScopeMutation(t, removePhysicalScopeFilter(&root.Population.ResourceOperations, "population_source", "project"))
		if err := ir.ValidateGenericPhysicalPlanScope(mutated); err == nil {
			t.Fatal("scope validator accepted a population source without its project guard")
		}
	})

	t.Run("related expansion route", func(t *testing.T) {
		plan := relatedScopeTestPlan(t, false)
		targetVariable := firstTraversalTargetForScopeTest(t, plan.StageSequence.Stages[0].RelatedExpand.RelatedRecords.Operations)
		if err := ir.ValidateGenericPhysicalPlanScope(plan); err != nil {
			t.Fatalf("compiled related expansion scope: %v", err)
		}
		cases := []struct {
			name   string
			mutate func(*testing.T, *[]ir.PhysicalOperation)
		}{
			{"missing project guard", func(t *testing.T, operations *[]ir.PhysicalOperation) {
				requireScopeMutation(t, removePhysicalScopeFilter(operations, targetVariable, "project"))
			}},
			{"misbound generation guard", func(t *testing.T, operations *[]ir.PhysicalOperation) {
				requireScopeMutation(t, setPhysicalScopeFilterBind(operations, targetVariable, "dataset_generation", "project"))
			}},
			{"missing auth path input", func(t *testing.T, operations *[]ir.PhysicalOperation) {
				requireScopeMutation(t, removePhysicalScopeAuthPath(operations, targetVariable))
			}},
			{"misbound auth equality", func(t *testing.T, operations *[]ir.PhysicalOperation) {
				requireScopeMutation(t, setPhysicalScopeAuthEqualityBind(operations, targetVariable, "project"))
			}},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				mutated := ir.ClonePhysicalPlan(plan)
				operations := &mutated.StageSequence.Stages[0].RelatedExpand.RelatedRecords.Operations
				test.mutate(t, operations)
				if err := ir.ValidateGenericPhysicalPlanScope(mutated); err == nil {
					t.Fatal("scope validator accepted an unscoped related resource")
				}
			})
		}
	})

	t.Run("active related document lookup", func(t *testing.T) {
		plan := relatedScopeTestPlan(t, true)
		if err := ir.ValidateGenericPhysicalPlanScope(plan); err != nil {
			t.Fatalf("compiled active related lookup scope: %v", err)
		}
		stage := plan.StageSequence.Stages[1]
		lookup := findDocumentLookupForScopeTest(t, stage.RelatedExpand.RelatedRecords.Operations)
		if lookup.Variable == "" {
			t.Fatal("compiled active related stage has an empty document lookup variable")
		}
		mutated := ir.ClonePhysicalPlan(plan)
		operations := mutated.StageSequence.Stages[1].RelatedExpand.RelatedRecords.Operations
		requireScopeMutation(t, removePhysicalScopeFilter(&operations, lookup.Variable, "dataset_generation"))
		mutated.StageSequence.Stages[1].RelatedExpand.RelatedRecords.Operations = operations
		if err := ir.ValidateGenericPhysicalPlanScope(mutated); err == nil {
			t.Fatal("scope validator accepted an active related document lookup without its generation guard")
		}
	})
}

func relatedScopeTestPlan(t *testing.T, onward bool) ir.PhysicalPlan {
	t.Helper()
	output := constructionTestOutput()
	groupLabel, categoryLabel, amountLabel := "Group", "Category", "Amount"
	steps := []recipe.ConstructionStep{{
		ID: "expand_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
			AnchorColumnID: "_key", ChoiceID: "patient-observation", TargetNodeID: "observation-node", TargetResourceType: "Observation",
			Route: []recipe.ConstructionRelatedRouteStep{{
				EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
				FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
				StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
			}},
			ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionPreserveParent, RelatedRecordColumnID: "observation_id",
		}},
		Outputs: []recipe.StageColumn{
			{ID: "group_id", Name: "group", Label: groupLabel}, {ID: "category_id", Name: "category", Label: categoryLabel},
			{ID: "amount_id", Name: "amount", Label: amountLabel}, {ID: "observation_id", Name: "observation_id", Label: "Observation ID", Type: "string", Nullable: true},
		},
	}}
	if onward {
		_, observationIdentity := relatedExpandIdentityColumnNames("expand_observations")
		steps = append(steps, recipe.ConstructionStep{
			ID: "expand_specimens", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand_observations"}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
				AnchorColumnID: observationIdentity, ChoiceID: "observation-specimen", TargetNodeID: "specimen-node", TargetResourceType: "Specimen",
				Route: []recipe.ConstructionRelatedRouteStep{{
					EdgeID: "observation-specimen", FromNodeID: "observation-node", ToNodeID: "specimen-node",
					FromResourceType: "Observation", ToResourceType: "Specimen", Relationship: "specimen_Specimen",
					StorageDirection: "OUTBOUND", MatchMode: "OPTIONAL",
				}},
				ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionPreserveParent, RelatedRecordColumnID: "specimen_id",
			}},
			Outputs: []recipe.StageColumn{
				{ID: "group_id", Name: "group", Label: groupLabel}, {ID: "category_id", Name: "category", Label: categoryLabel},
				{ID: "amount_id", Name: "amount", Label: amountLabel},
				{ID: "observation_id", Name: "observation_id", Label: "Observation ID", Type: "string", Nullable: true},
				{ID: "specimen_id", Name: "specimen_id", Label: "Specimen ID", Type: "string", Nullable: true},
			},
		})
	}
	output.Construction = &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{
		{ID: "group_id", Name: "group", Label: groupLabel}, {ID: "category_id", Name: "category", Label: categoryLabel},
		{ID: "amount_id", Name: "amount", Label: amountLabel},
	}, Steps: steps}
	return compileDerivedTestOutput(t, output).Plan
}

func findPhysicalSetForScopeTest(t *testing.T, plan ir.PhysicalPlan) int {
	t.Helper()
	for index, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalSetOp && operation.Set != nil {
			return index
		}
	}
	t.Fatal("compiled correlated plan has no physical set")
	return -1
}

func findPopulationRootForScopeTest(t *testing.T, plan ir.PhysicalPlan) *ir.PhysicalRootScan {
	t.Helper()
	for index := range plan.Operations {
		if plan.Operations[index].RootScan != nil && plan.Operations[index].RootScan.Population != nil {
			return plan.Operations[index].RootScan
		}
	}
	t.Fatal("compiled population plan has no population root")
	return nil
}

func findDocumentLookupForScopeTest(t *testing.T, operations []ir.PhysicalOperation) *ir.PhysicalDocumentLookup {
	t.Helper()
	for index := range operations {
		if operations[index].DocumentLookup != nil {
			return operations[index].DocumentLookup
		}
	}
	t.Fatal("compiled related subplan has no document lookup")
	return nil
}

func removePhysicalScopeFilter(operations *[]ir.PhysicalOperation, variable, path string) bool {
	for index := range *operations {
		operation := (*operations)[index]
		if operation.Kind == ir.PhysicalFilterOp && operation.Filter != nil && operation.Filter.Predicate.Left.Variable == variable &&
			len(operation.Filter.Predicate.Left.Path) == 1 && operation.Filter.Predicate.Left.Path[0] == path {
			*operations = append((*operations)[:index], (*operations)[index+1:]...)
			return true
		}
	}
	return false
}

func setPhysicalScopeFilterBind(operations *[]ir.PhysicalOperation, variable, path, bind string) bool {
	for index := range *operations {
		operation := (*operations)[index]
		if operation.Kind == ir.PhysicalFilterOp && operation.Filter != nil && operation.Filter.Predicate.Left.Variable == variable &&
			len(operation.Filter.Predicate.Left.Path) == 1 && operation.Filter.Predicate.Left.Path[0] == path && operation.Filter.Predicate.Right != nil {
			operation.Filter.Predicate.Right = &ir.PhysicalValue{BindKey: bind}
			return true
		}
	}
	return false
}

func removePhysicalScopeAuthPath(operations *[]ir.PhysicalOperation, resourceVariable string) bool {
	for index := range *operations {
		operation := (*operations)[index]
		if operation.Kind != ir.PhysicalDerivedLetOp || operation.DerivedLet == nil || operation.DerivedLet.Operator != "AUTH_RESOURCE_PATH_ALLOWED" {
			continue
		}
		for inputIndex, input := range operation.DerivedLet.Inputs {
			if input.Variable == resourceVariable && len(input.Path) == 1 && input.Path[0] == "auth_resource_path" {
				operation.DerivedLet.Inputs = append(operation.DerivedLet.Inputs[:inputIndex], operation.DerivedLet.Inputs[inputIndex+1:]...)
				return true
			}
		}
	}
	return false
}

func setPhysicalScopeAuthEqualityBind(operations *[]ir.PhysicalOperation, resourceVariable, bind string) bool {
	for index := range *operations {
		operation := (*operations)[index]
		if operation.Kind != ir.PhysicalDerivedLetOp || operation.DerivedLet == nil || operation.DerivedLet.Operator != "AUTH_RESOURCE_PATH_ALLOWED" {
			continue
		}
		found := false
		for _, input := range operation.DerivedLet.Inputs {
			if input.Variable == resourceVariable && len(input.Path) == 1 && input.Path[0] == "auth_resource_path" {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		for filterIndex := range *operations {
			filter := (*operations)[filterIndex]
			if filter.Kind == ir.PhysicalFilterOp && filter.Filter != nil && filter.Filter.Predicate.Left.Variable == operation.DerivedLet.Variable && len(filter.Filter.Predicate.Left.Path) == 0 && filter.Filter.Predicate.Right != nil {
				filter.Filter.Predicate.Right = &ir.PhysicalValue{BindKey: bind}
				return true
			}
		}
	}
	return false
}

func firstTraversalTargetForScopeTest(t *testing.T, operations []ir.PhysicalOperation) string {
	t.Helper()
	for _, operation := range operations {
		if operation.Traversal != nil {
			return operation.Traversal.TargetVariable
		}
	}
	t.Fatal("compiled nested plan has no traversal target")
	return ""
}

func requireScopeMutation(t *testing.T, mutated bool) {
	t.Helper()
	if !mutated {
		t.Fatal("scope test mutation did not find its exact guard")
	}
}
