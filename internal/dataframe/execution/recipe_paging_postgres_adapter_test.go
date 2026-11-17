package execution_test

import (
	postgres "github.com/calypr/loom/internal/backends/postgres"
	"github.com/calypr/loom/internal/dataframe/execution"
	"github.com/jackc/pgx/v5/pgxpool"
)

func init() {
	execution.Exec05InstallRowsAdapterFactory(func(iso *pgxpool.Pool) (execution.SourceRows, error) {
		return postgres.NewSourceRows(iso)
	})
}
