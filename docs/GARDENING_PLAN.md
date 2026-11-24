# Gardening plan

Status: proposed. Base: `internal-cleanup` at `f3c878aec`. Executor: OpenCode with pstack agents on the
DeepSeek endpoint (4×H200).

The goal is traditional clean-code gardening across `internal/` and `ui/`. That means judging every interface,
file, package responsibility, struct and function, then deleting, merging, moving and creating packages
until the tree reads like the domain. The earlier internal-cleanup phases mostly split files *within* packages.
This plan changes package boundaries.

## What the code looks like today (measured at `f3c878aec`)

These come from the AST scan of `internal/` (production code only), `go list`, and the file tree.

**Packages that do too much**

| Package | Prod files / lines | Test files | Signal |
| --- | ---: | ---: | --- |
| `explorer/lifecycle` | 72 / 23.7k | 84 | `Service`: 145 methods in 58 files; `Config` has 44 fields; 17 `construction_*` files, 6 `row_*`, 6 `interpretation_*`, 5 `population_*` |
| `explorer/authoringv2` | 47 / 15.8k | 51 | `Command`: 47 fields and 43 methods (a sum type flattened into one struct); 10 `construction_*`, 7 `stored_*`, 5 `source_*`, 5 `command_*` |
| `dataframe/compiler/render/aql` | 45 / 15.3k | 23 | `physicalPlanRenderer`: 80 methods in 22 files |
| `dataframe/compiler/ir` | 42 / 13.1k | 20 | 28 of 42 files are `physical_*`. The package is a physical plan, and its file names repeat that |
| `dataframe/compiler/lower` | 45 / 12.1k | 38 | 19 `recipe_*` files; `lowerConstructionGroup` 301 lines with 10 params |
| `dataframe/compiler` (root) | 28 / 7.6k | **95** | Grab bag of features (category, row lineage, cell trace, paging) with 31.7k test lines |
| `explorer` (root) | 27 / 5.0k | 21 | Grab bag plus `types.go`. Imported by almost every `explorer/*` package. `Store` interface has 19 methods |
| `api/explorer` | 25 / 3.4k | 60 | `Handlers`: 69 methods; 18k test lines |

**Concepts with no home**

| Concept | Prod files | Packages |
| --- | ---: | ---: |
| construction | 72 | 10 |
| semantic | 22 | 12 (`dataframe/semantic`, `fhir/semantic`, catalog `semantic_*`, lifecycle `semantic_catalog`, …) |
| workspace | 19 | 8 |
| population | 16 | 7 |
| lineage | 13 | 5 |
| receipt | 11 | 7 |
| selection | 9 | 7 |
| inventory | 9 | 6 |

**Wrong-direction dependencies** (7 edges)
- `dataframe/recipe/runtime` imports `explorer/arango`, `catalog`, `catalog/fieldprofile` and `api/authpolicy`.
- `dataframe/execution/arango` imports `api/http`.
- `ingest/sourceload` imports `explorer` and `explorer/projectconfig`.
- `explorer` also reaches into compiler internals: `compiler/lower` (7 importers), `compiler/ir` (2), `compiler/capability` (2).

**Struct and function shape**
- **Large receivers:** 7 with ≥25 methods.
  - Besides the four above: `execution.Engine` (34), `sourceload.RepositorySourceLoader` (34) and `explorer.Service` (30).
- **Fat structs:** 41 with ≥20 fields.
  - Examples: `CompilationReceipt` (37), `OutputStream` (37), `semanticCoverageStoredDocument` (42) and `SemanticCoverageCounts` (40).
- **Long functions:** 247 are ≥100 lines, 92 are ≥150, and 10 are ≥300.
  - The longest: `fileLoader.load` (428), `loadGeneration` (369), `recipeConstructionOperation` (346) and `BrowseSemanticInventory` (324).
- **Long parameter lists:** 72 functions take ≥8 parameters, up to 14 (`resolveSemanticConstructionChoice`).

**File-level mess**
- **Catch-all files:** 15 named `types.go` or `helpers.go`, in `catalog`, `explorer`, `fieldprofile`, `codedconcept`, `store/arango`, `execution` (both), `published`, `api/dataframe/query` (both), `authoringv2`, `lifecycle`, `lower` and `columntransform`.
- **Tiny packages that are merge candidates:**
  - `catalog/identity` (18 lines), `projectid` (40), `columntransform` (75), `recipe/exec` (76) with `recipe/exec/arango` (262), `lineage` (147), `lifecycle/ports` (142), `execution/arango` (208), `viewbuilder` (302) and `api/authpolicy` (91).
  - Eight `testkit/*` packages (~1.2k lines combined).
- **Whole packages stuttering the same prefix:** `ir/physical_*`, `lower/recipe_*`, `ingest/generation/generation_*`.
- **White-box tests:** 265 test files in the five largest packages are `package X` (white-box). That pins private names and is why the earlier phases refused package splits.

None of this is a verdict yet. Each row is a hypothesis for the audit to confirm or reject with file:line evidence.

## Shape of the work

```
Stage 0  tooling (small, serial)                      → scanners, oracles, move checker
Stage 1  AUDIT (wide, read-only, 15–25 agents)        → one structured report per package / concept / UI area
Stage 2  SYNTHESIS (coordinator + you)                → TARGET_TREE: delete / merge / move / new / interface list
Stage 3  EXECUTION (6–10 lanes, worktrees)            → deletes → test decoupling → moves → shapes → interfaces
Stage 4  acceptance
```

Stage 1 is where the GPUs pay off. It is read-only, so CPU cost is low and there are no write conflicts, which means
it can run as wide as the endpoint can serve. Stage 3 is the heavy moving. It is wide too, but writers need
ownership and serialized integration.

## Where it runs

Run OpenCode, the worktrees and the tests **on the H200 host**, not the Mac. The Mac has 10 CPUs, and five
parallel `go test` runs over these packages saturate it. Before Stage 3, check the host's `nproc`/RAM and set:
- concurrent Go test jobs ≈ `nproc / 8`, each with `-p 8`;
- a shared `GOCACHE`/`GOMODCACHE` (the Go build cache is concurrency-safe), with per-worktree log and output dirs;
- if RAM allows, one Arango + ClickHouse docker pair per execution lane on distinct ports
  (`LOOM_TEST_ARANGO_URL`, `LOOM_CLICKHOUSE_URL`). Lanes can then run `make test-integration` independently
  instead of queueing on one DB.

Runtime state lives in `.cache/garden/` (gitignored). The coordinator is the only writer of `.cache/garden/ledger.tsv`.

## Stage 0: tooling (coordinator or one worker; about half a day)

| Tool | Purpose |
| --- | --- |
| `scripts/gardenscan` (Go, `go/ast`) | Per package: receivers (method and file counts), interfaces (method count, implementations, consumers), structs (field count), functions (lines, params, bool/flag params), exported symbols with external caller counts, file-name prefix clusters, catch-all files. Emits TSV. A prototype is at `/private/tmp/claude-1520582618/-Users-peterkor-Desktop-BMEG-loom/a8175f1d-2b95-4103-bf63-604b21dc9d82/scratchpad/astscan/main.go`; extend it with implementations and callers using `golang.org/x/tools/go/packages` and `go/types`. |
| `deadcode` | Run `deadcode -filter=github.com/calypr/loom/internal ./...` with and without `-test`, using Go 1.27 on `PATH` (see INTERNAL_CLEANUP_PLAN Phase 0). The diff is "prod code only tests reach". |
| Layer check | Encode the allowed layering (below) as `scripts/check_layers.sh` over `go list -deps`, and add it to `make test-go`. It fails on today's 7 bad edges until Wave 3C fixes them; keep the known violations on an allowlist that only shrinks. |
| Oracles (out of Git) | (a) Render AQL and ClickHouse SQL for every recipe in `tests/conformance` and `tests/fixtures`, before and after each change. Output must be byte-identical. (b) Record the HTTP responses of the `api/explorer` and `api/dataframe` test suites. (c) Per-package statement coverage from `go test -coverprofile`. These catch behavior drift from moves cheaply. They are disposable evidence, not committed pins. |
| Move checker | Wrap `git diff -M --color-moved=zebra --color-moved-ws=allow-indentation-change`. It lists non-moved lines so that a "pure move" PR proves it is one. |

**Allowed layering** (lower layers never import higher ones; the Stage 2 synthesis confirms it):
`foundation (projectid, authscope, errors, fhir/*)` → `store/*` → `catalog`, `dataset` → `ingest` →
`dataframe` → `explorer` → `api/*` → `server`, `cmd/*`. Each domain's Arango adapter sits beside its domain and is imported only by `server` wiring.

## Stage 1: audit (read-only, wide)

**Units** (each is one `pstack-explorer` agent with one output file):

| Kind | Units |
| --- | --- |
| Large packages, split by file cluster so each agent reads under about 8k lines | lifecycle ×4 (construction / row+proposal / interpretation+population+selection / semantic catalog+rest), authoringv2 ×3 (command / construction / stored+source+rest), render/aql ×2, ir ×2, lower ×2, compiler root ×1, explorer root ×1, api/explorer ×1 |
| Medium packages, one each | explorer/{arango,receipt,compilation,capability,recipeimport,importedsource,projectconfig}, dataframe/{recipe,execution,published,publication(+arango,+clickhouse),semantic}, catalog(+arango,+fieldprofile,+semanticinventory,+codedconcept), ingest(+generation,+sourceload,+backfill), store/arango, fhir/schema, api/{dataframe,bulk/load,http}, server |
| Small-package sweep, one agent | every package under 400 lines, plus all `testkit/*` |
| Concept audits, one each | construction, semantic, workspace, population+selection, receipt+lineage. Each answers: where does the concept live today, what is its single owner, and which files move there |
| Cross-cutting, one each | interfaces repo-wide (define-at-consumer, single implementation, test-only fakes, ports); layering (the 7 edges plus explorer→compiler internals); white-box test coupling (which private names each test file uses and which proposed moves it would block); `cmd/` + `scripts/` + root (what is still used) |
| UI | `src/` root (`types.ts` 4.1k, `api.ts` 2k), `ExplorerBuilder/{constructionOperations,components}` (12k each), `ExplorerBuilder/{authoring,constructionWorkspace}` and the root (`BuilderWorkspace.tsx` 2.4k), `ExplorerViewer` + `ExplorerViewBuilder` |

That makes about 45 units. Run 15–25 at a time.

**Report schema** (`.cache/garden/audit/<unit>.md`, with file:line for every claim):

1. **Responsibility.** One sentence. If it takes "and", list the separate responsibilities and the files of each.
2. **Files.** For every file: the noun it owns, and whether it belongs here. Flag catch-all files and files whose main type lives elsewhere.
3. **Exported API.** Each exported symbol with its count of external production callers. 0 means unexport or delete; test-only means move into `_test.go` or delete.
4. **Interfaces.**
   - For each one: methods, implementations, consumers, and whether it is defined at the consumer or the producer.
   - Verdicts: keep (real port or seam), shrink to what the consumer uses, move to the consumer, or delete (single implementation with no seam need).
5. **Types.**
   - Structs with ≥15 fields: which fields are used together, which split falls out, and which are flattened sum types.
   - Receivers with ≥20 methods: how the methods group into concepts (by shared fields and call edges).
6. **Functions.** ≥150 lines, ≥7 params, bool or flag params, near-duplicates (same shape in two places). For each: the cut, or keep with a reason.
7. **Delete candidates.** Each with `deadcode`/`gopls references` evidence and any reflection, registration or generated-code caveat.
8. **Merge candidates.** Which package or file it merges into, and why.
9. **New or moved packages.** Proposed name, the files that go there, its imports, and a cycle check using `go list` on today's imports per file.
10. **Test coupling.** White-box test files that block items 8–9, with the private names they use.
11. **Confidence and open questions.**

Rules for auditors:
- Do not edit.
- Every claim gets file:line evidence, and every delete gets a search for callers.
- "Unused" needs `deadcode` plus `rg` plus a check for interface satisfaction, reflection, `init` registration and generated callers.

## Stage 2: synthesis (coordinator, then you)

The coordinator merges the audits into `docs/TARGET_TREE.md`:

1. **Package list.** Every target package with its one-sentence responsibility, main types, and allowed imports.
2. **File map** (`.cache/garden/filemap.tsv`). Each production file's current path, its target package, and the action (`stay | move | split | merge | delete`).
3. **Delete list**, with evidence links into the audits.
4. **Merge list**: tiny packages, duplicate concepts (for example the two `SemanticCoverage*` homes), catch-all files.
5. **New packages.** Expect homes for construction, physical plan, recipe lowering, authoring command/stored model, lifecycle use cases, a compiler facade for `explorer`, and ports for the 7 inverted edges.
6. **Interface changes.** Shrink `explorer.Store` (19 methods), split `Command` into variants, and move interfaces to their consumers.
7. **Renames**, folded into the moves since the paths change anyway. This supersedes the INTERNAL_CLEANUP_PLAN rename list.

Run a script on the file map to check it mechanically. It builds the target package import graph from each file's
current imports and fails on cycles and layer violations. **You approve TARGET_TREE before Stage 3.** This is
the only decision gate. Disagreements go into the doc as alternatives with their trade-off, not as chat.

## Stage 3: execution

Lanes are drawn from the target tree so that each lane owns a disjoint set of source **and** destination packages.
Lanes run in parallel, each unit runs in its own worktree, and integration into `internal-cleanup` is serial.

| Wave | Work | Parallelism | Gate on top of the standard one |
| --- | --- | --- | --- |
| 3A Delete | Dead code, test-only production code, unused exports, dead `cmd/` and `scripts/` entries (deleted tests go into `DELETED_GUARANTEES.md`) | One unit per package, very wide | `deadcode` diff shrinks; oracles unchanged |
| 3B Test decoupling | Rewrite only the white-box tests that block a planned move. Assert through the package API or move the test with its subject. Never weaken an assertion | One unit per test file group, wide | Same test names pass; coverage of the moved code does not drop |
| 3C Layering | Fix the 7 inverted edges with ports owned by the consumer and adapters wired in `server`. Add the compiler facade and switch `explorer` to it, then move compiler internals under `dataframe/compiler/internal/…` so that Go enforces the boundary | One lane per edge family | Layer check allowlist shrinks to zero |
| 3D Moves | Apply the file map lane by lane: create the package, `git mv` files, fix package clauses and qualifiers, export only what the map says. Merge the tiny packages and break up the catch-all files | 6–10 lanes (lifecycle, authoring, compiler-ir+lower, render/aql, compiler root, explorer root+arango, catalog, ingest, api, UI) | Move checker shows move-only; oracles byte-identical; `package_audit_verify --full` diff matches the target tree |
| 3E Shapes | Split god receivers into per-concept types, turn `Command` into variants, give long parameter lists a state type, cut ≥300-line functions, split fat structs where the audit found field groups | Per package, after its 3D lane lands | Oracles unchanged; `pstack:interrogate` review on behavior-touching units |
| 3F Interfaces | Shrink, move to consumers, delete single-implementation interfaces that are not seams | Per consumer package | Same |
| 3U UI | Same sequence for `ui/`: generated API types replace handwritten `types.ts` (if approved), `ExplorerBuilder` splits by domain, giant test files split by subject. No `useEffect` | 2–3 lanes | `make test-ui`, test count unchanged, `make e2e` at the end of the wave |

3A and 3B start as soon as Stage 2 is approved, and 3C can run alongside them. 3D lanes start when their 3B blockers have landed. Within a
package, the order is always delete → decouple → move → shape, which keeps each diff reviewable.

**Standard gate for every unit:** `go build ./... && go vet ./...`, the focused tests for the owned and importing packages,
`make lint-go-comments`, `package_audit_verify` before and after any package change, and the oracles. The coordinator reruns the
focused tests after applying the patch, commits (explicit paths, unit ID in the message), and records it in the ledger.
After every 5 commits, the coordinator also runs `make test-go` and `make test-ui`. At the end of each wave it runs
`make test-integration` and `make e2e`.

**Worker stop conditions:**
- The unit needs a path outside its ownership.
- A move turns out not to be move-only.
- An oracle diff appears.
- A test fails twice with no new hypothesis.
- Passing would require weakening an assertion.

In each case the worker reports `blocked` with evidence, and the coordinator takes the next ready unit.

**Size per unit:** pure moves have no line cap, since the move checker proves them. Logic changes stay under about 3k lines.

## Stage 4: acceptance

On the final head, run:
- the full Go, UI, integration and e2e suites;
- the oracles against the Stage 0 baseline;
- `gardenscan` again.

Record before and after for: package count; the largest package; receivers with ≥25 methods; structs with ≥20
fields; functions with ≥150 lines and with ≥8 params; catch-all files; layer violations; white-box test share. Pushes and PRs wait for you.

## Kickoff

**Coordinator prompt** (main OpenCode session on the H200 host):

> Read `AGENTS.md` and `docs/GARDENING_PLAN.md`. You coordinate. Do Stage 0. Then launch the Stage 1 audit units as
> background `pstack-explorer` tasks, 20 at a time. Give each its unit scope, the report schema, and the auditor rules
> verbatim, and require file:line evidence. Spot-check 3 claims per report by rerunning the evidence; send back reports that
> fail. When all audits are in, draft `docs/TARGET_TREE.md` and the file map, run the cycle and layer check, and stop for my
> approval. Do not edit production code before I approve.

**Execution worker brief** (Stage 3):

```
Unit <id>  Wave <3A–3F>  Lane <lane>  Base <sha>  Worktree /private/tmp/loom-garden-<id>
Own: <source + destination paths, incl. _test.go>.  Everything else is read-only.
Do: <file-map rows / audit items>.  No API/wire/DB format change. No assertion weakened. No useEffect.
Gate: build+vet; focused tests (lock, -p 8, 20m timeout); lint-go-comments; package_audit_verify; oracles; move checker.
Stop & report blocked on: foreign path, non-move in a move unit, oracle diff, 2 failed attempts, assertion change.
Report: diff --stat, patch path, moved vs changed lines, commands + exit codes + log paths, oracle + coverage diffs, deletions → DELETED_GUARANTEES.
```
