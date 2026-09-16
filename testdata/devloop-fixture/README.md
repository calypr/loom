# Loom local development fixture

This two-resource fixture is intentionally small and deterministic. It has two
Patients, three related Observations, nested `name[].given[]` arrays, and a
missing `Patient.gender` value. The browser verification driver authors an
Explorer against this data through the Builder controls, then checks the new
receipt and materialization independently through the read APIs.

The first Patient has two Observations with different values. The verification
case uses one `Patient -> Observation` relationship and requires explicit
acknowledgment before publishing a first-related-record selection. It checks
that the first value remains deterministic and that the contract does not
claim this reduction is lossless or ready for machine learning.

Legacy FIRST sorts by the physical, generation-qualified resource key, not the
FHIR `id`. The chosen value can differ across fresh projects. The driver derives
one exact expected value from that ingestion-key contract and checks it in
Preview, publication, Viewer, and CSV; it does not accept either value loosely.
