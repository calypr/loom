--------------------- MODULE PublicationAcknowledgement ---------------------
EXTENDS Naturals, TLC
Inputs == [workspaceAttached: BOOLEAN, ownerMatches: BOOLEAN, explorerMatches: BOOLEAN, draftDigestMatches: BOOLEAN,
           intentMatches: BOOLEAN, snapshotMatches: BOOLEAN, previewRecompiled: BOOLEAN]
Matching(input) == input.workspaceAttached /\ input.ownerMatches /\ input.explorerMatches /\ input.draftDigestMatches
  /\ input.intentMatches /\ input.snapshotMatches
VARIABLES acknowledgementPhase, acknowledgementInput, accepted
vars == <<acknowledgementPhase, acknowledgementInput, accepted>>
Init == /\ acknowledgementPhase = "choose"
        /\ acknowledgementInput = [workspaceAttached |-> TRUE, ownerMatches |-> TRUE, explorerMatches |-> TRUE,
            draftDigestMatches |-> TRUE, intentMatches |-> TRUE, snapshotMatches |-> TRUE,
            previewRecompiled |-> FALSE]
        /\ accepted = FALSE
ChooseAcknowledgement(input) ==
  /\ acknowledgementPhase = "choose"
  /\ acknowledgementInput' = input /\ acknowledgementPhase' = "pending"
  /\ UNCHANGED accepted
ReceiveAcknowledgement ==
  /\ acknowledgementPhase = "pending"
  /\ accepted' = Matching(acknowledgementInput)
  /\ acknowledgementPhase' = "acknowledged" /\ UNCHANGED acknowledgementInput
Next == (\E input \in Inputs : ChooseAcknowledgement(input)) \/ ReceiveAcknowledgement
Spec == Init /\ [][Next]_vars
TypeOK == acknowledgementPhase \in {"choose", "pending", "acknowledged"}
  /\ acknowledgementInput \in Inputs /\ accepted \in BOOLEAN
Inv_OnlyMatchingPublicationAcknowledged == acknowledgementPhase = "acknowledged" /\ accepted
  => Matching(acknowledgementInput)
Inv_MatchingPublicationSurvivesPreviewRecompile == acknowledgementPhase = "acknowledged"
  /\ Matching(acknowledgementInput) => accepted
=============================================================================
