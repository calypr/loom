package aql

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestAQL16ConstructionUnpivotStageSchemaIdentity(t *testing.T) {
	score, amount := "score", "amount"
	output := recipe.Output{
		Name: "aql16_unpivot_schema", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "score", ColumnID: "score_id", Expr: recipe.Expression{Select: "root.valueInteger"}},
			{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.valueQuantity.value"}},
		},
		Construction: &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{
			{ID: "status_id", Name: "status"}, {ID: "score_id", Name: "score"}, {ID: "amount_id", Name: "amount"},
		}},
	}
	output.Construction.Steps = []recipe.ConstructionStep{{
		ID: "unpivot_measures", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionUnpivotOp, Unpivot: &recipe.ConstructionUnpivot{
			ConstructionID: "unpivot_measures_values",
			Inputs: []recipe.ConstructionUnpivotInput{
				{ColumnID: "score_id", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &score}},
				{ColumnID: "amount_id", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &amount}},
			},
			KeyOutputColumnID: "measure_id", ValueOutputColumnID: "measure_value_id",
			NullRowPolicy: recipe.UnpivotNullDrop,
		}},
		Outputs: []recipe.StageColumn{
			{ID: "status_id", Name: "status"}, {ID: "measure_id", Name: "measure"},
			{ID: "measure_value_id", Name: "measure_value"},
		},
	}}
	bindings := recipe.RuntimeBindings{
		Project: "p13-proj", DatasetGeneration: "p13-gen",
		AuthResourcePaths: []string{"FHIR/Observation"},
	}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                output.Name, TranslationVersion: "test",
		Outputs: []recipe.Output{output},
	}
	if err := bundle.Validate(); err != nil {
		t.Fatalf("bundle validate: %v", err)
	}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("BuildRecipePlan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatalf("ResolveRecipePlan: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("lower.CompileResolvedRecipePlan: %v", err)
	}
	if len(compiled.Outputs) != 1 {
		t.Fatalf("compiled outputs = %d, want 1", len(compiled.Outputs))
	}
	physical := compiled.Outputs[0].Plan
	if physical.StageSequence == nil || len(physical.StageSequence.Stages) != 1 {
		t.Fatalf("expected one construction stage, got %+v", physical.StageSequence)
	}
	stage := physical.StageSequence.Stages[0]
	if stage.Kind != ir.PhysicalStageUnpivotOp || stage.Unpivot == nil {
		t.Fatalf("stage kind = %q, want UNPIVOT payload", string(stage.Kind))
	}
	var rootKey *ir.PhysicalStageColumn
	identities := 0
	for i := range stage.OutputColumns {
		column := &stage.OutputColumns[i]
		if column.Identity {
			identities++
			if column.Name != stage.RowIdentityColumn {
				t.Fatalf("identity column %q is not the named row identity %q: %#v", column.Name, stage.RowIdentityColumn, column)
			}
		}
		if column.Name == "_key" {
			rootKey = column
		}
	}
	if stage.RowIdentityColumn != "__loom_row_id" {
		t.Fatalf("row identity column = %q, want the generated __loom_row_id", stage.RowIdentityColumn)
	}
	if identities != 1 {
		t.Fatalf("stage schema declares %d identity columns, want exactly the generated row identity: %#v", identities, stage.OutputColumns)
	}
	if rootKey == nil || !rootKey.Internal || rootKey.Identity {
		t.Fatalf("carried root _key must be internal and identity-free: %#v", rootKey)
	}
	rendered, err := RenderPhysicalPlan(physical)
	if err != nil {
		t.Fatalf("AQL renderer refused the production-lowered UNPIVOT stage: %v", err)
	}
	if !strings.Contains(rendered.Query, `"TABLE_UNPIVOT"`) {
		t.Fatalf("rendered AQL lacks the TABLE_UNPIVOT identity witness (table_reshape.go:557 posture):\n%s", rendered.Query)
	}
}
