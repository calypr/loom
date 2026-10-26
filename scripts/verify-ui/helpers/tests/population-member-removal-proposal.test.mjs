import assert from 'node:assert/strict';
import test from 'node:test';
import { assertExactPreviewMultiset, assertPopulationMemberRemovalApplyCommand, assertPopulationMemberRemovalProposal, shouldCapturePopulationMemberNativeResponse } from '../population-member-removal-proposal.mjs';

const expected = {
  outputId: 'output-1',
  snapshotToken: 'snapshot-1',
  draftVersion: 8,
  draftDigest: 'sha256:draft',
  project: 'loom_dev_cda_fhir',
  generation: 'cda-fhir-v1',
  resourceType: 'Specimen',
  scopeDigest: 'scope-1',
  baseSelectionId: 'selection-base',
  baseMembershipDigest: 'members-base',
  baseMemberCount: 2,
  removedMember: {
    project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1', resourceType: 'Specimen', id: 'mapped-1',
  },
};

const proposal = () => ({
  proposalId: 'proposal-1', outputId: expected.outputId, snapshotToken: expected.snapshotToken,
  draftVersion: expected.draftVersion, draftDigest: expected.draftDigest,
  baseDocumentDigest: 'sha256:document', candidateWorkspaceDigest: 'sha256:candidate',
  removedMember: { ...expected.removedMember },
  baseSelection: {
    id: expected.baseSelectionId, project: expected.project, generation: expected.generation,
    resourceType: expected.resourceType, scopeDigest: expected.scopeDigest,
    membershipDigest: expected.baseMembershipDigest, memberCount: expected.baseMemberCount, complete: true,
  },
  candidateSelection: {
    id: 'selection-candidate', project: expected.project, generation: expected.generation,
    resourceType: expected.resourceType, scopeDigest: expected.scopeDigest,
    membershipDigest: 'members-candidate', memberCount: 1, complete: true,
    source: { kind: 'SELECTION_REVISION', revisionId: expected.baseSelectionId, membershipDigest: expected.baseMembershipDigest },
    exclusions: [{ ...expected.removedMember }],
  },
});

test('accepts only an exact base-minus-one immutable removal proposal', () => {
  const result = assertPopulationMemberRemovalProposal(proposal(), expected);
  assert.equal(result.id, 'selection-candidate');
});

test('rejects an unknown, stale, or substituted member proposal', () => {
  for (const mutate of [
    (value) => { value.proposalId = ''; },
    (value) => { value.outputId = 'other-output'; },
    (value) => { value.draftDigest = 'sha256:stale'; },
    (value) => { value.baseSelection.id = 'other-selection'; },
    (value) => { value.candidateSelection.memberCount = 2; },
    (value) => { value.candidateSelection.scopeDigest = 'other-scope'; },
    (value) => { value.candidateSelection.source.revisionId = 'other-selection'; },
    (value) => { value.candidateSelection.exclusions[0].id = 'other-member'; },
    (value) => { value.removedMember.project = 'other-project'; },
  ]) {
    const altered = proposal();
    mutate(altered);
    assert.throws(() => assertPopulationMemberRemovalProposal(altered, expected));
  }
});

test('accepts only the exact sole proposal Apply command', () => {
  const commandRequest = {
    path: '/api/v1/projects/p/explorers/e/authoring/v2/commands',
    method: 'POST',
    scopeProject: 'p',
    scopeExplorer: 'e',
    startedAt: 20,
    completedAt: 27,
    endedAt: 30,
    status: 200,
    body: { snapshotToken: 'snapshot-1', expectedDraftVersion: 8, expectedDraftDigest: 'sha256:draft',
      commands: [{ type: 'APPLY_POPULATION_MEMBER_PROPOSAL', outputId: 'output-1', proposalId: 'proposal-1' }] },
  };
  assert.equal(assertPopulationMemberRemovalApplyCommand([commandRequest], {
    path: commandRequest.path, startedAt: 10, endedAt: 40,
    snapshotToken: 'snapshot-1', draftVersion: 8, draftDigest: 'sha256:draft',
    outputId: 'output-1', proposalId: 'proposal-1', scopeProject: 'p', scopeExplorer: 'e',
  }), commandRequest);
  assert.throws(() => assertPopulationMemberRemovalApplyCommand([{
    ...commandRequest,
    body: { commands: [...commandRequest.body.commands, { type: 'RENAME_TABLE', outputId: 'output-1' }] },
  }], { path: commandRequest.path, startedAt: 10, endedAt: 40,
    snapshotToken: 'snapshot-1', draftVersion: 8, draftDigest: 'sha256:draft',
    outputId: 'output-1', proposalId: 'proposal-1', scopeProject: 'p', scopeExplorer: 'e' }));
  assert.throws(() => assertPopulationMemberRemovalApplyCommand([{
    ...commandRequest,
    body: { commands: [{ type: 'APPLY_POPULATION_MEMBER_PROPOSAL', outputId: 'output-1', proposalId: 'unknown' }] },
  }], { path: commandRequest.path, startedAt: 10, endedAt: 40,
    snapshotToken: 'snapshot-1', draftVersion: 8, draftDigest: 'sha256:draft',
    outputId: 'output-1', proposalId: 'proposal-1', scopeProject: 'p', scopeExplorer: 'e' }));
  for (const bodyChange of [
    { snapshotToken: 'stale-snapshot' },
    { expectedDraftVersion: 7 },
    { expectedDraftDigest: 'stale-digest' },
    { commands: [{ type: 'APPLY_POPULATION_MEMBER_PROPOSAL', outputId: 'other-output', proposalId: 'proposal-1' }] },
  ]) {
    assert.throws(() => assertPopulationMemberRemovalApplyCommand([{
      ...commandRequest, body: { ...commandRequest.body, ...bodyChange },
    }], { path: commandRequest.path, startedAt: 10, endedAt: 40,
      snapshotToken: 'snapshot-1', draftVersion: 8, draftDigest: 'sha256:draft',
      outputId: 'output-1', proposalId: 'proposal-1', scopeProject: 'p', scopeExplorer: 'e' }));
  }
  assert.throws(() => assertPopulationMemberRemovalApplyCommand([
    commandRequest,
    { ...commandRequest, startedAt: 25 },
  ], { path: commandRequest.path, startedAt: 10, endedAt: 40,
    snapshotToken: 'snapshot-1', draftVersion: 8, draftDigest: 'sha256:draft',
    outputId: 'output-1', proposalId: 'proposal-1', scopeProject: 'p', scopeExplorer: 'e' }));
  assert.throws(() => assertPopulationMemberRemovalApplyCommand([{
    ...commandRequest, scopeExplorer: 'other-explorer',
  }], { path: commandRequest.path, startedAt: 10, endedAt: 40,
    snapshotToken: 'snapshot-1', draftVersion: 8, draftDigest: 'sha256:draft',
    outputId: 'output-1', proposalId: 'proposal-1', scopeProject: 'p', scopeExplorer: 'e' }));
  assert.throws(() => assertPopulationMemberRemovalApplyCommand([{
    ...commandRequest, path: '/api/v1/projects/p/explorers/other/authoring/v2/commands', scopeExplorer: 'other',
  }], { path: commandRequest.path, startedAt: 10, endedAt: 40,
    snapshotToken: 'snapshot-1', draftVersion: 8, draftDigest: 'sha256:draft',
    outputId: 'output-1', proposalId: 'proposal-1', scopeProject: 'p', scopeExplorer: 'e' }));
});

test('compares complete row multiplicities independently of order', () => {
  assertExactPreviewMultiset([['b', '1'], ['a', '1'], ['a', '1']], [['a', '1'], ['b', '1'], ['a', '1']]);
  assert.throws(() => assertExactPreviewMultiset([['a', '1']], [['a', '1'], ['a', '1']]));
  assert.throws(() => assertExactPreviewMultiset([[undefined]], [[null]]));
});

test('captures both construction proposal endpoint families for the exact Explorer', () => {
  const scope = { project: 'p', explorer: 'e' };
  for (const endpoint of ['construction-proposals', 'construction-choice-proposals']) {
    assert.equal(shouldCapturePopulationMemberNativeResponse({
      scopeProject: 'p', scopeExplorer: 'e',
      path: `/api/v1/projects/p/explorers/e/authoring/v2/${endpoint}`,
    }, scope), true, `${endpoint} responses must be decoded before UI proposal checks`);
  }
  assert.equal(shouldCapturePopulationMemberNativeResponse({
    scopeProject: 'p', scopeExplorer: 'other', path: '/api/v1/projects/p/explorers/other/authoring/v2/construction-proposals',
  }, scope), false, 'the response reader must not consume another Explorer');
});
