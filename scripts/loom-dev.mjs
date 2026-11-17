#!/usr/bin/env node

import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { closeSync, existsSync, mkdirSync, openSync, readFileSync, readdirSync, renameSync, statSync, unlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { EXPLORER_AUTHORING_SEMANTICS_VERSION } from '../ui/packages/loom-ui/src/authoringSemanticsVersion.mjs';

const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url));

const REPO_ROOT = resolve(SCRIPT_DIR, '..');
const COMPOSE_FILE = join(REPO_ROOT, 'compose.dev.yaml');
const VALID_NAME = /^[a-z][a-z0-9_-]{0,62}$/;
const VALID_IDENTIFIER = /^[A-Za-z][A-Za-z0-9_-]{0,62}$/;
const CANONICAL_PROJECT = 'loom-demo';
const CANONICAL_DATA_PROJECT = 'NCPI_ACCEPTANCE';
const LOOPBACK_HOSTS = new Set(['127.0.0.1', 'localhost', '::1']);
const OWNED_SERVICES = new Set(['arangodb', 'clickhouse', 'loom-api', 'loom-ui']);

const requiredFixtureFiles = ['Patient.ndjson', 'Observation.ndjson'];
const BOOTSTRAP_EXPLORER_NAME = 'loom-dev-bootstrap';
const BOOTSTRAP_EXPLORER_TITLE = 'Loom dev bootstrap';
const BOOTSTRAP_TABLE_TITLE = 'Patients';
const BOOTSTRAP_SEED_VERSION = 'v1';
const DEFAULT_PORT_REGISTRY = join(tmpdir(), 'loom-dev-port-registry.json');
const PORT_SLOT_COUNT = 8000;
const API_PORT_BASE = 8180;
const UI_PORT_BASE = 30000;
const PORT_LOCK_TIMEOUT_MS = 30000;
export const AUTHORING_SEMANTICS_VERSION = EXPLORER_AUTHORING_SEMANTICS_VERSION;

const waitSync = (milliseconds) => Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, milliseconds);

const withPortRegistryLock = (registryPath, operation) => {
  const lockPath = `${registryPath}.lock`;
  for (let attempt = 0; attempt < 200; attempt += 1) {
    try {
      const descriptor = openSync(lockPath, 'wx');
      try {
        return operation();
      } finally {
        closeSync(descriptor);
        unlinkSync(lockPath);
      }
    } catch (error) {
      if (error.code !== 'EEXIST') throw error;
      let stale = false;
      try { stale = Date.now() - statSync(lockPath).mtimeMs > PORT_LOCK_TIMEOUT_MS; } catch { stale = true; }
      if (stale) {
        try { unlinkSync(lockPath); } catch (unlinkError) {
          if (unlinkError.code !== 'ENOENT') throw unlinkError;
        }
        continue;
      }
      waitSync(10);
    }
  }
  throw new Error(`timed out waiting for development port registry lock: ${registryPath}`);
};

const allocatePortSlot = (registryPath, sourceRoot, identity) => withPortRegistryLock(registryPath, () => {
  let registry = { version: 1, assignments: {} };
  if (existsSync(registryPath)) registry = JSON.parse(readFileSync(registryPath, 'utf8'));
  if (registry.version !== 1 || typeof registry.assignments !== 'object') throw new Error(`invalid development port registry: ${registryPath}`);
  for (const [key, assignment] of Object.entries(registry.assignments)) {
    if (!existsSync(assignment.sourceRoot)) delete registry.assignments[key];
  }
  const existing = registry.assignments[identity];
  if (existing) {
    if (existing.sourceRoot !== sourceRoot) throw new Error(`development source identity collision: ${sourceRoot}`);
    return existing.slot;
  }
  const usedSlots = new Set(Object.values(registry.assignments).map((assignment) => assignment.slot));
  const slot = Array.from({ length: PORT_SLOT_COUNT }, (_, index) => index).find((candidate) => !usedSlots.has(candidate));
  if (slot === undefined) throw new Error('development port registry is full; remove unused worktrees and retry');
  registry.assignments[identity] = { sourceRoot, slot };
  const temporaryPath = `${registryPath}.${process.pid}.tmp`;
  writeFileSync(temporaryPath, `${JSON.stringify(registry, null, 2)}\n`, { mode: 0o600 });
  renameSync(temporaryPath, registryPath);
  return slot;
});

const defaultSessionValues = (sourceRoot, env) => {
  const identity = createHash('sha256').update(sourceRoot).digest('hex').slice(0, 12);
  const names = {
    composeProject: `loom-dev-${identity}`,
    project: `loom_dev_${identity}`,
    artifacts: join(sourceRoot, '.artifacts/loom-dev', identity),
  };
  const explicitAPIPort = envValue(env, 'LOOM_DEV_API_PORT', '');
  const explicitUIPort = envValue(env, 'LOOM_DEV_UI_PORT', '');
  if (explicitAPIPort && explicitUIPort) return { ...names, apiPort: explicitAPIPort, uiPort: explicitUIPort };
  const registryPath = envValue(env, 'LOOM_DEV_PORT_REGISTRY', DEFAULT_PORT_REGISTRY);
  const requestedComposeProject = envValue(env, 'LOOM_DEV_COMPOSE_PROJECT', names.composeProject);
  const portIdentity = requestedComposeProject === names.composeProject
    ? identity
    : `${identity}:${requestedComposeProject}`;
  const portSlot = allocatePortSlot(registryPath, sourceRoot, portIdentity);
  return {
    ...names,
    apiPort: String(API_PORT_BASE + portSlot * 2),
    uiPort: String(UI_PORT_BASE + portSlot * 2),
  };
};

const sleep = (ms) => new Promise((resolvePromise) => setTimeout(resolvePromise, ms));

const envValue = (env, key, fallback) => {
  const value = String(env[key] ?? '').trim();
  return value || fallback;
};

const portValue = (env, key, fallback) => {
  const value = envValue(env, key, fallback);
  if (!/^\d+$/.test(value) || Number(value) < 1 || Number(value) > 65535) {
    throw new Error(`${key} must be a TCP port between 1 and 65535`);
  }
  return Number(value);
};

const durationValue = (env, key, fallback, maximum) => {
  const value = envValue(env, key, String(fallback));
  if (!/^\d+$/.test(value) || Number(value) < 1 || Number(value) > maximum) {
    throw new Error(`${key} must be an integer between 1 and ${maximum} milliseconds`);
  }
  return Number(value);
};

const safeName = (value, field) => {
  if (!VALID_NAME.test(value)) throw new Error(`${field} must match ${VALID_NAME}`);
  return value;
};

const safeIdentifier = (value, field) => {
  if (!VALID_IDENTIFIER.test(value)) throw new Error(`${field} must match ${VALID_IDENTIFIER}`);
  return value;
};

const hostForURL = (host) => host.includes(':') && !host.startsWith('[') ? `[${host}]` : host;
const dockerHostPath = (path) => path.startsWith('/host_mnt/') ? path.slice('/host_mnt'.length) : path;

export const normalizeDockerHostPath = dockerHostPath;

export const sourceMountMatches = (mountedPath, expectedPath) =>
  resolve(dockerHostPath(String(mountedPath))) === resolve(expectedPath);

/**
 * A DevSession is the only input accepted by lifecycle and verification code.
 * It keeps ownership, URLs, fixture identity, and evidence location together.
 */
export const createDevSession = (env = process.env, cwd = REPO_ROOT) => {
  const sourceRoot = resolve(envValue(env, 'LOOM_DEV_SOURCE_ROOT', cwd));
  const defaults = defaultSessionValues(sourceRoot, env);
  const composeProject = safeName(envValue(env, 'LOOM_DEV_COMPOSE_PROJECT', defaults.composeProject), 'LOOM_DEV_COMPOSE_PROJECT');
  const project = safeIdentifier(envValue(env, 'LOOM_DEV_PROJECT', defaults.project), 'LOOM_DEV_PROJECT');
  const generation = safeName(envValue(env, 'LOOM_DEV_GENERATION', 'fixture-v1'), 'LOOM_DEV_GENERATION');
  const host = envValue(env, 'LOOM_DEV_HOST', '127.0.0.1');
  const apiPort = portValue(env, 'LOOM_DEV_API_PORT', defaults.apiPort);
  const uiPort = portValue(env, 'LOOM_DEV_UI_PORT', defaults.uiPort);
  const populationMappingCursorSecret = envValue(
    env,
    'LOOM_POPULATION_MAPPING_CURSOR_SECRET',
    createHash('sha256').update(`loom-dev-population-mapping-cursor\x00${sourceRoot}\x00${composeProject}\x00${project}`).digest('hex'),
  );
  const artifacts = resolve(envValue(env, 'LOOM_DEV_ARTIFACTS', defaults.artifacts));
  const fixtureDir = resolve(envValue(env, 'LOOM_DEV_FIXTURE_DIR', join(sourceRoot, 'testdata/devloop-fixture')));
  const fixtureLoadTimeout = durationValue(env, 'LOOM_DEV_FIXTURE_TIMEOUT_MS', 180000, 3_600_000);

  if (composeProject === CANONICAL_PROJECT) {
    throw new Error('development Compose must not use the canonical loom-demo project');
  }
  if (project === CANONICAL_DATA_PROJECT) {
    throw new Error('development fixture must not target the canonical NCPI_ACCEPTANCE project');
  }
  if (composeProject !== 'loom-dev' && !composeProject.startsWith('loom-dev-')) {
    throw new Error('development Compose project must use the loom-dev namespace');
  }
  if (!project.startsWith('loom_dev_')) {
    throw new Error('development data project must use the loom_dev_ namespace');
  }
  if (!LOOPBACK_HOSTS.has(host)) throw new Error('LOOM_DEV_HOST must be a loopback host');
  if (sourceRoot === '/') throw new Error('LOOM_DEV_SOURCE_ROOT cannot be the filesystem root');
  if (!existsSync(join(sourceRoot, 'go.mod'))) throw new Error(`source root has no go.mod: ${sourceRoot}`);
  if (sourceRoot === REPO_ROOT && !existsSync(COMPOSE_FILE)) throw new Error(`missing development Compose file: ${COMPOSE_FILE}`);
  if (fixtureDir === '/') throw new Error('LOOM_DEV_FIXTURE_DIR cannot be the filesystem root');
  if (!existsSync(fixtureDir) || !statSync(fixtureDir).isDirectory()) throw new Error(`missing development fixture: ${fixtureDir}`);
  for (const file of requiredFixtureFiles) {
    if (!existsSync(join(fixtureDir, file))) throw new Error(`missing development fixture file: ${join(fixtureDir, file)}`);
  }
  if (artifacts === '/' || artifacts === sourceRoot) throw new Error('LOOM_DEV_ARTIFACTS must be an owned child directory');

  const apiUrl = `http://${hostForURL(host)}:${apiPort}`;
  const uiUrl = `http://${hostForURL(host)}:${uiPort}`;
  for (const [key, expected] of [['LOOM_DEV_API_URL', apiUrl], ['LOOM_DEV_UI_URL', uiUrl]]) {
    if (env[key] === undefined || String(env[key]).trim() === '') continue;
    const supplied = new URL(String(env[key]));
    const wanted = new URL(expected);
    if (supplied.protocol !== 'http:' || supplied.hostname !== wanted.hostname || supplied.port !== wanted.port || supplied.pathname !== '/' || supplied.search || supplied.hash) {
      throw new Error(`${key} must point to this session's loopback port (${expected})`);
    }
  }
  return Object.freeze({
    kind: 'isolated',
    composeProject,
    composeFile: sourceRoot === REPO_ROOT ? COMPOSE_FILE : join(sourceRoot, 'compose.dev.yaml'),
    sourceRoot,
    fixtureDir,
    fixtureLoadTimeout,
    fixtureProject: project,
    fixtureGeneration: generation,
    host,
    apiPort,
    uiPort,
    populationMappingCursorSecret,
    apiUrl,
    uiUrl,
    artifacts,
  });
};

export const createVerificationReport = (target, scenario = 'dev-stack') => ({
  status: 'building',
  scenario,
  target: {
    composeProject: target.composeProject,
    apiUrl: target.apiUrl,
    uiUrl: target.uiUrl,
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    fixture: target.fixtureDir,
  },
  assertions: [],
  limitations: [],
  timings: {},
  evidencePaths: [],
});

const createVerificationTarget = (target, runID) => {
  const project = safeIdentifier(`loom_dev_verify_${runID}`, 'verification project');
  if (project === target.fixtureProject || project === CANONICAL_DATA_PROJECT) {
    throw new Error(`verification project collides with protected fixture: ${project}`);
  }
  return Object.freeze({ ...target, fixtureProject: project });
};

const writeJSON = (path, value) => {
  mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
  writeFileSync(path, `${JSON.stringify(value, null, 2)}\n`, { mode: 0o600 });
};

const recordAssertion = (report, name, expected, actual) => {
  const passed = JSON.stringify(expected) === JSON.stringify(actual);
  report.assertions.push({ name, status: passed ? 'passed' : 'failed', expected, actual });
  if (!passed) throw new Error(`${name} failed. expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
};

export const commandEnvironment = (target) => ({
  ...process.env,
  LOOM_DEV_COMPOSE_PROJECT: target.composeProject,
  LOOM_DEV_SOURCE_ROOT: target.sourceRoot,
  LOOM_DEV_PROJECT: target.fixtureProject,
  LOOM_DEV_GENERATION: target.fixtureGeneration,
  LOOM_DEV_HOST: target.host,
  LOOM_DEV_API_PORT: String(target.apiPort),
  LOOM_DEV_UI_PORT: String(target.uiPort),
  LOOM_DEV_EXPLORER: BOOTSTRAP_EXPLORER_NAME,
  LOOM_POPULATION_MAPPING_CURSOR_SECRET: target.populationMappingCursorSecret,
});

export const LOOM_DEV_BUILD_STAMP_SCRIPT = '/workspace/loom-dev-build-stamp.sh';
export const LOOM_DEV_BUILD_RECORD_PATH = '/workspace/tmp/arango-fhir-server.build.json';

export const parseBuildIdentity = ({ stampOutput, recordedOutput, gitHeadOutput }) => {
  const parts = (stampOutput || '').trim().split(/\s+/).map((part) => part || null);
  const [expectedSource, liveSource, runningBinary] = parts;
  let recorded = {};
  try { recorded = JSON.parse(recordedOutput); } catch { recorded = {}; }
  return {
    expectedSource: expectedSource ?? null,
    liveSource: liveSource ?? null,
    runningBinary: runningBinary ?? null,
    recordedBinary: recorded?.binary ?? null,
    recordedSource: recorded?.source ?? null,
    gitHead: (gitHeadOutput || '').trim() || null,
  };
};

// Resolve the API container by Compose ownership labels before checking its build.
export const resolveOwnedApiContainer = (target, { env = commandEnvironment(target), psRunner } = {}) => {
  const listed = (psRunner ?? ((args) => spawnSync('docker', args, { encoding: 'utf8', env, timeout: 15000 })))(
    ['ps', '--filter', `label=com.docker.compose.project=${target.composeProject}`,
      '--filter', 'label=com.docker.compose.service=loom-api', '--format', '{{.ID}}']);
  const ids = (listed.stdout || '').trim().split(/\s+/).filter((id) => /^[a-f0-9]{12,64}$/.test(id || ''));
  if (listed.status !== 0 || ids.length !== 1) return { container: null, error: ((listed.stderr || listed.stdout || '').trim().slice(0, 400)) || `expected exactly one loom-api container, got ${ids.length}` };
  return { container: ids[0], error: null };
};

export const collectBuildIdentity = (target, { repoRoot = REPO_ROOT, runner, psRunner, env = commandEnvironment(target), stampScript = LOOM_DEV_BUILD_STAMP_SCRIPT } = {}) => {
  const runCmd = runner ?? ((args) => spawnSync('docker', args, { encoding: 'utf8', env, timeout: 30000 }));
  const { container, error } = resolveOwnedApiContainer(target, { env, psRunner });
  if (!container) return { ...parseBuildIdentity({}), checkExit: 125, containerError: error };
  const stamp = runCmd(['exec', container, stampScript, '--check']);
  const recorded = runCmd(['exec', container, 'cat', LOOM_DEV_BUILD_RECORD_PATH]);
  const head = spawnSync('git', ['rev-parse', 'HEAD'], { cwd: repoRoot, encoding: 'utf8' });
  return { ...parseBuildIdentity({ stampOutput: stamp?.stdout, recordedOutput: recorded?.stdout, gitHeadOutput: head?.stdout }), checkExit: stamp?.status ?? 125 };
};

const run = (command, args, options = {}) => new Promise((resolvePromise, reject) => {
  const child = spawn(command, args, {
    cwd: options.cwd ?? REPO_ROOT,
    env: options.env ?? process.env,
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let stdout = '';
  let stderr = '';
  child.stdout.on('data', (chunk) => { stdout += chunk; });
  child.stderr.on('data', (chunk) => { stderr += chunk; });
  child.on('error', reject);
  child.on('close', (code, signal) => resolvePromise({ code: code ?? 1, signal, stdout, stderr }));
});

const compose = (target, args, options = {}) => run('docker', ['compose', '--project-name', target.composeProject, '--file', target.composeFile, ...args], { env: commandEnvironment(target), ...options });

const docker = (target, args, options = {}) => run('docker', args, { env: commandEnvironment(target), ...options });

const DEV_UI_ROUTING_MODES = new Set(['defaults', 'explicit-query']);

const assertDevUIRoutingConfiguration = (target, environment, routing = 'defaults') => {
  if (!DEV_UI_ROUTING_MODES.has(routing)) throw new Error(`unsupported development UI routing mode: ${routing}`);
  if (routing === 'explicit-query') return;
  if (environment.VITE_LOOM_PROJECT !== target.fixtureProject || environment.VITE_LOOM_EXPLORER !== BOOTSTRAP_EXPLORER_NAME) {
    throw new Error('development UI defaults do not target this session fixture and bootstrap Explorer');
  }
};

const inspectOwnedResources = async (target, {
  requirePorts = false,
  uiRouting = 'defaults',
  resourceCommands,
} = {}) => {
  if (!DEV_UI_ROUTING_MODES.has(uiRouting)) throw new Error(`unsupported development UI routing mode: ${uiRouting}`);
  const runCompose = (args) => resourceCommands?.compose ? resourceCommands.compose(args) : compose(target, args);
  const runDocker = (args) => resourceCommands?.docker ? resourceCommands.docker(args) : docker(target, args);
  const listed = await runCompose(['ps', '-aq']);
  if (listed.code !== 0) throw new Error(`cannot inspect Compose ownership: ${listed.stderr || listed.stdout}`);
  const ids = listed.stdout.split(/\s+/).filter(Boolean);
  const services = new Set();
  if (ids.length > 0) {
    const inspected = await runDocker(['inspect', ...ids]);
    if (inspected.code !== 0) throw new Error(`cannot inspect development containers: ${inspected.stderr || inspected.stdout}`);
    let containers;
    try { containers = JSON.parse(inspected.stdout); } catch { throw new Error('docker inspect returned invalid JSON'); }
    for (const container of containers) {
      const labels = container.Config?.Labels ?? {};
      if (labels['com.docker.compose.project'] !== target.composeProject) throw new Error(`refusing unowned Compose container ${container.Id}`);
      if (!OWNED_SERVICES.has(labels['com.docker.compose.service'])) throw new Error(`refusing unexpected service ${labels['com.docker.compose.service'] || '(missing)'}`);
      services.add(labels['com.docker.compose.service']);
      const expectedSource = labels['com.docker.compose.service'] === 'loom-api'
        ? resolve(target.sourceRoot, 'cmd')
        : labels['com.docker.compose.service'] === 'loom-ui'
          ? resolve(target.sourceRoot, 'ui/packages/loom-ui/src')
          : undefined;
      if (expectedSource) {
        const mount = container.Mounts?.find((candidate) => candidate.Destination === (labels['com.docker.compose.service'] === 'loom-api' ? '/workspace/cmd' : '/workspace/packages/loom-ui/src'));
        if (!mount || !sourceMountMatches(mount.Source, expectedSource)) throw new Error(`refusing ${labels['com.docker.compose.service']} mounted from an unexpected source`);
      }
      if (!requirePorts || !['loom-api', 'loom-ui'].includes(labels['com.docker.compose.service'])) continue;
      const port = container.NetworkSettings?.Ports?.['8080/tcp']?.[0];
      const wantedPort = labels['com.docker.compose.service'] === 'loom-api' ? target.apiPort : target.uiPort;
      const wantedHost = target.host === 'localhost' ? '127.0.0.1' : target.host;
      if (!port || Number(port.HostPort) !== wantedPort || !['127.0.0.1', '::1', 'localhost'].includes(port.HostIp) || (port.HostIp === 'localhost' ? wantedHost !== '127.0.0.1' : port.HostIp !== wantedHost)) {
        throw new Error(`refusing development service with unexpected loopback port mapping: ${labels['com.docker.compose.service']}`);
      }
      if (labels['com.docker.compose.service'] === 'loom-ui') {
        const environment = Object.fromEntries((container.Config?.Env ?? []).map((entry) => {
          const separator = entry.indexOf('=');
          return separator < 0 ? [entry, ''] : [entry.slice(0, separator), entry.slice(separator + 1)];
        }));
        assertDevUIRoutingConfiguration(target, environment, uiRouting);
      }
    }
  }
  if (requirePorts && (services.size !== OWNED_SERVICES.size || [...OWNED_SERVICES].some((service) => !services.has(service)))) {
    throw new Error(`development Compose is missing an owned service (found: ${[...services].sort().join(', ') || 'none'})`);
  }
  const volumesListed = await runDocker(['volume', 'ls', '-q', '--filter', `label=com.docker.compose.project=${target.composeProject}`]);
  if (volumesListed.code !== 0) throw new Error(`cannot inspect development volumes: ${volumesListed.stderr || volumesListed.stdout}`);
  const volumes = volumesListed.stdout.split(/\s+/).filter(Boolean);
  if (volumes.length > 0) {
    const volumeInspect = await runDocker(['volume', 'inspect', ...volumes]);
    if (volumeInspect.code !== 0) throw new Error(`cannot inspect development volumes: ${volumeInspect.stderr || volumeInspect.stdout}`);
    let volumeValues;
    try { volumeValues = JSON.parse(volumeInspect.stdout); } catch { throw new Error('docker volume inspect returned invalid JSON'); }
    for (const volume of volumeValues) {
      if (volume.Labels?.['com.docker.compose.project'] !== target.composeProject) throw new Error(`refusing unowned development volume ${volume.Name}`);
    }
  }
  return { ids, volumes };
};

export const assertOwnedDevSession = async (target, { uiRouting = 'defaults', resourceCommands } = {}) =>
  inspectOwnedResources(target, { requirePorts: true, uiRouting, resourceCommands });

const request = async (url, options = {}) => {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), options.timeout ?? 5000);
  try {
    return await fetch(url, { ...options, signal: controller.signal });
  } finally {
    clearTimeout(timeout);
  }
};

const requestJSON = async (url, options = {}) => {
  const response = await request(url, options);
  const text = await response.text();
  let value;
  try { value = text ? JSON.parse(text) : {}; } catch { value = { text }; }
  return { response, value };
};

const waitForHTTP = async (url, predicate = (response) => response.ok, timeout = 120000) => {
  const started = Date.now();
  let lastError = 'no response';
  while (Date.now() - started < timeout) {
    try {
      const response = await request(url, { timeout: 5000 });
      if (predicate(response)) return response;
      lastError = `HTTP ${response.status}`;
    } catch (error) {
      lastError = error instanceof Error ? error.message : String(error);
    }
    await sleep(500);
  }
  throw new Error(`timed out waiting for ${url}: ${lastError}`);
};

export const generationLoadDisposition = (value) => {
  const state = String(value?.state ?? '').trim().toUpperCase();
  // Modern immutable loads remain STAGED after graph/catalog finalization;
  // activation is recorded by the separate project-generation pointer.
  // READY is the legacy spelling retained by the dataset manifest codec.
  if (state === 'STAGED' || state === 'READY' || state === 'ACTIVE') return 'ready';
  if (state === 'FAILED' || state === 'ERROR') return 'failed';
  if (state === 'LOADING' || state === 'QUEUED') return 'loading';
  return 'unknown';
};

const waitForGenerationReady = async (url, timeout, initialError = '') => {
  const started = Date.now();
  let last = initialError || 'generation has not appeared';
  while (Date.now() - started < timeout) {
    try {
      const { response, value } = await requestJSON(url, { timeout: 30000 });
      if (response.ok) {
        const disposition = generationLoadDisposition(value);
        if (disposition === 'ready') return value;
        if (disposition === 'failed') throw new Error(`fixture generation failed: ${JSON.stringify(value).slice(0, 500)}`);
        last = `state ${value?.state ?? 'unknown'}`;
      } else if (response.status !== 404) {
        last = `HTTP ${response.status}`;
      }
    } catch (error) {
      if (String(error?.message ?? error).startsWith('fixture generation failed:')) throw error;
      last = error instanceof Error ? error.message : String(error);
    }
    await sleep(1000);
  }
  throw new Error(`timed out waiting for fixture generation: ${last}`);
};

// Air can leave the previous process answering /readyz during its debounce
// window. The build command records a source and binary digest only after a
// successful compile; the check also hashes the executable held by the running
// server, so edits, deletions, and a stale process cannot pass this barrier.
const waitForFreshBuild = async (target, timeout = 30000) => {
  const started = Date.now();
  let last = 'no build metadata';
  while (Date.now() - started < timeout) {
    const result = await compose(target, ['exec', '-T', 'loom-api', '/workspace/loom-dev-build-stamp.sh', '--check']);
    if (result.code === 0) return Date.now() - started;
    last = result.stderr || result.stdout || `exec exited ${result.code}`;
    await sleep(250);
  }
  throw new Error(`timed out waiting for Air to build current source (${last})`);
};

const assertFreshProject = async (target) => {
  const projectURL = `${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers`;
  const explorers = await requestJSON(projectURL, { timeout: 30000 });
  if (!explorers.response.ok && explorers.response.status !== 404) throw new Error(`verification project preflight returned HTTP ${explorers.response.status}`);
  if (explorers.response.ok && (!Array.isArray(explorers.value) || explorers.value.length > 0)) {
    throw new Error(`verification fixture project already exists: ${target.fixtureProject}`);
  }
  const generationURL = `${target.apiUrl}/api/v1/datasets/${encodeURIComponent(target.fixtureProject)}/generations/${encodeURIComponent(target.fixtureGeneration)}`;
  const generation = await request(generationURL, { timeout: 30000 });
  if (generation.ok) throw new Error(`verification fixture generation already exists: ${target.fixtureProject}/${target.fixtureGeneration}`);
  if (generation.status !== 404) throw new Error(`verification generation preflight returned HTTP ${generation.status}`);
};

const bootstrapAuthoringURL = (target, explorerId) => `${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorerId)}/authoring/v2`;

export const authoringCommandSemanticsVersion = (builder) =>
  builder?.workspace?.semanticsVersion ?? AUTHORING_SEMANTICS_VERSION;

export const bootstrapWorkspaceNeedsSeed = (state) => {
  if (!state || state.draftVersion === undefined) return false;
  if (state.lifecycleState === 'NEW' && state.workspace === null && state.draftVersion === 0) return true;
  const documents = state.workspace?.documents;
  return state.lifecycleState === 'READY'
    && state.draftVersion === 1
    && Array.isArray(documents)
    && documents.length === 1
    && documents[0]?.output?.title === BOOTSTRAP_TABLE_TITLE
    && (documents[0]?.columns?.length ?? 0) === 0;
};

export const bootstrapSeedPlan = (state) => {
  const catalog = state?.catalog;
  const root = catalog?.nodes?.find((node) => node.rowRootEligible && node.resourceType === 'Patient');
  if (!root?.nodeId) throw new Error('development fixture catalog has no eligible Patient root');
  const normalizedPath = (candidate) => String(candidate?.fieldPath ?? '').replace(/^root\./, '');
  const candidates = ['id', 'name[].family', 'gender'].map((path) => catalog.candidates?.find((candidate) => candidate.nodeId === root.nodeId && normalizedPath(candidate) === path));
  if (!candidates[0]?.candidateId) throw new Error('development fixture catalog has no Patient id candidate');
  return {
    createTable: !(state.workspace?.documents?.length === 1 && state.workspace.documents[0]?.output?.title === BOOTSTRAP_TABLE_TITLE),
    rootNodeId: root.nodeId,
    candidates: candidates.filter(Boolean),
  };
};

const applyBootstrapCommands = async (target, explorerId, state, commandId, commands) => {
  const { response, value } = await requestJSON(`${bootstrapAuthoringURL(target, explorerId)}/commands`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      commandId,
      semanticsVersion: authoringCommandSemanticsVersion(state),
      snapshotToken: state.catalog.snapshotToken,
      expectedDraftVersion: state.draftVersion,
      ...(state.draftDigest ? { expectedDraftDigest: state.draftDigest } : {}),
      commands,
    }),
    timeout: 30000,
  });
  if (!response.ok) throw new Error(`fixture bootstrap authoring command returned HTTP ${response.status}: ${JSON.stringify(value).slice(0, 500)}`);
  return value;
};

const seedBootstrapWorkspace = async (target, explorerId) => {
  const builder = await requestJSON(`${bootstrapAuthoringURL(target, explorerId)}/builder`, { timeout: 30000 });
  if (!builder.response.ok) throw new Error(`fixture bootstrap Builder returned HTTP ${builder.response.status}`);
  if (!bootstrapWorkspaceNeedsSeed(builder.value)) {
    return { seeded: false, draftVersion: builder.value.draftVersion, workspace: builder.value.workspace };
  }

  const plan = bootstrapSeedPlan(builder.value);
  let state = builder.value;
  let workspace = state.workspace;
  let outputID = workspace?.documents?.[0]?.output?.id;
  if (plan.createTable) {
    const created = await applyBootstrapCommands(
      target,
      explorerId,
      state,
      `loom-dev-bootstrap-${target.fixtureProject}-${BOOTSTRAP_SEED_VERSION}-table`,
      [{ type: 'CREATE_TABLE', title: BOOTSTRAP_TABLE_TITLE, rootNodeId: plan.rootNodeId }],
    );
    state = { ...state, ...created };
    workspace = created.workspace;
    outputID = created.results?.find((result) => result.type === 'TABLE_CREATED')?.outputId;
    if (!outputID) throw new Error('fixture bootstrap table command returned no output identity');
  }

  const existingColumns = new Set((workspace?.documents?.find((document) => document.output?.id === outputID)?.columns ?? []).map((column) => `${column.occurrenceId}:${column.source?.field?.path ?? ''}`));
  const commands = plan.candidates
    .filter((candidate) => !existingColumns.has(`base:${String(candidate.fieldPath ?? '').replace(/^root\./, '')}`))
    .map((candidate) => ({
      type: 'ADD_COLUMN',
      outputId: outputID,
      occurrenceId: 'base',
      candidateId: candidate.candidateId,
      projectionMode: candidate.defaultProjectionMode,
      initialPresentation: 'TABLE',
      title: candidate.label,
    }));
  if (commands.length > 0) {
    const completed = await applyBootstrapCommands(
      target,
      explorerId,
      state,
      `loom-dev-bootstrap-${target.fixtureProject}-${BOOTSTRAP_SEED_VERSION}-columns`,
      commands,
    );
    workspace = completed.workspace;
    state = { ...state, ...completed };
  }
  return { seeded: true, draftVersion: state.draftVersion, workspace };
};

const seedFixture = async (target, { requireFresh = false, populateBootstrap = true } = {}) => {
  if (requireFresh) await assertFreshProject(target);
  const statusURL = `${target.apiUrl}/api/v1/datasets/${encodeURIComponent(target.fixtureProject)}/generations/${encodeURIComponent(target.fixtureGeneration)}`;
  const status = await request(statusURL, { timeout: 5000 });
  if (requireFresh && status.ok) throw new Error(`verification fixture project already exists: ${target.fixtureProject}`);
  let reused = status.ok;
  if (!reused) {
    if (status.status !== 404) throw new Error(`fixture generation preflight returned HTTP ${status.status}`);
    const form = new FormData();
    const fixtureFiles = readdirSync(target.fixtureDir)
      .filter((name) => name.endsWith('.ndjson'))
      .sort()
      .map((name) => ({ name, contents: readFileSync(join(target.fixtureDir, name)) }));
    for (const { name, contents } of fixtureFiles) {
      form.append('file', new Blob([contents]), name);
    }
    form.append('defer_activation', 'false');
    let submitError = '';
    try {
      const response = await request(statusURL, { method: 'POST', body: form, timeout: target.fixtureLoadTimeout });
      const text = await response.text();
      if (!response.ok) throw new Error(`fixture seed returned HTTP ${response.status}: ${text.slice(0, 500)}`);
    } catch (error) {
      // Large multipart loads may outlive the HTTP connection while the
      // generation continues under its durable lifecycle record. Poll that
      // record before treating a transport disconnect as an ingestion failure.
      submitError = error instanceof Error ? error.message : String(error);
    }
    await waitForGenerationReady(statusURL, target.fixtureLoadTimeout, submitError);
  }
  const explorersURL = `${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers`;
  const list = await requestJSON(explorersURL, { timeout: 30000 });
  if (!list.response.ok || !Array.isArray(list.value)) throw new Error(`fixture Explorer list returned HTTP ${list.response.status}`);
  let bootstrap = list.value.find((explorer) => explorer.title === BOOTSTRAP_EXPLORER_TITLE);
  if (!bootstrap) {
    const created = await requestJSON(explorersURL, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: BOOTSTRAP_EXPLORER_NAME, title: BOOTSTRAP_EXPLORER_TITLE }), timeout: 30000 });
    if (created.response.status === 409) {
      const retry = await requestJSON(explorersURL, { timeout: 30000 });
      bootstrap = retry.value.find((explorer) => explorer.title === BOOTSTRAP_EXPLORER_TITLE);
    } else if (created.response.ok) {
      bootstrap = created.value;
    } else {
      throw new Error(`fixture bootstrap Explorer create returned HTTP ${created.response.status}`);
    }
  }
  if (!bootstrap?.explorerId) throw new Error('fixture bootstrap Explorer has no stable identity');
  const bootstrapWorkspace = populateBootstrap ? await seedBootstrapWorkspace(target, bootstrap.explorerId) : undefined;
  return { reused, fresh: requireFresh, bootstrapExplorerId: bootstrap.explorerId, bootstrapWorkspace };
};

export const createFreshVerificationFixture = async (session, runID, { populateBootstrap = false } = {}) => {
  if (!/^[a-z0-9][a-z0-9-]{0,24}$/.test(runID)) throw new Error('verification run id must be a short lowercase slug');
  const target = createVerificationTarget(session, runID);
  const seed = await seedFixture(target, { requireFresh: true, populateBootstrap });
  if (seed.reused || !seed.fresh) throw new Error(`verification fixture was unexpectedly reused: ${target.fixtureProject}`);
  return Object.freeze({ target, seed });
};

const ensureDev = async (target, report, rebuild = false) => {
  const started = Date.now();
  report.status = 'building';
  await inspectOwnedResources(target);
  if (rebuild) {
    const built = await compose(target, ['build']);
    if (built.code !== 0) throw new Error(`development image rebuild failed: ${built.stderr || built.stdout}`);
  }
  let up = await compose(target, ['up', '-d']);
  if (up.code !== 0) {
    const built = await compose(target, ['build']);
    if (built.code !== 0) throw new Error(`development image build failed: ${built.stderr || built.stdout}`);
    up = await compose(target, ['up', '-d']);
  }
  if (up.code !== 0) throw new Error(`development Compose start failed: ${up.stderr || up.stdout}`);
  await inspectOwnedResources(target, { requirePorts: true });
  await waitForHTTP(`${target.apiUrl}/readyz`);
  report.timings.api_build_barrier_ms = await waitForFreshBuild(target);
  const seed = await seedFixture(target);
  await waitForHTTP(target.uiUrl);
  report.status = 'ready';
  report.timings.startup_ms = Date.now() - started;
  report.target.fixtureSeed = seed.reused ? 'reused' : 'seeded';
  report.target.bootstrapExplorerId = seed.bootstrapExplorerId;
  report.target.bootstrapWorkspace = seed.bootstrapWorkspace?.seeded ? 'seeded' : 'reused';
  writeJSON(join(target.artifacts, 'dev-session.json'), { ...target, fixtureSeed: report.target.fixtureSeed, bootstrapExplorerId: seed.bootstrapExplorerId, bootstrapWorkspace: report.target.bootstrapWorkspace });
  return seed;
};

export const doctor = async (target) => {
  await inspectOwnedResources(target, { requirePorts: true });
  const buildBarrier = await waitForFreshBuild(target);
  const api = await request(`${target.apiUrl}/readyz`, { timeout: 5000 });
  const ui = await request(target.uiUrl, { timeout: 5000 });
  const generation = await request(`${target.apiUrl}/api/v1/datasets/${encodeURIComponent(target.fixtureProject)}/generations/${encodeURIComponent(target.fixtureGeneration)}`, { timeout: 5000 });
  const explorers = await requestJSON(`${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers`, { timeout: 30000 });
  const bootstrap = Array.isArray(explorers.value) ? explorers.value.find((explorer) => explorer.title === BOOTSTRAP_EXPLORER_TITLE) : undefined;
  const builder = bootstrap?.explorerId
    ? await requestJSON(`${bootstrapAuthoringURL(target, bootstrap.explorerId)}/builder`, { timeout: 30000 })
    : undefined;
  return { api: api.status, ui: ui.status, generation: generation.status, builder: builder?.response.status ?? 404, builderState: builder?.response.ok ? builder.value : undefined, bootstrapExplorerId: bootstrap?.explorerId, composeProject: target.composeProject, project: target.fixtureProject, generationName: target.fixtureGeneration, buildBarrier };
};

const cleanup = async (target, purge = false) => {
  await inspectOwnedResources(target);
  const args = ['down', '--remove-orphans'];
  if (purge) args.push('--volumes');
  const result = await compose(target, args);
  if (result.code !== 0) throw new Error(`development Compose cleanup failed: ${result.stderr || result.stdout}`);
};

const main = async (argv) => {
  const command = argv[0] ?? 'dev-doctor';
  if (['--help', '-h', 'help'].includes(command)) {
    console.log('Usage: node scripts/loom-dev.mjs dev | dev-rebuild | dev-doctor | dev-down [--purge]');
    return;
  }
  const target = createDevSession();
  const report = createVerificationReport(target);
  mkdirSync(target.artifacts, { recursive: true, mode: 0o700 });
  try {
    if (command === 'dev') {
      await ensureDev(target, report);
      writeJSON(join(target.artifacts, 'report.json'), report);
      console.log(`Loom development target is ready at ${target.uiUrl} with API ${target.apiUrl}`);
      return;
    }
    if (command === 'dev-rebuild') {
      await ensureDev(target, report, true);
      writeJSON(join(target.artifacts, 'report.json'), report);
      console.log(`Loom development images rebuilt and target is ready at ${target.uiUrl}`);
      return;
    }
    if (command === 'dev-doctor') {
      const result = await doctor(target);
      recordAssertion(report, 'isolated-compose-project', true, result.composeProject !== CANONICAL_PROJECT);
      recordAssertion(report, 'development-api-ready', 200, result.api);
      recordAssertion(report, 'development-ui-served', 200, result.ui);
      recordAssertion(report, 'fixture-generation-present', 200, result.generation);
      recordAssertion(report, 'fixture-bootstrap-builder-loads', 200, result.builder);
      recordAssertion(report, 'fixture-bootstrap-has-owned-identity', true, Boolean(result.bootstrapExplorerId));
      recordAssertion(report, 'fixture-bootstrap-has-editable-workspace', true, Boolean(result.builderState?.workspace?.documents?.length));
      recordAssertion(report, 'development-build-is-fresh', true, result.buildBarrier >= 0);
      report.timings.api_build_barrier_ms = result.buildBarrier;
      report.target.bootstrapExplorerId = result.bootstrapExplorerId;
      report.target.bootstrapWorkspace = result.builderState?.workspace?.documents?.length ? 'present' : 'missing';
      report.status = 'ready';
      writeJSON(join(target.artifacts, 'report.json'), report);
      console.log('DEV_DOCTOR_PASSED');
      return;
    }
    if (command === 'dev-down') {
      await cleanup(target, argv.includes('--purge'));
      report.status = 'ready';
      writeJSON(join(target.artifacts, 'report.json'), report);
      console.log(`Loom development target ${target.composeProject} stopped${argv.includes('--purge') ? ' and its volumes were removed' : ''}`);
      return;
    }
    throw new Error(`unknown command ${command}; use dev, dev-rebuild, dev-doctor, or dev-down [--purge]`);
  } catch (error) {
    report.status = 'failed';
    report.error = error instanceof Error ? error.message : String(error);
    try { writeJSON(join(target.artifacts, 'report.json'), report); } catch {}
    console.error(`LOOM_DEV_FAILED ${report.error}`);
    process.exitCode = 1;
  }
};

if (!process.env.NODE_TEST_CONTEXT && import.meta.url === pathToFileURL(process.argv[1] ?? '').href) await main(process.argv.slice(2));
