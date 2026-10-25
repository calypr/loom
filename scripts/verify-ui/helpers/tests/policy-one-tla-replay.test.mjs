import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import {
  buildPolicyOneMutationControls,
  compilePolicyOneJourneyTrace,
  policyOneTlaReplayModelPath,
  validatePolicyOneJourneyTrace,
} from '../policy-one-tla-replay.mjs';
import { makeSyntheticPolicyOneJourneyTrace } from './fixtures/policy-one-journey.mjs';

const TLC_SHA256 = '936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88';
const DEFAULT_TLC_JAR = '/private/var/folders/v_/j3zplgbs155cgwtqjftj377nd_4gyt/T/loom-builder-interactions-tools/tla2tools-1.7.4.jar';
const TLC_JAR = process.env.LOOM_TLA_TOOLS_JAR ?? DEFAULT_TLC_JAR;
const STANDALONE_CFG = fileURLToPath(new URL('../../../../verification/builder-interactions/PolicyOneDisagreement.cfg', import.meta.url));

function summary(output) {
  return output.split('\n').filter(line => /Error:|Invariant .*violated|Parse Error|Encountered|Was expecting|Attempted|nonexistent field|Semantic processing|Could not parse|Model checking completed|states generated|complete state graph/.test(line))
    .slice(0, 12).join('\n');
}

function checkWithTLC(trace, name) {
  assert(existsSync(TLC_JAR), `TLC 1.7.4 jar must exist at ${TLC_JAR}`);
  const digest = createHash('sha256').update(readFileSync(TLC_JAR)).digest('hex');
  assert.equal(digest, TLC_SHA256, 'TLC jar must match the pinned 1.7.4 digest');
  const compiled = compilePolicyOneJourneyTrace(trace, { moduleName: name });
  const runDir = mkdtempSync(join(tmpdir(), 'loom-policy-one-tlc-'));
  const statesDir = join(runDir, 'states');
  mkdirSync(statesDir);
  writeFileSync(join(runDir, 'PolicyOneDisagreement.tla'), compiled.baseModelText);
  writeFileSync(join(runDir, `${compiled.moduleName}.tla`), compiled.modelText);
  writeFileSync(join(runDir, `${compiled.moduleName}.cfg`), compiled.configText);
  const result = spawnSync('java', [
    '-XX:+UseParallelGC', '-cp', TLC_JAR, 'tlc2.TLC', '-workers', '1',
    '-metadir', statesDir, '-config', `${compiled.moduleName}.cfg`, `${compiled.moduleName}.tla`,
  ], { cwd: runDir, encoding: 'utf8', timeout: 30000, maxBuffer: 16 * 1024 * 1024 });
  const output = `${result.stdout ?? ''}${result.stderr ?? ''}`;
  const status = result.status;
  rmSync(runDir, { recursive: true, force: true });
  return { status, output, compiled, error: result.error };
}

function checkStandaloneModelWithTLC() {
  assert(existsSync(TLC_JAR), `TLC 1.7.4 jar must exist at ${TLC_JAR}`);
  const digest = createHash('sha256').update(readFileSync(TLC_JAR)).digest('hex');
  assert.equal(digest, TLC_SHA256, 'TLC jar must match the pinned 1.7.4 digest');
  const runDir = mkdtempSync(join(tmpdir(), 'loom-policy-one-standalone-tlc-'));
  const statesDir = join(runDir, 'states');
  mkdirSync(statesDir);
  writeFileSync(join(runDir, 'PolicyOneDisagreement.tla'), readFileSync(policyOneTlaReplayModelPath, 'utf8'));
  writeFileSync(join(runDir, 'PolicyOneDisagreement.cfg'), readFileSync(STANDALONE_CFG, 'utf8'));
  const result = spawnSync('java', [
    '-XX:+UseParallelGC', '-cp', TLC_JAR, 'tlc2.TLC', '-workers', '1',
    '-metadir', statesDir, '-config', 'PolicyOneDisagreement.cfg', 'PolicyOneDisagreement.tla',
  ], { cwd: runDir, encoding: 'utf8', timeout: 30000, maxBuffer: 16 * 1024 * 1024 });
  const output = `${result.stdout ?? ''}${result.stderr ?? ''}`;
  rmSync(runDir, { recursive: true, force: true });
  return { status: result.status, output, error: result.error };
}

function makeSyntheticReconcileLedgerTrace() {
  const trace = makeSyntheticPolicyOneJourneyTrace();
  const ownerScope = trace.transportLedger[0].ownerScope;
  const transport = ({
    transportIndex, requestEventIndex, settlementEventIndex, method, path, requestBody,
    requestId = null, status = 200, response = {}, classification = 'read-only',
    readOnlyReason = 'Absent', receiptProducerTransportIndices = [],
    compatibleProposalTransportIndices = [], previewConsumerTransportIndices = [],
    invalidatedByTransportIndices = [], reconcileOwner = null, reconcileCAS = null,
    ownerInvalidationObserved = false, reconcileDisposition = 'Absent',
  }) => ({
    transportIndex,
    requestEventIndex,
    settlementEventIndex,
    resourcePath: `${ownerScope}/authoring/v2/${path}`,
    ownerScope,
    method,
    path,
    classification,
    mutatesDraft: false,
    actionLabel: null,
    actionEvent: null,
    requestId,
    requestCaptureId: null,
    requestBody,
    settlement: 'response',
    responseOwnerIndex: transportIndex,
    status,
    response: { status, ...response },
    receiptProducerTransportIndices,
    hasInitialReceiptProducer: false,
    compatibleProposalTransportIndices,
    previewConsumerTransportIndices,
    invalidatedByTransportIndices,
    reconcileOwner,
    reconcileCAS,
    ownerInvalidationObserved,
    reconcileDisposition,
    readOnlyReason,
  });

  const receiptId = 'policy-one-ledger-unit-receipt';
  const firstReconcileIndex = trace.transportLedger.length + 1;
  const secondReconcileIndex = firstReconcileIndex + 1;
  const previewIndex = secondReconcileIndex + 1;
  const addReconcile = ({ transportIndex, requestEventIndex, settlementEventIndex, generation,
    invalidatedByTransportIndices = [], ownerInvalidationObserved }) => transport({
    transportIndex,
    requestEventIndex,
    settlementEventIndex,
    method: 'POST',
    path: 'reconcile',
    requestId: `builder-${generation}-1`,
    requestBody: { snapshotToken: 'snapshot-ledger-unit', draftVersion: 5, draftDigest: 'draft-ledger-unit' },
    classification: 'reconcile',
    response: {
      requestId: `builder-${generation}-1`,
      receiptId,
      receiptIds: [receiptId],
      outputIds: ['output-1'],
      snapshotToken: 'snapshot-ledger-unit',
      intentDigest: 'intent-ledger-unit',
    },
    previewConsumerTransportIndices: [previewIndex],
    invalidatedByTransportIndices,
    reconcileOwner: { requestId: `builder-${generation}-1`, compileGeneration: generation, attempt: 1 },
    reconcileCAS: {
      snapshotToken: 'snapshot-ledger-unit',
      draftVersion: 5,
      draftDigest: 'draft-ledger-unit',
      outputId: null,
    },
    ownerInvalidationObserved,
    reconcileDisposition: ownerInvalidationObserved
      ? 'invalidated-by-captured-owner-event'
      : 'no-captured-owner-invalidation',
  });
  trace.transportLedger.push(
    addReconcile({
      transportIndex: firstReconcileIndex,
      requestEventIndex: 17,
      settlementEventIndex: 20,
      generation: 41,
      invalidatedByTransportIndices: [secondReconcileIndex],
      ownerInvalidationObserved: true,
    }),
    addReconcile({
      transportIndex: secondReconcileIndex,
      requestEventIndex: 18,
      settlementEventIndex: 19,
      generation: 42,
      ownerInvalidationObserved: false,
    }),
    transport({
      transportIndex: previewIndex,
      requestEventIndex: 21,
      settlementEventIndex: 22,
      method: 'POST',
      path: 'preview',
      requestBody: { receiptId, outputId: 'output-1' },
      response: { receiptIds: [], outputIds: ['output-1'] },
      classification: 'read-only',
      readOnlyReason: 'Receipt-backed preview read',
      receiptProducerTransportIndices: [firstReconcileIndex, secondReconcileIndex],
    }),
    transport({
      transportIndex: previewIndex + 1,
      requestEventIndex: 23,
      settlementEventIndex: 24,
      method: 'GET',
      path: 'builder',
      requestBody: null,
      response: { outputIds: [] },
      classification: 'read-only',
      readOnlyReason: 'BuilderState read',
    }),
  );
  return trace;
}

test('compiler specializes the base witness to actual trace constants and emits replay checks', () => {
  const trace = makeSyntheticPolicyOneJourneyTrace();
  assert.equal(validatePolicyOneJourneyTrace(trace), trace);
  const compiled = compilePolicyOneJourneyTrace(trace);
  const groupApply = trace.actions.find(entry => entry.label === 'GroupApplySubmitted');
  const groupApplied = trace.actions.find(entry => entry.label === 'GroupApplied');
  const allApplied = trace.actions.find(entry => entry.label === 'AllApplied').observation.saved;
  const removalApplied = trace.actions.find(entry => entry.label === 'RemovalApplied');
  assert.equal(Object.hasOwn(groupApply.request.body, 'outputId'), false,
    'commands API outputId belongs on the command DTO');
  assert.equal(groupApply.request.body.commands[0].outputId, 'output-1');
  assert.equal(groupApplied.savedBefore.draftDigest, 'draft-root');
  assert.equal(groupApplied.savedAfter.draftDigest, 'draft-before');
  assert.deepEqual(groupApplied.savedAfter.group.rowValues, [],
    'saved Group row values come from the top-level ConstructionStep.rowValues DTO');
  assert.equal(removalApplied.savedBefore.group.rowValues[0].policy, 'ALL');
  assert.equal(allApplied.group.inputColumnBinding.columnId, 'patient-id-values');
  assert.equal(allApplied.group.outputColumnBinding.columnId, 'row_value_d953ae59403369dab2d0011c');
  assert.notEqual(allApplied.group.inputColumnBinding.columnId, allApplied.group.outputColumnBinding.columnId);
  assert.equal(allApplied.group.outputColumnBinding.type, 'array');
  assert.equal(removalApplied.savedAfter.group, null,
    'removal mutates the saved Group while retaining the added source column');
  assert(removalApplied.savedAfter.sourceColumns.some(column => column.id === 'patient-id-values'));
  assert.match(compiled.modelText, /ObservationMatches\(traceIndex \+ 1, model, model'\)/);
  assert.match(compiled.modelText, /ObservedResponseOwnership/);
  assert.match(compiled.modelText, /ObservedPolicyCAS/);
  assert.match(compiled.configText, /OneCaptureId = "capture-one-random"/);
  assert.match(compiled.configText, /AllCaptureId = "capture-all-random"/);
  assert.match(compiled.configText, /AddedPatientIdOutputColumnId = "row_value_d953ae59403369dab2d0011c"/);
  assert.match(compiled.baseModelText, /SourcePatientIds == \{"dev-patient-001", "dev-patient-002"\}/);
  assert.match(compiled.claim, /no concurrency or exhaustive interleaving claim/);
});

test('the standalone policy witness requires eventual restoration under weak fairness', t => {
  const result = checkStandaloneModelWithTLC();
  t.diagnostic(summary(result.output));
  assert.equal(result.error, undefined, result.error?.message);
  assert.equal(result.status, 0, summary(result.output));
  assert.match(result.output, /Model checking completed\. No error has been found\./);
});

test('TLC replays the complete synthetic lifecycle against captured workspace and protocol facts', t => {
  const result = checkWithTLC(makeSyntheticPolicyOneJourneyTrace(), 'PolicyOneReplayPositive');
  t.diagnostic(summary(result.output));
  assert.equal(result.error, undefined, result.error?.message);
  assert.equal(result.status, 0, summary(result.output));
  assert.match(result.output, /Model checking completed\. No error has been found\./);
});

test('TLC validates scoped reconcile ownership, receipt sets, and read-only routes in a unit trace', t => {
  const trace = makeSyntheticReconcileLedgerTrace();
  const compiled = compilePolicyOneJourneyTrace(trace, { moduleName: 'PolicyOneReconcileLedgerUnit' });
  assert.match(compiled.modelText, /CompatibleInvalidations/);
  assert.match(compiled.modelText, /CompatiblePreviewConsumers/);
  assert.match(compiled.modelText, /requestBodyFingerprint/);
  assert.match(compiled.modelText, /CompatibleProposalReceipts/);

  const positive = checkWithTLC(trace, 'PolicyOneReconcileLedgerPositive');
  t.diagnostic(summary(positive.output));
  assert.equal(positive.error, undefined, positive.error?.message);
  assert.equal(positive.status, 0, summary(positive.output));
  assert.match(positive.output, /Model checking completed\. No error has been found\./);

  const collapsedReceiptParents = structuredClone(trace);
  collapsedReceiptParents.transportLedger[10].receiptProducerTransportIndices = [10];
  const negative = checkWithTLC(collapsedReceiptParents, 'PolicyOneReconcileLedgerCollapsedParents');
  t.diagnostic(summary(negative.output));
  assert.equal(negative.error, undefined, negative.error?.message);
  assert.notEqual(negative.status, 0);
  assert.match(negative.output, /ObservedTransportLedgerInvariant/);
});

test('production mutation controls preserve the input and reach each intended TLC invariant', t => {
  const trace = makeSyntheticPolicyOneJourneyTrace();
  const original = structuredClone(trace);
  const controls = buildPolicyOneMutationControls(trace);
  const expected = new Map([
    ['wrong-owner', 'ObservedResponseOwnership'],
    ['stale-cas', 'ObservedPolicyCAS'],
    ['out-of-order-repair', 'ObservedChronology'],
    ['stale-restoration', 'TraceProgress'],
    ['request-body-ledger-drift', 'ObservedTransportLedgerInvariant'],
    ['wrong-row-value-output', 'TraceProgress'],
    ['non-array-row-value-output', 'TraceProgress'],
  ]);
  const names = controls.map(control => control.name);
  assert.equal(new Set(names).size, names.length, 'mutation control names must be unique');
  assert.deepEqual([...names].sort(), [...expected.keys()].sort(), 'the full required mutation control set must be present');
  assert.deepEqual(trace, original, 'building mutation controls must not mutate the captured trace');
  const control = name => controls.find(entry => entry.name === name).trace;
  const originalAction = label => original.actions.find(entry => entry.label === label);
  const mutatedAction = (name, label) => control(name).actions.find(entry => entry.label === label);
  assert.equal(mutatedAction('wrong-owner', 'OneRejected').response.requestObjectId,
    originalAction('GroupApplied').response.requestObjectId,
    'wrong-owner mutation must use another actual captured request owner');
  assert.equal(mutatedAction('stale-cas', 'AllSubmitted').request.body.expectedDraftVersion,
    originalAction('GroupPreviewSubmitted').request.body.expectedDraftVersion,
    'stale-CAS mutation must reuse an actual older captured draft version');
  assert.equal(mutatedAction('stale-cas', 'AllSubmitted').request.body.expectedDraftDigest,
    originalAction('GroupPreviewSubmitted').request.body.expectedDraftDigest,
    'stale-CAS mutation must reuse an actual older captured draft digest');
  assert.equal(mutatedAction('stale-restoration', 'RemovalApplied').savedAfter.draftVersion,
    originalAction('AllApplied').observation.saved.draftVersion,
    'restoration mutation must use the actual captured ALL version');
  assert.equal(mutatedAction('stale-restoration', 'RemovalApplied').savedAfter.draftDigest,
    originalAction('AllApplied').observation.saved.draftDigest,
    'restoration mutation must use the actual captured ALL digest');
  assert.equal(mutatedAction('request-body-ledger-drift', 'GroupPreviewSubmitted').request.body.observationProbe,
    true, 'body-drift control must alter only the actual modeled request copy');
  assert.equal(control('request-body-ledger-drift').transportLedger[0].requestBody.observationProbe,
    undefined, 'body-drift control must leave the independent raw transport row unchanged');
  const originalGroup = originalAction('RemovalApplied').savedBefore.group;
  const originalRowValue = originalGroup.rowValues.find(value => value.policy === 'ALL');
  const alternateOutput = originalGroup.outputs.find(output => output.id !== originalRowValue.outputColumnId);
  const wrongOutputGroup = mutatedAction('wrong-row-value-output', 'RemovalApplied').savedBefore.group;
  assert.equal(wrongOutputGroup.rowValues.find(value => value.policy === 'ALL').outputColumnId, alternateOutput.id,
    'output-ID mutation must reuse another actual Group output ID');
  const scalarOutputGroup = mutatedAction('non-array-row-value-output', 'RemovalApplied').savedBefore.group;
  assert.equal(scalarOutputGroup.outputs.find(output => output.id === originalRowValue.outputColumnId).type,
    alternateOutput.type, 'output-type mutation must reuse an actual scalar Group output type');

  for (const control of controls) {
    assert.equal(control.expectedFailure.stage, 'TLC');
    assert.equal(control.expectedFailure.invariant, expected.get(control.name));
    assert.notDeepEqual(control.trace, original, `${control.name} must change its own trace copy`);
    assert.equal(validatePolicyOneJourneyTrace(control.trace), control.trace,
      `${control.name} must retain a structurally valid canonical trace so its domain failure reaches TLC`);
    const moduleName = `PolicyOneMutation${control.name.split('-')
      .map(part => part[0].toUpperCase() + part.slice(1)).join('')}`;
    const result = checkWithTLC(control.trace, moduleName);
    t.diagnostic(`${control.name}: ${summary(result.output)}`);
    assert.equal(result.error, undefined, result.error?.message);
    assert.notEqual(result.status, 0, `${control.name} must be rejected by TLC`);
    assert.match(result.output, new RegExp(control.expectedFailure.invariant),
      `${control.name} must fail its intended TLC invariant`);
  }
  assert.deepEqual(trace, original, 'TLC replay checks must leave the captured trace unchanged');
});
