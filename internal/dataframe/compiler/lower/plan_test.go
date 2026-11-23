package lower_test

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestBuildGenericPhysicalPlanNavigationSkeleton(t *testing.T) {
	output := recipeplan.OutputPlan{
		Root: recipeplan.SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []recipeplan.SemanticNode{{
				Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
				Children: []recipeplan.SemanticNode{{Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Specimen"}},
			}},
		},
	}
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(output, recipeplan.ExecutionContext{Project: "project-1", AuthResourcePaths: []string{"/programs/p1"}}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("built plan does not validate: %v", err)
	}
	if plan.BindVars["root_collection"] != "Patient" || plan.BindVars["traversal_1_edge_collection"] != "fhir_edge" || plan.BindVars["traversal_1_label"] != "subject_Patient" || plan.BindVars["traversal_1_target_type"] != "Specimen" {
		t.Fatalf("unexpected bound physical values: %#v", plan.BindVars)
	}
	if plan.BindVars["auth_resource_paths_unrestricted"] != false {
		t.Fatalf("unexpected auth mode: %#v", plan.BindVars)
	}

	traversals := 0
	authPredicates := 0
	for _, operation := range plan.Operations {
		if operation.Traversal != nil {
			traversals++
			if operation.Traversal.Direction != ir.PhysicalInbound {
				t.Fatalf("generic traversal direction = %q", operation.Traversal.Direction)
			}
			if operation.Traversal.EdgeTargetTypeField != "from_type" {
				t.Fatalf("generic inbound traversal edge discriminator = %q", operation.Traversal.EdgeTargetTypeField)
			}
			if operation.Source.SemanticNode == "" || operation.Source.Relationship == "" {
				t.Fatalf("traversal lost provenance: %#v", operation.Source)
			}
		}
		if operation.DerivedLet != nil && operation.DerivedLet.Operator == "AUTH_RESOURCE_PATH_ALLOWED" {
			authPredicates++
		}
	}
	if traversals != 2 || authPredicates != 3 {
		t.Fatalf("traversals=%d auth predicates=%d operations=%#v", traversals, authPredicates, plan.Operations)
	}
	last := plan.Operations[len(plan.Operations)-1]
	if last.Kind != ir.PhysicalReturnOp || len(last.Return.Projections) != 1 || last.Return.Projections[0].Name != "_key" {
		t.Fatalf("unexpected terminal return: %#v", last)
	}
}

func TestBuildGenericPhysicalPlanEmptyAuthScopeIsExplicit(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{Alias: "root", ResourceType: "Observation"}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.BindVars["auth_resource_paths_unrestricted"] != true {
		t.Fatalf("empty auth paths were not represented as unrestricted: %#v", plan.BindVars)
	}
}

func TestBuildGenericPhysicalPlanPreparesSelectorsAcrossRichConsumers(t *testing.T) {
	status := spec.Selector{Steps: []spec.SelectorStep{{Field: "id"}}}
	policy := ir.DefaultPhysicalOptimizationPolicy().WithRule(ir.PhysicalOptimizationRuleCompactProjection, false).WithRule(ir.PhysicalOptimizationRulePreparedSelectors, true)
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []recipeplan.SemanticNode{{
			Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient",
			Aggregates: []recipeplan.SemanticAggregate{{Name: "status_count", Operation: "COUNT_DISTINCT", Selector: &status}},
			Pivots:     []recipeplan.SemanticPivot{{Name: "status_values", ColumnSelector: status, ValueSelector: status, Columns: []string{"active", "resolved"}}},
			Slices:     []recipeplan.SemanticSlice{{Name: "representative", Limit: 1, Fields: []recipeplan.SemanticField{testSemanticField("status", status, "")}}},
		}},
	},
	}, recipeplan.ExecutionContext{Project: "p"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("prepared physical plan does not validate: %v", err)
	}
	var child *ir.PhysicalSet
	for index := range plan.Operations {
		if plan.Operations[index].Kind == ir.PhysicalSetOp && plan.Operations[index].Set != nil {
			child = plan.Operations[index].Set
			break
		}
	}
	if child == nil || child.Prepared == nil {
		t.Fatalf("expected rich child set to have a prepared selector projection: %#v", child)
	}
	if len(child.Prepared.Fields) != 1 || child.Prepared.Fields[0].Selector.CanonicalPath() != "id" {
		t.Fatalf("prepared selector fields = %#v, want one stable id field", child.Prepared.Fields)
	}
	preparedSet := child.Prepared.Variable
	var preparedRefs int
	var check func(ir.PhysicalExpression)
	check = func(expression ir.PhysicalExpression) {
		switch expression.Kind {
		case ir.PhysicalAggregateExpression:
			if expression.Aggregate != nil && expression.Aggregate.Value != nil && expression.Aggregate.Value.Extract != nil && expression.Aggregate.Value.Extract.Prepared != nil {
				preparedRefs++
			}
		case ir.PhysicalPivotExpression:
			if expression.Pivot != nil && expression.Pivot.PreparedKey != nil && expression.Pivot.PreparedValue != nil {
				preparedRefs += 2
			}
		case ir.PhysicalSliceExpression:
			if expression.Slice != nil {
				for _, projection := range expression.Slice.Projections {
					if projection.Expression.Extract != nil && projection.Expression.Extract.Prepared != nil {
						preparedRefs++
					}
				}
			}
		}
	}
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Expression != nil {
				check(*projection.Expression)
			}
		}
	}
	if preparedRefs != 2 {
		t.Fatalf("prepared rich consumer references = %d, want 2 (one shared pivot key/value pair)", preparedRefs)
	}
	renderedA, err := aql.Render(plan, aql.Options{})
	if err != nil {
		t.Fatal(err)
	}
	renderedB, err := aql.Render(plan, aql.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if renderedA.Query != renderedB.Query {
		t.Fatalf("prepared rendering is nondeterministic:\nA:\n%s\nB:\n%s", renderedA.Query, renderedB.Query)
	}
	if !strings.Contains(renderedA.Query, "LET "+preparedSet+" = (") {
		t.Fatalf("prepared selector set was not rendered:\n%s", renderedA.Query)
	}
}

func TestPreparedSelectorsPreserveFallbackExtraction(t *testing.T) {
	primary := spec.Selector{Steps: []spec.SelectorStep{{Field: "id"}}}
	fallback := spec.Selector{Steps: []spec.SelectorStep{{Field: "code"}}}
	policy := ir.DefaultPhysicalOptimizationPolicy().WithRule(ir.PhysicalOptimizationRulePreparedSelectors, true)
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []recipeplan.SemanticNode{{
			Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient",
			Fields:     []recipeplan.SemanticField{testSemanticFieldWithFallback("status_with_fallback", primary, fallback)},
			Aggregates: []recipeplan.SemanticAggregate{{Name: "status_count", Operation: "COUNT_DISTINCT", Selector: &primary}},
		}},
	},
	}, recipeplan.ExecutionContext{Project: "p"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	var child *ir.PhysicalSet
	for index := range plan.Operations {
		if plan.Operations[index].Kind == ir.PhysicalSetOp && plan.Operations[index].Set != nil {
			child = plan.Operations[index].Set
			break
		}
	}
	if child == nil || child.Prepared == nil {
		t.Fatalf("expected primary selector to be prepared: %#v", child)
	}
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name == "condition__status_with_fallback" && projection.Expression != nil && projection.Expression.Extract != nil {
				if projection.Expression.Extract.Prepared != nil {
					t.Fatalf("fallback field incorrectly used prepared primary value: %#v", projection.Expression.Extract.Prepared)
				}
				if len(projection.Expression.Extract.Fallbacks) != 1 {
					t.Fatalf("fallback field lost fallback selectors: %#v", projection.Expression.Extract.Fallbacks)
				}
			}
		}
	}
}

func TestChildPivotRetainsPayloadForDeferredEvaluation(t *testing.T) {
	code := spec.Selector{Steps: []spec.SelectorStep{{Field: "code"}, {Field: "coding", Iterate: true}, {Field: "display"}}}
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{
		Root: recipeplan.SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []recipeplan.SemanticNode{{
				Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient",
				Pivots: []recipeplan.SemanticPivot{{Name: "code_values", ColumnSelector: code, ValueSelector: code, Columns: []string{"active"}}},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var childSet *ir.PhysicalSet
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalSetOp && operation.Set != nil && operation.Set.Variable == "child_set_1" {
			childSet = operation.Set
			break
		}
	}
	if childSet == nil {
		t.Fatalf("pivot child set was not lowered: %#v", plan.Operations)
	}
	if childSet.Projection != nil {
		t.Fatalf("pivot child set must retain payload, got selector-only projection: %#v", childSet.Projection)
	}
	if childSet.Output == nil || !containsPhysicalSetOutputField(childSet.Output.Fields, ir.PhysicalSetPayloadField) {
		t.Fatalf("pivot child set must retain payload output: %#v", childSet.Output)
	}
	rendered, err := aql.Render(plan, aql.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "payload: child_set_1_node.payload") {
		t.Fatalf("pivot child payload was omitted from rendered set:\n%s", rendered.Query)
	}
}

func containsPhysicalSetOutputField(fields []ir.PhysicalSetOutputField, want ir.PhysicalSetOutputField) bool {
	for _, field := range fields {
		if field == want {
			return true
		}
	}
	return false
}

func TestBuildGenericPhysicalPlanRejectsUnsupportedSemanticFeatures(t *testing.T) {
	tests := []struct {
		name string
		root recipeplan.SemanticNode
		want string
	}{
		{"unknown root", recipeplan.SemanticNode{Alias: "root", ResourceType: "Unknown"}, "not represented"},
		{"child filter with incomplete type metadata", recipeplan.SemanticNode{Alias: "root", ResourceType: "Patient", Children: []recipeplan.SemanticNode{{Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient", Filters: []spec.TypedFilter{{FieldRef: "status"}}}}}, "unknown filter field kind"},
		{"unknown traversal", recipeplan.SemanticNode{Alias: "root", ResourceType: "Patient", Children: []recipeplan.SemanticNode{{Alias: "medication", ResourceType: "Medication", EdgeLabel: "missing"}}}, "not represented"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: test.root})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v; want substring %q", err, test.want)
			}
		})
	}
}
