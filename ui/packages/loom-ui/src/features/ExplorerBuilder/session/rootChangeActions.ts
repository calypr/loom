import type { PreviewExplorerBuilderArgs } from '../../../client/api';
import { sameDraftRevision } from '../../../contracts/identity';
import type { ExplorerBuilderPreviewResult, ExplorerBuilderState, RowChangeAssessment } from '../../../contracts/types';
import type { RequestOwner } from '../../../data/requests';
import type { useAssessExplorerRowChangeMutation } from '../../../react';
import { isDraftRestoreError } from '../model/draftRestore';
import {
  sameRootChangeRequestIdentity,
  type RootChangePickerState,
  type RootChangeRequestIdentity,
  type RootChangeResolution,
  type RootChangeSelection,
} from '../model/rootChangeSelection';
import type { PreviewLimit } from '../model/previewRecovery';
import type { createRootChangeStateStore, PendingRowChangePreview } from './rootChangeState';
import type { useGetExplorerBuilderStateV2Query } from './useBuilderLoad';

export type ReadyRootChangeAssessment = Extract<RowChangeAssessment, { readonly status: 'READY' }>;

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

type Cell<Value> = { current: Value };

type RootChangePorts = {
  readonly rootRecipeActions: Cell<RootRecipeActions | undefined>;
  readonly rowSettingsOpenAction: Cell<(() => void) | undefined>;
  readonly rowSettingsCloseAction: Cell<(() => void) | undefined>;
  readonly abortRootChangeRequests: () => void;
  readonly rowChangeRequestEpoch: Cell<number>;
  readonly rowChangeRequests: RequestOwner;
  readonly rootChangeStateStore: ReturnType<typeof createRootChangeStateStore>;
  readonly getCurrentRootChangeIdentity: () => RootChangeRequestIdentity | undefined;
  readonly workspaceAttached: Cell<boolean>;
  readonly assessRowChange: ReturnType<typeof useAssessExplorerRowChangeMutation>[0];
  readonly rowChangeAssessmentRequest: Cell<{ readonly abort: () => void } | undefined>;
  readonly loadPreview: (args: PreviewExplorerBuilderArgs, signal: AbortSignal) => Promise<ExplorerBuilderPreviewResult>;
  readonly previewLimit: PreviewLimit;
  readonly refetchBuilder: ReturnType<typeof useGetExplorerBuilderStateV2Query>['refetch'];
  readonly syncBuilderData: (value: ExplorerBuilderState, mode: 'hydrate' | 'catalog') => void;
  readonly setMessage: (message: string | undefined) => void;
  readonly setPendingRowChangePreview: (preview: PendingRowChangePreview | undefined) => void;
  readonly setRootChangeState: (state: RootChangePickerState) => void;
};

export function changeTableRootFor({
  rootRecipeActions, rowSettingsOpenAction, rowSettingsCloseAction, abortRootChangeRequests, rowChangeRequestEpoch,
  rowChangeRequests, rootChangeStateStore, getCurrentRootChangeIdentity, workspaceAttached,
  assessRowChange, rowChangeAssessmentRequest, loadPreview, previewLimit, refetchBuilder, syncBuilderData,
  setMessage, setPendingRowChangePreview, setRootChangeState,
}: RootChangePorts): (selection: RootChangeSelection, resolution?: RootChangeResolution) => Promise<void> {
  return async function changeTableRoot(selection: RootChangeSelection, resolution: RootChangeResolution = {}) {
    const rootOwnership = rootRecipeActions.current?.begin(selection.nodeId, 'PRESERVE');
    if (!rootOwnership) return;
    rowSettingsOpenAction.current?.();
    abortRootChangeRequests();
    rowChangeRequestEpoch.current += 1;
    const epoch = rowChangeRequestEpoch.current;
    const requestIdentity = String(epoch);
    rowChangeRequests.setIdentity(requestIdentity);
    const storeLifecycleVersion = rootChangeStateStore.getLifecycleVersion();
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
      return rootChangeStateStore.getLifecycleVersion() === storeLifecycleVersion &&
        workspaceAttached.current && rowChangeRequestEpoch.current === epoch && currentIdentity !== undefined &&
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
      requestId: `row-change-${globalThis.crypto.randomUUID()}`,
    });
    rowChangeAssessmentRequest.current = request;
    try {
      const assessment = await request.unwrap();
      if (!isCurrentAttempt()) return;
      if (!sameDraftRevision(assessment, identity)) {
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
      const context = { assessment, identity };
      setRootChangeState({ kind: 'previewing', selection, resolution, identity });
      setPendingRowChangePreview({ ...context, status: 'loading' });
      const preview = rowChangeRequests.start('preview', requestIdentity);
      if (!preview) return;
      try {
        const candidatePreview = await loadPreview({
          project: identity.project,
          explorerId: identity.explorerId,
          authResourcePath: identity.authResourcePath,
          receiptId: candidateReceiptId,
          outputId: identity.outputId,
          limit: previewLimit,
          requestId: `row-change-preview-${globalThis.crypto.randomUUID()}`,
        }, preview.signal);
        if (preview.signal.aborted || !isCurrentAttempt()) return;
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
        if (preview.signal.aborted || !isCurrentAttempt()) return;
        rootOwnership.failed(error instanceof Error ? error.message : 'The proposed starting-record rows could not be loaded.');
        setPendingRowChangePreview(undefined);
        setRootChangeState({
          kind: 'failed', reason: 'request', selection, resolution, identity,
          message: error instanceof Error ? error.message : 'Loom could not preview the proposed rows.',
        });
      } finally {
        preview.finish();
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
  };
}
