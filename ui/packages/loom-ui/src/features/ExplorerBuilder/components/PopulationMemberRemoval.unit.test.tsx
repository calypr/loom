// @vitest-environment jsdom
import React from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import type { SelectionPage, SelectionRevision } from '../../../selection';
import { EXPLORER_AUTHORING_API_VERSION, type PopulationRouteChoice } from '../../../types';
import type { PopulationMemberRemovalProposalResponse } from '../../../api';
import type { DraftTable } from '../authoring/model';
import { PopulationPanel } from './PopulationPanel';

const loomClient = vi.hoisted(() => ({
  searchPopulationRoutes: vi.fn(),
  getSelection: vi.fn(),
  proposePopulationMemberRemoval: vi.fn(),
}));
vi.mock('../../../react', async (importOriginal) => ({
  ...await importOriginal<typeof import('../../../react')>(),
  useLoomClient: () => loomClient,
  usePopulationMappingMutation: () => [vi.fn(), { isLoading: false }],
}));

const selection: SelectionRevision = {
  id: 'selection-1', project: 'project', generation: 'generation', resourceType: 'DocumentReference',
  rule: { kind: 'EXPLICIT' }, source: { kind: 'EXPLICIT_REFS' }, scopeDigest: 'scope', ruleDigest: 'rule',
  membershipDigest: 'members-base', memberCount: 1, memberBytes: 20, complete: true,
  createdAt: '2026-10-04T00:00:00.000Z', completedAt: '2026-10-04T00:00:01.000Z',
};
const member = {
  memberKey: 'opaque-member-key-1', ordinal: 0,
  ref: { project: 'project', generation: 'generation', resourceType: 'DocumentReference', id: 'doc-1' },
} as const;
const page: SelectionPage = { revision: selection, members: [member] };
const candidateSelection: SelectionRevision = {
  ...selection, id: 'selection-2', membershipDigest: 'members-after', memberCount: 0, memberBytes: 0,
};
const preview = {
  apiVersion: EXPLORER_AUTHORING_API_VERSION,
  kind: 'ExplorerBuilderPreview' as const,
  receiptId: 'proposal-1', outputId: 'specimens',
  columns: [{ column: 'count', label: 'Observation count', logicalType: 'integer', filterable: false, chartable: false }],
  rows: [{ count: 3 }], rowLineageCapability: { status: 'UNAVAILABLE' as const, reasonCode: 'TEST_FIXTURE' },
  rowCount: 1, diagnostics: [],
};
const proposal: PopulationMemberRemovalProposalResponse = {
  proposalId: 'proposal-1', outputId: 'specimens', snapshotToken: 'snapshot',
  draftVersion: 4, draftDigest: 'draft-4', baseDocumentDigest: 'document-4',
  candidateWorkspaceDigest: 'candidate-5', baseSelection: selection,
  candidateSelection, removedMember: member.ref, previewStatus: 'READY', previewDurationMs: 12,
  preview,
};
const routeChoice: PopulationRouteChoice = {
  routeChoiceId: 'route-1', route: [], presentation: { summary: 'Selected resources', facts: [] },
};
const table: DraftTable = {
  outputId: 'specimens', tabId: 'tab', title: 'Specimens',
  document: {
    kind: 'ExplorerBuilderDocument', output: { id: 'specimens', title: 'Specimens' },
    rootResourceType: 'Specimen', route: { occurrenceId: 'base', resourceType: 'Specimen' },
    rows: { kind: 'RECORDS', records: {} }, columns: [],
    population: { selectionRevisionId: selection.id, route: [] },
  },
};

const renderPanel = (onApplyMemberRemoval = vi.fn().mockResolvedValue(true)) => {
  const view = render(
    <PopulationPanel
      table={table} selection={selection} loading={false} disabled={false}
      project="project" explorerId="explorer-1" authResourcePath="/programs/alpha"
      snapshotToken="snapshot" draftVersion={4} draftDigest="draft-4"
      onAttach={vi.fn()} onClear={vi.fn()} onApplyMemberRemoval={onApplyMemberRemoval}
    />,
  );
  return { ...view, onApplyMemberRemoval };
};

beforeEach(() => {
  loomClient.searchPopulationRoutes.mockReset().mockResolvedValue({
    snapshotToken: 'snapshot', outputId: 'specimens', selectionRevisionId: selection.id,
    complete: true, truncated: false, choices: [routeChoice],
  });
  loomClient.getSelection.mockReset().mockResolvedValue(page);
  loomClient.proposePopulationMemberRemoval.mockReset().mockResolvedValue(proposal);
});

it('automatically previews one exact member, then Cancel leaves the attached selection unchanged', async () => {
  renderPanel();
  fireEvent.click(await screen.findByRole('button', { name: 'Review removal of DocumentReference/doc-1' }));
  const region = await screen.findByRole('region', { name: 'Member removal preview' });
  expect(within(region).getByText('Remove DocumentReference/doc-1')).toBeInTheDocument();
  expect(within(region).getByRole('table', { name: 'Candidate table after member removal' })).toBeInTheDocument();
  expect(within(region).getByText('3')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /^Preview/ })).not.toBeInTheDocument();
  expect(loomClient.proposePopulationMemberRemoval).toHaveBeenCalledWith(expect.objectContaining({
    project: 'project', explorerId: 'explorer-1', authResourcePath: '/programs/alpha',
    snapshotToken: 'snapshot', expectedDraftVersion: 4, expectedDraftDigest: 'draft-4',
    outputId: 'specimens', baseSelectionRevisionId: selection.id, removedMember: member.ref,
    limit: 25,
  }), expect.any(AbortSignal));
  expect(region).toHaveAttribute('data-proposal-id', 'proposal-1');
  expect(region).toHaveAttribute('data-preview-status', 'READY');
  fireEvent.click(within(region).getByRole('button', { name: 'Cancel member removal' }));
  expect(screen.queryByRole('region', { name: 'Member removal preview' })).not.toBeInTheDocument();
  expect(screen.getByRole('region', { name: 'Starting collection' })).toHaveAttribute(
    'data-attached-selection-revision-id', selection.id,
  );
  expect(proposal.proposalId).toBe('proposal-1');
});

it('applies only the READY proposal ID through the workspace callback', async () => {
  const { onApplyMemberRemoval } = renderPanel();
  fireEvent.click(await screen.findByRole('button', { name: 'Review removal of DocumentReference/doc-1' }));
  const region = await screen.findByRole('region', { name: 'Member removal preview' });
  fireEvent.click(within(region).getByRole('button', { name: 'Apply member removal' }));
  await waitFor(() => expect(onApplyMemberRemoval).toHaveBeenCalledWith('proposal-1'));
  await waitFor(() => expect(screen.queryByRole('region', { name: 'Member removal preview' })).not.toBeInTheDocument());
});

it('fails closed when the automatic preview belongs to another receipt', async () => {
  loomClient.proposePopulationMemberRemoval.mockResolvedValueOnce({
    ...proposal, preview: { ...preview, receiptId: 'another-receipt' },
  });
  const { onApplyMemberRemoval } = renderPanel();
  fireEvent.click(await screen.findByRole('button', { name: 'Review removal of DocumentReference/doc-1' }));
  const region = await screen.findByRole('region', { name: 'Member removal preview' });
  expect(await within(region).findByRole('alert')).toHaveTextContent(/does not match the current table/);
  expect(within(region).getByRole('button', { name: 'Apply member removal' })).toBeDisabled();
  expect(onApplyMemberRemoval).not.toHaveBeenCalled();
});

it('rejects a selection page with a forged project or member scope before offering removal', async () => {
  loomClient.getSelection.mockResolvedValueOnce({
    revision: selection,
    members: [{ ...member, ref: { ...member.ref, project: 'foreign-project' } }],
  });
  renderPanel();
  expect(await screen.findByRole('alert')).toHaveTextContent(/duplicate or out-of-scope member/);
  expect(screen.queryByRole('button', { name: 'Review removal of DocumentReference/doc-1' })).not.toBeInTheDocument();
  expect(loomClient.proposePopulationMemberRemoval).not.toHaveBeenCalled();
});

it('locks controls during Apply and ignores the old completion after the authoring scope changes', async () => {
  let resolveApply!: (applied: boolean) => void;
  const pendingApply = new Promise<boolean>((resolve) => { resolveApply = resolve; });
  const onApplyMemberRemoval = vi.fn(() => pendingApply);
  const { rerender } = renderPanel(onApplyMemberRemoval);
  fireEvent.click(await screen.findByRole('button', { name: 'Review removal of DocumentReference/doc-1' }));
  const firstRegion = await screen.findByRole('region', { name: 'Member removal preview' });
  fireEvent.click(within(firstRegion).getByRole('button', { name: 'Apply member removal' }));
  expect(within(firstRegion).getByRole('button', { name: 'Cancel member removal' })).toBeDisabled();
  expect(screen.getByRole('button', { name: 'Review removal of DocumentReference/doc-1' })).toBeDisabled();

  const nextSelection: SelectionRevision = {
    ...selection, id: 'selection-next', membershipDigest: 'members-next', memberCount: 1,
  };
  const nextMember = {
    memberKey: 'opaque-member-key-next', ordinal: 0,
    ref: { ...member.ref, id: 'doc-next' },
  } as const;
  const nextCandidate: SelectionRevision = {
    ...nextSelection, id: 'selection-next-candidate', membershipDigest: 'members-next-candidate', memberCount: 0,
  };
  const nextProposal: PopulationMemberRemovalProposalResponse = {
    ...proposal, proposalId: 'proposal-next', snapshotToken: 'snapshot-next', draftVersion: 5,
    draftDigest: 'draft-5', baseSelection: nextSelection, candidateSelection: nextCandidate,
    removedMember: nextMember.ref, preview: { ...preview, receiptId: 'proposal-next' },
  };
  loomClient.getSelection.mockResolvedValueOnce({ revision: nextSelection, members: [nextMember] });
  loomClient.proposePopulationMemberRemoval.mockResolvedValueOnce(nextProposal);
  rerender(
    <PopulationPanel
      table={{ ...table, document: { ...table.document, population: { selectionRevisionId: nextSelection.id, route: [] } } }}
      selection={nextSelection} loading={false} disabled={false}
      project="project" explorerId="explorer-1" authResourcePath="/programs/alpha"
      snapshotToken="snapshot-next" draftVersion={5} draftDigest="draft-5"
      onAttach={vi.fn()} onClear={vi.fn()} onApplyMemberRemoval={onApplyMemberRemoval}
    />,
  );
  fireEvent.click(await screen.findByRole('button', { name: 'Review removal of DocumentReference/doc-next' }));
  const nextRegion = await screen.findByRole('region', { name: 'Member removal preview' });
  await waitFor(() => expect(nextRegion).toHaveAttribute('data-proposal-id', 'proposal-next'));

  await act(async () => { resolveApply(true); });
  expect(screen.getByRole('region', { name: 'Member removal preview' })).toHaveAttribute('data-proposal-id', 'proposal-next');
});

it('rejects a candidate header that is not marked complete', async () => {
  loomClient.proposePopulationMemberRemoval.mockResolvedValueOnce({
    ...proposal,
    candidateSelection: { ...candidateSelection, complete: false },
  });
  renderPanel();
  fireEvent.click(await screen.findByRole('button', { name: 'Review removal of DocumentReference/doc-1' }));
  const region = await screen.findByRole('region', { name: 'Member removal preview' });
  expect(await within(region).findByRole('alert')).toHaveTextContent(/does not match the current table/);
  expect(within(region).getByRole('button', { name: 'Apply member removal' })).toBeDisabled();
});

it('finishes a retired member read without showing its stale collection', async () => {
  let resolveOldPage!: (value: SelectionPage) => void;
  const pendingPage = new Promise<SelectionPage>((resolve) => { resolveOldPage = resolve; });
  loomClient.getSelection.mockReturnValueOnce(pendingPage);
  const { rerender } = renderPanel();
  await waitFor(() => expect(loomClient.getSelection).toHaveBeenCalledTimes(1));
  expect(loomClient.getSelection.mock.calls[0]).toHaveLength(1);
  const nextSelection = { ...selection, id: 'selection-next', membershipDigest: 'members-next' };
  const nextMember = { ...member, memberKey: 'next-member', ref: { ...member.ref, id: 'doc-next' } };
  loomClient.getSelection.mockResolvedValueOnce({ revision: nextSelection, members: [nextMember] });
  rerender(
    <PopulationPanel
      table={{ ...table, document: { ...table.document, population: { selectionRevisionId: nextSelection.id, route: [] } } }}
      selection={nextSelection} loading={false} disabled={false}
      project="project" explorerId="explorer-1" authResourcePath="/programs/alpha"
      snapshotToken="snapshot" draftVersion={5} draftDigest="draft-5"
      onAttach={vi.fn()} onClear={vi.fn()} onApplyMemberRemoval={vi.fn()}
    />,
  );
  expect(await screen.findByRole('button', { name: 'Review removal of DocumentReference/doc-next' })).toBeEnabled();
  await act(async () => { resolveOldPage(page); await pendingPage; });
  expect(screen.queryByRole('button', { name: 'Review removal of DocumentReference/doc-1' })).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Review removal of DocumentReference/doc-next' })).toBeEnabled();
  expect(loomClient.getSelection.mock.calls.every((call) => call.length === 1)).toBe(true);
});
