import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, rmSync, utimesSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import {
  AUTHORING_SEMANTICS_VERSION,
  assertOwnedDevSession,
  authoringCommandSemanticsVersion,
  bootstrapSeedPlan,
  bootstrapWorkspaceNeedsSeed,
  collectBuildIdentity,
  commandEnvironment,
  createDevSession,
  createVerificationReport,
  createFreshVerificationFixture,
  generationLoadDisposition,
  normalizeDockerHostPath,
  parseBuildIdentity,
  resolveOwnedApiContainer,
  sourceMountMatches,
} from './loom-dev.mjs';

const makeSource = (label, { patient = '{"resourceType":"Patient","id":"patient-1"}\n', observation = '{"resourceType":"Observation","id":"observation-1"}\n', fixtureDir } = {}) => {
  const root = mkdtempSync(join(tmpdir(), `loom-dev-${label}-`));
  writeFileSync(join(root, 'go.mod'), 'module example.test/loom-dev\n');
  const fixture = fixtureDir ?? join(root, 'testdata/devloop-fixture');
  mkdirSync(fixture, { recursive: true });
  writeFileSync(join(fixture, 'Patient.ndjson'), patient);
  writeFileSync(join(fixture, 'Observation.ndjson'), observation);
  return { root, fixture };
};

const createSession = (root, extra = {}) => createDevSession({
  LOOM_DEV_API_PORT: '18180',
  LOOM_DEV_UI_PORT: '38180',
  ...extra,
}, root);

const ownedContainerSet = target => {
  const services = ['arangodb', 'clickhouse', 'loom-api', 'loom-ui'];
  return services.map((service, index) => {
    const container = {
      Id: `container-${index}`,
      Config: {
        Labels: {
          'com.docker.compose.project': target.composeProject,
          'com.docker.compose.service': service,
        },
        Env: service === 'loom-ui'
          ? [`VITE_LOOM_PROJECT=${target.fixtureProject}`, 'VITE_LOOM_EXPLORER=loom-dev-bootstrap']
          : [],
      },
      Mounts: [],
      NetworkSettings: { Ports: {} },
    };
    if (service === 'loom-api') {
      container.Mounts.push({ Destination: '/workspace/cmd', Source: join(target.sourceRoot, 'cmd') });
      container.NetworkSettings.Ports['8080/tcp'] = [{ HostPort: String(target.apiPort), HostIp: '127.0.0.1' }];
    }
    if (service === 'loom-ui') {
      container.Mounts.push({ Destination: '/workspace/packages/loom-ui/src', Source: join(target.sourceRoot, 'ui/packages/loom-ui/src') });
      container.NetworkSettings.Ports['8080/tcp'] = [{ HostPort: String(target.uiPort), HostIp: '127.0.0.1' }];
    }
    return container;
  });
};

const ownedResourceCommands = (target, containers) => ({
  compose: async args => args[0] === 'ps'
    ? { code: 0, stdout: containers.map(container => container.Id).join('\n'), stderr: '' }
    : { code: 1, stdout: '', stderr: `unexpected compose ${args.join(' ')}` },
  docker: async args => {
    if (args[0] === 'inspect') return { code: 0, stdout: JSON.stringify(containers), stderr: '' };
    if (args[0] === 'volume' && args[1] === 'ls') return { code: 0, stdout: 'loom-dev-data', stderr: '' };
    if (args[0] === 'volume' && args[1] === 'inspect') {
      return { code: 0, stdout: JSON.stringify([{ Name: 'loom-dev-data', Labels: { 'com.docker.compose.project': target.composeProject } }]), stderr: '' };
    }
    return { code: 1, stdout: '', stderr: `unexpected docker ${args.join(' ')}` };
  },
});

test('development sessions isolate worktrees and allocate stable ports for named Compose projects', () => {
  const registryRoot = mkdtempSync(join(tmpdir(), 'loom-dev-registry-'));
  const first = makeSource('source-a');
  const second = makeSource('source-b');
  try {
    const registry = join(registryRoot, 'ports.json');
    const env = { LOOM_DEV_PORT_REGISTRY: registry };
    const a = createDevSession(env, first.root);
    const b = createDevSession(env, second.root);
    assert.match(a.composeProject, /^loom-dev-[a-f0-9]{12}$/);
    assert.match(a.fixtureProject, /^loom_dev_[a-f0-9]{12}$/);
    assert.notEqual(a.composeProject, b.composeProject);
    assert.notEqual(a.apiPort, b.apiPort);
    assert.notEqual(a.uiPort, b.uiPort);
    assert.equal(createDevSession(env, first.root).apiPort, a.apiPort);

    const namedA = createDevSession({ ...env, LOOM_DEV_COMPOSE_PROJECT: 'loom-dev-one' }, first.root);
    const namedB = createDevSession({ ...env, LOOM_DEV_COMPOSE_PROJECT: 'loom-dev-two' }, first.root);
    assert.notEqual(namedA.apiPort, namedB.apiPort);
    assert.equal(createDevSession({ ...env, LOOM_DEV_COMPOSE_PROJECT: 'loom-dev-one' }, first.root).apiPort, namedA.apiPort);
  } finally {
    rmSync(registryRoot, { recursive: true, force: true });
    rmSync(first.root, { recursive: true, force: true });
    rmSync(second.root, { recursive: true, force: true });
  }
});

test('development sessions accept an external fixture directory and require both seed resources', () => {
  const root = makeSource('external-source');
  const fixture = mkdtempSync(join(tmpdir(), 'loom-dev-external-fixture-'));
  const missing = mkdtempSync(join(tmpdir(), 'loom-dev-missing-fixture-'));
  try {
    writeFileSync(join(fixture, 'Patient.ndjson'), '{"resourceType":"Patient","id":"external-patient"}\n');
    writeFileSync(join(fixture, 'Observation.ndjson'), '{"resourceType":"Observation","id":"external-observation"}\n');
    const target = createSession(root.root, {
      LOOM_DEV_FIXTURE_DIR: fixture,
      LOOM_DEV_COMPOSE_PROJECT: 'loom-dev-external',
      LOOM_DEV_PROJECT: 'loom_dev_external',
      LOOM_DEV_FIXTURE_TIMEOUT_MS: '900000',
      LOOM_DEV_ARTIFACTS: join(root.root, '.artifacts'),
    });
    assert.equal(target.fixtureDir, fixture);
    assert.equal(target.fixtureLoadTimeout, 900000);

    writeFileSync(join(missing, 'Observation.ndjson'), '{"resourceType":"Observation","id":"only-observation"}\n');
    assert.throws(() => createSession(root.root, {
      LOOM_DEV_FIXTURE_DIR: missing,
      LOOM_DEV_PROJECT: 'loom_dev_missing_fixture',
    }), new RegExp(`missing development fixture file: ${join(missing, 'Patient.ndjson').replaceAll(/[.*+?^${}()|[\]\\]/g, '\\$&')}`));
  } finally {
    rmSync(root.root, { recursive: true, force: true });
    rmSync(fixture, { recursive: true, force: true });
    rmSync(missing, { recursive: true, force: true });
  }
});

test('development session rejects canonical targets, public hosts, and URLs outside its own ports', () => {
  const source = makeSource('unsafe-session');
  try {
    assert.throws(() => createSession(source.root, { LOOM_DEV_COMPOSE_PROJECT: 'loom-demo' }), /must not use the canonical loom-demo project/);
    assert.throws(() => createSession(source.root, { LOOM_DEV_PROJECT: 'NCPI_ACCEPTANCE' }), /must not target the canonical NCPI_ACCEPTANCE project/);
    assert.throws(() => createSession(source.root, { LOOM_DEV_HOST: '0.0.0.0' }), /must be a loopback host/);
    assert.throws(() => createSession(source.root, { LOOM_DEV_API_URL: 'http://example.test:18180' }), /must point to this session's loopback port/);
  } finally {
    rmSync(source.root, { recursive: true, force: true });
  }
});

test('development command environment carries session identity and authoring semantics', () => {
  const source = makeSource('command-environment');
  try {
    const target = createSession(source.root);
    const env = commandEnvironment(target);
    assert.equal(env.LOOM_DEV_SOURCE_ROOT, source.root);
    assert.equal(env.LOOM_DEV_COMPOSE_PROJECT, target.composeProject);
    assert.equal(env.LOOM_DEV_PROJECT, target.fixtureProject);
    assert.equal(env.LOOM_DEV_GENERATION, target.fixtureGeneration);
    assert.equal(env.LOOM_DEV_API_PORT, String(target.apiPort));
    assert.equal(env.LOOM_DEV_UI_PORT, String(target.uiPort));
    assert.equal(env.LOOM_POPULATION_MAPPING_CURSOR_SECRET, target.populationMappingCursorSecret);
    assert.equal(authoringCommandSemanticsVersion({ workspace: { semanticsVersion: 7 } }), 7);
    assert.equal(authoringCommandSemanticsVersion({ workspace: null }), AUTHORING_SEMANTICS_VERSION);
  } finally {
    rmSync(source.root, { recursive: true, force: true });
  }
});

test('Docker mount paths normalize host-mounted paths before ownership checks', () => {
  assert.equal(normalizeDockerHostPath('/host_mnt/Users/me/loom'), '/Users/me/loom');
  assert.equal(normalizeDockerHostPath('/Users/me/loom'), '/Users/me/loom');
  assert.equal(sourceMountMatches('/host_mnt/Users/me/loom/cmd', '/Users/me/loom/cmd'), true);
  assert.equal(sourceMountMatches('/Users/other/loom/cmd', '/Users/me/loom/cmd'), false);
});

test('build identity parsing preserves source, running binary, recorded binary, and Git identity', () => {
  assert.deepEqual(parseBuildIdentity({
    stampOutput: 'expected-source live-source running-binary\n',
    recordedOutput: '{"binary":"recorded-binary","source":"recorded-source"}',
    gitHeadOutput: 'abc123\n',
  }), {
    expectedSource: 'expected-source',
    liveSource: 'live-source',
    runningBinary: 'running-binary',
    recordedBinary: 'recorded-binary',
    recordedSource: 'recorded-source',
    gitHead: 'abc123',
  });
  const source = makeSource('build-identity');
  try {
    const target = createSession(source.root);
    const result = collectBuildIdentity(target, {
      repoRoot: process.cwd(),
      psRunner: () => ({ status: 0, stdout: 'abcdef0123456789\n' }),
      runner: args => ({
        status: 0,
        stdout: args.at(-1) === '--check'
          ? 'expected-source live-source running-binary\n'
          : '{"binary":"recorded-binary","source":"recorded-source"}',
      }),
    });
    assert.equal(result.checkExit, 0);
    assert.equal(result.expectedSource, 'expected-source');
    assert.equal(result.runningBinary, 'running-binary');
    assert.match(result.gitHead, /^[a-f0-9]{7,40}$/);
  } finally {
    rmSync(source.root, { recursive: true, force: true });
  }
});

test('owned API container lookup rejects missing or ambiguous Compose identities', () => {
  const target = { composeProject: 'loom-dev-owned' };
  const selected = resolveOwnedApiContainer(target, { psRunner: () => ({ status: 0, stdout: 'abcdef0123456789\n' }) });
  assert.deepEqual(selected, { container: 'abcdef0123456789', error: null });
  assert.equal(resolveOwnedApiContainer(target, { psRunner: () => ({ status: 0, stdout: '' }) }).container, null);
  assert.equal(resolveOwnedApiContainer(target, { psRunner: () => ({ status: 0, stdout: 'abcdef0123456789\nabcdef0123456780' }) }).container, null);
});

test('dev-down ownership inspection accepts only the exact Compose project, mounts, ports, and volumes', async () => {
  const source = makeSource('owned-stack');
  try {
    const target = createSession(source.root);
    const containers = ownedContainerSet(target);
    const result = await assertOwnedDevSession(target, { resourceCommands: ownedResourceCommands(target, containers) });
    assert.deepEqual(result.ids, containers.map(container => container.Id));
    assert.deepEqual(result.volumes, ['loom-dev-data']);

    const unowned = structuredClone(containers);
    unowned[0].Config.Labels['com.docker.compose.project'] = 'loom-demo';
    await assert.rejects(
      () => assertOwnedDevSession(target, { resourceCommands: ownedResourceCommands(target, unowned) }),
      /refusing unowned Compose container/,
    );

    const wrongVolume = {
      ...ownedResourceCommands(target, containers),
      docker: async args => args[0] === 'volume' && args[1] === 'inspect'
        ? { code: 0, stdout: JSON.stringify([{ Name: 'foreign-volume', Labels: { 'com.docker.compose.project': 'loom-demo' } }]), stderr: '' }
        : ownedResourceCommands(target, containers).docker(args),
    };
    await assert.rejects(() => assertOwnedDevSession(target, { resourceCommands: wrongVolume }), /refusing unowned development volume/);
  } finally {
    rmSync(source.root, { recursive: true, force: true });
  }
});

test('fresh development fixture seeding uploads only the literal source NDJSON and creates its bootstrap Explorer', async () => {
  const patient = '{"resourceType":"Patient","id":"raw-patient"}\n';
  const observation = '{"resourceType":"Observation","id":"raw-observation","status":"final"}\n';
  const source = makeSource('raw-fixture-seed', { patient, observation });
  let upload = '';
  let generationSubmitted = false;
  const originalFetch = globalThis.fetch;
  try {
    globalThis.fetch = async (input, init = {}) => {
      const pathname = new URL(String(input)).pathname;
      const method = String(init.method ?? 'GET').toUpperCase();
      if (pathname.endsWith('/generations/fixture-v1') && method === 'GET') {
        return generationSubmitted
          ? new Response('{"state":"STAGED"}', { status: 200, headers: { 'content-type': 'application/json' } })
          : new Response('', { status: 404 });
      }
      if (pathname.endsWith('/generations/fixture-v1') && method === 'POST') {
        const form = init.body;
        upload = await Promise.all([...form.entries()]
          .filter(([name]) => name === 'file')
          .map(async ([, file]) => ({ name: file.name, contents: await file.text() })));
        assert.equal(form.get('defer_activation'), 'false');
        generationSubmitted = true;
        return new Response('{}', { status: 200, headers: { 'content-type': 'application/json' } });
      }
      if (pathname.endsWith('/explorers') && method === 'GET') {
        return new Response('[]', { status: 200, headers: { 'content-type': 'application/json' } });
      }
      if (pathname.endsWith('/explorers') && method === 'POST') {
        return new Response('{"explorerId":"bootstrap-explorer"}', { status: 201, headers: { 'content-type': 'application/json' } });
      }
      return new Response('', { status: 404 });
    };
    const session = createSession(source.root, {
      LOOM_DEV_API_PORT: '18181',
      LOOM_DEV_UI_PORT: '38181',
      LOOM_DEV_PROJECT: 'loom_dev_raw_seed',
      LOOM_DEV_COMPOSE_PROJECT: 'loom-dev-raw-seed',
    });
    const result = await createFreshVerificationFixture(session, 'raw-seed', { populateBootstrap: false });
    assert.equal(result.seed.fresh, true);
    assert.equal(result.seed.reused, false);
    assert.equal(result.seed.bootstrapExplorerId, 'bootstrap-explorer');
    assert.deepEqual(upload, [
      { name: 'Observation.ndjson', contents: observation },
      { name: 'Patient.ndjson', contents: patient },
    ]);
    assert(!upload.some(file => file.contents.includes('dev-j01-concept-')));
  } finally {
    globalThis.fetch = originalFetch;
    rmSync(source.root, { recursive: true, force: true });
  }
});

test('generation and bootstrap seed planners distinguish incomplete, ready, and failed states', () => {
  assert.equal(generationLoadDisposition({ state: 'LOADING' }), 'loading');
  assert.equal(generationLoadDisposition({ state: 'STAGED' }), 'ready');
  assert.equal(generationLoadDisposition({ state: 'READY' }), 'ready');
  assert.equal(generationLoadDisposition({ state: 'FAILED' }), 'failed');
  assert.equal(generationLoadDisposition({}), 'unknown');

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
        { candidateId: 'patient-id', nodeId: 'patient', fieldPath: 'root.id', defaultProjectionMode: 'SCALAR' },
        { candidateId: 'patient-family', nodeId: 'patient', fieldPath: 'root.name[].family', defaultProjectionMode: 'INDEXED' },
        { candidateId: 'patient-gender', nodeId: 'patient', fieldPath: 'gender', defaultProjectionMode: 'SCALAR' },
        { candidateId: 'observation-id', nodeId: 'observation', fieldPath: 'id', defaultProjectionMode: 'SCALAR' },
      ],
    },
  };
  assert.equal(bootstrapWorkspaceNeedsSeed(state), true);
  const plan = bootstrapSeedPlan(state);
  assert.equal(plan.createTable, true);
  assert.equal(plan.rootNodeId, 'patient');
  assert.deepEqual(plan.candidates.map(candidate => candidate.candidateId), ['patient-id', 'patient-family', 'patient-gender']);
  assert.equal(bootstrapWorkspaceNeedsSeed({ lifecycleState: 'READY', draftVersion: 2, workspace: { documents: [] } }), false);
});

test('core development CLI help loads without browser dependencies', () => {
  const result = spawnSync(process.execPath, [new URL('./loom-dev.mjs', import.meta.url).pathname, '--help'], {
    encoding: 'utf8', timeout: 5000, env: { ...process.env, NODE_TEST_CONTEXT: '', PATH: '' },
  });
  assert.ifError(result.error);
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /dev-doctor/);
  assert.match(result.stdout, /dev-down/);
  assert.doesNotMatch(result.stdout, /verify-|journey|Playwright/);
  assert.doesNotMatch(result.stderr, /ERR_MODULE_NOT_FOUND/);
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
  assert.equal(report.scenario, 'dev-stack');
  assert.equal(report.target.project, target.fixtureProject);
  assert.deepEqual(report.assertions, []);
  assert.deepEqual(report.timings, {});
  assert.deepEqual(report.evidencePaths, []);
  assert.deepEqual(report.limitations, []);
});
