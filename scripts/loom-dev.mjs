#!/usr/bin/env node

import { spawn } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { closeSync, createReadStream, existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, readdirSync, renameSync, rmSync, statSync, unlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, basename, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createInterface } from 'node:readline';
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
export const AUTHORING_SEMANTICS_VERSION = 8;

export const canonicalProjectID = (raw) => {
  const value = String(raw ?? '').trim().replace(/^\/+|\/+$/g, '');
  if (!value || value.includes('/')) return value;
  const separator = value.indexOf('-');
  if (separator <= 0 || separator === value.length - 1) return value;
  const program = value.slice(0, separator);
  return program !== program.toLowerCase() || program.includes('_')
    ? `${program}/${value.slice(separator + 1)}`
    : value;
};

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
  limitations: [],
  timings: {},
  evidencePaths: [],
});

const routePathForEdgeIDs = (route, edgeIDs) => {
  const visit = (node, path) => {
    const next = node.catalogEdgeId ? [...path, node] : path;
    if (next.length === edgeIDs.length && next.every((step, index) => step.catalogEdgeId === edgeIDs[index])) return next;
    if (next.length >= edgeIDs.length) return undefined;
    for (const child of node.children ?? []) {
      const found = visit(child, next);
      if (found) return found;
    }
    return undefined;
  };
  return route ? visit(route, []) : undefined;
};

export const draftFingerprint = (state) => JSON.stringify({
  draftVersion: state.draftVersion,
  draftDigest: state.draftDigest,
  workspace: state.workspace,
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

const recordLimitation = (report, name, detail) => {
  report.limitations.push({ name, status: 'not-proven', detail });
};

const recordEvidence = (report, path) => {
  if (!report.evidencePaths.includes(path)) report.evidencePaths.push(path);
};

export const fixtureSourceDigest = (fixtureDir) => {
  const hash = createHash('sha256');
  const files = readdirSync(fixtureDir)
    .filter((file) => file.endsWith('.ndjson') || file.endsWith('.fixture.json'))
    .sort();
  for (const file of files) {
    hash.update(file);
    hash.update('\0');
    hash.update(readFileSync(join(fixtureDir, file)));
    hash.update('\0');
  }
  return `sha256:${hash.digest('hex')}`;
};

export const generatedJ01ConceptNDJSON = (fixtureDir) => {
  const path = join(fixtureDir, 'j01-concepts.fixture.json');
  if (!existsSync(path)) return undefined;
  const specification = JSON.parse(readFileSync(path, 'utf8'));
  const count = Number(specification.count);
  if (!Number.isInteger(count) || count < 1 || count > 10_000) {
    throw new Error('J01 concept fixture count must be an integer between 1 and 10000');
  }
  const system = String(specification.system ?? '').trim();
  const codePrefix = String(specification.codePrefix ?? '').trim();
  const displayPrefix = String(specification.displayPrefix ?? '').trim();
  if (!system || !codePrefix || !displayPrefix) {
    throw new Error('J01 concept fixture requires system, codePrefix, and displayPrefix');
  }
  return Array.from({ length: count }, (_, index) => {
    const suffix = String(index).padStart(4, '0');
    return JSON.stringify({
      resourceType: 'Observation',
      id: `dev-j01-concept-${suffix}`,
      status: 'final',
      code: { coding: [{ system, code: `${codePrefix}${suffix}`, display: `${displayPrefix} ${suffix}` }] },
      valueInteger: index,
    });
  }).join('\n') + '\n';
};

export const selectExternalJ01Manifest = async (fixtureDir) => {
  const specifications = [
    { resourceType: 'Patient', initialRecords: 32 },
    { resourceType: 'Observation', initialRecords: 32, requireRepeatedComponent: true, maximumScannedRecords: 100_000 },
  ];
  const files = [];
  for (const specification of specifications) {
    const sourcePath = join(fixtureDir, `${specification.resourceType}.ndjson`);
    const sourceStat = statSync(sourcePath);
    const selected = new Map();
    let recordNumber = 0;
    let sourceLine = 0;
    let repeatedComponentRecord;
    for await (const line of createInterface({ input: createReadStream(sourcePath), crlfDelay: Infinity })) {
      sourceLine += 1;
      if (!line.trim()) continue;
      recordNumber += 1;
      const resource = JSON.parse(line);
      if (resource.resourceType !== specification.resourceType || typeof resource.id !== 'string' || !resource.id) {
        throw new Error(`invalid ${specification.resourceType} source record at ${sourcePath}:${sourceLine}`);
      }
      const repeatedComponent = Array.isArray(resource.component)
        && resource.component.length > 1
        && resource.component.some((component) => component.code?.coding?.some((coding) => typeof coding.system === 'string' && coding.system && typeof coding.code === 'string' && coding.code));
      if (recordNumber <= specification.initialRecords || (repeatedComponent && !repeatedComponentRecord)) {
        selected.set(recordNumber, { recordNumber, sourceLine, id: resource.id, line, repeatedComponentCount: repeatedComponent ? resource.component.length : 0 });
      }
      if (repeatedComponent && !repeatedComponentRecord) {
        repeatedComponentRecord = { recordNumber, sourceLine, id: resource.id, componentCount: resource.component.length };
      }
      if (recordNumber >= specification.initialRecords && (!specification.requireRepeatedComponent || repeatedComponentRecord)) break;
      if (recordNumber >= (specification.maximumScannedRecords ?? specification.initialRecords)) break;
    }
    if (selected.size < specification.initialRecords) {
      throw new Error(`${specification.resourceType} source has only ${selected.size} records; J01 requires ${specification.initialRecords}`);
    }
    if (specification.requireRepeatedComponent && !repeatedComponentRecord) {
      throw new Error(`${specification.resourceType} source has no repeated component in the first ${specification.maximumScannedRecords} records`);
    }
    const records = [...selected.values()].sort((left, right) => left.recordNumber - right.recordNumber);
    const contents = Buffer.from(`${records.map((record) => record.line).join('\n')}\n`);
    const contentSHA256 = createHash('sha256').update(contents).digest('hex');
    files.push({
      name: `${specification.resourceType}.ndjson`,
      sourcePath,
      contents,
      contentSHA256,
      sourceStat: { sizeBytes: sourceStat.size, modifiedMs: sourceStat.mtimeMs, inode: sourceStat.ino },
      records: records.map(({ sourceLine: originalLine, id, repeatedComponentCount }) => ({ sourceLine: originalLine, id, ...(repeatedComponentCount ? { repeatedComponentCount } : {}) })),
      scannedRecords: recordNumber,
      repeatedComponentRecord,
    });
  }
  const manifestHash = createHash('sha256');
  for (const file of files) manifestHash.update(file.name).update('\0').update(file.contents).update('\0');
  const sourceSHA256 = `sha256:${manifestHash.digest('hex')}`;
  return {
    files,
    summary: {
      version: 'cda-fhir-meta-j01-v1',
      sourceDirectory: resolve(fixtureDir),
      sourceSHA256,
      selectedPayloadSHA256: sourceSHA256,
      sourceFiles: files.map(({ name, sourcePath, contentSHA256, sourceStat: selectedSourceStat, records, scannedRecords, repeatedComponentRecord: repeatedRecord }) => ({
        name,
        sourcePath,
        sourceFileSizeBytes: selectedSourceStat.sizeBytes,
        sourceFileModifiedMs: selectedSourceStat.modifiedMs,
        sourceFileInode: selectedSourceStat.inode,
        selectedContentSHA256: `sha256:${contentSHA256}`,
        scannedRecords,
        records,
        repeatedComponentRecord: repeatedRecord,
      })),
      selection: { firstPatientRecords: 32, firstObservationRecords: 32, firstRepeatedObservationComponentWithin: 100_000 },
    },
  };
};

export const assertExternalJ01SourcesUnchanged = (manifest) => {
  for (const file of manifest.files) {
    const sourceStat = statSync(file.sourcePath);
    if (sourceStat.size !== file.sourceStat.sizeBytes || sourceStat.mtimeMs !== file.sourceStat.modifiedMs || sourceStat.ino !== file.sourceStat.inode) {
      throw new Error(`external J01 source changed during verification: ${file.sourcePath}`);
    }
  }
  return true;
};

export const externalJ01PatientScalar = (builderState) => {
  const patientNode = builderState.catalog?.nodes?.find((node) => node.resourceType === 'Patient');
  const candidate = patientNode && builderState.catalog?.candidates?.find((entry) =>
    entry.nodeId === patientNode.nodeId
    && entry.fieldPath === 'resourceType'
    && entry.logicalType === 'string'
    && entry.cardinality === 'optional_one'
    && entry.constructionChoice?.options?.some((option) => option.form === 'VALUE' && option.support === 'SUPPORTED'));
  if (!candidate) {
    throw new Error('CDA J01 selected Patient manifest does not expose a supported scalar Patient.resourceType field');
  }
  return {
    fieldPath: candidate.fieldPath,
    label: candidate.label,
    checkboxLabel: `Select Patient.${candidate.fieldPath}`,
  };
};

export const summarizeTimingSamples = (samples) => {
  if (!Array.isArray(samples) || samples.length === 0) throw new Error('timing samples must be a non-empty array');
  if (samples.some((sample) => !Number.isFinite(sample) || sample < 0)) {
    throw new Error('timing samples must contain finite, non-negative durations');
  }
  const ordered = [...samples].sort((left, right) => left - right);
  const middle = Math.floor(ordered.length / 2);
  const p50Ms = ordered.length % 2 === 0
    ? (ordered[middle - 1] + ordered[middle]) / 2
    : ordered[middle];
  const p95Ms = ordered[Math.ceil(ordered.length * 0.95) - 1];
  return { count: ordered.length, p50Ms, p95Ms, minMs: ordered[0], maxMs: ordered.at(-1) };
};

export const j01ArtifactDownloadPlan = (buttonLabels) => {
  if (!Array.isArray(buttonLabels) || buttonLabels.some((label) => typeof label !== 'string')) {
    throw new Error('J01 artifact controls must be an array of button labels');
  }
  const labels = new Set(buttonLabels.map((label) => label.trim()));
  if (labels.has('Download training artifact')) {
    return { triggerLabel: 'Download training artifact', confirmationLabel: undefined };
  }
  if (labels.has('Download dataset')) {
    return { triggerLabel: 'Download dataset', confirmationLabel: 'Download ZIP' };
  }
  throw new Error('J01 Viewer exposes no supported artifact download control');
};

export const j01OwnerLiteralSnapshot = (entry) => ({
  status: entry.status,
  value: entry.value ?? null,
  unit: entry.unit ?? null,
  choiceArm: entry.choiceArm,
  system: entry.codings?.[0]?.system,
  code: entry.codings?.[0]?.code,
  source: {
    resourceType: entry.source?.resourceType,
    resourceId: entry.source?.resourceId,
    ownerPath: entry.source?.ownerPath,
    ownerOrdinal: entry.source?.ownerOrdinal,
  },
});

export const j01JSONValuesEquivalent = (left, right) => {
  if (left === null || left === undefined || right === null || right === undefined) {
    return left === null || left === undefined ? right === null || right === undefined : false;
  }
  if (Array.isArray(left) || Array.isArray(right)) {
    return Array.isArray(left) && Array.isArray(right) && left.length === right.length
      && left.every((value, index) => j01JSONValuesEquivalent(value, right[index]));
  }
  if (typeof left === 'object' || typeof right === 'object') {
    if (typeof left !== 'object' || typeof right !== 'object') return false;
    const keys = new Set([...Object.keys(left), ...Object.keys(right)]);
    return [...keys].every((key) => j01JSONValuesEquivalent(left[key] ?? null, right[key] ?? null));
  }
  return Object.is(left, right);
};

export const j01ViewerValuesAgree = ({ viewerTable, columns, previewByID, artifactByID, idColumn, structuredColumn }) => {
  const idLabel = columns.find((column) => column.column === idColumn)?.label;
  const idIndex = viewerTable.headers.indexOf(idLabel);
  if (idIndex < 0 || viewerTable.rows.length === 0) return false;
  let previewOverlap = 0;
  for (const row of viewerTable.rows) {
    const id = row[idIndex];
    const artifactRow = artifactByID.get(id);
    if (!artifactRow) return false;
    const previewRow = previewByID.get(id);
    if (previewRow) previewOverlap += 1;
    for (const [index, label] of viewerTable.headers.entries()) {
      const column = columns.find((candidate) => candidate.label === label);
      if (!column) return false;
      if (column.column === structuredColumn) continue;
      const artifactValue = artifactRow[column.column] ?? null;
      const displayed = artifactValue == null ? '—' : String(artifactValue);
      if (row[index] !== displayed) return false;
      if (previewRow && !j01JSONValuesEquivalent(artifactValue, previewRow[column.column] ?? null)) return false;
    }
  }
  return previewOverlap > 0;
};
export const j01SemanticInventoryRequest = ({ snapshotToken, rowRoot, resourceType, query, cursor }) => {
  if (typeof snapshotToken !== 'string' || snapshotToken.length === 0) throw new Error('J01 inventory request requires a catalog snapshot');
  if (typeof rowRoot !== 'string' || rowRoot.length === 0) throw new Error('J01 inventory request requires a row root');
  return {
    snapshotToken,
    rowRoot,
    ...(resourceType ? { resourceType } : {}),
    ...(query ? { query } : {}),
    ...(cursor ? { cursor } : {}),
    limit: 50,
  };
};

export const collectJ01SemanticConceptPages = async (readPage, request, fixture) => {
  const seenCursors = new Set();
  const seenCodes = new Set();
  const allowedSourceAvailability = new Set(['unknown', 'verified', 'unproven']);
  const pages = [];
  const expectedCodes = new Set(Array.from({ length: fixture.count }, (_, index) => `${fixture.codePrefix}${String(index).padStart(4, '0')}`));
  let cursor;
  let contextToken;
  let buildId;
  let sourceAvailability;
  let entries = [];
  do {
    const body = j01SemanticInventoryRequest({ ...request, cursor });
    const { response, value } = await readPage(body);
    if (!response?.ok) throw new Error(`J01 semantic inventory returned HTTP ${response?.status ?? 'unknown'}`);
    if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('J01 semantic inventory response is not an object');
    if (value.state !== 'complete' || !allowedSourceAvailability.has(value.sourceAvailability)) {
      throw new Error(`J01 semantic inventory is not complete or has invalid source availability: ${value.state ?? 'unknown'}/${value.sourceAvailability ?? 'missing'}`);
    }
    if (Object.keys(value).some((key) => /example|total.?count/i.test(key))) {
      throw new Error('J01 semantic inventory exposed an example or global count field');
    }
    if (typeof value.contextToken !== 'string' || value.contextToken.length === 0 || typeof value.buildId !== 'string' || value.buildId.length === 0) {
      throw new Error('J01 semantic inventory response is missing its context identity');
    }
    if (contextToken === undefined) {
      contextToken = value.contextToken;
      buildId = value.buildId;
      sourceAvailability = value.sourceAvailability;
    } else if (value.contextToken !== contextToken || value.buildId !== buildId || value.sourceAvailability !== sourceAvailability) {
      throw new Error('J01 semantic inventory page changed its context, build identity, or source availability');
    }
    if (!Array.isArray(value.entries) || value.entries.length > 50) throw new Error('J01 semantic inventory page exceeds its 50-entry contract');
    for (const item of value.entries) {
      if (
        typeof item?.code !== 'string' ||
        !expectedCodes.has(item.code) ||
        item.resourceType !== 'Observation' ||
        item.system !== fixture.system ||
        !String(item.display ?? '').startsWith(`${fixture.displayPrefix} `) ||
        typeof item.conceptId !== 'string' ||
        !item.conceptId ||
        typeof item.bindingId !== 'string' ||
        !item.bindingId
      ) {
        throw new Error('J01 semantic inventory returned a result outside the generated Observation concept set');
      }
      if (Object.hasOwn(item, 'examples')) throw new Error('J01 semantic inventory exposed example values outside the page identity contract');
      if (seenCodes.has(item.code)) throw new Error(`J01 semantic inventory repeated concept identity ${item.code}`);
      seenCodes.add(item.code);
      entries.push(item);
    }
    pages.push({ cursor, nextCursor: value.nextCursor, count: value.entries.length });
    cursor = value.nextCursor;
    if (cursor) {
      if (typeof cursor !== 'string' || seenCursors.has(cursor)) throw new Error('J01 semantic inventory repeated a pagination cursor');
      seenCursors.add(cursor);
      if (pages.length > fixture.count) throw new Error('J01 semantic inventory exceeded its pagination safety bound');
    }
  } while (cursor);

  const actualCodes = [...seenCodes].sort();
  if (JSON.stringify(actualCodes) !== JSON.stringify([...expectedCodes].sort())) {
    throw new Error(`J01 semantic inventory returned ${actualCodes.length} of ${fixture.count} expected concepts`);
  }
  return { contextToken, buildId, sourceAvailability, pages, entries, count: entries.length, countBasis: 'exact-paginated' };
};

const j01ColumnSourceIdentity = (column) => {
  switch (column?.source?.kind) {
    case 'field':
      return {
        kind: 'field',
        path: String(column.source.field?.path ?? '').replace(/^root\./, ''),
      };
    case 'ownerRecords':
      return {
        kind: 'ownerRecords',
        system: column.source.ownerRecords?.key?.system ?? '',
        code: column.source.ownerRecords?.key?.code ?? '',
        ownerPath: column.source.ownerRecords?.binding?.ownerPath ?? '',
        valuePath: column.source.ownerRecords?.binding?.valuePath ?? '',
      };
    default:
      throw new Error(`J01 column ${column?.column ?? '(unknown)'} has an unsupported saved source`);
  }
};

export const j01ColumnIdentitySnapshot = (builderState, outputId) => {
  const documents = builderState?.workspace?.documents;
  const document = Array.isArray(documents) ? documents.find((candidate) => candidate.output?.id === outputId) : undefined;
  if (!document) throw new Error(`J01 table ${outputId} is missing from the saved Builder workspace`);
  if (!Array.isArray(document.columns) || document.columns.length !== 3) {
    throw new Error(`J01 table must retain exactly three selected columns, got ${document.columns?.length ?? 0}`);
  }
  const snapshot = document.columns.map((column) => ({
    columnId: column.column,
    label: column.label,
    order: column.table?.order,
    source: j01ColumnSourceIdentity(column),
  })).sort((left, right) => left.order - right.order);
  const columnIds = snapshot.map((column) => column.columnId);
  const orders = snapshot.map((column) => column.order);
  if (columnIds.some((columnId) => typeof columnId !== 'string' || !columnId) || new Set(columnIds).size !== 3) {
    throw new Error('J01 table does not have three distinct stable column identities');
  }
  if (orders.some((order, index) => !Number.isInteger(order) || order !== index)) {
    throw new Error('J01 table does not have contiguous saved column positions from zero');
  }
  if (snapshot.some((column) => !column.label || Object.values(column.source).some((value) => !value))) {
    throw new Error('J01 saved column identity is missing its name or source');
  }
  return snapshot;
};

export const j01ConstructionChoiceCommandIdentities = (body, outputId) => {
  if (!body || typeof body !== 'object' || !Array.isArray(body.commands)) throw new Error('J01 authoring request has no command list');
  const allowedCommandFields = new Set(['type', 'outputId', 'constructionChoice', 'title', 'initialPresentation']);
  const allowedForms = new Set(['VALUE', 'FIRST', 'ALL', 'DISTINCT', 'OWNER_RECORDS']);
  const choices = body.commands.map((command, index) => {
    if (!command || command.type !== 'APPLY_CONSTRUCTION_CHOICE') throw new Error(`J01 command ${index} is not a compiler-choice application`);
    if (Object.keys(command).some((key) => !allowedCommandFields.has(key))) throw new Error(`J01 command ${index} contains a client-derived source field`);
    if (command.outputId !== outputId) throw new Error(`J01 command ${index} targets a different table`);
    const selection = command.constructionChoice;
    if (
      !selection ||
      typeof selection.choiceId !== 'string' ||
      !selection.choiceId ||
      !allowedForms.has(selection.form) ||
      Object.keys(selection).some((key) => !['choiceId', 'form'].includes(key))
    ) {
      throw new Error(`J01 command ${index} does not contain only a compiler-issued choice identity and form`);
    }
    return {
      choiceId: selection.choiceId,
      form: selection.form,
      outputId: command.outputId,
      title: command.title ?? '',
    };
  });
  const choiceIds = choices.map((choice) => choice.choiceId);
  if (!choices.length || new Set(choiceIds).size !== choices.length) throw new Error('J01 choice application must contain distinct selected choices');
  return choices;
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
      semanticsVersion: state.workspace?.semanticsVersion ?? AUTHORING_SEMANTICS_VERSION,
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

const seedFixture = async (target, { requireFresh = false, populateBootstrap = true, fixtureManifest } = {}) => {
  if (requireFresh) await assertFreshProject(target);
  const statusURL = `${target.apiUrl}/api/v1/datasets/${encodeURIComponent(target.fixtureProject)}/generations/${encodeURIComponent(target.fixtureGeneration)}`;
  const status = await request(statusURL, { timeout: 5000 });
  if (requireFresh && status.ok) throw new Error(`verification fixture project already exists: ${target.fixtureProject}`);
  let reused = status.ok;
  if (!reused) {
    if (status.status !== 404) throw new Error(`fixture generation preflight returned HTTP ${status.status}`);
    const form = new FormData();
    const generatedConcepts = fixtureManifest ? undefined : generatedJ01ConceptNDJSON(target.fixtureDir);
    const fixtureFiles = fixtureManifest
      ? fixtureManifest.files.map(({ name, contents }) => ({ name, contents }))
      : readdirSync(target.fixtureDir).filter((name) => name.endsWith('.ndjson')).sort().map((name) => {
        const source = readFileSync(join(target.fixtureDir, name));
        const contents = name === 'Observation.ndjson' && generatedConcepts
          ? Buffer.concat([source, Buffer.from(generatedConcepts)])
          : source;
        return { name, contents };
      });
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
  return { reused, fresh: requireFresh, bootstrapExplorerId: bootstrap.explorerId, bootstrapWorkspace, fixtureManifest: reused ? undefined : fixtureManifest?.summary };
};

const ensureDev = async (target, report, rebuild = false, fixtureManifest) => {
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
  const seed = await seedFixture(target, { fixtureManifest });
  await waitForHTTP(target.uiUrl);
  report.status = 'ready';
  report.timings.startup_ms = Date.now() - started;
  report.target.fixtureSeed = seed.reused ? 'reused' : 'seeded';
  report.target.bootstrapExplorerId = seed.bootstrapExplorerId;
  report.target.bootstrapWorkspace = seed.bootstrapWorkspace?.seeded ? 'seeded' : 'reused';
  writeJSON(join(target.artifacts, 'dev-session.json'), { ...target, fixtureSeed: report.target.fixtureSeed, bootstrapExplorerId: seed.bootstrapExplorerId, bootstrapWorkspace: report.target.bootstrapWorkspace, fixtureManifest: seed.fixtureManifest });
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
const scrollVirtualTableToRow = (testId, rowIndex) => {
  const scroll = document.querySelector('[data-testid="' + testId + '"]');
  const table = scroll?.querySelector('[role="table"]');
  const header = table?.querySelector('[role="row"]');
  const rowCount = Number(table?.getAttribute('aria-rowcount')) - 1;
  const headerHeight = Number.parseFloat(header?.style.height ?? '');
  const tableHeight = Number.parseFloat(table?.style.height ?? '');
  if (!scroll || !table || !Number.isInteger(rowIndex) || rowIndex < 0 || rowIndex >= rowCount || !Number.isFinite(headerHeight) || !Number.isFinite(tableHeight)) {
    throw new Error('virtual table row cannot be resolved: ' + testId + '/' + rowIndex);
  }
  const rowHeight = (tableHeight - headerHeight) / rowCount;
  if (!Number.isFinite(rowHeight) || rowHeight <= 0) throw new Error('virtual table row height is invalid: ' + testId);
  scroll.scrollTop = headerHeight + rowIndex * rowHeight;
  scroll.dispatchEvent(new Event('scroll'));
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

export const parseCSV = (text) => {
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

const parseArtifactCSV = (text) => {
  const rows = [];
  let row = [];
  let value = '';
  let quoted = false;
  let cellQuoted = false;
  let atCellStart = true;
  for (let index = 0; index < text.length; index += 1) {
    const char = text[index];
    if (quoted && char === '"' && text[index + 1] === '"') {
      value += '"';
      index += 1;
      continue;
    }
    if (char === '"') {
      if (!quoted && !atCellStart) throw new Error('artifact CSV has a quote inside an unquoted cell');
      quoted = !quoted;
      cellQuoted = true;
      atCellStart = false;
      continue;
    }
    if (!quoted && (char === ',' || char === '\n')) {
      row.push({ value: value.replace(/\r$/, ''), quoted: cellQuoted });
      value = '';
      cellQuoted = false;
      atCellStart = true;
      if (char === '\n') {
        if (row.some((cell) => cell.value !== '') || rows.length > 0 || row.length > 1) rows.push(row);
        row = [];
      }
      continue;
    }
    value += char;
    atCellStart = false;
  }
  if (quoted) throw new Error('artifact CSV has an unterminated quoted cell');
  if (value || row.length || cellQuoted) {
    row.push({ value: value.replace(/\r$/, ''), quoted: cellQuoted });
    rows.push(row);
  }
  return rows;
};

const parseArtifactCSVValue = (cell, column, nullEncoding) => {
  if (!cell.quoted && cell.value === nullEncoding) return null;
  const value = cell.value;
  const type = String(column.logicalType ?? '').toLowerCase();
  if (type === 'boolean' || type === 'bool') {
    if (value === 'true') return true;
    if (value === 'false') return false;
    throw new Error(`artifact CSV column ${column.name} contains a non-boolean value ${JSON.stringify(value)}`);
  }
  if (['integer', 'int', 'int32', 'int64', 'uint32', 'uint64'].includes(type)) {
    if (!/^-?(0|[1-9]\d*)$/.test(value)) throw new Error(`artifact CSV column ${column.name} contains a non-integer value ${JSON.stringify(value)}`);
    const parsed = Number(value);
    if (!Number.isSafeInteger(parsed)) throw new Error(`artifact CSV column ${column.name} exceeds JavaScript's exact integer range`);
    return parsed;
  }
  if (['number', 'decimal', 'float', 'float32', 'float64'].includes(type)) {
    if (!/^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/.test(value)) throw new Error(`artifact CSV column ${column.name} contains a non-number value ${JSON.stringify(value)}`);
    const parsed = Number(value);
    if (!Number.isFinite(parsed)) throw new Error(`artifact CSV column ${column.name} contains a non-finite number`);
    return parsed;
  }
  return value;
};

export const normalizeJ05LogicalValue = (value, column) => {
  if (value === null || value === undefined || typeof value !== 'string') return value;
  const type = String(column.logicalType ?? '').toLowerCase();
  if (['integer', 'int', 'int32', 'int64', 'uint32', 'uint64'].includes(type)) {
    if (!/^-?(0|[1-9]\d*)$/.test(value)) throw new Error(`J05 Viewer column ${column.column} contains a non-integer value ${JSON.stringify(value)}`);
    const parsed = Number(value);
    if (!Number.isSafeInteger(parsed)) throw new Error(`J05 Viewer column ${column.column} exceeds JavaScript's exact integer range`);
    return parsed;
  }
  if (['number', 'decimal', 'float', 'float32', 'float64'].includes(type)) {
    if (!/^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/.test(value)) throw new Error(`J05 Viewer column ${column.column} contains a non-number value ${JSON.stringify(value)}`);
    const parsed = Number(value);
    if (!Number.isFinite(parsed)) throw new Error(`J05 Viewer column ${column.column} contains a non-finite number`);
    return parsed;
  }
  if (type === 'boolean' || type === 'bool') {
    if (value === 'true') return true;
    if (value === 'false') return false;
    throw new Error(`J05 Viewer column ${column.column} contains a non-boolean value ${JSON.stringify(value)}`);
  }
  return value;
};

const parseArtifactJSONMember = (members, name) => {
  const bytes = members.get(name);
  if (!bytes) throw new Error(`J05 artifact is missing ${name}`);
  try { return JSON.parse(bytes.toString('utf8')); } catch (error) {
    throw new Error(`J05 artifact ${name} is invalid JSON: ${error instanceof Error ? error.message : String(error)}`);
  }
};

export const inspectJ05ArtifactPackage = (members) => {
  if (!(members instanceof Map)) throw new Error('J05 artifact members must be a Map from ZIP member names to bytes');
  const manifest = parseArtifactJSONMember(members, 'manifest.json');
  const schema = parseArtifactJSONMember(members, 'schema.json');
  const provenance = parseArtifactJSONMember(members, 'provenance.json');
  if (manifest.version !== 2) throw new Error(`J05 artifact manifest version must be 2, got ${manifest.version ?? 'missing'}`);
  if (!['CSV', 'JSONL'].includes(manifest.format)) throw new Error(`J05 artifact format is unsupported: ${manifest.format ?? 'missing'}`);
  const dataName = manifest.format === 'CSV' ? 'data.csv' : 'data.jsonl';
  if (members.has(manifest.format === 'CSV' ? 'data.jsonl' : 'data.csv')) throw new Error(`J05 ${manifest.format} artifact contains the wrong data representation`);
  const columns = schema.columns;
  if (!Array.isArray(columns) || !columns.length || columns.some((column) => !column?.name || !column?.outputKey || !column?.logicalType)) {
    throw new Error('J05 artifact schema columns must declare name, outputKey, and logicalType');
  }
  if (schema.format !== manifest.format) throw new Error('J05 artifact manifest and schema formats differ');
  if (manifest.features !== columns.length) throw new Error(`J05 artifact feature count ${manifest.features} differs from schema column count ${columns.length}`);
  if (JSON.stringify(manifest.descriptor?.columns?.map((column) => column.name)) !== JSON.stringify(columns.map((column) => column.name))) {
    throw new Error('J05 artifact descriptor column order differs from schema.json');
  }
  const descriptorColumns = manifest.descriptor.columns;
  const schemaContracts = columns.map((column) => ({ name: column.name, outputKey: column.outputKey, logicalType: column.logicalType, shape: column.shape, nullable: Boolean(column.nullable), repeated: Boolean(column.repeated) }));
  const descriptorContracts = descriptorColumns.map((column) => ({ name: column.name, outputKey: column.outputKey, logicalType: column.logicalType, shape: column.shape, nullable: Boolean(column.nullable), repeated: Boolean(column.repeated) }));
  if (JSON.stringify(schemaContracts) !== JSON.stringify(descriptorContracts)) throw new Error('J05 artifact schema types and shapes differ from its manifest descriptor');
  const checksums = manifest.members;
  if (!Array.isArray(checksums)) throw new Error('J05 artifact manifest has no member checksums');
  const seen = new Set();
  for (const item of checksums) {
    if (!item?.name || item.name === 'manifest.json' || seen.has(item.name)) throw new Error('J05 artifact manifest has an invalid or duplicate member checksum');
    seen.add(item.name);
    const bytes = members.get(item.name);
    if (!bytes || bytes.length !== item.bytes || createHash('sha256').update(bytes).digest('hex') !== item.sha256) {
      throw new Error(`J05 artifact member checksum mismatch: ${item.name}`);
    }
  }
  const archiveNames = [...members.keys()].filter((name) => name !== 'manifest.json').sort();
  if (JSON.stringify([...seen].sort()) !== JSON.stringify(archiveNames)) throw new Error('J05 artifact manifest member list differs from ZIP contents');
  if (!seen.has(dataName) || !seen.has('schema.json') || !seen.has('provenance.json') || !seen.has('quality.json')) {
    throw new Error(`J05 artifact is missing a required ${manifest.format} data, schema, provenance, or quality member`);
  }

  let rows;
  if (manifest.format === 'CSV') {
    const parsed = parseArtifactCSV(members.get(dataName).toString('utf8'));
    const header = parsed.shift()?.map((cell) => cell.value) ?? [];
    if (JSON.stringify(header) !== JSON.stringify(columns.map((column) => column.name))) throw new Error('J05 artifact CSV header differs from the declared schema column order');
    rows = parsed.map((cells, index) => {
      if (cells.length !== columns.length) throw new Error(`J05 artifact CSV row ${index + 1} has ${cells.length} cells; expected ${columns.length}`);
      return {
        rowId: undefined,
        values: Object.fromEntries(columns.map((column, columnIndex) => [column.name, parseArtifactCSVValue(cells[columnIndex], column, manifest.nullEncoding)])),
      };
    });
  } else {
    rows = members.get(dataName).toString('utf8').split(/\r?\n/).filter(Boolean).map((line, index) => {
      let parsed;
      try { parsed = JSON.parse(line); } catch (error) {
        throw new Error(`J05 artifact JSONL row ${index + 1} is invalid JSON: ${error instanceof Error ? error.message : String(error)}`);
      }
      if (!Object.hasOwn(parsed ?? {}, 'rowId') || !parsed.values || typeof parsed.values !== 'object' || Array.isArray(parsed.values)) {
        throw new Error(`J05 artifact JSONL row ${index + 1} must include rowId and a values object`);
      }
      return {
        rowId: parsed.rowId,
        values: Object.fromEntries(columns.filter((column) => Object.hasOwn(parsed.values, column.outputKey))
          .map((column) => [column.name, parsed.values[column.outputKey]])),
      };
    });
  }
  if (rows.length !== manifest.rows) throw new Error(`J05 artifact row count ${rows.length} differs from manifest count ${manifest.rows}`);
  const rowIdentityColumn = manifest.descriptor?.rowIdentity?.sourceIdColumn;
  const rowIdentities = rows.map((row) => JSON.stringify(row.rowId ?? row.values[rowIdentityColumn]));
  if (rowIdentities.some((identity) => identity === undefined || identity === 'null') || new Set(rowIdentities).size !== rowIdentities.length) {
    throw new Error('J05 artifact rows are missing distinct stable row identities');
  }
  return { manifest, schema, provenance, rows, dataName };
};

export const inspectJ01ArtifactRows = (members, idColumnName) => {
  if (typeof idColumnName !== 'string' || idColumnName.length === 0) throw new Error('J01 artifact inspection requires the selected stable id column');
  const artifact = inspectJ05ArtifactPackage(members);
  if (!artifact.schema.columns.some((column) => column.name === idColumnName)) {
    throw new Error(`J01 artifact schema omits the selected id column ${idColumnName}`);
  }
  const rowsByID = new Map();
  const rows = artifact.rows.map((row, rowIndex) => {
    const values = { ...row.values };
    if (artifact.manifest.format === 'CSV') {
      for (const column of artifact.schema.columns) {
        const isStructured = column.repeated
          || ['array', 'object', 'record', 'repeated'].includes(String(column.shape ?? '').toLowerCase())
          || ['array', 'object'].includes(String(column.logicalType ?? '').toLowerCase());
        if (!isStructured || typeof values[column.name] !== 'string') continue;
        try { values[column.name] = JSON.parse(values[column.name]); } catch (error) {
          throw new Error(`J01 artifact CSV row ${rowIndex + 1} column ${column.name} has invalid structured JSON: ${error instanceof Error ? error.message : String(error)}`);
        }
      }
    }
    return { ...row, values };
  });
  for (const row of rows) {
    const id = row.values[idColumnName];
    if (typeof id !== 'string' || id.length === 0 || rowsByID.has(id)) {
      throw new Error(`J01 artifact rows must contain distinct non-empty values for ${idColumnName}`);
    }
    rowsByID.set(id, row.values);
  }
  return { ...artifact, rows, rowsByID };
};

export const j05ArtifactIdentityIsCurrent = (prepared, current) => {
  const fields = ['project', 'datasetGeneration', 'outputId', 'revisionId', 'schemaDigest'];
  return fields.every((field) => typeof current?.[field] === 'string' && current[field] !== '' && prepared?.[field] === current[field]);
};

export const assertJ05ArtifactIdentity = (artifact, expected) => {
  for (const field of ['project', 'datasetGeneration', 'outputId', 'revisionId', 'schemaDigest']) {
    if (artifact?.manifest?.identity?.[field] !== expected?.[field]) {
      throw new Error(`J05 artifact ${field} differs from the current publication: expected ${JSON.stringify(expected?.[field])}, got ${JSON.stringify(artifact?.manifest?.identity?.[field])}`);
    }
  }
  for (const [field, manifestField] of [['datasetGeneration', 'sourceGeneration'], ['outputId', 'outputKey'], ['schemaDigest', 'publishedSchemaDigest']]) {
    if (artifact?.manifest?.descriptor?.[manifestField] !== expected?.[field]) {
      throw new Error(`J05 artifact descriptor ${manifestField} differs from the current publication: expected ${JSON.stringify(expected?.[field])}, got ${JSON.stringify(artifact?.manifest?.descriptor?.[manifestField])}`);
    }
  }
};

export const assertJ05ArtifactRows = (artifact, expectedRows) => {
  const compareRowID = expectedRows.some((row) => row.rowId !== undefined);
  const normalized = (row) => ({ ...(compareRowID ? { rowId: row.rowId } : {}), values: row.values });
  const canonical = (rows) => rows.map(normalized).sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  const expected = canonical(expectedRows);
  const actual = canonical(artifact.rows);
  if (JSON.stringify(actual) !== JSON.stringify(expected)) {
    throw new Error(`J05 artifact literal rows differ from Preview/Viewer: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
  }
};

const findDownloadedArchive = async (directory, timeout = 30000) => {
  const started = Date.now();
  while (Date.now() - started < timeout) {
    const files = readdirSync(directory).filter((file) => file.endsWith('.zip'));
    if (files.length) {
      const path = join(directory, files.sort().at(-1));
      if (!existsSync(`${path}.crdownload`)) return path;
    }
    await sleep(200);
  }
  throw new Error('viewer did not download a training artifact');
};

const j02RouteAlternatives = async (target, explorerId, state, outputId, candidateId, limit, report, occurrenceId) => {
  const url = `${bootstrapAuthoringURL(target, explorerId)}/construction-choices`;
  const pages = [];
  let cursor;
  do {
    const requestId = `j02-construction-${randomUUID()}`;
    const body = {
      snapshotToken: state.catalog.snapshotToken,
      outputId,
      source: { kind: 'FIELD', candidateId },
      limit: pages.length === 0 ? limit : Math.max(limit, 50),
      ...(occurrenceId ? { occurrenceId } : {}),
      ...(cursor ? { cursor } : {}),
    };
    const { response, value } = await requestJSON(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Request-ID': requestId },
      body: JSON.stringify(body),
      timeout: 30000,
    });
    report.requests.push({
      requestId,
      kind: 'construction-choices',
      status: response.status,
      request: body,
      result: {
        snapshotToken: value.snapshotToken,
        outputId: value.outputId,
        complete: value.complete,
        truncated: value.truncated,
        nextCursor: value.nextCursor,
        choices: (value.choices ?? []).map((choice) => ({
          choiceId: choice.choiceId,
          source: choice.source,
          route: choice.route,
          summary: choice.presentation?.summary,
          facts: choice.presentation?.facts,
          forms: choice.options?.map((option) => ({ form: option.form, decision: option.decision })),
        })),
      },
    });
    if (!response.ok) throw new Error(`J02 construction-choice search returned HTTP ${response.status}: ${JSON.stringify(value).slice(0, 500)}`);
    if (value.snapshotToken !== state.catalog.snapshotToken || value.outputId !== outputId) {
      throw new Error('J02 construction-choice response changed its catalog or table identity');
    }
    if (value.complete === value.truncated || (value.nextCursor && !value.truncated)) {
      throw new Error('J02 construction-choice response reported inconsistent completeness');
    }
    pages.push(value);
    cursor = value.nextCursor;
    if (pages.length >= 4) break;
  } while (cursor);
  const choices = pages.flatMap((page) => page.choices ?? []);
  return { pages, choices };
};

const routeChoiceSignature = (choice) => JSON.stringify((choice.route ?? []).map((step) => [
  step.edgeId,
  step.fromResourceType,
  step.relationship,
  step.storageDirection,
  step.toResourceType,
]));

const routeOccurrences = (root) => {
  const result = [];
  const visit = (node) => {
    result.push(node);
    for (const child of node.children ?? []) visit(child);
  };
  if (root) visit(root);
  return result;
};

const verifyJ02BrowserScenario = async (target, report, entryTarget = target) => {
  const runID = `${Date.now().toString(36)}-${randomUUID().slice(0, 8)}`;
  const evidenceDir = join(target.artifacts, runID);
  const downloadDir = join(evidenceDir, 'downloads');
  mkdirSync(downloadDir, { recursive: true, mode: 0o700 });
  report.target.evidenceDirectory = evidenceDir;
  recordEvidence(report, evidenceDir);
  report.requests = [];
  report.actions = [];
  report.sourceTuples = {};
  report.literalValues = {};
  const browser = await launchBrowser(downloadDir);
  const cdp = browser.cdp;
  const network = new Map();
  const pendingNetworkBodies = new Set();
  const captureCommandResult = async (item) => {
    try {
      const responseBody = await cdp.send('Network.getResponseBody', { requestId: item.requestId });
      const parsed = JSON.parse(responseBody.base64Encoded ? Buffer.from(responseBody.body, 'base64').toString('utf8') : responseBody.body);
      item.resultIdentity = {
        commandId: parsed.commandId,
        draftVersion: parsed.draftVersion,
        draftDigest: parsed.draftDigest,
        results: parsed.results,
      };
    } catch (error) {
      item.resultIdentity = { unavailable: true, reason: String(error) };
    }
  };
  cdp.on('Network.requestWillBeSent', (event) => {
    if (!event.request.url.includes('/authoring/v2/')) return;
    network.set(event.requestId, {
      requestId: event.requestId,
      url: new URL(event.request.url).pathname,
      method: event.request.method,
      xRequestId: event.request.headers?.['X-Request-ID'] ?? event.request.headers?.['x-request-id'],
      postData: event.request.postData,
      startedAtMs: event.wallTime ? Math.round(event.wallTime * 1000) : undefined,
    });
  });
  cdp.on('Network.responseReceived', (event) => {
    const item = network.get(event.requestId);
    if (item) item.response = { status: event.response.status, mimeType: event.response.mimeType };
  });
  cdp.on('Network.loadingFinished', (event) => {
    const item = network.get(event.requestId);
    if (!item) return;
    item.finishedAt = Date.now();
    if (!item.url.endsWith('/commands') || item.response?.status !== 200) return;
    const capture = captureCommandResult(item).finally(() => pendingNetworkBodies.delete(capture));
    pendingNetworkBodies.add(capture);
  });
  let explorerId = '';
  let outputId = '';
  let candidate = undefined;
  const action = async (name, operation) => {
    const started = Date.now();
    await operation();
    report.actions.push({ name, elapsedMs: Date.now() - started });
    report.timings[`j02_${name.replace(/[^a-z0-9]+/gi, '_').toLowerCase()}_ms`] = Date.now() - started;
  };
  const readState = async () => fetchBuilderState(target, explorerId);
  const waitForState = async (predicate, label, timeout = 30000) => {
    const started = Date.now();
    let state;
    while (Date.now() - started < timeout) {
      state = await readState();
      if (predicate(state)) return state;
      await sleep(200);
    }
    throw new Error(`timed out waiting for J02 Builder state: ${label}; draft=${state?.draftVersion}`);
  };
  const captureDOM = async (name) => {
    const path = join(evidenceDir, `${name}.html`);
    await snapshot(cdp, path);
    recordEvidence(report, path);
  };
  const clickGraphNode = async (nodeId) => browserEval(cdp, `
    const node = [...document.querySelectorAll('.react-flow__node')].find((candidate) => candidate.dataset.id === ${JSON.stringify(nodeId)});
    if (!node) throw new Error('graph node not found: ' + ${JSON.stringify(nodeId)});
    node.scrollIntoView({ block: 'center' });
    node.click();
  `);
  const selectTraversalOccurrence = async (document, occurrenceId) => {
    if (!routeOccurrences(document.route).some((occurrence) => occurrence.occurrenceId === occurrenceId)) {
      throw new Error(`J02 route occurrence is missing from the traversal: ${occurrenceId}`);
    }
    const encodedOccurrenceId = JSON.stringify(occurrenceId);
    await waitForBrowser(cdp, `[...document.querySelectorAll('nav[aria-label="Current traversal"] button[data-occurrence-id]')].some((button) => button.dataset.occurrenceId === ${encodedOccurrenceId})`);
    await browserEval(cdp, `
      const nav = document.querySelector('nav[aria-label="Current traversal"]');
      const button = [...(nav?.querySelectorAll('button[data-occurrence-id]') || [])]
        .find((candidate) => candidate.dataset.occurrenceId === ${encodedOccurrenceId});
      if (!button) throw new Error('traversal occurrence is missing: ' + ${encodedOccurrenceId});
      if (!button.parentElement?.className.includes('bg-blue-600')) button.click();
    `);
    await waitForBrowser(cdp, `[...document.querySelectorAll('nav[aria-label="Current traversal"] button[data-occurrence-id]')].some((button) => button.dataset.occurrenceId === ${encodedOccurrenceId} && button.parentElement?.className.includes('bg-blue-600'))`);
  };
  const waitForRoutePrefix = async (state, edgeIDs, label) => waitForState((next) => {
    const document = next.workspace?.documents?.find((item) => item.output?.id === outputId);
    return Boolean(routePathForEdgeIDs(document?.route, edgeIDs));
  }, label);
  const saveScreenshot = async (name) => {
    const image = await cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
    const path = join(evidenceDir, `${name}.png`);
    writeFileSync(path, Buffer.from(image.data, 'base64'), { mode: 0o600 });
    recordEvidence(report, path);
  };

  try {
    const browserURL = `${target.uiUrl}/?project=${encodeURIComponent(target.fixtureProject)}&explorer=${encodeURIComponent(report.target.bootstrapExplorerId)}&mode=builder`;
    await navigate(cdp, entryTarget.uiUrl);
    await navigate(cdp, browserURL);
    await waitForBrowser(cdp, `document.body.innerText.includes('Build your features') || document.body.innerText.includes('Create your first table')`, 60000);
    await action('create_editor_identity_through_dom', async () => {
      const title = `J02 ${target.fixtureProject.slice(-18)}`;
      await browserEval(cdp, `clickText('summary', 'New explorer')`);
      await browserEval(cdp, `setInput('new-explorer-name', ${JSON.stringify(title)})`);
      await browserEval(cdp, `clickButton('Create blank')`);
      await waitForBrowser(cdp, `document.querySelector('select[aria-label="Explorer"] option:checked')?.textContent.trim() === ${JSON.stringify(title)} && document.body.innerText.includes('Create your first table')`);
      explorerId = await evaluate(cdp, `document.querySelector('select[aria-label="Explorer"]')?.value || ''`);
      if (!explorerId) throw new Error('J02 blank Explorer was not selected in the Builder');
      report.target.j02ExplorerId = explorerId;
    });
    await action('create_patient_table_through_dom', async () => {
      await browserEval(cdp, `setInput('first-table-name', 'J02 patient routes')`);
      await browserEval(cdp, `clickButton('Create table')`);
      await waitForBrowser(cdp, `document.querySelector('button[aria-label="Choose Patient rows"]') && document.body.innerText.includes('What should one row represent?')`);
      await browserEval(cdp, `clickButton('Choose Patient rows')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('[aria-label="Search features by field name, concept, or code"]'))`);
      const state = await waitForState((value) => value.workspace?.documents?.length === 1 && value.workspace.documents[0].rootResourceType === 'Patient', 'Patient row root');
      outputId = state.workspace.documents[0].output.id;
      report.target.outputId = outputId;
    });
    await captureDOM('j02-catalog-initial');

    let state = await readState();
    const resourceByNode = new Map(state.catalog.nodes.map((node) => [node.nodeId, node.resourceType]));
    candidate = state.catalog.candidates.find((item) => {
      const node = state.catalog.nodes.find((value) => value.nodeId === item.nodeId);
      return node?.resourceType === 'DiagnosticReport' && item.fieldPath.replace(/^root\./, '') === 'status';
    });
    if (!candidate?.candidateId) throw new Error('J02 schema catalog lacks DiagnosticReport.status for the related-column route-choice proof');
    report.target.catalogCandidateId = candidate.candidateId;
    const choiceSearch = await j02RouteAlternatives(target, explorerId, state, outputId, candidate.candidateId, 1, report);
    const firstPage = choiceSearch.pages[0];
    const choices = choiceSearch.choices;
    const directChoice = choices.find((choice) => choice.route.length === 1
      && choice.route[0].fromResourceType === 'Patient'
      && choice.route[0].toResourceType === 'DiagnosticReport'
      && choice.route[0].relationship === 'subject_Patient');
    const distinctRouteSignatures = [...new Set(choices.map(routeChoiceSignature))];
    recordAssertion(report, 'j02-limited-route-search-reports-honest-truncation-and-cursor', true,
      firstPage.complete === false && firstPage.truncated === true && Boolean(firstPage.nextCursor)
      && firstPage.choices.length === 1 && choices.length >= 2);
    recordAssertion(report, 'j02-catalog-preserves-semantically-distinct-source-routes', true,
      Boolean(directChoice && distinctRouteSignatures.length >= 2));
    if (!directChoice) throw new Error('J02 route search did not return the direct DiagnosticReport.subject route needed for source inspection');
    report.sourceTuples.catalogAlternatives = choices.map((choice) => ({
      choiceId: choice.choiceId,
      tuple: choice.route.map((step) => ({
        catalogEdgeId: step.edgeId,
        fromResourceType: step.fromResourceType,
        relationship: step.relationship,
        storageDirection: step.storageDirection,
        toResourceType: step.toResourceType,
      })),
    }));

    await action('cancel_catalog_route_choice_without_draft_mutation', async () => {
      await browserEval(cdp, `setInput('Search features by field name, concept, or code', 'status')`);
      await browserEval(cdp, `clickButton('Search')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Select DiagnosticReport.status"]:not(:disabled)'))`);
      await browserEval(cdp, `const input = inputByLabel('Select DiagnosticReport.status'); if (!input) throw new Error('DiagnosticReport.status is unavailable in Concept catalog'); input.click();`);
      const beforeCancel = await readState();
      const beforeBytes = draftFingerprint(beforeCancel);
      await browserEval(cdp, `clickButton('Add 1 selected feature')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('[role="dialog"]') && document.body.innerText.includes('Choose output forms'))`);
      await captureDOM('j02-catalog-route-choice-before-cancel');
      await browserEval(cdp, `
        const dialog = document.querySelector('[role="dialog"][aria-labelledby="catalog-selection-dialog-title"]');
        const button = [...(dialog?.querySelectorAll('button') || [])].find((candidate) => norm(candidate.textContent) === 'Cancel');
        if (!button) throw new Error('catalog route-choice cancel control is missing');
        button.click();
      `);
      await waitForBrowser(cdp, `!document.querySelector('[role="dialog"][aria-labelledby="catalog-selection-dialog-title"]')`);
      const afterCancel = await readState();
      recordAssertion(report, 'j02-catalog-choice-cancel-is-byte-identical-draft', beforeBytes, draftFingerprint(afterCancel));
      report.target.cancelDraftBytes = Buffer.byteLength(beforeBytes, 'utf8');
      await captureDOM('j02-catalog-after-cancel');
    });

    const validForm = directChoice.options.find((option) => option.decision === 'DEFAULT')?.form ?? directChoice.options[0]?.form;
    if (!validForm) throw new Error('J02 direct route has no compiler-proved output form');
    await action('reject_tampered_route_choice_without_partial_mutation', async () => {
      const before = await readState();
      const beforeBytes = draftFingerprint(before);
      const requestId = `j02-tampered-${randomUUID()}`;
      const commandId = randomUUID();
      const body = {
        commandId,
        semanticsVersion: before.workspace?.semanticsVersion ?? AUTHORING_SEMANTICS_VERSION,
        snapshotToken: before.catalog.snapshotToken,
        expectedDraftVersion: before.draftVersion,
        expectedDraftDigest: before.draftDigest,
        commands: [{
          type: 'APPLY_CONSTRUCTION_CHOICE',
          outputId,
          constructionChoice: { choiceId: `${directChoice.choiceId}.tampered`, form: validForm },
          title: 'J02 tampered route must not apply',
        }],
      };
      const { response, value } = await requestJSON(`${bootstrapAuthoringURL(target, explorerId)}/commands`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Request-ID': requestId },
        body: JSON.stringify(body),
        timeout: 30000,
      });
      const after = await readState();
      report.requests.push({
        requestId,
        kind: 'negative-tampered-route-choice',
        commandId,
        status: response.status,
        error: value,
        draftBefore: { version: before.draftVersion, digest: before.draftDigest },
        draftAfter: { version: after.draftVersion, digest: after.draftDigest },
      });
      recordAssertion(report, 'j02-tampered-route-choice-rejected-as-client-error', true,
        response.status >= 400 && response.status < 500);
      recordAssertion(report, 'j02-tampered-route-choice-has-no-partial-mutation', beforeBytes, draftFingerprint(after));
    });

    await action('apply_related_column_from_catalog', async () => {
      await browserEval(cdp, `clickButton('Add 1 selected feature')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('[role="dialog"]') && document.body.innerText.includes('Choose output forms'))`);
      const currentChoices = await j02RouteAlternatives(target, explorerId, await readState(), outputId, candidate.candidateId, 50, report);
      const selectedIndex = currentChoices.choices.findIndex((choice) => choice.choiceId === directChoice.choiceId);
      if (selectedIndex < 0) throw new Error('J02 direct route choice identity changed between catalog review and apply');
      if (selectedIndex >= 50) throw new Error('J02 direct route choice is beyond the Concept catalog route-choice page');
      if (currentChoices.choices.length > 1) {
        const label = `${candidate.label.trim() || candidate.fieldPath} route ${selectedIndex + 1}: ${directChoice.presentation.summary}`;
        await browserEval(cdp, `const radio = [...document.querySelectorAll('input[type="radio"][aria-label]')].find((input) => input.getAttribute('aria-label').endsWith(${JSON.stringify(label)})); if (!radio) throw new Error('direct related route radio is missing'); radio.click();`);
      } else if (routeChoiceSignature(currentChoices.choices[0]) !== routeChoiceSignature(directChoice)) {
        throw new Error('J02 single catalog choice is not the expected direct source route');
      }
      const formOption = directChoice.options.find((option) => option.form === validForm);
      const formLabel = validForm === 'OWNER_RECORDS'
        ? 'Keep each matching record'
        : `${formOption.shape} · ${formOption.preservation} · ${validForm}`;
      const formRadio = `${candidate.label.trim() || candidate.fieldPath}: ${formLabel}`;
      await browserEval(cdp, `const form = [...document.querySelectorAll('input[type="radio"][aria-label]')].find((input) => input.getAttribute('aria-label') === ${JSON.stringify(formRadio)}); if (form && !form.checked) form.click();`);
      await browserEval(cdp, `clickButton('Add 1 selected feature')`);
      state = await waitForState((value) => value.workspace?.documents?.[0]?.columns?.some((column) => column.source?.kind === 'field' && column.source.field.path.replace(/^root\./, '') === 'status'), 'catalog related column');
      const doc = state.workspace.documents.find((value) => value.output?.id === outputId);
      const column = doc.columns.find((value) => value.source?.kind === 'field' && value.source.field.path.replace(/^root\./, '') === 'status');
      const pinnedChoices = await j02RouteAlternatives(target, explorerId, state, outputId, candidate.candidateId, 50, report, column.occurrenceId);
      const pinnedPage = pinnedChoices.pages[0];
      recordAssertion(report, 'j02-pinned-occurrence-route-search-is-complete', {
        complete: true, truncated: false, nextCursor: undefined, routeCount: 1,
      }, {
        complete: pinnedPage.complete, truncated: pinnedPage.truncated, nextCursor: pinnedPage.nextCursor,
        routeCount: pinnedPage.choices.length,
      });
      recordAssertion(report, 'j02-pinned-occurrence-search-matches-saved-choice', directChoice.choiceId,
        pinnedPage.choices[0]?.choiceId);
      report.target.relatedColumnId = column.column;
      recordAssertion(report, 'j02-concept-catalog-adds-related-column-without-graph', true,
        column.occurrenceId !== 'base' && !(await evaluate(cdp, `Boolean(document.querySelector('.react-flow__node'))`)));
      await captureDOM('j02-related-column-catalog');
    });

    let savedState = await readState();
    let savedDoc = savedState.workspace.documents.find((value) => value.output?.id === outputId);
    let savedColumn = savedDoc.columns.find((value) => value.column === report.target.relatedColumnId);
    const columnSourceRequestId = `j02-column-source-${randomUUID()}`;
    const columnSourceBody = { snapshotToken: savedState.catalog.snapshotToken, outputId, column: savedColumn.column };
    const columnSource = await requestJSON(`${bootstrapAuthoringURL(target, explorerId)}/column-source`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Request-ID': columnSourceRequestId },
      body: JSON.stringify(columnSourceBody),
      timeout: 30000,
    });
    if (!columnSource.response.ok) throw new Error(`J02 source inspection returned HTTP ${columnSource.response.status}`);
    report.requests.push({ requestId: columnSourceRequestId, kind: 'column-source', status: columnSource.response.status, request: columnSourceBody, result: columnSource.value });
    const inspectedStep = columnSource.value.route.find((step) => step.resourceType === 'DiagnosticReport');
    recordAssertion(report, 'j02-source-inspector-exposes-exact-related-route-and-inbound-direction', {
      resourceType: 'DiagnosticReport', relationship: 'subject_Patient', storageDirection: 'INBOUND',
    }, {
      resourceType: inspectedStep?.resourceType,
      relationship: inspectedStep?.relationship,
      storageDirection: inspectedStep?.storageDirection,
    });
    report.sourceTuples.catalogColumn = {
      column: savedColumn.column,
      source: columnSource.value,
      route: columnSource.value.route,
    };
    await browserEval(cdp, `clickButton('Column details')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('[aria-label="Source for status"]'))`);
    const visibleSource = await evaluate(cdp, `document.querySelector('[aria-label="Source for status"]')?.innerText || ''`);
    if (!visibleSource.includes('subject_Patient') || !visibleSource.includes('inbound')) throw new Error('J02 source inspector DOM omitted the exact relationship or stored direction');
    await captureDOM('j02-source-inspector');
    await action('edit_saved_occurrence_in_graph', async () => {
      await browserEval(cdp, `clickButton('Edit in graph')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('.react-flow__node')) && Boolean(document.querySelector('input[aria-label="Display name for configured status"]')) && Boolean([...document.querySelectorAll('nav[aria-label="Current traversal"] [data-traversal-label]')].find((label) => label.parentElement?.className.includes('bg-blue-600') && label.textContent.includes('DiagnosticReport'))) `);
      recordAssertion(report, 'j02-edit-in-graph-focuses-saved-column-occurrence', true,
        await evaluate(cdp, `Boolean(document.querySelector('input[aria-label="Display name for configured status"]')) && [...document.querySelectorAll('nav[aria-label="Current traversal"] [data-traversal-label]')].some((label) => label.parentElement?.className.includes('bg-blue-600') && label.textContent.includes('DiagnosticReport'))`));
      await captureDOM('j02-edit-in-graph-focus');
    });

    const chain = [
      ['Patient', 'Group', 'member_entity_Patient'],
      ['Group', 'Specimen', 'subject_Group'],
      ['Specimen', 'Observation', 'specimen_Specimen'],
      ['Observation', 'DiagnosticReport', 'result'],
      ['DiagnosticReport', 'ResearchStudy', 'result_DiagnosticReport'],
    ];
    const exactEdges = chain.map(([fromType, toType, label]) => {
      const matches = savedState.catalog.edges.filter((edge) =>
        resourceByNode.get(edge.fromNodeId) === fromType
        && resourceByNode.get(edge.toNodeId) === toType
        && edge.label === label,
      );
      if (matches.length !== 1) throw new Error(`J02 schema-backed graph edge is not unique: ${fromType} --${label}--> ${toType} (matches=${matches.length})`);
      return { ...matches[0], fromResourceType: fromType, toResourceType: toType, relationship: label };
    });
    report.target.explicitChain = exactEdges;
    await action('author_five_edge_route_through_graph_dom', async () => {
      const patientNode = savedState.catalog.nodes.find((node) => node.resourceType === 'Patient' && node.rowRootEligible);
      if (!patientNode) throw new Error('J02 Patient row node disappeared from the graph catalog');
      await clickGraphNode(patientNode.nodeId);
      await selectTraversalOccurrence(savedDoc, savedDoc.route.occurrenceId);
      const expectedEdgeIDs = [];
      for (const edge of exactEdges) {
        expectedEdgeIDs.push(edge.edgeId);
        const prefix = [...expectedEdgeIDs];
        await clickGraphNode(edge.toNodeId);
        let stateAfterClick;
        let routeAdded = false;
        let panelReady = false;
        const deadline = Date.now() + 5000;
        while (Date.now() < deadline) {
          stateAfterClick = await readState();
          const currentDocument = stateAfterClick.workspace?.documents?.find((item) => item.output?.id === outputId);
          routeAdded = Boolean(routePathForEdgeIDs(currentDocument?.route, prefix));
          if (routeAdded) break;
          panelReady = await evaluate(cdp, `Boolean(document.querySelector('select[aria-label="Relationship to add"]') || document.querySelector('button') && [...document.querySelectorAll('button')].some((button) => ['Add branch', 'Add traversal'].includes(button.textContent.trim())))`);
          if (panelReady) break;
          await sleep(200);
        }
        if (!routeAdded) {
          if (!panelReady) throw new Error(`graph did not offer ${edge.fromResourceType} --${edge.relationship}--> ${edge.toResourceType}`);
          await browserEval(cdp, `
            const select = document.querySelector('select[aria-label="Relationship to add"]');
            if (select) {
              const option = [...select.options].find((candidate) => candidate.value === ${JSON.stringify(edge.edgeId)});
              if (!option) throw new Error('expected relationship edge is not offered: ' + ${JSON.stringify(edge.edgeId)});
              select.value = option.value;
              select.dispatchEvent(new Event('change', { bubbles: true }));
            }
            const button = [...document.querySelectorAll('button')].find((candidate) => ['Add branch', 'Add traversal'].includes(norm(candidate.textContent)));
            if (!button || button.disabled) throw new Error('graph add traversal control is unavailable');
            button.click();
          `);
          stateAfterClick = await waitForRoutePrefix(stateAfterClick, prefix, `${edge.relationship} route edge`);
        }
        const currentDocument = stateAfterClick.workspace.documents.find((item) => item.output?.id === outputId);
        const exactPath = routePathForEdgeIDs(currentDocument.route, expectedEdgeIDs);
        if (!exactPath) throw new Error(`J02 route did not persist the expected exact prefix: ${expectedEdgeIDs.join(',')}`);
        await selectTraversalOccurrence(currentDocument, exactPath.at(-1).occurrenceId);
        report.sourceTuples[`graphHop${expectedEdgeIDs.length}`] = {
          catalogEdgeId: edge.edgeId,
          fromResourceType: edge.fromResourceType,
          relationship: edge.relationship,
          toResourceType: edge.toResourceType,
          draftVersion: stateAfterClick.draftVersion,
          draftDigest: stateAfterClick.draftDigest,
        };
      }
      const completed = await waitForRoutePrefix(await readState(), expectedEdgeIDs, 'five-edge J02 route');
      const completedDoc = completed.workspace.documents.find((item) => item.output?.id === outputId);
      const completedPath = routePathForEdgeIDs(completedDoc.route, exactEdges.map((edge) => edge.edgeId));
      recordAssertion(report, 'j02-graph-authors-exact-five-schema-edges', exactEdges.map((edge) => edge.edgeId), completedPath?.map((step) => step.catalogEdgeId));
      report.target.explicitRouteOccurrenceId = completedPath.at(-1).occurrenceId;
      await captureDOM('j02-graph-five-edge-route');
    });

    let graphState = await readState();
    let graphDocument = graphState.workspace.documents.find((item) => item.output?.id === outputId);
    const studyPath = routePathForEdgeIDs(graphDocument.route, exactEdges.map((edge) => edge.edgeId));
    if (!studyPath?.length) throw new Error('J02 five-edge route does not resolve to its ResearchStudy occurrence');
    await selectTraversalOccurrence(graphDocument, studyPath.at(-1).occurrenceId);
    await waitForBrowser(cdp, `Boolean(document.querySelector('[aria-label="Search features by field name, concept, or code"]')) && Boolean(document.querySelector('input[aria-label="Select ResearchStudy.title"]'))`);
    const studyNodeID = exactEdges.at(-1).toNodeId;
    const studyCandidate = graphState.catalog.candidates.find((item) =>
      item.nodeId === studyNodeID && item.fieldPath.replace(/^root\./, '') === 'title');
    if (!studyCandidate?.candidateId) throw new Error('J02 node-local catalog has no ResearchStudy.title field candidate');
    await action('select_node_local_field_at_reached_research_study', async () => {
      await browserEval(cdp, `setInput('Search features by field name, concept, or code', 'title')`);
      await browserEval(cdp, `clickButton('Search')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Select ResearchStudy.title"]:not(:disabled)'))`);
      await browserEval(cdp, `const input = inputByLabel('Select ResearchStudy.title'); if (!input) throw new Error('ResearchStudy.title is unavailable in the node-local catalog'); input.click();`);
      await browserEval(cdp, `clickButton('Add 1 selected feature')`);
      const hasDialog = await waitForBrowser(cdp, `Boolean(document.querySelector('[role="dialog"]') || document.querySelector('input[aria-label="Display name for configured title"]'))`).then(() => evaluate(cdp, `Boolean(document.querySelector('[role="dialog"]'))`));
      if (hasDialog) {
        await captureDOM('j02-node-local-output-form');
        await browserEval(cdp, `clickButton('Add 1 selected feature')`);
      }
      graphState = await waitForState((value) => value.workspace?.documents?.find((doc) => doc.output?.id === outputId)?.columns?.some((column) => column.source?.kind === 'field' && column.source.field.path.replace(/^root\./, '') === 'title'), 'node-local ResearchStudy.title column');
      graphDocument = graphState.workspace.documents.find((item) => item.output?.id === outputId);
      const titleColumn = graphDocument.columns.find((column) => column.source?.kind === 'field' && column.source.field.path.replace(/^root\./, '') === 'title');
      if (titleColumn.occurrenceId !== studyPath.at(-1).occurrenceId) throw new Error('J02 node-local field was not pinned to the reached ResearchStudy occurrence');
      report.target.titleColumnId = titleColumn.column;
      recordAssertion(report, 'j02-node-local-catalog-selects-field-on-reached-resource', studyPath.at(-1).occurrenceId, titleColumn.occurrenceId);
      await captureDOM('j02-node-local-title-applied');
    });

    const studyTitleColumn = graphDocument.columns.find((column) => column.column === report.target.titleColumnId);
    const sourceRequestId = `j02-explicit-source-${randomUUID()}`;
    const sourceBody = { snapshotToken: graphState.catalog.snapshotToken, outputId, column: studyTitleColumn.column };
    const sourceResponse = await requestJSON(`${bootstrapAuthoringURL(target, explorerId)}/column-source`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Request-ID': sourceRequestId },
      body: JSON.stringify(sourceBody),
      timeout: 30000,
    });
    if (!sourceResponse.response.ok) throw new Error(`J02 explicit source inspection returned HTTP ${sourceResponse.response.status}`);
    report.requests.push({ requestId: sourceRequestId, kind: 'explicit-column-source', status: sourceResponse.response.status, request: sourceBody, result: sourceResponse.value });
    const expectedChainIDs = exactEdges.map((edge) => edge.edgeId);
    const savedTitleIDs = sourceResponse.value.route.flatMap((step) => step.catalogEdgeId ? [step.catalogEdgeId] : []);
    recordAssertion(report, 'j02-exact-explicit-catalog-edge-sequence-is-saved', expectedChainIDs, savedTitleIDs);
    const explicitDirections = sourceResponse.value.route.filter((step) => step.catalogEdgeId).map((step) => step.storageDirection);
    recordAssertion(report, 'j02-five-edge-route-reports-real-inbound-storage-directions', true,
      explicitDirections.length === 5 && explicitDirections.every((direction) => direction === 'INBOUND'));
    report.sourceTuples.explicitTitle = {
      column: studyTitleColumn.column,
      occurrenceId: studyTitleColumn.occurrenceId,
      route: sourceResponse.value.route,
      facts: sourceResponse.value.facts,
      summary: sourceResponse.value.summary,
    };

    const beforeViewSwitch = draftFingerprint(graphState);
    await action('switch_catalog_and_graph_views_without_mutation', async () => {
      await browserEval(cdp, `clickButton('Concept catalog')`);
      await waitForBrowser(cdp, `!document.querySelector('.react-flow__node') && Boolean(document.querySelector('[aria-label="Search features by field name, concept, or code"]'))`);
      await captureDOM('j02-catalog-view');
      await browserEval(cdp, `clickButton('Advanced graph')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('.react-flow__node')) && document.body.innerText.includes('Dataset graph')`);
      await captureDOM('j02-graph-view');
      const afterViewSwitch = await readState();
      recordAssertion(report, 'j02-catalog-graph-view-switch-preserves-draft-bytes', beforeViewSwitch, draftFingerprint(afterViewSwitch));
    });

    const reloadURL = `${target.uiUrl}/?project=${encodeURIComponent(target.fixtureProject)}&explorer=${encodeURIComponent(explorerId)}&mode=builder`;
    await action('reload_and_confirm_saved_route', async () => {
      await navigate(cdp, reloadURL);
      await cdp.send('Page.reload', { ignoreCache: false });
      await waitForBrowser(cdp, `document.readyState === 'complete' && document.body.innerText.includes('J02 patient routes')`, 60000);
      await waitForState((value) => value.workspace?.documents?.some((doc) => doc.output?.id === outputId), 'reloaded J02 Builder workspace');
      const reloaded = await readState();
      const reloadedDoc = reloaded.workspace.documents.find((item) => item.output?.id === outputId);
      const reloadedPath = routePathForEdgeIDs(reloadedDoc.route, expectedChainIDs);
      recordAssertion(report, 'j02-reload-preserves-exact-five-edge-catalog-edge-sequence', expectedChainIDs, reloadedPath?.map((step) => step.catalogEdgeId));
      const reloadedTitle = reloadedDoc.columns.find((column) => column.column === report.target.titleColumnId);
      recordAssertion(report, 'j02-reload-preserves-research-study-title-occurrence', report.target.explicitRouteOccurrenceId, reloadedTitle?.occurrenceId);
      const reloadedSourceId = `j02-reloaded-source-${randomUUID()}`;
      const reloadedSourceBody = { snapshotToken: reloaded.catalog.snapshotToken, outputId, column: report.target.titleColumnId };
      const reloadedSource = await requestJSON(`${bootstrapAuthoringURL(target, explorerId)}/column-source`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Request-ID': reloadedSourceId },
        body: JSON.stringify(reloadedSourceBody),
        timeout: 30000,
      });
      if (!reloadedSource.response.ok) throw new Error(`J02 reloaded source read returned HTTP ${reloadedSource.response.status}`);
      report.requests.push({ requestId: reloadedSourceId, kind: 'reloaded-column-source', status: reloadedSource.response.status, request: reloadedSourceBody, result: reloadedSource.value });
      const reloadedSourceIDs = reloadedSource.value.route.flatMap((step) => step.catalogEdgeId ? [step.catalogEdgeId] : []);
      recordAssertion(report, 'j02-reload-preserves-source-tuple-identities', expectedChainIDs, reloadedSourceIDs);
      report.sourceTuples.reloadedTitle = { route: reloadedSource.value.route, facts: reloadedSource.value.facts, summary: reloadedSource.value.summary };
      await captureDOM('j02-builder-after-reload');
      await browserEval(cdp, `clickButton('Preview')`);
      await waitForBrowser(cdp, `document.body.innerText.includes('Preview and configure') && document.body.innerText.includes('J02 route study')`, 60000);
      const previewCells = await evaluate(cdp, `([...document.querySelectorAll('[role="table"] [role="cell"]')].map((cell) => ({ display: cell.innerText.trim(), literal: cell.querySelector('[title]')?.getAttribute('title') ?? '' })))`);
      recordAssertion(report, 'j02-reload-preview-retains-literal-research-study-title', true,
        previewCells.some((cell) => cell.display === 'J02 route study' || cell.literal === 'J02 route study'));
      report.literalValues.afterReloadPreview = previewCells;
      await captureDOM('j02-preview-after-reload');
      await saveScreenshot('j02-preview-after-reload');
    });

    await sleep(300);
    await Promise.allSettled([...pendingNetworkBodies]);
    const networkEvidence = [];
    for (const item of network.values()) {
      if (item.response && item.url.endsWith('/commands') && !item.resultIdentity) {
        await captureCommandResult(item);
      }
      networkEvidence.push({ ...item });
    }
    const networkPath = join(evidenceDir, 'network-identities.json');
    writeJSON(networkPath, networkEvidence);
    recordEvidence(report, networkPath);
    const commandTransactions = networkEvidence.filter((item) => item.url.endsWith('/commands') && item.response?.status === 200);
    recordAssertion(report, 'j02-ui-route-and-column-commands-have-request-result-identities', true,
      commandTransactions.length >= 6 && commandTransactions.every((item) => item.xRequestId && item.resultIdentity?.commandId && item.resultIdentity?.draftVersion));
    report.target.successfulCommandTransactions = commandTransactions.length;
  } finally {
    try {
      const path = join(evidenceDir, 'network-identities.json');
      if (!existsSync(path)) writeJSON(path, [...network.values()]);
      recordEvidence(report, path);
    } catch {}
    await browser.close();
  }
};

const verifyJ05BrowserScenario = async (target, report, entryTarget = target) => {
  const runID = `${Date.now().toString(36)}-${randomUUID().slice(0, 8)}`;
  const evidenceDir = join(target.artifacts, runID);
  const downloadDir = join(evidenceDir, 'downloads');
  mkdirSync(downloadDir, { recursive: true, mode: 0o700 });
  report.target.evidenceDirectory = evidenceDir;
  report.target.ports = { api: target.apiPort, ui: target.uiPort };
  recordEvidence(report, evidenceDir);
  report.requests = [];
  report.actions = [];

  const browser = await launchBrowser(downloadDir);
  const cdp = browser.cdp;
  const previewResponses = [];
  const pendingBodies = new Set();
  cdp.on('Network.requestWillBeSent', (event) => {
    if (!event.request.url.includes('/authoring/v2/') || !new URL(event.request.url).pathname.endsWith('/preview')) return;
    previewResponses.push({ requestId: event.requestId, method: event.request.method, postData: event.request.postData });
  });
  cdp.on('Network.responseReceived', (event) => {
    const item = previewResponses.find((candidate) => candidate.requestId === event.requestId);
    if (item) item.response = { status: event.response.status, mimeType: event.response.mimeType };
  });
  cdp.on('Network.loadingFinished', (event) => {
    const item = previewResponses.find((candidate) => candidate.requestId === event.requestId);
    if (!item || item.responseBody !== undefined) return;
    const capture = (async () => {
      try {
        const body = await cdp.send('Network.getResponseBody', { requestId: item.requestId });
        const raw = body.base64Encoded ? Buffer.from(body.body, 'base64').toString('utf8') : body.body;
        item.responseBody = JSON.parse(raw);
      } catch (error) { item.responseBodyError = String(error); }
    })().finally(() => pendingBodies.delete(capture));
    pendingBodies.add(capture);
  });

  const action = async (name, operation) => {
    const started = Date.now();
    await operation();
    report.actions.push({ name, elapsedMs: Date.now() - started });
  };
  const captureDOM = async (name) => {
    const path = join(evidenceDir, `${name}.html`);
    await snapshot(cdp, path);
    recordEvidence(report, path);
  };
  const readRows = (rows, names) => (Array.isArray(rows) ? rows : []).map((row) => {
    if (Array.isArray(row)) return Object.fromEntries(names.map((name, index) => [name, row[index]]));
    if (row && typeof row === 'object' && Array.isArray(row.values)) return Object.fromEntries(names.map((name, index) => [name, row.values[index]]));
    return Object.fromEntries(names.map((name) => [name, Object.hasOwn(row ?? {}, name) ? row[name] : undefined]));
  });
  const asArtifactRows = (rows, names, rowIDs) => rows.map((values, index) => ({
    ...(rowIDs?.[index] !== undefined ? { rowId: rowIDs[index] } : {}),
    values: Object.fromEntries(names.map((name) => [name, values[name]])),
  }));
  const rowsByIdentity = (rows, identityColumn) => [...rows].sort((left, right) =>
    String(left.values[identityColumn]).localeCompare(String(right.values[identityColumn])));

  try {
    const explorerId = report.target.bootstrapExplorerId;
    if (!explorerId) throw new Error('J05 fresh fixture bootstrap Explorer identity is missing');
    report.target.explorerId = explorerId;
    const builderURL = `${target.uiUrl}/?project=${encodeURIComponent(target.fixtureProject)}&explorer=${encodeURIComponent(explorerId)}&mode=builder`;
    await navigate(cdp, entryTarget.uiUrl);
    await navigate(cdp, builderURL);
    await waitForBrowser(cdp, `document.body.innerText.includes('Create your first table')`, 60000);
    await captureDOM('j05-empty-builder');

    await action('review-blocker-focuses-owner', async () => {
      await browserEval(cdp, `clickButton('Review dataset')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('#dataset-review-panel')) && document.body.innerText.includes('Create at least one output table before publishing.')`);
      recordAssertion(report, 'j05-empty-review-shows-deterministic-table-blocker', true,
        await evaluate(cdp, `document.querySelector('#dataset-review-panel')?.innerText.includes('Create at least one output table before publishing.') === true`));
      await browserEval(cdp, `clickButton('Create your first table')`);
      await waitForBrowser(cdp, `document.activeElement?.id === 'first-table-name'`);
      recordAssertion(report, 'j05-blocker-action-focuses-owning-control', 'first-table-name',
        await evaluate(cdp, `document.activeElement?.id ?? ''`));
      await captureDOM('j05-blocker-focused-control');
    });

    await action('seed-and-reload-the-same-explorer', async () => {
      const seeded = await seedBootstrapWorkspace(target, explorerId);
      if (!seeded.seeded || !seeded.workspace?.documents?.length) throw new Error('J05 bootstrap helper did not create the expected Patient table on the reviewed Explorer');
      await navigate(cdp, builderURL);
      await waitForBrowser(cdp, `document.body.innerText.includes('Patients') && Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Review dataset'))`, 60000);
      const builder = await fetchBuilderState(target, explorerId);
      const document = builder.workspace?.documents?.find((candidate) => candidate.output?.title === 'Patients');
      if (!document?.output?.id) throw new Error('J05 seeded Patient table has no saved output identity');
      report.target.outputId = document.output.id;
      report.target.builderColumns = document.columns.map((column) => ({ column: column.column, label: column.label, sourcePath: column.source?.field?.path?.replace(/^root\./, '') }));
      recordAssertion(report, 'j05-reloaded-builder-keeps-the-reviewed-explorer-and-three-columns', {
        explorerId,
        columns: 3,
      }, { explorerId: report.target.explorerId, columns: document.columns.length });
      const fieldColumn = (path) => document.columns.find((column) => String(column.source?.field?.path ?? '').replace(/^root\./, '') === path)?.column;
      report.target.columnIds = { id: fieldColumn('id'), family: fieldColumn('name[].family'), gender: fieldColumn('gender') };
      if (Object.values(report.target.columnIds).some((column) => !column)) throw new Error(`J05 seeded table omitted a required Patient field: ${JSON.stringify(report.target.columnIds)}`);
      report.target.previewSourcePaths = report.target.builderColumns.map((column) => column.sourcePath);
      await browserEval(cdp, `clickButton('Advanced graph')`);
      await waitForBrowser(cdp, `document.body.innerText.includes('Current query') && Boolean(document.querySelector('input[aria-label="Use gender as filter"]:not(:disabled)'))`, 30000);
      await browserEval(cdp, `const input = document.querySelector('input[aria-label="Use gender as filter"]'); if (!input || input.disabled) throw new Error('configured gender filter control is unavailable'); input.click();`);
      await captureDOM('j05-builder-gender-filter-configured');
    });

    const builder = await fetchBuilderState(target, explorerId);
    const document = builder.workspace.documents.find((candidate) => candidate.output?.id === report.target.outputId);
    const fixturePatients = readFileSync(join(target.fixtureDir, 'Patient.ndjson'), 'utf8').split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));
    const selectedColumnIDs = document.columns.map((column) => column.column);
    let previewBody;
    let previewExpected;
    let output;
    await action('preview-and-review-saved-output', async () => {
      const previousPreviewCount = previewResponses.length;
      await browserEval(cdp, `clickButton('Preview')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))`, 60000);
      const started = Date.now();
      let previewRequest;
      while (Date.now() - started < 30000) {
        await Promise.allSettled([...pendingBodies]);
        previewRequest = previewResponses.slice(previousPreviewCount).find((item) => item.responseBody !== undefined);
        if (previewRequest) break;
        await sleep(50);
      }
      if (!previewRequest || previewRequest.response?.status !== 200 || !Array.isArray(previewRequest.responseBody?.rows)) {
        throw new Error(`J05 Builder Preview response was unavailable: ${JSON.stringify(previewResponses.slice(previousPreviewCount).map(({ response, responseBodyError }) => ({ response, responseBodyError })))}`);
      }
      previewBody = previewRequest.responseBody;
      const previewColumns = previewBody.columns ?? [];
      const previewColumnIDs = previewColumns.map((column) => column.column);
      const previewRows = readRows(previewBody.rows, previewColumnIDs);
      const representsAuthoredColumn = (column, authoredColumn) =>
        column.column === authoredColumn || column.authoredColumns?.includes(authoredColumn);
      const missingAuthoredColumns = selectedColumnIDs.filter((authoredColumn) =>
        !previewColumns.some((column) => representsAuthoredColumn(column, authoredColumn)));
      recordAssertion(report, 'j05-preview-represents-every-selected-authored-column', [], missingAuthoredColumns);

      const idColumn = previewColumns.find((column) =>
        representsAuthoredColumn(column, report.target.columnIds.id) && column.label === 'id');
      const genderColumn = previewColumns.find((column) =>
        representsAuthoredColumn(column, report.target.columnIds.gender) && column.label === 'gender');
      const familyColumns = previewColumns
        .filter((column) => representsAuthoredColumn(column, report.target.columnIds.family) && /name\[\]\.family \[\d+\]$/.test(column.label))
        .sort((left, right) => left.label.localeCompare(right.label, undefined, { numeric: true }));
      const familyCountColumn = previewColumns.find((column) =>
        representsAuthoredColumn(column, report.target.columnIds.family) && column.logicalType === 'integer' && column.label === 'name__count');
      if (!idColumn || !genderColumn || familyColumns.length !== 2 || !familyCountColumn) {
        throw new Error(`J05 Preview did not expose the expected schema-derived Patient projection: ${JSON.stringify(previewColumns)}`);
      }
      report.target.previewIdentityColumn = idColumn.column;
      previewExpected = asArtifactRows(previewRows, previewColumnIDs);
      const previewLiterals = previewExpected.map((row) => ({
        id: row.values[idColumn.column],
        family: familyColumns.map((column) => row.values[column.column]),
        familyCount: row.values[familyCountColumn.column],
        gender: row.values[genderColumn.column],
      })).sort((left, right) => String(left.id).localeCompare(String(right.id)));
      const fixtureLiterals = fixturePatients.map((patient) => ({
        id: patient.id,
        family: Array.from({ length: familyColumns.length }, (_, index) => patient.name?.[index]?.family ?? null),
        familyCount: patient.name?.length ?? 0,
        gender: patient.gender ?? null,
      })).sort((left, right) => String(left.id).localeCompare(String(right.id)));
      recordAssertion(report, 'j05-preview-preserves-fixture-literal-values-and-patient-identity', fixtureLiterals, previewLiterals);
      report.target.preview = { columns: previewColumns.map((column) => ({ column: column.column, label: column.label })), rows: previewExpected };
      await captureDOM('j05-preview');

      await browserEval(cdp, `clickButton('Review dataset')`);
      await waitForBrowser(cdp, `document.querySelector('#dataset-review-panel')?.innerText.includes('No blocking issues are currently reported for these saved tables.') === true`, 30000);
      const reviewText = String(await evaluate(cdp, `document.querySelector('#dataset-review-panel')?.innerText ?? ''`));
      recordAssertion(report, 'j05-saved-table-review-has-no-blockers', true,
        reviewText.includes('No blocking issues are currently reported for these saved tables.')
        && reviewText.includes('Patients')
        && document.columns.every((column) => reviewText.includes(column.label)));
      await captureDOM('j05-dataset-review');
      await browserEval(cdp, `clickButton('Close review')`);
    });

    await action('publish-and-prove-current-revision', async () => {
      await browserEval(cdp, `clickButton('Publish')`);
      const started = Date.now();
      let state;
      let runtime;
      while (Date.now() - started < 60000) {
        try {
          state = await fetchExplorerState(target, explorerId);
          runtime = state.runtime ?? state;
          if (runtime.outputs?.length && (state.active?.revisionId || state.publication?.revisionId || runtime.publication?.revisionId)) break;
        } catch { /* publication becomes readable after the durable publish response */ }
        await sleep(300);
      }
      output = runtime?.outputs?.find((candidate) => candidate.outputId === report.target.outputId);
      if (!output) throw new Error(`J05 published Explorer runtime omitted output ${report.target.outputId}`);
      const revisionId = state.active?.revisionId ?? state.publication?.revisionId ?? runtime.publication?.revisionId;
      const resolvedSchemaDigest = runtime.schema?.digest ?? state.generated?.dataset?.schemaDigest ?? state.generated?.resolvedSchemaDigest;
      if (typeof resolvedSchemaDigest !== 'string' || !resolvedSchemaDigest) throw new Error('J05 published Explorer state omitted the current resolved schema digest');
      report.target.publication = { revisionId, resolvedSchemaDigest, outputId: output.outputId, outputs: runtime.outputs.map((candidate) => ({ outputId: candidate.outputId, columns: candidate.columns.map((column) => ({ column: column.column, label: column.label, logicalType: column.logicalType, repeated: column.repeated })) })) };
      recordAssertion(report, 'j05-publish-keeps-the-reviewed-output-and-physical-column-order', {
        outputId: report.target.outputId,
        columns: (previewBody.columns ?? []).map((column) => column.column),
      }, { outputId: output.outputId, columns: output.columns.map((column) => column.column) });
      recordAssertion(report, 'j05-published-viewer-exposes-the-authored-gender-facet', true,
        (output.filters ?? []).some((binding) => binding.column === report.target.columnIds.gender));
      const publishedRows = await graphQLRows(target, output.selector, output.columns.map((column) => column.column));
      if (!publishedRows?.materialization?.id) throw new Error('J05 published output did not produce a readable materialization');
      recordAssertion(report, 'j05-viewer-query-uses-the-current-fixture-generation-and-columns', {
        generation: target.fixtureGeneration,
        columns: output.columns.map((column) => column.column),
        totalCount: fixturePatientsCount(target.fixtureDir),
      }, {
        generation: publishedRows.materialization.datasetGeneration,
        columns: publishedRows.columns,
        totalCount: publishedRows.totalCount,
      });
      const queryRows = readRows(publishedRows.rows, output.columns.map((column) => column.column)).map((row) =>
        Object.fromEntries(output.columns.map((column) => [column.column, normalizeJ05LogicalValue(row[column.column], column)])));
      if (!Array.isArray(publishedRows.rowIds) || publishedRows.rowIds.length !== queryRows.length || publishedRows.rowIds.some((rowId) => rowId === undefined || rowId === null)) throw new Error('J05 unfiltered Viewer query omitted stable row identity values');
      recordAssertion(report, 'j05-unfiltered-viewer-row-identities-are-stable-and-distinct', true,
        new Set(publishedRows.rowIds.map((rowId) => JSON.stringify(rowId))).size === publishedRows.rowIds.length);
      report.target.publishedRows = asArtifactRows(queryRows, output.columns.map((column) => column.column), publishedRows.rowIds);
      const previewSelected = previewExpected.map((row) => ({ values: Object.fromEntries(output.columns.map((column) => [column.column, row.values[column.column]])) }));
      assertJ05ArtifactRows({ rows: report.target.publishedRows }, previewSelected);
      recordAssertion(
        report,
        'j05-preview-and-unfiltered-viewer-retain-exact-literal-rows',
        rowsByIdentity(previewSelected, report.target.previewIdentityColumn).map((row) => row.values),
        rowsByIdentity(report.target.publishedRows, report.target.previewIdentityColumn).map((row) => row.values),
      );
      report.target.materializationId = publishedRows.materialization.id;
    });

    await action('reload-viewer-explain-and-filter', async () => {
      await browserEval(cdp, `clickButton('Viewer')`);
      await waitForBrowser(cdp, `document.body.innerText.includes('Published') && document.body.innerText.includes('dev-patient-001') && document.body.innerText.includes('dev-patient-002')`, 60000);
      await cdp.send('Page.reload', { ignoreCache: false });
      await waitForBrowser(cdp, `document.readyState === 'complete'`);
      await waitForBrowser(cdp, `document.body.innerText.includes('Published') && document.body.innerText.includes('dev-patient-001') && document.body.innerText.includes('dev-patient-002')`, 60000);
      recordAssertion(report, 'j05-published-viewer-data-survives-reload', ['dev-patient-001', 'dev-patient-002'], await evaluate(cdp, `['dev-patient-001', 'dev-patient-002'].filter((id) => document.body.innerText.includes(id))`));
      const tableBeforeFilter = await evaluate(cdp, `(() => { const table = document.querySelector('table[aria-label$=" results"]'); return { headers: [...(table?.querySelectorAll('thead th') || [])].map((cell) => cell.textContent.trim()), ids: [...(table?.querySelectorAll('tbody tr') || [])].map((row) => row.querySelector('td')?.textContent.trim()) }; })()`);
      recordAssertion(report, 'j05-viewer-renders-all-published-patient-rows', report.target.publishedRows.map((row) => row.values[report.target.columnIds.id]).sort(), tableBeforeFilter.ids.sort());

      const familyColumn = outputColumnForPath(builder, output, 'name[].family');
      await browserEval(cdp, `
        const row = [...document.querySelectorAll('table[aria-label$=" results"] tbody tr')]
          .find((candidate) => norm(candidate.querySelector('td')?.textContent) === 'dev-patient-001');
        const explain = [...(row?.querySelectorAll('button[aria-label]') || [])]
          .find((candidate) => candidate.getAttribute('aria-label')?.startsWith(${JSON.stringify(`Explain ${familyColumn.label} for row `)}));
        if (!explain) throw new Error('family evidence cell was not found for dev-patient-001');
        explain.click();
      `);
      await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('[role="dialog"]')].find((dialog) => dialog.innerText.includes(${JSON.stringify(familyColumn.label)})))`, 30000);
      const explanationText = String(await evaluate(cdp, `(() => [...document.querySelectorAll('[role="dialog"]')].map((dialog) => dialog.innerText).find((text) => text.includes(${JSON.stringify(familyColumn.label)}) ) ?? '')()`));
      recordAssertion(report, 'j05-viewer-explains-a-rendered-cell-with-fixture-evidence', true,
        explanationText.includes('Example') || explanationText.includes('dev-patient-001'));
      report.target.cellExplanation = { column: familyColumn.label, containsFixtureEvidence: explanationText.includes('Example') || explanationText.includes('dev-patient-001') };
      await captureDOM('j05-viewer-cell-explanation');
      await browserEval(cdp, `clickButton('Close cell explanation')`);

      await browserEval(cdp, `clickButton('Load values')`);
      await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('label')].find((candidate) => candidate.textContent.trim().startsWith('female')))`, 30000);
      await browserEval(cdp, `clickFacetValue('female')`);
      await waitForBrowser(cdp, `document.querySelector('table[aria-label$=" results"] tbody')?.querySelectorAll('tr').length === 1`, 30000);
      const filteredIDs = await evaluate(cdp, `([...document.querySelectorAll('table[aria-label$=" results"] tbody tr')]).map((row) => row.querySelector('td')?.textContent.trim())`);
      recordAssertion(report, 'j05-viewer-female-filter-selects-only-matching-patient', ['dev-patient-001'], filteredIDs);
      recordAssertion(report, 'j05-viewer-stays-in-viewer-mode-after-filtering', 'viewer', await evaluate(cdp, `new URL(window.location.href).searchParams.get('mode')`));
      await captureDOM('j05-viewer-filtered');
    });

    let modal;
    await action('prepare-download-zip-and-inspect-package', async () => {
      await browserEval(cdp, `clickButton('Download dataset')`);
      await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('[role="dialog"]')].find((dialog) => dialog.innerText.includes('Download dataset') && dialog.querySelector('[aria-label="Declared output types"]')))`, 60000);
      modal = await evaluate(cdp, `(() => { const dialog = [...document.querySelectorAll('[role="dialog"]')].find((candidate) => candidate.innerText.includes('Download dataset') && candidate.querySelector('[aria-label="Declared output types"]')); const value = (label) => [...(dialog?.querySelectorAll('dt') || [])].find((term) => term.textContent.trim() === label)?.nextElementSibling?.textContent.trim() ?? ''; return { text: dialog?.innerText ?? '', representation: value('Representation'), sourceGeneration: value('Source generation'), schemaDigest: value('Schema digest'), types: dialog?.querySelector('[aria-label="Declared output types"]')?.innerText ?? '' }; })()`);
      recordAssertion(report, 'j05-download-modal-declares-complete-authorized-scope', true,
        modal.text.includes('complete authorized population'));
      recordAssertion(report, 'j05-download-modal-shows-current-generation-and-schema-digest', true,
        modal.sourceGeneration === target.fixtureGeneration && /^[a-f0-9]{64}$/.test(modal.schemaDigest));
      report.target.artifactPreparation = { datasetGeneration: modal.sourceGeneration, schemaDigest: modal.schemaDigest };
      recordAssertion(report, 'j05-download-modal-shows-exact-declared-column-count-and-types', true,
        modal.text.includes(`${output.columns.length} declared output columns`)
        && output.columns.every((column) => modal.types.includes(column.label)));
      await captureDOM('j05-dataset-download-modal');
      await browserEval(cdp, `const link = [...document.querySelectorAll('a[download]')].find((candidate) => norm(candidate.textContent) === 'Download ZIP'); if (!link) throw new Error('download link not found: Download ZIP'); link.click();`);
      const archivePath = await findDownloadedArchive(downloadDir, 60000);
      recordEvidence(report, archivePath);
      report.target.artifact = { path: archivePath, bytes: statSync(archivePath).size };
      const artifact = inspectJ05ArtifactPackage(readStoredZip(archivePath));
      const expectedIdentity = {
        project: canonicalProjectID(target.fixtureProject),
        datasetGeneration: target.fixtureGeneration,
        outputId: report.target.outputId,
        revisionId: report.target.publication.revisionId,
        schemaDigest: modal.schemaDigest,
      };
      assertJ05ArtifactIdentity(artifact, expectedIdentity);
      recordAssertion(report, 'j05-artifact-identity-is-current-not-a-stale-modal-result', true,
        j05ArtifactIdentityIsCurrent(artifact.manifest.identity, expectedIdentity));
      recordAssertion(report, 'j05-artifact-manifest-is-bound-to-current-publication-identity', expectedIdentity, Object.fromEntries(Object.keys(expectedIdentity).map((field) => [field, artifact.manifest.identity[field]])));
      const representation = artifact.manifest.format === 'CSV' ? 'Typed scalar CSV in a ZIP archive' : 'Typed JSON Lines in a ZIP archive';
      recordAssertion(report, 'j05-artifact-representation-matches-modal', representation, modal.representation);
      recordAssertion(report, 'j05-artifact-generation-schema-and-scope-match-preview', {
        generation: target.fixtureGeneration,
        schemaDigest: modal.schemaDigest,
        scope: 'complete authorized population',
        rows: report.target.publishedRows.length,
      }, {
        generation: artifact.manifest.identity.datasetGeneration,
        schemaDigest: artifact.manifest.identity.schemaDigest,
        scope: modal.text.includes('complete authorized population') ? 'complete authorized population' : 'filtered or unspecified',
        rows: artifact.manifest.rows,
      });
      recordAssertion(report, 'j05-artifact-selection-metadata-is-bound-to-fixture-generation', target.fixtureGeneration,
        artifact.manifest.selection?.datasetGeneration);
      recordAssertion(report, 'j05-artifact-schema-matches-published-output-columns', output.columns.map((column) => ({ name: column.column, label: column.label })), artifact.schema.columns.map((column) => ({ name: column.name, label: output.columns.find((candidate) => candidate.column === column.name)?.label })));
      recordAssertion(report, 'j05-artifact-declared-types-match-download-modal', true,
        artifact.schema.columns.every((column) => {
          const label = output.columns.find((candidate) => candidate.column === column.name)?.label ?? column.name;
          return modal.types.includes(label) && modal.types.includes(`${column.logicalType}${column.repeated ? ' · repeated' : ''}`);
        }));
      const familySchema = artifact.schema.columns.find((column) => column.sourcePath === 'name[].family');
      recordAssertion(report, 'j05-artifact-preserves-fixture-column-contributor-lineage', true,
        Boolean(familySchema?.sourcePath?.includes('name[].family') && familySchema.sourceResourceType === 'Patient' && familySchema.candidateId && familySchema.occurrenceId));
      const artifactRows = artifact.rows.map((row) => ({ rowId: row.rowId, values: row.values }));
      const previewByArtifactSchema = readRows(previewBody.rows, (previewBody.columns ?? []).map((column) => column.column));
      const previewArtifactRows = asArtifactRows(previewByArtifactSchema, artifact.schema.columns.map((column) => column.name));
      assertJ05ArtifactRows(artifact, previewArtifactRows);
      recordAssertion(report, 'j05-artifact-preserves-exact-preview-literal-values', rowsByIdentity(previewArtifactRows, report.target.previewIdentityColumn).map((row) => row.values), rowsByIdentity(artifactRows, report.target.previewIdentityColumn).map((row) => row.values));
      const viewerArtifactRows = report.target.publishedRows.map((row) => ({
        ...(artifact.manifest.format === 'JSONL' ? { rowId: row.rowId } : {}),
        values: Object.fromEntries(artifact.schema.columns.map((column) => [column.name, row.values[column.name]])),
      }));
      assertJ05ArtifactRows(artifact, viewerArtifactRows);
      recordAssertion(report, 'j05-artifact-preserves-exact-unfiltered-viewer-values-and-row-identities', rowsByIdentity(viewerArtifactRows, report.target.previewIdentityColumn), rowsByIdentity(artifactRows, report.target.previewIdentityColumn));
      const sourceEvidence = artifact.provenance;
      recordAssertion(report, 'j05-artifact-provenance-retains-fixture-project-and-publication-identities', true,
        sourceEvidence.project === artifact.manifest.identity.project
        && sourceEvidence.datasetGeneration === target.fixtureGeneration
        && sourceEvidence.schemaDigest === modal.schemaDigest
        && sourceEvidence.executionId === artifact.manifest.identity.executionId
        && sourceEvidence.revisionId === artifact.manifest.identity.revisionId
        && sourceEvidence.outputId === artifact.manifest.identity.outputId);
      report.target.export = { format: artifact.manifest.format, dataMember: artifact.dataName, rows: artifact.manifest.rows, features: artifact.manifest.features, schemaDigest: artifact.manifest.identity.schemaDigest };
      await captureDOM('j05-viewer-download-complete');
    });

    await cdp.send('Page.reload', { ignoreCache: false });
    await waitForBrowser(cdp, `document.readyState === 'complete'`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Published') && document.body.innerText.includes('dev-patient-001')`, 60000);
    recordAssertion(report, 'j05-filtered-viewer-reloads-to-published-rows', true,
      await evaluate(cdp, `document.body.innerText.includes('dev-patient-001') && document.body.innerText.includes('Published')`));
    await captureDOM('j05-viewer-after-reload');
  } finally {
    try {
      await Promise.allSettled([...pendingBodies]);
      await captureDOM('j05-final');
    } catch { /* preserve the primary failure */ }
    await browser.close();
  }
};

const fixturePatientsCount = (fixtureDir) => readFileSync(join(fixtureDir, 'Patient.ndjson'), 'utf8').split(/\r?\n/).filter(Boolean).length;

const outputColumnForPath = (builder, output, path) => {
  const document = builder.workspace?.documents?.find((candidate) => candidate.output?.id === output.outputId);
  const authored = document?.columns?.find((column) => String(column.source?.field?.path ?? '').replace(/^root\./, '') === path);
  const runtime = output.columns.find((column) =>
    column.column === authored?.column || column.label === authored?.label || column.label.startsWith(`${authored?.label} [`));
  if (!runtime) throw new Error(`J05 published output omitted its ${path} column`);
  return runtime;
};

const verifyJ01ExternalBrowserScenario = async (target, report, entryTarget, externalManifest) => {
  const runID = `j01-cda-${Date.now().toString(36)}-${randomUUID().slice(0, 8)}`;
  const evidenceDir = join(target.artifacts, runID);
  const downloadDir = join(evidenceDir, 'downloads');
  mkdirSync(downloadDir, { recursive: true, mode: 0o700 });
  report.target.evidenceDirectory = evidenceDir;
  report.target.ports = { api: target.apiPort, ui: target.uiPort };
  report.target.externalManifest = externalManifest.summary;
  report.target.fixtureSourceDigest = externalManifest.summary.sourceSHA256;
  report.actions = [];
  report.requests = [];
  report.target.externalTasks = [];
  recordEvidence(report, evidenceDir);
  const manifestPath = join(evidenceDir, 'external-manifest.json');

  const repeatedSource = externalManifest.files.find((file) => file.name === 'Observation.ndjson');
  const repeatedRecord = repeatedSource?.repeatedComponentRecord;
  const repeatedResource = repeatedSource?.records.find((record) => record.id === repeatedRecord?.id);
  if (!repeatedSource || !repeatedRecord || !repeatedResource) throw new Error('CDA J01 manifest has no selected repeated Observation record');
  const repeatedJSON = repeatedSource.contents.toString('utf8').split(/\r?\n/).filter(Boolean)
    .map((line) => JSON.parse(line)).find((resource) => resource.id === repeatedRecord.id);
  const repeatedFeature = (repeatedJSON?.component ?? []).flatMap((component) => component.code?.coding ?? [])
    .find((coding) => typeof coding.system === 'string' && coding.system && typeof coding.code === 'string' && coding.code);
  if (!repeatedFeature) throw new Error(`CDA repeated Observation ${repeatedRecord.id} has no coded repeated feature`);
  report.target.cdaSelection = {
    sourceSHA256: externalManifest.summary.sourceSHA256,
    selectedFiles: externalManifest.summary.sourceFiles.map((file) => ({
      name: file.name,
      selectedCount: file.records.length,
      firstSourceLine: file.records[0]?.sourceLine,
      lastSourceLine: file.records.at(-1)?.sourceLine,
      selectedContentSHA256: file.selectedContentSHA256,
      recordIDs: file.records.map((record) => record.id),
    })),
    repeatedObservation: {
      id: repeatedRecord.id,
      sourceLine: repeatedRecord.sourceLine,
      componentCount: repeatedRecord.componentCount,
      feature: { system: repeatedFeature.system, code: repeatedFeature.code, display: repeatedFeature.display ?? '' },
    },
  };
  writeJSON(manifestPath, { ...externalManifest.summary, selectedRepeatedFeature: report.target.cdaSelection.repeatedObservation.feature });
  recordEvidence(report, manifestPath);

  const browser = await launchBrowser(downloadDir);
  const cdp = browser.cdp;
  const network = [];
  cdp.on('Network.requestWillBeSent', (event) => {
    if (!event.request.url.includes('/authoring/v2/')) return;
    network.push({
      requestId: event.requestId,
      path: new URL(event.request.url).pathname,
      method: event.request.method,
      postData: event.request.postData,
      startedAt: event.timestamp,
      response: undefined,
    });
  });
  cdp.on('Network.responseReceived', (event) => {
    const item = network.find((candidate) => candidate.requestId === event.requestId);
    if (item) item.response = { status: event.response.status, mimeType: event.response.mimeType };
  });

  const action = async (name, operation) => {
    const started = Date.now();
    await operation();
    report.actions.push({ name, elapsedMs: Date.now() - started });
  };
  const saveDOM = async (name) => {
    const path = join(evidenceDir, `${name}.html`);
    await snapshot(cdp, path);
    recordEvidence(report, path);
  };
  const saveScreenshot = async (name) => {
    const screenshot = await cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
    const path = join(evidenceDir, `${name}.png`);
    writeFileSync(path, Buffer.from(screenshot.data, 'base64'), { mode: 0o600 });
    recordEvidence(report, path);
  };
  const readState = (explorerId) => fetchBuilderState(target, explorerId);
  const bootstrapExplorerId = report.target.bootstrapExplorerId;
  const entryURL = `${entryTarget.uiUrl}/?project=${encodeURIComponent(target.fixtureProject)}&explorer=${encodeURIComponent(bootstrapExplorerId ?? '')}&mode=builder`;
  const openBuilderEntry = async () => {
    if (!bootstrapExplorerId) throw new Error('CDA J01 bootstrap Explorer identity is missing');
    await navigate(cdp, entryURL);
    await waitForBrowser(cdp, `document.body.innerText.includes('Build your features') || document.body.innerText.includes('Create your first table')`, 60000);
  };
  const waitForState = async (explorerId, predicate, label, timeout = 30000) => {
    const started = Date.now();
    let state;
    while (Date.now() - started < timeout) {
      state = await readState(explorerId);
      if (predicate(state)) return state;
      await sleep(200);
    }
    throw new Error(`timed out waiting for CDA J01 Builder state: ${label}; draft=${state?.draftVersion}`);
  };
  const createBlankExplorer = async (title) => {
    await openBuilderEntry();
    await browserEval(cdp, `clickText('summary', 'New explorer')`);
    await browserEval(cdp, `setInput('new-explorer-name', ${JSON.stringify(title)})`);
    await browserEval(cdp, `clickButton('Create blank')`);
    await waitForBrowser(cdp, `document.querySelector('select[aria-label="Explorer"] option:checked')?.textContent.trim() === ${JSON.stringify(title)} && document.body.innerText.includes('Create your first table')`);
    const explorerId = await evaluate(cdp, `document.querySelector('select[aria-label="Explorer"]')?.value || ''`);
    if (!explorerId) throw new Error(`CDA J01 blank Explorer has no identity: ${title}`);
    return explorerId;
  };
  const createTableRoot = async (explorerId, resourceType, title) => {
    const before = await readState(explorerId);
    recordAssertion(report, `j01-cda-${resourceType.toLowerCase()}-starts-with-empty-new-explorer`, {
      lifecycleState: 'NEW', draftVersion: 0, workspace: null,
    }, { lifecycleState: before.lifecycleState, draftVersion: before.draftVersion, workspace: before.workspace });
    await browserEval(cdp, `setInput('first-table-name', ${JSON.stringify(title)})`);
    await browserEval(cdp, `clickButton('Create table')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('What should one row represent?') && Boolean(document.querySelector('button[aria-label="Choose ${resourceType} rows"]'))`);
    await browserEval(cdp, `clickButton('Choose ${resourceType} rows')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('[aria-label="Search features by field name, concept, or code"]'))`, 60000);
    const state = await waitForState(explorerId, (value) => value.workspace?.documents?.length === 1 && value.workspace.documents[0].rootResourceType === resourceType, `${resourceType} root selection`);
    const table = state.workspace.documents[0];
    recordAssertion(report, `j01-cda-${resourceType.toLowerCase()}-persists-records-row-definition`,
      { kind: 'RECORDS', records: {} }, table.rows);
    recordAssertion(report, `j01-cda-${resourceType.toLowerCase()}-starts-with-zero-columns`, 0, table.columns.length);
    recordAssertion(report, `j01-cda-${resourceType.toLowerCase()}-does-not-open-advanced-graph`, false, await evaluate(cdp, `Boolean(document.querySelector('.react-flow__node'))`));
    return { state, table };
  };
  const chooseFeature = async ({ explorerId, outputId, query, checkboxLabel, columnCount, label, ownerRecords = false, articleMatch }) => {
    await browserEval(cdp, `setInput('Search features by field name, concept, or code', ${JSON.stringify(query)})`);
    await browserEval(cdp, `clickButton('Search')`);
    await waitForBrowser(cdp, articleMatch
      ? `Boolean([...document.querySelectorAll('article')].find((article) => article.textContent.includes(${JSON.stringify(articleMatch)}) && article.querySelector('input[type="checkbox"]:not(:disabled)')))`
      : `Boolean(document.querySelector('input[aria-label="${checkboxLabel}"]:not(:disabled)'))`, 60000);
    if (articleMatch) {
      await browserEval(cdp, `(() => { const article = [...document.querySelectorAll('article')].find((candidate) => candidate.textContent.includes(${JSON.stringify(articleMatch)})); const input = article?.querySelector('input[type="checkbox"]'); if (!input || input.disabled) throw new Error('CDA J01 exact semantic feature is unavailable'); input.click(); })()`);
    } else {
      await browserEval(cdp, `const input = inputByLabel(${JSON.stringify(checkboxLabel)}); if (!input || input.disabled) throw new Error('CDA J01 field choice is unavailable: ' + ${JSON.stringify(checkboxLabel)}); input.click();`);
    }
    await browserEval(cdp, `clickButton('Add 1 selected feature')`);
    if (ownerRecords) {
      const formLabel = `${label}: Keep each matching record`;
      await waitForBrowser(cdp, `Boolean(document.querySelector('[role="dialog"]') && document.querySelector('input[aria-label="${formLabel}"]'))`);
      await browserEval(cdp, `const form = inputByLabel(${JSON.stringify(formLabel)}); if (!form) throw new Error('CDA J01 OWNER_RECORDS form is unavailable'); form.click();`);
      await browserEval(cdp, `clickButton('Add 1 selected feature')`);
    }
    return waitForState(explorerId, (value) => value.workspace?.documents?.find((document) => document.output?.id === outputId)?.columns.length === columnCount, `${label} selection`);
  };
  const columnSnapshot = (state, outputId) => {
    const document = state.workspace?.documents?.find((candidate) => candidate.output?.id === outputId);
    if (!document) throw new Error(`CDA J01 output is missing from Builder: ${outputId}`);
    return document.columns.map((column) => ({
      id: column.column,
      label: column.label,
      order: column.table?.order,
      kind: column.source?.kind,
      fieldPath: column.source?.field?.path,
      system: column.source?.ownerRecords?.key?.system,
      code: column.source?.ownerRecords?.key?.code,
      ownerPath: column.source?.ownerRecords?.binding?.ownerPath,
      valuePath: column.source?.ownerRecords?.binding?.valuePath,
      logicalType: column.logicalType,
    }));
  };
  const waitForNewArchive = async (before) => {
    const started = Date.now();
    while (Date.now() - started < 60000) {
      for (const name of readdirSync(downloadDir).filter((file) => file.endsWith('.zip')).sort()) {
        const path = join(downloadDir, name);
        const modifiedMs = statSync(path).mtimeMs;
        if (before.get(path) === modifiedMs || existsSync(`${path}.crdownload`)) continue;
        return path;
      }
      await sleep(200);
    }
    throw new Error('CDA J01 Viewer did not download a new training artifact');
  };
  const waitForAuthoringResponse = async (pathSuffix, afterIndex) => {
    const started = Date.now();
    while (Date.now() - started < 90000) {
      const item = network.find((candidate, index) => index >= afterIndex && candidate.path.endsWith(pathSuffix) && candidate.response);
      if (item) return item;
      await sleep(50);
    }
    throw new Error(`CDA J01 authoring response did not complete for ${pathSuffix}`);
  };
  const publishAndExport = async ({ resourceType, title, explorerId, outputId, columns, preview }) => {
    const startIndex = network.length;
    await browserEval(cdp, `clickButton('Publish')`);
    const publishRequest = await waitForAuthoringResponse('/publish', startIndex);
    await waitForBrowser(cdp, `!document.querySelector('button[aria-busy="true"]')`, 90000);
    recordAssertion(report, `j01-cda-${resourceType.toLowerCase()}-publish-returns-200`, 200, publishRequest.response.status);
    let explorerState;
    let runtime;
    const publishStarted = Date.now();
    while (Date.now() - publishStarted < 90000) {
      try {
        explorerState = await fetchExplorerState(target, explorerId);
        runtime = explorerState.runtime ?? explorerState;
        if (runtime.outputs?.some((output) => output.outputId === outputId)) break;
      } catch {}
      await sleep(300);
    }
    const output = runtime?.outputs?.find((candidate) => candidate.outputId === outputId);
    if (!output) throw new Error(`CDA J01 ${resourceType} publication omitted output ${outputId}`);
    recordAssertion(report, `j01-cda-${resourceType.toLowerCase()}-publishes-exact-column-identities`, columns.map((column) => column.id), output.columns.map((column) => column.column));
    const revisionId = explorerState.active?.revisionId ?? explorerState.publication?.revisionId;
    if (!revisionId) throw new Error(`CDA J01 ${resourceType} publication omitted its revision identity`);
    const builderAfterPublish = await readState(explorerId);
    recordAssertion(report, `j01-cda-${resourceType.toLowerCase()}-publish-preserves-draft-identities`, columns, columnSnapshot(builderAfterPublish, outputId));

    await browserEval(cdp, `clickButton('Viewer')`);
    const viewerURL = `${target.uiUrl}/?project=${encodeURIComponent(target.fixtureProject)}&explorer=${encodeURIComponent(explorerId)}&mode=viewer`;
    await navigate(cdp, viewerURL);
    await waitForBrowser(cdp, `Boolean(document.querySelector('table[aria-label="${title} results"]')) && [...document.querySelectorAll('button')].some((button) => ['Download training artifact', 'Download dataset'].includes(button.textContent.trim()))`, 90000);
    const artifactDownloadPlan = j01ArtifactDownloadPlan(await evaluate(cdp, `[...document.querySelectorAll('button')].map((button) => button.textContent.trim())`));
    recordAssertion(report, `j01-cda-${resourceType.toLowerCase()}-viewer-exposes-artifact-download`,
      true, ['Download training artifact', 'Download dataset'].includes(artifactDownloadPlan.triggerLabel));
    const viewerRows = await evaluate(cdp, `(() => {
      const table = document.querySelector('table[aria-label="${title} results"]');
      const rows = [...(table?.querySelectorAll('tbody tr') || [])];
      return {
        headers: [...(table?.querySelectorAll('thead th') || [])].map((cell) => cell.textContent.trim()),
        rows: rows.map((row) => [...row.querySelectorAll('td')].map((cell) => cell.textContent.trim())),
      };
    })()`);
    recordAssertion(report, `j01-cda-${resourceType.toLowerCase()}-viewer-renders-published-column-labels`, columns.map((column) => column.label), viewerRows.headers);
    if (viewerRows.rows.length === 0) throw new Error(`CDA J01 ${resourceType} Viewer rendered no published rows`);
    await saveDOM(`j01-cda-${resourceType.toLowerCase()}-viewer`);

    const before = new Map(readdirSync(downloadDir).filter((name) => name.endsWith('.zip')).map((name) => {
      const path = join(downloadDir, name);
      return [path, statSync(path).mtimeMs];
    }));
    let modalSchemaDigest;
    if (artifactDownloadPlan.confirmationLabel) {
      await browserEval(cdp, `clickButton(${JSON.stringify(artifactDownloadPlan.triggerLabel)})`);
      await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('[role="dialog"]')].find((dialog) => dialog.innerText.includes('Download dataset') && dialog.querySelector('[aria-label="Declared output types"]')))`, 60000);
      const modal = await evaluate(cdp, `(() => {
        const dialog = [...document.querySelectorAll('[role="dialog"]')].find((candidate) => candidate.innerText.includes('Download dataset') && candidate.querySelector('[aria-label="Declared output types"]'));
        const value = (label) => [...(dialog?.querySelectorAll('dt') || [])].find((term) => term.textContent.trim() === label)?.nextElementSibling?.textContent.trim() ?? '';
        return {
          text: dialog?.innerText ?? '',
          sourceGeneration: value('Source generation'),
          schemaDigest: value('Schema digest'),
          types: dialog?.querySelector('[aria-label="Declared output types"]')?.innerText ?? '',
        };
      })()`);
      const modalMatchesPublication = modal.sourceGeneration === target.fixtureGeneration
        && modal.text.includes(`${columns.length} declared output columns`)
        && columns.every((column) => modal.types.includes(column.label));
      recordAssertion(report, `j01-cda-${resourceType.toLowerCase()}-artifact-modal-matches-published-output`, true, modalMatchesPublication);
      if (!/^[a-f0-9]{64}$/.test(modal.schemaDigest)) throw new Error(`CDA J01 ${resourceType} artifact modal has an invalid schema digest`);
      modalSchemaDigest = modal.schemaDigest;
      report.target[`${resourceType.toLowerCase()}ArtifactModal`] = {
        sourceGeneration: modal.sourceGeneration,
        schemaDigest: modal.schemaDigest,
        declaredColumns: columns.map((column) => ({ label: column.label, logicalType: column.logicalType })),
      };
      await saveDOM(`j01-cda-${resourceType.toLowerCase()}-artifact-modal`);
      await browserEval(cdp, `(() => {
        const link = [...document.querySelectorAll('a[download]')].find((candidate) => candidate.textContent.trim() === ${JSON.stringify(artifactDownloadPlan.confirmationLabel)});
        if (!link) throw new Error('CDA J01 artifact modal has no Download ZIP link');
        link.click();
      })()`);
    } else {
      await browserEval(cdp, `clickButton(${JSON.stringify(artifactDownloadPlan.triggerLabel)})`);
    }
    const archivePath = await waitForNewArchive(before);
    recordEvidence(report, archivePath);
    const archive = readStoredZip(archivePath);
    const idColumn = columns.find((column) => column.fieldPath?.replace(/^root\./, '') === 'id');
    if (!idColumn) throw new Error(`CDA J01 ${resourceType} output has no stable id column`);
    const artifact = inspectJ01ArtifactRows(archive, idColumn.id);
    const { schema, manifest: artifactManifest, dataName, rowsByID: exportedByID } = artifact;
    const schemaColumnNames = schema.columns.map((column) => column.name);
    const exportedFeatureColumns = schema.columns.filter((column) => column.name !== 'project_id');
    recordAssertion(report, `j01-cda-${resourceType.toLowerCase()}-artifact-has-exact-typed-selected-columns`,
      columns.map((column) => column.id).sort(), exportedFeatureColumns.map((column) => column.name).sort());
    recordAssertion(report, `j01-cda-${resourceType.toLowerCase()}-artifact-data-member-matches-format`,
      artifactManifest.format === 'CSV' ? 'data.csv' : 'data.jsonl', dataName);
    if (artifactManifest.format === 'CSV') {
      const csvHeader = parseArtifactCSV(archive.get(dataName).toString('utf8'))[0]?.map((cell) => cell.value) ?? [];
      recordAssertion(report, `j01-cda-${resourceType.toLowerCase()}-artifact-header-matches-schema`, schemaColumnNames, csvHeader);
    } else {
      recordAssertion(report, `j01-cda-${resourceType.toLowerCase()}-artifact-jsonl-normalizes-schema-output-keys`, true,
        artifact.rows.every((row) => Object.keys(row.values).every((name) => schemaColumnNames.includes(name))));
    }
    recordAssertion(report, `j01-cda-${resourceType.toLowerCase()}-artifact-exposes-logical-types`, true, schema.columns.every((column) => typeof column.logicalType === 'string' && column.logicalType.length > 0));
    recordAssertion(report, `j01-cda-${resourceType.toLowerCase()}-artifact-binds-publication-identities`, {
      project: canonicalProjectID(target.fixtureProject), generation: target.fixtureGeneration, outputId, revisionId,
    }, {
      project: artifactManifest.identity.project, generation: artifactManifest.identity.datasetGeneration,
      outputId: artifactManifest.identity.outputId, revisionId: artifactManifest.identity.revisionId,
    });
    if (modalSchemaDigest) {
      recordAssertion(report, `j01-cda-${resourceType.toLowerCase()}-artifact-schema-digest-matches-modal`,
        modalSchemaDigest, artifactManifest.identity.schemaDigest);
    }
    const expectedIDs = preview.rows.map((row) => row[idColumn.id]).sort();
    const exportedIDs = [...exportedByID.keys()].sort();
    recordAssertion(report, `j01-cda-${resourceType.toLowerCase()}-artifact-preserves-preview-row-membership`, expectedIDs, exportedIDs);
    recordAssertion(report, `j01-cda-${resourceType.toLowerCase()}-artifact-row-count-matches-preview`, preview.rows.length, artifactManifest.rows);
    const previewByID = new Map(preview.rows.map((row) => [row[idColumn.id], row]));
    const columnsMatch = [...previewByID].every(([id, previewRow]) => {
      const artifactRow = exportedByID.get(id);
      return Boolean(artifactRow && columns.every((column) => j01JSONValuesEquivalent(
        artifactRow[column.id], previewRow[column.id] ?? null,
      )));
    });
    recordAssertion(report, `j01-cda-${resourceType.toLowerCase()}-typed-artifact-values-match-preview`, true, columnsMatch);
    const viewerMatchesArtifacts = viewerRows.rows.every((values) => {
      const id = values[columns.findIndex((column) => column.fieldPath?.replace(/^root\./, '') === 'id')];
      const artifactRow = exportedByID.get(id);
      const previewRow = previewByID.get(id);
      if (!artifactRow || !previewRow) return false;
      return columns.every((column, index) => {
        if (column.kind === 'ownerRecords') return true;
        const expected = previewRow[column.id] == null ? '—' : String(previewRow[column.id]);
        return values[index] === expected && j01JSONValuesEquivalent(artifactRow[column.id], previewRow[column.id] ?? null);
      });
    });
    recordAssertion(report, `j01-cda-${resourceType.toLowerCase()}-viewer-rows-agree-with-preview-and-artifact`, true, viewerRows.rows.length > 0 && viewerMatchesArtifacts);
    report.target.externalTasks.push({
      explorerId, outputId, revisionId, rowRoot: resourceType, draftVersion: builderAfterPublish.draftVersion,
      columns, previewRows: preview.rows.length, previewRowCount: preview.rowCount,
      viewerRows: viewerRows.rows.length, artifact: { path: archivePath, bytes: statSync(archivePath).size, rows: artifactManifest.rows, columns: schema.columns },
      repeatedFeature: resourceType === 'Observation' ? report.target.cdaSelection.repeatedObservation : undefined,
    });
    await saveScreenshot(`j01-cda-${resourceType.toLowerCase()}-viewer`);
    return { revisionId, output, builderAfterPublish };
  };

  try {
    assertExternalJ01SourcesUnchanged(externalManifest);
    await openBuilderEntry();
    await saveDOM('j01-cda-entry');

    await action('cda_patient_task', async () => {
      const explorerId = await createBlankExplorer(`J01 CDA Patient ${target.fixtureProject.slice(-8)}`);
      const { table } = await createTableRoot(explorerId, 'Patient', 'CDA Patient task');
      const outputId = table.output.id;
      const patientScalar = externalJ01PatientScalar(await readState(explorerId));
      let state = await chooseFeature({ explorerId, outputId, query: 'id', checkboxLabel: 'Select Patient.id', columnCount: 1, label: 'id' });
      state = await chooseFeature({
        explorerId, outputId, query: patientScalar.fieldPath, checkboxLabel: patientScalar.checkboxLabel,
        columnCount: 2, label: patientScalar.label,
      });
      const identities = columnSnapshot(state, outputId);
      const patientURL = `${target.uiUrl}/?project=${encodeURIComponent(target.fixtureProject)}&explorer=${encodeURIComponent(explorerId)}&mode=builder`;
      await navigate(cdp, patientURL);
      await waitForBrowser(cdp, `Boolean(document.querySelector('[aria-label="Display name for configured id"]'))`, 60000);
      const reloaded = await waitForState(explorerId, (value) => value.workspace?.documents?.some((document) => document.output?.id === outputId && document.columns.length === 2), 'reloaded Patient task');
      recordAssertion(report, 'j01-cda-patient-reload-preserves-column-identities', identities, columnSnapshot(reloaded, outputId));
      recordAssertion(report, 'j01-cda-patient-reload-preserves-records-row-definition',
        { kind: 'RECORDS', records: {} }, reloaded.workspace.documents.find((document) => document.output?.id === outputId)?.rows);
      await browserEval(cdp, `clickButton('Preview')`);
      await waitForBrowser(cdp, `document.body.innerText.includes('Preview and configure') && Boolean(document.querySelector('[role="table"]'))`, 90000);
      await browserEval(cdp, `selectOption('Preview row limit', '1,000')`);
      await waitForBrowser(cdp, `document.querySelector('select[aria-label="Preview row limit"]')?.value === '1000'`, 90000);
      const preview = await fetchJ01Preview(target, explorerId, outputId, await readState(explorerId), 1000);
      const expectedIDs = externalManifest.files.find((file) => file.name === 'Patient.ndjson').records.map((record) => record.id).sort();
      const idColumn = identities.find((column) => column.fieldPath?.replace(/^root\./, '') === 'id');
      recordAssertion(report, 'j01-cda-patient-preview-has-exact-selected-source-membership', expectedIDs, preview.rows.map((row) => row[idColumn.id]).sort());
      recordAssertion(report, 'j01-cda-patient-preview-has-exactly-two-selected-columns', 2, preview.columns.length);
      report.target.cdaPatientLiteralValues = preview.rows.slice(0, 5).map((row) => ({
        id: row[idColumn.id], [patientScalar.fieldPath]: row[identities[1].id] ?? null,
      }));
      const published = await publishAndExport({ resourceType: 'Patient', title: 'CDA Patient task', explorerId, outputId, columns: identities, preview });
      report.target.externalTasks.at(-1).draftVersion = published.builderAfterPublish.draftVersion;
    });

    await action('cda_observation_repeated_feature_task', async () => {
      const explorerId = await createBlankExplorer(`J01 CDA Observation ${target.fixtureProject.slice(-8)}`);
      const { table } = await createTableRoot(explorerId, 'Observation', 'CDA Observation task');
      const outputId = table.output.id;
      let state = await chooseFeature({ explorerId, outputId, query: 'id', checkboxLabel: 'Select Observation.id', columnCount: 1, label: 'id' });
      state = await chooseFeature({ explorerId, outputId, query: 'status', checkboxLabel: 'Select Observation.status', columnCount: 2, label: 'status' });
      const repeatedLabel = String(repeatedFeature.display || repeatedFeature.code);
      state = await chooseFeature({
        explorerId, outputId, query: repeatedFeature.code, checkboxLabel: `Select ${repeatedLabel}`,
        articleMatch: `${repeatedFeature.system} · ${repeatedFeature.code}`, columnCount: 3,
        label: repeatedLabel, ownerRecords: true,
      });
      const identities = columnSnapshot(state, outputId);
      const repeatedColumn = identities.find((column) => column.kind === 'ownerRecords' && column.system === repeatedFeature.system && column.code === repeatedFeature.code);
      if (!repeatedColumn || repeatedColumn.ownerPath !== 'component[]') throw new Error('CDA J01 selected feature is not a preserving repeated-component output');
      await measureJ01CatalogRequests(target, report, {
        explorerId, snapshotToken: state.catalog.snapshotToken, rowRoot: 'Observation', resourceType: 'Observation', query: repeatedFeature.code,
      });
      const observationURL = `${target.uiUrl}/?project=${encodeURIComponent(target.fixtureProject)}&explorer=${encodeURIComponent(explorerId)}&mode=builder`;
      await navigate(cdp, observationURL);
      await waitForBrowser(cdp, `Boolean(document.querySelector('[aria-label="Display name for configured id"]'))`, 60000);
      const reloaded = await waitForState(explorerId, (value) => value.workspace?.documents?.some((document) => document.output?.id === outputId && document.columns.length === 3), 'reloaded Observation task');
      recordAssertion(report, 'j01-cda-observation-reload-preserves-column-identities', identities, columnSnapshot(reloaded, outputId));
      recordAssertion(report, 'j01-cda-observation-reload-preserves-records-row-definition',
        { kind: 'RECORDS', records: {} }, reloaded.workspace.documents.find((document) => document.output?.id === outputId)?.rows);
      await browserEval(cdp, `clickButton('Preview')`);
      await waitForBrowser(cdp, `document.body.innerText.includes('Preview and configure') && Boolean(document.querySelector('[role="table"]'))`, 90000);
      await browserEval(cdp, `selectOption('Preview row limit', '1,000')`);
      await waitForBrowser(cdp, `document.querySelector('select[aria-label="Preview row limit"]')?.value === '1000'`, 90000);
      const preview = await fetchJ01Preview(target, explorerId, outputId, await readState(explorerId), 1000);
      const expectedIDs = externalManifest.files.find((file) => file.name === 'Observation.ndjson').records.map((record) => record.id).sort();
      const idColumn = identities.find((column) => column.fieldPath?.replace(/^root\./, '') === 'id');
      recordAssertion(report, 'j01-cda-observation-preview-has-exact-selected-source-membership', expectedIDs, preview.rows.map((row) => row[idColumn.id]).sort());
      recordAssertion(report, 'j01-cda-observation-preview-has-exactly-three-selected-columns', 3, preview.columns.length);
      const repeatedRowIndex = preview.rows.findIndex((row) => Array.isArray(row[repeatedColumn.id]) && row[repeatedColumn.id].some((record) => record.codings?.some((coding) => coding.system === repeatedFeature.system && coding.code === repeatedFeature.code)));
      if (repeatedRowIndex < 0) throw new Error(`CDA J01 Preview has no literal ${repeatedFeature.system} · ${repeatedFeature.code} owner record`);
      const matchingOwner = preview.rows[repeatedRowIndex][repeatedColumn.id].find((record) => record.codings?.some((coding) => coding.system === repeatedFeature.system && coding.code === repeatedFeature.code));
      recordAssertion(report, 'j01-cda-repeated-preview-keeps-exact-source-owner-and-status', {
        resourceId: repeatedRecord.id, ownerPath: 'component[]', system: repeatedFeature.system, code: repeatedFeature.code,
      }, {
        resourceId: matchingOwner.source?.resourceId, ownerPath: matchingOwner.source?.ownerPath,
        system: matchingOwner.codings?.find((coding) => coding.system === repeatedFeature.system && coding.code === repeatedFeature.code)?.system,
        code: matchingOwner.codings?.find((coding) => coding.system === repeatedFeature.system && coding.code === repeatedFeature.code)?.code,
      });
      report.target.cdaObservationLiteralValues = {
        id: preview.rows[repeatedRowIndex][idColumn.id],
        status: preview.rows[repeatedRowIndex][identities[1].id] ?? null,
        ownerRecord: matchingOwner,
      };
      const inspectorLabel = repeatedColumn.label;
      await browserEval(cdp, `scrollVirtualTableToRow('preview-table-scroll', ${repeatedRowIndex})`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('button[aria-label="Inspect ${inspectorLabel} for row ${repeatedRowIndex + 1}"]'))`, 60000);
      await browserEval(cdp, `clickButton('Inspect ${inspectorLabel} for row ${repeatedRowIndex + 1}')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('[role="dialog"][aria-label="${inspectorLabel} record evidence"]'))`, 30000);
      const dialogText = String(await evaluate(cdp, `document.querySelector('[role="dialog"][aria-label="${inspectorLabel} record evidence"]')?.innerText || ''`));
      const dialogFragments = [
        repeatedFeature.system, repeatedFeature.code, String(matchingOwner.status), repeatedRecord.id,
        'ownerPath: component[]',
        ...(matchingOwner.value === null || matchingOwner.value === undefined ? [] : [String(matchingOwner.value)]),
        ...(matchingOwner.unit ? [String(matchingOwner.unit)] : []),
      ];
      recordAssertion(report, 'j01-cda-repeated-inspector-exposes-selected-feature-source', true,
        dialogFragments.every((fragment) => dialogText.includes(fragment)));
      await browserEval(cdp, `clickButton('Close')`);
      const acknowledgementSamples = await measureJ01InspectorAcknowledgements(cdp, inspectorLabel, repeatedRowIndex);
      report.timings.uiAcknowledgements = summarizeTimingSamples(acknowledgementSamples);
      report.target.uiAcknowledgementSamples = acknowledgementSamples;
      recordAssertion(report, 'j01-cda-captures-thirty-ui-acknowledgements', 30, acknowledgementSamples.length);
      const published = await publishAndExport({ resourceType: 'Observation', title: 'CDA Observation task', explorerId, outputId, columns: identities, preview });
      report.target.externalTasks.at(-1).draftVersion = published.builderAfterPublish.draftVersion;
    });

    recordAssertion(report, 'j01-cda-runs-two-different-row-roots', ['Patient', 'Observation'], report.target.externalTasks.map((task) => task.rowRoot));
    recordAssertion(report, 'j01-cda-includes-a-preserving-repeated-feature', true,
      report.target.externalTasks.some((task) => task.columns.some((column) => column.kind === 'ownerRecords' && column.ownerPath === 'component[]')));
    recordAssertion(report, 'j01-cda-source-files-remain-read-only', true, assertExternalJ01SourcesUnchanged(externalManifest));
    const finalManifest = await selectExternalJ01Manifest(externalManifest.summary.sourceDirectory);
    const sourceDigestStable = externalManifest.summary.sourceSHA256 === finalManifest.summary.sourceSHA256;
    recordAssertion(report, 'j01-cda-selected-source-digest-remains-unchanged', externalManifest.summary.sourceSHA256, finalManifest.summary.sourceSHA256);
    report.target.sourceInvariance = externalManifest.files.map((file, index) => {
      const after = statSync(file.sourcePath);
      const finalFile = finalManifest.files[index];
      return {
        path: file.sourcePath,
        before: file.sourceStat,
        after: { sizeBytes: after.size, modifiedMs: after.mtimeMs, inode: after.ino },
        selectedContentSHA256Before: file.contentSHA256,
        selectedContentSHA256After: finalFile.contentSHA256,
        unchanged: file.contentSHA256 === finalFile.contentSHA256
          && after.size === file.sourceStat.sizeBytes
          && after.mtimeMs === file.sourceStat.modifiedMs
          && after.ino === file.sourceStat.inode,
      };
    });
    recordAssertion(report, 'j01-cda-source-stats-and-selected-content-remain-unchanged', true,
      sourceDigestStable && report.target.sourceInvariance.every((file) => file.unchanged));
    const sourceInvariancePath = join(evidenceDir, 'source-invariance.json');
    writeJSON(sourceInvariancePath, {
      sourceDirectory: externalManifest.summary.sourceDirectory,
      sourceSHA256Before: externalManifest.summary.sourceSHA256,
      sourceSHA256After: finalManifest.summary.sourceSHA256,
      files: report.target.sourceInvariance,
    });
    recordEvidence(report, sourceInvariancePath);
    const timingPath = join(evidenceDir, 'request-ui-timings.json');
    writeJSON(timingPath, {
      authoringRequests: report.requests.filter((request) => request.kind === 'warm-semantic-inventory'),
      authoringSummary: report.timings.authoringRequests,
      uiAcknowledgements: report.target.uiAcknowledgementSamples,
      uiSummary: report.timings.uiAcknowledgements,
    });
    recordEvidence(report, timingPath);
    await saveDOM('j01-cda-final');
    report.timings.browser_scenario_ms = report.actions.reduce((total, item) => total + item.elapsedMs, 0);
  } finally {
    try { await browser.close(); } catch {}
  }
};
const verifyJ01BrowserScenario = async (target, report, entryTarget = target, externalManifest) => {
  if (externalManifest) return verifyJ01ExternalBrowserScenario(target, report, entryTarget, externalManifest);
  const runID = `${Date.now().toString(36)}-${randomUUID().slice(0, 8)}`;
  const evidenceDir = join(target.artifacts, runID);
  const downloadDir = join(evidenceDir, 'downloads');
  mkdirSync(downloadDir, { recursive: true, mode: 0o700 });
  report.target.evidenceDirectory = evidenceDir;
  report.target.ports = { api: target.apiPort, ui: target.uiPort };
  recordEvidence(report, evidenceDir);
  report.requests = [];
  report.actions = [];
  let previewRowsForArtifact = [];

  const browser = await launchBrowser(downloadDir);
  const cdp = browser.cdp;
  const network = [];
  const pendingBodies = new Set();
  const parseJSON = (raw) => {
    try { return JSON.parse(raw ?? ''); } catch { return undefined; }
  };
  const bodyForPath = (path) => network.filter((item) => item.url.endsWith(path));
  cdp.on('Network.requestWillBeSent', (event) => {
    if (!event.request.url.includes('/authoring/v2/')) return;
    const item = {
      requestId: event.requestId,
      url: new URL(event.request.url).pathname,
      method: event.request.method,
      xRequestId: event.request.headers?.['X-Request-ID'] ?? event.request.headers?.['x-request-id'],
      postData: event.request.postData,
      startedAtMs: event.wallTime ? Math.round(event.wallTime * 1000) : undefined,
    };
    network.push(item);
  });
  cdp.on('Network.responseReceived', (event) => {
    const item = network.find((candidate) => candidate.requestId === event.requestId);
    if (item) item.response = { status: event.response.status, mimeType: event.response.mimeType };
  });
  cdp.on('Network.loadingFinished', (event) => {
    const item = network.find((candidate) => candidate.requestId === event.requestId);
    if (!item || !item.response?.mimeType?.includes('json')) return;
    const capture = (async () => {
      try {
        const response = await cdp.send('Network.getResponseBody', { requestId: item.requestId });
        item.responseBody = parseJSON(response.base64Encoded ? Buffer.from(response.body, 'base64').toString('utf8') : response.body);
      } catch (error) {
        item.responseBodyError = String(error);
      }
    })().finally(() => pendingBodies.delete(capture));
    pendingBodies.add(capture);
  });

  const action = async (name, operation) => {
    const started = Date.now();
    await operation();
    const elapsedMs = Date.now() - started;
    report.actions.push({ name, elapsedMs });
    report.timings[`j01_${name}_ms`] = elapsedMs;
  };
  const readState = async () => fetchBuilderState(target, report.target.explorerId);
  const waitForState = async (predicate, label, timeout = 30000) => {
    const started = Date.now();
    let state;
    while (Date.now() - started < timeout) {
      state = await readState();
      if (predicate(state)) return state;
      await sleep(200);
    }
    throw new Error(`timed out waiting for J01 Builder state: ${label}; draft=${state?.draftVersion}`);
  };
  const waitForNetworkResponse = async (path, afterIndex = -1, timeout = 30000, predicate = () => true) => {
    const started = Date.now();
    while (Date.now() - started < timeout) {
      const match = network.find((item, index) => index > afterIndex && item.url.endsWith(path) && item.responseBody !== undefined && predicate(item));
      if (match) return match;
      await sleep(50);
    }
    throw new Error(`timed out waiting for J01 ${path} response; observed=${JSON.stringify(bodyForPath(path).map((item) => ({ request: parseJSON(item.postData), response: item.response?.status, body: item.responseBodyError })))}`);
  };
  const captureDOM = async (name) => {
    const path = join(evidenceDir, `${name}.html`);
    await snapshot(cdp, path);
    recordEvidence(report, path);
  };
  const saveScreenshot = async (name) => {
    const image = await cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
    const path = join(evidenceDir, `${name}.png`);
    writeFileSync(path, Buffer.from(image.data, 'base64'), { mode: 0o600 });
    recordEvidence(report, path);
  };
  const saveNetworkEvidence = async () => {
    await Promise.allSettled([...pendingBodies]);
    const path = join(evidenceDir, 'network-identities.json');
    writeJSON(path, network);
    recordEvidence(report, path);
  };

  try {
    const bootstrapExplorerId = report.target.bootstrapExplorerId;
    if (!bootstrapExplorerId) throw new Error('J01 fresh fixture bootstrap Explorer identity is missing');
    const baseURL = `${target.uiUrl}/?project=${encodeURIComponent(target.fixtureProject)}&explorer=${encodeURIComponent(bootstrapExplorerId)}&mode=builder`;
    await navigate(cdp, entryTarget.uiUrl);
    await navigate(cdp, baseURL);
    await waitForBrowser(cdp, `document.body.innerText.includes('Build your features') || document.body.innerText.includes('Create your first table')`, 60000);
    await captureDOM('j01-builder-start');

    await action('blank_explorer_and_observation_table', async () => {
      const title = `J01 ${target.fixtureProject.slice(-18)}`;
      await browserEval(cdp, `clickText('summary', 'New explorer')`);
      await browserEval(cdp, `setInput('new-explorer-name', ${JSON.stringify(title)})`);
      await browserEval(cdp, `clickButton('Create blank')`);
      await waitForBrowser(cdp, `document.querySelector('select[aria-label="Explorer"] option:checked')?.textContent.trim() === ${JSON.stringify(title)} && document.body.innerText.includes('Create your first table')`);
      const explorerId = await evaluate(cdp, `document.querySelector('select[aria-label="Explorer"]')?.value || ''`);
      if (!explorerId) throw new Error('J01 blank Explorer was not selected');
      report.target.explorerId = explorerId;
      await browserEval(cdp, `setInput('first-table-name', 'J01 Observation values')`);
      await browserEval(cdp, `clickButton('Create table')`);
      await waitForBrowser(cdp, `document.body.innerText.includes('What should one row represent?') && Boolean(document.querySelector('button[aria-label="Choose Observation rows"]'))`);
      await browserEval(cdp, `clickButton('Choose Observation rows')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('[aria-label="Search features by field name, concept, or code"]')) && Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Search' && !button.disabled))`, 60000);
    });

    let state = await readState();
    const document = state.workspace?.documents?.find((candidate) => candidate.rootResourceType === 'Observation');
    if (!document?.output?.id) throw new Error('J01 selected Observation row root did not create one saved table');
    const outputId = document.output.id;
    report.target.outputId = outputId;
    recordAssertion(report, 'j01-persisted-observation-table-has-records-row-definition',
      { kind: 'RECORDS', records: {} }, document.rows);
    const rootNode = state.catalog.nodes.find((node) => node.resourceType === 'Observation' && node.rowRootEligible);
    if (!rootNode) throw new Error('J01 Observation root is missing from the authorized Builder catalog');
    recordAssertion(report, 'j01-starts-with-empty-observation-table', [], document.columns.map((column) => column.column));
    recordAssertion(report, 'j01-begins-in-catalog-without-advanced-graph', true,
      await evaluate(cdp, `!document.querySelector('.react-flow__node')`));
    await captureDOM('j01-catalog-empty-table');

    const fixture = JSON.parse(readFileSync(join(target.fixtureDir, 'j01-concepts.fixture.json'), 'utf8'));
    const catalogSearchStarted = Date.now();
    await browserEval(cdp, `setInput('Search features by field name, concept, or code', ${JSON.stringify(fixture.displayPrefix)})`);
    await browserEval(cdp, `clickButton('Search')`);
    const pageCodes = [];
    const expectedPageCount = Math.ceil(fixture.count / 50);
    let semanticPageCursor = -1;
    for (let pageNumber = 1; pageNumber <= expectedPageCount; pageNumber += 1) {
      const pageResponse = await waitForNetworkResponse('/semantic-inventory', semanticPageCursor, 60000,
        (item) => parseJSON(item.postData)?.query === fixture.displayPrefix);
      semanticPageCursor = network.indexOf(pageResponse);
      const requestBody = parseJSON(pageResponse.postData);
      if (requestBody?.query !== fixture.displayPrefix || requestBody?.rowRoot !== rootNode.resourceType || requestBody?.limit !== 50) {
        throw new Error(`J01 catalog page ${pageNumber} was not loaded through the expected visible search request: ${JSON.stringify(requestBody)}`);
      }
      if (Object.keys(pageResponse.responseBody ?? {}).some((key) => /example|total.?count/i.test(key))) {
        throw new Error(`J01 semantic inventory page ${pageNumber} returned an example or global count field`);
      }
      await waitForBrowser(cdp, `document.body.innerText.includes('Page ${pageNumber}') && (document.querySelector('section[aria-labelledby="feature-catalog-concepts-title"]')?.querySelectorAll('article').length || 0) > 0`, 60000);
      const visibleCodes = await evaluate(cdp, `(() => {
        const section = document.querySelector('section[aria-labelledby="feature-catalog-concepts-title"]');
        return [...(section?.querySelectorAll('article') || [])].map((article) => article.innerText.match(/\\bconcept-\\d{4}\\b/)?.[0]).filter(Boolean);
      })()`);
      recordAssertion(report, `j01-catalog-page-${pageNumber}-has-exactly-fifty-identities`, 50, visibleCodes.length);
      pageCodes.push(...visibleCodes);
      if (pageNumber < expectedPageCount) {
        await browserEval(cdp, `clickButton('Next')`);
        await waitForBrowser(cdp, `document.body.innerText.includes('Page ${pageNumber + 1}')`, 60000);
      }
    }
    const expectedCodes = Array.from({ length: fixture.count }, (_, index) => `${fixture.codePrefix}${String(index).padStart(4, '0')}`);
    recordAssertion(report, 'j01-browser-discovers-every-generated-code-once-across-pages', expectedCodes, [...pageCodes].sort());
    const generatedInventoryRequests = network.filter((item) => item.url.endsWith('/semantic-inventory') && parseJSON(item.postData)?.query === fixture.displayPrefix);
    const collectedInventory = await collectJ01SemanticConceptPages(async (requestBody) => {
      const item = generatedInventoryRequests[requestBody.cursor ? generatedInventoryRequests.findIndex((candidate) => parseJSON(candidate.postData)?.cursor === requestBody.cursor) : 0];
      if (!item) throw new Error(`J01 browser did not issue inventory page for cursor ${requestBody.cursor ?? '(first)'}`);
      const actualRequest = parseJSON(item.postData);
      for (const key of Object.keys(requestBody)) {
        if (actualRequest?.[key] !== requestBody[key]) throw new Error(`J01 browser inventory request ${key} differed from the verifier contract`);
      }
      if (Object.keys(actualRequest ?? {}).length !== Object.keys(requestBody).length) throw new Error('J01 browser inventory request included unexpected query fields');
      return { response: { ok: item.response?.status === 200, status: item.response?.status }, value: item.responseBody };
    }, {
      snapshotToken: state.catalog.snapshotToken,
      rowRoot: rootNode.resourceType,
      query: fixture.displayPrefix,
    }, fixture);
    report.timings.j01_catalog_pagination_ms = Date.now() - catalogSearchStarted;
    const collectedCodes = collectedInventory.entries.map((entry) => entry.code).sort();
    report.target.inventory = {
      discovered: collectedInventory.count,
      pageCount: collectedInventory.pages.length,
      countBasis: collectedInventory.countBasis,
      firstCode: collectedCodes[0],
      lastCode: collectedCodes.at(-1),
    };
    recordAssertion(report, 'j01-paginated-inventory-reports-exact-fixture-identity-coverage', {
      discovered: fixture.count,
      pageCount: expectedPageCount,
      countBasis: 'exact-paginated',
      firstCode: `${fixture.codePrefix}0000`,
      lastCode: `${fixture.codePrefix}${String(fixture.count - 1).padStart(4, '0')}`,
    }, report.target.inventory);
    await measureJ01CatalogRequests(target, report, {
      explorerId: report.target.explorerId,
      snapshotToken: state.catalog.snapshotToken,
      rowRoot: rootNode.resourceType,
      resourceType: rootNode.resourceType,
      query: fixture.displayPrefix,
    });
    state = await readState();
    recordAssertion(report, 'j01-opening-and-paging-catalog-does-not-create-columns', [],
      state.workspace.documents.find((candidate) => candidate.output?.id === outputId)?.columns.map((column) => column.column));
    await captureDOM('j01-catalog-after-generated-pagination');

    const fieldChoiceFor = (path) => {
      const candidate = state.catalog.candidates.find((item) => item.nodeId === rootNode.nodeId && item.fieldPath.replace(/^root\./, '') === path);
      const choice = candidate?.constructionChoice;
      const defaults = choice?.options?.filter((option) => option.decision === 'DEFAULT') ?? [];
      if (!candidate?.candidateId || choice?.source?.kind !== 'FIELD' || defaults.length !== 1 || choice.options.length !== 1) {
        throw new Error(`J01 Observation.${path} does not have one compiler-issued preserving default choice`);
      }
      return { candidate, choice, selection: { choiceId: choice.choiceId, form: defaults[0].form } };
    };
    const idField = fieldChoiceFor('id');
    const integerField = fieldChoiceFor('valueInteger');
    let selectedSemanticOwnerChoice;
    await action('add_ordinary_fields_from_catalog', async () => {
      for (const field of [idField, integerField]) {
        const path = field.candidate.fieldPath.replace(/^root\./, '');
        await browserEval(cdp, `setInput('Search features by field name, concept, or code', ${JSON.stringify(path)})`);
        await browserEval(cdp, `clickButton('Search')`);
        await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Select Observation.${path}"]:not(:disabled)'))`);
        await browserEval(cdp, `const input = inputByLabel(${JSON.stringify(`Select Observation.${path}`)}); if (!input || input.disabled) throw new Error('compiler-proved Observation.${path} choice is unavailable'); input.click();`);
        await browserEval(cdp, `clickButton('Add 1 selected feature')`);
        state = await waitForState((value) => value.workspace?.documents?.find((candidate) => candidate.output?.id === outputId)?.columns.length === (path === 'id' ? 1 : 2), `Observation.${path} compiler choice application`);
      }
    });

    await action('add_preserving_semantic_owner_records', async () => {
      await browserEval(cdp, `setInput('Search features by field name, concept, or code', 'shared')`);
      await browserEval(cdp, `clickButton('Search')`);
      const sharedRequest = await waitForNetworkResponse('/semantic-inventory', semanticPageCursor, 60000,
        (item) => parseJSON(item.postData)?.query === 'shared');
      semanticPageCursor = network.indexOf(sharedRequest);
      await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('article')].find((article) => article.textContent.includes('urn:study:A · shared') && article.textContent.includes('valueQuantity.value') && article.querySelector('input[type="checkbox"]:not(:disabled)')))`, 60000);
      const sharedBody = parseJSON(sharedRequest.postData);
      if (sharedBody?.query !== 'shared') throw new Error(`J01 semantic owner search used an unexpected query: ${JSON.stringify(sharedBody)}`);
      const sharedEntry = (sharedRequest.responseBody?.entries ?? []).find((entry) =>
        entry.resourceType === 'Observation' &&
        entry.system === 'urn:study:A' &&
        entry.code === 'shared' &&
        entry.constructionChoice?.source?.fieldPath === 'component[].valueQuantity.value');
      const semanticChoice = sharedEntry?.constructionChoice;
      if (semanticChoice?.source?.kind !== 'SEMANTIC' || semanticChoice.source.system !== 'urn:study:A' || semanticChoice.source.code !== 'shared') {
        throw new Error(`J01 semantic inventory did not issue the selected owner-bound compiler choice: ${JSON.stringify(semanticChoice?.source)}`);
      }
      if (!semanticChoice.options.some((option) => option.form === 'OWNER_RECORDS')) {
        throw new Error('J01 selected semantic choice does not advertise the preserving OWNER_RECORDS form');
      }
      selectedSemanticOwnerChoice = semanticChoice;
      report.target.semanticChoice = {
        choiceId: semanticChoice.choiceId,
        form: 'OWNER_RECORDS',
        system: semanticChoice.source.system,
        code: semanticChoice.source.code,
        ownerPath: semanticChoice.source.owningScope,
        valuePath: semanticChoice.source.fieldPath,
      };
      await browserEval(cdp, `const item = [...document.querySelectorAll('article')].find((article) => article.textContent.includes('urn:study:A · shared') && article.textContent.includes('valueQuantity.value')); const input = item?.querySelector('input[type="checkbox"]'); if (!input || input.disabled) throw new Error('Observation semantic owner choice is unavailable'); input.click();`);
      await browserEval(cdp, `clickButton('Add 1 selected feature')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('[role="dialog"]') && document.body.innerText.includes('Choose output forms'))`);
      await captureDOM('j01-owner-record-choice');
      await browserEval(cdp, `const input = inputByLabel('shared: Keep each matching record'); if (!input) throw new Error('compiler-proved OWNER_RECORDS choice is unavailable'); input.click();`);
      await browserEval(cdp, `clickButton('Add 1 selected feature')`);
      state = await waitForState((value) => value.workspace?.documents?.find((candidate) => candidate.output?.id === outputId)?.columns.length === 3, 'three compiler choices applied to one table');
    });

    const expectedChoices = [
      { choiceId: idField.selection.choiceId, form: idField.selection.form, outputId, title: 'id' },
      { choiceId: integerField.selection.choiceId, form: integerField.selection.form, outputId, title: 'valueInteger' },
      { choiceId: selectedSemanticOwnerChoice?.choiceId, form: 'OWNER_RECORDS', outputId, title: 'shared' },
    ];
    const constructionRequests = network
      .filter((item) => item.url.endsWith('/commands') && item.response?.status === 200)
      .flatMap((item) => {
        const body = parseJSON(item.postData);
        if (!body?.commands?.length || !body.commands.every((command) => command.type === 'APPLY_CONSTRUCTION_CHOICE')) return [];
        return j01ConstructionChoiceCommandIdentities(body, outputId);
      });
    recordAssertion(report, 'j01-browser-adds-exactly-three-distinct-compiler-issued-choices', expectedChoices, constructionRequests);
    report.target.selectedChoices = constructionRequests;
    recordAssertion(report, 'j01-no-graph-or-fhir-path-entry-was-used', true,
      await evaluate(cdp, `!document.querySelector('.react-flow__node') && ![...document.querySelectorAll('input,textarea')].some((input) => /FHIR.?Path/i.test(input.getAttribute('aria-label') || input.placeholder || ''))`));

    const baseColumns = state.workspace.documents.find((candidate) => candidate.output?.id === outputId).columns;
    const idColumn = baseColumns.find((column) => column.source.kind === 'field' && column.source.field.path.replace(/^root\./, '') === 'id');
    const valueColumn = baseColumns.find((column) => column.source.kind === 'field' && column.source.field.path.replace(/^root\./, '') === 'valueInteger');
    const ownerColumn = baseColumns.find((column) => column.source.kind === 'ownerRecords' && column.source.ownerRecords.key.system === 'urn:study:A' && column.source.ownerRecords.key.code === 'shared');
    if (!idColumn || !valueColumn || !ownerColumn) throw new Error('J01 selected column source identities differ from the three compiler choices');
    report.target.columnIds = { id: idColumn.column, valueInteger: valueColumn.column, ownerRecords: ownerColumn.column };

    await action('rename_and_reorder_stable_column', async () => {
      await browserEval(cdp, `setInput('Display name for configured id', 'Observation identifier')`);
      await browserEval(cdp, `inputByLabel('Display name for configured id').focus()`);
      await cdp.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Enter', code: 'Enter' });
      await cdp.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Enter', code: 'Enter' });
      await waitForState((value) => value.workspace?.documents?.find((candidate) => candidate.output?.id === outputId)?.columns.some((column) => column.column === idColumn.column && column.label === 'Observation identifier'), 'renamed stable id column');
      await browserEval(cdp, `clickButton('Move Observation identifier to end')`);
      state = await waitForState((value) => value.workspace?.documents?.find((candidate) => candidate.output?.id === outputId)?.columns.find((column) => column.column === idColumn.column)?.table?.order === 2, 'id column moved to end');
    });
    const savedIdentity = j01ColumnIdentitySnapshot(state, outputId);
    recordAssertion(report, 'j01-save-has-exact-three-renamed-reordered-source-identities', [
      { columnId: valueColumn.column, label: 'valueInteger', order: 0, source: { kind: 'field', path: 'valueInteger' } },
      { columnId: ownerColumn.column, label: 'shared', order: 1, source: { kind: 'ownerRecords', system: 'urn:study:A', code: 'shared', ownerPath: 'component[]', valuePath: 'valueQuantity.value' } },
      { columnId: idColumn.column, label: 'Observation identifier', order: 2, source: { kind: 'field', path: 'id' } },
    ], savedIdentity);
    await captureDOM('j01-renamed-reordered-builder');
    await saveScreenshot('j01-renamed-reordered-builder');

    await action('reload_preserves_three_stable_columns', async () => {
      const browserURL = `${target.uiUrl}/?project=${encodeURIComponent(target.fixtureProject)}&explorer=${encodeURIComponent(report.target.explorerId)}&mode=builder`;
      await navigate(cdp, browserURL);
      await waitForBrowser(cdp, `document.body.innerText.includes('Build your features') && Boolean(document.querySelector('[aria-label="Display name for configured Observation identifier"]'))`, 60000);
      const reloaded = await waitForState((value) => value.workspace?.documents?.some((candidate) => candidate.output?.id === outputId && candidate.columns.length === 3), 'reloaded J01 saved workspace');
      const reloadedIdentity = j01ColumnIdentitySnapshot(reloaded, outputId);
      recordAssertion(report, 'j01-reload-preserves-exact-column-identities-names-order-and-sources', savedIdentity, reloadedIdentity);
      await captureDOM('j01-builder-after-reload');
    });

    await action('preview_literal_scalar_and_owner_evidence', async () => {
      let previewStartIndex = network.length - 1;
      await browserEval(cdp, `clickButton('Preview')`);
      await waitForBrowser(cdp, `document.body.innerText.includes('Preview and configure') && Boolean(document.querySelector('button[aria-label^="Inspect shared for row "]'))`, 60000);
      await waitForNetworkResponse('/preview', previewStartIndex, 60000);
      previewStartIndex = network.length - 1;
      await browserEval(cdp, `selectOption('Preview row limit', '1,000')`);
      const previewResponse = await waitForNetworkResponse('/preview', previewStartIndex, 60000);
      if (previewResponse.response.status !== 200 || !Array.isArray(previewResponse.responseBody?.rows)) {
        throw new Error(`J01 preview API did not return a live row preview: HTTP ${previewResponse.response?.status} ${JSON.stringify(previewResponse.responseBody).slice(0, 300)}`);
      }
      const previewRows = previewResponse.responseBody.rows;
      previewRowsForArtifact = previewRows;
      const zeroRow = previewRows.find((row) => Object.values(row ?? {}).some((value) => String(value) === 'dev-j01-concept-0000'));
      const previewValueColumn = previewResponse.responseBody.columns?.find((column) => column.authoredColumns?.includes(valueColumn.column) || column.column === valueColumn.column);
      const previewIdColumn = previewResponse.responseBody.columns?.find((column) => column.authoredColumns?.includes(idColumn.column) || column.column === idColumn.column);
      if (!zeroRow || !previewValueColumn || !previewIdColumn) {
        throw new Error(`J01 preview omitted the exact zero-valued generated row or selected columns: ${JSON.stringify({ rowCount: previewRows.length, columns: previewResponse.responseBody.columns?.map((column) => ({ column: column.column, label: column.label, authoredColumns: column.authoredColumns })) })}`);
      }
      recordAssertion(report, 'j01-preview-preserves-literal-zero-not-missing', { identifier: 'dev-j01-concept-0000', value: 0 }, {
        identifier: zeroRow[previewIdColumn.column],
        value: zeroRow[previewValueColumn.column],
      });
      const ownerRowIndex = previewRows.findIndex((row) => row[previewIdColumn.column] === 'dev-pair-001');
      if (ownerRowIndex < 0) throw new Error('J01 1,000-row preview omitted the Study A owner-record fixture');
      const ownerButtonLabel = `Inspect shared for row ${ownerRowIndex + 1}`;
      await browserEval(cdp, `scrollVirtualTableToRow('preview-table-scroll', ${ownerRowIndex})`);
      await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button[aria-label^="Inspect shared for row "]')].find((button) => button.getAttribute('aria-label') === ${JSON.stringify(ownerButtonLabel)}))`, 60000);
      const previewTable = await evaluate(cdp, `(() => {
        const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
        const rows = [...(table?.querySelectorAll('[role="row"]') || [])];
        return {
          headers: [...(rows[0]?.querySelectorAll('[role="columnheader"]') || [])].map((cell) => cell.textContent.trim()),
          rowCount: ${previewRows.length},
          zeroRendered: [...rows.slice(1)].some((row) => [...row.querySelectorAll('[role="cell"]')].some((cell) => cell.textContent.trim() === '0')),
          tableColumns: Number(table?.getAttribute('aria-colcount') || 0),
        };
      })()`);
      recordAssertion(report, 'j01-preview-renders-only-the-three-selected-columns', 3, previewTable.tableColumns);
      recordAssertion(report, 'j01-preview-has-the-saved-column-order-and-name', ['valueInteger', 'shared', 'Observation identifier'], previewTable.headers);
      report.target.preview = { rowSample: previewRows.length, countBasis: 'sampled-preview-limit', literalZero: 0, headers: previewTable.headers };
      await captureDOM('j01-preview-table');
      await saveScreenshot('j01-preview-table');
      const ownerButton = await evaluate(cdp, `(() => { const button = [...document.querySelectorAll('button[aria-label^="Inspect shared for row "]')].find((candidate) => candidate.getAttribute('aria-label') === ${JSON.stringify(ownerButtonLabel)}); return button ? { label: button.getAttribute('aria-label'), title: button.title } : undefined; })()`);
      if (!ownerButton) throw new Error('J01 Preview has no inspectable Study A owner-record cell for dev-pair-001');
      await browserEval(cdp, `inputByLabel(${JSON.stringify(ownerButtonLabel)}).click()`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('[role="dialog"][aria-label="shared record evidence"]')) && document.body.innerText.includes('Repeated FHIR records preserved in this cell')`, 30000);
      await browserEval(cdp, `(() => {
        const dialog = document.querySelector('[role="dialog"][aria-label="shared record evidence"]');
        const owners = [...(dialog?.querySelectorAll('summary') || [])].filter((summary) => summary.textContent.trim() === 'Raw FHIR owner');
        if (owners.length !== 2) throw new Error('J01 repeated-cell inspector did not expose two matching owner records');
        owners.forEach((summary) => summary.click());
      })()`);
      const ownerEvidence = String(await evaluate(cdp, `document.querySelector('[role="dialog"][aria-label="shared record evidence"]')?.innerText || ''`));
      recordAssertion(report, 'j01-preview-preserves-owner-value-unit-absence-choice-arm-and-source', true,
        ownerEvidence.includes('111') && ownerEvidence.includes('cm') && ownerEvidence.includes('VALUE') && ownerEvidence.includes('ABSENT') && ownerEvidence.includes('urn:study:A') && ownerEvidence.includes('shared') && ownerEvidence.includes('dev-pair-001') && ownerEvidence.includes('ownerOrdinal: 0') && ownerEvidence.includes('ownerPath: component[]'));
      const ownerEntries = Array.isArray(previewRows[ownerRowIndex][ownerColumn.column]) ? previewRows[ownerRowIndex][ownerColumn.column] : [];
      const ownerLiterals = ownerEntries.map(j01OwnerLiteralSnapshot);
      recordAssertion(report, 'j01-same-owner-output-has-exact-value-and-absence-literals', [
        { status: 'VALUE', value: 111, unit: 'cm', choiceArm: 'valueQuantity', system: 'urn:study:A', code: 'shared', source: { resourceType: 'Observation', resourceId: 'dev-pair-001', ownerPath: 'component[]', ownerOrdinal: 0 } },
        { status: 'ABSENT', value: null, unit: null, choiceArm: 'valueQuantity', system: 'urn:study:A', code: 'shared', source: { resourceType: 'Observation', resourceId: 'dev-pair-001', ownerPath: 'component[]', ownerOrdinal: 2 } },
      ], ownerLiterals);
      recordAssertion(report, 'j01-repeated-cell-details-include-hostile-owner-fields', true,
        ownerEvidence.includes('unmodeledSignal') && ownerEvidence.includes('_valueString') && ownerEvidence.includes('urn:j01:missing-primitive'));
      report.target.ownerEvidence = {
        inspectedCell: ownerButton,
        sameOwnerLiterals: ownerLiterals,
        includesValue: ownerEvidence.includes('111'),
        includesUnit: ownerEvidence.includes('cm'),
        includesAbsent: ownerEvidence.includes('ABSENT'),
        includesContributorSource: ownerEvidence.includes('dev-pair-001') && ownerEvidence.includes('ownerOrdinal: 0') && ownerEvidence.includes('ownerPath: component[]'),
        includesUnknownOwnerField: ownerEvidence.includes('unmodeledSignal'),
        includesAbsentPrimitiveMetadata: ownerEvidence.includes('_valueString') && ownerEvidence.includes('urn:j01:missing-primitive'),
      };
      await captureDOM('j01-owner-record-evidence');
      await browserEval(cdp, `clickButton('Close')`);
      const acknowledgementSamples = await measureJ01InspectorAcknowledgements(cdp, 'shared', ownerRowIndex);
      report.timings.uiAcknowledgements = summarizeTimingSamples(acknowledgementSamples);
      report.target.uiAcknowledgementSamples = acknowledgementSamples;
      recordAssertion(report, 'j01-captures-thirty-ui-acknowledgements', 30, acknowledgementSamples.length);
    });

    const timingPath = join(evidenceDir, 'request-ui-timings.json');
    writeJSON(timingPath, {
      authoringRequests: report.requests.filter((request) => request.kind === 'warm-semantic-inventory'),
      authoringSummary: report.timings.authoringRequests,
      uiAcknowledgements: report.target.uiAcknowledgementSamples,
      uiSummary: report.timings.uiAcknowledgements,
    });
    recordEvidence(report, timingPath);

    await action('publish_exact_selected_output', async () => {
      await browserEval(cdp, `clickButton('Publish')`);
      const publishRequest = await waitForNetworkResponse('/publish', -1, 60000);
      if (publishRequest.response.status !== 200) throw new Error(`J01 publish returned HTTP ${publishRequest.response.status}: ${JSON.stringify(publishRequest.responseBody)}`);
      let explorerState;
      let runtime;
      const started = Date.now();
      while (Date.now() - started < 60000) {
        try {
          explorerState = await fetchExplorerState(target, report.target.explorerId);
          runtime = explorerState.runtime ?? explorerState;
          if (runtime.outputs?.length) break;
        } catch { /* the durable publication can precede its Viewer read */ }
        await sleep(500);
      }
      if (!runtime?.outputs?.length) throw new Error('J01 published Explorer runtime did not become readable');
      recordAssertion(report, 'j01-published-explorer-has-only-one-output', 1, runtime.outputs.length);
      const output = runtime.outputs.find((candidate) => candidate.outputId === outputId);
      if (!output) throw new Error(`J01 publication omitted selected output ${outputId}`);
      const runtimeColumnLabels = output.columns.map((column) => column.label);
      recordAssertion(report, 'j01-published-output-has-exact-renamed-selected-columns', ['valueInteger', 'shared', 'Observation identifier'], runtimeColumnLabels);
      const emittedSources = output.columns.map((column) => {
        const emitted = emittedForPhysicalColumn(explorerState, outputId, column.column);
        return { column: column.column, label: column.label, sourcePath: emitted?.sourcePath, sourceResourceType: emitted?.sourceResourceType, kind: emitted?.kind };
      });
      recordAssertion(report, 'j01-published-output-retains-three-source-evidence-identities', true,
        emittedSources.length === 3 && emittedSources.every((column) => column.sourcePath && column.sourceResourceType === 'Observation'));
      report.target.publication = {
        revisionId: explorerState.active?.revisionId ?? explorerState.publication?.revisionId,
        outputId: output.outputId,
        outputs: runtime.outputs.map((candidate) => ({ outputId: candidate.outputId, columns: candidate.columns.map((column) => ({ column: column.column, label: column.label })) })),
        emittedSources,
      };
      const materializedRows = await graphQLRows(target, output.selector, output.columns.map((column) => column.column));
      recordAssertion(report, 'j01-published-query-uses-exact-selected-output-columns', output.columns.map((column) => column.column), materializedRows.columns);
      recordAssertion(report, 'j01-published-query-has-a-materialized-preview-page', true,
        Boolean(materializedRows.materialization?.id) && materializedRows.rows.length > 0);
      report.target.publishedQuery = { materializationId: materializedRows.materialization?.id, firstPageRows: materializedRows.rows.length, totalCountBasis: 'server-reported-published-query' };
    });

    await action('download_and_verify_published_artifact', async () => {
      await browserEval(cdp, `clickButton('Viewer')`);
      await waitForBrowser(cdp, `document.body.innerText.includes('Published') && [...document.querySelectorAll('button')].some((button) => ['Download training artifact', 'Download dataset'].includes(button.textContent.trim()))`, 60000);
      const artifactDownloadPlan = j01ArtifactDownloadPlan(await evaluate(cdp, `[...document.querySelectorAll('button')].map((button) => button.textContent.trim())`));
      recordAssertion(report, 'j01-viewer-exposes-artifact-download', true,
        ['Download training artifact', 'Download dataset'].includes(artifactDownloadPlan.triggerLabel));
      await waitForBrowser(cdp, `Boolean(document.querySelector('table[aria-label$=" results"]')) && Boolean(document.querySelector('table[aria-label$=" results"] tbody tr'))`, 60000);
      const viewerTable = await evaluate(cdp, `(() => {
        const table = document.querySelector('table[aria-label$=" results"]');
        return {
          headers: [...(table?.querySelectorAll('thead th') || [])].map((cell) => cell.textContent.trim()),
          rows: [...(table?.querySelectorAll('tbody tr') || [])].map((row) => [...row.querySelectorAll('td')].map((cell) => cell.textContent.trim())),
        };
      })()`);
      recordAssertion(report, 'j01-viewer-renders-the-selected-published-columns', report.target.publication.outputs[0].columns.map((column) => column.label), viewerTable.headers);
      const exportStarted = Date.now();
      let modalSchemaDigest;
      if (artifactDownloadPlan.confirmationLabel) {
        await browserEval(cdp, `clickButton(${JSON.stringify(artifactDownloadPlan.triggerLabel)})`);
        await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('[role="dialog"]')].find((dialog) => dialog.innerText.includes('Download dataset') && dialog.querySelector('[aria-label="Declared output types"]')))`, 60000);
        const modal = await evaluate(cdp, `(() => {
          const dialog = [...document.querySelectorAll('[role="dialog"]')].find((candidate) => candidate.innerText.includes('Download dataset') && candidate.querySelector('[aria-label="Declared output types"]'));
          const value = (label) => [...(dialog?.querySelectorAll('dt') || [])].find((term) => term.textContent.trim() === label)?.nextElementSibling?.textContent.trim() ?? '';
          return { text: dialog?.innerText ?? '', sourceGeneration: value('Source generation'), schemaDigest: value('Schema digest'), types: dialog?.querySelector('[aria-label="Declared output types"]')?.innerText ?? '' };
        })()`);
        const columns = report.target.publication.outputs[0].columns;
        const modalMatchesPublication = modal.sourceGeneration === target.fixtureGeneration
          && modal.text.includes(`${columns.length} declared output columns`)
          && columns.every((column) => modal.types.includes(column.label));
        recordAssertion(report, 'j01-artifact-modal-matches-published-output', true, modalMatchesPublication);
        if (!/^[a-f0-9]{64}$/.test(modal.schemaDigest)) throw new Error('J01 artifact modal has an invalid schema digest');
        modalSchemaDigest = modal.schemaDigest;
        report.target.artifactModal = { sourceGeneration: modal.sourceGeneration, schemaDigest: modal.schemaDigest, columns };
        await captureDOM('j01-artifact-download-modal');
        await browserEval(cdp, `(() => {
          const link = [...document.querySelectorAll('a[download]')].find((candidate) => candidate.textContent.trim() === ${JSON.stringify(artifactDownloadPlan.confirmationLabel)});
          if (!link) throw new Error('J01 artifact modal has no Download ZIP link');
          link.click();
        })()`);
      } else {
        await browserEval(cdp, `clickButton(${JSON.stringify(artifactDownloadPlan.triggerLabel)})`);
      }
      const archivePath = await findDownloadedArchive(downloadDir, 60000);
      report.timings.j01_export_download_ms = Date.now() - exportStarted;
      recordEvidence(report, archivePath);
      const archive = readStoredZip(archivePath);
      const artifact = inspectJ01ArtifactRows(archive, report.target.columnIds.id);
      const { manifest, schema, dataName, rowsByID: exportedByID } = artifact;
      const requiredMembers = [dataName, 'schema.json', 'provenance.json', 'quality.json', 'manifest.json'];
      recordAssertion(report, 'j01-export-has-data-schema-provenance-quality-and-manifest', true, requiredMembers.every((name) => archive.has(name)));
      const runtime = report.target.publication.outputs[0];
      const expectedColumnIDs = runtime.columns.map((column) => column.column);
      const exportedFeatureColumns = schema.columns.filter((column) => column.name !== 'project_id');
      recordAssertion(report, 'j01-export-schema-contains-exactly-the-selected-columns', [...expectedColumnIDs].sort(), exportedFeatureColumns.map((column) => column.name).sort());
      recordAssertion(report, 'j01-export-schema-preserves-selected-logical-types', true,
        schema.columns.every((column) => typeof column.logicalType === 'string' && column.logicalType.length > 0));
      recordAssertion(report, 'j01-export-data-member-matches-format', manifest.format === 'CSV' ? 'data.csv' : 'data.jsonl', dataName);
      if (manifest.format === 'CSV') {
        const csvHeader = parseArtifactCSV(archive.get(dataName).toString('utf8'))[0]?.map((cell) => cell.value) ?? [];
        recordAssertion(report, 'j01-export-header-matches-its-selected-schema', schema.columns.map((column) => column.name), csvHeader);
      } else {
        const schemaColumnNames = new Set(schema.columns.map((column) => column.name));
        recordAssertion(report, 'j01-export-jsonl-normalizes-schema-output-keys', true,
          artifact.rows.every((row) => Object.keys(row.values).every((name) => schemaColumnNames.has(name))));
      }
      if (modalSchemaDigest) recordAssertion(report, 'j01-export-schema-digest-matches-download-modal', modalSchemaDigest, manifest.identity.schemaDigest);
      recordAssertion(report, 'j01-artifact-binds-stable-publication-identities', {
        project: canonicalProjectID(target.fixtureProject), generation: target.fixtureGeneration,
        outputId: report.target.outputId, revisionId: report.target.publication.revisionId,
      }, {
        project: manifest.identity.project, generation: manifest.identity.datasetGeneration,
        outputId: manifest.identity.outputId, revisionId: manifest.identity.revisionId,
      });
      const fixtureRecords = readFileSync(join(target.fixtureDir, 'Observation.ndjson'), 'utf8').split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));
      const expectedIDs = [...fixtureRecords.map((record) => record.id), ...Array.from({ length: fixture.count }, (_, index) => `dev-j01-concept-${String(index).padStart(4, '0')}`)].sort();
      const exportedIDs = [...exportedByID.keys()].sort();
      recordAssertion(report, 'j01-export-preserves-exact-observation-row-membership', expectedIDs, exportedIDs);
      const previewByID = new Map(previewRowsForArtifact.map((row) => [row[report.target.columnIds.id], row]));
      const previewMatchesArtifact = [...previewByID].every(([id, previewRow]) => {
        const artifactRow = exportedByID.get(id);
        return Boolean(artifactRow && expectedColumnIDs.every((column) =>
          j01JSONValuesEquivalent(artifactRow[column], previewRow[column] ?? null)));
      });
      recordAssertion(report, 'j01-typed-artifact-values-match-live-preview', true, previewMatchesArtifact);
      const viewerMatchesPublishedArtifact = j01ViewerValuesAgree({
        viewerTable, columns: runtime.columns, previewByID, artifactByID: exportedByID,
        idColumn: report.target.columnIds.id, structuredColumn: report.target.columnIds.ownerRecords,
      });
      recordAssertion(report, 'j01-viewer-values-agree-with-preview-and-typed-artifact', true, viewerMatchesPublishedArtifact);
      const zeroExportRow = exportedByID.get('dev-j01-concept-0000');
      recordAssertion(report, 'j01-export-preserves-integer-zero-as-zero', 0, zeroExportRow?.[report.target.columnIds.valueInteger]);
      const ownerExportLiterals = exportedByID.get('dev-pair-001')?.[report.target.columnIds.ownerRecords];
      recordAssertion(report, 'j01-export-preserves-exact-repeated-owner-literals', report.target.ownerEvidence.sameOwnerLiterals,
        Array.isArray(ownerExportLiterals) ? ownerExportLiterals.map(j01OwnerLiteralSnapshot) : []);
      recordAssertion(report, 'j01-artifact-is-bound-to-selected-publication-output', report.target.outputId, manifest.identity.outputId);
      recordAssertion(report, 'j01-artifact-row-count-matches-exact-fixture-membership', expectedIDs.length, manifest.rows);
      report.target.export = { path: archivePath, bytes: statSync(archivePath).size, rows: manifest.rows, features: manifest.features, countBasis: 'exact-exported-fixture-membership' };
      await captureDOM('j01-viewer-published-export');
      await saveScreenshot('j01-viewer-published-export');
    });

    recordLimitation(report, 'authorization-denial-local-stack', 'This isolated verification Compose stack uses the local AllowAll authorizer; the run proves exact fixture identity coverage and absence of examples/global counts from browse responses, but cannot exercise an unauthorized principal.');
    report.target.otherFixtureDistinctions = {
      observationFalsePrimitive: 'not-present-in-selected Observation fixture rows',
      observationEmptyString: 'not-present-in selected Observation fixture rows',
      zero: 'verified as integer 0 on dev-j01-concept-0000',
      absence: 'verified as ABSENT in dev-pair-001 owner-record evidence',
    };
    recordAssertion(report, 'j01-run-never-rendered-advanced-graph', true,
      await evaluate(cdp, `!document.querySelector('.react-flow__node')`));
    await saveNetworkEvidence();
  } finally {
    try {
      await saveNetworkEvidence();
      await captureDOM('j01-final');
    } catch { /* keep the main failure */ }
    await browser.close();
  }
};

export const readStoredZip = (path) => {
  const archive = readFileSync(path);
  let eocd = -1;
  for (let offset = archive.length - 22; offset >= Math.max(0, archive.length - 65557); offset -= 1) {
    if (archive.readUInt32LE(offset) === 0x06054b50) { eocd = offset; break; }
  }
  if (eocd < 0) throw new Error('training artifact has no ZIP end record');
  const entries = archive.readUInt16LE(eocd + 10);
  let cursor = archive.readUInt32LE(eocd + 16);
  const members = new Map();
  for (let index = 0; index < entries; index += 1) {
    if (archive.readUInt32LE(cursor) !== 0x02014b50) throw new Error('training artifact central directory is invalid');
    const method = archive.readUInt16LE(cursor + 10);
    const compressedBytes = archive.readUInt32LE(cursor + 20);
    const nameBytes = archive.readUInt16LE(cursor + 28);
    const extraBytes = archive.readUInt16LE(cursor + 30);
    const commentBytes = archive.readUInt16LE(cursor + 32);
    const localOffset = archive.readUInt32LE(cursor + 42);
    const name = archive.subarray(cursor + 46, cursor + 46 + nameBytes).toString('utf8');
    if (method !== 0 || archive.readUInt32LE(localOffset) !== 0x04034b50) throw new Error(`training artifact member ${name} is not a stored ZIP entry`);
    const localNameBytes = archive.readUInt16LE(localOffset + 26);
    const localExtraBytes = archive.readUInt16LE(localOffset + 28);
    const dataOffset = localOffset + 30 + localNameBytes + localExtraBytes;
    members.set(name, archive.subarray(dataOffset, dataOffset + compressedBytes));
    cursor += 46 + nameBytes + extraBytes + commentBytes;
  }
  return members;
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

const fetchJ01Preview = async (target, explorerId, outputId, state, limit = 1000) => {
  let receiptId = state.receipt?.receiptId ?? state.receiptId;
  if (!receiptId) {
    const reconciled = await requestJSON(`${bootstrapAuthoringURL(target, explorerId)}/reconcile`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ snapshotToken: state.catalog.snapshotToken, draftVersion: state.draftVersion, draftDigest: state.draftDigest }),
      timeout: 60000,
    });
    if (!reconciled.response.ok || !reconciled.value?.receiptId) {
      throw new Error(`J01 compile receipt was unavailable: HTTP ${reconciled.response.status} ${JSON.stringify(reconciled.value).slice(0, 500)}`);
    }
    receiptId = reconciled.value.receiptId;
  }
  const preview = await requestJSON(`${bootstrapAuthoringURL(target, explorerId)}/preview`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ receiptId, outputId, limit }),
    timeout: 90000,
  });
  if (!preview.response.ok || preview.value?.receiptId !== receiptId || preview.value?.outputId !== outputId) {
    throw new Error(`J01 literal preview read failed: HTTP ${preview.response.status} ${JSON.stringify(preview.value).slice(0, 500)}`);
  }
  return preview.value;
};

const measureJ01CatalogRequests = async (target, report, { explorerId, snapshotToken, rowRoot, resourceType, query }, sampleCount = 30) => {
  const samples = [];
  const requests = [];
  const url = `${bootstrapAuthoringURL(target, explorerId)}/semantic-inventory`;
  for (let index = 0; index < sampleCount; index += 1) {
    const requestId = `j01-authoring-timing-${index + 1}-${randomUUID()}`;
    const body = j01SemanticInventoryRequest({ snapshotToken, rowRoot, resourceType, query });
    const started = performance.now();
    const { response, value } = await requestJSON(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Request-ID': requestId },
      body: JSON.stringify(body),
      timeout: 30000,
    });
    const elapsedMs = performance.now() - started;
    if (!response.ok) throw new Error(`J01 authoring timing request ${index + 1} returned HTTP ${response.status}`);
    if (typeof value?.contextToken !== 'string' || !value.contextToken) throw new Error('J01 timed catalog response omitted its context identity');
    samples.push(elapsedMs);
    requests.push({ requestId, method: 'POST', path: '/semantic-inventory', status: response.status, elapsedMs, entryCount: value.entries?.length ?? 0 });
  }
  const summary = summarizeTimingSamples(samples);
  report.requests ??= [];
  report.requests.push(...requests.map((request) => ({ ...request, kind: 'warm-semantic-inventory' })));
  report.timings.authoringRequests = summary;
  report.target.authoringRequestSamples = samples;
  if (summary.count < 30) throw new Error(`J01 captured only ${summary.count} authoring request timings`);
  return { summary, requests };
};

const measureJ01InspectorAcknowledgements = async (cdp, label, rowIndex) => evaluate(cdp, `(async () => {
  const selector = ${JSON.stringify(`button[aria-label="Inspect ${label} for row ${rowIndex + 1}"]`)};
  const dialogSelector = ${JSON.stringify(`[role="dialog"][aria-label="${label} record evidence"]`)};
  const samples = [];
  const nextFrame = () => new Promise((resolve) => requestAnimationFrame(resolve));
  for (let index = 0; index < 30; index += 1) {
    const button = document.querySelector(selector);
    if (!button) throw new Error('J01 repeated-cell inspector button is not rendered: ' + selector);
    const started = performance.now();
    button.click();
    while (!document.querySelector(dialogSelector)) await nextFrame();
    samples.push(performance.now() - started);
    const close = [...document.querySelectorAll(dialogSelector + ' button')].find((candidate) => candidate.textContent.trim() === 'Close');
    if (!close) throw new Error('J01 repeated-cell dialog has no Close acknowledgement');
    close.click();
    while (document.querySelector(dialogSelector)) await nextFrame();
  }
  return samples;
})()`);
const interpretationLibrariesURL = (target, project) =>
  `${target.apiUrl}/api/v1/projects/${encodeURIComponent(project)}/interpretation-libraries`;

const interpretationRevisionURL = (target, project, revisionID) =>
  `${target.apiUrl}/api/v1/projects/${encodeURIComponent(project)}/interpretation-revisions/${encodeURIComponent(revisionID)}`;

const configuredColumnContextURL = (target, explorerID) =>
  `${bootstrapAuthoringURL(target, explorerID)}/configured-column-context`;

const interpretationRevisionFromColumnURL = (target, explorerID) =>
  `${bootstrapAuthoringURL(target, explorerID)}/interpretation-revisions`;

const fetchInterpretationLibraries = async (target, project) => {
  const { response, value } = await requestJSON(interpretationLibrariesURL(target, project), { timeout: 30000 });
  if (!response.ok) throw new Error(`interpretation library list failed: HTTP ${response.status} ${JSON.stringify(value).slice(0, 500)}`);
  if (!Array.isArray(value?.libraries)) throw new Error('interpretation library list returned no libraries array');
  return value;
};

const fetchInterpretationRevision = async (target, project, revisionID) => {
  const { response, value } = await requestJSON(interpretationRevisionURL(target, project, revisionID), { timeout: 30000 });
  if (!response.ok) throw new Error(`interpretation revision read failed: HTTP ${response.status} ${JSON.stringify(value).slice(0, 500)}`);
  return value;
};

const createInterpretationRevision = async (target, project, body) => {
  const { response, value } = await requestJSON(interpretationLibrariesURL(target, project), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    timeout: 30000,
  });
  if (!response.ok) throw new Error(`interpretation revision create failed: HTTP ${response.status} ${JSON.stringify(value).slice(0, 500)}`);
  return value;
};

const previewInterpretationCandidate = async (target, explorerID, state, revisionID, limit) => {
  const document = state.workspace?.documents?.[0];
  const column = document?.columns?.find((candidate) => candidate.source?.kind === 'field' && candidate.source.field?.path?.replace(/^root\./, '') === 'id');
  if (!document?.output?.id || !column) throw new Error('B06 verification feature for Patient id is missing from the draft');
  const { response, value } = await requestJSON(
    `${bootstrapAuthoringURL(target, explorerID)}/interpretation-preview`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        snapshotToken: state.catalog.snapshotToken,
        expectedDraftVersion: state.draftVersion,
        expectedDraftDigest: state.draftDigest,
        outputId: document.output.id,
        column: column.column,
        revisionId: revisionID,
        limit,
      }),
      timeout: 60000,
    },
  );
  return { response, value };
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

const verifyInterpretationCandidate = async (target, report, cdp, explorerID, evidenceDir, browserURL) => {
  const started = Date.now();
  const sourceDigestBefore = fixtureSourceDigest(target.fixtureDir);
  const initial = await fetchBuilderState(target, explorerID);
  const document = initial.workspace?.documents?.[0];
  const feature = document?.columns?.find((column) => column.source?.kind === 'field' && column.source.field?.path?.replace(/^root\./, '') === 'id');
  if (!document?.output?.id || !feature) throw new Error('B06 verification feature for Patient id is missing from the draft');
  const libraryID = `b06-map-${Date.now().toString(36)}-${Math.random().toString(16).slice(2, 8)}`;
  const contextRequest = {
    snapshotToken: initial.catalog.snapshotToken,
    expectedDraftVersion: initial.draftVersion,
    expectedDraftDigest: initial.draftDigest,
  };
  const currentContext = await requestJSON(configuredColumnContextURL(target, explorerID), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(contextRequest),
    timeout: 30000,
  });
  const featureContext = currentContext.value?.columns?.find((column) =>
    column.outputId === document.output.id && column.column === feature.column);
  recordAssertion(report, 'configured-column-context-resolves-saved-feature-with-array-identities', true,
    currentContext.response.status === 200 && featureContext?.resolution?.state === 'READY'
      && Array.isArray(featureContext.resolution.capabilityCandidateIds)
      && Array.isArray(featureContext.resolution.applicableRevisionIds));

  const staleContextRequest = { ...contextRequest, expectedDraftDigest: `${initial.draftDigest}-stale` };
  const staleContext = await requestJSON(configuredColumnContextURL(target, explorerID), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(staleContextRequest),
    timeout: 30000,
  });
  recordAssertion(report, 'stale-configured-column-context-is-rejected', 409, staleContext.response.status);
  const staleLibraryID = `${libraryID}-stale`;
  const staleCreate = await requestJSON(interpretationRevisionFromColumnURL(target, explorerID), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      ...staleContextRequest,
      outputId: document.output.id,
      column: feature.column,
      libraryId: staleLibraryID,
      explanation: 'This stale request must not create a mapping.',
    }),
    timeout: 30000,
  });
  recordAssertion(report, 'stale-create-from-column-is-rejected', 409, staleCreate.response.status);
  const afterStaleRequests = await fetchBuilderState(target, explorerID);
  recordAssertion(report, 'stale-context-and-create-leave-draft-unchanged', {
    version: initial.draftVersion,
    digest: initial.draftDigest,
  }, {
    version: afterStaleRequests.draftVersion,
    digest: afterStaleRequests.draftDigest,
  });
  const afterStaleLibraries = await fetchInterpretationLibraries(target, target.fixtureProject);
  recordAssertion(report, 'stale-create-does-not-create-a-library', false,
    afterStaleLibraries.libraries.some((item) => item.library?.id === staleLibraryID));
  const panelExpression = `(() => {
    const panel = [...document.querySelectorAll('div.col-span-full')].find((element) =>
      norm(element.innerText).includes('Interpretation') && /\\bid\\b/.test(element.innerText) &&
      (element.innerText.includes('Current feature meaning is inline') || element.innerText.includes('Pinned revision')));
    if (!panel) throw new Error('Patient id interpretation panel not found');
    return panel;
  })()`;
  const openMappingPanel = async () => {
    await browserEval(cdp, `const panel = ${panelExpression}; const details = [...panel.querySelectorAll('details')].find((element) => norm(element.querySelector('summary')?.textContent) === 'Reusable mappings'); if (!details) throw new Error('reusable mappings control not found'); details.open = true;`);
  };

  await openMappingPanel();
  await browserEval(cdp, `const panel = ${panelExpression}; const input = panel.querySelector('input[placeholder="vitals"]'); const explanation = panel.querySelector('textarea[placeholder="What this feature means"]'); if (!input || !explanation) throw new Error('mapping creation controls not found'); const setValue = (element, value) => { const setter = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(element), 'value')?.set; setter?.call(element, value); element.dispatchEvent(new Event('input', { bubbles: true })); element.dispatchEvent(new Event('change', { bubbles: true })); }; setValue(input, ${JSON.stringify(libraryID)}); setValue(explanation, 'Patient identifier meaning for the verification fixture'); const button = [...panel.querySelectorAll('button')].find((element) => norm(element.textContent) === 'Save reusable mapping'); if (!button) throw new Error('save reusable mapping control not found'); button.click();`);
  await waitForBrowser(cdp, `document.body.innerText.includes(${JSON.stringify(libraryID)})`, 30000);

  const createdList = await fetchInterpretationLibraries(target, target.fixtureProject);
  const createdView = createdList.libraries.find((item) => item.library?.id === libraryID);
  const firstRevision = createdView?.head;
  if (!createdView || !firstRevision) throw new Error(`created interpretation library ${libraryID} did not expose a head revision`);
  const firstRevisionFetched = await fetchInterpretationRevision(target, target.fixtureProject, firstRevision.id);
  recordAssertion(report, 'interpretation-library-creation-exposes-exact-head', true,
    firstRevisionFetched.id === firstRevision.id && firstRevisionFetched.libraryId === libraryID);

  const beforeV1Review = await fetchBuilderState(target, explorerID);
  await openMappingPanel();
  await browserEval(cdp, `const panel = ${panelExpression}; const button = [...panel.querySelectorAll('button')].find((element) => norm(element.textContent) === 'Review'); if (!button) throw new Error('review control not found for created mapping'); button.click();`);
  await waitForBrowser(cdp, `document.body.innerText.includes('Review: Current → With this mapping')`, 60000);
  await browserEval(cdp, `const panel = ${panelExpression}; const button = [...panel.querySelectorAll('button')].find((element) => norm(element.textContent) === 'Apply'); if (!button) throw new Error('v1 review apply control not found'); button.click();`);
  let appliedV1;
  const applyV1Deadline = Date.now() + 30000;
  while (Date.now() < applyV1Deadline) {
    appliedV1 = await fetchBuilderState(target, explorerID);
    const appliedColumn = appliedV1.workspace?.documents?.[0]?.columns?.find((column) => column.column === feature.column);
    if (appliedColumn?.interpretation?.pinned?.revisionId === firstRevision.id) break;
    await sleep(200);
  }
  const appliedV1Column = appliedV1?.workspace?.documents?.[0]?.columns?.find((column) => column.column === feature.column);
  recordAssertion(report, 'interpretation-v1-apply-pins-exact-reviewed-revision', firstRevision.id, appliedV1Column?.interpretation?.pinned?.revisionId);
  recordAssertion(report, 'interpretation-v1-apply-uses-existing-draft-cas', true, appliedV1?.draftVersion > beforeV1Review.draftVersion && appliedV1?.draftDigest !== beforeV1Review.draftDigest);

  const explorersURL = `${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers`;
  const v1ConsumerName = `b06-v1-consumer-${runIDForProject(libraryID)}`;
  const cloned = await requestJSON(explorersURL, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name: v1ConsumerName, title: 'B06 v1 pinned consumer', sourceExplorerId: explorerID }),
    timeout: 30000,
  });
  if (!cloned.response.ok || !cloned.value?.explorerId) throw new Error(`v1 pinned consumer clone failed: HTTP ${cloned.response.status}`);
  const v1ConsumerID = cloned.value.explorerId;
  const v1ConsumerState = await fetchBuilderState(target, v1ConsumerID);
  const v1ConsumerColumn = v1ConsumerState.workspace?.documents?.[0]?.columns?.find((column) => column.column === feature.column);
  recordAssertion(report, 'pre-existing-consumer-clones-v1-pinned-workspace', firstRevision.id, v1ConsumerColumn?.interpretation?.pinned?.revisionId);

  const genderFeature = initial.workspace?.documents?.[0]?.columns?.find((column) => column.source?.kind === 'field' && column.source.field?.path?.replace(/^root\./, '') === 'gender');
  if (!genderFeature) throw new Error('B06 verification feature for Patient gender is missing from the draft');
  const secondRevision = await createInterpretationRevision(target, target.fixtureProject, {
    libraryId: libraryID,
    parentRevisionId: firstRevision.id,
    applicability: firstRevision.applicability,
    rules: firstRevision.rules.map((rule) => ({ ...rule, definition: { ...rule.definition, source: genderFeature.source } })),
    explanation: 'Updated verification head maps Patient identifier to gender',
  });
  const updatedList = await fetchInterpretationLibraries(target, target.fixtureProject);
  const updatedView = updatedList.libraries.find((item) => item.library?.id === libraryID);
  const updatedHead = updatedView?.head;
  recordAssertion(report, 'interpretation-library-head-advances-by-exact-parent-cas', true,
    updatedHead?.id === secondRevision.id && updatedHead?.parentRevisionId === firstRevision.id);
  const secondRevisionFetched = await fetchInterpretationRevision(target, target.fixtureProject, secondRevision.id);
  recordAssertion(report, 'interpretation-exact-get-returns-updated-head', secondRevision.id, secondRevisionFetched.id);

  const cloneBrowserURL = `${target.uiUrl}/?project=${encodeURIComponent(target.fixtureProject)}&explorer=${encodeURIComponent(v1ConsumerID)}&mode=builder`;
  await navigate(cdp, cloneBrowserURL);
  await waitForBrowser(cdp, `document.body.innerText.includes('Feature meanings') && document.body.innerText.includes(${JSON.stringify(`Pinned revision ${firstRevision.id}`)})`, 60000);
  const beforeV2Review = await fetchBuilderState(target, v1ConsumerID);
  await openMappingPanel();
  await browserEval(cdp, `const panel = ${panelExpression}; const button = [...panel.querySelectorAll('button')].find((element) => norm(element.textContent) === 'Review'); if (!button) throw new Error('v2 review control not found'); button.click();`);
  await waitForBrowser(cdp, `document.body.innerText.includes('Review: Current → With this mapping')`, 60000);
  const reviewDOM = await evaluate(cdp, `(() => {
    const norm = (value) => String(value ?? '').replace(/\\s+/g, ' ').trim();
    const table = [...document.querySelectorAll('table')].find((candidate) => {
      const headers = [...candidate.querySelectorAll('thead th')].map((cell) => norm(cell.textContent));
      return headers.join('|') === 'Row|Current|With this mapping|State';
    });
    const panel = [...document.querySelectorAll('div.col-span-full')].find((element) => norm(element.innerText).includes('Review: Current → With this mapping') && /\\bid\\b/.test(element.innerText));
    const text = norm(panel?.innerText);
    return {
      complete: text.includes('The full output was exhausted for this review.'),
      incomplete: text.includes('Sample only: the bounded review did not exhaust the output.'),
      counts: /Compared \\d+ · Changed \\d+ · Resolved \\d+ · Unresolved \\d+/.test(text),
      changed: /Changed [1-9]\\d*/.test(text),
      changedValues: text.includes('dev-patient-001') && text.includes('female'),
      headers: [...(table?.querySelectorAll('thead th') || [])].map((cell) => norm(cell.textContent)),
      rows: [...(table?.querySelectorAll('tbody tr') || [])].map((row) => [...row.querySelectorAll('td')].map((cell) => norm(cell.textContent))),
    };
  })()`);
  recordAssertion(report, 'interpretation-v2-review-renders-before-after-samples', true,
    reviewDOM.headers.join('|') === 'Row|Current|With this mapping|State' && reviewDOM.rows.length > 0);
  recordAssertion(report, 'interpretation-v2-review-shows-changed-values', true, reviewDOM.changed && reviewDOM.changedValues);
  recordAssertion(report, 'interpretation-v2-review-renders-completeness-and-counts', true,
    reviewDOM.complete && reviewDOM.counts);
  await snapshot(cdp, join(evidenceDir, 'interpretation-review.html'));
  recordEvidence(report, join(evidenceDir, 'interpretation-review.html'));
  await browserEval(cdp, `const panel = ${panelExpression}; const button = [...panel.querySelectorAll('button')].find((element) => norm(element.textContent) === 'Cancel'); if (!button) throw new Error('v2 review cancel control not found'); button.click();`);
  await waitForBrowser(cdp, `!document.body.innerText.includes('Review: Current → With this mapping')`);
  const afterV2Cancel = await fetchBuilderState(target, v1ConsumerID);
  recordAssertion(report, 'interpretation-v2-cancel-keeps-draft-unchanged', {
    version: beforeV2Review.draftVersion,
    digest: beforeV2Review.draftDigest,
  }, {
    version: afterV2Cancel.draftVersion,
    digest: afterV2Cancel.draftDigest,
  });
  await openMappingPanel();
  await browserEval(cdp, `const panel = ${panelExpression}; const button = [...panel.querySelectorAll('button')].find((element) => norm(element.textContent) === 'Review'); if (!button) throw new Error('second v2 review control not found'); button.click();`);
  await waitForBrowser(cdp, `document.body.innerText.includes('Review: Current → With this mapping')`, 60000);
  const bounded = await previewInterpretationCandidate(target, v1ConsumerID, afterV2Cancel, secondRevision.id, 1);
  recordAssertion(report, 'interpretation-bounded-preview-reports-incomplete', 200, bounded.response.status);
  recordAssertion(report, 'interpretation-bounded-preview-has-incomplete-completeness', 'INCOMPLETE', bounded.value?.completeness);
  await browserEval(cdp, `const panel = ${panelExpression}; const button = [...panel.querySelectorAll('button')].find((element) => norm(element.textContent) === 'Apply'); if (!button) throw new Error('v2 review apply control not found'); button.click();`);
  let appliedV2;
  const applyV2Deadline = Date.now() + 30000;
  while (Date.now() < applyV2Deadline) {
    appliedV2 = await fetchBuilderState(target, v1ConsumerID);
    const appliedColumn = appliedV2.workspace?.documents?.[0]?.columns?.find((column) => column.column === feature.column);
    if (appliedColumn?.interpretation?.pinned?.revisionId === secondRevision.id) break;
    await sleep(200);
  }
  const appliedV2Column = appliedV2?.workspace?.documents?.[0]?.columns?.find((column) => column.column === feature.column);
  recordAssertion(report, 'interpretation-v2-apply-pins-exact-reviewed-revision', secondRevision.id, appliedV2Column?.interpretation?.pinned?.revisionId);
  recordAssertion(report, 'interpretation-v2-apply-uses-existing-draft-cas', true, appliedV2?.draftVersion > beforeV2Review.draftVersion && appliedV2?.draftDigest !== beforeV2Review.draftDigest);
  const originalAfterV2 = await fetchBuilderState(target, explorerID);
  const originalAfterV2Column = originalAfterV2.workspace?.documents?.[0]?.columns?.find((column) => column.column === feature.column);
  recordAssertion(report, 'pre-existing-consumer-remains-pinned-to-v1-after-v2-head-update', firstRevision.id, originalAfterV2Column?.interpretation?.pinned?.revisionId);

  const sourceDigestAfter = fixtureSourceDigest(target.fixtureDir);
  const digestEvidence = join(evidenceDir, 'interpretation-evidence.json');
  writeJSON(digestEvidence, {
    sourceDigestBefore,
    sourceDigestAfter,
    libraryID,
    firstRevisionID: firstRevision.id,
    secondRevisionID: secondRevision.id,
    firstRevisionContentDigest: firstRevision.contentDigest,
    secondRevisionContentDigest: secondRevision.contentDigest,
    beforeV1ReviewDraft: { version: beforeV1Review.draftVersion, digest: beforeV1Review.draftDigest },
    afterApplyV1Draft: { version: appliedV1.draftVersion, digest: appliedV1.draftDigest },
    beforeV2ReviewDraft: { version: beforeV2Review.draftVersion, digest: beforeV2Review.draftDigest },
    afterApplyV2Draft: { version: appliedV2.draftVersion, digest: appliedV2.draftDigest },
    originalPinnedRevisionAfterV2: originalAfterV2Column?.interpretation?.pinned?.revisionId,
    review: reviewDOM,
    boundedPreview: { status: bounded.response.status, completeness: bounded.value?.completeness, counts: bounded.value?.counts },
  });
  recordEvidence(report, digestEvidence);
  recordAssertion(report, 'fixture-source-digest-survives-interpretation-workflow', sourceDigestBefore, sourceDigestAfter);

  await cdp.send('Page.reload', { ignoreCache: false });
  await waitForBrowser(cdp, `document.readyState === 'complete'`);
  await waitForBrowser(cdp, `document.body.innerText.includes('Feature meanings') && document.body.innerText.includes(${JSON.stringify(`Pinned revision ${secondRevision.id}`)})`, 60000);
  await waitForBrowser(cdp, `document.body.innerText.includes(${JSON.stringify(secondRevisionFetched.explanation)}) && document.body.innerText.includes('authored by')`, 30000);
  const reloaded = await fetchBuilderState(target, v1ConsumerID);
  const reloadedColumn = reloaded.workspace?.documents?.[0]?.columns?.find((column) => column.column === feature.column);
  recordAssertion(report, 'reload-preserves-exact-v2-pinned-provenance-after-head-update', secondRevision.id, reloadedColumn?.interpretation?.pinned?.revisionId);
  recordAssertion(report, 'reload-exposes-pinned-explanation-and-author', true,
    String(await evaluate(cdp, 'document.body.innerText')).includes(secondRevisionFetched.explanation) && String(await evaluate(cdp, 'document.body.innerText')).includes('authored by'));
  await snapshot(cdp, join(evidenceDir, 'interpretation-pinned-v2-reload.html'));
  recordEvidence(report, join(evidenceDir, 'interpretation-pinned-v2-reload.html'));

  const wrongProject = `loom_dev_scope_${runIDForProject(libraryID)}`;
  const wrongList = await requestJSON(interpretationLibrariesURL(target, wrongProject), { timeout: 30000 });
  recordAssertion(report, 'interpretation-library-list-is-project-scoped', true,
    wrongList.response.ok && !wrongList.value?.libraries?.some((item) => item.library?.id === libraryID));
  const wrongRevision = await request(interpretationRevisionURL(target, wrongProject, firstRevision.id), { timeout: 30000 });
  recordAssertion(report, 'interpretation-exact-get-rejects-wrong-project', 404, wrongRevision.status);
  recordLimitation(report, 'auth-denial-under-local-allow-all', 'The verify-fast Compose entrypoint starts the API with --no-auth and AllowAllAuthorizer; no honest 401/403 denial assertion is possible in this local stack.');
  await navigate(cdp, browserURL);
  await waitForBrowser(cdp, `document.body.innerText.includes('Feature meanings') && document.body.innerText.includes(${JSON.stringify(`Pinned revision ${firstRevision.id}`)})`, 60000);
  await waitForBrowser(cdp, `document.body.innerText.includes(${JSON.stringify(firstRevisionFetched.explanation)}) && document.body.innerText.includes('authored by')`, 30000);
  const originalReloaded = await fetchBuilderState(target, explorerID);
  const originalReloadedColumn = originalReloaded.workspace?.documents?.[0]?.columns?.find((column) => column.column === feature.column);
  recordAssertion(report, 'original-consumer-reload-keeps-v1-provenance', firstRevision.id, originalReloadedColumn?.interpretation?.pinned?.revisionId);
  report.target.interpretation = {
    libraryID,
    firstRevisionID: firstRevision.id,
    updatedHeadRevisionID: secondRevision.id,
    pinnedRevisionIDAfterReload: reloadedColumn?.interpretation?.pinned?.revisionId,
    v1ConsumerExplorerID: v1ConsumerID,
    originalPinnedRevisionID: originalAfterV2Column?.interpretation?.pinned?.revisionId,
    projectScopeProject: wrongProject,
    authDenial: 'not-proven-local-allow-all',
  };
  report.timings.interpretation_workflow_ms = Date.now() - started;
};

const runIDForProject = (value) => value.replace(/[^a-z0-9_]/gi, '_').slice(-40);

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
    await waitForBrowser(cdp, `document.body.innerText.includes('Build your features') || document.body.innerText.includes('Create your first table')`, 60000);
    recordAssertion(
      report,
      'bare-development-entry-loads-owned-bootstrap',
      `${entryTarget.fixtureProject} / loom-dev-bootstrap`,
      await evaluate(cdp, `document.querySelector('.demo-controls span')?.textContent.trim() || ''`),
    );
    await snapshot(cdp, join(evidenceDir, 'bare-entry.html'));
    recordEvidence(report, join(evidenceDir, 'bare-entry.html'));

    await navigate(cdp, browserURL);
    await waitForBrowser(cdp, `document.body.innerText.includes('Build your features') || document.body.innerText.includes('Create your first table')`, 60000);
    await snapshot(cdp, join(evidenceDir, 'builder-initial.html'));
    recordEvidence(report, join(evidenceDir, 'builder-initial.html'));

    const ownerRecordsTitle = `Loom owner records ${target.fixtureProject.slice(-16)}`;
    await browserEval(cdp, `clickText('summary', 'New explorer')`);
    await browserEval(cdp, `setInput('new-explorer-name', ${JSON.stringify(ownerRecordsTitle)})`);
    await browserEval(cdp, `clickButton('Create blank')`);
    await waitForBrowser(cdp, `document.querySelector('select[aria-label="Explorer"] option:checked')?.textContent.trim() === ${JSON.stringify(ownerRecordsTitle)} && document.body.innerText.includes('Create your first table')`);
    const ownerRecordsExplorerId = await evaluate(cdp, `document.querySelector('select[aria-label="Explorer"]')?.value || ''`);
    recordAssertion(report, 'owner-records-browser-selected-owned-explorer', true, Boolean(ownerRecordsExplorerId && ownerRecordsExplorerId !== bootstrapExplorerId));
    report.target.ownerRecordsExplorerId = ownerRecordsExplorerId;

    await browserEval(cdp, `setInput('first-table-name', 'Observation owner records')`);
    await browserEval(cdp, `clickButton('Create table')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('What should one row represent?') && document.body.innerText.includes('Observation owner records')`);
    await browserEval(cdp, `const button = document.querySelector('button[aria-label="Choose Observation rows"]'); if (!button || button.disabled) throw new Error('Observation row choice is unavailable'); button.click();`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Search fields and concepts') && Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Search' && !button.disabled))`);
    await browserEval(cdp, `setInput('Search features by field name, concept, or code', 'shared')`);
    await browserEval(cdp, `clickButton('Search')`);
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('article')].find((article) => article.textContent.includes('urn:study:A · shared') && article.textContent.includes('valueQuantity.value') && article.querySelector('input[type="checkbox"]:not(:disabled)')))`);
    await browserEval(cdp, `
      const item = [...document.querySelectorAll('article')].find((article) =>
        article.textContent.includes('urn:study:A · shared') &&
        article.textContent.includes('valueQuantity.value')
      );
      const input = item?.querySelector('input[type="checkbox"]');
      if (!input || input.disabled) throw new Error('repeated coded Quantity is not selectable');
      input.click();
    `);
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Add 1 selected feature' && !button.disabled))`);
    await browserEval(cdp, `clickButton('Add 1 selected feature')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Choose output forms') && Boolean(document.querySelector('[aria-label="shared: Keep each matching record"]'))`);
    await browserEval(cdp, `const input = inputByLabel('shared: Keep each matching record'); if (!input) throw new Error('OWNER_RECORDS form is missing'); input.click();`);
    await browserEval(cdp, `clickButton('Add 1 selected feature')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Display name for configured shared"]'))`);
    const ownerRecordsBuilder = await fetchBuilderState(target, ownerRecordsExplorerId);
    const ownerRecordsColumn = ownerRecordsBuilder.workspace.documents[0].columns.find((column) => column.label === 'shared');
    recordAssertion(report, 'builder-persists-owner-record-construction', {
      kind: 'ownerRecords',
      system: 'urn:study:A',
      code: 'shared',
      ownerPath: 'component[]',
      valuePath: 'valueQuantity.value',
    }, {
      kind: ownerRecordsColumn?.source?.kind,
      system: ownerRecordsColumn?.source?.ownerRecords?.key?.system,
      code: ownerRecordsColumn?.source?.ownerRecords?.key?.code,
      ownerPath: ownerRecordsColumn?.source?.ownerRecords?.binding?.ownerPath,
      valuePath: ownerRecordsColumn?.source?.ownerRecords?.binding?.valuePath,
    });
    await browserEval(cdp, `clickButton('Preview')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Preview and configure') && Boolean([...document.querySelectorAll('button[aria-label^="Inspect shared for row "]')].find((button) => button.title.includes('dev-pair-001'))) && Boolean([...document.querySelectorAll('button[aria-label^="Inspect shared for row "]')].find((button) => button.title.includes('dev-pair-002')))`, 60000);
    await browserEval(cdp, `
      const button = [...document.querySelectorAll('button[aria-label^="Inspect shared for row "]')]
        .find((candidate) => candidate.title.includes('dev-pair-002'));
      if (!button) throw new Error('invalid owner-record preview cell is missing');
      button.click();
    `);
    await waitForBrowser(cdp, `Boolean(document.querySelector('[role="dialog"][aria-label="shared record evidence"]')) && document.body.innerText.includes('Repeated FHIR records preserved in this cell')`);
    const invalidOwnerRecordEvidence = String(await evaluate(cdp, `document.querySelector('[role="dialog"][aria-label="shared record evidence"]')?.innerText || ''`));
    recordAssertion(report, 'preview-owner-record-inspector-exposes-invalid-choice-arm', true,
      invalidOwnerRecordEvidence.includes('INVALID_CHOICE_ARM') &&
      invalidOwnerRecordEvidence.includes('dev-pair-002'));
    await browserEval(cdp, `clickButton('Close')`);
    await browserEval(cdp, `
      const button = [...document.querySelectorAll('button[aria-label^="Inspect shared for row "]')]
        .find((candidate) => candidate.title.includes('dev-pair-001'));
      if (!button) throw new Error('valid owner-record preview cell is missing');
      button.click();
    `);
    await waitForBrowser(cdp, `Boolean(document.querySelector('[role="dialog"][aria-label="shared record evidence"]')) && document.body.innerText.includes('Repeated FHIR records preserved in this cell')`);
    const ownerRecordEvidence = String(await evaluate(cdp, `document.querySelector('[role="dialog"][aria-label="shared record evidence"]')?.innerText || ''`));
    await snapshot(cdp, join(evidenceDir, 'owner-record-evidence.html'));
    recordEvidence(report, join(evidenceDir, 'owner-record-evidence.html'));
    recordAssertion(report, 'preview-owner-record-inspector-preserves-value-unit-code-and-source', true,
      ownerRecordEvidence.includes('111') &&
      ownerRecordEvidence.includes('cm') &&
      ownerRecordEvidence.includes('urn:study:A') &&
      ownerRecordEvidence.includes('shared') &&
      ownerRecordEvidence.includes('dev-pair-001'));
    await browserEval(cdp, `clickButton('Close')`);

    await navigate(cdp, browserURL);
    await waitForBrowser(cdp, `document.body.innerText.includes('Build your features') || document.body.innerText.includes('Create your first table')`, 60000);

    const verificationTitle = `Loom dev verification ${target.fixtureProject.slice(-16)}`;
    await browserEval(cdp, `clickText('summary', 'New explorer')`);
    await browserEval(cdp, `setInput('new-explorer-name', ${JSON.stringify(verificationTitle)})`);
    await browserEval(cdp, `clickButton('Create blank')`);
    await waitForBrowser(cdp, `document.querySelector('select[aria-label="Explorer"] option:checked')?.textContent.trim() === ${JSON.stringify(verificationTitle)} && document.body.innerText.includes('Create your first table')`);
    explorerId = await evaluate(cdp, `document.querySelector('select[aria-label="Explorer"]')?.value || ''`);
    recordAssertion(report, 'browser-selected-owned-verification-explorer', true, Boolean(explorerId && explorerId !== bootstrapExplorerId));
    report.target.explorerId = explorerId;
    const verificationBrowserURL = `${target.uiUrl}/?project=${encodeURIComponent(target.fixtureProject)}&explorer=${encodeURIComponent(explorerId)}&mode=builder`;

    await browserEval(cdp, `setInput('first-table-name', 'Patients with observations')`);
    await browserEval(cdp, `clickButton('Create table')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('What should one row represent?') && document.body.innerText.includes('Patients with observations')`);
    await browserEval(cdp, `const button = document.querySelector('button[aria-label="Choose Patient rows"]'); if (!button || button.disabled) throw new Error('Patient row choice is unavailable'); button.click();`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Search fields and concepts')`);
    await browserEval(cdp, `setInput('Search features by field name, concept, or code', 'id')`);
    await browserEval(cdp, `clickButton('Search')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Select Patient.id"]:not(:disabled)'))`);
    await browserEval(cdp, `
      const input = inputByLabel('Select Patient.id');
      if (!input) throw new Error('root id field is missing from Find features');
      if (input.disabled) throw new Error('root id field is not selectable');
      input.click();
    `);
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Add 1 selected feature' && !button.disabled))`);
    await browserEval(cdp, `clickButton('Add 1 selected feature')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Display name for configured id"]'))`);
    const catalogBuilder = await fetchBuilderState(target, explorerId);
    const catalogIDColumn = catalogBuilder.workspace.documents[0].columns.find((column) => column.label === 'id');
    recordAssertion(report, 'builder-catalog-adds-default-root-field-without-graph', {
      kind: 'field',
      path: 'id',
      projectionMode: 'VALUE',
      graphVisible: false,
    }, {
      kind: catalogIDColumn?.source?.kind,
      path: catalogIDColumn?.source?.field?.path,
      projectionMode: catalogIDColumn?.source?.field?.projectionMode,
      graphVisible: await evaluate(cdp, `document.body.innerText.includes('Dataset graph')`),
    });
    await snapshot(cdp, join(evidenceDir, 'builder-catalog-column.html'));
    recordEvidence(report, join(evidenceDir, 'builder-catalog-column.html'));
    await browserEval(cdp, `clickButton('Advanced graph')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Current query') && document.body.innerText.includes('Patient columns')`);
    await browserEval(cdp, `clickCandidate('name[].family', 'to table')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Display name for configured name[].family"]'))`);
    await browserEval(cdp, `clickCandidate('gender', 'as filter')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Display name for configured gender"]'))`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Display name for configured id"]') && document.querySelector('input[aria-label="Display name for configured name[].family"]') && document.querySelector('input[aria-label="Display name for configured gender"]'))`);
    const configuredFields = await evaluate(cdp, `([...document.querySelectorAll('input[aria-label^="Display name for configured "]')].map((input) => input.getAttribute('aria-label')).sort())`);
    recordAssertion(report, 'builder-configures-exact-root-fields', true, ['Display name for configured gender', 'Display name for configured id', 'Display name for configured name[].family'].every((label) => configuredFields.includes(label)));
    await verifyInterpretationCandidate(target, report, cdp, explorerId, evidenceDir, verificationBrowserURL);

    await waitForBrowser(cdp, `document.body.innerText.includes('Concept catalog')`);
    await browserEval(cdp, `clickButton('Advanced graph')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Dataset graph')`);
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
    const observationNode = equalityBuilder.catalog.nodes.find((node) => node.resourceType === 'Observation');
    const statusCandidate = equalityBuilder.catalog.candidates.find((candidate) => candidate.nodeId === observationNode?.nodeId && candidate.fieldPath === 'status');
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
    await browserEval(cdp, `
      const row = [...document.querySelectorAll('table[aria-label$=" results"] tbody tr')]
        .find((candidate) => norm(candidate.querySelector('td')?.textContent) === 'dev-patient-002');
      const explain = [...(row?.querySelectorAll('button[aria-label]') || [])]
        .find((candidate) => candidate.getAttribute('aria-label')?.startsWith('Explain name[].family [1] for row '));
      if (!explain) throw new Error('recorded-null family cell was not found for dev-patient-002');
      explain.click();
    `);
    await waitForBrowser(cdp, `document.body.innerText.includes('A matching source record exists, but its selected value is empty.')`, 30000);
    await browserEval(cdp, `clickButton('Review null handling')`);
    await waitForBrowser(cdp, `new URL(window.location.href).searchParams.get('mode') === 'builder' && Boolean(document.querySelector('[data-feature-focus="true"]'))`, 60000);
    const repairFocus = await evaluate(cdp, `({
      mode: new URL(window.location.href).searchParams.get('mode'),
      output: new URL(window.location.href).searchParams.get('focusOutput'),
      column: new URL(window.location.href).searchParams.get('focusColumn'),
      search: document.querySelector('input[aria-label="Search columns"]')?.value ?? '',
      highlighted: document.querySelector('[data-feature-focus="true"]')?.getAttribute('data-feature-focus') ?? '',
    })`);
    recordAssertion(report, 'viewer-null-repair-focuses-exact-builder-feature', true,
      repairFocus.mode === 'builder'
      && Boolean(repairFocus.output)
      && Boolean(repairFocus.column)
      && repairFocus.search === repairFocus.column
      && repairFocus.highlighted === 'true');
    await snapshot(cdp, join(evidenceDir, 'builder-focused-null-repair.html'));
    recordEvidence(report, join(evidenceDir, 'builder-focused-null-repair.html'));
    await browserEval(cdp, `clickButton('Viewer')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Published') && document.body.innerText.includes('dev-patient-001')`, 60000);
    await browserEval(cdp, `clickButton('Load values')`);
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('label')].find((candidate) => candidate.textContent.trim().startsWith('female')))`);
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

    await browserEval(cdp, `clickButton('Explain valueQuantity.value for row 1')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Why is valueQuantity.value ${maximumRelatedValue}?') && document.body.innerText.includes('source records contributed before Loom applied the feature rule.')`, 30000);
    await browserEval(cdp, `clickText('summary', 'Source details (2)')`);
    const cellExplanation = await evaluate(cdp, `(() => {
      const dialog = [...document.querySelectorAll('[role="dialog"]')].find((candidate) => candidate.innerText.includes('Why is valueQuantity.value'));
      return dialog?.innerText ?? '';
    })()`);
    recordAssertion(report, 'viewer-explains-related-aggregate-with-exact-fhir-sources', true,
      cellExplanation.includes('dev-observation-001')
      && cellExplanation.includes('172.5')
      && cellExplanation.includes('dev-observation-003')
      && cellExplanation.includes('180'));
    await snapshot(cdp, join(evidenceDir, 'viewer-cell-explanation.html'));
    recordEvidence(report, join(evidenceDir, 'viewer-cell-explanation.html'));
    await browserEval(cdp, `clickButton('Close cell explanation')`);
    await waitForBrowser(cdp, `![...document.querySelectorAll('[role="dialog"]')].some((candidate) => candidate.innerText.includes('Why is valueQuantity.value'))`);

    const trainingArtifactDownloadStarted = Date.now();
    await browserEval(cdp, `clickButton('Download training artifact')`);
    const archivePath = await findDownloadedArchive(downloadDir);
    report.timings.training_artifact_download_ms = Date.now() - trainingArtifactDownloadStarted;
    report.target.trainingArtifactBytes = statSync(archivePath).size;
    recordEvidence(report, archivePath);
    const archive = readStoredZip(archivePath);
    const requiredMembers = ['data.csv', 'schema.json', 'provenance.json', 'quality.json', 'README.md', 'manifest.json'];
    recordAssertion(report, 'training-artifact-has-fixed-members', requiredMembers, [...archive.keys()]);
    const manifest = JSON.parse(archive.get('manifest.json').toString('utf8'));
    const schema = JSON.parse(archive.get('schema.json').toString('utf8'));
    const csvRows = parseCSV(archive.get('data.csv').toString('utf8'));
    recordAssertion(report, 'training-artifact-is-bound-to-published-revision', {
      project: canonicalProjectID(target.fixtureProject),
      datasetGeneration: target.fixtureGeneration,
      executionId: runtime.publication.executionId,
      outputId: output.outputId,
      revisionId: runtime.publication.revisionId,
    }, {
      project: manifest.identity.project,
      datasetGeneration: manifest.identity.datasetGeneration,
      executionId: manifest.identity.executionId,
      outputId: manifest.identity.outputId,
      revisionId: manifest.identity.revisionId,
    });
    recordAssertion(report, 'training-artifact-schema-matches-data', schema.columns.map((column) => column.name), csvRows[0] ?? []);
    recordAssertion(report, 'training-artifact-has-full-published-row-count', 2, manifest.rows);
    const artifactIDIndex = (csvRows[0] ?? []).indexOf(idColumn.runtime.column);
    recordAssertion(report, 'training-artifact-has-row-identity-column', true, artifactIDIndex >= 0);
    recordAssertion(report, 'training-artifact-has-full-published-membership', ['dev-patient-001', 'dev-patient-002'], csvRows.slice(1).map((row) => row[artifactIDIndex]).sort());
    recordAssertion(report, 'training-artifact-member-checksums-match', true, manifest.members.every((member) => {
      const bytes = archive.get(member.name);
      return Boolean(bytes) && bytes.length === member.bytes && createHash('sha256').update(bytes).digest('hex') === member.sha256;
    }));

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

const verifyCurrentBuilderDOM = async (target, report, explorerId, builderState) => {
  if (!explorerId) throw new Error('current development target has no bootstrap Explorer');
  const evidenceDirectory = join(target.artifacts, `current-${Date.now().toString(36)}`);
  mkdirSync(evidenceDirectory, { recursive: true, mode: 0o700 });
  const browser = await launchBrowser(evidenceDirectory);
  const url = `${target.uiUrl}/?project=${encodeURIComponent(target.fixtureProject)}&explorer=${encodeURIComponent(explorerId)}&mode=builder`;
  try {
    await navigate(browser.cdp, url);
    await waitForBrowser(browser.cdp, `
      document.querySelector('#root')?.childElementCount > 0 &&
      document.body.innerText.trim().length > 0 &&
      !document.body.innerText.includes('Loading Explorer') &&
      !document.body.innerText.includes('Loading the selected Explorer configuration')
    `, 120000);
    const state = await browserEval(browser.cdp, `
      const text = norm(document.body.innerText);
      return {
        title: document.title,
        text,
        hasLoadFailure: text.includes('Builder state could not be loaded') || text.includes('no V1 fallback'),
        renderedValues: [
          ...document.querySelectorAll('input, textarea, select'),
        ].map((element) => norm(element.value)).filter(Boolean),
        visibleButtons: [...document.querySelectorAll('button')].filter(visible).map((button) => norm(button.getAttribute('aria-label') || button.textContent)).filter(Boolean),
      };
    `);
    const domPath = join(evidenceDirectory, 'builder.html');
    await snapshot(browser.cdp, domPath);
    recordEvidence(report, domPath);
    const screenshot = await browser.cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
    const screenshotPath = join(evidenceDirectory, 'builder.png');
    writeFileSync(screenshotPath, Buffer.from(screenshot.data, 'base64'), { mode: 0o600 });
    recordEvidence(report, screenshotPath);
    report.target.browserUrl = url;
    report.target.visibleButtons = state.visibleButtons;
    const expectedTableTitles = (builderState?.workspace?.documents ?? [])
      .map((document) => document.output?.title)
      .filter(Boolean);
    const renderedText = `${state.text}\n${state.renderedValues.join('\n')}`;
    recordAssertion(report, 'current-builder-renders', true, state.text.length > 0);
    recordAssertion(report, 'current-builder-has-no-load-failure', false, state.hasLoadFailure);
    recordAssertion(report, 'current-builder-shows-current-workspace', true,
      expectedTableTitles.length > 0 && expectedTableTitles.every((title) => renderedText.includes(title)));
    report.target.workspaceTables = expectedTableTitles;
    await measureHotReload(target, report, browser.cdp);
  } finally {
    await browser.close();
  }
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
  const j01Scenario = target.fixtureDir === FIXTURE_DIR
    ? 'S01-J01-three-column-choice-preview-persistence-export'
    : 'S01-J01-CDA-patient-and-observation-publish-export';
  const report = createVerificationReport(target,
    command === 'verify-current' ? 'current-builder-hotreload'
      : command === 'verify-j01' ? j01Scenario
        : command === 'verify-j02' ? 'S02-J02-related-column-route-edit-persistence'
          : command === 'verify-j05' ? 'S05-UI05-builder-review-viewer-dataset-artifact'
          : undefined);
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
    if (command === 'verify-current') {
      const result = await doctor(target);
      recordAssertion(report, 'development-api-ready', 200, result.api);
      recordAssertion(report, 'development-ui-served', 200, result.ui);
      recordAssertion(report, 'fixture-generation-present', 200, result.generation);
      recordAssertion(report, 'development-build-is-fresh', true, result.buildBarrier >= 0);
      await verifyCurrentBuilderDOM(target, report, result.bootstrapExplorerId, result.builderState);
      report.status = 'passed';
      report.timings.total_ms = Date.now() - commandStarted;
      writeJSON(join(target.artifacts, 'report.json'), report);
      console.log(`DEV_CURRENT_VERIFY_PASSED project=${target.fixtureProject} evidence=${report.evidencePaths[0] ? dirname(report.evidencePaths[0]) : target.artifacts}`);
      return;
    }
    if (command === 'verify-j02') {
      await ensureDev(target, report);
      const verificationTarget = createVerificationTarget(target, `${Date.now().toString(36)}-${randomUUID().slice(0, 8)}`);
      const verificationReport = createVerificationReport(verificationTarget, 'S02-J02-related-column-route-edit-persistence');
      activeReport = verificationReport;
      verificationReport.timings.startup_ms = report.timings.startup_ms;
      verificationReport.timings.api_build_barrier_ms = report.timings.api_build_barrier_ms;
      const seed = await seedFixture(verificationTarget, { requireFresh: true, populateBootstrap: false });
      if (seed.reused || !seed.fresh) throw new Error(`J02 verification fixture was unexpectedly reused: ${verificationTarget.fixtureProject}`);
      verificationReport.target.fixtureSeed = 'seeded';
      verificationReport.target.bootstrapExplorerId = seed.bootstrapExplorerId;
      recordAssertion(verificationReport, 'j02-starts-from-fresh-isolated-fixture-and-editor-identity', true,
        seed.fresh && !seed.reused && Boolean(seed.bootstrapExplorerId));
      await verifyJ02BrowserScenario(verificationTarget, verificationReport, target);
      verificationReport.status = 'passed';
      verificationReport.timings.total_ms = Date.now() - commandStarted;
      writeJSON(join(verificationReport.target.evidenceDirectory, 'report.json'), verificationReport);
      writeJSON(join(verificationTarget.artifacts, 'report.json'), verificationReport);
      console.log(`DEV_J02_VERIFY_PASSED project=${verificationTarget.fixtureProject} evidence=${verificationTarget.artifacts}`);
      return;
    }
    if (command === 'verify-j05') {
      await ensureDev(target, report);
      const verificationTarget = createVerificationTarget(target, `${Date.now().toString(36)}-${randomUUID().slice(0, 8)}`);
      const verificationReport = createVerificationReport(verificationTarget, 'S05-UI05-builder-review-viewer-dataset-artifact');
      activeReport = verificationReport;
      verificationReport.timings.startup_ms = report.timings.startup_ms;
      verificationReport.timings.api_build_barrier_ms = report.timings.api_build_barrier_ms;
      const seed = await seedFixture(verificationTarget, { requireFresh: true, populateBootstrap: false });
      if (seed.reused || !seed.fresh) throw new Error(`J05 verification fixture was unexpectedly reused: ${verificationTarget.fixtureProject}`);
      verificationReport.target.fixtureSeed = 'seeded';
      verificationReport.target.bootstrapExplorerId = seed.bootstrapExplorerId;
      recordAssertion(verificationReport, 'j05-starts-with-fresh-isolated-fixture-and-empty-bootstrap-explorer', true,
        seed.fresh && !seed.reused && Boolean(seed.bootstrapExplorerId));
      await verifyJ05BrowserScenario(verificationTarget, verificationReport, target);
      verificationReport.status = 'passed';
      verificationReport.timings.total_ms = Date.now() - commandStarted;
      writeJSON(join(verificationReport.target.evidenceDirectory, 'report.json'), verificationReport);
      writeJSON(join(verificationTarget.artifacts, 'report.json'), verificationReport);
      console.log(`DEV_J05_VERIFY_PASSED project=${verificationTarget.fixtureProject} evidence=${verificationReport.target.evidenceDirectory}`);
      return;
    }
    if (command === 'verify-j01') {
      const externalManifest = j01Scenario === 'S01-J01-CDA-patient-and-observation-publish-export'
        ? await selectExternalJ01Manifest(target.fixtureDir)
        : undefined;
      if (externalManifest) {
        assertExternalJ01SourcesUnchanged(externalManifest);
        report.target.externalManifest = externalManifest.summary;
      }
      await ensureDev(target, report, false, externalManifest);
      if (externalManifest) assertExternalJ01SourcesUnchanged(externalManifest);
      const verificationTarget = createVerificationTarget(target, `${Date.now().toString(36)}-${randomUUID().slice(0, 8)}`);
      const verificationReport = createVerificationReport(verificationTarget, report.scenario);
      activeReport = verificationReport;
      verificationReport.timings.startup_ms = report.timings.startup_ms;
      verificationReport.timings.api_build_barrier_ms = report.timings.api_build_barrier_ms;
      if (externalManifest) verificationReport.target.externalManifest = externalManifest.summary;
      const seed = await seedFixture(verificationTarget, { requireFresh: true, populateBootstrap: false, fixtureManifest: externalManifest });
      if (seed.reused || !seed.fresh) throw new Error(`J01 verification fixture was unexpectedly reused: ${verificationTarget.fixtureProject}`);
      verificationReport.target.fixtureSeed = 'seeded';
      verificationReport.target.bootstrapExplorerId = seed.bootstrapExplorerId;
      recordAssertion(verificationReport, 'j01-starts-with-fresh-isolated-fixture-and-editor-identity', true,
        seed.fresh && !seed.reused && Boolean(seed.bootstrapExplorerId));
      if (externalManifest) {
        recordAssertion(verificationReport, 'j01-external-manifest-digest-is-bound-to-ingested-fixture', externalManifest.summary.sourceSHA256, seed.fixtureManifest?.sourceSHA256);
        assertExternalJ01SourcesUnchanged(externalManifest);
      }
      await verifyJ01BrowserScenario(verificationTarget, verificationReport, target, externalManifest);
      verificationReport.status = 'passed';
      verificationReport.timings.total_ms = Date.now() - commandStarted;
      writeJSON(join(verificationReport.target.evidenceDirectory, 'report.json'), verificationReport);
      writeJSON(join(verificationTarget.artifacts, 'report.json'), verificationReport);
      console.log(`DEV_J01_VERIFY_PASSED project=${verificationTarget.fixtureProject} evidence=${verificationReport.target.evidenceDirectory}`);
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
    throw new Error(`unknown command ${command}; use dev, dev-doctor, verify-current, verify-fast, verify-full, verify-j01, verify-j02, verify-j05, dev-rebuild, or dev-down [--purge]`);
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
