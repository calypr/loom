package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	irscope "github.com/calypr/loom/internal/dataframe/compiler/ir/scope"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	model "github.com/calypr/loom/internal/dataframe/compiler/model"

	clickhousecombine "github.com/calypr/loom/internal/dataframe/compiler/render/clickhouse"

	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/projectid"
	"github.com/google/uuid"
)

type ClickHouseQueryRows func(context.Context, string, []string, func(map[string]any) error, ...any) error

type ResolveClickHouseInputs func(context.Context, ir.PhysicalClickHouseCombine, recipe.RuntimeBindings) ([]ir.ResolvedClickHouseTable, error)

type WithExecutionReadPins func(context.Context, []string, func(context.Context) error) error

type preparedClickHouseQuery struct {
	Query         string
	Columns       []string
	Args          []any
	Identity      chartifact.Identity
	PhysicalPlan  ir.PhysicalPlan
	ScopeIdentity irscope.WholeRelationScopeIdentity
	ScopeEvidence irscope.WholeRelationScopeEvidence
}

type materializedClickHouseCapture struct {
	Artifact      *chartifact.Artifact
	Identity      ir.ClickHouseArtifactIdentity
	PhysicalPlan  ir.PhysicalPlan
	ScopeIdentity irscope.WholeRelationScopeIdentity
	ScopeEvidence irscope.WholeRelationScopeEvidence
}

func (e *Engine) clickHouseStreamForOutput(resolved Resolved, output lower.CompiledRecipeOutput, limit int, building map[string]bool, bindings recipe.RuntimeBindings) (OutputStream, model.CompiledQuery, error) {
	physicalPlan := output.Plan
	if output.OptimizedPlan != nil {
		physicalPlan = ir.ClonePhysicalPlan(*output.OptimizedPlan)
	} else {
		physicalPlan = ir.ClonePhysicalPlan(output.Plan)
	}
	if physicalPlan.ClickHouseCombine == nil {
		return OutputStream{}, model.CompiledQuery{}, fmt.Errorf("output %q has no typed ClickHouse combine", output.Name)
	}
	hasWorkspaceInputs := false
	for _, input := range physicalPlan.ClickHouseCombine.Inputs {
		if input.WorkspaceOutputID != "" {
			hasWorkspaceInputs = true
			break
		}
	}
	if hasWorkspaceInputs {
		if err := physicalPlan.ClickHouseCombine.ValidateWithWorkspaceArtifacts(); err != nil {
			return OutputStream{}, model.CompiledQuery{}, fmt.Errorf("output %q workspace ClickHouse plan: %w", output.Name, err)
		}
	} else if err := physicalPlan.Validate(); err != nil {
		return OutputStream{}, model.CompiledQuery{}, fmt.Errorf("output %q ClickHouse plan: %w", output.Name, err)
	}
	if len(output.DynamicColumns) != 0 {
		return OutputStream{}, model.CompiledQuery{}, fmt.Errorf("output %q ClickHouse combine does not support dynamic columns", output.Name)
	}
	combine := clonePhysicalClickHouseCombine(*physicalPlan.ClickHouseCombine)
	query := model.CompiledQuery{
		Project: bindings.Project, DatasetGeneration: bindings.DatasetGeneration,
		TranslationVersion: output.TranslationVersion, AuthResourcePaths: append([]string(nil), bindings.AuthResourcePaths...),
		PlanMode: "clickhouse", PlanProfile: "pinned_table_combine", RowIdentity: output.RowIdentity.Clone(),
		Columns: append([]string(nil), output.Columns...), PublicColumns: append([]string(nil), output.Columns...),
		OutputSchema: lower.CloneCompiledOutputSchema(output.OutputSchema), Limit: limit,
		PhysicalPlan: physicalPlan,
	}
	source := &clickHouseSource{
		clickHouseCombine: &combine, project: bindings.Project,
		clickHouseQueryRows: e.clickHouseQueryRows, resolveClickHouseInputs: e.resolveClickHouseInputs,
		withExecutionReadPins: e.withExecutionReadPins,
	}
	stream := OutputStream{
		Name: output.Name, Columns: append([]string(nil), output.Columns...), sourceIdentityMode: previewSourceIdentityMode(output, query.OutputSchema), RowIdentity: output.RowIdentity.Clone(),
		DynamicChecks: map[string]map[string]DynamicColumnCheck{},
		source:        source, bindings: bindings, queryLimit: limit,
		recipeDigest: resolved.StoredRecipeDigest,
		stageID:      compiledOutputFinalStageID(output), outputSchema: lower.CloneCompiledOutputSchema(output.OutputSchema), compiledOutput: true,
		physicalPlan: physicalPlan,
	}
	if limit == 0 {
		stream.planFingerprint = ir.ClickHouseCombinePlanDigest(combine)
	}
	if hasWorkspaceInputs {
		if e.privateClickHouseArtifacts == nil {
			return OutputStream{}, model.CompiledQuery{}, fmt.Errorf("output %q requires the private workspace artifact manager", output.Name)
		}
		dependencies := make(map[string]lower.CompiledRecipeOutput, len(resolved.Compiled.Outputs)+len(resolved.Compiled.WorkspaceDependencies))
		for _, dependency := range resolved.Compiled.Outputs {
			dependencies[dependency.Name] = dependency
		}
		for _, dependency := range resolved.Compiled.WorkspaceDependencies {
			dependencies[dependency.Name] = dependency
		}
		source.workspaceArtifactManager = e.privateClickHouseArtifacts
		for _, input := range combine.Inputs {
			if input.WorkspaceOutputID == "" {
				continue
			}
			dependency, ok := dependencies[input.WorkspaceOutputID]
			if !ok {
				return OutputStream{}, model.CompiledQuery{}, fmt.Errorf("output %q is missing compiled workspace dependency %q", output.Name, input.WorkspaceOutputID)
			}
			dependencyStream, _, err := e.streamExecutor.buildCompiledOutputStream(resolved, dependency, 0, building, true)
			if err != nil {
				return OutputStream{}, model.CompiledQuery{}, fmt.Errorf("output %q dependency %q: %w", output.Name, input.WorkspaceOutputID, err)
			}
			columns, err := chartifact.ColumnsFromCompiledOutput(dependency.OutputSchema)
			if err != nil {
				return OutputStream{}, model.CompiledQuery{}, fmt.Errorf("output %q dependency %q schema: %w", output.Name, input.WorkspaceOutputID, err)
			}
			source.workspaceArtifactSources = append(source.workspaceArtifactSources, PrivateClickHouseArtifactSource{
				OutputID: input.WorkspaceOutputID, Stream: dependencyStream, Columns: columns,
			})
		}
	}
	return stream, query, nil
}

func requiresRestrictedRowAuthPathCapture(output lower.CompiledRecipeOutput, bindings recipe.RuntimeBindings) bool {
	if outputUsesWholeRelationScope(output.Plan) {
		return false
	}
	if bindings.AuthScopeMode == authscope.ReadScopeRestricted || len(bindings.AuthResourcePaths) > 0 {
		return true
	}
	unrestricted, ok := output.Plan.BindVars["auth_resource_paths_unrestricted"].(bool)
	return ok && !unrestricted
}

func compiledArtifactScope(output lower.CompiledRecipeOutput, bindings recipe.RuntimeBindings, actualPlan ir.PhysicalPlan) (string, *irscope.WholeRelationScopeEvidence, *irscope.WholeRelationScopeIdentity, error) {
	if output.ScopeEvidence == nil {
		if output.ScopeEvidenceIdentity != nil {
			return "", nil, nil, fmt.Errorf("scope evidence identity exists without opaque compiler evidence")
		}
		return chartifact.ScopeModeRows, nil, nil, nil
	}
	if output.ScopeEvidenceIdentity == nil {
		return "", nil, nil, fmt.Errorf("compiler source evidence is missing its exact identity")
	}
	identity := *output.ScopeEvidenceIdentity
	if identity.OutputID != output.Name || projectid.Canonical(identity.Project) != projectid.Canonical(bindings.Project) || identity.DatasetGeneration != bindings.DatasetGeneration ||
		(bindings.AuthScopeMode != "" && identity.AuthScopeMode != string(bindings.AuthScopeMode)) ||
		!sameExecutionStrings(identity.AuthResourcePaths, bindings.AuthResourcePaths) {
		return "", nil, nil, fmt.Errorf("compiler source evidence identity differs from the exact output scope")
	}
	if !output.ScopeEvidence.Matches(output.Plan, identity) {
		return "", nil, nil, fmt.Errorf("compiler source evidence does not match its lowered physical plan")
	}
	if planHasExecutionWindow(actualPlan) {
		mode := chartifact.ScopeModeRows
		if outputUsesWholeRelationScope(output.Plan) {
			mode = chartifact.ScopeModeWhole
		}

		return mode, nil, nil, nil
	}
	optimizedEvidence, err := irscope.NewWholeRelationScopeEvidence(actualPlan, identity)
	if err != nil {
		return "", nil, nil, fmt.Errorf("issue evidence for the actual captured physical plan: %w", err)
	}
	if !optimizedEvidence.Matches(actualPlan, identity) {
		return "", nil, nil, fmt.Errorf("actual captured physical plan does not match compiler source evidence")
	}
	mode := chartifact.ScopeModeRows
	if outputUsesWholeRelationScope(output.Plan) {
		mode = chartifact.ScopeModeWhole
	}
	return mode, &optimizedEvidence, &identity, nil
}

func outputUsesWholeRelationScope(plan ir.PhysicalPlan) bool {
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalGroupRowsOp || operation.Kind == ir.PhysicalGroupedPivotOp {
			return true
		}
	}
	if plan.StageSequence != nil {
		for _, stage := range plan.StageSequence.Stages {
			switch stage.Kind {
			case ir.PhysicalStageGroupOp, ir.PhysicalStagePivotOp, ir.PhysicalStageCohortGroupOp, ir.PhysicalStageCodedGroupOp:
				return true
			}
		}
	}
	return false
}

func planHasExecutionWindow(plan ir.PhysicalPlan) bool {
	if plan.PreviewSourceWindowByRootID {
		return true
	}
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalLimitOp ||
			(operation.Kind == ir.PhysicalGroupRowsOp && operation.GroupRows != nil && operation.GroupRows.LimitBindKey != "") {
			return true
		}
	}
	if sequence := plan.StageSequence; sequence != nil {
		if sequence.PreviewLimitBindKey != "" || sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow {
			return true
		}
	}
	return false
}

func sameExecutionStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func streamExecutionAuthScopeMode(stream OutputStream) (authscope.ReadScopeMode, error) {
	if identity := stream.scopeEvidenceIdentity; identity != nil {
		if identity.Project != stream.bindings.Project || identity.DatasetGeneration != stream.bindings.DatasetGeneration ||
			!sameExecutionStrings(identity.AuthResourcePaths, stream.bindings.AuthResourcePaths) {
			return "", fmt.Errorf("compiler scope evidence differs from the stream's exact project, generation, or paths")
		}
		mode := authscope.ReadScopeMode(identity.AuthScopeMode)
		if mode != authscope.ReadScopeUnrestricted && mode != authscope.ReadScopeRestricted {
			return "", fmt.Errorf("compiler scope evidence has an unsupported authorization mode %q", identity.AuthScopeMode)
		}
		if stream.bindings.AuthScopeMode != "" && stream.bindings.AuthScopeMode != mode {
			return "", fmt.Errorf("explicit runtime authorization mode differs from compiler scope evidence")
		}
		return mode, nil
	}
	if stream.bindings.AuthScopeMode == authscope.ReadScopeUnrestricted || stream.bindings.AuthScopeMode == authscope.ReadScopeRestricted {
		return stream.bindings.AuthScopeMode, nil
	}
	if len(stream.bindings.AuthResourcePaths) != 0 {

		return authscope.ReadScopeRestricted, nil
	}
	if unrestricted, ok := stream.physicalPlan.BindVars["auth_resource_paths_unrestricted"].(bool); ok {
		if unrestricted {
			return authscope.ReadScopeUnrestricted, nil
		}
		return authscope.ReadScopeRestricted, nil
	}
	return "", fmt.Errorf("stream has no explicit or compiler-bound authorization mode")
}

func expectedArtifactIdentity(stream OutputStream, columns []chartifact.Column) (ir.ClickHouseArtifactIdentity, error) {
	if !stream.compiledOutput || stream.Name == "" || stream.stageID == "" || stream.recipeDigest == "" || stream.planFingerprint == "" {
		return ir.ClickHouseArtifactIdentity{}, fmt.Errorf("private workspace source is missing exact compiler output identity")
	}
	normalized, err := chartifact.NormalizeSchema(columns)
	if err != nil {
		return ir.ClickHouseArtifactIdentity{}, err
	}
	schemaDigest, err := chartifact.SchemaDigest(stream.stageID, normalized)
	if err != nil {
		return ir.ClickHouseArtifactIdentity{}, err
	}
	authMode, err := streamExecutionAuthScopeMode(stream)
	if err != nil {
		return ir.ClickHouseArtifactIdentity{}, err
	}
	identity := chartifact.Identity{
		OutputID: stream.Name, StageID: stream.stageID,
		Project: stream.bindings.Project, DatasetGeneration: stream.bindings.DatasetGeneration,
		RecipeDigest: stream.recipeDigest, PlanDigest: stream.planFingerprint, SchemaDigest: schemaDigest,
		AuthScopeMode: authMode, AuthResourcePaths: append([]string(nil), stream.bindings.AuthResourcePaths...),
		ScopeMode: stream.artifactScopeMode, ScopeEvidenceDigest: stream.artifactScopeEvidence,
	}
	identity.ScopeDigest = chartifact.ScopeDigest(identity)
	return clickHouseArtifactIdentityFromChartifact(identity), nil
}

func clickHouseArtifactIdentityFromChartifact(identity chartifact.Identity) ir.ClickHouseArtifactIdentity {
	return ir.ClickHouseArtifactIdentity{
		ExecutionID: identity.ExecutionID, OutputID: identity.OutputID, StageID: identity.StageID,
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration,
		RecipeDigest: identity.RecipeDigest, PlanDigest: identity.PlanDigest,
		SchemaDigest: identity.SchemaDigest, ScopeDigest: identity.ScopeDigest,
		AuthScopeMode: string(identity.AuthScopeMode), AuthResourcePaths: append([]string(nil), identity.AuthResourcePaths...),
		ScopeMode: identity.ScopeMode, ScopeEvidenceDigest: identity.ScopeEvidenceDigest,
	}
}

func chartifactIdentityFromIR(identity ir.ClickHouseArtifactIdentity) chartifact.Identity {
	return chartifact.Identity{
		ExecutionID: identity.ExecutionID, OutputID: identity.OutputID, StageID: identity.StageID,
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration,
		RecipeDigest: identity.RecipeDigest, PlanDigest: identity.PlanDigest,
		SchemaDigest: identity.SchemaDigest, ScopeDigest: identity.ScopeDigest,
		AuthScopeMode: authscope.ReadScopeMode(identity.AuthScopeMode), AuthResourcePaths: append([]string(nil), identity.AuthResourcePaths...),
		ScopeMode: identity.ScopeMode, ScopeEvidenceDigest: identity.ScopeEvidenceDigest,
	}
}

func clonePhysicalClickHouseCombine(plan ir.PhysicalClickHouseCombine) ir.PhysicalClickHouseCombine {
	plan.Inputs = append([]ir.PhysicalCombineInputRef(nil), plan.Inputs...)
	plan.Keys = append([]ir.PhysicalCombineKey(nil), plan.Keys...)
	plan.Projections = append([]ir.PhysicalCombineProjection(nil), plan.Projections...)
	plan.Outputs = append([]ir.PhysicalCombineOutputColumn(nil), plan.Outputs...)
	return plan
}

type clickHouseSource struct {
	clickHouseCombine        *ir.PhysicalClickHouseCombine
	project                  string
	clickHouseQueryRows      ClickHouseQueryRows
	resolveClickHouseInputs  ResolveClickHouseInputs
	withExecutionReadPins    WithExecutionReadPins
	workspaceArtifactManager *chartifact.Manager
	workspaceArtifactSources []PrivateClickHouseArtifactSource
}

func (*clickHouseSource) identityBinds() map[string]any {
	return nil
}

func (source *clickHouseSource) rows(ctx context.Context, output OutputStream, visit func(map[string]any) error) error {
	return source.withPreparedClickHouse(ctx, output, func(preparedCtx context.Context, prepared preparedClickHouseQuery) error {
		return source.clickHouseQueryRows(preparedCtx, prepared.Query, prepared.Columns, visit, prepared.Args...)
	})
}

func (source *clickHouseSource) withPreparedClickHouse(ctx context.Context, s OutputStream, consume func(context.Context, preparedClickHouseQuery) error) error {
	if ctx == nil || consume == nil || source.clickHouseCombine == nil || source.clickHouseQueryRows == nil {
		return fmt.Errorf("ClickHouse preparation requires context, typed plan, query executor, and consumer")
	}
	plan := clonePhysicalClickHouseCombine(*source.clickHouseCombine)
	workspaceInputCount := 0
	publishedPlan := plan
	publishedPlan.Inputs = make([]ir.PhysicalCombineInputRef, 0, len(plan.Inputs))
	revisions := make([]string, 0, len(plan.Inputs))
	for _, input := range plan.Inputs {
		if input.WorkspaceOutputID != "" {
			workspaceInputCount++
			continue
		}
		publishedPlan.Inputs = append(publishedPlan.Inputs, input)
		revisions = append(revisions, input.RevisionID)
	}
	if workspaceInputCount != len(source.workspaceArtifactSources) {
		return fmt.Errorf("workspace ClickHouse capture sources do not match the typed output references")
	}
	if workspaceInputCount > 0 {
		if !s.compiledOutput || source.workspaceArtifactManager == nil {
			return fmt.Errorf("workspace ClickHouse Combine requires a compiled output and private artifact manager")
		}
		if err := plan.ValidateWithWorkspaceArtifacts(); err != nil {
			return err
		}
	} else if s.compiledOutput {
		if err := plan.Validate(); err != nil {
			return err
		}
	}
	if len(revisions) > 0 && (source.resolveClickHouseInputs == nil || source.withExecutionReadPins == nil) {
		return fmt.Errorf("published ClickHouse inputs require exact input resolution and read pins")
	}
	if !s.compiledOutput && workspaceInputCount > 0 {
		return fmt.Errorf("uncompiled ClickHouse output cannot capture workspace dependencies")
	}

	prepare := func(preparedCtx context.Context, captured []PrivateClickHouseArtifactInput, published []ir.ResolvedClickHouseTable) error {
		if s.compiledOutput && s.captureExecutionID == "" && workspaceInputCount == 0 {
			rendered, err := clickhousecombine.RenderCombineWithLimit(plan, published, source.project, s.queryLimit)
			if err != nil {
				return err
			}
			return consume(preparedCtx, preparedClickHouseQuery{Query: rendered.Query, Columns: rendered.Columns, Args: rendered.Args})
		}
		if !s.compiledOutput {
			var rendered clickhousecombine.RenderedCombine
			var err error
			if workspaceInputCount > 0 {
				return fmt.Errorf("uncompiled workspace Combine is unsupported")
			}
			rendered, err = clickhousecombine.RenderCombineWithLimit(plan, published, source.project, s.queryLimit)
			if err != nil {
				return err
			}
			return consume(preparedCtx, preparedClickHouseQuery{Query: rendered.Query, Columns: rendered.Columns, Args: rendered.Args})
		}

		scopeIdentity, err := s.workspaceScopeIdentityForInputs(captured, published)
		if err != nil {
			return err
		}
		operands := make([]irscope.WorkspaceCombineScopeOperand, len(plan.Inputs))
		capturedByOutput := make(map[string]PrivateClickHouseArtifactInput, len(captured))
		for _, input := range captured {
			if _, exists := capturedByOutput[input.OutputID]; exists {
				return fmt.Errorf("workspace output %q was captured more than once", input.OutputID)
			}
			capturedByOutput[input.OutputID] = input
		}
		publishedIndex := 0
		wholeScope := false
		workspace := make([]clickhousecombine.WorkspaceArtifact, 0, len(captured))
		for inputIndex, ref := range plan.Inputs {
			if ref.WorkspaceOutputID != "" {
				input, ok := capturedByOutput[ref.WorkspaceOutputID]
				if !ok {
					return fmt.Errorf("workspace output %q was not captured", ref.WorkspaceOutputID)
				}
				operand, err := irscope.NewWorkspaceOutputScopeOperand(inputIndex, ref, input.SourcePlan, input.ScopeIdentity, input.ScopeEvidence)
				if err != nil {
					return fmt.Errorf("workspace output %q scope provenance: %w", ref.WorkspaceOutputID, err)
				}
				operands[inputIndex] = operand
				if input.ExpectedIdentity.ScopeMode == chartifact.ScopeModeWhole {
					wholeScope = true
				}
				manifest := input.Artifact.Manifest()
				columns := make([]ir.ResolvedClickHouseColumn, 0, len(manifest.Columns))
				for _, column := range manifest.Columns {
					columns = append(columns, ir.ResolvedClickHouseColumn{
						ID: column.ID, Name: column.Name, SemanticPath: column.SemanticPath,
						LogicalType: column.LogicalType, ClickHouseType: column.ClickHouseType,
						Nullable: column.Nullable, Repeated: column.Repeated,
					})
				}
				artifact := ir.ResolvedClickHousePrivateArtifact{
					ArtifactID: manifest.ArtifactID, Identity: clickHouseArtifactIdentityFromChartifact(manifest.Identity),
					PhysicalTable: manifest.PhysicalTable, Columns: columns,
				}
				workspace = append(workspace, clickhousecombine.WorkspaceArtifact{
					OutputID: input.OutputID, ExpectedIdentity: input.ExpectedIdentity, Artifact: artifact,
				})
				continue
			}
			if publishedIndex >= len(published) {
				return fmt.Errorf("resolved published ClickHouse inputs are incomplete")
			}
			resolved := published[publishedIndex]
			publishedIndex++
			operand, err := irscope.NewPublishedWorkspaceScopeOperand(inputIndex, ref, resolved, scopeIdentity)
			if err != nil {
				return fmt.Errorf("published output %q scope provenance: %w", ref.OutputID, err)
			}
			operands[inputIndex] = operand
			if resolved.ScopeMode == chartifact.ScopeModeWhole {
				wholeScope = true
			}
		}
		if publishedIndex != len(published) || len(capturedByOutput) != workspaceInputCount {
			return fmt.Errorf("workspace and published scope operands are unused or incomplete")
		}
		actualPlan := s.physicalPlan
		if actualPlan.Engine != ir.PhysicalEngineClickHouse || actualPlan.ClickHouseCombine == nil {
			actualPlan = ir.PhysicalPlan{Version: 1, Engine: ir.PhysicalEngineClickHouse, ClickHouseCombine: &plan}
		}
		evidence, err := irscope.NewWorkspaceCombineScopeEvidence(actualPlan, scopeIdentity, operands)
		if err != nil {
			return fmt.Errorf("workspace Combine scope evidence: %w", err)
		}
		mode := chartifact.ScopeModeRows
		if wholeScope {
			mode = chartifact.ScopeModeWhole
		}
		artifactStream := s
		artifactStream.artifactScopeMode = mode
		artifactStream.artifactScopeEvidence = ""
		artifactStream.scopeEvidenceIdentity = &scopeIdentity
		artifactStream.scopeEvidence = &evidence
		if mode == chartifact.ScopeModeWhole {
			artifactStream.artifactScopeEvidence = evidence.Digest()
		}
		var artifactIdentity chartifact.Identity
		if s.captureExecutionID != "" {
			artifactColumns, err := chartifact.ColumnsFromCompiledOutput(s.outputSchema)
			if err != nil {
				return err
			}
			identity, err := expectedArtifactIdentity(artifactStream, artifactColumns)
			if err != nil {
				return err
			}
			identity.ExecutionID = s.captureExecutionID
			artifactIdentity = chartifactIdentityFromIR(identity)
			artifactIdentity.ScopeDigest = chartifact.ScopeDigest(artifactIdentity)
		}
		var rendered clickhousecombine.RenderedCombine
		if workspaceInputCount > 0 {
			rendered, err = clickhousecombine.RenderWorkspaceClickHouseCombine(plan, workspace, published, source.project, s.bindings.DatasetGeneration, s.queryLimit)
		} else {
			rendered, err = clickhousecombine.RenderCombineWithLimit(plan, published, source.project, s.queryLimit)
		}
		if err != nil {
			return err
		}
		prepared := preparedClickHouseQuery{
			Query: rendered.Query, Columns: rendered.Columns, Args: rendered.Args, Identity: artifactIdentity,
			PhysicalPlan: actualPlan, ScopeIdentity: scopeIdentity, ScopeEvidence: evidence,
		}
		return consume(preparedCtx, prepared)
	}

	if workspaceInputCount > 0 {
		executionID := s.captureExecutionID
		if executionID == "" {
			executionID = uuid.NewString()
		}
		sources := append([]PrivateClickHouseArtifactSource(nil), source.workspaceArtifactSources...)
		return WithPrivateClickHouseArtifacts(ctx, source.workspaceArtifactManager, executionID, sources, revisions, source.withExecutionReadPins,
			func(pinnedCtx context.Context, captured []PrivateClickHouseArtifactInput) error {
				var published []ir.ResolvedClickHouseTable
				if len(revisions) > 0 {
					resolved, err := source.resolveClickHouseInputs(pinnedCtx, publishedPlan, s.bindings.Clone())
					if err != nil {
						return err
					}
					published = resolved
				}
				return prepare(pinnedCtx, captured, published)
			})
	}
	return source.withExecutionReadPins(ctx, revisions, func(pinnedCtx context.Context) error {
		published, err := source.resolveClickHouseInputs(pinnedCtx, publishedPlan, s.bindings.Clone())
		if err != nil {
			return err
		}
		return prepare(pinnedCtx, nil, published)
	})
}

func (s OutputStream) workspaceScopeIdentityForInputs(captured []PrivateClickHouseArtifactInput, published []ir.ResolvedClickHouseTable) (irscope.WholeRelationScopeIdentity, error) {
	if s.scopeEvidenceIdentity != nil || s.bindings.AuthScopeMode != "" || len(s.bindings.AuthResourcePaths) != 0 {
		return s.workspaceScopeIdentity()
	}
	if _, ok := s.physicalPlan.BindVars["auth_resource_paths_unrestricted"].(bool); ok {
		return s.workspaceScopeIdentity()
	} else if _, exists := s.physicalPlan.BindVars["auth_resource_paths_unrestricted"]; exists {
		return irscope.WholeRelationScopeIdentity{}, fmt.Errorf("workspace Combine has an invalid typed authorization mode")
	}
	var sourceProject, sourceGeneration, sourceMode string
	var sourcePaths []string
	if len(captured) > 0 {
		source := captured[0]
		if !source.ScopeEvidence.Matches(source.SourcePlan, source.ScopeIdentity) {
			return irscope.WholeRelationScopeIdentity{}, fmt.Errorf("workspace input %q has no exact compiler scope evidence", source.OutputID)
		}
		sourceProject, sourceGeneration, sourceMode = source.ScopeIdentity.Project, source.ScopeIdentity.DatasetGeneration, source.ScopeIdentity.AuthScopeMode
		sourcePaths = append([]string(nil), source.ScopeIdentity.AuthResourcePaths...)
	} else if len(published) > 0 {
		source := published[0]
		sourceProject, sourceGeneration = source.Project, source.DatasetGeneration
		sourcePaths = append([]string(nil), source.AuthResourcePaths...)
		if source.Unrestricted {
			sourceMode = string(authscope.ReadScopeUnrestricted)
		} else {
			sourceMode = string(authscope.ReadScopeRestricted)
		}
	} else {
		return irscope.WholeRelationScopeIdentity{}, fmt.Errorf("workspace Combine has no proven source from which to derive its empty-path scope")
	}
	if sourceProject != s.bindings.Project || sourceGeneration != s.bindings.DatasetGeneration || !sameExecutionStrings(sourcePaths, s.bindings.AuthResourcePaths) {
		return irscope.WholeRelationScopeIdentity{}, fmt.Errorf("workspace input scope differs from the exact output project, generation, or authorization paths")
	}
	if sourceMode != string(authscope.ReadScopeUnrestricted) && sourceMode != string(authscope.ReadScopeRestricted) {
		return irscope.WholeRelationScopeIdentity{}, fmt.Errorf("workspace input has unsupported authorization scope mode %q", sourceMode)
	}
	schemaDigest, err := compiledOutputSchemaDigest(s.outputSchema)
	if err != nil {
		return irscope.WholeRelationScopeIdentity{}, err
	}
	return irscope.WholeRelationScopeIdentity{
		OutputID: s.Name, Project: s.bindings.Project, DatasetGeneration: s.bindings.DatasetGeneration,
		AuthScopeMode: sourceMode, AuthResourcePaths: sourcePaths, SchemaDigest: schemaDigest,
	}, nil
}

func (s OutputStream) workspaceScopeIdentity() (irscope.WholeRelationScopeIdentity, error) {
	if s.scopeEvidenceIdentity != nil {
		identity := *s.scopeEvidenceIdentity
		if identity.OutputID != s.Name || identity.Project != s.bindings.Project || identity.DatasetGeneration != s.bindings.DatasetGeneration ||
			(s.bindings.AuthScopeMode != "" && identity.AuthScopeMode != string(s.bindings.AuthScopeMode)) ||
			!sameExecutionStrings(identity.AuthResourcePaths, s.bindings.AuthResourcePaths) {
			return irscope.WholeRelationScopeIdentity{}, fmt.Errorf("workspace Combine scope identity differs from its exact output bindings")
		}
		if _, err := streamExecutionAuthScopeMode(s); err != nil {
			return irscope.WholeRelationScopeIdentity{}, err
		}
		return identity, nil
	}
	mode, err := streamExecutionAuthScopeMode(s)
	if err != nil {
		return irscope.WholeRelationScopeIdentity{}, err
	}
	schemaDigest, err := compiledOutputSchemaDigest(s.outputSchema)
	if err != nil {
		return irscope.WholeRelationScopeIdentity{}, err
	}
	return irscope.WholeRelationScopeIdentity{
		OutputID: s.Name, Project: s.bindings.Project, DatasetGeneration: s.bindings.DatasetGeneration,
		AuthScopeMode: string(mode), AuthResourcePaths: append([]string(nil), s.bindings.AuthResourcePaths...),
		SchemaDigest: schemaDigest,
	}, nil
}

func compiledOutputSchemaDigest(schema []lower.CompiledOutputColumn) (string, error) {
	encoded, err := json.Marshal(schema)
	if err != nil {
		return "", fmt.Errorf("marshal finalized output schema: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
