package postgres

import (
	"context"
	"errors"
	"net"
	"strings"
)

func CommitOutcomeUnknown(err error) bool {
	if err == nil {
		return false
	}
	if pgErr, ok := pgErrorFrom(err); ok {
		return strings.HasPrefix(pgErr.Code, "08") || pgErr.Code == sqlStateAdminShutdown || pgErr.Code == sqlStateCrashShutdown
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
