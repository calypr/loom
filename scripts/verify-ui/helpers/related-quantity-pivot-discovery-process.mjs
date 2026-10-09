import { createHash } from 'node:crypto';
import { spawn, spawnSync } from 'node:child_process';
import { buildArangoShellInvocation, buildArangoShellStopInvocation, ARANGOSH_PROCESS_PATH_MARKER } from './owned-arangosh-command.mjs';
import {
  createRelatedQuantityPivotDiscoveryAccumulator,
  buildRelatedQuantityPivotPatientBatch,
  buildRelatedQuantityPivotSpecimenPage,
  buildRelatedQuantityPivotSpecimenPatientPairs,
} from './related-quantity-pivot-oracle.mjs';

export const RELATED_QUANTITY_DISCOVERY_MARKER = '__LOOM_RQ_DISCOVERY_V1__:';
const REMOTE_PID_MARKER = '__LOOM_RQ_DISCOVERY_PID__:';
const MAX_RECORD_BYTES = 8 * 1024 * 1024;
const MAX_TOTAL_BYTES = 512 * 1024 * 1024;
const MAX_STDERR_BYTES = 1024 * 1024;
const MAX_QUERY_COUNT = 200_000;
const MAX_UNIQUE_PATIENTS = 1_000_000;
const DEFAULT_DEADLINE_MS = 30 * 60 * 1000;
const QUERY_RUNTIME_SECONDS = 30;
const QUERY_MEMORY_LIMIT_BYTES = 268_435_456;
const DEFAULT_BOUNDS = Object.freeze({
  specimenPageSize: 100,
  maxSpecimenPatientRows: 10_000,
  maxPatientGroups: 25_000,
  patientBatchSize: 100,
});

const hash = value => createHash('sha256').update(value).digest('hex');
const scopeIdentityFor = scope => ({
  project: scope.project,
  generation: scope.dataset_generation,
  authScope: {
    auth_resource_paths_unrestricted: scope.auth_resource_paths_unrestricted,
    auth_resource_paths: [...scope.auth_resource_paths],
    scope_allowed: scope.scope_allowed,
  },
});
const jsonForScript = value => JSON.stringify(value).replaceAll('@', '\\u0040');

const validateBounds = bounds => {
  for (const name of ['specimenPageSize', 'maxSpecimenPatientRows', 'maxPatientGroups', 'patientBatchSize']) {
    if (!Number.isSafeInteger(bounds[name]) || bounds[name] < 1) throw new TypeError(`${name} must be a positive safe integer`);
  }
  if (bounds.specimenPageSize > 1000 || bounds.patientBatchSize > 100) throw new RangeError('Discovery page and batch sizes exceed their validated maxima');
  return bounds;
};

export function buildRelatedQuantityPivotDiscoveryProcess(scope, bounds, { deadlineMs = DEFAULT_DEADLINE_MS } = {}) {
  validateBounds(bounds);
  if (!Number.isSafeInteger(deadlineMs) || deadlineMs < 1000) throw new TypeError('Discovery overall deadline must be at least one second');
  const pageTemplate = buildRelatedQuantityPivotSpecimenPage(scope, { afterSpecimenKey: '', pageSize: bounds.specimenPageSize });
  const pairTemplate = buildRelatedQuantityPivotSpecimenPatientPairs(scope, ['Specimen/__template__'], { maxRows: bounds.maxSpecimenPatientRows });
  const patientTemplate = buildRelatedQuantityPivotPatientBatch(scope, ['Patient/__template__'], { maxRows: bounds.maxPatientGroups });
  const pairBindVars = { ...pairTemplate.bindVars };
  delete pairBindVars.specimen_ids;
  const patientBindVars = { ...patientTemplate.bindVars };
  delete patientBindVars.patient_ids;
  const scopeIdentity = scopeIdentityFor(scope);
  const manifest = {
    version: 1,
    scopeHash: hash(JSON.stringify(scopeIdentity)),
    queryHashes: {
      specimenPage: hash(pageTemplate.query),
      specimenPatientPairs: hash(pairTemplate.query),
      patientBatch: hash(patientTemplate.query),
    },
    bounds: { ...bounds },
    maxRecordBytes: MAX_RECORD_BYTES,
    maxTotalBytes: MAX_TOTAL_BYTES,
    maxStderrBytes: MAX_STDERR_BYTES,
    maxQueryCount: MAX_QUERY_COUNT,
    maxUniquePatients: MAX_UNIQUE_PATIENTS,
    deadlineMs,
    queryRuntimeSeconds: QUERY_RUNTIME_SECONDS,
    queryMemoryLimitBytes: QUERY_MEMORY_LIMIT_BYTES,
  };
  const marker = JSON.stringify(RELATED_QUANTITY_DISCOVERY_MARKER);
  const script = `const protocolMarker = ${marker};
const manifest = ${jsonForScript(manifest)};
const pageQuery = ${jsonForScript(pageTemplate.query)};
const pageBindVars = ${jsonForScript(pageTemplate.bindVars)};
const pairQuery = ${jsonForScript(pairTemplate.query)};
const pairBindVars = ${jsonForScript(pairBindVars)};
const patientQuery = ${jsonForScript(patientTemplate.query)};
const patientBindVars = ${jsonForScript(patientBindVars)};
let queryCount = 0;
let pageCount = 0;
let pairPageCount = 0;
let patientBatchCount = 0;
const patientIds = new Set();
let lastEmittedQuery = null;
const remoteStartedAt = Date.now();
const remoteDeadlineAt = remoteStartedAt + manifest.deadlineMs;
function assertBeforeDeadline(phase) {
  if (Date.now() >= remoteDeadlineAt) {
    const error = new Error('overall-deadline');
    error.discoveryCode = 'overall-deadline';
    error.discoveryPhase = phase;
    throw error;
  }
}
function emit(record) {
  const serialized = JSON.stringify(record);
  if (serialized.length > manifest.maxRecordBytes) {
    const error = new Error('record-too-large');
    error.discoveryCode = 'record-too-large';
    throw error;
  }
  print(protocolMarker + serialized);
}
function execute(kind, phase, query, bindVars) {
  const queryHash = manifest.queryHashes[kind];
  const index = queryCount;
  const startedAt = Date.now();
  try {
    assertBeforeDeadline(phase);
    if (queryCount >= manifest.maxQueryCount) {
      const error = new Error('max-query-count');
      error.discoveryCode = 'max-query-count';
      throw error;
    }
    let rows;
    rows = db._query(query, bindVars, {
      maxRuntime: manifest.queryRuntimeSeconds,
      memoryLimit: manifest.queryMemoryLimitBytes,
    }).toArray();
    const finishedAt = Date.now();
    const completedQuery = { kind, phase, queryHash, index, startedAt, finishedAt };
    if (!Array.isArray(rows) || rows.length !== 1 || !rows[0] || typeof rows[0] !== 'object' || Array.isArray(rows[0])) {
      const error = new Error('result-shape');
      error.discoveryCode = 'result-shape';
      throw error;
    }
    assertBeforeDeadline(phase);
    emit({ type: 'result', index, kind, phase, queryHash, startedAt, finishedAt, payload: rows[0] });
    lastEmittedQuery = completedQuery;
    queryCount += 1;
    return rows[0];
  } catch (error) {
    if (error && typeof error === 'object') {
      error.discoveryPhase = phase;
      error.discoveryQueryHash = queryHash;
      error.discoveryQueryIndex = index;
      error.discoveryStartedAt = startedAt;
      error.discoveryFinishedAt = Date.now();
      if (error.discoveryCode !== 'result-shape' && error.discoveryCode !== 'overall-deadline' && error.discoveryCode !== 'max-query-count') {
        error.discoveryCode = 'arango-query-error';
        error.errorNum = Number.isSafeInteger(error.errorNum) ? error.errorNum : null;
      }
    }
    throw error;
  }
}
try {
  emit({ type: 'begin', manifest });
  let afterSpecimenKey = '';
  let previousPageAdvertisedMore = false;
  let previousPageLastKey = '';
  while (true) {
    if (pageCount >= 100000) {
      const error = new Error('max-specimen-pages');
      error.discoveryCode = 'max-specimen-pages';
      error.discoveryPhase = 'specimen-page';
      throw error;
    }
    const page = execute('specimenPage', 'specimen-page', pageQuery, {
      ...pageBindVars, after_specimen_key: afterSpecimenKey,
    });
    pageCount += 1;
    if (!Array.isArray(page.specimens) || typeof page.hasMore !== 'boolean'
      || page.specimens.length > manifest.bounds.specimenPageSize
      || (page.hasMore && page.specimens.length !== manifest.bounds.specimenPageSize)
      || page.afterSpecimenKey !== afterSpecimenKey
      || (previousPageAdvertisedMore && page.specimens.length === 0)) {
      const error = new Error('specimen-page-shape');
      error.discoveryCode = 'specimen-page-shape';
      error.discoveryPhase = 'specimen-page';
      throw error;
    }
    let lastKey = afterSpecimenKey;
    for (const specimen of page.specimens) {
      if (!specimen || typeof specimen.specimenId !== 'string' || typeof specimen.specimenKey !== 'string'
        || specimen.specimenKey <= lastKey || specimen.specimenId.split('/').pop() !== specimen.specimenKey) {
        const error = new Error('specimen-cursor-shape');
        error.discoveryCode = 'specimen-cursor-shape';
        error.discoveryPhase = 'specimen-page';
        throw error;
      }
      lastKey = specimen.specimenKey;
    }
    if (page.hasMore && (!page.nextAfterSpecimenKey || page.nextAfterSpecimenKey !== lastKey)) {
      const error = new Error('specimen-cursor-stalled');
      error.discoveryCode = 'specimen-cursor-stalled';
      error.discoveryPhase = 'specimen-page';
      throw error;
    }
    if (!page.hasMore && page.nextAfterSpecimenKey !== null) {
      const error = new Error('specimen-final-cursor');
      error.discoveryCode = 'specimen-final-cursor';
      error.discoveryPhase = 'specimen-page';
      throw error;
    }
    previousPageAdvertisedMore = page.hasMore;
    previousPageLastKey = lastKey;
    if (page.specimens.length > 0) {
      const specimenIds = page.specimens.map(specimen => specimen.specimenId);
      const pairs = execute('specimenPatientPairs', 'specimen-patient-pairs', pairQuery, {
        ...pairBindVars, specimen_ids: specimenIds,
      });
      pairPageCount += 1;
      if (!Array.isArray(pairs.rows) || pairs.rows.length > manifest.bounds.maxSpecimenPatientRows
        || pairs.overflow !== false || pairs.truncated !== false
        || JSON.stringify(pairs.specimenIds) !== JSON.stringify(specimenIds)) {
        const error = new Error('specimen-pairs-shape');
        error.discoveryCode = 'specimen-pairs-shape';
        error.discoveryPhase = 'specimen-patient-pairs';
        throw error;
      }
      for (const row of pairs.rows) {
        if (!row || typeof row.specimenId !== 'string'
          || (row.patientId !== null && (typeof row.patientId !== 'string' || row.patientId.trim() === ''))) {
          const error = new Error('specimen-pair-row-shape');
          error.discoveryCode = 'specimen-pair-row-shape';
          error.discoveryPhase = 'specimen-patient-pairs';
          throw error;
        }
        if (row.patientId !== null) {
          patientIds.add(row.patientId);
          if (patientIds.size > manifest.maxUniquePatients) {
            const error = new Error('max-unique-patients');
            error.discoveryCode = 'max-unique-patients';
            error.discoveryPhase = 'specimen-patient-pairs';
            throw error;
          }
        }
      }
    }
    if (!page.hasMore) break;
    if (page.nextAfterSpecimenKey <= afterSpecimenKey || page.nextAfterSpecimenKey === previousPageLastKey && page.specimens.length === 0) {
      const error = new Error('specimen-cursor-stalled');
      error.discoveryCode = 'specimen-cursor-stalled';
      error.discoveryPhase = 'specimen-page';
      throw error;
    }
    afterSpecimenKey = page.nextAfterSpecimenKey;
  }
  const sortedPatientIds = Array.from(patientIds).sort();
  for (let offset = 0; offset < sortedPatientIds.length; offset += manifest.bounds.patientBatchSize) {
    if (patientBatchCount >= 100000) {
      const error = new Error('max-patient-batches');
      error.discoveryCode = 'max-patient-batches';
      error.discoveryPhase = 'patient-observation-groups';
      throw error;
    }
    const patientBatch = sortedPatientIds.slice(offset, offset + manifest.bounds.patientBatchSize);
    const batch = execute('patientBatch', 'patient-observation-groups', patientQuery, {
      ...patientBindVars, patient_ids: patientBatch,
    });
    patientBatchCount += 1;
    if (!Array.isArray(batch.groups) || batch.groups.length > manifest.bounds.maxPatientGroups
      || batch.overflow !== false || batch.truncated !== false
      || JSON.stringify(batch.patientIds) !== JSON.stringify(patientBatch)) {
      const error = new Error('patient-batch-shape');
      error.discoveryCode = 'patient-batch-shape';
      error.discoveryPhase = 'patient-observation-groups';
      throw error;
    }
  }
  emit({ type: 'done', queryCount, pageCount, pairPageCount, patientBatchCount, distinctPatientCount: patientIds.size });
} catch (error) {
  emit({ type: 'failure', code: typeof error.discoveryCode === 'string' ? error.discoveryCode : 'remote-script-error',
    phase: typeof error.discoveryPhase === 'string' ? error.discoveryPhase : null,
    queryHash: typeof error.discoveryQueryHash === 'string' ? error.discoveryQueryHash : null,
    queryIndex: Number.isSafeInteger(error.discoveryQueryIndex) ? error.discoveryQueryIndex : queryCount,
    startedAt: Number.isSafeInteger(error.discoveryStartedAt) ? error.discoveryStartedAt : null,
    finishedAt: Number.isSafeInteger(error.discoveryFinishedAt) ? error.discoveryFinishedAt : null,
    lastCompletedQuery: lastEmittedQuery,
    errorNum: Number.isSafeInteger(error.errorNum) ? error.errorNum : null });
}`.replaceAll('@', '\\u0040');
  return {
    script,
    manifest,
    queries: {
      specimenPage: pageTemplate.query,
      specimenPatientPairs: pairTemplate.query,
      patientBatch: patientTemplate.query,
    },
  };
}

const safeRemoteScriptPath = value => typeof value === 'string' && /^\/tmp\/loom-arangosh\.[A-Za-z0-9]+$/.test(value);
const parseJsonRecord = (line, marker) => {
  try { return JSON.parse(line.slice(marker.length)); } catch { throw new Error('Malformed related quantity discovery protocol record'); }
};

const remoteFailureDetails = record => ({
  code: typeof record?.code === 'string' ? record.code : 'remote-script-error',
  phase: typeof record?.phase === 'string' ? record.phase : null,
  queryHash: typeof record?.queryHash === 'string' ? record.queryHash : null,
  queryIndex: Number.isSafeInteger(record?.queryIndex) ? record.queryIndex : null,
  startedAt: Number.isSafeInteger(record?.startedAt) ? record.startedAt : null,
  finishedAt: Number.isSafeInteger(record?.finishedAt) ? record.finishedAt : null,
  lastCompletedQuery: record?.lastCompletedQuery && typeof record.lastCompletedQuery === 'object'
    ? { ...record.lastCompletedQuery } : null,
  errorNum: Number.isSafeInteger(record?.errorNum) ? record.errorNum : null,
});

const stopRemoteProcess = ({ container, processId, scriptPath, spawnSyncImpl }) => {
  const invocation = buildArangoShellStopInvocation({ container, pid: processId, scriptPath });
  const result = spawnSyncImpl(invocation.command, invocation.args, { encoding: 'utf8', timeout: 7000, maxBuffer: 32 * 1024 });
  const marker = String(result?.stdout ?? '').split('\n').find(line => line.startsWith('__LOOM_ARANGOSH_STOPPED__:'));
  const status = marker?.slice('__LOOM_ARANGOSH_STOPPED__:'.length);
  if (result?.status !== 0 || !['already-exited', 'term', 'kill'].includes(status)) {
    throw new Error('Could not verify termination of the exact remote Arangosh process');
  }
  return status;
};

export async function runRelatedQuantityPivotDiscoveryProcess({
  scope,
  bounds,
  container,
  database = 'loom_dev',
  onRecord,
  signal,
  deadlineMs = DEFAULT_DEADLINE_MS,
  spawnImpl = spawn,
  spawnSyncImpl = spawnSync,
  stopImpl = stopRemoteProcess,
  now = Date.now,
  transportCloseTimeoutMs = 1000,
}) {
  if (typeof onRecord !== 'function') throw new TypeError('A synchronous discovery record consumer is required');
  if (!Number.isSafeInteger(deadlineMs) || deadlineMs < 1000) throw new TypeError('Discovery overall deadline must be at least one second');
  if (typeof container !== 'string' || container.trim() === '') throw new TypeError('Owned Arango container is required');
  if (signal !== undefined && !(signal instanceof AbortSignal)) throw new TypeError('Discovery signal must be an AbortSignal');
  if (signal?.aborted) throw new Error('Related quantity discovery was aborted');
  if (!Number.isSafeInteger(transportCloseTimeoutMs) || transportCloseTimeoutMs < 1 || transportCloseTimeoutMs > 30_000) {
    throw new TypeError('Local transport close timeout must be between 1 and 30000 milliseconds');
  }
  const built = buildRelatedQuantityPivotDiscoveryProcess(scope, bounds, { deadlineMs });
  const invocation = buildArangoShellInvocation({ container, database, script: built.script, processIdMarker: REMOTE_PID_MARKER });
  const identity = {
    startedAt: now(), deadlineMs,
    scopeHash: built.manifest.scopeHash,
    queryHashes: built.manifest.queryHashes,
    queryRuntimeSeconds: QUERY_RUNTIME_SECONDS,
    queryMemoryLimitBytes: QUERY_MEMORY_LIMIT_BYTES,
    maxRecordBytes: MAX_RECORD_BYTES,
    maxTotalBytes: MAX_TOTAL_BYTES,
    status: 'running',
  };
  const child = spawnImpl(invocation.command, invocation.args, { stdio: ['ignore', 'pipe', 'pipe'] });
  let scriptPath = null;
  let processId = null;
  let pendingLineChunks = [];
  let pendingLineBytes = 0;
  let stdoutBytes = 0;
  let recordCount = 0;
  let protocolLineCount = 0;
  let lastQueryFinishedAt = null;
  let begun = false;
  let done = null;
  let remoteFailure = null;
  let failure = null;
  let stopPromise = null;
  let resolveUnverifiedStopCleanup;
  const unverifiedStopCleanup = new Promise(resolve => { resolveUnverifiedStopCleanup = resolve; });
  let stderrBytes = 0;
  let lastRecordAt = null;
  let closedStatus = null;
  let resolveDeadlineReached;
  const deadlineReached = new Promise(resolve => { resolveDeadlineReached = resolve; });
  const closeResult = new Promise(resolve => child.once('close', (code, closeSignal) => resolve({ code, signal: closeSignal })));
  let resolveIdentityReady;
  const identityReady = new Promise(resolve => { resolveIdentityReady = resolve; });
  const terminateLocalTransport = async () => {
    const requestTermination = signalName => {
      try {
        const requested = child.kill(signalName);
        identity.localTransportTermination = signalName;
        identity.localTransportTerminationRequested = requested !== false;
      } catch (error) {
        identity.localTransportTermination = signalName;
        identity.localTransportTerminationRequested = false;
        identity.localTransportTerminationError = String(error?.message ?? error).slice(0, 300);
      }
    };
    requestTermination('SIGTERM');
    let close = await Promise.race([
      closeResult.then(status => ({ status })),
      new Promise(resolve => setTimeout(() => resolve(null), transportCloseTimeoutMs)),
    ]);
    if (close) {
      identity.localTransportClosed = true;
      identity.localTransportExitCode = close.status.code;
      identity.localTransportSignal = close.status.signal;
      return;
    }
    requestTermination('SIGKILL');
    close = await Promise.race([
      closeResult.then(status => ({ status })),
      new Promise(resolve => setTimeout(() => resolve(null), transportCloseTimeoutMs)),
    ]);
    identity.localTransportClosed = Boolean(close);
    if (close) {
      identity.localTransportExitCode = close.status.code;
      identity.localTransportSignal = close.status.signal;
    }
  };
  const setFailure = error => {
    if (failure) return;
    failure = error instanceof Error ? error : new Error(String(error));
    failure.discoveryIdentity = identity;
    if (!stopPromise) {
      stopPromise = (async () => {
        if (!scriptPath) {
          await Promise.race([identityReady, new Promise(resolve => setTimeout(resolve, 1500))]);
        }
        if (scriptPath && safeRemoteScriptPath(scriptPath)) {
          try {
            identity.remoteStop = await stopImpl({ container, processId, scriptPath, spawnSyncImpl });
          } catch (stopError) {
            identity.remoteStop = 'unverified';
            identity.remoteStopError = String(stopError?.message ?? stopError).slice(0, 300);
            await terminateLocalTransport();
            identity.remoteStopRequiresNaturalDeadline = true;
            resolveUnverifiedStopCleanup();
          }
        }
        if (!scriptPath) {
          identity.remoteStop = 'no-arangosh-identity';
          child.kill('SIGTERM');
        } else if (identity.remoteStop === 'unverified') {
          identity.remoteStopRequiresNaturalDeadline = true;
        }
      })();
    }
  };
  const protocolFailure = message => setFailure(new Error(message));
  const handleProtocolRecord = record => {
    protocolLineCount += 1;
    identity.protocolLineCount = protocolLineCount;
    if (!record || typeof record !== 'object' || Array.isArray(record)) throw new Error('Protocol record must be an object');
    if (record.type === 'begin') {
      if (begun || record.manifest?.version !== 1
        || record.manifest.scopeHash !== built.manifest.scopeHash
        || JSON.stringify(record.manifest.queryHashes) !== JSON.stringify(built.manifest.queryHashes)
        || JSON.stringify(record.manifest.bounds) !== JSON.stringify(built.manifest.bounds)
        || record.manifest.deadlineMs !== deadlineMs
        || record.manifest.queryRuntimeSeconds !== QUERY_RUNTIME_SECONDS
        || record.manifest.queryMemoryLimitBytes !== QUERY_MEMORY_LIMIT_BYTES
        || record.manifest.maxRecordBytes !== MAX_RECORD_BYTES
        || record.manifest.maxTotalBytes !== MAX_TOTAL_BYTES
        || record.manifest.maxStderrBytes !== MAX_STDERR_BYTES
        || record.manifest.maxQueryCount !== MAX_QUERY_COUNT
        || record.manifest.maxUniquePatients !== MAX_UNIQUE_PATIENTS) throw new Error('Discovery begin record identity does not match the source query manifest');
      begun = true;
      return;
    }
    if (!begun) throw new Error('Discovery result arrived before its begin record');
    if (record.type === 'result') {
      if (done || remoteFailure) throw new Error('Discovery result arrived after the terminal protocol boundary');
      const expectedHash = built.manifest.queryHashes[{ specimenPage: 'specimenPage', specimenPatientPairs: 'specimenPatientPairs', patientBatch: 'patientBatch' }[record.kind]];
      const expectedPhase = { specimenPage: 'specimen-page', specimenPatientPairs: 'specimen-patient-pairs', patientBatch: 'patient-observation-groups' }[record.kind];
      if (record.index !== recordCount || !expectedHash || record.queryHash !== expectedHash || !record.payload || typeof record.payload !== 'object' || Array.isArray(record.payload)) {
        throw new Error('Discovery result has an unexpected phase, query identity, order, or payload');
      }
      if (record.phase !== expectedPhase) throw new Error('Discovery result phase does not match its query kind');
      if (!Number.isSafeInteger(record.startedAt) || !Number.isSafeInteger(record.finishedAt) || record.finishedAt < record.startedAt) {
        throw new Error('Discovery query result has invalid execution timestamps');
      }
      if (lastQueryFinishedAt !== null && record.startedAt < lastQueryFinishedAt) {
        throw new Error('Discovery query timestamps are out of order');
      }
      identity.queryRecords ??= [];
      identity.queryRecords.push({
        index: record.index,
        kind: record.kind,
        phase: record.phase,
        queryHash: record.queryHash,
        startedAt: record.startedAt,
        finishedAt: record.finishedAt,
        protocolLineCount,
      });
      onRecord(record);
      recordCount += 1;
      lastQueryFinishedAt = record.finishedAt;
      identity.queryCount = recordCount;
      identity.phase = record.phase;
      identity.queryStartedAt = record.startedAt;
      identity.queryFinishedAt = record.finishedAt;
      lastRecordAt = now();
      return;
    }
    if (record.type === 'failure') {
      if (done || remoteFailure) throw new Error('Discovery failure appeared outside the terminal protocol boundary');
      const hashByPhase = {
        'specimen-page': built.manifest.queryHashes.specimenPage,
        'specimen-patient-pairs': built.manifest.queryHashes.specimenPatientPairs,
        'patient-observation-groups': built.manifest.queryHashes.patientBatch,
      };
      const knownPhases = Object.keys(hashByPhase);
      const lastQueryRecord = identity.queryRecords?.at(-1) ?? null;
      const lastCompletedQuery = record.lastCompletedQuery ?? null;
      const matchesLastCompleted = lastQueryRecord
        ? lastCompletedQuery?.kind === lastQueryRecord.kind
          && lastCompletedQuery?.phase === lastQueryRecord.phase
          && lastCompletedQuery?.queryHash === lastQueryRecord.queryHash
          && lastCompletedQuery?.index === lastQueryRecord.index
          && lastCompletedQuery?.startedAt === lastQueryRecord.startedAt
          && lastCompletedQuery?.finishedAt === lastQueryRecord.finishedAt
        : lastCompletedQuery === null;
      if ((record.phase !== null && !knownPhases.includes(record.phase))
        || !Number.isSafeInteger(record.queryIndex) || record.queryIndex !== recordCount
        || !matchesLastCompleted) {
        throw new Error('Discovery failure has invalid query position, phase, or last-completed-query context');
      }
      if (typeof record.queryHash === 'string') {
        if (!knownPhases.includes(record.phase) || hashByPhase[record.phase] !== record.queryHash
          || !Number.isSafeInteger(record.startedAt) || !Number.isSafeInteger(record.finishedAt)
          || record.finishedAt < record.startedAt
          || (lastQueryFinishedAt !== null && record.startedAt < lastQueryFinishedAt)) {
          throw new Error('Discovery failure has invalid query identity or timestamps');
        }
      } else if (record.queryHash !== null || record.startedAt !== null || record.finishedAt !== null) {
        throw new Error('Script failure must not reuse last-completed-query identity or timestamps');
      }
      remoteFailure = record;
      identity.remoteFailure = remoteFailureDetails(record);
      identity.terminalRecord = {
        type: 'failure',
        protocolLineCount,
        afterQueryCount: recordCount,
        ...identity.remoteFailure,
      };
      setFailure(new Error(`Remote related quantity discovery failed: ${identity.remoteFailure.code}`));
      return;
    }
    if (record.type === 'done') {
      if (done || remoteFailure) throw new Error('Discovery has duplicate or contradictory terminal records');
      if (done || record.queryCount !== recordCount || !Number.isSafeInteger(record.pageCount)
        || !Number.isSafeInteger(record.pairPageCount) || !Number.isSafeInteger(record.patientBatchCount)
        || !Number.isSafeInteger(record.distinctPatientCount)) throw new Error('Discovery terminal record has invalid totals');
      done = record;
      return;
    }
    throw new Error('Unknown related quantity discovery protocol record type');
  };
  const consumeLine = rawLine => {
    const line = rawLine.endsWith('\r') ? rawLine.slice(0, -1) : rawLine;
    if (line.startsWith(ARANGOSH_PROCESS_PATH_MARKER)) {
      const found = line.slice(ARANGOSH_PROCESS_PATH_MARKER.length);
      if (!safeRemoteScriptPath(found) || (scriptPath && scriptPath !== found)) throw new Error('Remote script identity marker is invalid or changed');
      scriptPath = found;
      resolveIdentityReady();
      return;
    }
    if (line.startsWith(REMOTE_PID_MARKER)) {
      const match = line.slice(REMOTE_PID_MARKER.length).match(/^([0-9]+):(\/tmp\/loom-arangosh\.[A-Za-z0-9]+)$/);
      if (!match || (processId !== null && processId !== Number(match[1])) || (scriptPath && scriptPath !== match[2])) throw new Error('Remote process identity marker is invalid or mismatched');
      processId = Number(match[1]);
      scriptPath = match[2];
      resolveIdentityReady();
      return;
    }
    if (!line.startsWith(RELATED_QUANTITY_DISCOVERY_MARKER)) return;
    const record = parseJsonRecord(line, RELATED_QUANTITY_DISCOVERY_MARKER);
    handleProtocolRecord(record);
  };
  const consumeChunk = chunk => {
    stdoutBytes += chunk.length;
    identity.stdoutBytes = stdoutBytes;
    if (stdoutBytes > MAX_TOTAL_BYTES) throw new Error('Related quantity discovery output exceeded its total byte limit');
    const bytes = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
    let start = 0;
    let newline;
    while ((newline = bytes.indexOf(0x0a, start)) >= 0) {
      const segment = bytes.subarray(start, newline);
      const lineSize = pendingLineBytes + segment.length;
      if (lineSize > MAX_RECORD_BYTES) throw new Error('Related quantity discovery record exceeded its byte limit');
      const line = Buffer.concat([...pendingLineChunks, segment], lineSize);
      pendingLineChunks = [];
      pendingLineBytes = 0;
      consumeLine(line.toString('utf8'));
      start = newline + 1;
    }
    if (start < bytes.length) {
      const tail = bytes.subarray(start);
      pendingLineBytes += tail.length;
      if (pendingLineBytes > MAX_RECORD_BYTES) throw new Error('Unterminated related quantity discovery record exceeded its byte limit');
      pendingLineChunks.push(tail);
    }
  };
  const onData = chunk => {
    try { consumeChunk(chunk); } catch (error) { setFailure(error); }
  };
  const onStderr = chunk => {
    stderrBytes += chunk.length;
    identity.stderrBytes = stderrBytes;
    if (stderrBytes > MAX_STDERR_BYTES) setFailure(new Error('Related quantity discovery stderr exceeded its byte limit'));
  };
  const timer = setTimeout(() => {
    identity.deadlineReached = true;
    setFailure(new Error('Related quantity discovery exceeded its overall deadline'));
    resolveDeadlineReached();
  }, deadlineMs);
  const onAbort = () => setFailure(new Error('Related quantity discovery was aborted'));
  child.stdout.on('data', onData);
  child.stderr.on('data', onStderr);
  child.once('error', error => setFailure(error));
  signal?.addEventListener('abort', onAbort, { once: true });
  try {
    const waitResult = await Promise.race([
      closeResult.then(closed => ({ closed })),
      deadlineReached.then(() => ({ deadline: true })),
      unverifiedStopCleanup.then(() => ({ unverifiedStopCleanup: true })),
    ]);
    if (waitResult.closed) closedStatus = waitResult.closed;
    clearTimeout(timer);
    signal?.removeEventListener('abort', onAbort);
    if (failure) {
      await stopPromise;
      identity.status = 'failed';
      identity.failure = failure.message;
      identity.exitCode = closedStatus?.code ?? null;
      identity.signal = closedStatus?.signal ?? null;
      identity.stderrBytes = stderrBytes;
      identity.durationMs = now() - identity.startedAt;
      throw failure;
    }
    const closed = closedStatus;
    if (pendingLineBytes > 0) consumeLine(Buffer.concat(pendingLineChunks, pendingLineBytes).toString('utf8'));
    if (closed.code !== 0 || closed.signal !== null) throw new Error('Related quantity Arangosh process did not exit successfully');
    if (!begun || remoteFailure || !done) throw new Error('Related quantity Arangosh process ended without a complete successful protocol');
    identity.status = 'complete';
    identity.exitCode = closed.code;
    identity.signal = closed.signal;
    identity.done = done;
    identity.protocolLineCount = protocolLineCount;
    identity.stdoutBytes = stdoutBytes;
    identity.stderrBytes = stderrBytes;
    identity.lastRecordAt = lastRecordAt;
    identity.durationMs = now() - identity.startedAt;
    return { identity, manifest: built.manifest, done };
  } catch (error) {
    clearTimeout(timer);
    signal?.removeEventListener('abort', onAbort);
    if (!failure && closedError(error)) {
      identity.status = 'failed';
      identity.failure = error.message;
      identity.exitCode = closedStatus?.code ?? null;
      identity.signal = closedStatus?.signal ?? null;
      identity.stderrBytes = stderrBytes;
      identity.durationMs = now() - identity.startedAt;
      if (remoteFailure) identity.remoteFailure = remoteFailureDetails(remoteFailure);
      error.discoveryIdentity = identity;
    }
    throw error;
  }
}

function closedError(error) {
  return error instanceof Error;
}

export async function discoverCompleteRelatedQuantityRouteInProcess({
  scope,
  container,
  database = 'loom_dev',
  bounds = DEFAULT_BOUNDS,
  signal,
  deadlineMs = DEFAULT_DEADLINE_MS,
  processOptions = {},
}) {
  const accumulator = createRelatedQuantityPivotDiscoveryAccumulator(scope, bounds);
  const queryEvidence = [];
  let pageCount = 0;
  let pairPageCount = 0;
  let patientBatchCount = 0;
  const result = await runRelatedQuantityPivotDiscoveryProcess({
    scope, bounds, container, database, signal, deadlineMs, ...processOptions,
    onRecord(record) {
      const { payload, kind, phase } = record;
      const evidence = {
        phase,
        sha256: record.queryHash,
        maxRuntimeSeconds: QUERY_RUNTIME_SECONDS,
        memoryLimitBytes: QUERY_MEMORY_LIMIT_BYTES,
        startedAt: record.startedAt,
        completedAt: record.finishedAt,
        durationMs: record.finishedAt - record.startedAt,
      };
      if (kind === 'specimenPage') {
        accumulator.addSpecimenPage(payload);
        pageCount += 1;
        if (pageCount > 100_000) throw new Error('Full-route Specimen keyset exceeded its bounded page ceiling');
        queryEvidence.push({ kind: 'specimen-page', afterSpecimenKey: payload.afterSpecimenKey,
          specimenCount: payload.specimens.length, hasMore: payload.hasMore,
          nextAfterSpecimenKey: payload.nextAfterSpecimenKey, query: evidence });
        return;
      }
      if (kind === 'specimenPatientPairs') {
        accumulator.addSpecimenPatientPairs(payload);
        pairPageCount += 1;
        queryEvidence.push({ kind: 'specimen-patient-pairs', specimenCount: payload.specimenIds.length,
          pairRows: payload.rows.length, overflow: payload.overflow, truncated: payload.truncated, query: evidence });
        return;
      }
      if (kind === 'patientBatch') {
        const expectedIds = accumulator.nextPatientBatch(bounds.patientBatchSize);
        if (expectedIds.length === 0 || JSON.stringify(payload.patientIds) !== JSON.stringify(expectedIds)) {
          throw new Error('Arangosh patient batch sequence does not match the validated host multiplicity cursor');
        }
        accumulator.addPatientBatch(payload);
        patientBatchCount += 1;
        if (patientBatchCount > 100_000) throw new Error('Full-route Patient batching exceeded its bounded batch ceiling');
        queryEvidence.push({ kind: 'patient-observation-groups', patientCount: payload.patientIds.length,
          groupCount: payload.groups.length, overflow: payload.overflow, truncated: payload.truncated, query: evidence });
        return;
      }
      throw new Error(`Unknown streamed related quantity phase ${String(kind)}`);
    },
  });
  const { done } = result;
  if (done.pageCount !== pageCount || done.pairPageCount !== pairPageCount || done.patientBatchCount !== patientBatchCount) {
    throw new Error('Remote discovery totals do not match the host-consumed query sequence');
  }
  const discovery = accumulator.finalize();
  if (done.distinctPatientCount !== discovery.fullRouteCounts.patientsWithSpecimenRoots) {
    throw new Error('Remote distinct Patient count does not match the host-validated route multiplicities');
  }
  return {
    discovery,
    queryEvidence,
    bounds: { ...bounds },
    pageCount,
    patientBatchCount,
    pairPageCount,
    processIdentity: result.identity,
  };
}
