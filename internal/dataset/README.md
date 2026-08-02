# Publication

`internal/dataset` owns the small persistence-neutral contract that makes a
completed ingest visible to readers.

It does not store FHIR rows, build catalogs, authorize requests, or open a
database. Ingest creates a manifest in `LOADING`, marks it `READY` only after
the graph and catalogs are complete, or marks it `FAILED` when loading cannot
finish. `READY` and `FAILED` are terminal.

The project-level active pointer selects the manifest used by normal reads.
`ResolveActive` validates that the adapter returned the requested project's
complete `READY` manifest. Explicit generation reads may resolve any `READY`
manifest, whether or not it is active.

The `arango` subpackage persists manifests and active pointers in
`loom_dataset_lifecycle`. Activation changes only the active pointer, using
Arango revision checking so concurrent activations cannot silently overwrite
one another. It also normalizes legacy lifecycle states when reading old
documents; it does not resume abandoned loads.
