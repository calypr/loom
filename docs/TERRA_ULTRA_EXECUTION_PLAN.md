# Terra Ultra Parallel Execution Plan

## 1. Purpose

This document converts the 20 gaps in
[`FORMAL_GAP_ANALYSIS.md`](FORMAL_GAP_ANALYSIS.md) into a safe multi-worker
execution program for a long-running Terra Ultra session.

The compiler-first schedule in
[`COMPILER_FIRST_PLAN.md`](COMPILER_FIRST_PLAN.md) is now authoritative. Run
CP0-CP9 before the service-heavy waves in this document. The package ownership,
worktree, contract-freeze, handoff, and merge rules here still apply to compiler
workers.

The objective is not to maximize the number of active workers. It is to
maximize completed, mergeable work while preventing workers from implementing
against contracts that another worker is still changing.

The governing rule is:

> Parallelize implementations behind frozen interfaces. Do not parallelize the
> definition of the same interface.

Twenty gaps must not become twenty simultaneous branches. Dataset generation,
analysis, recipes, planner semantics, row identity, GraphQL, jobs, and generated
files are shared contracts. Uncoordinated work on them would create substantial
rework.

## 1.1 Existing FHIR Backbone Is a Frozen Starting Asset

Workers must begin from the implementation that is already present. They must
not design a replacement FHIR model, parser, graph schema, GraphQL framework,
or sample-data convention unless a specific failing conformance fixture proves
that the current owner cannot be extended.

The baseline includes:

- `META/`: the primary 14-resource sample dataset used for development and
  end-to-end characterization
- `META_SMALL/`: the smaller sample dataset for faster local tests
- `schemas/graph-fhir.json`: the active graph/FHIR schema used by generation,
  generic validation, and edge extraction
- `cmd/generate/main.go`: the existing FHIR generator
- `internal/fhir/model.go`: generated Go FHIR structs
- `internal/fhir/validate.go`: generated validation methods
- `internal/fhir/extract.go`: generated graph-edge extraction
- `internal/fhirschema/generated.go`: generated field and traversal metadata
- `internal/fhirschema/schema.go`: the handwritten lookup, selector, and pivot
  logic over generated metadata
- `internal/ingest/generated_load.go`: generated fast-path resource dispatch
- `internal/ingest/row_builder.go`: generated/generic row-builder boundary
- `internal/catalog/`: existing populated-field, distinct-value, pivot, and
  populated-reference profiling/discovery
- `internal/graphqlapi/schema.graphqls`: the handwritten GraphQL contract
- `gqlgen.yml` and generated gqlgen artifacts under `internal/graphqlapi/`
- `Makefile` targets `generate-fhir`, `generate-graphql`, `graphql-check`, and
  `test`

The generator already emits more than structs. It emits validation, edge
extraction, generated load dispatch, and `fhirschema` definitions/traversals.
A worker must inspect the relevant generator output and generator source before
adding handwritten resource-specific equivalents.

The required extension policy is:

1. use `META/` to characterize current behavior before creating synthetic data
2. add minimal synthetic fixtures only for cases not represented in `META/`
3. extend `schemas/graph-fhir.json` or `cmd/generate` when the missing behavior
   is schema/generation-owned
4. regenerate with the existing Make targets
5. keep handwritten behavior in the existing owner package when it is not
   generated
6. prove generated/generic parity when changing ingestion semantics
7. never copy generated FHIR types into a new recipe, analysis, or planner
   package
8. keep GraphQL changes in `schema.graphqls` and regenerate through gqlgen;
   never hand-edit generated GraphQL artifacts as the source of truth

"Support any FHIR" therefore means extending and generalizing the existing
schema-driven backbone, not discarding it.

## 2. Orchestrator Responsibilities

Terra Ultra acts as the integration owner. It must:

1. maintain one green integration branch
2. create worker packets from a known integration commit
3. assign exclusive package and shared-file ownership
4. enforce contract freeze gates
5. merge foundational contracts before launching their consumers
6. stop or rebase workers when an upstream contract changes
7. run integration and conformance tests after every merge
8. publish a handoff manifest for each completed packet
9. keep unsupported capabilities explicit
10. prevent workers from weakening validation merely to make fixtures pass
11. reject work that duplicates existing generated FHIR or gqlgen ownership
12. require a baseline run against `META/` for every ingestion, catalog,
    planner, or analysis packet

Terra should use spare workers for review, tests, fixtures, performance
measurement, and threat analysis rather than assigning two workers to the same
hotspot.

## 3. Corrected Dependency Graph

The formal gap analysis lists a mostly sequential execution order. For parallel
delivery, Gap 15 must be split:

- **15A Job substrate:** durable state machine, leasing, cancellation, status,
  concurrency, handler interface
- **15B Export recovery:** row-stream progress, retries, resume/idempotency,
  artifact results
- **15C Job hardening:** retention, cleanup, operational recovery

Gap 3 requires 15A. Gap 14 can consume the job handler contract. Gap 15B cannot
finish until the streaming runtime exists.

```text
G1 Conformance corpus
 ├──> G6 Recipe V1 ───────────────────────────────────────────┐
 └──> shared product fixtures                                │
                                                             │
G2 Schema identity ──> G3 Dataset generations ──> G4 Integrity ──> G5 Analysis
       │                    ^                                      │
       │                    │                                      v
       └──────────────> G15A Job substrate                    G7 Templates
                                                                  │
G6 Recipe V1 ─────────────────────────────────────────────────────┤
                                                                  v
                                                         G8 Capability API ──> G9 Persistence
                                                                  │
G6 Recipe V1 ──> G10 Planner ──> G11 Filters                     │
                     │                                            │
                     └──────────> G12 Cost/cardinality <───────────┘
                                      │
                                      v
                                  G13 Preview
                                      │
                                      v
                                  G14 Row stream
                                      │
                         ┌────────────┴────────────┐
                         v                         v
                     G15B Jobs                 G16 Elasticsearch

G17 Authorization: continuous lane, final audit after G16
G18 Operations: skeleton after G2/G3, finish after G14/G15
G19 Observability: primitives early, budgets after G16
G20 Testing: framework first, continuous evidence, final matrix last
```

## 4. Contract Freeze Gates

No downstream worker starts before the applicable freeze has been merged into
the integration branch.

### C0: Conformance vocabulary

Freeze:

- recipe-family IDs
- row-grain IDs
- fixture schema and fixture IDs
- support states
- warning/error-code naming rules
- conformance result format

Required evidence:

- fixture schema tests
- duplicate-ID test
- deterministic conformance smoke result

### C1: Identity envelope

Freeze:

- project identity
- schema identity
- dataset generation ID and states
- active-generation resolution
- legacy dataset behavior
- analysis version placeholder
- authorization-scope fingerprint representation

Canonical concepts consumed by all later work:

```text
project
datasetGeneration
schemaIdentity
analysisVersion
authScopeFingerprint
```

Required evidence:

- schema digest tests
- generation activation tests
- no mixed-generation reads

### C2: Recipe V1

Freeze:

- semantic IDs
- grain representation
- column selections
- filter expression serialization
- destination representation
- normalized JSON
- typed error paths/codes
- recipe/template version fields

Required evidence:

- golden serialization tests
- deterministic normalization
- fixture round trips

Gap 11 may add operator implementations later, but it must not invent a second
serialized filter model.

### C3: Job contract

Freeze:

- job types and states
- claim/lease rules
- cancellation contract
- progress, result, and error envelopes
- idempotency key
- handler interface
- restart recovery behavior

Required evidence:

- two-worker lease test
- expired-lease reclaim test
- cancellation state test

### C4: Analysis snapshot

Freeze:

- resource, field, relationship, and value-analysis document keys
- project/generation/scope fields
- coverage denominators
- fanout representation
- truncation markers
- snapshot version/freshness
- relationship quality states

Required evidence:

- deterministic snapshot for fixture data
- auth-scope isolation test
- generation replacement test

### C5: Planner IR

Freeze:

- logical operators
- grain/root representation
- traversal representation
- projection and filter representation
- row identity
- duplicate/expansion semantics
- capability descriptors
- explain result shape

Required evidence:

- existing Patient result parity
- logical-plan goldens
- generic-versus-optimized parity

Only after C5 may separate workers add grain modules.

### C6: Product capability API

Freeze:

- support decision and reason codes
- dataset summary
- template availability
- recipe options
- validation result
- plan/cardinality explanation
- cache identity

Required evidence:

- capability and planner agreement for every fixture
- stale-analysis behavior
- GraphQL contract tests

### C7: Row-stream contract

Freeze:

- output column schema
- row representation
- missing-value policy
- array encoding policy
- cancellation/error semantics
- progress counters
- provenance
- checksum and row-count rules

Required evidence:

- preview/stream row parity
- bounded-memory test
- cancellation test

### C8: Artifact and destination contract

Freeze:

- artifact lifecycle
- temporary/finalization semantics
- destination secret references
- deterministic Elasticsearch document IDs
- retry classification
- partial-failure reporting

Required evidence:

- interrupted-artifact cleanup
- deterministic-ID test
- partial bulk response test

## 5. Package Ownership Lanes

Assign workers to durable package lanes rather than one worker per gap.

### Lane A: Dataset loading and lifecycle

Owns:

- G2 schema identity
- G3 dataset generations
- load integration with G15A

Primary files:

- new `internal/schemaidentity/`
- new `internal/dataset/`
- `internal/ingest/`
- generation-aware ingestion storage

Exclusive hotspots during its integration window:

- `internal/ingest/load.go`
- `internal/ingest/backend.go`
- `internal/ingest/row_builder.go`
- command entrypoints for load wiring

Published boundary:

```go
type DatasetRef struct {
    Project    string
    Generation string
}

type Resolver interface {
    Active(ctx context.Context, project string) (DatasetRef, error)
}
```

Exact names may change during C1, but only one canonical resolver may survive.

### Lane B: Analysis and catalog

Owns:

- G4 reference integrity
- G5 analysis snapshots

Primary files:

- new `internal/analysis/`
- new `internal/analysis/referenceintegrity/`
- `internal/catalog/`
- analysis cache behavior

Consumes Lane A's finalized `DatasetRef`. It must not invent generation
lifecycle or query inactive data.

Lane A invokes an analysis/finalization interface; Lane B implements it. The two
lanes must not both edit load orchestration.

### Lane C: Recipe domain and templates

Owns:

- G6 Recipe V1
- G7 semantic vocabulary and templates

Primary files:

- new `internal/recipe/`
- new `internal/recipe/templates/`
- recipe JSON Schema

During C2, consume current field and traversal behavior through adapters. Do
not immediately move:

- `internal/dataframebuilder/fieldrefs.go`
- `internal/dataframe/traversal_rules.go`

Consolidation occurs after recipe and planner contracts agree.

### Lane D: Planner and filters

Owns:

- G10 planner
- G11 filter compiler

Primary files:

- `internal/dataframe/`
- eventual `internal/dataframe/planner/`

This is the highest-collision lane. It has one lead until C5. After C5, additive
workers may implement:

- Patient parity/optimizations
- Specimen and DocumentReference/File
- Condition and Observation
- ResearchSubject/Study enrollment

Only the planner lead edits central dispatch and shared IR.

### Lane E: Product API and recipe persistence

Owns:

- G8 capability orchestration
- G9 recipe persistence
- product GraphQL integration

Primary files:

- new `internal/productapi/`
- recipe storage implementation
- GraphQL product operations

This lane is the exclusive GraphQL owner. Other workers return Go services and
types; they do not independently change public GraphQL.

### Lane F: Plan analysis and preview

Owns:

- G12 cardinality/cost analysis
- G13 bounded preview

Primary files:

- planner analysis/cost model
- preview service
- cursor codec

It consumes C4 and C5. It does not redefine the planner IR or edit GraphQL.

### Lane G: Streaming export and artifacts

Owns:

- G14 row stream
- NDJSON/CSV sinks
- artifact-store abstraction

Primary files:

- new `internal/export/`
- new `internal/export/artifact/`

It consumes C7 and does not modify planner semantics.

### Lane H: Durable jobs

Owns:

- G15A substrate early
- G15B export recovery
- G15C hardening

Primary files:

- new `internal/jobs/`

Implement the core with fake handlers. Lane A and Lane G add real handlers only
through the frozen job interface.

### Lane I: Elasticsearch

Owns:

- G16 bulk sink
- destination/mapping preflight

Primary files:

- new `internal/export/elasticsearch/`

Most tests use an HTTP test server. Retry scheduling remains owned by Lane H;
the Elasticsearch package classifies item errors and reports retryable items.

### Lane J: Security, operations, and observability

G17-G19 are continuous concerns, not permission to edit every package.

Own additive infrastructure:

- authorization test helpers and matrix
- scope fingerprint helper
- readiness/configuration package
- audit-event interface
- metrics registry
- deployment manifests and runbook

Every feature owner authorizes and instruments its own path. A final Lane J
worker audits the integrated system.

### Lane K: Conformance and system tests

Owns:

- G1
- continuous G20 evidence
- `conformance/`
- capability matrix publication

It must not add production shortcuts merely to pass fixtures.

## 6. Shared-File Hotspots

These files have one integration owner per wave:

- `internal/graphqlapi/schema.graphqls`
- `internal/graphqlapi/schema.resolvers.go`
- `internal/graphqlapi/generated.go`
- `internal/graphqlapi/model/models.go`
- `internal/api/routes.go`
- `internal/api/service.go`
- `internal/dataframe/service.go`
- current `internal/dataframe/planner.go`
- `internal/dataframe/lowered_types.go`
- `internal/dataframe/lowered_compile.go`
- `internal/ingest/load.go`
- `internal/ingest/backend.go`
- `cmd/arango-fhir-server/main.go`
- `cmd/arango-fhir-proto/main.go`
- `Makefile`
- `go.mod`
- `go.sum`
- top-level README and capability matrix

Generated GraphQL artifacts are regenerated once by Lane E or the integration
owner after service contracts merge. Parallel workers must not commit their own
independently generated copies.

If a worker needs a shared-file change it does not own, its handoff includes a
contract-change request or minimal integration patch description.

## 7. Parallel Execution Waves

The waves below assume four to six implementation workers plus one integrator.

### Wave 0: Product oracle and baseline

Start all workers from the same current green commit.

#### W0-A: Conformance corpus

Implements G1:

- fixture schema
- conversation fixtures
- fixture datasets
- runner
- deterministic results

Owns `conformance/`.

#### W0-B: Baseline characterization

Adds tests only for current:

- generated/generic ingestion
- planner support/rejections
- auth scoping
- GraphQL contracts
- preview memory/result behavior
- the resource types and link tuples observed in `META/`
- generated versus generic behavior for the `META/` resource types
- current field/pivot catalog output produced from `META/`
- current generator and gqlgen reproducibility

Must not improve production behavior in this packet.

#### W0-C: Architecture verification

Read-only review producing:

- package/caller map
- current collection/index map
- public API map
- hotspot confirmation

#### Gate C0

Merge fixtures and baseline tests. Freeze vocabulary before downstream work.

### Wave 1: Independent foundational contracts

#### W1-A: Schema identity and ingestion preflight

Implements G2 without dataset generation activation.

#### W1-B: Recipe V1 contract

Implements G6 types, JSON Schema, normalization, and error model. Does not yet
translate into live analysis/templates.

#### W1-C: Job substrate

Implements G15A with fake handlers.

#### W1-D: Security/test primitives

Adds:

- authorization matrix
- scope fingerprint proposal/helper
- reusable negative-test helpers
- test taxonomy

#### W1-E: Metrics/config primitives

Adds additive metrics and configuration packages only. Does not instrument all
features yet.

#### Gates C1-C3

The integrator aligns and freezes the shared identity, recipe, and job
contracts. Dependent worktrees are recreated or rebased from this merge.

### Wave 2: Atomic ingestion and semantic foundations

#### W2-A: Dataset generations

Implements G3 using C1 and C3.

#### W2-B: Template registry structure

Implements G7 registry mechanics with fake analysis/planner capability sources.
Do not finalize template availability yet.

#### W2-C: Planner foundation

Implements only:

- logical IR
- generic physical interfaces
- capability descriptor interface
- Patient parity path

This worker owns planner hotspots.

#### W2-D: Operational skeleton

Implements:

- migration registry
- startup configuration validation
- readiness framework
- graceful worker shutdown interfaces

#### W2-E: Generation and auth tests

Expands conformance/system tests without editing production owners' packages.

#### Gate

Dataset generation storage/query behavior must be frozen before analysis begins.
This is the most consequential repository-wide merge point.

### Wave 3: Dataset intelligence and planner expansion

#### W3-A: Reference integrity

Implements G4 against active-generation helpers.

#### W3-B: Resource and field analysis

Implements the G5 resource/field half using the analysis schema owner.

#### W3-C: Relationship, fanout, and value analysis

Implements the G5 relationship/value half. It consumes the same shared analysis
document types as W3-B.

One of W3-B/W3-C owns collection registration and migrations; the other may
only add analyzers and tests.

#### W3-D through W3-F: Grain modules

After C5 is merged, assign additive planner modules:

- Specimen and File
- Condition and Observation
- ResearchSubject and Study enrollment

The planner lead remains the only central dispatch owner.

#### W3-G: Typed filter implementation

Implements G11 against C2 and C5. It may add operators but cannot change Recipe
V1 serialization without a contract amendment.

#### Gate C4-C5

Freeze deterministic analysis snapshots and planner IR. Require fixture-based
result parity before product APIs consume them.

### Wave 4: Product capability plane

#### W4-A: Final templates and capability evaluator

Completes G7 using real analysis and planner descriptors.

Template-family entries may be split among workers only after registry schema
is frozen. Those workers add definitions and fixtures, not mechanics.

#### W4-B: Capability service and GraphQL

Implements G8 and exclusively owns GraphQL changes/generated artifacts.

#### W4-C: Recipe storage

Implements G9 CRUD and ownership. Compatibility/revalidation integrates after
the capability service lands.

#### W4-D: Cardinality and cost

Implements G12 against C4/C5.

#### W4-E: Security integration tests

Tests analysis, capability caching, recipes, and plan explanation across scopes.

#### W4-F: Thin frontend mock

May build against the proposed C6 contract using a faithful mock. It must not
invent missing fields or use raw catalog records.

#### Gate C6

Capability API and planner must agree on every conformance fixture. Only then
connect the frontend to live services.

### Wave 5: Execution plane

#### W5-A: Bounded preview

Implements G13.

#### W5-B: Row-stream foundation and executor seam

Defines C7 and performs the one shared execution refactor. This is the only
worker editing existing dataframe execution entrypoints during the wave.

#### W5-C: NDJSON sink

Starts after C7, owns additive export files.

#### W5-D: CSV and artifact store

Starts after C7, owns additive serializer/artifact files.

#### W5-E: Export job integration

Implements G15B against C3/C7.

#### W5-F: Execution verification

Owns:

- preview/export parity
- cancellation propagation
- stable paging
- bounded-memory evidence
- worker restart tests

#### Gates C7-C8

Freeze stream and artifact contracts before external delivery work.

### Wave 6: External delivery and release hardening

#### W6-A: Elasticsearch bulk transport

Implements encoding, batching, response parsing, and retry classification.

#### W6-B: Elasticsearch preflight

Implements destination configuration, permission/mapping checks, deterministic
ID verification, and secret handling.

#### W6-C: Final security audit

Completes G17, including cache isolation, artifact authorization, SSRF controls,
and audit events.

#### W6-D: Deployment and runbook

Completes G18.

#### W6-E: Metrics and performance

Completes G19 with budgets, load tests, dashboards, and Arango plan evidence.

#### W6-F: Release conformance

Completes G20, failure injection, and generated capability matrix.

#### Release gate

A clean development deployment must pass the same conformance suite as CI,
including restart, authorization isolation, preview/export parity, and any
advertised Elasticsearch capability.

## 8. False Parallelism to Prohibit

Terra must not launch these combinations without the named freeze:

- G2 and G3 both editing ingestion orchestration before C1
- G3 and G5 independently defining catalog/generation keys
- G4 and G5 independently defining relationship-analysis documents
- G6 and G11 independently defining filter serialization
- G7 and G10 independently defining semantic relationships
- multiple grain workers before C5
- G8 before real G5/G7/G10 capability sources exist
- G9 compatibility logic before template versioning
- G12 before C4/C5
- G13 and G14 both refactoring dataframe execution
- all of G15 deferred until export
- G16 before deterministic row identity and C8
- G17, G19, or G20 deferred as final cleanup
- frontend work against low-level catalog records
- multiple workers regenerating GraphQL artifacts

## 9. Worker Packet Template

Every Terra worker receives a packet using this exact structure.

```markdown
# Packet W<Wave>-<Lane>-<Slug>

## Identity
- Objective:
- Gap(s):
- Packet type: contract | implementation | integration | verification
- Base integration commit:
- Integration owner:

## Required reading
- Repository instructions
- Exact gap sections
- Applicable conformance fixture IDs
- Frozen contract documents/examples
- Current owner files and callers
- Existing backbone files listed in section 1.1 when the packet touches FHIR,
  ingestion, schema metadata, GraphQL, catalog, or planner behavior

## Frozen contracts consumed
- Contract name/version:
- Types and serialization that must not change:
- Dataset-generation invariant:
- Authorization invariant:

## Scope
- Owned packages/files:
- Read-only packages/files:
- Shared files requiring integrator changes:
- Generated files allowed: yes/no
- Migrations allowed: yes/no

## Non-goals
- Explicitly excluded behavior
- Existing generated or schema-driven behavior that must be reused

## Deliverables
- Production types/implementation
- Unit tests
- Integration/conformance evidence
- Migration/compatibility note
- API/operator documentation
- Handoff manifest

## Required verification
- Baseline command/result against `META/` when applicable
- Targeted package tests
- `go test ./...`
- race tests where applicable
- fixture subset
- integration command
- `git diff --check`
- generated-artifact check
- required negative/cancellation/performance tests

## Stop conditions
- Frozen contract is insufficient or contradictory
- Required fixture or upstream commit is absent
- Another active worker owns a required file
- Generation or authorization invariants cannot be preserved
- Destructive migration is required
- Public/generated contract must change
- Baseline tests fail for unrelated reasons
- Performance evidence disproves the design
- The proposed implementation would duplicate generated FHIR structs,
  validators, extractors, traversal metadata, or gqlgen-owned artifacts

## Handoff
- Structured manifest required
```

Do not issue a packet whose objective is simply "implement Gap N." Name the
exact packages, types, endpoints, collections, fixtures, and observable result.

## 10. Handoff Manifest

Every worker returns a machine-readable handoff:

```yaml
packet: W3-D-specimen-file-grains
base_commit: <sha>
result_commit: <sha>
status: complete | blocked | partial
contracts_consumed:
  - recipe/v1
  - analysis-snapshot/v1
  - logical-plan/v1
contracts_added: []
files_changed: []
migrations: []
fixtures_enabled: []
tests:
  - command: go test ./internal/dataframe/...
    result: pass
known_limits: []
downstream_actions: []
contract_change_requests: []
```

Blocked workers must include evidence, affected packet IDs, and the smallest
proposed contract amendment. They must not silently redesign an upstream
contract.

## 11. Worktree and Branch Strategy

Use a separate Git worktree per implementation worker. Workers sharing one
checkout would observe and overwrite incomplete files, especially generated
GraphQL artifacts.

`META/` and `META_SMALL/` are currently local workspace data rather than files
that a new Git worktree can be assumed to contain. Terra must make them
available explicitly to workers that need them, for example through a
read-only shared absolute path, a worktree-local symlink, or a configured
`META_DIR`. Workers must not silently fall back to invented data because their
worktree lacks `META/`. Do not commit or duplicate a large dataset merely to
solve worktree setup; keep small committed conformance fixtures separate.

Suggested layout:

```text
../loom-terra/
  integration/
  w0-conformance/
  w1-schema/
  w1-recipe/
  w1-jobs/
  w2-dataset/
  w3-analysis/
  w3-planner/
  w5-export/
```

Branch naming:

```text
codex/terra-w<Wave>-<lane>-<slug>
```

Rules:

1. Start every worker from the current wave's tagged green integration commit.
2. Provision and verify the shared sample-data path before launching any packet
   that requires a `META/` baseline.
3. Do not start from another active worker branch.
4. Workers commit only owned files unless explicitly assigned an integration
   patch.
5. The integrator merges or cherry-picks into `codex/terra-integration`.
6. After a contract merge, rebase or recreate dependent worktrees.
7. Split multi-day work into contract, implementation, and integration commits.
8. Regenerate shared generated code once on the integration branch.
9. Keep fixtures/generated data separate from production changes where
   practical.
10. Do not maintain one long-lived mega-branch per original gap.

## 12. Merge Procedure

For every packet:

1. Verify the base commit matches the declared integration gate.
2. Review the handoff manifest.
3. Reject edits outside package ownership unless preapproved.
4. Run targeted tests.
5. Merge into integration.
6. Regenerate shared artifacts if the integration owner is responsible.
7. Run `go test ./...`.
8. Run the relevant conformance subset.
9. Run `git diff --check`.
10. Update the capability matrix from test results.
11. Tag or record the new green integration commit.
12. Rebase/recreate downstream workers before they continue.

If integration exposes a contract defect:

1. pause all consumers of that contract
2. publish a versioned amendment
3. merge the amendment alone with contract tests
4. rebase consumers
5. rerun their contract/conformance tests

## 13. Required Integration Gates

Every branch:

```bash
rtk go test ./<owned-package>/...
rtk go test ./...
rtk git diff --check
```

Additional gates:

- FHIR backbone: existing `META/` resources still load; generator output is
  reproducible; generated/generic parity is maintained
- GraphQL: generation is clean and HTTP contract tests pass
- persistence: migrations are idempotent; legacy startup behavior is explicit
- dataset: no read mixes generations
- analysis: every query contains project, generation, and authorization scope
- planner: Patient parity and logical-plan goldens pass
- preview/export: row/schema parity and cancellation pass
- jobs: lease race and restart recovery pass
- Elasticsearch: partial success and deterministic retry IDs pass
- security: cross-project and cross-scope negative tests pass
- release: `make conformance` passes against a clean deployment

## 14. Terra Scheduling Rules

Use these rules for long-running efficiency:

- Keep one integrator slot available whenever a wave has more than three active
  workers.
- Limit the planner lane to one core owner until C5.
- Limit GraphQL to one owner at all times.
- Limit collection/migration registration to one owner per wave.
- When a worker blocks on a contract, reassign it to fixtures/tests for the same
  lane rather than allowing speculative implementation.
- Cancel or restart workers whose base commit predates a changed frozen
  contract.
- Prefer short contract packets followed by long additive implementation
  packets.
- Merge usable internal slices quickly; do not let foundational interfaces live
  only on a multi-day worker branch.
- Enable a capability only when its conformance fixture passes on integration.

## 15. First Terra Ultra Launch Set

The safest initial launch is now the Compiler Wave A set:

1. **CP0-A Compiler corpus and result comparison**
2. **CP0-B Current compiler/AQL/Arango baseline characterization**
3. **CP1-A Semantic IR contract design**
4. **CP2-A Generated FHIR semantic API inventory/design**

After the compiler oracle and semantic IDs freeze, launch Compiler Wave B:

1. **CP3-A Row grain/cardinality**
2. **CP4-A Typed expression/filter AST**
3. **CP6-A Aggregate semantics**
4. **CP6-B Pivot semantics**
5. **CP0-C Conformance fixture expansion**, if capacity remains

Do not launch a large frontend, recipe persistence, Elasticsearch, or broad job
infrastructure before the compiler release gate. Waiting is cheaper than
building product machinery around unstable grain, filter, pivot, or row-identity
semantics.
