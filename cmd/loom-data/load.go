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

	arangofactory "github.com/calypr/loom/internal/backends/arango"
	postgres "github.com/calypr/loom/internal/backends/postgres"
	"github.com/calypr/loom/internal/backends/postgres/control"
	"github.com/calypr/loom/internal/catalog"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest"
)

type backendTag string

const (
	backendArango   backendTag = "arango"
	backendPostgres backendTag = "postgres"
)

const (
	defaultURL      = "http://127.0.0.1:8529"
	defaultDatabase = "fhir_proto"
	defaultProject  = "ARANGODB_PROTO"
	defaultSchema   = "schemas/graph-fhir.json"
	defaultMetaDir  = "META"
)

const pgDSNEnv = "LOOM_DATA_PG_DSN"

var (
	ErrBackendRequired          = errors.New("loom-data: backend is required")
	ErrBackendUnknown           = errors.New("loom-data: backend must be arango or postgres")
	ErrProjectRequired          = errors.New("loom-data: --project is required for the postgres backend")
	ErrPGDSNRequired            = errors.New("loom-data: a PostgreSQL DSN is required: set --pg-dsn or LOOM_DATA_PG_DSN, never both")
	ErrPGDSNConflict            = errors.New("loom-data: --pg-dsn and LOOM_DATA_PG_DSN cannot both be set; exactly one source is required")
	ErrPGSchemaRequired         = errors.New("loom-data: --pg-schema is required for the postgres backend")
	ErrPGManifestSchemaRequired = errors.New("loom-data: --pg-manifest-schema is required for the postgres backend (the dataset_release series lives on its own schema; see ingest.PGSourceOptions.ManifestSchema)")
	ErrPGTruncateForbidden      = errors.New("loom-data: --truncate is not permitted for a postgres generation load")
	ErrPGApplicationNameInvalid = errors.New("loom-data: --pg-application-name must be 1-63 bytes of letters, digits, dot, underscore, or hyphen, starting with a letter or digit")
	ErrPGMaxConnsInvalid        = errors.New("loom-data: --pg-max-conns must be between 0 and 2147483647")
)

var pgApplicationNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

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

type loadRunner struct {
	loadArango         func(context.Context, ingest.LoadOptions) (ingest.LoadSummary, error)
	loadPostgres       func(context.Context, ingest.LoadOptions, ingest.PGSourceOptions) (ingest.LoadSummary, error)
	requireDatabase    func(context.Context, string, string) error
	openCatalogWriters func(ctx context.Context, dsn, schema, manifestSchema, application string) (ingest.GenerationCatalogWriters, func() error, error)
	verifyStaged       func(context.Context, publication.Ref, string, string, string) error
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

func dispatchLoadGeneration(ctx context.Context, args []string, runner loadRunner) error {
	rawBackend, backendSet, help := inspectLoadGenerationArgs(args)
	tag, tagErr := normalizeBackendTag(rawBackend, backendSet)
	if help && tagErr != nil {
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
		return fmt.Errorf("%w: refusing to dispatch with unregistered backend %q", ErrBackendUnknown, string(tag))
	}
}

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

func requireDatabaseExists(ctx context.Context, url, database string) error {
	databaseExists, err := arangofactory.DatabaseExists(ctx, url, database)
	if err != nil {
		return fmt.Errorf("check existing database: %w", err)
	}
	if !databaseExists {
		return fmt.Errorf("refusing to create missing database %q", database)
	}
	return nil
}

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

func configurePostgresLoadFlags(fs *flag.FlagSet, config *postgresLoadCommandConfig) {
	configureLoadFlagsCore(fs, &config.Options, &config.MemoryLimit, &config.CPUProfile, &config.MemProfile, &config.TraceProfile, &config.BlockProfile)
	fs.StringVar(&config.Options.Project, "project", "", "Project label (required)")
	fs.StringVar(&config.Generation, "generation", "", "Required opaque immutable dataset generation identifier")
	fs.BoolVar(&config.Truncate, "truncate", false, "Truncate before loading; always refused for a postgres generation load")
	fs.StringVar(new(string), "backend", "", "Required backend tag: arango or postgres (validated before any flag below)")
	fs.StringVar(&config.DSNFromFlag, "pg-dsn", "", "PostgreSQL connection string (mutually exclusive with LOOM_DATA_PG_DSN); never printed")
	fs.StringVar(&config.PG.Schema, "pg-schema", "", "Required PostgreSQL schema the generation is loaded into (source series)")
	fs.StringVar(&config.PG.ManifestSchema, "pg-manifest-schema", "", "Required PostgreSQL schema hosting the dataset_release series (generation manifests and the active-generation pointer); must differ from --pg-schema")
	fs.IntVar(&config.pgMaxConnsInt, "pg-max-conns", 0, "PostgreSQL load pool maximum connections; zero selects the loader default (4)")
	fs.DurationVar(&config.PG.AcquireTimeout, "pg-acquire-timeout", 0, "PostgreSQL pool acquire timeout; zero selects the loader default (30s)")
	fs.StringVar(&config.PG.ApplicationName, "pg-application-name", "", "Optional application_name tag for load backends; never printed")
	fs.IntVar(&config.PG.SemanticFlushThreshold, "semantic-flush-threshold", 0, "Semantic contribution buffer spill threshold; zero selects the loader default")
}

func parsePostgresLoadCommand(args []string, errorHandling flag.ErrorHandling) (postgresLoadCommandConfig, error) {
	fs := flag.NewFlagSet("load-generation", errorHandling)
	fs.Usage = func() {
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
	if strings.TrimSpace(config.PG.ManifestSchema) == "" {
		return postgresLoadCommandConfig{}, ErrPGManifestSchemaRequired
	}
	name := strings.TrimSpace(config.PG.ApplicationName)
	switch {
	case name == "":
		config.PG.ApplicationName = ""
	case !pgApplicationNameRE.MatchString(name):
		return postgresLoadCommandConfig{}, ErrPGApplicationNameInvalid
	default:
		config.PG.ApplicationName = name
	}
	if config.pgMaxConnsInt < 0 || config.pgMaxConnsInt > math.MaxInt32 {
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
	writers, closeWriters, err := runner.openCatalogWriters(ctx, dsn, config.PG.Schema, config.PG.ManifestSchema, config.PG.ApplicationName)
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
		if summary.Dataset != nil {
			runner.printNote("staged-manifest-inspect " + summaryJSON(summary))
		}
		return redactPGDiagnostic(err, dsn)
	}
	if summary.Dataset == nil {
		return errors.New("postgres generation load reported success without a dataset manifest; refusing to report success")
	}
	if err := runner.verifyStaged(ctx, *summary.Dataset, dsn, config.PG.Schema, config.PG.ManifestSchema); err != nil {
		return redactPGDiagnostic(fmt.Errorf("verify postgres generation staged state: %w", err), dsn)
	}
	if err := runner.printSummary(summary); err != nil {
		return err
	}
	runner.printNote(fmt.Sprintf("postgres generation %s/%s is STAGED (verified); not activated — activation is a separate explicit operation",
		summary.Dataset.Project, summary.Dataset.Generation))
	return nil
}

func openCatalogWriters(ctx context.Context, dsn, schema, manifestSchema, application string) (ingest.GenerationCatalogWriters, func() error, error) {
	poolDSN, _ := joinApplicationName(dsn, application)
	pg := ingest.PGSourceOptions{DSN: dsn, Schema: schema, ManifestSchema: manifestSchema}
	runtime, err := postgres.Open(ctx, ingest.PGLoadConfig(pg, poolDSN, control.SetupNone))
	if err != nil {
		return ingest.GenerationCatalogWriters{}, nil, fmt.Errorf("open postgres catalog pool: %w", err)
	}
	if runtime.FieldProfiles == nil || runtime.Inventory == nil || runtime.Relationships == nil || runtime.Contributions == nil {
		_ = runtime.Close()
		return ingest.GenerationCatalogWriters{}, nil, fmt.Errorf("postgres runtime assembled without catalog evidence ports")
	}
	return ingest.GenerationCatalogWriters{
		FieldProfiles: runtime.FieldProfiles,
		Inventory:     runtime.Inventory,
		Relationships: runtime.Relationships,
		Contributions: runtime.Contributions,
	}, func() error { return runtime.Close() }, nil
}

func verifyStagedManifest(ctx context.Context, ref publication.Ref, dsn, schema, manifestSchema string) error {
	pg := ingest.PGSourceOptions{DSN: dsn, Schema: schema, ManifestSchema: manifestSchema}
	runtime, err := postgres.Open(ctx, ingest.PGLoadConfig(pg, dsn, control.SetupNone))
	if err != nil {
		return err
	}
	defer func() { _ = runtime.Close() }()
	store := runtime.Manifests
	if store == nil {
		return fmt.Errorf("postgres runtime exposes no dataset manifest lifecycle port")
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

var (
	pgURLCredentialsRE       = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/\s:@]+:[^/\s@]+@`)
	pgKeywordPasswordRE      = regexp.MustCompile(`(?i)password\s*=\s*(?:'[^']*'|"[^"]*"|\S+)`)
	pgKeywordPasswordValueRE = regexp.MustCompile(`(?i)password\s*=\s*(?:'([^']*)'|"([^"]*)"|(\S+))`)
)

type redactedPGError struct {
	original error
	rendered string
}

func (e *redactedPGError) Error() string { return e.rendered }
func (e *redactedPGError) Unwrap() error { return e.original }

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

type profileSettings struct {
	CPUProfile   string
	MemProfile   string
	TraceProfile string
	BlockProfile string
}

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
