---------------- MODULE ConstructionLifecycle ----------------
EXTENDS Naturals, FiniteSets, TLC

(* Finite interaction model for useConstructionLifecycle.ts.               *)
(*                                                                        *)
(* Source map: useConstructionLifecycle.ts:63-80, 141-155, 230-326,        *)
(* 444-477; react.tsx:65-112 (resource snapshot, abort, and epoch publish). *)
(* Frozen hook SHA256: 02ca6f5b6e629c88bcc0d581f1ff9cf811a7c5f361fd54373919b16e523c8e06. *)
(* Run the cfg with Java 17 and TLC 1.7.4 from this directory.              *)
(*                                                                        *)
(* A request identity is the complete capabilitiesIdentity JSON value:    *)
(* project, auth path, explorer, snapshot, draft version/digest, output,   *)
(* stage, and group step. DraftA and DraftB stand for two distinct values. *)
(* Context changes do not increment proposal generation or clear owner.    *)
(* Thus DraftA -> DraftB -> DraftA with the same identity is indistinguish- *)
(* able from remaining on DraftA; an uncancelled owner can become active   *)
(* again after that ABA sequence.                                         *)
(*                                                                        *)
(* Proposal and preview API completions/errors, replacement, cancellation,*)
(* context changes, and beginApply are separate actions. An active owner   *)
(* can finish while its request identity is inactive; the resource data   *)
(* remains stored but is hidden until that exact identity returns.         *)
(* resourceFor abort/epoch publication and owner/generation checks are     *)
(* abstracted by allowing normal completion only for the current owner,    *)
(* and explicit ignored settlement actions after retirement.              *)
(*                                                                        *)
(* The model stops at beginApply. It does not model persistence or         *)
(* finishApply callbacks, including an old apply completing after proposal *)
(* replacement. It does not claim beginApply is at most once: the          *)
(* expectedProposal path reads the ready resource snapshot, so repeated   *)
(* calls with the same matching expected proposal can return the receipt   *)
(* while that snapshot remains ready.                                     *)
(* The finite bound is two unique proposal attempts: P2 can be one retry  *)
(* of a failed current P1 with the same request identity and intent.      *)
(* Each attempt has its own fixed receipt; duplicate server receipts across*)
(* attempts are outside this finite value domain.                          *)
Attempt1 == "proposal-1"
Attempt2 == "proposal-2"
Attempts == {Attempt1, Attempt2}

DraftA == "draft-A"
DraftB == "draft-B"
Identities == {DraftA, DraftB}
NoIdentity == "no-request"

OutputA == "output-A"
OutputB == "output-B"
Outputs == {OutputA, OutputB}
OutputFor(identity) == IF identity = DraftA THEN OutputA ELSE OutputB
ResponseA == "response-identity-A"
ResponseB == "response-identity-B"
ResponseIdentities == {ResponseA, ResponseB}
NoResponseIdentity == "no-response-identity"
ResponseIdentityFor(identity) ==
  IF identity = DraftA THEN ResponseA ELSE ResponseB

Receipt1 == "receipt-1"
Receipt2 == "receipt-2"
Receipts == {Receipt1, Receipt2}
NoReceipt == "no-receipt"
ReceiptFor(attempt) == IF attempt = Attempt1 THEN Receipt1 ELSE Receipt2

NoAttempt == "no-owner"
Gen0 == "generation-0"
Gen1 == "generation-1"
Gen2 == "generation-2"
Gen3 == "generation-3"
Gen4 == "generation-4"
NoGeneration == "no-generation"
Generation == {Gen0, Gen1, Gen2, Gen3, Gen4}
OwnerGenerations == {NoGeneration, Gen0, Gen1, Gen2, Gen3, Gen4}
NextGeneration(value) ==
  CASE value = Gen0 -> Gen1
    [] value = Gen1 -> Gen2
    [] value = Gen2 -> Gen3
    [] value = Gen3 -> Gen4
    [] value = Gen4 -> Gen4

Unstarted == "unstarted"
ProposalPending == "proposal-pending"
PreviewPending == "preview-pending"
ProposalReady == "ready"
NeedsRepair == "needs-repair"
ProposalErrorState == "error"
RetiredProposalPending == "retired-proposal-pending"
RetiredPreviewPending == "retired-preview-pending"
RetiredDone == "retired-done"
AttemptPhases == {
  Unstarted, ProposalPending, PreviewPending, ProposalReady,
  NeedsRepair, ProposalErrorState, RetiredProposalPending,
  RetiredPreviewPending, RetiredDone
}
RetirePhase(phase) ==
  IF phase = ProposalPending THEN RetiredProposalPending
  ELSE IF phase = PreviewPending THEN RetiredPreviewPending
  ELSE RetiredDone

NoOverride == "no-override"
ApplyingOverride == "applying"
OverrideStates == {NoOverride, ApplyingOverride}

NoExpectedProposal == "no-expected-proposal"
ExpectedProposals == Attempts \cup {NoExpectedProposal}

ApplyWitnesses ==
  [present : BOOLEAN,
   owner : Attempts,
   requestIdentity : Identities,
   currentIdentity : Identities,
   generation : Generation,
   ownerGeneration : Generation,
   responseMatches : BOOLEAN,
   proposalIdPresent : BOOLEAN,
   proposalReceipt : Receipts,
   responseOutput : Outputs,
   previewReceipt : Receipts,
   previewOutput : Outputs,
   previewRowsNonNull : BOOLEAN,
   expectedProposal : ExpectedProposals,
   expectedFieldsMatch : BOOLEAN,
   expectedIdentity : Identities \cup {NoIdentity},
   expectedResponseIdentity : ResponseIdentities \cup {NoResponseIdentity},
   expectedProposalOutput : Outputs \cup {"no-output"},
   expectedProposalReceipt : Receipts \cup {NoReceipt},
   expectedPreviewReceipt : Receipts \cup {NoReceipt}]
EmptyWitness(attempt) ==
  [present |-> FALSE,
   owner |-> attempt,
   requestIdentity |-> DraftA,
   currentIdentity |-> DraftA,
   generation |-> Gen0,
   ownerGeneration |-> Gen0,
   responseMatches |-> FALSE,
   proposalIdPresent |-> FALSE,
   proposalReceipt |-> Receipt1,
   responseOutput |-> OutputA,
   previewReceipt |-> Receipt1,
   previewOutput |-> OutputA,
   previewRowsNonNull |-> FALSE,
   expectedProposal |-> NoExpectedProposal,
   expectedFieldsMatch |-> FALSE,
   expectedIdentity |-> NoIdentity,
   expectedResponseIdentity |-> NoResponseIdentity,
   expectedProposalOutput |-> "no-output",
   expectedProposalReceipt |-> NoReceipt,
   expectedPreviewReceipt |-> NoReceipt]

VARIABLES
  currentIdentity,
  owner,
  generation,
  ownerGeneration,
  attemptPhase,
  requestIdentity,
  responseMatches,
  proposalIdPresent,
  proposalReceipt,
  responseOutput,
  previewReceipt,
  previewOutput,
  previewRowsNonNull,
  overrideOwner,
  overrideState,
  applyWitness,
  lastAction,
  lastBeginApplyResult,
  lastBeginApplyExpected,
  lastBeginApplyFieldsMatch

vars == <<
  currentIdentity,
  owner,
  generation,
  ownerGeneration,
  attemptPhase,
  requestIdentity,
  responseMatches,
  proposalIdPresent,
  proposalReceipt,
  responseOutput,
  previewReceipt,
  previewOutput,
  previewRowsNonNull,
  overrideOwner,
  overrideState,
  applyWitness,
  lastAction,
  lastBeginApplyResult,
  lastBeginApplyExpected,
  lastBeginApplyFieldsMatch
>>

Init ==
  /\ currentIdentity = DraftA
  /\ owner = NoAttempt
  /\ generation = Gen0
  /\ ownerGeneration = NoGeneration
  /\ attemptPhase = [attempt \in Attempts |-> Unstarted]
  /\ requestIdentity = [attempt \in Attempts |-> NoIdentity]
  /\ responseMatches = [attempt \in Attempts |-> FALSE]
  /\ proposalIdPresent = [attempt \in Attempts |-> FALSE]
  /\ proposalReceipt = [attempt \in Attempts |-> NoReceipt]
  /\ responseOutput = [attempt \in Attempts |-> "no-output"]
  /\ previewReceipt = [attempt \in Attempts |-> NoReceipt]
  /\ previewOutput = [attempt \in Attempts |-> "no-output"]
  /\ previewRowsNonNull = [attempt \in Attempts |-> FALSE]
  /\ overrideOwner = NoAttempt
  /\ overrideState = NoOverride
  /\ applyWitness = [attempt \in Attempts |-> EmptyWitness(attempt)]
  /\ lastAction = "Init"
  /\ lastBeginApplyResult = "not-called"
  /\ lastBeginApplyExpected = NoExpectedProposal
  /\ lastBeginApplyFieldsMatch = FALSE

ChangeDraftIdentity ==
  /\ currentIdentity' = IF currentIdentity = DraftA THEN DraftB ELSE DraftA
  /\ lastAction' =
       IF currentIdentity = DraftA
       THEN "ChangeDraftIdentity(A-to-B)"
       ELSE "ChangeDraftIdentity(B-to-A)"
  /\ UNCHANGED <<
       owner, generation, ownerGeneration, attemptPhase, requestIdentity,
       responseMatches, proposalIdPresent, proposalReceipt, responseOutput,
       previewReceipt, previewOutput, previewRowsNonNull, overrideOwner,
       overrideState, applyWitness, lastBeginApplyResult,
       lastBeginApplyExpected, lastBeginApplyFieldsMatch
     >>

RetryableProposal ==
  /\ owner = Attempt1
  /\ attemptPhase[Attempt1] = ProposalErrorState
  /\ requestIdentity[Attempt1] = currentIdentity
  /\ attemptPhase[Attempt2] = Unstarted

StartProposalTransition(attempt, isRetry) ==
  /\ attempt \in Attempts
  /\ attemptPhase[attempt] = Unstarted
  /\ generation # Gen4
  /\ IF isRetry
     THEN /\ attempt = Attempt2
          /\ RetryableProposal
     ELSE ~(attempt = Attempt2 /\ RetryableProposal)
  /\ LET phasesAfterReplacement ==
           IF owner \in Attempts
           THEN [attemptPhase EXCEPT ![owner] = RetirePhase(attemptPhase[owner])]
           ELSE attemptPhase
     IN attemptPhase' = [phasesAfterReplacement EXCEPT ![attempt] = ProposalPending]
  /\ requestIdentity' = [requestIdentity EXCEPT ![attempt] = currentIdentity]
  /\ owner' = attempt
  /\ generation' = NextGeneration(generation)
  /\ ownerGeneration' = NextGeneration(generation)
  /\ overrideOwner' = NoAttempt
  /\ overrideState' = NoOverride
  /\ lastAction' =
       IF isRetry THEN "RetryProposal(P1-to-P2)"
       ELSE IF owner = NoAttempt
            THEN IF attempt = Attempt1 THEN "StartProposal(P1)" ELSE "StartProposal(P2)"
            ELSE "ReplaceProposalOwner"
  /\ UNCHANGED <<
       currentIdentity, responseMatches, proposalIdPresent, proposalReceipt,
       responseOutput, previewReceipt, previewOutput, previewRowsNonNull,
       applyWitness, lastBeginApplyResult, lastBeginApplyExpected,
       lastBeginApplyFieldsMatch
     >>

StartProposal(attempt) == StartProposalTransition(attempt, FALSE)
RetryProposal == StartProposalTransition(Attempt2, TRUE)

CancelProposal ==
  /\ owner \in Attempts
  /\ generation # Gen4
  /\ owner' = NoAttempt
  /\ ownerGeneration' = NoGeneration
  /\ generation' = NextGeneration(generation)
  /\ attemptPhase' = [attemptPhase EXCEPT ![owner] = RetirePhase(attemptPhase[owner])]
  /\ overrideOwner' = NoAttempt
  /\ overrideState' = NoOverride
  /\ lastAction' = "CancelProposalOwner"
  /\ UNCHANGED <<
       currentIdentity, requestIdentity, responseMatches,
       proposalIdPresent, proposalReceipt, responseOutput, previewReceipt,
       previewOutput, previewRowsNonNull, applyWitness,
       lastBeginApplyResult, lastBeginApplyExpected,
       lastBeginApplyFieldsMatch
     >>

ProposalComplete(attempt, baseMatches, statusReady, hasProposalId,
                 embeddedPreviewMatches) ==
  /\ attempt \in Attempts
  /\ owner = attempt
  /\ attemptPhase[attempt] = ProposalPending
  /\ baseMatches \in BOOLEAN
  /\ statusReady \in BOOLEAN
  /\ hasProposalId \in BOOLEAN
  /\ embeddedPreviewMatches \in BOOLEAN
  /\ attemptPhase' =
       [attemptPhase EXCEPT ![attempt] =
         IF ~baseMatches THEN ProposalErrorState
         ELSE IF ~statusReady THEN NeedsRepair
         ELSE IF ~hasProposalId THEN ProposalErrorState
         ELSE IF embeddedPreviewMatches THEN ProposalReady
         ELSE PreviewPending]
  /\ responseMatches' = [responseMatches EXCEPT ![attempt] = baseMatches]
  /\ proposalIdPresent' =
       [proposalIdPresent EXCEPT ![attempt] =
         baseMatches /\ statusReady /\ hasProposalId]
  /\ proposalReceipt' =
       [proposalReceipt EXCEPT ![attempt] =
         IF baseMatches /\ statusReady /\ hasProposalId
         THEN ReceiptFor(attempt) ELSE NoReceipt]
  /\ responseOutput' =
       [responseOutput EXCEPT ![attempt] =
         IF baseMatches THEN OutputFor(requestIdentity[attempt]) ELSE "no-output"]
  /\ previewReceipt' =
       [previewReceipt EXCEPT ![attempt] =
         IF baseMatches /\ statusReady /\ hasProposalId /\ embeddedPreviewMatches
         THEN ReceiptFor(attempt) ELSE NoReceipt]
  /\ previewOutput' =
       [previewOutput EXCEPT ![attempt] =
         IF baseMatches /\ statusReady /\ hasProposalId /\ embeddedPreviewMatches
         THEN OutputFor(requestIdentity[attempt]) ELSE "no-output"]
  /\ previewRowsNonNull' =
       [previewRowsNonNull EXCEPT ![attempt] =
         baseMatches /\ statusReady /\ hasProposalId /\ embeddedPreviewMatches]
  /\ lastAction' =
       IF attempt = Attempt1 THEN "ProposalComplete(P1)" ELSE "ProposalComplete(P2)"
  /\ UNCHANGED <<
       currentIdentity, owner, generation, ownerGeneration, requestIdentity,
       overrideOwner, overrideState, applyWitness, lastBeginApplyResult,
       lastBeginApplyExpected, lastBeginApplyFieldsMatch
     >>

ProposalError(attempt) ==
  /\ attempt \in Attempts
  /\ owner = attempt
  /\ attemptPhase[attempt] = ProposalPending
  /\ attemptPhase' = [attemptPhase EXCEPT ![attempt] = ProposalErrorState]
  /\ lastAction' =
       IF attempt = Attempt1 THEN "ProposalError(P1)" ELSE "ProposalError(P2)"
  /\ UNCHANGED <<
       currentIdentity, owner, generation, ownerGeneration, requestIdentity,
       responseMatches, proposalIdPresent, proposalReceipt, responseOutput,
       previewReceipt, previewOutput, previewRowsNonNull, overrideOwner,
       overrideState, applyWitness, lastBeginApplyResult,
       lastBeginApplyExpected, lastBeginApplyFieldsMatch
     >>

LateProposalSettlementIgnored(attempt, isError) ==
  /\ attempt \in Attempts
  /\ isError \in BOOLEAN
  /\ attemptPhase[attempt] = RetiredProposalPending
  /\ attemptPhase' = [attemptPhase EXCEPT ![attempt] = RetiredDone]
  /\ lastAction' =
       IF isError
       THEN IF attempt = Attempt1
            THEN "LateProposalErrorIgnored(P1)"
            ELSE "LateProposalErrorIgnored(P2)"
       ELSE IF attempt = Attempt1
            THEN "LateProposalCompleteIgnored(P1)"
            ELSE "LateProposalCompleteIgnored(P2)"
  /\ UNCHANGED <<
       currentIdentity, owner, generation, ownerGeneration, requestIdentity,
       responseMatches, proposalIdPresent, proposalReceipt, responseOutput,
       previewReceipt, previewOutput, previewRowsNonNull, overrideOwner,
       overrideState, applyWitness, lastBeginApplyResult,
       lastBeginApplyExpected, lastBeginApplyFieldsMatch
     >>

PreviewComplete(attempt, returnedReceipt, returnedOutput, rowsNonNull) ==
  /\ attempt \in Attempts
  /\ owner = attempt
  /\ attemptPhase[attempt] = PreviewPending
  /\ returnedReceipt \in Receipts
  /\ returnedOutput \in Outputs
  /\ rowsNonNull \in BOOLEAN
  /\ previewReceipt' = [previewReceipt EXCEPT ![attempt] = returnedReceipt]
  /\ previewOutput' = [previewOutput EXCEPT ![attempt] = returnedOutput]
  /\ previewRowsNonNull' = [previewRowsNonNull EXCEPT ![attempt] = rowsNonNull]
  /\ attemptPhase' =
       [attemptPhase EXCEPT ![attempt] =
         IF returnedReceipt = proposalReceipt[attempt]
           /\ returnedOutput = responseOutput[attempt]
           /\ rowsNonNull
         THEN ProposalReady ELSE ProposalErrorState]
  /\ lastAction' =
       IF attempt = Attempt1 THEN "PreviewComplete(P1)" ELSE "PreviewComplete(P2)"
  /\ UNCHANGED <<
       currentIdentity, owner, generation, ownerGeneration, requestIdentity,
       responseMatches, proposalIdPresent, proposalReceipt, responseOutput,
       overrideOwner, overrideState, applyWitness, lastBeginApplyResult,
       lastBeginApplyExpected, lastBeginApplyFieldsMatch
     >>

PreviewError(attempt) ==
  /\ attempt \in Attempts
  /\ owner = attempt
  /\ attemptPhase[attempt] = PreviewPending
  /\ attemptPhase' = [attemptPhase EXCEPT ![attempt] = ProposalErrorState]
  /\ lastAction' =
       IF attempt = Attempt1 THEN "PreviewError(P1)" ELSE "PreviewError(P2)"
  /\ UNCHANGED <<
       currentIdentity, owner, generation, ownerGeneration, requestIdentity,
       responseMatches, proposalIdPresent, proposalReceipt, responseOutput,
       previewReceipt, previewOutput, previewRowsNonNull, overrideOwner,
       overrideState, applyWitness, lastBeginApplyResult,
       lastBeginApplyExpected, lastBeginApplyFieldsMatch
     >>

LatePreviewSettlementIgnored(attempt, isError) ==
  /\ attempt \in Attempts
  /\ isError \in BOOLEAN
  /\ attemptPhase[attempt] = RetiredPreviewPending
  /\ attemptPhase' = [attemptPhase EXCEPT ![attempt] = RetiredDone]
  /\ lastAction' =
       IF isError
       THEN IF attempt = Attempt1
            THEN "LatePreviewErrorIgnored(P1)"
            ELSE "LatePreviewErrorIgnored(P2)"
       ELSE IF attempt = Attempt1
            THEN "LatePreviewCompleteIgnored(P1)"
            ELSE "LatePreviewCompleteIgnored(P2)"
  /\ UNCHANGED <<
       currentIdentity, owner, generation, ownerGeneration, requestIdentity,
       responseMatches, proposalIdPresent, proposalReceipt, responseOutput,
       previewReceipt, previewOutput, previewRowsNonNull, overrideOwner,
       overrideState, applyWitness, lastBeginApplyResult,
       lastBeginApplyExpected, lastBeginApplyFieldsMatch
     >>

ResourceIsApplicable(attempt) ==
  /\ attempt \in Attempts
  /\ attemptPhase[attempt] = ProposalReady
  /\ requestIdentity[attempt] \in Identities
  /\ responseMatches[attempt]
  /\ proposalIdPresent[attempt]
  /\ proposalReceipt[attempt] = ReceiptFor(attempt)
  /\ responseOutput[attempt] = OutputFor(requestIdentity[attempt])
  /\ previewReceipt[attempt] = proposalReceipt[attempt]
  /\ previewOutput[attempt] = OutputFor(requestIdentity[attempt])
  /\ previewRowsNonNull[attempt]

StoredOutcomeIsReady(attempt) ==
  /\ attempt \in Attempts
  /\ requestIdentity[attempt] \in Identities
  /\ responseMatches[attempt]
  /\ proposalIdPresent[attempt]
  /\ proposalReceipt[attempt] = ReceiptFor(attempt)
  /\ responseOutput[attempt] = OutputFor(requestIdentity[attempt])
  /\ previewReceipt[attempt] = proposalReceipt[attempt]
  /\ previewOutput[attempt] = responseOutput[attempt]
  /\ previewRowsNonNull[attempt]

ExpectedFieldsMatch(expectedProposal) ==
  (* The attempt label selects a supplied outcome; the hook compares its   *)
  (* receipt and response identity fields, not a hidden owner-object token. *)
  IF expectedProposal \in Attempts /\ owner \in Attempts
  THEN /\ StoredOutcomeIsReady(expectedProposal)
       /\ responseMatches[owner]
       /\ proposalIdPresent[owner]
       /\ proposalReceipt[expectedProposal] = proposalReceipt[owner]
       /\ responseOutput[expectedProposal] = responseOutput[owner]
       /\ ResponseIdentityFor(requestIdentity[expectedProposal]) =
            ResponseIdentityFor(requestIdentity[owner])
       /\ previewReceipt[expectedProposal] = previewReceipt[owner]
  ELSE FALSE

ActiveProposalIsReady ==
  IF owner \in Attempts
  THEN /\ requestIdentity[owner] = currentIdentity
       /\ generation = ownerGeneration
       /\ attemptPhase[owner] = ProposalReady
       /\ (overrideOwner # owner \/ overrideState = NoOverride)
  ELSE FALSE

Inv_ConstructionProposalIsApplicable ==
  IF ActiveProposalIsReady
  THEN ResourceIsApplicable(owner)
  ELSE TRUE

BeginApplyWouldSucceed(expectedProposal) ==
  IF owner \in Attempts
  THEN /\ generation = ownerGeneration
       /\ requestIdentity[owner] = currentIdentity
       /\ ResourceIsApplicable(owner)
       /\ IF expectedProposal = NoExpectedProposal
          THEN ActiveProposalIsReady
          ELSE ExpectedFieldsMatch(expectedProposal)
  ELSE FALSE

BeginApply(expectedProposal) ==
  /\ expectedProposal \in ExpectedProposals
  /\ LET fieldsMatch == ExpectedFieldsMatch(expectedProposal)
         accepted == BeginApplyWouldSucceed(expectedProposal)
     IN /\ lastBeginApplyResult' = IF accepted THEN "accepted" ELSE "rejected"
        /\ lastBeginApplyExpected' = expectedProposal
        /\ lastBeginApplyFieldsMatch' = fieldsMatch
        /\ overrideOwner' = IF accepted THEN owner ELSE overrideOwner
        /\ overrideState' = IF accepted THEN ApplyingOverride ELSE overrideState
        /\ applyWitness' =
             IF accepted
             THEN [applyWitness EXCEPT ![owner] =
               [present |-> TRUE,
                owner |-> owner,
                requestIdentity |-> requestIdentity[owner],
                currentIdentity |-> currentIdentity,
                generation |-> generation,
                ownerGeneration |-> ownerGeneration,
                responseMatches |-> responseMatches[owner],
                proposalIdPresent |-> proposalIdPresent[owner],
                proposalReceipt |-> proposalReceipt[owner],
                responseOutput |-> responseOutput[owner],
                previewReceipt |-> previewReceipt[owner],
                previewOutput |-> previewOutput[owner],
                previewRowsNonNull |-> previewRowsNonNull[owner],
                expectedProposal |-> expectedProposal,
                expectedFieldsMatch |-> fieldsMatch,
                expectedIdentity |->
                  IF expectedProposal = NoExpectedProposal
                  THEN NoIdentity ELSE requestIdentity[expectedProposal],
                expectedResponseIdentity |->
                  IF expectedProposal = NoExpectedProposal
                  THEN NoResponseIdentity
                  ELSE ResponseIdentityFor(requestIdentity[expectedProposal]),
                expectedProposalOutput |->
                  IF expectedProposal = NoExpectedProposal
                  THEN "no-output" ELSE responseOutput[expectedProposal],
                expectedProposalReceipt |->
                  IF expectedProposal = NoExpectedProposal
                  THEN NoReceipt ELSE proposalReceipt[expectedProposal],
                expectedPreviewReceipt |->
                  IF expectedProposal = NoExpectedProposal
                  THEN NoReceipt ELSE previewReceipt[expectedProposal]]]
             ELSE applyWitness
        /\ lastAction' = IF accepted
                           THEN "BeginApply(accepted)"
                           ELSE "BeginApply(rejected)"
  /\ UNCHANGED <<
       currentIdentity, owner, generation, ownerGeneration, attemptPhase,
       requestIdentity, responseMatches, proposalIdPresent, proposalReceipt,
       responseOutput, previewReceipt, previewOutput, previewRowsNonNull
     >>

Inv_ExpectedProposalMatchesAppliedOwner ==
  \A attempt \in Attempts:
    IF applyWitness[attempt].present
       /\ applyWitness[attempt].expectedProposal # NoExpectedProposal
    THEN /\ applyWitness[attempt].expectedFieldsMatch
         /\ applyWitness[attempt].expectedResponseIdentity =
              ResponseIdentityFor(applyWitness[attempt].requestIdentity)
         /\ applyWitness[attempt].expectedProposalOutput =
              applyWitness[attempt].responseOutput
         /\ applyWitness[attempt].expectedProposalReceipt =
              applyWitness[attempt].proposalReceipt
         /\ applyWitness[attempt].expectedPreviewReceipt =
              applyWitness[attempt].previewReceipt
         /\ applyWitness[attempt].expectedPreviewReceipt =
              applyWitness[attempt].expectedProposalReceipt
    ELSE TRUE

Inv_BeginApplyHasCurrentProposalOwner ==
  /\ \A attempt \in Attempts:
       IF ~applyWitness[attempt].present
       THEN TRUE
       ELSE /\ applyWitness[attempt].owner = attempt
            /\ applyWitness[attempt].requestIdentity =
                 applyWitness[attempt].currentIdentity
            /\ applyWitness[attempt].generation =
                 applyWitness[attempt].ownerGeneration
            /\ applyWitness[attempt].responseMatches
            /\ applyWitness[attempt].proposalIdPresent
            /\ applyWitness[attempt].proposalReceipt = ReceiptFor(attempt)
            /\ applyWitness[attempt].responseOutput =
                 OutputFor(applyWitness[attempt].requestIdentity)
            /\ applyWitness[attempt].previewReceipt =
                 applyWitness[attempt].proposalReceipt
            /\ applyWitness[attempt].previewOutput =
                 applyWitness[attempt].responseOutput
            /\ applyWitness[attempt].previewRowsNonNull
  /\ Inv_ExpectedProposalMatchesAppliedOwner

TypeOK ==
  /\ currentIdentity \in Identities
  /\ owner \in (Attempts \cup {NoAttempt})
  /\ generation \in Generation
  /\ ownerGeneration \in OwnerGenerations
  /\ attemptPhase \in [Attempts -> AttemptPhases]
  /\ requestIdentity \in [Attempts -> (Identities \cup {NoIdentity})]
  /\ responseMatches \in [Attempts -> BOOLEAN]
  /\ proposalIdPresent \in [Attempts -> BOOLEAN]
  /\ proposalReceipt \in [Attempts -> (Receipts \cup {NoReceipt})]
  /\ responseOutput \in [Attempts -> (Outputs \cup {"no-output"})]
  /\ previewReceipt \in [Attempts -> (Receipts \cup {NoReceipt})]
  /\ previewOutput \in [Attempts -> (Outputs \cup {"no-output"})]
  /\ previewRowsNonNull \in [Attempts -> BOOLEAN]
  /\ overrideOwner \in (Attempts \cup {NoAttempt})
  /\ overrideState \in OverrideStates
  /\ applyWitness \in [Attempts -> ApplyWitnesses]
  /\ lastAction \in {
       "Init", "ChangeDraftIdentity(A-to-B)", "ChangeDraftIdentity(B-to-A)",
       "StartProposal(P1)", "StartProposal(P2)", "ReplaceProposalOwner",
       "CancelProposalOwner", "ProposalComplete(P1)", "ProposalComplete(P2)",
       "ProposalError(P1)", "ProposalError(P2)",
       "LateProposalCompleteIgnored(P1)", "LateProposalCompleteIgnored(P2)",
       "LateProposalErrorIgnored(P1)", "LateProposalErrorIgnored(P2)",
       "PreviewComplete(P1)", "PreviewComplete(P2)",
       "PreviewError(P1)", "PreviewError(P2)",
       "LatePreviewCompleteIgnored(P1)", "LatePreviewCompleteIgnored(P2)",
       "LatePreviewErrorIgnored(P1)", "LatePreviewErrorIgnored(P2)",
       "RetryProposal(P1-to-P2)",
       "BeginApply(accepted)", "BeginApply(rejected)"
     }
  /\ lastBeginApplyResult \in {"not-called", "accepted", "rejected"}
  /\ lastBeginApplyExpected \in ExpectedProposals
  /\ lastBeginApplyFieldsMatch \in BOOLEAN
  /\ (owner = NoAttempt) <=> (ownerGeneration = NoGeneration)
  /\ (overrideOwner = NoAttempt) <=> (overrideState = NoOverride)
  /\ overrideOwner = NoAttempt \/ overrideOwner = owner

Next ==
  \/ ChangeDraftIdentity
  \/ CancelProposal
  \/ \E attempt \in Attempts: StartProposal(attempt)
  \/ RetryProposal
  \/ \E attempt \in Attempts:
       \E baseMatches \in BOOLEAN:
         \E statusReady \in BOOLEAN:
           \E hasProposalId \in BOOLEAN:
             \E embeddedPreviewMatches \in BOOLEAN:
               ProposalComplete(attempt, baseMatches, statusReady,
                                hasProposalId, embeddedPreviewMatches)
  \/ \E attempt \in Attempts: ProposalError(attempt)
  \/ \E attempt \in Attempts:
       \E isError \in BOOLEAN:
         LateProposalSettlementIgnored(attempt, isError)
  \/ \E attempt \in Attempts:
       \E returnedReceipt \in Receipts:
         \E returnedOutput \in Outputs:
           \E rowsNonNull \in BOOLEAN:
             PreviewComplete(attempt, returnedReceipt, returnedOutput,
                             rowsNonNull)
  \/ \E attempt \in Attempts: PreviewError(attempt)
  \/ \E attempt \in Attempts:
       \E isError \in BOOLEAN:
         LatePreviewSettlementIgnored(attempt, isError)
  \/ \E expectedProposal \in ExpectedProposals:
       BeginApply(expectedProposal)

Spec == Init /\ [][Next]_vars

=============================================================================
