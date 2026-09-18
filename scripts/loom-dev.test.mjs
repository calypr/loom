import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, rmSync, utimesSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { bootstrapSeedPlan, bootstrapWorkspaceNeedsSeed, commandEnvironment, createDevSession, createVerificationReport, expectedFixtureRelatedValue, fixtureSourceDigest, graphQLRowsRequest, sourceMountMatches } from './loom-dev.mjs';

test('fixture FIRST expectation follows independently observed storage-key ordering', () => {
  assert.equal(expectedFixtureRelatedValue('loom_dev_verify_mu4ctgo1-4a680895', 'fixture-v1'), 172.5);
  assert.equal(expectedFixtureRelatedValue('loom_dev_verify_mu4d6n33-4abffd57', 'fixture-v1'), 180);
});

test('development session defaults to isolated names, ports, and fixture', () => {
  const registryRoot = mkdtempSync(join(tmpdir(), 'loom-dev-registry-'));
  try {
    const target = createDevSession({ LOOM_DEV_PORT_REGISTRY: join(registryRoot, 'ports.json') }, process.cwd());
    assert.match(target.composeProject, /^loom-dev-[a-f0-9]{12}$/);
    assert.match(target.fixtureProject, /^loom_dev_[a-f0-9]{12}$/);
    assert.equal(target.fixtureGeneration, 'fixture-v1');
    assert.ok(target.apiPort >= 8180 && target.apiPort < 30000);
    assert.ok(target.uiPort >= 30000);
    assert.match(target.fixtureDir, /testdata\/devloop-fixture$/);
    assert.notEqual(target.composeProject, 'loom-demo');
  } finally {
    rmSync(registryRoot, { recursive: true, force: true });
  }
});

test('bootstrap seeding is limited to a new or interrupted default draft', () => {
  const catalog = {
    nodes: [{ nodeId: 'patient', resourceType: 'Patient', rowRootEligible: true }],
    candidates: [],
  };
  assert.equal(bootstrapWorkspaceNeedsSeed({ lifecycleState: 'NEW', draftVersion: 0, workspace: null, catalog }), true);
  assert.equal(bootstrapWorkspaceNeedsSeed({ lifecycleState: 'READY', draftVersion: 1, workspace: { documents: [{ output: { title: 'Patients' }, columns: [] }] }, catalog }), true);
  assert.equal(bootstrapWorkspaceNeedsSeed({ lifecycleState: 'READY', draftVersion: 2, workspace: { documents: [] }, catalog }), false);
  assert.equal(bootstrapWorkspaceNeedsSeed({ lifecycleState: 'READY', draftVersion: 2, workspace: { documents: [{ output: { title: 'Patients' }, columns: [] }] }, catalog }), false);
});

test('bootstrap seed plan uses the current Patient catalog, never transient project identities', () => {
  const state = {
    lifecycleState: 'NEW',
    draftVersion: 0,
    workspace: null,
    catalog: {
      nodes: [
        { nodeId: 'patient', resourceType: 'Patient', rowRootEligible: true },
        { nodeId: 'observation', resourceType: 'Observation', rowRootEligible: true },
      ],
      candidates: [
        { candidateId: 'patient-id-current', nodeId: 'patient', fieldPath: 'root.id', defaultProjectionMode: 'SCALAR', label: 'id' },
        { candidateId: 'patient-family-current', nodeId: 'patient', fieldPath: 'root.name[].family', defaultProjectionMode: 'INDEXED', label: 'name[].family' },
        { candidateId: 'patient-gender-current', nodeId: 'patient', fieldPath: 'gender', defaultProjectionMode: 'SCALAR', label: 'gender' },
        { candidateId: 'observation-id-current', nodeId: 'observation', fieldPath: 'id', defaultProjectionMode: 'SCALAR', label: 'id' },
      ],
    },
  };
  const plan = bootstrapSeedPlan(state);
  assert.equal(plan.createTable, true);
  assert.equal(plan.rootNodeId, 'patient');
  assert.deepEqual(plan.candidates.map((candidate) => candidate.candidateId), [
    'patient-id-current',
    'patient-family-current',
    'patient-gender-current',
  ]);
  assert.equal(JSON.stringify(plan).includes('loom_dev_verify_'), false);
});

test('default development sessions separate worktree identities and ports', () => {
  const registryRoot = mkdtempSync(join(tmpdir(), 'loom-dev-registry-'));
  const registry = join(registryRoot, 'ports.json');
  const roots = [mkdtempSync(join(tmpdir(), 'loom-dev-session-a-')), mkdtempSync(join(tmpdir(), 'loom-dev-session-b-'))];
  try {
    for (const root of roots) {
      mkdirSync(join(root, 'testdata/devloop-fixture'), { recursive: true });
      writeFileSync(join(root, 'go.mod'), 'module example.test\n');
      for (const file of ['Patient.ndjson', 'Observation.ndjson', 'recipe.json']) {
        writeFileSync(join(root, 'testdata/devloop-fixture', file), '{}\n');
      }
    }
    const env = { LOOM_DEV_PORT_REGISTRY: registry };
    const first = createDevSession(env, roots[0]);
    const second = createDevSession(env, roots[1]);
    assert.notEqual(first.composeProject, second.composeProject);
    assert.notEqual(first.fixtureProject, second.fixtureProject);
    assert.notEqual(first.apiPort, second.apiPort);
    assert.notEqual(first.uiPort, second.uiPort);
    assert.notEqual(first.artifacts, second.artifacts);
  } finally {
    for (const root of roots) rmSync(root, { recursive: true, force: true });
    rmSync(registryRoot, { recursive: true, force: true });
  }
});

test('derived ports are forwarded to Compose', () => {
  const target = createDevSession({}, process.cwd());
  const env = commandEnvironment(target);
  assert.equal(env.LOOM_DEV_SOURCE_ROOT, target.sourceRoot);
  assert.equal(env.LOOM_DEV_COMPOSE_PROJECT, target.composeProject);
  assert.equal(env.LOOM_DEV_PROJECT, target.fixtureProject);
  assert.equal(env.LOOM_DEV_GENERATION, target.fixtureGeneration);
  assert.equal(env.LOOM_DEV_HOST, target.host);
  assert.equal(env.LOOM_DEV_API_PORT, String(target.apiPort));
  assert.equal(env.LOOM_DEV_UI_PORT, String(target.uiPort));
  assert.equal(env.LOOM_DEV_EXPLORER, 'loom-dev-bootstrap');
  assert.match(target.populationMappingCursorSecret, /^[a-f0-9]{64}$/);
  assert.equal(env.LOOM_POPULATION_MAPPING_CURSOR_SECRET, target.populationMappingCursorSecret);
});

test('development cursor secret can be explicitly supplied', () => {
  const target = createDevSession({ LOOM_POPULATION_MAPPING_CURSOR_SECRET: 'local-session-cursor-secret' }, process.cwd());
  assert.equal(target.populationMappingCursorSecret, 'local-session-cursor-secret');
  assert.equal(commandEnvironment(target).LOOM_POPULATION_MAPPING_CURSOR_SECRET, 'local-session-cursor-secret');
});

test('explicit ports bypass an unusable port registry', () => {
  const target = createDevSession({
    LOOM_DEV_API_PORT: '8281',
    LOOM_DEV_UI_PORT: '3281',
    LOOM_DEV_PORT_REGISTRY: '/private/tmp/loom-dev-unusable-registry/ports.json',
  }, process.cwd());
  assert.equal(target.apiPort, 8281);
  assert.equal(target.uiPort, 3281);
});

test('stale port registry locks are recoverable', () => {
  const registryRoot = mkdtempSync(join(tmpdir(), 'loom-dev-registry-'));
  const registry = join(registryRoot, 'ports.json');
  const lock = `${registry}.lock`;
  try {
    writeFileSync(lock, 'stale');
    const staleTime = new Date(Date.now() - 60000);
    utimesSync(lock, staleTime, staleTime);
    const target = createDevSession({ LOOM_DEV_PORT_REGISTRY: registry }, process.cwd());
    assert.match(target.composeProject, /^loom-dev-[a-f0-9]{12}$/);
    assert.equal(target.apiPort, 8180);
  } finally {
    rmSync(registryRoot, { recursive: true, force: true });
  }
});

test('development session rejects canonical Compose and data targets', () => {
  assert.throws(
    () => createDevSession({ LOOM_DEV_COMPOSE_PROJECT: 'research-stack' }, process.cwd()),
    /must use the loom-dev namespace/,
  );
  assert.throws(
    () => createDevSession({ LOOM_DEV_PROJECT: 'another_research_dataset' }, process.cwd()),
    /must use the loom_dev_ namespace/,
  );
  assert.throws(
    () => createDevSession({ LOOM_DEV_COMPOSE_PROJECT: 'loom-demo' }, process.cwd()),
    /must not use the canonical loom-demo project/,
  );
  assert.throws(
    () => createDevSession({ LOOM_DEV_PROJECT: 'NCPI_ACCEPTANCE' }, process.cwd()),
    /must not target the canonical NCPI_ACCEPTANCE project/,
  );
  assert.throws(
    () => createDevSession({ LOOM_DEV_API_URL: 'http://example.test:8080' }, process.cwd()),
    /must point to this session's loopback port/,
  );
  assert.throws(
    () => createDevSession({ LOOM_DEV_HOST: '0.0.0.0' }, process.cwd()),
    /must be a loopback host/,
  );
});

test('verification report starts with an explicit build state and target ownership', () => {
  const target = createDevSession({}, process.cwd());
  const report = createVerificationReport(target);
  assert.equal(report.status, 'building');
  assert.equal(report.scenario, 'builder-preview-publish-viewer-filter-export');
  assert.equal(report.target.project, target.fixtureProject);
  assert.deepEqual(report.assertions, []);
  assert.deepEqual(report.timings, {});
  assert.deepEqual(report.evidencePaths, []);
  assert.deepEqual(report.limitations, []);
});

test('fixture source digest is stable across file enumeration order and excludes non-source files', () => {
  const fixture = mkdtempSync(join(tmpdir(), 'loom-dev-fixture-digest-'));
  try {
    writeFileSync(join(fixture, 'Observation.ndjson'), '{"id":"observation"}\n');
    writeFileSync(join(fixture, 'Patient.ndjson'), '{"id":"patient"}\n');
    writeFileSync(join(fixture, 'recipe.json'), '{"not":"source"}\n');
    const first = fixtureSourceDigest(fixture);
    writeFileSync(join(fixture, 'README.txt'), 'ignored\n');
    assert.equal(fixtureSourceDigest(fixture), first);
    writeFileSync(join(fixture, 'Patient.ndjson'), '{"id":"changed"}\n');
    assert.notEqual(fixtureSourceDigest(fixture), first);
  } finally {
    rmSync(fixture, { recursive: true, force: true });
  }
});

test('Docker Desktop host mount normalization accepts only this checkout', () => {
  const checkout = '/private/tmp/loom-devloop-impl.cDPSXd';
  assert.equal(sourceMountMatches(`${checkout}/cmd`, `${checkout}/cmd`), true);
  assert.equal(sourceMountMatches(`/host_mnt${checkout}/cmd`, `${checkout}/cmd`), true);
  assert.equal(sourceMountMatches('/host_mnt/private/tmp/another-checkout/cmd', `${checkout}/cmd`), false);
});

test('dataframe verification uses the versioned frontend output contract', () => {
  const request = graphQLRowsRequest(
    { fixtureProject: 'loom_dev_contract' },
    { recipe: 'cohort', translationVersion: 'v1', output: 'patients' },
    ['id', 'status'],
    [{ column: 'status', op: 'IN', value: ['active'] }],
  );
  assert.equal(
    request.query,
    'query VerifyRows($input: DataframeRowsInput!) { dataframeRows(input: $input) { materialization { id name revision projectId datasetGeneration state rowCount selector { recipe translationVersion output } } columns rows totalCount pageInfo { hasNextPage endCursor } } }',
  );
  assert.deepEqual(request.variables.input, {
    projectId: 'loom_dev_contract',
    selector: { recipe: 'cohort', translationVersion: 'v1', output: 'patients' },
    columns: ['id', 'status'],
    filters: [{ column: 'status', op: 'IN', value: ['active'] }],
    first: 25,
  });
});
