import { exclusionModelControls } from './formal-exclusion-replay.mjs';
import { draftTransactionModelControls } from './formal-draft-transaction.mjs';
import { removalModelControls } from './formal-removal-replay.mjs';
import { publicationModelControls } from './formal-publication-replay.mjs';
import { populationModelControls } from './formal-population-replay.mjs';
import { constructionModelControls } from './formal-construction-replay.mjs';
import { acceptanceModelControls } from './formal-reconcile-replay.mjs';
export const modelControls = {
    PopulationExclusion: exclusionModelControls,
    DraftSaveTransaction: draftTransactionModelControls,
    PopulationRemoval: removalModelControls,
    PublicationAcknowledgement: publicationModelControls,
    PopulationMapping: populationModelControls,
    ConstructionChain: constructionModelControls,
    ReconcileRefresh: [
        ['Inv_ObsoleteRecoveryDoesNotResume', 'ResumeRecovery', 'admitted == CurrentRequest /\\ workspaceAttached /\\ ~cancelled', 'admitted == workspaceAttached /\\ ~cancelled'],
        ['Inv_ObsoleteRecoveryDoesNotResume', 'ResumeRecovery', 'admitted == CurrentRequest /\\ workspaceAttached /\\ ~cancelled', 'admitted == CurrentRequest /\\ ~cancelled'],
    ],
    ReconcileAcceptance: acceptanceModelControls,
    ApplySettlementRace: [
        ["Inv_CompletionOwnsCurrentContext", "ApplyReturns", "ownerContext[owner] = currentContext THEN owner ELSE None", "TRUE THEN owner ELSE None"],
        ["Inv_ApplyReturnSettlesItsOwnOwner", "ApplyReturns", "settledOwner' = IF applyingOwner = owner /\\ currentOwner = owner /\\ ownerContext[owner] = currentContext THEN owner ELSE None", "settledOwner' = applyingOwner"],
        ["Inv_OldReturnPreservesCurrentApply", "ApplyReturns", "applyingOwner' = IF applyingOwner = owner THEN None ELSE applyingOwner", "applyingOwner' = None"],
    ],
    BuilderLifecycle: [
        ["Inv_ReadyProposalIsCurrent", "ProposalCompletesCurrent", "/\\ proposalEpoch = epoch", "/\\ TRUE"],
        ["Inv_ReadyPreviewMatchesProposal", "PreviewCompletesCurrent", "/\\ proposalReady", "/\\ TRUE"],
        ["Inv_ApplyStartedFromReviewedCurrentPreview", "BeginApply", "/\\ previewReady", "/\\ TRUE"],
        ["Inv_ApplySettlementTargetsInitiatingOwner", "ApplySettles", "lastSettlementOwner' = applyOwner", "lastSettlementOwner' = currentIdentity"],
        ["Inv_DiscoveryAdoptedOnlyForCurrentIdentity", "DiscoveryCompletesCurrent", "/\\ discoveryEpoch = discoveryGeneration", "/\\ TRUE"],
    ],
    ReconcileLifecycle: [
        ["Inv_AcceptedReceiptIsCurrent", "ResponseArrives", "receiptGeneration' = IF accepted THEN requestGeneration ELSE 0", "receiptGeneration' = IF accepted THEN (requestGeneration + 1) % 3 ELSE 0"],
        ["Inv_AcceptedReceiptMatchesRequest", "ResponseArrives", "receiptSnapshot' = IF accepted THEN incomingSnapshot ELSE 0", "receiptSnapshot' = IF accepted THEN (incomingSnapshot + 1) % 3 ELSE 0"],
        ["Inv_ReceiptSelectsRequestedOutput", "ResponseArrives", "receiptOutput' = IF accepted THEN incomingOutput ELSE 0", "receiptOutput' = IF accepted THEN (incomingOutput + 1) % 3 ELSE 0"],
        ["Inv_NoNewAcceptanceOnFailure", "ResponseArrives", "newAcceptance' = accepted", "newAcceptance' = TRUE"],
        ["Inv_RetryBounds", "Retry", "retryCount + staleRefreshCount < MaxRetry", "retryCount <= MaxRetry"],
    ],
};
