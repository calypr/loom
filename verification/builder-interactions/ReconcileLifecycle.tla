---- MODULE ReconcileLifecycle ----
EXTENDS Naturals, TLC

MaxRetry == 2
MaxStaleRefresh == 2

RequestKeys == {"A", "B"}
RequestStatuses == {"idle", "requesting", "accepted", "rejected", "ignored"}
ReceiptIds == {"NONE", "R1", "R2"}
OutputIds == {"O1", "O2"}
WorkspaceKeys == {"W1", "W2", "W3", "W4"}

WorkspaceFor(key) ==
  IF key = "W1" THEN [
    draft |-> 1,
    digest |-> "D1",
    snapshot |-> "S1",
    outputKey |-> "O1",
    explorer |-> "E1",
    fingerprint |-> "F0"
  ] ELSE IF key = "W2" THEN [
    draft |-> 2,
    digest |-> "D2",
    snapshot |-> "S1",
    outputKey |-> "O1",
    explorer |-> "E1",
    fingerprint |-> "F1"
  ] ELSE IF key = "W3" THEN [
    draft |-> 1,
    digest |-> "D1",
    snapshot |-> "S1",
    outputKey |-> "O1",
    explorer |-> "E1",
    fingerprint |-> "F1"
  ] ELSE [
    draft |-> 1,
    digest |-> "D1",
    snapshot |-> "S1",
    outputKey |-> "O1",
    explorer |-> "E2",
    fingerprint |-> "F0"
  ]

EmptyRequest == [status |-> "idle", workspace |-> "W1"]
EmptyRequests == [key \in RequestKeys |-> EmptyRequest]
EmptyReceipt == [id |-> "NONE", requestKey |-> "NONE", workspace |-> "W1", outputKey |-> "O1"]
EmptyReturned == [requestKey |-> "NONE", id |-> "NONE", workspace |-> "W1", outputKey |-> "O1"]

VARIABLES r, lastAction

vars == <<r, lastAction>>

TypeOK ==
  /\ r.currentWorkspace \in WorkspaceKeys
  /\ r.activeRequest \in RequestKeys \cup {"NONE"}
  /\ r.requestLedger \in [RequestKeys -> [status : RequestStatuses, workspace : WorkspaceKeys]]
  /\ r.phase \in {"idle", "requesting", "succeeded", "stale", "conflict", "invalid", "failed"}
  /\ r.errorClass \in {"none", "stale", "conflict", "invalid", "retryable", "fatal"}
  /\ r.receipt.id \in ReceiptIds
  /\ r.receipt.requestKey \in RequestKeys \cup {"NONE"}
  /\ r.receipt.workspace \in WorkspaceKeys
  /\ r.receipt.outputKey \in OutputIds
  /\ r.lastReturned.requestKey \in RequestKeys \cup {"NONE"}
  /\ r.lastReturned.id \in ReceiptIds
  /\ r.lastReturned.workspace \in WorkspaceKeys
  /\ r.lastReturned.outputKey \in OutputIds
  /\ r.retryCount \in 0..MaxRetry
  /\ r.staleRefreshCount \in 0..MaxStaleRefresh
  /\ r.tablesRooted \in BOOLEAN
  /\ r.snapshotPresent \in BOOLEAN
  /\ lastAction \in {
       "Init",
       "RequestReconcile(A)", "RequestReconcile(B)",
       "RetryReconcile(A)", "RetryReconcile(B)",
       "ReconcileSuccess(A)", "ReconcileSuccess(B)",
       "LateReconcileIgnored(A)", "LateReconcileIgnored(B)",
       "ReconcileStale", "ReconcileConflict", "ReconcileInvalid",
       "ReconcileFailure(retryable)", "ReconcileFailure(fatal)",
       "WorkspaceChangeStartsReconcile(B)",
       "DraftChange", "AuthoredChange", "ChangeExplorer",
       "SetTablesRooted", "SetSnapshotPresent", "ClearReceipt"
     }

ReceiptFor(requestKey) == IF requestKey = "A" THEN "R1" ELSE "R2"
RequestActionLabel(requestKey) == IF requestKey = "A" THEN "RequestReconcile(A)" ELSE "RequestReconcile(B)"
RetryActionLabel(requestKey) == IF requestKey = "A" THEN "RetryReconcile(A)" ELSE "RetryReconcile(B)"
SuccessActionLabel(requestKey) == IF requestKey = "A" THEN "ReconcileSuccess(A)" ELSE "ReconcileSuccess(B)"
LateActionLabel(requestKey) == IF requestKey = "A" THEN "LateReconcileIgnored(A)" ELSE "LateReconcileIgnored(B)"

RequestMatchesCurrent(requestKey) ==
  r.requestLedger[requestKey].workspace = r.currentWorkspace

Init ==
  /\ r = [
       currentWorkspace |-> "W1",
       activeRequest |-> "NONE",
       requestLedger |-> EmptyRequests,
       phase |-> "idle",
       errorClass |-> "none",
       receipt |-> EmptyReceipt,
       lastReturned |-> EmptyReturned,
       retryCount |-> 0,
       staleRefreshCount |-> 0,
       tablesRooted |-> TRUE,
       snapshotPresent |-> TRUE
     ]
  /\ lastAction = "Init"

RequestReconcile(requestKey) ==
  /\ requestKey \in RequestKeys
  /\ r.phase = "idle"
  /\ r.activeRequest = "NONE"
  /\ r.requestLedger[requestKey].status = "idle"
  /\ r.tablesRooted
  /\ r.snapshotPresent
  /\ r' = [r EXCEPT
       !.requestLedger[requestKey] = [status |-> "requesting", workspace |-> r.currentWorkspace],
       !.activeRequest = requestKey,
       !.phase = "requesting",
       !.errorClass = "none",
       !.receipt = EmptyReceipt,
       !.retryCount = 0,
       !.staleRefreshCount = 0
     ]
  /\ lastAction' = RequestActionLabel(requestKey)

RetryReconcile ==
  LET requestKey == r.activeRequest
  IN
    /\ requestKey \in RequestKeys
    /\ \/ (r.phase = "stale" /\ r.staleRefreshCount < MaxStaleRefresh)
       \/ (r.phase = "failed" /\ r.errorClass = "retryable" /\ r.retryCount < MaxRetry)
    /\ r' = [r EXCEPT
         !.requestLedger[requestKey] = [status |-> "requesting", workspace |-> r.currentWorkspace],
         !.phase = "requesting",
         !.errorClass = "none",
         !.receipt = EmptyReceipt,
         !.retryCount = IF r.phase = "failed" THEN r.retryCount + 1 ELSE r.retryCount,
         !.staleRefreshCount = IF r.phase = "stale" THEN r.staleRefreshCount + 1 ELSE r.staleRefreshCount
       ]
    /\ lastAction' = RetryActionLabel(requestKey)

ReconcileSuccess(requestKey) ==
  /\ requestKey \in RequestKeys
  /\ r.activeRequest = requestKey
  /\ r.requestLedger[requestKey].status = "requesting"
  /\ RequestMatchesCurrent(requestKey)
  /\ r' = [r EXCEPT
       !.requestLedger[requestKey].status = "accepted",
       !.phase = "succeeded",
       !.errorClass = "none",
       !.receipt = [id |-> ReceiptFor(requestKey), requestKey |-> requestKey,
                    workspace |-> r.currentWorkspace,
                    outputKey |-> WorkspaceFor(r.currentWorkspace).outputKey],
       !.lastReturned = [requestKey |-> requestKey, id |-> ReceiptFor(requestKey),
                         workspace |-> r.currentWorkspace,
                         outputKey |-> WorkspaceFor(r.currentWorkspace).outputKey]
     ]
  /\ lastAction' = SuccessActionLabel(requestKey)

LateReconcileIgnored(requestKey) ==
  /\ requestKey \in RequestKeys
  /\ r.requestLedger[requestKey].status = "requesting"
  /\ \/ r.activeRequest # requestKey
     \/ ~RequestMatchesCurrent(requestKey)
  /\ r' = [r EXCEPT
       !.requestLedger[requestKey].status = "ignored",
       !.lastReturned = [requestKey |-> requestKey, id |-> ReceiptFor(requestKey),
                         workspace |-> r.requestLedger[requestKey].workspace,
                         outputKey |-> WorkspaceFor(r.requestLedger[requestKey].workspace).outputKey]
     ]
  /\ lastAction' = LateActionLabel(requestKey)

ReconcileStale ==
  /\ r.activeRequest \in RequestKeys
  /\ r.phase = "requesting"
  /\ r.requestLedger[r.activeRequest].status = "requesting"
  /\ r' = [r EXCEPT
       !.requestLedger[r.activeRequest].status = "rejected",
       !.phase = "stale",
       !.errorClass = "stale",
       !.receipt = EmptyReceipt
     ]
  /\ lastAction' = "ReconcileStale"

ReconcileConflict ==
  /\ r.activeRequest \in RequestKeys
  /\ r.phase = "requesting"
  /\ r.requestLedger[r.activeRequest].status = "requesting"
  /\ r' = [r EXCEPT
       !.requestLedger[r.activeRequest].status = "rejected",
       !.phase = "conflict",
       !.errorClass = "conflict",
       !.receipt = EmptyReceipt
     ]
  /\ lastAction' = "ReconcileConflict"

ReconcileInvalid ==
  /\ r.activeRequest \in RequestKeys
  /\ r.phase = "requesting"
  /\ r.requestLedger[r.activeRequest].status = "requesting"
  /\ r' = [r EXCEPT
       !.requestLedger[r.activeRequest].status = "rejected",
       !.phase = "invalid",
       !.errorClass = "invalid",
       !.receipt = EmptyReceipt
     ]
  /\ lastAction' = "ReconcileInvalid"

ReconcileFailure(retryable) ==
  /\ retryable \in BOOLEAN
  /\ r.activeRequest \in RequestKeys
  /\ r.phase = "requesting"
  /\ r.requestLedger[r.activeRequest].status = "requesting"
  /\ r' = [r EXCEPT
       !.requestLedger[r.activeRequest].status = "rejected",
       !.phase = "failed",
       !.errorClass = IF retryable THEN "retryable" ELSE "fatal",
       !.receipt = EmptyReceipt
     ]
  /\ lastAction' = IF retryable
       THEN "ReconcileFailure(retryable)"
       ELSE "ReconcileFailure(fatal)"

WorkspaceChangeStartsReconcile ==
  /\ r.currentWorkspace = "W1"
  /\ r.requestLedger["A"].status = "requesting"
  /\ r.requestLedger["B"].status = "idle"
  /\ r.tablesRooted
  /\ r.snapshotPresent
  /\ r' = [r EXCEPT
       !.currentWorkspace = "W2",
       !.activeRequest = "B",
       !.requestLedger["B"] = [status |-> "requesting", workspace |-> "W2"],
       !.phase = "requesting",
       !.errorClass = "none",
       !.receipt = EmptyReceipt,
       !.retryCount = 0,
       !.staleRefreshCount = 0
     ]
  /\ lastAction' = "WorkspaceChangeStartsReconcile(B)"

DraftChange ==
  /\ r.currentWorkspace # "W2"
  /\ r' = [r EXCEPT
       !.currentWorkspace = "W2",
       !.activeRequest = "NONE",
       !.phase = "idle",
       !.errorClass = "none",
       !.receipt = EmptyReceipt,
       !.retryCount = 0,
       !.staleRefreshCount = 0
     ]
  /\ lastAction' = "DraftChange"

AuthoredChange ==
  /\ r.currentWorkspace = "W1"
  /\ r' = [r EXCEPT
       !.currentWorkspace = "W3",
       !.activeRequest = "NONE",
       !.phase = "idle",
       !.errorClass = "none",
       !.receipt = EmptyReceipt,
       !.retryCount = 0,
       !.staleRefreshCount = 0
     ]
  /\ lastAction' = "AuthoredChange"

ChangeExplorer ==
  /\ r.currentWorkspace = "W1"
  /\ r' = [r EXCEPT
       !.currentWorkspace = "W4",
       !.activeRequest = "NONE",
       !.phase = "idle",
       !.errorClass = "none",
       !.receipt = EmptyReceipt,
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
       !.activeRequest = "NONE",
       !.phase = "idle",
       !.errorClass = "none",
       !.receipt = EmptyReceipt,
       !.retryCount = 0,
       !.staleRefreshCount = 0
     ]
  /\ lastAction' = "ClearReceipt"

Next ==
  \/ \E requestKey \in RequestKeys: RequestReconcile(requestKey)
  \/ RetryReconcile
  \/ \E requestKey \in RequestKeys: ReconcileSuccess(requestKey)
  \/ \E requestKey \in RequestKeys: LateReconcileIgnored(requestKey)
  \/ ReconcileStale
  \/ ReconcileConflict
  \/ ReconcileInvalid
  \/ \E retryable \in BOOLEAN: ReconcileFailure(retryable)
  \/ WorkspaceChangeStartsReconcile
  \/ DraftChange
  \/ AuthoredChange
  \/ ChangeExplorer
  \/ \E value \in BOOLEAN: SetTablesRooted(value)
  \/ \E value \in BOOLEAN: SetSnapshotPresent(value)
  \/ ClearReceipt

Spec == Init /\ [][Next]_vars

Inv_RequestMatchesCurrent ==
  lastAction \in {"RequestReconcile(A)", "RequestReconcile(B)", "RetryReconcile(A)", "RetryReconcile(B)"} =>
    RequestMatchesCurrent(r.activeRequest)

Inv_ActiveRequestMatchesCurrent ==
  r.activeRequest \in RequestKeys => RequestMatchesCurrent(r.activeRequest)

Inv_SuccessReceiptMatchesCurrent ==
  r.phase = "succeeded" =>
    /\ r.receipt.id # "NONE"
    /\ r.receipt.requestKey = r.activeRequest
    /\ r.receipt.workspace = r.currentWorkspace
    /\ r.receipt.outputKey = WorkspaceFor(r.currentWorkspace).outputKey
    /\ RequestMatchesCurrent(r.receipt.requestKey)

Inv_LateResponseDoesNotReplaceCurrentReceipt ==
  lastAction = "LateReconcileIgnored(A)" =>
    /\ r.receipt.id # "R1"
    /\ r.lastReturned.requestKey = "A"
    /\ r.lastReturned.id = "R1"
    /\ (r.receipt.id # "NONE" =>
         /\ r.receipt.requestKey = "B"
         /\ r.receipt.workspace = r.currentWorkspace
         /\ r.receipt.outputKey = WorkspaceFor(r.currentWorkspace).outputKey)

Inv_FailureHasNoReceipt ==
  r.phase \in {"stale", "conflict", "invalid", "failed"} => r.receipt.id = "NONE"

Inv_DraftChangeInvalidatesReceipt ==
  lastAction \in {"DraftChange", "AuthoredChange", "ChangeExplorer"} =>
    /\ r.receipt.id = "NONE"
    /\ r.phase = "idle"

Inv_WorkspaceChangeStartsReconcileEmpty ==
  lastAction = "WorkspaceChangeStartsReconcile(B)" =>
    /\ r.receipt.id = "NONE"
    /\ r.phase = "requesting"
    /\ r.activeRequest = "B"

Inv_RetryBounded ==
  /\ r.retryCount <= MaxRetry
  /\ r.staleRefreshCount <= MaxStaleRefresh

Inv_ReceiptOnlyWhenSucceeded ==
  r.receipt.id # "NONE" => r.phase = "succeeded"

Inv_ReconcileRequiresRootedTables ==
  r.phase = "requesting" =>
    /\ r.tablesRooted
    /\ r.snapshotPresent
    /\ r.activeRequest \in RequestKeys

====
