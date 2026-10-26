# Preview and publish

A researcher checks the dataframe, then publishes the authored fields into a
dataset that Viewer can read.

## Sub-features

- `preview-values` shows both fixture rows and related numeric values.
- `preview-repeated` shows two family coordinates and array lengths.
- `publish-materialized` makes the authored output readable through GraphQL.

## How to get to it (user POV)

Configure the fields in [authoring](authoring.md), then inspect the automatic
preview for the current draft.
Inspect the table and dataframe contract. Choose `Publish`.

## Driving it with loom-dev

Preconditions: the run has authored its own Explorer in a fresh project.

- Run `make verify-fast`. Preview must show `dev-patient-001`, `Example`,
  `Example-Smith`, length `2`, and numeric value `172.5`.
- The other row must show `dev-patient-002`, `Builder`, a missing second family
  name, length `1`, and value `68`. Row order is not guaranteed.
- The driver clicks enabled `Publish`, then checks runtime and GraphQL data.
  Require exact IDs, family coordinates, counts, `female` versus null gender,
  and numeric Observation values.
- Read lineage and materialization evidence. Physical columns must resolve to
  the intended resource paths, including the generated array-count column.

## Gotchas

- Preview displays missing values as `—`; GraphQL returns null. ClickHouse
  64-bit counts arrive as strings. Assert each surface's actual contract.
- Generated names include occurrence-qualified fields and count columns.
  They do not all start with `col_`.
- An older readable runtime is insufficient. Before seeding, require no
  Explorers and no fixture generation in the run's unique project.
- This fixture does not prove one-to-many related-resource losslessness.

## Real CDA native recipes

Run from the owned checkout using its validated target:

```bash
node scripts/run-native-verification-bracket.mjs --scenario cda-publication --case bounded-specimen-publication-viewer-reload --target .codex/owned-cda-target.json
node scripts/run-native-verification-bracket.mjs --scenario cda-expanded-publication --case component-row-expand-publish-clickhouse-viewer-reload --target .codex/owned-cda-target.json
```

The first compares a bounded Specimen ID oracle with Preview, materialized
rows, Viewer and reload. The second compares independently selected Observation
component tuples through expansion, saved preview, publication, ClickHouse,
Viewer and reload. Both assert project, Explorer, generation, publication
revision and output identity before and after Viewer reload. Both passed in
the 2026-10-11 session; the expanded case first required a workflow-only
Playwright evaluation-callback correction. These bounded lifecycles do not
prove every source family, authorization configuration or row-operation composition.
