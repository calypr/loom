import type { useReconcileExplorerBuilderV2Mutation, usePreviewExplorerAuthoringV2Mutation } from '../../../react';
import type { useGetExplorerBuilderStateV2Query } from './useBuilderLoad';
import type { ExplorerAuthoringApiError } from '../../../client/api';
import type { ExplorerAuthoringDiagnostic, ExplorerBuilderCompileResult, ExplorerBuilderPreviewResult, ExplorerBuilderState } from '../../../contracts/types';
import { intentFingerprint, workspaceFromState, type BuilderAuthoringState } from '../model/authoring';
import { builderAuthoringReducer } from '../model/reducer';
import { reconcileAcceptance } from '../model/reconcileAcceptance';
import { reconcileRecoveryAction, reconcileResultIsCurrent } from '../model/reconcileRecovery';
import { isPreviewResponseSizeError, previewRecoveryAction } from '../model/previewRecovery';
import type { AutomaticPreviewExecution, PreviewRequest } from './automaticPreview';
import type { ReconcileRequestSlot } from './reconcileRequests';

type Ref<Value> = { current: Value };

type DraftActions = {
  readonly draftAccess: { getState(): BuilderAuthoringState; setCurrentDraft(state: BuilderAuthoringState): void };
  readonly dispatch: (action: Parameters<typeof builderAuthoringReducer>[1]) => void;
  readonly refetchBuilder: ReturnType<typeof useGetExplorerBuilderStateV2Query>['refetch'];
  readonly syncBuilderData: (value: ExplorerBuilderState, mode: 'hydrate' | 'catalog') => void;
};

type ReconcileEnvironment = DraftActions & {
  readonly projectId: string;
  readonly authResourcePath?: string;
  readonly setMessage: (message: string | undefined) => void;
};

type CompilePorts = ReconcileEnvironment & {
  readonly workspaceAttached: Ref<boolean>;
  readonly compileRequests: ReconcileRequestSlot;
  readonly reconcileBuilder: ReturnType<typeof useReconcileExplorerBuilderV2Mutation>[0];
  readonly diagnosticsFromError: (error: unknown) => ReadonlyArray<ExplorerAuthoringDiagnostic>;
};

export function reconcileCurrentFor({
  projectId, authResourcePath, draftAccess, dispatch, refetchBuilder, syncBuilderData, setMessage,
  workspaceAttached, compileRequests, reconcileBuilder, diagnosticsFromError,
}: CompilePorts) {
  return async function reconcileCurrent(
    owner?: AutomaticPreviewExecution,
    outputId?: string,
  ): Promise<ExplorerBuilderCompileResult | undefined> {
    if (!workspaceAttached.current) return undefined;
    const submitted = draftAccess.getState();
    if (
      !submitted.catalog.snapshotToken ||
      submitted.tables.length === 0 ||
      submitted.tables.some((candidate) => !candidate.document.rootResourceType)
    )
      return undefined;
    const generation = compileRequests.advance();
    const submittedFingerprint = intentFingerprint(
      workspaceFromState(submitted),
    );
    const requestIsCurrent = () => workspaceAttached.current && reconcileResultIsCurrent({
      generation,
      currentGeneration: compileRequests.generation(),
      submittedExplorerId: submitted.explorerId,
      currentExplorerId: draftAccess.getState().explorerId,
      submittedFingerprint,
      currentFingerprint: intentFingerprint(workspaceFromState(draftAccess.getState())),
    });
    let snapshotToken = submitted.catalog.snapshotToken;
    let draftVersion = submitted.draftVersion;
    let draftDigest = submitted.draftDigest;
    let attempt = 1;
    let staleRefreshes = 0;
    const maxAttempts = 3;
    const maxStaleRefreshes = 2;
    if (!owner) dispatch({ type: 'compiling' });
    for (;;) {
      if (owner?.cancelled || !requestIsCurrent()) return undefined;
      const request = compileRequests.track(generation, () => reconcileBuilder({
        project: projectId,
        explorerId: submitted.explorerId,
        authResourcePath,
        snapshotToken,
        draftVersion,
        draftDigest,
        ...(outputId ? { outputId } : {}),
        requestId: `builder-${generation}-${attempt}`,
      }));
      if (!request) return undefined;
      if (owner) {
        owner.compileGeneration = generation;
        owner.compileRequest = request;
      }
      try {
        const value = await request.unwrap();
        if (owner?.cancelled || !requestIsCurrent()) return undefined;
        const current = draftAccess.getState();
        const acceptance = reconcileAcceptance(value, {
          generation,
          currentGeneration: compileRequests.generation(),
          submittedExplorerId: submitted.explorerId,
          currentExplorerId: current.explorerId,
          submittedFingerprint,
          currentFingerprint: intentFingerprint(workspaceFromState(current)),
        }, { snapshotToken, ...(outputId ? { outputId } : {}) });
        if (acceptance === 'obsolete') return undefined;
        if (acceptance === 'mismatch') {
          dispatch({
            type: 'repair',
            diagnostics: [{
              severity: 'error',
              code: 'RECONCILE_RECEIPT_MISMATCH',
              message: 'Loom returned a compile receipt for a different snapshot or output.',
            }],
          });
          return undefined;
        }
        dispatch({ type: 'compiled', value });
        return acceptance === 'accepted' ? value : undefined;
      } catch (error) {
        const apiError = error as ExplorerAuthoringApiError;
        if (
          owner?.cancelled ||
          !requestIsCurrent() ||
          apiError.code === 'CLIENT_CANCELLED'
        )
          return undefined;
        const recovery = reconcileRecoveryAction(apiError, {
          attempt,
          maxAttempts,
          staleRefreshes,
          maxStaleRefreshes,
        });
        if (recovery === 'refresh-and-stop') {
          const refreshed = await refetchBuilder({ reload: true });
          if (owner?.cancelled || !requestIsCurrent()) return undefined;
          if (refreshed.data) {
            syncBuilderData(refreshed.data, 'hydrate');
            setMessage(undefined);
          } else {
            dispatch({
              type: 'repair',
              diagnostics: diagnosticsFromError(apiError),
            });
          }
          return undefined;
        }
        if (recovery === 'refresh-and-retry') {
          const refreshed = await refetchBuilder({ reload: true });
          if (owner?.cancelled || !requestIsCurrent() || !refreshed.data) return undefined;
          syncBuilderData(refreshed.data, 'catalog');
          snapshotToken = refreshed.data.catalog.snapshotToken;
          draftVersion = refreshed.data.draftVersion;
          draftDigest = refreshed.data.draftDigest;
          staleRefreshes += 1;
          attempt += 1;
          continue;
        }
        if (recovery === 'retry') {
          attempt += 1;
          await new Promise((resolve) => setTimeout(resolve, 250));
          continue;
        }
        dispatch({
          type: 'repair',
          diagnostics: diagnosticsFromError(apiError),
        });
        return undefined;
      } finally {
        request.finish();
        if (owner?.compileRequest === request) owner.compileRequest = undefined;
      }
    }
  };
}

type PreviewPorts = Omit<ReconcileEnvironment, 'dispatch'> & {
  readonly previewRequests: ReconcileRequestSlot;
  readonly previewBuilder: ReturnType<typeof usePreviewExplorerAuthoringV2Mutation>[0];
  readonly reconcileCurrent: ReturnType<typeof reconcileCurrentFor>;
};

export function executePreviewFor({
  projectId, authResourcePath, draftAccess, refetchBuilder, syncBuilderData, setMessage,
  previewRequests, previewBuilder, reconcileCurrent,
}: PreviewPorts) {
  return async function executePreview(
    request: PreviewRequest,
    receipt: ExplorerBuilderCompileResult,
    owner?: AutomaticPreviewExecution,
  ): Promise<{ readonly receipt: ExplorerBuilderCompileResult; readonly preview: ExplorerBuilderPreviewResult } | undefined> {
    const generation = previewRequests.advance();
    let activeRequest = request;
    let activeReceipt = receipt;
    const limit = request.limit;
    let transientRetries = 0;
    for (;;) {
      if (owner?.cancelled || generation !== previewRequests.generation()) return undefined;
      const previewRequest = previewRequests.track(generation, () => previewBuilder({
        project: projectId,
        explorerId: draftAccess.getState().explorerId,
        authResourcePath,
        receiptId: activeReceipt.receiptId,
        outputId: activeRequest.outputId,
        limit,
      }));
      if (!previewRequest) return undefined;
      if (owner) {
        owner.previewGeneration = generation;
        owner.previewRequest = previewRequest;
      }
      try {
        const value = await previewRequest.unwrap();
        if (
          owner?.cancelled ||
          generation !== previewRequests.generation() ||
          value.receiptId !== activeReceipt.receiptId ||
          value.outputId !== activeRequest.outputId
        )
          return undefined;
        setMessage(undefined);
        return { receipt: activeReceipt, preview: value };
      } catch (error) {
        const apiError = error as ExplorerAuthoringApiError;
        if (
          owner?.cancelled ||
          generation !== previewRequests.generation() ||
          apiError.code === 'CLIENT_CANCELLED'
        )
          return undefined;
        const recovery = previewRecoveryAction(apiError, {
          receiptRefreshes: activeRequest.receiptRefreshes,
          transientRetries,
          limit,
        });
        if (recovery === 'retry') {
          transientRetries += 1;
          continue;
        }
        if (recovery === 'recompile') {
          activeRequest = {
            ...activeRequest,
            limit,
            receiptRefreshes: activeRequest.receiptRefreshes + 1,
          };
          const refreshedReceipt = await reconcileCurrent(owner, activeRequest.outputId);
          if (owner?.cancelled || generation !== previewRequests.generation()) return undefined;
          if (!refreshedReceipt) return undefined;
          activeReceipt = refreshedReceipt;
          setMessage(undefined);
          continue;
        }
        if (recovery === 'refresh-catalog') {
          activeRequest = {
            ...activeRequest,
            limit,
            receiptRefreshes: activeRequest.receiptRefreshes + 1,
          };
          const refreshed = await refetchBuilder({ reload: true });
          if (owner?.cancelled || generation !== previewRequests.generation()) return undefined;
          if (!refreshed.data) return undefined;
          draftAccess.setCurrentDraft(builderAuthoringReducer(draftAccess.getState(), {
            type: 'catalogRefreshed',
            catalog: refreshed.data.catalog,
          }));
          syncBuilderData(refreshed.data, 'catalog');
          const refreshedReceipt = await reconcileCurrent(owner, activeRequest.outputId);
          if (owner?.cancelled || generation !== previewRequests.generation()) return undefined;
          if (!refreshedReceipt) return undefined;
          activeReceipt = refreshedReceipt;
          setMessage(undefined);
          continue;
        }
        if (isPreviewResponseSizeError(apiError.code)) {
          setMessage(
            `Loom could not return ${limit} preview rows at the current table width. Choose fewer rows or hide some table columns.`,
          );
        } else if (
          ['PLAN_TOO_EXPENSIVE', 'EXPENSIVE_PLAN'].includes(
            apiError.code ?? '',
          )
        ) {
          setMessage(
            'This plan is too expensive to preview. Remove columns or shorten the route.',
          );
        } else {
          const suffix = apiError.code ? ` (${apiError.code})` : '';
          setMessage(`Preview failed: ${apiError.message}${suffix}`);
        }
        throw error;
      } finally {
        previewRequest.finish();
        if (owner?.previewRequest === previewRequest) owner.previewRequest = undefined;
      }
    }
  };
}
