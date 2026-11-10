// Authoritative production migration owner for the publication control-plane
// schema (arch-backend-05, series publication_control).
//
// This package is the single owner of the complete publication control-plane
// history: the ledger bootstrap, the bundle catalog (registry.go), the lease
// checkpoints (leases.go), the private ClickHouse artifact catalog (owned by
// internal/dataframe/execution/chartifact/postgres, which keeps no DDL of its
// own), and the read-retention pins (read_pins.go). The versions live on one
// shared ledger version space reserved by those tickets; the bytes are
// immutable and their identity is recorded in
// internal/store/postgres/migration_ownership.go. Production startup must
// apply this history through MigrateSchema -- never fragments assembled from
// tests/ directories.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"

	pgstore "github.com/calypr/loom/internal/store/postgres"
)

// migrationsFS holds the complete publication control-plane history. Files
// are named <version>_<name>.sql and are immutable once applied; new DDL is
// appended as a NEW version through the shared pg-store-02 runner and
// recorded in the ownership manifest in the same change.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// OwnerMigrations returns this package's embedded publication control-plane
// migration history, validated and sorted by version with the pg-store-02
// loader. The returned checksums are the ledger identity of the applied
// migrations and must match the manifest in internal/store/postgres.
func OwnerMigrations() ([]pgstore.Migration, error) {
	migrations, err := pgstore.LoadMigrations(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("publication postgres: load owner migrations: %w", err)
	}
	return migrations, nil
}

// MigrateSchema applies the complete publication control-plane history to
// one schema of the database reachable through src, serialized by the
// pg-store-02 advisory lock. The caller owns schema creation, and this
// history must be the only one ever applied to that schema: a schema already
// migrated by an incompatible history (see
// internal/store/postgres.ValidateAssignment) fails closed with a named
// ledger error before any statement runs. Re-applying the applied history is
// a no-op: the DDL is IF NOT EXISTS, so a rerun never truncates catalog,
// lease, or pin state. Failures are returned, never absorbed: there is no
// fallback to another backend.
func MigrateSchema(ctx context.Context, src pgstore.ConnSource, schema string) (pgstore.MigrateResult, error) {
	if src == nil {
		return pgstore.MigrateResult{}, errors.New("publication postgres: nil migration connection source")
	}
	migrations, err := OwnerMigrations()
	if err != nil {
		return pgstore.MigrateResult{}, err
	}
	runner, err := pgstore.NewMigrationRunner(src, schema, migrations)
	if err != nil {
		return pgstore.MigrateResult{}, err
	}
	return runner.Migrate(ctx)
}
