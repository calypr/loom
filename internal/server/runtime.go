package server

// pg-runtime-server-composition: the explicit PostgreSQL source backend's
// full server runtime. A deployment that selects server.backend postgres with
// the complete six-series placement ladder is composed entirely over the
// backend factory (internal/backends/postgres): six-series schemas, the
// Explorer store family, the recipe registries, the publication plane, and
// ClickHouse materialization. There is no Arango fall-through on this path:
// this file constructs no ArangoDB client, adapter, or AQL surface, and every
// capability without a PostgreSQL adapter is a named typed refusal, never a
// silent zero value and never an Arango-backed answer. Configurations that do
// not carry the consumable full-runtime ladder keep receiving the
// byte-stable ErrPostgreSQLRuntimeNotWired sentinel through the first-query
// route's own gate (server.go), before any dial.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	queryapi "github.com/calypr/loom/internal/api/graphql/graph/query"
	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	backendspostgres "github.com/calypr/loom/internal/backends/postgres"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	publication "github.com/calypr/loom/internal/dataframe/publication"
	publicationclickhouse "github.com/calypr/loom/internal/dataframe/publication/clickhouse"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataframe/recipe"
	publicationcontract "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/artifactfs"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/ingest"
	"github.com/calypr/loom/internal/projectid"
	clickhousestore "github.com/calypr/loom/internal/store/clickhouse"
)

// postgreSQLRuntimeAcquireTimeout bounds waiting for a pooled connection on
// the full runtime, the same ingest-lane default the first query route uses.
const postgreSQLRuntimeAcquireTimeout = 30 * time.Second

// postgreSQLRuntimeUnavailable is the typed explicit-unavailable posture for
// capabilities the full PostgreSQL runtime does not yet have an adapter for
// (the ValidateSelectionReferences reference check and repository-generation
// activation on the Explorer store). It is the sibling of
// postgreSQLFirstQueryRouteWiringError with the full-runtime attribution:
// named, retryable-false, backend-tagged, never Arango-backed.
func postgreSQLRuntimeUnavailable(operation string) error {
	return dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable,
		operation+" has no PostgreSQL adapter yet; the PostgreSQL runtime refuses it instead of serving ArangoDB data",
		dataframeerrors.WithRetryable(false),
		dataframeerrors.WithDetails(map[string]any{"backend": "postgresql", "stage": "adapter"}))
}

// postgresRuntimeAssignment resolves the consumable full-runtime placement:
// server.postgresql.schemas carrying exactly the six production series, each
// non-blank and pairwise distinct after trimming. Any other shape returns the
// arch-backend-06 sentinel so those configurations keep their pinned
// byte-stable startup refusal through the first-query route's gate. Deep
// schema-identifier validation and the cross-series migration-ledger law stay
// with the factory's authoritative control.Config.Validate, which also runs
// pre-dial.
func postgresRuntimeAssignment(pg PostgreSQLConfig) (backendspostgres.SeriesSchemas, map[backendspostgres.Series]string, error) {
	required := []backendspostgres.Series{
		backendspostgres.SeriesSource,
		backendspostgres.SeriesExplorerControl,
		backendspostgres.SeriesPublicationControl,
		backendspostgres.SeriesDatasetRelease,
		backendspostgres.SeriesRecipeChains,
		backendspostgres.SeriesRecipeExecRegistries,
	}
	if len(pg.Schemas) != len(required) {
		return nil, nil, ErrPostgreSQLRuntimeNotWired
	}
	assignment := backendspostgres.SeriesSchemas{}
	names := map[backendspostgres.Series]string{}
	seen := map[string]bool{}
	for _, series := range required {
		schema, assigned := pg.Schemas[string(series)]
		schema = strings.TrimSpace(schema)
		if !assigned || schema == "" || seen[schema] {
			return nil, nil, ErrPostgreSQLRuntimeNotWired
		}
		seen[schema] = true
		assignment[series] = schema
		names[series] = schema
	}
	return assignment, names, nil
}

// pgExplorerStore adapts the PostgreSQL Explorer family to the neutral
// explorer.Store contract. The persistent 17 methods ride the backend store
// directly; publication rides the sibling PublishStore (it crosses into the
// dataset_release ledger); the two capabilities with no PostgreSQL adapter --
// repository-generation activation and the Arango-only selection-reference
// reference check -- are named typed refusals. The compile-time assertion
// below is the no-omission proof: the composition cannot compile if the
// neutral contract grows a method this adapter does not decide.
type pgExplorerStore struct {
	*backendspostgres.ExplorerStore
	publish *backendspostgres.PublishStore
}

var _ explorer.Store = pgExplorerStore{}

func (s pgExplorerStore) PublishAuthoring(ctx context.Context, receipt explorer.CompilationReceipt, revision explorer.Revision, release publicationcontract.ProjectRelease, expectedReleaseRevision int64) (*explorer.Revision, error) {
	if s.publish == nil {
		return nil, postgreSQLRuntimeUnavailable("Explorer publication (the dataset_release assignment is missing)")
	}
	return s.publish.PublishAuthoring(ctx, receipt, revision, release, expectedReleaseRevision)
}

func (s pgExplorerStore) ActivateRepositoryGeneration(context.Context, string, string, string) error {
	return postgreSQLRuntimeUnavailable("Repository-generation activation")
}

// ValidateSelectionReferences is the Arango-side reference-integrity check on
// explicit selection inputs. There is no PostgreSQL adapter for it yet; the
// refusal is named and typed so an authoring flow that reaches it fails
// closed rather than reading Arango graph state.
func (s pgExplorerStore) ValidateSelectionReferences(context.Context, string, string, authscope.ReadScope, []explorer.ResourceRef) error {
	return postgreSQLRuntimeUnavailable("Explicit-selection reference validation")
}

// pgExplorerReads pairs the Explorer store with the explicit-group store for
// the resolver and lifecycle ports that need explicit-group persistence plus
// selection reads; the two backend stores share the explorer_control schema
// and the runtime pool, so the pair is a pure port union with no shared
// mutable state beyond the pool itself.
type pgExplorerReads struct {
	*backendspostgres.ExplorerStore
	*backendspostgres.ExplicitGroupStore
}

var _ explorer.ExplicitGroupRepository = pgExplorerReads{}

// pgRecipeAuthorization is the PostgreSQL sibling of recipeAuthorization. It
// is identical except for one honest omission: SelectionMembersCollection is
// the Arango-only runtime binding consumed exclusively by the AQL group-rows
// renderer, so the PostgreSQL runtime leaves it empty instead of stamping an
// Arango collection name onto every PostgreSQL binding. The PostgreSQL
// renderer never consumes the field, and a PostgreSQL-tagged command never
// reaches an AQL renderer (closed dispatch rejects it at the compiler
// boundary).
type pgRecipeAuthorization struct {
	resolver *authscope.ScopeResolver
}

var _ graphresolver.RecipeAuthorizer = pgRecipeAuthorization{}

func (a pgRecipeAuthorization) AuthorizeRead(ctx context.Context, bindings recipe.RuntimeBindings) (recipe.RuntimeBindings, error) {
	if strings.TrimSpace(bindings.Project) == "" {
		return recipe.RuntimeBindings{}, dataframeerrors.NewError(dataframeerrors.CodeProjectRequired, "")
	}
	bindings.Project = strings.TrimSpace(bindings.Project)
	bindings.SelectionProject = projectid.Canonical(bindings.Project)
	if a.resolver == nil {
		bindings.AuthScopeMode = authscope.ReadScopeUnrestricted
		return bindings, nil
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	scope, err := a.resolver.ResolveReadScopeForGeneration(ctx, principal, bindings.Project, bindings.DatasetGeneration, bindings.AuthResourcePaths)
	if err != nil {
		return recipe.RuntimeBindings{}, recipeAuthorizationError(err)
	}
	if scope.Mode == authscope.ReadScopeRestricted && len(scope.AuthResourcePaths) == 0 {
		return recipe.RuntimeBindings{}, dataframeerrors.NewError(dataframeerrors.CodeUnauthorizedProject, "")
	}
	bindings.AuthResourcePaths = append([]string(nil), scope.AuthResourcePaths...)
	bindings.AuthScopeMode = scope.Mode
	return bindings, nil
}

func (a pgRecipeAuthorization) AuthorizeWrite(ctx context.Context, bindings recipe.RuntimeBindings) (recipe.RuntimeBindings, error) {
	if strings.TrimSpace(bindings.Project) == "" {
		return recipe.RuntimeBindings{}, dataframeerrors.NewError(dataframeerrors.CodeProjectRequired, "")
	}
	bindings.Project = strings.TrimSpace(bindings.Project)
	bindings.SelectionProject = projectid.Canonical(bindings.Project)
	if a.resolver == nil {
		bindings.AuthScopeMode = authscope.ReadScopeUnrestricted
		return bindings, nil
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	scope, err := a.resolver.ResolveWriteScopeForGeneration(ctx, principal, bindings.Project, bindings.DatasetGeneration, bindings.AuthResourcePaths)
	if err != nil {
		return recipe.RuntimeBindings{}, recipeAuthorizationError(err)
	}
	bindings.AuthResourcePaths = append([]string(nil), scope.AuthResourcePaths...)
	bindings.AuthScopeMode = scope.Mode
	return bindings, nil
}

// pgIngestRunner drives a server-side PostgreSQL generation load through the
// ingest lane's own PostgreSQL entry point -- the same production path the
// loom-data CLI takes (ingest.LoadPostgresGeneration), never the Arango
// loader. The connection placement carries the credential and is never
// logged; errors surface through postgresFirstQueryRouteError's credential
// scrubbing at the composition boundary.
type pgIngestRunner struct {
	BaseOptions ingest.LoadOptions
	PG          ingest.PGSourceOptions
}

func (r pgIngestRunner) RunGeneration(ctx context.Context, req loadapi.GenerationLoadRequest, sink ingest.EventSink) (ingest.LoadSummary, error) {
	ref, err := publicationcontract.NewRef(req.Project, req.Generation)
	if err != nil {
		return ingest.LoadSummary{}, err
	}
	opts := r.BaseOptions
	opts.Project = req.Project
	opts.AuthResourcePath = req.AuthResourcePath
	opts.MetaDir = req.StagedDir
	opts.Dataset = &ref
	opts.DeferActivation = req.DeferActivation
	opts.Truncate = false
	opts.EventSink = sink
	// StageOnly is a CLI mode. The HTTP API stages through DeferActivation,
	// the same rule the Arango-lane IngestRunner enforces.
	opts.StageOnly = false
	summary, err := ingest.LoadPostgresGeneration(ctx, opts, r.PG)
	if err != nil {
		return summary, err
	}
	// The PG load entry point never activates (unlike the Arango-lane
	// ingest.Load, which activates inline). The CLI's non-stage-only flow
	// follows it with the explicit ActivatePostgresGeneration CAS call; the
	// HTTP lane's Activated=!DeferActivation answer is honest only when this
	// runner mirrors that exact sequence.
	if !req.DeferActivation {
		if err := ingest.ActivatePostgresGeneration(ctx, ref, r.PG); err != nil {
			return summary, err
		}
	}
	return summary, nil
}

// pgGenerationActivator is the loadapi.GenerationActivator for the full
// PostgreSQL runtime. ReadManifest rides the runtime's own dataset manifest
// lifecycle port. Activate mirrors the CLI lane's ActivatePostgresGeneration
// CAS discipline over the same port: staged-only, expected pointer read from
// the active pointer, typed conflicts preserved, and a commit-outcome-unknown
// activation surfaced as ingest.ActivationOutcomeError so the caller audits
// rather than retries blind.
type pgGenerationActivator struct {
	manifests publicationcontract.ManifestLifecycle
}

func (a pgGenerationActivator) ReadManifest(ctx context.Context, ref publicationcontract.Ref) (publicationcontract.Manifest, error) {
	if a.manifests == nil {
		return publicationcontract.Manifest{}, postgreSQLRuntimeUnavailable("Generation manifest reads (the dataset_release assignment is missing)")
	}
	return a.manifests.ReadManifest(ctx, ref)
}

func (a pgGenerationActivator) Activate(ctx context.Context, manifest publicationcontract.Manifest) error {
	if a.manifests == nil {
		return postgreSQLRuntimeUnavailable("Server-side generation activation (the dataset_release assignment is missing)")
	}
	ref := manifest.Dataset
	if !manifest.IsStaged() {
		if err := manifest.Validate(); err != nil {
			return err
		}
		return fmt.Errorf("activate dataset generation %s/%s: %w: state is %s", ref.Project, ref.Generation, publicationcontract.ErrGenerationNotReady, manifest.State)
	}
	var expected *publicationcontract.Ref
	active, resolveErr := a.manifests.ResolveActiveManifest(ctx, ref.Project)
	switch {
	case resolveErr == nil:
		if active.IsStaged() {
			expected = &active.Dataset
		}
	case errors.Is(resolveErr, publicationcontract.ErrNoActiveGeneration):
		expected = nil
	default:
		// A pointer naming a non-staged manifest is a store inconsistency.
		// Keep expected=nil so the CAS compares against the real pointer row
		// and refuses instead of clobbering it -- the CLI lane's rule.
		expected = nil
	}
	if err := a.manifests.ActivateGeneration(ctx, manifest, expected); err != nil {
		// CLI-parity classification (ingest.ActivatePostgresGeneration):
		// known typed refusals surface unchanged; connectivity-class
		// failures that leave the commit outcome unknown are wrapped so
		// the caller audits the pointer instead of retrying blind.
		if errors.Is(err, backendspostgres.ErrActivationConflict) ||
			errors.Is(err, publicationcontract.ErrGenerationNotReady) ||
			errors.Is(err, publicationcontract.ErrInvalidActiveGeneration) {
			return err
		}
		if pgActivationOutcomeUnknown(err) {
			return &ingest.ActivationOutcomeError{Dataset: ref, Err: err}
		}
		return err
	}
	return nil
}

// pgActivationOutcomeUnknown is the server-side sibling of the CLI lane's
// pgCommitOutcomeUnknown for the activation CAS. The driver-aware half
// (SQLSTATE 08xx/57P01/57P02, the store sentinels ErrCommitOutcomeUnknown /
// ErrConnect / ErrBatchWrite, context cancellation, and net.Error) is the
// backend's own CommitOutcomeUnknown classifier; the EOF pair joins it here
// exactly as on the CLI side. The CLI's pgSafePointCancelError exclusion is
// deliberately absent: that marker is produced only by the load phase's
// between-statement checkpoints, and an error surfacing from inside a store
// call -- which is the only source here -- is never wrapped that way, so a
// cancellation observed mid-activation is genuinely outcome-unknown.
func pgActivationOutcomeUnknown(err error) bool {
	if err == nil {
		return false
	}
	if backendspostgres.CommitOutcomeUnknown(err) {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

// composePostgreSQLFullRuntime is the listen-free full composition for a
// deployment that selected the PostgreSQL source backend with the complete
// six-series ladder. Order of laws mirrors the first-query route: (1) the
// consumability gate runs first and pre-dial -- shapes it cannot consume
// keep receiving ErrPostgreSQLRuntimeNotWired byte-stably through the
// first-query route's own gate; (2) connection-contract validation runs
// pre-dial and the factory's Config.Validate is the authoritative schema
// ledger gate; (3) the runtime opens exactly through the backend factory with
// all six series assigned and SetupNone -- migrations belong to the ingest
// lane's ownership law, and a schema the caller has not arranged is a named
// factory refusal, never a silent create; (4) every port is PostgreSQL: the
// compile target is selected once from the source schema binding, the row
// port is the factory's tagged executor, all durable state rides the
// factory's assembled stores, the active generation resolves through the
// runtime's manifest port, and no ArangoDB client, adapter, or AQL surface is
// constructed anywhere in this branch. Capabilities without a PostgreSQL
// adapter are named typed refusals (postgreSQLRuntimeUnavailable), never
// silently Arango-backed. ClickHouse is optional exactly as on the Arango
// lane: without it the publication target stays nil and Explorer publication
// refuses with the recorded degradation, while every read lane keeps serving
// from PostgreSQL.
func composePostgreSQLFullRuntime(ctx context.Context, serverConfig Config, logger *slog.Logger) (server *httpapi.HTTPServer, closeRuntime func() error, err error) {
	// Cleanup law: compose must not own any cleanup whose duty outlives its
	// return. Every acquired resource is pushed onto cleanups (acquisition
	// order); on an error return the deferred sweep tears down what was
	// acquired so far in reverse order, and on success the same stack is
	// returned as the caller's closer, so reconciler stops, the ClickHouse
	// client, and the pool all close at serve shutdown -- never when compose
	// returns. The AST witness in runtime_test.go fails this build's tests
	// if a defer reappears directly in this body.
	var cleanups []func() error
	defer func() {
		if err == nil {
			return
		}
		_ = runRuntimeCloseSteps(cleanups)
	}()
	assignment, schemas, err := postgresRuntimeAssignment(serverConfig.Server.PostgreSQL)
	if err != nil {
		return nil, nil, err
	}
	pg := serverConfig.Server.PostgreSQL
	if err := pg.validate(); err != nil {
		return nil, nil, err
	}
	dsn := postgresFirstQueryRouteDSN(pg)
	runtime, err := backendspostgres.Open(ctx, backendspostgres.Config{
		DSN:            dsn,
		MaxConns:       int32(pg.MaxOpenConns),
		MinConns:       int32(pg.MaxIdleConns),
		AcquireTimeout: postgreSQLRuntimeAcquireTimeout,
		Schemas:        assignment,
		Setup:          backendspostgres.SetupNone,
	})
	if err != nil {
		return nil, nil, postgresFirstQueryRouteError(fmt.Errorf("open PostgreSQL server runtime: %w", err), pg, dsn)
	}
	cleanups = append(cleanups, func() error {
		return postgresFirstQueryRouteError(runtime.Close(), pg, dsn)
	})
	// The six data-plane families are assembled by the factory only when
	// their series is assigned; the gate above assigns all six, so a nil here
	// means the factory regressed its own assembly contract. Fail closed with
	// a named refusal instead of composing a half-wired runtime.
	if runtime.Explorer == nil || runtime.Publish == nil || runtime.Interpretations == nil ||
		runtime.ExplicitGroups == nil || runtime.CapabilitySnapshots == nil || runtime.TableShapes == nil ||
		runtime.Bundles == nil || runtime.RecipeRevisions == nil || runtime.RecipeExecutions == nil ||
		runtime.Catalog == nil || runtime.SemanticReads == nil || runtime.Releases == nil || runtime.Manifests == nil {
		return nil, nil, postgresFirstQueryRouteError(fmt.Errorf("%w: assembled PostgreSQL runtime is missing a required data-plane port", backendspostgres.ErrBootstrap), pg, dsn)
	}

	// The compile target is selected exactly once from the source schema
	// placement; per-request dialect selection is not available to any
	// caller. This deployment serves sources from PostgreSQL, so every
	// compiler boundary (recipe engine, FHIR query, graph, preview) uses the
	// PostgreSQL target through the same target-aware operations.
	binding, err := ir.NewPostgresStorageBinding(schemas[backendspostgres.SeriesSource])
	if err != nil {
		return nil, nil, postgresFirstQueryRouteError(fmt.Errorf("bind PostgreSQL storage placement: %w", err), pg, dsn)
	}
	sourceCompileTarget, err := compiler.PostgreSQLTarget(binding)
	if err != nil {
		return nil, nil, postgresFirstQueryRouteError(fmt.Errorf("select PostgreSQL compile target: %w", err), pg, dsn)
	}

	dataframes := dataframeexecution.NewService(dataframeexecution.ServiceConfig{
		SourceRows: configuredPostgreSQLSourceRows(logger, "query_rows", runtime.SourceRows),
	})

	discoveryCache := catalog.NewCache()
	discoverFields := discoveryCache.DiscoverFields(runtime.DiscoverFields)

	auth, err := wireAuth(serverConfig, serverConfig.Server.AllowUnauthenticated || serverConfig.Auth.AllowUnauthenticated, runtime.DiscoverExistingAuthResourcePaths)
	if err != nil {
		return nil, nil, fmt.Errorf("configure authentication: %w", err)
	}
	authenticator, authorizer, scopeResolver := auth.authenticator, auth.authorizer, auth.scopeResolver

	degradation := error(nil)
	publicationReady := true
	var clickhouse *clickhousestore.Client
	var materializationReader *published.Reader
	var privateClickHouseArtifacts *chartifact.Manager
	if serverConfig.Server.ClickHouse.Enabled {
		clickhouse, err = clickhousestore.New(clickhousestore.Options{URL: serverConfig.Server.ClickHouse.URL, Database: serverConfig.Server.ClickHouse.Database, Username: serverConfig.Server.ClickHouse.Username, Password: serverConfig.Server.ClickHouse.Password})
		if err != nil {
			return nil, nil, fmt.Errorf("create ClickHouse client: %w", err)
		}
		cleanups = append(cleanups, clickhouse.Close)
		if err := clickhouse.EnsureDatabase(ctx); err != nil {
			degradation = recordDegradation(logger, degradation, "ClickHouse database", err)
			publicationReady = false
		}
		privateClickHouseArtifacts, err = chartifact.New(chartifact.Config{Catalog: runtime.Chartifacts, ClickHouse: clickhouse,
			BatchRows:  serverConfig.Server.RecipeBatchRows,
			BatchBytes: serverConfig.Server.RecipeBatchBytes,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("create private ClickHouse artifact manager: %w", err)
		}
		materializationReader = &published.Reader{ClickHouse: clickhouse, Catalog: runtime.Bundles, Logger: logger, MaxPage: 1000, ActiveManifestResolver: runtime.Manifests, ActiveReleaseResolver: runtime.Releases}
	}

	var clickHouseQueryRows dataframeexecution.ClickHouseQueryRows
	if clickhouse != nil {
		clickHouseQueryRows = clickhouse.QueryRowsArgsVisit
	}
	var resolveClickHouseInputs dataframeexecution.ResolveClickHouseInputs
	var withExecutionReadPins dataframeexecution.WithExecutionReadPins
	if materializationReader != nil {
		resolver := clickHouseCombineInputResolver{reader: materializationReader, scopes: scopeResolver}
		resolveClickHouseInputs = resolver.resolve
		withExecutionReadPins = materializationReader.WithExecutionReadPins
	}

	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		CompileTarget: sourceCompileTarget,
		Registry:      runtime.RecipeExecutions,
		Revisions:     runtime.RecipeRevisions,
		ResolveBundle: recipeSchemaResolver(runtime.DiscoverFields, discoveryCache),
		// The preview index/scan evidence ports are ArangoDB surfaces
		// (covering-index DDL, AQL EXPLAIN). The PostgreSQL engine leaves
		// them nil and the engine's own guards refuse preview index
		// evidence as typed unavailable -- never fabricated, never Arango.
		PreparePreviewIndex:        nil,
		PreviewCoveringIndexes:     nil,
		PreviewCollectionRevision:  nil,
		PreviewCollectionScans:     nil,
		PreviewCollectionCount:     nil,
		ClickHouseQueryRows:        clickHouseQueryRows,
		PreviewSourceRows:          configuredPostgreSQLSourceRows(logger, "preview_query_rows", runtime.SourceRows),
		ResolveClickHouseInputs:    resolveClickHouseInputs,
		WithExecutionReadPins:      withExecutionReadPins,
		PrivateClickHouseArtifacts: privateClickHouseArtifacts,
		SourceRows:                 configuredPostgreSQLSourceRows(logger, "query_rows", runtime.SourceRows),
		ScopeDigest:                recipeScopeDigest,
		RootPageRows:               serverConfig.Server.RecipeQueryPageRows,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create dataframe recipe engine: %w", err)
	}

	var bundleTarget publication.Target
	if serverConfig.Server.ClickHouse.Enabled && publicationReady {
		bundleStore, err := publicationclickhouse.NewBundleStore(clickhouse, runtime.Bundles)
		if err != nil {
			return nil, nil, fmt.Errorf("create dataframe bundle store: %w", err)
		}
		bundleStore.SetActiveReleaseReader(runtime.Releases)
		// Abandoned-execution sweep runs through the backend's own
		// reconciler: the registry scan, pointer binding check, lease, and
		// terminal write are PostgreSQL law (same pool, same ledger), and the
		// physical ClickHouse drop is the caller-owned hook.
		reconciler, err := runtime.NewPublicationReconciler(runtime.Releases)
		if err != nil {
			degradation = recordDegradation(logger, degradation, "publication reconciler", err)
			publicationReady = false
		} else if err := reconciler.SweepStale(ctx, time.Now().UTC().Add(-2*time.Minute), func(ctx context.Context, physicalTable string) error {
			return clickhouse.DropTable(ctx, physicalTable)
		}); err != nil {
			degradation = recordDegradation(logger, degradation, "dataframe publication reconciliation", err)
			publicationReady = false
		}
		if publicationReady {
			bundleTarget = bundleStore
		}
	}

	verificationStore := publicationVerificationStore{executions: runtime.Bundles}
	releaseService := &publicationcontract.ReleaseService{Manifests: runtime.Manifests, Releases: runtime.Releases, Verifier: verificationStore, Required: serverConfig.Server.RequiredDataframeSelectors}
	var activationConflictOnce atomic.Bool
	activationConflictOnce.Store(serverConfig.Server.DevActivationConflictOnce)
	var repositoryActivationConflictOnce atomic.Bool
	repositoryActivationConflictOnce.Store(serverConfig.Server.DevRepositoryActivationConflictOnce)
	activateExplorerRelease := newExplorerReleaseActivator(releaseService, &repositoryActivationConflictOnce)
	prepareExplorerRelease := func(ctx context.Context, project, generation string, selectors []publicationcontract.DataframeSelector) (publicationcontract.ProjectRelease, int64, error) {
		expectedRevision := int64(0)
		active, err := releaseService.Active(ctx, project)
		if err == nil {
			expectedRevision = active.Revision
		} else if !errors.Is(err, publicationcontract.ErrNoActiveRelease) {
			return publicationcontract.ProjectRelease{}, 0, err
		}
		release, err := releaseService.Create(ctx, publicationcontract.ActivationRequest{
			Project: project, Generation: generation, GitCommit: generation,
			OptionalSelectors: selectors,
		})
		// The dev fault scenario is harness parity with the Arango lane: the
		// candidate stays retained while PublishAuthoring's CAS refuses.
		if err == nil && activationConflictOnce.CompareAndSwap(true, false) {
			expectedRevision++
		}
		return release, expectedRevision, err
	}
	validateExplorerReleaseGeneration := func(ctx context.Context, project, generation string) error {
		return releaseService.ValidateGeneration(ctx, project, generation)
	}

	explorerStore := pgExplorerStore{ExplorerStore: runtime.Explorer, publish: runtime.Publish}
	explorerReads := pgExplorerReads{ExplorerStore: runtime.Explorer, ExplicitGroupStore: runtime.ExplicitGroups}
	capabilityResolver, err := newExplorerCapabilityResolver(runtime.Catalog, scopeResolver, runtime.Manifests, runtime.CapabilitySnapshots)
	if err != nil {
		return nil, nil, fmt.Errorf("create Explorer capability resolver: %w", err)
	}
	explorerService, err := explorer.NewService(explorerStore)
	if err != nil {
		return nil, nil, fmt.Errorf("create Explorer service: %w", err)
	}
	artifactStore, err := artifactfs.New(serverConfig.Server.ArtifactDirectory)
	if err != nil {
		return nil, nil, fmt.Errorf("create Explorer artifact store: %w", err)
	}

	resolver := graphresolver.NewResolver(graphresolver.ResolverConfig{
		DataframeQuery: queryapi.Config{
			CompileTarget: sourceCompileTarget,
			// Reference discovery has no PostgreSQL read lane yet; the nil
			// port makes reference-backed traversal fail closed as typed
			// BACKEND_UNAVAILABLE -- never a partial read, never Arango.
			DiscoverReferences:     nil,
			DiscoverFields:         discoverFields,
			Dataframes:             dataframes,
			ScopeResolver:          scopeResolver,
			ActiveManifestResolver: runtime.Manifests,
			Explain:                nil,
		},
		MaterializationReader: materializationReader,
		Logger:                logger,
		RecipeControl: dataframeexecution.Control{
			Engine: recipeEngine,
			ExplainPostgreSQLConnection: func(ctx context.Context, compiled compiler.CompiledQuery) (dataframeexecution.PostgreSQLPlanAssessment, error) {
				return explainPostgreSQLCompiledQuery(ctx, runtime.PlanJSON, compiled)
			},
		},
		RecipeAuthorizer: pgRecipeAuthorization{resolver: scopeResolver},
		RecipeRevisions:  runtime.RecipeRevisions,
		RecipeExecutions: graphresolver.NewAuthorizedRecipeExecutionReader(runtime.Bundles, scopeResolver),
	})

	ingestRunner := pgIngestRunner{
		// The graph schema is the server's own configured JSON (the same
		// server.schema the Arango-lane IngestRunner rides), never the
		// PostgreSQL source schema name: opts.Schema is a graph.Load path.
		BaseOptions: ingest.LoadOptions{Schema: serverConfig.Server.Schema},
		PG: ingest.PGSourceOptions{
			DSN:            dsn,
			Schema:         schemas[backendspostgres.SeriesSource],
			ManifestSchema: schemas[backendspostgres.SeriesDatasetRelease],
			// LoadPostgresGeneration requires all four catalog evidence
			// writers (ErrGenerationLoadRequiresCatalogWriters); the factory
			// assembled them from the same six-series placement, so the
			// server-side lane rides the identical ports the CLI wires.
			Catalog: ingest.GenerationCatalogWriters{
				FieldProfiles: runtime.FieldProfiles,
				Inventory:     runtime.Inventory,
				Relationships: runtime.Relationships,
				Contributions: runtime.Contributions,
			},
		},
	}
	generationService, err := loadapi.NewService(loadapi.ServiceConfig{
		LoadGeneration:      ingestRunner.RunGeneration,
		GenerationActivator: pgGenerationActivator{manifests: runtime.Manifests},
		DataframeReleases:   runtime.Bundles,
		Logger:              logger,
		OnSuccess: func(project string) {
			discoveryCache.InvalidateProject(project)
			if scopeResolver != nil {
				scopeResolver.InvalidateProject(project)
			}
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create generation load service: %w", err)
	}

	compileReceipt := func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		var resolveCombineSchemas combineInputSchemaResolver
		if materializationReader != nil {
			resolveCombineSchemas = func(ctx context.Context, output lower.CompiledRecipeOutput, bindings recipe.RuntimeBindings) (resolvedCombineInputSchema, error) {
				return exactPublishedCombineInputSchemas(ctx, output, bindings, materializationReader, materializationReader.WithExecutionReadPins, scopeResolver)
			}
		}
		return compileExplorerReceipt(ctx, request, capabilityResolver, recipeEngine, explorerService, logger, resolveCombineSchemas)
	}
	persistPublishedWorkspace, err := localWorkspaceWriter(serverConfig.Server.LocalWorkspaceWriteback, serverConfig.Server.LocalWorkspaceProject)
	if err != nil {
		return nil, nil, fmt.Errorf("configure local workspace writeback: %w", err)
	}
	populationMappingCursorCodec, err := lifecycle.NewHMACPopulationMappingCursorCodec(serverConfig.Server.PopulationMappingCursorSecret)
	if err != nil {
		return nil, nil, fmt.Errorf("configure population mapping cursor signing: %w", err)
	}
	var artifactPublishedReader lifecycle.ArtifactPublishedReader
	if materializationReader != nil {
		artifactPublishedReader = materializationReader
		if serverConfig.Server.DevArtifactRowDelay > 0 {
			artifactPublishedReader = artifactDelayReader{next: artifactPublishedReader, delay: serverConfig.Server.DevArtifactRowDelay}
		}
	}
	schemaIndex, err := fhirschema.GeneratedIndex()
	if err != nil {
		return nil, nil, fmt.Errorf("load generated schema index for row choices: %w", err)
	}
	rowChoiceResolver, err := lifecycle.NewSchemaRowChoiceResolver(schemaIndex)
	if err != nil {
		return nil, nil, fmt.Errorf("configure row-choice schema resolver: %w", err)
	}
	explicitGroupResolver, err := lifecycle.NewRepositoryExplicitGroupRevisionResolver(explorerReads)
	if err != nil {
		return nil, nil, fmt.Errorf("configure explicit group revision resolver: %w", err)
	}
	lifecycleConfig := lifecycle.Config{
		SemanticInventory:                  runtime.SemanticReads.PageSemanticInventory,
		ResolveSemanticInventorySelections: runtime.SemanticReads.ResolveSemanticInventorySelections,
		// SelectionMembersCollection is an AQL-only runtime binding (the AQL
		// group-rows renderer is its sole consumer); the PostgreSQL runtime
		// leaves it empty, which the PostgreSQL renderer never reads.
		SelectionMembersCollection:   "",
		InterpretationRepository:     runtime.Interpretations,
		PopulationMappingCursorCodec: populationMappingCursorCodec,
		SelectionSourceResolver:      published.SelectionSourceAdapter{Reader: materializationReader},
		SelectionReferenceValidator:  explorerStore.ValidateSelectionReferences,
		RowChoiceResolver:            rowChoiceResolver,
		RowChoicePlanner:             rowChoiceResolver,
		ExplicitGroupResolver:        explicitGroupResolver,
		ExplicitGroupRepository:      explorerReads,
		TableShapeCapabilities:       runtime.TableShapes,
		ScanCategories:               configuredCategoryScanner(logger, recipeEngine),
		CompileReceipt:               compileReceipt,
		ConstructionSourceStage: func(ctx context.Context, request lifecycle.ConstructionSourceStageRequest) (explorer.ReceiptConstructionStage, error) {
			return compileConstructionSourceStage(ctx, request, recipeEngine)
		},
		Capability: lifecycle.CapabilityResolver{
			Current: func(ctx context.Context, project, _ string, generation string) (capability.Snapshot, error) {
				return capabilityResolver.Resolve(ctx, project, generation)
			},
			Token:          capabilityResolver.ResolveToken,
			ForCompilation: capabilityResolver.ResolveForCompilation,
			ForExecution:   capabilityResolver.ResolveForExecution,
			Catalog:        authoringV2Catalog,
		},
		ReceiptLookup: func(ctx context.Context, project, explorerID, receiptID string) (*explorer.CompilationReceipt, error) {
			return explorerService.CompilationReceiptForExplorer(ctx, project, explorerID, receiptID)
		},
		ArtifactStore:    artifactStore,
		PublishedReader:  artifactPublishedReader,
		ArtifactTTL:      serverConfig.Server.ArtifactTTL,
		ArtifactMaxRows:  serverConfig.Server.ArtifactMaxRows,
		ArtifactMaxBytes: serverConfig.Server.ArtifactMaxBytes,
		PreviewReceipt: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			if receipt == nil {
				return dataframeexecution.PreviewSummary{}, fmt.Errorf("compilation receipt is required")
			}
			resolved, err := compileValidatedReceiptResolution(ctx, recipeEngine, receipt, bindings)
			if err != nil {
				logger.Error("Explorer receipt preview resolution failed", "receipt_id", receipt.ID, "error", err)
				return dataframeexecution.PreviewSummary{}, classifyReceiptPreviewResolutionError(receipt.ID, err)
			}
			output := ""
			if len(bindings.OutputNames) > 0 {
				output = bindings.OutputNames[0]
			}
			summary, previewErr := recipeEngine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{Output: output, Limit: bindings.PreviewLimit, IncludeRowIdentity: bindings.IncludeRowIdentity}, visit)
			if previewErr != nil {
				logReceiptPreviewContractFailure(logger, requestIDFromContext(ctx), receipt.ID, output, previewErr)
			}
			return summary, previewErr
		},
		ValidateReceiptStream: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings) error {
			err := validateReceiptFullStream(ctx, recipeEngine, receipt, bindings)
			if err != nil {
				logger.Error("Explorer row-value policy full-stream validation failed", "receipt_id", receipt.ID, "error", err)
			}
			return err
		},
		PopulationMapping: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, output string, memberIDs []string, after string, limit int) (dataframeexecution.PopulationMappingResult, error) {
			if receipt == nil {
				return dataframeexecution.PopulationMappingResult{}, fmt.Errorf("compilation receipt is required")
			}
			resolved, err := compileValidatedReceiptResolution(ctx, recipeEngine, receipt, bindings)
			if err != nil {
				logger.Error("Explorer receipt population mapping resolution failed", "receipt_id", receipt.ID, "error", err)
				return dataframeexecution.PopulationMappingResult{}, classifyReceiptPreviewResolutionError(receipt.ID, err)
			}
			reader := dataframeexecution.PopulationMemberReaderFunc(func(_ context.Context, visit func(string) error) error {
				for _, id := range memberIDs {
					if err := visit(id); err != nil {
						return err
					}
				}
				return nil
			})
			return recipeEngine.PopulationMapping(ctx, resolved, dataframeexecution.PopulationMappingRequest{Output: output, AfterMemberID: after, MaxUnmapped: limit}, reader)
		},
		CellTrace: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error) {
			if receipt == nil {
				return dataframeexecution.CellTraceResult{}, fmt.Errorf("compilation receipt is required")
			}
			resolved, err := compileValidatedReceiptResolution(ctx, recipeEngine, receipt, bindings)
			if err != nil {
				logger.Error("Explorer receipt cell trace resolution failed", "receipt_id", receipt.ID, "error", err)
				return dataframeexecution.CellTraceResult{}, classifyReceiptPreviewResolutionError(receipt.ID, err)
			}
			return recipeEngine.CellTrace(ctx, resolved, request)
		},
		RowLineage: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.RowLineageRequest) (dataframeexecution.RowLineageResult, error) {
			if receipt == nil {
				return dataframeexecution.RowLineageResult{}, fmt.Errorf("compilation receipt is required")
			}
			resolved, err := compileValidatedReceiptResolution(ctx, recipeEngine, receipt, bindings)
			if err != nil {
				logger.Error("Explorer receipt row lineage resolution failed", "receipt_id", receipt.ID, "error", err)
				return dataframeexecution.RowLineageResult{}, classifyReceiptPreviewResolutionError(receipt.ID, err)
			}
			return recipeEngine.RowLineage(ctx, resolved, request)
		},
		TableShapeExclusions: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.TableShapeExclusionRequest) (dataframeexecution.TableShapeExclusionResult, error) {
			if receipt == nil {
				return dataframeexecution.TableShapeExclusionResult{}, fmt.Errorf("compilation receipt is required")
			}
			resolved, err := compileValidatedReceiptResolution(ctx, recipeEngine, receipt, bindings)
			if err != nil {
				logger.Error("Explorer receipt table-shape exclusion resolution failed", "receipt_id", receipt.ID, "error", err)
				return dataframeexecution.TableShapeExclusionResult{}, classifyReceiptPreviewResolutionError(receipt.ID, err)
			}
			return recipeEngine.TableShapeExclusions(ctx, resolved, request)
		},
		MaterializeReceipt: explorerReceiptMaterializer(
			recipeEngine, bundleTarget, runtime.Bundles, degradation, logger,
			serverConfig.Server.RecipeBatchRows, serverConfig.Server.RecipeBatchBytes,
			serverConfig.Server.RecipeQualityMaxRows, serverConfig.Server.RecipeQualityMaxDistinctKeys,
		),
		ValidateReleaseGeneration: validateExplorerReleaseGeneration,
		ActivateRelease:           activateExplorerRelease,
		PrepareRelease:            prepareExplorerRelease,
		PersistPublishedWorkspace: persistPublishedWorkspace,
	}
	server, err = httpapi.NewHTTPServer(httpapi.HTTPConfig{Authenticator: authenticator, Authorizer: authorizer, Logger: logger,
		CoreReadyCheck: func(ctx context.Context) error {
			return runtime.Health(ctx)
		},
		ClickHouseReadyCheck: func(ctx context.Context) error {
			if clickhouse == nil {
				return nil
			}
			return clickhouse.Ping(ctx)
		}, ClickHouseEnabled: serverConfig.Server.ClickHouse.Enabled})
	if err != nil {
		return nil, nil, fmt.Errorf("create HTTP server: %w", err)
	}
	explorerHandlers := newExplorerHTTPHandlers(authorizer, func(ctx context.Context, principal *authscope.Principal, project string) error {
		if scopeResolver == nil {
			return nil
		}
		return scopeResolver.AuthorizeReadProject(ctx, principal, project)
	}, explorerService, lifecycleConfig)
	explorerHandlers.constructionInputs = constructionInputsCatalog{
		reader: materializationReader, catalog: runtime.Bundles, capabilities: lifecycleConfig.Capability,
		scopes: scopeResolver, explorers: explorerService, revisions: explorerService,
	}
	if err := registerRoutes(server, generationService, authorizer, resolver, explorerHandlers, runtime.Bundles, scopeResolver); err != nil {
		return nil, nil, fmt.Errorf("register HTTP routes: %w", err)
	}
	if privateClickHouseArtifacts != nil {
		reconcileCtx, cancelReconciler := context.WithCancel(ctx)
		reconcilerDone := startPrivateArtifactReconciler(reconcileCtx, privateClickHouseArtifacts, logger)
		cleanups = append(cleanups, func() error {
			cancelReconciler()
			timer := time.NewTimer(cleanupTimeout)
			defer timer.Stop()
			select {
			case <-reconcilerDone:
			case <-timer.C:
				logger.Error("private ClickHouse artifact reconciler did not stop before shutdown deadline")
			}
			return nil
		})
	}
	if artifactStore != nil {
		artifactReconcileCtx, cancelArtifactReconciler := context.WithCancel(ctx)
		artifactReconcilerDone := startArtifactFSReconciler(artifactReconcileCtx, artifactStore, logger)
		cleanups = append(cleanups, func() error {
			cancelArtifactReconciler()
			timer := time.NewTimer(cleanupTimeout)
			defer timer.Stop()
			select {
			case <-artifactReconcilerDone:
			case <-timer.C:
				logger.Error("filesystem artifact reconciler did not stop before shutdown deadline")
			}
			return nil
		})
	}
	// The caller's closer tears the runtime down in reverse acquisition
	// order: reconcilers stop first (they may still be writing through the
	// ClickHouse client and the pool), then the ClickHouse client closes,
	// then the pool.
	closeRuntime = func() error { return runRuntimeCloseSteps(cleanups) }
	return server, closeRuntime, nil
}

// runRuntimeCloseSteps tears down acquired resources in reverse
// acquisition order: every step runs even after one fails, and the first
// failure is the reported one. This is both compose's error-path sweep and
// the returned closer's body, so the shutdown ordering law has exactly one
// implementation.
func runRuntimeCloseSteps(steps []func() error) error {
	var firstErr error
	for i := len(steps) - 1; i >= 0; i-- {
		if cerr := steps[i](); cerr != nil && firstErr == nil {
			firstErr = cerr
		}
	}
	return firstErr
}

// runPostgreSQLFullRuntime is the production entry: compose, then serve. The
// startup line keeps the six-series placement visible for operators without
// echoing credentials (the recomputation is pure and cannot diverge from
// what compose opened).
func runPostgreSQLFullRuntime(ctx context.Context, serverConfig Config, logger *slog.Logger) error {
	server, closeRuntime, err := composePostgreSQLFullRuntime(ctx, serverConfig, logger)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := closeRuntime(); closeErr != nil {
			logger.Error("close PostgreSQL server runtime", "error", closeErr)
		}
	}()
	_, schemas, _ := postgresRuntimeAssignment(serverConfig.Server.PostgreSQL)
	errCh := make(chan error, 1)
	go func() {
		logger.Info("starting HTTP server", "listen", serverConfig.Server.Listen, "backend", serverConfig.Server.Backend,
			"source_schema", schemas[backendspostgres.SeriesSource],
			"explorer_schema", schemas[backendspostgres.SeriesExplorerControl],
			"publication_schema", schemas[backendspostgres.SeriesPublicationControl],
			"dataset_release_schema", schemas[backendspostgres.SeriesDatasetRelease],
			"recipe_chains_schema", schemas[backendspostgres.SeriesRecipeChains],
			"recipe_exec_schema", schemas[backendspostgres.SeriesRecipeExecRegistries],
			"no_auth", serverConfig.Server.AllowUnauthenticated || serverConfig.Auth.AllowUnauthenticated)
		errCh <- server.App().Listen(serverConfig.Server.Listen)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("server stopped: %w", err)
		}
	case <-ctx.Done():
		logger.Info("shutting down HTTP server", "reason", ctx.Err())
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		if err := server.App().ShutdownWithContext(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown failed: %w", err)
		}
	}
	return nil
}
