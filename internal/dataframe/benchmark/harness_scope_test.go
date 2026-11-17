package benchmark_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calypr/loom/internal/dataframe/benchmark"
)

// TestWriteRoutesCarryAuthResourcePath pins the scoped-authorization
// contract that a first live run discovered: AuthorizeWrite refuses a
// durable write with "auth_resource_path is required when scoped
// authorization is enabled", and every write route reads the value from the
// query string, not the body. A harness that omits it cannot publish a
// single workspace against a scoped deployment, so the param is asserted per
// route here instead of during a benchmark window.
func TestWriteRoutesCarryAuthResourcePath(t *testing.T) {
	const path = "/programs/loom/projects/loom-bench/benchmark"
	seen := map[string]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/projects/loom-bench/generations/gen-1/explorer-config", func(w http.ResponseWriter, r *http.Request) {
		seen["publish"] = r.URL.Query().Get("auth_resource_path")
		_ = json.NewEncoder(w).Encode(map[string]any{"receiptId": "rcpt-1", "revisionId": "rev-1", "recipe": "rec", "translationVersion": "tv", "executionId": "ex", "activated": true})
	})
	mux.HandleFunc("/api/v1/projects/loom-bench/explorers", func(w http.ResponseWriter, r *http.Request) {
		seen["create"] = r.URL.Query().Get("auth_resource_path")
		_ = json.NewEncoder(w).Encode(map[string]any{"explorerId": "ex-1"})
	})
	mux.HandleFunc("/api/v1/projects/loom-bench/explorers/ex-1/authoring/v2/reconcile", func(w http.ResponseWriter, r *http.Request) {
		seen["reconcile"] = r.URL.Query().Get("auth_resource_path")
		_ = json.NewEncoder(w).Encode(map[string]any{"receiptId": "rcpt-1"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	ctx := context.Background()
	state := benchmark.BuilderState{DraftVersion: 1}
	state.Catalog.SnapshotToken = "snap"
	run := func(arm *benchmark.Arm) {
		if _, err := arm.PublishWorkspace(ctx, "tok", "loom-bench", "gen-1", []byte(`{}`), "0123456789abcdef0123456789abcdef01234567"); err != nil {
			t.Fatalf("publish: %v", err)
		}
		if _, err := arm.CreateExplorer(ctx, "tok", "loom-bench", "w1", "W1", "default"); err != nil {
			t.Fatalf("create explorer: %v", err)
		}
		if _, err := arm.Reconcile(ctx, "tok", "loom-bench", "ex-1", "out", state); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
	}

	run(&benchmark.Arm{Backend: benchmark.BackendPostgres, BaseURL: server.URL, Client: server.Client(), AuthResourcePath: path})
	for _, route := range []string{"publish", "create", "reconcile"} {
		if seen[route] != path {
			t.Errorf("%s: auth_resource_path = %q, want %q", route, seen[route], path)
		}
	}

	// An unscoped deployment must not receive the parameter at all: sending a
	// scope the caller does not hold would narrow reads for no reason.
	for k := range seen {
		delete(seen, k)
	}
	run(&benchmark.Arm{Backend: benchmark.BackendPostgres, BaseURL: server.URL, Client: server.Client()})
	for _, route := range []string{"publish", "create", "reconcile"} {
		if seen[route] != "" {
			t.Errorf("%s: unscoped arm sent auth_resource_path=%q, want none", route, seen[route])
		}
	}
}
