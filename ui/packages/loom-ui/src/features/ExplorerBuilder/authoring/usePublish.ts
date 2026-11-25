import { useCallback, useRef, useState, type Dispatch, type MutableRefObject, type SetStateAction } from 'react';
import { usePublishExplorerAuthoringV2Mutation, type useGetExplorerBuilderStateV2Query } from '../../../react';
import type { ExplorerAuthoringApiError } from '../../../client/api';
import type { ExplorerBuilderCompileResult, ExplorerBuilderState } from '../../../contracts/types';
import { intentFingerprint, workspaceFromState, type BuilderAuthoringState } from '../model/authoring';
import { builderAuthoringReducer } from '../model/reducer';
import { isStaleSnapshot, reconcileResultIsCurrent } from '../model/reconcileRecovery';
import { effectiveOutputAvailability } from '../model/outputEligibility';
import type { useConstructionLifecycle } from '../constructionWorkspace/useConstructionLifecycle';
import type { useRecipeEditingOwner } from '../session/recipeEditingOwner';

type PublicationAcknowledgementIdentity = {
  readonly ownerKey: string;
  readonly explorerId: string;
  readonly draftDigest: string;
  readonly intentFingerprint: string;
  readonly snapshotToken: string;
};

type UsePublishOptions = {
  readonly ownerKey: string;
  readonly projectId: string;
  readonly authResourcePath?: string;
  readonly state: BuilderAuthoringState;
  readonly draftAccess: { getState(): BuilderAuthoringState; setCurrentDraft(state: BuilderAuthoringState): void };
  readonly workspaceAttached: MutableRefObject<boolean>;
  readonly compileGeneration: MutableRefObject<number>;
  readonly dispatch: Dispatch<Parameters<typeof builderAuthoringReducer>[1]>;
  readonly reconcileCurrent: () => Promise<ExplorerBuilderCompileResult | undefined>;
  readonly refetchBuilder: ReturnType<typeof useGetExplorerBuilderStateV2Query>['refetch'];
  readonly syncBuilderData: (value: ExplorerBuilderState, mode: 'hydrate' | 'catalog') => void;
  readonly setMessage: Dispatch<SetStateAction<string | undefined>>;
  readonly incomplete: boolean;
  readonly blockingDiagnostics: boolean;
  readonly recipeOwner: Pick<ReturnType<typeof useRecipeEditingOwner>, 'state'>;
  readonly constructionLifecycle: Pick<ReturnType<typeof useConstructionLifecycle>, 'proposal'>;
};

export const usePublish = ({
  ownerKey,
  projectId,
  authResourcePath,
  state,
  draftAccess,
  workspaceAttached,
  compileGeneration,
  dispatch,
  reconcileCurrent,
  refetchBuilder,
  syncBuilderData,
  setMessage,
  incomplete,
  blockingDiagnostics,
  recipeOwner,
  constructionLifecycle,
}: UsePublishOptions) => {
  const publicationOwnerKeyRef = useRef(ownerKey);
  publicationOwnerKeyRef.current = ownerKey;
  const [lastPublished, setLastPublished] = useState<{
    readonly ownerKey: string;
    readonly draftDigest: string;
  }>();
  const [publishing, setPublishing] = useState(false);
  const [publishBuilder] = usePublishExplorerAuthoringV2Mutation();

  const publishDisabled =
    (lastPublished?.ownerKey === ownerKey &&
      lastPublished.draftDigest === state.draftDigest) ||
    incomplete ||
    blockingDiagnostics ||
    recipeOwner.state.status !== 'saved' ||
    constructionLifecycle.proposal.status !== 'idle' ||
    state.tables.some((candidate) =>
      !effectiveOutputAvailability(candidate.document).hasAnyOutputColumn,
    );

  const executePublish = useCallback(
    async (receiptId: string) => {
      let activeReceiptId = receiptId;
      let refreshes = 0;
      const publicationOwnerIsCurrent = () =>
        workspaceAttached.current && publicationOwnerKeyRef.current === ownerKey;
      for (;;) {
        if (!publicationOwnerIsCurrent()) return;
        const submitted = draftAccess.getState();
        const generation = compileGeneration.current;
        const submittedFingerprint = intentFingerprint(workspaceFromState(submitted));
        const publicationIsCurrent = () => publicationOwnerIsCurrent() && reconcileResultIsCurrent({
          generation,
          currentGeneration: compileGeneration.current,
          submittedExplorerId: submitted.explorerId,
          currentExplorerId: draftAccess.getState().explorerId,
          submittedFingerprint,
          currentFingerprint: intentFingerprint(workspaceFromState(draftAccess.getState())),
        });
        const acknowledgementIdentity: PublicationAcknowledgementIdentity = {
          ownerKey,
          explorerId: submitted.explorerId,
          draftDigest: submitted.draftDigest,
          intentFingerprint: submittedFingerprint,
          snapshotToken: submitted.catalog.snapshotToken,
        };
        const acknowledgementIsCurrent = () =>
          publicationOwnerIsCurrent() &&
          publicationOwnerKeyRef.current === acknowledgementIdentity.ownerKey &&
          draftAccess.getState().explorerId === acknowledgementIdentity.explorerId &&
          draftAccess.getState().draftDigest === acknowledgementIdentity.draftDigest &&
          draftAccess.getState().catalog.snapshotToken === acknowledgementIdentity.snapshotToken &&
          intentFingerprint(workspaceFromState(draftAccess.getState())) === acknowledgementIdentity.intentFingerprint;
        try {
          await publishBuilder({
            project: projectId,
            explorerId: draftAccess.getState().explorerId,
            authResourcePath,
            receiptId: activeReceiptId,
          }).unwrap();
          if (!acknowledgementIsCurrent()) return;
          setLastPublished({
            ownerKey,
            draftDigest: submitted.draftDigest,
          });
          dispatch({ type: 'published' });
          setMessage(undefined);
          return;
        } catch (error) {
          if (!publicationOwnerIsCurrent()) return;
          const apiError = error as ExplorerAuthoringApiError;
          if (isStaleSnapshot(apiError.code) && refreshes === 0) {
            if (!publicationIsCurrent()) return;
            refreshes += 1;
            const refreshed = await refetchBuilder({ reload: true });
            if (!refreshed.data || !publicationIsCurrent()) return;
            draftAccess.setCurrentDraft(builderAuthoringReducer(draftAccess.getState(), {
              type: 'catalogRefreshed',
              catalog: refreshed.data.catalog,
            }));
            syncBuilderData(refreshed.data, 'catalog');
            const receipt = await reconcileCurrent();
            if (!receipt || !publicationOwnerIsCurrent()) return;
            activeReceiptId = receipt.receiptId;
            continue;
          }
          const suffix = apiError.code ? ` (${apiError.code})` : '';
          setMessage(
            apiError.code === 'PUBLICATION_STATUS_UNKNOWN'
              ? apiError.message
              : `Publication failed; the previously active Viewer revision remains available: ${apiError.message}${suffix}`,
          );
          return;
        }
      }
    },
    [
      authResourcePath,
      dispatch,
      ownerKey,
      projectId,
      publishBuilder,
      reconcileCurrent,
      refetchBuilder,
      syncBuilderData,
    ],
  );

  const publish = async () => {
    if (publishDisabled || publishing) return;
    setPublishing(true);
    try {
      const receiptCoversEveryTable = state.receipt !== undefined &&
        state.tables.every((candidate) => state.receipt?.outputs.some(
          (output) => output.outputId === candidate.outputId,
        ));
      const receipt =
        state.receipt && state.reconciliation === 'resolved' && receiptCoversEveryTable
          ? state.receipt
          : await reconcileCurrent();
      if (!receipt) return;
      const publishable = state.tables.every((candidate) =>
        receipt.outputs.some(
          (output) =>
            output.outputId === candidate.outputId && output.columns.length > 0,
        ),
      );
      if (!publishable) {
        setMessage(
          'Every table needs at least one output column before publishing.',
        );
        return;
      }
      await executePublish(receipt.receiptId);
    } finally {
      setPublishing(false);
    }
  };

  return { publishing, publishDisabled, publish };
};
