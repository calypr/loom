// Package capabilitystore contains persistence adapters for immutable
// Explorer capability snapshots.
package capabilitystore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/explorer/capability"
)

var (
	ErrNotFound        = errors.New("capability snapshot not found")
	ErrInvalidSnapshot = errors.New("invalid capability snapshot")
	ErrTokenCollision  = errors.New("capability snapshot token collision")
)

// Repository persists complete snapshots. Put is content-addressed and never
// changes an existing document. GetByIdentity returns a READY snapshot when
// one exists; otherwise it returns the best available diagnostic snapshot.
type Repository interface {
	GetByToken(context.Context, string) (*capability.Snapshot, error)
	GetByIdentity(context.Context, capability.SnapshotIdentity) (*capability.Snapshot, error)
	Put(context.Context, capability.Snapshot) (*capability.Snapshot, error)
}

// Store is retained as a short name for callers that use stores throughout
// the rest of Explorer.
type Store = Repository

func validate(snapshot capability.Snapshot) error {
	if snapshot.Token == "" {
		return fmt.Errorf("%w: token is required", ErrInvalidSnapshot)
	}
	if snapshot.Status == capability.StatusReady && !snapshot.Usable() {
		return fmt.Errorf("%w: READY snapshot must be complete and not truncated", ErrInvalidSnapshot)
	}
	want := snapshotToken(snapshot)
	if snapshot.Token != want {
		return fmt.Errorf("%w: token does not match canonical snapshot content", ErrInvalidSnapshot)
	}
	return nil
}

// ValidateSnapshot applies the repository invariants without requiring a
// caller to select a particular adapter.
func ValidateSnapshot(snapshot capability.Snapshot) error { return validate(snapshot) }

func snapshotToken(snapshot capability.Snapshot) string {
	sum := sha256.Sum256(snapshot.CanonicalPayload())
	return "sha256:" + hex.EncodeToString(sum[:])
}
