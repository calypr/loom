package capabilitydb

import arango "github.com/calypr/loom/internal/explorer/arango"

type Store = arango.Store
type CapabilitySnapshotStore = arango.CapabilitySnapshotStore

var (
	NewCapabilitySnapshotStore       = arango.NewCapabilitySnapshotStore
	CapabilitySnapshotCollectionSpec = arango.CapabilitySnapshotCollectionSpec
)
