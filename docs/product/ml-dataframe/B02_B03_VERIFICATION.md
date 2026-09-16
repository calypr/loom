# B02 and B03 integration verification

These are local implementation checkpoints, not completed work-package verdicts.

## B02 selection evidence

The production Arango adapters pass selection storage and publication read-pin tests. The storage test covers duplicate member retries, atomic completion, rejected foreign writers, 50 concurrent append/foreign-abort races, empty and multi-member aborts, expired-writer recovery, and protection of active writer leases. The read-pin test includes 50 pin/cleanup races.

Final isolated test run before API integration:

```text
TestSelectionStorageAgainstArango: PASS (0.773s)
TestExecutionReadPinArbitrationAgainstArango: PASS (0.234s)
```

`scripts/verify-selections.mjs` exercises the production HTTP API against an owned development fixture. It verifies exact explicit membership, exclusions, idempotent replay, pagination, equivalent all-matching membership from a pinned publication, an empty typed selection, unchanged membership after republishing, and rejection of foreign-project/stale-generation references.

Evidence is retained locally at `.artifacts/loom-dev/selections-1789584932868.json`. Explicit creation took 33.3ms for this small fixture. This is not a large-population performance claim.

The production storage scale test verified exact final counts and digests using 1,000-member batches. It measured 100 members in 56ms, 10,000 in 4.376s, and 100,000 in 58.824s. These are backend persistence measurements, not end-to-end HTTP timings or a speedup over a prior implementation. The largest selection is not a sub-30-second operation.

After restarting `loom-dev-arch-integration-loom-api-1`, the script's `--check-saved` mode verified the same membership, counts, and digests for explicit, matching, and empty selections.

Live verification exposed and corrected four integration mistakes that isolated tests had missed:

- Capability resolution received an Explorer ID instead of a snapshot token. Creation now uses active-generation compilation authorization; reads derive current authorization before returning counts.
- FHIR storage uses the existing legacy project encoding. Resource queries convert at that boundary, while public references retain canonical project identity.
- The source-row proof accepted only the generic resource grain. It now uses the existing named resource-grain validation and still rejects expanded or mismatched grains.
- A compilation schema digest was compared with a final physical publication schema digest. Selections now retain the exact execution's physical schema identity, independently of the compilation schema.

## B03 concept evidence

`internal/dataframe/compiler/correlated_arango_test.go` runs generated AQL against real Arango. All nine literal-value cases pass, including owner-preserving arrays, explicit distinct/first behavior, invalid multiplicity, same-Coding system/code pairing, rejected cross-element matches, missing systems, invalid choice arms, and string arrays. Paired predicates also prove independent bind values.

The first real run returned null for seven positive cases. Flattening the owner selector result fixed that defect. Final matrix time was 2.160s.

Catalog regressions prove quantity/date typing and prevent double-counting newly merged semantic observations. Command-level regressions exercise correlated bindings through `Service.ApplyCommands`, rather than only direct compilation.

The public concept-evidence and receipt Preview probe passes. It verifies distinct system A/B concepts and exact `[111]`/`[222]` values, null for a cross-element match, an explicit invalid-choice result for a string under the numeric binding, and null for a missing terminology system. Preview took 626.8ms. Evidence is at `.artifacts/loom-dev/correlated-concepts-1789585321214.json`.

Concept evidence is attached to the generated scalar value path, not advertised as a projectable object. The production public catalog mapper preserves it. Capability compiler/projection policy versions were bumped so retained old snapshots cannot mask the changed construction policy.

B03 remains open for nested extension parent identity and removal of new legacy lossy lookup writes.

## Existing journey

The integrated browser journey passed 25 assertions in 13.737s, including Builder, Preview, Publish, Viewer, filtering, CSV export, and reload. Evidence is at `.artifacts/loom-dev/6d7df93d6a37/mu4g56q1-8d2ee267/report.json`.

The UI suite passed 131 tests across 22 files, including type and package-boundary checks. Integrated Explorer, server, publication, and published-reader package suites passed after the identity corrections.

No remote branch, production service, or canonical demo was changed.
