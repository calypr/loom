package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	publication "github.com/calypr/loom/internal/dataset"
)

const defaultRowLimit = 25

var ErrActiveGenerationConflict = errors.New("requested dataset generation conflicts with active generation")

type ServiceConfig struct {
	QueryRows              func(context.Context, string, int, map[string]any, func(map[string]any) error) error
	ScopeResolver          *authscope.ScopeResolver
	ActiveManifestResolver publication.ActiveResolver
}

type Service struct {
	queryRows              func(context.Context, string, int, map[string]any, func(map[string]any) error) error
	scopeResolver          *authscope.ScopeResolver
	activeManifestResolver publication.ActiveResolver
}

func NewService(cfg ServiceConfig) *Service {
	return &Service{
		queryRows:              cfg.QueryRows,
		scopeResolver:          cfg.ScopeResolver,
		activeManifestResolver: cfg.ActiveManifestResolver,
	}
}

func (s *Service) Run(ctx context.Context, req RunRequest) (*Result, error) {
	started := time.Now()
	compiled, diagnostics, err := s.compileRunRequestWithDiagnostics(ctx, req)
	if err != nil {
		return nil, err
	}
	result, err := s.runQuery(ctx, compiled)
	if err != nil {
		return nil, err
	}
	diagnostics.ArangoQuery = result.Diagnostics.ArangoQuery
	diagnostics.RowMaterialization = result.Diagnostics.RowMaterialization
	diagnostics.ResultAssembly = result.Diagnostics.ResultAssembly
	diagnostics.Total = time.Since(started)
	result.Diagnostics = diagnostics
	return result, nil
}

func (s *Service) compileRunRequestWithDiagnostics(ctx context.Context, req RunRequest) (CompiledQuery, QueryDiagnostics, error) {
	compiled, diagnostics, err := s.prepareAndCompile(ctx, req)
	return compiled, diagnostics, err
}

// prepareAndCompile is the only runtime compiler boundary. It accepts the
// canonical recipe wire format, resolves request-scoped authorization and
// generation bindings, and invokes the shared semantic/physical compiler.
func (s *Service) prepareAndCompile(ctx context.Context, req RunRequest) (CompiledQuery, QueryDiagnostics, error) {
	prepareStarted := time.Now()
	bindings, err := s.prepareBindings(ctx, req.Bindings)
	if err != nil {
		return CompiledQuery{}, QueryDiagnostics{}, err
	}
	diagnostics := QueryDiagnostics{RequestPreparation: time.Since(prepareStarted)}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultRowLimit
	}
	bindings.PreviewLimit = limit
	compileStarted := time.Now()
	plan, err := semantic.BuildRecipePlan(req.Recipe, bindings)
	if err != nil {
		return CompiledQuery{}, QueryDiagnostics{}, err
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "runtime", bindings.DatasetGeneration)
	if err != nil {
		return CompiledQuery{}, QueryDiagnostics{}, err
	}
	queries, err := compiler.CompileResolvedRecipePlanWithPolicy(resolved, limit, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		return CompiledQuery{}, QueryDiagnostics{}, err
	}
	if len(queries) != 1 {
		return CompiledQuery{}, QueryDiagnostics{}, fmt.Errorf("runtime recipe produced %d outputs, want 1", len(queries))
	}
	diagnostics.Compilation = time.Since(compileStarted)
	diagnostics.Plan = queries[0].PlanDiagnostics
	return queries[0], diagnostics, nil
}

func (s *Service) prepareBindings(ctx context.Context, bindings recipe.RuntimeBindings) (recipe.RuntimeBindings, error) {
	if bindings.Project == "" {
		return recipe.RuntimeBindings{}, fmt.Errorf("project is required")
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := authscope.AuthorizeProject(principal, bindings.Project, s.scopeResolver != nil); err != nil {
		return recipe.RuntimeBindings{}, err
	}
	if s.activeManifestResolver != nil {
		manifest, err := publication.ResolveActive(ctx, s.activeManifestResolver, bindings.Project)
		if err != nil {
			return recipe.RuntimeBindings{}, fmt.Errorf("resolve active dataset generation: %w", err)
		}
		active := manifest.Dataset.Generation
		requested := normalizeDatasetGeneration(bindings.DatasetGeneration)
		if requested != "" && requested != active {
			return recipe.RuntimeBindings{}, fmt.Errorf("%w: project %q requested %q but active is %q", ErrActiveGenerationConflict, bindings.Project, requested, active)
		}
		bindings.DatasetGeneration = active
	}
	if s.scopeResolver == nil && bindings.AuthScopeMode != "" {
		return bindings, nil
	}
	scope, err := s.resolveReadScopeForGeneration(ctx, principal, bindings.Project, bindings.DatasetGeneration, bindings.AuthResourcePaths)
	if err != nil {
		return recipe.RuntimeBindings{}, err
	}
	bindings.AuthResourcePaths = cloneStrings(scope.AuthResourcePaths)
	bindings.AuthScopeMode = scope.Mode
	return bindings, nil
}
