package control

// Public control- and read-plane vocabulary.
//
// These re-exports complete what a caller needs to drive and verify a
// PostgreSQL generation through the factory boundary: batch conflict
// identities, the resource/edge repository constructors used for bounded
// fixture writes, the field-profile and inventory read/coverage types, and
// the catalog adapters' input contracts (so test doubles can name them).
// Same discipline as vocabulary.go: aliases of the implementation types,
// no new semantics, implementation stays under
// internal/backends/postgres/internal/.

import (
	catalogstore "github.com/calypr/loom/internal/backends/postgres/internal/catalog"
	chartifactstore "github.com/calypr/loom/internal/backends/postgres/internal/chartifact"
	datasetstore "github.com/calypr/loom/internal/backends/postgres/internal/dataset"
	explorerstore "github.com/calypr/loom/internal/backends/postgres/internal/explorer"
	publicationstore "github.com/calypr/loom/internal/backends/postgres/internal/publication"
	recipeexecstore "github.com/calypr/loom/internal/backends/postgres/internal/recipeexec"
	recipesstore "github.com/calypr/loom/internal/backends/postgres/internal/recipes"
	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"

	publicationcontract "github.com/calypr/loom/internal/dataset"
)

// Batch conflict identities: sentinel chains and the typed errors that
// carry them, so callers classify ledger outcomes with errors.As without
// importing the store package.
var (
	ErrBatchIdentityConflict = pgstore.ErrBatchIdentityConflict
	ErrBatchRowConflict      = pgstore.ErrBatchRowConflict
	ErrEdgeSourceMissing     = pgstore.ErrEdgeSourceMissing
	ErrInvalidDSN            = pgstore.ErrInvalidDSN
	ErrInvalidPoolConfig     = pgstore.ErrInvalidPoolConfig
)

type (
	BatchResourceConflictError = pgstore.BatchResourceConflictError
	BatchEdgeConflictError     = pgstore.BatchEdgeConflictError
	BatchIdentityConflictError = pgstore.BatchIdentityConflictError
	EdgeSourceMissingError     = pgstore.EdgeSourceMissingError
)

// Dataset manifest refusals carried by the lifecycle port.
var (
	ErrManifestNotFound  = datasetstore.ErrManifestNotFound
	ErrInvalidSchemaName = datasetstore.ErrInvalidSchemaName
)

// Edge write vocabulary (rows and scope) used by the ingest writer seam.
type (
	EdgeOccurrenceRow = pgstore.EdgeOccurrenceRow
	EdgeScope         = pgstore.EdgeScope
)

// Bounded repositories for direct resource/edge fixture writes over a
// ConnSource, keeping every store-side validation.
type (
	ResourceRepository = pgstore.ResourceRepository
	EdgeRepository     = pgstore.EdgeRepository
)

func NewResourceRepository(src ConnSource, schema string) (*ResourceRepository, error) {
	return pgstore.NewResourceRepository(src, schema)
}

func NewEdgeRepository(src ConnSource, schema string) (*EdgeRepository, error) {
	return pgstore.NewEdgeRepository(src, schema)
}

// Field-profile and inventory read/coverage vocabulary.
type (
	FieldProfileCoverage = pgstore.FieldProfileCoverage
	FieldProfileFilter   = pgstore.FieldProfileFilter
	InventorySourceGroup = pgstore.InventorySourceGroup
	InventoryCountScope  = pgstore.InventoryCountScope
)

// Catalog adapter input contracts: the store-shaped interfaces the catalog
// constructors accept, re-exported so consumer test doubles can name them.
type (
	FieldProfileStore = catalogstore.FieldProfileStore
	InventoryStore    = catalogstore.InventoryStore
	RelationshipStore = catalogstore.RelationshipStore
)

// Relationship read vocabulary and the semantic read repository used to
// verify contribution materialization.
type (
	RelationshipScope           = catalogstore.RelationshipScope
	RelationshipOccurrenceGroup = catalogstore.RelationshipOccurrenceGroup
	SemanticReadRepository      = catalogstore.SemanticReadRepository
)

func NewSemanticReadRepository(src ConnSource, schema string) (*SemanticReadRepository, error) {
	return catalogstore.NewSemanticReadRepository(src, schema)
}

// Store-side repository constructors for fixture and bootstrap wiring over
// a ConnSource, keeping every store-side validation.
type (
	FieldProfileRepository = pgstore.FieldProfileRepository
	InventoryRepository    = pgstore.InventoryRepository
)

func NewFieldProfileRepository(src ConnSource, schema string) (*FieldProfileRepository, error) {
	return pgstore.NewFieldProfileRepository(src, schema)
}

func NewInventoryRepository(src ConnSource, schema string) (*InventoryRepository, error) {
	return pgstore.NewInventoryRepository(src, schema)
}

// Catalog adapter constructors over store-shaped inputs, re-exported for
// the generation-load production recipe and test doubles.
type CatalogStore = catalogstore.Store

func NewCatalogStore(inventory InventoryStore) (*CatalogStore, error) {
	return catalogstore.New(inventory)
}

func NewCatalogFieldProfileStore(profiles FieldProfileStore) (*CatalogStore, error) {
	return catalogstore.NewFieldProfileStore(profiles)
}

func NewCatalogRelationshipEvidenceRepository(src ConnSource, schema string) (*catalogstore.RelationshipEvidenceRepository, error) {
	return catalogstore.NewRelationshipEvidenceRepository(src, schema)
}

func NewCatalogRelationshipEvidenceStore(relationships RelationshipStore) (*CatalogStore, error) {
	return catalogstore.NewRelationshipEvidenceStore(relationships)
}

type SemanticContributionRepository = catalogstore.SemanticContributionRepository

func NewSemanticContributionRepository(src ConnSource, schema string) (*SemanticContributionRepository, error) {
	return catalogstore.NewSemanticContributionRepository(src, schema)
}

// Explorer control-plane adapters over the runtime pool, re-exported for
// witness fixtures that establish selection and explicit-group state.
type (
	ExplorerStore      = explorerstore.Store
	ExplicitGroupStore = explorerstore.ExplicitGroupStore
)

func NewExplorerStore(client *Client, schema string) (*ExplorerStore, error) {
	return explorerstore.New(client, schema)
}

func NewExplicitGroupStore(client *Client, schema string) (*ExplicitGroupStore, error) {
	return explorerstore.NewExplicitGroupStore(client, schema)
}

// Data-plane adapters (pg-runtime-server-composition). The composed server
// consumes these through control.Runtime fields; the types and constructors
// are re-exported so witnesses and fixtures can name them and establish
// publication, recipe, interpretation, or Explorer state directly over a
// ConnSource without importing the private packages.
type (
	PublishStore              = explorerstore.PublishStore
	InterpretationStore       = explorerstore.InterpretationStore
	CapabilitySnapshotStore   = explorerstore.CapabilitySnapshotStore
	TableShapeCapabilityStore = explorerstore.TableShapeCapabilityStore
	DatasetReleaseStore       = datasetstore.ReleaseStore
	BundleCatalog             = publicationstore.BundleCatalog
	BundleRegistry            = publicationstore.Registry
	BundleReadPins            = publicationstore.ReadPins
	PublicationReconciler     = publicationstore.Reconciler
	PhysicalDropTable         = publicationstore.PhysicalDropTable
	RecipeExecRegistry        = recipeexecstore.Registry
	RecipeRevisionRegistry    = recipesstore.RevisionRegistry
	ChartifactCatalog         = chartifactstore.Catalog
)

func NewPublishStore(client *Client, explorerSchema, datasetSchema string) (*PublishStore, error) {
	return explorerstore.NewPublishStore(client, explorerSchema, datasetSchema)
}

func NewInterpretationStore(client *Client, schema string) (*InterpretationStore, error) {
	return explorerstore.NewInterpretationStore(client, schema)
}

func NewCapabilitySnapshotStore(client *Client, schema string) (*CapabilitySnapshotStore, error) {
	return explorerstore.NewCapabilitySnapshotStore(client, schema)
}

func NewTableShapeCapabilityStore(client *Client, schema string) (*TableShapeCapabilityStore, error) {
	return explorerstore.NewTableShapeCapabilityStore(client, schema)
}

func NewDatasetReleaseStore(src ConnSource, schema string) (*DatasetReleaseStore, error) {
	return datasetstore.NewReleaseStore(src, schema)
}

func NewBundleCatalog(client *Client, schema string) (*BundleCatalog, error) {
	return publicationstore.NewBundleCatalog(&leasedDB{client: client}, schema)
}

func NewPublicationReconciler(registry *BundleRegistry, pins *BundleReadPins, releases publicationcontract.ActiveReleaseResolver) (*PublicationReconciler, error) {
	return publicationstore.NewReconciler(registry, pins, releases)
}

func NewRecipeExecRegistry(client *Client, schema string) (*RecipeExecRegistry, error) {
	return recipeexecstore.New(&leasedDB{client: client}, schema)
}

func NewRecipeRevisionRegistry(client *Client, schema string) (*RecipeRevisionRegistry, error) {
	return recipesstore.New(&leasedDB{client: client}, schema)
}

func NewChartifactCatalog(client *Client, schema string) (*ChartifactCatalog, error) {
	return chartifactstore.NewCatalog(&leasedDB{client: client}, schema)
}
