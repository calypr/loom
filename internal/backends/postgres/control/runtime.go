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
	datasetstore "github.com/calypr/loom/internal/backends/postgres/internal/dataset"
	explorerstore "github.com/calypr/loom/internal/backends/postgres/internal/explorer"
	publicationstore "github.com/calypr/loom/internal/backends/postgres/internal/publication"
	recipeexecstore "github.com/calypr/loom/internal/backends/postgres/internal/recipeexec"
	recipesstore "github.com/calypr/loom/internal/backends/postgres/internal/recipes"
	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/catalog"

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

	client       *pgstore.Client
	sourceSchema string
	closeOne     sync.Once
	closeErr     error
	closed       bool
	mu           sync.Mutex
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
	if schema, assigned := cfg.Schemas[SeriesSource]; assigned {
		rt.sourceSchema = schema
	}
	if err := rt.setup(ctx, cfg); err != nil {
		rt.Close()
		return nil, err
	}
	if err := rt.assembleControlPorts(cfg); err != nil {
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
	return nil
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

// MigrateSchema creates the assigned source schema when missing and applies
// every owned source-series migration to it, idempotently, over the
// runtime's own pool. The generation-load bootstrap calls this while its
// own bootstrap stage is measured; the SetupMigrate policy covers callers
// that want setup folded into the open.
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
