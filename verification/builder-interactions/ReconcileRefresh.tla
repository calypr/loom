--------------------------- MODULE ReconcileRefresh ---------------------------
EXTENDS Naturals, TLC

(* BuilderWorkspace.reconcileCurrent recovery, from a refetchBuilder or retry-delay await *)
(* to synchronous syncBuilderData/continue. The call submitted A/F0/g0.      *)
(* AwaitRecovery captures a pending refresh or retry wait; other actions can *)
(* change current identity while its JavaScript continuation is suspended.  *)
(* ResumeRecovery describes the intended current-request gate after await. *)
(* The response is a valid builder DTO for the submitted Explorer; no API,  *)
(* response-content or later reconcile/preview behavior is modeled here.     *)
Owners == {"A", "B"}
Fingerprints == {"F0", "F1"}
Generations == 0..2
Kinds == {"stop", "retry", "backoff"}
VARIABLES refreshPhase, refreshKind, hasData, currentOwner, currentFingerprint,
  currentGeneration, workspaceAttached, cancelled, synchronized, retried
vars == <<refreshPhase, refreshKind, hasData, currentOwner, currentFingerprint,
  currentGeneration, workspaceAttached, cancelled, synchronized, retried>>
CurrentRequest == currentOwner = "A" /\ currentFingerprint = "F0"
  /\ currentGeneration = 0
Init ==
  /\ refreshPhase = "idle" /\ refreshKind = "stop" /\ hasData = FALSE
  /\ currentOwner = "A" /\ currentFingerprint = "F0" /\ currentGeneration = 0
  /\ workspaceAttached = TRUE /\ cancelled = FALSE /\ synchronized = FALSE /\ retried = FALSE
AwaitRecovery(kind, data) ==
  /\ refreshPhase = "idle"
  /\ (kind # "backoff" \/ ~data)
  /\ refreshKind' = kind /\ hasData' = data /\ refreshPhase' = "waiting"
  /\ UNCHANGED <<currentOwner, currentFingerprint, currentGeneration,
      workspaceAttached, cancelled, synchronized, retried>>
ChangeExplorer ==
  /\ refreshPhase = "waiting" /\ workspaceAttached
  /\ currentOwner' = IF currentOwner = "A" THEN "B" ELSE "A"
  /\ UNCHANGED <<refreshPhase, refreshKind, hasData, currentFingerprint,
      currentGeneration, workspaceAttached, cancelled, synchronized, retried>>
EditWorkspace ==
  /\ refreshPhase = "waiting" /\ workspaceAttached
  /\ currentFingerprint' = IF currentFingerprint = "F0" THEN "F1" ELSE "F0"
  /\ UNCHANGED <<refreshPhase, refreshKind, hasData, currentOwner,
      currentGeneration, workspaceAttached, cancelled, synchronized, retried>>
StartNewCompile ==
  /\ refreshPhase = "waiting" /\ workspaceAttached /\ currentGeneration < 2
  /\ currentGeneration' = currentGeneration + 1
  /\ UNCHANGED <<refreshPhase, refreshKind, hasData, currentOwner,
      currentFingerprint, workspaceAttached, cancelled, synchronized, retried>>
CancelOwner ==
  /\ refreshPhase = "waiting" /\ ~cancelled
  /\ cancelled' = TRUE
  /\ UNCHANGED <<refreshPhase, refreshKind, hasData, currentOwner,
      currentFingerprint, currentGeneration, workspaceAttached, synchronized, retried>>
DetachWorkspace ==
  /\ refreshPhase = "waiting" /\ workspaceAttached
  /\ workspaceAttached' = FALSE
  /\ UNCHANGED <<refreshPhase, refreshKind, hasData, currentOwner,
      currentFingerprint, currentGeneration, cancelled, synchronized, retried>>
ResumeRecovery ==
  /\ refreshPhase = "waiting"
  /\ LET admitted == CurrentRequest /\ workspaceAttached /\ ~cancelled /\ (refreshKind = "backoff" \/ hasData)
     IN /\ synchronized' = (admitted /\ refreshKind # "backoff")
        /\ retried' = (admitted /\ refreshKind # "stop")
  /\ refreshPhase' = "done"
  /\ UNCHANGED <<refreshKind, hasData, currentOwner, currentFingerprint,
      currentGeneration, workspaceAttached, cancelled>>
Next == (\E kind \in Kinds, data \in BOOLEAN : AwaitRecovery(kind, data))
  \/ ChangeExplorer \/ EditWorkspace \/ StartNewCompile \/ CancelOwner \/ DetachWorkspace \/ ResumeRecovery
Spec == Init /\ [][Next]_vars
TypeOK == refreshPhase \in {"idle", "waiting", "done"}
  /\ refreshKind \in Kinds /\ hasData \in BOOLEAN
  /\ currentOwner \in Owners /\ currentFingerprint \in Fingerprints
  /\ currentGeneration \in Generations /\ workspaceAttached \in BOOLEAN /\ cancelled \in BOOLEAN
  /\ synchronized \in BOOLEAN /\ retried \in BOOLEAN
Inv_ObsoleteRecoveryDoesNotResume == refreshPhase = "done"
  /\ (~CurrentRequest \/ ~workspaceAttached \/ cancelled) => ~synchronized /\ ~retried
=============================================================================
