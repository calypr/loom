// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { RowDefinitionChoicesResponse, RowDefinitionProposal } from '../../../types';
import type { DraftTable } from '../authoring/model';
import { RowDefinitionSettingsPanel } from './RowDefinitionSettingsPanel';

const choices: RowDefinitionChoicesResponse = {
  snapshotToken: 'snapshot-1',
  outputId: 'patients',
  choices: [{
    choiceId: 'expanded-choice',
    label: 'Patient.name[]',
    description: '',
    occurrenceSummary: 'Root occurrence',
    routeSummary: 'Root',
    kind: 'EXPANDED',
    valueType: 'ARRAY',
    policies: [{ name: 'emptyCollectionPolicy', options: ['EXCLUDE', 'PRESERVE_PARENT'] }],
  }],
  explicitGroups: [{
    revisionId: 'grouprev_0123456789abcdef',
    groupCount: 2,
    memberCount: 3,
    createdAt: '2026-09-20T00:00:00.000Z',
    unassignedMemberPolicies: ['ERROR', 'EXCLUDE', 'GROUP_AS_UNASSIGNED'],
  }],
};

const proposal: RowDefinitionProposal = {
  proposalId: 'proposal-receipt-1',
  baseReceiptId: 'base-receipt-1',
  outputId: 'patients',
  snapshotToken: 'snapshot-1',
  draftVersion: 4,
  draftDigest: 'draft-digest-4',
  baseDocumentDigest: 'document-digest-4',
  candidateWorkspaceDigest: 'candidate-digest-5',
  mode: 'EXPLICIT_GROUP',
  comparison: {
    status: 'AVAILABLE',
    base: { rowCount: 4, sampled: false },
    candidate: { rowCount: 2, sampled: false },
    affectedColumns: ['patient_id'],
    notices: [],
    examples: [
      { rowIdentity: 'grouprev_0123456789abcdef:group-a', basePresent: false, candidatePresent: true },
      { rowIdentity: 'patient-4', basePresent: true, candidatePresent: false },
    ],
  },
};

const table: DraftTable = {
  outputId: 'patients',
  tabId: 'patients',
  title: 'Patients',
  document: {
    kind: 'ExplorerBuilderDocument',
    output: { id: 'patients', title: 'Patients' },
    rootResourceType: 'Patient',
    route: { occurrenceId: 'base', resourceType: 'Patient' },
    rows: { kind: 'RECORDS', records: {} },
    columns: [],
  },
};

const renderSettings = (overrides: { draftVersion?: number; draftDigest?: string; proposalValue?: RowDefinitionProposal } = {}) => {
  const listRowDefinitionChoices = vi.fn().mockResolvedValue(choices);
  const proposeRowDefinition = vi.fn().mockResolvedValue(overrides.proposalValue ?? proposal);
  const onApply = vi.fn().mockResolvedValue(true);
  const view = render(
    <RowDefinitionSettingsPanel
      client={{ listRowDefinitionChoices, proposeRowDefinition }}
      project="project-a"
      explorerId="explorer-a"
      snapshotToken="snapshot-1"
      draftVersion={overrides.draftVersion ?? 4}
      draftDigest={overrides.draftDigest ?? 'draft-digest-4'}
      table={table}
      disabled={false}
      onApply={onApply}
    />,
  );
  return { ...view, listRowDefinitionChoices, proposeRowDefinition, onApply };
};

afterEach(cleanup);

describe('RowDefinitionSettingsPanel', () => {
  it('previews and applies only the server-issued explicit-group proposal', async () => {
    const { listRowDefinitionChoices, proposeRowDefinition, onApply } = renderSettings();
    expect(screen.getByText('Current rows: RECORDS · one row per source record')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    const select = await screen.findByRole('combobox', { name: 'New row definition' });
    expect(screen.getByRole('option', { name: /RECORDS/ })).toBeTruthy();
    expect(screen.getAllByRole('option', { name: /EXPANDED/ })).toHaveLength(2);
    expect(screen.getAllByRole('option', { name: /Explicit group/ })).toHaveLength(3);
    expect(listRowDefinitionChoices).toHaveBeenCalledWith(expect.objectContaining({
      project: 'project-a', explorerId: 'explorer-a', outputId: 'patients', snapshotToken: 'snapshot-1',
    }));

    fireEvent.change(select, { target: { value: 'explicit:grouprev_0123456789abcdef:EXCLUDE' } });
    fireEvent.click(screen.getByRole('button', { name: 'Preview row change' }));
    expect(await screen.findByText('Base rows: 4')).toBeTruthy();
    expect(screen.getByText('Candidate rows: 2')).toBeTruthy();
    expect(screen.getByText('Added · grouprev_0123456789abcdef:group-a')).toBeTruthy();
    expect(screen.getByText('Removed · patient-4')).toBeTruthy();
    expect(proposeRowDefinition).toHaveBeenCalledWith(expect.objectContaining({
      outputId: 'patients',
      expectedDraftVersion: 4,
      expectedDraftDigest: 'draft-digest-4',
      selection: { kind: 'EXPLICIT_GROUP', explicitGroup: { revisionId: 'grouprev_0123456789abcdef', unassignedMemberPolicy: 'EXCLUDE' } },
    }));
    expect(onApply).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Apply row definition' }));
    await waitFor(() => expect(onApply).toHaveBeenCalledWith('proposal-receipt-1'));
  });

  it('expires a proposal when the saved draft changes', async () => {
    const listRowDefinitionChoices = vi.fn().mockResolvedValue(choices);
    const proposeRowDefinition = vi.fn().mockResolvedValue(proposal);
    const onApply = vi.fn().mockResolvedValue(true);
    const props = {
      client: { listRowDefinitionChoices, proposeRowDefinition },
      project: 'project-a', explorerId: 'explorer-a', snapshotToken: 'snapshot-1',
      draftVersion: 4, draftDigest: 'draft-digest-4', table, disabled: false, onApply,
    };
    const view = render(<RowDefinitionSettingsPanel {...props} />);
    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    await screen.findByRole('combobox', { name: 'New row definition' });
    fireEvent.click(screen.getByRole('button', { name: 'Preview row change' }));
    await screen.findByRole('button', { name: 'Apply row definition' });
    view.rerender(<RowDefinitionSettingsPanel {...props} draftVersion={5} draftDigest="draft-digest-5" />);
    expect(await screen.findByRole('alert')).toHaveTextContent(/proposal is stale/i);
    expect(screen.queryByRole('button', { name: 'Apply row definition' })).toBeNull();
    expect(onApply).not.toHaveBeenCalled();
  });

  it('shows the server reason when preview is unavailable and cancel makes no draft change', async () => {
    const unavailable: RowDefinitionProposal = {
      ...proposal,
      proposalId: undefined,
      comparison: {
        status: 'UNAVAILABLE',
        reasonCode: 'PREVIEW_UNAVAILABLE',
        reason: 'The server could not compare this row definition.',
        base: { rowCount: 4, sampled: false },
        affectedColumns: [],
        notices: [],
        examples: [],
      },
    };
    const { proposeRowDefinition, onApply } = renderSettings({ proposalValue: unavailable });
    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    await screen.findByRole('combobox', { name: 'New row definition' });
    fireEvent.click(screen.getByRole('button', { name: 'Preview row change' }));
    expect((await screen.findAllByText('The server could not compare this row definition.')).length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(proposeRowDefinition).toHaveBeenCalledTimes(1);
    expect(onApply).not.toHaveBeenCalled();
  });

  it('shows a server rejection reason when the selected row definition cannot be previewed', async () => {
    const serverError = Object.assign(new Error('This explicit group revision is no longer available for this table.'), { status: 409 });
    const listRowDefinitionChoices = vi.fn().mockResolvedValue(choices);
    const proposeRowDefinition = vi.fn().mockRejectedValue(serverError);
    const onApply = vi.fn().mockResolvedValue(true);
    render(
      <RowDefinitionSettingsPanel
        client={{ listRowDefinitionChoices, proposeRowDefinition }}
        project="project-a"
        explorerId="explorer-a"
        snapshotToken="snapshot-1"
        draftVersion={4}
        draftDigest="draft-digest-4"
        table={table}
        disabled={false}
        onApply={onApply}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    await screen.findByRole('combobox', { name: 'New row definition' });
    fireEvent.click(screen.getByRole('button', { name: 'Preview row change' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(serverError.message);
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(onApply).not.toHaveBeenCalled();
  });
});
