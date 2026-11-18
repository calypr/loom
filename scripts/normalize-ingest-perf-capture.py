#!/usr/bin/env python3
import argparse
from contextlib import contextmanager
from decimal import Decimal
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import urllib.request
import uuid


INVENTORY = "fhir_semantic_inventory"
ENTRIES = "fhir_semantic_inventory_entries"
AQL = Path(__file__).resolve().parents[1] / "internal/catalog/arango/semantic_inventory.go"
SEMANTIC = AQL.parents[1] / "semantic.go"
IDENTITY = ("project", "dataset_generation", "build_id", "source_kind")


def encode(value):
    if isinstance(value, Decimal):
        return str(value)
    if isinstance(value, list):
        return "[" + ",".join(map(encode, value)) + "]"
    if isinstance(value, dict):
        return "{" + ",".join(encode(key) + ":" + encode(value[key]) for key in sorted(value)) + "}"
    return json.dumps(value, ensure_ascii=True, separators=(",", ":"))


def documents(path):
    with path.open() as source:
        for line in source:
            yield json.loads(line, parse_float=Decimal)


def request(url, path, *, method=None, data=None):
    headers = {"Content-Type": "application/json"}
    if isinstance(data, dict):
        data = json.dumps(data).encode()
    request = urllib.request.Request(url + path, method=method, data=data, headers=headers)
    with urllib.request.urlopen(request, timeout=180) as response:
        return json.loads(response.read(), parse_float=Decimal)


@contextmanager
def inventory_database(url):
    name = "loom_ingest_perf_" + uuid.uuid4().hex
    request(url, "/_api/database", data={"name": name})
    database = url + "/_db/" + name
    try:
        for collection in (INVENTORY, ENTRIES):
            request(database, "/_api/collection", data={"name": collection})
        yield database
    finally:
        request(url, "/_api/database/" + name, method="DELETE")


def query(database, text, variables):
    response = request(database, "/_api/cursor", data={
        "query": text, "bindVars": variables, "batchSize": 5000,
    })
    yield from response["result"]
    while response["hasMore"]:
        response = request(database, "/_api/cursor/" + response["id"], method="POST")
        yield from response["result"]


def materialized_entries(source, url):
    materializer = AQL.read_text().split("const materializeSemanticInventoryEntriesAQL = `", 1)[1].split("`", 1)[0]
    schema_version = int(re.search(r"(?m)^\s*SemanticObservationSchemaVersion\s*=\s*(\d+)", SEMANTIC.read_text())[1])
    identities = set()
    with inventory_database(url) as database:
        with source.open("rb") as rows:
            batch = []
            for line in rows:
                row = json.loads(line)
                source_kind = row.get("source_kind")
                identity = tuple(row[field] for field in IDENTITY[:-1]) + (
                    "file" if source_kind is None else source_kind,
                )
                identities.add(identity)
                batch.append(line)
                if len(batch) == 5000:
                    import_rows(database, batch)
                    batch.clear()
            if batch:
                import_rows(database, batch)
        for identity in sorted(identities):
            variables = dict(zip(IDENTITY, identity))
            variables["observation_schema"] = schema_version
            for _ in query(database, materializer, variables):
                pass
        for row in query(database, "FOR d IN " + ENTRIES + " RETURN UNSET(d, '_id', '_rev')", {}):
            yield row["_key"], row


def import_rows(database, rows):
    result = request(database, "/_api/import?complete=true&type=documents&collection=" + INVENTORY, data=b"".join(rows))
    if result["errors"] or result["created"] != len(rows):
        raise ValueError(f"Failed to import {len(rows)} inventory rows: {result}")


def normalize(source, output, arango_url):
    if not source.is_dir() or not any(source.glob("*.ndjson")):
        raise ValueError(f"No captured collections in {source}")
    output.mkdir(parents=True, exist_ok=False)
    for collection in sorted(source.glob("*.ndjson")):
        rows = (
            materialized_entries(collection, arango_url)
            if collection.stem == INVENTORY
            else ((document["_key"], document) for document in documents(collection))
        )
        with tempfile.NamedTemporaryFile(mode="w", dir=output) as unsorted:
            for key, value in rows:
                unsorted.write(encode(key) + "\t" + encode(value) + "\n")
            unsorted.flush()
            with (output / collection.name).open("wb") as normalized:
                subprocess.run(
                    ["sort", "-T", str(output), unsorted.name],
                    stdout=normalized,
                    env=dict(os.environ, LC_ALL="C"),
                    check=True,
                )


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("capture", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--arango-url", required=True, help="Disposable ArangoDB server for inventory materialization")
    args = parser.parse_args()
    normalize(args.capture, args.output, args.arango_url.rstrip("/"))
