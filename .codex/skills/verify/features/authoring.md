# Author a dataframe

A researcher starts an Explorer, chooses what one row represents, follows a
resource relationship, and selects fields for the table and filter controls.

## Sub-features

- `author-create` creates a blank Explorer and its first table.
- `author-root` starts one row per Patient.
- `author-related` adds Observation through `subject_Patient`.
- `author-fields` selects ID, repeated family names, gender, and numeric value.

## How to get to it (user POV)

Open the development URL and choose `Builder`. Expand `New explorer`, enter a
name, and choose `Create blank`. Name the first table and choose `Create table`.
Choose Patient in the graph, then Observation to add the supported route.

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
