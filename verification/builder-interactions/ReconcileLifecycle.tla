--------------------------- MODULE ReconcileLifecycle ---------------------------
EXTENDS Naturals, TLC

(* CurrentRequest abstracts reconcileResultIsCurrent (generation/explorer/    *)
(* workspace fingerprint). Incoming receipt fields and diagnostics are free  *)
(* inputs, not correct fields fabricated by the model. Receipt variables log *)
(* the NEW acceptance event for preview, not retained reducer storage.       *)
Identities == {"A", "B"}
Generations == 0..2
Versions == 0..2
NoIdentity == "none"
Owners == Identities \cup {NoIdentity}
Phases == {"idle", "requesting", "stale", "retryable", "succeeded", "repair"}
MaxRetry == 2
MaxStaleRefresh == 2
VARIABLES currentIdentity, generation, snapshotVersion, outputVersion,
  requestOwner, requestGeneration, requestSnapshot, requestOutput,
  phase, retryCount, staleRefreshCount,
  receiptOwner, receiptGeneration, receiptSnapshot, receiptOutput,
  newAcceptance, lastResponseMatched, lastResponseHadErrors, lastAction
vars == <<currentIdentity, generation, snapshotVersion, outputVersion,
  requestOwner, requestGeneration, requestSnapshot, requestOutput,
  phase, retryCount, staleRefreshCount,
  receiptOwner, receiptGeneration, receiptSnapshot, receiptOutput,
  newAcceptance, lastResponseMatched, lastResponseHadErrors, lastAction>>
Init ==
  /\ currentIdentity = "A" /\ generation = 0
  /\ snapshotVersion = 0 /\ outputVersion = 0
  /\ requestOwner = NoIdentity /\ requestGeneration = 0
  /\ requestSnapshot = 0 /\ requestOutput = 0
  /\ phase = "idle" /\ retryCount = 0 /\ staleRefreshCount = 0
  /\ receiptOwner = NoIdentity /\ receiptGeneration = 0
  /\ receiptSnapshot = 0 /\ receiptOutput = 0
  /\ newAcceptance = FALSE /\ lastResponseMatched = FALSE
  /\ lastResponseHadErrors = FALSE /\ lastAction = "Init"
CurrentRequest == requestOwner = currentIdentity /\ requestGeneration = generation
StartReconcile ==
  /\ phase \in {"idle", "succeeded", "repair"}
  /\ requestOwner' = currentIdentity /\ requestGeneration' = generation
  /\ requestSnapshot' = snapshotVersion /\ requestOutput' = outputVersion
  /\ phase' = "requesting" /\ retryCount' = 0 /\ staleRefreshCount' = 0
  /\ receiptOwner' = NoIdentity /\ receiptGeneration' = 0
  /\ receiptSnapshot' = 0 /\ receiptOutput' = 0
  /\ newAcceptance' = FALSE /\ lastAction' = "StartReconcile"
  /\ UNCHANGED <<currentIdentity, generation, snapshotVersion, outputVersion,
       lastResponseMatched, lastResponseHadErrors>>
ChangeWorkspace ==
  /\ generation < 2
  /\ currentIdentity' = IF currentIdentity = "A" THEN "B" ELSE "A"
  /\ generation' = generation + 1
  /\ phase' = IF phase = "requesting" THEN phase ELSE "idle"
  /\ receiptOwner' = NoIdentity /\ receiptGeneration' = 0
  /\ receiptSnapshot' = 0 /\ receiptOutput' = 0
  /\ newAcceptance' = FALSE /\ lastAction' = "ChangeWorkspace"
  /\ UNCHANGED <<snapshotVersion, outputVersion, requestOwner, requestGeneration,
       requestSnapshot, requestOutput, retryCount, staleRefreshCount,
       lastResponseMatched, lastResponseHadErrors>>
ChangeSnapshot ==
  /\ snapshotVersion' = (snapshotVersion + 1) % 3
  /\ phase' = IF phase = "requesting" THEN phase ELSE "idle"
  /\ receiptOwner' = NoIdentity /\ receiptGeneration' = 0
  /\ receiptSnapshot' = 0 /\ receiptOutput' = 0
  /\ newAcceptance' = FALSE /\ lastAction' = "ChangeSnapshot"
  /\ UNCHANGED <<currentIdentity, generation, outputVersion, requestOwner,
       requestGeneration, requestSnapshot, requestOutput, retryCount, staleRefreshCount,
       lastResponseMatched, lastResponseHadErrors>>
ChangeOutput ==
  /\ outputVersion' = (outputVersion + 1) % 3
  /\ phase' = IF phase = "requesting" THEN phase ELSE "idle"
  /\ receiptOwner' = NoIdentity /\ receiptGeneration' = 0
  /\ receiptSnapshot' = 0 /\ receiptOutput' = 0
  /\ newAcceptance' = FALSE /\ lastAction' = "ChangeOutput"
  /\ UNCHANGED <<currentIdentity, generation, snapshotVersion, requestOwner,
       requestGeneration, requestSnapshot, requestOutput, retryCount, staleRefreshCount,
       lastResponseMatched, lastResponseHadErrors>>
ResponseArrives(incomingSnapshot, incomingOutput, hasErrors) ==
  /\ phase = "requesting"
  /\ LET matched == incomingSnapshot = requestSnapshot /\ incomingOutput = requestOutput
         accepted == CurrentRequest /\ matched /\ ~hasErrors
     IN /\ lastResponseMatched' = matched
        /\ lastResponseHadErrors' = hasErrors
        /\ newAcceptance' = accepted
        /\ phase' = IF ~CurrentRequest THEN "idle" ELSE IF accepted THEN "succeeded" ELSE "repair"
        /\ receiptOwner' = IF accepted THEN requestOwner ELSE NoIdentity
        /\ receiptGeneration' = IF accepted THEN requestGeneration ELSE 0
        /\ receiptSnapshot' = IF accepted THEN incomingSnapshot ELSE 0
        /\ receiptOutput' = IF accepted THEN incomingOutput ELSE 0
  /\ lastAction' = "ResponseArrives"
  /\ UNCHANGED <<currentIdentity, generation, snapshotVersion, outputVersion,
       requestOwner, requestGeneration, requestSnapshot, requestOutput, retryCount, staleRefreshCount>>
RetryableFailure ==
  /\ phase = "requesting"
  /\ phase' = IF CurrentRequest THEN "retryable" ELSE "idle"
  /\ receiptOwner' = NoIdentity /\ receiptGeneration' = 0
  /\ receiptSnapshot' = 0 /\ receiptOutput' = 0
  /\ newAcceptance' = FALSE /\ lastAction' = "RetryableFailure"
  /\ UNCHANGED <<currentIdentity, generation, snapshotVersion, outputVersion,
       requestOwner, requestGeneration, requestSnapshot, requestOutput,
       retryCount, staleRefreshCount, lastResponseMatched, lastResponseHadErrors>>
StaleSnapshotFailure ==
  /\ phase = "requesting"
  /\ phase' = IF CurrentRequest THEN "stale" ELSE "idle"
  /\ receiptOwner' = NoIdentity /\ receiptGeneration' = 0
  /\ receiptSnapshot' = 0 /\ receiptOutput' = 0
  /\ newAcceptance' = FALSE /\ lastAction' = "StaleSnapshotFailure"
  /\ UNCHANGED <<currentIdentity, generation, snapshotVersion, outputVersion,
       requestOwner, requestGeneration, requestSnapshot, requestOutput,
       retryCount, staleRefreshCount, lastResponseMatched, lastResponseHadErrors>>
Retry ==
  /\ phase = "retryable" /\ retryCount + staleRefreshCount < MaxRetry
  /\ retryCount' = retryCount + 1 /\ phase' = "requesting"
  /\ receiptOwner' = NoIdentity /\ receiptGeneration' = 0
  /\ receiptSnapshot' = 0 /\ receiptOutput' = 0
  /\ newAcceptance' = FALSE /\ lastAction' = "Retry"
  /\ UNCHANGED <<currentIdentity, generation, snapshotVersion, outputVersion,
       requestOwner, requestGeneration, requestSnapshot, requestOutput, staleRefreshCount,
       lastResponseMatched, lastResponseHadErrors>>
RefreshAndRetry ==
  /\ phase = "stale" /\ staleRefreshCount < MaxStaleRefresh
  /\ staleRefreshCount' = staleRefreshCount + 1
  /\ requestSnapshot' = snapshotVersion /\ phase' = "requesting"
  /\ receiptOwner' = NoIdentity /\ receiptGeneration' = 0
  /\ receiptSnapshot' = 0 /\ receiptOutput' = 0
  /\ newAcceptance' = FALSE /\ lastAction' = "RefreshAndRetry"
  /\ UNCHANGED <<currentIdentity, generation, snapshotVersion, outputVersion,
       requestOwner, requestGeneration, requestOutput, retryCount,
       lastResponseMatched, lastResponseHadErrors>>
GiveUpForRepair ==
  /\ \/ /\ phase = "retryable" /\ retryCount + staleRefreshCount >= MaxRetry
     \/ /\ phase = "stale" /\ staleRefreshCount = MaxStaleRefresh
  /\ phase' = "repair" /\ receiptOwner' = NoIdentity /\ receiptGeneration' = 0
  /\ receiptSnapshot' = 0 /\ receiptOutput' = 0
  /\ newAcceptance' = FALSE /\ lastAction' = "GiveUpForRepair"
  /\ UNCHANGED <<currentIdentity, generation, snapshotVersion, outputVersion,
       requestOwner, requestGeneration, requestSnapshot, requestOutput, retryCount, staleRefreshCount,
       lastResponseMatched, lastResponseHadErrors>>
Next ==
  \/ StartReconcile \/ ChangeWorkspace \/ ChangeSnapshot \/ ChangeOutput
  \/ \E snapshot \in Versions, output \in Versions, errors \in BOOLEAN : ResponseArrives(snapshot, output, errors)
  \/ RetryableFailure \/ StaleSnapshotFailure \/ Retry \/ RefreshAndRetry \/ GiveUpForRepair
Spec == Init /\ [][Next]_vars
TypeOK ==
  /\ currentIdentity \in Identities /\ generation \in Generations
  /\ snapshotVersion \in Versions /\ outputVersion \in Versions
  /\ requestOwner \in Owners /\ requestGeneration \in Generations
  /\ requestSnapshot \in Versions /\ requestOutput \in Versions
  /\ phase \in Phases /\ retryCount \in 0..MaxRetry /\ staleRefreshCount \in 0..MaxStaleRefresh
  /\ receiptOwner \in Owners /\ receiptGeneration \in Generations
  /\ receiptSnapshot \in Versions /\ receiptOutput \in Versions
  /\ newAcceptance \in BOOLEAN /\ lastResponseMatched \in BOOLEAN /\ lastResponseHadErrors \in BOOLEAN
Inv_AcceptedReceiptIsCurrent == newAcceptance => receiptOwner = currentIdentity /\ receiptGeneration = generation
Inv_AcceptedReceiptMatchesRequest == newAcceptance => receiptSnapshot = requestSnapshot
Inv_ReceiptSelectsRequestedOutput == newAcceptance => receiptOutput = requestOutput
Inv_NoNewAcceptanceOnFailure == newAcceptance => phase = "succeeded" /\ lastResponseMatched /\ ~lastResponseHadErrors
Inv_RetryBounds == retryCount <= MaxRetry /\ staleRefreshCount <= MaxStaleRefresh
=============================================================================
