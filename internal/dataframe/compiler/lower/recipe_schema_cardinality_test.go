package lower

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestRecipeOutputSchemaUsesPhysicalArrayShapeForOptionalSemanticField(t *testing.T) {
	plan := ir.PhysicalPlan{Operations: []ir.PhysicalOperation{{
		Kind: ir.PhysicalReturnOp,
		Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{{
			Name: "concept_values",
			Expression: &ir.PhysicalExpression{
				Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalArrayCardinality,
				NullBehavior: ir.PhysicalEmptyOnNull,
				Value:        &ir.PhysicalValue{Variable: "concept_values"},
			},
		}}},
	}}}
	output := semantic.OutputPlan{
		Name: "specimens", RootResourceType: "Specimen",
		Root: semantic.SemanticNode{ResourceType: "Specimen", Fields: []semantic.SemanticField{{
			Name: "concept_values", FieldRef: "Observation.component[].valueString",
			Expr: semantic.SemanticExpression{Type: expression.Type{
				Kind: expression.KindString, Cardinality: expression.OptionalOne,
			}},
		}}},
	}
	schema, err := recipeOutputSchema(plan, output, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(schema) != 1 || schema[0].Cardinality != string(expression.Many) || !schema[0].Nullable {
		t.Fatalf("physical list projection must publish as a nullable list: %#v", schema)
	}
}

func TestRecipeOutputSchemaPreservesCorrelatedPivotListForm(t *testing.T) {
	for _, mode := range []string{"ALL", "DISTINCT", "FIRST", "VALUE"} {
		t.Run(mode, func(t *testing.T) {
			plan := ir.PhysicalPlan{Operations: []ir.PhysicalOperation{{
				Kind: ir.PhysicalReturnOp,
				Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{{
					Name: "concept_values",
					Expression: &ir.PhysicalExpression{
						Kind: ir.PhysicalObjectLookupExpression, Cardinality: ir.PhysicalScalarCardinality,
						NullBehavior: ir.PhysicalPreserveNull,
						ObjectLookup: &ir.PhysicalObjectLookup{ObjectVariable: "concept_map", KeyBindKey: "concept_key"},
					},
				}}},
			}}}
			output := semantic.OutputPlan{
				Name: "specimens", RootResourceType: "Specimen",
				Root: semantic.SemanticNode{ResourceType: "Specimen", Pivots: []semantic.SemanticPivot{{
					Name: "disease", ValueKind: expression.KindString,
					Columns:        []string{"primary_disease_type"},
					ColumnAliases:  map[string]string{"primary_disease_type": "concept_values"},
					ProjectionMode: mode,
				}}},
			}
			schema, err := recipeOutputSchema(plan, output, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			want := string(expression.RequiredOne)
			if mode == "ALL" || mode == "DISTINCT" {
				want = string(expression.Many)
			}
			if len(schema) != 1 || schema[0].Cardinality != want {
				t.Fatalf("%s pivot schema cardinality = %#v, want %s", mode, schema, want)
			}
		})
	}
}
