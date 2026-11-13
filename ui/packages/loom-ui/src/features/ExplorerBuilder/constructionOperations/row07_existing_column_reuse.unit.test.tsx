// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  type Construction,
  type ConstructionCapabilitiesResponse,
  type ConstructionProposalRequest,
  type ConstructionStageDescriptor,
  type ConstructionStep,
} from '../../../types';
import { ConstructionOperationEditor } from './ConstructionOperationEditor';
import { ConstructionReshapeEditor, type ConstructionReshapeEditorProps } from './ConstructionReshapeEditor';

type CandidateIntent = Pick<
  ConstructionProposalRequest,
  'candidateConstruction' | 'changedStepId' | 'removeStepIds' | 'filterSources' | 'deriveSources'
>;

const projection = (columnId: string, fieldPath: string): NonNullable<Construction['sourceProjections']>[number] => ({
  columnId,
  occurrenceId: `occ-${columnId}`,
  fieldPath,
  fhirType: 'string',
  logicalType: 'string',
  label: fieldPath,
});


const sourceStage = (cols: ConstructionStageDescriptor['columns']): ConstructionStageDescriptor => ({
  id: 'source_projection',
  inputStageId: '',
  operation: 'SOURCE_PROJECTION',
  columns: cols,
  capabilities: [
    { kind: 'FILTER', supported: true },
    { kind: 'DERIVE', supported: true },
    { kind: 'PIVOT', supported: false, reason: 'Not needed in this test.' },
    { kind: 'UNPIVOT', supported: false, reason: 'Not needed in this test.' },
  ],
});

const renderEditor = (args: {
  readonly family: 'KEEP_ROWS' | 'CALCULATE';
  readonly construction?: Construction;
  readonly stageColumns: ConstructionStageDescriptor['columns'];
  readonly filterSourceInput?: ConstructionCapabilitiesResponse['filterSourceInput'];
  readonly deriveSourceInput?: ConstructionCapabilitiesResponse['deriveSourceInput'];
  readonly editingStep?: ConstructionStep;
  readonly onCandidateChange?: (candidate: CandidateIntent | undefined) => void;
}) => {
  const selectedStage = sourceStage(args.stageColumns);
  const onCandidateChange = args.onCandidateChange ?? vi.fn();
  render(
    <ConstructionOperationEditor
      family={args.family}
      construction={args.construction ?? { version: 1, steps: [] }}
      capabilities={{
        snapshotToken: 'snapshot-1',
        draftVersion: 1,
        draftDigest: 'draft-digest-1',
        outputId: 'table-1',
        stageId: selectedStage.id,
        baseConstruction: { version: 1, steps: [] },
        stages: [selectedStage],
        selectedStage,
        groupFieldChoices: [],
        groupRowValueChoices: [],
        workspaceInputs: [],
        ...(args.filterSourceInput ? { filterSourceInput: args.filterSourceInput } : {}),
        ...(args.deriveSourceInput ? { deriveSourceInput: args.deriveSourceInput } : {}),
      }}
      editingStep={args.editingStep}
      disabled={false}
      onCandidateChange={onCandidateChange}
      onEditStep={vi.fn()}
    />,
  );
  return onCandidateChange;
};

const renderEditorWithStage = (
  construction: Construction,
  stage: ConstructionStageDescriptor,
  editingStep: ConstructionStep,
) => {
  render(
    <ConstructionOperationEditor
      family="KEEP_ROWS"
      construction={construction}
      capabilities={{
        snapshotToken: 'snapshot-1',
        draftVersion: 1,
        draftDigest: 'draft-digest-1',
        outputId: 'table-1',
        stageId: stage.id,
        baseConstruction: construction,
        stages: [stage],
        selectedStage: stage,
        groupFieldChoices: [],
        groupRowValueChoices: [],
        workspaceInputs: [],
        filterSourceInput: {
          supported: true,
          stageId: 'source_projection',
          choices: [{
            choiceId: 'choice-age',
            fieldPath: 'root.age',
            label: 'Age',
            logicalType: 'integer' as const,
            isPopulated: true,
            columnId: 'filter-input_age',
          }],
        },
      }}
      editingStep={editingStep}
      disabled={false}
      onCandidateChange={vi.fn()}
      onEditStep={vi.fn()}
    />,
  );
};

const filterColumnSelect = (): HTMLSelectElement =>
  screen.getByRole('combobox', { name: 'Column' }) as HTMLSelectElement;

const operandColumn = (label: string): HTMLSelectElement =>
  screen.getByRole('combobox', { name: `${label} column` }) as HTMLSelectElement;

const stringCol = (id: string, label: string) => ({ id, name: id, label, type: 'string' });
const intCol = (id: string, label: string) => ({ id, name: id, label, type: 'integer' });


const unpivotColumns: ConstructionReshapeEditorProps['capabilities']['selectedStage']['columns'] = [
  { id: 'opaque-a', name: 'col_opaque_a', label: 'Patient reference', type: 'string', cardinality: 'required_one' },
  { id: 'opaque-b', name: 'col_opaque_b', label: 'Melt two', type: 'string', cardinality: 'required_one' },
];

const unpivotStage: ConstructionReshapeEditorProps['capabilities']['selectedStage'] = {
  id: 'source_projection',
  inputStageId: '',
  operation: 'SOURCE_PROJECTION',
  columns: unpivotColumns,
  capabilities: [
    { kind: 'FILTER', supported: false, reason: 'Not needed in this test.' },
    { kind: 'DERIVE', supported: false, reason: 'Not needed in this test.' },
    { kind: 'PIVOT', supported: false, reason: 'Not needed in this test.' },
    { kind: 'UNPIVOT', supported: true },
    { kind: 'GROUP', supported: false, reason: 'Not needed in this test.' },
    { kind: 'EXPAND', supported: false, reason: 'Not needed in this test.' },
  ],
};

const populatedChoice = (choiceId: string, fieldPath: string, columnId: string) => ({
  choiceId,
  fieldPath,
  label: fieldPath,
  logicalType: 'decimal' as const,
  isPopulated: true,
  columnId,
});

const renderUnpivot = (args: {
  readonly construction: Construction;
  readonly choices: ReturnType<typeof populatedChoice>[];
}) => {
  render(
    <ConstructionReshapeEditor
      construction={args.construction}
      capabilities={{
        snapshotToken: 'snapshot-1',
        draftVersion: 1,
        draftDigest: 'draft-digest-1',
        outputId: 'table-1',
        stageId: unpivotStage.id,
        baseConstruction: { version: 1, steps: [] },
        stages: [unpivotStage],
        selectedStage: unpivotStage,
        groupFieldChoices: [],
        groupRowValueChoices: [],
        workspaceInputs: [],
        unpivotSourceInput: { supported: true, stageId: 'source_projection', choices: args.choices },
      }}
      selectedColumns={['opaque-a']}
      initialKind="unpivot"
      disabled={false}
      onCandidateChange={vi.fn()}
      onEditStep={vi.fn()}
    />,
  );
  const summary = screen.queryByText('Advanced options: field names and missing values');
  if (summary) fireEvent.click(summary);
};

const unpivotSourceGroup = (index: number): HTMLOptGroupElement | null => {
  const select = screen.getByTestId(`construction-reshape-unpivot-input-${index}-column`) as HTMLSelectElement;
  return select.querySelector('optgroup[label="Fields from your data"]');
};

const unpivotTableGroup = (index: number): HTMLOptGroupElement | null => {
  const select = screen.getByTestId(`construction-reshape-unpivot-input-${index}-column`) as HTMLSelectElement;
  return select.querySelector('optgroup[label="Fields in this table"]');
};

const withProjections = (projections: Construction['sourceProjections']): Construction => ({
  version: 1,
  steps: [],
  ...(projections ? { sourceProjections: projections } : {}),
});

afterEach(cleanup);

describe('ROW-SOURCE-07 filter lane', () => {
  const ageChoice = {
    choiceId: 'choice-age',
    fieldPath: 'root.age',
    label: 'Age',
    logicalType: 'integer' as const,
    isPopulated: true,
    columnId: 'filter-input_age',
  };

  it('suppresses the duplicate signed choice when the field is already projected and offers the existing column', () => {
    renderEditor({
      family: 'KEEP_ROWS',
      construction: withProjections([projection('age-col', 'root.age')]),
      stageColumns: [intCol('age-col', 'Age (table)'), stringCol('status-id', 'Status')],
      filterSourceInput: { supported: true, stageId: 'source_projection', choices: [ageChoice] },
    });
    const select = filterColumnSelect();
    expect([...select.options].some((option) => option.value === 'source:choice-age')).toBe(false);
    expect([...select.options].some((option) => option.value === 'age-col')).toBe(true);
  });

  it('selecting the reused field submits the existing column ID without a source binding', () => {
    const onCandidateChange = renderEditor({
      family: 'KEEP_ROWS',
      construction: withProjections([projection('age-col', 'root.age')]),
      stageColumns: [intCol('age-col', 'Age (table)'), stringCol('status-id', 'Status')],
      filterSourceInput: { supported: true, stageId: 'source_projection', choices: [ageChoice] },
    });
    fireEvent.change(filterColumnSelect(), { target: { value: 'age-col' } });
    fireEvent.change(screen.getByRole('combobox', { name: 'Condition' }), { target: { value: 'EXISTS' } });
    const calls = (onCandidateChange as ReturnType<typeof vi.fn>).mock.calls.map((call) => call[0]);
    const candidate = [...calls].reverse().find((entry) => entry !== undefined);
    expect(candidate).toBeDefined();
    expect(candidate.filterSources ?? []).toEqual([]);
    const step = candidate.candidateConstruction.steps.at(-1);
    expect(step?.operation.kind).toBe('FILTER');
    expect(step?.operation.kind === 'FILTER' ? step.operation.filter.columnId : undefined).toBe('age-col');
  });

  it('brings the signed choice back when the projected column is stale', () => {
    renderEditor({
      family: 'KEEP_ROWS',
      construction: withProjections([projection('removed-col', 'root.age')]),
      stageColumns: [intCol('age-col', 'Age (table)'), stringCol('status-id', 'Status')],
      filterSourceInput: { supported: true, stageId: 'source_projection', choices: [ageChoice] },
    });
    expect([...filterColumnSelect().options].some((option) => option.value === 'source:choice-age')).toBe(true);
  });

  it('keeps the reused existing-column selection across a reload of the saved step', () => {
    const saved = withProjections([projection('age-col', 'root.age')]);
    const savedStep = {
      id: 'filter-reuse-saved',
      inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: { kind: 'FILTER', filter: { columnId: 'age-col', operator: 'EXISTS' } },
      outputs: [intCol('age-col', 'Age (table)'), stringCol('status-id', 'Status')],
    } as unknown as ConstructionStep;
    const editingStage: ConstructionStageDescriptor = {
      ...sourceStage([intCol('age-col', 'Age (table)'), stringCol('status-id', 'Status')]),
      id: savedStep.id,
      inputStageId: 'source_projection',
      operation: 'FILTER',
    };
    renderEditorWithStage(saved, editingStage, savedStep);
    expect(filterColumnSelect().value).toBe('age-col');
    expect([...filterColumnSelect().options].some((option) => option.value === 'source:choice-age')).toBe(false);
  });

  it('keeps the signed choice when two live columns share the field path', () => {
    renderEditor({
      family: 'KEEP_ROWS',
      construction: withProjections([projection('age-col', 'root.age'), projection('age-copy', 'root.age')]),
      stageColumns: [intCol('age-col', 'Age'), intCol('age-copy', 'Age (copy)')],
      filterSourceInput: { supported: true, stageId: 'source_projection', choices: [ageChoice] },
    });
    expect([...filterColumnSelect().options].some((option) => option.value === 'source:choice-age')).toBe(true);
  });
});

describe('ROW-SOURCE-07 derive lane', () => {
  const ageChoice = {
    choiceId: 'choice-age',
    fieldPath: 'root.age',
    label: 'Age',
    logicalType: 'decimal' as const,
    isPopulated: true,
    columnId: 'derive-input_age',
  };

  it('suppresses the signed choice when the projected column is a usable numeric operand', () => {
    renderEditor({
      family: 'CALCULATE',
      construction: withProjections([projection('weight-id', 'root.age')]),
      stageColumns: [intCol('weight-id', 'Weight'), stringCol('status-id', 'Status')],
      deriveSourceInput: { supported: true, stageId: 'source_projection', choices: [ageChoice] },
    });
    fireEvent.change(screen.getByRole('combobox', { name: 'First value kind' }), { target: { value: 'column' } });
    expect([...operandColumn('First value').options].map((option) => option.value))
      .toEqual(expect.arrayContaining(['weight-id']));
    expect([...operandColumn('First value').options].some((option) => option.value.startsWith('source:'))).toBe(false);
  });

  it('keeps the signed choice when the projected column cannot serve as a numeric operand', () => {
    renderEditor({
      family: 'CALCULATE',
      construction: withProjections([projection('status-id', 'root.age')]),
      stageColumns: [intCol('w-id', 'Weight'), stringCol('status-id', 'Status')],
      deriveSourceInput: { supported: true, stageId: 'source_projection', choices: [ageChoice] },
    });
    fireEvent.change(screen.getByRole('combobox', { name: 'First value kind' }), { target: { value: 'column' } });
    expect([...operandColumn('First value').options].some((option) => option.value === 'source:choice-age')).toBe(true);
  });
});

describe('ROW-SOURCE-07 unpivot lane', () => {
  it('suppresses the duplicate signed choice for an already-projected field across melt rows', () => {
    renderUnpivot({
      construction: withProjections([projection('opaque-a', 'root.a')]),
      choices: [populatedChoice('choice-a', 'root.a', 'unpivot-a')],
    });
    expect(unpivotSourceGroup(0)?.querySelectorAll('option').length ?? 0).toBe(0);
    expect([...(unpivotTableGroup(0)?.querySelectorAll('option') ?? [])]
      .some((option) => option.value === 'opaque-a')).toBe(true);
  });

  it('brings the signed choice back when the projected column is stale', () => {
    renderUnpivot({
      construction: withProjections([projection('gone-column', 'root.a')]),
      choices: [populatedChoice('choice-a', 'root.a', 'unpivot-a')],
    });
    expect(unpivotSourceGroup(0)?.querySelectorAll('option').length ?? 0).toBe(1);
  });
});
