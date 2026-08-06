# Local snapshot-to-Explorer workflow

This guide separates commands available in this repository from integration
points owned by the external ETL and Explorer repositories. It does not invent
an ETL executable or frontend checkout.

## 1. Start dependencies and build Loom

```bash
rtk docker compose -f experimental/docker-compose.yml up -d
make generate-graphql
make build
make explorer-contract-test
```

Build and load a local image when exercising container startup:

```bash
make docker-build IMAGE=loom:reliability-local
rtk docker image inspect loom:reliability-local >/dev/null
```

`make docker-run IMAGE=loom:reliability-local` starts only the Loom container;
use Compose when Loom must reach the local ArangoDB and ClickHouse services.

## 2. Register immutable recipe versions

Set a distinct `translationVersion` in each recipe JSON. Validate syntax first:

```bash
jq -e '.name and .translationVersion and .outputs' ./dataframer-v1.json
jq -e '.name and .translationVersion and .outputs' ./dataframer-v2.json
```

Loom currently registers the configured recipe file during server startup.
Start once with each new version; the version-aware registry retains history
and rejects a changed digest after first execution:

```bash
LOOM_DEFAULT_RECIPE=aced-meta-default \
LOOM_DEFAULT_TRANSLATION_VERSION=2026-08-01 \
./bin/arango-fhir-server --no-auth \
  --dataframer-recipe "$PWD/dataframer-v1.json"
```

Stop the process before starting another local instance. Registering a later
version does not promote it. Promote only after validation with the operator
mutation shown in step 6.

## 3. Load a baseline generation or run snapshot ETL

The repository CLI can load a complete immutable META directory directly. Use
the source Git commit as its generation key:

```bash
PROJECT=ARANGODB_PROTO
GENERATION="$(git rev-parse HEAD)"
./bin/arango-fhir-proto load-generation \
  --url http://127.0.0.1:8529 \
  --database fhir_proto \
  --meta-dir META \
  --project "$PROJECT" \
  --generation "$GENERATION"
```

This existing CLI completes the older graph-generation activation workflow; it
does not emulate the new staged project-release boundary. Use it only to build
a local baseline.

The snapshot HTTP upload/resume/finalize API and checksum-aware ETL client are
integration deliverables from other repositories. No such ETL binary exists
in this checkout, so this guide cannot provide an honest executable name or
URL path for it yet. When integrated, run that client with the same project and
Git commit, require checksum verification and finalization, and wait for
release activation confirmation. Do not substitute the legacy mutable upload
except through its explicit rollout feature flag.

## 4. Start and poll exact materialization

Use Apollo Sandbox at `http://127.0.0.1:8080/apollo` or any GraphQL client:

```graphql
mutation Start($input: StartDataframeMaterializationInput!) {
  startDataframeMaterialization(input: $input) {
    id name translationVersion sourceGeneration state phase requestId
    error errorCode errorRetryable
    outputs {
      name state phase rowCount error errorCode errorRetryable
      selector { recipe translationVersion output }
    }
  }
}
```

```json
{
  "input": {
    "project": "ARANGODB_PROTO",
    "generation": "<git commit>",
    "selector": {
      "recipe": "aced-meta-default",
      "translationVersion": "2026-08-01",
      "output": "DocumentReference"
    }
  }
}
```

Poll the durable ID until it is `PUBLISHED` or `FAILED`:

```graphql
query Poll($id: ID!) {
  dataframeRecipeExecution(id: $id) {
    id state phase requestId errorCode errorRetryable
    outputs { name state phase rowCount errorCode errorRetryable }
  }
}
```

## 5. Inspect and activate the project release

Inspect execution state with the poll query above. The standalone release-read
query is supplied by the release service and was not frozen in this checkout;
do not guess its field name. Activation itself returns the authoritative active
release and uses compare-and-swap:

```graphql
mutation Activate($input: ActivateProjectReleaseInput!) {
  activateProjectRelease(input: $input) {
    id project generation revision state
  }
}
```

```json
{
  "input": {
    "project": "ARANGODB_PROTO",
    "releaseId": "<completed release id>",
    "expectedActiveRevision": "<observed revision or omit for first release>"
  }
}
```

A conflict requires re-reading the active revision. Never blindly retry with a
new expectation. A failed activation leaves the prior release active.

## 6. Inspect federation and promote a contract

Run the checked-in Explorer query after activation:

```bash
make explorer-query \
  EXPLORER_GRAPHQL_URL=http://127.0.0.1:8080/graphql/graph \
  EXPLORER_VARIABLES=examples/explorer-client/exact-selector.variables.json
```

Inspect `availability`, completeness, and every authorized project status in
the response. Promote a tested version explicitly:

```graphql
mutation Promote($input: PromoteDataframeContractInput!) {
  promoteDataframeContract(input: $input) {
    recipe translationVersion promotedAt
  }
}
```

```json
{"input":{"recipe":"aced-meta-default","translationVersion":"2026-08-01"}}
```

After promotion, legacy `dataType` resolves through this recipe/version. Verify
the compatibility window with:

```bash
make explorer-query \
  EXPLORER_GRAPHQL_URL=http://127.0.0.1:8080/graphql/graph \
  EXPLORER_VARIABLES=examples/explorer-client/legacy-data-type.variables.json
```

## 7. Connect the external Explorer

Point the external application at `/graphql/graph`, generate its client from
[`examples/explorer-client/explorer.graphql`](../examples/explorer-client/explorer.graphql),
and implement the behavior in
[`EXPLORER_VERSIONED_DATAFRAME_UX.md`](EXPLORER_VERSIONED_DATAFRAME_UX.md).
The default view is all authorized projects; no local project allowlist is
needed.
