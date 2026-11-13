package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func OwnerMigrations() ([]storepostgres.Migration, error) {
	migrations, err := storepostgres.LoadMigrations(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("dataset postgres: load owner migrations: %w", err)
	}
	return migrations, nil
}

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
