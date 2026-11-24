# Prompt: execute the Loom UI plumbing refactor end to end

Paste everything below the line into the executing agent, or tell it: "Read
`docs/UI_PLUMBING_EXECUTION_PROMPT.md` and execute it." The prompt is written to run unattended
to completion.

---

You are executing the complete UI plumbing refactor of `ui/packages/loom-ui` in the Loom
repository at `/Users/peterkor/Desktop/BMEG/loom`. Work until every ledger row has a terminal
status.

**Do not stop to ask questions, request approval, or report progress and wait.** The operator
has pre-authorized everything under "Authority". When you are unsure, apply the decision rules,
record the decision in the ledger, and continue. The run ends only with the final report
described at the bottom.

**This is a plumbing refactor, not a UI change.** Rendered DOM, copy, styling, focus order, and
interaction behavior must stay identical. If a change would alter anything the user can see or
do, it is a defect, however good its plumbing is.

## Sources of truth (read these first, fully)

1. `AGENTS.md`. The repository rules apply to everything; this prompt overrides them only where
   it says so. Pay particular attention to these sections: "Builder interaction", "Project
   conventions" (no `useEffect`), "Verification", "Adversarial review", "Local commits", and
   "Verification scheduling".
2. `docs/UI_PLUMBING_IMPLEMENTATION_PLAN.md`. This is the plan you execute. It gives the root
   causes C1–C4, the target architecture, Phases 0–5 with exit criteria, and gates G1–G4. All
   gates are resolved. G3 is a mechanical rule; follow it exactly.
3. `docs/UI_PLUMBING_AUDIT.md`. It holds findings F1–F8 and the evidence behind them.
4. `docs/ui-graph-audit.draft.mjs`. This is the import-graph scanner the audit used. Run it as
   `node docs/ui-graph-audit.draft.mjs ui/packages/loom-ui`. Phase 0 promotes it into the
   package.

The plan was written at commit `547a4b2ab`, so line numbers will drift as you work. Locate code
by symbol name, and use line numbers only as hints.

## Authority (pre-authorized; do not ask again)

**You may:**
- Work on the local branch `ui-plumbing-refactor`; it already exists and holds the plan. Create
  worktrees under `.cache/uiplumb/worktrees/` for parallel units.
- Edit, move, merge, split, and delete any file under `ui/packages/loom-ui/` (source, tests,
  `testSupport/`, scripts, config).
- Change import paths in `ui/apps/demo/` when a move requires it. Its behavior must stay the
  same.
- Update the plan and audit docs (status notes only), and add your execution report.
- Change the public surface exactly as G1 says: the Builder internals leave the public surface,
  and the version goes from `0.1.5` to `0.2.0`. Every other public name the frontend or demo
  consumes must stay exported under the same name and entry point. The plan lists them, and
  `public-api.json` will enforce them.
- Make local commits under AGENTS.md "Local commits".

**You must not:**
- push, open PRs, publish to npm, or deploy;
- run `git reset`, `stash`, `clean`, `rebase`, `amend`, `push --force`, or rewrite history;
- change Go code, `api/openapi.yaml`, or anything outside `ui/` and `docs/`. If a fix seems to
  need a server change, the row becomes `deferred` with the reason "needs server change".
- write to `~/Desktop/FFNEW/IDP-Frontend`. It is a read-only reference for G1.
  - Read it only with `git -C ~/Desktop/FFNEW/IDP-Frontend grep <pattern> <branch> -- ...` or
    `git show <branch>:<path>`.
  - Never check out a branch there, and never edit it.
  - The integration branch is `feature/clickhouse-explorer`.
- modify or delete files that were untracked before you started.
  - At start, run `git status --porcelain` and save it to
    `.cache/uiplumb/preexisting_untracked.txt`.
  - That list includes `TARGET_TREE.md`, `internal/out.txt`,
    `internal/explorer/rowsource/filter_test.go`, and several `docs/*_PLAN.md` files.
  - Phase 5's "update `TARGET_TREE.md`" step becomes `deferred` with the reason "operator-owned
    untracked file". Put the replacement text for its UI section in your report instead.

## Ledger (your memory; resume from it after any interruption or context loss)

Keep these files in `.cache/uiplumb/`:

| File | Contents |
| --- | --- |
| `ledger.tsv` | One line per row: `id`, `status`, `commit`, `decision`, `verification`, `note`. `status` is one of `todo`, `in-progress`, `done`, `obsolete`, `deferred`, `review-pending`. |
| `decisions.tsv` | Every judgment call and its reason. |
| `runs.tsv` | For every expensive check: command, source SHA, log path, exit code, duration. |
| `oracle_diffs.tsv` | Every call-log diff allowed under G3: test, step, the removed request, and the base-trace evidence that it was aborted. |
| `test_port_map.tsv` | Every assertion in a deleted or replaced test: old test name, then either the new test that asserts it or the reason it no longer applies. |

Before doing any work, read the ledger and continue from it. Never redo a `done` row.

Seed the ledger from the plan, one row per step:
- `P0.1`–`P0.4`
- `P1.delete`, `P1.fixture`, `P1.exports`, `P1.public-surface`, `P1.ratchets`
- `P2.contracts`, `P2.identity-merge`, `P2.client`, `P2.data`, `P2.request-owner`,
  `P2.mutations`, `P2.shells`, `P2.helpers`
- `P3.model`, `P3.test-only-exports`
- `P4.1`–`P4.7`
- `P5.ui-domains`, `P5.dom`, `P5.layer-enforce`, `P5.focus`, `P5.position`, `P5.escape`,
  `P5.effect-rename`, `P5.target-tree`

When a step turns out larger than expected, split it into sub-rows (`P4.4.1`, `P4.4.2`, …). Do
not shrink the goal.

Terminal statuses:
- **`done`:** implemented, verified, and committed.
- **`obsolete`:** the evidence no longer holds at HEAD. Say what you found.
- **`deferred`:** impossible under the Authority rules or physically blocked. Give the concrete
  reason and what would unblock it.

`review-pending` is not terminal. Resolve it before the final report.

## Decision rules (use these instead of asking)

1. **Re-verify first.** Before changing anything for a row, re-check its claim at HEAD with the
   cheapest check that settles it: the graph scanner, `rg`, or reading the code. If the claim no
   longer holds, mark the row `obsolete`.
2. **The oracle decides "no UI change".**
   - Any DOM diff against the base trace is a defect.
   - A call-log diff is allowed only under G3: it removes a request that the base trace shows
     was aborted before it settled. Every other call-log diff is a defect, including a new
     request, different arguments, a settled request removed, or a reorder across user-event
     steps.
   - A defect means you redesign the change; you never accept the diff.
   - Never regenerate the base trace to make a diff go away. The base comes from `BASE` plus
     only the Phase 0 tracing hook.
3. **The journey tests are the contract.** These are the 19 test files that render
   `BuilderWorkspace` or `LoomExplorerBuilder`, along with the Viewer and saved-views journey
   tests.
   - You may change their import paths and `vi.mock` targets when files move.
   - You must never change their assertions, expected text, queries, or user-event sequences.
   - If one fails, the production change is wrong. Never weaken an assertion just to make a
     test pass.
4. **White-box tests of replaced code** (the hook tests: `renderHook`, `useConstructionLifecycle*`,
   `useRootChange.identity`, `useAutomaticPreview`, `recipeEditingOwner`, and others the scanner
   reports as targeting deleted code):
   - Port every assertion to a plain TypeScript test of the session, model, or data module that
     now owns the behavior.
   - Record each mapping in `test_port_map.tsv`.
   - The only assertions you may drop are ones about a mechanism that no longer exists (an
     option-bag shape, a hook call order). Record the reason for each.
5. **No React effects, honest or disguised.**
   - Never add `useEffect`, `useLayoutEffect`, or `useInsertionEffect`.
   - Never add new `useBrowserSubscription` or `useCommitLifecycle` call sites.
   - Session lifecycle works exactly as the plan says: create during render with no side
     effects, start on the first store subscription, dispose in a microtask after the last
     unsubscribe.
   - Derive state during rendering, or update it through the action that owns it.
6. **Merging the semantic identities (`P2.identity-merge`).**
   - First write the table test the plan describes, covering every identity kind and the edge
     cases.
   - If both implementations agree on every case, merge them.
   - If they disagree, do not merge and do not "fix" either one, because fixing one changes
     behavior.
     - Commit the table test with the disagreeing cases marked `it.fails` against the merged
       candidate.
     - Mark the row `deferred` with the reason "operator decision: implementations diverge".
     - List the cases in the report.
     - Keep both implementations, each moved into `contracts/identity.ts` under a name that
       says where it came from.
7. **Which props stay** (Phase 4.7). A prop stays only if it is presentation, or a parent
   genuinely passes different values to different instances. Everything else is read from the
   session. A component that only forwarded state dissolves into its parent's layout or into
   the components it rendered.
8. **Where an owner belongs.**
   - Put state in the session if more than one component reads it or an async workflow reads
     or writes it.
   - Keep it local to the component if it is pure presentation and only that component uses
     it, such as an open/closed toggle, a hover, or a draft text field before submit.
   - Record each borderline call in `decisions.tsv`.
9. **Comments.** Follow the spirit of AGENTS.md's Go comment rules in TypeScript too: no
   comments that restate code, and delete the ones you touch that do.
10. **Failures you cause.** Fix the root cause. If a guard fires, fix the code, not the guard.
    You may lower a ratchet baseline only when your change legitimately reduced the count.
11. **Blocked rows.** Record the blocker, move to the next independent row, and come back.
    Before the final report, retry every `deferred` row once.

## Execution order

Follow the plan's phases in order. Within a phase, units without dependencies can run in
parallel.

**Phase 0, setup and oracle.**
1. Check CPU and memory (`sysctl -n hw.ncpu`, `vm_stat`), and size parallel jobs following
   AGENTS.md "Verification scheduling".
2. Record `BASE`: the branch tip before your first commit. Write `git rev-parse HEAD` into
   `.cache/uiplumb/BASE`. Every "against base" comparison in this prompt uses that SHA.
3. Run `make test-ui` and `npm --prefix ui run build` at `BASE`, and record both in
   `runs.tsv`.
4. Build the trace hook (`LOOM_UI_TRACE`) exactly as the plan specifies: DOM after each
   user-event dispatch and at test end, the mock client call log with abort and settle status,
   and normalized `useId` and UUID ids. Commit the hook by itself.
5. Record the base trace to `.cache/uiplumb/oracle/base/` by running the full UI suite with the
   hook enabled.
6. Run the suite a second time. If the two runs differ, the trace has nondeterminism. Fix the
   normalization before any other work, and record what you fixed.
7. Promote the scanner, add the ratchets, and generate `public-api.json`, as the plan lists.

**Phases 1–3.**
- Run them as the plan describes.
- In Phases 1–2, run independent units in separate worktrees: deletions, `contracts`, `client`,
  `data`/`RequestOwner`, and shells/display.
- The parent integrates and commits in dependency order: `contracts` → `client` → `data` → the
  rest.
- Phase 3 is moves only, with a single writer.

**Phase 4.**
- This phase is serial. One writer owns `BuilderWorkspace.tsx` and `session/`, and executes
  steps 4.1 → 4.7 in order.
- Useful parallel work alongside it:
  - a worker drafting the ported session tests for the next step, in new test files only;
  - a reviewer running `pstack:interrogate` on the previous step's commit;
  - a worker re-running the oracle diff on the previous commit in an isolated worktree.

**Phase 5.**
- Once the layer rule is in report mode, run the `ui/<domain>/` moves in parallel by domain,
  one writer per domain directory.
- The focus, position, and Escape units are serial and need behavior tests first. For Escape,
  pin the current nested-layer order before touching either owner.

## Verification for every unit (a unit is one commit)

1. Run `make test-ui`, which runs the boundaries check, the test typecheck, and vitest. Then run
   `npm --prefix ui run build`, which also builds the demo.
2. Diff the oracle against base. It must pass rule 2.
3. Run the graph audit. Every ratchet must be at or below its baseline. A guard the unit was
   meant to drive to a target must reach it.
4. `public-api.json` must be unchanged. The single exception is the `P1.public-surface` unit,
   and that diff must be exactly the G1 removals.
5. **Adversarial review.**
   - For every unit that changes production logic rather than just deleting or moving it,
     apply `pstack:interrogate` to the exact diff before committing. That covers
     `P2.request-owner`, `P2.identity-merge`, all of Phase 4, and `P5.focus`/`position`/`escape`.
   - If interrogate is unavailable, give an independent reviewer the diff, the affected
     callers, and the oracle result, and ask it to find a contract violation.
   - Keep a finding only after you rerun its reproducer.
   - Record the reviewer and the result in the ledger, and label same-model review as
     same-model.
6. **Browser acceptance.** At the end of each of P4.2, P4.4, P4.6, P4.7, and Phase 5, run
   `make e2e` and the Builder checks in `.codex/skills/verify/SKILL.md`, serialized, with a
   deadline.
   - If the local runtime is unavailable (`make dev-doctor` fails), mark those units
     `review-pending` with the reason "browser acceptance unverified", continue, and retry at
     the end.
   - Never change tested source while a browser run is in progress.
7. **Commit.**
   - Stage explicit paths only, and inspect `git diff --cached`.
   - Write the message as `P<phase>: <summary>`, then the ledger row IDs, the decisions, the
     verification run, the oracle status, and any G3 diffs.
   - End it with the repository's existing co-author convention, if one is used.

At the end of each phase, rescan with the graph audit into `.cache/uiplumb/rescan/`. Anything
your changes produced (new dead exports, newly unreachable files, new layer violations) becomes
a new ledger row `N-<n>`, worked in the same or the next phase.

## Exit targets (from the plan; the final HEAD must meet all of them)

- **Graph audit:** 0 unreachable production files, 0 unused exports, 0 import cycles, and
  0 layer violations, with the layer rule enforcing.
- **Test-only exports:** only from `model/` and `session/`.
- **`ui/**`:** no imports of `client/`, `useLoomClient`, or `LoomClient`.
- **Session cleanup:** no `actionsFor`, no `latestState`, and no hand-rolled stores outside
  `createStore`.
- **Width:** no hook takes more than about 8 options, and no component takes more than about 10
  non-presentational props. The audit reports this, and each exception is recorded with its
  reason.
- **Staleness and request ownership:**
  - 0 `snapshotToken`/`draftVersion`/`draftDigest` comparisons outside `contracts/identity`,
    `data/` request ownership, and `model/`. The baseline is 131.
  - `new AbortController` appears only in `data/` and `client/`.
- **Effects:**
  - `useDomSubscription` is used only in `dom/`, matching an exact allowlist.
  - There is no `useBrowserSubscription` or `useCommitLifecycle`, and the effect baseline stays
    `{}`.
- **`BuilderWorkspace`** is a layout shell.

## Final acceptance (run once, on the final HEAD)

- Run `make test-ui`, `npm --prefix ui run build`, `make e2e`, and the verify-skill Builder
  checks.
- Run the full oracle diff against base. It must be empty, apart from the diffs listed in
  `oracle_diffs.tsv`.
- Run the graph audit and confirm the exit targets above.
- Confirm the `public-api.json` diff against `BASE` is exactly the G1 removals.
- Re-check that `feature/clickhouse-explorer` in IDP-Frontend still imports only names that
  remain exported, using `git grep` only.
- Every ledger row must be `done`, `obsolete`, or `deferred`, with none `todo`, `in-progress`,
  or `review-pending`.

## Final report (the only time you stop)

Write `docs/UI_PLUMBING_EXECUTION_REPORT.md` and commit it. Contents:

- counts by status per phase;
- before and after numbers from the graph audit:
  - production and test lines;
  - unreachable files, unused and test-only exports, cycles;
  - the widest prop bags and option bags;
  - staleness comparisons, `AbortController` sites, and effect-like sites;
- `BuilderWorkspace.tsx` lines before and after;
- every G3-allowed call-log diff, for the operator's review;
- the `test_port_map.tsv` summary, including every dropped assertion and its reason;
- every decision taken, every `deferred` row with its reason, and the result of the semantic
  identity merge;
- the replacement text for the UI section of `TARGET_TREE.md`;
- every check that did not run, stated plainly;
- the commit list.

Then print a short summary. Pushing, PRs, and merging stay with the operator.
