# SurrealQL Query Set

This directory is reserved for backend-specific SurrealQL dataframe queries.

The benchmark runner will only mark a Surreal run as fully comparable when the
matching SurrealQL query file exists and executes successfully with the same
logical output schema as the Arango AQL version.

Probe queries under `queries_surreal/probes/` are intentionally narrower than
the full dataframe export. Use them to isolate whether slowdown is in:

- `patient_driver.surql`: project-scoped patient scan and ordering
- `patient_one_hop.surql`: one-hop graph expansion from `Patient`
- `patient_file_rollup.surql`: specimen/group/file aggregation path

Example:

```bash
./arango-fhir-proto query-gdc-case-assay-matrix \
  --backend surreal \
  --url ws://127.0.0.1:8001 \
  --namespace fhir_proto \
  --database fhir_proto \
  --username root \
  --password root \
  --project ARANGODB_PROTO \
  --auth-resource-path EllrottLab-GDC_Data \
  --query queries_surreal/probes/patient_one_hop.surql \
  --max-rows 25
```
