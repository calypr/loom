import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import vm from 'node:vm';
import {
  generateTraceModule,
  resolveTlaToolsJar,
  runTlc,
  traceProgressFailureForOutput,
} from '../../../verify-builder-traces.mjs';

const selfTestSource = readFileSync(new URL('../../../self-test-builder-traces.mjs', import.meta.url), 'utf8');
const start = selfTestSource.indexOf('const policyExpectedFailureMatches =');
const end = selfTestSource.indexOf('const runPolicyNativeJourneySelfTest =', start);
const receiptStart = selfTestSource.indexOf('const policyNativeJourneySelfTestReceipt =');
const receiptEnd = selfTestSource.indexOf('const policyMutationControlNamesMatch =', receiptStart);

test('Policy expected-failure matching uses structured TLC outcomes and fails closed', () => {
  assert.notEqual(start, -1, 'Policy structured-result matcher exists in the self-test');
  assert.notEqual(end, -1, 'Policy structured-result test has a bounded source region');
  const matcherSource = selfTestSource.slice(start, end);
  const passed = vm.runInNewContext(matcherSource + '\npolicyStructuredFailureBoundaryTest();');
  assert.equal(passed, true);
});

test('Policy self-test receipt carries and validates the native case key', () => {
  assert.notEqual(receiptStart, -1, 'Policy receipt builder exists in the self-test');
  assert.notEqual(receiptEnd, -1, 'Policy receipt check has a bounded source region');
  const receiptSource = selfTestSource.slice(receiptStart, receiptEnd);
  const passed = vm.runInNewContext(receiptSource + '\nselfTestReceiptWriterTest();');
  assert.equal(passed, true);
});

test('TraceProgress reports the blocked next action and rejects unrelated failure classes', () => {
  const trace = {
    model: 'ReconcileLifecycle',
    actions: [
      { label: 'WorkspaceChangeStartsReconcile(B)' },
      { label: 'ReconcileSuccess(B)' },
      { label: 'LateReconcileIgnored(A)' },
    ],
  };
  // TLC excerpt from the retained 8f29425 fast-gate run; stdout SHA-256 a83c3167c6fc36a97119596859a1a7b47a32c20b5490a097e9b780d6a9798c0c.
  const retainedTlcOutput = [
    'TLC2 Version 2.19 of 08 August 2024 (rev: 5a47802)',
    'Error: Invariant TraceProgress is violated.',
    'Error: The behavior up to this point is:',
    'State 3: <TraceNext line 177, col 3 to line 193, col 40 of module TraceCheck1>',
    '/\\ lastAction = "WorkspaceChangeStartsReconcile(B)"',
    '/\\ traceIndex = 2',
    'State 4: <TraceNext line 177, col 3 to line 193, col 40 of module TraceCheck1>',
    '/\\ lastAction = "ReconcileSuccess(B)"',
    '/\\ traceIndex = 3',
    '4 states generated, 4 distinct states found, 0 states left on queue.',
    'The depth of the complete state graph search is 4.',
  ].join('\n');
  const processResult = { status: 12, signal: null, error: null };
  const emptyObservation = () => ({
    status: 'idle', canApply: false, proposalIdPresent: false,
    previewReceiptPresent: false, rowsPresent: false, receiptPresent: false,
    reconcile: {},
  });
  const genericTraceForGeneration = {
    ...trace,
    initialObservation: emptyObservation(),
    actions: trace.actions.map((action) => ({ ...action, observation: emptyObservation() })),
  };
  const prefixedModule = generateTraceModule(genericTraceForGeneration, 'TraceProgressPrefixed', false);
  assert.equal(prefixedModule.traceIndexOffset, 1);
  assert.match(prefixedModule.moduleText, /TraceActions == <<\s*"Init",\s*"WorkspaceChangeStartsReconcile\(B\)"/u);
  assert.match(prefixedModule.moduleText, /traceIndex = 0/u);

  const blocked = traceProgressFailureForOutput(
    retainedTlcOutput, processResult, trace, prefixedModule.traceIndexOffset,
  );
  assert.deepEqual(blocked, { actionIndex: 2, actionLabel: 'LateReconcileIgnored(A)' },
    'The retained TLC run completed ReconcileSuccess(B); LateReconcileIgnored(A) is the blocked next action.');
  assert.notEqual(blocked?.actionLabel, trace.actions[1].label,
    'The report must not attribute the failure to the previously completed ReconcileSuccess(B) action.');
  assert.notEqual(blocked?.actionLabel, 'LateReconcileIgnored(P2)',
    'A different late action must not match the blocked transition label.');

  assert.equal(traceProgressFailureForOutput(
    retainedTlcOutput.replace('TraceProgress', 'TypeOK'), processResult, trace, prefixedModule.traceIndexOffset,
  ), null, 'Other invariants retain their existing failure attribution.');
  assert.equal(traceProgressFailureForOutput(retainedTlcOutput, {
    ...processResult, error: new Error('runner failed'),
  }, trace, prefixedModule.traceIndexOffset), null, 'Runner errors never become trace-completion results.');
  assert.equal(traceProgressFailureForOutput(retainedTlcOutput, {
    ...processResult, signal: 'SIGTERM',
  }, trace, prefixedModule.traceIndexOffset), null, 'Timeouts never become trace-completion results.');
  assert.equal(traceProgressFailureForOutput(retainedTlcOutput.replace('4 states generated, 4 distinct',
    '0 states generated, 0 distinct'), processResult, trace, prefixedModule.traceIndexOffset), null,
  'No-initial-state reports are not classified as blocked next actions.');
  assert.equal(traceProgressFailureForOutput(retainedTlcOutput.replace('/\\ traceIndex = 3',
    '/\\ traceIndex = 4'), processResult, trace, prefixedModule.traceIndexOffset), null,
  'An end index with no corresponding next action is rejected.');
  assert.equal(traceProgressFailureForOutput(retainedTlcOutput, processResult, {
    model: 'ReconcileLifecycle', actions: [trace.actions[0], trace.actions[1], {}],
  }, prefixedModule.traceIndexOffset), null, 'An unknown target action label is rejected.');

  const constructionTrace = {
    model: 'ConstructionLifecycle',
    actions: [{ label: 'ProposalComplete(P1)' }, { label: 'StartPreview(P1)' }, { label: 'PreviewComplete(P1)' }],
  };
  const constructionModule = generateTraceModule({
    ...constructionTrace,
    initialObservation: emptyObservation(),
    actions: constructionTrace.actions.map((action) => ({ ...action, observation: emptyObservation() })),
  }, 'TraceProgressConstruction', false);
  assert.equal(constructionModule.traceIndexOffset, 1);
  assert.match(constructionModule.moduleText, /TraceActions == <<\s*"Init",\s*"ProposalComplete\(P1\)"/u);
  const constructionTlcOutput = retainedTlcOutput.replaceAll('traceIndex = 3', 'traceIndex = 2');
  assert.deepEqual(traceProgressFailureForOutput(constructionTlcOutput, processResult, constructionTrace,
    constructionModule.traceIndexOffset),
    { actionIndex: 1, actionLabel: 'StartPreview(P1)' },
    'The generic trace Init offset maps the completed prefix to the next ConstructionLifecycle action.');

  const group = { stepId: 'group-step', keys: ['source-id'], aggregate: 'COUNT_ROWS' };
  const saved = {
    explorerId: 'explorer-fixture', group, relatedSource: null,
    workspace: { documents: [{
      output: { id: 'output-fixture' },
      columns: [{ columnId: 'source-id', label: 'Source ID' }],
      construction: { steps: [{
        id: 'group-step',
        operation: { kind: 'GROUP', group: {
          keys: [{ inputColumnId: 'source-id' }], aggregates: [{ operation: 'COUNT_ROWS' }],
        } },
        outputs: [{ id: 'group-output', label: 'Source ID count' }],
      }] },
    }] },
    outputId: 'output-fixture', draftVersion: 0, draftDigest: 'sha256:fixture',
  };
  const cdaJourneyCase = { casTransitionCount: 0, maxMountEpoch: 0 };
  const cdaTrace = { model: 'BuilderJourney', actions: [
    {
      label: 'GroupEditCancel', observedEpoch: 0, observation: emptyObservation(),
      groupLifecycle: { mountEpoch: 0, savedBefore: saved, savedAfter: saved },
    },
    { label: 'Reload' }, { label: 'LatePreviewCompleteIgnored(P1)' },
  ] };
  const cdaModule = generateTraceModule(cdaTrace, 'TraceProgressCda', true, cdaJourneyCase);
  assert.equal(cdaModule.traceIndexOffset, 0);
  assert.match(cdaModule.moduleText, /TraceActions == <<\s*"GroupEditCancel"/u);
  assert.match(cdaModule.moduleText, /traceIndex = 0/u);
  const cdaTlcOutput = retainedTlcOutput.replaceAll('traceIndex = 3', 'traceIndex = 2');
  assert.deepEqual(traceProgressFailureForOutput(cdaTlcOutput, processResult, cdaTrace, cdaModule.traceIndexOffset),
    { actionIndex: 2, actionLabel: 'LatePreviewCompleteIgnored(P1)' },
    'CDA traceIndex is zero-based and directly indexes its action list.');

  const replay = runTlc('unused-tlc-jar-path', genericTraceForGeneration, 1, false, null, null,
    () => ({ status: 12, signal: null, error: null, stdout: retainedTlcOutput, stderr: '' }));
  try {
    assert.equal(replay.failure.message, 'TLC did not complete the trace');
    assert.equal(replay.failure.actionIndex, 2);
    assert.equal(replay.failure.actionLabel, 'LateReconcileIgnored(A)');
    assert.equal(replay.failure.invariant, 'TraceProgress');
    assert.equal(replay.failure.stage, 'TLC');
    assert.equal(replay.failure.processExitCode, 12);
  } finally {
    rmSync(replay.runDirectory, { recursive: true, force: true });
  }
});

test('TLC resolver verifies explicit overrides and never falls back from an invalid override', () => {
  const directory = mkdtempSync(join(tmpdir(), 'builder-tlc-resolver-test-'));
  try {
    const overridePath = join(directory, 'explicit-invalid.jar');
    const cacheDirectory = join(directory, 'cache');
    mkdirSync(cacheDirectory);
    writeFileSync(overridePath, 'invalid explicit jar');
    writeFileSync(join(cacheDirectory, 'tla2tools-1.7.4.jar'), 'invalid cached jar');

    assert.throws(() => resolveTlaToolsJar({
      env: { TLA2TOOLS_JAR: overridePath },
      cacheDirectory,
    }), error => error.message.includes('checksum mismatch') &&
      error.message.includes('explicit-invalid.jar'),
    'An explicit bad override must fail at that path even when a cache file exists.');

    assert.throws(() => resolveTlaToolsJar({
      env: {},
      cacheDirectory: join(directory, 'missing-cache'),
    }), error => error.message.includes(join(directory, 'missing-cache', 'tla2tools-1.7.4.jar')),
    'Cache-only resolution must fail locally rather than downloading or searching elsewhere.');
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
