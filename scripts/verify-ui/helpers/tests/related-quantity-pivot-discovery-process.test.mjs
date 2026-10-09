import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import vm from 'node:vm';
import test from 'node:test';

import {
  buildRelatedQuantityPivotDiscoveryProcess,
  discoverCompleteRelatedQuantityRouteInProcess,
  RELATED_QUANTITY_DISCOVERY_MARKER,
  runRelatedQuantityPivotDiscoveryProcess,
} from '../related-quantity-pivot-discovery-process.mjs';

const scope = {
  project: 'case018-process-project',
  dataset_generation: 'case018-process-generation',
  scope_allowed: true,
  auth_resource_paths_unrestricted: false,
  auth_resource_paths: ['/case018/process'],
  emptyPolicies: ['PRESERVE_PARENT', 'PRESERVE_PARENT'],
};

const bounds = {
  specimenPageSize: 2,
  maxSpecimenPatientRows: 10,
  maxPatientGroups: 10,
  patientBatchSize: 1,
};

const authScope = {
  auth_resource_paths_unrestricted: false,
  auth_resource_paths: ['/case018/process'],
  scope_allowed: true,
};

const makeGroup = patientId => ({
  patientId,
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
  routeRows: 1,
  actualRouteRows: 1,
  emptyFirstHopRows: 0,
  emptySecondHopRows: 0,
  textMissingRows: 0,
  textNullRows: 0,
  textStringRows: 1,
  textOtherRows: 0,
  numericCount: 1,
  missingValueCount: 0,
  explicitNullValueCount: 0,
  terminalNullValueRows: 0,
  nonNumericValueCount: 0,
  numericSum: 5,
  numericMax: 5,
});

const oneRootRecords = manifest => [
  { type: 'begin', manifest },
  { type: 'result', index: 0, kind: 'specimenPage', phase: 'specimen-page', queryHash: manifest.queryHashes.specimenPage,
    startedAt: 10, finishedAt: 11, payload: {
      project: scope.project, generation: scope.dataset_generation, authScope,
      afterSpecimenKey: '', pageSize: bounds.specimenPageSize,
      specimens: [{ specimenId: 'Specimen/root-a', specimenKey: 'root-a' }], hasMore: false, nextAfterSpecimenKey: null,
    } },
  { type: 'result', index: 1, kind: 'specimenPatientPairs', phase: 'specimen-patient-pairs', queryHash: manifest.queryHashes.specimenPatientPairs,
    startedAt: 12, finishedAt: 13, payload: {
      project: scope.project, generation: scope.dataset_generation, authScope,
      specimenIds: ['Specimen/root-a'], rows: [{ specimenId: 'Specimen/root-a', patientId: 'Patient/patient-a' }],
      overflow: false, truncated: false,
    } },
  { type: 'result', index: 2, kind: 'patientBatch', phase: 'patient-observation-groups', queryHash: manifest.queryHashes.patientBatch,
    startedAt: 14, finishedAt: 15, payload: {
      project: scope.project, generation: scope.dataset_generation, authScope,
      patientIds: ['Patient/patient-a'], groups: [makeGroup('Patient/patient-a')], overflow: false, truncated: false,
    } },
  { type: 'done', queryCount: 3, pageCount: 1, pairPageCount: 1, patientBatchCount: 1, distinctPatientCount: 1 },
];

const protocolLines = records => records.map(record => `${RELATED_QUANTITY_DISCOVERY_MARKER}${JSON.stringify(record)}\n`).join('');
const lastCompletedQuery = record => record ? ({
  kind: record.kind,
  phase: record.phase,
  queryHash: record.queryHash,
  index: record.index,
  startedAt: record.startedAt,
  finishedAt: record.finishedAt,
}) : null;

const fakeProcess = (stdoutText, { code = 0, closeSignal = null, beforeClose } = {}) => {
  const child = new EventEmitter();
  child.stdout = new PassThrough();
  child.stderr = new PassThrough();
  child.kill = () => {
    beforeClose?.();
    child.stdout.end();
    child.stderr.end();
    setImmediate(() => child.emit('close', null, 'SIGTERM'));
    return true;
  };
  setImmediate(() => {
    child.stdout.write(stdoutText);
    child.stdout.end();
    child.stderr.end();
    setImmediate(() => child.emit('close', code, closeSignal));
  });
  return child;
};

test('single remote discovery script executes ordered bounded keyset, pair, and Patient phases', () => {
  const built = buildRelatedQuantityPivotDiscoveryProcess(scope, bounds, { deadlineMs: 15_000 });
  const writes = [];
  const calls = [];
  const pageRows = [
    { project: scope.project, generation: scope.dataset_generation, authScope,
      afterSpecimenKey: '', pageSize: bounds.specimenPageSize,
      specimens: [{ specimenId: 'Specimen/a', specimenKey: 'a' }, { specimenId: 'Specimen/b', specimenKey: 'b' }],
      hasMore: true, nextAfterSpecimenKey: 'b' },
    { project: scope.project, generation: scope.dataset_generation, authScope,
      afterSpecimenKey: 'b', pageSize: bounds.specimenPageSize,
      specimens: [{ specimenId: 'Specimen/c', specimenKey: 'c' }], hasMore: false, nextAfterSpecimenKey: null },
  ];
  const pairRows = [
    { project: scope.project, generation: scope.dataset_generation, authScope,
      specimenIds: ['Specimen/a', 'Specimen/b'], rows: [
        { specimenId: 'Specimen/a', patientId: 'Patient/shared' },
        { specimenId: 'Specimen/b', patientId: 'Patient/shared' },
      ], overflow: false, truncated: false },
    { project: scope.project, generation: scope.dataset_generation, authScope,
      specimenIds: ['Specimen/c'], rows: [{ specimenId: 'Specimen/c', patientId: 'Patient/other' }], overflow: false, truncated: false },
  ];
  const groupRows = patientIds => ({
    project: scope.project, generation: scope.dataset_generation, authScope, patientIds,
    groups: [], overflow: false, truncated: false,
  });
  const context = {
    db: { _query(query, bindVars, options) {
      calls.push({ query, bindVars, options });
      assert.equal(options.maxRuntime, 30);
      assert.equal(options.memoryLimit, 268_435_456);
      if (query === built.queries.specimenPage) return { toArray: () => [pageRows.shift()] };
      if (query === built.queries.specimenPatientPairs) return { toArray: () => [pairRows.shift()] };
      if (query === built.queries.patientBatch) return { toArray: () => [groupRows(bindVars.patient_ids)] };
      throw new Error('Unexpected query template');
    } },
    print: value => writes.push(value),
    Date,
    JSON,
    Set,
    Error,
  };
  vm.runInNewContext(built.script, context, { timeout: 1000 });
  const records = writes.filter(value => value.startsWith(RELATED_QUANTITY_DISCOVERY_MARKER))
    .map(value => JSON.parse(value.slice(RELATED_QUANTITY_DISCOVERY_MARKER.length)));
  assert.equal(records[0].type, 'begin');
  assert.deepEqual(records.filter(record => record.type === 'result').map(record => record.kind), [
    'specimenPage', 'specimenPatientPairs', 'specimenPage', 'specimenPatientPairs', 'patientBatch', 'patientBatch',
  ]);
  assert.deepEqual(records.filter(record => record.type === 'result').map(record => record.index), [0, 1, 2, 3, 4, 5]);
  assert.equal(records.at(-1).type, 'done');
  assert.deepEqual({ ...records.at(-1) }, {
    type: 'done', queryCount: 6, pageCount: 2, pairPageCount: 2, patientBatchCount: 2, distinctPatientCount: 2,
  });
  assert.equal(calls.length, 6);
});

test('generated script failures keep script context separate from the last completed query', () => {
  const built = buildRelatedQuantityPivotDiscoveryProcess(scope, bounds);
  const writes = [];
  const context = {
    db: { _query() {
      return { toArray: () => [{
        afterSpecimenKey: '', specimens: [], hasMore: 'invalid', nextAfterSpecimenKey: null,
      }] };
    } },
    print: value => writes.push(value), Date, JSON, Set, Error,
  };
  vm.runInNewContext(built.script, context, { timeout: 1000 });
  const records = writes.filter(value => value.startsWith(RELATED_QUANTITY_DISCOVERY_MARKER))
    .map(value => JSON.parse(value.slice(RELATED_QUANTITY_DISCOVERY_MARKER.length)));
  const result = records.find(record => record.type === 'result');
  const failure = records.find(record => record.type === 'failure');

  assert.equal(failure.code, 'specimen-page-shape');
  assert.equal(failure.queryHash, null, 'a script validation failure does not inherit the prior query hash');
  assert.equal(failure.queryIndex, 1, 'the script failure position follows one completed query');
  assert.equal(failure.startedAt, null);
  assert.equal(failure.finishedAt, null);
  assert.deepEqual(failure.lastCompletedQuery, {
    kind: 'specimenPage', phase: 'specimen-page', queryHash: built.manifest.queryHashes.specimenPage,
    index: 0, startedAt: result.startedAt, finishedAt: result.finishedAt,
  });
});

test('generated shape failure after a prior result reports only the last emitted query as completed', () => {
  const built = buildRelatedQuantityPivotDiscoveryProcess(scope, bounds);
  const writes = [];
  let queryIndex = 0;
  const context = {
    db: { _query(query) {
      queryIndex += 1;
      if (query === built.queries.specimenPage) {
        return { toArray: () => [{
          project: scope.project, generation: scope.dataset_generation, authScope,
          afterSpecimenKey: '', pageSize: bounds.specimenPageSize,
          specimens: [{ specimenId: 'Specimen/root-a', specimenKey: 'root-a' }],
          hasMore: false, nextAfterSpecimenKey: null,
        }] };
      }
      if (query === built.queries.specimenPatientPairs) return { toArray: () => [] };
      throw new Error('Unexpected query template');
    } },
    print: value => writes.push(value), Date, JSON, Set, Error,
  };
  vm.runInNewContext(built.script, context, { timeout: 1000 });
  const records = writes.filter(value => value.startsWith(RELATED_QUANTITY_DISCOVERY_MARKER))
    .map(value => JSON.parse(value.slice(RELATED_QUANTITY_DISCOVERY_MARKER.length)));
  const emittedResult = records.find(record => record.type === 'result');
  const failure = records.find(record => record.type === 'failure');

  assert.equal(queryIndex, 2);
  assert.equal(emittedResult.kind, 'specimenPage');
  assert.equal(failure.code, 'result-shape');
  assert.equal(failure.phase, 'specimen-patient-pairs');
  assert.equal(failure.queryHash, built.manifest.queryHashes.specimenPatientPairs);
  assert.equal(failure.queryIndex, 1);
  assert(Number.isSafeInteger(failure.startedAt));
  assert(Number.isSafeInteger(failure.finishedAt));
  assert(failure.finishedAt >= failure.startedAt);
  assert.deepEqual(failure.lastCompletedQuery, {
    kind: 'specimenPage', phase: 'specimen-page', queryHash: built.manifest.queryHashes.specimenPage,
    index: 0, startedAt: emittedResult.startedAt, finishedAt: emittedResult.finishedAt,
  });
});

test('an already-aborted signal rejects before launching the managed process', async () => {
  let spawnCount = 0;
  await assert.rejects(runRelatedQuantityPivotDiscoveryProcess({
    scope, bounds, container: 'owned-arango', signal: AbortSignal.abort(),
    spawnImpl() { spawnCount += 1; throw new Error('spawn must not run'); },
    onRecord() {},
  }), /Related quantity discovery was aborted/);
  assert.equal(spawnCount, 0);
});

test('one managed process streams a literal discovery into the existing multiplicity accumulator', async () => {
  const built = buildRelatedQuantityPivotDiscoveryProcess(scope, bounds);
  const remoteScriptPath = '/tmp/loom-arangosh.Abc123';
  const remotePid = 4567;
  const stdoutText = [
    `__LOOM_ARANGOSH_SCRIPT__:${remoteScriptPath}\n`,
    `__LOOM_RQ_DISCOVERY_PID__:${remotePid}:${remoteScriptPath}\n`,
    protocolLines(oneRootRecords(built.manifest)),
  ].join('');
  let spawnCount = 0;
  const result = await discoverCompleteRelatedQuantityRouteInProcess({
    scope, bounds, container: 'owned-arango', database: 'loom_dev',
    processOptions: { spawnImpl(command, args, options) {
      spawnCount += 1;
      assert.equal(command, 'rtk');
      assert.deepEqual(args.slice(0, 6), ['proxy', 'docker', 'exec', 'owned-arango', 'sh', '-lc']);
      assert.equal(options.stdio[0], 'ignore');
      return fakeProcess(stdoutText);
    } },
  });
  assert.equal(spawnCount, 1, 'the complete route discovery uses one remote shell process');
  assert.equal(result.discovery.complete, true);
  assert.equal(result.discovery.fullRouteCounts.leftJoinOutputRows, 1);
  assert.equal(result.discovery.fullRouteCounts.numericSum, undefined);
  assert.equal(result.pageCount, 1);
  assert.equal(result.pairPageCount, 1);
  assert.equal(result.patientBatchCount, 1);
  assert.equal(result.processIdentity.status, 'complete');
  assert.equal(result.processIdentity.done.distinctPatientCount, 1);
  assert.deepEqual(result.queryEvidence.map(entry => entry.query.phase), [
    'specimen-page', 'specimen-patient-pairs', 'patient-observation-groups',
  ]);
});

test('remote AQL failure after streamed results stops its exact process and never returns a partial oracle', async () => {
  const deadlineMs = 1000;
  const built = buildRelatedQuantityPivotDiscoveryProcess(scope, bounds, { deadlineMs });
  const remoteScriptPath = '/tmp/loom-arangosh.Err123';
  const remotePid = 7654;
  const failure = {
    type: 'failure',
    code: 'arango-query-error',
    phase: 'patient-observation-groups',
    queryHash: built.manifest.queryHashes.patientBatch,
    queryIndex: 2,
    startedAt: 20,
    finishedAt: 21,
    errorNum: 1501,
  };
  const partialResults = oneRootRecords(built.manifest)
    .filter(record => record.type === 'result')
    .slice(0, 2);
  const records = [
    { type: 'begin', manifest: built.manifest },
    ...partialResults,
    { ...failure, lastCompletedQuery: lastCompletedQuery(partialResults.at(-1)) },
  ];
  const child = new EventEmitter();
  child.stdout = new PassThrough();
  child.stderr = new PassThrough();
  child.kill = () => {
    throw new Error('Remote AQL failures must stop the managed remote process, not kill the local Docker client');
  };
  const stopCalls = [];
  setImmediate(() => child.stdout.write([
    `__LOOM_ARANGOSH_SCRIPT__:${remoteScriptPath}\n`,
    `__LOOM_RQ_DISCOVERY_PID__:${remotePid}:${remoteScriptPath}\n`,
    protocolLines(records),
  ].join('')));

  const outcome = await discoverCompleteRelatedQuantityRouteInProcess({
    scope,
    bounds,
    container: 'owned-arango',
    deadlineMs,
    processOptions: {
      spawnImpl: () => child,
      stopImpl: async request => {
        stopCalls.push(request);
        child.stdout.end();
        child.stderr.end();
        setImmediate(() => child.emit('close', null, 'SIGTERM'));
        return 'term';
      },
    },
  }).then(value => ({ value }), error => ({ error }));

  assert.equal(outcome.value, undefined, 'a partial stream must never be returned as a complete or partial oracle');
  assert.ok(outcome.error instanceof Error, 'a remote query failure must reject the discovery call');
  assert.equal(stopCalls.length, 1, 'the managed remote process is stopped once');
  assert.deepEqual(stopCalls.map(({ container, processId, scriptPath }) => ({ container, processId, scriptPath })), [{
    container: 'owned-arango', processId: remotePid, scriptPath: remoteScriptPath,
  }], 'remote cleanup targets the exact container, PID, and script identity');
  assert.equal(typeof stopCalls[0].spawnSyncImpl, 'function');
  assert.equal(outcome.error.discoveryIdentity.remoteStop, 'term');
  assert.deepEqual(outcome.error.discoveryIdentity.queryRecords, [
    {
      index: 0, kind: 'specimenPage', phase: 'specimen-page',
      queryHash: built.manifest.queryHashes.specimenPage, startedAt: 10, finishedAt: 11, protocolLineCount: 2,
    },
    {
      index: 1, kind: 'specimenPatientPairs', phase: 'specimen-patient-pairs',
      queryHash: built.manifest.queryHashes.specimenPatientPairs, startedAt: 12, finishedAt: 13, protocolLineCount: 3,
    },
  ], 'failure evidence preserves the ordered successful query chronology');
  assert.equal(outcome.error.discoveryIdentity.protocolLineCount, 4);
  assert.deepEqual(outcome.error.discoveryIdentity.terminalRecord, {
    type: 'failure', protocolLineCount: 4, afterQueryCount: 2,
    code: failure.code, phase: failure.phase, queryHash: failure.queryHash,
    queryIndex: failure.queryIndex, startedAt: failure.startedAt,
    finishedAt: failure.finishedAt, lastCompletedQuery: lastCompletedQuery(partialResults.at(-1)),
    errorNum: failure.errorNum,
  }, 'failure evidence identifies the terminal record position');
  const gaps = [];
  if (outcome.error.discoveryIdentity.durationMs >= deadlineMs) {
    gaps.push('the remote failure was not acted on before the overall deadline');
  }
  if (JSON.stringify(outcome.error.discoveryIdentity.remoteFailure) !== JSON.stringify({
    code: failure.code,
    phase: failure.phase,
    queryHash: failure.queryHash,
    queryIndex: failure.queryIndex,
    startedAt: failure.startedAt,
    finishedAt: failure.finishedAt,
    lastCompletedQuery: lastCompletedQuery(partialResults.at(-1)),
    errorNum: failure.errorNum,
  })) {
    gaps.push('the rejection did not preserve the failing remote code, phase, query hash, index, and error number');
  }
  assert.deepEqual(gaps, [], 'remote query failure handling must stop promptly and preserve its exact query identity');
});

test('duplicate terminal records reject the stream, stop the exact remote process, and expose no partial discovery', async () => {
  const built = buildRelatedQuantityPivotDiscoveryProcess(scope, bounds);
  const remoteScriptPath = '/tmp/loom-arangosh.Dup123';
  const remotePid = 4321;
  const child = new EventEmitter();
  child.stdout = new PassThrough();
  child.stderr = new PassThrough();
  child.kill = () => {
    throw new Error('Malformed protocol must stop the identified remote Arangosh process, not the local Docker client');
  };
  const stopCalls = [];
  setImmediate(() => child.stdout.write([
    `__LOOM_ARANGOSH_SCRIPT__:${remoteScriptPath}\n`,
    `__LOOM_RQ_DISCOVERY_PID__:${remotePid}:${remoteScriptPath}\n`,
    protocolLines([...oneRootRecords(built.manifest), oneRootRecords(built.manifest).at(-1)]),
  ].join('')));

  const outcome = await discoverCompleteRelatedQuantityRouteInProcess({
    scope,
    bounds,
    container: 'owned-arango',
    processOptions: {
      spawnImpl: () => child,
      stopImpl: async request => {
        stopCalls.push(request);
        child.stdout.end();
        child.stderr.end();
        setImmediate(() => child.emit('close', null, 'SIGTERM'));
        return 'term';
      },
    },
  }).then(value => ({ value }), error => ({ error }));

  assert.equal(outcome.value, undefined, 'a malformed terminal sequence must not return completed or partial route data');
  assert.match(outcome.error?.message ?? '', /duplicate or contradictory terminal records/);
  assert.equal(stopCalls.length, 1);
  assert.deepEqual(stopCalls.map(({ container, processId, scriptPath }) => ({ container, processId, scriptPath })), [{
    container: 'owned-arango', processId: remotePid, scriptPath: remoteScriptPath,
  }]);
  assert.equal(outcome.error.discoveryIdentity.remoteStop, 'term');
});

test('EOF without a terminal record rejects without exposing partial discovery', async () => {
  const built = buildRelatedQuantityPivotDiscoveryProcess(scope, bounds);
  const records = oneRootRecords(built.manifest).filter(record => record.type !== 'done');
  const stdoutText = [
    '__LOOM_ARANGOSH_SCRIPT__:/tmp/loom-arangosh.Eof123\n',
    '__LOOM_RQ_DISCOVERY_PID__:2468:/tmp/loom-arangosh.Eof123\n',
    protocolLines(records),
  ].join('');
  const outcome = await discoverCompleteRelatedQuantityRouteInProcess({
    scope, bounds, container: 'owned-arango',
    processOptions: { spawnImpl: () => fakeProcess(stdoutText) },
  }).then(value => ({ value }), error => ({ error }));

  assert.equal(outcome.value, undefined, 'incomplete EOF must never return accumulated route data');
  assert.match(outcome.error?.message ?? '', /ended without a complete successful protocol/);
  assert.equal(outcome.error.discoveryIdentity.protocolLineCount, 4);
  assert.equal(outcome.error.discoveryIdentity.queryRecords.length, 3);
});

test('out-of-order streamed query timestamps stop the remote process and reject without partial data', async () => {
  const built = buildRelatedQuantityPivotDiscoveryProcess(scope, bounds);
  const remoteScriptPath = '/tmp/loom-arangosh.OutOfOrder123';
  const remotePid = 2467;
  const child = new EventEmitter();
  child.stdout = new PassThrough();
  child.stderr = new PassThrough();
  child.kill = () => { throw new Error('The local transport is not the remote stop mechanism'); };
  const stopCalls = [];
  const records = oneRootRecords(built.manifest);
  records[2].startedAt = records[1].startedAt;
  setImmediate(() => child.stdout.write([
    `__LOOM_ARANGOSH_SCRIPT__:${remoteScriptPath}\n`,
    `__LOOM_RQ_DISCOVERY_PID__:${remotePid}:${remoteScriptPath}\n`,
    protocolLines(records),
  ].join('')));

  const outcome = await runRelatedQuantityPivotDiscoveryProcess({
    scope, bounds, container: 'owned-arango', spawnImpl: () => child,
    stopImpl: async request => {
      stopCalls.push(request);
      child.stdout.end();
      child.stderr.end();
      setImmediate(() => child.emit('close', null, 'SIGTERM'));
      return 'term';
    },
    onRecord() {},
  }).then(value => ({ value }), error => ({ error }));

  assert.equal(outcome.value, undefined);
  assert.match(outcome.error?.message ?? '', /timestamps are out of order/);
  assert.equal(stopCalls.length, 1);
  assert.equal(outcome.error.discoveryIdentity.remoteStop, 'term');
});

test('remote query failure rejects wrong identity, position, or timestamps', async () => {
  const built = buildRelatedQuantityPivotDiscoveryProcess(scope, bounds);
  const baseRecords = oneRootRecords(built.manifest).filter(record => record.type === 'result').slice(0, 2);
  const validFailure = {
    type: 'failure', code: 'arango-query-error', phase: 'patient-observation-groups',
    queryHash: built.manifest.queryHashes.patientBatch, queryIndex: 2,
    startedAt: 20, finishedAt: 21, lastCompletedQuery: lastCompletedQuery(baseRecords.at(-1)), errorNum: 1501,
  };
  const malformedFailures = [
    { ...validFailure, queryIndex: 1 },
    { ...validFailure, queryHash: built.manifest.queryHashes.specimenPage },
    { ...validFailure, phase: 'specimen-page' },
    { ...validFailure, startedAt: null },
    { ...validFailure, startedAt: 12 },
  ];

  for (const [index, failure] of malformedFailures.entries()) {
    const remoteScriptPath = `/tmp/loom-arangosh.BadFailure${index}X`;
    const remotePid = 2500 + index;
    const child = new EventEmitter();
    child.stdout = new PassThrough();
    child.stderr = new PassThrough();
    child.kill = () => { throw new Error('Malformed failure identity must stop the remote process'); };
    const stopCalls = [];
    setImmediate(() => child.stdout.write([
      `__LOOM_ARANGOSH_SCRIPT__:${remoteScriptPath}\n`,
      `__LOOM_RQ_DISCOVERY_PID__:${remotePid}:${remoteScriptPath}\n`,
      protocolLines([{ type: 'begin', manifest: built.manifest }, ...baseRecords, failure]),
    ].join('')));
    const outcome = await runRelatedQuantityPivotDiscoveryProcess({
      scope, bounds, container: 'owned-arango', spawnImpl: () => child,
      stopImpl: async request => {
        stopCalls.push(request);
        child.stdout.end();
        child.stderr.end();
        setImmediate(() => child.emit('close', null, 'SIGTERM'));
        return 'term';
      },
      onRecord() {},
    }).then(value => ({ value }), error => ({ error }));

    assert.equal(outcome.value, undefined, `malformed failure ${index} must not expose partial data`);
    assert.match(outcome.error?.message ?? '', /invalid query position|invalid query identity or timestamps/);
    assert.equal(stopCalls.length, 1);
    assert.equal(outcome.error.discoveryIdentity.remoteStop, 'term');
  }
});

test('script-level remote failure keeps query position separate from last completed query', async () => {
  const built = buildRelatedQuantityPivotDiscoveryProcess(scope, bounds);
  const lastResult = oneRootRecords(built.manifest)[1];
  const remoteScriptPath = '/tmp/loom-arangosh.ScriptFailure123';
  const remotePid = 2473;
  const child = new EventEmitter();
  child.stdout = new PassThrough();
  child.stderr = new PassThrough();
  child.kill = () => { throw new Error('Remote script failure must stop the remote process'); };
  const failure = {
    type: 'failure', code: 'specimen-page-shape', phase: 'specimen-page',
    queryHash: null, queryIndex: 1, startedAt: null, finishedAt: null,
    lastCompletedQuery: lastCompletedQuery(lastResult), errorNum: null,
  };
  const stopCalls = [];
  setImmediate(() => child.stdout.write([
    `__LOOM_ARANGOSH_SCRIPT__:${remoteScriptPath}\n`,
    `__LOOM_RQ_DISCOVERY_PID__:${remotePid}:${remoteScriptPath}\n`,
    protocolLines([{ type: 'begin', manifest: built.manifest }, lastResult, failure]),
  ].join('')));

  const outcome = await runRelatedQuantityPivotDiscoveryProcess({
    scope, bounds, container: 'owned-arango', spawnImpl: () => child,
    stopImpl: async request => {
      stopCalls.push(request);
      child.stdout.end();
      child.stderr.end();
      setImmediate(() => child.emit('close', null, 'SIGTERM'));
      return 'term';
    },
    onRecord() {},
  }).then(value => ({ value }), error => ({ error }));

  assert.equal(outcome.value, undefined);
  assert.equal(outcome.error.discoveryIdentity.remoteFailure.queryHash, null);
  assert.equal(outcome.error.discoveryIdentity.remoteFailure.queryIndex, 1);
  assert.equal(outcome.error.discoveryIdentity.remoteFailure.startedAt, null);
  assert.equal(outcome.error.discoveryIdentity.remoteFailure.lastCompletedQuery.index, 0);
  assert.notEqual(outcome.error.discoveryIdentity.remoteFailure.queryIndex,
    outcome.error.discoveryIdentity.remoteFailure.lastCompletedQuery.index);
  assert.equal(stopCalls.length, 1);
});

test('result after done rejects, stops the exact process, and exposes no partial discovery', async () => {
  const built = buildRelatedQuantityPivotDiscoveryProcess(scope, bounds);
  const remoteScriptPath = '/tmp/loom-arangosh.AfterDone123';
  const remotePid = 2469;
  const child = new EventEmitter();
  child.stdout = new PassThrough();
  child.stderr = new PassThrough();
  child.kill = () => { throw new Error('Protocol violation must stop the exact remote process'); };
  const stopCalls = [];
  const records = oneRootRecords(built.manifest);
  records.push({ ...records[1], index: records.filter(record => record.type === 'result').length });
  setImmediate(() => child.stdout.write([
    `__LOOM_ARANGOSH_SCRIPT__:${remoteScriptPath}\n`,
    `__LOOM_RQ_DISCOVERY_PID__:${remotePid}:${remoteScriptPath}\n`,
    protocolLines(records),
  ].join('')));

  const outcome = await runRelatedQuantityPivotDiscoveryProcess({
    scope, bounds, container: 'owned-arango',
    spawnImpl: () => child,
    stopImpl: async request => {
      stopCalls.push(request);
      child.stdout.end();
      child.stderr.end();
      setImmediate(() => child.emit('close', null, 'SIGTERM'));
      return 'term';
    },
    onRecord() {},
  }).then(value => ({ value }), error => ({ error }));

  assert.equal(outcome.value, undefined, 'records after done must never expose a discovery result');
  assert.match(outcome.error?.message ?? '', /after the terminal protocol boundary/);
  assert.deepEqual(stopCalls.map(({ container, processId, scriptPath }) => ({ container, processId, scriptPath })), [{
    container: 'owned-arango', processId: remotePid, scriptPath: remoteScriptPath,
  }]);
  assert.equal(outcome.error.discoveryIdentity.remoteStop, 'term');
});

test('failure after done rejects, stops the exact process, and exposes no partial discovery', async () => {
  const built = buildRelatedQuantityPivotDiscoveryProcess(scope, bounds);
  const remoteScriptPath = '/tmp/loom-arangosh.FailureAfterDone123';
  const remotePid = 2470;
  const child = new EventEmitter();
  child.stdout = new PassThrough();
  child.stderr = new PassThrough();
  child.kill = () => { throw new Error('Protocol violation must stop the exact remote process'); };
  const stopCalls = [];
  const records = [...oneRootRecords(built.manifest), {
    type: 'failure', code: 'late-failure', phase: 'after-done', queryHash: null,
    queryIndex: null, startedAt: null, finishedAt: null, errorNum: null,
  }];
  setImmediate(() => child.stdout.write([
    `__LOOM_ARANGOSH_SCRIPT__:${remoteScriptPath}\n`,
    `__LOOM_RQ_DISCOVERY_PID__:${remotePid}:${remoteScriptPath}\n`,
    protocolLines(records),
  ].join('')));

  const outcome = await runRelatedQuantityPivotDiscoveryProcess({
    scope, bounds, container: 'owned-arango',
    spawnImpl: () => child,
    stopImpl: async request => {
      stopCalls.push(request);
      child.stdout.end();
      child.stderr.end();
      setImmediate(() => child.emit('close', null, 'SIGTERM'));
      return 'term';
    },
    onRecord() {},
  }).then(value => ({ value }), error => ({ error }));

  assert.equal(outcome.value, undefined);
  assert.match(outcome.error?.message ?? '', /failure appeared outside the terminal protocol boundary/);
  assert.deepEqual(stopCalls.map(({ container, processId, scriptPath }) => ({ container, processId, scriptPath })), [{
    container: 'owned-arango', processId: remotePid, scriptPath: remoteScriptPath,
  }]);
  assert.equal(outcome.error.discoveryIdentity.remoteStop, 'term');
});

test('failed remote stop escalates local transport SIGTERM to bounded SIGKILL', async () => {
  const deadlineMs = 1000;
  const built = buildRelatedQuantityPivotDiscoveryProcess(scope, bounds, { deadlineMs });
  const remoteScriptPath = '/tmp/loom-arangosh.StopFail123';
  const remotePid = 2471;
  const child = new EventEmitter();
  child.stdout = new PassThrough();
  child.stderr = new PassThrough();
  const events = [];
  child.kill = signalName => {
    events.push(`local-${signalName}`);
    if (signalName === 'SIGKILL') {
      child.stdout.end();
      child.stderr.end();
      setImmediate(() => child.emit('close', null, signalName));
    }
    return true;
  };
  const failure = {
    type: 'failure', code: 'arango-query-error', phase: 'patient-observation-groups',
    queryHash: built.manifest.queryHashes.patientBatch, queryIndex: 0,
    startedAt: 20, finishedAt: 21, errorNum: 1501,
  };
  setImmediate(() => child.stdout.write([
    `__LOOM_ARANGOSH_SCRIPT__:${remoteScriptPath}\n`,
    `__LOOM_RQ_DISCOVERY_PID__:${remotePid}:${remoteScriptPath}\n`,
    protocolLines([{ type: 'begin', manifest: built.manifest }, failure]),
  ].join('')));

  const outcome = await discoverCompleteRelatedQuantityRouteInProcess({
    scope, bounds, container: 'owned-arango', deadlineMs,
    processOptions: {
      spawnImpl: () => child,
      transportCloseTimeoutMs: 5,
      stopImpl: async () => {
        events.push('remote-stop-attempt');
        throw new Error('remote stop confirmation unavailable');
      },
    },
  }).then(value => ({ value }), error => ({ error }));

  assert.equal(outcome.value, undefined, 'a stop failure must not expose a partial result');
  assert.match(outcome.error?.message ?? '', /Remote related quantity discovery failed/);
  assert.equal(outcome.error.discoveryIdentity.remoteStop, 'unverified');
  assert.equal(outcome.error.discoveryIdentity.remoteStopError, 'remote stop confirmation unavailable');
  assert.deepEqual(events, ['remote-stop-attempt', 'local-SIGTERM', 'local-SIGKILL'],
    'local transport termination escalates only after the failed remote-stop attempt');
  assert.equal(outcome.error.discoveryIdentity.localTransportClosed, true);
  assert.equal(outcome.error.discoveryIdentity.localTransportSignal, 'SIGKILL');
  assert.equal(outcome.error.discoveryIdentity.deadlineReached, undefined);
});

test('unclosable local transport settles bounded and remains fatal with remote stop unverified', async () => {
  const built = buildRelatedQuantityPivotDiscoveryProcess(scope, bounds);
  const remoteScriptPath = '/tmp/loom-arangosh.NeverClose123';
  const remotePid = 2474;
  const child = new EventEmitter();
  child.stdout = new PassThrough();
  child.stderr = new PassThrough();
  const events = [];
  child.kill = signalName => { events.push(`local-${signalName}`); return true; };
  setImmediate(() => child.stdout.write([
    `__LOOM_ARANGOSH_SCRIPT__:${remoteScriptPath}\n`,
    `__LOOM_RQ_DISCOVERY_PID__:${remotePid}:${remoteScriptPath}\n`,
    protocolLines([{ type: 'begin', manifest: built.manifest }, {
      type: 'failure', code: 'arango-query-error', phase: 'specimen-page',
      queryHash: built.manifest.queryHashes.specimenPage, queryIndex: 0,
      startedAt: 20, finishedAt: 21, lastCompletedQuery: null, errorNum: 1501,
    }]),
  ].join('')));
  const startedAt = Date.now();
  const outcome = await runRelatedQuantityPivotDiscoveryProcess({
    scope, bounds, container: 'owned-arango', transportCloseTimeoutMs: 5,
    spawnImpl: () => child,
    stopImpl: async () => { events.push('remote-stop-attempt'); throw new Error('remote stop unavailable'); },
    onRecord() {},
  }).then(value => ({ value }), error => ({ error }));

  assert.equal(outcome.value, undefined, 'failed stop and failed transport cleanup must not expose data');
  assert.match(outcome.error?.message ?? '', /Remote related quantity discovery failed/);
  assert.equal(outcome.error.discoveryIdentity.remoteStop, 'unverified');
  assert.equal(outcome.error.discoveryIdentity.localTransportClosed, false);
  assert.deepEqual(events, ['remote-stop-attempt', 'local-SIGTERM', 'local-SIGKILL']);
  assert.ok(Date.now() - startedAt < 1000, 'unclosable local cleanup remains bounded');
  child.stdout.end();
  child.stderr.end();
  child.emit('close', null, 'SIGKILL');
});

test('abort plus remote stop failure terminates the local transport before the default deadline', async () => {
  const built = buildRelatedQuantityPivotDiscoveryProcess(scope, bounds);
  const remoteScriptPath = '/tmp/loom-arangosh.AbortStopFail123';
  const remotePid = 2472;
  const child = new EventEmitter();
  child.stdout = new PassThrough();
  child.stderr = new PassThrough();
  const events = [];
  child.kill = signalName => {
    events.push(`local-${signalName}`);
    child.stdout.end();
    child.stderr.end();
    setImmediate(() => child.emit('close', null, signalName));
    return true;
  };
  const controller = new AbortController();
  setImmediate(() => child.stdout.write([
    `__LOOM_ARANGOSH_SCRIPT__:${remoteScriptPath}\n`,
    `__LOOM_RQ_DISCOVERY_PID__:${remotePid}:${remoteScriptPath}\n`,
    protocolLines([{ type: 'begin', manifest: built.manifest }]),
  ].join('')));
  const startedAt = Date.now();
  const running = runRelatedQuantityPivotDiscoveryProcess({
    scope, bounds, container: 'owned-arango', signal: controller.signal,
    spawnImpl: () => child,
    stopImpl: async request => {
      events.push(`remote-stop-${request.processId}`);
      throw new Error('remote stop confirmation unavailable');
    },
    onRecord() {},
  });
  setTimeout(() => controller.abort(), 10);
  const outcome = await running.then(value => ({ value }), error => ({ error }));

  assert.equal(outcome.value, undefined);
  assert.match(outcome.error?.message ?? '', /Related quantity discovery was aborted/);
  assert.equal(outcome.error.discoveryIdentity.remoteStop, 'unverified');
  assert.equal(outcome.error.discoveryIdentity.localTransportClosed, true);
  assert.deepEqual(events, [`remote-stop-${remotePid}`, 'local-SIGTERM']);
  assert.ok(Date.now() - startedAt < 2000, 'abort plus stop failure settles far earlier than the 30-minute default deadline');
});

test('deadline cancellation targets the exact remote Arangosh identity and waits for stop confirmation', async () => {
  const built = buildRelatedQuantityPivotDiscoveryProcess(scope, bounds, { deadlineMs: 5000 });
  const remoteScriptPath = '/tmp/loom-arangosh.Zyx987';
  const remotePid = 9876;
  const child = new EventEmitter();
  child.stdout = new PassThrough();
  child.stderr = new PassThrough();
  child.kill = () => {
    throw new Error('The local Docker client must not be used as the remote cancellation mechanism');
  };
  const stopCalls = [];
  const controller = new AbortController();
  const running = runRelatedQuantityPivotDiscoveryProcess({
    scope, bounds, container: 'owned-arango', deadlineMs: 5000, signal: controller.signal,
    spawnImpl: () => {
      setImmediate(() => child.stdout.write(
        `__LOOM_ARANGOSH_SCRIPT__:${remoteScriptPath}\n__LOOM_RQ_DISCOVERY_PID__:${remotePid}:${remoteScriptPath}\n${protocolLines([{ type: 'begin', manifest: { ...built.manifest, deadlineMs: 5000 } }])}`,
      ));
      return child;
    },
    stopImpl: async request => {
      stopCalls.push(request);
      child.stdout.end();
      child.stderr.end();
      setImmediate(() => child.emit('close', null, 'SIGTERM'));
      return 'term';
    },
    onRecord() {},
  });
  setTimeout(() => controller.abort(), 10);
  await assert.rejects(running, /Related quantity discovery was aborted/);
  assert.equal(stopCalls.length, 1);
  assert.equal(stopCalls[0].container, 'owned-arango');
  assert.equal(stopCalls[0].processId, remotePid);
  assert.equal(stopCalls[0].scriptPath, remoteScriptPath);
});
