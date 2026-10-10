import assert from 'node:assert/strict';
import test from 'node:test';

import {
  buildRelatedQuantityPivotPatientBatch,
  buildRelatedQuantityPivotSpecimenPage,
  buildRelatedQuantityPivotSpecimenPatientPairs,
  createRelatedQuantityPivotDiscoveryAccumulator,
  decodeRelatedQuantityPivotPatientBatch,
  decodeRelatedQuantityPivotSpecimenPatientPairs,
  summarizeRelatedQuantityPivotDiscoveryResult,
} from '../related-quantity-pivot-oracle.mjs';

const scope = {
  project: 'case018-project',
  dataset_generation: 'case018-generation',
  scope_allowed: true,
  auth_resource_paths_unrestricted: false,
  auth_resource_paths: ['/case018/allowed'],
  emptyPolicies: ['PRESERVE_PARENT', 'PRESERVE_PARENT'],
};

const authScope = {
  auth_resource_paths_unrestricted: false,
  auth_resource_paths: ['/case018/allowed'],
  scope_allowed: true,
};

const patientBatchWireFields = [
  'patientId', 'firstHopMissing', 'secondHopMissing', 'observationPresent',
  'conceptPresent', 'conceptType', 'textPresent', 'textType', 'text',
  'quantityPresent', 'quantityType', 'codePresent', 'codeType', 'code',
  'routeRows', 'actualRouteRows', 'emptyFirstHopRows', 'emptySecondHopRows',
  'textMissingRows', 'textNullRows', 'textStringRows', 'textOtherRows',
  'numericCount', 'missingValueCount', 'explicitNullValueCount', 'terminalNullValueRows',
  'nonNumericValueCount', 'numericSum', 'numericMax',
];

const patientGroupToWireTuple = group => patientBatchWireFields.map(field => group[field]);

const page1 = {
  project: 'case018-project',
  generation: 'case018-generation',
  authScope,
  afterSpecimenKey: '',
  pageSize: 2,
  specimens: [
    { specimenId: 'Specimen/root-a', specimenKey: 'root-a' },
    { specimenId: 'Specimen/root-b', specimenKey: 'root-b' },
  ],
  hasMore: true,
  nextAfterSpecimenKey: 'root-b',
};

const page2 = {
  project: 'case018-project',
  generation: 'case018-generation',
  authScope,
  afterSpecimenKey: 'root-b',
  pageSize: 2,
  specimens: [
    { specimenId: 'Specimen/root-c', specimenKey: 'root-c' },
    { specimenId: 'Specimen/root-d', specimenKey: 'root-d' },
  ],
  hasMore: true,
  nextAfterSpecimenKey: 'root-d',
};

const page3 = {
  project: 'case018-project',
  generation: 'case018-generation',
  authScope,
  afterSpecimenKey: 'root-d',
  pageSize: 2,
  specimens: [
    { specimenId: 'Specimen/root-e', specimenKey: 'root-e' },
  ],
  hasMore: false,
  nextAfterSpecimenKey: null,
};

const pairs1 = {
  project: 'case018-project',
  generation: 'case018-generation',
  authScope,
  specimenIds: ['Specimen/root-a', 'Specimen/root-b'],
  rows: [
    { specimenId: 'Specimen/root-a', patientId: 'Patient/patient-a' },
    { specimenId: 'Specimen/root-b', patientId: 'Patient/patient-a' },
  ],
  overflow: false,
  truncated: false,
};

const pairs2 = {
  project: 'case018-project',
  generation: 'case018-generation',
  authScope,
  specimenIds: ['Specimen/root-c', 'Specimen/root-d'],
  rows: [
    { specimenId: 'Specimen/root-c', patientId: 'Patient/patient-b' },
    { specimenId: 'Specimen/root-d', patientId: null },
  ],
  overflow: false,
  truncated: false,
};

const pairs3 = {
  project: 'case018-project',
  generation: 'case018-generation',
  authScope,
  specimenIds: ['Specimen/root-e'],
  rows: [{ specimenId: 'Specimen/root-e', patientId: 'Patient/patient-c' }],
  overflow: false,
  truncated: false,
};

const patientBatchA = {
  project: 'case018-project',
  generation: 'case018-generation',
  authScope,
  patientIds: ['Patient/patient-a'],
  overflow: false,
  truncated: false,
  groups: [
    {
      patientId: 'Patient/patient-a', firstHopMissing: false, secondHopMissing: false, observationPresent: true,
      conceptPresent: true, conceptType: 'OBJECT',
      textPresent: false, textType: 'MISSING', text: null,
      codePresent: false, codeType: 'MISSING', code: null,
      quantityPresent: true, quantityType: 'OBJECT',
      routeRows: 1, actualRouteRows: 1, emptyFirstHopRows: 0, emptySecondHopRows: 0,
      textMissingRows: 1, textNullRows: 0, textStringRows: 0, textOtherRows: 0,
      numericCount: 0, missingValueCount: 1, explicitNullValueCount: 0, terminalNullValueRows: 0,
      nonNumericValueCount: 0, numericSum: 0, numericMax: null,
    },
    {
      patientId: 'Patient/patient-a', firstHopMissing: false, secondHopMissing: false, observationPresent: true,
      conceptPresent: true, conceptType: 'OBJECT',
      textPresent: true, textType: 'NULL', text: null,
      codePresent: true, codeType: 'NULL', code: null,
      quantityPresent: true, quantityType: 'OBJECT',
      routeRows: 1, actualRouteRows: 1, emptyFirstHopRows: 0, emptySecondHopRows: 0,
      textMissingRows: 0, textNullRows: 1, textStringRows: 0, textOtherRows: 0,
      numericCount: 0, missingValueCount: 0, explicitNullValueCount: 1, terminalNullValueRows: 0,
      nonNumericValueCount: 0, numericSum: 0, numericMax: null,
    },
    {
      patientId: 'Patient/patient-a', firstHopMissing: false, secondHopMissing: false, observationPresent: true,
      conceptPresent: true, conceptType: 'OBJECT',
      textPresent: true, textType: 'NUMBER', text: 42,
      codePresent: true, codeType: 'STRING', code: 'd',
      quantityPresent: true, quantityType: 'OBJECT',
      routeRows: 1, actualRouteRows: 1, emptyFirstHopRows: 0, emptySecondHopRows: 0,
      textMissingRows: 0, textNullRows: 0, textStringRows: 0, textOtherRows: 1,
      numericCount: 1, missingValueCount: 0, explicitNullValueCount: 0, terminalNullValueRows: 0,
      nonNumericValueCount: 0, numericSum: 7, numericMax: 7,
    },
    {
      patientId: 'Patient/patient-a', firstHopMissing: false, secondHopMissing: false, observationPresent: true,
      conceptPresent: true, conceptType: 'OBJECT',
      textPresent: true, textType: 'STRING', text: 'FINAL',
      codePresent: true, codeType: 'STRING', code: 'd',
      quantityPresent: true, quantityType: 'OBJECT',
      routeRows: 2, actualRouteRows: 2, emptyFirstHopRows: 0, emptySecondHopRows: 0,
      textMissingRows: 0, textNullRows: 0, textStringRows: 2, textOtherRows: 0,
      numericCount: 2, missingValueCount: 0, explicitNullValueCount: 0, terminalNullValueRows: 0,
      nonNumericValueCount: 0, numericSum: 5, numericMax: 3,
    },
  ],
};

const patientBatchB = {
  project: 'case018-project',
  generation: 'case018-generation',
  authScope,
  patientIds: ['Patient/patient-b'],
  overflow: false,
  truncated: false,
  groups: [{
    patientId: 'Patient/patient-b', firstHopMissing: false, secondHopMissing: true, observationPresent: false,
    conceptPresent: true, conceptType: 'NULL',
    textPresent: true, textType: 'NULL', text: null,
    codePresent: true, codeType: 'NULL', code: null,
    quantityPresent: true, quantityType: 'NULL',
    routeRows: 1, actualRouteRows: 0, emptyFirstHopRows: 0, emptySecondHopRows: 1,
    textMissingRows: 0, textNullRows: 1, textStringRows: 0, textOtherRows: 0,
    numericCount: 0, missingValueCount: 0, explicitNullValueCount: 0, terminalNullValueRows: 1,
    nonNumericValueCount: 0, numericSum: 0, numericMax: null,
  }],
};

// The request result is grouped by Patient and does not retain Observation IDs.
// This second Patient's identical one-row FINAL/d numeric group models the same
// Observation value (2) reached through a distinct Patient lineage; the host
// must count that Patient's route separately while preserving each Patient's
// source group until the final cross-root merge.
const patientBatchC = {
  project: 'case018-project',
  generation: 'case018-generation',
  authScope,
  patientIds: ['Patient/patient-c'],
  overflow: false,
  truncated: false,
  groups: [{
    patientId: 'Patient/patient-c', firstHopMissing: false, secondHopMissing: false, observationPresent: true,
    conceptPresent: true, conceptType: 'OBJECT',
    textPresent: true, textType: 'STRING', text: 'FINAL',
    codePresent: true, codeType: 'STRING', code: 'd',
    quantityPresent: true, quantityType: 'OBJECT',
    routeRows: 1, actualRouteRows: 1, emptyFirstHopRows: 0, emptySecondHopRows: 0,
    textMissingRows: 0, textNullRows: 0, textStringRows: 1, textOtherRows: 0,
    numericCount: 1, missingValueCount: 0, explicitNullValueCount: 0, terminalNullValueRows: 0,
    nonNumericValueCount: 0, numericSum: 2, numericMax: 2,
  }],
};

const compareGroups = groups => [...groups].sort((left, right) => {
  const leftKey = JSON.stringify([
    left.patientId, left.firstHopMissing, left.secondHopMissing, left.observationPresent,
    left.textType, left.text, left.codeType, left.code,
  ]);
  const rightKey = JSON.stringify([
    right.patientId, right.firstHopMissing, right.secondHopMissing, right.observationPresent,
    right.textType, right.text, right.codeType, right.code,
  ]);
  return leftKey.localeCompare(rightKey);
});

const assertPatternCount = (source, pattern, expected, label) => {
  const count = [...source.matchAll(pattern)].length;
  assert.equal(count, expected, `${label} must appear exactly ${expected} time(s)`);
};

const assertEntityScope = (query, {
  alias, resourceType, allowedVariable, expectedEntityCount = 1, expectedAuthCount = 1,
  filterPrefix = `${alias} != null AND`,
}) => {
  const entityPattern = new RegExp(
    `FILTER ${filterPrefix} ${alias}\\.project == @project AND ${alias}\\.dataset_generation == @generation\\s+AND ${alias}\\.resourceType == "${resourceType}" AND ${alias}\\.payload\\.resourceType == "${resourceType}"`,
    'g',
  );
  const authPattern = new RegExp(
    `LET ${allowedVariable} = @auth_resource_paths_unrestricted == true OR ${alias}\\.auth_resource_path IN authResourcePaths\\s+FILTER ${allowedVariable} == @scope_allowed`,
    'g',
  );
  assertPatternCount(query, entityPattern, expectedEntityCount, `${alias} ${resourceType} project/generation/type scope`);
  assertPatternCount(query, authPattern, expectedAuthCount, `${alias} ${resourceType} auth-resource/allow filter`);
};

const assertEdgeScope = (query, { edgeFilter, allowedVariable = 'edgeAllowed' }) => {
  assertPatternCount(query, new RegExp(`FILTER ${edgeFilter}`, 'g'), 1, 'related edge project/generation/relationship scope');
  assertPatternCount(query, new RegExp(
    `LET ${allowedVariable} = @auth_resource_paths_unrestricted == true OR e\\.auth_resource_path IN authResourcePaths\\s+FILTER ${allowedVariable} == @scope_allowed`,
    'g',
  ), 1, 'related edge auth-resource/allow filter');
};

const addAllSpecimenPages = accumulator => {
  accumulator.addSpecimenPage(page1);
  accumulator.addSpecimenPatientPairs(pairs1);
  accumulator.addSpecimenPage(page2);
  accumulator.addSpecimenPatientPairs(pairs2);
  accumulator.addSpecimenPage(page3);
  accumulator.addSpecimenPatientPairs(pairs3);
};

test('CASE-018 paged discovery weights shared routes and matches literal typed host aggregates', () => {
  const firstPageQuery = buildRelatedQuantityPivotSpecimenPage(scope, { afterSpecimenKey: '', pageSize: 2 });
  const secondPageQuery = buildRelatedQuantityPivotSpecimenPage(scope, { afterSpecimenKey: 'root-b', pageSize: 2 });
  const thirdPageQuery = buildRelatedQuantityPivotSpecimenPage(scope, { afterSpecimenKey: 'root-d', pageSize: 2 });
  assert.equal(firstPageQuery.bindVars.after_specimen_key, '');
  assert.equal(firstPageQuery.bindVars.page_size, 2);
  assert.equal(firstPageQuery.bindVars.project, scope.project);
  assert.equal(firstPageQuery.bindVars.generation, scope.dataset_generation);
  assert.equal(firstPageQuery.bindVars.scope_allowed, true);
  assert.equal(firstPageQuery.bindVars.auth_resource_paths_unrestricted, false);
  assert.deepEqual(firstPageQuery.bindVars.auth_resource_paths, ['/case018/allowed']);
  assert.equal(secondPageQuery.bindVars.after_specimen_key, 'root-b');
  assert.equal(secondPageQuery.bindVars.page_size, 2);
  assert.equal(thirdPageQuery.bindVars.after_specimen_key, 'root-d');
  assert.equal(thirdPageQuery.bindVars.page_size, 2);

  const pairQuery = buildRelatedQuantityPivotSpecimenPatientPairs(scope, page2.specimens.map(row => row.specimenId), { maxRows: 10 });
  assert.deepEqual(pairQuery.bindVars.specimen_ids, ['Specimen/root-c', 'Specimen/root-d']);
  assert.equal(pairQuery.bindVars.max_rows, 10);
  const batchQuery = buildRelatedQuantityPivotPatientBatch(scope, ['Patient/patient-a'], { maxRows: 10 });
  assert.deepEqual(batchQuery.bindVars.patient_ids, ['Patient/patient-a']);
  assert.equal(batchQuery.bindVars.max_rows, 10);
  const sharedObservationGroupForPatientA = patientBatchA.groups.find(group => group.text === 'FINAL' && group.code === 'd');
  const sharedObservationGroupForPatientC = patientBatchC.groups[0];
  assert.deepEqual({
    patientId: sharedObservationGroupForPatientA.patientId,
    routeRows: sharedObservationGroupForPatientA.routeRows,
    numericCount: sharedObservationGroupForPatientA.numericCount,
    numericSum: sharedObservationGroupForPatientA.numericSum,
    numericMax: sharedObservationGroupForPatientA.numericMax,
  }, { patientId: 'Patient/patient-a', routeRows: 2, numericCount: 2, numericSum: 5, numericMax: 3 });
  assert.deepEqual({
    patientId: sharedObservationGroupForPatientC.patientId,
    routeRows: sharedObservationGroupForPatientC.routeRows,
    numericCount: sharedObservationGroupForPatientC.numericCount,
    numericSum: sharedObservationGroupForPatientC.numericSum,
    numericMax: sharedObservationGroupForPatientC.numericMax,
  }, { patientId: 'Patient/patient-c', routeRows: 1, numericCount: 1, numericSum: 2, numericMax: 2 });
  for (const builder of [pairQuery, batchQuery]) {
    assert.equal(builder.bindVars.project, scope.project);
    assert.equal(builder.bindVars.generation, scope.dataset_generation);
    assert.equal(builder.bindVars.scope_allowed, true);
    assert.equal(builder.bindVars.auth_resource_paths_unrestricted, false);
    assert.deepEqual(builder.bindVars.auth_resource_paths, ['/case018/allowed']);
  }

  for (const builder of [firstPageQuery, pairQuery, batchQuery]) {
    assert.equal(Object.hasOwn(builder.bindVars, 'visible_text_keys'), false, 'Raw key discovery must not depend on preview-derived text keys');
  }

  // Scope is enforced independently at every collection and edge boundary in
  // each query; a filter on one join endpoint cannot stand in for another.
  assert.match(firstPageQuery.query, /s\._key > \@after_specimen_key/);
  assertEntityScope(firstPageQuery.query, {
    alias: 's', resourceType: 'Specimen', allowedVariable: 'allowed', filterPrefix: 's\\._key > @after_specimen_key\\s+AND',
  });
  assert.match(firstPageQuery.query, /SORT s\._key\s+LIMIT @page_size \+ 1/);
  assertEntityScope(pairQuery.query, {
    alias: 's', resourceType: 'Specimen', allowedVariable: 'allowed', expectedEntityCount: 2,
  });
  assertEntityScope(pairQuery.query, {
    alias: 's', resourceType: 'Specimen', allowedVariable: 'specimenAllowed', expectedEntityCount: 2,
  });
  assertEntityScope(pairQuery.query, { alias: 'p', resourceType: 'Patient', allowedVariable: 'patientAllowed' });
  assertEdgeScope(pairQuery.query, {
    edgeFilter: 'e\\._from IN scopedSpecimenIds AND e\\.project == @project AND e\\.dataset_generation == @generation\\s+AND e\\.label == "subject_Patient" AND e\\.from_type == "Specimen" AND e\\.to_type == "Patient"',
  });
  assert.match(pairQuery.query, /RETURN \[row\.specimenId, row\.patientId\]/);
  assert.match(pairQuery.query, /wireVersion: 1/);
  assert.match(pairQuery.query, /COLLECT patientId = p\._id/);
  assert.match(pairQuery.query, /LIMIT \@max_rows \+ 1/);
  assertEntityScope(batchQuery.query, { alias: 'p', resourceType: 'Patient', allowedVariable: 'allowed' });
  assertEntityScope(batchQuery.query, { alias: 'candidate', resourceType: 'Observation', allowedVariable: 'observationAllowed' });
  assertEdgeScope(batchQuery.query, {
    edgeFilter: 'e\\._to IN scopedPatientIds AND e\\.project == @project AND e\\.dataset_generation == @generation\\s+AND e\\.label == "subject_Patient" AND e\\.from_type == "Observation" AND e\\.to_type == "Patient"',
  });
  assert.match(batchQuery.query, /LET compactGroups =/);
  assert.match(batchQuery.query, /RETURN \[group\.patientId, group\.firstHopMissing, group\.secondHopMissing, group\.observationPresent/);
  assert.match(batchQuery.query, /wireVersion: 1/);
  assert.match(batchQuery.query, /COLLECT observationId = candidate\._id/);
  assert.match(batchQuery.query, /LIMIT \@max_rows \+ 1/);

  const accumulator = createRelatedQuantityPivotDiscoveryAccumulator(scope, {
    specimenPageSize: 2,
    maxSpecimenPatientRows: 10,
    maxPatientGroups: 10,
  });
  accumulator.addSpecimenPage(page1);
  accumulator.addSpecimenPatientPairs(pairs1);
  assert.throws(() => accumulator.nextPatientBatch(1), /complete|page|settled/i);
  accumulator.addSpecimenPage(page2);
  accumulator.addSpecimenPatientPairs(pairs2);
  accumulator.addSpecimenPage(page3);
  accumulator.addSpecimenPatientPairs(pairs3);

  const firstBatch = accumulator.nextPatientBatch(1);
  assert.deepEqual(firstBatch, ['Patient/patient-a']);
  const firstBatchQuery = buildRelatedQuantityPivotPatientBatch(scope, firstBatch, { maxRows: 10 });
  assert.deepEqual(firstBatchQuery.bindVars.patient_ids, firstBatch);
  accumulator.addPatientBatch(patientBatchA);

  const secondBatch = accumulator.nextPatientBatch(1);
  assert.deepEqual(secondBatch, ['Patient/patient-b']);
  accumulator.addPatientBatch(patientBatchB);
  const thirdBatch = accumulator.nextPatientBatch(1);
  assert.deepEqual(thirdBatch, ['Patient/patient-c']);
  const thirdBatchQuery = buildRelatedQuantityPivotPatientBatch(scope, thirdBatch, { maxRows: 10 });
  assert.deepEqual(thirdBatchQuery.bindVars.patient_ids, ['Patient/patient-c']);
  assert.deepEqual(patientBatchC.groups.map(group => group.patientId), ['Patient/patient-c']);
  accumulator.addPatientBatch(patientBatchC);
  assert.deepEqual(accumulator.nextPatientBatch(1), []);

  const summary = accumulator.finalize();
  assert.deepEqual({
    project: summary.project,
    generation: summary.generation,
    authScope: summary.authScope,
    complete: summary.complete,
    sourceRows: summary.sourceRows,
    specimenCount: summary.specimenCount,
    emptySpecimenCount: summary.emptySpecimenCount,
    matchedPatientRows: summary.matchedPatientRows,
    emptyPatientObservationCount: summary.emptyPatientObservationCount,
    matchedObservationRows: summary.matchedObservationRows,
    preservedEmptySpecimenRows: summary.preservedEmptySpecimenRows,
    preservedEmptyPatientRows: summary.preservedEmptyPatientRows,
    fullRouteCounts: summary.fullRouteCounts,
    categoryDomain: summary.categoryDomain,
    visibleTextDomain: summary.visibleTextDomain,
    unsupportedTextGroupCount: summary.unsupportedTextGroupCount,
  }, {
    project: 'case018-project',
    generation: 'case018-generation',
    authScope,
    complete: true,
    sourceRows: 13,
    specimenCount: 5,
    emptySpecimenCount: 1,
    matchedPatientRows: 4,
    emptyPatientObservationCount: 1,
    matchedObservationRows: 11,
    preservedEmptySpecimenRows: 1,
    preservedEmptyPatientRows: 1,
    fullRouteCounts: {
      totalSpecimens: 5,
      matchedSpecimens: 4,
      emptyFirstHopSpecimenRoots: 1,
      distinctSpecimenPatientPairs: 4,
      patientsWithSpecimenRoots: 3,
      patientObservationGroups: 6,
      uniquePatientObservationPairs: 6,
      emptySecondHopPatientRows: 1,
      emptySecondHopPatientCount: 1,
      actualRouteRows: 11,
      leftJoinOutputRows: 13,
      rawTextTypedCodeGroupCount: 6,
      retainedRawGroupAccountingBytes: 10150,
      retainedRawGroupCountHighWater: 6,
      retainedRawGroupBytesHighWater: 10150,
      visibleTextGroupCount: 3,
      unsupportedTextGroupCount: 1,
      visiblePivotCellCount: 4,
      categoryDomainCount: 3,
      nonNumericValueCount: 0,
    },
    categoryDomain: [
      { categoryPresent: false, categoryType: 'MISSING', category: null },
      { categoryPresent: true, categoryType: 'NULL', category: null },
      { categoryPresent: true, categoryType: 'STRING', category: 'd' },
    ],
    visibleTextDomain: [
      { textType: 'NULL', text: null },
      { textType: 'NUMBER', text: 42 },
      { textType: 'STRING', text: 'FINAL' },
    ],
    unsupportedTextGroupCount: 1,
  });

  const selectedPreview = summarizeRelatedQuantityPivotDiscoveryResult(summary, scope, { visibleTextKeys: ['FINAL'] });
  assert.deepEqual(selectedPreview.groups.map(group => ({
    groupTextPresent: group.groupTextPresent,
    groupText: group.groupText,
    categoryPresent: group.categoryPresent,
    category: group.category,
    sourceRows: group.sourceRows,
    numericRows: group.numericRows,
    valueSum: group.valueSum,
    valueMax: group.valueMax,
  })), [{
    groupTextPresent: true,
    groupText: 'FINAL',
    categoryPresent: true,
    category: 'd',
    sourceRows: 5,
    numericRows: 5,
    valueSum: 12,
    valueMax: 3,
  }]);
  assert.equal(selectedPreview.sampleSourceRows, 5);
  assert.deepEqual(selectedPreview.previewTextGroupKeys, ['FINAL']);
  assert.equal(selectedPreview.sourceRows, summary.sourceRows, 'Preview selection must retain the independent full route count');
  assert.deepEqual(selectedPreview.fullRouteCounts, summary.fullRouteCounts);
  assert.deepEqual(selectedPreview.categoryDomain, [
    { key: { kind: 'MISSING' } },
    { key: { kind: 'NULL' } },
    { key: { kind: 'STRING', string: 'd' } },
  ]);
  assert.equal(selectedPreview.fullGroupCount, summary.fullRouteCounts.visibleTextGroupCount);
  assert.equal(selectedPreview.fullCellCount, summary.fullRouteCounts.visiblePivotCellCount);
  assert.equal(selectedPreview.fullCategoryCount, summary.fullRouteCounts.categoryDomainCount);
  assert.throws(() => summarizeRelatedQuantityPivotDiscoveryResult({ ...summary, complete: false }, scope, { visibleTextKeys: ['FINAL'] }), /complete/i);
  assert.throws(() => summarizeRelatedQuantityPivotDiscoveryResult({ ...summary, generation: 'other-generation' }, scope, { visibleTextKeys: ['FINAL'] }), /scope|generation|identity/i);
  assert.throws(() => summarizeRelatedQuantityPivotDiscoveryResult(summary, scope, { visibleTextKeys: ['NOT IN DISCOVERED DOMAIN'] }), /absent.*complete.*domain/i);

  assert.deepEqual(summary.groups, compareGroups(summary.groups), 'Final host groups must be emitted in deterministic typed-key order');
  assert.deepEqual(compareGroups(summary.groups), compareGroups([
    {
      patientId: null, firstHopMissing: true, secondHopMissing: false, observationPresent: false,
      conceptPresent: true, conceptType: 'NULL',
      textPresent: true, textType: 'NULL', text: null,
      codePresent: true, codeType: 'NULL', code: null,
      quantityPresent: true, quantityType: 'NULL',
      routeRows: 1, actualRouteRows: 0, emptyFirstHopRows: 1, emptySecondHopRows: 0,
      textMissingRows: 0, textNullRows: 1, textStringRows: 0, textOtherRows: 0,
      numericCount: 0, missingValueCount: 0, explicitNullValueCount: 0, terminalNullValueRows: 1,
      nonNumericValueCount: 0, numericSum: 0, numericMax: null,
    },
    {
      patientId: null, firstHopMissing: false, secondHopMissing: true, observationPresent: false,
      conceptPresent: true, conceptType: 'NULL',
      textPresent: true, textType: 'NULL', text: null,
      codePresent: true, codeType: 'NULL', code: null,
      quantityPresent: true, quantityType: 'NULL',
      routeRows: 1, actualRouteRows: 0, emptyFirstHopRows: 0, emptySecondHopRows: 1,
      textMissingRows: 0, textNullRows: 1, textStringRows: 0, textOtherRows: 0,
      numericCount: 0, missingValueCount: 0, explicitNullValueCount: 0, terminalNullValueRows: 1,
      nonNumericValueCount: 0, numericSum: 0, numericMax: null,
    },
    {
      patientId: null, firstHopMissing: false, secondHopMissing: false, observationPresent: true,
      conceptPresent: true, conceptType: 'OBJECT',
      textPresent: true, textType: 'STRING', text: 'FINAL',
      codePresent: true, codeType: 'STRING', code: 'd',
      quantityPresent: true, quantityType: 'OBJECT',
      routeRows: 5, actualRouteRows: 5, emptyFirstHopRows: 0, emptySecondHopRows: 0,
      textMissingRows: 0, textNullRows: 0, textStringRows: 5, textOtherRows: 0,
      numericCount: 5, missingValueCount: 0, explicitNullValueCount: 0, terminalNullValueRows: 0,
      nonNumericValueCount: 0, numericSum: 12, numericMax: 3,
    },
    {
      patientId: null, firstHopMissing: false, secondHopMissing: false, observationPresent: true,
      conceptPresent: true, conceptType: 'OBJECT',
      textPresent: false, textType: 'MISSING', text: null,
      codePresent: false, codeType: 'MISSING', code: null,
      quantityPresent: true, quantityType: 'OBJECT',
      routeRows: 2, actualRouteRows: 2, emptyFirstHopRows: 0, emptySecondHopRows: 0,
      textMissingRows: 2, textNullRows: 0, textStringRows: 0, textOtherRows: 0,
      numericCount: 0, missingValueCount: 2, explicitNullValueCount: 0, terminalNullValueRows: 0,
      nonNumericValueCount: 0, numericSum: 0, numericMax: null,
    },
    {
      patientId: null, firstHopMissing: false, secondHopMissing: false, observationPresent: true,
      conceptPresent: true, conceptType: 'OBJECT',
      textPresent: true, textType: 'NULL', text: null,
      codePresent: true, codeType: 'NULL', code: null,
      quantityPresent: true, quantityType: 'OBJECT',
      routeRows: 2, actualRouteRows: 2, emptyFirstHopRows: 0, emptySecondHopRows: 0,
      textMissingRows: 0, textNullRows: 2, textStringRows: 0, textOtherRows: 0,
      numericCount: 0, missingValueCount: 0, explicitNullValueCount: 2, terminalNullValueRows: 0,
      nonNumericValueCount: 0, numericSum: 0, numericMax: null,
    },
    {
      patientId: null, firstHopMissing: false, secondHopMissing: false, observationPresent: true,
      conceptPresent: true, conceptType: 'OBJECT',
      textPresent: true, textType: 'NUMBER', text: 42,
      codePresent: true, codeType: 'STRING', code: 'd',
      quantityPresent: true, quantityType: 'OBJECT',
      routeRows: 2, actualRouteRows: 2, emptyFirstHopRows: 0, emptySecondHopRows: 0,
      textMissingRows: 0, textNullRows: 0, textStringRows: 0, textOtherRows: 2,
      numericCount: 2, missingValueCount: 0, explicitNullValueCount: 0, terminalNullValueRows: 0,
      nonNumericValueCount: 0, numericSum: 14, numericMax: 7,
    },
  ]));
});

test('CASE-018 streaming discovery preserves one shared Observation on distinct Patient routes and deduplicates edge responses', () => {
  const pairQuery = buildRelatedQuantityPivotSpecimenPatientPairs(scope, ['Specimen/root-a', 'Specimen/root-b'], { maxRows: 10 });
  const patientQuery = buildRelatedQuantityPivotPatientBatch(scope, ['Patient/patient-a', 'Patient/patient-b'], { maxRows: 10 });
  assert.match(pairQuery.query, /COLLECT patientId = p\._id, specimenId = s\._id/,
    'duplicate Specimen→Patient edges collapse to one response row per root/patient pair');
  assert.match(patientQuery.query, /COLLECT observationId = candidate\._id, patientId = e\._to/,
    'duplicate Observation→Patient edges collapse per Observation and Patient without collapsing distinct Patient routes');

  const accumulator = createRelatedQuantityPivotDiscoveryAccumulator(scope, {
    specimenPageSize: 2,
    maxSpecimenPatientRows: 10,
    maxPatientGroups: 10,
  });
  accumulator.addSpecimenPage({
    ...page1,
    specimens: [
      { specimenId: 'Specimen/root-a', specimenKey: 'root-a' },
      { specimenId: 'Specimen/root-b', specimenKey: 'root-b' },
    ],
    hasMore: false,
    nextAfterSpecimenKey: null,
  });

  // Each duplicate raw edge pair is represented once in the bounded AQL
  // response, while the same Observation remains paired with both Patients.
  const specimenPatientEdges = [
    ['Specimen/root-a', 'Patient/patient-a'], ['Specimen/root-a', 'Patient/patient-a'],
    ['Specimen/root-b', 'Patient/patient-b'], ['Specimen/root-b', 'Patient/patient-b'],
  ];
  const uniqueSpecimenPatientPairs = [...new Map(specimenPatientEdges.map(([specimenId, patientId]) => [
    JSON.stringify([specimenId, patientId]), { specimenId, patientId },
  ])).values()];
  accumulator.addSpecimenPatientPairs({
    project: scope.project,
    generation: scope.dataset_generation,
    authScope,
    specimenIds: ['Specimen/root-a', 'Specimen/root-b'],
    rows: uniqueSpecimenPatientPairs,
    overflow: false,
    truncated: false,
  });

  const observationPatientEdges = [
    ['Observation/shared', 'Patient/patient-a'], ['Observation/shared', 'Patient/patient-a'],
    ['Observation/shared', 'Patient/patient-b'], ['Observation/shared', 'Patient/patient-b'],
  ];
  const uniqueObservationPatientEdges = [...new Map(observationPatientEdges.map(([observationId, patientId]) => [
    JSON.stringify([observationId, patientId]), [observationId, patientId],
  ])).values()];
  assert.deepEqual(uniqueObservationPatientEdges, [
    ['Observation/shared', 'Patient/patient-a'],
    ['Observation/shared', 'Patient/patient-b'],
  ]);
  assert.deepEqual(accumulator.nextPatientBatch(2), ['Patient/patient-a', 'Patient/patient-b']);

  // The query groups by (observationId, patientId); its bounded response then
  // carries one aggregate group for each Patient lineage.
  const sharedObservationGroups = uniqueObservationPatientEdges.map(([observationId, patientId]) => {
    assert.equal(observationId, 'Observation/shared');
    return {
      ...patientBatchC.groups[0],
      patientId,
      numericSum: 7,
      numericMax: 7,
    };
  });
  accumulator.addPatientBatch({
    project: scope.project,
    generation: scope.dataset_generation,
    authScope,
    patientIds: ['Patient/patient-a', 'Patient/patient-b'],
    groups: sharedObservationGroups,
    overflow: false,
    truncated: false,
  });
  assert.deepEqual(accumulator.nextPatientBatch(2), []);

  const summary = accumulator.finalize();
  assert.deepEqual({
    sourceRows: summary.sourceRows,
    specimens: summary.specimenCount,
    patientPaths: summary.matchedPatientRows,
    observations: summary.matchedObservationRows,
    distinctSpecimenPatientPairs: summary.fullRouteCounts.distinctSpecimenPatientPairs,
    patientObservationGroups: summary.fullRouteCounts.patientObservationGroups,
    uniquePatientObservationPairs: summary.fullRouteCounts.uniquePatientObservationPairs,
    sum: summary.groups[0].numericSum,
    max: summary.groups[0].numericMax,
  }, {
    sourceRows: 2,
    specimens: 2,
    patientPaths: 2,
    observations: 2,
    distinctSpecimenPatientPairs: 2,
    patientObservationGroups: 2,
    uniquePatientObservationPairs: 2,
    sum: 14,
    max: 7,
  });
});

test('CASE-018 compact discovery wire decoders restore literal pair and typed Patient-group objects', () => {
  const pairPayload = {
    wireVersion: 1,
    project: scope.project,
    generation: scope.dataset_generation,
    authScope,
    specimenIds: ['Specimen/root-a', 'Specimen/root-b'],
    rows: [['Specimen/root-a', 'Patient/patient-a'], ['Specimen/root-b', null]],
    overflow: false,
    truncated: false,
  };
  assert.deepEqual(decodeRelatedQuantityPivotSpecimenPatientPairs(pairPayload), {
    ...pairPayload,
    rows: [
      { specimenId: 'Specimen/root-a', patientId: 'Patient/patient-a' },
      { specimenId: 'Specimen/root-b', patientId: null },
    ],
  });

  const expectedGroup = patientBatchA.groups[0];
  assert.equal(patientBatchWireFields.length, 29);
  const patientPayload = {
    wireVersion: 1,
    project: scope.project,
    generation: scope.dataset_generation,
    authScope,
    patientIds: ['Patient/patient-a'],
    groups: [patientGroupToWireTuple(expectedGroup)],
    overflow: false,
    truncated: false,
  };
  assert.deepEqual(decodeRelatedQuantityPivotPatientBatch(patientPayload), {
    ...patientPayload,
    groups: [expectedGroup],
  });

  assert.throws(() => decodeRelatedQuantityPivotSpecimenPatientPairs({ ...pairPayload, wireVersion: 2 }), /unsupported compact wire version/i);
  assert.throws(() => decodeRelatedQuantityPivotSpecimenPatientPairs({ ...pairPayload, rows: [['Specimen/root-a']] }), /invalid shape/i);
  assert.throws(() => decodeRelatedQuantityPivotSpecimenPatientPairs({ ...pairPayload, rows: [['', 'Patient/patient-a']] }), /invalid identifier/i);
  assert.throws(() => decodeRelatedQuantityPivotPatientBatch({ ...patientPayload, wireVersion: 2 }), /unsupported compact wire version/i);
  assert.throws(() => decodeRelatedQuantityPivotPatientBatch({ ...patientPayload, groups: [[...patientGroupToWireTuple(expectedGroup).slice(0, -1)]] }), /invalid shape/i);
});

test('CASE-018 accumulates one typed raw group across more than 25,000 bounded Patient groups', () => {
  const patientGroupCount = 25_001;
  const patientIds = Array.from({ length: patientGroupCount }, (_, index) => `Patient/p-${String(index).padStart(5, '0')}`);
  const accumulator = createRelatedQuantityPivotDiscoveryAccumulator(scope, {
    specimenPageSize: 1,
    maxSpecimenPatientRows: patientGroupCount,
    maxPatientGroups: 25_000,
    maxRetainedRawGroups: 10,
    maxRetainedRawGroupBytes: 1024 * 1024,
  });
  accumulator.addSpecimenPage({
    project: scope.project,
    generation: scope.dataset_generation,
    authScope,
    afterSpecimenKey: '',
    pageSize: 1,
    specimens: [{ specimenId: 'Specimen/root-a', specimenKey: 'root-a' }],
    hasMore: false,
    nextAfterSpecimenKey: null,
  });
  const compactPairs = {
    wireVersion: 1,
    project: scope.project,
    generation: scope.dataset_generation,
    authScope,
    specimenIds: ['Specimen/root-a'],
    rows: patientIds.map(patientId => ['Specimen/root-a', patientId]),
    overflow: false,
    truncated: false,
  };
  accumulator.addSpecimenPatientPairs(decodeRelatedQuantityPivotSpecimenPatientPairs(compactPairs));

  let processedPatientGroups = 0;
  while (processedPatientGroups < patientGroupCount) {
    const requestedPatients = accumulator.nextPatientBatch(100);
    assert.ok(requestedPatients.length > 0 && requestedPatients.length <= 100);
    const groups = requestedPatients.map(patientId => patientGroupToWireTuple({
      ...patientBatchC.groups[0],
      patientId,
      numericSum: 2,
      numericMax: 2,
    }));
    const compactBatch = {
      wireVersion: 1,
      project: scope.project,
      generation: scope.dataset_generation,
      authScope,
      patientIds: requestedPatients,
      groups,
      overflow: false,
      truncated: false,
    };
    accumulator.addPatientBatch(decodeRelatedQuantityPivotPatientBatch(compactBatch));
    processedPatientGroups += groups.length;
  }
  assert.deepEqual(accumulator.nextPatientBatch(100), []);
  const retained = accumulator.partialSnapshot();
  assert.deepEqual({
    patientObservationGroups: retained.patientObservationGroups,
    retainedRawGroupCount: retained.retainedRawGroupCount,
    retainedRawGroupBytesWithinCap: retained.retainedRawGroupBytes <= retained.maxRetainedRawGroupBytes,
  }, {
    patientObservationGroups: 25_001,
    retainedRawGroupCount: 1,
    retainedRawGroupBytesWithinCap: true,
  });

  const summary = accumulator.finalize();
  assert.deepEqual({
    complete: summary.complete,
    patientObservationGroups: summary.fullRouteCounts.patientObservationGroups,
    rawTypedTextCodeGroupCount: summary.fullRouteCounts.rawTextTypedCodeGroupCount,
    sourceRows: summary.sourceRows,
    matchedObservationRows: summary.matchedObservationRows,
    numericCount: summary.groups[0].numericCount,
    numericSum: summary.groups[0].numericSum,
    numericMax: summary.groups[0].numericMax,
    retainedRawGroupAccountingBytes: summary.fullRouteCounts.retainedRawGroupAccountingBytes,
  }, {
    complete: true,
    patientObservationGroups: 25_001,
    rawTypedTextCodeGroupCount: 1,
    sourceRows: 25_001,
    matchedObservationRows: 25_001,
    numericCount: 25_001,
    numericSum: 50_002,
    numericMax: 2,
    retainedRawGroupAccountingBytes: retained.retainedRawGroupBytes,
  });
});

test('CASE-018 retained raw-group count and byte caps reject additions atomically', () => {
  const withActivePatient = options => {
    const accumulator = createRelatedQuantityPivotDiscoveryAccumulator(scope, {
      specimenPageSize: 2,
      maxSpecimenPatientRows: 10,
      maxPatientGroups: 10,
      ...options,
    });
    accumulator.addSpecimenPage({
      ...page1,
      specimens: [
        { specimenId: 'Specimen/root-a', specimenKey: 'root-a' },
        { specimenId: 'Specimen/root-b', specimenKey: 'root-b' },
      ],
      hasMore: false,
      nextAfterSpecimenKey: null,
    });
    accumulator.addSpecimenPatientPairs(decodeRelatedQuantityPivotSpecimenPatientPairs({
      wireVersion: 1,
      project: scope.project,
      generation: scope.dataset_generation,
      authScope,
      specimenIds: ['Specimen/root-a', 'Specimen/root-b'],
      rows: [['Specimen/root-a', 'Patient/patient-a'], ['Specimen/root-b', 'Patient/patient-b']],
      overflow: false,
      truncated: false,
    }));
    assert.deepEqual(accumulator.nextPatientBatch(1), ['Patient/patient-a']);
    return accumulator;
  };
  const compactBatch = (patientId, groupText) => decodeRelatedQuantityPivotPatientBatch({
    wireVersion: 1,
    project: scope.project,
    generation: scope.dataset_generation,
    authScope,
    patientIds: [patientId],
    groups: [patientGroupToWireTuple({ ...patientBatchC.groups[0], patientId, text: groupText })],
    overflow: false,
    truncated: false,
  });

  const countBounded = withActivePatient({ maxRetainedRawGroups: 1, maxRetainedRawGroupBytes: 1024 * 1024 });
  countBounded.addPatientBatch(compactBatch('Patient/patient-a', 'FINAL'));
  assert.deepEqual(countBounded.nextPatientBatch(1), ['Patient/patient-b']);
  assert.throws(() => countBounded.addPatientBatch(compactBatch('Patient/patient-b', 'OTHER')),
    /retained raw group count exceeds 1/i);
  assert.deepEqual({
    retainedRawGroupCount: countBounded.partialSnapshot().retainedRawGroupCount,
    patientObservationGroups: countBounded.partialSnapshot().patientObservationGroups,
    cursor: countBounded.partialSnapshot().patientBatchCursor,
    activeBatchSize: countBounded.partialSnapshot().activePatientBatchSize,
  }, { retainedRawGroupCount: 1, patientObservationGroups: 1, cursor: 1, activeBatchSize: 1 });

  const byteBounded = withActivePatient({ maxRetainedRawGroups: 10, maxRetainedRawGroupBytes: 1 });
  assert.throws(() => byteBounded.addPatientBatch(compactBatch('Patient/patient-a', 'FINAL')),
    /retained raw group byte accounting exceeds 1/i);
  assert.deepEqual({
    retainedRawGroupCount: byteBounded.partialSnapshot().retainedRawGroupCount,
    retainedRawGroupBytes: byteBounded.partialSnapshot().retainedRawGroupBytes,
    patientObservationGroups: byteBounded.partialSnapshot().patientObservationGroups,
    cursor: byteBounded.partialSnapshot().patientBatchCursor,
    activeBatchSize: byteBounded.partialSnapshot().activePatientBatchSize,
  }, { retainedRawGroupCount: 0, retainedRawGroupBytes: 0, patientObservationGroups: 0, cursor: 0, activeBatchSize: 1 });
});

test('CASE-018 rejects malformed pair and Patient-batch envelopes and rows', () => {
  const withFirstPage = () => {
    const accumulator = createRelatedQuantityPivotDiscoveryAccumulator(scope, {
      specimenPageSize: 2,
      maxSpecimenPatientRows: 10,
      maxPatientGroups: 10,
    });
    accumulator.addSpecimenPage(page1);
    return accumulator;
  };
  const pairEnvelope = withFirstPage();
  assert.throws(() => pairEnvelope.addSpecimenPatientPairs({
    ...pairs1,
    rows: { specimenId: 'Specimen/root-a', patientId: 'Patient/patient-a' },
  }), /malformed or unbounded/i);

  const pairRow = withFirstPage();
  assert.throws(() => pairRow.addSpecimenPatientPairs({
    ...pairs1,
    rows: [{ patientId: 'Patient/patient-a' }, { specimenId: 'Specimen/root-b', patientId: 'Patient/patient-a' }],
  }), /outside the current root page/i);

  const withActivePatientBatch = () => {
    const accumulator = createRelatedQuantityPivotDiscoveryAccumulator(scope, {
      specimenPageSize: 2,
      maxSpecimenPatientRows: 10,
      maxPatientGroups: 10,
    });
    addAllSpecimenPages(accumulator);
    assert.deepEqual(accumulator.nextPatientBatch(1), ['Patient/patient-a']);
    return accumulator;
  };
  const patientEnvelope = withActivePatientBatch();
  assert.throws(() => patientEnvelope.addPatientBatch({
    ...patientBatchA,
    groups: null,
  }), /malformed or unbounded/i);

  const patientRow = withActivePatientBatch();
  assert.throws(() => patientRow.addPatientBatch({
    ...patientBatchA,
    groups: [{ ...patientBatchA.groups[0], routeRows: 'one' }],
  }), /must be a non-negative safe integer/i);
});

test('CASE-018 incremental host rejects scope drift, duplicate work, truncation, and incomplete finalization', () => {
  const wrongProject = createRelatedQuantityPivotDiscoveryAccumulator(scope, { specimenPageSize: 2 });
  assert.throws(() => wrongProject.addSpecimenPage({ ...page1, project: 'other-project' }), /scope|project|identity/i);

  const wrongGeneration = createRelatedQuantityPivotDiscoveryAccumulator(scope, { specimenPageSize: 2 });
  assert.throws(() => wrongGeneration.addSpecimenPage({ ...page1, generation: 'other-generation' }), /scope|generation|identity/i);

  const wrongAuthScope = createRelatedQuantityPivotDiscoveryAccumulator(scope, { specimenPageSize: 2 });
  assert.throws(() => wrongAuthScope.addSpecimenPage({ ...page1, authScope: { ...authScope, scope_allowed: false } }), /scope|auth|identity/i);

  const incomplete = createRelatedQuantityPivotDiscoveryAccumulator(scope, { specimenPageSize: 2 });
  incomplete.addSpecimenPage(page1);
  assert.throws(() => incomplete.finalize(), /complete|page|pair|patient/i);

  const duplicatePairs = createRelatedQuantityPivotDiscoveryAccumulator(scope, { specimenPageSize: 2 });
  duplicatePairs.addSpecimenPage(page1);
  assert.throws(() => duplicatePairs.addSpecimenPatientPairs({
    ...pairs1,
    rows: [...pairs1.rows, { specimenId: 'Specimen/root-a', patientId: 'Patient/patient-a' }],
  }), /duplicate|unique|pair/i);

  const truncatedPairs = createRelatedQuantityPivotDiscoveryAccumulator(scope, { specimenPageSize: 2 });
  truncatedPairs.addSpecimenPage(page1);
  assert.throws(() => truncatedPairs.addSpecimenPatientPairs({ ...pairs1, overflow: true }), /overflow|truncat|complete/i);

  const truncatedPairRows = createRelatedQuantityPivotDiscoveryAccumulator(scope, { specimenPageSize: 2 });
  truncatedPairRows.addSpecimenPage(page1);
  assert.throws(() => truncatedPairRows.addSpecimenPatientPairs({ ...pairs1, truncated: true }), /overflow|truncat|complete/i);

  const duplicateGroups = createRelatedQuantityPivotDiscoveryAccumulator(scope, {
    specimenPageSize: 2,
    maxSpecimenPatientRows: 10,
    maxPatientGroups: 10,
  });
  addAllSpecimenPages(duplicateGroups);
  assert.deepEqual(duplicateGroups.nextPatientBatch(1), ['Patient/patient-a']);
  assert.throws(() => duplicateGroups.addPatientBatch({
    ...patientBatchA,
    groups: [...patientBatchA.groups, patientBatchA.groups[0]],
  }), /duplicate|unique|typed groups/i);

  const truncatedBatch = createRelatedQuantityPivotDiscoveryAccumulator(scope, {
    specimenPageSize: 2,
    maxSpecimenPatientRows: 10,
    maxPatientGroups: 10,
  });
  addAllSpecimenPages(truncatedBatch);
  assert.deepEqual(truncatedBatch.nextPatientBatch(1), ['Patient/patient-a']);
  assert.throws(() => truncatedBatch.addPatientBatch({ ...patientBatchA, truncated: true }), /overflow|truncat|complete/i);

  const overflowBatch = createRelatedQuantityPivotDiscoveryAccumulator(scope, {
    specimenPageSize: 2,
    maxSpecimenPatientRows: 10,
    maxPatientGroups: 10,
  });
  addAllSpecimenPages(overflowBatch);
  assert.deepEqual(overflowBatch.nextPatientBatch(1), ['Patient/patient-a']);
  assert.throws(() => overflowBatch.addPatientBatch({ ...patientBatchA, overflow: true }), /overflow|truncat|complete/i);

  const independentlyBoundedGroups = createRelatedQuantityPivotDiscoveryAccumulator(scope, {
    specimenPageSize: 2,
    maxSpecimenPatientRows: 10,
    maxPatientGroups: 4,
  });
  addAllSpecimenPages(independentlyBoundedGroups);
  assert.deepEqual(independentlyBoundedGroups.nextPatientBatch(1), ['Patient/patient-a']);
  independentlyBoundedGroups.addPatientBatch(patientBatchA);
  assert.deepEqual(independentlyBoundedGroups.nextPatientBatch(1), ['Patient/patient-b']);
  independentlyBoundedGroups.addPatientBatch(patientBatchB);
  assert.equal(independentlyBoundedGroups.partialSnapshot().patientObservationGroups, 5,
    'the 4-group per-batch limit does not cap cumulative work across bounded batches');

  const unexpectedPatient = createRelatedQuantityPivotDiscoveryAccumulator(scope, {
    specimenPageSize: 2,
    maxSpecimenPatientRows: 10,
    maxPatientGroups: 10,
  });
  addAllSpecimenPages(unexpectedPatient);
  assert.deepEqual(unexpectedPatient.nextPatientBatch(1), ['Patient/patient-a']);
  assert.throws(() => unexpectedPatient.addPatientBatch({
    ...patientBatchA,
    groups: patientBatchA.groups.map(group => ({ ...group, patientId: 'Patient/unrequested' })),
  }), /outside|requested patient/i);

  const omittedPatientGroup = createRelatedQuantityPivotDiscoveryAccumulator(scope, {
    specimenPageSize: 2,
    maxSpecimenPatientRows: 10,
    maxPatientGroups: 10,
  });
  addAllSpecimenPages(omittedPatientGroup);
  assert.deepEqual(omittedPatientGroup.nextPatientBatch(2), ['Patient/patient-a', 'Patient/patient-b']);
  assert.throws(() => omittedPatientGroup.addPatientBatch({
    ...patientBatchA,
    patientIds: ['Patient/patient-a', 'Patient/patient-b'],
  }), /missing an Observation group|PRESERVE_PARENT sentinel/i);

  const stalledContinuation = createRelatedQuantityPivotDiscoveryAccumulator(scope, { specimenPageSize: 2 });
  assert.throws(() => stalledContinuation.addSpecimenPage({ ...page1, nextAfterSpecimenKey: 'root-a' }), /cursor|stalled|inconsistent/i);

  const missingContinuation = createRelatedQuantityPivotDiscoveryAccumulator(scope, { specimenPageSize: 2 });
  missingContinuation.addSpecimenPage(page1);
  missingContinuation.addSpecimenPatientPairs(pairs1);
  assert.throws(() => missingContinuation.finalize(), /complete|page|pair|patient/i);

  const outOfOrderPage = createRelatedQuantityPivotDiscoveryAccumulator(scope, {
    specimenPageSize: 2,
    maxSpecimenPatientRows: 10,
    maxPatientGroups: 10,
  });
  outOfOrderPage.addSpecimenPage(page1);
  outOfOrderPage.addSpecimenPatientPairs(pairs1);
  assert.throws(() => outOfOrderPage.addSpecimenPage({ ...page2, afterSpecimenKey: 'root-a' }), /cursor|after|order|page/i);
});

test('CASE-018 rejects malformed, oversized, duplicate, and cursor-inconsistent keyset pages', () => {
  const emptyResultStream = createRelatedQuantityPivotDiscoveryAccumulator(scope, { specimenPageSize: 2 });
  assert.throws(() => emptyResultStream.addSpecimenPage([]), /must return one result object/i);

  const multipleResultStream = createRelatedQuantityPivotDiscoveryAccumulator(scope, { specimenPageSize: 2 });
  assert.throws(() => multipleResultStream.addSpecimenPage([page1, page1]), /must return one result object/i);

  const malformedEnvelope = createRelatedQuantityPivotDiscoveryAccumulator(scope, { specimenPageSize: 2 });
  assert.throws(() => malformedEnvelope.addSpecimenPage({ ...page1, specimens: 'not-an-array' }), /malformed|limit/i);

  for (const flag of ['overflow', 'truncated']) {
    const overflowedPage = createRelatedQuantityPivotDiscoveryAccumulator(scope, { specimenPageSize: 2 });
    assert.throws(() => overflowedPage.addSpecimenPage({ ...page1, [flag]: true }), /overflow|truncation/i,
      `a specimen page marked ${flag} must be fatal`);
  }

  const oversized = createRelatedQuantityPivotDiscoveryAccumulator(scope, { specimenPageSize: 2 });
  assert.throws(() => oversized.addSpecimenPage({
    ...page1,
    specimens: [...page1.specimens, { specimenId: 'Specimen/root-c', specimenKey: 'root-c' }],
    nextAfterSpecimenKey: 'root-c',
  }), /malformed|limit/i);

  const duplicateKey = createRelatedQuantityPivotDiscoveryAccumulator(scope, { specimenPageSize: 2 });
  assert.throws(() => duplicateKey.addSpecimenPage({
    ...page1,
    specimens: [page1.specimens[0], { ...page1.specimens[0] }],
    nextAfterSpecimenKey: 'root-a',
  }), /duplicate|stalled|inconsistent/i);

  const terminalCursor = createRelatedQuantityPivotDiscoveryAccumulator(scope, { specimenPageSize: 2 });
  assert.throws(() => terminalCursor.addSpecimenPage({
    ...page1,
    specimens: [page1.specimens[0]],
    hasMore: false,
    nextAfterSpecimenKey: 'root-a',
  }), /final specimen page must not advertise another cursor/i);

  const missingCursor = createRelatedQuantityPivotDiscoveryAccumulator(scope, { specimenPageSize: 2 });
  assert.throws(() => missingCursor.addSpecimenPage({ ...page1, nextAfterSpecimenKey: null }), /continuation cursor is missing/i);

  const emptyContinuation = createRelatedQuantityPivotDiscoveryAccumulator(scope, { specimenPageSize: 2 });
  emptyContinuation.addSpecimenPage(page1);
  emptyContinuation.addSpecimenPatientPairs(pairs1);
  assert.throws(() => emptyContinuation.addSpecimenPage({
    ...page2,
    specimens: [],
    hasMore: false,
    nextAfterSpecimenKey: null,
  }), /continuation page is empty/i);
});

test('CASE-018 rejects missing or mixed PRESERVE_PARENT sentinels at both route hops', () => {
  const missingFirstHop = createRelatedQuantityPivotDiscoveryAccumulator(scope, { specimenPageSize: 2 });
  missingFirstHop.addSpecimenPage(page1);
  assert.throws(() => missingFirstHop.addSpecimenPatientPairs({
    ...pairs1,
    rows: pairs1.rows.filter(row => row.specimenId !== 'Specimen/root-b'),
  }), /missing.*PRESERVE_PARENT sentinel/i);

  const mixedFirstHop = createRelatedQuantityPivotDiscoveryAccumulator(scope, { specimenPageSize: 2 });
  mixedFirstHop.addSpecimenPage(page1);
  assert.throws(() => mixedFirstHop.addSpecimenPatientPairs({
    ...pairs1,
    rows: [
      { specimenId: 'Specimen/root-a', patientId: 'Patient/patient-a' },
      { specimenId: 'Specimen/root-a', patientId: null },
      { specimenId: 'Specimen/root-b', patientId: 'Patient/patient-a' },
    ],
  }), /sentinel cannot coexist/i);

  const mixedSecondHop = createRelatedQuantityPivotDiscoveryAccumulator(scope, {
    specimenPageSize: 2,
    maxSpecimenPatientRows: 10,
    maxPatientGroups: 10,
  });
  addAllSpecimenPages(mixedSecondHop);
  const requestedPatients = mixedSecondHop.nextPatientBatch(2);
  assert.deepEqual(requestedPatients, ['Patient/patient-a', 'Patient/patient-b']);
  assert.throws(() => mixedSecondHop.addPatientBatch({
    ...patientBatchA,
    patientIds: requestedPatients,
    groups: [
      ...patientBatchA.groups,
      ...patientBatchB.groups,
      { ...patientBatchC.groups[0], patientId: 'Patient/patient-b' },
    ],
  }), /empty-second-hop sentinel cannot coexist with Observation groups/i);

  const malformedSecondHop = createRelatedQuantityPivotDiscoveryAccumulator(scope, {
    specimenPageSize: 2,
    maxSpecimenPatientRows: 10,
    maxPatientGroups: 10,
  });
  addAllSpecimenPages(malformedSecondHop);
  assert.deepEqual(malformedSecondHop.nextPatientBatch(1), ['Patient/patient-a']);
  assert.throws(() => malformedSecondHop.addPatientBatch({
    ...patientBatchA,
    groups: [{ ...patientBatchA.groups[0], secondHopMissing: true, observationPresent: false }],
  }), /route counts do not match its second-hop presence/i);
});
