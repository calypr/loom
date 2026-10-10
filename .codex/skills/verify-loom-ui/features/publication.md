# Publication

## Sub-features

Receipt publication, materialization, activation, and Viewer availability.

## How to get to it (user POV)

Preview a valid output and publish the reconciled receipt.

## Driving it with Chrome CDP

Not automated yet. Use a disposable Explorer and verify both the visible READY state and the corresponding Loom execution and ClickHouse materialization.

## Gotchas

Publication mutates active state and requires write authorization. Keep it outside the read-only local smoke test.
