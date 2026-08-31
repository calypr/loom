package recipe

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestFragmentExpansionIsHygienicAndDigestable(t *testing.T) {
	lib := FragmentLibrary{Fragments: map[string]Fragment{"reference_id": {Name: "reference_id", Version: "1", Params: []string{"value"}, Expr: Expression{Call: "path_segment", Args: []Expression{{Select: "$value"}}}}}}
	input := Expression{Call: "fragment:reference_id", Args: []Expression{{Select: "root.subject.reference"}}}
	expanded, err := lib.ExpandExpression(input)
	if err != nil {
		t.Fatal(err)
	}
	if expanded.Call != "path_segment" || expanded.Args[0].Select != "root.subject.reference" {
		t.Fatalf("unexpected expansion: %#v", expanded)
	}
	if _, err := lib.Digest(); err != nil {
		t.Fatal(err)
	}
}

func TestFragmentCycleRejected(t *testing.T) {
	lib := FragmentLibrary{Fragments: map[string]Fragment{"a": {Name: "a", Version: "1", Expr: Expression{Call: "fragment:b"}}, "b": {Name: "b", Version: "1", Expr: Expression{Call: "fragment:a"}}}}
	if _, err := lib.ExpandExpression(Expression{Call: "fragment:a"}); err == nil {
		t.Fatal("expected fragment cycle")
	}
}

func TestBundleExpandFragmentsProducesStandaloneDigestInput(t *testing.T) {
	bundle := Bundle{RecipeSchemaVersion: 1, Name: "fragments", TranslationVersion: "v", Fragments: &FragmentLibrary{Fragments: map[string]Fragment{
		"id": {Name: "id", Version: "1", Params: []string{"ctx"}, Expr: Expression{Select: "$ctx.id"}},
	}}, Outputs: []Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "patient", Fields: []Field{{Name: "id", Expr: Expression{Call: "fragment:id", Args: []Expression{{Select: "root"}}}}}}}}
	expanded, err := bundle.ExpandFragments()
	if err != nil {
		t.Fatal(err)
	}
	if expanded.Fragments != nil || expanded.Outputs[0].Fields[0].Expr.Select != "root.id" {
		t.Fatalf("bundle was not standalone: %#v", expanded)
	}
}

func TestExpandBundleExpandsEveryExpressionNodeWithoutMutatingInputs(t *testing.T) {
	fragment := Fragment{
		Name:    "selector",
		Version: "1",
		Params:  []string{"value"},
		Expr:    Expression{Call: "path_segment", Args: []Expression{{Select: "$value"}}},
	}
	lib := FragmentLibrary{Fragments: map[string]Fragment{"selector": fragment}}

	bundle := Bundle{
		RecipeSchemaVersion: 1,
		Name:                "all-expression-nodes",
		TranslationVersion:  "v1",
		Fragments:           &lib,
		Outputs: []Output{{
			Name:             "Patient",
			RootResourceType: "Patient",
			RowGrain:         "patient",
			Fields: []Field{{
				Name:      "field",
				Expr:      fragmentCall("root.field"),
				Fallbacks: []Expression{{Select: "root.fallback"}},
			}},
			Pivots: []Pivot{{
				Name:           "pivot",
				ColumnExpr:     fragmentCall("root.column"),
				ValueExpr:      fragmentCall("root.value"),
				ValueFallbacks: []Expression{fragmentCall("root.value_fallback")},
				ItemSource:     fragmentCall("root.items"),
				Columns:        []string{"one"},
			}},
			Aggregates: []Aggregate{{
				Name:      "aggregate",
				Operation: AggregateMin,
				Expr:      expressionPointer(fragmentCall("root.aggregate")),
			}},
			Slices: []RepresentativeSlice{{
				Name:  "slice",
				Limit: 1,
				Fields: []Field{{
					Name: "slice_field",
					Expr: fragmentCall("root.slice"),
				}},
			}},
			Traversals: []Traversal{{
				Name:           "subject",
				ToResourceType: "Observation",
				From:           expressionPointer(fragmentCall("root.subject")),
				Fields: []Field{{
					Name: "subject_field",
					Expr: fragmentCall("subject.field"),
				}},
				Pivots: []Pivot{{
					Name:       "subject_pivot",
					ColumnExpr: fragmentCall("subject.column"),
					ValueExpr:  fragmentCall("subject.value"),
					Columns:    []string{"one"},
				}},
				Aggregates: []Aggregate{{
					Name:      "subject_aggregate",
					Operation: AggregateMax,
					Expr:      expressionPointer(fragmentCall("subject.aggregate")),
				}},
				Slices: []RepresentativeSlice{{
					Name:  "subject_slice",
					Limit: 1,
					Fields: []Field{{
						Name: "subject_slice_field",
						Expr: fragmentCall("subject.slice"),
					}},
				}},
				Traversals: []Traversal{{
					Name:           "detail",
					ToResourceType: "Condition",
					From:           expressionPointer(fragmentCall("subject.detail")),
					Fields: []Field{{
						Name: "detail_field",
						Expr: fragmentCall("detail.field"),
					}},
				}},
				DynamicColumns: []DynamicColumn{{
					Name:   "subject_dynamic",
					Source: fragmentCall("subject.dynamic_source"),
					Key:    expressionPointer(fragmentCall("subject.dynamic_key")),
					Value:  expressionPointer(fragmentCall("subject.dynamic_value")),
				}},
				ExtensionColumns: []ExtensionColumn{{
					Name:       "subject_extension",
					Source:     fragmentCall("subject.extension"),
					MaxColumns: 1,
				}},
			}},
			Expand: &Expansion{
				From: fragmentCall("root.expand"),
				As:   "item",
			},
			Identity: &Identity{
				Name: "identity",
				Expr: fragmentCall("root.identity"),
			},
			DynamicColumns: []DynamicColumn{{
				Name:   "dynamic",
				Source: fragmentCall("root.dynamic_source"),
				Key:    expressionPointer(fragmentCall("root.dynamic_key")),
				Value:  expressionPointer(fragmentCall("root.dynamic_value")),
			}},
			ExtensionColumns: []ExtensionColumn{{
				Name:       "extension",
				Source:     fragmentCall("root.extension"),
				MaxColumns: 1,
			}},
		}},
	}

	before, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := bundle.ExpandFragments()
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("fragment expansion mutated its input\nbefore: %s\nafter:  %s", before, after)
	}
	if expanded.Fragments != nil {
		t.Fatal("expanded bundle still contains fragments")
	}
	repeated, err := bundle.ExpandFragments()
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := json.Marshal(expanded)
	if err != nil {
		t.Fatal(err)
	}
	repeatedJSON, err := json.Marshal(repeated)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, repeatedJSON) {
		t.Fatalf("repeated expansion changed the result\nfirst: %s\nagain: %s", firstJSON, repeatedJSON)
	}

	output := expanded.Outputs[0]
	assertExpandedExpression(t, output.Fields[0].Expr, "root.field")
	assertExpandedExpression(t, output.Pivots[0].ColumnExpr, "root.column")
	assertExpandedExpression(t, output.Pivots[0].ValueExpr, "root.value")
	assertExpandedExpression(t, output.Pivots[0].ValueFallbacks[0], "root.value_fallback")
	assertExpandedExpression(t, output.Pivots[0].ItemSource, "root.items")
	assertExpandedExpression(t, *output.Aggregates[0].Expr, "root.aggregate")
	assertExpandedExpression(t, output.Slices[0].Fields[0].Expr, "root.slice")
	assertExpandedExpression(t, *output.Traversals[0].From, "root.subject")
	assertExpandedExpression(t, output.Traversals[0].Fields[0].Expr, "subject.field")
	assertExpandedExpression(t, output.Traversals[0].Pivots[0].ColumnExpr, "subject.column")
	assertExpandedExpression(t, output.Traversals[0].Pivots[0].ValueExpr, "subject.value")
	assertExpandedExpression(t, *output.Traversals[0].Aggregates[0].Expr, "subject.aggregate")
	assertExpandedExpression(t, output.Traversals[0].Slices[0].Fields[0].Expr, "subject.slice")
	assertExpandedExpression(t, *output.Traversals[0].Traversals[0].From, "subject.detail")
	assertExpandedExpression(t, output.Traversals[0].Traversals[0].Fields[0].Expr, "detail.field")
	assertExpandedExpression(t, output.Traversals[0].DynamicColumns[0].Source, "subject.dynamic_source")
	assertExpandedExpression(t, *output.Traversals[0].DynamicColumns[0].Key, "subject.dynamic_key")
	assertExpandedExpression(t, *output.Traversals[0].DynamicColumns[0].Value, "subject.dynamic_value")
	assertExpandedExpression(t, output.Traversals[0].ExtensionColumns[0].Source, "subject.extension")
	assertExpandedExpression(t, output.Expand.From, "root.expand")
	assertExpandedExpression(t, output.Identity.Expr, "root.identity")
	assertExpandedExpression(t, output.DynamicColumns[0].Source, "root.dynamic_source")
	assertExpandedExpression(t, *output.DynamicColumns[0].Key, "root.dynamic_key")
	assertExpandedExpression(t, *output.DynamicColumns[0].Value, "root.dynamic_value")
	assertExpandedExpression(t, output.ExtensionColumns[0].Source, "root.extension")
}

func TestFragmentExpansionDoesNotMutateFragmentBodies(t *testing.T) {
	lib := FragmentLibrary{Fragments: map[string]Fragment{
		"selector": {
			Name:    "selector",
			Version: "1",
			Params:  []string{"value"},
			Expr:    Expression{Call: "path_segment", Args: []Expression{{Select: "$value"}}},
		},
	}}

	first, err := lib.ExpandExpression(fragmentCall("root.first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := lib.ExpandExpression(fragmentCall("root.second"))
	if err != nil {
		t.Fatal(err)
	}
	assertExpandedExpression(t, first, "root.first")
	assertExpandedExpression(t, second, "root.second")
	if got := lib.Fragments["selector"].Expr.Args[0].Select; got != "$value" {
		t.Fatalf("fragment body was mutated to %q", got)
	}
}

func fragmentCall(selectPath string) Expression {
	return Expression{Call: "fragment:selector", Args: []Expression{{Select: selectPath}}}
}

func expressionPointer(expr Expression) *Expression { return &expr }

func assertExpandedExpression(t *testing.T, expr Expression, selectPath string) {
	t.Helper()
	if expr.Call != "path_segment" || len(expr.Args) != 1 || expr.Args[0].Select != selectPath {
		t.Fatalf("expected path_segment(%q), got %#v", selectPath, expr)
	}
}
