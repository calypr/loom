# Launch Loom from a data repository

Use the repository launcher when a checkout contains hydrated FHIR metadata and
a native Loom Explorer workspace:

```text
repository/
├── META/
│   ├── Patient.ndjson
│   └── Specimen.ndjson
└── CONFIG/
    └── HTAN_INT-BForePC.json
```

The CONFIG file must be an Explorer Builder workspace with API version
`loom.calypr.org/explorer-authoring/v2`. Legacy Gen3/Guppy Explorer JSON is not
accepted. Rebuild or export that configuration through the current Loom Builder
before using it with this launcher.

From the data repository, run:

```bash
/path/to/loom/scripts/loom-repo-up.sh
```

If the CONFIG filename does not identify the project, pass it explicitly:

```bash
/path/to/loom/scripts/loom-repo-up.sh \
  --project HTAN_INT/BForePC
```

Use `--config CONFIG/name.json` when CONFIG contains more than one JSON file.
From the Loom checkout, the equivalent command is:

```bash
make repository-up REPOSITORY=/path/to/data-repository
```

Each repository path gets a stable `loom-repository-<digest>` Compose project
by default, so it cannot reuse the standalone demo's containers or volumes.
Override the runtime when needed:

```bash
/path/to/loom/scripts/loom-repo-up.sh \
  --compose-project bforepc-demo \
  --api-port 18080 \
  --ui-port 13080
```

The launcher also accepts `--api-host` and `--ui-host`. Their environment
equivalents are `LOOM_REPOSITORY_COMPOSE_PROJECT`,
`LOOM_REPOSITORY_API_HOST`, `LOOM_REPOSITORY_API_PORT`,
`LOOM_REPOSITORY_UI_HOST`, and `LOOM_REPOSITORY_UI_PORT`; command-line flags
take precedence.

## Startup behavior

The launcher completes repository preflight before running Docker. It reads the
working tree directly and never invokes Git LFS, `git-drs`, smudge filters, or
credential helpers. If any selected META or CONFIG file is still an LFS/DRS
pointer, it lists every unresolved path and exits without changing Docker or the
databases.

After preflight succeeds, the launcher:

1. Starts ArangoDB, ClickHouse, and the no-auth Loom API.
2. Checks whether the content-addressed generation is reusable. If the
   generation is absent, uploads every `META/*.ndjson` file.
3. Publishes the CONFIG workspace, which materializes its outputs in ClickHouse.
4. Verifies the execution registry and Explorer Viewer response.
5. Starts the frontend and prints direct Builder and Viewer URLs.

The META content digest determines the generation ID, so changing CONFIG does
not reload ArangoDB. Repeating the same launch checks the persisted generation
before upload and reuses the same generation and publication identities.

## Local CONFIG write-back

The repository Compose overlay mounts only the selected CONFIG directory as
writable. In this explicit no-auth mode, a successful Builder Publish atomically
replaces the selected CONFIG file with the canonical published V2 workspace.
The frontend package does not write files and normal Loom/Calypr deployments do
not enable this server option.

If materialization or publication fails, the frontend is not started. Existing
database containers remain available for diagnosis and a subsequent launch
retries the same content-addressed operation.
