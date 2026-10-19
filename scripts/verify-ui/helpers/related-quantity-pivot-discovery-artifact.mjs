import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { lstat, readFile, realpath } from 'node:fs/promises';
import { isAbsolute, relative, resolve, sep } from 'node:path';
import { buildRelatedQuantityPivotDiscoveryProcess } from './related-quantity-pivot-discovery-process.mjs';
import { summarizeRelatedQuantityPivotDiscoveryResult } from './related-quantity-pivot-oracle.mjs';

const MAX_ARTIFACT_BYTES = 512 * 1024 * 1024;
const MAX_SOURCE_CAPTURE_BYTES = 8 * 1024 * 1024;
const SOURCE_APPLICABILITY_FILES = Object.freeze([
  'scripts/verify-ui/helpers/related-quantity-pivot-oracle.mjs',
  'scripts/verify-ui/helpers/related-quantity-pivot-discovery-process.mjs',
  'scripts/verify-ui/helpers/owned-arangosh-command.mjs',
]);
const PHASES = Object.freeze({
  'specimen-page': { kind: 'specimen-page', queryHash: 'specimenPage' },
  'specimen-patient-pairs': { kind: 'specimen-patient-pairs', queryHash: 'specimenPatientPairs' },
  'patient-observation-groups': { kind: 'patient-observation-groups', queryHash: 'patientBatch' },
});

const sha256 = value => createHash('sha256').update(value).digest('hex');
const isPlainObject = value => value !== null && typeof value === 'object' && !Array.isArray(value);

async function readPinnedJson(path, expectedHash, maxBytes, label) {
  if (typeof path !== 'string' || !isAbsolute(path)) throw new TypeError(`${label} path must be absolute`);
  if (!/^[a-f0-9]{64}$/.test(expectedHash ?? '')) throw new TypeError(`${label} SHA-256 must be lowercase hexadecimal`);
  const info = await lstat(path);
  if (!info.isFile() || info.isSymbolicLink()) throw new Error(`${label} must be a regular, non-symlink file`);
  if (info.size < 2 || info.size > maxBytes) throw new Error(`${label} exceeds its bounded file size`);
  const bytes = await readFile(path);
  if (bytes.length !== info.size || bytes.length > maxBytes) throw new Error(`${label} changed while being read or exceeds its size limit`);
  const actualHash = sha256(bytes);
  if (actualHash !== expectedHash) throw new Error(`${label} SHA-256 does not match its trusted pin`);
  let value;
  try {
    value = JSON.parse(bytes.toString('utf8'));
  } catch (error) {
    throw new Error(`${label} is not valid JSON: ${String(error?.message ?? error).slice(0, 200)}`);
  }
  return { value, actualHash, resolvedPath: await realpath(path), sizeBytes: bytes.length };
}

function assertOutsideRoot(filePath, root, label) {
  const rel = relative(root, filePath);
  if (rel === '' || (!rel.startsWith(`..${sep}`) && rel !== '..' && !isAbsolute(rel))) {
    throw new Error(`${label} must be stored outside the watched source root`);
  }
}

function assertExpectedScope(scope) {
  if (!isPlainObject(scope)
    || typeof scope.project !== 'string' || !scope.project
    || typeof scope.dataset_generation !== 'string' || !scope.dataset_generation
    || scope.scope_allowed !== true
    || scope.auth_resource_paths_unrestricted !== true
    || !Array.isArray(scope.auth_resource_paths) || scope.auth_resource_paths.length !== 0
    || !Array.isArray(scope.emptyPolicies)
    || JSON.stringify(scope.emptyPolicies) !== JSON.stringify(['PRESERVE_PARENT', 'PRESERVE_PARENT'])) {
    throw new Error('Retained discovery requires the exact full-population CDA authorization and empty-route scope');
  }
}

function assertExpectedTarget(target, scope) {
  if (!isPlainObject(target)
    || target.project !== scope.project
    || target.generation !== scope.dataset_generation
    || typeof target.arangoContainer !== 'string' || !target.arangoContainer
    || target.database !== 'loom_dev'
    || !isPlainObject(target.authScope)
    || target.authScope.scope_allowed !== true
    || target.authScope.auth_resource_paths_unrestricted !== true
    || !Array.isArray(target.authScope.auth_resource_paths)
    || target.authScope.auth_resource_paths.length !== 0) {
    throw new Error('Expected discovery target must identify the owned CDA project, generation, database, container, and unrestricted no-auth scope');
  }
  assert.deepEqual(target.authScope, {
    scope_allowed: scope.scope_allowed,
    auth_resource_paths_unrestricted: scope.auth_resource_paths_unrestricted,
    auth_resource_paths: scope.auth_resource_paths,
  }, 'Expected target auth scope must match the exact full-population query scope');
}

function queryPhaseFor(entry) {
  const phase = PHASES[entry?.query?.phase];
  if (!phase || entry.kind !== phase.kind) throw new Error('Retained discovery query evidence has a phase/kind mismatch');
  return phase;
}

function validateQueryEvidence(run, manifest) {
  const { queryEvidence, processIdentity, bounds } = run;
  if (!Array.isArray(queryEvidence) || queryEvidence.length === 0 || queryEvidence.length > manifest.maxQueryCount) {
    throw new Error('Retained discovery query evidence is missing or exceeds its query-count bound');
  }
  const counts = { 'specimen-page': 0, 'specimen-patient-pairs': 0, 'patient-observation-groups': 0 };
  if (!isPlainObject(processIdentity.phaseQueryMetrics)
    || !isPlainObject(processIdentity.phaseWireBytes)
    || !Array.isArray(processIdentity.queryRecords)) {
    throw new Error('Retained process identity is missing its bounded phase metrics or request history');
  }
  let patientPhaseStarted = false;
  let previousCompletedAt = null;
  let nextSpecimenCursor = '';
  let pendingPairCount = null;
  let lastSpecimenPage = null;
  let specimenStreamEnded = false;
  let specimenTotal = 0;
  let pairTotal = 0;
  let patientTotal = 0;
  let patientGroupTotal = 0;
  for (const entry of queryEvidence) {
    const phase = queryPhaseFor(entry);
    const query = entry.query;
    const expectedPhaseHash = manifest.queryHashes[phase.queryHash];
    if (query.sha256 !== expectedPhaseHash
      || query.maxRuntimeSeconds !== manifest.queryRuntimeSeconds
      || query.memoryLimitBytes !== manifest.queryMemoryLimitBytes
      || !Number.isSafeInteger(query.startedAt) || !Number.isSafeInteger(query.completedAt)
      || query.completedAt < query.startedAt
      || query.durationMs !== query.completedAt - query.startedAt
      || query.durationMs > manifest.queryRuntimeSeconds * 1000
      || query.startedAt < processIdentity.startedAt
      || query.completedAt > processIdentity.lastRecordAt
      || (previousCompletedAt !== null && query.startedAt < previousCompletedAt)) {
      throw new Error('Retained discovery query chronology, hash, or fixed resource limit does not match the current generated manifest');
    }
    previousCompletedAt = query.completedAt;
    counts[query.phase] += 1;
    if (query.phase === 'patient-observation-groups') patientPhaseStarted = true;
    else if (patientPhaseStarted) throw new Error('Retained discovery phases are out of order');

    if (query.phase === 'specimen-page') {
      if (specimenStreamEnded || pendingPairCount !== null
        || entry.afterSpecimenKey !== nextSpecimenCursor
        || !Number.isSafeInteger(entry.specimenCount)
        || entry.specimenCount < 1 || entry.specimenCount > bounds.specimenPageSize
        || typeof entry.hasMore !== 'boolean') {
        throw new Error('Retained Specimen keyset pages are not complete and contiguous');
      }
      if (entry.hasMore) {
        if (entry.specimenCount !== bounds.specimenPageSize
          || typeof entry.nextAfterSpecimenKey !== 'string' || !entry.nextAfterSpecimenKey
          || entry.nextAfterSpecimenKey <= entry.afterSpecimenKey) {
          throw new Error('Retained Specimen keyset continuation is malformed');
        }
        nextSpecimenCursor = entry.nextAfterSpecimenKey;
      } else {
        if (entry.nextAfterSpecimenKey !== null) throw new Error('Final Specimen keyset page must have no continuation cursor');
      }
      specimenTotal += entry.specimenCount;
      pendingPairCount = entry.specimenCount;
      lastSpecimenPage = entry;
    } else if (query.phase === 'specimen-patient-pairs') {
      if (specimenStreamEnded || pendingPairCount === null
        || entry.specimenCount !== pendingPairCount
        || !Number.isSafeInteger(entry.pairRows) || entry.pairRows < entry.specimenCount || entry.pairRows > bounds.maxSpecimenPatientRows
        || entry.overflow !== false || entry.truncated !== false) {
        throw new Error('Retained Specimen/Patient pair page is incomplete, over-bound, or detached from its Specimen page');
      }
      pairTotal += entry.pairRows;
      pendingPairCount = null;
      if (lastSpecimenPage?.hasMore === false) specimenStreamEnded = true;
    } else {
      if (!specimenStreamEnded || pendingPairCount !== null
        || !Number.isSafeInteger(entry.patientCount) || entry.patientCount < 1 || entry.patientCount > bounds.patientBatchSize
        || !Number.isSafeInteger(entry.groupCount) || entry.groupCount < 0 || entry.groupCount > bounds.maxPatientGroups
        || entry.overflow !== false || entry.truncated !== false) {
        throw new Error('Retained Patient observation batch is incomplete or exceeds its validated bounds');
      }
      patientTotal += entry.patientCount;
      patientGroupTotal += entry.groupCount;
    }
  }
  if (!specimenStreamEnded || pendingPairCount !== null || counts['specimen-page'] !== counts['specimen-patient-pairs']) {
    throw new Error('Retained raw discovery ended before every Specimen page had a matching pair page');
  }
  if (run.pageCount !== counts['specimen-page']
    || run.pairPageCount !== counts['specimen-patient-pairs']
    || run.patientBatchCount !== counts['patient-observation-groups']) {
    throw new Error('Retained discovery phase counts do not match its top-level completion counters');
  }
  const discoveryCounts = run.discovery?.fullRouteCounts;
  if (!isPlainObject(discoveryCounts)
    || specimenTotal !== run.discovery.specimenCount
    || pairTotal !== run.discovery.matchedPatientRows + run.discovery.emptySpecimenCount
    || patientTotal !== processIdentity.done?.distinctPatientCount
    || patientTotal !== discoveryCounts.patientsWithSpecimenRoots
    || patientGroupTotal !== discoveryCounts.patientObservationGroups) {
    throw new Error('Retained query page cardinalities do not reconcile to the complete host-verified discovery totals');
  }

  const identity = processIdentity;
  const expectedQueryCount = queryEvidence.length;
  if (identity.queryCount !== expectedQueryCount
    || identity.receivedQueryCount !== expectedQueryCount
    || identity.verifiedQueryCount !== expectedQueryCount
    || identity.done?.queryCount !== expectedQueryCount
    || identity.done?.pageCount !== run.pageCount
    || identity.done?.pairPageCount !== run.pairPageCount
    || identity.done?.patientBatchCount !== run.patientBatchCount
    || identity.done?.type !== 'done'
    || identity.protocolLineCount !== expectedQueryCount + 2) {
    throw new Error('Retained process completion counters do not reconcile to every query evidence record');
  }
  for (const [phaseName, expectedCount] of Object.entries(counts)) {
    const metrics = identity.phaseQueryMetrics?.[phaseName];
    if (!metrics || metrics.received !== expectedCount || metrics.hostVerified !== expectedCount) {
      throw new Error(`Retained ${phaseName} requests are not all host-verified`);
    }
    const wireBytes = identity.phaseWireBytes?.[phaseName];
    if (!Number.isSafeInteger(wireBytes) || wireBytes < 0) throw new Error(`Retained ${phaseName} byte count is malformed`);
  }
  const expectedPhases = Object.keys(counts).sort();
  if (JSON.stringify(Object.keys(identity.phaseQueryMetrics).sort()) !== JSON.stringify(expectedPhases)
    || JSON.stringify(Object.keys(identity.phaseWireBytes).sort()) !== JSON.stringify(expectedPhases)
    || !Number.isSafeInteger(identity.stdoutBytes) || !Number.isSafeInteger(identity.stderrBytes)) {
    throw new Error('Retained process phase metrics contain missing or unexpected phases');
  }
  const totalPhaseBytes = Object.values(identity.phaseWireBytes).reduce((sum, bytes) => sum + bytes, 0);
  if (totalPhaseBytes > identity.stdoutBytes
    || identity.stdoutBytes > manifest.maxTotalBytes
    || identity.stderrBytes > manifest.maxStderrBytes
    || identity.queryRecords.length > 32) {
    throw new Error('Retained discovery process exceeded an output bound or has malformed bounded request history');
  }
  const expectedTail = Math.min(expectedQueryCount, 32);
  if (identity.queryRecords.length !== expectedTail) throw new Error('Retained process request history is truncated inconsistently');
  for (let offset = 0; offset < identity.queryRecords.length; offset += 1) {
    const record = identity.queryRecords[offset];
    const evidenceIndex = expectedQueryCount - identity.queryRecords.length + offset;
    const evidence = queryEvidence[evidenceIndex];
    if (record.index !== evidenceIndex
      || record.phase !== evidence.query.phase
      || record.queryHash !== evidence.query.sha256
      || record.startedAt !== evidence.query.startedAt
      || record.finishedAt !== evidence.query.completedAt
      || record.hostVerification !== 'verified'
      || !Number.isSafeInteger(record.wireBytes) || record.wireBytes > manifest.maxRecordBytes) {
      throw new Error('Retained process request history disagrees with the end of its complete query evidence');
    }
  }
  if (!Number.isSafeInteger(identity.done.distinctPatientCount)
    || identity.done.distinctPatientCount > manifest.maxUniquePatients) {
    throw new Error('Retained unique Patient count is malformed or exceeds its validated limit');
  }
}

/** Load a trusted complete acquisition after validating its source, scope, query manifest, and full result. */
export async function loadRelatedQuantityPivotDiscoveryArtifact({
  artifactPath,
  artifactSha256,
  sourceCapturePath,
  sourceCaptureSha256,
  expectedScope,
  expectedTarget,
  expectedSourceRoot,
}) {
  assertExpectedScope(expectedScope);
  assertExpectedTarget(expectedTarget, expectedScope);
  if (typeof expectedSourceRoot !== 'string' || !isAbsolute(expectedSourceRoot)) throw new TypeError('Expected source root must be absolute');
  const sourceRoot = await realpath(expectedSourceRoot);
  const artifact = await readPinnedJson(artifactPath, artifactSha256, MAX_ARTIFACT_BYTES, 'Discovery artifact');
  const capture = await readPinnedJson(sourceCapturePath, sourceCaptureSha256, MAX_SOURCE_CAPTURE_BYTES, 'Source-before capture');
  assertOutsideRoot(artifact.resolvedPath, sourceRoot, 'Discovery artifact');
  assertOutsideRoot(capture.resolvedPath, sourceRoot, 'Source-before capture');

  const envelope = artifact.value;
  const sourceCapture = capture.value;
  if (!isPlainObject(envelope) || envelope.version !== 1 || envelope.status !== 'complete'
    || !isPlainObject(envelope.source) || !isPlainObject(envelope.result)) {
    throw new Error('Pinned discovery artifact is not a complete version-1 acquisition');
  }
  if (!isPlainObject(sourceCapture) || sourceCapture.phase !== 'before'
    || await realpath(sourceCapture.root) !== sourceRoot
    || !isPlainObject(sourceCapture.fingerprint) || !isPlainObject(sourceCapture.manifest)) {
    throw new Error('Pinned source capture is not a before-capture for the expected source root');
  }
  if (typeof envelope.source.root !== 'string' || await realpath(envelope.source.root) !== sourceRoot) {
    throw new Error('Acquisition source root does not match the expected mounted checkout');
  }
  assert.deepEqual(envelope.source.fingerprint, sourceCapture.fingerprint,
    'Acquisition source fingerprint must match its retained source-before capture');
  assert.deepEqual(envelope.source.target, expectedTarget,
    'Acquisition target must match the currently validated project, generation, container, database, and auth scope');

  for (const file of SOURCE_APPLICABILITY_FILES) {
    const capturedHash = sourceCapture.manifest[file];
    if (!/^[a-f0-9]{64}$/.test(capturedHash ?? '')) throw new Error(`Source-before capture is missing ${file}`);
    const actualHash = sha256(await readFile(resolve(sourceRoot, file)));
    if (actualHash !== capturedHash) throw new Error(`Current ${file} differs from the acquisition's source-before capture`);
  }

  const process = buildRelatedQuantityPivotDiscoveryProcess(expectedScope, undefined, { deadlineMs: 1_800_000 });
  const expectedManifest = process.manifest;
  const run = envelope.result;
  const identity = run.processIdentity;
  if (!isPlainObject(run) || !isPlainObject(identity)
    || !isPlainObject(run.discovery) || !isPlainObject(run.bounds)) {
    throw new Error('Pinned discovery artifact is missing its completed process or discovery result');
  }
  assert.deepEqual(run.bounds, expectedManifest.bounds, 'Acquisition bounds must match the current bounded query manifest');
  assert.equal(identity.scopeHash, expectedManifest.scopeHash, 'Acquisition authorization-scope hash is stale');
  assert.deepEqual(identity.queryHashes, expectedManifest.queryHashes, 'Acquisition query hashes differ from the current generated queries');
  for (const key of ['deadlineMs', 'queryRuntimeSeconds', 'queryMemoryLimitBytes', 'maxRecordBytes', 'maxTotalBytes']) {
    assert.equal(identity[key], expectedManifest[key], `Acquisition ${key} does not match the current bounded process manifest`);
  }
  if (identity.status !== 'complete' || identity.exitCode !== 0 || identity.signal !== null
    || !Number.isSafeInteger(identity.durationMs) || identity.durationMs < 0
    || identity.durationMs > expectedManifest.deadlineMs
    || !Number.isFinite(Date.parse(envelope.startedAt)) || !Number.isFinite(Date.parse(envelope.completedAt))
    || Date.parse(envelope.completedAt) < Date.parse(envelope.startedAt)) {
    throw new Error('Pinned discovery process did not finish successfully within its overall deadline');
  }
  const acquisitionStart = Date.parse(envelope.startedAt);
  const acquisitionEnd = Date.parse(envelope.completedAt);
  const sourceCapturedAt = Date.parse(sourceCapture.capturedAt);
  if (!Number.isFinite(sourceCapturedAt) || sourceCapturedAt > acquisitionStart) {
    throw new Error('Source-before capture must have a valid timestamp no later than the acquisition start');
  }
  if (!Number.isSafeInteger(identity.startedAt)
    || identity.startedAt < acquisitionStart || identity.startedAt > acquisitionEnd
    || !Number.isSafeInteger(identity.lastRecordAt)
    || identity.lastRecordAt < identity.startedAt || identity.lastRecordAt > acquisitionEnd) {
    throw new Error('Pinned process chronology falls outside the acquisition envelope');
  }

  validateQueryEvidence(run, expectedManifest);
  const discovery = run.discovery;
  assert.equal(discovery.project, expectedScope.project);
  assert.equal(discovery.generation, expectedScope.dataset_generation);
  assert.deepEqual(discovery.authScope, {
    auth_resource_paths_unrestricted: expectedScope.auth_resource_paths_unrestricted,
    auth_resource_paths: expectedScope.auth_resource_paths,
    scope_allowed: expectedScope.scope_allowed,
  });
  const counts = discovery.fullRouteCounts;
  if (!isPlainObject(counts)
    || !Number.isSafeInteger(counts.retainedRawGroupCountHighWater)
    || counts.retainedRawGroupCountHighWater < 0
    || counts.retainedRawGroupCountHighWater > expectedManifest.maxRetainedRawGroups
    || !Number.isSafeInteger(counts.retainedRawGroupBytesHighWater)
    || counts.retainedRawGroupBytesHighWater < 0
    || counts.retainedRawGroupBytesHighWater > expectedManifest.maxRetainedRawGroupBytes
    || !Number.isSafeInteger(counts.retainedRawGroupAccountingBytes)
    || counts.retainedRawGroupAccountingBytes < 0
    || counts.retainedRawGroupAccountingBytes > expectedManifest.maxRetainedRawGroupBytes
    || counts.leftJoinOutputRows > Number.MAX_SAFE_INTEGER) {
    throw new Error('Complete discovery result exceeds a retained-group bound or has invalid route counts');
  }
  const validationTextKey = discovery.visibleTextDomain?.find(entry => entry.textType === 'NULL' || entry.textType === 'STRING');
  if (!validationTextKey) throw new Error('Complete acquisition has no supported visible text key for typed-summary validation');
  summarizeRelatedQuantityPivotDiscoveryResult(discovery, expectedScope, {
    visibleTextKeys: [validationTextKey.textType === 'NULL' ? null : validationTextKey.text],
  });

  return {
    ...run,
    artifactProvenance: {
      artifactPath: artifact.resolvedPath,
      artifactSha256: artifact.actualHash,
      sourceCapturePath: capture.resolvedPath,
      sourceCaptureSha256: capture.actualHash,
      sourceFingerprint: sourceCapture.fingerprint,
      relevantSourceHashes: Object.fromEntries(SOURCE_APPLICABILITY_FILES.map(path => [path, sourceCapture.manifest[path]])),
      queryHashes: expectedManifest.queryHashes,
      loadedAt: new Date().toISOString(),
    },
  };
}
