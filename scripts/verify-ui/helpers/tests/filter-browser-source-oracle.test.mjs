import assert from 'node:assert/strict';
import test from 'node:test';
import {
  assertFilterBrowserDefaultMode,
  buildFilterBrowserOracleQuery,
  FILTER_PERFORMANCE_CHECK,
  recordFilterLifecycleChecks,
  recordFilterPerformanceCheck,
} from '../../workflows/verify-cda-filter-browser.mjs';
import { scenarioCaseFor } from '../../registry.mjs';
import { summarizeRenderCheckpoints } from '../../../run-native-verification-bracket.mjs';

test('raw CDA filter oracle pins escaped project and nondefault generation literals', () => {
  const project = 'archive\" OR s.project != \"archive';
  const generation = 'snapshot-2031';
  const query = buildFilterBrowserOracleQuery({ project, generation });

  assert.equal(query,
    `FOR s IN Specimen FILTER s.project == ${JSON.stringify(project)} AND s.dataset_generation == ${JSON.stringify(generation)} LIMIT 1 RETURN {id:s.id,generation:s.dataset_generation,value:s.id}`);
  assert.doesNotMatch(query, /cda-fhir-v1/);
});

test('typed CDA filter oracle constrains the field value to the selected resource type', () => {
  const commonScope = 'FILTER s.project == \"loom_dev_cda_fhir\" AND s.dataset_generation == \"generation-2\"';
  assert.equal(buildFilterBrowserOracleQuery({ project: 'loom_dev_cda_fhir', generation: 'generation-2', numeric: true }),
    `FOR s IN Observation ${commonScope} FILTER IS_NUMBER(s.payload.valueQuantity.value) LIMIT 1 RETURN {id:s.id,generation:s.dataset_generation,value:s.payload.valueQuantity.value}`);
  assert.equal(buildFilterBrowserOracleQuery({ project: 'loom_dev_cda_fhir', generation: 'generation-2', booleanCase: true }),
    `FOR s IN Substance ${commonScope} FILTER IS_BOOL(s.payload.instance) LIMIT 1 RETURN {id:s.id,generation:s.dataset_generation,value:s.payload.instance}`);
});

test('raw CDA filter oracle rejects an unpinned target or ambiguous value mode', () => {
  assert.throws(() => buildFilterBrowserOracleQuery({ project: 'loom_dev_cda_fhir', generation: null }), /pinned source generation/);
  assert.throws(() => buildFilterBrowserOracleQuery({ project: 'loom_dev_cda_fhir', generation: 'generation-2', numeric: true, booleanCase: true }), /two typed resource modes/);
});

test('registered filter lifecycle rejects saved-operator and typed-resource variants', () => {
  assert.doesNotThrow(() => assertFilterBrowserDefaultMode({}));
  assert.throws(() => assertFilterBrowserDefaultMode({ LOOM_SAVED_FILTER_OPERATOR: 'NOT_EQUALS' }), /default filter operator/);
  assert.throws(() => assertFilterBrowserDefaultMode({ LOOM_SAVED_FILTER_OPERATOR: 'IN' }), /default filter operator/);
  assert.throws(() => assertFilterBrowserDefaultMode({ LOOM_SAVED_FILTER_OPERATOR: 'CONTAINS_TEXT' }), /default filter operator/);
  assert.throws(() => assertFilterBrowserDefaultMode({ LOOM_SAVED_FILTER_OPERATOR: 'GT' }), /default filter operator/);
  assert.throws(() => assertFilterBrowserDefaultMode({ LOOM_FILTER_VALUE_TYPE: 'BOOLEAN' }), /default Specimen ID type/);
  assert.throws(() => assertFilterBrowserDefaultMode({ LOOM_FILTER_VALUE_TYPE: 'INTEGER_GROUP' }), /default Specimen ID type/);
  assert.throws(() => assertFilterBrowserDefaultMode({ LOOM_GROUP_FILTER_UPSTREAM_EDIT: '1' }), /must not enable upstream Group edit/);
  assert.doesNotThrow(() => assertFilterBrowserDefaultMode({ LOOM_GROUP_FILTER_UPSTREAM_EDIT: '0' }));
});

test('registered filter browser case selects its exact title and owned CDA identity', () => {
  const contract = scenarioCaseFor('cda-filter-browser', 'filter-lifecycle');
  assert.equal(contract.playwrightTest, 'scripts/verify-ui/specs/standalone-cda-other.spec.mjs');
  assert.equal(contract.playwrightGrep, 'create, edit, cancel, apply, and remove a typed CDA filter$');
  assert.deepEqual(contract.expectedIdentity, { project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' });
  assert.equal(contract.requiredChecks.length, 9);
});

test('Filter performance evidence normalizes every retained render checkpoint and records the performance dimension', () => {
  const retainedCheckpoints = [
    ['reload-to-persisted-state', 1836],
    ['open-filter-editor', 128],
    ['missing-ID-preview', 751],
    ['cancel-to-source-rows', 710],
    ['confirmed-missing-ID-preview', 662],
    ['apply-to-persisted-state', 946],
    ['reload-to-persisted-state', 1817],
    ['edit-filter-to-controls', 765],
    ['edit-equality-preview', 717],
    ['apply-to-persisted-state', 904],
    ['reload-to-persisted-state', 1801],
    ['remove-filter-preview', 703],
    ['apply-to-persisted-state', 939],
    ['filter-removal-to-restored-state', 1779],
  ].map(([name, durationMs]) => ({ name, durationMs }));
  const retainedNativeActions = [72, 62, 35, 197, 91, 35, 42, 91, 105, 119, 103, 34, 77, 96, 103, 114, 94, 110, 92]
    .map(elapsedMs => ({ status: 'passed', elapsedMs }));
  const contract = scenarioCaseFor('cda-filter-browser', 'filter-lifecycle');
  assert.equal(contract.requiredChecks.at(-1), FILTER_PERFORMANCE_CHECK);

  const assertions = [];
  const cda = {
    check: (dimension, name, passed, evidence) => {
      assertions.push({ dimension, name, status: passed ? 'passed' : 'failed', evidence });
      return passed;
    },
  };
  recordFilterLifecycleChecks(cda, {
    choice: { status: 'passed' },
    proposal: { status: 'passed', durationMs: 751 },
    cancel: { status: 'passed', durationMs: 710 },
    apply: { status: 'passed', durationMs: 904 },
    savedRows: { status: 'passed' },
    reload: { status: 'passed' },
    edit: { status: 'passed', controlsDurationMs: 765 },
    restoration: { status: 'passed' },
  });
  assert.equal(recordFilterPerformanceCheck(cda, retainedCheckpoints, retainedNativeActions), true);

  const performance = assertions.at(-1);
  assert.equal(performance.dimension, 'performance');
  assert.equal(performance.name, FILTER_PERFORMANCE_CHECK);
  assert.equal(performance.evidence.workflowCheckpointCount, 14);
  assert.equal(performance.evidence.maximumWorkflowCheckpointMs, 1836);
  assert.equal(performance.evidence.nativeActionCount, 19);
  assert.equal(performance.evidence.maximumNativeActionMs, 197);
  assert.equal(performance.evidence.budgetMs, 5000);
  assert.equal(assertions.find(({ name }) => name.includes('proposals and rendered result')).evidence.durationMs, undefined);
  assert.equal(assertions.find(({ name }) => name.startsWith('Cancel preserves')).evidence.durationMs, undefined);
  assert.equal(assertions.find(({ name }) => name.startsWith('Apply persists')).evidence.durationMs, undefined);

  const normalized = summarizeRenderCheckpoints({ assertions }, {
    performanceCheckNames: [FILTER_PERFORMANCE_CHECK],
    requiredCheckNames: contract.requiredChecks,
  });
  assert.equal(normalized.count, 14);
  assert.equal(normalized.maximumDurationMs, 1836);
  assert.deepEqual(normalized.checkpoints.map(({ name, durationMs }) => ({ name, durationMs })), retainedCheckpoints);
  assert(normalized.checkpoints.every(checkpoint => checkpoint.evidencePath === 'assertions[].evidence.workflowCheckpoints[].durationMs'));

  const rejected = [];
  recordFilterPerformanceCheck({ check: (_dimension, _name, passed) => { rejected.push(passed); return passed; } },
    [{ name: 'slow reload', durationMs: 5001 }], retainedNativeActions);
  assert.deepEqual(rejected, [false]);
});
