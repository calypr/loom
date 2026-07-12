package dataframe

import (
	"strings"
	"testing"
)

func TestCompileTypedFilterUsesBindVariables(t *testing.T) {
	value := "female"
	c := &compiler{bindVars: map[string]any{}}
	expr, err := c.compileTypedFilter("root.payload", TypedFilter{
		FieldRef:  "Patient.gender",
		Selector:  "gender",
		FieldKind: FilterString,
		Operator:  FilterEquals,
		Values:    []FilterValue{{Kind: FilterString, String: &value}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(expr, value) || !strings.Contains(expr, "@__filter_value_0") {
		t.Fatalf("filter expression does not use a bind variable: %s", expr)
	}
	if got := c.bindVars["__filter_value_0"]; got != value {
		t.Fatalf("bind value = %#v, want %q", got, value)
	}
}

func TestCompileTypedFilterQuantifiers(t *testing.T) {
	value := "BAM"
	for _, quantifier := range []ArrayQuantifier{QuantifierAny, QuantifierAll, QuantifierNone} {
		t.Run(string(quantifier), func(t *testing.T) {
			c := &compiler{bindVars: map[string]any{}}
			expr, err := c.compileTypedFilter("node.payload", TypedFilter{
				FieldRef:   "DocumentReference.type",
				Selector:   "type.coding[].display",
				FieldKind:  FilterString,
				Repeated:   true,
				Quantifier: quantifier,
				Operator:   FilterEquals,
				Values:     []FilterValue{{Kind: FilterString, String: &value}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(expr, "FOR __value") {
				t.Fatalf("unexpected quantifier expression: %s", expr)
			}
		})
	}
}

func TestCompileTypedFilterNormalizesOrderedTemporalComparison(t *testing.T) {
	date := "2025-01-01"
	c := &compiler{bindVars: map[string]any{}}
	expr, err := c.compileTypedFilter("root.payload", TypedFilter{
		FieldRef: "Patient.birth_date", Selector: "birthDate", FieldKind: FilterDate,
		Operator: FilterGreaterEq, Values: []FilterValue{{Kind: FilterDate, Date: &date}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(expr, "DATE_TIMESTAMP(__value)") || !strings.Contains(expr, "DATE_TIMESTAMP(@__filter_value_0)") {
		t.Fatalf("ordered temporal filter did not normalize timestamps: %s", expr)
	}
}

func TestGenericLoweringPushesRootAndChildFiltersIntoAQL(t *testing.T) {
	gender := "female"
	bam := "BAM"
	compiled, err := CompileRequest(Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		Filters: []TypedFilter{{
			FieldRef: "Patient.gender", Selector: "gender", FieldKind: FilterString, Operator: FilterEquals,
			Values: []FilterValue{{Kind: FilterString, String: &gender}},
		}},
		Traversals: []TraversalStep{{
			Label: "subject_Patient", ToResourceType: "DocumentReference", Alias: "file",
			Filters: []TypedFilter{{
				FieldRef: "DocumentReference.type", Selector: "type.coding[].display", FieldKind: FilterString,
				Repeated: true, Quantifier: QuantifierAny, Operator: FilterEquals,
				Values: []FilterValue{{Kind: FilterString, String: &bam}},
			}},
		}},
	}, 25)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.PlanProfile != "generic_fhir_graph" {
		t.Fatalf("expected generic filtered plan, got %q", compiled.PlanProfile)
	}
	if !containsOptimizerRule(compiled.OptimizationRules, OptimizerRuleFilterPushdown) {
		t.Fatalf("expected filter-pushdown optimizer provenance, got %#v", compiled.OptimizationRules)
	}
	if strings.Count(compiled.Query, "FILTER (LENGTH(FOR __value") < 2 {
		t.Fatalf("expected root and child pushed filters, got:\n%s", compiled.Query)
	}
	if len(compiled.BindVars) < 6 {
		t.Fatalf("expected selector and value bind variables, got %#v", compiled.BindVars)
	}
}
