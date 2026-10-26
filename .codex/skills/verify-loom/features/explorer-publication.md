# Explorer publication

## Sub-features

- Validate the researcher workspace paths against the loaded catalog.
- Materialize and publish the repository-owned Explorer workspace.
- Read the active viewer projection.

## How to get to it (user POV)

The acceptance command reaches publication after generation upload using the
workspace in `testdata/acceptance/ncpi-tcga-brca/workspace.json`.

## Driving it with HTTP

POST the workspace with `X-Loom-Source-Commit`, save `executionId`, then GET
the execution registry and `/api/v1/projects/{project}/explorers/default`.

## Gotchas

The current checkout must provide the server process. A pre-existing Loom pod
cannot prove the source under test. Run-specific databases avoid mutating the
deployed `fhir_proto` database.
