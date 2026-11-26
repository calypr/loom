import React, { createContext, useContext } from 'react';
import type { LoomClient } from '../client/api';

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

export const useOptionalLoomClient = (): LoomClient | null => useContext(LoomClientContext);

export const useLoomClient = (): LoomClient => {
  const client = useOptionalLoomClient();
  if (!client) throw new Error('Loom UI must be rendered inside LoomProvider.');
  return client;
};
