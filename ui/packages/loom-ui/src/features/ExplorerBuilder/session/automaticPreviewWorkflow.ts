import type { ReconcileRequestSlot } from './reconcileRequests';
import { sameDraftRevision, sameCatalogSnapshot } from '../../../contracts/identity';
import type { ExplorerAuthoringApiError } from '../../../client/api';
import type { ExplorerBuilderCompileResult, ExplorerBuilderPreviewResult } from '../../../contracts/types';
import { selectedTable, type BuilderAuthoringState } from '../model/authoring';
import type { builderAuthoringReducer } from '../model/reducer';
import type { PreviewLimit } from '../model/previewRecovery';
import { createAutomaticPreview, type AutomaticPreviewExecution, type AutomaticPreviewIdentity, type AutomaticPreviewRun, type PreviewRequest } from './automaticPreview';
import { matchesAcceptedPreviewHandoff, type AppliedPreviewHandoff } from './appliedChoicePreview';
import { createStore } from '../../../data/createStore';
import type { QueryRefetchOptions } from '../../../data/resource';

type AutomaticPreviewWorkflow = {
  readonly limit: ReturnType<typeof createStore<PreviewLimit>>;
  readonly appliedChoicePreview: { current: AppliedPreviewHandoff | undefined };
  readonly automaticPreviewGeneration: { current: number };
  readonly automaticPreviewRequest: { current: AutomaticPreviewIdentity | undefined };
  readonly activeAutomaticPreview: {
    current: { readonly generation: number; readonly cancel: () => void } | undefined;
  };
  readonly cache: ReturnType<typeof createAutomaticPreview>;
  readonly cancelAutomaticPreview: () => void;
};

export type AutomaticPreviewOptions = {
  readonly request: AutomaticPreviewIdentity | undefined;
  readonly enabled: boolean;
  readonly refresh: (
    request: AutomaticPreviewIdentity,
    signal: AbortSignal,
    options?: QueryRefetchOptions,
  ) => Promise<AutomaticPreviewRun | undefined>;
  readonly owner: ReturnType<typeof createAutomaticPreview>;
};

export function createAutomaticPreviewWorkflow(): AutomaticPreviewWorkflow {
  const activeAutomaticPreview: AutomaticPreviewWorkflow['activeAutomaticPreview'] = { current: undefined };
  return {
    limit: createStore<PreviewLimit>(25),
    appliedChoicePreview: { current: undefined },
    automaticPreviewGeneration: { current: 0 },
    automaticPreviewRequest: { current: undefined },
    activeAutomaticPreview,
    cache: createAutomaticPreview(),
    cancelAutomaticPreview: () => activeAutomaticPreview.current?.cancel(),
  };
}

export function automaticPreviewIdentityFor(
  ownerKey: string,
  state: BuilderAuthoringState,
  previewLimit: PreviewLimit,
): AutomaticPreviewIdentity | undefined {
  const table = selectedTable(state);
  const { draftVersion, draftDigest, previewRequestVersion } = state;
  const { snapshotToken } = state.catalog;
  if (!(table && snapshotToken && draftVersion > 0 && draftDigest)) return undefined;
  const { outputId } = table;
  return {
    key: JSON.stringify([ownerKey, snapshotToken, draftVersion, draftDigest, previewRequestVersion, outputId, previewLimit]),
    ownerKey,
    snapshotToken,
    draftVersion,
    draftDigest,
    previewRequestVersion,
    outputId,
    limit: previewLimit,
  };
}

type AutomaticPreviewQueryOptions = {
  readonly previewDisabled: boolean;
  readonly pendingCommands: number;
  readonly firstTableProgress: { readonly kind: 'idle' | 'running' };
  readonly reconcileCurrent: (owner?: AutomaticPreviewExecution, outputId?: string) => Promise<ExplorerBuilderCompileResult | undefined>;
  readonly executePreview: (request: PreviewRequest, receipt: ExplorerBuilderCompileResult, owner?: AutomaticPreviewExecution) => Promise<{ readonly receipt: ExplorerBuilderCompileResult; readonly preview: ExplorerBuilderPreviewResult } | undefined>;
};

type WorkflowPorts = {
  readonly projectId: string;
  readonly draftAccess: { getState(): BuilderAuthoringState };
  readonly compileRequests: ReconcileRequestSlot;
  readonly previewRequests: ReconcileRequestSlot;
  readonly dispatch: (action: Parameters<typeof builderAuthoringReducer>[1]) => void;
  readonly setMessage: (message: string | undefined) => void;
};

export function bindAutomaticPreview(
  { activeAutomaticPreview, automaticPreviewRequest, automaticPreviewGeneration, appliedChoicePreview, cache }: AutomaticPreviewWorkflow,
  { projectId, draftAccess, compileRequests, previewRequests, dispatch, setMessage }: WorkflowPorts,
) {
  const request = automaticPreviewRequest.current;
  return function previewOptions({
    previewDisabled,
    pendingCommands,
    firstTableProgress,
    reconcileCurrent,
    executePreview,
  }: AutomaticPreviewQueryOptions): AutomaticPreviewOptions {
    return {
      owner: cache,
      request,
      enabled: !previewDisabled && pendingCommands === 0 && firstTableProgress.kind === 'idle',
      refresh: async (identity, signal, options): Promise<AutomaticPreviewRun | undefined> => {
        activeAutomaticPreview.current?.cancel();
        const generation = ++automaticPreviewGeneration.current;
        const execution: AutomaticPreviewExecution = { cancelled: false };
        const cancel = () => {
          if (automaticPreviewGeneration.current !== generation) return;
          automaticPreviewGeneration.current += 1;
          execution.cancelled = true;
          if (activeAutomaticPreview.current?.generation === generation) {
            activeAutomaticPreview.current = undefined;
          }
          if (execution.compileRequest && execution.compileGeneration === compileRequests.generation()) {
            compileRequests.advance();
          }
          if (execution.previewRequest && execution.previewGeneration === previewRequests.generation()) {
            previewRequests.advance();
          }
          execution.compileRequest?.abort();
          execution.previewRequest?.abort();
        };
        activeAutomaticPreview.current = { generation, cancel };
        signal.addEventListener('abort', cancel, { once: true });
        const identityIsCurrent = () => {
          const current = draftAccess.getState();
          return !signal.aborted &&
            automaticPreviewRequest.current?.key === identity.key &&
            sameDraftRevision({ snapshotToken: current.catalog.snapshotToken, draftVersion: current.draftVersion, draftDigest: current.draftDigest }, identity) &&
            current.previewRequestVersion === identity.previewRequestVersion &&
            selectedTable(current)?.outputId === identity.outputId;
        };
        const accept = (receipt: ExplorerBuilderCompileResult, preview: ExplorerBuilderPreviewResult) => {
          if (!identityIsCurrent() ||
            !sameCatalogSnapshot(receipt.snapshotToken, identity.snapshotToken) ||
            !receipt.outputs.some((output) => output.outputId === identity.outputId) ||
            preview.receiptId !== receipt.receiptId ||
            preview.outputId !== identity.outputId) return undefined;
          dispatch({
            type: 'previewAccepted',
            identity: {
              project: projectId,
              explorerId: draftAccess.getState().explorerId,
              snapshotToken: identity.snapshotToken,
              draftVersion: identity.draftVersion,
              draftDigest: identity.draftDigest,
              previewRequestVersion: identity.previewRequestVersion,
              outputId: identity.outputId,
            },
            receipt,
            preview,
          });
          setMessage(undefined);
          return { identity, receipt, preview };
        };
        try {
          if (!identityIsCurrent()) return undefined;
          const current = draftAccess.getState();
          const receipt = !options?.reload && current.receipt && current.reconciliation === 'resolved' &&
            sameCatalogSnapshot(current.receipt.snapshotToken, identity.snapshotToken) &&
            current.receipt.outputs.some((output) => output.outputId === identity.outputId)
            ? current.receipt
            : await reconcileCurrent(execution, identity.outputId);
          if (execution.cancelled || !receipt || !identityIsCurrent()) return undefined;
          const candidate = appliedChoicePreview.current;
          appliedChoicePreview.current = undefined;
          if (matchesAcceptedPreviewHandoff(candidate, {
            ownerKey: identity.ownerKey,
            draftVersion: identity.draftVersion,
            previewRequestVersion: identity.previewRequestVersion,
            outputId: identity.outputId,
            limit: identity.limit,
            snapshotToken: identity.snapshotToken,
            receipt,
          })) return accept(receipt, candidate.preview);
          const completed = await executePreview({
            outputId: identity.outputId,
            limit: identity.limit,
            receiptRefreshes: 0,
          }, receipt, execution);
          if (execution.cancelled || !completed || !identityIsCurrent()) return undefined;
          return accept(completed.receipt, completed.preview);
        } catch (error) {
          if (!identityIsCurrent()) return undefined;
          const apiError = error as ExplorerAuthoringApiError;
          const suffix = apiError.code ? ` (${apiError.code})` : '';
          setMessage(`Preview failed: ${apiError.message}${suffix}`);
          throw error;
        } finally {
          signal.removeEventListener('abort', cancel);
          if (activeAutomaticPreview.current?.generation === generation) {
            activeAutomaticPreview.current = undefined;
          }
        }
      },
    };
  }
}
