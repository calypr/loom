# Native browser run plan: basic entry and self-seeding CDA

Preparation only. Do not run these commands until the native conversion patch is integrated and the owner has cleared the browser-run window. This plan uses the already owned local stack; it does not rebuild or redeploy it. Run all Playwright commands from the canonical checkout at `/private/tmp/loom-construction-implementation/scripts`.

The canonical fixture contract is `scripts/playwright/cda-fixtures.mjs`: CDA runs require `LOOM_CDA_ARANGO_CONTAINER`; with the full source target below, the fixture also validates the real source root, dataset directory, Compose project, ports and origins, API container, project, and generation against the owned stack. The fixture takes a source fingerprint and API build identity before each case and checks both after the case. The field and cohort specs set `cdaRequireSourceFixture: true`. The fields test timeout is 300 seconds per case; Playwright's global timeout is 600 seconds, with one worker and no retries.

## Environment for the existing owned stack

Use these values only after confirming the owned stack is still the recorded `loom-dev-6d7df93d6a37` project. Container names below correspond to its Compose services (`loom-api`, `arangodb`, and `clickhouse`); the fixture validates ownership labels before using them. Do not substitute another container or point the source root at a different checkout.

```sh
export LOOM_CDA_SOURCE_ROOT=/private/tmp/loom-construction-implementation
export LOOM_CDA_DATASET_DIR=/Users/peterkor/Desktop/BMEG/loom/CDA-FHIR/META
export LOOM_CDA_COMPOSE_PROJECT=loom-dev-6d7df93d6a37
export LOOM_CDA_API_PORT=8188
export LOOM_CDA_UI_PORT=30008
export LOOM_CDA_API_ORIGIN=http://127.0.0.1:8188
export LOOM_CDA_UI_ORIGIN=http://127.0.0.1:30008
export LOOM_CDA_API_CONTAINER="${LOOM_CDA_COMPOSE_PROJECT}-loom-api-1"
export LOOM_CDA_ARANGO_CONTAINER="${LOOM_CDA_COMPOSE_PROJECT}-arangodb-1"
export LOOM_CDA_CLICKHOUSE_CONTAINER="${LOOM_CDA_COMPOSE_PROJECT}-clickhouse-1"
export LOOM_CDA_PROJECT=loom_dev_cda_fhir
export LOOM_CDA_GENERATION=cda-fhir-v1

# The basic fixture helper consumes the same owned dev-session identity.
export LOOM_DEV_SOURCE_ROOT="$LOOM_CDA_SOURCE_ROOT"
export LOOM_DEV_COMPOSE_PROJECT="$LOOM_CDA_COMPOSE_PROJECT"
export LOOM_DEV_API_PORT="$LOOM_CDA_API_PORT"
export LOOM_DEV_UI_PORT="$LOOM_CDA_UI_PORT"
export LOOM_DEV_API_URL="$LOOM_CDA_API_ORIGIN"
export LOOM_DEV_UI_URL="$LOOM_CDA_UI_ORIGIN"
export LOOM_DEV_PROJECT="$LOOM_CDA_PROJECT"
export LOOM_DEV_GENERATION="$LOOM_CDA_GENERATION"

# The legacy raw-related-record workflow reads these exact names. The Arango
# alias points at the already validated CDA target in this same shell.
export LOOM_ARANGO_CONTAINER="$LOOM_CDA_ARANGO_CONTAINER"
export LOOM_ARANGO_DATABASE=loom_dev
export LOOM_CDA_QUANTITY_WITNESS_REPORT=/tmp/loom-pivot-native-advanced-open/report.json

cd /private/tmp/loom-construction-implementation/scripts
```

No API token, password, or credential value is embedded in this plan. Leave optional cohort variant switches unset for the baseline groups below. They are separately selectable through `LOOM_COHORT_FIELD` (default `resourceType`; alternate `id`) and the `LOOM_COHORT_COMPOSITION`, `LOOM_COHORT_FILTER_ONE`, `LOOM_COHORT_POLICY_EDIT`, `LOOM_COHORT_POST_FILTER`, `LOOM_COHORT_COLLECTION_ROUND_TRIP`, `LOOM_COHORT_REMOVE_ANCHOR`, and `LOOM_COHORT_AUTHORED_EXPAND` flags (each defaults off).

## Source/API freeze and artifact capture

Create a private evidence directory outside the checkout, then capture the same source fingerprint manifest and API identity that `cda-fixtures.mjs` verifies around each CDA case. These are runbook commands, not commands executed during preparation.

```sh
export CDA_RUN_EVIDENCE="$(mktemp -d /tmp/loom-cda-native-evidence.XXXXXX)"

node --input-type=module > "$CDA_RUN_EVIDENCE/source-before.json" <<'NODE'
import { sourceFingerprintWithManifest } from './verify-ui/source-fingerprint.mjs';
process.stdout.write(`${JSON.stringify(sourceFingerprintWithManifest(process.env.LOOM_CDA_SOURCE_ROOT), null, 2)}\n`);
NODE

docker exec "$LOOM_CDA_API_CONTAINER" /workspace/loom-dev-build-stamp.sh --check \
  > "$CDA_RUN_EVIDENCE/api-build-before.txt"
```

After the final selected group, repeat those commands to `source-after.json` and `api-build-after.txt`. Compare the source JSON fingerprints/manifests and API identity text with `cmp`; any difference invalidates the run and requires investigation before accepting reports. Every CDA report also carries its per-case source/API before-and-after verification identity. Playwright writes the JSON summary to `../.artifacts/playwright/results.json`, test output and CDA report attachments under `../.artifacts/playwright/results/`, and the basic fixture's reports under `.artifacts/loom-dev/`. Preserve those artifacts with the evidence directory. `node verify-ui/coverage-status.mjs` from `scripts/` summarizes the basic fixture's registry reports; review each CDA case's attached `cda-report.json` and the Playwright JSON summary separately.

## Executable groups

Each selection below was enumerated with Playwright's official `--list` option against the current canonical specs. The basic selection lists exactly 2 cases; each field invocation lists 1; each rows pair lists 2; and the cohort invocations list 2, 1, 2, 2, and 2 cases respectively. These are discovery counts only; no browser tests were run.

The two basic cases use `scripts/playwright/fixtures.mjs` and the checked-in `testdata/devloop-fixture`; `group-entry` creates its own Group entry in a fresh fixture project. The compound BASIC case also selects, edits, removes, and reloads its coded group from a fresh synthetic fixture. They need the `LOOM_DEV_*` identity above, but no external CDA seed or selection file. The combined run has two cases at 180 seconds each (360 seconds total worst case).

```sh
npm run test:browser -- playwright/builder-authoring.spec.mjs playwright/standalone-misc.spec.mjs \
  --grep 'group-entry|Native standalone compound coded grouping: basic'
```

The native field cases each have a 300-second per-test timeout. Run one per Playwright invocation so each invocation remains below the 600-second global ceiling. Each case creates its own Explorer/source state through the native workflow; no preexisting Explorer, selection, or seed report is needed except for contributor-code, noted below.

```sh
npm run test:browser -- playwright/standalone-cda-fields.spec.mjs --grep 'CDA coded field lifecycle'
npm run test:browser -- playwright/standalone-cda-fields.spec.mjs --grep 'CDA cohort fields'
npm run test:browser -- playwright/standalone-cda-fields.spec.mjs --grep 'CDA compound fields'
npm run test:browser -- playwright/standalone-cda-fields.spec.mjs --grep 'CDA contributor exists'
npm run test:browser -- playwright/standalone-cda-fields.spec.mjs --grep 'CDA contributor rules'
npm run test:browser -- playwright/standalone-cda-fields.spec.mjs --grep 'CDA source fields'
npm run test:browser -- playwright/standalone-cda-fields.spec.mjs --grep 'CDA repeated contributor ANY'
```

Before running contributor-code, validate its independent quantity witness. The checked-in workflow defaults to this report path and requires a passed report for generation `cda-fhir-v1`, one Patient with at least two and at most 100 Observation rows, exactly one concrete `Observation.valueQuantity.code` value plus a null witness, and a raw Specimen document identity. The currently present report at the path below is recorded as passed for `cda-fhir-v1` with 31 Observation witnesses; still validate it immediately before the run. If it is absent, stale, or fails these conditions, leave this case pending until its witness-producing preparation workflow is run; do not invent substitute observations.

```sh
node --input-type=module - "$LOOM_CDA_QUANTITY_WITNESS_REPORT" <<'NODE'
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
const report = JSON.parse(await readFile(process.argv[2], 'utf8'));
const values = report.oracle?.domains?.['Observation.valueQuantity.code'];
assert.equal(report.status, 'passed');
assert.equal(report.oracle?.source?.generation, process.env.LOOM_CDA_GENERATION);
assert(Array.isArray(values) && values.some(value => value.kind === 'NULL'));
assert.equal(values.filter(value => ['STRING', 'CODE'].includes(value.kind)).length, 1);
assert(Array.isArray(report.oracle?.observations) && report.oracle.observations.length >= 2 && report.oracle.observations.length <= 100);
assert.equal(report.oracle.observationCount, report.oracle.observations.length);
assert(report.oracle?.source?._id);
console.log('quantity witness report is eligible');
NODE

npm run test:browser -- playwright/standalone-cda-fields.spec.mjs --grep 'CDA contributor code'
```

Rows cases use native self-seeding workflows and retain their raw-source identity/value oracles. Run pairs (two 180-second default-timeout cases, at most 360 seconds per invocation):

```sh
npm run test:browser -- playwright/standalone-cda-rows.spec.mjs \
  --grep 'CDA nested repeated component values|CDA repeated component empty-list policies'
npm run test:browser -- playwright/standalone-cda-rows.spec.mjs \
  --grep 'CDA related source ONE and ALL values|CDA repeated component rows'
```

The related ONE/ALL case self-seeds its Explorer but its current workflow also reads `LOOM_ARANGO_CONTAINER`, `LOOM_ARANGO_DATABASE=loom_dev`, `LOOM_CDA_API_CONTAINER`, and `LOOM_CDA_COMPOSE_PROJECT`; the exports above set those to the same owned stack. Its default mode and field are `cda` and `id`. Do not override those for this baseline.

Cohort cases each create their own membership/row sources and Selection revisions through the native workflow, so no external selection file is required. They use the default 180-second timeout; keep each invocation to two cases at most:

```sh
npm run test:browser -- playwright/standalone-cda-cohort.spec.mjs \
  --grep 'CDA authored EXPAND lifecycle|membership-revision-source-scope-preserved'
npm run test:browser -- playwright/standalone-cda-cohort.spec.mjs \
  --grep 'membership-revision-source-scope-change'
npm run test:browser -- playwright/standalone-cda-cohort.spec.mjs --grep 'CDA cohort row sources'
npm run test:browser -- playwright/standalone-cda-cohort.spec.mjs \
  --grep 'CDA row lineage: COMPOSED_RELATED compare|CDA row lineage: DIRECT_PIVOT compare'
npm run test:browser -- playwright/standalone-cda-cohort.spec.mjs \
  --grep 'CDA row lineage: DIRECT_PIVOT_SHARED_CONTRIBUTOR|CDA row lineage: DIRECT_GROUP_COUNT_PIVOT'
```

## Other seed/report prerequisites; keep out of the initial groups

These files currently exist under `/tmp`, but they serve distinct cases and are not prerequisites for the groups above:

- `/tmp/loom-legacy-collection-final/report.json` currently reports `passed` and names Explorer `collection-repair-1790882734024`. A legacy collection case still needs `LOOM_LEGACY_COLLECTION_SEED_REPORT` and that Explorer to exist in the target when run.
- `/tmp/loom-related-resource-type-after-unpivot-native-complete/report.json` currently reports `passed` for `cda-fhir-v1`; it witnesses the related-field-after-unpivot `gender-null` case.
- No `/tmp/loom-pivot-reload-native/report.json` is present. The `LOOM_PIVOT_RELOAD_SEED` case requires automated seed preparation before it is executable. The migration notes also record that the owned 31-category Pivot seed report is unavailable.
- Other `standalone-cda-other.spec.mjs` cases require existing `LOOM_QA_EXPLORER`; related eligibility also needs `LOOM_QA_SELECTION_ID`. Framing requires `LOOM_CDA_EXPLORER_ID`. Identifier multiplicity needs `LOOM_CDA_EXPLORER_SEED` or its named legacy Explorer, which must be confirmed present.
- `standalone-builder.spec.mjs` requires existing `LOOM_CDA_EXPLORER` and ClickHouse; its switch-explorers case additionally needs `LOOM_CDA_SECOND_EXPLORER` and `LOOM_CDA_SECOND_EXPLORER_TABLE`.
- The CDA cases in `standalone-misc.spec.mjs` that assert selection evidence require a valid `LOOM_CDA_SELECTION_EVIDENCE` JSON report; the constructor-removal case requires explicit Explorer, output, and step IDs.

Do not count skipped prerequisite-dependent cases as passed coverage. Keep them out of the initial baseline run until the named live state or seed artifact is verified.
