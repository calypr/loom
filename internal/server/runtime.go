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
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	publication "github.com/calypr/loom/internal/dataframe/publication"
	publicationclickhouse "github.com/calypr/loom/internal/dataframe/publication/clickhouse"
	"github.com/calypr/loom/internal/dataframe/published"
	publicationcontract "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/artifactfs"
	"github.com/calypr/loom/internal/ingest"
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
	runtime, err := openPostgresBackend(ctx, backendspostgres.Config{
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
	var plane *publicationPlane
	if serverConfig.Server.ClickHouse.Enabled {
		var closeClickHouse func() error
		var planeErr error
		plane, closeClickHouse, planeErr = newPublicationPlane(ctx, serverConfig, logger, publicationPlaneInput{
			InitialDegradation:      degradation,
			InitialPublicationReady: publicationReady,
			ClickHouse:              clickhousestore.Options{URL: serverConfig.Server.ClickHouse.URL, Database: serverConfig.Server.ClickHouse.Database, Username: serverConfig.Server.ClickHouse.Username, Password: serverConfig.Server.ClickHouse.Password},
			// The PostgreSQL lane rides the factory's own artifact catalog
			// port: no bootstrap step, no Arango collection.
			AcquireArtifactCatalog: func(context.Context, *clickhousestore.Client) (chartifact.Catalog, error) {
				return runtime.Chartifacts, nil
			},
			BundleCatalog:          runtime.Bundles,
			ActiveManifestResolver: runtime.Manifests,
			ActiveReleaseResolver:  runtime.Releases,
			Scopes:                 scopeResolver,
		})
		// The client was acquired the moment the constructor returned a
		// closer: the cleanups stack owns it even on the plane's error path.
		if closeClickHouse != nil {
			cleanups = append(cleanups, closeClickHouse)
		}
		if planeErr != nil {
			return nil, nil, planeErr
		}
		clickhouse = plane.ClickHouse
		privateClickHouseArtifacts = plane.PrivateArtifacts
		materializationReader = plane.MaterializationReader
		degradation = plane.Degradation
		publicationReady = plane.PublicationReady
	}

	var clickHouseQueryRows dataframeexecution.ClickHouseQueryRows
	var resolveClickHouseInputs dataframeexecution.ResolveClickHouseInputs
	var withExecutionReadPins dataframeexecution.WithExecutionReadPins
	if plane != nil {
		clickHouseQueryRows = plane.ClickHouseQueryRows
		resolveClickHouseInputs = plane.ResolveClickHouseInputs
		withExecutionReadPins = plane.WithExecutionReadPins
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
	// pg-exec-11: with ClickHouse enabled, the PostgreSQL engine must carry
	// the full private workspace Combine port set (capture manager, result
	// executor, published resolver, read pins). Silent config drift here
	// would surface only as a first-Combine failure, so refuse at boot.
	if serverConfig.Server.ClickHouse.Enabled && !recipeEngine.CombinePortsConfigured() {
		return nil, nil, fmt.Errorf("PostgreSQL runtime has ClickHouse enabled but the recipe engine is missing a private workspace Combine port")
	}

	var bundleTarget publication.Target
	if plane != nil {
		// Abandoned-execution sweep runs through the backend's own
		// reconciler: the registry scan, pointer binding check, lease, and
		// terminal write are PostgreSQL law (same pool, same ledger), and the
		// physical ClickHouse drop is the caller-owned hook.
		if err := plane.reconcileBundles(ctx, logger, publicationReady, degradation, runtime.Bundles, runtime.Releases,
			func(ctx context.Context, clickhouse *clickhousestore.Client, _ *publicationclickhouse.ClickHouseBundleStore, cutoff time.Time) (string, error) {
				reconciler, err := runtime.NewPublicationReconciler(runtime.Releases)
				if err != nil {
					return "publication reconciler", err
				}
				if err := reconciler.SweepStale(ctx, cutoff, func(ctx context.Context, physicalTable string) error {
					return clickhouse.DropTable(ctx, physicalTable)
				}); err != nil {
					return "dataframe publication reconciliation", err
				}
				return "", nil
			}); err != nil {
			return nil, nil, err
		}
		publicationReady = plane.PublicationReady
		degradation = plane.Degradation
		bundleTarget = plane.BundleTarget
	}

	// The release service, dev CAS-fault one-shots, and the prepare/
	// activate/validate closures are the shared composition (compose.go):
	// byte-identical to the Arango lane, over this lane's ports.
	hooks := newReleaseHooks(serverConfig, runtime.Manifests, runtime.Releases, runtime.Bundles)

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
		// The PostgreSQL lane's authorizer leaves SelectionMembersCollection
		// empty: the zero value is the explicit PostgreSQL stamp policy (the
		// AQL group-rows renderer is that binding's only consumer, and the
		// PostgreSQL renderer never consumes it).
		RecipeAuthorizer: recipeAuthorization{resolver: scopeResolver},
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

	// The Explorer lifecycle is the shared composition in compose.go; the
	// lane differences arrive as one typed ports struct (semantic reads,
	// interpretations, table shapes, explicit groups, receipt executions).
	lifecycleConfig, err := buildLifecycleConfig(lifecycleCompositionInput{
		ServerConfig:          serverConfig,
		Logger:                logger,
		RecipeEngine:          recipeEngine,
		CapabilityResolver:    capabilityResolver,
		ExplorerService:       explorerService,
		ArtifactStore:         artifactStore,
		MaterializationReader: materializationReader,
		ScopeResolver:         scopeResolver,
		BundleTarget:          bundleTarget,
		Degradation:           degradation,
		Hooks:                 hooks,
		Ports: lifecycleLanePorts{
			SemanticInventory:                  runtime.SemanticReads.PageSemanticInventory,
			ResolveSemanticInventorySelections: runtime.SemanticReads.ResolveSemanticInventorySelections,
			// SelectionMembersCollection is an AQL-only runtime binding (the
			// AQL group-rows renderer is its sole consumer); the PostgreSQL
			// runtime leaves it empty, which the PostgreSQL renderer never
			// reads.
			SelectionMembersCollection:  "",
			InterpretationRepository:    runtime.Interpretations,
			TableShapeCapabilities:      runtime.TableShapes,
			ExplicitGroups:              explorerReads,
			SelectionReferenceValidator: explorerStore.ValidateSelectionReferences,
			ReceiptExecutions:           runtime.Bundles,
		},
	})
	if err != nil {
		return nil, nil, err
	}
	server, err = httpapi.NewHTTPServer(httpapi.HTTPConfig{Authenticator: authenticator, Authorizer: authorizer, Logger: logger,
		// Core readiness is the backend's full probe: reachability + the
		// PostgreSQL 18 version gate + the read-only ledger probe over every
		// assigned series (control.Runtime.Readiness). A never-migrated,
		// migrated-away, or ledger-disagreeing schema is not ready, and
		// CoreState maps the failure onto a safe token so operators can
		// tell schema state from connectivity without a DSN in sight.
		CoreReadyCheck: func(ctx context.Context) error {
			return runtime.Readiness(ctx)
		},
		CoreState: postgreSQLCoreReadinessState,
		ClickHouseReadyCheck: func(ctx context.Context) error {
			if clickhouse == nil {
				return nil
			}
			return clickhouse.Ping(ctx)
		}, ClickHouseEnabled: serverConfig.Server.ClickHouse.Enabled})
	if err != nil {
		return nil, nil, fmt.Errorf("create HTTP server: %w", err)
	}
	// Typed route composition contract (arch-backend-08): the releases port
	// and the explicit scoped-auth policy arrive as named fields, validated
	// complete before any route installs; the shared wiring lives in
	// compose.go.
	if err := wireExplorerRoutes(server, generationService, authorizer, resolver, scopeResolver, explorerService, lifecycleConfig, constructionInputsCatalog{
		reader: materializationReader, catalog: runtime.Bundles, capabilities: lifecycleConfig.Capability,
		scopes: scopeResolver, explorers: explorerService, revisions: explorerService,
	}, routeDependencies{releases: runtime.Bundles, scopes: auth.scopePolicy}); err != nil {
		return nil, nil, err
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

// runPostgreSQLFullRuntime is the production entry: compose, then serve
// through the one listen/shutdown loop (compose.go serveRuntime). The
// startup line keeps the six-series placement visible for operators without
// echoing credentials (the recomputation is pure and cannot diverge from
// what compose opened).
func runPostgreSQLFullRuntime(ctx context.Context, serverConfig Config, logger *slog.Logger) error {
	server, closeRuntime, err := composePostgreSQLFullRuntime(ctx, serverConfig, logger)
	if err != nil {
		return err
	}
	_, schemas, _ := postgresRuntimeAssignment(serverConfig.Server.PostgreSQL)
	return serveRuntime(ctx, server, serverConfig.Server.Listen, closeRuntime, "close PostgreSQL server runtime", logger,
		"listen", serverConfig.Server.Listen, "backend", serverConfig.Server.Backend,
		"source_schema", schemas[backendspostgres.SeriesSource],
		"explorer_schema", schemas[backendspostgres.SeriesExplorerControl],
		"publication_schema", schemas[backendspostgres.SeriesPublicationControl],
		"dataset_release_schema", schemas[backendspostgres.SeriesDatasetRelease],
		"recipe_chains_schema", schemas[backendspostgres.SeriesRecipeChains],
		"recipe_exec_schema", schemas[backendspostgres.SeriesRecipeExecRegistries],
		"no_auth", serverConfig.Server.AllowUnauthenticated || serverConfig.Auth.AllowUnauthenticated)
}
