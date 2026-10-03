# Preview

## Sub-features

Reconcile, receipt selection, output selection, bounded row preview, and diagnostic recovery.

## How to get to it (user POV)

Build a document with selected columns, reconcile it, and request Preview for one output.

## Driving it with Chrome CDP

Not automated yet. A future driver must verify the receipt ID, emitted columns, row count, and visible preview table.

## Gotchas

Preview uses an immutable receipt. Stale workspace state must be reconciled instead of silently recompiled.
