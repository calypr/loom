// @vitest-environment jsdom
import React from 'react';
import { vi, type Mock } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import {
  useApplyExplorerBuilderCommandsV2Mutation,
  useCreateExplorerAuthoringMutation,
  useDeleteExplorerAuthoringMutation,
  useGetExplorerAuthoringCapabilityV2Query,
  useGetExplorerAuthoringExplorersQuery,
  useGetExplorerBuilderStateV2Query,
  useGetExplorerCandidateSuggestionsV2Mutation,
  usePreviewExplorerAuthoringV2Mutation,
  usePublishExplorerAuthoringV2Mutation,
  useReconcileExplorerBuilderV2Mutation,
} from '../../react';
import BuilderWorkspace from './BuilderWorkspace';

vi.mock('../../react', () => ({
  useApplyExplorerBuilderCommandsV2Mutation: vi.fn(),
  useCreateExplorerAuthoringMutation: vi.fn(),
  useDeleteExplorerAuthoringMutation: vi.fn(),
  useGetExplorerAuthoringCapabilityV2Query: vi.fn(),
  useGetExplorerAuthoringExplorersQuery: vi.fn(),
  useGetExplorerBuilderStateV2Query: vi.fn(),
  useGetExplorerCandidateSuggestionsV2Mutation: vi.fn(),
  usePreviewExplorerAuthoringV2Mutation: vi.fn(),
  usePublishExplorerAuthoringV2Mutation: vi.fn(),
  useReconcileExplorerBuilderV2Mutation: vi.fn(),
}));

const mutationResult = () => [vi.fn(), { isLoading: false }];
const newBuilderState = () => ({
  apiVersion: 'loom.calypr.org/explorer-authoring/v2' as const,
  kind: 'ExplorerBuilderState' as const,
  lifecycleState: 'NEW' as const,
  draftVersion: 0,
  draftDigest: '',
  workspace: null,
  catalog: {
    snapshotToken: 'snapshot-first',
    generation: 'generation-first',
    routePolicy: { allowRepeatedEdges: false, allowSelfLoops: false },
    nodes: [],
    edges: [],
    candidates: [],
  },
});

describe('BuilderWorkspace route selection', () => {
  beforeEach(() => {
    (useGetExplorerAuthoringExplorersQuery as Mock).mockReturnValue({
      data: [],
      isLoading: true,
      refetch: vi.fn(),
    });
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: undefined,
      isLoading: true,
      refetch: vi.fn(),
    });
    (useGetExplorerAuthoringCapabilityV2Query as Mock).mockReturnValue({
      data: undefined,
    });
    (useCreateExplorerAuthoringMutation as Mock).mockReturnValue(
      mutationResult(),
    );
    (useDeleteExplorerAuthoringMutation as Mock).mockReturnValue(
      mutationResult(),
    );
    (useApplyExplorerBuilderCommandsV2Mutation as Mock).mockReturnValue(
      mutationResult(),
    );
    (useReconcileExplorerBuilderV2Mutation as Mock).mockReturnValue(
      mutationResult(),
    );
    (useGetExplorerCandidateSuggestionsV2Mutation as Mock).mockReturnValue(
      mutationResult(),
    );
    (usePreviewExplorerAuthoringV2Mutation as Mock).mockReturnValue(
      mutationResult(),
    );
    (usePublishExplorerAuthoringV2Mutation as Mock).mockReturnValue(
      mutationResult(),
    );
  });

  it('requests the Explorer named by the route instead of default', () => {
    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    expect(useGetExplorerBuilderStateV2Query).toHaveBeenCalledWith({
      project: 'HTAN_INT/BForePC',
      explorerId: 'test',
      authResourcePath: '/programs/HTAN_INT/projects/BForePC',
    });
  });

  it('tracks a new Explorer when client-side navigation changes the query', async () => {
    const view = render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="first"
      />,
    );

    view.rerender(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="second"
      />,
    );

    await waitFor(() =>
      expect(useGetExplorerBuilderStateV2Query).toHaveBeenLastCalledWith({
        project: 'HTAN_INT/BForePC',
        explorerId: 'second',
        authResourcePath: '/programs/HTAN_INT/projects/BForePC',
      }),
    );
  });

  it('waits for a controlled route update before requesting the selected Explorer', () => {
    const onExplorerChange = vi.fn();
    const builderState = newBuilderState();
    (useGetExplorerAuthoringExplorersQuery as Mock).mockReturnValue({
      data: [
        { explorerId: 'first', title: 'First Explorer' },
        { explorerId: 'second', title: 'Second Explorer' },
      ],
      isLoading: false,
      refetch: vi.fn(),
    });
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: builderState,
      isLoading: false,
      refetch: vi.fn(),
    });
    (useGetExplorerBuilderStateV2Query as Mock).mockClear();

    const view = render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="first"
        onExplorerChange={onExplorerChange}
      />,
    );

    fireEvent.change(screen.getByRole('combobox', { name: 'Explorer' }), {
      target: { value: 'second' },
    });

    expect(onExplorerChange).toHaveBeenCalledWith('second');
    expect(useGetExplorerBuilderStateV2Query).not.toHaveBeenCalledWith({
      project: 'HTAN_INT/BForePC',
      explorerId: 'second',
      authResourcePath: '/programs/HTAN_INT/projects/BForePC',
    });

    view.rerender(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="second"
        onExplorerChange={onExplorerChange}
      />,
    );

    expect(useGetExplorerBuilderStateV2Query).toHaveBeenLastCalledWith({
      project: 'HTAN_INT/BForePC',
      explorerId: 'second',
      authResourcePath: '/programs/HTAN_INT/projects/BForePC',
    });
  });

  it('does not refetch explorers from a controlled create before the parent rerenders', async () => {
    const onExplorerChange = vi.fn();
    const refetch = vi.fn();
    const createExplorer = vi.fn().mockReturnValue({
      unwrap: vi.fn().mockResolvedValue({ explorerId: 'second' }),
    });
    (useGetExplorerAuthoringExplorersQuery as Mock).mockReturnValue({
      data: [{ explorerId: 'first', title: 'First Explorer' }],
      isLoading: false,
      refetch,
    });
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: newBuilderState(),
      isLoading: false,
      refetch: vi.fn(),
    });
    (useCreateExplorerAuthoringMutation as Mock).mockReturnValue([
      createExplorer,
      { isLoading: false },
    ]);
    (useGetExplorerBuilderStateV2Query as Mock).mockClear();

    const view = render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="first"
        onExplorerChange={onExplorerChange}
      />,
    );

    fireEvent.click(screen.getByText('New explorer'));
    fireEvent.change(screen.getByLabelText('Explorer name'), {
      target: { value: 'Second Explorer' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Create blank' }));

    await waitFor(() => {
      expect(createExplorer).toHaveBeenCalledTimes(1);
      expect(onExplorerChange).toHaveBeenCalledWith('second');
    });
    expect(refetch).not.toHaveBeenCalled();

    view.rerender(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="second"
        onExplorerChange={onExplorerChange}
      />,
    );
  });
});
