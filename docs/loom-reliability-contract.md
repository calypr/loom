# Loom Reliability Contract

This document freezes the compatibility contract for versioned dataframe
publication, immutable ingest, project releases, federation, and ETL. New code
must use these identities and states. Legacy readers remain additive during the
compatibility window described below.

## Canonical identities

A dataframe is identified by the exact selector
`(recipeName, translationVersion, outputName)`. Serialized selectors use the
fields `recipe`, `translationVersion`, and `output`; no component is inferred by
internal publication or federation code.

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
- Federation availability: `AVAILABLE`, `DEGRADED`, `UNAVAILABLE`.
- Authorized project state: `CURRENT`, `STALE`, `BUILDING`, `FAILED`,
  `MISSING`, `EXCLUDED`.

Legacy `READY` values remain readable and map to the appropriate successful
state, but new workflows never write `READY`.

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

## GraphQL and Flat API

```graphql
input DataframeSelectorInput {
  recipe: String!
  translationVersion: String!
  output: String!
}
```

Dataset discovery, rows, aggregate, aggregations, and export accept exactly one
of `selector` or deprecated `dataType`. Passing both is an input error.
`dataType` resolves through the configured default recipe and active promoted
contract version.

Dataset metadata exposes the exact selector, active contract version,
availability, completeness, included and expected project counts, and an
authorization-filtered project status list. Project status includes project
state, generation, execution ID, timestamps, error code, and retryability.
Unauthorized project identities are never returned.

Operator operations support:

1. starting an asynchronous exact-version materialization;
2. polling its durable execution;
3. activating a completed project release; and
4. explicitly promoting a recipe version as the default federation contract.

The first registered contract may initialize the default automatically. Every
later version requires explicit promotion.

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
- `FEDERATION_INCOMPATIBLE`: published sources cannot share a logical schema;
- `CHECKSUM_CONFLICT`: an immutable resource key was reused with new content;
- `GENERATION_INCOMPLETE`: finalize was requested before all uploads completed;
- `RELEASE_REQUIREMENTS_UNMET`: required selectors are not published and
  queryable for the staged generation;
- `RELEASE_ACTIVATION_CONFLICT`: compare-and-swap observed a newer active
  release; and
- `INVALID_SELECTOR`: selector/dataType inputs are absent, ambiguous, or
  mutually supplied.

Every structured failure may include phase, output, details, retryability, and
the Loom request ID. Retryability is explicit; clients do not infer it from an
HTTP status or message.

## Configuration

- `LOOM_DEFAULT_RECIPE`: recipe used to resolve deprecated `dataType`.
- `LOOM_DEFAULT_TRANSLATION_VERSION`: promoted default contract version.
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

The rollout is additive. Existing name-only recipes, direct materialization-ID
reads, deprecated `dataType`, and stored `READY` states remain readable for one
compatibility window. Legacy recipe rows are interpreted at the configured
default translation version. Legacy executions retain their IDs and acquire
selector fields when resolvable; unresolved legacy rows are readable but never
chosen by exact-selector federation.

New writes always use exact recipe versions, immutable generation keys, durable
publication states, and release-controlled visibility. No migration step moves
an active pointer merely because a newer generation or publication exists.

## Readiness

`/readyz` reports infrastructure dependencies only. Failed, building, stale, or
degraded dataset publications are surfaced by dataset/execution status APIs and
do not make the Loom process unready.
