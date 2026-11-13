package control

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

var (
	ErrManifestNotFound  = datasetstore.ErrManifestNotFound
	ErrInvalidSchemaName = datasetstore.ErrInvalidSchemaName
)

type (
	EdgeOccurrenceRow = pgstore.EdgeOccurrenceRow
	EdgeScope         = pgstore.EdgeScope
)

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

type (
	FieldProfileCoverage = pgstore.FieldProfileCoverage
	FieldProfileFilter   = pgstore.FieldProfileFilter
	InventorySourceGroup = pgstore.InventorySourceGroup
	InventoryCountScope  = pgstore.InventoryCountScope
)

type (
	FieldProfileStore = catalogstore.FieldProfileStore
	InventoryStore    = catalogstore.InventoryStore
	RelationshipStore = catalogstore.RelationshipStore
)

type (
	RelationshipScope           = catalogstore.RelationshipScope
	RelationshipOccurrenceGroup = catalogstore.RelationshipOccurrenceGroup
	SemanticReadRepository      = catalogstore.SemanticReadRepository
)

func NewSemanticReadRepository(src ConnSource, schema string) (*SemanticReadRepository, error) {
	return catalogstore.NewSemanticReadRepository(src, schema)
}

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

type (
	PublishStore              = explorerstore.PublishStore
	ActivationStore           = explorerstore.ActivationStore
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

func NewActivationStore(client *Client, explorerSchema, datasetSchema, publicationSchema string) (*ActivationStore, error) {
	return explorerstore.NewActivationStore(client, explorerSchema, datasetSchema, publicationSchema)
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
