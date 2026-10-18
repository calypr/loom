---
name: builder-interaction-formal-verification
description: Verify Builder proposal ownership, draft identity, preview receipts, and Apply eligibility when changing the construction workspace lifecycle or its applicability checks.
---

# Builder interaction formal verification

Use this skill when changing `useConstructionLifecycle`, `constructionProposalIsApplicable`, proposal cancellation or replacement, preview receipt handling, or the proposal's Apply gate.

The checked promise is: only an uncancelled proposal owner for the current Builder request identity, with a matching proposal response and preview receipt/output with non-null rows, can be ready or begin Apply. A break can show or apply a preview for another draft or another proposal.

Run the integrated check from the repository root containing the current Builder implementation:

    node scripts/verify-builder-interactions.mjs

The runner checks the pinned source files, explores the finite TLA+ model with TLC 1.7.4, and drives the real React hook through the explicit schedule families in its Vitest suite. It downloads the official TLC release to a cache under the operating-system temporary directory when needed. The download and any `TLA2TOOLS_JAR` override must match the SHA-256 recorded in the runner. Java and the existing UI workspace dependencies are required; this workflow adds no package dependency.

## What the check covers

The source map is `verification/builder-interactions/source-contract.json`. Open the mapped source before changing the model:

- `useConstructionLifecycle.ts` builds the request identity, filters the active owner by exact request key, checks proposal response identity, fetches or validates the preview, invalidates a replaced or canceled owner, and gates `beginApply`.
- `ConstructionProposalPanel.tsx` contains `constructionProposalIsApplicable`, which checks the response identity and the preview's proposal receipt, output, and rows.
- `resourceFor` in `ui/packages/loom-ui/src/react.tsx` owns the proposal resource snapshot, abort/epoch handling, and notification behavior used by the hook. Its source is fingerprinted because the hook exposes its snapshot.

The hook's finishApply(true) calls cancel() on the current owner. finishApply(false) closes over the active owner. The model and generated schedule suite stop at beginApply; the included pre-existing unit suite exercises the immediate finishApply(true) client state. They do not model a delayed completion from an old Apply after a replacement. Do not infer an old-owner guard from these functions.

The model has two proposal attempts, two abstract request identities, and owner generations from 0 through 4, with finite proposal outcomes and preview receipts. TLC explores every reachable interleaving in that configured state space. The real hook suite drives the implementation with deferred transports and fake timers through 59 explicitly enumerated schedules: 30 proposal response-order/outcome combinations, 4 proposal identity mismatches, 10 fallback-preview response-order/outcome combinations, 3 late cancellation results, 9 one-field request-identity changes, one A-to-B-to-A timing case, one stale expected proposal after replacement, and one proposal retry. These are bounded model and implementation-conformance checks, not an unbounded proof or a full refinement proof of the entire Builder.

A request identity change makes the old owner inactive while its request key differs; the hook does not itself increment proposal generation on that change. The finite model tracks identity, not React subscription-cleanup timing. Its A-to-B-to-A test rerenders B and then the exact same A synchronously before queued unsubscribe cleanup, and verifies that the pending A owner is not aborted and can become ready. That case does not establish that every A-to-B-to-A timing preserves the old owner.

The check does not establish server persistence, saved-data semantics, construction calculations, full browser behavior, or Apply completion behavior after replacement.

## Reading failures

A normal run stops before model checking if any source hash in `source-contract.json` differs. That means the implementation changed and the mapping needs review. Read the current implementation, update the model and real-hook schedule only when the intended behavior changed, then update the source fingerprint deliberately. A hash update alone does not complete that review.

If TLC stops before exploring states with a Java RMI ExportException that says “Listen failed on port: 0” and a SocketException saying “Operation not permitted,” the execution environment blocked TLC's local ephemeral listener. Keep that launch log and rerun the same verifier where local sockets are permitted. This is not an invariant counterexample; do not change the model to work around it.

A TLC counterexample names a reachable model action sequence that violates an invariant. Compare each action with the mapped hook steps. If the source permits the sequence, keep the finding and report it; do not weaken the invariant to make TLC pass.

A Vitest failure names the concrete hook schedule. Deferred clients may resolve even after receiving an abort signal so the suite can test whether stale work still takes control. A failure from that assertion is a source behavior finding; a module-load or environment failure is not.

## Proving the check can catch a source regression

The integrated check has a controlled mutant mode. Use a separate worktree at the same commit as a pristine reference, and change exactly one fingerprinted production file to remove a real identity or preview-ownership guard. Leave the model, tests, manifest, skill, and runner unchanged. From the mutated worktree root, run the verifier with the absolute pristine-worktree root as the argument to `--allow-source-drift-for-mutation`.

The runner verifies the distinct worktree paths, same Git HEAD, pristine reference fingerprints, exactly one changed fingerprinted production source, and byte-identical declared verification inputs. It bypasses only the source fingerprint gate, still runs TLC and Vitest, and reports success only when the formal hook suite fails with a behavioral assertion. A surviving mutant or a setup/module failure is a failed probe.

For an intended production behavior change, update the source mapping, abstract model, and real-hook schedule together, then run the ordinary command without mutation mode.
