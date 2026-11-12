package server

// Schema-ownership config surface. Original law (arch-backend-05, review on
// PR #110 thread PRRT_kwDOS-yV5s61pLW-): server.postgresql.schemas entries
// failed closed while no production factory consumed them.
// pg-runtime-first-query-route is the documented "once a factory honors the
// map" moment named by the field comment: runPostgreSQLFirstQueryRoute
// consumes exactly the source + dataset_release pair.
// pg-runtime-server-composition added the second consumable shape: the
// complete six-series ladder, consumed by runPostgreSQLFullRuntime
// (runtime.go). Both shapes pass through the backend factory, where
// storepostgres.ValidateAssignment stays the authoritative pre-pool gate.
// Every other shape (foreign series, single ladder members, subsets beyond
// the pair) still FAILS configuration (fail closed); each consumable shape
// is accepted only in its exact, distinct, non-blank form. Absent/empty
// stays valid here and keeps the routes' pre-dial consumability gates on the
// ErrPostgreSQLRuntimeNotWired refusal rather than a partial composition.
// The authoritative assignment semantics (unknown series, unsafe
// identifiers, ledger-disagreeing sharing, structured *AssignmentConflict
// identity including its Schema field) are asserted at the owning layer in
// internal/backends/postgres/internal/store/ownership_contract_test.go
// (ValidateAssignmentMatrix).

import (
	"context"
	"strings"
	"testing"
	"time"
)

func validPostgreSQLConfig() PostgreSQLConfig {
	return PostgreSQLConfig{
		Host:     "db.internal",
		Port:     5432,
		Database: "loom",
		User:     "loom",
		// Non-empty, distinctive credential so the no-secret assertion
		// below actually exercises password leakage (review thread
		// PRRT_kwDOS-yV5s61pLXK; same canary discipline as
		// config_test.go's credentialCanary).
		Password:        "ab05-Sup3r-Pg-Pa55w0rd-canary",
		SSLMode:         "require",
		MaxOpenConns:    4,
		MaxIdleConns:    1,
		ConnMaxLifetime: 0,
	}
}

func TestPostgreSQLConfigSchemasConsumablePairAndFailClosed(t *testing.T) {
	tests := []struct {
		name    string
		schemas map[string]string
		accept  bool
		// wantSubstrings, when set, must all appear in the rejection.
		wantSubstrings []string
	}{
		{
			name:    "absent schemas stay optional (route gate then keeps the startup refusal)",
			schemas: nil,
			accept:  true,
		},
		{
			name:    "empty schemas map accepted",
			schemas: map[string]string{},
			accept:  true,
		},
		{
			name:    "the consumable pair is accepted once the factory honors it",
			schemas: map[string]string{"source": "loom_source", "dataset_release": "loom_release"},
			accept:  true,
		},
		{
			name:           "source alone rejected: neither consumable shape",
			schemas:        map[string]string{"source": "loom_source"},
			wantSubstrings: []string{"consumable shapes", "got 1 entries"},
		},
		{
			name:           "dataset_release alone rejected: neither consumable shape",
			schemas:        map[string]string{"dataset_release": "loom_release"},
			wantSubstrings: []string{"consumable shapes", "got 1 entries"},
		},
		{
			name:           "blank source rejected",
			schemas:        map[string]string{"source": " ", "dataset_release": "loom_release"},
			wantSubstrings: []string{"cannot be blank"},
		},
		{
			name:           "blank dataset_release rejected",
			schemas:        map[string]string{"source": "loom_source", "dataset_release": ""},
			wantSubstrings: []string{"cannot be blank"},
		},
		{
			name:           "source and dataset_release sharing one schema rejected",
			schemas:        map[string]string{"source": "shared", "dataset_release": "shared"},
			wantSubstrings: []string{"must name distinct schemas"},
		},
		{
			// The assignment trims before the factory sees the pair, so a
			// whitespace-only-differing pair must get THIS precise message
			// at validate time, not the generic not-wired sentinel after
			// the trimmed names collapse (review e22c4825 optional 3).
			name:           "whitespace-only-differing pair gets the precise distinct-schema refusal",
			schemas:        map[string]string{"source": " shared ", "dataset_release": "shared"},
			wantSubstrings: []string{"must name distinct schemas"},
		},
		{
			// Single ladder members are consumed only by the full ladder, so
			// alone they are neither consumable shape and fail closed.
			name:           "lone explorer_control entry rejected: neither consumable shape",
			schemas:        map[string]string{"explorer_control": "loom_explorer"},
			wantSubstrings: []string{"explorer_control", "consumable shapes", "got 1 entries"},
		},
		{
			name:           "lone publication_control entry rejected: neither consumable shape",
			schemas:        map[string]string{"publication_control": "loom_publication"},
			wantSubstrings: []string{"publication_control", "consumable shapes", "got 1 entries"},
		},
		{
			name:           "lone recipe_chains entry rejected: neither consumable shape",
			schemas:        map[string]string{"recipe_chains": "loom_recipe"},
			wantSubstrings: []string{"recipe_chains", "consumable shapes", "got 1 entries"},
		},
		{
			name:           "lone recipe_exec_registries entry rejected: neither consumable shape",
			schemas:        map[string]string{"recipe_exec_registries": "loom_exec"},
			wantSubstrings: []string{"recipe_exec_registries", "consumable shapes", "got 1 entries"},
		},
		{
			name: "a series beside the consumable pair that does not complete the ladder fails closed",
			schemas: map[string]string{
				"source": "loom_source", "dataset_release": "loom_release",
				"explorer_control": "loom_explorer",
			},
			wantSubstrings: []string{"explorer_control", "consumable shapes", "got 3 entries"},
		},
		{
			name: "the complete distinct six-series ladder is accepted (full runtime)",
			schemas: map[string]string{
				"source": "loom_source", "explorer_control": "loom_explorer",
				"publication_control": "loom_publication", "dataset_release": "loom_release",
				"recipe_chains": "loom_recipe", "recipe_exec_registries": "loom_exec",
			},
			accept: true,
		},
		{
			name: "the six-series ladder with two series sharing one schema fails closed",
			schemas: map[string]string{
				"source": "loom_source", "explorer_control": "loom_explorer",
				"publication_control": "loom_publication", "dataset_release": "loom_release",
				"recipe_chains": "loom_exec", "recipe_exec_registries": "loom_exec",
			},
			wantSubstrings: []string{"must name distinct schemas"},
		},
		{
			name: "unknown series rejected",
			schemas: map[string]string{
				"warehouse_ledger": "loom_anything",
			},
			wantSubstrings: []string{"warehouse_ledger", "not consumed by any production factory"},
		},
		{
			// Six entries with a typo'd member hits the foreign-series law
			// first: the assignment is never silently treated as a ladder.
			name: "six entries with a typo'd ladder member rejected as foreign series",
			schemas: map[string]string{
				"source": "loom_source", "explorer_control": "loom_explorer",
				"publication_control": "loom_publication", "dataset_release": "loom_release",
				"recipe_chainz": "loom_recipe", "recipe_exec_registries": "loom_exec",
			},
			wantSubstrings: []string{"recipe_chainz", "not consumed by any production factory"},
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
				t.Fatalf("validate accepted schemas %v; an unconsumed or malformed assignment must fail closed, not pass silently", tc.schemas)
			}
			msg := err.Error()
			if !strings.Contains(msg, "server.postgresql.schemas") {
				t.Fatalf("error %q must name the server.postgresql.schemas field", msg)
			}
			for _, want := range tc.wantSubstrings {
				if !strings.Contains(msg, want) {
					t.Fatalf("error %q must contain %q", msg, want)
				}
			}
			// The rejection stays non-secret: it must not echo connection
			// values -- above all the password -- from the config it came
			// from. The fixture guard keeps this assertion reachable: an
			// empty fixture Password would make every Contains below
			// trivially true and the leak check a lie.
			if cfg.Password == "" {
				t.Fatal("fixture must carry a non-empty Password so the no-secret check is exercised")
			}
			for _, secret := range []string{cfg.Password, cfg.Host, cfg.User, cfg.Database} {
				if strings.Contains(msg, secret) {
					t.Fatalf("error %q must not echo config value %q", msg, secret)
				}
			}
		})
	}
}

// TestPostgreSQLConfigSchemasUnsafeIdentifierFailsPreDial pins that a
// malformed schema name never reaches a connection: validate() itself
// delegates identifier policy to the factory (no second policy here), so the
// refusal must arrive from postgres.Open's pre-dial Config.Validate, not
// from a dial. 192.0.2.1 is TEST-NET-1: any dial attempt would hang until
// the deadline instead of returning the named factory refusal.
func TestPostgreSQLConfigSchemasUnsafeIdentifierFailsPreDial(t *testing.T) {
	cfg := Config{}
	cfg.Server.Backend = sourceBackendPostgres
	cfg.Server.PostgreSQL = validPostgreSQLConfig()
	cfg.Server.PostgreSQL.Host = "192.0.2.1"
	cfg.Server.PostgreSQL.Port = 60543
	cfg.Server.PostgreSQL.Schemas = map[string]string{"source": "Loom Source", "dataset_release": "loom_release"}

	// TEST-NET-1 dials never answer, so the deadline itself is the bound:
	// a pass must return the pre-dial refusal long before this fires.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := RunWithConfig(ctx, cfg)
	if err == nil {
		t.Fatal("unsafe schema identifier unexpectedly started the server")
	}
	if strings.Contains(err.Error(), cfg.Server.PostgreSQL.Password) {
		t.Fatalf("refusal leaked the connection password: %v", err)
	}
	// Must not be the not-consumed sentinel: the pair shape is consumable,
	// the refusal is the factory's own pre-dial schema-name law.
	if err == ErrPostgreSQLRuntimeNotWired {
		t.Fatalf("unsafe identifier was refused by the consumability sentinel instead of the factory's pre-dial identifier law: %v", err)
	}
	if !strings.Contains(err.Error(), "Loom Source") && !strings.Contains(err.Error(), "schema") {
		t.Fatalf("error %q must name the unsafe schema identifier or the schema law", err)
	}
}
