package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestRenderFirstProjectionStopsSelectorAfterFirstValue(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Fields: []semantic.SemanticField{{
			Name: "extension_url", FieldRef: "Patient.extension[].url",
			Expr:       semantic.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "extension[].url"})},
			Projection: spec.ProjectionFirst,
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "FILTER __value != null\n    LIMIT 1\n    RETURN __value") {
		t.Fatalf("FIRST selector does not stop after its first value:\n%s", rendered.Query)
	}
}

func TestRenderArrayProjectionRetainsEverySelectorValue(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Fields: []semantic.SemanticField{{
			Name: "extension_urls", FieldRef: "Patient.extension[].url",
			Expr:       semantic.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "extension[].url"})},
			Projection: spec.ProjectionArray,
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered.Query, "LIMIT 1") {
		t.Fatalf("array selector was truncated:\n%s", rendered.Query)
	}
}

func TestRenderChildSelectorLimitsOnlyWhenEveryConsumerUsesFirst(t *testing.T) {
	build := func(projections ...spec.ProjectionMode) string {
		t.Helper()
		fields := make([]semantic.SemanticField, 0, len(projections))
		for index, projection := range projections {
			fields = append(fields, semantic.SemanticField{
				Name: "title_" + string(rune('a'+index)), FieldRef: "DocumentReference.content[].attachment.title",
				Expr:       semantic.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "content[].attachment.title"})},
				Projection: projection,
			})
		}
		plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []semantic.SemanticNode{{
				Alias: "document", ResourceType: "DocumentReference", EdgeLabel: "subject_Patient", Fields: fields,
			}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		rendered, err := aql.RenderPhysicalPlan(plan)
		if err != nil {
			t.Fatal(err)
		}
		return rendered.Query
	}

	firstOnly := build(spec.ProjectionFirst)
	if !strings.Contains(firstOnly, "FILTER __value != null\n    LIMIT 1\n    RETURN __value") {
		t.Fatalf("first-only child selector does not stop early:\n%s", firstOnly)
	}

	sharedWithArray := build(spec.ProjectionFirst, spec.ProjectionArray)
	if strings.Contains(sharedWithArray, "LIMIT 1") {
		t.Fatalf("shared all-values child selector was truncated:\n%s", sharedWithArray)
	}
}
