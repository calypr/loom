---- MODULE RootChange ----
EXTENDS Naturals, TLC

CONSTANT MaxEpoch

Epochs == 0..MaxEpoch
OptionalEpochs == 0..MaxEpoch

VARIABLES
  phase, currentEpoch, requestEpoch,
  pendingStatus, pendingEpoch, appliedEpoch, lastAction

vars == <<
  phase, currentEpoch, requestEpoch,
  pendingStatus, pendingEpoch, appliedEpoch, lastAction
>>

TypeOK ==
  /\ phase \in {"idle", "assessing", "blocked", "previewing", "failed"}
  /\ currentEpoch \in 1..MaxEpoch
  /\ requestEpoch \in 1..MaxEpoch
  /\ pendingStatus \in {"none", "loading", "ready"}
  /\ pendingEpoch \in 1..MaxEpoch
  /\ appliedEpoch \in OptionalEpochs
  /\ lastAction \in {
       "Init", "BeginChange", "WorkspaceChanged", "AssessmentNoChange",
       "AssessmentBlocked", "AssessmentStale", "AssessmentReady",
       "PreviewReady", "PreviewUnavailable", "ApplyPreview", "Cancel"
     }

Init ==
  /\ phase = "idle"
  /\ currentEpoch = 1
  /\ requestEpoch = 1
  /\ pendingStatus = "none"
  /\ pendingEpoch = 1
  /\ appliedEpoch = 0
  /\ lastAction = "Init"

BeginChange ==
  /\ currentEpoch' =
       IF currentEpoch < MaxEpoch THEN currentEpoch + 1 ELSE currentEpoch
  /\ requestEpoch' = currentEpoch'
  /\ phase' = "assessing"
  /\ pendingStatus' = "none"
  /\ lastAction' = "BeginChange"
  /\ UNCHANGED <<pendingEpoch, appliedEpoch>>

WorkspaceChanged ==
  /\ currentEpoch' =
       IF currentEpoch < MaxEpoch THEN currentEpoch + 1 ELSE currentEpoch
  /\ phase' = IF phase = "assessing" THEN "assessing" ELSE "idle"
  /\ pendingStatus' = "none"
  /\ lastAction' = "WorkspaceChanged"
  /\ UNCHANGED <<requestEpoch, pendingEpoch, appliedEpoch>>

AssessmentNoChange ==
  /\ phase = "assessing"
  /\ requestEpoch = currentEpoch
  /\ phase' = "idle"
  /\ lastAction' = "AssessmentNoChange"
  /\ UNCHANGED <<currentEpoch, requestEpoch, pendingStatus, pendingEpoch, appliedEpoch>>

AssessmentBlocked ==
  /\ phase = "assessing"
  /\ requestEpoch = currentEpoch
  /\ phase' = "blocked"
  /\ lastAction' = "AssessmentBlocked"
  /\ UNCHANGED <<currentEpoch, requestEpoch, pendingStatus, pendingEpoch, appliedEpoch>>

AssessmentStale ==
  /\ phase = "assessing"
  /\ requestEpoch # currentEpoch
  /\ phase' = "failed"
  /\ pendingStatus' = "none"
  /\ lastAction' = "AssessmentStale"
  /\ UNCHANGED <<currentEpoch, requestEpoch, pendingEpoch, appliedEpoch>>

AssessmentReady ==
  /\ phase = "assessing"
  /\ requestEpoch = currentEpoch
  /\ phase' = "previewing"
  /\ pendingStatus' = "loading"
  /\ pendingEpoch' = requestEpoch
  /\ lastAction' = "AssessmentReady"
  /\ UNCHANGED <<currentEpoch, requestEpoch, appliedEpoch>>

PreviewReady ==
  /\ phase = "previewing"
  /\ pendingStatus = "loading"
  /\ pendingEpoch = currentEpoch
  /\ phase' = "idle"
  /\ pendingStatus' = "ready"
  /\ lastAction' = "PreviewReady"
  /\ UNCHANGED <<currentEpoch, requestEpoch, pendingEpoch, appliedEpoch>>

PreviewUnavailable ==
  /\ phase = "previewing"
  /\ phase' = "failed"
  /\ pendingStatus' = "none"
  /\ lastAction' = "PreviewUnavailable"
  /\ UNCHANGED <<currentEpoch, requestEpoch, pendingEpoch, appliedEpoch>>

ApplyPreview ==
  /\ pendingStatus = "ready"
  /\ pendingEpoch = currentEpoch
  /\ appliedEpoch' = pendingEpoch
  /\ pendingStatus' = "none"
  /\ lastAction' = "ApplyPreview"
  /\ UNCHANGED <<phase, currentEpoch, requestEpoch, pendingEpoch>>

Cancel ==
  /\ currentEpoch' =
       IF currentEpoch < MaxEpoch THEN currentEpoch + 1 ELSE currentEpoch
  /\ phase' = "idle"
  /\ pendingStatus' = "none"
  /\ lastAction' = "Cancel"
  /\ UNCHANGED <<requestEpoch, pendingEpoch, appliedEpoch>>

Next ==
  \/ BeginChange
  \/ WorkspaceChanged
  \/ AssessmentNoChange
  \/ AssessmentBlocked
  \/ AssessmentStale
  \/ AssessmentReady
  \/ PreviewReady
  \/ PreviewUnavailable
  \/ ApplyPreview
  \/ Cancel

Spec == Init /\ [][Next]_vars

Inv_ApplyOnlyCurrentPreview ==
  lastAction = "ApplyPreview" =>
    /\ appliedEpoch = pendingEpoch
    /\ pendingEpoch = currentEpoch

Inv_PendingPreviewMatchesEpoch ==
  pendingStatus # "none" => pendingEpoch = currentEpoch

Inv_BlockedMatchesEpoch ==
  phase = "blocked" => requestEpoch = currentEpoch

Inv_CancelInvalidatesPreview ==
  lastAction = "Cancel" => pendingStatus = "none"

Inv_EpochBounds ==
  /\ currentEpoch \in 1..MaxEpoch
  /\ requestEpoch \in 1..MaxEpoch
  /\ pendingEpoch \in 1..MaxEpoch

====
