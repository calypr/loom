import { builderDataKeyFor, hydrateDraft } from '../session/draftHydration';
import { useGetExplorerBuilderStateV2Query } from '../session/useBuilderLoad';
import { useCallback, useMemo, useRef, useState, useSyncExternalStore } from 'react';
import type { ExplorerBuilderState } from '../../../contracts/types';
import { builderAuthoringReducer } from '../model/reducer';
import { shouldSyncServerDraft } from '../model/serverDraftSync';
import { createBuilderDraftAccess, createLocalDraftStore, createRememberedTable } from '../session/draftAccess';

type UseBuilderDraftOptions = {
  readonly projectId: string;
  readonly authResourcePath?: string;
  readonly effectiveExplorerId: string;
  readonly ownerKey: string;
};

export const useBuilderDraft = ({
  projectId,
  authResourcePath,
  effectiveExplorerId,
  ownerKey,
}: UseBuilderDraftOptions) => {
  const rememberedTable = useMemo(
    () => createRememberedTable(ownerKey, () => window.sessionStorage),
    [ownerKey],
  );
  const rememberedOutputId = useMemo(rememberedTable.read, [rememberedTable]);
  const draftScope = useMemo(
    () => ({ projectId, effectiveExplorerId, ownerKey, rememberedOutputId }),
    [projectId, effectiveExplorerId, ownerKey, rememberedOutputId],
  );
  const explorerQueryEnabled = Boolean(effectiveExplorerId);
  const builderQueryArgs = {
    project: projectId,
    explorerId: effectiveExplorerId,
    authResourcePath,
  };
  const builder = useGetExplorerBuilderStateV2Query(builderQueryArgs);
  const refetchBuilder = builder.refetch;
  const builderDataKey = builder.data
    ? builderDataKeyFor(ownerKey, builder.data)
    : '';
  const builderDataRef = useRef(builder.data);
  builderDataRef.current = builder.data;
  const [{ getSnapshot, subscribe, update: setLocalState }] = useState(createLocalDraftStore);
  const localState = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  const [restorableDraft, setRestorableDraft] = useState<{
    readonly ownerKey: string;
    readonly revisionId?: string;
  }>();
  const previousDraftRevisionId = restorableDraft?.ownerKey === ownerKey
    ? restorableDraft.revisionId
    : builder.data?.previousDraftRevisionId;
  const state =
    localState?.key === builderDataKey
      ? localState.value
      : hydrateDraft(builder.data, localState, draftScope);
  const dispatch = useCallback(
    (action: Parameters<typeof builderAuthoringReducer>[1]) => {
      setLocalState((current) => {
        const base =
          current?.key === builderDataKey
            ? current.value
            : hydrateDraft(builderDataRef.current, current, draftScope);
        return {
          key: builderDataKey,
          value: builderAuthoringReducer(base, action),
        };
      });
    },
    [authResourcePath, builderDataKey, draftScope, setLocalState],
  );
  const serverDraft = useRef({ version: 0, digest: '' });
  const [draftAccess] = useState(() => createBuilderDraftAccess(state));
  draftAccess.setCurrentDraft(state);
  const serverDraftKey = useRef('');
  if (shouldSyncServerDraft(builder.data, serverDraftKey.current, builderDataKey)) {
    serverDraft.current = {
      version: builder.data.draftVersion,
      digest: builder.data.draftDigest,
    };
    serverDraftKey.current = builderDataKey;
  }

  const syncBuilderData = useCallback(
    (value: ExplorerBuilderState, mode: 'hydrate' | 'catalog') => {
      const nextKey = builderDataKeyFor(ownerKey, value);
      setRestorableDraft({
        ownerKey,
        revisionId: value.previousDraftRevisionId,
      });
      serverDraft.current = {
        version: value.draftVersion,
        digest: value.draftDigest,
      };
      serverDraftKey.current = nextKey;
      setLocalState((current) => {
        const nextValue =
          mode === 'hydrate'
            ? hydrateDraft(value, current, draftScope)
            : builderAuthoringReducer(
                current?.key === builderDataKey
                  ? current.value
                  : draftAccess.getState(),
                { type: 'catalogRefreshed', catalog: value.catalog },
              );
        return { key: nextKey, value: nextValue };
      });
    },
    [builderDataKey, draftAccess, draftScope, ownerKey, setLocalState],
  );

  return {
    builderQueryArgs,
    builder,
    refetchBuilder,
    explorerQueryEnabled,
    builderDataKey,
    state,
    dispatch,
    rememberSelectedTable: rememberedTable.write,
    previousDraftRevisionId,
    setLocalState,
    setRestorableDraft,
    serverDraft,
    serverDraftKey,
    draftAccess,
    syncBuilderData,
  };
};
