import { QueryClient, QueryObserver, type QueryKey } from '@tanstack/react-query';

export interface QueryRefetchOptions {
  readonly reload?: boolean;
}

interface QuerySnapshot<T> {
  readonly data?: T;
  readonly error?: unknown;
  readonly loading: boolean;
}

export interface QueryCacheScope {
  readonly client: QueryClient;
  readonly key: QueryKey;
}

export function createQueryResource<T>(
  loader: (signal: AbortSignal, options?: QueryRefetchOptions) => Promise<T>,
  enabled = true,
  cache?: QueryCacheScope,
) {
  const client = cache?.client ?? new QueryClient();
  const queryKey = cache?.key ?? ['loom'];
  const observer = new QueryObserver(client, {
    queryKey,
    queryFn: async ({ signal }) => ({ value: await loader(signal) }),
    enabled,
    retry: false,
    networkMode: 'always',
    staleTime: Infinity,
    gcTime: Infinity,
    structuralSharing: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
  const listeners = new Set<() => void>();
  let unsubscribeObserver: (() => void) | undefined;
  let lastResult: ReturnType<typeof observer.getCurrentResult> | undefined;
  let snapshot: QuerySnapshot<T> = { loading: enabled };

  function getSnapshot(): QuerySnapshot<T> {
    const result = observer.getCurrentResult();
    if (result !== lastResult) {
      lastResult = result;
      snapshot = result.isFetching || result.isPending
        ? { data: result.data?.value, loading: enabled }
        : result.isError
          ? { error: result.error, loading: false }
          : { data: result.data?.value, loading: false };
    }
    return snapshot;
  }

  return {
    getSnapshot,
    subscribe(listener: () => void) {
      listeners.add(listener);
      unsubscribeObserver ??= observer.subscribe(() => listeners.forEach(notify => notify()));
      return () => {
        listeners.delete(listener);
        queueMicrotask(() => {
          if (listeners.size || !unsubscribeObserver) return;
          unsubscribeObserver();
          unsubscribeObserver = undefined;
          if (!cache) {
            client.removeQueries({ queryKey, exact: true });
            observer.setOptions(observer.options);
          }
        });
      };
    },
    async refresh(nextOptions?: QueryRefetchOptions): Promise<{ readonly data?: T; readonly error?: unknown }> {
      if (!enabled) return {};
      void client.cancelQueries({ queryKey, exact: true });
      try {
        const result = await client.fetchQuery({
          ...observer.options,
          staleTime: 0,
          queryFn: async ({ signal }) => ({ value: await loader(signal, nextOptions) }),
        });
        return { data: result.value };
      } catch (error) {
        return { error };
      } finally {
        observer.updateResult();
      }
    },
  };
}
