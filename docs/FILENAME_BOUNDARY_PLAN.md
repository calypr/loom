# Underscore filename plan

Status: proposed, not started. Base: `internal-cleanup` at `5118b729f` (host) = `cb7497a` (OpenShell checkout; same tree).
Executor: OpenCode with pstack agents on the DeepSeek endpoint. The Claude session coordinates, checks
evidence and owns commits.

## Goal

Every underscore in an `internal/` production filename either becomes a package boundary or is removed.
In the end state:

- No production `.go` file under `internal/` has an underscore in its stem. The only allowed suffixes are
  `_test.go`, `.gen.go`, and Go build-constraint suffixes such as `_linux.go`.
- Each declaration sits in the package that owns it. A file whose declarations belong to different
  packages is split, not moved as a whole. Each resulting file has a one-word name for its main type or
  concept inside its package.
- A lint check enforces the rule, so new files can't bring the pattern back.

Today the prefixes look like package boundaries without being boundaries. This is janitor work: put
every declaration where it belongs, and change nothing else. The file is where the review starts. The
declaration is what gets a verdict.

## Measured baseline (`5118b729f`)

- **Production:** 850 files, 192.6k lines, 119 packages. 541 production files (64%) have an underscore.
- **Tests:** 706 of 850 test files have an underscore before `_test.go`.
- **Packages where nearly every file has an underscore:**

| Package | Files with underscores / all files |
| --- | --- |
| `compiler/lower` | 41/45 |
| `explorer/lifecycle` | 39/50 |
| `render/aql` | 36/45 |
| `compiler/ir` | 33/42 |
| `explorer/workspace` | 30/40 |
| `api/explorer` | 25/25 |
| `explorer/construction` | 25/27 |
| `dataframe/compiler` | 25/28 |
| `ingest/generation` | 12/12 |
| `ingest/sourceload` | 11/11 |

- **Most common name words:**

| Word | Files | Word | Files |
| --- | ---: | --- | ---: |
| construction | 75 | physical | 28 |
| source | 51 | openapi | 25 |
| recipe | 38 | preview | 21 |
| row | 32 | validation | 20 |
| group | 31 | aliases | 16 |
| explorer | 31 | | |

- **Alias shims:** 16 `*_aliases.go` files (1,074 lines) re-export symbols left behind by earlier moves. The
  largest is `authoringv2/workspace_aliases.go` at 526 lines. `ir/physical/` and `ir/validate/` are
  one-file packages that only re-export names from `ir`: the package names exist, but the code was never
  moved into them.

### What the first probe found

A type-checked scan grouped files by filename prefix and measured which way package-level symbol
references cross between the group and the rest of its package. It found four kinds of prefix:

1. **Repeats the package path.** Examples: `sourceload/source_load_*`, `api/explorer/openapi_explorer_*`,
   `construction/construction_*`. Often a real group sits one word deeper. For example,
   `ir/physical_validate_*`, `physical_validation_*` and `physical_stage_*_validation` together form an
   18-file validation layer. It references 200 symbols in the rest of `ir`, and the rest references only
   16 of its symbols back.
2. **Clean one-way split today.** `compiler/cell_*` and `row_*`, `published/aggregate_*`,
   `population/member_*`, `store/arango/preview_*`, and `api/explorer/openapi_explorer_construction*`.
3. **Nearly one-way.** These split cleanly after 1–3 symbol relocations. Examples: `lifecycle/population_*`,
   `server/wire_*`, `explorer/arango/workspace_*`, `receipt/construction_*`.
4. **Shared word, not a module.** The files barely reference each other but are tangled with the rest of
   the package. Examples: `authoringv2/command_*` (0 references within the group, 123 outgoing, 67
   incoming), `lower/construction_*`, and `generation/generation_file_*`.

Two lessons for the method:

- **The concept word moves around in the name.** Validation appears as `physical_validate_x`,
  `physical_validation_x` and `physical_stage_x_validation`. Grouping by first word alone puts
  `physical_stage_group_validation.go` with the stage files when it belongs with validation.
- **A name only proposes a group.** Every proposed group must pass the reference-direction check.

## Rules

- **No alias shims.** A move updates every caller in the same wave. Existing `*_aliases.go` files are
  removed the same way.
- **No compound-word cheats.** Renaming `source_load_copy.go` to `sourceloadcopy.go` hides the problem.
  If a name needs two concepts, the first concept is probably a package.
- **No catch-all names** for packages or files: no `util`, `common`, `helpers`, `types`, `support` or
  `misc`. Helper files fold into the files that use them, or into the package that owns the concept.
- **No stutter.** A package name doesn't repeat its parent (`ir/validate`, not `ir/irvalidate`). A filename
  doesn't repeat its package (`validate/expressions.go`, not `validate/validate_expressions.go`).
- **New-package threshold** (proposed default, operator may change):
  - The group has 2 or more files, or one file of 300 or more lines.
  - It names one responsibility.
  - After at most 3 symbol relocations, it depends on its parent one way.
  - Its new imports obey the layer ranks in `TARGET_TREE.md`.
- **Tests follow their code.** A white-box test that reaches across a new boundary either moves with the
  symbols it tests or becomes a black-box test of the exported surface. Tests are not deleted to make a
  move compile.
- **Behavior doesn't change.** The only edits allowed are:
  - moving declarations
  - splitting and renaming files
  - adding the exports and package qualifiers that a move requires

  Logic cleanup found along the way goes to a follow-up list.
- **Helpers go where their callers go.** An unexported helper follows its callers. If callers end up in
  two packages, the helper goes to the lower-ranked one, which the other already imports. It is never
  duplicated.

## Verdict procedure

The decision is made per declaration, at package level: each type with its methods, each function, and
each var or const block. File outcomes follow from the declaration verdicts.

### Declaration verdicts

Every package-level declaration in the 541 files gets one destination:

1. **Normalize the file name first,** for use as a hint only.
   - Drop words that repeat the package path, including split forms such as `source_load` for `sourceload`
     and `openapi_explorer` in `api/explorer`.
   - Fold spelling variants together: validate/validation, plural/singular.
2. **STAY** when the declaration's references, in both directions, are mostly to its current package.
3. **MOVE to an existing package `P`** when:
   - the declaration's own references point mostly at `P`;
   - moving it keeps layers and cycles legal.
4. **NEW package `<pkg>/<concept>`** when the declarations sharing a concept, possibly drawn from several
   files, pass the new-package threshold as a group. The group is built from declarations, not whole
   files. A file can send half its declarations into a new package and keep the rest.
5. **Receiver-bound:** methods move only with their receiver type. Go doesn't allow otherwise.
   - A method whose logic belongs to another package gets a `receiver-bound` flag and stays put.
   - Turning it into a function or splitting the receiver is a design change, which is out of janitor
     scope.
   - Flagged methods are listed per receiver for the operator. `lifecycle.Service` (145 methods) and
     `physicalPlanRenderer` (80) will dominate that list.
6. **Flag, without acting,** any declaration that looks dead or duplicated. Deletion follows
   `docs/REPO_CLEANUP_GOAL.md`.

### File outcomes

These follow from the declaration verdicts:

| Declarations in the file | Outcome |
| --- | --- |
| All stay | **RENAME** to one word naming the main declaration |
| All go to one place | **MOVE** the whole file, then rename it |
| Destinations differ | **SPLIT**: one new file per destination, each named for its contents; the original file is removed |
| Too little left to justify a file | **MERGE** into the sibling that uses it; `*_helpers.go`, `*_compat.go` and `*_aliases.go` usually end here |
| A rename would collide with a sibling | **MERGE** with that sibling, or choose a more specific one-word name |

## Stages

### Stage 0: Tooling (Claude session, small)

Turn the probe into a repository tool under `scripts/filenames/`. It writes regenerable output to
`.cache/filenames/`, which stays out of Git.

- **`inventory`** writes one row per package-level declaration in an underscore file:
  - file, package, kind, receiver, lines, exported or not
  - the file's raw and normalized tokens
  - the declarations it references and the declarations that reference it, inside and outside the
    package
  - affinity: the share of its references that go to each package, its own included

  A rolled-up per-file view shows how many distinct destinations each file's declarations have.
- **`groupcheck <dir> <decl-set>`** reports reference direction for any proposed set of declarations,
  selected by file regex, name regex or an explicit list. It lists the exact symbols that cross back,
  which are what must be relocated.
- **`graphcheck <ledger>`** applies every MOVE and NEW verdict to a virtual package graph. It reports
  cycles, layer-rank violations and forbidden edges (rules from `TARGET_TREE.md`).
- **`moveverify <base> <head>`** checks that a wave is a pure move. It compares a hash of each declaration
  body, ignoring package qualifiers and export-case changes, and reports any body that changed.

- **`apply <ledger> <package>`** carries out one package's approved rows mechanically:
  - cuts each declaration byte for byte, with its doc comment;
  - uses `git mv` for whole-file moves;
  - runs `goimports`.

  Workers then fix only compile errors.

Exit check: the tool reproduces the probe numbers above at `5118b729f`. The seed probe is saved at
`.cache/filenames/probe/`.

### Stage 1: Inventory ledger (tool only)

Run `inventory` to produce `.cache/filenames/ledger.tsv`. The tool fills in proposed values for:

- normalized concept
- candidate group
- top-affinity package
- `groupcheck` result for each candidate group

Agents start Stage 2 from this ledger, not from filenames.

### Stage 2: Per-package judgment (DeepSeek fan-out, read-only)

Each worker owns one package's rows in the ledger. Large packages are split by concept:

- `lifecycle`, `lower`, `render/aql`, `ir`, `workspace`: two workers each
- every other package: one worker

For every file in its slice, a worker:

- reads the whole file;
- gives each declaration a destination (`dest_package`, `dest_file`), confirming or overriding the
  affinity the tool proposed;
- runs `groupcheck` on any proposed NEW group, given as a set of declarations;
- for each declaration, fills these columns:
  - `exports_added`
  - `receiver_bound` (with the package the method's logic belongs to)
  - `tests_affected` (which test functions follow it)
  - `evidence`
- records the resulting file outcome: RENAME, MOVE, SPLIT or MERGE.

A file whose declarations all get the same destination needs a one-line justification. Its file name
probably named one concept, but the default question is still "what in here belongs elsewhere?"

Workers don't edit source. A second worker reviews each slice, checking cross-package MOVE claims in
particular. The coordinator reruns `groupcheck` on any disputed group before accepting it.

### Stage 3: Reconcile and approve (Claude session, then operator)

1. Merge the slices and resolve conflicts. Two packages may claim the same destination, or a NEW package
   in one slice may duplicate a concept in another (construction appears in 10 packages).
2. Run `graphcheck` on the full ledger. Fix cycles by relocating symbols, not by aliasing.
3. Write `docs/FILENAME_BOUNDARY_TARGET.md`, containing:
   - the resulting package list with each package's responsibility
   - the counts before and after: packages, files with underscores, average files per package, files split
   - every NEW package with its group evidence
   - the receiver-bound list, grouped per receiver, with each method's target package
   - every judgment call the operator should look at
4. Reconcile with `TARGET_TREE.md`, which is still awaiting approval and whose `.cache/garden` evidence is
   gone. The ledger becomes the per-file map that the target tree points to. Package-level decisions that
   disagree are listed for the operator.

**Operator approval gate.** Stage 4 doesn't start without approval.

### Stage 4: Execution waves (DeepSeek workers in worktrees)

Waves go leaf-first, so each wave's destinations already exist:

1. `store/*`, `ingest/*`, `dataframe/publication*`, `dataframe/published`
2. `compiler/ir`, then `compiler/lower`, then `render/aql`, then the `dataframe/compiler` root, then the
   rest of `dataframe/*`
3. `explorer/*` sub-packages, then the `explorer` root; alias shims are removed here
4. `api/explorer`, `server`, `acceptance`

Within a wave, there is one worker per package, each in its own worktree.

- **Whole-file moves and renames** use `git mv`, so `git log --follow` keeps their history.
- **Splits** cut each declaration verbatim, with its doc comment, into its destination file. The
  declaration's text doesn't change, so `moveverify` can match every moved declaration to its original.
- **Test functions** split the same way and follow the declarations they test.

This is mechanical work, so checks are tied to milestones, not edits:

- **While a worker is editing:** nothing runs.
- **When a package unit is done:**
  - `go build ./...`
  - `go vet` on the touched packages
  - `go test -run '^$'` on the touched packages (compiles tests only)
  - `moveverify` against the wave base, with zero body changes beyond qualifiers and export case
  - `make lint-go-comments`
  - no underscore file stems left in the package
- **When integrating a unit:** `go build ./...` only.
- **At the end of each wave:**
  - `make test-go`, once
  - `python3 scripts/package_audit_verify.py --full`
  - `graphcheck` on the real tree

The units don't change behavior, so there is no per-unit adversarial review; `moveverify` takes its place.
After the wave's tests pass, the coordinator makes one commit per package unit and writes the unit's
ledger summary into the commit message. Units that can't be made to compile go back to Stage 2.

At the end of each wave, `make test-integration` runs once. `make e2e` runs once at the end of Stage 4
only. The waves change no behavior, so the browser run guards against wiring mistakes, such as handler
registration that moved.

### Stage 5: Enforcement

- Add `make lint-filenames`, which fails on any `internal/` production stem containing an underscore. Wire
  it into `make lint`.
- An allowlist exists only for rows the operator explicitly deferred. Each entry carries its ledger
  reason, and the allowlist is expected to be empty.
- Test filenames follow their production file. A test that covers several files is named for the behavior
  it covers, without underscores before `_test.go`.

## Out of scope

- `ui/` (TypeScript naming is a separate question)
- `cmd/`, `scripts/`
- generated files
- logic refactors found during moves; they go to `docs/BACKLOG.md`

## Open questions for the operator

1. **New-package threshold:** keep the default (2 or more files, or 300 or more lines, one-way after at
   most 3 relocations), or prefer more, smaller packages?
2. **Relationship to `TARGET_TREE.md`:** should this plan replace its file-cluster portion, or run after
   its package-level moves are approved?
3. **Test filenames:** should they be held to the same no-underscore rule from Stage 5, or only follow
   their production files?
4. **Scope of the lint:** may it also cover `cmd/` once `internal/` is clean?
5. **Receiver-bound methods:** these stay put under janitor rules. Should a later pass turn them into
   functions or split their receivers? That would be a design change, decided per receiver from the
   Stage 3 list.
