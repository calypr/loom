---- MODULE LifecycleEntry ----
\* Entry and command gating model. Reconcile request identity and outcomes live in ReconcileLifecycle.tla.
EXTENDS Naturals, TLC

VARIABLES e, lastAction

vars == <<e, lastAction>>

TypeOK ==
  /\ e.mode \in {"demo", "edit"}
  /\ e.project \in {"P1"}
  /\ e.explorer \in {"E1", "E2"}
  /\ e.selectionRevision \in {"NONE", "S1"}
  /\ e.featureFocus \in {"NONE", "F1"}
  /\ e.focusOutput \in {"NONE", "O1"}
  /\ e.focusColumn \in {"NONE", "C1"}
  /\ e.loadPhase \in {"idle", "loading", "ready", "failed"}
  /\ e.routeGeneration \in 0..2
  /\ e.selectionGeneration \in 0..2
  /\ e.draftVersion \in 0..3
  /\ e.serverVersion \in 0..3
  /\ e.digest \in {"D0", "D1", "D2"}
  /\ e.serverDigest \in {"D0", "D1", "D2"}
  /\ e.commandStatus \in {"idle", "sending", "saved", "stale"}
  /\ e.requestVersion \in 0..3
  /\ e.committedVersion \in 0..3
  /\ e.reconcileStatus \in {"idle", "checking", "resolved", "repair"}
  /\ e.receiptFingerprint \in {"NONE", "F0", "F1"}
  /\ e.currentFingerprint \in {"F0", "F1"}
  /\ e.receiptExplorer \in {"NONE", "E1", "E2"}
  /\ e.currentExplorer \in {"E1", "E2"}
  /\ e.retryCount \in 0..3
  /\ e.tablesRooted \in BOOLEAN
  /\ e.snapshotPresent \in BOOLEAN
  /\ lastAction \in {
       "Init", "ParseRoute", "SelectionReady", "SelectionFailed",
       "ReplaceExplorer", "SetFeatureFocus", "StartCommand",
       "CommandSuccess", "CommandStale", "GateReconcile",
       "ReconcileReceiptBound", "ReconcileNeedsRepair", "AuthoredChange",
       "RetryEntryReconcile"
     }

Init ==
  /\ e = [
       mode |-> "demo",
       project |-> "P1",
       explorer |-> "E1",
       selectionRevision |-> "NONE",
       featureFocus |-> "NONE",
       focusOutput |-> "NONE",
       focusColumn |-> "NONE",
       loadPhase |-> "idle",
       routeGeneration |-> 0,
       selectionGeneration |-> 0,
       draftVersion |-> 0,
       serverVersion |-> 0,
       digest |-> "D0",
       serverDigest |-> "D0",
       commandStatus |-> "idle",
       requestVersion |-> 0,
       committedVersion |-> 0,
       reconcileStatus |-> "idle",
       receiptFingerprint |-> "NONE",
       currentFingerprint |-> "F0",
       receiptExplorer |-> "NONE",
       currentExplorer |-> "E1",
       retryCount |-> 0,
       tablesRooted |-> TRUE,
       snapshotPresent |-> TRUE
     ]
  /\ lastAction = "Init"

ParseRoute(mode, explorer, selectionRevision) ==
  /\ mode \in {"demo", "edit"}
  /\ explorer \in {"E1", "E2"}
  /\ selectionRevision \in {"NONE", "S1"}
  /\ e.routeGeneration < 2
  /\ e' = [e EXCEPT
       !.mode = mode,
       !.explorer = explorer,
       !.selectionRevision = selectionRevision,
       !.routeGeneration = e.routeGeneration + 1,
       !.selectionGeneration = e.routeGeneration + 1,
       !.loadPhase = IF selectionRevision = "NONE" THEN "idle" ELSE "loading",
       !.currentExplorer = explorer,
       !.receiptFingerprint = "NONE",
       !.receiptExplorer = "NONE",
       !.reconcileStatus = "idle"
     ]
  /\ lastAction' = "ParseRoute"

SelectionReady(generation) ==
  /\ e.loadPhase = "loading"
  /\ generation = e.selectionGeneration
  /\ e' = [e EXCEPT !.loadPhase = "ready"]
  /\ lastAction' = "SelectionReady"

SelectionFailed(generation) ==
  /\ e.loadPhase = "loading"
  /\ generation = e.selectionGeneration
  /\ e' = [e EXCEPT !.loadPhase = "failed"]
  /\ lastAction' = "SelectionFailed"

ReplaceExplorer(explorer) ==
  /\ explorer \in {"E1", "E2"}
  /\ e.routeGeneration < 2
  /\ e' = [e EXCEPT
       !.explorer = explorer,
       !.currentExplorer = explorer,
       !.selectionRevision = "NONE",
       !.loadPhase = "idle",
       !.routeGeneration = e.routeGeneration + 1,
       !.selectionGeneration = e.routeGeneration + 1,
       !.receiptFingerprint = "NONE",
       !.receiptExplorer = "NONE",
       !.reconcileStatus = "idle"
     ]
  /\ lastAction' = "ReplaceExplorer"

SetFeatureFocus(output, column) ==
  /\ output \in {"NONE", "O1"}
  /\ column \in {"NONE", "C1"}
  /\ e' = [e EXCEPT
       !.focusOutput = output,
       !.focusColumn = column,
       !.featureFocus = IF output # "NONE" /\ column # "NONE" THEN "F1" ELSE "NONE"
     ]
  /\ lastAction' = "SetFeatureFocus"

StartCommand(version) ==
  /\ version \in 0..3
  /\ version = e.draftVersion
  /\ e' = [e EXCEPT
       !.commandStatus = "sending",
       !.requestVersion = version
     ]
  /\ lastAction' = "StartCommand"

CommandSuccess(version, digest) ==
  /\ e.commandStatus = "sending"
  /\ e.requestVersion = e.draftVersion
  /\ version \in 1..3
  /\ digest \in {"D1", "D2"}
  /\ e' = [e EXCEPT
       !.draftVersion = version,
       !.serverVersion = version,
       !.digest = digest,
       !.serverDigest = digest,
       !.committedVersion = version,
       !.commandStatus = "saved",
       !.receiptFingerprint = "NONE",
       !.reconcileStatus = "idle"
     ]
  /\ lastAction' = "CommandSuccess"

CommandStale ==
  /\ e.commandStatus = "sending"
  /\ e' = [e EXCEPT
       !.draftVersion = e.serverVersion,
       !.digest = e.serverDigest,
       !.commandStatus = "stale",
       !.receiptFingerprint = "NONE",
       !.reconcileStatus = "idle"
     ]
  /\ lastAction' = "CommandStale"

GateReconcile ==
  /\ e.tablesRooted
  /\ e.snapshotPresent
  /\ e.draftVersion = e.committedVersion
  /\ e' = [e EXCEPT
       !.reconcileStatus = "checking",
       !.retryCount = 0
     ]
  /\ lastAction' = "GateReconcile"

ReconcileReceiptBound(fingerprint) ==
  /\ e.reconcileStatus = "checking"
  /\ fingerprint = e.currentFingerprint
  /\ e' = [e EXCEPT
       !.reconcileStatus = "resolved",
       !.receiptFingerprint = fingerprint,
       !.receiptExplorer = e.currentExplorer
     ]
  /\ lastAction' = "ReconcileReceiptBound"

ReconcileNeedsRepair ==
  /\ e.reconcileStatus = "checking"
  /\ e' = [e EXCEPT
       !.reconcileStatus = "repair",
       !.receiptFingerprint = "NONE"
     ]
  /\ lastAction' = "ReconcileNeedsRepair"

AuthoredChange(fingerprint) ==
  /\ fingerprint \in {"F0", "F1"}
  /\ e' = [e EXCEPT
       !.currentFingerprint = fingerprint,
       !.receiptFingerprint = "NONE",
       !.receiptExplorer = "NONE",
       !.reconcileStatus = "idle"
     ]
  /\ lastAction' = "AuthoredChange"

RetryEntryReconcile ==
  /\ e.reconcileStatus = "repair"
  /\ e.retryCount < 3
  /\ e' = [e EXCEPT
       !.reconcileStatus = "checking",
       !.retryCount = e.retryCount + 1
     ]
  /\ lastAction' = "RetryEntryReconcile"

Next ==
  \/ \E mode \in {"demo", "edit"}, explorer \in {"E1", "E2"}, selectionRevision \in {"NONE", "S1"}:
       ParseRoute(mode, explorer, selectionRevision)
  \/ \E generation \in 0..2: SelectionReady(generation)
  \/ \E generation \in 0..2: SelectionFailed(generation)
  \/ \E explorer \in {"E1", "E2"}: ReplaceExplorer(explorer)
  \/ \E output \in {"NONE", "O1"}, column \in {"NONE", "C1"}: SetFeatureFocus(output, column)
  \/ \E version \in 0..3: StartCommand(version)
  \/ \E version \in 1..3, digest \in {"D1", "D2"}: CommandSuccess(version, digest)
  \/ CommandStale
  \/ GateReconcile
  \/ \E fingerprint \in {"F0", "F1"}: ReconcileReceiptBound(fingerprint)
  \/ ReconcileNeedsRepair
  \/ \E fingerprint \in {"F0", "F1"}: AuthoredChange(fingerprint)
  \/ RetryEntryReconcile

Spec == Init /\ [][Next]_vars

Inv_SelectionLoadedOnlyWithRevision ==
  e.loadPhase = "ready" => e.selectionRevision # "NONE"

Inv_ReadySelectionMatchesGeneration ==
  e.loadPhase = "ready" => e.selectionGeneration = e.routeGeneration

Inv_FocusRequiresBothParameters ==
  e.featureFocus # "NONE" =>
    /\ e.focusOutput # "NONE"
    /\ e.focusColumn # "NONE"

Inv_CommandUsesCurrentDraft ==
  lastAction = "StartCommand" => e.requestVersion = e.draftVersion

Inv_CommandAdvancesServerIdentity ==
  e.commandStatus = "saved" =>
    /\ e.draftVersion = e.serverVersion
    /\ e.digest = e.serverDigest
    /\ e.committedVersion = e.draftVersion

Inv_StaleReloadsAuthoritative ==
  lastAction = "CommandStale" =>
    /\ e.draftVersion = e.serverVersion
    /\ e.digest = e.serverDigest

Inv_ReceiptCurrentOnlyForFingerprintAndExplorer ==
  e.reconcileStatus = "resolved" =>
    /\ e.receiptFingerprint = e.currentFingerprint
    /\ e.receiptExplorer = e.currentExplorer

Inv_ReconcileRequiresRootedTables ==
  e.reconcileStatus = "checking" =>
    /\ e.tablesRooted
    /\ e.snapshotPresent

Inv_RetryBounded ==
  e.retryCount <= 3

Inv_AuthoredChangeInvalidatesReceipt ==
  lastAction = "AuthoredChange" =>
    /\ e.receiptFingerprint = "NONE"
    /\ e.reconcileStatus = "idle"

====
