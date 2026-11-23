# `dataframe/compiler` root package cleanup plan

Status: Phases 0 through 2 complete. Phase 3 awaits the fast-path audit.
Baseline measured after lineage merge `136ba0894`.
Scope: the root package `internal/dataframe/compiler` only. Its subpackages have their own plans
(`AQL_RENDER_REFACTOR_PLAN.md`, `AQL_FAST_PATH_AND_LINEAGE_PLAN.md`,
`COMPILER_REDESIGN_PLAN.md`) or are out of scope.

## Verdict

The root package's source is not the problem. It is 5,005 lines with no unused private
functions (grep heuristic). The problems are its role and its tests:

- **Its role is inconsistent.** Root is the "compile an output into a query" layer. Three
  query purposes (cell trace, row lineage, population mapping) live in their own subpackages.
  Two others (category scan, column population) live in root, next to the core output
  compiler.
- **Its tests belong to other packages.** Root has 31,220 lines of tests, six times its source,
  in 69 files with 361 tests and 342 helper functions. At least 30 of those files never call a
  root function. They test `lower`, `render/aql`, `celltrace`, `rowlineage` and `population`
  through shared helpers. So a package's own test run does not cover that package, and an
  assertion about `lower` lives in `compiler`.
- **Each purpose repeats the finishing steps,** and some of those steps are redundant.

From scratch, root would hold the core output compiler (stream, preview, page) and the shared
index-spec helpers. Each other query purpose would be its own subpackage. Each package's tests
would live with it, with fixture builders in `internal/testkit`.

| Measure | Value |
| --- | --- |
| Source | 18 files, 5,005 lines. Largest: `paging.go` 953, `covering.go` 622, `execution.go` 589, `scan.go` 526 |
| Tests | 69 files, 31,220 lines, 362 test declarations, and 337 helpers |
| Production importers | `execution` (6 files), `api/dataframe/query` (2), `compiler/facade`, `cmd/dataframe-profile` |
| Unused private functions | none found |

## Findings

1. **Dead code and pass-through wrappers.**
   - `api.go` contains only `package compiler`.
   - `diagnostics.go` holds three one-line wrappers around `ir` functions:
     `normalizeDatasetGeneration`, `physicalScopeWindowEnd` and `PhysicalPlanDiagnostics`.
     `population/mapping.go` already calls `ir.PhysicalPlanDiagnostics` directly.
   - `PublicOutputColumns` has no caller.
   - `AppendUniqueProjection`, `WithGenericPhysicalExecutionWindow`, and
     `CategoryScanRefusalCode` have no external explicit references.
     `CategoryOverflowWitness` is also constructed in execution tests.
   - `exclusions.go` (183 lines) is deleted by the AQL plan's Phase 0.
2. **Category scan and column population are separate purposes living in root.**
   - Category scan spans `scan.go`, `proof.go`, `pushdown.go`, `indexed.go` and `related.go`,
     about 1.6k lines. Column population is `population.go`, 122 lines.
   - The dependency runs one way. Scan code calls 10 preview helpers: covering-index naming
     and specs (`previewCoveringIndexName*`, `previewQuantityCategory*`,
     `validPreviewIndexPath`), `rootPageInsertionIndex` and
     `WithGenericPhysicalExecutionWindow`. Preview code calls nothing in scan (measured). The
     split has no cycle.
3. **Category scan has four entry points for a two-by-two choice.**
   `CompileCategoryScanOutputWithPolicy`, `CompileCategoryScanOutputRequiringValue`,
   `CompileCategoryScanStageWithPolicy` and `CompileCategoryScanStageRequiringValue` (`scan.go:97–126`)
   combine an output-column or stage-column target with whether a value is required.
4. **Redundant validation before render.** `aql` rendering already runs `plan.Validate()` and
   `ir.ValidateGenericPhysicalPlanScope` (`render/aql/render.go:354–380`). Some callers run both
   again immediately before rendering the same plan:
   - `celltrace/compile.go:100–106`;
   - `rowlineage/compile.go:262–268`;
   - `population/mapping.go:100–103`.
5. **Tests live in the wrong package.** These root test files never call a root function:
   `bind`, `columns`, `compat`, `construction`, `count`, `expansion`, `extension`, `frontier`,
   `grain`, `guard`, `lineage`, `lowering`, `mapping`, `match`, `name`, `optimize`, `oracles`,
   `physical`, `projection`, `render`, `scope`, `semantics`, `summary`, `trace`, `typed`,
   `unpivot`, `validation` and `version` (plus `helpers`, which holds shared helpers).
   - `lineage_test.go` makes 46 calls into the purpose subpackages and `trace_test.go` makes 27.
   - `physical_test.go` makes 16 `lower` calls and 14 `aql` calls.
   - `scripts/run-arango-integration.mjs` names required tests per package, so moving a test
     changes the runner's requirements.
   - **Why: the package splits moved code and left its tests behind.** Commit `3bf2e2b5b`
     (filename boundary wave 2b) renamed `compiler/cell_trace.go` to `celltrace/compile.go`.
     In the same commit it rewrote the root `cell_trace_test.go` (58 lines changed) to call the
     new package instead of moving it. Wave 2a (`40c1ad2ee`) did the same for `lower`. The
     orphans are a side effect of those moves, not a design choice.

## Target layout

```
compiler/          core output compilation: CompileResolvedRecipePlanWithPolicy,
                   CompileRecipeOutputWithPolicy, CompileRecipeOutputPageWithPolicy, graph query,
                   liveness, paging, preview filters
compiler/model     + covering-index naming and spec helpers (model already owns
                   PreviewCoveringIndexSpec), shared by root and scan
compiler/scan      category scans and column population; one Compile entry point per scan kind,
                   with a typed target
compiler/celltrace, rowlineage, population   unchanged, minus the redundant pre-render checks
internal/testkit/compilerfixtures            shared recipe, plan and Arango fixture builders
```

Each test lives in the package whose behavior it pins.

## Phases

Each phase is one local commit with its own check. The AQL refactor is
already in this branch's base. The root uses `aql.Render(plan, Options)`, and
`exclusions.go` is already gone. The fast-path audit's A4 can delete preview
code in `covering.go`, `projection.go` and `execution.go`; scan extraction still
depends on that decision.

### Phase 0: inventory (read-only, can start now)

- **Test map, built from history first.** For each production file that moved out of root
  (`git log --follow -M --name-status` over waves 2a, 2b and the earlier package splits), find
  the root test files changed in the same commit to call the new package. Those tests belong to
  that package. Commit the script that builds this map. Read by hand only the files the history
  leaves ambiguous. For each file, record the owning package, the helpers it uses, and any
  unexported root symbols those helpers need.
- **Deletion candidates, with evidence.** A test is deleted only in these cases, never because it
  is orphaned:
  - it pins code that has been deleted (table-shape exclusions, or fast paths removed by A4);
  - it asserts the same behavior as a test in the owning package, shown by both tests failing on
    the same injected fault or by identical assertions on identical inputs.
- **Helper map.** For each of the 342 helpers, record whether it builds fixtures (moves to
  testkit), asserts behavior (moves with its test), or is unused (delete).
- **Category scan.** The exact difference between the four entry points, and every caller.
- **Runner.** The required-test entries in `scripts/run-arango-integration.mjs` that name root
  tests.
- Write all of this into this plan.

#### Inventory results

The branch starts at research merge `6c2f008fb`. The authorized lineage merge
`136ba0894` added `agreement_test.go` and its three required integration
registrations. The compiler source and integration runner at this merge match
the preliminary inventory snapshot `3e3d81176`.

- [History mapper](../scripts/research/map_compiler_test_history.py) uses
  rename detection and follows individual file histories. It found 27
  root-to-subpackage production renames and 104 same-commit correlations
  across 43 historical root test paths. It identifies one historical
  ownership candidate for 17 current files and flags the other 52. A same-commit import is a lead for
  ownership. In particular, imports of `model` often only supply fixtures.
  Wave `3bf2e2b5b` moved nine production files. Wave `40c1ad2ee` renamed
  files inside their respective directories. Earlier splits also appear in
  `d16dbaa` and `a11b8ef`.
- [Helper inventory](COMPILER_ROOT_HELPERS.tsv) lists 126 fixture builders and
  211 assertion or utility helpers. All 337 have a use. The scan helpers
  `appendPresenceAuditPivot`, `eligibleCategoryColumns`, and
  `assertCategoryScanLimitFollowsStreamedWitnesses` use private scan symbols.
  Move them with the scan code to preserve access.
- The four category entry points vary by final output schema versus explicit
  stage and whether a value is required. Output without a value requirement
  supplies no value column. Output with it resolves both names in
  `OutputSchema` and rejects identical IDs. Both stage calls select and
  truncate to the requested stage and validate both column IDs, including
  when value filtering is disabled. Execution dispatches among all four.
  The facade aliases the stage call that requires a value.
- No tests qualify for deletion. Injecting an error into
  `lower.LowerCorrelatedBindingWithVersion` failed both
  `TestCorrelatedPhysicalScalarSelectorAQLFingerprint` and
  `TestLowerCorrelatedBindingPreservesCodingValueScope`. Both passed before
  the injection. The root assertion also pins emitted AQL, so the overlap
  does not justify deleting it. The injected fault was restored in its
  isolated copy. `exclusions_test.go` covers live construction behavior.

The [ownership inventory](COMPILER_ROOT_TEST_OWNERS.tsv) covers all 69 current
test files. The 52 files without one history owner were checked by subject.
Seventeen files also have a single historical candidate. Where an import only
supplies a fixture type, the assertions determine ownership. Whole-file
moves must account for mixed files. In `agreement_test.go`, the Pivot test
checks the forward compiler, row lineage and cell trace together. The Group
test checks row lineage and the filtered-source test checks cell trace.

The research merge exposed two calls to the retired AQL renderer in
`internal/research/compilerprobe/acceptance_test.go`. The baseline test suite
and its vet gate failed. The two calls now use `aql.Render` with default
options. The focused research test, `go build ./...`, `make test-go`, and
`make lint-go-comments` passed after the correction. The baseline lists
1,253 distinct test names under `internal/dataframe`.

The integration runner requires these names in root before the moves. Its
additional `render/aql` case, `TestScalarSelectorArrayMatchesLegacyAgainstArango`,
already has its own package registration.

| Required integration test | Root file |
| --- | --- |
| `TestPivotPresenceArangoAudit` | `scan_test.go` |
| `TestCategoryPairProjectionPreservesPresenceScopeAndOverflowAgainstArango` | `pair_test.go` |
| `TestColumnPopulationMatchesExactArangoStageRows` | `population_test.go` |
| `TestNestedExtensionColumnsKeepAncestorOwnersAgainstArango` | `extension_test.go` |
| `TestCurrentPivotNullMissingPartitionAgainstArango` | `exclusions_test.go` |
| `TestPivotLineagePartitionsForwardRowsAndTraceReconstructsAgainstArango` | `agreement_test.go` |
| `TestGroupRowLineagePartitionsForwardRowsAgainstArango` | `agreement_test.go` |
| `TestFilteredSourceCellTraceMatchesForwardValueAgainstArango` | `agreement_test.go` |
| `TestS04TableReshapeOracleAgainstArango` | `pivot_test.go` |
| `TestS04GroupedPivotMaterializationPreservesAuthResourcePathAgainstArango` | `pivot_test.go` |
| `TestGroupedPivotReservedPhysicalOutputNameAgainstArango` | `name_test.go` |
| `TestRootCategoryScanUsesSharedTypedIndexContractAgainstArango` | `explain_test.go` |
| `TestRelatedCategoryScanUsesTypedIndexAgainstArango` | `explain_test.go` |
| `TestRelatedStreamingCategoryScanUsesEdgeIndexesAgainstArango` | `explain_test.go` |

### Phase 1: delete and inline (after the AQL plan's Phase 4 merges)

- Delete `api.go`, `PublicOutputColumns` and the three wrappers in `diagnostics.go`. Callers use
  `ir` directly.
- Unexport the four internal-only exports.
- Remove the redundant `Validate` and scope checks in `celltrace`, `rowlineage` and
  `population/mapping`, but only where the same plan goes straight to rendering. If a test
  asserts the wrapped error text, keep that check and record why.
- Check: `go build ./...`, `make test-go`, `make lint-go-comments`.

Phase 1 removed the empty package file, the unused public column wrapper, and
the diagnostic forwarding file. Same-package helpers now use private names.
The overflow witness stays exported for the two execution test fixtures.
Cell trace and row lineage now rely on renderer checks for plans they pass
straight to the renderer. No test asserted their old wrapper strings.
Population keeps both checks in `MappingPhysicalPlan` because it returns a
validated plan directly and the compile path transforms that plan before
rendering.

`go build ./...`, `make test-go`, and `make lint-go-comments` passed.

### Phase 2: move tests to their owners (after the AQL plan's Phase 4 merges)

- Move fixture builders into `internal/testkit/compilerfixtures`, following the existing
  `recipefixtures` and `constructionfixtures` pattern. Export only what the moved tests need.
- Move each test file whose subject is another package into that package. Test bodies do not
  change; only imports and helper qualifiers change. Do it with a script, and commit the script.
- Update `scripts/run-arango-integration.mjs` so every required test points at its new package.
- Check:
  - the set of test names is identical before and after (`go test -list . ./internal/dataframe/...`,
    compared as sorted sets);
  - every moved test passes in its new package;
  - `make test-go` and `make test-integration` pass;
  - no assertion lines changed (`git diff` filtered to lines containing `t.Fatal`, `t.Error` or
    `want`).

[The migration script](../scripts/compilercleanup/move_tests.py) moves complete
Go declarations. Run it from the Phase 1 snapshot with `goimports` installed.
It moved 177 tests to their owners and preserved 185 root tests, including
cross-package agreement cases. No test was deleted. Shared builders live in
`internal/testkit/compilerfixtures`. An injected compile function keeps those
fixtures independent of the root compiler package. Test aliases preserve
call sites and assertions. The typed category terminal test uses the
renderer package's existing private bind name and drops its local package
qualifier.

`go test -list . ./internal/dataframe/...` listed the same 1,253 names.
A declaration comparison matched all 1,253 bodies after normalizing the one
renderer qualifier. Git diff on the extracted assertion lines was empty for
all 7,586 lines. This comparison pairs split-file declarations by test name,
so relocated lines are compared in their new files.

Three independent same-model review passes checked assertion preservation,
runner registration and repeatability of the generated move. All prior
required witnesses remain registered in their destination packages. The
runner now explicitly verifies the relocated population cases, too. No
confirmed review findings remained. The initial code generation attempts
exposed a renderer test import cycle and a mistaken alias of a method as a
package function. Splitting that renderer file along its package dependency
boundary and restricting aliases to free functions resolved both.

Phase 2 passed the Go build, full Go suite, comment lint, runner unit tests,
and `make test-integration` against disposable Arango and ClickHouse.
The integration runner recorded 40 setup/test profiles, no failures, and an
unchanged source fingerprint. It explicitly marked six external-data cases
unverified. All required tests passed.

| Phase | Root source lines before → after | Root test lines before → after |
| --- | ---: | ---: |
| 0 | 5,005 → 5,005 | 31,220 → 31,220 |
| 1 | 5,005 → 4,987 | 31,220 → 31,220 |
| 2 | 4,987 → 4,987 | 31,220 → 21,052 |

The recipeplan-only cases remain in root under the write restriction on
`dataframe/recipeplan`. The scan-owned cases move with their private scan
helpers in Phase 3.

### Phase 3: extract `compiler/scan` (after the fast-path audit's A4 lands)

Blocked pending a decision to resume the paused fast-path audit. Track A
has its inventory and the isolated CDA load. A1, A3 and A4 remain pending,
and the operator previously directed the audit to pause after AQL Phase 4.

- Move the covering-index naming and spec helpers that scan uses into `compiler/model`.
- Move category scan and column population into `compiler/scan`.
- Replace the four category-scan entry points with one `Compile(output, target, maxValues,
  policy)`. The target is a sealed type, either an output column or a stage column, and carries
  whether a value is required. Migrate callers in `execution` and `compiler/facade` in the same
  commit, with no compatibility wrapper.
- Move the scan tests with the code.
- Run `python3 scripts/package_audit_verify.py` on `compiler`, `compiler/scan` and
  `compiler/model` before and after, then its `--full` mode.
- Check: `make test-go`, `make test-integration`. The render snapshot from the AQL plan is
  identical for every scan query.

## Out of scope

- Splitting the core output compiler (preview, page and stream share liveness and paging).
- Renaming the `WithPolicy` suffixes. Every entry point takes a policy, so the suffix is noise,
  but renaming it is churn with no structural gain.
- `compiler/facade`. It is a deliberate import boundary for Explorer. Its aliases are updated,
  not removed.
- Lowering and recipe model changes, which belong to `COMPILER_REDESIGN_PLAN.md`.

## Expected result

- Root drops to about 3k lines (inferred): 5k, minus about 1.75k in scan, minus dead code and whatever A4 deletes.
- Root's tests drop from 31k lines to only those that pin root behavior.
- Each package's tests cover that package.
- Category scan has one entry point.
- No query purpose validates the same plan twice.

## Verification and review

- Never edit assertions while moving tests. If a test only passes after an assertion change, it
  was depending on something else. Stop and report it.
- Every phase: `go build ./...`, `make test-go`, `make lint-go-comments`.
- Phases 2 and 3: `make test-integration`, and the sorted test-name comparison.
- Phase 3: an adversarial review of the scan extraction, focused on helpers that changed
  package and callers of the new typed target.
- Record timeouts and tool errors as failures.

## Risks

- **Test helpers may reach into root's unexported state.** Phase 0 finds out. Export only what a
  moved test needs, or leave that test in root with the reason recorded.
- **Moving Arango tests changes runner requirements.** A test that silently stops running is
  worse than one in the wrong package. That is why the test-name comparison and the runner
  update are part of the check.
- **Phase 3 depends on A4.** The extraction and fast-path deletions share preview helpers.
