# Author a dataframe

A researcher starts an Explorer, chooses what one row represents, follows a
resource relationship, and selects fields for the table and filter controls.

## Sub-features

- `author-create` creates a blank Explorer and its first table.
- `author-root` starts one row per Patient.
- `author-root-change` preserves or replaces the row record type of an existing table.
- `author-related` adds Observation through `subject_Patient`.
- `author-fields` selects ID, repeated family names, gender, and numeric value.

## How to get to it (user POV)

Open the development URL and choose `Builder`. Expand `New explorer`, enter a
name, and choose `Create blank`. Name the first table and choose `Create table`.
Choose `Choose Patient rows` for the initial table. Open Advanced graph later
to choose Observation and add the supported route.

## Driving it with loom-dev

Preconditions: `make dev-doctor` succeeds. The run owns a fresh project with
two Patients and one Observation for each Patient.

- Run `make verify-fast`. The driver creates the Explorer through the form and
  waits for its selection before creating a table.
- It chooses Patient and the exact controls `Add id to table`,
  `Add name[].family to table`, and `Add gender as filter`.
- After selecting Observation, it uses `Add valueQuantity.value to table`.
  Configured-field controls must appear before preview proceeds.
- Read the report and Builder DOM evidence. The selected Explorer must differ
  from the bootstrap Explorer and belong to this run's project.

## Gotchas

- Candidate lists are virtualized. Catalog presence does not prove selection.
- Commands persist asynchronously. Wait for configured fields and enabled actions.
- The UI retains its last table. Do not reset and reuse an earlier test Explorer.

## Registered focused lifecycle

`make verify-case SCENARIO=builder-authoring CASE=authoring TARGET_FROM_ENV=1`
selects the native test “Builder authoring previews, changes an existing table
root, and reloads”. Its fresh Basic fixture is
`testdata/builder-coded-source-column`; it verifies Patient/Gender rows,
publication/reload, and Patient↔Observation changes on the existing table.
Configure the owned environment as described in the verification skill first.
This Basic case does not establish the complete CDA row-operation inventory.

## Change what each row represents

For an existing table, open `Configure rows`. The `What should each row
represent?` box has one `Record type` picker. Choosing a connected type opens a
receipt-bound Preview automatically when its compiler route is unique. If the
compiler finds multiple valid routes, its alternatives appear inline in that
same picker; choosing one opens its Preview automatically. `Start fresh with
<type>` is available in the same box and opens a replacement Preview showing
removed setup and current-draft consumer effects. Disconnected types open their
replacement Preview directly.
Cancel preserves the saved table, and Apply commits the reviewed change.

The final serial native Basic batch passed all three row-root lifecycles on one
unchanged source and API identity: `builder-authoring/condition-specimen-route-ambiguity`
passed 17/17 checks for compiler route alternatives, receipt-bound Preview,
Cancel, Apply, exact fixture rows, and reload; `builder-authoring/authoring`
passed 22/22 checks for connected Patient↔Observation preservation, Cancel,
Apply, and Patient restoration; `builder-root-replacement/replace-current-setup`
passed 14/14 checks for disconnected ResearchStudy replacement and connected
DiagnosticReport `Start fresh`, including consumer effects and reload. The
shared batch summary is
`/private/var/folders/v_/j3zplgbs155cgwtqjftj377nd_4gyt/T/loom-verification-brackets/run-batch-1791858364721-WB0iLS/batch-summary.json`.
These Basic fixture lifecycles do not establish CDA-specific row behavior.

## Routed semantic value with blank fallback

`standalone-misc / semantic-construction-choice-route-fallback` selects the
native ConceptCatalog workflow in
`scripts/verify-ui/specs/semantic-construction-choice-route.spec.mjs`. Its
fresh fixture covers Observation.code `1222590007`, VALUE from
`valueCodeableConcept.text`, blank fallback to
`valueCodeableConcept.coding[].display`, and the optional Medication →
MedicationAdministration → Patient → Observation route. The case verifies the
exact Preview rows, Apply, reload, label edit, removal, and source/API
integrity for this fixture and route.

## Maintenance result

2026-10-11: after correcting the two stale selector expectations from `base`
to `current`, the same registered native authoring lifecycle passed 22/22
checks in 34.1 seconds. Exact Patient↔Observation rows, Cancel, Apply,
restoration and reload passed; maximum measured action was 899 ms. Source/API
identity stayed unchanged and after-health passed. This is Basic fixture proof.
