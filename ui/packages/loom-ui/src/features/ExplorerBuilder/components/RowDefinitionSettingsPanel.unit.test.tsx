// @vitest-environment jsdom
import React from 'react';
import type { ReactNode } from 'react';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ExplorerBuilderPreviewResult, RowChangeAssessment, RowDefinitionChoicesResponse, RowDefinitionProposal } from '../../../types';
import type { SelectionRevision } from '../../../selection';
import type { DraftTable } from '../authoring/model';
import type { RootChangePickerState, RootChangeSelection } from '../authoring/rootChangeSelection';
import type { ConstructionHistoryStep } from '../constructionWorkspace/ConstructionWorkspace';
import { RowDefinitionSettingsPanel, type ReadyReplacementAssessment } from './RowDefinitionSettingsPanel';

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

const patientWorkspaceTable: DraftTable = {
  ...table,
  document: {
    ...table.document,
    columns: [
      { columnId: 'patient-id', column: 'patient_id', label: 'Patient identifier', occurrenceId: 'base', source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } } },
      { columnId: 'patient-name', column: 'patient_name', label: 'Patient name', occurrenceId: 'base', source: { kind: 'field', field: { path: 'name', projectionMode: 'VALUE' } } },
    ],
  },
};

const reportWorkspaceTable: DraftTable = {
  outputId: 'reports',
  tabId: 'reports',
  title: 'Reports',
  document: {
    ...table.document,
    output: { id: 'reports', title: 'Reports' },
    rootResourceType: 'ResearchStudy',
    route: { occurrenceId: 'base', resourceType: 'ResearchStudy' },
    construction: {
      version: 1,
      steps: [{
        id: 'join-step',
        inputs: [
          { kind: 'WORKSPACE_OUTPUT', outputId: 'patients' },
          { kind: 'WORKSPACE_OUTPUT', outputId: 'lab-rows' },
        ],
        operation: { kind: 'COMBINE', combine: {
          kind: 'KEY_JOIN',
          keys: [{ leftColumnId: 'patient-id', rightColumnId: 'lab-patient-id' }],
          projections: [
            { outputColumnId: 'report-patient-id', inputIndex: 0, inputColumnId: 'patient-id' },
            { outputColumnId: 'report-patient-name', inputIndex: 0, inputColumnId: 'patient-name' },
            { outputColumnId: 'report-lab-id', inputIndex: 1, inputColumnId: 'lab-patient-id' },
          ],
        } },
        outputs: [
          { id: 'report-patient-id', name: 'patient_id', label: 'Patient identifier' },
          { id: 'report-patient-name', name: 'patient_name', label: 'Patient name' },
          { id: 'report-lab-id', name: 'lab_patient_id', label: 'Lab patient identifier' },
        ],
      }],
    },
  },
};

const reportConstruction = reportWorkspaceTable.document.construction!;
const reportJoinStep = reportConstruction.steps[0]!;
const nonCombineConsumerTable: DraftTable = {
  ...reportWorkspaceTable,
  document: {
    ...reportWorkspaceTable.document,
    construction: {
      ...reportConstruction,
      steps: [{
        ...reportJoinStep,
        operation: { kind: 'FILTER', filter: { columnId: 'patient-id', operator: 'EXISTS' } },
      }],
    },
  },
};
const noColumnJoinConsumerTable: DraftTable = {
  ...reportWorkspaceTable,
  outputId: 'empty-join',
  tabId: 'empty-join',
  title: 'Empty join',
  document: {
    ...reportWorkspaceTable.document,
    output: { id: 'empty-join', title: 'Empty join' },
    construction: {
      ...reportConstruction,
      steps: [{
        ...reportJoinStep,
        operation: { kind: 'COMBINE', combine: {
          kind: 'KEY_JOIN',
          projections: [{ outputColumnId: 'report-lab-id', inputIndex: 1, inputColumnId: 'lab-patient-id' }],
        } },
      }],
    },
  },
};

const replacementAssessment: RowChangeAssessment = {
  status: 'READY',
  mode: 'REPLACE',
  snapshotToken: 'snapshot-1',
  draftVersion: 4,
  draftDigest: 'draft-digest-4',
  currentRootResourceType: 'Patient',
  candidateRootResourceType: 'ResearchStudy',
  preservedFeatureKeys: [],
  removedFeatureKeys: ['root:Patient', 'column:patient_id'],
  workspaceOutputConsumers: [{ outputId: 'reports', stepId: 'join-step', inputIndex: 0 }],
  sharedFilterBindings: [{ name: 'active-patients', outputId: 'patients', column: 'patient_id', bindingIndex: 1 }],
  diagnostics: [],
  proposal: {
    mode: 'REPLACE',
    outputId: 'patients',
    rootNodeId: 'research-study-node',
    rootOccurrenceId: 'base',
    sourceDocumentDigest: 'source-document-digest',
    routeRebase: [],
    preservedFeatureKeys: [],
    catalogSnapshotToken: 'snapshot-1',
    catalogGeneration: 'generation-1',
    catalogDigest: 'catalog-digest-1',
    removedFeatureKeys: ['root:Patient', 'column:patient_id'],
    workspaceOutputConsumers: [{ outputId: 'reports', stepId: 'join-step', inputIndex: 0 }],
    sharedFilterBindings: [{ name: 'active-patients', outputId: 'patients', column: 'patient_id', bindingIndex: 1 }],
  },
  candidateReceiptId: 'replacement-receipt-1',
  unresolved: [],
};

const replacementPreview: ExplorerBuilderPreviewResult = {
  apiVersion: 'loom.calypr.org/explorer-authoring/v2',
  kind: 'ExplorerBuilderPreview',
  receiptId: 'replacement-receipt-1',
  outputId: 'patients',
  columns: [
    { column: 'resource_type', label: 'Resource type', logicalType: 'string', filterable: true, chartable: false },
    { column: 'id', label: 'ResearchStudy ID', logicalType: 'string', filterable: true, chartable: false, sourceResourceType: 'ResearchStudy', sourcePath: 'id' },
  ],
  rows: [{ resource_type: 'ResearchStudy', id: 'study-1' }],
  rowLineageCapability: { status: 'AVAILABLE' },
  rowCount: 1,
  diagnostics: [],
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

const replacementConsequenceTable: DraftTable = {
  ...patientWorkspaceTable,
  document: {
    ...patientWorkspaceTable.document,
    output: { id: 'patients', title: 'Patients', rowLabel: 'Patient summary' },
    route: {
      occurrenceId: 'base',
      resourceType: 'Patient',
      children: [{ occurrenceId: 'patient-observations', resourceType: 'Observation', relationship: 'observations' }],
    },
    rows: {
      kind: 'EXPANDED',
      expanded: { occurrenceId: 'patient-observations', scopePath: 'value', emptyCollectionPolicy: 'EXCLUDE' },
    },
    population: {
      selectionRevisionId: 'selection-source-1',
      route: [{ resourceType: 'Observation', relationship: 'observations' }],
    },
    frames: [{
      id: 'patient-frame',
      title: 'Patient details',
      description: 'Patient detail view',
      source: {
        bindingId: 'patient-frame-binding', resourceType: 'Patient', sourcePath: 'name', owningScope: 'base',
        keyPath: 'id', valuePath: 'name', logicalType: 'string', ruleVersion: 'v1', schemaVersion: 1,
      },
      route: [], form: 'VALUE', zeroPolicy: 'NULL', manyPolicy: 'INVALID_MULTIPLE_VALUES',
    }],
    construction: tableWithGroupStep.document.construction,
    fixedFilters: [{ column: 'patient_id', values: ['active'] }],
    actions: [{ type: 'download', title: 'Export patients' }],
  },
};

const replacementShapeTable: DraftTable = {
  ...patientWorkspaceTable,
  document: { ...patientWorkspaceTable.document, tableShape: { reshape: { kind: 'PIVOT' } } },
};

const relatedRowProps = {
  currentRowMeaning: 'One row per source record',
  startingCollectionSummary: 'Starting collection: All authorized Patient records',
  renderRootSettings: (
    onRootChange: (selection: RootChangeSelection) => void,
    _onResolveRootChange: (referenceId: string, alternative: string) => void,
    onReplaceRoot: (rootNodeId: string) => void,
  ) => (
    <section aria-label="Starting record type">
      <label>
        <span>Starting record type</span>
        <select aria-label="Record type" value="base" onChange={(event) => {
          if (event.currentTarget.value === 'patient-observation') {
            onRootChange({ kind: 'authored-occurrence', nodeId: 'observation', occurrenceId: 'patient-observation' });
          }
        }}>
          <option value="base">Patient (current)</option>
          <option value="patient-observation">Observation via observations</option>
        </select>
      </label>
      <button type="button" onClick={() => onReplaceRoot('research-study-node')}>Replace current setup</button>
    </section>
  ),
  startingCollectionSettings: null,
  relatedRows: { supported: false, reason: 'No executable route' },
  reshapeRows: { group: { supported: true }, groupEntry: 'group' as const, groupAlternatives: [], pivot: { supported: true }, expand: { supported: true } },
  onChooseRelatedRows: vi.fn(),
  onChooseReshape: vi.fn(),
  onChangeRootSelection: vi.fn(),
};

const idleRootChangeProps = () => ({
  rootChangeState: { kind: 'idle' } as const satisfies RootChangePickerState,
  rootChangePreviewOpen: false,
  onCancelRootChange: vi.fn(),
  onResolveRootSelection: vi.fn(),
});

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
  relatedRowsReason?: string;
  groupSupported?: boolean;
  groupReason?: string;
  expandReason?: string;
  pivotSupported?: boolean;
  pivotPending?: boolean;
  expandSupported?: boolean;
  codedPivotDefault?: boolean;
  tablePivotAlternative?: boolean;
  codedGroupDefault?: boolean;
  sourceGroupAlternative?: boolean;
  onChangeRootSelection?: (selection: RootChangeSelection) => void;
  tableValue?: DraftTable;
  selection?: SelectionRevision;
  constructionHistory?: ReadonlyArray<ConstructionHistoryStep>;
  onEditConstructionStep?: (stepId: string) => void;
  onRemoveConstructionStep?: (stepId: string) => void;
  sourceCollectionAction?: ReactNode;
  currentRowMeaning?: string;
  renderRootSettings?: typeof relatedRowProps.renderRootSettings;
  startingCollectionSettings?: ReactNode;
  workspaceTables?: ReadonlyArray<DraftTable>;
  replacementAssessment?: RowChangeAssessment;
  replacementPreview?: ExplorerBuilderPreviewResult;
  onApplyRootReplacement?: (assessment: ReadyReplacementAssessment) => Promise<boolean>;
} = {}) => {
  const listRowDefinitionChoices = vi.fn().mockResolvedValue(overrides.choicesValue ?? choices);
  const proposeRowDefinition = vi.fn().mockResolvedValue(overrides.proposalValue ?? proposal);
  const getSelection = vi.fn().mockResolvedValue(selectionPage);
  const createExplicitGroupRevision = vi.fn();
  const assessRowChange = vi.fn().mockResolvedValue(overrides.replacementAssessment ?? replacementAssessment);
  const preview = vi.fn().mockResolvedValue(overrides.replacementPreview ?? replacementPreview);
  const onApply = vi.fn().mockResolvedValue(true);
  const onChooseRelatedRows = vi.fn();
  const onChooseReshape = vi.fn();
  const onChangeRootSelection = overrides.onChangeRootSelection ?? vi.fn();
  const onApplyRootReplacement = overrides.onApplyRootReplacement ?? vi.fn().mockResolvedValue(true);
  const onEditConstructionStep = overrides.onEditConstructionStep ?? vi.fn();
  const onRemoveConstructionStep = overrides.onRemoveConstructionStep ?? vi.fn();
  const view = render(
    <RowDefinitionSettingsPanel
      {...idleRootChangeProps()}
      {...relatedRowProps}
      renderRootSettings={overrides.renderRootSettings ?? relatedRowProps.renderRootSettings}
      currentRowMeaning={overrides.currentRowMeaning ?? relatedRowProps.currentRowMeaning}
      onChangeRootSelection={onChangeRootSelection}
      relatedRows={{ supported: overrides.relatedRowsSupported ?? false, reason: overrides.relatedRowsReason ?? 'No executable route' }}
      reshapeRows={{ group: { supported: overrides.groupSupported ?? true, reason: overrides.groupReason ?? 'No executable grouping operation' }, groupEntry: overrides.codedGroupDefault ? 'coded-group' : 'group',
        groupAlternatives: overrides.sourceGroupAlternative ? [{ kind: 'source-group', label: 'By source field' }] : [],
        pivotEntry: overrides.codedPivotDefault ? 'coded-pivot' : 'pivot', pivot: {
        supported: overrides.pivotSupported ?? true,
        reason: 'No executable category-to-column operation',
      }, expand: { supported: overrides.expandSupported ?? true, reason: overrides.expandReason ?? 'No list column in this table. Use a repeated source field before current row changes.' },
      pivotPending: overrides.pivotPending ?? false, ...(overrides.tablePivotAlternative ? { pivotAlternative: 'pivot' as const } : {}) }}
      onChooseRelatedRows={onChooseRelatedRows}
      onChooseReshape={onChooseReshape}
      sourceCollectionAction={overrides.sourceCollectionAction}
      client={{ listRowDefinitionChoices, proposeRowDefinition, getSelection, createExplicitGroupRevision, assessRowChange, preview }}
      project="project-a"
      explorerId="explorer-a"
      snapshotToken="snapshot-1"
      startingCollectionSettings={overrides.startingCollectionSettings ?? relatedRowProps.startingCollectionSettings}
      draftVersion={overrides.draftVersion ?? 4}
      draftDigest={overrides.draftDigest ?? 'draft-digest-4'}
      table={overrides.tableValue ?? table}
      workspaceTables={overrides.workspaceTables ?? [table]}
      constructionHistory={overrides.constructionHistory ?? []}
      selection={overrides.selection}
      disabled={false}
      onApply={onApply}
      onApplyRootReplacement={onApplyRootReplacement}
      onEditConstructionStep={onEditConstructionStep}
      onRemoveConstructionStep={onRemoveConstructionStep}
    />,
  );
  return { ...view, listRowDefinitionChoices, proposeRowDefinition, getSelection, createExplicitGroupRevision, assessRowChange, preview, onApply, onApplyRootReplacement, onChooseRelatedRows, onChooseReshape, onChangeRootSelection, onEditConstructionStep, onRemoveConstructionStep };
};

afterEach(cleanup);

describe('RowDefinitionSettingsPanel', () => {
  it('previews exact replacement consequences for the same table and cancels without applying', async () => {
    const { assessRowChange, preview, onApplyRootReplacement } = renderSettings({
      tableValue: patientWorkspaceTable,
      workspaceTables: [patientWorkspaceTable, reportWorkspaceTable],
    });
    fireEvent.click(screen.getByRole('button', { name: 'Change current rows' }));
    await screen.findByRole('combobox', { name: 'Rows per starting record' });
    fireEvent.click(screen.getByRole('button', { name: 'Replace current setup' }));

    const dialog = await screen.findByRole('dialog', { name: 'Replace this table’s current setup?' });
    const previewTable = await within(dialog).findByTestId('construction-proposal-preview');
    expect(previewTable.tagName).toBe('TABLE');
    expect(dialog.querySelector('table[data-testid="construction-proposal-preview"]')).toBe(previewTable);
    expect(previewTable.querySelector('table')).toBeNull();
    expect(assessRowChange).toHaveBeenCalledWith(expect.objectContaining({
      mode: 'REPLACE', outputId: 'patients', rootNodeId: 'research-study-node',
      snapshotToken: 'snapshot-1', draftVersion: 4, draftDigest: 'draft-digest-4',
    }), expect.any(AbortSignal));
    expect(preview).toHaveBeenCalledWith(expect.objectContaining({
      receiptId: 'replacement-receipt-1', outputId: 'patients',
    }), expect.any(AbortSignal));

    const consequences = within(dialog).getByRole('region', { name: 'Replacement consequences' });
    expect(consequences).toHaveTextContent('Patients · same output and tab');
    expect(consequences).toHaveTextContent('ResearchStudy');
    expect(consequences).toHaveTextContent('1 preview rows');
    expect(consequences).toHaveTextContent('ResearchStudy ID · id (base value)');
    expect(consequences).toHaveTextContent('Starting record type — Patient');
    expect(consequences).toHaveTextContent('Field — Patient identifier');
    expect(consequences).not.toHaveTextContent('root:Patient');
    expect(consequences).not.toHaveTextContent('column:patient_id');
    expect(consequences).toHaveTextContent('Reports · Join fields: Patient identifier, Patient name');
    expect(consequences).not.toHaveTextContent('Lab patient identifier');
    expect(consequences).toHaveTextContent('active-patients · Patients · Patient identifier');
    expect(within(previewTable).getByRole('columnheader', { name: 'ResearchStudy ID' })).toBeTruthy();
    expect(within(previewTable).getByText('study-1')).toBeTruthy();
    expect(within(dialog).getByRole('button', { name: 'Change starting record type' })).toBeEnabled();
    expect(within(dialog).queryByRole('checkbox')).toBeNull();

    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog', { name: 'Replace this table’s current setup?' })).toBeNull();
    expect(onApplyRootReplacement).not.toHaveBeenCalled();
  });

  it('translates backend removal keys into readable setup consequences', async () => {
    const featureKeys = [
      'root:Patient',
      'rowLabel:Patient summary',
      'route:patient-observations',
      'rows:EXPANDED',
      'column:patient_id',
      'population:selection-source-1',
      'populationRoute:0:Observation:observations',
      'frame:patient-frame',
      'construction:group-step',
      'fixedFilter:patient_id',
      'action:download:Export patients',
    ];
    const assessmentWithEffects: RowChangeAssessment = {
      ...replacementAssessment,
      removedFeatureKeys: featureKeys,
      proposal: { ...replacementAssessment.proposal, removedFeatureKeys: featureKeys },
    };
    renderSettings({
      tableValue: replacementConsequenceTable,
      workspaceTables: [replacementConsequenceTable],
      constructionHistory: [groupStepHistory],
      selection: sourceSelection,
      replacementAssessment: assessmentWithEffects,
    });
    fireEvent.click(screen.getByRole('button', { name: 'Change current rows' }));
    await screen.findByRole('combobox', { name: 'Rows per starting record' });
    fireEvent.click(screen.getByRole('button', { name: 'Replace current setup' }));

    const dialog = await screen.findByRole('dialog', { name: 'Replace this table’s current setup?' });
    const consequences = within(dialog).getByRole('region', { name: 'Replacement consequences' });
    for (const expected of [
      'Starting record type — Patient',
      'Row label — Patient summary',
      'Related record path — observations',
      'Row definition — one row per list value',
      'Field — Patient identifier',
      'Starting collection — 3 Patient records',
      'Starting collection path 1 — Observation via observations',
      'Record detail frame — Patient details',
      'Construction step — Group',
      'Fixed filter — Patient identifier',
      'Saved action — Export patients',
    ]) {
      expect(consequences).toHaveTextContent(expected);
    }
    for (const featureKey of featureKeys) expect(consequences).not.toHaveTextContent(featureKey);
  });

  it('keeps consumer IDs out of the review when its table, operation, or referenced columns are unavailable', async () => {
    const consumers = [
      { outputId: 'opaque-missing-consumer', stepId: 'opaque-missing-step', inputIndex: 4 },
      { outputId: 'reports', stepId: 'join-step', inputIndex: 0 },
      { outputId: 'empty-join', stepId: 'join-step', inputIndex: 0 },
    ];
    const assessmentWithConsumers: RowChangeAssessment = {
      ...replacementAssessment,
      workspaceOutputConsumers: consumers,
      proposal: { ...replacementAssessment.proposal, workspaceOutputConsumers: consumers },
    };
    renderSettings({
      tableValue: patientWorkspaceTable,
      workspaceTables: [nonCombineConsumerTable, noColumnJoinConsumerTable],
      replacementAssessment: assessmentWithConsumers,
    });
    fireEvent.click(screen.getByRole('button', { name: 'Change current rows' }));
    await screen.findByRole('combobox', { name: 'Rows per starting record' });
    fireEvent.click(screen.getByRole('button', { name: 'Replace current setup' }));

    const dialog = await screen.findByRole('dialog', { name: 'Replace this table’s current setup?' });
    const consequences = within(dialog).getByRole('region', { name: 'Replacement consequences' });
    const consumersList = within(consequences).getByRole('list', { name: 'Affected current-draft consumers' });
    expect(consumersList).toHaveTextContent('Another table · Uses this table in a construction step');
    expect(consumersList).toHaveTextContent('Reports · Uses this table in a construction step');
    expect(consumersList).toHaveTextContent('Empty join · Uses this table in a join');
    for (const opaqueId of ['opaque-missing-consumer', 'opaque-missing-step', 'join-step']) {
      expect(consumersList).not.toHaveTextContent(opaqueId);
    }
  });

  it('describes reshaped and derived table-shape consequences without exposing their keys', async () => {
    const featureKeys = ['tableShape:reshape:PIVOT', 'tableShape:derived:derived-patient-age'];
    const assessmentWithEffects: RowChangeAssessment = {
      ...replacementAssessment,
      removedFeatureKeys: featureKeys,
      proposal: { ...replacementAssessment.proposal, removedFeatureKeys: featureKeys },
    };
    renderSettings({ tableValue: replacementShapeTable, replacementAssessment: assessmentWithEffects });
    fireEvent.click(screen.getByRole('button', { name: 'Change current rows' }));
    await screen.findByRole('combobox', { name: 'Rows per starting record' });
    fireEvent.click(screen.getByRole('button', { name: 'Replace current setup' }));

    const dialog = await screen.findByRole('dialog', { name: 'Replace this table’s current setup?' });
    const consequences = within(dialog).getByRole('region', { name: 'Replacement consequences' });
    expect(consequences).toHaveTextContent('Table shape — pivoted rows');
    expect(consequences).toHaveTextContent('Table shape — derived columns');
    for (const featureKey of featureKeys) expect(consequences).not.toHaveTextContent(featureKey);
  });

  it('applies the exact ready replacement assessment after the review is complete', async () => {
    const { onApplyRootReplacement } = renderSettings({
      workspaceTables: [patientWorkspaceTable, reportWorkspaceTable],
    });
    fireEvent.click(screen.getByRole('button', { name: 'Change current rows' }));
    await screen.findByRole('combobox', { name: 'Rows per starting record' });
    fireEvent.click(screen.getByRole('button', { name: 'Replace current setup' }));
    const dialog = await screen.findByRole('dialog', { name: 'Replace this table’s current setup?' });
    const applyButton = await within(dialog).findByRole('button', { name: 'Change starting record type' });
    fireEvent.click(applyButton);
    await waitFor(() => expect(onApplyRootReplacement).toHaveBeenCalledTimes(1));
    expect(onApplyRootReplacement).toHaveBeenCalledWith(replacementAssessment);
    expect(screen.queryByRole('dialog', { name: 'Replace this table’s current setup?' })).toBeNull();
  });

  it('keeps source-field expansion adjacent to current row changes as a separate action', async () => {
    const onExpandSource = vi.fn();
    const { onChooseReshape } = renderSettings({
      sourceCollectionAction: (
        <button type="button" data-testid="construction-reshape-expand-source" onClick={onExpandSource}>
          Expand a repeated source field
        </button>
      ),
    });
    expect(table.document.columns).toHaveLength(0);

    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    const rowChanges = await screen.findByRole('region', { name: 'Choose a row change' });
    const startingRecordRows = within(rowChanges).getByRole('region', { name: 'Starting-record rows' });
    const sourceExpansion = await within(rowChanges).findByTestId('construction-reshape-expand-source');
    const tableExpansion = within(rowChanges).getByTestId('construction-action-expand-rows');
    expect(startingRecordRows.contains(sourceExpansion)).toBe(true);
    expect(startingRecordRows.contains(tableExpansion)).toBe(false);
    fireEvent.click(sourceExpansion);
    expect(onExpandSource).toHaveBeenCalledOnce();
    expect(onChooseReshape).not.toHaveBeenCalled();
  });

  it('opens authored list expansion separately from source collection expansion', async () => {
    const onExpandSource = vi.fn();
    const { onChooseReshape } = renderSettings({
      sourceCollectionAction: (
        <button type="button" data-testid="construction-reshape-expand-source" onClick={onExpandSource}>
          Expand a repeated source field
        </button>
      ),
    });
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    const rowChanges = await screen.findByRole('region', { name: 'Choose a row change' });
    const tableExpansion = within(rowChanges).getByTestId('construction-action-expand-rows');
    const startingRecordRows = within(rowChanges).getByRole('region', { name: 'Starting-record rows' });
    const sourceExpansion = within(startingRecordRows).getByTestId('construction-reshape-expand-source');
    expect(tableExpansion).toHaveTextContent('Make one row per list value');
    expect(sourceExpansion).toHaveTextContent('Expand a repeated source field');
    expect(startingRecordRows.contains(tableExpansion)).toBe(false);

    fireEvent.click(sourceExpansion);
    expect(onExpandSource).toHaveBeenCalledOnce();
    fireEvent.click(tableExpansion);
    expect(onChooseReshape).toHaveBeenCalledWith('expand');
  });

  it('disables authored list expansion when the current stage has no expansion support', async () => {
    renderSettings({ expandSupported: false });
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    const expand = await screen.findByTestId('construction-action-expand-rows');

    expect(expand).toBeDisabled();
    expect(expand).toHaveTextContent('No list column in this table. Use a repeated source field before current row changes.');
    expect(expand.querySelector('.sr-only')).toBeNull();
    expect(expand.querySelector('span.text-xs')).not.toHaveClass('truncate');
  });

  it('shows non-array capability errors in the disabled table expansion action', async () => {
    renderSettings({ expandSupported: false, expandReason: 'Capability lookup failed. Retry after refreshing row capabilities.' });
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    const expand = await screen.findByTestId('construction-action-expand-rows');
    expect(expand).toBeDisabled();
    expect(expand).toHaveTextContent('Capability lookup failed. Retry after refreshing row capabilities.');
    expect(expand.querySelector('span.text-xs')).not.toHaveClass('truncate');
  });

  it('shows one compact Rows card and opens its row and starting-collection settings', async () => {
    renderSettings();

    const trigger = screen.getByTestId('construction-rows-settings-trigger');
    expect(trigger).toHaveTextContent('Change current rows');
    expect(trigger).not.toHaveTextContent('One row per source record');
    expect(trigger).not.toHaveTextContent('Starting collection');
    expect(screen.queryByRole('dialog')).toBeNull();

    fireEvent.click(trigger);
    const dialog = await screen.findByRole('dialog', { name: 'Row definition settings' });
    expect(within(dialog).getByRole('heading', { name: 'Change current rows' })).toBeInTheDocument();
    expect(dialog.contains(screen.getByRole('region', { name: 'Starting record type' }))).toBe(true);
    expect(dialog.contains(screen.getByRole('region', { name: 'Choose a row change' }))).toBe(true);
    expect(screen.queryByText('All authorized Patient records')).not.toBeInTheDocument();
    expect(within(dialog).queryByTestId('construction-starting-collection-footer')).not.toBeInTheDocument();
    expect(dialog.querySelectorAll('details')).toHaveLength(0);
    expect(dialog.contains(await screen.findByRole('combobox', { name: 'Rows per starting record' }))).toBe(true);
  });

  it.each([
    ['an active selection', { selection: sourceSelection }],
    ['an attached population', {
      tableValue: {
        ...table,
        document: {
          ...table.document,
          population: { selectionRevisionId: sourceSelection.id, route: [] },
        },
      },
    }],
  ] as const)('keeps starting collection controls for %s', async (_state, state) => {
    renderSettings({
      ...state,
      startingCollectionSettings: <section aria-label="Starting collection">Collection controls</section>,
    });

    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    const dialog = await screen.findByRole('dialog', { name: 'Row definition settings' });
    const collectionFooter = within(dialog).getByTestId('construction-starting-collection-footer');
    expect(collectionFooter).not.toHaveAttribute('open');
    fireEvent.click(within(collectionFooter).getByText('Starting collection'));
    expect(collectionFooter).toHaveAttribute('open');
    expect(within(collectionFooter).getByText('Collection controls')).toBeInTheDocument();
  });

  it('opens executable related rows from the Rows settings dialog', async () => {
    const { onChooseRelatedRows } = renderSettings({ relatedRowsSupported: true });
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    const related = await screen.findByRole('button', { name: /Make a row for each related record/ });
    expect(related).toHaveTextContent('Follow a relationship; make one row for each match.');
    expect(related.getAttribute('title')).toContain('unmatched rows follow the selected policy');
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
    expect(screen.queryByRole('region', { name: 'Named cohorts' })).not.toBeInTheDocument();
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
    expect(group).toHaveTextContent('Group by matching values in selected fields.');
    expect(group).not.toHaveTextContent('Example:');
    fireEvent.click(group);
    expect(onChooseReshape).toHaveBeenCalledWith('group');
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('opens grouping by a recorded code when the backend offers it', async () => {
    const { onChooseReshape } = renderSettings({ codedGroupDefault: true });
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    const group = await screen.findByTestId('construction-action-group-rows');
    expect(group).toHaveTextContent('Combine rows into groups');
    expect(group).toHaveTextContent('Group rows by one recorded code.');
    expect(group).not.toHaveTextContent('Example:');
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
    expect(pivot).toHaveTextContent('Make columns from distinct categories.');
    expect(pivot.querySelector('span[title]')?.getAttribute('title')).toBe('Make a column for each category. Choose which values fill those columns.');
    expect(pivot).not.toHaveTextContent('Example:');
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

  it('keeps row settings open while the parent starts a root assessment', async () => {
    const onChangeRootSelection = vi.fn();
    const { onChangeRootSelection: onChangeRoot } = renderSettings({
      currentRowMeaning: 'One row per Patient record.',
      onChangeRootSelection,
    });
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    const dialog = await screen.findByRole('dialog', { name: 'Row definition settings' });

    const summary = within(dialog).getByText('One row per Patient record.');
    const startingType = within(dialog).getByRole('region', { name: 'Starting record type' });
    const rowChanges = within(dialog).getByRole('region', { name: 'Choose a row change' });
    const select = within(startingType).getByRole('combobox', { name: 'Record type' });
    expect(summary.closest('header')?.nextElementSibling?.firstElementChild).toBe(startingType);
    expect(startingType.compareDocumentPosition(rowChanges) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(select instanceof HTMLSelectElement && select.value).toBe('base');
    const patientOption = within(startingType).getByRole('option', { name: 'Patient (current)' });
    expect(patientOption instanceof HTMLOptionElement && patientOption.value).toBe('base');

    fireEvent.change(select, { target: { value: 'patient-observation' } });

    expect(onChangeRoot).toHaveBeenCalledWith({ kind: 'authored-occurrence', nodeId: 'observation', occurrenceId: 'patient-observation' });
    expect(dialog).toBeInTheDocument();
    expect(within(dialog).getAllByRole('combobox', { name: 'Record type' })).toHaveLength(1);
  });

  it('explains when the backend has no executable related row path', async () => {
    const { onChooseRelatedRows } = renderSettings();
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    const related = screen.getByRole('button', { name: /Make a row for each related record/ });
    expect(related).toBeDisabled();
    expect(related).toHaveTextContent('No executable route');
    expect(related.querySelector('span.text-xs')).not.toHaveClass('truncate');
    expect(onChooseRelatedRows).not.toHaveBeenCalled();
  });

  it('keeps unavailable grouping reasons visible on the disabled action', async () => {
    renderSettings({ groupSupported: false, groupReason: 'Grouping needs a selected value field.' });
    fireEvent.click(screen.getByTestId('construction-rows-settings-trigger'));
    const group = await screen.findByTestId('construction-action-group-rows');
    expect(group).toBeDisabled();
    expect(group).toHaveTextContent('Grouping needs a selected value field.');
    expect(group.querySelector('span.text-xs')).not.toHaveClass('truncate');
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
    const shape = await screen.findByRole('combobox', { name: 'Rows per starting record' });
    expect(shape instanceof HTMLSelectElement && shape.value).toBe('expanded:expanded-choice');
    const policy = screen.getByRole('combobox', { name: 'Unmatched record policy' });
    expect(policy instanceof HTMLSelectElement && policy.value).toBe('expanded:expanded-choice:EXCLUDE');
  });

  it('defaults repeated values to preserving unmatched records and allows an explicit policy change', async () => {
    const { proposeRowDefinition } = renderSettings();
    fireEvent.click(screen.getByRole('button', { name: 'Change current rows' }));
    const shape = await screen.findByRole('combobox', { name: 'Rows per starting record' });
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
    fireEvent.click(screen.getByRole('button', { name: 'Change current rows' }));
    const shape = await screen.findByRole('combobox', { name: 'Rows per starting record' });
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
    fireEvent.click(screen.getByRole('button', { name: 'Change current rows' }));
    const shape = await screen.findByRole('combobox', { name: 'Rows per starting record' });
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
    fireEvent.click(screen.getByRole('button', { name: 'Change current rows' }));
    await screen.findByRole('combobox', { name: 'Rows per starting record' });
    expect(screen.queryByRole('option', { name: /Status/ })).toBeNull();
  });

  it('previews and applies only the server-issued explicit-group proposal', async () => {
    const { listRowDefinitionChoices, proposeRowDefinition, onApply } = renderSettings();
    expect(screen.getByTestId('construction-rows-settings-trigger')).toHaveTextContent('Change current rows');
    fireEvent.click(screen.getByRole('button', { name: 'Change current rows' }));
    const select = await screen.findByRole('combobox', { name: 'Rows per starting record' });
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
    fireEvent.click(screen.getByRole('button', { name: 'Change current rows' }));
    await screen.findByRole('combobox', { name: 'Rows per starting record' });

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
    fireEvent.click(screen.getByRole('button', { name: 'Change current rows' }));
    await screen.findByRole('combobox', { name: 'Rows per starting record' });

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
    fireEvent.click(screen.getByRole('button', { name: 'Change current rows' }));
    await screen.findByRole('combobox', { name: 'Rows per starting record' });

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
        {...idleRootChangeProps()}
        {...relatedRowProps}
        client={{ listRowDefinitionChoices, proposeRowDefinition, getSelection, createExplicitGroupRevision }}
        project="project-a"
        explorerId="explorer-a"
        snapshotToken="snapshot-1"
        draftVersion={4}
        draftDigest="draft-digest-4"
        table={table}
        workspaceTables={[table]}
        selection={sourceSelection}
        disabled={false}
        onApply={onApply}
        onApplyRootReplacement={vi.fn().mockResolvedValue(true)}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Change current rows' }));
    await screen.findByRole('combobox', { name: 'Rows per starting record' });
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
        {...idleRootChangeProps()}
        {...relatedRowProps}
        client={{ listRowDefinitionChoices, proposeRowDefinition, getSelection, createExplicitGroupRevision }}
        project="project-a"
        explorerId="explorer-a"
        snapshotToken="snapshot-1"
        draftVersion={4}
        draftDigest="draft-digest-4"
        table={table}
        workspaceTables={[table]}
        selection={sourceSelection}
        disabled={false}
        onApply={onApply}
        onApplyRootReplacement={vi.fn().mockResolvedValue(true)}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Change current rows' }));
    await screen.findByRole('combobox', { name: 'Rows per starting record' });
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
        {...idleRootChangeProps()}
        {...relatedRowProps}
        client={{ listRowDefinitionChoices, proposeRowDefinition: vi.fn().mockResolvedValue(proposal), getSelection, createExplicitGroupRevision }}
        project="project-a" explorerId="explorer-a" snapshotToken="snapshot-1" draftVersion={4} draftDigest="draft-digest-4"
        table={table} workspaceTables={[table]} selection={sourceSelection} disabled={false} onApply={vi.fn().mockResolvedValue(true)} onApplyRootReplacement={vi.fn().mockResolvedValue(true)}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Change current rows' }));
    await screen.findByRole('combobox', { name: 'Rows per starting record' });
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
        {...idleRootChangeProps()}
        {...relatedRowProps}
        client={{ listRowDefinitionChoices, proposeRowDefinition: vi.fn().mockResolvedValue(proposal), getSelection: vi.fn().mockResolvedValue(selectionPage), createExplicitGroupRevision }}
        project="project-a" explorerId="explorer-a" snapshotToken="snapshot-1" draftVersion={4} draftDigest="draft-digest-4"
        table={table} workspaceTables={[table]} selection={sourceSelection} disabled={false} onApply={vi.fn().mockResolvedValue(true)} onApplyRootReplacement={vi.fn().mockResolvedValue(true)}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Change current rows' }));
    await screen.findByRole('combobox', { name: 'Rows per starting record' });
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
      ...idleRootChangeProps(),
      client: { listRowDefinitionChoices, proposeRowDefinition, getSelection: vi.fn(), createExplicitGroupRevision: vi.fn() },
      project: 'project-a', explorerId: 'explorer-a', snapshotToken: 'snapshot-1',
      draftVersion: 4, draftDigest: 'draft-digest-4', table, workspaceTables: [table], disabled: false, onApply,
      onApplyRootReplacement: vi.fn().mockResolvedValue(true),
      ...relatedRowProps,
    };
    const view = render(<RowDefinitionSettingsPanel {...props} />);
    fireEvent.click(screen.getByRole('button', { name: 'Change current rows' }));
    fireEvent.change(await screen.findByRole('combobox', { name: 'Rows per starting record' }), {
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
    fireEvent.click(screen.getByRole('button', { name: 'Change current rows' }));
    fireEvent.change(await screen.findByRole('combobox', { name: 'Rows per starting record' }), {
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
        {...idleRootChangeProps()}
        {...relatedRowProps}
        client={{ listRowDefinitionChoices, proposeRowDefinition, getSelection: vi.fn(), createExplicitGroupRevision: vi.fn() }}
        project="project-a"
        explorerId="explorer-a"
        snapshotToken="snapshot-1"
        draftVersion={4}
        draftDigest="draft-digest-4"
        table={table}
        workspaceTables={[table]}
        disabled={false}
        onApply={onApply}
        onApplyRootReplacement={vi.fn().mockResolvedValue(true)}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Change current rows' }));
    fireEvent.change(await screen.findByRole('combobox', { name: 'Rows per starting record' }), {
      target: { value: 'expanded:expanded-choice' },
    });
    expect((await screen.findByRole('alert')).textContent).toContain(serverError.message);
    expect(screen.getByRole('combobox', { name: 'Rows per starting record' })).toBeEnabled();
    expect(screen.getByRole('combobox', { name: 'Unmatched record policy' })).toBeEnabled();
    fireEvent.click(screen.getByRole('button', { name: 'Back to table' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(onApply).not.toHaveBeenCalled();
  });

  it('repairs a missing-value validation failure by changing policy in the same dialog', async () => {
    const { proposeRowDefinition, onApply } = renderSettings();
    proposeRowDefinition.mockRejectedValueOnce(Object.assign(new Error('Some records have no values for this field. Choose another missing-value policy.'), { status: 422, code: 'EMPTY_COLLECTION_ERROR' }));
    fireEvent.click(screen.getByRole('button', { name: 'Change current rows' }));
    fireEvent.change(await screen.findByRole('combobox', { name: 'Rows per starting record' }), { target: { value: 'expanded:expanded-choice' } });
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
