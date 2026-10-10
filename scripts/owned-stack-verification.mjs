import assert from 'node:assert/strict';
import { mkdir, readFile, realpath, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { assertOwnedCdaTarget } from './verify-ui/helpers/owned-cda-target.mjs';
import { checkContainerApiBuildStamp } from './verify-ui/helpers/api-build-freeze.mjs';
import {
  assertCapturedTargetMatches,
  inspectBuildStamp,
  parseCapturedBuildIdentity,
  runOwnedStackHealth,
} from './verify-ui/helpers/owned-stack-health.mjs';

const usage = `Usage:
  node scripts/owned-stack-verification.mjs --mode precheck --output <path>
  node scripts/owned-stack-verification.mjs --mode health --output <path> --identity <api-identity-before.json>

Load the owned LOOM_CDA_* environment first. Health mode expects the before-capture JSON from
scripts/capture-owned-verification.mjs and revalidates its owned target before sampling.`;

const { values, positionals } = parseArgs({
  options: {
    mode: { type: 'string', short: 'm' },
    output: { type: 'string', short: 'o' },
    identity: { type: 'string', short: 'i' },
    help: { type: 'boolean', short: 'h' },
  },
  allowPositionals: false,
  strict: true,
});
assert.equal(positionals.length, 0);
if (values.help) {
  console.log(usage);
  process.exit(0);
}

assert(['precheck', 'health'].includes(values.mode), 'Use --mode precheck|health.');
assert(values.output?.trim(), 'Provide --output with a result path.');
const output = resolve(values.output);
const identityPath = values.identity ? resolve(values.identity) : undefined;
if (values.mode === 'precheck') assert.equal(identityPath, undefined, 'Precheck mode does not accept --identity.');
if (values.mode === 'health') {
  assert(identityPath, 'Health mode requires --identity from capture-owned-verification.mjs.');
  assert.notEqual(identityPath, output, 'Health output and API identity input must use distinct paths.');
}

async function writeResult(value) {
  await mkdir(dirname(output), { recursive: true });
  await writeFile(output, `${JSON.stringify(value, null, 2)}\n`, { mode: 0o600 });
}

if (values.mode === 'precheck') {
  const container = process.env.LOOM_CDA_API_CONTAINER?.trim();
  assert(container, 'Set LOOM_CDA_API_CONTAINER in the owned capture environment.');
  const checkedAt = new Date().toISOString();
  const result = await checkContainerApiBuildStamp(container);
  const observation = inspectBuildStamp(result);
  const record = {
    checkedAt,
    targetContainer: container,
    command: '/workspace/loom-dev-build-stamp.sh --check',
    exitCode: observation.exitCode,
    apiBuildIdentity: observation.apiBuildIdentity,
    sourceDigestMatchesCurrentMountedSource: observation.sourceDigestMatchesCurrentMountedSource,
    runningBinaryMatchesRecordedBuild: observation.runningBinaryMatchesRecordedBuild,
    fresh: observation.fresh,
    ...(observation.failureKind ? { failureKind: observation.failureKind } : {}),
    ...(observation.diagnostic ? { diagnostic: observation.diagnostic } : {}),
    note: 'The helper runs the stamp --check. The first two digests must match; the third identifies the running binary and is not compared directly to the source digests.',
  };
  await writeResult(record);
  console.log(JSON.stringify({ exitCode: record.exitCode, apiBuildIdentity: record.apiBuildIdentity, fresh: record.fresh,
    ...(record.failureKind ? { failureKind: record.failureKind } : {}), ...(record.diagnostic ? { diagnostic: record.diagnostic } : {}), output }, null, 2));
  if (!record.fresh) process.exitCode = 1;
} else {
  const env = process.env;
  for (const name of [
    'LOOM_CDA_SOURCE_ROOT', 'LOOM_CDA_PROJECT', 'LOOM_CDA_API_ORIGIN', 'LOOM_CDA_UI_ORIGIN',
    'LOOM_CDA_API_CONTAINER', 'LOOM_CDA_COMPOSE_PROJECT',
  ]) assert(env[name]?.trim(), `Set ${name} in the owned capture environment.`);
  const sourceRoot = await realpath(resolve(env.LOOM_CDA_SOURCE_ROOT));
  const identityRecord = JSON.parse(await readFile(identityPath, 'utf8'));
  assert.equal(identityRecord.phase, 'before', 'Health identity must come from the before capture.');
  const expectedIdentity = parseCapturedBuildIdentity(identityRecord.apiBuildIdentity);
  const target = await assertOwnedCdaTarget({
    project: env.LOOM_CDA_PROJECT,
    apiOrigin: env.LOOM_CDA_API_ORIGIN,
    uiOrigin: env.LOOM_CDA_UI_ORIGIN,
    apiContainer: env.LOOM_CDA_API_CONTAINER,
    composeProject: env.LOOM_CDA_COMPOSE_PROJECT,
    sourceRoot,
    arangoContainer: env.LOOM_CDA_ARANGO_CONTAINER,
    clickhouseContainer: env.LOOM_CDA_CLICKHOUSE_CONTAINER,
  });
  assertCapturedTargetMatches(identityRecord.target, { ...target, generation: env.LOOM_CDA_GENERATION ?? null });
  const health = await runOwnedStackHealth({
    apiURL: env.LOOM_CDA_API_ORIGIN,
    uiURL: env.LOOM_CDA_UI_ORIGIN,
    apiContainer: env.LOOM_CDA_API_CONTAINER,
    expectedIdentity,
  });
  const result = { ...health, target: identityRecord.target };
  await writeResult(result);
  console.log(JSON.stringify({ status: result.status, samples: result.samples.length, apiBuildIdentity: result.apiBuildIdentity, output }, null, 2));
}
