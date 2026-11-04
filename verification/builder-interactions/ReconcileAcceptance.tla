-------------------------- MODULE ReconcileAcceptance --------------------------
EXTENDS Naturals, FiniteSets, TLC

(* Source: authoring/reconcileAcceptance.ts. This is the synchronous decision   *)
(* frontier after an awaited reconcile and cancellation check, not the await.  *)
(* Submitted owner A/fingerprint F0 are symmetry representatives. Replay also  *)
(* renames both owner/fingerprint pairs and reverses output order on real code. *)
Owners == {"A", "B"}
Fingerprints == {"F0", "F1"}
Versions == 0..2
NoOutput == 3
Inputs == [currentOwner : Owners, currentFingerprint : Fingerprints,
  requestGeneration : Versions, currentGeneration : Versions,
  requestSnapshot : Versions, responseSnapshot : Versions,
  requestOutput : Versions \cup {NoOutput}, responseOutputs : SUBSET Versions,
  diagnostic : {"none", "warning", "error"}]
DefaultInput == [currentOwner |-> "A", currentFingerprint |-> "F0",
  requestGeneration |-> 0, currentGeneration |-> 0,
  requestSnapshot |-> 0, responseSnapshot |-> 0,
  requestOutput |-> NoOutput, responseOutputs |-> {}, diagnostic |-> "none"]
VARIABLES acceptanceInput, phase, outcome, lastAction
vars == <<acceptanceInput, phase, outcome, lastAction>>
IsCurrent == acceptanceInput.currentOwner = "A"
  /\ acceptanceInput.currentFingerprint = "F0"
  /\ acceptanceInput.requestGeneration = acceptanceInput.currentGeneration
ReceiptMatches == acceptanceInput.requestSnapshot = acceptanceInput.responseSnapshot
  /\ (acceptanceInput.requestOutput = NoOutput
      \/ acceptanceInput.requestOutput \in acceptanceInput.responseOutputs)
Init ==
  /\ acceptanceInput = DefaultInput
  /\ phase = "choose" /\ outcome = "none" /\ lastAction = "Init"
ChooseInputs ==
  /\ phase = "choose"
  /\ acceptanceInput' \in Inputs
  /\ phase' = "returned" /\ outcome' = "none" /\ lastAction' = "ChooseInputs"
ClassifyResponse ==
  /\ phase = "returned"
  /\ outcome' = IF ~IsCurrent THEN "obsolete"
       ELSE IF ~ReceiptMatches THEN "mismatch"
       ELSE IF acceptanceInput.diagnostic = "error" THEN "diagnostics"
       ELSE "accepted"
  /\ phase' = "checked" /\ lastAction' = "ClassifyResponse"
  /\ UNCHANGED acceptanceInput
Next == ChooseInputs \/ ClassifyResponse
Spec == Init /\ [][Next]_vars
TypeOK == acceptanceInput \in Inputs
  /\ phase \in {"choose", "returned", "checked"}
  /\ outcome \in {"none", "obsolete", "mismatch", "diagnostics", "accepted"}
Inv_ObsoleteReconcileIsIgnored == phase = "checked" /\ ~IsCurrent => outcome = "obsolete"
Inv_MismatchedReceiptRequiresRepair == phase = "checked" /\ IsCurrent /\ ~ReceiptMatches => outcome = "mismatch"
Inv_ErrorReceiptIsNotAdmitted == phase = "checked" /\ IsCurrent /\ ReceiptMatches
  /\ acceptanceInput.diagnostic = "error" => outcome = "diagnostics"
Inv_CurrentReceiptIsAdmitted == phase = "checked" /\ IsCurrent /\ ReceiptMatches
  /\ acceptanceInput.diagnostic # "error" => outcome = "accepted"
=============================================================================
