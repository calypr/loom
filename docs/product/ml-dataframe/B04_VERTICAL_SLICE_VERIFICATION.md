# B04 population-to-row vertical slice verification

## Implemented boundary

Builder accepts an immutable selection revision without forcing that resource type to become the table row grain. The workspace stores the selection revision and a checked semantic route. Reconcile resolves the exact membership digest into receipt identity. Preview and publication enforce the selection through a typed Arango semijoin before the row window.

The first UI slice shows the selection size, chooses a supported route, attaches or clears the population, and preserves the attachment after reload. A table without an attached population still uses every authorized row resource.

The explicit **Check selected-resource coverage** action now runs a receipt-bound final-row witness query. Complete reports show exact selected, mapped, unmapped, and emitted-row counts plus a bounded authorized repair list. Incomplete reports expose no exact counts. Signed cursors bind every later page to the receipt, output, selection, generation, and authorization scope.

`SET_TABLE_ROOT` no longer deletes configured routes, columns, filters, actions, or population state. A same-root command is a no-op. A nontrivial root change returns `ROOT_REBASE_REQUIRED` before the draft compare-and-swap.

The Builder now replaces that rejected destructive path with a server-assessed
row change. The first bounded rebase promotes one direct child occurrence when
the catalog proves one inverse edge back to the old root. Assessment is
read-only and binds its proposal to the exact draft version, document digest,
and capability snapshot. Applying that proposal atomically preserves feature
keys, columns, filters, actions, and population selection while rebasing the
route. A stale proposal or an absent or ambiguous inverse relationship leaves
the draft unchanged and returns a typed conflict or unresolved reference.
The traversal control exposes each eligible direct child as a row-start choice.
Semantic recipes use exact authored traversal column names so feature identity
does not depend on where an occurrence currently sits in the route.

Independent sibling feature occurrences may now traverse the same catalog relationship without sharing occurrence identity or predicate scope. Reusing that relationship again along one root-to-leaf branch remains rejected when the route policy disallows repeated edges. Two optional count contributors with different predicates survive authoring, semantic compilation, physical lowering, and AQL rendering as two contributor sets.

## Executable evidence

The full Go suite passed:

```text
GOTOOLCHAIN=auto go test ./... -count=1
```

The UI passed 140 tests, the boundary check, TypeScript compilation, and the production build:

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

The row-rebase browser probe started with Patient rows and an Observation
child, clicked **Make rows** for Observation, accepted the assessed change, and
then ran Preview. The row-change assessment, command, reconcile, and preview
requests all returned 200. Observation became the root with Patient beneath it;
all four feature keys and the configured gender filter were identical before
and after the rebase.

- Report: `.artifacts/loom-dev/row-rebase-ui-1789673863601.json`
- DOM evidence: `.artifacts/loom-dev/row-rebase-ui-1789673863601.html`

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

The first live row rebase applied successfully but reconcile rejected the old
root features because public column keys encoded their former occurrence
prefix. Traversal lowering now supports `EXACT` authored names, and semantic
Builder recipes use it under translation version `authoring-v2-native-8`.
Feature keys therefore remain stable when their occurrences move. The replayed
browser journey reconciled and previewed successfully.

## Remaining B04 work

B04 remains in progress. This slice does not yet provide:

- selection variants and exclusions that create a new immutable starting collection;
- ambiguity repair controls for choosing among multiple valid inverse relationships;
- live exact-mapping assertions for both direct and reversed population routes;
- typed authoring and literal Arango execution of independent contributor predicates, plus required population-match and predicate-aware sharing rules;
- support for compatible rebases deeper than one direct child;
- a density benchmark that compares target scans with membership-driven traversal.

## Acceptance audit on 2026-09-16

The superseding B01-B08 plan classifies this work as a partial B04 slice.

| Issue | Status | Proven | Still required |
| --- | --- | --- | --- |
| `ML-B04-01` | In progress | Ordinary execution uses a bounded existence semijoin. The explicit receipt-bound report computes final-row witnesses, exact counts, and a bounded unmatched page; live evidence maps two selected files to one Specimen and returns only unlinked file 004. | Prove exact mappings for both direct and reversed routes and complete the sparse/dense performance gate. |
| `ML-B04-02` | In progress | Builder requests a draft-bound assessment and atomically applies an unambiguous direct-child rebase. Unit and HTTP-route tests prove preserved selection/filters/actions, read-only assessment, and no mutation after stale or unresolved proposals. The live browser journey proves the real control, stable feature keys and filter, successful reconcile, and Preview after Patient-to-Observation rebase. | Add an ambiguity-choice repair control and support compatible deeper rebases if the product journey requires them. |
| `ML-B04-03` | In progress | Sibling occurrences reuse one relationship with distinct stable IDs. Two optional count contributors retain different scalar predicates through semantic compilation, physical lowering, and AQL rendering. Repeats within one branch remain rejected, including edge updates that would collide with a descendant. | Add typed predicate authoring, required population matching, predicate-aware traversal sharing, and literal Arango execution proving the two counts differ without dropping the root row. |
| `ML-B04-04` | In progress | Builder attaches and clears one supplied selection, previews constrained rows, displays exact resulting/unmapped coverage with a bounded unmatched-resource list, and clears stale evidence on reload. | Add selection variants and exclusions, an explicit row-definition control, and another non-file-root journey. Semantic interpretation repair remains B06; reason-specific repair navigation remains B07. |

The current focused Go package gate passes. The UI boundary check, TypeScript
test compilation, and all 140 UI tests pass. The live API rerun produced
`.artifacts/loom-dev/population-row-1789599644955.json`. The browser rerun
produced `.artifacts/loom-dev/population-row-ui-1789600065099.html` after the
driver learned to clear retained population state before attachment. These
artifacts prove the implemented slice, not the missing acceptance criteria.

Commit `7074790d` passed the focused contributor-scope regressions and the full
B04 backend package gate covering Explorer, recipe execution, semantic planning,
physical compilation, AQL rendering, and the server. This is unit-level proof;
it does not replace the missing literal Arango execution or browser journey.

Commit `abc74479` passed `go test ./internal/explorer/... ./internal/server
-count=1`, the OpenAPI route-ownership check, all 140 Loom UI tests, TypeScript
test compilation, and the production UI build. Its real HTTP test assesses a
row change, applies the returned proposal through the command endpoint, verifies
the new row root and stable feature key, and rejects replay of the stale
proposal. This is API/UI unit proof; live Docker/browser evidence is still
required.

Commit `03cb2a80` adds the reachable traversal control and exact traversal
column naming. The complete recipe, semantic, compiler, Explorer, and server
package gate passed, as did OpenAPI ownership, all 140 UI tests, and the
production build. The retained Docker/browser report is
`.artifacts/loom-dev/row-rebase-ui-1789673863601.json`.

The B04 performance gate has no result. B02 selection-storage measurements do
not replace the required sparse-versus-dense population-plan comparison or the
row-change-to-preview timing.
