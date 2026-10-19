---- MODULE SupportingSurfaces ----
EXTENDS Naturals, TLC

VARIABLES s, lastAction

vars == <<s, lastAction>>

TypeOK ==
  /\ s.reviewPhase \in {"idle", "focused"}
  /\ s.reviewTarget \in {"NONE", "TABLE", "ROW", "COLUMN"}
  /\ s.reviewWrites \in BOOLEAN
  /\ s.graphShowOrphans \in BOOLEAN
  /\ s.graphExpanded \in BOOLEAN
  /\ s.graphInspected \in {"NONE", "N1", "N2"}
  /\ s.graphInline \in BOOLEAN
  /\ s.framePhase \in {"idle", "confirming-replace", "confirming-remove", "replaced", "removed", "canceled"}
  /\ s.frameActive \in {"F1"}
  /\ s.frameReplacement \in {"F2"}
  /\ s.frameColumnsOwned \in BOOLEAN
  /\ s.frameConfirmed \in BOOLEAN
  /\ s.allowNewSource = FALSE
  /\ s.rowPhase \in {"idle", "loading", "ready", "unavailable"}
  /\ s.requestedRow \in {"NONE", "R1", "R2"}
  /\ s.responseRow \in {"NONE", "R1", "R2"}
  /\ s.pageOffset \in 0..2
  /\ s.pageLimit = 25
  /\ s.rowComplete \in BOOLEAN
  /\ lastAction \in {
       "Init", "FocusReview", "ToggleGraphOrphans", "ExpandGraph",
       "InspectGraphNode", "BeginFrameReplace", "BeginFrameRemove",
       "ConfirmFrameChange", "ApplyFrameReplace", "ApplyFrameRemove",
       "CancelFrameChange", "RequestRowSources", "RowSourcesReady",
       "RowSourcesUnavailable", "NextLineagePage"
     }

Init ==
  /\ s = [
       reviewPhase |-> "idle",
       reviewTarget |-> "NONE",
       reviewWrites |-> FALSE,
       graphShowOrphans |-> FALSE,
       graphExpanded |-> FALSE,
       graphInspected |-> "NONE",
       graphInline |-> FALSE,
       framePhase |-> "idle",
       frameActive |-> "F1",
       frameReplacement |-> "F2",
       frameColumnsOwned |-> FALSE,
       frameConfirmed |-> FALSE,
       allowNewSource |-> FALSE,
       rowPhase |-> "idle",
       requestedRow |-> "NONE",
       responseRow |-> "NONE",
       pageOffset |-> 0,
       pageLimit |-> 25,
       rowComplete |-> FALSE
     ]
  /\ lastAction = "Init"

FocusReview(target) ==
  /\ target \in {"TABLE", "ROW", "COLUMN"}
  /\ s' = [s EXCEPT
       !.reviewPhase = "focused",
       !.reviewTarget = target,
       !.reviewWrites = FALSE
     ]
  /\ lastAction' = "FocusReview"

ToggleGraphOrphans(value) ==
  /\ value \in BOOLEAN
  /\ s' = [s EXCEPT !.graphShowOrphans = value]
  /\ lastAction' = "ToggleGraphOrphans"

ExpandGraph(value) ==
  /\ value \in BOOLEAN
  /\ s' = [s EXCEPT !.graphExpanded = value]
  /\ lastAction' = "ExpandGraph"

InspectGraphNode(node) ==
  /\ node \in {"N1", "N2"}
  /\ s' = [s EXCEPT !.graphInspected = node, !.graphInline = TRUE]
  /\ lastAction' = "InspectGraphNode"

BeginFrameReplace ==
  /\ s' = [s EXCEPT
       !.framePhase = "confirming-replace",
       !.frameConfirmed = FALSE
     ]
  /\ lastAction' = "BeginFrameReplace"

BeginFrameRemove ==
  /\ s' = [s EXCEPT
       !.framePhase = "confirming-remove",
       !.frameConfirmed = FALSE
     ]
  /\ lastAction' = "BeginFrameRemove"

ConfirmFrameChange ==
  /\ s.framePhase \in {"confirming-replace", "confirming-remove"}
  /\ s' = [s EXCEPT !.frameConfirmed = TRUE]
  /\ lastAction' = "ConfirmFrameChange"

ApplyFrameReplace ==
  /\ s.framePhase = "confirming-replace"
  /\ s.frameConfirmed
  /\ s' = [s EXCEPT !.framePhase = "replaced"]
  /\ lastAction' = "ApplyFrameReplace"

ApplyFrameRemove ==
  /\ s.framePhase = "confirming-remove"
  /\ s.frameConfirmed
  /\ s' = [s EXCEPT !.framePhase = "removed"]
  /\ lastAction' = "ApplyFrameRemove"

CancelFrameChange ==
  /\ s.framePhase \in {"confirming-replace", "confirming-remove"}
  /\ s' = [s EXCEPT
       !.framePhase = "canceled",
       !.frameConfirmed = FALSE
     ]
  /\ lastAction' = "CancelFrameChange"

RequestRowSources(row) ==
  /\ row \in {"R1", "R2"}
  /\ s' = [s EXCEPT
       !.rowPhase = "loading",
       !.requestedRow = row,
       !.responseRow = "NONE",
       !.pageOffset = 0,
       !.rowComplete = FALSE
     ]
  /\ lastAction' = "RequestRowSources"

RowSourcesReady(row, complete) ==
  /\ s.rowPhase = "loading"
  /\ row = s.requestedRow
  /\ complete \in BOOLEAN
  /\ s' = [s EXCEPT
       !.rowPhase = "ready",
       !.responseRow = row,
       !.rowComplete = complete
     ]
  /\ lastAction' = "RowSourcesReady"

RowSourcesUnavailable(row) ==
  /\ s.rowPhase = "loading"
  /\ row = s.requestedRow
  /\ s' = [s EXCEPT
       !.rowPhase = "unavailable",
       !.responseRow = "NONE",
       !.rowComplete = FALSE
     ]
  /\ lastAction' = "RowSourcesUnavailable"

NextLineagePage ==
  /\ s.rowPhase = "ready"
  /\ s.pageOffset < 2
  /\ s' = [s EXCEPT !.pageOffset = s.pageOffset + 1]
  /\ lastAction' = "NextLineagePage"

Next ==
  \/ \E target \in {"TABLE", "ROW", "COLUMN"}: FocusReview(target)
  \/ \E value \in BOOLEAN: ToggleGraphOrphans(value)
  \/ \E value \in BOOLEAN: ExpandGraph(value)
  \/ \E node \in {"N1", "N2"}: InspectGraphNode(node)
  \/ BeginFrameReplace
  \/ BeginFrameRemove
  \/ ConfirmFrameChange
  \/ ApplyFrameReplace
  \/ ApplyFrameRemove
  \/ CancelFrameChange
  \/ \E row \in {"R1", "R2"}: RequestRowSources(row)
  \/ \E row \in {"R1", "R2"}, complete \in BOOLEAN: RowSourcesReady(row, complete)
  \/ \E row \in {"R1", "R2"}: RowSourcesUnavailable(row)
  \/ NextLineagePage

Spec == Init /\ [][Next]_vars

Inv_ReviewNeverWrites ==
  s.reviewWrites = FALSE

Inv_ReviewFocusTarget ==
  s.reviewPhase = "focused" => s.reviewTarget # "NONE"

Inv_FrameApplyRequiresConfirmation ==
  s.framePhase \in {"replaced", "removed"} => s.frameConfirmed

Inv_FrameReplacementBoundToActive ==
  s.framePhase = "replaced" =>
    /\ s.frameActive = "F1"
    /\ s.frameReplacement = "F2"

Inv_NoNewFrameSourceWhenDisabled ==
  ~s.allowNewSource

Inv_RowSourcesBoundToRequest ==
  s.rowPhase = "ready" => s.responseRow = s.requestedRow

Inv_UnavailableRowSourcesNotComplete ==
  s.rowPhase = "unavailable" => ~s.rowComplete

Inv_RowPageLimit ==
  s.pageLimit = 25

Inv_GraphInspectionInline ==
  s.graphInspected # "NONE" => s.graphInline

====
