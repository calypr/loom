---
name: verify-publication-bundle
description: Regression + formal verification entry point for the ClickHouse publication-bundle lease/pointer/reconcile protocol (bundle_store.go, arango/bundle_registry.go). Use when touching publish/commit/abort/reconcile/cleanup-pin logic, lease TTLs, or the Arango publication documents.
---

# Verify publication bundle

Covers `internal/dataframe/publication/clickhouse/bundle_store.go`
(Begin/Commit/ConfirmCommit/Abort/Reconcile) and
`internal/dataframe/publication/arango/bundle_registry.go` (SaveExecution
fence, single-AQL PublishExecution, lease acquire/renew/release, execution read
pins, cleanup claim). Read this file first if resuming that work.

## Source identity

Pin hashes before and after every experiment; a mismatch invalidates results:

```
sha256sum internal/dataframe/publication/clickhouse/bundle_store.go \
          internal/dataframe/publication/arango/bundle_registry.go
```

Last verified: `2f5c0fda...5d6a2` / `1f0a6cde...de138` at HEAD `dc9016bc`
(recorded in `reviewed-sources.json`).

## Model checks (TLA+)

Canonical model: `BundleCommitLifecycle.tla` (+`.cfg`). Requires
`$TLA2TOOLS_JAR` (tla2tools.jar sha256 `936a2620...`, per
`verification/builder-interactions/tools.lock.json`). Run foreground with a
private `-metadir`:

```
TLC_WORKERS=4 TLC_HEAP_MB=2048 /sandbox/.local/bin/tlc-run -fp 21 -fpbits 6 \
  -metadir /tmp/tla-pb-$$ -config <cfg> BundleCommitLifecycle.tla
```

Properties and their fault controls (each fault constant is TRUE in a scratch
overlay config outside Git; run each separately):

| Invariant | Meaning | Fault constant | Expected |
| --- | --- | --- | --- |
| `InvariantNoDropUnderPointer` | never drop the pointer-advertised bundle (`:840`) | none needed | **violates by design today — this is the confirmed F-B1 finding; a PASS would be suspicious** |
| `InvariantNoDropWhenNamed` (ghost.m) | drop even though the memoised pointer read (`:1068`, `rec.ptr`/`rec.rd`) named it | `FReconcileSkipsPointerCheck = TRUE` | clean baseline, violated under fault |
| `InvariantNoDropUnderPin` (ghost.p) | drop while a reader pin is live at the drop instant (`claimExecutionCleanup :416-418`) | `FClaimIgnoresPins = TRUE` | clean baseline, violated under fault |
| `InvariantNoForeignPublish` | publish only under a live caller lease (`:289-290`) | `FNoLeaseFencePublish = TRUE` | clean baseline, violated under fault |

Model-editing rules (learned the hard way — see FINDINGS.md postmortem):
- every action must assign or UNCHANGED-declare every VARIABLE, including
  `cleanupExpires` and `ghost`; otherwise TLC silently never fires it;
- never write in-action branches as leading-`\/`/`/\` column groups; use
  `IF ... THEN ... ELSE ...`;
- deep EXCEPT must be `[rec.x EXCEPT !.y = [rec.x.y EXCEPT ![t] = v]]`;
- reconciler snapshot fields live in one `rec` record (`cur`, `cand`, `ptr`,
  `rd`); claim requires `rec.rd[t]` (read-before-claim order per `:1068`→`:1076`).

## Component + real-DB reproductions

Sanctioned red artifacts (they fail ON PURPOSE as findings; keep them red,
do not "fix" them):

```
go test -count=1 -run 'TestReconcile(Drops|Skips)' ./internal/dataframe/publication/clickhouse/
```

End-to-end on the real Arango of the owned stack (pinned container
`loom-dev-qwen-current-loom-api-1`; `/workspace` is this checkout; only
`sh -c 'cd /workspace && ...'` exec shape is permitted):

```
docker exec loom-dev-qwen-current-loom-api-1 sh -c \
  'cd /workspace && LOOM_TEST_ARANGO_URL=http://arangodb:8529 go test -count=1 \
   -run "Test(Reconcile|Abort).*AgainstArango" -v ./internal/dataframe/publication/clickhouse/'
```

Tests skip when `LOOM_TEST_ARANGO_URL` is unset. Every e2e owns a UUID database
`loom_verify_*` and must drop it via HTTP `DELETE /_api/database/<name>` in
`t.Cleanup`. After any e2e run, confirm stack residue-free:

```
docker exec loom-dev-qwen-current-loom-api-1 sh -c \
  'curl -s http://arangodb:8529/_api/database | grep -o "loom_verify_[a-z0-9_-]*"'
```

(must print nothing).

## Known open operator items

- F-B1 fix authorization (pre-drop lease re-validation / fresh GetPointer gate)
  — operator decision, never apply without approval.
- F-B2 (abort-after-loose-publish-ACK drop) — see FINDINGS.md for reproduction
  status.
- Findings, coverage ledger, and unverified scope live next to this file:
  `FINDINGS.md`, `reviewed-sources.json`.

Local TLC runs use `/sandbox/.local/bin/tlc-run` (explicit budget; split via `TLC_WORKERS`/`TLC_HEAP_MB`, one `-metadir` per run). If unreadable `/sys/fs/cgroup/{cpu,memory}.max` crashes the launcher, it needs the OSError guards (applied 2026-11-01).
