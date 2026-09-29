// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  type ConstructionReshapeEditorProps,
  type ConstructionReshapeStep,
} from './ConstructionReshapeEditor';
import { constructionSchema } from '../../../types';
import { ConstructionReshapeEditor } from './ConstructionReshapeEditor';

const sourceColumns: ConstructionReshapeEditorProps['capabilities']['selectedStage']['columns'] = [
  { id: 'site-id', name: 'site', label: 'Site', type: 'string', cardinality: 'required_one' },
  { id: 'age-id', name: 'age', label: 'Age', type: 'integer', cardinality: 'optional_one' },
  { id: 'tags-id', name: 'tags', label: 'Tags', type: 'string', cardinality: 'many' },
];

const sourceStage: ConstructionReshapeEditorProps['capabilities']['selectedStage'] = {
  id: 'source_projection',
  inputStageId: '',
  columns: sourceColumns,
  capabilities: [
    { kind: 'FILTER', supported: true },
    { kind: 'DERIVE', supported: true },
    { kind: 'PIVOT', supported: false, reason: 'Not needed in this test.' },
    { kind: 'UNPIVOT', supported: false, reason: 'Not needed in this test.' },
    { kind: 'GROUP', supported: true },
    { kind: 'EXPAND', supported: true },
  ],
};

const capabilitiesFor = (
  stages: ConstructionReshapeEditorProps['capabilities']['stages'] = [sourceStage],
  selectedStage = sourceStage,
): ConstructionReshapeEditorProps['capabilities'] => ({
  snapshotToken: 'snapshot-1',
  draftVersion: 1,
  draftDigest: 'draft-digest-1',
  outputId: 'table-1',
  stageId: selectedStage.id,
  baseConstruction: { version: 1, steps: [] },
  stages,
  selectedStage,
});

const renderEditor = (args: {
  readonly construction?: ConstructionReshapeEditorProps['construction'];
  readonly capabilities?: ConstructionReshapeEditorProps['capabilities'];
  readonly editingStep?: ConstructionReshapeStep;
  readonly initialKind?: ConstructionReshapeEditorProps['initialKind'];
  readonly selectedColumns?: ReadonlyArray<string>;
  readonly onCandidateChange?: ConstructionReshapeEditorProps['onCandidateChange'];
  readonly onDiscoverCategories?: ConstructionReshapeEditorProps['onDiscoverCategories'];
  readonly onAddCodedValues?: ConstructionReshapeEditorProps['onAddCodedValues'];
  readonly disabled?: boolean;
  readonly onEditStep?: ConstructionReshapeEditorProps['onEditStep'];
}) => {
  const onCandidateChange = args.onCandidateChange ?? vi.fn();
  const onEditStep = args.onEditStep ?? vi.fn();
  render(
    <ConstructionReshapeEditor
      construction={args.construction ?? { version: 1, steps: [] }}
      capabilities={args.capabilities ?? capabilitiesFor()}
      editingStep={args.editingStep}
      initialKind={args.initialKind}
      selectedColumns={args.selectedColumns}
      onDiscoverCategories={args.onDiscoverCategories}
      onAddCodedValues={args.onAddCodedValues}
      disabled={args.disabled ?? false}
      onCandidateChange={onCandidateChange}
      onEditStep={onEditStep}
    />,
  );
  return { onCandidateChange, onEditStep };
};

const controlValue = (label: string): string => {
  const control = screen.getByLabelText(label);
  return control instanceof HTMLInputElement || control instanceof HTMLSelectElement ? control.value : '';
};

const controlChecked = (label: string): boolean => {
  const control = screen.getByLabelText(label);
  return control instanceof HTMLInputElement && control.checked;
};

const assertCandidateMatchesSchemaAnd = (
  onCandidateChange: ReturnType<typeof vi.fn>,
  expectedOperation: Record<string, unknown>,
) => {
  const intent = onCandidateChange.mock.lastCall?.[0];
  expect(intent).toBeDefined();
  if (!intent) throw new Error('Expected a candidate construction');
  expect(constructionSchema.parse(intent.candidateConstruction)).toEqual(intent.candidateConstruction);
  expect(intent.candidateConstruction.steps.at(-1)?.operation).toEqual(expectedOperation);
};

afterEach(cleanup);

describe('ConstructionReshapeEditor', () => {
  it('resolves the neutral categories entry to coded pivot when capabilities support it', () => {
    const codedStage = {
      ...sourceStage,
      capabilities: [...sourceStage.capabilities, { kind: 'CODED_PIVOT' as const, supported: true }],
    } satisfies ConstructionReshapeEditorProps['capabilities']['selectedStage'];
    renderEditor({ initialKind: 'categories', capabilities: capabilitiesFor([codedStage], codedStage) });

    expect(screen.getByText(/Coded source discovery is unavailable for these starting records/)).toBeInTheDocument();
  });

  it('resolves the neutral categories entry to table pivot when coded pivot is unsupported', () => {
    renderEditor({ initialKind: 'categories' });

    expect(screen.getByRole('region', { name: 'Pivot categories into columns' })).toBeInTheDocument();
    expect(screen.getByText('Not needed in this test.')).toBeInTheDocument();
  });

  it('opens an unsupported table pivot and explains the capability limit inside the editor', () => {
    const unsupportedPivotStage = {
      ...sourceStage,
      capabilities: sourceStage.capabilities.map((capability) => capability.kind === 'PIVOT'
        ? { ...capability, reasonCode: 'INSUFFICIENT_SCALAR_COLUMNS', reason: 'pivot requires public scalar group, category, and value columns' }
        : capability),
    } satisfies ConstructionReshapeEditorProps['capabilities']['selectedStage'];
    renderEditor({ initialKind: 'pivot', capabilities: capabilitiesFor([unsupportedPivotStage], unsupportedPivotStage) });

    expect(screen.getByRole('region', { name: 'Pivot categories into columns' })).toBeInTheDocument();
    expect(screen.getByText(/needs a field to keep each row grouped, a category field, and a value field/)).toBeInTheDocument();
    expect(screen.queryByText(/public scalar/)).not.toBeInTheDocument();
    expect(screen.getByLabelText('Pivot category field')).toBeDisabled();
  });

  it('opens the requested row operation without another choice click', () => {
    renderEditor({ initialKind: 'group' });
    expect(screen.getByRole('region', { name: 'Summarize into groups' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Change row operation' }));
    expect(screen.getByTestId('construction-reshape-choice-pivot')).toBeInTheDocument();
    cleanup();
    renderEditor({ initialKind: 'pivot' });
    expect(screen.getByRole('region', { name: 'Pivot categories into columns' })).toBeInTheDocument();
  });

  it('offers only server-authorized Coding paths and reopens the saved row grouping', () => {
    const codedStage = {
      ...sourceStage,
      capabilities: [...sourceStage.capabilities, { kind: 'CODED_GROUP' as const, supported: true }],
      codedGroupChoices: [
        { choiceId: 'signed-type', occurrenceId: 'root-1', resourceType: 'Specimen', codingPath: 'type.coding[]', label: 'Specimen type' },
        { choiceId: 'signed-other', occurrenceId: 'root-1', resourceType: 'Specimen', codingPath: 'collection.method.coding[]', label: 'Collection method' },
      ],
    } satisfies ConstructionReshapeEditorProps['capabilities']['selectedStage'];
    const capabilities = capabilitiesFor([codedStage], codedStage);
    const onCandidateChange = vi.fn();
    renderEditor({ capabilities, onCandidateChange });
    fireEvent.click(screen.getByTestId('construction-reshape-choice-coded-group'));
    const first = onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0];
    expect(first?.operation.kind).toBe('CODED_GROUP');
    if (first?.operation.kind !== 'CODED_GROUP') throw new Error('Expected coded grouping');
    expect(first.operation.codedGroup.source).toMatchObject({ codingPath: 'type.coding[]', occurrenceId: 'root-1' });
    expect(first.outputs.map((column: { label: string }) => column.label)).toEqual(['Code system', 'Code version', 'Code', 'Source records']);

    fireEvent.change(screen.getByTestId('construction-coded-group-path'), { target: { value: 'collection.method.coding[]' } });
    const changed = onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0];
    expect(changed?.operation.kind).toBe('CODED_GROUP');
    if (changed?.operation.kind !== 'CODED_GROUP') throw new Error('Expected coded grouping');
    expect(changed.operation.codedGroup.source.codingPath).toBe('collection.method.coding[]');
    expect(changed.operation.codedGroup.choiceId).toBe('signed-other');

    cleanup();
    renderEditor({ construction: { version: 1, steps: [changed] }, capabilities, editingStep: changed });
    expect(controlValue('Coding field')).toBe('collection.method.coding[]');
    expect(screen.getByText(/current columns leave this table/i)).toBeInTheDocument();
  });

  it('proposes a coded group when opened directly from Rows', () => {
    const codedStage = {
      ...sourceStage,
      capabilities: [...sourceStage.capabilities, { kind: 'CODED_GROUP' as const, supported: true }],
      codedGroupChoices: [
        { choiceId: 'signed-type', occurrenceId: 'root-1', resourceType: 'Specimen', codingPath: 'type.coding[]', label: 'Specimen type' },
      ],
    } satisfies ConstructionReshapeEditorProps['capabilities']['selectedStage'];
    const onCandidateChange = vi.fn();
    renderEditor({ capabilities: capabilitiesFor([codedStage], codedStage), initialKind: 'coded-group', onCandidateChange, onAddCodedValues: vi.fn() });
    expect(screen.getByTestId('construction-reshape-coded-group')).toBeInTheDocument();
    expect(screen.queryByText('Need a coded-value column first?')).not.toBeInTheDocument();
    const step = onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0];
    expect(step?.operation.kind).toBe('CODED_GROUP');
    expect(step?.operation.codedGroup.source.codingPath).toBe('type.coding[]');
    expect(screen.queryByTestId('construction-reshape-choice-group')).not.toBeInTheDocument();
  });
  it('groups by a server-selected source field before that field becomes a table column', () => {
    const capabilities = {
      ...capabilitiesFor(),
      sourceInput: {
        supported: true,
        stageId: 'source_projection',
        choices: [
          { choiceId: 'status-choice', occurrenceId: 'root-1', fieldPath: 'status', label: 'Status', fhirType: 'code', logicalType: 'string', valueType: 'string', isIdentifier: false, isReference: false, isPopulated: true },
          { choiceId: 'gender-choice', occurrenceId: 'root-1', fieldPath: 'gender', label: 'Gender', fhirType: 'code', logicalType: 'string', valueType: 'string', isIdentifier: false, isReference: false, isPopulated: true },
        ],
      },
    } satisfies ConstructionReshapeEditorProps['capabilities'];
    const onCandidateChange = vi.fn();
    renderEditor({ capabilities, initialKind: 'source-group', onCandidateChange });
    expect(screen.getByTestId('construction-reshape-source-group')).toBeInTheDocument();
    const intent = onCandidateChange.mock.lastCall?.[0];
    expect(intent?.groupSource).toEqual({ rowChoiceId: 'status-choice', columnId: expect.any(String) });
    expect(intent?.candidateConstruction.steps[0].operation.group.aggregates).toEqual([
      { operation: 'COUNT_ROWS', outputColumnId: expect.any(String) },
    ]);
    expect(intent?.candidateConstruction.steps[0].outputs.map((column: { label: string }) => column.label)).toEqual(['Status', 'Source records']);
    fireEvent.change(screen.getByTestId('construction-source-group-field'), { target: { value: 'gender-choice' } });
    expect(onCandidateChange.mock.lastCall?.[0]?.groupSource.rowChoiceId).toBe('gender-choice');
    expect(onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0].outputs[0].label).toBe('Gender');
  });
  it('shows why backend capability choices are unavailable and emits no candidate', () => {
    const unsupportedStage = {
      ...sourceStage,
      capabilities: sourceStage.capabilities.map((capability) =>
        capability.kind === 'GROUP' || capability.kind === 'EXPAND'
          ? { ...capability, supported: false, reason: `${capability.kind} was rejected for this stage.` }
          : capability,
      ),
    } satisfies ConstructionReshapeEditorProps['capabilities']['selectedStage'];
    const onCandidateChange = vi.fn();
    renderEditor({ capabilities: capabilitiesFor([unsupportedStage], unsupportedStage), onCandidateChange });

    expect(screen.getByTestId('construction-reshape-choice-group')).toBeDisabled();
    expect(screen.getByText('GROUP was rejected for this stage.')).toBeInTheDocument();
    expect(screen.getByTestId('construction-reshape-choice-expand')).toBeDisabled();
    expect(screen.getByText('EXPAND was rejected for this stage.')).toBeInTheDocument();
    expect(screen.getByTestId('construction-reshape-choice-related-expand')).toBeDisabled();
    expect(onCandidateChange).not.toHaveBeenCalledWith(expect.objectContaining({ candidateConstruction: expect.anything() }));
  });

  it('offers paired coded concepts beside Pivot inputs and keeps them distinct from current table columns', () => {
    const pivotStage = {
      ...sourceStage,
      capabilities: sourceStage.capabilities.map((capability) => ({ ...capability, supported: capability.kind === 'PIVOT' })),
    } satisfies ConstructionReshapeEditorProps['capabilities']['selectedStage'];
    const onAddCodedValues = vi.fn();
    renderEditor({
      capabilities: capabilitiesFor([pivotStage], pivotStage),
      onDiscoverCategories: vi.fn(),
      onAddCodedValues,
    });

    fireEvent.click(screen.getByRole('button', { name: 'Add coded-value column' }));
    expect(onAddCodedValues).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByTestId('construction-reshape-choice-pivot'));

    expect(screen.getByLabelText('Pivot category field')).toBeInTheDocument();
    expect(screen.getByLabelText('Pivot values field')).toBeInTheDocument();
    expect(screen.getByText(/category and values selectors use columns already in this table/i)).toBeInTheDocument();
    expect(screen.getByText(/paired coded concept adds a separate column/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Add coded-value column' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add a paired coded concept' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Add a paired coded concept' }));
    expect(onAddCodedValues).toHaveBeenCalledTimes(2);
  });

  it('builds a whole-table group summary with editable stable outputs', () => {
    const onCandidateChange = vi.fn();
    renderEditor({ onCandidateChange });

    fireEvent.click(screen.getByTestId('construction-reshape-choice-group'));
    expect(screen.getByTestId('construction-reshape-group')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Change row operation' })).toBeInTheDocument();
    expect(screen.queryByLabelText('Group by Tags')).not.toBeInTheDocument();
    expect(controlValue('Summary 1')).toBe('COUNT_ROWS');
    const advanced = screen.getByTestId('construction-reshape-group-advanced');
    expect(advanced).not.toHaveAttribute('open');
    expect(screen.getByLabelText('Summary output name 1')).toBeInTheDocument();
    fireEvent.click(screen.getByText('Advanced options'));
    expect(advanced).toHaveAttribute('open');
    fireEvent.change(screen.getByLabelText('Summary output name 1'), { target: { value: 'participant_count' } });
    fireEvent.change(screen.getByLabelText('Summary output label 1'), { target: { value: 'Participant count' } });

    assertCandidateMatchesSchemaAnd(onCandidateChange, {
      kind: 'GROUP',
      group: {
        constructionId: expect.any(String),
        missingKeyPolicy: 'GROUP',
        keys: [],
        aggregates: [{ operation: 'COUNT_ROWS', outputColumnId: expect.any(String) }],
      },
    });
  });

  it('uses selected scalar columns as group keys and restricts numeric summaries by type', () => {
    const onCandidateChange = vi.fn();
    renderEditor({ selectedColumns: ['site-id'], onCandidateChange });
    fireEvent.click(screen.getByTestId('construction-reshape-choice-group'));

    expect(controlChecked('Group by Site')).toBe(true);
    expect(screen.getByLabelText('Summary 1')).toBeInTheDocument();
    expect(screen.getByTestId('construction-reshape-group-missing-key-effect')).toHaveTextContent(
      'those rows stay together in one missing-key group',
    );
    const advanced = screen.getByTestId('construction-reshape-group-advanced');
    expect(advanced).not.toHaveAttribute('open');
    fireEvent.click(screen.getByText('Advanced options'));
    expect(controlValue('Missing group key policy')).toBe('GROUP');
    fireEvent.change(screen.getByLabelText('Missing group key policy'), { target: { value: 'EXCLUDE' } });
    expect(screen.getByTestId('construction-reshape-group-missing-key-effect')).toHaveTextContent(
      'rows with any missing key are excluded before summaries run',
    );
    expect(screen.queryByLabelText('Group by Tags')).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('Advanced options'));
    fireEvent.change(screen.getByLabelText('Summary 1'), { target: { value: 'MEAN' } });
    expect(advanced).not.toHaveAttribute('open');
    expect(screen.getByLabelText('Summary field 1')).toBeInTheDocument();
    fireEvent.click(screen.getByText('Advanced options'));
    fireEvent.change(screen.getByLabelText('Summary output name 1'), { target: { value: 'mean_age' } });
    fireEvent.click(screen.getByTestId('construction-reshape-add-summary'));
    fireEvent.change(screen.getByLabelText('Summary 2'), { target: { value: 'SUM' } });
    fireEvent.change(screen.getByLabelText('Summary output name 2'), { target: { value: 'total_age' } });
    fireEvent.change(screen.getByLabelText('Summary field 2'), { target: { value: 'age-id' } });

    const intent = onCandidateChange.mock.lastCall?.[0];
    expect(intent).toBeDefined();
    if (!intent) throw new Error('Expected a candidate construction');
    const parsed = constructionSchema.parse(intent.candidateConstruction);
    expect(parsed.steps.at(-1)?.operation).toMatchObject({
      kind: 'GROUP',
      group: {
        missingKeyPolicy: 'EXCLUDE',
        keys: [{ inputColumnId: 'site-id', outputColumnId: expect.any(String) }],
        aggregates: [
          { operation: 'MEAN', inputColumnId: 'age-id', outputColumnId: expect.any(String) },
          { operation: 'SUM', inputColumnId: 'age-id', outputColumnId: expect.any(String) },
        ],
      },
    });
  });

  it('adds a group after an intermediate step and retains generated identities while editing labels', () => {
    const priorStep: ConstructionReshapeStep = {
      id: 'filter-specimens',
      inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: { kind: 'FILTER', filter: { columnId: 'site-id', operator: 'EXISTS' } },
      outputs: sourceColumns.map(({ id, name, label, type }) => ({ id, name, label, ...(type ? { type } : {}) })),
    };
    const construction = { version: 1, steps: [priorStep] } satisfies ConstructionReshapeEditorProps['construction'];
    const filteredStage = {
      ...sourceStage,
      id: priorStep.id,
      inputStageId: sourceStage.id,
      operation: 'FILTER',
    } satisfies ConstructionReshapeEditorProps['capabilities']['stages'][number];
    const onCandidateChange = vi.fn<ConstructionReshapeEditorProps['onCandidateChange']>();
    renderEditor({
      construction,
      capabilities: capabilitiesFor([sourceStage, filteredStage], filteredStage),
      selectedColumns: ['site-id'],
      onCandidateChange,
    });

    fireEvent.click(screen.getByTestId('construction-reshape-choice-group'));
    fireEvent.click(screen.getByText('Advanced options'));
    fireEvent.change(screen.getByLabelText('Summary output label 1'), { target: { value: 'Specimen rows' } });
    const firstIntent = onCandidateChange.mock.lastCall?.[0];
    expect(firstIntent).toBeDefined();
    if (!firstIntent) throw new Error('Expected a group proposal candidate');
    const firstGroup = firstIntent.candidateConstruction.steps.at(-1);
    expect(firstGroup?.inputs).toEqual([{ kind: 'STEP_OUTPUT', stepId: priorStep.id }]);

    fireEvent.change(screen.getByLabelText('Summary output label 1'), { target: { value: 'Updated specimen rows' } });
    const editedIntent = onCandidateChange.mock.lastCall?.[0];
    expect(editedIntent).toBeDefined();
    if (!editedIntent) throw new Error('Expected an updated group proposal candidate');
    const editedGroup = editedIntent.candidateConstruction.steps.at(-1);
    expect(editedGroup?.id).toBe(firstGroup?.id);
    expect(editedGroup?.operation).toMatchObject({
      kind: 'GROUP',
      group: {
        keys: [{ inputColumnId: 'site-id', outputColumnId: expect.any(String) }],
        aggregates: [{ operation: 'COUNT_ROWS', outputColumnId: expect.any(String) }],
      },
    });
    expect(editedGroup?.outputs.map((column) => column.id)).toEqual(firstGroup?.outputs.map((column) => column.id));
    expect(editedGroup?.outputs.at(-1)?.label).toBe('Updated specimen rows');
  });

  it('requires a summary before proposing a group with no calculated outputs', () => {
    const onCandidateChange = vi.fn();
    renderEditor({ onCandidateChange });
    fireEvent.click(screen.getByTestId('construction-reshape-choice-group'));
    fireEvent.click(screen.getByRole('button', { name: 'Remove summary 1' }));

    expect(screen.getByRole('status')).toHaveTextContent('Add at least one summary before proposing this group.');
    expect(onCandidateChange).toHaveBeenLastCalledWith(undefined);
  });

  it('reopens and edits a saved group without replacing its step or output identities', () => {
    const groupStep: ConstructionReshapeStep = {
      id: 'saved-group',
      inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: {
        kind: 'GROUP',
        group: {
          constructionId: 'saved-group',
          missingKeyPolicy: 'EXCLUDE',
          keys: [{ inputColumnId: 'site-id', outputColumnId: 'site-group-id' }],
          aggregates: [{ operation: 'COUNT_NON_NULL', inputColumnId: 'age-id', outputColumnId: 'age-count-id' }],
        },
      },
      outputs: [
        { id: 'site-group-id', name: 'study_site', label: 'Study site' },
        { id: 'age-count-id', name: 'recorded_age_count', label: 'Recorded age count', type: 'integer' },
      ],
    };
    const onCandidateChange = vi.fn<ConstructionReshapeEditorProps['onCandidateChange']>();
    renderEditor({
      construction: { version: 1, steps: [groupStep] },
      capabilities: capabilitiesFor(),
      editingStep: groupStep,
      onCandidateChange,
    });

    expect(controlChecked('Group by Site')).toBe(true);
    expect(screen.getByTestId('construction-reshape-group-missing-key-effect')).toHaveTextContent(
      'rows with any missing key are excluded before summaries run',
    );
    expect(screen.getByTestId('construction-reshape-group-advanced')).not.toHaveAttribute('open');
    fireEvent.click(screen.getByText('Advanced options'));
    expect(controlValue('Missing group key policy')).toBe('EXCLUDE');
    expect(controlValue('Group output name 1')).toBe('study_site');
    expect(controlValue('Group output label 1')).toBe('Study site');
    expect(controlValue('Summary 1')).toBe('COUNT_NON_NULL');
    expect(controlValue('Summary field 1')).toBe('age-id');
    expect(controlValue('Summary output name 1')).toBe('recorded_age_count');
    expect(controlValue('Summary output label 1')).toBe('Recorded age count');
    fireEvent.change(screen.getByLabelText('Summary output label 1'), { target: { value: 'Populated ages' } });

    const intent = onCandidateChange.mock.lastCall?.[0];
    expect(intent).toBeDefined();
    if (!intent) throw new Error('Expected an edited group proposal candidate');
    expect(intent.changedStepId).toBe(groupStep.id);
    const editedGroup = intent.candidateConstruction.steps[0];
    expect(editedGroup?.id).toBe(groupStep.id);
    expect(editedGroup?.operation).toMatchObject({
      kind: 'GROUP',
      group: {
        constructionId: groupStep.id,
        missingKeyPolicy: 'EXCLUDE',
        keys: [{ inputColumnId: 'site-id', outputColumnId: 'site-group-id' }],
        aggregates: [{ operation: 'COUNT_NON_NULL', inputColumnId: 'age-id', outputColumnId: 'age-count-id' }],
      },
    });
    expect(editedGroup?.outputs).toContainEqual(expect.objectContaining({ id: 'site-group-id', name: 'study_site', label: 'Study site' }));
    expect(editedGroup?.outputs).toContainEqual(expect.objectContaining({ id: 'age-count-id', name: 'recorded_age_count', label: 'Populated ages' }));
  });

  it('disables primary and advanced group controls when editing is disabled', () => {
    const groupStep: ConstructionReshapeStep = {
      id: 'disabled-group',
      inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: {
        kind: 'GROUP',
        group: {
          constructionId: 'disabled-group',
          missingKeyPolicy: 'ERROR',
          keys: [{ inputColumnId: 'site-id', outputColumnId: 'disabled-site-id' }],
          aggregates: [{ operation: 'COUNT_NON_NULL', inputColumnId: 'age-id', outputColumnId: 'disabled-age-count-id' }],
        },
      },
      outputs: [
        { id: 'disabled-site-id', name: 'site', label: 'Site' },
        { id: 'disabled-age-count-id', name: 'age_count', label: 'Age count', type: 'integer' },
      ],
    };
    renderEditor({
      construction: { version: 1, steps: [groupStep] },
      editingStep: groupStep,
      disabled: true,
    });

    expect(screen.getByLabelText('Group by Site')).toBeDisabled();
    expect(screen.getByLabelText('Summary 1')).toBeDisabled();
    expect(screen.getByLabelText('Summary field 1')).toBeDisabled();
    expect(screen.getByTestId('construction-reshape-add-summary')).toBeDisabled();
    fireEvent.click(screen.getByText('Advanced options'));
    expect(screen.getByLabelText('Missing group key policy')).toBeDisabled();
    expect(screen.getByLabelText('Group output name 1')).toBeDisabled();
    expect(screen.getByLabelText('Group output label 1')).toBeDisabled();
    expect(screen.getByLabelText('Summary output name 1')).toBeDisabled();
    expect(screen.getByLabelText('Summary output label 1')).toBeDisabled();
  });

  it('keeps empty-list parent rows by default and offers a zero-based position column in Advanced options', () => {
    const onCandidateChange = vi.fn();
    renderEditor({ onCandidateChange });
    fireEvent.click(screen.getByTestId('construction-reshape-choice-expand'));

    expect(controlValue('Repeated field')).toBe('tags-id');
    expect(controlValue('Empty list policy')).toBe('PRESERVE_PARENT');
    expect(screen.getByTestId('construction-reshape-expand-empty-effect')).toHaveTextContent('keep the original row with an empty item');
    const advanced = screen.getByTestId('construction-reshape-expand-advanced');
    expect(advanced).not.toHaveAttribute('open');
    expect(controlChecked('Include item position')).toBe(false);
    fireEvent.click(screen.getByText('Advanced options'));
    expect(advanced).toHaveAttribute('open');
    fireEvent.click(screen.getByLabelText('Include item position'));
    fireEvent.change(screen.getByLabelText('Expanded item name'), { target: { value: 'tag_item' } });

    const intent = onCandidateChange.mock.lastCall?.[0];
    expect(intent).toBeDefined();
    if (!intent) throw new Error('Expected a candidate construction');
    const parsed = constructionSchema.parse(intent.candidateConstruction);
    expect(parsed.steps.at(-1)?.operation).toMatchObject({
      kind: 'EXPAND',
      expand: {
        constructionId: expect.any(String),
        inputColumnId: 'tags-id',
        outputColumnId: expect.any(String),
        ordinalColumnId: expect.any(String),
        emptyPolicy: 'PRESERVE_PARENT',
      },
    });
    expect(parsed.steps.at(-1)?.outputs).toContainEqual(expect.objectContaining({ name: 'tag_item' }));
    fireEvent.change(screen.getByLabelText('Empty list policy'), { target: { value: 'EXCLUDE' } });
    expect(screen.getByTestId('construction-reshape-expand-empty-effect')).toHaveTextContent('leave out the original row');
  });

  it('expands only list-cardinality columns at an intermediate stage and preserves output IDs', () => {
    const priorStep: ConstructionReshapeStep = {
      id: 'filter-specimens',
      inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: { kind: 'FILTER', filter: { columnId: 'site-id', operator: 'EXISTS' } },
      outputs: sourceColumns.map(({ id, name, label, type }) => ({ id, name, label, ...(type ? { type } : {}) })),
    };
    const construction = { version: 1, steps: [priorStep] } satisfies ConstructionReshapeEditorProps['construction'];
    const filteredStage = {
      ...sourceStage,
      id: priorStep.id,
      inputStageId: sourceStage.id,
      operation: 'FILTER',
    } satisfies ConstructionReshapeEditorProps['capabilities']['stages'][number];
    const onCandidateChange = vi.fn<ConstructionReshapeEditorProps['onCandidateChange']>();
    renderEditor({
      construction,
      capabilities: capabilitiesFor([sourceStage, filteredStage], filteredStage),
      onCandidateChange,
    });

    fireEvent.click(screen.getByTestId('construction-reshape-choice-expand'));
    const repeatedField = screen.getByLabelText('Repeated field');
    expect(repeatedField.querySelectorAll('option')).toHaveLength(1);
    expect(controlValue('Repeated field')).toBe('tags-id');
    const firstIntent = onCandidateChange.mock.lastCall?.[0];
    expect(firstIntent).toBeDefined();
    if (!firstIntent) throw new Error('Expected an expand proposal candidate');
    const firstExpand = firstIntent.candidateConstruction.steps.at(-1);
    expect(firstExpand?.inputs).toEqual([{ kind: 'STEP_OUTPUT', stepId: priorStep.id }]);
    const firstExpandOutputId = firstExpand?.operation.kind === 'EXPAND'
      ? firstExpand.operation.expand.outputColumnId
      : undefined;
    const outputIds = firstExpand?.outputs.map((column) => column.id);

    fireEvent.click(screen.getByText('Advanced options'));
    fireEvent.change(screen.getByLabelText('Expanded item label'), { target: { value: 'Observed tag' } });
    const editedIntent = onCandidateChange.mock.lastCall?.[0];
    expect(editedIntent).toBeDefined();
    if (!editedIntent) throw new Error('Expected an updated expand proposal candidate');
    const editedExpand = editedIntent.candidateConstruction.steps.at(-1);
    expect(editedExpand?.id).toBe(firstExpand?.id);
    expect(editedExpand?.outputs.map((column) => column.id)).toEqual(outputIds);
    expect(editedExpand?.outputs).toContainEqual(expect.objectContaining({ id: firstExpandOutputId, label: 'Observed tag' }));
  });

  it('does not offer expansion when the stage has no many-cardinality columns', () => {
    const scalarOnlyStage = {
      ...sourceStage,
      columns: sourceColumns.filter((column) => column.cardinality !== 'many'),
    } satisfies ConstructionReshapeEditorProps['capabilities']['selectedStage'];
    renderEditor({
      capabilities: capabilitiesFor([scalarOnlyStage], scalarOnlyStage),
    });

    expect(screen.getByTestId('construction-reshape-choice-expand')).toBeDisabled();
    expect(screen.getByText('Add a column with multiple values before expanding it.')).toBeInTheDocument();
  });

  it('reopens an existing expand with its stable IDs, names, policy, and history edit action', () => {
    const expandStep: ConstructionReshapeStep = {
      id: 'expand-saved',
      inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: {
        kind: 'EXPAND',
        expand: {
          constructionId: 'expand-saved',
          inputColumnId: 'tags-id',
          outputColumnId: 'tag-value-id',
          ordinalColumnId: 'tag-position-id',
          emptyPolicy: 'PRESERVE_PARENT',
        },
      },
      outputs: [
        { id: 'site-id', name: 'site', label: 'Site', type: 'string' },
        { id: 'tag-value-id', name: 'tag_value', label: 'Tag value', type: 'string' },
        { id: 'tag-position-id', name: 'tag_position', label: 'Tag position', type: 'integer' },
        { id: 'age-id', name: 'age', label: 'Age', type: 'integer' },
      ],
    };
    const afterExpand = {
      ...sourceStage,
      id: expandStep.id,
      inputStageId: sourceStage.id,
      operation: 'EXPAND',
      columns: expandStep.outputs,
    } satisfies ConstructionReshapeEditorProps['capabilities']['stages'][number];
    const construction: ConstructionReshapeEditorProps['construction'] = { version: 1, steps: [expandStep] };
    const onCandidateChange = vi.fn();
    const onEditStep = vi.fn();
    renderEditor({
      construction,
      capabilities: capabilitiesFor([sourceStage, afterExpand]),
      editingStep: expandStep,
      onCandidateChange,
      onEditStep,
    });

    expect(controlValue('Repeated field')).toBe('tags-id');
    expect(screen.getByTestId('construction-reshape-expand-advanced')).not.toHaveAttribute('open');
    fireEvent.click(screen.getByText('Advanced options'));
    expect(controlValue('Expanded item name')).toBe('tag_value');
    expect(controlValue('Position column name')).toBe('tag_position');
    expect(controlValue('Empty list policy')).toBe('PRESERVE_PARENT');
    fireEvent.click(screen.getByTestId('construction-reshape-edit-expand-saved'));
    expect(onEditStep).toHaveBeenCalledWith('expand-saved');
  });

  it('keeps saved pivot categories editable until a complete scan finds them missing', () => {
    const pivotStage: ConstructionReshapeEditorProps['capabilities']['selectedStage'] = {
      ...sourceStage,
      columns: [
        { id: 'patient-id', name: 'patient_id', label: 'Patient ID', type: 'string', cardinality: 'required_one' },
        { id: 'visit-id', name: 'visit', label: 'Visit', type: 'string', cardinality: 'required_one' },
        { id: 'kind-id', name: 'kind', label: 'Kind', type: 'string', cardinality: 'required_one' },
        { id: 'value-id', name: 'value', label: 'Value', type: 'decimal', cardinality: 'optional_one' },
      ],
      capabilities: sourceStage.capabilities.map((capability) => ({ ...capability, supported: capability.kind === 'PIVOT' })),
    };
    const pivotStep: ConstructionReshapeStep = {
      id: 'pivot-saved',
      inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: {
        kind: 'PIVOT',
        pivot: {
          constructionId: 'pivot-saved',
          groupKeyIds: ['patient-id'],
          categoryColumnId: 'kind-id',
          valueColumnId: 'value-id',
          categories: [{ key: { kind: 'STRING', string: 'baseline' }, outputColumnId: 'baseline-value-id' }],
          duplicatePolicy: 'SUM',
          missingCellPolicy: 'NULL',
          unlistedCategoryPolicy: 'EXCLUDE_WITH_EVIDENCE',
        },
      },
      outputs: [
        { id: 'patient-id', name: 'subject_key', label: 'Study subject', type: 'string' },
        { id: 'baseline-value-id', name: 'baseline_value', label: 'Baseline value', type: 'decimal' },
      ],
    };
    expect(constructionSchema.safeParse({ version: 1, steps: [pivotStep] })).toMatchObject({ success: true });
    const afterPivot = {
      ...pivotStage,
      id: pivotStep.id,
      inputStageId: pivotStage.id,
      operation: 'PIVOT',
      columns: pivotStep.outputs,
    } satisfies ConstructionReshapeEditorProps['capabilities']['stages'][number];
    const onCandidateChange = vi.fn();
    const onEditStep = vi.fn();
    const editorProps: ConstructionReshapeEditorProps = {
      construction: { version: 1, steps: [pivotStep] },
      capabilities: capabilitiesFor([pivotStage, afterPivot], pivotStage),
      editingStep: pivotStep,
      disabled: false,
      onCandidateChange,
      onEditStep,
    };
    const view = render(<ConstructionReshapeEditor {...editorProps} />);

    expect(controlValue('Pivot category field')).toBe('kind-id');
    expect(controlValue('Pivot values field')).toBe('value-id');
    expect(screen.getByTestId('construction-reshape-pivot-category-summary')).toHaveTextContent('Selected 1 of 1 categories: baseline');
    fireEvent.click(screen.getByText('Change selected categories'));
    expect(controlChecked('Keep saved category baseline')).toBe(true);
    expect(screen.queryByText('Not found in the latest category list')).not.toBeInTheDocument();
    const advanced = screen.getByTestId('construction-reshape-pivot-advanced');
    expect(advanced).not.toHaveAttribute('open');
    expect(screen.getByTestId('construction-reshape-pivot-policy-summary')).toHaveTextContent(
      'Duplicate values: be added together. Empty cells: stay empty. Unselected categories: be skipped and reported.',
    );
    fireEvent.click(screen.getByText('Advanced settings'));
    expect(controlValue('Pivot duplicate policy')).toBe('SUM');
    expect(controlValue('Pivot missing cell policy')).toBe('NULL');
    expect(controlValue('Pivot unlisted category policy')).toBe('EXCLUDE_WITH_EVIDENCE');
    expect(controlValue('Pivot output name baseline')).toBe('baseline_value');
    fireEvent.change(screen.getByLabelText('Pivot output label baseline'), { target: { value: 'Baseline result' } });

    expect(screen.queryByTestId('construction-reshape-schema-unavailable')).not.toBeInTheDocument();
    const intent = onCandidateChange.mock.lastCall?.[0];
    expect(intent?.candidateConstruction.steps[0]?.operation).toEqual({
      kind: 'PIVOT',
      pivot: {
        constructionId: 'pivot-saved',
        groupKeyIds: ['patient-id'],
        categoryColumnId: 'kind-id',
        valueColumnId: 'value-id',
        categories: [{ key: { kind: 'STRING', string: 'baseline' }, outputColumnId: 'baseline-value-id' }],
        duplicatePolicy: 'SUM',
        missingCellPolicy: 'NULL',
        unlistedCategoryPolicy: 'EXCLUDE_WITH_EVIDENCE',
      },
    });
    expect(intent?.candidateConstruction.steps[0]?.outputs).toContainEqual(expect.objectContaining({ id: 'patient-id', name: 'subject_key', label: 'Study subject' }));
    expect(intent?.candidateConstruction.steps[0]?.outputs).toContainEqual(expect.objectContaining({ id: 'baseline-value-id', label: 'Baseline result' }));
    fireEvent.click(screen.getByTestId('construction-reshape-edit-pivot-saved'));
    expect(onEditStep).toHaveBeenCalledWith('pivot-saved');

    view.rerender(
      <ConstructionReshapeEditor
        {...editorProps}
        pivotDiscovery={{
          stageId: 'source_projection',
          categoryColumnId: 'kind-id',
          valueColumnId: 'value-id',
          status: 'complete',
          categories: [{ key: { kind: 'STRING', string: 'followup' }, label: 'Follow up' }],
        }}
      />,
    );
    expect(screen.getByText('Not found in the latest category list')).toBeInTheDocument();
  });

  it('uses visible, distinct column labels as new unpivot keys', () => {
    const unpivotStage = {
      ...sourceStage,
      columns: [
        { id: 'opaque-a', name: 'col_opaque_a', label: 'Patient reference', type: 'string', cardinality: 'required_one' as const },
        { id: 'opaque-b', name: 'col_opaque_b', label: 'Patient reference', type: 'string', cardinality: 'required_one' as const },
      ],
      capabilities: sourceStage.capabilities.map((capability) => ({ ...capability, supported: capability.kind === 'UNPIVOT' })),
    } satisfies ConstructionReshapeEditorProps['capabilities']['selectedStage'];
    const onCandidateChange = vi.fn<ConstructionReshapeEditorProps['onCandidateChange']>();
    renderEditor({
      capabilities: capabilitiesFor([unpivotStage], unpivotStage),
      selectedColumns: ['opaque-a', 'opaque-b'],
      onCandidateChange,
    });

    fireEvent.click(screen.getByTestId('construction-reshape-choice-unpivot'));
    expect(screen.getByTestId('construction-unpivot-advanced')).not.toHaveAttribute('open');
    expect(screen.getByTestId('construction-unpivot-effect')).toHaveTextContent('Rows with missing values stay in the table.');
    const operation = onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0]?.operation;
    expect(operation?.kind).toBe('UNPIVOT');
    if (operation?.kind !== 'UNPIVOT') throw new Error('Expected an Unpivot candidate');
    expect(operation.unpivot.nullRowPolicy).toBe('PRESERVE');
    fireEvent.click(screen.getByText('Advanced options: field names and missing values'));
    expect(controlValue('Unpivot key value opaque-a')).toBe('Patient reference');
    expect(controlValue('Unpivot key value opaque-b')).toBe('Patient reference (2)');
  });

  it('reopens an unpivot with exact typed keys and edits output metadata without changing its mapping', () => {
    const unpivotStage: ConstructionReshapeEditorProps['capabilities']['selectedStage'] = {
      ...sourceStage,
      columns: [
        { id: 'patient-id', name: 'patient_id', label: 'Patient ID', type: 'string', cardinality: 'required_one' },
        { id: 'baseline-id', name: 'baseline', label: 'Baseline', type: 'integer', cardinality: 'optional_one' },
        { id: 'followup-id', name: 'followup', label: 'Follow up', type: 'integer', cardinality: 'optional_one' },
      ],
      capabilities: sourceStage.capabilities.map((capability) => ({ ...capability, supported: capability.kind === 'UNPIVOT' })),
    };
    const unpivotStep: ConstructionReshapeStep = {
      id: 'unpivot-saved',
      inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: {
        kind: 'UNPIVOT',
        unpivot: {
          constructionId: 'unpivot-saved',
          inputs: [
            { columnId: 'baseline-id', key: { kind: 'STRING', string: 'baseline' } },
            { columnId: 'followup-id', key: { kind: 'INTEGER', integer: 2 } },
          ],
          keyOutputColumnId: 'visit-id',
          valueOutputColumnId: 'result-id',
          nullRowPolicy: 'PRESERVE',
        },
      },
      outputs: [
        { id: 'patient-id', name: 'subject_key', label: 'Study subject', type: 'string' },
        { id: 'visit-id', name: 'visit', label: 'Visit', type: 'string' },
        { id: 'result-id', name: 'result', label: 'Result', type: 'integer' },
      ],
    };
    expect(constructionSchema.safeParse({ version: 1, steps: [unpivotStep] })).toMatchObject({ success: true });
    const afterUnpivot = {
      ...unpivotStage,
      id: unpivotStep.id,
      inputStageId: unpivotStage.id,
      operation: 'UNPIVOT',
      columns: unpivotStep.outputs,
    } satisfies ConstructionReshapeEditorProps['capabilities']['stages'][number];
    const onCandidateChange = vi.fn();
    renderEditor({
      construction: { version: 1, steps: [unpivotStep] },
      capabilities: capabilitiesFor([unpivotStage, afterUnpivot], unpivotStage),
      editingStep: unpivotStep,
      onCandidateChange,
    });

    expect(controlChecked('Unpivot Baseline')).toBe(true);
    expect(controlChecked('Unpivot Follow up')).toBe(true);
    expect(screen.getByTestId('construction-unpivot-advanced')).not.toHaveAttribute('open');
    fireEvent.click(screen.getByText('Advanced options: field names and missing values'));
    expect(controlValue('Unpivot key type baseline-id')).toBe('STRING');
    expect(controlValue('Unpivot key value baseline-id')).toBe('baseline');
    expect(controlValue('Unpivot key type followup-id')).toBe('INTEGER');
    expect(controlValue('Unpivot key value followup-id')).toBe('2');
    expect(controlValue('Unpivot null row policy')).toBe('PRESERVE');
    fireEvent.change(screen.getByLabelText('Unpivot value output label'), { target: { value: 'Visit result' } });

    expect(screen.queryByTestId('construction-reshape-schema-unavailable')).not.toBeInTheDocument();
    const intent = onCandidateChange.mock.lastCall?.[0];
    expect(intent?.candidateConstruction.steps[0]?.operation).toEqual(unpivotStep.operation);
    expect(intent?.candidateConstruction.steps[0]?.outputs).toContainEqual(expect.objectContaining({ id: 'patient-id', name: 'subject_key', label: 'Study subject' }));
    expect(intent?.candidateConstruction.steps[0]?.outputs).toContainEqual(expect.objectContaining({ id: 'result-id', label: 'Visit result' }));
  });

  it('requests stage-scoped pivot discovery and defaults every complete result', () => {
    const onCandidateChange = vi.fn();
    const onDiscoverCategories = vi.fn();
    const discoveryStage = {
      ...sourceStage,
      columns: [...sourceStage.columns, { id: 'sex-id', name: 'sex', label: 'Sex', type: 'string', cardinality: 'required_one' as const }],
      capabilities: sourceStage.capabilities.map((capability) => ({ ...capability, supported: capability.kind === 'PIVOT' })),
    } satisfies ConstructionReshapeEditorProps['capabilities']['selectedStage'];
    const props: ConstructionReshapeEditorProps = {
      construction: { version: 1, steps: [] },
      capabilities: capabilitiesFor([discoveryStage], discoveryStage),
      disabled: false,
      onCandidateChange,
      onEditStep: vi.fn(),
      onDiscoverCategories,
    };
    const view = render(
      <ConstructionReshapeEditor
        {...props}
      />,
    );

    fireEvent.click(screen.getByTestId('construction-reshape-choice-pivot'));
    expect(controlValue('Pivot category field')).toBe('');
    expect(screen.getByRole('button', { name: 'Find category values' })).toBeDisabled();
    fireEvent.change(screen.getByLabelText('Pivot category field'), { target: { value: 'site-id' } });
    fireEvent.click(screen.getByRole('button', { name: 'Find category values' }));
    expect(onDiscoverCategories).toHaveBeenCalledWith({ stageId: 'source_projection', categoryColumnId: 'site-id', valueColumnId: 'age-id' });
    expect(onCandidateChange).toHaveBeenLastCalledWith(undefined);
    expect(screen.getByText('Find category values for the selected fields before applying this pivot.')).toBeInTheDocument();

    view.rerender(
      <ConstructionReshapeEditor
        {...props}
        pivotDiscovery={{
          stageId: 'source_projection',
          categoryColumnId: 'site-id',
          valueColumnId: 'age-id',
          status: 'complete',
          categories: [
            { key: { kind: 'STRING', string: 'site-a' }, label: 'Site A' },
            { key: { kind: 'STRING', string: 'site-b' }, label: 'Site B' },
          ],
        }}
      />,
    );
    expect(screen.getByTestId('construction-reshape-pivot-category-summary')).toHaveTextContent('Selected 2 of 2 categories: Site A, Site B');
    fireEvent.click(screen.getByText('Change selected categories'));
    expect(controlChecked('Include category Site A')).toBe(true);
    expect(controlChecked('Include category Site B')).toBe(true);
    expect(screen.getByTestId('construction-reshape-pivot-category-summary')).toHaveTextContent('Site A');
    fireEvent.click(screen.getByLabelText('Pivot group Sex'));
    const intent = onCandidateChange.mock.lastCall?.[0];
    expect(intent).toBeDefined();
    if (!intent) throw new Error('Expected a candidate after category discovery');
    const operation = constructionSchema.parse(intent.candidateConstruction).steps[0]?.operation;
    expect(operation).toMatchObject({
      kind: 'PIVOT',
      pivot: {
        groupKeyIds: ['sex-id'],
        categoryColumnId: 'site-id',
        valueColumnId: 'age-id',
        categories: expect.arrayContaining([
          { key: { kind: 'STRING', string: 'site-a' }, outputColumnId: expect.any(String) },
          { key: { kind: 'STRING', string: 'site-b' }, outputColumnId: expect.any(String) },
        ]),
      },
    });
  });

  it('proposes valid pivot outputs for discovered digit-leading categories', () => {
    const bodyStructureIds = [
      '123e4567-e89b-12d3-a456-426614174000',
      '223e4567-e89b-12d3-a456-426614174001',
    ];
    const discovery = {
      stageId: sourceStage.id,
      categoryColumnId: 'site-id',
      valueColumnId: 'age-id',
      status: 'complete',
      categories: bodyStructureIds.map((id, index) => ({
        key: { kind: 'STRING' as const, string: id },
        label: `Body structure ${index + 1}`,
      })),
    } satisfies NonNullable<ConstructionReshapeEditorProps['pivotDiscovery']>;
    const existingOutputName = 'column_123e4567_e89b_12d3_a456_426614174000';
    const pivotStage = {
      ...sourceStage,
      columns: [
        ...sourceColumns,
        { id: 'sex-id', name: existingOutputName, label: 'Sex', type: 'string', cardinality: 'required_one' as const },
      ],
      capabilities: sourceStage.capabilities.map((capability) => ({ ...capability, supported: capability.kind === 'PIVOT' })),
    } satisfies ConstructionReshapeEditorProps['capabilities']['selectedStage'];
    const onCandidateChange = vi.fn<ConstructionReshapeEditorProps['onCandidateChange']>();
    render(
      <ConstructionReshapeEditor
        construction={{ version: 1, steps: [] }}
        capabilities={capabilitiesFor([pivotStage], pivotStage)}
        selectedColumns={['sex-id']}
        pivotDiscovery={discovery}
        disabled={false}
        onCandidateChange={onCandidateChange}
        onEditStep={vi.fn()}
        onDiscoverCategories={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByTestId('construction-reshape-choice-pivot'));
    fireEvent.change(screen.getByLabelText('Pivot category field'), { target: { value: 'site-id' } });

    const intent = onCandidateChange.mock.lastCall?.[0];
    expect(intent).toBeDefined();
    if (!intent) throw new Error('Expected discovered categories to produce a pivot candidate');
    const candidate = constructionSchema.parse(intent.candidateConstruction);
    const step = candidate.steps[0];
    expect(step?.operation.kind).toBe('PIVOT');
    if (step?.operation.kind !== 'PIVOT') throw new Error('Expected a pivot candidate');
    const categoryOutputIds = new Set(step.operation.pivot.categories.map((category) => category.outputColumnId));
    const categoryOutputs = step.outputs.filter((output) => categoryOutputIds.has(output.id));

    expect(categoryOutputs.map(({ name, label }) => ({ name, label }))).toEqual([
      { name: `${existingOutputName}_2`, label: 'Body structure 1' },
      { name: 'column_223e4567_e89b_12d3_a456_426614174001', label: 'Body structure 2' },
    ]);
    expect(new Set(step.outputs.map((output) => output.name)).size).toBe(step.outputs.length);
  });

  it('keeps a manual category opt-out when discovery refreshes', () => {
    const categories = [
      { key: { kind: 'STRING', string: 'site-a' }, label: 'Site A' },
      { key: { kind: 'STRING', string: 'site-b' }, label: 'Site B' },
    ] satisfies NonNullable<Extract<ConstructionReshapeEditorProps['pivotDiscovery'], { status: 'complete' }>['categories']>;
    const pivotStage = {
      ...sourceStage,
      columns: [...sourceColumns, { id: 'sex-id', name: 'sex', label: 'Sex', type: 'string', cardinality: 'required_one' as const }],
      capabilities: sourceStage.capabilities.map((capability) => ({ ...capability, supported: capability.kind === 'PIVOT' })),
    } satisfies ConstructionReshapeEditorProps['capabilities']['selectedStage'];
    const onCandidateChange = vi.fn<ConstructionReshapeEditorProps['onCandidateChange']>();
    const props: ConstructionReshapeEditorProps = {
      construction: { version: 1, steps: [] },
      capabilities: capabilitiesFor([pivotStage], pivotStage),
      selectedColumns: ['sex-id'],
      disabled: false,
      onCandidateChange,
      onEditStep: vi.fn(),
      onDiscoverCategories: vi.fn(),
    };
    const view = render(<ConstructionReshapeEditor {...props} />);

    fireEvent.click(screen.getByTestId('construction-reshape-choice-pivot'));
    fireEvent.change(screen.getByLabelText('Pivot category field'), { target: { value: 'site-id' } });
    view.rerender(
      <ConstructionReshapeEditor
        {...props}
        pivotDiscovery={{ stageId: pivotStage.id, categoryColumnId: 'site-id', valueColumnId: 'age-id', status: 'complete', categories }}
      />,
    );
    expect(screen.getByTestId('construction-reshape-pivot-category-summary')).toHaveTextContent('Selected 2 of 2 categories');
    fireEvent.click(screen.getByText('Change selected categories'));
    fireEvent.click(screen.getByLabelText('Include category Site B'));
    expect(controlChecked('Include category Site A')).toBe(true);
    expect(controlChecked('Include category Site B')).toBe(false);
    expect(onCandidateChange).toHaveBeenLastCalledWith(undefined);

    view.rerender(
      <ConstructionReshapeEditor
        {...props}
        pivotDiscovery={{
          stageId: pivotStage.id,
          categoryColumnId: 'site-id',
          valueColumnId: 'age-id',
          status: 'complete',
          categories: [...categories, { key: { kind: 'STRING', string: 'site-c' }, label: 'Site C' }],
        }}
      />,
    );
    expect(controlChecked('Include category Site A')).toBe(true);
    expect(controlChecked('Include category Site B')).toBe(false);
    expect(controlChecked('Include category Site C')).toBe(false);
    expect(screen.getByTestId('construction-reshape-pivot-category-summary')).toHaveTextContent('Selected 1 of 3 categories: Site A');
    expect(screen.getByText('Select every discovered category, or filter rows before pivoting.')).toBeInTheDocument();
    expect(onCandidateChange).toHaveBeenLastCalledWith(undefined);
  });

  it('applies the category default again when returning to a field pair without manual choices', () => {
    const categories = [
      { key: { kind: 'STRING', string: 'site-a' }, label: 'Site A' },
      { key: { kind: 'STRING', string: 'site-b' }, label: 'Site B' },
    ] satisfies NonNullable<Extract<ConstructionReshapeEditorProps['pivotDiscovery'], { status: 'complete' }>['categories']>;
    const pivotStage = {
      ...sourceStage,
      columns: [...sourceColumns, { id: 'sex-id', name: 'sex', label: 'Sex', type: 'string', cardinality: 'required_one' as const }],
      capabilities: sourceStage.capabilities.map((capability) => ({ ...capability, supported: capability.kind === 'PIVOT' })),
    } satisfies ConstructionReshapeEditorProps['capabilities']['selectedStage'];
    const view = render(
      <ConstructionReshapeEditor
        construction={{ version: 1, steps: [] }}
        capabilities={capabilitiesFor([pivotStage], pivotStage)}
        selectedColumns={['sex-id']}
        pivotDiscovery={{ stageId: pivotStage.id, categoryColumnId: 'site-id', valueColumnId: 'age-id', status: 'complete', categories }}
        disabled={false}
        onCandidateChange={vi.fn()}
        onEditStep={vi.fn()}
        onDiscoverCategories={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByTestId('construction-reshape-choice-pivot'));
    fireEvent.change(screen.getByLabelText('Pivot category field'), { target: { value: 'site-id' } });
    expect(screen.getByTestId('construction-reshape-pivot-category-summary')).toHaveTextContent('Selected 2 of 2 categories');
    fireEvent.click(screen.getByText('Change selected categories'));
    expect(controlChecked('Include category Site A')).toBe(true);
    expect(controlChecked('Include category Site B')).toBe(true);

    fireEvent.change(screen.getByLabelText('Pivot category field'), { target: { value: 'age-id' } });
    fireEvent.change(screen.getByLabelText('Pivot category field'), { target: { value: 'site-id' } });
    fireEvent.change(screen.getByLabelText('Pivot values field'), { target: { value: 'age-id' } });

    expect(screen.getByTestId('construction-reshape-pivot-category-summary')).toHaveTextContent('Selected 2 of 2 categories');
    expect(controlChecked('Include category Site A')).toBe(true);
    expect(controlChecked('Include category Site B')).toBe(true);
  });

  it('searches Pivot category values and selects or clears only the shown categories in one update', () => {
    const categories = [
      { key: { kind: 'STRING', string: 'site-a' }, label: 'Site A', suggestedName: 'site' },
      { key: { kind: 'STRING', string: 'site-b' }, label: 'site B', suggestedName: 'site' },
      { key: { kind: 'STRING', string: 'bone' }, label: 'Skeletal tissue', suggestedName: 'site' },
    ] satisfies NonNullable<Extract<ConstructionReshapeEditorProps['pivotDiscovery'], { status: 'complete' }>['categories']>;
    const pivotStage = {
      ...sourceStage,
      columns: [...sourceColumns, { id: 'sex-id', name: 'sex', label: 'Sex', type: 'string', cardinality: 'required_one' as const }],
      capabilities: sourceStage.capabilities.map((capability) => ({ ...capability, supported: capability.kind === 'PIVOT' })),
    } satisfies ConstructionReshapeEditorProps['capabilities']['selectedStage'];
    const onCandidateChange = vi.fn<ConstructionReshapeEditorProps['onCandidateChange']>();
    render(
      <ConstructionReshapeEditor
        construction={{ version: 1, steps: [] }}
        capabilities={capabilitiesFor([pivotStage], pivotStage)}
        selectedColumns={['sex-id']}
        pivotDiscovery={{
          stageId: pivotStage.id,
          categoryColumnId: 'site-id',
          valueColumnId: 'age-id',
          status: 'complete',
          categories,
        }}
        onDiscoverCategories={vi.fn()}
        disabled={false}
        onCandidateChange={onCandidateChange}
        onEditStep={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByTestId('construction-reshape-choice-pivot'));
    fireEvent.change(screen.getByLabelText('Pivot category field'), { target: { value: 'site-id' } });
    expect(screen.getByTestId('construction-reshape-pivot-category-summary')).toHaveTextContent(
      'Selected 3 of 3 categories: Site A, site B, Skeletal tissue',
    );
    fireEvent.click(screen.getByText('Advanced settings'));
    fireEvent.click(screen.getByText('Change selected categories'));
    const search = screen.getByLabelText('Search category values');
    const status = screen.getByTestId('construction-reshape-pivot-category-status');
    expect(status).toHaveTextContent('3 selected · Showing 3 of 3 category values');
    expect(screen.getByText('If a row has an unselected category')).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'Skip it and report it' })).toBeDisabled();
    expect(screen.getByText(/New pivots require every category present in the rows to be selected/)).toBeInTheDocument();
    expect(screen.queryByText('Select every discovered category, or filter rows before pivoting.')).not.toBeInTheDocument();

    fireEvent.change(search, { target: { value: 'SKELETAL' } });
    expect(controlChecked('Include category Skeletal tissue')).toBe(true);
    expect(status).toHaveTextContent('3 selected · Showing 1 of 3 category values');
    const callsBeforeSelect = onCandidateChange.mock.calls.length;

    fireEvent.click(screen.getByRole('button', { name: 'Clear shown categories' }));
    expect(onCandidateChange).toHaveBeenCalledTimes(callsBeforeSelect + 1);
    expect(onCandidateChange).toHaveBeenLastCalledWith(undefined);
    expect(status).toHaveTextContent('2 selected · Showing 1 of 3 category values');
    expect(screen.getByText('Select every discovered category, or filter rows before pivoting.')).toBeInTheDocument();

    const callsBeforeSearch = onCandidateChange.mock.calls.length;
    fireEvent.change(search, { target: { value: 'sItE' } });
    expect(onCandidateChange).toHaveBeenCalledTimes(callsBeforeSearch);
    expect(status).toHaveTextContent('2 selected · Showing 2 of 3 category values');
    expect(controlChecked('Include category Site A')).toBe(true);
    expect(controlChecked('Include category site B')).toBe(true);

    fireEvent.change(search, { target: { value: 'SKELETAL' } });
    expect(controlChecked('Include category Skeletal tissue')).toBe(false);
    const callsBeforeSecondSelect = onCandidateChange.mock.calls.length;
    fireEvent.click(screen.getByRole('button', { name: 'Select shown categories' }));
    expect(onCandidateChange).toHaveBeenCalledTimes(callsBeforeSecondSelect + 1);
    const selectedIntent = onCandidateChange.mock.lastCall?.[0];
    expect(selectedIntent).toBeDefined();
    if (!selectedIntent) throw new Error('Expected a candidate after selecting every discovered category');
    const selectedStep = constructionSchema.parse(selectedIntent.candidateConstruction).steps[0];
    expect(selectedStep?.operation).toMatchObject({
      kind: 'PIVOT',
      pivot: {
        categories: expect.arrayContaining([
          { key: { kind: 'STRING', string: 'site-a' }, outputColumnId: expect.any(String) },
          { key: { kind: 'STRING', string: 'site-b' }, outputColumnId: expect.any(String) },
          { key: { kind: 'STRING', string: 'bone' }, outputColumnId: expect.any(String) },
        ]),
      },
    });
    if (selectedStep?.operation.kind !== 'PIVOT') throw new Error('Expected the selected step to be a Pivot');
    const selectedOutputIds = selectedStep.operation.pivot.categories.map((category) => category.outputColumnId);
    const selectedCategoryOutputs = selectedStep.outputs.filter((output) => selectedOutputIds.includes(output.id));
    expect(selectedCategoryOutputs.map((output) => output.name).sort()).toEqual(['site_2', 'site_3', 'site_4']);
    expect(new Set(selectedCategoryOutputs.map((output) => output.id)).size).toBe(3);

    const callsBeforeClear = onCandidateChange.mock.calls.length;
    fireEvent.click(screen.getByRole('button', { name: 'Clear shown categories' }));
    expect(onCandidateChange).toHaveBeenCalledTimes(callsBeforeClear + 1);
    expect(onCandidateChange).toHaveBeenLastCalledWith(undefined);

    fireEvent.change(search, { target: { value: '' } });
    expect(status).toHaveTextContent('2 selected · Showing 3 of 3 category values');
    expect(screen.getByText('Select every discovered category, or filter rows before pivoting.')).toBeInTheDocument();
    expect(controlChecked('Include category Site A')).toBe(true);
    expect(controlChecked('Include category site B')).toBe(true);
    expect(controlChecked('Include category Skeletal tissue')).toBe(false);
    expect(screen.getByTestId('construction-reshape-pivot-category-summary')).toHaveTextContent('Selected 2 of 3 categories: Site A, site B');

    const callsBeforeReselect = onCandidateChange.mock.calls.length;
    fireEvent.click(screen.getByRole('button', { name: 'Select shown categories' }));
    expect(onCandidateChange).toHaveBeenCalledTimes(callsBeforeReselect + 1);
    const reselectedIntent = onCandidateChange.mock.lastCall?.[0];
    expect(reselectedIntent).toBeDefined();
    if (!reselectedIntent) throw new Error('Expected a candidate after reselecting all categories');
    const reselectedStep = constructionSchema.parse(reselectedIntent.candidateConstruction).steps[0];
    if (reselectedStep?.operation.kind !== 'PIVOT') throw new Error('Expected the reselected step to be a Pivot');
    const reselectedOutputIds = reselectedStep.operation.pivot.categories.map((category) => category.outputColumnId);
    const reselectedCategoryOutputs = reselectedStep.outputs.filter((output) => reselectedOutputIds.includes(output.id));
    const retainedOutputIds = new Set(selectedStep.operation.pivot.categories.slice(0, 2).map((category) => category.outputColumnId));
    expect(reselectedCategoryOutputs.filter((output) => retainedOutputIds.has(output.id))).toEqual(selectedCategoryOutputs.slice(0, 2));
    expect(screen.queryByText('Select every discovered category, or filter rows before pivoting.')).not.toBeInTheDocument();
  });
});
