package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	catalogstore "github.com/calypr/loom/internal/backends/postgres/internal/catalog"
	chartifactstore "github.com/calypr/loom/internal/backends/postgres/internal/chartifact"
	datasetstore "github.com/calypr/loom/internal/backends/postgres/internal/dataset"
	explorerstore "github.com/calypr/loom/internal/backends/postgres/internal/explorer"
	publicationstore "github.com/calypr/loom/internal/backends/postgres/internal/publication"
	recipeexecstore "github.com/calypr/loom/internal/backends/postgres/internal/recipeexec"
	recipesstore "github.com/calypr/loom/internal/backends/postgres/internal/recipes"
	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/catalog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/calypr/loom/internal/backends/postgres/internal/redact"

	publicationcontract "github.com/calypr/loom/internal/dataset"
)

// Rows and Row are the structural read shapes of the runtime's streaming
// query surface. They declare exactly the methods a caller may use on a
// result (the same subset the row-visitor boundary exposes), which keeps
// the control plane free of any driver import: the concrete value behind
// them is the pool's own cursor, satisfied structurally.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Values() ([]any, error)
	Err() error
	Close()
}

type Row interface {
	Scan(dest ...any) error
}

// Runtime is the control-plane composition returned by Open: typed domain
// ports, the pool behind narrow structural surfaces, and lifecycle hooks.
// It is deliberately a struct of exported fields and not a service locator:
// no map lookups, no generic Service() accessor, and no field exposes a pgx
// pool, a driver type, or a private adapter type (asserted by the backend
// ownership contract). The row-execution ports (SourceRows, PlanJSON) live
// on the postgres.Runtime that embeds this one.
type Runtime struct {
	// Manifests is the dataset generation lifecycle port (create, read,
	// CAS transition, active resolution, activation CAS) over the
	// DatasetReleaseSeries assignment.
	Manifests publicationcontract.ManifestLifecycle
	// The four catalog evidence ports a PostgreSQL generation load drives
	// after the resource and edge passes, wired over the source schema.
	FieldProfiles catalog.FieldProfileWriter
	Inventory     catalog.InventoryRebuilder
	Relationships catalog.RelationshipDiscoverer
	Contributions catalog.SemanticContributor
	// Catalog is the full catalog store over the source series: the value
	// behind the four narrow evidence ports above, and additionally the
	// semantic-inventory read lane (PageSemanticInventory,
	// ResolveSemanticInventorySelections) and the capability-evidence reads
	// (DiscoverResourceInventory, DiscoverRelationshipObservations,
	// DiscoverFieldEnrichment) the Explorer capability resolver consumes.
	Catalog *catalogstore.Store
	// Releases is the dataset release repository (save, read-active,
	// compare-and-swap activate) over the dataset_release series.
	Releases *datasetstore.ReleaseStore
	// SemanticReads is the semantic-inventory read lane over the source
	// series (PageSemanticInventory, ResolveSemanticInventorySelections)
	// consumed by the Explorer authoring compile path.
	SemanticReads *catalogstore.SemanticReadRepository

	// Data-plane ports (pg-runtime-server-composition). Each is assembled
	// only when its owning series is assigned in Config; a consumer that
	// needs an unassigned port treats the nil as a configuration refusal,
	// never a silent zero value. The Explorer family is hosted by
	// explorer_control; Publish crosses into dataset_release (it moves the
	// active project release pointer) and is assembled only when both are
	// assigned. Bundles and Chartifacts are hosted by the publication_control
	// ledger; RecipeRevisions by recipe_chains; RecipeExecutions by
	// recipe_exec_registries.
	Explorer            *explorerstore.Store
	Publish             *explorerstore.PublishStore
	Interpretations     *explorerstore.InterpretationStore
	ExplicitGroups      *explorerstore.ExplicitGroupStore
	CapabilitySnapshots *explorerstore.CapabilitySnapshotStore
	TableShapes         *explorerstore.TableShapeCapabilityStore
	Bundles             *publicationstore.BundleCatalog
	Chartifacts         *chartifactstore.Catalog
	RecipeRevisions     *recipesstore.RevisionRegistry
	RecipeExecutions    *recipeexecstore.Registry

	client        *pgstore.Client
	sourceSchema  string
	datasetSchema string
	// schemas is the validated full placement copied from Config at Open:
	// the readiness probe verifies every assigned series' ledger, so every
	// assigned schema must stay reachable (pg-runtime-health-readiness).
	schemas  map[Series]string
	closeOne sync.Once
	closeErr error
	closed   bool
	mu       sync.Mutex
}

// Compile-time port conformance: signature drift between these adapters and
// their neutral contracts fails the build, not the live run. (The
// rows.Querier conformance assertion lives in the postgres package, the
// only place the rows adapter may be imported without an import cycle.)
var (
	_ publicationcontract.ManifestLifecycle = (*datasetstore.Store)(nil)
	_ catalog.FieldProfileWriter            = (*catalogstore.Store)(nil)
	_ catalog.InventoryRebuilder            = (*catalogstore.Store)(nil)
	_ catalog.RelationshipDiscoverer        = (*catalogstore.Store)(nil)
	_ catalog.SemanticContributor           = (*catalogstore.SemanticContributionRepository)(nil)
)

// Open validates the configuration, opens exactly one readiness-gated
// PostgreSQL 18 pool for the selected backend, applies the approved schema
// setup policy, and assembles the control-plane adapters. Every failure
// path closes the resources created so far; no half-initialized runtime
// escapes. The error text never carries connection credentials while the
// cause chain stays intact for errors.Is/As classification.
func Open(ctx context.Context, cfg Config) (*Runtime, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	applicationName := cfg.ApplicationName
	if applicationName == "" {
		nonce, err := connectionNonce()
		if err != nil {
			return nil, fmt.Errorf("%w: generate application name: %v", ErrInvalidConfig, err)
		}
		applicationName = ApplicationNamePrefix + nonce
	}
	password := redact.ExtractDSNPassword(cfg.DSN)

	client, err := pgstore.Open(ctx, pgstore.Config{
		DSN:            withApplicationName(cfg.DSN, applicationName),
		MaxConns:       cfg.MaxConns,
		MinConns:       cfg.MinConns,
		AcquireTimeout: cfg.AcquireTimeout,
	})
	if err != nil {
		return nil, redactWrap(ErrBootstrap, "open postgres source/control pool", err, password)
	}

	rt := &Runtime{client: client}
	// Retain the full validated placement: the readiness probe verifies
	// every assigned series' ledger, so the runtime must know all assigned
	// schemas, not only the two the migration helpers address.
	if len(cfg.Schemas) > 0 {
		rt.schemas = make(map[Series]string, len(cfg.Schemas))
		for series, schema := range cfg.Schemas {
			rt.schemas[series] = schema
		}
	}
	if schema, assigned := cfg.Schemas[SeriesSource]; assigned {
		rt.sourceSchema = schema
	}
	if schema, assigned := cfg.Schemas[SeriesDatasetRelease]; assigned {
		rt.datasetSchema = schema
	}
	if err := rt.setup(ctx, cfg); err != nil {
		rt.Close()
		return nil, err
	}
	if err := rt.assembleControlPorts(cfg); err != nil {
		rt.Close()
		return nil, err
	}
	if err := rt.assembleDataPlanePorts(cfg); err != nil {
		rt.Close()
		return nil, err
	}
	return rt, nil
}

// setup applies the approved schema setup policy for every assigned series.
func (r *Runtime) setup(ctx context.Context, cfg Config) error {
	if cfg.Setup == SetupNone {
		return nil
	}
	password := redact.ExtractDSNPassword(cfg.DSN)
	for _, series := range AllSeries() {
		schema, assigned := cfg.Schemas[series]
		if !assigned {
			continue
		}
		var err error
		switch series {
		case SeriesSource:
			_, err = pgstore.MigrateSchema(ctx, r.client, schema)
		case SeriesExplorerControl:
			_, err = explorerstore.MigrateSchema(ctx, r.client, schema)
		case SeriesPublicationControl:
			_, err = publicationstore.MigrateSchema(ctx, r.client, schema)
		case SeriesDatasetRelease:
			_, err = datasetstore.MigrateSchema(ctx, r.client, schema)
		case SeriesRecipeChains:
			_, err = recipesstore.MigrateSchema(ctx, r.client, schema)
		case SeriesRecipeExecRegistries:
			_, err = recipeexecstore.MigrateSchema(ctx, r.client, schema)
		}
		if err != nil {
			return redactWrap(ErrBootstrap, fmt.Sprintf("schema setup series %q in %q under policy %q", series, schema, cfg.Setup), err, password)
		}
	}
	return nil
}

// assembleControlPorts wires the dataset lifecycle port over the
// DatasetReleaseSeries assignment and the four catalog evidence ports over
// the source assignment. An unassigned series leaves its port nil: a
// consumer that needs it must assign the series in Config, and the port
// nil-ness is a configuration refusal, never a silent zero value at use
// time. Every constructor keeps its own validation; the assembly performs
// no computation of its own.
func (r *Runtime) assembleControlPorts(cfg Config) error {
	password := redact.ExtractDSNPassword(cfg.DSN)
	if schema, assigned := cfg.Schemas[SeriesDatasetRelease]; assigned {
		store, err := datasetstore.New(r.client, schema)
		if err != nil {
			return redactWrap(ErrBootstrap, fmt.Sprintf("assemble dataset manifest lifecycle in %q", schema), err, password)
		}
		r.Manifests = store
	}
	schema, assigned := cfg.Schemas[SeriesSource]
	if !assigned {
		return nil
	}
	profileRepo, err := pgstore.NewFieldProfileRepository(r.client, schema)
	if err != nil {
		return redactWrap(ErrBootstrap, fmt.Sprintf("assemble field profile repository in %q", schema), err, password)
	}
	profiles, err := catalogstore.NewFieldProfileStore(profileRepo)
	if err != nil {
		return redactWrap(ErrBootstrap, fmt.Sprintf("assemble field profile store in %q", schema), err, password)
	}
	r.FieldProfiles = profiles
	inventoryRepo, err := pgstore.NewInventoryRepository(r.client, schema)
	if err != nil {
		return redactWrap(ErrBootstrap, fmt.Sprintf("assemble inventory repository in %q", schema), err, password)
	}
	inventory, err := catalogstore.New(inventoryRepo)
	if err != nil {
		return redactWrap(ErrBootstrap, fmt.Sprintf("assemble inventory store in %q", schema), err, password)
	}
	r.Inventory = inventory
	r.Catalog = inventory
	relationshipsRepo, err := catalogstore.NewRelationshipEvidenceRepository(r.client, schema)
	if err != nil {
		return redactWrap(ErrBootstrap, fmt.Sprintf("assemble relationship evidence repository in %q", schema), err, password)
	}
	relationships, err := catalogstore.NewRelationshipEvidenceStore(relationshipsRepo)
	if err != nil {
		return redactWrap(ErrBootstrap, fmt.Sprintf("assemble relationship evidence store in %q", schema), err, password)
	}
	r.Relationships = relationships
	contributions, err := catalogstore.NewSemanticContributionRepository(r.client, schema)
	if err != nil {
		return redactWrap(ErrBootstrap, fmt.Sprintf("assemble semantic contribution repository in %q", schema), err, password)
	}
	r.Contributions = contributions
	semanticReads, err := catalogstore.NewSemanticReadRepository(r.client, schema)
	if err != nil {
		return redactWrap(ErrBootstrap, fmt.Sprintf("assemble semantic read repository in %q", schema), err, password)
	}
	r.SemanticReads = semanticReads
	return nil
}

// leasedDB presents the runtime's one readiness-gated pool through the
// structural Exec/Query/QueryRow/Begin contract that the publication,
// recipe-chain, recipe-execution, and private-artifact constructors require.
// It is deliberately behaviorally identical to the *pgxpool.Pool method set
// those constructors accept in witnesses: Query and QueryRow delegate to the
// store client's own lease-aware methods (same acquire-timeout discipline as
// the control plane), Exec leases one connection for the statement, and
// Begin binds a leased connection to the returned transaction, releasing it
// exactly once on Commit or Rollback -- the standing pgx contract that every
// transaction in this backend already honors. One leasedDB value is shared
// by all data-plane stores of a runtime, so consumers that verify handle
// identity (the reconciler's registry/pins same-database law) compare one
// pointer. The type adds no semantics: same pool, same leases, no second
// connection domain.
type leasedDB struct {
	client *pgstore.Client
}

func (d *leasedDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return d.client.Query(ctx, sql, args...)
}

func (d *leasedDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return d.client.QueryRow(ctx, sql, args...)
}

func (d *leasedDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	conn, err := d.client.Acquire(ctx)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	defer conn.Release()
	return conn.Exec(ctx, sql, args...)
}

func (d *leasedDB) Begin(ctx context.Context) (pgx.Tx, error) {
	conn, err := d.client.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		conn.Release()
		return nil, err
	}
	return &leasedTx{Tx: tx, releaseOne: &sync.Once{}, release: conn.Release}, nil
}

// leasedTx completes the Begin lease: the connection returns to the pool on
// whichever of Commit or Rollback the caller performs (pgx requires exactly
// one; the once-guard makes an accidental second call a no-op instead of a
// pool misuse).
type leasedTx struct {
	pgx.Tx
	releaseOne *sync.Once
	release    func()
}

func (t *leasedTx) Commit(ctx context.Context) error {
	err := t.Tx.Commit(ctx)
	t.releaseOne.Do(t.release)
	return err
}

func (t *leasedTx) Rollback(ctx context.Context) error {
	err := t.Tx.Rollback(ctx)
	t.releaseOne.Do(t.release)
	return err
}

// assembleDataPlanePorts wires the read- and write-plane adapters a fully
// composed PostgreSQL server needs, each over its owning series' assigned
// schema: the Explorer family over explorer_control (with Publish, which
// additionally requires the dataset_release assignment because it moves the
// active project release pointer), the dataset release repository over
// dataset_release, the bundle catalog and private-artifact catalog over
// publication_control (both hosted by that series' ledger, so one schema
// and one pool can never straddle), the recipe revision chain over
// recipe_chains, and the recipe execution registry over
// recipe_exec_registries. An unassigned series leaves its ports nil; the
// consumer's need for a nil port is a configuration refusal at the
// composition boundary, never a silent zero value at use time. Every
// constructor keeps its own validation; the assembly performs no
// computation of its own.
func (r *Runtime) assembleDataPlanePorts(cfg Config) error {
	password := redact.ExtractDSNPassword(cfg.DSN)
	// One handle for every DB-seam store, so handle-identity laws downstream
	// (the reconciler's registry/pins same-database check) compare one value.
	dataDB := &leasedDB{client: r.client}
	if schema, assigned := cfg.Schemas[SeriesDatasetRelease]; assigned {
		releases, err := datasetstore.NewReleaseStore(r.client, schema)
		if err != nil {
			return redactWrap(ErrBootstrap, fmt.Sprintf("assemble dataset release repository in %q", schema), err, password)
		}
		r.Releases = releases
	}
	if schema, assigned := cfg.Schemas[SeriesExplorerControl]; assigned {
		explorer, err := explorerstore.New(r.client, schema)
		if err != nil {
			return redactWrap(ErrBootstrap, fmt.Sprintf("assemble Explorer store in %q", schema), err, password)
		}
		r.Explorer = explorer
		interpretations, err := explorerstore.NewInterpretationStore(r.client, schema)
		if err != nil {
			return redactWrap(ErrBootstrap, fmt.Sprintf("assemble interpretation store in %q", schema), err, password)
		}
		r.Interpretations = interpretations
		explicitGroups, err := explorerstore.NewExplicitGroupStore(r.client, schema)
		if err != nil {
			return redactWrap(ErrBootstrap, fmt.Sprintf("assemble explicit group store in %q", schema), err, password)
		}
		r.ExplicitGroups = explicitGroups
		snapshots, err := explorerstore.NewCapabilitySnapshotStore(r.client, schema)
		if err != nil {
			return redactWrap(ErrBootstrap, fmt.Sprintf("assemble capability snapshot store in %q", schema), err, password)
		}
		r.CapabilitySnapshots = snapshots
		tableShapes, err := explorerstore.NewTableShapeCapabilityStore(r.client, schema)
		if err != nil {
			return redactWrap(ErrBootstrap, fmt.Sprintf("assemble table-shape capability store in %q", schema), err, password)
		}
		r.TableShapes = tableShapes
		if datasetSchema, datasetAssigned := cfg.Schemas[SeriesDatasetRelease]; datasetAssigned {
			publish, err := explorerstore.NewPublishStore(r.client, schema, datasetSchema)
			if err != nil {
				return redactWrap(ErrBootstrap, fmt.Sprintf("assemble Explorer publish store across %q and %q", schema, datasetSchema), err, password)
			}
			r.Publish = publish
		}
	}
	if schema, assigned := cfg.Schemas[SeriesPublicationControl]; assigned {
		bundles, err := publicationstore.NewBundleCatalog(dataDB, schema)
		if err != nil {
			return redactWrap(ErrBootstrap, fmt.Sprintf("assemble bundle catalog in %q", schema), err, password)
		}
		r.Bundles = bundles
		// The private ClickHouse artifact catalog's tables are hosted by the
		// publication_control ledger (chartifact package note), so it shares
		// the bundle catalog's schema assignment by construction.
		chartifacts, err := chartifactstore.NewCatalog(dataDB, schema)
		if err != nil {
			return redactWrap(ErrBootstrap, fmt.Sprintf("assemble private artifact catalog in %q", schema), err, password)
		}
		r.Chartifacts = chartifacts
	}
	if schema, assigned := cfg.Schemas[SeriesRecipeChains]; assigned {
		revisions, err := recipesstore.New(dataDB, schema)
		if err != nil {
			return redactWrap(ErrBootstrap, fmt.Sprintf("assemble recipe revision registry in %q", schema), err, password)
		}
		r.RecipeRevisions = revisions
	}
	if schema, assigned := cfg.Schemas[SeriesRecipeExecRegistries]; assigned {
		executions, err := recipeexecstore.New(dataDB, schema)
		if err != nil {
			return redactWrap(ErrBootstrap, fmt.Sprintf("assemble recipe execution registry in %q", schema), err, password)
		}
		r.RecipeExecutions = executions
	}
	return nil
}

// NewPublicationReconciler binds the abandoned-execution sweep to the
// runtime's own bundle registry and pin arbiter (one pool, one schema, as
// NewBundleCatalog guarantees) with the caller's active-release reader --
// pass the runtime's Releases repository for the PUBLISHED janitor arm, or
// nil to skip it, the same wiring rule the ClickHouse store enforces. The
// sweep's physical drop hook is caller-owned because the physical tables
// live in ClickHouse, not here.
func (r *Runtime) NewPublicationReconciler(releases publicationcontract.ActiveReleaseResolver) (*publicationstore.Reconciler, error) {
	if r.isClosed() {
		return nil, ErrRuntimeClosed
	}
	if r.Bundles == nil {
		return nil, fmt.Errorf("%w: publication_control series is not assigned; the reconciler needs the bundle registry and its pin arbiter", ErrInvalidConfig)
	}
	return publicationstore.NewReconciler(r.Bundles.Registry, r.Bundles.Pins(), releases)
}

// ConnSource exposes the pool behind the store's connection-source
// contract, so callers construct store-side writers and repositories over
// the runtime's own pool without naming the pool type.
func (r *Runtime) ConnSource() ConnSource { return r.client }

// QuerierAsAny hands the pool client to the postgres package, the only
// legal importer of the rows adapter, as an untyped value: the rows
// adapter's Querier contract is then asserted at compile time in that
// package (var _ rows.Querier = (*store.Client)(nil)) and re-checked once
// here with a fail-closed assertion, so the bridge never silently degrades
// to reflection at row-consumption time.
func (r *Runtime) QuerierAsAny() any {
	if r.isClosed() {
		return nil
	}
	return r.client
}

// Query runs one statement on the pool and streams its rows behind the
// structural Rows surface, honoring the configured acquire timeout. The
// caller must close the returned Rows.
func (r *Runtime) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	if r.isClosed() {
		return nil, ErrRuntimeClosed
	}
	return r.client.Query(ctx, sql, args...)
}

// QueryRow runs one statement expected to yield at most one row, behind
// the structural Row surface.
func (r *Runtime) QueryRow(ctx context.Context, sql string, args ...any) Row {
	if r.isClosed() {
		return closedRow{}
	}
	return r.client.QueryRow(ctx, sql, args...)
}

type closedRow struct{}

func (closedRow) Scan(dest ...any) error { return ErrRuntimeClosed }

// MigrateDatasetReleaseSchema applies the dataset_release series' owned
// migrations to its assigned schema over the runtime pool, idempotently and
// advisory-locked — the manifest lane's counterpart to MigrateSchema for
// callers that own bootstrap explicitly. It never creates a schema: an
// absent assigned schema is a named refusal, and schema lifecycle stays
// with the caller.
func (r *Runtime) MigrateDatasetReleaseSchema(ctx context.Context) (MigrateResult, error) {
	if r.isClosed() {
		return MigrateResult{}, ErrRuntimeClosed
	}
	if r.datasetSchema == "" {
		return MigrateResult{}, fmt.Errorf("%w: dataset_release series is not assigned; manifest-lane migrations need its schema", ErrInvalidConfig)
	}
	return datasetstore.MigrateSchema(ctx, r.client, r.datasetSchema)
}

// MigrateSchema applies every owned source-series migration to the assigned
// source schema over the runtime's own pool, idempotently and under an
// advisory lock. It never creates a schema: when the assigned schema is
// absent the migration runner refuses, and the caller owns schema lifecycle
// (the live Open witness pins exactly that refusal under SetupMigrate). The
// generation-load bootstrap calls this while its own bootstrap stage is
// measured; the SetupMigrate policy covers callers that want setup folded
// into the open.
func (r *Runtime) MigrateSchema(ctx context.Context) (MigrateResult, error) {
	if r.isClosed() {
		return MigrateResult{}, ErrRuntimeClosed
	}
	if r.sourceSchema == "" {
		return MigrateResult{}, fmt.Errorf("%w: source series schema is not assigned", ErrInvalidConfig)
	}
	return pgstore.MigrateSchema(ctx, r.client, r.sourceSchema)
}

// Health reports whether the backend is reachable and still the selected
// PostgreSQL 18 service. It is the narrow liveness hook: no pool internals.
func (r *Runtime) Health(ctx context.Context) error {
	if r.isClosed() {
		return ErrRuntimeClosed
	}
	return r.client.Ping(ctx)
}

// Readiness is the core-readiness probe for the selected PostgreSQL backend
// (pg-runtime-health-readiness). It is Health's reachability and version
// verdict plus, for every assigned series, the read-only ledger probe
// (store.MigrationRunner.Verify through each series' VerifySchema): a
// selected backend whose schema was never migrated, migrated away from, or
// edited under the ledger is NOT ready, and the returned error classifies
// which via errors.Is against the factory schema-state sentinels
// (ErrInvalidSchemaName, ErrMigrationPending, ErrMigrationChecksumMismatch,
// ErrFutureSchemaVersion, ErrMigrationMissing, ErrMigrationOutOfOrder),
// while connectivity keeps ErrConnect and the version gate keeps
// ErrUnsupportedVersion. The probe acquires no migration lock, applies no
// DDL, and writes no ledger row: a readiness read can never mutate the
// store. Unassigned series are skipped, never probed.
func (r *Runtime) Readiness(ctx context.Context) error {
	if err := r.Health(ctx); err != nil {
		return err
	}
	for _, series := range AllSeries() {
		schema, assigned := r.schemas[series]
		if !assigned || strings.TrimSpace(schema) == "" {
			continue
		}
		var err error
		switch series {
		case SeriesSource:
			err = pgstore.VerifySchema(ctx, r.client, schema)
		case SeriesExplorerControl:
			err = explorerstore.VerifySchema(ctx, r.client, schema)
		case SeriesPublicationControl:
			err = publicationstore.VerifySchema(ctx, r.client, schema)
		case SeriesDatasetRelease:
			err = datasetstore.VerifySchema(ctx, r.client, schema)
		case SeriesRecipeChains:
			err = recipesstore.VerifySchema(ctx, r.client, schema)
		case SeriesRecipeExecRegistries:
			err = recipeexecstore.VerifySchema(ctx, r.client, schema)
		}
		if err != nil {
			return fmt.Errorf("postgres backend: schema readiness for series %q: %w", series, err)
		}
	}
	return nil
}

// Close releases the pool. It is safe to call concurrently and repeatedly:
// the underlying pool close runs once; later calls return the same verdict.
func (r *Runtime) Close() error {
	r.closeOne.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.mu.Unlock()
		r.client.Close()
	})
	return r.closeErr
}

func (r *Runtime) isClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

func connectionNonce() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// withApplicationName tags the DSN's backends so pg_stat_activity witnesses
// exactly the connections this runtime owns. A DSN that already carries an
// application_name keeps it verbatim: the consumer's own witness tag (the
// loader's attribution tag, a test suite's named pool) must survive to
// pg_stat_activity, and the own-prefix nonce is only minted when nothing
// tagged the connection.
func withApplicationName(dsn, applicationName string) string {
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		q := u.Query()
		if q.Get("application_name") != "" {
			return dsn
		}
		q.Set("application_name", applicationName)
		u.RawQuery = q.Encode()
		return u.String()
	}
	if strings.Contains(strings.ToLower(dsn), "application_name=") {
		return dsn
	}
	return dsn + " application_name=" + applicationName
}

// redactWrap keeps causes reachable (errors.Is/As) while guaranteeing the
// rendered text never carries the connection credential.
type redactedError struct {
	msg   string
	cause error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.cause }

func redactWrap(class error, op string, cause error, secret string) error {
	text := redact.ScrubSecretText(fmt.Sprintf("%v: %v", op, cause), secret)
	return &redactedError{
		msg:   text,
		cause: errors.Join(class, cause),
	}
}
