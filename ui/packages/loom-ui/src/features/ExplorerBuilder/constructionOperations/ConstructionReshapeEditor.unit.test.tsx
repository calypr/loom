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
  readonly selectedColumns?: ReadonlyArray<string>;
  readonly onCandidateChange?: ConstructionReshapeEditorProps['onCandidateChange'];
  readonly onEditStep?: ConstructionReshapeEditorProps['onEditStep'];
}) => {
  const onCandidateChange = args.onCandidateChange ?? vi.fn();
  const onEditStep = args.onEditStep ?? vi.fn();
  render(
    <ConstructionReshapeEditor
      construction={args.construction ?? { version: 1, steps: [] }}
      capabilities={args.capabilities ?? capabilitiesFor()}
      editingStep={args.editingStep}
      selectedColumns={args.selectedColumns}
      disabled={false}
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
    expect(onCandidateChange).not.toHaveBeenCalledWith(expect.objectContaining({ candidateConstruction: expect.anything() }));
  });

  it('builds a whole-table group summary with editable stable outputs', () => {
    const onCandidateChange = vi.fn();
    renderEditor({ onCandidateChange });

    fireEvent.click(screen.getByTestId('construction-reshape-choice-group'));
    expect(screen.getByTestId('construction-reshape-group')).toBeInTheDocument();
    expect(screen.getByText(/With no group fields, make one summary row for the whole table/)).toBeInTheDocument();
    expect(screen.queryByLabelText('Group by Tags')).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Summary 1'), { target: { value: 'COUNT_ROWS' } });
    fireEvent.change(screen.getByLabelText('Summary output name 1'), { target: { value: 'participant_count' } });
    fireEvent.change(screen.getByLabelText('Summary output label 1'), { target: { value: 'Participant count' } });

    assertCandidateMatchesSchemaAnd(onCandidateChange, {
      kind: 'GROUP',
      group: {
        constructionId: expect.any(String),
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
    expect(screen.queryByLabelText('Group by Tags')).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Summary 1'), { target: { value: 'MEAN' } });
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
        keys: [{ inputColumnId: 'site-id', outputColumnId: expect.any(String) }],
        aggregates: [
          { operation: 'MEAN', inputColumnId: 'age-id', outputColumnId: expect.any(String) },
          { operation: 'SUM', inputColumnId: 'age-id', outputColumnId: expect.any(String) },
        ],
      },
    });
  });

  it('requires an explicit empty-list policy and offers a zero-based position column', () => {
    const onCandidateChange = vi.fn();
    renderEditor({ onCandidateChange });
    fireEvent.click(screen.getByTestId('construction-reshape-choice-expand'));

    expect(controlValue('Repeated field')).toBe('tags-id');
    expect(controlValue('Empty list policy')).toBe('');
    expect(controlChecked('Include item position')).toBe(false);
    fireEvent.click(screen.getByLabelText('Include item position'));
    fireEvent.change(screen.getByLabelText('Empty list policy'), { target: { value: 'PRESERVE_PARENT' } });
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
    expect(controlValue('Expanded item name')).toBe('tag_value');
    expect(controlValue('Position column name')).toBe('tag_position');
    expect(controlValue('Empty list policy')).toBe('PRESERVE_PARENT');
    fireEvent.click(screen.getByTestId('construction-reshape-edit-expand-saved'));
    expect(onEditStep).toHaveBeenCalledWith('expand-saved');
  });

  it('reopens a pivot with the saved categories, stable outputs, and all policies', () => {
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
    renderEditor({
      construction: { version: 1, steps: [pivotStep] },
      capabilities: capabilitiesFor([pivotStage, afterPivot], pivotStage),
      editingStep: pivotStep,
      onCandidateChange,
      onEditStep,
    });

    expect(controlValue('Pivot category field')).toBe('kind-id');
    expect(controlValue('Pivot values field')).toBe('value-id');
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

  it('requests stage-scoped pivot discovery and does not make a candidate before categories return', () => {
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
          categories: [{ key: { kind: 'STRING', string: 'site-a' }, label: 'Site A' }],
        }}
      />,
    );
    fireEvent.click(screen.getByLabelText('Pivot group Sex'));
    fireEvent.click(screen.getByLabelText('Include category Site A'));
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
        categories: [{ key: { kind: 'STRING', string: 'site-a' }, outputColumnId: expect.any(String) }],
      },
    });
  });
});
