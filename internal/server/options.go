package server

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataset"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	Server ServerConfig `yaml:"server"`
	Auth   AuthConfig   `yaml:"auth"`
}

type ServerConfig struct {
	Listen                     string                      `yaml:"listen"`
	URL                        string                      `yaml:"url"`
	Database                   string                      `yaml:"database"`
	Schema                     string                      `yaml:"schema"`
	ClickHouse                 ClickHouseConfig            `yaml:"clickhouse"`
	Dataframer                 DataframerConfig            `yaml:"dataframer"`
	RecipeBatchRows            int                         `yaml:"recipe_batch_rows"`
	RecipeBatchBytes           int                         `yaml:"recipe_batch_bytes"`
	AllowUnauthenticated       bool                        `yaml:"allow_unauthenticated"`
	RequiredDataframeSelectors []dataset.DataframeSelector `yaml:"required_dataframe_selectors"`
}

type ClickHouseConfig struct {
	Enabled  bool   `yaml:"enabled"`
	URL      string `yaml:"url"`
	Database string `yaml:"database"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
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
	RequestTimeout time.Duration `yaml:"request_timeout"`
	CacheTTL       time.Duration `yaml:"cache_ttl"`
}

func DefaultConfig() Config {
	return Config{
		Server: ServerConfig{
			Listen: ":8080", URL: "http://127.0.0.1:8529", Database: "fhir_proto",
			Schema: "schemas/graph-fhir.json", ClickHouse: ClickHouseConfig{Enabled: true, URL: "clickhouse://127.0.0.1:9000", Database: "loom", Username: "default"},
			RecipeBatchRows: 1000, RecipeBatchBytes: 4 << 20,
		},
		Auth: AuthConfig{Mode: "basic", Calypr: CalyprAuthConfig{RequestTimeout: 5 * time.Second, CacheTTL: 30 * time.Second}},
	}
}

func parseServerOptions(args []string, handling flag.ErrorHandling) (Config, error) {
	var (
		configPath         string
		listen             string
		noAuth             bool
		url                string
		database           string
		schema             string
		clickhouseURL      string
		clickhouseDatabase string
		clickhouseUsername string
		clickhousePassword string
		dataframerRecipe   string
		recipeBatchRows    int
		recipeBatchBytes   int
	)
	fs := flag.NewFlagSet("arango-fhir-server", handling)
	fs.StringVar(&configPath, "config", "", "YAML server configuration file")
	fs.StringVar(&listen, "listen", ":8080", "HTTP listen address")
	fs.BoolVar(&noAuth, "no-auth", false, "disable scoped authorization for local development")
	fs.StringVar(&url, "url", "http://127.0.0.1:8529", "ArangoDB URL")
	fs.StringVar(&database, "database", "fhir_proto", "ArangoDB database")
	fs.StringVar(&schema, "schema", "schemas/graph-fhir.json", "FHIR graph schema path for imports")
	fs.StringVar(&clickhouseURL, "clickhouse-url", "clickhouse://127.0.0.1:9000", "ClickHouse native URL for published dataframe reads")
	fs.StringVar(&clickhouseDatabase, "clickhouse-database", "loom", "ClickHouse database for published dataframe reads")
	fs.StringVar(&clickhouseUsername, "clickhouse-username", "default", "ClickHouse username for published dataframe reads")
	fs.StringVar(&clickhousePassword, "clickhouse-password", "", "ClickHouse password for published dataframe reads")
	fs.StringVar(&dataframerRecipe, "dataframer-recipe", "", "dataframer recipe JSON file (required when ClickHouse is enabled)")
	fs.IntVar(&recipeBatchRows, "recipe-batch-rows", 1000, "maximum recipe materialization rows per ClickHouse batch")
	fs.IntVar(&recipeBatchBytes, "recipe-batch-bytes", 4<<20, "maximum recipe materialization bytes per ClickHouse batch")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

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
	cfg.Server.URL = url
	cfg.Server.Database = database
	cfg.Server.Schema = schema
	cfg.Server.ClickHouse.URL = clickhouseURL
	cfg.Server.ClickHouse.Database = clickhouseDatabase
	cfg.Server.ClickHouse.Username = clickhouseUsername
	cfg.Server.ClickHouse.Password = clickhousePassword
	cfg.Server.Dataframer.Recipe = dataframerRecipe
	cfg.Server.RecipeBatchRows = recipeBatchRows
	cfg.Server.RecipeBatchBytes = recipeBatchBytes
	if noAuth {
		cfg.Server.AllowUnauthenticated = true
		cfg.Auth.AllowUnauthenticated = true
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
	if cfg.Auth.Basic.Username == "" {
		cfg.Auth.Basic.Username = os.Getenv("LOOM_AUTH_BASIC_USERNAME")
	}
	if cfg.Auth.Basic.Password == "" {
		cfg.Auth.Basic.Password = os.Getenv("LOOM_AUTH_BASIC_PASSWORD")
	}
	if raw := strings.TrimSpace(os.Getenv("LOOM_REQUIRED_DATAFRAME_SELECTORS")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg.Server.RequiredDataframeSelectors); err != nil {
			return Config{}, fmt.Errorf("parse LOOM_REQUIRED_DATAFRAME_SELECTORS: %w", err)
		}
	}
	return cfg, nil
}

func (c Config) Validate() error {
	cfg := c
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
		if cfg.Auth.Calypr.RequestTimeout <= 0 {
			return fmt.Errorf("auth.calypr.request_timeout must be positive")
		}
		if cfg.Auth.Calypr.CacheTTL <= 0 {
			return fmt.Errorf("auth.calypr.cache_ttl must be positive")
		}
	}
	return nil
}
