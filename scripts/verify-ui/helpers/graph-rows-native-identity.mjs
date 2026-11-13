import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { collectBuildIdentity, resolveOwnedApiContainer } from '../../loom-dev.mjs';

export function graphRowsNativeIdentity(target) {
  const files = {};
  const visit = relative => {
    for (const entry of readdirSync(join(target.sourceRoot, relative), { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
      const path = `${relative}/${entry.name}`;
      if (entry.isDirectory()) visit(path);
      else if (entry.isFile()) files[path] = createHash('sha256').update(readFileSync(join(target.sourceRoot, path))).digest('hex');
    }
  };
  for (const path of ['internal', 'generated', 'schemas', 'openapi', 'ui/packages/loom-ui/src', 'ui/apps/demo/src', 'testdata/graph-rows']) visit(path);
  for (const path of ['go.mod', 'go.sum', 'scripts/loom-dev.mjs', 'scripts/package.json', 'scripts/package-lock.json', 'scripts/run-graph-rows-native.mjs', 'scripts/prepare-graph-rows-history.mjs', 'scripts/revalidate-graph-rows-history.mjs', 'scripts/verify-ui/helpers/graph-rows-history-fixture.mjs', 'scripts/verify-ui/helpers/tests/graph-rows-history-fixture.test.mjs', 'scripts/verify-ui/registry.mjs', 'scripts/graph-rows.playwright.config.mjs', 'scripts/verify-ui/specs/graph-rows.spec.mjs', 'scripts/verify-ui/helpers/graph-rows-fixture-oracle.mjs', 'scripts/verify-ui/helpers/graph-rows-complementary-oracle.mjs', 'scripts/verify-ui/helpers/tests/graph-rows-complementary-oracle.test.mjs', 'scripts/verify-ui/helpers/graph-rows-native-identity.mjs', 'scripts/verify-ui/helpers/tests/graph-rows-native-identity.test.mjs', 'scripts/verify-ui/workflows/graph-rows-related-rows.mjs', 'scripts/verify-ui/workflows/graph-rows-complementary.mjs', 'scripts/verify-ui/workflows/graph-rows-mixed.mjs', 'scripts/verify-ui/workflows/graph-rows-ordinary-policy.mjs', 'scripts/verify-ui/workflows/graph-rows-root-proposal.mjs', 'scripts/verify-ui/workflows/graph-rows-upstream-dependency.mjs', 'scripts/verify-ui/workflows/graph-rows-history-stress.mjs', 'scripts/verify-ui/helpers/tests/graph-rows-history-stress.test.mjs']) {
    files[path] = createHash('sha256').update(readFileSync(join(target.sourceRoot, path))).digest('hex');
  }
  const api = collectBuildIdentity(target, { repoRoot: target.sourceRoot });
  assert.equal(api.checkExit, 0, 'Native acceptance requires a running API built from current source');
  assert.equal(api.expectedSource, api.liveSource);
  assert.equal(api.recordedSource, api.liveSource);
  assert.equal(api.recordedBinary, api.runningBinary);
  assert(process.env.LOOM_GRAPH_ROWS_RUNTIME_CONFIG, 'Set LOOM_GRAPH_ROWS_RUNTIME_CONFIG to the exact runtime Air configuration');
  assert(process.env.LOOM_GRAPH_ROWS_COMPOSE_CONFIG, 'Set LOOM_GRAPH_ROWS_COMPOSE_CONFIG to the exact local Compose override');
  const runtimeComposeConfigurationSHA256 = createHash('sha256').update(readFileSync(process.env.LOOM_GRAPH_ROWS_COMPOSE_CONFIG)).digest('hex');
  const runtimeConfigurationSHA256 = createHash('sha256').update(readFileSync(process.env.LOOM_GRAPH_ROWS_RUNTIME_CONFIG)).digest('hex');
  const recipeReferenceSHA256 = createHash('sha256').update(readFileSync(join(target.sourceRoot, 'docs/work-packages/record-network-and-rows/fixtures/default-dataframer.json'))).digest('hex');
  assert.equal(recipeReferenceSHA256, '998c6aead0d3f432f3f0cf4d3fb515cbbacda9af53c0c090f457c3931b709dad');
  const { container } = resolveOwnedApiContainer(target);
  assert(container, 'Native runtime must have exactly one owned API container');
  const runtimeFiles = spawnSync('docker', ['exec', container, 'sha256sum', '/workspace/.air.dev.toml', '/workspace/graph-rows-default.json'], { encoding: 'utf8', timeout: 5000 });
  assert.equal(runtimeFiles.status, 0, 'Read exact live runtime configuration and recipe identities');
  const [liveRuntimeConfigurationSHA256, liveRecipeReferenceSHA256] = runtimeFiles.stdout.trim().split('\n').map(line => line.split(/\s+/)[0]);
  assert.equal(liveRuntimeConfigurationSHA256, runtimeConfigurationSHA256, 'Running API must use the recorded local configuration');
  assert.equal(liveRecipeReferenceSHA256, recipeReferenceSHA256, 'Running API must mount the unchanged default reference');
  const buildLimits = spawnSync('docker', ['exec', container, 'printenv', 'GOMAXPROCS', 'GOFLAGS'], { encoding: 'utf8', timeout: 5000 });
  assert.equal(buildLimits.status, 0);
  const liveBuildLimits = buildLimits.stdout.trim().split('\n');
  assert.deepEqual(liveBuildLimits, ['2', '-p=1'], 'Native runtime must use its bounded build configuration');
  return { runtimeComposeConfigurationSHA256, liveBuildLimits, liveRuntimeConfigurationSHA256, liveRecipeReferenceSHA256, files, sourceFingerprint: createHash('sha256').update(JSON.stringify(files)).digest('hex'), api, runtimeConfigurationSHA256, recipeReferenceSHA256 };
}

export function assertGraphRowsNativeIdentity(before, after) {
  assert.deepEqual(after.files, before.files, 'Source changed during the native run');
  assert.equal(after.sourceFingerprint, before.sourceFingerprint);
  assert.equal(after.api.liveSource, before.api.liveSource);
  assert.equal(after.api.runningBinary, before.api.runningBinary);
  assert.equal(after.runtimeComposeConfigurationSHA256, before.runtimeComposeConfigurationSHA256);
  assert.deepEqual(after.liveBuildLimits, before.liveBuildLimits);
  assert.equal(after.runtimeConfigurationSHA256, before.runtimeConfigurationSHA256);
  assert.equal(after.recipeReferenceSHA256, before.recipeReferenceSHA256);
}
