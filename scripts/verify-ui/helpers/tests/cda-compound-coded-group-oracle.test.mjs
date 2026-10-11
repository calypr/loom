import assert from 'node:assert/strict';
import test from 'node:test';
import {
  CDA_COMPOUND_CODED_GROUP_CATEGORY,
  CDA_COMPOUND_CODED_GROUP_IDS,
  CDA_COMPOUND_CODED_GROUP_SCOPE,
  assertCdaCompoundCodedGroupRows,
  authoredCodedGroupValue,
  buildCdaCompoundCodedGroupExactReadQuery,
  prepareCdaCompoundCodedGroupOracle,
  validateCdaCompoundCodedGroupCandidate,
  validateCdaCompoundCodedGroupFrameSource,
  validateCdaCompoundCodedGroupInventoryItem,
  validateCdaCompoundCodedGroupSourceOption,
} from '../cda-compound-coded-group-oracle.mjs';

const { project, generation } = CDA_COMPOUND_CODED_GROUP_SCOPE;
const rows = [
  { id: CDA_COMPOUND_CODED_GROUP_IDS[0], project, generation, payload: { resourceType: 'Observation', code: { coding: [{ system: 'http://snomed.info/sct', code: '1222593009' }] }, valueCodeableConcept: { coding: [{ display: 'Stage I' }] } } },
  { id: CDA_COMPOUND_CODED_GROUP_IDS[1], project, generation, payload: { resourceType: 'Observation', code: { coding: [{ system: 'http://snomed.info/sct', code: '1222593009' }] }, valueCodeableConcept: { coding: [{ display: 'Stage I' }] } } },
  { id: CDA_COMPOUND_CODED_GROUP_IDS[2], project, generation, payload: { resourceType: 'Observation', code: { coding: [{ system: 'http://loinc.org', code: '81247-9' }] }, valueQuantity: null } },
];

test('the exact raw CDA witness yields literal Stage I and null GROUP rows', () => {
  const oracle = prepareCdaCompoundCodedGroupOracle(rows, { project, generation });
  assert.deepEqual(oracle.source, {
    resourceType: 'Observation', sourcePath: 'code', valuePath: 'valueCodeableConcept.text',
    valueFallbacks: ['valueCodeableConcept.coding[].display'], fallbackOnBlankString: true,
    keyPath: 'code.coding[].code',
  });
  assert.deepEqual(oracle.expectedRows, [['Stage I', 2], [null, 1]]);
  assert.deepEqual(Object.values(oracle.valuesByID).sort((left, right) => String(left).localeCompare(String(right))), [null, 'Stage I', 'Stage I']);
  assert.deepEqual(assertCdaCompoundCodedGroupRows([[null, 1], ['Stage I', 2]]), [['Stage I', 2], [null, 1]]);
});

test('authored CodeableConcept text wins unless blank, then coding display supplies the value', () => {
  assert.equal(authoredCodedGroupValue({ valueCodeableConcept: { text: 'Stage II', coding: [{ display: 'Stage I' }] } }), 'Stage II');
  assert.equal(authoredCodedGroupValue({ valueCodeableConcept: { text: ' ', coding: [{ display: 'Stage I' }] } }), 'Stage I');
  assert.equal(authoredCodedGroupValue({ valueCodeableConcept: { coding: [{ display: 'Stage I' }] } }), 'Stage I');
  assert.equal(authoredCodedGroupValue({}), null);
  assert.throws(() => authoredCodedGroupValue({ valueCodeableConcept: { coding: [{ display: 'Stage I' }, { display: 'Stage II' }] } }),
    /bounded to the retained single-display witness/);
});

test('the raw oracle rejects a fabricated missing value and a wrong selected category', () => {
  const populatedWithFakeValue = structuredClone(rows);
  populatedWithFakeValue[2].payload.valueString = 'missing';
  assert.throws(() => prepareCdaCompoundCodedGroupOracle(populatedWithFakeValue, { project, generation }), /must not acquire a fabricated value/);

  const wrongCategory = structuredClone(rows);
  wrongCategory[0].payload.code.coding[0].code = '81247-9';
  assert.throws(() => prepareCdaCompoundCodedGroupOracle(wrongCategory, { project, generation }), /must carry exactly one/);
});

test('the native source and semantic item contract retain exact source and category choices', () => {
  const sourceOption = {
    choiceId: 'opaque-frame-choice', resourceType: 'Observation', sourcePath: 'code',
    keyPath: 'code.coding[].code', valuePath: 'valueCodeableConcept.text',
    route: [], defaultForm: 'ALL', forms: [
      { form: 'VALUE', zeroPolicy: 'NULL', manyPolicy: 'INVALID_MULTIPLE_VALUES', decision: 'REQUIRES_DECISION' },
      { form: 'ALL', zeroPolicy: 'EMPTY_LIST', manyPolicy: 'ALL', decision: 'DEFAULT' },
    ], bindingId: 'binding', owningScope: 'Observation', logicalType: 'string',
  };
  assert.equal(validateCdaCompoundCodedGroupSourceOption(sourceOption, { outputId: 'output', snapshotToken: 'snapshot' }), 'opaque-frame-choice');
  const frameSource = { source: {
    resourceType: 'Observation', sourcePath: 'code', keyPath: 'code.coding[].code',
    valuePath: 'valueCodeableConcept.text', valueFallbacks: ['valueCodeableConcept.coding[].display'],
    fallbackOnBlankString: true,
  }, route: [], form: 'ALL', zeroPolicy: 'EMPTY_LIST', manyPolicy: 'ALL' };
  assert.equal(validateCdaCompoundCodedGroupFrameSource(frameSource, sourceOption), frameSource.source);
  assert.throws(() => validateCdaCompoundCodedGroupFrameSource({
    ...frameSource, source: { ...frameSource.source, fallbackOnBlankString: false },
  }, sourceOption), /true/);
  assert.throws(() => validateCdaCompoundCodedGroupFrameSource({ ...frameSource, form: 'VALUE' }, sourceOption), /selected source option/);
  assert.throws(() => validateCdaCompoundCodedGroupSourceOption({
    ...sourceOption, forms: sourceOption.forms.map(form => form.form === 'VALUE' ? { ...form, decision: 'DEFAULT' } : form),
  }, { outputId: 'output', snapshotToken: 'snapshot' }), /REQUIRES_DECISION/);
  const item = {
    bindingId: 'binding', resourceType: 'Observation', sourcePath: 'code',
    valueSelector: 'valueCodeableConcept.text', system: CDA_COMPOUND_CODED_GROUP_CATEGORY.system,
    code: CDA_COMPOUND_CODED_GROUP_CATEGORY.code, codingVersion: '', valueType: 'string',
    readiness: { status: 'READY' }, constructionChoice: { choiceId: 'opaque-category-choice', source: {
      kind: 'SEMANTIC', bindingId: 'binding', candidateId: 'candidate', nodeId: 'node',
      resourceType: 'Observation', sourcePath: 'code', sourceCanonical: undefined, sourceProfile: undefined,
      fieldPath: 'valueCodeableConcept.text', owningScope: 'Observation',
      keySelector: 'code.coding[].code', system: CDA_COMPOUND_CODED_GROUP_CATEGORY.system,
      version: '', code: CDA_COMPOUND_CODED_GROUP_CATEGORY.code,
      valueSelector: 'valueCodeableConcept.text', valueFallbacks: ['valueCodeableConcept.coding[].display'],
      fallbackOnBlankString: true, logicalType: 'string',
      ruleVersion: 'cda-rule-v1', schemaVersion: 1,
    }, route: [] },
  };
  assert.equal(validateCdaCompoundCodedGroupInventoryItem(item, sourceOption), 'opaque-category-choice');
});

test('the proposal contract persists an owned coded pivot and a null-preserving GROUP', () => {
  const candidate = { steps: [
    { id: 'coded', ownerStepId: 'group', inputs: [{ kind: 'SOURCE_PROJECTION' }], operation: { kind: 'CODED_PIVOT', codedPivot: {
      source: { family: { resourceType: 'Observation', bindingId: 'binding', sourcePath: 'code', keyPath: 'code.coding[].code', valuePath: 'valueCodeableConcept.text', valueFallbacks: ['valueCodeableConcept.coding[].display'], fallbackOnBlankString: true, owningScope: 'Observation', logicalType: 'string', ruleVersion: 'cda-rule-v1', schemaVersion: 1 }, candidateId: 'candidate', nodeId: 'node', fieldPath: 'valueCodeableConcept.text', route: [] },
      categories: [{ ...CDA_COMPOUND_CODED_GROUP_CATEGORY, outputColumnId: 'coded-value' }], duplicatePolicy: 'ERROR', missingCellPolicy: 'NULL',
    } } },
    { id: 'group', inputs: [{ kind: 'STEP_OUTPUT', stepId: 'coded' }], operation: { kind: 'GROUP', group: {
      missingKeyPolicy: 'GROUP', keys: [{ inputColumnId: 'coded-value' }], aggregates: [{ operation: 'COUNT_ROWS' }],
    } } },
  ] };
  const sourceOption = { bindingId: 'binding', resourceType: 'Observation', sourcePath: 'code', keyPath: 'code.coding[].code', valuePath: 'valueCodeableConcept.text', owningScope: 'Observation', logicalType: 'string' };
  const inventoryItem = { constructionChoice: { source: {
      kind: 'SEMANTIC', bindingId: 'binding', candidateId: 'candidate', nodeId: 'node', resourceType: 'Observation',
      sourcePath: 'code', sourceCanonical: undefined, sourceProfile: undefined, fieldPath: 'valueCodeableConcept.text',
      owningScope: 'Observation', keySelector: 'code.coding[].code', valueSelector: 'valueCodeableConcept.text',
      valueFallbacks: ['valueCodeableConcept.coding[].display'], fallbackOnBlankString: true,
      logicalType: 'string', ruleVersion: 'cda-rule-v1', schemaVersion: 1,
  } } };
  assert.deepEqual(validateCdaCompoundCodedGroupCandidate(candidate, sourceOption, inventoryItem), {
    codedPivotStepId: 'coded', groupStepId: 'group', category: CDA_COMPOUND_CODED_GROUP_CATEGORY,
  });
});

test('the exact-ID query is limited to the three retained project/generation-scoped Observations', () => {
  const query = buildCdaCompoundCodedGroupExactReadQuery({ project, generation });
  for (const id of CDA_COMPOUND_CODED_GROUP_IDS) assert(query.includes(id));
  assert.match(query, /d\.project == "loom_dev_cda_fhir"/);
  assert.match(query, /d\.dataset_generation == "cda-fhir-v1"/);
  assert.match(query, /FILTER d\.id IN/);
});
