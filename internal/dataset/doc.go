// Package dataset defines the persistence-neutral identity and lifecycle
// contract for an immutable project dataset generation.
//
// It intentionally does not open a database transaction, persist manifests,
// mutate Arango collections, or resolve authorization. Storage and ingest
// adapters must apply its validated values atomically in their own storage
// transaction. In particular, a successful activation means a READY manifest
// is selected by an ActiveGeneration reference; this package never claims that
// persisting that reference and superseding a prior manifest is atomic.
package dataset
