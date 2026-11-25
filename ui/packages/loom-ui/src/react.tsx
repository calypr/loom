import { createLoomClient, type LoomClient, type LoomOutputRequest, type LoomOutputResult } from './client/api';
import type {
  ApplyExplorerBuilderCommandsArgs,
  AssessExplorerRowChangeArgs,
  BrowseSemanticInventoryArgs,
  CreateExplorerArgs,
  DeleteExplorerArgs,
  ExplorerAuthoringProjectArgs,
  ExplorerAuthoringStateArgs,
  ExplorerCandidateSuggestionsArgs,
  PreviewExplorerBuilderArgs,
  PopulationMappingArgs,
  PublishExplorerBuilderArgs,
  ReconcileExplorerBuilderArgs,
} from './client/api';

import { useLoomClient, useOptionalLoomClient } from './data/provider';
import { useQuery, type QueryResult } from './data/queries';
import { useMutation } from './data/useMutation';

export { LoomProvider, useLoomClient } from './data/provider';
export { useQuery } from './data/queries';

export const useGetExplorerAuthoringExplorersQuery = (args: ExplorerAuthoringProjectArgs): QueryResult<Awaited<ReturnType<LoomClient['listExplorers']>>> => {
  const client = useLoomClient();
  return useQuery((signal) => client.listExplorers(args, signal), [client, args.project]);
};

export const useGetExplorerAuthoringCapabilityV2Query = (args: ExplorerAuthoringStateArgs): QueryResult<Awaited<ReturnType<LoomClient['getCapability']>>> => {
  const client = useLoomClient();
  return useQuery((signal) => client.getCapability(args, signal), [client, args.project, args.explorerId, args.authResourcePath], Boolean(args.explorerId));
};

export const useApplyExplorerBuilderCommandsV2Mutation = () => {
  const client = useLoomClient();
  return useMutation<ApplyExplorerBuilderCommandsArgs, Awaited<ReturnType<LoomClient['applyCommands']>>>((args, signal) => client.applyCommands(args, signal));
};

export const useAssessExplorerRowChangeMutation = () => {
  const client = useLoomClient();
  return useMutation<AssessExplorerRowChangeArgs, Awaited<ReturnType<LoomClient['assessRowChange']>>>((args, signal) => client.assessRowChange(args, signal));
};

export const useReconcileExplorerBuilderV2Mutation = () => {
  const client = useLoomClient();
  return useMutation<ReconcileExplorerBuilderArgs, Awaited<ReturnType<LoomClient['reconcile']>>>((args, signal) => client.reconcile(args, signal));
};

export const useGetExplorerCandidateSuggestionsV2Mutation = () => {
  const client = useLoomClient();
  return useMutation<ExplorerCandidateSuggestionsArgs, Awaited<ReturnType<LoomClient['suggestions']>>>((args, signal) => client.suggestions(args, signal));
};



export const usePreviewExplorerAuthoringV2Mutation = () => {
  const client = useLoomClient();
  return useMutation<PreviewExplorerBuilderArgs, Awaited<ReturnType<LoomClient['preview']>>>((args, signal) => client.preview(args, signal));
};

export const usePopulationMappingMutation = () => {
  const client = useOptionalLoomClient();
  return useMutation<PopulationMappingArgs, Awaited<ReturnType<LoomClient['populationMapping']>>>((args, signal) => {
    if (!client) return Promise.reject(new Error('Loom UI must be rendered inside LoomProvider.'));
    return client.populationMapping(args, signal);
  });
};

export const usePublishExplorerAuthoringV2Mutation = () => {
  const client = useLoomClient();
  return useMutation<PublishExplorerBuilderArgs, Awaited<ReturnType<LoomClient['publish']>>>((args, signal) => client.publish(args, signal));
};

export const useCreateExplorerAuthoringMutation = () => {
  const client = useLoomClient();
  return useMutation<CreateExplorerArgs, Awaited<ReturnType<LoomClient['createExplorer']>>>((args, signal) => client.createExplorer(args, signal));
};

export const useDeleteExplorerAuthoringMutation = () => {
  const client = useLoomClient();
  return useMutation<DeleteExplorerArgs, null>((args, signal) => client.deleteExplorer(args, signal));
};

export const useLoomRuntime = (args: ExplorerAuthoringStateArgs): QueryResult<Awaited<ReturnType<LoomClient['getExplorer']>>> => {
  const client = useLoomClient();
  return useQuery((signal) => client.getExplorer(args, signal), [client, 'runtime', args.project, args.explorerId]);
};

export const useLoomOutput = (
  request: LoomOutputRequest,
  options: { readonly enabled?: boolean } = {},
): QueryResult<LoomOutputResult> => {
  const client = useLoomClient();
  const enabled = options.enabled ?? true;
  const identity = JSON.stringify(request);
  const query = useQuery(
    (signal) => enabled
      ? client.queryOutput(request, signal)
      : Promise.resolve({ columns: [], rows: [], rowIds: [], totalCount: 0, pageInfo: { hasNextPage: false }, facets: [] }),
    [client, enabled, identity],
  );
  return enabled ? query : { data: undefined, error: undefined, isLoading: false, isFetching: false, refetch: async () => ({}) };
};

export const useLoomRows = (
  project: string,
  selector: Awaited<ReturnType<LoomClient['getExplorer']>>['outputs'][number]['selector'] | undefined,
  columns: ReadonlyArray<string>,
  options: { readonly enabled?: boolean; readonly first?: number } = {},
): QueryResult<Awaited<ReturnType<LoomClient['rows']>>> => {
  const client = useLoomClient();
  const enabled = options.enabled ?? Boolean(selector);
  const identity = selector ? JSON.stringify(selector) : 'none';
  const query = useQuery(
    (signal) => enabled && selector ? client.rows(selector, columns, { project, first: options.first, signal }) : Promise.resolve({ columns: [], rows: [], totalCount: 0 }),
    [client, 'rows', enabled, project, identity, columns.join(',')],
  );
  return enabled ? query : { data: undefined, error: undefined, isLoading: false, isFetching: false, refetch: async () => ({}) };
};
