import assert from 'node:assert/strict';
import test from 'node:test';
import { adaptTemporalFilterJourney } from '../temporal-filter-trace-adapter.mjs';
import {
  makeTemporalFilterJourneyCaptureFixture,
  TEMPORAL_FILTER_FIXTURE_BASE_TIME,
} from './fixtures/temporal-filter-journey-schema-fixture.mjs';

test('adapter schema fixtures preserve DATE evidence as canonical events without claiming browser proof', () => {
  const rawCapture = makeTemporalFilterJourneyCaptureFixture('date');
  const journeyTrace = adaptTemporalFilterJourney(rawCapture);
  assert.equal(journeyTrace.model, 'TemporalFilterJourney');
  assert.equal(journeyTrace.name, 'basic-temporal-filter:date');
  assert.equal(journeyTrace.actions.length, 16);
  assert.deepEqual(journeyTrace.temporalFilterLifecycle, rawCapture);
  assert.equal(journeyTrace.initialObservation.snapshotToken, 'snapshot-1');
  assert(rawCapture.builderReads.every(read => read.snapshotToken === `snapshot-${read.draftVersion}`));
  const proposal = journeyTrace.actions.find(event => event.action === 'FilterProposed(create)');
  assert.deepEqual(proposal.filter, {
    intent: 'create',
    proposalId: 'proposal-first-create',
    receiptId: 'proposal-first-create',
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
  assert.equal(journeyTrace.actions.find(event => event.action === 'FilterApplied(create)').request.isMutation, true);
  assert.equal(journeyTrace.actions.find(event => event.action === 'FilterApplied(create)').filter.proposalId,
    'proposal-second-create-proposal');
  assert.equal(journeyTrace.actions.find(event => event.action === 'FilterApplied(create)').filter.receiptId,
    'proposal-second-create-proposal');
  assert.equal(proposal.request.isMutation, false);
  assert.equal(journeyTrace.actions.find(event => event.action === 'Reload').request, null);
  const edited = journeyTrace.actions.find(event => event.action === 'FilterApplied(edit)');
  assert.equal(edited.filter.proposalId, 'proposal-edit');
  assert.equal(edited.filter.receiptId, 'proposal-edit');
  const editProposal = journeyTrace.actions.find(event => event.action === 'FilterProposed(edit)');
  assert.equal(editProposal.filter.proposalId, 'proposal-edit');
  assert.equal(editProposal.filter.receiptId, 'proposal-edit');
  const removed = journeyTrace.actions.find(event => event.action === 'FilterRemoved');
  assert.equal(removed.filter.proposalId, 'proposal-remove');
  assert.equal(removed.filter.receiptId, 'proposal-remove');
  const removeProposal = journeyTrace.actions.find(event => event.action === 'FilterProposed(remove)');
  assert.equal(removeProposal.filter.proposalId, 'proposal-remove');
  assert.equal(removeProposal.filter.receiptId, 'proposal-remove');
  const choiceProposal = rawCapture.requestEvents.find(event => event.browserRequestId ===
    'schema-fixture-browser-request-choice-proposal');
  assert.deepEqual(choiceProposal.response.candidateColumnIds, ['birth-date-column']);
  assert.equal(choiceProposal.request.expectedDraftVersion, 2);
  assert.equal(choiceProposal.request.expectedDraftDigest, 'draft-2');
  const choiceApply = rawCapture.requestEvents.find(event => event.browserRequestId ===
    'schema-fixture-browser-request-choice-apply').request.commands[0];
  assert.deepEqual(choiceApply.constructionChoice, { choiceId: 'temporal-field-choice', form: 'VALUE' });
  assert.equal(Object.hasOwn(choiceApply, 'columnId'), false);
  const sourceCreate = rawCapture.requestEvents.find(event => event.browserRequestId ===
    'schema-fixture-browser-request-source-table');
  assert.equal(sourceCreate.request.expectedDraftVersion, 1);
  assert.equal(sourceCreate.request.expectedDraftDigest, 'draft-1');
});

test('adapter binds each reload to its actual reconcile request, CAS snapshot, and DOM receipt', () => {
  const rawCapture = makeTemporalFilterJourneyCaptureFixture('date');
  const journeyTrace = adaptTemporalFilterJourney(rawCapture);
  const reloads = journeyTrace.actions.filter(event => event.action === 'Reload');
  const reconcileRequests = rawCapture.requestEvents.filter(event => event.method === 'POST' &&
    event.path.endsWith('/reconcile'));
  assert.equal(reloads.length, 4);
  assert.equal(reconcileRequests.length, 4);
  assert.equal(new Set(reloads.flatMap(event => event.reconcileCaptureIds)).size, 4);

  const precedingProposals = [
    journeyTrace.actions.find(event => event.action === 'FilterProposed(create)'),
    journeyTrace.actions.filter(event => event.action === 'FilterProposed(create)')[1],
    journeyTrace.actions.find(event => event.action === 'FilterProposed(edit)'),
    journeyTrace.actions.find(event => event.action === 'FilterProposed(remove)'),
  ];
  for (const [index, reload] of reloads.entries()) {
    assert.equal(reload.reconcileCaptureIds.length, 1);
    const captureId = reload.reconcileCaptureIds[0];
    const request = reconcileRequests.find(entry => entry.browserRequestId === captureId);
    assert(request, 'Reload reconcile link must use the real observer browserRequestId.');
    assert.equal(request.isMutation, false);
    assert.equal(request.mountEpoch, reload.mountEpoch);
    assert.equal(request.request.snapshotToken, reload.after.snapshotToken);
    assert.equal(request.request.draftVersion, reload.after.draftVersion);
    assert.equal(request.request.draftDigest, reload.after.draftDigest);
    assert.equal(request.request.outputId, rawCapture.outputId);
    assert.equal(request.response.snapshotToken, reload.after.snapshotToken);
    assert(request.response.outputs.some(output => output.outputId === rawCapture.outputId));
    assert.equal(reload.ui.receiptId, request.response.receiptId);
    assert.notEqual(reload.ui.receiptId, precedingProposals[index].filter.receiptId,
      'Reload DOM must preserve its actual reconcile receipt, distinct from the prior proposal receipt.');
  }
});

test('adapter schema fixtures keep DATE_TIME request, response, and saved literals separate', () => {
  const rawCapture = makeTemporalFilterJourneyCaptureFixture('date-time');
  const journeyTrace = adaptTemporalFilterJourney(rawCapture);
  const proposed = journeyTrace.actions.filter(event => event.action === 'FilterProposed(create)');
  assert.deepEqual(proposed[0].ui.rowIds, [], 'The empty DATE_TIME proposal must retain no data row IDs.');
  assert.equal(proposed[0].filter.proposalId, 'proposal-first-create');
  assert.equal(proposed[0].filter.receiptId, 'proposal-first-create');
  assert.deepEqual(proposed[0].filter.requestValue,
    { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00.000Z' });
  assert.deepEqual(proposed[0].filter.responseValue,
    { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00Z' });
  const applied = journeyTrace.actions.find(event => event.action === 'FilterApplied(create)');
  assert.deepEqual(applied.filter.savedValue, { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00Z' });
  assert.equal(journeyTrace.actions.some(event => event.filter?.valueKind === 'Instant'), false);
});

test('adapter rejects an observer write that no semantic action consumes', () => {
  const rawCapture = makeTemporalFilterJourneyCaptureFixture('date');
  rawCapture.requestEvents.push({
    browserRequestId: 'schema-fixture-browser-request-unexpected-write',
    requestId: 'schema-fixture-raw-request-unexpected-write',
    method: 'POST',
    path: '/api/v1/projects/loom_dev_verify_temporal/unexpected-write',
    status: 200,
    request: {},
    response: {},
    startedAt: TEMPORAL_FILTER_FIXTURE_BASE_TIME + 1000,
    responseReceivedAt: TEMPORAL_FILTER_FIXTURE_BASE_TIME + 1001,
    completedAt: TEMPORAL_FILTER_FIXTURE_BASE_TIME + 1002,
    isMutation: true,
    atMonotonicMs: TEMPORAL_FILTER_FIXTURE_BASE_TIME + 1000,
    atEpochMs: TEMPORAL_FILTER_FIXTURE_BASE_TIME + 1000,
    mountEpoch: 5,
  });
  assert.throws(() => adaptTemporalFilterJourney(rawCapture), /Every observer-classified write must be consumed exactly once/);
});

test('adapter preserves an absent raw request header as null in normalized evidence', () => {
  const rawCapture = makeTemporalFilterJourneyCaptureFixture('date');
  delete rawCapture.requestEvents.find(event => event.browserRequestId === 'schema-fixture-browser-request-first-create').requestId;
  const journeyTrace = adaptTemporalFilterJourney(rawCapture);
  const proposal = journeyTrace.actions.find(event => event.action === 'FilterProposed(create)');
  assert.equal(proposal.request.requestId, null);
});

test('adapter schema fixture retains failed read-only request diagnostics', () => {
  const rawCapture = makeTemporalFilterJourneyCaptureFixture('date');
  const journeyTrace = adaptTemporalFilterJourney(rawCapture);
  const request = journeyTrace.temporalFilterLifecycle.requestEvents.find(event =>
    event.browserRequestId === 'schema-fixture-browser-request-edit-open-cancellation');
  assert.deepEqual(request.failure, { kind: 'transport', errorText: 'net::ERR_ABORTED', canceled: true });
  assert.equal(request.readCancellationDiagnostic, true);
  assert.equal(request.responseCapture, 'not-configured');
  assert.equal(request.responseCaptureComplete, true);
  assert.equal(request.responseBodyCaptured, false);
  assert.equal(request.responseReceivedAt, null, 'A canceled proposal has no observer response event.');
  const successful = rawCapture.requestEvents.find(event =>
    event.browserRequestId === 'schema-fixture-browser-request-first-create');
  assert(Number.isFinite(successful.responseReceivedAt), 'A successful proposal must retain its observer response event time.');
  assert(successful.completedAt > successful.responseReceivedAt,
    'Response-body completion must remain distinct from response receipt.');
});

test('adapter schema fixture evidence follows the ordered lifecycle timestamps and read indexes', () => {
  const capture = makeTemporalFilterJourneyCaptureFixture('date');
  const sorted = values => assert.deepEqual(values, [...values].sort((left, right) => left - right));
  sorted(capture.requestEvents.map(event => event.atMonotonicMs));
  sorted(capture.builderReads.map(read => read.capturedAtMonotonicMs));
  sorted(capture.domObservations.map(observation => observation.capturedAtMonotonicMs));
  sorted(capture.steps.map(step => step.atMonotonicMs));
  for (const step of capture.steps) {
    if (step.beforeReadIndex !== null) {
      assert(capture.builderReads[step.beforeReadIndex].capturedAtMonotonicMs <= step.atMonotonicMs,
        `${step.label} before read must precede its semantic event.`);
    }
    if (step.requestCaptureId && step.afterReadIndex !== null) {
      assert(capture.builderReads[step.afterReadIndex].capturedAtMonotonicMs >= step.atMonotonicMs,
        `${step.label} post-request read must follow its request event.`);
    }
    if (step.requestCaptureId && step.domObservationIndex !== null) {
      assert(capture.domObservations[step.domObservationIndex].capturedAtMonotonicMs > step.atMonotonicMs,
        `${step.label} DOM observation must follow its request event.`);
    }
  }
});
