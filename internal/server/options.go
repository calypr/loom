package server

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataset"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	Server ServerConfig `yaml:"server"`
	Auth   AuthConfig   `yaml:"auth"`
}

type ServerConfig struct {
	Listen                        string                      `yaml:"listen"`
	Backend                       string                      `yaml:"backend"`
	URL                           string                      `yaml:"url"`
	Database                      string                      `yaml:"database"`
	Schema                        string                      `yaml:"schema"`
	PostgreSQL                    PostgreSQLConfig            `yaml:"postgresql"`
	ClickHouse                    ClickHouseConfig            `yaml:"clickhouse"`
	Dataframer                    DataframerConfig            `yaml:"dataframer"`
	RecipeBatchRows               int                         `yaml:"recipe_batch_rows"`
	RecipeBatchBytes              int                         `yaml:"recipe_batch_bytes"`
	RecipeQueryPageRows           int                         `yaml:"recipe_query_page_rows"`
	RecipeQualityMaxRows          int64                       `yaml:"recipe_quality_max_rows"`
	RecipeQualityMaxDistinctKeys  int64                       `yaml:"recipe_quality_max_distinct_keys"`
	ArtifactDirectory             string                      `yaml:"artifact_directory"`
	ArtifactTTL                   time.Duration               `yaml:"artifact_ttl"`
	ArtifactMaxRows               int64                       `yaml:"artifact_max_rows"`
	ArtifactMaxBytes              int64                       `yaml:"artifact_max_bytes"`
	PopulationMappingCursorSecret string                      `yaml:"population_mapping_cursor_secret"`
	AllowUnauthenticated          bool                        `yaml:"allow_unauthenticated"`
	RequiredDataframeSelectors    []dataset.DataframeSelector `yaml:"required_dataframe_selectors"`
	LocalWorkspaceWriteback       string                      `yaml:"local_workspace_writeback"`
	LocalWorkspaceProject         string                      `yaml:"local_workspace_project"`
	// DevActivationConflictOnce is intentionally not configurable through YAML.
	// It is a one-shot fault injector for the isolated, --no-auth development
	// stack and is ignored unless both server and auth are unauthenticated.
	DevActivationConflictOnce bool `yaml:"-"`
	// DevRepositoryActivationConflictOnce is the repository-publish sibling of
	// DevActivationConflictOnce. It is intentionally not configurable through
	// YAML, arms the one-shot corrupted-compare fault on the repository
	// activation leg, and is honored only under the same local no-auth mode
	// gate as the other dev fault switches.
	DevRepositoryActivationConflictOnce bool          `yaml:"-"`
	DevArtifactRowDelay                 time.Duration `yaml:"-"`
}

type ClickHouseConfig struct {
	Enabled  bool   `yaml:"enabled"`
	URL      string `yaml:"url"`
	Database string `yaml:"database"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// PostgreSQLConfig holds the connection contract for the PostgreSQL source
// backend. It is validated only when server.backend is postgres. Password is
// a credential: it must never be interpolated into errors or rendered output
// (see Config.String and the secret-redaction regressions).
type PostgreSQLConfig struct {
	Host            string        `yaml:"host"`
	Port            int           `yaml:"port"`
	Database        string        `yaml:"database"`
	User            string        `yaml:"user"`
	Password        string        `yaml:"password"`
	SSLMode         string        `yaml:"sslmode"`
	MaxOpenConns    int           `yaml:"max_open_conns"`
	MaxIdleConns    int           `yaml:"max_idle_conns"`
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime"`
	// Schemas is the declaration of schema ownership for each production
	// migration series (see
	// internal/backends/postgres/internal/store/migration_ownership.go).
	// Since pg-runtime-server-composition it is CONSUMED in exactly two
	// shapes: the complete six-series ladder (the full PostgreSQL runtime,
	// internal/server runPostgreSQLFullRuntime) and the first-query-route
	// pair (source and dataset_release only, runPostgreSQLFirstQueryRoute).
	// Both pass the assignment through the backend factory, where
	// storepostgres.ValidateAssignment is the authoritative pre-pool gate.
	// Any other shape -- an extra entry, a typo'd series, or a subset of the
	// ladder beyond the pair -- is rejected at configuration time (fail
	// closed) instead of being silently ignored: a deployment can never
	// believe an unconsumed series is isolated in the named schema. A
	// missing map is valid here (no error), but the route's pre-dial
	// consumability gates then keep the deployment on the
	// ErrPostgreSQLRuntimeNotWired refusal, never a partial composition.
	Schemas map[string]string `yaml:"schemas"`
}

const (
	sourceBackendArango   = "arango"
	sourceBackendPostgres = "postgres"

	defaultServerURL      = "http://127.0.0.1:8529"
	defaultServerDatabase = "fhir_proto"

	defaultPostgresPort            = 5432
	defaultPostgresSSLMode         = "prefer"
	defaultPostgresMaxOpenConns    = 10
	defaultPostgresMaxIdleConns    = 5
	defaultPostgresConnMaxLifetime = 30 * time.Minute
)

var postgresSSLModes = []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}

// postgresCLIFlagNames lists the PostgreSQL connection flags. With --config
// they are rejected explicitly rather than silently dropped.
var postgresCLIFlagNames = []string{
	"pg-host", "pg-port", "pg-database", "pg-user", "pg-password", "pg-sslmode",
	"pg-max-open-conns", "pg-max-idle-conns", "pg-conn-max-lifetime",
}

type DataframerConfig struct {
	Recipe string `yaml:"recipe"`
}

type AuthConfig struct {
	Mode                 string           `yaml:"mode"`
	Basic                BasicAuthConfig  `yaml:"basic"`
	Calypr               CalyprAuthConfig `yaml:"calypr"`
	AllowUnauthenticated bool             `yaml:"allow_unauthenticated"`
}

type BasicAuthConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type CalyprAuthConfig struct {
	TrustedIssuer  string        `yaml:"trusted_issuer"`
	RequestTimeout time.Duration `yaml:"request_timeout"`
	CacheTTL       time.Duration `yaml:"cache_ttl"`
}

const defaultRecipeQueryPageRows = 250

func DefaultConfig() Config {
	return Config{
		Server: ServerConfig{
			Listen: ":8080", Backend: sourceBackendArango, URL: defaultServerURL, Database: defaultServerDatabase,
			Schema: "schemas/graph-fhir.json",
			PostgreSQL: PostgreSQLConfig{
				Port: defaultPostgresPort, SSLMode: defaultPostgresSSLMode,
				MaxOpenConns: defaultPostgresMaxOpenConns, MaxIdleConns: defaultPostgresMaxIdleConns,
				ConnMaxLifetime: defaultPostgresConnMaxLifetime,
			},
			ClickHouse:      ClickHouseConfig{Enabled: true, URL: "clickhouse://127.0.0.1:9000", Database: "loom", Username: "default"},
			RecipeBatchRows: 1000, RecipeBatchBytes: 4 << 20, RecipeQueryPageRows: defaultRecipeQueryPageRows,
			RecipeQualityMaxRows: 1_000_000, RecipeQualityMaxDistinctKeys: 1_000_000,
			ArtifactDirectory: "/tmp/loom-artifacts", ArtifactTTL: 24 * time.Hour,
			ArtifactMaxRows: 10_000_000, ArtifactMaxBytes: 2 << 30,
		},
		Auth: AuthConfig{Mode: "basic", Calypr: CalyprAuthConfig{RequestTimeout: 5 * time.Second, CacheTTL: 30 * time.Second}},
	}
}

func parseServerOptions(args []string, handling flag.ErrorHandling) (Config, error) {
	var (
		configPath              string
		listen                  string
		noAuth                  bool
		backend                 string
		url                     string
		database                string
		schema                  string
		pgHost                  string
		pgPort                  int
		pgDatabase              string
		pgUser                  string
		pgPassword              string
		pgSSLMode               string
		pgMaxOpenConns          int
		pgMaxIdleConns          int
		pgConnMaxLifetime       time.Duration
		clickhouseURL           string
		clickhouseDatabase      string
		clickhouseUsername      string
		clickhousePassword      string
		dataframerRecipe        string
		recipeBatchRows         int
		recipeBatchBytes        int
		recipeQueryPageRows     int
		recipeQualityMaxRows    int64
		recipeQualityMaxKeys    int64
		localWorkspaceWriteback string
		localWorkspaceProject   string
	)
	fs := flag.NewFlagSet("arango-fhir-server", handling)
	fs.StringVar(&configPath, "config", "", "YAML server configuration file")
	fs.StringVar(&listen, "listen", ":8080", "HTTP listen address")
	fs.BoolVar(&noAuth, "no-auth", false, "disable scoped authorization for local development")
	fs.StringVar(&backend, "backend", sourceBackendArango, "source graph/catalog backend: arango or postgres")
	fs.StringVar(&url, "url", defaultServerURL, "ArangoDB URL (ArangoDB backend only)")
	fs.StringVar(&database, "database", defaultServerDatabase, "ArangoDB database (ArangoDB backend only)")
	fs.StringVar(&schema, "schema", "schemas/graph-fhir.json", "FHIR graph schema path for imports")
	fs.StringVar(&pgHost, "pg-host", "", "PostgreSQL host (required with --backend postgres)")
	fs.IntVar(&pgPort, "pg-port", defaultPostgresPort, "PostgreSQL port")
	fs.StringVar(&pgDatabase, "pg-database", "", "PostgreSQL database (required with --backend postgres)")
	fs.StringVar(&pgUser, "pg-user", "", "PostgreSQL user (required with --backend postgres)")
	fs.StringVar(&pgPassword, "pg-password", "", "PostgreSQL password; never rendered or logged")
	fs.StringVar(&pgSSLMode, "pg-sslmode", defaultPostgresSSLMode, "PostgreSQL TLS mode: disable, allow, prefer, require, verify-ca, or verify-full")
	fs.IntVar(&pgMaxOpenConns, "pg-max-open-conns", defaultPostgresMaxOpenConns, "PostgreSQL maximum open pool connections")
	fs.IntVar(&pgMaxIdleConns, "pg-max-idle-conns", defaultPostgresMaxIdleConns, "PostgreSQL maximum idle pool connections; must not exceed --pg-max-open-conns")
	fs.DurationVar(&pgConnMaxLifetime, "pg-conn-max-lifetime", defaultPostgresConnMaxLifetime, "PostgreSQL maximum connection lifetime; zero disables expiry")
	fs.StringVar(&clickhouseURL, "clickhouse-url", "clickhouse://127.0.0.1:9000", "ClickHouse native URL for published dataframe reads")
	fs.StringVar(&clickhouseDatabase, "clickhouse-database", "loom", "ClickHouse database for published dataframe reads")
	fs.StringVar(&clickhouseUsername, "clickhouse-username", "default", "ClickHouse username for published dataframe reads")
	fs.StringVar(&clickhousePassword, "clickhouse-password", "", "ClickHouse password for published dataframe reads")
	fs.StringVar(&dataframerRecipe, "dataframer-recipe", "", "dataframer recipe JSON file (required when ClickHouse is enabled)")
	fs.IntVar(&recipeBatchRows, "recipe-batch-rows", 1000, "maximum recipe materialization rows per ClickHouse batch")
	fs.IntVar(&recipeBatchBytes, "recipe-batch-bytes", 4<<20, "maximum recipe materialization bytes per ClickHouse batch")
	fs.IntVar(&recipeQueryPageRows, "recipe-query-page-rows", defaultRecipeQueryPageRows, "root documents per bounded dataframe query page; zero disables paging")
	fs.Int64Var(&recipeQualityMaxRows, "recipe-quality-max-rows", 1_000_000, "maximum rows accepted by the full-population publication quality scan")
	fs.Int64Var(&recipeQualityMaxKeys, "recipe-quality-max-distinct-keys", 1_000_000, "maximum distinct row identities retained by publication quality checks")
	fs.StringVar(&localWorkspaceWriteback, "local-workspace-writeback", "", "exact local CONFIG workspace file updated after successful publication (no-auth development only)")
	fs.StringVar(&localWorkspaceProject, "local-workspace-project", "", "only this project may update the local CONFIG workspace")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	explicitFlags := make(map[string]bool, fs.NFlag())
	fs.Visit(func(f *flag.Flag) { explicitFlags[f.Name] = true })

	if strings.TrimSpace(configPath) != "" {
		cfg, err := LoadConfig(configPath)
		if err != nil {
			return Config{}, err
		}
		// --no-auth is an explicit local-development override. Keep the
		// configuration file authoritative for normal server settings, but honor
		// this flag even when the server is started with --config (as the Helm
		// local-development path does).
		if noAuth {
			cfg.Server.AllowUnauthenticated = true
			cfg.Auth.AllowUnauthenticated = true
		}
		if err := applyDevFaultOverrides(&cfg); err != nil {
			return Config{}, err
		}
		if strings.TrimSpace(localWorkspaceWriteback) != "" {
			cfg.Server.LocalWorkspaceWriteback = localWorkspaceWriteback
			cfg.Server.LocalWorkspaceProject = localWorkspaceProject
		}
		if err := checkSourceBackendConflicts(&cfg, explicitFlags, true); err != nil {
			return Config{}, fmt.Errorf("invalid server config: %w", err)
		}
		if err := cfg.Validate(); err != nil {
			return Config{}, fmt.Errorf("invalid server config: %w", err)
		}
		return cfg, nil
	}

	cfg, err := LoadConfig("")
	if err != nil {
		return Config{}, err
	}
	cfg.Server.Listen = listen
	cfg.Server.Backend = backend
	cfg.Server.URL = url
	cfg.Server.Database = database
	cfg.Server.Schema = schema
	cfg.Server.PostgreSQL = PostgreSQLConfig{
		Host:            pgHost,
		Port:            pgPort,
		Database:        pgDatabase,
		User:            pgUser,
		Password:        pgPassword,
		SSLMode:         pgSSLMode,
		MaxOpenConns:    pgMaxOpenConns,
		MaxIdleConns:    pgMaxIdleConns,
		ConnMaxLifetime: pgConnMaxLifetime,
	}
	cfg.Server.ClickHouse.URL = clickhouseURL
	cfg.Server.ClickHouse.Database = clickhouseDatabase
	cfg.Server.ClickHouse.Username = clickhouseUsername
	cfg.Server.ClickHouse.Password = clickhousePassword
	cfg.Server.Dataframer.Recipe = dataframerRecipe
	cfg.Server.RecipeBatchRows = recipeBatchRows
	cfg.Server.RecipeBatchBytes = recipeBatchBytes
	cfg.Server.RecipeQueryPageRows = recipeQueryPageRows
	cfg.Server.RecipeQualityMaxRows = recipeQualityMaxRows
	cfg.Server.RecipeQualityMaxDistinctKeys = recipeQualityMaxKeys
	cfg.Server.LocalWorkspaceWriteback = localWorkspaceWriteback
	cfg.Server.LocalWorkspaceProject = localWorkspaceProject
	if noAuth {
		cfg.Server.AllowUnauthenticated = true
		cfg.Auth.AllowUnauthenticated = true
	}
	if err := applyDevFaultOverrides(&cfg); err != nil {
		return Config{}, err
	}
	if err := checkSourceBackendConflicts(&cfg, explicitFlags, false); err != nil {
		return Config{}, fmt.Errorf("invalid server config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid server config: %w", err)
	}
	return cfg, nil
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if strings.TrimSpace(path) == "" {
		return applyEnvironment(cfg)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read server config %q: %w", path, err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode server config %q: %w", path, err)
	}
	return applyEnvironment(cfg)
}

func applyEnvironment(cfg Config) (Config, error) {
	if strings.TrimSpace(cfg.Auth.Calypr.TrustedIssuer) == "" {
		cfg.Auth.Calypr.TrustedIssuer = strings.TrimSpace(os.Getenv("LOOM_AUTH_CALYPR_TRUSTED_ISSUER"))
	}
	if cfg.Auth.Basic.Username == "" {
		cfg.Auth.Basic.Username = os.Getenv("LOOM_AUTH_BASIC_USERNAME")
	}
	if cfg.Auth.Basic.Password == "" {
		cfg.Auth.Basic.Password = os.Getenv("LOOM_AUTH_BASIC_PASSWORD")
	}
	if cfg.Server.PopulationMappingCursorSecret == "" {
		cfg.Server.PopulationMappingCursorSecret = os.Getenv("LOOM_POPULATION_MAPPING_CURSOR_SECRET")
	}
	if value := strings.TrimSpace(os.Getenv("LOOM_ARTIFACT_DIRECTORY")); value != "" {
		cfg.Server.ArtifactDirectory = value
	}
	if raw := strings.TrimSpace(os.Getenv("LOOM_REQUIRED_DATAFRAME_SELECTORS")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg.Server.RequiredDataframeSelectors); err != nil {
			return Config{}, fmt.Errorf("parse LOOM_REQUIRED_DATAFRAME_SELECTORS: %w", err)
		}
	}
	return cfg, nil
}

// applyDevFaultOverrides is deliberately gated by the local no-auth mode.
// These switches provide deterministic fault scenarios for the isolated dev
// Compose stack without creating a production request or OpenAPI surface.
func applyDevFaultOverrides(cfg *Config) error {
	if cfg == nil || !cfg.Server.AllowUnauthenticated || !cfg.Auth.AllowUnauthenticated {
		return nil
	}
	if raw := strings.TrimSpace(os.Getenv("LOOM_DEV_RECIPE_QUALITY_MAX_ROWS")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			return fmt.Errorf("LOOM_DEV_RECIPE_QUALITY_MAX_ROWS must be a positive integer")
		}
		cfg.Server.RecipeQualityMaxRows = value
	}
	if raw := strings.TrimSpace(os.Getenv("LOOM_DEV_RECIPE_QUALITY_MAX_DISTINCT_KEYS")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			return fmt.Errorf("LOOM_DEV_RECIPE_QUALITY_MAX_DISTINCT_KEYS must be a positive integer")
		}
		cfg.Server.RecipeQualityMaxDistinctKeys = value
	}
	if raw := strings.TrimSpace(os.Getenv("LOOM_DEV_ACTIVATION_CONFLICT_ONCE")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("LOOM_DEV_ACTIVATION_CONFLICT_ONCE must be boolean")
		}
		cfg.Server.DevActivationConflictOnce = value
	}
	if raw := strings.TrimSpace(os.Getenv("LOOM_DEV_REPOSITORY_ACTIVATION_CONFLICT_ONCE")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("LOOM_DEV_REPOSITORY_ACTIVATION_CONFLICT_ONCE must be boolean")
		}
		cfg.Server.DevRepositoryActivationConflictOnce = value
	}
	if raw := strings.TrimSpace(os.Getenv("LOOM_DEV_ARTIFACT_MAX_ROWS")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			return fmt.Errorf("LOOM_DEV_ARTIFACT_MAX_ROWS must be a positive integer")
		}
		cfg.Server.ArtifactMaxRows = value
	}
	if raw := strings.TrimSpace(os.Getenv("LOOM_DEV_ARTIFACT_ROW_DELAY_MS")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			return fmt.Errorf("LOOM_DEV_ARTIFACT_ROW_DELAY_MS must be a non-negative integer")
		}
		cfg.Server.DevArtifactRowDelay = time.Duration(value) * time.Millisecond
	}
	return nil
}

// normalizeSourceBackend resolves the strict source-backend selector. An
// empty value is the legacy default (arango); anything outside the enum is
// rejected. There is deliberately no URL-based auto-detection.
func normalizeSourceBackend(value string) (string, error) {
	backend := strings.ToLower(strings.TrimSpace(value))
	if backend == "" {
		return sourceBackendArango, nil
	}
	switch backend {
	case sourceBackendArango, sourceBackendPostgres:
		return backend, nil
	default:
		return "", fmt.Errorf("server.backend must be %s or %s, got %q", sourceBackendArango, sourceBackendPostgres, backend)
	}
}

// checkSourceBackendConflicts keeps the resolved source backend honest about
// how it was selected and canonicalizes it onto the configuration. With
// --config the configuration file owns backend selection and the PostgreSQL
// connection contract, so an explicit --backend or --pg-* override is
// rejected instead of being silently dropped next to the file's choice: an
// ignored selector could otherwise start the server on the wrong backend, and
// an ignored TLS flag could silently downgrade the connection. On the pure
// flag path the PostgreSQL flags are authoritative, but explicitly passed
// legacy ArangoDB overrides (--url/--database) still conflict with a postgres
// selection even when they repeat the ArangoDB defaults, so an ignored
// override can never silently coexist with a postgres selection. Error
// messages name settings only and never echo values.
func checkSourceBackendConflicts(cfg *Config, explicitFlags map[string]bool, usingConfigFile bool) error {
	backend, err := normalizeSourceBackend(cfg.Server.Backend)
	if err != nil {
		return err
	}
	cfg.Server.Backend = backend
	if usingConfigFile {
		if explicitFlags["backend"] {
			return fmt.Errorf("server.backend is owned by the --config file; remove the explicit --backend override")
		}
		var pgFlags []string
		for _, name := range postgresCLIFlagNames {
			if explicitFlags[name] {
				pgFlags = append(pgFlags, "--"+name)
			}
		}
		if len(pgFlags) > 0 {
			return fmt.Errorf("server.postgresql settings are owned by the --config file; remove the explicit %s override", strings.Join(pgFlags, ", "))
		}
	}
	if backend != sourceBackendPostgres {
		return nil
	}
	var conflicting []string
	if explicitFlags["url"] {
		conflicting = append(conflicting, "--url")
	}
	if explicitFlags["database"] {
		conflicting = append(conflicting, "--database")
	}
	if len(conflicting) > 0 {
		return fmt.Errorf("server.backend %s conflicts with explicitly set ArangoDB settings %s; remove them when selecting postgres", sourceBackendPostgres, strings.Join(conflicting, " and "))
	}
	return nil
}

// validate enforces the PostgreSQL connection contract. It is only invoked
// when the postgres backend is selected. Errors reference field names and
// non-secret values only; Password is never interpolated.
func (p PostgreSQLConfig) validate() error {
	if strings.TrimSpace(p.Host) == "" {
		return errors.New("server.postgresql.host is required when server.backend is postgres")
	}
	if p.Port < 1 || p.Port > 65535 {
		return fmt.Errorf("server.postgresql.port must be between 1 and 65535, got %d", p.Port)
	}
	if strings.TrimSpace(p.Database) == "" {
		return errors.New("server.postgresql.database is required when server.backend is postgres")
	}
	if strings.TrimSpace(p.User) == "" {
		return errors.New("server.postgresql.user is required when server.backend is postgres")
	}
	sslMode := strings.ToLower(strings.TrimSpace(p.SSLMode))
	known := false
	for _, mode := range postgresSSLModes {
		if sslMode == mode {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("server.postgresql.sslmode must be one of %s, got %q", strings.Join(postgresSSLModes, ", "), p.SSLMode)
	}
	if p.MaxOpenConns <= 0 {
		return fmt.Errorf("server.postgresql.max_open_conns must be positive, got %d", p.MaxOpenConns)
	}
	if p.MaxIdleConns < 0 || p.MaxIdleConns > p.MaxOpenConns {
		return fmt.Errorf("server.postgresql.max_idle_conns must be between 0 and server.postgresql.max_open_conns (%d), got %d", p.MaxOpenConns, p.MaxIdleConns)
	}
	if p.ConnMaxLifetime < 0 {
		return errors.New("server.postgresql.conn_max_lifetime cannot be negative")
	}
	// The field comment anticipated exactly this moment, verbatim: "Entries
	// become acceptable once a factory honors the map, at which point
	// storepostgres.ValidateAssignment is the authoritative pre-pool gate
	// (it stays authoritative and fully tested at the store layer in the
	// meantime)." pg-runtime-first-query-route opened that door for the
	// source and dataset_release pair, and pg-runtime-server-composition now
	// consumes the complete six-series ladder through the full PostgreSQL
	// runtime (runPostgreSQLFullRuntime). Both shapes pass through the
	// factory's Config.Validate, where storepostgres.ValidateAssignment
	// remains the authoritative pre-dial ledger gate. The fail-closed law
	// still governs every shape: silently ignoring an explicit entry would
	// let a deployment believe a series is hosted in the named schema while
	// the assignment has no effect, so any map that is neither the exact
	// first-query-route pair nor the exact full ladder is rejected here.
	if len(p.Schemas) == 0 {
		return nil
	}
	const (
		seriesSource               = "source"
		seriesExplorerControl      = "explorer_control"
		seriesPublicationControl   = "publication_control"
		seriesDatasetRelease       = "dataset_release"
		seriesRecipeChains         = "recipe_chains"
		seriesRecipeExecRegistries = "recipe_exec_registries"
	)
	ladder := []string{seriesSource, seriesExplorerControl, seriesPublicationControl, seriesDatasetRelease, seriesRecipeChains, seriesRecipeExecRegistries}
	_, hasSource := p.Schemas[seriesSource]
	_, hasDatasetRelease := p.Schemas[seriesDatasetRelease]
	firstQueryPair := len(p.Schemas) == 2 && hasSource && hasDatasetRelease
	fullLadder := len(p.Schemas) == len(ladder)
	for _, series := range ladder {
		if _, assigned := p.Schemas[series]; !assigned && fullLadder {
			// A 6-entry map missing a ladder member carries a foreign key;
			// neither consumable shape accepts it.
			fullLadder = false
			break
		}
	}
	for series := range p.Schemas {
		known := false
		for _, candidate := range ladder {
			if series == candidate {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("server.postgresql.schemas: series %q is not consumed by any production factory; the consumable shapes are the exact first-query-route pair (source and dataset_release) or the complete six-series ladder", series)
		}
	}
	if !firstQueryPair && !fullLadder {
		return fmt.Errorf("server.postgresql.schemas: the consumable shapes are the exact first-query-route pair (source and dataset_release) or the complete six-series ladder (source, explorer_control, publication_control, dataset_release, recipe_chains, recipe_exec_registries); got %d entries", len(p.Schemas))
	}
	for series, schema := range p.Schemas {
		if strings.TrimSpace(schema) == "" {
			return fmt.Errorf("server.postgresql.schemas: the %s entry cannot be blank", series)
		}
	}
	// Compare the TRIMMED values pairwise: the composition gates trim before
	// handing the map to the factory, so names that differ only by
	// surrounding whitespace would collapse to one schema downstream and
	// surface as the generic not-wired sentinel. Refusing them here keeps the
	// precise shared-schema message for the operator
	// (review e22c4825 optional 3; both paths already failed closed). The
	// migration-ownership law gives every schema exactly one series' history,
	// so every consumed pair must be distinct.
	trimmed := make(map[string]string, len(p.Schemas))
	for series, schema := range p.Schemas {
		name := strings.TrimSpace(schema)
		if other, clash := trimmed[name]; clash {
			return fmt.Errorf("server.postgresql.schemas: %s and %s must name distinct schemas; their pinned migration ledgers disagree, and the shared-schema refusal stays authoritative at storepostgres.ValidateAssignment", series, other)
		}
		trimmed[name] = series
	}
	return nil
}

const redactedConfigSecret = "[redacted]"

func redactedConfigSecretFor(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return redactedConfigSecret
}

var configURLCredentials = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/\s:@]+:[^/\s@]+@`)

func redactConfigURL(value string) string {
	return configURLCredentials.ReplaceAllString(value, "${1}[redacted]@")
}

// String renders a single-line, log-safe summary of the configuration. Every
// credential (PostgreSQL, ClickHouse, basic auth, and the population mapping
// cursor secret) is replaced with a redaction marker, and URL credentials are
// stripped, so rendering a Config never prints a password or DSN credential.
func (c Config) String() string {
	return fmt.Sprintf(
		"server{listen=%q backend=%q url=%q database=%q schema=%q postgresql{host=%q port=%d database=%q user=%q password=%q sslmode=%q max_open_conns=%d max_idle_conns=%d conn_max_lifetime=%s} clickhouse{enabled=%t url=%q database=%q username=%q password=%q} artifact_directory=%q population_mapping_cursor_secret=%q} auth{mode=%q basic{username=%q password=%q}}",
		c.Server.Listen, c.Server.Backend, redactConfigURL(c.Server.URL), c.Server.Database, c.Server.Schema,
		c.Server.PostgreSQL.Host, c.Server.PostgreSQL.Port, c.Server.PostgreSQL.Database, c.Server.PostgreSQL.User,
		redactedConfigSecretFor(c.Server.PostgreSQL.Password), c.Server.PostgreSQL.SSLMode,
		c.Server.PostgreSQL.MaxOpenConns, c.Server.PostgreSQL.MaxIdleConns, c.Server.PostgreSQL.ConnMaxLifetime,
		c.Server.ClickHouse.Enabled, redactConfigURL(c.Server.ClickHouse.URL), c.Server.ClickHouse.Database,
		c.Server.ClickHouse.Username, redactedConfigSecretFor(c.Server.ClickHouse.Password),
		c.Server.ArtifactDirectory, redactedConfigSecretFor(c.Server.PopulationMappingCursorSecret),
		c.Auth.Mode, c.Auth.Basic.Username, redactedConfigSecretFor(c.Auth.Basic.Password),
	)
}

func (c Config) Validate() error {
	cfg := c
	if cfg.Server.RecipeQueryPageRows < 0 {
		return errors.New("server.recipe_query_page_rows cannot be negative")
	}
	if cfg.Server.RecipeQualityMaxRows <= 0 || cfg.Server.RecipeQualityMaxDistinctKeys <= 0 {
		return errors.New("server recipe quality limits must be positive")
	}
	if strings.TrimSpace(cfg.Server.ArtifactDirectory) == "" || cfg.Server.ArtifactTTL <= 0 || cfg.Server.ArtifactMaxRows <= 0 || cfg.Server.ArtifactMaxBytes <= 0 {
		return errors.New("server artifact directory, TTL, row limit, and byte limit must be configured")
	}
	backend, err := normalizeSourceBackend(cfg.Server.Backend)
	if err != nil {
		return err
	}
	if backend == sourceBackendPostgres {
		// The legacy ArangoDB settings stay untouched for the arango backend,
		// but a customized one beside a postgres selection is a conflict that
		// must fail before startup. Values are never echoed: an Arango URL can
		// carry embedded credentials.
		if strings.TrimSpace(cfg.Server.URL) != defaultServerURL || strings.TrimSpace(cfg.Server.Database) != defaultServerDatabase {
			return errors.New("server.url and server.database are ArangoDB settings and cannot be customized while server.backend is postgres")
		}
		if err := cfg.Server.PostgreSQL.validate(); err != nil {
			return err
		}
	}
	if strings.TrimSpace(cfg.Server.LocalWorkspaceWriteback) != "" && !(cfg.Server.AllowUnauthenticated || cfg.Auth.AllowUnauthenticated) {
		return errors.New("server.local_workspace_writeback requires unauthenticated local-development mode")
	}
	if (strings.TrimSpace(cfg.Server.LocalWorkspaceWriteback) == "") != (strings.TrimSpace(cfg.Server.LocalWorkspaceProject) == "") {
		return errors.New("server.local_workspace_writeback and server.local_workspace_project must be configured together")
	}
	if cfg.Server.ClickHouse.Enabled && strings.TrimSpace(cfg.Server.Dataframer.Recipe) == "" {
		return fmt.Errorf("server.dataframer.recipe is required when server.clickhouse.enabled is true")
	}
	for index, selector := range cfg.Server.RequiredDataframeSelectors {
		if err := selector.Validate(); err != nil {
			return fmt.Errorf("server.required_dataframe_selectors[%d]: %w", index, err)
		}
	}
	cfg.Auth.Mode = strings.ToLower(strings.TrimSpace(cfg.Auth.Mode))
	if cfg.Auth.Mode == "" {
		cfg.Auth.Mode = "basic"
	}
	if cfg.Auth.Mode != "basic" && cfg.Auth.Mode != "calypr" {
		return fmt.Errorf("auth.mode must be basic or calypr, got %q", cfg.Auth.Mode)
	}
	if cfg.Server.AllowUnauthenticated || cfg.Auth.AllowUnauthenticated {
		return nil
	}
	if cfg.Auth.Mode == "basic" {
		if cfg.Auth.Basic.Username == "" || cfg.Auth.Basic.Password == "" {
			return fmt.Errorf("auth.basic.username and auth.basic.password are required in basic mode")
		}
	}
	if cfg.Auth.Mode == "calypr" {
		if strings.TrimSpace(cfg.Auth.Calypr.TrustedIssuer) == "" {
			return fmt.Errorf("auth.calypr.trusted_issuer is required")
		}
		if err := authscope.ValidateTrustedFenceIssuer(cfg.Auth.Calypr.TrustedIssuer); err != nil {
			return fmt.Errorf("auth.calypr.trusted_issuer: %w", err)
		}
		if cfg.Auth.Calypr.RequestTimeout <= 0 {
			return fmt.Errorf("auth.calypr.request_timeout must be positive")
		}
		if cfg.Auth.Calypr.CacheTTL <= 0 {
			return fmt.Errorf("auth.calypr.cache_ttl must be positive")
		}
	}
	return nil
}
