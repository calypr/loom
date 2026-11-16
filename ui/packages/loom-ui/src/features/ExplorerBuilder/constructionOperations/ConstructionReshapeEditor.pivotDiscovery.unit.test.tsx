// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
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
}) => {
  const onCandidateChange = (args.onCandidateChange
    ?? vi.fn()) as ReturnType<typeof vi.fn<NonNullable<ConstructionReshapeEditorProps['onCandidateChange']>>>;
  const view = render(
    <ConstructionReshapeEditor
      construction={args.construction ?? { version: 1, steps: [] }}
      capabilities={args.capabilities ?? capabilitiesFor()}
      editingStep={args.editingStep}
      initialKind="pivot"
      disabled={false}
      onCandidateChange={onCandidateChange}
      onEditStep={vi.fn()}
      onDiscoverCategories={vi.fn()}
    />,
  );
  return { onCandidateChange, view };
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
});
