package compilation

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestRecipeConstructionMapsTypedSequenceAndResolvedSourceSchema(t *testing.T) {
	authoredColumns := []authoringv2.Column{
		{ColumnID: "group_id", Column: "group", Label: "Group", LogicalType: "string"},
		{ColumnID: "category_id", Column: "category", Label: "Category", LogicalType: "string"},
		{ColumnID: "amount_id", Column: "amount", Label: "Amount", LogicalType: "decimal"},
	}
	alpha, beta := "alpha", "beta"
	minimum := 10.0
	authored := &authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			{
				ID: "pivot", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationPivot, Pivot: &authoringv2.ConstructionPivot{
					ConstructionID: "pivot", GroupKeyIDs: []string{"group_id"}, CategoryColumnID: "category_id", ValueColumnID: "amount_id",
					Categories: []authoringv2.ConstructionPivotCategory{
						{Key: authoringv2.TableScalar{Kind: authoringv2.TableScalarString, String: &alpha}, OutputColumnID: "alpha_id"},
						{Key: authoringv2.TableScalar{Kind: authoringv2.TableScalarString, String: &beta}, OutputColumnID: "beta_id"},
					},
					DuplicatePolicy:        authoringv2.ConstructionPivotDuplicateSum,
					MissingCellPolicy:      authoringv2.ConstructionPivotMissingNull,
					UnlistedCategoryPolicy: authoringv2.ConstructionPivotUnlistedError,
				}},
				Outputs: []authoringv2.StageColumn{
					{ID: "group_id", Name: "group", Label: "Group", Type: "string"},
					{ID: "alpha_id", Name: "alpha", Label: "Alpha", Type: "decimal"},
					{ID: "beta_id", Name: "beta", Label: "Beta", Type: "decimal"},
				},
			},
			{
				ID: "derive", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "pivot"}},
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationDerive, Derive: &authoringv2.ConstructionDerive{
					ConstructionID: "derive", OutputColumnID: "total_id", Operation: authoringv2.ConstructionDerivedAdd,
					Left:               authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionColumnOperand, ColumnID: "alpha_id"},
					Right:              authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionColumnOperand, ColumnID: "beta_id"},
					MissingInputPolicy: authoringv2.ConstructionMissingInputPropagateNull,
				}},
				Outputs: []authoringv2.StageColumn{
					{ID: "group_id", Name: "group", Label: "Group", Type: "string"},
					{ID: "alpha_id", Name: "alpha", Label: "Alpha", Type: "decimal"},
					{ID: "beta_id", Name: "beta", Label: "Beta", Type: "decimal"},
					{ID: "total_id", Name: "total", Label: "Total", Type: "decimal"},
				},
			},
			{
				ID: "filter", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "derive"}},
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{
					ColumnID: "total_id", Operator: authoringv2.ConstructionFilterGreaterEq,
					Values: []authoringv2.FilterValue{{Kind: authoringv2.ConstructionFilterDecimal, Decimal: &minimum}},
				}},
				Outputs: []authoringv2.StageColumn{
					{ID: "group_id", Name: "group", Label: "Group", Type: "string"},
					{ID: "alpha_id", Name: "alpha", Label: "Alpha", Type: "decimal"},
					{ID: "beta_id", Name: "beta", Label: "Beta", Type: "decimal"},
					{ID: "total_id", Name: "total", Label: "Total", Type: "decimal"},
				},
			},
			{
				ID: "unpivot", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "filter"}},
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationUnpivot, Unpivot: &authoringv2.ConstructionUnpivot{
					ConstructionID: "unpivot", Inputs: []authoringv2.ConstructionUnpivotInput{
						{ColumnID: "alpha_id", Key: authoringv2.TableScalar{Kind: authoringv2.TableScalarString, String: &alpha}},
						{ColumnID: "beta_id", Key: authoringv2.TableScalar{Kind: authoringv2.TableScalarString, String: &beta}},
					},
					KeyOutputColumnID: "measure_id", ValueOutputColumnID: "amount_output_id",
					NullRowPolicy: authoringv2.ConstructionUnpivotPreserve,
				}},
				Outputs: []authoringv2.StageColumn{
					{ID: "group_id", Name: "group", Label: "Group", Type: "string"},
					{ID: "total_id", Name: "total", Label: "Total", Type: "decimal"},
					{ID: "measure_id", Name: "measure", Label: "Measure", Type: "string"},
					{ID: "amount_output_id", Name: "amount_out", Label: "Amount", Type: "decimal"},
				},
			},
		},
	}
	if err := authored.Validate(authoredColumns); err != nil {
		t.Fatalf("validate authored construction: %v", err)
	}
	emitted := []explorer.EmittedColumn{
		{EmissionID: "emission-group", AuthoredColumns: []string{"group"}, PublicColumn: "resolved_group"},
		{EmissionID: "emission-category", AuthoredColumns: []string{"category"}, PublicColumn: "resolved_category"},
		{EmissionID: "emission-amount", AuthoredColumns: []string{"amount"}, PublicColumn: "resolved_amount"},
	}
	got, err := recipeConstruction(authored, authoredColumns, emitted)
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Validate(nil); err != nil {
		t.Fatalf("validate mapped recipe construction: %v", err)
	}
	wantSource := []recipe.StageColumn{
		{ID: "group_id", Name: "resolved_group", Label: "Group", Type: "string"},
		{ID: "category_id", Name: "resolved_category", Label: "Category", Type: "string"},
		{ID: "amount_id", Name: "resolved_amount", Label: "Amount", Type: "decimal"},
	}
	if !reflect.DeepEqual(got.SourceColumns, wantSource) {
		t.Fatalf("mapped source schema = %#v, want resolved public emissions %#v", got.SourceColumns, wantSource)
	}
	if len(got.Steps) != 4 || got.Steps[0].Operation.Pivot == nil || got.Steps[1].Operation.Derive == nil || got.Steps[2].Operation.Filter == nil || got.Steps[3].Operation.Unpivot == nil {
		t.Fatalf("mapped operation sequence = %#v", got.Steps)
	}
	if got.Steps[0].Operation.Pivot.Categories[0].Key.String == nil || *got.Steps[0].Operation.Pivot.Categories[0].Key.String != alpha {
		t.Fatalf("pivot scalar was not preserved: %#v", got.Steps[0].Operation.Pivot.Categories)
	}
	if got.Steps[1].Operation.Derive.Left.ColumnID != "alpha_id" || got.Steps[1].Operation.Derive.Right.ColumnID != "beta_id" {
		t.Fatalf("derive stable operands = %#v", got.Steps[1].Operation.Derive)
	}
	if got.Steps[2].Operation.Filter.Values[0].Decimal == nil || *got.Steps[2].Operation.Filter.Values[0].Decimal != minimum {
		t.Fatalf("filter scalar was not preserved: %#v", got.Steps[2].Operation.Filter)
	}
	if got.Steps[3].Operation.Unpivot.Inputs[1].ColumnID != "beta_id" || got.Steps[3].Operation.Unpivot.ValueOutputColumnID != "amount_output_id" {
		t.Fatalf("unpivot stable identities = %#v", got.Steps[3].Operation.Unpivot)
	}
}

func TestCompileSourceOnlyConstructionCarriesColumnIDsIntoStageDescriptors(t *testing.T) {
	document := exactRecodeDocument(nil)
	document.Columns[0].ColumnID = "status_id"
	document.Columns[1].ColumnID = "untouched_id"
	document.Construction = &authoringv2.Construction{Version: authoringv2.ConstructionVersion}

	compiled, err := Compile(context.Background(), "project", "explorer", document, fixtureSnapshotForProject("project"))
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Bundle.Outputs) != 1 || compiled.Bundle.Outputs[0].Construction == nil {
		t.Fatalf("recipe construction = %#v", compiled.Bundle.Outputs)
	}
	wantSource := []recipe.StageColumn{
		{ID: "status_id", Name: "status", Label: "Status", Type: "string"},
		{ID: "untouched_id", Name: "untouched", Label: "Untouched", Type: "string"},
	}
	if !reflect.DeepEqual(compiled.Bundle.Outputs[0].Construction.SourceColumns, wantSource) {
		t.Fatalf("source construction schema = %#v, want %#v", compiled.Bundle.Outputs[0].Construction.SourceColumns, wantSource)
	}
	plan, err := semantic.BuildRecipePlan(compiled.Bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation-a"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	lowered, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(lowered.Outputs) != 1 || len(lowered.Outputs[0].Stages) != 1 || lowered.Outputs[0].Stages[0].ID != recipe.ConstructionSourceProjectionID {
		t.Fatalf("source-only stage descriptors = %#v", lowered.Outputs)
	}
	columnsByID := map[string]string{}
	for _, column := range lowered.Outputs[0].Stages[0].Columns {
		columnsByID[column.ID] = column.Name
	}
	if columnsByID["status_id"] != "status" || columnsByID["untouched_id"] != "untouched" {
		t.Fatalf("compiled source stage column identities = %#v", columnsByID)
	}
}

func TestRecipeConstructionRejectsIndexedSourceFanout(t *testing.T) {
	columns := []authoringv2.Column{{
		ColumnID: "indexed_id", Column: "indexed", Label: "Indexed",
		Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{ProjectionMode: "INDEXED"}},
	}}
	emitted := []explorer.EmittedColumn{
		{EmissionID: "indexed-a", AuthoredColumns: []string{"indexed"}, PublicColumn: "indexed_x"},
		{EmissionID: "indexed-b", AuthoredColumns: []string{"indexed"}, PublicColumn: "indexed_y"},
	}
	_, err := recipeConstructionSourceColumns(columns, emitted)
	if err == nil || !strings.Contains(err.Error(), "INDEXED projection") {
		t.Fatalf("indexed source error = %v, want explicit fanout limitation", err)
	}
}
