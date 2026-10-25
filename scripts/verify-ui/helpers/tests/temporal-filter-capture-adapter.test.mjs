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
  const choiceApply = rawCapture.requestEvents.find(event => event.browserRequestId ===
    'schema-fixture-browser-request-choice-apply').request.commands[0];
  assert.deepEqual(choiceApply.constructionChoice, { choiceId: 'temporal-field-choice', form: 'VALUE' });
  assert.equal(Object.hasOwn(choiceApply, 'columnId'), false);
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
    completedAt: TEMPORAL_FILTER_FIXTURE_BASE_TIME + 1001,
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
  const journeyTrace = adaptTemporalFilterJourney(makeTemporalFilterJourneyCaptureFixture('date'));
  const request = journeyTrace.temporalFilterLifecycle.requestEvents.find(event =>
    event.browserRequestId === 'schema-fixture-browser-request-edit-open-cancellation');
  assert.deepEqual(request.failure, { kind: 'transport', errorText: 'net::ERR_ABORTED', canceled: true });
  assert.equal(request.readCancellationDiagnostic, true);
  assert.equal(request.responseCapture, 'not-configured');
  assert.equal(request.responseCaptureComplete, true);
  assert.equal(request.responseBodyCaptured, false);
});
