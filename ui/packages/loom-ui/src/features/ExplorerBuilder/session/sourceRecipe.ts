import { sameDraftRevision } from '../../../contracts/identity';
import { RequestOwner } from '../../../data/requests';
import type { SourceProposalRequest } from './useSourceRelationshipReview';
import { useRef, useState, type Dispatch, type MutableRefObject, type SetStateAction } from 'react';
import type { useLoomClient } from '../../../react';
import type { AuthoringNetworkResponse, ExplorerBuilderColumn, ExplorerBuilderCommand, ExplorerBuilderPreviewResult, RowDefinitionProposal, SourceRelationshipChange, SourceRelationshipProposal } from '../../../contracts/types';
import type { BuilderAuthoringState, DraftTable } from '../model/authoring';
import { createRecipeNetworkLoader } from './recipeNetwork';
import type { useRecipeEditingOwner } from './useRecipeEditingOwner';
import type { RecipeEditToken } from './recipeEditingOwner';
import type { useRecipeFocus } from './useRecipeFocus';
import type { useConstructionLifecycle } from './useConstructionLifecycle';
import type { useCommandQueueActions } from './useCommandQueueActions';

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
  readonly setEditingRecipeColumn: Dispatch<SetStateAction<{ owner: string; columnId: string } | undefined>>;
  readonly constructionLifecycle: Pick<ReturnType<typeof useConstructionLifecycle>, 'cancel'>;
  readonly restoreRecipeFocus: ReturnType<typeof useRecipeFocus>['restoreRecipeFocus'];
  readonly recipeFocusGeneration: ReturnType<typeof useRecipeFocus>['recipeFocusGeneration'];
  readonly recipeInitiator: ReturnType<typeof useRecipeFocus>['recipeInitiator'];
  readonly recipeInitiatorFact: ReturnType<typeof useRecipeFocus>['recipeInitiatorFact'];
  readonly pendingRecipeFocus: ReturnType<typeof useRecipeFocus>['pendingRecipeFocus'];
  readonly applyCommands: ReturnType<typeof useCommandQueueActions>['applyCommands'];
};

type SourceProposalOwnerCandidate = Pick<SourceRelationshipProposal,
  'proposalId' | 'candidateDocument' | 'candidateNetwork' | 'candidateRowIdentity' |
  'candidateWorkspaceDigest' | 'candidateReceiptId' | 'outputId' | 'snapshotToken' |
  'draftVersion' | 'draftDigest'> &
  Partial<Pick<SourceRelationshipProposal, 'affectedColumns' | 'notices'>>;

export const sourceRecipeOwnerKey = (builderDataKey: string, state: BuilderAuthoringState, outputId?: string) =>
  JSON.stringify([builderDataKey, state.explorerId, outputId,
    state.catalog.snapshotToken, state.draftVersion, state.draftDigest]);

export const useSourceRecipeState = () => {
  const sourceRelationshipRequest = useRef<SourceProposalRequest | undefined>(undefined);
  const [sourceRecipeRequests] = useState(() => {
    const owner = new RequestOwner();
    owner.setIdentity('source-recipe');
    return owner;
  });
  const networkRequestOwner = useRef('');
  const recipeEditToken = useRef<RecipeEditToken | undefined>(undefined);
  const [sourceProposal, setSourceProposal] = useState<{ owner: string; proposal: SourceProposalOwnerCandidate; preview: ExplorerBuilderPreviewResult }>();
  const lastAcceptedOutputPreview = useRef<{ readonly owner: string; readonly outputId: string; readonly snapshotToken: string; readonly draftVersion: number; readonly draftDigest: string; readonly receiptId: string } | undefined>(undefined);
  const [connectingRelationship, setConnectingRelationship] = useState(false);
  const [savedNetwork, setSavedNetwork] = useState<{ owner: string; result: AuthoringNetworkResponse }>();
  const [recipeNotice, setRecipeNotice] = useState<string>();
  const [relationshipEdit, setRelationshipEdit] = useState<{ owner: string; change: SourceRelationshipChange }>();
  return {
    sourceRelationshipRequest, sourceRecipeRequests, recipeEditToken, sourceProposal, setSourceProposal,
    lastAcceptedOutputPreview, connectingRelationship, setConnectingRelationship, savedNetwork,
    recipeNotice, setRecipeNotice, setRelationshipEdit, networkRequestOwner,
    setSavedNetwork, relationshipEdit,
  };
};

export function createSourceRecipeActions(
  {
    sourceRelationshipRequest, sourceRecipeRequests, networkRequestOwner, recipeEditToken, setSourceProposal,
    lastAcceptedOutputPreview, setConnectingRelationship, setSavedNetwork, setRecipeNotice,
    relationshipEdit, setRelationshipEdit,
  }: ReturnType<typeof useSourceRecipeState>,
  {
    builderDataKey, state, table, firstTableProgress, loomClient, projectId, authResourcePath,
    completePendingRecipeFocus, recipeMutationAllowed, rememberRecipeFocus, recipeOwner,
    setEditingRecipeColumn, constructionLifecycle, restoreRecipeFocus,
    recipeFocusGeneration, recipeInitiator, recipeInitiatorFact, pendingRecipeFocus, applyCommands
  }: SourceRecipeActionsOptions) {
  const relationshipOwner = sourceRecipeOwnerKey(builderDataKey, state, table?.outputId);
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
    sourceRelationshipRequest.current?.cancel();
    sourceRelationshipRequest.current = undefined;
    setConnectingRelationship(false);
    sourceRecipeRequests.cancel('column-policy');
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
        if (proposal.outputId !== attempt.identity.outputId || !sameDraftRevision(proposal, attempt.identity)) {
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
    sourceRelationshipRequest.current?.cancel();
    const controller = sourceRecipeRequests.start('relationship', 'source-recipe');
    if (!controller) return;
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
        controller.finish();
        if (sourceRelationshipRequest.current === controller) sourceRelationshipRequest.current = undefined;
      }
    })();
  };
  const proposeColumnPolicy = (column: ExplorerBuilderColumn, columnValue: ExplorerBuilderColumn) => {
    if (!table) return;
    if (columnValue.source.kind !== 'field' || !columnValue.source.field.projectionMode) return;
    const attempt = beginSourceReview();
    if (!attempt) return;
    const controller = sourceRecipeRequests.start('column-policy', 'source-recipe');
    if (!controller) return;
    void (async () => {
      try {
        const proposal = await loomClient.proposeColumnPolicy({ project: projectId, explorerId: state.explorerId,
          authResourcePath, snapshotToken: state.catalog.snapshotToken, expectedDraftVersion: state.draftVersion,
          expectedDraftDigest: state.draftDigest, outputId: table.outputId, columnId: column.columnId ?? column.column,
          projectionMode: columnValue.source.kind === 'field' ? columnValue.source.field.projectionMode! : 'VALUE' }, controller.signal);
        if (controller.signal.aborted) return;
        const preview = await loomClient.preview({ project: projectId, explorerId: state.explorerId, authResourcePath,
          receiptId: proposal.candidateReceiptId, outputId: table.outputId }, controller.signal);
        if (controller.signal.aborted) return;
        attempt.ready(proposal, preview);
      } catch (error) { if (!controller.signal.aborted) attempt.failed(error instanceof Error ? error.message : 'The column policy could not be previewed.'); }
      finally { controller.finish(); }
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

  return { relationshipOwner, loadRecipeNetwork, pendingRelationship, proposeRelationship, cancelRecipeRelationship, sourceReviewStatus, beginSourceReview, proposeColumnPolicy, beginSourceRowsReview, applySourceRecipe };
}
