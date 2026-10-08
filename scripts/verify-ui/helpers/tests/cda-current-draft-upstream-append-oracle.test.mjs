import assert from 'node:assert/strict';
import test from 'node:test';
import {
  CDA_UPSTREAM_APPEND_RESOURCES,
  CDA_UPSTREAM_APPEND_SOURCES,
  CdaUpstreamAppendWitnessUnavailable,
  MAX_CDA_UPSTREAM_APPEND_SCAN,
  assertCdaUpstreamAppendReread,
  cdaUpstreamAppendRereadQuery,
  cdaUpstreamAppendScanQuery,
  prepareCdaUpstreamAppendOracle,
  prepareCdaUpstreamAppendPatientSubsetOracle,
  proveObservedSupersededEmptyGroupProposals,
  proveSupersededEmptyGroupProposal,
} from '../cda-current-draft-upstream-append-oracle.mjs';

const project = 'loom-cda-oracle';
const generation = 'cda-fhir-v1';
const row = (resourceType, _id, id, fieldPresent, fieldValue) => ({
  _id: `${resourceType}/${_id}`,
  id,
  project,
  generation,
  resourceType,
  fieldPresent,
  fieldValue,
});
const scans = () => ({
  Patient: [
    row('Patient', 'patient-doc-02', 'patient-b', true, 'patient-b'),
    row('Patient', 'patient-doc-01', 'patient-a', true, 'patient-a'),
  ],
  Observation: [
    row('Observation', 'obs-doc-04', 'obs-final-4', true, 'final'),
    row('Observation', 'obs-doc-02', 'obs-final-2', true, 'final'),
    row('Observation', 'obs-doc-01', 'obs-final-1', true, 'final'),
    row('Observation', 'obs-doc-03', 'obs-final-3', true, 'final'),
    row('Observation', 'obs-doc-05', 'obs-preliminary', true, 'preliminary'),
  ],
});

test('bounded scans cover exact Patient.id and Observation.status fields with FHIR and Arango identities separate', () => {
  assert.deepEqual(CDA_UPSTREAM_APPEND_RESOURCES.map(({ resourceType, fieldPath }) => [resourceType, fieldPath]), [
    ['Patient', 'id'], ['Observation', 'status'],
  ]);
  assert.deepEqual(CDA_UPSTREAM_APPEND_SOURCES.map(({ sourceKey, resourceType }) => [sourceKey, resourceType]), [
    ['observation-left', 'Observation'], ['observation-right', 'Observation'], ['patient-id', 'Patient'],
  ]);
  for (const resource of CDA_UPSTREAM_APPEND_RESOURCES) {
    const query = cdaUpstreamAppendScanQuery({ ...resource, project, generation });
    assert.ok(query.includes(`r.project == ${JSON.stringify(project)}`));
    assert.match(query, /r\.dataset_generation ==/);
    assert.match(query, /r\.payload\.resourceType ==/);
    assert.match(query, /SORT r\._id LIMIT 2000/);
    assert.match(query, /fieldPresent:HAS\(r\.payload/);
  }
  const exact = cdaUpstreamAppendRereadQuery({
    resourceType: 'Patient', fieldPath: 'id', project, generation,
    documentIDs: ['Patient/patient-doc-01', 'Patient/patient-doc-02'],
  });
  assert.match(exact, /r\._id IN \["Patient\/patient-doc-01","Patient\/patient-doc-02"\]/);
  assert.match(exact, /r\.id,/);
  assert.notEqual('Patient/patient-doc-01', 'patient-a');
  assert.equal(MAX_CDA_UPSTREAM_APPEND_SCAN, 2_000);
});

test('oracle derives two disjoint Observation pairs, Patient ID counts, and duplicate-preserving APPEND rows', () => {
  const oracle = prepareCdaUpstreamAppendOracle(scans(), { project, generation });
  const left = oracle.sources['observation-left'];
  const right = oracle.sources['observation-right'];
  const patient = oracle.sources['patient-id'];
  assert.deepEqual(left.map(item => item._id), ['Observation/obs-doc-01', 'Observation/obs-doc-02']);
  assert.deepEqual(right.map(item => item._id), ['Observation/obs-doc-03', 'Observation/obs-doc-04']);
  assert.equal(new Set([...left, ...right].map(item => item._id)).size, 4);
  assert(left.every(item => item.resourceType === 'Observation' && item.fieldValue === 'final'));
  assert.deepEqual(patient.map(item => item.id), ['patient-a', 'patient-b']);
  assert(patient.every(item => item.id !== item._id && item.key === item.id));
  assert.deepEqual(oracle.grouped['observation-left'], [['final', 2]]);
  assert.deepEqual(oracle.grouped['observation-right'], [['final', 2]]);
  assert.deepEqual(oracle.grouped['patient-id'], [['patient-a', 1], ['patient-b', 1]]);
  assert.deepEqual(oracle.patientDerived.plusOne, [['patient-a', 1, 2], ['patient-b', 1, 2]]);
  assert.deepEqual(oracle.patientDerived.plusTwo, [['patient-a', 1, 3], ['patient-b', 1, 3]]);
  assert.deepEqual(oracle.append.plusOne, [
    ['final', '2'], ['final', '2'], ['patient-a', '2'], ['patient-b', '2'],
  ]);
  assert.deepEqual(oracle.append.plusTwo, [
    ['final', '2'], ['final', '2'], ['patient-a', '3'], ['patient-b', '3'],
  ]);
  assert.equal(oracle.append.duplicateStatusCount, 2);
  assert.equal(oracle.resources.find(item => item.resourceType === 'Patient').eligibleRows, 2);
  assert.equal(oracle.resources.find(item => item.resourceType === 'Observation').selectedFinalStatusRows, 4);
  assert.equal(oracle.selected['observation-left'].length, 2);
  assert.equal(oracle.selected['observation-right'].length, 2);
  assert.equal(oracle.selected['patient-id'].length, 2);
  assertCdaUpstreamAppendReread(
    [scans().Patient[1], scans().Patient[0]], oracle.exactExpected['patient-id'],
    { project, generation, resourceType: 'Patient' },
  );
});

test('current-draft Patient subset narrows GROUP→DERIVE→APPEND from four rows to three and restores four', () => {
  const oracle = prepareCdaUpstreamAppendOracle(scans(), { project, generation });
  const patientA = oracle.sources['patient-id'][0];
  const subset = prepareCdaUpstreamAppendPatientSubsetOracle(oracle, [patientA]);

  const expectedPatientA = {
    project: 'loom-cda-oracle',
    generation: 'cda-fhir-v1',
    resourceType: 'Patient',
    id: 'patient-a',
    _id: 'Patient/patient-doc-01',
    fieldPresent: true,
    fieldValue: 'patient-a',
    key: 'patient-a',
  };
  const expectedPatientB = {
    project: 'loom-cda-oracle',
    generation: 'cda-fhir-v1',
    resourceType: 'Patient',
    id: 'patient-b',
    _id: 'Patient/patient-doc-02',
    fieldPresent: true,
    fieldValue: 'patient-b',
    key: 'patient-b',
  };
  const expectedFullAppend = [
    ['final', '2'],
    ['final', '2'],
    ['patient-a', '2'],
    ['patient-b', '2'],
  ];

  assert.deepEqual(subset.startingCollection, {
    before: [expectedPatientA, expectedPatientB],
    selected: expectedPatientA,
    afterNarrowing: [expectedPatientA],
    afterRestoration: [expectedPatientA, expectedPatientB],
  });
  assert.deepEqual(subset.grouped, {
    before: [['patient-a', 1], ['patient-b', 1]],
    afterNarrowing: [['patient-a', 1]],
    afterRestoration: [['patient-a', 1], ['patient-b', 1]],
  });
  assert.deepEqual(subset.derived, {
    before: [['patient-a', 1, 2], ['patient-b', 1, 2]],
    afterNarrowing: [['patient-a', 1, 2]],
    afterRestoration: [['patient-a', 1, 2], ['patient-b', 1, 2]],
  });
  assert.deepEqual(subset.append, {
    before: expectedFullAppend,
    afterNarrowing: [['final', '2'], ['final', '2'], ['patient-a', '2']],
    afterRestoration: expectedFullAppend,
    rowCounts: { before: 4, afterNarrowing: 3, afterRestoration: 4 },
    duplicateFinalRowsAfterNarrowing: 2,
  });
});

test('current-draft Patient subset rejects invalid, nonmember, and wrong-scope rows', () => {
  const oracle = prepareCdaUpstreamAppendOracle(scans(), { project, generation });
  const patientA = oracle.sources['patient-id'][0];
  const invalidSubsets = [
    ['missing selection', []],
    ['multiple selected Patients', oracle.sources['patient-id']],
    ['malformed selection', [null]],
    ['nonmember Patient', [row('Patient', 'patient-doc-03', 'patient-c', true, 'patient-c')]],
    ['wrong project', [{ ...patientA, project: 'other-project' }]],
    ['wrong generation', [{ ...patientA, generation: 'other-generation' }]],
    ['wrong resource type', [{ ...patientA, resourceType: 'Observation' }]],
    ['changed source id field', [{ ...patientA, fieldValue: 'patient-other' }]],
  ];

  for (const [label, subset] of invalidSubsets) {
    assert.throws(() => prepareCdaUpstreamAppendPatientSubsetOracle(oracle, subset), undefined, label);
  }
});

test('oracle reports honest bounded unavailability without four final Observations or two Patient IDs', () => {
  const input = scans();
  input.Patient = [row('Patient', 'patient-doc-01', 'patient-a', true, 'patient-a')];
  input.Observation = input.Observation.filter(item => item.fieldValue !== 'final').concat(
    row('Observation', 'one', 'obs-only', true, 'final'),
  );
  assert.throws(() => prepareCdaUpstreamAppendOracle(input, { project, generation }), error => {
    assert(error instanceof CdaUpstreamAppendWitnessUnavailable);
    assert.match(error.message, /Patient\.id and disjoint Observation final-status witnesses/);
    assert.equal(error.evidence.scanLimitPerResource, 2_000);
    assert.equal(error.evidence.resources.length, 2);
    assert.deepEqual(error.evidence.reason, [
      'the bounded Patient.id scan has fewer than two eligible unique FHIR IDs',
      'the bounded Observation.status scan has fewer than four final rows for two disjoint pairs',
    ]);
    return true;
  });
});

test('oracle rejects a row outside the exact project or generation', () => {
  const input = scans();
  input.Patient[0] = { ...input.Patient[0], generation: 'other-generation' };
  assert.throws(() => prepareCdaUpstreamAppendOracle(input, { project, generation }), /out-of-scope/);
});

const supersededProposalEvidence = () => {
  const uiOrigin = 'http://127.0.0.1:30008';
  const proposalPath = '/api/v1/projects/owned/explorers/editor/authoring/v2/construction-proposals';
  const outputId = 'out_patient_group';
  const snapshotToken = 'catalog-snapshot-17';
  const draftVersion = 9;
  const draftDigest = 'draft-digest-9';
  const inputColumnId = 'column_patient_id';
  const changedStepId = 'step_group_patient';
  const countOutputColumnId = 'column_patient_count';
  const keyOutputColumnId = 'column_grouped_patient_id';
  const base = { outputId, snapshotToken, expectedDraftVersion: draftVersion, expectedDraftDigest: draftDigest };
  const step = (keys) => ({
    id: changedStepId,
    operation: {
      kind: 'GROUP',
      group: {
        keys,
        aggregates: [{ operation: 'COUNT_ROWS', outputColumnId: countOutputColumnId }],
      },
    },
    outputs: [
      ...(keys.length === 0 ? [] : [{ id: keyOutputColumnId, name: 'patient_id', label: 'Patient ID' }]),
      { id: countOutputColumnId, name: 'count', label: 'Rows' },
    ],
  });
  const canceledStep = step([]);
  const selectedStep = step([{ inputColumnId, outputColumnId: keyOutputColumnId }]);
  const canceledEntry = {
    origin: uiOrigin,
    path: proposalPath,
    method: 'POST',
    requestId: 'request-empty-group-1',
    browserRequestId: 'browser-request-empty-group-1',
    startedAt: 100,
    completedAt: 140,
    failure: 'net::ERR_ABORTED',
  };
  const replacementEntry = {
    origin: uiOrigin,
    path: proposalPath,
    method: 'POST',
    requestId: 'request-selected-group-2',
    browserRequestId: 'browser-request-selected-group-2',
    startedAt: 160,
    completedAt: 190,
    status: 200,
  };
  const requestBody = (candidateStep) => ({
    ...base,
    changedStepId,
    candidateConstruction: { steps: [candidateStep] },
  });
  const response = {
    outputId,
    snapshotToken,
    draftVersion,
    draftDigest,
    changedStepId,
    candidateConstruction: { steps: [selectedStep] },
    previewStatus: 'READY',
    proposalId: 'receipt-selected-group-2',
    preview: { outputId, receiptId: 'receipt-selected-group-2', rowCount: 2 },
  };

  return {
    canceledEntry,
    canceledBody: requestBody(canceledStep),
    replacementEntry,
    replacementBody: requestBody(selectedStep),
    replacementResponse: response,
    uiOrigin,
    proposalPath,
    outputId,
    snapshotToken,
    draftVersion,
    draftDigest,
    inputColumnId,
    checkboxClickedAt: 120,
    previewRowCount: 2,
    previewRowsMatched: true,
  };
};

test('superseded empty GROUP evidence accepts a captured abort followed by the selected-key READY receipt', () => {
  const evidence = proveSupersededEmptyGroupProposal(supersededProposalEvidence());
  assert.deepEqual(evidence, {
    supersession: 'initial-empty-group-candidate-replaced-by-selected-key',
    uiOriginBound: true,
    proposalEndpointBound: true,
    outputId: 'out_patient_group',
    changedStepId: 'step_group_patient',
    request: {
      canceledRequestId: 'request-empty-group-1',
      canceledBrowserRequestId: 'browser-request-empty-group-1',
      replacementRequestId: 'request-selected-group-2',
      replacementBrowserRequestId: 'browser-request-selected-group-2',
    },
    base: { sameSnapshot: true, sameDraftVersion: 9, sameDraftDigest: true },
    canceledCandidate: { operation: 'GROUP', keyCount: 0, aggregate: 'COUNT_ROWS' },
    replacementCandidate: {
      operation: 'GROUP', keyCount: 1, inputColumnId: 'column_patient_id', aggregate: 'COUNT_ROWS',
      keyOutputColumnId: 'column_grouped_patient_id', countOutputColumnId: 'column_patient_count',
    },
    replacementResponse: {
      status: 200, previewStatus: 'READY', receiptBound: true,
      previewRowCount: 2, independentRowsMatched: true,
    },
    timing: {
      intermediateStartedAt: 100,
      checkboxClickedAt: 120,
      intermediateAbortedAt: 140,
      replacementStartedAt: 160,
    },
  });
});

test('superseded empty GROUP proof rejects wrong origin, route, output, and CAS provenance', () => {
  const invalidCases = [
    ['wrong UI origin', args => { args.replacementEntry.origin = 'https://other-origin.example'; }],
    ['wrong proposal route', args => { args.replacementEntry.path += '/wrong'; }],
    ['wrong output', args => { args.replacementBody.outputId = 'out_other'; }],
    ['wrong catalog snapshot', args => { args.canceledBody.snapshotToken = 'other-snapshot'; }],
    ['wrong request draft version', args => { args.replacementBody.expectedDraftVersion += 1; }],
    ['wrong request draft digest', args => { args.canceledBody.expectedDraftDigest = 'other-digest'; }],
    ['contradictory legacy request version', args => { args.canceledBody.draftVersion = 8; }],
    ['contradictory response CAS alias', args => { args.replacementResponse.expectedDraftDigest = 'other-digest'; }],
  ];

  for (const [label, mutate] of invalidCases) {
    const args = supersededProposalEvidence();
    mutate(args);
    assert.throws(() => proveSupersededEmptyGroupProposal(args), undefined, label);
  }
});

test('superseded empty GROUP proof rejects wrong selected key, timing, failure, receipt, and oracle match', () => {
  const invalidCases = [
    ['wrong selected source key', args => {
      args.replacementBody.candidateConstruction.steps[0].operation.group.keys[0].inputColumnId = 'column_wrong';
    }],
    ['checkbox click outside the canceled request', args => { args.checkboxClickedAt = 140; }],
    ['canceled request timestamps are reversed', args => { args.canceledEntry.completedAt = 90; }],
    ['replacement starts before canceled request completion', args => { args.replacementEntry.startedAt = 135; }],
    ['replacement request timestamps are reversed', args => { args.replacementEntry.completedAt = 150; }],
    ['replacement has a native failure', args => { args.replacementEntry.failure = 'net::ERR_CONNECTION_RESET'; }],
    ['replacement response body was not captured', args => { args.replacementEntry.responseReadError = 'response body unavailable'; }],
    ['replacement preview is bound to another receipt', args => { args.replacementResponse.preview.receiptId = 'receipt-stale'; }],
    ['preview rows differ from the independent oracle', args => { args.previewRowsMatched = false; }],
    ['preview row count differs from the independent oracle', args => { args.replacementResponse.preview.rowCount = 3; }],
  ];

  for (const [label, mutate] of invalidCases) {
    const args = supersededProposalEvidence();
    mutate(args);
    assert.throws(() => proveSupersededEmptyGroupProposal(args), undefined, label);
  }
});

test('observed superseded empty GROUP proposals ignore a completed empty-key proposal with a successful replacement', () => {
  const { canceledEntry, canceledBody, ...commonEvidence } = supersededProposalEvidence();
  const initialEntry = {
    ...canceledEntry,
    requestId: 'request-initial-empty-group-0',
    browserRequestId: 'browser-request-initial-empty-group-0',
    startedAt: 80,
    completedAt: 110,
    failure: undefined,
    status: 200,
  };
  const proof = proveObservedSupersededEmptyGroupProposals({
    proposalEntries: [{ entry: initialEntry, body: canceledBody }],
    ...commonEvidence,
  });
  assert.deepEqual(proof, [], 'a completed initial empty-key proposal requires no superseded-abort proof');
});

test('observed superseded empty GROUP proposals prove one captured abort and reject a second unproven abort', () => {
  const { canceledEntry, canceledBody, ...commonEvidence } = supersededProposalEvidence();
  const capturedAbort = { entry: canceledEntry, body: canceledBody };
  const single = proveObservedSupersededEmptyGroupProposals({ proposalEntries: [capturedAbort], ...commonEvidence });
  assert.equal(single.length, 1);
  assert.equal(single[0].request.canceledRequestId, canceledEntry.requestId);
  assert.equal(single[0].canceledCandidate.keyCount, 0);
  assert.equal(single[0].replacementCandidate.inputColumnId, 'column_patient_id');

  const unprovenAbort = {
    ...canceledEntry,
    requestId: 'request-unproven-empty-group-2',
    browserRequestId: 'browser-request-unproven-empty-group-2',
    startedAt: 101,
    completedAt: 141,
    expected: true,
  };
  assert.throws(() => proveObservedSupersededEmptyGroupProposals({
    proposalEntries: [capturedAbort, { entry: unprovenAbort, body: canceledBody }],
    ...commonEvidence,
  }));
});
