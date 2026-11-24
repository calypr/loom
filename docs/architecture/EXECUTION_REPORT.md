# Architecture execution report for Waves 3 and 4

Waves 3 and 4 ran on `architecture-cleanup` from the Wave 2 handoff at `4e327aa00`. The verified units are local commits. The decision register names the original work, and `.cache/archexec/ledger.tsv` gives each row's disposition, evidence and commit. Waves 5 and 6 are outside this pass.

## Architecture changes

- **Wave 3.** Bundle publication now owns state interpretation, including legacy reads, while the API still presents the established READY spelling. Dataset transitions and source-load receipts have explicit state owners. Source uploads, bundle publication and chart artifacts share cancellable lease renewal. Semantic backfill, selection writes and explicit-group writes keep their checkpoints and ownership predicates in their own transactions.
- **Wave 4.** The server composes Explorer from its concrete stores. Receipt compilation and lookup, selections, revision reads and ViewBuilder use the stores that do the work. HTTP routes invoke their owning services directly. Direct calls removed 23 Explorer store forwarding and accessor methods and 25 construction forwarding methods. Publication retains its shared transaction, and legacy workspace recovery uses the immutable projection reader.
- **Wave 4.** Signed semantic choices, row-source traversal, typed compiler selectors, workspace construction leaves, catalog/ingest records and storage identifiers now reuse their existing owners. Workspace, archive, receipt and HTTP encodings retain the validation and identity rules that each caller depends on.

## Reproduced findings and fixes

- Lease renewal could hide the work callback's error during renewal cancellation. The shared runner preserves both results. The focused race and contention witnesses passed.
- Direct writes and reads initially lost storage-specific project normalization. Receipt lookup uses the canonical ID. Explorer revision writes and repository activation use the legacy storage key. Revision reads and Undo preserve their original ID trimming.
- Legacy recovery initially constructed a service without all its dependencies. Recovery now reads the projection store directly. Three real-Arango migration fixtures also needed to provide the reader after the store split. They passed with their assertions intact.
- The route-owner view initially panicked when handlers were unconfigured. Construction now preserves the existing unavailable response. Explicit owner stores are still required for lifecycle initialization.
- Two publication tests originally injected a revision transport error into the façade. After moving the read, both tests pass the faulting revision store directly and still assert `REVISION_STORE_UNAVAILABLE` and its cause.
- The semantic candidate proof briefly introduced a compiler test import cycle. Pure schema descriptors moved to dataframe spec. An attempted semantic-plan alias was dropped after comparing its JSON tags.
- Two workers briefly edited the main checkout when tool calls omitted their worktree directory. Their exact hunks were reversed. The pre-existing whitespace edit in `internal/semantic/inventory.go` and all 11 pre-existing untracked entries were checked and preserved.

## Review and verification

The coordinator integrated source and reran confirmed failures. Separate worker contexts reviewed bundle/storage compatibility, retry and transaction ownership, compiler and workspace contracts, and API routing and fixtures. Reviewers used the same model (`gpt-6.1-sol`, low reasoning). Wave 3 workers covered leases, semantic coverage and transaction probes. Wave 4 workers covered compiler and workspace contracts, publication and storage, row-source consolidation, and ownership of Explorer routes and stores. The ledger links individual rows to test and review evidence.

All database runs used disposable local Arango and ClickHouse services. The tests exercised persisted publication replay, draft CAS, legacy recovery, and explicit-group execution in a named test database. HTTP comparison uses the baseline's documented nondeterminism masks. Render comparison checks 16 AQL/bind records and three ClickHouse records byte for byte.

Full per-test coverage, the full statechart suite, browser acceptance, and the H-01/H-17 replays were outside these two waves. The rescan used historical coverage input. No new coverage or performance measurement is claimed.

The full package-audit command fails the ownership registry check. The pre-Wave-4 comparison already misses `internal/lease` and `internal/testkit/semanticfixtures`. Wave 4 adds `internal/foundation/text`, which also needs a registry entry. The registry script is outside the authorized source-edit scope. The focused Arango audit passed. The live Go dependency graph includes the intended package edges for owned Arango stores and semantic proof and builds without cycles. The direct edge lists are in `.cache/archexec/logs/wave4-graph-stores-delta.tsv` and `wave4-graph-direct-callers-delta.tsv`.

Logs record the command, frozen source location, result and duration in `.cache/archexec/runs.tsv`. Earlier compilation failures, missing test fixture wiring, sandbox loopback denial, and the cold integration timeout remain recorded as failures. Subsequent runs corrected the harness conditions or source and checked the same contracts. The late production-root scan led to deletion of a publication store aggregate. Its two test callers now compose the actual stores. The first compile and integration attempt after that removal failed on the missed API fixture and was rerun after correcting it.

### Closing check receipts

| Check | Result | Log under `.cache/archexec/logs/` |
| --- | --- | --- |
| Wave 3 disposable integration and transaction witnesses | Pass (exit 0) | `wave34-wave3-final-integration-20261124-092320-064221.log` |
| Wave 3 audit regeneration | Pass (exit 0) | `wave34-wave3-regenerate-20261124-093001-841770.log` |
| Build, comment lint and full Go suite | Pass (exit 0) | `wave4-complete-go-20261124-113615-470443.log` |
| Disposable integration and named explicit-group witness | Pass (exit 0) | `wave4-complete-integration-20261124-113627-640370.log` |
| 628 HTTP captures | Pass (exit 0) | `wave4-complete-http-20261124-113636-734702.log` |
| 19 render records | Pass (exit 0) | `wave4-complete-render-20261124-113624-438096.log` |
| Focused Explorer Arango package audit | Pass (exit 0) | `wave4-final-arango-audit-20261124-112259-301912.log` |
| Full package audit | Fail (exit 1) | `wave4-final-verified-audit-20261124-112730-968801.log` |
| Closing scanner and audit regeneration | Pass (exit 0) | `wave4-complete-scan-20261124-113644-532676.log` |

The final scanner recorded 84 exact clone pairs, down from 187 at the end of Wave 3, and 595 normalized pairs, down from 882. No exact pairs, functions unreachable with tests enabled, staticcheck diagnostics, or unused-parameter diagnostics were added. The 17 new normalized matches cover distinct typed lookups, different path validation, or distinct publication tests. The production-root scan still flags two helpers in `internal/testkit/explorerfixtures` whose available callers are tests. Staticcheck and unparam exit 1 on existing findings. Both scan manifests and the generated TSV are under `.cache/archexec/rescan/`.

## Audit outcomes

| Wave | Category | Done | Obsolete | Deferred |
| --- | --- | ---: | ---: | ---: |
| 3 | curated | 18 | 7 | 2 |
| 4 | curated | 86 | 19 | 5 |
| 4 | prod-clone | 23 | 24 | 0 |
| 4 | rescan findings | 9 | 4 | 0 |

## Measured change

Go line counts include generated Go and Go fixtures tracked under `internal/`. Package counts are directories containing Go source outside `testdata/`. The declaration scanner counts production functions and methods, interfaces, and aliases.

| Snapshot | Packages | Production lines | Test lines | Functions and methods | Interfaces | Aliases |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Program base (`036c340e7`) | 125 | 193,308 | 206,897 | 6,298 | 105 | 114 |
| Wave 2 handoff (`4e327aa00`) | 126 | 192,418 | 203,215 | — | — | — |
| Wave 3 (`35de276cf`) | 127 | 192,419 | 203,565 | 6,198 | 105 | 112 |
| Wave 4 (`24c9dce45`) | 128 | 190,974 | 204,573 | 6,065 | 99 | 75 |

| Range | Production + / − | Tests + / − | Net `internal/` Go lines |
| --- | ---: | ---: | ---: |
| Wave 3 | +385 / −384 | +463 / −113 | +351 |
| Wave 4 | +3,013 / −4,458 | +2,181 / −1,173 | -437 |
| Both waves | +3,395 / −4,839 | +2,644 / −1,286 | -86 |
| Program base to Wave 4 | +5,104 / −7,438 | +5,018 / −7,342 | -4,658 |

The source register estimated 966 removable lines for 47 Wave 4 clone candidates. That estimate counts candidate blocks and includes overlapping candidates. The measured diff includes replacement code and regression fixtures.

## Judgment rows

- **15** Semantic owns persisted coverage build states; fieldprofile keeps separate census completeness.
- **17** Retain the readiness predicate because status, completeness and truncation can vary independently.
- **25** Keep ownership renewal in the same atomic group transaction. Shared ticker cannot replace this protocol.
- **64** Common row-source choices, matching, and ports; route-specific checks remain.
- **89** Retain authored v2 decoding at the workspace boundary. Only independently proved leaf shapes are shared by 90, 91 and 94.
- **90** Share exact leaf types and trim redundant conversions while keeping authored validators and separate related/pivot shapes.
- **91** Share recipe combine shape; retain workspace-specific validator. Reuse scalar-kind predicates.
- **92** Keep separate live and archive scalar decoders. Workspace rejects unknown and trailing JSON independently of recipe semantics.
- **94** Share exact leaf types and trim redundant conversions while keeping authored validators and separate related/pivot shapes.
- **100** Use spec/schema and receipt choice types directly; route API compilation through its existing facade.
- **145** Share HTTP pointer-string conversion and the exact workspace ID predicate. Compute both project forms from one decode in ViewBuilder state.
- **146** Share HTTP pointer-string conversion and the exact workspace ID predicate. Compute both project forms from one decode in ViewBuilder state.

## Deferred rows

| Wave | Row | Reason and prerequisite |
| --- | --- | --- |
| 3 | 6 | Retain READY decoding and stored AQL filters. GET generation status directly serializes manifest.State. Mapping READY or SUPERSEDED to STAGED would change that API. |
| 3 | 16 | Reconciler stays in fieldprofile. It depends on that package’s private census/occurrence/evidence types and its profiler invokes it. semantic/arango imports fieldprofile already. |
| 4 | 86 | Retain ArtifactStage until both consumers can depend on the same lower-level artifact types. |
| 4 | 92 | Keep separate live and archive scalar decoders. Workspace rejects unknown and trailing JSON independently of recipe semantics. |
| 4 | 96 | Retain capability-specific provenance and scope facts in the signed representation. Recipe and workspace lack those facts. |
| 4 | 97 | Retain capability-specific provenance and scope facts in the signed representation. Recipe and workspace lack those facts. |
| 4 | 117 | Do not generate clone code through the IR child walker. Its schema describes traversal, while the clone needs independent map, pointer and nil slice handling. |

## Commits

- `14a650b8e` Wave 3: consolidate bundle execution state handling
- `a4848b4b4` Wave 3: separate manifest write states and source-load receipts
- `30a5739c8` Wave 3: share revision projection and Arango primitives
- `1a0f1be6d` Wave 3: put semantic coverage build states in their domain
- `35de276cf` Wave 3: share cancellable lease renewal
- `354c17a9e` Wave 4: share HTTP option and identifier handling
- `053815a44` Wave 4: give new revisions and repository upserts an owner
- `394f5fb0b` Wave 4: consolidate bundle publication contracts
- `26a71da56` Wave 4: share row-source choices and lane contracts
- `5eea8952d` Wave 4: reuse compiler selectors and IR traversal operations
- `32093d3c3` Wave 4: consolidate text and slice primitives
- `a24436b75` Wave 4: make selection storage and revision reading explicit
- `28d51261e` Wave 4: name the inputs to recipe publication
- `877ecf281` Wave 4: reuse the digest for explicit-group storage keys
- `288346b7d` Wave 4: remove an uncalled lifecycle path formatter
- `4d94cc319` Wave 4: share compiler anchors, resolved types and lowering context
- `9dec588ca` Wave 4: share workspace combine shapes and pivot classification
- `6d7a6993c` Wave 4: point authoring and receipts at their domain owners
- `031d88c0f` Wave 4: use the native explain and population reader contracts
- `a3819cc36` Wave 4: consolidate catalog, semantic and ingest storage contracts
- `c2bf415e0` Wave 4: use selection, row-source and dataframe types at their owners
- `36ca4be35` Wave 4: share recipe scalar validation and identity formatting
- `fdd7b9d12` Wave 4: share authorized semantic candidate evidence
- `1b10f8c00` Wave 4: share Derive and Unpivot source traversal
- `ffe5e506a` Wave 4: consolidate repeated ordering and compiler checks
- `52a7a5e25` Wave 4: bind explicit-group collections at the storage boundary
- `72ad036cf` Wave 4: use standard compaction for semantic keys and scope
- `1714ba2c3` Wave 4: share workspace and recipe construction leaves
- `4c1060020` Wave 4: use compiler and receipt types from their owners
- `8a3237775` Wave 4: compose Explorer from its owned Arango stores
- `72c99edff` Wave 4: route Explorer handlers directly to their owners
- `212fb52e8` Wave 4: remove construction service forwards
- `a7ab7db39` Wave 4: reuse existing projection and semantic lookups
- `0b2c649a0` Wave 4: remove Explorer storage forwarding
- `24c9dce45` Wave 4: remove the unused publication store aggregate
