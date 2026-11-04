------------------------- MODULE PopulationMapping -------------------------
EXTENDS Naturals, TLC
Inputs == [authorizedScopeMatches: BOOLEAN, selectionComplete: BOOLEAN, selectionBindingMatches: BOOLEAN, memberReadCompletes: BOOLEAN, mappingCompletes: BOOLEAN]
Trusted(input) == input.authorizedScopeMatches /\ input.selectionComplete /\ input.selectionBindingMatches
VARIABLES populationPhase, populationInput, outcome, memberReads, mappingCalls, counts
vars == <<populationPhase, populationInput, outcome, memberReads, mappingCalls, counts>>
Init == /\ populationPhase = "choose"
        /\ populationInput = [authorizedScopeMatches |-> TRUE, selectionComplete |-> TRUE, selectionBindingMatches |-> TRUE, memberReadCompletes |-> TRUE, mappingCompletes |-> TRUE]
        /\ outcome = "pending" /\ memberReads = 0 /\ mappingCalls = 0 /\ counts = <<>>
ChoosePopulation(input) == /\ populationPhase = "choose"
                           /\ populationInput' = input /\ populationPhase' = "candidate"
                           /\ UNCHANGED <<outcome, memberReads, mappingCalls, counts>>
ClassifyPopulation ==
  /\ populationPhase = "candidate"
  /\ LET trusted == Trusted(populationInput)
         result == IF ~trusted THEN "rejected" ELSE IF ~populationInput.memberReadCompletes \/ ~populationInput.mappingCompletes THEN "incomplete" ELSE "complete"
     IN /\ outcome' = result
        /\ memberReads' = IF trusted THEN 1 ELSE 0
        /\ mappingCalls' = IF trusted /\ populationInput.memberReadCompletes THEN 1 ELSE 0
        /\ counts' = IF result = "complete" THEN <<3, 2, 1, 1>> ELSE <<>>
  /\ populationPhase' = "checked" /\ UNCHANGED populationInput
Next == (\E input \in Inputs : ChoosePopulation(input)) \/ ClassifyPopulation
Spec == Init /\ [][Next]_vars
TypeOK == populationPhase \in {"choose", "candidate", "checked"} /\ populationInput \in Inputs
  /\ outcome \in {"pending", "rejected", "incomplete", "complete"} /\ memberReads \in 0..1 /\ mappingCalls \in 0..1 /\ counts \in {<<>>, <<3, 2, 1, 1>>}
Inv_UntrustedPopulationDoesNotExecuteOrDisclose == populationPhase = "checked" /\ ~Trusted(populationInput)
  => outcome = "rejected" /\ memberReads = 0 /\ mappingCalls = 0 /\ counts = <<>>
Inv_InterruptedPopulationHasNoCounts == populationPhase = "checked" /\ Trusted(populationInput) /\ (~populationInput.memberReadCompletes \/ ~populationInput.mappingCompletes)
  => outcome = "incomplete" /\ counts = <<>> /\ (mappingCalls = IF populationInput.memberReadCompletes THEN 1 ELSE 0)
Inv_CompletePopulationReturnsBoundResult == populationPhase = "checked" /\ Trusted(populationInput) /\ populationInput.memberReadCompletes /\ populationInput.mappingCompletes
  => outcome = "complete" /\ memberReads = 1 /\ mappingCalls = 1 /\ counts = <<3, 2, 1, 1>>
=============================================================================
