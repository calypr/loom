package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/optimize"
	clickhousecombine "github.com/calypr/loom/internal/dataframe/compiler/render/clickhouse"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	"github.com/calypr/loom/internal/dataframe/recipe"

	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/projectid"
	"github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

type QueryRows func(context.Context, string, int, map[string]any, func(map[string]any) error) error

type WithPreviewReadSnapshot func(context.Context, func(context.Context, QueryRows) error) error

type ClickHouseQueryRows func(context.Context, string, []string, func(map[string]any) error, ...any) error

type ResolveClickHouseInputs func(context.Context, ir.PhysicalClickHouseCombine, recipe.RuntimeBindings) ([]ir.ResolvedClickHouseTable, error)

type WithExecutionReadPins func(context.Context, []string, func(context.Context) error) error

const (
	DefaultPreviewLimit = 25

	MaxPreviewLimit    = 1000
	previewPlanMode    = "physical"
	previewPlanProfile = "generic_fhir_graph_recipe"
)

type PreviewRequest struct {
	Output             string
	Limit              int
	IncludeRowIdentity bool
}

type PreviewSummary struct {
	Output           string
	Columns          []string
	RowCount         int
	PlanMode         string
	PlanProfile      string
	PlanFingerprint  string
	TraversalCount   int
	LoweringDuration time.Duration
	QueryDuration    time.Duration

	Complete  bool
	Truncated bool

	PartialValidation    bool
	RowLineageCapability compiler.RowLineageCapability
}

type Config struct {
	Registry                   recipe.Reader
	Revisions                  recipe.RevisionStore
	ResolveBundle              func(context.Context, recipe.Bundle, recipe.RuntimeBindings) (recipe.Bundle, error)
	QueryRows                  QueryRows
	PreviewQueryRows           QueryRows
	WithPreviewReadSnapshot    WithPreviewReadSnapshot
	PreparePreviewIndex        func(context.Context, compiler.PreviewCoveringIndexSpec) error
	PreviewCoveringIndexes     func(context.Context, string) ([]arango.PreviewCoveringIndex, error)
	PreviewCollectionRevision  func(context.Context, string) (string, error)
	PreviewExplainQuery        func(context.Context, string, map[string]any) (arango.ExplainResult, error)
	PreviewCollectionCount     func(context.Context, string) (int64, error)
	ClickHouseQueryRows        ClickHouseQueryRows
	ResolveClickHouseInputs    ResolveClickHouseInputs
	WithExecutionReadPins      WithExecutionReadPins
	PrivateClickHouseArtifacts *chartifact.Manager
	ScopeDigest                func(recipe.RuntimeBindings) string
	BatchSize                  int

	RootPageRows int
}

type Engine struct {
	registry                   recipe.Reader
	revisions                  recipe.RevisionStore
	resolveBundle              func(context.Context, recipe.Bundle, recipe.RuntimeBindings) (recipe.Bundle, error)
	queryRows                  QueryRows
	previewQueryRows           QueryRows
	withPreviewReadSnapshot    WithPreviewReadSnapshot
	preparePreviewIndex        func(context.Context, compiler.PreviewCoveringIndexSpec) error
	previewCoveringIndexes     func(context.Context, string) ([]arango.PreviewCoveringIndex, error)
	previewCollectionRevision  func(context.Context, string) (string, error)
	previewExplainQuery        func(context.Context, string, map[string]any) (arango.ExplainResult, error)
	previewCollectionCount     func(context.Context, string) (int64, error)
	groupPreviewRowsCache      groupPreviewRowsCache
	clickHouseQueryRows        ClickHouseQueryRows
	resolveClickHouseInputs    ResolveClickHouseInputs
	withExecutionReadPins      WithExecutionReadPins
	privateClickHouseArtifacts *chartifact.Manager
	scopeDigest                func(recipe.RuntimeBindings) string
	batchSize                  int
	rootPageRows               int
}

type Resolved struct {
	Bundle               recipe.Bundle
	Semantic             semantic.ResolvedRecipePlan
	Compiled             lower.CompiledRecipe
	StoredRecipeDigest   string
	ResolvedSchemaDigest string
}

type ResolutionError struct{ Err error }

func (e *ResolutionError) Error() string {
	if e == nil || e.Err == nil {
		return "recipe resolution failed"
	}
	return e.Err.Error()
}

func (e *ResolutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type OutputStream struct {
	Name                     string
	Columns                  []string
	sourceIdentityMode       string
	RowIdentity              *spec.RowIdentity
	DynamicChecks            map[string]map[string]DynamicColumnCheck
	query                    string
	bindVars                 map[string]any
	stream                   QueryRows
	batchSize                int
	rootPageRows             int
	page                     *compiler.CompiledOutputPage
	withPreviewReadSnapshot  WithPreviewReadSnapshot
	physicalEngine           ir.PhysicalEngine
	clickHouseCombine        *ir.PhysicalClickHouseCombine
	project                  string
	bindings                 recipe.RuntimeBindings
	clickHouseQueryRows      ClickHouseQueryRows
	resolveClickHouseInputs  ResolveClickHouseInputs
	withExecutionReadPins    WithExecutionReadPins
	queryLimit               int
	queryMemoryLimit         int64
	sampledAggregate         bool
	projectionDecisionKey    *groupPreviewRowsKey
	projectionDecision       *groupPreviewProjectionDecision
	recipeDigest             string
	planFingerprint          string
	stageID                  string
	outputSchema             []lower.CompiledOutputColumn
	compiledOutput           bool
	artifactScopeMode        string
	artifactScopeEvidence    string
	scopeEvidence            *ir.WholeRelationScopeEvidence
	scopeEvidenceIdentity    *ir.WholeRelationScopeIdentity
	workspaceArtifactManager *chartifact.Manager
	workspaceArtifactSources []PrivateClickHouseArtifactSource
	captureExecutionID       string
	physicalPlan             ir.PhysicalPlan
}

type preparedClickHouseQuery struct {
	Query         string
	Columns       []string
	Args          []any
	Identity      chartifact.Identity
	PhysicalPlan  ir.PhysicalPlan
	ScopeIdentity ir.WholeRelationScopeIdentity
	ScopeEvidence ir.WholeRelationScopeEvidence
}

type materializedClickHouseCapture struct {
	Artifact      *chartifact.Artifact
	Identity      ir.ClickHouseArtifactIdentity
	PhysicalPlan  ir.PhysicalPlan
	ScopeIdentity ir.WholeRelationScopeIdentity
	ScopeEvidence ir.WholeRelationScopeEvidence
}

type DynamicColumnCheck struct {
	ColumnName       string
	ValueType        string
	Many             bool
	AllowUnknownKeys bool
}

type StreamResult struct {
	Output   string
	Columns  []string
	RowCount int
}

func New(cfg Config) (*Engine, error) {
	if cfg.Registry == nil {
		return nil, fmt.Errorf("recipe registry is required")
	}
	if cfg.QueryRows == nil {
		return nil, fmt.Errorf("recipe query executor is required")
	}
	batch := cfg.BatchSize
	if batch <= 0 {
		batch = 1000
	}
	if cfg.RootPageRows < 0 {
		return nil, fmt.Errorf("recipe root page rows cannot be negative")
	}
	return &Engine{
		registry: cfg.Registry, revisions: cfg.Revisions, resolveBundle: cfg.ResolveBundle,
		queryRows: cfg.QueryRows, previewQueryRows: cfg.PreviewQueryRows, withPreviewReadSnapshot: cfg.WithPreviewReadSnapshot, preparePreviewIndex: cfg.PreparePreviewIndex,
		previewCoveringIndexes:    cfg.PreviewCoveringIndexes,
		previewCollectionRevision: cfg.PreviewCollectionRevision, previewExplainQuery: cfg.PreviewExplainQuery,
		previewCollectionCount: cfg.PreviewCollectionCount, clickHouseQueryRows: cfg.ClickHouseQueryRows,
		resolveClickHouseInputs: cfg.ResolveClickHouseInputs, withExecutionReadPins: cfg.WithExecutionReadPins,
		privateClickHouseArtifacts: cfg.PrivateClickHouseArtifacts,
		scopeDigest:                cfg.ScopeDigest, batchSize: batch, rootPageRows: cfg.RootPageRows,
	}, nil
}

func (e *Engine) entry(ctx context.Context, name string, bindings recipe.RuntimeBindings) (recipe.Entry, error) {
	if strings.TrimSpace(bindings.Project) == "" {
		return recipe.Entry{}, dataframeerrors.NewError(dataframeerrors.CodeProjectRequired, "")
	}
	if bindings.RecipeDigest != "" && e.revisions != nil {
		revision, err := e.revisions.Get(ctx, bindings.Project, name, bindings.RecipeDigest)
		if err != nil {
			return recipe.Entry{}, err
		}
		return recipe.Entry{Bundle: revision.Bundle, Digest: revision.Digest}, nil
	}
	return e.registry.LoadRecipe(ctx, name)
}

func (e *Engine) Resolve(ctx context.Context, name string, bindings recipe.RuntimeBindings) (Resolved, error) {
	entry, err := e.entry(ctx, name, bindings)
	if err != nil {
		return Resolved{}, err
	}
	return e.resolveEntry(ctx, entry, bindings)
}

func (e *Engine) ResolveVersion(ctx context.Context, name, translationVersion string, bindings recipe.RuntimeBindings) (Resolved, error) {
	entry, err := e.registry.LoadRecipeVersion(ctx, name, translationVersion)
	if err != nil {
		return Resolved{}, err
	}
	return e.resolveEntry(ctx, entry, bindings)
}

func (e *Engine) ResolveBundle(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (Resolved, error) {
	if strings.TrimSpace(bindings.Project) == "" {
		return Resolved{}, dataframeerrors.NewError(dataframeerrors.CodeProjectRequired, "")
	}
	digest, err := bundle.Digest()
	if err != nil {
		return Resolved{}, fmt.Errorf("digest bundle: %w", err)
	}
	return e.resolveEntry(ctx, recipe.Entry{Bundle: bundle, Digest: digest}, bindings)
}

func (e *Engine) CompileResolvedBundle(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (Resolved, error) {
	if strings.TrimSpace(bindings.Project) == "" {
		return Resolved{}, dataframeerrors.NewError(dataframeerrors.CodeProjectRequired, "")
	}
	digest, err := bundle.Digest()
	if err != nil {
		return Resolved{}, fmt.Errorf("digest resolved bundle: %w", err)
	}
	return e.compileResolvedBundle(ctx, bundle, bindings, digest)
}

func (e *Engine) PreviewBundle(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (map[string][]map[string]any, error) {
	resolved, err := e.ResolveBundle(ctx, bundle, bindings)
	if err != nil {
		return nil, &ResolutionError{Err: err}
	}
	return e.Preview(ctx, resolved, bindings.PreviewLimit)
}

func (e *Engine) PreviewResolvedBundle(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (map[string][]map[string]any, error) {
	resolved, err := e.CompileResolvedBundle(ctx, bundle, bindings)
	if err != nil {
		return nil, &ResolutionError{Err: err}
	}
	return e.Preview(ctx, resolved, bindings.PreviewLimit)
}

func (e *Engine) MaterializeBundle(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings, publish func(context.Context, Resolved) error) (Resolved, error) {
	resolved, err := e.ResolveBundle(ctx, bundle, bindings)
	if err != nil {
		return Resolved{}, &ResolutionError{Err: err}
	}
	if publish != nil {
		if err := publish(ctx, resolved); err != nil {
			return Resolved{}, err
		}
	}
	return resolved, nil
}

func (e *Engine) MaterializeResolvedBundle(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings, publish func(context.Context, Resolved) error) (Resolved, error) {
	resolved, err := e.CompileResolvedBundle(ctx, bundle, bindings)
	if err != nil {
		return Resolved{}, &ResolutionError{Err: err}
	}
	if publish != nil {
		if err := publish(ctx, resolved); err != nil {
			return Resolved{}, err
		}
	}
	return resolved, nil
}

func (e *Engine) resolveEntry(ctx context.Context, entry recipe.Entry, bindings recipe.RuntimeBindings) (Resolved, error) {
	if strings.TrimSpace(bindings.Project) == "" {
		return Resolved{}, dataframeerrors.NewError(dataframeerrors.CodeProjectRequired, "")
	}
	bundle := entry.Bundle
	storedRecipeDigest, err := bundle.Digest()
	if err != nil {
		return Resolved{}, fmt.Errorf("digest stored recipe: %w", err)
	}
	if e.resolveBundle != nil {
		bundle, err = e.resolveBundle(ctx, bundle, bindings)
		if err != nil {
			return Resolved{}, fmt.Errorf("resolve recipe schema: %w", err)
		}
	}
	return e.compileResolvedBundle(ctx, bundle, bindings, storedRecipeDigest)
}

func (e *Engine) compileResolvedBundle(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings, storedRecipeDigest string) (Resolved, error) {
	if strings.TrimSpace(bindings.Project) == "" {
		return Resolved{}, dataframeerrors.NewError(dataframeerrors.CodeProjectRequired, "")
	}
	if strings.TrimSpace(storedRecipeDigest) == "" {
		var err error
		storedRecipeDigest, err = bundle.Digest()
		if err != nil {
			return Resolved{}, fmt.Errorf("digest resolved bundle: %w", err)
		}
	}
	semanticPlan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		return Resolved{}, err
	}

	semanticPlan.RecipeDigest = storedRecipeDigest
	scope := ""
	if e.scopeDigest != nil {
		scope = e.scopeDigest(bindings)
	}
	resolved, err := semantic.ResolveRecipePlan(semanticPlan, scope, bindings.DatasetGeneration)
	if err != nil {
		return Resolved{}, err
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		return Resolved{}, err
	}

	policy := ir.DefaultPhysicalOptimizationPolicy()
	for index := range compiled.Outputs {
		plan := compiled.Outputs[index].Plan
		if plan.Engine == ir.PhysicalEngineClickHouse {
			workspaceInputs := false
			if plan.ClickHouseCombine != nil {
				for _, input := range plan.ClickHouseCombine.Inputs {
					workspaceInputs = workspaceInputs || input.WorkspaceOutputID != ""
				}
			}
			var validateErr error
			if workspaceInputs {
				validateErr = plan.ClickHouseCombine.ValidateWithWorkspaceArtifacts()
			} else {
				validateErr = plan.Validate()
			}
			if validateErr != nil {
				return Resolved{}, fmt.Errorf("validate output %q: %w", compiled.Outputs[index].Name, validateErr)
			}
			optimized := ir.ClonePhysicalPlan(plan)
			compiled.Outputs[index].OptimizedPlan = &optimized
			continue
		}
		optimized, optimizeErr := optimize.OptimizePhysicalPlanWithPolicy(plan, policy)
		if optimizeErr != nil {
			return Resolved{}, fmt.Errorf("optimize output %q: %w", compiled.Outputs[index].Name, optimizeErr)
		}
		compiled.Outputs[index].OptimizedPlan = &optimized
	}
	resolvedSchemaDigest, err := resolvedBundleSchemaDigest(storedRecipeDigest, bundle, bindings)
	if err != nil {
		return Resolved{}, err
	}
	resolved.ResolvedSchemaDigest = resolvedSchemaDigest
	return Resolved{Bundle: bundle, Semantic: resolved, Compiled: compiled, StoredRecipeDigest: storedRecipeDigest, ResolvedSchemaDigest: resolvedSchemaDigest}, nil
}

func resolvedBundleSchemaDigest(storedDigest string, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (string, error) {
	resolvedDigest, err := bundle.Digest()
	if err != nil {
		return "", err
	}
	paths := append([]string(nil), bindings.AuthResourcePaths...)
	sort.Strings(paths)
	payload, err := json.Marshal(struct {
		Stored, Resolved, Project, Generation, ScopeMode string
		Paths                                            []string
	}{storedDigest, resolvedDigest, bindings.Project, bindings.DatasetGeneration, string(bindings.AuthScopeMode), paths})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func (e *Engine) Materialize(ctx context.Context, name string, bindings recipe.RuntimeBindings, publish func(context.Context, Resolved) error) (Resolved, error) {
	resolved, err := e.Resolve(ctx, name, bindings)
	if err != nil {
		return Resolved{}, &ResolutionError{Err: err}
	}
	if publish != nil {
		if err := publish(ctx, resolved); err != nil {
			return Resolved{}, err
		}
	}
	return resolved, nil
}

func (e *Engine) MaterializeVersion(ctx context.Context, name, translationVersion string, bindings recipe.RuntimeBindings, publish func(context.Context, Resolved) error) (Resolved, error) {
	resolved, err := e.ResolveVersion(ctx, name, translationVersion, bindings)
	if err != nil {
		return Resolved{}, &ResolutionError{Err: err}
	}
	if publish != nil {
		if err := publish(ctx, resolved); err != nil {
			return Resolved{}, err
		}
	}
	return resolved, nil
}

func (e *Engine) clickHouseStreamForOutput(resolved Resolved, output lower.CompiledRecipeOutput, limit int, building map[string]bool, bindings recipe.RuntimeBindings) (OutputStream, compiler.CompiledQuery, error) {
	physicalPlan := output.Plan
	if output.OptimizedPlan != nil {
		physicalPlan = ir.ClonePhysicalPlan(*output.OptimizedPlan)
	} else {
		physicalPlan = ir.ClonePhysicalPlan(output.Plan)
	}
	if physicalPlan.ClickHouseCombine == nil {
		return OutputStream{}, compiler.CompiledQuery{}, fmt.Errorf("output %q has no typed ClickHouse combine", output.Name)
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
			return OutputStream{}, compiler.CompiledQuery{}, fmt.Errorf("output %q workspace ClickHouse plan: %w", output.Name, err)
		}
	} else if err := physicalPlan.Validate(); err != nil {
		return OutputStream{}, compiler.CompiledQuery{}, fmt.Errorf("output %q ClickHouse plan: %w", output.Name, err)
	}
	if len(output.DynamicColumns) != 0 {
		return OutputStream{}, compiler.CompiledQuery{}, fmt.Errorf("output %q ClickHouse combine does not support dynamic columns", output.Name)
	}
	combine := clonePhysicalClickHouseCombine(*physicalPlan.ClickHouseCombine)
	query := compiler.CompiledQuery{
		Project: bindings.Project, DatasetGeneration: bindings.DatasetGeneration,
		TranslationVersion: output.TranslationVersion, AuthResourcePaths: append([]string(nil), bindings.AuthResourcePaths...),
		PlanMode: "clickhouse", PlanProfile: "pinned_table_combine", RowIdentity: output.RowIdentity.Clone(),
		Columns: append([]string(nil), output.Columns...), PublicColumns: append([]string(nil), output.Columns...),
		OutputSchema: lower.CloneCompiledOutputSchema(output.OutputSchema), Limit: limit,
		PlanDiagnostics: ir.CompilerPlanDiagnostics{Fingerprint: clickHouseCombineFingerprint(combine)},
	}
	stream := OutputStream{
		Name: output.Name, Columns: append([]string(nil), output.Columns...), sourceIdentityMode: previewSourceIdentityMode(output, query.OutputSchema), RowIdentity: output.RowIdentity.Clone(),
		DynamicChecks: map[string]map[string]DynamicColumnCheck{}, physicalEngine: ir.PhysicalEngineClickHouse,
		clickHouseCombine: &combine, project: bindings.Project, bindings: bindings, queryLimit: limit,
		clickHouseQueryRows: e.clickHouseQueryRows, resolveClickHouseInputs: e.resolveClickHouseInputs,
		withExecutionReadPins: e.withExecutionReadPins,
		recipeDigest:          resolved.StoredRecipeDigest, planFingerprint: query.PlanDiagnostics.Fingerprint,
		stageID: compiledOutputFinalStageID(output), outputSchema: lower.CloneCompiledOutputSchema(output.OutputSchema), compiledOutput: true,
		physicalPlan: physicalPlan,
	}
	if hasWorkspaceInputs {
		if e.privateClickHouseArtifacts == nil {
			return OutputStream{}, compiler.CompiledQuery{}, fmt.Errorf("output %q requires the private workspace artifact manager", output.Name)
		}
		dependencies := make(map[string]lower.CompiledRecipeOutput, len(resolved.Compiled.Outputs)+len(resolved.Compiled.WorkspaceDependencies))
		for _, dependency := range resolved.Compiled.Outputs {
			dependencies[dependency.Name] = dependency
		}
		for _, dependency := range resolved.Compiled.WorkspaceDependencies {
			dependencies[dependency.Name] = dependency
		}
		stream.workspaceArtifactManager = e.privateClickHouseArtifacts
		for _, input := range combine.Inputs {
			if input.WorkspaceOutputID == "" {
				continue
			}
			dependency, ok := dependencies[input.WorkspaceOutputID]
			if !ok {
				return OutputStream{}, compiler.CompiledQuery{}, fmt.Errorf("output %q is missing compiled workspace dependency %q", output.Name, input.WorkspaceOutputID)
			}
			dependencyStream, _, err := e.outputStreams().buildCompiledOutputStream(resolved, dependency, 0, building, true)
			if err != nil {
				return OutputStream{}, compiler.CompiledQuery{}, fmt.Errorf("output %q dependency %q: %w", output.Name, input.WorkspaceOutputID, err)
			}
			columns, err := chartifact.ColumnsFromCompiledOutput(dependency.OutputSchema)
			if err != nil {
				return OutputStream{}, compiler.CompiledQuery{}, fmt.Errorf("output %q dependency %q schema: %w", output.Name, input.WorkspaceOutputID, err)
			}
			stream.workspaceArtifactSources = append(stream.workspaceArtifactSources, PrivateClickHouseArtifactSource{
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

func compiledArtifactScope(output lower.CompiledRecipeOutput, bindings recipe.RuntimeBindings, actualPlan ir.PhysicalPlan) (string, *ir.WholeRelationScopeEvidence, *ir.WholeRelationScopeIdentity, error) {
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
	optimizedEvidence, err := ir.NewWholeRelationScopeEvidence(actualPlan, identity)
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

func clickHouseCombineFingerprint(plan ir.PhysicalClickHouseCombine) string {
	payload, err := json.Marshal(plan)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func clonePhysicalClickHouseCombine(plan ir.PhysicalClickHouseCombine) ir.PhysicalClickHouseCombine {
	plan.Inputs = append([]ir.PhysicalCombineInputRef(nil), plan.Inputs...)
	plan.Keys = append([]ir.PhysicalCombineKey(nil), plan.Keys...)
	plan.Projections = append([]ir.PhysicalCombineProjection(nil), plan.Projections...)
	plan.Outputs = append([]ir.PhysicalCombineOutputColumn(nil), plan.Outputs...)
	return plan
}

func selectedOutputNames(names []string, outputs []lower.CompiledRecipeOutput) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	known := make(map[string]bool, len(outputs))
	for _, output := range outputs {
		known[output.Name] = false
	}
	for _, name := range names {
		if _, ok := known[name]; ok {
			known[name] = true
		}
	}
	return known
}

func publicPreviewRow(row map[string]any, columns []string, includeRowIdentity bool) map[string]any {
	public := make(map[string]any, len(columns))
	for _, column := range columns {
		if value, ok := row[column]; ok {
			public[column] = value
		}
	}
	if includeRowIdentity {
		if value, ok := row["__loom_row_id"]; ok {
			public["__loom_row_id"] = value
		}
	}
	return public
}

var errPreviewLimit = fmt.Errorf("preview limit reached")

func (s OutputStream) Stream(ctx context.Context, visit func(map[string]any) error) (StreamResult, error) {
	if visit == nil {
		return StreamResult{}, fmt.Errorf("row visitor is required")
	}
	count := 0
	err := s.streamRaw(ctx, func(row map[string]any) error {
		resolved, err := materializePostQueryRowWithChecks(row, s.DynamicChecks)
		if err != nil {
			return err
		}
		if err := ensureStableRowIdentity(resolved, s.RowIdentity, s.bindVars); err != nil {
			return err
		}
		resolved = publicStreamRow(resolved, s.Columns)
		count++
		return visit(resolved)
	})
	return StreamResult{Output: s.Name, Columns: append([]string(nil), s.Columns...), RowCount: count}, err
}

func (s OutputStream) streamPreparedClickHouse(ctx context.Context, prepared preparedClickHouseQuery, visit func(map[string]any) error) (StreamResult, error) {
	if ctx == nil || visit == nil || s.clickHouseQueryRows == nil || strings.TrimSpace(prepared.Query) == "" {
		return StreamResult{}, fmt.Errorf("prepared ClickHouse stream requires context, query executor, query, and visitor")
	}
	count := 0
	err := s.clickHouseQueryRows(ctx, prepared.Query, prepared.Columns, func(row map[string]any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		resolved, err := materializePostQueryRowWithChecks(row, s.DynamicChecks)
		if err != nil {
			return err
		}
		if err := ensureStableRowIdentity(resolved, s.RowIdentity, s.bindVars); err != nil {
			return err
		}
		resolved = publicStreamRow(resolved, s.Columns)
		count++
		return visit(resolved)
	}, prepared.Args...)
	return StreamResult{Output: s.Name, Columns: append([]string(nil), s.Columns...), RowCount: count}, err
}

func (s OutputStream) streamRaw(ctx context.Context, visit func(map[string]any) error) error {
	if s.queryMemoryLimit > 0 {
		ctx = arango.WithQueryMemoryLimit(ctx, s.queryMemoryLimit)
	}
	if s.physicalEngine == ir.PhysicalEngineClickHouse {
		return s.withPreparedClickHouse(ctx, func(preparedCtx context.Context, prepared preparedClickHouseQuery) error {
			return s.clickHouseQueryRows(preparedCtx, prepared.Query, prepared.Columns, visit, prepared.Args...)
		})
	}
	if s.page == nil || s.rootPageRows == 0 {
		return s.stream(ctx, s.query, s.batchSize, s.bindVars, visit)
	}
	if len(s.page.RowsPartitions) > 0 && s.withPreviewReadSnapshot != nil {
		return s.withPreviewReadSnapshot(ctx, func(snapshotCtx context.Context, query QueryRows) error {
			return s.streamRootPages(snapshotCtx, query, true, visit)
		})
	}
	return s.streamRootPages(ctx, s.stream, false, visit)
}

func (s OutputStream) streamRootPages(ctx context.Context, query QueryRows, partitioned bool, visit func(map[string]any) error) error {
	after := ""
	pageRows := s.rootPageRows
	for {
		keys := make([]string, 0, pageRows)
		keyBinds := cloneBindVars(s.page.RootKeysBindVars)
		keyBinds[compiler.RootPageAfterKeyBind] = after
		keyBinds[compiler.RootPageSizeBind] = pageRows
		if err := query(ctx, s.page.RootKeysQuery, s.batchSize, keyBinds, func(row map[string]any) error {
			key, ok := row["_key"].(string)
			if !ok || key == "" {
				return fmt.Errorf("root-key page returned an invalid _key")
			}
			if len(keys) != 0 && key <= keys[len(keys)-1] {
				return fmt.Errorf("root-key page is not strictly ordered")
			}
			keys = append(keys, key)
			return nil
		}); err != nil {
			return err
		}
		if len(keys) == 0 {
			return nil
		}
		if partitioned {
			rows, err := previewProjectionRows(ctx, query, s.page.RowsPartitions, keys, s.batchSize)
			if err != nil {
				return err
			}
			for _, row := range rows {
				if err := visit(row); err != nil {
					return err
				}
			}
		} else {
			rowBinds := cloneBindVars(s.page.RowsBindVars)
			rowBinds[compiler.RootPageKeysBind] = keys
			if err := query(ctx, s.page.RowsQuery, s.batchSize, rowBinds, visit); err != nil {
				return err
			}
		}
		if s.sampledAggregate {
			return nil
		}
		after = keys[len(keys)-1]
		if len(keys) < pageRows {
			return nil
		}
	}
}

func (s OutputStream) withPreparedClickHouse(ctx context.Context, consume func(context.Context, preparedClickHouseQuery) error) error {
	if ctx == nil || consume == nil || s.clickHouseCombine == nil || s.clickHouseQueryRows == nil {
		return fmt.Errorf("ClickHouse preparation requires context, typed plan, query executor, and consumer")
	}
	plan := clonePhysicalClickHouseCombine(*s.clickHouseCombine)
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
	if workspaceInputCount != len(s.workspaceArtifactSources) {
		return fmt.Errorf("workspace ClickHouse capture sources do not match the typed output references")
	}
	if workspaceInputCount > 0 {
		if !s.compiledOutput || s.workspaceArtifactManager == nil {
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
	if len(revisions) > 0 && (s.resolveClickHouseInputs == nil || s.withExecutionReadPins == nil) {
		return fmt.Errorf("published ClickHouse inputs require exact input resolution and read pins")
	}
	if !s.compiledOutput && workspaceInputCount > 0 {
		return fmt.Errorf("uncompiled ClickHouse output cannot capture workspace dependencies")
	}

	prepare := func(preparedCtx context.Context, captured []PrivateClickHouseArtifactInput, published []ir.ResolvedClickHouseTable) error {
		if s.compiledOutput && s.captureExecutionID == "" && workspaceInputCount == 0 {
			rendered, err := clickhousecombine.RenderCombineWithLimit(plan, published, s.project, s.queryLimit)
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
			rendered, err = clickhousecombine.RenderCombineWithLimit(plan, published, s.project, s.queryLimit)
			if err != nil {
				return err
			}
			return consume(preparedCtx, preparedClickHouseQuery{Query: rendered.Query, Columns: rendered.Columns, Args: rendered.Args})
		}

		scopeIdentity, err := s.workspaceScopeIdentityForInputs(captured, published)
		if err != nil {
			return err
		}
		operands := make([]ir.WorkspaceCombineScopeOperand, len(plan.Inputs))
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
				operand, err := ir.NewWorkspaceOutputScopeOperand(inputIndex, ref, input.SourcePlan, input.ScopeIdentity, input.ScopeEvidence)
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
			operand, err := ir.NewPublishedWorkspaceScopeOperand(inputIndex, ref, resolved, scopeIdentity)
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
		evidence, err := ir.NewWorkspaceCombineScopeEvidence(actualPlan, scopeIdentity, operands)
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
			rendered, err = clickhousecombine.RenderWorkspaceClickHouseCombine(plan, workspace, published, s.project, s.bindings.DatasetGeneration, s.queryLimit)
		} else {
			rendered, err = clickhousecombine.RenderCombineWithLimit(plan, published, s.project, s.queryLimit)
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
		sources := append([]PrivateClickHouseArtifactSource(nil), s.workspaceArtifactSources...)
		return WithPrivateClickHouseArtifacts(ctx, s.workspaceArtifactManager, executionID, sources, revisions, s.withExecutionReadPins,
			func(pinnedCtx context.Context, captured []PrivateClickHouseArtifactInput) error {
				var published []ir.ResolvedClickHouseTable
				if len(revisions) > 0 {
					resolved, err := s.resolveClickHouseInputs(pinnedCtx, publishedPlan, s.bindings.Clone())
					if err != nil {
						return err
					}
					published = resolved
				}
				return prepare(pinnedCtx, captured, published)
			})
	}
	return s.withExecutionReadPins(ctx, revisions, func(pinnedCtx context.Context) error {
		published, err := s.resolveClickHouseInputs(pinnedCtx, publishedPlan, s.bindings.Clone())
		if err != nil {
			return err
		}
		return prepare(pinnedCtx, nil, published)
	})
}

func (s OutputStream) workspaceScopeIdentityForInputs(captured []PrivateClickHouseArtifactInput, published []ir.ResolvedClickHouseTable) (ir.WholeRelationScopeIdentity, error) {
	if s.scopeEvidenceIdentity != nil || s.bindings.AuthScopeMode != "" || len(s.bindings.AuthResourcePaths) != 0 {
		return s.workspaceScopeIdentity()
	}
	if _, ok := s.physicalPlan.BindVars["auth_resource_paths_unrestricted"].(bool); ok {
		return s.workspaceScopeIdentity()
	} else if _, exists := s.physicalPlan.BindVars["auth_resource_paths_unrestricted"]; exists {
		return ir.WholeRelationScopeIdentity{}, fmt.Errorf("workspace Combine has an invalid typed authorization mode")
	}
	var sourceProject, sourceGeneration, sourceMode string
	var sourcePaths []string
	if len(captured) > 0 {
		source := captured[0]
		if !source.ScopeEvidence.Matches(source.SourcePlan, source.ScopeIdentity) {
			return ir.WholeRelationScopeIdentity{}, fmt.Errorf("workspace input %q has no exact compiler scope evidence", source.OutputID)
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
		return ir.WholeRelationScopeIdentity{}, fmt.Errorf("workspace Combine has no proven source from which to derive its empty-path scope")
	}
	if sourceProject != s.bindings.Project || sourceGeneration != s.bindings.DatasetGeneration || !sameExecutionStrings(sourcePaths, s.bindings.AuthResourcePaths) {
		return ir.WholeRelationScopeIdentity{}, fmt.Errorf("workspace input scope differs from the exact output project, generation, or authorization paths")
	}
	if sourceMode != string(authscope.ReadScopeUnrestricted) && sourceMode != string(authscope.ReadScopeRestricted) {
		return ir.WholeRelationScopeIdentity{}, fmt.Errorf("workspace input has unsupported authorization scope mode %q", sourceMode)
	}
	schemaDigest, err := compiledOutputSchemaDigest(s.outputSchema)
	if err != nil {
		return ir.WholeRelationScopeIdentity{}, err
	}
	return ir.WholeRelationScopeIdentity{
		OutputID: s.Name, Project: s.bindings.Project, DatasetGeneration: s.bindings.DatasetGeneration,
		AuthScopeMode: sourceMode, AuthResourcePaths: sourcePaths, SchemaDigest: schemaDigest,
	}, nil
}

func (s OutputStream) workspaceScopeIdentity() (ir.WholeRelationScopeIdentity, error) {
	if s.scopeEvidenceIdentity != nil {
		identity := *s.scopeEvidenceIdentity
		if identity.OutputID != s.Name || identity.Project != s.bindings.Project || identity.DatasetGeneration != s.bindings.DatasetGeneration ||
			(s.bindings.AuthScopeMode != "" && identity.AuthScopeMode != string(s.bindings.AuthScopeMode)) ||
			!sameExecutionStrings(identity.AuthResourcePaths, s.bindings.AuthResourcePaths) {
			return ir.WholeRelationScopeIdentity{}, fmt.Errorf("workspace Combine scope identity differs from its exact output bindings")
		}
		if _, err := streamExecutionAuthScopeMode(s); err != nil {
			return ir.WholeRelationScopeIdentity{}, err
		}
		return identity, nil
	}
	mode, err := streamExecutionAuthScopeMode(s)
	if err != nil {
		return ir.WholeRelationScopeIdentity{}, err
	}
	schemaDigest, err := compiledOutputSchemaDigest(s.outputSchema)
	if err != nil {
		return ir.WholeRelationScopeIdentity{}, err
	}
	return ir.WholeRelationScopeIdentity{
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

func cloneBindVars(input map[string]any) map[string]any {
	clone := make(map[string]any, len(input))
	for key, value := range input {
		clone[key] = value
	}
	return clone
}

func publicStreamRow(row map[string]any, columns []string) map[string]any {
	public := make(map[string]any, len(columns)+2)
	for _, column := range columns {
		if value, ok := row[column]; ok {
			public[column] = value
		}
	}
	for _, column := range []string{"__loom_row_id", "auth_resource_path"} {
		if value, ok := row[column]; ok {
			public[column] = value
		}
	}
	return public
}

func ensureStableRowIdentity(row map[string]any, identity *spec.RowIdentity, bindVars map[string]any) error {
	if row == nil {
		return fmt.Errorf("row is nil")
	}
	if value, ok := row["__loom_row_id"]; ok && value != nil && fmt.Sprint(value) != "" {
		return nil
	}
	if identity == nil || len(identity.Fields) == 0 {
		return fmt.Errorf("compiled output is missing a stable row identity")
	}
	parts := make([]any, 0, len(identity.Fields))
	for _, field := range identity.Fields {
		value, ok := row[field]
		if !ok && bindVars != nil {
			value, ok = bindVars[field]
		}
		if !ok || value == nil {
			return fmt.Errorf("stable row identity field %q is missing", field)
		}
		parts = append(parts, value)
	}
	encoded, err := json.Marshal(parts)
	if err != nil {
		return fmt.Errorf("encode stable row identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	row["__loom_row_id"] = hex.EncodeToString(digest[:])
	return nil
}

func dynamicChecks(metadata []lower.DynamicColumnMetadata) map[string]map[string]DynamicColumnCheck {
	checks := make(map[string]map[string]DynamicColumnCheck)
	for _, column := range metadata {
		if checks[column.DynamicName] == nil {
			checks[column.DynamicName] = map[string]DynamicColumnCheck{}
		}
		checks[column.DynamicName][column.SourceKey] = DynamicColumnCheck{ColumnName: column.Name, ValueType: column.ValueType, Many: column.Many, AllowUnknownKeys: column.AllowUnknownKeys}
	}
	return checks
}
