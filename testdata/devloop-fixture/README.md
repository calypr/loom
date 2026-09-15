# Loom local development fixture

This two-resource fixture is intentionally small and deterministic. It has two
Patients, two related Observations, nested `name[].given[]` arrays, and a
missing `Patient.gender` value. The browser verification driver authors an
Explorer against this data through the Builder controls, then checks the new
receipt and materialization independently through the read APIs.

The verification case uses one `Patient -> Observation` relationship. It does
not claim semantics for multiple related-resource `FIRST` projections.
