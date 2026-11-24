package capabilitydb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/calypr/loom/internal/explorer/capability"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/testkit/arangotest"
)

func TestCapabilitySnapshotIdentitySeparatesShapeAndSkipsUnusableRowsAgainstArango(t *testing.T) {
	database := arangotest.Owned(t, "loom_verify_capidentity_")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := database.Client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{CapabilitySnapshotCollectionSpec()}}); err != nil {
		t.Fatal(err)
	}
	snapshots, err := NewCapabilitySnapshotStore(database.Client)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"", "observed-availability-v2"} {
		t.Run("cache-version="+version, func(t *testing.T) {
			identity := readyCapabilitySnapshot().Identity
			identity.CacheVersion = version
			makeSnapshot := func(shape string, status capability.Status, complete, truncated bool) capability.Snapshot {
				id := identity
				id.ShapeDigest = shape
				return capability.NewSnapshot(id, capability.Policy{}, status, complete, truncated, nil, nil, nil, nil)
			}
			legacy := makeSnapshot("", capability.StatusReady, true, false)
			doc, err := canonicalSnapshotDocument(legacy, capabilitySnapshotKey(legacy.Token))
			if err != nil {
				t.Fatal(err)
			}
			if _, exists := doc["identity"].(map[string]any)["shapeDigest"]; exists {
				t.Fatal("legacy shapeDigest was not omitted from stored representation")
			}
			if _, err := snapshots.Put(ctx, legacy); err != nil {
				t.Fatal(err)
			}
			shaped := makeSnapshot("shape-a", capability.StatusReady, true, false)
			if _, err := snapshots.Put(ctx, shaped); err != nil {
				t.Fatal(err)
			}
			for _, want := range []capability.Snapshot{legacy, shaped} {
				got, err := snapshots.GetByIdentity(ctx, want.Identity)
				if err != nil || got == nil || got.Token != want.Token {
					t.Fatalf("exact shape identity returned %+v, %v; want %s", got, err, want.Token)
				}
			}
			other := identity
			other.ShapeDigest = "shape-b"
			if got, err := snapshots.GetByIdentity(ctx, other); !errors.Is(err, capability.ErrNotFound) || got != nil {
				t.Fatalf("different repeated shape reused snapshot: %+v, %v", got, err)
			}
			for _, snapshot := range []capability.Snapshot{
				makeSnapshot("shape-b", capability.StatusFailed, false, false),
				makeSnapshot("shape-b", capability.StatusBuilding, false, false),
				makeSnapshot("shape-b", capability.StatusBuilding, true, true),
			} {
				// Essential: Historical deployments wrote unusable snapshots; seed their original format without the current write boundary.
				doc, err := canonicalSnapshotDocument(snapshot, capabilitySnapshotKey(snapshot.Token))
				if err != nil {
					t.Fatal(err)
				}
				if err := database.Client.ExecuteAQL(ctx, `INSERT @doc INTO @@c`, map[string]any{"@c": CapabilitySnapshotCollection, "doc": doc}); err != nil {
					t.Fatal(err)
				}
				if got, err := snapshots.GetByIdentity(ctx, snapshot.Identity); !errors.Is(err, capability.ErrNotFound) || got != nil {
					t.Fatalf("unusable row satisfied current cache identity: %+v, %v", got, err)
				}
				retained, err := snapshots.GetByToken(ctx, snapshot.Token)
				if err != nil || retained == nil || retained.Token != snapshot.Token || !errors.Is(retained.ValidateToken(snapshot.Token), capability.ErrSnapshotUnavailable) {
					t.Fatalf("historical unusable token changed behavior: %+v, %v", retained, err)
				}
			}
		})
	}
}
