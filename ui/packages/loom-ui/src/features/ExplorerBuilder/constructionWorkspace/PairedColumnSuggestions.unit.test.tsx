// @vitest-environment jsdom
import React from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type {
  ConstructionChoice,
  ConstructionChoiceSearchResponse,
  SemanticInventoryBrowseResponse,
  SemanticInventoryItem,
} from '../../../types';
import {
  MAX_VISIBLE_PAIRED_COLUMN_SUGGESTIONS,
  PairedColumnSuggestions,
  type PairedColumnSuggestion,
} from './PairedColumnSuggestions';

const mockClient = vi.hoisted(() => ({
  browseSemanticInventory: vi.fn(),
  searchConstructionChoices: vi.fn(),
}));

vi.mock('../../../react', () => ({
  useLoomClient: () => mockClient,
}));

const item = (
  conceptId: string,
  display: string,
  overrides: Partial<SemanticInventoryItem> = {},
): SemanticInventoryItem => ({
  conceptId,
  bindingId: `binding-${conceptId}`,
  resourceType: 'Specimen',
  sourcePath: 'extension.valueCodeableConcept',
  system: 'https://example.org/codes',
  code: conceptId,
  codingVersion: '',
  display,
  valueSelector: 'value',
  valueType: 'decimal',
  owningScope: 'extension',
  occurrences: 123,
  examplesTruncated: false,
  observedUnitsTruncated: false,
  readiness: { status: 'READY', code: 'READY', message: 'Supported semantic pairing.' },
  ...overrides,
});

const choiceFor = (value: SemanticInventoryItem): ConstructionChoice => ({
  choiceId: `choice-${value.conceptId}`,
  source: {
    kind: 'SEMANTIC',
    conceptId: value.conceptId,
    bindingId: value.bindingId,
    candidateId: `candidate-${value.conceptId}`,
    nodeId: 'specimen-node',
    resourceType: value.resourceType,
    sourcePath: value.sourcePath,
    fieldPath: `root.${value.sourcePath}`,
    valueSelector: value.valueSelector,
    logicalType: value.valueType,
    ruleVersion: '1',
    schemaVersion: 1,
    cardinality: 'optional_one',
  },
  route: [],
  presentation: { summary: `Pair ${value.code} with its value`, facts: [] },
  options: [{
    form: 'VALUE',
    shape: 'SCALAR',
    decision: 'REQUIRES_DECISION',
    preservation: 'PRESERVING',
    rowEffect: 'PRESERVES_ROW_GRAIN',
    support: 'SUPPORTED',
    reason: 'The paired value preserves the current row grain.',
  }],
});

const inventory = (
  entries: ReadonlyArray<SemanticInventoryItem>,
  contextToken = 'context-a',
): SemanticInventoryBrowseResponse => ({
  contextToken,
  buildId: 'build-a',
  state: 'complete',
  sourceAvailability: 'verified',
  entries: [...entries],
});

const choicesFor = (
  value: SemanticInventoryItem,
  outputId: string,
  snapshotToken = 'snapshot-a',
): ConstructionChoiceSearchResponse => ({
  snapshotToken,
  outputId,
  complete: true,
  truncated: false,
  choices: [choiceFor(value)],
});

const renderSuggestions = (
  outputId = 'specimens',
  onSelectSuggestion = vi.fn<(suggestion: PairedColumnSuggestion) => void>(),
) => render(
  <PairedColumnSuggestions
    project="project-a"
    explorerId="explorer-a"
    snapshotToken="snapshot-a"
    outputId={outputId}
    rowRoot="Specimen"
    columns={[]}
    onSelectSuggestion={onSelectSuggestion}
    onBrowseAll={vi.fn()}
  />,
);

describe('PairedColumnSuggestions', () => {
  beforeEach(() => {
    mockClient.browseSemanticInventory.mockReset();
    mockClient.searchConstructionChoices.mockReset();
  });

  it('shows only coded, ready concepts with a current-output route and supported form', async () => {
    const pairing = item('days_to_collection', 'Days to collection');
    const entries = [
      pairing,
      item('no-code', 'No code', { code: '' }),
      item('needs-mapping', 'Needs mapping', {
        readiness: { status: 'NEEDS_MAPPING', code: 'NEEDS_MAPPING', message: 'Map this first.' },
      }),
      item('already-authored', 'Already authored'),
      item('unsupported-route', 'Unsupported route'),
      item('paired-two', 'Second pairing'),
      item('paired-three', 'Third pairing'),
      item('paired-four', 'Fourth pairing'),
      item('paired-five', 'Fifth pairing'),
    ];
    mockClient.browseSemanticInventory.mockResolvedValue(inventory(entries));
    const unsupported = entries.find((candidate) => candidate.conceptId === 'unsupported-route')!;
    mockClient.searchConstructionChoices.mockImplementation(async ({ source, outputId }: {
      source: { conceptId: string };
      outputId: string;
    }) => {
      if (source.conceptId === unsupported.conceptId) {
        return { ...choicesFor(unsupported, outputId), choices: [] };
      }
      const selected = entries.find((candidate) => candidate.conceptId === source.conceptId)!;
      return choicesFor(selected, outputId);
    });

    const onSelectSuggestion = vi.fn<(suggestion: PairedColumnSuggestion) => void>();
    renderSuggestions('specimens', onSelectSuggestion);
    const suggestion = await screen.findByTestId('paired-column-suggestion-days_to_collection');
    expect(screen.getByTestId('paired-column-suggestions')).toBeInTheDocument();
    expect(screen.getByTestId('paired-column-suggestions-browse-all')).toBeInTheDocument();
    expect(screen.getByText('Add paired value columns')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add Days to collection as a column' })).toBeInTheDocument();
    expect(screen.getAllByText('Add column →').length).toBeGreaterThan(0);
    expect(screen.queryByTestId('paired-column-suggestion-no-code')).not.toBeInTheDocument();
    expect(screen.queryByTestId('paired-column-suggestion-needs-mapping')).not.toBeInTheDocument();
    expect(screen.queryByTestId('paired-column-suggestion-unsupported-route')).not.toBeInTheDocument();
    expect(screen.getAllByTestId(/^paired-column-suggestion-/)).toHaveLength(
      MAX_VISIBLE_PAIRED_COLUMN_SUGGESTIONS,
    );
    expect(mockClient.searchConstructionChoices).toHaveBeenCalledWith(
      expect.objectContaining({
        outputId: 'specimens',
        snapshotToken: 'snapshot-a',
        source: expect.objectContaining({
          kind: 'SEMANTIC',
          conceptId: 'days_to_collection',
          bindingId: 'binding-days_to_collection',
        }),
      }),
      expect.any(AbortSignal),
    );

    fireEvent.click(suggestion);
    expect(onSelectSuggestion).toHaveBeenCalledWith(expect.objectContaining({
      item: pairing,
      outputId: 'specimens',
      choices: expect.objectContaining({ outputId: 'specimens', choices: [choiceFor(pairing)] }),
    }));
  });

  it('ignores an inventory response after the output changes', async () => {
    let resolveOldInventory!: (value: SemanticInventoryBrowseResponse) => void;
    const oldInventory = new Promise<SemanticInventoryBrowseResponse>((resolve) => {
      resolveOldInventory = resolve;
    });
    const oldItem = item('old-output-only', 'Old output concept');
    const newItem = item('new-output-only', 'New output concept');
    mockClient.browseSemanticInventory
      .mockReturnValueOnce(oldInventory)
      .mockResolvedValueOnce(inventory([newItem], 'context-new'));
    mockClient.searchConstructionChoices.mockImplementation(async ({ source, outputId }: {
      source: { conceptId: string };
      outputId: string;
    }) => choicesFor(source.conceptId === oldItem.conceptId ? oldItem : newItem, outputId));

    const { rerender } = renderSuggestions('old-output');
    await waitFor(() => expect(mockClient.browseSemanticInventory).toHaveBeenCalledTimes(1));
    rerender(
      <PairedColumnSuggestions
        project="project-a"
        explorerId="explorer-a"
        snapshotToken="snapshot-a"
        outputId="new-output"
        rowRoot="Specimen"
        columns={[]}
        onSelectSuggestion={vi.fn()}
        onBrowseAll={vi.fn()}
      />,
    );
    expect(await screen.findByTestId('paired-column-suggestion-new-output-only')).toBeInTheDocument();

    await act(async () => resolveOldInventory(inventory([oldItem], 'context-old')));
    expect(screen.queryByTestId('paired-column-suggestion-old-output-only')).not.toBeInTheDocument();
    expect(screen.getByTestId('paired-column-suggestion-new-output-only')).toBeInTheDocument();
  });

  it('does not repeat an authored paired concept by its displayed label', async () => {
    const authored = item('authored-pair', 'Days to collection');
    mockClient.browseSemanticInventory.mockResolvedValue(inventory([authored]));
    mockClient.searchConstructionChoices.mockResolvedValue(choicesFor(authored, 'specimens'));
    render(
      <PairedColumnSuggestions
        project="project-a"
        explorerId="explorer-a"
        snapshotToken="snapshot-a"
        outputId="specimens"
        rowRoot="Specimen"
        columns={[{ id: 'existing-column', label: 'days to collection' }]}
        onSelectSuggestion={vi.fn()}
        onBrowseAll={vi.fn()}
      />,
    );
    expect(await screen.findByText(/No coded pairings with a supported route/)).toBeInTheDocument();
    expect(mockClient.searchConstructionChoices).not.toHaveBeenCalled();
  });
});
