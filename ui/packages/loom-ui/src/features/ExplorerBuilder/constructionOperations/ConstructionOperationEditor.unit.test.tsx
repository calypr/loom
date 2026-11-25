// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createLoomClient } from '../../../client/api';
import {
  constructionSchema,
  constructionCapabilitiesResponseSchema,
  type Construction,
  type ConstructionCapabilitiesResponse,
  type ConstructionProposalResponse,
  type ConstructionProposalRequest,
  type ConstructionStageDescriptor,
  type ConstructionStep,
} from '../../../contracts/types';
import {
  liveFilterSourceUnmaterializedProvenance,
  liveFilterSourceUnmaterializedResponse,
  liveFilterSourceUnmaterializedResponseJson,
} from '../../../../testSupport/liveConstructionCapabilitiesFixtures';
import { ConstructionOperationEditor } from './ConstructionOperationEditor';
import { capabilitiesFor as makeCapabilities, sourceStage as makeSourceStage } from '../../../../testSupport/constructionFixtures';
import { sourceStage as reviewSourceStage } from '../../../../testSupport/constructionFixtures';
import { type ConstructionDeriveSourceSelection } from '../../../contracts/types';

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

const sourceStage: ConstructionStageDescriptor = makeSourceStage(columns, {
  unavailableReason: 'Not needed by this editor test.',
});

type GroupFieldChoice = ConstructionCapabilitiesResponse['groupFieldChoices'][number];
type GroupRowValueChoice = ConstructionCapabilitiesResponse['groupRowValueChoices'][number];

const capabilitiesFor = (
  construction: Construction = { version: 1, steps: [] },
  stages: ReadonlyArray<ConstructionStageDescriptor> = [sourceStage],
  selectedStage: ConstructionStageDescriptor = sourceStage,
  filterSourceInput?: FilterSourceInput,
  deriveSourceInput?: DeriveSourceInput,
  groupFieldChoices: ReadonlyArray<GroupFieldChoice> = [],
  groupRowValueChoices: ReadonlyArray<GroupRowValueChoice> = [],
): ConstructionCapabilitiesResponse => makeCapabilities({
  selectedStage,
  stages,
  baseConstruction: construction,
  groupFieldChoices,
  groupRowValueChoices,
  overrides: {
    ...(filterSourceInput ? { filterSourceInput } : {}),
    ...(deriveSourceInput ? { deriveSourceInput } : {}),
  },
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
  readonly groupFieldChoices?: ReadonlyArray<GroupFieldChoice>;
  readonly groupRowValueChoices?: ReadonlyArray<GroupRowValueChoice>;
  readonly onCandidateChange?: (candidate: CandidateIntent | undefined) => void;
  readonly onEditStep?: (stepId: string) => void;
}) => {
  const onCandidateChange = args.onCandidateChange ?? vi.fn();
  const onEditStep = args.onEditStep ?? vi.fn();
  render(
    <ConstructionOperationEditor
      family={args.family}
      construction={args.construction ?? { version: 1, steps: [] }}
      capabilities={capabilitiesFor(args.construction, args.stages, args.selectedStage, args.filterSourceInput, args.deriveSourceInput, args.groupFieldChoices ?? [], args.groupRowValueChoices ?? [])}
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

    expect(screen.getByRole<HTMLInputElement>('textbox', { name: 'Output column label' }).value).toBe('Calculated value');
    expect(screen.queryByRole('textbox', { name: 'Output column name' })).not.toBeInTheDocument();
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

    expect(screen.getByTestId('construction-calculate-replacement')).toHaveTextContent('Applying updates the existing column');
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

    const fieldsCarriedByPublicColumns = (capabilities: ConstructionCapabilitiesResponse): Set<string> => {
      const stage = capabilities.selectedStage;
      const carried = new Set<string>();
      for (const field of [...capabilities.groupFieldChoices, ...capabilities.groupRowValueChoices]) {
        if (field.kind !== 'FIELD_SOURCE' || field.valueKind !== 'SCALAR') continue;
        if (field.sourceRole !== 'ROOT' || field.boundarySelector !== undefined) continue;
        const columnId = field.inputColumnId ?? '';
        if (columnId.trim() === '' || !stage.columns.some((column) => column.id === columnId)) continue;
        carried.add(field.canonicalPath);
      }
      return carried;
    };

    const expectedSignedValues = (capabilities: ConstructionCapabilitiesResponse): string[] => {
      const carried = fieldsCarriedByPublicColumns(capabilities);
      return (capabilities.filterSourceInput?.choices ?? [])
        .filter((choice) => !carried.has(choice.fieldPath))
        .map((choice) => `source:${choice.choiceId}`);
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
      expect(optgroupValues(group)).toEqual(expectedSignedValues(capabilities));

      const carriedIdColumnId = capabilities.selectedStage.columns[0]!.id;
      expect(fieldsCarriedByPublicColumns(capabilities)).toEqual(new Set(['id']));
      expect(optgroupValues(group)).not.toContain(`source:${liveChoiceIdByPath(capabilities, 'id')}`);
      expect(Array.from(select.options).some((option) => option.value === carriedIdColumnId)).toBe(true);
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
      expect(onCandidateChange).not.toHaveBeenCalled();
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

      expect(identityColumnId).toBe('source_ef82eef4462a7f20fbcecfff');
      expect(optgroupValues(sourceOptgroup(select)!)).toEqual(expectedSignedValues(capabilities));

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
      expect(savedOnCandidateChange).not.toHaveBeenCalled();
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
        .toHaveTextContent('No populated numeric root scalar is available.');
      expect(sourceOptgroup(operandColumn('First'))).toBeNull();
    });
  });

  describe('row-source picker empty and refused-field explanations (ROW-SOURCE-06)', () => {
    const unpopulatedChoice = (choiceId: string, fieldPath: string): FilterSourceChoice => ({
      choiceId,
      fieldPath,
      label: fieldPath,
      logicalType: 'string',
      isPopulated: false,
    });

    it('lists exactly the unpopulated dataset fields when nothing has values (filter lane)', () => {
      renderEditor({
        family: 'KEEP_ROWS',
        onCandidateChange: vi.fn(),
        filterSourceInput: {
          supported: true,
          stageId: 'source_projection',
          choices: [unpopulatedChoice('choice-status', 'root.status'), unpopulatedChoice('choice-note', 'root.note.text')],
        },
      });
      const status = screen.getByTestId('source-picker-explanation');
      expect(status.getAttribute('role')).toBe('status');
      expect(status.textContent).toContain('contains values in this dataset yet');
      const items = within(screen.getByTestId('source-picker-empty-list')).getAllByTestId('source-picker-empty-list-item');
      expect(items.map((item) => item.getAttribute('title'))).toEqual(['root.status', 'root.note.text']);
    });

    it('explains that the API listed no dataset fields without inventing a reason (filter lane)', () => {
      renderEditor({
        family: 'KEEP_ROWS',
        onCandidateChange: vi.fn(),
        filterSourceInput: { supported: true, stageId: 'source_projection', choices: [] },
      });
      expect(screen.getByTestId('source-picker-explanation').textContent)
        .toBe('Loom has not listed any dataset fields for this step.');
    });

    it('shows no false all-bound explanation when the picker only holds its own saved selection (filter lane)', () => {
      const savedSourceFilter: ConstructionStep = {
        id: 'filter-source-saved',
        inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: { kind: 'FILTER', filter: { columnId: 'filter-input_saved', operator: 'EXISTS' } },
        outputs: [...columns, { id: 'filter-input_saved', name: 'root_status', label: 'Observation.status', type: 'string' }],
      };
      renderEditor({
        family: 'KEEP_ROWS',
        construction: { version: 1, steps: [savedSourceFilter] },
        stages: [sourceStage, { ...sourceStage, id: savedSourceFilter.id, inputStageId: sourceStage.id, operation: 'FILTER' }],
        editingStep: savedSourceFilter,
        onCandidateChange: vi.fn(),
        filterSourceInput: {
          supported: true,
          stageId: 'source_projection',
          choices: [{ choiceId: 'choice-status', fieldPath: 'root.status', label: 'Observation.status', logicalType: 'string', isPopulated: true, columnId: 'filter-input_saved' }],
        },
      });
      expect(screen.queryByTestId('source-picker-explanation')).toBeNull();
    });

    it('renders no explanation while eligible populated dataset fields exist (filter lane)', () => {
      renderEditor({
        family: 'KEEP_ROWS',
        onCandidateChange: vi.fn(),
        filterSourceInput: {
          supported: true,
          stageId: 'source_projection',
          choices: [
            { choiceId: 'choice-status', fieldPath: 'root.status', label: 'Observation status', logicalType: 'string', isPopulated: true, columnId: '' },
            unpopulatedChoice('choice-note', 'root.note.text'),
          ],
        },
      });
      expect(screen.queryByTestId('source-picker-explanation')).toBeNull();
    });

    it('explains the unavailable dataset-field operand states in the derive numeric picker', () => {
      const onCandidateChange = vi.fn();
      renderEditor({
        family: 'CALCULATE',
        onCandidateChange,
        selectedColumns: ['weight-id'],
        deriveSourceInput: {
          supported: true,
          stageId: 'source_projection',
          choices: [unpopulatedChoice('choice-status', 'root.status')],
        },
      });
      fireEvent.change(screen.getByRole('combobox', { name: 'First value kind' }), { target: { value: 'column' } });
      expect(screen.getByTestId('source-picker-explanation').textContent).toContain('contains values in this dataset yet');
      expect(within(screen.getByTestId('source-picker-empty-list')).getAllByTestId('source-picker-empty-list-item')
        .map((item) => item.getAttribute('title'))).toEqual(['root.status']);
    });
    it('explains that the API listed no dataset fields without inventing a reason (derive lane, ROW-SOURCE-06 audit)', () => {
      const onCandidateChange = vi.fn();
      renderEditor({
        family: 'CALCULATE',
        onCandidateChange,
        selectedColumns: ['weight-id'],
        deriveSourceInput: { supported: true, stageId: 'source_projection', choices: [] },
      });
      fireEvent.change(screen.getByRole('combobox', { name: 'First value kind' }), { target: { value: 'column' } });
      expect(screen.getByTestId('source-picker-explanation').textContent)
        .toBe('Loom has not listed any dataset fields for this step.');
      expect(screen.queryByTestId('source-picker-empty-list')).toBeNull();
    });
    it('renders one shared explanation for both derive numeric picker slots when both are column-bound', () => {
      const onCandidateChange = vi.fn();
      renderEditor({
        family: 'CALCULATE',
        onCandidateChange,
        selectedColumns: ['weight-id'],
        deriveSourceInput: {
          supported: true,
          stageId: 'source_projection',
          choices: [unpopulatedChoice('choice-status', 'root.status')],
        },
      });
      fireEvent.change(screen.getByRole('combobox', { name: 'First value kind' }), { target: { value: 'column' } });
      fireEvent.change(screen.getByRole('combobox', { name: 'Second value kind' }), { target: { value: 'column' } });
      expect(screen.getAllByTestId('source-picker-explanation')).toHaveLength(1);
    });

    it('keeps the lane-level server refusal notice distinct from the zero-choice explanation', () => {
      const onCandidateChange = vi.fn();
      renderEditor({
        family: 'KEEP_ROWS',
        onCandidateChange,
        filterSourceInput: {
          supported: false,
          stageId: 'source_projection',
          reasonCode: 'FILTER_SOURCE_REQUIRES_SCALAR_KIND',
          reason: 'Filters need scalar fields.',
          choices: [],
        },
      });
      expect(screen.getByTestId('construction-filter-source-unavailable').textContent)
        .toContain('Filters need scalar fields.');
      expect(screen.queryByTestId('source-picker-explanation')).toBeNull();
    });
  });

  describe('audit07 public-column reuse', () => {
    const AUDIT_GENDER_COLUMN = { id: 'gender-id', name: 'gender', label: 'Gender', type: 'string' };
    const AUDIT_STAGE_COLUMNS: ConstructionStageDescriptor['columns'] = [
      AUDIT_GENDER_COLUMN,
      { id: 'status-id', name: 'status', label: 'Status', type: 'string' },
    ];
    const auditGenderChoice = {
      choiceId: 'choice-audit-gender',
      fieldPath: 'gender',
      label: 'Patient.gender',
      logicalType: 'string',
      isPopulated: true,
      columnId: '',
    };

    const auditGenderPublicIdentity: GroupRowValueChoice = {
      kind: 'FIELD_SOURCE',
      valueKind: 'SCALAR',
      fieldSource: { kind: 'CHOICE', choiceId: 'group-choice-audit-gender' },
      label: 'Gender',
      sourceStageId: 'source_projection',
      sourceShapeId: 'group-source-shape-v1:audit',
      candidateId: 'candidate-audit-gender',
      sourceRole: 'ROOT',
      occurrenceId: 'base',
      resourceType: 'string',
      ownerOccurrenceId: 'base',
      ownerResourceType: 'Patient',
      canonicalPath: 'gender',
      valueType: 'string',
      inputColumnId: 'gender-id',
    };

    const renderAudit = () => {
      const onCandidateChange = vi.fn();
      renderEditor({
        family: 'KEEP_ROWS',

        construction: { version: 1, steps: [] },
        selectedStage: { ...sourceStage, columns: AUDIT_STAGE_COLUMNS },
        filterSourceInput: { supported: true, stageId: 'source_projection', choices: [auditGenderChoice] },
        groupFieldChoices: [auditGenderPublicIdentity],
        onCandidateChange,
      });
      return onCandidateChange;
    };

    const genderOptions = (): HTMLOptionElement[] => Array.from(filterSourceSelect().options)
      .filter((option) => option.value === AUDIT_GENDER_COLUMN.id || option.getAttribute('title') === 'gender');

    it('offers no duplicate signed option while the public column carries the field', () => {
      renderAudit();
      const options = Array.from(filterSourceSelect().options);
      expect(options.filter((option) => option.value.startsWith('source:'))).toEqual([]);
      expect(genderOptions().map((option) => option.value)).toEqual([AUDIT_GENDER_COLUMN.id]);
    });

    it('reuses the existing public column id when the gender field is chosen, with no minted filter-input binding', () => {
      const onCandidateChange = renderAudit();
      const signed = Array.from(filterSourceSelect().options)
        .find((option) => option.value === `source:${auditGenderChoice.choiceId}`);
      const target = signed ?? genderOptions()[0];
      if (!target) throw new Error('Expected the public gender column to stay offered.');
      fireEvent.change(filterSourceSelect(), { target: { value: target.value } });
      fireEvent.change(screen.getByRole('textbox', { name: 'Value' }), { target: { value: 'female' } });
      const candidate = onCandidateChange.mock.lastCall?.[0] as CandidateIntent | undefined;
      if (!candidate) throw new Error('Expected a filter candidate.');
      const bindings = candidate.filterSources ?? [];
      expect(bindings.every((binding) => binding.columnId === AUDIT_GENDER_COLUMN.id)).toBe(true);
      expect(bindings.some((binding) => /^filter-input_/.test(binding.columnId))).toBe(false);
      const step = candidate.candidateConstruction.steps.at(-1);
      expect(step?.operation.kind).toBe('FILTER');
      expect(step?.operation.kind === 'FILTER' ? step.operation.filter.columnId : undefined).toBe(AUDIT_GENDER_COLUMN.id);
      expect((step?.outputs ?? []).some((output) => /^filter-input_/.test(output.id))).toBe(false);
    });
  });
});

{
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
  const selectedStage = args.selectedStage ?? reviewSourceStage(columns);
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
        sourceInput: { supported: false, stageId: selectedStage.id, reasonCode: 'TEST_FIXTURE_NO_CHOICES', choices: [] },
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
      selectedStage: reviewSourceStage([{ id: 'status-id', name: 'status', label: 'Status', type: 'string' }]),
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
      selectedStage: reviewSourceStage([{ id: 'status-id', name: 'status', label: 'Status', type: 'string' }]),
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
      selectedStage: reviewSourceStage([{ id: 'status-id', name: 'status', label: 'Status', type: 'string' }]),
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
      selectedStage: reviewSourceStage([]),
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
      selectedStage: reviewSourceStage([]),
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
      selectedStage: reviewSourceStage([{ id: 'status-id', name: 'status', label: 'Status', type: 'string' }]),
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
      selectedStage: reviewSourceStage([{ id: 'status-id', name: 'status', label: 'Status', type: 'string' }]),
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
}

{
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

const renderEditor = (args: {
  readonly family: 'KEEP_ROWS' | 'CALCULATE';
  readonly construction?: Construction;
  readonly stageColumns: ConstructionStageDescriptor['columns'];
  readonly filterSourceInput?: ConstructionCapabilitiesResponse['filterSourceInput'];
  readonly deriveSourceInput?: ConstructionCapabilitiesResponse['deriveSourceInput'];
  readonly editingStep?: ConstructionStep;
  readonly onCandidateChange?: (candidate: CandidateIntent | undefined) => void;
}) => {
  const selectedStage = reviewSourceStage(args.stageColumns, { unavailableReason: 'Not needed in this test.' });
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
        sourceInput: { supported: false, stageId: selectedStage.id, reasonCode: 'TEST_FIXTURE_NO_CHOICES', choices: [] },
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

const filterColumnSelect = (): HTMLSelectElement =>
  screen.getByRole('combobox', { name: 'Column' }) as HTMLSelectElement;

const operandColumn = (label: string): HTMLSelectElement =>
  screen.getByRole('combobox', { name: `${label} column` }) as HTMLSelectElement;

const operandKind = (label: string): HTMLSelectElement =>
  screen.getByRole('combobox', { name: `${label} kind` }) as HTMLSelectElement;

const stringCol = (id: string, label: string) => ({ id, name: id, label, type: 'string' });

const intCol = (id: string, label: string) => ({ id, name: id, label, type: 'integer' });

const decCol = (id: string, label: string) => ({ id, name: id, label, type: 'decimal' });

const filterChoice = (choiceId: string, fieldPath: string, label: string, isPopulated = true, columnId?: string) => ({
  choiceId,
  fieldPath,
  label,
  logicalType: 'integer' as const,
  isPopulated,
  ...(columnId ? { columnId } : {}),
});

const deriveChoice = (choiceId: string, fieldPath: string, label: string, isPopulated = true, columnId?: string) => ({
  choiceId,
  fieldPath,
  label,
  logicalType: 'decimal' as const,
  isPopulated,
  ...(columnId ? { columnId } : {}),
});

const withProjections = (projections: Construction['sourceProjections']): Construction => ({
  version: 1,
  steps: [],
  ...(projections ? { sourceProjections: projections } : {}),
});

const optionValues = (select: HTMLSelectElement) => [...select.options].map((option) => option.value);

const explanationNodes = () => screen.queryAllByTestId('source-picker-explanation');

const allBoundText = () =>
  screen.queryByText('Every eligible dataset field that contains values is already used by another source selection here. Remove one to choose a different field.');

const nonePopulatedText = () =>
  screen.queryByText('No eligible dataset field contains values in this dataset yet.');

function renderPair(args: {
  readonly stageColumns: ConstructionStageDescriptor['columns'];
  readonly choices: ReturnType<typeof deriveChoice>[];
  readonly projections?: Construction['sourceProjections'];
}) {
  const selectedStage = reviewSourceStage(args.stageColumns);
  const capabilities: ConstructionCapabilitiesResponse = {
    snapshotToken: 'snapshot-1',
    draftVersion: 1,
    draftDigest: 'draft-digest-1',
    outputId: 'table-1',
    stageId: selectedStage.id,
    sourceInput: { supported: false, stageId: selectedStage.id, reasonCode: 'TEST_FIXTURE_NO_CHOICES', choices: [] },
    baseConstruction: { version: 1, steps: [] },
    stages: [selectedStage],
    selectedStage,
    groupFieldChoices: [],
    groupRowValueChoices: [],
    workspaceInputs: [],
    deriveSourceInput: { supported: true, stageId: 'source_projection', choices: args.choices },
  };
  const initial = (
    <ConstructionOperationEditor
      family="CALCULATE"
      construction={withProjections(args.projections)}
      capabilities={capabilities}
      editingStep={undefined}
      disabled={false}
      onCandidateChange={vi.fn()}
      onEditStep={vi.fn()}
    />
  );
  const view = render(initial);
  return {
    rerender: () => undefined,
    rerenderPair: (next: { stageColumns: ConstructionStageDescriptor['columns']; choices: ReturnType<typeof deriveChoice>[]; projections?: Construction['sourceProjections'] }) => {
      const stage = reviewSourceStage(next.stageColumns);
      view.rerender(
        <ConstructionOperationEditor
          family="CALCULATE"
          construction={withProjections(next.projections)}
          capabilities={{ ...capabilities, stages: [stage], selectedStage: stage }}
          editingStep={undefined}
          disabled={false}
          onCandidateChange={vi.fn()}
          onEditStep={vi.fn()}
        />,
      );
    },
  };
}

describe('ATTACK 1 label-matching cannot cross-suppress', () => {
  const choiceA = filterChoice('choice-a', 'root.age', 'Age');
  const choiceB = filterChoice('choice-b', 'root.note', 'Note');

  it('suppression follows projection fieldPath only, never column labels', () => {
    renderEditor({
      family: 'KEEP_ROWS',
      construction: withProjections([projection('col-age', 'root.age')]),

      stageColumns: [intCol('col-age', 'root.note'), intCol('col-note', 'Age')],
      filterSourceInput: { supported: true, stageId: 'source_projection', choices: [choiceA, choiceB] },
    });
    const values = optionValues(filterColumnSelect());
    expect(values).not.toContain('source:choice-a');
    expect(values).toContain('source:choice-b');
  });

  it('mirrored: label-equals-fieldPath on an unprojected column does not suppress the projected field choice', () => {
    renderEditor({
      family: 'KEEP_ROWS',
      construction: withProjections([projection('col-note', 'root.note')]),
      stageColumns: [intCol('col-age', 'root.note'), intCol('col-note', 'Age')],
      filterSourceInput: { supported: true, stageId: 'source_projection', choices: [choiceA, choiceB] },
    });
    const values = optionValues(filterColumnSelect());
    expect(values).toContain('source:choice-a');
    expect(values).not.toContain('source:choice-b');
  });
});

describe('ATTACK 2 ambiguity: 3-way and duplicate-columnId payloads', () => {
  const choiceA = filterChoice('choice-a', 'root.age', 'Age');

  it('two live + one stale projection for one fieldPath keeps the signed choice (usable==2)', () => {
    renderEditor({
      family: 'KEEP_ROWS',
      construction: withProjections([
        projection('col-a', 'root.age'),
        projection('col-b', 'root.age'),
        projection('col-gone', 'root.age'),
      ]),
      stageColumns: [intCol('col-a', 'Age'), intCol('col-b', 'Age copy'), stringCol('other', 'Other')],
      filterSourceInput: { supported: true, stageId: 'source_projection', choices: [choiceA] },
    });
    expect(optionValues(filterColumnSelect())).toContain('source:choice-a');
  });

  it('two live projection entries sharing ONE columnId value: falls back to signed (reported behaviour)', () => {
    renderEditor({
      family: 'KEEP_ROWS',
      construction: withProjections([
        projection('col-dup', 'root.age'),
        projection('col-dup', 'root.age'),
      ]),
      stageColumns: [intCol('col-dup', 'Age'), stringCol('other', 'Other')],
      filterSourceInput: { supported: true, stageId: 'source_projection', choices: [choiceA] },
    });

    const values = optionValues(filterColumnSelect());
    expect(values).toContain('source:choice-a');
    expect(values).toContain('col-dup');
  });
});

describe("ATTACK 3 banner honesty with reused choices", () => {
it('filter lane: all reused, none bound → no banner anywhere', () => {
    const choiceA = filterChoice('choice-a', 'root.age', 'Age');
    const choiceB = filterChoice('choice-b', 'root.note', 'Note');
    renderEditor({
      family: 'KEEP_ROWS',
      construction: withProjections([projection('col-age', 'root.age'), projection('col-note', 'root.note')]),
      stageColumns: [intCol('col-age', 'Age'), intCol('col-note', 'Note')],
      filterSourceInput: { supported: true, stageId: 'source_projection', choices: [choiceA, choiceB] },
    });
    expect(explanationNodes()).toHaveLength(0);
    expect(allBoundText()).toBeNull();
    expect(nonePopulatedText()).toBeNull();
  });

it('filter lane: all reused + one stale unpopulated choice → still no dishonest banner', () => {
    const choiceA = filterChoice('choice-a', 'root.age', 'Age');
    const choiceB = filterChoice('choice-b', 'root.note', 'Note');
    const stale = filterChoice('choice-c', 'root.dead', 'Dead', false);
    renderEditor({
      family: 'KEEP_ROWS',
      construction: withProjections([projection('col-age', 'root.age'), projection('col-note', 'root.note')]),
      stageColumns: [intCol('col-age', 'Age'), intCol('col-note', 'Note')],
      filterSourceInput: { supported: true, stageId: 'source_projection', choices: [choiceA, choiceB, stale] },
    });

    expect(allBoundText()).toBeNull();
    expect(nonePopulatedText()).toBeNull();
    expect(explanationNodes()).toHaveLength(0);
  });

it('derive lane: all reused, column-kind operand, no bindings → no banner', () => {
    const a = deriveChoice('choice-a', 'root.age', 'Age');
    const b = deriveChoice('choice-b', 'root.note', 'Note');
    renderEditor({
      family: 'CALCULATE',
      construction: withProjections([projection('w-age', 'root.age'), projection('w-note', 'root.note')]),
      stageColumns: [decCol('w-age', 'AgeN'), decCol('w-note', 'NoteN')],
      deriveSourceInput: { supported: true, stageId: 'source_projection', choices: [a, b] },
    });
    fireEvent.change(operandKind('First value'), { target: { value: 'column' } });
    fireEvent.change(operandKind('Second value'), { target: { value: 'column' } });
    expect(explanationNodes()).toHaveLength(0);
    expect(allBoundText()).toBeNull();
    expect(nonePopulatedText()).toBeNull();
  });
});

describe('ATTACK 4 derive operand pair consistency', () => {
  it('both operands open on the SAME reused existing column: no source option anywhere', () => {
    const a = deriveChoice('choice-a', 'root.age', 'Age');
    renderEditor({
      family: 'CALCULATE',
      construction: withProjections([projection('weight-id', 'root.age')]),
      stageColumns: [intCol('weight-id', 'Weight'), stringCol('status-id', 'Status')],
      deriveSourceInput: { supported: true, stageId: 'source_projection', choices: [a] },
    });
    fireEvent.change(operandKind('First value'), { target: { value: 'column' } });
    fireEvent.change(operandKind('Second value'), { target: { value: 'column' } });
    const left = optionValues(operandColumn('First value'));
    const right = optionValues(operandColumn('Second value'));
    expect(left).toEqual(['weight-id']);
    expect(right).toEqual(['weight-id']);
    expect(left.some((v) => v.startsWith('source:'))).toBe(false);
    expect(right.some((v) => v.startsWith('source:'))).toBe(false);
    expect(allBoundText()).toBeNull();
  });

  it('left reuses column while right binds a DIFFERENT choice: suppression identical, no false all-bound', () => {
    const a = deriveChoice('choice-a', 'root.age', 'Age');
    const b = deriveChoice('choice-b', 'root.weight', 'Weight', true, 'derive-input_weight');
    const onCandidateChange = renderEditor({
      family: 'CALCULATE',
      construction: withProjections([projection('age-col', 'root.age')]),
      stageColumns: [intCol('age-col', 'Age')],
      deriveSourceInput: { supported: true, stageId: 'source_projection', choices: [a, b] },
    });
    fireEvent.change(operandKind('First value'), { target: { value: 'column' } });
    fireEvent.change(operandKind('Second value'), { target: { value: 'column' } });
    fireEvent.change(operandColumn('Second value'), { target: { value: 'source:choice-b' } });
    const left = optionValues(operandColumn('First value'));
    const right = optionValues(operandColumn('Second value'));

    expect(left).not.toContain('source:choice-a');
    expect(right).not.toContain('source:choice-a');
    expect(right).toContain('derive-input_weight');
    expect(allBoundText()).toBeNull();
    const calls = (onCandidateChange as ReturnType<typeof vi.fn>).mock.calls.map((call) => call[0]);
    const candidate = [...calls].reverse().find((entry) => entry !== undefined);
    const bindings = candidate?.deriveSources ?? [];
    expect(bindings.map((binding: ConstructionDeriveSourceSelection) => binding.choiceId)).toEqual(['choice-b']);
    expect(bindings.every((binding: ConstructionDeriveSourceSelection) => binding.columnId !== 'age-col')).toBe(true);
  });

  it('reverse: bind first via source, then the projection lands — pair stays consistent, selection not rewritten', () => {
    const a = deriveChoice('choice-a', 'root.age', 'Age', true, 'derive-input_age');
    const stageColumns = [decCol('plain-n', 'PlainN')];

    const { rerenderPair } = renderPair({ stageColumns, choices: [a], projections: undefined });
    fireEvent.change(operandKind('Second value'), { target: { value: 'column' } });
    fireEvent.change(operandColumn('Second value'), { target: { value: 'source:choice-a' } });
    expect((screen.getByRole('combobox', { name: 'Second value column' }) as HTMLSelectElement).value)
      .toBe('derive-input_age');

    rerenderPair({ stageColumns, choices: [a], projections: [projection('derive-input_age', 'root.age')] });
    const left = optionValues(operandColumn('First value'));
    const right = optionValues(operandColumn('Second value')) ;
    expect((screen.getByRole('combobox', { name: 'Second value column' }) as HTMLSelectElement).value)
      .toBe('derive-input_age');
    expect(left.some((v) => v.startsWith('source:'))).toBe(false);
    expect(right).not.toContain('source:choice-a');
  });

  it('pair symmetry single-open-lane: identical option sets for left and right with mixed choices', () => {
    const a = deriveChoice('choice-a', 'root.age', 'Age');
    const b = deriveChoice('choice-b', 'root.weight', 'Weight');
    renderEditor({
      family: 'CALCULATE',
      construction: withProjections([projection('age-col', 'root.age')]),
      stageColumns: [intCol('age-col', 'Age')],
      deriveSourceInput: { supported: true, stageId: 'source_projection', choices: [a, b] },
    });
    fireEvent.change(operandKind('First value'), { target: { value: 'column' } });
    fireEvent.change(operandKind('Second value'), { target: { value: 'column' } });
    expect(optionValues(operandColumn('First value'))).toEqual(optionValues(operandColumn('Second value')));
  });

  it('no numeric column AND all choices reused → Numeric-column shortcut cannot materialize, no crash', () => {
    const a = deriveChoice('choice-a', 'root.age', 'Age', true, 'derive-input_age');
    const onCandidateChange = renderEditor({
      family: 'CALCULATE',
      construction: withProjections([projection('status-id', 'root.age')]),
      stageColumns: [stringCol('status-id', 'Status')],
      deriveSourceInput: { supported: true, stageId: 'source_projection', choices: [a] },
    });

    fireEvent.change(operandKind('First value'), { target: { value: 'column' } });

    const signedFirst = optionValues(operandColumn('First value'));
    expect(signedFirst).toEqual(['derive-input_age']);
    expect(signedFirst.some((v) => v.startsWith('source:'))).toBe(false);
    fireEvent.change(operandKind('Second value'), { target: { value: 'column' } });
    const signedCalls = (onCandidateChange as ReturnType<typeof vi.fn>).mock.calls.map((call) => call[0]);
    const signedCandidate = [...signedCalls].reverse().find((entry) => entry !== undefined);
    expect((signedCandidate?.deriveSources ?? []).map((binding: ConstructionDeriveSourceSelection) => binding.choiceId)).toEqual(['choice-a']);

    cleanup();
    const on2 = renderEditor({
      family: 'CALCULATE',
      construction: withProjections([projection('age-num', 'root.age')]),
      stageColumns: [decCol('age-num', 'AgeN')],
      deriveSourceInput: { supported: true, stageId: 'source_projection', choices: [a] },
    });
    const kind = operandKind('First value');
    const numericOption = [...kind.options].find((option) => option.value === 'column');

    expect(numericOption?.disabled).toBe(false);
    fireEvent.change(kind, { target: { value: 'column' } });
    expect(optionValues(operandColumn('First value'))).toEqual(['age-num']);
    const calls = (on2 as ReturnType<typeof vi.fn>).mock.calls.map((call) => call[0]);
    const candidate = [...calls].reverse().find((entry) => entry !== undefined);
    expect(candidate?.deriveSources ?? []).toEqual([]);
    void onCandidateChange;
  });
});

describe('ATTACK 4c chooseSourceOperand kept-filter (REV4)', () => {
  it('switching the shared operand keeps BOTH bindings when the other slot still consumes the old column', () => {
    const c1 = deriveChoice('choice-c1', 'root.a', 'Alpha', true, 'x-col');
    const c2 = deriveChoice('choice-c2', 'root.b', 'Beta', true, 'y-col');
    const onCandidateChange = renderEditor({
      family: 'CALCULATE',
      construction: withProjections(undefined),
      stageColumns: [],
      deriveSourceInput: { supported: true, stageId: 'source_projection', choices: [c1, c2] },
    });

    fireEvent.change(operandKind('First value'), { target: { value: 'column' } });
    expect(operandColumn('First value').value).toBe('x-col');
    fireEvent.change(operandKind('Second value'), { target: { value: 'column' } });
    expect(operandColumn('Second value').value).toBe('x-col');

    fireEvent.change(operandColumn('First value'), { target: { value: 'source:choice-c2' } });
    const calls = (onCandidateChange as ReturnType<typeof vi.fn>).mock.calls.map((call) => call[0]);
    const candidate = [...calls].reverse().find((entry) => entry !== undefined);
    const bindings = candidate?.deriveSources ?? [];
    expect(bindings.map((binding: ConstructionDeriveSourceSelection) => `${binding.choiceId}@${binding.columnId}`).sort())
      .toEqual(['choice-c1@x-col', 'choice-c2@y-col']);
  });
});

describe('ATTACK 4d REV4 pickerEmptinessHost ownVisible', () => {
  it('sole source choice bound as a virtual materialization → no false all-bound banner', () => {
    const c1 = deriveChoice('choice-c1', 'root.a', 'Alpha', true, 'x-col');
    renderEditor({
      family: 'CALCULATE',
      construction: withProjections(undefined),
      stageColumns: [],
      deriveSourceInput: { supported: true, stageId: 'source_projection', choices: [c1] },
    });
    fireEvent.change(operandKind('First value'), { target: { value: 'column' } });
    fireEvent.change(operandKind('Second value'), { target: { value: 'column' } });
    expect(explanationNodes()).toHaveLength(0);
    expect(allBoundText()).toBeNull();
  });

  it('foreign stage column reusing the materialization id cannot silence the explanation', () => {
    const c1 = deriveChoice('choice-c1', 'root.a', 'Alpha', true, 'x-col');
    renderEditor({
      family: 'CALCULATE',
      construction: withProjections(undefined),
      stageColumns: [decCol('x-col', 'Foreign numeric with same id')],
      deriveSourceInput: { supported: true, stageId: 'source_projection', choices: [c1] },
    });
    fireEvent.change(operandKind('First value'), { target: { value: 'column' } });
    fireEvent.change(operandColumn('First value'), { target: { value: 'source:choice-c1' } });

    expect(allBoundText()).not.toBeNull();
  });
});

describe('ATTACK 2c contradictory same-column dual-field projection', () => {
  it('documents: both signed choices are suppressed and one column carries both', () => {
    const a = filterChoice('choice-a', 'root.age', 'Age');
    const b = filterChoice('choice-b', 'root.note', 'Note');
    renderEditor({
      family: 'KEEP_ROWS',
      construction: withProjections([
        projection('col-x', 'root.age'),
        projection('col-x', 'root.note'),
      ]),
      stageColumns: [intCol('col-x', 'X'), stringCol('other', 'Other')],
      filterSourceInput: { supported: true, stageId: 'source_projection', choices: [a, b] },
    });
    const values = optionValues(filterColumnSelect());

    expect(values).not.toContain('source:choice-a');
    expect(values).not.toContain('source:choice-b');
    expect(values).toContain('col-x');
  });
});
}

{
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

const renderEditor = (args: {
  readonly family: 'KEEP_ROWS' | 'CALCULATE';
  readonly construction?: Construction;
  readonly stageColumns: ConstructionStageDescriptor['columns'];
  readonly filterSourceInput?: ConstructionCapabilitiesResponse['filterSourceInput'];
  readonly deriveSourceInput?: ConstructionCapabilitiesResponse['deriveSourceInput'];
  readonly editingStep?: ConstructionStep;
  readonly onCandidateChange?: (candidate: CandidateIntent | undefined) => void;
}) => {
  const selectedStage = reviewSourceStage(args.stageColumns, { unavailableReason: 'Not needed in this test.' });
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
        sourceInput: { supported: false, stageId: selectedStage.id, reasonCode: 'TEST_FIXTURE_NO_CHOICES', choices: [] },
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
        sourceInput: { supported: false, stageId: stage.id, reasonCode: 'TEST_FIXTURE_NO_CHOICES', choices: [] },
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

const withProjections = (projections: Construction['sourceProjections']): Construction => ({
  version: 1,
  steps: [],
  ...(projections ? { sourceProjections: projections } : {}),
});

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
      ...reviewSourceStage([intCol('age-col', 'Age (table)'), stringCol('status-id', 'Status')], { unavailableReason: 'Not needed in this test.' }),
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
}

{
type CandidateIntent = Pick<
  ConstructionProposalRequest,
  'candidateConstruction' | 'changedStepId' | 'removeStepIds' | 'filterSources' | 'deriveSources'
>;
type DeriveSourceInput = NonNullable<ConstructionCapabilitiesResponse['deriveSourceInput']>;

const sourceStage = (cols: ConstructionStageDescriptor['columns']): ConstructionStageDescriptor => ({
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

const stringCols: ConstructionStageDescriptor['columns'] = [
  { id: 'status-id', name: 'status', label: 'Status', type: 'string' },
];

const numericChoice = (choiceId: string, columnId: string, type: 'string' | 'decimal' = 'decimal', isPopulated = true) => ({
  choiceId,
  fieldPath: `root.${choiceId}`,
  label: choiceId,
  logicalType: type,
  isPopulated,
  columnId,
});

let lastCandidate: CandidateIntent | undefined;

const renderEditor = (args: {
  readonly selectedStage?: ConstructionStageDescriptor;
  readonly deriveSourceInput?: DeriveSourceInput;
  readonly construction?: Construction;
  readonly editingStep?: ConstructionStep;
  readonly rerenderable?: boolean;
}) => {
  const selectedStage = args.selectedStage ?? sourceStage(stringCols);
  const construction = args.construction ?? { version: 1, steps: [] };
  const onCandidateChange = vi.fn((candidate: CandidateIntent | undefined) => {
    lastCandidate = candidate;
  });
  const view = render(
    <ConstructionOperationEditor
      family="CALCULATE"
      construction={construction}
      capabilities={{
        snapshotToken: 'snapshot-1',
        draftVersion: 1,
        draftDigest: 'draft-digest-1',
        outputId: 'table-1',
        stageId: selectedStage.id,
        sourceInput: { supported: false, stageId: selectedStage.id, reasonCode: 'TEST_FIXTURE_NO_CHOICES', choices: [] },
        baseConstruction: construction,
        stages: [selectedStage],
        selectedStage,
        groupFieldChoices: [],
        groupRowValueChoices: [],
        workspaceInputs: [],
        ...(args.deriveSourceInput ? { deriveSourceInput: args.deriveSourceInput } : {}),
      }}
      editingStep={args.editingStep}
      selectedColumns={[]}
      disabled={false}
      onCandidateChange={onCandidateChange}
      onEditStep={vi.fn()}
    />,
  );
  return view;
};

const operandColumn = (label: string): HTMLSelectElement =>
  screen.getByRole('combobox', { name: `${label} column` }) as HTMLSelectElement;

const operandKind = (label: string): HTMLSelectElement =>
  screen.getByRole('combobox', { name: `${label} kind` }) as HTMLSelectElement;

const setKind = (label: string, value: 'column' | 'literal' | 'unset') => {
  fireEvent.change(operandKind(label), { target: { value } });
};

const sourceOptgroup = (select: HTMLSelectElement): HTMLOptGroupElement | null =>
  select.querySelector('optgroup[label="Dataset fields (not in output)"]');

const optValues = (select: HTMLSelectElement): string[] => {
  const group = sourceOptgroup(select);
  return group ? [...group.querySelectorAll('option')].map((o) => o.value) : [];
};

type Frame = {
  readonly label: string;
  readonly explainCount: number;
  readonly explainSlots: string[];
  readonly explainClass: string | null;
  readonly left: { readonly present: boolean; readonly options: string[]; readonly optgroup: string[] };
  readonly right: { readonly present: boolean; readonly options: string[]; readonly optgroup: string[] };
  readonly candidate: string;
};

const sideOf = (el: HTMLElement): string => {
  const fieldset = el.closest('fieldset');
  const legend = fieldset?.querySelector('legend')?.textContent ?? '?';
  return legend;
};

const frame = (label: string): Frame => {
  const banners = [...document.querySelectorAll('[data-testid="source-picker-explanation"]')];
  const text = banners[0]?.textContent ?? null;
  const emptyList = document.querySelector('[data-testid="source-picker-empty-list"]') !== null;
  const cls = text === null ? null
    : emptyList ? 'none-populated'
    : text.includes('already used') ? 'all-bound'
    : text.includes('contains values') ? 'none-populated'
    : text.includes('has not listed') ? 'none-listed'
    : '?';
  const side = (name: string) => {
    let present = false;
    let options: string[] = [];
    let optgroup: string[] = [];
    try {
      const select = operandColumn(name);
      present = true;
      options = [...select.options].map((o) => o.value);
      optgroup = optValues(select);
    } catch {
      present = false;
    }
    return { present, options, optgroup };
  };
  return {
    label,
    explainCount: banners.length,
    explainSlots: banners.map((b) => sideOf(b as HTMLElement)),
    explainClass: cls,
    left: side('First value'),
    right: side('Second value'),
    candidate: lastCandidate === undefined ? 'none' : JSON.stringify(lastCandidate.deriveSources ?? null),
  };
};

const DUMPS: Frame[] = [];
const snap = (label: string): Frame => {
  const f = frame(label);
  DUMPS.push(f);
  return f;
};
const boom = (message: string): never => {
  throw new Error(`${message}\nFRAMES:\n${DUMPS.map((f) => JSON.stringify(f)).join('\n')}`);
};

describe('REV4 adversarial probes (derive pickerEmptinessHost)', () => {
  it('attack a: kind-shortcut column sharing then dataset switch -> prune clobbers the other operand binding, false all-bound banner while a populated field stays listable', () => {
    lastCandidate = undefined;
    DUMPS.length = 0;
    renderEditor({
      selectedStage: sourceStage(stringCols),
      deriveSourceInput: {
        supported: true,
        stageId: 'source_projection',
        choices: [numericChoice('choice-a', 'derive-input_a'), numericChoice('choice-b', 'derive-input_b')],
      },
    });
    snap('f0 initial');
    setKind('Second value', 'column');
    snap('f1 right bound to choice-a (kind shortcut)');
    setKind('First value', 'column');
    const f2 = snap('f2 left shares derive-input_a');
    expect(f2.explainCount).toBe(0);
    fireEvent.change(operandColumn('Second value'), { target: { value: 'source:choice-b' } });
    const f3 = snap('f3 right switched to choice-b (kept-filter clobber)');

    expect(f3.explainCount).toBeLessThanOrEqual(1);

    const optgroupSize = Math.max(f3.left.optgroup.length, f3.right.optgroup.length);
    if (f3.explainClass === 'all-bound' && optgroupSize > 0) {
      boom(`FALSE BANNER: class=all-bound while optgroup lists ${optgroupSize} field(s); left.optgroup=${JSON.stringify(f3.left.optgroup)} right.optgroup=${JSON.stringify(f3.right.optgroup)} candidate=${f3.candidate}`);
    }

    if (f3.candidate !== 'none' && !f3.candidate.includes('choice-a')) {
      boom(`BINDING LOST: left operand still kind=column columnId=derive-input_a but candidate deriveSources=${f3.candidate}`);
    }
  });

  it('attack b: two choices share one materialized columnId, single active binding stays consistent (host must not double or vanish)', () => {
    lastCandidate = undefined;
    DUMPS.length = 0;
    renderEditor({
      selectedStage: sourceStage(stringCols),
      deriveSourceInput: {
        supported: true,
        stageId: 'source_projection',
        choices: [numericChoice('choice-a', 'shared-col'), numericChoice('choice-a2', 'shared-col'), numericChoice('choice-b', 'derive-input_b')],
      },
    });
    setKind('First value', 'column');
    setKind('Second value', 'column');
    snap('g1 both drafts shared-col, binding choice-a');
    fireEvent.change(operandColumn('Second value'), { target: { value: 'source:choice-a2' } });
    const g2 = snap('g2 right re-picks choice-a2 (same columnId)');
    expect(g2.explainCount).toBeLessThanOrEqual(1);

    const leftListsA = g2.left.optgroup.includes('source:choice-a');
    if (g2.explainClass === 'all-bound' && leftListsA) {
      boom(`FALSE BANNER (shared columnId): all-bound while left lists ${JSON.stringify(g2.left.optgroup)} candidate=${g2.candidate}`);
    }
    if (g2.candidate !== 'none' && !g2.candidate.includes('choice-a"') && !g2.candidate.includes('choice-a2')) {
      boom(`WIRE: ${g2.candidate}`);
    }
  });

  it('attack c: live stage change keeps ownVisible true via foreign numeric column id => zero-field picker loses its explanation', () => {
    lastCandidate = undefined;
    DUMPS.length = 0;
    const input: DeriveSourceInput = {
      supported: true,
      stageId: 'source_projection',
      choices: [numericChoice('choice-note', 'clash-col', 'string')],
    };
    const view = renderEditor({
      selectedStage: sourceStage(stringCols),
      deriveSourceInput: input,
    });
    setKind('First value', 'column');
    const h1 = snap('h1 sole string-typed binding, zero dataset fields');
    expect(h1.explainCount).toBe(1);
    expect(h1.explainClass).toBe('all-bound');

    const laterStage = sourceStage([
      ...stringCols,
      { id: 'clash-col', name: 'clash', label: 'Clash', type: 'integer' },
    ]);
    const construction = { version: 1, steps: [] };
    view.rerender(
      <ConstructionOperationEditor
        family="CALCULATE"
        construction={construction}
        capabilities={{
          snapshotToken: 'snapshot-1',
          draftVersion: 1,
          draftDigest: 'draft-digest-1',
          outputId: 'table-1',
          stageId: laterStage.id,
          sourceInput: { supported: false, stageId: laterStage.id, reasonCode: 'TEST_FIXTURE_NO_CHOICES', choices: [] },
          baseConstruction: construction,
          stages: [laterStage],
          selectedStage: laterStage,
          groupFieldChoices: [],
          groupRowValueChoices: [],
          workspaceInputs: [],
          deriveSourceInput: input,
        }}
        editingStep={undefined}
        selectedColumns={[]}
        disabled={false}
        onCandidateChange={(candidate) => { lastCandidate = candidate; }}
        onEditStep={vi.fn()}
      />,
    );
    const h2 = snap('h2 after stage gains numeric column id=clash-col');
    const leftFields = h2.left.optgroup.length;
    if (leftFields === 0 && h2.explainCount === 0) {
      boom(`FALSE AVAILABLE: zero dataset fields, no explanation; left.options=${JSON.stringify(h2.left.options)} candidate=${h2.candidate}`);
    }
  });

  it('attack e: released (literal) operand prunes its binding; no stale-binding frame may raise a false banner', () => {
    lastCandidate = undefined;
    DUMPS.length = 0;
    renderEditor({
      selectedStage: sourceStage([...stringCols, { id: 'n1', name: 'n1', label: 'N1', type: 'integer' }]),
      deriveSourceInput: {
        supported: true,
        stageId: 'source_projection',
        choices: [numericChoice('choice-note', 'derive-input_note', 'string')],
      },
    });
    setKind('Second value', 'column');
    fireEvent.change(operandColumn('Second value'), { target: { value: 'source:choice-note' } });
    setKind('Second value', 'literal');
    const m1 = snap('m1 right released to literal, left still column n1');
    if (m1.explainCount !== 0
      || (m1.candidate !== 'none' && m1.candidate !== 'null' && m1.candidate.includes('choice-note'))) {
      boom(`STALE BINDING: right kind=literal but explainCount=${m1.explainCount} class=${m1.explainClass} candidate=${m1.candidate}`);
    }
  });

  it('attack d: right-hosted banner migrates when left switches to column and back; exactly one element per frame', () => {
    lastCandidate = undefined;
    DUMPS.length = 0;
    renderEditor({
      selectedStage: sourceStage([...stringCols, { id: 'n1', name: 'n1', label: 'N1', type: 'integer' }]),
      deriveSourceInput: {
        supported: true,
        stageId: 'source_projection',
        choices: [numericChoice('choice-note', 'derive-input_note', 'string')],
      },
    });
    setKind('First value', 'literal');
    setKind('Second value', 'column');
    const k0 = snap('k1 right drafts n1, note listable, no banner');
    expect(k0.explainCount).toBe(0);
    fireEvent.change(operandColumn('Second value'), { target: { value: 'source:choice-note' } });
    const k1 = snap('k2 right bound to string note -> zero fields, right hosts');
    expect(k1.explainCount).toBe(1);
    expect(k1.explainSlots).toEqual(['Second value']);
    expect(k1.explainClass).toBe('all-bound');

    setKind('First value', 'column');
    const k2 = snap('k3 left switched to column n1');
    expect(k2.explainCount).toBe(1);
    if (k2.explainSlots.length !== 1) boom('two hosts');

    if (k2.left.optgroup.length === 0 && !k2.explainSlots.includes('First value') && !k2.explainSlots.includes('Second value')) {
      boom('banner lost in migration');
    }
    setKind('First value', 'literal');
    const k3 = snap('k4 left literal again');
    expect(k3.explainCount).toBe(1);
    expect(k3.explainSlots).toEqual(['Second value']);
  });
});

}
