# Iterate on source

A developer saves code and observes the result in the isolated application
without rebuilding production images for each edit.

## Sub-features

- `dev-own` checks source ownership and isolated ports.
- `dev-ui` observes package-source CSS through Vite HMR.
- `dev-api` observes a successful server rebuild and new process.
- `dev-error` rejects failed compilation and verifies recovery.

## How to get to it (user POV)

Run `make dev` and open its URL. Edit UI package or Go server source. Use
`make dev-rebuild` after dependency or toolchain changes.

## Driving it with loom-dev

Preconditions: Docker and Chrome are available; `make dev-doctor` succeeds.
Do not edit source or run another probe concurrently.

- Run `make verify-full`. It drives the browser scenario, performs temporary
  source edits, and restores the original content.
- Require a computed-style change, a process running successfully compiled new
  backend source, and deliberate syntax-error failure/recovery evidence.
  Read the measured timings in the report.
- Run `make dev-doctor` afterward. Source/build and service checks must pass.
- Run `make dev-down` to stop owned services while preserving evidence and
  volumes. Run `make dev` to resume.

## Gotchas

- HTTP readiness alone can belong to an old binary. Check source, built
  binary, and running-process identity together.
- Cold dependency builds are not warm iteration timings.
- Stopping services retains synthetic projects. Only explicit owned-volume
  purge discards them and their materializations.
- Probes do not replace unit tests, production builds, or real-data acceptance.

## Narrow iteration-only recipe

`make verify-current` uses the existing configured bootstrap workspace, without
the full journey's fixture-seeding branch. It exercises CSS HMR, successful
backend rebuild, deliberate compilation failure and recovery. It still owns
temporary watched-source probes and must run alone, with the same validated
owned target environment used by the doctor.

2026-10-11: the native iteration-only recipe passed in 29.7 seconds. The broader
`verify-full` attempt stopped earlier at its owner-records inspector locator;
that failure does not invalidate the separate iteration pass or establish a
full journey pass. The existing owned CDA stack was reused with its separate
synthetic routing project; no CDA reload was performed.
