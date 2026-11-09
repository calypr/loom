------------------------- MODULE PopulationRemoval -------------------------
EXTENDS Naturals, TLC
Inputs == [receiptMatches: BOOLEAN, provenanceMatches: BOOLEAN, authorizationMatches: BOOLEAN, previewCompletes: BOOLEAN, concurrentDraftChange: BOOLEAN]
Trusted(input) == input.receiptMatches /\ input.provenanceMatches /\ input.authorizationMatches
VARIABLES removalPhase, removalInput, accepted, previewCalls, saveAttempts, successfulSaves, preservedHead, exactCandidate, conflict
vars == <<removalPhase, removalInput, accepted, previewCalls, saveAttempts, successfulSaves, preservedHead, exactCandidate, conflict>>
Init == /\ removalPhase = "choose"
 /\ removalInput = [receiptMatches |-> TRUE, provenanceMatches |-> TRUE, authorizationMatches |-> TRUE, previewCompletes |-> TRUE, concurrentDraftChange |-> TRUE]
 /\ accepted = FALSE /\ previewCalls = 0 /\ saveAttempts = 0 /\ successfulSaves = 0 /\ preservedHead = TRUE /\ exactCandidate = FALSE /\ conflict = FALSE
ChooseRemoval(input) == /\ removalPhase = "choose" /\ removalInput' = input /\ removalPhase' = "candidate"
 /\ UNCHANGED <<accepted, previewCalls, saveAttempts, successfulSaves, preservedHead, exactCandidate, conflict>>
ApplyRemoval ==
 /\ removalPhase = "candidate"
 /\ LET trusted == Trusted(removalInput)
        previewed == trusted /\ removalInput.previewCompletes
        committed == previewed /\ ~removalInput.concurrentDraftChange
    IN /\ accepted' = committed /\ previewCalls' = (IF trusted THEN 1 ELSE 0)
       /\ saveAttempts' = (IF previewed THEN 1 ELSE 0) /\ successfulSaves' = (IF committed THEN 1 ELSE 0)
       /\ preservedHead' = ~committed /\ exactCandidate' = committed
       /\ conflict' = (previewed /\ removalInput.concurrentDraftChange)
 /\ removalPhase' = "checked" /\ UNCHANGED removalInput
Next == (\E input \in Inputs : ChooseRemoval(input)) \/ ApplyRemoval
Spec == Init /\ [][Next]_vars
TypeOK == removalPhase \in {"choose", "candidate", "checked"} /\ removalInput \in Inputs
 /\ accepted \in BOOLEAN /\ previewCalls \in 0..1 /\ saveAttempts \in 0..1 /\ successfulSaves \in 0..1 /\ preservedHead \in BOOLEAN /\ exactCandidate \in BOOLEAN /\ conflict \in BOOLEAN
Inv_RemovalApplyRequiresExactReceipt == removalPhase = "checked" /\ ~removalInput.receiptMatches
 => ~accepted /\ previewCalls = 0 /\ saveAttempts = 0 /\ successfulSaves = 0 /\ preservedHead
Inv_RemovalRequiresExactCandidate == removalPhase = "checked" /\ removalInput.receiptMatches /\ (~removalInput.provenanceMatches \/ ~removalInput.authorizationMatches)
 => ~accepted /\ saveAttempts = 0 /\ successfulSaves = 0 /\ preservedHead
Inv_RemovalFailureKeepsCollection == removalPhase = "checked" /\ Trusted(removalInput) /\ ~removalInput.previewCompletes
 => ~accepted /\ previewCalls = 1 /\ saveAttempts = 0 /\ successfulSaves = 0 /\ preservedHead
Inv_RemovalCannotOverwriteCompetingDraft == removalPhase = "checked" /\ Trusted(removalInput) /\ removalInput.previewCompletes /\ removalInput.concurrentDraftChange
 => ~accepted /\ previewCalls = 1 /\ saveAttempts = 1 /\ successfulSaves = 0 /\ preservedHead /\ conflict
Inv_ValidRemovalChangesOnlyPopulationRevision == removalPhase = "checked" /\ Trusted(removalInput) /\ removalInput.previewCompletes /\ ~removalInput.concurrentDraftChange
 => accepted /\ previewCalls = 1 /\ saveAttempts = 1 /\ successfulSaves = 1 /\ ~preservedHead /\ exactCandidate /\ ~conflict
=============================================================================
