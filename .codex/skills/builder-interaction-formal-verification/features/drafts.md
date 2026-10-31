# Draft editing and restore

Users edit the current draft, recover from reconcile failures, and restore the previous server revision without changing the published revision.

## Sub-features

Route legality; reducer changes; server synchronization; reconcile acceptance; CAS commands and Undo.

## How to get to it (user POV)

In Builder select an existing table, edit its construction, Apply or Cancel, then use the construction Undo control (`construction-undo`) when a previous revision is available.

## Driving it with Vitest

Preconditions: installed UI dependencies; repository root; fresh output directory. No running app or token is needed for this stand-in.

- Run `node scripts/verify-builder-formal-core.mjs --phase behavior --feature drafts --out /tmp/loom-drafts-review`.
- The actual existing suites (authoring/*.unit.test.ts and BuilderWorkspace*.test.tsx) exercise production components/functions. Read every failed assertion in `behavior/vitest.json`; don't infer correctness from a rendered fixture or a suite name.
- Require executed assertions and inspect skipped names in `behavior/behavior.json`. `partial` means listed paths remain unverified.
- Formal link: ReconcileLifecycle covers ordering/retry ownership; ReconcileAcceptance generates all bounded decoded-response admission cases against the real synchronous helper. PublicationAcknowledgement separately checks successful publish response identity across preview recompilation. None proves storage.

## Gotchas

Five default operation-history cases and seven comprehensive editor/client cases execute from local fixtures. External generated replay modes still need their own trace/seed inputs and are not implied by these bounded cases. Workspace command mocks prove submitted commands, not storage or exact saved-route row results.

The connected public API restore recipe is:

```sh
node scripts/verify-builder-formal-core.mjs --phase api --feature restore
```

Use the repo's Go 1.26.5 toolchain with dependencies installed. It POSTs a restore through production routes, makes fresh GETs, compares the exact restored workspace/version/digest, and checks duplicate, stale-CAS and stale-source requests leave the result unchanged. The fixture repository is in memory; this is HTTP/service read-back, not a restarted database or browser reload. Preserve stdout/exit evidence outside Git. No exact saved-route row guarantee is inferred from a title/workspace fixture.

Actual saved Related route coverage is connected as `--phase api --feature route-roundtrip` (fast structural/compiler/HTTP) and explicit `--feature route-rows` with `LOOM_TEST_ARANGO_URL` (real graph rows/repository). Literal A rows `p1/o1,p2/o2` become B `p1/o2` and restore to exact A. Stable IDs, versions/digests, stale/duplicate/late Apply, history and fresh independent repository reads are asserted. Tests own a UUID database and remove it; no browser or database restart is inferred.


## Retained operation-history bridge

Run `node scripts/verify-builder-formal-core.mjs --phase journey --feature operation-history` to connect two historical Group composition witnesses through actual editor capture, actual Go identity/proposal/Apply/Preview and panel admission. The applied MEAN witness continues through fresh public Builder/capabilities read and two real component remounts, preserving Group/Filter identifiers and the saved operation. Capabilities requests must bind the captured version, digest, snapshot and output, including the source-stage request. The existing formal self-test checks independent Calculate preservation plus public-read and editor-initialization faults. This is a fixed-witness companion route with a controlled repository/catalog, not a full TLA, durable reload, aggregate-result or observed-transport guarantee. The normal five local operation-history assertions remain a separate bounded UI check.


Actual draft write conflict and rollback checks run with `--phase api --feature draft-cas` and `LOOM_TEST_ARANGO_URL` pointing to an owned local instance. They cover two competing actual transactions, a delayed loser, public lifecycle conflict classification and rollback after history INSERT. Each uses a private UUID database. The same four named cases are mandatory in the existing unsharded Arango CI profile. HTTP and lost commit acknowledgements are outside these real-database witnesses.


A mounted actual-query recovery witness now closes the boundary hidden by mocked refetch: saved v2, forced fresh GET and cache publication, loading blocks edits, new snapshot admission, then the next command uses exact fresh snapshot/version/digest and Workspace exposes exact v3 output identities. The client and mutations remain controlled. `Inv_RecoveryQueryForcesFreshBuilder` catches a real query-hook reload-forwarding fault. This is a companion transport witness, not a new TLA model or backend-cache proof.


Population exclusion now has a separate `--model population-exclusion` protocol and actual callback replay. Captured base, route/context and lifetime must still match after preflight and inside the queued write. Current local/server CAS must be coherent, while unrelated coherent saves remain valid. Six mounted cases establish real refresh/queue/UI reachability and own-success behavior; the model stops at outgoing submission, before ACK or database commit.


## Operation-history profiles

Routine CI runs `node scripts/verify-builder-formal-core.mjs --phase journey --feature operation-history-routine --out /tmp/loom-operation-history-routine` with a fresh output directory. Eleven representative schedules retain all five saved construction shapes (Group alone, Group/Filter, Group/Calculate, both downstream steps, and removal of a key-dependent Filter while preserving Calculate), one Cancel/no-save, the panel's reviewed cascade, restored-key identity, replacement boolean-key identity, saved MEAN initialization and MEAN→COUNT_ROWS type propagation. The reviewed-cascade checkpoint intentionally overlaps the applied cascade: the panel assertion consumes that exact proposal. The original two-witness `operation-history` profile remains the focused source-control route.

The eleven schedules are an explicit seed/action table in `scripts/lib/formal-operation-history-model.mjs`. The retained interpreter and independent cascade oracle derive their expected observations. The 78-case breadth search and optional full-profile command were removed after no additional fault detection had been demonstrated for them. The original generator remains recoverable with `git show 5646633e:verification/builder-interactions/operationHistoryModel.mjs`.

The omitted cases covered repeated key-restoration histories, alternative edit orders, source-key availability and other proposal-only combinations. Removing them deliberately reduces explored histories. They were not proved equivalent to the retained schedules or subsumed by current TLA checks. Recover a particular schedule when a bug, changed production branch or new property gives it a concrete purpose.

Each selected case's input and expectations must match the fixed witness definitions. Before removing the search, all eleven records were compared exactly against its output. Missing, duplicate, foreign or changed evidence blocks. The same three production fault controls are checked against the smaller profile: collateral Calculate removal, corrupted public saved-Group read and wrong saved-MEAN editor initialization. These controls establish retained detection for those faults, not every omitted behavior.

All five saved cases receive actual Go service Apply and fresh Builder/capabilities read-back against a controlled repository. Only the original saved Group/Filter witness continues through two mounted editor remounts. Catalog, preview executor and repository remain controlled. Actual aggregate query rows, durable database/browser reload, restore/late-response ordering, arbitrary histories and full old BuilderJourney/TLA equivalence remain outside this bound. No product promise changed during downsizing; only routine exploration scope changed.
