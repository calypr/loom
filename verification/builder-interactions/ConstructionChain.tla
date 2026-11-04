------------------------- MODULE ConstructionChain -------------------------
EXTENDS Naturals, TLC

FirstKinds == {"FILTER", "DERIVE"}
References == {"PREVIOUS", "SOURCE", "SELF", "FOREIGN", "REVISION"}
Columns == {"x", "d", "foreign"}
PredecessorColumns(kind) == IF kind = "DERIVE" THEN {"x", "d"} ELSE {"x"}
Inputs == [firstKind: FirstKinds, secondInput: References, secondColumn: Columns]
VARIABLES chainPhase, chainInput, accepted
vars == <<chainPhase, chainInput, accepted>>
Init == /\ chainPhase = "choose"
        /\ chainInput = [firstKind |-> "FILTER", secondInput |-> "PREVIOUS", secondColumn |-> "x"]
        /\ accepted = FALSE
ChooseChain(input) == /\ chainPhase = "choose"
                      /\ chainInput' = input /\ chainPhase' = "candidate"
                      /\ UNCHANGED accepted
ClassifyChain ==
  /\ chainPhase = "candidate"
  /\ LET referenceOK == chainInput.secondInput = "PREVIOUS"
         columnOK == chainInput.secondColumn \in PredecessorColumns(chainInput.firstKind)
     IN accepted' = (referenceOK /\ columnOK)
  /\ chainPhase' = "checked" /\ UNCHANGED chainInput
Next == (\E input \in Inputs : ChooseChain(input)) \/ ClassifyChain
Spec == Init /\ [][Next]_vars
TypeOK == chainPhase \in {"choose", "candidate", "checked"} /\ chainInput \in Inputs /\ accepted \in BOOLEAN
Inv_OnlyImmediatePredecessorAccepted == chainPhase = "checked" /\ accepted => chainInput.secondInput = "PREVIOUS"
Inv_PredecessorSchemaDeterminesAcceptance == chainPhase = "checked" /\ chainInput.secondInput = "PREVIOUS"
  => accepted = (chainInput.secondColumn \in PredecessorColumns(chainInput.firstKind))
=============================================================================
