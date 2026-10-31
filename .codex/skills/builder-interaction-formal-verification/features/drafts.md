# Draft editing and restore

Users edit the current draft, recover from reconcile failures, and restore the previous server revision without changing the published revision.

## Sub-features

Route legality; reducer changes; server synchronization; reconcile acceptance; CAS commands and Undo.

## How to get to it (user POV)

In Builder select an existing table, edit its construction, Apply or Cancel, then use the construction Undo control (`construction-undo`) when a previous revision is available.

## Driving it with Vitest

Preconditions: installed UI dependencies; repository root; fresh output directory. No running app or token is needed for this stand-in.

- Run `node scripts/verify-builder-formal-core.mjs --phase behavior --feature drafts --out /tmp/loom-drafts-review`.
- The actual existing suites (authoring/*.unit.test.ts and BuilderWorkspace*.test.tsx) exercise production components/functions. Read every failed assertion in `behavior/vitest.json`; don't infer correctness from a rendered fixture or a suite name.
- Require executed assertions and inspect skipped names in `behavior/behavior.json`. `partial` means listed paths remain unverified.
- Formal link: ReconcileLifecycle covers ordering/retry ownership; ReconcileAcceptance generates all bounded decoded-response admission cases against the real synchronous helper. Neither proves storage.

## Gotchas

Four default operation-history cases and seven comprehensive editor/client cases execute from local fixtures. External generated replay modes still need their own trace/seed inputs and are not implied by these bounded cases. Workspace command mocks prove submitted commands, not storage or exact saved-route row results.

The connected public API restore recipe is:

```sh
node scripts/verify-builder-formal-core.mjs --phase api --feature restore
```

Use the repo's Go 1.26.5 toolchain with dependencies installed. It POSTs a restore through production routes, makes fresh GETs, compares the exact restored workspace/version/digest, and checks duplicate, stale-CAS and stale-source requests leave the result unchanged. The fixture repository is in memory; this is HTTP/service read-back, not a restarted database or browser reload. Preserve stdout/exit evidence outside Git. No exact saved-route row guarantee is inferred from a title/workspace fixture.

Actual saved Related route coverage is connected as `--phase api --feature route-roundtrip` (fast structural/compiler/HTTP) and explicit `--feature route-rows` with `LOOM_TEST_ARANGO_URL` (real graph rows/repository). Literal A rows `p1/o1,p2/o2` become B `p1/o2` and restore to exact A. Stable IDs, versions/digests, stale/duplicate/late Apply, history and fresh independent repository reads are asserted. Tests own a UUID database and remove it; no browser or database restart is inferred.
