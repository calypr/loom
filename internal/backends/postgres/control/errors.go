package control

import (
	"errors"

	datasetstore "github.com/calypr/loom/internal/backends/postgres/internal/dataset"
	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
)

// Factory-level classification sentinels. Adapter/domain errors keep their
// own classes; the factory wraps Open-stage failures so callers can classify
// startup outcomes without importing private adapter packages.
var (
	// ErrInvalidConfig reports factory settings rejected before any network
	// connection: pool bounds, connection-tag policy, or setup policy.
	ErrInvalidConfig = errors.New("postgres backend: invalid config")
	// ErrSeriesAssignment reports a rejected schema-ownership assignment
	// (unknown series, runner-refused schema name, or two series whose
	// pinned ledgers disagree at a shared version). It wraps the store's
	// authoritative ValidateAssignment verdict.
	ErrSeriesAssignment = errors.New("postgres backend: invalid series assignment")
	// ErrBootstrap reports a readiness or schema setup failure after the
	// pool opened; Open unwinds every resource it created before returning.
	ErrBootstrap = errors.New("postgres backend: bootstrap failed")
	// ErrRuntimeClosed reports use of a Runtime whose Close has completed.
	// Closing is safe to repeat; use after close is a refusal, not a panic.
	ErrRuntimeClosed = errors.New("postgres backend: runtime closed")
)

// Re-exported store class sentinels (divergence register V3). These are
// classification contracts, not forwarding constructors: they keep the
// already-published error taxonomy (pool readiness, connect, version, unique
// conflict, commit-outcome-unknown) reachable through errors.Is for
// consumers that must not import the store implementation directly.
var (
	ErrConnect              = pgstore.ErrConnect
	ErrUnsupportedVersion   = pgstore.ErrUnsupportedVersion
	ErrUniqueConflict       = pgstore.ErrUniqueConflict
	ErrCommitOutcomeUnknown = pgstore.ErrCommitOutcomeUnknown
	ErrBatchWrite           = pgstore.ErrBatchWrite

	// Dataset generation manifest CAS refusals, reachable through the
	// factory boundary for the generation-load orchestrator and the load
	// CLI (arch-backend-06).
	ErrManifestAlreadyExists      = datasetstore.ErrManifestAlreadyExists
	ErrManifestTransitionConflict = datasetstore.ErrManifestTransitionConflict
	ErrActivationConflict         = datasetstore.ErrActivationConflict
)

// Error-translation taxonomy (arch-backend-06): the api/http and server
// error-boundary contracts classify translated driver errors without
// importing the store implementation. Same values, same identities.
type (
	DriverCondition = pgstore.DriverCondition
	TranslatedError = pgstore.TranslatedError
	ConnectError    = pgstore.ConnectError
	// UniqueConflictError carries the constraint identity of a domain
	// unique violation behind ErrUniqueConflict.
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
	// TranslateError maps a driver error to its sanitized transport class;
	// TranslateCommitError is the COMMIT-specific variant; AsUniqueConflict
	// extracts the domain conflict identity; RetryableTransactionConflict
	// reports serialization-class retryability. None of them retry or hide.
	TranslateError               = pgstore.TranslateError
	TranslateCommitError         = pgstore.TranslateCommitError
	AsUniqueConflict             = pgstore.AsUniqueConflict
	RetryableTransactionConflict = pgstore.RetryableTransactionConflict
)
