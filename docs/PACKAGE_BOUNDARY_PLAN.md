# Internal package boundary plan

Baseline `d148fc19d` on `internal-cleanup`. This plan follows the [repository cleanup goal](REPO_CLEANUP_GOAL.md) and builds on the [internal reorganization plan](INTERNAL_CLEANUP_PLAN.md), [filename work](TEST_FILENAME_TARGET.md#package-candidates), and [package audit](PACKAGE_AUDIT_DECISIONS.csv). Use the current source when any earlier plan differs from the implementation.

## What clean looks like

- A maintainer can locate the implementation, shared types, and behavior tests for a workflow by following its normal entry point.
- Each package owns a responsibility that can be stated in one sentence. Its callers use a small, purposeful API, and its dependency direction follows that ownership.
- Large receivers, protocol models, and transaction boundaries have an explicit owner. Files inside that package are named for the behavior they implement.
- Auth decisions, persistence semantics, generated interfaces, and observable behavior survive each move. Remove obsolete paths and temporary adapters once callers migrate.

The baseline has 17 internal directories with at least 20 tracked Go files. Eight have at least 20 production files. Count production, tests, and generated code separately. File count selects directories to examine. A package graduates when its responsibility and dependency direction make sense, even if it is still large.

## Compare designs before choosing a boundary

1. **Trace the workflow.** Record entry points, state and type ownership, private helpers and receiver methods, external importers, tests, and persisted or generated contracts. Use the audit and compiler-backed references. Verify findings in source. Check method receivers, private fields, and test-only imports explicitly. A low groupcheck count alone cannot establish a legal move.
2. **Build two or three competing prototypes from the same commit.** Use separate worktrees and scoped ownership. For each proposed extraction compare coherent files in the existing package, an extraction behind a narrow API, and a different ownership model when credible, such as extending an existing domain package or moving the shared model down a layer. Exercise the same end-to-end slice in each prototype. Run `go list` and compile the affected packages and direct importers so the proposed graph is checked by Go. A sketch or an empty package is insufficient evidence. Keep worktree changes isolated from the live checkout and keep raw logs in `.cache/package-boundaries/`.
3. **Compare the running prototypes side by side.** Use the same caller walkthrough, target tests, fixture data, and baseline. Record the files readers traverse for that workflow and the production and test lines relocated. Count new imports, exports, adapters, and callers migrated. Record dependency direction and the exact behavior check results. Read the actual diff for each alternative. Show how the winning design removes an ownership ambiguity or a dependency.
4. **Choose the smallest design that delivers the ownership improvement.** A cycle, layer violation, behavior change, access to another package's private state, or lost test coverage disqualifies the prototype until fixed. Prefer the in-package layout if extraction adds exports or wrappers with little improvement. Record failed alternatives and what observation would justify revisiting them. Mark unavailable checks explicitly. Keep a rollback point at the unit boundary.
5. **Apply one cohesive unit.** Move the declarations and behavior tests, migrate callers and selectors together, and remove any staging adapters. Keep product fixes in separately verified units. Use the repository's ownership and verification rules before committing.

For each decision record the base commit, proposed owner, competing layouts, measured results, chosen boundary and why, and verification logs. Keep the durable decision in the existing package audit decision record or the implementation commit. Put expanded graphs and diffs in the local evidence directory.

## Execution order

| Unit | Alternatives to prototype | What decides |
| --- | --- | --- |
| **1. Receipt output reconciliation** | A. Split reconciliation into focused files within `explorer/receipt`. B. Extract `explorer/receipt/output` with reconciliation, per-step handlers, and focused tests. Compare against the existing layout. | A disposable B prototype moved 1,882 production lines with zero references back to the parent and two production symbols used by the parent. Receipt, compilation, lifecycle, and Explorer API suites passed after test imports were migrated. Reuse that as evidence for B, build A to the same acceptance slice, and compare output metadata, typed errors, and receipt identity. |
| **2. Lifecycle semantic browsing** | A. Separate browse orchestration, route proof, and field presentation within lifecycle. B. Put policy and result construction in the existing `explorer/semanticbrowse` package. C. Give the whole browse workflow an independent service with explicit authorization, source resolution, and readers. | Trace `BrowseSemanticInventory` through the 1,785-line `catalog.go`. Compare policy ownership and imports. Exercise denial and authorized results, cursor continuation, and field-presentation proof against the same fixtures. Keep database adapters and lifecycle-private state out of the chosen domain surface. |
| **3. Lifecycle artifacts** | A. Split preparation, open/validation, and metadata files within lifecycle. B. Extract the artifact use case while lifecycle supplies its dependencies. C. Extract descriptor, metadata, and identity logic while retaining orchestration in lifecycle. | Preserve revision and receipt identity, authorization, row limits, and aborted stage cleanup. Measure how much of `*Service` the new API needs. Keep `artifactfs` and backend storage adapters at their existing layer unless the prototype proves an ownership change is needed. |
| **4. Arango storage subsystems** | Compare focused files inside `store/arango` with separate query inspection and preview-index maintenance packages. Prototype query inspection first, then compare passing a narrow index port against moving that workflow behind the existing store client. | Query inspection screened at one outgoing and zero incoming references. The six-file preview-index group screened at three outgoing and zero incoming, but reaches into private `Client.db`. Prove a one-way graph and unchanged query behavior, index definitions, migration order, retry, and dry-run semantics before picking either extraction. |
| **5. Execution tracing** | Compare cohesive tracing files inside execution with an executor for already-compiled queries. Keep the `Engine` entry points in both proofs and compare the effect of moving result types separately. | Check bounded witnesses, paging, cancellation, error classification, and real response shapes. Trace request and result types through receipt, lifecycle, and API. Defer the package move if it requires broad type churn for a small implementation gain. |
| **6. Compiler and remaining large packages** | Compare in-package layouts for lowering, IR stage validation, AQL rendering, and generated Explorer handler implementations. Prototype a package only when a concrete leaf appears. | Lowering and AQL share private state across many files. Use receiver ownership and validated IR contracts as constraints. Fix the IR scan to use active build files before drawing a dependency conclusion. Reopen compiler paging when there is a concrete owner for its seven outgoing references. |

Screen `workspace`, `construction`, `recipe`, and `published` as shared-model or shared-workflow owners. The current dependency evidence favors keeping their package boundaries. For workspace, an independent protocol model is a distinct design question. Compare it against a coherent within-package model layout using canonical JSON, digest, compatibility decode, and caller migration evidence.

On a fresh baseline, classify every internal directory with at least 20 Go files as **extract**, **organize within package**, or **keep**, with one sentence of evidence. Include lower-count packages only when there is a concrete ownership issue, as with receipt. Review large files in the keep packages by responsibility. Reopen a decision when the code changes materially or a new caller exposes a boundary problem.

## Verification and completion

- Run `python3 scripts/package_audit_verify.py <location>` before and after package moves. Include source and destination packages and direct callers. Run the full audit and inspect the dependency diff. Run `make lint-filenames`, `make lint-go-comments`, and `make test-go` before landing a wave.
- Compare package test lists before and after. Account for each moved test by its old and new package and name. Preserve examples, fuzz targets, environment gates, build constraints, and fixture paths. Check for stale imports and hardcoded selectors in Make, CI, and scripts.
- Verify behavior at the normal entry point for the subsystem. Use the repository's owned integration or Builder checks where applicable. Capture the base outcome, relevant source snapshot, command, log, exit status, and any skipped coverage. The filename pass recorded Arango draft conflicts on the base and stale recipe and backfill driver paths. Establish current behavior before relying on that runner. Treat environmental failures and timeouts as failures and carry unresolved checks forward explicitly.
- Finish the program when each screened package has a supported decision and each accepted move has passed caller, dependency, and behavior checks. Defer a split with a concrete blocker and a condition for reopening it. Avoid rows that merely say a package still feels large.

## Baseline dispositions

The paths below are relative to `internal/`. Counts exclude nested packages and show production/test Go files at the baseline.

| Package | Files | Disposition | Evidence |
| --- | ---: | --- | --- |
| `api/explorer` | 10/34 | Organize within package | Generated OpenAPI methods share the `Handlers` receiver. Route files can follow operation ownership while preserving that receiver. |
| `dataframe/compiler` | 19/69 | Keep | The root owns orchestration. Paging and liveness have seven outgoing references and an inbound helper used by two files. Reopen when the outgoing logic has an independent owner. |
| `dataframe/compiler/ir` | 24/10 | Organize within package | Active-build groupcheck on stage validation found 104 outgoing references and 14 incoming edges. Preserve the shared IR and separate validation by operation. |
| `dataframe/compiler/lower` | 34/33 | Organize within package | The 3,764-line Construction implementation has 41 outgoing and 25 incoming edges. Stage handlers share lowering state. |
| `dataframe/compiler/render/aql` | 58/17 | Organize within package | Sequence, layout, and navigation share 44 outgoing and 81 incoming edges. Sequence rendering can separate from source preparation and pruning while sharing renderer state. |
| `dataframe/execution` | 23/25 | Organize within package | Tracing is reachable through Engine and shares error and result contracts with Explorer. Compare local grouping with a compiled-query executor. |
| `dataframe/published` | 13/8 | Keep | The read, SQL, paging, and planning group has traffic in both directions. Authorization and materialization selection belong to the same read surface. |
| `dataframe/recipe` | 29/13 | Keep | Encoding, documents, and bundle validation share domain types and cross-references. Reopen if the persistence contracts acquire a separate consumer and stable owner. |
| `explorer` | 18/15 | Keep | Viewer projection consumes root revision types. Reopen when projection has an independent input model that can replace those dependencies. |
| `explorer/arango` | 10/14 | Keep | Saved views and project defaults share the publication settings transaction. Preserve the transaction and persisted keys before considering a move. |
| `explorer/authoringv2` | 14/12 | Keep | The command, commands, and change group has 55 outgoing and 126 incoming edges. The dispatcher and operation implementations share the authoring model. |
| `explorer/compilation` | 10/11 | Keep | Interpretation resolution feeds the same resolved-input contract used by compilation and source selection. Reopen if candidate selection can return an independently owned type. |
| `explorer/construction` | 26/14 | Keep | Proposal, route, and evidence methods share a service and cross-package inputs. Their three-file selector has 102 outgoing and 61 incoming edges. |
| `explorer/lifecycle` | 39/54 | Organize within package | Semantic browsing and artifact handling have distinct responsibilities. Private Service dependencies determine what can move. |
| `explorer/receipt` | 11/10 | Extract | Output reconciliation and per-step handlers form a one-way boundary with two production symbols used by the parent. |
| `explorer/workspace` | 40/14 | Keep | A six-file construction and validation group has 69 outgoing and 227 incoming edges. Reopen after a standalone model preserves compatibility decode and digests. |
| `store/arango` | 18/9 | Organize within package | Query inspection and preview-index maintenance are cohesive. The transport methods and index code use private client state. |

## Selected layouts

All comparisons used `d148fc19d`. Raw commands, diffs, and logs are under `.cache/package-boundaries/`.

| Unit | Comparison and selection |
| --- | --- |
| Receipt | Both focused files inside receipt and an output subpackage passed receipt, compilation, lifecycle, and API tests. The subpackage adds a one-way production dependency, uses no forwarding API, and accounts for all 60 original test entries. It incorporates the smaller files from the file-only prototype. The largest moved production file is now 675 lines. |
| Semantic browsing | The local file split and the policy extraction passed the browse and caller suites. The policy variant moved about 223 implementation lines and added 12 exported functions while lifecycle still owned orchestration. Keep inventory and field presentation in separate files within lifecycle. Revisit a service extraction after the five dependencies already grouped by field browsing can be shared without reaching into private lifecycle state. |
| Artifacts | Both local files and an artifact metadata subpackage passed lifecycle, compilation, and API suites. The child exported 12 helpers without moving the workflow. Keep types, preparation, downloads, descriptors, and identity validation in focused lifecycle files. |
| Query inspection | Both layouts passed store, profiling, conformance, compiler, execution, and ingest caller suites. The child variant required 31 root aliases or wrappers, or direct updates in 13 caller files. Keep parsers and assessment in place and put the Explain and Profile HTTP methods together. |
| Preview-index maintenance | The full child package built but exposed private dependencies used by the tests. The narrower prototype passed store and caller tests using a three-method collection port. It added 21 exports and kept database scans, locks, and the migration ledger in the parent. Keep current ownership. Reopen when per-collection tests can move with the implementation and the public surface can shrink. Real migration and missing-database cases passed on both sides. |
| Execution tracing | Both layouts passed the execution suite. The row-lineage executor prototype required three type aliases and a forwarded sentinel. Keep the Engine methods and their behavior in the cell and lineage files. The real cell-trace test passed on base and the selected layout with an owned Arango database on each side. |
| Compiler and API | Both tested lower, IR, AQL, and OpenAPI layouts preserved declarations and passed their focused suites. Use six lower files to separate stage dispatch from operation handlers, keep coded-group validation separate in IR, and separate AQL sequence rendering, source selection, and pruning. Organize API handlers by route family. The combined server suite exposed an `openapi*.go` test selector. Status and 500-body audits now find route declarations across the package and retain coverage and provenance checks. The ownership guard still requires exactly one generated registrar in the server package. |

The IR dependency probe now uses active build files and module export data. Its stage-validation probe reported 104 outgoing references and 14 incoming edges after excluding the ignored generator. The IR baseline count includes one generated Go file and one ignored generator file.

## Verification record

- The extracted receipt and its parent retain all 60 listed tests. The broader inventory comparison matched 1,568 entries across 16 baseline packages after mapping the receipt tests to their new owner.
- The full Go suite passed. Artifact, browse, receipt, and route caller suites passed. Targeted real Arango cases ran on base and the selected code for cell tracing and preview-index migration, with no skipped targets in those runs.
- A stale IR generator directive used the retired generator filename. Running the corrected directive reproduces the tracked Children file byte for byte.
- Adversarial review used three same-model GPT-6.1 Sol reviewers. The coordinator reproduced a 500-audit false pass caused by a same-named helper and a groupcheck failure on an active Cgo declaration. The corrected audit rejects the bad route body. Groupcheck now uses Go's compiled-file mapping and attributes the edge to the original Cgo file. The affected reviewers rechecked both corrections.
- Full integration coverage remains unverified. This pass exercised the named Arango cases independently of the previously documented draft conflicts and stale integration-driver selectors.

- The final package audit passed. The sole new package is receipt output, and the dependency delta is limited to the receipt move and its callers.

- The checked source tree is . Command statuses and logs are in .
