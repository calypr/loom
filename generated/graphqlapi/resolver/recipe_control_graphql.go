package resolver

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/calypr/loom/generated/graphqlapi/model"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/control"
	recipeexec "github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/dataframe/semantic"
	materializationapi "github.com/calypr/loom/internal/graphqlapi/materialization"
)

func requireRecipeControl(control RecipeControl) error {
	if control == nil {
		return dataframeerrors.Wrap(nil, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	return nil
}

func recipeGraphQLError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := dataframeerrors.AsUserError(err); !ok {
		var validation *recipe.ValidationError
		switch {
		case errors.As(err, &validation):
			err = dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidRequest, "", dataframeerrors.WithFieldPath(validation.Path), dataframeerrors.WithDetails(map[string]any{"validationCode": validation.Code}))
		case errors.Is(err, recipeexec.ErrRecipeNotFound):
			err = dataframeerrors.Wrap(err, dataframeerrors.CodeRecipeNotFound, "")
		case errors.Is(err, authscope.ErrUnauthenticated):
			err = dataframeerrors.Wrap(err, dataframeerrors.CodeUnauthenticated, "")
		case errors.Is(err, authscope.ErrForbidden):
			err = dataframeerrors.Wrap(err, dataframeerrors.CodeUnauthorizedProject, "")
		case errors.Is(err, authscope.ErrAuthorizationBackendUnavailable):
			err = dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
		case errors.Is(err, context.Canceled):
			err = dataframeerrors.Wrap(err, dataframeerrors.CodeClientCanceled, "")
		case errors.Is(err, context.DeadlineExceeded):
			err = dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
		}
	}
	return err
}

func graphqlRecipeBindings(bindings *model.DataframeRecipeBindingsInput) (recipe.RuntimeBindings, error) {
	if bindings == nil {
		return recipe.RuntimeBindings{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	value := recipeBindings(*bindings)
	if value.PreviewLimit < 0 {
		return recipe.RuntimeBindings{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidLimit, "")
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
			Fields: fields, Identity: identity, Expansion: expansion, DynamicMaps: append([]string(nil), output.DynamicMap...), CatalogProjections: append([]string(nil), output.CatalogProjections...),
		})
	}
	return &model.DataframeRecipeExplanation{Name: name, RecipeDigest: plan.RecipeDigest, TranslationVersion: plan.TranslationVersion, Outputs: outputs}
}

func physicalExplanation(value control.PhysicalExplanation) *model.DataframeRecipePhysicalExplanation {
	outputs := make([]*model.DataframeRecipePhysicalOutputExplanation, 0, len(value.Outputs))
	for _, output := range value.Outputs {
		diagnostics := output.Diagnostics
		states := make([]*model.DataframeRecipeOptimizationRuleState, 0, len(diagnostics.OptimizationPolicy.RuleStates))
		for _, state := range diagnostics.OptimizationPolicy.RuleStates {
			states = append(states, &model.DataframeRecipeOptimizationRuleState{Rule: string(state.Rule), Enabled: state.Enabled, Reason: state.Reason})
		}
		decisions := make([]*model.DataframeRecipeOptimizationDecision, 0, len(diagnostics.OptimizationPolicy.Decisions))
		for _, decision := range diagnostics.OptimizationPolicy.Decisions {
			decisions = append(decisions, &model.DataframeRecipeOptimizationDecision{
				Rule: decision.Rule, Enabled: decision.Enabled, CandidateSets: decision.CandidateSets,
				EstimatedBaselineWork: decision.EstimatedBaselineWork, EstimatedOptimizedWork: decision.EstimatedOptimizedWork,
				EstimatedSavings: decision.EstimatedSavings, Reason: decision.Reason,
			})
		}
		optimization := &model.DataframeRecipeOptimizationExplanation{
			Policy: diagnostics.OptimizationPolicy.Policy, Enabled: diagnostics.OptimizationPolicy.Enabled,
			MinimumSavings: diagnostics.OptimizationPolicy.MinimumSavings, Rules: states, Decisions: decisions,
		}
		mapped := &model.DataframeRecipePhysicalOutputExplanation{
			Name: output.Name, PlanFingerprint: output.PlanFingerprint, Columns: append([]string(nil), output.Columns...),
			TraversalSets: diagnostics.TraversalSets, EndpointTraversalCount: diagnostics.EndpointTraversalCount,
			NativeTraversalCount: diagnostics.NativeTraversalCount, SharedTraversalCount: diagnostics.SharedTraversalCount,
			RequiredMatchReuseCount: diagnostics.RequiredMatchReuseCount, Optimization: optimization,
		}
		if output.Live != nil {
			mapped.Live = arangoAssessment(*output.Live)
		}
		outputs = append(outputs, mapped)
	}
	return &model.DataframeRecipePhysicalExplanation{Outputs: outputs}
}

func arangoAssessment(value control.ExplainAssessment) *model.DataframeRecipeArangoAssessment {
	result := &model.DataframeRecipeArangoAssessment{
		Plans:                 make([]*model.DataframeRecipeExplainPlanEstimate, 0, len(value.Plans)),
		FullCollectionScans:   make([]*model.DataframeRecipeExplainCollectionScan, 0, len(value.FullCollectionScans)),
		Indexes:               make([]*model.DataframeRecipeExplainIndexSummary, 0, len(value.Indexes)),
		Warnings:              make([]*model.DataframeRecipeExplainWarning, 0, len(value.Warnings)),
		AppliedOptimizerRules: append([]string(nil), value.AppliedOptimizerRules...),
	}
	for _, plan := range value.Plans {
		result.Plans = append(result.Plans, &model.DataframeRecipeExplainPlanEstimate{Plan: plan.Plan, EstimatedCost: plan.EstimatedCost, EstimatedNrItems: plan.EstimatedNrItems})
	}
	for _, scan := range value.FullCollectionScans {
		result.FullCollectionScans = append(result.FullCollectionScans, &model.DataframeRecipeExplainCollectionScan{Plan: scan.Plan, NodeID: int(scan.NodeID), Collection: scan.Collection})
	}
	for _, index := range value.Indexes {
		mapped := &model.DataframeRecipeExplainIndexSummary{Collection: index.Collection, ID: index.ID, Name: index.Name, Type: index.Type, Fields: append([]string(nil), index.Fields...), Uses: make([]*model.DataframeRecipeExplainIndexLocation, 0, len(index.Uses))}
		for _, use := range index.Uses {
			mapped.Uses = append(mapped.Uses, &model.DataframeRecipeExplainIndexLocation{Plan: use.Plan, NodeID: int(use.NodeID)})
		}
		result.Indexes = append(result.Indexes, mapped)
	}
	for _, warning := range value.Warnings {
		result.Warnings = append(result.Warnings, &model.DataframeRecipeExplainWarning{Code: warning.Code, Message: warning.Message})
	}
	return result
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
	if len(preview.outputs) == 0 {
		return nil, fmt.Errorf("compiler output schema is missing")
	}
	outputs := make([]*model.DataframeRecipePreviewOutput, 0, len(preview.outputs))
	for _, output := range preview.outputs {
		columns := append([]string(nil), output.Columns...)
		rows := logicalPreviewRows(columns, output.Rows)
		encoded, err := json.Marshal(rows)
		if err != nil {
			return nil, fmt.Errorf("marshal preview output %q: %w", output.Name, err)
		}
		csvValue, err := dataframeCSV(columns, rows)
		if err != nil {
			return nil, fmt.Errorf("marshal preview CSV output %q: %w", output.Name, err)
		}
		outputs = append(outputs, &model.DataframeRecipePreviewOutput{Name: output.Name, Columns: columns, Rows: encoded, CSV: &csvValue, RowCount: len(rows)})
	}
	return &model.DataframeRecipePreview{
		Name: name, RecipeDigest: preview.plan.SemanticPlan.RecipeDigest,
		ResolvedSchemaDigest: preview.plan.ResolvedSchemaDigest, SourceGeneration: preview.plan.SourceGeneration,
		Outputs: outputs,
	}, nil
}

func fullRecipeResult(result recipePreviewView, name string) (*model.DataframeRecipeResult, error) {
	if len(result.outputs) == 0 {
		return nil, fmt.Errorf("compiler output schema is missing")
	}
	outputs := make([]*model.DataframeRecipeResultOutput, 0, len(result.outputs))
	for _, output := range result.outputs {
		columns := append([]string(nil), output.Columns...)
		rows := logicalPreviewRows(columns, output.Rows)
		encoded, err := json.Marshal(rows)
		if err != nil {
			return nil, fmt.Errorf("marshal dataframe output %q: %w", output.Name, err)
		}
		csvValue, err := dataframeCSV(columns, rows)
		if err != nil {
			return nil, fmt.Errorf("marshal dataframe CSV output %q: %w", output.Name, err)
		}
		outputs = append(outputs, &model.DataframeRecipeResultOutput{Name: output.Name, Columns: columns, Rows: encoded, CSV: &csvValue, RowCount: len(rows)})
	}
	return &model.DataframeRecipeResult{
		Name: name, RecipeDigest: result.plan.SemanticPlan.RecipeDigest,
		ResolvedSchemaDigest: result.plan.ResolvedSchemaDigest, SourceGeneration: result.plan.SourceGeneration,
		Outputs: outputs,
	}, nil
}

// dataframeCSV serializes one logical output using the compiler-resolved
// column order. Repeated and object values are represented as JSON in one CSV
// cell so the flat export remains lossless.
func dataframeCSV(columns []string, rows []map[string]any) (string, error) {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := writer.Write(columns); err != nil {
		return "", err
	}
	for _, row := range rows {
		record := make([]string, len(columns))
		for index, column := range columns {
			value, ok := row[column]
			if !ok || value == nil {
				continue
			}
			switch typed := value.(type) {
			case string:
				record[index] = typed
			case bool:
				record[index] = strconv.FormatBool(typed)
			case int:
				record[index] = strconv.Itoa(typed)
			default:
				encoded, err := json.Marshal(typed)
				if err != nil {
					return "", err
				}
				record[index] = string(encoded)
			}
		}
		if err := writer.Write(record); err != nil {
			return "", err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

func logicalPreviewRows(columns []string, rows []map[string]any) []map[string]any {
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		copy := make(map[string]any, len(columns))
		for _, column := range columns {
			if value, ok := row[column]; ok {
				copy[column] = value
			}
		}
		result = append(result, copy)
	}
	return result
}

type recipePreviewView struct {
	plan    semantic.ResolvedRecipePlan
	outputs []control.OutputRows
}

func executionModel(value RecipeExecution) *model.DataframeRecipeExecution {
	outputs := make([]*model.DataframeRecipeExecutionOutput, 0, len(value.Outputs))
	for _, output := range value.Outputs {
		errorText, errorCode, errorRetryable := materializationapi.PersistedFailure(output.Error, output.ErrorCode, output.ErrorRetryable)
		outputs = append(outputs, &model.DataframeRecipeExecutionOutput{Name: output.Name, State: model.DataframeRecipeExecutionState(output.State), RowCount: output.RowCount, Error: errorText, ErrorCode: errorCode, ErrorRetryable: errorRetryable})
	}
	errorText, errorCode, errorRetryable := materializationapi.PersistedFailure(value.Error, value.ErrorCode, value.ErrorRetryable)
	return &model.DataframeRecipeExecution{ID: value.ID, Name: value.Name, RecipeDigest: value.RecipeDigest, ResolvedSchemaDigest: value.ResolvedSchemaDigest, SourceGeneration: value.SourceGeneration, State: model.DataframeRecipeExecutionState(value.State), Outputs: outputs, Error: errorText, ErrorCode: errorCode, ErrorRetryable: errorRetryable}
}
