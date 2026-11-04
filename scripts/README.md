# Scripts

Run commands from the repository root.

The required test loop is split across independent CI jobs in
`.github/workflows/tests.yaml`. Vitest, Go, Node, Python, shell, contract, and
build jobs run in parallel. Every test process has a 30-second bound where its
runner supports one, and every job has a 10-minute timeout.

Retained Node tests are selected with:

```sh
node scripts/ci/list-node-tests.mjs
```

The selector follows relative imports and excludes tests whose dependency
closure reaches Playwright or a missing module. CI distributes that stable
list across four native `node:test` shards.

The same Builder entry point also attaches every remaining UI suite to five user-facing feature groups. Run `node scripts/verify-builder-formal-core.mjs --phase behavior --list` for the read-only doctor, then `--phase behavior --feature drafts --out /tmp/loom-drafts-check` for a focused behavior check without Java or formal tools. `--case` narrows the assertion pattern; zero assertions block. Four CI behavior shards cover the complete existing suite inventory and retain actual assertion results. Skipped fixture-gated cases are reported as partial coverage. The feature map in the existing formal skill links these tests to applicable model guarantees and names remaining browser/storage gaps.

Builder lifecycle and reconcile safety use bounded TLA+ models and mounted code:

```sh
node scripts/setup-formal-tools.mjs
node scripts/verify-builder-formal-core.mjs --model lifecycle
node scripts/verify-builder-formal-core.mjs --model reconcile
```

Requires Node 22.18+, Java 21, curl, Python 3, and UI dependencies.
Each invocation checks reviewed source and pinned TLC/upstream tool hashes, explores the bounded model,
mounts the real implementation with controlled response schedules, and runs TLC
on its observed states. The Apply model also drives every abstract transition
against the production synchronous ownership component. `--self-test` adds model, trace-integrity, and real-source
mutation controls. `--out /tmp/loom-formal-review` retains evidence without
checking generated reports into Git. Child checks have a 30-second timeout;
`--models-only` explicitly skips mounted code and is not implementation proof.

The existing skill at
`.codex/skills/builder-interaction-formal-verification/SKILL.md` records mappings,
bounds, findings, and a generated Mermaid state diagram. Use pstack `/statechart`
for every result; the runner already imports its pinned parser/IR/Mermaid API
with `verification/builder-interactions/statechart.view.mjs`. Models exceeding
the upstream diagram budget retain reports; scoped trace samples have explicit
coverage metadata. CI shards the batch instead of imposing a 30-second batch limit.

The refinement loop has closed two real ownership gaps. `beginApply` now returns
an attempt-bound `{ proposalId, finish }` handle: old/duplicate completions cannot
settle another Apply, and a completion in a changed workspace cannot claim the
current editor. `BuilderWorkspace` performs editor cleanup only when that handle
still owns the current context. Receipt IDs are not used as attempt identities.

The lifecycle area includes `ApplySettlementRace.tla` and mounted regressions for
cancel/reapply, both completion orders, duplicate calls, reused receipt IDs, and
nine independently changed workspace fields. The focused model also runs with:

```sh
node scripts/verify-builder-formal-core.mjs --model apply-settlement --models-only
```

Use the existing formal skill as a code-refinement workflow: find a counterexample,
replay it on production code, fix the scoped product behavior, re-run regressions
and negative controls, update the model/diagram, then explore adjacent schedules.
A framework-only change is not a completed product refinement. The command checks
and reports; the agent skill owns diagnosis and reviewed code changes.

Historical browser reports under `docs/verification/` are records only. They
are not executable tests or current-source proof.
