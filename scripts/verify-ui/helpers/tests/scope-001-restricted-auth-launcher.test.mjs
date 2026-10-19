import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';
import {
  assertScope001ComposeIsolation,
  assertScope001Target,
  scope001ComposeEnvironment,
  scope001RuntimeEnvironment,
  scope001ToolImageTags,
} from '../../../run-scope-001-restricted-auth.mjs';
import { loadOwnedCdaTargetConfig } from '../owned-cda-target-config.mjs';
import { createScope001SyntheticBearer } from '../scope-001-restricted-auth-bootstrap.mjs';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../../..');
const targetPath = resolve(repositoryRoot, 'scripts/verify-ui/targets/scope-001-restricted-auth.target.example.json');
const expectedIdentity = { project: 'loom_dev_scope001-auth', generation: 'scope001-auth-v1' };

test('restricted-auth example resolves to the exact separate project, generation, fixture, and no-auth=false target', async () => {
  const loaded = await loadOwnedCdaTargetConfig({ targetPath, repositoryRoot, expectedIdentity, env: {} });
  assert.equal(assertScope001Target(loaded.target), true);
  assert.equal(loaded.target.sourceRoot, repositoryRoot);
  assert.equal(loaded.target.datasetDir, resolve(repositoryRoot, 'testdata/scope-001-restricted-auth'));
  assert.equal(loaded.target.noAuth, false);
});

test('fixture cursor key is target-scoped and trusted issuer and bearer stay in their intended environments', async () => {
  const loaded = await loadOwnedCdaTargetConfig({ targetPath, repositoryRoot, expectedIdentity, env: {} });
  const inherited = {
    LOOM_POPULATION_MAPPING_CURSOR_SECRET: 'inherited-not-for-fixture-use',
    LOOM_AUTH_CALYPR_TRUSTED_ISSUER: 'https://inherited.example/issuer',
    LOOM_SCOPE001_AUTH_BEARER: 'inherited-external-bearer',
  };
  const localIssuer = 'http://host.docker.internal:43210';
  const localBearer = createScope001SyntheticBearer(localIssuer, 'scope-001-launcher-test');
  const [, encodedClaims] = localBearer.split('.');
  assert.equal(JSON.parse(Buffer.from(encodedClaims, 'base64url').toString('utf8')).iss, localIssuer);

  const composeEnv = scope001ComposeEnvironment(loaded.target, inherited, localIssuer);
  const nativeEnv = scope001RuntimeEnvironment(loaded.target, inherited, localBearer, localIssuer);
  assert.match(composeEnv.LOOM_POPULATION_MAPPING_CURSOR_SECRET, /^[a-f0-9]{64}$/);
  assert.equal(composeEnv.LOOM_POPULATION_MAPPING_CURSOR_SECRET.length >= 16, true);
  assert.equal(Object.hasOwn(nativeEnv, 'LOOM_POPULATION_MAPPING_CURSOR_SECRET'), false);
  assert.notEqual(composeEnv.LOOM_POPULATION_MAPPING_CURSOR_SECRET, inherited.LOOM_POPULATION_MAPPING_CURSOR_SECRET);
  const otherTarget = { ...loaded.target, generation: 'scope001-auth-v2' };
  assert.notEqual(scope001ComposeEnvironment(otherTarget, inherited, localIssuer).LOOM_POPULATION_MAPPING_CURSOR_SECRET,
    composeEnv.LOOM_POPULATION_MAPPING_CURSOR_SECRET);

  const compose = await readFile(resolve(repositoryRoot, 'compose.dev.yaml'), 'utf8');
  assert.match(compose, /^\s+LOOM_POPULATION_MAPPING_CURSOR_SECRET: \${LOOM_POPULATION_MAPPING_CURSOR_SECRET:-}\s*$/m);
  assert.equal(composeEnv.LOOM_CDA_NO_AUTH, '0');
  assert.equal(composeEnv.LOOM_DEV_API_PORT, '8198');
  assert.equal(composeEnv.LOOM_DEV_UI_PORT, '30088');
  assert.equal(composeEnv.LOOM_AUTH_CALYPR_TRUSTED_ISSUER, localIssuer);
  assert.equal(nativeEnv.LOOM_AUTH_CALYPR_TRUSTED_ISSUER, localIssuer);
  assert.equal(composeEnv.LOOM_SCOPE001_AUTH_BEARER, undefined);
  assert.equal(JSON.stringify(composeEnv).includes(localBearer), false);
  assert.equal(JSON.stringify(composeEnv).includes('inherited-external-bearer'), false);
  assert.equal(nativeEnv.LOOM_SCOPE001_AUTH_BEARER, localBearer);
  assert.equal(nativeEnv.LOOM_SCOPE001_AUTH_BEARER === inherited.LOOM_SCOPE001_AUTH_BEARER, false);

  const composeWithoutIssuer = scope001ComposeEnvironment(loaded.target, inherited);
  const nativeWithoutIssuer = scope001RuntimeEnvironment(loaded.target, inherited);
  assert.equal(Object.hasOwn(composeWithoutIssuer, 'LOOM_AUTH_CALYPR_TRUSTED_ISSUER'), false);
  assert.equal(Object.hasOwn(nativeWithoutIssuer, 'LOOM_AUTH_CALYPR_TRUSTED_ISSUER'), false);
  assert.throws(() => scope001ComposeEnvironment(loaded.target, inherited, 'https://external.example/issuer'),
    /local Fence stub/);
  assert.throws(() => scope001RuntimeEnvironment(loaded.target, inherited, localBearer, 'https://external.example/issuer'),
    /local Fence stub/);
});

test('Compose model keeps database volumes target-owned and only the two approved Go caches external', () => {
  const target = {
    composeProject: 'loom-dev-scope001-auth',
    sourceRoot: repositoryRoot,
  };
  const imageTags = scope001ToolImageTags(repositoryRoot);
  const model = {
    name: target.composeProject,
    services: {
      arangodb: { volumes: [{ source: 'loom_dev_arangodb_data', target: '/var/lib/arangodb3', type: 'volume' }] },
      clickhouse: { volumes: [{ source: 'loom_dev_clickhouse_data', target: '/var/lib/clickhouse', type: 'volume' }] },
      'loom-api': {
        image: `loom-dev-scope001-auth-api:${imageTags.api}`,
        environment: { LOOM_AUTH_CALYPR_TRUSTED_ISSUER: 'http://host.docker.internal:43210' },
        volumes: [
          { source: 'loom_dev_go_module_cache', target: '/go/pkg/mod', type: 'volume' },
          { source: 'loom_dev_go_build_cache', target: '/root/.cache/go-build', type: 'volume' },
          { source: resolve(repositoryRoot, 'scripts/verify-ui/targets/scope-001-restricted-auth.air.toml'), target: '/workspace/.air.dev.toml', read_only: true },
          { source: resolve(repositoryRoot, 'scripts/verify-ui/targets/scope-001-restricted-auth.server.yaml'), target: '/workspace/scope-001-restricted-auth.server.yaml', read_only: true },
        ],
        extra_hosts: ['host.docker.internal=host-gateway'],
      },
      'loom-ui': { image: `loom-dev-scope001-auth-ui:${imageTags.ui}` },
    },
    volumes: {
      loom_dev_go_module_cache: { external: true, name: 'loom-dev-6d7df93d6a37_loom_dev_go_module_cache' },
      loom_dev_go_build_cache: { external: true, name: 'loom-dev-6d7df93d6a37_loom_dev_go_build_cache' },
      loom_dev_arangodb_data: { name: `${target.composeProject}_loom_dev_arangodb_data` },
      loom_dev_clickhouse_data: { name: `${target.composeProject}_loom_dev_clickhouse_data` },
    },
  };
  const localIssuer = 'http://host.docker.internal:43210';
  assert.equal(assertScope001ComposeIsolation(model, target, localIssuer), true);
  const missingHostGateway = structuredClone(model);
  missingHostGateway.services['loom-api'].extra_hosts = [];
  assert.throws(() => assertScope001ComposeIsolation(missingHostGateway, target, localIssuer), /host gateway/);
  const invalid = structuredClone(model);
  invalid.volumes.loom_dev_arangodb_data.external = true;
  assert.throws(() => assertScope001ComposeIsolation(invalid, target, localIssuer), /target-owned data volume/);
  const copiedCache = structuredClone(model);
  copiedCache.volumes.loom_dev_go_build_cache.name = `${target.composeProject}_loom_dev_go_build_cache`;
  assert.throws(() => assertScope001ComposeIsolation(copiedCache, target, localIssuer), /target-local cache copy/);
  const wrongIssuer = structuredClone(model);
  wrongIssuer.services['loom-api'].environment.LOOM_AUTH_CALYPR_TRUSTED_ISSUER = 'https://external.example/issuer';
  assert.throws(() => assertScope001ComposeIsolation(wrongIssuer, target, localIssuer), /exact local trusted Calypr issuer/);
});

test('Compose auth overlay pins the readonly Air/server configs and shared-cache names', async () => {
  const compose = await readFile(resolve(repositoryRoot, 'scripts/verify-ui/targets/scope-001-restricted-auth.compose.yaml'), 'utf8');
  assert.match(compose, /target: \/workspace\/\.air\.dev\.toml\s+read_only: true/);
  assert.match(compose, /target: \/workspace\/scope-001-restricted-auth\.server\.yaml\s+read_only: true/);
  assert.match(compose, /LOOM_AUTH_CALYPR_TRUSTED_ISSUER:\s*\"\$\{LOOM_AUTH_CALYPR_TRUSTED_ISSUER:\?set the local Scope-001 Fence issuer\}\"/);
  assert.match(compose, /name: loom-dev-6d7df93d6a37_loom_dev_go_module_cache/);
  assert.match(compose, /name: loom-dev-6d7df93d6a37_loom_dev_go_build_cache/);
});
