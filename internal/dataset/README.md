# Dataset lifecycle

`internal/dataset` owns persistence-neutral contracts for immutable FHIR
generations, exact dataframe selectors, and atomic project releases.

Snapshot upload sessions begin in `LOADING`. Each declared resource type is
recorded with an immutable SHA-256 checksum. Finalization loads the graph and
catalogs and moves the generation to `STAGED`; it never changes normal reads.
Failures and explicit aborts move an unfinalized session to `FAILED`.

A project release binds one staged Git generation to verified `PUBLISHED`
dataframe executions selected by `(recipe, translationVersion, output)`.
Release creation is durable but invisible. Compare-and-swap activation updates
the active release and active generation together, so graph and dataframe
visibility change through one release revision. Optional publications from the
prior release are carried forward and marked stale when their generation is
older.

Stored legacy `READY` manifests remain readable as successful staged data.
New workflows write `STAGED`, and only release activation makes that data
active. The legacy direct manifest activation method remains for the migration
window but is not used by snapshot finalization.

Retention removes only expired failed generations. Active, last-good, staged,
loading, recoverable, and in-flight generations are protected, with the Arango
adapter rechecking those guards before deleting generation-qualified data.
