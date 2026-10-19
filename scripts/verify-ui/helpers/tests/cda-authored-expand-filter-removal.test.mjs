import assert from 'node:assert/strict';
import test from 'node:test';
import {
  assertExpandFilterRemovalProposal,
  waitForAppliedSourceCapabilities,
  waitForSourceCapabilities,
} from '../../workflows/verify-cda-authored-expand-browser.mjs';

const expectedRemoval = {
  outputId: 'output-a',
  snapshotToken: 'snapshot-a',
  draftVersion: 6,
  draftDigest: 'draft-a',
  expandStepId: 'expand-a',
  filterStepId: 'filter-a',
  sourceRowCount: 3,
};

const removalRequest = () => ({
  request: {
    outputId: 'output-a',
    snapshotToken: 'snapshot-a',
    expectedDraftVersion: 6,
    expectedDraftDigest: 'draft-a',
    removeStepIds: ['expand-a'],
    candidateConstruction: { version: 1, steps: [{ id: 'filter-a', operation: { kind: 'FILTER' } }] },
  },
});

const removalResponse = () => ({
  outputId: 'output-a',
  snapshotToken: 'snapshot-a',
  draftVersion: 6,
  draftDigest: 'draft-a',
  candidateConstruction: { version: 1, steps: [] },
  dependencyImpact: { affectedStepIds: [], removedStepIds: ['expand-a', 'filter-a'] },
  preview: { outputId: 'output-a', rowCount: 3 },
});

test('Expand removal request names the selected step and response proves Filter cascade and RECORDS restoration', () => {
  assert.doesNotThrow(() => assertExpandFilterRemovalProposal(removalRequest(), removalResponse(), expectedRemoval));
});

test('Expand removal proof rejects a request for the wrong step', () => {
  const request = removalRequest();
  request.request.removeStepIds = ['other-step'];
  assert.throws(() => assertExpandFilterRemovalProposal(request, removalResponse(), expectedRemoval), /selected EXPAND step/);
});

test('Expand removal proof rejects a response that omits the dependent Filter impact', () => {
  const response = removalResponse();
  response.dependencyImpact.removedStepIds = ['expand-a'];
  assert.throws(() => assertExpandFilterRemovalProposal(removalRequest(), response, expectedRemoval), /dependent Filter step/);
});

test('Expand removal proof rejects a response that retains the Filter candidate', () => {
  const response = removalResponse();
  response.candidateConstruction.steps = [{ id: 'filter-a', operation: { kind: 'FILTER' } }];
  assert.throws(() => assertExpandFilterRemovalProposal(removalRequest(), response, expectedRemoval), /remove both dependent operations/);
});

test('Expand removal proof rejects an output mismatch', () => {
  const response = removalResponse();
  response.outputId = 'output-b';
  assert.throws(() => assertExpandFilterRemovalProposal(removalRequest(), response, expectedRemoval), /saved output/);
});

const capabilityIdentity = {
  snapshotToken: 'snapshot-a',
  draftVersion: 7,
  draftDigest: 'draft-after-cascade',
  outputId: 'output-a',
  stageId: 'source_projection',
};
const capabilityPath = '/api/v1/projects/owned/explorers/owned/authoring/v2/construction-capabilities';
const capabilityEntry = (overrides = {}) => ({
  method: 'POST',
  path: capabilityPath,
  request: {
    snapshotToken: capabilityIdentity.snapshotToken,
    expectedDraftVersion: capabilityIdentity.draftVersion,
    expectedDraftDigest: capabilityIdentity.draftDigest,
    outputId: capabilityIdentity.outputId,
    stageId: capabilityIdentity.stageId,
  },
  response: {
    snapshotToken: capabilityIdentity.snapshotToken,
    draftVersion: capabilityIdentity.draftVersion,
    draftDigest: capabilityIdentity.draftDigest,
    outputId: capabilityIdentity.outputId,
    stageId: capabilityIdentity.stageId,
    selectedStage: { id: capabilityIdentity.stageId },
  },
  status: 200,
  completedAt: Date.now(),
  ...overrides,
});

const createObserver = (entries = []) => {
  const requests = [...entries];
  const waiters = new Set();
  return {
    requests,
    waitForResponse(predicate, { fromIndex = 0, timeoutMs = 5000 } = {}) {
      const matched = requests.slice(fromIndex).find(predicate);
      if (matched) return Promise.resolve(matched);
      return new Promise((resolve, reject) => {
        const waiter = { predicate, fromIndex, resolve, reject };
        const timer = setTimeout(() => {
          waiters.delete(waiter);
          reject(new Error('Timed out waiting for a matching response'));
        }, timeoutMs);
        waiter.resolve = entry => {
          clearTimeout(timer);
          waiters.delete(waiter);
          resolve(entry);
        };
        waiters.add(waiter);
      });
    },
    push(entry) {
      requests.push(entry);
      for (const waiter of [...waiters]) {
        if (requests.indexOf(entry) >= waiter.fromIndex && waiter.predicate(entry)) waiter.resolve(entry);
      }
    },
  };
};

test('applied source capabilities match the exact successor draft response', async () => {
  const observer = createObserver();
  const waiting = waitForSourceCapabilities(observer, {
    fromIndex: 0,
    deadlineAt: Date.now() + 1000,
    path: capabilityPath,
    expected: capabilityIdentity,
  });
  observer.push(capabilityEntry({ request: { ...capabilityEntry().request, outputId: 'other-output' } }));
  observer.push(capabilityEntry());

  const result = await waiting;
  assert.equal(result.request.outputId, 'output-a');
  assert.deepEqual(result.response, capabilityEntry().response);
});

test('applied source capabilities reject the wrong successor identity by the action deadline', async () => {
  const applyRequest = {
    method: 'POST',
    path: '/api/v1/projects/owned/explorers/owned/authoring/v2/commands',
    request: {
      snapshotToken: 'snapshot-a',
      commands: [{ type: 'APPLY_CONSTRUCTION_PROPOSAL', outputId: 'output-a' }],
    },
    response: { draftVersion: 7, draftDigest: 'draft-after-cascade' },
    status: 200,
  };
  const observer = createObserver([applyRequest]);
  const waiting = waitForAppliedSourceCapabilities(observer, {
    applyRequest,
    fromIndex: 0,
    deadlineAt: Date.now() + 40,
    path: capabilityPath,
    outputId: 'output-a',
  });
  observer.push(capabilityEntry({
    request: { ...capabilityEntry().request, expectedDraftVersion: 8, expectedDraftDigest: 'other-draft' },
    response: { ...capabilityEntry().response, draftVersion: 8, draftDigest: 'other-draft' },
  }));
  await assert.rejects(waiting, /Timed out waiting for a matching response/);
});
