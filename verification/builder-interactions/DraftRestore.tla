---- MODULE DraftRestore ----
EXTENDS Naturals, TLC

CONSTANT MaxDraftVersion

DraftVersions == 0..MaxDraftVersion
OptionalVersions == 0..MaxDraftVersion

VARIABLES
  currentVersion, previousVersion, requestedVersion, restoredVersion,
  restoring, lastAction

vars == <<currentVersion, previousVersion, requestedVersion, restoredVersion, restoring, lastAction>>

TypeOK ==
  /\ currentVersion \in 0..MaxDraftVersion
  /\ previousVersion \in OptionalVersions
  /\ requestedVersion \in OptionalVersions
  /\ restoredVersion \in DraftVersions
  /\ restoring \in BOOLEAN
  /\ lastAction \in {
       "Init", "SaveDraft", "StartRestore", "RestoreComplete",
       "CancelRestore", "StaleDraft"
     }

Init ==
  /\ currentVersion = 0
  /\ previousVersion = 0
  /\ requestedVersion = 0
  /\ restoredVersion = 0
  /\ restoring = FALSE
  /\ lastAction = "Init"

SaveDraft(version) ==
  /\ version \in 1..MaxDraftVersion
  /\ version # currentVersion
  /\ currentVersion' = version
  /\ previousVersion' = currentVersion
  /\ requestedVersion' = 0
  /\ restoredVersion' = restoredVersion
  /\ restoring' = FALSE
  /\ lastAction' = "SaveDraft"

StartRestore ==
  /\ previousVersion \in 1..MaxDraftVersion
  /\ ~restoring
  /\ requestedVersion' = previousVersion
  /\ restoring' = TRUE
  /\ lastAction' = "StartRestore"
  /\ UNCHANGED <<currentVersion, previousVersion, restoredVersion>>

RestoreComplete ==
  /\ restoring
  /\ requestedVersion \in 1..MaxDraftVersion
  /\ requestedVersion = previousVersion
  /\ currentVersion' = requestedVersion
  /\ previousVersion' = 0
  /\ requestedVersion' = 0
  /\ restoredVersion' = requestedVersion
  /\ restoring' = FALSE
  /\ lastAction' = "RestoreComplete"

CancelRestore ==
  /\ restoring
  /\ requestedVersion' = 0
  /\ restoring' = FALSE
  /\ lastAction' = "CancelRestore"
  /\ UNCHANGED <<currentVersion, previousVersion, restoredVersion>>

StaleDraft(version) ==
  /\ restoring
  /\ version \in 1..MaxDraftVersion
  /\ version # currentVersion
  /\ currentVersion' = version
  /\ previousVersion' = currentVersion
  /\ requestedVersion' = 0
  /\ restoredVersion' = restoredVersion
  /\ restoring' = FALSE
  /\ lastAction' = "StaleDraft"

Next ==
  \/ \E version \in 1..MaxDraftVersion: SaveDraft(version)
  \/ StartRestore
  \/ RestoreComplete
  \/ CancelRestore
  \/ \E version \in 1..MaxDraftVersion: StaleDraft(version)

Spec == Init /\ [][Next]_vars

Inv_RestoreRequestMatchesPrevious ==
  restoring => requestedVersion = previousVersion

Inv_RestoreUsesRequestedRevision ==
  lastAction = "RestoreComplete" =>
    /\ restoredVersion = currentVersion
    /\ restoredVersion \in 1..MaxDraftVersion

Inv_RestoreClearsRequest ==
  lastAction \in {"RestoreComplete", "CancelRestore", "StaleDraft"} =>
    /\ ~restoring
    /\ requestedVersion = 0

Inv_DraftVersionsBounded ==
  /\ currentVersion \in 0..MaxDraftVersion
  /\ previousVersion \in OptionalVersions

====
