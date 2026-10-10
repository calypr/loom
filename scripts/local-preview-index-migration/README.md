# Local preview-index migration

This is a one-off, rerunnable **local verification setup migration** for the
owned CDA Compose stack. It is explicitly not evidence that the old index is
superseded by this compiler plan. The compiler-replacement metadata stays
unset; the manifest separately authorizes the local inventory transition.

The runner is read-only by default and requires both `--apply` and
`--authorize-local-migration` before it changes an index. It accepts an exact
`PreviewCoveringIndexSpec` projection emitted by the current native-shaped
compiler request through `--compiler-spec`. It does not infer a candidate name
or projection paths from the old index.

For this authorized verification case, the accepted candidate is pinned to
`Observation`, index name `loom_pivot_preview_cdeb305dcc142386`, fields
`project`, `dataset_generation`, `auth_resource_path`, `_key`, `payload.id`, and
`payload.status`, with stored value `payload.valueQuantity`. The helper rejects the
previous `subject.reference` candidate and other substituted projections. The
provenance artifact contains more evidence fields than the migration input;
project it to the four fields the migration helper accepts:

```sh
jq '{collection, name, fields, storedValues}' \
  /tmp/loom-category-pivot-covering-index-spec.json \
  > /tmp/loom-local-preview-index-spec.json
```

The Docker preflight requires the already-running API and Arango containers to
have the expected Compose project/service labels, to share a network, and for
the API's mounted `internal/` and `go.mod` to resolve to this checkout. The Go
helper runs through a temporary Go overlay in that owned API container and
connects only to `http://arangodb:8529`; it refuses to create the database or
collection. It also requires a document witness for project
`loom_dev_cda_fhir` and generation `cda-fhir-v1`.

Before mutation, it verifies all four compiler-owned Observation indexes by
their exact stable IDs, names, persistent type, field order, empty stored-value
lists, `sparse:false`, and `unique:false`. It validates the new full-hash name
and exact compiler-provided stored-value paths. It creates and verifies the
new index first, rereads the old ID and every unrelated index immediately
before deleting only that old ID, and verifies the final four-index inventory.
On a failed verification after creation it removes only the exact candidate ID;
if the old index was already removed, it restores the authorized old definition
before rolling the candidate back. All unrelated index definitions are
compared before and after.

The original index tuple is tied to the inventory reviewed on 2026-10-04. If
the local database has changed since then, the helper aborts and preserves the
current inventory for review.

When running this staged wrapper before copying it into the checkout's
`scripts/` directory, pass `--source-root /private/tmp/loom-construction-implementation`
or set `LOOM_LOCAL_INDEX_SOURCE_ROOT`; the wrapper verifies that the running
API source mounts resolve to that exact checkout. The installed script
defaults to the checkout containing itself.

After placing the exact compiler output in a JSON file with these four fields
(`collection`, `name`, `fields`, `storedValues`), run a dry inspection:

```sh
node scripts/local-preview-index-migration.mjs \
  --compiler-spec /tmp/loom-production-preview-index-spec.json
```

Only after reviewing the dry-run report should the explicitly authorized local
transition be applied:

```sh
node scripts/local-preview-index-migration.mjs \
  --compiler-spec /tmp/loom-production-preview-index-spec.json \
  --apply --authorize-local-migration
```

Each run writes a JSON report under `/tmp`. The script removes its temporary
helper files from the API container after execution. No migration has been run
as part of preparing this artifact.
