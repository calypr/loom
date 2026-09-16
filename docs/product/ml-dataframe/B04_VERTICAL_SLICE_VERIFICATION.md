# B04 population-to-row vertical slice verification

## Implemented boundary

Builder accepts an immutable selection revision without forcing that resource type to become the table row grain. The workspace stores the selection revision and a checked semantic route. Reconcile resolves the exact membership digest into receipt identity. Preview and publication enforce the selection through a typed Arango semijoin before the row window.

The first UI slice shows the selection size, chooses a supported route, attaches or clears the population, and preserves the attachment after reload. A table without an attached population still uses every authorized row resource.

`SET_TABLE_ROOT` no longer deletes configured routes, columns, filters, actions, or population state. A same-root command is a no-op. A nontrivial root change returns `ROOT_REBASE_REQUIRED` before the draft compare-and-swap.

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

## Live defect found and fixed

The first live Preview returned zero rows. Stored FHIR documents used the legacy hyphenated project ID, while selection members used the canonical slash form. The physical semijoin had reused the FHIR project bind for selection members. Runtime bindings now carry separate FHIR-storage and selection-storage project identities. The same live probe passed after the fix.

## Remaining B04 work

B04 remains in progress. This slice does not yet provide:

- exact matched-member provenance such as `__loom_population_members`;
- unmapped selected-resource counts and repair controls;
- independent contributor-scoped relationship occurrences;
- a checked nontrivial root rebase that preserves compatible features;
- a density benchmark that compares target scans with membership-driven traversal.

## Acceptance audit on 2026-09-16

The superseding B01-B08 plan classifies this work as a partial B04 slice.

| Issue | Status | Proven | Still required |
| --- | --- | --- | --- |
| `ML-B04-01` | In progress | The compiler applies direct and reversed population semijoins. The saved live evidence maps two selected files to one Specimen. | Retain matched member IDs, report unmapped members, and prove both directions through live Preview. |
| `ML-B04-02` | In progress | A same-root command preserves the table. An unsafe root change returns `ROOT_REBASE_REQUIRED` before draft persistence. | Assess a proposed row change, identify affected features, and atomically apply an unambiguous rebase without changing stable feature IDs. |
| `ML-B04-03` | Not started | None. | Add independent contributor-scoped occurrences, typed predicates, required population matching, and predicate-aware traversal sharing. |
| `ML-B04-04` | In progress | Builder attaches and clears one supplied selection, chooses a unique route, previews constrained rows, and reloads the attachment. | Add selection variants and exclusions, an explicit row-definition control, resulting and unmapped counts, and a live non-file-root journey. |

The current focused Go package gate passes. The UI boundary check, TypeScript
test compilation, and all 134 UI tests pass. The current machine could not
repeat the live gate because Docker Desktop canceled credential access while
building `node:22.22.0-bookworm-slim`. The prior retained live artifacts remain
evidence for the implemented slice, not for the missing acceptance criteria.

The B04 performance gate has no result. B02 selection-storage measurements do
not replace the required sparse-versus-dense population-plan comparison or the
row-change-to-preview timing.
