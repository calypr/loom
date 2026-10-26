# MJS focused-check integration

The family rules in `verification/builder-interactions/mjs-focused-check-families.json` discover matching Node tests through the existing obligation import edges. The migration command validates each test-to-oracle-to-workflow-to-case chain, applies a generated JSON attachment consumed by the canonical registry, and adds the matching source-claim links. It does not infer TLA coverage.

## Recreate the initial application

The accepted non-empty plan was generated from clean pre-apply baseline `66c55c067`. The exact reviewed plan is [`evidence/initial-applied-plan.json`](evidence/initial-applied-plan.json), SHA-256 `7352000f4e7a0ef14802095faf3deb1ce8513a36ec38c5b07129a6b6a6a345aa`. It records two attachments, twelve domain claim edits, and two discovery edits with token `b2c9ac4cfde17ad3b5535ead0df5954d9823bd06f3104804684df01acdef7f2e`.

Recreate that plan in a separate worktree at the baseline:

```bash
git worktree add --detach /tmp/loom-mjs-focused-check-baseline 66c55c067
cd /tmp/loom-mjs-focused-check-baseline
node scripts/build-test-obligations.mjs --check
node scripts/migrate-mjs-focused-checks.mjs --plan > /tmp/mjs-focused-check-plan.json
```

Review the plan and apply its token. Then regenerate obligations for the new claim links and verify the registry consumes the attachments:

```bash
node scripts/migrate-mjs-focused-checks.mjs --apply --plan-token b2c9ac4cfde17ad3b5535ead0df5954d9823bd06f3104804684df01acdef7f2e
node scripts/build-test-obligations.mjs
node scripts/build-test-obligations.mjs --check
node scripts/migrate-mjs-focused-checks.mjs --check
```

On the current applied checkout, `--plan` correctly reports a no-op with zero pending edits; it will not recreate the initial twelve claim edits. The current no-op token is `bb0a25ded72289dcea6a27f95610bee659fa8c2492ae10cc36b76fd01289972d`.

Run the registered checks through the existing runner:

```bash
node scripts/run-native-verification-bracket.mjs \
  --batch docs/verification/mjs-focused-check-migration-20261025/focused-check-batch.json \
  --checks-only \
  --evidence-parent /tmp/loom-mjs-focused-check-evidence \
  --concurrency 1
```

For a fresh runtime join, pass both emitted case `summaryPath` values to `node scripts/build-test-obligations.mjs --runtime-summary <summary> --runtime-summary <summary> --runtime-output <external-json>`. It credits only the two executed test files as `focused-command-passed`.

## Retained proof

The generated attachment adds `group-pivot-join-raw-oracle` to `cda-workspace-combine:group-pivot-join` and `diagnostic-report-append-raw-oracle` to `cda-current-draft-diagnostic-report-append:diagnostic-report-append`. The exact source tests remain unchanged. The check-only batch passed all 7 Group Pivot tests and all 5 DiagnosticReport tests with no skips; its batch and case summaries, TAP logs, and focused-command join are under `evidence/`.

The source pin manifest now contains 78 entries, including the registry and generated attachment; all hashes matched and its inventory source commit remained unchanged.

The exact command `node --test scripts/verify-ui/helpers/tests/mjs-focused-check-migration.test.mjs` passes 9/9, including stale-hash rejection, ambiguity rejection, registry-consumption rollback, orphaned-group rejection, and no-op idempotence. Its TAP output is `evidence/migration-tool-selftest.stdout`. A disposable copy with one intentionally failing focused test returns `failureCategory: focused-check`, exit 1, and `nativeStatus: not-run`; its summary and log are under `evidence/focused-failure-probe/`.

After the applied patch and catalogue-anchor correction, a fresh plan was a no-op with token `bb0a25ded72289dcea6a27f95610bee659fa8c2492ae10cc36b76fd01289972d`. Applying that token printed `NO-OP`; byte comparisons remained equal for the attachment (`096b07f89211456abfe052da7a390ef945e9589494eb96436ff703a0a6590624`) and semantic catalogue (`a5f3a04969817d98366aa1728ddc9bc3fea9ab1f9fdf8a8c3d980bd4961e6675`). The output is `evidence/noop-apply.stdout`.

These are focused command results. The batch did not run a browser case, shared bracket, native lifecycle, or TLA model. DiagnosticReport native data remains unavailable for this environment.
