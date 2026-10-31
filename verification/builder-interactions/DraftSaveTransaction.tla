----------------------- MODULE DraftSaveTransaction -----------------------
EXTENDS Naturals, TLC
VARIABLES phase, outcomes, stagedHistory, stagedHead, history, head, transactionResult, result, backendConflict
vars == <<phase, outcomes, stagedHistory, stagedHead, history, head, transactionResult, result, backendConflict>>
EmptyOutcomes == [begin |-> "none", read |-> "none", archive |-> "none", update |-> "none", commit |-> "none"]
Init == /\ phase = "begin" /\ outcomes = EmptyOutcomes
        /\ stagedHistory = FALSE /\ stagedHead = FALSE /\ history = FALSE /\ head = FALSE
        /\ transactionResult = "none" /\ result = "none" /\ backendConflict = FALSE
Begin(outcome) ==
 /\ phase = "begin" /\ outcome \in {"ok", "conflict", "other"}
 /\ outcomes' = [outcomes EXCEPT !.begin = outcome]
 /\ phase' = IF outcome = "ok" THEN "read" ELSE "return"
 /\ transactionResult' = IF outcome = "ok" THEN "none" ELSE outcome
 /\ backendConflict' = (outcome = "conflict")
 /\ UNCHANGED <<stagedHistory, stagedHead, history, head, result>>
Query(stage, outcome, next) ==
 /\ phase = stage /\ outcome \in (IF stage = "archive" THEN {"ok", "conflict", "other"} ELSE {"ok", "missing", "conflict", "other"})
 /\ outcomes' = [outcomes EXCEPT ![stage] = outcome]
 /\ phase' = IF outcome = "ok" THEN next ELSE "abort"
 /\ transactionResult' = IF outcome = "missing" THEN "domain" ELSE IF outcome = "ok" THEN "none" ELSE outcome
 /\ backendConflict' = (outcome = "conflict")
 /\ stagedHistory' = (stagedHistory \/ (stage = "archive" /\ outcome = "ok"))
 /\ stagedHead' = (stagedHead \/ (stage = "update" /\ outcome = "ok"))
 /\ UNCHANGED <<history, head, result>>
Abort == /\ phase = "abort" /\ phase' = "return"
         /\ stagedHistory' = FALSE /\ stagedHead' = FALSE
         /\ UNCHANGED <<outcomes, history, head, transactionResult, result, backendConflict>>
Commit(outcome) ==
 /\ phase = "commit" /\ outcome \in {"ok", "conflict", "reject", "unknown"}
 /\ outcomes' = [outcomes EXCEPT !.commit = outcome]
 /\ phase' = "return"
 /\ history' = (stagedHistory /\ outcome \in {"ok", "unknown"}) /\ head' = (stagedHead /\ outcome \in {"ok", "unknown"})
 /\ transactionResult' = IF outcome = "reject" THEN "other" ELSE IF outcome = "unknown" THEN "unknown" ELSE outcome
 /\ backendConflict' = (outcome = "conflict")
 /\ UNCHANGED <<stagedHistory, stagedHead, result>>
Return ==
 /\ phase = "return" /\ phase' = "done"
 /\ result' = IF transactionResult \in {"conflict", "domain"} THEN "draft-conflict" ELSE IF transactionResult = "ok" THEN "success" ELSE "other-error"
 /\ UNCHANGED <<outcomes, stagedHistory, stagedHead, history, head, transactionResult, backendConflict>>
Next == (\E o \in {"ok", "conflict", "other"} : Begin(o))
 \/ (\E o \in {"ok", "missing", "conflict", "other"} : Query("read", o, "archive"))
 \/ (\E o \in {"ok", "conflict", "other"} : Query("archive", o, "update"))
 \/ (\E o \in {"ok", "missing", "conflict", "other"} : Query("update", o, "commit"))
 \/ Abort \/ (\E o \in {"ok", "conflict", "reject", "unknown"} : Commit(o)) \/ Return
Spec == Init /\ [][Next]_vars
TypeOK == /\ phase \in {"begin", "read", "archive", "update", "commit", "abort", "return", "done"}
          /\ stagedHistory \in BOOLEAN /\ stagedHead \in BOOLEAN /\ history \in BOOLEAN /\ head \in BOOLEAN /\ backendConflict \in BOOLEAN
          /\ result \in {"none", "success", "draft-conflict", "other-error"}
Inv_NoPartialDraftHistory == history = head
Inv_ConflictIsDomainConflict == phase = "done" /\ backendConflict => result = "draft-conflict"
Inv_SuccessRequiresConfirmedCommit == phase = "done" /\ result = "success" => outcomes.commit = "ok" /\ history /\ head
Inv_MissingCASAborts == phase = "done" /\ (outcomes.read = "missing" \/ outcomes.update = "missing") => result = "draft-conflict" /\ ~history /\ ~head
Inv_ValidSaveWorks == phase = "done" /\ outcomes.commit = "ok" => result = "success"
Inv_KnownFailureDoesNotPublish == phase = "done" /\ (outcomes.begin \in {"conflict", "other"} \/ outcomes.read \in {"missing", "conflict", "other"} \/ outcomes.archive \in {"conflict", "other"} \/ outcomes.update \in {"missing", "conflict", "other"} \/ outcomes.commit \in {"conflict", "reject"}) => ~history /\ ~head
=============================================================================
