package postgres

import (
	"errors"
	"testing"

	"github.com/calypr/loom/internal/backends/postgres/control"
	datasetstore "github.com/calypr/loom/internal/backends/postgres/internal/dataset"
	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
)

func TestControlSentinelsMatchProbeEmittedIdentities(t *testing.T) {
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
