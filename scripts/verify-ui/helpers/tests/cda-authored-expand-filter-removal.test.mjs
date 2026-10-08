import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import { captureCDARequests } from '../cda-playwright-requests.mjs';
import { assertExpandFilterRemovalProposal, waitForAppliedSourceCapabilities, waitForSourceCapabilities } from '../../workflows/verify-cda-authored-expand-browser.mjs';

const expected = {
  outputId: 'output-a',
  snapshotToken: 'snapshot-a',
  draftVersion: 6,
  draftDigest: 'draft-a',
  expandStepId: 'expand-a',
  filterStepId: 'filter-a',
  sourceRowCount: 3,
};

const actualRequest = () => ({
  body: {
    outputId: 'output-a',
    snapshotToken: 'snapshot-a',
    expectedDraftVersion: 6,
    expectedDraftDigest: 'draft-a',
    removeStepIds: ['expand-a'],
    candidateConstruction: { version: 1, steps: [{ id: 'filter-a', operation: { kind: 'FILTER' } }] },
  },
});

const actualResponse = () => ({
  outputId: 'output-a',
  snapshotToken: 'snapshot-a',
  draftVersion: 6,
  draftDigest: 'draft-a',
  candidateConstruction: { version: 1, steps: [] },
  dependencyImpact: { affectedStepIds: [], removedStepIds: ['expand-a', 'filter-a'] },
  preview: { outputId: 'output-a', rowCount: 3 },
});

test('Expand removal request names the selected step and response proves Filter cascade and RECORDS restoration', () => {
  assert.doesNotThrow(() => assertExpandFilterRemovalProposal(actualRequest(), actualResponse(), expected));
});

test('Expand removal proof rejects a request for the wrong step', () => {
  const request = actualRequest();
  request.body.removeStepIds = ['other-step'];
  assert.throws(() => assertExpandFilterRemovalProposal(request, actualResponse(), expected), /selected EXPAND step/);
});

test('Expand removal proof rejects a response that omits the dependent Filter impact', () => {
  const response = actualResponse();
  response.dependencyImpact.removedStepIds = ['expand-a'];
  assert.throws(() => assertExpandFilterRemovalProposal(actualRequest(), response, expected), /dependent Filter step/);
});

test('Expand removal proof rejects a response that retains the Filter candidate', () => {
  const response = actualResponse();
  response.candidateConstruction.steps = [{ id: 'filter-a', operation: { kind: 'FILTER' } }];
  assert.throws(() => assertExpandFilterRemovalProposal(actualRequest(), response, expected), /remove both dependent operations/);
});


const origin = 'http://127.0.0.1:30102';
const ownedPathPrefix = '/api/v1/projects/owned/explorers/owned';
const commandPath = `${ownedPathPrefix}/authoring/v2/commands`;
const capabilitiesPath = `${ownedPathPrefix}/authoring/v2/construction-capabilities`;
const appliedSnapshot = 'snapshot-a';
const appliedDraftVersion = 7;
const appliedDraftDigest = 'draft-after-cascade';
const appliedOutputId = 'output-a';
const appliedStageId = 'source_projection';
const appliedIdentity = {
  snapshotToken: appliedSnapshot,
  expectedDraftVersion: appliedDraftVersion,
  expectedDraftDigest: appliedDraftDigest,
  outputId: appliedOutputId,
  stageId: appliedStageId,
};
const appliedExpectedIdentity = {
  snapshotToken: appliedSnapshot, draftVersion: appliedDraftVersion, draftDigest: appliedDraftDigest,
  outputId: appliedOutputId, stageId: appliedStageId,
};
const appliedResponse = {
  snapshotToken: appliedSnapshot,
  draftVersion: appliedDraftVersion,
  draftDigest: appliedDraftDigest,
  outputId: appliedOutputId,
  stageId: appliedStageId,
  selectedStage: { id: appliedStageId },
};
const makeOwnedRequest = (path, body) => ({
  url: () => `${origin}${path}`,
  method: () => 'POST',
  headers: () => ({}),
  postData: () => JSON.stringify(body),
  failure: () => null,
});
const makeResponse = (request, text) => ({
  request: () => request,
  status: () => 200,
  headers: () => ({}),
  text,
});
const createCapture = () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const capture = captureCDARequests(page, { apiOrigin: origin, ownedPathPrefix, report });
  return { page, report, capture };
};
const emitApply = async ({ page, report }) => {
  const applyBody = {
    snapshotToken: appliedSnapshot,
    expectedDraftVersion: 6,
    expectedDraftDigest: 'draft-before-cascade',
    commands: [{ type: 'APPLY_CONSTRUCTION_PROPOSAL', outputId: appliedOutputId }],
  };
  const request = makeOwnedRequest(commandPath, applyBody);
  page.emit('request', request);
  page.emit('response', makeResponse(request, async () => JSON.stringify({
    commandId: 'apply-command', draftVersion: appliedDraftVersion, draftDigest: appliedDraftDigest,
  })));
  await new Promise(resolve => setImmediate(resolve));
  return report.nativeRequests[0];
};

test('applied source capabilities wait for the exact successor draft through delayed response-body capture', async () => {
  const harness = createCapture();
  const applyRequest = await emitApply(harness);
  let settled = false;
  const waiting = waitForAppliedSourceCapabilities(harness.capture, {
    applyRequest, fromIndex: 0, deadlineAt: Date.now() + 1000,
    path: capabilitiesPath, outputId: appliedOutputId,
  }).then(value => { settled = true; return value; });

  const staleRequest = makeOwnedRequest(capabilitiesPath, {
    ...appliedIdentity, expectedDraftVersion: 6, expectedDraftDigest: 'draft-before-cascade', outputId: 'other-output',
  });
  harness.page.emit('request', staleRequest);
  harness.page.emit('response', makeResponse(staleRequest, async () => JSON.stringify({
    ...appliedResponse, draftVersion: 6, draftDigest: 'draft-before-cascade', outputId: 'other-output',
  })));
  await harness.capture.flush();
  assert.equal(settled, false, 'stale draft and output cannot settle the Apply lifecycle');

  const exactRequest = makeOwnedRequest(capabilitiesPath, appliedIdentity);
  harness.page.emit('request', exactRequest);
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(settled, false, 'the exact capability request must remain pending until its response is captured');
  let resolveBody;
  const delayedBody = new Promise(resolve => { resolveBody = resolve; });
  harness.page.emit('response', makeResponse(exactRequest, () => delayedBody));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(harness.report.nativeRequests.at(-1).status, 200);
  assert.equal(harness.report.nativeRequests.at(-1).completedAt, undefined);
  assert.equal(settled, false, 'HTTP 200 headers alone are not terminal evidence');

  resolveBody(JSON.stringify(appliedResponse));
  const result = await waiting;
  await harness.capture.flush();
  assert.equal(result.capabilities.status, 200);
  assert(Number.isFinite(result.capabilities.completedAt));
  assert.deepEqual(result.expected, {
    snapshotToken: appliedSnapshot, draftVersion: appliedDraftVersion,
    draftDigest: appliedDraftDigest, outputId: appliedOutputId, stageId: appliedStageId,
  });
  assert.equal(result.capabilities.requestId, harness.report.nativeRequests.at(-1).requestId);
});

test('applied source capabilities reject a wrong successor identity at the shared deadline', async () => {
  const harness = createCapture();
  const applyRequest = await emitApply(harness);
  const waiting = waitForAppliedSourceCapabilities(harness.capture, {
    applyRequest, fromIndex: 0, deadlineAt: Date.now() + 40,
    path: capabilitiesPath, outputId: appliedOutputId,
  });
  const wrongRequest = makeOwnedRequest(capabilitiesPath, {
    ...appliedIdentity, expectedDraftVersion: 8, expectedDraftDigest: 'other-draft', outputId: 'other-output',
  });
  harness.page.emit('request', wrongRequest);
  harness.page.emit('response', makeResponse(wrongRequest, async () => JSON.stringify({
    ...appliedResponse, draftVersion: 8, draftDigest: 'other-draft', outputId: 'other-output',
  })));
  await harness.capture.flush();
  await assert.rejects(waiting, /Timed out waiting for owned CDA request/);
});

test('Expand removal proof rejects an output mismatch', () => {
  const response = actualResponse();
  response.outputId = 'output-b';
  assert.throws(() => assertExpandFilterRemovalProposal(actualRequest(), response, expected), /saved output/);
});


test('final reload source capabilities wait for the exact saved identity through delayed body capture', async () => {
  const harness = createCapture();
  let settled = false;
  const waiting = waitForSourceCapabilities(harness.capture, {
    fromIndex: 0, deadlineAt: Date.now() + 1000, path: capabilitiesPath, expected: appliedExpectedIdentity,
  }).then(value => { settled = true; return value; });

  const wrongRequest = makeOwnedRequest(capabilitiesPath, {
    ...appliedIdentity, expectedDraftDigest: 'stale-draft',
  });
  harness.page.emit('request', wrongRequest);
  harness.page.emit('response', makeResponse(wrongRequest, async () => JSON.stringify({
    ...appliedResponse, draftDigest: 'stale-draft',
  })));
  await harness.capture.flush();
  assert.equal(settled, false, 'a stale reload capability cannot satisfy the cascade identity');

  const exactRequest = makeOwnedRequest(capabilitiesPath, appliedIdentity);
  harness.page.emit('request', exactRequest);
  await new Promise(resolve => setImmediate(resolve));
  let resolveBody;
  const delayedBody = new Promise(resolve => { resolveBody = resolve; });
  harness.page.emit('response', makeResponse(exactRequest, () => delayedBody));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(harness.report.nativeRequests.at(-1).status, 200);
  assert.equal(harness.report.nativeRequests.at(-1).completedAt, undefined);
  assert.equal(settled, false, 'reload waits for captured response body, not HTTP 200 headers');

  resolveBody(JSON.stringify(appliedResponse));
  const result = await waiting;
  assert.equal(result.status, 200);
  assert(Number.isFinite(result.completedAt));
  assert.equal(result.requestId, harness.report.nativeRequests.at(-1).requestId);
});

test('final reload source capabilities reject the wrong identity by the shared action deadline', async () => {
  const harness = createCapture();
  const waiting = waitForSourceCapabilities(harness.capture, {
    fromIndex: 0, deadlineAt: Date.now() + 40, path: capabilitiesPath, expected: appliedExpectedIdentity,
  });
  const wrongRequest = makeOwnedRequest(capabilitiesPath, {
    ...appliedIdentity, outputId: 'other-output',
  });
  harness.page.emit('request', wrongRequest);
  harness.page.emit('response', makeResponse(wrongRequest, async () => JSON.stringify({
    ...appliedResponse, outputId: 'other-output',
  })));
  await harness.capture.flush();
  await assert.rejects(waiting, /Timed out waiting for owned CDA request/);
});
