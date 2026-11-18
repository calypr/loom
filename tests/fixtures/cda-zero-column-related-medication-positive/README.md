# Positive five-hop Medication route fixture

This independent source fixture supplies one complete route:

`Specimen/positive-specimen <-focus- Observation/route-observation <-stage_assessment- Condition/route-condition -subject-> Patient/route-patient <-subject- MedicationAdministration/route-admin-a|b -medication_reference-> Medication/medication-a|b`.

The Observation contributes one focus edge, the Condition contributes one stage and one subject edge, and the two MedicationAdministration resources each contribute one subject edge and one Medication reference. The positive Specimen therefore matches exactly two distinct Medication IDs: `medication-a` and `medication-b`.

`Specimen/unmatched-specimen` has no route edges. It is the unmatched-parent source shape for a policy contrast; this source/extractor test does not materialize PRESERVE_PARENT or EXCLUDE rows. The existing registered browser lifecycle remains a separate negative case and does not consume this fixture.

`scope.json` supplies the project and generation that the dataset loader would attach to every source document and extracted edge.
