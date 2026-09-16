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

The production storage scale test verifies exact final counts and digests using 1,000-member batches. The initial implementation measured 100 members in 56ms, 10,000 in 4.376s, and 100,000 in 58.824s.

Reading batched insert results in batches instead of one row per cursor request reduced the 100,000-member run to 31.656s. Arango's query plan then exposed repeated sorting during digest pagination. Binding the header's generation and resource type lets the existing compound index supply ordered ID ranges. Canonical member keys already enforce one member per resource ID within the homogeneous selection.

The combined changes measured 100 members in 28ms, 10,000 in 1.542s, and 100,000 in 15.636s. The real storage correctness suite also passed in 0.83s. These are single-run backend persistence measurements, not end-to-end HTTP timings. They do not establish that a 100,000-member HTTP request fits the lifecycle deadline.

The committed scale test now uses the lifecycle's 256-member batch size. That run passed exact counts and digests at 31ms for 100 members, 1.918s for 10,000, and 20.493s for 100,000. Lifecycle regressions also prove abort without completion after an interrupted source stream or exceeded row limit, and rejection of an invalid authorization scope before loading the selection header.

A separate valid deny-all scope regression rejects a selection created under unrestricted access without returning its header, members, or cursor. The rebuilt API probe also passed after the optimization, with evidence at `.artifacts/loom-dev/selections-1789586419969.json` and small-fixture explicit creation at 28.6ms.

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

Nested extension ancestry and new-write validation now pass the production chain. The closed extension binding carries one URL per extension boundary. Schema validation, semantic lowering, and the existing physical correlation renderer preserve that ancestry. New ambiguous extension, Coding, and Observation lookup commands are rejected; historical legacy artifacts remain readable.

Review caught a candidate renderer that merged repeated terminal matches into the same output key. The corrected renderer collects all matched owners before applying the shared reduction. `TestExtensionCompilerLiteralValuesAgainstArango` proves two same-parent values survive `ALL`, `VALUE` reports `INVALID_MULTIPLE_VALUES`, the other parent remains separate, a missing parent returns null, and omitted choice-arm metadata still exposes a mismatched value as `INVALID_CHOICE_ARM`. All five cases passed in 0.09s. The existing nine Coding cases passed again in 2.15s.

The extended public commands and receipt Preview probe passed 13 assertions, including legacy-write rejection and exact left/right nested values. Evidence is `.artifacts/loom-dev/correlated-concepts-1789586775612.json`; Preview took 1.691s. This closes the B03 ancestry verification gap. These backend capabilities do not yet provide the B04 guided frontend workflow.

## Existing journey

The integrated browser journey passed 25 assertions in 13.737s, including Builder, Preview, Publish, Viewer, filtering, CSV export, and reload. Evidence is at `.artifacts/loom-dev/6d7df93d6a37/mu4g56q1-8d2ee267/report.json`.

The UI suite passed 131 tests across 22 files, including type and package-boundary checks. Integrated Explorer, server, publication, and published-reader package suites passed after the identity corrections.

After extension integration, the browser journey passed all 25 assertions in 12.352s. Evidence is `.artifacts/loom-dev/6d7df93d6a37/mu4hrsbx-9f75d55c/report.json`. The UI suite passed 132 tests across 22 files. `go test ./... -count=1`, OpenAPI ownership, and dataframe package-boundary checks passed. The full Go run used local listener permissions, resolving the worker's sandbox-only acceptance failures.

B02 still needs selection attachment to feed the membership digest into compilation identity alongside B04's typed population constraint. Storage/API verification alone does not prove that remaining compiler integration.

No remote branch, production service, or canonical demo was changed.
