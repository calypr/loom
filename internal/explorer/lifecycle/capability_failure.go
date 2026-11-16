package lifecycle

import (
	"errors"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer/capability"
)

func capabilityResolutionFailure(stage, message string, err error) error {
	switch {
	case errors.Is(err, capability.ErrStaleSnapshot), errors.Is(err, capability.ErrSnapshotUnavailable), errors.Is(err, capability.ErrNotFound):
		return conflict(stage, "STALE_CATALOG_SNAPSHOT", message, nil, err)
	case errors.Is(err, authscope.ErrUnauthenticated), errors.Is(err, authscope.ErrForbidden):
		return forbidden(stage, message, err)
	default:
		return unavailable(stage, "CAPABILITY_UNAVAILABLE", message, err)
	}
}

func receiptCapabilityFailure(stage, message string, err error) error {
	switch {
	case errors.Is(err, capability.ErrStaleSnapshot), errors.Is(err, capability.ErrSnapshotUnavailable), errors.Is(err, capability.ErrNotFound):
		return conflict(stage, "RECEIPT_STALE", message, nil, err)
	case errors.Is(err, authscope.ErrUnauthenticated), errors.Is(err, authscope.ErrForbidden):
		return forbidden(stage, message, err)
	default:
		return unavailable(stage, "CAPABILITY_UNAVAILABLE", message, err)
	}
}
