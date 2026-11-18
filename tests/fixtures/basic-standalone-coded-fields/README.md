# Basic standalone coded-field catalog fixture

This fixture has two Patient root rows. Each Patient has one related Condition,
Observation, Specimen, and MedicationAdministration; each administration points
to one Medication. It exercises direct and two-hop source routes from the same
table and leaves the root IDs stable through a no-op `Patient.active` Filter.

The first Patient has a Condition and Medication CodeableConcept with two
different Coding displays, a Specimen type Coding with no display, and an
Observation `Diagnostic Method` code paired with a numeric value. The second
Patient has text-only Condition/Medication codes, no Specimen type, and no
Observation value. The expected coded projections therefore verify exact
Coding/display correlation, code-only display fallback, paired Observation
behavior, and absence preservation without synthesizing entries from the
text-only or missing controls.

`Condition`, `Observation`, and `Specimen` reference Patient through `subject`.
`MedicationAdministration.subject` joins each Patient to its administration,
then `MedicationAdministration.medication.reference.reference` joins to the
Medication record. The native test must use compiler-issued routes for these
sources and verify the current output stage after Filter.
