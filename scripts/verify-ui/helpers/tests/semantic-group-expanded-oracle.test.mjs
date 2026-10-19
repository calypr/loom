import assert from 'node:assert/strict';
import test from 'node:test';
import { registry } from '../../registry.mjs';
import {
  assertExactRows,
  protectNativeRequestBodies,
  stableDigest,
  summarizeOracle,
} from '../semantic-group-expanded-report.mjs';
import {
  buildSemanticGroupExpandedOracle,
  expectedIdentityContributorRows,
  expectedIdentityGroupRows,
  expectedSubjectGroupRows,
  sortPreviewRows,
} from '../semantic-group-expanded-oracle.mjs';

const project = 'loom_dev_cda_fhir';
const generation = 'cda-fhir-v1';

const rows = [
  {
    _key: 'observation-a', id: 'observation-a', project, generation, resourceType: 'Observation',
    subjectReference: 'Patient/patient-a',
    codings: [
      { ordinal: 0, system: 'urn:example:stage', version: null, code: 'stage-one', display: 'Stage I' },
    ],
  },
  {
    _key: 'observation-b', id: 'observation-b', project, generation, resourceType: 'Observation',
    subjectReference: 'Patient/patient-b',
    codings: [
      { ordinal: 0, system: 'urn:example:stage', version: null, code: 'stage-one', display: 'Stage I' },
      { ordinal: 1, system: 'urn:example:stage', version: null, code: 'stage-one', display: 'Stage I' },
      { ordinal: 2, system: 'urn:example:stage', version: '2025', code: 'stage-two', display: 'Stage II' },
    ],
  },
];

test('expanded semantic Group oracle keeps exact Coding identity, source labels, and owner contributions', () => {
  const oracle = buildSemanticGroupExpandedOracle(rows, { project, generation });

  assert.equal(oracle.sourceResourceType, 'Observation');
  assert.equal(oracle.sourcePath, 'valueCodeableConcept.coding[]');
  assert.equal(oracle.occurrenceId, 'base');
  assert.deepEqual(oracle.sourceIDs, ['observation-a', 'observation-b']);
  assert.equal(oracle.expectedExpandedRowCount, 4);
  assert.deepEqual(oracle.familyTuples, [
    { system: 'urn:example:stage', version: '2025', code: 'stage-two' },
    { system: 'urn:example:stage', version: null, code: 'stage-one' },
  ]);
  assert.deepEqual(oracle.displayLabels, ['Stage I', 'Stage II']);
  assert.deepEqual(sortPreviewRows(expectedSubjectGroupRows(oracle)), sortPreviewRows([
    ['Patient/patient-a', 'Stage I', '1'],
    ['Patient/patient-b', 'Stage I', '2'],
    ['Patient/patient-b', 'Stage II', '1'],
  ]));
  assert.deepEqual(sortPreviewRows(expectedIdentityGroupRows(oracle)), sortPreviewRows([
    ['Stage I', '3'],
    ['Stage II', '1'],
  ]));
  assert.deepEqual(sortPreviewRows(expectedIdentityContributorRows(oracle)), sortPreviewRows([
    ['Stage I', '3', ['observation-a', 'observation-b']],
    ['Stage II', '1', ['observation-b']],
  ]));
  assert.deepEqual(sortPreviewRows(oracle.expectedRawExpandedRows), sortPreviewRows([
    ['observation-a'], ['observation-b'], ['observation-b'], ['observation-b'],
  ]));
  const summary = summarizeOracle(oracle);
  assert.equal(summary.selectedRootCount, 2);
  assert.equal(summary.expandedOccurrenceCount, 4);
  assert.equal(summary.codingIdentityCount, 2);
  assert.equal(summary.subjectCodingGroupCount, 3);
  assert.equal(summary.digest, stableDigest(oracle.occurrenceRows));
  for (const privateValue of ['observation-a', 'Patient/patient-a', 'stage-one', 'urn:example:stage']) {
    assert(!JSON.stringify(summary).includes(privateValue));
  }
});

test('expanded semantic Group oracle refuses a source row from another project or generation', () => {
  assert.throws(() => buildSemanticGroupExpandedOracle([
    ...rows,
    { ...rows[0], _key: 'foreign', id: 'foreign', project: 'another-project' },
  ], { project, generation }), /project/);
  assert.throws(() => buildSemanticGroupExpandedOracle([
    ...rows,
    { ...rows[0], _key: 'old', id: 'old', generation: 'older-generation' },
  ], { project, generation }), /generation/);
  assert.throws(() => buildSemanticGroupExpandedOracle(rows.map(row => ({
    ...row,
    codings: row.codings.map(coding => ({ ...coding, display: null })),
  })), { project, generation }), /useful display label/);
});

test('oracle preserves exact string systems and raw nonblank display while blanking only version', () => {
  const sourceRows = rows.map(row => ({
    ...row,
    codings: [...row.codings, {
      ordinal: row.codings.length,
      system: '',
      version: '  ',
      code: 'blank-system-code',
      display: '  Raw  display  ',
    }],
  }));
  const oracle = buildSemanticGroupExpandedOracle(sourceRows, { project, generation });
  const exactTuple = oracle.familyTuples.find(tuple => tuple.code === 'blank-system-code');

  assert.deepEqual(exactTuple, { system: '', version: null, code: 'blank-system-code' });
  assert(oracle.occurrenceRows.every(row => row.identity.code !== 'blank-system-code' ||
    (row.identity.system === '' && row.identity.version === null && row.display === '  Raw  display  ')));
  assert(oracle.expectedSubjectGroups.some(group => group.display === '  Raw  display  '));
});

test('expanded Coding Group lifecycle is registered on the owned CDA scenario', () => {
  const scenario = registry.find(entry => entry.id === 'cda-workspace-combine');
  const registeredCase = scenario?.cases?.['semantic-group-expanded'];

  assert.equal(registeredCase?.playwrightTest,
    'scripts/verify-ui/specs/semantic-group-expanded-lifecycle.spec.mjs');
  assert.deepEqual(registeredCase?.expectedIdentity, { project, generation });
  assert(registeredCase?.requiredChecks.includes(
    'native Group selects CODING_IDENTITY for valueCodeableConcept.coding[] and the exact scalar key before public columns'));
  assert(registeredCase?.requiredChecks.includes(
    'Add columns rehydrates exact Observation owner IDs from pre-Group Coding occurrences with matching counts'));
  assert(registeredCase?.requiredChecks.includes(
    'native expanded Coding lifecycle preserves exact owner route and never selects Observation.code.coding[]'));
  assert.deepEqual(registeredCase?.focusedChecks?.[0]?.command, [
    'node-test', 'scripts/verify-ui/helpers/tests/semantic-group-expanded-oracle.test.mjs',
  ]);
});

test('expanded Group reports retain only digests of native request and response bodies', () => {
  const requestBody = { sourceIDs: ['private-observation-id'], subjectReference: 'Patient/private-subject' };
  const responseBody = { preview: { rows: [['private-code', 'Private display']] } };
  const report = { nativeRequests: [{ request: requestBody, response: responseBody }] };
  protectNativeRequestBodies(report);
  const futureEntry = {};
  report.nativeRequests.push(futureEntry);
  futureEntry.request = requestBody;
  futureEntry.response = responseBody;

  const serializedReport = JSON.stringify(report);
  for (const privateValue of ['private-observation-id', 'Patient/private-subject', 'private-code', 'Private display']) {
    assert(!serializedReport.includes(privateValue));
  }
  assert.deepEqual(report.nativeRequests[0].request, { redacted: true, sha256: stableDigest(requestBody) });
  assert.deepEqual(report.nativeRequests[1].response, { redacted: true, sha256: stableDigest(responseBody) });
  assert.throws(() => assertExactRows([['private-observation-id']], [], 'safe mismatch'), error =>
    error instanceof Error && error.message.includes('actualSha256') && !error.message.includes('private-observation-id'));
});
