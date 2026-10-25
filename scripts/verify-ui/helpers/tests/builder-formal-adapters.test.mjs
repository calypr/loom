import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { test } from 'node:test';
import { makeSyntheticPolicyOneJourneyTrace } from './fixtures/policy-one-journey.mjs';
import { writeAndReplayBuilderJourneyTrace } from '../builder-journey-trace.mjs';
import { nativeJourneyActionInventoryProblem } from '../../../verify-builder-traces.mjs';
import {
  builderFormalAdapterForBinding,
  builderFormalAdapterForContract,
} from '../builder-formal-adapters.mjs';

const policyContract = JSON.parse(readFileSync(
  new URL('../../../../verification/builder-interactions/builder-policy-one-contract.json', import.meta.url),
  'utf8',
));

const policyBinding = {
  kind: 'native-lifecycle',
  captureKind: 'builder-policy-one-native-lifecycle',
  captureAdapter: 'policy-one-disagreement-v1',
  name: 'builder-authoring:patient-one-disagreement',
  model: 'PolicyOneDisagreement',
};

test('registered Policy adapter binds the exact trace, model, case, and controls', () => {
  const adapter = builderFormalAdapterForBinding(policyBinding);
  assert.equal(adapter.captureAdapter, policyContract.nativeJourney.captureAdapter);
  assert.equal(adapter.traceName, policyContract.nativeJourney.traceName);
  assert.equal(adapter.model, policyContract.nativeJourney.model);
  assert.equal(policyContract.nativeJourney.nativeCaseKey, 'builder-authoring/patient-one-disagreement');
  const contractAdapter = builderFormalAdapterForContract(policyContract);
  assert.equal(contractAdapter, adapter);
});

test('unregistered adapter IDs and mismatched capture identities fail closed', () => {
  assert.throws(() => builderFormalAdapterForBinding({ ...policyBinding, captureAdapter: 'arbitrary-module' }),
    /Unknown registered Builder formal adapter/);
  assert.throws(() => builderFormalAdapterForBinding({ ...policyBinding, captureKind: 'builder-cda-native-lifecycle' }),
    /requires captureKind/);
  assert.throws(() => builderFormalAdapterForBinding({ ...policyBinding, name: 'other-trace' }),
    /requires trace/);
  assert.throws(() => builderFormalAdapterForContract({
    ...policyContract,
    nativeJourney: { ...policyContract.nativeJourney, captureAdapter: 'arbitrary-module' },
  }), /Unknown registered Builder formal adapter/);
});

test('Policy adapter validates, compiles, and binds transport accounting to the retained trace', () => {
  const adapter = builderFormalAdapterForBinding(policyBinding);
  const trace = makeSyntheticPolicyOneJourneyTrace();
  assert.equal(adapter.validateTrace(trace), trace);
  const compiled = adapter.compileTrace(trace, { moduleName: 'PolicyOneAdapterUnit' });
  assert.equal(compiled.modelName, 'PolicyOneDisagreement');
  assert.equal(compiled.moduleName, 'PolicyOneAdapterUnit');
  assert.match(compiled.baseModelText, /SourcePatientIds == \{/);
  assert.match(compiled.modelText, /EXTENDS PolicyOneDisagreement/);
  assert.match(compiled.configText, /INVARIANT/);
  const coverage = adapter.requestCoverage(trace);
  assert.equal(coverage.observed, trace.transportLedger.length);
  assert.equal(coverage.modeled, coverage.observed);
  assert.equal(coverage.unmapped, 0);
  assert.equal(coverage.duplicates, 0);
  assert.equal(coverage.modeledActions + coverage.reconcileReads + coverage.supportedReadOnly +
    coverage.setupMutations, coverage.observed);
  const controls = adapter.buildMutationControls(trace);
  assert.deepEqual(controls.map(control => control.name), policyContract.nativeJourney.requiredMutationControlNames);
  assert(controls.every(control => control.expectedFailure.stage === 'TLC' &&
    typeof control.expectedFailure.invariant === 'string' && control.expectedFailure.invariant.length > 0));
});

test('Policy coverage counts setup mutations separately and leaves malformed setup rows unmapped', () => {
  const adapter = builderFormalAdapterForBinding(policyBinding);
  const trace = makeSyntheticPolicyOneJourneyTrace();
  const baseline = adapter.requestCoverage(trace);
  const setupRow = {
    ...structuredClone(trace.transportLedger[0]),
    transportIndex: Math.max(...trace.transportLedger.map(row => row.transportIndex)) + 1,
    capturePhase: 'SETUP',
    classification: 'setup',
    setupMutation: true,
  };
  const withSetupMutation = {
    ...trace,
    transportLedger: [...trace.transportLedger, setupRow],
  };
  const setupCoverage = adapter.requestCoverage(withSetupMutation);
  assert.equal(setupCoverage.observed, baseline.observed + 1);
  assert.equal(setupCoverage.modeledActions, baseline.modeledActions);
  assert.equal(setupCoverage.setupMutations, 1);
  assert.equal(setupCoverage.modeled, setupCoverage.observed);
  assert.equal(setupCoverage.unmapped, 0);
  assert.equal(setupCoverage.modeledActions + setupCoverage.reconcileReads +
    setupCoverage.supportedReadOnly + setupCoverage.setupMutations, setupCoverage.observed);

  const malformed = structuredClone(withSetupMutation);
  malformed.transportLedger.at(-1).setupMutation = false;
  const malformedCoverage = adapter.requestCoverage(malformed);
  assert.equal(malformedCoverage.setupMutations, 0);
  assert.equal(malformedCoverage.unmapped, 1);
  assert.equal(malformedCoverage.modeled, malformedCoverage.observed - 1);

  const setupAsAction = structuredClone(trace);
  setupAsAction.transportLedger.push({
    ...setupRow,
    transportIndex: Math.max(...trace.transportLedger.map(row => row.transportIndex)) + 1,
    classification: 'modeled',
    setupMutation: false,
  });
  const setupAsActionCoverage = adapter.requestCoverage(setupAsAction);
  assert.equal(setupAsActionCoverage.modeledActions, baseline.modeledActions);
  assert.equal(setupAsActionCoverage.setupMutations, 0);
  assert.equal(setupAsActionCoverage.unmapped, 1,
    'A SETUP row cannot be counted as a formal modeled action.');
});

test('native journey shape preserves exact action/event pairs while leaving chronology to TLC', () => {
  const adapter = builderFormalAdapterForBinding(policyBinding);
  const trace = makeSyntheticPolicyOneJourneyTrace();
  const outOfOrder = structuredClone(trace);
  const submitted = outOfOrder.actions.findIndex(action => action.label === 'AllSubmitted');
  const ready = outOfOrder.actions.findIndex(action => action.label === 'AllReady');
  [outOfOrder.actions[submitted], outOfOrder.actions[ready]] =
    [outOfOrder.actions[ready], outOfOrder.actions[submitted]];
  assert.equal(adapter.validateTrace(outOfOrder), outOfOrder);
  assert.equal(nativeJourneyActionInventoryProblem(outOfOrder.actions, policyContract.nativeJourney.actions), null);

  const wrongEvent = structuredClone(trace.actions);
  wrongEvent[0].event = 'cancel';
  assert.match(nativeJourneyActionInventoryProblem(wrongEvent, policyContract.nativeJourney.actions),
    /label\/event pair exactly/);
  const duplicatePair = structuredClone(trace.actions);
  duplicatePair[0] = structuredClone(duplicatePair[1]);
  assert.match(nativeJourneyActionInventoryProblem(duplicatePair, policyContract.nativeJourney.actions),
    /label\/event pair exactly/);
});

test('shared journey writer replays a complete synthetic Policy trace and all registered controls', { timeout: 120000 }, t => {
  const directory = mkdtempSync(join(tmpdir(), 'policy-one-shared-writer-unit-'));
  const tracePath = join(directory, 'synthetic-policy-one.trace.json');
  const priorTracePath = process.env.LOOM_JOURNEY_TRACE;
  let passed = false;
  t.after(() => {
    if (priorTracePath === undefined) delete process.env.LOOM_JOURNEY_TRACE;
    else process.env.LOOM_JOURNEY_TRACE = priorTracePath;
    if (passed) rmSync(directory, { recursive: true, force: true });
    else console.error('Synthetic Policy common-writer diagnostics retained at ' + directory);
  });
  process.env.LOOM_JOURNEY_TRACE = tracePath;
  const journeyTrace = makeSyntheticPolicyOneJourneyTrace();
  const report = {
    scenario: 'builder-authoring',
    case: 'patient-one-disagreement',
    evidenceDirectory: directory,
    target: {},
  };
  const receipt = writeAndReplayBuilderJourneyTrace({
    scenarioID: report.scenario,
    caseName: report.case,
    journeyTrace,
    report,
    fixture: {
      unitOnly: true,
      project: 'synthetic-policy-one-unit',
      generation: 'unit',
      explorer: 'synthetic-explorer',
      outputId: 'synthetic-output',
      patientIDs: ['dev-patient-001', 'dev-patient-002'],
    },
    repositoryRoot: resolve(new URL('../../../../', import.meta.url).pathname),
  });
  assert.equal(receipt.status, 'pass');
  assert.equal(receipt.replayStatus, 'pass');
  assert.equal(receipt.selfTestStatus, 'pass');
  assert.equal(receipt.nativeCaseKey, 'builder-authoring/patient-one-disagreement');
  assert.equal(receipt.traceName, 'builder-authoring:patient-one-disagreement');
  assert.equal(receipt.mutationControls.length, policyContract.nativeJourney.requiredMutationControlNames.length);
  assert(receipt.mutationControls.every(control => control.status === 'pass'));
  assert.equal(receipt.requestCoverage.unmapped, 0);
  assert.equal(receipt.requestCoverage.duplicates, 0);
  assert.equal(receipt.requestCoverageBasis, policyContract.nativeJourney.coverageBasis);
  passed = true;
});
