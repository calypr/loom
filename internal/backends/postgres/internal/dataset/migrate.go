// Authoritative production migration owner for the dataset release/generation
// lane schema (arch-backend-05, series dataset_release).
//
// This package is the single owner of the complete dataset lane history: the
// ledger bootstrap, the generation manifest (manifest_lifecycle.go), and the
// project releases plus active-release pointers (release_store.go). The
// bootstrap and manifest bytes are the immutable pg-store-02 originals (this
// series keeps the version/name/checksum identity those rows recorded on the
// lane schema), and the whole history's identity is recorded in
// internal/backends/postgres/internal/store/migration_ownership.go. Production startup must
// apply this history through MigrateSchema -- never fragments assembled from
// testdata directories.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
)

// migrationsFS holds the complete dataset lane history. Files are named
// <version>_<name>.sql and are immutable once applied; new DDL is appended
// as a NEW version through the shared pg-store-02 runner and recorded in the
// ownership manifest in the same change.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// OwnerMigrations returns this package's embedded dataset lane migration
// history, validated and sorted by version with the pg-store-02 loader. The
// returned checksums are the ledger identity of the applied migrations and
// must match the manifest in internal/backends/postgres/internal/store.
func OwnerMigrations() ([]storepostgres.Migration, error) {
	migrations, err := storepostgres.LoadMigrations(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("dataset postgres: load owner migrations: %w", err)
	}
	return migrations, nil
}

// MigrateSchema applies the complete dataset lane history to one schema of
// the database reachable through src, serialized by the pg-store-02 advisory
// lock. The caller owns schema creation, and this history must be the only
// one ever applied to that schema: assigning it the schema already migrated
// by the source series is refused by
// internal/backends/postgres/internal/store.ValidateAssignment (version 8 disagrees), and
// applying it anyway fails closed with a named ledger error before any
// statement runs. Re-applying the applied history is a no-op: the DDL is IF
// NOT EXISTS, so a rerun never truncates manifest or release state.
// Failures are returned, never absorbed: there is no fallback to another
// backend.
func MigrateSchema(ctx context.Context, src storepostgres.ConnSource, schema string) (storepostgres.MigrateResult, error) {
	if src == nil {
		return storepostgres.MigrateResult{}, errors.New("dataset postgres: nil migration connection source")
	}
	migrations, err := OwnerMigrations()
	if err != nil {
		return storepostgres.MigrateResult{}, err
	}
	runner, err := storepostgres.NewMigrationRunner(src, schema, migrations)
	if err != nil {
		return storepostgres.MigrateResult{}, err
	}
	return runner.Migrate(ctx)
}

// VerifySchema is the read-only counterpart of MigrateSchema for the
// dataset_release series: it reports the assigned schema's ledger state
// against the owner history (store.MigrationRunner.Verify) without taking
// the migration lock or writing anything. Failures are returned, never
// absorbed; there is no fallback to another backend.
func VerifySchema(ctx context.Context, src storepostgres.ConnSource, schema string) error {
	if src == nil {
		return errors.New("dataset postgres: nil migration connection source")
	}
	migrations, err := OwnerMigrations()
	if err != nil {
		return err
	}
	runner, err := storepostgres.NewMigrationRunner(src, schema, migrations)
	if err != nil {
		return err
	}
	return runner.Verify(ctx)
}
