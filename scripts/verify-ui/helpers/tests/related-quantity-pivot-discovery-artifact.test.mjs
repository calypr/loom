import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { EventEmitter } from 'node:events';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { PassThrough } from 'node:stream';
import test from 'node:test';

import { loadRelatedQuantityPivotDiscoveryArtifact } from '../related-quantity-pivot-discovery-artifact.mjs';
import { sourceFingerprintWithManifest } from '../source-fingerprint.mjs';
import {
  buildRelatedQuantityPivotDiscoveryProcess,
  discoverCompleteRelatedQuantityRouteInProcess,
  RELATED_QUANTITY_DISCOVERY_MARKER,
} from '../related-quantity-pivot-discovery-process.mjs';
import { ARANGOSH_PROCESS_PATH_MARKER } from '../owned-arangosh-command.mjs';

const sourceRoot = resolve(import.meta.dirname, '../../../..');
const scope = {
  project: 'case018-retained-project',
  dataset_generation: 'case018-retained-generation',
  scope_allowed: true,
  auth_resource_paths_unrestricted: true,
  auth_resource_paths: [],
  emptyPolicies: ['PRESERVE_PARENT', 'PRESERVE_PARENT'],
};
const target = {
  project: scope.project,
  generation: scope.dataset_generation,
  arangoContainer: 'loom-arango-case018',
  database: 'loom_dev',
  authScope: {
    auth_resource_paths_unrestricted: true,
    auth_resource_paths: [],
    scope_allowed: true,
  },
};
const bounds = {
  specimenPageSize: 100,
  maxSpecimenPatientRows: 10_000,
  maxPatientGroups: 25_000,
  patientBatchSize: 100,
};
const group = {
  patientId: 'Patient/patient-a',
  firstHopMissing: false,
  secondHopMissing: false,
  observationPresent: true,
  conceptPresent: true,
  conceptType: 'OBJECT',
  textPresent: true,
  textType: 'STRING',
  text: 'FINAL',
  quantityPresent: true,
  quantityType: 'OBJECT',
  codePresent: true,
  codeType: 'STRING',
  code: 'mg',
  routeRows: 2,
  actualRouteRows: 2,
  emptyFirstHopRows: 0,
  emptySecondHopRows: 0,
  textMissingRows: 0,
  textNullRows: 0,
  textStringRows: 2,
  textOtherRows: 0,
  numericCount: 2,
  missingValueCount: 0,
  explicitNullValueCount: 0,
  terminalNullValueRows: 0,
  nonNumericValueCount: 0,
  numericSum: 7,
  numericMax: 5,
};
const emptyPatientGroup = {
  patientId: 'Patient/patient-b',
  firstHopMissing: false,
  secondHopMissing: true,
  observationPresent: false,
  conceptPresent: true,
  conceptType: 'NULL',
  textPresent: true,
  textType: 'NULL',
  text: null,
  quantityPresent: true,
  quantityType: 'NULL',
  codePresent: true,
  codeType: 'NULL',
  code: null,
  routeRows: 1,
  actualRouteRows: 0,
  emptyFirstHopRows: 0,
  emptySecondHopRows: 1,
  textMissingRows: 0,
  textNullRows: 1,
  textStringRows: 0,
  textOtherRows: 0,
  numericCount: 0,
  missingValueCount: 0,
  explicitNullValueCount: 0,
  terminalNullValueRows: 1,
  nonNumericValueCount: 0,
  numericSum: 0,
  numericMax: null,
};
const compactGroup = row => [
  row.patientId, row.firstHopMissing, row.secondHopMissing, row.observationPresent,
  row.conceptPresent, row.conceptType, row.textPresent, row.textType, row.text,
  row.quantityPresent, row.quantityType, row.codePresent, row.codeType, row.code,
  row.routeRows, row.actualRouteRows, row.emptyFirstHopRows, row.emptySecondHopRows,
  row.textMissingRows, row.textNullRows, row.textStringRows, row.textOtherRows,
  row.numericCount, row.missingValueCount, row.explicitNullValueCount,
  row.terminalNullValueRows, row.nonNumericValueCount, row.numericSum, row.numericMax,
];
const sha256 = bytes => createHash('sha256').update(bytes).digest('hex');
const acquisitionStartMs = Date.parse('2026-01-02T03:04:05.000Z');

function discoveryRecords(manifest) {
  const authScope = target.authScope;
  return [
    { type: 'begin', manifest },
    { type: 'result', index: 0, kind: 'specimenPage', phase: 'specimen-page',
      queryHash: manifest.queryHashes.specimenPage, startedAt: acquisitionStartMs + 1, finishedAt: acquisitionStartMs + 2,
      payload: { project: scope.project, generation: scope.dataset_generation, authScope,
        afterSpecimenKey: '', pageSize: bounds.specimenPageSize,
        specimens: [
          { specimenId: 'Specimen/root-a', specimenKey: 'root-a' },
          { specimenId: 'Specimen/root-b', specimenKey: 'root-b' },
          { specimenId: 'Specimen/root-c', specimenKey: 'root-c' },
          { specimenId: 'Specimen/root-d', specimenKey: 'root-d' },
        ],
        hasMore: false, nextAfterSpecimenKey: null } },
    { type: 'result', index: 1, kind: 'specimenPatientPairs', phase: 'specimen-patient-pairs',
      queryHash: manifest.queryHashes.specimenPatientPairs, startedAt: acquisitionStartMs + 3, finishedAt: acquisitionStartMs + 4,
      payload: { wireVersion: 1, project: scope.project, generation: scope.dataset_generation, authScope,
        specimenIds: ['Specimen/root-a', 'Specimen/root-b', 'Specimen/root-c', 'Specimen/root-d'],
        rows: [
          ['Specimen/root-a', 'Patient/patient-a'],
          ['Specimen/root-b', 'Patient/patient-a'],
          ['Specimen/root-c', 'Patient/patient-b'],
          ['Specimen/root-d', null],
        ],
        overflow: false, truncated: false } },
    { type: 'result', index: 2, kind: 'patientBatch', phase: 'patient-observation-groups',
      queryHash: manifest.queryHashes.patientBatch, startedAt: acquisitionStartMs + 5, finishedAt: acquisitionStartMs + 6,
      payload: { project: scope.project, generation: scope.dataset_generation, authScope,
        wireVersion: 1, patientIds: ['Patient/patient-a', 'Patient/patient-b'],
        groups: [compactGroup(group), compactGroup(emptyPatientGroup)],
        overflow: false, truncated: false } },
    { type: 'done', queryCount: 3, pageCount: 1, pairPageCount: 1,
      patientBatchCount: 1, distinctPatientCount: 2 },
  ];
}

function fakeSpawn(stdoutText) {
  return () => {
    const child = new EventEmitter();
    child.stdout = new PassThrough();
    child.stderr = new PassThrough();
    child.kill = () => true;
    setImmediate(() => {
      child.stdout.end(stdoutText);
      child.stderr.end();
      setImmediate(() => child.emit('close', 0, null));
    });
    return child;
  };
}

async function makeFixture(t) {
  const directory = await mkdtemp(join(tmpdir(), 'case018-retained-artifact-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const artifactPath = join(directory, 'acquisition.json');
  const sourceCapturePath = join(directory, 'source-before.json');
  const expectedManifest = buildRelatedQuantityPivotDiscoveryProcess(scope, bounds, { deadlineMs: 1_800_000 }).manifest;
  const records = discoveryRecords(expectedManifest);
  const protocolText = [
    `${ARANGOSH_PROCESS_PATH_MARKER}/tmp/loom-arangosh.case018\n`,
    '__LOOM_RQ_DISCOVERY_PID__:123:/tmp/loom-arangosh.case018\n',
    ...records.map(record => `${RELATED_QUANTITY_DISCOVERY_MARKER}${JSON.stringify(record)}\n`),
  ].join('');
  const run = await discoverCompleteRelatedQuantityRouteInProcess({
    scope, bounds, container: target.arangoContainer, deadlineMs: 1_800_000,
    processOptions: {
      spawnImpl: fakeSpawn(protocolText),
      now: (() => { let tick = 0; return () => acquisitionStartMs + tick++ * 10; })(),
    },
  });
  assert.deepEqual(run.discovery.groups, [
    { ...group, patientId: null, routeRows: 4, actualRouteRows: 4,
      textStringRows: 4, numericCount: 4, numericSum: 14 },
    { ...emptyPatientGroup, patientId: null },
    {
      patientId: null,
      firstHopMissing: true,
      secondHopMissing: false,
      observationPresent: false,
      conceptPresent: true,
      conceptType: 'NULL',
      textPresent: true,
      textType: 'NULL',
      text: null,
      quantityPresent: true,
      quantityType: 'NULL',
      codePresent: true,
      codeType: 'NULL',
      code: null,
      routeRows: 1,
      actualRouteRows: 0,
      emptyFirstHopRows: 1,
      emptySecondHopRows: 0,
      textMissingRows: 0,
      textNullRows: 1,
      textStringRows: 0,
      textOtherRows: 0,
      numericCount: 0,
      missingValueCount: 0,
      explicitNullValueCount: 0,
      terminalNullValueRows: 1,
      nonNumericValueCount: 0,
      numericSum: 0,
      numericMax: null,
    },
  ]);
  const source = sourceFingerprintWithManifest(sourceRoot);
  const sourceCapture = {
    phase: 'before', root: sourceRoot, capturedAt: '2026-01-02T03:04:04.000Z', ...source,
  };
  const envelope = {
    version: 1,
    status: 'complete',
    startedAt: '2026-01-02T03:04:05.000Z',
    completedAt: '2026-01-02T03:04:06.000Z',
    source: { root: sourceRoot, fingerprint: source.fingerprint, target },
    result: run,
  };
  let artifact = envelope;
  let capture = sourceCapture;
  const writePinned = async () => {
    const artifactBytes = Buffer.from(JSON.stringify(artifact));
    const captureBytes = Buffer.from(JSON.stringify(capture));
    await writeFile(artifactPath, artifactBytes);
    await writeFile(sourceCapturePath, captureBytes);
    return { artifactSha256: sha256(artifactBytes), sourceCaptureSha256: sha256(captureBytes) };
  };
  const load = async (options = {}) => {
    const pins = await writePinned();
    return loadRelatedQuantityPivotDiscoveryArtifact({
      artifactPath,
      artifactSha256: options.artifactSha256 ?? pins.artifactSha256,
      sourceCapturePath,
      sourceCaptureSha256: options.sourceCaptureSha256 ?? pins.sourceCaptureSha256,
      expectedScope: options.expectedScope ?? scope,
      expectedTarget: options.expectedTarget ?? target,
      expectedSourceRoot: options.expectedSourceRoot ?? sourceRoot,
    });
  };
  return {
    artifactPath, sourceCapturePath, sourceRoot, source, envelope,
    setArtifact(value) { artifact = value; },
    setCapture(value) { capture = value; },
    writePinned, load,
  };
}

test('loads a tiny complete retained acquisition against the current source and query manifest', async t => {
  const fixture = await makeFixture(t);
  const loaded = await fixture.load();
  assert.equal(loaded.discovery.complete, true);
  assert.equal(loaded.discovery.project, 'case018-retained-project');
  assert.equal(loaded.discovery.generation, 'case018-retained-generation');
  assert.deepEqual(loaded.discovery.fullRouteCounts, {
    totalSpecimens: 4,
    matchedSpecimens: 3,
    emptyFirstHopSpecimenRoots: 1,
    distinctSpecimenPatientPairs: 3,
    patientsWithSpecimenRoots: 2,
    patientObservationGroups: 2,
    uniquePatientObservationPairs: 2,
    emptySecondHopPatientRows: 1,
    emptySecondHopPatientCount: 1,
    actualRouteRows: 4,
    leftJoinOutputRows: 6,
    nonNumericValueCount: 0,
    rawTextTypedCodeGroupCount: 3,
    retainedRawGroupAccountingBytes: loaded.discovery.fullRouteCounts.retainedRawGroupAccountingBytes,
    retainedRawGroupCountHighWater: 3,
    retainedRawGroupBytesHighWater: loaded.discovery.fullRouteCounts.retainedRawGroupAccountingBytes,
    visibleTextGroupCount: 2,
    unsupportedTextGroupCount: 0,
    visiblePivotCellCount: 2,
    categoryDomainCount: 2,
  });
  assert.equal(loaded.artifactProvenance.artifactSha256, sha256(await readFile(fixture.artifactPath)));
  assert.deepEqual(loaded.artifactProvenance.queryHashes, fixture.envelope.result.processIdentity.queryHashes);
});

test('rejects tampered artifact and source-capture SHA pins', async t => {
  const fixture = await makeFixture(t);
  const pins = await fixture.writePinned();
  await assert.rejects(fixture.load({ artifactSha256: '0'.repeat(64) }), /SHA-256 does not match/);
  await assert.rejects(fixture.load({ sourceCaptureSha256: '0'.repeat(64) }), /SHA-256 does not match/);
  assert.equal(pins.artifactSha256.length, 64);
});

test('rejects target, generation, authorization, and empty-policy mismatches', async t => {
  const fixture = await makeFixture(t);
  await assert.rejects(fixture.load({ expectedTarget: { ...target, arangoContainer: 'wrong-container' } }), /Acquisition target must match/);
  await assert.rejects(fixture.load({ expectedScope: { ...scope, dataset_generation: 'wrong-generation' } }), /Expected discovery target/);
  await assert.rejects(fixture.load({ expectedScope: { ...scope, auth_resource_paths_unrestricted: false } }), /exact full-population/);
  await assert.rejects(fixture.load({ expectedScope: { ...scope, emptyPolicies: ['DROP', 'PRESERVE_PARENT'] } }), /exact full-population/);

  const altered = structuredClone(fixture.envelope);
  altered.source.target.generation = 'stale-generation';
  fixture.setArtifact(altered);
  await assert.rejects(fixture.load(), /Acquisition target must match/);
  const wrongAuth = structuredClone(fixture.envelope);
  wrongAuth.source.target.authScope.scope_allowed = false;
  fixture.setArtifact(wrongAuth);
  await assert.rejects(fixture.load(), /Acquisition target must match/);
});

test('rejects stale generated query manifest, bounds, and fixed limits', async t => {
  const fixture = await makeFixture(t);
  for (const mutate of [
    result => { result.processIdentity.queryHashes.patientBatch = '0'.repeat(64); },
    result => { result.bounds.patientBatchSize += 1; },
    result => { result.processIdentity.queryMemoryLimitBytes += 1; },
    result => { result.processIdentity.deadlineMs -= 1; },
  ]) {
    const altered = structuredClone(fixture.envelope);
    mutate(altered.result);
    fixture.setArtifact(altered);
    await assert.rejects(fixture.load(), /bounds|query hashes|does not match|fixed resource limit/i);
  }
});

test('rejects incomplete process evidence and phase counters', async t => {
  const fixture = await makeFixture(t);
  for (const mutate of [
    result => { result.processIdentity.status = 'failed'; },
    result => { result.queryEvidence.pop(); },
    result => { result.patientBatchCount = 0; },
    result => { result.processIdentity.done.queryCount = 2; },
  ]) {
    const altered = structuredClone(fixture.envelope);
    mutate(altered.result);
    fixture.setArtifact(altered);
    await assert.rejects(fixture.load(), /complete|evidence|counters|query evidence|phase counts|finish successfully/i);
  }
});

test('rejects a repinned source capture whose current oracle source hash differs', async t => {
  const fixture = await makeFixture(t);
  const alteredCapture = structuredClone({
    phase: 'before', root: fixture.sourceRoot, capturedAt: '2026-01-02T03:04:04.000Z', ...fixture.source,
  });
  alteredCapture.manifest['scripts/verify-ui/helpers/related-quantity-pivot-oracle.mjs'] = '0'.repeat(64);
  fixture.setCapture(alteredCapture);
  await assert.rejects(fixture.load(), /differs from the acquisition.s source-before capture/);
  const missingTimestamp = structuredClone({
    phase: 'before', root: fixture.sourceRoot, capturedAt: '2026-01-02T03:04:04.000Z', ...fixture.source,
  });
  delete missingTimestamp.capturedAt;
  fixture.setCapture(missingTimestamp);
  await assert.rejects(fixture.load(), /valid timestamp no later than the acquisition start/);

  const futureTimestamp = structuredClone({
    phase: 'before', root: fixture.sourceRoot, capturedAt: '2026-01-02T03:04:06.000Z', ...fixture.source,
  });
  fixture.setCapture(futureTimestamp);
  await assert.rejects(fixture.load(), /valid timestamp no later than the acquisition start/);
});

test('rejects internally tampered typed summary even when the artifact pin is recomputed', async t => {
  const fixture = await makeFixture(t);
  const altered = structuredClone(fixture.envelope);
  altered.result.discovery.groups[0].textType = 'NULL';
  fixture.setArtifact(altered);
  await assert.rejects(fixture.load(), /text value does not match|does not reconcile|typed groups/i);
});
