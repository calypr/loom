import { useCallback, useRef, useState, type Dispatch, type MutableRefObject, type SetStateAction } from 'react';
import type { ExplorerAuthoringApiError } from '../../../client/api';
import type { ExplorerBuilderCompileResult, ExplorerBuilderPreviewResult } from '../../../contracts/types';
import { selectedTable, type BuilderAuthoringState } from '../model/authoring';
import type { builderAuthoringReducer } from '../model/reducer';
import type { PreviewLimit } from '../model/previewRecovery';
import type { useAutomaticPreview, AutomaticPreviewIdentity, AutomaticPreviewRun } from '../useAutomaticPreview';
import { matchesAcceptedPreviewHandoff, type AppliedPreviewHandoff } from '../constructionWorkspace/appliedChoicePreview';

export type PreviewRequest = {
  readonly outputId: string;
  readonly limit: PreviewLimit;
  readonly receiptRefreshes: number;
};

export type AutomaticPreviewExecution = {
  cancelled: boolean;
  compileGeneration?: number;
  compileRequest?: { readonly abort: () => void };
  previewGeneration?: number;
  previewRequest?: { readonly abort: () => void };
};

type UseAutomaticPreviewOwnerOptions = {
  readonly ownerKey: string;
  readonly projectId: string;
  readonly state: BuilderAuthoringState;
  readonly draftAccess: { getState(): BuilderAuthoringState };
  readonly compileGeneration: MutableRefObject<number>;
  readonly previewGeneration: MutableRefObject<number>;
  readonly dispatch: Dispatch<Parameters<typeof builderAuthoringReducer>[1]>;
  readonly setMessage: Dispatch<SetStateAction<string | undefined>>;
};

type AutomaticPreviewQueryOptions = {
  readonly previewDisabled: boolean;
  readonly pendingCommands: number;
  readonly firstTableProgress: { readonly kind: 'idle' | 'running' };
  readonly reconcileCurrent: (owner?: AutomaticPreviewExecution, outputId?: string) => Promise<ExplorerBuilderCompileResult | undefined>;
  readonly executePreview: (request: PreviewRequest, receipt: ExplorerBuilderCompileResult, owner?: AutomaticPreviewExecution) => Promise<{ readonly receipt: ExplorerBuilderCompileResult; readonly preview: ExplorerBuilderPreviewResult } | undefined>;
};

export const useAutomaticPreviewOwner = ({
  ownerKey,
  projectId,
  state,
  draftAccess,
  compileGeneration,
  previewGeneration,
  dispatch,
  setMessage,
}: UseAutomaticPreviewOwnerOptions) => {
  const [previewLimit, setPreviewLimit] = useState<PreviewLimit>(25);
  const appliedChoicePreview = useRef<AppliedPreviewHandoff | undefined>(undefined);
  const automaticPreviewGeneration = useRef(0);
  const activeAutomaticPreview = useRef<{ readonly generation: number; readonly execution: AutomaticPreviewExecution; readonly cancel: () => void } | undefined>(undefined);
  const automaticPreviewRequest = useRef<AutomaticPreviewIdentity | undefined>(undefined);
  const cancelAutomaticPreview = useCallback(() => {
    activeAutomaticPreview.current?.cancel();
  }, []);
  const table = selectedTable(state);
  const automaticPreviewIdentity: AutomaticPreviewIdentity | undefined =
    table && state.catalog.snapshotToken && state.draftVersion > 0 && state.draftDigest
      ? {
          key: JSON.stringify([
            ownerKey,
            state.catalog.snapshotToken,
            state.draftVersion,
            state.draftDigest,
            state.previewRequestVersion,
            table.outputId,
            previewLimit,
          ]),
          ownerKey,
          snapshotToken: state.catalog.snapshotToken,
          draftVersion: state.draftVersion,
          draftDigest: state.draftDigest,
          previewRequestVersion: state.previewRequestVersion,
          outputId: table.outputId,
          limit: previewLimit,
        }
      : undefined;
  automaticPreviewRequest.current = automaticPreviewIdentity;
  const queryOptionsFor = ({
    previewDisabled,
    pendingCommands,
    firstTableProgress,
    reconcileCurrent,
    executePreview,
  }: AutomaticPreviewQueryOptions): Parameters<typeof useAutomaticPreview>[0] => ({
    request: automaticPreviewIdentity,
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
        if (execution.compileRequest && execution.compileGeneration === compileGeneration.current) {
          compileGeneration.current += 1;
        }
        if (execution.previewRequest && execution.previewGeneration === previewGeneration.current) {
          previewGeneration.current += 1;
        }
        execution.compileRequest?.abort();
        execution.previewRequest?.abort();
      };
      activeAutomaticPreview.current = { generation, execution, cancel };
      signal.addEventListener('abort', cancel, { once: true });
      const identityIsCurrent = () => {
        const current = draftAccess.getState();
        return !signal.aborted &&
          automaticPreviewRequest.current?.key === identity.key &&
          current.catalog.snapshotToken === identity.snapshotToken &&
          current.draftVersion === identity.draftVersion &&
          current.draftDigest === identity.draftDigest &&
          current.previewRequestVersion === identity.previewRequestVersion &&
          selectedTable(current)?.outputId === identity.outputId;
      };
      const accept = (receipt: ExplorerBuilderCompileResult, preview: ExplorerBuilderPreviewResult) => {
        if (!identityIsCurrent() ||
          receipt.snapshotToken !== identity.snapshotToken ||
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
          current.receipt.snapshotToken === identity.snapshotToken &&
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
  });

  return { previewLimit, setPreviewLimit, appliedChoicePreview, automaticPreviewGeneration, automaticPreviewRequest, cancelAutomaticPreview, queryOptionsFor };
};
