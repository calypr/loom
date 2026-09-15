# Fast local Explorer development

Use this guide when you need to edit Loom backend or Explorer UI code and
verify the result through a real browser. The development stack uses the
separate Compose project `loom-dev`, ports `8180` and `3180`, an isolated
ArangoDB volume, an isolated ClickHouse volume, and the checked-in fixture in
`testdata/devloop-fixture`.

The stack does not use `loom-demo`, `NCPI_ACCEPTANCE`, the repository `CONFIG`,
or a researcher-owned Explorer. The Vite service proxies `/api` and
`/graphql` to `loom-api` on the Compose network, so browser requests stay
same-origin.

The CLI honors the caller's Docker environment. If Docker is not using its
default socket or configuration, export `DOCKER_HOST` and `DOCKER_CONFIG`
before running the Make targets. The CLI has no machine-specific Docker
fallbacks.

## Start the development stack

Requires Node.js 22 or newer, Docker with Compose, and Chrome or Chromium for
browser verification.

Run:

```bash
make dev
```

The command starts ArangoDB, ClickHouse, the Air-watched Go server, and the
Vite server. The first run builds the development images. Later runs attach to
the same services and reuse the seeded generation. The loader posts the two
fixture files to the real generation API only when the target generation does
not exist.

Open `http://127.0.0.1:3180` for manual exploration. Use
`http://127.0.0.1:8180` for the API. Change `LOOM_DEV_API_PORT` and
`LOOM_DEV_UI_PORT` when those ports are occupied.

Use `make dev-rebuild` after changing `go.mod`, `go.sum`, npm dependencies, the
Go toolchain, or a development Dockerfile. Source edits do not need an image
rebuild. Air watches `cmd`, `internal`, `generated`, and `schemas`. Vite watches
the demo source and `packages/loom-ui/src`. The watchers never scan
`CDA-FHIR`, `META_SMALL`, `node_modules`, `.gitnexus`, or `.audit`.

Check the target without opening a browser:

```bash
make dev-doctor
```

The command prints `DEV_DOCTOR_PASSED` only when the development API, UI, and
fixture generation return HTTP 200. It writes `.artifacts/loom-dev/report.json`.

## Run browser verification

Run the short browser path:

```bash
make verify-fast
```

The driver launches a temporary headless Chrome profile through the Chrome
DevTools Protocol. Each run checks that its unique namespaced project
`loom_dev_verify_<run-id>` has no Explorers or fixture generation, seeds the fixture, and creates a
new Explorer in the Builder. It starts a Patient query, adds the
`Patient -> Observation` relationship, selects nested Patient name fields and
an Observation scalar, previews the exact table, and publishes through the UI.
It then reads the new materialization through `/graphql/graph`, switches to
Viewer, applies the gender filter, clicks `Download CSV`, parses the exact
physical-column CSV, reloads Viewer, and checks that the published data remains
available.

The stable `loom_dev_fixture` project and `loom-dev-bootstrap` Explorer are
used by `make dev` and Doctor. Verification projects are retained because the
backend has no Explorer-delete operation. `make dev-down` stops services;
`node scripts/loom-dev.mjs dev-down --purge` removes the exact development
volumes and their retained per-run projects.

Run the longer path when you need watcher evidence:

```bash
make verify-full
```

The full path adds an observable package-source Vite CSS edit and restore, then
creates a temporary success probe in the compiled Go server package and proves
its unique marker executes in a fresh binary. It then introduces a separate
syntax error in an exact Go probe file, checks that Air reports the failed
build and stops the stale API, restores the probe, and waits for a fresh build
and `/readyz` to recover. The driver restores every temporary source edit in a
`finally` block and refuses to overwrite a concurrent edit.

Set `CHROME_BIN` when Chrome is not installed at a standard path. Set
`LOOM_DEV_ARTIFACTS` to an owned directory when you need evidence outside the
repository. Reports include assertion results, timings, the target session,
the materialization identity, DOM snapshots, CSV output, and the failed-build
log. They never include credentials.

Stop services and keep their isolated data:

```bash
make dev-down
```

Remove the exact development volumes and retained per-run projects as well:

```bash
node scripts/loom-dev.mjs dev-down --purge
```

The purge flag applies only to the validated `LOOM_DEV_COMPOSE_PROJECT`.

## Fixture and known limits

The fixture has two Patients and two related Observations. Patient 001 has two
names and two identifiers. Patient 002 has no `gender`. Observation 001 has a
numeric `valueQuantity` and a nested component array. These literal values make
the preview, materialization, filter, and CSV assertions deterministic.

The verification case uses one related resource and does not assert the
existing multi-related-resource `FIRST` limitation. The case does not modify
the canonical demo or repository configuration. Read
`.codex/skills/verify/SKILL.md` for the driver workflow and evidence contract.
