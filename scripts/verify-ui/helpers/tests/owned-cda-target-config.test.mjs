import assert from 'node:assert/strict';
import { mkdtemp, mkdir, realpath, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { loadOwnedCdaTargetConfig } from '../owned-cda-target-config.mjs';

const expectedIdentity = Object.freeze({ project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' });

async function createConfigFixture(overrides = {}) {
  const parent = await mkdtemp(join(tmpdir(), 'loom-owned-cda-target-'));
  const repositoryRoot = join(parent, 'checkout');
  const datasetDir = join(parent, 'dataset');
  const configDirectory = join(repositoryRoot, '.codex');
  await mkdir(configDirectory, { recursive: true });
  await mkdir(datasetDir);
  const config = {
    schemaVersion: 1,
    sourceRoot: repositoryRoot,
    datasetDir,
    composeProject: 'loom-dev-local-1234',
    project: expectedIdentity.project,
    generation: expectedIdentity.generation,
    apiOrigin: 'http://127.0.0.1:8188',
    uiOrigin: 'http://127.0.0.1:30008',
    apiContainer: 'loom-dev-local-1234-loom-api-1',
    arangoContainer: 'loom-dev-local-1234-arangodb-1',
    clickhouseContainer: 'loom-dev-local-1234-clickhouse-1',
    noAuth: true,
    ...overrides,
  };
  const targetPath = join(configDirectory, 'owned-cda-target.json');
  await writeFile(targetPath, JSON.stringify(config), { mode: 0o600 });
  return {
    config,
    repositoryRoot,
    targetPath,
    cleanup: () => rm(parent, { recursive: true, force: true }),
  };
}

test('loads one explicit config and derives the canonical owned environment from its identity', async (t) => {
  const fixture = await createConfigFixture();
  t.after(fixture.cleanup);
  const loaded = await loadOwnedCdaTargetConfig({
    targetPath: '.codex/owned-cda-target.json',
    repositoryRoot: fixture.repositoryRoot,
    expectedIdentity,
    env: {},
  });

  assert.deepEqual(loaded.target, {
    sourceRoot: await realpath(fixture.repositoryRoot),
    datasetDir: await realpath(fixture.config.datasetDir),
    composeProject: fixture.config.composeProject,
    project: expectedIdentity.project,
    generation: expectedIdentity.generation,
    apiOrigin: 'http://127.0.0.1:8188',
    apiPort: '8188',
    uiOrigin: 'http://127.0.0.1:30008',
    uiPort: '30008',
    apiContainer: fixture.config.apiContainer,
    arangoContainer: fixture.config.arangoContainer,
    clickhouseContainer: fixture.config.clickhouseContainer,
    noAuth: true,
  });
  assert.equal(loaded.environment.LOOM_CDA_PROJECT, expectedIdentity.project);
  assert.equal(loaded.environment.LOOM_CDA_GENERATION, expectedIdentity.generation);
  assert.equal(loaded.environment.LOOM_CDA_API_PORT, '8188');
  assert.equal(loaded.environment.LOOM_CDA_UI_PORT, '30008');
  assert.equal(loaded.environment.LOOM_DEV_PROJECT, expectedIdentity.project);
  assert.equal(loaded.environment.LOOM_CDA_NO_AUTH, '1');
  assert.equal(Object.hasOwn(loaded, 'credentials'), false);
  assert.equal(loaded.validationScope, 'configuration-only');
  assert.equal(loaded.runtimeDatasetIdentity, 'not-checked');
});

test('requires the selected case registry identity instead of trusting config values', async (t) => {
  const fixture = await createConfigFixture({ project: 'loom_dev_other', generation: 'other-generation' });
  t.after(fixture.cleanup);

  await assert.rejects(loadOwnedCdaTargetConfig({
    targetPath: fixture.targetPath,
    repositoryRoot: fixture.repositoryRoot,
    expectedIdentity,
    env: {},
  }), /Target project does not match the selected case registry targetIdentity/);
  await assert.rejects(loadOwnedCdaTargetConfig({
    targetPath: fixture.targetPath,
    repositoryRoot: fixture.repositoryRoot,
    expectedIdentity: { project: 'loom_dev_other', generation: 'cda-fhir-v1' },
    env: {},
  }), /Target generation does not match the selected case registry targetIdentity/);
  await assert.rejects(loadOwnedCdaTargetConfig({
    targetPath: fixture.targetPath,
    repositoryRoot: fixture.repositoryRoot,
    env: {},
  }), /selected registry case must provide an independent targetIdentity/);
});

test('rejects conflicting CDA and legacy dev environment overrides without echoing their values', async (t) => {
  const fixture = await createConfigFixture();
  t.after(fixture.cleanup);
  for (const [name, value] of [
    ['LOOM_CDA_PROJECT', 'wrong_project_secret'],
    ['LOOM_CDA_GENERATION', 'wrong_generation_secret'],
    ['LOOM_DEV_PROJECT', 'wrong_dev_project_secret'],
    ['LOOM_CDA_API_ORIGIN', 'http://127.0.0.1:9999'],
  ]) {
    await assert.rejects(loadOwnedCdaTargetConfig({
      targetPath: fixture.targetPath,
      repositoryRoot: fixture.repositoryRoot,
      expectedIdentity,
      env: { [name]: value },
    }), (error) => {
      assert.match(error.message, new RegExp(`Conflicting inherited target environment variable ${name}`));
      assert.equal(error.message.includes(value), false);
      return true;
    });
  }
});

test('rejects URL credentials and never includes them in the error', async (t) => {
  const fixture = await createConfigFixture({ apiOrigin: 'http://private-user:private-password@127.0.0.1:8188' });
  t.after(fixture.cleanup);
  await assert.rejects(loadOwnedCdaTargetConfig({
    targetPath: fixture.targetPath,
    repositoryRoot: fixture.repositoryRoot,
    expectedIdentity,
    env: {},
  }), (error) => {
    assert.match(error.message, /apiOrigin must not embed credentials/);
    assert.equal(error.message.includes('private-user'), false);
    assert.equal(error.message.includes('private-password'), false);
    return true;
  });
});

test('rejects unknown credential fields and does not echo values', async (t) => {
  const fixture = await createConfigFixture({ password: 'do-not-print-this' });
  t.after(fixture.cleanup);
  await assert.rejects(loadOwnedCdaTargetConfig({
    targetPath: fixture.targetPath,
    repositoryRoot: fixture.repositoryRoot,
    expectedIdentity,
    env: {},
  }), (error) => {
    assert.match(error.message, /Unsupported target config fields: password/);
    assert.equal(error.message.includes('do-not-print-this'), false);
    return true;
  });
});

test('rejects ambiguous containers and duplicate local origins before any Docker work', async (t) => {
  const sameContainer = await createConfigFixture({ clickhouseContainer: 'loom-dev-local-1234-loom-api-1' });
  t.after(sameContainer.cleanup);
  await assert.rejects(loadOwnedCdaTargetConfig({
    targetPath: sameContainer.targetPath,
    repositoryRoot: sameContainer.repositoryRoot,
    expectedIdentity,
    env: {},
  }), /container names must be distinct/);

  const sameOrigin = await createConfigFixture({ uiOrigin: 'http://127.0.0.1:8188' });
  t.after(sameOrigin.cleanup);
  await assert.rejects(loadOwnedCdaTargetConfig({
    targetPath: sameOrigin.targetPath,
    repositoryRoot: sameOrigin.repositoryRoot,
    expectedIdentity,
    env: {},
  }), /API and UI origins must be distinct/);

  const samePort = await createConfigFixture({ uiOrigin: 'http://localhost:8188' });
  t.after(samePort.cleanup);
  await assert.rejects(loadOwnedCdaTargetConfig({
    targetPath: samePort.targetPath,
    repositoryRoot: samePort.repositoryRoot,
    expectedIdentity,
    env: {},
  }), /API and UI ports must be distinct/);
});

test('requires the config source root to be the checkout and the data directory to exist', async (t) => {
  const fixture = await createConfigFixture();
  t.after(fixture.cleanup);
  const otherRoot = join(fixture.repositoryRoot, 'other-checkout');
  await mkdir(otherRoot);
  const wrongSource = await createConfigFixture({ sourceRoot: otherRoot });
  t.after(wrongSource.cleanup);
  await assert.rejects(loadOwnedCdaTargetConfig({
    targetPath: wrongSource.targetPath,
    repositoryRoot: wrongSource.repositoryRoot,
    expectedIdentity,
    env: {},
  }), /sourceRoot must resolve to the repository checkout/);

  const missingData = await createConfigFixture({ datasetDir: join(fixture.repositoryRoot, 'missing-dataset') });
  t.after(missingData.cleanup);
  await assert.rejects(loadOwnedCdaTargetConfig({
    targetPath: missingData.targetPath,
    repositoryRoot: missingData.repositoryRoot,
    expectedIdentity,
    env: {},
  }), /ENOENT/);
});

test('requires a single explicit target path and an explicit schema version', async (t) => {
  const fixture = await createConfigFixture({ schemaVersion: 2 });
  t.after(fixture.cleanup);
  await assert.rejects(loadOwnedCdaTargetConfig({
    repositoryRoot: fixture.repositoryRoot,
    expectedIdentity,
    env: {},
  }), /targetPath must be a string/);
  await assert.rejects(loadOwnedCdaTargetConfig({
    targetPath: fixture.targetPath,
    repositoryRoot: fixture.repositoryRoot,
    expectedIdentity,
    env: {},
  }), /schemaVersion must be 1/);
});
