# Loom local development fixture

This fixture is intentionally small. It has two
Patients, three Patient-related Observations, nested `name[].given[]` arrays, and a
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

The non-Patient population fixture adds four DocumentReferences and two Specimens.
Files 001 and 002 both reference Specimen 001 through `subject`; file 003
references Specimen 002; file 004 has no specimen. They have no Patient dependency.

Literal population expectations:

- Selecting files 001, 002, and 003 with exclusion 002 retains exactly 001 and 003.
- Selecting files 001 and 002 yields exactly one specimen, 001, with two sources.
- Excluding file 002 leaves the same specimen with one source.
- File 004 remains an explicit unmatched source, not a fabricated specimen row.

Three further Observations exercise FHIR pairing without changing the Patient
journey. `dev-pair-001` has two components: `(urn:study:A, shared)` owns quantity
111 cm; `(urn:study:B, shared)` owns 222 cm. Each component also has a decoy Coding
item. Matching system and code independently would produce an incorrect pair.
Its two `urn:leaf` extensions sit under different parent URLs and contain exactly
`left-only` and `right-only`.

`dev-pair-002` supplies `not-numeric` through `valueString` for A/shared, not a
quantity. `dev-pair-003` supplies 333 cm with a missing coding system. The former
must not be coerced to a numeric value, and the latter must not be treated as
known A/shared or B/shared. Both raw records remain available.
