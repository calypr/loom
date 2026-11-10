package server

// Schema-ownership config surface (arch-backend-05, review on PR #110
// thread PRRT_kwDOS-yV5s61pLW-): no production factory consumes
// server.postgresql.schemas today -- the source series migrates via the
// ingest CLI flag and the other five series have no production migrate
// call sites -- so any explicit entry must FAIL configuration (fail
// closed) instead of being validated and silently ignored. Absent/empty
// keeps working configurations compatible. The authoritative assignment
// semantics (unknown series, unsafe identifiers, ledger-disagreeing
// sharing, structured *AssignmentConflict identity including its Schema
// field) are asserted at the owning layer in
// internal/store/postgres/ownership_contract_test.go
// (ValidateAssignmentMatrix).

import (
	"strings"
	"testing"
)

func validPostgreSQLConfig() PostgreSQLConfig {
	return PostgreSQLConfig{
		Host:            "db.internal",
		Port:            5432,
		Database:        "loom",
		User:            "loom",
		SSLMode:         "require",
		MaxOpenConns:    4,
		MaxIdleConns:    1,
		ConnMaxLifetime: 0,
	}
}

func TestPostgreSQLConfigSchemasFailClosedUntilConsumed(t *testing.T) {
	tests := []struct {
		name    string
		schemas map[string]string
		accept  bool
	}{
		{
			name:    "absent schemas stay optional",
			schemas: nil,
			accept:  true,
		},
		{
			name:    "empty schemas map accepted",
			schemas: map[string]string{},
			accept:  true,
		},
		{
			name:    "single series assignment rejected until a factory honors it",
			schemas: map[string]string{"source": "loom_source"},
		},
		{
			name: "full distinct assignment rejected even though every pair is ownership-legal",
			schemas: map[string]string{
				"source": "loom_source", "explorer_control": "loom_explorer",
				"publication_control": "loom_publication", "dataset_release": "loom_release",
				"recipe_chains": "loom_recipe", "recipe_exec_registries": "loom_exec",
			},
		},
		{
			name: "recipe pair sharing one schema also rejected (nothing consumes it)",
			schemas: map[string]string{
				"recipe_chains": "loom_recipe", "recipe_exec_registries": "loom_recipe",
			},
		},
		{
			name: "ledger-disagreeing sharing rejected at the fail-closed gate",
			schemas: map[string]string{
				"source": "shared", "explorer_control": "shared",
			},
		},
		{
			name: "unknown series rejected",
			schemas: map[string]string{
				"warehouse_ledger": "loom_anything",
			},
		},
		{
			name: "unsafe schema identifier rejected",
			schemas: map[string]string{
				"source": "Loom Source",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validPostgreSQLConfig()
			cfg.Schemas = tc.schemas
			err := cfg.validate()
			if tc.accept {
				if err != nil {
					t.Fatalf("validate with schemas %v = %v, want acceptance", tc.schemas, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validate accepted schemas %v; an unconsumed assignment must fail closed, not pass silently", tc.schemas)
			}
			msg := err.Error()
			if !strings.Contains(msg, "server.postgresql.schemas") {
				t.Fatalf("error %q must name the server.postgresql.schemas field", msg)
			}
			// The actionable truth an operator needs: the assignment is
			// not consumed yet.
			if !strings.Contains(msg, "not yet consumed by any production factory") {
				t.Fatalf("error %q must state the assignment is not yet consumed", msg)
			}
			// The rejection stays non-secret: it must not echo connection
			// values from the config it came from.
			for _, secret := range []string{cfg.Host, cfg.User, cfg.Database} {
				if strings.Contains(msg, secret) {
					t.Fatalf("error %q must not echo config value %q", msg, secret)
				}
			}
		})
	}
}
