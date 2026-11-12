package postgres

// Sentinel-identity contract for the readiness classifier (PR #148
// verifier finding F1).
//
// internal/server's postgreSQLCoreReadinessState classifies probe failures
// with errors.Is against the facade sentinels below. The probe lives in
// control.Runtime.Readiness and emits through the six per-series
// VerifySchema wrappers, which all build a pgstore.MigrationRunner — so a
// facade alias that resolves to a DIFFERENT errors.New than the one the
// probe emits makes its token unreachable: the offline classifier cell
// cannot catch it because a cell fabricates the error from the facade
// sentinel itself. This guard pins, for every sentinel the classifier
// consumes, that the facade identity and the probe-emitted identity match
// in both directions — the aliases.go header's "Aliases, not copies"
// promise, now enforced.

import (
	"errors"
	"testing"

	"github.com/calypr/loom/internal/backends/postgres/control"
	datasetstore "github.com/calypr/loom/internal/backends/postgres/internal/dataset"
	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
)

func TestFacadeSentinelsMatchProbeEmittedIdentities(t *testing.T) {
	// each row: facade symbol <-> the identity the production probe path
	// actually wraps. Both directions are identity checks for leaf
	// sentinels; a one-directional match would mean wrapping, not alias.
	for _, row := range []struct {
		name      string
		facade    error
		probeSide error
	}{
		{"ErrRuntimeClosed (control-native; Readiness->Health emits it)", ErrRuntimeClosed, control.ErrRuntimeClosed},
		{"ErrConnect (ConnectError.Unwrap carries it; Acquire/Ping/verify faults)", ErrConnect, pgstore.ErrConnect},
		{"ErrUnsupportedVersion (Ping's PostgreSQL-18 gate)", ErrUnsupportedVersion, pgstore.ErrUnsupportedVersion},
		{"ErrInvalidSchemaName (runner name + absent-schema arms; F1 regression: dataset's copy aliased the store identity)", ErrInvalidSchemaName, pgstore.ErrInvalidSchemaName},
		{"ErrInvalidSchemaName via the dataset config emitter", ErrInvalidSchemaName, datasetstore.ErrInvalidSchemaName},
		{"ErrMigrationPending", ErrMigrationPending, pgstore.ErrMigrationPending},
		{"ErrMigrationChecksumMismatch", ErrMigrationChecksumMismatch, pgstore.ErrMigrationChecksumMismatch},
		{"ErrFutureSchemaVersion", ErrFutureSchemaVersion, pgstore.ErrFutureSchemaVersion},
		{"ErrMigrationMissing", ErrMigrationMissing, pgstore.ErrMigrationMissing},
		{"ErrMigrationOutOfOrder", ErrMigrationOutOfOrder, pgstore.ErrMigrationOutOfOrder},
	} {
		row := row
		t.Run(row.name, func(t *testing.T) {
			if !errors.Is(row.probeSide, row.facade) {
				t.Fatalf("probe-emitted %v does not match the facade sentinel: classifier token unreachable (F1 class)", row.name)
			}
			if !errors.Is(row.facade, row.probeSide) {
				t.Fatalf("facade sentinel %v does not match the probe-emitted identity: alias drift", row.name)
			}
		})
	}
}
