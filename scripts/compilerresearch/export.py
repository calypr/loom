#!/usr/bin/env python3
"""Export compiler research inputs from a generated, owned acceptance project."""
import argparse
import json
import re
import subprocess
from pathlib import Path

COLLECTIONS = {
    "registry": "loom_dataframe_recipes",
    "recipe-revisions": "loom_dataframe_recipe_revisions",
    "receipts": "loom_explorer_compilation_receipts",
    "workspaces": "loom_explorers",
    "draft-revisions": "loom_explorer_draft_revisions",
    "published-revisions": "loom_explorer_revisions",
    "workspace-projections": "loom_explorer_workspace_projections",
}


def command(*args):
    return subprocess.check_output(args, timeout=90)


def imported_recipe_refs(value):
    if isinstance(value, dict):
        imported = value.get("importedRecipe")
        if isinstance(imported, dict) and {"name", "translationVersion", "digest", "output"} <= imported.keys():
            yield (imported["name"], imported["translationVersion"], imported["digest"], imported["output"])
        for child in value.values():
            yield from imported_recipe_refs(child)
    elif isinstance(value, list):
        for child in value:
            yield from imported_recipe_refs(child)


def match_imported_bundles(snapshot):
    available = {
        (entry["name"], entry["translationVersion"], entry["digest"]): entry["Bundle"]
        for entry in snapshot["registry"]
    }
    refs = sorted(set(imported_recipe_refs(snapshot)))
    matches = []
    for name, version, digest, output in refs:
        bundle = available.get((name, version, digest))
        if bundle is None or not any(item.get("name") == output for item in bundle["outputs"]):
            raise RuntimeError(f"unresolved imported recipe pin {name}@{version}, output {output}")
        matches.append({"name": name, "translationVersion": version, "digest": digest, "output": output, "bundle": bundle})
    return matches


def export(container, database, destination):
    labels = json.loads(command("docker", "inspect", "--format", "{{json .Config.Labels}}", container))
    project = labels.get("com.docker.compose.project", "")
    if not re.fullmatch(r"loom-acceptance-[a-z\d-]+", project) or labels.get("com.docker.compose.service") != "arangodb":
        raise ValueError("export requires an owned acceptance Arango container")
    if not re.fullmatch(r"loom_acceptance_[a-f\d]{16}", database):
        raise ValueError("export requires an acceptance database namespace")

    def get(path, payload=None):
        options = ["docker", "exec", container, "wget", "-qO-"]
        if payload is not None:
            options.extend(["--header=Content-Type: application/json", "--post-data=" + json.dumps(payload)])
        return json.loads(command(*options, f"http://127.0.0.1:8529/_db/{database}{path}"))

    snapshot = {}
    for label, collection in COLLECTIONS.items():
        payload = {
            "query": "FOR document IN @@collection SORT document._key RETURN document",
            "bindVars": {"@collection": collection},
            "batchSize": 100000,
        }
        response = get("/_api/cursor", payload)
        if response.get("error") or response.get("hasMore") or not isinstance(response.get("result"), list):
            raise RuntimeError(f"could not export complete collection {collection}")
        snapshot[label] = response["result"]

    snapshot["imported-bundles"] = match_imported_bundles(snapshot)
    destination.mkdir(mode=0o700, parents=True, exist_ok=False)
    for label, documents in snapshot.items():
        target = destination / f"{label}.jsonl"
        target.touch(mode=0o600)
        target.write_text("".join(json.dumps(document, sort_keys=True) + "\n" for document in documents))
    print(json.dumps({label: len(documents) for label, documents in snapshot.items()}, sort_keys=True))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("container")
    parser.add_argument("database")
    parser.add_argument("destination", type=Path)
    args = parser.parse_args()
    export(args.container, args.database, args.destination)


if __name__ == "__main__":
    main()
