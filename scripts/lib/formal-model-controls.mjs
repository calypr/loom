import { acceptanceModelControls } from './formal-reconcile-replay.mjs';
export const modelControls = {
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
