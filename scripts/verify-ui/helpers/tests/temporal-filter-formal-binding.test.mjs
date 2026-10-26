import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { registry } from '../../registry.mjs';
import { planFocusedCheckGroups } from '../focused-check-groups.mjs';
import { makeTemporalFilterJourneyCaptureFixture } from './fixtures/temporal-filter-journey-schema-fixture.mjs';
import {
  nativeBuilderJourneyReceiptFromReport,
  writeAndReplayBuilderJourneyTrace,
} from '../builder-journey-trace.mjs';
import {
  builderFormalAdapterForBinding,
  builderFormalAdapterForCaptureKind,
  builderFormalAdapterForContract,
  isNativeBuilderFormalCaptureKind,
  registeredBuilderFormalAdapterIds,
} from '../builder-formal-adapters.mjs';
import {
  adaptTemporalFilterJourney,
  TEMPORAL_FILTER_CAPTURE_KIND,
} from '../temporal-filter-trace-adapter.mjs';
import { TEMPORAL_FILTER_MUTATION_CONTROL_NAMES } from '../temporal-filter-tla-adapter.mjs';

const cases = [
  { caseName: 'date', valueKind: 'DATE', resourceType: 'Patient', fieldPath: 'birthDate' },
  { caseName: 'date-time', valueKind: 'DATE_TIME', resourceType: 'Observation', fieldPath: 'effectiveDateTime' },
].map(spec => ({
  ...spec,
  nativeCaseKey: `basic-temporal-filter/${spec.caseName}`,
  traceName: `basic-temporal-filter:${spec.caseName}`,
  contract: JSON.parse(readFileSync(new URL(
    `../../../../verification/builder-interactions/builder-temporal-filter-${spec.caseName}-contract.json`,
    import.meta.url,
  ), 'utf8')),
}));

const bindingFor = caseName => registry.find(entry => entry.id === 'basic-temporal-filter')
  ?.cases?.[caseName]?.journeyTrace;
const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../../../');

test('DATE and DATE_TIME focused binding groups plan their command test exactly once', () => {
  const scenario = registry.find(entry => entry.id === 'basic-temporal-filter');
  assert(scenario, 'Missing the registered Temporal scenario.');

  for (const spec of cases) {
    const registeredCase = scenario.cases[spec.caseName];
    assert(registeredCase, `Missing the registered Temporal ${spec.caseName} case.`);
    const group = registeredCase.focusedChecks.find(candidate => candidate.id === 'basic-temporal-filter-formal-binding');
    assert(group, `Missing the Temporal ${spec.caseName} formal binding check.`);
    const testFile = 'scripts/verify-ui/helpers/tests/temporal-filter-formal-binding.test.mjs';
    assert.deepEqual(group.command, ['node-test', testFile]);
    assert.equal(group.sourceFiles.includes(testFile), false,
      'The command test is already a fingerprinted input and must not also appear in sourceFiles.');

    const plans = planFocusedCheckGroups(registeredCase.focusedChecks, repositoryRoot);
    const plan = plans.find(candidate => candidate.id === group.id);
    assert(plan, `The Temporal ${spec.caseName} binding check was not planned.`);
    assert.deepEqual(plan.args, ['--test', testFile]);
    assert.equal(plan.args.filter(argument => argument === testFile).length, 1);
  }

  const dateGroup = scenario.cases.date.focusedChecks.find(candidate => candidate.id === 'basic-temporal-filter-formal-binding');
  assert.throws(() => planFocusedCheckGroups([{
    ...dateGroup,
    sourceFiles: [...dateGroup.sourceFiles, dateGroup.command[1]],
  }], repositoryRoot), /duplicate file/);
});

test('DATE and DATE_TIME bindings select stable case-specific Temporal adapters', () => {
  assert.equal(registeredBuilderFormalAdapterIds.filter(id => id === 'temporal-filter-journey-v1').length, 1);
  assert.equal(isNativeBuilderFormalCaptureKind(TEMPORAL_FILTER_CAPTURE_KIND), true);
  assert.equal(builderFormalAdapterForCaptureKind(TEMPORAL_FILTER_CAPTURE_KIND), null,
    'A capture kind shared by two profiles must not select an arbitrary adapter.');

  const resolved = cases.map(spec => {
    const binding = bindingFor(spec.caseName);
    assert(binding, `Missing registered Temporal ${spec.caseName} journeyTrace binding.`);
    const adapter = builderFormalAdapterForBinding(binding);
    assert.strictEqual(builderFormalAdapterForContract(spec.contract), adapter,
      'Binding and contract resolution must return the same static adapter object.');
    assert.equal(adapter.captureAdapter, 'temporal-filter-journey-v1');
    assert.equal(adapter.captureKind, TEMPORAL_FILTER_CAPTURE_KIND);
    assert.equal(adapter.nativeCaseKey, spec.nativeCaseKey);
    assert.equal(adapter.traceName, spec.traceName);
    assert.equal(adapter.model, 'TemporalFilterJourney');
    assert.equal(adapter.valueKind, spec.valueKind);
    assert.equal(adapter.resourceType, spec.resourceType);
    assert.equal(adapter.fieldPath, spec.fieldPath);
    assert.equal(binding.formalProofAssertion, spec.contract.nativeJourney.formalProofAssertion);
    assert.deepEqual(binding.requiredMutationControlNames, TEMPORAL_FILTER_MUTATION_CONTROL_NAMES);
    assert.deepEqual(binding.requiredActions,
      [...new Set(spec.contract.nativeJourney.actions.map(action => action.label))]);
    assert.deepEqual(binding.requiredActionCounts,
      spec.contract.phase1.requiredActionCountsByTrace[spec.traceName]);

    const capture = makeTemporalFilterJourneyCaptureFixture(spec.caseName);
    const trace = adaptTemporalFilterJourney(capture);
    assert.equal(adapter.validateTrace(trace).caseName, spec.caseName);
    const compiled = adapter.compileTrace(trace, { moduleName: `TemporalBinding${spec.caseName.replace('-', '')}` });
    assert.equal(compiled.caseName, spec.caseName);
    assert.match(compiled.moduleText, /EXTENDS TemporalFilterJourney/u);
    assert.match(compiled.configText, /INVARIANT InvSemanticActionOrder/u);
    assert.deepEqual(adapter.buildMutationControls(trace).map(control => control.name),
      spec.contract.nativeJourney.requiredMutationControlNames);
    const coverage = adapter.requestCoverage(trace);
    assert.equal(coverage.observed, trace.nativeRequests.length);
    assert.equal(coverage.modeled, coverage.observed);
    assert.equal(coverage.unmapped, 0);
    assert.equal(coverage.duplicates, 0);
    assert.equal(coverage.modeledActions + coverage.reconcileReads + coverage.supportedReadOnly,
      coverage.observed);
    return adapter;
  });

  assert.notStrictEqual(resolved[0], resolved[1],
    'The DATE and DATE_TIME case keys must resolve to distinct static identities.');
});

test('Temporal adapter lookup rejects swapped case keys, traces, and contract profiles', () => {
  const date = cases[0];
  const dateTime = cases[1];
  const dateBinding = bindingFor('date');

  assert.throws(() => builderFormalAdapterForBinding({
    ...dateBinding,
    nativeCaseKey: dateTime.nativeCaseKey,
  }), /requires trace basic-temporal-filter:date-time/u);
  assert.throws(() => builderFormalAdapterForBinding({
    ...dateBinding,
    nativeCaseKey: 'basic-temporal-filter/unknown',
  }), /does not register nativeCaseKey/u);
  assert.throws(() => builderFormalAdapterForContract({
    ...date.contract,
    nativeJourney: { ...date.contract.nativeJourney, nativeCaseKey: dateTime.nativeCaseKey },
  }), /does not match static adapter/u);
  assert.throws(() => builderFormalAdapterForContract({
    ...date.contract,
    nativeJourney: { ...date.contract.nativeJourney, valueKind: 'DATE_TIME' },
  }), /valueKind does not match its exact Temporal case binding/u);
  assert.throws(() => builderFormalAdapterForContract({
    ...date.contract,
    nativeJourney: { ...date.contract.nativeJourney, captureAdapter: 'unregistered-temporal-adapter' },
  }), /Unknown registered Builder formal adapter/u);
});

test('shared writer replays both synthetic Temporal contracts and all registered controls', { timeout: 120000 }, t => {
  const directory = mkdtempSync(join(tmpdir(), 'temporal-formal-binding-unit-'));
  const priorTracePath = process.env.LOOM_JOURNEY_TRACE;
  let passed = false;
  t.after(() => {
    if (priorTracePath === undefined) delete process.env.LOOM_JOURNEY_TRACE;
    else process.env.LOOM_JOURNEY_TRACE = priorTracePath;
    if (passed) rmSync(directory, { recursive: true, force: true });
    else console.error('Synthetic Temporal common-writer diagnostics retained at ' + directory);
  });

  for (const spec of cases) {
    const capture = makeTemporalFilterJourneyCaptureFixture(spec.caseName, {
      pendingCapabilities: spec.caseName === 'date',
    });
    const trace = adaptTemporalFilterJourney(capture);
    const tracePath = join(directory, `${spec.caseName}.trace.json`);
    process.env.LOOM_JOURNEY_TRACE = tracePath;
    const report = {
      scenario: 'basic-temporal-filter',
      case: spec.caseName,
      evidenceDirectory: directory,
      target: {},
      dimensions: { correctness: { status: 'untested', evidence: [] } },
      assertions: [],
      requiredChecks: [spec.contract.nativeJourney.formalProofAssertion],
    };
    const receipt = writeAndReplayBuilderJourneyTrace({
      scenarioID: report.scenario,
      caseName: report.case,
      journeyTrace: trace,
      report,
      fixture: {
        unitOnly: true,
        project: capture.project,
        generation: capture.generation,
        outputId: capture.outputId,
        fixtureIDs: capture.ids,
      },
      repositoryRoot,
    });
    assert.equal(receipt.status, 'pass');
    assert.equal(receipt.replayStatus, 'pass');
    assert.equal(receipt.selfTestStatus, 'pass');
    assert.equal(receipt.nativeCaseKey, spec.nativeCaseKey);
    assert.equal(receipt.traceName, spec.traceName);
    assert.equal(receipt.mutationControls.length, TEMPORAL_FILTER_MUTATION_CONTROL_NAMES.length);
    assert(receipt.mutationControls.every(control => control.status === 'pass'));
    assert.equal(receipt.requestCoverageBasis, spec.contract.nativeJourney.coverageBasis);
    assert.equal(receipt.requestCoverage.unmapped, 0);
    assert.equal(receipt.requestCoverage.duplicates, 0);
    const proofAssertions = report.assertions.filter(assertion =>
      assertion.name === spec.contract.nativeJourney.formalProofAssertion);
    assert.equal(proofAssertions.length, 1);
    assert.equal(proofAssertions[0].dimension, 'correctness');
    assert.equal(proofAssertions[0].status, 'passed');
    assert.strictEqual(proofAssertions[0].evidence, receipt);
    assert.strictEqual(report.target.builderJourneyTrace, receipt,
      'The target receipt and registered formal proof assertion must share one completed receipt object.');
    assert.equal(report.dimensions.correctness.evidence.filter(entry =>
      entry.name === spec.contract.nativeJourney.formalProofAssertion).length, 1);
    assert.deepEqual(nativeBuilderJourneyReceiptFromReport(report, {
      binding: bindingFor(spec.caseName),
      scenarioID: report.scenario,
      caseName: report.case,
      requiredChecks: report.requiredChecks,
    }), receipt, 'The registered assertion must retain the complete authoritative formal receipt.');
  }

  passed = true;
});
