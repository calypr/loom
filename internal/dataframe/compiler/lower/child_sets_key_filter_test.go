package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestOptionalCorrelatedPivotFiltersKeyBeforeSetSort(t *testing.T) {
	for _, matchMode := range []spec.TraversalMatchMode{"", spec.TraversalMatchOptional} {
		t.Run(string(matchMode), func(t *testing.T) {
			plan, err := BuildGenericPhysicalPlanWithPolicy(optionalCorrelatedPivotPlan(t, matchMode), semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			if err := plan.Validate(); err != nil {
				t.Fatalf("physical plan validation: %v", err)
			}
			if got := countCorrelationKeyMatches(plan.Operations); got != 1 {
				t.Fatalf("correlation key-match predicates = %d, want 1", got)
			}
			rendered, err := aql.RenderPhysicalPlan(plan)
			if err != nil {
				t.Fatal(err)
			}
			keyFilter := strings.Index(rendered.Query, "FILTER __correlation_key_system == @child_set_1_pivot_key_shared_system")
			setSort := strings.Index(rendered.Query, "SORT child_set_1_node._key")
			unsupportedArm := strings.Index(rendered.Query, "INVALID_CHOICE_ARM")
			if keyFilter < 0 || setSort < 0 || keyFilter > setSort {
				t.Fatalf("key-only pivot filter must precede the child set sort (filter=%d sort=%d):\n%s", keyFilter, setSort, rendered.Query)
			}
			if unsupportedArm < setSort {
				t.Fatalf("unsupported value[x] handling must remain in the later pivot projection (sort=%d unsupported=%d):\n%s", setSort, unsupportedArm, rendered.Query)
			}
			if rendered.BindVars["child_set_1_pivot_key_shared_code"] != "shared" || rendered.BindVars["child_set_1_pivot_key_shared_system"] != "urn:study:A" {
				t.Fatalf("key-match bind values = %#v", rendered.BindVars)
			}
		})
	}
}

func TestOptionalCorrelatedPivotKeyFilterRequiresIsolatedLeaf(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*semantic.SemanticNode)
	}{
		{
			name: "another field",
			change: func(child *semantic.SemanticNode) {
				child.Fields = append(child.Fields, semantic.SemanticField{
					Name: "status",
					Expr: semantic.SemanticExpression{
						Expression: expression.Select(expression.SelectorRef{Context: "observation", Path: "status"}),
						Type:       expression.Type{Kind: expression.KindCode, Cardinality: expression.OptionalOne}, Context: "observation",
					},
				})
			},
		},
		{
			name: "another pivot",
			change: func(child *semantic.SemanticNode) {
				second := child.Pivots[0]
				second.Name = "another"
				second.Columns = []string{"another"}
				second.CorrelationCode = "another"
				child.Pivots = append(child.Pivots, second)
			},
		},
		{
			name: "descendant",
			change: func(child *semantic.SemanticNode) {
				child.Children = append(child.Children, semantic.SemanticNode{Alias: "patient", ResourceType: "Patient", EdgeLabel: "subject_Patient", MatchMode: spec.TraversalMatchOptional})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := optionalCorrelatedPivotPlan(t, "")
			test.change(&output.Root.Children[0])
			plan, err := BuildGenericPhysicalPlanWithPolicy(output, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			if got := countCorrelationKeyMatches(plan.Operations); got != 0 {
				t.Fatalf("non-isolated child has %d key-match predicates, want none", got)
			}
		})
	}
}

func optionalCorrelatedPivotPlan(t *testing.T, matchMode spec.TraversalMatchMode) semantic.OutputPlan {
	t.Helper()
	selector := func(path string) spec.Selector {
		parsed, err := spec.ParseSelector(path)
		if err != nil {
			t.Fatalf("parse selector %q: %v", path, err)
		}
		return parsed
	}
	return semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []semantic.SemanticNode{{
			Alias: "observation", ResourceType: "Observation", EdgeLabel: "subject_Patient", MatchMode: matchMode,
			Pivots: []semantic.SemanticPivot{{
				Name: "shared", Columns: []string{"shared"}, ProjectionMode: "VALUE",
				ColumnSelector: selector("component[].code.coding[].code"), ValueSelector: selector("valueQuantity.value"),
				Correlation: &fhirschema.CorrelatedBinding{
					OwnerPath: "component[]", KeyPath: "component[].code.coding[]", SystemPath: "system", CodePath: "code",
					ValuePath: "valueQuantity.value", ChoiceArms: []string{"valueQuantity"}, LogicalType: "decimal",
				},
				CorrelationSystem: "urn:study:A", CorrelationCode: "shared",
			}},
		}},
	}}
}

func countCorrelationKeyMatches(operations []ir.PhysicalOperation) int {
	count := 0
	for _, operation := range operations {
		if operation.Filter != nil && operation.Filter.Expression != nil && operation.Filter.Expression.Comparison != nil && operation.Filter.Expression.Comparison.CorrelationKeyMatch != nil {
			count++
		}
		if operation.Set != nil {
			count += countCorrelationKeyMatches(operation.Set.Subplan.Operations)
		}
	}
	return count
}
