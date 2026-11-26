import { createLoomClient, type LoomClient, type LoomOutputRequest, type LoomOutputResult } from './client/api';
import type {
  BrowseSemanticInventoryArgs,
  ExplorerAuthoringProjectArgs,
  ExplorerAuthoringStateArgs,
} from './client/api';

import { useLoomClient } from './data/provider';
import { useLoomReadCache } from './data/loomReadCache';
import { useQuery, type QueryResult } from './data/queries';
export { useBuilderMutation, type BuilderMutationResult } from './data/builderMutations';

export { LoomProvider, useLoomClient } from './data/provider';
export { useQuery } from './data/queries';

export const useGetExplorerAuthoringExplorersQuery = (args: ExplorerAuthoringProjectArgs): QueryResult<Awaited<ReturnType<LoomClient['listExplorers']>>> => {
  const client = useLoomClient();
  const cache = useLoomReadCache(client, 'explorers', args);
  return useQuery((signal) => client.listExplorers(args, signal), [client, args.project, args.authResourcePath], true, cache);
};

export const useGetExplorerAuthoringCapabilityV2Query = (args: ExplorerAuthoringStateArgs): QueryResult<Awaited<ReturnType<LoomClient['getCapability']>>> => {
  const client = useLoomClient();
  const cache = useLoomReadCache(client, 'capability', args);
  return useQuery((signal) => client.getCapability(args, signal), [client, args.project, args.authResourcePath, args.explorerId], Boolean(args.explorerId), cache);
};

export const useLoomRuntime = (args: ExplorerAuthoringStateArgs): QueryResult<Awaited<ReturnType<LoomClient['getExplorer']>>> => {
  const client = useLoomClient();
  const cache = useLoomReadCache(client, 'viewer', args);
  return useQuery((signal) => client.getExplorer(args, signal), [client, args.project, args.authResourcePath, args.explorerId], true, cache);
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
