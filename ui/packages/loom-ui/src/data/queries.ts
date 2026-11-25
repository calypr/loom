import { useMemo, useRef, useSyncExternalStore } from 'react';
import { resourceFor, type QueryRefetchOptions } from './resource';

export { resourceFor } from './resource';
export type { QueryRefetchOptions } from './resource';

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
): QueryResult<T> => {
  const loaderRef = useRef(loader);
  loaderRef.current = loader;
  const resource = useMemo(
    () => resourceFor((signal, options) => loaderRef.current(signal, options), enabled),
    [...dependencies, enabled],
  );
  const snapshot = useSyncExternalStore(resource.subscribe, resource.getSnapshot, resource.getSnapshot);
  return {
    ...snapshot,
    isLoading: enabled && snapshot.loading,
    isFetching: enabled && snapshot.loading,
    refetch: resource.refresh,
  };
};

export const useKeyedQuery = <T,>(
  key: string | undefined,
  loader: (signal: AbortSignal, options?: QueryRefetchOptions) => Promise<T>,
  enabled = true,
): QueryResult<T> => useQuery(loader, [key], Boolean(key) && enabled);
