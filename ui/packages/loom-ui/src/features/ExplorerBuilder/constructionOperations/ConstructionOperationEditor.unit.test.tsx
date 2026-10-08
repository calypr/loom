// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createLoomClient } from '../../../api';
import {
  constructionSchema,
  type Construction,
  type ConstructionCapabilitiesResponse,
  type ConstructionProposalResponse,
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
  operation: 'SOURCE_PROJECTION',
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
  selectedStage: ConstructionStageDescriptor = sourceStage,
): ConstructionCapabilitiesResponse => ({
  snapshotToken: 'snapshot-1',
  draftVersion: 1,
  draftDigest: 'draft-digest-1',
  outputId: 'table-1',
  stageId: selectedStage.id,
  baseConstruction: construction,
  stages: [...stages],
  selectedStage,
  workspaceInputs: [],
});

const renderEditor = (args: {
  readonly family: 'KEEP_ROWS' | 'CALCULATE';
  readonly construction?: Construction;
  readonly stages?: ReadonlyArray<ConstructionStageDescriptor>;
  readonly selectedStage?: ConstructionStageDescriptor;
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
      capabilities={capabilitiesFor(args.construction, args.stages, args.selectedStage)}
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

    const editor = screen.getByTestId('construction-filter-editor');
    expect(editor).toHaveAttribute('aria-label', 'Filter output rows by condition');
    expect(within(editor).getByRole('heading', { name: 'Filter output rows' })).toBeInTheDocument();
    expect(editor).toHaveTextContent(/rows must match every condition/i);
    expect(editor).toHaveTextContent(/contributor rules still choose which records supply their values/i);

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

  it('projects stage-only cardinality out before the real proposal request is serialized', async () => {
    const stage: ConstructionStageDescriptor = {
      ...sourceStage,
      columns: [
        { id: 'age-id', name: 'age', label: 'Age', type: 'integer', cardinality: 'required_one' },
        { id: 'weight-id', name: 'weight', label: 'Weight', type: 'decimal', cardinality: 'optional_one' },
      ],
    };
    const onCandidateChange = vi.fn();
    renderEditor({
      family: 'KEEP_ROWS',
      stages: [stage],
      selectedStage: stage,
      onCandidateChange,
    });

    fireEvent.change(screen.getByRole('spinbutton', { name: 'Value' }), { target: { value: '21' } });
    const candidate = onCandidateChange.mock.lastCall?.[0];
    expect(candidate).toBeDefined();
    if (!candidate) throw new Error('Expected a typed filter candidate.');
    const changedStepId = candidate.changedStepId;
    if (!changedStepId) throw new Error('Expected a changed step ID.');
    const candidateConstruction = candidate.candidateConstruction;
    expect(candidateConstruction.steps[0]?.outputs).toEqual([
      { id: 'age-id', name: 'age', label: 'Age', type: 'integer' },
      { id: 'weight-id', name: 'weight', label: 'Weight', type: 'decimal' },
    ]);
    expect(constructionSchema.parse(candidateConstruction)).toEqual(candidateConstruction);

    const proposalResponse: ConstructionProposalResponse = {
      proposalId: 'proposal-1',
      outputId: 'table-1',
      snapshotToken: 'snapshot-1',
      draftVersion: 1,
      draftDigest: 'draft-digest-1',
      baseDocumentDigest: 'document-1',
      candidateWorkspaceDigest: 'workspace-1',
      changedStepId,
      candidateConstruction,
      dependencyImpact: { changedStepId, affectedStepIds: [] },
      stages: [stage],
      previewStatus: 'NEEDS_REPAIR',
      previewDurationMs: 0,
    };
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValueOnce(
      new Response(JSON.stringify(proposalResponse), { status: 200 }),
    );
    const client = createLoomClient({ fetch });
    const proposalArgs = {
      project: 'project-a',
      explorerId: 'explorer-a',
      snapshotToken: 'snapshot-1',
      expectedDraftVersion: 1,
      expectedDraftDigest: 'draft-digest-1',
      outputId: 'table-1',
      changedStepId,
      candidateConstruction,
    };

    await expect(client.proposeConstruction(proposalArgs)).resolves.toEqual(proposalResponse);
    expect(fetch).toHaveBeenCalledWith(
      '/api/v1/projects/project-a/explorers/explorer-a/authoring/v2/construction-proposals',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({
          snapshotToken: 'snapshot-1',
          expectedDraftVersion: 1,
          expectedDraftDigest: 'draft-digest-1',
          outputId: 'table-1',
          changedStepId,
          candidateConstruction,
        }),
      }),
    );
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

  it('builds and reopens value-free EXISTS filters', () => {
    const onCandidateChange = vi.fn();
    renderEditor({ family: 'KEEP_ROWS', onCandidateChange });

    fireEvent.change(screen.getByRole('combobox', { name: 'Condition' }), { target: { value: 'EXISTS' } });
    const candidate = onCandidateChange.mock.lastCall?.[0];
    expect(candidate).toBeDefined();
    if (!candidate) throw new Error('Expected an EXISTS filter candidate.');
    expect(candidate.candidateConstruction.steps.at(-1)?.operation).toEqual({
      kind: 'FILTER', filter: { columnId: 'age-id', operator: 'EXISTS' },
    });

    const savedExists: ConstructionStep = {
      ...filterStep,
      operation: { kind: 'FILTER', filter: { columnId: 'age-id', operator: 'EXISTS' } },
    };
    cleanup();
    renderEditor({
      family: 'KEEP_ROWS',
      construction: { version: 1, steps: [savedExists] },
      stages: [sourceStage, { ...sourceStage, id: savedExists.id, inputStageId: sourceStage.id, operation: 'FILTER' }],
      editingStep: savedExists,
      onCandidateChange,
    });
    const editButton = screen.getByTestId('construction-filter-edit-filter-saved');
    expect(editButton).toBeEnabled();
    const conditionControl = screen.getByRole('combobox', { name: 'Condition' });
    if (!(conditionControl instanceof HTMLSelectElement)) throw new Error('Expected the filter condition control to be a select.');
    expect(conditionControl.value).toBe('EXISTS');
    fireEvent.change(screen.getByRole('combobox', { name: 'Column' }), { target: { value: 'status-id' } });
    const edited = onCandidateChange.mock.lastCall?.[0];
    expect(edited).toBeDefined();
    if (!edited) throw new Error('Expected the saved EXISTS filter to remain editable.');
    expect(edited.candidateConstruction.steps[0]?.operation).toEqual({
      kind: 'FILTER', filter: { columnId: 'status-id', operator: 'EXISTS' },
    });
  });

  it('reopens and edits a saved NOT_EQUALS filter without changing its typed operator', () => {
    const savedNotEquals: ConstructionStep = {
      ...filterStep,
      operation: {
        kind: 'FILTER',
        filter: { columnId: 'status-id', operator: 'NOT_EQUALS', values: [{ kind: 'STRING', string: 'closed' }] },
      },
    };
    const onCandidateChange = vi.fn();
    renderEditor({
      family: 'KEEP_ROWS',
      construction: { version: 1, steps: [savedNotEquals] },
      stages: [sourceStage, { ...sourceStage, id: savedNotEquals.id, inputStageId: sourceStage.id, operation: 'FILTER' }],
      editingStep: savedNotEquals,
      onCandidateChange,
    });

    const condition = screen.getByRole<HTMLSelectElement>('combobox', { name: 'Condition' });
    expect(condition.value).toBe('NOT_EQUALS');
    expect(screen.getByRole<HTMLInputElement>('textbox', { name: 'Value' }).value).toBe('closed');
    fireEvent.change(screen.getByRole('textbox', { name: 'Value' }), { target: { value: 'paused' } });

    const candidate = onCandidateChange.mock.lastCall?.[0];
    expect(candidate).toBeDefined();
    if (!candidate) throw new Error('Expected an editable NOT_EQUALS candidate.');
    expect(candidate.candidateConstruction.steps[0]?.operation).toEqual({
      kind: 'FILTER',
      filter: { columnId: 'status-id', operator: 'NOT_EQUALS', values: [{ kind: 'STRING', string: 'paused' }] },
    });
  });

  it('creates and edits typed IN lists without losing saved values', () => {
    const onCandidateChange = vi.fn();
    renderEditor({ family: 'KEEP_ROWS', onCandidateChange });

    const condition = screen.getByRole<HTMLSelectElement>('combobox', { name: 'Condition' });
    expect(Array.from(condition.options).map((option) => option.value)).toContain('GT');
    expect(Array.from(condition.options).map((option) => option.value)).not.toContain('CONTAINS_TEXT');
    fireEvent.change(screen.getByRole('combobox', { name: 'Column' }), { target: { value: 'status-id' } });
    expect(Array.from(condition.options).map((option) => option.value)).toContain('CONTAINS_TEXT');
    expect(Array.from(condition.options).map((option) => option.value)).not.toContain('GT');

    fireEvent.change(screen.getByRole('combobox', { name: 'Condition' }), { target: { value: 'IN' } });
    fireEvent.change(screen.getByRole('textbox', { name: 'Value 1' }), { target: { value: 'open' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add another value' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Value 2' }), { target: { value: 'in-progress' } });
    const candidate = onCandidateChange.mock.lastCall?.[0];
    expect(candidate).toBeDefined();
    if (!candidate) throw new Error('Expected an IN candidate.');
    expect(candidate.candidateConstruction.steps[0]?.operation).toEqual({
      kind: 'FILTER',
      filter: {
        columnId: 'status-id', operator: 'IN',
        values: [{ kind: 'STRING', string: 'open' }, { kind: 'STRING', string: 'in-progress' }],
      },
    });

    const savedIn: ConstructionStep = {
      ...filterStep,
      operation: {
        kind: 'FILTER',
        filter: {
          columnId: 'status-id', operator: 'IN',
          values: [{ kind: 'STRING', string: 'open' }, { kind: 'STRING', string: 'in-progress' }],
        },
      },
    };
    cleanup();
    onCandidateChange.mockClear();
    renderEditor({
      family: 'KEEP_ROWS',
      construction: { version: 1, steps: [savedIn] },
      stages: [sourceStage, { ...sourceStage, id: savedIn.id, inputStageId: sourceStage.id, operation: 'FILTER' }],
      editingStep: savedIn,
      onCandidateChange,
    });
    expect(screen.getByRole<HTMLInputElement>('textbox', { name: 'Value 1' }).value).toBe('open');
    expect(screen.getByRole<HTMLInputElement>('textbox', { name: 'Value 2' }).value).toBe('in-progress');
    fireEvent.change(screen.getByRole('textbox', { name: 'Value 1' }), { target: { value: 'opening' } });
    const editedFirst = onCandidateChange.mock.lastCall?.[0];
    expect(editedFirst?.candidateConstruction.steps[0]?.operation).toEqual({
      kind: 'FILTER',
      filter: {
        columnId: 'status-id', operator: 'IN',
        values: [{ kind: 'STRING', string: 'opening' }, { kind: 'STRING', string: 'in-progress' }],
      },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Remove value 2' }));
    const removedSecond = onCandidateChange.mock.lastCall?.[0];
    expect(removedSecond?.candidateConstruction.steps[0]?.operation).toEqual({
      kind: 'FILTER',
      filter: { columnId: 'status-id', operator: 'IN', values: [{ kind: 'STRING', string: 'opening' }] },
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
