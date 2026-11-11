package arango

// Public re-export surface of the private adapter tree.
//
// The adapters live under internal/backends/arango/internal and are
// unreachable to any importer outside this tree by Go internal rules; this
// file keeps the package-path contract the production consumers (server,
// ingest, acceptance, CLIs) and the backend's own contract tests were
// written against: one public entry,
// github.com/calypr/loom/internal/backends/arango. Aliases, not copies:
// same types, same error identities, zero drift risk — the same shape as
// internal/backends/postgres/aliases.go.

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
	// ConnectionOptions is the store-shaped spelling of the factory Config;
	// the same two fields, accepted by Open.
	ConnectionOptions = Config

	// Shared store vocabulary.
	Client                 = store.Client
	RowVisitor             = store.RowVisitor
	BootstrapSpec          = store.BootstrapSpec
	CollectionSpec         = store.CollectionSpec
	TransactionCollections = store.TransactionCollections
	TransactionFunc        = store.TransactionFunc
	PreviewCoveringIndex   = store.PreviewCoveringIndex

	// Explain vocabulary (preview scan evidence, explain control plane).
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

	// Profile vocabulary (query profiling witnesses and CLI reports).
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

	// Adapter concrete types (consumed through Runtime; named here for
	// narrow re-assembly in contract witnesses).
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
	// Store mechanisms exported at the boundary.
	WithQueryMemoryLimit        = store.WithQueryMemoryLimit
	QueryMemoryLimitFromContext = store.QueryMemoryLimitFromContext
	DiagnosticCause             = store.DiagnosticCause
	TranslateQueryError         = store.TranslateQueryError
	// DatabaseExists is the no-provision existence probe the CLI routes use
	// to refuse a missing database before Open would create one.
	DatabaseExists = store.DatabaseExists
	// Vendor-response classifiers for the server's authz taxonomy: which
	// errors are ArangoDB responses, and which of those are the store's own
	// permission decision. Classification stays adapter-owned.
	IsArangoErrorResponse      = store.IsArangoErrorResponse
	IsArangoPermissionResponse = store.IsArangoPermissionResponse
	AssessExplainResult        = store.AssessExplainResult
	SummarizeProfile           = store.SummarizeProfile

	// Adapter constructors for witnesses that need a second instance over
	// the same owned client (CAS conflicts, dual writers). Production
	// composition uses the Runtime ports from Open instead.
	NewCatalogStore                   = catalogadapter.New
	NewDatasetStore                   = datasetadapter.New
	NewExplorerStore                  = exploreradapter.New
	NewCapabilitySnapshotStore        = exploreradapter.NewCapabilitySnapshotStore
	NewTableShapeCapabilityRepository = exploreradapter.NewTableShapeCapabilityRepository
	NewRecipeRegistry                 = recipeadapter.New
	NewRevisionRegistry               = recipeadapter.NewRevisionRegistry
	NewPublishedRegistry              = publicationadapter.New
	NewArtifactCatalog                = chartifactadapter.NewCatalog

	// Bootstrap specs for composition-gated (degradable or conditional)
	// startup steps; the required policy (dataset lifecycle, Explorer)
	// already ran inside Open and is idempotent if re-applied.
	DatasetBootstrapSpec   = datasetadapter.BootstrapSpec
	ExplorerBootstrapSpec  = exploreradapter.BootstrapSpec
	RecipeBootstrapSpec    = recipeadapter.BootstrapSpec
	RevisionBootstrapSpec  = recipeadapter.RevisionBootstrapSpec
	PublishedBootstrapSpec = publicationadapter.BootstrapSpec
	ArtifactBootstrapSpec  = chartifactadapter.BootstrapSpec

	// Dataset helpers and sentinels.
	DatasetActiveDocumentKey      = datasetadapter.ActiveDocumentKey
	DatasetCollectionSpecs        = datasetadapter.CollectionSpecs
	DatasetCompareAndSwapActivate = datasetadapter.CompareAndSwapActivateRelease
	DatasetCompareAndSwapPublish  = datasetadapter.CompareAndSwapPublishRelease
	ErrManifestAlreadyExists      = datasetadapter.ErrManifestAlreadyExists

	// Catalog sentinels.
	ErrAllGenerationsWithGeneration = catalogadapter.ErrAllGenerationsWithGeneration
	ErrSemanticInventoryClaimed     = catalogadapter.ErrSemanticInventoryClaimed
	ErrSemanticInventoryClaimLost   = catalogadapter.ErrSemanticInventoryClaimLost
)

const (
	// Collection names for test-owned AQL cleanup and index attestation.
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
