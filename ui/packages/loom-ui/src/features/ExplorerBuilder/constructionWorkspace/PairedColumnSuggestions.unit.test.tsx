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
import { catalogItemLabel } from '../catalogItems';
import {
  MAX_VISIBLE_PAIRED_COLUMN_SUGGESTIONS,
  PairedColumnSuggestions,
  type PairedColumnSuggestion,
} from './PairedColumnSuggestions';

const mockClient = vi.hoisted(() => ({
  browseSemanticInventory: vi.fn(),
  searchConstructionChoices: vi.fn(),
}));

vi.mock('../../../react', async (importOriginal) => ({
  ...await importOriginal<typeof import('../../../react')>(),
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
    const pairing = item('days_to_collection', 'days_to_collection');
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
    expect(screen.getByText('Suggested coded columns')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add Days to collection as a column' })).toBeInTheDocument();
    expect(catalogItemLabel({ kind: 'SEMANTIC', item: pairing })).toBe('Days to collection');
    expect(screen.getAllByRole('button', { name: /as a column$/ })).toHaveLength(
      MAX_VISIBLE_PAIRED_COLUMN_SUGGESTIONS,
    );
    expect(screen.queryByTestId('paired-column-suggestion-no-code')).not.toBeInTheDocument();
    expect(screen.queryByTestId('paired-column-suggestion-needs-mapping')).not.toBeInTheDocument();
    expect(screen.queryByTestId('paired-column-suggestion-unsupported-route')).not.toBeInTheDocument();
    expect(screen.getAllByTestId(/^paired-column-suggestion-/)).toHaveLength(
      MAX_VISIBLE_PAIRED_COLUMN_SUGGESTIONS,
    );
    expect(mockClient.browseSemanticInventory).toHaveBeenCalledWith(
      expect.objectContaining({
        project: 'project-a',
        explorerId: 'explorer-a',
        snapshotToken: 'snapshot-a',
        rowRoot: 'Specimen',
        limit: 50,
      }),
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

  it('retires in-flight choices without aborting transport or showing stale output', async () => {
    let resolveOldChoices!: (value: ConstructionChoiceSearchResponse) => void;
    const oldChoices = new Promise<ConstructionChoiceSearchResponse>((resolve) => {
      resolveOldChoices = resolve;
    });
    const oldItem = item('old-output-only', 'Old output concept');
    const newItem = item('new-output-only', 'New output concept');
    mockClient.browseSemanticInventory
      .mockResolvedValueOnce(inventory([oldItem], 'context-old'))
      .mockResolvedValueOnce(inventory([newItem], 'context-new'));
    mockClient.searchConstructionChoices.mockImplementation(({ outputId }: { outputId: string }) =>
      outputId === 'old-output' ? oldChoices : Promise.resolve(choicesFor(newItem, outputId)),
    );

    const { rerender } = renderSuggestions('old-output');
    await waitFor(() => expect(mockClient.searchConstructionChoices).toHaveBeenCalledTimes(1));
    expect(mockClient.browseSemanticInventory.mock.calls[0]).toHaveLength(1);
    expect(mockClient.searchConstructionChoices.mock.calls[0]).toHaveLength(1);

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

    await act(async () => {
      resolveOldChoices(choicesFor(oldItem, 'old-output'));
      await oldChoices;
    });
    expect(screen.queryByTestId('paired-column-suggestion-old-output-only')).not.toBeInTheDocument();
    expect(screen.getByTestId('paired-column-suggestion-new-output-only')).toBeInTheDocument();
    expect(mockClient.browseSemanticInventory.mock.calls[1]).toHaveLength(1);
    expect(mockClient.searchConstructionChoices).toHaveBeenCalledTimes(2);
    expect(mockClient.searchConstructionChoices.mock.calls.every((call) => call.length === 1)).toBe(true);
  });

  it('does not repeat an authored paired concept by its displayed label', async () => {
    const authored = item('authored-pair', 'days_to_collection');
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

  it('shows a construction-choice failure instead of silently dropping that candidate', async () => {
    const candidate = item('needs-route', 'Needs route');
    mockClient.browseSemanticInventory.mockResolvedValue(inventory([candidate]));
    mockClient.searchConstructionChoices.mockRejectedValue(new Error('Choice service unavailable'));

    renderSuggestions();

    expect(await screen.findByText('Choice service unavailable')).toHaveAttribute('role', 'status');
    expect(screen.queryByText(/No coded pairings with a supported route/)).not.toBeInTheDocument();
  });

  it('surfaces choices returned for a different output or snapshot', async () => {
    const candidate = item('wrong-scope', 'Wrong scope');
    mockClient.browseSemanticInventory.mockResolvedValue(inventory([candidate]));
    mockClient.searchConstructionChoices.mockResolvedValue(choicesFor(candidate, 'another-output'));

    renderSuggestions('specimens');

    expect(await screen.findByText('Loom returned paired construction choices for another table or catalog snapshot.'))
      .toHaveAttribute('role', 'status');
  });

  it('ignores a late inventory result after its query is retired and does not fan out', async () => {
    let resolveInventory!: (value: SemanticInventoryBrowseResponse) => void;
    const deferredInventory = new Promise<SemanticInventoryBrowseResponse>((resolve) => { resolveInventory = resolve; });
    mockClient.browseSemanticInventory.mockReturnValueOnce(deferredInventory);
    const candidate = item('stale-tab-candidate', 'Stale tab candidate');
    const onSelectSuggestion = vi.fn();
    const TabbedColumns = () => {
      const [tab, setTab] = React.useState<'coded' | 'fields'>('coded');
      return (
        <>
          <button type="button" onClick={() => setTab('fields')}>Fields and related data</button>
          {tab === 'coded' ? <PairedColumnSuggestions
            project="project-a" explorerId="explorer-a" snapshotToken="snapshot-a" outputId="specimens"
            rowRoot="Specimen" columns={[]} onSelectSuggestion={onSelectSuggestion} onBrowseAll={vi.fn()}
          /> : <p>Related field chooser</p>}
        </>
      );
    };
    render(<TabbedColumns />);
    await waitFor(() => expect(mockClient.browseSemanticInventory).toHaveBeenCalledTimes(1));
    expect(mockClient.browseSemanticInventory.mock.calls[0]).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: 'Fields and related data' }));
    expect(screen.getByText('Related field chooser')).toBeInTheDocument();

    await act(async () => {
      resolveInventory(inventory([candidate], 'old-tab-context'));
      await deferredInventory;
    });
    expect(mockClient.searchConstructionChoices).not.toHaveBeenCalled();
    expect(screen.queryByTestId('paired-column-suggestion-stale-tab-candidate')).not.toBeInTheDocument();
  });
});
