import assert from 'node:assert/strict';
import { isDeepStrictEqual } from 'node:util';

const exactRef = (value) => ({
  project: value?.project,
  generation: value?.generation,
  resourceType: value?.resourceType,
  id: value?.id,
});

/** The verifier decodes every response belonging to its exact project and Explorer. */
export const shouldCapturePopulationMemberNativeResponse = (request, { project, explorer }) => {
  if (!request || typeof request.path !== 'string') return false;
  return request.scopeProject === project && request.scopeExplorer === explorer &&
    request.path.startsWith(`/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/`);
};

/** Compare complete preview populations without assuming the backend's row order. */
export const assertExactPreviewMultiset = (actual, expected, label = 'preview rows') => {
  assert(Array.isArray(actual) && Array.isArray(expected), `${label} must be arrays`);
  const unmatched = [...expected];
  for (const row of actual) {
    assert(Array.isArray(row), `${label} entries must be rows`);
    const index = unmatched.findIndex(candidate => isDeepStrictEqual(candidate, row));
    assert(index >= 0, `${label} contains an unexpected row: ${String(row)}`);
    unmatched.splice(index, 1);
  }
  assert.equal(unmatched.length, 0, `${label} is missing rows or multiplicity: ${String(unmatched)}`);
};

/** Assert that a native response is the exact attached-selection minus one member proposal. */
export const assertPopulationMemberRemovalProposal = (proposal, expected) => {
  assert(proposal && typeof proposal === 'object', 'member-removal proposal response is required');
  for (const key of ['proposalId', 'baseDocumentDigest', 'candidateWorkspaceDigest']) {
    assert.equal(typeof proposal[key], 'string', `${key} must be a string`);
    assert(proposal[key].length > 0, `${key} must be nonempty`);
  }
  assert.equal(proposal.outputId, expected.outputId, 'proposal output must match the selected table');
  assert.equal(proposal.snapshotToken, expected.snapshotToken, 'proposal must be bound to the current snapshot');
  assert.equal(proposal.draftVersion, expected.draftVersion, 'proposal must be bound to the current draft version');
  assert.equal(proposal.draftDigest, expected.draftDigest, 'proposal must be bound to the current draft digest');
  assert.deepEqual(exactRef(proposal.removedMember), exactRef(expected.removedMember), 'proposal must remove the exact clicked scoped member');

  const base = proposal.baseSelection;
  const candidate = proposal.candidateSelection;
  assert(base && candidate, 'proposal must return both immutable selection headers');
  for (const selection of [base, candidate]) {
    assert.equal(selection.complete, true, 'selection header must be complete');
    assert.equal(selection.project, expected.project, 'selection project must match the active project');
    assert.equal(selection.generation, expected.generation, 'selection generation must match the active dataset');
    assert.equal(selection.resourceType, expected.resourceType, 'selection resource type must match the attached collection');
    assert.equal(selection.scopeDigest, expected.scopeDigest, 'selection scope digest must match the active authorization');
  }
  assert.equal(base.id, expected.baseSelectionId, 'proposal base must be the currently attached immutable selection');
  assert.equal(base.membershipDigest, expected.baseMembershipDigest, 'proposal base membership digest must match the pinned selection');
  assert.equal(base.memberCount, expected.baseMemberCount, 'proposal base member count must match the pinned selection');
  assert.notEqual(candidate.id, base.id, 'removal must create a distinct immutable selection revision');
  assert.notEqual(candidate.membershipDigest, base.membershipDigest, 'removal must create a distinct membership digest');
  assert.equal(candidate.memberCount, base.memberCount - 1, 'candidate membership must remove exactly one selected member');
  assert.equal(candidate.source?.kind, 'SELECTION_REVISION', 'candidate must derive from an immutable selection revision');
  assert.equal(candidate.source?.revisionId, base.id, 'candidate provenance must point to the exact base revision');
  assert.equal(candidate.source?.membershipDigest, base.membershipDigest, 'candidate provenance must pin the exact base membership');
  assert.deepEqual(candidate.exclusions?.map(exactRef), [exactRef(expected.removedMember)], 'candidate must exclude exactly the clicked member');
  return candidate;
};

/** Assert that one native authoring request applies only the current removal receipt. */
export const assertPopulationMemberRemovalApplyCommand = (requests, expected) => {
  assert(Array.isArray(requests), 'native requests must be an array');
  assert(typeof expected.path === 'string' && expected.path.length > 0, 'expected scoped commands path is required');
  assert(Number.isFinite(expected.startedAt) && Number.isFinite(expected.endedAt) && expected.startedAt <= expected.endedAt,
    'exact Apply request time window is required');
  const inWindow = requests.filter(request => /\/authoring\/v2\/commands$/.test(request.path ?? '') &&
    request.startedAt >= expected.startedAt && request.startedAt <= expected.endedAt);
  assert.equal(inWindow.length, 1, 'the exact Apply window must contain only one scoped authoring command request');
  const request = inWindow[0];
  assert.equal(request.path, expected.path, 'Apply must use the exact project and Explorer authoring path');
  assert.equal(request.method, 'POST', 'Apply must be a POST command request');
  if (expected.scopeProject !== undefined) assert.equal(request.scopeProject, expected.scopeProject, 'Apply project path must match the active project');
  if (expected.scopeExplorer !== undefined) assert.equal(request.scopeExplorer, expected.scopeExplorer, 'Apply Explorer path must match the active Explorer');
  assert.equal(request.status, 200, 'Apply command request must succeed');
  assert(Number.isFinite(request.completedAt) && request.completedAt <= expected.endedAt, 'Apply response must complete inside the measured window');
  assert.equal(request.body?.snapshotToken, expected.snapshotToken, 'Apply must use the current snapshot token');
  assert.equal(request.body?.expectedDraftVersion, expected.draftVersion, 'Apply must use the exact proposal draft version');
  assert.equal(request.body?.expectedDraftDigest, expected.draftDigest, 'Apply must use the exact proposal draft digest');
  assert.deepEqual(request.body?.commands, [{
    type: 'APPLY_POPULATION_MEMBER_PROPOSAL',
    outputId: expected.outputId,
    proposalId: expected.proposalId,
  }], 'member Apply must contain only the exact proposal command');
  return request;
};
