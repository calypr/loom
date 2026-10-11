import assert from 'node:assert/strict';

export const CDA_COMPOUND_CODED_GROUP_SCOPE = Object.freeze({
  project: 'loom_dev_cda_fhir',
  generation: 'cda-fhir-v1',
});

export const CDA_COMPOUND_CODED_GROUP_SOURCE = Object.freeze({
  resourceType: 'Observation',
  sourcePath: 'code',
  valuePath: 'valueCodeableConcept.text',
  valueFallbacks: Object.freeze(['valueCodeableConcept.coding[].display']),
  fallbackOnBlankString: true,
  keyPath: 'code.coding[]',
});

export const CDA_COMPOUND_CODED_GROUP_CATEGORY = Object.freeze({
  system: 'http://snomed.info/sct',
  code: '1222593009',
});

export const CDA_COMPOUND_CODED_GROUP_IDS = Object.freeze([
  '26c7500c-f54f-5229-b5a7-a6757614bc21',
  '64704041-c4da-5375-aac0-88f951f78344',
  '7d2fe423-f934-56ff-bd96-426fb1fdf94e',
]);

export const CDA_COMPOUND_CODED_GROUP_ROWS = Object.freeze([
  Object.freeze(['Stage I', 2]),
  Object.freeze([null, 1]),
]);

export function buildCdaCompoundCodedGroupExactReadQuery({ project, generation }) {
  if (project !== CDA_COMPOUND_CODED_GROUP_SCOPE.project || generation !== CDA_COMPOUND_CODED_GROUP_SCOPE.generation) {
    throw new Error('Compound coded GROUP oracle requires the pinned CDA project and generation.');
  }
  return `FOR d IN Observation FILTER d.id IN ${JSON.stringify(CDA_COMPOUND_CODED_GROUP_IDS)} AND d.project == ${JSON.stringify(project)} AND d.dataset_generation == ${JSON.stringify(generation)} SORT d.id RETURN {id:d.id,project:d.project,generation:d.dataset_generation,payload:d.payload}`;
}

function exactCoding(source, expected, label) {
  const matches = (source.payload?.code?.coding ?? []).filter(coding =>
    coding.system === expected.system && coding.code === expected.code);
  assert.equal(matches.length, 1, `${label} must carry exactly one ${expected.system}|${expected.code} root Observation.code coding.`);
  return matches[0];
}

export function authoredCodedGroupValue(payload) {
  const valueConcept = payload?.valueCodeableConcept;
  const primary = valueConcept?.text;
  if (typeof primary === 'string' && (!CDA_COMPOUND_CODED_GROUP_SOURCE.fallbackOnBlankString || primary.trim() !== '')) {
    return primary;
  }
  const displays = (valueConcept?.coding ?? []).map(coding => coding.display)
    .filter(value => typeof value === 'string' && value.trim() !== '');
  assert(displays.length <= 1, 'This display projection is bounded to the retained single-display witness.');
  return displays[0] ?? null;
}

export function prepareCdaCompoundCodedGroupOracle(rows, { project, generation }) {
  assert.equal(project, CDA_COMPOUND_CODED_GROUP_SCOPE.project);
  assert.equal(generation, CDA_COMPOUND_CODED_GROUP_SCOPE.generation);
  assert(Array.isArray(rows));
  assert.equal(rows.length, CDA_COMPOUND_CODED_GROUP_IDS.length,
    'The exact-ID Arango reread must resolve all and only the three retained Observations.');

  const byID = new Map();
  for (const source of rows) {
    assert.equal(source.project, project);
    assert.equal(source.generation, generation);
    assert.equal(source.payload?.resourceType, 'Observation');
    assert(CDA_COMPOUND_CODED_GROUP_IDS.includes(source.id), `Unexpected Observation ID ${source.id}.`);
    assert(!byID.has(source.id), `Duplicate Observation ID ${source.id}.`);
    byID.set(source.id, source);
  }
  assert.deepEqual([...byID.keys()].sort(), [...CDA_COMPOUND_CODED_GROUP_IDS].sort());

  const populatedIDs = [CDA_COMPOUND_CODED_GROUP_IDS[0], CDA_COMPOUND_CODED_GROUP_IDS[1]];
  const missingID = CDA_COMPOUND_CODED_GROUP_IDS[2];
  for (const id of populatedIDs) {
    const source = byID.get(id);
    exactCoding(source, CDA_COMPOUND_CODED_GROUP_CATEGORY, id);
    const valueConcept = source.payload.valueCodeableConcept;
    assert(valueConcept && typeof valueConcept === 'object', `${id} must carry valueCodeableConcept.`);
    assert.equal(typeof valueConcept.text, 'undefined', `${id} must activate the approved coding-display fallback from absent CodeableConcept.text.`);
    assert.deepEqual((valueConcept.coding ?? []).map(coding => coding.display), ['Stage I'],
      `${id} must supply one literal valueCodeableConcept.coding[].display of Stage I.`);
    const concreteValueKeys = Object.entries(source.payload)
      .filter(([key, value]) => /^value[A-Z]/.test(key) && value != null)
      .map(([key]) => key);
    assert.deepEqual(concreteValueKeys, ['valueCodeableConcept'], `${id} must have only the proven populated value[x] selector.`);
  }
  const missing = byID.get(missingID);
  exactCoding(missing, { system: 'http://loinc.org', code: '81247-9' }, missingID);
  const missingConcreteValueKeys = Object.entries(missing.payload)
    .filter(([key, value]) => /^value[A-Z]/.test(key) && value != null)
    .map(([key]) => key);
  assert.deepEqual(missingConcreteValueKeys, [], `${missingID} must not acquire a fabricated value[x] cell.`);

  const valuesByID = Object.fromEntries(CDA_COMPOUND_CODED_GROUP_IDS.map(id => [id, authoredCodedGroupValue(byID.get(id).payload)]));
  assert.deepEqual(valuesByID, {
    [CDA_COMPOUND_CODED_GROUP_IDS[0]]: 'Stage I',
    [CDA_COMPOUND_CODED_GROUP_IDS[1]]: 'Stage I',
    [CDA_COMPOUND_CODED_GROUP_IDS[2]]: null,
  }, 'The authored text selector falls back to coding display only for blank or absent text.');

  return Object.freeze({
    project,
    generation,
    ids: Object.freeze([...CDA_COMPOUND_CODED_GROUP_IDS]),
    category: CDA_COMPOUND_CODED_GROUP_CATEGORY,
    source: CDA_COMPOUND_CODED_GROUP_SOURCE,
    valuesByID: Object.freeze(valuesByID),
    expectedRows: CDA_COMPOUND_CODED_GROUP_ROWS,
  });
}

export function validateCdaCompoundCodedGroupSourceOption(option, { outputId, snapshotToken }) {
  assert(option?.choiceId, 'The native frame chooser must return an opaque source choice ID.');
  assert.equal(option.owningScope, '', 'The coded value source must remain at the direct Observation root.');
  assert.equal(option.resourceType, CDA_COMPOUND_CODED_GROUP_SOURCE.resourceType);
  assert.equal(option.sourcePath, CDA_COMPOUND_CODED_GROUP_SOURCE.sourcePath);
  assert.equal(option.valuePath, CDA_COMPOUND_CODED_GROUP_SOURCE.valuePath);
  assert.equal(option.keyPath, CDA_COMPOUND_CODED_GROUP_SOURCE.keyPath);
  assert.deepEqual(option.route, [], 'The coded value source must remain a direct Observation-root binding.');
  assert.equal(option.defaultForm, 'ALL', 'The repeated fallback source must preserve all values by default.');
  const forms = new Map((option.forms ?? []).map(form => [form.form, form]));
  assert.equal(forms.size, (option.forms ?? []).length, 'Frame source forms must have unique form identities.');
  assert.deepEqual(forms.get('VALUE'), {
    form: 'VALUE', zeroPolicy: 'NULL', manyPolicy: 'INVALID_MULTIPLE_VALUES', decision: 'REQUIRES_DECISION',
  }, 'The scalar VALUE form must remain an explicit decision for repeated fallback values.');
  assert.deepEqual(forms.get('ALL'), {
    form: 'ALL', zeroPolicy: 'EMPTY_LIST', manyPolicy: 'ALL', decision: 'DEFAULT',
  }, 'The ALL form must preserve the repeated fallback values as the safe default.');
  assert(snapshotToken && outputId);
  return option.choiceId;
}

export function validateCdaCompoundCodedGroupInventoryItem(item, sourceOption) {
  assert(item?.constructionChoice?.choiceId, 'The exact category must have a current server-issued construction choice ID.');
  assert.equal(sourceOption.owningScope, '', 'The authorized source option must remain at the direct Observation root.');
  const semanticSource = item.constructionChoice.source;
  assert.equal(item.bindingId, sourceOption.bindingId);
  assert.equal(item.resourceType, CDA_COMPOUND_CODED_GROUP_SOURCE.resourceType);
  assert.equal(item.sourcePath, CDA_COMPOUND_CODED_GROUP_SOURCE.sourcePath);
  assert.equal(item.owningScope, sourceOption.owningScope, 'The inventory item must remain at the authorized root scope.');
  assert.equal(item.valueSelector, CDA_COMPOUND_CODED_GROUP_SOURCE.valuePath);
  assert.equal(item.system, CDA_COMPOUND_CODED_GROUP_CATEGORY.system);
  assert.equal(item.code, CDA_COMPOUND_CODED_GROUP_CATEGORY.code);
  assert.equal(item.codingVersion, '');
  assert.equal(item.valueType, 'string');
  assert(['READY', 'READY_WITH_WARNING'].includes(item.readiness?.status));
  assert.equal(semanticSource?.kind, 'SEMANTIC');
  assert.equal(semanticSource.bindingId, sourceOption.bindingId);
  assert.equal(semanticSource.resourceType, sourceOption.resourceType);
  assert.equal(semanticSource.sourcePath, sourceOption.sourcePath);
  assert.equal(semanticSource.sourceCanonical, sourceOption.sourceCanonical);
  assert.equal(semanticSource.sourceProfile, sourceOption.sourceProfile);
  assert.equal(semanticSource.owningScope ?? '', sourceOption.owningScope,
    'An omitted semantic owningScope represents the empty direct-root scope.');
  assert.equal(semanticSource.keySelector, sourceOption.keyPath);
  assert.equal(semanticSource.valueSelector, CDA_COMPOUND_CODED_GROUP_SOURCE.valuePath);
  assert.deepEqual(semanticSource.valueFallbacks, CDA_COMPOUND_CODED_GROUP_SOURCE.valueFallbacks);
  assert.equal(semanticSource.fallbackOnBlankString, CDA_COMPOUND_CODED_GROUP_SOURCE.fallbackOnBlankString);
  assert.equal(semanticSource.system, item.system);
  assert.equal(semanticSource.version ?? '', item.codingVersion,
    'An omitted semantic version represents the empty coding version.');
  assert.equal(semanticSource.code, item.code);
  assert.equal(typeof semanticSource.ruleVersion, 'string');
  assert(semanticSource.ruleVersion.length > 0);
  assert(Number.isInteger(semanticSource.schemaVersion) && semanticSource.schemaVersion > 0);
  assert.deepEqual(item.constructionChoice.route, []);
  return item.constructionChoice.choiceId;
}

export function validateCdaCompoundCodedGroupCandidate(candidateConstruction, sourceOption, inventoryItem) {
  assert.equal(sourceOption.owningScope, '', 'The authorized source option must remain at the direct Observation root.');
  const steps = candidateConstruction?.steps;
  assert(Array.isArray(steps) && steps.length === 2, 'Compound coded GROUP must contain one owned CODED_PIVOT and one GROUP step.');
  const coded = steps.find(step => step.operation?.kind === 'CODED_PIVOT');
  const group = steps.find(step => step.operation?.kind === 'GROUP');
  assert(coded && group);
  assert.equal(coded.ownerStepId, group.id, 'CODED_PIVOT must remain owned by its GROUP step.');
  assert.equal(coded.inputs?.length, 1);
  assert.equal(coded.inputs[0]?.kind, 'SOURCE_PROJECTION', 'CODED_PIVOT must read the exact direct source projection.');
  const durableSource = coded.operation.codedPivot.source;
  const family = durableSource?.family;
  const semanticSource = inventoryItem?.constructionChoice?.source;
  assert(semanticSource, 'The candidate must retain the exact semantic inventory schema binding.');
  assert.equal(family?.resourceType, sourceOption.resourceType);
  assert.equal(family?.bindingId, sourceOption.bindingId);
  assert.equal(family?.sourcePath, sourceOption.sourcePath);
  assert.equal(family?.valuePath, sourceOption.valuePath);
  assert.equal(family?.keyPath, sourceOption.keyPath);
  assert.equal(family?.sourceCanonical, sourceOption.sourceCanonical);
  assert.equal(family?.sourceProfile, sourceOption.sourceProfile);
  assert.equal(family?.owningScope, sourceOption.owningScope);
  assert.equal(family?.logicalType, sourceOption.logicalType);
  assert.deepEqual(family?.valueFallbacks, CDA_COMPOUND_CODED_GROUP_SOURCE.valueFallbacks);
  assert.equal(family?.fallbackOnBlankString, CDA_COMPOUND_CODED_GROUP_SOURCE.fallbackOnBlankString);
  assert.equal(family?.ruleVersion, semanticSource.ruleVersion);
  assert.equal(family?.schemaVersion, semanticSource.schemaVersion);
  assert.equal(durableSource.candidateId, semanticSource.candidateId);
  assert.equal(durableSource.nodeId, semanticSource.nodeId);
  assert.equal(durableSource.fieldPath, semanticSource.fieldPath);
  assert.equal(semanticSource.keySelector, family.keyPath);
  assert.equal(semanticSource.valueSelector, CDA_COMPOUND_CODED_GROUP_SOURCE.valuePath);
  assert.deepEqual(semanticSource.valueFallbacks, CDA_COMPOUND_CODED_GROUP_SOURCE.valueFallbacks);
  assert.equal(semanticSource.fallbackOnBlankString, CDA_COMPOUND_CODED_GROUP_SOURCE.fallbackOnBlankString);
  assert.equal(semanticSource.logicalType, family.logicalType);
  assert.equal(semanticSource.owningScope ?? '', family.owningScope,
    'An omitted semantic owningScope represents the empty direct-root scope.');
  assert.equal(semanticSource.sourceCanonical, family.sourceCanonical);
  assert.equal(semanticSource.sourceProfile, family.sourceProfile);
  assert.deepEqual(coded.operation.codedPivot.source?.route, []);
  assert.deepEqual(coded.operation.codedPivot.categories.map(({ system, code }) => ({ system, code })), [CDA_COMPOUND_CODED_GROUP_CATEGORY]);
  assert.equal(coded.operation.codedPivot.duplicatePolicy, 'ERROR');
  assert.equal(coded.operation.codedPivot.missingCellPolicy, 'NULL');
  assert.equal(group.inputs?.[0]?.kind, 'STEP_OUTPUT');
  assert.equal(group.inputs?.[0]?.stepId, coded.id);
  assert.equal(group.operation.group.missingKeyPolicy, 'GROUP');
  assert.equal(group.operation.group.keys?.length, 1);
  assert.equal(group.operation.group.keys[0].inputColumnId,
    coded.operation.codedPivot.categories[0].outputColumnId);
  assert.deepEqual(group.operation.group.aggregates?.map(({ operation }) => operation), ['COUNT_ROWS']);
  assert.equal(sourceOption.sourcePath, family.sourcePath);
  assert.equal(sourceOption.valuePath, family.valuePath);
  return { codedPivotStepId: coded.id, groupStepId: group.id, category: CDA_COMPOUND_CODED_GROUP_CATEGORY };
}

export function assertCdaCompoundCodedGroupRows(actualRows) {
  assert(Array.isArray(actualRows), 'Rendered Group rows must be an array.');
  const compareKey = (left, right) => left === null ? (right === null ? 0 : 1) : right === null ? -1 : left.localeCompare(right);
  const ordered = [...actualRows].map(row => [...row]).sort((left, right) => compareKey(left[0], right[0]));
  assert.deepEqual(ordered, CDA_COMPOUND_CODED_GROUP_ROWS);
  return ordered;
}
