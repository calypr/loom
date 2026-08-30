# Loom Reliability Contract

This document freezes the compatibility contract for versioned dataframe
publication, immutable ingest, project releases, and ETL. New code
must use these identities and states. The public APIs have no name-only or
default-contract compatibility path.

## Canonical identities

A dataframe is identified by the exact selector
`(recipeName, translationVersion, outputName)`. Serialized selectors use the
fields `recipe`, `translationVersion`, and `output`; no component is inferred by
internal publication or read code.

A recipe version is identified by `(name, translationVersion)`. Versions are
retained. A version may be replaced only before its first durable execution is
created; after that point its content digest is immutable.

An immutable FHIR generation is identified by `(projectID, gitCommit)`. A
resource upload within it is identified by `(projectID, gitCommit,
resourceType)`, and its checksum is part of the idempotency contract.

## States

- Raw generation: `LOADING`, `STAGED`, `FAILED`.
- Publication: `QUEUED`, `RUNNING`, `VALIDATING`, `PUBLISHED`, `FAILED`.
- Project release: `ACTIVE`; visibility is determined solely by the atomic
  active-release pointer.

Historical storage rows may be normalized by the storage adapters while they
are retired, but new workflows use only `PUBLISHED`.

## Project release

A project release atomically records:

- project ID and source Git revision;
- the selected staged FHIR generation;
- published execution IDs keyed by exact dataframe selector;
- required-contract verification results; and
- optional publications carried from the prior release, marked `STALE` when
  their source generation is different.

Creation and activation are separate. Activation is compare-and-swap against
the observed active release revision. A failed generation load, verification,
or publication never changes the active pointer.

Those are internal persistence operations, not separate Explorer authoring
steps. Explorer Preview is read-only. Explorer Publish materializes the config
outputs and creates and activates the corresponding project release in one
server workflow for both repository-managed defaults and user-created
Explorers. The UI does not save or activate releases directly.

## GraphQL and Flat API

```graphql
input DataframeSelectorInput {
  recipe: String!
  translationVersion: String!
  output: String!
}
```

Dataset discovery, rows, aggregate, aggregations, and export require a
complete `selector`. No `dataType`, materialization-ID, or default recipe
resolution is accepted.

Dataset metadata exposes the exact selector and active project publication.
Every read requires an explicit authorized project; cross-project discovery
and schema reconciliation are not supported. Explorer Preview and Publish are
the supported materialization workflow.

## Snapshot HTTP API

Snapshot operations are idempotent:

1. create or resume a generation keyed by Git commit;
2. upload one resource type with checksum verification;
3. finalize a complete generation as `STAGED`;
4. inspect generation status; and
5. abort an unfinalized generation without changing the active release.

Repeating an operation with identical content succeeds. Different content for
the same generation/resource identity returns a non-retryable conflict.

## Error taxonomy

Public structured errors use stable codes:

- `DYNAMIC_SCHEMA_DRIFT`: runtime/project-specific fields cannot be reconciled;
- `RECIPE_CONTRACT_VIOLATION`: a recipe output violates its declared contract;
- `PUBLICATION_FAILED`: materialization or output verification failed;
- `CHECKSUM_CONFLICT`: an immutable resource key was reused with new content;
- `GENERATION_INCOMPLETE`: finalize was requested before all uploads completed;
- `RELEASE_REQUIREMENTS_UNMET`: required selectors are not published and
  queryable for the staged generation;
- `RELEASE_ACTIVATION_CONFLICT`: compare-and-swap observed a newer active
  release; and
- `INVALID_SELECTOR`: the selector is absent or incomplete.

Every structured failure may include phase, output, details, retryability, and
the Loom request ID. Retryability is explicit; clients do not infer it from an
HTTP status or message.

## Configuration

- `LOOM_REQUIRED_DATAFRAME_SELECTORS`: JSON array of exact selectors required
  for release activation.
- `LOOM_PUBLICATION_WORKER_LEASE`: publication worker lease duration.
- `LOOM_PUBLICATION_MAX_ATTEMPTS`: bounded publication retry count.
- `LOOM_SNAPSHOT_RETENTION`: inactive generation/release retention duration.
- `LOOM_ETL_LEGACY_MUTABLE_UPLOAD`: ETL rollout escape hatch; snapshot mode is
  the default after integration acceptance.

Configuration files may expose equivalent structured fields, but environment
names and meanings remain stable.

## Migration rules

The rollout is selector-first. Existing persisted publication rows may be
read by storage migration code, but they are never selected through a name-only
or default contract and no new request can address them without an exact
selector.

New writes always use exact recipe versions, immutable generation keys, durable
publication states, and release-controlled visibility. No migration step moves
an active pointer merely because a newer generation or publication exists.

## Readiness

`/readyz` reports infrastructure dependencies only. Failed, building, stale, or
degraded dataset publications are surfaced by dataset/execution status APIs and
do not make the Loom process unready.
