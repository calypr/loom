#!/usr/bin/env node

import { spawn } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { closeSync, createReadStream, existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, readdirSync, renameSync, rmSync, statSync, unlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, basename, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createInterface } from 'node:readline';
import { dataframeOutputQuery } from '../ui/packages/loom-ui/src/dataframeOutputQuery.mjs';
import { EXPLORER_AUTHORING_SEMANTICS_VERSION } from '../ui/packages/loom-ui/src/authoringSemanticsVersion.mjs';

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
export const AUTHORING_SEMANTICS_VERSION = EXPLORER_AUTHORING_SEMANTICS_VERSION;

export const explicitGroupPreviewRows = (rows) => rows.map((row) => {
  const identity = row.__loom_row_id;
  const validIdentity = identity && typeof identity === 'object' &&
    typeof identity.group_revision_id === 'string' && typeof identity.group_id === 'string';
  const groupID = row.group_id ?? (validIdentity ? identity.group_id : '');
  const revisionID = row.group_revision_id ?? (validIdentity ? identity.group_revision_id : '');
  return {
    rowIdentity: validIdentity
      ? `map[group_id:${identity.group_id} group_revision_id:${identity.group_revision_id}]`
      : '',
    groupRevisionId: revisionID,
    groupId: groupID,
    sourceMemberIDs: Array.isArray(row.members)
      ? row.members.map((member) => member?.source_identity?.id).filter(Boolean).sort()
      : [],
    rawIdentity: identity,
  };
});

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

export const assertOwnedDevSession = async (target) => inspectOwnedResources(target, { requirePorts: true });

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

export const createFreshVerificationFixture = async (session, runID) => {
  if (!/^[a-z0-9][a-z0-9-]{0,24}$/.test(runID)) throw new Error('verification run id must be a short lowercase slug');
  const target = createVerificationTarget(session, runID);
  const seed = await seedFixture(target, { requireFresh: true, populateBootstrap: false });
  if (seed.reused || !seed.fresh) throw new Error(`verification fixture was unexpectedly reused: ${target.fixtureProject}`);
  return Object.freeze({ target, seed });
};

const seedJ03ExplicitGroupRevision = async (target, explorerId) => {
  const builder = await fetchBuilderState(target, explorerId);
  if (!builder.catalog?.snapshotToken) throw new Error('J03 cannot seed groups without a current Builder snapshot');
  const sourceMemberIDs = readFileSync(join(target.fixtureDir, 'Patient.ndjson'), 'utf8')
    .trim().split('\n').map((line) => JSON.parse(line).id).sort().slice(0, 3);
  if (sourceMemberIDs.length !== 3) throw new Error('J03 group-authoring fixture requires exactly three selected records');
  const selectionURL = `${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorerId)}/selections`;
  const selection = await requestJSON(selectionURL, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, timeout: 30000,
    body: JSON.stringify({
      idempotencyKey: `loom-dev-j03-source-${target.fixtureProject}`,
      snapshotToken: builder.catalog.snapshotToken,
      source: {
        kind: 'resources',
        resources: {
          resourceType: 'Patient',
          refs: sourceMemberIDs.map((id) => ({
            project: target.fixtureProject, generation: target.fixtureGeneration, resourceType: 'Patient', id,
          })),
        },
      },
    }),
  });
  if (selection.response.status !== 201 || selection.value.complete !== true || selection.value.memberCount !== 3) {
    throw new Error(`J03 source selection returned HTTP ${selection.response.status}: ${JSON.stringify(selection.value).slice(0, 700)}`);
  }
  return { selection: selection.value, sourceMemberIDs };
};

const seedJ04PatientSelectionRevision = async (target, explorerId, contract, selectedRowIdentities = contract.aggregateScope.selectedRowIdentities) => {
  const builder = await fetchBuilderState(target, explorerId);
  if (!builder.catalog?.snapshotToken) throw new Error('J04 cannot seed Patient selection without a current Builder snapshot');
  const plan = j04PatientSelectionSeedPlan({
    ...contract,
    aggregateScope: { ...contract.aggregateScope, selectedRowIdentities },
  });
  const refs = plan.refs.map((ref) => ({
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    ...ref,
  }));
  const selectionURL = `${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorerId)}/selections`;
  const created = await requestJSON(selectionURL, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, timeout: 30000,
    body: JSON.stringify({
      idempotencyKey: `loom-dev-j04-patient-${target.fixtureProject}-${explorerId}-${refs.map(({ id }) => id).sort().join('-')}`,
      snapshotToken: builder.catalog.snapshotToken,
      source: { kind: 'resources', resources: { resourceType: plan.resourceType, refs } },
    }),
  });
  if (created.response.status !== 201 || created.value.complete !== true || created.value.memberCount !== plan.memberCount) {
    throw new Error(`J04 Patient selection seed returned HTTP ${created.response.status}: ${JSON.stringify(created.value).slice(0, 700)}`);
  }

  const members = [];
  let cursor;
  do {
    const query = new URLSearchParams({ limit: '1', ...(cursor ? { cursor } : {}) });
    const page = await requestJSON(`${selectionURL}/${encodeURIComponent(created.value.id)}?${query}`, { timeout: 30000 });
    if (!page.response.ok || page.value.revision?.complete !== true
      || page.value.revision?.membershipDigest !== created.value.membershipDigest
      || page.value.revision?.memberCount !== plan.memberCount) {
      throw new Error(`J04 Patient selection page did not match immutable revision ${created.value.id}: ${JSON.stringify(page.value).slice(0, 700)}`);
    }
    members.push(...(page.value.members ?? []).map(({ ref }) => ref));
    if (members.length > plan.memberCount) throw new Error('J04 Patient selection pagination repeated a member');
    cursor = page.value.nextCursor;
  } while (cursor);

  const expected = refs.map((ref) => ({
    project: canonicalProjectID(ref.project),
    generation: ref.generation,
    resourceType: ref.resourceType,
    id: ref.id,
  })).sort((left, right) => left.id.localeCompare(right.id));
  const actual = members.map((ref) => ({
    project: canonicalProjectID(ref.project),
    generation: ref.generation,
    resourceType: ref.resourceType,
    id: ref.id,
  })).sort((left, right) => left.id.localeCompare(right.id));
  if (!j04ExactEqual(actual, expected)) {
    throw new Error(`J04 immutable Patient selection differs from its exact source identity: ${JSON.stringify({ expected, actual })}`);
  }
  return { selection: created.value, members: actual, snapshotToken: builder.catalog.snapshotToken };
};

const createJ03ThreeMemberFixture = (sourceDirectory) => {
  const directory = mkdtempSync(join(tmpdir(), 'loom-dev-j03-fixture-'));
  try {
    for (const name of readdirSync(sourceDirectory)) {
      const sourcePath = join(sourceDirectory, name);
      if (!statSync(sourcePath).isFile()) continue;
      writeFileSync(join(directory, name), readFileSync(sourcePath), { mode: 0o600 });
    }
    const patientPath = join(directory, 'Patient.ndjson');
    const patientLines = readFileSync(patientPath, 'utf8').split(/\r?\n/).filter((line) => line.trim());
    const patientIDs = new Set(patientLines.map((line) => JSON.parse(line).id));
    let index = patientLines.length + 1;
    while (patientLines.length < 3) {
      let id = `loom-dev-j03-member-${String(index).padStart(3, '0')}`;
      while (patientIDs.has(id)) {
        index += 1;
        id = `loom-dev-j03-member-${String(index).padStart(3, '0')}`;
      }
      patientIDs.add(id);
      patientLines.push(JSON.stringify({
        resourceType: 'Patient', id,
        name: [{ use: 'official', family: 'J03Fixture' }],
        gender: 'unknown',
      }));
      index += 1;
    }
    writeFileSync(patientPath, `${patientLines.join('\n')}\n`, { mode: 0o600 });
    return directory;
  } catch (error) {
    rmSync(directory, { recursive: true, force: true });
    throw error;
  }
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

  off(method, listener) {
    const listeners = this.listeners.get(method);
    if (!listeners) return;
    this.listeners.set(method, listeners.filter((candidate) => candidate !== listener));
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
const selectOptionValue = (label, value) => {
  const select = document.querySelector('select[aria-label="' + label + '"]');
  if (!select || ![...select.options].some((option) => option.value === value)) {
    throw new Error('select value not found: ' + label + '/' + value);
  }
  select.value = value;
  select.dispatchEvent(new Event('change', { bubbles: true }));
  return select.value;
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
  throw new Error(`timed out waiting for browser condition ${predicate.slice(0, 240)}: ${lastError}`);
};

export const addColumnsActionReadinessCondition = (tableReadyCondition) => {
  const selector = '[data-testid="construction-action-add-columns"]';
  return `(() => {
    const workspace = document.querySelector('[data-testid="construction-workspace"]');
    const preview = document.querySelector('[data-testid="construction-preview"]');
    const selectedTable = document.querySelector('[data-testid^="construction-table-"][aria-current="page"]');
    const selectedOutputId = selectedTable?.getAttribute('data-testid')?.slice('construction-table-'.length);
    const pendingStatus = [...document.querySelectorAll('[role=status]')].some((element) =>
      /^(Checking .* fields|Creating .* table|Adding the ID column|Loading the preview|Loom is (?:refreshing the current table draft|finishing the previous table update))/.test(element.innerText?.trim() || '')
    );
    const currentPreview = Boolean(
      workspace && preview && selectedOutputId &&
      preview.dataset.previewStatus === 'ready' &&
      preview.dataset.previewReceiptId &&
      preview.dataset.previewOutputId === selectedOutputId &&
      preview.dataset.currentDraftVersion === workspace.dataset.draftVersion &&
      preview.dataset.currentDraftDigest === workspace.dataset.draftDigest
    );
    return (${tableReadyCondition}) && !pendingStatus && currentPreview &&
      Boolean(document.querySelector('${selector}:not(:disabled)'));
  })()`;
};

const firstTableAddColumnsObserverKey = '__loomFirstTableAddColumnsObserver';

export const installFirstTableAddColumnsObserver = async (cdp, tableReadyCondition) => {
  await browserEval(cdp, `(() => {
    const key = ${JSON.stringify(firstTableAddColumnsObserverKey)};
    window[key]?.observer?.disconnect?.();
    const events = [];
    const sample = (source) => {
      const workspace = document.querySelector('[data-testid="construction-workspace"]');
      const preview = document.querySelector('[data-testid="construction-preview"]');
      const selectedTable = document.querySelector('[data-testid^="construction-table-"][aria-current="page"]');
      const selectedOutputId = selectedTable?.getAttribute('data-testid')?.slice('construction-table-'.length);
      const button = document.querySelector('[data-testid="construction-action-add-columns"]');
      const pendingStatus = [...document.querySelectorAll('[role=status]')].some((element) =>
        /^(Checking .* fields|Creating .* table|Adding the ID column|Loading the preview|Loom is (?:refreshing the current table draft|finishing the previous table update))/.test(element.innerText?.trim() || '')
      );
      const scoped = (${tableReadyCondition});
      if (!scoped || !button || button.disabled) return;
      const acceptedCurrentPreview = Boolean(
        workspace && preview && selectedOutputId && !pendingStatus &&
        preview.dataset.previewStatus === 'ready' &&
        preview.dataset.previewReceiptId &&
        preview.dataset.previewOutputId === selectedOutputId &&
        preview.dataset.currentDraftVersion === workspace.dataset.draftVersion &&
        preview.dataset.currentDraftDigest === workspace.dataset.draftDigest
      );
      events.push({
        source,
        enabled: true,
        acceptedCurrentPreview,
        selectedOutputId: selectedOutputId || '',
        draftVersion: workspace?.dataset.draftVersion || '',
        previewDraftVersion: preview?.dataset.currentDraftVersion || '',
        at: Math.round(performance.now()),
      });
    };
    const observer = new MutationObserver(() => sample('mutation'));
    observer.observe(document.body, {
      subtree: true,
      childList: true,
      attributes: true,
      attributeFilter: [
        'disabled', 'aria-current', 'data-preview-status', 'data-preview-receipt-id',
        'data-preview-output-id', 'data-current-draft-version', 'data-current-draft-digest',
        'data-draft-version', 'data-draft-digest',
      ],
    });
    window[key] = { events, observer, sample };
    sample('installed');
  })()`);
};

export const finishFirstTableAddColumnsObserver = async (cdp) => browserEval(cdp, `
  const key = ${JSON.stringify(firstTableAddColumnsObserverKey)};
  const probe = window[key];
  if (!probe) throw new Error('first-table Add columns observer was not installed');
  probe.sample('final');
  probe.observer.disconnect();
  const events = [...probe.events];
  delete window[key];
  return events;
`);

export const waitForAddColumnsAction = async (cdp, tableReadyCondition) => {
  const selector = '[data-testid="construction-action-add-columns"]';
  try {
    await waitForBrowser(cdp, addColumnsActionReadinessCondition(tableReadyCondition));
  } catch (error) {
    let state;
    try {
      state = await evaluate(cdp, `(() => {
        const button = document.querySelector('${selector}');
        const pendingStatus = [...document.querySelectorAll('[role=status]')]
          .map((element) => element.innerText?.trim()).filter(Boolean);
        return { button: !button ? 'missing' : button.disabled ? 'disabled' : 'enabled', pendingStatus };
      })()`);
    } catch (diagnosticError) {
      state = { button: 'unavailable', diagnosticError: diagnosticError instanceof Error ? diagnosticError.message : String(diagnosticError) };
    }
    const detail = state.button === 'disabled'
      ? state.pendingStatus?.length
        ? `button stayed disabled while Builder reported pending status: ${state.pendingStatus.join(' | ')}`
        : 'button stayed disabled with no pending Builder status visible'
      : `button was ${state.button}${state.diagnosticError ? `; diagnostic failed: ${state.diagnosticError}` : ''}`;
    throw new Error(`Add columns readiness failed: ${detail}`, { cause: error });
  }
};

const snapshot = async (cdp, path) => {
  const html = await evaluate(cdp, 'document.documentElement.outerHTML');
  mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
  writeFileSync(path, String(html ?? ''), { mode: 0o600 });
};

export const takeJavaScriptDialogCommandParams = (cdp, event, promptText) => {
  const response = cdp.nextDialogResponse;
  if (response !== undefined) delete cdp.nextDialogResponse;
  const responsePromptText = response?.promptText;
  return {
    accept: response?.accept ?? true,
    ...(event.type === 'prompt' && responsePromptText !== undefined
      ? { promptText: responsePromptText }
      : event.type === 'prompt' && promptText !== undefined
        ? { promptText }
        : {}),
  };
};

const launchBrowser = async (downloadDir, { promptText } = {}) => {
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
  const dialogHandler = (event) => {
    void cdp.send('Page.handleJavaScriptDialog', {
      ...takeJavaScriptDialogCommandParams(cdp, event, promptText),
    });
  };
  cdp.on('Page.javascriptDialogOpening', dialogHandler);
  const awaitExit = () => new Promise((resolvePromise) => {
    if (child.exitCode !== null || child.signalCode !== null) { resolvePromise(); return; }
    const timer = setTimeout(() => {
      try { child.kill('SIGKILL'); } catch {}
      resolvePromise();
    }, 3000);
    child.once('close', () => { clearTimeout(timer); resolvePromise(); });
  });
  return {
    cdp,
    child,
    profile,
    close: async () => {
      await Promise.race([cdp.send('Browser.close').catch(() => {}), sleep(1000)]);
      cdp.close();
      if (child.exitCode === null && child.signalCode === null) {
        try { child.kill('SIGTERM'); } catch {}
      }
      await awaitExit();
      rmSync(profile, { recursive: true, force: true });
    },
  };
};

const navigate = async (cdp, url) => {
  const expectedURL = new URL(url).href;
  const navigation = await cdp.send('Page.navigate', { url });
  if (navigation.errorText) {
    throw new Error(`browser navigation failed: ${navigation.errorText}`);
  }
  await waitForBrowser(
    cdp,
    `window.location.href === ${JSON.stringify(expectedURL)} && document.readyState === 'complete'`,
    30000,
  );
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

const J04_PRESENCE_STATES = new Set(['missing', 'false', 'zero', 'blank']);

const j04ValueAtPath = (value, path) => path.split('.').reduce((current, part) => {
  if (!current || typeof current !== 'object') return undefined;
  const repeated = part.endsWith('[]');
  const key = repeated ? part.slice(0, -2) : part;
  if (!Object.hasOwn(current, key)) return undefined;
  const next = current[key];
  return repeated ? (Array.isArray(next) ? next[0] : undefined) : next;
}, value);

const j04Presence = (record, path) => {
  const parts = path.split('.');
  let current = record;
  for (const part of parts.slice(0, -1)) {
    if (!current || typeof current !== 'object' || !Object.hasOwn(current, part)) return 'missing';
    current = current[part];
  }
  if (!current || typeof current !== 'object' || !Object.hasOwn(current, parts.at(-1))) return 'missing';
  const value = current[parts.at(-1)];
  if (value === null) return 'null';
  if (value === false) return 'false';
  if (value === 0) return 'zero';
  if (typeof value === 'string' && value.trim() === '') return 'blank';
  return 'value';
};

const j04Identity = (record) => `${record?.resourceType}/${record?.id}`;

const j04IdentityParts = (identity) => {
  if (typeof identity !== 'string') return null;
  const separator = identity.indexOf('/');
  if (separator <= 0 || separator === identity.length - 1) return null;
  return { resourceType: identity.slice(0, separator), id: identity.slice(separator + 1) };
};

const j04AssertEqual = (expected, actual, label) => {
  if (!j04ExactEqual(expected, actual)) throw new Error(`J04 ${label} differs from the source fixture`);
};

const j04WindowBounds = (window, label) => {
  const start = Date.parse(window?.startInclusive);
  const end = Date.parse(window?.endExclusive);
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) throw new Error(`J04 ${label} must have a valid exclusive upper bound`);
  return { start, end };
};

const j04IsInWindow = (record, path, { start, end }) => {
  const timestamp = Date.parse(j04ValueAtPath(record, path));
  if (!Number.isFinite(timestamp)) throw new Error(`J04 source record ${record.id} has no valid time at ${path}`);
  return timestamp >= start && timestamp < end;
};

const j04GroupValues = (record, columns) => Object.fromEntries(columns.map(({ path }) => [path, j04ValueAtPath(record, path)]));

export const validateJ04FixtureContract = (contract, sourceRecords) => {
  if (!contract || typeof contract !== 'object' || contract.version !== 2) throw new Error('J04 fixture contract must have version 2');
  if (typeof contract.sourceFile !== 'string' || !/^j04-[a-z0-9-]+\.ndjson\.fixture$/.test(contract.sourceFile)) throw new Error('J04 fixture contract must name one J04-specific NDJSON fixture');
  if (!Array.isArray(contract.sourceRecords) || !contract.sourceRecords.length) throw new Error('J04 fixture contract must list its source records');
  if (!Array.isArray(sourceRecords) || sourceRecords.length !== contract.sourceRecords.length) throw new Error('J04 fixture source record count differs from the contract');
  const expectedIDs = contract.sourceRecords.map(j04Identity);
  const actualIDs = sourceRecords.map(j04Identity);
  if (expectedIDs.some((identity) => !j04IdentityParts(identity)) || new Set(expectedIDs).size !== expectedIDs.length) throw new Error('J04 fixture contract has invalid or duplicate source record identities');
  if (new Set(sourceRecords.map((record) => record?.id)).size !== sourceRecords.length) throw new Error('J04 source record IDs must be unique across resource types');
  if (!j04ExactEqual(expectedIDs, actualIDs)) throw new Error('J04 fixture source identities or order differ from the contract');
  const sourceByID = new Map(sourceRecords.map((record) => [record.id, record]));
  const sourceByIdentity = new Map(sourceRecords.map((record) => [j04Identity(record), record]));
  const hasSource = (id) => sourceByID.has(id);
  const numericOperators = ['count', 'exists', 'min', 'max', 'mean', 'sum'];
  const baseResourceType = contract.baseRowResourceType;
  if (typeof baseResourceType !== 'string' || !baseResourceType) throw new Error('J04 base output must name its row resource type');
  if (!Array.isArray(contract.baseColumns) || contract.baseColumns.length < 2 || contract.baseColumns.some((column) => !column?.path || !column?.label)) throw new Error('J04 fixture must declare at least two named base columns');
  if (new Set(contract.baseColumns.map((column) => column.path)).size !== contract.baseColumns.length) throw new Error('J04 base column paths must be distinct');
  const baseRows = sourceRecords.filter((record) => record.resourceType === baseResourceType);
  if (!baseRows.length || baseRows.some((record) => contract.baseColumns.some(({ path, optional }) => !optional && j04ValueAtPath(record, path) === undefined))) throw new Error(`J04 required base columns must resolve against ${baseResourceType} rows`);

  const scope = contract.aggregateScope;
  if (!scope || scope.rowResourceType !== 'Patient' || scope.rowResourceType === baseResourceType || scope.sourceResourceType !== 'Observation' || !Array.isArray(scope.selectedRowIdentities) || !scope.selectedRowIdentities.length || !scope.ownerReferencePath || !scope.categoryPath || !scope.categoryCode || !scope.valuePath || !scope.unitPath || !scope.timePath || !scope.anchorPath || !scope.normalizedUnit) throw new Error('J04 aggregate scope must describe selected Patient rows separate from the Observation base output');
  const aggregateWindow = j04WindowBounds(scope.window, 'aggregate time window');
  if (!Array.isArray(contract.expectedAggregates) || !contract.expectedAggregates.length) throw new Error('J04 fixture contract must list exact aggregate outcomes');
  const normalization = contract.normalizationPolicy;
  if (normalization?.targetSystem !== 'http://unitsofmeasure.org' || normalization.targetUnit !== scope.normalizedUnit || !normalization.factorByUnit || typeof normalization.factorByUnit !== 'object') throw new Error('J04 normalization policy must define the UCUM system, aggregate target unit, and supported conversion factors');
  for (const [unit, factor] of Object.entries(normalization.factorByUnit)) {
    if (!unit || !Number.isFinite(factor) || factor <= 0) throw new Error(`J04 unit conversion factor for ${unit || '(empty)'} is invalid`);
  }
  if (!Array.isArray(contract.normalizationCases) || !contract.normalizationCases.some((item) => item.status === 'REFUSED')) throw new Error('J04 normalization cases must include an unsupported-unit refusal');
  const normalizationBySource = new Map();
  for (const item of contract.normalizationCases) {
    const source = sourceByID.get(item?.sourceRecordId);
    const actualValue = j04ValueAtPath(source, scope.valuePath);
    const actualUnit = j04ValueAtPath(source, scope.unitPath);
    if (!source || !Number.isFinite(item?.input?.value) || typeof item.input.unit !== 'string' || !item.input.unit || actualValue !== item.input.value || actualUnit !== item.input.unit) throw new Error('J04 normalization case differs from its source value or unit');
    if (normalizationBySource.has(item.sourceRecordId)) throw new Error(`J04 source ${item.sourceRecordId} has duplicate normalization outcomes`);
    const factor = normalization.factorByUnit[item.input.unit];
    if (factor === undefined) {
      if (item.status !== 'REFUSED' || item.reason !== 'UNIT_IDENTITY_UNKNOWN') throw new Error(`J04 unsupported unit ${item.input.unit} must be refused as UNIT_IDENTITY_UNKNOWN`);
    } else if (item.status !== 'NORMALIZED' || !j04ExactEqual(item.expected, { value: item.input.value * factor, unit: normalization.targetUnit })) {
      throw new Error(`J04 normalization of ${item.sourceRecordId} differs from its declared conversion`);
    }
    if (!['NORMALIZED', 'REFUSED'].includes(item.status)) throw new Error(`J04 normalization status is unsupported: ${item.status}`);
    normalizationBySource.set(item.sourceRecordId, item);
  }
  const patientRows = scope.selectedRowIdentities;
  if (new Set(patientRows).size !== patientRows.length || patientRows.some((identity) => sourceByIdentity.get(identity)?.resourceType !== scope.rowResourceType)) throw new Error('J04 aggregate population must contain unique existing Patient rows');
  const temporalAnchor = sourceByIdentity.get(patientRows[0]);
  const windowStart = Date.parse(scope.window.startInclusive);
  const windowEnd = Date.parse(scope.window.endExclusive);
  if (patientRows.length !== 1 || j04ValueAtPath(temporalAnchor, scope.anchorPath) !== scope.window.endExclusive
    || windowEnd - windowStart !== 2 * 86_400_000) {
    throw new Error('J04 Patient date anchor must prove the two-day window from Jan 1 inclusive to Jan 3 exclusive');
  }
  const aggregateRowIdentities = contract.expectedAggregates.map(({ rowIdentity }) => rowIdentity).sort();
  j04AssertEqual([...patientRows].sort(), aggregateRowIdentities, 'selected aggregate row identities');
  const aggregateByRow = new Map();
  for (const aggregate of contract.expectedAggregates) {
    const row = sourceByIdentity.get(aggregate?.rowIdentity);
    if (!row || row.resourceType !== scope.rowResourceType || !Array.isArray(aggregate.matchingSourceRecordIds) || !Array.isArray(aggregate.operatorSourceRecordIds)) throw new Error('J04 aggregate row must exist in the declared Patient row scope');
    if (!numericOperators.every((operator) => Object.hasOwn(aggregate, operator))) throw new Error('J04 aggregate outcome must declare COUNT, EXISTS, MIN, MAX, MEAN, and SUM');
    const rowSources = sourceRecords.filter((record) => record.resourceType === scope.sourceResourceType
      && j04ValueAtPath(record, scope.ownerReferencePath) === aggregate.rowIdentity
      && j04ValueAtPath(record, scope.categoryPath) === scope.categoryCode);
    const sourceIDs = rowSources.map((record) => record.id);
    j04AssertEqual(sourceIDs, aggregate.matchingSourceRecordIds, `aggregate matched sources for ${aggregate.rowIdentity}`);
    const inWindow = rowSources.filter((record) => j04IsInWindow(record, scope.timePath, aggregateWindow));
    const contributors = inWindow.filter((record) => normalizationBySource.get(record.id)?.status === 'NORMALIZED');
    const contributorIDs = contributors.map((record) => record.id);
    const timeExcludedIDs = rowSources.filter((record) => !j04IsInWindow(record, scope.timePath, aggregateWindow)).map((record) => record.id);
    const refusedIDs = inWindow.filter((record) => normalizationBySource.get(record.id)?.status === 'REFUSED').map((record) => record.id);
    if (inWindow.some((record) => !normalizationBySource.has(record.id))) throw new Error(`J04 aggregate source for ${aggregate.rowIdentity} has no unit outcome`);
    j04AssertEqual(contributorIDs, aggregate.operatorSourceRecordIds, `aggregate contributors for ${aggregate.rowIdentity}`);
    j04AssertEqual(timeExcludedIDs, aggregate.timeExcludedRecordIds, `aggregate time exclusions for ${aggregate.rowIdentity}`);
    j04AssertEqual(refusedIDs, aggregate.normalizationRefusedRecordIds, `aggregate unit refusals for ${aggregate.rowIdentity}`);
    const values = contributors.map((record) => normalizationBySource.get(record.id).expected.value);
    const sum = values.reduce((total, value) => total + value, 0);
    const computed = {
      count: values.length,
      exists: values.length > 0,
      min: values.length ? Math.min(...values) : null,
      max: values.length ? Math.max(...values) : null,
      mean: values.length ? sum / values.length : null,
      sum: values.length ? sum : null,
    };
    for (const operator of numericOperators) j04AssertEqual(computed[operator], aggregate[operator], `${operator.toUpperCase()} for ${aggregate.rowIdentity}`);
    aggregateByRow.set(aggregate.rowIdentity, aggregate);
  }
  const unitRefusal = contract.unsupportedUnitRefusal;
  const refusalSource = sourceByID.get(unitRefusal?.sourceRecordId);
  const refusalRow = sourceByIdentity.get(unitRefusal?.rowIdentity);
  const refusalOutcome = normalizationBySource.get(unitRefusal?.sourceRecordId);
  if (!refusalSource || refusalRow?.resourceType !== scope.rowResourceType
    || j04ValueAtPath(refusalSource, scope.ownerReferencePath) !== unitRefusal.rowIdentity
    || refusalOutcome?.status !== 'REFUSED' || unitRefusal.status !== 'REFUSED'
    || unitRefusal.reason !== 'UNIT_IDENTITY_UNKNOWN' || refusalOutcome.reason !== unitRefusal.reason
    || unitRefusal.applied !== false || aggregateByRow.has(unitRefusal.rowIdentity)) {
    throw new Error('J04 unsupported-unit input must remain a separate refused preview with no applied aggregate row');
  }

  const temporal = contract.temporalOutcomes;
  const temporalWindow = j04WindowBounds(temporal?.window, 'temporal window');
  if (!temporal?.rowIdentity || !aggregateByRow.has(temporal.rowIdentity) || temporal.anchorPath !== scope.anchorPath
    || j04ValueAtPath(sourceByIdentity.get(temporal.rowIdentity), temporal.anchorPath) !== temporal.window.endExclusive
    || !j04ExactEqual(temporal.window, scope.window)) throw new Error('J04 temporal outcomes must use the declared Patient date anchor and aggregate time window');
  if (temporal.latestTiePolicy !== 'SOURCE_ID_ASCENDING') throw new Error('J04 temporal outcomes must declare deterministic source-ID tie ordering');
  const temporalSources = sourceRecords.filter((record) => record.resourceType === scope.sourceResourceType
    && j04ValueAtPath(record, scope.ownerReferencePath) === temporal.rowIdentity
    && j04ValueAtPath(record, scope.categoryPath) === scope.categoryCode);
  const withinWindow = temporalSources.filter((record) => j04IsInWindow(record, scope.timePath, temporalWindow));
  const outsideWindowIDs = temporalSources.filter((record) => !j04IsInWindow(record, scope.timePath, temporalWindow)).map((record) => record.id);
  const orderedByTime = [...withinWindow].sort((left, right) => Date.parse(j04ValueAtPath(left, scope.timePath)) - Date.parse(j04ValueAtPath(right, scope.timePath)) || left.id.localeCompare(right.id));
  const earliestID = orderedByTime[0]?.id;
  const latestTimestamp = orderedByTime.length ? Date.parse(j04ValueAtPath(orderedByTime.at(-1), scope.timePath)) : null;
  const latestTieIDs = orderedByTime.filter((record) => Date.parse(j04ValueAtPath(record, scope.timePath)) === latestTimestamp).map((record) => record.id).sort();
  j04AssertEqual(earliestID, temporal.earliestRecordId, 'earliest in-window record');
  j04AssertEqual(latestTieIDs, temporal.latestTieRecordIds, 'latest timestamp tie members');
  j04AssertEqual(latestTieIDs[0], temporal.latestSelectedRecordId, 'deterministic latest tie winner');
  j04AssertEqual(outsideWindowIDs, temporal.excludedRecordIds, 'temporal window exclusions');

  const recoding = contract.recodingOutcomes;
  if (!recoding?.sourcePath || recoding.casePolicy !== 'EXACT' || recoding.unknownPolicy !== 'KEEP_ORIGINAL' || !recoding.mapping || typeof recoding.mapping !== 'object' || !Array.isArray(recoding.cases) || !recoding.cases.length) throw new Error('J04 recoding outcomes must declare exact-case mapping and unknown preservation');
  if (!recoding.cases.every((item) => hasSource(item?.sourceRecordId) && typeof item.input === 'string' && typeof item.expected === 'string' && j04ValueAtPath(sourceByID.get(item.sourceRecordId), recoding.sourcePath) === item.input)) throw new Error('J04 recoding cases must match source codes and literal input values');
  for (const item of recoding.cases) j04AssertEqual(Object.hasOwn(recoding.mapping, item.input) ? recoding.mapping[item.input] : item.input, item.expected, `exact recoding for ${item.sourceRecordId}`);
  if (!recoding.cases.some((item) => item.input.toLowerCase() === item.input && Object.hasOwn(recoding.mapping, item.input.toUpperCase())) || !recoding.cases.some((item) => !Object.hasOwn(recoding.mapping, item.input))) throw new Error('J04 recoding cases must cover case mismatch and an unknown code');

  const pivot = contract.pivot;
  if (pivot?.sourceResourceType !== baseResourceType || pivot.categoryColumn?.logicalType !== 'string' || pivot.valueColumn?.logicalType !== 'number' || pivot.categoryColumn.path === pivot.valueColumn.path || !Array.isArray(pivot.categories) || pivot.categories.length < 2 || pivot.categories.some((item) => typeof item?.code !== 'string' || !item.code || !item.outputColumn)) throw new Error('J04 pivot must use one string category column and one distinct numeric value column');
  if (new Set(pivot.categories.map((item) => item.outputColumn)).size !== pivot.categories.length || new Set(pivot.categories.map((item) => item.code)).size !== pivot.categories.length || pivot.duplicatePolicy !== 'SUM' || pivot.missingPolicy !== 'NULL' || pivot.unlistedCategoryPolicy !== 'EXCLUDE_WITH_EVIDENCE') throw new Error('J04 pivot categories or policies are invalid');
  if (!Array.isArray(pivot.groupColumns) || !pivot.groupColumns.length || pivot.groupColumns.some((column) => column.logicalType !== 'string' || !column.path)) throw new Error('J04 pivot group columns must have declared string types');
  if (!Array.isArray(pivot.requiredSourceColumns) || pivot.requiredSourceColumns.some((column) => !column?.path || !column?.label)) throw new Error('J04 pivot phase must declare its additional source columns');
  const availablePivotPaths = new Set([...contract.baseColumns, ...pivot.requiredSourceColumns].map((column) => column.path));
  const requiredPivotPaths = [...pivot.groupColumns.map(({ path }) => path), pivot.categoryColumn.path, pivot.valueColumn.path];
  if (requiredPivotPaths.some((path) => !availablePivotPaths.has(path))) throw new Error('J04 pivot phase must author its group, category, and numeric value source columns');
  const pivotSources = sourceRecords.filter((record) => record.resourceType === pivot.sourceResourceType);
  if (pivotSources.some((record) => typeof j04ValueAtPath(record, pivot.categoryColumn.path) !== 'string')) throw new Error('J04 pivot category values must all use the declared string type');
  const selectedCodes = new Set(pivot.categories.map((item) => item.code));
  const selectedSources = pivotSources.filter((record) => selectedCodes.has(j04ValueAtPath(record, pivot.categoryColumn.path)));
  const excludedSources = pivotSources.filter((record) => !selectedCodes.has(j04ValueAtPath(record, pivot.categoryColumn.path)));
  for (const record of selectedSources) {
    const value = j04ValueAtPath(record, pivot.valueColumn.path);
    const unit = j04ValueAtPath(record, pivot.valueColumn.unitPath);
    if (!Number.isFinite(value) || unit !== pivot.valueColumn.unit || pivot.valueColumn.unit !== 'cm') throw new Error(`J04 selected pivot source ${record.id} does not match the numeric ${pivot.valueColumn.unit} value type`);
    for (const column of pivot.groupColumns) if (typeof j04ValueAtPath(record, column.path) !== column.logicalType) throw new Error(`J04 pivot group source ${record.id} does not match ${column.logicalType} at ${column.path}`);
  }
  const groupMap = new Map();
  for (const record of selectedSources) {
    const groupValues = j04GroupValues(record, pivot.groupColumns);
    const groupKey = JSON.stringify(groupValues);
    const categoryCode = j04ValueAtPath(record, pivot.categoryColumn.path);
    const group = groupMap.get(groupKey) ?? { groupValues, cells: new Map() };
    const cell = group.cells.get(categoryCode) ?? [];
    cell.push(record);
    group.cells.set(categoryCode, cell);
    groupMap.set(groupKey, group);
  }
  const orderedGroups = [...groupMap.values()].sort((left, right) => JSON.stringify(left.groupValues).localeCompare(JSON.stringify(right.groupValues)));
  const computedPivotRows = orderedGroups.map((group) => ({
    groupValues: group.groupValues,
    values: Object.fromEntries(pivot.categories.map(({ code, outputColumn }) => {
      const cell = group.cells.get(code) ?? [];
      return [outputColumn, cell.length ? cell.reduce((total, record) => total + j04ValueAtPath(record, pivot.valueColumn.path), 0) : null];
    })),
  }));
  if (!Array.isArray(pivot.derivedColumns) || !pivot.derivedColumns.length) throw new Error('J04 grouped pivot must declare a derived output that uses pivot columns');
  const pivotOutputNames = new Set(pivot.categories.map((item) => item.outputColumn));
  for (const derived of pivot.derivedColumns) {
    if (!derived?.name || pivotOutputNames.has(derived.name) || derived.operation !== 'ADD' || derived.nullPolicy !== 'PROPAGATE_NULL'
      || !Array.isArray(derived.operands) || derived.operands.length !== 2 || !Array.isArray(derived.expectedByGroup)) {
      throw new Error('J04 pivot-derived outputs must add two distinct pivot columns with null propagation');
    }
    const operands = derived.operands.map((operand) => {
      const category = pivot.categories.find((item) => item.outputColumn === operand?.name);
      if (operand?.kind !== 'pivot' || !category) throw new Error(`J04 pivot-derived output ${derived.name} has an unavailable pivot operand`);
      return category.outputColumn;
    });
    const expectedByGroup = [];
    for (const row of computedPivotRows) {
      const [left, right] = operands.map((name) => row.values[name]);
      const value = left === null || right === null ? null : left + right;
      expectedByGroup.push({ groupValues: row.groupValues, presence: value === null ? 'null' : 'value', value });
      row.values[derived.name] = value;
    }
    j04AssertEqual(expectedByGroup, derived.expectedByGroup, `pivot-derived values for ${derived.name}`);
    pivotOutputNames.add(derived.name);
  }
  j04AssertEqual(computedPivotRows, pivot.expectedRows, 'grouped pivot output rows');
  const computedContributors = orderedGroups.flatMap((group) => pivot.categories.flatMap(({ code }) => {
    const records = [...(group.cells.get(code) ?? [])].sort((left, right) => left.id.localeCompare(right.id));
    if (!records.length) return [];
    return [{
      groupValues: group.groupValues,
      categoryCode: code,
      sourceRecordIds: records.map((record) => record.id),
      inputValues: records.map((record) => j04ValueAtPath(record, pivot.valueColumn.path)),
      outputValue: records.reduce((total, record) => total + j04ValueAtPath(record, pivot.valueColumn.path), 0),
    }];
  }));
  j04AssertEqual(computedContributors, pivot.expectedContributors, 'grouped pivot contributor evidence');
  const computedExclusions = excludedSources.map((record) => ({
    sourceRecordId: record.id,
    categoryCode: j04ValueAtPath(record, pivot.categoryColumn.path),
    reason: 'UNLISTED_CATEGORY',
  }));
  j04AssertEqual(computedExclusions, pivot.expectedExclusions, 'grouped pivot exclusion evidence');
  const contributingIDs = selectedSources.map((record) => record.id);
  const missingCategoryCells = computedPivotRows.flatMap((row) => pivot.categories.flatMap(({ code, outputColumn }) => {
    const hasCategory = groupMap.get(JSON.stringify(row.groupValues)).cells.has(code);
    return hasCategory ? [] : [{ groupValues: row.groupValues, categoryCode: code, outputColumn, presence: 'null' }];
  }));
  j04AssertEqual({
    sourceRowResourceType: pivot.sourceResourceType,
    groupedRowCount: computedPivotRows.length,
    contributingSourceRecordIds: contributingIDs,
    unlistedExcludedRecordCount: excludedSources.length,
    missingCategoryCells,
  }, pivot.expectedInformationLoss, 'grouped pivot information-loss evidence');

  if (!Array.isArray(contract.derivedResults) || contract.derivedResults.length < 3) throw new Error('J04 derived outcomes must cover numeric, division-by-zero, and later-derived cases');
  const derivedByName = new Map();
  for (const result of contract.derivedResults) {
    if (!result?.name || derivedByName.has(result.name) || !Array.isArray(result.operands) || result.operands.length !== 2 || !aggregateByRow.has(result.rowIdentity)) throw new Error('J04 derived outcomes must have unique names, two operands, and a declared Patient row');
    const aggregate = aggregateByRow.get(result.rowIdentity);
    const resolve = (operand) => {
      if (operand?.kind === 'literal' && Number.isFinite(operand.value)) return operand.value;
      if (operand?.kind === 'aggregate' && Object.hasOwn(aggregate, operand.name) && Number.isFinite(aggregate[operand.name])) return aggregate[operand.name];
      if (operand?.kind === 'derived' && derivedByName.has(operand.name)) return derivedByName.get(operand.name);
      throw new Error(`J04 derived column ${result.name} has an unavailable or forward operand`);
    };
    const [left, right] = result.operands.map(resolve);
    let computed;
    if (result.operation === 'ADD') computed = left + right;
    else if (result.operation === 'DIVIDE' && right !== 0) computed = left / right;
    else if (result.operation === 'DIVIDE' && right === 0 && result.divisionByZeroPolicy === 'NULL') computed = null;
    else throw new Error(`J04 derived column ${result.name} has an unsupported operation or division policy`);
    const presence = computed === null ? 'null' : 'value';
    j04AssertEqual({ presence, value: computed }, { presence: result.expectedPresence, value: result.expected }, `derived output ${result.name}`);
    derivedByName.set(result.name, computed);
  }
  if (!contract.derivedResults.some((item) => item.operation === 'DIVIDE' && item.divisionByZeroPolicy === 'NULL' && item.expectedPresence === 'null' && item.expected === null)
    || !contract.derivedResults.some((item) => item.operands.some((operand) => operand.kind === 'derived'))) throw new Error('J04 derived outcomes must include division-by-zero null and an earlier derived reference');

  const unrelated = contract.unrelatedColumnLiteral;
  const unrelatedRow = sourceByIdentity.get(unrelated?.rowIdentity);
  if (!unrelated?.column || !unrelatedRow || typeof unrelated.value !== 'string') throw new Error('J04 unrelated column must have one exact source literal');
  j04AssertEqual(j04ValueAtPath(unrelatedRow, unrelated.column), unrelated.value, 'unrelated source column literal');

  if (!Array.isArray(contract.presenceCases) || !['missing', 'false', 'zero', 'blank'].every((state) => contract.presenceCases.some((item) => item.presence === state))) throw new Error('J04 fixture must declare valid FHIR missing, false, zero, and blank source states');
  for (const item of contract.presenceCases) {
    const source = sourceByID.get(item?.sourceRecordId);
    if (!source || source.resourceType !== baseResourceType || typeof item.fieldPath !== 'string' || !J04_PRESENCE_STATES.has(item.presence) || j04Presence(source, item.fieldPath) !== item.presence) throw new Error(`J04 ${item?.presence ?? 'unknown'} scalar evidence must belong to ${baseResourceType} rows`);
    if (item.presence === 'missing' ? Object.hasOwn(item, 'value') : !Object.hasOwn(item, 'value')) throw new Error(`J04 ${item.presence} presence case has an invalid value-presence contract`);
    if (item.presence !== 'missing' && !j04ExactEqual(j04ValueAtPath(source, item.fieldPath), item.value)) throw new Error(`J04 source record differs from its declared ${item.presence} literal`);
  }
  const absence = contract.recordedAbsence;
  const absenceRecord = sourceByID.get(absence?.sourceRecordId);
  const absenceCodings = absenceRecord?.dataAbsentReason?.coding;
  if (!absenceRecord || absenceRecord.resourceType !== 'Observation' || absence.valuePath !== 'valueString'
    || !Array.isArray(absenceCodings) || absenceCodings.length !== 1
    || !j04ExactEqual(absenceCodings[0], { system: 'http://terminology.hl7.org/CodeSystem/data-absent-reason', code: 'unknown', display: 'Unknown' })
    || j04Presence(absenceRecord, absence.valuePath) !== 'missing'
    || j04ValueAtPath(absenceRecord, absence.systemPath) !== 'http://terminology.hl7.org/CodeSystem/data-absent-reason'
    || j04ValueAtPath(absenceRecord, absence.codingPath) !== 'unknown'
    || j04ValueAtPath(absenceRecord, absence.displayPath) !== 'Unknown'
    || absence.system !== 'http://terminology.hl7.org/CodeSystem/data-absent-reason' || absence.code !== 'unknown' || absence.display !== 'Unknown'
    || !contract.presenceCases.some((item) => item.sourceRecordId === absence.sourceRecordId && item.fieldPath === absence.valuePath && item.presence === 'missing')) {
    throw new Error('J04 recorded absence must be a valid Observation with missing valueString and the exact official data-absent-reason coding');
  }
  if (!j04ExactEqual(contract.acceptanceExpectations, {
    unsupportedUnitProposalIsRefused: true,
    previewProposalIsNonMutating: true,
    cancelPreservesSavedDefinition: true,
    applyRequiresProposalReceipt: true,
    staleApplyStatus: 409,
    reloadPreservesAppliedDefinition: true,
    previewViewerAndTypedArtifactAgree: true,
    typedArtifactFormat: 'JSONL',
    typedArtifactPreservesNativeJSONTypes: true,
  })) throw new Error('J04 acceptance expectations must retain receipt, reload, cross-surface, and native-typing checks');
  return true;
};

export const loadJ04FixtureContract = (fixtureDir) => {
  const contract = JSON.parse(readFileSync(join(fixtureDir, 'j04-contract.fixture.json'), 'utf8'));
  if (typeof contract.sourceFile !== 'string' || !/^j04-[a-z0-9-]+\.ndjson\.fixture$/.test(contract.sourceFile)) throw new Error('J04 fixture contract must name one J04-specific NDJSON fixture');
  const sourceRecords = readFileSync(join(fixtureDir, contract.sourceFile), 'utf8')
    .split(/\r?\n/).filter(Boolean).map((line, index) => {
      try { return JSON.parse(line); } catch (error) {
        throw new Error(`J04 source record ${index + 1} is invalid JSON: ${error instanceof Error ? error.message : String(error)}`);
      }
    });
  validateJ04FixtureContract(contract, sourceRecords);
  return { contract, sourceRecords };
};

export const j04FixtureManifest = (_fixtureDir, fixture) => {
  const byResourceType = new Map();
  for (const record of fixture.sourceRecords) {
    const records = byResourceType.get(record.resourceType) ?? [];
    records.push(record);
    byResourceType.set(record.resourceType, records);
  }
  return {
    files: [...byResourceType.entries()]
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([resourceType, records]) => ({
        name: `${resourceType}.ndjson`,
        contents: Buffer.from(`${records.map((record) => JSON.stringify(record)).join('\n')}\n`),
      })),
    summary: {
      sourceFile: fixture.contract.sourceFile,
      sourceRecords: fixture.contract.sourceRecords.length,
    },
  };
};

export const j04BrowserControlPlan = (contract) => {
  if (!contract?.aggregateScope || !contract?.expectedAggregates?.length || !contract?.pivot) {
    throw new Error('J04 browser control plan requires aggregate and pivot fixture scopes');
  }
  const aggregate = contract.expectedAggregates[0];
  const operations = ['count', 'exists', 'min', 'max', 'mean', 'sum'];
  if (operations.some((operation) => !Object.hasOwn(aggregate, operation))) {
    throw new Error('J04 browser control plan requires all six aggregate literals');
  }
  return {
    aggregate: {
      rowResourceType: contract.aggregateScope.rowResourceType,
      selectedRowIdentities: [...contract.aggregateScope.selectedRowIdentities],
      operations: operations.map((operation) => ({
        operation: operation.toUpperCase(),
        expected: aggregate[operation],
      })),
      contributorWindow: structuredClone(contract.temporalOutcomes),
      unitNormalization: {
        ...structuredClone(contract.normalizationPolicy),
        cases: structuredClone(contract.normalizationCases),
        refusal: structuredClone(contract.unsupportedUnitRefusal),
      },
      derived: structuredClone(contract.derivedResults),
    },
    observation: {
      rowResourceType: contract.baseRowResourceType,
      baseColumns: structuredClone(contract.baseColumns),
      pivotSourceColumns: structuredClone(contract.pivot.requiredSourceColumns),
      recoding: structuredClone(contract.recodingOutcomes),
      pivot: structuredClone(contract.pivot),
      presenceCases: structuredClone(contract.presenceCases),
      recordedAbsence: structuredClone(contract.recordedAbsence),
      unrelatedColumnLiteral: structuredClone(contract.unrelatedColumnLiteral),
    },
    acceptance: structuredClone(contract.acceptanceExpectations),
  };
};

export const j04PatientSelectionSeedPlan = (contract) => {
  const scope = contract?.aggregateScope;
  if (scope?.rowResourceType !== 'Patient' || !Array.isArray(scope.selectedRowIdentities) || !scope.selectedRowIdentities.length) {
    throw new Error('J04 Patient selection seed plan requires exact Patient row identities');
  }
  const refs = scope.selectedRowIdentities.map((identity) => {
    const match = /^Patient\/(.+)$/.exec(identity);
    if (!match?.[1]) throw new Error(`J04 Patient selection identity is invalid: ${identity}`);
    return { resourceType: 'Patient', id: match[1] };
  });
  return {
    resourceType: 'Patient',
    selectedRowIdentities: [...scope.selectedRowIdentities],
    refs,
    memberCount: refs.length,
  };
};

export const j04PatientOperatorDOMPlan = (contract) => {
  const aggregate = contract?.expectedAggregates?.[0];
  const scope = contract?.aggregateScope;
  const outcomes = contract?.temporalOutcomes;
  if (!aggregate || scope?.rowResourceType !== 'Patient' || !outcomes) {
    throw new Error('J04 Patient DOM plan requires Patient aggregate and contributor-window outcomes');
  }
  return {
    selectionAttachment: [
      'New explorer', 'Explorer name', 'Create blank', 'first-table-name', 'Create table',
      'Choose Patient rows', 'Starting collection', 'Use selected resources',
    ],
    refusalTable: {
      visibleCreateAction: 'New table',
      rowIdentity: contract.unsupportedUnitRefusal?.rowIdentity,
      sourceRecordId: contract.unsupportedUnitRefusal?.sourceRecordId,
      expectedCode: contract.unsupportedUnitRefusal?.reason,
      mustNotPublish: true,
    },
    graph: ['Advanced graph', 'Relationship to add', 'Add branch', 'Add traversal'],
    aggregateOperations: ['COUNT', 'EXISTS', 'MIN', 'MAX', 'MEAN', 'SUM'].map((operation) => ({
      operation,
      expected: aggregate[operation.toLowerCase()],
      contributorWindowRequired: true,
    })),
    contributorWindow: {
      recordDatePath: scope.timePath,
      anchorPath: scope.anchorPath,
      lookbackDays: (Date.parse(outcomes.window.endExclusive) - Date.parse(outcomes.window.startInclusive)) / 86_400_000,
      window: structuredClone(outcomes.window),
      earliestRecordId: outcomes.earliestRecordId,
      latestSelectedRecordId: outcomes.latestSelectedRecordId,
      tiePolicy: 'RESOURCE_KEY',
    },
    unitNormalization: {
      targetUnit: contract.normalizationPolicy?.targetUnit,
      targetSystem: contract.normalizationPolicy?.targetSystem,
      refusal: structuredClone(contract.unsupportedUnitRefusal),
    },
    recoding: structuredClone(contract.recodingOutcomes),
    visibleActions: [
      'Add date window', 'Edit date window', 'Record date', 'Compare with row date',
      'Look back days', 'Include start boundary', 'Include end boundary',
      'Date selection direction', 'Equal date handling',
      'Apply date window', 'Apply date selection', 'Normalize units', 'Unit conversion preset',
      'Apply normalization', 'Recode exact category values', 'Save recoding', 'Preview',
    ],
  };
};

export const j04PatientOperatorSourceIDs = (contract, operation) => {
  const aggregate = contract.expectedAggregates[0];
  const operator = operation.toLowerCase();
  const sourceIDs = aggregate.operatorSourceRecordIds;
  if (!['min', 'max'].includes(operator)) return [...sourceIDs];
  const normalizedBySource = new Map(contract.normalizationCases
    .filter((item) => item.status === 'NORMALIZED')
    .map((item) => [item.sourceRecordId, item.expected.value]));
  const values = sourceIDs.map((id) => [id, normalizedBySource.get(id)]).filter(([, value]) => Number.isFinite(value));
  const extreme = operator === 'min' ? Math.min(...values.map(([, value]) => value)) : Math.max(...values.map(([, value]) => value));
  return values.filter(([, value]) => value === extreme).map(([id]) => id).sort();
};

export const j04ExactEqual = (expected, actual) => {
  if (Object.is(expected, actual)) return true;
  if (expected === null || actual === null || typeof expected !== typeof actual) return false;
  if (Array.isArray(expected) || Array.isArray(actual)) return Array.isArray(expected) && Array.isArray(actual)
    && expected.length === actual.length && expected.every((value, index) => j04ExactEqual(value, actual[index]));
  if (typeof expected !== 'object') return false;
  const expectedKeys = Object.keys(expected).sort();
  const actualKeys = Object.keys(actual).sort();
  return j04ExactEqual(expectedKeys, actualKeys) && expectedKeys.every((key) => j04ExactEqual(expected[key], actual[key]));
};

const j04ValueEvidence = (values, columnID) => {
  if (!Object.hasOwn(values, columnID)) return { presence: 'missing' };
  const value = values[columnID];
  if (value === null) return { presence: 'null', value: null };
  if (value === false) return { presence: 'false', value: false };
  if (value === 0) return { presence: 'zero', value: 0 };
  if (value === '') return { presence: 'empty', value: '' };
  if (typeof value === 'string' && value.trim() === '') return { presence: 'blank', value };
  return { presence: 'value', value };
};

export const shapeJ04Evidence = ({ columns, rows }) => {
  if (!Array.isArray(columns) || columns.some((column) => !column?.id || !column?.name || !column?.logicalType)) throw new Error('J04 evidence columns must declare stable identity, name, and logical type');
  if (new Set(columns.map((column) => column.id)).size !== columns.length) throw new Error('J04 evidence columns must have distinct stable identities');
  if (!Array.isArray(rows)) throw new Error('J04 evidence rows must be an array');
  const schema = columns.map((column) => ({
    id: column.id,
    name: column.name,
    logicalType: column.logicalType,
    ...Object.fromEntries(['resultUnit', 'shape', 'nullable', 'repeated', 'sourcePath', 'sourceResourceType', 'authoredColumns']
      .filter((key) => Object.hasOwn(column, key)).map((key) => [key, column[key]])),
  }));
  const shapedRows = rows.map((row) => {
    if (row?.rowId === undefined || row.rowId === null || !row.values || typeof row.values !== 'object' || Array.isArray(row.values)) throw new Error('J04 evidence rows must have a stable row identity and values object');
    return {
      rowId: row.rowId,
      values: columns.map((column) => ({ columnId: column.id, ...j04ValueEvidence(row.values, column.id) })),
    };
  });
  return { schema, rows: shapedRows };
};

export const readJ05OutputRows = (rows, names, { preserveMissing = false } = {}) => (Array.isArray(rows) ? rows : []).map((row) => {
  if (Array.isArray(row)) return Object.fromEntries(names.flatMap((name, index) =>
    index < row.length && row[index] !== undefined ? [[name, row[index]]] : []));
  if (row && typeof row === 'object' && Array.isArray(row.values)) {
    return Object.fromEntries(names.flatMap((name, index) =>
      index < row.values.length && row.values[index] !== undefined ? [[name, row.values[index]]] : []));
  }
  return Object.fromEntries(names.flatMap((name) => Object.hasOwn(row ?? {}, name)
    ? [[name, row[name]]]
    : preserveMissing ? [] : [[name, undefined]]));
});

export const normalizeJ04Surface = ({ columns, rows, identityColumns }) => {
  if (!Array.isArray(columns) || !columns.length) throw new Error('J04 surface requires declared output columns');
  if (!Array.isArray(identityColumns) || !identityColumns.length) throw new Error('J04 surface requires stable grouped identity columns');
  const normalizedColumns = columns.map((column) => {
    const id = column?.id ?? column?.column ?? column?.outputKey ?? column?.name;
    const name = column?.name ?? column?.label ?? column?.column ?? id;
    const logicalType = column?.logicalType;
    if (typeof id !== 'string' || !id || typeof name !== 'string' || !name || typeof logicalType !== 'string' || !logicalType) {
      throw new Error(`J04 surface column must declare stable identity, name, and logical type: ${JSON.stringify(column)}`);
    }
    return {
      id,
      name,
      logicalType,
      ...Object.fromEntries(['resultUnit', 'shape', 'nullable', 'repeated', 'sourcePath', 'sourceResourceType', 'authoredColumns']
        .filter((key) => Object.hasOwn(column, key)).map((key) => [key, column[key]])),
    };
  });
  const rowKeys = columns.map((column, index) => column?.rowKey ?? column?.outputKey ?? column?.column ?? column?.name ?? normalizedColumns[index].id);
  const identityIDs = identityColumns.map((identity) => typeof identity === 'string' ? identity : identity.id);
  if (identityIDs.some((id) => !normalizedColumns.some((column) => column.id === id))) {
    throw new Error(`J04 surface identity columns are absent from the declared schema: ${JSON.stringify(identityIDs)}`);
  }
  const normalizedRows = readJ05OutputRows(rows, rowKeys, { preserveMissing: true }).map((rawValues) => {
    const values = Object.fromEntries(columns.flatMap((_column, index) =>
      Object.hasOwn(rawValues, rowKeys[index]) ? [[normalizedColumns[index].id, rawValues[rowKeys[index]]]] : []));
    const identity = identityIDs.map((id) => {
      if (!Object.hasOwn(values, id)) throw new Error(`J04 surface row omits grouped identity column ${id}`);
      return values[id];
    });
    return { rowId: JSON.stringify(identity), values };
  }).sort((left, right) => left.rowId.localeCompare(right.rowId));
  if (new Set(normalizedRows.map((row) => row.rowId)).size !== normalizedRows.length) {
    throw new Error('J04 grouped surface contains duplicate stable row identities');
  }
  return { columns: normalizedColumns, rows: normalizedRows };
};

export const compareJ04Evidence = (expected, actual) => {
  const expectedEvidence = shapeJ04Evidence(expected);
  const actualEvidence = shapeJ04Evidence(actual);
  return { matches: j04ExactEqual(expectedEvidence, actualEvidence), expected: expectedEvidence, actual: actualEvidence };
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

export const expectedTrainingArtifactMembers = (format) => {
  const dataName = format === 'CSV' ? 'data.csv' : format === 'JSONL' ? 'data.jsonl' : undefined;
  if (!dataName) throw new Error(`training artifact format is unsupported: ${format ?? 'missing'}`);
  return [dataName, 'schema.json', 'provenance.json', 'quality.json', 'README.md', 'manifest.json'];
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
        semanticsVersion: authoringCommandSemanticsVersion(before),
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
  const readRows = readJ05OutputRows;
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

const reconcileSavedBuilderDraft = async (target, explorerId, state) => {
  const { response, value } = await requestJSON(`${bootstrapAuthoringURL(target, explorerId)}/reconcile`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      snapshotToken: state.catalog?.snapshotToken,
      draftVersion: state.draftVersion,
      draftDigest: state.draftDigest,
    }),
    timeout: 60000,
  });
  if (!response.ok || !value?.receiptId || !value?.intentDigest) {
    throw new Error(`saved Builder draft did not reconcile to a receipt: HTTP ${response.status} ${JSON.stringify(value).slice(0, 500)}`);
  }
  return value;
};

const readBuilderPreviewDOM = async (cdp) => evaluate(cdp, `(() => {
  const preview = document.querySelector('[data-testid="construction-preview"]');
  const errorText = [...document.querySelectorAll('[role="alert"]')]
    .map((element) => element.innerText.trim().replace(/\\s+/g, ' '))
    .find((text) => text.includes('Preview failed:')) ?? '';
  return {
    status: preview?.getAttribute('data-preview-status') ?? '',
    receiptId: preview?.getAttribute('data-preview-receipt-id') ?? '',
    outputId: preview?.getAttribute('data-preview-output-id') ?? '',
    proposalId: preview?.getAttribute('data-preview-proposal-id') ?? '',
    draftVersion: Number(preview?.getAttribute('data-current-draft-version') ?? 0),
    draftDigest: preview?.getAttribute('data-current-draft-digest') ?? '',
    errorText,
    terminalText: preview?.querySelector('[role="status"]')?.innerText.trim().replace(/\\s+/g, ' ') ?? '',
    text: document.body.innerText,
  };
})()`);

export const builderDraftIsNewerThanBaseline = (baseline, state, outputId) => {
  const selectedDocument = state?.workspace?.documents?.find((document) => document.output?.id === outputId);
  return Boolean(
    selectedDocument &&
    state.lifecycleState === 'READY' &&
    baseline?.snapshotToken &&
    state.catalog?.snapshotToken === baseline.snapshotToken &&
    Number.isInteger(state.draftVersion) &&
    state.draftVersion > baseline.draftVersion &&
    state.draftDigest &&
    state.draftDigest !== baseline.draftDigest,
  );
};

export const builderDraftMatchesPreviewDOM = (baseline, state, preview, outputId) => Boolean(
  builderDraftIsNewerThanBaseline(baseline, state, outputId) &&
  preview?.outputId === outputId &&
  preview.draftVersion === state.draftVersion &&
  preview.draftDigest === state.draftDigest,
);

export const builderPreviewFailureMatchesDraft = (baseline, state, preview, outputId, code) => Boolean(
  builderDraftIsNewerThanBaseline(baseline, state, outputId) &&
  preview?.status === 'error' &&
  preview.outputId === outputId &&
  preview.draftVersion === state.draftVersion &&
  preview.draftDigest === state.draftDigest &&
  !preview.receiptId &&
  !preview.proposalId &&
  !baseline.text?.includes(code) &&
  preview.errorText?.includes('Preview failed:') &&
  preview.errorText.includes(code) &&
  preview.terminalText?.includes(code) &&
  !preview.terminalText.includes('Loading your table')
);

export const builderPreviewIsFreshForDraft = (baseline, state, preview, outputId, savedCompileReceipt) =>
  builderDraftMatchesPreviewDOM(baseline, state, preview, outputId) &&
  preview.status === 'ready' &&
  !preview.proposalId &&
  Boolean(preview.receiptId) &&
  (preview.receiptId !== baseline.previewReceiptId || Boolean(
    baseline.previewProposalId &&
    baseline.previewProposalId === baseline.previewReceiptId &&
    preview.receiptId === baseline.previewProposalId &&
    savedCompileReceipt?.receiptId === preview.receiptId &&
    savedCompileReceipt.intentDigest === state.draftDigest &&
    savedCompileReceipt.snapshotToken === baseline.snapshotToken &&
    savedCompileReceipt.outputs?.some((output) => output.outputId === outputId),
  ));

const captureBuilderPreviewBaseline = async (target, cdp, explorerId) => {
  const [state, preview] = await Promise.all([
    fetchBuilderState(target, explorerId),
    readBuilderPreviewDOM(cdp),
  ]);
  const outputId = state.workspace?.documents?.[0]?.output?.id;
  if (!outputId) throw new Error('Builder preview baseline has no selected table output');
  return {
    snapshotToken: state.catalog?.snapshotToken ?? '',
    draftVersion: state.draftVersion,
    draftDigest: state.draftDigest,
    previewReceiptId: preview.receiptId,
    previewProposalId: preview.proposalId,
    outputId,
    text: preview.text,
  };
};

const waitForFreshBuilderPreview = async (target, cdp, explorerId, baseline, report, assertionName) => {
  const deadline = Date.now() + 60000;
  let latest;
  let candidateReceiptKey = '';
  let candidateSavedCompileReceipt;
  while (Date.now() < deadline) {
    const [state, preview] = await Promise.all([
      fetchBuilderState(target, explorerId),
      readBuilderPreviewDOM(cdp),
    ]);
    latest = { state, preview };
    const isCandidateReceiptReuse = Boolean(
      baseline.previewProposalId &&
      baseline.previewProposalId === baseline.previewReceiptId &&
      preview.status === 'ready' &&
      !preview.proposalId &&
      preview.receiptId === baseline.previewProposalId &&
      builderDraftMatchesPreviewDOM(baseline, state, preview, baseline.outputId),
    );
    if (isCandidateReceiptReuse) {
      const currentCandidateReceiptKey = `${state.draftVersion}:${state.draftDigest}:${baseline.outputId}`;
      if (currentCandidateReceiptKey !== candidateReceiptKey) {
        candidateReceiptKey = currentCandidateReceiptKey;
        candidateSavedCompileReceipt = await reconcileSavedBuilderDraft(target, explorerId, state);
      }
    }
    const savedCompileReceipt = isCandidateReceiptReuse ? candidateSavedCompileReceipt : undefined;
    if (builderPreviewIsFreshForDraft(baseline, state, preview, baseline.outputId, savedCompileReceipt)) {
      report.target.previewFreshness ??= [];
      report.target.previewFreshness.push({
        assertion: assertionName,
        snapshotToken: state.catalog.snapshotToken,
        draftVersion: state.draftVersion,
        draftDigest: state.draftDigest,
        outputId: baseline.outputId,
        previousReceiptId: baseline.previewReceiptId,
        receiptId: preview.receiptId,
        ...(isCandidateReceiptReuse ? {
          previousProposalId: baseline.previewProposalId,
          savedCompileReceiptId: savedCompileReceipt.receiptId,
          savedCompileIntentDigest: savedCompileReceipt.intentDigest,
          savedCompileSnapshotToken: savedCompileReceipt.snapshotToken,
          savedCompileOutputIds: (savedCompileReceipt.outputs ?? []).map((output) => output.outputId),
        } : {}),
      });
      return state;
    }
    await sleep(200);
  }
  throw new Error(`automatic preview did not produce a fresh receipt for the committed Builder draft: ${JSON.stringify({
    outputId: baseline.outputId,
    baseline: { snapshotToken: baseline.snapshotToken, draftVersion: baseline.draftVersion, draftDigest: baseline.draftDigest, receiptId: baseline.previewReceiptId, proposalId: baseline.previewProposalId },
    current: latest && { snapshotToken: latest.state.catalog?.snapshotToken, draftVersion: latest.state.draftVersion, draftDigest: latest.state.draftDigest, ...latest.preview },
  })}`);
};

const waitForFreshBuilderDiagnostic = async (target, cdp, explorerId, baseline, code) => {
  const deadline = Date.now() + 60000;
  let latest;
  while (Date.now() < deadline) {
    const [state, preview] = await Promise.all([
      fetchBuilderState(target, explorerId),
      readBuilderPreviewDOM(cdp),
    ]);
    latest = { state, preview };
    if (builderPreviewFailureMatchesDraft(baseline, state, preview, baseline.outputId, code)) return { state, preview };
    await sleep(200);
  }
  throw new Error(`automatic preview did not report ${code} for the newly committed Builder draft: ${JSON.stringify({
    outputId: baseline.outputId,
    baseline: { snapshotToken: baseline.snapshotToken, draftVersion: baseline.draftVersion, draftDigest: baseline.draftDigest },
    current: latest && { snapshotToken: latest.state.catalog?.snapshotToken, draftVersion: latest.state.draftVersion, draftDigest: latest.state.draftDigest, ...latest.preview },
  })}`);
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

const verifyInterpretationCandidate = async (target, report, cdp, explorerID, evidenceDir, browserURL, physicalColumnID) => {
  const started = Date.now();
  const sourceDigestBefore = fixtureSourceDigest(target.fixtureDir);
  const initial = await fetchBuilderState(target, explorerID);
  const document = initial.workspace?.documents?.[0];
  const feature = document?.columns?.find((column) => column.column === physicalColumnID);
  if (!document?.output?.id || !feature || feature.source?.kind !== 'field' || String(feature.source.field?.path ?? '').replace(/^root\./, '') !== 'id' || feature.source.field?.projectionMode !== 'VALUE') {
    throw new Error(`B06 verification feature for Patient id column ${physicalColumnID} is missing or changed in the draft`);
  }
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
  const sameLabelColumns = document.columns.filter((column) => column.label === feature.label);
  const sameLabelIndex = sameLabelColumns.findIndex((column) => column.column === feature.column);
  if (sameLabelIndex < 0) throw new Error(`Patient id column ${feature.column} is missing from its saved label group`);
  const panelExpression = `(() => {
    const expectedLabel = ${JSON.stringify(feature.label)};
    const matches = [...document.querySelectorAll('div.col-span-full')].filter((element) => {
      const label = element.querySelector(':scope > div.flex > span.text-slate-500');
      const text = norm(element.innerText);
      return norm(label?.textContent) === expectedLabel && text.includes('Interpretation') &&
        (text.includes('Current feature meaning is inline') || text.includes('Pinned revision'));
    });
    if (matches.length !== ${sameLabelColumns.length}) throw new Error('Expected ${sameLabelColumns.length} interpretation panels for saved label ' + expectedLabel + '; found ' + matches.length);
    const panel = matches[${sameLabelIndex}];
    if (!panel) throw new Error('Patient id interpretation panel for ' + expectedLabel + ' at saved occurrence ${sameLabelIndex} not found');
    return panel;
  })()`;
  const openMappingPanel = async () => {
    await browserEval(cdp, `const panel = ${panelExpression}; const details = [...panel.querySelectorAll('details')].find((element) => norm(element.querySelector('summary')?.textContent) === 'Reusable mappings'); if (!details) throw new Error('reusable mappings control not found'); details.open = true;`);
  };
  const openSourceSetup = async () => {
    await waitForBrowser(cdp, `Boolean(document.querySelector('[data-testid="construction-source-setup"]'))`, 60000);
    await browserEval(cdp, `const details = document.querySelector('[data-testid="construction-source-setup"]'); if (!details.open) details.querySelector('summary')?.click();`);
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
  await openSourceSetup();
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
    const panel = ${panelExpression};
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
  await openSourceSetup();
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
  await openSourceSetup();
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

// Normal resource rows use [project, _key] as their compiler identity. The
// generation loader qualifies each physical document key before the cell-trace
// endpoint hashes those identity parts as JSON.
export const j04DefaultRecordCellTraceRowID = (project, generation, resourceType, sourceID) => {
  const parts = [project, generation, resourceType, sourceID];
  if (parts.some((part) => typeof part !== 'string' || !part.trim())) {
    throw new Error('J04 default cell-trace identity requires project, generation, resource type, and source ID');
  }
  const documentKey = `g_${createHash('sha256')
    .update(['vertex', project, generation, resourceType, sourceID, ''].join('\0')).digest('hex')}`;
  return createHash('sha256').update(JSON.stringify([project, documentKey])).digest('hex');
};

export const builderDOMReadyCondition = `Boolean(document.querySelector('[data-testid="construction-workspace"]')) || Boolean(document.querySelector('#first-table-name') && document.querySelector('[aria-label="Choose row type"]'))`;

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
    await waitForBrowser(cdp, builderDOMReadyCondition, 60000);
    recordAssertion(
      report,
      'bare-development-entry-loads-owned-bootstrap',
      `${entryTarget.fixtureProject} / loom-dev-bootstrap`,
      await evaluate(cdp, `document.querySelector('.demo-controls span')?.textContent.trim() || ''`),
    );
    await snapshot(cdp, join(evidenceDir, 'bare-entry.html'));
    recordEvidence(report, join(evidenceDir, 'bare-entry.html'));

    await navigate(cdp, browserURL);
    await waitForBrowser(cdp, `document.querySelector('.demo-controls span')?.textContent.trim() === ${JSON.stringify(`${target.fixtureProject} / ${bootstrapExplorerId}`)} && (${builderDOMReadyCondition})`, 60000);
    await snapshot(cdp, join(evidenceDir, 'builder-initial.html'));
    recordEvidence(report, join(evidenceDir, 'builder-initial.html'));

    const ownerRecordsTitle = `Loom owner records ${target.fixtureProject.slice(-16)}`;
    await browserEval(cdp, `clickText('summary', 'New explorer')`);
    await browserEval(cdp, `setInput('new-explorer-name', ${JSON.stringify(ownerRecordsTitle)})`);
    await browserEval(cdp, `clickButton('Create blank')`);
    await waitForBrowser(cdp, `document.querySelector('select[aria-label="Explorer"] option:checked')?.textContent.trim() === ${JSON.stringify(ownerRecordsTitle)} && (${builderDOMReadyCondition})`);
    const ownerRecordsExplorerId = await evaluate(cdp, `document.querySelector('select[aria-label="Explorer"]')?.value || ''`);
    recordAssertion(report, 'owner-records-browser-selected-owned-explorer', true, Boolean(ownerRecordsExplorerId && ownerRecordsExplorerId !== bootstrapExplorerId));
    report.target.ownerRecordsExplorerId = ownerRecordsExplorerId;

    await browserEval(cdp, `setInput('first-table-name', 'Observation owner records')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('button[aria-label="Choose Observation rows"]:not(:disabled)'))`);
    const ownerRecordsTableReadyCondition = `document.body.innerText.includes('Observation owner records') && Boolean(document.querySelector('[data-testid="construction-workspace"]'))`;
    await installFirstTableAddColumnsObserver(cdp, ownerRecordsTableReadyCondition);
    await browserEval(cdp, `const button = document.querySelector('button[aria-label="Choose Observation rows"]'); if (!button || button.disabled) throw new Error('Observation row choice is unavailable'); button.click();`);
    await waitForAddColumnsAction(cdp, ownerRecordsTableReadyCondition);
    const ownerRecordsAvailabilityEvents = await finishFirstTableAddColumnsObserver(cdp);
    const ownerRecordsPrematureEnabledEvents = ownerRecordsAvailabilityEvents.filter((event) => !event.acceptedCurrentPreview);
    report.target.ownerRecordsAddColumnsAvailability = ownerRecordsAvailabilityEvents;
    recordAssertion(report, 'verified-id-first-table-never-enables-add-columns-before-current-preview', 0, ownerRecordsPrematureEnabledEvents.length);
    recordAssertion(report, 'verified-id-first-table-observer-sees-enabled-action-after-current-preview', true,
      ownerRecordsAvailabilityEvents.some((event) => event.source === 'mutation' && event.acceptedCurrentPreview));
    recordAssertion(report, 'verified-id-first-table-final-action-has-current-preview', true,
      ownerRecordsAvailabilityEvents.some((event) => event.source === 'final' && event.acceptedCurrentPreview));
    const ownerRecordsInitialBuilder = await fetchBuilderState(target, ownerRecordsExplorerId);
    const ownerRecordsInitialTable = ownerRecordsInitialBuilder.workspace?.documents?.[0];
    recordAssertion(report, 'owner-records-builder-creates-observation-rows', 'Observation', ownerRecordsInitialTable?.rootResourceType);
    const ownerRecordsInitialIDColumn = ownerRecordsInitialTable?.columns?.find((column) =>
      column.source?.kind === 'field' && column.source.field?.path?.replace(/^root\./, '') === 'id'
    );
    recordAssertion(report, 'owner-records-first-table-uses-verified-root-id-field', true, Boolean(ownerRecordsInitialIDColumn));
    await browserEval(cdp, `const button = document.querySelector('[data-testid="construction-action-add-columns"]'); if (!button || button.disabled) throw new Error('Add columns action is unavailable'); button.click();`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('[aria-label="Add columns editor"]'))`);
    await browserEval(cdp, `clickButton('Fields and related data')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Search features by field name, concept, or code"]')) && Boolean(document.querySelector('details[data-testid="feature-catalog-raw-fields"]'))`);
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Search' && !button.disabled))`);
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
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('[role="dialog"]')].find((dialog) => dialog.innerText.includes('Choose how to add these fields') && dialog.querySelector('[aria-label="shared: Keep each matching record"]')))`);
    await browserEval(cdp, `const input = inputByLabel('shared: Keep each matching record'); if (!input) throw new Error('OWNER_RECORDS form is missing'); input.click();`);
    await browserEval(cdp, `const dialog = [...document.querySelectorAll('[role="dialog"]')].find((candidate) => candidate.innerText.includes('Choose how to add these fields')); const button = [...(dialog?.querySelectorAll('button') || [])].find((candidate) => candidate.textContent.trim() === 'Add 1 column'); if (!button || button.disabled) throw new Error('owner-record column confirmation is unavailable'); button.click();`);
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Apply columns' && !button.disabled))`);
    const ownerPreviewBaseline = await captureBuilderPreviewBaseline(target, cdp, ownerRecordsExplorerId);
    await browserEval(cdp, `clickButton('Apply columns')`);
    const ownerRecordsStateIsApplied = (builder) => {
      const table = builder?.workspace?.documents?.find((document) => document.output?.id === ownerPreviewBaseline.outputId);
      const column = table?.columns?.find((candidate) => candidate.label === 'shared');
      const source = column?.source;
      return Boolean(
        builder?.draftVersion > ownerPreviewBaseline.draftVersion &&
        builder?.draftDigest && builder.draftDigest !== ownerPreviewBaseline.draftDigest &&
        source?.kind === 'ownerRecords' &&
        source.ownerRecords?.key?.system === 'urn:study:A' &&
        source.ownerRecords?.key?.code === 'shared' &&
        source.ownerRecords?.binding?.ownerPath === 'component[]' &&
        source.ownerRecords?.binding?.valuePath === 'valueQuantity.value'
      );
    };
    const ownerApplyDeadline = Date.now() + 30000;
    let ownerRecordsBuilder;
    while (Date.now() < ownerApplyDeadline) {
      ownerRecordsBuilder = await fetchBuilderState(target, ownerRecordsExplorerId);
      if (ownerRecordsStateIsApplied(ownerRecordsBuilder)) break;
      await sleep(200);
    }
    if (!ownerRecordsStateIsApplied(ownerRecordsBuilder)) {
      const latestTable = ownerRecordsBuilder?.workspace?.documents?.find((document) => document.output?.id === ownerPreviewBaseline.outputId);
      const latestColumn = latestTable?.columns?.find((candidate) => candidate.label === 'shared');
      throw new Error(`timed out waiting for the exact saved OWNER_RECORDS column and an advanced Builder draft: ${JSON.stringify({
        baselineDraftVersion: ownerPreviewBaseline.draftVersion,
        currentDraftVersion: ownerRecordsBuilder?.draftVersion,
        draftAdvanced: Boolean(ownerRecordsBuilder?.draftDigest && ownerRecordsBuilder.draftDigest !== ownerPreviewBaseline.draftDigest),
        columnLabel: latestColumn?.label,
        sourceKind: latestColumn?.source?.kind,
      })}`);
    }
    const ownerRecordsTable = ownerRecordsBuilder.workspace.documents.find((document) => document.output?.id === ownerPreviewBaseline.outputId);
    const ownerRecordsColumn = ownerRecordsTable.columns.find((column) => column.label === 'shared');
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
    await browserEval(cdp, `const button = document.querySelector('[data-testid="construction-close-operation-editor"]'); if (!button) throw new Error('Add columns editor close control is unavailable'); button.click();`);
    await waitForBrowser(cdp, `!document.querySelector('[aria-label="Add columns editor"]')`);
    const configuredOwnerLabel = `Display name for configured ${ownerRecordsColumn.label}`;
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('input')].find((input) => input.getAttribute('aria-label') === ${JSON.stringify(configuredOwnerLabel)} && !input.disabled))`);
    const ownerPreviewReadyBuilder = await waitForFreshBuilderPreview(
      target, cdp, ownerRecordsExplorerId, ownerPreviewBaseline, report,
      'owner-records-preview-uses-current-saved-column-draft',
    );
    const ownerPreviewReceiptID = String((await readBuilderPreviewDOM(cdp)).receiptId);
    const ownerPreview = await requestJSON(`${bootstrapAuthoringURL(target, ownerRecordsExplorerId)}/preview`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ receiptId: ownerPreviewReceiptID, outputId: ownerPreviewReadyBuilder.workspace.documents[0].output.id, limit: 1000 }),
      timeout: 90000,
    });
    if (!ownerPreview.response.ok || !Array.isArray(ownerPreview.value?.rows)) {
      throw new Error(`owner-record preview window failed: HTTP ${ownerPreview.response.status}`);
    }
    const ownerPreviewRow = (sourceID) => ownerPreview.value.rows.findIndex((row) =>
      row[ownerRecordsColumn.column]?.some?.((record) => record?.source?.resourceId === sourceID));
    const validOwnerRow = ownerPreviewRow('dev-pair-001');
    const invalidOwnerRow = ownerPreviewRow('dev-pair-002');
    recordAssertion(report, 'owner-record-source-rows-in-preview-window', true, validOwnerRow >= 0 && invalidOwnerRow >= 0);
    await browserEval(cdp, `selectOption('Preview row limit', '1,000')`);
    await waitForBrowser(cdp, `Number(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')) > ${Math.max(validOwnerRow, invalidOwnerRow) + 1}`, 60000);
    await browserEval(cdp, `scrollVirtualTableToRow('preview-table-scroll', ${invalidOwnerRow})`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('button[aria-label="Inspect shared for row ${invalidOwnerRow + 1}"]'))`);
    await browserEval(cdp, `clickButton('Inspect shared for row ${invalidOwnerRow + 1}')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('[role="dialog"][aria-label="shared record evidence"]')) && document.body.innerText.includes('Repeated FHIR records preserved in this cell')`);
    const invalidOwnerRecordEvidence = String(await evaluate(cdp, `document.querySelector('[role="dialog"][aria-label="shared record evidence"]')?.innerText || ''`));
    recordAssertion(report, 'preview-owner-record-inspector-exposes-invalid-choice-arm', true,
      invalidOwnerRecordEvidence.includes('INVALID_CHOICE_ARM') &&
      invalidOwnerRecordEvidence.includes('dev-pair-002'));
    await browserEval(cdp, `clickButton('Close')`);
    await browserEval(cdp, `scrollVirtualTableToRow('preview-table-scroll', ${validOwnerRow})`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('button[aria-label="Inspect shared for row ${validOwnerRow + 1}"]'))`);
    await browserEval(cdp, `clickButton('Inspect shared for row ${validOwnerRow + 1}')`);
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
    await waitForBrowser(cdp, `document.querySelector('.demo-controls span')?.textContent.trim() === ${JSON.stringify(`${target.fixtureProject} / ${bootstrapExplorerId}`)} && (${builderDOMReadyCondition})`, 60000);

    const verificationTitle = `Loom dev verification ${target.fixtureProject.slice(-16)}`;
    await browserEval(cdp, `clickText('summary', 'New explorer')`);
    await browserEval(cdp, `setInput('new-explorer-name', ${JSON.stringify(verificationTitle)})`);
    await browserEval(cdp, `clickButton('Create blank')`);
    await waitForBrowser(cdp, `document.querySelector('select[aria-label="Explorer"] option:checked')?.textContent.trim() === ${JSON.stringify(verificationTitle)} && (${builderDOMReadyCondition})`);
    explorerId = await evaluate(cdp, `document.querySelector('select[aria-label="Explorer"]')?.value || ''`);
    recordAssertion(report, 'browser-selected-owned-verification-explorer', true, Boolean(explorerId && explorerId !== bootstrapExplorerId));
    report.target.explorerId = explorerId;
    const verificationBrowserURL = `${target.uiUrl}/?project=${encodeURIComponent(target.fixtureProject)}&explorer=${encodeURIComponent(explorerId)}&mode=builder`;

    await browserEval(cdp, `setInput('first-table-name', 'Patients with observations')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('button[aria-label="Choose Patient rows"]:not(:disabled)'))`);
    await browserEval(cdp, `const button = document.querySelector('button[aria-label="Choose Patient rows"]'); if (!button || button.disabled) throw new Error('Patient row choice is unavailable'); button.click();`);
    await waitForAddColumnsAction(cdp, `document.body.innerText.includes('Patients with observations') && Boolean(document.querySelector('[data-testid="construction-workspace"]'))`);
    const patientInitialBuilder = await fetchBuilderState(target, explorerId);
    const patientInitialTable = patientInitialBuilder.workspace?.documents?.[0];
    const patientOutputId = patientInitialTable?.output?.id;
    if (!patientOutputId) throw new Error('Patient Builder table output identity is missing');
    recordAssertion(report, 'patient-builder-creates-patient-rows', 'Patient', patientInitialTable?.rootResourceType);
    const patientInitialIDColumns = (patientInitialTable.columns ?? []).filter((column) =>
      column.source?.kind === 'field' &&
      String(column.source.field?.path ?? '').replace(/^root\./, '') === 'id' &&
      column.source.field?.projectionMode === 'VALUE');
    if (patientInitialIDColumns.length !== 1) throw new Error(`Patient starting table must have one direct id column, found ${patientInitialIDColumns.length}`);
    const patientInitialIDColumn = patientInitialIDColumns[0];
    await browserEval(cdp, `const button = document.querySelector('[data-testid="construction-action-add-columns"]'); if (!button || button.disabled) throw new Error('Add columns action is unavailable'); button.click();`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('[aria-label="Add columns editor"]'))`);
    await browserEval(cdp, `clickButton('Fields and related data')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Search features by field name, concept, or code"]')) && Boolean(document.querySelector('details[data-testid="feature-catalog-raw-fields"]'))`);
    await browserEval(cdp, `setInput('Search features by field name, concept, or code', 'id')`);
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Search' && !button.disabled))`);
    await browserEval(cdp, `clickButton('Search')`);
    await browserEval(cdp, `const details = document.querySelector('details[data-testid="feature-catalog-raw-fields"]'); if (!details) throw new Error('raw FHIR field list is unavailable'); if (!details.open) details.querySelector('summary')?.click();`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Select Patient.id"]:not(:disabled)'))`);
    await browserEval(cdp, `
      const input = inputByLabel('Select Patient.id');
      if (!input) throw new Error('root id field is missing from Find features');
      if (input.disabled) throw new Error('root id field is not selectable');
      input.click();
    `);
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Add 1 selected feature' && !button.disabled))`);
    await browserEval(cdp, `clickButton('Add 1 selected feature')`);
    await waitForBrowser(cdp, `document.querySelector('[role="dialog"] h2')?.textContent.trim() === 'Choose how to add these fields' || Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Apply columns' && !button.disabled))`);
    const patientNeedsFormChoice = await evaluate(cdp, `document.querySelector('[role="dialog"] h2')?.textContent.trim() === 'Choose how to add these fields'`);
    if (patientNeedsFormChoice) {
      await browserEval(cdp, `const dialog = document.querySelector('[role="dialog"]'); const selected = dialog?.querySelector('input[type="radio"]:checked'); const button = [...(dialog?.querySelectorAll('button') || [])].find((candidate) => candidate.textContent.trim() === 'Add 1 column'); if (!selected || !button || button.disabled) throw new Error('Patient ID default form is unavailable'); button.click();`);
    }
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Apply columns' && !button.disabled))`);
    await browserEval(cdp, `clickButton('Apply columns')`);
    await browserEval(cdp, `const button = document.querySelector('[data-testid="construction-close-operation-editor"]'); if (!button) throw new Error('Add columns editor close control is unavailable'); button.click();`);
    await waitForBrowser(cdp, `!document.querySelector('[aria-label="Add columns editor"]')`);
    await browserEval(cdp, `const details = document.querySelector('[data-testid="construction-source-setup"]'); if (!details) throw new Error('advanced source setup is unavailable'); if (!details.open) details.querySelector('summary')?.click();`);
    const initialPatientColumns = new Set(patientInitialTable.columns.map(column => column.column));
    const waitForRootField = async (fieldPath, newlyAdded = false) => {
      const deadline = Date.now() + 30000;
      let latestBuilder;
      while (Date.now() < deadline) {
        latestBuilder = await fetchBuilderState(target, explorerId);
        const document = latestBuilder.workspace?.documents?.find((candidate) => candidate.output?.id === patientOutputId);
        if (document && document.rootResourceType !== 'Patient') throw new Error(`Patient table ${patientOutputId} changed root to ${document.rootResourceType}`);
        const matchingColumns = (document?.columns ?? []).filter((column) =>
          column.source?.kind === 'field' &&
          String(column.source.field?.path ?? '').replace(/^root\./, '') === fieldPath &&
          (!newlyAdded || !initialPatientColumns.has(column.column)));
        if (matchingColumns.length > 1) throw new Error(`Patient ${fieldPath} binding is ambiguous: ${matchingColumns.length} saved columns`);
        if (document && matchingColumns.length === 1) return { builder: latestBuilder, document, column: matchingColumns[0] };
        await sleep(200);
      }
      throw new Error(`timed out waiting for the saved Patient.${fieldPath} source binding; draft=${latestBuilder?.draftVersion}`);
    };
    const waitForConfiguredColumnInput = async (column) => {
      const inputLabel = `Display name for configured ${column.label}`;
      await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('input[aria-label^="Display name for configured "]')].some((input) => input.getAttribute('aria-label') === ${JSON.stringify(inputLabel)}))`);
      return inputLabel;
    };
    const catalogID = await waitForRootField('id', true);
    for (const initialColumn of patientInitialTable.columns) {
      const retained = catalogID.document.columns.find(column => column.column === initialColumn.column);
      recordAssertion(report, 'builder-catalog-preserves-initial-' + initialColumn.column, initialColumn, retained);
    }
    const catalogBuilder = catalogID.builder;
    const catalogIDColumn = catalogID.column;
    if (catalogIDColumn.column === patientInitialIDColumn.column) throw new Error('Patient catalog id reused the existing auto-id physical column');
    const patientIDColumnsAfterCatalog = catalogID.document.columns.filter((column) =>
      column.source?.kind === 'field' &&
      String(column.source.field?.path ?? '').replace(/^root\./, '') === 'id' &&
      column.source.field?.projectionMode === 'VALUE');
    recordAssertion(report, 'builder-keeps-auto-id-and-adds-distinct-catalog-id', [
      { column: patientInitialIDColumn.column, label: 'Patient ID', path: 'id', projectionMode: 'VALUE' },
      { column: catalogIDColumn.column, label: 'Patient ID', path: 'id', projectionMode: 'VALUE' },
    ], patientIDColumnsAfterCatalog.map((column) => ({
      column: column.column,
      label: column.label,
      path: String(column.source.field.path).replace(/^root\./, ''),
      projectionMode: column.source.field.projectionMode,
    })));
    const rootFieldCandidate = (builder, fieldPath) => {
      const root = builder.catalog?.nodes?.find((node) => node.resourceType === 'Patient' && node.rowRootEligible);
      const matches = (builder.catalog?.candidates ?? []).filter((candidate) => candidate.nodeId === root?.nodeId &&
        String(candidate.fieldPath ?? '').replace(/^root\./, '') === fieldPath);
      if (matches.length !== 1 || !matches[0].defaultProjectionMode) {
        throw new Error(`Patient ${fieldPath} needs one root field candidate with a default projection; found ${matches.length}`);
      }
      return matches[0];
    };
    const catalogIDCandidate = rootFieldCandidate(catalogBuilder, 'id');
    if (catalogIDColumn.source.field.projectionMode !== 'VALUE' || catalogIDCandidate.defaultProjectionMode !== 'VALUE') {
      throw new Error(`Patient.id must be the direct VALUE binding; saved=${catalogIDColumn.source.field.projectionMode}, catalog=${catalogIDCandidate.defaultProjectionMode}`);
    }
    const catalogIDInputLabel = await waitForConfiguredColumnInput(catalogIDColumn);
    recordAssertion(report, 'builder-catalog-adds-default-root-field-without-graph', {
      kind: 'field',
      path: 'id',
      projectionMode: 'VALUE',
      graphVisible: false,
    }, {
      kind: catalogIDColumn.source.kind,
      path: String(catalogIDColumn.source.field.path).replace(/^root\./, ''),
      projectionMode: catalogIDColumn.source.field.projectionMode,
      graphVisible: await evaluate(cdp, `document.body.innerText.includes('Dataset graph')`),
    });
    await snapshot(cdp, join(evidenceDir, 'builder-catalog-column.html'));
    recordEvidence(report, join(evidenceDir, 'builder-catalog-column.html'));
    await waitForBrowser(cdp, `Boolean(document.querySelector('[aria-label="Feature authoring view"]'))`);
    await browserEval(cdp, `clickButton('Advanced graph')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Current query') && document.body.innerText.includes('Patient columns')`);
    await browserEval(cdp, `clickCandidate('name[].family', 'to table')`);
    const familyField = await waitForRootField('name[].family');
    const familyCandidate = rootFieldCandidate(familyField.builder, 'name[].family');
    if (familyField.column.source.field.projectionMode !== familyCandidate.defaultProjectionMode) {
      throw new Error(`Patient.name[].family projection differs from its selected root candidate: saved=${familyField.column.source.field.projectionMode}, candidate=${familyCandidate.defaultProjectionMode}`);
    }
    const familyInputLabel = await waitForConfiguredColumnInput(familyField.column);
    await browserEval(cdp, `setInput('Search columns', 'gender')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Add gender as filter"]'))`);
    await browserEval(cdp, `clickCandidate('gender', 'as filter')`);
    await browserEval(cdp, `setInput('Search columns', '')`);
    const genderField = await waitForRootField('gender');
    const genderCandidate = rootFieldCandidate(genderField.builder, 'gender');
    if (genderField.column.source.field.projectionMode !== genderCandidate.defaultProjectionMode) {
      throw new Error(`Patient.gender projection differs from its selected root candidate: saved=${genderField.column.source.field.projectionMode}, candidate=${genderCandidate.defaultProjectionMode}`);
    }
    const genderInputLabel = await waitForConfiguredColumnInput(genderField.column);
    const expectedConfiguredInputLabels = [catalogIDInputLabel, familyInputLabel, genderInputLabel].sort();
    const configuredInputLabelsExpression = `Array.from(new Set(Array.from(document.querySelectorAll('input[aria-label^="Display name for configured "]'), (input) => input.getAttribute('aria-label')))).sort()`;
    await waitForBrowser(cdp, `(() => { const expected = ${JSON.stringify(expectedConfiguredInputLabels)}; const actual = ${configuredInputLabelsExpression}; return expected.every((label) => actual.includes(label)); })()`);
    const configuredFields = await evaluate(cdp, configuredInputLabelsExpression);
    recordAssertion(report, 'builder-configures-exact-root-fields', expectedConfiguredInputLabels, configuredFields);
    const configuredSourceBindings = [catalogIDColumn, familyField.column, genderField.column].map((column) => ({
      path: String(column.source.field.path).replace(/^root\./, ''),
      projectionMode: column.source.field.projectionMode,
    }));
    recordAssertion(report, 'builder-configures-exact-root-field-source-bindings', [
      { path: 'id', projectionMode: 'VALUE' },
      { path: 'name[].family', projectionMode: familyCandidate.defaultProjectionMode },
      { path: 'gender', projectionMode: genderCandidate.defaultProjectionMode },
    ], configuredSourceBindings);
    await verifyInterpretationCandidate(target, report, cdp, explorerId, evidenceDir, verificationBrowserURL, catalogIDColumn.column);

    await waitForBrowser(cdp, `document.body.innerText.includes('Concept catalog')`);
    await browserEval(cdp, `clickButton('Advanced graph')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Dataset graph')`);
    await browserEval(cdp, `clickContains('.react-flow__node', 'Observation')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Observation columns')`);
    await waitForBrowser(cdp, `document.querySelector('[aria-label="Require Observation match"]')?.disabled === false`);
    await browserEval(cdp, `clickButton('Require Observation match');`);
    await waitForBrowser(cdp, `document.querySelector('[aria-label="Keep Observation match"]')?.disabled === false`);
    const requiredBuilder = await fetchBuilderState(target, explorerId);
    recordAssertion(report, 'builder-required-match-persists-route-intent', 'REQUIRED', requiredBuilder.workspace.documents[0].route.children[0].matchMode);
    await browserEval(cdp, `clickButton('Keep Observation match');`);
    await waitForBrowser(cdp, `document.querySelector('[aria-label="Require Observation match"]')?.disabled === false`);
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
    await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label^="Add "][aria-label$=" to table"]'))`);
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
    const exactTablePreviewBaseline = await captureBuilderPreviewBaseline(target, cdp, explorerId);
    await browserEval(cdp, `clickCandidate('valueQuantity.value', 'to table')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Display name for configured valueQuantity.value"]'))`);
    await waitForFreshBuilderPreview(target, cdp, explorerId, exactTablePreviewBaseline, report,
      'patient-preview-uses-current-configured-field-draft');
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
      headers: ['Patient ID', 'Patient ID', 'name[].family [0]', 'name[].family [1]', 'name__count', 'Observation count', 'Has Observation', 'valueQuantity.value'],
      rows: [
        ['dev-patient-001', 'dev-patient-001', 'Example', 'Example-Smith', '2', '1', 'true', String(relatedValue)],
        ['dev-patient-002', 'dev-patient-002', 'Builder', '—', '1', '0', 'true', '68'],
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
    const requireOnePreviewBaseline = await captureBuilderPreviewBaseline(target, cdp, explorerId);
    await browserEval(cdp, `selectOption('Across related Observation records for valueQuantity.value', 'Require zero or one value')`);
    const requireOneDiagnostic = await waitForFreshBuilderDiagnostic(target, cdp, explorerId, requireOnePreviewBaseline, 'RELATIONSHIP_CARDINALITY_VIOLATION');
    recordAssertion(report, 'require-one-rejects-ambiguous-related-values', true,
      String(await evaluate(cdp, 'document.body.innerText')).includes('RELATIONSHIP_CARDINALITY_VIOLATION'));
    const ambiguousState = await fetchExplorerState(target, explorerId);
    recordAssertion(report, 'ambiguous-require-one-does-not-publish', false,
      Boolean(ambiguousState.active?.revisionId || ambiguousState.runtime?.outputs?.length));
    const requireOneErrorWitness = {
      snapshotToken: requireOneDiagnostic.state.catalog?.snapshotToken ?? '',
      draftVersion: requireOneDiagnostic.state.draftVersion,
      draftDigest: requireOneDiagnostic.state.draftDigest,
      outputId: requireOneDiagnostic.preview.outputId,
      status: requireOneDiagnostic.preview.status,
      errorText: requireOneDiagnostic.preview.errorText,
      terminalText: requireOneDiagnostic.preview.terminalText,
    };
    const requireOneErrorActual = {
      snapshotMatchesBaseline: requireOneErrorWitness.snapshotToken === requireOnePreviewBaseline.snapshotToken,
      outputMatchesSelected: requireOneErrorWitness.outputId === requireOnePreviewBaseline.outputId,
      statusIsTerminalError: requireOneErrorWitness.status === 'error',
      changedSavedDraft: builderDraftIsNewerThanBaseline(requireOnePreviewBaseline, requireOneDiagnostic.state, requireOnePreviewBaseline.outputId),
      previewMatchesSavedDraft: requireOneDiagnostic.preview.draftVersion === requireOneDiagnostic.state.draftVersion &&
        requireOneDiagnostic.preview.draftDigest === requireOneDiagnostic.state.draftDigest,
      errorCodeMatches: requireOneErrorWitness.errorText.includes('RELATIONSHIP_CARDINALITY_VIOLATION') &&
        requireOneErrorWitness.terminalText.includes('RELATIONSHIP_CARDINALITY_VIOLATION'),
      noReceiptOrProposal: !requireOneDiagnostic.preview.receiptId && !requireOneDiagnostic.preview.proposalId,
      loadingPlaceholderCleared: !requireOneErrorWitness.terminalText.includes('Loading your table'),
    };
    report.target.automaticPreviewDiagnostic = { assertion: 'require-one-terminal-error-is-bound-to-current-draft-and-output', ...requireOneErrorWitness };
    recordAssertion(report, 'require-one-terminal-error-is-bound-to-current-draft-and-output', {
      snapshotMatchesBaseline: true,
      outputMatchesSelected: true,
      statusIsTerminalError: true,
      changedSavedDraft: true,
      previewMatchesSavedDraft: true,
      errorCodeMatches: true,
      noReceiptOrProposal: true,
      loadingPlaceholderCleared: true,
    }, requireOneErrorActual);
    const valueCountPreviewBaseline = await captureBuilderPreviewBaseline(target, cdp, explorerId);
    await browserEval(cdp, `selectOption('Across related Observation records for valueQuantity.value', 'Count values or records')`);
    const valueCountRecoveryState = await waitForFreshBuilderPreview(target, cdp, explorerId, valueCountPreviewBaseline, report,
      'value-count-preview-uses-current-aggregate-draft');
    const valueCountRecoveryPreview = await readBuilderPreviewDOM(cdp);
    const valueCountRecoveryActual = {
      outputMatchesSelected: valueCountRecoveryPreview.outputId === valueCountPreviewBaseline.outputId,
      statusIsReady: valueCountRecoveryPreview.status === 'ready',
      snapshotMatchesErrorDraft: valueCountRecoveryState.catalog?.snapshotToken === requireOneDiagnostic.state.catalog?.snapshotToken,
      savedDraftAdvancedAfterError: valueCountRecoveryState.draftVersion > requireOneDiagnostic.state.draftVersion &&
        valueCountRecoveryState.draftDigest !== requireOneDiagnostic.state.draftDigest,
      previewMatchesSavedDraft: valueCountRecoveryPreview.draftVersion === valueCountRecoveryState.draftVersion &&
        valueCountRecoveryPreview.draftDigest === valueCountRecoveryState.draftDigest,
      freshReceiptPresent: Boolean(valueCountRecoveryPreview.receiptId),
      proposalCleared: !valueCountRecoveryPreview.proposalId,
    };
    report.target.automaticPreviewRecovery = {
      assertion: 'count-policy-recovers-preview-for-newer-draft-after-require-one-error',
      afterError: { draftVersion: requireOneDiagnostic.state.draftVersion, draftDigest: requireOneDiagnostic.state.draftDigest, outputId: requireOnePreviewBaseline.outputId },
      recovered: {
        snapshotToken: valueCountRecoveryState.catalog?.snapshotToken ?? '',
        draftVersion: valueCountRecoveryState.draftVersion,
        draftDigest: valueCountRecoveryState.draftDigest,
        outputId: valueCountRecoveryPreview.outputId,
        status: valueCountRecoveryPreview.status,
        receiptId: valueCountRecoveryPreview.receiptId,
      },
    };
    recordAssertion(report, 'count-policy-recovers-preview-for-newer-draft-after-require-one-error', {
      outputMatchesSelected: true,
      statusIsReady: true,
      snapshotMatchesErrorDraft: true,
      savedDraftAdvancedAfterError: true,
      previewMatchesSavedDraft: true,
      freshReceiptPresent: true,
      proposalCleared: true,
    }, valueCountRecoveryActual);
    const valueCounts = await evaluate(cdp, `(() => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      const rows = [...(table?.querySelectorAll('[role="row"]') || [])];
      const headers = [...(rows[0]?.querySelectorAll('[role="columnheader"]') || [])].map((cell) => cell.textContent.trim());
      const idIndex = headers.indexOf('Patient ID');
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
    await waitForBrowser(cdp, `Boolean(document.querySelector('select[aria-label="Record date"]')) && Boolean(document.querySelector('select[aria-label="Compare with row date"]')) && Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Apply date selection'))`);
    await browserEval(cdp, `selectOptionValue('Record date', 'effectiveDateTime')`);
    await browserEval(cdp, `selectOptionValue('Compare with row date', 'meta.lastUpdated')`);
    recordAssertion(report, 'date-aware-editor-selects-expected-source-and-anchor', ['effectiveDateTime', 'meta.lastUpdated'],
      await evaluate(cdp, `[document.querySelector('select[aria-label="Record date"]')?.value, document.querySelector('select[aria-label="Compare with row date"]')?.value]`));
    const temporalPreviewBaseline = await captureBuilderPreviewBaseline(target, cdp, explorerId);
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
      timestampPath: temporalFeature?.source?.aggregate?.contributorWindow?.timestampPath,
      anchorPath: temporalFeature?.source?.aggregate?.contributorWindow?.anchorPath,
      direction: temporalFeature?.source?.aggregate?.ordering?.direction,
      precision: temporalFeature?.source?.aggregate?.contributorWindow?.precision,
      tiePolicy: temporalFeature?.source?.aggregate?.ordering?.tiePolicy,
    });
    await waitForFreshBuilderDiagnostic(target, cdp, explorerId, temporalPreviewBaseline, 'TEMPORAL_TIE_AMBIGUOUS');
    recordAssertion(report, 'date-aware-selection-rejects-equal-date-ambiguity', true,
      String(await evaluate(cdp, 'document.body.innerText')).includes('TEMPORAL_TIE_AMBIGUOUS'));
    await browserEval(cdp, `clickButton('Edit date window')`);
    await browserEval(cdp, `selectOption('Equal date handling', 'Choose deterministically by resource key')`);
    const tiePolicyPreviewBaseline = await captureBuilderPreviewBaseline(target, cdp, explorerId);
    await browserEval(cdp, `clickButton('Apply date selection')`);
    const tiePolicyDeadline = Date.now() + 30000;
    while (Date.now() < tiePolicyDeadline) {
      temporalBuilder = await fetchBuilderState(target, explorerId);
      const valueFeature = temporalBuilder.workspace.documents[0].columns.find((column) => column.label === 'valueQuantity.value');
      if (valueFeature?.source?.aggregate?.ordering?.tiePolicy === 'RESOURCE_KEY') break;
      await sleep(200);
    }
    recordAssertion(report, 'builder-persists-explicit-equal-date-resolution', 'RESOURCE_KEY',
      temporalBuilder?.workspace.documents[0].columns.find((column) => column.label === 'valueQuantity.value')?.source?.aggregate?.ordering?.tiePolicy);
    await waitForFreshBuilderPreview(target, cdp, explorerId, tiePolicyPreviewBaseline, report,
      'resource-key-preview-uses-current-temporal-policy-draft');
    const temporalValues = await evaluate(cdp, `(() => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      const rows = [...(table?.querySelectorAll('[role="row"]') || [])];
      const headers = [...(rows[0]?.querySelectorAll('[role="columnheader"]') || [])].map((cell) => cell.textContent.trim());
      const idIndex = headers.indexOf('Patient ID');
      const valueIndex = headers.indexOf('valueQuantity.value');
      return rows.slice(1).map((row) => {
        const cells = [...row.querySelectorAll('[role="cell"]')].map((cell) => cell.textContent.trim());
        return [cells[idIndex], cells[valueIndex]];
      }).sort((left, right) => left[0].localeCompare(right[0]));
    })()`);
    recordAssertion(report, 'date-aware-selection-resolves-equal-dates-deterministically', [
      ['dev-patient-001', '172.5'],
      ['dev-patient-002', '68'],
    ], temporalValues);
    const maximumPreviewBaseline = await captureBuilderPreviewBaseline(target, cdp, explorerId);
    await browserEval(cdp, `selectOption('Across related Observation records for valueQuantity.value', 'Maximum value')`);
    let reducedBuilder;
    const reductionDeadline = Date.now() + 30000;
    while (Date.now() < reductionDeadline) {
      reducedBuilder = await fetchBuilderState(target, explorerId);
      const valueFeature = reducedBuilder.workspace.documents[0].columns.find((column) => column.label === 'valueQuantity.value');
      if (valueFeature?.source?.aggregate?.operation === 'MAX') break;
      await sleep(200);
    }
    await browserEval(cdp, `clickButton('Remove date window')`);
    const unwindowedDeadline = Date.now() + 30000;
    while (Date.now() < unwindowedDeadline) {
      reducedBuilder = await fetchBuilderState(target, explorerId);
      const valueFeature = reducedBuilder.workspace.documents[0].columns.find((column) => column.label === 'valueQuantity.value');
      if (valueFeature?.source?.aggregate?.operation === 'MAX' && !valueFeature.source.aggregate.contributorWindow) break;
      await sleep(200);
    }
    const reducedValueFeature = reducedBuilder?.workspace.documents[0].columns.find((column) => column.label === 'valueQuantity.value');
    recordAssertion(report, 'builder-replaces-unsafe-related-first-with-explicit-maximum', {
      operation: 'MAX', path: 'valueQuantity.value', contributorWindow: undefined,
    }, {
      operation: reducedValueFeature?.source?.aggregate?.operation,
      path: reducedValueFeature?.source?.aggregate?.path,
      contributorWindow: reducedValueFeature?.source?.aggregate?.contributorWindow,
    });
    await waitForFreshBuilderPreview(target, cdp, explorerId, maximumPreviewBaseline, report,
      'maximum-preview-uses-current-unwindowed-aggregate-draft');
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Publish' && !button.disabled))`, 60000);
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
    const expectedPatientIDColumnIDs = [patientInitialIDColumn.column, catalogIDColumn.column];
    recordAssertion(report, 'published-output-preserves-exact-auto-and-catalog-id-columns', expectedPatientIDColumnIDs,
      outputLineage.filter((column) => expectedPatientIDColumnIDs.includes(column.column)).map((column) => column.column));
    recordAssertion(report, 'published-output-has-exact-supported-shape', [
      { label: 'Patient ID', sourcePath: 'id', sourceResourceType: 'Patient', projectionMode: 'VALUE', coordinates: [] },
      { label: 'Patient ID', sourcePath: 'id', sourceResourceType: 'Patient', projectionMode: 'VALUE', coordinates: [] },
      { label: 'name[].family [0]', sourcePath: 'name[].family', sourceResourceType: 'Patient', projectionMode: 'INDEXED', coordinates: [0] },
      { label: 'name[].family [1]', sourcePath: 'name[].family', sourceResourceType: 'Patient', projectionMode: 'INDEXED', coordinates: [1] },
      { label: 'name__count', sourcePath: 'name[]', sourceResourceType: 'Patient', projectionMode: 'COUNT', coordinates: [] },
      { label: 'Observation count', sourcePath: '$resource', sourceResourceType: 'Observation', projectionMode: 'COUNT', coordinates: [] },
      { label: 'Has Observation', sourcePath: '$resource', sourceResourceType: 'Observation', projectionMode: 'EXISTS', coordinates: [] },
      { label: 'valueQuantity.value', sourcePath: 'valueQuantity.value', sourceResourceType: 'Observation', projectionMode: 'MAX', coordinates: [] },
      { label: 'gender', sourcePath: 'gender', sourceResourceType: 'Patient', projectionMode: 'VALUE', coordinates: [] },
    ], outputLineage.map(({ label, sourcePath, sourceResourceType, projectionMode, coordinates }) => ({ label, sourcePath, sourceResourceType, projectionMode, coordinates })));
    const idColumn = findPhysicalColumn(state, output, (runtimeColumn, emitted) =>
      runtimeColumn.column === patientInitialIDColumn.column && emitted.sourcePath === 'id');
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
    recordAssertion(report, 'materialized-preserves-both-literal-patient-id-values', [
      ['dev-patient-001', 'dev-patient-001'],
      ['dev-patient-002', 'dev-patient-002'],
    ], orderedRows.map((row) => expectedPatientIDColumnIDs.map((columnID) => rowValue(row, columnID))));
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
      if (!explain) throw new Error('missing indexed family cell was not found for dev-patient-002');
      explain.click();
    `);
    await waitForBrowser(cdp, `document.body.innerText.includes('No authorized source record matched this feature')`, 30000);
    await browserEval(cdp, `clickButton('Review matching rules')`);
    await waitForBrowser(cdp, `new URL(window.location.href).searchParams.get('mode') === 'builder' && Boolean(document.querySelector('[data-feature-focus="true"]'))`, 60000);
    const repairFocus = await evaluate(cdp, `({
      mode: new URL(window.location.href).searchParams.get('mode'),
      output: new URL(window.location.href).searchParams.get('focusOutput'),
      column: new URL(window.location.href).searchParams.get('focusColumn'),
      search: document.querySelector('input[aria-label="Search columns"]')?.value ?? '',
      highlighted: document.querySelector('[data-feature-focus="true"]')?.getAttribute('data-feature-focus') ?? '',
    })`);
    recordAssertion(report, 'viewer-missing-index-repair-focuses-exact-builder-feature', true,
      repairFocus.mode === 'builder'
      && Boolean(repairFocus.output)
      && Boolean(repairFocus.column)
      && repairFocus.search === repairFocus.column
      && repairFocus.highlighted === 'true');
    await snapshot(cdp, join(evidenceDir, 'builder-focused-missing-index-repair.html'));
    recordEvidence(report, join(evidenceDir, 'builder-focused-missing-index-repair.html'));
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
      headers: ['Patient ID', 'Patient ID', 'name[].family [0]', 'name[].family [1]', 'name__count', 'Observation count', 'Has Observation', 'valueQuantity.value'],
      rows: [['dev-patient-001', 'dev-patient-001', 'Example', 'Example-Smith', '2', '1', 'true', String(maximumRelatedValue)]],
    }, filteredViewer);
    recordAssertion(report, 'viewer-mode-is-persisted-in-url', 'viewer', await evaluate(cdp, 'new URL(window.location.href).searchParams.get("mode")'));

    await browserEval(cdp, `clickButton('Explain valueQuantity.value for row 1')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Why is valueQuantity.value ${maximumRelatedValue}?') && document.body.innerText.includes('One source record supplied this value.')`, 30000);
    await browserEval(cdp, `clickText('summary', 'Source details (1)')`);
    const cellExplanation = await evaluate(cdp, `(() => {
      const dialog = [...document.querySelectorAll('[role="dialog"]')].find((candidate) => candidate.innerText.includes('Why is valueQuantity.value'));
      return dialog?.innerText ?? '';
    })()`);
    recordAssertion(report, 'viewer-explains-related-maximum-with-winning-fhir-source', true,
      cellExplanation.includes('dev-observation-003')
      && cellExplanation.includes('180'));
    await snapshot(cdp, join(evidenceDir, 'viewer-cell-explanation.html'));
    recordEvidence(report, join(evidenceDir, 'viewer-cell-explanation.html'));
    await browserEval(cdp, `clickButton('Close cell explanation')`);
    await waitForBrowser(cdp, `![...document.querySelectorAll('[role="dialog"]')].some((candidate) => candidate.innerText.includes('Why is valueQuantity.value'))`);

    const trainingArtifactDownloadStarted = Date.now();
    await browserEval(cdp, `clickButton('Download dataset')`);
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('[role="dialog"]')].find((dialog) => dialog.innerText.includes('Download dataset') && dialog.querySelector('a[download]')))`, 60000);
    await browserEval(cdp, `const link = [...document.querySelectorAll('a[download]')].find((candidate) => norm(candidate.textContent) === 'Download ZIP'); if (!link) throw new Error('download link not found: Download ZIP'); link.click();`);
    const archivePath = await findDownloadedArchive(downloadDir);
    report.timings.training_artifact_download_ms = Date.now() - trainingArtifactDownloadStarted;
    report.target.trainingArtifactBytes = statSync(archivePath).size;
    recordEvidence(report, archivePath);
    const archive = readStoredZip(archivePath);
    const artifact = inspectJ05ArtifactPackage(archive);
    const { manifest, schema, rows: artifactRows, dataName } = artifact;
    const requiredMembers = expectedTrainingArtifactMembers(manifest.format);
    recordAssertion(report, 'training-artifact-has-fixed-members', requiredMembers, [...archive.keys()]);
    recordAssertion(report, 'training-artifact-data-member-matches-manifest-format', manifest.format, dataName === 'data.csv' ? 'CSV' : 'JSONL');
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
    const schemaNames = schema.columns.map((column) => column.name);
    recordAssertion(report, 'training-artifact-schema-matches-data', true,
      artifactRows.every((row) => JSON.stringify(Object.keys(row.values)) === JSON.stringify(schemaNames)));
    recordAssertion(report, 'training-artifact-has-full-published-row-count', 2, manifest.rows);
    recordAssertion(report, 'training-artifact-has-both-exact-patient-id-columns', expectedPatientIDColumnIDs.map((name) => ({ name, label: 'Patient ID' })),
      schema.columns.filter((column) => expectedPatientIDColumnIDs.includes(column.name)).map((column) => ({ name: column.name, label: column.label })));
    recordAssertion(report, 'training-artifact-preserves-both-literal-patient-id-values', [
      ['dev-patient-001', 'dev-patient-001'],
      ['dev-patient-002', 'dev-patient-002'],
    ], artifactRows.map((row) => expectedPatientIDColumnIDs.map((name) => row.values[name])).sort((left, right) => String(left[0]).localeCompare(String(right[0]))));
    const artifactRowIdentityColumn = manifest.descriptor?.rowIdentity?.sourceIdColumn;
    const artifactRowIdentities = artifactRows.map((row) => row.rowId ?? row.values[artifactRowIdentityColumn]);
    recordAssertion(report, 'training-artifact-has-row-identity-column', true,
      artifactRowIdentities.length === manifest.rows && artifactRowIdentities.every((identity) => identity !== undefined && identity !== null) &&
      new Set(artifactRowIdentities.map((identity) => JSON.stringify(identity))).size === artifactRowIdentities.length);
    recordAssertion(report, 'training-artifact-has-full-published-membership', ['dev-patient-001', 'dev-patient-002'],
      artifactRows.map((row) => row.values[idColumn.runtime.column]).sort());
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

const verifyJ03BrowserScenario = async (target, report, entryTarget = target) => {
  const evidenceDirectory = join(target.artifacts, `${Date.now().toString(36)}-${randomUUID().slice(0, 8)}`);
  const downloadDirectory = join(evidenceDirectory, 'downloads');
  mkdirSync(downloadDirectory, { recursive: true, mode: 0o700 });
  report.target.evidenceDirectory = evidenceDirectory;
  report.target.ports = { api: target.apiPort, ui: target.uiPort };
  recordEvidence(report, evidenceDirectory);
  const explorerId = report.target.bootstrapExplorerId;
  if (!explorerId) throw new Error('J03 bootstrap Explorer identity is missing');
  report.target.explorerId = explorerId;
  const authoring = bootstrapAuthoringURL(target, explorerId);
  const network = [];
  const pendingBodies = new Set();
  const browser = await launchBrowser(downloadDirectory);
  const cdp = browser.cdp;
  cdp.on('Network.requestWillBeSent', (event) => {
    const pathname = new URL(event.request.url).pathname;
    if (!pathname.includes('/authoring/v2/') && !pathname.includes('/selections/')) return;
    network.push({ requestId: event.requestId, url: new URL(event.request.url).pathname, method: event.request.method, postData: event.request.postData });
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
        const body = await cdp.send('Network.getResponseBody', { requestId: item.requestId });
        item.responseBody = JSON.parse(body.base64Encoded ? Buffer.from(body.body, 'base64').toString('utf8') : body.body);
      } catch (error) {
        item.responseBodyError = String(error);
      }
    })().finally(() => pendingBodies.delete(capture));
    pendingBodies.add(capture);
  });

  const readState = () => fetchBuilderState(target, explorerId);
  const captureDOM = async (name) => {
    const path = join(evidenceDirectory, `${name}.html`);
    await snapshot(cdp, path);
    recordEvidence(report, path);
  };
  const waitForBuilderDOM = () => waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Configure rows'))`, 60000);
  const reloadBuilder = async (readyPredicate) => {
    const { frameTree } = await cdp.send('Page.getFrameTree');
    const currentLoaderId = frameTree.frame.loaderId;
    let timeout;
    let onNavigation;
    let onLoad;
    const navigation = new Promise((resolvePromise, reject) => {
      timeout = setTimeout(() => {
        cdp.off('Page.frameNavigated', onNavigation);
        cdp.off('Page.loadEventFired', onLoad);
        reject(new Error('timed out waiting for Builder reload navigation and load event'));
      }, 60000);
      let navigated = false;
      let loaded = false;
      const finish = () => {
        if (!navigated || !loaded) return;
        clearTimeout(timeout);
        cdp.off('Page.frameNavigated', onNavigation);
        cdp.off('Page.loadEventFired', onLoad);
        resolvePromise();
      };
      onNavigation = ({ frame }) => {
        if (frame.parentId || !frame.loaderId || frame.loaderId === currentLoaderId) return;
        navigated = true;
        finish();
      };
      onLoad = () => {
        if (!navigated) return;
        loaded = true;
        finish();
      };
      cdp.on('Page.frameNavigated', onNavigation);
      cdp.on('Page.loadEventFired', onLoad);
    });
    try {
      await cdp.send('Page.reload', { ignoreCache: true });
      await navigation;
    } catch (error) {
      clearTimeout(timeout);
      cdp.off('Page.frameNavigated', onNavigation);
      cdp.off('Page.loadEventFired', onLoad);
      throw error;
    }
    await waitForBrowser(cdp, `document.readyState === 'complete' && (${readyPredicate})`, 60000);
  };
  const waitForResponse = async (path, afterIndex, timeout = 60000) => {
    const started = Date.now();
    while (Date.now() - started < timeout) {
      const match = network.find((item, index) => index > afterIndex && item.url.endsWith(path) && item.responseBody !== undefined);
      if (match) return match;
      await sleep(50);
    }
    throw new Error(`timed out waiting for J03 ${path}: ${JSON.stringify(network.filter((item) => item.url.endsWith(path)).map((item) => ({ status: item.response?.status, error: item.responseBodyError })))}`);
  };
  const selectExpandedPolicy = async (policy) => {
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('select[aria-label="New row definition"] option')].some((option) => option.textContent.includes('EXPANDED') && option.textContent.includes(${JSON.stringify(policy)})))`, 30000);
    return browserEval(cdp, `(() => {
    const select = document.querySelector('select[aria-label="New row definition"]');
    const option = [...(select?.options || [])].find((candidate) => candidate.textContent.includes('EXPANDED') && candidate.textContent.includes(${JSON.stringify(policy)}));
    if (!select || !option) throw new Error('server offered no EXPANDED row choice with policy ' + ${JSON.stringify(policy)});
    Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set?.call(select, option.value);
    select.dispatchEvent(new Event('change', { bubbles: true }));
    return option.textContent.trim();
    })()`);
  };
  const selectExplicitGroupPolicy = async (policy, revisionId) => {
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('select[aria-label="New row definition"] option')].some((option) => option.textContent.includes('Explicit group') && option.textContent.includes(${JSON.stringify(revisionId.slice(0, 12))}) && option.textContent.includes(${JSON.stringify(policy)})))`, 30000);
    return browserEval(cdp, `(() => {
    const select = document.querySelector('select[aria-label="New row definition"]');
    const option = [...(select?.options || [])].find((candidate) => candidate.textContent.includes('Explicit group') && candidate.textContent.includes(${JSON.stringify(revisionId.slice(0, 12))}) && candidate.textContent.includes(${JSON.stringify(policy)}));
    if (!select || !option) throw new Error('server offered no explicit group row choice with policy ' + ${JSON.stringify(policy)});
    Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set?.call(select, option.value);
    select.dispatchEvent(new Event('change', { bubbles: true }));
    return option.textContent.trim();
    })()`);
  };
  const readExpandedSelection = async (state, policy) => {
    const query = new URLSearchParams({ outputId: report.target.outputId, snapshotToken: state.catalog.snapshotToken });
    const { response, value } = await requestJSON(`${authoring}/row-definition-choices?${query}`, { timeout: 30000 });
    if (!response.ok) throw new Error(`J03 row choices returned HTTP ${response.status}: ${JSON.stringify(value)}`);
    const choice = value.choices?.find((candidate) => candidate.kind === 'EXPANDED' && candidate.policies?.some((item) => item.name === 'emptyCollectionPolicy' && item.options.includes(policy)));
    if (!choice) throw new Error(`J03 server offered no EXPANDED row choice with ${policy}`);
    return { kind: 'EXPANDED', expanded: { rowChoiceId: choice.choiceId, emptyCollectionPolicy: policy } };
  };
  const readExplicitGroupSelection = async (state, revisionId, policy) => {
    const query = new URLSearchParams({ outputId: report.target.outputId, snapshotToken: state.catalog.snapshotToken });
    const { response, value } = await requestJSON(`${authoring}/row-definition-choices?${query}`, { timeout: 30000 });
    if (!response.ok) throw new Error(`J03 row choices returned HTTP ${response.status}: ${JSON.stringify(value)}`);
    const choice = value.explicitGroups?.find((candidate) => candidate.revisionId === revisionId && candidate.unassignedMemberPolicies.includes(policy));
    if (!choice) throw new Error(`J03 server did not offer explicit group revision ${revisionId} with ${policy}`);
    return { kind: 'EXPLICIT_GROUP', explicitGroup: { revisionId: choice.revisionId, unassignedMemberPolicy: policy } };
  };
  const postProposal = async (state, selection) => requestJSON(`${authoring}/row-definition-proposals`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, timeout: 30000,
    body: JSON.stringify({ snapshotToken: state.catalog.snapshotToken, expectedDraftVersion: state.draftVersion, expectedDraftDigest: state.draftDigest, outputId: report.target.outputId, selection }),
  });
  const applyProposal = async (state, proposalId) => requestJSON(`${authoring}/commands`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, timeout: 30000,
    body: JSON.stringify({
      commandId: `loom-dev-j03-${randomUUID()}`,
      semanticsVersion: state.workspace.semanticsVersion,
      snapshotToken: state.catalog.snapshotToken,
      expectedDraftVersion: state.draftVersion,
      expectedDraftDigest: state.draftDigest,
      commands: [{ type: 'APPLY_ROW_DEFINITION_PROPOSAL', outputId: report.target.outputId, proposalId }],
    }),
  });
  const captureBrowserProposal = async (selectChoice) => {
    await browserEval(cdp, `if (!document.querySelector('[role="dialog"][aria-labelledby="row-definition-dialog-title"]')) clickButton('Configure rows')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('[role="dialog"][aria-labelledby="row-definition-dialog-title"]'))`);
    const label = await selectChoice();
    const afterIndex = network.length - 1;
    await browserEval(cdp, `clickButton('Preview row change')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('[aria-label="Row definition preview"]')) && Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Apply row definition'))`, 60000);
    const response = await waitForResponse('/row-definition-proposals', afterIndex);
    return { proposal: response.responseBody, label };
  };
  const capturePreview = async () => {
    const afterIndex = network.length - 1;
    await browserEval(cdp, `clickButton('Preview')`);
    const response = await waitForResponse('/preview', afterIndex);
    if (response.response?.status !== 200 || !Array.isArray(response.responseBody?.rows)) {
      throw new Error(`J03 Preview did not return rows: HTTP ${response.response?.status} ${JSON.stringify(response.responseBody).slice(0, 400)}`);
    }
    return response.responseBody;
  };
  try {
    let state = await readState();
    const initialDocument = state.workspace?.documents?.find((candidate) => candidate.rootResourceType === 'Patient');
    if (!initialDocument?.output?.id || initialDocument.rows?.kind !== 'RECORDS') throw new Error('J03 fixture bootstrap has no Patient RECORDS table');
    const outputId = initialDocument.output.id;
    report.target.outputId = outputId;
    const initialFingerprint = draftFingerprint(state);
    recordAssertion(report, 'j03-opens-current-records-table', 'RECORDS', initialDocument.rows.kind);
    const sourceSelection = report.target.explicitGroupFixture?.selection;
    if (!sourceSelection?.id || sourceSelection.memberCount !== 3) throw new Error('J03 existing Explorer selection identity is missing');
    const url = `${target.uiUrl}/?project=${encodeURIComponent(target.fixtureProject)}&explorer=${encodeURIComponent(explorerId)}&selection=${encodeURIComponent(sourceSelection.id)}&mode=builder`;
    await navigate(cdp, entryTarget.uiUrl);
    await navigate(cdp, url);
    await waitForBuilderDOM();
    await captureDOM('j03-builder-row-settings-closed');

    const first = await captureBrowserProposal(() => selectExpandedPolicy('PRESERVE_PARENT'));
    const proposal = first.proposal;
    if (proposal.outputId !== outputId || !proposal.proposalId || proposal.comparison?.status !== 'AVAILABLE') {
      throw new Error(`J03 Builder did not return an applicable row proposal: ${JSON.stringify(proposal).slice(0, 1000)}`);
    }
    state = await readState();
    recordAssertion(report, 'j03-preview-does-not-mutate-draft', initialFingerprint, draftFingerprint(state));
    const examples = proposal.comparison.examples ?? [];
    const membershipChanges = examples.filter((item) => item.basePresent !== item.candidatePresent);
    if (membershipChanges.length === 0) throw new Error(`J03 proposal has no row membership changes: ${JSON.stringify(proposal.comparison)}`);
    const dialogText = String(await evaluate(cdp, `document.querySelector('[role="dialog"][aria-labelledby="row-definition-dialog-title"]')?.innerText || ''`));
    recordAssertion(report, 'j03-preview-shows-base-and-candidate-counts', true,
      Number.isInteger(proposal.comparison.base?.rowCount) && Number.isInteger(proposal.comparison.candidate?.rowCount) &&
      dialogText.includes(`Base rows: ${proposal.comparison.base.rowCount}`) && dialogText.includes(`Candidate rows: ${proposal.comparison.candidate.rowCount}`));
    const renderedChanges = await evaluate(cdp, `JSON.stringify([...document.querySelectorAll('ul[aria-label="Membership changes"] li')].map((item) => item.innerText.trim()).filter((item) => !item.startsWith('Unchanged · ')))`);
    const expectedChanges = membershipChanges.map((item) => `${item.candidatePresent ? 'Added' : 'Removed'} · ${item.rowIdentity}`);
    recordAssertion(report, 'j03-preview-shows-literal-membership-changes', expectedChanges, JSON.parse(renderedChanges));
    const explicitOptionCount = await evaluate(cdp, `document.querySelector('[role="dialog"] select[aria-label="New row definition"]') ? [...document.querySelector('[role="dialog"] select[aria-label="New row definition"]').options].filter((option) => option.textContent.includes('Explicit group')).length : -1`);
    recordAssertion(report, 'j03-explicit-group-choice-is-server-provided', {
      serverReason: true, explicitGroupOptions: 0,
    }, {
      serverReason: dialogText.includes('The server has no complete explicit group revisions for this table.'),
      explicitGroupOptions: explicitOptionCount,
    });
    report.target.rowDefinitionComparison = { base: proposal.comparison.base, candidate: proposal.comparison.candidate, examples, membershipChanges };
    await captureDOM('j03-row-proposal-preview');
    await browserEval(cdp, `(() => {
      const dialog = document.querySelector('[role="dialog"][aria-labelledby="row-definition-dialog-title"]');
      const button = [...(dialog?.querySelectorAll('button') || [])].find((candidate) => norm(candidate.textContent) === 'Cancel');
      if (!button) throw new Error('row-definition dialog has no Cancel button');
      button.click();
      return true;
    })()`);
    await waitForBrowser(cdp, `!document.querySelector('[role="dialog"][aria-labelledby="row-definition-dialog-title"]')`);
    recordAssertion(report, 'j03-cancel-does-not-mutate-draft', initialFingerprint, draftFingerprint(await readState()));

    const alternateState = await readState();
    const alternate = await postProposal(alternateState, await readExpandedSelection(alternateState, 'EXCLUDE'));
    if (!alternate.response.ok || !alternate.value.proposalId) throw new Error(`J03 could not create an intervening proposal: HTTP ${alternate.response.status} ${JSON.stringify(alternate.value)}`);
    const appliedAlternate = await applyProposal(alternateState, alternate.value.proposalId);
    if (!appliedAlternate.response.ok) throw new Error(`J03 intervening proposal apply returned HTTP ${appliedAlternate.response.status}: ${JSON.stringify(appliedAlternate.value)}`);
    state = await readState();
    const rejectedStale = await applyProposal(state, proposal.proposalId);
    recordAssertion(report, 'j03-stale-server-proposal-is-rejected', 409, rejectedStale.response.status);
    recordAssertion(report, 'j03-rejected-stale-proposal-does-not-mutate-draft', draftFingerprint(state), draftFingerprint(await readState()));

    await reloadBuilder(`Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Configure rows'))`);
    const sourceMemberIDs = report.target.explicitGroupFixture.sourceMemberIDs;
    await browserEval(cdp, `clickButton('Configure rows')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('[role="dialog"][aria-labelledby="row-definition-dialog-title"] button') && [...document.querySelectorAll('button')].some((button) => button.textContent.trim() === 'Create groups from this selection'))`, 30000);
    const beforeGroupSetup = draftFingerprint(await readState());
    const explicitGroupPostCountBeforeCancel = network.filter((item) => item.method === 'POST' && item.url.endsWith('/explicit-groups')).length;
    await browserEval(cdp, `clickButton('Create groups from this selection')`);
    await waitForBrowser(cdp, `document.querySelectorAll('input[aria-label^="Assign Record "]').length === 6`, 30000);
    await captureDOM('j03-group-authoring-preview-before-cancel');
    await browserEval(cdp, `clickButton('Cancel group setup')`);
    await waitForBrowser(cdp, `!document.querySelector('[aria-label="Create explicit groups"]')`);
    recordAssertion(report, 'j03-cancel-group-setup-does-not-mutate-draft', beforeGroupSetup, draftFingerprint(await readState()));
    recordAssertion(report, 'j03-cancel-group-setup-does-not-create-a-revision', explicitGroupPostCountBeforeCancel,
      network.filter((item) => item.method === 'POST' && item.url.endsWith('/explicit-groups')).length);

    const selectionReadIndex = network.length - 1;
    await browserEval(cdp, `clickButton('Create groups from this selection')`);
    await waitForBrowser(cdp, `document.querySelectorAll('input[aria-label^="Assign Record "]').length === 6`, 30000);
    const sourcePage = await waitForResponse(`/selections/${sourceSelection.id}`, selectionReadIndex);
    if (sourcePage.response?.status !== 200 || sourcePage.responseBody?.revision?.id !== sourceSelection.id || sourcePage.responseBody.members?.length !== 3) {
      throw new Error(`J03 group editor did not load the exact three-member source selection: ${JSON.stringify(sourcePage.responseBody).slice(0, 700)}`);
    }
    const selectedMembers = sourcePage.responseBody.members;
    const selectedKeys = selectedMembers.map((member) => member.memberKey);
    const selectedIDs = selectedMembers.map((member) => member.ref?.id);
    recordAssertion(report, 'j03-group-editor-uses-three-opaque-members-from-existing-selection', sourceMemberIDs, [...selectedIDs].sort());
    if (selectedKeys.some((key) => typeof key !== 'string' || key.length === 0) || new Set(selectedKeys).size !== 3) {
      throw new Error('J03 existing selection did not provide three unique opaque member keys');
    }
    const groupAName = 'J03 Alpha';
    const groupBName = 'J03 Beta';
    await browserEval(cdp, `setInput('Group 1 name', ${JSON.stringify(groupAName)})`);
    await browserEval(cdp, `setInput('Group 2 name', ${JSON.stringify(groupBName)})`);
    for (const [memberIndex, groupName] of [[0, groupAName], [1, groupAName], [1, groupBName], [2, groupBName]]) {
      const checkboxLabel = `Assign Record ${memberIndex + 1} · ${selectedIDs[memberIndex]} to ${groupName}`;
      await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label=' + JSON.stringify(${JSON.stringify(checkboxLabel)}) + ']'))`, 30000);
      await browserEval(cdp, `(() => { const input = document.querySelector('input[aria-label=' + JSON.stringify(${JSON.stringify(checkboxLabel)}) + ']'); if (!input || input.checked) throw new Error('J03 group membership checkbox is unavailable or already selected: ' + ${JSON.stringify(checkboxLabel)}); input.click(); })()`);
    }
    const exactMembershipRows = JSON.parse(await evaluate(cdp, `JSON.stringify([...document.querySelectorAll('[aria-label="Exact group memberships"] li')].map((item) => item.innerText.replace(/\\s+/g, ' ').trim()))`));
    const expectedMembershipPreview = [
      `${groupAName}: Record 1 · ${selectedIDs[0]}, Record 2 · ${selectedIDs[1]}`,
      `${groupBName}: Record 2 · ${selectedIDs[1]}, Record 3 · ${selectedIDs[2]}`,
    ];
    recordAssertion(report, 'j03-group-editor-previews-exact-overlapping-memberships', expectedMembershipPreview, exactMembershipRows);
    await captureDOM('j03-group-authoring-exact-memberships');
    const createGroupsAfter = network.length - 1;
    await browserEval(cdp, `clickButton('Create group revision')`);
    const createGroupsResponse = await waitForResponse('/explicit-groups', createGroupsAfter);
    if (createGroupsResponse.method !== 'POST' || createGroupsResponse.response?.status !== 201 || !createGroupsResponse.postData) {
      throw new Error(`J03 explicit-group API transaction failed: ${JSON.stringify({ method: createGroupsResponse.method, status: createGroupsResponse.response?.status, response: createGroupsResponse.responseBody }).slice(0, 1000)}`);
    }
    const createGroupsRequest = JSON.parse(createGroupsResponse.postData);
    const explicitGroup = createGroupsResponse.responseBody;
    const expectedGroupMemberKeys = [selectedKeys.slice(0, 2), selectedKeys.slice(1, 3)];
    if (createGroupsRequest.groups?.length !== 2 || createGroupsRequest.groups.some((group) => !Array.isArray(group.memberIds)) ||
        createGroupsRequest.groups.some((group) => group.memberIds.some((memberId) => !selectedKeys.includes(memberId))) ||
        JSON.stringify(createGroupsRequest.groups.map((group) => group.memberIds)) !== JSON.stringify(expectedGroupMemberKeys) ||
        !createGroupsRequest.groups[0].memberIds.includes(selectedKeys[1]) || !createGroupsRequest.groups[1].memberIds.includes(selectedKeys[1]) ||
        explicitGroup.sourceSelectionRevisionId !== sourceSelection.id || explicitGroup.groupCount !== 2 || explicitGroup.memberCount !== 4) {
      throw new Error(`J03 group revision did not preserve two groups and four exact opaque relations: ${JSON.stringify({ request: createGroupsRequest, response: explicitGroup }).slice(0, 1000)}`);
    }
    const groupDefinitions = createGroupsRequest.groups.map((group) => ({
      id: group.id,
      label: group.label,
      memberIds: group.memberIds,
      sourceMemberIDs: selectedMembers.filter((member) => group.memberIds.includes(member.memberKey)).map((member) => member.ref.id).sort(),
    }));
    recordAssertion(report, 'j03-create-uses-only-opaque-keys-from-exact-selection', {
      selectionRevision: sourceSelection.id,
      groupCount: 2,
      memberCount: 4,
      groupNames: [groupAName, groupBName],
      membersAreOpaque: true,
    }, {
      selectionRevision: explicitGroup.sourceSelectionRevisionId,
      groupCount: explicitGroup.groupCount,
      memberCount: explicitGroup.memberCount,
      groupNames: createGroupsRequest.groups.map((group) => group.label),
      membersAreOpaque: createGroupsRequest.groups.every((group) => group.memberIds.every((memberId) => selectedKeys.includes(memberId))) && !JSON.stringify(createGroupsRequest).includes('resourceType'),
    });
    report.target.explicitGroupFixture.explicitGroup = {
      revisionId: explicitGroup.revisionId,
      sourceSelectionRevisionId: explicitGroup.sourceSelectionRevisionId,
      sourceMemberIds: [...selectedIDs].sort(),
      groups: groupDefinitions,
      groupCount: explicitGroup.groupCount,
      memberCount: explicitGroup.memberCount,
      unassignedMemberIds: [],
    };
    await waitForBrowser(cdp, `!document.querySelector('[aria-label="Create explicit groups"]')`);
    const freshBaseFingerprint = draftFingerprint(await readState());
    const fresh = await captureBrowserProposal(() => selectExplicitGroupPolicy('ERROR', explicitGroup.revisionId));
    if (!fresh.proposal.proposalId || fresh.proposal.comparison?.status !== 'AVAILABLE') throw new Error(`J03 fresh proposal was unavailable: ${JSON.stringify(fresh.proposal).slice(0, 1000)}`);
    const freshExamples = fresh.proposal.comparison.examples ?? [];
    const freshMembershipChanges = freshExamples.filter((item) => item.basePresent !== item.candidatePresent);
    if (fresh.proposal.comparison.candidate?.rowCount !== 2) {
      throw new Error(`J03 explicit-group proposal candidate count was ${fresh.proposal.comparison.candidate?.rowCount}, want two rows`);
    }
    state = await readState();
    recordAssertion(report, 'j03-explicit-preview-does-not-mutate-draft', freshBaseFingerprint, draftFingerprint(state));
    const explicitDialogText = String(await evaluate(cdp, `document.querySelector('[role="dialog"][aria-labelledby="row-definition-dialog-title"]')?.innerText || ''`));
    recordAssertion(report, 'j03-explicit-preview-shows-base-and-candidate-counts', true,
      explicitDialogText.includes(`Base rows: ${fresh.proposal.comparison.base.rowCount}`) &&
      explicitDialogText.includes(`Candidate rows: ${fresh.proposal.comparison.candidate.rowCount}`));
    const explicitRenderedChanges = await evaluate(cdp, `JSON.stringify([...document.querySelectorAll('ul[aria-label="Membership changes"] li')].map((item) => item.innerText.trim()).filter((item) => !item.startsWith('Unchanged · ')))`);
    const expectedExplicitChanges = freshMembershipChanges.map((item) => `${item.candidatePresent ? 'Added' : 'Removed'} · ${item.rowIdentity}`);
    recordAssertion(report, 'j03-explicit-preview-shows-fresh-membership-changes', expectedExplicitChanges, JSON.parse(explicitRenderedChanges));
    report.target.selectedExplicitGroupChoice = fresh.label;
    await captureDOM('j03-row-proposal-fresh');
    await browserEval(cdp, `clickButton('Apply row definition')`);
    await waitForBrowser(cdp, `!document.querySelector('[role="dialog"][aria-labelledby="row-definition-dialog-title"]')`, 60000);
    state = await readState();
    const appliedDocument = state.workspace?.documents?.find((candidate) => candidate.output?.id === outputId);
    const appliedGroup = appliedDocument?.rows?.kind === 'GROUPS' && appliedDocument.rows.groups?.source?.kind === 'EXPLICIT'
      ? appliedDocument.rows.groups.source.explicit
      : undefined;
    if (!appliedDocument || !appliedGroup || appliedGroup.revisionId !== explicitGroup.revisionId || appliedGroup.unassignedMemberPolicy !== 'ERROR') {
      throw new Error(`J03 applied row definition differs from the fresh server proposal: ${JSON.stringify(appliedDocument?.rows)}`);
    }
    recordAssertion(report, 'j03-applies-explicit-group-row-definition', {
      kind: 'EXPLICIT_GROUP', revisionId: explicitGroup.revisionId, policy: 'ERROR',
    }, { kind: appliedGroup ? 'EXPLICIT_GROUP' : appliedDocument?.rows?.kind, revisionId: appliedGroup?.revisionId, policy: appliedGroup?.unassignedMemberPolicy });
    const preview = await capturePreview();
    const fixturePatientIDs = report.target.explicitGroupFixture.explicitGroup.sourceMemberIds;
    const expectedGroupedRows = groupDefinitions.map((group) => ({
      rowIdentity: `map[group_id:${group.id} group_revision_id:${explicitGroup.revisionId}]`,
      groupRevisionId: explicitGroup.revisionId,
      groupId: group.id,
      sourceMemberIDs: group.sourceMemberIDs,
    }));
    const actualGroupedRows = explicitGroupPreviewRows(preview.rows).map(({ rawIdentity, ...row }) => row);
    recordAssertion(report, 'j03-preview-contains-exact-explicit-group-identities-and-membership', expectedGroupedRows, actualGroupedRows);
    const expectedSourceTuples = groupDefinitions.map((group) => ({
      groupRevisionId: explicitGroup.revisionId,
      groupId: group.id,
      sourceTuples: group.sourceMemberIDs.map((id) => {
        const member = selectedMembers.find((candidate) => candidate.ref.id === id);
        if (!member) throw new Error(`J03 selection omitted grouped source identity ${id}`);
        return { project: member.ref.project, generation: member.ref.generation, resourceType: member.ref.resourceType, id };
      }),
    }));
    const readPreviewSourceTuples = (rows) => rows.map((row) => ({
      groupRevisionId: row.__loom_row_id?.group_revision_id,
      groupId: row.__loom_row_id?.group_id,
      sourceTuples: (row.members ?? []).map((member) => ({
        project: member.source_identity?.project,
        generation: member.source_identity?.generation,
        resourceType: member.source_identity?.resource_type ?? member.source_identity?.resourceType,
        id: member.source_identity?.id,
      })).sort((left, right) => String(left.id).localeCompare(String(right.id))),
    }));
    recordAssertion(report, 'j03-preview-retains-exact-source-tuples', expectedSourceTuples, readPreviewSourceTuples(preview.rows));
    const candidateGroupIdentities = freshExamples.filter((item) => item.candidatePresent).map((item) => item.rowIdentity).sort();
    recordAssertion(report, 'j03-explicit-group-proposal-identities-match-live-preview', expectedGroupedRows.map((row) => row.rowIdentity).sort(), candidateGroupIdentities);
    const previewMemberIDs = [...new Set(actualGroupedRows.flatMap((row) => row.sourceMemberIDs))].sort();
    recordAssertion(report, 'j03-preview-contains-exact-source-selection-membership', fixturePatientIDs, previewMemberIDs);
    recordAssertion(report, 'j03-preview-row-count-matches-server-candidate-count', fresh.proposal.comparison.candidate.rowCount, preview.rows.length);
    report.target.preview = {
      rowCount: preview.rows.length,
      candidateRowCount: fresh.proposal.comparison.candidate.rowCount,
      policy: 'ERROR',
      sourceMemberIDs: fixturePatientIDs,
      rowIdentities: actualGroupedRows.map((row) => row.rowIdentity),
      groupedRows: actualGroupedRows,
      membershipChanges: freshMembershipChanges,
    };

    const savedRows = structuredClone(appliedDocument.rows);
    const savedVersion = state.draftVersion;
    const savedDigest = state.draftDigest;
    await reloadBuilder(`document.body.innerText.includes(${JSON.stringify(`Current rows: EXPLICIT_GROUP · ${explicitGroup.revisionId} · ERROR`)})`);
    const reloaded = await readState();
    const reloadedDocument = reloaded.workspace?.documents?.find((candidate) => candidate.output?.id === outputId);
    recordAssertion(report, 'j03-reload-persists-row-definition', savedRows, reloadedDocument?.rows);
    recordAssertion(report, 'j03-reload-persists-draft-identity', { draftVersion: savedVersion, draftDigest: savedDigest }, { draftVersion: reloaded.draftVersion, draftDigest: reloaded.draftDigest });
    const currentRowsText = String(await evaluate(cdp, 'document.body.innerText'));
    recordAssertion(report, 'j03-reloaded-builder-inspects-explicit-group-current-rows', true,
      currentRowsText.includes(`Current rows: EXPLICIT_GROUP · ${explicitGroup.revisionId} · ERROR`));
    const reloadedPreview = await capturePreview();
    const reloadedGroupedRows = explicitGroupPreviewRows(reloadedPreview.rows).map(({ rawIdentity, ...row }) => row);
    recordAssertion(report, 'j03-reload-preview-persists-exact-row-membership', expectedGroupedRows, reloadedGroupedRows);
    recordAssertion(report, 'j03-reload-preview-preserves-stable-row-identities', expectedGroupedRows.map((row) => row.rowIdentity).sort(),
      reloadedGroupedRows.map((row) => row.rowIdentity).sort());
    recordAssertion(report, 'j03-reload-preview-retains-exact-source-tuples', expectedSourceTuples, readPreviewSourceTuples(reloadedPreview.rows));

    const reconciled = await requestJSON(`${authoring}/reconcile`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, timeout: 90000,
      body: JSON.stringify({ snapshotToken: reloaded.catalog.snapshotToken, draftVersion: reloaded.draftVersion, draftDigest: reloaded.draftDigest }),
    });
    if (!reconciled.response.ok || !reconciled.value?.receiptId) {
      throw new Error(`J03 receipt-backed export reconcile failed: HTTP ${reconciled.response.status} ${JSON.stringify(reconciled.value).slice(0, 700)}`);
    }
    const published = await requestJSON(`${authoring}/publish`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, timeout: 90000,
      body: JSON.stringify({ receiptId: reconciled.value.receiptId }),
    });
    if (!published.response.ok || !published.value?.revisionId) {
      throw new Error(`J03 receipt-backed export publish failed: HTTP ${published.response.status} ${JSON.stringify(published.value).slice(0, 700)}`);
    }
    recordAssertion(report, 'j03-publish-uses-current-immutable-receipt', reconciled.value.receiptId, published.value.receiptId);
    const prepared = await requestJSON(`${authoring}/artifacts`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, timeout: 90000,
      body: JSON.stringify({ revisionId: published.value.revisionId, outputId, idempotencyKey: `loom-dev-j03-${randomUUID()}` }),
    });
    if (!prepared.response.ok || prepared.value?.state !== 'COMPLETE' || prepared.value?.revisionId !== published.value.revisionId) {
      throw new Error(`J03 artifact preparation failed: HTTP ${prepared.response.status} ${JSON.stringify(prepared.value).slice(0, 700)}`);
    }
    const archiveResponse = await fetch(`${authoring}/artifacts/${encodeURIComponent(prepared.value.id)}`, { headers: { Accept: 'application/zip' } });
    if (!archiveResponse.ok) throw new Error(`J03 artifact download returned HTTP ${archiveResponse.status}`);
    const archivePath = join(evidenceDirectory, 'j03-explicit-group-export.zip');
    const archiveBytes = Buffer.from(await archiveResponse.arrayBuffer());
    writeFileSync(archivePath, archiveBytes, { mode: 0o600 });
    recordEvidence(report, archivePath);
    const artifact = inspectJ05ArtifactPackage(readStoredZip(archivePath));
    const expectedArtifactIdentities = expectedGroupedRows.map((row) => ({ groupRevisionId: row.groupRevisionId, groupId: row.groupId }));
    const artifactIdentities = artifact.rows.map((row) => {
      const identity = row.rowId ?? row.values?.[artifact.manifest.descriptor.rowIdentity.sourceIdColumn];
      if (typeof identity === 'string') {
        try { return JSON.parse(identity); } catch { return identity; }
      }
      return identity;
    });
    recordAssertion(report, 'j03-export-describes-synthetic-group-grain', {
      rowGrain: 'groups', sourceResourceType: null, sourceIdColumn: null,
    }, {
      rowGrain: artifact.manifest.descriptor.rowGrain,
      sourceResourceType: artifact.manifest.descriptor.rowIdentity.sourceResourceType ?? null,
      sourceIdColumn: artifact.manifest.descriptor.rowIdentity.sourceIdColumn ?? null,
    });
    recordAssertion(report, 'j03-export-retains-typed-explicit-group-row-identities', expectedArtifactIdentities,
      artifactIdentities.map((identity) => ({ groupRevisionId: identity?.group_revision_id, groupId: identity?.group_id })));
    recordAssertion(report, 'j03-export-retains-stable-explicit-group-row-identities', expectedArtifactIdentities.length,
      new Set(artifactIdentities.map((identity) => JSON.stringify(identity))).size);
    const artifactMembersColumn = artifact.schema.columns.find((column) => column.name === 'members');
    if (!artifactMembersColumn || artifactMembersColumn.logicalType !== 'object' || artifactMembersColumn.shape !== 'record_list' || !artifactMembersColumn.repeated) {
      throw new Error(`J03 export schema omitted the repeated object membership tuples: ${JSON.stringify(artifactMembersColumn)}`);
    }
    const exportedSourceTuples = artifact.rows.map((row, index) => {
      const identity = artifactIdentities[index];
      const members = row.values[artifactMembersColumn.name];
      if (!Array.isArray(members) || members.some((member) => !member || typeof member !== 'object' ||
          !member.source_identity || typeof member.source_identity !== 'object' || Array.isArray(member.source_identity))) {
        throw new Error(`J03 export row ${index + 1} did not retain nested source member tuples: ${JSON.stringify(members)}`);
      }
      return {
        groupRevisionId: identity?.group_revision_id,
        groupId: identity?.group_id,
        sourceTuples: members.map((member) => ({
          project: member.source_identity.project,
          generation: member.source_identity.generation,
          resourceType: member.source_identity.resource_type,
          id: member.source_identity.id,
        })).sort((left, right) => left.id.localeCompare(right.id)),
      };
    });
    const expectedExportSourceTuples = groupDefinitions.map((group) => ({
      groupRevisionId: explicitGroup.revisionId,
      groupId: group.id,
      sourceTuples: group.sourceMemberIDs.map((id) => {
        const member = selectedMembers.find((candidate) => candidate.ref.id === id);
        if (!member) throw new Error(`J03 export cannot reconstruct source identity ${id}`);
        return {
          project: member.ref.project,
          generation: member.ref.generation,
          resourceType: member.ref.resourceType,
          id: member.ref.id,
        };
      }),
    }));
    recordAssertion(report, 'j03-export-reconstructs-exact-source-tuples-with-stable-group-identities', expectedExportSourceTuples, exportedSourceTuples);
    recordAssertion(report, 'j03-export-row-count-matches-exact-group-memberships', expectedGroupedRows.length, artifact.rows.length);
    report.target.export = {
      path: archivePath,
      bytes: archiveBytes.length,
      revisionId: published.value.revisionId,
      receiptId: reconciled.value.receiptId,
      rowIdentities: artifactIdentities,
      sourceTuples: exportedSourceTuples,
      groupedRows: expectedGroupedRows,
    };
    await captureDOM('j03-reloaded-explicit-group-preview');
    await Promise.allSettled([...pendingBodies]);
    const networkPath = join(evidenceDirectory, 'network-identities.json');
    writeJSON(networkPath, network);
    recordEvidence(report, networkPath);
    writeJSON(join(evidenceDirectory, 'report.json'), { status: 'passed', scenario: 'J03-row-definition-settings', target: report.target, assertions: report.assertions, evidencePaths: report.evidencePaths });
    console.log(`DEV_J03_BROWSER_PASSED explorer=${explorerId} output=${outputId} evidence=${evidenceDirectory}`);
  } catch (error) {
    await captureDOM('failure');
    const networkPath = join(evidenceDirectory, 'network-failure.json');
    writeJSON(networkPath, network.map(({ requestId, url, method, postData, response, responseBody, responseBodyError }) => ({
      requestId, url, method, postData, response,
      responseBodyError,
      responseBody: response?.status >= 400 ? responseBody : undefined,
      responseKeys: responseBody && typeof responseBody === 'object' ? Object.keys(responseBody) : [],
    })));
    recordEvidence(report, networkPath);
    throw error;
  } finally {
    await browser.close();
    rmSync(downloadDirectory, { recursive: true, force: true });
  }
};

const j04UnprovenAssertions = [
  'j04-count-exists-min-max-mean-sum-match-literal-oracle',
  'j04-earliest-latest-tie-and-window-exclusion-match-literal-oracle',
  'j04-unit-normalization-preserves-zero-and-refuses-unsupported-units',
  'j04-unsupported-unit-preview-refuses-without-applying',
  'j04-exact-recoding-honors-case-and-unknown-policy',
  'j04-pivot-source-columns-come-from-the-saved-catalog',
  'j04-pivot-freezes-category-names-and-policies',
  'j04-observation-preview-materializes-missing-values-as-null-cells',
  'j04-observation-cell-trace-proves-literals-and-exact-source-row-identities',
  'j04-missing-valueString-is-a-null-no-match-cell',
  'j04-recorded-absence-is-visibly-emitted-with-exact-coding-and-source',
  'j04-preview-preserves-missing-false-zero-and-whitespace-blank-source-values',
  'j04-recorded-absence-code-is-visible-in-preview',
  'j04-recorded-absence-cell-trace-pins-source-observation',
  'j04-pivot-derived-alpha-plus-beta-matches-literals-with-null-propagation',
  'j04-derived-numeric-division-by-zero-and-later-reference-match-literals',
  'j04-preview-is-nonmutating-and-cancel-discards-the-proposal',
  'j04-receipt-apply-and-stale-proposal-rejection-preserve-workspace-contract',
  'j04-complete-exclusion-partition-proves-exact-retained-pivot-sources',
  'j04-pivot-exclusion-evidence-is-complete-and-exact',
  'j04-grouped-pivot-information-loss-names-every-dropped-column',
  'j04-reload-preserves-stable-output-and-column-identities',
  'j04-preview-viewer-and-downloaded-typed-artifact-agree-exactly',
  'j04-unrelated-column-remains-literal',
  'j04-contributor-exclusion-and-information-loss-evidence-is-exact',
];

const markJ04DownstreamUnproven = (report) => {
  const detail = report.target.firstMissingDOMAction
    ? 'The Builder route did not render the required UI04 editor action.'
    : `The journey stopped before those assertions could run: ${report.error ?? 'setup did not complete'}`;
  for (const name of j04UnprovenAssertions) {
    if (report.assertions.some((assertion) => assertion.name === name)) continue;
    report.assertions.push({ name, status: 'not-proven', detail });
  }
};

const j04WorkspaceSnapshot = (state) => ({
  draftVersion: state?.draftVersion,
  draftDigest: state?.draftDigest,
  semanticsVersion: state?.workspace?.semanticsVersion,
  workspace: state?.workspace,
});

const j04EvidenceDocument = (report) => {
  const missing = report.target.firstMissingDOMAction;
  const lines = [
    '# J04 browser verification evidence',
    '',
    `Status: ${report.status}`,
    `Evidence directory: ${report.target.evidenceDirectory ?? '(not created)'}`,
    `Fixture project: ${report.target.project ?? '(not created)'}`,
    `Fixture generation: ${report.target.generation ?? '(unknown)'}`,
    '',
    missing
      ? `First missing DOM action: ${missing.action} requires selector ${missing.selector}. ${missing.reason}`
      : `First missing DOM action: ${report.error ?? 'none recorded'}`,
    '',
    'Assertions after the first missing action are marked not-proven in report.json.',
    'The driver did not substitute API authoring for the missing Builder action.',
  ];
  return `${lines.join('\n')}\n`;
};

const verifyJ04PatientOperatorScenario = async (target, report, entryTarget, fixture) => {
  const controlPlan = j04PatientOperatorDOMPlan(fixture.contract);
  const evidenceDirectory = join(report.target.evidenceDirectory, 'patient-operator');
  const downloadDirectory = join(evidenceDirectory, 'downloads');
  mkdirSync(downloadDirectory, { recursive: true, mode: 0o700 });
  report.target.patientOperatorEvidenceDirectory = evidenceDirectory;
  report.target.patientOperatorControlPlan = controlPlan;
  report.target.patientOperatorJourney = { status: 'running', actions: [], previews: [], proposals: [], receipts: [] };
  report.actions ??= [];
  const journey = report.target.patientOperatorJourney;
  const network = [];
  const pendingNetworkBodies = new Set();
  let browser;
  let cdp;
  let explorerId;
  let outputId;
  let failure;
  const started = Date.now();
  const captureDOM = async (name) => {
    const path = join(evidenceDirectory, `${name}.html`);
    await snapshot(cdp, path);
    recordEvidence(report, path);
    return path;
  };
  const captureScreenshot = async (name) => {
    const shot = await cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
    const path = join(evidenceDirectory, `${name}.png`);
    writeFileSync(path, Buffer.from(shot.data, 'base64'), { mode: 0o600 });
    recordEvidence(report, path);
    return path;
  };
  const action = async (name, operation) => {
    const actionStarted = Date.now();
    await operation();
    journey.actions.push({ name, elapsedMs: Date.now() - actionStarted });
  };
  const waitForState = async (predicate, label, timeout = 45000) => {
    const deadline = Date.now() + timeout;
    let state;
    while (Date.now() < deadline) {
      state = await fetchBuilderState(target, explorerId);
      if (predicate(state)) return state;
      await sleep(150);
    }
    throw new Error(`J04 Patient Builder did not reach ${label}: ${JSON.stringify(state?.workspace?.documents?.map((doc) => ({ id: doc.output?.id, rows: doc.rootResourceType, columns: doc.columns?.length })))}`);
  };
  const candidatePath = (candidate) => String(candidate?.fieldPath ?? '').replace(/^root\./, '');
  const recordLiteral = (name, expected, actual, details = {}) => {
    const passed = j04ExactEqual(expected, actual);
    report.assertions.push({ name, expected, actual, ...details, status: passed ? 'passed' : 'failed' });
    return passed;
  };
  const readCellTrace = async (receiptId, column, rowIdentity, rawRow) => {
    const patientID = rowIdentity?.replace(/^Patient\//, '');
    const candidates = [
      rawRow?.__loom_row_id,
      rawRow?.rowId,
      patientID && j04DefaultRecordCellTraceRowID(
        target.fixtureProject,
        target.fixtureGeneration,
        'Patient',
        patientID,
      ),
      patientID,
      rowIdentity,
    ].filter((value, index, values) => typeof value === 'string' && value.length > 0 && values.indexOf(value) === index);
    const url = `${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorerId)}/authoring/v2/cell-trace`;
    const attempts = [];
    for (const rowId of candidates) {
      const result = await requestJSON(url, {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, timeout: 30000,
        body: JSON.stringify({ receiptId, outputId, rowId, column, limit: 100 }),
      });
      attempts.push({ rowId, status: result.response.status, body: result.value });
      if (result.response.ok && result.value?.trace?.complete === true) return { rowId, ...result.value, attempts };
    }
    return { unavailable: true, attempts };
  };
  const readVisiblePreview = async (label, expected, expectedContributorIDs = [], requestStart = 0) => {
    await waitForBrowser(cdp, `Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))`, 60000);
    const visible = await evaluate(cdp, `(() => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      const rows = [...(table?.querySelectorAll('[role="row"]') || [])];
      const headers = [...(rows[0]?.querySelectorAll('[role="columnheader"]') || [])].map((cell) => cell.textContent.trim());
      return rows.slice(1).map((row) => Object.fromEntries([...row.querySelectorAll('[role="cell"]')].map((cell, index) => [headers[index], cell.textContent.trim()])));
    })()`);
    const state = await fetchBuilderState(target, explorerId);
    const receiptId = state.receipt?.receiptId ?? null;
    const visibleRow = visible.find((row) => row['Patient ID'] === 'j04-patient-001');
    const visibleLabel = Object.keys(visibleRow ?? {}).find((header) => header === label || header.startsWith(`${label} (`));
    const actual = visibleLabel ? visibleRow?.[visibleLabel] ?? null : null;
    const literal = typeof expected === 'number' && actual !== null ? Number(actual) : typeof expected === 'boolean' ? actual === 'true' : actual;
    let response;
    const responseDeadline = Date.now() + 60000;
    while (Date.now() < responseDeadline) {
      await Promise.allSettled([...pendingNetworkBodies]);
      response = network.slice(requestStart).reverse().find((item) => item.path.endsWith('/preview')
        && item.status >= 200 && item.status < 300 && item.responseJSON?.rows);
      if (response) break;
      await sleep(150);
    }
    if (!response) throw new Error(`J04 Patient Preview returned no receipt-backed row response for ${label}`);
    const authoredColumns = state.workspace?.documents?.find((item) => item.output?.id === outputId)?.columns ?? [];
    const patientIDColumn = authoredColumns.find((column) => column.label === 'Patient ID');
    const authoredColumn = authoredColumns.find((column) => column.label === label);
    const rawRow = response?.responseJSON?.rows?.find((row) => row?.[patientIDColumn?.column] === 'j04-patient-001');
    const resultColumn = response?.responseJSON?.columns?.find((column) => column.column === authoredColumn?.column);
    const actualReceiptId = response?.responseJSON?.receiptId ?? receiptId;
    const trace = actualReceiptId && authoredColumn
      ? await readCellTrace(actualReceiptId, authoredColumn.column, 'Patient/j04-patient-001', rawRow)
      : { unavailable: true, reason: 'preview receipt or authored column identity is unavailable' };
    const sourceIdentities = trace.trace?.contributions?.flatMap((item) =>
      item.resourceType && item.resourceId ? [`${item.resourceType}/${item.resourceId}`] : []) ?? [];
    const preview = {
      label,
      rows: visible,
      selectedSourceIdentity: visibleRow?.['Patient ID'] ? `Patient/${visibleRow['Patient ID']}` : null,
      expected,
      actual: literal,
      receiptId: actualReceiptId,
      outputId: response?.responseJSON?.outputId ?? outputId,
      resultUnit: resultColumn?.resultUnit ?? null,
      responseRowCount: response?.responseJSON?.rowCount ?? null,
      cellTrace: trace,
      sourceIdentities,
      draftVersion: state.draftVersion,
      draftDigest: state.draftDigest,
    };
    journey.previews.push(preview);
    if (preview.receiptId) journey.receipts.push({ stage: label, receiptId: preview.receiptId });
    journey.proposals.push({ stage: label, proposalId: null, reason: 'Column policy edits compile directly; this Builder interaction creates no table-shape proposal.' });
    recordLiteral(`j04-patient-preview-${label}-matches-literal`, expected, literal, { receiptId: actualReceiptId, draftVersion: state.draftVersion });
    recordLiteral(`j04-patient-preview-${label}-retains-selected-patient-identity`, 'Patient/j04-patient-001', preview.selectedSourceIdentity, { receiptId: preview.receiptId });
    const expectedSourceIdentities = expectedContributorIDs.map((id) => `Observation/${id}`).sort();
    if (trace.trace?.complete === true) {
      recordLiteral(`j04-patient-preview-${label}-source-identities-match-literal`, expectedSourceIdentities, sourceIdentities.sort(), { receiptId: actualReceiptId, rowId: trace.rowId });
    } else {
      report.assertions.push({ name: `j04-patient-preview-${label}-source-identities-match-literal`, status: 'not-proven', expected: expectedSourceIdentities, actual: trace, detail: 'Receipt-bound cell trace did not return complete source identities.' });
    }
    return { ...preview, visible, state, receiptId, actual: literal };
  };
  const previewNow = async (label, expected, expectedContributorIDs = []) => {
    const requestStart = network.length;
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Preview' && !button.disabled))`, 60000);
    await browserEval(cdp, `clickButton('Preview')`);
    await waitForBrowser(cdp, `document.body.innerText.includes('Preview and configure') && Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))`, 60000);
    await waitForBrowser(cdp, `(() => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      const rows = [...(table?.querySelectorAll('[role="row"]') || [])];
      const headers = [...(rows[0]?.querySelectorAll('[role="columnheader"]') || [])].map((cell) => cell.textContent.trim());
      const index = headers.findIndex((header) => header === ${JSON.stringify(label)} || header.startsWith(${JSON.stringify(`${label} (`)}));
      return index >= 0 && rows.slice(1).some((row) => row.querySelectorAll('[role="cell"]')[index]?.textContent.trim() === ${JSON.stringify(String(expected))});
    })()`, 60000);
    return readVisiblePreview(label, expected, expectedContributorIDs, requestStart);
  };
  const previewForRefusal = async (expectedCode) => {
    const firstRequestIndex = network.length;
    await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Preview' && !button.disabled))`, 60000);
    await browserEval(cdp, `clickButton('Preview')`);
    const deadline = Date.now() + 60000;
    let response;
    while (Date.now() < deadline) {
      await Promise.allSettled([...pendingNetworkBodies]);
      response = network.slice(firstRequestIndex).reverse().find((item) => item.path.endsWith('/preview') && item.responseJSON);
      if (response) break;
      await sleep(150);
    }
    if (!response) throw new Error('J04 Patient unsupported-unit Preview returned no captured server response');
    const findCode = (value) => {
      if (!value || typeof value !== 'object') return undefined;
      if (typeof value.code === 'string') return value.code;
      for (const child of Object.values(value)) {
        const found = findCode(child);
        if (found) return found;
      }
      return undefined;
    };
    const alertText = await evaluate(cdp, `(() => [...document.querySelectorAll('[role="alert"]')].filter((item) => item.getClientRects().length > 0).map((item) => item.innerText.trim()).join(' | '))()`);
    const refusal = {
      requestId: response.requestId,
      status: response.status,
      code: findCode(response.responseJSON),
      body: response.responseJSON,
      visibleAlert: alertText,
    };
    journey.refusalTable.preview = refusal;
    await waitForBrowser(cdp, `[...document.querySelectorAll('[role="alert"]')].some((item) => item.textContent.includes(${JSON.stringify(expectedCode)}))`, 15000);
    refusal.visibleAlert = await evaluate(cdp, `(() => [...document.querySelectorAll('[role="alert"]')].filter((item) => item.getClientRects().length > 0).map((item) => item.innerText.trim()).join(' | '))()`);
    recordLiteral('j04-unsupported-unit-preview-returns-exact-refusal-code', expectedCode, refusal.code, { requestId: refusal.requestId, httpStatus: refusal.status });
    recordLiteral('j04-unsupported-unit-refusal-is-visible-in-builder', true, alertText.includes(expectedCode), { visibleAlert: alertText, requestId: refusal.requestId });
    await captureDOM('patient-unsupported-unit-refusal');
    return refusal;
  };
  const setSelectValue = async (label, value) => browserEval(cdp, `(() => {
    const select = [...document.querySelectorAll('select[aria-label]')].find((item) => item.getAttribute('aria-label') === ${JSON.stringify(label)});
    const option = [...(select?.options || [])].find((item) => item.value === ${JSON.stringify(value)});
    if (!select || !option || option.disabled) throw new Error('J04 Patient visible option is unavailable: ' + ${JSON.stringify(label)} + '=' + ${JSON.stringify(value)});
    Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set?.call(select, option.value);
    select.dispatchEvent(new Event('input', { bubbles: true }));
    select.dispatchEvent(new Event('change', { bubbles: true }));
    return norm(option.textContent);
  })()`);
  const setAccessibleSelect = async (label, value) => browserEval(cdp, `(() => {
    const select = [...document.querySelectorAll('select[aria-label]')].find((item) => item.getAttribute('aria-label') === ${JSON.stringify(label)});
    const option = [...(select?.options || [])].find((item) => item.value === ${JSON.stringify(value)});
    if (!select || !option || option.disabled) throw new Error('J04 Patient accessible choice is unavailable: ' + ${JSON.stringify(label)} + '=' + ${JSON.stringify(value)});
    Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set?.call(select, option.value);
    select.dispatchEvent(new Event('input', { bubbles: true }));
    select.dispatchEvent(new Event('change', { bubbles: true }));
    return norm(option.textContent);
  })()`);
  const setCheckboxByLabel = async (labelNeedle, checked) => browserEval(cdp, `(() => {
    const needle = norm(${JSON.stringify(labelNeedle)}).toLowerCase();
    const input = [...document.querySelectorAll('input[type="checkbox"]')].find((item) => norm(item.closest('label')?.innerText).toLowerCase().includes(needle));
    if (!input || input.disabled) throw new Error('J04 Patient visible checkbox is unavailable: ' + ${JSON.stringify(labelNeedle)});
    if (input.checked !== ${JSON.stringify(checked)}) input.click();
    return input.checked;
  })()`);
  const applyCmNormalization = async (column, targetOutputId = outputId, capability = journey.capabilities?.unitNormalization) => {
    const preset = capability?.presets?.find((item) => item.available && item.target?.code === controlPlan.unitNormalization.targetUnit);
    if (!preset) throw new Error(`J04 Patient Builder did not offer an available ${controlPlan.unitNormalization.targetUnit} unit-normalization preset`);
    await waitForBrowser(cdp, `[...document.querySelectorAll('button')].some((item) => ['Normalize units', 'Edit unit normalization'].includes(item.textContent.trim()))`, 30000);
    const button = await evaluate(cdp, `(() => [...document.querySelectorAll('button')].find((item) => ['Normalize units', 'Edit unit normalization'].includes(item.textContent.trim()))?.textContent.trim())()`);
    if (button === 'Normalize units') await browserEval(cdp, `clickButton('Normalize units')`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('select[aria-label="Unit conversion preset"]'))`, 20000);
    await setAccessibleSelect('Unit conversion preset', JSON.stringify([preset.policyId, preset.version]));
    await browserEval(cdp, `clickButton('Apply normalization')`);
    const state = await waitForState((value) => {
      const saved = value.workspace?.documents?.find((item) => item.output?.id === targetOutputId)?.columns?.find((item) => item.column === column.column);
      return saved?.source?.kind === 'aggregate' && saved.source.aggregate.unitNormalization?.policyId === preset.policyId;
    }, `${column.label} ${controlPlan.unitNormalization.targetUnit} normalization`);
    const saved = state.workspace.documents.find((item) => item.output?.id === targetOutputId).columns.find((item) => item.column === column.column);
    return { preset, saved };
  };
  const setInputValue = async (label, value) => browserEval(cdp, `(() => {
    const input = [...document.querySelectorAll('input[aria-label]')].find((item) => item.getAttribute('aria-label') === ${JSON.stringify(label)});
    if (!input || input.disabled) throw new Error('J04 Patient visible input is unavailable: ' + ${JSON.stringify(label)});
    const setter = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(input), 'value')?.set;
    setter?.call(input, ${JSON.stringify(value)});
    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.dispatchEvent(new Event('change', { bubbles: true }));
    return input.value;
  })()`);
  const applyContributorWindow = async (column, operation, direction) => {
    const stateBefore = await fetchBuilderState(target, explorerId);
    const current = stateBefore.workspace?.documents?.find((item) => item.output?.id === outputId)?.columns?.find((item) => item.column === column.column);
    if (current?.source?.kind !== 'aggregate' || current.source.aggregate.operation !== operation) {
      throw new Error(`J04 date-window editor requires saved ${operation} source for ${column.label}`);
    }
    const aggregate = current.source.aggregate;
    await browserEval(cdp, `clickButton(${JSON.stringify(aggregate.contributorWindow ? 'Edit date window' : 'Add date window')})`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('select[aria-label="Record date"]')) && Boolean(document.querySelector('select[aria-label="Compare with row date"]')) && Boolean(document.querySelector('input[aria-label="Look back days"]'))`, 20000);
    const windowPlan = controlPlan.contributorWindow;
    await setAccessibleSelect('Record date', windowPlan.recordDatePath);
    await setAccessibleSelect('Compare with row date', windowPlan.anchorPath);
    await setInputValue('Look back days', String(windowPlan.lookbackDays));
    await setCheckboxByLabel('Include start boundary', true);
    await setCheckboxByLabel('Include end boundary', false);
    if (direction) {
      await setAccessibleSelect('Date selection direction', direction);
      await setAccessibleSelect('Equal date handling', windowPlan.tiePolicy);
    }
    const expectedWindow = {
      timestampPath: windowPlan.recordDatePath,
      anchorPath: windowPlan.anchorPath,
      lowerOffsetSeconds: -windowPlan.lookbackDays * 86_400,
      upperOffsetSeconds: 0,
      lowerInclusive: true,
      upperInclusive: false,
      precision: 'INSTANT',
    };
    const applyLabel = direction ? 'Apply date selection' : 'Apply date window';
    await browserEval(cdp, `clickButton(${JSON.stringify(applyLabel)})`);
    const persisted = await waitForState((value) => {
      const saved = value.workspace?.documents?.find((item) => item.output?.id === outputId)?.columns?.find((item) => item.column === column.column);
      if (saved?.source?.kind !== 'aggregate' || saved.source.aggregate.operation !== operation
        || !j04ExactEqual(saved.source.aggregate.contributorWindow, expectedWindow)) return false;
      if (!direction) return !saved.source.aggregate.ordering;
      return j04ExactEqual(saved.source.aggregate.ordering, {
        timestampPath: windowPlan.recordDatePath,
        direction,
        tiePolicy: windowPlan.tiePolicy,
      });
    }, `${operation} contributor window`);
    const saved = persisted.workspace.documents.find((item) => item.output?.id === outputId).columns.find((item) => item.column === column.column);
    return { saved, expectedWindow, ordering: direction ? saved.source.aggregate.ordering : undefined };
  };
  const applyFirstOrderedSelection = async (column, direction) => {
    const editingSavedSelection = column.source?.kind === 'aggregate'
      && column.source.aggregate.operation === 'FIRST_ORDERED';
    await setSelectValue(`Across related Observation records for ${column.label}`, 'FIRST_ORDERED');
    await waitForBrowser(cdp, `Boolean(document.querySelector('select[aria-label="Record date"]')) && Boolean(document.querySelector('select[aria-label="Compare with row date"]')) && Boolean(document.querySelector('input[aria-label="Look back days"]'))`, 20000);
    const windowPlan = controlPlan.contributorWindow;
    if (!editingSavedSelection) {
      await setAccessibleSelect('Record date', windowPlan.recordDatePath);
      await setAccessibleSelect('Compare with row date', windowPlan.anchorPath);
      await setInputValue('Look back days', String(windowPlan.lookbackDays));
      await setCheckboxByLabel('Include start boundary', true);
      await setCheckboxByLabel('Include end boundary', false);
    }
    const chooseNativeSelectValue = async (label, value, referenceName) => {
      await browserEval(cdp, `(() => {
        const select = [...document.querySelectorAll('select[aria-label]')]
          .find((item) => item.getAttribute('aria-label') === ${JSON.stringify(label)});
        if (!select || select.disabled) throw new Error('J04 Patient native select is unavailable: ' + ${JSON.stringify(label)});
        window[${JSON.stringify(referenceName)}] = select;
        Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set?.call(select, ${JSON.stringify(value)});
        select.dispatchEvent(new Event('input', { bubbles: true }));
        select.dispatchEvent(new Event('change', { bubbles: true }));
      })()`);
      await evaluate(cdp, `new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)))`);
    };
    const currentOrdering = column.source?.kind === 'aggregate' && column.source.aggregate.operation === 'FIRST_ORDERED'
      ? column.source.aggregate.ordering
      : undefined;
    const currentDirection = currentOrdering?.direction ?? 'DESC';
    const currentTiePolicy = currentOrdering?.tiePolicy ?? 'REQUIRE_UNIQUE';
    if (currentDirection !== direction) {
      await chooseNativeSelectValue('Date selection direction', direction, '__loomJ04TemporalDirectionSelect');
    }
    if (currentTiePolicy !== windowPlan.tiePolicy) {
      await chooseNativeSelectValue('Equal date handling', windowPlan.tiePolicy, '__loomJ04TemporalTieSelect');
    }
    const temporalSelection = await evaluate(cdp, `(() => {
      const directionSelect = document.querySelector('select[aria-label="Date selection direction"]');
      const tieSelect = document.querySelector('select[aria-label="Equal date handling"]');
      const summary = directionSelect?.closest('fieldset')?.innerText || '';
      const savedDirection = window.__loomJ04TemporalDirectionSelect;
      return {
        direction: directionSelect?.value,
        tiePolicy: tieSelect?.value,
        summary,
        savedDirectionConnected: savedDirection?.isConnected ?? null,
        savedDirectionIsCurrent: savedDirection ? savedDirection === directionSelect : null,
      };
    })()`);
    const expectedSummary = `${direction === 'DESC' ? 'Latest' : 'Earliest'} ${column.source?.kind === 'aggregate' ? column.source.aggregate.path : column.label}`;
    if (temporalSelection.direction !== direction || temporalSelection.tiePolicy !== windowPlan.tiePolicy
      || !temporalSelection.summary.includes(expectedSummary)) {
      throw new Error(`J04 Patient temporal controls did not reach React state before Apply: ${JSON.stringify({ expected: { direction, tiePolicy: windowPlan.tiePolicy, summary: expectedSummary }, actual: temporalSelection })}`);
    }
    const expectedWindow = {
      timestampPath: windowPlan.recordDatePath,
      anchorPath: windowPlan.anchorPath,
      lowerOffsetSeconds: -windowPlan.lookbackDays * 86_400,
      upperOffsetSeconds: 0,
      lowerInclusive: true,
      upperInclusive: false,
      precision: 'INSTANT',
    };
    await browserEval(cdp, `(() => {
      const directionSelect = document.querySelector('select[aria-label="Date selection direction"]');
      const fieldset = directionSelect?.closest('fieldset');
      const button = [...(fieldset?.querySelectorAll('button') || [])]
        .find((item) => norm(item.textContent) === 'Apply date selection');
      if (!button || button.disabled) throw new Error('J04 Patient scoped Apply date selection is unavailable');
      button.click();
    })()`);
    const persisted = await waitForState((value) => {
      const saved = value.workspace?.documents?.find((item) => item.output?.id === outputId)?.columns?.find((item) => item.column === column.column);
      return saved?.source?.kind === 'aggregate' && saved.source.aggregate.operation === 'FIRST_ORDERED'
        && j04ExactEqual(saved.source.aggregate.contributorWindow, expectedWindow)
        && j04ExactEqual(saved.source.aggregate.ordering, {
          timestampPath: windowPlan.recordDatePath,
          direction,
          tiePolicy: windowPlan.tiePolicy,
        });
    }, 'FIRST_ORDERED value selection');
    const saved = persisted.workspace.documents.find((item) => item.output?.id === outputId).columns.find((item) => item.column === column.column);
    return { saved, expectedWindow, ordering: saved.source.aggregate.ordering };
  };

  try {
    browser = await launchBrowser(downloadDirectory, { promptText: 'J04 unsupported-unit refusal' });
    cdp = browser.cdp;
    cdp.on('Network.requestWillBeSent', (event) => {
      if (!event.request.url.includes('/authoring/v2/')) return;
      const path = new URL(event.request.url).pathname;
      network.push({ requestId: event.requestId, path, method: event.request.method,
        requestJSON: event.request.postData ? (() => { try { return JSON.parse(event.request.postData); } catch { return undefined; } })() : undefined });
    });
    cdp.on('Network.responseReceived', (event) => {
      const item = network.find((candidate) => candidate.requestId === event.requestId);
      if (item) {
        item.status = event.response.status;
        item.mimeType = event.response.mimeType;
      }
    });
    cdp.on('Network.loadingFinished', (event) => {
      const item = network.find((candidate) => candidate.requestId === event.requestId);
      if (!item || !/\/(preview|compile)$/.test(item.path) || item.status < 100) return;
      const capture = (async () => {
        try {
          const body = await cdp.send('Network.getResponseBody', { requestId: item.requestId });
          const raw = body.base64Encoded ? Buffer.from(body.body, 'base64').toString('utf8') : body.body;
          item.responseJSON = JSON.parse(raw);
        } catch (error) {
          item.responseBodyError = String(error);
        }
      })().finally(() => pendingNetworkBodies.delete(capture));
      pendingNetworkBodies.add(capture);
    });

    await navigate(cdp, entryTarget.uiUrl);
    const bootstrapId = report.target.bootstrapExplorerId;
    if (!bootstrapId) throw new Error('J04 Patient journey requires the fresh fixture bootstrap Explorer');
    await navigate(cdp, `${target.uiUrl}/?project=${encodeURIComponent(target.fixtureProject)}&explorer=${encodeURIComponent(bootstrapId)}&mode=builder`);
    await waitForBrowser(cdp, `Boolean(document.querySelector('select[aria-label="Explorer"]'))`, 60000);
    await captureDOM('before-new-explorer');
    await action('create-blank-explorer-through-visible-controls', async () => {
      await browserEval(cdp, `clickText('summary', 'New explorer')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('#new-explorer-name'))`, 10000);
      await browserEval(cdp, `setInput('new-explorer-name', 'J04 Patient operators')`);
      await browserEval(cdp, `clickButton('Create blank')`);
      await waitForBrowser(cdp, `new URL(location.href).searchParams.get('explorer') && new URL(location.href).searchParams.get('explorer') !== ${JSON.stringify(bootstrapId)}`, 30000);
      explorerId = await evaluate(cdp, `new URL(location.href).searchParams.get('explorer')`);
      if (!explorerId) throw new Error('visible blank Explorer creation did not select a new Explorer');
    });

    const selectionFixture = await seedJ04PatientSelectionRevision(target, explorerId, fixture.contract);
    journey.selectionRevision = {
      id: selectionFixture.selection.id,
      membershipDigest: selectionFixture.selection.membershipDigest,
      memberCount: selectionFixture.selection.memberCount,
      members: selectionFixture.members,
      snapshotToken: selectionFixture.snapshotToken,
    };
    recordAssertion(report, 'j04-patient-selection-revision-is-exactly-one-fixture-resource', [
      { project: canonicalProjectID(target.fixtureProject), generation: target.fixtureGeneration, resourceType: 'Patient', id: 'j04-patient-001' },
    ], selectionFixture.members.map((member) => ({ ...member, project: canonicalProjectID(member.project) })));

    await action('create-patient-table-and-attach-selection-in-builder', async () => {
      const builderURL = `${target.uiUrl}/?project=${encodeURIComponent(target.fixtureProject)}&explorer=${encodeURIComponent(explorerId)}&selection=${encodeURIComponent(selectionFixture.selection.id)}&mode=builder`;
      await navigate(cdp, builderURL);
      await waitForBrowser(cdp, `document.body.innerText.includes('Create your first table')`, 60000);
      await browserEval(cdp, `setInput('first-table-name', 'J04 Patient metrics')`);
      await browserEval(cdp, `clickButton('Create table')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('button[aria-label="Choose Patient rows"]'))`, 30000);
      await browserEval(cdp, `clickButton('Choose Patient rows')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('[aria-label="Starting collection"]'))`, 30000);
      await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('[aria-label="Starting collection"] button')].find((button) => button.textContent.trim() === 'Use selected resources' && !button.disabled))`, 60000);
      await captureDOM('patient-table-before-selection-attachment');
      await browserEval(cdp, `clickButton('Use selected resources')`);
      const state = await waitForState((value) => {
        const doc = value.workspace?.documents?.find((item) => item.rootResourceType === 'Patient');
        return doc?.population?.selectionRevisionId === selectionFixture.selection.id;
      }, 'visible starting-collection selection attachment');
      const document = state.workspace.documents.find((item) => item.rootResourceType === 'Patient');
      outputId = document.output.id;
      journey.outputId = outputId;
      journey.rowDefinition = { rootResourceType: document.rootResourceType, selectionRevisionId: document.population.selectionRevisionId, route: document.population.route };
      recordAssertion(report, 'j04-patient-table-attaches-exact-selection-through-visible-starting-collection', selectionFixture.selection.id, document.population.selectionRevisionId);
      await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('[aria-label="Starting collection"] button')].find((button) => button.textContent.trim() === 'Use all authorized rows' && !button.disabled))`, 30000);
    });

    let state = await fetchBuilderState(target, explorerId);
    const document = state.workspace.documents.find((item) => item.output?.id === outputId);
    const root = state.catalog.nodes.find((node) => node.resourceType === 'Patient' && node.rowRootEligible);
    const observationNodes = state.catalog.nodes.filter((node) => node.resourceType === 'Observation');
    if (!document || !root || observationNodes.length === 0) throw new Error('J04 Patient graph did not expose Patient and Observation schema nodes');
    const relatedEdges = state.catalog.edges.filter((edge) => edge.fromNodeId === root.nodeId
      && observationNodes.some((node) => node.nodeId === edge.toNodeId)
      && /subject/i.test(edge.label));
    if (relatedEdges.length !== 1) throw new Error(`J04 Patient graph needs one subject-to-Observation edge, found ${relatedEdges.length}`);
    const relatedEdge = relatedEdges[0];
    const observationNode = state.catalog.nodes.find((node) => node.nodeId === relatedEdge.toNodeId);
    journey.sourceRoute = { edgeId: relatedEdge.edgeId, relationship: relatedEdge.label, from: 'Patient', to: 'Observation' };
    await action('add-observation-source-through-advanced-graph', async () => {
      await browserEval(cdp, `clickButton('Advanced graph')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('.react-flow__node'))`, 30000);
      const patientIDCandidate = state.catalog.candidates.find((candidate) => candidate.nodeId === root.nodeId && candidatePath(candidate) === 'id');
      if (!patientIDCandidate) throw new Error('J04 Patient graph did not expose its source id candidate');
      await browserEval(cdp, `setInput('Search columns', 'id')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Add id to table"]'))`, 30000);
      await browserEval(cdp, `clickCandidate('id', 'to table')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Display name for configured id"]'))`, 30000);
      await browserEval(cdp, `setInput('Display name for configured id', 'Patient ID')`);
      await browserEval(cdp, `inputByLabel('Display name for configured id').focus()`);
      await cdp.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Enter', code: 'Enter' });
      await cdp.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Enter', code: 'Enter' });
      state = await waitForState((value) => value.workspace?.documents?.find((item) => item.output?.id === outputId)?.columns?.some((column) => column.label === 'Patient ID'), 'Patient ID source column');
      const rootOccurrence = state.workspace.documents.find((item) => item.output?.id === outputId).route.occurrenceId;
      await browserEval(cdp, `(() => { const button = [...document.querySelectorAll('nav[aria-label="Current traversal"] button[data-occurrence-id]')].find((item) => item.dataset.occurrenceId === ${JSON.stringify(rootOccurrence)}); if (!button) throw new Error('J04 Patient root occurrence is not visible'); button.click(); })()`);
      await browserEval(cdp, `(() => {
        const node = [...document.querySelectorAll('.react-flow__node')].find((item) => item.dataset.id === ${JSON.stringify(observationNode.nodeId)});
        if (!node) throw new Error('J04 Patient Observation node is not visible in Advanced graph');
        node.scrollIntoView({ block: 'center' }); node.click();
      })()`);
      await waitForState((value) => Boolean(routePathForEdgeIDs(
        value.workspace?.documents?.find((item) => item.output?.id === outputId)?.route,
        [relatedEdge.edgeId],
      )), 'Patient-to-Observation source route');
      state = await fetchBuilderState(target, explorerId);
      const route = routePathForEdgeIDs(state.workspace.documents.find((item) => item.output?.id === outputId).route, [relatedEdge.edgeId]);
      const occurrenceId = route.at(-1).occurrenceId;
      await waitForBrowser(cdp, `[...document.querySelectorAll('nav[aria-label="Current traversal"] button[data-occurrence-id]')].some((button) => button.dataset.occurrenceId === ${JSON.stringify(occurrenceId)})`, 30000);
      await browserEval(cdp, `(() => { const button = [...document.querySelectorAll('nav[aria-label="Current traversal"] button[data-occurrence-id]')].find((item) => item.dataset.occurrenceId === ${JSON.stringify(occurrenceId)}); if (!button) throw new Error('J04 Observation traversal occurrence is not visible'); button.click(); })()`);
      journey.sourceRoute.occurrenceId = occurrenceId;
      await captureDOM('patient-observation-route');
    });

    const metricPath = fixture.contract.aggregateScope.valuePath;
    const metricCandidate = state.catalog.candidates.find((candidate) => candidate.nodeId === observationNode.nodeId && candidatePath(candidate) === metricPath);
    const categoryCandidate = state.catalog.candidates.find((candidate) => candidate.nodeId === observationNode.nodeId && candidatePath(candidate) === fixture.contract.aggregateScope.categoryPath);
    if (!metricCandidate || !categoryCandidate) throw new Error('J04 Patient Observation catalog did not expose metric and category candidates');
    await action('add-patient-metric-through-visible-column-selector', async () => {
      await browserEval(cdp, `setInput('Search columns', ${JSON.stringify(metricPath)})`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Add ${metricPath} to table"]'))`, 30000);
      await browserEval(cdp, `clickCandidate(${JSON.stringify(metricPath)}, 'to table')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label^="Display name for configured "]'))`, 30000);
      state = await waitForState((value) => value.workspace?.documents?.find((item) => item.output?.id === outputId)?.columns?.some((column) => column.source?.field?.path === metricPath), 'configured Patient metric column');
      journey.metricColumn = state.workspace.documents.find((item) => item.output?.id === outputId).columns.find((column) => column.source?.field?.path === metricPath);
      const candidateCapability = state.catalog.candidates.find((candidate) => candidate.candidateId === metricCandidate.candidateId);
      journey.capabilities = {
        metricCandidateId: metricCandidate.candidateId,
        categoryCandidateId: categoryCandidate.candidateId,
        aggregateOperations: candidateCapability?.aggregateOperations,
        temporalReduction: candidateCapability?.transformations?.temporalReduction,
        unitNormalization: candidateCapability?.transformations?.unitNormalization,
      };
      await captureDOM('patient-metric-and-category-filter');
    });

    const recodingContract = controlPlan.recoding;
    const recodingCandidate = state.catalog.candidates.find((candidate) => candidate.nodeId === observationNode.nodeId
      && candidatePath(candidate) === recodingContract.sourcePath);
    if (!recodingCandidate) throw new Error(`J04 Patient Observation catalog did not expose recoding candidate ${recodingContract.sourcePath}`);
    const recodingCapability = recodingCandidate.valueTransformations?.exactCategoryRecode
      ?? recodingCandidate.valueTransformations?.codedValueRecoding;
    if (!recodingCapability?.available) throw new Error(`J04 Patient recoding candidate is unavailable: ${recodingCapability?.reason ?? recodingContract.sourcePath}`);
    let recodingColumn;
    await action('configure-exact-recoding-through-visible-column-controls', async () => {
      await browserEval(cdp, `setInput('Search columns', ${JSON.stringify(recodingContract.sourcePath)})`);
      await waitForBrowser(cdp, `Boolean(document.querySelector(${JSON.stringify(`input[aria-label="Add ${recodingContract.sourcePath} to table"]`)}))`, 30000);
      await browserEval(cdp, `clickCandidate(${JSON.stringify(recodingContract.sourcePath)}, 'to table')`);
      state = await waitForState((value) => value.workspace?.documents?.find((item) => item.output?.id === outputId)?.columns?.some((column) => column.source?.field?.path === recodingContract.sourcePath), 'configured exact-recoding column');
      recodingColumn = state.workspace.documents.find((item) => item.output?.id === outputId).columns.find((column) => column.source?.field?.path === recodingContract.sourcePath);
      journey.recoding = {
        sourcePath: recodingContract.sourcePath,
        candidateId: recodingCandidate.candidateId,
        occurrenceId: recodingColumn.occurrenceId,
        capability: recodingCapability,
        expectedCases: recodingContract.cases,
      };
      await waitForBrowser(cdp, `Boolean(document.querySelector(${JSON.stringify(`select[aria-label="Repeated values for ${recodingColumn.label}"]`)}))`, 30000);
      await setAccessibleSelect(`Repeated values for ${recodingColumn.label}`, 'FIRST');
      state = await waitForState((value) => {
        const column = value.workspace?.documents?.find((item) => item.output?.id === outputId)?.columns?.find((item) => item.column === recodingColumn.column);
        return column?.source?.kind === 'field' && column.source.field.projectionMode === 'FIRST';
      }, 'scalar exact-recoding source');
      recodingColumn = state.workspace.documents.find((item) => item.output?.id === outputId).columns.find((item) => item.column === recodingColumn.column);
      await waitForBrowser(cdp, `[...document.querySelectorAll('summary')].some((item) => item.textContent.trim() === 'Recode exact category values')`, 30000);
      await browserEval(cdp, `clickText('summary', 'Recode exact category values')`);
      const mappings = Object.entries(recodingContract.mapping);
      for (let index = 0; index < mappings.length; index += 1) {
        await browserEval(cdp, `clickButton('Add mapping')`);
        const [from, to] = mappings[index];
        await setInputValue(`Recorded category ${index + 1} for ${recodingColumn.label}`, from);
        await setInputValue(`Replacement value ${index + 1} for ${recodingColumn.label}`, to);
      }
      await setAccessibleSelect(`Unmapped value policy for ${recodingColumn.label}`, recodingContract.unknownPolicy);
      await browserEval(cdp, `clickButton('Save recoding')`);
      state = await waitForState((value) => {
        const column = value.workspace?.documents?.find((item) => item.output?.id === outputId)?.columns?.find((item) => item.column === recodingColumn.column);
        return column?.valueTransformation?.kind === 'EXACT_CATEGORY_RECODE';
      }, 'saved exact-category recoding');
      recodingColumn = state.workspace.documents.find((item) => item.output?.id === outputId).columns.find((item) => item.column === recodingColumn.column);
      const savedRecode = recodingColumn.valueTransformation?.exactCategoryRecode;
      recordLiteral('j04-patient-exact-recoding-definition-is-exact', {
        mappings: Object.entries(recodingContract.mapping).map(([from, to]) => ({ from, to })),
        unknownPolicy: recodingContract.unknownPolicy,
      }, savedRecode);
      await captureDOM('patient-exact-recoding-definition');
    });

    const recodingOperationControl = `Across related Observation records for ${recodingColumn.label}`;
    await action('preview-case-sensitive-recoding-through-visible-controls', async () => {
      await setSelectValue(recodingOperationControl, 'MIN');
      state = await waitForState((value) => {
        const column = value.workspace?.documents?.find((item) => item.output?.id === outputId)?.columns?.find((item) => item.column === recodingColumn.column);
        return column?.source?.kind === 'aggregate' && column.source.aggregate.operation === 'MIN';
      }, 'recoding MIN source');
      recodingColumn = state.workspace.documents.find((item) => item.output?.id === outputId).columns.find((item) => item.column === recodingColumn.column);
      const applyCategory = async (rawValue, expectedValue, sourceRecordId) => {
        const live = await fetchBuilderState(target, explorerId);
        recodingColumn = live.workspace.documents.find((item) => item.output?.id === outputId).columns.find((item) => item.column === recodingColumn.column);
        if (!recodingColumn.contributor) {
          await browserEval(cdp, `(() => {
            const select = inputByLabel('Contributors for ' + ${JSON.stringify(recodingColumn.label)});
            const option = [...(select?.options || [])].find((item) => item.value === ${JSON.stringify(recodingCandidate.candidateId)});
            if (!select || !option) throw new Error('J04 recoding field is unavailable as its own contributor filter');
            Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set?.call(select, option.value);
            select.dispatchEvent(new Event('input', { bubbles: true }));
            select.dispatchEvent(new Event('change', { bubbles: true }));
          })()`);
          await setAccessibleSelect(`Contributor condition for ${recodingColumn.label}`, 'EQUALS');
        }
        await setInputValue(`Contributor value for ${recodingColumn.label}`, rawValue);
        await browserEval(cdp, `clickButton('Apply condition')`);
        state = await waitForState((value) => value.workspace?.documents?.find((item) => item.output?.id === outputId)?.columns?.find((item) => item.column === recodingColumn.column)?.contributor?.value?.code?.code === rawValue
          || value.workspace?.documents?.find((item) => item.output?.id === outputId)?.columns?.find((item) => item.column === recodingColumn.column)?.contributor?.value?.string === rawValue, `recoding contributor ${rawValue}`);
        const preview = await previewNow(recodingColumn.label, expectedValue, [sourceRecordId]);
        journey.recoding.cases ??= [];
        journey.recoding.cases.push({ input: rawValue, expected: expectedValue, sourceIdentity: `Observation/${sourceRecordId}`, previewReceiptId: preview.receiptId });
      };
      await applyCategory('A', 'Positive', 'j04-measure-001');
      await applyCategory('a', 'a', 'j04-measure-002');
      await applyCategory('UNKNOWN', 'UNKNOWN', 'j04-measure-003');
      recordLiteral('j04-exact-recoding-honors-case-and-unknown-policy', recodingContract.cases.map(({ input, expected }) => ({ input, expected })),
        journey.recoding.cases.map(({ input, expected }) => ({ input, expected })));
    });

    const metricLabel = journey.metricColumn.label;
    const operationControl = `Across related Observation records for ${metricLabel}`;
    await browserEval(cdp, `setInput('Search columns', ${JSON.stringify(metricPath)})`);
    await waitForBrowser(cdp, `Boolean(document.querySelector(${JSON.stringify(`select[aria-label="${operationControl}"]`)}))`, 30000);
    const observedOperations = [];
    for (const item of controlPlan.aggregateOperations) {
      await action(`preview-${item.operation.toLowerCase()}-on-patient-row`, async () => {
        const beforeState = await fetchBuilderState(target, explorerId);
        const currentColumn = beforeState.workspace.documents.find((candidate) => candidate.output?.id === outputId).columns.find((column) => column.column === journey.metricColumn.column);
        if (currentColumn?.source?.kind === 'field') {
          await browserEval(cdp, `(() => {
            const details = [...document.querySelectorAll('details')].find((item) => norm(item.innerText).includes('Time and units'));
            const select = inputByLabel('Across related Observation records for ' + ${JSON.stringify(metricLabel)});
            if (!select) throw new Error('J04 aggregate operation selector is missing');
            const option = [...select.options].find((candidate) => candidate.value === ${JSON.stringify(item.operation)});
            if (!option || option.disabled) throw new Error('J04 aggregate operation is not offered: ' + ${JSON.stringify(item.operation)} + '; options=' + [...select.options].map((candidate) => norm(candidate.textContent)).join(' | '));
            Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set?.call(select, option.value);
            select.dispatchEvent(new Event('change', { bubbles: true }));
          })()`);
        } else {
          await setSelectValue(operationControl, item.operation);
        }
        let persisted = await waitForState((value) => {
          const column = value.workspace.documents.find((candidate) => candidate.output?.id === outputId)?.columns?.find((candidate) => candidate.column === journey.metricColumn.column);
          return column?.source?.kind === 'aggregate' && column.source.aggregate.operation === item.operation;
        }, `${item.operation} persisted`);
        let savedColumn = persisted.workspace.documents.find((candidate) => candidate.output?.id === outputId).columns.find((column) => column.column === journey.metricColumn.column);
        if (item.operation === 'COUNT' && !savedColumn.contributor) {
          const categoryCandidateID = journey.capabilities.categoryCandidateId;
          await browserEval(cdp, `(() => {
            const select = inputByLabel('Contributors for ' + ${JSON.stringify(savedColumn.label)});
            const option = [...(select?.options || [])].find((item) => item.value === ${JSON.stringify(categoryCandidateID)});
            if (!select || !option) throw new Error('J04 category candidate is absent from the visible contributor filter');
            Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set?.call(select, option.value);
            select.dispatchEvent(new Event('input', { bubbles: true }));
            select.dispatchEvent(new Event('change', { bubbles: true }));
          })()`);
          await setAccessibleSelect(`Contributor condition for ${savedColumn.label}`, 'EQUALS');
          await setInputValue(`Contributor value for ${savedColumn.label}`, fixture.contract.aggregateScope.categoryCode);
          await browserEval(cdp, `clickButton('Apply condition')`);
          persisted = await waitForState((value) => value.workspace.documents.find((candidate) => candidate.output?.id === outputId)?.columns?.find((column) => column.column === journey.metricColumn.column)?.contributor?.operator === 'EQUALS', 'exact measure-code contributor condition');
          savedColumn = persisted.workspace.documents.find((candidate) => candidate.output?.id === outputId).columns.find((column) => column.column === journey.metricColumn.column);
          recordLiteral('j04-patient-metric-contributor-condition-is-exact', {
            candidateId: categoryCandidateID,
            operator: 'EQUALS',
            value: fixture.contract.aggregateScope.categoryCode,
          }, {
            candidateId: savedColumn.contributor?.candidateId,
            operator: savedColumn.contributor?.operator,
            value: savedColumn.contributor?.value?.string ?? savedColumn.contributor?.value?.code?.code,
          });
        }
        const windowed = await applyContributorWindow(savedColumn, item.operation);
        savedColumn = windowed.saved;
        let normalization;
        if (['MIN', 'MAX', 'MEAN', 'SUM'].includes(item.operation)) {
          normalization = await applyCmNormalization(savedColumn);
          savedColumn = normalization.saved;
        }
        journey.metricColumn = savedColumn;
        const expectedContributorIDs = j04PatientOperatorSourceIDs(fixture.contract, item.operation);
        const preview = await previewNow(savedColumn.label, item.expected, expectedContributorIDs);
        const resultUnit = preview.resultUnit
          ?? preview.state.receipt?.outputs?.find((entry) => entry.outputId === outputId)?.columns?.find((entry) => entry.column === savedColumn.column)?.resultUnit
          ?? preview.state.preview?.columns?.find((entry) => entry.column === savedColumn.column)?.resultUnit
          ?? null;
        recordLiteral(`j04-patient-${item.operation.toLowerCase()}-contributor-window-is-exact`, {
          timestampPath: windowed.expectedWindow.timestampPath,
          anchorPath: windowed.expectedWindow.anchorPath,
          lowerOffsetSeconds: windowed.expectedWindow.lowerOffsetSeconds,
          upperOffsetSeconds: windowed.expectedWindow.upperOffsetSeconds,
          lowerInclusive: windowed.expectedWindow.lowerInclusive,
          upperInclusive: windowed.expectedWindow.upperInclusive,
          precision: windowed.expectedWindow.precision,
        }, savedColumn.source.aggregate.contributorWindow, { receiptId: preview.receiptId });
        recordLiteral(`j04-patient-${item.operation.toLowerCase()}-excludes-out-of-window-record`, false,
          preview.sourceIdentities.includes('Observation/j04-window-excluded'), {
            receiptId: preview.receiptId,
            sourceIdentities: preview.sourceIdentities,
          });
        if (normalization) {
          recordLiteral(`j04-patient-${item.operation.toLowerCase()}-result-unit-is-centimeters`, {
            system: controlPlan.unitNormalization.targetSystem,
            code: controlPlan.unitNormalization.targetUnit,
          }, resultUnit ? { system: resultUnit.system, code: resultUnit.code } : null, { receiptId: preview.receiptId });
        }
        observedOperations.push({
          operation: item.operation,
          column: savedColumn.column,
          source: savedColumn.source,
          contributor: savedColumn.contributor,
          expectedContributorIDs,
          actual: preview.actual,
          resultUnit,
          sourceIdentities: preview.sourceIdentities,
          contributorWindow: savedColumn.source.aggregate.contributorWindow,
          ordering: savedColumn.source.aggregate.ordering,
          receiptId: preview.receiptId,
        });
      });
    }
    journey.observedOperations = observedOperations;
    recordLiteral('j04-count-exists-min-max-mean-sum-match-literal-oracle', controlPlan.aggregateOperations.map(({ operation, expected }) => ({ operation, expected })),
      observedOperations.map(({ operation, actual }) => ({ operation, expected: actual })), { receipts: observedOperations.map(({ operation, receiptId }) => ({ operation, receiptId })) });

    const normalizedSum = observedOperations.find((item) => item.operation === 'SUM');
    const normalizedMinimum = observedOperations.find((item) => item.operation === 'MIN');
    journey.normalization = {
      sourceUnitCases: fixture.contract.normalizationCases.filter((item) => item.status === 'NORMALIZED'),
      resultUnit: normalizedSum?.resultUnit ?? null,
      policy: normalizedSum?.source?.aggregate?.unitNormalization ?? null,
    };
    recordLiteral('j04-patient-normalized-sum-result-unit-is-centimeters', {
      system: controlPlan.unitNormalization.targetSystem,
      code: controlPlan.unitNormalization.targetUnit,
    }, normalizedSum?.resultUnit ? { system: normalizedSum.resultUnit.system, code: normalizedSum.resultUnit.code } : null,
    { receiptId: normalizedSum?.receiptId, sourceIdentities: normalizedSum?.sourceIdentities });
    recordLiteral('j04-patient-normalized-zero-is-retained-with-centimeter-unit', {
      value: 0, system: controlPlan.unitNormalization.targetSystem, code: controlPlan.unitNormalization.targetUnit,
    }, {
      value: normalizedMinimum?.actual,
      system: normalizedMinimum?.resultUnit?.system,
      code: normalizedMinimum?.resultUnit?.code,
    }, { receiptId: normalizedMinimum?.receiptId, sourceIdentities: normalizedMinimum?.sourceIdentities });

    const configureDateSelection = async (direction, expectedValue, expectedRecordId) => {
      const live = await fetchBuilderState(target, explorerId);
      const current = live.workspace.documents.find((item) => item.output?.id === outputId).columns.find((item) => item.column === journey.metricColumn.column);
      const windowed = await applyFirstOrderedSelection(current, direction);
      let saved = windowed.saved;
      const normalized = await applyCmNormalization(saved);
      saved = normalized.saved;
      journey.metricColumn = saved;
      const preview = await previewNow(saved.label, expectedValue, [expectedRecordId]);
      journey.windowedSelections ??= [];
      journey.windowedSelections.push({ direction, expectedRecordId, contributorWindow: saved.source.aggregate.contributorWindow, ordering: saved.source.aggregate.ordering, preview });
      recordLiteral(`j04-patient-${direction.toLowerCase()}-contributor-window-is-exact`, {
        timestampPath: controlPlan.contributorWindow.recordDatePath,
        anchorPath: controlPlan.contributorWindow.anchorPath,
        lowerOffsetSeconds: -controlPlan.contributorWindow.lookbackDays * 86_400,
        upperOffsetSeconds: 0,
        lowerInclusive: true,
        upperInclusive: false,
        precision: 'INSTANT',
      }, saved.source.aggregate.contributorWindow, { selectedRecordId: expectedRecordId, receiptId: preview.receiptId });
      recordLiteral(`j04-patient-${direction.toLowerCase()}-ordering-is-exact`, {
        timestampPath: controlPlan.contributorWindow.recordDatePath,
        direction,
        tiePolicy: controlPlan.contributorWindow.tiePolicy,
      }, saved.source.aggregate.ordering, { selectedRecordId: expectedRecordId, receiptId: preview.receiptId });
      recordLiteral(`j04-patient-${direction.toLowerCase()}-value-unit-is-centimeters`, {
        system: controlPlan.unitNormalization.targetSystem,
        code: controlPlan.unitNormalization.targetUnit,
      }, preview.resultUnit ? { system: preview.resultUnit.system, code: preview.resultUnit.code } : null, { receiptId: preview.receiptId });
    };
    await action('preview-earliest-windowed-value', () => configureDateSelection('ASC', 180, controlPlan.contributorWindow.earliestRecordId));
    await action('preview-latest-windowed-value-with-deterministic-tie', () => configureDateSelection('DESC', 180, controlPlan.contributorWindow.latestSelectedRecordId));

    await action('create-separate-visible-patient-unit-refusal-table', async () => {
      const beforeState = await fetchBuilderState(target, explorerId);
      const p1DocumentBefore = structuredClone(beforeState.workspace.documents.find((item) => item.output?.id === outputId));
      journey.refusalTable = { expected: controlPlan.refusalTable, p1OutputId: outputId, p1DocumentBefore, published: false };
      const refusalSelection = await seedJ04PatientSelectionRevision(target, explorerId, fixture.contract, [controlPlan.refusalTable.rowIdentity]);
      journey.refusalTable.selectionRevision = {
        id: refusalSelection.selection.id,
        membershipDigest: refusalSelection.selection.membershipDigest,
        memberCount: refusalSelection.selection.memberCount,
        members: refusalSelection.members,
      };
      recordLiteral('j04-refusal-selection-is-exactly-patient-002', [
        { project: canonicalProjectID(target.fixtureProject), generation: target.fixtureGeneration, resourceType: 'Patient', id: 'j04-patient-002' },
      ], refusalSelection.members);
      const refusalBuilderURL = `${target.uiUrl}/?project=${encodeURIComponent(target.fixtureProject)}&explorer=${encodeURIComponent(explorerId)}&selection=${encodeURIComponent(refusalSelection.selection.id)}&mode=builder`;
      await navigate(cdp, refusalBuilderURL);
      await cdp.send('Page.reload', { ignoreCache: true });
      await waitForBrowser(cdp, `window.location.href === ${JSON.stringify(new URL(refusalBuilderURL).href)} && document.readyState === 'complete'`, 30000);
      await waitForBrowser(cdp, `Boolean(document.querySelector('select[aria-label="Explorer"]'))`, 30000);
      await waitForBrowser(cdp, `document.querySelector('[aria-label="Starting collection"]')?.dataset.selectionRevisionId === ${JSON.stringify(refusalSelection.selection.id)}`, 60000);

      await browserEval(cdp, `(() => {
        const selector = document.querySelector('[aria-label="Table selector"]');
        if (!selector) throw new Error('J04 table selector is missing');
        selector.click();
      })()`);
      await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'New table' && button.getClientRects().length > 0))`, 15000);
      await browserEval(cdp, `clickButton('New table')`);
      const p2Title = 'J04 unsupported-unit refusal';
      await waitForBrowser(cdp, `Boolean(document.querySelector('button[aria-label="Choose Patient rows"]'))`, 30000);
      await browserEval(cdp, `clickButton('Choose Patient rows')`);
      let createdState = await waitForState((value) => value.workspace?.documents?.some((item) => item.output?.title === p2Title && item.rootResourceType === 'Patient'), 'separate Patient-002 refusal table');
      const p2Document = createdState.workspace.documents.find((item) => item.output?.title === p2Title);
      const p2OutputId = p2Document.output.id;
      journey.refusalTable.outputId = p2OutputId;
      await waitForBrowser(cdp, `Boolean(document.querySelector('[aria-label="Starting collection"]'))`, 30000);
      await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('[aria-label="Starting collection"] button')].find((button) => button.textContent.trim() === 'Use selected resources' && !button.disabled))`, 60000);
      await browserEval(cdp, `clickButton('Use selected resources')`);
      createdState = await waitForState((value) => {
        const item = value.workspace?.documents?.find((doc) => doc.output?.id === p2OutputId);
        return item?.rootResourceType === 'Patient' && item.population?.selectionRevisionId === refusalSelection.selection.id;
      }, 'visible Patient-002 starting-collection attachment');
      const attached = createdState.workspace.documents.find((item) => item.output?.id === p2OutputId);
      journey.refusalTable.rowDefinition = {
        rootResourceType: attached.rootResourceType,
        selectionRevisionId: attached.population.selectionRevisionId,
        route: attached.population.route,
      };
      recordLiteral('j04-refusal-table-attaches-exact-patient-002-selection-visibly', refusalSelection.selection.id, attached.population.selectionRevisionId);

      const patientNode = createdState.catalog.nodes.find((node) => node.resourceType === 'Patient' && node.rowRootEligible);
      const observationNodes = createdState.catalog.nodes.filter((node) => node.resourceType === 'Observation');
      const edge = createdState.catalog.edges.find((candidate) => candidate.fromNodeId === patientNode?.nodeId
        && observationNodes.some((node) => node.nodeId === candidate.toNodeId) && /subject/i.test(candidate.label));
      const observationNode = observationNodes.find((node) => node.nodeId === edge?.toNodeId);
      if (!patientNode || !observationNode || !edge) throw new Error('J04 Patient-002 graph has no Patient-to-Observation subject route');
      await browserEval(cdp, `clickButton('Advanced graph')`);
      const p2RootOccurrence = attached.route.occurrenceId;
      await browserEval(cdp, `(() => {
        const root = [...document.querySelectorAll('nav[aria-label="Current traversal"] button[data-occurrence-id]')].find((item) => item.dataset.occurrenceId === ${JSON.stringify(p2RootOccurrence)});
        if (!root) throw new Error('J04 Patient-002 root occurrence is not visible');
        root.click();
        const node = [...document.querySelectorAll('.react-flow__node')].find((item) => item.dataset.id === ${JSON.stringify(observationNode.nodeId)});
        if (!node) throw new Error('J04 Patient-002 Observation node is not visible');
        node.scrollIntoView({ block: 'center' }); node.click();
      })()`);
      await waitForState((value) => Boolean(routePathForEdgeIDs(value.workspace?.documents?.find((item) => item.output?.id === p2OutputId)?.route, [edge.edgeId])), 'Patient-002 Observation source route');
      let p2State = await fetchBuilderState(target, explorerId);
      const metricPath = fixture.contract.aggregateScope.valuePath;
      const metricCandidate = p2State.catalog.candidates.find((candidate) => candidate.nodeId === observationNode.nodeId && candidatePath(candidate) === metricPath);
      const categoryCandidate = p2State.catalog.candidates.find((candidate) => candidate.nodeId === observationNode.nodeId && candidatePath(candidate) === fixture.contract.aggregateScope.categoryPath);
      if (!metricCandidate || !categoryCandidate) throw new Error('J04 Patient-002 catalog lacks value or category source candidates');
      await browserEval(cdp, `setInput('Search columns', ${JSON.stringify(metricPath)})`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Add ${metricPath} to table"]'))`, 30000);
      await browserEval(cdp, `clickCandidate(${JSON.stringify(metricPath)}, 'to table')`);
      p2State = await waitForState((value) => value.workspace?.documents?.find((item) => item.output?.id === p2OutputId)?.columns?.some((column) => column.source?.field?.path === metricPath), 'Patient-002 unsupported-unit value column');
      let p2Column = p2State.workspace.documents.find((item) => item.output?.id === p2OutputId).columns.find((item) => item.source?.field?.path === metricPath);
      const p2Capability = p2State.catalog.candidates.find((candidate) => candidate.candidateId === metricCandidate.candidateId)?.transformations?.unitNormalization;
      journey.refusalTable.metricColumn = { id: p2Column.column, label: p2Column.label, candidateId: metricCandidate.candidateId };
      journey.refusalTable.sourceIdentity = `Observation/${controlPlan.refusalTable.sourceRecordId}`;
      journey.refusalTable.unitNormalizationCapability = p2Capability;
      await waitForBrowser(cdp, `(() => {
        const select = [...document.querySelectorAll('select[aria-label]')]
          .find((item) => item.getAttribute('aria-label') === ${JSON.stringify(`Across related Observation records for ${p2Column.label}`)});
        const option = [...(select?.options || [])].find((item) => item.value === 'SUM');
        return Boolean(option && !option.disabled);
      })()`, 30000);
      await setSelectValue(`Across related Observation records for ${p2Column.label}`, 'SUM');
      p2State = await waitForState((value) => value.workspace?.documents?.find((item) => item.output?.id === p2OutputId)?.columns?.find((column) => column.column === p2Column.column)?.source?.aggregate?.operation === 'SUM', 'Patient-002 SUM aggregate');
      p2Column = p2State.workspace.documents.find((item) => item.output?.id === p2OutputId).columns.find((item) => item.column === p2Column.column);
      await browserEval(cdp, `(() => {
        const select = inputByLabel('Contributors for ' + ${JSON.stringify(p2Column.label)});
        const option = [...(select?.options || [])].find((item) => item.value === ${JSON.stringify(categoryCandidate.candidateId)});
        if (!select || !option) throw new Error('J04 Patient-002 code contributor is not visible');
        Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set?.call(select, option.value);
        select.dispatchEvent(new Event('input', { bubbles: true })); select.dispatchEvent(new Event('change', { bubbles: true }));
      })()`);
      await waitForBrowser(cdp, `(() => {
        const select = [...document.querySelectorAll('select[aria-label]')]
          .find((item) => item.getAttribute('aria-label') === ${JSON.stringify(`Contributor condition for ${p2Column.label}`)});
        const option = [...(select?.options || [])].find((item) => item.value === 'EQUALS');
        return Boolean(option && !option.disabled);
      })()`, 30000);
      await setAccessibleSelect(`Contributor condition for ${p2Column.label}`, 'EQUALS');
      await setInputValue(`Contributor value for ${p2Column.label}`, fixture.contract.aggregateScope.categoryCode);
      await browserEval(cdp, `clickButton('Apply condition')`);
      p2State = await waitForState((value) => value.workspace?.documents?.find((item) => item.output?.id === p2OutputId)?.columns?.find((item) => item.column === p2Column.column)?.contributor?.operator === 'EQUALS', 'Patient-002 measure category filter');
      p2Column = p2State.workspace.documents.find((item) => item.output?.id === p2OutputId).columns.find((item) => item.column === p2Column.column);
      recordLiteral('j04-refusal-table-filters-to-measure-code', {
        candidateId: categoryCandidate.candidateId, operator: 'EQUALS', value: fixture.contract.aggregateScope.categoryCode,
      }, {
        candidateId: p2Column.contributor?.candidateId, operator: p2Column.contributor?.operator,
        value: p2Column.contributor?.value?.string ?? p2Column.contributor?.value?.code?.code,
      });
      const normalized = await applyCmNormalization(p2Column, p2OutputId, p2Capability);
      p2Column = normalized.saved;
      journey.refusalTable.normalization = {
        policyId: normalized.preset.policyId, version: normalized.preset.version,
        target: normalized.preset.target, resultUnit: null,
      };
      const p2BeforePreview = await fetchBuilderState(target, explorerId);
      const p2BeforeDocument = structuredClone(p2BeforePreview.workspace.documents.find((item) => item.output?.id === p2OutputId));
      journey.refusalTable.beforePreview = { draftVersion: p2BeforePreview.draftVersion, draftDigest: p2BeforePreview.draftDigest, document: p2BeforeDocument };
      const refusal = await previewForRefusal(controlPlan.refusalTable.expectedCode);
      const p2AfterPreview = await fetchBuilderState(target, explorerId);
      const p2AfterDocument = p2AfterPreview.workspace.documents.find((item) => item.output?.id === p2OutputId);
      const p1AfterDocument = p2AfterPreview.workspace.documents.find((item) => item.output?.id === outputId);
      journey.refusalTable.afterPreview = { draftVersion: p2AfterPreview.draftVersion, draftDigest: p2AfterPreview.draftDigest, document: structuredClone(p2AfterDocument) };
      recordLiteral('j04-unsupported-unit-refusal-does-not-mutate-draft', {
        draftVersion: p2BeforePreview.draftVersion, draftDigest: p2BeforePreview.draftDigest, document: p2BeforeDocument,
      }, {
        draftVersion: p2AfterPreview.draftVersion, draftDigest: p2AfterPreview.draftDigest, document: p2AfterDocument,
      }, { requestId: refusal.requestId, errorCode: refusal.code });
      recordLiteral('j04-unsupported-unit-refusal-preserves-success-table', p1DocumentBefore, p1AfterDocument, { p1OutputId: outputId });
      recordLiteral('j04-unsupported-unit-row-source-identity-is-fixture-owned', 'Observation/j04-unsupported-unit', journey.refusalTable.sourceIdentity);
      journey.refusalTable.published = false;
      journey.refusalTable.publicationReceiptId = null;
    });

    recordLiteral('j04-earliest-latest-tie-and-window-exclusion-match-literal-oracle', {
      earliest: 'Observation/j04-measure-001', latestTieWinner: 'Observation/j04-measure-002', windowExcluded: true,
    }, {
      earliest: journey.windowedSelections?.find((item) => item.direction === 'ASC')?.preview?.sourceIdentities?.[0],
      latestTieWinner: journey.windowedSelections?.find((item) => item.direction === 'DESC')?.preview?.sourceIdentities?.[0],
      windowExcluded: !journey.windowedSelections?.flatMap((item) => item.preview?.sourceIdentities ?? []).includes('Observation/j04-window-excluded'),
    });
    recordLiteral('j04-unsupported-unit-preview-refuses-without-applying', {
      code: controlPlan.refusalTable.expectedCode, applied: false, published: false,
    }, {
      code: journey.refusalTable.preview?.code, applied: false, published: journey.refusalTable.published,
    }, { requestId: journey.refusalTable.preview?.requestId, outputId: journey.refusalTable.outputId });
    recordLiteral('j04-unit-normalization-preserves-zero-and-refuses-unsupported-units', {
      normalizedZero: 0, refusalCode: controlPlan.refusalTable.expectedCode,
    }, {
      normalizedZero: journey.previews.find((item) => item.label === metricLabel && item.expected === 0)?.actual,
      refusalCode: journey.refusalTable.preview?.code,
    });

    journey.status = 'completed';
    const requiredVisibleActions = [
      'create-blank-explorer-through-visible-controls',
      'create-patient-table-and-attach-selection-in-builder',
      'add-observation-source-through-advanced-graph',
      'add-patient-metric-through-visible-column-selector',
    ];
    recordAssertion(report, 'j04-patient-visible-explorer-selection-and-graph-actions-ran', requiredVisibleActions,
      journey.actions.map((item) => item.name).filter((name) => requiredVisibleActions.includes(name)));
  } catch (error) {
    failure = error;
    journey.status = 'failed';
    journey.error = error instanceof Error ? error.message : String(error);
    throw error;
  } finally {
    if (cdp) {
      try { await captureDOM('final'); } catch {}
      try { journey.failureScreenshot = await captureScreenshot('failure'); } catch {}
      try { await browser.close(); } catch {}
    }
    journey.network = network.map(({ path, method, status, requestJSON }) => ({ path, method, status, commandId: requestJSON?.commandId }));
    journey.elapsedMs = Date.now() - started;
    if (failure) report.target.patientOperatorError = journey.error;
    writeJSON(join(evidenceDirectory, 'patient-operator.json'), journey);
    recordEvidence(report, join(evidenceDirectory, 'patient-operator.json'));
  }
};

const verifyJ04BrowserScenario = async (target, report, entryTarget, fixture) => {
  const controlPlan = j04BrowserControlPlan(fixture.contract);
  const evidenceDirectory = report.target.evidenceDirectory;
  const downloadDirectory = join(evidenceDirectory, 'downloads');
  mkdirSync(downloadDirectory, { recursive: true, mode: 0o700 });
  report.target.ports = { api: target.apiPort, ui: target.uiPort };
  report.target.fixtureContract = 'j04-contract.fixture.json';
  report.target.workspaceSnapshots = {};
  report.target.capabilityIdentities = [];
  report.target.pivotCapabilityIdentities = [];
  report.target.pivotSourceColumns = null;
  report.target.proposalIdentities = [];
  report.target.previewRows = null;
  report.target.viewerRows = null;
  report.target.downloadedTypedArtifact = null;
  report.target.contributorEvidence = null;
  report.target.exclusionEvidence = null;
  report.target.informationLossEvidence = null;
  report.target.appliedComparisonScreenshot = null;
  report.target.browserControlPlan = controlPlan;
  report.target.proposalEvidenceCrossSurfaceGap = {
    status: 'known-gap',
    proposalSurface: 'table-shape comparison response',
    artifactSurface: 'quality.json and provenance.json',
    detail: 'The typed artifact does not currently carry proposal-time contributor, exclusion, or grouped-pivot information-loss evidence. Final artifact equality is limited to retained schema and rows; proposal evidence is asserted from the server comparison response.',
  };
  recordLimitation(report, 'j04-proposal-evidence-is-not-carried-into-typed-artifact', report.target.proposalEvidenceCrossSurfaceGap.detail);
  const network = [];
  const pendingNetworkBodies = new Set();
  let browser;
  let cdp;
  let failure;
  const started = Date.now();
  const captureDOM = async (name) => {
    const path = join(evidenceDirectory, `${name}.html`);
    await snapshot(cdp, path);
    recordEvidence(report, path);
    return path;
  };
  const captureScreenshot = async (name) => {
    const shot = await cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
    const path = join(evidenceDirectory, `${name}.png`);
    writeFileSync(path, Buffer.from(shot.data, 'base64'), { mode: 0o600 });
    recordEvidence(report, path);
    return path;
  };
  const action = async (name, operation) => {
    const actionStarted = Date.now();
    await operation();
    report.actions ??= [];
    report.actions.push({ name, elapsedMs: Date.now() - actionStarted });
  };
  const failAtMissingDOMAction = (actionName, selector, reason, details = {}) => {
    report.target.firstMissingDOMAction ??= { action: actionName, selector, reason, ...details };
    report.assertions.push({ name: `j04-dom-action-${actionName}`, status: 'failed', expected: selector, actual: null });
    markJ04DownstreamUnproven(report);
    throw new Error(`J04 first missing DOM action: ${actionName} requires ${selector}: ${reason}`);
  };
  const saveWorkspaceSnapshot = async (stage, explorerId) => {
    const state = await fetchBuilderState(target, explorerId);
    const snapshotValue = j04WorkspaceSnapshot(state);
    report.target.workspaceSnapshots[stage] = snapshotValue;
    const path = join(evidenceDirectory, `workspace-${stage}.json`);
    writeJSON(path, snapshotValue);
    recordEvidence(report, path);
    return state;
  };
  const waitForColumnCount = async (explorerId, outputId, count) => {
    const startedWaiting = Date.now();
    while (Date.now() - startedWaiting < 60000) {
      const state = await fetchBuilderState(target, explorerId);
      const document = state.workspace?.documents?.find((candidate) => candidate.output?.id === outputId);
      if (document?.columns?.length === count) return state;
      await sleep(100);
    }
    throw new Error(`J04 Builder did not persist ${count} browser-authored columns`);
  };
  const candidatePath = (candidate) => String(candidate?.fieldPath ?? '').replace(/^root\./, '');
  const fieldChoicesFromCatalog = (state, nodeId, columns) => columns.map((column) => {
    const candidate = state.catalog?.candidates?.find((item) => item.nodeId === nodeId && candidatePath(item) === column.path);
    const options = candidate?.constructionChoice?.options ?? [];
    const selectedOptions = column.projectionMode
      ? options.filter((option) => option.form === column.projectionMode)
      : options.filter((option) => option.decision === 'DEFAULT');
    if (!candidate?.candidateId || candidate.constructionChoice?.source?.kind !== 'FIELD' || selectedOptions.length !== 1) {
      throw new Error(`J04 cannot browser-author required field ${fixture.contract.baseRowResourceType}.${column.path} from the saved catalog`);
    }
    return { ...column, candidate, selection: { choiceId: candidate.constructionChoice.choiceId, form: selectedOptions[0].form } };
  });
  const authorCatalogFields = async (explorerId, outputId, resourceType, fields, initialColumnCount) => {
    for (let index = 0; index < fields.length; index += 1) {
      const field = fields[index];
      await browserEval(cdp, `setInput('Search features by field name, concept, or code', ${JSON.stringify(field.path)})`);
      await sleep(100);
      await waitForBrowser(cdp, `(() => {
        const input = document.querySelector('input[aria-label="Search features by field name, concept, or code"]');
        const submit = input?.closest('form')?.querySelector('button[type="submit"]');
        return input?.value === ${JSON.stringify(field.path)} && Boolean(submit && !submit.disabled);
      })()`, 60000);
      await browserEval(cdp, `clickButton('Search')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector(${JSON.stringify(`input[aria-label="Select ${resourceType}.${field.path}"]:not(:disabled)`)}))`, 60000);
      await browserEval(cdp, `const input = inputByLabel(${JSON.stringify(`Select ${resourceType}.${field.path}`)}); if (!input || input.disabled) throw new Error('J04 visible field choice is unavailable: ' + ${JSON.stringify(field.path)}); input.click();`);
      await browserEval(cdp, `clickButton('Add 1 selected feature')`);
      const expectedCount = initialColumnCount + index + 1;
      let outputFormDialog = false;
      const startedResolvingForm = Date.now();
      while (Date.now() - startedResolvingForm < 5000) {
        outputFormDialog = await evaluate(cdp, `Boolean(document.querySelector('[role="dialog"][aria-labelledby="catalog-selection-dialog-title"]'))`);
        if (outputFormDialog) break;
        const current = await fetchBuilderState(target, explorerId);
        const currentDocument = current.workspace?.documents?.find((candidate) => candidate.output?.id === outputId);
        if (currentDocument?.columns?.length === expectedCount) break;
        await sleep(50);
      }
      if (outputFormDialog) {
        await browserEval(cdp, `(() => {
          const dialog = document.querySelector('[role="dialog"][aria-labelledby="catalog-selection-dialog-title"]');
          const suffix = ' · ' + ${JSON.stringify(field.selection.form)};
          const choice = [...(dialog?.querySelectorAll('input[type="radio"][aria-label]') || [])]
            .find((input) => input.getAttribute('aria-label')?.endsWith(suffix));
          if (!choice || choice.disabled) throw new Error('J04 output form is unavailable: ' + suffix);
          if (!choice.checked) choice.click();
          const confirm = [...dialog.querySelectorAll('button')].find((button) => norm(button.textContent) === 'Add 1 selected feature');
          if (!confirm || confirm.disabled) throw new Error('J04 output-form confirmation is unavailable');
          confirm.click();
        })()`);
      }
      await waitForColumnCount(explorerId, outputId, expectedCount);
      await waitForBrowser(cdp, `[...document.querySelectorAll('span')].some((element) => element.textContent?.trim() === ${JSON.stringify(`${expectedCount} configured`)})`, 60000);
    }
    return fetchBuilderState(target, explorerId);
  };
  const clickTestID = async (testId) => browserEval(cdp, `(() => {
    const element = document.querySelector('[data-testid="' + ${JSON.stringify(testId)} + '"]');
    if (!element || !visible(element)) throw new Error('J04 visible test selector is missing: ' + ${JSON.stringify(testId)});
    if (element.disabled || element.getAttribute('aria-disabled') === 'true') throw new Error('J04 test selector is disabled: ' + ${JSON.stringify(testId)});
    element.scrollIntoView({ block: 'center' });
    element.click();
    return ${JSON.stringify(testId)};
  })()`);
  const setTestInput = async (testId, value) => browserEval(cdp, `(() => {
    const element = document.querySelector('[data-testid="' + ${JSON.stringify(testId)} + '"]');
    if (!element || !visible(element)) throw new Error('J04 visible test input is missing: ' + ${JSON.stringify(testId)});
    if (element.disabled) throw new Error('J04 test input is disabled: ' + ${JSON.stringify(testId)});
    const descriptor = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(element), 'value');
    descriptor?.set?.call(element, ${JSON.stringify(String(value))});
    element.dispatchEvent(new Event('input', { bubbles: true }));
    element.dispatchEvent(new Event('change', { bubbles: true }));
    return element.value;
  })()`);
  const selectTestOption = async (testId, textNeedle) => browserEval(cdp, `(() => {
    const select = document.querySelector('[data-testid="' + ${JSON.stringify(testId)} + '"]');
    const wanted = String(${JSON.stringify(textNeedle)}).toLocaleLowerCase();
    const option = [...(select?.options || [])].find((item) => norm(item.textContent).toLocaleLowerCase().includes(wanted));
    if (!select || !visible(select)) throw new Error('J04 visible test select is missing: ' + ${JSON.stringify(testId)});
    if (!option || option.disabled) throw new Error('J04 test option is unavailable: ' + ${JSON.stringify(textNeedle)} + '; choices=' + [...select.options].map((item) => norm(item.textContent)).join(' | '));
    Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set?.call(select, option.value);
    select.dispatchEvent(new Event('input', { bubbles: true }));
    select.dispatchEvent(new Event('change', { bubbles: true }));
    return { value: option.value, label: norm(option.textContent) };
  })()`);
  const checkTestID = async (testId, checked = true) => browserEval(cdp, `(() => {
    const input = document.querySelector('[data-testid="' + ${JSON.stringify(testId)} + '"]');
    if (!input || !visible(input)) throw new Error('J04 visible test checkbox is missing: ' + ${JSON.stringify(testId)});
    if (input.disabled) throw new Error('J04 test checkbox is disabled: ' + ${JSON.stringify(testId)});
    if (input.checked !== ${JSON.stringify(checked)}) input.click();
    return input.checked;
  })()`);
  const setAccessibleSelect = async (label, value) => browserEval(cdp, `(() => {
    const select = [...document.querySelectorAll('select[aria-label]')].find((item) => item.getAttribute('aria-label') === ${JSON.stringify(label)} && visible(item));
    if (!select) throw new Error('J04 visible select is missing: ' + ${JSON.stringify(label)});
    const option = [...select.options].find((item) => item.value === ${JSON.stringify(value)});
    if (!option || option.disabled) throw new Error('J04 accessible option is unavailable: ' + ${JSON.stringify(value)} + ' for ' + ${JSON.stringify(label)});
    Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set?.call(select, option.value);
    select.dispatchEvent(new Event('input', { bubbles: true }));
    select.dispatchEvent(new Event('change', { bubbles: true }));
    return option.textContent.trim();
  })()`);
  const checkChoiceByLabel = async (testIdPrefix, labelNeedle) => browserEval(cdp, `(() => {
    const needle = norm(${JSON.stringify(labelNeedle)}).toLocaleLowerCase();
    const input = [...document.querySelectorAll('input[type="checkbox"][data-testid^="' + ${JSON.stringify(testIdPrefix)} + '"]')]
      .find((item) => norm(item.closest('label')?.innerText).toLocaleLowerCase().includes(needle));
    if (!input || !visible(input)) throw new Error('J04 visible choice is missing: ' + ${JSON.stringify(testIdPrefix)} + ' / ' + ${JSON.stringify(labelNeedle)});
    if (input.disabled) throw new Error('J04 visible choice is disabled: ' + ${JSON.stringify(labelNeedle)});
    if (!input.checked) input.click();
    return { testId: input.dataset.testid, label: norm(input.closest('label')?.innerText) };
  })()`);

  try {
    browser = await launchBrowser(downloadDirectory);
    cdp = browser.cdp;
    cdp.on('Network.requestWillBeSent', (event) => {
      if (!event.request.url.includes('/authoring/v2/')) return;
      const path = new URL(event.request.url).pathname;
      network.push({ requestId: event.requestId, path, method: event.request.method,
        requestJSON: event.request.postData ? (() => { try { return JSON.parse(event.request.postData); } catch { return undefined; } })() : undefined });
    });
    cdp.on('Network.responseReceived', (event) => {
      const item = network.find((candidate) => candidate.requestId === event.requestId);
      if (item) {
        item.status = event.response.status;
        item.mimeType = event.response.mimeType;
      }
    });
    cdp.on('Network.loadingFinished', (event) => {
      const item = network.find((candidate) => candidate.requestId === event.requestId);
      if (!item || !/table-shape|preview/i.test(item.path) || item.responseJSON !== undefined) return;
      const capture = (async () => {
        try {
          const body = await cdp.send('Network.getResponseBody', { requestId: item.requestId });
          const raw = body.base64Encoded ? Buffer.from(body.body, 'base64').toString('utf8') : body.body;
          item.responseJSON = JSON.parse(raw);
        } catch (error) { item.responseBodyError = String(error); }
      })().finally(() => pendingNetworkBodies.delete(capture));
      pendingNetworkBodies.add(capture);
    });
    const explorerId = report.target.bootstrapExplorerId;
    if (!explorerId) throw new Error('J04 fixture seed has no empty bootstrap Explorer identity');
    report.target.explorerId = explorerId;
    const before = await saveWorkspaceSnapshot('before', explorerId);
    recordAssertion(report, 'j04-fixture-starts-with-an-empty-editor-workspace', 0, before.workspace?.documents?.length ?? 0);
    const builderURL = `${target.uiUrl}/?project=${encodeURIComponent(target.fixtureProject)}&explorer=${encodeURIComponent(explorerId)}&mode=builder`;
    await navigate(cdp, entryTarget.uiUrl);
    await navigate(cdp, builderURL);
    await waitForBrowser(cdp, `document.querySelector('.demo-controls span')?.textContent.trim() === ${JSON.stringify(`${target.fixtureProject} / ${explorerId}`)} && (${builderDOMReadyCondition})`, 60000);
    await captureDOM('j04-builder-before');

    await action('create-observation-table-in-builder', async () => {
      await browserEval(cdp, `setInput('first-table-name', 'J04 measurements')`);
      await waitForBrowser(cdp, `(() => {
        const button = document.querySelector('button[aria-label="Choose Observation rows"]');
        return Boolean(button && !button.disabled && button.getClientRects().length > 0);
      })()`, 60000);
      await browserEval(cdp, `clickButton('Choose Observation rows')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('input[aria-label="Search features by field name, concept, or code"]'))`, 60000);
    });

    let state = await fetchBuilderState(target, explorerId);
    const rowResourceType = fixture.contract.baseRowResourceType;
    const root = state.catalog?.nodes?.find((node) => node.resourceType === rowResourceType && node.rowRootEligible);
    const outputDocument = state.workspace?.documents?.find((document) => document.rootResourceType === rowResourceType);
    if (!root?.nodeId || !outputDocument?.output?.id) throw new Error(`J04 ${rowResourceType} row selection did not create a saved output table`);
    const outputId = outputDocument.output.id;
    report.target.outputId = outputId;
    const patientJourney = report.target.patientOperatorJourney;
    recordAssertion(report, 'j04-patient-operator-population-is-exactly-the-fixture-identity',
      controlPlan.aggregate.selectedRowIdentities, patientJourney?.selectionRevision?.members?.map((item) => `${item.resourceType}/${item.id}`) ?? []);
    const fieldChoices = fieldChoicesFromCatalog(state, root.nodeId, fixture.contract.baseColumns);
    report.target.capabilityIdentities = fieldChoices.map(({ path, candidate, selection }) => ({
      path,
      candidateId: candidate.candidateId,
      choiceId: selection.choiceId,
      form: selection.form,
    }));
    recordAssertion(report, 'j04-base-field-capability-identities-come-from-the-saved-catalog', fixture.contract.baseColumns.map((column) => column.path), fieldChoices.map((choice) => candidatePath(choice.candidate)));

    await action('add-base-columns-through-visible-builder-controls', async () => {
      state = await authorCatalogFields(explorerId, outputId, rowResourceType, fieldChoices, 0);
    });

    const authored = state.workspace?.documents?.find((document) => document.output?.id === outputId);
    if (!authored) throw new Error('J04 browser-authored output disappeared from the saved workspace');
    report.target.exactOutputSchemaColumns = authored.columns.map((column) => ({
      id: column.column,
      name: column.label,
      position: column.table?.order,
      source: {
        kind: column.source?.kind,
        path: column.source?.field?.path?.replace(/^root\./, ''),
      },
    }));
    recordAssertion(report, 'j04-builder-saves-the-literal-browser-authored-base-column-identities', fixture.contract.baseColumns.map((column) => column.path), report.target.exactOutputSchemaColumns.map((column) => column.source.path));
    await saveWorkspaceSnapshot('after-base-authoring', explorerId);
    await captureDOM('j04-builder-base-authored');
    const pivotFieldChoices = fieldChoicesFromCatalog(state, root.nodeId, fixture.contract.pivot.requiredSourceColumns);
    report.target.pivotCapabilityIdentities = pivotFieldChoices.map(({ path, candidate, selection }) => ({
      path,
      candidateId: candidate.candidateId,
      choiceId: selection.choiceId,
      form: selection.form,
    }));
    recordAssertion(report, 'j04-pivot-source-columns-come-from-the-saved-catalog',
      fixture.contract.pivot.requiredSourceColumns.map((column) => column.path),
      pivotFieldChoices.map((choice) => candidatePath(choice.candidate)));
    await action('add-pivot-source-columns-through-visible-builder-controls', async () => {
      state = await authorCatalogFields(explorerId, outputId, rowResourceType, pivotFieldChoices, authored.columns.length);
    });
    const pivotAuthored = state.workspace?.documents?.find((document) => document.output?.id === outputId);
    if (!pivotAuthored) throw new Error('J04 pivot source columns were not saved in the Observation output');
    report.target.pivotSourceColumns = pivotAuthored.columns.map((column) => column.source?.field?.path?.replace(/^root\./, ''));
    recordAssertion(report, 'j04-pivot-source-columns-are-saved-before-pivot-configuration',
      [...fixture.contract.baseColumns, ...fixture.contract.pivot.requiredSourceColumns].map((column) => column.path),
      report.target.pivotSourceColumns);
    const recoding = controlPlan.observation.recoding;
    const scalarFields = controlPlan.observation.presenceCases.map((item) => ({
      path: item.fieldPath,
      label: `J04 ${item.presence} scalar evidence`,
    }));
    const absenceFields = [
      { path: controlPlan.observation.recordedAbsence.systemPath, label: 'J04 recorded-absence code system', projectionMode: 'FIRST' },
      { path: controlPlan.observation.recordedAbsence.codingPath, label: 'J04 recorded-absence code', projectionMode: 'FIRST' },
      { path: controlPlan.observation.recordedAbsence.displayPath, label: 'J04 recorded-absence display', projectionMode: 'FIRST' },
    ];
    const existingPaths = new Set(pivotAuthored.columns.map((column) => String(column?.source?.field?.path ?? '').replace(/^root\./, '')));
    const additionalFields = [...new Map([
      ...scalarFields,
      ...absenceFields,
      { path: recoding.sourcePath, label: 'J04 exact category recoding', projectionMode: 'FIRST' },
    ].map((column) => [column.path, column])).values()].filter((column) => !existingPaths.has(column.path));
    const additionalChoices = fieldChoicesFromCatalog(state, root.nodeId, additionalFields);
    const recodingChoice = additionalChoices.find((choice) => choice.path === recoding.sourcePath)
      ?? pivotFieldChoices.find((choice) => choice.path === recoding.sourcePath);
    if (!recodingChoice) throw new Error(`J04 cannot resolve the saved recoding capability ${recoding.sourcePath}`);
    report.target.policyCapabilityIdentities = [...additionalChoices, recodingChoice].map(({ path, candidate, selection }) => ({
      path,
      candidateId: candidate.candidateId,
      choiceId: selection.choiceId,
      form: selection.form,
      logicalType: candidate.logicalType,
      projectionModes: candidate.projectionModes,
      recoding: candidate.valueTransformations?.exactCategoryRecode ?? candidate.valueTransformations?.codedValueRecoding,
    }));
    await action('add-presence-and-recode-columns-through-visible-builder-controls', async () => {
      state = await authorCatalogFields(explorerId, outputId, rowResourceType, additionalChoices, pivotAuthored.columns.length);
    });
    let authoredObservation = state.workspace?.documents?.find((document) => document.output?.id === outputId);
    if (!authoredObservation) throw new Error('J04 Observation output disappeared after Values columns were authored');
    const sourcePathFor = (column) => String(column?.source?.field?.path ?? '').replace(/^root\./, '');
    const recodingColumn = authoredObservation.columns.find((column) => sourcePathFor(column) === recoding.sourcePath);
    if (!recodingColumn) throw new Error(`J04 Builder omitted the recoding source column ${recoding.sourcePath}`);
    const recodingCandidate = recodingChoice.candidate;
    const recodingCapability = recodingCandidate?.valueTransformations?.exactCategoryRecode
      ?? recodingCandidate?.valueTransformations?.codedValueRecoding;
    if (!recodingCapability?.available) {
      failAtMissingDOMAction('open-exact-category-recoding', `details summary "Recode exact category values" for ${recodingColumn.label}`,
        recodingCapability?.reason ?? `the saved catalog offered no exact recoding capability for ${recoding.sourcePath}`, {
          sourcePath: recoding.sourcePath,
          candidateId: recodingCandidate?.candidateId,
        });
    }
    await action('recode-exact-category-values-through-values-controls', async () => {
      await browserEval(cdp, `setInput('Search columns', ${JSON.stringify(recoding.sourcePath)})`);
      await waitForBrowser(cdp, `Boolean(document.querySelector(${JSON.stringify(`input[aria-label="Display name for configured ${recodingColumn.label}"]`)}))`, 60000);
      const summary = await browserEval(cdp, `return Boolean([...document.querySelectorAll('summary')].find((item) => visible(item) && norm(item.textContent) === 'Recode exact category values'))`);
      if (!summary) failAtMissingDOMAction('open-exact-category-recoding', `details summary "Recode exact category values" for ${recodingColumn.label}`,
        'the Values section did not render the saved catalog recoding capability');
      await browserEval(cdp, `clickText('summary', 'Recode exact category values')`);
      const recodeMappings = Object.entries(recoding.mapping);
      for (let index = 0; index < recodeMappings.length; index += 1) {
        await browserEval(cdp, `clickButton('Add mapping')`);
        const [targetFrom, targetTo] = recodeMappings[index];
        await browserEval(cdp, `setInput(${JSON.stringify(`Recorded category ${index + 1} for ${recodingColumn.label}`)}, ${JSON.stringify(targetFrom)})`);
        await browserEval(cdp, `setInput(${JSON.stringify(`Replacement value ${index + 1} for ${recodingColumn.label}`)}, ${JSON.stringify(targetTo)})`);
      }
      await setAccessibleSelect(`Unmapped value policy for ${recodingColumn.label}`, recoding.unknownPolicy);
      await browserEval(cdp, `clickButton('Save recoding')`);
      const startedSaving = Date.now();
      while (Date.now() - startedSaving < 30000) {
        state = await fetchBuilderState(target, explorerId);
        authoredObservation = state.workspace?.documents?.find((document) => document.output?.id === outputId);
        const saved = authoredObservation?.columns?.find((column) => sourcePathFor(column) === recoding.sourcePath)?.valueTransformation;
        if (saved?.kind === 'EXACT_CATEGORY_RECODE') break;
        await sleep(150);
      }
      const saved = authoredObservation?.columns?.find((column) => sourcePathFor(column) === recoding.sourcePath)?.valueTransformation;
      const savedMapping = Object.fromEntries((saved?.exactCategoryRecode?.mappings ?? []).map(({ from, to }) => [from, to]));
      recordAssertion(report, 'j04-values-control-saves-exact-case-recoding-and-unknown-preservation', {
        mapping: recoding.mapping,
        unknownPolicy: recoding.unknownPolicy,
      }, { mapping: savedMapping, unknownPolicy: saved?.exactCategoryRecode?.unknownPolicy });
      if (!j04ExactEqual({ mapping: recoding.mapping, unknownPolicy: recoding.unknownPolicy },
        { mapping: savedMapping, unknownPolicy: saved?.exactCategoryRecode?.unknownPolicy })) {
        throw new Error(`J04 browser-authored recoding differs from its literal plan: ${JSON.stringify(saved)}`);
      }
      await browserEval(cdp, `setInput('Search columns', '')`);
      report.target.recoding = { column: recodingColumn.column, sourcePath: recoding.sourcePath, saved: saved.exactCategoryRecode };
    });
    const presenceAndAbsencePaths = [...new Set([...scalarFields.map((item) => item.path), ...absenceFields.map((item) => item.path)])];
    const savedPresencePaths = authoredObservation.columns.map(sourcePathFor).filter((path) => presenceAndAbsencePaths.includes(path));
    recordAssertion(report, 'j04-observation-output-authors-valid-source-states-and-recorded-absence-coding',
      [...presenceAndAbsencePaths].sort(),
      [...new Set(savedPresencePaths)].sort());
    report.target.presenceColumns = controlPlan.observation.presenceCases.map((item) => {
      const column = authoredObservation.columns.find((candidate) => sourcePathFor(candidate) === item.fieldPath);
      return { sourceRecordId: item.sourceRecordId, fieldPath: item.fieldPath, presence: item.presence, column: column?.column, label: column?.label };
    });
    await action('preview-observation-source-scalar-presence-before-grouping', async () => {
      const startIndex = network.length;
      await browserEval(cdp, `clickButton('Preview')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))`, 60000);
      const previewDeadline = Date.now() + 30000;
      let response;
      while (Date.now() < previewDeadline) {
        await Promise.allSettled([...pendingNetworkBodies]);
        response = network.slice(startIndex).find((item) => item.path.endsWith('/preview') && item.responseJSON !== undefined);
        if (response) break;
        await sleep(50);
      }
      if (!response || response.status !== 200 || !Array.isArray(response.responseJSON?.rows)) {
        throw new Error(`J04 pre-grouping Observation Preview was unavailable: ${JSON.stringify(network.slice(startIndex).filter((item) => item.path.endsWith('/preview')).map(({ status, responseJSON, responseBodyError }) => ({ status, responseJSON, responseBodyError }))).slice(0, 1000)}`);
      }
      const preview = response.responseJSON;
      const columns = preview.columns ?? [];
      const rows = readJ05OutputRows(preview.rows, columns.map((column) => column.column), { preserveMissing: true });
      const previewColumnForAuthored = (authoredColumn) => columns.find((column) =>
        column.column === authoredColumn.column || column.authoredColumns?.includes(authoredColumn.column));
      const sourceID = authoredObservation.columns.find((column) => sourcePathFor(column) === 'id');
      const previewID = sourceID && previewColumnForAuthored(sourceID);
      if (!previewID) throw new Error('J04 pre-grouping Preview omitted the authored Observation.id column needed to identify scalar examples');
      const bySourceID = new Map(rows.map((values) => [values[previewID.column], values]));
      const actual = controlPlan.observation.presenceCases.map((item) => {
        const authoredColumn = authoredObservation.columns.find((column) => sourcePathFor(column) === item.fieldPath);
        const previewColumn = authoredColumn && previewColumnForAuthored(authoredColumn);
        if (!previewColumn) return { sourceRecordId: item.sourceRecordId, fieldPath: item.fieldPath, expected: item.presence, actual: 'column-not-emitted' };
        const row = bySourceID.get(item.sourceRecordId);
        if (!row) return { sourceRecordId: item.sourceRecordId, fieldPath: item.fieldPath, expected: item.presence, actual: 'source-row-not-emitted' };
        return { sourceRecordId: item.sourceRecordId, fieldPath: item.fieldPath, ...j04ValueEvidence(row, previewColumn.column) };
      });
      const expected = controlPlan.observation.presenceCases.map((item) => ({
        sourceRecordId: item.sourceRecordId,
        fieldPath: item.fieldPath,
        presence: item.presence === 'missing' || item.presence === 'null' ? 'null' : item.presence,
        value: item.presence === 'missing' ? null : item.value,
      }));
      recordAssertion(report, 'j04-observation-preview-materializes-missing-values-as-null-cells', expected, actual);

      const previewOutputId = preview.outputId ?? outputId;
      if (!preview.receiptId || previewOutputId !== outputId) {
        throw new Error(`J04 source-presence Preview returned an invalid receipt/output identity: ${JSON.stringify({ receiptId: preview.receiptId, previewOutputId, outputId })}`);
      }
      const requestCellTrace = async (rowId, column) => {
        const result = await requestJSON(`${bootstrapAuthoringURL(target, explorerId)}/cell-trace`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ receiptId: preview.receiptId, outputId: previewOutputId, rowId, column, limit: 10 }),
          timeout: 30000,
        });
        if (result.response.status !== 200 || !result.value?.trace) {
          throw new Error(`J04 receipt-bound CellTrace failed for row ${rowId}, column ${column}: HTTP ${result.response.status} ${JSON.stringify(result.value).slice(0, 1200)}`);
        }
        return result.value;
      };
      const cellTraces = [];
      for (const item of controlPlan.observation.presenceCases) {
        const authoredColumn = authoredObservation.columns.find((column) => sourcePathFor(column) === item.fieldPath);
        const previewColumn = authoredColumn && previewColumnForAuthored(authoredColumn);
        if (!previewColumn) throw new Error(`J04 source-presence Preview omitted ${item.fieldPath} public column identity`);
        const rowId = j04DefaultRecordCellTraceRowID(target.fixtureProject, target.fixtureGeneration, 'Observation', item.sourceRecordId);
        const traceResponseJSON = await requestCellTrace(rowId, previewColumn.column);
        const trace = traceResponseJSON.trace;
        const identityTraceResponse = await requestCellTrace(rowId, previewID.column);
        const identityTrace = identityTraceResponse.trace;
        const identityEvidence = {
          rowId: identityTrace.rowId,
          status: identityTrace.status,
          value: identityTrace.value,
          contributors: (identityTrace.contributions ?? []).map(({ resourceType, resourceId, value }) => ({ resourceType, resourceId, value })),
        };
        if (identityEvidence.rowId !== rowId || identityEvidence.status !== 'VALUE'
          || identityEvidence.value !== item.sourceRecordId || identityEvidence.contributors.length !== 1
          || identityEvidence.contributors[0].resourceType !== 'Observation'
          || identityEvidence.contributors[0].resourceId !== item.sourceRecordId
          || identityEvidence.contributors[0].value !== item.sourceRecordId) {
          throw new Error(`J04 CellTrace row identity did not resolve to source Observation/${item.sourceRecordId}: ${JSON.stringify(identityEvidence)}`);
        }
        cellTraces.push({
          sourceRecordId: item.sourceRecordId,
          fieldPath: item.fieldPath,
          rowId,
          traceRowId: trace.rowId,
          column: previewColumn.column,
          outputId: previewOutputId,
          receiptId: preview.receiptId,
          binding: traceResponseJSON.binding,
          feature: traceResponseJSON.feature,
          status: trace.status,
          value: trace.value,
          contributors: (trace.contributions ?? []).map(({ resourceType, resourceId, value }) => ({ resourceType, resourceId, value })),
          complete: trace.complete,
          identityTrace: identityEvidence,
        });
      }
      const expectedTraces = controlPlan.observation.presenceCases.map((item) => {
        const cell = cellTraces.find((candidate) => candidate.sourceRecordId === item.sourceRecordId);
        const common = {
          sourceRecordId: item.sourceRecordId,
          fieldPath: item.fieldPath,
          rowId: cell?.rowId,
          traceRowId: cell?.rowId,
          column: cell?.column,
          outputId: previewOutputId,
          receiptId: preview.receiptId,
        };
        const identityTrace = {
          rowId: cell?.rowId,
          status: 'VALUE',
          value: item.sourceRecordId,
          contributors: [{ resourceType: 'Observation', resourceId: item.sourceRecordId, value: item.sourceRecordId }],
        };
        if (item.presence === 'missing' || item.presence === 'null') {
          return { ...common, status: 'NO_MATCH', value: null, contributors: [], complete: true, identityTrace };
        }
        return {
          ...common,
          status: 'VALUE',
          value: item.value,
          contributors: [{ resourceType: 'Observation', resourceId: item.sourceRecordId, value: item.value }],
          complete: true,
          identityTrace,
        };
      });
      const actualTraces = cellTraces.map(({ sourceRecordId, fieldPath, rowId, traceRowId, column, outputId: traceOutputId, receiptId, status, value, contributors, complete, identityTrace }) => ({
        sourceRecordId, fieldPath, rowId, traceRowId, column, outputId: traceOutputId, receiptId, status, value, contributors, complete, identityTrace,
      }));
      recordAssertion(report, 'j04-observation-cell-trace-proves-literals-and-exact-source-row-identities', expectedTraces, actualTraces);
      report.target.fixtureMissingValueCellTrace = 'NO_MATCH; exact source row is independently confirmed by its id-column VALUE trace';
      report.target.sourcePresenceEvidence = { previewReceiptId: preview.receiptId, previewOutputId, previewValues: actual, cellTraces };
      await captureDOM('j04-observation-source-preview-presence');
      await navigate(cdp, builderURL);
      await waitForBrowser(cdp, `document.body.innerText.includes('J04 measurements') && Boolean(document.querySelector('[data-testid="ui04-open-table-shape-settings"]'))`, 60000);
    });
    report.target.recordedAbsenceColumns = absenceFields.map((field) => {
      const column = authoredObservation.columns.find((candidate) => sourcePathFor(candidate) === field.path);
      return { path: field.path, column: column?.column, label: column?.label };
    });
    const policyRegionColumns = [];
    for (const column of authoredObservation.columns) {
      await browserEval(cdp, `setInput('Search columns', ${JSON.stringify(column.column)})`);
      await waitForBrowser(cdp, `Boolean(document.querySelector(${JSON.stringify(`input[aria-label="Display name for configured ${column.label}"]`)}))`, 10000);
      const regions = await evaluate(cdp, `(() => {
        const input = document.querySelector(${JSON.stringify(`input[aria-label="Display name for configured ${column.label}"]`)});
        const row = input?.closest('div.grid');
        return {
          values: row?.querySelectorAll('[data-testid="feature-policy-values"]').length ?? 0,
          timeUnits: row?.querySelectorAll('[data-testid="feature-policy-time-units"]').length ?? 0,
        };
      })()`);
      if (regions.values === 1 && regions.timeUnits === 1) policyRegionColumns.push(column.column);
    }
    await browserEval(cdp, `setInput('Search columns', '')`);
    const policyRegions = { authored: authoredObservation.columns.length, verifiedColumns: policyRegionColumns };
    recordAssertion(report, 'j04-values-and-time-unit-policy-regions-render-for-authored-observation-columns',
      authoredObservation.columns.map((column) => column.column).sort(), [...policyRegionColumns].sort());
    report.target.observationPolicyRegions = policyRegions;
    await saveWorkspaceSnapshot('after-pivot-and-values-authoring', explorerId);
    await captureDOM('j04-builder-pivot-and-values-authored');

    await action('preview-recorded-fhir-absence-before-reshape', async () => {
      const requestStart = network.length;
      await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Preview' && !button.disabled))`, 60000);
      await browserEval(cdp, `clickButton('Preview')`);
      await waitForBrowser(cdp, `Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))`, 60000);
      let previewResponse;
      const deadline = Date.now() + 60000;
      while (Date.now() < deadline) {
        await Promise.allSettled([...pendingNetworkBodies]);
        previewResponse = network.slice(requestStart).reverse().find((item) => item.path.endsWith('/preview') && item.responseJSON?.rows);
        if (previewResponse) break;
        await sleep(150);
      }
      if (!previewResponse) throw new Error('J04 recorded-absence Preview returned no receipt-backed row response');
      const idColumn = authoredObservation.columns.find((column) => sourcePathFor(column) === 'id');
      const valueColumn = authoredObservation.columns.find((column) => sourcePathFor(column) === controlPlan.observation.recordedAbsence.valuePath);
      const codeColumn = authoredObservation.columns.find((column) => sourcePathFor(column) === controlPlan.observation.recordedAbsence.codingPath);
      const systemColumn = authoredObservation.columns.find((column) => sourcePathFor(column) === controlPlan.observation.recordedAbsence.systemPath);
      const displayColumn = authoredObservation.columns.find((column) => sourcePathFor(column) === controlPlan.observation.recordedAbsence.displayPath);
      if (![idColumn, valueColumn, codeColumn, systemColumn, displayColumn].every(Boolean)) throw new Error('J04 recorded-absence Preview is missing a visible authored source column');
      const sourceRecordID = controlPlan.observation.recordedAbsence.sourceRecordId;
      const rowIndex = previewResponse.responseJSON.rows.findIndex((row) => row?.[idColumn.column] === sourceRecordID);
      if (rowIndex < 0) throw new Error(`J04 Preview did not emit Observation/${sourceRecordID}`);
      const row = previewResponse.responseJSON.rows[rowIndex];
      const getCell = (column) => Object.hasOwn(row, column.column) ? row[column.column] : null;
      const projected = {
        sourceIdentity: `Observation/${getCell(idColumn)}`,
        valueString: getCell(valueColumn),
        absenceCode: getCell(codeColumn),
        absenceSystem: getCell(systemColumn),
        absenceDisplay: getCell(displayColumn),
      };
      await browserEval(cdp, `scrollVirtualTableToRow('preview-table-scroll', ${rowIndex})`);
      const codePreviewColumnIndex = previewResponse.responseJSON.columns.findIndex((column) =>
        column.column === codeColumn.column || column.authoredColumns?.includes(codeColumn.column));
      if (codePreviewColumnIndex < 0) throw new Error('J04 recorded-absence code has no emitted Preview column');
      await browserEval(cdp, `(() => {
        const scroll = document.querySelector('[data-testid="preview-table-scroll"]');
        if (!scroll) throw new Error('J04 Preview scroll viewport is unavailable');
        scroll.scrollLeft = ${codePreviewColumnIndex} * 180;
        scroll.dispatchEvent(new Event('scroll'));
      })()`);
      await waitForBrowser(cdp, `[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].some((cell) => cell.textContent.trim() === ${JSON.stringify(codeColumn.label)})`, 10000);
      const visibleRow = await evaluate(cdp, `(() => {
        const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
        const rows = [...(table?.querySelectorAll('[role="row"]') || [])];
        const headers = [...(rows[0]?.querySelectorAll('[role="columnheader"]') || [])].map((cell) => cell.textContent.trim());
        const found = rows.slice(1).find((candidate) => Number.parseFloat(candidate.style.top) === 42 + ${rowIndex} * 44);
        return found ? Object.fromEntries([...found.querySelectorAll('[role="cell"]')].map((cell, index) => [headers[index], cell.textContent.trim()])) : null;
      })()`);
      const cellTraceURL = `${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorerId)}/authoring/v2/cell-trace`;
      const traceRowIDs = [
        j04DefaultRecordCellTraceRowID(target.fixtureProject, target.fixtureGeneration, 'Observation', sourceRecordID),
        row.__loom_row_id,
        row.rowId,
        sourceRecordID,
        `Observation/${sourceRecordID}`,
      ]
        .filter((value, index, values) => typeof value === 'string' && value && values.indexOf(value) === index);
      const traceAttempts = [];
      for (const rowId of traceRowIDs) {
        const trace = await requestJSON(cellTraceURL, {
          method: 'POST', headers: { 'Content-Type': 'application/json' }, timeout: 30000,
          body: JSON.stringify({ receiptId: previewResponse.responseJSON.receiptId, outputId, rowId, column: codeColumn.column, limit: 20 }),
        });
        traceAttempts.push({ rowId, status: trace.response.status, value: trace.value });
        if (trace.response.ok && trace.value?.trace?.complete === true) break;
      }
      const completeTrace = traceAttempts.find((item) => item.value?.trace?.complete === true);
      const traceSources = completeTrace?.value?.trace?.contributions?.flatMap((item) => item.resourceType && item.resourceId
        ? [`${item.resourceType}/${item.resourceId}`] : []) ?? [];
      const valueTraceAttempts = [];
      for (const rowId of traceRowIDs) {
        const trace = await requestJSON(cellTraceURL, {
          method: 'POST', headers: { 'Content-Type': 'application/json' }, timeout: 30000,
          body: JSON.stringify({ receiptId: previewResponse.responseJSON.receiptId, outputId, rowId, column: valueColumn.column, limit: 20 }),
        });
        valueTraceAttempts.push({ rowId, status: trace.response.status, value: trace.value });
        if (trace.response.ok && trace.value?.trace?.complete === true) break;
      }
      const completeValueTrace = valueTraceAttempts.find((item) => item.value?.trace?.complete === true);
      const noMatchTrace = completeValueTrace?.value?.trace;
      const absence = controlPlan.observation.recordedAbsence;
      report.target.recordedAbsenceEvidence = {
        outputId,
        receiptId: previewResponse.responseJSON.receiptId,
        previewRequestId: previewResponse.requestId,
        previewStatus: previewResponse.status,
        rowIndex,
        sourceIdentity: projected.sourceIdentity,
        previewValue: projected,
        visibleRow,
        cellTrace: completeTrace?.value ?? { attempts: traceAttempts },
        sourceIdentities: traceSources,
        missingValueTrace: completeValueTrace?.value ?? { attempts: valueTraceAttempts },
      };
      recordAssertion(report, 'j04-missing-valueString-is-a-null-no-match-cell', { sourceIdentity: `Observation/${sourceRecordID}`, value: null, traceStatus: 'NO_MATCH', sourceIdentities: [] }, {
        sourceIdentity: projected.sourceIdentity, value: projected.valueString, traceStatus: noMatchTrace?.status ?? null,
        sourceIdentities: noMatchTrace?.contributions?.flatMap((item) => item.resourceType && item.resourceId ? [`${item.resourceType}/${item.resourceId}`] : []) ?? null,
      });
      recordAssertion(report, 'j04-recorded-absence-is-visibly-emitted-with-exact-coding-and-source', {
        sourceIdentity: `Observation/${sourceRecordID}`, code: absence.code, system: absence.system, display: absence.display,
      }, {
        sourceIdentity: projected.sourceIdentity, code: projected.absenceCode, system: projected.absenceSystem,
        display: projected.absenceDisplay,
      });
      const actualPresenceCases = controlPlan.observation.presenceCases.map((item) => {
        const sourceRow = previewResponse.responseJSON.rows.find((candidate) => candidate?.[idColumn.column] === item.sourceRecordId);
        const column = authoredObservation.columns.find((candidate) => sourcePathFor(candidate) === item.fieldPath);
        return {
          sourceRecordId: item.sourceRecordId,
          presence: item.presence,
          value: sourceRow && column && Object.hasOwn(sourceRow, column.column) ? sourceRow[column.column] : null,
        };
      });
      recordAssertion(report, 'j04-preview-preserves-missing-false-zero-and-whitespace-blank-source-values',
        controlPlan.observation.presenceCases.map((item) => ({
          sourceRecordId: item.sourceRecordId, presence: item.presence, value: item.presence === 'missing' ? null : item.value,
        })), actualPresenceCases);
      recordAssertion(report, 'j04-recorded-absence-code-is-visible-in-preview', absence.code,
        visibleRow?.[authoredObservation.columns.find((column) => sourcePathFor(column) === absence.codingPath)?.label] ?? null);
      if (completeTrace) {
        recordAssertion(report, 'j04-recorded-absence-cell-trace-pins-source-observation', [`Observation/${sourceRecordID}`], traceSources);
      } else {
        report.assertions.push({ name: 'j04-recorded-absence-cell-trace-pins-source-observation', status: 'not-proven', expected: [`Observation/${sourceRecordID}`], actual: traceAttempts, detail: 'The receipt-bound code-cell trace did not return complete source identities.' });
      }
      await captureDOM('j04-recorded-absence-visible-preview');
    });

    const openShapeSelector = '[data-testid="ui04-open-table-shape-settings"]';
    const openShapeVisible = await browserEval(cdp, `return Boolean([...document.querySelectorAll(${JSON.stringify(openShapeSelector)})].find(visible))`);
    if (!openShapeVisible) {
      const visibleShapeActions = await evaluate(cdp, `([...document.querySelectorAll('button')].map((button) => button.textContent.trim()).filter((label) => /shape|reshape|pivot|transform/i.test(label)))`);
      failAtMissingDOMAction('open-ui04-table-shape-settings', openShapeSelector,
        'The saved-table Builder route does not expose the UI04 Table shape settings action.', {
          visibleShapeActions,
          outputId,
          authoredColumns: authoredObservation.columns.length,
        });
    }
    await action('open-table-shape-settings-through-builder', async () => {
      await clickTestID('ui04-open-table-shape-settings');
      await waitForBrowser(cdp, `Boolean(document.querySelector('[data-testid="ui04-table-shape-dialog"] [data-testid="ui04-table-shape-editor"]'))`, 30000);
      await captureDOM('j04-table-shape-editor-open');
    });

    const sourceColumn = (path) => authoredObservation.columns.find((column) => sourcePathFor(column) === path);
    const requiredEditor = async (testId, actionName, reason) => {
      const visibleControl = await browserEval(cdp, `return Boolean([...document.querySelectorAll('[data-testid="${testId}"]')].find(visible))`);
      if (!visibleControl) failAtMissingDOMAction(actionName, `[data-testid="${testId}"]`, reason, { outputId });
    };
    const configureObservationPivot = async () => {
      await requiredEditor('ui04-table-shape-editor', 'configure-grouped-pivot', 'The route did not mount the standalone UI04 editor inside its dialog.');
      await requiredEditor('ui04-reshape-mode', 'select-grouped-pivot-mode', 'The grouped-pivot mode selector is absent from the visible editor.');
      await selectTestOption('ui04-reshape-mode', 'Grouped pivot');
      const groups = controlPlan.observation.pivot.groupColumns.map((group) => sourceColumn(group.path));
      if (groups.some((column) => !column)) throw new Error(`J04 grouped pivot is missing a browser-authored group column: ${JSON.stringify(controlPlan.observation.pivot.groupColumns)}`);
      for (const group of groups) {
        const found = await browserEval(cdp, `return Boolean([...document.querySelectorAll('input[type="checkbox"][data-testid^="ui04-pivot-group-columns-choice-"]')].find((input) => norm(input.closest('label')?.innerText).includes(${JSON.stringify(group.label)})))`);
        if (!found) failAtMissingDOMAction('select-pivot-group-column', `[data-testid^="ui04-pivot-group-columns-choice-"]`, `The visible group-column choices do not contain authored column ${group.label}.`, { sourcePath: sourcePathFor(group) });
        await checkChoiceByLabel('ui04-pivot-group-columns-choice-', group.label);
      }
      const categoryColumn = sourceColumn(controlPlan.observation.pivot.categoryColumn.path);
      const valueColumn = sourceColumn(controlPlan.observation.pivot.valueColumn.path);
      if (!categoryColumn || !valueColumn) throw new Error('J04 category/value columns are missing from the authored Observation table');
      await selectTestOption('ui04-pivot-category-column', categoryColumn.label);
      await selectTestOption('ui04-pivot-value-column', valueColumn.label);
      await requiredEditor('ui04-pivot-discover-categories', 'discover-pivot-categories', 'The server-backed category discovery action is absent.');
      await clickTestID('ui04-pivot-discover-categories');
      await waitForBrowser(cdp, `document.querySelector('[data-testid="ui04-pivot-category-discovery-state"]')?.innerText.includes('Discovered')`, 60000);
      const discovered = await browserEval(cdp, `return ([...document.querySelectorAll('[data-testid="ui04-pivot-frozen-categories"] > li')]).map((row, index) => ({ index: index + 1, testId: row.querySelector('input[type="checkbox"]')?.dataset.testid, text: norm(row.innerText) }))`);
      for (const category of controlPlan.observation.pivot.categories) {
        const found = discovered.find((item) => item.testId && item.text.split(/[\s(]/).includes(category.code));
        if (!found) failAtMissingDOMAction('select-discovered-pivot-category', '[data-testid="ui04-pivot-frozen-categories"] input[type="checkbox"]', `The server did not visibly discover fixture category ${category.code}.`, { discovered });
        await checkTestID(found.testId);
        await setTestInput(`ui04-pivot-category-output-${found.index}-column`, category.outputColumn);
        await setTestInput(`ui04-pivot-category-output-${found.index}-label`, category.outputColumn);
      }
      await selectTestOption('ui04-pivot-duplicate-policy', 'sum');
      await selectTestOption('ui04-pivot-missing-policy', 'null');
      await selectTestOption('ui04-pivot-unlisted-policy', 'exclude');
      await requiredEditor('ui04-add-derived-column', 'add-pivot-derived-column', 'The derived-column action is absent or unavailable for this compiler output.');
      await clickTestID('ui04-add-derived-column');
      await setTestInput('ui04-derived-output-1-column', controlPlan.observation.pivot.derivedColumns[0].name);
      await setTestInput('ui04-derived-output-1-label', controlPlan.observation.pivot.derivedColumns[0].name);
      await selectTestOption('ui04-derived-operator-1', 'add');
      await selectTestOption('ui04-derived-left-operand-1-source', 'pivot output');
      await selectTestOption('ui04-derived-left-operand-1-pivot-output', controlPlan.observation.pivot.categories[0].outputColumn);
      await selectTestOption('ui04-derived-right-operand-1-source', 'pivot output');
      await selectTestOption('ui04-derived-right-operand-1-pivot-output', controlPlan.observation.pivot.categories[1].outputColumn);
      await selectTestOption('ui04-derived-missing-input-policy-1', 'propagate');
      await captureDOM('j04-grouped-pivot-proposal-form');
    };
    const summarizeShapeNetwork = async (stage) => {
      await Promise.allSettled([...pendingNetworkBodies]);
      const recent = network.filter((item) => /table-shape|preview/i.test(item.path));
      const pickIdentity = (value) => {
        if (!value || typeof value !== 'object') return undefined;
        return Object.fromEntries(['proposalId', 'receiptId', 'resolutionId', 'catalogId', 'outputId', 'draftVersion', 'draftDigest', 'snapshotToken', 'status', 'reasonCode']
          .filter((key) => value[key] !== undefined).map((key) => [key, value[key]]));
      };
      report.target.proposalIdentities.push({
        stage,
        requests: recent.map((item) => ({ path: item.path, method: item.method, status: item.status,
          request: pickIdentity(item.requestJSON), response: pickIdentity(item.responseJSON),
          responseBodyError: item.responseBodyError })),
      });
      const latestComparison = [...recent].reverse().find((item) => item.responseJSON && /proposal|table-shape/i.test(item.path));
      return latestComparison?.responseJSON;
    };
    const requestProposal = async (stage) => {
      const visibleProposalError = async () => String(await evaluate(cdp,
        `document.querySelector('[data-testid="ui04-table-shape-error"]')?.innerText?.trim() ?? ''`));
      const beforeRequestError = await visibleProposalError();
      if (beforeRequestError) {
        report.target.tableShapeProposalFailure = { stage, source: 'ui04-table-shape-error', message: beforeRequestError };
        recordAssertion(report, `j04-${stage}-table-shape-proposal-has-no-visible-error`, '', beforeRequestError);
        throw new Error(`J04 table-shape editor is already showing a proposal failure: ${beforeRequestError}`);
      }
      await requiredEditor('ui04-preview-table-shape', 'request-table-shape-proposal', 'The editor does not expose its proposal preview action.');
      try {
        await clickTestID('ui04-preview-table-shape');
      } catch (error) {
        const currentError = await visibleProposalError();
        if (!currentError) throw error;
        report.target.tableShapeProposalFailure = { stage, source: 'ui04-table-shape-error', message: currentError };
        recordAssertion(report, `j04-${stage}-table-shape-proposal-has-no-visible-error`, '', currentError);
        throw new Error(`J04 table-shape proposal failed visibly: ${currentError}`);
      }
      const proposalDeadline = Date.now() + 60000;
      let comparisonVisible = false;
      let proposalError = '';
      while (Date.now() < proposalDeadline) {
        proposalError = await visibleProposalError();
        if (proposalError) break;
        comparisonVisible = await evaluate(cdp,
          `Boolean(document.querySelector('[data-testid="ui04-table-shape-comparison"]'))`);
        if (comparisonVisible) break;
        await sleep(100);
      }
      if (proposalError) {
        await captureDOM(`j04-${stage}-proposal-error`);
        await summarizeShapeNetwork(`${stage}-proposal-error`);
        report.target.tableShapeProposalFailure = { stage, source: 'ui04-table-shape-error', message: proposalError };
        recordAssertion(report, `j04-${stage}-table-shape-proposal-has-no-visible-error`, '', proposalError);
        throw new Error(`J04 table-shape proposal failed visibly: ${proposalError}`);
      }
      if (!comparisonVisible) {
        const currentError = await visibleProposalError();
        throw new Error(`J04 table-shape proposal produced neither a comparison nor a visible error before timeout${currentError ? `: ${currentError}` : ''}`);
      }
      await captureDOM(`j04-${stage}-comparison`);
      const text = await evaluate(cdp, `document.querySelector('[data-testid="ui04-table-shape-comparison"]')?.innerText ?? ''`);
      const details = await summarizeShapeNetwork(stage);
      const comparison = {
        text,
        baseRows: await evaluate(cdp, `document.querySelector('[data-testid="ui04-comparison-base-rows"]')?.innerText ?? ''`),
        candidateRows: await evaluate(cdp, `document.querySelector('[data-testid="ui04-comparison-candidate-rows"]')?.innerText ?? ''`),
        contributors: await evaluate(cdp, `document.querySelector('[data-testid="ui04-comparison-contributors"]')?.innerText ?? ''`),
        exclusions: await evaluate(cdp, `document.querySelector('[data-testid="ui04-comparison-exclusions"]')?.innerText ?? ''`),
        informationLoss: await evaluate(cdp, `document.querySelector('[data-testid="ui04-comparison-information-loss"]')?.innerText ?? ''`),
        response: details ?? null,
      };
      report.target[`${stage}Comparison`] = comparison;
      return comparison;
    };

    await action('preview-grouped-pivot-with-receipt-backed-comparison', async () => {
      const beforeProposal = await saveWorkspaceSnapshot('before-proposal', explorerId);
      const beforeDefinition = j04WorkspaceSnapshot(beforeProposal);
      await configureObservationPivot();
      const previewComparison = await requestProposal('cancelled');
      const proposalState = await saveWorkspaceSnapshot('after-proposal-preview', explorerId);
      recordAssertion(report, 'j04-preview-proposal-does-not-mutate-saved-workspace', beforeDefinition, j04WorkspaceSnapshot(proposalState));
      const comparison = previewComparison.response?.comparison;
      if (!comparison) throw new Error(`J04 proposal response omitted structured comparison evidence: ${JSON.stringify(previewComparison.response).slice(0, 1000)}`);
      const expectedContributorIDs = [...new Set(controlPlan.observation.pivot.expectedContributors.flatMap((item) => item.sourceRecordIds))].sort();
      const actualContributorIDs = [...new Set((comparison.contributors ?? []).map((item) => `${item.resourceType}/${item.resourceId}`))].sort();
      report.target.contributorEvidence = {
        expected: expectedContributorIDs.map((id) => `Observation/${id}`),
        sample: actualContributorIDs,
        sampled: comparison.contributorsSampled,
      };

      const expectedExclusions = controlPlan.observation.pivot.expectedExclusions.map((item) => ({
        sourceIdentity: { resourceType: 'Observation', resourceId: item.sourceRecordId },
        category: { present: true, value: item.categoryCode },
        categoryType: 'string',
        reason: item.reason,
      })).sort((left, right) => left.sourceIdentity.resourceId.localeCompare(right.sourceIdentity.resourceId));
      const actualExclusions = (comparison.exclusions?.records ?? []).map((item) => ({
        sourceIdentity: item.sourceIdentity ? { resourceType: item.sourceIdentity.resourceType, resourceId: item.sourceIdentity.resourceId } : null,
        category: item.category,
        categoryType: String(item.categoryType ?? '').toLocaleLowerCase(),
        reason: item.reason,
      })).sort((left, right) => String(left.sourceIdentity?.resourceId).localeCompare(String(right.sourceIdentity?.resourceId)));
      recordAssertion(report, 'j04-pivot-exclusion-evidence-is-complete-and-exact', {
        status: 'COMPLETE', complete: true, sampled: false, records: expectedExclusions,
      }, {
        status: comparison.exclusions?.status, complete: comparison.exclusions?.complete, sampled: comparison.exclusions?.sampled,
        records: actualExclusions,
      });
      report.target.exclusionEvidence = { status: comparison.exclusions?.status, complete: comparison.exclusions?.complete, records: actualExclusions };
      const excludedIdentities = new Set(actualExclusions.map((item) => `${item.sourceIdentity?.resourceType}/${item.sourceIdentity?.resourceId}`));
      const retainedPivotSourceIDs = fixture.sourceRecords
        .filter((record) => record.resourceType === controlPlan.observation.pivot.sourceResourceType)
        .map((record) => `${record.resourceType}/${record.id}`)
        .filter((identity) => !excludedIdentities.has(identity))
        .sort();
      recordAssertion(report, 'j04-complete-exclusion-partition-proves-exact-retained-pivot-sources',
        expectedContributorIDs.map((id) => `Observation/${id}`), retainedPivotSourceIDs);

      const groupPaths = new Set(controlPlan.observation.pivot.groupColumns.map((column) => column.path));
      const expectedDroppedColumns = authoredObservation.columns
        .filter((column) => !groupPaths.has(sourcePathFor(column)))
        .map((column) => column.column)
        .sort();
      const informationLoss = (comparison.declaredInformationLoss?.items ?? []).find((item) => item.code === 'GROUPED_PIVOT_DROPS_NON_GROUP_OUTPUT_COLUMNS');
      const actualDroppedColumns = [...(informationLoss?.affectedColumns ?? [])].sort();
      recordAssertion(report, 'j04-grouped-pivot-information-loss-names-every-dropped-column', expectedDroppedColumns, actualDroppedColumns);
      report.target.informationLossEvidence = {
        status: comparison.declaredInformationLoss?.status,
        code: informationLoss?.code,
        affectedColumns: actualDroppedColumns,
        droppedSourcePaths: authoredObservation.columns.filter((column) => actualDroppedColumns.includes(column.column)).map(sourcePathFor).sort(),
      };
      await requiredEditor('ui04-cancel-table-shape-proposal', 'cancel-reviewed-table-shape-proposal', 'The receipt-backed comparison does not expose the explicit proposal-cancel action.');
      await clickTestID('ui04-cancel-table-shape-proposal');
      await waitForBrowser(cdp, `!document.querySelector('[data-testid="ui04-table-shape-comparison"]')`, 30000);
      const afterCancel = await saveWorkspaceSnapshot('after-cancelled-proposal', explorerId);
      recordAssertion(report, 'j04-cancelling-reviewed-proposal-preserves-saved-definition', beforeDefinition, j04WorkspaceSnapshot(afterCancel));
      if (!await evaluate(cdp, `Boolean(document.querySelector('[data-testid="ui04-table-shape-editor"]'))`)) {
        await clickTestID('ui04-open-table-shape-settings');
        await waitForBrowser(cdp, `Boolean(document.querySelector('[data-testid="ui04-table-shape-dialog"] [data-testid="ui04-table-shape-editor"]'))`, 30000);
      }
      await configureObservationPivot();
      const applyComparison = await requestProposal('applied');
      const applyComparisonPath = await captureScreenshot('j04-table-shape-receipt-comparison');
      report.target.appliedComparisonScreenshot = applyComparisonPath;
      await requiredEditor('ui04-confirm-table-shape', 'confirm-receipt-backed-table-shape', 'The comparison does not expose the receipt-backed confirmation action.');
      await clickTestID('ui04-confirm-table-shape');
      await waitForBrowser(cdp, `!document.querySelector('[data-testid="ui04-table-shape-comparison"]')`, 60000);
      const appliedStarted = Date.now();
      let appliedState;
      let savedShape;
      while (Date.now() - appliedStarted < 60000) {
        appliedState = await fetchBuilderState(target, explorerId);
        savedShape = appliedState.workspace?.documents?.find((document) => document.output?.id === outputId)?.tableShape;
        if (savedShape?.reshape?.kind === 'PIVOT' && savedShape.derived?.length) break;
        await sleep(200);
      }
      if (!savedShape?.reshape?.pivot || !savedShape.derived?.length) throw new Error(`J04 receipt confirmation did not persist a grouped pivot and derived output: ${JSON.stringify(savedShape)}`);
      report.target.appliedTableShape = savedShape;
      recordAssertion(report, 'j04-confirm-applies-only-the-reviewed-receipt-backed-shape', {
        mode: 'PIVOT',
        outputs: [
          ...controlPlan.observation.pivot.categories.map((category) => category.outputColumn),
          ...controlPlan.observation.pivot.derivedColumns.map((column) => column.name),
        ].sort(),
      }, {
        mode: savedShape.reshape.kind,
        outputs: [...(savedShape.reshape.pivot?.categories ?? []).map((category) => category.output?.column), ...(savedShape.derived ?? []).map((column) => column.output?.column)].sort(),
      });
      const appliedSnapshot = await saveWorkspaceSnapshot('after-applied-shape', explorerId);
      await captureDOM('j04-table-shape-applied');
      const staleProposalID = previewComparison.response?.proposalId;
      if (!staleProposalID) throw new Error('J04 first reviewed proposal did not return an immutable proposal identity for the stale-apply regression');
      const staleApply = await requestJSON(`${bootstrapAuthoringURL(target, explorerId)}/commands`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        timeout: 30000,
        body: JSON.stringify({
          commandId: `loom-dev-j04-stale-${randomUUID()}`,
          semanticsVersion: beforeProposal.workspace.semanticsVersion,
          snapshotToken: beforeProposal.catalog.snapshotToken,
          expectedDraftVersion: beforeProposal.draftVersion,
          expectedDraftDigest: beforeProposal.draftDigest,
          commands: [{ type: 'APPLY_TABLE_SHAPE_PROPOSAL', outputId, proposalId: staleProposalID }],
        }),
      });
      const afterStaleApply = await fetchBuilderState(target, explorerId);
      recordAssertion(report, 'j04-stale-proposal-application-is-rejected-with-conflict', 409, staleApply.response.status);
      recordAssertion(report, 'j04-stale-proposal-application-does-not-mutate-draft', true,
        j04ExactEqual(j04WorkspaceSnapshot(appliedSnapshot), j04WorkspaceSnapshot(afterStaleApply)));
      report.target.staleProposalApplication = {
        proposalId: staleProposalID,
        status: staleApply.response.status,
        response: staleApply.value,
        workspaceUnchanged: j04ExactEqual(j04WorkspaceSnapshot(appliedSnapshot), j04WorkspaceSnapshot(afterStaleApply)),
      };
      if (applyComparison) report.target.appliedProposalResponse = applyComparison.response ?? null;

      await action('reload-builder-and-prove-table-shape-persisted', async () => {
        await navigate(cdp, builderURL);
        await waitForBrowser(cdp, `document.body.innerText.includes('J04 measurements') && Boolean(document.querySelector('[data-testid="ui04-open-table-shape-settings"]')) && !document.body.innerText.includes('EXPLORER_AUTHORING_FAILED')`, 60000);
        const reloadedState = await fetchBuilderState(target, explorerId);
        const reloadedDocument = reloadedState.workspace?.documents?.find((document) => document.output?.id === outputId);
        if (!reloadedDocument) throw new Error('J04 Builder reload did not preserve the Observation output identity');
        recordAssertion(report, 'j04-reload-preserves-applied-table-shape-and-column-identities', {
          tableShape: savedShape,
          columns: appliedState.workspace.documents.find((document) => document.output?.id === outputId)?.columns.map((column) => column.column),
        }, {
          tableShape: reloadedDocument.tableShape,
          columns: reloadedDocument.columns.map((column) => column.column),
        });
        report.target.reloadedTableShape = reloadedDocument.tableShape;
        await captureDOM('j04-builder-after-shape-reload');
      });

      const previewStart = network.length;
      await action('preview-applied-grouped-observation-table', async () => {
        await requiredEditor('ui04-open-table-shape-settings', 'reload-saved-shape-boundary', 'The Builder route did not render after loading the saved tableShape document.');
        await browserEval(cdp, `clickButton('Preview')`);
        await waitForBrowser(cdp, `Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))`, 60000);
        const previewDeadline = Date.now() + 30000;
        let previewRequest;
        while (Date.now() < previewDeadline) {
          await Promise.allSettled([...pendingNetworkBodies]);
          previewRequest = network.slice(previewStart).find((item) => item.path.endsWith('/preview') && item.status === 200 && item.responseJSON?.rows);
          if (previewRequest) break;
          await sleep(50);
        }
        if (!previewRequest) throw new Error(`J04 grouped Observation Preview response was unavailable: ${JSON.stringify(network.slice(previewStart).filter((item) => item.path.endsWith('/preview')).map(({ status, responseJSON, responseBodyError }) => ({ status, responseJSON, responseBodyError }))).slice(0, 1000)}`);

        const reloadedDocument = (await fetchBuilderState(target, explorerId)).workspace?.documents?.find((document) => document.output?.id === outputId);
        const pivot = reloadedDocument?.tableShape?.reshape?.pivot;
        const outputColumns = reloadedDocument?.columns ?? [];
        if (!pivot || !Array.isArray(pivot.groupKeys) || !Array.isArray(pivot.categories)) throw new Error('J04 persisted table shape omitted its grouped pivot details');
        const groups = controlPlan.observation.pivot.groupColumns.map((group) => {
          const column = outputColumns.find((candidate) => candidate.column === group.path || candidate.label === group.path || sourcePathFor(candidate) === group.path);
          if (!column || !pivot.groupKeys.includes(column.column)) throw new Error(`J04 persisted pivot omitted group source ${group.path}`);
          return { path: group.path, column };
        });
        const categoryOutputs = pivot.categories.map((category) => category.output?.column);
        const derivedOutput = reloadedDocument.tableShape.derived?.[0]?.output?.column;
        const expectedOutputIDs = [...groups.map(({ column }) => column.column), ...categoryOutputs, derivedOutput];
        if (expectedOutputIDs.some((column) => typeof column !== 'string' || !column)) throw new Error(`J04 persisted pivot output identities are incomplete: ${JSON.stringify(expectedOutputIDs)}`);
        const preview = previewRequest.responseJSON;
        const actualOutputIDs = preview.columns.map((column) => column.column);
        recordAssertion(report, 'j04-preview-schema-retains-group-keys-frozen-outputs-and-derived-output', expectedOutputIDs, actualOutputIDs);

        const previewSurface = normalizeJ04Surface({
          columns: preview.columns.map((column) => ({ ...column })),
          rows: preview.rows,
          identityColumns: groups.map(({ column }) => column.column),
        });
        const expectedRows = controlPlan.observation.pivot.expectedRows.map((expected) => {
          const values = Object.fromEntries(groups.map(({ path, column }) => [column.column, expected.groupValues[path]]));
          for (const category of controlPlan.observation.pivot.categories) values[category.outputColumn] = expected.values[category.outputColumn];
          for (const derived of controlPlan.observation.pivot.derivedColumns) values[derived.name] = expected.values[derived.name];
          return { rowId: JSON.stringify(groups.map(({ path }) => expected.groupValues[path])), values };
        }).sort((left, right) => left.rowId.localeCompare(right.rowId));
        const expectedSurface = { columns: previewSurface.columns, rows: expectedRows };
        const previewComparison = compareJ04Evidence(expectedSurface, previewSurface);
        recordAssertion(report, 'j04-applied-pivot-preview-matches-literal-grouped-rows', true, previewComparison.matches);
        recordAssertion(report, 'j04-pivot-preview-preserves-native-numeric-and-null-values', true,
          previewSurface.rows.every((row) => typeof row.values[categoryOutputs[0]] === 'number'
            && (row.values[categoryOutputs[1]] === null || typeof row.values[categoryOutputs[1]] === 'number')
            && (row.values[derivedOutput] === null || typeof row.values[derivedOutput] === 'number')));
        const unitColumns = preview.columns.filter((column) => categoryOutputs.includes(column.column) || column.column === derivedOutput);
        report.target.pivotResultUnits = unitColumns.map((column) => ({
          column: column.column,
          label: column.label,
          logicalType: column.logicalType,
          resultUnit: column.resultUnit ?? null,
        }));
        recordAssertion(report, 'j04-pivot-preview-does-not-invent-unconfigured-unit-metadata', true,
          unitColumns.length === 3 && unitColumns.every((column) => column.resultUnit === undefined));
        report.target.previewRows = previewSurface;
        report.target.expectedObservationSurface = expectedSurface;
        report.target.previewReceiptId = preview.receiptId;
        await captureDOM('j04-grouped-observation-preview');
      });

      const publishStart = network.length;
      await action('publish-observation-table-through-visible-builder', async () => {
        await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Publish' && !button.disabled))`, 60000);
        await browserEval(cdp, `clickButton('Publish')`);
        const publishDeadline = Date.now() + 90000;
        let publishedState;
        let runtime;
        while (Date.now() < publishDeadline) {
          await Promise.allSettled([...pendingNetworkBodies]);
          try {
            publishedState = await fetchExplorerState(target, explorerId);
            runtime = publishedState.runtime ?? publishedState;
            if (runtime?.outputs?.some((output) => output.outputId === outputId) && (runtime.publication?.revisionId || publishedState.active?.revisionId)) break;
          } catch { /* Publish may still be materializing. */ }
          await sleep(300);
        }
        const publishResponse = network.slice(publishStart).find((item) => item.path.endsWith('/publish'));
        if (!publishResponse || publishResponse.status !== 200) throw new Error(`J04 visible Publish did not return HTTP 200: ${JSON.stringify(publishResponse ?? network.slice(publishStart).map(({ path, status }) => ({ path, status })))}`);
        if (!runtime?.outputs?.some((output) => output.outputId === outputId)) throw new Error('J04 published Observation output did not become readable in runtime state');
        const output = runtime.outputs.find((candidate) => candidate.outputId === outputId);
        report.target.publication = runtime.publication ?? publishedState.active;
        report.target.runtimeColumns = output.columns;
        recordAssertion(report, 'j04-published-output-retains-the-preview-schema',
          report.target.expectedObservationSurface.columns.map(({ id, name, logicalType }) => ({ id, name, logicalType })),
          output.columns.map((column) => ({ id: column.column, name: column.label, logicalType: column.logicalType })));
        recordAssertion(report, 'j04-published-output-retains-result-units',
          report.target.expectedObservationSurface.columns.filter((column) => column.resultUnit).map((column) => ({ id: column.id, resultUnit: column.resultUnit })),
          output.columns.filter((column) => column.resultUnit).map((column) => ({ id: column.column, resultUnit: column.resultUnit })));
        report.target.runtimeOutput = output;
        await captureDOM('j04-observation-published');
      });

      await action('open-observation-output-in-visible-viewer', async () => {
        await browserEval(cdp, `clickButton('Viewer')`);
        await waitForBrowser(cdp, `new URL(window.location.href).searchParams.get('mode') === 'viewer' && document.querySelector('table[aria-label="J04 measurements results"] tbody')?.querySelectorAll('tr').length === 2`, 60000);
        const viewer = await evaluate(cdp, `(() => {
          const table = document.querySelector('table[aria-label="J04 measurements results"]');
          return {
            headers: [...(table?.querySelectorAll('thead th') || [])].map((cell) => cell.textContent.trim()),
            rows: [...(table?.querySelectorAll('tbody tr') || [])].map((row) => [...row.querySelectorAll('td')].map((cell) => cell.textContent.trim())),
          };
        })()`);
        const runtimeColumns = report.target.runtimeOutput.columns;
        const expectedHeaders = runtimeColumns.map((column) => `${column.label}${column.resultUnit?.code ? ` (${column.resultUnit.code})` : ''}`);
        recordAssertion(report, 'j04-viewer-renders-stable-typed-grouped-schema', expectedHeaders, viewer.headers);
        const expectedRows = report.target.expectedObservationSurface.rows.map((row) => runtimeColumns.map((column) => {
          const value = row.values[column.column];
          return value === null || value === undefined ? '—' : String(value);
        }));
        recordAssertion(report, 'j04-viewer-renders-literal-preview-grouped-rows', expectedRows, viewer.rows);
        report.target.viewerRows = viewer;
        report.target.viewerMode = await evaluate(cdp, `new URL(window.location.href).searchParams.get('mode')`);
        await captureDOM('j04-observation-viewer');
      });

      await action('download-and-compare-typed-observation-artifact', async () => {
        await browserEval(cdp, `clickButton('Download dataset')`);
        await waitForBrowser(cdp, `Boolean([...document.querySelectorAll('[role="dialog"]')].find((dialog) => dialog.innerText.includes('Download dataset') && dialog.querySelector('[aria-label="Declared output types"]')))`, 60000);
        const modal = await evaluate(cdp, `(() => {
          const dialog = [...document.querySelectorAll('[role="dialog"]')].find((candidate) => candidate.innerText.includes('Download dataset') && candidate.querySelector('[aria-label="Declared output types"]'));
          const value = (label) => [...(dialog?.querySelectorAll('dt') || [])].find((term) => term.textContent.trim() === label)?.nextElementSibling?.textContent.trim() ?? '';
          return { text: dialog?.innerText ?? '', representation: value('Representation'), schemaDigest: value('Schema digest'), types: dialog?.querySelector('[aria-label="Declared output types"]')?.innerText ?? '' };
        })()`);
        await captureDOM('j04-observation-artifact-download-modal');
        await browserEval(cdp, `const link = [...document.querySelectorAll('a[download]')].find((candidate) => norm(candidate.textContent) === 'Download ZIP'); if (!link) throw new Error('J04 typed artifact modal has no Download ZIP link'); link.click();`);
        const archivePath = await findDownloadedArchive(downloadDirectory, 60000);
        recordEvidence(report, archivePath);
        const artifact = inspectJ05ArtifactPackage(readStoredZip(archivePath));
        const expectedIdentity = {
          project: canonicalProjectID(target.fixtureProject),
          datasetGeneration: target.fixtureGeneration,
          outputId,
          revisionId: report.target.publication?.revisionId,
          schemaDigest: modal.schemaDigest,
        };
        assertJ05ArtifactIdentity(artifact, expectedIdentity);
        recordAssertion(report, 'j04-artifact-is-an-explicitly-typed-native-jsonl-package', 'JSONL', artifact.manifest.format);
        const artifactSurface = normalizeJ04Surface({
          columns: artifact.schema.columns.map((column) => ({
            id: column.outputKey,
            name: column.label ?? column.name,
            rowKey: column.name,
            logicalType: column.logicalType,
            ...(column.resultUnit ? { resultUnit: column.resultUnit } : {}),
            ...(column.shape ? { shape: column.shape } : {}),
            ...(column.nullable !== undefined ? { nullable: column.nullable } : {}),
            ...(column.repeated !== undefined ? { repeated: column.repeated } : {}),
            ...(column.authoredColumns ? { authoredColumns: column.authoredColumns } : {}),
          })),
          rows: artifact.rows.map((row) => row.values),
          identityColumns: report.target.expectedObservationSurface.columns.slice(0, 2).map((column) => column.id),
        });
        const previewSurface = report.target.previewRows;
        recordAssertion(report, 'j04-artifact-schema-matches-preview-stable-ids-types-and-result-units', previewSurface.columns, artifactSurface.columns);
        recordAssertion(report, 'j04-artifact-rows-match-preview-and-viewer-native-values', previewSurface.rows, artifactSurface.rows);
        recordAssertion(report, 'j04-artifact-does-not-invent-unconfigured-pivot-unit-metadata', [],
          artifactSurface.columns.filter((column) => column.resultUnit).map((column) => ({ id: column.id, resultUnit: column.resultUnit })));
        report.target.downloadedTypedArtifact = {
          path: archivePath,
          format: artifact.manifest.format,
          schema: artifactSurface.columns,
          rows: artifactSurface.rows,
          representation: modal.representation,
          expectedIdentity,
        };
      });
    });
  } catch (error) {
    failure = error;
    report.status = 'failed';
    report.error = error instanceof Error ? error.message : String(error);
    if (report.target.firstMissingDOMAction) markJ04DownstreamUnproven(report);
    throw error;
  } finally {
    if (cdp) {
      try { await captureDOM('j04-failure-dom'); } catch {}
      try { report.target.failureScreenshot = await captureScreenshot('j04-failure'); } catch {}
      try {
        const explorerId = report.target.explorerId ?? report.target.bootstrapExplorerId;
        if (explorerId) await saveWorkspaceSnapshot('after', explorerId);
      } catch (error) {
        report.target.afterWorkspaceError = String(error);
      }
      try { await browser.close(); } catch {}
    }
    report.target.networkSummary = network.map((item) => ({ path: item.path, method: item.method, status: item.status }));
    const networkPath = join(evidenceDirectory, 'network-summary.json');
    writeJSON(networkPath, report.target.networkSummary);
    recordEvidence(report, networkPath);
    report.timings.j04_browser_journey_ms = Date.now() - started;
    if (failure && !report.target.firstMissingDOMAction) markJ04DownstreamUnproven(report);
    const evidenceDoc = join(evidenceDirectory, 'J04-evidence.md');
    writeFileSync(evidenceDoc, j04EvidenceDocument(report), { mode: 0o600 });
    recordEvidence(report, evidenceDoc);
    writeJSON(join(evidenceDirectory, 'report.json'), report);
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
      : command === 'verify-j03' ? 'S03-J03-row-definition-settings-preview-stale-apply-persistence'
            : command === 'verify-j04' || command === 'verify-j04-patient' ? 'S04-J04-values-time-shape-typed-pivot-derived'
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
    if (command === 'verify-j03') {
      await ensureDev(target, report);
      const j03FixtureDirectory = createJ03ThreeMemberFixture(target.fixtureDir);
      try {
        const verificationTarget = Object.freeze({
          ...createVerificationTarget(target, `${Date.now().toString(36)}-${randomUUID().slice(0, 8)}`),
          fixtureDir: j03FixtureDirectory,
        });
        const verificationReport = createVerificationReport(verificationTarget, 'S03-J03-row-definition-settings-preview-stale-apply-persistence');
        activeReport = verificationReport;
        verificationReport.timings.startup_ms = report.timings.startup_ms;
        verificationReport.timings.api_build_barrier_ms = report.timings.api_build_barrier_ms;
        const seed = await seedFixture(verificationTarget, { requireFresh: true, populateBootstrap: true });
        if (seed.reused || !seed.fresh || !seed.bootstrapExplorerId) throw new Error(`J03 verification fixture was not freshly seeded: ${verificationTarget.fixtureProject}`);
        verificationReport.target.fixtureSeed = 'seeded';
        verificationReport.target.bootstrapExplorerId = seed.bootstrapExplorerId;
        recordAssertion(verificationReport, 'j03-starts-with-fresh-isolated-fixture-and-bootstrap-table', true,
          seed.fresh && !seed.reused && Boolean(seed.bootstrapExplorerId) && Boolean(seed.bootstrapWorkspace?.workspace?.documents?.length));
        verificationReport.target.explicitGroupFixture = await seedJ03ExplicitGroupRevision(verificationTarget, seed.bootstrapExplorerId);
        recordAssertion(verificationReport, 'j03-starts-from-existing-three-member-selection', {
          complete: true, memberCount: 3,
        }, {
          complete: verificationReport.target.explicitGroupFixture.selection.complete,
          memberCount: verificationReport.target.explicitGroupFixture.selection.memberCount,
        });
        await verifyJ03BrowserScenario(verificationTarget, verificationReport, target);
        verificationReport.status = 'passed';
        verificationReport.timings.total_ms = Date.now() - commandStarted;
        writeJSON(join(verificationReport.target.evidenceDirectory, 'report.json'), verificationReport);
        writeJSON(join(verificationTarget.artifacts, 'report.json'), verificationReport);
        console.log(`DEV_J03_VERIFY_PASSED project=${verificationTarget.fixtureProject} evidence=${verificationReport.target.evidenceDirectory}`);
      } finally {
        rmSync(j03FixtureDirectory, { recursive: true, force: true });
      }
      return;
    }
    if (command === 'verify-j04' || command === 'verify-j04-patient') {
      const runID = `${Date.now().toString(36)}-${randomUUID().slice(0, 8)}`;
      const verificationTarget = createVerificationTarget(target, runID);
      const verificationReport = createVerificationReport(verificationTarget, 'S04-J04-values-time-shape-typed-pivot-derived');
      activeReport = verificationReport;
      verificationReport.timings.startup_ms = report.timings.startup_ms;
      verificationReport.timings.api_build_barrier_ms = report.timings.api_build_barrier_ms;
      verificationReport.target.evidenceDirectory = join(verificationTarget.artifacts, runID);
      mkdirSync(verificationReport.target.evidenceDirectory, { recursive: true, mode: 0o700 });
      recordEvidence(verificationReport, verificationReport.target.evidenceDirectory);
      writeJSON(join(verificationReport.target.evidenceDirectory, 'report.json'), verificationReport);

      const fixture = loadJ04FixtureContract(verificationTarget.fixtureDir);
      verificationReport.target.fixtureContractSummary = {
        sourceFile: fixture.contract.sourceFile,
        sourceRecords: fixture.contract.sourceRecords,
        baseRowResourceType: fixture.contract.baseRowResourceType,
        baseColumns: fixture.contract.baseColumns,
        aggregateRowResourceType: fixture.contract.aggregateScope.rowResourceType,
        aggregatePopulation: fixture.contract.aggregateScope.selectedRowIdentities,
        expectedAggregateRows: fixture.contract.expectedAggregates.length,
        unsupportedUnitRefusal: fixture.contract.unsupportedUnitRefusal,
        normalizationCases: fixture.contract.normalizationCases.length,
        pivotSourceColumns: fixture.contract.pivot.requiredSourceColumns,
        pivotCategories: fixture.contract.pivot.categories,
        pivotDerivedColumns: fixture.contract.pivot.derivedColumns,
      };
      await ensureDev(target, report);
      verificationReport.timings.startup_ms = report.timings.startup_ms;
      verificationReport.timings.api_build_barrier_ms = report.timings.api_build_barrier_ms;
      const seed = await seedFixture(verificationTarget, {
        requireFresh: true,
        populateBootstrap: false,
        fixtureManifest: j04FixtureManifest(verificationTarget.fixtureDir, fixture),
      });
      if (seed.reused || !seed.fresh || !seed.bootstrapExplorerId) throw new Error(`J04 fixture project was not freshly seeded with an empty Explorer: ${verificationTarget.fixtureProject}`);
      verificationReport.target.fixtureSeed = 'seeded';
      verificationReport.target.bootstrapExplorerId = seed.bootstrapExplorerId;
      recordAssertion(verificationReport, 'j04-seeds-a-fresh-isolated-project-with-an-empty-builder', true,
        seed.fresh && !seed.reused && Boolean(seed.bootstrapExplorerId) && !seed.bootstrapWorkspace?.workspace?.documents?.length);
      await verifyJ04PatientOperatorScenario(verificationTarget, verificationReport, target, fixture);
      if (command === 'verify-j04-patient') {
        const failures = verificationReport.assertions.filter((assertion) => assertion.status === 'failed');
        const unproven = verificationReport.assertions.filter((assertion) => assertion.status === 'not-proven');
        if (failures.length || unproven.length || verificationReport.limitations.length) {
          throw new Error(`J04 Patient operator acceptance incomplete: ${failures.length} literal assertion mismatch(es), ${unproven.length} unproven assertion(s), ${verificationReport.limitations.length} limitation(s)`);
        }
        verificationReport.status = 'passed';
        verificationReport.timings.total_ms = Date.now() - commandStarted;
        writeJSON(join(verificationReport.target.evidenceDirectory, 'report.json'), verificationReport);
        writeJSON(join(verificationTarget.artifacts, 'report.json'), verificationReport);
        console.log(`DEV_J04_PATIENT_VERIFY_PASSED project=${verificationTarget.fixtureProject} evidence=${verificationReport.target.evidenceDirectory}`);
        return;
      }
      await verifyJ04BrowserScenario(verificationTarget, verificationReport, target, fixture);
      verificationReport.status = 'passed';
      verificationReport.timings.total_ms = Date.now() - commandStarted;
      writeFileSync(join(verificationReport.target.evidenceDirectory, 'J04-evidence.md'), j04EvidenceDocument(verificationReport), { mode: 0o600 });
      writeJSON(join(verificationReport.target.evidenceDirectory, 'report.json'), verificationReport);
      writeJSON(join(verificationTarget.artifacts, 'report.json'), verificationReport);
      console.log(`DEV_J04_VERIFY_PASSED project=${verificationTarget.fixtureProject} evidence=${verificationReport.target.evidenceDirectory}`);
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
    throw new Error(`unknown command ${command}; use dev, dev-doctor, verify-current, verify-fast, verify-full, verify-j01, verify-j02, verify-j03, verify-j04, verify-j04-patient, verify-j05, dev-rebuild, or dev-down [--purge]`);
  } catch (error) {
    activeReport.status = 'failed';
    activeReport.error = error instanceof Error ? error.message : String(error);
    if (activeReport.scenario === 'S04-J04-values-time-shape-typed-pivot-derived' && activeReport.target.evidenceDirectory) {
      markJ04DownstreamUnproven(activeReport);
      const evidenceDoc = join(activeReport.target.evidenceDirectory, 'J04-evidence.md');
      try { writeFileSync(evidenceDoc, j04EvidenceDocument(activeReport), { mode: 0o600 }); } catch {}
      if (!activeReport.evidencePaths.includes(evidenceDoc)) recordEvidence(activeReport, evidenceDoc);
    }
    if (activeReport.target.evidenceDirectory) writeJSON(join(activeReport.target.evidenceDirectory, 'report.json'), activeReport);
    try { writeJSON(join(activeReport.target.artifacts ?? target.artifacts, 'report.json'), activeReport); } catch { /* Keep the original command error. */ }
    console.error(`DEV_VERIFY_FAILED ${activeReport.error}`);
    process.exitCode = 1;
  }
};

export { browserEval, evaluate, launchBrowser, navigate, snapshot, waitForBrowser };

if (!process.env.NODE_TEST_CONTEXT && import.meta.url === pathToFileURL(process.argv[1] ?? '').href) await main(process.argv.slice(2));
