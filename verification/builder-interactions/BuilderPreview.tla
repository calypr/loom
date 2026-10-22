---------------- MODULE BuilderPreview ----------------
EXTENDS Naturals, FiniteSets, Sequences, TLC

(*
  Bounded asynchronous-state model for the Builder proposal, preview, Apply,
  and Pivot-category-discovery lifecycle.

  The model is separate from BuilderOperations.tla.  BuilderOperations owns
  operation and data-shape combinations; this module owns the interleavings of
  request owners, stale results, cancellation, replacement, Apply completion,
  and key-scoped category discovery.

  Source map:
    ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.ts
    ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx
    ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/ConstructionProposalPanel.tsx
    ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx
    ui/packages/loom-ui/src/react.tsx
    internal/explorer/lifecycle/construction_category_discovery.go

  Bound: two accepted-draft identities and three proposal generations.  TLC
  explores every reachable interleaving in that bound.
*)

MaxDraft == 2
MaxGeneration == 2
Drafts == {1, 2}
Gens == {0, 1, 2}
NoReceipt == 0
OtherReceipt == 3
AckNone == 3
Receipts == Gens \union {OtherReceipt}

ProposalPhases == {"none", "pending", "ready", "error", "retired", "ignored"}
PreviewPhases == {"none", "pending", "ready", "error", "retired", "ignored"}
ApplyPhases == {"none", "applying", "applied", "rejected"}
DiscoveryPhases == {"idle", "pending", "complete", "missing-unsupported", "ignored"}
Categories == {"string", "integer", "code"}

NextDraft(d) == IF d < MaxDraft THEN d + 1 ELSE d
NextGeneration(g) == IF g < MaxGeneration THEN g + 1 ELSE g

Owners == {
  [generation |-> generation, draft |-> draft, receipt |-> receipt] :
    generation \in Gens, draft \in Drafts, receipt \in Receipts
}

VARIABLES
  currentDraft, acceptedDraft,
  proposalPhase, proposalOwner,
  previewPhase, previewOwner,
  applyPhase, applyOwner, applyAckTarget, applyAckResult,
  discoveryPhase, discoveryDraft, discoveryCategory, discoveryValue,
  discoveryResultDraft, discoveryResultCategory, discoveryResultValue,
  pivotMounted, pivotMissing, presenceTracked,
  lastAction

vars == <<
  currentDraft, acceptedDraft, proposalPhase, proposalOwner,
  previewPhase, previewOwner, applyPhase, applyOwner, applyAckTarget,
  applyAckResult, discoveryPhase, discoveryDraft, discoveryCategory,
  discoveryValue, discoveryResultDraft, discoveryResultCategory,
  discoveryResultValue, pivotMounted, pivotMissing, presenceTracked,
  lastAction
>>

Init ==
  /\ currentDraft = 1
  /\ acceptedDraft = 1
  /\ proposalPhase = "none"
  /\ proposalOwner = [generation |-> 0, draft |-> 1, receipt |-> NoReceipt]
  /\ previewPhase = "none"
  /\ previewOwner = [generation |-> 0, draft |-> 1, receipt |-> NoReceipt]
  /\ applyPhase = "none"
  /\ applyOwner = [generation |-> 0, draft |-> 1, receipt |-> NoReceipt]
  /\ applyAckTarget = AckNone
  /\ applyAckResult = "none"
  /\ discoveryPhase = "idle"
  /\ discoveryDraft = 1
  /\ discoveryCategory = "string"
  /\ discoveryValue = "integer"
  /\ discoveryResultDraft = 1
  /\ discoveryResultCategory = "string"
  /\ discoveryResultValue = "integer"
  /\ pivotMounted = FALSE
  /\ pivotMissing = FALSE
  /\ presenceTracked = FALSE
  /\ lastAction = "Init"

StartProposal ==
  /\ proposalPhase \in {"none", "ready", "error", "ignored"}
  /\ proposalOwner.generation < MaxGeneration
  /\ proposalPhase' = "pending"
  /\ proposalOwner' = [generation |-> proposalOwner.generation + 1,
                      draft |-> currentDraft,
                      receipt |-> proposalOwner.generation + 1]
  /\ previewPhase' = "none"
  /\ lastAction' = "StartProposal"
  /\ UNCHANGED <<
       currentDraft, acceptedDraft, previewOwner, applyPhase, applyOwner,
       applyAckTarget, applyAckResult, discoveryPhase, discoveryDraft,
       discoveryCategory, discoveryValue, discoveryResultDraft,
       discoveryResultCategory, discoveryResultValue, pivotMounted,
       pivotMissing, presenceTracked
     >>

ReplaceProposal ==
  /\ proposalOwner.generation < MaxGeneration
  /\ proposalPhase' = "pending"
  /\ proposalOwner' = [generation |-> proposalOwner.generation + 1,
                      draft |-> currentDraft,
                      receipt |-> proposalOwner.generation + 1]
  /\ previewPhase' = "none"
  /\ lastAction' = "ReplaceProposal"
  /\ UNCHANGED <<
       currentDraft, acceptedDraft, previewOwner, applyPhase, applyOwner,
       applyAckTarget, applyAckResult, discoveryPhase, discoveryDraft,
       discoveryCategory, discoveryValue, discoveryResultDraft,
       discoveryResultCategory, discoveryResultValue, pivotMounted,
       pivotMissing, presenceTracked
     >>

ProposalComplete ==
  /\ proposalPhase = "pending"
  /\ proposalPhase' =
       IF proposalOwner.draft = currentDraft THEN "ready" ELSE "error"
  /\ lastAction' = "ProposalComplete"
  /\ UNCHANGED <<
       currentDraft, acceptedDraft, proposalOwner, previewPhase,
       previewOwner, applyPhase, applyOwner, applyAckTarget, applyAckResult,
       discoveryPhase, discoveryDraft, discoveryCategory, discoveryValue,
       discoveryResultDraft, discoveryResultCategory, discoveryResultValue,
       pivotMounted, pivotMissing, presenceTracked
     >>

ProposalError ==
  /\ proposalPhase = "pending"
  /\ proposalPhase' = "error"
  /\ lastAction' = "ProposalError"
  /\ UNCHANGED <<
       currentDraft, acceptedDraft, proposalOwner, previewPhase,
       previewOwner, applyPhase, applyOwner, applyAckTarget, applyAckResult,
       discoveryPhase, discoveryDraft, discoveryCategory, discoveryValue,
       discoveryResultDraft, discoveryResultCategory, discoveryResultValue,
       pivotMounted, pivotMissing, presenceTracked
     >>

LateProposalComplete ==
  /\ proposalPhase = "retired"
  /\ proposalPhase' = "ignored"
  /\ lastAction' = "LateProposalCompleteIgnored"
  /\ UNCHANGED <<
       currentDraft, acceptedDraft, proposalOwner, previewPhase,
       previewOwner, applyPhase, applyOwner, applyAckTarget, applyAckResult,
       discoveryPhase, discoveryDraft, discoveryCategory, discoveryValue,
       discoveryResultDraft, discoveryResultCategory, discoveryResultValue,
       pivotMounted, pivotMissing, presenceTracked
     >>

CancelProposal ==
  /\ proposalPhase \in {"pending", "ready", "error"}
  /\ proposalPhase' = "none"
  /\ previewPhase' = "none"
  /\ lastAction' = "CancelProposal"
  /\ UNCHANGED <<
       currentDraft, acceptedDraft, proposalOwner, previewOwner, applyPhase,
       applyOwner, applyAckTarget, applyAckResult, discoveryPhase,
       discoveryDraft, discoveryCategory, discoveryValue,
       discoveryResultDraft, discoveryResultCategory, discoveryResultValue,
       pivotMounted, pivotMissing, presenceTracked
     >>

RetryProposal ==
  /\ proposalPhase = "error"
  /\ proposalOwner.generation < MaxGeneration
  /\ proposalPhase' = "pending"
  /\ proposalOwner' = [proposalOwner EXCEPT
       !.generation = proposalOwner.generation + 1,
       !.draft = currentDraft,
       !.receipt = proposalOwner.generation + 1]
  /\ previewPhase' = "none"
  /\ lastAction' = "RetryProposal"
  /\ UNCHANGED <<
       currentDraft, acceptedDraft, previewOwner, applyPhase, applyOwner,
       applyAckTarget, applyAckResult, discoveryPhase, discoveryDraft,
       discoveryCategory, discoveryValue, discoveryResultDraft,
       discoveryResultCategory, discoveryResultValue, pivotMounted,
       pivotMissing, presenceTracked
     >>

StartPreview ==
  /\ proposalPhase = "ready"
  /\ previewPhase \in {"none", "error", "ignored"}
  /\ previewPhase' = "pending"
  /\ previewOwner' = [generation |-> proposalOwner.generation,
                     draft |-> acceptedDraft,
                     receipt |-> proposalOwner.receipt]
  /\ lastAction' = "StartPreview"
  /\ UNCHANGED <<
       currentDraft, acceptedDraft, proposalPhase, proposalOwner,
       applyPhase, applyOwner, applyAckTarget, applyAckResult,
       discoveryPhase, discoveryDraft, discoveryCategory, discoveryValue,
       discoveryResultDraft, discoveryResultCategory, discoveryResultValue,
       pivotMounted, pivotMissing, presenceTracked
     >>

PreviewComplete(good) ==
  /\ previewPhase = "pending"
  /\ previewPhase' = IF good THEN "ready" ELSE "error"
  /\ previewOwner' = IF good THEN previewOwner
                     ELSE [previewOwner EXCEPT !.receipt = OtherReceipt]
  /\ lastAction' = "PreviewComplete"
  /\ UNCHANGED <<
       currentDraft, acceptedDraft, proposalPhase, proposalOwner,
       applyPhase, applyOwner, applyAckTarget, applyAckResult,
       discoveryPhase, discoveryDraft, discoveryCategory, discoveryValue,
       discoveryResultDraft, discoveryResultCategory, discoveryResultValue,
       pivotMounted, pivotMissing, presenceTracked
     >>

LatePreviewComplete ==
  /\ previewPhase = "retired"
  /\ previewPhase' = "ignored"
  /\ lastAction' = "LatePreviewCompleteIgnored"
  /\ UNCHANGED <<
       currentDraft, acceptedDraft, proposalPhase, proposalOwner,
       previewOwner, applyPhase, applyOwner, applyAckTarget, applyAckResult,
       discoveryPhase, discoveryDraft, discoveryCategory, discoveryValue,
       discoveryResultDraft, discoveryResultCategory, discoveryResultValue,
       pivotMounted, pivotMissing, presenceTracked
     >>

BeginApply ==
  /\ proposalPhase = "ready"
  /\ previewPhase = "ready"
  /\ applyPhase = "none"
  /\ previewOwner.receipt = proposalOwner.receipt
  /\ previewOwner.generation = proposalOwner.generation
  /\ previewOwner.draft = acceptedDraft
  /\ currentDraft = acceptedDraft
  /\ applyPhase' = "applying"
  /\ applyOwner' = previewOwner
  /\ applyAckTarget' = AckNone
  /\ applyAckResult' = "none"
  /\ lastAction' = "BeginApply"
  /\ UNCHANGED <<
       currentDraft, acceptedDraft, proposalPhase, proposalOwner,
       previewPhase, previewOwner, discoveryPhase, discoveryDraft,
       discoveryCategory, discoveryValue, discoveryResultDraft,
       discoveryResultCategory, discoveryResultValue, pivotMounted,
       pivotMissing, presenceTracked
     >>

(*
  BuilderWorkspace.applyConstructionProposal calls finishApply(applied).
  finishApply(true) calls cancel() on the *current* owner; finishApply(false)
  marks the *current* owner as an error.  The current owner can be a
  replacement started while Apply was pending.
*)
ApplyAck(success) ==
  /\ applyPhase = "applying"
  /\ applyAckTarget' = applyOwner.generation
  /\ applyAckResult' = IF success THEN "success" ELSE "failure"
  /\ discoveryPhase' = IF success THEN "idle" ELSE discoveryPhase
  /\ IF success
     THEN /\ applyPhase' = "applied"
          /\ IF proposalOwner.generation = applyOwner.generation
             THEN /\ proposalPhase' = "none"
                  /\ previewPhase' = "none"
                  /\ currentDraft' = NextDraft(currentDraft)
                  /\ acceptedDraft' = NextDraft(acceptedDraft)
             ELSE /\ proposalPhase' = proposalPhase
                  /\ previewPhase' = previewPhase
                  /\ currentDraft' = currentDraft
                  /\ acceptedDraft' = acceptedDraft
     ELSE /\ applyPhase' = "rejected"
          /\ IF proposalOwner.generation = applyOwner.generation
             THEN /\ proposalPhase' = "error"
                  /\ previewPhase' = "none"
             ELSE /\ proposalPhase' = proposalPhase
                  /\ previewPhase' = previewPhase
          /\ currentDraft' = currentDraft
          /\ acceptedDraft' = acceptedDraft
  /\ lastAction' = "ApplyAck"
  /\ UNCHANGED <<
       proposalOwner, previewOwner, applyOwner,
       discoveryDraft, discoveryCategory, discoveryValue,
       discoveryResultDraft, discoveryResultCategory, discoveryResultValue,
       pivotMounted, pivotMissing, presenceTracked
     >>

ChangeDraft ==
  /\ currentDraft' = NextDraft(currentDraft)
  /\ proposalPhase' = IF proposalPhase = "pending" THEN "retired" ELSE "none"
  /\ previewPhase' = IF previewPhase = "pending" THEN "retired" ELSE "none"
  /\ discoveryPhase' = IF discoveryPhase = "pending" THEN "ignored" ELSE "idle"
  /\ lastAction' = "ChangeDraft"
  /\ UNCHANGED <<
       acceptedDraft, proposalOwner, previewOwner, applyPhase, applyOwner,
       applyAckTarget, applyAckResult, discoveryDraft, discoveryCategory,
       discoveryValue, discoveryResultDraft, discoveryResultCategory,
       discoveryResultValue, pivotMounted, pivotMissing, presenceTracked
     >>

AcceptCurrentDraft ==
  /\ acceptedDraft' = currentDraft
  /\ proposalPhase' = "none"
  /\ previewPhase' = "none"
  /\ discoveryPhase' = "idle"
  /\ lastAction' = "AcceptCurrentDraft"
  /\ UNCHANGED <<
       currentDraft, proposalOwner, previewOwner, applyPhase, applyOwner,
       applyAckTarget, applyAckResult, discoveryDraft, discoveryCategory,
       discoveryValue, discoveryResultDraft, discoveryResultCategory,
       discoveryResultValue, pivotMounted, pivotMissing, presenceTracked
     >>

SetPivotPair(missing) ==
  /\ missing \in BOOLEAN
  /\ pivotMounted' = TRUE
  /\ pivotMissing' = missing
  /\ discoveryDraft' = currentDraft
  /\ discoveryCategory' = "string"
  /\ discoveryValue' = "integer"
  /\ discoveryPhase' = "idle"
  /\ discoveryResultDraft' = currentDraft
  /\ discoveryResultCategory' = "string"
  /\ discoveryResultValue' = "integer"
  /\ lastAction' = "SetPivotPair"
  /\ UNCHANGED <<
       currentDraft, acceptedDraft, proposalPhase, proposalOwner,
       previewPhase, previewOwner, applyPhase, applyOwner, applyAckTarget,
       applyAckResult, presenceTracked
     >>

ScheduleDiscovery ==
  /\ pivotMounted
  /\ discoveryPhase = "idle"
  /\ discoveryPhase' = "pending"
  /\ lastAction' = "ScheduleDiscovery"
  /\ UNCHANGED <<
       currentDraft, acceptedDraft, proposalPhase, proposalOwner,
       previewPhase, previewOwner, applyPhase, applyOwner, applyAckTarget,
       applyAckResult, discoveryDraft, discoveryCategory, discoveryValue,
       discoveryResultDraft, discoveryResultCategory, discoveryResultValue,
       pivotMounted, pivotMissing, presenceTracked
     >>

DiscoveryComplete ==
  /\ discoveryPhase = "pending"
  /\ LET outcome ==
           IF discoveryDraft # currentDraft THEN "ignored"
           ELSE IF discoveryCategory = discoveryValue THEN "ignored"
           ELSE IF pivotMissing /\ ~presenceTracked THEN "missing-unsupported"
           ELSE "complete"
     IN /\ discoveryPhase' = outcome
        /\ discoveryResultDraft' = currentDraft
        /\ discoveryResultCategory' = discoveryCategory
        /\ discoveryResultValue' = discoveryValue
        /\ lastAction' = "DiscoveryComplete(" \o outcome \o ")"
  /\ UNCHANGED <<
       currentDraft, acceptedDraft, proposalPhase, proposalOwner,
       previewPhase, previewOwner, applyPhase, applyOwner, applyAckTarget,
       applyAckResult, discoveryDraft, discoveryCategory, discoveryValue,
       pivotMounted, pivotMissing, presenceTracked
     >>

ChangePivotKey ==
  /\ discoveryCategory' = "code"
  /\ discoveryValue' = "integer"
  /\ discoveryPhase' = "idle"
  /\ discoveryResultDraft' = currentDraft
  /\ discoveryResultCategory' = "string"
  /\ discoveryResultValue' = "integer"
  /\ lastAction' = "ChangePivotKey"
  /\ UNCHANGED <<
       currentDraft, acceptedDraft, proposalPhase, proposalOwner,
       previewPhase, previewOwner, applyPhase, applyOwner, applyAckTarget,
       applyAckResult, discoveryDraft, pivotMounted, pivotMissing,
       presenceTracked
     >>

UnmountEditor ==
  /\ pivotMounted
  /\ pivotMounted' = FALSE
  /\ discoveryPhase' = IF discoveryPhase = "pending" THEN "ignored"
                       ELSE discoveryPhase
  /\ lastAction' = "UnmountEditor"
  /\ UNCHANGED <<
       currentDraft, acceptedDraft, proposalPhase, proposalOwner,
       previewPhase, previewOwner, applyPhase, applyOwner, applyAckTarget,
       applyAckResult, discoveryDraft, discoveryCategory, discoveryValue,
       discoveryResultDraft, discoveryResultCategory, discoveryResultValue,
       pivotMissing, presenceTracked
     >>

AdoptDiscovery ==
  /\ pivotMounted
  /\ discoveryPhase = "complete"
  /\ discoveryResultDraft = currentDraft
  /\ discoveryResultCategory = discoveryCategory
  /\ discoveryResultValue = discoveryValue
  /\ lastAction' = "AdoptDiscovery"
  /\ UNCHANGED <<
       currentDraft, acceptedDraft, proposalPhase, proposalOwner,
       previewPhase, previewOwner, applyPhase, applyOwner, applyAckTarget,
       applyAckResult, discoveryPhase, discoveryDraft, discoveryCategory,
       discoveryValue, discoveryResultDraft, discoveryResultCategory,
       discoveryResultValue, pivotMounted, pivotMissing, presenceTracked
     >>

Next ==
  \/ StartProposal
  \/ ReplaceProposal
  \/ ProposalComplete
  \/ ProposalError
  \/ LateProposalComplete
  \/ CancelProposal
  \/ RetryProposal
  \/ StartPreview
  \/ \E good \in BOOLEAN: PreviewComplete(good)
  \/ LatePreviewComplete
  \/ BeginApply
  \/ \E success \in BOOLEAN: ApplyAck(success)
  \/ ChangeDraft
  \/ AcceptCurrentDraft
  \/ \E missing \in BOOLEAN: SetPivotPair(missing)
  \/ ScheduleDiscovery
  \/ DiscoveryComplete
  \/ ChangePivotKey
  \/ UnmountEditor
  \/ AdoptDiscovery

Spec == Init /\ [][Next]_vars

Inv_PreviewOwnedByCurrentProposal ==
  IF previewPhase = "ready"
  THEN /\ proposalPhase = "ready"
       /\ previewOwner.generation = proposalOwner.generation
       /\ previewOwner.receipt = proposalOwner.receipt
       /\ previewOwner.draft = acceptedDraft
  ELSE TRUE

Inv_ApplyUsesReviewedPreview ==
  IF applyPhase \in {"applying", "applied"}
     /\ proposalOwner.generation = applyOwner.generation
  THEN /\ applyOwner.generation = previewOwner.generation
       /\ applyOwner.receipt = previewOwner.receipt
       /\ (applyOwner.draft = acceptedDraft \/
           acceptedDraft = NextDraft(applyOwner.draft))
  ELSE TRUE

Inv_ApplyAckTargetsInitiatingOwner ==
  IF applyPhase = "applied"
  THEN applyAckTarget = applyOwner.generation
  ELSE TRUE

Inv_ApplyFailureTargetsInitiatingOwner ==
  IF applyPhase = "rejected"
  THEN applyAckTarget = applyOwner.generation
  ELSE TRUE

Inv_DiscoveryOwnedByCurrentKey ==
  IF discoveryPhase = "complete" /\ pivotMounted
  THEN /\ discoveryResultDraft = currentDraft
       /\ discoveryResultCategory = discoveryCategory
       /\ discoveryResultValue = discoveryValue
  ELSE TRUE

Inv_NoMissingUnsupportedPivot ==
  discoveryPhase # "missing-unsupported"

Inv_DraftMonotonic ==
  acceptedDraft <= currentDraft

TypeOK ==
  /\ currentDraft \in Drafts
  /\ acceptedDraft \in Drafts
  /\ proposalPhase \in ProposalPhases
  /\ proposalOwner \in Owners
  /\ previewPhase \in PreviewPhases
  /\ previewOwner \in Owners
  /\ applyPhase \in ApplyPhases
  /\ applyOwner \in Owners
  /\ applyAckTarget \in (Gens \union {AckNone})
  /\ applyAckResult \in {"none", "success", "failure"}
  /\ discoveryPhase \in DiscoveryPhases
  /\ discoveryDraft \in Drafts
  /\ discoveryCategory \in Categories
  /\ discoveryValue \in Categories
  /\ discoveryResultDraft \in Drafts
  /\ discoveryResultCategory \in Categories
  /\ discoveryResultValue \in Categories
  /\ pivotMounted \in BOOLEAN
  /\ pivotMissing \in BOOLEAN
  /\ presenceTracked \in BOOLEAN

====
