package control

import (
	"errors"

	datasetstore "github.com/calypr/loom/internal/backends/postgres/internal/dataset"
	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
)

var (
	ErrInvalidConfig    = errors.New("postgres backend: invalid config")
	ErrSeriesAssignment = errors.New("postgres backend: invalid series assignment")
	ErrBootstrap        = errors.New("postgres backend: bootstrap failed")
	ErrRuntimeClosed    = errors.New("postgres backend: runtime closed")
)

var (
	ErrConnect              = pgstore.ErrConnect
	ErrUnsupportedVersion   = pgstore.ErrUnsupportedVersion
	ErrUniqueConflict       = pgstore.ErrUniqueConflict
	ErrCommitOutcomeUnknown = pgstore.ErrCommitOutcomeUnknown
	ErrBatchWrite           = pgstore.ErrBatchWrite

	ErrManifestAlreadyExists      = datasetstore.ErrManifestAlreadyExists
	ErrManifestTransitionConflict = datasetstore.ErrManifestTransitionConflict
	ErrActivationConflict         = datasetstore.ErrActivationConflict

	ErrMigrationPending          = pgstore.ErrMigrationPending
	ErrMigrationChecksumMismatch = pgstore.ErrMigrationChecksumMismatch
	ErrFutureSchemaVersion       = pgstore.ErrFutureSchemaVersion
	ErrMigrationMissing          = pgstore.ErrMigrationMissing
	ErrMigrationOutOfOrder       = pgstore.ErrMigrationOutOfOrder
)

type (
	DriverCondition     = pgstore.DriverCondition
	TranslatedError     = pgstore.TranslatedError
	ConnectError        = pgstore.ConnectError
	UniqueConflictError = pgstore.UniqueConflictError
)

var (
	ConditionAbortedTransaction   = pgstore.ConditionAbortedTransaction
	ConditionAdminShutdown        = pgstore.ConditionAdminShutdown
	ConditionCannotConnectNow     = pgstore.ConditionCannotConnectNow
	ConditionCommitRejected       = pgstore.ConditionCommitRejected
	ConditionConnectionFailure    = pgstore.ConditionConnectionFailure
	ConditionConnectionLost       = pgstore.ConditionConnectionLost
	ConditionCrashShutdown        = pgstore.ConditionCrashShutdown
	ConditionDeadlockDetected     = pgstore.ConditionDeadlockDetected
	ConditionQueryCanceled        = pgstore.ConditionQueryCanceled
	ConditionRequestCanceled      = pgstore.ConditionRequestCanceled
	ConditionSerializationFailure = pgstore.ConditionSerializationFailure
	ConditionServerRejected       = pgstore.ConditionServerRejected
	ConditionSessionTerminated    = pgstore.ConditionSessionTerminated
	ConditionTransactionClosed    = pgstore.ConditionTransactionClosed
	ConditionUniqueViolation      = pgstore.ConditionUniqueViolation
	TranslateError                = pgstore.TranslateError
	TranslateCommitError          = pgstore.TranslateCommitError
	AsUniqueConflict              = pgstore.AsUniqueConflict
	RetryableTransactionConflict  = pgstore.RetryableTransactionConflict
)
