# GRAPH-ROWS independent acceptance specification

Implementation and independent fixture checks are integrated; final native acceptance is in progress. Seeds and
literal exact expectations are in `testdata/graph-rows/`. The default recipe
reference remains unchanged. Fixture helper tests are not product passes.

Freeze the final integrated source snapshot before a native run. Retain per-file
hashes, aggregate source fingerprint and API build identity before/after; changed
source invalidates the run. Record commands, exit codes, first-failure action,
elapsed time and sanitized request/error diagnostics. Native input must use real
controls; read-only result checks may inspect saved documents and receipts.

## Current follow-up native matrix: 11 of 12 whole cases pass

Imported passes at `00990f30edd660c72d6cfa6c251e0033ddda4a544527768a866b8f54eb7b2ae1`.
The later first-table scheduling repair changes current source (`557e277f75787a93245e18d7ee0a285c61e782b80e48ac2660290b0c660033a7`).
Ordinary attempt 2 completed functional actions but remains incomplete because
the strict semantic-inventory check observed a 409 after REMOVE_ROUTE. The closed
Advanced catalog gate now has controlled native red/green proof; ordinary attempt 3 now passes at cec3182784afcb07ec1926ef3a49757085c2fd9db380322ec7947f4056fb40e4
(26.431s, zero errors/409).
Repeated final UI build now passes (34.02s/24.95s Vite plus TypeScript). Carry-in-4
passes at `45142ac0e16c1a5fdf2c066bb0e526e512e076cd5b8dd9bd04b8b4ce57ebb3e1`;
related and upstream whole cases pass. Mixed scalar Preview exposed a confirmed
backend runtime-metadata defect; compiler-21 repair/review now passes, but the final
compiler-21 matrix remains pending at 8d23bdb2…. Group EXCLUDE adds a reviewed
23-action carry-in run; earlier carry-in-4 keep-policy pass retains its scope. Earlier imported/ordinary/graph/row passes retain their
recorded source scopes; final reruns, remaining cases and fresh history remain pending. Graph-first and row-first have earlier
whole passes and strict canonical equality at `01b7fd280661390fd82e0fa776af21f3b3967b675e31f99760bd104bc24b328f`;
rerun them against the current frozen harness before promoting those scopes.
Layout, saved-preview, ordering and focus repairs are reviewed. Earlier failures
remain in VERIFICATION.md. Registration alone remains distinct from execution.

| Case | Required current proof | Status |
| --- | --- | --- |
| imported | Import, overlay relationship lifecycle, immutable pins, real stale conflict and explicit owner recovery | PASS, imported-3, 90.519s/17 actions at 7535bdaf…; expected real 409/stale recovery retained |
| ordinary | Document.route lifecycle and reload | PASS, ordinary-4, 31.103s/6 actions at 7535bdaf… |
| carry-in | Source-step Preview/Cancel/Apply/reload/restore; complete Group tuple, summaries and contributors | PASS, carry-in-5, 100.547s/23 actions incl EXCLUDE and restore at 7535bdaf… |
| related-rows | Exact five/six related pairs, empty policy, full edit/removal/reload lifecycle | PASS, related-rows-2, 80.414s/7 actions at 7535bdaf… |
| upstream-dependency | Valid edits preserve downstream work; invalid removal rejects atomically | PASS, upstream-dependency-2, 40.467s/4 actions at 7535bdaf…; expected 422 retained |
| history-stress | Current-source admission of 100 operations and 300 cells; bounded visible ordered history | FAIL, history-stress-1 setup713.969s: 98 saved, 99th candidate exceeds30s; no native phase. Performance repair pending |
| graph-first | Native connection and source policy; exact rows/values; canonical comparison with row-first | PASS, graph-first-11, 77.863s/7 actions plus 17 layouts at 4da639e4…; exact initiating focus and strict row4 canonical PASS |
| row-first | Same supported recipe authored through editable terms; exact rows/values and canonical equivalence | PASS, row-first-4, 56.940s/5 actions at 4da639e4…; same-snapshot strict graph11 canonical PASS |
| mixed | Explicit expansion retains ALL list; separately authored per-match scalar; complete lifecycle | PASS, mixed-4, 110.432s/12 actions at c339f7e2…; prior failed attempts retained |
| earlier-term | ALL → DISTINCT → ALL preserves downstream expansion/scalar IDs, order, inputs, anchors and pin | PASS, earlier-term-1, 107.858s/8 actions at c339f7e2… |
| ordinary-policy | Exact stable-ID FIELD policy Preview/Cancel/Apply/edit/reload with literal values | PASS, ordinary-policy-2, 36.609s/3 actions at 7535bdaf… |
| root-proposal | Imported PRESERVE blocked; explicit REPLACE disclosed; Cancel intact, Apply/reload exact four Patients and unchanged other outputs | PASS, root-proposal-1, 35.280s/5 actions at 7535bdaf… |

Both editors, relevant row/traversal meaning, one inspector and useful preview
must fit at 1536×900 and 1536×1100/back, including an open proposal and 100-operation
history. Preview must remain at least 150px high; Apply/Cancel must be available
without document scrolling. Include keyboard/focus, table switching and real stale
recovery. Owned fixture success does not establish authenticated Calypr acceptance.

## Original 01–07 workflow evidence (historical)

| Native workflow | Required proof | Status |
| --- | --- | --- |
| Default import | Import unmodified reference; all five outputs retain traversals/settings/fields and immutable pin/digest | historical 01–07 imported pass |
| Carry-in source step | Preview bounded source-step edit; Cancel; Preview/Apply; reload; restore original settings; reload exact values; revision changes recorded | historical incomplete: see original case outcomes in VERIFICATION.md |
| Carry-in Group | Preview complete key tuple, summaries and retained contributors; Cancel without saved mutation | historical incomplete: see original case outcomes in VERIFICATION.md |
| Imported relationship | Add catalog-authorized optional relationship through native overlay; Preview exact four rows; Cancel; Preview/Apply; reload; edit required policy; Preview three rows; Apply/reload; remove/reload; immutable pin/digest unchanged | historical 01–07 imported pass |
| Ordinary relationship | Native ADD_ROUTE add/edit/removal preserves Document.route and reload semantics | historical 01–07 ordinary pass |
| Related expansion | Explicit RELATED_EXPAND preview five input/Observation pairs; preserve-parent six rows; Cancel/Apply/edit/removal/reload; show repeated upstream values | historical incomplete attempt; five-second gate removed |
| Independent branches | Optional lists remain four rows; unsupported 2×3 expansion composition rejects without mutation or downstream deletion | production contract/real-Arango checks pass; see verification record for exact scope |
| Source item rows | Source EXPANDED uses identifier item identity and explicit empty policy, separate from related records | production contract/real-Arango checks pass; see verification record for exact scope |
| Group contributors | Complete gender/status tuple; missing-key keep/drop; separate summaries and retained values; Apply/edit/reload | historical incomplete: see original case outcomes in VERIFICATION.md |
| Upstream dependency | Downstream column/filter/Group references survive valid edits; invalid removal offers explicit repair or rejects atomically across outputs | historical incomplete: see original case outcomes in VERIFICATION.md |
| Conflict | Two editors with same version/digest: first Apply succeeds, second real stale draft conflicts/reconciles; late proposal cannot resurrect removed relationship | historical 01–07 imported pass |
| Root boundary | Imported preserve-root change rejected; explicit replacement discloses detach/reset before confirmation | production contract/real-Arango checks pass; see verification record for exact scope |
| Interaction/layout | 1536×900 useful preview with graph/row/inspector, keyboard traversal, bounded catalogs, 1536×1100 and back, 100-operation ordered groups | historical incomplete: see original case outcomes in VERIFICATION.md |
| Authenticated Calypr | Doctor first, then integrated lifecycle on authorized disposable target; load alone cannot pass | blocked: no short-lived token configured |

The owned local `loom-dev-cloud` stack provides the independently validated target in a
separate checkout, with the unchanged default recipe and production-validated
fixture generation. No production changes or publication are authorized.

The original six registered cases were `imported`, `ordinary`, `carry-in`, `related-rows`,
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

## Follow-up acceptance for complementary editors

The scoped outcomes above belong to the original implementation. Follow-up 08–11
adds an editable abstract row recipe and its correspondence with the graph. Use
[ticket 07](../tickets/GRAPH-ROWS-07-acceptance.md#follow-up-for-the-finished-two-interface-workflow)
for graph-first, row-first, mixed-editing, proposal-ownership, and Calypr composition
requirements. Graph-first and row-first now have scoped whole passes and canonical
equality; the current matrix above tracks reruns and remaining cases separately.

Reuse this fixture and the existing runner. Equivalent recipes must produce the
same canonical semantics, exact row identities, and values. Display-only collapse
and selection do not affect equivalence. Distinct saved occurrences must retain
their own identities within each recipe. Tests must prove editable expression
terms and their effect, rather than locating only the row-settings trigger.

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

Run the twelve cases serially while sharing a runtime. The history runner first
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
