# CDA Builder browser QA ledger

This ledger tracks the visible Builder in the single local CDA stack. **Pass** means the stated browser path has evidence. **Partial** means one or more required checks are missing. A feature is complete only when its controls, CDA values, Apply, reload, edit or remove, and restoration pass where those actions apply. API success alone does not count.

The browser driver is [`scripts/verify-cda-builder.mjs`](../../../scripts/verify-cda-builder.mjs). Local DOM and network evidence is retained under `.artifacts/cda-builder/`. The earlier action inventory and evidence paths are in `.audit/cda-builder-qa/coverage.tsv`. These local evidence files are not checked in.

| Builder feature | DOM usability | CDA result | Saved state | Status and next check |
| --- | --- | --- | --- | --- |
| Choose a populated row type | Pass | Pass | Partial | Specimen starts with 742,505 authorized records. Rerun create, edit, and restore from a fresh Explorer. |
| Choose a row type without a safe ID | Unrun | Unrun | Unrun | Find a CDA row type without one safe string ID, then test the recovery flow. |
| Change the row definition | Fail | Unrun | Unrun | The editor exposes `EXPANDED` and raw empty collection policy names. Test each CDA result before revising this control. |
| Attach or clear a starting collection | Partial | Unrun | Unrun | The panel is visible, but no CDA selection path has been completed. |
| Manage tables | Pass | Pass | Pass | New, rename, duplicate, reorder, delete, undo, and reload passed on CDA drafts. |
| Add a direct scalar field | Pass | Pass | Partial | Visible values match CDA Specimen source records. Edit and remove of that field need one fresh-page rerun. |
| Add a sparse direct field | Pass | Pass | Partial | Null and populated body-site references match source records. Edit and remove need a rerun. |
| Add a repeated direct field | Partial | Partial | Partial | Singleton code lists match source records; the CDA sample has not shown a multi-item code list. |
| Discover related resource fields | Pass | N/A | N/A | Eight CDA related resource types are visible and searchable from Add columns. [DOM and screenshot](../../../.artifacts/cda-builder/2026-09-26T21-47-54.756Z/related-source-chooser.json). |
| Add a related scalar field on a direct path | Pass | Pass | Pass | Patient `id` values match the Specimen patient references; proposal, Apply, reload, edit label, remove, and restored preview passed. [Apply](../../../.artifacts/cda-builder/2026-09-26T22-04-37.511Z/patient-related-applied.json), [edit and restore](../../../.artifacts/cda-builder/2026-09-26T22-04-55.894Z/patient-related-edited-removed.json). Eight clicks from Add columns to Apply; proposal reported 45 ms. |
| Choose among several relationship paths | Partial | Partial | Partial | Shortest path is visible, longer paths expand, and ambiguity stays unselected. Test a saved indirect route and its source values. [Patient choices](../../../.artifacts/cda-builder/2026-09-26T21-58-49.195Z/patient-field-choice.json). |
| Change a saved related source or result form | Partial | Unrun | Unrun | The saved step editor opens and its label edit passes. Changing the source, route, form, or contributor rule needs a value check. |
| Choose which related records contribute | Partial | Unrun | Unrun | The form exposes field conditions; no CDA condition has passed preview and restoration. |
| Handle no related match | Partial | Unrun | Unrun | Count explains zero. Patient `54b50ad3-aa10-5483-85e2-5382aac7d374` has no direct `subject_Patient` Observation edge in Arango; verify list and presence behavior in the browser. |
| Handle several related matches | Partial | Unrun | Unrun | ALL, COUNT, and PRESENCE are selectable. Patient `02f8e963-73b8-50ea-b840-c4a80719a06a` has ten direct `subject_Patient` Observation edges in Arango; verify cardinality and values in the browser. |
| Add a coded concept | Partial | Pass | Partial | A Specimen to Observation value matched Arango before the dialog change. Rerun the new dialog through Apply, reload, and removal. |
| Explain field and code coverage | Partial | Partial | N/A | Source occurrence counts appear, but coverage against the current output rows is unavailable and stated as such in the UI. |
| Rename, reorder, show, hide, or remove columns | Partial | Partial | Partial | Rename and visibility persist; reorder and removal need the complete CDA path. |
| Mark a column for Viewer filtering | Pass | N/A | Pass | The presentation flag toggles and survives reload. It does not filter Builder output rows. |
| Filter rows by equality | Pass | Pass | Pass | One known CDA Specimen remains; saved condition edit, removal, and restoration passed. Rerun with the current Filter rows label. |
| Filter rows by missing value | Pass | Pass | Pass | Sparse body-site condition excludes the populated specimen; Apply, reload, remove, and restore passed. Rerun with the current label. |
| Summarize rows into groups | Partial | Partial | Pass | Whole-table count matched 742,505 and removal restored rows. Grouped-column values and editor usability need checks. |
| Expand a repeated value | Partial | Partial | Pass | Singleton code expansion applied and restored. Test row multiplication and empty-list policy on a bounded CDA case. |
| Expand related records | Partial | Unrun | Unrun | The editor is exposed; no complete CDA browser path yet. |
| Turn categories into columns | Partial | Unrun | Unrun | Pivot editor opens with enabled fields. Category discovery, values, Apply, reload, and removal remain. |
| Turn columns into rows | Partial | Pass | Pass | Two Specimen fields matched source values through proposal, Apply, reload, remove, and restore. The Reshape entry menu remains difficult to understand. |
| Choose a Reshape operation | Fail | N/A | N/A | Five operations have equal emphasis and technical descriptions. Explain each row and column effect before calling this usable. |
| Preview and diagnostics | Partial | Pass | N/A | Row limits returned expected counts; repair flow and user-perceived preview time need checks. |
| Review a saved draft | Pass | Pass | N/A | Review now loads a current 25-row sample from a fresh page. |
| Publish and inspect ClickHouse | Partial | Pass | Pass | The published Specimen table has 742,505 rows and matching sampled values. Browser publish request outlasted the driver; rerun on a bounded CDA output. |
| Open Viewer and export | Partial | Pass | Pass | Viewer shows the published rows. Current-version one-row ZIP works; export of an older publication returned 409. |
| Use legacy table shape settings | Unrun | Unrun | Unrun | Needs an isolated root-only table without saved construction steps. |

The next browser slices are row definition and population, related-record conditions and zero or many matches, and Reshape operations. Keep each slice bounded; do not republish the 742,505-row table for routine checks.
