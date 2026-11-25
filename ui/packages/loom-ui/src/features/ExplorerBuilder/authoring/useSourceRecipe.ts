import { useRef, useState, type Dispatch, type MutableRefObject, type SetStateAction } from 'react';
import type { useLoomClient } from '../../../react';
import type { AuthoringNetworkResponse, ExplorerBuilderCommand, ExplorerBuilderPreviewResult, RowDefinitionProposal, SourceRelationshipChange, SourceRelationshipProposal } from '../../../contracts/types';
import type { BuilderAuthoringState, DraftTable } from '../model/authoring';
import { createRecipeNetworkLoader } from '../session/recipeNetwork';
import type { useRecipeEditingOwner, RecipeEditToken } from '../session/recipeEditingOwner';
import type { useRecipeFocus } from './useRecipeFocus';
import type { useConstructionLifecycle } from '../constructionWorkspace/useConstructionLifecycle';
import type { useCommandQueue } from './useCommandQueue';

type SourceRecipeActionsOptions = {
  readonly builderDataKey: string;
  readonly state: BuilderAuthoringState;
  readonly table: DraftTable | undefined;
  readonly firstTableProgress: { readonly kind: 'idle' | 'running' };
  readonly loomClient: ReturnType<typeof useLoomClient>;
  readonly projectId: string;
  readonly authResourcePath?: string;
  readonly completePendingRecipeFocus: ReturnType<typeof useRecipeFocus>['completePendingRecipeFocus'];
  readonly recipeMutationAllowed: () => boolean;
  readonly rememberRecipeFocus: ReturnType<typeof useRecipeFocus>['rememberRecipeFocus'];
  readonly recipeOwner: ReturnType<typeof useRecipeEditingOwner>;
  readonly columnPolicyRequest: MutableRefObject<AbortController | undefined>;
  readonly setEditingRecipeColumn: Dispatch<SetStateAction<{ owner: string; columnId: string } | undefined>>;
  readonly constructionLifecycle: Pick<ReturnType<typeof useConstructionLifecycle>, 'cancel'>;
  readonly restoreRecipeFocus: ReturnType<typeof useRecipeFocus>['restoreRecipeFocus'];
  readonly recipeFocusGeneration: ReturnType<typeof useRecipeFocus>['recipeFocusGeneration'];
  readonly recipeInitiator: ReturnType<typeof useRecipeFocus>['recipeInitiator'];
  readonly recipeInitiatorFact: ReturnType<typeof useRecipeFocus>['recipeInitiatorFact'];
  readonly pendingRecipeFocus: ReturnType<typeof useRecipeFocus>['pendingRecipeFocus'];
  readonly applyCommands: ReturnType<typeof useCommandQueue>['applyCommands'];
};

type SourceProposalOwnerCandidate = Pick<SourceRelationshipProposal,
  'proposalId' | 'candidateDocument' | 'candidateNetwork' | 'candidateRowIdentity' |
  'candidateWorkspaceDigest' | 'candidateReceiptId' | 'outputId' | 'snapshotToken' |
  'draftVersion' | 'draftDigest'> &
  Partial<Pick<SourceRelationshipProposal, 'affectedColumns' | 'notices'>>;

export const useSourceRecipe = () => {
  const sourceRelationshipRequest = useRef<AbortController | undefined>(undefined);
  const networkRequestOwner = useRef('');
  const recipeEditToken = useRef<RecipeEditToken | undefined>(undefined);
  const [sourceProposal, setSourceProposal] = useState<{ owner: string; proposal: SourceProposalOwnerCandidate; preview: ExplorerBuilderPreviewResult }>();
  const lastAcceptedOutputPreview = useRef<{ readonly owner: string; readonly outputId: string; readonly snapshotToken: string; readonly draftVersion: number; readonly draftDigest: string; readonly receiptId: string } | undefined>(undefined);
  const [connectingRelationship, setConnectingRelationship] = useState(false);
  const [savedNetwork, setSavedNetwork] = useState<{ owner: string; result: AuthoringNetworkResponse }>();
  const [recipeNotice, setRecipeNotice] = useState<string>();
  const [relationshipEdit, setRelationshipEdit] = useState<{ owner: string; change: SourceRelationshipChange }>();
  const actionsFor = ({
    builderDataKey, state, table, firstTableProgress, loomClient, projectId, authResourcePath,
    completePendingRecipeFocus, recipeMutationAllowed, rememberRecipeFocus, recipeOwner,
    columnPolicyRequest, setEditingRecipeColumn, constructionLifecycle, restoreRecipeFocus,
    recipeFocusGeneration, recipeInitiator, recipeInitiatorFact, pendingRecipeFocus, applyCommands
  }: SourceRecipeActionsOptions) => {
    const relationshipOwner = JSON.stringify([builderDataKey, state.explorerId, table?.outputId,
      state.catalog.snapshotToken, state.draftVersion, state.draftDigest]);
    const loadRecipeNetwork = createRecipeNetworkLoader({
      firstTableProgress,
      table,
      networkRequestOwner,
      relationshipOwner,
      loomClient,
      projectId,
      state,
      authResourcePath,
      setSavedNetwork,
      completePendingRecipeFocus,
    });
    const pendingRelationship = relationshipEdit?.owner === relationshipOwner ? relationshipEdit.change : undefined;
    const proposeRelationship = (change: SourceRelationshipChange, prepareImmediately = false) => {
      if (!recipeMutationAllowed()) return;
      rememberRecipeFocus();
      const token = recipeOwner.begin(change.action === 'ADD'
        ? { kind: 'connection', parentOccurrenceId: change.parentOccurrenceId, catalogEdgeId: change.edgeId }
        : { kind: 'relationship', occurrenceId: change.occurrenceId });
      if (!token) return;
      recipeEditToken.current = token;
      setRecipeNotice(undefined);
      setSourceProposal(undefined);
      setRelationshipEdit({ owner: relationshipOwner, change });
      if (prepareImmediately) void prepareSourceRelationship(change);
    };
    const cancelRecipeRelationship = (restoreFocus = true) => {
      const editing = recipeOwner.getState();
      if (editing.status === 'applying') return false;
      const token = recipeEditToken.current;
      if (editing.status === 'stale') recipeOwner.discardStale();
      else if (token && !recipeOwner.cancel(token)) return false;
      sourceRelationshipRequest.current?.abort();
      sourceRelationshipRequest.current = undefined;
      setConnectingRelationship(false);
      columnPolicyRequest.current?.abort();
      recipeEditToken.current = undefined;
      setSourceProposal(undefined);
      setRelationshipEdit(undefined);
      setEditingRecipeColumn(undefined);
      setRecipeNotice(undefined);
      constructionLifecycle.cancel();
      if (restoreFocus) restoreRecipeFocus();
      else {
        recipeFocusGeneration.current += 1;
        recipeInitiator.current = undefined;
        recipeInitiatorFact.current = undefined;
        pendingRecipeFocus.current = undefined;
      }
      return true;
    };
    const sourceReviewStatus = (status: 'previewing' | 'applying' | 'failed', message?: string) => {
      const token = recipeEditToken.current;
      if (!token) return;
      if (status === 'previewing') recipeEditToken.current = recipeOwner.previewing(token);
      else if (status === 'failed') recipeOwner.fail(token, message ?? 'The proposal failed.');
    };
    const beginSourceReview = () => {
      const currentToken = recipeEditToken.current;
      if (!currentToken) return undefined;
      const attempt = recipeOwner.previewing(currentToken);
      if (!attempt) return undefined;
      recipeEditToken.current = attempt;
      setSourceProposal(undefined);
      return {
        ready: (proposal: SourceProposalOwnerCandidate, preview: ExplorerBuilderPreviewResult) => {
          if (proposal.outputId !== attempt.identity.outputId || proposal.snapshotToken !== attempt.identity.snapshotToken ||
            proposal.draftVersion !== attempt.identity.draftVersion || proposal.draftDigest !== attempt.identity.draftDigest) {
            recipeOwner.fail(attempt, 'The draft changed while the proposal was loading. Cancel and review the current draft.', true);
            return false;
          }
          const accepted = recipeOwner.ready(attempt, { receiptId: proposal.candidateReceiptId,
            candidateWorkspaceDigest: proposal.candidateWorkspaceDigest, preview,
            candidateDocument: proposal.candidateDocument, candidateNetwork: proposal.candidateNetwork,
            candidateRowIdentity: proposal.candidateRowIdentity });
          if (accepted) {
            lastAcceptedOutputPreview.current = { owner: relationshipOwner, outputId: attempt.identity.outputId,
              snapshotToken: attempt.identity.snapshotToken, draftVersion: attempt.identity.draftVersion,
              draftDigest: attempt.identity.draftDigest, receiptId: preview.receiptId };
            setSourceProposal({ owner: relationshipOwner, proposal, preview });
          }
          else recipeOwner.fail(attempt, 'The candidate preview does not belong to this recipe edit. Preview again or Cancel.');
          return accepted;
        },
        failed: (message: string) => { recipeOwner.fail(attempt, message); },
      };
    };
    const prepareSourceRelationship = (change: SourceRelationshipChange) => {
      const ownership = beginSourceReview();
      if (!ownership) {
        setConnectingRelationship(false);
        return;
      }
      sourceRelationshipRequest.current?.abort();
      const controller = new AbortController();
      sourceRelationshipRequest.current = controller;
      setConnectingRelationship(true);
      void (async () => {
        try {
          const proposal = await loomClient.proposeSourceRelationship({
            project: projectId,
            explorerId: state.explorerId,
            authResourcePath,
            snapshotToken: state.catalog.snapshotToken,
            expectedDraftVersion: state.draftVersion,
            expectedDraftDigest: state.draftDigest,
            outputId: table?.outputId ?? '',
            change,
          }, controller.signal);
          if (controller.signal.aborted) return;
          const preview = proposal.preview;
          if (preview.rows === null || preview.receiptId !== proposal.candidateReceiptId || preview.outputId !== proposal.outputId) {
            throw new Error('The proposal did not include a preview for its candidate receipt.');
          }
          setConnectingRelationship(false);
          ownership.ready(proposal, preview);
        } catch (cause) {
          if (!controller.signal.aborted) {
            setConnectingRelationship(false);
            ownership.failed(cause instanceof Error ? cause.message : 'The proposed relationship could not be previewed.');
          }
        } finally {
          if (sourceRelationshipRequest.current === controller) sourceRelationshipRequest.current = undefined;
        }
      })();
    };
    const beginSourceRowsReview = () => {
      const editing = recipeOwner.getState();
      if (editing.status === 'saved') {
        if (!recipeMutationAllowed()) return undefined;
        recipeEditToken.current = recipeOwner.begin({ kind: 'sourceRows' });
      } else if (editing.target?.kind !== 'sourceRows') {
        setRecipeNotice('Apply or Cancel the active recipe change before opening another row editor.');
        return undefined;
      }
      const attempt = beginSourceReview();
      return attempt ? { failed: attempt.failed, ready: (proposal: RowDefinitionProposal, preview: ExplorerBuilderPreviewResult) => proposal.proposalId
        ? attempt.ready({ ...proposal, proposalId: proposal.proposalId, candidateReceiptId: proposal.proposalId }, preview) : false } : undefined;
    };
    const applySourceRecipe = async (commands: ExplorerBuilderCommand[]) => {
      const token = recipeEditToken.current;
      if (!token || commands.some((command) => command.proposalId !== recipeOwner.state.candidate?.receiptId) || !recipeOwner.applying(token)) return false;
      const settleBeforeDraftPublish = () => {
        recipeOwner.applied(token);
        recipeEditToken.current = undefined;
        setSourceProposal(undefined);
        setRelationshipEdit(undefined);
        setEditingRecipeColumn(undefined);
        restoreRecipeFocus();
      };
      let applied = false;
      try { applied = await applyCommands(commands, undefined, undefined, settleBeforeDraftPublish); } catch (error) {
        recipeOwner.fail(token, error instanceof Error ? error.message : 'The proposal was not saved.'); return false;
      }
      if (!applied) recipeOwner.fail(token, 'The change was not saved. Review the current draft.');
      return applied;
    };

    return { relationshipOwner, loadRecipeNetwork, pendingRelationship, proposeRelationship, cancelRecipeRelationship, sourceReviewStatus, beginSourceReview, beginSourceRowsReview, applySourceRecipe };
  };
  return { sourceRelationshipRequest, recipeEditToken, sourceProposal, setSourceProposal,
    lastAcceptedOutputPreview, connectingRelationship, setConnectingRelationship, savedNetwork,
    recipeNotice, setRecipeNotice, setRelationshipEdit, actionsFor };
};
