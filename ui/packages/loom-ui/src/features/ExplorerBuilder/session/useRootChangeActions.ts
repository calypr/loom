import { RequestOwner } from '../../../data/requests';
import { changeTableRootFor, rootChangeChoiceActionsFor, type ReadyRootChangeAssessment, type RootRecipeActions } from './rootChangeActions';
import { createRootChangeStateStore, type PendingRowChangePreview } from './rootChangeState';
import { useBuilderActions, useBuilderSession } from './BuilderSessionProvider';
import { useCallback, useMemo, useRef, useState, useSyncExternalStore, type Dispatch, type MutableRefObject, type SetStateAction } from 'react';
import {
  sameRootChangeRequestIdentity,
  type RootChangePickerState,
  type RootChangeRequestIdentity,
} from '../model/rootChangeSelection';
import type { ExplorerBuilderPreviewResult } from '../../../contracts/types';
import type { PreviewExplorerBuilderArgs } from '../../../client/api';
import type { BuilderMutationResult } from '../../../react';
import { selectedTable } from '../model/authoring';
import { type PreviewLimit } from '../model/previewRecovery';

type RootChangeOptions = {
  readonly workspaceAttached: MutableRefObject<boolean>;
  readonly assessRowChange: BuilderMutationResult<'assessRowChange'>[0];
  readonly loomClient: { preview(args: PreviewExplorerBuilderArgs, signal?: AbortSignal): Promise<ExplorerBuilderPreviewResult> };
  readonly previewLimit: PreviewLimit;
  readonly rowSettingsOpenAction: MutableRefObject<(() => void) | undefined>;
  readonly rowSettingsCloseAction: MutableRefObject<(() => void) | undefined>;
  readonly setMessage: Dispatch<SetStateAction<string | undefined>>;
};

export const useRootChangeActions = ({
  workspaceAttached,
  assessRowChange,
  loomClient,
  previewLimit,
  rowSettingsOpenAction,
  rowSettingsCloseAction,
  setMessage,
}: RootChangeOptions) => {
  const { state, draftAccess, serverDraft, builderQueryArgs } = useBuilderSession(draft => draft);
  const { refetchBuilder, syncBuilderData } = useBuilderActions();
  const { project: projectId, authResourcePath } = builderQueryArgs;
  const table = selectedTable(state);
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

  const choices = useMemo(() => rootChangeChoiceActionsFor({
    changeTableRoot, getCurrentRootChangeIdentity, readRootChangeState: () => rootChangeStateRef.current,
    setRootChangeState,
  }), [changeTableRoot, getCurrentRootChangeIdentity, rootChangeStateRef, setRootChangeState]);

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
    ...choices,
    applyRowChangePreview,
  };
};
