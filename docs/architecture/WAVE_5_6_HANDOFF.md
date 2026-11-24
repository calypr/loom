# Architecture cleanup plan for Waves 5 and 6

Start from the verified Wave 4 source at `24c9dce45` and its report at `da5ca067d` on `architecture-cleanup`. The [decision register](WAVE_5_6_DECISIONS.tsv) uses the original audit IDs. The ledger has 94 open rows at this base, with 69 in Wave 5 and 25 in Wave 6. The base's HTTP comparison matched 628 masked captures. Its render comparison matched 16 fixture corpus records and three supplemental ClickHouse records. [Wave 4 report](EXECUTION_REPORT.md)

The first implementation unit replays the two release hypotheses against disposable Arango. The first architectural change makes one operation responsible for publishing a project release and the state that must become visible with it. Wave 6 changes what the tests observe after the architecture has settled.

## Execution order

1. Reproduce H-01 and H-17. Record the database observations and rerun the publish-spine model. Extract only the shared commit contract those observations support.
2. Land B1 and B3 together in verifiable slices. Keep the compatibility reader and historical replay witnesses running while moving writers. Then migrate current readers and remove fallbacks.
3. Run B2 and B4–B11 in independent worktrees when their prerequisites and package ownership allow it. Ingestion staging follows the release commit boundary. Capability caching follows the live-generation read cutover. Preview transformations and schema ownership can start independently if their IR edits are serialized.
4. Run B12 last within Wave 5. Remove the composition façade after assigning each operation it still owns. Check production and test imports for each proposed merge before moving its files.
5. Start Wave 6 from the Wave 5 tip. Reconcile the original query-assertion rows with the live tests, replace assertions in small package batches, then run final acceptance.

### Before the first move

- The full package audit currently fails its ownership registry check for `internal/lease`, `internal/testkit/semanticfixtures`, and `internal/foundation/text`. Updating those entries in `scripts/package_audit.py` needs scope for that script. With that scope, regenerate the audit and rerun `--full` alongside the first move. While the registry is blocked, use the focused audit and a fresh Go import graph to judge package edges and report the full check as failing.
- The initiating checkout contains the untracked publish-spine model and original audit. Copy model inputs into owned scratch space for replay. Track any authorized model changes with the implementation unit. Record TLC version, jar, available model inventory, bounds, and results. A model counterexample is a candidate schedule until the real store replay proves it.
- Reuse the existing archive, HTTP, render, and disposable integration tools under `.cache/archexec/tools/`. Freeze one source snapshot per checked unit. Keep the plan's public route and stored-data compatibility rules, resource scheduling, and local-only commit policy.

## Wave 5

### Release and revision publication

| Unit | Rows | Change and contract | Evidence before deleting the old path |
| --- | --- | --- | --- |
| Release replay | 32, 150 | H-01 pauses A after a successful release commit and before its source-load completion, expires A's lease, activates B, and retries A. Read the active release and source-load records independently. H-17 activates a staged generation using the generation API while a different release is active. Read through the query, capability, current source resolution and published output paths. Keep operation-scoped status a record of its own load. | Use the existing `TestSourceLoadReconcilesLostInitialActivationAcknowledgement` as a control. The F-B13 stale-CAS witness covers a losing candidate and complements the H-17 generation-route replay. Reproduce on the Wave 4 base. On a refutation, correct the model and record the missing premise. On confirmation, keep a regression for the exact schedule and prove the fix with the same real-store witness. |
| Package name and commit owner | 28–30 | Rename `internal/dataset` to `internal/release` in one import rewrite. Place shared commit orchestration in `internal/releasecommit`, above release and Explorer persistence. Use the same transaction handle for release CAS, owning revision/view writes and any required commit receipt. Keep compilation, authorization and remote materialization outside the transaction. | Compare the old and new dependency graphs, assert stale-CAS rollback of every participant, and verify a replay returns the original result even after a newer release becomes active. Remove server adapter code after both production wiring and tests have moved. |
| Publication participants | 152, 158, 164–168 | Reuse the Wave 4 revision constructors. Give the repository, interactive and source-load paths a shared commit input containing proposed release, expected revision, and prepared revision/view changes. Preserve each path's preparation rules and identities. Repository publication may prepare a private candidate but may only expose it through the shared commit. Remove old shortcuts after migration. | Inject failure after each transaction participant and inspect the active pointers, revision state and views. Compare legacy workspace recovery and all existing repository/authoring/source-load replay cases. Keep typed values for AQL state binds. |
| Route and reader cutover | 151, 153–157 | Keep the generation endpoints and route activation through the shared commit. Include CLI `activate-generation` and any stage-and-activate path in `generation.Load` in the same writer cutover. The active release becomes the authority for current generation and bundle selector binding. Preserve exact historical execution reads. A legacy active-generation record can remain as a derived compatibility projection during migration. | Seed only-generation, current-release, superseded and malformed legacy records. Require a deterministic validated backfill for only-generation data before requiring releases on current reads. Verify that query, capability, status, list and published reads agree and that reconciliation protects the active execution and live read pins. |
| Cache version identity | 31 | Capture release identity when resolving a current request and key release-dependent caches by the identity and their existing authorization scope. Keep historical reads pinned to their explicit input. | Warm both revisions with separate principals, cut over without a process-local invalidation callback, and prove cache freshness, no cross-scope hits, and stable lookup counts. |

The durable success fact for H-01 must refer to **A's own commit**, even after B becomes active. If the existing persisted release contains enough committed identity, read it by that identity. Otherwise record the smallest operation-to-commit receipt inside the same transaction. An active-pointer lookup alone loses the fact when B goes live. Chaining two live writers leaves the split that H-17 is testing. Prefer the shared commit and coordinated reader cutover. Keep proof of an uncommitted candidate distinct from proof of a previously successful release.

The three caller paths keep their own preparation and call the same commit. This is a shape for the executor to verify against release and revision storage after replay.

```go
prepared, err := preparePublication(ctx, intent)
if err != nil {
    return err
}
outcome, err := releaseCommit.GoLive(ctx, releasecommit.Request{
    Release:          prepared.Release,
    ExpectedRevision: prepared.ExpectedRevision,
    Revisions:        prepared.Revisions,
    ViewChanges:      prepared.ViewChanges,
})
```

A generation activation can have no Explorer revision participants. The release request still binds the published selectors and their executions. Move database operations behind the commit owner without making the preparation path depend on transaction handles.

### Ingestion and registry boundaries

| Unit | Rows | Change and evidence |
| --- | --- | --- |
| Existing ingestion routes | 159, 163 | Keep the public routes. `scripts/loom-dev.mjs` and `cmd/loom-repository` use the generation surface, and OpenAPI exposes it. Trace the create/status/activate calls separately. Reduce transport-specific orchestration when its internal callers are migrated. Keep existing response and auth ordering. |
| Shared staging | 33–35, 160–162 | Extract the staging operation that both ingestion paths actually share. Move only stage behavior with the same input and output semantics. Keep upload leases, partial receipts, finalization and public activation under their existing owners until the release commit path is in use. Inject the semantic inventory writer. Migrate the CLI and its fixtures through the shared staging entry. Compare row and edge counts, field and semantic digests, failure states and the manifest produced by the same input. |
| Legacy source preparation | 36 | Move mutable-source migration out of publication only after an idempotent maintenance path can prepare all supported legacy fixtures and readiness can be established for existing projects. Keep the live recovery path if inventory or rollout evidence is unavailable. |
| Registry read ownership | 37–39 | Recheck the numeric helper. Give recipe deletion an authoritative publication usage check with the original race protection. Exercise old document spellings before changing queries. A one-time migration only replaces compatibility reads once mixed documents, ambiguous fields, restart, and mixed writer behavior are accounted for. |

### Compiler output, previews and trace

| Unit | Rows | Change and evidence |
| --- | --- | --- |
| Output schema | 169–170 | Carry authored identity and presentation through the compilation input. `recipe.Field` already carries column ID and label. Start with the scalar field case and add only source metadata the compiler cannot derive. Keep it out of archived recipe bytes and existing digest inputs. Begin with one representative authored output and compare compiled column order, types, IDs, emission IDs, selector, presentation and receipt bytes. Include at least one shape-changing operator in the comparison before removing predicted metadata. |
| Public contract | 171–173 | Make the compiled output and its ordered columns the source of the contract once the projections agree across all supported shape families. Prove which step owns output grain, loss and suitability, source choices, and filter and chart capability before removing their current producers. Old stored receipts keep their own decoder, digest validation, and historical query access. Keep validation that compares a receipt to a subsequent execution. Shrink stored fields only with explicit identity/version and round-trip evidence. |
| Root paging | 174 | Reuse `PhysicalLimitOp`. Root paging already builds filter, sort and limit operations and issues a root-key query followed by a selected-root query. Try one explicit selection/page-after transformation and preserve the two-query contract until a single plan proves equivalent. Compare against the current typed root-preview variant. Keep post-filter counts, ordering, source identity, stable paged results and byte-identical query/binds. Track exactly which predicates can run before the limit. |
| Other previews | 175–177 | Migrate one caller-selected mode at a time and delete the matching path only after equal render and live row behavior. Preserve safe fallback for unsupported shapes and the existing index choice. Use current modes and callers instead of the old file list. Keep a specialized path when it cannot meet the render or latency contracts. |
| Provenance | 178–181 | Reuse the typed `PhysicalCellTraceReturnOp` and keep row-lineage output identity explicit. Prototype carrying shared provenance through one bounded operator family and check it against the current lineage, trace and forward query. Expand only if the pass preserves authorization, attribution for duplicate/null/missing contributors, cell pagination, and row identity through cardinality-changing operators. Keep agreement tests until a retained end-to-end test proves each behavior they covered. |

Compare keeping the typed preview at the rendering boundary with moving one mode into a typed physical plan transformation. The new shape earns its place if it removes the render branch, honors the same query and performance contracts, and needs no second set of permissive flags. The 19-record corpus omits many specialized modes. Add focused witnesses for each migrated mode using the old renderer's results and existing assertion fixtures. Compare warm/cold measurements only if query shape, reads or materialization change. Each observation needs a fixed dataset, same scope and preview limit, and the deadline for that workflow.

### Explorer and query service boundaries

| Unit | Rows | Change and evidence |
| --- | --- | --- |
| Capability cache | 182–184 | Build a validated immutable cached value. Failed builds return errors. Status participates in the snapshot token hash. Stop storing failed builds first, then keep a reader for the old payload and token before changing either. Incomplete, truncated or failed legacy records remain unusable. Keep public readiness projection and historical token authorization. Use the legacy-identity rebuild and old-token witness, then verify same-key coalescing, collisions, incomplete or truncated snapshots, and revoked access. Switch reads/writes before dropping store query fields. |
| Prepared commands | 185–188 | Use existing sealed payloads and a separate prepared value for each kind that needs server data. Move mutable resolution and draft source context out in the same caller migration. Preserve absent, explicit null and clear semantics, kind-specific field rejection, JSON bytes and the apply request digest. Keep strict restore and proposal authorization rechecks. Move pure apply logic to its payload owner when this removes transport-kind dispatch. Keep orchestration near the dependencies that perform it. |
| Error boundary | 189–191 | Unify semantic classification where two branches make the same decision. Map that classification to HTTP once per boundary while preserving detailed public codes, stages, diagnostics and error identity through wrapping. Test typed causes and selected 400/403/404/409/422/503 responses before removing classifiers. Treat any response-body delta as deliberate schema/client work with an approved oracle entry. |
| Construction and startup | 192–194 | Wire required services in the server and validate a dependency once at its owning constructor. Surface required-dependency errors that `NewHandlers` currently discards. Preserve routes and responses for genuinely disabled features. Delete one proven redundant call-time guard at a time. Start with an always-wired lifecycle owner, compare zero-config responses, then repeat for each service. |
| Query input | 195–197 | Prove graph-only methods have no live caller using call sites, registry wiring and a compile. The generated preview input aliases `dataframe.FhirDataframeInput`. Replace that schema ownership before deleting the handwritten DTO. Map the existing preview and `cmd/dataframe-profile -variables` inputs to the same recipe with exact enum and validation behavior. Keep historical recipe names and IDs until versioned decoders and stored receipt/digest witnesses cover any rename. |
| Semantic coverage | 198–200 | Trace coverage reachability first. Explorer has a live coverage lookup, but the optional profiler coverage sink and build writers have no verified production caller. Keep the live reader compatible and extract only behavior with a demonstrated owner. Move pure transitions and the accumulator beneath storage if they remain needed. Keep claim, checkpoint and release writes in their original atomic transactions. Move only census/evidence inputs that break the fieldprofile dependency before relocating the reconciler. Verify competing owners, partial restart, missing/invalid census, and stable sorting. Row 200 describes two external heaps. Current `runs.go` has one external heap and a separate in-memory reduction. Confirm there is no second live external merger and close the stale row without adding a generic helper. |

### Last Wave 5 step

The Wave 4 owner view already dispatches most HTTP operations directly. The remaining lifecycle package has 46 production files, including draft, restore and execution orchestration. Move those entry points to the services that own their invariants. Construct the owners at server composition and assemble the generated OpenAPI interface from the route groups. Delete the façade only after compile, tests, and import trace establish that no caller uses its remaining entry points.

| Rows | Plan and dependency proof |
| --- | --- |
| 201, 206 | Split handlers along real owners while keeping the generated route contract. Migrate one route family first and keep its auth/error/receipt behavior. After the remaining lifecycle owners move, remove the unused façade and its configuration aggregate. |
| 202 | Recheck the remaining output helpers after B4 and fold them into receipt if it remains their only caller. A direct receipt-record merge currently closes `receipt → explorer → receiptmodel` and `receipt/output → compilation → receiptmodel`. Keep the lower record package unless the move first removes both reverse paths without moving persistence into compilation. |
| 203 | Check the row definition and row source dependencies in both product and test builds. The recorded production graph has no indirect path that would make this merge cyclic. Keep row definition preparation and row-source selection as separate use cases, as Wave 4 did. Keep construction inputs with its published-output lookup and authorization. Move shared pure row-source leaves only if this eliminates duplication. |
| 204 | Keep the wire codec below the services that prepare and apply commands. The proposed direct merge closes `authoringcore → explorer → authoringv2` and a path through lifecycle support to authoring network. Move selected pure operations after B7 or record the boundary as retained. |
| 205 | Keep shared semantic proof below both semantic browsing and construction. Directly merging proof and browse would close `semanticbrowse → explorer → authoringv2 → semanticauthoring`. Reconsider only after removal of that path. |

## Wave 6

The original 25 rows 799–823 report 402 lexical predicate hits. At Wave 4, the same scanner reports 183 hits in 46 test files across `internal/`. Its generated table has four rows past its reporting threshold. Eleven original file paths still report hits, with 90 hits across those files. A hit can be setup, a real contract assertion, or multiple predicates on one statement. A zero does not show a moved test is gone. Reconcile by test function and observed guarantee after Wave 5, then attach replacement tests or a retain reason to each original row.

For each unit, inventory the predicate, the case that makes it fail, and the output or contract that should fail instead. Classify as follows.

| Predicate protects | Witness |
| --- | --- |
| Rows, output shape, presence, grouping, lineage, scope, or exact errors | Reuse or add an Arango/ClickHouse result or typed outcome assertion with independent expected values. Keep denied/foreign/empty fixtures that show the behavior would fail. |
| Stable serializer or renderer form with no result-level substitute | Use a bounded per-case golden with exact query plus bind arguments, or a typed IR assertion if that is the layer owning the invariant. Capture from the verified source, compare a second run, then review the golden's actual content. |
| Query strategy, index selection, or bounded work | Use the existing typed plan decision or live EXPLAIN/index witness. Confirm the same scope, fixture and limit. Recording a full query alone does not prove runtime work is bounded. |
| Injected fault, transaction scheduling or stub query dispatch | Keep dispatch that drives real regression coverage. Explain its role in the row. Replace shape assertions on scoping or CAS with required tests that prove rejection, rollback and no leaked history. |

Start with row 802's repeated coded display cardinality, then row 822's restricted related scan. For the latter keep negative root/edge/target scope, null/missing and cap behavior alongside existing index witnesses. Row 807 mixes real draft CAS fault injection, DTO assertions and query predicates. Keep the synchronization and failure triggers, and use the required concurrent-writer, rollback, conflict and scoped-read cases as the behavior witnesses. Changing the test requires exercising the replacement against a deliberate wrong output or missing predicate in an isolated copy, or pointing to an existing independently failing fixture that does so.

Each package unit keeps the original assertions until replacements run. Show the sorted test list diff and the remaining predicate classifications. Require every replacement integration test to execute. Add it to the required runner when that file is in scope. Otherwise invoke the named test explicitly and require a pass event in its captured Go JSON output. Regenerate the audit after Wave 6 and record newly discovered actionable rows with their own IDs.

## Verification and handoff

- Every unit runs build, closest package and direct importer tests, and Go comment lint. Capture 628 HTTP and 19 render records for affected domains and extend focused witnesses where the original baseline has no coverage. Run disposable integration for storage, publication, ingestion and compiler behavior changes. Retain exact source and logs alongside the ledger.
- Review risky units on the final diff with separate briefs. Publication review covers transaction participants, retry and compatibility data. Compiler review covers metadata/digests, row and bind parity, and lookup/performance invariants. For package changes, inspect focused audit and live import-graph diffs before and after.
- After Wave 6, run `make test-go`, `make test-integration`, `make test-ui`, `make e2e`, `make lint-go-comments`, and the full package audit. Run the repository-wide internal coverage gate below. Run each available statechart with explicit bounds and record missing models or tooling. Record every failure and retry with its source and logs.
- Close each of the 94 original rows and any findings generated by the new source, then update [the execution report](EXECUTION_REPORT.md) with final source, dispositions, test outcomes, and any remaining conditions. H-01/H-17 outcomes belong in the report even when a replay refutes the model. Local commits only.

### Repository-wide internal coverage

Run from the final source snapshot. This gate supersedes the execution prompt's per-test coverage rerun for final acceptance.

```sh
coverage_run=$(mktemp -d "${TMPDIR:-/tmp}/loom-internal-coverage.XXXXXXXX")
GOMAXPROCS=3 GOFLAGS=-p=3 go run scripts/internal_coverage.go \
  -out "$coverage_run/results" -minimum-covered 67455
```

The command discovers every package under `internal/`, derives the runnable tests from their source files, and runs one instrumented suite. It checks package and top-level test completions against the inventory, with test skips reported separately. The combined profile unions statement blocks across packages, including code exercised by another package's tests. Compare its covered-statement count to the saved 67,455-statement package-union baseline. Report the percentage, count and test dispositions from `summary.json`, and retain the raw events, profile and log. Require the separate database integration witnesses to pass as before.

The first repository-wide run at the Wave 5 planning base (`ef539d752`) accounted for all 128 packages and 3,447 top-level tests. It recorded 3,243 passes, 204 skips, and 66,809 of 91,507 statements covered (73.0097%). The gate exited 1 because coverage was 646 statements below the floor. Every previously covered block in unchanged files remained covered. The net deficit is in changed or moved files. Record this starting condition and keep the original floor for final acceptance.
