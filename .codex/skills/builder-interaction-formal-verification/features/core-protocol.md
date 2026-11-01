# Core authoring protocol

Saved-head reconciliation, immutable compile receipts, and receipt-bound preview execution, including one competing save racing a captured-head reconcile.

## Sub-features

Entry CAS and capture-at-entry; receipt persistence and frozen replay; stale-save rejection; client admission of the requested receipt echo.

## How to get to it (user POV)

In Builder rename a column (save), then change identity again while an earlier reconcile is still compiling; the completed reconcile must describe its captured head, and the preview must show only the receipt the client currently holds.

## Driving it with Vitest

Preconditions: installed UI dependencies; repository root; fresh output directory. No running app or token is needed.

- Run `node scripts/verify-builder-formal-core.mjs --phase behavior --feature core-protocol --out /tmp/loom-core-protocol-review`.
- The mounted driver (`src/features/ExplorerBuilder/BuilderWorkspace.coreProtocol.formal.test.tsx`) exercises production `useAutomaticPreview`, `PreviewTable`, and the real admission/discard paths in `BuilderWorkspace.tsx`; read every failed assertion in `behavior/vitest.json` rather than trusting the suite name.
- Server side: `node scripts/verify-builder-formal-core.mjs --phase api --feature core-protocol --out /tmp/loom-core-protocol-api` runs the named Go witnesses (vertical witness, competing-save-during-reconcile, error boundaries). The eight-schedule model replay is a SEPARATE check: `node scripts/verify-builder-formal-core.mjs --model core-protocol --phase generated` drives `TestCoreAuthoringProtocolModelReplay` + the concrete-prefix lane through `scripts/lib/formal-core-protocol-replay.mjs`.
- Formal link: `CoreAuthoringProtocol` (bounded TLC, 69 states) with generated-code replay; the authoritative operation table lives in `../core-protocol.md`. Arango head/receipt durability is witnessed by `TestCoreAuthoringProtocolAgainstArango` and dual-store replay (see `../core-protocol.md`).
