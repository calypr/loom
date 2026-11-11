package execution_test

import (
	postgres "github.com/calypr/loom/internal/backends/postgres"
	"github.com/calypr/loom/internal/dataframe/execution"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The live root-page battery (recipe_paging_postgres_test.go) runs inside
// the internal execution test package so it can attach parent-shaped page
// steps. The production row adapter imports the execution package, so only
// this external test package may import it; the adapter is installed here
// once, and every key and row step of the battery then travels through the
// real internal/dataframe/postgres executor against the live server.
func init() {
	// The public factory seam assembles the private rows executor (the same
	// Querier contract the factory wires for SourceRows); the execution
	// harness never imports the private package directly.
	execution.Exec05InstallRowsAdapterFactory(func(iso *pgxpool.Pool) (execution.SourceRows, error) {
		return postgres.NewSourceRows(iso)
	})
}
