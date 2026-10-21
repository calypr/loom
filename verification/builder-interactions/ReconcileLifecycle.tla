---- MODULE ReconcileLifecycle ----
EXTENDS Naturals, TLC

\* Canonical reconcile request and response model.
\* Entry gating and command lifecycle remain in LifecycleEntry.tla.

MaxDraft == 2
MaxRetry == 2
MaxStaleRefresh == 2

Drafts == 0..MaxDraft
Digests == {"D0", "D1", "D2"}
Snapshots == {"S1", "S2"}
Outputs == {"O1", "O2"}
Explorers == {"E1", "E2"}
Fingerprints == {"F0", "F1"}
ReceiptIds == {"NONE", "R1"}

VARIABLES r, lastAction

vars == <<r, lastAction>>

TypeOK ==
  /\ r.currentDraft \in Drafts
  /\ r.currentDigest \in Digests
  /\ r.currentSnapshot \in Snapshots
  /\ r.currentOutput \in Outputs
  /\ r.currentExplorer \in Explorers
  /\ r.currentFingerprint \in Fingerprints
  /\ r.requestDraft \in Drafts
  /\ r.requestDigest \in Digests
  /\ r.requestSnapshot \in Snapshots
  /\ r.requestOutput \in Outputs
  /\ r.requestExplorer \in Explorers
  /\ r.requestFingerprint \in Fingerprints
  /\ r.phase \in {"idle", "requesting", "succeeded", "stale", "conflict", "invalid", "failed"}
  /\ r.errorClass \in {"none", "stale", "conflict", "invalid", "retryable", "fatal"}
  /\ r.receipt.id \in ReceiptIds
  /\ r.receipt.draft \in Drafts
  /\ r.receipt.digest \in Digests
  /\ r.receipt.snapshot \in Snapshots
  /\ r.receipt.output \in Outputs
  /\ r.receipt.explorer \in Explorers
  /\ r.receipt.fingerprint \in Fingerprints
  /\ r.retryCount \in 0..MaxRetry
  /\ r.staleRefreshCount \in 0..MaxStaleRefresh
  /\ r.tablesRooted \in BOOLEAN
  /\ r.snapshotPresent \in BOOLEAN
  /\ lastAction \in {
       "Init", "RequestReconcile", "RetryReconcile", "ReconcileSuccess",
       "ReconcileStale", "ReconcileConflict", "ReconcileInvalid",
       "ReconcileFailure(retryable)", "ReconcileFailure(fatal)",
       "DraftChange", "AuthoredChange", "ChangeExplorer",
       "SetTablesRooted", "SetSnapshotPresent", "ClearReceipt"
     }

EmptyReceipt == [
  id |-> "NONE",
  draft |-> 0,
  digest |-> "D0",
  snapshot |-> "S1",
  output |-> "O1",
  explorer |-> "E1",
  fingerprint |-> "F0"
]

Init ==
  /\ r = [
       currentDraft |-> 0,
       currentDigest |-> "D0",
       currentSnapshot |-> "S1",
       currentOutput |-> "O1",
       currentExplorer |-> "E1",
       currentFingerprint |-> "F0",
       requestDraft |-> 0,
       requestDigest |-> "D0",
       requestSnapshot |-> "S1",
       requestOutput |-> "O1",
       requestExplorer |-> "E1",
       requestFingerprint |-> "F0",
       phase |-> "idle",
       errorClass |-> "none",
       receipt |-> EmptyReceipt,
       retryCount |-> 0,
       staleRefreshCount |-> 0,
       tablesRooted |-> TRUE,
       snapshotPresent |-> TRUE
     ]
  /\ lastAction = "Init"

RequestReconcile ==
  /\ r.phase = "idle"
  /\ r.tablesRooted
  /\ r.snapshotPresent
  /\ r' = [r EXCEPT
       !.requestDraft = r.currentDraft,
       !.requestDigest = r.currentDigest,
       !.requestSnapshot = r.currentSnapshot,
       !.requestOutput = r.currentOutput,
       !.requestExplorer = r.currentExplorer,
       !.requestFingerprint = r.currentFingerprint,
       !.phase = "requesting",
       !.errorClass = "none",
       !.receipt = [r.receipt EXCEPT !.id = "NONE"],
       !.retryCount = 0,
       !.staleRefreshCount = 0
     ]
  /\ lastAction' = "RequestReconcile"

RetryReconcile ==
  /\ \/ (r.phase = "stale" /\ r.staleRefreshCount < MaxStaleRefresh)
     \/ (r.phase = "failed" /\ r.errorClass = "retryable" /\ r.retryCount < MaxRetry)
  /\ r' = [r EXCEPT
       !.requestDraft = r.currentDraft,
       !.requestDigest = r.currentDigest,
       !.requestSnapshot = r.currentSnapshot,
       !.requestOutput = r.currentOutput,
       !.requestExplorer = r.currentExplorer,
       !.requestFingerprint = r.currentFingerprint,
       !.phase = "requesting",
       !.errorClass = "none",
       !.receipt = [r.receipt EXCEPT !.id = "NONE"],
       !.retryCount = IF r.phase = "failed" THEN r.retryCount + 1 ELSE r.retryCount,
       !.staleRefreshCount = IF r.phase = "stale" THEN r.staleRefreshCount + 1 ELSE r.staleRefreshCount
     ]
  /\ lastAction' = "RetryReconcile"

ReconcileSuccess(receipt) ==
  /\ r.phase = "requesting"
  /\ r.requestDraft = r.currentDraft
  /\ r.requestDigest = r.currentDigest
  /\ r.requestSnapshot = r.currentSnapshot
  /\ r.requestOutput = r.currentOutput
  /\ r.requestExplorer = r.currentExplorer
  /\ r.requestFingerprint = r.currentFingerprint
  /\ receipt \in {"R1"}
  /\ r' = [r EXCEPT
       !.phase = "succeeded",
       !.errorClass = "none",
       !.receipt = [
         id |-> receipt,
         draft |-> r.currentDraft,
         digest |-> r.currentDigest,
         snapshot |-> r.currentSnapshot,
         output |-> r.currentOutput,
         explorer |-> r.currentExplorer,
         fingerprint |-> r.currentFingerprint
       ]
     ]
  /\ lastAction' = "ReconcileSuccess"

ReconcileStale ==
  /\ r.phase = "requesting"
  /\ r' = [r EXCEPT
       !.phase = "stale",
       !.errorClass = "stale",
       !.receipt = [r.receipt EXCEPT !.id = "NONE"]
     ]
  /\ lastAction' = "ReconcileStale"

ReconcileConflict ==
  /\ r.phase = "requesting"
  /\ r' = [r EXCEPT
       !.phase = "conflict",
       !.errorClass = "conflict",
       !.receipt = [r.receipt EXCEPT !.id = "NONE"]
     ]
  /\ lastAction' = "ReconcileConflict"

ReconcileInvalid ==
  /\ r.phase = "requesting"
  /\ r' = [r EXCEPT
       !.phase = "invalid",
       !.errorClass = "invalid",
       !.receipt = [r.receipt EXCEPT !.id = "NONE"]
     ]
  /\ lastAction' = "ReconcileInvalid"

ReconcileFailure(retryable) ==
  /\ retryable \in BOOLEAN
  /\ r.phase = "requesting"
  /\ r' = [r EXCEPT
       !.phase = "failed",
       !.errorClass = IF retryable THEN "retryable" ELSE "fatal",
       !.receipt = [r.receipt EXCEPT !.id = "NONE"]
     ]
  /\ lastAction' = IF retryable
       THEN "ReconcileFailure(retryable)"
       ELSE "ReconcileFailure(fatal)"

DraftChange(draft, digest) ==
  /\ draft \in 1..MaxDraft
  /\ digest \in {"D1", "D2"}
  /\ r' = [r EXCEPT
       !.currentDraft = draft,
       !.currentDigest = digest,
       !.phase = "idle",
       !.errorClass = "none",
       !.receipt = [r.receipt EXCEPT !.id = "NONE"],
       !.retryCount = 0,
       !.staleRefreshCount = 0
     ]
  /\ lastAction' = "DraftChange"

AuthoredChange(fingerprint) ==
  /\ fingerprint \in Fingerprints
  /\ r' = [r EXCEPT
       !.currentFingerprint = fingerprint,
       !.phase = "idle",
       !.errorClass = "none",
       !.receipt = [r.receipt EXCEPT !.id = "NONE"],
       !.retryCount = 0,
       !.staleRefreshCount = 0
     ]
  /\ lastAction' = "AuthoredChange"

ChangeExplorer(explorer) ==
  /\ explorer \in Explorers
  /\ r' = [r EXCEPT
       !.currentExplorer = explorer,
       !.phase = "idle",
       !.errorClass = "none",
       !.receipt = [r.receipt EXCEPT !.id = "NONE"],
       !.retryCount = 0,
       !.staleRefreshCount = 0
     ]
  /\ lastAction' = "ChangeExplorer"

SetTablesRooted(value) ==
  /\ value \in BOOLEAN
  /\ r.phase = "idle"
  /\ r' = [r EXCEPT !.tablesRooted = value]
  /\ lastAction' = "SetTablesRooted"

SetSnapshotPresent(value) ==
  /\ value \in BOOLEAN
  /\ r.phase = "idle"
  /\ r' = [r EXCEPT !.snapshotPresent = value]
  /\ lastAction' = "SetSnapshotPresent"

ClearReceipt ==
  /\ r' = [r EXCEPT
       !.phase = "idle",
       !.errorClass = "none",
       !.receipt = [r.receipt EXCEPT !.id = "NONE"],
       !.retryCount = 0,
       !.staleRefreshCount = 0
     ]
  /\ lastAction' = "ClearReceipt"

Next ==
  \/ RequestReconcile
  \/ RetryReconcile
  \/ \E receipt \in {"R1"}: ReconcileSuccess(receipt)
  \/ ReconcileStale
  \/ ReconcileConflict
  \/ ReconcileInvalid
  \/ \E retryable \in BOOLEAN: ReconcileFailure(retryable)
  \/ \E draft \in 1..MaxDraft, digest \in {"D1", "D2"}: DraftChange(draft, digest)
  \/ \E fingerprint \in Fingerprints: AuthoredChange(fingerprint)
  \/ \E explorer \in Explorers: ChangeExplorer(explorer)
  \/ \E value \in BOOLEAN: SetTablesRooted(value)
  \/ \E value \in BOOLEAN: SetSnapshotPresent(value)
  \/ ClearReceipt

Spec == Init /\ [][Next]_vars

Inv_RequestMatchesCurrent ==
  lastAction \in {"RequestReconcile", "RetryReconcile"} =>
    /\ r.requestDraft = r.currentDraft
    /\ r.requestDigest = r.currentDigest
    /\ r.requestSnapshot = r.currentSnapshot
    /\ r.requestOutput = r.currentOutput
    /\ r.requestExplorer = r.currentExplorer
    /\ r.requestFingerprint = r.currentFingerprint

Inv_SuccessReceiptMatchesCurrent ==
  r.phase = "succeeded" =>
    /\ r.receipt.id # "NONE"
    /\ r.receipt.draft = r.currentDraft
    /\ r.receipt.digest = r.currentDigest
    /\ r.receipt.snapshot = r.currentSnapshot
    /\ r.receipt.output = r.currentOutput
    /\ r.receipt.explorer = r.currentExplorer
    /\ r.receipt.fingerprint = r.currentFingerprint

Inv_FailureHasNoReceipt ==
  r.phase \in {"stale", "conflict", "invalid", "failed"} => r.receipt.id = "NONE"

Inv_DraftChangeInvalidatesReceipt ==
  lastAction \in {"DraftChange", "AuthoredChange", "ChangeExplorer"} =>
    /\ r.receipt.id = "NONE"
    /\ r.phase = "idle"

Inv_RetryBounded ==
  /\ r.retryCount <= MaxRetry
  /\ r.staleRefreshCount <= MaxStaleRefresh

Inv_ReceiptOnlyWhenSucceeded ==
  r.receipt.id # "NONE" => r.phase = "succeeded"

Inv_ReconcileRequiresRootedTables ==
  r.phase = "requesting" =>
    /\ r.tablesRooted
    /\ r.snapshotPresent

====
