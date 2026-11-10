package postgres

import (
	"errors"

	pgstore "github.com/calypr/loom/internal/store/postgres"
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
)
