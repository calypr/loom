// Catalog discovery reads over the source assignment
// (pg-runtime-first-query-route, Ask C). These are the control-plane ports
// the public fhirDataframe route consumes for field discovery and
// existing-auth-path listing. The read store is composed per call from
// the runtime's own pool and source-schema assignment: the constructors
// validate without I/O, the server-side discovery cache keeps repeat
// queries off the database, and the frozen Runtime struct gains no field.
//
// Posture rules:
//   - An unassigned source series is a typed configuration refusal, never
//     a zero-value answer: a consumer that needs catalog reads assigns
//     Schemas[source].
//   - There is no backend fallback anywhere on this path: every failure is
//     the store's own wrapped error, and no Arango surface is reachable
//     from this package.
package control

import (
	"context"
	"errors"
	"fmt"

	catalogstore "github.com/calypr/loom/internal/backends/postgres/internal/catalog"
	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/catalog"
)

// ErrSourceSeriesUnassigned reports a catalog read requested while the
// source series is unassigned. It is a configuration refusal the operator
// fixes by assigning the series, not a transient error: nothing was read
// and nothing was fabricated.
var ErrSourceSeriesUnassigned = errors.New("postgres backend: source series is unassigned; catalog reads are unavailable")

// DiscoverFields returns the populated-field view of one project/
// generation from persisted field-profile rows in the source assignment.
// It delegates to the catalog adapter's DiscoverFields (the Arango-
// mirrored read; see internal/backends/postgres/internal/catalog/
// field_discovery.go for the option semantics and registered divergences).
func (r *Runtime) DiscoverFields(ctx context.Context, opts catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
	store, err := r.catalogReadStore()
	if err != nil {
		return nil, err
	}
	return store.DiscoverFields(ctx, opts)
}

// DiscoverExistingAuthResourcePaths lists the distinct non-empty auth
// paths recorded for one project/generation in the source assignment,
// derived from the same persisted field-profile rows the Arango reference
// collects. AllGenerations is refused as typed unsupported on this lane
// (see the adapter's file comment); the F-2 conflict with a selected
// generation is the named conflict error.
func (r *Runtime) DiscoverExistingAuthResourcePaths(ctx context.Context, opts catalog.AuthResourcePathOptions) ([]string, error) {
	store, err := r.catalogReadStore()
	if err != nil {
		return nil, err
	}
	return store.DiscoverExistingAuthResourcePaths(ctx, opts)
}

// catalogReadStore composes the read-only catalog adapter over the
// runtime's own pool and the source assignment. The repository and store
// constructors validate their inputs without touching the database, so
// per-call composition is cheap; the caller-side discovery cache is what
// keeps steady-state discovery off the pool.
func (r *Runtime) catalogReadStore() (*catalogstore.Store, error) {
	if r.isClosed() {
		return nil, fmt.Errorf("%w: discover catalog fields", ErrRuntimeClosed)
	}
	if r.sourceSchema == "" {
		return nil, fmt.Errorf("%w: assign Schemas[%q] to read catalog discovery facts",
			ErrSourceSeriesUnassigned, SeriesSource)
	}
	repo, err := pgstore.NewFieldProfileRepository(r.client, r.sourceSchema)
	if err != nil {
		return nil, fmt.Errorf("postgres backend: compose field profile read repository: %w", err)
	}
	store, err := catalogstore.NewFieldProfileStore(repo)
	if err != nil {
		return nil, fmt.Errorf("postgres backend: compose field profile read store: %w", err)
	}
	return store, nil
}
