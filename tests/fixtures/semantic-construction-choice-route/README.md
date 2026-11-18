# Semantic construction choice route fixture

This small FHIR fixture gives the native ConceptCatalog case one exact path:

`Medication → MedicationAdministration → Patient → Observation`.

The first two route matches have one Observation each. One has nonblank
`valueCodeableConcept.text`; the other has blank text and one
`valueCodeableConcept.coding[].display`. A third MedicationAdministration
reaches a Patient with no Observation, so the final `focus_Patient` edge must be
OPTIONAL and preserve that Medication row.

The case seeds these records into a fresh disposable verification project. The
source files remain the independent row oracle throughout the native lifecycle.
