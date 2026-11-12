package postgres

// Public re-export surface of the control subpackage.
//
// The control package holds the single definition of every
// configuration, vocabulary, sentinel, and constructor symbol; this file
// keeps the package-path contract that production consumers (the ingest
// orchestrator, the load CLI) and the backend's own contract tests were
// written against: one public entry,
// github.com/calypr/loom/internal/backends/postgres, whose data-plane
// types (Runtime, Open) live in open.go. Aliases, not copies: same types,
// same error identities, zero drift risk.
//
// arch-backend-09 deleted every forwarding alias with zero references
// anywhere in the repository (52 dead-leaf entries, aliased-import and
// same-package references included in the census); unreferenced
// forwarding APIs must stay deleted. The facade-collapse ruling decides
// whether the remaining referenced entries migrate to direct control
// imports at the consumers.

import (
	"github.com/calypr/loom/internal/backends/postgres/control"
)

type (
	BatchEdgeConflictError      = control.BatchEdgeConflictError
	BatchIdentityConflictError  = control.BatchIdentityConflictError
	BatchResourceConflictError  = control.BatchResourceConflictError
	Config                      = control.Config
	ConnSource                  = control.ConnSource
	ConnectError                = control.ConnectError
	EdgeBatch                   = control.EdgeBatch
	EdgeBatchResult             = control.EdgeBatchResult
	EdgeOccurrenceRow           = control.EdgeOccurrenceRow
	EdgeScope                   = control.EdgeScope
	EdgeSourceMissingError      = control.EdgeSourceMissingError
	FieldProfileCoverage        = control.FieldProfileCoverage
	FieldProfileFilter          = control.FieldProfileFilter
	FieldProfileScope           = control.FieldProfileScope
	FieldProfileStore           = control.FieldProfileStore
	InventoryCountScope         = control.InventoryCountScope
	InventorySourceGroup        = control.InventorySourceGroup
	InventoryStore              = control.InventoryStore
	RelationshipOccurrenceGroup = control.RelationshipOccurrenceGroup
	RelationshipScope           = control.RelationshipScope
	RelationshipStore           = control.RelationshipStore
	ResourceBatch               = control.ResourceBatch
	ResourceBatchResult         = control.ResourceBatchResult
	Rows                        = control.Rows
	Series                      = control.Series
	SeriesSchemas               = control.SeriesSchemas
	SetupPolicy                 = control.SetupPolicy
	TranslatedError             = control.TranslatedError
	Client                      = control.Client
	ClientConfig                = control.ClientConfig
	ExplorerStore               = control.ExplorerStore
	ExplicitGroupStore          = control.ExplicitGroupStore
	PublishStore                = control.PublishStore
	BundleCatalog               = control.BundleCatalog
)

const (
	MaxBatchResources          = control.MaxBatchResources
	MaxBatchEdges              = control.MaxBatchEdges
	MaxBatchIDLength           = control.MaxBatchIDLength
	MaxFieldProfileWriteBatch  = control.MaxFieldProfileWriteBatch
	MaxInventoryWriteBatch     = control.MaxInventoryWriteBatch
	SeriesSource               = control.SeriesSource
	SeriesExplorerControl      = control.SeriesExplorerControl
	SeriesPublicationControl   = control.SeriesPublicationControl
	SeriesDatasetRelease       = control.SeriesDatasetRelease
	SeriesRecipeChains         = control.SeriesRecipeChains
	SeriesRecipeExecRegistries = control.SeriesRecipeExecRegistries
	SetupMigrate               = control.SetupMigrate
	SetupNone                  = control.SetupNone
)

var (
	ErrInvalidConfig              = control.ErrInvalidConfig
	ErrSeriesAssignment           = control.ErrSeriesAssignment
	ErrBootstrap                  = control.ErrBootstrap
	ErrRuntimeClosed              = control.ErrRuntimeClosed
	ErrConnect                    = control.ErrConnect
	ErrUnsupportedVersion         = control.ErrUnsupportedVersion
	ErrUniqueConflict             = control.ErrUniqueConflict
	ErrCommitOutcomeUnknown       = control.ErrCommitOutcomeUnknown
	ErrBatchWrite                 = control.ErrBatchWrite
	ErrManifestAlreadyExists      = control.ErrManifestAlreadyExists
	ErrManifestTransitionConflict = control.ErrManifestTransitionConflict
	ErrActivationConflict         = control.ErrActivationConflict
	// Schema-state classification for the readiness posture
	// (pg-runtime-health-readiness): same values as the runner's sentinels.
	// ErrInvalidSchemaName is already aliased below.
	ErrMigrationPending                      = control.ErrMigrationPending
	ErrMigrationChecksumMismatch             = control.ErrMigrationChecksumMismatch
	ErrFutureSchemaVersion                   = control.ErrFutureSchemaVersion
	ErrMigrationMissing                      = control.ErrMigrationMissing
	ErrMigrationOutOfOrder                   = control.ErrMigrationOutOfOrder
	ConditionServerRejected                  = control.ConditionServerRejected
	TranslateError                           = control.TranslateError
	TranslateCommitError                     = control.TranslateCommitError
	AsUniqueConflict                         = control.AsUniqueConflict
	RetryableTransactionConflict             = control.RetryableTransactionConflict
	ErrBatchIdentityConflict                 = control.ErrBatchIdentityConflict
	ErrBatchRowConflict                      = control.ErrBatchRowConflict
	ErrEdgeSourceMissing                     = control.ErrEdgeSourceMissing
	ErrInvalidDSN                            = control.ErrInvalidDSN
	ErrInvalidPoolConfig                     = control.ErrInvalidPoolConfig
	ErrManifestNotFound                      = control.ErrManifestNotFound
	ErrInvalidSchemaName                     = control.ErrInvalidSchemaName
	AllSeries                                = control.AllSeries
	GenerationAdvisoryLockKey                = control.GenerationAdvisoryLockKey
	WithAdvisoryLock                         = control.WithAdvisoryLock
	NewCatalogRelationshipEvidenceRepository = control.NewCatalogRelationshipEvidenceRepository
	NewCatalogFieldProfileStore              = control.NewCatalogFieldProfileStore
	NewCatalogRelationshipEvidenceStore      = control.NewCatalogRelationshipEvidenceStore
	NewCatalogStore                          = control.NewCatalogStore
	NewEdgeRepository                        = control.NewEdgeRepository
	NewEdgeWriter                            = control.NewEdgeWriter
	NewFieldProfileRepository                = control.NewFieldProfileRepository
	NewInventoryRepository                   = control.NewInventoryRepository
	NewResourceRepository                    = control.NewResourceRepository
	NewResourceWriter                        = control.NewResourceWriter
	NewSemanticContributionRepository        = control.NewSemanticContributionRepository
	NewSemanticReadRepository                = control.NewSemanticReadRepository
	CommitOutcomeUnknown                     = control.CommitOutcomeUnknown
	OpenPool                                 = control.OpenPool
	MigrateSchema                            = control.MigrateSchema
	MigrateDatasetReleaseSchema              = control.MigrateDatasetReleaseSchema
	VerifySchema                             = control.VerifySchema
	VerifyExplorerControlSchema              = control.VerifyExplorerControlSchema
	VerifyPublicationControlSchema           = control.VerifyPublicationControlSchema
	VerifyDatasetReleaseSchema               = control.VerifyDatasetReleaseSchema
	VerifyRecipeChainsSchema                 = control.VerifyRecipeChainsSchema
	VerifyRecipeExecRegistriesSchema         = control.VerifyRecipeExecRegistriesSchema
)
