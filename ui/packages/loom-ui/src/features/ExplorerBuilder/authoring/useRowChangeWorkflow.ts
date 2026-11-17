import { useCallback, useEffect, useRef, useState } from 'react';
import type { MutableRefObject } from 'react';
import type { AssessExplorerRowChangeArgs, LoomClient } from '../../../api';
import type {
  ExplorerBuilderCommand,
  ExplorerBuilderPreviewResult,
  ExplorerBuilderState,
  RowChangeAssessment,
} from '../../../types';
import { isDraftRestoreError } from './draftRestore';
import { selectedTable, type BuilderAuthoringState } from './model';
import type { PreviewLimit } from './previewRecovery';
import {
  sameRootChangeRequestIdentity,
  type RootChangePickerState,
  type RootChangeRequestIdentity,
  type RootChangeResolution,
  type RootChangeSelection,
} from './rootChangeSelection';
import { isCompilerNavigationSelection } from './workspaceGuards';

export type RowChangePreviewContext = {
  readonly assessment: Extract<RowChangeAssessment, { readonly status: 'READY' }>;
  readonly identity: RootChangeRequestIdentity;
};

export type PendingRowChangePreview =
  | (RowChangePreviewContext & { readonly status: 'loading' })
  | (RowChangePreviewContext & { readonly status: 'ready'; readonly preview: ExplorerBuilderPreviewResult });

type RowChangeAssessmentRequest = {
  readonly unwrap: () => Promise<RowChangeAssessment>;
  readonly abort: () => void;
};

export type RowChangeWorkflowOptions = {
  readonly applyCommands: (commands: ReadonlyArray<ExplorerBuilderCommand>) => Promise<boolean>;
  readonly assessRowChange: (args: AssessExplorerRowChangeArgs) => RowChangeAssessmentRequest;
  readonly authResourcePath: string | undefined;
  readonly draftDigest: string;
  readonly draftVersion: number;
  readonly explorerId: string;
  readonly latestState: MutableRefObject<BuilderAuthoringState>;
  readonly loomClient: LoomClient;
  readonly previewLimit: PreviewLimit;
  readonly projectId: string;
  readonly refetchBuilder: (options?: { readonly reload?: boolean }) => Promise<{ readonly data?: ExplorerBuilderState }>;
  readonly selectedOutputId: string | undefined;
  readonly serverDraft: MutableRefObject<{ readonly version: number; readonly digest: string }>;
  readonly setMessage: (message: string | undefined) => void;
  readonly snapshotToken: string;
  readonly syncBuilderData: (value: ExplorerBuilderState, mode: 'hydrate') => void;
  readonly workspaceAttached: MutableRefObject<boolean>;
};

export const useRowChangeWorkflow = (options: RowChangeWorkflowOptions) => {
  const {
    applyCommands,
    assessRowChange,
    authResourcePath,
    draftDigest,
    draftVersion,
    explorerId,
    latestState,
    loomClient,
    previewLimit,
    projectId,
    refetchBuilder,
    selectedOutputId,
    serverDraft,
    setMessage,
    snapshotToken,
    syncBuilderData,
    workspaceAttached,
  } = options;

  const [rootChangeState, setRootChangeState] =
    useState<RootChangePickerState>({ kind: 'idle' });
  const rootChangeStateRef = useRef(rootChangeState);
  rootChangeStateRef.current = rootChangeState;
  const [pendingRowChangePreview, setPendingRowChangePreview] =
    useState<PendingRowChangePreview>();
  const rowChangeAssessmentRequest = useRef<{ readonly abort: () => void } | undefined>(undefined);
  const rowChangeRequestEpoch = useRef(0);
  const rowChangePreviewRequest = useRef<AbortController | undefined>(undefined);

  useEffect(() => {
    const currentIdentity: RootChangeRequestIdentity | undefined = selectedOutputId !== undefined ? {
      epoch: rowChangeRequestEpoch.current,
      project: projectId,
      ...(authResourcePath ? { authResourcePath } : {}),
      explorerId,
      outputId: selectedOutputId,
      snapshotToken,
      draftVersion: serverDraft.current.version,
      draftDigest: serverDraft.current.digest,
    } : undefined;
    const active = rootChangeStateRef.current;
    const pendingPreview = pendingRowChangePreview;
    const activeIsStale = active.kind !== 'idle' && (
      currentIdentity === undefined ||
      !sameRootChangeRequestIdentity(active.identity, currentIdentity)
    );
    const previewIsStale = pendingPreview !== undefined && (
      currentIdentity === undefined ||
      !sameRootChangeRequestIdentity(pendingPreview.identity, currentIdentity)
    );
    if (!activeIsStale && !previewIsStale) return;

    rowChangeAssessmentRequest.current?.abort();
    rowChangeAssessmentRequest.current = undefined;
    rowChangePreviewRequest.current?.abort();
    rowChangePreviewRequest.current = undefined;
    rowChangeRequestEpoch.current += 1;
    setPendingRowChangePreview(undefined);

    if (activeIsStale) {
      const sameExplorerOutput = active.identity.project === projectId &&
        active.identity.authResourcePath === authResourcePath &&
        active.identity.explorerId === explorerId &&
        active.identity.outputId === selectedOutputId;
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
  }, [authResourcePath, projectId, explorerId, snapshotToken,
    draftVersion, draftDigest, selectedOutputId, pendingRowChangePreview]);

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
    abortRootChangeRequests();
    rowChangeRequestEpoch.current += 1;
    setPendingRowChangePreview(undefined);
    setRootChangeState({ kind: 'idle' });
  }, [abortRootChangeRequests]);

  const changeTableRoot = useCallback(
    async (selection: RootChangeSelection, resolution: RootChangeResolution = {}) => {
      abortRootChangeRequests();
      rowChangeRequestEpoch.current += 1;
      const epoch = rowChangeRequestEpoch.current;
      const identity = getCurrentRootChangeIdentity();
      if (!identity) {
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
          setRootChangeState({
            kind: 'failed', reason: 'stale', selection, resolution, identity,
            message: 'The table changed while Loom checked this row choice. Refresh the choices or keep the current rows.',
          });
          return;
        }
        if (assessment.status === 'NO_CHANGE') {
          setRootChangeState({ kind: 'idle' });
          setMessage(undefined);
          return;
        }
        if (assessment.status === 'BLOCKED') {
          setRootChangeState({ kind: 'blocked', selection, resolution, identity, assessment });
          setMessage(undefined);
          return;
        }
        const candidateReceiptId = assessment.candidateReceiptId;
        if (!candidateReceiptId || assessment.proposal.outputId !== identity.outputId ||
            assessment.proposal.rootNodeId !== selection.nodeId ||
            (resolution.rootOccurrenceId !== undefined &&
              assessment.proposal.rootOccurrenceId !== resolution.rootOccurrenceId)) {
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
            setPendingRowChangePreview(undefined);
            setRootChangeState({
              kind: 'failed', reason: 'unavailable', selection, resolution, identity,
              message: 'Loom returned a preview that does not match the selected row change. The table has not changed.',
            });
            return;
          }
          setPendingRowChangePreview({ ...context, status: 'ready', preview: candidatePreview });
          setRootChangeState({ kind: 'idle' });
        } catch (error) {
          if (controller.signal.aborted || !isCurrentAttempt()) return;
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
    const applied = await applyCommands([{
      type: 'APPLY_TABLE_ROOT_REBASE',
      rowChange: pending.assessment.proposal,
    }]);
    if (applied) setPendingRowChangePreview(undefined);
  }, [applyCommands, getCurrentRootChangeIdentity, pendingRowChangePreview]);

  const deriveIdentity = (outputId: string): RootChangeRequestIdentity => ({
    epoch: rowChangeRequestEpoch.current,
    project: projectId,
    ...(authResourcePath ? { authResourcePath } : {}),
    explorerId,
    outputId,
    snapshotToken,
    draftVersion: serverDraft.current.version,
    draftDigest: serverDraft.current.digest,
  });

  const currentRootChangeIdentity = selectedOutputId !== undefined
    ? deriveIdentity(selectedOutputId)
    : undefined;
  const readyRowChangePreview = pendingRowChangePreview?.status === 'ready' &&
    currentRootChangeIdentity !== undefined &&
    sameRootChangeRequestIdentity(pendingRowChangePreview.identity, currentRootChangeIdentity)
    ? pendingRowChangePreview
    : undefined;

  return {
    applyRowChangePreview,
    cancelRootChange,
    deriveIdentity,
    pendingRowChangePreview,
    readyRowChangePreview,
    refreshRootChange,
    resolveRootChange,
    rootChangeState,
    selectRootChoice,
  };
};
