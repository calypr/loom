import { useCallback, useMemo, useRef, useState } from 'react';
import { useGetExplorerBuilderStateV2Query } from '../../../react';
import type { ExplorerBuilderCatalog, ExplorerBuilderState } from '../../../contracts/types';
import { stateFromBuilder, type BuilderAuthoringState } from '../model/authoring';
import { builderAuthoringReducer } from '../model/reducer';
import { shouldSyncServerDraft } from '../model/serverDraftSync';

const emptyCatalog = (): ExplorerBuilderCatalog => ({
  snapshotToken: '',
  generation: '',
  routePolicy: {},
  nodes: [],
  edges: [],
  candidates: [],
});
const emptyBuilderState = (project: string): BuilderAuthoringState => ({
  project,
  explorerId: 'dataframe-default',
  catalog: emptyCatalog(),
  workspace: null,
  draftVersion: 0,
  draftDigest: '',
  previewRequestVersion: 0,
  tables: [],
  selectedOccurrenceId: 'base',
  diagnostics: [],
  dirty: false,
  reconciliation: 'idle',
});

const builderDataKeyFor = (
  ownerKey: string,
  value: {
    readonly catalog: ExplorerBuilderCatalog;
    readonly workspace: unknown;
    readonly draftVersion: number;
    readonly draftDigest: string;
  },
) =>
  `${ownerKey}:${value.catalog.snapshotToken}:${value.draftVersion}:${value.draftDigest}:${JSON.stringify(value.workspace)}`;

type UseBuilderDraftOptions = {
  readonly projectId: string;
  readonly authResourcePath?: string;
  readonly effectiveExplorerId: string;
  readonly ownerKey: string;
  readonly builderOwnerKeyFor: (project: string, authResourcePath: string | undefined, explorerId: string) => string;
};

export const useBuilderDraft = ({
  projectId,
  authResourcePath,
  effectiveExplorerId,
  ownerKey,
  builderOwnerKeyFor,
}: UseBuilderDraftOptions) => {
  const selectedTableStorageKey = `loom.builder.selected-table:${ownerKey}`;
  const rememberedOutputId = useMemo(() => {
    try {
      return window.sessionStorage.getItem(selectedTableStorageKey) ?? undefined;
    } catch {
      return undefined;
    }
  }, [selectedTableStorageKey]);
  const explorerQueryEnabled = Boolean(effectiveExplorerId);
  const builderQueryArgs = {
    project: projectId,
    explorerId: effectiveExplorerId,
    authResourcePath,
  };
  const builder = useGetExplorerBuilderStateV2Query(builderQueryArgs);
  const refetchBuilder = builder.refetch;
  const builderDataKey = builder.data
    ? builderDataKeyFor(
        builderOwnerKeyFor(projectId, authResourcePath, effectiveExplorerId),
        builder.data,
      )
    : '';
  const builderDataRef = useRef(builder.data);
  builderDataRef.current = builder.data;
  const [localState, setLocalState] = useState<{
    readonly key: string;
    readonly value: BuilderAuthoringState;
  }>();
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
      : builder.data
        ? stateFromBuilder(builder.data, {
            project: projectId,
            explorerId: effectiveExplorerId,
            selectedOutputId: localState?.key.startsWith(`${ownerKey}:`) ? localState.value.selectedOutputId : rememberedOutputId,
          })
        : emptyBuilderState(projectId);
  const dispatch = useCallback(
    (action: Parameters<typeof builderAuthoringReducer>[1]) => {
      setLocalState((current) => {
        const base =
          current?.key === builderDataKey
            ? current.value
            : builderDataRef.current
              ? stateFromBuilder(builderDataRef.current, {
                  project: projectId,
                  explorerId: effectiveExplorerId,
                  selectedOutputId: current?.key.startsWith(`${ownerKey}:`) ? current.value.selectedOutputId : rememberedOutputId,
                })
              : emptyBuilderState(projectId);
        return {
          key: builderDataKey,
          value: builderAuthoringReducer(base, action),
        };
      });
    },
    [authResourcePath, builderDataKey, effectiveExplorerId, ownerKey, projectId, rememberedOutputId],
  );
  const rememberSelectedTable = useCallback((outputId: string) => {
    try {
      window.sessionStorage.setItem(selectedTableStorageKey, outputId);
    } catch {
      // Browser storage can be unavailable; table editing remains usable.
    }
  }, [selectedTableStorageKey]);
  const serverDraft = useRef({ version: 0, digest: '' });
  const latestState = useRef(state);
  latestState.current = state;
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
      const nextKey = builderDataKeyFor(
        builderOwnerKeyFor(projectId, authResourcePath, effectiveExplorerId),
        value,
      );
      setRestorableDraft({
        ownerKey: builderOwnerKeyFor(projectId, authResourcePath, effectiveExplorerId),
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
            ? stateFromBuilder(value, {
                project: projectId,
                explorerId: effectiveExplorerId,
                selectedOutputId: current?.key.startsWith(`${ownerKey}:`) ? current.value.selectedOutputId : rememberedOutputId,
              })
            : builderAuthoringReducer(
                current?.key === builderDataKey
                  ? current.value
                  : latestState.current,
                { type: 'catalogRefreshed', catalog: value.catalog },
              );
        return { key: nextKey, value: nextValue };
      });
    },
    [builderDataKey, effectiveExplorerId, ownerKey, projectId, rememberedOutputId],
  );

  return {
    builderQueryArgs,
    builder,
    refetchBuilder,
    explorerQueryEnabled,
    builderDataKey,
    state,
    dispatch,
    rememberSelectedTable,
    previousDraftRevisionId,
    setLocalState,
    setRestorableDraft,
    serverDraft,
    serverDraftKey,
    latestState,
    syncBuilderData,
  };
};
