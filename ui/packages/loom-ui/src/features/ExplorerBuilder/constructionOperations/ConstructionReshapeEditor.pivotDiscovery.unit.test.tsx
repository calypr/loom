// @vitest-environment jsdom
import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  ConstructionReshapeEditor,
  type CandidateIntent,
  type ConstructionReshapeEditorProps,
  type ConstructionReshapeStep,
} from './ConstructionReshapeEditor';

const sourceColumns: ConstructionReshapeEditorProps['capabilities']['selectedStage']['columns'] = [
  { id: 'site-id', name: 'site', label: 'Site', type: 'string', cardinality: 'required_one' },
  { id: 'age-id', name: 'age', label: 'Age', type: 'integer', cardinality: 'optional_one' },
  { id: 'flag-id', name: 'flag', label: 'Flag', type: 'boolean', cardinality: 'optional_one' },
];

const pivotStage: ConstructionReshapeEditorProps['capabilities']['selectedStage'] = {
  id: 'source_projection',
  inputStageId: '',
  operation: 'SOURCE_PROJECTION',
  columns: sourceColumns,
  capabilities: [
    { kind: 'PIVOT', supported: true },
  ],
};

const capabilitiesFor = (
  stage = pivotStage,
): ConstructionReshapeEditorProps['capabilities'] => ({
  snapshotToken: 'snapshot-1',
  draftVersion: 1,
  draftDigest: 'draft-digest-1',
  outputId: 'table-1',
  stageId: stage.id,
  sourceInput: { supported: false, stageId: stage.id, reasonCode: 'TEST_FIXTURE_NO_CHOICES', choices: [] },
  baseConstruction: { version: 1, steps: [] },
  stages: [stage],
  selectedStage: stage,
  groupFieldChoices: [],
  groupRowValueChoices: [],
  workspaceInputs: [],
});

const renderEditor = (args: {
  readonly construction?: ConstructionReshapeEditorProps['construction'];
  readonly capabilities?: ConstructionReshapeEditorProps['capabilities'];
  readonly editingStep?: ConstructionReshapeStep;
  readonly onCandidateChange?: ConstructionReshapeEditorProps['onCandidateChange'];
  readonly onDiscoverCategories?: ConstructionReshapeEditorProps['onDiscoverCategories'];
  readonly populationStatuses?: Record<string, 'POPULATED' | 'EMPTY' | 'UNKNOWN'>;
  readonly populationGate?: Promise<void>;
}) => {
  const onCandidateChange = (args.onCandidateChange
    ?? vi.fn()) as ReturnType<typeof vi.fn<NonNullable<ConstructionReshapeEditorProps['onCandidateChange']>>>;
  const capabilities = args.capabilities ?? capabilitiesFor();
  const getConstructionColumnPopulation = vi.fn(async (request: { readonly columnIds: string[] }) => {
    await args.populationGate;
    return {
      snapshotToken: capabilities.snapshotToken,
      draftVersion: capabilities.draftVersion,
      draftDigest: capabilities.draftDigest,
      outputId: capabilities.outputId,
      stageId: capabilities.selectedStage.id,
      columns: request.columnIds.map((columnId) => ({
        columnId,
        status: args.populationStatuses?.[columnId] ?? 'POPULATED' as const,
      })),
    };
  });
  const view = render(
    <ConstructionReshapeEditor
      construction={args.construction ?? { version: 1, steps: [] }}
      capabilities={capabilities}
      editingStep={args.editingStep}
      initialKind="pivot"
      disabled={false}
      onCandidateChange={onCandidateChange}
      onEditStep={vi.fn()}
      onDiscoverCategories={args.onDiscoverCategories ?? vi.fn()}
      codedPivotContext={{
        client: { browseFrameSourceOptions: vi.fn(), browseSemanticInventory: vi.fn(), getConstructionColumnPopulation },
        project: 'project-1', explorerId: 'explorer-1', snapshotToken: capabilities.snapshotToken,
        outputId: capabilities.outputId, rowRoot: 'Patient',
      }}
    />,
  );
  return { onCandidateChange, getConstructionColumnPopulation, view };
};

const pivotFrom = (intent: CandidateIntent | undefined) => {
  if (!intent) throw new Error('Expected a ready pivot candidate');
  const step = intent.candidateConstruction.steps.at(-1);
  if (step?.operation.kind !== 'PIVOT') throw new Error('Expected a PIVOT step');
  return step.operation.pivot;
};

const isChecked = (label: string): boolean => (screen.getByLabelText(label) as HTMLInputElement).checked;

afterEach(cleanup);

describe('ConstructionReshapeEditor pivot discovery opt-in', () => {
  it('checks population in batches of at most 16 columns', async () => {
    const columns = Array.from({ length: 33 }, (_, index) => ({
      id: `column-${index}`, name: `column_${index}`, label: `Column ${index}`,
      type: 'string', cardinality: 'optional_one' as const,
    }));
    const stage = { ...pivotStage, columns };
    const { getConstructionColumnPopulation } = renderEditor({ capabilities: capabilitiesFor(stage) });
    await waitFor(() => expect(getConstructionColumnPopulation).toHaveBeenCalledTimes(3));
    const batches = getConstructionColumnPopulation.mock.calls.map(([request]) => request.columnIds);
    expect(batches.every((batch) => batch.length <= 16)).toBe(true);
    expect(batches.flat()).toEqual(columns.map((column) => column.id));
  });

  it('cancels a pending category lookup when a selected column is confirmed empty', async () => {
    let release!: () => void;
    const populationGate = new Promise<void>((resolve) => { release = resolve; });
    const { onCandidateChange } = renderEditor({ populationGate, populationStatuses: { 'age-id': 'EMPTY' } });
    fireEvent.click(screen.getByLabelText('Discover categories when proposing'));
    fireEvent.click(screen.getByLabelText('Pivot group Site'));
    fireEvent.change(screen.getByLabelText('Pivot category field'), { target: { value: 'age-id' } });
    fireEvent.change(screen.getByLabelText('Pivot values field'), { target: { value: 'flag-id' } });
    expect(onCandidateChange.mock.lastCall?.[0]).toBeDefined();
    release();
    await waitFor(() => expect(screen.getByRole('option', { name: /Age — No populated values/ })).toBeDisabled());
    expect(onCandidateChange.mock.lastCall?.[0]).toBeUndefined();
  });
  it('emits empty categories with discovery.maxCategories when enabled', () => {
    const { onCandidateChange } = renderEditor({ onCandidateChange: vi.fn() });

    const toggle = screen.getByLabelText('Discover categories when proposing');
    expect(isChecked('Discover categories when proposing')).toBe(false);
    fireEvent.click(toggle);
    expect(isChecked('Discover categories when proposing')).toBe(true);
    expect(screen.getByText(/They freeze at that point, so turn discovery on and propose the pivot again after the data changes/)).toBeInTheDocument();
    expect(screen.getByText(/Manual category selection is unavailable while this is on/)).toBeInTheDocument();
    expect(screen.queryByText('Change selected categories')).not.toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('Pivot group Site'));
    fireEvent.change(screen.getByLabelText('Pivot category field'), { target: { value: 'age-id' } });
    fireEvent.change(screen.getByLabelText('Pivot values field'), { target: { value: 'flag-id' } });

    const pivot = pivotFrom(onCandidateChange.mock.lastCall?.[0]);
    expect(pivot.categories).toEqual([]);
    expect(pivot.discovery).toEqual({ maxCategories: 256 });
  });

  it('preserves explicit categories when discovery is not enabled', () => {
    const step: ConstructionReshapeStep = {
      id: 'explicit-pivot',
      inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: {
        kind: 'PIVOT',
        pivot: {
          constructionId: 'explicit-pivot',
          groupKeyIds: ['site-id'],
          categoryColumnId: 'age-id',
          valueColumnId: 'flag-id',
          categories: [{ key: { kind: 'STRING', string: 'baseline' }, outputColumnId: 'saved-output' }],
          duplicatePolicy: 'MIN',
          missingCellPolicy: 'NULL',
          unlistedCategoryPolicy: 'ERROR',
        },
      },
      outputs: [
        { id: 'site-id', name: 'site', label: 'Site', type: 'string' },
        { id: 'saved-output', name: 'baseline', label: 'Baseline', type: 'boolean' },
      ],
    };
    const { onCandidateChange } = renderEditor({
      construction: { version: 1, steps: [step] },
      editingStep: step,
      onCandidateChange: vi.fn(),
    });

    expect(isChecked('Discover categories when proposing')).toBe(false);
    expect(screen.getByText('Change selected categories')).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Pivot missing cell policy'), { target: { value: 'ERROR' } });
    const pivot = pivotFrom(onCandidateChange.mock.lastCall?.[0]);
    expect(pivot.discovery).toBeUndefined();
    expect(pivot.categories).toEqual([{ key: { kind: 'STRING', string: 'baseline' }, outputColumnId: 'saved-output' }]);
  });

  it('reopens a saved discovery pivot with the opt-in enabled', () => {
    const step: ConstructionReshapeStep = {
      id: 'discovery-pivot',
      inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: {
        kind: 'PIVOT',
        pivot: {
          constructionId: 'discovery-pivot',
          groupKeyIds: ['site-id'],
          categoryColumnId: 'age-id',
          valueColumnId: 'flag-id',
          categories: [],
          discovery: { maxCategories: 128 },
          duplicatePolicy: 'MIN',
          missingCellPolicy: 'NULL',
          unlistedCategoryPolicy: 'ERROR',
        },
      },
      outputs: [{ id: 'site-id', name: 'site', label: 'Site', type: 'string' }],
    };
    const { onCandidateChange } = renderEditor({
      construction: { version: 1, steps: [step] },
      editingStep: step,
      onCandidateChange: vi.fn(),
    });

    expect(isChecked('Discover categories when proposing')).toBe(true);
    expect(screen.getByText(/up to 128/)).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Pivot missing cell policy'), { target: { value: 'ERROR' } });
    const pivot = pivotFrom(onCandidateChange.mock.lastCall?.[0]);
    expect(pivot.categories).toEqual([]);
    expect(pivot.discovery).toEqual({ maxCategories: 128 });
  });

  it('cancels a scheduled category lookup when discovery becomes propose-time', async () => {
    vi.useFakeTimers();
    try {
      const onDiscoverCategories = vi.fn<NonNullable<ConstructionReshapeEditorProps['onDiscoverCategories']>>();
      const { onCandidateChange } = renderEditor({ onCandidateChange: vi.fn(), onDiscoverCategories });

      await act(async () => { await vi.advanceTimersByTimeAsync(0); });
      fireEvent.click(screen.getByLabelText('Pivot group Site'));
      fireEvent.change(screen.getByLabelText('Pivot category field'), { target: { value: 'age-id' } });
      fireEvent.change(screen.getByLabelText('Pivot values field'), { target: { value: 'flag-id' } });
      expect(screen.getByTestId('construction-reshape-pivot-discovery-status')).toHaveAttribute('aria-busy', 'true');

      fireEvent.click(screen.getByLabelText('Discover categories when proposing'));
      await act(async () => { await vi.advanceTimersByTimeAsync(250); });

      expect(onDiscoverCategories).not.toHaveBeenCalled();
      expect(screen.queryByTestId('construction-reshape-pivot-discovery-status')).not.toBeInTheDocument();
      expect(onCandidateChange.mock.lastCall?.[0]).toBeDefined();
      expect(pivotFrom(onCandidateChange.mock.lastCall?.[0]).discovery).toEqual({ maxCategories: 256 });
    } finally {
      vi.useRealTimers();
    }
  });
});
