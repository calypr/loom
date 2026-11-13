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

type Runtime struct {
	Manifests     publicationcontract.ManifestLifecycle
	FieldProfiles catalog.FieldProfileWriter
	Inventory     catalog.InventoryRebuilder
	Relationships catalog.RelationshipDiscoverer
	Contributions catalog.SemanticContributor
	Catalog       *catalogstore.Store
	Releases      *datasetstore.ReleaseStore
	SemanticReads *catalogstore.SemanticReadRepository

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
	schemas       map[Series]string
	closeOne      sync.Once
	closeErr      error
	closed        bool
	mu            sync.Mutex
}

var (
	_ publicationcontract.ManifestLifecycle = (*datasetstore.Store)(nil)
	_ catalog.FieldProfileWriter            = (*catalogstore.Store)(nil)
	_ catalog.InventoryRebuilder            = (*catalogstore.Store)(nil)
	_ catalog.RelationshipDiscoverer        = (*catalogstore.Store)(nil)
	_ catalog.CapabilityEvidenceReader      = (*catalogstore.Store)(nil)
	_ catalog.SemanticContributor           = (*catalogstore.SemanticContributionRepository)(nil)
	_ catalog.InventoryRebuilder            = (*catalogstore.InventoryAdapter)(nil)
	_ catalog.FieldProfileWriter            = (*catalogstore.FieldProfileAdapter)(nil)
	_ catalog.RelationshipDiscoverer        = (*catalogstore.RelationshipAdapter)(nil)
)

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
	inventoryRepo, err := pgstore.NewInventoryRepository(r.client, schema)
	if err != nil {
		return redactWrap(ErrBootstrap, fmt.Sprintf("assemble inventory repository in %q", schema), err, password)
	}
	relationshipsRepo, err := catalogstore.NewRelationshipEvidenceRepository(r.client, schema)
	if err != nil {
		return redactWrap(ErrBootstrap, fmt.Sprintf("assemble relationship evidence repository in %q", schema), err, password)
	}
	catalogStore, err := catalogstore.NewFullStore(inventoryRepo, profileRepo, relationshipsRepo)
	if err != nil {
		return redactWrap(ErrBootstrap, fmt.Sprintf("assemble catalog store in %q", schema), err, password)
	}
	r.FieldProfiles = catalogStore
	r.Inventory = catalogStore
	r.Catalog = catalogStore
	r.Relationships = catalogStore
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

func (r *Runtime) assembleDataPlanePorts(cfg Config) error {
	password := redact.ExtractDSNPassword(cfg.DSN)
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

func (r *Runtime) NewPublicationReconciler(releases publicationcontract.ActiveReleaseResolver) (*publicationstore.Reconciler, error) {
	if r.isClosed() {
		return nil, ErrRuntimeClosed
	}
	if r.Bundles == nil {
		return nil, fmt.Errorf("%w: publication_control series is not assigned; the reconciler needs the bundle registry and its pin arbiter", ErrInvalidConfig)
	}
	return publicationstore.NewReconciler(r.Bundles.Registry, r.Bundles.Pins(), releases)
}

func (r *Runtime) ConnSource() ConnSource { return r.client }

func (r *Runtime) PoolClient() *Client {
	if r.isClosed() {
		return nil
	}
	return r.client
}

func (r *Runtime) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	if r.isClosed() {
		return nil, ErrRuntimeClosed
	}
	return r.client.Query(ctx, sql, args...)
}

func (r *Runtime) QueryRow(ctx context.Context, sql string, args ...any) Row {
	if r.isClosed() {
		return closedRow{}
	}
	return r.client.QueryRow(ctx, sql, args...)
}

type closedRow struct{}

func (closedRow) Scan(dest ...any) error { return ErrRuntimeClosed }

func (r *Runtime) MigrateDatasetReleaseSchema(ctx context.Context) (MigrateResult, error) {
	if r.isClosed() {
		return MigrateResult{}, ErrRuntimeClosed
	}
	if r.datasetSchema == "" {
		return MigrateResult{}, fmt.Errorf("%w: dataset_release series is not assigned; manifest-lane migrations need its schema", ErrInvalidConfig)
	}
	return datasetstore.MigrateSchema(ctx, r.client, r.datasetSchema)
}

func (r *Runtime) MigrateSchema(ctx context.Context) (MigrateResult, error) {
	if r.isClosed() {
		return MigrateResult{}, ErrRuntimeClosed
	}
	if r.sourceSchema == "" {
		return MigrateResult{}, fmt.Errorf("%w: source series schema is not assigned", ErrInvalidConfig)
	}
	return pgstore.MigrateSchema(ctx, r.client, r.sourceSchema)
}

func (r *Runtime) Health(ctx context.Context) error {
	if r.isClosed() {
		return ErrRuntimeClosed
	}
	return r.client.Ping(ctx)
}

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
