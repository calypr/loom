# B04 population-to-row vertical slice verification

## Implemented boundary

Builder accepts an immutable selection revision without forcing that resource type to become the table row grain. The workspace stores the selection revision and a checked semantic route. Reconcile resolves the exact membership digest into receipt identity. Preview and publication enforce the selection through a typed Arango semijoin before the row window.

The first UI slice shows the selection size, chooses a supported route, attaches or clears the population, and preserves the attachment after reload. A table without an attached population still uses every authorized row resource.

The explicit **Check selected-resource coverage** action now runs a receipt-bound final-row witness query. Complete reports show exact selected, mapped, unmapped, and emitted-row counts plus a bounded authorized repair list. Incomplete reports expose no exact counts. Signed cursors bind every later page to the receipt, output, selection, generation, and authorization scope.

`SET_TABLE_ROOT` no longer deletes configured routes, columns, filters, actions, or population state. A same-root command is a no-op. A nontrivial root change returns `ROOT_REBASE_REQUIRED` before the draft compare-and-swap.

Independent sibling feature occurrences may now traverse the same catalog relationship without sharing occurrence identity or predicate scope. Reusing that relationship again along one root-to-leaf branch remains rejected when the route policy disallows repeated edges. Two optional count contributors with different predicates survive authoring, semantic compilation, physical lowering, and AQL rendering as two contributor sets.

## Executable evidence

The full Go suite passed:

```text
GOTOOLCHAIN=auto go test ./... -count=1
```

The UI passed 134 tests, the boundary check, TypeScript compilation, and the production build:

```text
npm test -- --run
npm run build --workspace @calypr/loom-ui
```

The isolated Docker stack passed `dev-doctor` and the existing Builder-to-Viewer browser scenario. The population API probe then proved these literal results:

- selected `dev-file-001` and `dev-file-002` produce one `dev-specimen-001` row;
- clearing the population produces both fixture Specimen rows;
- a complete empty selection produces zero rows;
- each population state has a different receipt ID.

The retained API evidence is `.artifacts/loom-dev/population-row-1789594521955.json`.

The population browser probe proved these user-visible results:

- Builder loads the immutable selection handoff;
- **Use selected resources** submits the population command;
- Preview contains `dev-file-001` and `dev-file-002`, but not `dev-file-003`;
- the attached population remains visible after a full reload.

The retained DOM evidence is `.artifacts/loom-dev/population-row-ui-1789594635734.html`.

The integrated 2026-09-17 rerun used three selected files: linked files 001 and
002 plus unlinked file 004. The API returned
`selected=3, mapped=2, unmapped=1, emittedRows=1` and only file 004. The
browser rendered `3 selected · 2 produce rows · 1 needs attention`, showed
`DocumentReference/dev-file-004`, kept Preview at one `dev-specimen-001` row,
and cleared the report after reload while preserving the population.

- API evidence: `.artifacts/loom-dev/population-row-1789666389159.json`
- DOM evidence: `.artifacts/loom-dev/population-row-ui-1789666860945.html`

## Live defect found and fixed

The first live Preview returned zero rows. Stored FHIR documents used the legacy hyphenated project ID, while selection members used the canonical slash form. The physical semijoin had reused the FHIR project bind for selection members. Runtime bindings now carry separate FHIR-storage and selection-storage project identities. The same live probe passed after the fix.

## Remaining B04 work

B04 remains in progress. This slice does not yet provide:

- selection variants and exclusions that create a new immutable starting collection;
- an explicit row-definition control and assess/apply rebase flow for changing row grain without silently deleting compatible features;
- live exact-mapping assertions for both direct and reversed population routes;
- typed authoring and literal Arango execution of independent contributor predicates, plus required population-match and predicate-aware sharing rules;
- a checked nontrivial root rebase that preserves compatible features;
- a density benchmark that compares target scans with membership-driven traversal.

## Acceptance audit on 2026-09-16

The superseding B01-B08 plan classifies this work as a partial B04 slice.

| Issue | Status | Proven | Still required |
| --- | --- | --- | --- |
| `ML-B04-01` | In progress | Ordinary execution uses a bounded existence semijoin. The explicit receipt-bound report computes final-row witnesses, exact counts, and a bounded unmatched page; live evidence maps two selected files to one Specimen and returns only unlinked file 004. | Prove exact mappings for both direct and reversed routes and complete the sparse/dense performance gate. |
| `ML-B04-02` | In progress | A same-root command preserves the table. An unsafe root change returns `ROOT_REBASE_REQUIRED` before draft persistence. | Assess a proposed row change, identify affected features, and atomically apply an unambiguous rebase without changing stable feature IDs. |
| `ML-B04-03` | In progress | Sibling occurrences reuse one relationship with distinct stable IDs. Two optional count contributors retain different scalar predicates through semantic compilation, physical lowering, and AQL rendering. Repeats within one branch remain rejected, including edge updates that would collide with a descendant. | Add typed predicate authoring, required population matching, predicate-aware traversal sharing, and literal Arango execution proving the two counts differ without dropping the root row. |
| `ML-B04-04` | In progress | Builder attaches and clears one supplied selection, previews constrained rows, displays exact resulting/unmapped coverage with a bounded unmatched-resource list, and clears stale evidence on reload. | Add selection variants and exclusions, an explicit row-definition control, and another non-file-root journey. Semantic interpretation repair remains B06; reason-specific repair navigation remains B07. |

The current focused Go package gate passes. The UI boundary check, TypeScript
test compilation, and all 134 UI tests pass. The live API rerun produced
`.artifacts/loom-dev/population-row-1789599644955.json`. The browser rerun
produced `.artifacts/loom-dev/population-row-ui-1789600065099.html` after the
driver learned to clear retained population state before attachment. These
artifacts prove the implemented slice, not the missing acceptance criteria.

Commit `7074790d` passed the focused contributor-scope regressions and the full
B04 backend package gate covering Explorer, recipe execution, semantic planning,
physical compilation, AQL rendering, and the server. This is unit-level proof;
it does not replace the missing literal Arango execution or browser journey.

The B04 performance gate has no result. B02 selection-storage measurements do
not replace the required sparse-versus-dense population-plan comparison or the
row-change-to-preview timing.
