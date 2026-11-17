import { useCallback, useRef, useState, type Dispatch, type MutableRefObject, type SetStateAction } from 'react';
import { isCompilerNavigationSelection } from './workspaceGuards';
import {
  sameRootChangeRequestIdentity,
  type RootChangePickerState,
  type RootChangeRequestIdentity,
  type RootChangeResolution,
  type RootChangeSelection,
} from './rootChangeSelection';
import type {
  ExplorerBuilderCommand,
  ExplorerBuilderPreviewResult,
  ExplorerBuilderState,
  RowChangeAssessment,
} from '../../../types';
import type {
  useAssessExplorerRowChangeMutation,
  useGetExplorerBuilderStateV2Query,
  useLoomClient,
} from '../../../react';
import { isDraftRestoreError } from './draftRestore';
import { type BuilderAuthoringState, type DraftTable, selectedTable } from './model';
import { type PreviewLimit } from './previewRecovery';

type ReadyRootChangeAssessment = Extract<RowChangeAssessment, { readonly status: 'READY' }>;

type RootRecipeAttempt = {
  ready: (assessment: ReadyRootChangeAssessment, preview: ExplorerBuilderPreviewResult) => boolean;
  failed: (message: string, stale?: boolean) => void;
  noChange: () => void;
};

export type RootRecipeActions = {
  begin: (nodeId: string, mode: 'PRESERVE' | 'REPLACE') => RootRecipeAttempt | undefined;
  cancel: () => boolean;
  apply: (assessment: ReadyRootChangeAssessment) => Promise<boolean>;
};

type RowChangePreviewContext = {
  readonly assessment: ReadyRootChangeAssessment;
  readonly identity: RootChangeRequestIdentity;
};

export type PendingRowChangePreview =
  | (RowChangePreviewContext & { readonly status: 'loading' })
  | (RowChangePreviewContext & { readonly status: 'ready'; readonly preview: ExplorerBuilderPreviewResult });

type UseRootChangeOptions = {
  readonly projectId: string;
  readonly authResourcePath?: string;
  readonly state: BuilderAuthoringState;
  readonly table?: DraftTable;
  readonly latestState: MutableRefObject<BuilderAuthoringState>;
  readonly serverDraft: MutableRefObject<{ version: number; digest: string }>;
  readonly workspaceAttached: MutableRefObject<boolean>;
  readonly assessRowChange: ReturnType<typeof useAssessExplorerRowChangeMutation>[0];
  readonly loomClient: ReturnType<typeof useLoomClient>;
  readonly previewLimit: PreviewLimit;
  readonly refetchBuilder: ReturnType<typeof useGetExplorerBuilderStateV2Query>['refetch'];
  readonly syncBuilderData: (value: ExplorerBuilderState, mode: 'hydrate' | 'catalog') => void;
  readonly applyCommands: (commands: ReadonlyArray<ExplorerBuilderCommand>) => Promise<boolean>;
  readonly rowSettingsOpenAction: MutableRefObject<(() => void) | undefined>;
  readonly rowSettingsCloseAction: MutableRefObject<(() => void) | undefined>;
  readonly setMessage: Dispatch<SetStateAction<string | undefined>>;
};

export const useRootChange = ({
  projectId,
  authResourcePath,
  state,
  table,
  latestState,
  serverDraft,
  workspaceAttached,
  assessRowChange,
  loomClient,
  previewLimit,
  refetchBuilder,
  syncBuilderData,
  applyCommands,
  rowSettingsOpenAction,
  rowSettingsCloseAction,
  setMessage,
}: UseRootChangeOptions) => {
  const [rootChangeState, setRootChangeState] =
    useState<RootChangePickerState>({ kind: 'idle' });
  const rootChangeStateRef = useRef(rootChangeState);
  rootChangeStateRef.current = rootChangeState;
  const [pendingRowChangePreview, setPendingRowChangePreview] =
    useState<PendingRowChangePreview>();
  const rowChangeAssessmentRequest = useRef<{ readonly abort: () => void } | undefined>(undefined);
  const rowChangeRequestEpoch = useRef(0);
  const rowChangePreviewRequest = useRef<AbortController | undefined>(undefined);
  const rootRecipeAssessment = useRef<ReadyRootChangeAssessment | undefined>(undefined);
  const rootRecipeActions = useRef<RootRecipeActions | undefined>(undefined);
  const [rootReplacementInspectorHost, setRootReplacementInspectorHost] = useState<HTMLElement | null>(null);

  const rootChangeIdentityForTable = (outputId: string): RootChangeRequestIdentity => ({
    epoch: rowChangeRequestEpoch.current,
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
  const [handledRootIdentityKey, setHandledRootIdentityKey] = useState(rootIdentityKey);
  if (handledRootIdentityKey !== rootIdentityKey) {
    setHandledRootIdentityKey(rootIdentityKey);
    const active = rootChangeStateRef.current;
    const pendingPreview = pendingRowChangePreview;
    const activeIsStale = active.kind !== 'idle' && (
      currentRootChangeIdentity === undefined ||
      !sameRootChangeRequestIdentity(active.identity, currentRootChangeIdentity)
    );
    const previewIsStale = pendingPreview !== undefined && (
      currentRootChangeIdentity === undefined ||
      !sameRootChangeRequestIdentity(pendingPreview.identity, currentRootChangeIdentity)
    );
    if (activeIsStale || previewIsStale) {
      rowChangeAssessmentRequest.current?.abort();
      rowChangeAssessmentRequest.current = undefined;
      rowChangePreviewRequest.current?.abort();
      rowChangePreviewRequest.current = undefined;
      rowChangeRequestEpoch.current += 1;
      setPendingRowChangePreview(undefined);

      if (activeIsStale) {
        const sameExplorerOutput = active.identity.project === projectId &&
          active.identity.authResourcePath === authResourcePath &&
          active.identity.explorerId === state.explorerId &&
          active.identity.outputId === table?.outputId;
        setRootChangeState(sameExplorerOutput
          ? {
              kind: 'failed',
              reason: 'stale',
              selection: active.selection,
              resolution: active.resolution,
              identity: active.identity,
              message: 'The table changed while Loom checked this row choice. Refresh the choices or keep the current rows.',
            }
          : { kind: 'idle' });
      }
      if (previewIsStale) {
        setMessage('The table changed while Loom prepared the row preview. Review the change again.');
      }
    }
  }

  const getCurrentRootChangeIdentity = useCallback((): RootChangeRequestIdentity | undefined => {
    const current = latestState.current;
    const currentTable = selectedTable(current);
    if (!currentTable) return undefined;
    return {
      epoch: rowChangeRequestEpoch.current,
      project: projectId,
      ...(authResourcePath ? { authResourcePath } : {}),
      explorerId: current.explorerId,
      outputId: currentTable.outputId,
      snapshotToken: current.catalog.snapshotToken,
      draftVersion: serverDraft.current.version,
      draftDigest: serverDraft.current.digest,
    };
  }, [authResourcePath, projectId]);

  const abortRootChangeRequests = useCallback(() => {
    rowChangeAssessmentRequest.current?.abort();
    rowChangeAssessmentRequest.current = undefined;
    rowChangePreviewRequest.current?.abort();
    rowChangePreviewRequest.current = undefined;
  }, []);

  const cancelRootChange = useCallback(() => {
    if (rootRecipeActions.current?.cancel() === false) return;
    abortRootChangeRequests();
    rowChangeRequestEpoch.current += 1;
    setPendingRowChangePreview(undefined);
    setRootChangeState({ kind: 'idle' });
  }, [abortRootChangeRequests]);

  const changeTableRoot = useCallback(
    async (selection: RootChangeSelection, resolution: RootChangeResolution = {}) => {
      const rootOwnership = rootRecipeActions.current?.begin(selection.nodeId, 'PRESERVE');
      if (!rootOwnership) return;
      rowSettingsOpenAction.current?.();
      abortRootChangeRequests();
      rowChangeRequestEpoch.current += 1;
      const epoch = rowChangeRequestEpoch.current;
      const identity = getCurrentRootChangeIdentity();
      if (!identity) {
        rootOwnership.failed('The selected table is unavailable.');
        setRootChangeState({ kind: 'idle' });
        return;
      }
      setPendingRowChangePreview(undefined);
      setMessage(undefined);
      setRootChangeState({ kind: 'assessing', selection, resolution, identity });
      const isCurrentAttempt = () => {
        const currentIdentity = getCurrentRootChangeIdentity();
        return workspaceAttached.current && rowChangeRequestEpoch.current === epoch && currentIdentity !== undefined &&
          sameRootChangeRequestIdentity(identity, currentIdentity);
      };
      const request = assessRowChange({
        project: identity.project,
        explorerId: identity.explorerId,
        authResourcePath: identity.authResourcePath,
        snapshotToken: identity.snapshotToken,
        draftVersion: identity.draftVersion,
        draftDigest: identity.draftDigest,
        outputId: identity.outputId,
        rootNodeId: selection.nodeId,
        ...resolution,
        requestId: `row-change-${window.crypto.randomUUID()}`,
      });
      rowChangeAssessmentRequest.current = request;
      try {
        const assessment = await request.unwrap();
        if (!isCurrentAttempt()) return;
        if (assessment.snapshotToken !== identity.snapshotToken ||
            assessment.draftVersion !== identity.draftVersion ||
            assessment.draftDigest !== identity.draftDigest) {
          rootOwnership.failed('The draft changed while checking the starting record.', true);
          setRootChangeState({
            kind: 'failed', reason: 'stale', selection, resolution, identity,
            message: 'The table changed while Loom checked this row choice. Refresh the choices or keep the current rows.',
          });
          return;
        }
        if (assessment.status === 'NO_CHANGE') {
          rootOwnership.noChange();
          setRootChangeState({ kind: 'idle' });
          setMessage(undefined);
          return;
        }
        if (assessment.status === 'BLOCKED') {
          rootOwnership.failed(assessment.diagnostics.map((item) => item.message).join(' ') || 'This starting-record change needs a supported relationship or repair.');
          setRootChangeState({ kind: 'blocked', selection, resolution, identity, assessment });
          setMessage(undefined);
          return;
        }
        const candidateReceiptId = assessment.candidateReceiptId;
        if (!candidateReceiptId || assessment.proposal.outputId !== identity.outputId ||
            assessment.proposal.rootNodeId !== selection.nodeId ||
            (resolution.rootOccurrenceId !== undefined &&
              assessment.proposal.rootOccurrenceId !== resolution.rootOccurrenceId)) {
          rootOwnership.failed('The starting-record assessment has no matching preview receipt.');
          setRootChangeState({
            kind: 'failed', reason: 'unavailable', selection, resolution, identity,
            message: 'Loom could not match this row choice to a preview receipt. The table has not changed.',
          });
          return;
        }
        const context: RowChangePreviewContext = { assessment, identity };
        setRootChangeState({ kind: 'previewing', selection, resolution, identity });
        setPendingRowChangePreview({ ...context, status: 'loading' });
        const controller = new AbortController();
        rowChangePreviewRequest.current = controller;
        try {
          const candidatePreview = await loomClient.preview({
            project: identity.project,
            explorerId: identity.explorerId,
            authResourcePath: identity.authResourcePath,
            receiptId: candidateReceiptId,
            outputId: identity.outputId,
            limit: previewLimit,
            requestId: `row-change-preview-${window.crypto.randomUUID()}`,
          }, controller.signal);
          if (controller.signal.aborted || !isCurrentAttempt()) return;
          if (candidatePreview.receiptId !== candidateReceiptId ||
              candidatePreview.outputId !== identity.outputId ||
              candidatePreview.rows === null) {
            rootOwnership.failed('The preview does not match the starting-record candidate.');
            setPendingRowChangePreview(undefined);
            setRootChangeState({
              kind: 'failed', reason: 'unavailable', selection, resolution, identity,
              message: 'Loom returned a preview that does not match the selected row change. The table has not changed.',
            });
            return;
          }
          if (!rootOwnership.ready(assessment, candidatePreview)) {
            setPendingRowChangePreview(undefined);
            setRootChangeState({kind:'failed', reason:'unavailable', selection, resolution, identity, message:'The starting-record candidate could not be described for both editors. The saved table has not changed.'});
            return;
          }
          setPendingRowChangePreview({ ...context, status: 'ready', preview: candidatePreview });
          rowSettingsCloseAction.current?.();
          setRootChangeState({ kind: 'idle' });
        } catch (error) {
          if (controller.signal.aborted || !isCurrentAttempt()) return;
          rootOwnership.failed(error instanceof Error ? error.message : 'The proposed starting-record rows could not be loaded.');
          setPendingRowChangePreview(undefined);
          setRootChangeState({
            kind: 'failed', reason: 'request', selection, resolution, identity,
            message: error instanceof Error ? error.message : 'Loom could not preview the proposed rows.',
          });
        } finally {
          if (rowChangePreviewRequest.current === controller) rowChangePreviewRequest.current = undefined;
        }
      } catch (error) {
        if (!isCurrentAttempt()) return;
        rootOwnership.failed(error instanceof Error ? error.message : 'The starting-record change could not be assessed.');
        const apiError = typeof error === 'object' && error !== null && 'code' in error &&
          typeof error.code === 'string'
          ? {
              code: error.code,
              ...( 'message' in error && typeof error.message === 'string'
                ? { message: error.message }
                : {}),
            }
          : undefined;
        if (apiError && isDraftRestoreError(apiError.code)) {
          const refreshed = await refetchBuilder({ reload: true });
          if (!isCurrentAttempt()) return;
          if (refreshed.data) syncBuilderData(refreshed.data, 'hydrate');
          setRootChangeState({
            kind: 'failed', reason: 'stale', selection, resolution, identity,
            message: 'The table changed while Loom checked this row choice. Refresh the choices or keep the current rows.',
          });
          return;
        }
        if (apiError?.code === 'CLIENT_CANCELLED') return;
        setRootChangeState({
          kind: 'failed', reason: 'request', selection, resolution, identity,
          message: apiError?.message ?? (error instanceof Error ? error.message : 'Loom could not assess the row change.'),
        });
      } finally {
        if (rowChangeAssessmentRequest.current === request) rowChangeAssessmentRequest.current = undefined;
      }
    },
    [abortRootChangeRequests, assessRowChange, getCurrentRootChangeIdentity, loomClient,
      previewLimit, projectId, refetchBuilder, syncBuilderData],
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
  }, [changeTableRoot, getCurrentRootChangeIdentity]);

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
  }, [changeTableRoot, getCurrentRootChangeIdentity]);

  const refreshRootChange = useCallback(() => {
    const active = rootChangeStateRef.current;
    if (active.kind === 'idle') return;
    if (active.kind === 'failed' && active.reason !== 'request') {
      void changeTableRoot({ kind: 'catalog-node', nodeId: active.selection.nodeId }, {});
      return;
    }
    void changeTableRoot(active.selection, active.resolution);
  }, [changeTableRoot]);

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
  }, [applyCommands, getCurrentRootChangeIdentity, pendingRowChangePreview]);

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
