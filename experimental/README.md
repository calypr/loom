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
`http://127.0.0.1:8123`) for bundle-published dataframe materializations.
The retired `materialize-dataframe` command and its metadata collection are no
longer read or created. Existing `loom_dataframe_materializations` documents
and `loom_df_*` tables are left untouched for optional operator cleanup.

The real reader, aggregation, and streaming-path fixture is opt-in locally
and runs against the same service:

```bash
LOOM_CLICKHOUSE_URL=clickhouse://127.0.0.1:9000 \
LOOM_CLICKHOUSE_DATABASE=loom_test \
GOCACHE=$(pwd)/.gocache \
go test ./internal/dataframe/materialization ./internal/store/clickhouse \
  -run 'Test(ReaderAgainstRealClickHouse|ClickHouseNativeRoundTrip)$' -count=1
```
