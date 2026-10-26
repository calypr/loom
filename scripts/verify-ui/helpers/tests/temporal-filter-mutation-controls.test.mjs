import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { runTlc as runRegisteredTlc } from '../../../verify-builder-traces.mjs';
import { registry } from '../../registry.mjs';
import { builderFormalAdapterForBinding } from '../builder-formal-adapters.mjs';
import { adaptTemporalFilterJourney } from '../temporal-filter-trace-adapter.mjs';
import {
  makeTemporalFilterJourneyCaptureFixture,
  readNativeTemporalDateCapture,
} from './fixtures/temporal-filter-journey-schema-fixture.mjs';
import {
  createTemporalFilterMutationControls,
  TEMPORAL_FILTER_MUTATION_CONTROL_NAMES,
  validateTemporalFilterJourneyTrace,
} from '../temporal-filter-tla-adapter.mjs';

const tlcJar = process.env.TLA2TOOLS_JAR;
const dateCase = registry.find(entry => entry.id === 'basic-temporal-filter').cases.date;
const temporalAdapter = builderFormalAdapterForBinding(dateCase.journeyTrace);
let nextTlcIndex = 1_500;
const runTlc = (trace, moduleSuffix) => {
  assert(tlcJar, 'Set TLA2TOOLS_JAR to the verified TLC 1.7.4 jar for this focused check.');
  const compiled = temporalAdapter.compileTrace(trace, { moduleName: `TemporalFilterControl${moduleSuffix}` });
  const result = runRegisteredTlc(tlcJar, trace, nextTlcIndex++, false, null, compiled);
  const output = readFileSync(result.stdout.path, 'utf8') + readFileSync(result.stderr.path, 'utf8');
  return { ...result, status: result.status === 'pass' ? 0 : 1, output };
};


test('Temporal mutation factory returns fixed compiler-valid controls rejected by TLC', { timeout: 60_000 }, () => {
  assert.deepEqual(TEMPORAL_FILTER_MUTATION_CONTROL_NAMES, [
    'proposal-owner-drift',
    'proposal-cas-drift',
    'semantic-action-order-drift',
    'source-auto-id-cas-drift',
    'source-auto-id-command-echo-drift',
    'explorer-created-stale-read-mount',
  ]);
  const actualDateTrace = adaptTemporalFilterJourney(readNativeTemporalDateCapture('run6').capture);
  for (const [caseName, trace] of [
    ['date', actualDateTrace],
    ['date-time', adaptTemporalFilterJourney(makeTemporalFilterJourneyCaptureFixture('date-time'))],
  ]) {
    const controls = createTemporalFilterMutationControls(trace);
    assert.deepEqual(controls.map(control => control.name), TEMPORAL_FILTER_MUTATION_CONTROL_NAMES);
    for (const control of controls) {
      assert.equal(control.expectedFailure.stage, 'TLC');
      validateTemporalFilterJourneyTrace(control.trace);
      const result = runTlc(control.trace, `${caseName.replace('-', '')}${control.name.replaceAll('-', '')}`);
      assert.notEqual(result.status, 0, `${caseName} ${control.name} should fail TLC.`);
      const expectedOutput = control.expectedFailure.invariant === 'Deadlock'
        ? /Error: Deadlock reached\./u
        : new RegExp(`${control.expectedFailure.invariant} is equal to FALSE`, 'u');
      assert.match(result.output, expectedOutput,
        `${caseName} ${control.name} failed at an unexpected stage:\n${result.output}`);
    }
  }
});
