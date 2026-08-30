package server

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseServerOptionsWithoutConfigUsesFlags(t *testing.T) {
	options, err := parseServerOptions([]string{"--listen", ":19091", "--no-auth", "--dataframer-recipe", "recipe.json"}, flag.ContinueOnError)
	if err != nil {
		t.Fatalf("parseServerOptions() error = %v", err)
	}
	if options.Server.Listen != ":19091" || !options.Server.AllowUnauthenticated || !options.Auth.AllowUnauthenticated {
		t.Fatalf("resolved options = %#v", options)
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

func TestLoadConfigStrictlyDecodesAndAppliesAuthSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  listen: :18080\n  dataframer:\n    recipe: /etc/loom/dataframer.json\nauth:\n  mode: calypr\n  calypr:\n    request_timeout: 7s\n    cache_ttl: 45s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.Server.Listen != ":18080" || cfg.Server.Dataframer.Recipe != "/etc/loom/dataframer.json" || cfg.Auth.Calypr.RequestTimeout != 7*time.Second || cfg.Auth.Calypr.CacheTTL != 45*time.Second {
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

func TestRequiredDataframeSelectorsAndSnapshotRetentionFromEnvironment(t *testing.T) {
	t.Setenv("LOOM_REQUIRED_DATAFRAME_SELECTORS", `[{"recipe":"core","translationVersion":"v1","output":"Patient"}]`)
	t.Setenv("LOOM_SNAPSHOT_RETENTION", "48h")
	t.Setenv("LOOM_SNAPSHOT_DIRECTORY", t.TempDir())
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Server.RequiredDataframeSelectors) != 1 || cfg.Server.RequiredDataframeSelectors[0].Output != "Patient" || cfg.Server.SnapshotRetention != 48*time.Hour {
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
