package lifecycle

import (
	"errors"

	"github.com/calypr/loom/internal/explorer/lifecycle/support"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer/capability"
)

func capabilityResolutionFailure(stage, message string, err error) error {
	return capabilityFailure(stage, "STALE_CATALOG_SNAPSHOT", message, err)
}

func capabilityFailure(stage, code, message string, err error) error {
	switch {
	case errors.Is(err, capability.ErrStaleSnapshot), errors.Is(err, capability.ErrSnapshotUnavailable), errors.Is(err, capability.ErrNotFound):
		return support.Conflict(stage, code, message, nil, err)
	case errors.Is(err, authscope.ErrUnauthenticated), errors.Is(err, authscope.ErrForbidden):
		return support.Forbidden(stage, message, err)
	default:
		return support.Unavailable(stage, "CAPABILITY_UNAVAILABLE", message, err)
	}
}

func receiptCapabilityFailure(stage, message string, err error) error {
	return capabilityFailure(stage, "RECEIPT_STALE", message, err)
}
