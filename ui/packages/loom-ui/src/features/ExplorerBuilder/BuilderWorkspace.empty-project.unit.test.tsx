// @vitest-environment jsdom
import React from 'react';
import { beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { createLoomClient } from '../../api';
import {
  useApplyExplorerBuilderCommandsV2Mutation,
  useAssessExplorerRowChangeMutation,
  useCreateExplorerAuthoringMutation,
  useDeleteExplorerAuthoringMutation,
  useGetExplorerCandidateSuggestionsV2Mutation,
  usePreviewExplorerAuthoringV2Mutation,
  usePopulationMappingMutation,
  usePublishExplorerAuthoringV2Mutation,
  useReconcileExplorerBuilderV2Mutation,
  LoomProvider,
} from '../../react';
import BuilderWorkspace from './BuilderWorkspace';

vi.mock('../../react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../react')>();
  return {
    ...actual,
    useApplyExplorerBuilderCommandsV2Mutation: vi.fn(),
    useAssessExplorerRowChangeMutation: vi.fn(),
    useCreateExplorerAuthoringMutation: vi.fn(),
    useDeleteExplorerAuthoringMutation: vi.fn(),
    useResolveConfiguredColumnContextsQuery: vi.fn(() => ({
      data: undefined,
      error: undefined,
      isLoading: false,
      isFetching: false,
      refetch: vi.fn(),
    })),
    useResolvePopulationSelectionQuery: vi.fn(() => ({
      data: undefined,
      error: undefined,
      isLoading: false,
      isFetching: false,
      refetch: vi.fn(),
    })),
    useGetExplorerCandidateSuggestionsV2Mutation: vi.fn(),
    usePreviewExplorerAuthoringV2Mutation: vi.fn(),
    usePopulationMappingMutation: vi.fn(),
    usePublishExplorerAuthoringV2Mutation: vi.fn(),
    useReconcileExplorerBuilderV2Mutation: vi.fn(),
  };
});

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

const project = 'HTAN_INT/BForePC';
const authResourcePath = '/programs/HTAN_INT/projects/BForePC';
const explorerSummary = (explorerId: string, title: string) => ({
  project,
  explorerId,
  title,
  management: 'repository',
  updatedAt: '2026-10-10T00:00:00Z',
});
const createTestClient = (
  listedExplorers: () => ReadonlyArray<ReturnType<typeof explorerSummary>>,
) => {
  const client = createLoomClient();
  const listExplorers = vi.spyOn(client, 'listExplorers').mockImplementation(
    async () => listedExplorers(),
  );
  const getBuilder = vi.spyOn(client, 'getBuilder').mockResolvedValue(newBuilderState());
  const getCapability = vi.spyOn(client, 'getCapability').mockResolvedValue({
    apiVersion: 'loom.calypr.org/explorer-authoring/v2',
    kind: 'ExplorerAuthoringCapabilities',
    operations: [],
    features: {
      emissionFilters: false,
      emissionCharts: false,
      sharedFilters: false,
      fixedFilters: false,
      fileActions: false,
      deleteExplorer: false,
    },
  });
  return { client, listExplorers, getBuilder, getCapability };
};

describe('BuilderWorkspace empty authenticated project', () => {
  beforeEach(() => {
    (useCreateExplorerAuthoringMutation as Mock).mockReturnValue(
      mutationResult(),
    );
    (useDeleteExplorerAuthoringMutation as Mock).mockReturnValue(
      mutationResult(),
    );
    (useApplyExplorerBuilderCommandsV2Mutation as Mock).mockReturnValue(
      mutationResult(),
    );
    (useAssessExplorerRowChangeMutation as Mock).mockReturnValue(
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
    (usePopulationMappingMutation as Mock).mockReturnValue(mutationResult());
    (usePublishExplorerAuthoringV2Mutation as Mock).mockReturnValue(
      mutationResult(),
    );
  });

  it('loads the reserved shared dataframe for a default URL in an empty project', async () => {
    const { client, listExplorers, getBuilder, getCapability } = createTestClient(
      () => [],
    );
    render(
      <LoomProvider client={client}>
        <BuilderWorkspace
          organization="HTAN_INT"
          project="BForePC"
          explorerId="default"
        />
      </LoomProvider>,
    );

    await waitFor(() => expect(getBuilder).toHaveBeenCalledWith(
      expect.objectContaining({ project, explorerId: 'dataframe-default', authResourcePath }),
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    ));

    expect(listExplorers).toHaveBeenCalledWith(
      { project, authResourcePath },
      expect.any(AbortSignal),
    );
    expect(getCapability).toHaveBeenCalledWith(
      expect.objectContaining({ project, explorerId: 'dataframe-default', authResourcePath }),
      expect.any(AbortSignal),
    );
    expect((screen.getByRole('combobox', { name: 'Explorer' }) as HTMLSelectElement).value)
      .toBe('dataframe-default');
    expect(screen.getByRole('option', { name: 'Shared default dataframe' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Create your first Explorer' })).not.toBeInTheDocument();
    expect(getBuilder).not.toHaveBeenCalledWith(
      expect.objectContaining({ explorerId: 'default' }), expect.anything(),
    );
    expect(screen.getByText('New explorer')).toBeInTheDocument();
    expect(screen.queryByText('Couldn’t load this dataset.')).not.toBeInTheDocument();
  });

  it('creates a blank Explorer from the shared dataframe toolbar and switches its owner', async () => {
    const createdExplorer = { explorerId: 'first-explorer', title: 'First Explorer' };
    const { client, listExplorers, getBuilder } = createTestClient(() => []);

    const createExplorer = vi.fn().mockReturnValue({
      unwrap: vi.fn().mockResolvedValue({ explorerId: createdExplorer.explorerId }),
    });
    createExplorer.mockImplementation(() => {
      return {
        unwrap: vi.fn().mockResolvedValue({ explorerId: createdExplorer.explorerId }),
      };
    });
    (useCreateExplorerAuthoringMutation as Mock).mockReturnValue([
      createExplorer,
      { isLoading: false },
    ]);

    render(
      <LoomProvider client={client}>
        <BuilderWorkspace
          organization="HTAN_INT"
          project="BForePC"
          explorerId="default"
        />
      </LoomProvider>,
    );

    await screen.findByRole('heading', { name: 'Build your first table' });
    expect(getBuilder).toHaveBeenCalledWith(
      expect.objectContaining({ project, explorerId: 'dataframe-default', authResourcePath }),
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );

    fireEvent.click(screen.getByText('New explorer'));
    fireEvent.change(screen.getByLabelText('Explorer name'), {
      target: { value: 'First Explorer' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Create blank' }));

    await waitFor(() => {
      expect(createExplorer).toHaveBeenCalledTimes(1);
      expect(screen.getByRole('heading', { name: 'Build your first table' }))
        .toBeInTheDocument();
      expect(getBuilder).toHaveBeenCalledWith(
        expect.objectContaining({ project, explorerId: createdExplorer.explorerId, authResourcePath }),
        expect.anything(),
      );
    });
    expect(listExplorers).toHaveBeenCalledTimes(2);
    await expect(listExplorers.mock.results[1]?.value).resolves.toEqual([]);
    const payload = createExplorer.mock.calls[0]?.[0];
    expect(payload).toMatchObject({
      project,
      authResourcePath,
      name: createdExplorer.title,
      title: createdExplorer.title,
    });
    expect(payload).not.toHaveProperty('sourceExplorerId');
  });

  it('loads a valid explicit non-default route Explorer', async () => {
    const routeExplorer = explorerSummary('routed-explorer', 'Routed Explorer');
    const { client, getBuilder, getCapability } = createTestClient(
      () => [routeExplorer],
    );

    render(
      <LoomProvider client={client}>
        <BuilderWorkspace
          organization="HTAN_INT"
          project="BForePC"
          explorerId={routeExplorer.explorerId}
        />
      </LoomProvider>,
    );

    await waitFor(() => expect(getBuilder).toHaveBeenCalledWith(
      expect.objectContaining({ project, explorerId: routeExplorer.explorerId, authResourcePath }),
      expect.anything(),
    ));
    expect(getCapability).toHaveBeenCalledWith(
      expect.objectContaining({ project, explorerId: routeExplorer.explorerId, authResourcePath }),
      expect.any(AbortSignal),
    );
    expect(screen.queryByText('Couldn’t load this dataset.')).not.toBeInTheDocument();
  });

  it('keeps a missing explicit non-default route visible as an error', async () => {
    const routeExplorerId = 'missing-explicit-explorer';
    const { client, getBuilder } = createTestClient(() => []);
    getBuilder.mockRejectedValue(new Error('EXPLORER_NOT_FOUND'));

    render(
      <LoomProvider client={client}>
        <BuilderWorkspace
          organization="HTAN_INT"
          project="BForePC"
          explorerId={routeExplorerId}
        />
      </LoomProvider>,
    );

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('Couldn’t load this dataset.');
    });
    expect(getBuilder).toHaveBeenCalledWith(
      expect.objectContaining({ project, explorerId: routeExplorerId, authResourcePath }),
      expect.anything(),
    );
    expect(screen.queryByRole('heading', { name: 'Create your first Explorer' }))
      .not.toBeInTheDocument();
  });

  it('keeps the shared dataframe selected until a listed Explorer is explicitly chosen', async () => {
    const listedExplorer = explorerSummary('existing-explorer', 'Existing Explorer');
    const { client, getBuilder, getCapability } = createTestClient(
      () => [listedExplorer],
    );

    render(
      <LoomProvider client={client}>
        <BuilderWorkspace
          organization="HTAN_INT"
          project="BForePC"
          explorerId="default"
        />
      </LoomProvider>,
    );

    await waitFor(() => expect(getBuilder).toHaveBeenCalledWith(
      expect.objectContaining({ project, explorerId: 'dataframe-default', authResourcePath }),
      expect.anything(),
    ));
    expect(getCapability).toHaveBeenCalledWith(
      expect.objectContaining({ project, explorerId: 'dataframe-default', authResourcePath }),
      expect.any(AbortSignal),
    );
    expect(getBuilder).not.toHaveBeenCalledWith(
      expect.objectContaining({ explorerId: listedExplorer.explorerId }), expect.anything(),
    );
    expect((screen.getByRole('combobox', { name: 'Explorer' }) as HTMLSelectElement).value)
      .toBe('dataframe-default');
    fireEvent.change(screen.getByRole('combobox', { name: 'Explorer' }), {
      target: { value: listedExplorer.explorerId },
    });
    await waitFor(() => expect(getBuilder).toHaveBeenCalledWith(
      expect.objectContaining({ project, explorerId: listedExplorer.explorerId, authResourcePath }),
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    ));
    expect(getCapability).toHaveBeenCalledWith(
      expect.objectContaining({ project, explorerId: listedExplorer.explorerId, authResourcePath }),
      expect.any(AbortSignal),
    );
  });

  it('resolves the default URL to the shared dataframe even when a legacy default is listed', async () => {
    const listedDefault = explorerSummary('default', 'Default Explorer');
    const { client, getBuilder, getCapability } = createTestClient(
      () => [listedDefault],
    );

    render(
      <LoomProvider client={client}>
        <BuilderWorkspace
          organization="HTAN_INT"
          project="BForePC"
          explorerId="default"
        />
      </LoomProvider>,
    );

    await waitFor(() => expect(getBuilder).toHaveBeenCalledWith(
      expect.objectContaining({ project, explorerId: 'dataframe-default', authResourcePath }),
      expect.anything(),
    ));
    expect(getCapability).toHaveBeenCalledWith(
      expect.objectContaining({ project, explorerId: 'dataframe-default', authResourcePath }),
      expect.any(AbortSignal),
    );
    expect(screen.getByRole('option', { name: 'Shared default dataframe' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: listedDefault.title })).toBeInTheDocument();
    expect(getBuilder).not.toHaveBeenCalledWith(
      expect.objectContaining({ explorerId: 'default' }), expect.anything(),
    );
  });
});
