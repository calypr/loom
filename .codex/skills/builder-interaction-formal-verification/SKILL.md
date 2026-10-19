---
name: builder-interaction-formal-verification
description: Verify Builder proposal ownership, draft identity, preview receipts, and Apply eligibility when changing the construction workspace lifecycle or its applicability checks.
---

# Builder interaction formal verification

Use this skill when changing `useConstructionLifecycle`, `constructionProposalIsApplicable`, proposal cancellation or replacement, preview receipt handling, the proposal's Apply gate, the active Group editor's operation history and downstream recalculation, or the supported Builder actions covered by the UI contract inventory.

The checked promise is: only an uncancelled proposal owner for the current Builder request identity, with a matching proposal response and preview receipt/output with non-null rows, can be ready or begin Apply. A break can show or apply a preview for another draft or another proposal.

Run the integrated check from the repository root containing the current Builder implementation:

    node scripts/verify-builder-interactions.mjs

The runner checks the pinned source files, explores the finite TLA+ model with TLC 1.7.4, and drives the real React hook through the explicit schedule families in its Vitest suite. It downloads the official TLC release to a cache under the operating-system temporary directory when needed. The download and any `TLA2TOOLS_JAR` override must match the SHA-256 recorded in the runner. Java and the existing UI workspace dependencies are required; this workflow adds no package dependency.

This is the focused proposal-owner/concurrency check. It does not cover every Builder affordance or replay UI requests through the backend.

The bounded operation-history slice has a separate command:

    node scripts/verify-builder-operation-history.mjs

It runs the explicit transition explorer, asks the focused Builder DOM adapter to export each accepted workspace seed, has Go validate and compile those seeds into canonical draft identities, then replays every modeled action through the UI using those identities. Finally it passes the unmodified captured proposal requests and the same trace JSON to the Go lifecycle replay. Every case and proposal checkpoint must appear in each artifact; missing, skipped, or unsupported rows fail. This mode checks pinned operation/analyzer source hashes. It does not change the default hook/TLC mode or its source-mutation probe behavior.

## What the check covers

The source map is `verification/builder-interactions/source-contract.json`. Open the mapped source before changing the model:

- `useConstructionLifecycle.ts` builds the request identity, filters the active owner by exact request key, checks proposal response identity, fetches or validates the preview, invalidates a replaced or canceled owner, and gates `beginApply`.
- `ConstructionProposalPanel.tsx` contains `constructionProposalIsApplicable`, which checks the response identity and the preview's proposal receipt, output, and rows.
- `resourceFor` in `ui/packages/loom-ui/src/react.tsx` owns the proposal resource snapshot, abort/epoch handling, and notification behavior used by the hook. Its source is fingerprinted because the hook exposes its snapshot.

The hook's finishApply(true) calls cancel() on the current owner. finishApply(false) closes over the active owner. The model and generated schedule suite stop at beginApply; the included pre-existing unit suite exercises the immediate finishApply(true) client state. They do not model a delayed completion from an old Apply after a replacement. Do not infer an old-owner guard from these functions.

The model has two proposal attempts, two abstract request identities, and owner generations from 0 through 4, with finite proposal outcomes and preview receipts. TLC explores every reachable interleaving in that configured state space. The real hook suite drives the implementation with deferred transports and fake timers through 59 explicitly enumerated schedules: 30 proposal response-order/outcome combinations, 4 proposal identity mismatches, 10 fallback-preview response-order/outcome combinations, 3 late cancellation results, 9 one-field request-identity changes, one A-to-B-to-A timing case, one stale expected proposal after replacement, and one proposal retry. These are bounded model and implementation-conformance checks, not an unbounded proof or a full refinement proof of the entire Builder.

A request identity change makes the old owner inactive while its request key differs; the hook does not itself increment proposal generation on that change. The finite model tracks identity, not React subscription-cleanup timing. Its A-to-B-to-A test rerenders B and then the exact same A synchronously before queued unsubscribe cleanup, and verifies that the pending A owner is not aborted and can become ready. That case does not establish that every A-to-B-to-A timing preserves the old owner.

The check does not establish server persistence, saved-data semantics, construction calculations, full browser behavior, or Apply completion behavior after replacement.

## Bounded operation-history checks

`node scripts/verify-builder-operation-history.mjs` remains a focused Group operation-history check. It is useful for the COUNT_ROWS-to-MEAN edit, dependent Filter/Calculate reconciliation, cancel, preview, and Apply lifecycle. Treat it as a narrow regression check; it does not establish coverage of the full UI affordance inventory.

`verification/builder-interactions/operationHistoryModel.mjs` performs bounded breadth-first transition exploration from 14 sampled accepted-workspace seeds. They cover COUNT_ROWS or MEAN, numeric input availability, key A (`Patient.gender`) and key B (`Patient.active`) availability, and accepted histories with no dependent or up to two FILTER/CALCULATE steps. The seeds are not the full set of possible construction histories. The model enumerates each enabled transition through one Group editor session, with at most six transitions, four user edits, and two downstream steps. Successful Apply acknowledgment and cancel end that session. The artifact contains the exact trace IDs and proposal action indices that the UI and Go phases must replay; the runner derives coverage counts from that artifact and rejects missing or skipped work.

The key cascade witness changes COUNT_ROWS to MEAN and removes the Group key while retaining the accepted downstream history Group→Filter(group-key)→Calculate(aggregate). The raw UI request keeps both dependent steps unchanged. Go's proposal response must remove only the incompatible Filter, preserve the Calculate step ID and order, reconnect its input stage directly to Group, and report the resolved construction the user reviewed. The model independently derives which raw dependent input IDs remain available and asserts survivor membership, exact removals, operation/column identity, order, and reconnected stages without using the resolver as its own oracle. Proposal and cancel do not save; successful Apply acknowledgment advances accepted workspace to exactly that resolved candidate. The model has separate raw and resolved candidates so it does not mistake backend cascading for an editor rewrite.

The model predicts Group aggregate output as integer for COUNT_ROWS and decimal for MEAN. Its seeded, uncased Calculate expression is aggregate plus integer literal `1`; the raw editor output carries no declared type, and the model independently predicts the inferred result type from the expression: integer under COUNT_ROWS and decimal under MEAN. Explicit casts are outside the modeled expression. The real frontend and Go replay assert the resolved/analyzed and compiled stage input/output types, including decimal inference in the combined cascade witness.

The Go prepare phase validates and compiles every accepted seed to provide the exact draft version and digest used by the later UI replay. The UI adapter captures the unmodified proposal request. Go replays those requests through real proposal, analyzer, Apply, and acknowledgment code using a synthetic in-memory store. For each READY proposal it calls the real `Service.Preview` and exports that result beside the unchanged `PREVIEW_PENDING` service DTO. The final panel check presents the DTO's actual `DependencyImpact` and `CandidateConstruction` with that actual preview result, adding the READY presentation shape required by the panel schema. It runs only the panel and inventory assertions and must leave the Go-checked candidate artifact byte-for-byte unchanged. This is a service-DTO-to-panel presentation fixture, not the wire response from the HTTP handler; the runner separately executes the existing server HTTP Group/proposal tests that cover the handler's PREVIEW_PENDING-to-READY-plus-preview conversion. Transport, chrome, and store contents remain synthetic. This is bounded model-based conformance, not a whole-application theorem or browser lifecycle proof; it does not establish database persistence, calculated value correctness, or production transport behavior. On mismatch, the runner reports the trace ID, preceding actions, expected state, and actual UI or Go result.

The operation runner pins the actual `BuilderWorkspace` wiring, Group editor, analyzer/lifecycle proposal, proposal HTTP conversion sources, and the corresponding server route test. It keeps one fresh temporary artifact set across model generation, Go seed preparation, UI replay, Go lifecycle replay, server boundary tests, and panel verification. Run the artifact-dependent UI and Go checks through that runner so trace, identity, candidate, response, and preview files all come from one invocation. Its controlled mutation mode is:

    node scripts/verify-builder-operation-history.mjs --allow-source-drift-for-mutation /absolute/path/to/pristine-worktree

Use a same-HEAD pristine worktree and change only `internal/explorer/lifecycle/construction_proposal.go` to bypass dependent-step cascade resolution. The runner requires the model, UI/Go tests, skill, and verifier to match the pristine reference, then passes only if the Go replay fails at the READY-versus-NEEDS_REPAIR behavioral assertion for the modeled cascade. A source-hash rejection, preparation failure, test setup error, or surviving mutation is a failed probe. Re-read the mapped source before extending the model's action alphabet or claims.

At source HEAD `63e330d12`, Apply also has an `ApplyCommandsWithConstructionPreview` path that returns captured accepted-preview data to the Builder. This bounded Group-history replay still calls the lifecycle `ApplyCommands` API and verifies the saved candidate; it does not verify the new accepted-preview response/handoff through that wrapper and UI path. That additional contract remains outside this check.

## Reading failures

A normal run stops before model checking if any source hash in `source-contract.json` differs. That means the implementation changed and the mapping needs review. Read the current implementation, update the model and real-hook schedule only when the intended behavior changed, then update the source fingerprint deliberately. A hash update alone does not complete that review.

If TLC stops before exploring states with a Java RMI ExportException that says “Listen failed on port: 0” and a SocketException saying “Operation not permitted,” the execution environment blocked TLC's local ephemeral listener. Keep that launch log and rerun the same verifier where local sockets are permitted. This is not an invariant counterexample; do not change the model to work around it.

A TLC counterexample names a reachable model action sequence that violates an invariant. Compare each action with the mapped hook steps. If the source permits the sequence, keep the finding and report it; do not weaken the invariant to make TLC pass.

A Vitest failure names the concrete hook schedule. Deferred clients may resolve even after receiving an abort signal so the suite can test whether stale work still takes control. A failure from that assertion is a source behavior finding; a module-load or environment failure is not.

## Proving the check can catch a source regression

The operation-history check has a controlled mutant mode. Use a separate worktree at the same commit as a pristine reference, and change only `internal/explorer/lifecycle/construction_proposal.go` to bypass dependent-step cascade resolution. Leave the model, tests, manifest, skill, and runner unchanged. From the mutated worktree root, run `node scripts/verify-builder-operation-history.mjs --allow-source-drift-for-mutation /absolute/path/to/pristine-worktree`.

The runner verifies the distinct worktree paths, same Git HEAD, pristine reference fingerprints, exactly one changed fingerprinted production source, and byte-identical declared verification inputs. It bypasses only the source fingerprint gate, runs the bounded UI/Go replay, and reports success only when Go fails at the modeled READY-versus-NEEDS_REPAIR cascade assertion. A source-hash rejection, preparation failure, test setup error, unrelated assertion, or surviving mutant is a failed probe.

For an intended production behavior change, update the source mapping, abstract model, and real-hook schedule together, then run the ordinary command without mutation mode.
