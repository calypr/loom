# `dataframe/execution` cleanup plan

Status: in progress on `execution-cleanup`, based on `bb50db30b`.
Original findings measured at `889f8aa30` on `internal-cleanup`.

| Phase | Status | Verification |
| --- | --- | --- |
| 1 | Done | Declaration-removal compile probe, `go build ./...`, execution tests, `make test-go`, and `make lint-go-comments` passed. |
| 2 | Skipped | The integration runner requires the exclusion-only `TestCurrentPivotNullMissingExclusionPartitionAgainstArango` to run and pass. Updating `scripts/run-arango-integration.mjs` is outside scope. |
| 3 | Done | `go build ./...`, execution tests (including unchanged cache and scanner tests), `make test-go`, and `make lint-go-comments` passed. |
| 4 | Done | `go build ./...`, `make test-go`, and `make lint-go-comments` passed. `git diff -M --stat` recognized the move. All 57 Go declarations matched the previous source. |
| 5 | Pending | |

Phase 1 tests kept their invariants and moved to the underlying Engine calls. No tests were
deleted. The initial compile probe hit Go cache permissions and then a reference inside the
dead declaration set; the corrected probe passed. The first full suite could not open
loopback listeners in the sandbox. Its unsandboxed retry found a staged-file dependency in
the repository gofmt gate. The full suite passed with local sockets and the deletion staged.
Relation to other plans: this carries out the `execution` item under "Proposed: IR, AQL and
execution receiver ownership" in `docs/INTERNAL_CLEANUP_PLAN.md`. That plan rated
`execution/arango`, `execution/chartifact` and `chartifact/arango` FINE. They stay out of scope.

## Verdict

Keep one package. The feature files (category, cell trace, lineage, population mapping) already
line up with the compiler's feature files, and that alignment is worth keeping. The size comes
from four problems: an orphaned feature, public entry points that only tests call, executors
rebuilt from copied fields on every call, and an `OutputStream` that holds two backends in one
struct.

| Measure | Value |
| --- | --- |
| Source | 21 files, 5,226 lines. `engine.go` is 1,304 |
| Tests | 8,375 lines |
| Production importers | 18 packages, mostly `explorer/*` |
| `Engine` | 18 private fields copied one-to-one from `Config`, plus the preview cache |
| `OutputStream` | 37 fields. About half apply only to Arango or only to ClickHouse |

### Findings

1. **Orphaned feature: table-shape exclusions.** Commit `83d70d30a` (2026-11-17, #160) removed
   the last production caller, the Explorer table-shape comparison. What it left behind has no
   production caller and no API or UI reference (measured with rg):
   - `execution/exclusion.go` and `exclusions.go` (193 lines);
   - `compiler/exclusions.go` (183);
   - `compiler/render/aql/exclusion.go` (156);
   - the exclusion IR nodes (41 references in `compiler/ir`, plus generated `children.gen.go`).
2. **Public entry points with no production caller.**
   - `MaterializeBundle`, `MaterializeVersion`, `PreviewBundle` and `PreviewResolvedBundle`.
     `ResolveVersion` is called only by `MaterializeVersion`.
   - Exported wrappers that only in-package tests call: `CellTraceCompiled`,
     `PopulationMappingCompiled` and `OutputStream.MaterializeClickHouseArtifact`.
   - `Validation`, `Preview` and `OutputRows` in `validation.go`, plus `PhysicalExplanation` and
     `PhysicalOutputExplanation` in `explain.go`. These exist only to serve `Control`, a facade
     defined in `helpers_test.go`.
   - `min` in `population.go` shadows the Go builtin.
3. **Executors rebuilt on every call.** `New` copies `Config` into private fields.
   `outputStreams()` (`emitter.go:7`) and `chooseGroupPreviewScan` (`scanner.go:9`) then copy
   those fields again into a `streamExecutor` or `groupPreviewPlanner` on each call. This is the
   same pattern lifecycle Phase 2 removed.
4. **`OutputStream` holds two backends.** `streamRaw` (`engine.go:819`) branches on
   `physicalEngine`. Arango-only state includes the query, bind variables, page, root-page rows
   and read snapshot. ClickHouse-only state includes the combine plan, ClickHouse query and input
   ports, read pins, and the artifact capture and scope fields. Every stream also carries copies
   of the engine's ports.
5. **Duplicated row pipeline.** `Stream` (`engine.go:776`) and `streamPreparedClickHouse`
   (`engine.go:796`) repeat the same steps: post-query materialization, stable row identity and
   the public row projection.
6. **`engine.go` mixes three jobs:**
   - recipe resolution and materialization (lines 1–465);
   - ClickHouse stream building and preparation (465–760 and 890–1165), including the
     200-line `withPreparedClickHouse`;
   - Arango streaming and row helpers.

## Target design

- `Engine` builds its stream executor, preview executor and group-preview planner once in `New`
  and holds them. It no longer copies ports field by field.
- `OutputStream` keeps its public fields (`Name`, `Columns`, `RowIdentity`, `DynamicChecks`)
  and one row pipeline.
- The backend sits behind a private `rowSource` with two implementations, `arangoSource` and
  `clickHouseSource`. Each holds only its own state and ports.
- Files are split by job: `resolve.go`, `arango_stream.go`, `clickhouse_stream.go`,
  `output_stream.go`.

Public behavior does not change: rows, column order, row identities, error codes, preview
summaries and artifact identities stay the same.

## Phases

Each phase is one local commit. It must pass its check before the next phase starts.

### Phase 1: delete dead entry points

- Delete the methods and types in Finding 2. Confirm each with `go build ./...` before
  deleting it.
- Delete `Control`. Rewrite its tests to call `Engine` methods directly. If a test needs a
  helper that combines several engine calls, keep it unexported in the test file.
- Tests of the deleted exported wrappers call the unexported method instead. Keep their
  assertions.
- Check: `go test ./internal/dataframe/execution/...`, `make test-go`, `make lint-go-comments`.

### Phase 2: delete table-shape exclusions

- Delete the chain in Finding 1, in this order:
  1. execution;
  2. compiler;
  3. `render/aql`;
  4. the IR nodes, then rerun `go generate` in `compiler/ir`.
- Delete their tests.
- Before deleting, search `docs/BACKLOG.md`, `api/openapi.yaml` and `ui/packages/loom-ui/src`
  for the `TABLE_SHAPE_EXCLUSION_*` codes. If anything outside Go references them, stop.
- Check: `go test ./internal/dataframe/...`, `make test-go`, `make test-integration`. Run
  `python3 scripts/package_audit_verify.py` on `compiler`, `compiler/ir` and `render/aql` before
  and after, then `--full`.

### Phase 3: build executors once

- `New` builds the stream executor, preview executor and group-preview planner, and `Engine`
  stores them.
- Delete `outputStreams()`, `scanner.go` and the private port fields that only fed those
  copies.
- Keep the group-preview cache as one shared instance with the same bounds.
- Check: `make test-go`. The cache tests in `cache_test.go` and `scanner_test.go` pass without
  assertion edits.

### Phase 4: split `engine.go` by job (move only)

- Move code into `resolve.go`, `clickhouse_stream.go` and `output_stream.go`. Do not change it.
- Check: `go build ./...` and `make test-go`. `git diff --stat -M` shows moves only.

### Phase 5: one row pipeline, two backends

- First, record in this file which `OutputStream` fields each backend path reads and writes.
  The group-preview planner mutates the stream through `*OutputStream`, so list those writes.
- Add `rowSource` with `arangoSource` and `clickHouseSource`. Move each backend's fields into
  its source.
- Merge `Stream` and `streamPreparedClickHouse` into one pipeline over the source.
- Migrate the 9 `OutputStream{...}` literals in tests to constructors or sources. Keep their
  assertions.
- Check: `make test-go`, `make test-integration`. Run `pstack:interrogate` on the diff, focused
  on the planner's stream mutations and on artifact capture cleanup.

## Out of scope

- `Service` in `service.go`. It runs ad-hoc compiled graph queries for `api/dataframe/query` and
  has a separate contract.
- Splitting features into child packages. A child package would need the engine's query and
  access state, and the cross-layer alignment with the compiler is deliberate.
- `execution/arango`, `execution/chartifact`, `chartifact/arango`.

## Expected result

Phases 1 and 2 remove 650 to 750 production lines (inferred from the file sizes above), about
200 of them in `execution`, plus their tests. Phase 3 removes the duplicated port fields.
Phase 5 removes one copy of the row pipeline. `engine.go` is no longer a single 1,300-line
file.

## Verification and review

- Never edit assertions in execution tests or in `explorer/receipt`, `explorer/construction` or
  `api/dataframe/query` tests. If a needed assertion edit appears, stop and report it.
- Every phase: `go build ./...`, `make test-go`, `make lint-go-comments`.
- Record timeouts and tool errors as failures.

## Risks

- The group-preview planner chooses query shapes by mutating the stream. Phase 5 can break that
  without failing a fast test. The Phase 5 inventory and review exist to catch it.
- Private ClickHouse artifact capture has lease and cleanup ordering (`artifact.go:223`). Moving
  its fields must keep cleanup on cancellation and consumer error. The
  `TestWithPrivateClickHouseArtifacts*` tests cover this.
- Phase 2 edits generated IR code. Regenerate it. Never hand-edit `children.gen.go`.
