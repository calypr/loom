import { QueryClient } from '@tanstack/react-query';
import { useMemo } from 'react';
import { loomReadKey } from '../client/api';
import { isLoomReadPublisher, observeLoomReadInvalidation, type LoomReadKey } from '../client/readInvalidation';

const clients = new WeakMap<object, QueryClient>();

export function loomReadCache(client: object, key: LoomReadKey) {
  if (key[0] !== 'capability' && !isLoomReadPublisher(client)) return undefined;
  let queryClient = clients.get(client);
  if (!queryClient) {
    const ownedClient = new QueryClient();
    observeLoomReadInvalidation(client, (queryKey, refreshActive) => {
      const interrupted = refreshActive ? [] : ownedClient.getQueryCache().findAll({
        queryKey, type: 'active', fetchStatus: 'fetching',
        predicate: (query) => query.state.data === undefined,
      });
      void ownedClient.cancelQueries({ queryKey });
      void ownedClient.invalidateQueries({ queryKey, refetchType: refreshActive ? 'active' : 'none' });
      if (interrupted.length) {
        void ownedClient.refetchQueries({ type: 'active', predicate: (query) => interrupted.includes(query) });
      }
    });
    queryClient = ownedClient;
    clients.set(client, queryClient);
  }
  return { client: queryClient, key };
}

export function useLoomReadCache(
  client: object,
  kind: LoomReadKey[0],
  { project, authResourcePath, explorerId }: Parameters<typeof loomReadKey>[1],
) {
  return useMemo(
    () => loomReadCache(client, loomReadKey(kind, { project, authResourcePath, explorerId })),
    [client, kind, project, authResourcePath, explorerId],
  );
}
