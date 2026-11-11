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

// ErrInvalidConfig marks a rejected factory configuration. The message
// never carries connection credentials.
var ErrInvalidConfig = fmt.Errorf("arango: invalid configuration")

// ErrBootstrap marks a startup step that could not complete against an
// opened client; every such failure closes the client before Open returns.
var ErrBootstrap = fmt.Errorf("arango: bootstrap failed")

// Config is the validated startup input for the ArangoDB backend. It names
// only the connection target; credentials, when required, come from the
// environment the store layer reads, so the struct never carries secrets.
type Config struct {
	URL      string
	Database string
}

// Runtime is the owned ArangoDB backend composition returned by Open: one
// lifecycle client plus the assembled adapter set the composition roots
// (server, ingest, acceptance, CLIs) consume through typed domain ports.
//
// Ownership contract: Open opens exactly one client and bootstraps the
// required collections before any adapter is assembled. Optional startup
// steps a deployment gates on its own configuration — the recipe registry
// and dataframe-registry bootstraps the server degrades independently, and
// the private artifact catalog only present with ClickHouse — stay with the
// composition root, which drives them through Client with the exported
// bootstrap specs. Every Open failure path closes the client; no
// half-initialized runtime escapes.
type Runtime struct {
	// Client is the single lifecycle client the backend owns. Consumers
	// drive the exported row, transaction, explain, profile, and preview
	// index surface through it; it is not a driver handle and carries no
	// ClickHouse authority.
	Client *store.Client
	// Catalog is the discovery/evidence store (fields, references, auth
	// resource paths, semantic coverage, relationship evidence).
	Catalog *catalogadapter.Store
	// Dataset is the dataset lifecycle/manifest/release store.
	Dataset *datasetadapter.Store
	// Explorer is the authoring store (drafts, repositories, selections).
	Explorer *exploreradapter.Store
	// ExplorerCapabilitySnapshots backs Explorer capability caching.
	ExplorerCapabilitySnapshots *exploreradapter.CapabilitySnapshotStore
	// RecipeRegistry holds persisted dataframe recipe bundles.
	RecipeRegistry *recipeadapter.Registry
	// RecipeRevisions holds recipe revision chains.
	RecipeRevisions *recipeadapter.RevisionRegistry
	// PublishedRegistry is the published-dataframe metadata registry.
	PublishedRegistry *publicationadapter.Registry
}

// Open validates the configuration, opens exactly one lifecycle client for
// the selected database, applies the required collection bootstrap policy
// (dataset lifecycle, then Explorer — both fatal), and assembles the
// adapter set. Failure at any step closes the client created so far.
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
	// assemble closes the owned client on the first failure so no partial
	// runtime leaks; assembly steps are pure struct wiring over the client.
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

// Close releases the owned client and is terminal. After it returns, the
// Runtime and its Client hold no reusable connection: every operation on
// the client fails with ErrClientClosed without reaching the server, and
// the owned HTTP transport's idle connections are released. Close is safe
// to call more than once; repeated calls are no-ops returning nil.
func (r *Runtime) Close(ctx context.Context) error {
	if r == nil || r.Client == nil {
		return nil
	}
	return r.Client.Close(ctx)
}
