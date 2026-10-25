import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { runTlc } from '../../../verify-builder-traces.mjs';
import { registry } from '../../registry.mjs';
import {
  builderFormalAdapterForBinding,
  builderFormalAdapterForContract,
} from '../builder-formal-adapters.mjs';
import { adaptTemporalFilterJourney } from '../temporal-filter-trace-adapter.mjs';
import {
  NATIVE_TEMPORAL_DATE_CAPTURE_NAMES,
  readNativeTemporalDateCapture,
} from './fixtures/temporal-filter-journey-schema-fixture.mjs';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../../../');
const tlcJar = process.env.TLA2TOOLS_JAR;

test('retained native DATE captures pass static adapter validation and production TLC replay', () => {
  assert(tlcJar, 'Set TLA2TOOLS_JAR to the verified TLC 1.7.4 jar.');
  const literalRows = [
    { id: 'dev-patient-001', value: '1980-01-02' },
    { id: 'dev-patient-002', value: '1985-03-04' },
  ];
  const registeredCase = registry.find(entry => entry.id === 'basic-temporal-filter').cases.date;
  const binding = registeredCase.journeyTrace;
  const contract = JSON.parse(readFileSync(resolve(repositoryRoot,
    'verification/builder-interactions/builder-temporal-filter-date-contract.json'), 'utf8'));
  const adapter = builderFormalAdapterForBinding(binding);
  assert.strictEqual(builderFormalAdapterForContract(contract), adapter,
    'The DATE binding and contract must resolve to the same static adapter.');
  const traces = new Map();

  NATIVE_TEMPORAL_DATE_CAPTURE_NAMES.forEach((name, index) => {
    const { source, capture } = readNativeTemporalDateCapture(name);
    assert.equal(capture.requestEvents.length, 79);
    assert.equal(capture.steps.length, 16);
    assert.deepEqual(capture.rows, literalRows,
      `${name} must preserve each Patient ID with its literal birthDate value.`);
    const sourceStep = capture.steps.find(step => step.label === 'SourceTableCreated');
    const sourceDOM = capture.domObservations[sourceStep.domObservationIndex];
    assert.deepEqual(sourceDOM.rowIds, source.sourceDOMRowIds,
      `${name} must preserve actual DOM row order.`);
    assert.deepEqual([...sourceDOM.rowIds].sort(), capture.ids,
      `${name} must retain the exact source identity multiset.`);

    const trace = adaptTemporalFilterJourney(capture);
    const validation = adapter.validateTrace(trace);
    assert.equal(validation.caseName, 'date');
    const coverage = adapter.requestCoverage(trace);
    assert.deepEqual({ observed: coverage.observed, modeled: coverage.modeled,
      unmapped: coverage.unmapped, duplicates: coverage.duplicates },
    { observed: 79, modeled: 79, unmapped: 0, duplicates: 0 });
    const compiled = adapter.compileTrace(trace, { moduleName: `NativeTemporal${name}` });
    assert.equal(compiled.eventCount, 16);
    assert.equal(compiled.observedRequestCount, 79);
    const replay = runTlc(tlcJar, trace, 800 + index, false, null, compiled);
    assert.equal(replay.status, 'pass', `${name} native DATE replay failed: ${JSON.stringify(replay.failure ?? {})}`);
    assert.equal(replay.processExitCode, 0);
    assert.equal(replay.stateCount, 19);
    assert.equal(replay.distinctStateCount, 17);
    assert.equal(replay.depth, 17);
    console.log(`DATE ${name} ${source.sourceCommit}: 16 actions, 79 requests, ${replay.stateCount}/${replay.distinctStateCount}, depth ${replay.depth}; ${replay.runDirectory}`);
    traces.set(name, trace);
  });

  const run6Trace = traces.get('run6');
  const duplicateRowTrace = structuredClone(run6Trace);
  const cancelActionIndex = duplicateRowTrace.actions.findIndex(event => event.action === 'FilterCancelled');
  const cancelStep = duplicateRowTrace.temporalFilterLifecycle.steps[cancelActionIndex];
  const cancelDOM = duplicateRowTrace.temporalFilterLifecycle.domObservations[cancelStep.domObservationIndex];
  const duplicatedIds = [...cancelDOM.rowIds, cancelDOM.rowIds[0]];
  assert.deepEqual(new Set(duplicatedIds), new Set(cancelDOM.rowIds));
  assert.equal(duplicatedIds.length, cancelDOM.rowIds.length + 1);
  cancelDOM.rowIds = duplicatedIds;
  duplicateRowTrace.actions[cancelActionIndex].ui.rowIds = duplicatedIds;
  adapter.validateTrace(duplicateRowTrace);
  const duplicateModule = adapter.compileTrace(duplicateRowTrace, { moduleName: 'Run6DuplicateRowMultiset' });
  const duplicateReplay = runTlc(tlcJar, duplicateRowTrace, 900, false, null, duplicateModule);
  assert.equal(duplicateReplay.status, 'fail');
  assert.equal(duplicateReplay.failure?.invariant, 'Deadlock');
  console.log(`DATE run6 same-set duplicate row control: ${duplicateReplay.failure.invariant}; ${duplicateReplay.runDirectory}`);

  const invalidRows = [
    ['missing source row', capture => capture.rows.splice(0, 1)],
    ['extra source row', capture => capture.rows.push({ id: 'dev-patient-extra', value: '1990-06-07' })],
    ['duplicate source ID', capture => { capture.rows[1].id = capture.rows[0].id; }],
    ['wrong ID-value association', capture => {
      [capture.rows[0].value, capture.rows[1].value] = [capture.rows[1].value, capture.rows[0].value];
    }],
  ];
  for (const [name, mutate] of invalidRows) {
    const malformed = structuredClone(run6Trace.temporalFilterLifecycle);
    mutate(malformed);
    assert.throws(() => adaptTemporalFilterJourney(malformed), {
      name: 'AssertionError',
      message: /Trace row literals must remain the independent temporal oracle\./u,
    }, name);
  }
});
