import { useMemo, useRef, useSyncExternalStore } from 'react';
import { createQueryResource, type QueryCacheScope, type QueryRefetchOptions } from './queryResource';

export interface QueryResult<T> {
  readonly data?: T;
  readonly error?: unknown;
  readonly isLoading: boolean;
  readonly isFetching: boolean;
  readonly refetch: (options?: QueryRefetchOptions) => Promise<{ readonly data?: T; readonly error?: unknown }>;
}

export const useQuery = <T,>(
  loader: (signal: AbortSignal, options?: QueryRefetchOptions) => Promise<T>,
  dependencies: ReadonlyArray<unknown>,
  enabled = true,
  cache?: QueryCacheScope,
): QueryResult<T> => {
  const loaderRef = useRef(loader);
  loaderRef.current = loader;
  const resource = useMemo(
    () => createQueryResource((signal, options) => loaderRef.current(signal, options), enabled, cache),
    [...dependencies, enabled, cache?.client, cache?.key],
  );
  const snapshot = useSyncExternalStore(resource.subscribe, resource.getSnapshot, resource.getSnapshot);
  return {
    ...snapshot,
    isLoading: enabled && snapshot.loading,
    isFetching: enabled && snapshot.loading,
    refetch: resource.refresh,
  };
};
