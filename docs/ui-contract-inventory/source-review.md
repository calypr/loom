# Reviewed source checkpoint

The affordance/request contract still originates at `dc331783dde3f61e764bab3a421b66b28698c9d4`. The source fingerprints were reviewed against baseline `5646633e0e5c7ff8de568a4f111478d121e7f75f` plus the current **uncommitted** Apply ownership fixes. This is a working-tree review, not a new sync commit. `sourceCoverage.reviewedWorkingTree` records that distinction; `watchedFiles` contains the actual reviewed bytes.

The review preserves all 57 affordances, all 159 request checkpoints, every expanded affordance/request definition, source binding/surface mapping, and every fragment hash exactly. Only source checkpoint metadata changed:

- `BuilderWorkspace.tsx` uses the initiating Apply handle's ownership result before editor cleanup. The submitted public command remains `APPLY_CONSTRUCTION_PROPOSAL` with its proposal/output IDs and existing CAS request envelope.
- `useConstructionLifecycle.ts` returns an attempt-bound completion handle and checks current owner/request/generation. Public proposal and preview request constructors are unchanged.
- `constructionApplyOwnership.ts` is the new synchronous ownership helper. It is now watched; it adds no user affordance or HTTP request.
- The previous `ConceptCatalog.tsx` fingerprint matches commit `beb65e38e6a28b65862a22e7ceab4dea20e43263`. Its change to the baseline only wraps selected-feature labels and displays the already-computed label suffix. It changes no selection action or request constructor.

`python3 scripts/verify_ui_contract_inventory.py --source-only` validates the reviewed file set and hashes. The normalizer/verifier unit tests still reject fragment, source-byte, source-file-set, and expanded-contract drift. A source-only pass proves checkpoint alignment, not model coverage, browser behavior, or all affordance combinations. The current executable formal/behavior checks are documented in `.codex/skills/builder-interaction-formal-verification/SKILL.md`; historical runner claims in this directory do not substitute for them.

Future deliberate regeneration must include another source review and renew the `reviewedWorkingTree` metadata (or advance it to a real committed checkpoint). The normalizer does not infer that review or invent a commit; its `--expand` path must continue preserving the checked-in pins without refreshing them.

## Reconcile and artifact correctness follow-up

The same baseline and uncommitted review now include the next production fixes.
All 57 affordances, all 159 request checkpoints, all expanded affordance/request
records, disabled-affordance records, source bindings, surface mappings, and all
four fragment hashes remain exactly unchanged. The watched Builder source set
increases from 99 to 100 files solely for the new actual acceptance helper.

- `reconcileAcceptance.ts` extracts the existing synchronous post-response
  classifier into an executable production boundary. It calls the existing
  current-result and receipt predicates. Obsolete responses still return without
  dispatch, receipt mismatch still dispatches repair, and current matching
  responses still dispatch compiled even when error diagnostics prohibit preview.
  The caller still checks cancellation before classification. No await or public
  request constructor moved. Error diagnostics are read within that same
  synchronous boundary before dispatch; decoded response DTOs remain unchanged.
- `BuilderWorkspace.tsx` delegates that classification and retains the original
  repair/compiled/cancellation branches. This is a behavior-preserving extraction,
  not a new HTTP or affordance contract.
- `api.ts` now rejects a prepared artifact when its canonical project, Explorer,
  revision, or output differs from the requested identity. This fixes admission
  of a foreign reply; the request endpoint/method/body and valid-response schema
  are unchanged. Generated identity-input and mounted UI regressions provide the
  separate behavioral proof; source hashes alone do not.
- `ExplorerViewer/components.tsx` fixes FileCell URL construction by adding the
  encoded file ID to the path before preserving query and fragment and setting
  the download redirect flag. Viewer code is outside this Builder manifest's
  watched file set and original scope. Its review is recorded as adjacent context,
  not falsely added to the watched Builder set or counted among its affordances.
  Separate component/browser checks establish that observable URL behavior.

The source-only verifier and existing normalizer/verifier tests pass against this
checkpoint. Deliberate source-byte and active-file-set changes must still fail;
the review does not replace those controls or establish browser/storage coverage.
