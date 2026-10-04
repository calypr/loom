// @vitest-environment jsdom
import React from 'react';
import type { ReactNode } from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { RowDefinitionChoicesResponse, RowDefinitionProposal } from '../../../types';
import type { SelectionRevision } from '../../../selection';
import type { DraftTable } from '../authoring/model';
import type { ConstructionHistoryStep } from '../constructionWorkspace/ConstructionWorkspace';
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

const tableWithGroupStep: DraftTable = {
  ...table,
  document: {
    ...table.document,
    construction: {
      version: 1,
      steps: [{
        id: 'group-step',
        inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: { kind: 'GROUP', group: {
          constructionId: 'group-step',
          keys: [{ inputColumnId: 'status-id', outputColumnId: 'status-id' }],
        } },
        outputs: [{ id: 'status-id', name: 'status', label: 'Status' }],
      }],
    },
  },
};

const groupStepHistory: ConstructionHistoryStep = {
  id: 'group-step',
  title: 'Group',
  summary: 'Group rows by Status.',
  editable: true,
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
  startingCollectionSummary: 'Starting collection: All authorized Patient records',
  renderRootSettings: (onRootChange: (nodeId: string, occurrenceId: string) => void) => (
    <section aria-label="Row occurrence settings">
      <button type="button" onClick={() => onRootChange('patient', 'patient-root')}>Change row occurrence</button>
    </section>
  ),
  startingCollectionSettings: <section aria-label="Starting collection">Collection controls</section>,
  relatedRows: { supported: false, reason: 'No executable route' },
  reshapeRows: { group: { supported: true }, groupEntry: 'group' as const, groupAlternatives: [], pivot: { supported: true } },
  onChooseRelatedRows: vi.fn(),
  onChooseReshape: vi.fn(),
  onChangeRootOccurrence: vi.fn(),
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
  pivotSupported?: boolean;
  pivotPending?: boolean;
  codedPivotDefault?: boolean;
  tablePivotAlternative?: boolean;
  codedGroupDefault?: boolean;
  sourceGroupAlternative?: boolean;
  onChangeRootOccurrence?: (nodeId: string, occurrenceId: string) => void;
  tableValue?: DraftTable;
  constructionHistory?: ReadonlyArray<ConstructionHistoryStep>;
  onEditConstructionStep?: (stepId: string) => void;
  onRemoveConstructionStep?: (stepId: string) => void;
  sourceCollectionAction?: ReactNode;
} = {}) => {
  const listRowDefinitionChoices = vi.fn().mockResolvedValue(overrides.choicesValue ?? choices);
  const proposeRowDefinition = vi.fn().mockResolvedValue(overrides.proposalValue ?? proposal);
  const getSelection = vi.fn().mockResolvedValue(selectionPage);
  const createExplicitGroupRevision = vi.fn();
  const onApply = vi.fn().mockResolvedValue(true);
  const onChooseRelatedRows = vi.fn();
  const onChooseReshape = vi.fn();
  const onChangeRootOccurrence = overrides.onChangeRootOccurrence ?? vi.fn();
  const onEditConstructionStep = overrides.onEditConstructionStep ?? vi.fn();
  const onRemoveConstructionStep = overrides.onRemoveConstructionStep ?? vi.fn();
  const view = render(
    <RowDefinitionSettingsPanel
      {...relatedRowProps}
      onChangeRootOccurrence={onChangeRootOccurrence}
      relatedRows={{ supported: overrides.relatedRowsSupported ?? false, reason: 'No executable route' }}
      reshapeRows={{ group: { supported: true }, groupEntry: overrides.codedGroupDefault ? 'coded-group' : 'group',
        groupAlternatives: overrides.sourceGroupAlternative ? [{ kind: 'source-group', label: 'By source field' }] : [],
        pivotEntry: overrides.codedPivotDefault ? 'coded-pivot' : 'pivot', pivot: {
        supported: overrides.pivotSupported ?? true,
        reason: 'No executable category-to-column operation',
      }, pivotPending: overrides.pivotPending ?? false, ...(overrides.tablePivotAlternative ? { pivotAlternative: 'pivot' as const } : {}) }}
      onChooseRelatedRows={onChooseRelatedRows}
      onChooseReshape={onChooseReshape}
      sourceCollectionAction={overrides.sourceCollectionAction}
      client={{ listRowDefinitionChoices, proposeRowDefinition, getSelection, createExplicitGroupRevision }}
      project="project-a"
      explorerId="explorer-a"
      snapshotToken="snapshot-1"
      draftVersion={overrides.draftVersion ?? 4}
      draftDigest={overrides.draftDigest ?? 'draft-digest-4'}
      table={overrides.tableValue ?? table}
      constructionHistory={overrides.constructionHistory ?? []}
      disabled={false}
      onApply={onApply}
      onEditConstructionStep={onEditConstructionStep}
      onRemoveConstructionStep={onRemoveConstructionStep}
    />,
  );
  return { ...view, listRowDefinitionChoices, proposeRowDefinition, getSelection, createExplicitGroupRevision, onApply, onChooseRelatedRows, onChooseReshape, onChangeRootOccurrence, onEditConstructionStep, onRemoveConstructionStep };
};

afterEach(cleanup);

describe('RowDefinitionSettingsPanel', () => {
  it('offers source collection expansion directly in the row change menu', async () => {
    const { onChooseReshape } = renderSettings({
      sourceCollectionAction: (
        <button type="button" data-testid="construction-reshape-expand-source">
          Expand a repeated source field
        </button>
      ),
    });
    expect(table.document.columns).toHaveLength(0);

    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    const rowChangeMenu = await screen.findByRole('region', { name: 'Choose a row change' });
    expect(rowChangeMenu.contains(
      await screen.findByTestId('construction-reshape-expand-source'),
    )).toBe(true);
    expect(onChooseReshape).not.toHaveBeenCalled();
  });

  it('shows one compact Rows card and opens its row and starting-collection settings', async () => {
    renderSettings();

    const trigger = screen.getByTestId('construction-rows-settings-trigger');
    expect(trigger).toHaveTextContent('Rows');
    expect(trigger).toHaveTextContent('One row per source record');
    expect(trigger).not.toHaveTextContent('Starting collection');
    expect(screen.queryByRole('dialog')).toBeNull();

    fireEvent.click(trigger);
    const dialog = await screen.findByRole('dialog', { name: 'Row definition settings' });
    expect(dialog.contains(screen.getByRole('region', { name: 'Row occurrence settings' }))).toBe(true);
    expect(dialog.contains(screen.getByRole('region', { name: 'Starting collection' }))).toBe(true);
    expect(screen.getByText('All authorized Patient records')).toBeInTheDocument();
    expect(dialog.querySelectorAll('details')).toHaveLength(0);
    expect(dialog.contains(await screen.findByRole('combobox', { name: 'What should each row represent?' }))).toBe(true);
  });

  it('opens executable related rows from the Rows settings dialog', async () => {
    const { onChooseRelatedRows } = renderSettings({ relatedRowsSupported: true });
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    const related = await screen.findByRole('button', { name: /Make a row for each related record/ });
    expect(related).toHaveTextContent('Follow a relationship to another record type, such as Patient to Observation');
    expect(related).toHaveTextContent('Make a row for each match, with existing values repeated');
    expect(related).toHaveTextContent('By default, keep a current row once when no records match');
    expect(related).toHaveTextContent('Fields from each matched record become available in Add columns');
    expect(related).toHaveTextContent('Patient A with 2 Observations → 2 rows');
    fireEvent.click(related);
    expect(onChooseRelatedRows).toHaveBeenCalledOnce();
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('opens Unpivot from the compact reshape entry even when the editor will explain missing prerequisites', async () => {
    const { onChooseReshape } = renderSettings({ pivotSupported: false });
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    const unpivot = await screen.findByTestId('construction-action-unpivot-rows');

    expect(unpivot).toBeEnabled();
    fireEvent.click(unpivot);

    expect(onChooseReshape).toHaveBeenCalledWith('unpivot');
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('shows applied row changes and closes Define dataframe before editing the selected step', async () => {
    const onEditConstructionStep = vi.fn();
    renderSettings({ tableValue: tableWithGroupStep, constructionHistory: [groupStepHistory], onEditConstructionStep });
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    const history = await screen.findByTestId('construction-row-operation-history');
    expect(history).toHaveTextContent('1');
    expect(history).toHaveTextContent('Group rows by Status.');
    expect(screen.getByRole('heading', { name: 'Named cohorts' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Create groups from this selection' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId('construction-row-edit-group-step'));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(onEditConstructionStep).toHaveBeenCalledWith('group-step');
  });

  it('closes Define dataframe before removing an applied row change', async () => {
    const onRemoveConstructionStep = vi.fn();
    renderSettings({ tableValue: tableWithGroupStep, constructionHistory: [groupStepHistory], onRemoveConstructionStep });
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    await screen.findByTestId('construction-row-operation-history');
    fireEvent.click(screen.getByTestId('construction-row-remove-group-step'));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(onRemoveConstructionStep).toHaveBeenCalledWith('group-step');
  });

  it('opens the selected grouping editor directly from Rows', async () => {
    const { onChooseReshape } = renderSettings();
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    const group = await screen.findByTestId('construction-action-group-rows');
    expect(group).toHaveTextContent('Combine rows into groups');
    expect(group).toHaveTextContent('one row for each combination of matching values in the fields you choose');
    expect(group).toHaveTextContent('3 rows with Status=active and Unit=north → 1 group row for active + north');
    fireEvent.click(group);
    expect(onChooseReshape).toHaveBeenCalledWith('group');
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('opens grouping by a recorded code when the backend offers it', async () => {
    const { onChooseReshape } = renderSettings({ codedGroupDefault: true });
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    const group = await screen.findByTestId('construction-action-group-rows');
    expect(group).toHaveTextContent('Combine rows into groups');
    expect(group).toHaveTextContent('one row for each distinct recorded code');
    expect(group).toHaveTextContent('3 rows with diagnosis code A → 1 group row for diagnosis A');
    fireEvent.click(group);
    expect(onChooseReshape).toHaveBeenCalledWith('coded-group');
  });

  it('opens source-field grouping directly when coded grouping is the default', async () => {
    const { onChooseReshape } = renderSettings({ codedGroupDefault: true, sourceGroupAlternative: true });
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    fireEvent.click(await screen.findByRole('button', { name: 'By source field' }));
    expect(onChooseReshape).toHaveBeenCalledWith('source-group');
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('opens the neutral category-to-column entry directly from Rows', async () => {
    const { onChooseReshape } = renderSettings();
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    fireEvent.click(await screen.findByTestId('construction-action-pivot-rows'));
    expect(onChooseReshape).toHaveBeenCalledWith('categories');
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('opens the neutral category entry when coded pivot is preferred', async () => {
    const { onChooseReshape } = renderSettings({ codedPivotDefault: true });
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    const pivot = await screen.findByTestId('construction-action-pivot-rows');
    expect(pivot).toBeEnabled();
    fireEvent.click(pivot);
    expect(onChooseReshape).toHaveBeenCalledWith('categories');
  });

  it('disables category entry while matching Pivot capabilities are pending', async () => {
    const { onChooseReshape } = renderSettings({ pivotSupported: false, pivotPending: true });
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    const pivot = await screen.findByTestId('construction-action-pivot-rows');
    expect(pivot).toBeDisabled();
    fireEvent.click(pivot);
    expect(onChooseReshape).not.toHaveBeenCalled();
    expect(screen.getByRole('dialog', { name: 'Row definition settings' })).toBeInTheDocument();
  });

  it('uses table columns when coded values are absent but table Pivot is available', async () => {
    const { onChooseReshape } = renderSettings({ codedPivotDefault: true, tablePivotAlternative: true });
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    const pivot = await screen.findByTestId('construction-action-pivot-rows');
    expect(pivot).toHaveTextContent('Make a column for each category. Choose which values fill those columns.');
    expect(pivot).toHaveTextContent('group by person. Height=170 and Weight=60 in two rows → one row with Height=170 and Weight=60 columns');
    expect(pivot).toBeEnabled();
    fireEvent.click(await screen.findByTestId('construction-action-table-pivot-rows'));
    expect(onChooseReshape).toHaveBeenCalledWith('pivot');
  });

  it('keeps the neutral category entry available after settled unsupported capabilities', async () => {
    const { onChooseReshape } = renderSettings({ pivotSupported: false, pivotPending: false });
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    const pivot = await screen.findByTestId('construction-action-pivot-rows');
    expect(pivot).toBeEnabled();
    fireEvent.click(pivot);
    expect(onChooseReshape).toHaveBeenCalledWith('categories');
  });

  it('closes row settings before changing the root occurrence', async () => {
    const onChangeRootOccurrence = vi.fn();
    const { onChangeRootOccurrence: onChangeRoot } = renderSettings({ onChangeRootOccurrence });
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    const dialog = await screen.findByRole('dialog', { name: 'Row definition settings' });

    fireEvent.click(screen.getByText('Starting record type'));
    fireEvent.click(screen.getByRole('button', { name: 'Change row occurrence' }));

    expect(onChangeRoot).toHaveBeenCalledWith('patient', 'patient-root');
    expect(dialog).not.toBeInTheDocument();
  });

  it('explains when the backend has no executable related row path', async () => {
    const { onChooseRelatedRows } = renderSettings();
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    expect(screen.getByRole('button', { name: /Make a row for each related record/ })).toBeDisabled();
    expect(screen.getByText('No executable route')).toBeInTheDocument();
    expect(onChooseRelatedRows).not.toHaveBeenCalled();
  });

  it('restores the saved expansion using occurrence, path, and empty policy', async () => {
    renderSettings({
      tableValue: { ...table, document: { ...table.document, rows: {
        kind: 'EXPANDED', expanded: { occurrenceId: 'base', scopePath: 'name[]', emptyCollectionPolicy: 'EXCLUDE' },
      } } },
      choicesValue: { ...choices, choices: [
        { ...choices.choices[0]!, choiceId: 'other-occurrence', occurrenceId: 'related' },
        { ...choices.choices[0]!, occurrenceId: 'base' },
      ] },
    });
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    const shape = await screen.findByRole('combobox', { name: 'What should each row represent?' });
    expect(shape instanceof HTMLSelectElement && shape.value).toBe('expanded:expanded-choice');
    const policy = screen.getByRole('combobox', { name: 'Unmatched record policy' });
    expect(policy instanceof HTMLSelectElement && policy.value).toBe('expanded:expanded-choice:EXCLUDE');
  });

  it('defaults repeated values to preserving unmatched records and allows an explicit policy change', async () => {
    const { proposeRowDefinition } = renderSettings();
    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    const shape = await screen.findByRole('combobox', { name: 'What should each row represent?' });
    fireEvent.change(shape, { target: { value: 'expanded:expanded-choice' } });
    expect(screen.getByRole('combobox', { name: 'Unmatched record policy' })).toBeInTheDocument();
    await waitFor(() => expect(proposeRowDefinition).toHaveBeenCalledWith(expect.objectContaining({
      selection: { kind: 'EXPANDED', expanded: { rowChoiceId: 'expanded-choice', emptyCollectionPolicy: 'PRESERVE_PARENT' } },
    }), expect.any(AbortSignal)));
    fireEvent.change(screen.getByRole('combobox', { name: 'Unmatched record policy' }), {
      target: { value: 'expanded:expanded-choice:EXCLUDE' },
    });
    await waitFor(() => expect(proposeRowDefinition).toHaveBeenLastCalledWith(expect.objectContaining({
      selection: { kind: 'EXPANDED', expanded: { rowChoiceId: 'expanded-choice', emptyCollectionPolicy: 'EXCLUDE' } },
    }), expect.any(AbortSignal)));
  });

  it('previews the latest row choice without a Preview click', async () => {
    const { proposeRowDefinition } = renderSettings();
    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    const shape = await screen.findByRole('combobox', { name: 'What should each row represent?' });
    fireEvent.change(shape, { target: { value: 'expanded:expanded-choice' } });
    fireEvent.change(screen.getByRole('combobox', { name: 'Unmatched record policy' }), {
      target: { value: 'expanded:expanded-choice:EXCLUDE' },
    });

    await waitFor(() => expect(proposeRowDefinition).toHaveBeenCalledTimes(1));
    expect(proposeRowDefinition).toHaveBeenCalledWith(expect.objectContaining({
      selection: { kind: 'EXPANDED', expanded: { rowChoiceId: 'expanded-choice', emptyCollectionPolicy: 'EXCLUDE' } },
    }), expect.any(AbortSignal));
    expect(await screen.findByText('4 rows → 2 rows')).toBeInTheDocument();
  });

  it('cancels an in-flight row preview when the policy changes', async () => {
    const { proposeRowDefinition } = renderSettings();
    proposeRowDefinition.mockImplementationOnce(() => new Promise(() => {}));
    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    const shape = await screen.findByRole('combobox', { name: 'What should each row represent?' });
    fireEvent.change(shape, { target: { value: 'expanded:expanded-choice' } });
    await waitFor(() => expect(proposeRowDefinition).toHaveBeenCalledTimes(1));
    const firstSignal = proposeRowDefinition.mock.calls[0]?.[1] as AbortSignal;
    expect(shape).toBeEnabled();
    expect(screen.getByRole('combobox', { name: 'Unmatched record policy' })).toBeEnabled();

    fireEvent.change(screen.getByRole('combobox', { name: 'Unmatched record policy' }), {
      target: { value: 'expanded:expanded-choice:EXCLUDE' },
    });
    expect(firstSignal.aborted).toBe(true);
    await waitFor(() => expect(proposeRowDefinition).toHaveBeenCalledTimes(2));
    expect(proposeRowDefinition.mock.calls[1]?.[0].selection.expanded.emptyCollectionPolicy).toBe('EXCLUDE');
    expect(await screen.findByText('4 rows → 2 rows')).toBeInTheDocument();
  });

  it('explains that field grouping cannot be applied as a starting row shape yet', async () => {
    renderSettings({ choicesValue: {
      ...choices,
      choices: [...choices.choices, {
        choiceId: 'field-group-choice', fieldPath: 'status', label: 'Status', description: '',
        occurrenceSummary: 'Root occurrence', routeSummary: 'Root', kind: 'FIELD_GROUP', valueType: 'STRING',
        policies: [{ name: 'missingKeyPolicy', options: ['ERROR', 'EXCLUDE', 'GROUP_AS_MISSING'] }],
      }],
    } });
    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    await screen.findByRole('combobox', { name: 'What should each row represent?' });
    expect(screen.queryByRole('option', { name: /Status/ })).toBeNull();
  });

  it('previews and applies only the server-issued explicit-group proposal', async () => {
    const { listRowDefinitionChoices, proposeRowDefinition, onApply } = renderSettings();
    expect(screen.getByTestId('construction-rows-settings-trigger')).toHaveTextContent('One row per source record');
    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    const select = await screen.findByRole('combobox', { name: 'What should each row represent?' });
    expect(screen.getByRole('option', { name: 'One row per source record' })).toBeTruthy();
    expect(screen.getAllByRole('option', { name: 'One row per value in Name' })).toHaveLength(1);
    expect(screen.getAllByRole('option', { name: /One row per saved group/ })).toHaveLength(1);
    expect(listRowDefinitionChoices).toHaveBeenCalledWith(expect.objectContaining({
      project: 'project-a', explorerId: 'explorer-a', outputId: 'patients', snapshotToken: 'snapshot-1',
    }));

    fireEvent.change(select, { target: { value: 'explicit:grouprev_0123456789abcdef' } });
    expect(screen.getByRole('combobox', { name: 'Unmatched record policy' })).toBeInTheDocument();
    fireEvent.change(screen.getByRole('combobox', { name: 'Unmatched record policy' }), {
      target: { value: 'explicit:grouprev_0123456789abcdef:EXCLUDE' },
    });
    expect(await screen.findByText('4 rows → 2 rows')).toBeTruthy();
    expect(screen.getByText('Added · grouprev_0123456789abcdef:group-a')).toBeTruthy();
    expect(screen.getByText('Removed · patient-4')).toBeTruthy();
    expect(proposeRowDefinition).toHaveBeenCalledWith(expect.objectContaining({
      outputId: 'patients',
      expectedDraftVersion: 4,
      expectedDraftDigest: 'draft-digest-4',
      selection: { kind: 'EXPLICIT_GROUP', explicitGroup: { revisionId: 'grouprev_0123456789abcdef', unassignedMemberPolicy: 'EXCLUDE' } },
    }), expect.any(AbortSignal));
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
    await screen.findByRole('combobox', { name: 'What should each row represent?' });

    expect(screen.getAllByRole('option', { name: 'One row per code in Component → Code' })).toHaveLength(1);
    expect(screen.getAllByRole('option', { name: 'One row per code in Category' })).toHaveLength(1);
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
    await screen.findByRole('combobox', { name: 'What should each row represent?' });

    expect(screen.getByRole('option', { name: 'One row per code in Component → Code (component[].code.coding[])' })).toBeTruthy();
    expect(screen.getByRole('option', { name: 'One row per code in Component → Code (component.code.coding[])' })).toBeTruthy();
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
    await screen.findByRole('combobox', { name: 'What should each row represent?' });

    expect(screen.getAllByRole('option', { name: /One row per code in Component → Code.*Occurrence first via Subject/ })).toHaveLength(1);
    expect(screen.getAllByRole('option', { name: /One row per code in Component → Code.*Occurrence second via Focus/ })).toHaveLength(1);
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
    await screen.findByRole('combobox', { name: 'What should each row represent?' });
    expect(screen.getByRole('region', { name: 'Named cohorts' })).toBeInTheDocument();
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
    await screen.findByRole('button', { name: 'Apply row definition' });
    expect(proposeRowDefinition).toHaveBeenCalledWith(expect.objectContaining({
      selection: { kind: 'EXPLICIT_GROUP', explicitGroup: { revisionId: revisionID, unassignedMemberPolicy: 'ERROR' } },
    }), expect.any(AbortSignal));
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
    await screen.findByRole('combobox', { name: 'What should each row represent?' });
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
    await screen.findByRole('combobox', { name: 'What should each row represent?' });
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
    await screen.findByRole('combobox', { name: 'What should each row represent?' });
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
    fireEvent.change(await screen.findByRole('combobox', { name: 'What should each row represent?' }), {
      target: { value: 'expanded:expanded-choice' },
    });
    await screen.findByRole('button', { name: 'Apply row definition' });
    view.rerender(<RowDefinitionSettingsPanel {...props} draftVersion={5} draftDigest="draft-digest-5" />);
    expect((await screen.findByRole('alert')).textContent).toMatch(/row change is out of date/i);
    expect(screen.queryByRole('button', { name: 'Apply row definition' })).toBeNull();
    expect(onApply).not.toHaveBeenCalled();
  });

  it('treats an unavailable response as a failure, retaining controls without an Apply action', async () => {
    const unavailable: RowDefinitionProposal = {
      ...proposal,
      proposalId: 'unavailable-receipt',
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
    fireEvent.change(await screen.findByRole('combobox', { name: 'What should each row represent?' }), {
      target: { value: 'expanded:expanded-choice' },
    });
    expect((await screen.findAllByText('The server could not compare this row definition.')).length).toBeGreaterThan(0);
    expect(screen.queryByRole('button', { name: 'Apply row definition' })).not.toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('The server could not compare this row definition.');
    expect(screen.getByRole('combobox', { name: 'Unmatched record policy' })).toBeEnabled();
    fireEvent.click(screen.getByRole('button', { name: 'Back to table' }));
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
    fireEvent.change(await screen.findByRole('combobox', { name: 'What should each row represent?' }), {
      target: { value: 'expanded:expanded-choice' },
    });
    expect((await screen.findByRole('alert')).textContent).toContain(serverError.message);
    expect(screen.getByRole('combobox', { name: 'What should each row represent?' })).toBeEnabled();
    expect(screen.getByRole('combobox', { name: 'Unmatched record policy' })).toBeEnabled();
    fireEvent.click(screen.getByRole('button', { name: 'Back to table' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(onApply).not.toHaveBeenCalled();
  });

  it('repairs a missing-value validation failure by changing policy in the same dialog', async () => {
    const { proposeRowDefinition, onApply } = renderSettings();
    proposeRowDefinition.mockRejectedValueOnce(Object.assign(new Error('Some records have no values for this field. Choose another missing-value policy.'), { status: 422, code: 'EMPTY_COLLECTION_ERROR' }));
    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    fireEvent.change(await screen.findByRole('combobox', { name: 'What should each row represent?' }), { target: { value: 'expanded:expanded-choice' } });
    expect(await screen.findByRole('alert')).toHaveTextContent('Some records have no values');
    expect(screen.queryByRole('button', { name: 'Apply row definition' })).not.toBeInTheDocument();
    const policy = screen.getByRole('combobox', { name: 'Unmatched record policy' });
    expect(policy).toBeEnabled();
    fireEvent.change(policy, { target: { value: 'expanded:expanded-choice:EXCLUDE' } });
    fireEvent.click(await screen.findByRole('button', { name: 'Apply row definition' }));
    await waitFor(() => expect(onApply).toHaveBeenCalledWith('proposal-receipt-1'));
    expect(proposeRowDefinition).toHaveBeenLastCalledWith(expect.objectContaining({ selection: { kind: 'EXPANDED', expanded: { rowChoiceId: 'expanded-choice', emptyCollectionPolicy: 'EXCLUDE' } } }), expect.any(AbortSignal));
  });
});
