package compiler

import (
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestCorrelatedSystemVersionSelectionPropagatesThroughPhysicalPlan(t *testing.T) {
	for _, version := range []string{"", "v2"} {
		t.Run("version="+version, func(t *testing.T) {
			binding := &fhirschema.CorrelatedBinding{
				KeyPath: "code.coding[]", VersionPath: "version", ValueScope: fhirschema.CorrelatedValueScopeCoding,
				SelectionScope: fhirschema.CorrelatedSelectionScopeSystemVersion,
				SystemPath:     "system", CodePath: "code", ValuePath: "display", ValueFallback: []string{"code"}, LogicalType: "string",
			}
			plan, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
				Alias: "root", ResourceType: "Condition",
				Pivots: []semantic.SemanticPivot{{Name: "diagnosis", Columns: []string{"category"}, Correlation: binding, CorrelationSystem: "urn:test", CorrelationVersion: version, ProjectionMode: "DISTINCT"}},
				Filters: []spec.TypedFilter{{
					FieldRef: "Condition.code", Selector: "code.coding[].code", FieldKind: spec.FilterCode, Repeated: true, Quantifier: spec.QuantifierAny,
					Operator: spec.FilterEquals, Correlation: binding, Values: []spec.FilterValue{{Kind: spec.FilterCode, Code: &spec.CodeValue{System: "urn:test", Version: version}}},
				}},
			}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			rendered, err := aql.RenderPhysicalPlan(plan)
			if err != nil {
				t.Fatal(err)
			}
			for key := range rendered.BindVars {
				if key == "pivot_root_diagnosis_columns_code" || strings.HasPrefix(key, "correlated_code_") {
					t.Fatalf("category retains unused code bind %q: %#v", key, rendered.BindVars)
				}
			}
			for _, key := range []string{"pivot_root_diagnosis_columns_version", "correlated_version_root_filter_1"} {
				if got, ok := rendered.BindVars[key]; !ok || got != version {
					t.Fatalf("category version bind %q = %#v, present=%t", key, got, ok)
				}
			}
			for _, forbidden := range []string{"POSITION(@pivot_root_diagnosis_columns, __correlation_code)", "FILTER __correlation_code == @"} {
				if strings.Contains(rendered.Query, forbidden) {
					t.Fatalf("category retains exact-code operator %q:\n%s", forbidden, rendered.Query)
				}
			}
			if !strings.Contains(rendered.Query, "RETURN { key: FIRST(@pivot_root_diagnosis_columns),") {
				t.Fatalf("category must collect every matched code into one declared column:\n%s", rendered.Query)
			}
			for _, operation := range plan.DeferredExpressionLets {
				if operation.ExpressionLet != nil && operation.ExpressionLet.Expression.Pivot != nil {
					correlation := operation.ExpressionLet.Expression.Pivot.Correlation
					if correlation == nil || correlation.SelectionScope != fhirschema.CorrelatedSelectionScopeSystemVersion {
						t.Fatalf("physical category lost scope: %#v", correlation)
					}
					correlation.SelectionScope = "ALL_CODES"
					if err := plan.Validate(); err == nil {
						t.Fatal("forged physical selection scope accepted")
					}
					correlation.SelectionScope = fhirschema.CorrelatedSelectionScopeSystemVersion
					correlation.CodeBindKey = "unused"
					plan.BindVars["unused"] = "normal"
					if err := plan.Validate(); err == nil {
						t.Fatal("category physical IR accepted unused code bind")
					}
					break
				}
			}
		})
	}
}

func TestCorrelatedExplicitExactSelectionKeepsLegacyAQLAndBinds(t *testing.T) {
	for _, valueScope := range []fhirschema.CorrelatedValueScope{"", fhirschema.CorrelatedValueScopeCoding} {
		t.Run(string(valueScope), func(t *testing.T) {
			binding := &fhirschema.CorrelatedBinding{KeyPath: "code.coding[]", SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value", LogicalType: "decimal"}
			if valueScope == fhirschema.CorrelatedValueScopeCoding {
				binding.ValueScope, binding.ValuePath, binding.LogicalType, binding.VersionPath = valueScope, "display", "string", "version"
				binding.ValueFallback = []string{"code"}
			}
			render := func() aql.RenderedPhysicalPlan {
				t.Helper()
				plan, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
					Alias: "root", ResourceType: "Observation", Pivots: []semantic.SemanticPivot{{Name: "exact", Columns: []string{"normal"}, Correlation: binding, CorrelationSystem: "urn:test", CorrelationCode: "normal"}},
				}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
				if err != nil {
					t.Fatal(err)
				}
				rendered, err := aql.RenderPhysicalPlan(plan)
				if err != nil {
					t.Fatal(err)
				}
				return rendered
			}
			legacy := render()
			binding.SelectionScope = fhirschema.CorrelatedSelectionScopeExact
			explicit := render()
			if explicit.Query != legacy.Query || !reflect.DeepEqual(explicit.BindVars, legacy.BindVars) {
				t.Fatal("explicit EXACT changed legacy AQL or bind values")
			}
			if !strings.Contains(explicit.Query, "FILTER POSITION(@pivot_root_exact_columns, __correlation_code)") || explicit.BindVars["pivot_root_exact_columns_normal"] != "normal" {
				t.Fatalf("exact selection lost selected-code semantics: %#v\n%s", explicit.BindVars, explicit.Query)
			}
		})
	}
}
