# Product backlog

This is the consolidated queue for open product findings, incomplete acceptance,
and deferred product decisions. Historical reports are not current-source
reproductions. Keep an item open until its exact behavior has a retained test
and applicable evidence; a similar passing fixture case does not close it.

## Findings awaiting current reproduction

### CASE-018 — full-population related-text Pivot latency

**Status: open performance finding; optimization deferred by owner.** Full-population
related-text quantity Pivot category discovery historically returned
`CATEGORY_SCAN_TIMEOUT` after about 10.3 seconds under the former budget. The
later attempt ended at the browser test deadline shortly after starting its
request, so it did not measure the approved 30-second Pivot allowance. Preserve
full population, authorization, multiplicity, and missing-value semantics when
resuming. **Reproducing test: none retained for this exact population.** The
former registered full-population case was retired; the bounded Observation
Pivot in `scripts/e2e/group-pivot.spec.mjs` is not its replacement oracle.

### F-001 — Group-derived Pivot receipt input

**Status: needs reproduction.** The 2026-10-22 audit reported HTTP 500
`CONSTRUCTION_RECEIPT_INPUT_MISSING` for Pivot grouped by `id`, with category
`gender` and value `name__count`. The historical red specs
`group-pivot-preview.spec.mjs` and `pivot-error.spec.mjs` are absent. Reconstruct
the authored recipe and an independent expected output before calling this a
current defect or marking it fixed. **Reproducing test: none retained.** The
Observation Pivot e2e uses a different source and does not settle this finding.

### F-004 — repeated-family Unpivot receipt input

**Status: needs reproduction.** The same audit reported HTTP 500
`CONSTRUCTION_RECEIPT_INPUT_MISSING` when Unpivoting `name[].family[0]` and
`name[].family[1]`, with a historical four-row key/value oracle. Its
`unpivot-preview.spec.mjs` is absent. Reconstruct that source fixture and oracle;
do not assume its row count applies to today's fixture. **Reproducing test:
none retained for the reported recipe.** Existing signed-source Unpivot HTTP
coverage exercises a different contract.

### CODING-GROUP-SCALE-01 — full-scope connected Coding Group

**Status: historical performance finding; needs current reproduction.** The
2026-10-18 backlog recorded a full-scope Patient→Specimen `type.coding[]`
`COUNT_ROWS` query killed at about 8.1 seconds under an eight-second/256-MiB
limit, without rows or execution counters. The smaller connected Group lifecycle
was separately accepted. Do not infer a current speedup from EXPLAIN or that
smaller pass. **Reproducing test: none retained for this full population.**
Capture its current source scope and actual execution profile before proposing
another optimization or changing limits.

### GROUP-ID-LABEL-01 — indistinguishable contributor identities

**Status: needs reproduction; label clarity follow-up.** The 2026-10-18 backlog
reported Patient.id and Specimen.id both displayed as “Logical id of this
artifact.” Confirm the current picker and applied table before changing labels;
resource/route identity should distinguish available choices. **Reproducing
test: none retained for this exact duplicate-label display.** The new related
source selector distinguishes source resources, which does not establish that
all applied column labels are distinct.

## Acceptance still incomplete

### GRAPH-ROWS-01–11 — shared Record network and row editing

**Status: acceptance gap, not a reproduced product defect.** Historical worklist
rows record implementation and scoped owned acceptance, while authenticated
Calypr host identity, wide-frame rendering, and equivalence across the Record
network/row-definition views remain unverified. Keep carry-in, related rows,
upstream dependencies, history, and shared-editing guarantees separate from a
single network expansion. **Exact reproducer: none retained for complete
cross-editor equivalence.** `scripts/e2e/record-network.spec.mjs`,
`root-change.spec.mjs`, and `related-field.spec.mjs` cover bounded local slices.
External acceptance requires an authorized target and credentials.

## Deferred product and contract decisions

These entries preserve intentional unsupported behavior. They do not authorize
implementation or describe confirmed regressions.

| ID | Status and decision | Current test boundary |
|---|---|---|
| ROW-SOURCE-02 | Awaiting operator decision: signed scalar DERIVE/UNPIVOT lanes are delivered; EXPAND and Coding/CodeableConcept source-input lanes need their own shape and semantic contracts. EXPAND currently requires a public array column. | `TestConstructionUnpivotSourceHTTPContractPreservesCapabilitiesAndProposals` covers the delivered Unpivot lane, not deferred shapes. |
| ROW-SOURCE-04 | Proposal only: post-transform coded Pivot and related source-input design remain unapproved. | No reproducer for an approved feature contract. |
| OPS-62-01 | Keep the named 422 when a signed source duplicates an already projected public field, or approve wider dedupe/bind semantics. Shipped refusal stays until decided. | `internal/server/fs1_signed_slot_collision_test.go` covers current collision behavior. |
| OPS-62-02 | Decide whether capability discovery should hide projected fields; currently it may advertise a choice and reject its collision at proposal time. EXPAND admission stays deferred. | Existing signed-source proposal guards establish refusal, not a new discovery contract. |
| OPS-63-01 | Triage cross-lane schema hardening separately. Optional capability `columnId`, nonempty labels, and form-gated Unpivot inputs have different dispositions; do not combine them into a claimed product fix. | `ui/packages/loom-ui/src/types.ts` and `openapi/openapi.yaml` remain the contract sources; no test reproduces a demonstrated user failure here. |
| ROW-CONTRACT-01 | Direct list-aware Unpivot needs item pairing, cardinality, null, and row-identity rules. Scalar Unpivot after supported expansion is a different supported path. | No approved direct-list regression contract. |
| ROW-CONTRACT-02 | Connected Group supports its canonical root/contributor route; arbitrary later-stage and independent relationship branches need a separate contract. | Current related Coding HTTP tests cover the supported route only. |
| ROW-CONTRACT-03 | Versioned coded Pivot remains intentionally unsupported; both UI and lifecycle reject nonempty coding versions. | `CodedPivotEditor.tsx` and `construction_coded_pivot.go` enforce the current restriction. |
| ROW-CONTRACT-04 | Repeated object source expansion cannot yet compose mid-sequence or with GROUPS rows. Decide the source/projection model before extending it. | No approved regression contract for these shapes. |

Historical fixture-only gaps and architectural cleanup notes were not promoted
to product defects. Retired harness disposition and recovery locations belong
in [DELETED_GUARANTEES.md](verification/DELETED_GUARANTEES.md); Git retains the
superseded worklists. Current verification routes are in
[the verification skill](../.codex/skills/verify/SKILL.md) and
[the script guide](../scripts/README.md).

## Current cleanup findings and acceptance gaps

### ARC-C13 — one read pin aborts unrelated reconciliation

**Status: reproduced against Arango; open.** A pinned stale execution aborts the
whole reconciliation run, leaving unrelated reclaimable executions untouched.
The no-pin control reclaims them. Retained cases are
`TestStalePinBlocksEntireReconcileAgainstArango`,
`TestLivePinOnOnlyStaleExecutionStillAbortsRunAgainstArango`, and
`TestExportCrashInsideFirstTickLeavesCrashWindowPinAgainstArango`. With owned service credentials these assertions run normally and remain red
until the product defect is repaired.

### ARC-C14 — legacy READY selection chooses the older execution

**Status: reproduced against Arango; open.** A valid legacy READY execution
with a newer ReadyAt and no PublishedAt canonicalizes as published, but the
selector returns an older execution.
`TestFindExecutionBySelectorLegacyCaseAsymmetryAgainstArango` retains the red
assertion in normal integration runs. It leaves one Phase-1 baseline
guarantee red until the product defect is repaired.

### ARC-E12 — end-to-end CI cadence

**Status: awaiting owner choice of every PR or scheduled/on demand.** All
seven native browser cases passed three consecutive runs. Fresh local stack
startup plus cases took 102.06 seconds; hosted image/download/job timing is
unmeasured. Keep the ten-minute job budget when wiring the selected cadence.

### ARC-E14 — safe development-project pruning

**Status: blocked on a backend deletion seam.** No project/generation DELETE
API exists. E2E projects span shared Arango documents, reference-keyed
metadata, active publication bindings, leases, and physical ClickHouse tables.
Deleting project-tagged documents would leave orphaned outputs. Add a
first-class deletion service with binding/lease checks in the owning backend
packages before implementing `loom-dev prune` for exact `loom_dev_e2e_*` IDs.
No partial deletion or successful no-op CLI was added.
