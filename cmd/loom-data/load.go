package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"runtime/trace"
	"strconv"
	"strings"
	"time"

	driver "github.com/arangodb/go-driver/v2/arangodb"
	"github.com/arangodb/go-driver/v2/connection"
	"github.com/calypr/loom/internal/catalog"
	catalogpostgres "github.com/calypr/loom/internal/catalog/postgres"
	publication "github.com/calypr/loom/internal/dataset"
	datasetpostgres "github.com/calypr/loom/internal/dataset/postgres"
	"github.com/calypr/loom/internal/ingest"
	storepostgres "github.com/calypr/loom/internal/store/postgres"
)

// Backend tags are the only routing keys. The tag is resolved and validated
// before any backend-specific setting is parsed (the tag-first law of the
// PostgreSQL command leaf, internal/dataframe/postgres/rows.go:158-181: a
// mistagged request is rejected "before the Querier is touched"), and the
// dispatch switch is exhaustive with a fail-closed default, mirroring
// internal/dataframe/execution/engine.go:1679-1701. There is no URL-based
// auto-detection and no fallback (internal/server/options.go:403-417).
type backendTag string

const (
	backendArango   backendTag = "arango"
	backendPostgres backendTag = "postgres"
)

// Defaults copied from cmd/arango-fhir-proto/main.go:30-36 so the Arango
// route's flag defaults are byte-identical.
const (
	defaultURL      = "http://127.0.0.1:8529"
	defaultDatabase = "fhir_proto"
	defaultProject  = "ARANGODB_PROTO"
	defaultSchema   = "schemas/graph-fhir.json"
	defaultMetaDir  = "META"
)

// pgDSNEnv is the environment spelling of the PostgreSQL DSN. Exactly one of
// --pg-dsn and this variable may supply the DSN (D2): the flag never takes
// precedence over the environment and vice versa — a both-set invocation is
// refused, following the "owned by one source" conflict law of
// internal/server/options.go:437-449.
const pgDSNEnv = "LOOM_DATA_PG_DSN"

// Typed refusals for misrouting. Every message names the setting only and
// never echoes a value that could carry a credential
// (internal/server/options.go:430 law).
var (
	ErrBackendRequired          = errors.New("loom-data: backend is required")
	ErrBackendUnknown           = errors.New("loom-data: backend must be arango or postgres")
	ErrProjectRequired          = errors.New("loom-data: --project is required for the postgres backend")
	ErrPGDSNRequired            = errors.New("loom-data: a PostgreSQL DSN is required: set --pg-dsn or LOOM_DATA_PG_DSN, never both")
	ErrPGDSNConflict            = errors.New("loom-data: --pg-dsn and LOOM_DATA_PG_DSN cannot both be set; exactly one source is required")
	ErrPGSchemaRequired         = errors.New("loom-data: --pg-schema is required for the postgres backend")
	ErrPGTruncateForbidden      = errors.New("loom-data: --truncate is not permitted for a postgres generation load")
	ErrPGApplicationNameInvalid = errors.New("loom-data: --pg-application-name must be 1-63 bytes of letters, digits, dot, underscore, or hyphen, starting with a letter or digit")
	ErrPGMaxConnsInvalid        = errors.New("loom-data: --pg-max-conns must be between 0 and 2147483647")
)

// pgApplicationNameRE bounds --pg-application_name so the value can never
// smuggle credential-shaped or syntax-bearing content into a DSN through the
// loader's application_name join (internal/ingest/generation_load.go:1275-1289).
var pgApplicationNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// inspectLoadGenerationArgs is the stage-1 scan: it locates only --backend
// and the help switches without interpreting any other setting, so the tag
// verdict exists before a backend-specific FlagSet is ever built. Scanning
// stops at "--" like the flag package.
func inspectLoadGenerationArgs(args []string) (backend string, backendSet bool, help bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			return
		case arg == "-h" || arg == "--help":
			help = true
		case strings.HasPrefix(arg, "--backend="):
			backend, backendSet = arg[len("--backend="):], true
		case strings.HasPrefix(arg, "-backend="):
			backend, backendSet = arg[len("-backend="):], true
		case arg == "--backend" || arg == "-backend":
			backendSet = true
			if i+1 < len(args) {
				backend = args[i+1]
				i++
			}
		}
	}
	return
}

// normalizeBackendTag resolves the strict backend enum. Unlike the server
// selector (internal/server/options.go:406-410 defaults empty to arango),
// omission here is a refusal: this entrypoint requires an explicit backend.
func normalizeBackendTag(raw string, set bool) (backendTag, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if !set || value == "" {
		return "", fmt.Errorf("%w: pass --backend arango or postgres; the backend is never inferred from connection settings", ErrBackendRequired)
	}
	switch value {
	case string(backendArango):
		return backendArango, nil
	case string(backendPostgres):
		return backendPostgres, nil
	default:
		return "", fmt.Errorf("%w: got %q", ErrBackendUnknown, value)
	}
}

// loadRunner is the injectable dependency seam around everything that opens
// a backend or dials a network. Production wiring uses defaultRunner; tests
// substitute recorders so "the PG invocation uses only the PG loader" is
// witnessed by zero-call assertions on every Arango-facing seam, and vice
// versa.
type loadRunner struct {
	loadArango         func(context.Context, ingest.LoadOptions) (ingest.LoadSummary, error)
	loadPostgres       func(context.Context, ingest.LoadOptions, ingest.PGSourceOptions) (ingest.LoadSummary, error)
	requireDatabase    func(context.Context, string, string) error
	openCatalogWriters func(ctx context.Context, dsn, schema, application string) (ingest.GenerationCatalogWriters, func() error, error)
	verifyStaged       func(context.Context, publication.Ref, string, string) error
	printSummary       func(any) error
	printNote          func(string)
}

func defaultRunner() loadRunner {
	return loadRunner{
		loadArango:         ingest.Load,
		loadPostgres:       ingest.LoadPostgresGeneration,
		requireDatabase:    requireDatabaseExists,
		openCatalogWriters: openCatalogWriters,
		verifyStaged:       verifyStagedManifest,
		printSummary:       printJSON,
		printNote:          func(note string) { fmt.Fprintln(os.Stderr, note) },
	}
}

// dispatchLoadGeneration is the exhaustive tagged dispatch. Stage 1 decides
// the tag before any backend dialect is parsed; stage 2 hands the full
// argument list to exactly one route.
func dispatchLoadGeneration(ctx context.Context, args []string, runner loadRunner) error {
	rawBackend, backendSet, help := inspectLoadGenerationArgs(args)
	tag, tagErr := normalizeBackendTag(rawBackend, backendSet)
	if help && tagErr != nil {
		// -h without a usable tag shows the combined two-route usage. With a
		// valid tag the route's own flag package prints the full per-route
		// flag list and exits 0, mirroring the Arango CLI's -h behavior.
		usage()
		return nil
	}
	if tagErr != nil {
		return tagErr
	}
	switch tag {
	case backendArango:
		return runArangoLoadGeneration(ctx, args, runner)
	case backendPostgres:
		return runPostgresLoadGeneration(ctx, args, runner)
	default:
		// Unreachable through normalizeBackendTag; kept so a zero or unknown
		// tag can never reach a loader (engine.go:1698-1700 law).
		return fmt.Errorf("%w: refusing to dispatch with unregistered backend %q", ErrBackendUnknown, string(tag))
	}
}

// ---------------------------------------------------------------------------
// Arango route: the existing implementation, contract-preserved.
// ---------------------------------------------------------------------------

// arangoLoadCommandConfig mirrors cmd/arango-fhir-proto's loadCommandConfig
// (main.go:101-112) field-for-field. These helpers are deliberately
// duplicated rather than extracted from cmd/arango-fhir-proto because the
// ticket scope keeps that command unchanged; the parity is enforced by a
// differential usage-surface test, not by review.
type arangoLoadCommandConfig struct {
	Options ingest.LoadOptions

	CPUProfile   string
	MemProfile   string
	TraceProfile string
	BlockProfile string
	MemoryLimit  string
	MemoryBytes  int64

	Generation string
}

// configureLoadFlagsCore registers the load knobs both routes honor, copied
// verbatim from cmd/arango-fhir-proto/main.go:187-215 (names, defaults, and
// usage strings) except --url, --database, --project, and --write-api, which
// are Arango-only and registered solely by configureLoadFlags so the
// PostgreSQL route refuses them as unknown flags. One shared registration
// means the two routes cannot drift on shared flags; the differential usage
// test in load_test.go compares against the untouched original binary.
func configureLoadFlagsCore(fs *flag.FlagSet, options *ingest.LoadOptions, memoryLimit, cpuProfile, memProfile, traceProfile, blockProfile *string) {
	fs.StringVar(cpuProfile, "cpu-profile", "", "Write CPU profile to file")
	fs.StringVar(memProfile, "mem-profile", "", "Write heap profile to file at end of run")
	fs.StringVar(traceProfile, "trace-profile", "", "Write runtime trace to file")
	fs.StringVar(blockProfile, "block-profile", "", "Write block profile to file at end of run")
	fs.StringVar(memoryLimit, "memory-limit", "4GiB", "Go memory limit for this load, for example 4GiB; 0 disables the soft limit")
	fs.StringVar(&options.Schema, "schema", defaultSchema, "graph-fhir JSON schema")
	fs.StringVar(&options.MetaDir, "meta-dir", defaultMetaDir, "Directory containing META/*.ndjson")
	fs.StringVar(&options.AuthResourcePath, "auth-resource-path", "", "Optional auth resource path copied onto vertex data, for example EllrottLab-GDC_Data")
	fs.IntVar(&options.BatchSize, "batch-size", 5000, "Bulk insert batch size")
	fs.IntVar(&options.ProgressEvery, "progress-every", 50000, "Emit progress every N input rows")
	fs.IntVar(&options.WriterCount, "writers", 2, "Concurrent writer goroutines")
	fs.IntVar(&options.WorkerCount, "workers", 2, "Concurrent parse/profile workers")
	fs.IntVar(&options.LineQueueSize, "line-queue-size", 1024, "Buffered input lines")
	fs.IntVar(&options.WriteQueueSize, "write-queue-size", 8, "Buffered write batches")
	profileLimits := catalog.DefaultProfileLimits()
	fs.IntVar(&options.CatalogLimits.MaxFields, "max-catalog-fields", profileLimits.MaxFields, "Maximum retained catalog fields per resource")
	fs.IntVar(&options.CatalogLimits.MaxDistinctValuesPerField, "max-distinct-values", profileLimits.MaxDistinctValuesPerField, "Maximum retained distinct values per field")
	fs.IntVar(&options.CatalogLimits.MaxDistinctValueBytes, "max-distinct-value-bytes", profileLimits.MaxDistinctValueBytes, "Maximum retained bytes per catalog value")
	fs.IntVar(&options.CatalogLimits.MaxPivotColumnsPerField, "max-pivot-columns", profileLimits.MaxPivotColumnsPerField, "Maximum retained pivot columns per field")
	fs.IntVar(&options.CatalogLimits.MaxExtensionValuesPerField, "max-extension-values", profileLimits.MaxExtensionValuesPerField, "Maximum retained extension observations per field")
	fs.IntVar(&options.CatalogLimits.MaxShapePlans, "max-shape-plans", profileLimits.MaxShapePlans, "Maximum cached payload shape plans")
	fs.BoolVar(&options.FailFast, "fail-fast", false, "Stop on the first decode, validation, or edge conversion error")
	fs.BoolVar(&options.UseGeneric, "use-generic", false, "Use the generic jsonschema + jsonschemagraph validator and extractor")
}

// configureLoadFlags registers the full arango load-generation surface,
// identical to cmd/arango-fhir-proto's configureLoadFlags (main.go:179-182):
// the core plus --url, --database, --project, --write-api, and the required
// --generation. --backend is the routing tag validated in stage 1; it is
// registered here only so the route's parse accepts the already-validated
// value.
func configureLoadFlags(fs *flag.FlagSet, config *arangoLoadCommandConfig) {
	configureLoadFlagsCore(fs, &config.Options, &config.MemoryLimit, &config.CPUProfile, &config.MemProfile, &config.TraceProfile, &config.BlockProfile)
	fs.StringVar(&config.Options.URL, "url", defaultURL, "Backend base URL")
	fs.StringVar(&config.Options.Database, "database", defaultDatabase, "Backend database")
	fs.StringVar(&config.Options.Project, "project", defaultProject, "Project label")
	fs.StringVar(&config.Options.WriteAPI, "write-api", "import", "Bulk write API: import or document")
	fs.StringVar(&config.Generation, "generation", "", "Required opaque immutable dataset generation identifier")
	fs.StringVar(new(string), "backend", "", "Required backend tag: arango or postgres (validated before any flag below)")
}

func parseArangoLoadCommand(args []string, errorHandling flag.ErrorHandling) (arangoLoadCommandConfig, error) {
	fs := flag.NewFlagSet("load-generation", errorHandling)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "loom-data load-generation (arango): identical flag, output, and exit contract to arango-fhir-proto load-generation.")
		fs.PrintDefaults()
	}
	config := arangoLoadCommandConfig{}
	configureLoadFlags(fs, &config)
	if err := fs.Parse(args); err != nil {
		return arangoLoadCommandConfig{}, err
	}
	if err := config.parseMemoryLimit(); err != nil {
		return arangoLoadCommandConfig{}, err
	}
	// Invariants preserved verbatim from
	// cmd/arango-fhir-proto/main.go:163-173.
	if config.Generation == "" {
		return arangoLoadCommandConfig{}, fmt.Errorf("--generation is required for load-generation")
	}
	if config.Options.Truncate {
		return arangoLoadCommandConfig{}, fmt.Errorf("--truncate=true is not permitted for load-generation")
	}
	ref, err := publication.NewRef(config.Options.Project, config.Generation)
	if err != nil {
		return arangoLoadCommandConfig{}, fmt.Errorf("invalid --generation for load-generation: %w", err)
	}
	config.Options.Dataset = &ref
	return config, nil
}

func (config *arangoLoadCommandConfig) parseMemoryLimit() error {
	bytes, err := parseMemoryBytes(config.MemoryLimit)
	if err != nil {
		return fmt.Errorf("invalid --memory-limit: %w", err)
	}
	config.MemoryBytes = bytes
	return nil
}

func runArangoLoadGeneration(ctx context.Context, args []string, runner loadRunner) error {
	config, err := parseArangoLoadCommand(args, flag.ExitOnError)
	if err != nil {
		return err
	}
	if err := runner.requireDatabase(ctx, config.Options.URL, config.Options.Database); err != nil {
		return err
	}
	summary, err := runLoadWithProfiles(ctx,
		profileSettings{CPUProfile: config.CPUProfile, MemProfile: config.MemProfile, TraceProfile: config.TraceProfile, BlockProfile: config.BlockProfile},
		config.MemoryBytes,
		func(loadCtx context.Context) (ingest.LoadSummary, error) {
			return runner.loadArango(loadCtx, config.Options)
		})
	if err != nil {
		return err
	}
	return runner.printSummary(summary)
}

// requireDatabaseExists is copied from cmd/arango-fhir-proto/main.go:294-312
// so the Arango route keeps the refuse-missing-database gate: the store's
// Open keeps auto-creating for server/tests/harness; only the CLI adds this
// gate.
func requireDatabaseExists(ctx context.Context, url, database string) error {
	conn := connection.NewHttpConnection(connection.HttpConfiguration{
		Endpoint: connection.NewRoundRobinEndpoints([]string{url}),
	})
	client := driver.NewClient(conn)
	databaseExists, err := client.DatabaseExists(ctx, database)
	if err != nil {
		return fmt.Errorf("check existing database: %w", err)
	}
	if !databaseExists {
		return fmt.Errorf("refusing to create missing database %q", database)
	}
	return nil
}

// ---------------------------------------------------------------------------
// PostgreSQL route: the completed pg-ingest-03 lifecycle loader, STAGED-only.
// ---------------------------------------------------------------------------

type postgresLoadCommandConfig struct {
	Options ingest.LoadOptions
	PG      ingest.PGSourceOptions

	MemoryLimit string
	MemoryBytes int64

	CPUProfile   string
	MemProfile   string
	TraceProfile string
	BlockProfile string

	Generation    string
	Truncate      bool
	DSNFromFlag   string
	ResolvedDSN   string
	pgMaxConnsInt int
}

// configurePostgresLoadFlags registers the PostgreSQL generation-load
// surface. Arango-only settings (--url, --database, --write-api) are
// deliberately absent so they fail as unknown flags on this route instead of
// being silently ignored — the same law that keeps the mutable `load`
// command from advertising --generation (cmd/arango-fhir-proto/main.go:184-186).
// Honored shared knobs (UseGeneric, FailFast, AuthResourcePath, batching,
// progress, queues, catalog limits, profiles, memory limit) are verified in
// internal/ingest/generation_load.go:751-782, 1171-1174.
func configurePostgresLoadFlags(fs *flag.FlagSet, config *postgresLoadCommandConfig) {
	// One shared core registration with the Arango route keeps the common
	// knobs from drifting.
	configureLoadFlagsCore(fs, &config.Options, &config.MemoryLimit, &config.CPUProfile, &config.MemProfile, &config.TraceProfile, &config.BlockProfile)
	fs.StringVar(&config.Options.Project, "project", "", "Project label (required)")
	fs.StringVar(&config.Generation, "generation", "", "Required opaque immutable dataset generation identifier")
	fs.BoolVar(&config.Truncate, "truncate", false, "Truncate before loading; always refused for a postgres generation load")
	fs.StringVar(new(string), "backend", "", "Required backend tag: arango or postgres (validated before any flag below)")
	fs.StringVar(&config.DSNFromFlag, "pg-dsn", "", "PostgreSQL connection string (mutually exclusive with LOOM_DATA_PG_DSN); never printed")
	fs.StringVar(&config.PG.Schema, "pg-schema", "", "Required PostgreSQL schema the generation is loaded into")
	// PGSourceOptions.MaxConns is int32; flag has no int32 helper, so bind
	// through an int and normalize in parse.
	fs.IntVar(&config.pgMaxConnsInt, "pg-max-conns", 0, "PostgreSQL load pool maximum connections; zero selects the loader default (4)")
	fs.DurationVar(&config.PG.AcquireTimeout, "pg-acquire-timeout", 0, "PostgreSQL pool acquire timeout; zero selects the loader default (30s)")
	fs.StringVar(&config.PG.ApplicationName, "pg-application-name", "", "Optional application_name tag for load backends; never printed")
	fs.IntVar(&config.PG.SemanticFlushThreshold, "semantic-flush-threshold", 0, "Semantic contribution buffer spill threshold; zero selects the loader default")
}

func parsePostgresLoadCommand(args []string, errorHandling flag.ErrorHandling) (postgresLoadCommandConfig, error) {
	fs := flag.NewFlagSet("load-generation", errorHandling)
	fs.Usage = func() {
		// The help text must state the STAGED boundary explicitly and must
		// never imply activation (D3 condition).
		fmt.Fprintln(os.Stderr, "loom-data load-generation (postgres): one bounded load that leaves the generation STAGED (verified) and NOT activated; activation is a separate explicit operation. The DSN is never printed.")
		fs.PrintDefaults()
	}
	config := postgresLoadCommandConfig{}
	configurePostgresLoadFlags(fs, &config)
	if err := fs.Parse(args); err != nil {
		return postgresLoadCommandConfig{}, err
	}
	if config.Generation == "" {
		return postgresLoadCommandConfig{}, fmt.Errorf("--generation is required for load-generation")
	}
	if config.Truncate {
		return postgresLoadCommandConfig{}, ErrPGTruncateForbidden
	}
	bytes, err := parseMemoryBytes(config.MemoryLimit)
	if err != nil {
		return postgresLoadCommandConfig{}, fmt.Errorf("invalid --memory-limit: %w", err)
	}
	config.MemoryBytes = bytes
	if strings.TrimSpace(config.Options.Project) == "" {
		return postgresLoadCommandConfig{}, ErrProjectRequired
	}
	dsn, err := resolvePostgresDSN(fs, config.DSNFromFlag)
	if err != nil {
		return postgresLoadCommandConfig{}, err
	}
	config.ResolvedDSN = dsn
	if strings.TrimSpace(config.PG.Schema) == "" {
		return postgresLoadCommandConfig{}, ErrPGSchemaRequired
	}
	name := strings.TrimSpace(config.PG.ApplicationName)
	switch {
	case name == "":
		// Unset (or blank-only) selects the loader's generated witness tag.
		config.PG.ApplicationName = ""
	case !pgApplicationNameRE.MatchString(name):
		// The value is never echoed: a rejected application name may itself
		// carry credential-shaped content, which is exactly why it is bounded.
		return postgresLoadCommandConfig{}, ErrPGApplicationNameInvalid
	default:
		// Use the trimmed value for both consumers so validation and use
		// stay on one string: the loader trims again at
		// internal/ingest/generation_load.go:591, and openCatalogWriters
		// places this exact value into the catalog DSN's application_name
		// query parameter.
		config.PG.ApplicationName = name
	}
	if config.pgMaxConnsInt < 0 || config.pgMaxConnsInt > math.MaxInt32 {
		// flag.IntVar range-checks against int (64-bit on shipped platforms),
		// so an int32 conversion without this bound would silently wrap into
		// a pool bound nobody asked for. Backend bounds are explicit here.
		return postgresLoadCommandConfig{}, ErrPGMaxConnsInvalid
	}
	config.PG.MaxConns = int32(config.pgMaxConnsInt)
	ref, err := publication.NewRef(config.Options.Project, config.Generation)
	if err != nil {
		return postgresLoadCommandConfig{}, fmt.Errorf("invalid --generation for load-generation: %w", err)
	}
	config.Options.Dataset = &ref
	config.Options.Truncate = false
	return config, nil
}

// resolvePostgresDSN enforces D2: exactly one source. An explicitly set
// --pg-dsn together with a non-empty LOOM_DATA_PG_DSN is a conflict, not a
// precedence decision. Error text names settings only.
func resolvePostgresDSN(fs *flag.FlagSet, flagValue string) (string, error) {
	explicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "pg-dsn" {
			explicit = true
		}
	})
	envValue := strings.TrimSpace(os.Getenv(pgDSNEnv))
	flagTrimmed := strings.TrimSpace(flagValue)
	if explicit && envValue != "" {
		return "", ErrPGDSNConflict
	}
	if explicit {
		if flagTrimmed == "" {
			return "", ErrPGDSNRequired
		}
		return flagValue, nil
	}
	if envValue == "" {
		return "", ErrPGDSNRequired
	}
	return envValue, nil
}

func runPostgresLoadGeneration(ctx context.Context, args []string, runner loadRunner) error {
	config, err := parsePostgresLoadCommand(args, flag.ExitOnError)
	if err != nil {
		return err
	}
	dsn := config.ResolvedDSN
	// The catalog evidence writers are the caller-side production wiring the
	// loader requires (internal/ingest/generation_load.go:187-204): all four
	// over the same DSN/schema before the load is invoked.
	writers, closeWriters, err := runner.openCatalogWriters(ctx, dsn, config.PG.Schema, config.PG.ApplicationName)
	if err != nil {
		return redactPGDiagnostic(err, dsn)
	}
	defer func() { _ = closeWriters() }()
	config.PG.Catalog = writers
	config.PG.DSN = dsn

	summary, err := runLoadWithProfiles(ctx,
		profileSettings{CPUProfile: config.CPUProfile, MemProfile: config.MemProfile, TraceProfile: config.TraceProfile, BlockProfile: config.BlockProfile},
		config.MemoryBytes,
		func(loadCtx context.Context) (ingest.LoadSummary, error) {
			return runner.loadPostgres(loadCtx, config.Options, config.PG)
		})
	if err != nil {
		// The loader deliberately keeps summary.Dataset populated on failure
		// so operators can identify the inactive manifest needing inspection
		// (internal/ingest/generation_load.go:283-285); surface it on stderr
		// while keeping stdout machine-readable.
		if summary.Dataset != nil {
			runner.printNote("staged-manifest-inspect " + summaryJSON(summary))
		}
		return redactPGDiagnostic(err, dsn)
	}
	if summary.Dataset == nil {
		return errors.New("postgres generation load reported success without a dataset manifest; refusing to report success")
	}
	// STAGED-VERIFIED boundary law: a successful load means the manifest read
	// back STAGED through the store, not merely that rows were copied.
	if err := runner.verifyStaged(ctx, *summary.Dataset, dsn, config.PG.Schema); err != nil {
		return redactPGDiagnostic(fmt.Errorf("verify postgres generation staged state: %w", err), dsn)
	}
	if err := runner.printSummary(summary); err != nil {
		return err
	}
	// The user-visible success line must say STAGED and must not imply
	// activation (D3 condition): activation is the separate explicit
	// ingest.ActivatePostgresGeneration CAS, never performed here.
	runner.printNote(fmt.Sprintf("postgres generation %s/%s is STAGED (verified); not activated — activation is a separate explicit operation",
		summary.Dataset.Project, summary.Dataset.Generation))
	return nil
}

// openCatalogWriters performs the production wiring recipe documented at
// internal/ingest/generation_load.go:187-204 verbatim over one
// readiness-checked pool. The loader opens its own pool internally; this one
// serves the four catalog evidence writers and is closed when the command
// returns. When application is explicitly set (validated by
// pgApplicationNameRE before reaching here), the pool's backends carry it as
// application_name so operator witnesses in pg_stat_activity attribute them
// to this invocation; a keyword/value DSN keeps an untagged connection
// rather than a guessed syntax, same as the loader.
func openCatalogWriters(ctx context.Context, dsn, schema, application string) (ingest.GenerationCatalogWriters, func() error, error) {
	poolDSN, _ := joinApplicationName(dsn, application)
	client, err := storepostgres.Open(ctx, storepostgres.Config{
		DSN:            poolDSN,
		MaxConns:       4,
		MinConns:       0,
		AcquireTimeout: 30 * time.Second,
	})
	if err != nil {
		return ingest.GenerationCatalogWriters{}, nil, fmt.Errorf("open postgres catalog pool: %w", err)
	}
	profileRepo, err := storepostgres.NewFieldProfileRepository(client, schema)
	if err != nil {
		client.Close()
		return ingest.GenerationCatalogWriters{}, nil, fmt.Errorf("field profile repository: %w", err)
	}
	profiles, err := catalogpostgres.NewFieldProfileStore(profileRepo)
	if err != nil {
		client.Close()
		return ingest.GenerationCatalogWriters{}, nil, fmt.Errorf("field profile store: %w", err)
	}
	inventoryRepo, err := storepostgres.NewInventoryRepository(client, schema)
	if err != nil {
		client.Close()
		return ingest.GenerationCatalogWriters{}, nil, fmt.Errorf("inventory repository: %w", err)
	}
	inventory, err := catalogpostgres.New(inventoryRepo)
	if err != nil {
		client.Close()
		return ingest.GenerationCatalogWriters{}, nil, fmt.Errorf("inventory store: %w", err)
	}
	relationshipsRepo, err := catalogpostgres.NewRelationshipEvidenceRepository(client, schema)
	if err != nil {
		client.Close()
		return ingest.GenerationCatalogWriters{}, nil, fmt.Errorf("relationship evidence repository: %w", err)
	}
	relationships, err := catalogpostgres.NewRelationshipEvidenceStore(relationshipsRepo)
	if err != nil {
		client.Close()
		return ingest.GenerationCatalogWriters{}, nil, fmt.Errorf("relationship evidence store: %w", err)
	}
	contributions, err := catalogpostgres.NewSemanticContributionRepository(client, schema)
	if err != nil {
		client.Close()
		return ingest.GenerationCatalogWriters{}, nil, fmt.Errorf("semantic contribution repository: %w", err)
	}
	return ingest.GenerationCatalogWriters{
		FieldProfiles: profiles,
		Inventory:     inventory,
		Relationships: relationships,
		Contributions: contributions,
	}, func() error { client.Close(); return nil }, nil
}

// verifyStagedManifest reads the manifest back through the store so the CLI
// only exits 0 on a STAGED-verified generation.
func verifyStagedManifest(ctx context.Context, ref publication.Ref, dsn, schema string) error {
	client, err := storepostgres.Open(ctx, storepostgres.Config{
		DSN:            dsn,
		MaxConns:       1,
		MinConns:       0,
		AcquireTimeout: 30 * time.Second,
	})
	if err != nil {
		return err
	}
	defer client.Close()
	store, err := datasetpostgres.New(client, schema)
	if err != nil {
		return err
	}
	manifest, err := store.ReadManifest(ctx, ref)
	if err != nil {
		return err
	}
	if !manifest.IsStaged() {
		return fmt.Errorf("dataset generation %s/%s has state %s, want STAGED", ref.Project, ref.Generation, manifest.State)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Boundary redaction (acceptance: no credential value in diagnostics).
// ---------------------------------------------------------------------------

var (
	// Mirrors internal/server/options.go:529-533 (URL userinfo stripping).
	pgURLCredentialsRE = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/\s:@]+:[^/\s@]+@`)
	// Keyword/value DSN password forms: password=word, 'quoted', or "quoted".
	pgKeywordPasswordRE = regexp.MustCompile(`(?i)password\s*=\s*(?:'[^']*'|"[^"]*"|\S+)`)
	// Same shape with capture groups, for extracting the value as a secret.
	pgKeywordPasswordValueRE = regexp.MustCompile(`(?i)password\s*=\s*(?:'([^']*)'|"([^"]*)"|(\S+))`)
)

// redactedPGError changes only the rendering of a PostgreSQL-path diagnostic;
// errors.Is/errors.As still reach every original identity, so classification
// survives redaction.
type redactedPGError struct {
	original error
	rendered string
}

func (e *redactedPGError) Error() string { return e.rendered }
func (e *redactedPGError) Unwrap() error { return e.original }

// redactPGDiagnostic renders every PostgreSQL-route diagnostic with all
// credential material removed. The store's Open path formats the pgx pool
// parse error with %v (internal/store/postgres/client.go:141-143), which can
// echo the connection string including userinfo, so the redaction belongs at
// this CLI boundary and nowhere deeper.
func redactPGDiagnostic(err error, dsn string) error {
	if err == nil {
		return nil
	}
	rendered := err.Error()
	rendered = pgURLCredentialsRE.ReplaceAllString(rendered, "${1}[redacted]@")
	rendered = pgKeywordPasswordRE.ReplaceAllString(rendered, "password=[redacted]")
	for _, secret := range dsnSecrets(dsn) {
		if secret != "" {
			rendered = strings.ReplaceAll(rendered, secret, "[redacted]")
		}
	}
	if rendered == err.Error() {
		return err
	}
	return &redactedPGError{original: err, rendered: rendered}
}

// dsnSecrets enumerates every credential view of a DSN (following the
// scrub-secret enumeration in
// internal/ingest/generation_load_postgres_test.go:362-378): the full
// string, the URL userinfo, the URL password, and a keyword/value password.
func dsnSecrets(dsn string) []string {
	if strings.TrimSpace(dsn) == "" {
		return nil
	}
	secrets := []string{dsn}
	if parsed, err := url.Parse(dsn); err == nil && parsed.User != nil {
		if info := parsed.User.String(); info != "" {
			secrets = append(secrets, info)
		}
		if pw, ok := parsed.User.Password(); ok && pw != "" {
			secrets = append(secrets, pw)
		}
	}
	if match := pgKeywordPasswordValueRE.FindStringSubmatch(dsn); match != nil {
		for _, group := range match[1:] {
			if group != "" {
				secrets = append(secrets, group)
				break
			}
		}
	}
	return secrets
}

func summaryJSON(summary ingest.LoadSummary) string {
	data, err := json.Marshal(summary)
	if err != nil {
		if summary.Dataset != nil {
			return fmt.Sprintf(`{"project":%q,"generation":%q}`, summary.Dataset.Project, summary.Dataset.Generation)
		}
		return "{}"
	}
	return string(data)
}

// ---------------------------------------------------------------------------
// Load profiles and memory limit, copied from cmd/arango-fhir-proto
// (main.go:217-252, 254-292, 778-846) so both routes keep the identical
// operator instrumentation contract.
// ---------------------------------------------------------------------------

type profileSettings struct {
	CPUProfile   string
	MemProfile   string
	TraceProfile string
	BlockProfile string
}

// runLoadWithProfiles preserves cmd/arango-fhir-proto/main.go:234-252 with
// the ingest.Load call replaced by the runner seam.
func runLoadWithProfiles(ctx context.Context, profiles profileSettings, memoryBytes int64, load func(context.Context) (ingest.LoadSummary, error)) (summary ingest.LoadSummary, err error) {
	previousMemoryLimit := int64(-1)
	if memoryBytes > 0 {
		previousMemoryLimit = debug.SetMemoryLimit(memoryBytes)
	}
	if previousMemoryLimit >= 0 {
		defer debug.SetMemoryLimit(previousMemoryLimit)
	}
	stopProfiles, profileErr := startProfiles(profiles.CPUProfile, profiles.MemProfile, profiles.TraceProfile, profiles.BlockProfile)
	if profileErr != nil {
		return ingest.LoadSummary{}, profileErr
	}
	defer func() {
		if stopErr := stopProfiles(); stopErr != nil && err == nil {
			err = stopErr
		}
	}()
	return load(ctx)
}

func parseMemoryBytes(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "0" {
		return 0, nil
	}
	lower := strings.ToLower(raw)
	units := []struct {
		suffix string
		factor float64
	}{
		{suffix: "gib", factor: 1 << 30},
		{suffix: "gb", factor: 1e9},
		{suffix: "gi", factor: 1 << 30},
		{suffix: "g", factor: 1e9},
		{suffix: "mib", factor: 1 << 20},
		{suffix: "mb", factor: 1e6},
		{suffix: "mi", factor: 1 << 20},
		{suffix: "m", factor: 1e6},
		{suffix: "kib", factor: 1 << 10},
		{suffix: "kb", factor: 1e3},
		{suffix: "ki", factor: 1 << 10},
		{suffix: "k", factor: 1e3},
		{suffix: "b", factor: 1},
	}
	number := lower
	factor := float64(1)
	for _, unit := range units {
		if strings.HasSuffix(lower, unit.suffix) {
			number = strings.TrimSpace(strings.TrimSuffix(lower, unit.suffix))
			factor = unit.factor
			break
		}
	}
	value, err := strconv.ParseFloat(number, 64)
	if err != nil || value <= 0 || value*factor > math.MaxInt64 {
		return 0, fmt.Errorf("expected a positive byte value such as 4GiB")
	}
	return int64(value * factor), nil
}

// joinApplicationName mirrors internal/ingest/generation_load.go:1275-1289
// (unexported there) for the CLI-owned catalog pool, so backends this
// command opens are attributable in pg_stat_activity. A keyword/value DSN is
// returned untagged rather than guessed into a syntax, same as the loader.
func joinApplicationName(dsn, application string) (string, bool) {
	if strings.TrimSpace(application) == "" {
		return dsn, false
	}
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return dsn, false
	}
	query := parsed.Query()
	if query.Get("application_name") != "" {
		return dsn, true
	}
	query.Set("application_name", application)
	parsed.RawQuery = query.Encode()
	return parsed.String(), true
}

func startProfiles(cpuPath, memPath, tracePath, blockPath string) (func() error, error) {
	type closer func() error
	var closers []closer

	if blockPath != "" {
		runtime.SetBlockProfileRate(1)
		closers = append(closers, func() error {
			defer runtime.SetBlockProfileRate(0)
			f, err := os.Create(blockPath)
			if err != nil {
				return err
			}
			defer f.Close()
			return pprof.Lookup("block").WriteTo(f, 0)
		})
	}

	if tracePath != "" {
		f, err := os.Create(tracePath)
		if err != nil {
			return nil, err
		}
		if err := trace.Start(f); err != nil {
			f.Close()
			return nil, err
		}
		closers = append(closers, func() error {
			trace.Stop()
			return f.Close()
		})
	}

	if cpuPath != "" {
		f, err := os.Create(cpuPath)
		if err != nil {
			return nil, err
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			f.Close()
			return nil, err
		}
		closers = append(closers, func() error {
			pprof.StopCPUProfile()
			return f.Close()
		})
	}

	if memPath != "" {
		closers = append(closers, func() error {
			runtime.GC()
			f, err := os.Create(memPath)
			if err != nil {
				return err
			}
			defer f.Close()
			return pprof.WriteHeapProfile(f)
		})
	}

	return func() error {
		var firstErr error
		for i := len(closers) - 1; i >= 0; i-- {
			if err := closers[i](); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}, nil
}
