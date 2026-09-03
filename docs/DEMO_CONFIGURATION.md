# Demo configuration

The demo scripts read these environment variables. Use the same values for
`demo-up`, `demo-smoke`, `demo-browser-smoke`, and `demo-down`.

## Stack settings

| Variable | Default | Purpose |
| --- | --- | --- |
| `LOOM_DEMO_COMPOSE_PROJECT` | `loom-demo` | Compose project and volume namespace |
| `LOOM_DEMO_API_HOST` | `127.0.0.1` | API bind address |
| `LOOM_DEMO_API_PORT` | `8080` | API host port |
| `LOOM_DEMO_UI_HOST` | `127.0.0.1` | UI bind address |
| `LOOM_DEMO_UI_PORT` | `3080` | UI host port |
| `LOOM_DEMO_API_URL` | derived from the API host and port | URL used by readiness and smoke checks |
| `LOOM_DEMO_UI_URL` | derived from the UI host and port | URL used by readiness and smoke checks |
| `LOOM_API_IMAGE` | `loom-demo-api:local` | API and seed image tag |
| `LOOM_RECIPE_QUERY_PAGE_ROWS` | `25` | Root documents per bounded preview/materialization query; `0` restores single-query execution |

## Dataset settings

| Variable | Default | Purpose |
| --- | --- | --- |
| `LOOM_DEMO_PROJECT` | `NCPI_ACCEPTANCE` | Seeded project |
| `LOOM_DEMO_GENERATION` | `tcga-brca-locked` | Seeded generation |
| `LOOM_DEMO_FIXTURE_DIR` | `./testdata/acceptance/ncpi-tcga-brca` | Directory that contains `fixture.lock.json`, `workspace.json`, `oracle.json`, and `recipe.json` |

## Smoke expectations

| Variable | Default | Purpose |
| --- | --- | --- |
| `LOOM_DEMO_MANAGEMENT` | `REPOSITORY` | Expected Explorer management mode |
| `LOOM_DEMO_OUTPUT_ID` | `tcga_brca_cohort` | Expected runtime output ID |
| `LOOM_DEMO_OUTPUT_TITLE` | `TCGA-BRCA patient cohort` | Expected runtime output title |
| `LOOM_DEMO_EXPECTED_COLUMN_LABEL` | `Patient ID` | Column label required by the browser smoke check |
| `LOOM_DEMO_EXPECTED_CELL` | `TCGA-` | Cell text required by the browser smoke check |
| `LOOM_DEMO_EXPECTED_RESOURCES` | `Patient Condition Specimen Observation ResearchStudy` | Space-separated resource names required by the Builder smoke check |
| `LOOM_DEMO_BROWSER_URL` | project-specific URL derived from `LOOM_DEMO_UI_URL` | Base URL used by the browser smoke check |

`demo-smoke` reads the ordered physical column names from `oracle.json` in the
fixture directory. The check fails if the running output has a different
schema.
