# Local Arango development

This directory contains the checked-in ArangoDB compose setup used by the
quickstart. Start it from the repository root:

```bash
rtk docker compose -f experimental/docker-compose.yml up -d
```

The runtime implementation lives under [`internal/store/arango/`](../internal/store/arango/)
and [`internal/ingest/`](../internal/ingest/). This is not a home for a second
query engine or manually maintained AQL recipes.
