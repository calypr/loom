package arango

import (
	"context"
	"fmt"

	catalogadapter "github.com/calypr/loom/internal/backends/arango/internal/catalog"
	datasetadapter "github.com/calypr/loom/internal/backends/arango/internal/dataset"
	exploreradapter "github.com/calypr/loom/internal/backends/arango/internal/explorer"
	publicationadapter "github.com/calypr/loom/internal/backends/arango/internal/publication"
	recipeadapter "github.com/calypr/loom/internal/backends/arango/internal/recipeexec"
	store "github.com/calypr/loom/internal/backends/arango/internal/store"
)

var ErrInvalidConfig = fmt.Errorf("arango: invalid configuration")

var ErrBootstrap = fmt.Errorf("arango: bootstrap failed")

type Config struct {
	URL      string
	Database string
}

type Runtime struct {
	Client                      *store.Client
	Catalog                     *catalogadapter.Store
	Dataset                     *datasetadapter.Store
	Explorer                    *exploreradapter.Store
	ExplorerCapabilitySnapshots *exploreradapter.CapabilitySnapshotStore
	RecipeRegistry              *recipeadapter.Registry
	RecipeRevisions             *recipeadapter.RevisionRegistry
	PublishedRegistry           *publicationadapter.Registry
}

func Open(ctx context.Context, cfg Config) (*Runtime, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("%w: url is required", ErrInvalidConfig)
	}
	if cfg.Database == "" {
		return nil, fmt.Errorf("%w: database is required", ErrInvalidConfig)
	}
	client, err := store.Open(ctx, cfg.URL, cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("open dataset lifecycle store: %w", err)
	}
	if err := client.Bootstrap(ctx, datasetadapter.BootstrapSpec()); err != nil {
		_ = client.Close(ctx)
		return nil, fmt.Errorf("%w: bootstrap dataset lifecycle store: %w", ErrBootstrap, err)
	}
	if err := client.Bootstrap(ctx, exploreradapter.BootstrapSpec()); err != nil {
		_ = client.Close(ctx)
		return nil, fmt.Errorf("%w: bootstrap Explorer store: %w", ErrBootstrap, err)
	}
	runtime := &Runtime{Client: client}
	if err := runtime.assemble(client); err != nil {
		_ = client.Close(ctx)
		return nil, err
	}
	return runtime, nil
}

func (r *Runtime) assemble(client *store.Client) error {
	recipeRegistry, err := recipeadapter.New(client)
	if err != nil {
		return fmt.Errorf("create recipe registry: %w", err)
	}
	r.RecipeRegistry = recipeRegistry
	datasetStore, err := datasetadapter.New(client)
	if err != nil {
		return fmt.Errorf("create dataset lifecycle store: %w", err)
	}
	r.Dataset = datasetStore
	catalogStore, err := catalogadapter.New(client)
	if err != nil {
		return fmt.Errorf("create catalog store: %w", err)
	}
	r.Catalog = catalogStore
	publishedRegistry, err := publicationadapter.New(client)
	if err != nil {
		return fmt.Errorf("create published dataframe registry: %w", err)
	}
	r.PublishedRegistry = publishedRegistry
	recipeRevisions, err := recipeadapter.NewRevisionRegistry(client)
	if err != nil {
		return fmt.Errorf("create recipe revision registry: %w", err)
	}
	r.RecipeRevisions = recipeRevisions
	explorerStore, err := exploreradapter.New(client)
	if err != nil {
		return fmt.Errorf("create Explorer store: %w", err)
	}
	r.Explorer = explorerStore
	capabilitySnapshots, err := exploreradapter.NewCapabilitySnapshotStore(client)
	if err != nil {
		return fmt.Errorf("create Explorer capability snapshot store: %w", err)
	}
	r.ExplorerCapabilitySnapshots = capabilitySnapshots
	return nil
}

func (r *Runtime) Close(ctx context.Context) error {
	if r == nil || r.Client == nil {
		return nil
	}
	return r.Client.Close(ctx)
}
