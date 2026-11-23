# `compiler/render/aql` cleanup plan

Status: Phase 0 and Phase 1 done. Phases 2–4 pending. Original measurements at `bb50db30b` on `internal-cleanup`.
Relation to other plans: this follows the `render/aql` item under "Proposed: IR, AQL and
execution receiver ownership" in `docs/INTERNAL_CLEANUP_PLAN.md`. That item asked for
per-family renderers sharing a `renderContext`. The renderers exist, but they did not separate
anything (Finding 3). `docs/EXECUTION_REFACTOR_PLAN.md` Phase 2 deletes `exclusion.go` and the
exclusion fields here. Start this plan after that phase is merged.

## Verdict

Keep one package. The renderer is a single recursive family (operation, expression, selector,
predicate), and a package split would cut through that recursion. The real cost is reader load,
from four sources:

- file names that do not say what the files hold;
- a public API that encodes option combinations in function names;
- two parallel flag structs;
- sub-renderers that are only views of the core renderer.

| Measure | Value |
| --- | --- |
| Source | 60 files, 15,321 lines. Largest is `render.go` at 814 |
| Tests | 4,064 lines |
| Functions | 351. Methods on renderer types: 144, of which 80 are on `physicalPlanRenderer` |
| Exported entry points | 20 functions at the original base. The 31 production call sites in 15 compiler files include 7 specialized render calls that stay separate. |
| Unused unexported functions | none found (grep heuristic) |

### Findings

1. **File names do not track content.** The ban on underscore filenames was applied
   mechanically, and it produced synonym groups:
   - `group` / `grouped` / `grouping`, `pivot` / `pivots` / `pivoting`,
     `lineage` / `lineages`, `values` / `valued`, `collection` / `collections`;
   - `streaming` / `streamed`, `selector` / `selectors`, `dynamic` / `dynamism`,
     `expand` / `expansion`, `occurrence` / `occurrences`, `graph` / `graphing`,
     `navigation` / `navigator`, `keys` / `keying`, `reshape` / `reshaping`,
     `coding` / `codes` / `coded`.

   Measured examples:
   - `values.go` holds `renderExpression` and `renderLiteral`;
   - `collections.go` holds pivot and aggregate rendering;
   - `pivots.go` holds pivot row lineage;
   - `pivot.go`, `lineages.go`, `navigator.go` and `reshaping.go` are 5-line files with one
     declaration each.
2. **Options are encoded as function names.** At the original base there were 11 `RenderPhysicalPlanWith*` variants,
   including `...WithStreamingMissingCategoryPivotPreviewAndRootIndexHint`. Each one checks its
   preconditions and then fills a private `physicalRenderOptions`. Two of the 20 entry points
   have no production caller: `RenderClickHousePrefix` and
   `RenderPhysicalPlanWithTerminalProjection`. `RenderRelatedEligibilityCategoryOverflowWitness`
   is exported but has only an in-package caller.
3. **The sub-renderers do not separate anything.** `rowLineageRenderer`, `cellTraceRenderer`,
   `tableShapeRenderer` and `navigationRenderer` each hold `*renderContext` plus
   `core *physicalPlanRenderer`, and each is built per call by a one-line accessor. Their files
   call back through `core.` 355 times (`lineage.go` 77, `reshape.go` 53,
   `trace.go` 41). The second hop (`r.core.tableShape().renderGroupedPivotIdentity`) adds
   indirection and no boundary.
4. **Two parallel flag structs.** `physicalRenderOptions` (`render.go:293`, 22 fields) and
   `renderContext` (`render.go:710`) repeat most preview fields. Five preview modes are
   independent bools or pointers: two-scan pivot, dynamic pivot, streaming missing-category
   pivot, semantic group projection, and root-page keys. The types do not say which
   combinations are legal.

## Target design

- **One public entry point:** `Render(plan, Options)`.
  - `Options` holds the root index choice (default, hint, disabled) and one `Preview` value.
  - `Preview` is a sealed type with one variant per preview mode. Each variant carries its own
    parameters.
  - Preconditions move into each variant's validation.
  - `RenderCategoryScan`, `RenderRelatedCategoryScan`, the lineage renderers and
    `RenderPhysicalRootScopeCount` stay separate functions. They render different terminals.
- **`renderContext` is built from `Options` once.** The duplicate flags are gone, and preview
  state is the `Preview` value itself.
- **Sub-renderers become plain methods on `physicalPlanRenderer`,** grouped by file. Delete the
  `core` back-pointer and the accessors.
- **Files are named by what they render:** `expression.go`, `selector.go`, `predicate.go`,
  `sequence.go`, `group.go`, `pivot.go`, `unpivot.go`, `related.go`, `lineage.go`, `trace.go`,
  `category.go`, `count.go`, `preview.go`, `output.go`, `bindvars.go`, `validation.go`, and
  similar.
  - One word per concept, no synonyms.
  - A file over about 1,200 lines gets a second distinct noun, for example `pivotlineage.go`.

Rendered AQL text and bind variables stay byte-identical in every phase.

## The check: a render snapshot

The package's own tests cover a fraction of rendered shapes. Most coverage is in `compiler`,
`execution` and the Explorer suites. Before Phase 1, build a snapshot lever:

- Add temporary instrumentation in the worktree, never committed. When `LOOM_AQL_SNAPSHOT=<dir>`
  is set, the shared render exit writes one record per render:
  - the entry point name;
  - a digest of the input plan;
  - the query text;
  - the bind variables as sorted JSON.
- Run `go test` for `./internal/dataframe/...` and `./internal/explorer/...` at the base commit
  and save the snapshot. After each phase, rerun and diff.
- Keep the script that applies and removes the instrumentation. Its output must be identical
  except for records from deleted entry points.
- If instrumentation shows non-deterministic output on the base commit, fix the recorder (sort
  keys, drop pointers). Never relax the comparison.

## Phases

Each phase is one local commit. It passes the snapshot diff plus its own check before the next
phase starts.

### Phase 1: delete and unexport

- Delete `RenderClickHousePrefix` and `RenderPhysicalPlanWithTerminalProjection`. Move their
  tests to the path they exercise, or delete them when they cover nothing else.
- Unexport `RenderRelatedEligibilityCategoryOverflowWitness`.
- Check: `go build ./...`, `make test-go`.

Phase 1 passed the Go build, Go suite, comment lint and the disposable Arango
reverse-count oracle. The snapshot removed the two retired entry-point records and
matched the other 8,308. The lowerer prefix test retains its rendered query and scope
checks. The terminal-projection-only CODED_GROUP test was removed, and the reverse
count equivalence test moved to the renderer package with its assertions intact.

### Phase 2: rename files by content (move only)

- Write the target file map into this plan first: each current file, its declarations, and its
  destination file.
- Move declarations with `scripts/filenames` (or a small `gofmt`-safe script) and change
  nothing else.
- Check: `go build ./...`, `make test-go`, snapshot identical, and
  `git diff -M --stat` shows only moves and merges.

### Phase 3: flatten the sub-renderers

- Turn `rowLineageRenderer`, `cellTraceRenderer`, `tableShapeRenderer` and `navigationRenderer`
  methods into `physicalPlanRenderer` methods.
- Delete the accessor files and every `core.` hop.
- Rename only where two methods collide, and record each rename here.
- Check: `go build ./...`, `make test-go`, snapshot identical.

### Phase 4: one entry point with a typed preview

- First record in this plan which preview modes can combine today. Read each wrapper's
  preconditions and every caller.
- Add `Options` and `Preview`. Migrate the generic renderer callers in the same commit, and
  delete the remaining 10 wrappers and the duplicated `renderContext` flags.
- Do not keep a compatibility wrapper.
- Check: `go build ./...`, `make test-go`, `make test-integration`, snapshot identical. Run
  `pstack:interrogate` on the diff, focused on preview preconditions that might be lost or
  weakened.

## Out of scope

- A child-package split. The recursion spans every family.
- Changing any rendered AQL, even to simplify it.
- `compiler`'s use of these entry points beyond the call-site migration in Phase 4.
- `render/clickhouse`, `aqlsyntax`.

## Expected result

Line count drops a little (inferred). Phase 1 removes two entry points, Phase 3 removes the
accessors and wrapper types, and Phase 4 removes the remaining 10 wrappers and the duplicate flags. The gains
are these: a reader finds a renderer by its file name; there is one way to call the renderer;
and an illegal preview combination fails to compile.

## Verification and review

- Never edit assertions in tests under `render/aql`, `compiler`, `compiler/*` or `execution`.
  If an assertion needs to change, stop and report it.
- Every phase: `go build ./...`, `make test-go`, `make lint-go-comments`, snapshot diff.
- Record timeouts and tool errors as failures.

## Risks

- **Snapshot coverage is only as good as the test suites.** Preview modes with one caller each
  may get few records. The Phase 4 review reads each mode's preconditions against its old
  wrapper.
- **Phase 3 can surface name collisions** between sub-renderer methods and core methods. Any
  rename has to keep call sites resolving to the same function, which the compiler checks.
- **Phase 2 touches every file in the package.** Other work in `render/aql` must wait or be
  rebased onto it, so land it quickly after it starts.
