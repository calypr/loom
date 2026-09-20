# B06 reusable interpretation verification

## Outcome

B06 is runtime-proven on the isolated local Docker stack. `make verify-fast` passed 62 assertions in 19.134 seconds. The interpretation-specific create, review, revise, apply, isolation, and reload journey took 3.778 seconds.

The verified journey created an immutable v1 interpretation for `Patient.id`, reviewed and applied it, cloned a consumer pinned to v1, advanced the library head to a v2 definition backed by `Patient.gender`, reviewed changed before/after values, cancelled without changing the draft, exercised a bounded incomplete preview, applied v2, and reloaded both consumers. The original consumer remained pinned to v1. The fixture source digest remained `sha256:4b33028bf5d05107e32d88b21083d54b2eb005bf7930eb5e686d314389db1e6e`.

## Executable evidence

- `go test ./internal/explorer/... ./internal/server -count=1`
- `npm --prefix ui/packages/loom-ui test` — 25 files and 157 tests passed
- `npm --prefix ui/packages/loom-ui run build`
- `node --test scripts/loom-dev.test.mjs` — 14 tests passed
- `make openapi-check`
- `make verify-fast` — 62 live assertions passed

The live report is `.artifacts/loom-dev/6d7df93d6a37/report.json`. Per-run evidence is under `.artifacts/loom-dev/6d7df93d6a37/mu7gy5qb-a3ef9d6c/`, including the review DOM, pinned-v2 reload DOM, source/revision evidence, Builder preview, Viewer reload, and final DOM.

## Boundary found by the live run

The browser run caught two contract gaps that unit fixtures had hidden. Builder exposed only collapsed `repeated` state while compilation matched exact cardinality, so the API now carries the exact capability cardinality. Ordinary scalar receipt emissions also lacked their authored-column owner, so all physical emissions now retain that provenance. Candidate preview continues to fail closed instead of guessing.

## Known limitation

The local Compose workflow starts Loom with `--no-auth` and an allow-all authorizer. It cannot honestly demonstrate a live 401/403. Project scoping and wrong-project 404 behavior are live-proven; route tests prove the authorization-denial branch. An authenticated deployment remains the proper environment for a live denial probe.
