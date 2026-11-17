package control

import (
	"context"
	"errors"
	"fmt"

	catalogstore "github.com/calypr/loom/internal/backends/postgres/internal/catalog"
	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/catalog"
)

var ErrSourceSeriesUnassigned = errors.New("postgres backend: source series is unassigned; catalog reads are unavailable")

func (r *Runtime) DiscoverFields(ctx context.Context, opts catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
	store, err := r.catalogReadStore()
	if err != nil {
		return nil, err
	}
	return store.DiscoverFields(ctx, opts)
}

func (r *Runtime) DiscoverExistingAuthResourcePaths(ctx context.Context, opts catalog.AuthResourcePathOptions) ([]string, error) {
	store, err := r.catalogReadStore()
	if err != nil {
		return nil, err
	}
	return store.DiscoverExistingAuthResourcePaths(ctx, opts)
}

func (r *Runtime) catalogReadStore() (*catalogstore.FieldProfileAdapter, error) {
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
	store, err := catalogstore.NewFieldProfileAdapter(repo)
	if err != nil {
		return nil, fmt.Errorf("postgres backend: compose field profile read store: %w", err)
	}
	return store, nil
}
