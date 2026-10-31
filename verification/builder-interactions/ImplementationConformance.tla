------------------------ MODULE ImplementationConformance ------------------------
EXTENDS Naturals, Sequences, TLC

(* Traces are fresh observations of mounted production hooks/components.      *)
(* This checks their safety obligations, not full behavioral refinement.      *)
CONSTANT Traces
VARIABLES trace, position, observation, previousObservation
vars == <<trace, position, observation, previousObservation>>

Init ==
  /\ trace \in 1..Len(Traces)
  /\ position = 1
  /\ observation = Traces[trace][1]
  /\ previousObservation = observation

Observe ==
  /\ position < Len(Traces[trace])
  /\ position' = position + 1
  /\ observation' = Traces[trace][position']
  /\ previousObservation' = observation
  /\ UNCHANGED trace

Spec == Init /\ [][Observe]_vars
Previous == previousObservation
Hook == observation.kind = "lifecycle"
Reviewed ==
  /\ observation.proposalId # ""
  /\ observation.proposalOutput = observation.currentOutput
  /\ observation.proposalSnapshot = observation.currentSnapshot
  /\ observation.proposalVersion = observation.currentVersion
  /\ observation.proposalDigest = observation.currentDigest
  /\ observation.previewReceipt = observation.proposalId
  /\ observation.previewOutput = observation.currentOutput
  /\ observation.rowsPresent

Inv_VisibleReadyIsReviewed ==
  Hook /\ observation.status \in {"ready", "applying"} => Reviewed
Inv_ApplyRequiresReadyPreview ==
  Hook /\ observation.canApply => observation.status = "ready" /\ Reviewed
Inv_BeginApplyWasReviewed ==
  Hook /\ observation.event = "begin-apply" /\ observation.status = "applying" =>
    /\ Previous.status = "ready"
    /\ Previous.canApply
    /\ observation.proposalId = Previous.proposalId

(* Compare real rendered observations, not expected states from action labels. *)
Visible == <<observation.status, observation.proposalId, observation.canApply,
  observation.previewReceipt, observation.previewOutput>>
PreviousVisible == <<Previous.status, Previous.proposalId, Previous.canApply,
  Previous.previewReceipt, Previous.previewOutput>>
Inv_LateResponseDoesNotReplaceOwner ==
  Hook /\ observation.staleEvent => Visible = PreviousVisible
Inv_OldApplyDoesNotClearReplacement ==
  Hook /\ observation.event = "finish-apply"
    /\ observation.actorAttempt # ""
    /\ observation.actorAttempt # Previous.activeApplyAttempt =>
    Visible = PreviousVisible

Inv_AcceptedCompletionOwnsCurrentContext ==
  Hook /\ observation.completionObserved /\ observation.completionAccepted =>
    /\ observation.actorContext # ""
    /\ observation.actorContext = observation.currentContext

Inv_RenderedReconcileReceiptIsCurrent ==
  observation.kind = "reconcile" /\ observation.receiptId # "" =>
    /\ observation.receiptWorkspace = observation.currentWorkspace
    /\ observation.receiptSnapshot = observation.currentSnapshot
    /\ observation.receiptOutput = observation.currentOutput
    /\ observation.currentOutput \in {observation.receiptOutputs[i] : i \in 1..Len(observation.receiptOutputs)}

=============================================================================
