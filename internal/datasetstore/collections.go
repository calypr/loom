package datasetstore

import arangostore "github.com/calypr/loom/internal/store/arango"

const (
	// LifecycleCollection stores two internal document shapes: immutable
	// generation manifests and one active-generation pointer per project. They
	// intentionally share a collection so activation can use one AQL UPDATE
	// operation to change both records atomically.
	LifecycleCollection = "loom_dataset_lifecycle"

	manifestRecordType = "manifest"
	activeRecordType   = "active_generation"
)

// CollectionSpecs returns a fresh bootstrap specification for persistent
// dataset lifecycle metadata. It never requests truncation: a FHIR reload
// must not erase manifest history or an active-generation selection.
func CollectionSpecs() []arangostore.CollectionSpec {
	return []arangostore.CollectionSpec{{
		Name: LifecycleCollection,
		Indexes: [][]string{
			{"recordType", "dataset.project", "dataset.generation"},
			{"recordType", "state", "dataset.project"},
			{"recordType", "project"},
		},
	}}
}

// BootstrapSpec returns the Arango bootstrap work needed by this adapter.
// Callers own when to bootstrap it; this package never wires the collection
// into ingest's truncate-oriented bootstrap path.
func BootstrapSpec() arangostore.BootstrapSpec {
	return arangostore.BootstrapSpec{Collections: CollectionSpecs()}
}
