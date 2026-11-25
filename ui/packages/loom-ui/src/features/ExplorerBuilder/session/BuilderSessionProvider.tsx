import { createContext, useContext, type ReactNode } from 'react';
import type { LoomClient } from '../../../client/api';
import type { useBuilderSessionDraft } from './useBuilderSessionDraft';

type BuilderSessionDraft = ReturnType<typeof useBuilderSessionDraft>;
type BuilderSession = BuilderSessionDraft & { readonly transport: LoomClient };

const BuilderSessionContext = createContext<BuilderSession | undefined>(undefined);

export function BuilderSessionProvider({ draft, transport, children }: {
  readonly draft: BuilderSessionDraft;
  readonly transport: LoomClient;
  readonly children: ReactNode;
}) {
  return <BuilderSessionContext.Provider value={{ ...draft, transport }}>{children}</BuilderSessionContext.Provider>;
}

export function useBuilderSession<Selection>(select: (draft: BuilderSession) => Selection): Selection {
  const draft = useContext(BuilderSessionContext);
  if (!draft) throw new Error('Builder session must be read inside BuilderSessionProvider.');
  return select(draft);
}

export function useBuilderActions() {
  return useBuilderSession(({ refetchBuilder, dispatch, rememberSelectedTable, syncBuilderData }) => ({
    refetchBuilder, dispatch, rememberSelectedTable, syncBuilderData,
  }));
}
