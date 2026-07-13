package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataset"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

const defaultRowLimit = 25

type ServiceConfig struct {
	ConnectionOptions  arangostore.ConnectionOptions
	DiscoverReferences func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error)
	DiscoverFields     func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error)
	ExecuteRows        func(context.Context, ExecuteQueryOptions, string, map[string]any, func(map[string]any) error) error
	ScopeResolver      *authscope.ScopeResolver
	// ActiveManifestResolver is optional. When configured, every Run/Stream
	// selects one READY active generation before resolving scope, catalog facts,
	// lowering, or AQL. A Builder's explicit generation must agree with it.
	ActiveManifestResolver dataset.ActiveManifestResolver
}

type Service struct {
	connOpts               arangostore.ConnectionOptions
	discoverReferences     func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error)
	discoverFields         func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error)
	executeRows            func(context.Context, ExecuteQueryOptions, string, map[string]any, func(map[string]any) error) error
	scopeResolver          *authscope.ScopeResolver
	activeManifestResolver dataset.ActiveManifestResolver
}

func NewService(cfg ServiceConfig) *Service {
	svc := &Service{
		connOpts:               cfg.ConnectionOptions,
		scopeResolver:          cfg.ScopeResolver,
		activeManifestResolver: cfg.ActiveManifestResolver,
	}
	if cfg.DiscoverReferences != nil {
		svc.discoverReferences = cfg.DiscoverReferences
	} else {
		svc.discoverReferences = catalog.DiscoverPopulatedReferences
	}
	if cfg.DiscoverFields != nil {
		svc.discoverFields = cfg.DiscoverFields
	} else {
		svc.discoverFields = catalog.DiscoverPopulatedFields
	}
	if cfg.ExecuteRows != nil {
		svc.executeRows = cfg.ExecuteRows
	} else {
		svc.executeRows = ExecuteQueryRows
	}
	return svc
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
	_, compiled, diagnostics, err := s.prepareAndCompile(ctx, req.Builder, req.Limit)
	return compiled, diagnostics, err
}

// prepareAndCompile is the single preparation/compilation boundary shared by
// execution and execution-independent validation. Keeping it here prevents a
// frontend validation path from silently accepting shapes that Run rejects.
func (s *Service) prepareAndCompile(ctx context.Context, builder Builder, requestedLimit int) (Builder, CompiledQuery, QueryDiagnostics, error) {
	prepareStarted := time.Now()
	spec, err := s.prepareSpec(ctx, builder)
	if err != nil {
		return Builder{}, CompiledQuery{}, QueryDiagnostics{}, err
	}
	diagnostics := QueryDiagnostics{RequestPreparation: time.Since(prepareStarted)}
	limit := requestedLimit
	if limit <= 0 {
		limit = defaultRowLimit
	}
	// Keep the validated logical request through the physical compiler boundary.
	compileStarted := time.Now()
	compiled, err := CompileRequest(spec, limit)
	if err != nil {
		return Builder{}, CompiledQuery{}, QueryDiagnostics{}, err
	}
	diagnostics.Compilation = time.Since(compileStarted)
	diagnostics.Plan = compiled.PlanDiagnostics
	return spec, compiled, diagnostics, nil
}

func (s *Service) prepareSpec(ctx context.Context, builder Builder) (Builder, error) {
	if builder.Project == "" {
		return Builder{}, fmt.Errorf("project is required")
	}
	if builder.RootResourceType == "" {
		return Builder{}, fmt.Errorf("rootResourceType is required")
	}

	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := authorizeProject(principal, builder.Project, s.scopeResolver != nil); err != nil {
		return Builder{}, err
	}
	resolvedBuilder, err := s.resolveActiveBuilder(ctx, builder)
	if err != nil {
		return Builder{}, err
	}
	builder = resolvedBuilder
	var scope authscope.ReadScope
	// A dataframebuilder service can hand an already-resolved scope to a
	// dataframe service configured without its own resolver. Preserve that
	// explicit mode rather than reinterpreting a restricted empty list through
	// the legacy no-paths-means-unrestricted rule.
	if s.scopeResolver == nil && builder.AuthScopeMode != "" {
		scope = authscope.ReadScope{
			AuthResourcePaths: cloneStrings(builder.AuthResourcePaths),
			Mode:              builder.AuthScopeMode,
		}
	} else {
		var err error
		scope, err = s.resolveReadScopeForGeneration(ctx, principal, builder.Project, builder.DatasetGeneration, builder.AuthResourcePaths)
		if err != nil {
			return Builder{}, err
		}
	}

	builder.AuthResourcePaths = cloneStrings(scope.AuthResourcePaths)
	builder.AuthScopeMode = scope.Mode
	if err := s.validateBuilder(ctx, builder); err != nil {
		return Builder{}, err
	}
	expanded, err := s.expandPivotColumns(ctx, builder)
	if err != nil {
		return Builder{}, err
	}
	return expanded, nil
}
