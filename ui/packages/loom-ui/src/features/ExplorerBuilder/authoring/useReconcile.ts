import type { useGetExplorerBuilderStateV2Query } from '../session/useBuilderLoad';
import { useCallback, useRef, type Dispatch, type MutableRefObject, type SetStateAction } from 'react';
import { useReconcileExplorerBuilderV2Mutation, usePreviewExplorerAuthoringV2Mutation } from '../../../react';
import type { ExplorerAuthoringApiError } from '../../../client/api';
import type { ExplorerAuthoringDiagnostic, ExplorerBuilderCompileResult, ExplorerBuilderPreviewResult, ExplorerBuilderState } from '../../../contracts/types';
import { intentFingerprint, workspaceFromState, type BuilderAuthoringState } from '../model/authoring';
import { builderAuthoringReducer } from '../model/reducer';
import { reconcileAcceptance } from '../model/reconcileAcceptance';
import { reconcileRecoveryAction, reconcileResultIsCurrent } from '../model/reconcileRecovery';
import { isPreviewResponseSizeError, previewRecoveryAction } from '../model/previewRecovery';
import type { AutomaticPreviewExecution, PreviewRequest } from './useAutomaticPreviewOwner';

type UseReconcileOptions = {
  readonly projectId: string;
  readonly authResourcePath?: string;
  readonly draftAccess: { getState(): BuilderAuthoringState; setCurrentDraft(state: BuilderAuthoringState): void };
  readonly workspaceAttached: MutableRefObject<boolean>;
  readonly dispatch: Dispatch<Parameters<typeof builderAuthoringReducer>[1]>;
  readonly refetchBuilder: ReturnType<typeof useGetExplorerBuilderStateV2Query>['refetch'];
  readonly syncBuilderData: (value: ExplorerBuilderState, mode: 'hydrate' | 'catalog') => void;
  readonly setMessage: Dispatch<SetStateAction<string | undefined>>;
  readonly diagnosticsFromError: (error: unknown) => ReadonlyArray<ExplorerAuthoringDiagnostic>;
};

type UseReconcileResult = {
  readonly compileGeneration: MutableRefObject<number>;
  readonly previewGeneration: MutableRefObject<number>;
  readonly activeCompile: MutableRefObject<{ abort: () => void } | undefined>;
  readonly activePreview: MutableRefObject<{ abort: () => void } | undefined>;
  readonly reconcileStatus: ReturnType<typeof useReconcileExplorerBuilderV2Mutation>[1];
  readonly previewStatus: ReturnType<typeof usePreviewExplorerAuthoringV2Mutation>[1];
  readonly reconcileCurrent: (owner?: AutomaticPreviewExecution, outputId?: string) => Promise<ExplorerBuilderCompileResult | undefined>;
  readonly executePreview: (request: PreviewRequest, receipt: ExplorerBuilderCompileResult, owner?: AutomaticPreviewExecution) => Promise<{ readonly receipt: ExplorerBuilderCompileResult; readonly preview: ExplorerBuilderPreviewResult } | undefined>;
};

export const useReconcile = ({
  projectId,
  authResourcePath,
  draftAccess,
  workspaceAttached,
  dispatch,
  refetchBuilder,
  syncBuilderData,
  setMessage,
  diagnosticsFromError,
}: UseReconcileOptions): UseReconcileResult => {
  const [reconcileBuilder, reconcileStatus] =
    useReconcileExplorerBuilderV2Mutation();
  const [previewBuilder, previewStatus] =
    usePreviewExplorerAuthoringV2Mutation();
  const compileGeneration = useRef(0);
  const previewGeneration = useRef(0);
  const activeCompile = useRef<{ abort: () => void } | undefined>(undefined);
  const activePreview = useRef<{ abort: () => void } | undefined>(undefined);

  const reconcileCurrent = useCallback(async (
    owner?: AutomaticPreviewExecution,
    outputId?: string,
  ): Promise<ExplorerBuilderCompileResult | undefined> => {
    if (!workspaceAttached.current) return undefined;
    const submitted = draftAccess.getState();
    if (
      !submitted.catalog.snapshotToken ||
      submitted.tables.length === 0 ||
      submitted.tables.some((candidate) => !candidate.document.rootResourceType)
    )
      return undefined;
    const generation = ++compileGeneration.current;
    const submittedFingerprint = intentFingerprint(
      workspaceFromState(submitted),
    );
    const requestIsCurrent = () => workspaceAttached.current && reconcileResultIsCurrent({
      generation,
      currentGeneration: compileGeneration.current,
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
      const request = reconcileBuilder({
        project: projectId,
        explorerId: submitted.explorerId,
        authResourcePath,
        snapshotToken,
        draftVersion,
        draftDigest,
        ...(outputId ? { outputId } : {}),
        requestId: `builder-${generation}-${attempt}`,
      });
      activeCompile.current = request;
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
          currentGeneration: compileGeneration.current,
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
          await new Promise((resolve) => window.setTimeout(resolve, 250));
          continue;
        }
        dispatch({
          type: 'repair',
          diagnostics: diagnosticsFromError(apiError),
        });
        return undefined;
      } finally {
        if (activeCompile.current === request) activeCompile.current = undefined;
        if (owner?.compileRequest === request) owner.compileRequest = undefined;
      }
    }
  }, [
    authResourcePath,
    dispatch,
    projectId,
    reconcileBuilder,
    refetchBuilder,
    syncBuilderData,
  ]);

  const executePreview = useCallback(
    async (
      request: PreviewRequest,
      receipt: ExplorerBuilderCompileResult,
      owner?: AutomaticPreviewExecution,
    ): Promise<{ readonly receipt: ExplorerBuilderCompileResult; readonly preview: ExplorerBuilderPreviewResult } | undefined> => {
      const generation = ++previewGeneration.current;
      let activeRequest = request;
      let activeReceipt = receipt;
      const limit = request.limit;
      let transientRetries = 0;
      for (;;) {
        if (owner?.cancelled || generation !== previewGeneration.current) return undefined;
        const previewRequest = previewBuilder({
          project: projectId,
          explorerId: draftAccess.getState().explorerId,
          authResourcePath,
          receiptId: activeReceipt.receiptId,
          outputId: activeRequest.outputId,
          limit,
        });
        activePreview.current = previewRequest;
        if (owner) {
          owner.previewGeneration = generation;
          owner.previewRequest = previewRequest;
        }
        try {
          const value = await previewRequest.unwrap();
          if (
            owner?.cancelled ||
            generation !== previewGeneration.current ||
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
            generation !== previewGeneration.current ||
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
            if (owner?.cancelled || generation !== previewGeneration.current) return undefined;
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
            if (owner?.cancelled || generation !== previewGeneration.current) return undefined;
            if (!refreshed.data) return undefined;
            draftAccess.setCurrentDraft(builderAuthoringReducer(draftAccess.getState(), {
              type: 'catalogRefreshed',
              catalog: refreshed.data.catalog,
            }));
            syncBuilderData(refreshed.data, 'catalog');
            const refreshedReceipt = await reconcileCurrent(owner, activeRequest.outputId);
            if (owner?.cancelled || generation !== previewGeneration.current) return undefined;
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
          if (activePreview.current === previewRequest) activePreview.current = undefined;
          if (owner?.previewRequest === previewRequest) owner.previewRequest = undefined;
        }
      }
    },
    [
      authResourcePath,
      previewBuilder,
      projectId,
      reconcileCurrent,
      refetchBuilder,
      syncBuilderData,
    ],
  );

  return { compileGeneration, previewGeneration, activeCompile, activePreview, reconcileStatus, previewStatus, reconcileCurrent, executePreview };
};
