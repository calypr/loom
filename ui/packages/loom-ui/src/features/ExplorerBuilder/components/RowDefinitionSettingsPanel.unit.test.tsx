// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { RowDefinitionChoicesResponse, RowDefinitionProposal } from '../../../types';
import type { SelectionRevision } from '../../../selection';
import type { DraftTable } from '../authoring/model';
import { RowDefinitionSettingsPanel } from './RowDefinitionSettingsPanel';

const choices: RowDefinitionChoicesResponse = {
  snapshotToken: 'snapshot-1',
  outputId: 'patients',
  choices: [{
    choiceId: 'expanded-choice',
    fieldPath: 'name[]',
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

const sourceSelection: SelectionRevision = {
  id: 'selection-source-1',
  project: 'project-a',
  generation: 'generation-1',
  resourceType: 'Patient',
  rule: { kind: 'EXPLICIT' },
  source: { kind: 'EXPLICIT_REFS' },
  scopeDigest: 'scope-1',
  ruleDigest: 'rule-1',
  membershipDigest: 'membership-1',
  memberCount: 3,
  memberBytes: 3,
  complete: true,
  createdAt: '2026-09-20T00:00:00.000Z',
};

const relatedRowProps = {
  currentRowMeaning: 'One row per source record',
  relatedRows: { supported: false, reason: 'No executable route' },
  onChooseRelatedRows: vi.fn(),
};

const selectionPage = {
  revision: sourceSelection,
  members: [
    { ref: { project: 'project-a', generation: 'generation-1', resourceType: 'Patient', id: 'record-a' }, memberKey: 'opaque-member-a' },
    { ref: { project: 'project-a', generation: 'generation-1', resourceType: 'Patient', id: 'record-b' }, memberKey: 'opaque-member-b' },
    { ref: { project: 'project-a', generation: 'generation-1', resourceType: 'Patient', id: 'record-c' }, memberKey: 'opaque-member-c' },
  ],
};

const renderSettings = (overrides: {
  draftVersion?: number;
  draftDigest?: string;
  proposalValue?: RowDefinitionProposal;
  choicesValue?: RowDefinitionChoicesResponse;
  relatedRowsSupported?: boolean;
} = {}) => {
  const listRowDefinitionChoices = vi.fn().mockResolvedValue(overrides.choicesValue ?? choices);
  const proposeRowDefinition = vi.fn().mockResolvedValue(overrides.proposalValue ?? proposal);
  const getSelection = vi.fn().mockResolvedValue(selectionPage);
  const createExplicitGroupRevision = vi.fn();
  const onApply = vi.fn().mockResolvedValue(true);
  const onChooseRelatedRows = vi.fn();
  const view = render(
    <RowDefinitionSettingsPanel
      {...relatedRowProps}
      relatedRows={{ supported: overrides.relatedRowsSupported ?? false, reason: 'No executable route' }}
      onChooseRelatedRows={onChooseRelatedRows}
      client={{ listRowDefinitionChoices, proposeRowDefinition, getSelection, createExplicitGroupRevision }}
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
  return { ...view, listRowDefinitionChoices, proposeRowDefinition, getSelection, createExplicitGroupRevision, onApply, onChooseRelatedRows };
};

afterEach(cleanup);

describe('RowDefinitionSettingsPanel', () => {
  it('opens executable related rows from the row decision card', () => {
    const { onChooseRelatedRows } = renderSettings({ relatedRowsSupported: true });
    fireEvent.click(screen.getByRole('button', { name: 'One row per related record' }));
    expect(onChooseRelatedRows).toHaveBeenCalledOnce();
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('explains when the backend has no executable related row path', () => {
    const { onChooseRelatedRows } = renderSettings();
    expect(screen.getByRole('button', { name: 'One row per related record' })).toBeDisabled();
    expect(screen.getByText('No executable route')).toBeInTheDocument();
    expect(onChooseRelatedRows).not.toHaveBeenCalled();
  });

  it('defaults repeated values to preserving unmatched records and allows an explicit policy change', async () => {
    const { proposeRowDefinition } = renderSettings();
    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    const shape = await screen.findByRole('combobox', { name: 'New row shape' });
    fireEvent.change(shape, { target: { value: 'expanded:expanded-choice' } });
    expect(screen.getByText(/^When a source record has no matching values: Keep records with no values as one empty row$/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Preview row change' }));
    await waitFor(() => expect(proposeRowDefinition).toHaveBeenCalledWith(expect.objectContaining({
      selection: { kind: 'EXPANDED', expanded: { rowChoiceId: 'expanded-choice', emptyCollectionPolicy: 'PRESERVE_PARENT' } },
    })));
    fireEvent.change(screen.getByRole('combobox', { name: 'Unmatched record policy' }), {
      target: { value: 'expanded:expanded-choice:EXCLUDE' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Preview row change' }));
    await waitFor(() => expect(proposeRowDefinition).toHaveBeenLastCalledWith(expect.objectContaining({
      selection: { kind: 'EXPANDED', expanded: { rowChoiceId: 'expanded-choice', emptyCollectionPolicy: 'EXCLUDE' } },
    })));
  });

  it('explains that field grouping belongs in Reshape while its direct row proposal is unavailable', async () => {
    renderSettings({ choicesValue: {
      ...choices,
      choices: [...choices.choices, {
        choiceId: 'field-group-choice', fieldPath: 'status', label: 'Status', description: '',
        occurrenceSummary: 'Root occurrence', routeSummary: 'Root', kind: 'FIELD_GROUP', valueType: 'STRING',
        policies: [{ name: 'missingKeyPolicy', options: ['ERROR', 'EXCLUDE', 'GROUP_AS_MISSING'] }],
      }],
    } });
    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    await screen.findByRole('combobox', { name: 'New row shape' });
    expect(screen.getByText(/To make one row per distinct field value, use Reshape/)).toBeTruthy();
    expect(screen.queryByRole('option', { name: /Status/ })).toBeNull();
  });

  it('previews and applies only the server-issued explicit-group proposal', async () => {
    const { listRowDefinitionChoices, proposeRowDefinition, onApply } = renderSettings();
    expect(screen.getByText('Current table rows: One row per source record')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    const select = await screen.findByRole('combobox', { name: 'New row shape' });
    expect(screen.getByRole('option', { name: 'One row per source record' })).toBeTruthy();
    expect(screen.getAllByRole('option', { name: 'One row per value in Name' })).toHaveLength(1);
    expect(screen.getAllByRole('option', { name: /One row per saved group/ })).toHaveLength(1);
    expect(listRowDefinitionChoices).toHaveBeenCalledWith(expect.objectContaining({
      project: 'project-a', explorerId: 'explorer-a', outputId: 'patients', snapshotToken: 'snapshot-1',
    }));

    fireEvent.change(select, { target: { value: 'explicit:grouprev_0123456789abcdef' } });
    expect(screen.getByText(/^When a source record has no matching values: Put records without a group in their own group$/)).toBeTruthy();
    fireEvent.change(screen.getByRole('combobox', { name: 'Unmatched record policy' }), {
      target: { value: 'explicit:grouprev_0123456789abcdef:EXCLUDE' },
    });
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

  it('distinguishes repeated field choices with readable path breadcrumbs', async () => {
    const ambiguousChoices: RowDefinitionChoicesResponse = {
      ...choices,
      choices: [
        { ...choices.choices[0]!, choiceId: 'component-choice', label: 'Code defined by a terminology system', fieldPath: 'component[].code.coding[]' },
        { ...choices.choices[0]!, choiceId: 'category-choice', label: 'Code defined by a terminology system', fieldPath: 'category[].coding[]' },
      ],
    };
    renderSettings({ choicesValue: ambiguousChoices });
    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    await screen.findByRole('combobox', { name: 'New row shape' });

    expect(screen.getAllByRole('option', { name: 'One row per value in Component → Code → Coding' })).toHaveLength(1);
    expect(screen.getAllByRole('option', { name: 'One row per value in Category → Coding' })).toHaveLength(1);
  });

  it('shows exact paths only when different fields have the same readable breadcrumb', async () => {
    renderSettings({ choicesValue: {
      ...choices,
      choices: [
        { ...choices.choices[0]!, choiceId: 'nested-component', fieldPath: 'component[].code.coding[]' },
        { ...choices.choices[0]!, choiceId: 'direct-component', fieldPath: 'component.code.coding[]' },
      ],
    } });
    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    await screen.findByRole('combobox', { name: 'New row shape' });

    expect(screen.getByRole('option', { name: 'One row per value in Component → Code → Coding (component[].code.coding[])' })).toBeTruthy();
    expect(screen.getByRole('option', { name: 'One row per value in Component → Code → Coding (component.code.coding[])' })).toBeTruthy();
  });

  it('distinguishes the same path on different route occurrences', async () => {
    const repeatedPathChoices: RowDefinitionChoicesResponse = {
      ...choices,
      choices: [
        { ...choices.choices[0]!, choiceId: 'first-route', fieldPath: 'component[].code.coding[]', occurrenceSummary: 'Occurrence first via Subject' },
        { ...choices.choices[0]!, choiceId: 'second-route', fieldPath: 'component[].code.coding[]', occurrenceSummary: 'Occurrence second via Focus' },
      ],
    };
    renderSettings({ choicesValue: repeatedPathChoices });
    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    await screen.findByRole('combobox', { name: 'New row shape' });

    expect(screen.getAllByRole('option', { name: /Component → Code → Coding.*Occurrence first via Subject/ })).toHaveLength(1);
    expect(screen.getAllByRole('option', { name: /Component → Code → Coding.*Occurrence second via Focus/ })).toHaveLength(1);
  });

  it('authors exact overlapping groups from the owned selection before previewing and applying the receipt-backed proposal', async () => {
    const revisionID = 'grouprev_new-revision';
    const createdChoices: RowDefinitionChoicesResponse = {
      ...choices,
      explicitGroups: [...choices.explicitGroups, {
        revisionId: revisionID,
        groupCount: 2,
        memberCount: 4,
        createdAt: '2026-09-20T01:00:00.000Z',
        unassignedMemberPolicies: ['ERROR', 'EXCLUDE', 'GROUP_AS_UNASSIGNED'],
      }],
    };
    const listRowDefinitionChoices = vi.fn()
      .mockResolvedValueOnce(choices)
      .mockResolvedValueOnce(createdChoices);
    const proposeRowDefinition = vi.fn().mockResolvedValue(proposal);
    const getSelection = vi.fn().mockResolvedValue(selectionPage);
    const createExplicitGroupRevision = vi.fn().mockImplementation(async (args: {
      readonly selectionRevision: string;
      readonly groups: ReadonlyArray<{ readonly id: string; readonly label: string; readonly ordinal: number; readonly memberIds: ReadonlyArray<string> }>;
    }) => ({
      revisionId: revisionID,
      sourceSelectionRevisionId: args.selectionRevision,
      groupCount: args.groups.length,
      memberCount: args.groups.reduce((count, group) => count + group.memberIds.length, 0),
      createdAt: '2026-09-20T01:00:00.000Z',
      groups: args.groups.map((group) => ({ ...group, memberCount: group.memberIds.length })),
    }));
    const onApply = vi.fn().mockResolvedValue(true);
    render(
      <RowDefinitionSettingsPanel
        {...relatedRowProps}
        client={{ listRowDefinitionChoices, proposeRowDefinition, getSelection, createExplicitGroupRevision }}
        project="project-a"
        explorerId="explorer-a"
        snapshotToken="snapshot-1"
        draftVersion={4}
        draftDigest="draft-digest-4"
        table={table}
        selection={sourceSelection}
        disabled={false}
        onApply={onApply}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    await screen.findByRole('combobox', { name: 'New row shape' });
    fireEvent.click(screen.getByRole('button', { name: 'Create groups from this selection' }));
    await screen.findByText('Record 3 · record-c');
    expect(getSelection).toHaveBeenCalledWith(
      expect.objectContaining({ selectionRevision: sourceSelection.id, cursor: undefined, limit: 100 }),
      expect.any(AbortSignal),
    );

    fireEvent.click(screen.getByRole('checkbox', { name: 'Assign Record 1 · record-a to Group A' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Assign Record 2 · record-b to Group A' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Assign Record 2 · record-b to Group B' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Assign Record 3 · record-c to Group B' }));
    expect(screen.getByRole('region', { name: 'Exact group memberships' }).textContent).toContain('Group A: Record 1 · record-a, Record 2 · record-b');
    expect(screen.getByRole('region', { name: 'Exact group memberships' }).textContent).toContain('Group B: Record 2 · record-b, Record 3 · record-c');
    fireEvent.click(screen.getByRole('button', { name: 'Create group revision' }));

    await waitFor(() => expect(createExplicitGroupRevision).toHaveBeenCalledTimes(1));
    const createRequest = createExplicitGroupRevision.mock.calls[0]?.[0];
    expect(createRequest).toEqual(expect.objectContaining({ selectionRevision: sourceSelection.id, snapshotToken: 'snapshot-1' }));
    expect(createRequest.groups.map((group: { label: string; memberIds: ReadonlyArray<string> }) => ({ label: group.label, memberIds: group.memberIds }))).toEqual([
      { label: 'Group A', memberIds: ['opaque-member-a', 'opaque-member-b'] },
      { label: 'Group B', memberIds: ['opaque-member-b', 'opaque-member-c'] },
    ]);
    expect(JSON.stringify(createRequest.groups)).not.toContain('resourceType');
    expect(await screen.findAllByRole('option', { name: /grouprev_new/ })).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: 'Preview row change' }));
    await screen.findByRole('button', { name: 'Apply row definition' });
    expect(proposeRowDefinition).toHaveBeenCalledWith(expect.objectContaining({
      selection: { kind: 'EXPLICIT_GROUP', explicitGroup: { revisionId: revisionID, unassignedMemberPolicy: 'ERROR' } },
    }));
    fireEvent.click(screen.getByRole('button', { name: 'Apply row definition' }));
    await waitFor(() => expect(onApply).toHaveBeenCalledWith('proposal-receipt-1'));
  });

  it('cancels group setup without creating a revision or changing the draft', async () => {
    const listRowDefinitionChoices = vi.fn().mockResolvedValue(choices);
    const proposeRowDefinition = vi.fn();
    const getSelection = vi.fn().mockResolvedValue(selectionPage);
    const createExplicitGroupRevision = vi.fn();
    const onApply = vi.fn();
    render(
      <RowDefinitionSettingsPanel
        {...relatedRowProps}
        client={{ listRowDefinitionChoices, proposeRowDefinition, getSelection, createExplicitGroupRevision }}
        project="project-a"
        explorerId="explorer-a"
        snapshotToken="snapshot-1"
        draftVersion={4}
        draftDigest="draft-digest-4"
        table={table}
        selection={sourceSelection}
        disabled={false}
        onApply={onApply}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    await screen.findByRole('combobox', { name: 'New row shape' });
    fireEvent.click(screen.getByRole('button', { name: 'Create groups from this selection' }));
    await screen.findByText('Record 3 · record-c');
    fireEvent.click(screen.getByRole('button', { name: 'Cancel group setup' }));
    expect(createExplicitGroupRevision).not.toHaveBeenCalled();
    expect(proposeRowDefinition).not.toHaveBeenCalled();
    expect(onApply).not.toHaveBeenCalled();
  });

  it('loads selection members by page and retains earlier assignments', async () => {
    const revisionID = 'grouprev_paged';
    const firstPage = { revision: sourceSelection, members: selectionPage.members.slice(0, 2), nextCursor: 'cursor-page-2' };
    const secondPage = { revision: sourceSelection, members: selectionPage.members.slice(2) };
    const createdChoices: RowDefinitionChoicesResponse = {
      ...choices,
      explicitGroups: [...choices.explicitGroups, {
        revisionId: revisionID, groupCount: 2, memberCount: 1, createdAt: '2026-09-20T01:00:00.000Z',
        unassignedMemberPolicies: ['ERROR', 'EXCLUDE', 'GROUP_AS_UNASSIGNED'],
      }],
    };
    const listRowDefinitionChoices = vi.fn().mockResolvedValueOnce(choices).mockResolvedValueOnce(createdChoices);
    const getSelection = vi.fn().mockResolvedValueOnce(firstPage).mockResolvedValueOnce(secondPage);
    const createExplicitGroupRevision = vi.fn().mockImplementation(async (args: {
      readonly selectionRevision: string;
      readonly groups: ReadonlyArray<{ readonly id: string; readonly label: string; readonly ordinal: number; readonly memberIds: ReadonlyArray<string> }>;
    }) => ({
      revisionId: revisionID, sourceSelectionRevisionId: args.selectionRevision, groupCount: args.groups.length,
      memberCount: args.groups.reduce((count, group) => count + group.memberIds.length, 0), createdAt: '2026-09-20T01:00:00.000Z',
      groups: args.groups.map((group) => ({ ...group, memberCount: group.memberIds.length })),
    }));
    render(
      <RowDefinitionSettingsPanel
        {...relatedRowProps}
        client={{ listRowDefinitionChoices, proposeRowDefinition: vi.fn().mockResolvedValue(proposal), getSelection, createExplicitGroupRevision }}
        project="project-a" explorerId="explorer-a" snapshotToken="snapshot-1" draftVersion={4} draftDigest="draft-digest-4"
        table={table} selection={sourceSelection} disabled={false} onApply={vi.fn().mockResolvedValue(true)}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    await screen.findByRole('combobox', { name: 'New row shape' });
    fireEvent.click(screen.getByRole('button', { name: 'Create groups from this selection' }));
    await screen.findByText('Record 2 · record-b');
    expect(screen.getByText('Loaded 2 of 3. Records not loaded or not assigned remain unassigned.')).toBeTruthy();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Assign Record 1 · record-a to Group A' }));
    fireEvent.click(screen.getByRole('button', { name: 'Load more selected records' }));
    await screen.findByText('Record 3 · record-c');
    expect((screen.getByRole('checkbox', { name: 'Assign Record 1 · record-a to Group A' }) as HTMLInputElement).checked).toBe(true);
    expect(getSelection).toHaveBeenNthCalledWith(2,
      expect.objectContaining({ selectionRevision: sourceSelection.id, cursor: 'cursor-page-2', limit: 100 }), expect.any(AbortSignal));
    fireEvent.click(screen.getByRole('button', { name: 'Create group revision' }));
    await waitFor(() => expect(createExplicitGroupRevision).toHaveBeenCalledTimes(1));
    const request = createExplicitGroupRevision.mock.calls[0]?.[0];
    expect(request.groups.map((group: { label: string; memberIds: ReadonlyArray<string> }) => ({ label: group.label, memberIds: group.memberIds }))).toEqual([
      { label: 'Group A', memberIds: ['opaque-member-a'] },
      { label: 'Group B', memberIds: [] },
    ]);
  });

  it('allows creating one explicitly named group', async () => {
    const revisionID = 'grouprev_one';
    const createdChoices: RowDefinitionChoicesResponse = {
      ...choices,
      explicitGroups: [...choices.explicitGroups, {
        revisionId: revisionID, groupCount: 1, memberCount: 0, createdAt: '2026-09-20T01:00:00.000Z',
        unassignedMemberPolicies: ['ERROR', 'EXCLUDE', 'GROUP_AS_UNASSIGNED'],
      }],
    };
    const listRowDefinitionChoices = vi.fn().mockResolvedValueOnce(choices).mockResolvedValueOnce(createdChoices);
    const createExplicitGroupRevision = vi.fn().mockImplementation(async (args: {
      readonly selectionRevision: string;
      readonly groups: ReadonlyArray<{ readonly id: string; readonly label: string; readonly ordinal: number; readonly memberIds: ReadonlyArray<string> }>;
    }) => ({
      revisionId: revisionID, sourceSelectionRevisionId: args.selectionRevision, groupCount: args.groups.length,
      memberCount: args.groups.reduce((count, group) => count + group.memberIds.length, 0), createdAt: '2026-09-20T01:00:00.000Z',
      groups: args.groups.map((group) => ({ ...group, memberCount: group.memberIds.length })),
    }));
    render(
      <RowDefinitionSettingsPanel
        {...relatedRowProps}
        client={{ listRowDefinitionChoices, proposeRowDefinition: vi.fn().mockResolvedValue(proposal), getSelection: vi.fn().mockResolvedValue(selectionPage), createExplicitGroupRevision }}
        project="project-a" explorerId="explorer-a" snapshotToken="snapshot-1" draftVersion={4} draftDigest="draft-digest-4"
        table={table} selection={sourceSelection} disabled={false} onApply={vi.fn().mockResolvedValue(true)}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    await screen.findByRole('combobox', { name: 'New row shape' });
    fireEvent.click(screen.getByRole('button', { name: 'Create groups from this selection' }));
    await screen.findByText('Record 3 · record-c');
    fireEvent.click(screen.getByRole('button', { name: 'Remove Group B' }));
    expect(screen.queryByRole('button', { name: 'Remove Group A' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Create group revision' }));
    await waitFor(() => expect(createExplicitGroupRevision).toHaveBeenCalledTimes(1));
    expect(createExplicitGroupRevision.mock.calls[0]?.[0].groups).toHaveLength(1);
    expect(createExplicitGroupRevision.mock.calls[0]?.[0].groups[0]).toEqual(expect.objectContaining({ label: 'Group A', memberIds: [] }));
  });

  it('expires a proposal when the saved draft changes', async () => {
    const listRowDefinitionChoices = vi.fn().mockResolvedValue(choices);
    const proposeRowDefinition = vi.fn().mockResolvedValue(proposal);
    const onApply = vi.fn().mockResolvedValue(true);
    const props = {
      client: { listRowDefinitionChoices, proposeRowDefinition, getSelection: vi.fn(), createExplicitGroupRevision: vi.fn() },
      project: 'project-a', explorerId: 'explorer-a', snapshotToken: 'snapshot-1',
      draftVersion: 4, draftDigest: 'draft-digest-4', table, disabled: false, onApply,
      ...relatedRowProps,
    };
    const view = render(<RowDefinitionSettingsPanel {...props} />);
    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    await screen.findByRole('combobox', { name: 'New row shape' });
    fireEvent.click(screen.getByRole('button', { name: 'Preview row change' }));
    await screen.findByRole('button', { name: 'Apply row definition' });
    view.rerender(<RowDefinitionSettingsPanel {...props} draftVersion={5} draftDigest="draft-digest-5" />);
    expect((await screen.findByRole('alert')).textContent).toMatch(/proposal is stale/i);
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
    await screen.findByRole('combobox', { name: 'New row shape' });
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
        {...relatedRowProps}
        client={{ listRowDefinitionChoices, proposeRowDefinition, getSelection: vi.fn(), createExplicitGroupRevision: vi.fn() }}
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
    await screen.findByRole('combobox', { name: 'New row shape' });
    fireEvent.click(screen.getByRole('button', { name: 'Preview row change' }));
    expect((await screen.findByRole('alert')).textContent).toContain(serverError.message);
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(onApply).not.toHaveBeenCalled();
  });
});
