# Run the standalone Explorer demo

This tutorial starts Loom, ArangoDB, ClickHouse, and the Loom-owned Explorer UI. The seed job downloads the locked open-access FHIR Aggregator sample, loads 100 TCGA-BRCA patients and their related resources, publishes the patient cohort dataframe, and verifies the result before the UI opens.

The demo does not use Calypr, Fence, or browser authentication. Both exposed ports bind to `127.0.0.1`.

## Start the demo

Install Docker Desktop or another Docker installation with Compose v2. Then run:

```bash
make demo-up
```

The first run builds the Loom API and UI images and downloads 6,858 locked FHIR resources. Later runs reuse the content-addressed fixture cache and the database volumes.

Wait for this line:

```text
Loom demo is ready at http://127.0.0.1:3080
```

Open [the standalone Explorer](http://127.0.0.1:3080). The page starts with the `NCPI_ACCEPTANCE` project, the `default` Explorer, and the published TCGA-BRCA patient cohort.

The Builder is available from the same application. Preview and publish requests use Loom's V2 authoring API through the UI container's same-origin proxy.

Set the same variables on each demo command to select a Compose project,
different host ports, or another fixture and dataset. For example:

```bash
LOOM_DEMO_COMPOSE_PROJECT=analyst-demo \
LOOM_DEMO_API_PORT=18080 \
LOOM_DEMO_UI_PORT=13080 \
make demo-up
```

See [Demo configuration](DEMO_CONFIGURATION.md) for every setting and its
default. When you bind to `0.0.0.0` or `::`, set the corresponding URL to a
reachable host such as `http://127.0.0.1:18080`.

## Check the running demo

Run the smoke check:

```bash
make demo-smoke
```

This checks Loom readiness, the UI document, and the expected Explorer
management mode, generation, output ID, output title, and ordered schema. For a
custom fixture, set `LOOM_DEMO_OUTPUT_ID`, `LOOM_DEMO_OUTPUT_TITLE`, and, when
needed, `LOOM_DEMO_MANAGEMENT` on the smoke command.

Verify that the seeded Explorer renders through the real Builder and Viewer in
headless Chrome:

```bash
make demo-browser-smoke
```

Set `CHROME_BIN` if Chrome or Chromium is not on a standard path.

The seed job has already verified these deeper contracts:

- ArangoDB contains the locked Patient, Condition, Observation, ResearchStudy, and Specimen counts.
- ClickHouse contains 100 patient rows and 100 unique patients.
- The published dataframe schema matches `testdata/acceptance/ncpi-tcga-brca/oracle.json`.
- The GraphQL rows match the locked row digest.
- Publishing the same workspace twice reuses the same execution.

To inspect a service in the default project, run:

```bash
docker compose logs loom-api
docker compose logs loom-ui
docker compose logs demo-seed
```

## Stop or reset the demo

Stop the containers and retain the downloaded fixture and database data:

```bash
make demo-down
```

To remove the demo data and force a clean seed on the next run, remove the Compose volumes:

```bash
./scripts/demo-down.sh --volumes
```

The volume removal deletes only volumes in the `loom-demo` Compose project.
Set `LOOM_DEMO_COMPOSE_PROJECT` to stop or reset a custom project.

## Change the example Explorer

The demo seed reads these versioned inputs:

- `fixture.lock.json` fixes the upstream FHIR resource identities and digests.
- `workspace.json` defines the patient-level Explorer and Builder workspace.
- `oracle.json` fixes the expected schema, coverage, row count, and row digest.
- `recipe.json` supplies Loom's startup recipe before the Builder publishes the full workspace.

After you change the workspace or oracle, run `./scripts/demo-down.sh --volumes` and then `make demo-up`. Keep the fixture lock unchanged unless you intend to select a new upstream cohort.
