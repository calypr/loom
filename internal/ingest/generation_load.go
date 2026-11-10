// PostgreSQL generation-load orchestrator (pg-ingest-03).
//
// LoadPostgresGeneration composes the completed PostgreSQL adapters — the
// resource-batch and edge-occurrence writers (pg-ingest-01/02), the
// field-profile, resource-inventory, relationship-evidence, and
// semantic-contribution surfaces (pg-catalog-01/02/03/05), and the
// generation-manifest CAS store (pg-store-03) — behind one bounded
// generation load with the existing staged and failure states. It adds no
// new SQL: every statement runs inside a merged adapter or repository, so
// this file contains no SQL strings at all.
//
// Lifecycle (mirrors the ordering the Arango path proves in
// generation_orchestration.go, with the PG writers substituted):
//
//  1. DiscoverNDJSON, graph.Load + loadSchemaSnapshot, PreflightFiles, and
//     newGenerationLoadPlan all run before any PostgreSQL connection is
//     opened or generation state is touched. A malformed staged payload
//     inside the preflight sample window, a missing schema, or an invalid
//     dataset reference fails here without a single database round trip.
//  2. The pool opens (bounded, validated through the store's Config) and
//     MigrateSchema applies the embedded ledger; both are idempotent and
//     advisory-locked.
//  3. CreateManifest is the load claim: the CAS insert either creates the
//     LOADING manifest (this attempt owns the lifecycle) or refuses a
//     second claim. A same-generation retry whose stored manifest carries
//     exactly the same immutable schema identity and is still LOADING
//     resumes it — replayed batches are ledger no-ops — but never rewrites
//     it; a STAGED manifest is reported as committed success
//     (ErrGenerationAlreadyStaged) and a FAILED one as terminal
//     (ErrGenerationLoadAlreadyFailed). A manifest with different immutable
//     bytes is never claimed (ErrGenerationManifestIdentityConflict), and
//     no path here writes the active-generation pointer.
//  4. Bounded write passes, in commit order: resource rows first (all
//     files), then edge occurrences (all files, so every typed edge source
//     is already committed for the store's source precondition), then the
//     catalog evidence — field profiles, the resource-inventory build, the
//     derived relationship evidence, and the retained semantic
//     contributions. Batch identities are content-independent digests of
//     (project, generation, staged-file identity, pass, window ordinal), so
//     a retry from any process offers byte-identical batches and the
//     store's ledger classifies them as replays. Semantic contributions
//     may spill to the store in bounded chunks during the resource pass
//     (they are content-addressed and replay-safe, and no reader consults
//     them until the generation is STAGED); the remainder always lands in
//     the semantic step.
//  5. Only after every pass commits does TransitionManifest CAS the
//     manifest LOADING->STAGED. Activation is never implicit here: the
//     separate explicit call ActivatePostgresGeneration performs the CAS
//     through the generation repository. The load never changes the
//     active-generation pointer.
//
// Failure states follow the same discipline as the Arango path:
//   - The creator of the manifest marks it FAILED whenever a failure has a
//     known commit outcome (server-answered refusals, guard drops, row
//     errors, cancellation observed at a safe point).
//   - When a batch/commit outcome is genuinely unknown (connection loss,
//     backend termination, cancellation racing an in-flight statement) the
//     manifest stays LOADING — never downgraded, because the commit may
//     have landed — and a later replay from any process converges it.
//   - A resumed load (one that did not create the manifest) never marks
//     FAILED: it cannot prove no concurrent loader owns the shared state,
//     which is exactly the unknown-outcome case the LOADING state encodes.
//   - After STAGED, nothing is rolled back; an activation error is an
//     unknown outcome surfaced as ActivationOutcomeError.
//
// The Arango Load behavior and default route are untouched: this entry
// point is explicit and the separate cutover owner alone decides when
// configuration selects it.

package ingest

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/bmeg/jsonschemagraph/graph"
	"github.com/calypr/loom/internal/catalog"
	publication "github.com/calypr/loom/internal/dataset"
	datasetpostgres "github.com/calypr/loom/internal/dataset/postgres"
	storepostgres "github.com/calypr/loom/internal/store/postgres"
	"github.com/jackc/pgx/v5/pgconn"
)

// Batch identities are "ing03:<pass>:<digest>" where the digest covers the
// stable provenance (project, generation, staged-file identity, pass,
// window ordinal) — never a per-attempt nonce, so every retry offers the
// same identity and the store's ledger decides replay versus conflict.
const (
	pgResourceBatchPass   = "r"
	pgEdgeBatchPass       = "e"
	pgBatchIDPrefix       = "ing03:"
	pgApplicationNameBase = "loom-ing03"
	// pgSemanticFlushMinimum floors the in-memory semantic contribution
	// buffer before a bounded spill to the store.
	pgSemanticFlushMinimum = 2000
)

// PGSourceOptions locates one explicit PostgreSQL 18 generation load.
// There are no hidden defaults beyond the pool bounds named below: every
// field is validated by the store layer before a connection is opened.
type PGSourceOptions struct {
	// DSN is the PostgreSQL connection string for the source store.
	DSN string
	// Schema is the schema the load writes into; migrations are applied to
	// it by this entry point (idempotent, advisory-locked).
	Schema string
	// MaxConns bounds the load's connection pool. Zero selects 4.
	MaxConns int32
	// AcquireTimeout bounds waiting for a pool connection. Zero or negative
	// selects 30 seconds.
	AcquireTimeout time.Duration
	// ApplicationName tags this load's backends in pg_stat_activity so
	// operators (and tests) can witness exactly which connections a load
	// owns. The zero value selects a generated loom-ing03-<nonce> name; an
	// explicitly set name is used verbatim.
	ApplicationName string
	// SemanticFlushThreshold bounds the in-memory semantic contribution
	// buffer before bounded spill; zero or negative selects
	// max(2*normalized BatchSize, 2000).
	SemanticFlushThreshold int
	// Catalog wires the four catalog evidence writers the load drives
	// after the resource and edge passes (see GenerationCatalogWriters
	// for the production wiring recipe). All four are required: the load
	// fails with ErrGenerationLoadRequiresCatalogWriters before opening
	// any connection when any is nil, so a miswired caller can never
	// stage a generation with silently missing catalog evidence.
	Catalog GenerationCatalogWriters
}

// Manifest-claim outcomes for a same-generation retry.
var (
	// ErrGenerationAlreadyStaged reports that the generation reached STAGED
	// in an earlier (or concurrent) attempt: read-back distinguishes this
	// committed success from an unknown commit acknowledgement, and the
	// retry wrote nothing.
	ErrGenerationAlreadyStaged = errors.New("dataset generation is already staged")
	// ErrGenerationLoadAlreadyFailed reports a retry against a FAILED
	// generation. FAILED is terminal: a new generation is required.
	ErrGenerationLoadAlreadyFailed = errors.New("dataset generation load already failed")
	// ErrGenerationManifestIdentityConflict reports a retry whose immutable
	// schema identity differs from the stored manifest: one load can never
	// claim another load's manifest, and nothing was written.
	ErrGenerationManifestIdentityConflict = errors.New("dataset generation manifest identity conflict")
	// ErrGenerationManifestOutcomeUnknown reports that a manifest commit
	// outcome itself could not be established (the store was unreachable
	// when the load tried to create or stage it). The manifest keeps its
	// prior state; a later replay from any process converges it.
	ErrGenerationManifestOutcomeUnknown = errors.New("dataset generation manifest commit outcome is unknown")
	// ErrPGGenerationDatasetRequired reports a PostgreSQL generation load
	// requested without an explicit dataset generation: the PG entry point
	// is generation-only and refuses the unversioned namespace outright.
	ErrPGGenerationDatasetRequired = errors.New("postgres generation load requires an explicit dataset generation")
	// ErrGenerationLoadRequiresCatalogWriters reports a PostgreSQL
	// generation load whose PGSourceOptions.Catalog is missing any of the
	// four catalog evidence writers. It fires before any connection or
	// staging write, so a half-wired caller can never produce a STAGED
	// generation with silently missing catalog evidence.
	ErrGenerationLoadRequiresCatalogWriters = errors.New("postgres generation load requires all four catalog evidence writers")
)

// The four catalog evidence writers a PostgreSQL generation load drives
// after the resource and edge passes. The interfaces are declared on the
// consumer side so internal/ingest composes the load without importing
// internal/catalog/postgres (whose in-package tests import ingest, which
// would form an import cycle). The production adapters are the
// catalog/postgres stores wired by the caller over the same DSN/schema:
//
//	profileRepo, _ := storepostgres.NewFieldProfileRepository(src, schema)
//	profiles, _ := catalogpostgres.NewFieldProfileStore(profileRepo)
//	inventoryRepo, _ := storepostgres.NewInventoryRepository(src, schema)
//	inventory, _ := catalogpostgres.New(inventoryRepo)
//	relationsRepo, _ := catalogpostgres.NewRelationshipEvidenceRepository(src, schema)
//	relations, _ := catalogpostgres.NewRelationshipEvidenceStore(relationsRepo)
//	contribs, _ := catalogpostgres.NewSemanticContributionRepository(src, schema)
//
// The store-layer constructors keep their full validation and commit
// guards: the load performs no catalog computation of its own and keeps no
// SQL here.
type GenerationFieldProfileWriter interface {
	WriteFieldProfile(ctx context.Context, scope storepostgres.FieldProfileScope, docs []catalog.FieldCatalogDocument, batchSize int) error
}

type GenerationInventoryRebuilder interface {
	RebuildResourceInventory(ctx context.Context, opts catalog.ResourceInventoryRebuildOptions) (catalog.ResourceInventoryRebuildSummary, error)
}

type GenerationRelationshipDiscoverer interface {
	DiscoverRelationshipObservations(ctx context.Context, opts catalog.RelationshipObservationOptions) (catalog.RelationshipObservationResult, error)
}

type GenerationSemanticContributor interface {
	WriteSemanticInventoryContributions(ctx context.Context, contributions []catalog.SemanticInventoryContribution, batchSize int) error
}

// GenerationCatalogWriters bundles the catalog evidence writers required
// by one PostgreSQL generation load. All four are required; the load
// refuses to start with any of them nil so a miswired caller can never
// silently skip catalog evidence.
type GenerationCatalogWriters struct {
	FieldProfiles GenerationFieldProfileWriter
	Inventory     GenerationInventoryRebuilder
	Relationships GenerationRelationshipDiscoverer
	Contributions GenerationSemanticContributor
}

// Complete reports whether all four catalog writers are wired.
func (w GenerationCatalogWriters) Complete() bool {
	return w.FieldProfiles != nil && w.Inventory != nil &&
		w.Relationships != nil && w.Contributions != nil
}

// pgGenerationLoad carries one orchestrator run's mutable state.
type pgGenerationLoad struct {
	opts LoadOptions
	pg   PGSourceOptions
	plan *generationLoadPlan
	ref  publication.Ref

	client      *storepostgres.Client
	lifecycle   *datasetpostgres.Store
	resourceW   *PostgresResourceWriter
	edgeW       *PostgresEdgeWriter
	profiles    GenerationFieldProfileWriter
	inventory   GenerationInventoryRebuilder
	relations   GenerationRelationshipDiscoverer
	contribs    GenerationSemanticContributor
	application string

	manifest     publication.Manifest
	ownsManifest bool
	staged       bool

	pendingCatalogs map[generationCatalogKey]*catalog.Profiler
	semanticBuffer  []catalog.SemanticInventoryContribution
	semanticFlush   int

	// The merged PostgreSQL writers name their own hard bounds
	// (MaxBatchResources/MaxBatchEdges per offered batch,
	// MaxFieldProfileWriteBatch, MaxInventoryWriteBatch); the ingest
	// BatchSize default (5000, shared with the Arango route) exceeds
	// them, so every write path clamps its window/chunk to the store's
	// bound instead of forwarding the raw option.
	resourceWindow int
	edgeWindow     int
	profileBatch   int
	inventoryBatch int
}

// LoadPostgresGeneration loads every staged NDJSON file into the PostgreSQL
// source store as one immutable generation and leaves it STAGED. It is the
// explicit PG entry point: it never touches Arango and never activates.
// Activation is the separate explicit ActivatePostgresGeneration CAS call.
//
// The load requires opts.Dataset (an exact project/generation), refuses
// truncation, and runs the full pre-backend evidence chain (discovery,
// schema identity, preflight, plan) before opening the PG source
// transaction or mutating generation state. A successful return means the
// manifest is STAGED; summary.Dataset is populated even on failure so
// operators can identify the inactive manifest needing inspection.
func LoadPostgresGeneration(ctx context.Context, opts LoadOptions, pg PGSourceOptions) (summary LoadSummary, err error) {
	start := time.Now()
	opts = normalizeLoadOptions(opts)
	if !pg.Catalog.Complete() {
		return LoadSummary{}, fmt.Errorf("%w: field profiles, inventory, relationships, and semantic contributions must all be wired", ErrGenerationLoadRequiresCatalogWriters)
	}

	// ------------------------------------------------------------------
	// Pre-backend evidence chain: identical order to loadGeneration. No
	// PostgreSQL connection exists anywhere in this block.
	// ------------------------------------------------------------------
	// The PG route loads exactly one staging directory: a bare file path
	// (which the Arango route may accept) has no directory-scoped identity
	// for the ledger batch keys, so it is refused before any other work.
	if info, statErr := os.Stat(opts.MetaDir); statErr != nil || !info.IsDir() {
		return LoadSummary{}, fmt.Errorf("%w: %s", ErrGenerationLoadRequiresDirectory, opts.MetaDir)
	}
	files, err := DiscoverNDJSON(opts.MetaDir)
	if err != nil {
		return LoadSummary{}, err
	}
	if len(files) == 0 {
		return LoadSummary{}, fmt.Errorf("%w: %s", ErrGenerationLoadRequiresFiles, opts.MetaDir)
	}
	summary = LoadSummary{
		Files:        len(files),
		Resources:    map[string]int{},
		BatchCounts:  map[string]int{},
		StageSeconds: map[string]float64{},
	}

	schema, err := graph.Load(opts.Schema)
	if err != nil {
		return summary, err
	}
	schemaIdentity, err := loadSchemaSnapshot(opts.Schema)
	if err != nil {
		return summary, err
	}
	summary.SchemaIdentity = &schemaIdentity
	preflightSampleRows := opts.PreflightSampleRows
	if preflightSampleRows <= 0 {
		preflightSampleRows = defaultPreflightSampleRows
	}
	emitEvent(opts.EventSink, "go_preflight_start", map[string]any{
		"files":              len(files),
		"sampleRows":         preflightSampleRows,
		"schemaSha256":       schemaIdentity.SchemaSHA256,
		"generatedRootCount": len(schemaIdentity.GeneratedResourceTypes),
		"backend":            "postgres",
	})
	preflightStart := time.Now()
	preflight, err := PreflightFiles(files, schema, preflightSampleRows)
	summary.Preflight = preflight
	summary.StageSeconds["preflight"] = time.Since(preflightStart).Seconds()
	emitEvent(opts.EventSink, "go_preflight_complete", map[string]any{
		"files":     len(files),
		"resources": len(preflight.Resources),
		"issues":    len(preflight.Issues),
		"seconds":   summary.StageSeconds["preflight"],
	})
	if err != nil {
		return summary, err
	}

	plan, err := newGenerationLoadPlan(opts, files, schemaIdentity)
	if err != nil {
		return summary, err
	}
	if plan == nil {
		return summary, fmt.Errorf("dataset generation plan is required")
	}
	if strings.TrimSpace(plan.Dataset.Generation) == "" {
		return summary, ErrPGGenerationDatasetRequired
	}
	datasetRef := plan.Dataset
	summary.Dataset = &datasetRef
	if err := ctx.Err(); err != nil {
		return summary, safePointCancel(err)
	}

	resourceTypes := make([]string, 0, len(preflight.Resources))
	for _, resource := range preflight.Resources {
		resourceTypes = append(resourceTypes, resource.ResourceType)
	}

	load := &pgGenerationLoad{opts: opts, pg: pg, plan: plan, ref: datasetRef}
	if load.application = strings.TrimSpace(pg.ApplicationName); load.application == "" {
		load.application = fmt.Sprintf("%s-%d-%s", pgApplicationNameBase, time.Now().UnixNano(), pgRandomHexToken())
	}

	// ------------------------------------------------------------------
	// The PostgreSQL source transaction boundary begins here.
	// ------------------------------------------------------------------
	emitEvent(opts.EventSink, "go_pg_connect_start", map[string]any{
		"backend": "postgres",
		"schema":  pg.Schema,
	})
	connectStart := time.Now()
	if err := load.open(); err != nil {
		return summary, err
	}
	defer func() {
		err = generationCleanupError(ctx, generationCleanupTimeout, err, "close postgres generation backend",
			func(context.Context) error { return load.close() })
	}()
	summary.StageSeconds["pg_connect"] = time.Since(connectStart).Seconds()
	emitEvent(opts.EventSink, "go_pg_connect_complete", map[string]any{
		"backend":   "postgres",
		"schema":    pg.Schema,
		"seconds":   summary.StageSeconds["pg_connect"],
		"app_name":  load.application,
		"resources": len(resourceTypes),
	})

	emitEvent(opts.EventSink, "go_pg_bootstrap_start", map[string]any{
		"schema":   pg.Schema,
		"truncate": false,
	})
	bootstrapStart := time.Now()
	if _, err := storepostgres.MigrateSchema(ctx, load.client, pg.Schema); err != nil {
		return summary, err
	}
	summary.StageSeconds["pg_bootstrap"] = time.Since(bootstrapStart).Seconds()
	emitEvent(opts.EventSink, "go_pg_bootstrap_complete", map[string]any{
		"schema":  pg.Schema,
		"seconds": summary.StageSeconds["pg_bootstrap"],
	})

	if err := load.claimManifest(ctx); err != nil {
		return summary, err
	}
	// The lifecycle deferred cleanup: the manifest creator marks FAILED on
	// every known-outcome failure and leaves LOADING whenever the commit
	// outcome is unknown. After STAGED nothing is ever rolled back.
	defer func() {
		if err == nil || load.staged || !load.ownsManifest {
			return
		}
		if pgCommitOutcomeUnknown(err) {
			return
		}
		err = generationCleanupError(ctx, generationCleanupTimeout, err,
			fmt.Sprintf("mark dataset generation %s/%s failed", plan.Dataset.Project, plan.Dataset.Generation),
			func(cleanupCtx context.Context) error {
				_, transitionErr := load.lifecycle.TransitionManifest(cleanupCtx, load.manifest, publication.StateFailed)
				return transitionErr
			})
	}()

	// Row-error candidates are kept per file and sampled into the summary
	// only when the load returns, exactly as loadGeneration does; the
	// deferred assignment covers every early return.
	var rowErrorCandidates [][]RowErrorSample
	defer func() {
		summary.RowErrors = selectGenerationRowErrorSamples(rowErrorCandidates)
	}()

	if err := load.writeResourcePass(ctx, schema, files, &summary, &rowErrorCandidates); err != nil {
		return summary, load.surfaceStoreError(err, "resource pass")
	}
	if summary.ValidationErrors != 0 || summary.GenerationErrors != 0 || summary.EdgeErrors != 0 {
		return summary, &GenerationLoadIncompleteError{
			Dataset:          plan.Dataset,
			ValidationErrors: summary.ValidationErrors,
			GenerationErrors: summary.GenerationErrors,
			EdgeErrors:       summary.EdgeErrors,
		}
	}
	if err := ctx.Err(); err != nil {
		return summary, safePointCancel(err)
	}

	if err := load.writeEdgePass(ctx, schema, files, &summary); err != nil {
		return summary, load.surfaceStoreError(err, "edge pass")
	}
	if err := ctx.Err(); err != nil {
		return summary, safePointCancel(err)
	}

	if err := load.writeCatalogEvidence(ctx, schemaIdentity, &summary); err != nil {
		return summary, load.surfaceStoreError(err, "catalog evidence")
	}
	if err := ctx.Err(); err != nil {
		return summary, safePointCancel(err)
	}

	stagedManifest, transitionErr := load.lifecycle.TransitionManifest(ctx, load.manifest, publication.StateStaged)
	if transitionErr != nil {
		if pgCommitOutcomeUnknown(transitionErr) {
			return summary, fmt.Errorf("%w: stage dataset generation %s/%s: %w",
				ErrGenerationManifestOutcomeUnknown, plan.Dataset.Project, plan.Dataset.Generation, transitionErr)
		}
		if errors.Is(transitionErr, datasetpostgres.ErrManifestTransitionConflict) {
			// A concurrent loader of the same immutable identity decided
			// the outcome first: the stored terminal state is read back
			// and named, never re-run and never overwritten.
			if stored, readErr := load.lifecycle.ReadManifest(ctx, load.ref); readErr == nil {
				if stored.IsStaged() {
					return summary, fmt.Errorf("%w: %s/%s is already STAGED (a concurrent loader of the same identity won the transition); read back the committed load instead of re-running it",
						ErrGenerationAlreadyStaged, plan.Dataset.Project, plan.Dataset.Generation)
				}
				if stored.State == publication.StateFailed {
					return summary, fmt.Errorf("%w: %s/%s; a new dataset generation is required",
						ErrGenerationLoadAlreadyFailed, plan.Dataset.Project, plan.Dataset.Generation)
				}
			}
		}
		return summary, transitionErr
	}
	load.manifest = stagedManifest
	load.staged = true

	emitEvent(opts.EventSink, "go_load_complete", map[string]any{
		"backend":            "postgres",
		"files":              summary.Files,
		"vertices_inserted":  summary.VerticesInserted,
		"edges_inserted":     summary.EdgesInserted,
		"validation_errors":  summary.ValidationErrors,
		"generation_errors":  summary.GenerationErrors,
		"edge_errors":        summary.EdgeErrors,
		"dataset_generation": plan.Dataset.Generation,
		"seconds":            SecondsSince(start),
	})
	return summary, nil
}

// ActivatePostgresGeneration explicitly activates one STAGED generation by
// compare-and-swap through the pg-store-03 generation repository: nothing
// is ever activated without this call. A generation that is not STAGED
// (LOADING or FAILED) is refused with publication.ErrGenerationNotReady; a
// concurrent pointer move yields the repository's typed
// datasetpostgres.ErrActivationConflict and leaves the pointer untouched.
// An unreachable repository is surfaced as ActivationOutcomeError, which —
// like the Arango path — leaves STAGED for an operator to reconcile and
// never downgrades it.
func ActivatePostgresGeneration(ctx context.Context, ref publication.Ref, pg PGSourceOptions) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(pg.ApplicationName) == "" {
		pg.ApplicationName = pgApplicationNameBase + "-activate"
	}
	client, err := openPGGenerationClient(pg)
	if err != nil {
		return err
	}
	defer client.Close()
	store, err := datasetpostgres.New(client, pg.Schema)
	if err != nil {
		return err
	}
	manifest, err := store.ReadManifest(ctx, ref)
	if err != nil {
		return err
	}
	if !manifest.IsStaged() {
		if err := manifest.Validate(); err != nil {
			return err
		}
		return fmt.Errorf("activate dataset generation %s/%s: %w: state is %s",
			ref.Project, ref.Generation, publication.ErrGenerationNotReady, manifest.State)
	}
	var expected *publication.Ref
	active, resolveErr := store.ResolveActiveManifest(ctx, ref.Project)
	switch {
	case resolveErr == nil:
		if active.IsStaged() {
			expected = &active.Dataset
		}
	case errors.Is(resolveErr, publication.ErrNoActiveGeneration):
		expected = nil
	default:
		// A pointer naming a non-staged manifest is a store inconsistency.
		// Keep expected=nil: the repository's CAS then compares against the
		// real pointer row and refuses the swap instead of clobbering it.
		expected = nil
	}
	if activationErr := store.ActivateGeneration(ctx, manifest, expected); activationErr != nil {
		if errors.Is(activationErr, datasetpostgres.ErrActivationConflict) ||
			errors.Is(activationErr, publication.ErrGenerationNotReady) ||
			errors.Is(activationErr, publication.ErrInvalidActiveGeneration) {
			return activationErr
		}
		if pgCommitOutcomeUnknown(activationErr) {
			return &ActivationOutcomeError{Dataset: ref, Err: activationErr}
		}
		return activationErr
	}
	return nil
}

// openPGGenerationClient validates the pool configuration through the
// store's own Config before any network dial, so blank or malformed
// settings are named store refusals rather than connect failures.
func openPGGenerationClient(pg PGSourceOptions) (*storepostgres.Client, error) {
	maxConns := pg.MaxConns
	if maxConns == 0 {
		maxConns = 4
	}
	acquireTimeout := pg.AcquireTimeout
	if acquireTimeout <= 0 {
		acquireTimeout = 30 * time.Second
	}
	dsn := strings.TrimSpace(pg.DSN)
	application := strings.TrimSpace(pg.ApplicationName)
	if application != "" {
		dsn = joinApplicationName(dsn, application)
	}
	return storepostgres.Open(context.Background(), storepostgres.Config{
		DSN:            dsn,
		MaxConns:       maxConns,
		MinConns:       0,
		AcquireTimeout: acquireTimeout,
	})
}

func (l *pgGenerationLoad) open() error {
	client, err := openPGGenerationClient(l.pg)
	if err != nil {
		return err
	}
	l.client = client
	if err := l.bind(); err != nil {
		l.close()
		return err
	}
	return nil
}

func (l *pgGenerationLoad) close() error {
	if l.client != nil {
		l.client.Close()
		l.client = nil
	}
	return nil
}

func (l *pgGenerationLoad) bind() error {
	schema := l.pg.Schema
	var err error
	if l.lifecycle, err = datasetpostgres.New(l.client, schema); err != nil {
		return err
	}
	if l.resourceW, err = NewPostgresResourceWriter(l.client, schema); err != nil {
		return err
	}
	if l.edgeW, err = NewPostgresEdgeWriter(l.client, schema); err != nil {
		return err
	}
	// The catalog evidence writers arrive wired via PGSourceOptions.Catalog
	// (consumer-side interfaces; the store-layer constructors and their
	// validation/guards run at the caller's wiring site, never here).
	l.profiles = l.pg.Catalog.FieldProfiles
	l.inventory = l.pg.Catalog.Inventory
	l.relations = l.pg.Catalog.Relationships
	l.contribs = l.pg.Catalog.Contributions
	l.semanticFlush = l.pg.SemanticFlushThreshold
	if l.semanticFlush <= 0 {
		l.semanticFlush = 2 * l.opts.BatchSize
		if l.semanticFlush < pgSemanticFlushMinimum {
			l.semanticFlush = pgSemanticFlushMinimum
		}
	}
	l.resourceWindow = min(l.opts.BatchSize, storepostgres.MaxBatchResources)
	l.edgeWindow = min(l.opts.BatchSize, storepostgres.MaxBatchEdges)
	l.profileBatch = min(l.opts.BatchSize, storepostgres.MaxFieldProfileWriteBatch)
	l.inventoryBatch = min(l.opts.BatchSize, storepostgres.MaxInventoryWriteBatch)
	return nil
}

// claimManifest runs the pg-store-03 create CAS and resolves every
// same-generation retry outcome.
func (l *pgGenerationLoad) claimManifest(ctx context.Context) error {
	created, err := l.lifecycle.CreateManifest(ctx, l.plan.Manifest)
	if err == nil {
		l.manifest = created
		l.ownsManifest = true
		emitEvent(l.opts.EventSink, "go_pg_manifest_create", map[string]any{
			"dataset_generation": l.ref.Generation,
			"state":              string(created.State),
		})
		return nil
	}
	if !errors.Is(err, datasetpostgres.ErrManifestAlreadyExists) {
		if pgCommitOutcomeUnknown(err) {
			return fmt.Errorf("%w: create dataset generation manifest %s/%s: %w",
				ErrGenerationManifestOutcomeUnknown, l.ref.Project, l.ref.Generation, err)
		}
		return err
	}
	stored, readErr := l.lifecycle.ReadManifest(ctx, l.ref)
	if readErr != nil {
		if pgCommitOutcomeUnknown(readErr) {
			return fmt.Errorf("%w: read dataset generation manifest %s/%s: %w",
				ErrGenerationManifestOutcomeUnknown, l.ref.Project, l.ref.Generation, readErr)
		}
		return readErr
	}
	if !reflect.DeepEqual(stored.SchemaIdentity, l.plan.Manifest.SchemaIdentity) {
		return fmt.Errorf("%w: %s/%s stores a different immutable schema identity; one load cannot claim another load's manifest",
			ErrGenerationManifestIdentityConflict, l.ref.Project, l.ref.Generation)
	}
	switch {
	case stored.IsStaged():
		l.manifest = stored
		return fmt.Errorf("%w: %s/%s is already STAGED; read back the committed load instead of re-running it",
			ErrGenerationAlreadyStaged, l.ref.Project, l.ref.Generation)
	case stored.State == publication.StateFailed:
		l.manifest = stored
		return fmt.Errorf("%w: %s/%s; a new dataset generation is required",
			ErrGenerationLoadAlreadyFailed, l.ref.Project, l.ref.Generation)
	default:
		// Same immutable identity, still LOADING: resume. This attempt did
		// not create the manifest, so it never marks FAILED — a concurrent
		// loader may own the outcome, which is exactly what LOADING encodes.
		l.manifest = stored
		l.ownsManifest = false
		emitEvent(l.opts.EventSink, "go_pg_manifest_resume", map[string]any{
			"dataset_generation": l.ref.Generation,
			"state":              string(stored.State),
		})
		return nil
	}
}

// writeResourcePass streams every staged file, builds rows with the same
// generation-qualified builders loadFile uses, commits bounded resource
// batches through the ledgered writer, profiles every accepted payload, and
// buffers (or bounded-spills) its semantic contributions.
func (l *pgGenerationLoad) writeResourcePass(
	ctx context.Context,
	schema *graph.GraphSchema,
	files []string,
	summary *LoadSummary,
	rowErrorCandidates *[][]RowErrorSample,
) error {
	shapeCache := catalog.NewShapePlanCacheWithLimits(l.opts.CatalogLimits.MaxShapePlans, l.opts.CatalogLimits.MaxRetainedBytes)
	catalogs := make(map[generationCatalogKey]*catalog.Profiler)
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return safePointCancel(err)
		}
		fileStart := time.Now()
		resourceType := ResourceTypeFromPath(file)
		fileID := semanticInventorySourceFileID(l.opts.MetaDir, file)
		emitEvent(l.opts.EventSink, "go_pg_load_file_start", map[string]any{
			"file": file, "resource": resourceType, "pass": "resources",
		})

		rowBuilder, err := l.newRowBuilder(schema, resourceType)
		if err != nil {
			return err
		}
		profiler := catalog.NewProfilerForGenerationWithLimits(
			l.opts.Project, l.plan.Dataset.Generation, l.opts.AuthResourcePath, resourceType,
			shapeCache, l.opts.CatalogLimits)
		localTimings := map[string]float64{}

		scanner, closeFn, err := OpenLineScanner(file)
		if err != nil {
			return err
		}
		var windowRows []rowBuildResult
		windowIndex := 0
		lineNumber := 0
		fileRows := 0
		var fileErrors fileLoadResult
		scanErr := func() (loopErr error) {
			defer func() {
				if loopErr == nil && len(windowRows) > 0 {
					loopErr = l.commitResourceWindow(ctx, fileID, windowIndex, windowRows, summary)
					windowRows = nil
				}
			}()
			for scanner.Scan() {
				lineNumber++
				if err := ctx.Err(); err != nil {
					return safePointCancel(err)
				}
				line := strings.TrimSpace(scanner.Text())
				if line == "" {
					continue
				}
				built, errorType, buildErr := rowBuilder.Build(resourceType, []byte(line), localTimings)
				if buildErr != nil {
					if l.opts.FailFast {
						return fmt.Errorf("%s %s row %d: %w", filepath.Base(file), resourceType, lineNumber, buildErr)
					}
					*rowErrorCandidates = append(*rowErrorCandidates, []RowErrorSample{{
						File:         filepath.Base(file),
						Line:         lineNumber,
						ResourceType: resourceType,
						Category:     string(errorType),
						Message:      buildErr.Error(),
					}})
					switch errorType {
					case rowErrorValidation:
						fileErrors.ValidationErrors++
					case rowErrorGeneration:
						fileErrors.GenerationErrors++
					case rowErrorEdge:
						fileErrors.EdgeErrors++
					}
					continue
				}
				sourceID := semanticInventorySourceRecordID(fileID, lineNumber)
				if err := profiler.ObservePayloadWithInventory(built.payload, localTimings, sourceID,
					func(contribution catalog.SemanticInventoryContribution) {
						l.semanticBuffer = append(l.semanticBuffer, contribution)
					}); err != nil {
					return fmt.Errorf("observe semantic inventory for %s row %d: %w", filepath.Base(file), lineNumber, err)
				}
				if len(l.semanticBuffer) >= l.semanticFlush {
					if err := l.flushSemanticContributions(ctx, summary); err != nil {
						return err
					}
				}
				windowRows = append(windowRows, built)
				fileRows++
				if lineNumber%l.resourceWindow == 0 {
					if err := l.commitResourceWindow(ctx, fileID, windowIndex, windowRows, summary); err != nil {
						return err
					}
					windowRows = nil
					windowIndex++
				}
			}
			return scanner.Err()
		}()
		if err := errors.Join(scanErr, closeFn()); err != nil {
			return err
		}

		summary.Resources[resourceType] += fileRows
		summary.ValidationErrors += fileErrors.ValidationErrors
		summary.GenerationErrors += fileErrors.GenerationErrors
		summary.EdgeErrors += fileErrors.EdgeErrors
		for name, seconds := range localTimings {
			summary.StageSeconds[name] += seconds
		}
		key := generationCatalogKey{
			project:           l.opts.Project,
			datasetGeneration: l.plan.Dataset.Generation,
			authResourcePath:  l.opts.AuthResourcePath,
			resourceType:      resourceType,
		}
		if merged := catalogs[key]; merged == nil {
			catalogs[key] = profiler
		} else if err := merged.Merge(profiler); err != nil {
			return fmt.Errorf("merge catalog for dataset generation %s/%s %s: %w",
				l.plan.Dataset.Project, l.plan.Dataset.Generation, resourceType, err)
		}
		emitEvent(l.opts.EventSink, "go_pg_load_file_complete", map[string]any{
			"file":      filepath.Base(file),
			"resource":  resourceType,
			"file_rows": fileRows,
			"pass":      "resources",
			"seconds":   time.Since(fileStart).Seconds(),
		})
	}
	l.pendingCatalogs = catalogs
	return nil
}

// commitResourceWindow commits one deterministic row window of one staged
// file. A window with no accepted rows writes nothing at all: a row-error
// sweep must never fabricate an empty batch.
func (l *pgGenerationLoad) commitResourceWindow(ctx context.Context, fileID string, window int, rows []rowBuildResult, summary *LoadSummary) error {
	if len(rows) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return safePointCancel(err)
	}
	batchID := pgGenerationBatchID(l.plan.Dataset.Project, l.plan.Dataset.Generation, fileID, pgResourceBatchPass, window)
	result, err := l.resourceW.WriteResourceBatch(ctx, PGResourceBatchScope{
		BatchID:           batchID,
		Project:           l.plan.Dataset.Project,
		DatasetGeneration: l.plan.Dataset.Generation,
		AuthResourcePath:  l.opts.AuthResourcePath,
	}, rows)
	if err != nil {
		return fmt.Errorf("commit postgres resource batch %s (%d rows): %w", batchID, len(rows), err)
	}
	summary.VerticesInserted += int(result.Committed)
	summary.BatchCounts["resource_batch"]++
	if result.Replay {
		summary.BatchCounts["resource_batch_replay"]++
	}
	emitEvent(l.opts.EventSink, "go_pg_resource_batch_commit", map[string]any{
		"batch":   batchID,
		"offered": result.Offered,
		"commit":  result.Committed,
		"replay":  result.Replay,
		"digest":  result.Digest,
	})
	return nil
}

// writeEdgePass re-streams every staged file with the identical builders
// and commits bounded edge-occurrence batches. Running it only after every
// resource batch committed is what lets the store's typed source
// precondition hold for cross-file references; a genuinely missing source
// is the store's named refusal and fails the load.
func (l *pgGenerationLoad) writeEdgePass(ctx context.Context, schema *graph.GraphSchema, files []string, summary *LoadSummary) error {
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return safePointCancel(err)
		}
		fileStart := time.Now()
		resourceType := ResourceTypeFromPath(file)
		fileID := semanticInventorySourceFileID(l.opts.MetaDir, file)
		emitEvent(l.opts.EventSink, "go_pg_load_file_start", map[string]any{
			"file": file, "resource": resourceType, "pass": "edges",
		})
		rowBuilder, err := l.newRowBuilder(schema, resourceType)
		if err != nil {
			return err
		}
		localTimings := map[string]float64{}
		scanner, closeFn, err := OpenLineScanner(file)
		if err != nil {
			return err
		}
		var windowRows []rowBuildResult
		windowIndex := 0
		lineNumber := 0
		scanErr := func() (loopErr error) {
			defer func() {
				if loopErr == nil && len(windowRows) > 0 {
					loopErr = l.commitEdgeWindow(ctx, fileID, windowIndex, windowRows, summary)
					windowRows = nil
				}
			}()
			for scanner.Scan() {
				lineNumber++
				if err := ctx.Err(); err != nil {
					return safePointCancel(err)
				}
				line := strings.TrimSpace(scanner.Text())
				if line == "" {
					continue
				}
				built, _, buildErr := rowBuilder.Build(resourceType, []byte(line), localTimings)
				if buildErr != nil {
					// Row errors were counted (and already refused the
					// stage) in the resource pass; this pass rebuilds
					// identical rows, so a rebuild that now fails is a
					// staged-input inconsistency, not a row error.
					return fmt.Errorf("%s %s row %d rebuilt with error: %w", filepath.Base(file), resourceType, lineNumber, buildErr)
				}
				if len(built.edges) > 0 {
					windowRows = append(windowRows, built)
				}
				if lineNumber%l.edgeWindow == 0 {
					if err := l.commitEdgeWindow(ctx, fileID, windowIndex, windowRows, summary); err != nil {
						return err
					}
					windowRows = nil
					windowIndex++
				}
			}
			return scanner.Err()
		}()
		if err := errors.Join(scanErr, closeFn()); err != nil {
			return err
		}
		emitEvent(l.opts.EventSink, "go_pg_load_file_complete", map[string]any{
			"file":     filepath.Base(file),
			"resource": resourceType,
			"pass":     "edges",
			"seconds":  time.Since(fileStart).Seconds(),
		})
	}
	return nil
}

func (l *pgGenerationLoad) commitEdgeWindow(ctx context.Context, fileID string, window int, rows []rowBuildResult, summary *LoadSummary) error {
	if len(rows) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return safePointCancel(err)
	}
	batchID := pgGenerationBatchID(l.plan.Dataset.Project, l.plan.Dataset.Generation, fileID, pgEdgeBatchPass, window)
	result, err := l.edgeW.WriteEdgeBatch(ctx, PGEdgeBatchScope{
		BatchID:           batchID,
		Project:           l.plan.Dataset.Project,
		DatasetGeneration: l.plan.Dataset.Generation,
		AuthResourcePath:  l.opts.AuthResourcePath,
	}, rows)
	if err != nil {
		return fmt.Errorf("commit postgres edge batch %s (%d rows): %w", batchID, len(rows), err)
	}
	summary.EdgesInserted += int(result.Committed)
	summary.BatchCounts["edge_batch"]++
	if result.Replay {
		summary.BatchCounts["edge_batch_replay"]++
	}
	emitEvent(l.opts.EventSink, "go_pg_edge_batch_commit", map[string]any{
		"batch":   batchID,
		"offered": result.Offered,
		"commit":  result.Committed,
		"replay":  result.Replay,
		"digest":  result.Digest,
	})
	return nil
}

// writeCatalogEvidence commits the catalog evidence in the acceptance
// order: field profiles, the resource-inventory build, the derived
// relationship evidence, then the remaining semantic contributions. Every
// write is replay-safe; a known-outcome failure here keeps the manifest
// pre-STAGED and the creator deferral marks it FAILED.
func (l *pgGenerationLoad) writeCatalogEvidence(ctx context.Context, schemaIdentity publication.SchemaSnapshot, summary *LoadSummary) error {
	fieldStart := time.Now()
	keys := sortedGenerationCatalogKeys(l.pendingCatalogs)
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return safePointCancel(err)
		}
		scope := storepostgres.FieldProfileScope{
			Project:           key.project,
			DatasetGeneration: key.datasetGeneration,
			AuthResourcePath:  key.authResourcePath,
			ResourceType:      key.resourceType,
		}
		if err := l.profiles.WriteFieldProfile(ctx, scope, l.pendingCatalogs[key].Documents(), l.profileBatch); err != nil {
			return fmt.Errorf("write field profile for %s/%s %s: %w", key.project, key.datasetGeneration, key.resourceType, err)
		}
		summary.BatchCounts["field_profile_write"]++
	}
	summary.StageSeconds["catalog_field_profiles"] = time.Since(fieldStart).Seconds()
	emitEvent(l.opts.EventSink, "go_pg_catalog_field_profiles", map[string]any{"scopes": len(keys)})

	inventoryStart := time.Now()
	// Concurrent loaders of one generation (creator plus resumed attempts)
	// compute the identical content-addressed build and publish the same
	// marker; PostgreSQL's speculative-insert path can report a unique
	// violation on those identical concurrent reinserts, so the rebuild —
	// scan, count insert, publish — runs serialized on one session-scoped
	// advisory lock. Every loader still derives the build from its own
	// scan of the committed rows; the lock orders persistence, never
	// fabricates results.
	var inventory catalog.ResourceInventoryRebuildSummary
	inventoryErr := storepostgres.WithAdvisoryLock(ctx, l.client,
		storepostgres.GenerationAdvisoryLockKey(
			l.plan.Dataset.Project, l.plan.Dataset.Generation),
		func() error {
			var rebuildErr error
			inventory, rebuildErr = l.inventory.RebuildResourceInventory(ctx, catalog.ResourceInventoryRebuildOptions{
				Project:           l.plan.Dataset.Project,
				DatasetGeneration: l.plan.Dataset.Generation,
				ResourceTypes:     schemaIdentity.GeneratedResourceTypes,
				BatchSize:         l.inventoryBatch,
			})
			return rebuildErr
		})
	if inventoryErr != nil {
		return fmt.Errorf("build resource inventory for dataset generation %s/%s: %w",
			l.plan.Dataset.Project, l.plan.Dataset.Generation, inventoryErr)
	}
	summary.StageSeconds["catalog_resource_inventory"] = time.Since(inventoryStart).Seconds()
	summary.BatchCounts["resource_inventory"]++
	emitEvent(l.opts.EventSink, "go_pg_resource_inventory_complete", map[string]any{
		"dataset_generation":  l.plan.Dataset.Generation,
		"build_id":            inventory.BuildID,
		"resource_type_count": inventory.ResourceTypeCount,
		"count_row_count":     inventory.CountRowCount,
		"document_count":      inventory.DocumentCount,
	})

	relationshipStart := time.Now()
	relationship, err := l.relations.DiscoverRelationshipObservations(ctx, catalog.RelationshipObservationOptions{
		Project:           l.plan.Dataset.Project,
		DatasetGeneration: l.plan.Dataset.Generation,
		AuthResourcePaths: []string{l.opts.AuthResourcePath},
	})
	if err != nil {
		return fmt.Errorf("derive relationship evidence for dataset generation %s/%s: %w",
			l.plan.Dataset.Project, l.plan.Dataset.Generation, err)
	}
	summary.StageSeconds["catalog_relationship_evidence"] = time.Since(relationshipStart).Seconds()
	summary.BatchCounts["relationship_observations"] = len(relationship.Values)
	emitEvent(l.opts.EventSink, "go_pg_relationship_evidence_complete", map[string]any{
		"observations": len(relationship.Values),
		"digest":       relationship.Digest,
		"diagnostics":  len(relationship.Diagnostics),
	})

	semanticStart := time.Now()
	if err := l.flushSemanticContributions(ctx, summary); err != nil {
		return err
	}
	summary.StageSeconds["catalog_semantic_evidence"] = time.Since(semanticStart).Seconds()
	return nil
}

// flushSemanticContributions persists the buffered contributions in bounded
// chunks. The store classifies every skipped row exactly (byte replay versus
// named conflict), so a retry from any process rewrites identical rows as
// no-ops.
func (l *pgGenerationLoad) flushSemanticContributions(ctx context.Context, summary *LoadSummary) error {
	if len(l.semanticBuffer) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return safePointCancel(err)
	}
	chunk := l.semanticBuffer
	if err := l.contribs.WriteSemanticInventoryContributions(ctx, chunk, l.opts.BatchSize); err != nil {
		return fmt.Errorf("write semantic inventory contributions: %w", err)
	}
	summary.BatchCounts["semantic_contributions"] += len(chunk)
	summary.BatchCounts["semantic_chunks"]++
	emitEvent(l.opts.EventSink, "go_pg_semantic_write", map[string]any{"contributions": len(chunk)})
	l.semanticBuffer = nil
	return nil
}

// newRowBuilder reproduces exactly the builder selection loadFile uses,
// generation-qualified through generationRowBuilder.
func (l *pgGenerationLoad) newRowBuilder(schema *graph.GraphSchema, resourceType string) (RowBuilder, error) {
	class := schema.GetClass(resourceType)
	if class == nil {
		return nil, fmt.Errorf("%s: class %q not found in graph schema", filepath.Base(resourceType), resourceType)
	}
	var rowBuilder RowBuilder
	if l.opts.UseGeneric || !supportsGeneratedLoad(resourceType) {
		rowBuilder = NewGenericRowBuilder(l.opts.Project, class, schema, graphExtraArgs(l.opts.Project, l.opts.AuthResourcePath))
	} else {
		rowBuilder = NewGeneratedRowBuilder(l.opts.Project, l.opts.AuthResourcePath)
	}
	qualified, err := newGenerationRowBuilder(rowBuilder, l.opts.Project, l.plan.Dataset.Generation)
	if err != nil {
		return nil, err
	}
	return qualified, nil
}

// pgGenerationBatchID derives one deterministic batch identity from stable
// provenance: project, generation, staged-file identity, pass, and the
// file-local window ordinal. Content never enters the identity — the
// ledger digest does that job — so a retry offers the same identity and
// either replays (identical content) or is refused by name (different
// content under a claimed identity).
func pgGenerationBatchID(project, generation, fileID, pass string, window int) string {
	return pgBatchIDPrefix + pass + ":" + generationHash(
		"pg-ingest-03/batch/v1", project, generation, fileID, pass, strconv.Itoa(window),
	)
}

// pgSafePointCancelError marks a cancellation observed at a load checkpoint:
// no commit was in flight, so the failure outcome is known and the creator
// deferral may mark FAILED. Errors surfacing from inside a store call are
// never wrapped this way and stay outcome-unknown.
type pgSafePointCancelError struct{ err error }

func (e *pgSafePointCancelError) Error() string { return e.err.Error() }
func (e *pgSafePointCancelError) Unwrap() error { return e.err }

func safePointCancel(err error) error {
	if err == nil {
		return nil
	}
	return &pgSafePointCancelError{err: err}
}

// surfaceStoreError gives a genuinely outcome-unknown store failure its
// caller-visible identity: the manifest stays LOADING for a replay to
// converge, and the error names the dataset generation load as
// outcome-unknown so no caller can mistake it for a known refusal.
func (l *pgGenerationLoad) surfaceStoreError(err error, stage string) error {
	if err == nil || !pgCommitOutcomeUnknown(err) {
		return err
	}
	return fmt.Errorf("%w: %s/%s %s left a commit outcome unknown: %w",
		ErrGenerationManifestOutcomeUnknown, l.ref.Project, l.ref.Generation, stage, err)
}

// pgCommitOutcomeUnknown reports whether err leaves a database commit
// outcome genuinely unknown: the server never answered (or answered only
// with a backend-shutdown/connectivity class), so the in-flight transaction
// may or may not have committed. Every such failure keeps the manifest in
// LOADING for a replay to converge; every known-outcome failure (a
// server-answered refusal, a safe-point cancellation) lets the creator mark
// FAILED.
func pgCommitOutcomeUnknown(err error) bool {
	if err == nil {
		return false
	}
	var safeCancel *pgSafePointCancelError
	if errors.As(err, &safeCancel) {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// Class 08 (connection exception) and 57P01/57P02 (backend
		// shutdown/crash) leave the outcome unknown; every other
		// server-answered error means the transaction did not commit.
		return strings.HasPrefix(pgErr.Code, "08") || pgErr.Code == "57P01" || pgErr.Code == "57P02"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		// A cancellation surfacing through a store call (not a safe point)
		// raced an in-flight statement.
		return true
	}
	if errors.Is(err, storepostgres.ErrConnect) || errors.Is(err, storepostgres.ErrBatchWrite) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	return false
}

// joinApplicationName appends application_name to a URL-form DSN that does
// not already carry one. Keyword/value DSNs have no URL query to set, so
// those callers keep an untagged connection rather than a guessed syntax.
func joinApplicationName(dsn, application string) string {
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return dsn
	}
	query := parsed.Query()
	if query.Get("application_name") != "" {
		return dsn
	}
	query.Set("application_name", application)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// pgRandomHexToken returns 12 hex characters of crypto-random entropy for
// unique application names.
func pgRandomHexToken() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "000000000000"
	}
	return fmt.Sprintf("%x", b[:])
}
