package server

import (
	"fmt"
	"os"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// serverOptions is the normalized command-line surface. Config remains the
// YAML compatibility boundary; Run owns the final merge and validation.
type serverOptions struct {
	ConfigPath, Listen, Backend, URL, Database, Schema string
	NoAuth, DatasetGenerations                         bool
	ClickHouseURL, ClickHouseDatabase                  string
	ClickHouseUsername, ClickHousePassword             string
	DataframerRecipe                                   string
	RecipeBatchRows, RecipeBatchBytes                  int
}

type Config struct {
	Server ServerConfig `yaml:"server"`
	Auth   AuthConfig   `yaml:"auth"`
}

type ServerConfig struct {
	Listen               string           `yaml:"listen"`
	Backend              string           `yaml:"backend"`
	URL                  string           `yaml:"url"`
	Database             string           `yaml:"database"`
	Schema               string           `yaml:"schema"`
	Arango               ArangoConfig     `yaml:"arango"`
	DatasetGenerations   bool             `yaml:"dataset_generations"`
	ClickHouse           ClickHouseConfig `yaml:"clickhouse"`
	Dataframer           DataframerConfig `yaml:"dataframer"`
	RecipeBatchRows      int              `yaml:"recipe_batch_rows"`
	RecipeBatchBytes     int              `yaml:"recipe_batch_bytes"`
	AllowUnauthenticated bool             `yaml:"allow_unauthenticated"`
}

type ArangoConfig struct {
	URL      string `yaml:"url"`
	Database string `yaml:"database"`
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
			Listen: ":8080", Backend: "arango", URL: "http://127.0.0.1:8529", Database: "fhir_proto",
			Schema: "schemas/graph-fhir.json", ClickHouse: ClickHouseConfig{Enabled: true, URL: "clickhouse://127.0.0.1:9000", Database: "loom", Username: "default"},
			RecipeBatchRows: 1000, RecipeBatchBytes: 4 << 20,
		},
		Auth: AuthConfig{Mode: "basic", Calypr: CalyprAuthConfig{RequestTimeout: 5 * time.Second, CacheTTL: 30 * time.Second}},
	}
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if strings.TrimSpace(path) == "" {
		return applyEnvironment(cfg), nil
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
	if cfg.Server.Arango.URL != "" {
		cfg.Server.URL = cfg.Server.Arango.URL
	}
	if cfg.Server.Arango.Database != "" {
		cfg.Server.Database = cfg.Server.Arango.Database
	}
	return applyEnvironment(cfg), nil
}

func applyEnvironment(cfg Config) Config {
	if cfg.Auth.Basic.Username == "" {
		cfg.Auth.Basic.Username = os.Getenv("LOOM_AUTH_BASIC_USERNAME")
	}
	if cfg.Auth.Basic.Password == "" {
		cfg.Auth.Basic.Password = os.Getenv("LOOM_AUTH_BASIC_PASSWORD")
	}
	return cfg
}

func (c Config) Validate() error {
	cfg := c
	if cfg.Server.ClickHouse.Enabled && strings.TrimSpace(cfg.Server.Dataframer.Recipe) == "" {
		return fmt.Errorf("server.dataframer.recipe is required when server.clickhouse.enabled is true")
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
