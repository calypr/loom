# GRAPH-ROWS verification record

## Scope and source

The implementation starts at `1685bc4c2adda944e380e721a80fcdcfbf04def5`
on `feature/default-recipe-authoring`, containing
`4b64797e3dcf391114471e86555ad511bb084b0d`. Both were confirmed on the
requested remote branch before implementation. No alternate baseline was used.

The default recipe remains byte-for-byte unchanged: SHA-256
`998c6aead0d3f432f3f0cf4d3fb515cbbacda9af53c0c090f457c3931b709dad`.
Go and UI authoring semantics are version **12**; accepted version 11 documents
retain the explicit legacy adapter until a current command migrates them.
Compiler contract **19** uses execution fingerprint algorithm **3**, including
source dynamic scalar/list cardinality. Historical ordinary receipts remain
readable but require explicit recompilation before execution; their compilation
cache keys differ. Historical proposal receipts cannot authorize an Apply.
The immutable recipe pin and authored draft are preserved during recompilation.

Ordinary documents own `Route` and `Rows`. Imported documents own the immutable
recipe pin plus the typed project-local source overlay. The authoring network
is derived, never a second persisted canvas. Project additions have stable
`project_` identities and exact authorized catalog edges. Source-step,
occurrence, compiled column and construction anchor bindings are explicit.
Imported graph commands use receipt-backed source proposals; ordinary graph
commands retain `ADD_ROUTE` and `REMOVE_ROUTE`.

Optional related values preserve rows. Required matching filters the applicable
path; it does not expand rows. Source item expansion, per-input related-record
pairs, named source groups and construction Group key tuples remain distinct.
Independent related expansions that would require an unspecified product are
rejected. Imported preserve-mode root edits remain unavailable; deliberate
replacement discloses pin detachment and reset.

The current frozen API source identity is
`8d6b1c244537b92e1eaafa50b4da24c1952310186f3c18c4361d907f29197a27`;
its binary SHA-256 is
`c956bddd7a4029e8a91b7939f6f2b621fb88204c7c3574846799ade65eefff28`.
The Group runtime repair is complete and reviewed. Final integrated source and
commit identity remain pending while native acceptance is incomplete.

## Automated evidence

Raw development evidence is retained locally under `work/graph-rows/` and
`work/graph-rows-runtime/`; these disposable logs are intentionally untracked.

| Check | Result | Evidence |
| --- | --- | --- |
| Full authoringv2, recipeimport, lifecycle suites | pass | `backend-frozen.log` (0.323s / 0.105s / 26.952s) |
| Final full IR, compiler/lower, AQL renderer and execution suites | pass | `group-runtime-packages.log` (0.018s / 8.331s / 0.072s / 11.800s) |
| Public HTTP proposals, strict intents and OpenAPI ownership | pass | `server-final.log`, `openapi-ownership.log` |
| Final registered draft behavior | 193 passed, zero skipped | `behavior-drafts-complete.log`; `/tmp/loom-graph-rows-drafts-complete` (46.50s) |
| Final registered real-Arango graph fixture | ten original cases plus seven dynamic-source Group cases pass | `api-complete.log`; `/tmp/loom-graph-rows-api-complete` (8.66s including build) |
| Production fixture ingest validation | pass | `fixture-ingest-final.log` (5.310s) |
| Final UI production build | pass | `ui-build-complete.log` (Vite 33.41s plus production TypeScript/assets) |
| Final UI typecheck | pass | `typecheck-complete.log` |
| Final server receipt/source/network checks | pass | `server-complete.log` (6.416s) |
| Compiler-contract migration | pass | `receipt-version19.log` (0.079s), old execution refused and cache key changes |
| Group receipt replay and metadata integrity | pass | `group-receipt-green.log` (9.661s); red logs retain original provenance and scalar/list failures |
| Compiler value-policy normalization | six mappings plus default provenance pass | `value-policy-fixed.log` |

The focused package commands are `go test ./internal/explorer/authoringv2
./internal/explorer/recipeimport ./internal/explorer/lifecycle` and `go test
./internal/dataframe/compiler/ir ./internal/dataframe/compiler/lower
./internal/dataframe/compiler/render/aql ./internal/dataframe/execution`. The UI
build is the `@calypr/loom-ui` package `npm run build`. Registered behavior/API
runs retain their exact invocation and coverage in the cited evidence directories.

The Arango cases assert literal expected identities and values for zero/one/many
matches, two-by-three independent branches without a product, distinct Patient
paths, required-path filtering, shared input/related-record pairs, item expansion
and empty items, named empty groups, Group tuples, summaries and retained values.
Additional Group cases use the real engine and Arango to prove keyed, keyless,
empty and repeated grouping while retaining every contributing dynamic-key
inventory. Earlier unknown keys remain rejected; hidden validation metadata never
leaks into public results. The coordinator reproduced seven failures against the
old implementation before repair (`group-runtime-red.log`, 0.141s).
The oracle was authored independently of the compiler and rejects arbitrary first
values, missing pairs, reordered values and accidental Cartesian rows.

## Native evidence

All cases run through the registered `graph-rows` scenario and official
Playwright runner, in an isolated frozen checkout and the owned local
`loom-dev-cloud` stack. No production stack is changed. Each run records all
source hashes, the aggregate fingerprint, API source/binary identity, runtime
configuration, recipe hash, actions and sanitized request diagnostics before and
after. Invoke `node scripts/run-graph-rows-native.mjs --case <case>` with the
owned-target configuration in [ACCEPTANCE.md](fixtures/ACCEPTANCE.md). Raw logs
are `work/graph-rows-runtime/native-<case>-<attempt>.log`. A source or binary change invalidates the run. Native action/request
budgets originally enforced five seconds, with zero retries. The operator
explicitly removed the five-second timing gate on 2026-11-13: final functional
acceptance uses bounded 30s actions/requests, with all exact value, pin, provenance,
Cancel, persistence and CAS checks retained. Measured latency is reported separately
and these passes make no five-second performance claim. A separate baseline-envelope
setup probe has an explicit 30s deadline. The 100-operation fixture is prepared
through real sequential proposals, receipt previews and CAS Applies under a
900s setup deadline with 30s setup requests. Native history controls retain
the 180s test deadline and use the same 30s functional action/request bounds.
Final reruns use 30s expect/action/navigation/API bounds and zero retries;
surrounding process deadlines are 240s, or 420s for loaded history including
a separate 120s current-source admission. All 100 intermediate
calculated results and all 100 final values are checked; this does not claim
100 native operation-creation lifecycles.

| Case | Result | Raw evidence |
| --- | --- | --- |
| `imported` | pass: all five outputs; relationship Preview/Cancel/Apply/edit/removal/reload; real stale draft; layout and keyboard | `/tmp/loom-graph-rows-imported-8/results.json` (73.176s test, zero retries) |
| `ordinary` | pass: ordinary route add, required match, removal and reload | `/tmp/loom-graph-rows-ordinary-2/results.json` (21.075s test, zero retries) |
| `carry-in` | incomplete: source-step and Group lifecycle checks exist; latest whole case fails five-second Group Apply budget (5.29s, command 4.52s) | `/tmp/loom-graph-rows-carry-in-12/results.json`; earlier 17-action prefix is partial evidence only |
| `related-rows` | incomplete on current snapshot: initial ResearchSubject tab click exceeds five seconds; earlier snapshot passed exact pairs/lifecycle | `/tmp/loom-graph-rows-related-rows-3/results.json` |
| `upstream-dependency` | incomplete: five-second initial Builder API GET timeout before Group/source actions | `/tmp/loom-graph-rows-upstream-dependency-1/results.json` |
| `history-stress` | 100-operation production fixture created and checked; current-source admission/native acceptance running (attempt 5) | `/tmp/loom-graph-rows-history-stress-4/` (creation 830.492s); prior native timeout is not a pass |

First failures are retained, including the initial readiness timeouts, an incorrect
verifier join by authored name instead of stable column ID, and the Group receipt
mismatch. Failed runs are not counted as acceptance. The ID join was corrected
against the production binding, and runtime CPU configuration was measured and
recorded. The later operator timing override permits bounded functional reruns;
the original five-second failures remain raw evidence.

The [compact layout comparison](fixtures/LAYOUT-COMPARISON.md) uses the same real
default recipe, catalog, source steps and four preview rows. At 1536×900 the
retained beside arrangement shows four rows in a 330px preview; a bounded
stacked candidate leaves a 122px preview with one fully visible row. Both were
also measured at 1536×1100 and restored to 900px. This comparison is presentation
evidence, separate from native lifecycle acceptance.

## Exact-snapshot review

Two independent reviewers used the inherited model; this is **same-model review**,
not cross-model review. The coordinator reproduced retained findings before repair.
Backend review covered pin/overlay ownership, exact provenance, catalog identity,
strict wire intents, receipt binding and CAS. UI review covered proposal ownership,
source-step review, effective match policy, one graph inspector and popup hit testing.

Retained findings fixed and rechecked: missing default catalog provenance; public
VALUE policy conflicting with the overlay contract; ambiguous legacy route identity;
wrong exact parent node acceptance; clipped compact row controls; and pinned rather
than effective source match text. Later value-policy display normalization and
ordinary removal confirmation counts received scoped reviews with no new verified
findings. Compiler-contract 19 also received a scoped migration review: old v18
and v15 receipts return `RECEIPT_RECOMPILE_REQUIRED` before validation/execution;
new cache keys differ and current-only proposal authorization remains intact.
Receipt: `/workspace/loom-review-a/work/review/compiler19-review.txt`.

Review receipts remain in `/workspace/loom-review-a/work/review/` and
`/workspace/loom-review-b/work/review-ui/`. The repaired backend manifest is
`959f791c18e48b3df68c54a58a8656f0baac0edf87f2ab95d849bd7b1bcd80e7`;
its value-policy delta manifest is
`f8af617c89d8f2b0b85de155e79fd5b412e7f26a8713710c473de266aad90c20`.
The final UI base manifest is
`39f2b0b055c562c4c616b205550ec03f30ffb1b23c4b0bb07450d0ebe6a693a6`;
`tiny-final-manifest.json` identifies the later two-file reviewed display delta.
The final Group repair was reviewed on helper hash
`a8038dbcbc1b135179f75125a2c51debcdb75740e93108298c6fb2f75442fb5b`
and regression hash
`712adb758356c9e63197e3b93158920c35aca1bb8b812d3d74e3bd32654ef382`.
The coordinator reproduced the reviewer finding that source-only dynamic `Many`
cardinality was not fingerprinted, then repaired it. Real Group replay and seven
independent metadata-tamper controls pass. Receipt:
`/workspace/loom-review-a/work/review/group-receipt-review.txt`.
Compiler-version migration hashes are
`9bbbc0af20756bc1c20fd85d769d6a591e4418eca3e95f63cc82f40cea952f53`
and `ebcd92ed27fe8c9fd9bf333d9c33e931ddf31fc30ffa9e1cb0bbf68ad08957f0`.

Independent acceptance-verifier review found no verified findings. Its final
25-file manifest SHA-256 is
`d2f8076481632da5eaf738af0d7722d72e9ca512371916aeea6be8cd28606af2`;
receipt: `/workspace/loom-acceptance-review/work/review-acceptance/final-budget-review.md`.
Earlier nine focused oracle/integrity/history checks passed; later admission
checks pass 15 tests, and official discovery now finds six cases in one file. Review found that Playwright action timeout did not
constrain direct API requests; the coordinator reproduced a 5200ms response being
accepted in 5262ms. Native API calls originally explicitly used 5000ms, and the delayed
response control rejected the response at 5011ms. Under the later operator
override the final functional runs use 30000ms; this changes the timing gate,
not the value or integrity assertions. These focused checks do not substitute
for native execution.

The final Group runtime repair has a separate nine-file reviewed manifest:
`98b528f76f4d43dcf7641fcc0d0f155d9501c324d4721fbdb4c748d8f6b81f8c`.
It extends the existing typed stage IR and renderer to carry each contributing
inventory through grouping, then recursively checks every inventory at execution.
No family is selected arbitrarily and no validation is disabled. Independent IR
variable/projection tamper controls and strict mixed-family checks pass. Receipt:
`/workspace/loom-review-a/work/review/group-runtime-review.txt`. This scoped delta
also covers the added Arango fixture helper beyond the earlier verifier manifest.

Group contributor UI now offers existing public scalar input columns even when
complex imported sources cannot expose unprojected raw FHIR field choices. It
authors the exact `InputColumnID` with explicit `ALL`, excludes identity/list/
object values, and keeps keys and summaries intact. Same-model review caught a
stale coded-capability response that could retarget a Group; the coordinator
reproduced it and the final guard blocks emission until the saved input is
available. Positive derived-stage edits preserve the original anchor. The focused
reshape suite passes 108 tests, five new checks pass in the integrated tree, and
production and focused test TypeScript checks pass. Receipt:
`/workspace/loom-review-b/work/review-ui/input-contributors-final-review.md`.

The receipt store removes repeated content hashing only at already validated
boundaries, retaining caller-body revalidation after storage I/O and all legacy
checks. Actual modern/legacy corruption, concurrent writer, cancellation,
idempotent metadata and aliased-body mutation tests pass. Same-model review
manifest: `a50db8d670dff29b444a40b10757b4936724646aa66e3ddcc44b2557be25c549`;
receipt: `/workspace/loom-review-a/work/review/receipt-store-final-review.md`.
On the identical actual 2,568,639-byte 100-step artifact, one-CPU store-path
samples were 1.546/1.391/1.298s before and 0.985/0.990/1.163s after: a 29% median
reduction with unchanged receipt ID. This is not a native end-to-end speed claim.
Parent regression evidence: `receipt-store-final.log` (0.091s).

The loaded-history admission review covers original-proof integrity, current owned
target/generation, unchanged draft 103, all 100 operation IDs, inputs and schemas,
and all 300 calculated cells. Current reconciliation and preview record fresh
source/API identity before and after. The original creation artifact SHA-256 is
`1c691746b7375313751511437c9f14884cbc021d7541e3c09407c19cca60bffc`.
The private original includes development session metadata and must not be shared
or committed; normal and reuse summaries omit secrets and use exclusive mode-0600
writes. Filesystem alias/race controls preserve original bytes. Receipt:
`/workspace/loom-acceptance-review/work/review-acceptance/history-admission-final-review.md`;
eight-file manifest `1ecece5469364a7b80ca39e2481c7a4232f40c4a3b27599522ecfcfe3f691070`.

The separate upstream-dependency case preserves the complete carry-in lifecycle
and independently checks exact Group input binding, literal results and rejected
upstream removal without saved mutation. Its five-path review manifest is
`301a2b32e8dde5813a2e6945b974c87db60680dea24573e87c30ebf5ba06a406`;
receipt: `/workspace/loom-acceptance-review/work/review-acceptance/upstream-extraction-final-review.md`.
Both are same-model reviews with no unresolved verified findings. An extracted
successful prefix of a failed run never counts as a passing whole case.

The final operator-authorized functional-bounds delta received scoped same-model
review with no verified findings. Only the 30s observation bounds, elapsed-time
and exceeds-five-seconds telemetry, and related comments changed; functional
assertions, data, source/API provenance, immutable pin checks, CAS, zero retries
and the 180s native test deadline remain intact. The 15 focused controls pass
with zero skips (8.968s), and official discovery finds six tests. Receipt and
three-path manifest:
`/workspace/loom-acceptance-review/work/review-acceptance/user-functional-bounds/{receipt.md,manifest.json}`;
manifest SHA-256
`d9e741dfc2ea472fa801df1f254c825fe1a35bcf00d11982959ff28a088066c1`.
Original failed raw runs remain unchanged.

## External limitation

Authenticated Calypr acceptance is blocked. The current skill doctor exits **2**:
`usage: set LOOM_E2E_TOKEN or pass --token-file PATH`.
Evidence: `work/graph-rows/authenticated-doctor-closeout.log`. No short-lived
credential is configured. The authenticated doctor, browser drive and integrated
authoring lifecycle therefore remain unrun. A local fixture pass cannot replace
these checks, and the current authenticated drive alone proves load rather than
the complete authoring lifecycle. The operator subsequently authorized pushing the reviewed code with the
remaining verification gaps documented; deployment and publication remain out
of scope.

## Pushed snapshot and unfinished verification

The operator explicitly requested pushing the reviewed implementation while the
remaining functional verification stays open. The final 30s carry-in attempt
completed its focused checks and official test discovery, then the execution
connection failed; no completed native result was captured. The original
five-second failures remain recorded and are not relabeled as passes. Tickets
remain `verification-in-progress`; this push does not close acceptance.

The final implementation/fixture manifest covers **113 explicit paths**
(documentation excluded), represented as sorted compact JSON of path to SHA-256.
Its SHA-256 is `930c8f3db1f560ee5c3ef8627b79068edb940c2f4fc52edb50a4b49bcde5eec2`.
Raw local manifest: `work/graph-rows/final-source-manifest.json`.
