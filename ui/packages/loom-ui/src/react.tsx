import React, { createContext, useCallback, useContext, useMemo, useRef, useState, useSyncExternalStore } from 'react';
import { createLoomClient, type LoomClient, type LoomOutputRequest, type LoomOutputResult } from './api';
import type {
  ApplyExplorerBuilderCommandsArgs,
  CreateExplorerArgs,
  DeleteExplorerArgs,
  ExplorerAuthoringProjectArgs,
  ExplorerAuthoringStateArgs,
  ExplorerCandidateSuggestionsArgs,
  PreviewExplorerBuilderArgs,
  PublishExplorerBuilderArgs,
  ReconcileExplorerBuilderArgs,
} from './api';

const LoomClientContext = createContext<LoomClient | null>(null);

export const LoomProvider = ({
  client,
  children,
}: {
  readonly client: LoomClient;
  readonly children: React.ReactNode;
}) => (
  <LoomClientContext.Provider value={client}>
    {children}
  </LoomClientContext.Provider>
);

export const useLoomClient = (): LoomClient => {
  const client = useContext(LoomClientContext);
  if (!client) throw new Error('Loom UI must be rendered inside LoomProvider.');
  return client;
};

interface QueryResult<T> {
  readonly data?: T;
  readonly error?: unknown;
  readonly isLoading: boolean;
  readonly isFetching: boolean;
  readonly refetch: () => Promise<{ readonly data?: T; readonly error?: unknown }>;
}

interface ResourceSnapshot<T> {
  readonly data?: T;
  readonly error?: unknown;
  readonly loading: boolean;
}

interface Resource<T> {
  readonly getSnapshot: () => ResourceSnapshot<T>;
  readonly subscribe: (listener: () => void) => () => void;
  readonly refresh: () => Promise<{ readonly data?: T; readonly error?: unknown }>;
}

export const resourceFor = <T,>(loader: (signal: AbortSignal) => Promise<T>): Resource<T> => {
  let snapshot: ResourceSnapshot<T> = { loading: true };
  let controller: AbortController | undefined;
  const listeners = new Set<() => void>();
  let started = false;
  let epoch = 0;
  const notify = () => listeners.forEach((listener) => listener());
  const refresh = async () => {
    controller?.abort();
    const requestController = new AbortController();
    controller = requestController;
    const requestEpoch = ++epoch;
    started = true;
    snapshot = { ...snapshot, loading: true, error: undefined };
    notify();
    try {
      const data = await loader(requestController.signal);
      if (controller !== requestController || epoch !== requestEpoch) return { data };
      snapshot = { data, loading: false };
      notify();
      return { data };
    } catch (error) {
      if (requestController.signal.aborted || controller !== requestController || epoch !== requestEpoch) {
        return { error };
      }
      snapshot = { error, loading: false };
      notify();
      return { error };
    }
  };
  const resource: Resource<T> = {
    getSnapshot: () => snapshot,
    subscribe: (listener) => {
      listeners.add(listener);
      if (!started) void refresh();
      return () => {
        listeners.delete(listener);
        queueMicrotask(() => {
          if (listeners.size > 0) return;
          controller?.abort();
          controller = undefined;
          epoch += 1;
          started = false;
          snapshot = { loading: true };
        });
      };
    },
    refresh,
  };
  return resource;
};

const useQuery = <T,>(
  loader: (signal: AbortSignal) => Promise<T>,
  dependencies: ReadonlyArray<unknown>,
): QueryResult<T> => {
  const loaderRef = useRef(loader);
  loaderRef.current = loader;
  const resource = useMemo(
    () => resourceFor((signal) => loaderRef.current(signal)),
    dependencies,
  );
  const snapshot = useSyncExternalStore(resource.subscribe, resource.getSnapshot, resource.getSnapshot);
  return {
    ...snapshot,
    isLoading: snapshot.loading,
    isFetching: snapshot.loading,
    refetch: resource.refresh,
  };
};

interface MutationState {
  readonly isLoading: boolean;
}

type Trigger<TArgs, TResult> = (args: TArgs) => {
  readonly unwrap: () => Promise<TResult>;
  readonly abort: () => void;
};

const useMutation = <TArgs, TResult>(
  operation: (args: TArgs, signal: AbortSignal) => Promise<TResult>,
): [Trigger<TArgs, TResult>, MutationState] => {
  const [pendingRequests, setPendingRequests] = useState(0);
  const operationRef = useRef(operation);
  operationRef.current = operation;
  const trigger = useCallback<Trigger<TArgs, TResult>>((args) => {
    const controller = new AbortController();
    const promise = operationRef.current(args, controller.signal);
    setPendingRequests((count) => count + 1);
    const settle = () => setPendingRequests((count) => count - 1);
    void promise.then(settle, settle);
    return { unwrap: () => promise, abort: () => controller.abort() };
  }, []);
  return [trigger, { isLoading: pendingRequests > 0 }];
};

export const useGetExplorerAuthoringExplorersQuery = (args: ExplorerAuthoringProjectArgs): QueryResult<Awaited<ReturnType<LoomClient['listExplorers']>>> => {
  const client = useLoomClient();
  return useQuery((signal) => client.listExplorers(args, signal), [client, args.project]);
};

export const useGetExplorerBuilderStateV2Query = (args: ExplorerAuthoringStateArgs): QueryResult<Awaited<ReturnType<LoomClient['getBuilder']>>> => {
  const client = useLoomClient();
  return useQuery((signal) => client.getBuilder(args, { signal }), [client, args.project, args.explorerId]);
};

export const useGetExplorerAuthoringCapabilityV2Query = (args: ExplorerAuthoringStateArgs): QueryResult<Awaited<ReturnType<LoomClient['getCapability']>>> => {
  const client = useLoomClient();
  return useQuery((signal) => client.getCapability(args, signal), [client, args.project, args.explorerId]);
};

export const useApplyExplorerBuilderCommandsV2Mutation = () => {
  const client = useLoomClient();
  return useMutation<ApplyExplorerBuilderCommandsArgs, Awaited<ReturnType<LoomClient['applyCommands']>>>((args, signal) => client.applyCommands(args, signal));
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
      : Promise.resolve({ columns: [], rows: [], totalCount: 0, pageInfo: { hasNextPage: false }, facets: [] }),
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

export type { QueryResult };
