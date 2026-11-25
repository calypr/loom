import { RequestOwner } from '../../../data/requests';
import { changeTableRootFor, type ReadyRootChangeAssessment, type RootRecipeActions } from '../session/rootChangeActions';
import { createRootChangeStateStore, type PendingRowChangePreview } from '../session/rootChangeState';
import type { useGetExplorerBuilderStateV2Query } from '../session/useBuilderLoad';
import { useCallback, useMemo, useRef, useState, useSyncExternalStore, type Dispatch, type MutableRefObject, type SetStateAction } from 'react';
import { isCompilerNavigationSelection } from '../model/workspaceGuards';
import {
  sameRootChangeRequestIdentity,
  type RootChangePickerState,
  type RootChangeRequestIdentity,
  type RootChangeResolution,
  type RootChangeSelection,
} from '../model/rootChangeSelection';
import type {
  ExplorerBuilderState,
} from '../../../contracts/types';
import type {
  useAssessExplorerRowChangeMutation,
  useLoomClient,
} from '../../../react';
import { type BuilderAuthoringState, type DraftTable, selectedTable } from '../model/authoring';
import { type PreviewLimit } from '../model/previewRecovery';

type UseRootChangeOptions = {
  readonly projectId: string;
  readonly authResourcePath?: string;
  readonly state: BuilderAuthoringState;
  readonly table?: DraftTable;
  readonly draftAccess: { getState(): BuilderAuthoringState };
  readonly serverDraft: MutableRefObject<{ version: number; digest: string }>;
  readonly workspaceAttached: MutableRefObject<boolean>;
  readonly assessRowChange: ReturnType<typeof useAssessExplorerRowChangeMutation>[0];
  readonly loomClient: ReturnType<typeof useLoomClient>;
  readonly previewLimit: PreviewLimit;
  readonly refetchBuilder: ReturnType<typeof useGetExplorerBuilderStateV2Query>['refetch'];
  readonly syncBuilderData: (value: ExplorerBuilderState, mode: 'hydrate' | 'catalog') => void;
  readonly rowSettingsOpenAction: MutableRefObject<(() => void) | undefined>;
  readonly rowSettingsCloseAction: MutableRefObject<(() => void) | undefined>;
  readonly setMessage: Dispatch<SetStateAction<string | undefined>>;
};

export const useRootChange = ({
  projectId,
  authResourcePath,
  state,
  table,
  draftAccess,
  serverDraft,
  workspaceAttached,
  assessRowChange,
  loomClient,
  previewLimit,
  refetchBuilder,
  syncBuilderData,
  rowSettingsOpenAction,
  rowSettingsCloseAction,
  setMessage,
}: UseRootChangeOptions) => {
  const [rowChangeAttempt] = useState(() => ({ current: Symbol() }));
  const [rowChangeRequests] = useState(() => {
    const owner = new RequestOwner();
    owner.setIdentity('root-change');
    return owner;
  });
  const rootRecipeAssessment = useRef<ReadyRootChangeAssessment | undefined>(undefined);
  const rootRecipeActions = useRef<RootRecipeActions | undefined>(undefined);
  const [rootReplacementInspectorHost, setRootReplacementInspectorHost] = useState<HTMLElement | null>(null);
  const abortRootChangeRequests = useCallback(() => {
    rowChangeRequests.cancelAll();
  }, [rowChangeRequests]);

  const rootChangeIdentityForTable = (outputId: string): RootChangeRequestIdentity => ({
    attempt: rowChangeAttempt.current,
    project: projectId,
    ...(authResourcePath ? { authResourcePath } : {}),
    explorerId: state.explorerId,
    outputId,
    snapshotToken: state.catalog.snapshotToken,
    draftVersion: serverDraft.current.version,
    draftDigest: serverDraft.current.digest,
  });
  const currentRootChangeIdentity = table
    ? rootChangeIdentityForTable(table.outputId)
    : undefined;
  const rootIdentityKey = currentRootChangeIdentity
    ? JSON.stringify([
        currentRootChangeIdentity.project,
        currentRootChangeIdentity.authResourcePath ?? '',
        currentRootChangeIdentity.explorerId,
        currentRootChangeIdentity.outputId,
        currentRootChangeIdentity.snapshotToken,
        currentRootChangeIdentity.draftVersion,
        currentRootChangeIdentity.draftDigest,
      ])
    : 'none';
  const rootChangeStateStore = useMemo(
    () => createRootChangeStateStore(
      abortRootChangeRequests,
      setMessage,
    ),
    [abortRootChangeRequests, setMessage],
  );
  const rootChangeIdentityForSubscription = useMemo(
    () => currentRootChangeIdentity,
    [rootIdentityKey],
  );
  const subscribeRootChangeState = useCallback((subscriber: () => void) => {
    const unsubscribe = rootChangeStateStore.subscribe(subscriber);
    rootChangeStateStore.activate(rootChangeIdentityForSubscription);
    return unsubscribe;
  }, [rootChangeIdentityForSubscription, rootChangeStateStore]);
  const rootChangeStateSnapshot = useSyncExternalStore(
    subscribeRootChangeState,
    rootChangeStateStore.getSnapshot,
    rootChangeStateStore.getSnapshot,
  );
  const rootChangeState = rootChangeStateSnapshot.rootChangeState;
  const pendingRowChangePreview = rootChangeStateSnapshot.pendingRowChangePreview;
  const setRootChangeState = useCallback<Dispatch<SetStateAction<RootChangePickerState>>>(
    (update) => rootChangeStateStore.update((current) => ({
      ...current,
      rootChangeState: typeof update === 'function'
        ? update(current.rootChangeState)
        : update,
    })),
    [rootChangeStateStore],
  );
  const setPendingRowChangePreview = useCallback<Dispatch<SetStateAction<PendingRowChangePreview | undefined>>>(
    (update) => rootChangeStateStore.update((current) => ({
      ...current,
      pendingRowChangePreview: typeof update === 'function'
        ? update(current.pendingRowChangePreview)
        : update,
    })),
    [rootChangeStateStore],
  );
  const rootChangeStateRef = useMemo(() => ({
    get current() {
      return rootChangeStateStore.getSnapshot().rootChangeState;
    },
  }), [rootChangeStateStore]);

  const getCurrentRootChangeIdentity = useCallback((): RootChangeRequestIdentity | undefined => {
    const current = draftAccess.getState();
    const currentTable = selectedTable(current);
    if (!currentTable) return undefined;
    return {
      attempt: rowChangeAttempt.current,
      project: projectId,
      ...(authResourcePath ? { authResourcePath } : {}),
      explorerId: current.explorerId,
      outputId: currentTable.outputId,
      snapshotToken: current.catalog.snapshotToken,
      draftVersion: serverDraft.current.version,
      draftDigest: serverDraft.current.digest,
    };
  }, [authResourcePath, projectId]);

  const cancelRootChange = useCallback(() => {
    if (rootRecipeActions.current?.cancel() === false) return;
    abortRootChangeRequests();
    rowChangeAttempt.current = Symbol();
    setPendingRowChangePreview(undefined);
    setRootChangeState({ kind: 'idle' });
  }, [abortRootChangeRequests, setPendingRowChangePreview, setRootChangeState]);

  const changeTableRoot = useCallback(
    changeTableRootFor({
      rootRecipeActions, rowSettingsOpenAction, rowSettingsCloseAction, abortRootChangeRequests, rowChangeAttempt,
      rowChangeRequests, getCurrentRootChangeIdentity, workspaceAttached,
      assessRowChange, loadPreview: (args, signal) => loomClient.preview(args, signal),
      previewLimit, refetchBuilder, syncBuilderData, setMessage, setPendingRowChangePreview, setRootChangeState,
    }),
    [abortRootChangeRequests, assessRowChange, getCurrentRootChangeIdentity, loomClient,
      previewLimit, projectId, refetchBuilder, setMessage,
      setPendingRowChangePreview, setRootChangeState, syncBuilderData],
  );

  const selectRootChoice = useCallback((selection: RootChangeSelection) => {
    const active = rootChangeStateRef.current;
    const sameTarget = active.kind !== 'idle' && active.selection.nodeId === selection.nodeId;
    const currentIdentity = getCurrentRootChangeIdentity();
    const validPreviousResolution = active.kind === 'blocked' && sameTarget &&
      currentIdentity !== undefined &&
      sameRootChangeRequestIdentity(active.identity, currentIdentity)
      ? active.resolution
      : {};
    if (isCompilerNavigationSelection(selection)) {
      const isCurrentBlocked = active.kind === 'blocked' && currentIdentity !== undefined &&
        sameRootChangeRequestIdentity(active.identity, currentIdentity);
      const alternative = selection.kind === 'compiler-route'
        ? selection.routeToken
        : selection.occurrenceId;
      const reference = isCurrentBlocked
        ? active.assessment.unresolved.find((candidate) =>
            candidate.id === selection.referenceId &&
            candidate.code === 'AMBIGUOUS_ROW_ROOT_OCCURRENCE' &&
            candidate.alternatives?.includes(alternative),
          )
        : undefined;
      if (!reference || !isCurrentBlocked || active.kind !== 'blocked') {
        if (currentIdentity) setRootChangeState({
          kind: 'failed', reason: 'unavailable', selection: active.kind === 'idle' ? selection : active.selection,
          resolution: active.kind === 'idle' ? {} : active.resolution,
          identity: active.kind === 'idle' ? currentIdentity : active.identity,
          message: 'That relationship choice is no longer available. Refresh the choices or keep the current rows.',
        });
        return;
      }
      void changeTableRoot(selection, {
        ...active.resolution,
        rootOccurrenceId: alternative,
      });
      return;
    }
    const resolution: RootChangeResolution = {
      ...(validPreviousResolution.routeRebase?.length
        ? { routeRebase: validPreviousResolution.routeRebase }
        : {}),
      ...(selection.kind === 'authored-occurrence'
        ? { rootOccurrenceId: selection.occurrenceId }
        : {}),
    };
    void changeTableRoot(selection, resolution);
  }, [changeTableRoot, getCurrentRootChangeIdentity, rootChangeStateRef, setRootChangeState]);

  const resolveRootChange = useCallback((referenceId: string, alternative: string) => {
    const active = rootChangeStateRef.current;
    const currentIdentity = getCurrentRootChangeIdentity();
    const isCurrentBlocked = active.kind === 'blocked' && currentIdentity !== undefined &&
      sameRootChangeRequestIdentity(active.identity, currentIdentity);
    const reference = isCurrentBlocked && active.kind === 'blocked'
      ? active.assessment.unresolved.find((candidate) =>
          candidate.id === referenceId &&
          candidate.code === 'AMBIGUOUS_ROUTE_REBASE_EDGE' &&
          candidate.alternatives?.includes(alternative),
        )
      : undefined;
    if (!reference || !isCurrentBlocked || active.kind !== 'blocked') {
      if (active.kind !== 'idle') setRootChangeState({
        kind: 'failed', reason: 'unavailable', selection: active.selection,
        resolution: active.resolution, identity: active.identity,
        message: 'That relationship choice is no longer available. Refresh the choices or keep the current rows.',
      });
      return;
    }
    const resolution: RootChangeResolution = {
      ...active.resolution,
      routeRebase: [
        ...(active.resolution.routeRebase ?? []).filter(
          (choice) => choice.occurrenceId !== referenceId,
        ),
        { occurrenceId: referenceId, edgeId: alternative },
      ],
    };
    void changeTableRoot(active.selection, resolution);
  }, [changeTableRoot, getCurrentRootChangeIdentity, rootChangeStateRef, setRootChangeState]);

  const refreshRootChange = useCallback(() => {
    const active = rootChangeStateRef.current;
    if (active.kind === 'idle') return;
    if (active.kind === 'failed' && active.reason !== 'request') {
      void changeTableRoot({ kind: 'catalog-node', nodeId: active.selection.nodeId }, {});
      return;
    }
    void changeTableRoot(active.selection, active.resolution);
  }, [changeTableRoot, rootChangeStateRef]);

  const applyRowChangePreview = useCallback(async () => {
    const pending = pendingRowChangePreview;
    if (pending?.status !== 'ready') return;
    const currentIdentity = getCurrentRootChangeIdentity();
    if (!currentIdentity || !sameRootChangeRequestIdentity(pending.identity, currentIdentity)) {
      setPendingRowChangePreview(undefined);
      setMessage('The table changed. Review the proposed rows again.');
      return;
    }
    const applied = await rootRecipeActions.current?.apply(pending.assessment);
    if (applied) setPendingRowChangePreview(undefined);
  }, [getCurrentRootChangeIdentity, pendingRowChangePreview, setMessage, setPendingRowChangePreview]);

  return {
    rootChangeState,
    setRootChangeState,
    rootChangeStateRef,
    pendingRowChangePreview,
    setPendingRowChangePreview,
    rootRecipeAssessment,
    rootRecipeActions,
    rootReplacementInspectorHost,
    setRootReplacementInspectorHost,
    rootChangeIdentityForTable,
    currentRootChangeIdentity,
    getCurrentRootChangeIdentity,
    cancelRootChange,
    changeTableRoot,
    selectRootChoice,
    resolveRootChange,
    refreshRootChange,
    applyRowChangePreview,
  };
};
