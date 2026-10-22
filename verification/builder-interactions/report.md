# Builder formal verification report

Source commit: `dc331783dde3f61e764bab3a421b66b28698c9d4`

Tool: TLC 1.7.4 (`936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88`), java version "17.0.7" 2023-04-18 LTS.

The model is bounded to the state spaces in the checked-in `.cfg` files. A TLC pass covers those bounds, not an unbounded refinement proof of the React or Go applications.

## Checks

- `builder-operations`: **pass** (86902 generated / 5525 distinct, depth 7)
- `builder-preview-safety`: **pass** (496664 generated / 66469 distinct, depth 20)
- `builder-workspace-lifecycle`: **pass** (48194160 generated / 3472373 distinct, depth 30)
- `builder-row-definition-lifecycle`: **pass** (619 generated / 177 distinct, depth 10)
- `builder-draft-restore`: **pass** (376 generated / 103 distinct, depth 9)
- `builder-root-change`: **pass** (510 generated / 137 distinct, depth 10)
- `builder-column-presentation`: **pass** (2845751 generated / 130465 distinct, depth 12)
- `builder-population`: **pass** (1584747 generated / 114118 distinct, depth 19)
- `builder-interpretation`: **pass** (5711 generated / 636 distinct, depth 12)
- `builder-supporting-surfaces`: **pass** (516262 generated / 34161 distinct, depth 13)
- `builder-lifecycle-entry`: **pass** (13379003 generated / 845705 distinct, depth 20)
- `builder-reconcile-lifecycle`: **pass** (1913635 generated / 148629 distinct, depth 19)
- `real-controller-schedules`: **pass**
- `builder-trace-validation`: **pass**
- `builder-trace-validator-self-test`: **pass**
- `go-internal-explorer-lifecycle`: **pass**
- `go-internal-server`: **pass**
- `ui-contract-inventory-source`: **pass**
- `builder-route-ledger`: **pass**
- `builder-readiness`: **pass**

## Route ledger

- Builder routes: 39 (21 Tier 1, 18 Tier 2).
- Proof: 3 modeled-and-traced, 17 modeled-only, 16 contract-tested, 2 unproven.
- Excluded authoring routes: 4.
- Builder evidence: 39/39 routes.
- Route-level evidence: 4 traces, 20 tests, 9 browser cases.

## Trace conformance

- 70 formal traces, 21 unique after deduplication.
- 70 validated against TLC model paths; 0 invalid.
- Models: BuilderPreview=61, ConstructionLifecycle=1, ReconcileLifecycle=8.
- Required actions: all covered.
- Required events: all covered.
- Implementation events outside the trace vocabulary: 55, 0 unclassified.
- Classified event kinds: BeginApply(rejected)=2, late-reconcile-ignored=1, LatePreviewCompleteIgnored=10, LateProposalCompleteIgnored=31, LateProposalErrorIgnored=11.

## Resolved regressions

- `apply-ack-targets-current-owner-success`: Successful Apply acknowledgment now targets the owner that began Apply. Regression: `regression: old Apply success after replacement must not cancel the replacement owner`.
- `apply-ack-targets-current-owner-failure`: Failed Apply acknowledgment now targets the owner that began Apply. Regression: `regression: old Apply failure after replacement must not mark the replacement owner as an error`.

## Retired findings

### `pivot-missing-category-unsupported`

- The mounted Pivot editor offered category fields whose absent values the backend refused to discover.
- Retired because: Not reproduced in the current compiler. Source fields with absent values are presence-tracked; materialized Derive, Filter, Expand, Group, and Unpivot outputs report present=true. No audited valid path produced missing && !PresenceTracked.
- Evidence: `internal/dataframe/compiler/pivot_presence_audit_test.go`, `internal/dataframe/compiler/pivot_presence_arango_audit_test.go`, `internal/explorer/lifecycle/construction_category_discovery.go:426-437`

## Findings

No model or implementation findings were reproduced in this run.

## Coverage gaps
