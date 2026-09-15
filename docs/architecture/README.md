# Architecture execution plan

This is the planning handoff for Loom's architecture audit on 2026-09-15.
No proposed refactor has been implemented. The source checkpoint is
`3af5e2a7c1f47c104b601f92d7e517b265671bfd`, pushed to
[`planning/architecture-workpackages-20260915`](https://github.com/calypr/loom/tree/planning/architecture-workpackages-20260915).
`main` was not changed.

The reviewed worklist contains 64 items: 59 planned and five blocked on
reproduction. There were 69 discovery candidates; review rejected four and
consolidated one overlapping finding. Another 16 claims were rejected during
discovery. Rejected claims are not counted as work items.

Coordinated breaking changes are allowed when justified. Each work package must
name affected callers, handle saved formats, and pass its migration gate before
integration. This permission does not authorize a production migration or data
cleanup.

## Read this first

[ISSUES.csv](ISSUES.csv) is the canonical issue tracker. Filter by `wp_id`, then
`priority`. Each row includes the actual source locations, proposed change,
observable acceptance condition, verification command, independent review,
counterevidence, migration requirements, and execution status.

[WORK_PACKAGES.csv](WORK_PACKAGES.csv) is the execution sheet. It supplies file
ownership, dependencies, branches, unit/live/performance gates, and rollback
constraints. Owners are unassigned. The issue and package statuses describe
implementation progress, not whether the architecture review is complete.

The CSV files can be opened directly in a spreadsheet. They are Git-friendly
execution records, not a formatted workbook. Keep them as the single source of
truth instead of maintaining a second status table.

## Work packages and merge order

| Wave | Package | Boundary to improve | Prerequisites |
| --- | --- | --- | --- |
| 1 | WP01, 13 items | Recipe validation, compiler lowering, cardinality and provenance | Baseline |
| 1 | WP02, 5 items | Catalog memory budgets, ingest cancellation and retained generations | Baseline |
| 1 | WP03, 6 items | Publication checkpoints, fencing, recovery and reconciliation | Baseline |
| 1 | WP04, 4 items | Worktree isolation, UI/browser CI and verification ownership | Baseline |
| 2 | WP05, 13 items | Explorer commands, receipts, output contracts and publication lifecycle | WP01, WP02, WP03 |
| 2 | WP06, 13 items | API errors, schema generation, read identity and authorization boundaries | WP01, WP02, WP03, WP04 |
| 3 | WP07, 10 items | Frontend cache ownership, decoding, project state and publication-aware reads | WP04, WP05, WP06 |

Start with the small boundary fixes inside each package. Do not turn WP01 into a
compiler rewrite or WP05 into a replacement backend. Within WP03, fence durable
writes before removing the process-wide admission lock. Within WP06, settle the
publication-bound read contract before WP07 migrates the client.

Where an issue lists alternatives, prefer the smallest explicit contract. For
example, reject an unsupported recipe form at validation unless a current
caller or saved recipe requires implementing it. Record that choice before
coding. A shared abstraction needs actual consumers and a demonstrated policy
to own; duplication alone is not a reason to add another layer. `S`, `M`, and
`L` are relative effort estimates, not delivery-time promises.

The earlier three concerns are retained and made actionable:

- Receipt policy belongs at the Explorer lifecycle boundary, not only in a
  server callback. See ARC-B04 and ARC-B07 in WP05.
- Related-record cardinality must agree with declared losslessness and ML
  readiness. ARC-B05 remains blocked on the multi-related-record reproduction.
  Compiler and public-contract changes cross WP01 and WP05.
- Paging and export must refer to one publication. ARC-D06 and ARC-E03 cross
  WP06 and WP07. The client must not silently combine rows across a republish.

Run ARC-B05's reproduction early. If it requires a compiler follow-up after
WP01, the integrator schedules that follow-up with the compiler owner on the
current integration commit before finishing WP05. The Explorer worker does not
gain permission to edit compiler files through a source citation.

Useful first targets include bounded ingest cleanup, cancellation-aware
publication metadata writes, Builder refetch invalidation, and preserving
structured API error paths. These have narrow acceptance tests. Larger items
such as fragment identity, durable publication recovery, and persisted protocol
migration need their own before/after fixtures; they are not labeled easy wins.

## Execution contract

1. Fetch the planning branch and record its exact current SHA as the launch
   commit. It contains this plan on top of the frozen source checkpoint.
2. The integrator creates one integration branch from that launch commit. Start
   wave-1 worktrees from it. Start later-wave worktrees from the verified
   integration commit containing every listed prerequisite, not the old source
   checkpoint.
3. Assign one owner per package. A worker changes only `worker_paths` after
   subtracting the global `integration_paths` reservation. The integrator alone
   edits reserved files, the canonical CSVs, and the GitNexus index.
4. Before editing, turn each selected issue's acceptance statement into an
   executable fixture. A `blocked` issue is authorized for reproduction work
   only. Release that gate only when the evidence establishes the scoped issue.
5. Make the package change and migrate its callers. Put cross-package contract
   changes in an integrator-owned contract patch, with old-format handling and
   the regeneration command. Do not leave temporary compatibility layers as the
   final architecture merely to keep intermediate branches green.
6. Run focused tests for the changed package and direct importers. Perform its
   live and performance gates. Submit the patch, exact commit, test commands and
   evidence location to the integrator.
7. Integrate one package boundary at a time. Run the full repository suite once
   after the integrated wave, plus the UI and real browser gates below. Resolve
   failures before starting dependents.
8. Mark an issue `done` only with its implementation SHA and verification
   evidence. A package is done only when all accepted issues and migration gates
   are satisfied. Update the decision trail for accepted changes in scope.

The reservation includes `server.go`, `options.go`, OpenAPI's source document,
generated code, Makefile, UI dependency manifests, and `scripts/loom-dev.mjs`.
The dev driver is shared by WP04 and WP07, so those workers propose changes to
the integrator instead of both owning it. Generated changes come from their
schema or generator source, never hand edits.

The validator checks existing tracked-file ownership. Before adding or moving
a file, the integrator must also resolve its owner. A clean static file split
does not prove semantic independence; the dependency waves and contract gates
remain mandatory. The checker also verifies each issue's existing edit targets
against its assigned owner and rejects done packages with unfinished issues.

Any package move, combine, or deletion starts from `docs/PACKAGE_AUDIT.csv` and
requires the regression-guard and blast-radius workflow. Run
`python3 scripts/package_audit_verify.py <location>` before and after each
boundary change. Regenerate and inspect the audit/importer diff. Run
`python3 scripts/package_audit_verify.py --full` after integrating the wave.
No deletion is authorized merely because GitNexus shows no callers.

## Isolated worktree verification

The current dev CLI supports separate projects and ports, but its defaults are
shared across checkouts. Until WP04 improves that default, reserve explicit
settings per worktree. For example, in the WP01 worktree:

```bash
export LOOM_DEV_COMPOSE_PROJECT=loom-dev-wp01
export LOOM_DEV_PROJECT=loom_dev_wp01
export LOOM_DEV_API_PORT=8281
export LOOM_DEV_UI_PORT=3281
export LOOM_DEV_SOURCE_ROOT="$PWD"
export LOOM_DEV_ARTIFACTS="$PWD/.artifacts/architecture/wp01"
make dev
make verify-fast
```

Reserve other ports and Compose names for other workers. Check that the ports
are free first. These settings isolate containers, volumes, source mounts and
evidence. Never run against `loom-demo`, the canonical CONFIG, or a researcher's
dataset. The full watcher test temporarily edits source, so only the worktree
owner may run `make verify-full`, with no simultaneous edits there.

Do not purge an existing stack as an incidental test step. Any cleanup must
name the exact owned project and retain the needed evidence first.

## Verification and evidence

The frozen baseline passed the full Go suite, 103 UI tests, UI builds, six
verification-driver tests, and a real browser workflow with 20 assertions.
The short browser run took 11.173 seconds including setup, with 6.790 seconds
in the browser scenario. That is not an edit-to-rebuild benchmark.
[BASELINE.json](BASELINE.json) records commands, scope and local evidence.

The baseline fixture has only one related record per root. It does not prove
multi-related-record losslessness, production authentication, crash recovery,
concurrent export, or large-data performance. Green baseline tests do not turn
source-inferred risks into reproduced bugs.

Run the plan checks after changing the execution tables:

```bash
python3 scripts/validate_architecture_plan.py
python3 -m unittest discover -s scripts -p 'test_validate_architecture_plan.py'
```

At the end of an implementation wave:

```bash
make test
make dataframe-boundaries
make openapi-check graphql-check
cd ui && npm run check-boundaries && npm test && npm run build
```

Then, from the repository root in the owned integration stack, run
`make verify-fast`. Run `make verify-full` when watcher, build, or dev-driver
behavior changed. New acceptance cases in the CSV are tests to add, not tests
claimed to exist. Use the repository Makefile's Go toolchain and local cache
settings when running the focused `go test` commands.

The performance percentages in the execution sheet are proposed investigation
thresholds. Capture repeatable pre-change measurements on the same machine and
fixture before evaluating them. They are not measured regressions or universal
service-level promises.

## What GitNexus did and did not establish

Five disjoint discovery slices used graph queries to locate symbols and caller
relationships, then checked source and focused tests. Fresh reviewers tried to
falsify every candidate. [COVERAGE.csv](COVERAGE.csv) records queries, scope and
gaps; [REJECTED.csv](REJECTED.csv) retains rejected claims and their reasons.

GitNexus helped navigation. It did not supply architectural judgments. Callback
dispatch, HTTP-to-TypeScript connections and some named Go types were missing
from the graph, so an absent edge was not treated as proof. Examples rejected
on source review include supposedly absent Arango activation fencing and an
alleged stale-output bug that was intentional historical retention.

The post-audit index refresh initially failed with an invalid UTF-8 keyword-index
error and a sandbox-denied registry write. Its repair command required recovering
the interrupted analysis first. A permitted normal analysis recovered through a
full rebuild and reported success in 49.3 seconds. A follow-up keyword query
succeeded. Analysis also warned that flow extraction skipped 8,161 of 8,361
candidate entry points under its ranking/budget limits. Treat flow coverage as
partial.

This execution sheet is the interpretation layer above the graph. Keep the
issue IDs stable, link future changes to them, and refresh the graph once from
the integrated branch after each wave. Do not have workers concurrently rebuild
the same index. This is broad coverage of the identified subsystems, not a claim
that every symbol in the repository has been exhaustively audited.

`pstack:poteto-mode` shaped this work through graph-first discovery, independent
falsification, explicit ownership, small verifiable changes, and the executable
plan checks. The repository's conservative audit policy limited agent fan-out
and kept application changes out of discovery.
