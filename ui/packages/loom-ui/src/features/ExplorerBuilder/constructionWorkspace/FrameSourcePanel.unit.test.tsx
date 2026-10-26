// @vitest-environment jsdom
import React from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { FrameDefinition, FrameSourceOption } from '../../../types';
import { FrameSourcePanel } from './FrameSourcePanel';

const mockClient = vi.hoisted(() => ({ browseFrameSourceOptions: vi.fn(), browseSemanticInventory: vi.fn() }));
vi.mock('../../../react', async (importOriginal) => ({
  ...await importOriginal<typeof import('../../../react')>(),
  useLoomClient: () => mockClient,
}));

const source = (choiceId: string, title: string): FrameSourceOption => ({
  choiceId,
  title,
  description: '',
  resourceType: 'Observation',
  sourcePath: 'valueQuantity.code',
  bindingId: `binding-${choiceId}`,
  owningScope: 'Observation',
  keyPath: 'valueQuantity.code',
  valuePath: 'valueQuantity.value',
  logicalType: 'code',
  exampleConcept: 'code',
  observedOccurrences: 10,
  route: [],
  forms: [{ form: 'VALUE', zeroPolicy: 'NULL', manyPolicy: 'INVALID_MULTIPLE_VALUES', decision: 'supported' }],
  defaultForm: 'VALUE',
});

const page = (outputId: string, sources: ReadonlyArray<FrameSourceOption>, nextCursor?: string) => ({
  snapshotToken: 'snapshot-a', outputId, complete: true, truncated: false, nextCursor, sources,
});

const renderPanel = (outputId = 'output-a') => render(
  <FrameSourcePanel
    project="project-a"
    explorerId="explorer-a"
    snapshotToken="snapshot-a"
    outputId={outputId}
    rowRoot="Patient"
    frames={[]}
    columns={[]}
    disabled={false}
    onSet={vi.fn(async () => true)}
    onReplace={vi.fn(async () => true)}
    onRemove={vi.fn(async () => true)}
    onAddSelected={vi.fn(async () => undefined)}
    onRemoveColumn={vi.fn(async () => true)}
  />,
);

describe('FrameSourcePanel query ownership', () => {
  beforeEach(() => {
    mockClient.browseFrameSourceOptions.mockReset();
    mockClient.browseSemanticInventory.mockReset();
  });

  it('loads the first page on opening and keeps paginated choices together', async () => {
    const first = source('source-1', 'First source');
    const second = source('source-2', 'Second source');
    mockClient.browseFrameSourceOptions
      .mockResolvedValueOnce(page('output-a', [first], 'cursor-2'))
      .mockResolvedValueOnce(page('output-a', [second]));
    renderPanel();

    expect(screen.getByRole('heading', { name: 'Coded sources' })).toBeInTheDocument();
    expect(screen.getByText('Browse coded-value sources Loom can add to this table.')).toBeInTheDocument();
    expect(mockClient.browseFrameSourceOptions).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Browse sources' }));
    expect(await screen.findByTestId('frame-source-choice-source-1')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'More sources and paths' }));
    expect(await screen.findByTestId('frame-source-choice-source-2')).toBeInTheDocument();
    expect(screen.getByTestId('frame-source-choice-source-1')).toBeInTheDocument();
    expect(mockClient.browseFrameSourceOptions).toHaveBeenNthCalledWith(1,
      expect.objectContaining({ project: 'project-a', explorerId: 'explorer-a', outputId: 'output-a', snapshotToken: 'snapshot-a' }),
      expect.any(AbortSignal));
    expect(mockClient.browseFrameSourceOptions).toHaveBeenNthCalledWith(2,
      expect.objectContaining({ outputId: 'output-a', cursor: 'cursor-2' }),
      expect.any(AbortSignal));
  });

  it('aborts an old scope and never renders its late result after the output changes', async () => {
    let resolveOld!: (value: unknown) => void;
    const oldResponse = new Promise((resolve) => { resolveOld = resolve; });
    const oldSource = source('old-source', 'Old output source');
    const newSource = source('new-source', 'New output source');
    mockClient.browseFrameSourceOptions
      .mockReturnValueOnce(oldResponse)
      .mockResolvedValueOnce(page('output-b', [newSource]));
    const { rerender } = renderPanel('output-a');
    fireEvent.click(screen.getByRole('button', { name: 'Browse sources' }));
    await waitFor(() => expect(mockClient.browseFrameSourceOptions).toHaveBeenCalledTimes(1));
    const oldSignal = mockClient.browseFrameSourceOptions.mock.calls[0]?.[1] as AbortSignal;

    rerender(
      <FrameSourcePanel project="project-a" explorerId="explorer-a" snapshotToken="snapshot-a" outputId="output-b"
        rowRoot="Patient" frames={[]} columns={[]} disabled={false} onSet={vi.fn(async () => true)}
        onReplace={vi.fn(async () => true)} onRemove={vi.fn(async () => true)} onAddSelected={vi.fn(async () => undefined)}
        onRemoveColumn={vi.fn(async () => true)} />,
    );
    await screen.findByTestId('frame-source-choice-new-source');
    await waitFor(() => expect(oldSignal.aborted).toBe(true));
    await act(async () => {
      resolveOld(page('output-a', [oldSource]));
      await oldResponse;
    });
    expect(screen.queryByTestId('frame-source-choice-old-source')).not.toBeInTheDocument();
    expect(screen.getByTestId('frame-source-choice-new-source')).toBeInTheDocument();
    expect(mockClient.browseFrameSourceOptions.mock.calls[1]?.[0]).toEqual(expect.objectContaining({ outputId: 'output-b' }));
  });

  it('accumulates a third page once and hides prior pages on a new output scope', async () => {
    mockClient.browseFrameSourceOptions
      .mockResolvedValueOnce(page('output-a', [source('page-1', 'Page one')], 'cursor-2'))
      .mockResolvedValueOnce(page('output-a', [source('page-2', 'Page two')], 'cursor-3'))
      .mockResolvedValueOnce(page('output-a', [source('page-3', 'Page three')]))
      .mockResolvedValueOnce(page('output-b', [source('new-output', 'New output source')]));
    const { rerender } = renderPanel('output-a');
    fireEvent.click(screen.getByRole('button', { name: 'Browse sources' }));
    await screen.findByTestId('frame-source-choice-page-1');
    fireEvent.click(screen.getByRole('button', { name: 'More sources and paths' }));
    await screen.findByTestId('frame-source-choice-page-2');
    fireEvent.click(screen.getByRole('button', { name: 'More sources and paths' }));
    await screen.findByTestId('frame-source-choice-page-3');
    expect(screen.getByTestId('frame-source-choice-page-1')).toBeInTheDocument();
    expect(screen.getByTestId('frame-source-choice-page-2')).toBeInTheDocument();
    expect(screen.getAllByTestId(/^frame-source-choice-page-/)).toHaveLength(3);

    rerender(
      <FrameSourcePanel project="project-a" explorerId="explorer-a" snapshotToken="snapshot-a" outputId="output-b"
        rowRoot="Patient" frames={[]} columns={[]} disabled={false} onSet={vi.fn(async () => true)}
        onReplace={vi.fn(async () => true)} onRemove={vi.fn(async () => true)} onAddSelected={vi.fn(async () => undefined)}
        onRemoveColumn={vi.fn(async () => true)} />,
    );
    await screen.findByTestId('frame-source-choice-new-output');
    expect(screen.queryByTestId('frame-source-choice-page-1')).not.toBeInTheDocument();
    expect(screen.queryByTestId('frame-source-choice-page-2')).not.toBeInTheDocument();
    expect(screen.queryByTestId('frame-source-choice-page-3')).not.toBeInTheDocument();
    expect(mockClient.browseFrameSourceOptions).toHaveBeenLastCalledWith(
      expect.objectContaining({ outputId: 'output-b', cursor: undefined }), expect.any(AbortSignal));
  });

  it('opens values for a newly saved frame from the event-owned source identity', async () => {
    const selectedSource = source('new-frame-source', 'New frame source');
    mockClient.browseFrameSourceOptions.mockResolvedValueOnce(page('output-a', [selectedSource]));
    mockClient.browseSemanticInventory.mockResolvedValueOnce({
      snapshotToken: 'snapshot-a', outputId: 'output-a', frameId: 'frame-new',
      frameSource: { id: 'frame-new' }, state: 'complete', entries: [],
    });
    const frame = (choice: FrameSourceOption): FrameDefinition => ({
      id: 'frame-new', title: choice.title, description: choice.description,
      source: {
        bindingId: choice.bindingId, resourceType: choice.resourceType, sourcePath: choice.sourcePath,
        ...(choice.sourceCanonical ? { sourceCanonical: choice.sourceCanonical } : {}),
        ...(choice.sourceProfile ? { sourceProfile: choice.sourceProfile } : {}),
        owningScope: choice.owningScope, keyPath: choice.keyPath, valuePath: choice.valuePath,
        logicalType: choice.logicalType, ruleVersion: '1', schemaVersion: 1,
      },
      route: choice.route, form: 'VALUE', zeroPolicy: 'NULL', manyPolicy: 'INVALID_MULTIPLE_VALUES',
    });
    const DynamicFramePanel = () => {
      const [frames, setFrames] = React.useState<ReadonlyArray<FrameDefinition>>([]);
      return <FrameSourcePanel project="project-a" explorerId="explorer-a" snapshotToken="snapshot-a" outputId="output-a"
        rowRoot="Patient" frames={frames} columns={[]} disabled={false}
        onSet={async (choice) => { setFrames([frame(choice)]); return true; }}
        onReplace={vi.fn(async () => true)} onRemove={vi.fn(async () => true)}
        onAddSelected={vi.fn(async () => undefined)} onRemoveColumn={vi.fn(async () => true)} />;
    };
    render(<DynamicFramePanel />);
    fireEvent.click(screen.getByRole('button', { name: 'Browse sources' }));
    fireEvent.click(await screen.findByTestId('frame-source-choice-new-frame-source'));
    expect(await screen.findByTestId('frame-categories-frame-new')).toBeInTheDocument();
    await waitFor(() => {
      expect(mockClient.browseSemanticInventory).toHaveBeenCalledWith(
        expect.objectContaining({ frameId: 'frame-new', outputId: 'output-a' }), expect.any(AbortSignal));
      expect(mockClient.browseSemanticInventory.mock.calls.at(-1)?.[0].selectionMode).toBeUndefined();
    });
    expect(mockClient.browseSemanticInventory.mock.calls.at(-1)?.[0].selectionMode).toBeUndefined();
  });
});
