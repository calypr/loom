# Operator decision digest — pending program decisions

Finalized 2026-11-03 by the continuous find–fix–verify program (originally
drafted at HEAD `0dd7be44`; committed so the blocking decisions persist
outside the ephemeral `/tmp/opencode` evidence area). Cited files under
`verification/publication-bundle/**` remain record-writer owned.

Safe-default for every item below: keep the current shipped behavior (all
these paths are fail-closed today: named 422 refusals, compiler-whitelist
refusals, no new admission classes, no migration). The program does not
proceed on any of them without an explicit operator answer.

## (a) Signed-slot collision semantics — keep named-422 or accept dedupe-bind

- Current (`62f627fb`, shipped F-DS1 = refusal-not-dedupe):
  a signed source slot for a root field already projected as a public
  `SourceField` column is refused with a named 422 at
  `internal/explorer/lifecycle/derive_source.go:304`,
  `internal/explorer/lifecycle/filter_source.go:252`, and
  `internal/explorer/lifecycle/unpivot_source.go:344`, driven by the shared
  predicate at `internal/explorer/lifecycle/construction_pivot_source.go:658`
  with carrier set at `internal/explorer/lifecycle/construction_pivot_source.go:687`
  (Derive, Filter, Unpivot lanes only).
- probe6 case-D distinction (`internal/server/fs1_signed_slot_collision_test.go:323-346`):
  bind-to-existing-column dedupe (DERIVE operand reuses the user's own
  public column id, no signed slot binding) stays a legitimate 200; the
  refusal targets the *second, distinct* signed slot on the same field, not
  the same-column reuse — so "dedupe-bind" is a different, wider change, not a
  relaxation of case-D.
- Consequence of each. Keep named-422: user must unproject the public column
  before adding a signed slot on the same field; two independent intents stay
  distinguishable and the receipt-contract semantic-path check at
  `internal/server/explorer_output_reconcile.go:497-512` cannot fire on
  accidental duplicate columns. Dedupe-bind: fewer refusals but the signed
  lane becomes the same projection as the user column, so removing that
  public column silently strips the operand/melt/filter and the receipt
  guard loses the ability to distinguish two independent user intents.
- Absent an answer: program keeps named-422 refusal.

## (b) Capability advertisement exclusion of user-projected fields

- Current: capability discovery does NOT subtract the user's already-projected
  public columns from the signed-choice list — see the enumeration loops at
  `internal/explorer/lifecycle/unpivot_source.go:161-175` (unpivot lane, twin
  at `internal/explorer/lifecycle/derive_source.go:137` and
  `internal/explorer/lifecycle/filter_source.go:94`); collision is caught at
  propose time by (a).
- Honesty-of-advertising argument *for* exclusion: advertising a field the
  user has already projected offers an affordance that cannot be bound (the
  subsequent propose will 422 at the F-DS1 predicate). Counter: user-projected
  public columns can be unprojected at any time, so excluding them couples
  capability discovery to the current document shape; a field valid one edit
  later disappears from the option list, and any later "keep public-column
  projection while binding a signed slot" relaxation would either require a
  capability-refetch per keystroke or silently break the UI optgroup restore
  path.
- Absent an answer: program keeps advertise-then-refuse (fail-closed at
  propose; capability discovery stays pure and document-shape-agnostic).

## (c) Coding/CodeableConcept per-operation semantics for later slices

- Current: compiler whitelist is the single source of truth (F-1 alignment in
  `docs/BUILDER_WORKLIST.tsv` row 95 evidence / `/tmp/opencode/rs02-slice2-report.md`
  §F-1). UNPIVOT melt value/key must be `lower.TableReshapeScalarKind` at
  `internal/dataframe/compiler/lower/recipe_table_reshape.go:371` (delegated
  from `internal/explorer/lifecycle/unpivot_source.go:87-88`), so a
  Coding/CodeableConcept is refused with the compiler's own reason at the
  admission gate; EXPAND for a coding-family input would need an ARRAY-shaped
  signed projection admission class that does not exist yet.
- What to ratify per operation for later slices (semantics, not implementation):
  - UNPIVOT value/key with a Coding/CodeableConcept: stay refused (accepting
    one and stringifying would alter what the operator melts;
    `rs02-slice2-report.md` §matrix row on Coding).
  - FILTER/DERIVE on a coding path: currently refused by the compiler scalar
    whitelist (numeric operands only in derive, `derive_source_test.go` and
    the compiler expression.ValueKind gate). Ratify whether a coding code
    projected to a string is a distinct accepted meaning.
  - EXPAND on array-of-Coding / array-of-CodeableConcept: paired with (d).
- Absent an answer: program keeps compiler-whitelist refusal; no fabricated
  admissions and no coding-shape signed-choice design is started.

## (d) EXPAND deferral ratification

- Deferral justification (`/tmp/opencode/rs02-slice2-report.md` § matrix,
  EXPAND row): EXPAND requires an ARRAY-valued public column today
  (`NO_ARRAY_COLUMNS` capability at
  `internal/dataframe/compiler/lower/recipe_construction.go:2249`);
  an ARRAY-shaped signed projection admission class is a distinct shape
  contract from the frozen scalar signed-choice design. Real shape anchors:
  `internal/explorer/capability/row_choice.go:30-32` (`RowChoiceScalar` /
  `RowChoiceObject` / `RowChoiceArray`) and
  `internal/explorer/capability/row_choice.go:282` (`validateRowChoiceFacts`).
  The report's cited `resolveRowChoiceFacts(ArrayProjection=true)` at
  `internal/explorer/compilation/recipe_build.go:1056` does not resolve in this
  source tree — flagging for the report author's correction; the underlying
  claim (three-way row-choice shape with no signed-projection ARRAY admission
  class today) is source-verified at the anchors above, so the deferral
  rationale stands. Array-shape reconcile facts remain uncharted.
- Ratification confirms EXPAND is a later slice that first records the
  array-projection contract decision (shape, repeated-boundary observation
  facts, value-type ledger) before an implementation slice opens.
- Absent an answer: EXPAND source channel stays unimplemented; no
  ARRAY-shaped signed projection design is drafted ahead of ratification.

## (e) ROW-SOURCE-04 pending proposal decisions — pointers only

- Status source: `docs/BUILDER_WORKLIST.tsv` row 97 (ROW-SOURCE-04) status =
  `proposal-delivered-awaiting-operator`.
- Proposal pointers (do not restate):
  - `docs/ROW_OPERATION_SOURCE_INPUT_BACKLOG.md:14` — the ROW-SOURCE-04 scope
    statement (bounded recommendation, not a redesign).
  - `verification/publication-bundle/reviewed-sources.json:1285`
    (`row-source-04-proposal`: Candidate A, compiler G4 gate
    `recipe_construction_coded_pivot.go:172-199`, blockers R1/R2 + G1
    `:665-667` + G2, contract table G5 · MissingCellPolicy · versionless
    categories; 7 operator-decision items enumerated in place; Candidate B
    deferred). Record-writer-owned; read-only here.
  - `verification/publication-bundle/OPERATOR_REVIEW_DIGEST.md:244` — same
    summary as the operator-facing digest line. Record-writer-owned; read-only
    here.
- The 7 items are only in those pointers: versioned categories, branch
  combos, prefix op set, coded-GROUP post-prefix exclusion, compound group
  coded-values, multi semantic keys, prefix-column visibility. This digest
  does not restate them.
- Absent answers: ROW-SOURCE-04 stays proposal-only under
  `docs/verification/CONTINUOUS_FIND_FIX_VERIFY_GOAL.md` § authority line 20;
  implementation is not authorized.

## (f) releaseId bit-stability migration veto status

- Current: `releaseID` is content-addressed and byte-stable —
  `internal/dataset/release.go:345-349` hashes
  `internal/dataset/release.go:356-365` (`releaseIDFraming`) which renders
  through `internal/dataset/release.go:367-371` (`writeLengthPrefixed`) over
  Project / Generation / Publications[Selector.Key(), ExecutionID]. Round-trip
  proof in `internal/dataset/release_id_framing_test.go:82-` (REGRESSION
  guards on the fold at :226-235). Idempotent-activation Repair-3 keeps the
  revision excluded from the frame
  (`verification/publication-bundle/OPERATOR_REVIEW_DIGEST.md:51`, record-
  writer-owned line; cited only).
- Veto target: any future migration that would re-frame or re-hash an
  existing release's `releaseID` bytes (which would strand ID-keyed
  consumers). No such migration is proposed, drafted, or shipped.
- Absent an answer (and this is a standing veto default): the framing bytes
  stay frozen at release.go:356-365 as shipped; no migration tool is built and
  the `release_id_framing_test.go` REGRESSION guards stay the durable trip.

## (g) Signed-set on non-matching step kind — silent 200 vs named refusal

- Status: needs-operator-decision (C3, matrix complete at HEAD `8459f080`;
  all four kind-mismatch arms ALIVE — worker probes A-F + parent Probe G,
  `/tmp/opencode/c3-reachability.md`; round-58 dead-code claim refuted).
- Reachable harm surface is ONE row: a non-empty signed set (e.g.
  `deriveSources`) aimed at a step of a foreign kind is silently accepted —
  `err=nil, PREVIEW_PENDING`, set ignored (not cleared, not decorated, no
  diagnostics) — while the identical set on a matching-kind step with nil
  payload is refused by the named arm, and `[]` on a foreign kind is a no-op,
  not a clear. Unknown changed steps ARE refused.
- Options (none implemented; 2 runnable probe sketches in the receipt):
  (1) keep today's silent 200; (2) named 422 per lane mirroring the existing
  `*_SOURCE_REQUIRES_*` arms (2-4 lines each, class already maps to 422 at
  `openapi_explorer_direct.go:69-70`); (3) refuse only non-empty sets (keep
  `[]`/nil no-op); (4) keep 200 + advisory entry in `dependencyImpact`.
  Deleting arms is not an option — they are the only named guard.
- Absent an answer: shipped behavior unchanged (option 1).

## (h) Row-96 four-state UX polish proposals (EXPAND-family, triage)

- Source: parent MET-BY-CODE audit `/tmp/opencode/row96-ui-four-state-audit.md`
  (contract clause satisfied by identified code — loading text, sanitized
  alerts, server reasonCode+reason `role=status` pass-through with unit
  tests). These are NOT defects; implementation requires explicit approval:
  1. positively-communicated empty state for supported lanes with zero
     populated dataset-field choices (today: absence of the optgroup only);
  2. visible per-field refused-entry channel in the pickers (today: S-A
     per-field reasonCodes are payload-only; journey check
     `row96-ui-control-consumes-reason` honestly records the gap via its
     API-payload fallback rule).
- Absent approval: current behavior ships; journey sideFindings keep the gap
  visible without asserting it away.

## (i) OPS-63-01 pointer — zod leniency cases 2-4

- Worklist row: OPS-63-01 (proposal-only triage). Case 1 CLOSED by
  `c63b622e`; Cases 2-4 byte-derived coordinates in
  `/tmp/opencode/zod-leniency.md`; no product change until triaged.

## (j) Availability dependency — host port-forwards — RESOLVED as of 2026-11-03

- The 2026-11-02 outage (`127.0.0.1:8182 -> 000`) is CLOSED: verified same-day
  by the coordinator — `http://127.0.0.1:8182/readyz -> 200` and
  `http://127.0.0.1:3182/ -> 200`, and `docker loom status` reports both
  services HTTP 200. The journey re-accept family (label WILL-FLIP
  re-asserts, filter row-279 live-green flip, row-96 `JOURNEY_OK checks=6`,
  Arango smoke) is no longer port-blocked.

## (k) NEW runtime-identity split blocking the acceptance window — operator-side

- `docker loom status` now reports `source_root` at a HOST-side path
  (`/var/tmp/os-native-image-test-*/rootfs-*/root/var/lib/docker/volumes/arc-loom-qwen-current-20261031/_data`)
  that does NOT exist in the agent mount namespace; its recorded identity is
  `git_head cc3a42aa…`, `source_changed: true`, `state: failed`, 2502 files,
  git-dirty — while the agent integration volume HEAD is `13866eb7` (docs
  discovery commits on this program's line, last product code shipped at the
  `438305f2` stamp live in the running container). The host sees a sibling/
  derived checkout ahead of or divergent from this program's lineage (its
  commits were never pushed from here; pushes were never authorized).
- Consequence required before any full acceptance run: the acceptance
  begin/end boundary samples the TRACKED root only, so a journey re-accept
  while this split stands would certify the host clone's content, not this
  program's source snapshot. Operator action needed: re-point the runtime's
  registered source root at the integration volume (or reconcile the clone)
  so tested-source identity is singular; the port window alone is no longer
  the blocker.

- (g.1) Parent independent confirmation of the harm row: Probe B rerun by the
  coordinator at 49d6999e PASS verbatim — `err=<nil> previewStatus=PREVIEW_PENDING
  deriveOwnedProjections=0`, steps byte-equal, dependencyImpact empty (log
  c3_reachability_probe_test.go:74/:105-106 in /tmp/opencode/c3probe).
