import assert from 'node:assert/strict';
import test from 'node:test';
import { assertFilterBrowserDefaultMode, buildFilterBrowserOracleQuery } from '../../workflows/verify-cda-filter-browser.mjs';
import { scenarioCaseFor } from '../../registry.mjs';

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
  assert.equal(contract.requiredChecks.length, 8);
});
