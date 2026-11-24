# Architecture execution report

Waves 3 and 4 ran on `architecture-cleanup` from the Wave 2 handoff at `4e327aa00`. The verified units are local commits. The decision register names the original work, and `.cache/archexec/ledger.tsv` gives each row's disposition, evidence and commit. The Waves 5 and 6 section records the final acceptance runs and the remaining gates.

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

## Waves 5 and 6

Wave 5 gave release activation one transaction owner. Source loads record success in that transaction. Interactive activation, repository publication, the generation API and direct activation commands use the same release commit path. Current selectors use the active release first, and the database tests still read exact historical executions.

Explorer now passes a prepared command and explicit draft source context to apply. The wire command contains the client payload only. Capability caching writes usable READY snapshots, retains valid old tokens and separates identities by shape. Recipe deletion checks publication usage inside the write guard, including legacy document spellings. Preview and the profiler share one recipe input conversion.

Receipt owns final output reconciliation and cell-trace preparation. The cell-trace route calls that owner directly. Lifecycle retains use cases that still require its orchestration. Ingestion shares semantic completion and manifest staging, and whole-file and per-file paths produced the same measured fixture inventory, rows, edges and manifest. The semantic package owns pure coverage proofs, and Arango still owns the transaction.

### Publication replays

| Replay | Before | After |
| --- | --- | --- |
| H-01, lost source-load completion | A committed, its acknowledgement was lost, and B activated before A retried. The real retry reached PUBLISH_PENDING and a legacy first-load conflict. The original model's direct SUPERSEDED observation did not describe that real retry. | A's completed receipt is durable at the commit boundary. Retry returns A's original execution and revision while B stays active. A forced receipt-write failure rolls back the release, owner, view and revision writes. |
| H-17, divergent generation and release | Direct activation selected generation B while the active release and published reader still used A. | Activation commits through the shared release owner. Current generation, release and published selectors agree on B, and the historical reader still resolves A. A release-backed CLI load without a new output leaves the previous bundle's bindings intact. |

The corrected publish-spine model verified 31 distinct reachable states, 62 generated states and five invariants at depth 9. LiveLoadHasReceipt captures the corrected H-01 premise. The first JVM sandbox attempt failed on local RMI bind. The owned local rerun passed. The real-store logs are under .cache/archexec/logs/ with the wave5-h01-, wave5-release- and wave56-publish-statechart- prefixes. The raw H-17 before evidence is /private/tmp/loom-wave5-h17/logs/h17-baseline.log.

### What Wave 6 changed

- Coded display uses a live eight-case result and cardinality witness. Preferred text wins, blank or missing preferred text falls back when configured, repeated displays still reject the cell, and generic empty and blank text stay present. The identical-display case rejects a renderer mutation that deduplicates before checking cardinality.
- Pivot lineage checks actual page order, final-page boundaries, malformed and foreign identities and global policy errors on unrelated groups. A mutation that drops the unrelated group's duplicate check fails. The tests retain assertions that bound materialization and require exact scope and bind values.
- Draft history checks actual concurrent CAS, stale digest at the current version, unchanged head and history, wrong key, wrong stored ID and foreign project or Explorer. Missing-predicate overlays expose each history guard. Keep barriers, rollback fault injection and query dispatch that actually schedule the competing writes.
- Related scans check independently expected foreign and denied roots, intermediate targets, terminal targets and both edges. Preserve live null/missing, capping and EXPLAIN index checks. Keep query checks where they constrain work or strategy beyond the row outcomes.
- Group renderer tests use typed frontier eligibility and still execute the fallback renderer. Four bounded fixtures cover exact AQL or SQL and binds for keyless ONE/ALL, scoped LEFT JOIN and workspace APPEND. Two captures were byte-identical. Mutations changing sentinel counting, accepting the ONE frontier, disabling join null extension and reversing APPEND order fail the named checks.

### Bounded alternatives retained

The output-schema probe matched scalar field identities and selected audited shapes. Coded Pivot emits a code-selected column with no recipe.Field identity carrier. The original recipe metadata supplies IDs and labels for this case. A uniform recipe metadata rewrite would change archived bytes and digests. Keep the current receipt contract and validate it at execution until one compiler-produced representation covers these shape families with explicit identity compatibility.

Replacing the indexed grouped Pivot preview with a plain selection filter preserved the selected IDs but changed the render and lost the observed lookup strategy. Sharing the COUNT_ROWS contributor path changed AQL and binds and introduced eager buffering. Keep those specialized plans and the existing index choice. The bounded provenance check preserved the tested attribution and error contracts in the existing implementation. The data does not support migrating all operator families at once.

Release remains the authority for current reads, and old active-generation rows and legacy publication spellings remain readable. Clients still call the generation endpoints. Keep required service construction errors visible and leave optional-service guards in the owners that enforce them. A direct semantic browse and proof merge would close an existing import cycle, so the current shared proof package remains below both.


### Verification on the final source

Verified source is the staged tree 9a78d9ca635f0558da7ebc4827a96c5fa2f31333. The Go, integration and browser runs used the same captured code and test fixtures. The final report commit changes documentation. Full commands, source identity, log paths, exit codes and elapsed times are in .cache/archexec/runs.tsv.

| Check | Result | Log in .cache/archexec/logs |
| --- | --- | --- |
| Build, Go comment lint and make test-go | Passed. The first Wave 5 full suite caught a gofmt drift in the server composition after patch integration. Formatted the file, reran the format gate and passed the final Go suite. | wave56-final-go-20261124-135338-495229.log |
| make test-integration, disposable Arango and ClickHouse | Passed with no failed profiles on the uncontended retry. Concurrent verification exhausted the original driver budget and the dataset profile timed out. The same suite was rerun with its normal deadline. The runner reports optional external-data cases separately. | wave56-final-integration-retry-20261124-140712-999988.log |
| Named Wave 6 witnesses | All 17 named checks passed, with zero named skips. The first supplemental run needed the Observation collection to be created in its private test database. The fixture retry passed. | wave6-final-witnesses-retry, wave6-final-required.jsonl and focused wave6-* logs |
| make test-ui | Passed 1,600 tests in 112 files with an offline install from the lockfile in an isolated checkout. Two earlier runs loaded React across two dependency trees and failed. A clean dependency install resolved the failure without a source change. | wave56-final-ui-clean-install-20261124-140140-886265.log |
| make e2e | Full suite failed with 10 of 12 scenarios passing. Group timed out at the same checkbox on the final source and pre-Wave-5 baseline. Pivot twice hit Chrome's DevTools startup deadline and passed when run alone on the final source. | wave56-final-browser-20261124-135338-097443.log, wave56-browser-group-pivot-baseline-20261124-140202-748160.log, wave56-browser-pivot-only-20261124-140452-574302.log |
| HTTP and render oracles | Passed. 628 canonical HTTP captures matched with the established masks. All 19 recipe render records matched byte for byte. | wave56-final-http-20261124-135507-932243.log, wave56-final-render-20261124-135504-908737.log |
| Internal coverage floor | Failed. 66,823 of 91,451 statements covered (73.0697%), 632 below the required count of 67,455. The inventory recorded 127 Go packages, 3,254 test passes and 210 reported skips. | wave56-final-coverage-20261124-135336-960012.log |
| Full package audit | Failed at the responsibility map. Missing entries include foundation/text, lease, releasecommit and testkit/semanticfixtures. The map still includes the retired receipt/output and server/sourcepublication packages. The Go import graph and focused package checks passed. Updating scripts/package_audit.py is outside the authorized write scope. | wave56-package-audit-exact and wave56-final-package-registry |
| Final source rescan | Passed generation with explicitly historical per-test redundancy input. Compared with the first Wave 5 rescan, one unreachable wrapper and one fixed-argument warning disappeared. No new exact or normalized clone pairs or staticcheck/unparam findings appeared. | wave56-final-rescan-20261124-135615-178698.log |
| Publish-spine statechart | Passed. It is the only executable model present. The remaining cards and system chart specified in the design do not have model files in this checkout. | wave56-publish-statechart-20261124-133604-021450.log |

The saved program baseline covered 67,455 of 92,701 statements (72.7662%). The comparable one-suite run before Wave 5 covered 66,809 statements, already 646 short. The final run recovered 14 statements. The profile comparison located 310 formerly covered statements in deleted files, including the old recipe adapter and graph service. Other changes include moved blocks and changes in instrumentation coordinates. Block relocation changes instrumentation coordinates, so the attribution check treats unmatched coordinates as candidates for investigation. The gate still uses the original floor. No Wave 6 test names were removed, and the new database case counts as skipped in the unit-only coverage run.

The nine Wave 6 packages list 669 tests, examples, fuzz targets or benchmarks, up from 668. The only addition is TestConstructionCodedPivotDisplayFallbackCardinalityAgainstArango. The raw test-name comparison is wave6-test-list.diff. The narrow scanner counted 73 matching predicates in the original 25 paths immediately before Wave 6 and 52 after it. That count omits selectors such as rendered.Query. The same scanner counted 187 hits across 46 internal test files before Wave 6 and 166 across 44 files after it. The retained-row table records function-level checks from the broader predicate inventory.

The lineage harness first skipped because it lacked the test database name. The rerun used a new named database and required each pass event. The draft ID mutant failed in the intended assertion, but the harness initially searched for the wrong failure text. The corrected check read the original Go JSON result and verified the failing test event. The scope mutation run reached its deadline after 12 cases. A smaller retry completed the three remaining auth cases. All 15 mutations exposed their corresponding marker. No mutations entered the staged tree.

Other handled failures include the initial revision-helper compilation against an incorrect package name and missed caller paths. The final build covers all five callers, and the query and bind values are identical. Wave 5 rescan generation initially lacked the untracked architecture changes input in its snapshot; supplying a read-only copy of that input regenerated the table. Staticcheck and unparam returned diagnostic exit statuses on both snapshots. The delta found no new diagnostic after the two fixes. Filename lint still reports the three pre-existing execution backend filenames, which were outside the work.

The disposable probes never accessed the canonical dataset volumes. The optional read-only full-CDA factorization experiment was retained as a test and was outside the required final fixture run. The authoring schema probe and grouped preview alternatives were evaluated independently and left out of production. Full package audit, Group browser acceptance and the fixed coverage floor remain open.

### Measured change

Counts include Go files under internal outside testdata. The package column counts directories with production Go source. The coverage inventory also includes the test-only package. A single AST method counted all three snapshots.

| Snapshot | Packages with source | Production files | Production lines | Test files | Test lines | Production functions and methods | Interfaces | Aliases |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Program start 036c340e7 | 124 | 834 | 193,308 | 624 | 206,897 | 6,316 | 105 | 126 |
| Wave 5 start bed29aea1 | 127 | 855 | 190,974 | 634 | 204,573 | 6,083 | 99 | 89 |
| Wave 6 cac9cece5 | 126 | 858 | 191,016 | 639 | 205,654 | 6,101 | 103 | 90 |

Waves 5 and 6 added a net 42 production Go lines and 1,081 test Go lines. Across the full program, production Go fell by 2,292 lines and tests by 1,243 lines, a net reduction of 3,535. The additional tests in the last two waves include real transaction replays, rollback checks and retained query-contract fixtures. The original register sums to 47,258 estimated removable lines across 643 estimated rows, including overlapping candidates and cases retained after verification. Measure the actual result with the Git diff and source counts above.


Git reports 6,716 insertions and 9,145 deletions for production Go, and 6,806 insertions and 7,912 deletions for Go tests under internal/. The all-file diff includes fixtures and other tracked data. 821 files changed, 13617 insertions(+), 17057 deletions(-).

### Final ledger counts

| Wave | Category | Done | Obsolete | Deferred |
| --- | --- | ---: | ---: | ---: |
| 1 | dead-test-helper | 15 | 0 | 0 |
| 1 | redundant-tests | 12 | 291 | 1 |
| 1 | test-clone | 69 | 19 | 0 |
| 1 | test-copies-prod | 12 | 3 | 0 |
| 1 | tests-on-dead | 63 | 13 | 0 |
| 2 | dead-code | 128 | 12 | 0 |
| 2 | duplicate-import | 22 | 0 | 0 |
| 2 | error-strings | 0 | 22 | 0 |
| 2 | staticcheck | 51 | 1 | 0 |
| 2 | unparam | 78 | 2 | 0 |
| 2 | unused | 35 | 0 | 0 |
| 3 | curated | 18 | 7 | 2 |
| 4 | curated | 86 | 19 | 5 |
| 4 | prod-clone | 23 | 24 | 0 |
| 5 | curated | 7 | 2 | 3 |
| 5 | structural | 29 | 10 | 18 |
| 6 | query-text-asserts | 9 | 0 | 16 |
| Rescan | test/source scan | 22 | 4 | 0 |
| All | | 679 | 429 | 45 |

### Judgment and retained rows

| Rows | Decision | Evidence or prerequisite |
| --- | --- | --- |
| 6 | Retain READY decoding and stored AQL filters. GET generation status directly serializes manifest.State. Mapping READY or SUPERSEDED to STAGED would change that API. | Final retry confirmed bulk/load/service.go:131 returns the decoded Manifest.State directly and dataset/arango/documentcodec.go still decodes historical values. |
| 16 | Reconciler stays in fieldprofile. It depends on that package’s private census/occurrence/evidence types and its profiler invokes it. semantic/arango imports fieldprofile already. | Final retry confirmed fieldprofile semantic.go invokes ReconcileSemanticCoverage with census/emission types and semantic/arango imports fieldprofile. Move requires a lower shared census input. |
| 28 | Keep internal/dataset until the integration runner and ownership registry are in scope together. | scripts/run-arango-integration.mjs:133–134 names both package paths and scripts/package_audit.py:130–132 owns them; a code-only move breaks named required checks. A single scripted path/alias rewrite including those two files unblocks it. |
| 36 | Keep first-publication migration on the legacy mutable path. | Legacy prepareLegacySourceLoadActivation is an observed source-load recovery path in the H-01 baseline. No owned inventory proves all applicable projects have been converted. Needs idempotent maintenance plus deployment readiness. |
| 39 | Retain both spellings in deployed readers and the atomic deletion guard. | Legacy uppercase fixture remains readable and can race a write. Needs staged migration with restart and mixed-writer witnesses; no shared data access. |
| 86 | Retain ArtifactStage until both consumers can depend on the same lower-level artifact types. | Final retry confirmed explorer.ArtifactStore.Begin uses its own ArtifactStage interface and explorer/artifactfs imports Explorer types. Concrete wiring would reverse the dependency or require new neutral artifact types. |
| 92 | Keep separate live and archive scalar decoders. Workspace rejects unknown and trailing JSON independently of recipe semantics. | Final retry confirmed workspace.TableScalar.UnmarshalJSON uses strictDecode, preserves raw field presence, and rejects incompatible payloads; archive decoding remains different. |
| 96 | Retain capability-specific provenance and scope facts in the signed representation. Recipe and workspace lack those facts. | Final retry confirmed signed construction steps bind node IDs, resource types, direction and match mode. Recipe route steps do not represent the same signed contract. |
| 97 | Retain capability-specific provenance and scope facts in the signed representation. Recipe and workspace lack those facts. | Final retry confirmed capability.ObservedResource and ObservedRelationship carry raw observations separately from workspace catalog readiness and policy. Unification needs a validated mapping and compatibility rules. |
| 117 | Do not generate clone code through the IR child walker. Its schema describes traversal, while the clone needs independent map, pointer and nil slice handling. | Final retry confirmed clonePhysicalOperation and clonePhysicalBindVars deep-copy container payloads; generatechildren.go generates traversal only. |
| 154 | Require a release for current published reads in server composition (already configured), retain historical/pre-release adapters. | The validated backfill for legacy only-generation records is absent. Removing the compatibility reader before historical migration can lose visible outputs. |
| 169, 170, 171, 172, 173 | Retain compiled schema and receipt metadata producers until one can own every shape without changing durable identities. | A Field-only carrier cannot represent pivot, aggregate, owner-record or dynamic outputs. Experiment also alters new archived recipe/digest bytes. No contract/version bump or experimental metadata landed. |
| 175, 176, 177, 179, 180, 181 | Retain proven specialized preview and provenance paths. | Existing positive/negative renderer witnesses pass. Generic selected keys scan instead of indexed DOCUMENT; sharing provenance eagerly materializes GROUP contributors. A different bounded representation needs exact rows/paging/scope and physical query evidence. |
| 188 | Keep the operation discriminators that carry authorization or different resolution requirements. | TablePayload, ColumnPayload and ProposalPayload group distinct operations. Signed population route vs direct edges, imported guard, authored vs pinned field removal and frame Set/Replace/Remove do not have a common authorization or preparation. Needs operation-specific variants; no new compatibility dispatch shipped. |
| 193, 194 | Keep guards for intentionally optional services until their complete owner/configuration is moved. | Runtime nil capabilities correspond to supported disabled endpoints and tested protocol responses. Required lifecycle construction now surfaces its error at the server boundary. |
| 197 | Keep the historical recipe identity strings. | Rewriting graphql_request/graphql-request would require an identity/receipt decoder and mixed stored-data migration. B10 proved both REST and CLI keep the original canonical recipe bytes. |
| 199 | Keep the fieldprofile reconciler and Arango owner transaction. | A direct move would create coverage/semantic to fieldprofile to semantic import cycle. Factor the census/evidence input dependency first; coverage finalization itself is pure and moved separately. |
| 201 | Cell trace now calls receipt directly. Keep the lifecycle application for its remaining live route use cases. | The remaining lifecycle use-case contracts and configuration ports must move to their owners before deleting the façade. Removing it now requires replacing live orchestration. |
| 799 | Retain the ancestry route and contribution replay checks. | TestExpandedCodingGroupLineagePagesExactInputOccurrences checks duplicate occurrence counting and ordered paging. The composed live witness does not isolate truncation or rescanning at each ancestor. |
| 801 | Retain pre-materialization and root-hint strategy checks. | TestConstructionPreviewBoundsProvenRootIDPivotBeforeSourceMaterialization and TestConstructionGroupPreviewUsesProjectionFieldsAndOneRootHint check physical work and hint placement. Rows alone would pass an eager fallback. |
| 803 | Retain lowerer shape and shared computation probes. | TestWideDynamicFamilyUsesOneSourceComputation distinguishes repeated field evaluation. The extension and traversal examples also pin nested aliases and JSON projection. No equivalent work-count or complete bind fixture was established. |
| 804 | Retain versioned correlation checks. | TestCorrelatedPhysicalVersionedFilterMatchesSameCodingVersion ties version and matching value to one coding owner. There is no live mutation-proven conflicting-version fixture for the exact branch. |
| 806 | Retain remaining reshape compilation checks. | TestConstructionPivotTracksPresenceForExactRelatedField exercises the exact related-field shape as well as the typed group cases. Existing scan results do not cover every reshape projection branch. |
| 808 | Retain concrete renderer-contract tests. | TestBuildAndRenderOrderedContributorWindowAggregate and the direct-child projection test distinguish bounds and contraction after navigation. None of the four new fixtures covers general navigation windows. |
| 809 | Retain distinct expansion ownership and route projections. | TestSchemaDefinedSecondShapeKeepsIndependentArraysSeparate and the deep owner-route test pin independent scans and identity structure. No exact fixture replaced the remaining strategy assertions. |
| 812 | Retain pinned cohort terminal and ordering checks. | TestCompileExplicitGroupRowsUsesPinnedRevisionTerminal checks the pinned terminal and absence of a broad root scan; TestRelatedCountAppendedToExplicitCohortUsesExactRootContributors covers a separate private contributor stream. |
| 814 | Retain typed intermediate stage and related-source coverage. | TestCompileConstructionUsesTypedIntermediateStages and TestCompileRelatedSourceOnePolicyReducesDistinctPresentValuesToNullableScalar exercise private stage IDs and presence semantics distinct from published row tests. |
| 815 | Retain work-bound tests alongside result-based execution checks. | TestConstructionGroupCountRowsOnlyQueryAvoidsInputRowBuffers and TestConstructionGroupAllRowValuesUsesStreamingTypedAggregates reject eager buffering even when resulting rows match. |
| 816 | Retain typed table lowering and renderer assertions. | TestCompileGroupedTablePivotPreservesNullAndMissingCategoryIdentity and TestCompileUnpivotPreservesColumnOrderNullPolicyAndIdentity protect exact identity construction and independent reduction policies. |
| 817 | Retain coded lowering strategy checks. | TestConstructionCodedGroupMissingPoliciesAndTupleDeduplication covers tuple attribution and expected missing behavior. The coded display regression covers a distinct Pivot operation. |
| 818 | Retain private population plan and route witness checks. | TestPopulationMappingDirectPlanAndCompilationKeepMemberRowWitnessesInternal and TestCompilePopulationMappingReversedRouteRunsOnceBeforeWitnessTerminal pin path execution and output scoping. Live frontier dedup alone does not catch those changes. |
| 819 | Keep factored projection shape and real index checks. | TestSemanticGroupProjectionFactorizationMatchesExactArangoOutputs compares rows. Scoped EXPLAIN and optional full-CDA checks retain index selection because equal rows do not imply equal cost. |
| 821 | Retain expansion strategy and explicit empty route checks. | TestCompileRelatedExpandUsesDistinctTerminalIdentityAndExplicitEmptyPolicy and TestOnwardRelatedExpandLooksUpActiveIdentityAndPreservesNullParentRows cover within-row identity and no broad scan independently of result count. |
| 823 | Keep exact trace contributor and query structure checks. | TestCompileSourceOnlyGroupRowsCellTraceMatchesExactOneAndAllMemberValues and TestConstructionRelatedSourceCellTraceReplaysScopedRoutePerOccurrence cover different proof branches. Existing typed/real cases do not exhaustively replace provenance for all operators. |
| 828 | Preserve preexisting operator-owned file. | Closing retry: file still exists untracked in operator checkout and is absent from implementation index. Preserve as authorized. |
| 15 | Semantic owns persisted coverage build states; fieldprofile keeps separate census completeness. | Three gpt-6.1-sol low reviewers found no verified regression. |
| 17 | Retain the readiness predicate because status, completeness and truncation can vary independently. | TestIncompleteAndTruncatedSnapshotsAreNotUsable and isolated WaveThreeReadinessProbe passed. |
| 25 | Keep ownership renewal in the same atomic group transaction. Shared ticker cannot replace this protocol. | Prior supplemental invocation skipped for missing database name; corrected log wave34-wave3-witnesses-20261124-092627-622228.log. |
| 29 |  | Release-backed generation, API bundle, source-load and authoring CAS share transaction; legacy generation-only fallback remains. Repository participant is tracked separately by 152,158,164,165. |
| 31 | Keep discovery and scope keys on immutable generation plus existing authorization scope. | The two caches contain generation data and authorization paths. Same-generation publication changes selector bindings read directly by published.Reader, not those cached values. Keep invalidation for the remaining legacy mutable path. |
| 64 | Common row-source choices, matching, and ports; route-specific checks remain. | oWWMWQW6: build, Go and comment lint, 628 HTTP exact, wire/skip probes; two independent same-model reviews cleared |
| 89 | Retain authored v2 decoding at the workspace boundary. Only independently proved leaf shapes are shared by 90, 91 and 94. | Current workspace ConstructionInputRef, ConstructionRowValue and ConstructionOperation have closed, operation-specific UnmarshalJSON paths that recipe lacks; persisted-shape and strict-decode suites passed. |
| 90 | Share exact leaf types and trim redundant conversions while keeping authored validators and separate related/pivot shapes. | Integrated source under final build, exact HTTP/render, and required review; explicit store changes await disposable integration. |
| 91 | Share recipe combine shape; retain workspace-specific validator. Reuse scalar-kind predicates. | Third full Go/build/lint and integration, exact HTTP and renders, combine JSON test, same-model compiler review. |
| 94 | Share exact leaf types and trim redundant conversions while keeping authored validators and separate related/pivot shapes. | Integrated source under final build, exact HTTP/render, and required review; explicit store changes await disposable integration. |
| 100 | Use spec/schema and receipt choice types directly; route API compilation through its existing facade. | Integrated source under final build, exact HTTP/render, and required review; explicit store changes await disposable integration. |
| 145 | Share HTTP pointer-string conversion and the exact workspace ID predicate. Compute both project forms from one decode in ViewBuilder state. | Initial integration exhausted the runner budget under concurrent load; warmed combined retry passed. Broader parse-once migration needs to account for sequential percent decoding. |
| 146 | Share HTTP pointer-string conversion and the exact workspace ID predicate. Compute both project forms from one decode in ViewBuilder state. | Initial integration exhausted the runner budget under concurrent load; warmed combined retry passed. Broader parse-once migration needs to account for sequential percent decoding. |
| 159 |  | Release-backed generation, API bundle, source-load and authoring CAS share transaction; legacy generation-only fallback remains. Repository participant is tracked separately by 152,158,164,165. |
| 178 | Keep bounded lineage and cell trace independent of forward contributor materialization. | Typed contributor carry for COUNT_ROWS adds materialized COLLECT INTO and changes all three renders. The bounded lazy path is required. |
| 205 | Keep independent current owner boundaries. | Rowdef/rowsource/constructioninputs have distinct inputs and invariants. Merging authoring core into wire types closes authoringcore→explorer→authoringv2; merging semantic proof into browse closes semanticbrowse→explorer→authoringv2→semanticauthoring. |
| 800 | Pivot lineage page order, forged/foreign identities and unrelated-group validation proved by exact Arango rows and errors. Preserve typed scope, bind and work bounds. | Existing test names retained; focused adversarial evidence and full suite logs indexed in the execution report. |
| 802 | Coded display fallback and multiplicity use real output cells and the cardinality error. Keep typed duplicate policy and selector checks. | Existing test names retained; focused adversarial evidence and full suite logs indexed in the execution report. |
| 805 | Group preview fallback asserts its typed frontier decision and still renders. Keep existing bounded execution/order/strategy checks. | Existing test names retained; focused adversarial evidence and full suite logs indexed in the execution report. |
| 807 | Draft-history reads and same-version stale digest are checked on persisted data. Keep barrier and rollback dispatch. | Existing test names retained; focused adversarial evidence and full suite logs indexed in the execution report. |
| 810 | Use exact scoped LEFT JOIN SQL and binds for the selected case. Keep existing live membership and nullable behavior. | Existing test names retained; focused adversarial evidence and full suite logs indexed in the execution report. |
| 811 | Use exact workspace APPEND SQL and binds for the selected case. | Existing test names retained; focused adversarial evidence and full suite logs indexed in the execution report. |
| 813 | Keyless ONE/ALL use small exact AQL and bind fixtures. Keep distinct keyed, coded and bounded aggregate cases. | Existing test names retained; focused adversarial evidence and full suite logs indexed in the execution report. |
| 820 | Category and column population use typed output IDs and real scope, presence and cap results. Keep independent stage prefix, shape and index checks. | Existing test names retained; focused adversarial evidence and full suite logs indexed in the execution report. |
| 822 | Restricted related scan checks actual roots, both edges, intermediate and terminal targets under independent project, generation and auth scopes. | Existing test names retained; focused adversarial evidence and full suite logs indexed in the execution report. |

### Local commit history

- `5055ded54` Wave 1: exercise the real row-source service and remove copied test helpers
- `2783629be` Wave 1: use the live ClickHouse renderer and share storage test fixtures
- `56a1eb4e3` Wave 1: share the Explorer source-input probes and retain receipt error cases
- `cd64a6456` Wave 1: exercise the active FHIR row loader and remove test-only encoders
- `b3f066a62` Decide architecture cleanup Waves 1 and 2 for execution
- `237ec1fa2` Wave 1: exercise live Explorer helpers and remove test-only wrappers
- `ca81d13b0` Wave 1: share token churn and manifest transition fixtures
- `d363af4ab` Wave 1: exercise recipe authorization and workspace contracts at their owners
- `abc671a4f` Wave 1: exercise live Arango index and migration operations
- `977f88e02` Wave 1: verify receipt fingerprint drift through the live compiler
- `0227499ca` Wave 1: share compiler recipe and agreement fixtures
- `489925040` Wave 1: consolidate recipe, IR, and population test cases
- `c5d840b7e` Wave 1: share Explorer construction and publication fixtures
- `7e2114cbe` Wave 1: retire legacy semantic writers and generation spill
- `5f447590a` Wave 1: share the Arango diagnostic error test
- `50652bf3b` Wave 1: consolidate compiler and storage test fixtures
- `589609417` Wave 1: keep contained-resource coverage on the live semantic walker
- `bc56f21f4` Wave 1: exercise lifecycle behavior through its owning services
- `295f4ee34` Wave 2: remove dead code and simplify internal signatures
- `bc2176030` Wave 2: finish signature cleanup found by the rescan
- `4e327aa00` Plan architecture cleanup Waves 3 and 4 against the Wave 2 tip
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
- `da5ca067d` Record architecture cleanup Waves 3 and 4
- `ef539d752` Plan architecture cleanup Waves 5 and 6
- `bed29aea1` Verify internal coverage with one Go suite
- `8399fceca` Wave 5: commit source-load receipts with release activation
- `88b6f064c` Wave 5: give release activation one transaction owner
- `310cda6a4` Wave 5: use the recipe model for dataframe preview input
- `abd8e1e6e` Wave 5: surface Explorer construction errors at startup
- `0a396cb82` Wave 5: put repository visibility and source staging under their owners
- `3101a064f` Wave 5: let publication own recipe usage reads and the write guard
- `9d27c3919` Wave 5: store only usable capability snapshots
- `51ebd19aa` Wave 5: keep semantic coverage proofs below Arango persistence
- `4e3e78fa4` Wave 5: make prepared Explorer commands explicit
- `18fc4cdce` Wave 5: move output reconciliation into receipt ownership
- `1fb98acd5` Wave 5: route cell trace directly to its receipt owner
- `d3e2ff513` Wave 5: clear reachable-graph and revision helper scan findings
- `cac9cece5` Wave 6: prove query behavior with live rows and focused goldens
