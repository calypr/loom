# Repository cleanup goal

## Product and verification

The frontend UI and backend API are the product. Keep a test only if it would fail
when the product behavior it names breaks. Tests must execute production code or
drive the running app, HTTP API, Arango or ClickHouse and assert observable outputs.
Codegen determinism, gofmt/vet gates and tests of retained repository tooling also stay.

Remove harness-only tests, source-text and hash pins, registries and inventories,
TLA+ models, mutation source-proof controls, log-only passes and dormant checks.
Re-route real tests before deleting their wrappers. Preserve valid and rejected
behavior cases; do not silently drop failing schedules or change their expectations.

Record deleted harness paths, immutable recovery SHAs and their real-test replacements
(or why they were dropped) in [Deleted verification guarantees](verification/DELETED_GUARANTEES.md).
Generated indexes and disposable runtime reports stay outside Git.

## Execution budgets

Every required CI job has a ten-minute budget. Run complete suites without shard
matrices. Database jobs remain separate because they need different services.
Measure workflow wall-clock time before and after changes.

Run the closest real check: `make test-ui`, `make test-go`, or
`make test-integration`. Use `make e2e` for visible Builder changes once available.
Run required acceptance against the final relevant source snapshot. Reuse evidence
only while source, configuration, assumptions and coverage remain unchanged.
Inspect saved output after an inconclusive run before changing the diagnostic or budget.
Timeouts, tool errors and malformed evidence are failures, never passes.

## Collaboration

Preserve unrelated working-tree changes. Do not reset, stash, clean, publish,
or rewrite branch history as part of cleanup. Follow ownership and dependency gates
in [cleanup plan 2](REPO_HYGIENE_CLEANUP_PLAN.md).
