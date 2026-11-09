package server

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseServerOptionsWithoutConfigUsesFlags(t *testing.T) {
	options, err := parseServerOptions([]string{"--listen", ":19091", "--no-auth", "--dataframer-recipe", "recipe.json", "--recipe-query-page-rows", "7"}, flag.ContinueOnError)
	if err != nil {
		t.Fatalf("parseServerOptions() error = %v", err)
	}
	if options.Server.Listen != ":19091" || !options.Server.AllowUnauthenticated || !options.Auth.AllowUnauthenticated || options.Server.RecipeQueryPageRows != 7 {
		t.Fatalf("resolved options = %#v", options)
	}
}

func TestParseServerOptionsDefaultsRootQueryPageRows(t *testing.T) {
	options, err := parseServerOptions([]string{"--listen", ":19091", "--no-auth", "--dataframer-recipe", "recipe.json"}, flag.ContinueOnError)
	if err != nil {
		t.Fatalf("parseServerOptions() error = %v", err)
	}
	if options.Server.RecipeQueryPageRows != defaultRecipeQueryPageRows {
		t.Fatalf("root query page rows = %d, want %d", options.Server.RecipeQueryPageRows, defaultRecipeQueryPageRows)
	}
}

func TestDevFaultOverridesAreNoAuthOnly(t *testing.T) {
	t.Setenv("LOOM_DEV_RECIPE_QUALITY_MAX_ROWS", "1")
	t.Setenv("LOOM_DEV_RECIPE_QUALITY_MAX_DISTINCT_KEYS", "2")
	t.Setenv("LOOM_DEV_ACTIVATION_CONFLICT_ONCE", "true")
	t.Setenv("LOOM_DEV_ARTIFACT_MAX_ROWS", "3")
	t.Setenv("LOOM_DEV_ARTIFACT_ROW_DELAY_MS", "25")

	options, err := parseServerOptions([]string{"--no-auth", "--dataframer-recipe", "recipe.json"}, flag.ContinueOnError)
	if err != nil {
		t.Fatalf("parseServerOptions() error = %v", err)
	}
	if options.Server.RecipeQualityMaxRows != 1 || options.Server.RecipeQualityMaxDistinctKeys != 2 || !options.Server.DevActivationConflictOnce || options.Server.ArtifactMaxRows != 3 || options.Server.DevArtifactRowDelay != 25*time.Millisecond {
		t.Fatalf("dev fault overrides = %#v", options.Server)
	}

	secure := DefaultConfig()
	secure.Server.RecipeQualityMaxRows = 17
	secure.Auth.Basic.Username = "loom"
	secure.Auth.Basic.Password = "secret"
	if err := applyDevFaultOverrides(&secure); err != nil {
		t.Fatalf("authenticated override check = %v", err)
	}
	if secure.Server.RecipeQualityMaxRows != 17 || secure.Server.DevActivationConflictOnce || secure.Server.ArtifactMaxRows != 10_000_000 || secure.Server.DevArtifactRowDelay != 0 {
		t.Fatalf("authenticated config was changed by dev fault overrides: %#v", secure.Server)
	}
}

func TestDevFaultOverridesRejectInvalidValues(t *testing.T) {
	t.Setenv("LOOM_DEV_RECIPE_QUALITY_MAX_ROWS", "zero")
	cfg := DefaultConfig()
	cfg.Server.AllowUnauthenticated = true
	cfg.Auth.AllowUnauthenticated = true
	if err := applyDevFaultOverrides(&cfg); err == nil || !strings.Contains(err.Error(), "LOOM_DEV_RECIPE_QUALITY_MAX_ROWS") {
		t.Fatalf("invalid quality limit error = %v", err)
	}
}

func TestDevArtifactFaultOverridesRejectInvalidValues(t *testing.T) {
	for _, test := range []struct {
		name, key, value string
	}{
		{name: "row limit", key: "LOOM_DEV_ARTIFACT_MAX_ROWS", value: "zero"},
		{name: "row delay", key: "LOOM_DEV_ARTIFACT_ROW_DELAY_MS", value: "-1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(test.key, test.value)
			cfg := DefaultConfig()
			cfg.Server.AllowUnauthenticated = true
			cfg.Auth.AllowUnauthenticated = true
			if err := applyDevFaultOverrides(&cfg); err == nil || !strings.Contains(err.Error(), test.key) {
				t.Fatalf("invalid artifact fault error = %v", err)
			}
		})
	}
}

func TestLocalWorkspaceWritebackRequiresNoAuth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	if _, err := parseServerOptions([]string{"--dataframer-recipe", "recipe.json", "--local-workspace-writeback", path, "--local-workspace-project", "project-a"}, flag.ContinueOnError); err == nil || !strings.Contains(err.Error(), "requires unauthenticated") {
		t.Fatalf("authenticated writeback error = %v", err)
	}
	options, err := parseServerOptions([]string{"--no-auth", "--dataframer-recipe", "recipe.json", "--local-workspace-writeback", path, "--local-workspace-project", "project-a"}, flag.ContinueOnError)
	if err != nil {
		t.Fatal(err)
	}
	if options.Server.LocalWorkspaceWriteback != path {
		t.Fatalf("writeback path = %q", options.Server.LocalWorkspaceWriteback)
	}
}

func TestParseServerOptionsConfigRetainsSettingsButNoAuthOverridesAuth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := "server:\n  listen: :19092\n  allow_unauthenticated: false\n  clickhouse:\n    enabled: false\nauth:\n  mode: basic\n  basic:\n    username: loom\n    password: secret\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	options, err := parseServerOptions([]string{"--config", path, "--listen", ":19093", "--no-auth"}, flag.ContinueOnError)
	if err != nil {
		t.Fatalf("parseServerOptions() error = %v", err)
	}
	if options.Server.Listen != ":19092" || !options.Server.AllowUnauthenticated || !options.Auth.AllowUnauthenticated {
		t.Fatalf("configuration/--no-auth resolution = %#v", options)
	}
}

func TestConfigDefaultsToBasicAndRequiresCredentials(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Auth.Mode != "basic" {
		t.Fatalf("default auth mode = %q", cfg.Auth.Mode)
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("default config without credentials unexpectedly validated")
	}
}

func TestRecipeQueryPageRowsCanBeConfiguredOrDisabled(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Server.RecipeQueryPageRows != defaultRecipeQueryPageRows {
		t.Fatalf("default recipe query page rows = %d, want %d", cfg.Server.RecipeQueryPageRows, defaultRecipeQueryPageRows)
	}
	cfg.Server.RecipeQueryPageRows = 0
	cfg.Auth.AllowUnauthenticated = true
	cfg.Server.ClickHouse.Enabled = false
	if err := cfg.Validate(); err != nil {
		t.Fatalf("disabled recipe query paging: %v", err)
	}
	cfg.Server.RecipeQueryPageRows = -1
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "cannot be negative") {
		t.Fatalf("negative recipe query page rows error = %v", err)
	}
}

func TestArtifactConfigurationHasBoundedDefaultsAndEnvironmentOverride(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Server.ArtifactDirectory == "" || cfg.Server.ArtifactTTL != 24*time.Hour || cfg.Server.ArtifactMaxRows <= 0 || cfg.Server.ArtifactMaxBytes <= 0 {
		t.Fatalf("artifact defaults = %#v", cfg.Server)
	}
	t.Setenv("LOOM_ARTIFACT_DIRECTORY", filepath.Join(t.TempDir(), "exports"))
	loaded, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Server.ArtifactDirectory != os.Getenv("LOOM_ARTIFACT_DIRECTORY") {
		t.Fatalf("artifact directory = %q", loaded.Server.ArtifactDirectory)
	}
	loaded.Server.ArtifactMaxBytes = 0
	loaded.Auth.AllowUnauthenticated = true
	loaded.Server.ClickHouse.Enabled = false
	if err := loaded.Validate(); err == nil || !strings.Contains(err.Error(), "artifact directory") {
		t.Fatalf("invalid artifact bound error = %v", err)
	}
}

func TestLoadConfigPreservesExplicitRecipeQueryPageRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  recipe_query_page_rows: 37\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Server.RecipeQueryPageRows != 37 {
		t.Fatalf("explicit YAML root query page rows = %d, want 37", cfg.Server.RecipeQueryPageRows)
	}
}

func TestLoadConfigStrictlyDecodesAndAppliesAuthSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  listen: :18080\n  dataframer:\n    recipe: /etc/loom/dataframer.json\nauth:\n  mode: calypr\n  calypr:\n    trusted_issuer: https://fence.example.test\n    request_timeout: 7s\n    cache_ttl: 45s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.Server.Listen != ":18080" || cfg.Server.Dataframer.Recipe != "/etc/loom/dataframer.json" || cfg.Server.RecipeQueryPageRows != defaultRecipeQueryPageRows || cfg.Auth.Calypr.TrustedIssuer != "https://fence.example.test" || cfg.Auth.Calypr.RequestTimeout != 7*time.Second || cfg.Auth.Calypr.CacheTTL != 45*time.Second {
		t.Fatalf("config = %#v", cfg)
	}
}

func TestDataframerRecipeIsRequiredOnlyWhenClickHouseIsEnabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.AllowUnauthenticated = true
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "server.dataframer.recipe") {
		t.Fatalf("missing dataframer recipe error = %v", err)
	}

	cfg.Server.ClickHouse.Enabled = false
	if err := cfg.Validate(); err != nil {
		t.Fatalf("disabled ClickHouse unexpectedly requires dataframer recipe: %v", err)
	}

	cfg.Server.ClickHouse.Enabled = true
	cfg.Server.Dataframer.Recipe = "/etc/loom/dataframer.json"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("configured dataframer recipe did not validate: %v", err)
	}
}

func TestLoadConfigCanDisableClickHouse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  clickhouse:\n    enabled: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Server.ClickHouse.Enabled {
		t.Fatal("ClickHouse unexpectedly enabled")
	}
}

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("auth:\n  mode: basic\n  typo: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("unknown config field unexpectedly accepted")
	}
}

func TestLoadConfigRejectsRemovedPublicationBackends(t *testing.T) {
	for name, contents := range map[string]string{
		"publication target": "server:\n  publication_target: elasticsearch\n",
		"elasticsearch":      "server:\n  elasticsearch:\n    url: http://elasticsearch:9200\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(path); err == nil {
				t.Fatal("removed publication backend configuration unexpectedly accepted")
			}
		})
	}
}

func TestRequiredDataframeSelectorsFromEnvironment(t *testing.T) {
	t.Setenv("LOOM_REQUIRED_DATAFRAME_SELECTORS", `[{"recipe":"core","translationVersion":"v1","output":"Patient"}]`)
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Server.RequiredDataframeSelectors) != 1 || cfg.Server.RequiredDataframeSelectors[0].Output != "Patient" {
		t.Fatalf("environment config = %#v", cfg.Server)
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "dataframer.recipe") {
		// Environment parsing succeeded; the unrelated default ClickHouse recipe
		// requirement remains enforced by Validate.
		t.Fatalf("validation error = %v", err)
	}
}

func TestInvalidRequiredDataframeSelectorsEnvironmentIsRejected(t *testing.T) {
	t.Setenv("LOOM_REQUIRED_DATAFRAME_SELECTORS", `{not-json}`)
	if _, err := LoadConfig(""); err == nil || !strings.Contains(err.Error(), "LOOM_REQUIRED_DATAFRAME_SELECTORS") {
		t.Fatalf("invalid selector environment = %v", err)
	}
}

func TestPopulationMappingCursorSecretFromEnvironment(t *testing.T) {
	t.Setenv("LOOM_POPULATION_MAPPING_CURSOR_SECRET", "configured-population-cursor-secret")
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.PopulationMappingCursorSecret != "configured-population-cursor-secret" {
		t.Fatalf("population mapping cursor secret = %q", cfg.Server.PopulationMappingCursorSecret)
	}
}

func TestCalyprAuthRequiresValidTrustedIssuer(t *testing.T) {
	t.Setenv("LOOM_AUTH_CALYPR_TRUSTED_ISSUER", "")

	cfg := DefaultConfig()
	cfg.Auth.Mode = "calypr"
	cfg.Server.ClickHouse.Enabled = false
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "auth.calypr.trusted_issuer is required") {
		t.Fatalf("missing trusted issuer error = %v", err)
	}

	cfg.Auth.Calypr.TrustedIssuer = "not-a-url"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "auth.calypr.trusted_issuer") {
		t.Fatalf("invalid trusted issuer error = %v", err)
	}

	cfg.Auth.Calypr.TrustedIssuer = "https://fence.example.test"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid trusted issuer: %v", err)
	}
}

func TestCalyprTrustedIssuerEnvironmentFallback(t *testing.T) {
	t.Setenv("LOOM_AUTH_CALYPR_TRUSTED_ISSUER", " https://env-fence.example.test/ ")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Auth.Calypr.TrustedIssuer != "https://env-fence.example.test/" {
		t.Fatalf("trusted issuer from environment = %q", cfg.Auth.Calypr.TrustedIssuer)
	}

	cfg.Auth.Mode = "calypr"
	cfg.Server.ClickHouse.Enabled = false
	if err := cfg.Validate(); err != nil {
		t.Fatalf("environment issuer did not validate: %v", err)
	}

	cfg.Auth.Calypr.TrustedIssuer = "https://config-fence.example.test"
	resolved, err := applyEnvironment(cfg)
	if err != nil {
		t.Fatalf("applyEnvironment() error = %v", err)
	}
	if resolved.Auth.Calypr.TrustedIssuer != cfg.Auth.Calypr.TrustedIssuer {
		t.Fatalf("configured trusted issuer was overridden: got %q, want %q", resolved.Auth.Calypr.TrustedIssuer, cfg.Auth.Calypr.TrustedIssuer)
	}
}

func TestCalyprAuthNoAuthBypassDoesNotRequireTrustedIssuer(t *testing.T) {
	for _, test := range []struct {
		name string
		set  func(*Config)
	}{
		{name: "server bypass", set: func(cfg *Config) { cfg.Server.AllowUnauthenticated = true }},
		{name: "auth bypass", set: func(cfg *Config) { cfg.Auth.AllowUnauthenticated = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Auth.Mode = "calypr"
			cfg.Server.ClickHouse.Enabled = false
			test.set(&cfg)
			if err := cfg.Validate(); err != nil {
				t.Fatalf("no-auth Calypr config unexpectedly failed validation: %v", err)
			}
		})
	}
}

// postgresReadyConfig is a valid postgres-backend configuration for use in
// failure-path tests: only the field under test is mutated afterwards.
func postgresReadyConfig() Config {
	cfg := DefaultConfig()
	cfg.Server.Backend = sourceBackendPostgres
	cfg.Server.ClickHouse.Enabled = false
	cfg.Auth.AllowUnauthenticated = true
	cfg.Server.PostgreSQL.Host = "db.internal"
	cfg.Server.PostgreSQL.Database = "loom_pg"
	cfg.Server.PostgreSQL.User = "loom"
	return cfg
}

func writeServerConfigFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSourceBackendDefaultsToArangoForLegacyConfigs(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Server.Backend != sourceBackendArango {
		t.Fatalf("default source backend = %q, want %q", cfg.Server.Backend, sourceBackendArango)
	}
	if cfg.Server.URL != defaultServerURL || cfg.Server.Database != defaultServerDatabase {
		t.Fatalf("legacy Arango defaults changed: url=%q database=%q", cfg.Server.URL, cfg.Server.Database)
	}
	pg := cfg.Server.PostgreSQL
	if pg.Port != defaultPostgresPort || pg.SSLMode != defaultPostgresSSLMode || pg.MaxOpenConns != defaultPostgresMaxOpenConns || pg.MaxIdleConns != defaultPostgresMaxIdleConns || pg.ConnMaxLifetime != defaultPostgresConnMaxLifetime {
		t.Fatalf("postgres connection defaults = %#v", pg)
	}

	loaded, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if loaded.Server.Backend != sourceBackendArango {
		t.Fatalf("environment-loaded backend = %q", loaded.Server.Backend)
	}

	path := writeServerConfigFile(t, "server:\n  url: http://arango:8529\n  database: fhir_proto\n  clickhouse:\n    enabled: false\n  allow_unauthenticated: true\n")
	legacy, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("legacy YAML LoadConfig() error = %v", err)
	}
	if legacy.Server.Backend != sourceBackendArango || legacy.Server.URL != "http://arango:8529" || legacy.Server.Database != "fhir_proto" {
		t.Fatalf("legacy YAML resolution = %#v", legacy.Server)
	}
	if err := legacy.Validate(); err != nil {
		t.Fatalf("legacy YAML Validate() error = %v", err)
	}

	options, err := parseServerOptions([]string{"--listen", ":19091", "--no-auth", "--dataframer-recipe", "recipe.json"}, flag.ContinueOnError)
	if err != nil {
		t.Fatalf("legacy CLI parseServerOptions() error = %v", err)
	}
	if options.Server.Backend != sourceBackendArango || options.Server.URL != defaultServerURL || options.Server.Database != defaultServerDatabase {
		t.Fatalf("legacy CLI resolution = %#v", options.Server)
	}
}

func TestLoadConfigExplicitArangoBackendParsesUnchanged(t *testing.T) {
	path := writeServerConfigFile(t, "server:\n  backend: arango\n  url: http://arango:8529\n  database: custom_graph\n  clickhouse:\n    enabled: false\n  allow_unauthenticated: true\n")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("explicit arango Validate() error = %v", err)
	}
	if cfg.Server.Backend != sourceBackendArango || cfg.Server.URL != "http://arango:8529" || cfg.Server.Database != "custom_graph" {
		t.Fatalf("explicit arango resolution = %#v", cfg.Server)
	}
	if cfg.Server.PostgreSQL.Port != defaultPostgresPort || cfg.Server.PostgreSQL.SSLMode != defaultPostgresSSLMode {
		t.Fatalf("explicit arango changed postgres defaults: %#v", cfg.Server.PostgreSQL)
	}
}

func TestLoadConfigPostgresBackendParsesConnectionSettings(t *testing.T) {
	path := writeServerConfigFile(t, "server:\n  backend: postgres\n  clickhouse:\n    enabled: false\n  allow_unauthenticated: true\n  postgresql:\n    host: db.internal\n    port: 5433\n    database: loom_pg\n    user: loom\n    password: yaml-pg-secret-canary\n    sslmode: verify-full\n    max_open_conns: 20\n    max_idle_conns: 8\n    conn_max_lifetime: 15m\n")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("postgres Validate() error = %v", err)
	}
	pg := cfg.Server.PostgreSQL
	if pg.Host != "db.internal" || pg.Port != 5433 || pg.Database != "loom_pg" || pg.User != "loom" || pg.Password != "yaml-pg-secret-canary" || pg.SSLMode != "verify-full" || pg.MaxOpenConns != 20 || pg.MaxIdleConns != 8 || pg.ConnMaxLifetime != 15*time.Minute {
		t.Fatalf("postgres settings = %#v", pg)
	}
	if cfg.Server.Backend != sourceBackendPostgres {
		t.Fatalf("backend = %q", cfg.Server.Backend)
	}
	// Legacy Arango fields remain at their Arango defaults and are preserved.
	if cfg.Server.URL != defaultServerURL || cfg.Server.Database != defaultServerDatabase {
		t.Fatalf("postgres selection changed legacy Arango fields: url=%q database=%q", cfg.Server.URL, cfg.Server.Database)
	}
}

func TestLoadConfigRejectsUnknownPostgresField(t *testing.T) {
	path := writeServerConfigFile(t, "server:\n  backend: postgres\n  postgresql:\n    host: db.internal\n    ssl_mode: require\n")
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("unknown postgres field unexpectedly accepted")
	}
}

func TestValidateRejectsUnknownSourceBackendBeforeStartup(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.Backend = "cockroachdb"
	cfg.Auth.AllowUnauthenticated = true
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "server.backend must be arango or postgres") {
		t.Fatalf("unknown backend Validate() error = %v", err)
	}

	if _, err := parseServerOptions([]string{"--no-auth", "--dataframer-recipe", "recipe.json", "--backend", "graphdb"}, flag.ContinueOnError); err == nil || !strings.Contains(err.Error(), "invalid server config: server.backend must be arango or postgres") {
		t.Fatalf("unknown backend CLI error = %v", err)
	}

	path := writeServerConfigFile(t, "server:\n  backend: mongodb\n  clickhouse:\n    enabled: false\n  allow_unauthenticated: true\n")
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if err := loaded.Validate(); err == nil || !strings.Contains(err.Error(), "server.backend must be arango or postgres") {
		t.Fatalf("unknown YAML backend Validate() error = %v", err)
	}
}

func TestValidateRequiresCompletePostgresConnectionSettings(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "missing host", mutate: func(c *Config) { c.Server.PostgreSQL.Host = "" }, want: "server.postgresql.host"},
		{name: "missing database", mutate: func(c *Config) { c.Server.PostgreSQL.Database = "" }, want: "server.postgresql.database"},
		{name: "missing user", mutate: func(c *Config) { c.Server.PostgreSQL.User = "" }, want: "server.postgresql.user"},
		{name: "zero port", mutate: func(c *Config) { c.Server.PostgreSQL.Port = 0 }, want: "server.postgresql.port"},
		{name: "port out of range", mutate: func(c *Config) { c.Server.PostgreSQL.Port = 70000 }, want: "server.postgresql.port"},
		{name: "unknown sslmode", mutate: func(c *Config) { c.Server.PostgreSQL.SSLMode = "needed" }, want: "server.postgresql.sslmode"},
		{name: "blank sslmode", mutate: func(c *Config) { c.Server.PostgreSQL.SSLMode = "" }, want: "server.postgresql.sslmode"},
		{name: "zero max open conns", mutate: func(c *Config) { c.Server.PostgreSQL.MaxOpenConns = 0 }, want: "server.postgresql.max_open_conns"},
		{name: "negative max open conns", mutate: func(c *Config) { c.Server.PostgreSQL.MaxOpenConns = -3 }, want: "server.postgresql.max_open_conns"},
		{name: "idle above open", mutate: func(c *Config) { c.Server.PostgreSQL.MaxOpenConns = 4; c.Server.PostgreSQL.MaxIdleConns = 5 }, want: "server.postgresql.max_idle_conns"},
		{name: "negative idle", mutate: func(c *Config) { c.Server.PostgreSQL.MaxIdleConns = -1 }, want: "server.postgresql.max_idle_conns"},
		{name: "negative lifetime", mutate: func(c *Config) { c.Server.PostgreSQL.ConnMaxLifetime = -time.Second }, want: "server.postgresql.conn_max_lifetime"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := postgresReadyConfig()
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want mention of %q", err, test.want)
			}
		})
	}
}

func TestValidateAcceptsPostgresWithoutPasswordAndWithZeroLifetime(t *testing.T) {
	cfg := postgresReadyConfig()
	cfg.Server.PostgreSQL.Password = ""
	cfg.Server.PostgreSQL.ConnMaxLifetime = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("passwordless postgres Validate() error = %v", err)
	}
}

func TestArangoBackendIgnoresPostgresContract(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.AllowUnauthenticated = true
	cfg.Server.ClickHouse.Enabled = false
	cfg.Server.PostgreSQL.Port = 0
	cfg.Server.PostgreSQL.SSLMode = "nonsense"
	cfg.Server.PostgreSQL.MaxIdleConns = 99
	cfg.Server.PostgreSQL.MaxOpenConns = -2
	if err := cfg.Validate(); err != nil {
		t.Fatalf("arango backend must not validate postgres contract: %v", err)
	}
}

func TestPostgresBackendRejectsConflictingArangoSettings(t *testing.T) {
	t.Run("customized yaml url", func(t *testing.T) {
		cfg := postgresReadyConfig()
		cfg.Server.URL = "http://arango:8529"
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "server.url") {
			t.Fatalf("customized url with postgres error = %v", err)
		}
	})
	t.Run("customized yaml database", func(t *testing.T) {
		cfg := postgresReadyConfig()
		cfg.Server.Database = "legacy_graph"
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "server.database") {
			t.Fatalf("customized database with postgres error = %v", err)
		}
	})
	base := []string{"--no-auth", "--dataframer-recipe", "recipe.json", "--backend", "postgres", "--pg-host", "db.internal", "--pg-database", "loom_pg", "--pg-user", "loom"}
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "explicit url", args: []string{"--url", "http://arango:8529"}, want: "--url"},
		{name: "explicit url default value", args: []string{"--url", defaultServerURL}, want: "--url"},
		{name: "explicit database", args: []string{"--database", "other_db"}, want: "--database"},
		{name: "explicit database default value", args: []string{"--database", defaultServerDatabase}, want: "--database"},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append(append([]string{}, base...), test.args...)
			if _, err := parseServerOptions(args, flag.ContinueOnError); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("postgres + %s error = %v, want mention of %q", test.name, err, test.want)
			}
		})
	}
	t.Run("config path explicit url", func(t *testing.T) {
		path := writeServerConfigFile(t, "server:\n  backend: postgres\n  clickhouse:\n    enabled: false\n  allow_unauthenticated: true\n  postgresql:\n    host: db.internal\n    database: loom_pg\n    user: loom\n")
		if _, err := parseServerOptions([]string{"--config", path, "--url", "http://arango:8529"}, flag.ContinueOnError); err == nil || !strings.Contains(err.Error(), "--url") {
			t.Fatalf("--config postgres + explicit --url error = %v", err)
		}
	})
	t.Run("arango backend keeps customized url", func(t *testing.T) {
		options, err := parseServerOptions([]string{"--no-auth", "--dataframer-recipe", "recipe.json", "--url", "http://arango:8529"}, flag.ContinueOnError)
		if err != nil {
			t.Fatalf("arango backend customized url error = %v", err)
		}
		if options.Server.URL != "http://arango:8529" {
			t.Fatalf("customized url = %q", options.Server.URL)
		}
	})
}

func TestParseServerOptionsPostgresBackendFromFlags(t *testing.T) {
	options, err := parseServerOptions([]string{
		"--no-auth", "--dataframer-recipe", "recipe.json",
		"--backend", "Postgres",
		"--pg-host", "db.internal", "--pg-port", "5433", "--pg-database", "loom_pg", "--pg-user", "loom",
		"--pg-password", "cli-secret", "--pg-sslmode", "require",
		"--pg-max-open-conns", "25", "--pg-max-idle-conns", "10", "--pg-conn-max-lifetime", "45m",
	}, flag.ContinueOnError)
	if err != nil {
		t.Fatalf("postgres CLI parseServerOptions() error = %v", err)
	}
	if options.Server.Backend != sourceBackendPostgres {
		t.Fatalf("backend = %q", options.Server.Backend)
	}
	pg := options.Server.PostgreSQL
	if pg.Host != "db.internal" || pg.Port != 5433 || pg.Database != "loom_pg" || pg.User != "loom" || pg.Password != "cli-secret" || pg.SSLMode != "require" || pg.MaxOpenConns != 25 || pg.MaxIdleConns != 10 || pg.ConnMaxLifetime != 45*time.Minute {
		t.Fatalf("postgres CLI settings = %#v", pg)
	}
	if options.Server.URL != defaultServerURL || options.Server.Database != defaultServerDatabase {
		t.Fatalf("postgres selection changed legacy Arango fields: url=%q database=%q", options.Server.URL, options.Server.Database)
	}
}

func TestRunRefusesPostgresBackendBeforeOpeningStores(t *testing.T) {
	cfg := postgresReadyConfig()
	if err := run(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "postgres") || !strings.Contains(err.Error(), "not wired") {
		t.Fatalf("run() with postgres backend error = %v", err)
	}

	cfg.Server.Backend = "graphdb"
	if err := run(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "server.backend must be arango or postgres") {
		t.Fatalf("run() with unknown backend error = %v", err)
	}
}

func TestPostgresCredentialsNeverAppearInConfigRenderingOrErrors(t *testing.T) {
	const credentialCanary = "Sup3r-Secret-Pa55w0rd"
	pgPassword := "postgres://loom:" + credentialCanary + "@db.internal"

	renderedConfig := func() Config {
		cfg := postgresReadyConfig()
		cfg.Server.PostgreSQL.Password = pgPassword
		cfg.Server.ClickHouse.Enabled = true
		cfg.Server.ClickHouse.Password = "clickhouse-" + credentialCanary
		cfg.Auth.Basic.Username = "loom"
		cfg.Auth.Basic.Password = "basic-" + credentialCanary
		cfg.Server.PopulationMappingCursorSecret = "cursor-" + credentialCanary
		cfg.Server.URL = "http://root:url" + credentialCanary + "@127.0.0.1:8529"
		return cfg
	}

	renders := map[string]func(Config) string{
		"%v":     func(c Config) string { return fmt.Sprintf("%v", c) },
		"%s":     func(c Config) string { return fmt.Sprintf("%s", c) },
		"%+v":    func(c Config) string { return fmt.Sprintf("%+v", c) },
		"Sprint": func(c Config) string { return fmt.Sprint(c) },
		"errorf": func(c Config) string { return fmt.Errorf("startup config: %w", fmt.Errorf("%v", c)).Error() },
	}
	for verb, render := range renders {
		out := render(renderedConfig())
		if strings.Contains(out, credentialCanary) {
			t.Fatalf("%s rendering leaked credential: %s", verb, out)
		}
		if strings.Contains(out, "postgres://") || strings.Contains(out, "root:") {
			t.Fatalf("%s rendering leaked DSN or URL credentials: %s", verb, out)
		}
		if !strings.Contains(out, redactedConfigSecret) {
			t.Fatalf("%s rendering lost redaction marker: %s", verb, out)
		}
	}

	// Every postgres validation failure path must reject without echoing
	// the password, including DSN-shaped values.
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Server.PostgreSQL.Host = "" },
		func(c *Config) { c.Server.PostgreSQL.Database = "" },
		func(c *Config) { c.Server.PostgreSQL.User = "" },
		func(c *Config) { c.Server.PostgreSQL.Port = 0 },
		func(c *Config) { c.Server.PostgreSQL.SSLMode = "needed" },
		func(c *Config) { c.Server.PostgreSQL.MaxOpenConns = 0 },
		func(c *Config) { c.Server.PostgreSQL.MaxIdleConns = 99 },
		func(c *Config) { c.Server.PostgreSQL.ConnMaxLifetime = -time.Second },
		func(c *Config) { c.Server.URL = "http://arango:8529" },
		func(c *Config) { c.Server.Database = "legacy_graph" },
	} {
		cfg := postgresReadyConfig()
		cfg.Server.PostgreSQL.Password = pgPassword
		mutate(&cfg)
		err := cfg.Validate()
		if err == nil {
			t.Fatalf("expected validation failure for a postgres contract mutation")
		}
		if strings.Contains(err.Error(), credentialCanary) || strings.Contains(err.Error(), "postgres://") {
			t.Fatalf("validation error leaked credential: %v", err)
		}
	}

	// CLI failure path: incomplete postgres settings with a password set.
	if _, err := parseServerOptions([]string{"--no-auth", "--dataframer-recipe", "recipe.json", "--backend", "postgres", "--pg-host", "db.internal", "--pg-database", "loom_pg", "--pg-password", pgPassword}, flag.ContinueOnError); err == nil || !strings.Contains(err.Error(), "server.postgresql.user") {
		t.Fatalf("incomplete postgres CLI error = %v", err)
	} else if strings.Contains(err.Error(), credentialCanary) || strings.Contains(err.Error(), "postgres://") {
		t.Fatalf("CLI validation error leaked credential: %v", err)
	}

	// YAML failure path: an unknown field next to a DSN-shaped password must
	// reject without echoing the file contents.
	path := writeServerConfigFile(t, "server:\n  backend: postgres\n  postgresql:\n    host: db.internal\n    database: loom_pg\n    user: loom\n    password: "+pgPassword+"\n    typo_field: true\n")
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("unknown postgres field with password unexpectedly accepted")
	} else if strings.Contains(err.Error(), credentialCanary) || strings.Contains(err.Error(), "postgres://") {
		t.Fatalf("YAML decode error leaked credential: %v", err)
	}
}
