# Repository cleanup goal

## Product and verification

The frontend UI and backend API are the product. Verification protects their behavior.
Keep the verification code small enough to maintain and rerun during development.

Preserve the intended guarantees when replacing old machinery. A smaller model,
a unit-test count, or a generated report does not establish equivalent coverage.
Record the deleted promises and their immutable Git recovery points in
[Deleted verification guarantees](verification/DELETED_GUARANTEES.md).
Build a current replacement for one bounded promise at a time. Keep the rest open.
Restoring the retired reporting system is not a prerequisite for new verification.

## Execution budgets

Every required CI job has a ten-minute budget. Run complete suites without shard matrices.
The ordinary Go job includes the complete catalog witness test. UI checks and the build
share one job. Node and Python each run a complete suite. Database jobs remain separate
because they need different services. The formal job runs one complete self-test.

Focused local checks default to short child-process deadlines. Those local deadlines
must not become a thirty-second CI policy. The shared verification budget module
uses ten-minute child limits in CI. Browser checks remain separately selectable.

Run the full checkpoint after a coherent change. Use focused actual-code replays
while editing. Tool, compiler, timeout, missing-case, and malformed-evidence errors block
verification. They never count as a caught defect.

## Formal implementation checks

The current formal skill owns the model, counterexample, actual-code replay,
regression, source-fault, and statechart loop. Its source map records the relevant
production functions, bounds, assumptions, and reviewed hashes.

TLC checks bounded models. Direct replay executes the named production code against
model-derived inputs or schedules. Mounted trace checks verify recorded React schedules.
Ordinary component, API, database, and browser tests remain separate evidence.
Do not add their counts into a formal-test total.

Keep valid and rejected cases. A check that ignores every completion or rejects every
construction must fail. Remove the actual production guard in a scratch overlay, keep
its concrete witness, replay it on pristine and broken source, then verify restoration.
The pinned pstack parser, IR, and Mermaid backend render model results and counterexamples.

## Historical material

Generated indexes and disposable reports stay outside Git. Authored models, decisions,
contracts, and historical native evidence have distinct value. Their deletion does not
establish that their promises were obsolete or replaced. Consult the recovery record
before porting a guarantee. Update its disposition only after executable evidence exists.

The old browser runners and report generators are retired. Historical command names
are recovery references, not runnable recipes. Current commands and limits live in the
[formal skill](../.codex/skills/builder-interaction-formal-verification/SKILL.md) and
[retained coverage decisions](verification/RETAINED_COVERAGE_DECISIONS.md).

Preserve unrelated working-tree changes. Do not reset, stash, clean, publish,
or rewrite branch history as part of cleanup.
