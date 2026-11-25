import { createContext, useContext, type ReactNode } from 'react';
import type { useBuilderSessionDraft } from './useBuilderSessionDraft';

type BuilderSessionDraft = ReturnType<typeof useBuilderSessionDraft>;

const BuilderSessionContext = createContext<BuilderSessionDraft | undefined>(undefined);

export function BuilderSessionProvider({ draft, children }: {
  readonly draft: BuilderSessionDraft;
  readonly children: ReactNode;
}) {
  return <BuilderSessionContext.Provider value={draft}>{children}</BuilderSessionContext.Provider>;
}

export function useBuilderSession<Selection>(select: (draft: BuilderSessionDraft) => Selection): Selection {
  const draft = useContext(BuilderSessionContext);
  if (!draft) throw new Error('Builder session must be read inside BuilderSessionProvider.');
  return select(draft);
}

export function useBuilderActions() {
  return useBuilderSession(({ refetchBuilder, dispatch, rememberSelectedTable, syncBuilderData }) => ({
    refetchBuilder, dispatch, rememberSelectedTable, syncBuilderData,
  }));
}
