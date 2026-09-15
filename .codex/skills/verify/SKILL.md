---
name: verify
description: Drive the isolated local Loom development stack through Builder, Preview, Publish, Viewer, filter, export, reload, and evidence checks.
---

# Verify Loom development

Use this skill for frontend and backend iterations against the development
Compose project. It does not drive the canonical `loom-demo` deployment.

## Launch

Start or attach to the isolated stack:

```bash
make dev
```

The target uses `loom-dev`, `http://127.0.0.1:8180`,
`http://127.0.0.1:3180`, and `testdata/devloop-fixture`. Source edits flow
through the mounted Go and Vite watchers. Use `make dev-rebuild` only after a
dependency, toolchain, or development image change.

## Doctor

Check the service and fixture without opening Chrome:

```bash
make dev-doctor
```

Proceed only after `DEV_DOCTOR_PASSED`. The report records the validated
Compose project, fixture project, fixture generation, API status, and UI
status.

## Drive

Run the browser path:

```bash
make verify-fast
```

The driver launches a temporary headless Chrome profile through CDP. It uses
accessible roles, labels, and visible text to create a new per-run Explorer,
create a table, choose Patient as the root, add the supported
`Patient -> Observation` relationship, choose nested and scalar fields, click
Preview, and click Publish. It then reads the published materialization through
the API as independent proof, opens Viewer, loads and applies a filter, clicks
Download CSV, parses the CSV, and reloads Viewer.

Every run uses a unique `loom_dev_verify_<run-id>` project and checks that it
has no Explorers or fixture generation before seeding. The stable
`loom_dev_fixture` project and `loom-dev-bootstrap` Explorer are used only by
Launch and Doctor. Verification projects and materializations are retained:
the backend has no Explorer-delete operation. `make dev-down` stops only the
owned stack, while `node scripts/loom-dev.mjs dev-down --purge` removes its
exact development volumes.

Run the timing and recovery checks with:

```bash
make verify-full
```

The driver performs a harmless aliased package-source CSS edit and restore,
then creates an exact compiled Go success probe and proves its unique marker
executes in a fresh binary. It removes that probe, creates a separate
syntax-error probe, requires the stale API to stop and the current probe name
to appear in logs, then restores the probe and requires a fresh build stamp and
`/readyz` recovery. Source restoration has an identity guard and Chrome exits
before its temporary profile is removed.

## Evidence

Each browser run retains `.artifacts/loom-dev/<run-id>/report.json` alongside
its DOM evidence. `.artifacts/loom-dev/report.json` holds the latest command
report. These reports contain `status`,
`scenario`, `target`, `assertions`, `timings`, and `evidencePaths`.

Evidence includes initial, preview, and post-reload DOM snapshots, the parsed
CSV, the new materialization identity, and the failed-build log when the full
path runs. The driver does not write credentials, raw network traces, or
authorization headers.

## Cleanup

Stop the services and keep the isolated database volumes:

```bash
make dev-down
```

Remove only the validated development volumes when the fixture data is no
longer needed:

```bash
node scripts/loom-dev.mjs dev-down --purge
```

The cleanup command validates the complete owned Compose identity, service
ports, source mounts, and labeled volumes before it starts. It rejects
`loom-demo`, `NCPI_ACCEPTANCE`, and any other unowned project, and never calls
an unsupported Explorer-delete endpoint.

## Helpers and feature map

Run session-safety tests with:

```bash
node --test scripts/loom-dev.test.mjs
```

The implemented feature map is in [features/README.md](features/README.md).
The implemented checks are:

| Feature | Driver proof |
| --- | --- |
| Isolated target | Compose project, ports, volumes, and fixture validation |
| Source iteration | Vite CSS HMR and Air build recovery in `verify-full` |
| Builder authoring | Explorer creation, table creation, root and relationship controls |
| Preview | Literal fixture rows, nested family values, related Observation value, and physical-column contract |
| Publication | New runtime and materialization identity for the current fixture generation |
| Viewer | Filtered rows, parsed CSV, and data after reload |

The fixture intentionally does not claim correctness for multiple related
resources projected with `FIRST`. The existing `verify-loom-ui` skill targets
the authenticated Kubernetes Builder and must not be used for this local
workflow.
