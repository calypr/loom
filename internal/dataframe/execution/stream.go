package execution

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	model "github.com/calypr/loom/internal/dataframe/compiler/model"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

type streamExecutor struct {
	queryRows                 QueryRows
	batchSize                 int
	rootPageRows              int
	clickHouseStreamForOutput func(Resolved, lower.CompiledRecipeOutput, int, map[string]bool, recipe.RuntimeBindings) (OutputStream, model.CompiledQuery, error)
}

func (e streamExecutor) run(ctx context.Context, resolved Resolved) (map[string][]map[string]any, error) {
	streams, err := e.streamsWithLimit(ctx, resolved, 0)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]map[string]any, len(streams))
	for _, stream := range streams {
		rows := make([]map[string]any, 0)
		_, err := stream.Stream(ctx, func(row map[string]any) error {
			rows = append(rows, row)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("output %q: %w", stream.Name, err)
		}
		result[stream.Name] = rows
	}
	return result, nil
}

func (e streamExecutor) streamsWithLimit(_ context.Context, resolved Resolved, limit int) ([]OutputStream, error) {
	streams := make([]OutputStream, 0, len(resolved.Compiled.Outputs))
	selected := selectedOutputNames(resolved.Semantic.SemanticPlan.Bindings.OutputNames, resolved.Compiled.Outputs)
	for _, name := range resolved.Semantic.SemanticPlan.Bindings.OutputNames {
		if !selected[name] {
			return nil, dataframeerrors.Wrap(fmt.Errorf("requested recipe output %q was not found", name), dataframeerrors.CodeInvalidRequest, "", dataframeerrors.WithDetails(map[string]any{"output": name}))
		}
	}
	for _, output := range resolved.Compiled.Outputs {
		if selected != nil && !selected[output.Name] {
			continue
		}
		stream, _, err := e.streamForOutput(resolved, output.Name, limit)
		if err != nil {
			return nil, err
		}
		streams = append(streams, stream)
	}
	return streams, nil
}

func (e streamExecutor) streamForOutput(resolved Resolved, name string, limit int) (OutputStream, model.CompiledQuery, error) {
	for _, output := range resolved.Compiled.Outputs {
		if output.Name != name {
			continue
		}
		return e.streamForCompiledOutput(resolved, output, limit)
	}
	return OutputStream{}, model.CompiledQuery{}, previewAdmissionError(dataframeerrors.CodeInvalidRequest, "requested preview output is not available", map[string]any{"output": name})
}

func (e streamExecutor) streamForCompiledOutput(resolved Resolved, output lower.CompiledRecipeOutput, limit int) (OutputStream, model.CompiledQuery, error) {
	return e.buildCompiledOutputStream(resolved, output, limit, make(map[string]bool), false)
}

func (e streamExecutor) buildCompiledOutputStream(resolved Resolved, output lower.CompiledRecipeOutput, limit int, building map[string]bool, privateArtifactCapture bool) (OutputStream, model.CompiledQuery, error) {
	if building[output.Name] {
		return OutputStream{}, model.CompiledQuery{}, fmt.Errorf("workspace output dependency cycle includes %q", output.Name)
	}
	building[output.Name] = true
	defer delete(building, output.Name)
	bindings := resolved.Semantic.SemanticPlan.Bindings.Clone()
	if privateArtifactCapture && requiresRestrictedRowAuthPathCapture(output, bindings) {

		bindings.IncludeAuthResourcePath = true
	}
	switch output.Plan.Engine {
	case ir.PhysicalEngineClickHouse:
		return e.clickHouseStreamForOutput(resolved, output, limit, building, bindings)
	case "", ir.PhysicalEngineAQL:
	default:
		return OutputStream{}, model.CompiledQuery{}, fmt.Errorf("output %q has unsupported physical engine %q", output.Name, output.Plan.Engine)
	}
	query, err := compiler.CompileRecipeOutputWithPolicy(output, bindings, limit, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		return OutputStream{}, model.CompiledQuery{}, fmt.Errorf("output %q: %w", output.Name, err)
	}
	rootPageRows := e.rootPageRows
	if limit > 0 && limit < rootPageRows {
		rootPageRows = limit
	}
	source := &arangoSource{query: query.Query, bindVars: query.BindVars, stream: e.queryRows, batchSize: e.batchSize, rootPageRows: rootPageRows}
	stream := OutputStream{
		Name: output.Name, Columns: append([]string(nil), query.PublicColumns...), sourceIdentityMode: previewSourceIdentityMode(output, query.OutputSchema), RowIdentity: query.RowIdentity.Clone(),
		DynamicChecks: dynamicChecks(output.DynamicColumns), source: source,
		queryLimit: limit, recipeDigest: resolved.StoredRecipeDigest, planFingerprint: query.PlanDiagnostics.Fingerprint,
		bindings: bindings,
		stageID:  compiledOutputFinalStageID(output), outputSchema: lower.CloneCompiledOutputSchema(output.OutputSchema), compiledOutput: true,
		physicalPlan: query.PhysicalPlan,
	}
	scopeMode, scopeEvidence, scopeIdentity, err := compiledArtifactScope(output, bindings, query.PhysicalPlan)
	if err != nil {
		return OutputStream{}, model.CompiledQuery{}, fmt.Errorf("output %q artifact scope: %w", output.Name, err)
	}
	stream.artifactScopeMode, stream.scopeEvidence, stream.scopeEvidenceIdentity = scopeMode, scopeEvidence, scopeIdentity
	if scopeMode == chartifact.ScopeModeWhole && scopeEvidence != nil {
		stream.artifactScopeEvidence = scopeEvidence.Digest()
	}

	wholeInput := len(output.Plan.Operations) == 1 &&
		output.Plan.Operations[0].Kind == ir.PhysicalGroupRowsOp && output.Plan.Operations[0].GroupRows != nil
	for _, operation := range output.Plan.Operations {
		if operation.Kind == ir.PhysicalGroupedPivotOp {
			wholeInput = true
		}
	}
	for _, stage := range output.Stages {
		if stage.Operation == string(recipe.ConstructionGroupOp) || stage.Operation == string(recipe.ConstructionPivotOp) ||
			stage.Operation == string(recipe.ConstructionCodedGroupOp) {
			wholeInput = true
		}
	}
	if sequence := output.Plan.StageSequence; sequence != nil {
		for _, stage := range sequence.Stages {
			if stage.Kind == ir.PhysicalStageCohortGroupOp {
				wholeInput = true
				break
			}
		}
	}
	sampledAggregate := wholeInput && bindings.AllowSampledAggregatePreview
	source.sampledAggregate = sampledAggregate
	if rootPageRows > 0 && (!wholeInput || sampledAggregate) {
		pageBindings := resolved.Semantic.SemanticPlan.Bindings.Clone()
		pageBindings.PreviewLimit = limit
		page, pageErr := compiler.CompileRecipeOutputPageWithPolicy(output, pageBindings, rootPageRows, ir.DefaultPhysicalOptimizationPolicy())
		if pageErr != nil {
			return OutputStream{}, model.CompiledQuery{}, fmt.Errorf("output %q paging: %w", output.Name, pageErr)
		}
		source.page = &page
		query.PlanDiagnostics = page.RowsDiagnostics
		stream.planFingerprint = query.PlanDiagnostics.Fingerprint
	}
	return stream, query, nil
}

func compiledOutputFinalStageID(output lower.CompiledRecipeOutput) string {
	if output.Plan.StageSequence != nil && output.Plan.StageSequence.FinalStageID != "" {
		return output.Plan.StageSequence.FinalStageID
	}
	if len(output.Stages) > 0 && strings.TrimSpace(output.Stages[len(output.Stages)-1].ID) != "" {
		return output.Stages[len(output.Stages)-1].ID
	}
	return recipe.ConstructionSourceProjectionID
}
