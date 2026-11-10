package execution_test

import (
	"github.com/calypr/loom/internal/dataframe/execution"
	dataframepostgres "github.com/calypr/loom/internal/dataframe/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The live root-page battery (recipe_paging_postgres_test.go) runs inside
// the internal execution test package so it can attach parent-shaped page
// steps. The production row adapter imports the execution package, so only
// this external test package may import it; the adapter is installed here
// once, and every key and row step of the battery then travels through the
// real internal/dataframe/postgres executor against the live server.
func init() {
	execution.Exec05InstallRowsAdapterFactory(func(iso *pgxpool.Pool) (execution.SourceRows, error) {
		executor, err := dataframepostgres.NewExecutor(iso)
		if err != nil {
			return nil, err
		}
		return executor.SourceRows, nil
	})
}
