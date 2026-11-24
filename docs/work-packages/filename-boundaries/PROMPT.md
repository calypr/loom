# Prompt: underscore filename cleanup (OpenCode / DeepSeek)

Paste everything below the line into OpenCode as the coordinator prompt.

---

You are the coordinator for a janitor pass over `internal/` in the Loom repository, on branch
`internal-cleanup`. The full plan is `docs/FILENAME_BOUNDARY_PLAN.md`. Read it once, in full, before
starting. This prompt tells you how to run it fast.

## What this work is

A large share of `internal/` filenames use underscores (`physical_validate_expressions.go`,
`source_load_copy.go`) as fake package boundaries. Your job is to put every package-level declaration in
the package that owns it, then give each file a one-word name. If a file's declarations belong to
different packages, split the file. When you finish, no production file under `internal/` has an
underscore in its stem, except `_test.go`, `.gen.go` and Go build-constraint suffixes.

This is mechanical work, so do it fast. You move code; you don't rewrite it. Function bodies, signatures
and behavior stay the same. The only edits allowed are:

- moving declarations
- splitting and renaming files
- adding the exports, package qualifiers and imports that a move requires
- deleting `*_aliases.go` shims after their callers are updated

Anything else you notice (dead code, bad design, a method that belongs elsewhere) goes on
`.cache/filenames/followups.md`. You don't fix it.

## Operator overrides for this program

These replace the matching defaults in `AGENTS.md`, for this program only:

- **No adversarial review per unit.** Don't run `pstack:interrogate`. These changes don't alter behavior;
  the `moveverify` check (below) and the compiler are the review.
- **No tests after small moves.** Follow the verification cadence below exactly. Don't run `go test`
  after editing a file, after a split, or to "check progress". The compiler tells you what broke.
- **Fix forward.** When a build fails, read the compiler output and fix it. Don't rerun suites to find
  the problem, and don't revert a move to make something compile.

Everything else in `AGENTS.md` still applies:

- one writer per file or worktree
- preserve unrelated changes
- no stash, reset, clean, rebase or amend
- no pushes
- keep `.cache/` out of commits

## Hard rules

- **No alias shims.** No `type X = other.X` and no `var F = other.F` bridges. Update every caller in the
  same unit.
- **No compound-word cheats.** `sourceloadcopy.go` is not a fix. If a name needs two concepts, the first
  is probably a package.
- **No catch-all names** for packages or files: no `util`, `common`, `helpers`, `types`, `support` or
  `misc`.
- **No stutter.** A package name doesn't repeat its parent: `ir/validate`, not `ir/irvalidate`. A
  filename doesn't repeat its package: `validate/expressions.go`, not `validate/validate_expressions.go`.
- **Cut, don't retype.** Move each declaration with its doc comment byte for byte, or with the `apply`
  tool. Never paraphrase or reformat code while moving it.
- **Methods stay with their receiver type.** If a method's logic belongs in another package, flag it
  `receiver_bound` in the ledger and leave it in place.
- **Helpers follow their callers.** If callers end up in two packages, the helper goes to the lower-ranked
  one. Never duplicate it.
- **Respect the layer ranks** in `TARGET_TREE.md`. Never create an import cycle.
- **Tests follow the code they test.** A test function moves with its declaration. Never delete or skip a
  test to make a move compile.
- **Run `make lint-go-comments` once per unit.** Go comments that restate the code still get removed.

## Stages

### Stage 0: Tooling (one worker, you review)

Build `scripts/filenames/`, a Go program with no dependencies outside the standard library. Use
`go/types` with `importer.ForCompiler(fset, "gc", nil)`. The seed is `.cache/filenames/probe/main.go`.
It already type-checks every package and measures reference direction between filename-prefix groups.

Subcommands:

- **`inventory`** writes `.cache/filenames/ledger.tsv` with one row per package-level declaration in an
  underscore file. A type and its methods count as one unit, recorded with its receiver.
  - Columns: file, package, kind, receiver, lines, exported, raw tokens, normalized tokens.
  - Also: the referenced declarations and the referencing declarations, inside and outside the
    package.
  - Also: per-package affinity, meaning the share of the declaration's references that go to each
    package.
  - Also write a per-file rollup that counts the distinct top-affinity destinations in each file.
- **`groupcheck <dir> <selector>`** works like the probe but on a set of declarations. The selector is a
  file regex, a name regex, or an explicit list. It prints reference direction and the exact symbols that
  cross back.
- **`graphcheck <ledger>`** applies the ledger's destinations to a virtual package graph. It reports
  cycles and violations of the `TARGET_TREE.md` layer ranks.
- **`apply <ledger> <package>`** carries out one package's approved rows mechanically:
  1. creates destination directories and files with the right `package` clause;
  2. cuts each declaration with its doc comment, byte for byte;
  3. uses `git mv` when a whole file moves;
  4. deletes emptied files;
  5. runs `goimports`, or `gofmt` plus import repair.

  It doesn't need to fix qualifiers or exports perfectly. Compile errors are fixed in the next step.
- **`moveverify <base-ref>`** checks that the work is a pure move. For every declaration at the base, it
  finds the declaration at `HEAD`, which may be in a different package or file. It compares a hash of the
  body after normalizing package qualifiers and the case of the first letter of names. It reports
  anything missing, added or changed.

Stage 0 is done when:

- `groupcheck` reproduces the prefix-group numbers in `.cache/filenames/probe/baseline_prefix_groups.txt`
  at the base commit;
- `moveverify <base>` against an unchanged tree reports zero differences.

Commit the tool on its own.

### Stage 1: Inventory

Run `inventory`. Report how many declarations it found and how many files have more than one
destination by affinity.

### Stage 2: Verdicts (fan-out, read-only)

Split the ledger by package and run up to five background workers at once:

- `explorer/lifecycle`, `compiler/lower`, `render/aql`, `compiler/ir` and `explorer/workspace` get two
  workers each, split by concept;
- every other package gets one worker.

Each worker:

1. Reads every underscore file in its slice in full.
2. Gives each declaration a destination: stay, an existing package, or a new package `<pkg>/<concept>`.
   It starts from the affinity column and overrides it when reading the code shows otherwise.
3. Proposes a new package only when the group passes the plan's threshold:
   - 2 or more files' worth of declarations, or at least 300 lines;
   - one responsibility;
   - one-way after at most 3 symbol relocations, confirmed by `groupcheck`.
4. For each declaration, fills `dest_package`, `dest_file`, `exports_added`, `receiver_bound`,
   `tests_affected` and a short `evidence` field.
5. Writes a one-line reason for any file whose declarations all keep the same destination. The default
   question is "what in here belongs elsewhere?"
6. Edits no source.

Then run a second pass with fresh workers, each reviewing a different slice than it wrote. They check
cross-package moves and new groups. Rerun `groupcheck` yourself on every disputed group, and keep only
the verdicts that hold.

### Stage 3: Target and stop

1. Merge the slices and resolve conflicting claims on the same concept. `construction`, `source`,
   `recipe`, `row` and `group` appear in many packages.
2. Run `graphcheck` and fix every cycle by relocating symbols.
3. Write `docs/FILENAME_BOUNDARY_TARGET.md`, containing:
   - the package list with each package's responsibility;
   - before and after counts: packages, files with underscores, files split;
   - every new package with its `groupcheck` evidence;
   - the receiver-bound list, grouped per receiver;
   - the judgment calls the operator should look at.
4. **Stop and wait for operator approval.** Don't start Stage 4 without it.

### Stage 4: Execute (fan-out in worktrees)

Run waves leaf-first:

1. `store/*`, `ingest/*`, `dataframe/publication*`, `dataframe/published`
2. `compiler/ir`, then `compiler/lower`, then `render/aql`, then the `dataframe/compiler` root, then the
   rest of `dataframe/*`
3. `explorer/*` sub-packages, then the `explorer` root, deleting the alias shims
4. `api/explorer`, `server`, `acceptance`

There is one worker per package unit, each in its own worktree branched from the current wave base. Up
to five run at once. Packages within a wave that move declarations into each other run one after the
other.

Each worker:

1. Runs `apply` for its package.
2. Fixes compile errors: exports, qualifiers, imports, and callers in other packages.
3. Renames the remaining underscore files in its package to one-word names.
4. Splits the matching test files so test functions follow their declarations.

### Verification cadence

**While a worker is editing:** nothing. No builds, no tests.

**When a package unit is done** (the worker runs these once):

- `go build ./...`
- `go vet <touched packages>`
- `go test -run '^$' <touched packages>/...` (compiles tests without running them)
- `scripts/filenames moveverify <wave-base>`, which must report zero body changes
- `make lint-go-comments`
- no underscore file stems left in the package

**When integrating:** merge the units into the wave branch one at a time, and after each merge run only
`go build ./...`.

**At the end of each wave** (you run these once):

- `make test-go`
- `python3 scripts/package_audit_verify.py --full`
- `graphcheck` on the real tree

If tests fail, read the failing test, find which unit's move caused it, and fix that unit. Then rerun
only the failing packages, followed by one final `make test-go`.

Commit once the wave's `make test-go` passes. Make one commit per package unit, staged by explicit paths,
with that unit's ledger summary in the message.

**At the end of the program** (once each):

- `make test-integration` (needs `LOOM_TEST_ARANGO_URL` and `LOOM_CLICKHOUSE_URL`; if they are unset,
  report it as unverified)
- `make e2e` against a running stack (if it isn't available, report it as unverified)

Long checks run in the background with a deadline and a saved log path. A timeout is a failure, never
a pass.

### Stage 5: Lock it in

Add a `make lint-filenames` target that fails on any underscore stem in an `internal/` production file,
except `_test.go`, `.gen.go` and build-constraint suffixes. Wire it into `make lint`. Expect an empty
allowlist; any entry needs operator sign-off.

## Report back

At each stop (after Stage 0, Stage 3, each wave, and the end), report:

- what changed: packages created, files moved, split or renamed, and the underscore count before and
  after;
- the checks run, with exit status and log path;
- anything unverified;
- the follow-ups list.

Keep each report short and use numbers, not narrative.
