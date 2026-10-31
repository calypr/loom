--------------------------- MODULE ApplySettlementRace ---------------------------
EXTENDS Naturals, FiniteSets, TLC

(* Source: constructionApplyOwnership.beginConstructionApply and its finish. *)
(* SetCurrentOwner/ChangeContext are synchronous inputs to the live-identity    *)
(* reader, not a model of proposal HTTP/React scheduling. Full hook tests     *)
(* separately exercise that integration boundary. One outstanding per owner.    *)
(* A pending applyCommandsWithResult promise outlives cancel's ref reset.      *)
(* Owner A/B means two Apply attempts, not receipt IDs or workspace identities.   *)
Owners == {"A", "B"}
None == "none"
VARIABLES currentContext, ownerContext, currentOwner, applyingOwner, usedOwners, pendingApplies,
  responseOwner, settledOwner, priorApplyingOwner, priorCurrentOwner, inputOwner, lastApplied, lastAction
vars == <<currentContext, ownerContext, currentOwner, applyingOwner, usedOwners, pendingApplies,
  responseOwner, settledOwner, priorApplyingOwner, priorCurrentOwner, inputOwner, lastApplied, lastAction>>
Init ==
  /\ currentContext = "W1"
  /\ ownerContext = [owner \in Owners |-> "none"]
  /\ currentOwner = None
  /\ applyingOwner = None
  /\ usedOwners = {}
  /\ pendingApplies = {}
  /\ responseOwner = None
  /\ settledOwner = None
  /\ priorApplyingOwner = None
  /\ priorCurrentOwner = None
  /\ inputOwner = None
  /\ lastApplied = TRUE
  /\ lastAction = "Init"

SetCurrentOwner(owner) ==
  /\ owner \in Owners \ usedOwners
  /\ inputOwner' = owner
  /\ currentOwner' = owner
  /\ ownerContext' = [ownerContext EXCEPT ![owner] = currentContext]
  /\ usedOwners' = usedOwners \cup {owner}
  /\ lastAction' = "SetCurrentOwner"
  /\ UNCHANGED <<currentContext, applyingOwner, pendingApplies, responseOwner, settledOwner, priorApplyingOwner, priorCurrentOwner, lastApplied>>
BeginApply ==
  /\ inputOwner' = None
  /\ currentOwner # None
  /\ applyingOwner = None
  /\ ownerContext[currentOwner] = currentContext
  /\ currentOwner \notin pendingApplies
  /\ applyingOwner' = currentOwner
  /\ pendingApplies' = pendingApplies \cup {currentOwner}
  /\ lastAction' = "BeginApply"
  /\ UNCHANGED <<currentContext, ownerContext, currentOwner, usedOwners, responseOwner, settledOwner, priorApplyingOwner, priorCurrentOwner, lastApplied>>
Cancel ==
  /\ inputOwner' = None
  /\ currentOwner # None
  /\ currentOwner' = None
  /\ applyingOwner' = None
  /\ lastAction' = "Cancel"
  /\ UNCHANGED <<currentContext, ownerContext, usedOwners, pendingApplies, responseOwner, settledOwner, priorApplyingOwner, priorCurrentOwner, lastApplied>>
ApplyReturns(owner, applied) ==
  /\ owner \in pendingApplies
  /\ inputOwner' = owner
  /\ responseOwner' = owner
  /\ lastApplied' = applied
  (* The returned completion handle identifies its initiating Apply attempt. *)
  /\ priorApplyingOwner' = applyingOwner
  /\ priorCurrentOwner' = currentOwner
  /\ settledOwner' = IF applyingOwner = owner /\ currentOwner = owner /\ ownerContext[owner] = currentContext THEN owner ELSE None
  /\ applyingOwner' = IF applyingOwner = owner THEN None ELSE applyingOwner
  /\ pendingApplies' = pendingApplies \ {owner}
  /\ currentOwner' = IF applied /\ applyingOwner = owner /\ ownerContext[owner] = currentContext /\ applyingOwner = currentOwner THEN None ELSE currentOwner
  /\ lastAction' = "ApplyReturns"
  /\ UNCHANGED <<currentContext, ownerContext, usedOwners>>
ChangeContext ==
  /\ inputOwner' = None
  /\ currentContext' = IF currentContext = "W1" THEN "W2" ELSE "W1"
  /\ lastAction' = "ChangeContext"
  /\ UNCHANGED <<ownerContext, currentOwner, applyingOwner, usedOwners,
       pendingApplies, responseOwner, settledOwner, priorApplyingOwner, priorCurrentOwner, lastApplied>>
Next ==
  \/ \E owner \in Owners : SetCurrentOwner(owner)
  \/ BeginApply
  \/ Cancel
  \/ ChangeContext
  \/ \E owner \in Owners, applied \in BOOLEAN : ApplyReturns(owner, applied)
Spec == Init /\ [][Next]_vars
Inv_ApplyReturnSettlesItsOwnOwner ==
  lastAction = "ApplyReturns" => settledOwner = None \/ settledOwner = responseOwner
Inv_OldReturnPreservesCurrentApply ==
  lastAction = "ApplyReturns" /\ responseOwner # priorApplyingOwner =>
    /\ applyingOwner = priorApplyingOwner
    /\ currentOwner = priorCurrentOwner
Inv_CompletionOwnsCurrentContext ==
  lastAction = "ApplyReturns" /\ settledOwner # None => ownerContext[settledOwner] = currentContext
=============================================================================
