// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createLoomClient } from '../../../api';
import {
  constructionSchema,
  constructionCapabilitiesResponseSchema,
  type Construction,
  type ConstructionCapabilitiesResponse,
  type ConstructionProposalResponse,
  type ConstructionProposalRequest,
  type ConstructionStageDescriptor,
  type ConstructionStep,
} from '../../../types';
import {
  liveFilterSourceUnmaterializedProvenance,
  liveFilterSourceUnmaterializedResponse,
  liveFilterSourceUnmaterializedResponseJson,
} from '../../../../testSupport/liveConstructionCapabilitiesFixtures';
import { ConstructionOperationEditor } from './ConstructionOperationEditor';

type CandidateIntent = Pick<
  ConstructionProposalRequest,
  'candidateConstruction' | 'changedStepId' | 'removeStepIds' | 'filterSources' | 'deriveSources'
>;

type FilterSourceInput = NonNullable<ConstructionCapabilitiesResponse['filterSourceInput']>;
type FilterSourceChoice = FilterSourceInput['choices'][number];
type DeriveSourceInput = NonNullable<ConstructionCapabilitiesResponse['deriveSourceInput']>;
type DeriveSourceChoice = DeriveSourceInput['choices'][number];

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
  filterSourceInput?: FilterSourceInput,
  deriveSourceInput?: DeriveSourceInput,
): ConstructionCapabilitiesResponse => ({
  snapshotToken: 'snapshot-1',
  draftVersion: 1,
  draftDigest: 'draft-digest-1',
  outputId: 'table-1',
  stageId: selectedStage.id,
  baseConstruction: construction,
  stages: [...stages],
  selectedStage,
  groupFieldChoices: [],
  groupRowValueChoices: [],
  workspaceInputs: [],
  ...(filterSourceInput ? { filterSourceInput } : {}),
  ...(deriveSourceInput ? { deriveSourceInput } : {}),
});

const renderEditor = (args: {
  readonly family: 'KEEP_ROWS' | 'CALCULATE';
  readonly construction?: Construction;
  readonly stages?: ReadonlyArray<ConstructionStageDescriptor>;
  readonly selectedStage?: ConstructionStageDescriptor;
  readonly editingStep?: ConstructionStep;
  readonly selectedColumns?: ReadonlyArray<string>;
  readonly filterSourceInput?: FilterSourceInput;
  readonly deriveSourceInput?: DeriveSourceInput;
  readonly onCandidateChange?: (candidate: CandidateIntent | undefined) => void;
  readonly onEditStep?: (stepId: string) => void;
}) => {
  const onCandidateChange = args.onCandidateChange ?? vi.fn();
  const onEditStep = args.onEditStep ?? vi.fn();
  render(
    <ConstructionOperationEditor
      family={args.family}
      construction={args.construction ?? { version: 1, steps: [] }}
      capabilities={capabilitiesFor(args.construction, args.stages, args.selectedStage, args.filterSourceInput, args.deriveSourceInput)}
      editingStep={args.editingStep}
      selectedColumns={args.selectedColumns}
      disabled={false}
      onCandidateChange={onCandidateChange}
      onEditStep={onEditStep}
    />,
  );
  return { onCandidateChange, onEditStep };
};

const filterSourceSelect = (): HTMLSelectElement => {
  const control = screen.getByRole('combobox', { name: 'Column' });
  if (!(control instanceof HTMLSelectElement)) throw new Error('Expected the filter column control to be a select.');
  return control;
};

const sourceOptgroup = (select: HTMLSelectElement): HTMLOptGroupElement | null =>
  select.querySelector('optgroup[label="Dataset fields (not in output)"]');

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

  describe('filter rows on unselected dataset fields', () => {
    const supportedSource = (choices: ReadonlyArray<FilterSourceChoice>): FilterSourceInput => ({
      supported: true,
      stageId: 'source_projection',
      choices: [...choices],
    });

    const statusChoice: FilterSourceChoice = {
      choiceId: 'choice-status',
      fieldPath: 'root.status',
      label: 'Observation.status',
      logicalType: 'string',
      isPopulated: true,
    };

    const optgroupValues = (group: HTMLOptGroupElement): ReadonlyArray<string> =>
      Array.from(group.querySelectorAll('option')).map((option) => option.value);

    it('offers only populated dataset fields with a prettified label and field-path tooltip', () => {
      renderEditor({
        family: 'KEEP_ROWS',
        filterSourceInput: supportedSource([
          { ...statusChoice, choiceId: 'choice-unpopulated', isPopulated: false },
          statusChoice,
        ]),
      });

      const select = filterSourceSelect();
      expect(Array.from(select.options)
        .map((option) => option.value)
        .filter((value) => value !== '' && !value.startsWith('source:')))
        .toEqual(['age-id', 'weight-id', 'status-id']);

      const group = sourceOptgroup(select);
      if (!group) throw new Error('Expected a Dataset fields (not in output) optgroup.');
      expect(optgroupValues(group)).toEqual(['source:choice-status']);
      const option = group.querySelector('option');
      expect(option?.textContent).toBe('Observation · Status (string)');
      expect(option?.getAttribute('title')).toBe('root.status');
    });

    it('emits the signed choice in proposal filterSources when a dataset field is chosen', async () => {
      const onCandidateChange = vi.fn();
      renderEditor({ family: 'KEEP_ROWS', onCandidateChange, filterSourceInput: supportedSource([statusChoice]) });

      fireEvent.change(filterSourceSelect(), { target: { value: 'source:choice-status' } });
      const columnId = filterSourceSelect().value;
      expect(columnId).toMatch(/^filter-input_/);
      fireEvent.change(screen.getByRole('textbox', { name: 'Value' }), { target: { value: 'final' } });

      const candidate = onCandidateChange.mock.lastCall?.[0];
      if (!candidate) throw new Error('Expected a dataset-field filter candidate.');
      expect(candidate.filterSources).toEqual([{ choiceId: 'choice-status', columnId }]);
      expect(constructionSchema.parse(candidate.candidateConstruction)).toEqual(candidate.candidateConstruction);
      expect(candidate.candidateConstruction.steps[0]).toMatchObject({
        operation: {
          kind: 'FILTER',
          filter: { columnId, operator: 'EQUALS', values: [{ kind: 'STRING', string: 'final' }] },
        },
        outputs: expect.arrayContaining([
          expect.objectContaining({ id: columnId, name: 'root_status', label: 'Observation.status', type: 'string' }),
        ]),
      });

      const changedStepId = candidate.changedStepId;
      if (!changedStepId) throw new Error('Expected a changed step ID.');
      const proposalResponse: ConstructionProposalResponse = {
        outputId: 'table-1',
        snapshotToken: 'snapshot-1',
        draftVersion: 1,
        draftDigest: 'draft-digest-1',
        baseDocumentDigest: 'document-1',
        candidateWorkspaceDigest: 'workspace-1',
        changedStepId,
        candidateConstruction: candidate.candidateConstruction,
        dependencyImpact: { changedStepId, affectedStepIds: [] },
        stages: [sourceStage],
        previewStatus: 'READY',
        previewDurationMs: 0,
      };
      const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValueOnce(
        new Response(JSON.stringify(proposalResponse), { status: 200 }),
      );
      await createLoomClient({ fetch }).proposeConstruction({
        project: 'project-a',
        explorerId: 'explorer-a',
        snapshotToken: 'snapshot-1',
        expectedDraftVersion: 1,
        expectedDraftDigest: 'draft-digest-1',
        outputId: 'table-1',
        ...candidate,
      });
      expect(fetch).toHaveBeenCalledWith(
        '/api/v1/projects/project-a/explorers/explorer-a/authoring/v2/construction-proposals',
        expect.objectContaining({ method: 'POST' }),
      );
      const [, requestInit] = fetch.mock.calls[0] ?? [];
      const proposalBody = JSON.parse(String(requestInit?.body)) as {
        changedStepId?: string;
        filterSources?: ReadonlyArray<{ choiceId: string; columnId: string }>;
      };
      expect(proposalBody.changedStepId).toBe(changedStepId);
      expect(proposalBody.filterSources).toEqual([{ choiceId: 'choice-status', columnId }]);
    });

    it('restores the saved dataset-field binding when a source-backed filter reopens', () => {
      const savedSourceFilter: ConstructionStep = {
        id: 'filter-source-saved',
        inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: { kind: 'FILTER', filter: { columnId: 'filter-input_saved', operator: 'EXISTS' } },
        outputs: [...columns, { id: 'filter-input_saved', name: 'root_status', label: 'Observation.status', type: 'string' }],
      };
      const onCandidateChange = vi.fn();
      renderEditor({
        family: 'KEEP_ROWS',
        construction: { version: 1, steps: [savedSourceFilter] },
        stages: [sourceStage, { ...sourceStage, id: savedSourceFilter.id, inputStageId: sourceStage.id, operation: 'FILTER' }],
        editingStep: savedSourceFilter,
        onCandidateChange,
        filterSourceInput: supportedSource([{ ...statusChoice, columnId: 'filter-input_saved' }]),
      });

      const select = filterSourceSelect();
      expect(select.value).toBe('filter-input_saved');
      const checked = select.querySelector('option:checked');
      expect(checked?.textContent).toBe('Observation · Status (string)');
      expect(checked?.getAttribute('title')).toBe('root.status');
      expect(screen.queryByText(/saved condition column is no longer present/i)).not.toBeInTheDocument();

      fireEvent.change(screen.getByRole('combobox', { name: 'Condition' }), { target: { value: 'MISSING' } });
      const candidate = onCandidateChange.mock.lastCall?.[0];
      expect(candidate?.filterSources).toEqual([{ choiceId: 'choice-status', columnId: 'filter-input_saved' }]);
      expect(candidate?.candidateConstruction.steps[0]?.operation).toEqual({
        kind: 'FILTER', filter: { columnId: 'filter-input_saved', operator: 'MISSING' },
      });
    });

    it('clears the filterSources contribution when the condition leaves the dataset field', () => {
      const onCandidateChange = vi.fn();
      renderEditor({ family: 'KEEP_ROWS', onCandidateChange, filterSourceInput: supportedSource([statusChoice]) });

      fireEvent.change(filterSourceSelect(), { target: { value: 'source:choice-status' } });
      expect(filterSourceSelect().value).toMatch(/^filter-input_/);
      fireEvent.change(screen.getByRole('textbox', { name: 'Value' }), { target: { value: 'final' } });
      expect(onCandidateChange.mock.lastCall?.[0]?.filterSources).toHaveLength(1);

      fireEvent.change(filterSourceSelect(), { target: { value: 'age-id' } });
      fireEvent.change(screen.getByRole('spinbutton', { name: 'Value' }), { target: { value: '21' } });
      const candidate = onCandidateChange.mock.lastCall?.[0];
      if (!candidate) throw new Error('Expected a selected-column filter candidate.');
      expect(candidate.filterSources).toBeUndefined();
      expect(candidate.candidateConstruction.steps[0]?.operation).toEqual({
        kind: 'FILTER', filter: { columnId: 'age-id', operator: 'EQUALS', values: [{ kind: 'INTEGER', integer: 21 }] },
      });
      expect(candidate.candidateConstruction.steps[0]?.outputs).toEqual(columns.map((column) => ({ ...column })));

      const group = sourceOptgroup(filterSourceSelect());
      if (!group) throw new Error('Expected the dataset field to be selectable again.');
      expect(optgroupValues(group)).toEqual(['source:choice-status']);
    });

    it('shows the server reason verbatim when dataset-field filtering is unsupported', () => {
      renderEditor({
        family: 'KEEP_ROWS',
        filterSourceInput: {
          supported: false,
          stageId: 'source_projection',
          reasonCode: 'FILTER_SOURCE_AFTER_GROUP',
          reason: 'Loom cannot filter on unselected fields after rows are grouped.',
          choices: [],
        },
      });

      const status = screen.getByTestId('construction-filter-source-unavailable');
      expect(status).toHaveTextContent('FILTER_SOURCE_AFTER_GROUP');
      expect(status).toHaveTextContent('Loom cannot filter on unselected fields after rows are grouped.');
      expect(sourceOptgroup(filterSourceSelect())).toBeNull();
    });

    it('does not invent a reason when the server reports unsupported dataset fields without one', () => {
      renderEditor({
        family: 'KEEP_ROWS',
        filterSourceInput: { supported: false, stageId: 'source_projection', choices: [] },
      });

      const status = screen.getByTestId('construction-filter-source-unavailable');
      expect(status).toHaveTextContent(
        'Loom did not report why filtering on dataset fields is unavailable at this stage.',
      );
      expect(sourceOptgroup(filterSourceSelect())).toBeNull();
    });

    it('keeps the selected-column filter flow primary when dataset fields are offered', () => {
      const onCandidateChange = vi.fn();
      renderEditor({
        family: 'KEEP_ROWS',
        selectedColumns: ['status-id'],
        onCandidateChange,
        filterSourceInput: supportedSource([
          { ...statusChoice, choiceId: 'choice-note', fieldPath: 'root.note.text', label: 'Observation.note.text' },
        ]),
      });

      fireEvent.change(screen.getByRole('textbox', { name: 'Value' }), { target: { value: 'final' } });
      const candidate = onCandidateChange.mock.lastCall?.[0];
      if (!candidate) throw new Error('Expected a selected-column filter candidate.');
      expect(candidate.filterSources).toBeUndefined();
      expect(candidate.candidateConstruction.steps[0]).toMatchObject({
        operation: {
          kind: 'FILTER',
          filter: { columnId: 'status-id', operator: 'EQUALS', values: [{ kind: 'STRING', string: 'final' }] },
        },
      });

      const select = filterSourceSelect();
      expect(Array.from(select.options)
        .map((option) => option.value)
        .filter((value) => value !== '' && !value.startsWith('source:')))
        .toEqual(['age-id', 'weight-id', 'status-id']);
      const group = sourceOptgroup(select);
      if (!group) throw new Error('Expected the dataset fields optgroup to be added, not to replace columns.');
      expect(optgroupValues(group)).toEqual(['source:choice-note']);
    });

    const liveCapabilities = () => constructionCapabilitiesResponseSchema.parse(liveFilterSourceUnmaterializedResponse);

    const liveChoiceIdByPath = (capabilities: ConstructionCapabilitiesResponse, fieldPath: string): string => {
      const choice = capabilities.filterSourceInput?.choices.find((candidate) => candidate.fieldPath === fieldPath);
      if (!choice) throw new Error(`Expected the live ${fieldPath} source choice.`);
      return choice.choiceId;
    };

    const renderLive = (args: {
      readonly capabilities: ConstructionCapabilitiesResponse;
      readonly construction?: Construction;
      readonly editingStep?: ConstructionStep;
      readonly onCandidateChange?: (candidate: CandidateIntent | undefined) => void;
    }) => {
      const onCandidateChange = args.onCandidateChange ?? vi.fn();
      render(
        <ConstructionOperationEditor
          family="KEEP_ROWS"
          construction={args.construction ?? args.capabilities.baseConstruction}
          capabilities={args.capabilities}
          editingStep={args.editingStep}
          disabled={false}
          onCandidateChange={onCandidateChange}
          onEditStep={vi.fn()}
        />,
      );
      return { onCandidateChange };
    };

    it('mounts the picker from the live capabilities payload whose choices carry an empty columnId', () => {
      expect(liveFilterSourceUnmaterializedResponseJson.match(/"columnId": ""/g)).toHaveLength(
        liveFilterSourceUnmaterializedProvenance.unmaterializedChoiceCount,
      );

      const capabilities = liveCapabilities();
      renderLive({ capabilities });

      const select = filterSourceSelect();
      expect(Array.from(select.options)
        .map((option) => option.value)
        .filter((value) => value !== '' && !value.startsWith('source:')))
        .toEqual(['source_ef82eef4462a7f20fbcecfff']);

      const group = sourceOptgroup(select);
      if (!group) throw new Error('Expected the dataset fields optgroup from the live payload.');
      expect(optgroupValues(group)).toEqual(
        capabilities.filterSourceInput?.choices.map((choice) => `source:${choice.choiceId}`) ?? [],
      );
      const gender = Array.from(group.querySelectorAll('option'))
        .find((option) => option.getAttribute('title') === 'gender');
      expect(gender?.textContent).toBe('Patient · Gender (string)');
      const lastUpdated = Array.from(group.querySelectorAll('option'))
        .find((option) => option.getAttribute('title') === 'meta.lastUpdated');
      expect(lastUpdated?.textContent).toBe('Patient · Meta Last Updated (date_time)');
    });

    it('mints a column identity for a live unmaterialized choice instead of binding to the empty columnId', () => {
      const capabilities = liveCapabilities();
      const genderChoiceId = liveChoiceIdByPath(capabilities, 'gender');
      const onCandidateChange = vi.fn();
      renderLive({ capabilities, onCandidateChange });

      fireEvent.change(filterSourceSelect(), { target: { value: `source:${genderChoiceId}` } });
      const select = filterSourceSelect();
      const columnId = select.value;
      expect(columnId).toMatch(/^filter-input_/);
      expect(columnId).not.toBe('');
      expect(Array.from(select.options).some((option) => option.value === '')).toBe(false);
      const bound = select.querySelector('option:checked');
      expect(bound?.textContent).toBe('Patient · Gender (string)');
      expect(bound?.getAttribute('title')).toBe('gender');

      fireEvent.change(screen.getByRole('textbox', { name: 'Value' }), { target: { value: 'female' } });
      const candidate = onCandidateChange.mock.lastCall?.[0];
      if (!candidate) throw new Error('Expected a candidate from the live unmaterialized choice.');
      expect(candidate.filterSources).toEqual([{ choiceId: genderChoiceId, columnId }]);
      const step = candidate.candidateConstruction.steps[0];
      expect(step?.operation).toEqual({
        kind: 'FILTER',
        filter: { columnId, operator: 'EQUALS', values: [{ kind: 'STRING', string: 'female' }] },
      });
      expect(step?.outputs).toContainEqual(expect.objectContaining({ id: columnId, name: 'gender', label: 'Patient.gender' }));
      expect(step?.outputs.some((output: ConstructionStep['outputs'][number]) => output.id === '' || output.name === '')).toBe(false);
    });

    it('keeps a saved condition honest when its dataset field is no longer materialized', () => {
      const capabilities = liveCapabilities();
      const savedGoneFilter: ConstructionStep = {
        id: 'filter-gender-saved',
        inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: { kind: 'FILTER', filter: { columnId: 'filter-input_gone', operator: 'EXISTS' } },
        outputs: [],
      };
      const onCandidateChange = vi.fn();
      renderLive({
        capabilities,
        construction: { version: 1, steps: [savedGoneFilter] },
        editingStep: savedGoneFilter,
        onCandidateChange,
      });

      expect(screen.getByText('The saved condition column is no longer present in Loom’s predecessor stage.')).toBeInTheDocument();
      expect(screen.queryByRole('combobox', { name: 'Column' })).toBeNull();
      expect(screen.queryAllByRole('option').filter((option) => option.getAttribute('value') === '')).toHaveLength(0);
      expect(onCandidateChange).toHaveBeenLastCalledWith(undefined);
    });

    it('saves a condition on an ordinary stage column without any source binding when choices are unmaterialized', () => {
      const capabilities = liveCapabilities();
      const identityColumnId = capabilities.selectedStage.columns[0]!.id;
      const savedIdentityFilter: ConstructionStep = {
        id: 'filter-identity-saved',
        inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: { kind: 'FILTER', filter: { columnId: identityColumnId, operator: 'EXISTS' } },
        outputs: [],
      };
      const onCandidateChange = vi.fn();
      renderLive({
        capabilities,
        construction: { version: 1, steps: [savedIdentityFilter] },
        editingStep: savedIdentityFilter,
        onCandidateChange,
      });

      const select = filterSourceSelect();
      expect(select.value).toBe(identityColumnId);
      expect(Array.from(select.options).some((option) => option.value === '')).toBe(false);
      expect(optgroupValues(sourceOptgroup(select)!)).toEqual(
        capabilities.filterSourceInput!.choices.map((choice) => `source:${choice.choiceId}`),
      );

      fireEvent.change(screen.getByRole('combobox', { name: 'Condition' }), { target: { value: 'MISSING' } });
      const candidate = onCandidateChange.mock.lastCall?.[0];
      expect(candidate?.filterSources).toBeUndefined();
      expect(candidate?.candidateConstruction.steps[0]?.operation).toEqual({
        kind: 'FILTER', filter: { columnId: identityColumnId, operator: 'MISSING' },
      });
    });

    it('treats a whitespace-only columnId as unmaterialized', () => {
      const capabilities = liveCapabilities();
      const genderChoiceId = liveChoiceIdByPath(capabilities, 'gender');
      const blankColumnCapabilities: ConstructionCapabilitiesResponse = {
        ...capabilities,
        filterSourceInput: {
          ...capabilities.filterSourceInput!,
          choices: capabilities.filterSourceInput!.choices.map((choice) => ({ ...choice, columnId: '   ' })),
        },
      };
      const onCandidateChange = vi.fn();
      renderLive({ capabilities: blankColumnCapabilities, onCandidateChange });

      fireEvent.change(filterSourceSelect(), { target: { value: `source:${genderChoiceId}` } });
      const columnId = filterSourceSelect().value;
      expect(columnId).toMatch(/^filter-input_/);
      fireEvent.change(screen.getByRole('textbox', { name: 'Value' }), { target: { value: 'female' } });
      expect(onCandidateChange.mock.lastCall?.[0]?.filterSources).toEqual([{ choiceId: genderChoiceId, columnId }]);

      const savedBlankFilter: ConstructionStep = {
        id: 'filter-blank-saved',
        inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: { kind: 'FILTER', filter: { columnId: 'filter-input_blank', operator: 'MISSING' } },
        outputs: [],
      };
      const savedOnCandidateChange = vi.fn();
      cleanup();
      renderLive({
        capabilities: blankColumnCapabilities,
        construction: { version: 1, steps: [savedBlankFilter] },
        editingStep: savedBlankFilter,
        onCandidateChange: savedOnCandidateChange,
      });
      expect(screen.getByText('The saved condition column is no longer present in Loom’s predecessor stage.')).toBeInTheDocument();
      expect(savedOnCandidateChange).toHaveBeenLastCalledWith(undefined);
    });
  });

  describe('calculate with unselected dataset fields', () => {
    const birthChoice: DeriveSourceChoice = {
      choiceId: 'derive-choice-birth',
      fieldPath: 'multipleBirthInteger',
      label: 'Patient.multipleBirthInteger',
      logicalType: 'integer',
      isPopulated: true,
      columnId: '',
    };
    const weightChoice: DeriveSourceChoice = {
      choiceId: 'derive-choice-weight',
      fieldPath: 'bodyWeight',
      label: 'Patient.bodyWeight',
      logicalType: 'decimal',
      isPopulated: true,
      columnId: '',
    };
    const unpopulatedChoice: DeriveSourceChoice = {
      choiceId: 'derive-choice-unpopulated',
      fieldPath: 'multipleBirthBoolean',
      label: 'Patient.multipleBirthBoolean',
      logicalType: 'integer',
      isPopulated: false,
      columnId: '',
    };
    const supported = (choices: DeriveSourceInput['choices']): DeriveSourceInput => ({
      supported: true,
      stageId: 'source_projection',
      choices,
    });

    const operandColumn = (which: 'First' | 'Second'): HTMLSelectElement =>
      screen.getByRole<HTMLSelectElement>('combobox', { name: `${which} value column` });

    const setRightLiteral = (text: string) => {
      fireEvent.change(screen.getByRole('combobox', { name: 'Second value kind' }), { target: { value: 'literal' } });
      fireEvent.change(screen.getByRole('combobox', { name: 'Second value number type' }), { target: { value: 'INTEGER' } });
      fireEvent.change(screen.getByRole('spinbutton', { name: 'Second value number' }), { target: { value: text } });
    };

    it('offers an unmaterialised dataset field as an arithmetic operand and signs the selection', () => {
      const onCandidateChange = vi.fn();
      renderEditor({ family: 'CALCULATE', onCandidateChange, deriveSourceInput: supported([birthChoice, unpopulatedChoice]) });

      setRightLiteral('2');
      const first = operandColumn('First');
      const group = sourceOptgroup(first);
      expect(group).not.toBeNull();
      expect(Array.from(group!.querySelectorAll('option')).map((option) => option.value)).toEqual([`source:${birthChoice.choiceId}`]);

      fireEvent.change(first, { target: { value: `source:${birthChoice.choiceId}` } });
      const columnId = operandColumn('First').value;
      expect(columnId).toMatch(/^derive-input_/);
      const candidate = onCandidateChange.mock.lastCall?.[0];
      expect(candidate?.deriveSources).toEqual([{ choiceId: birthChoice.choiceId, columnId }]);
      expect(candidate?.candidateConstruction.steps[0]?.operation).toMatchObject({
        kind: 'DERIVE',
        derive: { left: { kind: 'COLUMN', columnId }, right: { kind: 'LITERAL', literal: { kind: 'INTEGER', integer: 2 } } },
      });
      expect(candidate?.candidateConstruction.steps[0]?.outputs).toContainEqual(
        expect.objectContaining({ id: columnId, label: 'Patient.multipleBirthInteger', type: 'integer' }),
      );
    });

    it('binds both operand slots to distinct dataset fields', () => {
      const onCandidateChange = vi.fn();
      renderEditor({ family: 'CALCULATE', onCandidateChange, deriveSourceInput: supported([birthChoice, weightChoice]) });

      fireEvent.change(screen.getByRole('combobox', { name: 'First value kind' }), { target: { value: 'column' } });
      fireEvent.change(operandColumn('First'), { target: { value: `source:${birthChoice.choiceId}` } });
      fireEvent.change(screen.getByRole('combobox', { name: 'Second value kind' }), { target: { value: 'column' } });
      const second = operandColumn('Second');
      const group = sourceOptgroup(second);
      expect(group).not.toBeNull();
      expect(Array.from(group!.querySelectorAll('option')).map((option) => option.value)).toEqual([`source:${weightChoice.choiceId}`]);
      fireEvent.change(second, { target: { value: `source:${weightChoice.choiceId}` } });

      const candidate = onCandidateChange.mock.lastCall?.[0];
      const leftId = operandColumn('First').value;
      const rightId = operandColumn('Second').value;
      expect(candidate?.deriveSources).toEqual([
        { choiceId: birthChoice.choiceId, columnId: leftId },
        { choiceId: weightChoice.choiceId, columnId: rightId },
      ]);
      expect(candidate?.candidateConstruction.steps[0]?.operation).toMatchObject({
        derive: {
          left: { kind: 'COLUMN', columnId: leftId },
          right: { kind: 'COLUMN', columnId: rightId },
        },
      });
    });

    it('drops the signed selection when the operand returns to a stage column', () => {
      const onCandidateChange = vi.fn();
      renderEditor({ family: 'CALCULATE', onCandidateChange, deriveSourceInput: supported([birthChoice]) });

      setRightLiteral('2');
      fireEvent.change(operandColumn('First'), { target: { value: `source:${birthChoice.choiceId}` } });
      expect(onCandidateChange.mock.lastCall?.[0]?.deriveSources).toHaveLength(1);

      fireEvent.change(operandColumn('First'), { target: { value: 'weight-id' } });
      const candidate = onCandidateChange.mock.lastCall?.[0];
      expect(candidate?.deriveSources).toBeUndefined();
      expect(candidate?.candidateConstruction.steps[0]?.operation).toMatchObject({
        derive: { left: { kind: 'COLUMN', columnId: 'weight-id' } },
      });
      const group = sourceOptgroup(operandColumn('First'));
      expect(Array.from(group!.querySelectorAll('option')).map((option) => option.value)).toEqual([`source:${birthChoice.choiceId}`]);
    });

    it('restores a saved operand that is backed by a materialised choice', () => {
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
        outputs: [...columns, { id: 'calculated-id', name: 'birth_plus_two', label: 'Birth plus two', type: 'integer' }],
      };
      const onCandidateChange = vi.fn();
      renderEditor({
        family: 'CALCULATE',
        construction: { version: 1, steps: [saved] },
        editingStep: saved,
        deriveSourceInput: supported([{ ...birthChoice, columnId: 'derive-input_saved' }]),
        onCandidateChange,
      });

      expect(operandColumn('First').value).toBe('derive-input_saved');
      fireEvent.change(screen.getByRole('spinbutton', { name: 'Second value number' }), { target: { value: '3' } });
      const candidate = onCandidateChange.mock.lastCall?.[0];
      expect(candidate?.deriveSources).toEqual([{ choiceId: birthChoice.choiceId, columnId: 'derive-input_saved' }]);
      expect(candidate?.changedStepId).toBe('derive-source-saved');
    });

    it('keeps plain selected-column calculations unchanged when no deriveSourceInput is returned', () => {
      const onCandidateChange = vi.fn();
      renderEditor({ family: 'CALCULATE', onCandidateChange });

      setRightLiteral('5');
      const candidate = onCandidateChange.mock.lastCall?.[0];
      expect(candidate?.deriveSources).toBeUndefined();
      expect(candidate?.candidateConstruction.steps[0]?.operation).toMatchObject({
        derive: {
          left: { kind: 'COLUMN', columnId: 'age-id' },
          right: { kind: 'LITERAL', literal: { kind: 'INTEGER', integer: 5 } },
        },
      });
      expect(sourceOptgroup(operandColumn('First'))).toBeNull();
    });

    it('explains why dataset-field operands are unavailable at the stage', () => {
      const onCandidateChange = vi.fn();
      renderEditor({
        family: 'CALCULATE',
        onCandidateChange,
        deriveSourceInput: {
          supported: false,
          stageId: 'source_projection',
          reasonCode: 'NO_ELIGIBLE_POPULATED_DERIVE_SOURCE_FIELDS',
          reason: 'No populated numeric root scalar is available.',
          choices: [],
        },
      });

      expect(screen.getByTestId('construction-calculate-left-source-unavailable'))
        .toHaveTextContent('NO_ELIGIBLE_POPULATED_DERIVE_SOURCE_FIELDS · No populated numeric root scalar is available.');
      expect(sourceOptgroup(operandColumn('First'))).toBeNull();
    });
  });
});
