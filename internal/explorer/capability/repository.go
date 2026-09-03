package capability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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
	GetByToken(context.Context, string) (*Snapshot, error)
	GetByIdentity(context.Context, SnapshotIdentity) (*Snapshot, error)
	Put(context.Context, Snapshot) (*Snapshot, error)
}

func validateSnapshot(snapshot Snapshot) error {
	if snapshot.Token == "" {
		return fmt.Errorf("%w: token is required", ErrInvalidSnapshot)
	}
	if snapshot.Status == StatusReady && !snapshot.Usable() {
		return fmt.Errorf("%w: READY snapshot must be complete and not truncated", ErrInvalidSnapshot)
	}
	want := snapshotToken(snapshot)
	if snapshot.Token != want {
		return fmt.Errorf("%w: token does not match canonical snapshot content", ErrInvalidSnapshot)
	}
	return nil
}

// ValidateSnapshot applies repository invariants without requiring a caller
// to select a particular persistence adapter.
func ValidateSnapshot(snapshot Snapshot) error { return validateSnapshot(snapshot) }

func snapshotToken(snapshot Snapshot) string {
	sum := sha256.Sum256(snapshot.CanonicalPayload())
	return "sha256:" + hex.EncodeToString(sum[:])
}
