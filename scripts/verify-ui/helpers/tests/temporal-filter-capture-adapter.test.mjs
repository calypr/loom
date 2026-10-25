import assert from 'node:assert/strict';
import test from 'node:test';
import { adaptTemporalFilterJourney } from '../temporal-filter-trace-adapter.mjs';

const CASES = Object.freeze({
  date: Object.freeze({
    resourceType: 'Patient',
    fieldPath: 'birthDate',
    inputType: 'date',
    temporalColumnId: 'birth-date-column',
    rows: [
      { id: 'dev-patient-001', value: '1980-01-02' },
      { id: 'dev-patient-002', value: '1985-03-04' },
    ],
    initial: { operator: 'GT', inputValue: '1980-01-02', requestValue: { kind: 'DATE', date: '1980-01-02' },
      savedValue: { kind: 'DATE', date: '1980-01-02' }, ids: ['dev-patient-002'] },
    edited: { operator: 'GTE', inputValue: '1980-01-02', requestValue: { kind: 'DATE', date: '1980-01-02' },
      savedValue: { kind: 'DATE', date: '1980-01-02' }, ids: ['dev-patient-001', 'dev-patient-002'] },
  }),
  'date-time': Object.freeze({
    resourceType: 'Observation',
    fieldPath: 'effectiveDateTime',
    inputType: 'datetime-local',
    temporalColumnId: 'effective-datetime-column',
    rows: [
      { id: 'dev-observation-001', value: '2026-01-02T00:00:00Z' },
      { id: 'dev-observation-002', value: '2026-01-02T00:00:00Z' },
      { id: 'dev-observation-003', value: '2026-01-02T00:00:00Z' },
    ],
    initial: { operator: 'GT', inputValue: '2026-01-02T00:00',
      requestValue: { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00.000Z' },
      savedValue: { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00Z' }, ids: [] },
    edited: { operator: 'GTE', inputValue: '2026-01-02T00:00',
      requestValue: { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00Z' },
      savedValue: { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00Z' },
      ids: ['dev-observation-001', 'dev-observation-002', 'dev-observation-003'] },
  }),
});

const clone = value => structuredClone(value);
const baseTime = 1_000;

const makeFilterStep = (stepId, spec, predicate = spec.savedValue) => ({
  id: stepId,
  operation: {
    kind: 'FILTER',
    filter: { columnId: spec.temporalColumnId, operator: spec.operator, values: [clone(predicate)] },
  },
});

const makeDocument = (spec, withTemporalColumn, filterStep = null) => ({
  rootResourceType: spec.resourceType,
  output: { id: 'temporal-output' },
  columns: [
    { columnId: 'source-id-column', source: { kind: 'field', field: { path: 'id' } } },
    ...(withTemporalColumn ? [{
      columnId: spec.temporalColumnId,
      source: { kind: 'field', field: { path: spec.fieldPath } },
    }] : []),
  ],
  construction: { steps: [{ id: 'source-step', operation: { kind: 'SOURCE' } }, ...(filterStep ? [clone(filterStep)] : [])] },
});

const filterConstruction = (spec, intent, predicate = spec.initial.savedValue) => ({
  steps: [
    { id: 'source-step', operation: { kind: 'SOURCE' } },
    ...(intent === 'remove' ? [] : [makeFilterStep(intent === 'edit' ? 'saved-filter-step' : 'candidate-filter-step', {
      temporalColumnId: spec.temporalColumnId,
      operator: intent === 'edit' ? spec.edited.operator : spec.initial.operator,
      savedValue: predicate,
    }, predicate)]),
  ],
});

const makeCapture = caseName => {
  const spec = CASES[caseName];
  const outputId = 'temporal-output';
  const filterStepInitial = makeFilterStep('saved-filter-step', {
    temporalColumnId: spec.temporalColumnId, operator: spec.initial.operator, savedValue: spec.initial.savedValue,
  });
  const filterStepEdited = makeFilterStep('saved-filter-step', {
    temporalColumnId: spec.temporalColumnId, operator: spec.edited.operator, savedValue: spec.edited.savedValue,
  });
  const document = {
    blank: [],
    root: [makeDocument(spec, false)],
    temporal: [makeDocument(spec, true)],
    initial: [makeDocument(spec, true, filterStepInitial)],
    edited: [makeDocument(spec, true, filterStepEdited)],
    removed: [makeDocument(spec, true)],
  };
  const builderStates = [
    ['blank', 1, 'draft-1', 1], ['blank', 1, 'draft-1', 1],
    ['root', 2, 'draft-2', 1], ['root', 2, 'draft-2', 1],
    ['temporal', 3, 'draft-3', 1], ['temporal', 3, 'draft-3', 1],
    ['temporal', 3, 'draft-3', 1], ['temporal', 3, 'draft-3', 2],
    ['temporal', 3, 'draft-3', 2], ['initial', 4, 'draft-4', 2],
    ['initial', 4, 'draft-4', 3], ['initial', 4, 'draft-4', 3],
    ['initial', 4, 'draft-4', 3], ['edited', 5, 'draft-5', 3],
    ['edited', 5, 'draft-5', 4], ['edited', 5, 'draft-5', 4],
    ['edited', 5, 'draft-5', 4], ['removed', 6, 'draft-6', 4],
    ['removed', 6, 'draft-6', 5],
  ];
  const builderReads = builderStates.map(([state, draftVersion, draftDigest, epoch], index) => ({
    readId: `schema-fixture-builder-read-${index}`,
    requestId: `schema-fixture-api-read-${index}`,
    status: 200,
    capturedAtMonotonicMs: baseTime + index * 10,
    capturedAtEpochMs: baseTime + index * 10,
    mountEpoch: epoch,
    catalogGeneration: 'basic-temporal-generation',
    draftVersion,
    draftDigest,
    workspaceDocuments: clone(document[state]),
  }));

  const requestEvents = [];
  const addRequest = ({ name, path, body, response, isMutation, status = 200, mountEpoch = 1 }) => {
    const captureId = `schema-fixture-browser-request-${name}`;
    requestEvents.push({
      browserRequestId: captureId,
      requestId: `schema-fixture-raw-request-${name}`,
      method: 'POST',
      path,
      status,
      request: clone(body),
      response: clone(response),
      startedAt: baseTime + requestEvents.length * 10,
      completedAt: baseTime + requestEvents.length * 10 + 1,
      isMutation,
      atMonotonicMs: baseTime + requestEvents.length * 10,
      atEpochMs: baseTime + requestEvents.length * 10,
      mountEpoch,
    });
    return captureId;
  };
  const commandPath = '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/commands';
  const proposalPath = '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/construction-proposals';
  const choiceProposalPath = '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/construction-choice-proposals';
  const explorerRequestId = addRequest({
    name: 'explorer-create', path: '/api/v1/projects/loom_dev_verify_temporal/explorers',
    body: { title: 'temporal fixture Explorer' }, response: { explorerId: 'explorer-temporal' }, isMutation: true, status: 201,
  });
  const sourceRequestId = addRequest({ name: 'source-table', path: commandPath,
    body: { commands: [{ type: 'CREATE_TABLE' }] }, response: { accepted: true }, isMutation: true });
  const choiceProposalId = addRequest({ name: 'choice-proposal', path: choiceProposalPath,
    body: { outputId }, response: { outputId, previewStatus: 'READY', preview: { receiptId: 'column-receipt', outputId } },
    isMutation: false });
  const choiceApplyId = addRequest({ name: 'choice-apply', path: commandPath,
    body: { commands: [{ type: 'APPLY_CONSTRUCTION_CHOICE', outputId }] },
    response: { accepted: true }, isMutation: true });

  const addFilterProposal = (name, intent, specValue, stepId, epoch, draftVersion, draftDigest) => {
    const predicate = clone(specValue.requestValue);
    const requestCandidate = filterConstruction(spec, intent, predicate);
    const responseCandidate = filterConstruction(spec, intent,
      intent === 'edit' ? spec.edited.savedValue : spec.initial.savedValue);
    if (intent !== 'remove') {
      requestCandidate.steps[1].id = stepId;
      responseCandidate.steps[1].id = stepId;
    }
    const proposalId = `proposal-${name}`;
    return addRequest({
      name, path: proposalPath,
      body: { outputId, expectedDraftVersion: draftVersion, expectedDraftDigest: draftDigest,
        candidateConstruction: requestCandidate },
      response: {
        proposalId,
        outputId,
        draftVersion,
        draftDigest,
        previewStatus: 'READY',
        candidateConstruction: responseCandidate,
        preview: { receiptId: proposalId, outputId },
      },
      isMutation: false,
      mountEpoch: epoch,
    });
  };
  const firstProposalId = addFilterProposal('first-create', 'create', spec.initial,
    'candidate-create-filter', 1, 3, 'draft-3');
  const secondProposalId = addFilterProposal('second-create-proposal', 'create', spec.initial,
    'saved-filter-step', 2, 3, 'draft-3');
  const applyCreateId = addRequest({ name: 'create-apply-command', path: commandPath,
    body: { expectedDraftVersion: 3, expectedDraftDigest: 'draft-3', commands: [{
      type: 'APPLY_CONSTRUCTION_PROPOSAL', outputId, proposalId: 'proposal-second-create-proposal',
    }] }, response: { accepted: true }, isMutation: true, mountEpoch: 2 });
  const editProposalId = addFilterProposal('edit', 'edit', spec.edited, 'saved-filter-step', 3, 4, 'draft-4');
  const applyEditId = addRequest({ name: 'apply-edit', path: commandPath,
    body: { expectedDraftVersion: 4, expectedDraftDigest: 'draft-4', commands: [{
      type: 'APPLY_CONSTRUCTION_PROPOSAL', outputId, proposalId: 'proposal-edit',
    }] }, response: { accepted: true }, isMutation: true, mountEpoch: 3 });
  const removeProposalId = addFilterProposal('remove', 'remove', spec.edited,
    'saved-filter-step', 4, 5, 'draft-5');
  const applyRemoveId = addRequest({ name: 'apply-remove', path: commandPath,
    body: { expectedDraftVersion: 5, expectedDraftDigest: 'draft-5', commands: [{
      type: 'APPLY_CONSTRUCTION_PROPOSAL', outputId, proposalId: 'proposal-remove',
    }] }, response: { accepted: true }, isMutation: true, mountEpoch: 4 });

  const domObservations = [];
  const addDOM = ({ epoch, proposalId = null, receiptId = null, status = null, columnId = null,
    inputType = null, inputValue = null, operator = null, rowIds }) => {
    domObservations.push({
      capturedAtMonotonicMs: baseTime + domObservations.length * 10,
      capturedAtEpochMs: baseTime + domObservations.length * 10,
      mountEpoch: epoch,
      proposalStatus: status,
      proposalId,
      receiptId,
      outputId,
      columnId,
      inputType,
      inputValue,
      operator,
      rowIds: [...rowIds],
    });
    return domObservations.length - 1;
  };
  const allIDs = spec.rows.map(row => row.id);
  const domIndexes = {
    source: addDOM({ epoch: 1, rowIds: allIDs }),
    column: addDOM({ epoch: 1, rowIds: allIDs }),
    proposeFirst: addDOM({ epoch: 1, proposalId: 'proposal-first-create', receiptId: 'proposal-first-create',
      status: 'ready', columnId: spec.temporalColumnId, inputType: spec.inputType, inputValue: spec.initial.inputValue,
      operator: spec.initial.operator, rowIds: spec.initial.ids }),
    cancel: addDOM({ epoch: 1, rowIds: allIDs }),
    reloadCancel: addDOM({ epoch: 2, rowIds: allIDs }),
    proposeCreate: addDOM({ epoch: 2, proposalId: 'proposal-second-create-proposal', receiptId: 'proposal-second-create-proposal',
      status: 'ready', columnId: spec.temporalColumnId, inputType: spec.inputType, inputValue: spec.initial.inputValue,
      operator: spec.initial.operator, rowIds: spec.initial.ids }),
    applyCreate: addDOM({ epoch: 2, rowIds: spec.initial.ids }),
    reloadCreate: addDOM({ epoch: 3, rowIds: spec.initial.ids }),
    editOpened: addDOM({ epoch: 3, columnId: spec.temporalColumnId, inputType: spec.inputType,
      inputValue: spec.initial.inputValue, operator: spec.initial.operator, rowIds: spec.initial.ids }),
    proposeEdit: addDOM({ epoch: 3, proposalId: 'proposal-edit', receiptId: 'proposal-edit', status: 'ready',
      columnId: spec.temporalColumnId, inputType: spec.inputType, inputValue: spec.edited.inputValue,
      operator: spec.edited.operator, rowIds: spec.edited.ids }),
    applyEdit: addDOM({ epoch: 3, rowIds: spec.edited.ids }),
    reloadEdit: addDOM({ epoch: 4, rowIds: spec.edited.ids }),
    proposeRemove: addDOM({ epoch: 4, proposalId: 'proposal-remove', receiptId: 'proposal-remove',
      status: 'ready', rowIds: allIDs }),
    remove: addDOM({ epoch: 4, rowIds: allIDs }),
    reloadRemove: addDOM({ epoch: 5, rowIds: allIDs }),
  };

  const step = (label, { requestCaptureId = null, proposalCaptureIds = [], beforeReadIndex = null,
    afterReadIndex = null, domObservationIndex = null, mountEpoch = 1 }) => {
    const request = requestEvents.find(event => event.browserRequestId === requestCaptureId);
    const dom = domObservationIndex === null ? null : domObservations[domObservationIndex];
    const timestamp = request?.atMonotonicMs ?? dom?.capturedAtMonotonicMs ?? baseTime;
    const epochTimestamp = request?.atEpochMs ?? dom?.capturedAtEpochMs ?? baseTime;
    const capturedMountEpoch = request?.mountEpoch ?? dom?.mountEpoch ?? mountEpoch;
    return {
      label,
      atMonotonicMs: timestamp,
      atEpochMs: epochTimestamp,
      mountEpoch: capturedMountEpoch,
      requestCaptureId,
      proposalCaptureIds,
      beforeReadIndex,
      afterReadIndex,
      domObservationIndex,
    };
  };
  const steps = [
    step('ExplorerCreated', { requestCaptureId: explorerRequestId, afterReadIndex: 0, mountEpoch: 1 }),
    step('SourceTableCreated', { requestCaptureId: sourceRequestId, beforeReadIndex: 1, afterReadIndex: 2,
      domObservationIndex: domIndexes.source, mountEpoch: 1 }),
    step('TemporalColumnApplied', { requestCaptureId: choiceApplyId, proposalCaptureIds: [choiceProposalId],
      beforeReadIndex: 3, afterReadIndex: 4, domObservationIndex: domIndexes.column, mountEpoch: 1 }),
    step('FilterProposed(create)', { requestCaptureId: firstProposalId, proposalCaptureIds: [firstProposalId],
      beforeReadIndex: 5, domObservationIndex: domIndexes.proposeFirst, mountEpoch: 1 }),
    step('FilterCancelled', { beforeReadIndex: 5, afterReadIndex: 6, domObservationIndex: domIndexes.cancel, mountEpoch: 1 }),
    step('Reload', { beforeReadIndex: 6, afterReadIndex: 7, domObservationIndex: domIndexes.reloadCancel, mountEpoch: 2 }),
    step('FilterProposed(create)', { requestCaptureId: secondProposalId, proposalCaptureIds: [secondProposalId],
      beforeReadIndex: 7, domObservationIndex: domIndexes.proposeCreate, mountEpoch: 2 }),
    step('FilterApplied(create)', { requestCaptureId: applyCreateId, beforeReadIndex: 8, afterReadIndex: 9,
      domObservationIndex: domIndexes.applyCreate, mountEpoch: 2 }),
    step('Reload', { beforeReadIndex: 9, afterReadIndex: 10, domObservationIndex: domIndexes.reloadCreate, mountEpoch: 3 }),
    step('FilterEditOpened', { beforeReadIndex: 11, domObservationIndex: domIndexes.editOpened, mountEpoch: 3 }),
    step('FilterProposed(edit)', { requestCaptureId: editProposalId, proposalCaptureIds: [editProposalId],
      beforeReadIndex: 11, domObservationIndex: domIndexes.proposeEdit, mountEpoch: 3 }),
    step('FilterApplied(edit)', { requestCaptureId: applyEditId, beforeReadIndex: 12, afterReadIndex: 13,
      domObservationIndex: domIndexes.applyEdit, mountEpoch: 3 }),
    step('Reload', { beforeReadIndex: 13, afterReadIndex: 14, domObservationIndex: domIndexes.reloadEdit, mountEpoch: 4 }),
    step('FilterProposed(remove)', { requestCaptureId: removeProposalId, proposalCaptureIds: [removeProposalId],
      beforeReadIndex: 15, domObservationIndex: domIndexes.proposeRemove, mountEpoch: 4 }),
    step('FilterRemoved', { requestCaptureId: applyRemoveId, beforeReadIndex: 16, afterReadIndex: 17,
      domObservationIndex: domIndexes.remove, mountEpoch: 4 }),
    step('Reload', { beforeReadIndex: 17, afterReadIndex: 18, domObservationIndex: domIndexes.reloadRemove, mountEpoch: 5 }),
  ];

  return {
    schemaVersion: 1,
    scenario: 'basic-temporal-filter',
    caseName,
    sourceCommit: 'a'.repeat(40),
    sourceFingerprint: { sha256: 'b'.repeat(64), files: 48 },
    project: 'loom_dev_verify_temporal',
    generation: 'basic-temporal-generation',
    resourceType: spec.resourceType,
    fieldPath: spec.fieldPath,
    fixturePath: `testdata/basic-temporal-filter/${spec.resourceType}.ndjson`,
    fixtureSha256: 'c'.repeat(64),
    rows: clone(spec.rows),
    ids: spec.rows.map(row => row.id),
    outputId,
    temporalColumnId: spec.temporalColumnId,
    requestEvents,
    builderReads,
    domObservations,
    steps,
  };
};

test('adapter schema fixtures preserve DATE evidence as canonical events without claiming browser proof', () => {
  const rawCapture = makeCapture('date');
  const journeyTrace = adaptTemporalFilterJourney(rawCapture);
  assert.equal(journeyTrace.model, 'TemporalFilterJourney');
  assert.equal(journeyTrace.name, 'basic-temporal-filter:date');
  assert.equal(journeyTrace.actions.length, 16);
  assert.deepEqual(journeyTrace.temporalFilterLifecycle, rawCapture);
  const proposal = journeyTrace.actions.find(event => event.action === 'FilterProposed(create)');
  assert.deepEqual(proposal.filter, {
    intent: 'create',
    stepId: 'candidate-create-filter',
    outputId: 'temporal-output',
    columnId: 'birth-date-column',
    valueKind: 'DATE',
    operator: 'GT',
    requestValue: { kind: 'DATE', date: '1980-01-02' },
    responseValue: { kind: 'DATE', date: '1980-01-02' },
    savedValue: null,
  });
  assert.equal(proposal.request.captureId, 'schema-fixture-browser-request-first-create');
  assert.equal(proposal.request.requestId, 'schema-fixture-raw-request-first-create');
});

test('adapter schema fixtures keep DATE_TIME request, response, and saved literals separate', () => {
  const rawCapture = makeCapture('date-time');
  const journeyTrace = adaptTemporalFilterJourney(rawCapture);
  const proposed = journeyTrace.actions.filter(event => event.action === 'FilterProposed(create)');
  assert.deepEqual(proposed[0].filter.requestValue,
    { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00.000Z' });
  assert.deepEqual(proposed[0].filter.responseValue,
    { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00Z' });
  const applied = journeyTrace.actions.find(event => event.action === 'FilterApplied(create)');
  assert.deepEqual(applied.filter.savedValue, { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00Z' });
  assert.equal(journeyTrace.actions.some(event => event.filter?.valueKind === 'Instant'), false);
});

test('adapter rejects an observer write that no semantic action consumes', () => {
  const rawCapture = makeCapture('date');
  rawCapture.requestEvents.push({
    browserRequestId: 'schema-fixture-browser-request-unexpected-write',
    requestId: 'schema-fixture-raw-request-unexpected-write',
    method: 'POST',
    path: '/api/v1/projects/loom_dev_verify_temporal/unexpected-write',
    status: 200,
    request: {},
    response: {},
    startedAt: baseTime + 1000,
    completedAt: baseTime + 1001,
    isMutation: true,
    atMonotonicMs: baseTime + 1000,
    atEpochMs: baseTime + 1000,
    mountEpoch: 5,
  });
  assert.throws(() => adaptTemporalFilterJourney(rawCapture), /Every observer-classified write must be consumed exactly once/);
});

test('adapter preserves an absent raw request header as null in normalized evidence', () => {
  const rawCapture = makeCapture('date');
  delete rawCapture.requestEvents.find(event => event.browserRequestId === 'schema-fixture-browser-request-first-create').requestId;
  const journeyTrace = adaptTemporalFilterJourney(rawCapture);
  const proposal = journeyTrace.actions.find(event => event.action === 'FilterProposed(create)');
  assert.equal(proposal.request.requestId, null);
});
