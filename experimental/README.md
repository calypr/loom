# Local Arango development

This directory contains the checked-in ArangoDB and ClickHouse compose setup
used by the quickstart. Start it from the repository root:

```bash
rtk docker compose -f experimental/docker-compose.yml up -d
```

The runtime implementation lives under [`internal/store/arango/`](../internal/store/arango/)
and [`internal/ingest/`](../internal/ingest/). This is not a home for a second
query engine or manually maintained AQL recipes.

ClickHouse is available at native `clickhouse://127.0.0.1:9000` (and HTTP
`http://127.0.0.1:8123`) for published dataframe materializations. The operator
command uses the native driver by default:

```bash
./bin/arango-fhir-proto materialize-dataframe \
  --request dataframe.json \
  --name case-explorer \
  --clickhouse-url clickhouse://127.0.0.1:9000 \
  --clickhouse-database loom
```
