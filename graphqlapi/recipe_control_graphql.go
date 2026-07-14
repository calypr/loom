package graphqlapi

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/calypr/loom/graphqlapi/model"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func requireRecipeControl(control RecipeControl) error {
	if control == nil {
		return fmt.Errorf("recipe control plane is not configured")
	}
	return nil
}

func recipeGraphQLError(err error) error {
	if err == nil {
		return nil
	}
	return GraphQLError(err, "")
}

func graphqlRecipeBindings(bindings *model.DataframeRecipeBindingsInput) (recipe.RuntimeBindings, error) {
	if bindings == nil {
		return recipe.RuntimeBindings{}, fmt.Errorf("recipe bindings are required")
	}
	value := recipeBindings(*bindings)
	if value.PreviewLimit < 0 {
		return recipe.RuntimeBindings{}, fmt.Errorf("preview limit must not be negative")
	}
	return value, nil
}

func outputValidation(output semantic.OutputPlan) *model.DataframeRecipeOutputValidation {
	fields := append([]string(nil), output.DeclaredOrder...)
	if len(fields) == 0 {
		for _, field := range output.Fields {
			fields = append(fields, field.Name)
		}
	}
	dynamic := make([]string, 0, len(output.DynamicMaps))
	for _, value := range output.DynamicMaps {
		dynamic = append(dynamic, value.Name)
	}
	return &model.DataframeRecipeOutputValidation{
		Name: output.Name, RootResourceType: output.RootResourceType,
		RowGrain: string(output.RowGrain), FieldNames: fields, DynamicColumns: dynamic,
	}
}

func expressionExplanation(value semantic.SemanticExpression) *model.DataframeRecipeExpressionExplanation {
	return &model.DataframeRecipeExpressionExplanation{
		SourcePath: value.SourcePath,
		Context:    value.Context,
		Kind:       string(value.Expression.Kind),
		ValueType:  string(value.Type.Kind),
		Repeated:   value.Type.Cardinality == "many",
		Nullable:   value.Type.Cardinality == "optional_one" || value.Type.Cardinality == "many",
	}
}

func planExplanation(plan semantic.RecipePlanExplanation, name string) *model.DataframeRecipeExplanation {
	outputs := make([]*model.DataframeRecipeOutputExplanation, 0, len(plan.Outputs))
	for _, output := range plan.Outputs {
		fields := make([]*model.DataframeRecipeExpressionExplanation, 0, len(output.Fields))
		for _, field := range output.Fields {
			fields = append(fields, &model.DataframeRecipeExpressionExplanation{
				SourcePath: field.SourcePath, Context: field.Context, Kind: string(field.Kind),
				ValueType: string(field.Type.Kind), Repeated: field.Type.Cardinality == "many",
				Nullable: field.Type.Cardinality == "optional_one" || field.Type.Cardinality == "many",
			})
		}
		var identity *model.DataframeRecipeExpressionExplanation
		if output.Identity != nil {
			identity = &model.DataframeRecipeExpressionExplanation{
				SourcePath: output.Identity.SourcePath, Context: output.Identity.Context, Kind: string(output.Identity.Kind),
				ValueType: string(output.Identity.Type.Kind), Repeated: output.Identity.Type.Cardinality == "many",
				Nullable: output.Identity.Type.Cardinality == "optional_one" || output.Identity.Type.Cardinality == "many",
			}
		}
		var expansion *model.DataframeRecipeExpansionExplanation
		if output.Expansion != nil {
			expansion = &model.DataframeRecipeExpansionExplanation{SourcePath: output.Expansion.SourcePath, Alias: output.Expansion.As}
		}
		outputs = append(outputs, &model.DataframeRecipeOutputExplanation{
			Name: output.Name, RootResourceType: output.Root, RowGrain: string(output.RowGrain),
			Fields: fields, Identity: identity, Expansion: expansion, DynamicMaps: append([]string(nil), output.DynamicMap...),
		})
	}
	return &model.DataframeRecipeExplanation{Name: name, RecipeDigest: plan.RecipeDigest, TranslationVersion: plan.TranslationVersion, Outputs: outputs}
}

func preflightResult(plan semantic.ResolvedRecipePlan, name string) *model.DataframeRecipePreflight {
	columns := make([]*model.DataframeRecipeColumn, 0)
	for _, output := range plan.SemanticPlan.Outputs {
		for _, field := range output.Fields {
			columns = append(columns, &model.DataframeRecipeColumn{
				Output: output.Name, Name: field.Name, LogicalType: string(field.Expr.Type.Kind),
				Repeated: field.Expr.Type.Cardinality == "many",
				Nullable: field.Expr.Type.Cardinality == "optional_one" || field.Expr.Type.Cardinality == "many",
			})
		}
	}
	keys := make([]string, 0, len(plan.ResolvedColumns))
	for key := range plan.ResolvedColumns {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, column := range plan.ResolvedColumns[key] {
			columns = append(columns, &model.DataframeRecipeColumn{
				Output: column.Output, DynamicName: column.DynamicName, Name: column.Column.Name,
				LogicalType: column.Column.ValueType,
			})
		}
	}
	return &model.DataframeRecipePreflight{
		Name: name, RecipeDigest: plan.SemanticPlan.RecipeDigest,
		ResolvedSchemaDigest: plan.ResolvedSchemaDigest, SourceGeneration: plan.SourceGeneration,
		ScopeDigest: plan.ScopeDigest, Columns: columns,
	}
}

func previewResult(preview recipePreviewView, name string) (*model.DataframeRecipePreview, error) {
	outputs := make([]*model.DataframeRecipePreviewOutput, 0, len(preview.rows))
	outputNames := make([]string, 0, len(preview.rows))
	for outputName := range preview.rows {
		outputNames = append(outputNames, outputName)
	}
	sort.Strings(outputNames)
	for _, outputName := range outputNames {
		rows := logicalPreviewRows(preview.rows[outputName])
		columns := previewColumns(preview.plan, outputName, rows)
		encoded, err := json.Marshal(rows)
		if err != nil {
			return nil, fmt.Errorf("marshal preview output %q: %w", outputName, err)
		}
		outputs = append(outputs, &model.DataframeRecipePreviewOutput{Name: outputName, Columns: columns, Rows: encoded, RowCount: len(rows)})
	}
	return &model.DataframeRecipePreview{
		Name: name, RecipeDigest: preview.plan.SemanticPlan.RecipeDigest,
		ResolvedSchemaDigest: preview.plan.ResolvedSchemaDigest, SourceGeneration: preview.plan.SourceGeneration,
		Outputs: outputs,
	}, nil
}

func logicalPreviewRows(rows []map[string]any) []map[string]any {
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		copy := make(map[string]any, len(row))
		for key, value := range row {
			if key == "__loom_row_id" {
				continue
			}
			copy[key] = value
		}
		result = append(result, copy)
	}
	return result
}

type recipePreviewView struct {
	plan semantic.ResolvedRecipePlan
	rows map[string][]map[string]any
}

func previewColumns(plan semantic.ResolvedRecipePlan, outputName string, rows []map[string]any) []string {
	for _, output := range plan.SemanticPlan.Outputs {
		if output.Name != outputName {
			continue
		}
		columns := append([]string(nil), output.DeclaredOrder...)
		if len(columns) == 0 {
			for _, field := range output.Fields {
				columns = append(columns, field.Name)
			}
		}
		seen := make(map[string]struct{}, len(columns))
		for _, column := range columns {
			seen[column] = struct{}{}
		}
		for _, resolved := range plan.ResolvedColumns {
			for _, column := range resolved {
				if column.Output == outputName {
					if _, ok := seen[column.Column.Name]; !ok {
						columns = append(columns, column.Column.Name)
						seen[column.Column.Name] = struct{}{}
					}
				}
			}
		}
		return columns
	}
	if len(rows) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	columns := make([]string, 0, len(rows[0]))
	for name := range rows[0] {
		seen[name] = struct{}{}
		columns = append(columns, name)
	}
	sort.Strings(columns)
	return columns
}

func executionModel(value RecipeExecution) *model.DataframeRecipeExecution {
	outputs := make([]*model.DataframeRecipeExecutionOutput, 0, len(value.Outputs))
	for _, output := range value.Outputs {
		var errorText *string
		if output.Error != "" {
			value := output.Error
			errorText = &value
		}
		outputs = append(outputs, &model.DataframeRecipeExecutionOutput{Name: output.Name, State: model.DataframeRecipeExecutionState(output.State), RowCount: output.RowCount, Error: errorText})
	}
	var errorText *string
	if value.Error != "" {
		copy := value.Error
		errorText = &copy
	}
	return &model.DataframeRecipeExecution{ID: value.ID, Name: value.Name, RecipeDigest: value.RecipeDigest, ResolvedSchemaDigest: value.ResolvedSchemaDigest, SourceGeneration: value.SourceGeneration, State: model.DataframeRecipeExecutionState(value.State), Outputs: outputs, Error: errorText}
}
