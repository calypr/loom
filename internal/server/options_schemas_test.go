package server

// Schema-ownership validation on the server surface (arch-backend-05): the
// optional server.postgresql.schemas map must be rejected or accepted
// purely from the authoritative ownership record, before any pool opens or
// DDL runs. PostgreSQLConfig.validate is the boundary the factory calls
// pre-connection, so exercising it here proves the pre-pool guarantee.

import (
	"errors"
	"strings"
	"testing"

	storepostgres "github.com/calypr/loom/internal/store/postgres"
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

func TestPostgreSQLConfigSchemaOwnershipValidation(t *testing.T) {
	tests := []struct {
		name    string
		schemas map[string]string
		wantErr error // nil means acceptance; otherwise errors.Is target
	}{
		{
			name:    "absent schemas stay optional",
			schemas: nil,
		},
		{
			name:    "empty schemas map accepted",
			schemas: map[string]string{},
		},
		{
			name: "full distinct assignment accepted",
			schemas: map[string]string{
				"source": "loom_source", "explorer_control": "loom_explorer",
				"publication_control": "loom_publication", "dataset_release": "loom_release",
				"recipe_chains": "loom_recipe", "recipe_exec_registries": "loom_exec",
			},
		},
		{
			name: "recipe pair may share one schema (byte-identical ledger v1)",
			schemas: map[string]string{
				"recipe_chains": "loom_recipe", "recipe_exec_registries": "loom_recipe",
			},
		},
		{
			name: "source and explorer on one schema conflict at ledger v1",
			schemas: map[string]string{
				"source": "shared", "explorer_control": "shared",
			},
			wantErr: storepostgres.ErrInvalidAssignment,
		},
		{
			name: "source and dataset release on one schema conflict at v8",
			schemas: map[string]string{
				"source": "shared", "dataset_release": "shared",
			},
			wantErr: storepostgres.ErrInvalidAssignment,
		},
		{
			name: "unknown series rejected",
			schemas: map[string]string{
				"warehouse_ledger": "loom_anything",
			},
			wantErr: storepostgres.ErrInvalidAssignment,
		},
		{
			name: "unsafe schema identifier rejected",
			schemas: map[string]string{
				"source": "Loom Source",
			},
			wantErr: storepostgres.ErrInvalidAssignment,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validPostgreSQLConfig()
			cfg.Schemas = tc.schemas
			err := cfg.validate()
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("validate with schemas %v = %v, want acceptance", tc.schemas, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validate accepted schemas %v, want rejection", tc.schemas)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error %v does not wrap %v", err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), "server.postgresql.schemas") {
				t.Fatalf("error %q must name the server.postgresql.schemas field", err)
			}
		})
	}
}

func TestPostgreSQLConfigSchemaConflictCarriesStructuredIdentity(t *testing.T) {
	cfg := validPostgreSQLConfig()
	cfg.Schemas = map[string]string{"source": "shared", "publication_control": "shared"}
	err := cfg.validate()
	if err == nil {
		t.Fatal("validate accepted conflicting schema ownership")
	}
	var conflict *storepostgres.AssignmentConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("error %v is not an *AssignmentConflict", err)
	}
	if conflict.Schema != "shared" || conflict.Version != 4 {
		t.Fatalf("conflict = schema %q version %d, want shared at version 4 (first shared disagreement)",
			conflict.Schema, conflict.Version)
	}
	// The message must stay non-secret: field name plus identifiers only.
	if strings.Contains(err.Error(), validPostgreSQLConfig().Password) && cfg.Password != "" {
		t.Fatal("password must never appear in an ownership error")
	}
}
