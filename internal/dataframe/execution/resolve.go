package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	model "github.com/calypr/loom/internal/dataframe/compiler/model"
	"github.com/calypr/loom/internal/dataframe/compiler/optimize"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/store/arango"
)

type Config struct {
	Registry                   recipe.Reader
	Revisions                  recipe.RevisionStore
	ResolveBundle              func(context.Context, recipe.Bundle, recipe.RuntimeBindings) (recipe.Bundle, error)
	QueryRows                  QueryRows
	PreviewQueryRows           QueryRows
	WithPreviewReadSnapshot    WithPreviewReadSnapshot
	PreparePreviewIndex        func(context.Context, model.PreviewCoveringIndexSpec) error
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
	streamExecutor streamExecutor
	previewExecutor
	groupPreviewPlanner

	registry                   recipe.Reader
	revisions                  recipe.RevisionStore
	resolveBundle              func(context.Context, recipe.Bundle, recipe.RuntimeBindings) (recipe.Bundle, error)
	queryRows                  QueryRows
	clickHouseQueryRows        ClickHouseQueryRows
	resolveClickHouseInputs    ResolveClickHouseInputs
	withExecutionReadPins      WithExecutionReadPins
	privateClickHouseArtifacts *chartifact.Manager
	scopeDigest                func(recipe.RuntimeBindings) string
	batchSize                  int
}

type Resolved struct {
	Bundle               recipe.Bundle
	Semantic             recipeplan.ResolvedRecipePlan
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
	engine := &Engine{
		registry: cfg.Registry, revisions: cfg.Revisions, resolveBundle: cfg.ResolveBundle,
		queryRows: cfg.QueryRows, batchSize: batch,
		clickHouseQueryRows: cfg.ClickHouseQueryRows, resolveClickHouseInputs: cfg.ResolveClickHouseInputs,
		withExecutionReadPins:      cfg.WithExecutionReadPins,
		privateClickHouseArtifacts: cfg.PrivateClickHouseArtifacts,
		scopeDigest:                cfg.ScopeDigest,
		streamExecutor:             streamExecutor{queryRows: cfg.QueryRows, batchSize: batch, rootPageRows: cfg.RootPageRows},
		groupPreviewPlanner: groupPreviewPlanner{
			previewCoveringIndexes:    cfg.PreviewCoveringIndexes,
			previewCollectionRevision: cfg.PreviewCollectionRevision,
			previewExplainQuery:       cfg.PreviewExplainQuery,
			previewCollectionCount:    cfg.PreviewCollectionCount,
			groupPreviewRowsCache:     &groupPreviewRowsCache{},
		},
	}
	engine.streamExecutor.clickHouseStreamForOutput = engine.clickHouseStreamForOutput
	engine.previewExecutor = previewExecutor{
		streamForOutput:         engine.streamExecutor.streamForOutput,
		preparePreviewIndex:     cfg.PreparePreviewIndex,
		previewQueryRows:        cfg.PreviewQueryRows,
		withPreviewReadSnapshot: cfg.WithPreviewReadSnapshot,
		groupPlanner:            &engine.groupPreviewPlanner,
		scheduleIndexPrewarm: func(ctx context.Context, spec model.PreviewCoveringIndexSpec) {
			schedulePreviewIndexPrewarm(ctx, engine, spec)
		},
	}
	return engine, nil
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
	semanticPlan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		return Resolved{}, err
	}

	semanticPlan.RecipeDigest = storedRecipeDigest
	scope := ""
	if e.scopeDigest != nil {
		scope = e.scopeDigest(bindings)
	}
	resolved, err := recipeplan.ResolveRecipePlan(semanticPlan, scope, bindings.DatasetGeneration)
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
