package arango

import (
	catalogadapter "github.com/calypr/loom/internal/backends/arango/internal/catalog"
	chartifactadapter "github.com/calypr/loom/internal/backends/arango/internal/chartifact"
	datasetadapter "github.com/calypr/loom/internal/backends/arango/internal/dataset"
	exploreradapter "github.com/calypr/loom/internal/backends/arango/internal/explorer"
	publicationadapter "github.com/calypr/loom/internal/backends/arango/internal/publication"
	recipeadapter "github.com/calypr/loom/internal/backends/arango/internal/recipeexec"
	store "github.com/calypr/loom/internal/backends/arango/internal/store"
)

type (
	ConnectionOptions = Config

	Client                 = store.Client
	RowVisitor             = store.RowVisitor
	BootstrapSpec          = store.BootstrapSpec
	CollectionSpec         = store.CollectionSpec
	TransactionCollections = store.TransactionCollections
	TransactionFunc        = store.TransactionFunc
	PreviewCoveringIndex   = store.PreviewCoveringIndex

	ExplainRequest        = store.ExplainRequest
	ExplainOptions        = store.ExplainOptions
	ExplainResult         = store.ExplainResult
	ExplainPlan           = store.ExplainPlan
	ExplainNode           = store.ExplainNode
	ExplainIndexes        = store.ExplainIndexes
	ExplainIndex          = store.ExplainIndex
	ExplainCollection     = store.ExplainCollection
	ExplainWarning        = store.ExplainWarning
	ExplainStats          = store.ExplainStats
	ExplainIndexUse       = store.ExplainIndexUse
	ExplainAssessment     = store.ExplainAssessment
	ExplainPlanEstimate   = store.ExplainPlanEstimate
	ExplainCollectionScan = store.ExplainCollectionScan
	ExplainIndexSummary   = store.ExplainIndexSummary
	ExplainIndexLocation  = store.ExplainIndexLocation

	ProfileRequest     = store.ProfileRequest
	ProfileOptions     = store.ProfileOptions
	ProfileResult      = store.ProfileResult
	ProfileExtra       = store.ProfileExtra
	ProfileStats       = store.ProfileStats
	ProfileNode        = store.ProfileNode
	ProfilePhases      = store.ProfilePhases
	ProfileNodeSummary = store.ProfileNodeSummary
	ProfileSummary     = store.ProfileSummary
	ProfileNodeGroup   = store.ProfileNodeGroup

	CatalogStore                           = catalogadapter.Store
	DatasetStore                           = datasetadapter.Store
	ExplorerStore                          = exploreradapter.Store
	ExplorerCapabilitySnapshotStore        = exploreradapter.CapabilitySnapshotStore
	ExplorerTableShapeCapabilityRepository = exploreradapter.TableShapeCapabilityRepository
	RecipeRegistry                         = recipeadapter.Registry
	RecipeRevisionRegistry                 = recipeadapter.RevisionRegistry
	PublishedRegistry                      = publicationadapter.Registry
	ArtifactCatalog                        = chartifactadapter.Catalog
)

var (
	WithQueryMemoryLimit        = store.WithQueryMemoryLimit
	QueryMemoryLimitFromContext = store.QueryMemoryLimitFromContext
	DiagnosticCause             = store.DiagnosticCause
	TranslateQueryError         = store.TranslateQueryError
	DatabaseExists              = store.DatabaseExists
	ErrClientClosed             = store.ErrClientClosed
	IsArangoErrorResponse       = store.IsArangoErrorResponse
	IsArangoPermissionResponse  = store.IsArangoPermissionResponse
	AssessExplainResult         = store.AssessExplainResult
	SummarizeProfile            = store.SummarizeProfile

	NewCatalogStore                   = catalogadapter.New
	NewDatasetStore                   = datasetadapter.New
	NewExplorerStore                  = exploreradapter.New
	NewCapabilitySnapshotStore        = exploreradapter.NewCapabilitySnapshotStore
	NewTableShapeCapabilityRepository = exploreradapter.NewTableShapeCapabilityRepository
	NewRecipeRegistry                 = recipeadapter.New
	NewRevisionRegistry               = recipeadapter.NewRevisionRegistry
	NewPublishedRegistry              = publicationadapter.New
	NewArtifactCatalog                = chartifactadapter.NewCatalog

	DatasetBootstrapSpec   = datasetadapter.BootstrapSpec
	ExplorerBootstrapSpec  = exploreradapter.BootstrapSpec
	RecipeBootstrapSpec    = recipeadapter.BootstrapSpec
	RevisionBootstrapSpec  = recipeadapter.RevisionBootstrapSpec
	PublishedBootstrapSpec = publicationadapter.BootstrapSpec
	ArtifactBootstrapSpec  = chartifactadapter.BootstrapSpec

	DatasetActiveDocumentKey      = datasetadapter.ActiveDocumentKey
	DatasetCollectionSpecs        = datasetadapter.CollectionSpecs
	DatasetCompareAndSwapActivate = datasetadapter.CompareAndSwapActivateRelease
	DatasetCompareAndSwapPublish  = datasetadapter.CompareAndSwapPublishRelease
	ErrManifestAlreadyExists      = datasetadapter.ErrManifestAlreadyExists

	ErrAllGenerationsWithGeneration = catalogadapter.ErrAllGenerationsWithGeneration
	ErrSemanticInventoryClaimed     = catalogadapter.ErrSemanticInventoryClaimed
	ErrSemanticInventoryClaimLost   = catalogadapter.ErrSemanticInventoryClaimLost
)

const (
	DatasetLifecycleCollection        = datasetadapter.LifecycleCollection
	CatalogSemanticCoverageCollection = catalogadapter.SemanticCoverageCollection
	ExplorersCollection               = exploreradapter.ExplorersCollection
	DraftRevisionsCollection          = exploreradapter.DraftRevisionsCollection
	RevisionsCollection               = exploreradapter.RevisionsCollection
	CompilationReceiptsCollection     = exploreradapter.CompilationReceiptsCollection
	SelectionsCollection              = exploreradapter.SelectionsCollection
	SelectionMembersCollection        = exploreradapter.SelectionMembersCollection
	TableShapeCapabilitiesCollection  = exploreradapter.TableShapeCapabilitiesCollection
	CapabilitySnapshotCollection      = exploreradapter.CapabilitySnapshotCollection
	RecipeCollection                  = recipeadapter.Collection
	RecipeRevisionCollection          = recipeadapter.RevisionCollection
	BundleExecutionsCollection        = publicationadapter.BundleExecutionsCollection
	BundlePointersCollection          = publicationadapter.BundlePointersCollection
	BundleLeasesCollection            = publicationadapter.BundleLeasesCollection
	ExecutionReadPinsCollection       = publicationadapter.ExecutionReadPinsCollection
	PrivateArtifactsCollection        = chartifactadapter.PrivateArtifactsCollection
)
