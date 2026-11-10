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

import (
	"github.com/calypr/loom/internal/backends/postgres/control"
)

type (
	BatchEdgeConflictError         = control.BatchEdgeConflictError
	BatchIdentityConflictError     = control.BatchIdentityConflictError
	BatchResourceConflictError     = control.BatchResourceConflictError
	CatalogStore                   = control.CatalogStore
	Config                         = control.Config
	ConnSource                     = control.ConnSource
	ConnectError                   = control.ConnectError
	DriverCondition                = control.DriverCondition
	EdgeBatch                      = control.EdgeBatch
	EdgeBatchResult                = control.EdgeBatchResult
	EdgeOccurrenceRow              = control.EdgeOccurrenceRow
	EdgeRepository                 = control.EdgeRepository
	EdgeScope                      = control.EdgeScope
	EdgeSourceMissingError         = control.EdgeSourceMissingError
	EdgeWriter                     = control.EdgeWriter
	FieldProfileCoverage           = control.FieldProfileCoverage
	FieldProfileFilter             = control.FieldProfileFilter
	FieldProfileRepository         = control.FieldProfileRepository
	FieldProfileScope              = control.FieldProfileScope
	FieldProfileStore              = control.FieldProfileStore
	InventoryCountScope            = control.InventoryCountScope
	InventoryRepository            = control.InventoryRepository
	InventorySourceGroup           = control.InventorySourceGroup
	InventoryStore                 = control.InventoryStore
	MigrateResult                  = control.MigrateResult
	RelationshipOccurrenceGroup    = control.RelationshipOccurrenceGroup
	RelationshipScope              = control.RelationshipScope
	RelationshipStore              = control.RelationshipStore
	ResourceBatch                  = control.ResourceBatch
	ResourceBatchResult            = control.ResourceBatchResult
	ResourceRepository             = control.ResourceRepository
	ResourceWriter                 = control.ResourceWriter
	Row                            = control.Row
	Rows                           = control.Rows
	SemanticContributionRepository = control.SemanticContributionRepository
	SemanticReadRepository         = control.SemanticReadRepository
	Series                         = control.Series
	SeriesSchemas                  = control.SeriesSchemas
	SetupPolicy                    = control.SetupPolicy
	TranslatedError                = control.TranslatedError
	UniqueConflictError            = control.UniqueConflictError
	Client                         = control.Client
	ClientConfig                   = control.ClientConfig
	ExplorerStore                  = control.ExplorerStore
	ExplicitGroupStore             = control.ExplicitGroupStore
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
	ApplicationNamePrefix      = control.ApplicationNamePrefix
)

var (
	ErrInvalidConfig                         = control.ErrInvalidConfig
	ErrSeriesAssignment                      = control.ErrSeriesAssignment
	ErrBootstrap                             = control.ErrBootstrap
	ErrRuntimeClosed                         = control.ErrRuntimeClosed
	ErrConnect                               = control.ErrConnect
	ErrUnsupportedVersion                    = control.ErrUnsupportedVersion
	ErrUniqueConflict                        = control.ErrUniqueConflict
	ErrCommitOutcomeUnknown                  = control.ErrCommitOutcomeUnknown
	ErrBatchWrite                            = control.ErrBatchWrite
	ErrManifestAlreadyExists                 = control.ErrManifestAlreadyExists
	ErrManifestTransitionConflict            = control.ErrManifestTransitionConflict
	ErrActivationConflict                    = control.ErrActivationConflict
	ConditionAbortedTransaction              = control.ConditionAbortedTransaction
	ConditionAdminShutdown                   = control.ConditionAdminShutdown
	ConditionCannotConnectNow                = control.ConditionCannotConnectNow
	ConditionCommitRejected                  = control.ConditionCommitRejected
	ConditionConnectionFailure               = control.ConditionConnectionFailure
	ConditionConnectionLost                  = control.ConditionConnectionLost
	ConditionCrashShutdown                   = control.ConditionCrashShutdown
	ConditionDeadlockDetected                = control.ConditionDeadlockDetected
	ConditionQueryCanceled                   = control.ConditionQueryCanceled
	ConditionRequestCanceled                 = control.ConditionRequestCanceled
	ConditionSerializationFailure            = control.ConditionSerializationFailure
	ConditionServerRejected                  = control.ConditionServerRejected
	ConditionSessionTerminated               = control.ConditionSessionTerminated
	ConditionTransactionClosed               = control.ConditionTransactionClosed
	ConditionUniqueViolation                 = control.ConditionUniqueViolation
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
	OwnerMigrations                          = control.OwnerMigrations
	MigrateSchema                            = control.MigrateSchema
	NewExplorerStore                         = control.NewExplorerStore
	NewExplicitGroupStore                    = control.NewExplicitGroupStore
)
