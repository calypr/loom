import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, dirname, join, resolve } from 'node:path';
import { test } from 'node:test';
import { makeSyntheticPolicyOneJourneyTrace } from './fixtures/policy-one-journey.mjs';
import {
  finalizeNativeBuilderJourneyReceipt,
  nativeBuilderJourneyReceiptFromReport,
  writeAndReplayBuilderJourneyTrace,
} from '../builder-journey-trace.mjs';
import {
  nativeJourneyActionInventoryProblem,
  runTlc,
  tlcExecutionReceipt,
} from '../../../verify-builder-traces.mjs';
import { inspectNativeLifecycleJourney } from '../../../run-builder-journey.mjs';
import { scenarioCaseFor } from '../../registry.mjs';
import {
  builderFormalAdapterForBinding,
  builderFormalAdapterForContract,
} from '../builder-formal-adapters.mjs';
import { main as runBuilderFormalVerification } from '../../../verify-builder-formal.mjs';

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

test('formal verifier stops before Java, TLC, and model work when registry conformance fails', async () => {
  const calls = [];
  let gateCommand;
  const options = { strict: false, skipGo: true, model: 'builder-operations', listModels: false };
  await assert.rejects(runBuilderFormalVerification({
    options,
    runCommandFn: (label, command, args, cwd) => {
      calls.push('registry-conformance');
      gateCommand = { label, command, args, cwd };
      return { status: 1, error: null, output: 'deterministic registry conformance failure' };
    },
    verifyJavaFn: () => {
      calls.push('verify-java');
      return 'java';
    },
    acquireTlcJarFn: () => {
      calls.push('acquire-tlc');
      return '/unused/tla2tools.jar';
    },
    runTlcFn: () => {
      calls.push('run-model');
      return { passed: true };
    },
  }), /Registry-wide native Builder formal conformance failed before formal model checks:[\s\S]*deterministic registry conformance failure/);
  assert.deepEqual(gateCommand, {
    label: 'Registry-wide native Builder formal conformance',
    command: process.execPath,
    args: ['--test', 'scripts/verify-ui/helpers/tests/native-builder-formal-gate.test.mjs'],
    cwd: resolve(new URL('../../../../', import.meta.url).pathname),
  });
  assert.deepEqual(calls, ['registry-conformance'],
    'a failed registry conformance gate must not reach Java, jar acquisition, or any TLC model run');
});

test('outer native inspector accepts the Policy fixture producer and rejects missing event inventory', () => {
  const trace = makeSyntheticPolicyOneJourneyTrace();
  const registeredCase = scenarioCaseFor('builder-authoring', 'patient-one-disagreement');
  const inspected = inspectNativeLifecycleJourney(trace, registeredCase.journeyTrace);
  assert.equal(inspected.name, policyContract.nativeJourney.traceName);
  assert.equal(inspected.model, policyContract.nativeJourney.model);
  assert.equal(inspected.actionCount, 20);
  assert.deepEqual(trace.unmodeledEvents, []);

  const missingInventory = structuredClone(trace);
  delete missingInventory.unmodeledEvents;
  assert.throws(() => inspectNativeLifecycleJourney(missingInventory, registeredCase.journeyTrace),
    /unmodeled-event inventory/);
});

test('TLC retains exact stdout, stderr, and process metadata on success and failure', () => {
  const trace = {
    name: 'builder-authoring:output-retention-unit',
    model: 'PolicyOneDisagreement',
    actions: [{ label: 'Observed' }],
  };
  const compiledAdapter = {
    moduleName: 'TlcOutputReceiptUnit',
    modelName: trace.model,
    baseModelText: '---- MODULE PolicyOneDisagreement ----\n====\n',
    modelText: '---- MODULE TlcOutputReceiptUnit ----\n====\n',
    configText: 'SPECIFICATION Spec\n',
  };
  const successStdout = Buffer.from([
    'Model checking completed. No error has been found.',
    '5 states generated, 5 distinct states found, 0 states left on queue, 0.0s',
    'The depth of the complete state graph search is 1.',
    '',
  ].join('\n'));
  const successStderr = Buffer.from('TLC diagnostic channel\n');
  const failureStdout = Buffer.from([
    'Invariant PolicyOneGuard is violated.',
    '5 states generated, 3 distinct states found, 0 states left on queue, 0.0s',
    'The depth of the complete state graph search is 2.',
    '',
  ].join('\n'));
  const failureStderr = Buffer.from('TLC failed with an invariant violation\n');

  for (const [expectedStatus, stdout, stderr, exitCode] of [
    ['pass', successStdout, successStderr, 0],
    ['fail', failureStdout, failureStderr, 151],
  ]) {
    const run = runTlc('unused-tlc.jar', trace, 1, false, null, compiledAdapter, (_command, _args, options) => {
      assert.equal(basename(options.cwd), 'model');
      assert.equal(options.maxBuffer, 32 * 1024 * 1024);
      return { pid: 1234, status: exitCode, signal: null, stdout, stderr };
    });
    try {
      assert.equal(run.status, expectedStatus);
      assert.deepEqual(readFileSync(run.stdout.path), stdout);
      assert.deepEqual(readFileSync(run.stderr.path), stderr);
      assert.equal(run.stdout.bytes, stdout.length);
      assert.equal(run.stderr.bytes, stderr.length);
      assert.match(run.stdout.sha256, /^[0-9a-f]{64}$/);
      assert.match(run.stderr.sha256, /^[0-9a-f]{64}$/);

      const metadata = JSON.parse(readFileSync(run.executionMetadataPath, 'utf8'));
      assert.equal(metadata.command, 'java');
      assert.deepEqual(metadata.args.slice(-2), ['TlcOutputReceiptUnit.cfg', 'TlcOutputReceiptUnit.tla']);
      assert.equal(metadata.cwd, dirname(run.modulePath));
      assert.equal(metadata.processExitCode, exitCode);
      assert.equal(metadata.processSignal, null);
      assert.equal(metadata.pid, 1234);
      assert.equal(metadata.maxBufferBytes, 32 * 1024 * 1024);
      assert.equal(Number.isFinite(Date.parse(metadata.startedAt)), true);
      assert.equal(Number.isFinite(Date.parse(metadata.completedAt)), true);
      assert.equal(metadata.durationMs >= 0, true);
      assert.equal(metadata.stdout.sha256, run.stdout.sha256);
      assert.equal(metadata.stderr.sha256, run.stderr.sha256);

      const receipt = tlcExecutionReceipt(run);
      assert.equal(receipt.runDirectory, run.runDirectory);
      assert.equal(receipt.modulePath, run.modulePath);
      assert.equal(receipt.configPath, run.configPath);
      assert.equal(receipt.executionMetadataPath, run.executionMetadataPath);
      assert.equal(receipt.processExitCode, exitCode);
      assert.equal(receipt.stdout.path, run.stdout.path);
      assert.equal(receipt.stderr.path, run.stderr.path);
      if (expectedStatus === 'fail') {
        assert.equal(run.failure.invariant, 'PolicyOneGuard');
        assert.match(run.failure.outputTail, /PolicyOneGuard is violated/);
      }
    } finally {
      rmSync(run.runDirectory, { recursive: true, force: true });
    }
  }
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
  assert.equal(setupCoverage.setupMutations, baseline.setupMutations + 1);
  assert.equal(setupCoverage.modeled, setupCoverage.observed);
  assert.equal(setupCoverage.unmapped, 0);
  assert.equal(setupCoverage.modeledActions + setupCoverage.reconcileReads +
    setupCoverage.supportedReadOnly + setupCoverage.setupMutations, setupCoverage.observed);

  const malformed = structuredClone(withSetupMutation);
  malformed.transportLedger.at(-1).setupMutation = false;
  const malformedCoverage = adapter.requestCoverage(malformed);
  assert.equal(malformedCoverage.setupMutations, baseline.setupMutations);
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
  assert.equal(setupAsActionCoverage.setupMutations, baseline.setupMutations);
  assert.equal(setupAsActionCoverage.unmapped, 1,
    'A SETUP row cannot be counted as a formal modeled action.');
});

test('final receipt validation keeps Policy coverage fields fixed by the registered contract', () => {
  assert.throws(() => finalizeNativeBuilderJourneyReceipt({}, {}, {
    contract: policyContract,
    requestCoverageFields: ['modeledActions', 'reconcileReads', 'supportedReadOnly'],
  }), /must match the registered adapter contract/u);
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

test('shared writer rejects missing, partial, duplicate, and pre-populated proof inputs before replay', () => {
  const directory = mkdtempSync(join(tmpdir(), 'policy-one-proof-preflight-'));
  const registeredCase = scenarioCaseFor('builder-authoring', 'patient-one-disagreement');
  const assertionName = registeredCase.journeyTrace.formalProofAssertion;
  const journeyTrace = makeSyntheticPolicyOneJourneyTrace();
  const baseReport = {
    scenario: 'builder-authoring',
    case: 'patient-one-disagreement',
    evidenceDirectory: directory,
    target: { builderJourneyTrace: null },
    dimensions: { correctness: { status: 'untested', evidence: [] } },
    assertions: [],
    requiredChecks: [assertionName],
  };
  const rejectWithoutMutation = (label, report, expression) => {
    const before = structuredClone(report);
    assert.throws(() => writeAndReplayBuilderJourneyTrace({
      scenarioID: report.scenario,
      caseName: report.case,
      journeyTrace,
      report,
      fixture: { unitOnly: true },
      repositoryRoot: resolve(new URL('../../../../', import.meta.url).pathname),
    }), expression, label);
    assert.deepEqual(report, before, `${label} must be rejected without partially recording proof state`);
  };

  try {
    rejectWithoutMutation('missing required proof check', {
      ...structuredClone(baseReport), requiredChecks: [],
    }, /exactly one required check/);
    rejectWithoutMutation('partial registered proof assertion', {
      ...structuredClone(baseReport),
      assertions: [{ dimension: 'correctness', name: assertionName, status: 'passed', evidence: { status: 'pass' } }],
    }, /already contains the registered formal proof assertion/);
    rejectWithoutMutation('duplicate registered proof assertion', {
      ...structuredClone(baseReport),
      assertions: [1, 2].map(() => ({
        dimension: 'correctness', name: assertionName, status: 'passed', evidence: { status: 'pass' },
      })),
    }, /already contains duplicate registered formal proof assertions/);
    rejectWithoutMutation('partial target proof receipt', {
      ...structuredClone(baseReport),
      target: { builderJourneyTrace: { traceName: journeyTrace.name, status: 'pass' } },
    }, /must not pre-populate a partial formal proof receipt/);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test('shared journey writer replays Policy pending support reads, the full lifecycle, and every registered control', { timeout: 120000 }, t => {
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
  const journeyTrace = makeSyntheticPolicyOneJourneyTrace({ pendingSupportReads: true });
  const registeredCase = scenarioCaseFor('builder-authoring', 'patient-one-disagreement');
  const formalProofAssertion = registeredCase.journeyTrace.formalProofAssertion;
  const repositoryRoot = resolve(new URL('../../../../', import.meta.url).pathname);
  const pendingSupportRows = journeyTrace.transportLedger.filter(row => row.settlement === 'pending');
  assert.equal(journeyTrace.actions.length, 20);
  assert.equal(pendingSupportRows.length, 2);
  assert.deepEqual(pendingSupportRows.map(row => row.path), ['semantic-inventory', 'construction-capabilities']);
  assert(pendingSupportRows.every(row => row.classification === 'read-only' && row.status === null &&
    row.responseOwnerIndex === null && row.responseHeaders === null && row.response === null),
  'pending pure support reads are retained without response, owner, or success credit');
  const report = {
    scenario: 'builder-authoring',
    case: 'patient-one-disagreement',
    evidenceDirectory: directory,
    target: { builderJourneyTrace: null },
    dimensions: { correctness: { status: 'untested', evidence: [] } },
    assertions: [],
    requiredChecks: [formalProofAssertion],
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
    repositoryRoot,
  });
  assert.equal(receipt.status, 'pass');
  assert.equal(receipt.replayStatus, 'pass');
  assert.equal(receipt.selfTestStatus, 'pass');
  assert.equal(receipt.nativeCaseKey, 'builder-authoring/patient-one-disagreement');
  assert.equal(receipt.traceName, 'builder-authoring:patient-one-disagreement');
  assert.equal(receipt.mutationControls.length, policyContract.nativeJourney.requiredMutationControlNames.length);
  assert(receipt.mutationControls.every(control => control.status === 'pass'));
  assert.equal(receipt.requestCoverage.observed, journeyTrace.transportLedger.length);
  assert.equal(receipt.requestCoverage.setupMutations,
    journeyTrace.transportLedger.filter(row => row.classification === 'setup').length);
  assert.equal(receipt.requestCoverage.modeledActions,
    journeyTrace.transportLedger.filter(row => row.classification === 'modeled').length);
  assert.equal(receipt.requestCoverage.modeledActions + receipt.requestCoverage.reconcileReads +
    receipt.requestCoverage.supportedReadOnly + receipt.requestCoverage.setupMutations,
  receipt.requestCoverage.observed);
  assert.equal(receipt.requestCoverage.unmapped, 0);
  assert.equal(receipt.requestCoverage.duplicates, 0);
  assert.equal(receipt.requestCoverageBasis, policyContract.nativeJourney.coverageBasis);
  assert.equal(receipt.requestCoverage.supportedReadOnly,
    journeyTrace.transportLedger.filter(row => row.classification === 'read-only').length);
  const proofAssertions = report.assertions.filter(assertion => assertion.name === formalProofAssertion);
  assert.equal(proofAssertions.length, 1);
  assert.equal(proofAssertions[0].dimension, 'correctness');
  assert.equal(proofAssertions[0].status, 'passed');
  assert.strictEqual(proofAssertions[0].evidence, receipt);
  assert.strictEqual(report.target.builderJourneyTrace, receipt,
    'The target receipt and registered formal proof assertion must share one completed receipt object.');
  assert.deepEqual(nativeBuilderJourneyReceiptFromReport(report, {
    binding: registeredCase.journeyTrace,
    scenarioID: report.scenario,
    caseName: report.case,
    requiredChecks: report.requiredChecks,
  }), receipt);
  assert.equal(report.dimensions.correctness.evidence.filter(entry => entry.name === formalProofAssertion).length, 1);

  const replayReport = JSON.parse(readFileSync(receipt.replayReportPath, 'utf8'));
  const replayedTrace = replayReport.traces.find(entry => entry.name === journeyTrace.name);
  assert.equal(replayedTrace.status, 'pass');
  const tlc = replayedTrace.tlc;
  assert.equal(tlc.processExitCode, 0);
  assert.equal(tlc.runDirectory.length > 0, true);
  for (const artifact of [tlc.stdout, tlc.stderr]) {
    const bytes = readFileSync(artifact.path);
    assert.equal(artifact.bytes, bytes.length);
    assert.equal(artifact.sha256, createHash('sha256').update(bytes).digest('hex'));
  }
  const execution = JSON.parse(readFileSync(tlc.executionMetadataPath, 'utf8'));
  assert.equal(execution.processExitCode, 0);
  assert.equal(execution.stdout.sha256, tlc.stdout.sha256);
  assert.equal(execution.stderr.sha256, tlc.stderr.sha256);
  passed = true;
});
