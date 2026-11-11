package postgres

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// CommitOutcomeUnknown reports whether a failed write may still have
// committed server-side, so the caller keeps the manifest in LOADING for a
// replay to converge instead of marking it FAILED. It owns the driver-side
// half of that classification (SQLSTATE classes and store sentinels) so no
// production consumer outside the backend needs to import pgconn:
//
//   - Class 08 (connection exception) and 57P01/57P02 (backend
//     shutdown/crash) leave the outcome unknown; every other
//     server-answered error means the transaction did not commit.
//   - ErrCommitOutcomeUnknown names the failed-COMMIT-with-unknown-effect
//     case even where the raw *pgconn.PgError is no longer Is/As-visible
//     through a translated wrapper.
//   - ErrConnect and ErrBatchWrite are store-level connectivity/
//     infrastructure classes with the same consequence.
//
// The caller-side half (safe-point cancellation, context cancellation, and
// net.Error) stays with the orchestrating consumer, which owns those
// domain-specific cases.
func CommitOutcomeUnknown(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return strings.HasPrefix(pgErr.Code, "08") || pgErr.Code == "57P01" || pgErr.Code == "57P02"
	}
	if errors.Is(err, ErrCommitOutcomeUnknown) {
		return true
	}
	if errors.Is(err, ErrConnect) || errors.Is(err, ErrBatchWrite) {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}
