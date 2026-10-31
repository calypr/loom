---------------------------- MODULE BuilderLifecycle ----------------------------
EXTENDS Naturals, TLC

(* A bounded model of the proposal -> preview -> apply lifecycle.             *)
(* Render identity switches hide an owner; explicit cancellation or a new    *)
(* request changes its generation. A brief A -> B -> A can reuse that owner.  *)

Identities == {"A", "B"}
Epochs == 0..2
NoOwner == "none"
Owners == Identities \cup {NoOwner}

VARIABLES
  currentIdentity, epoch, discoveryGeneration,
  proposalOwner, proposalEpoch, proposalReady,
  previewOwner, previewEpoch, previewReady,
  applyOwner, applyEpoch, applyInFlight,
  applyProposalWasReady, applyPreviewWasReady,
  lastApplyOwner, lastSettlementOwner,
  discoveryOwner, discoveryEpoch, discoveryReady, adoptedDiscoveryOwner,
  lastAction

vars == <<
  currentIdentity, epoch, discoveryGeneration,
  proposalOwner, proposalEpoch, proposalReady,
  previewOwner, previewEpoch, previewReady,
  applyOwner, applyEpoch, applyInFlight,
  applyProposalWasReady, applyPreviewWasReady,
  lastApplyOwner, lastSettlementOwner,
  discoveryOwner, discoveryEpoch, discoveryReady, adoptedDiscoveryOwner,
  lastAction
>>

Init ==
  /\ currentIdentity = "A"
  /\ epoch = 0
  /\ discoveryGeneration = 0
  /\ proposalOwner = NoOwner
  /\ proposalEpoch = 0
  /\ proposalReady = FALSE
  /\ previewOwner = NoOwner
  /\ previewEpoch = 0
  /\ previewReady = FALSE
  /\ applyOwner = NoOwner
  /\ applyEpoch = 0
  /\ applyInFlight = FALSE
  /\ applyProposalWasReady = FALSE
  /\ applyPreviewWasReady = FALSE
  /\ lastApplyOwner = NoOwner
  /\ lastSettlementOwner = NoOwner
  /\ discoveryOwner = NoOwner
  /\ discoveryEpoch = 0
  /\ discoveryReady = FALSE
  /\ adoptedDiscoveryOwner = NoOwner
  /\ lastAction = "Init"

ChangeIdentity ==
  /\ currentIdentity' = IF currentIdentity = "A" THEN "B" ELSE "A"
  /\ proposalReady' = FALSE
  /\ previewReady' = FALSE
  /\ discoveryReady' = FALSE
  /\ adoptedDiscoveryOwner' = NoOwner
  /\ lastAction' = "ChangeIdentity"
  /\ UNCHANGED <<
       epoch, discoveryGeneration, proposalOwner, proposalEpoch, previewOwner, previewEpoch,
       applyOwner, applyEpoch, applyInFlight,
       applyProposalWasReady, applyPreviewWasReady,
       lastApplyOwner, lastSettlementOwner,
       discoveryOwner, discoveryEpoch
     >>

StartProposal ==
  /\ epoch < 2
  /\ epoch' = epoch + 1
  /\ proposalOwner' = currentIdentity
  /\ proposalEpoch' = epoch + 1
  /\ proposalReady' = FALSE
  /\ previewReady' = FALSE
  /\ lastAction' = "StartProposal"
  /\ UNCHANGED <<
       currentIdentity, discoveryGeneration, previewOwner, previewEpoch,
       applyOwner, applyEpoch, applyInFlight,
       applyProposalWasReady, applyPreviewWasReady,
       lastApplyOwner, lastSettlementOwner,
       discoveryOwner, discoveryEpoch, discoveryReady, adoptedDiscoveryOwner
     >>

ProposalCompletesCurrent ==
  /\ proposalOwner = currentIdentity
  /\ proposalEpoch = epoch
  /\ proposalReady' = TRUE
  /\ lastAction' = "ProposalCompletesCurrent"
  /\ UNCHANGED <<
       currentIdentity, epoch, discoveryGeneration, proposalOwner, proposalEpoch,
       previewOwner, previewEpoch, previewReady,
       applyOwner, applyEpoch, applyInFlight,
       applyProposalWasReady, applyPreviewWasReady,
       lastApplyOwner, lastSettlementOwner,
       discoveryOwner, discoveryEpoch, discoveryReady, adoptedDiscoveryOwner
     >>

StaleProposalIgnored ==
  /\ proposalOwner # NoOwner
  /\ \/ proposalOwner # currentIdentity
     \/ proposalEpoch # epoch
  /\ lastAction' = "StaleProposalIgnored"
  /\ UNCHANGED <<
       currentIdentity, epoch, discoveryGeneration, proposalOwner, proposalEpoch, proposalReady,
       previewOwner, previewEpoch, previewReady,
       applyOwner, applyEpoch, applyInFlight,
       applyProposalWasReady, applyPreviewWasReady,
       lastApplyOwner, lastSettlementOwner,
       discoveryOwner, discoveryEpoch, discoveryReady, adoptedDiscoveryOwner
     >>

StartPreview ==
  /\ proposalReady
  /\ proposalOwner = currentIdentity
  /\ proposalEpoch = epoch
  /\ previewOwner' = proposalOwner
  /\ previewEpoch' = proposalEpoch
  /\ previewReady' = FALSE
  /\ lastAction' = "StartPreview"
  /\ UNCHANGED <<
       currentIdentity, epoch, discoveryGeneration, proposalOwner, proposalEpoch, proposalReady,
       applyOwner, applyEpoch, applyInFlight,
       applyProposalWasReady, applyPreviewWasReady,
       lastApplyOwner, lastSettlementOwner,
       discoveryOwner, discoveryEpoch, discoveryReady, adoptedDiscoveryOwner
     >>

PreviewCompletesCurrent ==
  /\ previewOwner = currentIdentity
  /\ previewEpoch = epoch
  /\ proposalReady
  /\ previewOwner = proposalOwner
  /\ previewEpoch = proposalEpoch
  /\ previewReady' = TRUE
  /\ lastAction' = "PreviewCompletesCurrent"
  /\ UNCHANGED <<
       currentIdentity, epoch, discoveryGeneration, proposalOwner, proposalEpoch, proposalReady,
       previewOwner, previewEpoch,
       applyOwner, applyEpoch, applyInFlight,
       applyProposalWasReady, applyPreviewWasReady,
       lastApplyOwner, lastSettlementOwner,
       discoveryOwner, discoveryEpoch, discoveryReady, adoptedDiscoveryOwner
     >>

StalePreviewIgnored ==
  /\ previewOwner # NoOwner
  /\ \/ previewOwner # currentIdentity
     \/ previewEpoch # epoch
  /\ lastAction' = "StalePreviewIgnored"
  /\ UNCHANGED <<
       currentIdentity, epoch, discoveryGeneration, proposalOwner, proposalEpoch, proposalReady,
       previewOwner, previewEpoch, previewReady,
       applyOwner, applyEpoch, applyInFlight,
       applyProposalWasReady, applyPreviewWasReady,
       lastApplyOwner, lastSettlementOwner,
       discoveryOwner, discoveryEpoch, discoveryReady, adoptedDiscoveryOwner
     >>

BeginApply ==
  /\ ~applyInFlight
  /\ proposalReady
  /\ previewReady
  /\ proposalOwner = currentIdentity
  /\ proposalEpoch = epoch
  /\ previewOwner = proposalOwner
  /\ previewEpoch = proposalEpoch
  /\ applyOwner' = proposalOwner
  /\ applyEpoch' = proposalEpoch
  /\ applyInFlight' = TRUE
  /\ applyProposalWasReady' = proposalReady
  /\ applyPreviewWasReady' = previewReady
  /\ lastApplyOwner' = proposalOwner
  /\ lastAction' = "BeginApply"
  /\ UNCHANGED <<
       currentIdentity, epoch, discoveryGeneration, proposalOwner, proposalEpoch, proposalReady,
       previewOwner, previewEpoch, previewReady,
       lastSettlementOwner,
       discoveryOwner, discoveryEpoch, discoveryReady, adoptedDiscoveryOwner
     >>

ApplySettles ==
  /\ applyInFlight
  /\ lastSettlementOwner' = applyOwner
  /\ applyOwner' = NoOwner
  /\ applyInFlight' = FALSE
  /\ lastAction' = "ApplySettles"
  /\ UNCHANGED <<
       currentIdentity, epoch, discoveryGeneration, proposalOwner, proposalEpoch, proposalReady,
       previewOwner, previewEpoch, previewReady,
       applyEpoch, applyProposalWasReady, applyPreviewWasReady, lastApplyOwner,
       discoveryOwner, discoveryEpoch, discoveryReady, adoptedDiscoveryOwner
     >>

StartDiscovery ==
  /\ discoveryGeneration < 2
  /\ discoveryGeneration' = discoveryGeneration + 1
  /\ discoveryOwner' = currentIdentity
  /\ discoveryEpoch' = discoveryGeneration + 1
  /\ discoveryReady' = FALSE
  /\ adoptedDiscoveryOwner' = NoOwner
  /\ lastAction' = "StartDiscovery"
  /\ UNCHANGED <<
       currentIdentity, epoch, proposalOwner, proposalEpoch, proposalReady,
       previewOwner, previewEpoch, previewReady,
       applyOwner, applyEpoch, applyInFlight,
       applyProposalWasReady, applyPreviewWasReady,
       lastApplyOwner, lastSettlementOwner
     >>

DiscoveryCompletesCurrent ==
  /\ discoveryOwner = currentIdentity
  /\ discoveryEpoch = discoveryGeneration
  /\ discoveryReady' = TRUE
  /\ adoptedDiscoveryOwner' = discoveryOwner
  /\ lastAction' = "DiscoveryCompletesCurrent"
  /\ UNCHANGED <<
       currentIdentity, epoch, discoveryGeneration, proposalOwner, proposalEpoch, proposalReady,
       previewOwner, previewEpoch, previewReady,
       applyOwner, applyEpoch, applyInFlight,
       applyProposalWasReady, applyPreviewWasReady,
       lastApplyOwner, lastSettlementOwner,
       discoveryOwner, discoveryEpoch
     >>

StaleDiscoveryIgnored ==
  /\ discoveryOwner # NoOwner
  /\ \/ discoveryOwner # currentIdentity
     \/ discoveryEpoch # discoveryGeneration
  /\ lastAction' = "StaleDiscoveryIgnored"
  /\ UNCHANGED <<
       currentIdentity, epoch, discoveryGeneration, proposalOwner, proposalEpoch, proposalReady,
       previewOwner, previewEpoch, previewReady,
       applyOwner, applyEpoch, applyInFlight,
       applyProposalWasReady, applyPreviewWasReady,
       lastApplyOwner, lastSettlementOwner,
       discoveryOwner, discoveryEpoch, discoveryReady, adoptedDiscoveryOwner
     >>

CancelProposal ==
  /\ epoch < 2
  /\ epoch' = epoch + 1
  /\ proposalReady' = FALSE
  /\ previewReady' = FALSE
  /\ lastAction' = "CancelProposal"
  /\ UNCHANGED <<currentIdentity, discoveryGeneration,
       proposalOwner, proposalEpoch, previewOwner, previewEpoch,
       applyOwner, applyEpoch, applyInFlight,
       applyProposalWasReady, applyPreviewWasReady,
       lastApplyOwner, lastSettlementOwner,
       discoveryOwner, discoveryEpoch, discoveryReady, adoptedDiscoveryOwner>>

CancelDiscovery ==
  /\ discoveryGeneration < 2
  /\ discoveryGeneration' = discoveryGeneration + 1
  /\ discoveryReady' = FALSE
  /\ adoptedDiscoveryOwner' = NoOwner
  /\ lastAction' = "CancelDiscovery"
  /\ UNCHANGED <<currentIdentity, epoch,
       proposalOwner, proposalEpoch, proposalReady, previewOwner, previewEpoch, previewReady,
       applyOwner, applyEpoch, applyInFlight,
       applyProposalWasReady, applyPreviewWasReady,
       lastApplyOwner, lastSettlementOwner, discoveryOwner, discoveryEpoch>>

Next ==
  \/ ChangeIdentity
  \/ CancelProposal
  \/ CancelDiscovery
  \/ StartProposal
  \/ ProposalCompletesCurrent
  \/ StaleProposalIgnored
  \/ StartPreview
  \/ PreviewCompletesCurrent
  \/ StalePreviewIgnored
  \/ BeginApply
  \/ ApplySettles
  \/ StartDiscovery
  \/ DiscoveryCompletesCurrent
  \/ StaleDiscoveryIgnored

Spec == Init /\ [][Next]_vars

TypeOK ==
  /\ currentIdentity \in Identities
  /\ epoch \in Epochs
  /\ discoveryGeneration \in Epochs
  /\ proposalOwner \in Owners
  /\ proposalEpoch \in Epochs
  /\ proposalReady \in BOOLEAN
  /\ previewOwner \in Owners
  /\ previewEpoch \in Epochs
  /\ previewReady \in BOOLEAN
  /\ applyOwner \in Owners
  /\ applyEpoch \in Epochs
  /\ applyInFlight \in BOOLEAN
  /\ applyProposalWasReady \in BOOLEAN
  /\ applyPreviewWasReady \in BOOLEAN
  /\ lastApplyOwner \in Owners
  /\ lastSettlementOwner \in Owners
  /\ discoveryOwner \in Owners
  /\ discoveryEpoch \in Epochs
  /\ discoveryReady \in BOOLEAN
  /\ adoptedDiscoveryOwner \in Owners

Inv_ReadyProposalIsCurrent ==
  proposalReady =>
    /\ proposalOwner = currentIdentity
    /\ proposalEpoch = epoch

Inv_ReadyPreviewMatchesProposal ==
  previewReady =>
    /\ proposalReady
    /\ previewOwner = proposalOwner
    /\ previewEpoch = proposalEpoch

Inv_ApplyStartedFromReviewedCurrentPreview ==
  applyInFlight =>
    /\ applyOwner # NoOwner
    /\ applyProposalWasReady
    /\ applyPreviewWasReady

Inv_ApplySettlementTargetsInitiatingOwner ==
  lastAction = "ApplySettles" => lastSettlementOwner = lastApplyOwner

Inv_DiscoveryAdoptedOnlyForCurrentIdentity ==
  adoptedDiscoveryOwner # NoOwner =>
    /\ discoveryReady
    /\ adoptedDiscoveryOwner = currentIdentity
    /\ discoveryEpoch = discoveryGeneration

=============================================================================
