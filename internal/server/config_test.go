package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
	if err := os.WriteFile(path, []byte("server:\n  listen: :18080\nauth:\n  mode: calypr\n  calypr:\n    request_timeout: 7s\n    cache_ttl: 45s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.Server.Listen != ":18080" || cfg.Auth.Calypr.RequestTimeout != 7*time.Second || cfg.Auth.Calypr.CacheTTL != 45*time.Second {
		t.Fatalf("config = %#v", cfg)
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
