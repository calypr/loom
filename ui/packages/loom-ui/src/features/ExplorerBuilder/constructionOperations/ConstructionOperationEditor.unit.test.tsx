// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  constructionSchema,
  type Construction,
  type ConstructionCapabilitiesResponse,
  type ConstructionProposalRequest,
  type ConstructionStageDescriptor,
  type ConstructionStep,
} from '../../../types';
import { ConstructionOperationEditor } from './ConstructionOperationEditor';

type CandidateIntent = Pick<ConstructionProposalRequest, 'candidateConstruction' | 'changedStepId' | 'removeStepIds'>;

const columns: ConstructionStageDescriptor['columns'] = [
  { id: 'age-id', name: 'age', label: 'Age', type: 'integer' },
  { id: 'weight-id', name: 'weight', label: 'Weight', type: 'decimal' },
  { id: 'status-id', name: 'status', label: 'Status', type: 'string' },
];

const sourceStage: ConstructionStageDescriptor = {
  id: 'source_projection',
  inputStageId: '',
  columns,
  capabilities: [
    { kind: 'FILTER', supported: true },
    { kind: 'DERIVE', supported: true },
    { kind: 'PIVOT', supported: false, reason: 'Not needed by this editor test.' },
    { kind: 'UNPIVOT', supported: false, reason: 'Not needed by this editor test.' },
  ],
};

const capabilitiesFor = (
  construction: Construction = { version: 1, steps: [] },
  stages: ReadonlyArray<ConstructionStageDescriptor> = [sourceStage],
): ConstructionCapabilitiesResponse => ({
  snapshotToken: 'snapshot-1',
  draftVersion: 1,
  draftDigest: 'draft-digest-1',
  outputId: 'table-1',
  stageId: sourceStage.id,
  baseConstruction: construction,
  stages: [...stages],
  selectedStage: sourceStage,
});

const renderEditor = (args: {
  readonly family: 'KEEP_ROWS' | 'CALCULATE';
  readonly construction?: Construction;
  readonly stages?: ReadonlyArray<ConstructionStageDescriptor>;
  readonly editingStep?: ConstructionStep;
  readonly selectedColumns?: ReadonlyArray<string>;
  readonly onCandidateChange?: (candidate: CandidateIntent | undefined) => void;
  readonly onEditStep?: (stepId: string) => void;
}) => {
  const onCandidateChange = args.onCandidateChange ?? vi.fn();
  const onEditStep = args.onEditStep ?? vi.fn();
  render(
    <ConstructionOperationEditor
      family={args.family}
      construction={args.construction ?? { version: 1, steps: [] }}
      capabilities={capabilitiesFor(args.construction, args.stages)}
      editingStep={args.editingStep}
      selectedColumns={args.selectedColumns}
      disabled={false}
      onCandidateChange={onCandidateChange}
      onEditStep={onEditStep}
    />,
  );
  return { onCandidateChange, onEditStep };
};

const filterStep: ConstructionStep = {
  id: 'filter-saved',
  inputs: [{ kind: 'SOURCE_PROJECTION' }],
  operation: {
    kind: 'FILTER',
    filter: { columnId: 'age-id', operator: 'EQUALS', values: [{ kind: 'INTEGER', integer: 18 }] },
  },
  outputs: columns,
};

const deriveStep: ConstructionStep = {
  id: 'derive-saved',
  inputs: [{ kind: 'SOURCE_PROJECTION' }],
  operation: {
    kind: 'DERIVE',
    derive: {
      constructionId: 'derive-saved',
      outputColumnId: 'calculated-id',
      operation: 'ADD',
      left: { kind: 'COLUMN', columnId: 'age-id' },
      right: { kind: 'LITERAL', literal: { kind: 'INTEGER', integer: 2 } },
      missingInputPolicy: 'PROPAGATE_NULL',
    },
  },
  outputs: [...columns, { id: 'calculated-id', name: 'age_plus_two', label: 'Age plus two', type: 'integer' }],
};

afterEach(cleanup);

describe('ConstructionOperationEditor', () => {
  it('builds a typed equals filter from a compiler-returned column and clears preview intent when incomplete', () => {
    const onCandidateChange = vi.fn();
    renderEditor({ family: 'KEEP_ROWS', onCandidateChange });

    fireEvent.change(screen.getByRole('spinbutton', { name: 'Value' }), { target: { value: '21' } });
    const complete = onCandidateChange.mock.lastCall?.[0];
    expect(complete).toBeDefined();
    if (!complete) throw new Error('Expected a typed filter candidate.');
    expect(constructionSchema.parse(complete.candidateConstruction)).toEqual(complete.candidateConstruction);
    expect(complete.candidateConstruction.steps[0]).toMatchObject({
      inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: { kind: 'FILTER', filter: {
        columnId: 'age-id', operator: 'EQUALS', values: [{ kind: 'INTEGER', integer: 21 }],
      } },
    });

    fireEvent.change(screen.getByRole('spinbutton', { name: 'Value' }), { target: { value: '' } });
    expect(onCandidateChange).toHaveBeenLastCalledWith(undefined);
  });

  it('emits a value-free missing filter and routes saved conditions to the history editor', () => {
    const conditionConstruction: Construction = { version: 1, steps: [filterStep] };
    const filterStage: ConstructionStageDescriptor = {
      ...sourceStage,
      id: filterStep.id,
      inputStageId: sourceStage.id,
      operation: 'FILTER',
    };
    const onCandidateChange = vi.fn();
    const onEditStep = vi.fn();
    renderEditor({
      family: 'KEEP_ROWS',
      construction: conditionConstruction,
      stages: [sourceStage, filterStage],
      onCandidateChange,
      onEditStep,
    });

    expect(screen.getByText('Age equals 18')).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('construction-filter-edit-filter-saved'));
    expect(onEditStep).toHaveBeenCalledWith('filter-saved');

    fireEvent.change(screen.getByRole('combobox', { name: 'Condition' }), { target: { value: 'MISSING' } });
    const candidate = onCandidateChange.mock.lastCall?.[0];
    expect(candidate).toBeDefined();
    if (!candidate) throw new Error('Expected a missing filter candidate.');
    expect(constructionSchema.parse(candidate.candidateConstruction)).toEqual(candidate.candidateConstruction);
    expect(candidate.candidateConstruction.steps.at(-1)?.operation).toEqual({
      kind: 'FILTER', filter: { columnId: 'age-id', operator: 'MISSING' },
    });
  });

  it('creates an arithmetic calculation with a new output and a canonical expression', () => {
    const onCandidateChange = vi.fn();
    renderEditor({ family: 'CALCULATE', onCandidateChange, selectedColumns: ['weight-id'] });

    fireEvent.change(screen.getByRole('combobox', { name: 'Second value kind' }), { target: { value: 'literal' } });
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Second value number' }), { target: { value: '2.5' } });

    expect(screen.getByTestId('construction-calculate-new-output')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Formula editor' })).toBeInTheDocument();
    const candidate = onCandidateChange.mock.lastCall?.[0];
    expect(candidate).toBeDefined();
    if (!candidate) throw new Error('Expected a derived candidate.');
    expect(constructionSchema.parse(candidate.candidateConstruction)).toEqual(candidate.candidateConstruction);
    const step = candidate.candidateConstruction.steps[0];
    expect(step?.operation).toMatchObject({
      kind: 'DERIVE',
      derive: {
        operation: 'ADD',
        left: { kind: 'COLUMN', columnId: 'weight-id' },
        right: { kind: 'LITERAL', literal: { kind: 'DECIMAL', decimal: 2.5 } },
        missingInputPolicy: 'PROPAGATE_NULL',
      },
    });
    expect(step?.outputs).toHaveLength(columns.length + 1);
    expect(step?.outputs.at(-1)).toMatchObject({ name: 'calculated_value', label: 'Calculated value' });
  });

  it('roundtrips a saved calculation between guided controls and formula text with stable IDs', () => {
    const construction: Construction = { version: 1, steps: [deriveStep] };
    const deriveStage: ConstructionStageDescriptor = {
      ...sourceStage,
      id: deriveStep.id,
      inputStageId: sourceStage.id,
      operation: 'DERIVE',
      columns: deriveStep.outputs,
    };
    const onCandidateChange = vi.fn();
    renderEditor({
      family: 'CALCULATE',
      construction,
      stages: [sourceStage, deriveStage],
      editingStep: deriveStep,
      onCandidateChange,
    });

    expect(screen.getByTestId('construction-calculate-replacement')).toHaveTextContent('keeping its column identity');
    fireEvent.click(screen.getByRole('button', { name: 'Formula editor' }));
    expect(screen.getByRole<HTMLTextAreaElement>('textbox', { name: 'Formula' }).value).toBe('age + 2');
    fireEvent.change(screen.getByRole('textbox', { name: 'Formula' }), { target: { value: 'weight * 3.5' } });
    const candidate = onCandidateChange.mock.lastCall?.[0];
    expect(candidate).toBeDefined();
    if (!candidate) throw new Error('Expected an edited calculation candidate.');
    expect(candidate.changedStepId).toBe('derive-saved');
    expect(candidate.candidateConstruction.steps[0]).toMatchObject({
      id: 'derive-saved',
      operation: {
        kind: 'DERIVE',
        derive: {
          constructionId: 'derive-saved',
          outputColumnId: 'calculated-id',
          operation: 'MULTIPLY',
          left: { kind: 'COLUMN', columnId: 'weight-id' },
          right: { kind: 'LITERAL', literal: { kind: 'DECIMAL', decimal: 3.5 } },
        },
      },
      outputs: [...columns, { id: 'calculated-id', name: 'age_plus_two', label: 'Age plus two' }],
    });

    fireEvent.click(screen.getByRole('button', { name: 'Guided controls' }));
    expect(screen.getByRole<HTMLSelectElement>('combobox', { name: 'Operation' }).value).toBe('MULTIPLY');
    expect(screen.getByRole<HTMLSelectElement>('combobox', { name: 'First value kind' }).value).toBe('column');
    expect(screen.getByRole<HTMLSelectElement>('combobox', { name: 'First value column' }).value).toBe('weight-id');
    expect(screen.getByRole<HTMLInputElement>('spinbutton', { name: 'Second value number' }).value).toBe('3.5');

    fireEvent.click(screen.getByRole('button', { name: 'Formula editor' }));
    expect(screen.getByRole<HTMLTextAreaElement>('textbox', { name: 'Formula' }).value).toBe('weight * 3.5');
  });

  it('shows syntax errors for unsupported expressions and clears the preview candidate', () => {
    const onCandidateChange = vi.fn();
    renderEditor({ family: 'CALCULATE', onCandidateChange });

    fireEvent.change(screen.getByRole('combobox', { name: 'Second value kind' }), { target: { value: 'literal' } });
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Second value number' }), { target: { value: '2' } });
    expect(onCandidateChange.mock.lastCall?.[0]).toBeDefined();

    fireEvent.click(screen.getByRole('button', { name: 'Formula editor' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Formula' }), { target: { value: 'age + sqrt(2)' } });
    expect(screen.getByTestId('construction-calculate-formula-error')).toHaveTextContent(/functions/i);
    expect(screen.getByRole<HTMLTextAreaElement>('textbox', { name: 'Formula' }).value).toBe('age + sqrt(2)');
    expect(onCandidateChange).toHaveBeenLastCalledWith(undefined);
  });
});
