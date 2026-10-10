import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import {
  buildCdaGroupNumericFilterInitialQuery,
  buildCdaGroupNumericFilterExactReadQuery,
  buildCdaGroupNumericFilterTargetedQuery,
  authoredColumnIds,
  hasExactAuthoredColumnRestoration,
  prepareCdaGroupNumericFilterOracle,
  validateCdaGroupCandidate,
} from '../cda-group-numeric-filter-oracle.mjs';
import { chooseCdaGroupPivotJoinWitness } from '../cda-group-pivot-join-oracle.mjs';
import { getScenario, scenarioCaseFor } from '../../registry.mjs';

const project = 'loom_dev_cda_fhir';
const generation = 'cda-fhir-v1';
const rawRows = [
  { _id: 'Observation/a', id: 'a', project, generation, resourceType: 'Observation', subjectReference: 'Patient/shared', status: 'final', codeCodingCodes: ['A'] },
  { _id: 'Observation/b', id: 'b', project, generation, resourceType: 'Observation', subjectReference: 'Patient/shared', status: 'final', codeCodingCodes: ['A'] },
  { _id: 'Observation/c', id: 'c', project, generation, resourceType: 'Observation', subjectReference: 'Patient/shared', status: 'final', codeCodingCodes: ['B'] },
  { _id: 'Observation/d', id: 'd', project, generation, resourceType: 'Observation', subjectReference: 'Patient/left', status: 'final', codeCodingCodes: ['C'] },
];

test('validates a retained Group candidate against the V2 source columnId DTO', () => {
  const fixture = JSON.parse(readFileSync(new URL('./fixtures/cda-group-numeric-filter-retained-candidate.json', import.meta.url), 'utf8'));
  assert.equal(fixture.sourceSubjectColumn.source.field.path, 'subject.reference');
  assert.deepEqual(validateCdaGroupCandidate({
    candidateConstruction: fixture.candidateConstruction,
    sourceSubjectColumn: fixture.sourceSubjectColumn,
  }), {
    stepId: 'group_586b9249-cf53-4385-be8a-9cf842a37133',
    keyInputColumnId: 'source_50da1012a2b5d01596d07a6d',
    aggregate: { operation: 'COUNT_ROWS', outputColumnId: 'group-column_2fdd0358-774c-414b-bcdd-d9cf430127ee' },
  });
});

test('numeric COUNT_ROWS Filter derives exact 3/1 groups and a strict GT 1 subset from raw identities', () => {
  const witness = chooseCdaGroupPivotJoinWitness(rawRows, { project, generation });
  const oracle = prepareCdaGroupNumericFilterOracle(witness, { project, generation });
  assert.deepEqual(oracle.groupRows, [['Patient/left', '1'], ['Patient/shared', '3']]);
  assert.deepEqual(oracle.filteredRows, [['Patient/shared', '3']]);
  assert.deepEqual(prepareCdaGroupNumericFilterOracle(witness, { project, generation, threshold: 3, requireStrictSubset: false }).filteredRows, []);
  assert.deepEqual(prepareCdaGroupNumericFilterOracle(witness, { project, generation, threshold: 2 }).filteredRows, [['Patient/shared', '3']]);
});

test('numeric COUNT_ROWS Filter oracle rejects invalid scope, threshold, and non-strict witness', () => {
  const witness = chooseCdaGroupPivotJoinWitness(rawRows, { project, generation });
  assert.throws(() => prepareCdaGroupNumericFilterOracle(witness, { project: 'other-project', generation }), /project/);
  assert.throws(() => prepareCdaGroupNumericFilterOracle(witness, { project, generation: 'other-generation' }), /generation/);
  assert.throws(() => prepareCdaGroupNumericFilterOracle(witness, { project, generation, threshold: 1.5 }), /safe integer/);
  assert.throws(() => prepareCdaGroupNumericFilterOracle(witness, { project, generation, threshold: 0 }), /strict subset/);
  assert.throws(() => prepareCdaGroupNumericFilterOracle(witness, { project, generation, operator: 'EQUALS' }), /Unsupported/);
});

test('numeric Filter reuses the terminal exact Group-stage capability captured before the editor opens', async () => {
  const { waitForSourceCapabilities } = await import('../../workflows/verify-cda-authored-expand-browser.mjs');
  const snapshotToken = 'sha256:7e9a025ec2555033fd138a3cbf9eaecebb24f870a6b37cf5e86e0355c84b40f7';
  const outputId = 'out_7ee0ed073a29895cc948ee64';
  const stageId = 'group_eed1dd26-e2ab-4d64-aa57-b5e41d34a363';
  const draftDigest = 'sha256:e40aabee58278a4593dc2d2e1daa2a39b5985d623a205558f3bd30f9f2724139';
  const draftVersion = 5;
  const path = `/api/v1/projects/${project}/explorers/cda-group-numeric-filter-fb2c4cc9-9e6a-4c98-b102-771db2af3639/authoring/v2/construction-capabilities`;
  const makeEntry = (requestId, identity) => ({
    requestId, method: 'POST', path, status: 200, completedAt: Date.now(),
    body: {
      snapshotToken: identity.snapshotToken,
      expectedDraftVersion: identity.draftVersion,
      expectedDraftDigest: identity.draftDigest,
      outputId: identity.outputId,
      stageId: identity.stageId,
    },
    response: {
      snapshotToken: identity.snapshotToken,
      draftVersion: identity.draftVersion,
      draftDigest: identity.draftDigest,
      outputId: identity.outputId,
      stageId: identity.stageId,
      selectedStage: {
        id: identity.stageId,
        capabilities: [{ kind: 'FILTER', supported: true }],
        columns: [{ id: 'group-column_e51cba78-5106-4510-b5c9-9d6518289494', name: 'row_count', label: 'Row count', type: 'integer' }],
      },
    },
  });
  const exact = makeEntry('playwright-42', { snapshotToken, outputId, stageId, draftVersion, draftDigest });
  const stale = makeEntry('playwright-41', {
    snapshotToken, outputId, stageId, draftVersion: 4,
    draftDigest: 'sha256:06dfafc9f9f0694545a836071ff0359ba535026ab25bc640b83ca9371f6c6858',
  });
  const entries = [stale, exact];
  let observedFromIndex;
  const capture = {
    waitFor(predicate, { fromIndex, timeoutMs }) {
      observedFromIndex = fromIndex;
      assert(timeoutMs > 0);
      const match = entries.slice(fromIndex).find(entry => Number.isFinite(entry.completedAt) && predicate(entry));
      return match ? Promise.resolve(match) : Promise.reject(new Error('no exact completed cached capability'));
    },
    rawRequestBody: entry => entry.body,
    rawResponseBody: entry => entry.response,
  };
  const matched = await waitForSourceCapabilities(capture, {
    fromIndex: 0, deadlineAt: Date.now() + 5_000, path,
    expected: { snapshotToken, outputId, stageId, draftVersion, draftDigest },
  });
  assert.equal(observedFromIndex, 0, 'Filter must search the complete captured request history for the exact saved identity.');
  assert.equal(matched, exact, 'A prior terminal capability with an older draft must not shadow the exact Group response.');
  assert.deepEqual(matched.response.selectedStage.capabilities, [{ kind: 'FILTER', supported: true }]);
  assert.deepEqual(matched.response.selectedStage.columns, [
    { id: 'group-column_e51cba78-5106-4510-b5c9-9d6518289494', name: 'row_count', label: 'Row count', type: 'integer' },
  ]);
});

test('numeric Group Filter raw queries stay project/generation scoped and cap sample and targeted rows', () => {
  const initialQuery = buildCdaGroupNumericFilterInitialQuery({ project, generation });
  const targetedQuery = buildCdaGroupNumericFilterTargetedQuery({
    project, generation, selectedSubjects: ['Patient/shared', 'Patient/left'],
  });
  const exactReadQuery = buildCdaGroupNumericFilterExactReadQuery({ project, generation, exactIds: rawRows.map(row => row._id) });
  assert.match(initialQuery, /r\.project == "loom_dev_cda_fhir"/);
  assert.match(initialQuery, /r\.dataset_generation == "cda-fhir-v1"/);
  assert.match(initialQuery, /SORT r\._id LIMIT 2000/);
  for (const query of [targetedQuery, exactReadQuery]) {
    assert.match(query, /r\.project == "loom_dev_cda_fhir"/);
    assert.match(query, /r\.dataset_generation == "cda-fhir-v1"/);
  }
  assert.match(targetedQuery, /r\.payload\.subject\.reference IN \["Patient\/shared","Patient\/left"\]/);
  assert.match(targetedQuery, /SORT r\._id LIMIT 2000/);
  assert.match(exactReadQuery, /r\._id IN \["Observation\/a","Observation\/b","Observation\/c","Observation\/d"\]/);
  assert.throws(() => buildCdaGroupNumericFilterTargetedQuery({ project, generation, selectedSubjects: [] }), /selected subjects/);
  assert.throws(() => buildCdaGroupNumericFilterExactReadQuery({ project, generation, exactIds: rawRows.slice(0, 3).map(row => row._id) }), /four distinct/);
});

test('numeric Group Filter lifecycle acceptance maps to exact named checks', () => {
  const scenario = getScenario('cda-group-numeric-filter');
  const coverage = scenario.coverage.find(entry => entry.feature.startsWith('CDA numeric Group COUNT_ROWS'));
  assert.ok(coverage, 'numeric Group Filter lifecycle coverage is registered');
  const contract = scenarioCaseFor(scenario, coverage.acceptance.case);
  assert.equal(contract.requiredChecks.length, 19);
  const namedChecks = Object.fromEntries(Object.entries(coverage.acceptance.checks)
    .map(([phase, index]) => [phase, contract.requiredChecks[index]]));
  assert.deepEqual(namedChecks, {
    choice: 'Native Filter selects the Group integer COUNT_ROWS output',
    proposal: 'Numeric GT 1 proposal returns exactly the count-3 Group row',
    cancel: 'Filter Cancel preserves the exact Group construction and 3/1 rows',
    apply: 'Applied numeric GT 1 Filter retains exactly the count-3 Group row',
    savedRows: 'Filtered Group row survives reload with exact count and stable bindings',
    reload: 'Filtered Group row survives reload with exact count and stable bindings',
    edit: 'Saved Filter edit preserves Group STEP_OUTPUT and integer COUNT_ROWS column identity',
    restoration: 'Group removal restores exact source construction, columns, and four raw rows after reload',
  });
});

test('Group removal requires exact ordered V2 authored columnId and source-binding restoration', () => {
  const sourceColumns = [
    { columnId: 'source-observation-id', column: 'col-observation-id', occurrenceId: 'base', logicalType: 'string',
      source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } }, table: { visible: true, order: 0 } },
    { columnId: 'source-subject-reference', column: 'col-subject-reference', occurrenceId: 'base', logicalType: 'string',
      source: { kind: 'field', field: { path: 'subject.reference', projectionMode: 'VALUE' } }, table: { visible: true, order: 1 } },
  ];
  assert.deepEqual(authoredColumnIds(sourceColumns), ['source-observation-id', 'source-subject-reference']);
  assert.equal(hasExactAuthoredColumnRestoration(sourceColumns, structuredClone(sourceColumns)), true);
  assert.equal(hasExactAuthoredColumnRestoration(sourceColumns, [...sourceColumns].reverse()), false,
    'restoration must preserve source column order');
  assert.equal(hasExactAuthoredColumnRestoration(sourceColumns, sourceColumns.map((column, index) => index === 1
    ? { ...column, source: { kind: 'field', field: { path: 'status', projectionMode: 'VALUE' } } }
    : column)), false, 'restoration must preserve each source binding');
  assert.equal(authoredColumnIds(sourceColumns.map(({ columnId, ...column }) => ({ ...column, id: columnId }))), undefined,
    'legacy id does not stand in for the V2 authored columnId');
  assert.equal(hasExactAuthoredColumnRestoration(sourceColumns, sourceColumns.map(({ columnId, ...column }) => column)), false,
    'missing restored authored IDs must fail rather than compare undefined values');
  assert.equal(authoredColumnIds([sourceColumns[0], { ...sourceColumns[1], columnId: sourceColumns[0].columnId }]), undefined,
    'duplicate authored IDs cannot prove exact restoration');
});
