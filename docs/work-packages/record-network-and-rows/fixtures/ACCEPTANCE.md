# GRAPH-ROWS independent acceptance specification

Implementation and independent fixture checks are integrated; final native acceptance is in progress. Seeds and
literal exact expectations are in `testdata/graph-rows/`. The default recipe
reference remains unchanged. Fixture helper tests are not product passes.

Freeze the final integrated source snapshot before a native run. Retain per-file
hashes, aggregate source fingerprint and API build identity before/after; changed
source invalidates the run. Record commands, exit codes, first-failure action,
elapsed time and sanitized request/error diagnostics. Native input must use real
controls; read-only result checks may inspect saved documents and receipts.

| Native workflow | Required proof | Status |
| --- | --- | --- |
| Default import | Import unmodified reference; all five outputs retain traversals/settings/fields and immutable pin/digest | current frozen snapshot imported pass |
| Carry-in source step | Preview bounded source-step edit; Cancel; Preview/Apply; reload; restore original settings; reload exact values; revision changes recorded | incomplete: see current case outcomes in VERIFICATION.md |
| Carry-in Group | Preview complete key tuple, summaries and retained contributors; Cancel without saved mutation | incomplete: see current case outcomes in VERIFICATION.md |
| Imported relationship | Add catalog-authorized optional relationship through native overlay; Preview exact four rows; Cancel; Preview/Apply; reload; edit required policy; Preview three rows; Apply/reload; remove/reload; immutable pin/digest unchanged | current frozen snapshot imported pass |
| Ordinary relationship | Native ADD_ROUTE add/edit/removal preserves Document.route and reload semantics | current frozen snapshot ordinary pass |
| Related expansion | Explicit RELATED_EXPAND preview five input/Observation pairs; preserve-parent six rows; Cancel/Apply/edit/removal/reload; show repeated upstream values | current snapshot incomplete: initial tab click exceeds five seconds |
| Independent branches | Optional lists remain four rows; unsupported 2×3 expansion composition rejects without mutation or downstream deletion | production contract/real-Arango checks pass; see verification record for exact scope |
| Source item rows | Source EXPANDED uses identifier item identity and explicit empty policy, separate from related records | production contract/real-Arango checks pass; see verification record for exact scope |
| Group contributors | Complete gender/status tuple; missing-key keep/drop; separate summaries and retained values; Apply/edit/reload | incomplete: see current case outcomes in VERIFICATION.md |
| Upstream dependency | Downstream column/filter/Group references survive valid edits; invalid removal offers explicit repair or rejects atomically across outputs | incomplete: see current case outcomes in VERIFICATION.md |
| Conflict | Two editors with same version/digest: first Apply succeeds, second real stale draft conflicts/reconciles; late proposal cannot resurrect removed relationship | current frozen snapshot imported pass |
| Root boundary | Imported preserve-root change rejected; explicit replacement discloses detach/reset before confirmation | production contract/real-Arango checks pass; see verification record for exact scope |
| Interaction/layout | 1536×900 useful preview with graph/row/inspector, keyboard traversal, bounded catalogs, 1536×1100 and back, 100-operation ordered groups | incomplete: see current case outcomes in VERIFICATION.md |
| Authenticated Calypr | Doctor first, then integrated lifecycle on authorized disposable target; load alone cannot pass | blocked: no short-lived token configured |

The owned local `loom-dev-cloud` stack now runs the frozen implementation in a
separate checkout, with the unchanged default recipe and production-validated
fixture generation. No production changes or publication are authorized.

The six registered cases are `imported`, `ordinary`, `carry-in`, `related-rows`,
`upstream-dependency` and `history-stress`. Execute one at a time against the validated owned target:

```bash
node scripts/run-graph-rows-native.mjs --case imported
```

Use `--checks-only` for focused oracle/integrity checks without claiming native
acceptance. Full runs require the owned target environment and exact runtime
configuration paths; the verifier rejects stale source/API/configuration.
The operator removed the five-second timing gate on 2026-11-13. Use bounded
30s functional action/request deadlines and zero retries, retaining every exact
value, identity, pin, persistence and CAS assertion. Record actual latencies and
failed attempts separately; functional passes do not establish a five-second SLA. [../VERIFICATION.md](../VERIFICATION.md) records
commands, evidence, review identity, final coverage and remaining limitations.

## Owned local target configuration

The native cases require a physically separate, frozen source checkout and an
owned development stack validated by the current verification skill. The API
must load this package's unchanged default recipe, and the fixture generation
must come from `testdata/graph-rows` through production ingest.

For the recorded run, the source checkout is `/workspace/loom-runtime`, Compose
project `loom-dev-cloud`, API `http://127.0.0.1:8180`, UI
`http://127.0.0.1:3180`, fixture project `loom_dev_graph_rows`, generation
`fixture-v2`. The local Compose override mounts the reference read-only at
`/workspace/graph-rows-default.json` and the exact Air configuration read-only at
`/workspace/.air.dev.toml`. Air's normal server command adds
`--dataframer-recipe /workspace/graph-rows-default.json --recipe-query-page-rows 250`.
The recorded serving/build profile is `GOMAXPROCS=2`, `GOFLAGS=-p=1`.
These are local development settings, not deployment instructions.

Set `LOOM_DEV_SOURCE_ROOT`, `LOOM_DEV_COMPOSE_PROJECT`, `LOOM_DEV_PROJECT`,
`LOOM_DEV_GENERATION`, `LOOM_DEV_API_PORT`, `LOOM_DEV_UI_PORT` and
`LOOM_DEV_ARTIFACTS` to that validated target. Also set
`LOOM_GRAPH_ROWS_RUNTIME_CONFIG` to the exact host Air file,
`LOOM_GRAPH_ROWS_COMPOSE_CONFIG` to the exact local override, and
`LOOM_GRAPH_ROWS_EVIDENCE` to a fresh per-case directory. The identity verifier
compares the files with the live mounted configuration, source and binary; these
settings cannot waive a stale-source failure. Run doctor before browser input.

Run the six cases serially while sharing a runtime. The history runner first
prepares 100 real sequential proposals/receipt previews/CAS Applies under a
separate 900s setup deadline with 30s setup requests. It then launches the official
native test with its unchanged 180s test deadline and 30s functional action/request bounds.
A fresh-fixture process deadline must allow both phases (1140s in the recorded
creation run). Final functional runs retain a 180s native test deadline in every
case, with 30s expect/action/navigation/API bounds and zero retries. Their
surrounding deadline is 240s for ordinary cases and 420s for loaded history,
allowing its separate 120s current-source admission plus native execution. Report fixture setup separately
from native interactions, and never claim 100 native creation lifecycles.

A completed 100-operation fixture can be reused without replaying its creation:

```bash
node scripts/run-graph-rows-native.mjs --case history-stress \
  --reuse-history-fixture /private/evidence/history-preparation.json \
  --history-fixture-sha256 <original-file-sha256>
```

Reuse is a separate current-source validation phase. It verifies the original
creation proof, owned target, immutable reference, unchanged generation and saved
draft, all 100 operation IDs/inputs/schemas, original source values and all 300
calculated cells. It reconciles and previews through current production APIs,
then records new before/after source and binary identities. Native handoff still
requires exact equality with that current verification identity. It neither
rewrites the original evidence nor accepts a stale API. Evidence files are
created exclusively with mode 0600, including errors; normal and reused fixture
reports omit development session secrets.
