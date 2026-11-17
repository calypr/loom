# Backend data migrations

These explicit commands open an existing local database. They do not run at
server startup. Apply only to an owned local development or disposable test
database; production and shared database runs require separate owner authorization.

Run the read-only inventory first:

```sh
go run ./cmd/arango-preview-index-audit --endpoint http://127.0.0.1:8529 --database loom_dev
```

The JSON retains the preview-index report fields and adds a `workspace` report.
Workspace counts distinguish authoritative packets from discovered copies and
include TableShape documents, legacy encodings, nil step lists, and digest
relations. A query or deadline failure exits nonzero without a complete report.
Unclassified copies, unrecognized stored digests, conflicting workspace packets,
or missing retained catalogs prevent workspace migration.

## Preview indexes: `wp19_preview_supersedes_v1`

```sh
go run ./cmd/arango-preview-index-migration --endpoint http://127.0.0.1:8529 --database loom_dev
go run ./cmd/arango-preview-index-migration --endpoint http://127.0.0.1:8529 --database loom_dev --mode apply --authorize-local-migration
```

The default is dry-run. Apply recognizes exact historical compiler-owned index
definitions, creates and verifies each replacement before deleting its old ID,
and preserves other indexes. It changes index metadata and writes a completion
marker in `loom_schema_migrations`; stored workspace, digest, and receipt bytes
do not change. A failed run can be retried. The marker's `replaced` list describes
the successful final attempt, rather than an accumulated history of prior attempts.

## Workspaces: `wp18_workspace_v1`

```sh
go run ./cmd/arango-workspace-migration --endpoint http://127.0.0.1:8529 --database loom_dev
go run ./cmd/arango-workspace-migration --endpoint http://127.0.0.1:8529 --database loom_dev --mode apply --authorize-local-migration
```

The default is dry-run and creates no collections or markers. Planning resolves
historical semantics against the exact retained catalog token, without selecting
a newer catalog. Apply updates mutable draft/repository workspace encodings and
their digests. Each changed owner advances once through revision-bound draft CAS;
its original history and canonical projection are inserted in that transaction.
The canonical digest change count includes projections whose original digest
differs, even though immutable records retain their original digest.

Published revisions, draft history, receipts, and their IDs remain unchanged.
Create-only canonical workspace projections support authoring from historical
revisions and history. Reads verify the original source fingerprint, context,
projection content address, and canonical digest after the migration is sealed.
Legacy receipts remain verifiable historical artifacts; Preview and Publish
require a new compilation receipt. Rewriting a receipt under its old ID is invalid.

Reruns reuse verified projections and preserve owner versions and marker revisions.
A sealed version with newly introduced legacy packets refuses further changes.
WP-18 leaves TableShape conversion to its separate migration. Runtime decoding
and compilation accept current workspaces and do not run historical conversions.
