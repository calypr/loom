import assert from 'node:assert/strict';
import test from 'node:test';
import { isRecoveredStaleCapabilitiesConflict } from './loom-dev.mjs';

const capabilitiesURL = 'http://127.0.0.1:3380/api/v1/projects/test%252Frun/explorers/test-explorer/authoring/v2/construction-capabilities';
const commandsURL = capabilitiesURL.replace('/construction-capabilities', '/commands');
const outputId = 'out-patient-table';
const currentDraftVersion = 14;
const currentDraftDigest = 'sha256:current-draft';
const requestId = 'construction-capabilities-stale-request';

const evidence = () => ({
  failure: {
    status: 409,
    method: 'POST',
    url: capabilitiesURL,
    requestId,
    startedAt: 405,
    observedAt: 725,
    body: JSON.stringify({ error: { code: 'DRAFT_CONFLICT', requestId } }),
  },
  failedRequest: {
    requestId,
    status: 409,
    method: 'POST',
    url: capabilitiesURL,
    outputId,
    expectedDraftVersion: 13,
    expectedDraftDigest: 'sha256:previous-draft',
    startedAt: 405,
    observedAt: 725,
  },
  mutationResponse: {
    status: 200,
    method: 'POST',
    url: commandsURL,
    outputIds: [outputId],
    startedAt: 385,
    observedAt: 725,
  },
  recoveryRequest: {
    status: 200,
    method: 'POST',
    url: capabilitiesURL,
    outputId,
    expectedDraftVersion: currentDraftVersion,
    expectedDraftDigest: currentDraftDigest,
    startedAt: 749,
    observedAt: 2124,
  },
  expectedCapabilitiesURL: capabilitiesURL,
  expectedCommandsURL: commandsURL,
  outputId,
  currentDraftVersion,
  currentDraftDigest,
});

test('accepts only an overlapping stale capability read recovered for the saved draft', () => {
  assert.equal(isRecoveredStaleCapabilitiesConflict(evidence()), true);
});

test('does not classify conflicts without the exact owner, overlapping mutation, or current-draft recovery', () => {
  const differentOwner = evidence();
  differentOwner.failure.url = `${capabilitiesURL}-other-explorer`;
  assert.equal(isRecoveredStaleCapabilitiesConflict(differentOwner), false);

  const nonOverlappingMutation = evidence();
  nonOverlappingMutation.mutationResponse.startedAt = 500;
  assert.equal(isRecoveredStaleCapabilitiesConflict(nonOverlappingMutation), false);

  const staleRecovery = evidence();
  staleRecovery.recoveryRequest.expectedDraftDigest = 'sha256:previous-draft';
  assert.equal(isRecoveredStaleCapabilitiesConflict(staleRecovery), false);
});
