package arango

import arangostore "github.com/calypr/loom/internal/backends/arango/internal/store"

const (
	LifecycleCollection = "loom_dataset_lifecycle"

	manifestRecordType      = "manifest"
	activeRecordType        = "active_generation"
	snapshotRecordType      = "snapshot_generation"
	releaseRecordType       = "project_release"
	activeReleaseRecordType = "active_project_release"
)

func CollectionSpecs() []arangostore.CollectionSpec {
	return []arangostore.CollectionSpec{{Name: LifecycleCollection}}
}

func BootstrapSpec() arangostore.BootstrapSpec {
	return arangostore.BootstrapSpec{Collections: CollectionSpecs()}
}
