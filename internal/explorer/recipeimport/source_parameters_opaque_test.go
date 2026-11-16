package recipeimport

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestUnchangedOpaqueSourceExpressionsSurviveUnrelatedEdits(t *testing.T) {
	cases := []struct {
		name       string
		expression recipe.Expression
	}{
		{name: "document reference", expression: recipe.Expression{Document: &recipe.DocumentRef{Context: "root"}}},
		{name: "unsupported function", expression: recipe.Expression{Call: "legacy_lookup", Args: []recipe.Expression{{Select: "root.id"}}}},
		{name: "complex literal", expression: recipe.Expression{Literal: []byte(`{"legacy":true}`)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pinned := recipe.Output{
				RootResourceType: "Patient",
				Fields:           []recipe.Field{{Name: "legacy_value", Label: "Legacy", Expr: tc.expression}},
			}
			plan := SourcePlan(pinned)
			params := plan.Steps[1].Parameters
			params.FieldGroup.Fields[0].Label = "Current label"
			change := authoringv2.ImportedSourceStepChange{StepID: "fields", Enabled: true, Parameters: params}
			raw, err := json.Marshal(change)
			if err != nil {
				t.Fatal(err)
			}
			var decoded authoringv2.ImportedSourceStepChange
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("unrelated parameter edit failed request validation: %v", err)
			}
			plan.Steps[1].Parameters = decoded.Parameters

			got, err := ApplySourcePlan(pinned, &plan)
			if err != nil {
				t.Fatalf("unrelated field edit rejected unchanged opaque expression: %v", err)
			}
			if !reflect.DeepEqual(got.Fields[0].Expr, tc.expression) {
				t.Fatalf("opaque expression changed: got %#v, want %#v", got.Fields[0].Expr, tc.expression)
			}

			changed := SourcePlan(pinned)
			changed.Steps[1].Parameters.FieldGroup.Fields[0].Expr = recipe.Expression{Select: "root.id"}
			if _, err := ApplySourcePlan(pinned, &changed); err == nil || !strings.Contains(err.Error(), "cannot be changed") {
				t.Fatalf("editing an opaque expression was not rejected at the pin boundary: %v", err)
			}
		})
	}
}

func TestSourceParameterEditsCannotIntroduceOpaqueExpressions(t *testing.T) {
	unsupported := []recipe.Expression{
		{Document: &recipe.DocumentRef{Context: "root"}},
		{Call: "legacy_lookup", Args: []recipe.Expression{{Select: "root.id"}}},
		{Literal: []byte(`{"legacy":true}`)},
	}
	for _, expression := range unsupported {
		plan := SourcePlan(recipe.Output{
			RootResourceType: "Patient",
			Fields:           []recipe.Field{{Name: "record_id", Expr: recipe.Expression{Select: "root.id"}}},
		})
		plan.Steps[1].Parameters.FieldGroup.Fields[0].Expr = expression
		if _, err := ApplySourcePlan(recipe.Output{
			RootResourceType: "Patient",
			Fields:           []recipe.Field{{Name: "record_id", Expr: recipe.Expression{Select: "root.id"}}},
		}, &plan); err == nil {
			t.Fatalf("new opaque expression %#v was accepted", expression)
		}
	}
}
