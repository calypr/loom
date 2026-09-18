#!/usr/bin/env node

import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { closeSync, existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, readdirSync, renameSync, rmSync, statSync, unlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, basename, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { dataframeOutputQuery } from '../ui/packages/loom-ui/src/dataframeOutputQuery.mjs';

const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(SCRIPT_DIR, '..');
const COMPOSE_FILE = join(REPO_ROOT, 'compose.dev.yaml');
const FIXTURE_DIR = join(REPO_ROOT, 'testdata/devloop-fixture');
const ARTIFACT_ROOT = join(REPO_ROOT, '.artifacts/loom-dev');
const VALID_NAME = /^[a-z][a-z0-9_-]{0,62}$/;
const VALID_IDENTIFIER = /^[A-Za-z][A-Za-z0-9_-]{0,62}$/;
const CANONICAL_PROJECT = 'loom-demo';
const CANONICAL_DATA_PROJECT = 'NCPI_ACCEPTANCE';
const LOOPBACK_HOSTS = new Set(['127.0.0.1', 'localhost', '::1']);
const OWNED_SERVICES = new Set(['arangodb', 'clickhouse', 'loom-api', 'loom-ui']);

const requiredFixtureFiles = ['Patient.ndjson', 'Observation.ndjson', 'recipe.json'];
const BOOTSTRAP_EXPLORER_NAME = 'loom-dev-bootstrap';
const BOOTSTRAP_EXPLORER_TITLE = 'Loom dev bootstrap';
const BOOTSTRAP_TABLE_TITLE = 'Patients';
const BOOTSTRAP_SEED_VERSION = 'v1';
const DEFAULT_PORT_REGISTRY = join(tmpdir(), 'loom-dev-port-registry.json');
const PORT_SLOT_COUNT = 8000;
const API_PORT_BASE = 8180;
const UI_PORT_BASE = 30000;
const PORT_LOCK_TIMEOUT_MS = 30000;

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
  const portSlot = allocatePortSlot(registryPath, sourceRoot, identity);
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
  const fixtureDir = join(sourceRoot, 'testdata/devloop-fixture');

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
  if (!existsSync(fixtureDir)) throw new Error(`missing development fixture: ${fixtureDir}`);
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

export const createVerificationReport = (target, scenario = 'builder-preview-publish-viewer-filter-export') => ({
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

const recordEvidence = (report, path) => {
  if (!report.evidencePaths.includes(path)) report.evidencePaths.push(path);
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

const inspectOwnedResources = async (target, { requirePorts = false } = {}) => {
  const listed = await compose(target, ['ps', '-aq']);
  if (listed.code !== 0) throw new Error(`cannot inspect Compose ownership: ${listed.stderr || listed.stdout}`);
  const ids = listed.stdout.split(/\s+/).filter(Boolean);
  const services = new Set();
  if (ids.length > 0) {
    const inspected = await docker(target, ['inspect', ...ids]);
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
        if (environment.VITE_LOOM_PROJECT !== target.fixtureProject || environment.VITE_LOOM_EXPLORER !== BOOTSTRAP_EXPLORER_NAME) {
          throw new Error('development UI defaults do not target this session fixture and bootstrap Explorer');
        }
      }
    }
  }
  if (requirePorts && (services.size !== OWNED_SERVICES.size || [...OWNED_SERVICES].some((service) => !services.has(service)))) {
    throw new Error(`development Compose is missing an owned service (found: ${[...services].sort().join(', ') || 'none'})`);
  }
  const volumesListed = await docker(target, ['volume', 'ls', '-q', '--filter', `label=com.docker.compose.project=${target.composeProject}`]);
  if (volumesListed.code !== 0) throw new Error(`cannot inspect development volumes: ${volumesListed.stderr || volumesListed.stdout}`);
  const volumes = volumesListed.stdout.split(/\s+/).filter(Boolean);
  if (volumes.length > 0) {
    const volumeInspect = await docker(target, ['volume', 'inspect', ...volumes]);
    if (volumeInspect.code !== 0) throw new Error(`cannot inspect development volumes: ${volumeInspect.stderr || volumeInspect.stdout}`);
    let volumeValues;
    try { volumeValues = JSON.parse(volumeInspect.stdout); } catch { throw new Error('docker volume inspect returned invalid JSON'); }
    for (const volume of volumeValues) {
      if (volume.Labels?.['com.docker.compose.project'] !== target.composeProject) throw new Error(`refusing unowned development volume ${volume.Name}`);
    }
  }
  return { ids, volumes };
};

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
      semanticsVersion: 4,
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
    for (const name of readdirSync(target.fixtureDir).filter((name) => name.endsWith('.ndjson')).sort()) {
      const path = join(target.fixtureDir, name);
      form.append('file', new Blob([readFileSync(path)]), name);
    }
    form.append('defer_activation', 'false');
    const response = await request(statusURL, { method: 'POST', body: form, timeout: 180000 });
    const text = await response.text();
    if (!response.ok) throw new Error(`fixture seed returned HTTP ${response.status}: ${text.slice(0, 500)}`);
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

const findChrome = () => {
  const candidates = [
    process.env.CHROME_BIN,
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
    'google-chrome',
    'chromium',
    'chromium-browser',
  ].filter(Boolean);
  for (const candidate of candidates) {
    if (candidate.startsWith('/') ? existsSync(candidate) : true) return candidate;
  }
  throw new Error('Chrome or Chromium is required. Set CHROME_BIN to its executable.');
};

const freePort = async () => new Promise((resolvePromise, reject) => {
  import('node:net').then(({ createServer }) => {
    const server = createServer();
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      const value = typeof address === 'object' && address ? address.port : 0;
      server.close(() => resolvePromise(value));
    });
  }, reject);
});

class CDPConnection {
  constructor(url) {
    this.url = url;
    this.nextID = 1;
    this.pending = new Map();
    this.listeners = new Map();
  }

  async connect() {
    this.socket = new WebSocket(this.url);
    await new Promise((resolvePromise, reject) => {
      this.socket.addEventListener('open', resolvePromise, { once: true });
      this.socket.addEventListener('error', reject, { once: true });
    });
    this.socket.addEventListener('message', (event) => {
      const message = JSON.parse(String(event.data));
      if (message.id) {
        const pending = this.pending.get(message.id);
        if (!pending) return;
        this.pending.delete(message.id);
        if (message.error) pending.reject(new Error(message.error.message));
        else pending.resolve(message.result);
        return;
      }
      for (const listener of this.listeners.get(message.method) ?? []) listener(message.params);
    });
    return this;
  }

  on(method, listener) {
    const listeners = this.listeners.get(method) ?? [];
    listeners.push(listener);
    this.listeners.set(method, listeners);
  }

  send(method, params = {}) {
    const id = this.nextID++;
    return new Promise((resolvePromise, reject) => {
      this.pending.set(id, { resolve: resolvePromise, reject });
      this.socket.send(JSON.stringify({ id, method, params }));
    });
  }

  close() {
    this.socket?.close();
  }
}

const evaluate = async (cdp, expression) => {
  const result = await cdp.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true, userGesture: true });
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text || 'browser evaluation failed');
  if (result.result?.subtype === 'error') throw new Error(result.result.description || 'browser evaluation failed');
  return result.result?.value;
};

const DOM_HELPERS = `
const norm = (value) => String(value ?? '').replace(/\\s+/g, ' ').trim();
const visible = (element) => {
  if (!element || element.hidden || element.getAttribute('aria-hidden') === 'true') return false;
  const style = getComputedStyle(element);
  return style.display !== 'none' && style.visibility !== 'hidden' && element.getClientRects().length > 0;
};
const buttonByName = (name) => [...document.querySelectorAll('button,[role="button"]')].find((element) => visible(element) && norm(element.getAttribute('aria-label') || element.textContent) === name);
const clickButton = (name) => {
  const element = buttonByName(name);
  if (!element) throw new Error('button not found: ' + name);
  if (element.disabled || element.getAttribute('aria-disabled') === 'true') throw new Error('button disabled: ' + name);
  element.scrollIntoView({ block: 'center' });
  element.click();
  return name;
};
const inputByLabel = (label) => {
  const escaped = label.replace(/\\\\/g, '\\\\\\\\').replace(/"/g, '\\\\"');
  return document.querySelector('[aria-label="' + escaped + '"]') || document.getElementById(label);
};
const setInput = (label, value) => {
  const element = inputByLabel(label);
  if (!element) throw new Error('input not found: ' + label);
  const descriptor = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(element), 'value');
  descriptor?.set?.call(element, value);
  element.dispatchEvent(new Event('input', { bubbles: true }));
  element.dispatchEvent(new Event('change', { bubbles: true }));
  return value;
};
const selectOption = (label, optionText) => {
  const select = document.querySelector('select[aria-label="' + label + '"]');
  const option = [...(select?.options || [])].find((candidate) => norm(candidate.textContent) === optionText);
  if (!select || !option) throw new Error('select option not found: ' + optionText);
  select.value = option.value;
  select.dispatchEvent(new Event('change', { bubbles: true }));
  return option.value;
};
const clickText = (selector, text) => {
  const element = [...document.querySelectorAll(selector)].find((candidate) => visible(candidate) && norm(candidate.textContent) === text);
  if (!element) throw new Error('text not found: ' + text);
  element.scrollIntoView({ block: 'center' });
  element.click();
  return text;
};
const clickContains = (selector, text) => {
  const element = [...document.querySelectorAll(selector)].find((candidate) => (selector === '.react-flow__node' || visible(candidate)) && norm(candidate.textContent).includes(text));
  if (!element) throw new Error('text not found: ' + text);
  element.scrollIntoView({ block: 'center' });
  element.click();
  return text;
};
const clickCandidate = (fieldPath, suffix) => {
  const prefix = suffix === 'to table' ? 'Add' : 'Add';
  const wanted = prefix + ' ' + fieldPath + ' ' + suffix;
  const input = [...document.querySelectorAll('input[type="checkbox"][aria-label]')].find((candidate) => candidate.getAttribute('aria-label') === wanted);
  if (!input) throw new Error('candidate not found by exact accessible label: ' + wanted);
  if (input.disabled) throw new Error('candidate checkbox disabled: ' + fieldPath);
  input.scrollIntoView({ block: 'center' });
  input.click();
  return input.getAttribute('aria-label');
};
const clickFacetValue = (value) => {
  const element = [...document.querySelectorAll('label')].find((candidate) => visible(candidate) && norm(candidate.textContent).startsWith(value));
  if (!element) throw new Error('facet value not found: ' + value);
  const input = element.querySelector('input[type="checkbox"]') || (element.htmlFor ? document.getElementById(element.htmlFor) : null);
  if (!input) throw new Error('facet value has no checkbox: ' + value);
  input.click();
  return value;
};
`;

const browserEval = (cdp, source) => evaluate(cdp, `(async () => { ${DOM_HELPERS} ${source} })()`);

const waitForBrowser = async (cdp, predicate, timeout = 30000) => {
  const started = Date.now();
  let lastError = 'condition was false';
  while (Date.now() - started < timeout) {
    try {
      if (await evaluate(cdp, `(async () => (${predicate}))()`)) return;
    } catch (error) {
      lastError = error instanceof Error ? error.message : String(error);
    }
    await sleep(200);
  }
  throw new Error(`timed out waiting for browser condition: ${lastError}`);
};

const snapshot = async (cdp, path) => {
  const html = await evaluate(cdp, 'document.documentElement.outerHTML');
  mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
  writeFileSync(path, String(html ?? ''), { mode: 0o600 });
};

const launchBrowser = async (downloadDir) => {
  const chrome = findChrome();
  const port = await freePort();
  const profile = mkdtempSync(join(tmpdir(), 'loom-dev-chrome-'));
  const child = spawn(chrome, [
    '--headless=new', '--no-sandbox', '--disable-gpu', '--disable-background-networking',
    '--disable-component-update', '--no-first-run', '--no-default-browser-check',
    '--remote-allow-origins=*', `--remote-debugging-port=${port}`, `--user-data-dir=${profile}`,
    `--download.default_directory=${downloadDir}`,
  ], { stdio: ['ignore', 'ignore', 'pipe'] });
  const started = Date.now();
  let browserError = '';
  child.stderr.on('data', (chunk) => { browserError += String(chunk); });
  let page;
  while (Date.now() - started < 15000) {
    try {
      const response = await request(`http://127.0.0.1:${port}/json/list`, { timeout: 1000 });
      const pages = await response.json();
      page = pages.find((candidate) => candidate.type === 'page');
      if (page) break;
    } catch {
      await sleep(100);
    }
  }
  if (!page) {
    child.kill('SIGTERM');
    rmSync(profile, { recursive: true, force: true });
    throw new Error(`Chrome did not expose a CDP page: ${browserError.slice(-500)}`);
  }
  const cdp = await new CDPConnection(page.webSocketDebuggerUrl).connect();
  await cdp.send('Runtime.enable');
  await cdp.send('Page.enable');
  await cdp.send('Network.enable');
  await cdp.send('Browser.setDownloadBehavior', { behavior: 'allow', downloadPath: downloadDir });
  const dialogHandler = () => { void cdp.send('Page.handleJavaScriptDialog', { accept: true }); };
  cdp.on('Page.javascriptDialogOpening', dialogHandler);
  const awaitExit = () => new Promise((resolvePromise) => {
    if (child.exitCode !== null || child.signalCode !== null) { resolvePromise(); return; }
    const timer = setTimeout(() => { child.kill('SIGKILL'); resolvePromise(); }, 3000);
    child.once('close', () => { clearTimeout(timer); resolvePromise(); });
  });
  return {
    cdp,
    child,
    profile,
    close: async () => {
      cdp.close();
      if (child.exitCode === null && child.signalCode === null) child.kill('SIGTERM');
      await awaitExit();
      rmSync(profile, { recursive: true, force: true });
    },
  };
};

const navigate = async (cdp, url) => {
  await cdp.send('Page.navigate', { url });
  await waitForBrowser(cdp, `document.readyState === 'complete'`, 30000);
};

const parseCSV = (text) => {
  const rows = [];
  let row = [];
  let cell = '';
  let quoted = false;
  for (let index = 0; index < text.length; index += 1) {
    const char = text[index];
    if (quoted && char === '"' && text[index + 1] === '"') { cell += '"'; index += 1; continue; }
    if (char === '"') { quoted = !quoted; continue; }
    if (!quoted && char === ',') { row.push(cell); cell = ''; continue; }
    if (!quoted && char === '\n') { row.push(cell.replace(/\r$/, '')); rows.push(row); row = []; cell = ''; continue; }
    cell += char;
  }
  if (cell || row.length) { row.push(cell); rows.push(row); }
  return rows;
};

const findDownloadedCSV = async (directory, timeout = 10000) => {
  const started = Date.now();
  while (Date.now() - started < timeout) {
    const files = readdirSync(directory).filter((file) => file.endsWith('.csv'));
    if (files.length) {
      const path = join(directory, files.sort().at(-1));
      if (!existsSync(`${path}.crdownload`)) return path;
    }
    await sleep(200);
  }
  throw new Error('viewer did not download a CSV file');
};

export const graphQLRowsRequest = (target, selector, columns, filters = []) => ({
  query: dataframeOutputQuery('VerifyRows'),
  variables: { input: { projectId: target.fixtureProject, selector, columns, filters, first: 25 } },
});

const graphQLRows = async (target, selector, columns, filters = []) => {
  const body = graphQLRowsRequest(target, selector, columns, filters);
  const { response, value } = await requestJSON(`${target.apiUrl}/graphql/graph`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body), timeout: 30000 });
  if (!response.ok || value.errors?.length) throw new Error(`dataframe proof failed: HTTP ${response.status} ${JSON.stringify(value.errors ?? value).slice(0, 500)}`);
  return value.data?.dataframeRows;
};

const fetchExplorerState = async (target, explorerId) => {
  const url = `${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorerId)}`;
  const { response, value } = await requestJSON(url, { timeout: 30000 });
  if (!response.ok) throw new Error(`published Explorer read failed: HTTP ${response.status}`);
  return value;
};

const fetchBuilderState = async (target, explorerId) => {
  const url = `${bootstrapAuthoringURL(target, explorerId)}/builder`;
  const { response, value } = await requestJSON(url, { timeout: 30000 });
  if (!response.ok) throw new Error(`Builder state read failed: HTTP ${response.status}`);
  return value;
};

const normalizeRows = (rows, columns) => (Array.isArray(rows) ? rows : []).map((row) => {
  if (Array.isArray(row)) return Object.fromEntries(columns.map((column, index) => [column, row[index] ?? null]));
  if (row && typeof row === 'object' && Array.isArray(row.values)) return Object.fromEntries(columns.map((column, index) => [column, row.values[index] ?? null]));
  if (row && typeof row === 'object') return Object.fromEntries(columns.map((column) => [column, row[column] ?? null]));
  return Object.fromEntries(columns.map((column) => [column, null]));
});

const emittedForPhysicalColumn = (state, outputID, physical) => (state.generated?.emittedColumns ?? []).find((column) => column.outputId === outputID && column.publicColumn === physical);

const findPhysicalColumn = (state, output, predicate) => output.columns
  .map((column) => ({ runtime: column, emitted: emittedForPhysicalColumn(state, output.outputId, column.column) }))
  .find(({ runtime, emitted }) => emitted && predicate(runtime, emitted));

const rowValue = (row, column) => row[column] ?? null;

const coordinateIndex = (emitted) => emitted?.coordinates?.at(-1)?.index ?? -1;

// Ingestion keys include the project and generation. Legacy FIRST orders by
// those storage keys, not FHIR IDs; these expectations do not use the compiler.
export const expectedFixtureRelatedValue = (project, generation) => {
  const key = (id) => createHash('sha256')
    .update(['vertex', project, generation, 'Observation', id, ''].join('\0')).digest('hex');
  return key('dev-observation-001') < key('dev-observation-003') ? 172.5 : 180;
};

const verifyBrowserScenario = async (target, report, full, entryTarget = target) => {
  const relatedValue = expectedFixtureRelatedValue(target.fixtureProject, target.fixtureGeneration);
  const maximumRelatedValue = 180;
  const scenarioStarted = Date.now();
  const runID = `${Date.now().toString(36)}-${Math.random().toString(16).slice(2, 10)}`;
  const evidenceDir = join(target.artifacts, runID);
  report.target.evidenceDirectory = evidenceDir;
  const downloadDir = join(evidenceDir, 'downloads');
  mkdirSync(downloadDir, { recursive: true, mode: 0o700 });
  recordEvidence(report, evidenceDir);
  const browser = await launchBrowser(downloadDir);
  const cdp = browser.cdp;
  const bootstrapExplorerId = report.target.bootstrapExplorerId;
  if (!bootstrapExplorerId) throw new Error('fixture bootstrap Explorer identity is missing');
  const browserURL = `${target.uiUrl}/?project=${encodeURIComponent(target.fixtureProject)}&explorer=${encodeURIComponent(bootstrapExplorerId)}&mode=builder`;
  let explorerId = '';
  try {
    await navigate(cdp, entryTarget.uiUrl);
    await waitForBrowser(cdp, `document.body.innerText.includes('Dataset graph') || document.body.innerText.includes('Create your first table')`, 60000);
    recordAssertion(
      report,
      'bare-development-entry-loads-owned-bootstrap',
      `${entryTarget.fixtureProject} / loom-dev-bootstrap`,
      await evaluate(cdp, `document.querySelector('.demo-controls span')?.textContent.trim() || ''`),
    );
    await snapshot(cdp, join(evidenceDir, 'bare-entry.html'));
    recordEvidence(report, join(evidenceDir, 'bare-entry.html'));

    await navigate(cdp, browserURL);
    await waitForBrowser(cdp, `document.body.innerText.includes('Dataset graph') || document.body.innerText.includes('Create your first table')`, 60000);
    await snapshot(cdp, join(evidenceDir, 'builder-initial.html'));
    recordEvidence(report, join(evidenceDir, 'builder-initial.html'));

    const verificationTitle = `Loom dev verification ${target.fixtureProject.slice(-16)}`;
    await browserEval(cdp, `clickText('summary', 'New explorer')`);
    await browserEval(cdp, `setInput('new-explorer-name', ${JSON.stringify(verificationTitle)})`);
    await browserEval(cdp, `clickButton('Create blank')`);
    await waitForBrowser(cdp, `document.querySelector('select[aria-label="Explorer"] option:checked')?.textContent.trim() === ${JSON.stringify(verificationTitle)} && document.body.innerText.includes('Create your first table')`);
    explorerId = await evaluate(cdp, `document.querySelector('select[aria-label="Explorer"]')?.value || ''`);
    recordAssertion(report, 'browser-selected-owned-verification-explorer', true, Boolean(explorerId && explorerId !== bootstrapExplorerId));
    report.target.explorerId = explorerId;

    await browserEval(cdp, `setInput('first-table-name', 'Patients with observations')`);
    await browserEval(cdp, `clickButton('Create table')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Dataset graph') && document.body.innerText.includes('Patient')`);
    await browserEval(cdp, `clickContains('.react-flow__node', 'Patient')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Current query') && document.body.innerText.includes('Patient columns')`);
    await browserEval(cdp, `clickCandidate('id', 'to table')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Display name for configured id"]'))`);
    await browserEval(cdp, `clickCandidate('name[].family', 'to table')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Display name for configured name[].family"]'))`);
    await browserEval(cdp, `clickCandidate('gender', 'as filter')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Display name for configured gender"]'))`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Display name for configured id"]') && document.querySelector('input[aria-label="Display name for configured name[].family"]') && document.querySelector('input[aria-label="Display name for configured gender"]'))`);
    const configuredFields = await evaluate(cdp, `([...document.querySelectorAll('input[aria-label^="Display name for configured "]')].map((input) => input.getAttribute('aria-label')).sort())`);
    recordAssertion(report, 'builder-configures-exact-root-fields', true, ['Display name for configured gender', 'Display name for configured id', 'Display name for configured name[].family'].every((label) => configuredFields.includes(label)));

    await browserEval(cdp, `clickContains('.react-flow__node', 'Observation')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Observation columns')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('[aria-label="Require Observation match"]'))`);
    await browserEval(cdp, `clickButton('Require Observation match');`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('[aria-label="Keep Observation match"]'))`);
    const requiredBuilder = await fetchBuilderState(target, explorerId);
    recordAssertion(report, 'builder-required-match-persists-route-intent', 'REQUIRED', requiredBuilder.workspace.documents[0].route.children[0].matchMode);
    await browserEval(cdp, `clickButton('Keep Observation match');`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('[aria-label="Require Observation match"]'))`);
    const optionalBuilder = await fetchBuilderState(target, explorerId);
    recordAssertion(report, 'builder-optional-match-restores-feature-only-route', 'OPTIONAL', optionalBuilder.workspace.documents[0].route.children[0].matchMode);
    const columnScroll = await browserEval(cdp, `
      const search = document.querySelector('input[aria-label="Search columns"]');
      const pane = search?.closest('aside')?.querySelector('.overflow-y-auto');
      if (!pane) throw new Error('column viewport not found');
      pane.scrollTop = Math.min(1100, pane.scrollHeight - pane.clientHeight);
      pane.dispatchEvent(new Event('scroll'));
      return { scrollTop: pane.scrollTop, scrollHeight: pane.scrollHeight, clientHeight: pane.clientHeight };
    `);
    await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Add component[].valueString to table"]'))`);
    recordAssertion(report, 'builder-virtualized-column-list-scrolls', true,
      columnScroll.scrollTop > 0 && columnScroll.scrollHeight > columnScroll.clientHeight);
    await browserEval(cdp, `clickButton('Count')`);
    await browserEval(cdp, `setInput('Search columns', 'Observation count')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Display name for configured Observation count"]'))`);
    await browserEval(cdp, `selectOption('Contributors for Observation count', 'component[].valueString')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('select[aria-label="Contributors for Observation count"]')?.value)`);
    let scopedBuilder;
    const scopedDeadline = Date.now() + 30000;
    while (Date.now() < scopedDeadline) {
      scopedBuilder = await fetchBuilderState(target, explorerId);
      const feature = scopedBuilder.workspace.documents[0].columns.find((column) => column.label === 'Observation count');
      if (feature?.contributor?.operator === 'EXISTS') break;
      await sleep(200);
    }
    const componentTextCandidate = scopedBuilder.catalog.candidates.find((candidate) => candidate.fieldPath === 'component[].valueString');
    const scopedCount = scopedBuilder.workspace.documents[0].columns.find((column) => column.label === 'Observation count');
    recordAssertion(report, 'builder-persists-typed-contributor-scope', {
      candidateId: componentTextCandidate?.candidateId,
      operator: 'EXISTS',
      quantifier: 'ANY',
    }, scopedCount?.contributor);
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Preview' && !button.disabled))`);
    await browserEval(cdp, `clickButton('Yes / no')`);
    let featureBuilder;
    const featureDeadline = Date.now() + 30000;
    while (Date.now() < featureDeadline) {
      featureBuilder = await fetchBuilderState(target, explorerId);
      if (featureBuilder.workspace.documents[0].columns.some((column) => column.label === 'Has Observation')) break;
      await sleep(200);
    }
    recordAssertion(report, 'builder-persists-independent-related-features', true,
      featureBuilder?.workspace.documents[0].columns.some((column) => column.label === 'Has Observation') === true);
    await browserEval(cdp, `setInput('Search columns', 'Has Observation')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('select[aria-label="Contributors for Has Observation"]'))`);
    await browserEval(cdp, `selectOption('Contributors for Has Observation', 'status')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('select[aria-label="Contributors for Has Observation"]')?.value)`);
    await browserEval(cdp, `selectOption('Contributor condition for Has Observation', 'Equals a value')`);
    await browserEval(cdp, `setInput('Contributor value for Has Observation', 'final')`);
    await browserEval(cdp, `clickButton('Apply condition')`);
    let equalityBuilder;
    const equalityDeadline = Date.now() + 30000;
    while (Date.now() < equalityDeadline) {
      equalityBuilder = await fetchBuilderState(target, explorerId);
      const feature = equalityBuilder.workspace.documents[0].columns.find((column) => column.label === 'Has Observation');
      if (feature?.contributor?.operator === 'EQUALS') break;
      await sleep(200);
    }
    const statusCandidate = equalityBuilder.catalog.candidates.find((candidate) => candidate.fieldPath === 'status');
    const equalityFeature = equalityBuilder.workspace.documents[0].columns.find((column) => column.label === 'Has Observation');
    recordAssertion(report, 'builder-persists-independent-equality-scope', {
      candidateId: statusCandidate?.candidateId,
      operator: 'EQUALS',
      value: { kind: 'STRING', string: 'final' },
    }, equalityFeature?.contributor);
    const valueCandidateDeadline = Date.now() + 30000;
    let valueCandidateVisible = false;
    while (Date.now() < valueCandidateDeadline && !valueCandidateVisible) {
      await browserEval(cdp, `setInput('Search columns', 'valueQuantity.value')`);
      await sleep(200);
      valueCandidateVisible = await evaluate(cdp, `Boolean(document.querySelector('input[aria-label="Add valueQuantity.value to table"]'))`);
    }
    if (!valueCandidateVisible) throw new Error('valueQuantity.value candidate did not remain visible after Builder reconciliation');
    await browserEval(cdp, `clickCandidate('valueQuantity.value', 'to table')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Display name for configured valueQuantity.value"]'))`);
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Preview' && !button.disabled))`);
    await browserEval(cdp, `clickButton('Preview')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Dataframe contract') && document.body.innerText.includes('Preview and configure') && document.body.innerText.includes('dev-patient-001')`, 60000);
    await snapshot(cdp, join(evidenceDir, 'builder-preview.html'));
    recordEvidence(report, join(evidenceDir, 'builder-preview.html'));
    const preview = await evaluate(cdp, `(() => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      const rows = [...(table?.querySelectorAll('[role="row"]') || [])];
      return {
        headers: [...(rows[0]?.querySelectorAll('[role="columnheader"]') || [])].map((cell) => cell.textContent.trim()),
        rows: rows.slice(1).map((row) => [...row.querySelectorAll('[role="cell"]')].map((cell) => cell.textContent.trim())).sort((left, right) => String(left[0]).localeCompare(String(right[0]))),
      };
    })()`);
    recordAssertion(report, 'preview-shows-exact-fixture-table', {
      headers: ['id', 'name[].family [0]', 'name[].family [1]', 'name__count', 'Observation count', 'Has Observation', 'valueQuantity.value'],
      rows: [
        ['dev-patient-001', 'Example', 'Example-Smith', '2', '1', 'true', String(relatedValue)],
        ['dev-patient-002', 'Builder', '—', '1', '0', 'true', '68'],
      ],
    }, preview);

    await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Allow first related value for valueQuantity.value"]'))`);
    recordAssertion(report, 'related-selection-starts-unacknowledged', false,
      await evaluate(cdp, `document.querySelector('input[aria-label="Allow first related value for valueQuantity.value"]').checked`));
    recordAssertion(report, 'contract-does-not-claim-ml-readiness', false,
      await evaluate(cdp, `document.body.innerText.includes('ML-ready: Yes')`));
    await browserEval(cdp, `clickButton('Publish')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('UNACKNOWLEDGED_RELATED_FIRST')`, 30000);
    const rejectedState = await fetchExplorerState(target, explorerId);
    recordAssertion(report, 'unacknowledged-related-selection-cannot-publish', false,
      Boolean(rejectedState.active?.revisionId || rejectedState.runtime?.outputs?.length));
    await browserEval(cdp, `selectOption('Across related Observation records for valueQuantity.value', 'Require zero or one value')`);
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Preview' && !button.disabled))`);
    await browserEval(cdp, `clickButton('Preview')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('RELATIONSHIP_CARDINALITY_VIOLATION')`, 60000);
    recordAssertion(report, 'require-one-rejects-ambiguous-related-values', true,
      String(await evaluate(cdp, 'document.body.innerText')).includes('RELATIONSHIP_CARDINALITY_VIOLATION'));
    const ambiguousState = await fetchExplorerState(target, explorerId);
    recordAssertion(report, 'ambiguous-require-one-does-not-publish', false,
      Boolean(ambiguousState.active?.revisionId || ambiguousState.runtime?.outputs?.length));
    await browserEval(cdp, `selectOption('Across related Observation records for valueQuantity.value', 'Count values')`);
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Preview' && !button.disabled))`);
    await browserEval(cdp, `clickButton('Preview')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Dataframe contract') && document.body.innerText.includes('dev-patient-001')`, 60000);
    const valueCounts = await evaluate(cdp, `(() => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      const rows = [...(table?.querySelectorAll('[role="row"]') || [])];
      const headers = [...(rows[0]?.querySelectorAll('[role="columnheader"]') || [])].map((cell) => cell.textContent.trim());
      const idIndex = headers.indexOf('id');
      const valueIndex = headers.indexOf('valueQuantity.value');
      return rows.slice(1).map((row) => {
        const cells = [...row.querySelectorAll('[role="cell"]')].map((cell) => cell.textContent.trim());
        return [cells[idIndex], cells[valueIndex]];
      }).sort((left, right) => left[0].localeCompare(right[0]));
    })()`);
    recordAssertion(report, 'value-count-counts-non-null-values-not-resources', [
      ['dev-patient-001', '2'],
      ['dev-patient-002', '1'],
    ], valueCounts);
    await browserEval(cdp, `selectOption('Across related Observation records for valueQuantity.value', 'Value nearest a date')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Date-aware value selection') && Boolean(document.querySelector('button') && [...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Apply date selection'))`);
    const beforeTemporalApply = await fetchBuilderState(target, explorerId);
    recordAssertion(report, 'date-aware-editor-does-not-persist-partial-policy', 'COUNT',
      beforeTemporalApply.workspace.documents[0].columns.find((column) => column.label === 'valueQuantity.value')?.source?.aggregate?.operation);
    await browserEval(cdp, `clickButton('Apply date selection')`);
    let temporalBuilder;
    const temporalDeadline = Date.now() + 30000;
    while (Date.now() < temporalDeadline) {
      temporalBuilder = await fetchBuilderState(target, explorerId);
      const valueFeature = temporalBuilder.workspace.documents[0].columns.find((column) => column.label === 'valueQuantity.value');
      if (valueFeature?.source?.aggregate?.operation === 'FIRST_ORDERED') break;
      await sleep(200);
    }
    const temporalFeature = temporalBuilder?.workspace.documents[0].columns.find((column) => column.label === 'valueQuantity.value');
    recordAssertion(report, 'builder-persists-complete-date-aware-policy', {
      operation: 'FIRST_ORDERED',
      path: 'valueQuantity.value',
      timestampPath: 'effectiveDateTime',
      anchorPath: 'meta.lastUpdated',
      direction: 'DESC',
      precision: 'INSTANT',
      tiePolicy: 'REQUIRE_UNIQUE',
    }, {
      operation: temporalFeature?.source?.aggregate?.operation,
      path: temporalFeature?.source?.aggregate?.path,
      timestampPath: temporalFeature?.source?.aggregate?.temporal?.timestampPath,
      anchorPath: temporalFeature?.source?.aggregate?.temporal?.anchorPath,
      direction: temporalFeature?.source?.aggregate?.temporal?.direction,
      precision: temporalFeature?.source?.aggregate?.temporal?.precision,
      tiePolicy: temporalFeature?.source?.aggregate?.temporal?.tiePolicy,
    });
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Preview' && !button.disabled))`);
    await browserEval(cdp, `clickButton('Preview')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('TEMPORAL_TIE_AMBIGUOUS')`, 60000);
    recordAssertion(report, 'date-aware-selection-rejects-equal-date-ambiguity', true,
      String(await evaluate(cdp, 'document.body.innerText')).includes('TEMPORAL_TIE_AMBIGUOUS'));
    await browserEval(cdp, `selectOption('Equal date handling', 'Choose deterministically by resource key')`);
    await browserEval(cdp, `clickButton('Apply date selection')`);
    const tiePolicyDeadline = Date.now() + 30000;
    while (Date.now() < tiePolicyDeadline) {
      temporalBuilder = await fetchBuilderState(target, explorerId);
      const valueFeature = temporalBuilder.workspace.documents[0].columns.find((column) => column.label === 'valueQuantity.value');
      if (valueFeature?.source?.aggregate?.temporal?.tiePolicy === 'RESOURCE_KEY') break;
      await sleep(200);
    }
    recordAssertion(report, 'builder-persists-explicit-equal-date-resolution', 'RESOURCE_KEY',
      temporalBuilder?.workspace.documents[0].columns.find((column) => column.label === 'valueQuantity.value')?.source?.aggregate?.temporal?.tiePolicy);
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Preview' && !button.disabled))`);
    await browserEval(cdp, `clickButton('Preview')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Dataframe contract') && document.body.innerText.includes('dev-patient-001')`, 60000);
    const temporalValues = await evaluate(cdp, `(() => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      const rows = [...(table?.querySelectorAll('[role="row"]') || [])];
      const headers = [...(rows[0]?.querySelectorAll('[role="columnheader"]') || [])].map((cell) => cell.textContent.trim());
      const idIndex = headers.indexOf('id');
      const valueIndex = headers.indexOf('valueQuantity.value');
      return rows.slice(1).map((row) => {
        const cells = [...row.querySelectorAll('[role="cell"]')].map((cell) => cell.textContent.trim());
        return [cells[idIndex], cells[valueIndex]];
      }).sort((left, right) => left[0].localeCompare(right[0]));
    })()`);
    recordAssertion(report, 'date-aware-selection-resolves-equal-dates-deterministically', [
      ['dev-patient-001', String(relatedValue)],
      ['dev-patient-002', '68'],
    ], temporalValues);
    await browserEval(cdp, `selectOption('Across related Observation records for valueQuantity.value', 'Maximum value')`);
    let reducedBuilder;
    const reductionDeadline = Date.now() + 30000;
    while (Date.now() < reductionDeadline) {
      reducedBuilder = await fetchBuilderState(target, explorerId);
      const valueFeature = reducedBuilder.workspace.documents[0].columns.find((column) => column.label === 'valueQuantity.value');
      if (valueFeature?.source?.aggregate?.operation === 'MAX') break;
      await sleep(200);
    }
    const reducedValueFeature = reducedBuilder?.workspace.documents[0].columns.find((column) => column.label === 'valueQuantity.value');
    recordAssertion(report, 'builder-replaces-unsafe-related-first-with-explicit-maximum', {
      kind: 'aggregate',
      aggregate: { operation: 'MAX', path: 'valueQuantity.value' },
    }, reducedValueFeature?.source);
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Preview' && !button.disabled))`);
    await browserEval(cdp, `clickButton('Preview')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Dataframe contract') && document.body.innerText.includes('dev-patient-001') && Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Publish' && !button.disabled))`, 60000);
    await browserEval(cdp, `clickButton('Publish')`);
    const runtimeStarted = Date.now();
    let state;
    let runtime;
    while (Date.now() - runtimeStarted < 60000) {
      try {
        state = await fetchExplorerState(target, explorerId);
        runtime = state.runtime ?? state;
        if (runtime?.outputs?.length) break;
      } catch { /* The publish request may still be materializing. */ }
      await sleep(500);
    }
    if (!runtime?.outputs?.length) throw new Error('published Explorer runtime did not become readable');
    recordAssertion(report, 'publish-created-a-fresh-revision-or-materialization', true, Boolean(state.active?.revisionId || state.generated?.materializations?.[0]?.id));
    report.timings.publish_to_runtime_ms = Date.now() - runtimeStarted;
    const output = runtime.outputs[0];
    const physicalColumns = output.columns.map((column) => column.column);
    const outputLineage = output.columns.map((runtimeColumn) => {
      const emitted = emittedForPhysicalColumn(state, output.outputId, runtimeColumn.column);
      return {
        column: runtimeColumn.column,
        label: runtimeColumn.label,
        sourcePath: emitted?.sourcePath ?? null,
        sourceResourceType: emitted?.sourceResourceType ?? null,
        projectionMode: emitted?.projectionMode ?? null,
        coordinates: emitted?.coordinates?.map((coordinate) => coordinate.index) ?? [],
      };
    });
    recordAssertion(report, 'published-output-columns-have-generated-lineage', true,
      outputLineage.length === physicalColumns.length && outputLineage.every((column) => column.sourcePath && column.column));
    recordAssertion(report, 'published-output-has-exact-supported-shape', [
      { label: 'id', sourcePath: 'id', sourceResourceType: 'Patient', projectionMode: 'VALUE', coordinates: [] },
      { label: 'name[].family [0]', sourcePath: 'name[].family', sourceResourceType: 'Patient', projectionMode: 'INDEXED', coordinates: [0] },
      { label: 'name[].family [1]', sourcePath: 'name[].family', sourceResourceType: 'Patient', projectionMode: 'INDEXED', coordinates: [1] },
      { label: 'Observation count', sourcePath: '$resource', sourceResourceType: 'Observation', projectionMode: 'COUNT', coordinates: [] },
      { label: 'name__count', sourcePath: 'name[]', sourceResourceType: 'Patient', projectionMode: 'COUNT', coordinates: [] },
      { label: 'Has Observation', sourcePath: '$resource', sourceResourceType: 'Observation', projectionMode: 'EXISTS', coordinates: [] },
      { label: 'valueQuantity.value', sourcePath: 'valueQuantity.value', sourceResourceType: 'Observation', projectionMode: 'MAX', coordinates: [] },
      { label: 'gender', sourcePath: 'gender', sourceResourceType: 'Patient', projectionMode: 'VALUE', coordinates: [] },
    ], outputLineage.map(({ label, sourcePath, sourceResourceType, projectionMode, coordinates }) => ({ label, sourcePath, sourceResourceType, projectionMode, coordinates })));
    const idColumn = findPhysicalColumn(state, output, (runtimeColumn, emitted) => emitted.sourcePath === 'id' || emitted.authoredColumns?.includes('id') || /patient\s*id/i.test(runtimeColumn.label));
    const familyColumns = output.columns
      .map((runtimeColumn) => ({ runtime: runtimeColumn, emitted: emittedForPhysicalColumn(state, output.outputId, runtimeColumn.column) }))
      .filter(({ emitted }) => emitted && (emitted.sourcePath?.includes('family') || emitted.authoredColumns?.some((path) => path.includes('family')) || /family/i.test(emitted.label)))
      .sort((left, right) => coordinateIndex(left.emitted) - coordinateIndex(right.emitted));
    const nameCountColumn = findPhysicalColumn(state, output, (runtimeColumn, emitted) => emitted.projectionMode === 'COUNT' && emitted.sourcePath === 'name[]');
    const genderColumn = findPhysicalColumn(state, output, (runtimeColumn, emitted) => emitted.sourcePath === 'gender' || emitted.authoredColumns?.includes('gender') || /gender/i.test(runtimeColumn.label));
    const valueColumn = findPhysicalColumn(state, output, (runtimeColumn, emitted) => emitted.sourcePath?.includes('value') || emitted.authoredColumns?.some((path) => path.includes('value')) || /value/i.test(emitted.label || runtimeColumn.label));
    const scopedObservationCountColumn = findPhysicalColumn(state, output, (runtimeColumn, emitted) => runtimeColumn.label === 'Observation count' && emitted.sourceResourceType === 'Observation' && emitted.sourcePath === '$resource' && emitted.projectionMode === 'COUNT');
    const hasObservationColumn = findPhysicalColumn(state, output, (runtimeColumn, emitted) => runtimeColumn.label === 'Has Observation' && emitted.sourceResourceType === 'Observation' && emitted.sourcePath === '$resource' && emitted.projectionMode === 'EXISTS');
    recordAssertion(report, 'published-output-has-id-lineage', true, Boolean(idColumn));
    recordAssertion(report, 'published-output-has-repeated-family-lineage', true, familyColumns.length >= 2 && familyColumns.every(({ emitted }) => emitted.coordinates?.length > 0));
    recordAssertion(report, 'published-output-has-related-value-lineage', true, Boolean(valueColumn));
    recordAssertion(report, 'indexed-family-and-count-preserve-selected-values', true,
      [...familyColumns, nameCountColumn].every(({ emitted }) => emitted.lossless === true && !emitted.lossReasons?.length));
    recordAssertion(report, 'related-value-publishes-explicit-reduction-contract', {
      lossless: false, structure: 'scalar', reasons: ['AGGREGATE_REDUCTION'],
    }, {
      lossless: valueColumn.emitted.lossless === true,
      structure: valueColumn.emitted.structuralSuitability,
      reasons: valueColumn.emitted.lossReasons,
    });
    const result = await graphQLRows(target, output.selector, physicalColumns);
    report.target.materialization = result.materialization;
    recordAssertion(report, 'new-materialization-is-readable', true, Boolean(result.materialization?.id));
    recordAssertion(report, 'materialization-targets-fixture-generation', target.fixtureGeneration, result.materialization?.datasetGeneration);
    recordAssertion(report, 'materialization-has-two-rows', 2, result.rows.length);
    recordAssertion(report, 'materialization-reports-two-total-rows', 2, result.totalCount);
    const rows = normalizeRows(result.rows, result.columns);
    const orderedRows = [...rows].sort((left, right) => String(rowValue(left, idColumn.runtime.column)).localeCompare(String(rowValue(right, idColumn.runtime.column))));
    const exactRows = orderedRows.map((row) => ({
      id: rowValue(row, idColumn.runtime.column),
      family: familyColumns.map(({ runtime: column }) => rowValue(row, column.column)),
      nameCount: rowValue(row, nameCountColumn.runtime.column),
      gender: rowValue(row, genderColumn.runtime.column),
      relatedValue: rowValue(row, valueColumn.runtime.column),
      scopedObservationCount: rowValue(row, scopedObservationCountColumn.runtime.column),
      hasObservation: rowValue(row, hasObservationColumn.runtime.column),
    }));
    recordAssertion(report, 'materialized-exact-fixture-rows', [
      { id: 'dev-patient-001', family: ['Example', 'Example-Smith'], nameCount: '2', gender: 'female', relatedValue: maximumRelatedValue, scopedObservationCount: '1', hasObservation: true },
      { id: 'dev-patient-002', family: ['Builder', null], nameCount: '1', gender: null, relatedValue: 68, scopedObservationCount: '0', hasObservation: true },
    ], exactRows);

    await browserEval(cdp, `clickButton('Viewer')`);
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Viewer' && button.classList.contains('active')))`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Published') && document.body.innerText.includes('dev-patient-001')`, 60000);
    await snapshot(cdp, join(evidenceDir, 'viewer-before-filter.html'));
    recordEvidence(report, join(evidenceDir, 'viewer-before-filter.html'));
    await browserEval(cdp, `clickButton('Load values')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('female')`);
    await browserEval(cdp, `clickFacetValue('female')`);
    await waitForBrowser(cdp, `document.querySelector('table[aria-label$=" results"] tbody')?.querySelectorAll('tr').length === 1`, 30000);
    const filteredViewer = await evaluate(cdp, `(() => {
      const table = document.querySelector('table[aria-label$=" results"]');
      return {
        headers: [...(table?.querySelectorAll('thead th') || [])].map((cell) => cell.textContent.trim()),
        rows: [...(table?.querySelectorAll('tbody tr') || [])].map((row) => [...row.querySelectorAll('td')].map((cell) => cell.textContent.trim())),
      };
    })()`);
    recordAssertion(report, 'viewer-filter-shows-exact-table', {
      headers: ['id', 'name[].family [0]', 'name[].family [1]', 'Observation count', 'name__count', 'Has Observation', 'valueQuantity.value'],
      rows: [['dev-patient-001', 'Example', 'Example-Smith', '1', '2', 'true', String(maximumRelatedValue)]],
    }, filteredViewer);
    recordAssertion(report, 'viewer-mode-is-persisted-in-url', 'viewer', await evaluate(cdp, 'new URL(window.location.href).searchParams.get("mode")'));

    await browserEval(cdp, `clickButton('Download CSV')`);
    const csvPath = await findDownloadedCSV(downloadDir);
    recordEvidence(report, csvPath);
    const csvRows = parseCSV(readFileSync(csvPath, 'utf8'));
    const expectedCSV = {
      headers: output.columns.filter((column) => column.visible).map((column) => column.column),
      rows: [['dev-patient-001', 'Example', 'Example-Smith', '1', '2', 'true', String(maximumRelatedValue)]],
    };
    recordAssertion(report, 'downloaded-csv-has-generated-physical-headers', expectedCSV.headers, csvRows[0] ?? []);
    recordAssertion(report, 'downloaded-csv-has-exact-filtered-rows', expectedCSV.rows, csvRows.slice(1));

    await cdp.send('Page.reload', { ignoreCache: false });
    await waitForBrowser(cdp, `document.readyState === 'complete'`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Published') && document.body.innerText.includes('dev-patient-001')`, 60000);
    recordAssertion(report, 'published-data-survives-reload', true, String(await evaluate(cdp, 'document.body.innerText')).includes('dev-patient-001'));
    await snapshot(cdp, join(evidenceDir, 'viewer-after-reload.html'));
    recordEvidence(report, join(evidenceDir, 'viewer-after-reload.html'));

    if (full) {
      report.timings.browser_scenario_ms = Date.now() - scenarioStarted;
      await measureHotReload(target, report, cdp);
    } else {
      report.timings.browser_scenario_ms = Date.now() - scenarioStarted;
    }

  } finally {
    if (explorerId) report.target.explorerRetention = 'retained with fresh verification project; remove only with owned dev-volume purge';
    try { await snapshot(cdp, join(evidenceDir, 'final.html')); recordEvidence(report, join(evidenceDir, 'final.html')); } catch { /* Preserve the primary failure. */ }
    await browser.close();
  }
};

const measureHotReload = async (target, report, cdp) => {
  // This file is the package source imported through the development-only
  // @calypr/loom-ui alias. The wrapper stylesheet is intentionally not the
  // probe: a wrapper-only HMR check could pass while the package alias is stale.
  const probePath = join(target.sourceRoot, 'ui/packages/loom-ui/src/styles.css');
  const original = readFileSync(probePath);
  const marker = `\n/* loom-dev-hotreload-probe */\n.loom-ui-root { --loom-dev-hotreload-probe: ${target.fixtureGeneration}; }\n`;
  const edited = Buffer.concat([original, Buffer.from(marker)]);
  const started = Date.now();
  try {
    writeFileSync(probePath, edited);
    await waitForBrowser(cdp, `getComputedStyle(document.querySelector('.loom-ui-root')).getPropertyValue('--loom-dev-hotreload-probe').trim() === ${JSON.stringify(target.fixtureGeneration)}`, 15000);
    report.timings.vite_hotreload_ms = Date.now() - started;
  } finally {
    const current = readFileSync(probePath);
    if (!current.equals(edited)) throw new Error(`refusing to overwrite a concurrent edit in ${probePath}`);
    writeFileSync(probePath, original);
  }
  await waitForBrowser(cdp, `getComputedStyle(document.querySelector('.loom-ui-root')).getPropertyValue('--loom-dev-hotreload-probe').trim() === ''`, 15000);
  recordAssertion(report, 'vite-restores-aliased-package-css', '', await evaluate(cdp, "getComputedStyle(document.querySelector('.loom-ui-root')).getPropertyValue('--loom-dev-hotreload-probe').trim()"));

  const probeID = target.fixtureProject.replace(/[^A-Za-z0-9_]/g, '_');
  const successName = `devloop_hotreload_success_${probeID}.go`;
  const successProbe = join(target.sourceRoot, 'cmd/arango-fhir-server', successName);
  if (existsSync(successProbe)) throw new Error(`refusing to overwrite existing probe: ${successProbe}`);
  const successMarker = `LOOM_DEV_HOTRELOAD_SUCCESS_${probeID}`;
  const successSource = `package main\n// loom-dev successful compiled probe ${successName}\n\nfunc init() { println(${JSON.stringify(successMarker)}) }\n`;
  const successStarted = Date.now();
  let successLogs = '';
  try {
    writeFileSync(successProbe, successSource);
    const since = new Date(successStarted - 1000).toISOString();
    const markerStarted = Date.now();
    while (Date.now() - markerStarted < 30000) {
      const logResult = await compose(target, ['logs', '--no-color', '--since', since, 'loom-api']);
      successLogs = `${logResult.stdout}\n${logResult.stderr}`;
      if (successLogs.includes(successMarker)) {
        await waitForFreshBuild(target);
        break;
      }
      await sleep(250);
    }
    if (!successLogs.includes(successMarker)) throw new Error(`successful backend probe did not execute: ${successMarker}`);
    recordAssertion(report, 'successful-backend-source-edit-executes', true, successLogs.includes(successMarker));
    recordAssertion(report, 'successful-backend-source-edit-is-fresh', true, (await compose(target, ['exec', '-T', 'loom-api', '/workspace/loom-dev-build-stamp.sh', '--check'])).code === 0);
    report.timings.backend_hotreload_ms = Date.now() - successStarted;
  } finally {
    const current = readFileSync(successProbe);
    if (!current.equals(Buffer.from(successSource))) throw new Error(`refusing to remove a concurrent edit in ${successProbe}`);
    rmSync(successProbe);
  }
  await waitForHTTP(`${target.apiUrl}/readyz`);
  await waitForFreshBuild(target);
  const failureName = `devloop_hotreload_failure_${probeID}.go`;
  const failureProbe = join(target.sourceRoot, 'cmd/arango-fhir-server', failureName);
  if (existsSync(failureProbe)) throw new Error(`refusing to overwrite existing probe: ${failureProbe}`);
  const failureStarted = Date.now();
  let logs = '';
  const probeSource = `package main\n// loom-dev syntax-error probe ${failureName}\n\nfunc devloopProbe( {\n`;
  try {
    writeFileSync(failureProbe, probeSource);
    const stoppedStarted = Date.now();
    let stopped = false;
    let stopReason = '';
    while (Date.now() - stoppedStarted < 30000) {
      try {
        const response = await request(`${target.apiUrl}/readyz`, { timeout: 2000 });
        if (!response.ok) { stopped = true; stopReason = `HTTP ${response.status}`; break; }
      } catch (error) {
        stopped = true;
        stopReason = error instanceof Error ? error.message : String(error);
        break;
      }
      await sleep(250);
    }
    if (!stopped) throw new Error('failed build left the previous API serving readyz');
    const failedDoctor = await compose(target, ['exec', '-T', 'loom-api', '/workspace/loom-dev-build-stamp.sh', '--check']);
    recordAssertion(report, 'failed-build-fails-freshness-doctor', true, failedDoctor.code !== 0);
    let compilerRejectedProbe = false;
    while (Date.now() - failureStarted < 30000) {
      const logResult = await compose(target, ['logs', '--no-color', '--since', new Date(failureStarted - 1000).toISOString(), 'loom-api']);
      logs = `${logResult.stdout}\n${logResult.stderr}`;
      compilerRejectedProbe = logs.split('\n').some((line) => line.includes(`${failureName}:`) && line.includes('syntax error'));
      if (compilerRejectedProbe) break;
      await sleep(250);
    }
    recordAssertion(report, 'compiler-rejects-current-syntax-error-probe', true, compilerRejectedProbe);
    recordAssertion(report, 'failed-build-stops-stale-api', true, stopped);
    recordAssertion(report, 'failed-build-names-current-probe', true, logs.includes(failureName));
    report.target.failedBuildStopReason = stopReason;
  } finally {
    const current = readFileSync(failureProbe);
    if (!current.equals(Buffer.from(probeSource))) throw new Error(`refusing to remove a concurrent edit in ${failureProbe}`);
    rmSync(failureProbe);
  }
  await waitForHTTP(`${target.apiUrl}/readyz`, (response) => response.ok, 30000);
  await waitForFreshBuild(target);
  report.timings.failed_build_recovery_ms = Date.now() - failureStarted;
  const successLogPath = join(target.artifacts, `${Date.now().toString(36)}-successful-build.log`);
  writeFileSync(successLogPath, successLogs, { mode: 0o600 });
  recordEvidence(report, successLogPath);
  const logPath = join(target.artifacts, `${Date.now().toString(36)}-failed-build.log`);
  writeFileSync(logPath, logs, { mode: 0o600 });
  recordEvidence(report, logPath);
};

const doctor = async (target) => {
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
  const commandStarted = Date.now();
  const command = argv[0] ?? 'dev-doctor';
  const target = createDevSession();
  const report = createVerificationReport(target);
  let activeReport = report;
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
    if (command === 'verify-fast' || command === 'verify-full') {
      await ensureDev(target, report);
      const verificationTarget = createVerificationTarget(target, `${Date.now().toString(36)}-${Math.random().toString(16).slice(2, 10)}`);
      const verificationReport = createVerificationReport(verificationTarget);
      activeReport = verificationReport;
      verificationReport.timings.startup_ms = report.timings.startup_ms;
      verificationReport.timings.api_build_barrier_ms = report.timings.api_build_barrier_ms;
      const seed = await seedFixture(verificationTarget, { requireFresh: true, populateBootstrap: false });
      if (seed.reused || !seed.fresh) throw new Error(`verification fixture was unexpectedly reused: ${verificationTarget.fixtureProject}`);
      verificationReport.target.fixtureSeed = 'seeded';
      verificationReport.target.bootstrapExplorerId = seed.bootstrapExplorerId;
      recordAssertion(verificationReport, 'verification-started-with-no-explorers-or-fixture-generation', true, seed.fresh && !seed.reused);
      await verifyBrowserScenario(verificationTarget, verificationReport, command === 'verify-full', target);
      verificationReport.status = 'passed';
      verificationReport.timings.total_ms = Date.now() - commandStarted;
      writeJSON(join(verificationReport.target.evidenceDirectory, 'report.json'), verificationReport);
      writeJSON(join(verificationTarget.artifacts, 'report.json'), verificationReport);
      console.log(`DEV_VERIFY_PASSED status=${verificationReport.status} project=${verificationTarget.fixtureProject} evidence=${verificationTarget.artifacts}`);
      return;
    }
    if (command === 'dev-down') {
      await cleanup(target, argv.includes('--purge'));
      report.status = 'ready';
      writeJSON(join(target.artifacts, 'report.json'), report);
      console.log(`Loom development target ${target.composeProject} stopped${argv.includes('--purge') ? ' and its volumes were removed' : ''}`);
      return;
    }
    throw new Error(`unknown command ${command}; use dev, dev-doctor, verify-fast, verify-full, dev-rebuild, or dev-down [--purge]`);
  } catch (error) {
    activeReport.status = 'failed';
    activeReport.error = error instanceof Error ? error.message : String(error);
    if (activeReport.target.evidenceDirectory) writeJSON(join(activeReport.target.evidenceDirectory, 'report.json'), activeReport);
    try { writeJSON(join(activeReport.target.artifacts ?? target.artifacts, 'report.json'), activeReport); } catch { /* Keep the original command error. */ }
    console.error(`DEV_VERIFY_FAILED ${activeReport.error}`);
    process.exitCode = 1;
  }
};

export { browserEval, launchBrowser, navigate, snapshot, waitForBrowser };

if (!process.env.NODE_TEST_CONTEXT && import.meta.url === pathToFileURL(process.argv[1] ?? '').href) await main(process.argv.slice(2));
