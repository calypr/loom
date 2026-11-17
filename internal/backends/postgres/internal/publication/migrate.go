package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"

	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func OwnerMigrations() ([]pgstore.Migration, error) {
	migrations, err := pgstore.LoadMigrations(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("publication postgres: load owner migrations: %w", err)
	}
	return migrations, nil
}

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

func VerifySchema(ctx context.Context, src pgstore.ConnSource, schema string) error {
	if src == nil {
		return errors.New("publication postgres: nil migration connection source")
	}
	migrations, err := OwnerMigrations()
	if err != nil {
		return err
	}
	runner, err := pgstore.NewMigrationRunner(src, schema, migrations)
	if err != nil {
		return err
	}
	return runner.Verify(ctx)
}
