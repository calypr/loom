------------------------- MODULE CoreAuthoringProtocol -------------------------
EXTENDS Naturals, TLC
(*
Composed save/reconcile/preview interaction: one editor-side reconcile attempt of
the saved head plus one competing save attempt, asynchronous message arrival,
persisted immutable receipts and the client admission gate. Abstraction of
internal/server/core_authoring_protocol_test.go witnesses; content is reduced to
head version (1 or 2); receipt capN records the head N captured at compile time.
*)
Targets == {1, 2}
InitialHead == 1
VARIABLES headVersion, rStatus, rTarget, rCaptured, sStatus, sTarget,
          receipts, previewExecuted, previewEcho, clientCurrent, admitted
vars == <<headVersion, rStatus, rTarget, rCaptured, sStatus, sTarget,
          receipts, previewExecuted, previewEcho, clientCurrent, admitted>>
Cap(v) == IF v = 1 THEN "cap1" ELSE "cap2"
Content(r) == IF r = "cap1" THEN 1 ELSE 2
Init ==
 /\ headVersion = InitialHead
 /\ rStatus = "pending" /\ rTarget \in Targets
 /\ rCaptured = 0
 /\ sStatus = "pending" /\ sTarget \in Targets
 /\ receipts = {} /\ previewExecuted = {} /\ previewEcho = "none"
 /\ clientCurrent = InitialHead /\ admitted = "none"
REntry ==
 /\ rStatus = "pending"
 /\ IF rTarget = headVersion
      THEN /\ rStatus' = "entered"
           /\ rCaptured' = headVersion
      ELSE /\ rStatus' = "rejected409"
           /\ UNCHANGED rCaptured
 /\ UNCHANGED <<headVersion, rTarget, sStatus, sTarget, receipts,
                previewExecuted, previewEcho, clientCurrent, admitted>>
RCapture ==
 /\ rStatus = "entered"
 /\ rStatus' = "captured"
 /\ UNCHANGED <<headVersion, rTarget, rCaptured, sStatus, sTarget, receipts,
                previewExecuted, previewEcho, clientCurrent, admitted>>
RPersist ==
 /\ rStatus = "captured"
 /\ receipts' = receipts \cup {Cap(rCaptured)}
 /\ rStatus' = "persisted"
 /\ UNCHANGED <<headVersion, rTarget, sStatus, sTarget, rCaptured,
                previewExecuted, previewEcho, clientCurrent, admitted>>
RComplete ==
 /\ rStatus = "persisted"
 /\ rStatus' = "completed"
 /\ UNCHANGED <<headVersion, rTarget, rCaptured, sStatus, sTarget, receipts,
                previewExecuted, previewEcho, clientCurrent, admitted>>
SCommit ==
 /\ sStatus = "pending" /\ sTarget = headVersion /\ headVersion < 2
 /\ headVersion' = headVersion + 1
 /\ sStatus' = "committed"
 (* save dispatch cancels in-flight and admitted previews (:813-820) *)
 /\ admitted' = "none"
 /\ UNCHANGED <<rStatus, rTarget, rCaptured, sTarget, receipts,
                previewExecuted, previewEcho, clientCurrent>>
SCommitStale ==
 /\ sStatus = "pending" /\ sTarget # headVersion
 /\ sStatus' = "rejected409"
 (* the click cancels previews even when the server later rejects the save *)
 /\ admitted' = "none"
 /\ UNCHANGED <<headVersion, rStatus, rTarget, rCaptured, sTarget, receipts,
                previewExecuted, previewEcho, clientCurrent>>
SAck ==
 /\ sStatus = "committed"
 /\ sStatus' = "acked"
 /\ clientCurrent' = headVersion
 (* the ack reducer rebuilds state without a preview unless the save response
    carries a compile receipt it owns (authoring/model.ts stateFromCommands:
    receipt: accepted, reconciliation: idle otherwise), so an older admitted
    preview loses its receipt backing and cannot be displayed *)
 /\ admitted' = "none"
 /\ UNCHANGED <<headVersion, rStatus, rTarget, rCaptured, sTarget, receipts,
                previewExecuted, previewEcho>>
Preview(r) ==
 (* executions may complete out of order; the latest completion defines the echo *)
 /\ r \in receipts
 /\ previewEcho' = r
 /\ previewExecuted' = previewExecuted \cup {r}
 /\ UNCHANGED <<headVersion, rStatus, rTarget, rCaptured, sStatus, sTarget,
                receipts, clientCurrent, admitted>>
Admit ==
 /\ previewEcho # "none"
 /\ Content(previewEcho) = clientCurrent
 /\ admitted' = previewEcho
 /\ UNCHANGED <<headVersion, rStatus, rTarget, rCaptured, sStatus, sTarget,
                receipts, previewExecuted, previewEcho, clientCurrent>>
Next ==
 REntry \/ RCapture \/ RPersist \/ RComplete
 \/ SCommit \/ SCommitStale \/ SAck
 \/ (\E r \in {"cap1","cap2"} : Preview(r))
 \/ Admit
Spec == Init /\ [][Next]_vars
TypeOK ==
 /\ headVersion \in {1,2} /\ clientCurrent \in {1,2}
 /\ rStatus \in {"pending","entered","captured","persisted","completed","rejected409"}
 /\ rTarget \in Targets \cup {InitialHead}
 /\ sStatus \in {"pending","committed","acked","rejected409"}
 /\ sTarget \in Targets \cup {InitialHead}
 /\ rCaptured \in {0,1,2}
 /\ receipts \subseteq {"cap1","cap2"} /\ previewExecuted \subseteq receipts
 /\ previewEcho \in {"none","cap1","cap2"} /\ admitted \in {"none","cap1","cap2"}
Inv_EntryCASDecidesCompilation == rStatus = "completed" => rCaptured = rTarget
Inv_CaptureEqualsCommittedHead == rStatus \in {"captured","persisted","completed"} => rCaptured \in {1,2}
Inv_ReceiptPersistedForCapture == rStatus \in {"persisted","completed"} => Cap(rCaptured) \in receipts
Inv_PreviewRequiresPersistedReceipt == previewExecuted \subseteq receipts
Inv_ServerEchoesRequestedReceipt == previewEcho \in {"none"} \cup previewExecuted
Inv_NoAdmissionWithoutExecution == admitted # "none" => admitted \in previewExecuted
Inv_StaleAdmissionIsUnreachable == admitted # "none" => Content(admitted) = clientCurrent
Inv_HeadAdvancedOnlyByCommittedSave == headVersion # InitialHead => sStatus \in {"committed","acked"}
Inv_LostAckCannotAdmitAdvancedHead == sStatus = "committed" => admitted # "cap2"
Inv_StaleSaveDoesNotMoveHead == sStatus = "rejected409" => headVersion = InitialHead \/ sTarget # headVersion
=============================================================================
