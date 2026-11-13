package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const postgreSQLRefusalMessage = "server.backend postgres is configured, but the PostgreSQL source runtime is not wired yet; refusing to start on the ArangoDB backend"

func TestPostgreSQLBackendStartupRefusalInvariant(t *testing.T) {
	t.Run("postgres_selection_refuses_with_the_named_sentinel", func(t *testing.T) {
		cfg := Config{}
		cfg.Server.Backend = sourceBackendPostgres
		cfg.Server.PostgreSQL.Host = "192.0.2.1"
		cfg.Server.PostgreSQL.Port = 60543
		cfg.Server.PostgreSQL.Database = "loom_refusal_witness"
		cfg.Server.PostgreSQL.User = "loom_refusal_witness"

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		started := time.Now()
		err := RunWithConfig(ctx, cfg)
		elapsed := time.Since(started)

		if !errors.Is(err, ErrPostgreSQLRuntimeNotWired) {
			t.Fatalf("RunWithConfig(postgres) err = %v, want errors.Is(err, ErrPostgreSQLRuntimeNotWired)", err)
		}
		if got := err.Error(); got != postgreSQLRefusalMessage {
			t.Fatalf("refusal message drifted: got %q, want byte-stable %q", got, postgreSQLRefusalMessage)
		}
		if elapsed > 1500*time.Millisecond {
			t.Fatalf("refusal took %v: the gate must refuse before any connection attempt", elapsed)
		}
		if strings.Contains(err.Error(), "192.0.2.1") || strings.Contains(err.Error(), "loom_refusal_witness") {
			t.Fatalf("refusal error leaked connection material: %v", err)
		}
	})

	t.Run("alive_control_arango_selection_passes_the_gate", func(t *testing.T) {
		cfg := Config{}
		cfg.Server.Backend = sourceBackendArango
		cfg.Server.URL = "http://127.0.0.1:1"
		cfg.Server.Database = "loom_gate_alive_control"

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := RunWithConfig(ctx, cfg)
		if err == nil {
			t.Fatal("RunWithConfig(arango) unexpectedly started against a closed port")
		}
		if errors.Is(err, ErrPostgreSQLRuntimeNotWired) {
			t.Fatal("Arango selection was refused by the PostgreSQL gate")
		}
	})
}
