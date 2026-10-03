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
| `LOOM_UI_IMAGE` | `loom-demo-ui:local` | UI image tag |
| `LOOM_DEMO_RUN_ID` | `d000000000000001` | 16-hex run ID used for the ArangoDB and ClickHouse databases |
| `LOOM_DEMO_SEED` | `true` | Run `demo-seed`; acceptance sets this to `false` while redeploying the canonical demo |
| `LOOM_DEMO_SOURCE_ROOT` | current repository | Source root used by both API and UI builds |
| `LOOM_API_BUILD_CONTEXT` | `LOOM_DEMO_SOURCE_ROOT` | API Docker build context; overrides the source-root default |
| `LOOM_UI_BUILD_CONTEXT` | `LOOM_DEMO_SOURCE_ROOT/ui` | UI Docker build context; overrides the source-root default |
| `LOOM_DEMO_FIXTURE_CACHE_DIR` | Compose named volume `fixture_cache` | Optional host directory or named volume mounted at `/var/cache/loom` |
| `LOOM_RECIPE_QUERY_PAGE_ROWS` | `25` | Root documents per bounded preview/materialization query; `0` restores single-query execution |
| `LOOM_ACCEPTANCE_PROJECT` | `NCPI_ACCEPTANCE` | Project seeded inside the disposable acceptance database |

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

Docker Compose is the authoritative demo and acceptance deployment. `demo-up`
rebuilds the API and UI images from the configured build contexts and leaves
the selected Compose project running. `acceptance-real` redeploys canonical
`loom-demo` without reseeding it, then runs the locked fixture in a disposable
project. Performance runs also select unique project names, ports, source
roots, and image tags. Acceptance cleanup removes only generated projects and
never targets canonical volumes.
