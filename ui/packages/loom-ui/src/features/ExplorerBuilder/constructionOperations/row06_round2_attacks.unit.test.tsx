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

type CandidateIntent = Pick<
  ConstructionProposalRequest,
  'candidateConstruction' | 'changedStepId' | 'removeStepIds' | 'filterSources' | 'deriveSources'
>;
type FilterSourceInput = NonNullable<ConstructionCapabilitiesResponse['filterSourceInput']>;
type DeriveSourceInput = NonNullable<ConstructionCapabilitiesResponse['deriveSourceInput']>;

const columns: ConstructionStageDescriptor['columns'] = [
  { id: 'age-id', name: 'age', label: 'Age', type: 'integer' },
  { id: 'weight-id', name: 'weight', label: 'Weight', type: 'decimal' },
  { id: 'status-id', name: 'status', label: 'Status', type: 'string' },
];

const sourceStage = (cols: ConstructionStageDescriptor['columns'] = columns): ConstructionStageDescriptor => ({
  id: 'source_projection',
  inputStageId: '',
  operation: 'SOURCE_PROJECTION',
  columns: cols,
  capabilities: [
    { kind: 'FILTER', supported: true },
    { kind: 'DERIVE', supported: true },
    { kind: 'PIVOT', supported: false, reason: 'Not needed by this test.' },
    { kind: 'UNPIVOT', supported: false, reason: 'Not needed by this test.' },
  ],
});

const renderEditor = (args: {
  readonly family: 'KEEP_ROWS' | 'CALCULATE';
  readonly construction?: Construction;
  readonly selectedStage?: ConstructionStageDescriptor;
  readonly editingStep?: ConstructionStep;
  readonly selectedColumns?: ReadonlyArray<string>;
  readonly filterSourceInput?: FilterSourceInput;
  readonly deriveSourceInput?: DeriveSourceInput;
  readonly onCandidateChange?: (candidate: CandidateIntent | undefined) => void;
}) => {
  const selectedStage = args.selectedStage ?? sourceStage();
  const construction = args.construction ?? { version: 1, steps: [] };
  render(
    <ConstructionOperationEditor
      family={args.family}
      construction={construction}
      capabilities={{
        snapshotToken: 'snapshot-1',
        draftVersion: 1,
        draftDigest: 'draft-digest-1',
        outputId: 'table-1',
        stageId: selectedStage.id,
        baseConstruction: construction,
        stages: [selectedStage],
        selectedStage,
        groupFieldChoices: [],
        groupRowValueChoices: [],
        workspaceInputs: [],
        ...(args.filterSourceInput ? { filterSourceInput: args.filterSourceInput } : {}),
        ...(args.deriveSourceInput ? { deriveSourceInput: args.deriveSourceInput } : {}),
      }}
      editingStep={args.editingStep}
      selectedColumns={args.selectedColumns}
      disabled={false}
      onCandidateChange={args.onCandidateChange ?? vi.fn()}
      onEditStep={vi.fn()}
    />,
  );
};

const sourceOptgroup = (select: HTMLSelectElement): HTMLOptGroupElement | null =>
  select.querySelector('optgroup[label="Dataset fields (not in output)"]');

const filterColumn = (): HTMLSelectElement => screen.getByRole('combobox', { name: 'Column' }) as HTMLSelectElement;

const operandColumn = (label: string): HTMLSelectElement =>
  screen.getByRole('combobox', { name: `${label} column` }) as HTMLSelectElement;

const setKind = (label: string, value: 'column' | 'literal' | 'unset') => {
  fireEvent.change(screen.getByRole('combobox', { name: `${label} kind` }), { target: { value } });
};

afterEach(cleanup);

const numericChoice = (choiceId: string, fieldPath: string, columnId: string) => ({
  choiceId,
  fieldPath,
  label: fieldPath,
  logicalType: 'decimal' as const,
  isPopulated: true,
  columnId,
});

describe('ROW-SOURCE-06 round-2 attacks (ConstructionOperationEditor)', () => {
  it('A1 CONTRACT: explains when the only populated derive field is bound but not listable in the picker', () => {
    renderEditor({
      family: 'CALCULATE',
      selectedStage: sourceStage([{ id: 'status-id', name: 'status', label: 'Status', type: 'string' }]),
      deriveSourceInput: {
        supported: true,
        stageId: 'source_projection',
        choices: [{ choiceId: 'choice-note', fieldPath: 'root.note.text', label: 'Note', logicalType: 'string', isPopulated: true, columnId: 'derive-input_note' }],
      },
    });
    setKind('First value', 'column');

    const left = operandColumn('First value');
    expect(left.options).toHaveLength(0);
    expect(sourceOptgroup(left)).toBeNull();
    expect(screen.getByTestId('source-picker-explanation').textContent).toContain('dataset field');
  });

  it('A2 both derive operands keep every live signed field selectable, so the all-bound pair needs no banner', () => {
    renderEditor({
      family: 'CALCULATE',
      selectedStage: sourceStage([{ id: 'status-id', name: 'status', label: 'Status', type: 'string' }]),
      deriveSourceInput: {
        supported: true,
        stageId: 'source_projection',
        choices: [numericChoice('choice-a', 'root.a.value', 'derive-input_a'), numericChoice('choice-b', 'root.b.value', 'derive-input_b')],
      },
    });
    setKind('First value', 'column');
    setKind('Second value', 'column');
    fireEvent.change(operandColumn('Second value'), { target: { value: 'source:choice-b' } });

    const offers = (select: HTMLSelectElement, choiceId: string, columnId: string) =>
      [...select.options].some((o) => o.value === columnId || o.value === `source:${choiceId}`);
    const first = operandColumn('First value');
    const second = operandColumn('Second value');
    expect(offers(first, 'choice-a', 'derive-input_a')).toBe(true);
    expect(offers(first, 'choice-b', 'derive-input_b')).toBe(true);
    expect(offers(second, 'choice-a', 'derive-input_a')).toBe(true);
    expect(offers(second, 'choice-b', 'derive-input_b')).toBe(true);
  });

  it('A3 a released operand binding is pruned, so the freed field returns to the picker instead of staying silently bound', () => {
    renderEditor({
      family: 'CALCULATE',
      selectedStage: sourceStage([{ id: 'status-id', name: 'status', label: 'Status', type: 'string' }]),
      deriveSourceInput: {
        supported: true,
        stageId: 'source_projection',
        choices: [numericChoice('choice-a', 'root.a.value', 'derive-input_a'), numericChoice('choice-b', 'root.b.value', 'derive-input_b')],
      },
    });
    setKind('Second value', 'column');
    setKind('First value', 'column');
    setKind('First value', 'literal');

    const right = operandColumn('Second value');
    expect([...right.options].map((o) => o.value)).toEqual(['derive-input_a', 'source:choice-b']);
    expect(sourceOptgroup(right)?.querySelectorAll('option').length).toBe(1);
  });

  it('A4 CONTRACT: two live filter bindings on one column leave no invisible field unexplained', () => {
    const onCandidateChange = vi.fn();
    renderEditor({
      family: 'KEEP_ROWS',
      onCandidateChange,
      filterSourceInput: {
        supported: true,
        stageId: 'source_projection',
        choices: [numericChoice('choice-a', 'root.a', 'shared-column'), numericChoice('choice-b', 'root.b', 'shared-column')],
      },
    });
    fireEvent.change(filterColumn(), { target: { value: 'source:choice-a' } });
    fireEvent.change(filterColumn(), { target: { value: 'source:choice-b' } });

    const control = filterColumn();
    expect(sourceOptgroup(control)).toBeNull();
    expect([...control.options].filter((o) => o.value === 'shared-column')).toHaveLength(1);
    expect(screen.getByTestId('source-picker-explanation').textContent)
      .toContain('already used by another source selection');
  });

  it('A5a mechanism: the zero-column stage path replaces the picker with a column notice', () => {
    renderEditor({
      family: 'KEEP_ROWS',
      selectedStage: sourceStage([]),
      filterSourceInput: {
        supported: true,
        stageId: 'source_projection',
        choices: [{ choiceId: 'choice-status', fieldPath: 'root.status', label: 'Status', logicalType: 'string', isPopulated: false, columnId: '' }],
      },
    });
    expect(screen.getAllByRole('status').some((node) => (node.textContent ?? '').includes('no columns'))).toBe(true);
    expect(screen.queryByRole('combobox', { name: 'Column' })).toBeNull();
  });

  it('A5 CONTRACT: a filter lane with no stage columns still distinguishes unpopulated dataset fields', () => {
    renderEditor({
      family: 'KEEP_ROWS',
      selectedStage: sourceStage([]),
      filterSourceInput: {
        supported: true,
        stageId: 'source_projection',
        choices: [{ choiceId: 'choice-status', fieldPath: 'root.status', label: 'Status', logicalType: 'string', isPopulated: false, columnId: '' }],
      },
    });
    expect(screen.getByTestId('source-picker-explanation').textContent)
      .toContain('contains values in this dataset yet');
  });

  it('A7 regression: derive lane with one populated field bound by the saved operand explains or keeps it selectable', () => {
    const saved: ConstructionStep = {
      id: 'derive-source-saved',
      inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: {
        kind: 'DERIVE',
        derive: {
          constructionId: 'derive-source-saved',
          outputColumnId: 'calculated-id',
          operation: 'ADD',
          left: { kind: 'COLUMN', columnId: 'derive-input_saved' },
          right: { kind: 'LITERAL', literal: { kind: 'INTEGER', integer: 2 } },
          missingInputPolicy: 'PROPAGATE_NULL',
        },
      },
      outputs: [...columns, { id: 'calculated-id', name: 'derived', label: 'Derived', type: 'integer' }],
    };
    renderEditor({
      family: 'CALCULATE',
      construction: { version: 1, steps: [saved] },
      editingStep: saved,
      deriveSourceInput: {
        supported: true,
        stageId: 'source_projection',
        choices: [{ choiceId: 'choice-a', fieldPath: 'root.a', label: 'Root a', logicalType: 'decimal', isPopulated: true, columnId: 'derive-input_saved' }],
      },
    });
    const first = operandColumn('First value');
    const selectable = [...first.options].some((o) => o.value === 'derive-input_saved' || o.value === 'source:choice-a');
    const explained = screen.queryByTestId('source-picker-explanation') !== null;
    expect(selectable || explained).toBe(true);
  });

  it('hosts the explanation on the second operand when the first operand is literal', () => {
    renderEditor({
      family: 'CALCULATE',
      selectedStage: sourceStage([{ id: 'status-id', name: 'status', label: 'Status', type: 'string' }]),
      deriveSourceInput: {
        supported: true,
        stageId: 'source_projection',
        choices: [{ choiceId: 'choice-note', fieldPath: 'root.note.text', label: 'Note', logicalType: 'string', isPopulated: true, columnId: 'derive-input_note' }],
      },
    });
    setKind('First value', 'literal');
    setKind('Second value', 'column');
    expect([...operandColumn('Second value').options]).toHaveLength(0);
    expect(screen.getByTestId('source-picker-explanation').textContent).toContain('dataset field');
  });

  it('renders no banner while the sole bound derive field stays selectable as its materialized column', () => {
    renderEditor({
      family: 'CALCULATE',
      selectedStage: sourceStage([{ id: 'status-id', name: 'status', label: 'Status', type: 'string' }]),
      deriveSourceInput: {
        supported: true,
        stageId: 'source_projection',
        choices: [numericChoice('choice-a', 'root.a.value', 'derive-input_a')],
      },
    });
    setKind('First value', 'column');
    expect([...operandColumn('First value').options].map((option) => option.value)).toContain('derive-input_a');
    expect(screen.queryByTestId('source-picker-explanation')).toBeNull();
  });

  it('A8 switching the condition column off the bound private column returns that field to the picker', () => {
    const saved: ConstructionStep = {
      id: 'filter-source-saved',
      inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: { kind: 'FILTER', filter: { columnId: 'filter-input_saved', operator: 'EXISTS' } },
      outputs: [...columns, { id: 'filter-input_saved', name: 'root_a', label: 'Root a', type: 'decimal' }],
    };
    renderEditor({
      family: 'KEEP_ROWS',
      construction: { version: 1, steps: [saved] },
      editingStep: saved,
      selectedColumns: ['age-id'],
      filterSourceInput: {
        supported: true,
        stageId: 'source_projection',
        choices: [{ choiceId: 'choice-a', fieldPath: 'root.a', label: 'Root a', logicalType: 'decimal', isPopulated: true, columnId: 'filter-input_saved' }],
      },
    });
    fireEvent.change(filterColumn(), { target: { value: 'age-id' } });

    const control = filterColumn();
    const showsBoundField = [...control.options].some((o) => o.value === 'filter-input_saved' || o.value === 'source:choice-a');
    expect(showsBoundField).toBe(true);
  });

  it('A6 a second saved condition does not produce a false all-bound banner in the editing lane', () => {
    const otherSaved: ConstructionStep = {
      id: 'filter-other',
      inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: { kind: 'FILTER', filter: { columnId: 'filter-input_a', operator: 'EXISTS' } },
      outputs: [...columns, { id: 'filter-input_a', name: 'root_a', label: 'Root a', type: 'decimal' }],
    };
    renderEditor({
      family: 'KEEP_ROWS',
      construction: { version: 1, steps: [otherSaved] },
      filterSourceInput: {
        supported: true,
        stageId: 'source_projection',
        choices: [numericChoice('choice-a', 'root.a', 'filter-input_a')],
      },
    });
    expect(sourceOptgroup(filterColumn())?.querySelectorAll('option').length).toBe(1);
    expect(screen.queryByTestId('source-picker-explanation')).toBeNull();
  });
});
