import { useMemo, useRef, useSyncExternalStore } from 'react';
import { createStore } from './createStore';

export interface QueryResult<T> {
  readonly data?: T;
  readonly error?: unknown;
  readonly isLoading: boolean;
  readonly isFetching: boolean;
  readonly refetch: (options?: QueryRefetchOptions) => Promise<{ readonly data?: T; readonly error?: unknown }>;
}

export interface QueryRefetchOptions {
  readonly reload?: boolean;
}

interface ResourceSnapshot<T> {
  readonly data?: T;
  readonly error?: unknown;
  readonly loading: boolean;
}

interface Resource<T> {
  readonly getSnapshot: () => ResourceSnapshot<T>;
  readonly subscribe: (listener: () => void) => () => void;
  readonly refresh: (options?: QueryRefetchOptions) => Promise<{ readonly data?: T; readonly error?: unknown }>;
}

export const resourceFor = <T,>(
  loader: (signal: AbortSignal, options?: QueryRefetchOptions) => Promise<T>,
  enabled = true,
): Resource<T> => {
  const store = createStore<ResourceSnapshot<T>>({ loading: enabled });
  let controller: AbortController | undefined;
  let started = false;
  let epoch = 0;
  const refresh = async (options?: QueryRefetchOptions) => {
    if (!enabled) return {};
    controller?.abort();
    const requestController = new AbortController();
    controller = requestController;
    const requestEpoch = ++epoch;
    started = true;
    store.setSnapshot({ ...store.getSnapshot(), loading: true, error: undefined });
    try {
      const data = await loader(requestController.signal, options);
      if (controller !== requestController || epoch !== requestEpoch) return { data };
      store.setSnapshot({ data, loading: false });
      return { data };
    } catch (error) {
      if (requestController.signal.aborted || controller !== requestController || epoch !== requestEpoch) {
        return { error };
      }
      store.setSnapshot({ error, loading: false });
      return { error };
    }
  };
  const resource: Resource<T> = {
    getSnapshot: store.getSnapshot,
    subscribe: (listener) => {
      const unsubscribe = store.subscribe(listener);
      if (!started && enabled) void refresh();
      return () => {
        unsubscribe();
        queueMicrotask(() => {
          if (store.hasSubscribers()) return;
          controller?.abort();
          controller = undefined;
          epoch += 1;
          started = false;
          store.setSnapshot({ loading: enabled });
        });
      };
    },
    refresh,
  };
  return resource;
};

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
