package postgres

// Sentinel-identity contract for the readiness classifier (PR #148
// verifier finding F1).
//
// internal/server's postgreSQLCoreReadinessState classifies probe failures
// with errors.Is against the control sentinels below. The probe lives in
// control.Runtime.Readiness and emits through the six per-series
// VerifySchema wrappers, which all build a pgstore.MigrationRunner — so a
// control sentinel that resolves to a DIFFERENT errors.New than the one
// the probe emits makes its token unreachable: the offline classifier cell
// cannot catch it because a cell fabricates the error from the control
// sentinel itself. This guard pins, for every sentinel the classifier
// consumes, that the control identity and the probe-emitted identity match
// in both directions. (Before arch-backend-09 this also pinned the
// root-facade aliases; aliases.go is deleted, so the control plane is now
// the single identity source.)

import (
	"errors"
	"testing"

	"github.com/calypr/loom/internal/backends/postgres/control"
	datasetstore "github.com/calypr/loom/internal/backends/postgres/internal/dataset"
	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
)

func TestControlSentinelsMatchProbeEmittedIdentities(t *testing.T) {
	// each row: control symbol <-> the identity the production probe path
	// actually wraps. Both directions are identity checks for leaf
	// sentinels; a one-directional match would mean wrapping, not alias.
	for _, row := range []struct {
		name        string
		controlSide error
		probeSide   error
	}{
		{"control.ErrRuntimeClosed (control-native; Readiness->Health emits it)", control.ErrRuntimeClosed, control.ErrRuntimeClosed},
		{"control.ErrConnect (ConnectError.Unwrap carries it; Acquire/Ping/verify faults)", control.ErrConnect, pgstore.ErrConnect},
		{"control.ErrUnsupportedVersion (Ping's PostgreSQL-18 gate)", control.ErrUnsupportedVersion, pgstore.ErrUnsupportedVersion},
		{"control.ErrInvalidSchemaName (runner name + absent-schema arms; F1 regression: dataset's copy aliased the store identity)", control.ErrInvalidSchemaName, pgstore.ErrInvalidSchemaName},
		{"control.ErrInvalidSchemaName via the dataset config emitter", control.ErrInvalidSchemaName, datasetstore.ErrInvalidSchemaName},
		{"control.ErrMigrationPending", control.ErrMigrationPending, pgstore.ErrMigrationPending},
		{"control.ErrMigrationChecksumMismatch", control.ErrMigrationChecksumMismatch, pgstore.ErrMigrationChecksumMismatch},
		{"control.ErrFutureSchemaVersion", control.ErrFutureSchemaVersion, pgstore.ErrFutureSchemaVersion},
		{"control.ErrMigrationMissing", control.ErrMigrationMissing, pgstore.ErrMigrationMissing},
		{"control.ErrMigrationOutOfOrder", control.ErrMigrationOutOfOrder, pgstore.ErrMigrationOutOfOrder},
	} {
		row := row
		t.Run(row.name, func(t *testing.T) {
			if !errors.Is(row.probeSide, row.controlSide) {
				t.Fatalf("probe-emitted %v does not match the control sentinel: classifier token unreachable (F1 class)", row.name)
			}
			if !errors.Is(row.controlSide, row.probeSide) {
				t.Fatalf("control sentinel %v does not match the probe-emitted identity: alias drift", row.name)
			}
		})
	}
}
